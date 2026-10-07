package spool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// DefaultSpoolURL is the official MuxCore spool.
const DefaultSpoolURL = "https://github.com/Muxcore-Media/spool"

var validTagName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

var fetcherMu sync.RWMutex
var currentFetcher = newFetcher(nil, nil, nil)

// SetAllowedHosts restricts every spool request and redirect to an exact host
// string, including any explicit port. Listed hosts may resolve to LAN or
// loopback addresses; metadata and link-local destinations are always refused.
// An empty list permits public destinations only.
func SetAllowedHosts(hosts []string) {
	next := newFetcher(hosts, nil, nil)
	fetcherMu.Lock()
	previous := currentFetcher
	currentFetcher = next
	fetcherMu.Unlock()
	previous.client.CloseIdleConnections()
}

// FetchTag fetches a tag definition from a spool URL by appending
// "/tags/{tagName}.json" to the base URL and parsing the JSON response.
// spoolURL is the base URL (e.g., "https://myspool.example.com/spool").
// tagName is the tag to fetch (e.g., "default").
// Requests use a snapshot of the host policy and its guarded connection pool.
// The transport ignores environment proxies to preserve checked-IP dialing.
func FetchTag(ctx context.Context, spoolURL, tagName string) (*contracts.TagDefinition, error) {
	fetcherMu.RLock()
	f := currentFetcher
	fetcherMu.RUnlock()
	return f.fetchTag(ctx, spoolURL, tagName)
}

func (f *fetcher) fetchTag(ctx context.Context, spoolURL, tagName string) (*contracts.TagDefinition, error) {
	if spoolURL == "" || tagName == "" {
		return nil, fmt.Errorf("spool: spoolURL and tagName are required")
	}

	if !validTagName.MatchString(tagName) {
		return nil, fmt.Errorf("spool: invalid tag name %q — must match %s", tagName, validTagName.String())
	}

	baseURL, err := url.Parse(spoolURL)
	if err != nil {
		return nil, fmt.Errorf("spool: invalid base URL %q: %w", spoolURL, err)
	}
	u := baseURL.JoinPath("tags", tagName+".json")
	if validateErr := f.validateURL(u); validateErr != nil {
		return nil, validateErr
	}
	fetchURL := u.String()

	// Warn when using a non-official spool.
	if spoolURL != DefaultSpoolURL {
		fmt.Fprintf(os.Stderr, "\n⚠️  WARNING: Using a non-official spool: %s\n", spoolURL)
		fmt.Fprintf(os.Stderr, "   Modules from third-party spools run with the same privileges as the core process.\n")
		fmt.Fprintf(os.Stderr, "   Only use spools from sources you trust. See https://opencode.ai for details.\n\n")
	}

	// Transport deliberately detaches dial contexts from request cancellation.
	// Preserve this fetch's lifetime so canceled or timed-out fetches also stop
	// DNS and connection attempts, including when Client.Timeout expires.
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = context.WithValue(requestCtx, spoolRequestContextKey{}, requestCtx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("spool: create request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spool: fetch %s: %w", fetchURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spool: fetch %s: HTTP %d", fetchURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("spool: read body: %w", err)
	}

	var tag contracts.TagDefinition
	if err := json.Unmarshal(body, &tag); err != nil {
		return nil, fmt.Errorf("spool: parse tag %q: %w", tagName, err)
	}

	return &tag, nil
}
