package unit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quiz/internal/uploads"
)

// validID22 is a well-formed 22-char upload id (raw URL-safe base64 shape).
const validID22 = "Abc-_0123456789abcdefX" // 22 chars

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// testImage builds a deterministic "PNG": the real 8-byte magic followed by
// deterministic filler (sniffing only inspects the prefix).
func testImage(n int) []byte {
	b := make([]byte, n)
	copy(b, "\x89PNG\r\n\x1a\n")
	for i := len("\x89PNG\r\n\x1a\n"); i < n; i++ {
		b[i] = byte(i % 251)
	}
	return b
}

// --- protocol constants ---------------------------------------------------

// The wire contract: exact protocol constants (frontend builds against
// these literals).
func TestUploadProtocolConstants(t *testing.T) {
	if uploads.MaxImageBytes != 26214400 {
		t.Errorf("MaxImageBytes = %d, want 26214400", uploads.MaxImageBytes)
	}
	if uploads.ChunkSize != 1048576 {
		t.Errorf("ChunkSize = %d, want 1048576", uploads.ChunkSize)
	}
	if uploads.SessionTTL != 24*time.Hour {
		t.Errorf("SessionTTL = %v, want 24h", uploads.SessionTTL)
	}
	if uploads.MaxChunks != 25 {
		t.Errorf("MaxChunks = %d, want 25", uploads.MaxChunks)
	}
	if uploads.DefaultRoot != "data/uploads" {
		t.Errorf("DefaultRoot = %q, want data/uploads", uploads.DefaultRoot)
	}
}

// --- (a) id format --------------------------------------------------------

func TestUploadIDFormat(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"valid 22 chars", validID22, true},
		{"valid all charset", "AZaz09_-AZaz09_-AZaz09", true},
		{"empty", "", false},
		{"too short", "abc", false},
		{"21 chars", strings.Repeat("a", 21), false},
		{"23 chars", strings.Repeat("a", 23), false},
		{"traversal", strings.Repeat("../", 6) + "abcd", false},
		{"absolute path", "/" + strings.Repeat("a", 21), false},
		{"nul byte", strings.Repeat("a", 21) + "\x00", false},
		{"dot segment", strings.Repeat("a", 20) + "..", false},
		{"backslash", strings.Repeat("a", 21) + `\`, false},
		{"space", strings.Repeat("a", 21) + " ", false},
		{"unicode rune", strings.Repeat("a", 20) + "é" + "b", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := uploads.ValidID(c.id); got != c.want {
				t.Errorf("ValidID(%q) = %v, want %v", c.id, got, c.want)
			}
		})
	}
}

func TestNewIDIsValid(t *testing.T) {
	for range 8 {
		id, err := uploads.NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if len(id) != 22 || !uploads.ValidID(id) {
			t.Fatalf("NewID() = %q (len %d), want 22 valid chars", id, len(id))
		}
	}
}

// --- (b) reference-path regex --------------------------------------------

func TestReferencePathRegex(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"jpg", "questions/2026/09/" + validID22 + "/original.jpg", true},
		{"png", "questions/2026/12/" + validID22 + "/original.png", true},
		{"gif", "questions/2025/01/" + validID22 + "/original.gif", true},
		{"webp", "questions/2026/09/" + validID22 + "/original.webp", true},
		{"traversal", "questions/2026/09/../../../etc/passwd/original.png", false},
		{"absolute", "/questions/2026/09/" + validID22 + "/original.png", false},
		{"double slash", "questions//2026/09/" + validID22 + "/original.png", false},
		{"single-digit month", "questions/2026/9/" + validID22 + "/original.png", false},
		{"missing month", "questions/2026/" + validID22 + "/original.png", false},
		{"wrong ext jpeg", "questions/2026/09/" + validID22 + "/original.jpeg", false},
		{"wrong ext bmp", "questions/2026/09/" + validID22 + "/original.bmp", false},
		{"subdir segment", "questions/2026/09/" + validID22 + "/sub/original.png", false},
		{"bad id chars", "questions/2026/09/" + strings.Repeat("a", 21) + "./original.png", false},
		{"backslash separators", `questions\2026\09\` + validID22 + `\original.png`, false},
		{"chunk file", "questions/2026/09/" + validID22 + "/chunk.000000", false},
		{"meta file", "questions/2026/09/" + validID22 + "/meta.json", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := uploads.ValidRefPath(c.path); got != c.want {
				t.Errorf("ValidRefPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

// --- (c) chunk size / index guards / initiate consistency -----------------

func TestExpectedChunkSize(t *testing.T) {
	cases := []struct {
		name  string
		size  int64
		index int
		want  int
	}{
		{"small single chunk", 100, 0, 100},
		{"exact chunk", uploads.ChunkSize, 0, uploads.ChunkSize},
		{"full first + tail", uploads.ChunkSize + 1, 0, uploads.ChunkSize},
		{"full first + tail (last)", uploads.ChunkSize + 1, 1, 1},
		{"cap first chunk", 25 << 20, 0, uploads.ChunkSize},
		{"cap last chunk", 25 << 20, 24, uploads.ChunkSize},
		{"cap middle chunk", 25 << 20, 13, uploads.ChunkSize},
		{"negative index is defensive zero", 100, -1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := uploads.ExpectedChunkSize(c.size, c.index); got != c.want {
				t.Errorf("ExpectedChunkSize(%d, %d) = %d, want %d", c.size, c.index, got, c.want)
			}
		})
	}
}

func TestChunkAcceptSequentialGate(t *testing.T) {
	cases := []struct {
		name     string
		index    int
		received int
		total    int
		want     uploads.ChunkDecision
	}{
		{"first chunk 0", 0, 0, 3, uploads.ChunkOK},
		{"sequential 1", 1, 1, 3, uploads.ChunkOK},
		{"sequential last", 2, 2, 3, uploads.ChunkOK},
		{"retransmit last", 1, 2, 3, uploads.ChunkOK},
		{"retransmit first", 0, 1, 3, uploads.ChunkOK},
		{"gap from zero", 1, 0, 3, uploads.ChunkErrOrder},
		{"gap forward", 2, 1, 3, uploads.ChunkErrOrder},
		{"replay too old", 0, 3, 3, uploads.ChunkErrOrder},
		{"replay two behind", 0, 2, 3, uploads.ChunkErrOrder},
		{"index equals total", 3, 3, 3, uploads.ChunkErrIndex},
		{"index beyond total", 5, 0, 3, uploads.ChunkErrIndex},
		{"single-chunk replay edge", 0, 0, 1, uploads.ChunkOK},
		{"single-chunk second accept impossible", 1, 1, 1, uploads.ChunkErrIndex},
		{"negative index", -1, 0, 3, uploads.ChunkErrIndex},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := uploads.ChunkAccept(c.index, c.received, c.total); got != c.want {
				t.Errorf("ChunkAccept(%d, %d, %d) = %v, want %v", c.index, c.received, c.total, got, c.want)
			}
		})
	}
}

func TestValidateInitiate(t *testing.T) {
	goodSHA := shaHex([]byte("x"))
	cases := []struct {
		name    string
		total   int
		size    int64
		sha     string
		wantErr error // nil = accepted
		wantMsg string
	}{
		{"minimal valid", 1, 1, goodSHA, nil, ""},
		{"cap valid", 25, 25 << 20, goodSHA, nil, ""},
		{"one-byte over cap", 26, 25<<20 + 1, goodSHA, uploads.ErrSizeLimit, "Image exceeds the 25 MB limit."},
		{"way over cap", 30, 1 << 30, goodSHA, uploads.ErrSizeLimit, "Image exceeds the 25 MB limit."},
		{"zero size", 1, 0, goodSHA, uploads.ErrInvalidRequest, "Invalid upload request."},
		{"negative size", 1, -5, goodSHA, uploads.ErrInvalidRequest, "Invalid upload request."},
		{"total below ceil", 1, 25 << 20, goodSHA, uploads.ErrInvalidRequest, "Invalid upload request."},
		{"total above ceil", 3, 25 << 20, goodSHA, uploads.ErrInvalidRequest, "Invalid upload request."},
		{"zero total", 0, 100, goodSHA, uploads.ErrInvalidRequest, "Invalid upload request."},
		{"negative total", -1, 100, goodSHA, uploads.ErrInvalidRequest, "Invalid upload request."},
		{"sha uppercase hex", 1, 100, strings.ToUpper(goodSHA), uploads.ErrInvalidChecksum, "Invalid checksum."},
		{"sha too short", 1, 100, goodSHA[:63], uploads.ErrInvalidChecksum, "Invalid checksum."},
		{"sha too long", 1, 100, goodSHA + "a", uploads.ErrInvalidChecksum, "Invalid checksum."},
		{"sha non-hex", 1, 100, strings.Repeat("g", 64), uploads.ErrInvalidChecksum, "Invalid checksum."},
		{"sha empty", 1, 100, "", uploads.ErrInvalidChecksum, "Invalid checksum."},
		{"sha checked after size", 1, 25<<20 + 1, "", uploads.ErrSizeLimit, "Image exceeds the 25 MB limit."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := uploads.ValidateInitiate(c.total, c.size, c.sha)
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateInitiate = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("ValidateInitiate = %v, want %v", err, c.wantErr)
			}
			if err.Error() != c.wantMsg {
				t.Errorf("message = %q, want %q", err.Error(), c.wantMsg)
			}
		})
	}
}

// --- (d) MIME sniff allowlist --------------------------------------------

func TestSniffImageAllowlist(t *testing.T) {
	pad := bytes.Repeat([]byte{0x2A}, 512)
	cases := []struct {
		name     string
		head     []byte
		wantMIME string
		wantExt  string
		wantOK   bool
	}{
		{"jpeg FFD8FF", append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, pad...), "image/jpeg", "jpg", true},
		{"png 89504E47", append([]byte("\x89PNG\r\n\x1a\n"), pad...), "image/png", "png", true},
		{"gif87a", append([]byte("GIF87a"), pad...), "image/gif", "gif", true},
		{"gif89a", append([]byte("GIF89a"), pad...), "image/gif", "gif", true},
		{"webp RIFF....WEBP", append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), pad...), "image/webp", "webp", true},
		{"reject bmp", append([]byte("BM"), pad...), "image/bmp", "", false},
		{"reject html", append([]byte("<!DOCTYPE html><html></html>"), pad...), "text/html; charset=utf-8", "", false},
		{"reject octet-stream", append([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}, pad...), "application/octet-stream", "", false},
		{"reject svg-as-text", append([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), pad...), "text/plain; charset=utf-8", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mime, ext, ok := uploads.SniffImage(c.head)
			if mime != c.wantMIME {
				t.Errorf("sniffed mime = %q, want %q", mime, c.wantMIME)
			}
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ext != c.wantExt {
				t.Errorf("ext = %q, want %q", ext, c.wantExt)
			}
		})
	}
}

// --- (e) sweep planner ----------------------------------------------------

func TestPlanExpiredSweepPlanner(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ttl := uploads.SessionTTL
	entries := []uploads.DirInfo{
		{Name: "oldunlinked", ModTime: now.Add(-25 * time.Hour)},
		{Name: "oldlinked", ModTime: now.Add(-25 * time.Hour)},
		{Name: "fresh", ModTime: now.Add(-time.Hour)},
		{Name: "boundaryexact", ModTime: now.Add(-ttl)},
		{Name: "justoverboundary", ModTime: now.Add(-ttl - time.Nanosecond)},
	}
	linked := map[string]bool{"oldlinked": true}

	got := uploads.PlanExpired(now, ttl, entries, linked)
	want := []string{"oldunlinked", "justoverboundary"}
	if len(got) != len(want) {
		t.Fatalf("PlanExpired = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("PlanExpired[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}

	// fresh-only input → nothing swept; nil linked is safe
	fresh := []uploads.DirInfo{{Name: "a", ModTime: now.Add(-ttl)}}
	if out := uploads.PlanExpired(now, ttl, fresh, nil); len(out) != 0 {
		t.Errorf("boundary input swept: %v, want none", out)
	}
	// old + linked only → kept
	oldLinked := []uploads.DirInfo{{Name: "a", ModTime: now.Add(-100 * time.Hour)}}
	if out := uploads.PlanExpired(now, ttl, oldLinked, map[string]bool{"a": true}); len(out) != 0 {
		t.Errorf("linked dir swept: %v, want none", out)
	}
}

// --- (f) assembly from a temp root ----------------------------------------

func TestStoreAssembly(t *testing.T) {
	t.Run("multi-chunk concat produces expected bytes and sha", func(t *testing.T) {
		root := t.TempDir()
		st := uploads.New(root, nil)
		payload := testImage(uploads.ChunkSize + 100)
		declared := shaHex(payload)

		sess, err := st.Initiate(7, 2, int64(len(payload)), declared)
		if err != nil {
			t.Fatalf("initiate: %v", err)
		}
		if sess.ChunkSize != uploads.ChunkSize || sess.Total != 2 {
			t.Fatalf("session = %+v, want chunk_size %d total 2", sess, uploads.ChunkSize)
		}
		if want := time.Now().Add(uploads.SessionTTL); sess.ExpiresAt.Before(want.Add(-time.Minute)) ||
			sess.ExpiresAt.After(want.Add(time.Minute)) {
			t.Errorf("expires_at = %v, want ~now+24h", sess.ExpiresAt)
		}

		var expected0, expected1 int
		ack, err := st.PutChunk(sess.UploadID, "0", func(expected int) ([]byte, error) {
			expected0 = expected
			return payload[:uploads.ChunkSize], nil
		})
		if err != nil {
			t.Fatalf("chunk 0: %v", err)
		}
		if ack.Index != 0 || ack.Received != 1 || ack.Total != 2 {
			t.Errorf("chunk 0 ack = %+v, want {0 1 2}", ack)
		}
		if expected0 != uploads.ChunkSize {
			t.Errorf("expected size for chunk 0 = %d, want %d", expected0, uploads.ChunkSize)
		}
		ack, err = st.PutChunk(sess.UploadID, "1", func(expected int) ([]byte, error) {
			expected1 = expected
			return payload[uploads.ChunkSize:], nil
		})
		if err != nil {
			t.Fatalf("chunk 1: %v", err)
		}
		if ack.Received != 2 {
			t.Errorf("chunk 1 received = %d, want 2", ack.Received)
		}
		if expected1 != 100 {
			t.Errorf("expected size for chunk 1 = %d, want 100", expected1)
		}

		res, err := st.Complete(sess.UploadID)
		if err != nil {
			t.Fatalf("complete: %v", err)
		}
		if res.MIME != "image/png" {
			t.Errorf("mime = %q, want image/png", res.MIME)
		}
		if res.Size != int64(len(payload)) {
			t.Errorf("size = %d, want %d", res.Size, len(payload))
		}
		if res.SHA256 != declared {
			t.Errorf("sha256 = %q, want %q", res.SHA256, declared)
		}
		if !strings.HasPrefix(res.Path, "questions/") || !strings.HasSuffix(res.Path, "/"+sess.UploadID+"/original.png") {
			t.Errorf("path = %q, want questions/<yyyy>/<mm>/%s/original.png", res.Path, sess.UploadID)
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(res.Path)))
		if err != nil {
			t.Fatalf("read original: %v", err)
		}
		if !bytes.Equal(data, payload) {
			t.Fatalf("assembled %d bytes do not match the uploaded payload", len(data))
		}
		if parts := globSession(t, root, sess.UploadID, "chunk.*"); len(parts) != 0 {
			t.Errorf("chunks not cleaned up: %v", parts)
		}
		// idempotent re-complete returns the stored payload
		again, err := st.Complete(sess.UploadID)
		if err != nil {
			t.Fatalf("re-complete: %v", err)
		}
		if again != res {
			t.Errorf("re-complete = %+v, want %+v", again, res)
		}
	})

	t.Run("missing chunk detected", func(t *testing.T) {
		root := t.TempDir()
		st := uploads.New(root, nil)
		payload := testImage(uploads.ChunkSize + 100)
		sess, err := st.Initiate(1, 2, int64(len(payload)), shaHex(payload))
		if err != nil {
			t.Fatalf("initiate: %v", err)
		}
		if _, err := st.PutChunk(sess.UploadID, "0", func(int) ([]byte, error) {
			return payload[:uploads.ChunkSize], nil
		}); err != nil {
			t.Fatalf("chunk 0: %v", err)
		}
		if _, err := st.Complete(sess.UploadID); !errors.Is(err, uploads.ErrIncomplete) {
			t.Fatalf("complete = %v, want ErrIncomplete", err)
		}
		if origs := globSession(t, root, sess.UploadID, "original.*"); len(origs) != 0 {
			t.Errorf("original written despite missing chunk: %v", origs)
		}
		// nothing at all → also incomplete (complete-without-chunks)
		sess2, err := st.Initiate(1, 1, 100, shaHex(testImage(100)))
		if err != nil {
			t.Fatalf("initiate 2: %v", err)
		}
		if _, err := st.Complete(sess2.UploadID); !errors.Is(err, uploads.ErrIncomplete) {
			t.Fatalf("complete without chunks = %v, want ErrIncomplete", err)
		}
	})

	t.Run("sha mismatch detected and chunks kept", func(t *testing.T) {
		root := t.TempDir()
		st := uploads.New(root, nil)
		payload := testImage(700)
		declared := shaHex([]byte("different content entirely"))
		sess, err := st.Initiate(1, 1, int64(len(payload)), declared)
		if err != nil {
			t.Fatalf("initiate: %v", err)
		}
		if _, err := st.PutChunk(sess.UploadID, "0", func(int) ([]byte, error) {
			return payload, nil
		}); err != nil {
			t.Fatalf("chunk 0: %v", err)
		}
		if _, err := st.Complete(sess.UploadID); !errors.Is(err, uploads.ErrCorrupted) {
			t.Fatalf("complete = %v, want ErrCorrupted", err)
		}
		if origs := globSession(t, root, sess.UploadID, "original.*"); len(origs) != 0 {
			t.Errorf("original/tmp left behind: %v", origs)
		}
		if parts := globSession(t, root, sess.UploadID, "chunk.000000"); len(parts) != 1 {
			t.Errorf("chunks must be KEPT after a sha mismatch, found %v", parts)
		}
		// the tmp must be gone while the chunks remain
		if tmps := globSession(t, root, sess.UploadID, "*.tmp"); len(tmps) != 0 {
			t.Errorf("tmp files left behind: %v", tmps)
		}
	})
}

// globSession lists files of one session dir without knowing its month
// (the layout is questions/<yyyy>/<mm>/<id>/).
func globSession(t *testing.T, root, id, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "questions", "*", "*", id, pattern))
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	return matches
}
