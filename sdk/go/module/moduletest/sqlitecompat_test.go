package moduletest_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Muxcore-Media/core/sdk/go/module/moduletest"
)

// recorder captures failures instead of failing the real test.
type recorder struct {
	testing.TB
	msgs []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}
func (r *recorder) Helper() {}

func openDB(t *testing.T, path string, ddl ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range ddl {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return db
}

const (
	oldDDL = `CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`
	newDDL = `CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL, tag TEXT DEFAULT 'x')`
	idxDDL = `CREATE UNIQUE INDEX idx_items_name ON items(name)`
)

func TestSupersetOK(t *testing.T) {
	dir := t.TempDir()
	up := openDB(t, filepath.Join(dir, "up.db"), oldDDL, `ALTER TABLE items ADD COLUMN tag TEXT DEFAULT 'x'`, idxDDL,
		`CREATE TABLE extra (a INTEGER)`)
	fresh := openDB(t, filepath.Join(dir, "fresh.db"), newDDL, idxDDL)
	r := &recorder{TB: t}
	moduletest.RequireSchemaSuperset(r, moduletest.Schema(t, up), moduletest.Schema(t, fresh))
	if len(r.msgs) != 0 {
		t.Fatalf("unexpected failures: %v", r.msgs)
	}
}

func TestMissingColumnDetected(t *testing.T) {
	dir := t.TempDir()
	up := openDB(t, filepath.Join(dir, "up.db"), oldDDL)
	fresh := openDB(t, filepath.Join(dir, "fresh.db"), newDDL)
	r := &recorder{TB: t}
	moduletest.RequireSchemaSuperset(r, moduletest.Schema(t, up), moduletest.Schema(t, fresh))
	if len(r.msgs) != 1 || !strings.Contains(r.msgs[0], "missing column items.tag") {
		t.Fatalf("got %v", r.msgs)
	}
}

func TestMissingIndexAndTableDetected(t *testing.T) {
	dir := t.TempDir()
	up := openDB(t, filepath.Join(dir, "up.db"), newDDL)
	fresh := openDB(t, filepath.Join(dir, "fresh.db"), newDDL, idxDDL, `CREATE TABLE more (a INTEGER)`)
	r := &recorder{TB: t}
	moduletest.RequireSchemaSuperset(r, moduletest.Schema(t, up), moduletest.Schema(t, fresh))
	if len(r.msgs) != 1 || !strings.Contains(r.msgs[0], `missing index "idx_items_name"`) ||
		!strings.Contains(r.msgs[0], `missing table "more"`) {
		t.Fatalf("got %v", r.msgs)
	}
}

func TestTypeMismatchDetected(t *testing.T) {
	dir := t.TempDir()
	up := openDB(t, filepath.Join(dir, "up.db"), `CREATE TABLE t (a TEXT)`)
	fresh := openDB(t, filepath.Join(dir, "fresh.db"), `CREATE TABLE t (a INTEGER)`)
	r := &recorder{TB: t}
	moduletest.RequireSchemaSuperset(r, moduletest.Schema(t, up), moduletest.Schema(t, fresh))
	if len(r.msgs) != 1 || !strings.Contains(r.msgs[0], "column t.a type") {
		t.Fatalf("got %v", r.msgs)
	}
}

func TestIntegrityOK(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "i.db"),
		`CREATE TABLE p (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE c (pid INTEGER REFERENCES p(id))`,
		`INSERT INTO p VALUES (1)`, `INSERT INTO c VALUES (1)`)
	r := &recorder{TB: t}
	moduletest.RequireIntegrity(r, db)
	if len(r.msgs) != 0 {
		t.Fatalf("unexpected failures: %v", r.msgs)
	}
}

func TestIntegrityForeignKeyViolation(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "i.db"),
		`CREATE TABLE p (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE c (pid INTEGER REFERENCES p(id))`,
		`INSERT INTO c VALUES (42)`)
	r := &recorder{TB: t}
	moduletest.RequireIntegrity(r, db)
	if len(r.msgs) != 1 || !strings.Contains(r.msgs[0], "foreign_key_check") {
		t.Fatalf("got %v", r.msgs)
	}
}

func TestCopyFixture(t *testing.T) {
	src := filepath.Join(t.TempDir(), "f.db")
	for suffix, body := range map[string]string{"": "db", "-wal": "wal", "-shm": "shm"} {
		if err := os.WriteFile(src+suffix, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dst := moduletest.CopyFixture(t, src)
	if dst == src || filepath.Dir(dst) == filepath.Dir(src) {
		t.Fatalf("copy not isolated: %s", dst)
	}
	for suffix, body := range map[string]string{"": "db", "-wal": "wal", "-shm": "shm"} {
		got, err := os.ReadFile(dst + suffix)
		if err != nil || string(got) != body {
			t.Fatalf("%s: %q %v", suffix, got, err)
		}
	}
}
