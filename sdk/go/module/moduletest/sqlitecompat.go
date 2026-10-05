// Package moduletest provides helpers for module upgrade tests.
//
// ADR-0015 requires every stateful module to open committed snapshot
// fixtures (testdata/upgrade/<tag>.db, produced by that tag's code) with the
// current code and to assert that the upgraded schema is a superset of a
// fresh database's schema and that integrity checks pass. These helpers
// implement the shared parts of that check.
//
// The package uses only the standard library (database/sql). It does not
// import a SQLite driver; callers open the *sql.DB with whichever driver
// their module already uses.
//
// Intended upgrade_test.go pattern:
//
//	func TestUpgradeFromV0_3_0(t *testing.T) {
//		for i := 0; i < 2; i++ { // twice: the second open must be a no-op
//			path := moduletest.CopyFixture(t, "testdata/upgrade/v0.3.0.db")
//			upgraded := openStore(t, path) // current code, runs its inline DDL
//			// ... assert seeded rows read back and new columns have defaults ...
//			upSchema := moduletest.Schema(t, upgraded.DB())
//			fresh := openStore(t, filepath.Join(t.TempDir(), "fresh.db"))
//			moduletest.RequireSchemaSuperset(t, upSchema, moduletest.Schema(t, fresh.DB()))
//			moduletest.RequireIntegrity(t, upgraded.DB())
//		}
//	}
package moduletest

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Column describes one table column as reported by PRAGMA table_info.
type Column struct {
	Name    string
	Type    string
	Default string // empty when there is no default
	PK      int    // 1-based position in the primary key, 0 when not part of it
	NotNull bool
	HasDflt bool
}

// Table is a table with its columns in declaration order.
type Table struct {
	Name    string
	Columns []Column
}

// Index describes an index and its columns in order.
type Index struct {
	Name    string
	Table   string
	Columns []string
	Unique  bool
}

// SchemaInfo is a comparable snapshot of a database schema.
type SchemaInfo struct {
	Tables  map[string]Table
	Indexes map[string]Index
}

// CopyFixture copies the fixture database at src, plus any src-wal and
// src-shm siblings, into t.TempDir() and returns the path of the copy.
func CopyFixture(t testing.TB, src string) string {
	t.Helper()
	dir := t.TempDir()
	dst := filepath.Join(dir, filepath.Base(src))
	copyFile(t, src, dst)
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(src + suffix); err == nil {
			copyFile(t, src+suffix, dst+suffix)
		}
	}
	return dst
}

func copyFile(t testing.TB, src, dst string) {
	t.Helper()
	in, err := os.Open(src) //nolint:gosec // test helper opens caller-supplied fixture path
	if err != nil {
		t.Fatalf("moduletest: open fixture %s: %v", src, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // dst is under t.TempDir()
	if err != nil {
		t.Fatalf("moduletest: create %s: %v", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatalf("moduletest: copy %s: %v", src, err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("moduletest: close %s: %v", dst, err)
	}
}

// Schema reads the schema of db (tables and indexes, excluding sqlite_*
// internals) into a comparable structure.
func Schema(t testing.TB, db *sql.DB) SchemaInfo {
	t.Helper()
	s := SchemaInfo{Tables: map[string]Table{}, Indexes: map[string]Index{}}

	names := queryStrings(t, db,
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	for _, name := range names {
		s.Tables[name] = Table{Name: name, Columns: readColumns(t, db, name)}
	}
	for _, name := range names {
		rows, err := db.QueryContext(t.Context(), fmt.Sprintf("PRAGMA index_list(%s)", quote(name)))
		if err != nil {
			t.Fatalf("moduletest: index_list(%s): %v", name, err)
		}
		type listed struct {
			name   string
			unique bool
		}
		var idxs []listed
		for rows.Next() {
			var seq, unique, partial int
			var iname, origin string
			if err := rows.Scan(&seq, &iname, &unique, &origin, &partial); err != nil {
				_ = rows.Close()
				t.Fatalf("moduletest: scan index_list(%s): %v", name, err)
			}
			if strings.HasPrefix(iname, "sqlite_") {
				continue
			}
			idxs = append(idxs, listed{iname, unique != 0})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatalf("moduletest: index_list(%s): %v", name, err)
		}
		_ = rows.Close()
		for _, ix := range idxs {
			s.Indexes[ix.name] = Index{
				Name: ix.name, Table: name, Unique: ix.unique,
				Columns: readIndexColumns(t, db, ix.name),
			}
		}
	}
	return s
}

func readColumns(t testing.TB, db *sql.DB, table string) []Column {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), fmt.Sprintf("PRAGMA table_info(%s)", quote(table)))
	if err != nil {
		t.Fatalf("moduletest: table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var cols []Column
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("moduletest: scan table_info(%s): %v", table, err)
		}
		cols = append(cols, Column{
			Name: name, Type: strings.ToUpper(typ), NotNull: notnull != 0,
			Default: dflt.String, HasDflt: dflt.Valid, PK: pk,
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("moduletest: table_info(%s): %v", table, err)
	}
	return cols
}

func readIndexColumns(t testing.TB, db *sql.DB, index string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), fmt.Sprintf("PRAGMA index_info(%s)", quote(index)))
	if err != nil {
		t.Fatalf("moduletest: index_info(%s): %v", index, err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var seqno, cid int
		var name sql.NullString // NULL for expression columns
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			t.Fatalf("moduletest: scan index_info(%s): %v", index, err)
		}
		cols = append(cols, name.String)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("moduletest: index_info(%s): %v", index, err)
	}
	return cols
}

// RequireSchemaSuperset fails the test listing every table, column, or index
// present in fresh that is missing from upgraded, or whose column type,
// index columns, or uniqueness differ. Extra items in upgraded are allowed.
func RequireSchemaSuperset(t testing.TB, upgraded, fresh SchemaInfo) {
	t.Helper()
	var problems []string

	for _, name := range sortedKeys(fresh.Tables) {
		ft := fresh.Tables[name]
		ut, ok := upgraded.Tables[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("missing table %q", name))
			continue
		}
		have := map[string]Column{}
		for _, c := range ut.Columns {
			have[c.Name] = c
		}
		for _, fc := range ft.Columns {
			uc, ok := have[fc.Name]
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("missing column %s.%s", name, fc.Name))
			case uc.Type != fc.Type:
				problems = append(problems, fmt.Sprintf("column %s.%s type %q, want %q", name, fc.Name, uc.Type, fc.Type))
			}
		}
	}
	for _, name := range sortedKeys(fresh.Indexes) {
		fi := fresh.Indexes[name]
		ui, ok := upgraded.Indexes[name]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("missing index %q on %s", name, fi.Table))
		case strings.Join(ui.Columns, ",") != strings.Join(fi.Columns, ","):
			problems = append(problems, fmt.Sprintf("index %q columns (%s), want (%s)",
				name, strings.Join(ui.Columns, ","), strings.Join(fi.Columns, ",")))
		case ui.Unique != fi.Unique:
			problems = append(problems, fmt.Sprintf("index %q unique=%v, want %v", name, ui.Unique, fi.Unique))
		}
	}
	if len(problems) > 0 {
		t.Errorf("moduletest: upgraded schema is not a superset of fresh schema:\n  %s",
			strings.Join(problems, "\n  "))
	}
}

// RequireIntegrity fails unless PRAGMA integrity_check returns "ok" and
// PRAGMA foreign_key_check returns no rows.
func RequireIntegrity(t testing.TB, db *sql.DB) {
	t.Helper()
	got := queryStrings(t, db, "PRAGMA integrity_check")
	if len(got) != 1 || got[0] != "ok" {
		t.Errorf("moduletest: integrity_check: %v", got)
	}
	rows, err := db.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("moduletest: foreign_key_check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("moduletest: foreign_key_check: %v", err)
	}
	if n > 0 {
		t.Errorf("moduletest: foreign_key_check reported %d violation(s)", n)
	}
}

func queryStrings(t testing.TB, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), q)
	if err != nil {
		t.Fatalf("moduletest: %s: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("moduletest: %s: scan: %v", q, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("moduletest: %s: %v", q, err)
	}
	return out
}

func quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
