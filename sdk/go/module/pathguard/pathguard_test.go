package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree builds base/{data/movies/a.mkv, data/movies2, outside/secret} and
// returns the real base path.
func tree(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"data/movies/sub", "data/movies2", "outside"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"data/movies/a.mkv", "outside/secret"} {
		if err := os.WriteFile(filepath.Join(base, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestConfine(t *testing.T) {
	base := tree(t)
	movies := filepath.Join(base, "data/movies")
	outside := filepath.Join(base, "outside")
	symlink(t, outside, filepath.Join(movies, "escape"))                     // dir link out of root
	symlink(t, filepath.Join(outside, "secret"), filepath.Join(movies, "s")) // file link out of root
	symlink(t, filepath.Join(movies, "sub"), filepath.Join(movies, "inner")) // link staying inside
	symlink(t, filepath.Join(outside, "nope"), filepath.Join(movies, "dangling"))
	symlink(t, movies, filepath.Join(base, "alias")) // alias of the root
	roots := []string{movies}

	cases := []struct {
		name, path string
		want       string // real path expected; "" means error
		err        error
	}{
		{"root itself", movies, movies, nil},
		{"root trailing slash", movies + "/", movies, nil},
		{"existing file", filepath.Join(movies, "a.mkv"), filepath.Join(movies, "a.mkv"), nil},
		{"nonexistent leaf", filepath.Join(movies, "new.mkv"), filepath.Join(movies, "new.mkv"), nil},
		{"nonexistent deep", filepath.Join(movies, "x/y/z.mkv"), filepath.Join(movies, "x/y/z.mkv"), nil},
		{"redundant separators", movies + "//sub/./", filepath.Join(movies, "sub"), nil},
		{"symlink inside root", filepath.Join(movies, "inner/f"), filepath.Join(movies, "sub/f"), nil},
		{"via root alias", filepath.Join(base, "alias/a.mkv"), filepath.Join(movies, "a.mkv"), nil},
		{"sibling prefix", filepath.Join(base, "data/movies2/x"), "", ErrOutsideRoots},
		{"sibling prefix dir", filepath.Join(base, "data/movies2"), "", ErrOutsideRoots},
		{"parent", filepath.Join(base, "data"), "", ErrOutsideRoots},
		{"outside", "/etc/passwd", "", ErrOutsideRoots},
		{"dir symlink escape", filepath.Join(movies, "escape/secret"), "", ErrOutsideRoots},
		{"dir symlink escape new leaf", filepath.Join(movies, "escape/new"), "", ErrOutsideRoots},
		{"file symlink escape", filepath.Join(movies, "s"), "", ErrOutsideRoots},
		{"dangling symlink", filepath.Join(movies, "dangling"), "", ErrInvalidPath},
		{"dotdot", movies + "/../outside/secret", "", ErrInvalidPath},
		{"dotdot staying inside", movies + "/sub/../a.mkv", "", ErrInvalidPath},
		{"relative", "data/movies/a.mkv", "", ErrInvalidPath},
		{"empty", "", "", ErrInvalidPath},
		{"NUL", movies + "/a\x00.mkv", "", ErrInvalidPath},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Confine(c.path, roots)
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("want %v, got %q %v", c.err, got, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("want %q, got %q %v", c.want, got, err)
			}
		})
	}
}

func TestConfineMultipleRoots(t *testing.T) {
	base := tree(t)
	roots := []string{filepath.Join(base, "data/movies"), filepath.Join(base, "data/movies2")}
	if _, err := Confine(filepath.Join(base, "data/movies2/x"), roots); err != nil {
		t.Fatalf("second root: %v", err)
	}
	if _, err := Confine(filepath.Join(base, "outside"), roots); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("want outside, got %v", err)
	}
	if _, err := Confine(filepath.Join(base, "x"), nil); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("no roots must refuse, got %v", err)
	}
	if _, err := Confine(filepath.Join(base, "x"), []string{"relative/root"}); err == nil {
		t.Fatal("relative root must be refused")
	}
	// A root that does not exist yet still confines lexically.
	missing := filepath.Join(base, "not-yet")
	if got, err := Confine(filepath.Join(missing, "a"), []string{missing}); err != nil || got != filepath.Join(missing, "a") {
		t.Fatalf("missing root: %q %v", got, err)
	}
	if _, err := Confine(filepath.Join(base, "not-yet2"), []string{missing}); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("missing root sibling prefix: %v", err)
	}
}

func TestJoin(t *testing.T) {
	base := tree(t)
	movies := filepath.Join(base, "data/movies")
	symlink(t, filepath.Join(base, "outside"), filepath.Join(movies, "escape"))
	ok := map[string]string{
		"a.mkv":       filepath.Join(movies, "a.mkv"),
		"sub/new.srt": filepath.Join(movies, "sub/new.srt"),
		"":            movies,
		".":           movies,
		"x/./y":       filepath.Join(movies, "x/y"),
		"..foo/bar..": filepath.Join(movies, "..foo/bar.."),
	}
	for rel, want := range ok {
		if got, err := Join(movies, rel); err != nil || got != want {
			t.Errorf("Join(%q) = %q %v, want %q", rel, got, err, want)
		}
	}
	bad := map[string]error{
		"../outside/secret": ErrInvalidPath,
		"sub/../../x":       ErrInvalidPath,
		"..":                ErrInvalidPath,
		`..\x`:              ErrInvalidPath,
		"/etc/passwd":       ErrInvalidPath,
		`\etc\passwd`:       ErrInvalidPath,
		"a\x00b":            ErrInvalidPath,
		"escape/secret":     ErrOutsideRoots,
		"escape/new.srt":    ErrOutsideRoots,
	}
	for rel, want := range bad {
		if got, err := Join(movies, rel); !errors.Is(err, want) {
			t.Errorf("Join(%q) = %q %v, want %v", rel, got, err, want)
		}
	}
	if _, err := Join("relative", "a"); err == nil {
		t.Error("relative root must be refused")
	}
}

func TestSanitizeComponent(t *testing.T) {
	for _, s := range []string{"en", "pt-BR", "eng.forced", "a_b-1.2", ".hidden", "..x"} {
		if got, err := SanitizeComponent(s, 32); err != nil || got != s {
			t.Errorf("%q: got %q %v", s, got, err)
		}
	}
	for _, s := range []string{"", ".", "..", "a/b", `a\b`, "a b", "a\x00", "ü", "../x", "a:b", strings.Repeat("a", 33)} {
		if _, err := SanitizeComponent(s, 32); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%q: want ErrInvalidPath, got %v", s, err)
		}
	}
	if _, err := SanitizeComponent(strings.Repeat("a", 300), 0); err != nil {
		t.Errorf("maxLen 0 means unlimited: %v", err)
	}
}
