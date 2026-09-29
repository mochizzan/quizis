package uploads

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Meta is the session record persisted as meta.json. The first block is
// written at initiate; MIME/Ext/CompletedAt are added by a successful
// complete. yyyy/mm of the session dir is derived from CreatedAt — the
// server time at initiate — never from time.Now() at a later step.
type Meta struct {
	UploadID    string     `json:"upload_id"`
	UserID      uint64     `json:"user_id"`
	Total       int        `json:"total"`
	Size        int64      `json:"size"`
	SHA256      string     `json:"sha256"`
	ChunkSize   int        `json:"chunk_size"`
	CreatedAt   time.Time  `json:"created_at"`
	MIME        string     `json:"mime,omitempty"`
	Ext         string     `json:"ext,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Session is the initiate response payload.
type Session struct {
	UploadID  string    `json:"upload_id"`
	ChunkSize int       `json:"chunk_size"`
	Total     int       `json:"total"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ChunkAck is the per-chunk response payload.
type ChunkAck struct {
	Index    int `json:"index"`
	Received int `json:"received"`
	Total    int `json:"total"`
}

// Result is the complete response payload; Path is relative to the root.
type Result struct {
	Path   string `json:"path"`
	MIME   string `json:"mime"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// chunkRe matches completed chunk parts (chunk.%06d) — .tmp files and any
// other stray entry never count toward `received` or the byte totals.
var chunkRe = regexp.MustCompile(`^chunk\.\d{6}$`)

// Store owns the on-disk upload sessions. One mutex serializes every
// session operation (sweep, chunk write, complete, delete) — low
// concurrency by design, no per-session lock map.
type Store struct {
	mu   sync.Mutex
	root string
	// db backs the sweep's linked set and the delete guard. nil skips the
	// DB lookups (unit-test seam; production wiring always passes the pool).
	// A failing DB never yields an empty linked set — the sweep aborts that
	// month instead of deleting referenced images.
	db *sql.DB
}

// New returns a store rooted at root ("" → DefaultRoot).
func New(root string, db *sql.DB) *Store {
	if root == "" {
		root = DefaultRoot
	}
	return &Store{root: root, db: db}
}

// Root is the storage root the session paths are relative to.
func (s *Store) Root() string { return s.root }

// monthOf is the yyyy/mm layout segment for a timestamp.
func monthOf(t time.Time) string { return t.Format("2006/01") }

// candidateMonths is the month of now plus the month of now-SessionTTL
// (deduplicated — at most 2 directories, the same window the sweep scans).
func candidateMonths(now time.Time) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 2)
	for _, t := range []time.Time{now, now.Add(-SessionTTL)} {
		if m := monthOf(t); !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// relDir builds the slash-separated dir path recorded in
// question_images.path and returned by complete — derived from the meta's
// CreatedAt ("path always uses meta's values").
func relDir(m *Meta) string {
	return "questions/" + monthOf(m.CreatedAt) + "/" + m.UploadID
}

// findMeta loads <root>/questions/<yyyy>/<mm>/<id>/meta.json by scanning
// the candidate months for now (sessions live at most SessionTTL, so their
// month is always one of them). Callers hold the mutex.
func (s *Store) findMeta(id string) (meta *Meta, dir string, found bool, err error) {
	for _, ym := range candidateMonths(time.Now()) {
		d := filepath.Join(s.root, "questions", filepath.FromSlash(ym), id)
		b, rerr := os.ReadFile(filepath.Join(d, "meta.json"))
		switch {
		case rerr == nil:
			var m Meta
			if jerr := json.Unmarshal(b, &m); jerr != nil {
				return nil, "", false, fmt.Errorf("meta.json for %s: %w", id, jerr)
			}
			return &m, d, true, nil
		case errors.Is(rerr, os.ErrNotExist):
			continue
		default:
			return nil, "", false, rerr
		}
	}
	return nil, "", false, nil
}

// chunkName is the on-disk name of one chunk part.
func chunkName(index int) string { return fmt.Sprintf("chunk.%06d", index) }

// listChunks returns the completed chunk parts of a session dir. Callers
// hold the mutex.
func listChunks(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	parts := make([]os.DirEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && chunkRe.MatchString(e.Name()) {
			parts = append(parts, e)
		}
	}
	return parts, nil
}

// chunkTotal sums the completed chunk parts, excluding `except` (so an
// idempotent retransmit of chunk i is counted as a replacement, not an
// addition). Callers hold the mutex.
func chunkTotal(dir string, except int) (int64, error) {
	parts, err := listChunks(dir)
	if err != nil {
		return 0, err
	}
	var sum int64
	for _, p := range parts {
		if p.Name() == chunkName(except) {
			continue
		}
		info, err := p.Info()
		if err != nil {
			return 0, err
		}
		sum += info.Size()
	}
	return sum, nil
}

// writeFileAtomic writes data to a tmp sibling then renames it into place.
func writeFileAtomic(dst string, data []byte) error {
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func writeMeta(dir string, m *Meta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "meta.json"), b)
}

// escapeLike escapes LIKE metacharacters in a literal path prefix so an
// upload id's "_" never matches a foreign character (MySQL default escape
// is '\').
func escapeLike(lit string) string {
	lit = strings.ReplaceAll(lit, `\`, `\\`)
	lit = strings.ReplaceAll(lit, `%`, `\%`)
	return strings.ReplaceAll(lit, `_`, `\_`)
}

// linkedNames loads the question_images references for one month prefix and
// returns the referenced session-dir names (the path segment after the
// month). A query failure is an error — never an empty set, or the sweep
// would delete referenced images.
func (s *Store) linkedNames(ym string) (map[string]bool, error) {
	linked := map[string]bool{}
	if s.db == nil {
		return linked, nil
	}
	prefix := "questions/" + ym + "/"
	rows, err := s.db.Query(`SELECT path FROM question_images WHERE path LIKE ?`,
		escapeLike(prefix)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(p, prefix)
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[:i]
		}
		if name != "" {
			linked[name] = true
		}
	}
	return linked, rows.Err()
}

// isLinked reports whether a completed session's path is already
// referenced by question_images (delete guard → 409).
func (s *Store) isLinked(dirRel string) (bool, error) {
	if s.db == nil {
		return false, nil
	}
	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM question_images WHERE path LIKE ?)`,
		escapeLike(dirRel)+"/%").Scan(&exists)
	return exists, err
}

// Sweep removes expired, unlinked session dirs from the month of now and
// of now-SessionTTL (2 directories max). Per month: ReadDir, DB-load the
// linked set, plan (PlanExpired), RemoveAll. It takes the store mutex.
// Failures are collected per month — a failing month is skipped entirely
// (an aborted DB query must never become "nothing is linked") and the
// first error is returned for the caller to log; cleanup never blocks a
// new session.
func (s *Store) Sweep() error {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for _, ym := range candidateMonths(now) {
		if err := s.sweepMonth(now, ym); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Store) sweepMonth(now time.Time, ym string) error {
	monthDir := filepath.Join(s.root, "questions", filepath.FromSlash(ym))
	entries, err := os.ReadDir(monthDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	infos := make([]DirInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		infos = append(infos, DirInfo{Name: e.Name(), ModTime: info.ModTime()})
	}
	if len(infos) == 0 {
		return nil
	}
	linked, err := s.linkedNames(ym)
	if err != nil {
		return err
	}
	for _, name := range PlanExpired(now, SessionTTL, infos, linked) {
		if !ValidID(name) {
			continue // never RemoveAll a name that is not one of ours
		}
		if err := os.RemoveAll(filepath.Join(monthDir, name)); err != nil {
			return err
		}
	}
	return nil
}

// Initiate creates a fresh session dir + meta.json for the declaration the
// caller already validated (ValidateInitiate). The caller runs Sweep first
// (spec: sweep at the start of initiate).
func (s *Store) Initiate(userID uint64, total int, size int64, sha string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	monthDir := filepath.Join(s.root, "questions", filepath.FromSlash(monthOf(now)))
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		return Session{}, ErrIO
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		id, err := NewID()
		if err != nil {
			return Session{}, err
		}
		dir := filepath.Join(monthDir, id)
		err = os.Mkdir(dir, 0o755)
		if errors.Is(err, os.ErrExist) {
			lastErr = err // id collision — regenerate
			continue
		}
		if err != nil {
			return Session{}, ErrIO
		}
		meta := &Meta{
			UploadID:  id,
			UserID:    userID,
			Total:     total,
			Size:      size,
			SHA256:    sha,
			ChunkSize: ChunkSize,
			CreatedAt: now,
		}
		if err := writeMeta(dir, meta); err != nil {
			os.RemoveAll(dir)
			return Session{}, ErrIO
		}
		return Session{
			UploadID:  id,
			ChunkSize: ChunkSize,
			Total:     total,
			ExpiresAt: now.Add(SessionTTL),
		}, nil
	}
	return Session{}, fmt.Errorf("upload id collision: %w", lastErr)
}

// PutChunk validates and stores one chunk, strictly sequential. The whole
// step runs under the store mutex in protocol order: id format → meta
// (expired sessions are removed and treated as missing) → index parse/range
// → order gate → read body at the exact expected size → per-chunk length →
// cumulative cap → atomic write (tmp + rename; a retransmit of the same
// index overwrites idempotently).
//
// indexParam is the raw :index path segment — parsed AFTER the session
// lookup so error precedence matches the protocol. read is invoked with the
// expected byte count; handlers wrap the request body in http.MaxBytesReader
// (expected+4096) and map *http.MaxBytesError to ErrChunkTooLarge, so an
// oversized body fails even when the client lied about its sizes.
func (s *Store) PutChunk(id, indexParam string, read func(expected int) ([]byte, error)) (ChunkAck, error) {
	if !ValidID(id) {
		return ChunkAck{}, ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, dir, found, err := s.findMeta(id)
	if err != nil {
		return ChunkAck{}, ErrIO
	}
	if !found {
		return ChunkAck{}, ErrNotFound
	}
	if time.Since(meta.CreatedAt) > SessionTTL {
		// expired → treated as missing: remove the dir, 404
		os.RemoveAll(dir)
		return ChunkAck{}, ErrNotFound
	}
	index, aerr := strconv.Atoi(indexParam)
	if aerr != nil {
		return ChunkAck{}, ErrInvalidIndex
	}
	received, rerr := countReceived(dir)
	if rerr != nil {
		return ChunkAck{}, ErrIO
	}
	switch ChunkAccept(index, received, meta.Total) {
	case ChunkErrIndex:
		return ChunkAck{}, ErrInvalidIndex
	case ChunkErrOrder:
		return ChunkAck{}, ErrOrder
	}
	expected := ExpectedChunkSize(meta.Size, index)
	body, err := read(expected)
	if err != nil {
		return ChunkAck{}, err // ErrChunkTooLarge or an envelope-shaped respError
	}
	if len(body) != expected {
		return ChunkAck{}, ErrChunkSize
	}
	// defense-in-depth: cumulative cap (retransmits replace, not add)
	prior, err := chunkTotal(dir, index)
	if err != nil {
		return ChunkAck{}, ErrIO
	}
	if prior+int64(len(body)) > MaxImageBytes {
		return ChunkAck{}, ErrSizeLimit
	}
	if err := writeFileAtomic(filepath.Join(dir, chunkName(index)), body); err != nil {
		return ChunkAck{}, ErrIO
	}
	received, err = countReceived(dir)
	if err != nil {
		return ChunkAck{}, ErrIO
	}
	return ChunkAck{Index: index, Received: received, Total: meta.Total}, nil
}

// countReceived is the number of completed chunk parts on disk. Callers
// hold the mutex.
func countReceived(dir string) (int, error) {
	parts, err := listChunks(dir)
	if err != nil {
		return 0, err
	}
	return len(parts), nil
}

// Complete assembles the chunks in index order through sha256, verifies
// the digest and the sniffed MIME type, then renames original.tmp →
// original.<ext>, drops the chunks and persists mime/ext/completed_at.
// A completed session re-complete is idempotent: it returns the stored
// payload without touching the disk. Assemble/verify failures delete the
// tmp but KEEP the chunks (the client can retry or restart).
func (s *Store) Complete(id string) (Result, error) {
	if !ValidID(id) {
		return Result{}, ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, dir, found, err := s.findMeta(id)
	if err != nil {
		return Result{}, ErrIO
	}
	if !found {
		return Result{}, ErrNotFound
	}
	if meta.Ext != "" {
		orig := filepath.Join(dir, "original."+meta.Ext)
		if st, serr := os.Stat(orig); serr == nil && !st.IsDir() {
			return Result{
				Path:   relDir(meta) + "/original." + meta.Ext,
				MIME:   meta.MIME,
				Size:   meta.Size,
				SHA256: meta.SHA256,
			}, nil
		}
	}
	// every index 0..N-1 must be present before assembling
	for i := range meta.Total {
		if _, err := os.Stat(filepath.Join(dir, chunkName(i))); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Result{}, ErrIncomplete
			}
			return Result{}, ErrIO
		}
	}
	tmp := filepath.Join(dir, "original.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return Result{}, ErrIO
	}
	h := sha256.New()
	var assembled int64
	for i := range meta.Total {
		cf, cerr := os.Open(filepath.Join(dir, chunkName(i)))
		if cerr != nil {
			f.Close()
			os.Remove(tmp)
			if errors.Is(cerr, os.ErrNotExist) {
				return Result{}, ErrIncomplete
			}
			return Result{}, ErrIO
		}
		n, cerr := io.Copy(io.MultiWriter(f, h), cf)
		cf.Close()
		if cerr != nil {
			f.Close()
			os.Remove(tmp)
			return Result{}, ErrIO
		}
		assembled += n
		if assembled > MaxImageBytes {
			f.Close()
			os.Remove(tmp)
			return Result{}, ErrSizeLimit
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return Result{}, ErrIO
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if digest != meta.SHA256 {
		os.Remove(tmp) // KEEP the chunks — the client may resend
		return Result{}, ErrCorrupted
	}
	head, err := readHead(tmp, 512)
	if err != nil {
		os.Remove(tmp)
		return Result{}, ErrIO
	}
	mime, ext, ok := SniffImage(head)
	if !ok {
		os.Remove(tmp) // KEEP the chunks — the client may restart
		return Result{}, ErrUnsupported
	}
	orig := filepath.Join(dir, "original."+ext)
	if err := os.Rename(tmp, orig); err != nil {
		os.Remove(tmp)
		return Result{}, ErrIO
	}
	// best-effort chunk cleanup; the sweep reclaims leftovers eventually
	if parts, gerr := filepath.Glob(filepath.Join(dir, "chunk.*")); gerr == nil {
		for _, p := range parts {
			os.Remove(p)
		}
	}
	completed := time.Now()
	meta.MIME = mime
	meta.Ext = ext
	meta.CompletedAt = &completed
	if err := writeMeta(dir, meta); err != nil {
		return Result{}, ErrIO
	}
	return Result{
		Path:   relDir(meta) + "/original." + ext,
		MIME:   mime,
		Size:   assembled,
		SHA256: digest,
	}, nil
}

// Remove cancels/cleans up a session (whole dir). A COMPLETED session whose
// path is already referenced by question_images is never removed
// (ErrAttachedConflict → 409 CONFLICT).
// VerifyLink validates the hidden form field (a path returned by Complete)
// before it becomes a question_images row. Everything is re-derived from
// server state — the reference format, the server-written meta.json, the
// on-disk size and a full sha256 re-hash — and an upload may be attached
// exactly once (the DB has no constraint on path; this is where the rule
// lives). Row values come from meta, never from the form.
func (s *Store) VerifyLink(ref string) (Result, error) {
	if !ValidRefPath(ref) {
		return Result{}, ErrInvalidRef
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file := filepath.Join(s.root, filepath.FromSlash(ref))
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "meta.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, ErrImageGone
		}
		return Result{}, ErrIO
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Result{}, ErrIO
	}
	if m.Ext == "" || m.MIME == "" {
		return Result{}, ErrImageGone // upload never reached Complete
	}
	if ref != relDir(&m)+"/original."+m.Ext || !AllowedMIME(m.MIME) {
		return Result{}, ErrInvalidRef // month/id/ext disagree with meta
	}
	st, err := os.Stat(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, ErrImageGone
		}
		return Result{}, ErrIO
	}
	if st.IsDir() || st.Size() != m.Size || m.Size > MaxImageBytes {
		return Result{}, ErrImageChanged
	}
	f, err := os.Open(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, ErrImageGone
		}
		return Result{}, ErrIO
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return Result{}, ErrIO
	}
	if hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return Result{}, ErrImageChanged
	}
	linked, err := s.isLinked(relDir(&m))
	if err != nil {
		return Result{}, err
	}
	if linked {
		return Result{}, ErrAttachedConflict
	}
	return Result{Path: ref, MIME: m.MIME, Size: m.Size, SHA256: m.SHA256}, nil
}

func (s *Store) Remove(id string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, dir, found, err := s.findMeta(id)
	if err != nil {
		return ErrIO
	}
	if !found {
		return ErrNotFound
	}
	if meta.Ext != "" {
		linked, err := s.isLinked(relDir(meta))
		if err != nil {
			return err
		}
		if linked {
			return ErrAttachedConflict
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return ErrIO
	}
	return nil
}

// readHead reads up to n leading bytes of a file (MIME sniffing window).
func readHead(p string, n int) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	m, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:m], nil
}
