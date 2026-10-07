package spool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var errBlockedDestination = errors.New("spool: destination blocked")

type spoolResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type spoolDialFunc func(context.Context, string, string) (net.Conn, error)

type spoolRequestContextKey struct{}

type fetcher struct {
	resolver spoolResolver
	dial     spoolDialFunc
	client   *http.Client
	hosts    []string
}

func newFetcher(hosts []string, resolver spoolResolver, dial spoolDialFunc) *fetcher {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	f := &fetcher{hosts: append([]string(nil), hosts...), resolver: resolver, dial: dial}
	f.client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			// A proxy would resolve the destination again and defeat IP pinning.
			Proxy:                 nil,
			DialContext:           f.dialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("spool: too many redirects")
			}
			return f.validateURL(req.URL)
		},
	}
	return f
}

func (f *fetcher) validateURL(u *url.URL) error {
	if u.Scheme != "https" || u.Opaque != "" || u.Host == "" {
		return fmt.Errorf("%w: an HTTPS URL with a host is required", errBlockedDestination)
	}
	if u.User != nil {
		return fmt.Errorf("%w: URL userinfo is not permitted", errBlockedDestination)
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%w: invalid port", errBlockedDestination)
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("%w: empty port", errBlockedDestination)
	}
	if len(f.hosts) > 0 {
		allowed := false
		for _, host := range f.hosts {
			if u.Host == host {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("%w: host %q is not in the spool allowed-hosts list", errBlockedDestination, u.Host)
		}
	}
	return f.checkHost(u.Hostname())
}

func (f *fetcher) checkHost(host string) error {
	if addr, err := netip.ParseAddr(host); err == nil {
		return checkSpoolAddress(addr, len(f.hosts) > 0)
	}
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h == "" || len(h) > 253 || strings.ContainsAny(h, ":%") {
		return fmt.Errorf("%w: invalid host", errBlockedDestination)
	}
	switch h {
	case "metadata", "metadata.google.internal", "metadata.goog", "instance-data", "instance-data.ec2.internal", "metadata.azure.internal":
		return fmt.Errorf("%w: metadata host", errBlockedDestination)
	case "localhost", "localhost.localdomain", "ip6-localhost", "ip6-loopback":
		if len(f.hosts) == 0 {
			return fmt.Errorf("%w: loopback host", errBlockedDestination)
		}
	}
	labels := strings.Split(h, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("%w: invalid host", errBlockedDestination)
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' {
				return fmt.Errorf("%w: invalid host", errBlockedDestination)
			}
		}
	}
	last := labels[len(labels)-1]
	if strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x") {
		return fmt.Errorf("%w: non-canonical IP literal", errBlockedDestination)
	}
	return nil
}

func (f *fetcher) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// Bound resolution and all attempts together, even though net/http removes
	// the request deadline before calling DialContext. A fetch ending sooner
	// must also cancel this work instead of leaving a detached dial behind.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if requestCtx, ok := ctx.Value(spoolRequestContextKey{}).(context.Context); ok {
		stop := context.AfterFunc(requestCtx, cancel) //nolint:contextcheck // Restore the request lifetime removed by net/http's detached dial context.
		defer stop()
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if err := f.checkHost(host); err != nil {
		return nil, err
	}
	var addresses []netip.Addr
	if addr, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{addr}
	} else {
		ipNetwork := "ip"
		switch network {
		case "tcp4":
			ipNetwork = "ip4"
		case "tcp6":
			ipNetwork = "ip6"
		}
		addresses, err = f.resolver.LookupNetIP(ctx, ipNetwork, host)
		if err != nil {
			return nil, fmt.Errorf("spool: resolve host: %w", err)
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("spool: host has no addresses")
	}
	// Validate the entire answer before dialing: a mixed answer must not make a
	// forbidden address a fallback if the public endpoint fails.
	for _, addr := range addresses {
		if err := checkSpoolAddress(addr, len(f.hosts) > 0); err != nil {
			return nil, err
		}
	}
	return f.dialChecked(ctx, network, port, addresses)
}

// dialChecked staggers attempts without letting a stalled address consume the
// entire timeout. Interleaving families preserves fast IPv4/IPv6 fallback while
// every attempt still targets a literal address from the checked DNS answer.
func (f *fetcher) dialChecked(ctx context.Context, network, port string, addresses []netip.Addr) (net.Conn, error) {
	var primary, fallback []netip.Addr
	for _, addr := range addresses {
		if addr.Unmap().Is4() == addresses[0].Unmap().Is4() {
			primary = append(primary, addr)
		} else {
			fallback = append(fallback, addr)
		}
	}
	ordered := make([]netip.Addr, 0, len(addresses))
	for len(primary) > 0 || len(fallback) > 0 {
		if len(primary) > 0 {
			ordered = append(ordered, primary[0])
			primary = primary[1:]
		}
		if len(fallback) > 0 {
			ordered = append(ordered, fallback[0])
			fallback = fallback[1:]
		}
	}
	type dialResult struct {
		conn net.Conn
		err  error
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	results := make(chan dialResult)
	next, pending := 0, 0
	start := func() {
		addr := ordered[next]
		next++
		pending++
		go func() {
			var conn net.Conn
			err := ctx.Err()
			if err == nil {
				conn, err = f.dial(ctx, network, net.JoinHostPort(addr.String(), port))
			}
			select {
			case results <- dialResult{conn, err}:
			case <-done:
				if conn != nil {
					_ = conn.Close()
				}
			}
		}()
	}
	start()
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-results:
			pending--
			if result.err == nil {
				return result.conn, nil
			}
			lastErr = result.err
			if next < len(ordered) {
				start()
				timer.Reset(300 * time.Millisecond)
			} else if pending == 0 {
				return nil, lastErr
			}
		case <-timer.C:
			if next < len(ordered) {
				start()
				timer.Reset(300 * time.Millisecond)
			}
		}
	}
}

var forbiddenSpoolRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"), // Teredo can tunnel to arbitrary IPv4.
	netip.MustParsePrefix("fec0::/10"),
	// Network-specific NAT64 may use different embedded IPv4 positions. There
	// is no configured translation prefix here with which to validate them.
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

var (
	spoolCGNAT  = netip.MustParsePrefix("100.64.0.0/10")
	spoolNAT64  = netip.MustParsePrefix("64:ff9b::/96")
	spool6To4   = netip.MustParsePrefix("2002::/16")
	spoolIMDSv4 = netip.MustParseAddr("100.100.100.200")
	spoolIMDSv6 = netip.MustParseAddr("fd00:ec2::254")
)

func checkSpoolAddress(addr netip.Addr, allowPrivate bool) error {
	deny := func() error { return fmt.Errorf("%w: address %q", errBlockedDestination, addr.String()) }
	if !addr.IsValid() || addr.Zone() != "" {
		return deny()
	}
	a := addr.Unmap()
	if a == spoolIMDSv4 || a == spoolIMDSv6 || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return deny()
	}
	if a.IsLoopback() {
		if allowPrivate {
			return nil
		}
		return deny()
	}
	for _, prefix := range forbiddenSpoolRanges {
		if prefix.Contains(a) {
			return deny()
		}
	}
	if a.Is6() {
		b := a.As16()
		if spoolNAT64.Contains(a) {
			return checkSpoolAddress(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), allowPrivate)
		}
		if spool6To4.Contains(a) {
			return checkSpoolAddress(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), allowPrivate)
		}
	}
	if !a.IsGlobalUnicast() || (!allowPrivate && (a.IsPrivate() || spoolCGNAT.Contains(a))) {
		return deny()
	}
	return nil
}
