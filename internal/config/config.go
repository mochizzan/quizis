// Package config loads application configuration via cleanenv: from a .env
// file when one exists (local go run / go test), or purely from the OS
// environment when it does not (containers — the image never carries .env).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/ilyakaznacheev/cleanenv"
)

// Config is the application configuration, mapped from environment variables.
//
// DB_HOST deliberately carries env-default only (no env-required): cleanenv's
// required-check runs before the default fallback, and DB_HOST is intentionally
// absent from .env so that Docker compose's `environment: DB_HOST=db` survives
// (parseENV only overwrites OS env for keys present in the file).
type Config struct {
	Port       int    `env:"PORT" env-default:"8090"`
	GuruUser   string `env:"GURU_USER" env-required:"true"`
	GuruPass   string `env:"GURU_PASS" env-required:"true"`
	DBUser     string `env:"DB_USER" env-required:"true"`
	DBPass     string `env:"DB_PASS" env-required:"true"`
	DBName     string `env:"DB_NAME" env-required:"true"`
	DBPort     int    `env:"DB_PORT" env-default:"3306"`
	DBHost     string `env:"DB_HOST" env-default:"127.0.0.1"` // NOT in .env
	TestDBName string `env:"TEST_DB_NAME" env-default:"quiz_test"`
}

// Load reads the configuration from path and validates required fields.
//
// The file is optional. A container never has one — the image excludes .env
// (.dockerignore) and compose injects the host's .env.example/.env through
// env_file at run time (the environment: section outranks env_file) — so
// those values arrive as plain OS environment. When the file IS present
// (local `go run` / `go test`), its keys win over OS env: cleanenv's
// parseENV writes every file key into the process environment
// unconditionally. Either way the required-field check fails fast at boot.
func Load(path string) (*Config, error) {
	var c Config
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		// No file (container): resolve from OS env + env-default only.
		if err := cleanenv.ReadEnv(&c); err != nil {
			return nil, fmt.Errorf("read config %s (absent, environment only): %w", path, err)
		}
		return &c, nil
	}
	if err := cleanenv.ReadConfig(path, &c); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return &c, nil
}

// DSN returns the MySQL DSN for dbName. parseTime=true is mandatory:
// DATETIME columns (answers.answered_at, participants.*_at) are scanned into
// time.Time / sql.NullTime.
func (c *Config) DSN(dbName string) string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4&loc=UTC",
		c.DBUser, c.DBPass, c.DBHost, c.DBPort, dbName)
}
