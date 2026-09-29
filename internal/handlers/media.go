package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"

	mw "quiz/internal/middleware"
	"quiz/internal/uploads"
)

// ServeQuestionImage is GET /media/question/:imageId (spec step 6): a BARE
// route with an in-handler session gate (guru OR murid). No middleware
// group and no ForceChangePassword — an image request must never trigger
// the password-change redirect, and anonymous callers must not learn
// whether an id exists.
func (t *Teacher) ServeQuestionImage(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	if sess == nil || (sess.Role != "guru" && sess.Role != "murid") {
		return fail(c, http.StatusUnauthorized, ErrUnauthenticated, "Sign in to view this image.")
	}
	id, err := strconv.ParseUint(c.Param("imageId"), 10, 32)
	if err != nil {
		return fail(c, http.StatusNotFound, ErrNotFound, "Image not found.")
	}
	var storedPath, filename, mime, sha string
	err = t.DB.QueryRowContext(c.Request().Context(),
		`SELECT path, filename, mime_type, sha256 FROM question_images
		 WHERE id = ? AND active = 1`, id).Scan(&storedPath, &filename, &mime, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(c, http.StatusNotFound, ErrNotFound, "Image not found.")
	}
	if err != nil {
		return err
	}
	// defense before any filesystem call: only the exact original-image
	// layout and allowlisted MIME types are ever served
	if !uploads.ValidRefPath(storedPath) || !uploads.AllowedMIME(mime) {
		return fail(c, http.StatusNotFound, ErrNotFound, "Image not found.")
	}
	etag := `"` + sha + `"`
	h := c.Response().Header()
	// Set, never Add — StaticCache pre-set Cache-Control: no-store for this
	// non-mount path; the media policy must overwrite it, not append.
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", etag)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", `inline; filename="`+headerFilename(filename)+`"`)
	h.Set("Content-Type", mime)
	if mw.ETagMatches(c.Request().Header.Get("If-None-Match"), etag) {
		return c.NoContent(http.StatusNotModified)
	}
	full := filepath.Join(t.uploads().Root(), filepath.FromSlash(storedPath))
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return fail(c, http.StatusNotFound, ErrNotFound, "Image not found.")
		}
		return fail(c, http.StatusInternalServerError, ErrServer, "Could not read the image — please try again.")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return fail(c, http.StatusInternalServerError, ErrServer, "Could not read the image — please try again.")
	}
	// Content-Type was set above, so ServeContent serves it as-is (no
	// re-sniff) and adds Range/HEAD/Last-Modified handling.
	http.ServeContent(c.Response(), c.Request(), filename, st.ModTime(), f)
	return nil
}

// headerFilename strips the characters that would break the quoted
// filename parameter (quote, CR, LF) from a stored client filename.
func headerFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, name)
}
