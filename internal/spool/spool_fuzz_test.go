package spool

import (
	"net/url"
	"testing"
)

func FuzzSpoolURLValidation(f *testing.F) {
	strict := newFetcher(nil, nil, nil)
	listed := newFetcher([]string{"github.com", "127.0.0.1"}, nil, nil)
	f.Cleanup(strict.client.CloseIdleConnections)
	f.Cleanup(listed.client.CloseIdleConnections)
	seeds := []string{
		"https://github.com/Muxcore-Media/spool",
		"https://example.com/spool",
		"http://insecure.com/spool",
		"ftp://evil.com/spool",
		"https://github.com/spool",
		"https://127.0.0.1/spool",
		"https://[::1]/spool",
		"https://user:pass@host.com/spool",
		"https://host.com:8443/spool",
		"relative/path/spool",
		"/absolute/path/spool",
		"",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, rawURL string) {
		u, err := url.Parse(rawURL)
		if err != nil {
			return
		}

		_ = strict.validateURL(u)
		_ = listed.validateURL(u)
	})
}

func FuzzSpoolTagName(f *testing.F) {
	seeds := []string{
		"default",
		"v1.0.0",
		"my-tag_123",
		"",
		"../../../etc/passwd",
		"with spaces",
		"new\nline",
		"null\x00byte",
		"UPPERCASE",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, tagName string) {
		// validTagName regex must never panic on any input.
		_ = validTagName.MatchString(tagName)
	})
}
