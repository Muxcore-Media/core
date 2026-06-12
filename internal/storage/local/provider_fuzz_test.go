package local

import (
	"strings"
	"testing"
)

func FuzzEncodeKeyRoundTrip(f *testing.F) {
	seeds := []string{
		"simple-key",
		"path/to/key",
		"key/with/trailing/",
		"double..dot",
		"../path/traversal",
		"../../../etc/passwd",
		"back\\slash",
		"null\x00byte",
		"spaces in key",
		"UPPERCASE",
		"unicode/αβγ",
		"",
		"/leading/slash",
		"trailing/slash/",
		"mix/of/.. and \\slashes\x00",
		"a",
		"ab/cd/ef",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, key string) {
		encoded := encodeKey(key)

		// Encoded key must be safe for filesystem paths (no /, \, .., null).
		if strings.Contains(encoded, "/") {
			t.Errorf("encodeKey(%q) = %q; contains '/'", key, encoded)
		}
		if strings.Contains(encoded, "\\") {
			t.Errorf("encodeKey(%q) = %q; contains '\\'", key, encoded)
		}
		if strings.Contains(encoded, "..") {
			t.Errorf("encodeKey(%q) = %q; contains '..'", key, encoded)
		}
		if strings.Contains(encoded, "\x00") {
			t.Errorf("encodeKey(%q) = %q; contains null byte", key, encoded)
		}

		// Round-trip: decode(encode(key)) must return the original.
		decoded := decodeKey(encoded)
		if decoded != key {
			t.Errorf("round-trip: decode(encode(%q)) = %q, want %q", key, decoded, key)
		}
	})
}

func FuzzDecodeKeyNoPanic(f *testing.F) {
	seeds := []string{
		"simple-key",
		"%2Fpath%2Fto%2Fkey",
		"%2E%2E%2Ftraversal",
		"%5Cslash",
		"%00null",
		"mixed%2F%5C%2E%2E%00stuff",
		"",
		"%%%%",
		"%2",
		"%",
		"%GG",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, encoded string) {
		// decodeKey must never panic on any input.
		_ = decodeKey(encoded)
	})
}
