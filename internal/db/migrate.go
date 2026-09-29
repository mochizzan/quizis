package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"quiz/migrations"
)

// Migrate applies every embedded migration not yet recorded in
// schema_migrations. Each file runs in its own transaction (statements, then
// the bookkeeping insert, then COMMIT); on error the record is rolled back
// and the error names the file. Applied names are printed to stdout.
//
// A MySQL named lock held on one dedicated connection serializes concurrent
// migrators (container boot + `go run` + parallel test packages): whoever
// waits acquires the lock after the winner recorded its files and then finds
// nothing to apply.
func Migrate(pool *sql.DB) error {
	ctx := context.Background()

	conn, err := pool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate conn: %w", err)
	}
	defer conn.Close()

	var locked int
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK('quiz.migrations', 30)`).Scan(&locked); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if locked != 1 {
		return fmt.Errorf("acquire migration lock: timed out")
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('quiz.migrations')`)

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
    name VARCHAR(191) PRIMARY KEY,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	applied, err := appliedNames(ctx, conn)
	if err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := applyOne(ctx, conn, name, string(body)); err != nil {
			return err
		}
		fmt.Printf("migrated %s\n", name)
	}
	return nil
}

// migrator is satisfied by both *sql.DB and *sql.Conn.
type migrator interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

func applyOne(ctx context.Context, db migrator, name, body string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", name, err)
	}
	for _, stmt := range splitStatements(body) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		tx.Rollback()
		return fmt.Errorf("record %s: %w", name, err)
	}
	return tx.Commit()
}

func appliedNames(ctx context.Context, db migrator) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	names := make(map[string]bool)
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		names[n] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return names, nil
}

// splitStatements splits a migration file into executable statements, honoring
// single-quoted strings and backtick identifiers, and stripping -- and #
// comments. The MySQL driver rejects multi-statement queries, so each CREATE
// TABLE must be sent on its own.
func splitStatements(body string) []string {
	var out []string
	var buf strings.Builder
	var inStr, inTick bool

	flush := func() {
		if s := strings.TrimSpace(buf.String()); s != "" {
			out = append(out, s)
		}
		buf.Reset()
	}

	runes := []rune(body)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if inStr {
			buf.WriteRune(ch)
			if ch == '\\' && i+1 < len(runes) {
				i++
				buf.WriteRune(runes[i])
				continue
			}
			if ch == '\'' {
				inStr = false
			}
			continue
		}
		if inTick {
			buf.WriteRune(ch)
			if ch == '`' {
				inTick = false
			}
			continue
		}
		if ch == '#' {
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			continue
		}
		if ch == '-' && i+1 < len(runes) && runes[i+1] == '-' &&
			(i+2 >= len(runes) || runes[i+2] == ' ' || runes[i+2] == '\t' ||
				runes[i+2] == '\r' || runes[i+2] == '\n') {
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			continue
		}
		switch ch {
		case '\'':
			inStr = true
			buf.WriteRune(ch)
		case '`':
			inTick = true
			buf.WriteRune(ch)
		case ';':
			flush()
		default:
			buf.WriteRune(ch)
		}
	}
	flush()
	return out
}
