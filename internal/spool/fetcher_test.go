package spool

import (
	"context"
	"encoding/json"
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
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

type dialRecorder struct {
	mu        sync.Mutex
	addresses []string
}

func (d *dialRecorder) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.addresses...)
}

// Only DNS, the final socket destination, and fixture CA trust are injected.
// Requests still pass through the production client, redirect checks, and dial guard.
func fixtureFetcher(t *testing.T, srv *httptest.Server, hosts []string, resolver spoolResolver) (*fetcher, *dialRecorder) {
	t.Helper()
	recorder := &dialRecorder{}
	if resolver == nil {
		resolver = resolverFunc(func(_ context.Context, _, host string) ([]netip.Addr, error) {
			if host != "example.com" {
				return nil, fmt.Errorf("unexpected lookup: %s", host)
			}
			return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
		})
	}
	f := newFetcher(hosts, resolver, func(ctx context.Context, network, address string) (net.Conn, error) {
		recorder.mu.Lock()
		recorder.addresses = append(recorder.addresses, address)
		recorder.mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	})
	tr := f.client.Transport.(*http.Transport)
	tr.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("fixture must verify TLS")
	}
	t.Cleanup(f.client.CloseIdleConnections)
	return f, recorder
}

func fixtureURL(t *testing.T, srv *httptest.Server, host string) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return "https://" + net.JoinHostPort(host, u.Port())
}

func TestFetchTag_HTTPS(t *testing.T) {
	tag := contracts.TagDefinition{Name: "production", Version: "3.1.0", Description: "Production modules", Modules: []contracts.TagModule{{Repo: "https://github.com/Muxcore-Media/admin-ui", Version: "v2.0.0", Required: true}}}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spool/tags/production.json" || r.URL.RawQuery != "channel=stable" {
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(400)
			return
		}
		if r.TLS.ServerName != "example.com" {
			t.Errorf("TLS SNI = %q", r.TLS.ServerName)
		}
		if err := json.NewEncoder(w).Encode(tag); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	f, d := fixtureFetcher(t, srv, nil, nil)
	got, err := f.fetchTag(context.Background(), fixtureURL(t, srv, "example.com")+"/spool/?channel=stable", "production")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != tag.Name || got.Description != tag.Description || got.Version != tag.Version || len(got.Modules) != 1 || !got.Modules[0].Required {
		t.Fatalf("unexpected tag: %+v", got)
	}
	addresses := d.snapshot()
	if len(addresses) != 1 || !strings.HasPrefix(addresses[0], "93.184.215.14:") {
		t.Fatalf("dialed %v; expected checked literal address", addresses)
	}
}

func TestFetchTag_ResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"not found", 404, "", "HTTP 404"},
		{"invalid JSON", 200, "not json", "parse tag"},
		{"response cap", 200, `{"name":"` + strings.Repeat("a", 1<<20) + `"}`, "parse tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			f, _ := fixtureFetcher(t, srv, nil, nil)
			_, err := f.fetchTag(context.Background(), fixtureURL(t, srv, "example.com"), "default")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFetchTag_InvalidInput(t *testing.T) {
	f := newFetcher(nil, nil, nil)
	t.Cleanup(f.client.CloseIdleConnections)
	for _, tc := range []struct{ raw, tag string }{
		{"", "default"}, {"https://example.com", ""}, {"http://example.com", "default"},
		{"ftp://example.com", "default"}, {"file:///tmp/spool", "default"},
		{"https://user:password@example.com", "default"}, {"https://example.com:", "default"},
		{"https://example.com:0", "default"}, {"https://example.com:65536", "default"},
		{"https://example.com", "../etc/passwd"}, {"https://example.com", "foo/bar"}, {"https://example.com", "tag name"},
	} {
		if _, err := f.fetchTag(context.Background(), tc.raw, tc.tag); err == nil {
			t.Errorf("accepted %q tag %q", tc.raw, tc.tag)
		}
	}
}

func TestFetchTag_ForbiddenAddressesEvenWhenListed(t *testing.T) {
	for _, host := range []string{
		"169.254.169.254", "169.254.170.2", "100.100.100.200", "0.0.0.0", "224.0.0.1", "255.255.255.255",
		"[fd00:ec2::254]", "[fe80::1]", "[::]", "[ff02::1]", "[::ffff:169.254.169.254]",
		"[64:ff9b::a9fe:a9fe]", "[2002:a9fe:a9fe::1]", "[64:ff9b:1:a9fe:a9:fe00::]",
		"[fe80::1%25eth0]", "metadata.google.internal", "metadata.goog", "instance-data.ec2.internal",
		"2130706433", "0177.0.0.1", "0x7f.1", "127.1",
	} {
		t.Run(host, func(t *testing.T) {
			raw := "https://" + host
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			for _, hosts := range [][]string{nil, {u.Host}} {
				f := newFetcher(hosts, nil, func(context.Context, string, string) (net.Conn, error) {
					t.Error("forbidden address reached dial")
					return nil, errors.New("unexpected dial")
				})
				_, err := f.fetchTag(context.Background(), raw, "default")
				f.client.CloseIdleConnections()
				if !errors.Is(err, errBlockedDestination) {
					t.Fatalf("hosts=%v got %v, want blocked destination", hosts, err)
				}
			}
		})
	}
}

func TestFetchTag_DNSAddressPolicy(t *testing.T) {
	for _, tc := range []struct {
		name            string
		addresses       []string
		listed, allowed bool
	}{
		{"public", []string{"93.184.215.14"}, false, true},
		{"private default", []string{"192.168.40.2"}, false, false},
		{"listed LAN", []string{"192.168.40.2"}, true, true},
		{"listed loopback", []string{"127.0.0.1"}, true, true},
		{"listed IPv6 LAN", []string{"fd12::2"}, true, true},
		{"listed IPv6 loopback", []string{"::1"}, true, true},
		{"metadata DNS", []string{"169.254.169.254"}, true, false},
		{"metadata IPv6 DNS", []string{"fd00:ec2::254"}, true, false},
		{"mixed DNS default", []string{"93.184.215.14", "10.0.0.2"}, false, false},
		{"mixed DNS listed", []string{"93.184.215.14", "169.254.169.254"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"name":"default"}`)) }))
			defer srv.Close()
			raw := fixtureURL(t, srv, "example.com")
			u, _ := url.Parse(raw)
			var hosts []string
			if tc.listed {
				hosts = []string{u.Host}
			}
			resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
				var out []netip.Addr
				for _, s := range tc.addresses {
					out = append(out, netip.MustParseAddr(s))
				}
				return out, nil
			})
			f, d := fixtureFetcher(t, srv, hosts, resolver)
			got, err := f.fetchTag(context.Background(), raw, "default")
			if tc.allowed {
				if err != nil || got.Name != "default" {
					t.Fatalf("legitimate destination: tag=%v err=%v", got, err)
				}
			} else if !errors.Is(err, errBlockedDestination) || len(d.snapshot()) != 0 {
				t.Fatalf("blocked DNS: err=%v dials=%v", err, d.snapshot())
			}
		})
	}
}

func TestFetchTag_DNSRebindingPinsCheckedAddress(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"name":"default"}`)) }))
	defer srv.Close()
	var mu sync.Mutex
	calls := 0
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	})
	f, d := fixtureFetcher(t, srv, nil, resolver)
	raw := fixtureURL(t, srv, "example.com")
	if _, err := f.fetchTag(context.Background(), raw, "default"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	firstCalls := calls
	mu.Unlock()
	if firstCalls != 1 || len(d.snapshot()) != 1 {
		t.Fatalf("first fetch: lookups=%d dials=%v", firstCalls, d.snapshot())
	}
	f.client.CloseIdleConnections()
	if _, err := f.fetchTag(context.Background(), raw, "default"); !errors.Is(err, errBlockedDestination) {
		t.Fatalf("changed DNS answer: %v", err)
	}
	if len(d.snapshot()) != 1 {
		t.Fatalf("forbidden DNS answer reached dial: %v", d.snapshot())
	}
}

func TestFetchTag_RedirectPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, target, answer string
		listed, allowed      bool
	}{
		{"default private DNS", "https://private.example.com/final", "127.0.0.1", false, false},
		{"listed metadata DNS", "https://private.example.com/final", "169.254.169.254", true, false},
		{"listed IPv6 metadata", "https://[fd00:ec2::254]/final", "", true, false},
		{"unlisted host", "https://other.example.com/final", "93.184.215.14", true, false},
		{"HTTP downgrade", "http://example.com/final", "", false, false},
		{"IPv6 without port", "https://[::1]/final", "", false, false},
		{"listed LAN redirect", "fixture", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.target
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/final" {
					_, _ = w.Write([]byte(`{"name":"default"}`))
					return
				}
				http.Redirect(w, r, target, http.StatusFound)
			}))
			defer srv.Close()
			if target == "fixture" {
				target = srv.URL + "/final"
			}
			raw := fixtureURL(t, srv, "example.com")
			u, _ := url.Parse(raw)
			targetURL, _ := url.Parse(target)
			var hosts []string
			if tc.listed {
				hosts = []string{u.Host}
				if tc.name != "unlisted host" {
					hosts = append(hosts, targetURL.Host)
				}
			}
			resolver := resolverFunc(func(_ context.Context, _, host string) ([]netip.Addr, error) {
				if host == "example.com" {
					return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
				}
				if tc.answer != "" {
					return []netip.Addr{netip.MustParseAddr(tc.answer)}, nil
				}
				return nil, fmt.Errorf("unexpected lookup %s", host)
			})
			f, d := fixtureFetcher(t, srv, hosts, resolver)
			got, err := f.fetchTag(context.Background(), raw, "default")
			if tc.allowed {
				if err != nil || got.Name != "default" {
					t.Fatalf("allowed redirect: %v", err)
				}
				if len(d.snapshot()) != 2 {
					t.Fatalf("dials=%v", d.snapshot())
				}
			} else if !errors.Is(err, errBlockedDestination) || len(d.snapshot()) != 1 {
				t.Fatalf("blocked redirect: err=%v dials=%v", err, d.snapshot())
			}
		})
	}
}

func TestFetchTag_RedirectLimit(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer srv.Close()
	f, _ := fixtureFetcher(t, srv, nil, nil)
	_, err := f.fetchTag(context.Background(), fixtureURL(t, srv, "example.com"), "default")
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 3 {
		t.Fatalf("server hits=%d; third redirect must be rejected", hits)
	}
}

func TestFetchTag_ExactAllowedHostAndPort(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"name":"default"}`)) }))
	defer srv.Close()
	raw := fixtureURL(t, srv, "example.com")
	u, _ := url.Parse(raw)
	for _, host := range []string{"example.com", "EXAMPLE.COM:" + u.Port(), "example.com.:" + u.Port(), "*.example.com:" + u.Port(), "example.com:443"} {
		f, d := fixtureFetcher(t, srv, []string{host}, nil)
		if _, err := f.fetchTag(context.Background(), raw, "default"); !errors.Is(err, errBlockedDestination) || len(d.snapshot()) != 0 {
			t.Fatalf("allow-list entry %q accepted; err=%v", host, err)
		}
	}
	hosts := []string{u.Host}
	f, _ := fixtureFetcher(t, srv, hosts, nil)
	hosts[0] = "changed.example.com"
	if _, err := f.fetchTag(context.Background(), raw, "default"); err != nil {
		t.Fatalf("snapshot changed: %v", err)
	}
}

func TestFetchTag_NoImplicitProxy(t *testing.T) {
	var mu sync.Mutex
	proxyHits := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); proxyHits++; mu.Unlock(); w.WriteHeader(500) }))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"name":"default"}`)) }))
	defer srv.Close()
	f, _ := fixtureFetcher(t, srv, nil, nil)
	if f.client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("spool transport must not delegate destination resolution to a proxy")
	}
	if _, err := f.fetchTag(context.Background(), fixtureURL(t, srv, "example.com"), "default"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if proxyHits != 0 {
		t.Fatalf("proxy hits=%d", proxyHits)
	}
}

func TestFetchTag_TimeoutIncludesDNS(t *testing.T) {
	stopped := make(chan struct{})
	resolver := resolverFunc(func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	f := newFetcher(nil, resolver, nil)
	defer f.client.CloseIdleConnections()
	f.client.Timeout = 20 * time.Millisecond
	_, err := f.fetchTag(context.Background(), "https://example.com", "default")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DNS not bound to client deadline: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("resolver remained active after the fetch timed out")
	}
}

func TestFetchTag_IPv4FallbackAfterStalledIPv6(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"name":"default"}`)) }))
	defer srv.Close()
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("2606:4700::1111"), netip.MustParseAddr("2606:4700::1001"), netip.MustParseAddr("93.184.215.14")}, nil
	})
	f, _ := fixtureFetcher(t, srv, nil, resolver)
	dial := f.dial
	stopped := make(chan struct{})
	var once sync.Once
	loser, peer := net.Pipe()
	defer func() { _ = loser.Close(); _ = peer.Close() }()
	f.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.HasPrefix(address, "[2606:4700::") {
			<-ctx.Done()
			once.Do(func() { close(stopped) })
			// Model a connection completing concurrently with cancellation.
			return loser, nil
		}
		return dial(ctx, network, address)
	}
	f.client.Timeout = time.Second
	if _, err := f.fetchTag(context.Background(), fixtureURL(t, srv, "example.com"), "default"); err != nil {
		t.Fatalf("healthy IPv4 fallback was starved: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("losing IPv6 attempt was not canceled")
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("losing connection was not closed: %v", err)
	}
}

func TestFetchTag_CancelStopsDialAttempts(t *testing.T) {
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.215.14"), netip.MustParseAddr("93.184.215.15")}, nil
	})
	started := make(chan struct{})
	stopped := make(chan struct{})
	f := newFetcher(nil, resolver, func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != "93.184.215.14:443" {
			t.Errorf("started additional dial after cancellation: %s", address)
			return nil, errors.New("unexpected dial")
		}
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	defer f.client.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := f.fetchTag(ctx, "https://example.com", "default")
		result <- err
	}()
	select {
	case <-started:
	case err := <-result:
		t.Fatalf("fetch failed before dialing: %v", err)
	case <-time.After(time.Second):
		t.Fatal("fetch did not start dialing")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("dial remained active after the caller canceled")
	}
}

func TestSetAllowedHosts_PublicFetchUsesNewPolicy(t *testing.T) {
	defer SetAllowedHosts(nil)
	SetAllowedHosts([]string{"metadata.google.internal"})
	if _, err := FetchTag(context.Background(), "https://metadata.google.internal", "default"); !errors.Is(err, errBlockedDestination) {
		t.Fatalf("metadata policy: %v", err)
	}
	SetAllowedHosts(nil)
	if _, err := FetchTag(context.Background(), "https://127.0.0.1", "default"); !errors.Is(err, errBlockedDestination) {
		t.Fatalf("default policy: %v", err)
	}
}

func TestFetchTag_InFlightPolicySnapshot(t *testing.T) {
	started := make(chan struct{})
	proceed := make(chan struct{})
	var fixtureBase string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			_, _ = w.Write([]byte(`{"name":"default"}`))
			return
		}
		close(started)
		<-proceed
		http.Redirect(w, r, fixtureBase+"/final", http.StatusFound)
	}))
	defer srv.Close()
	fixtureBase = srv.URL
	raw := fixtureURL(t, srv, "example.com")
	u, _ := url.Parse(raw)
	local, _ := url.Parse(srv.URL)
	f, _ := fixtureFetcher(t, srv, []string{u.Host, local.Host}, nil)
	fetcherMu.Lock()
	previous := currentFetcher
	currentFetcher = f
	fetcherMu.Unlock()
	t.Cleanup(func() {
		fetcherMu.Lock()
		current := currentFetcher
		currentFetcher = previous
		fetcherMu.Unlock()
		current.client.CloseIdleConnections()
	})
	result := make(chan error, 1)
	go func() {
		_, err := FetchTag(context.Background(), raw, "default")
		result <- err
	}()
	select {
	case <-started:
	case err := <-result:
		close(proceed)
		t.Fatalf("initial fetch failed: %v", err)
	}
	SetAllowedHosts([]string{"other.example.com"})
	close(proceed)
	if err := <-result; err != nil {
		t.Fatalf("in-flight redirect lost its original host policy: %v", err)
	}
	if _, err := FetchTag(context.Background(), raw, "default"); !errors.Is(err, errBlockedDestination) {
		t.Fatalf("new fetch did not receive updated policy: %v", err)
	}
}
