// Package migrations holds the embedded, numbered SQL migrations.
//
// This package lives at the repo root (beside *.sql) because go:embed cannot
// reference paths outside its own directory. internal/db imports quiz/migrations.
package migrations

import "embed"

// FS contains every numbered migration file, applied in ascending name order.
//
//go:embed *.sql
var FS embed.FS
