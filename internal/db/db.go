// Package db connects to MariaDB and applies embedded migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Open connects to MariaDB, tunes the pool, and pings with a 10s timeout.
func Open(dsn string) (*sql.DB, error) {
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(10)
	conn.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return conn, nil
}
