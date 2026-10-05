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
	"github.com/Muxcore-Media/core/sdk/go/module/netguard"
)

// DefaultSpoolURL is the official MuxCore spool.
const DefaultSpoolURL = "https://github.com/Muxcore-Media/spool"

// defaultClient is the production sentinel. FetchTag builds a netguard client
// unless a test replaces client with one that trusts a local certificate.
var defaultClient = &http.Client{Timeout: 10 * time.Second}

// client is overridable by tests.
var client = defaultClient

func spoolGuard() (netguard.Profile, netguard.Options) {
	allowedHostsMu.RLock()
	hosts := append([]string(nil), allowedHosts...)
	allowedHostsMu.RUnlock()
	opts := netguard.Options{RequireHTTPS: true, Timeout: 10 * time.Second}
	if len(hosts) == 0 {
		return netguard.UserURL, opts
	}
	// An explicit allow-list may name a LAN spool. Link-local and cloud
	// metadata stay refused even when listed.
	opts.AllowPrivate = true
	opts.AllowLoopback = true
	opts.AllowedHosts = hosts
	return netguard.Integration, opts
}

func fetchHTTPClient() *http.Client {
	if client != defaultClient {
		return client
	}
	profile, opts := spoolGuard()
	return netguard.NewClient(profile, opts)
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

	profile, opts := spoolGuard()
	if err := netguard.ValidateURL(fetchURL, profile, opts); err != nil {
		return nil, fmt.Errorf("spool: %w", err)
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
	resp, err := fetchHTTPClient().Do(req)
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
