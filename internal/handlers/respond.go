package handlers

import "github.com/labstack/echo/v5"

// Error codes for the JSON contract {"ok":false,"error":CODE,"message":...}.
// These exact literals are reused by the frontend and the tests.
const (
	ErrValidation      = "VALIDATION"
	ErrUnauthenticated = "UNAUTHENTICATED"
	ErrForbidden       = "FORBIDDEN"
	ErrNotFound        = "NOT_FOUND"
	ErrConflict        = "CONFLICT"
	ErrQuizEnded       = "QUIZ_ENDED"
	ErrQuizInProgress  = "QUIZ_IN_PROGRESS"
	ErrAttemptLimit    = "ATTEMPT_LIMIT"
	ErrInvalidCode     = "INVALID_CODE"
	ErrServer          = "SERVER_ERROR"
)

// MsgQuizInProgress is the exact message required by spec §6.12 / §11.18.
const MsgQuizInProgress = "Kuis sedang berlangsung, Anda tidak dapat bergabung."

// MsgInvalidCode is the shared wording for an unknown join code (POST /join
// and the SSE stream both use it).
const MsgInvalidCode = "Kuis tidak ditemukan untuk kode tersebut."

// ok writes the success envelope {"ok":true,"data":...} with status 200.
func ok(c *echo.Context, data any) error {
	return c.JSON(200, map[string]any{"ok": true, "data": data})
}

// fail writes the error envelope {"ok":false,"error":code,"message":msg}.
func fail(c *echo.Context, status int, code, msg string) error {
	return c.JSON(status, map[string]any{"ok": false, "error": code, "message": msg})
}

// failData writes an error envelope that also carries client context in
// data (e.g. the working count that drives the close-confirmation modal).
func failData(c *echo.Context, status int, code, msg string, data any) error {
	return c.JSON(status, map[string]any{"ok": false, "error": code, "message": msg, "data": data})
}
