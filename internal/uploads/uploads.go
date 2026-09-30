// Package uploads implements the server side of the chunked image-upload
// protocol for teacher question images: pure validation/sniffing helpers
// (id format, initiate declaration, sequential order gate, MIME allowlist,
// sweep planner) plus the on-disk session store.
//
// Security invariants: a client filename NEVER enters any path (the
// extension comes only from the server-sniffed MIME type) and every upload
// id is re-validated against ^[A-Za-z0-9_-]{22}$ before any filesystem use.
package uploads

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"time"
)

const (
	// MaxImageBytes is the inclusive upload size cap (25 MB).
	MaxImageBytes = 25 << 20 // 26214400

	// ChunkSize is the fixed chunk payload (1 MiB); the last chunk carries
	// the remainder of the file.
	ChunkSize = 1 << 20 // 1048576

	// MaxChunks is ceil(MaxImageBytes/ChunkSize) — the largest legal chunk
	// count for a 25 MB file.
	MaxChunks = 25

	// SessionTTL bounds a session dir's idle time. The orphan sweep removes
	// dirs untouched for longer: stalled chunk uploads AND completed-but-
	// never-linked images (linked images are excluded via question_images).
	SessionTTL = 24 * time.Hour

	// DefaultRoot is the storage root used when no root is injected (tests
	// inject t.TempDir()). Layout: <root>/questions/<yyyy>/<mm>/<id>/.
	DefaultRoot = "data/uploads"
)

// Protocol errors. Their messages are the exact wire literals shared with
// the frontend — never reword them; handlers map each one onto the JSON
// envelope (see handlers.uploadFail).
var (
	ErrInvalidRequest   = errors.New("Permintaan unggahan tidak valid.")
	ErrSizeLimit        = errors.New("Gambar melebihi batas 25 MB.")
	ErrInvalidChecksum  = errors.New("Checksum tidak valid.")
	ErrInvalidID        = errors.New("ID unggahan tidak valid.")
	ErrNotFound         = errors.New("Sesi unggahan tidak ditemukan.")
	ErrInvalidIndex     = errors.New("Indeks bagian tidak valid.")
	ErrOrder            = errors.New("Bagian harus diunggah secara berurutan.")
	ErrChunkSize        = errors.New("Ukuran bagian tidak valid.")
	ErrChunkTooLarge    = errors.New("Bagian terlalu besar.")
	ErrIncomplete       = errors.New("Unggahan tidak lengkap — beberapa bagian hilang.")
	ErrCorrupted        = errors.New("Berkas rusak saat diunggah — coba lagi.")
	ErrUnsupported      = errors.New("Jenis gambar tidak didukung — gunakan JPEG, PNG, GIF, atau WebP.")
	ErrIO               = errors.New("Tidak dapat menyimpan gambar — silakan coba lagi.")
	ErrAttachedConflict = errors.New("Gambar sudah terpasang pada pertanyaan.")
	// link-time errors (CreateQuestion verifying the hidden image field)
	ErrInvalidRef   = errors.New("Referensi gambar tidak valid.")
	ErrImageGone    = errors.New("Gambar tidak ditemukan — unggah ulang.")
	ErrImageChanged = errors.New("Berkas gambar berubah sejak diunggah — unggah ulang.")
)

// mimeExts is the image allowlist: sniffed MIME → stored extension (the
// extension never comes from the client).
var mimeExts = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// ExtForMIME maps a sniffed MIME type through the image allowlist.
func ExtForMIME(mime string) (string, bool) {
	ext, ok := mimeExts[mime]
	return ext, ok
}

// AllowedMIME reports whether a MIME type is in the image allowlist.
func AllowedMIME(mime string) bool {
	_, ok := mimeExts[mime]
	return ok
}

var (
	// idRe is the upload-id format: exactly 22 raw URL-safe base64 chars
	// (16 crypto/rand bytes). Re-checked before every filesystem use.
	idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	// shaRe is the declared checksum format: 64 lowercase hex chars.
	shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// refPathRe is the ONLY shape a stored question_images.path may have
	// before the media endpoint opens it (spec step 6 defense).
	refPathRe = regexp.MustCompile(`^questions/\d{4}/\d{2}/[A-Za-z0-9_-]{22}/original\.(jpg|png|gif|webp)$`)
)

// NewID returns a fresh upload id: 16 crypto/rand bytes as raw URL-safe
// base64 (always 22 chars).
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// ValidID re-validates an id before any filesystem use — the only thing
// standing between a route param and a path traversal.
func ValidID(id string) bool { return idRe.MatchString(id) }

// ValidRefPath reports whether a stored question_images.path is a
// well-formed relative original-image path.
func ValidRefPath(p string) bool { return refPathRe.MatchString(p) }

// ValidateInitiate enforces the initiate declaration: size within
// [1, MaxImageBytes] (below 1 is a malformed request, above is the limit
// message), total == ceil(size/ChunkSize) inside [1, MaxChunks], and a
// 64-char lowercase hex sha256. Returned messages are the wire literals.
func ValidateInitiate(total int, size int64, sha string) error {
	if size < 1 {
		return ErrInvalidRequest
	}
	if size > MaxImageBytes {
		return ErrSizeLimit
	}
	if total < 1 || total > MaxChunks || int64(total) != (size+ChunkSize-1)/ChunkSize {
		return ErrInvalidRequest
	}
	if !shaRe.MatchString(sha) {
		return ErrInvalidChecksum
	}
	return nil
}

// ChunkDecision is the outcome of the sequential order gate.
type ChunkDecision int

const (
	// ChunkOK — accept: the next expected chunk, or an idempotent
	// retransmit of the last one.
	ChunkOK ChunkDecision = iota
	// ChunkErrIndex — index outside [0, total): "Invalid chunk index."
	ChunkErrIndex
	// ChunkErrOrder — gap or too-old replay: "Chunks must be uploaded in
	// order."
	ChunkErrOrder
)

// ChunkAccept gates a chunk index against the strictly-sequential
// protocol: index must be inside [0,total), then equal received (the next
// chunk) or received-1 with received > 0 (idempotent retransmit of the
// last chunk). Any other index — a gap forward or a replay older than the
// last chunk — is an order violation.
func ChunkAccept(index, received, total int) ChunkDecision {
	if index < 0 || index >= total {
		return ChunkErrIndex
	}
	if index == received || (received > 0 && index == received-1) {
		return ChunkOK
	}
	return ChunkErrOrder
}

// ExpectedChunkSize is the exact byte count accepted for chunk index:
// min(ChunkSize, size-index*ChunkSize). Callers guard the index range.
func ExpectedChunkSize(size int64, index int) int {
	if index < 0 {
		return 0
	}
	remain := size - int64(index)*ChunkSize
	if remain > ChunkSize {
		return ChunkSize
	}
	if remain < 0 {
		return 0
	}
	return int(remain)
}

// SniffImage detects the MIME type of an assembled file's head (first 512
// bytes) and maps it through the allowlist. ok is false for anything
// outside {image/jpeg, image/png, image/gif, image/webp}.
func SniffImage(head []byte) (mime, ext string, ok bool) {
	m := http.DetectContentType(head)
	e, ok := mimeExts[m]
	return m, e, ok
}

// DirInfo is one session-dir candidate for the sweep planner.
type DirInfo struct {
	Name    string
	ModTime time.Time
}

// PlanExpired is the pure orphan-sweep planner: a session dir is swept
// when it has been untouched STRICTLY longer than ttl AND its image is not
// referenced by question_images (linked). The boundary is strict — a dir
// exactly ttl old is kept. Names come from ReadDir of one month directory;
// linked holds the referenced dir names for that month.
func PlanExpired(now time.Time, ttl time.Duration, entries []DirInfo, linked map[string]bool) []string {
	var out []string
	for _, e := range entries {
		if linked[e.Name] {
			continue
		}
		if now.Sub(e.ModTime) > ttl {
			out = append(out, e.Name)
		}
	}
	return out
}
