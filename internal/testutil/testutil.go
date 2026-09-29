// Package testutil provides shared helpers for tests that need configuration
// or the MariaDB test database (quiz_test).
package testutil

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"quiz/internal/config"
	"quiz/internal/db"
)

// Config loads the application config from the repo-root .env (found by
// walking up from the test's working directory). CONFIG_PATH overrides it.
func Config(t *testing.T) *config.Config {
	t.Helper()
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = findEnv(t)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config %s: %v (copy .env.example to .env)", path, err)
	}
	return cfg
}

func findEnv(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal(".env not found in any parent directory; copy .env.example to .env")
		}
		dir = parent
	}
}

// DB opens a pooled connection to the test database (cfg.TestDBName) and
// skips the test when it is unreachable.
func DB(t *testing.T) *sql.DB {
	t.Helper()
	cfg := Config(t)
	pool, err := sql.Open("mysql", cfg.DSN(cfg.TestDBName))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		t.Skipf("test database unreachable: %v — start `docker compose up -d db`", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// Reset truncates the given tables so integration tests are independent.
func Reset(t *testing.T, pool *sql.DB, tables ...string) {
	t.Helper()
	for _, tbl := range tables {
		if _, err := pool.Exec("TRUNCATE TABLE " + tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}

// Migrate applies all embedded migrations; failing the test on error.
func Migrate(t *testing.T, pool *sql.DB) {
	t.Helper()
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}
