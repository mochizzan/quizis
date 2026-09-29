package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"

	"quiz/internal/models"
)

// NewSessionID returns a 43-char crypto/rand base64url session id (32 bytes
// → RawURLEncoding, no padding).
func NewSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// InsertSession stores a new session row. The caller must delete the mirror
// entry (there is none yet) and, on re-login, any stale entry first.
func InsertSession(ctx context.Context, conn *sql.DB, id string, userID uint64, role string, expiresAt time.Time) error {
	_, err := conn.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, role, expires_at) VALUES (?, ?, ?, ?)`,
		id, userID, role, expiresAt)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetSession returns the session row, or (nil, nil) when the id is unknown or
// the session has expired.
func GetSession(ctx context.Context, conn *sql.DB, id string) (*models.Session, error) {
	row := conn.QueryRowContext(ctx,
		`SELECT id, user_id, role, expires_at, created_at FROM sessions WHERE id = ?`, id)
	var s models.Session
	if err := row.Scan(&s.ID, &s.UserID, &s.Role, &s.ExpiresAt, &s.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get session: %w", err)
	}
	if !s.ExpiresAt.After(time.Now()) {
		return nil, nil
	}
	return &s, nil
}

// DeleteSession removes one session row (logout). Callers delete the mirror
// entry first so revocation is instant.
func DeleteSession(ctx context.Context, conn *sql.DB, id string) error {
	if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteUserSessions removes every session of a user (password change → all
// devices logged out).
func DeleteUserSessions(ctx context.Context, conn *sql.DB, userID uint64) error {
	if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

// SessionIDsForUser returns every live session id of a user, so callers can
// delete mirror entries before deleting the rows (instant revocation).
func SessionIDsForUser(ctx context.Context, conn *sql.DB, userID uint64) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT id FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user sessions: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan session id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session ids: %w", err)
	}
	return ids, nil
}

// DeleteExpired garbage-collects expired rows (boot + on login).
func DeleteExpired(ctx context.Context, conn *sql.DB, now time.Time) error {
	if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}
