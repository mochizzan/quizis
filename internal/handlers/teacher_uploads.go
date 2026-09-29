package handlers

import (
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v5"

	mw "quiz/internal/middleware"
	"quiz/internal/uploads"
)

// uploads returns the session store, built once from UploadsRoot (+ the
// teacher's DB) on first use — the zero value stays inert so fixtures that
// never hit the upload routes write nothing to disk.
func (t *Teacher) uploads() *uploads.Store {
	t.uploadsOnce.Do(func() {
		if t.Uploads == nil {
			t.Uploads = uploads.New(t.UploadsRoot, t.DB)
		}
	})
	return t.Uploads
}

// uploadFail maps the protocol errors (single source: internal/uploads) and
// envelope-shaped respErrors onto the JSON contract; anything else is an
// unexpected failure and propagates to echo's default 500.
func uploadFail(c *echo.Context, err error) error {
	var rerr *respError
	if errors.As(err, &rerr) {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	for _, m := range []struct {
		err    error
		status int
		code   string
	}{
		{uploads.ErrInvalidRequest, http.StatusBadRequest, ErrValidation},
		{uploads.ErrSizeLimit, http.StatusBadRequest, ErrValidation},
		{uploads.ErrInvalidChecksum, http.StatusBadRequest, ErrValidation},
		{uploads.ErrInvalidID, http.StatusBadRequest, ErrValidation},
		{uploads.ErrInvalidIndex, http.StatusBadRequest, ErrValidation},
		{uploads.ErrOrder, http.StatusBadRequest, ErrValidation},
		{uploads.ErrChunkSize, http.StatusBadRequest, ErrValidation},
		{uploads.ErrChunkTooLarge, http.StatusBadRequest, ErrValidation},
		{uploads.ErrIncomplete, http.StatusBadRequest, ErrValidation},
		{uploads.ErrCorrupted, http.StatusBadRequest, ErrValidation},
		{uploads.ErrUnsupported, http.StatusBadRequest, ErrValidation},
		{uploads.ErrInvalidRef, http.StatusBadRequest, ErrValidation},
		{uploads.ErrImageGone, http.StatusBadRequest, ErrValidation},
		{uploads.ErrImageChanged, http.StatusBadRequest, ErrValidation},
		{uploads.ErrNotFound, http.StatusNotFound, ErrNotFound},
		{uploads.ErrAttachedConflict, http.StatusConflict, ErrConflict},
		{uploads.ErrIO, http.StatusInternalServerError, ErrServer},
	} {
		if errors.Is(err, m.err) {
			return fail(c, m.status, m.code, m.err.Error())
		}
	}
	return err
}

// InitiateUpload is POST /teacher/uploads (teacher group): validate the
// {total,size,sha256} declaration, sweep orphaned sessions, create the
// session dir + meta.json (spec step 1).
func (t *Teacher) InitiateUpload(c *echo.Context) error {
	var req struct {
		Total  int    `json:"total"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	}
	if rerr := decodeJSON(c, &req); rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	if err := uploads.ValidateInitiate(req.Total, req.Size, req.SHA256); err != nil {
		return uploadFail(c, err)
	}
	sess := mw.SessionFrom(c)
	if sess == nil {
		return fail(c, http.StatusUnauthorized, ErrUnauthenticated, "Sign in to upload an image.")
	}
	// sweep first (spec): best-effort — a failure is logged and never
	// blocks a new session
	if err := t.uploads().Sweep(); err != nil {
		c.Logger().Error("upload sweep failed", "err", err)
	}
	session, err := t.uploads().Initiate(sess.UserID, req.Total, req.Size, req.SHA256)
	if err != nil {
		return uploadFail(c, err)
	}
	return ok(c, session)
}

// UploadChunk is POST /teacher/uploads/:id/chunks/:index — raw binary body,
// strictly sequential (spec step 2 + the order gate: next chunk or
// idempotent retransmit of the last one, anything else 400).
func (t *Teacher) UploadChunk(c *echo.Context) error {
	id := c.Param("id")
	if !uploads.ValidID(id) {
		return uploadFail(c, uploads.ErrInvalidID)
	}
	// The body is read through the store's order gate at the exact expected
	// size, capped at expected+4096 so a lying/oversized body fails with
	// "Chunk is too large." instead of being buffered.
	read := func(expected int) ([]byte, error) {
		r := http.MaxBytesReader(c.Response(), c.Request().Body, int64(expected)+4096)
		body, err := io.ReadAll(r)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, uploads.ErrChunkTooLarge
			}
			return nil, err
		}
		return body, nil
	}
	ack, err := t.uploads().PutChunk(id, c.Param("index"), read)
	if err != nil {
		return uploadFail(c, err)
	}
	return ok(c, ack)
}

// CompleteUpload is POST /teacher/uploads/:id/complete: assemble, verify
// sha256 + sniffed MIME, drop the chunks (spec step 3). Idempotent for an
// already-completed session.
func (t *Teacher) CompleteUpload(c *echo.Context) error {
	id := c.Param("id")
	if !uploads.ValidID(id) {
		return uploadFail(c, uploads.ErrInvalidID)
	}
	res, err := t.uploads().Complete(id)
	if err != nil {
		return uploadFail(c, err)
	}
	return ok(c, res)
}

// DeleteUpload is POST /teacher/uploads/:id/delete: cancel/cleanup (spec
// step 4). A completed image already referenced by question_images is
// never deleted (409 CONFLICT).
func (t *Teacher) DeleteUpload(c *echo.Context) error {
	id := c.Param("id")
	if !uploads.ValidID(id) {
		return uploadFail(c, uploads.ErrInvalidID)
	}
	if err := t.uploads().Remove(id); err != nil {
		return uploadFail(c, err)
	}
	return ok(c, nil)
}
