package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

const (
	defaultTimeout      = 30 * time.Second
	defaultMaxRedirects = 5
	dialTimeout         = 10 * time.Second
)

// envProxy is the proxy function used with Options.UseEnvProxy; tests replace
// it because http.ProxyFromEnvironment caches the environment.
var envProxy = http.ProxyFromEnvironment

// dialHook, when non-nil, replaces the network dial of an already-checked
// address. Tests use it to route a "public" address to a local test server.
var dialHook func(ctx context.Context, network, addr string) (net.Conn, error)

type proxiedKey struct{}

type guard struct {
	resolver Resolver
	dial     func(ctx context.Context, network, addr string) (net.Conn, error)
	opts     Options
	profile  Profile
}

// NewClient returns an HTTP client that enforces profile p on every request:
// each request URL and redirect hop is validated as by ValidateURL, and
// connections are made only to addresses that were resolved and checked at
// dial time. Redirects are capped (Options.MaxRedirects, default 5), the
// whole request is bounded by Options.Timeout (default 30 s), and no proxy is
// used unless Options.UseEnvProxy is set.
//
// Refusals are returned as errors wrapping ErrBlocked (inside *url.Error).
func NewClient(p Profile, opts Options) *http.Client {
	opts.AllowedHosts = append([]string(nil), opts.AllowedHosts...)
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.MaxRedirects == 0 {
		opts.MaxRedirects = defaultMaxRedirects
	}
	g := &guard{opts: opts, profile: p, resolver: opts.Resolver}
	if g.resolver == nil {
		g.resolver = net.DefaultResolver
	}
	dt := min(dialTimeout, opts.Timeout)
	dialer := &net.Dialer{Timeout: dt, KeepAlive: 30 * time.Second}
	g.dial = dialer.DialContext
	if dialHook != nil {
		g.dial = dialHook
	}
	tr := &http.Transport{
		DialContext:           g.dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   dt,
		ExpectContinueTimeout: time.Second,
	}
	if opts.UseEnvProxy {
		tr.Proxy = envProxy
	}
	return &http.Client{
		Transport:     &guardTransport{base: tr, g: g},
		CheckRedirect: g.checkRedirect,
		Timeout:       opts.Timeout,
	}
}

func (g *guard) checkRedirect(req *http.Request, via []*http.Request) error {
	if g.opts.MaxRedirects < 0 {
		return http.ErrUseLastResponse
	}
	if len(via) >= g.opts.MaxRedirects {
		return fmt.Errorf("netguard: stopped after %d redirects", len(via))
	}
	return validateParsed(req.URL, g.profile, &g.opts)
}

// dialContext resolves the host itself, refuses the connection if any
// resolved address is blocked, and dials the checked addresses directly so
// the address that was checked is the address that is connected to.
func (g *guard) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if proxied, _ := ctx.Value(proxiedKey{}).(bool); proxied {
		// Connection to the operator-configured proxy; the target was
		// checked in RoundTrip.
		return g.dial(ctx, network, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := g.resolve(ctx, network, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, a := range addrs {
		c, err := g.dial(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// resolve returns the checked addresses for host; an error if the host is
// refused, does not resolve, or any address it resolves to is blocked.
func (g *guard) resolve(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if err := CheckHost(host, g.profile, g.opts); err != nil {
		return nil, err
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	ipNet := "ip"
	switch network {
	case "tcp4", "udp4":
		ipNet = "ip4"
	case "tcp6", "udp6":
		ipNet = "ip6"
	}
	addrs, err := g.resolver.LookupNetIP(ctx, ipNet, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("netguard: no addresses for %q", host)
	}
	for _, a := range addrs {
		// One blocked answer refuses the host: a mixed public/private answer
		// is a rebinding signal, not a fallback list.
		if err := CheckAddr(a, g.profile, g.opts); err != nil {
			var be *BlockedError
			if errors.As(err, &be) {
				be.Reason = fmt.Sprintf("%s resolves to %s", host, be.Reason)
			}
			return nil, err
		}
	}
	return addrs, nil
}

type guardTransport struct {
	base *http.Transport
	g    *guard
}

// RoundTrip validates the request URL (covers the first request and every
// redirect hop) before handing it to the base transport.
func (t *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	fail := func(err error) (*http.Response, error) {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if err := validateParsed(req.URL, t.g.profile, &t.g.opts); err != nil {
		return fail(err)
	}
	if t.base.Proxy != nil {
		proxyURL, err := t.base.Proxy(req)
		if err != nil {
			return fail(err)
		}
		if proxyURL != nil {
			if _, err := t.g.resolve(req.Context(), "tcp", req.URL.Hostname()); err != nil {
				return fail(err)
			}
			req = req.WithContext(context.WithValue(req.Context(), proxiedKey{}, true))
		}
	}
	return t.base.RoundTrip(req)
}

// CloseIdleConnections lets http.Client.CloseIdleConnections reach the pool.
func (t *guardTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
