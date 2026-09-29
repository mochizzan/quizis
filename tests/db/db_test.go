package dbtest

import (
	"database/sql"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"testing"

	"quiz/internal/db"
	"quiz/internal/testutil"
	"quiz/migrations"
)

func applied(t *testing.T, pool *sql.DB) []string {
	t.Helper()
	rows, err := pool.Query(`SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	return names
}

// TestMigrateTwiceIsIdempotent proves re-running Migrate against an
// already-migrated database applies nothing and errors nothing. It asserts
// the recorded set equals exactly the embedded migration set — deterministic
// even though other test packages migrate the same database in parallel.
func TestMigrateTwiceIsIdempotent(t *testing.T) {
	pool := testutil.DB(t)

	if err := db.Migrate(pool); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("second migrate must be a no-op: %v", err)
	}

	got := applied(t, pool)
	sort.Strings(got)

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var want []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			want = append(want, e.Name())
		}
	}
	sort.Strings(want)

	if !slices.Equal(got, want) {
		t.Errorf("schema_migrations = %v, want exactly the embedded migrations %v", got, want)
	}
	if len(want) == 0 {
		t.Fatal("no embedded migrations found")
	}
}

func TestSchemaMatchesSpecSection5(t *testing.T) {
	pool := testutil.DB(t)
	cfg := testutil.Config(t)
	testutil.Migrate(t, pool)
	schema := cfg.TestDBName

	columns := []struct{ table, column string }{
		{"quizzes", "code"},
		{"participants", "qorder"},
		{"participants", "ends_at"},
		{"anti_cheat_events", "kind"},
	}
	for _, c := range columns {
		assertColumn(t, pool, schema, c.table, c.column)
	}

	// uq_ans is a UNIQUE KEY, not a column — assert it via statistics.
	assertIndex(t, pool, schema, "answers", "uq_ans")

	// spec §5: zero foreign keys anywhere in the schema.
	var fks int
	if err := pool.QueryRow(
		`SELECT COUNT(*) FROM information_schema.referential_constraints WHERE constraint_schema = ?`,
		schema,
	).Scan(&fks); err != nil {
		t.Fatalf("count foreign keys: %v", err)
	}
	if fks != 0 {
		t.Errorf("schema has %d foreign keys; spec §5 requires zero", fks)
	}
}

func assertColumn(t *testing.T, pool *sql.DB, schema, table, column string) {
	t.Helper()
	var one int
	err := pool.QueryRow(
		`SELECT 1 FROM information_schema.columns
		 WHERE table_schema = ? AND table_name = ? AND column_name = ?`,
		schema, table, column,
	).Scan(&one)
	if err == sql.ErrNoRows {
		t.Errorf("missing column %s.%s", table, column)
		return
	}
	if err != nil {
		t.Errorf("query column %s.%s: %v", table, column, err)
	}
}

func assertIndex(t *testing.T, pool *sql.DB, schema, table, index string) {
	t.Helper()
	var one int
	err := pool.QueryRow(
		`SELECT 1 FROM information_schema.statistics
		 WHERE table_schema = ? AND table_name = ? AND index_name = ?`,
		schema, table, index,
	).Scan(&one)
	if err == sql.ErrNoRows {
		t.Errorf("missing index %s.%s", table, index)
		return
	}
	if err != nil {
		t.Errorf("query index %s.%s: %v", table, index, err)
	}
}
