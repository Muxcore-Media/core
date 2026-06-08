package spool

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// DefaultSpoolURL is the official MuxCore spool.
const DefaultSpoolURL = "https://github.com/Muxcore-Media/spool"

// client is a reusable HTTP client for spool fetching.
var client = &http.Client{Timeout: 10 * time.Second}

// FetchTag fetches a tag definition from a spool URL by appending
// "/tags/{tagName}.json" to the base URL and parsing the JSON response.
// spoolURL is the base URL (e.g., "https://myspool.example.com/spool").
// tagName is the tag to fetch (e.g., "default").
// Security: rejects non-HTTPS URLs, caps response at 1MB.
func FetchTag(spoolURL, tagName string) (*contracts.TagDefinition, error) {
	if spoolURL == "" || tagName == "" {
		return nil, fmt.Errorf("spool: spoolURL and tagName are required")
	}

	fetchURL := strings.TrimRight(spoolURL, "/") + "/tags/" + tagName + ".json"

	u, err := url.Parse(fetchURL)
	if err != nil {
		return nil, fmt.Errorf("spool: invalid URL %q: %w", fetchURL, err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("spool: only HTTPS URLs are allowed, got %q", u.Scheme)
	}

	resp, err := client.Get(fetchURL)
	if err != nil {
		return nil, fmt.Errorf("spool: fetch %s: %w", fetchURL, err)
	}
	defer resp.Body.Close()

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

