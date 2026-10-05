// Package netguard guards outbound HTTP requests to URLs that come from
// configuration or users, so a module cannot be turned into a server-side
// request forgery (SSRF) proxy into the host, the LAN, or a cloud metadata
// service. It implements RULE-VAL-2 (docs/specs/FRD.md) / NFR-SEC-009: outbound
// HTTP to user-configured URLs (indexers, lists, spool, webhooks) goes through
// host allow-lists or SSRF guards.
//
// Two profiles are provided:
//
//   - UserURL (the zero value, strictest) is for URLs a user or third party
//     supplies: http/https only, no userinfo, and every loopback, private
//     (RFC 1918, ULA), CGNAT, link-local, multicast, reserved, documentation
//     and cloud-metadata destination is refused. Single-label and
//     intranet-suffix host names (.local, .lan, .internal, .home.arpa) are
//     refused too.
//   - Integration is for service endpoints an administrator configures (a
//     download client, a media server). Private LAN addresses are refused
//     unless Options.AllowPrivate is set, loopback unless
//     Options.AllowLoopback is set; link-local, multicast, unspecified and
//     cloud-metadata addresses are always refused.
//
// ValidateURL checks a URL without touching the network (use it when a URL is
// saved). NewClient returns an *http.Client that enforces the same policy on
// every request and redirect hop and, at dial time, resolves the host itself,
// checks every resolved address and connects only to a checked address, so a
// DNS answer that changes between validation and connection (DNS rebinding)
// cannot reach a blocked address.
//
// Non-canonical IPv4 literals (decimal 2130706433, octal 0177.0.0.1, hex
// 0x7f.1, short 127.1) are refused outright rather than interpreted, and
// IPv4-mapped, NAT64 and 6to4 IPv6 addresses are checked by their embedded
// IPv4 address.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Profile selects the destination policy.
type Profile int

const (
	// UserURL is for URLs supplied by users or third parties. It is the zero
	// value, so an unset profile gets the strictest policy.
	UserURL Profile = iota
	// Integration is for administrator-configured service endpoints.
	Integration
)

// String returns the profile name.
func (p Profile) String() string {
	switch p {
	case UserURL:
		return "user-url"
	case Integration:
		return "integration"
	default:
		return "profile(" + strconv.Itoa(int(p)) + ")"
	}
}

// Resolver resolves host names. *net.Resolver satisfies it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options tunes a profile. The zero value is the strictest setting.
type Options struct {
	// Resolver resolves host names at dial time; nil uses net.DefaultResolver.
	// Tests inject a fake to simulate DNS answers.
	Resolver Resolver
	// AllowedHosts, when non-empty, restricts requests to these hosts. An entry
	// is "example.com" (exactly that host), "*.example.com" or ".example.com"
	// (any subdomain, not the apex), optionally with ":port" to also pin the
	// port. Matching is case-insensitive. The allow-list narrows the address
	// policy; it never permits a blocked address.
	AllowedHosts []string
	// Timeout bounds a whole request including redirects and reading the body
	// (http.Client.Timeout). Zero means 30 s.
	Timeout time.Duration
	// MaxRedirects caps redirect hops. Zero means 5; negative disables
	// redirects (the 3xx response is returned to the caller).
	MaxRedirects int
	// AllowLoopback permits loopback destinations (127.0.0.0/8, ::1,
	// localhost) for the Integration profile. Ignored for UserURL.
	AllowLoopback bool
	// AllowPrivate permits private LAN destinations (RFC 1918, CGNAT
	// 100.64.0.0/10, IPv6 ULA fc00::/7) for the Integration profile.
	// Ignored for UserURL.
	AllowPrivate bool
	// RequireHTTPS refuses plain http URLs.
	RequireHTTPS bool
	// UseEnvProxy routes requests through the HTTP(S)_PROXY environment
	// proxy. Off by default because a proxy would perform the connection
	// itself, bypassing the dial-time address check. When on, the target
	// host is resolved and checked before the request is handed to the proxy
	// (not rebinding-safe: the proxy resolves again) and the proxy address
	// itself is not checked.
	UseEnvProxy bool
}

// ErrBlocked is the sentinel every refusal wraps; test with errors.Is.
var ErrBlocked = errors.New("netguard: blocked")

// BlockedError reports why a URL, host or address was refused.
type BlockedError struct {
	// Target is the URL, host or address that was refused.
	Target string
	// Reason is a short human-readable cause, e.g. "loopback address".
	Reason string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("netguard: blocked %q: %s", e.Target, e.Reason)
}

// Unwrap makes errors.Is(err, ErrBlocked) true.
func (e *BlockedError) Unwrap() error { return ErrBlocked }

func blocked(target, format string, args ...any) error {
	return &BlockedError{Target: target, Reason: fmt.Sprintf(format, args...)}
}

// ValidateURL checks raw against profile p without network access: syntax,
// scheme (http/https), userinfo (refused for UserURL), port, IP literal or
// host name policy, and opts.AllowedHosts. Host names are not resolved; use a
// client from NewClient to enforce the policy on resolved addresses.
func ValidateURL(raw string, p Profile, opts Options) error {
	_, err := parseAndValidate(raw, p, &opts)
	return err
}

func parseAndValidate(raw string, p Profile, opts *Options) (*url.URL, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return nil, blocked(raw, "empty URL or surrounding whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, blocked(raw, "invalid URL: %v", err)
	}
	return u, validateParsed(u, p, opts)
}

func validateParsed(u *url.URL, p Profile, opts *Options) error {
	target := u.Redacted()
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if opts.RequireHTTPS {
			return blocked(target, "https required")
		}
	default:
		return blocked(target, "scheme %q not allowed (http/https only)", u.Scheme)
	}
	if u.Opaque != "" || u.Host == "" {
		return blocked(target, "URL has no host")
	}
	if u.User != nil && p != Integration {
		return blocked(target, "userinfo not allowed in URL")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return blocked(target, "invalid port %q", port)
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return blocked(target, "empty port")
	}
	if err := CheckHost(u.Hostname(), p, *opts); err != nil {
		return err
	}
	if len(opts.AllowedHosts) > 0 && !hostAllowed(u, opts.AllowedHosts) {
		return blocked(target, "host not in allow-list")
	}
	return nil
}

// CheckHost checks a bare host (IP literal or DNS name, no port) against the
// profile without resolving it. IPv6 literals may be given with or without
// brackets.
func CheckHost(host string, p Profile, opts Options) error {
	h := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if h == "" {
		return blocked(host, "empty host")
	}
	if strings.Contains(h, ":") || strings.Contains(h, "%") {
		a, err := netip.ParseAddr(h)
		if err != nil {
			return blocked(host, "invalid IP literal")
		}
		return CheckAddr(a, p, opts)
	}
	if a, err := netip.ParseAddr(h); err == nil {
		return CheckAddr(a, p, opts)
	}
	return checkHostname(h, p, &opts)
}

var (
	loopbackNames = map[string]bool{
		"localhost": true, "localhost.localdomain": true,
		"ip6-localhost": true, "ip6-loopback": true,
	}
	metadataNames = map[string]bool{
		"metadata": true, "metadata.google.internal": true, "metadata.goog": true,
		"instance-data": true, "instance-data.ec2.internal": true,
		"metadata.azure.internal": true,
	}
	intranetSuffixes = []string{".local", ".localdomain", ".internal", ".lan", ".home.arpa", ".intranet"}
)

func checkHostname(raw string, p Profile, opts *Options) error {
	h := strings.TrimSuffix(strings.ToLower(raw), ".")
	if h == "" || len(h) > 253 {
		return blocked(raw, "invalid host name")
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 {
			return blocked(raw, "invalid host name")
		}
		for _, r := range l {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
				return blocked(raw, "invalid character in host name")
			}
		}
	}
	// A numeric last label is never a DNS name; it is an IPv4 literal in a
	// non-canonical form (2130706433, 0x7f.1, 0177.0.0.1, 127.1) that some
	// resolvers would interpret. Refuse rather than guess.
	if isNumericLabel(labels[len(labels)-1]) {
		return blocked(raw, "non-canonical IP literal")
	}
	if metadataNames[h] {
		return blocked(raw, "cloud metadata host")
	}
	if loopbackNames[h] || strings.HasSuffix(h, ".localhost") {
		if p == Integration && opts.AllowLoopback {
			return nil
		}
		return blocked(raw, "loopback host name")
	}
	if p != Integration {
		if len(labels) == 1 {
			return blocked(raw, "single-label (intranet) host name")
		}
		for _, s := range intranetSuffixes {
			if strings.HasSuffix(h, s) {
				return blocked(raw, "intranet host name")
			}
		}
	}
	return nil
}

func isNumericLabel(l string) bool {
	if strings.HasPrefix(l, "0x") {
		for _, r := range l[2:] {
			if !strings.ContainsRune("0123456789abcdef", r) {
				return false
			}
		}
		return true
	}
	for _, r := range l {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

var (
	metadataAddrs = map[netip.Addr]bool{
		netip.MustParseAddr("169.254.169.254"): true, // AWS, GCP, Azure, OCI, DO
		netip.MustParseAddr("169.254.170.2"):   true, // AWS ECS task metadata
		netip.MustParseAddr("100.100.100.200"): true, // Alibaba Cloud
		netip.MustParseAddr("fd00:ec2::254"):   true, // AWS IPv6 IMDS
	}
	// Never a legitimate destination for either profile.
	alwaysBlocked = mustPrefixes(
		"0.0.0.0/8",      // "this network", unspecified
		"169.254.0.0/16", // link-local
		"224.0.0.0/4",    // multicast
		"240.0.0.0/4",    // reserved, broadcast
		"::/96",          // unspecified, IPv4-compatible (deprecated)
		"100::/64",       // discard-only
		"2001::/32",      // Teredo (tunnels to arbitrary IPv4)
		"fe80::/10",      // link-local
		"fec0::/10",      // site-local (deprecated)
		"ff00::/8",       // multicast
	)
	privateRanges = mustPrefixes(
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // RFC 1918
		"100.64.0.0/10", // CGNAT
		"fc00::/7",      // ULA
	)
	// Additionally refused for UserURL: special-purpose and documentation
	// ranges a public URL never legitimately points at.
	userSpecial = mustPrefixes(
		"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32",
	)
	globalUnicast6 = netip.MustParsePrefix("2000::/3")
	nat64          = mustPrefixes("64:ff9b::/96", "64:ff9b:1::/48")
	sixToFour      = netip.MustParsePrefix("2002::/16")
)

func inAny(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// CheckAddr checks a resolved or literal IP address against the profile.
func CheckAddr(addr netip.Addr, p Profile, opts Options) error {
	return checkAddr(addr, p, &opts, 0)
}

func checkAddr(addr netip.Addr, p Profile, opts *Options, depth int) error {
	target := addr.String()
	if !addr.IsValid() {
		return blocked(target, "invalid address")
	}
	if addr.Zone() != "" {
		return blocked(target, "scoped (zoned) IPv6 address")
	}
	a := addr.Unmap()
	if metadataAddrs[a] {
		return blocked(target, "cloud metadata address")
	}
	if a.IsLoopback() {
		if p == Integration && opts.AllowLoopback {
			return nil
		}
		return blocked(target, "loopback address")
	}
	if inAny(a, alwaysBlocked) {
		return blocked(target, "link-local, multicast, unspecified or reserved address")
	}
	if a.Is6() && depth == 0 {
		// Addresses that embed an IPv4 destination are judged by it.
		if inAny(a, nat64) {
			b := a.As16()
			return checkAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), p, opts, 1)
		}
		if sixToFour.Contains(a) {
			b := a.As16()
			return checkAddr(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), p, opts, 1)
		}
	}
	if inAny(a, privateRanges) {
		if p == Integration && opts.AllowPrivate {
			return nil
		}
		return blocked(target, "private network address")
	}
	if p != Integration {
		if inAny(a, userSpecial) {
			return blocked(target, "special-purpose address")
		}
		if a.Is6() && !globalUnicast6.Contains(a) {
			return blocked(target, "non-global IPv6 address")
		}
	}
	return nil
}

func hostAllowed(u *url.URL, allow []string) bool {
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	for _, entry := range allow {
		e := strings.ToLower(strings.TrimSpace(entry))
		if e == "" {
			continue
		}
		eHost, ePort := e, ""
		if h, pt, err := net.SplitHostPort(e); err == nil {
			eHost, ePort = h, pt
		}
		eHost = strings.TrimSuffix(strings.Trim(eHost, "[]"), ".")
		if ePort != "" && ePort != port {
			continue
		}
		switch {
		case strings.HasPrefix(eHost, "*."):
			if strings.HasSuffix(host, eHost[1:]) {
				return true
			}
		case strings.HasPrefix(eHost, "."):
			if strings.HasSuffix(host, eHost) {
				return true
			}
		case host == eHost:
			return true
		}
	}
	return false
}
