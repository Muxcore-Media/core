// Package pathguard confines user-supplied filesystem paths to configured
// allow-listed roots. It implements RULE-VAL-1 (docs/specs/FRD.md) /
// NFR-SEC-008: every user-supplied path (library roots, restore targets,
// imports, generated file names) must resolve inside an allow-listed root.
//
// Confine accepts an absolute path, Join a relative name under one root, and
// SanitizeComponent a single file-name part. All three refuse ".." segments
// and NUL bytes outright instead of normalising them away, and Confine/Join
// compare real paths: symlinks are resolved on the nearest existing ancestor
// of the path and on the roots, and the comparison is by path component, so
// /data/movies2 is not inside /data/movies.
//
// The checks are a point-in-time decision. A caller that then creates or
// opens files must not let untrusted parties change the tree between the
// check and the use (or must re-check, e.g. with os.Root for the operation).
package pathguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideRoots means the path does not resolve inside any allowed root.
var ErrOutsideRoots = errors.New("pathguard: path outside allowed roots")

// ErrInvalidPath means the input itself is malformed (empty, relative where an
// absolute path is required, "..", NUL, a dangling symlink, bad characters).
var ErrInvalidPath = errors.New("pathguard: invalid path")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPath, fmt.Sprintf(format, args...))
}

// Confine checks that path, an absolute user-supplied path, resolves inside
// one of roots and returns its cleaned real path (symlinks in the existing
// part resolved). The path need not exist; its nearest existing ancestor is
// resolved and the rest appended. A root itself is accepted. Roots must be
// absolute; roots that do not exist are compared by their own nearest
// existing ancestor in the same way.
func Confine(path string, roots []string) (string, error) {
	if err := checkInput(path); err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		return "", invalid("path %q is not absolute", path)
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("%w: no roots configured", ErrOutsideRoots)
	}
	realPath, err := resolve(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if root == "" || strings.ContainsRune(root, 0) || !filepath.IsAbs(root) {
			return "", fmt.Errorf("pathguard: root %q must be an absolute path", root)
		}
		realRoot, err := resolve(filepath.Clean(root))
		if err != nil {
			return "", fmt.Errorf("pathguard: resolve root %q: %w", root, err)
		}
		if within(realRoot, realPath) {
			return realPath, nil
		}
	}
	return "", ErrOutsideRoots
}

// Join joins an untrusted relative name (one or more components) under root
// and confines the result to root as Confine does, returning the real path.
// Absolute names, ".." segments and NUL bytes are refused; an empty name or
// "." yields root itself.
func Join(root, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", invalid("NUL byte in path")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || filepath.VolumeName(rel) != "" {
		return "", invalid("name %q must be relative", rel)
	}
	if hasDotDot(rel) {
		return "", invalid("name %q contains a \"..\" segment", rel)
	}
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("pathguard: root %q must be an absolute path", root)
	}
	return Confine(filepath.Join(root, rel), []string{root})
}

// SanitizeComponent validates s as a single file-name component built from
// untrusted input (a subtitle language, a tag, an id): only [A-Za-z0-9._-],
// not empty, not "." or "..", and at most maxLen bytes when maxLen > 0. It
// returns s unchanged when valid; it never rewrites the input.
func SanitizeComponent(s string, maxLen int) (string, error) {
	if s == "" || s == "." || s == ".." {
		return "", invalid("component %q not allowed", s)
	}
	if maxLen > 0 && len(s) > maxLen {
		return "", invalid("component longer than %d bytes", maxLen)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-'
		if !ok {
			return "", invalid("component %q has a character outside [A-Za-z0-9._-]", s)
		}
	}
	return s, nil
}

func checkInput(path string) error {
	if path == "" {
		return invalid("empty path")
	}
	if strings.ContainsRune(path, 0) {
		return invalid("NUL byte in path")
	}
	if hasDotDot(path) {
		return invalid("path %q contains a \"..\" segment", path)
	}
	return nil
}

func hasDotDot(p string) bool {
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

// resolve returns the real path of p (absolute, clean): symlinks resolved on
// the nearest existing ancestor, the non-existent remainder appended. A
// dangling symlink on the way is refused because writing through it would
// land wherever it points.
func resolve(p string) (string, error) {
	existing, rest := p, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("pathguard: stat %q: %w", existing, err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	realPath, err := filepath.EvalSymlinks(existing)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", invalid("dangling symlink at %q", existing)
		}
		return "", fmt.Errorf("pathguard: resolve %q: %w", existing, err)
	}
	if rest == "" {
		return realPath, nil
	}
	return filepath.Join(realPath, rest), nil
}

// within reports whether p equals root or lies beneath it, by path component.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
