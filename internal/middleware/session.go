// Package middleware provides session loading and role guards (spec §6.4, §8).
package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"quiz/internal/cache"
	"quiz/internal/db"
)

// CookieName is the session cookie (spec §6.4: 43-char crypto/rand id).
const CookieName = "session_id"

// SessionCtxKey is the echo context key holding *Session.
const SessionCtxKey = "quiz.session"

// Session is the authenticated-principal value carried through the request.
// MustChangePW is snapshotted from users when the session is first loaded
// (login reads the flag again for its own redirect decision).
type Session struct {
	ID           string
	UserID       uint64
	Role         string
	MustChangePW bool
}

// SessionFrom returns the *Session stored by LoadSession, or nil when
// anonymous.
func SessionFrom(c *echo.Context) *Session {
	s, _ := c.Get(SessionCtxKey).(*Session)
	return s
}

// LoadSession resolves the session cookie → mirror (5 min) → DB, and stores
// the result on the context. Missing/tampered/expired/unknown sessions clear
// the cookie and continue as anonymous.
//
// The guru account lives in .env, not in users (user_id = 0 sentinel); only
// murid sessions consult users.must_change_pw.
func LoadSession(conn *sql.DB, store *cache.Store) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ck, err := c.Cookie(CookieName)
			if err != nil || ck.Value == "" {
				return next(c)
			}
			id := ck.Value
			if len(id) != 43 {
				ClearSessionCookie(c)
				return next(c)
			}

			if v, ok := store.Get(cache.SessionKey(id)); ok {
				if s, isSession := v.(*Session); isSession && s != nil {
					c.Set(SessionCtxKey, s)
					return next(c)
				}
			}

			ctx := c.Request().Context()
			row, err := db.GetSession(ctx, conn, id)
			if err != nil {
				return err
			}
			if row == nil {
				ClearSessionCookie(c)
				return next(c)
			}

			s := &Session{ID: row.ID, UserID: row.UserID, Role: row.Role}
			if row.Role != "guru" {
				var must, aktif bool
				err := conn.QueryRowContext(ctx,
					`SELECT must_change_pw, aktif FROM users WHERE id = ?`, row.UserID).
					Scan(&must, &aktif)
				if err == sql.ErrNoRows {
					// user gone → session is meaningless
					ClearSessionCookie(c)
					return next(c)
				}
				if err != nil {
					return err
				}
				if !aktif {
					// deactivated after this session was issued → dead now
					ClearSessionCookie(c)
					return next(c)
				}
				s.MustChangePW = must
			}

			store.Set(cache.SessionKey(id), s, cache.TTLSession)
			c.Set(SessionCtxKey, s)
			return next(c)
		}
	}
}

// AuthTeacher requires the guru role; otherwise redirect. No session →
// /login, wrong role → home.
func AuthTeacher(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		s := SessionFrom(c)
		if s == nil {
			return c.Redirect(http.StatusFound, "/login")
		}
		if s.Role != "guru" {
			return c.Redirect(http.StatusFound, "/")
		}
		return next(c)
	}
}

// AuthStudent requires the murid role; otherwise redirect to /login.
func AuthStudent(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		s := SessionFrom(c)
		if s == nil || s.Role != "murid" {
			return c.Redirect(http.StatusFound, "/login")
		}
		return next(c)
	}
}

// ForceChangePassword redirects flagged users to /change-password from every
// path except /change-password itself and /logout.
func ForceChangePassword(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		s := SessionFrom(c)
		if s != nil && s.MustChangePW {
			p := c.Request().URL.Path
			if p != "/change-password" && p != "/logout" {
				return c.Redirect(http.StatusFound, "/change-password")
			}
		}
		return next(c)
	}
}

// SetSessionCookie writes the 7-day HttpOnly session cookie. Secure is set
// only over TLS (local dev is plain HTTP).
func SetSessionCookie(c *echo.Context, id string) {
	c.SetCookie(&http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
		Secure:   c.Request().TLS != nil,
	})
}

// ClearSessionCookie expires the session cookie immediately.
func ClearSessionCookie(c *echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Secure:   c.Request().TLS != nil,
	})
}

// RevokeSession deletes the mirror entry FIRST, then the DB row — order is
// mandatory (spec §6.2/§6.4): the mirror must never outlive the row.
func RevokeSession(ctx context.Context, conn *sql.DB, store *cache.Store, id string) error {
	store.Delete(cache.SessionKey(id))
	return db.DeleteSession(ctx, conn, id)
}

// RevokeUserSessions is the password-change variant: delete every mirror
// entry first, then every DB row, so all of the user's cookies die at once.
func RevokeUserSessions(ctx context.Context, conn *sql.DB, store *cache.Store, userID uint64) error {
	ids, err := db.SessionIDsForUser(ctx, conn, userID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		store.Delete(cache.SessionKey(id))
	}
	return db.DeleteUserSessions(ctx, conn, userID)
}

// SessionExpiresAt is the cookie/session lifetime (spec §6.4: 7 days).
func SessionExpiresAt(now time.Time) time.Time { return now.Add(7 * 24 * time.Hour) }
