package netguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestValidateURL(t *testing.T) {
	lan := Options{AllowPrivate: true}
	lo := Options{AllowLoopback: true}
	cases := []struct {
		url  string
		p    Profile
		opts Options
		ok   bool
	}{
		// scheme / syntax
		{"https://example.com/x", UserURL, Options{}, true},
		{"http://example.com/x", UserURL, Options{}, true},
		{"http://example.com/x", UserURL, Options{RequireHTTPS: true}, false},
		{"ftp://example.com/", UserURL, Options{}, false},
		{"file:///etc/passwd", UserURL, Options{}, false},
		{"gopher://example.com/", Integration, lan, false},
		{"example.com/x", UserURL, Options{}, false},
		{"", UserURL, Options{}, false},
		{" https://example.com", UserURL, Options{}, false},
		{"https://example.com:0/", UserURL, Options{}, false},
		{"https://example.com:99999/", UserURL, Options{}, false},
		{"https://example.com:/", UserURL, Options{}, false},
		{"https://user:pw@example.com/", UserURL, Options{}, false},
		{"https://user:pw@example.com/", Integration, Options{}, true},
		{"https://exa mple.com/", UserURL, Options{}, false},
		// public literals
		{"https://93.184.215.14/", UserURL, Options{}, true},
		{"https://[2606:2800:21f:cb07:6820:80da:af6b:8b2c]/", UserURL, Options{}, true},
		// loopback
		{"http://127.0.0.1/", UserURL, Options{}, false},
		{"http://127.0.0.1/", UserURL, lo, false}, // AllowLoopback ignored for UserURL
		{"http://127.0.0.1/", Integration, Options{}, false},
		{"http://127.0.0.1:8096/", Integration, lo, true},
		{"http://127.9.9.9/", UserURL, Options{}, false},
		{"http://[::1]/", UserURL, Options{}, false},
		{"http://[::1]:8080/", Integration, lo, true},
		{"http://[::ffff:127.0.0.1]/", UserURL, Options{}, false},
		{"http://[::ffff:7f00:1]/", UserURL, Options{}, false},
		{"http://localhost/", UserURL, Options{}, false},
		{"http://LOCALHOST./", UserURL, Options{}, false},
		{"http://foo.localhost/", UserURL, Options{}, false},
		{"http://localhost:8080/", Integration, Options{}, false},
		{"http://localhost:8080/", Integration, lo, true},
		{"http://ip6-localhost/", UserURL, Options{}, false},
		// non-canonical IPv4 literals
		{"http://2130706433/", UserURL, Options{}, false},
		{"http://2130706433/", Integration, lo, false},
		{"http://0177.0.0.1/", UserURL, Options{}, false},
		{"http://0x7f.0.0.1/", UserURL, Options{}, false},
		{"http://0x7f000001/", UserURL, Options{}, false},
		{"http://127.1/", UserURL, Options{}, false},
		{"http://1.2.3.0x4/", UserURL, Options{}, false},
		{"http://017700000001/", UserURL, Options{}, false},
		// private LAN
		{"http://10.0.0.5/", UserURL, Options{}, false},
		{"http://10.0.0.5/", UserURL, lan, false},
		{"http://10.0.0.5/", Integration, Options{}, false},
		{"http://10.0.0.5:8080/", Integration, lan, true},
		{"http://172.16.1.1/", UserURL, Options{}, false},
		{"http://172.32.1.1/", UserURL, Options{}, true},
		{"http://192.168.1.10/", Integration, lan, true},
		{"http://[::ffff:192.168.1.10]/", UserURL, Options{}, false},
		{"http://100.64.0.1/", UserURL, Options{}, false},
		{"http://100.64.0.1/", Integration, lan, true},
		{"http://[fd12:3456::1]/", UserURL, Options{}, false},
		{"http://[fd12:3456::1]/", Integration, lan, true},
		{"http://[64:ff9b::a00:1]/", UserURL, Options{}, false},    // NAT64 of 10.0.0.1
		{"http://[64:ff9b::5db8:d70e]/", UserURL, Options{}, true}, // NAT64 of 93.184.215.14
		{"http://[2002:7f00:1::]/", UserURL, Options{}, false},     // 6to4 of 127.0.0.1
		{"http://[2002:a9fe:a9fe::]/", Integration, lan, false},    // 6to4 of metadata
		{"http://[2001:0:4136:e378::1]/", Integration, lan, false}, // Teredo
		// always blocked, even with every allowance
		{"http://169.254.169.254/latest/meta-data/", Integration, Options{AllowPrivate: true, AllowLoopback: true}, false},
		{"http://169.254.1.1/", Integration, lan, false},
		{"http://[::ffff:169.254.169.254]/", Integration, lan, false},
		{"http://[fd00:ec2::254]/", Integration, lan, false},
		{"http://100.100.100.200/", Integration, lan, false},
		{"http://metadata.google.internal/", Integration, lan, false},
		{"http://METADATA.google.internal./", Integration, lan, false},
		{"http://[fe80::1]/", Integration, lan, false},
		{"http://[fe80::1%25eth0]/", Integration, lan, false},
		{"http://0.0.0.0/", Integration, lo, false},
		{"http://[::]/", Integration, lo, false},
		{"http://224.0.0.1/", Integration, lan, false},
		{"http://[ff02::1]/", Integration, lan, false},
		{"http://255.255.255.255/", Integration, lan, false},
		// UserURL special-purpose / intranet names
		{"http://192.0.2.1/", UserURL, Options{}, false},
		{"http://192.0.2.1/", Integration, Options{}, true},
		{"http://[2001:db8::1]/", UserURL, Options{}, false},
		{"http://nas/", UserURL, Options{}, false},
		{"http://nas/", Integration, Options{}, true},
		{"http://printer.local/", UserURL, Options{}, false},
		{"http://router.home.arpa/", UserURL, Options{}, false},
		{"http://jellyfin.lan:8096/", Integration, Options{}, true},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%s", c.p, c.url), func(t *testing.T) {
			err := ValidateURL(c.url, c.p, c.opts)
			if c.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !c.ok {
				if err == nil {
					t.Fatal("want blocked, got ok")
				}
				if !errors.Is(err, ErrBlocked) {
					t.Fatalf("error %v does not wrap ErrBlocked", err)
				}
			}
		})
	}
}

func TestAllowedHosts(t *testing.T) {
	opts := Options{AllowedHosts: []string{"api.example.com", "*.cdn.example.net", ".lists.example.org", "pinned.example.com:8443"}}
	cases := map[string]bool{
		"https://api.example.com/x":         true,
		"https://API.Example.com./x":        true,
		"https://evil.api.example.com/":     false,
		"https://example.com/":              false,
		"https://a.cdn.example.net/":        true,
		"https://cdn.example.net/":          false,
		"https://evilcdn.example.net/":      false,
		"https://x.y.lists.example.org/":    true,
		"https://pinned.example.com:8443/":  true,
		"https://pinned.example.com/":       false,
		"https://api.example.com.evil.com/": false,
	}
	for u, want := range cases {
		err := ValidateURL(u, UserURL, opts)
		if (err == nil) != want {
			t.Errorf("%s: want ok=%v, got %v", u, want, err)
		}
	}
	// The allow-list never re-enables a blocked address.
	if err := ValidateURL("http://127.0.0.1/", UserURL, Options{AllowedHosts: []string{"127.0.0.1"}}); err == nil {
		t.Fatal("allow-listed loopback must stay blocked for UserURL")
	}
}

func TestBlockedErrorReason(t *testing.T) {
	err := ValidateURL("http://169.254.169.254/", Integration, Options{AllowPrivate: true})
	var be *BlockedError
	if !errors.As(err, &be) || !strings.Contains(be.Reason, "metadata") {
		t.Fatalf("want metadata BlockedError, got %v", err)
	}
	if err := ValidateURL("https://u:secret@example.com/", UserURL, Options{}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("want redacted userinfo refusal, got %v", err)
	}
}

// fakeResolver answers from a map; when seq is set, successive lookups of a
// host return successive answers (DNS rebinding).
type fakeResolver struct {
	answers map[string][][]string
	calls   map[string]int
	mu      sync.Mutex
}

func newResolver(m map[string][]string) *fakeResolver {
	r := &fakeResolver{answers: map[string][][]string{}, calls: map[string]int{}}
	for h, a := range m {
		r.answers[h] = [][]string{a}
	}
	return r
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seq, ok := r.answers[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	i := min(r.calls[host], len(seq)-1)
	r.calls[host]++
	out := make([]netip.Addr, 0, len(seq[i]))
	for _, s := range seq[i] {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

// routeTo makes every checked dial land on srv, recording the address the
// guard chose, so tests can serve "public" addresses from a local server.
func routeTo(t *testing.T, srv *httptest.Server) *[]string {
	t.Helper()
	var mu sync.Mutex
	var dialed []string
	target := srv.Listener.Addr().String()
	dialHook = func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, network, target)
	}
	t.Cleanup(func() { dialHook = nil })
	return &dialed
}

func get(t *testing.T, c *http.Client, u string) (string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func TestClientPublicHostViaResolver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	dialed := routeTo(t, srv)
	res := newResolver(map[string][]string{"indexer.example.com": {"93.184.215.14"}})
	c := NewClient(UserURL, Options{Resolver: res})
	body, err := get(t, c, "http://indexer.example.com/api")
	if err != nil || body != "ok" {
		t.Fatalf("want ok, got %q %v", body, err)
	}
	if len(*dialed) != 1 || (*dialed)[0] != "93.184.215.14:80" {
		t.Fatalf("guard must dial the checked address, dialed %v", *dialed)
	}
}

func TestClientDNSRebinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secret")
	}))
	defer srv.Close()
	dialed := routeTo(t, srv)
	res := newResolver(nil)
	// First answer is public (what a naive pre-check would see); the answer
	// at connection time is loopback.
	res.answers["rebind.example.com"] = [][]string{{"93.184.215.14"}, {"127.0.0.1"}}
	if addrs, _ := res.LookupNetIP(context.Background(), "ip", "rebind.example.com"); addrs[0].String() != "93.184.215.14" {
		t.Fatal("resolver setup")
	}
	c := NewClient(UserURL, Options{Resolver: res})
	_, err := get(t, c, "http://rebind.example.com/")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("rebinding to loopback must be blocked, got %v", err)
	}
	// Mixed answers are refused as a whole.
	res.answers["mixed.example.com"] = [][]string{{"93.184.215.14", "10.0.0.1"}}
	if _, err := get(t, c, "http://mixed.example.com/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("mixed public/private answer must be blocked, got %v", err)
	}
	if len(*dialed) != 0 {
		t.Fatalf("no connection may be made, dialed %v", *dialed)
	}
}

func TestClientRedirectToBlocked(t *testing.T) {
	var target atomic.Value
	target.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = io.WriteString(w, "ok")
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			http.Redirect(w, r, target.Load().(string), http.StatusFound) //nolint:forcetypeassert // test
		}
	}))
	defer srv.Close()

	t.Run("integration loopback server to metadata", func(t *testing.T) {
		c := NewClient(Integration, Options{AllowLoopback: true, AllowPrivate: true})
		for _, tgt := range []string{
			"http://169.254.169.254/latest/meta-data/",
			"http://metadata.google.internal/computeMetadata/v1/",
			"file:///etc/passwd",
		} {
			target.Store(tgt)
			if _, err := get(t, c, srv.URL+"/r"); !errors.Is(err, ErrBlocked) {
				t.Fatalf("redirect to %s must be blocked, got %v", tgt, err)
			}
		}
		target.Store(srv.URL + "/ok")
		if body, err := get(t, c, srv.URL+"/r"); err != nil || body != "ok" {
			t.Fatalf("allowed redirect: %q %v", body, err)
		}
		if _, err := get(t, c, srv.URL+"/loop"); err == nil || !strings.Contains(err.Error(), "redirects") {
			t.Fatalf("redirect loop must stop, got %v", err)
		}
	})

	t.Run("user url public host to loopback", func(t *testing.T) {
		routeTo(t, srv)
		res := newResolver(map[string][]string{"list.example.com": {"93.184.215.14"}})
		c := NewClient(UserURL, Options{Resolver: res})
		for _, tgt := range []string{srv.URL + "/ok", "http://localhost/", "http://10.1.2.3/", "http://[::1]/"} {
			target.Store(tgt)
			if _, err := get(t, c, "http://list.example.com/r"); !errors.Is(err, ErrBlocked) {
				t.Fatalf("redirect to %s must be blocked, got %v", tgt, err)
			}
		}
	})

	t.Run("redirects disabled", func(t *testing.T) {
		target.Store(srv.URL + "/ok")
		c := NewClient(Integration, Options{AllowLoopback: true, MaxRedirects: -1})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/r", nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("want 302, got %d", resp.StatusCode)
		}
	})
}

func TestClientBlocksDirectRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	for _, c := range []*http.Client{
		NewClient(UserURL, Options{}),
		NewClient(Integration, Options{}),
		NewClient(Integration, Options{AllowPrivate: true}),
	} {
		if _, err := get(t, c, srv.URL); !errors.Is(err, ErrBlocked) {
			t.Fatalf("loopback test server must be blocked, got %v", err)
		}
	}
	c := NewClient(Integration, Options{AllowLoopback: true})
	if body, err := get(t, c, srv.URL); err != nil || body != "ok" {
		t.Fatalf("explicit loopback allowance: %q %v", body, err)
	}
	// Host allow-list is enforced by the client too.
	c = NewClient(Integration, Options{AllowLoopback: true, AllowedHosts: []string{"other.example.com"}})
	if _, err := get(t, c, srv.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("allow-list must be enforced, got %v", err)
	}
}

func TestClientEnvProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "via-proxy "+r.URL.Host)
	}))
	defer proxy.Close()
	pu, _ := url.Parse(proxy.URL)
	envProxy = func(*http.Request) (*url.URL, error) { return pu, nil }
	t.Cleanup(func() { envProxy = http.ProxyFromEnvironment })

	res := newResolver(map[string][]string{
		"public.example.com":  {"93.184.215.14"},
		"private.example.com": {"192.168.1.5"},
	})
	// Without UseEnvProxy the proxy function is never consulted.
	c := NewClient(UserURL, Options{Resolver: res})
	if body, err := get(t, c, "http://private.example.com/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("want blocked without proxy, got %q %v", body, err)
	}
	c = NewClient(UserURL, Options{Resolver: res, UseEnvProxy: true})
	if body, err := get(t, c, "http://public.example.com/"); err != nil || body != "via-proxy public.example.com" {
		t.Fatalf("proxied public request: %q %v", body, err)
	}
	if _, err := get(t, c, "http://private.example.com/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("proxied target must still be checked, got %v", err)
	}
}
