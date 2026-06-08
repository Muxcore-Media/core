package spool

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultSpoolURL is the official MuxCore spool.
const DefaultSpoolURL = "https://github.com/Muxcore-Media/spool"

// TagDefinition is a curated module preset fetched from a spool.
type TagDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Version     string      `json:"version"`
	Modules     []TagModule `json:"modules"`
}

// TagModule is a single module entry in a tag definition.
type TagModule struct {
	Repo     string `json:"repo"`
	Version  string `json:"version"`
	Required bool   `json:"required"`
}

// FetchTag fetches a tag definition from a spool URL.
// spoolURL is the base URL (e.g., "https://github.com/Muxcore-Media/spool").
// tagName is the tag to fetch (e.g., "default").
func FetchTag(spoolURL, tagName string) (*TagDefinition, error) {
	fetchURL, err := buildFetchURL(spoolURL, tagName)
	if err != nil {
		return nil, fmt.Errorf("spool: build fetch URL: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fetchURL)
	if err != nil {
		return nil, fmt.Errorf("spool: fetch %s: %w", fetchURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spool: fetch %s: HTTP %d", fetchURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("spool: read body: %w", err)
	}

	var tag TagDefinition
	if err := json.Unmarshal(body, &tag); err != nil {
		return nil, fmt.Errorf("spool: parse tag %q: %w", tagName, err)
	}

	return &tag, nil
}

// buildFetchURL converts a spool URL and tag name into a raw fetch URL.
// GitHub URLs get converted to raw.githubusercontent.com.
// Non-GitHub URLs use a direct path append.
func buildFetchURL(spoolURL, tagName string) (string, error) {
	u, err := url.Parse(spoolURL)
	if err != nil {
		return "", fmt.Errorf("invalid spool URL: %w", err)
	}

	if strings.Contains(u.Host, "github.com") {
		return buildGitHubRawURL(u, tagName)
	}

	// Non-GitHub: direct path append
	u.Path = strings.TrimRight(u.Path, "/") + "/tags/" + tagName + ".json"
	return u.String(), nil
}

// buildGitHubRawURL converts a parsed GitHub URL to a raw.githubusercontent.com URL.
func buildGitHubRawURL(u *url.URL, tagName string) (string, error) {
	path := strings.Trim(u.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("github URL missing owner/repo: %s", u.String())
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/master/tags/%s.json",
		owner, repo, tagName)
	return rawURL, nil
}
