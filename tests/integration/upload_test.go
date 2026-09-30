package integration

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quiz/internal/cache"
	"quiz/internal/handlers"
	"quiz/internal/testutil"
	"quiz/internal/uploads"
)

// uploadFixture wires the real routes (mirror of cmd/server) with an
// isolated TempDir uploads root — nothing is ever written to the repo.
// Skips when the test database is unreachable (centralized policy).
func uploadFixture(t *testing.T) (*httptest.Server, *sql.DB, string) {
	t.Helper()
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users", "password_resets", "question_images")
	store := cache.New()
	cfg := testutil.Config(t)
	root := t.TempDir()
	teach := &handlers.Teacher{DB: pool, Store: store, UploadsRoot: root}
	ts, _ := newServerWith(t, pool, store, cfg, teach)
	return ts, pool, root
}

// pngBytes builds a deterministic PNG-magic payload of exactly n bytes.
func pngBytes(n int) []byte {
	b := make([]byte, n)
	copy(b, "\x89PNG\r\n\x1a\n")
	for i := len("\x89PNG\r\n\x1a\n"); i < n; i++ {
		b[i] = byte(i % 251)
	}
	return b
}

func shaHexOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// uploadURL builds the endpoint family for one session.
func uploadURL(ts *httptest.Server, id string) string {
	return ts.URL + "/teacher/uploads/" + id
}

// initiateUpload POSTs the declaration and returns the parsed data block.
func initiateUpload(t *testing.T, ts *httptest.Server, ck *http.Cookie,
	total int, size int64, sha string) struct {
	UploadID  string `json:"upload_id"`
	ChunkSize int    `json:"chunk_size"`
	Total     int    `json:"total"`
	ExpiresAt string `json:"expires_at"`
} {
	t.Helper()
	resp, body := postJSON(t, ts.URL+"/teacher/uploads",
		map[string]any{"total": total, "size": size, "sha256": sha}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initiate = %d: %s", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if !env.OK {
		t.Fatalf("initiate envelope: %s", body)
	}
	var data struct {
		UploadID  string `json:"upload_id"`
		ChunkSize int    `json:"chunk_size"`
		Total     int    `json:"total"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("initiate data %q: %v", env.Data, err)
	}
	return data
}

// assertValidation pins one 400 VALIDATION envelope to its exact literal.
func assertValidation(t *testing.T, resp *http.Response, body, wantMsg string) {
	t.Helper()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if env.Error != "VALIDATION" {
		t.Errorf("error = %q, want VALIDATION (body %s)", env.Error, body)
	}
	if env.Message != wantMsg {
		t.Errorf("message = %q, want %q", env.Message, wantMsg)
	}
}

func TestUploadFlowHappyPath(t *testing.T) {
	ts, _, root := uploadFixture(t)
	ck := guruLogin(t, ts)

	payload := pngBytes(600)
	sha := shaHexOf(payload)

	sess := initiateUpload(t, ts, ck, 1, int64(len(payload)), sha)
	if !uploads.ValidID(sess.UploadID) {
		t.Fatalf("upload_id %q is not a valid id", sess.UploadID)
	}
	if sess.ChunkSize != uploads.ChunkSize || sess.Total != 1 {
		t.Errorf("chunk_size/total = %d/%d, want %d/1", sess.ChunkSize, sess.Total, uploads.ChunkSize)
	}
	exp, err := time.Parse(time.RFC3339, sess.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at %q: %v", sess.ExpiresAt, err)
	}
	wantExp := time.Now().Add(uploads.SessionTTL)
	if exp.Before(wantExp.Add(-time.Minute)) || exp.After(wantExp.Add(time.Minute)) {
		t.Errorf("expires_at = %v, want ~%v", exp, wantExp)
	}

	// chunk 0
	resp, body := postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/0",
		"application/octet-stream", string(payload), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chunk = %d: %s", resp.StatusCode, body)
	}
	var ack struct {
		Index    int `json:"index"`
		Received int `json:"received"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal(decodeEnv(t, body).Data, &ack); err != nil {
		t.Fatalf("chunk data %q: %v", body, err)
	}
	if ack.Index != 0 || ack.Received != 1 || ack.Total != 1 {
		t.Errorf("chunk ack = %+v, want {0 1 1}", ack)
	}

	// complete
	resp, body = postJSON(t, uploadURL(ts, sess.UploadID)+"/complete", nil, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete = %d: %s", resp.StatusCode, body)
	}
	var done struct {
		Path string `json:"path"`
		MIME string `json:"mime"`
		Size int64  `json:"size"`
		SHA  string `json:"sha256"`
	}
	if err := json.Unmarshal(decodeEnv(t, body).Data, &done); err != nil {
		t.Fatalf("complete data %q: %v", body, err)
	}
	if done.MIME != "image/png" {
		t.Errorf("mime = %q, want image/png", done.MIME)
	}
	if done.Size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", done.Size, len(payload))
	}
	if done.SHA != sha {
		t.Errorf("sha256 = %q, want %q", done.SHA, sha)
	}
	if !strings.HasPrefix(done.Path, "questions/") || !strings.HasSuffix(done.Path, "/"+sess.UploadID+"/original.png") {
		t.Errorf("path = %q, want questions/<yyyy>/<mm>/%s/original.png", done.Path, sess.UploadID)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(done.Path)))
	if err != nil {
		t.Fatalf("read assembled file: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("assembled file does not match the uploaded bytes")
	}

	// idempotent re-complete returns the stored payload
	resp, body = postJSON(t, uploadURL(ts, sess.UploadID)+"/complete", nil, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-complete = %d: %s", resp.StatusCode, body)
	}
	var again struct {
		Path string `json:"path"`
		SHA  string `json:"sha256"`
	}
	if err := json.Unmarshal(decodeEnv(t, body).Data, &again); err != nil {
		t.Fatalf("re-complete data %q: %v", body, err)
	}
	if again.Path != done.Path || again.SHA != done.SHA {
		t.Errorf("re-complete = %+v, want %+v", again, done)
	}
}

// --- strictly sequential chunks (amendment) -------------------------------

func TestUploadRejectsOutOfOrderChunks(t *testing.T) {
	ts, _, root := uploadFixture(t)
	ck := guruLogin(t, ts)

	payload := pngBytes(uploads.ChunkSize + 64)
	sess := initiateUpload(t, ts, ck, 2, int64(len(payload)), shaHexOf(payload))

	// index 1 before index 0 → a gap is an order violation
	resp, body := postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/1",
		"application/octet-stream", string(payload[uploads.ChunkSize:]), ck)
	assertValidation(t, resp, body, "Bagian harus diunggah secara berurutan.")

	// now sequential: 0 then 1 (index 1 == received 1) → complete works
	resp, body = postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/0",
		"application/octet-stream", string(payload[:uploads.ChunkSize]), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chunk 0 = %d: %s", resp.StatusCode, body)
	}
	var ack struct {
		Index    int `json:"index"`
		Received int `json:"received"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal(decodeEnv(t, body).Data, &ack); err != nil || ack.Received != 1 {
		t.Fatalf("chunk 0 ack = %+v (%s)", ack, body)
	}
	resp, body = postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/1",
		"application/octet-stream", string(payload[uploads.ChunkSize:]), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chunk 1 = %d: %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(decodeEnv(t, body).Data, &ack); err != nil || ack.Received != 2 {
		t.Fatalf("chunk 1 ack = %+v (%s)", ack, body)
	}

	resp, body = postJSON(t, uploadURL(ts, sess.UploadID)+"/complete", nil, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete = %d: %s", resp.StatusCode, body)
	}
	var done struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(decodeEnv(t, body).Data, &done); err != nil {
		t.Fatalf("complete data %q: %v", body, err)
	}
	if done.Size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", done.Size, len(payload))
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(done.Path))); err != nil {
		t.Errorf("assembled file missing: %v", err)
	}
}

// --- validation matrix ----------------------------------------------------

func TestUploadValidation(t *testing.T) {
	ts, _, _ := uploadFixture(t)
	ck := guruLogin(t, ts)

	t.Run("oversize declaration at initiate", func(t *testing.T) {
		resp, body := postJSON(t, ts.URL+"/teacher/uploads", map[string]any{
			"total": 26, "size": int64(uploads.MaxImageBytes) + 1, "sha256": shaHexOf([]byte("x")),
		}, ck)
		assertValidation(t, resp, body, "Gambar melebihi batas 25 MB.")
	})

	t.Run("bad checksum format at initiate", func(t *testing.T) {
		resp, body := postJSON(t, ts.URL+"/teacher/uploads", map[string]any{
			"total": 1, "size": 100, "sha256": "not-a-checksum",
		}, ck)
		assertValidation(t, resp, body, "Checksum tidak valid.")
	})

	t.Run("chunk count mismatch at initiate", func(t *testing.T) {
		resp, body := postJSON(t, ts.URL+"/teacher/uploads", map[string]any{
			"total": 5, "size": 100, "sha256": shaHexOf([]byte("x")),
		}, ck)
		assertValidation(t, resp, body, "Permintaan unggahan tidak valid.")
	})

	t.Run("complete without chunks", func(t *testing.T) {
		payload := pngBytes(600)
		sess := initiateUpload(t, ts, ck, 1, int64(len(payload)), shaHexOf(payload))
		resp, body := postJSON(t, uploadURL(ts, sess.UploadID)+"/complete", nil, ck)
		assertValidation(t, resp, body, "Unggahan tidak lengkap — beberapa bagian hilang.")
	})

	t.Run("out-of-range chunk index", func(t *testing.T) {
		payload := pngBytes(100)
		sess := initiateUpload(t, ts, ck, 1, int64(len(payload)), shaHexOf(payload))
		resp, body := postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/5",
			"application/octet-stream", string(payload), ck)
		assertValidation(t, resp, body, "Indeks bagian tidak valid.")
	})

	t.Run("non-numeric chunk index", func(t *testing.T) {
		payload := pngBytes(100)
		sess := initiateUpload(t, ts, ck, 1, int64(len(payload)), shaHexOf(payload))
		resp, body := postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/zero",
			"application/octet-stream", string(payload), ck)
		assertValidation(t, resp, body, "Indeks bagian tidak valid.")
	})

	t.Run("invalid upload id", func(t *testing.T) {
		bad := strings.Repeat("A", 23)
		resp, body := postRaw(t, ts.URL+"/teacher/uploads/"+bad+"/chunks/0",
			"application/octet-stream", "x", ck)
		assertValidation(t, resp, body, "ID unggahan tidak valid.")
		resp, body = postJSON(t, ts.URL+"/teacher/uploads/"+bad+"/delete", nil, ck)
		assertValidation(t, resp, body, "ID unggahan tidak valid.")
		resp, body = postJSON(t, ts.URL+"/teacher/uploads/"+bad+"/complete", nil, ck)
		assertValidation(t, resp, body, "ID unggahan tidak valid.")
	})

	t.Run("bad magic bytes at complete", func(t *testing.T) {
		text := bytes.Repeat([]byte("This is definitely not an image. "), 20)
		sess := initiateUpload(t, ts, ck, 1, int64(len(text)), shaHexOf(text))
		resp, body := postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/0",
			"application/octet-stream", string(text), ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("chunk = %d: %s", resp.StatusCode, body)
		}
		resp, body = postJSON(t, uploadURL(ts, sess.UploadID)+"/complete", nil, ck)
		assertValidation(t, resp, body, "Jenis gambar tidak didukung — gunakan JPEG, PNG, GIF, atau WebP.")
	})

	t.Run("sha mismatch at complete", func(t *testing.T) {
		payload := pngBytes(600)
		wrong := shaHexOf([]byte("some other bytes entirely"))
		sess := initiateUpload(t, ts, ck, 1, int64(len(payload)), wrong)
		resp, body := postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/0",
			"application/octet-stream", string(payload), ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("chunk = %d: %s", resp.StatusCode, body)
		}
		resp, body = postJSON(t, uploadURL(ts, sess.UploadID)+"/complete", nil, ck)
		assertValidation(t, resp, body, "Berkas rusak saat diunggah — coba lagi.")
	})

	t.Run("unknown session is 404", func(t *testing.T) {
		id, err := uploads.NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		resp, body := postJSON(t, uploadURL(ts, id)+"/complete", nil, ck)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("complete unknown = %d, want 404 (%s)", resp.StatusCode, body)
		}
		if env := decodeEnv(t, body); env.Error != "NOT_FOUND" || env.Message != "Sesi unggahan tidak ditemukan." {
			t.Errorf("envelope = %+v, want NOT_FOUND %q", env, "Upload session not found.")
		}
	})
}

// --- cancel ---------------------------------------------------------------

func TestUploadCancel(t *testing.T) {
	ts, _, root := uploadFixture(t)
	ck := guruLogin(t, ts)

	payload := pngBytes(600)
	sess := initiateUpload(t, ts, ck, 1, int64(len(payload)), shaHexOf(payload))

	resp, body := postJSON(t, uploadURL(ts, sess.UploadID)+"/delete", nil, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); !env.OK || string(env.Data) != "null" {
		t.Errorf("delete envelope = %s, want ok with data null", body)
	}

	// the whole session dir is gone
	dirs, err := filepath.Glob(filepath.Join(root, "questions", "*", "*", sess.UploadID))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("session dir still present: %v", dirs)
	}

	// a later chunk sees a missing session
	resp, body = postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/0",
		"application/octet-stream", string(payload), ck)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("chunk after delete = %d, want 404 (%s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "NOT_FOUND" || env.Message != "Sesi unggahan tidak ditemukan." {
		t.Errorf("envelope = %+v, want NOT_FOUND %q", env, "Upload session not found.")
	}
}

// --- delete guard: attached images are never removed ----------------------

func TestUploadDeleteGuardWhenAttached(t *testing.T) {
	ts, pool, root := uploadFixture(t)
	ck := guruLogin(t, ts)

	payload := pngBytes(600)
	sha := shaHexOf(payload)
	sess := initiateUpload(t, ts, ck, 1, int64(len(payload)), sha)
	if resp, body := postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/0",
		"application/octet-stream", string(payload), ck); resp.StatusCode != http.StatusOK {
		t.Fatalf("chunk = %d: %s", resp.StatusCode, body)
	}
	resp, body := postJSON(t, uploadURL(ts, sess.UploadID)+"/complete", nil, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete = %d: %s", resp.StatusCode, body)
	}
	var done struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(decodeEnv(t, body).Data, &done); err != nil {
		t.Fatalf("complete data %q: %v", body, err)
	}

	// the image gets attached to a question
	if _, err := pool.Exec(`INSERT INTO question_images
		(question_id, filename, path, byte_size, mime_type, sha256)
		VALUES (?, ?, ?, ?, 'image/png', ?)`,
		1, "foto.png", done.Path, len(payload), sha); err != nil {
		t.Fatalf("attach insert: %v", err)
	}

	resp, body = postJSON(t, uploadURL(ts, sess.UploadID)+"/delete", nil, ck)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete attached = %d, want 409 (%s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "CONFLICT" || env.Message != "Gambar sudah terpasang pada pertanyaan." {
		t.Errorf("envelope = %+v, want CONFLICT %q", env, "Gambar sudah terpasang pada pertanyaan.")
	}
	// no deletion happened
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(done.Path))); err != nil {
		t.Errorf("attached file removed: %v", err)
	}
}

// --- expiry + orphan sweep (I/O wrapper around planExpired) ---------------

// sessionDir locates the on-disk session dir without knowing its month.
func sessionDir(t *testing.T, root, id string) string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(root, "questions", "*", "*", id))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("locate session dir %s: err=%v dirs=%v", id, err, dirs)
	}
	return dirs[0]
}

// backdateMeta rewrites meta.json with an older CreatedAt (expiry seam).
func backdateMeta(t *testing.T, dir string, at time.Time) {
	t.Helper()
	p := filepath.Join(dir, "meta.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var m uploads.Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	m.CreatedAt = at
	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("encode meta: %v", err)
	}
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

func TestUploadExpiryAndSweep(t *testing.T) {
	ts, pool, root := uploadFixture(t)
	ck := guruLogin(t, ts)
	payload := pngBytes(600)

	// expired session: a chunk request treats it as missing AND removes it
	sess := initiateUpload(t, ts, ck, 1, int64(len(payload)), shaHexOf(payload))
	dir := sessionDir(t, root, sess.UploadID)
	backdateMeta(t, dir, time.Now().Add(-25*time.Hour))
	resp, body := postRaw(t, uploadURL(ts, sess.UploadID)+"/chunks/0",
		"application/octet-stream", string(payload), ck)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("chunk on expired session = %d, want 404 (%s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "NOT_FOUND" || env.Message != "Sesi unggahan tidak ditemukan." {
		t.Errorf("envelope = %+v, want NOT_FOUND %q", env, "Upload session not found.")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expired session dir not removed: stat err=%v", err)
	}

	// orphan sweep at initiate: expired+unlinked removed, expired+linked
	// and fresh dirs kept
	fresh := initiateUpload(t, ts, ck, 1, int64(len(payload)), shaHexOf(payload))
	freshDir := sessionDir(t, root, fresh.UploadID)

	orphan := initiateUpload(t, ts, ck, 1, int64(len(payload)), shaHexOf(payload))
	orphanDir := sessionDir(t, root, orphan.UploadID)

	linked := initiateUpload(t, ts, ck, 1, int64(len(payload)), shaHexOf(payload))
	linkedDir := sessionDir(t, root, linked.UploadID)
	rel, err := filepath.Rel(root, linkedDir)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	if _, err := pool.Exec(`INSERT INTO question_images
		(question_id, filename, path, byte_size, mime_type, sha256)
		VALUES (?, ?, ?, ?, 'image/png', ?)`,
		77, "kept.png", filepath.ToSlash(rel)+"/original.png", 10, shaHexOf(payload)); err != nil {
		t.Fatalf("insert linked row: %v", err)
	}

	old := time.Now().Add(-25 * time.Hour)
	for _, d := range []string{orphanDir, linkedDir} {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", d, err)
		}
	}

	// this initiate sweeps under the store mutex first
	initiateUpload(t, ts, ck, 1, int64(len(payload)), shaHexOf(payload))

	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Errorf("expired unlinked orphan not swept: stat err=%v", err)
	}
	if _, err := os.Stat(linkedDir); err != nil {
		t.Errorf("linked (attached) dir must NEVER be swept: %v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Errorf("fresh dir must be kept: %v", err)
	}
}

// --- media endpoint -------------------------------------------------------

func TestQuestionImageServing(t *testing.T) {
	ts, pool, root := uploadFixture(t)
	ck := guruLogin(t, ts)

	// a real file under the injected root + a directly inserted row
	now := time.Now()
	id, err := uploads.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	rel := fmt.Sprintf("questions/%s/%s/%s/original.png", now.Format("2006"), now.Format("01"), id)
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	img := pngBytes(600)
	if err := os.WriteFile(abs, img, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	sha := shaHexOf(img)
	res, err := pool.Exec(`INSERT INTO question_images
		(question_id, filename, path, byte_size, mime_type, sha256)
		VALUES (?, ?, ?, ?, 'image/png', ?)`, 42, "foto soal.png", rel, len(img), sha)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	imageID, _ := res.LastInsertId()

	// inactive row → not found
	res, err = pool.Exec(`INSERT INTO question_images
		(question_id, filename, path, byte_size, mime_type, sha256, active)
		VALUES (?, ?, ?, ?, 'image/png', ?, 0)`, 43, "hidden.png", rel, len(img), sha)
	if err != nil {
		t.Fatalf("insert inactive: %v", err)
	}
	inactiveID, _ := res.LastInsertId()

	// unvalidated path → not found (never opened)
	res, err = pool.Exec(`INSERT INTO question_images
		(question_id, filename, path, byte_size, mime_type, sha256)
		VALUES (?, ?, ?, ?, 'image/png', ?)`, 44, "evil.png", "../../etc/passwd", 10, sha)
	if err != nil {
		t.Fatalf("insert evil: %v", err)
	}
	evilID, _ := res.LastInsertId()

	get := func(t *testing.T, imageID any, cookies ...*http.Cookie) (*http.Response, string) {
		t.Helper()
		return getWith(t, fmt.Sprintf("%s/media/question/%v", ts.URL, imageID), cookies...)
	}

	// 200 with the exact header contract + exact bytes
	resp, body := get(t, imageID, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("media = %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if et := resp.Header.Get("ETag"); et != `"`+sha+`"` {
		t.Errorf("ETag = %q, want %q", et, `"`+sha+`"`)
	}
	if ns := resp.Header.Get("X-Content-Type-Options"); ns != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", ns)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `inline; filename="foto soal.png"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if body != string(img) {
		t.Errorf("body = %d bytes, want the %d image bytes", len(body), len(img))
	}

	// If-None-Match hit → 304, body-less, headers intact
	resp, body = getWithHeader(t, fmt.Sprintf("%s/media/question/%d", ts.URL, imageID),
		"If-None-Match", `"`+sha+`"`, ck)
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match = %d, want 304 (%s)", resp.StatusCode, body)
	}
	if body != "" {
		t.Errorf("304 body = %q, want empty", body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("304 Cache-Control = %q, want no-cache", cc)
	}

	// anonymous → 401
	resp, body = get(t, imageID)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401 (%s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "UNAUTHENTICATED" || env.Message != "Masuk untuk melihat gambar ini." {
		t.Errorf("envelope = %+v, want UNAUTHENTICATED %q", env, "Masuk untuk melihat gambar ini.")
	}

	// unknown id → 404
	resp, body = get(t, 99999999, ck)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id = %d, want 404 (%s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "NOT_FOUND" || env.Message != "Gambar tidak ditemukan." {
		t.Errorf("envelope = %+v, want NOT_FOUND %q", env, "Gambar tidak ditemukan.")
	}

	// non-numeric id → 404
	resp, body = get(t, "abc", ck)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-numeric id = %d, want 404 (%s)", resp.StatusCode, body)
	}

	// inactive row → 404
	if resp, body := get(t, inactiveID, ck); resp.StatusCode != http.StatusNotFound {
		t.Errorf("inactive row = %d, want 404 (%s)", resp.StatusCode, body)
	}

	// unvalidated stored path → 404, file untouched
	if resp, body := get(t, evilID, ck); resp.StatusCode != http.StatusNotFound {
		t.Errorf("evil path = %d, want 404 (%s)", resp.StatusCode, body)
	}
}
