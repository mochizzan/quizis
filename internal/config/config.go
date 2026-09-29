// Package config loads application configuration from a .env file via cleanenv.
package config

import (
	"fmt"

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

// Load reads the config file at path and validates required fields.
// For any key present in the file the file value wins; keys absent from the
// file resolve from OS env or the env-default fallback.
func Load(path string) (*Config, error) {
	var c Config
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
