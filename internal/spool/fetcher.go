package spool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// DefaultSpoolURL is the official MuxCore spool.
const DefaultSpoolURL = "https://github.com/Muxcore-Media/spool"

// client is a reusable HTTP client for spool fetching.
// Does NOT follow redirects: an HTTPS spool could redirect to an internal
// HTTP endpoint (SSRF vector). See custom CheckRedirect.
var client = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("spool: too many redirects")
		}
		// Reject redirects to non-HTTPS URLs (SSRF protection).
		if req.URL.Scheme != "https" {
			return fmt.Errorf("spool: redirect to non-HTTPS URL %q rejected (SSRF protection)", req.URL.String())
		}
		// Block redirects to private IP ranges.
		redirectHost := stripPort(req.URL.Host)
		if ip := net.ParseIP(redirectHost); ip != nil && isPrivateIP(ip) {
			return fmt.Errorf("spool: redirect to private IP %q blocked (SSRF protection)", redirectHost)
		}
		// Reject redirects to hosts not in the allow-list.
		allowedHostsMu.RLock()
		defer allowedHostsMu.RUnlock()
		if len(allowedHosts) > 0 {
			hostAllowed := false
			for _, h := range allowedHosts {
				if req.URL.Host == h {
					hostAllowed = true
					break
				}
			}
			if !hostAllowed {
				return fmt.Errorf("spool: redirect to host %q not in allowed-hosts list (SSRF protection)", req.URL.Host)
			}
		}
		return nil
	},
}

var validTagName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// allowedHosts restricts which hosts FetchTag will connect to.
// Empty slice means all hosts are allowed (backward compatible).
var allowedHosts []string
var allowedHostsMu sync.RWMutex

// SetAllowedHosts sets the list of permitted spool hosts for SSRF protection.
// Only hosts in this list (matched by exact host string) are allowed for
// non-official spool URLs. Pass nil or empty to allow all hosts.
func SetAllowedHosts(hosts []string) {
	allowedHostsMu.Lock()
	defer allowedHostsMu.Unlock()
	if len(hosts) == 0 {
		allowedHosts = nil
		return
	}
	allowedHosts = make([]string, len(hosts))
	copy(allowedHosts, hosts)
}

// FetchTag fetches a tag definition from a spool URL by appending
// "/tags/{tagName}.json" to the base URL and parsing the JSON response.
// spoolURL is the base URL (e.g., "https://myspool.example.com/spool").
// tagName is the tag to fetch (e.g., "default").
// Security: rejects non-HTTPS URLs, blocks private IPs, caps response at 1MB,
// validates tagName, and enforces host allow-list when configured.
func FetchTag(ctx context.Context, spoolURL, tagName string) (*contracts.TagDefinition, error) {
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
	fetchURL := baseURL.JoinPath("tags", tagName+".json").String()

	u, err := url.Parse(fetchURL)
	if err != nil {
		return nil, fmt.Errorf("spool: invalid URL %q: %w", fetchURL, err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("spool: only HTTPS URLs are allowed, got %q", u.Scheme)
	}

	// Enforce allowed host allow-list when configured (SSRF protection).
	allowedHostsMu.RLock()
	hasAllowList := len(allowedHosts) > 0
	hostAllowed := false
	if hasAllowList {
		for _, h := range allowedHosts {
			if u.Host == h {
				hostAllowed = true
				break
			}
		}
	}
	allowedHostsMu.RUnlock()
	if hasAllowList && !hostAllowed {
		return nil, fmt.Errorf("spool: host %q is not in the spool allowed-hosts list", u.Host)
	}

	// When no explicit allow-list is configured, block private IP ranges
	// as a safety net (SSRF protection). An explicit allow-list entry
	// overrides this check — operators who add a private host to the list
	// are assumed to have a legitimate local spool.
	if !hasAllowList {
		if err2 := blockPrivateHost(ctx, u.Host); err2 != nil {
			return nil, err2
		}
	}

	// Warn when using a non-official spool.
	if spoolURL != DefaultSpoolURL {
		fmt.Fprintf(os.Stderr, "\n⚠️  WARNING: Using a non-official spool: %s\n", spoolURL)
		fmt.Fprintf(os.Stderr, "   Modules from third-party spools run with the same privileges as the core process.\n")
		fmt.Fprintf(os.Stderr, "   Only use spools from sources you trust. See https://opencode.ai for details.\n\n")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("spool: create request: %w", err)
	}
	resp, err := client.Do(req)
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

// stripPort removes the port from a host:port string.
func stripPort(hostport string) string {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return h
}

// blockPrivateHost resolves the host and rejects private, loopback,
// link-local, and unspecified IP addresses (SSRF protection).
func blockPrivateHost(ctx context.Context, host string) error {
	hostOnly := stripPort(host)
	if ip := net.ParseIP(hostOnly); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("spool: private IP %q is not allowed (SSRF protection)", hostOnly)
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, hostOnly)
	if err != nil {
		return fmt.Errorf("spool: host lookup failed for %q: %w", hostOnly, err)
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && isPrivateIP(ip) {
			return fmt.Errorf("spool: host %q resolves to private IP %q — blocked (SSRF protection)", hostOnly, a)
		}
	}
	return nil
}

// isPrivateIP returns true if the IP is in a private, loopback, link-local,
// or unspecified range. These should never be targets for spool fetching.
func isPrivateIP(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}
