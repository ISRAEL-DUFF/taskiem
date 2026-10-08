package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// connectProxy is an in-process HTTP CONNECT proxy that records what it
// was asked to connect to and checks basic credentials.
type connectProxy struct {
	user, password string
	refuse         func(target string) bool

	mu      sync.Mutex
	targets []string
}

func (p *connectProxy) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}

func (p *connectProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
		return
	}
	if p.user != "" && r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(p.user+":"+p.password)) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="test"`)
		http.Error(w, "auth", http.StatusProxyAuthRequired)
		return
	}
	p.mu.Lock()
	p.targets = append(p.targets, r.Host)
	p.mu.Unlock()
	if p.refuse != nil && p.refuse(r.Host) {
		http.Error(w, "not here", http.StatusForbidden)
		return
	}
	up, err := net.Dial("tcp", r.Host) // a name here would be resolved again: what the guard must prevent
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	client, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		_ = up.Close()
		return
	}
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go func() { _, _ = io.Copy(up, client); _ = up.Close() }()
	_, _ = io.Copy(client, up)
	_ = client.Close()
}

// upstreamFixture: a TLS and a plain upstream on loopback, a CONNECT
// proxy, and a guard whose block list lets loopback through (the test's
// stand-in for "public").
type upstreamFixture struct {
	proxy   *connectProxy
	up      *Upstream
	tlsSrv  *httptest.Server
	httpSrv *httptest.Server
	roots   *x509.CertPool
}

func newUpstreamFixture(t *testing.T) *upstreamFixture {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-metadata":
			http.Redirect(w, r, "http://metadata.partner.test/latest/meta-data/", http.StatusFound)
			return
		case "/to-elsewhere":
			http.Redirect(w, r, "http://elsewhere.test/", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "hello "+r.Host)
	})
	f := &upstreamFixture{proxy: &connectProxy{user: "taskiem", password: "s3cret"}, tlsSrv: httptest.NewTLSServer(handler), httpSrv: httptest.NewServer(handler)}
	t.Cleanup(f.tlsSrv.Close)
	t.Cleanup(f.httpSrv.Close)
	ps := httptest.NewServer(f.proxy)
	t.Cleanup(ps.Close)
	f.up = &Upstream{Addr: ps.Listener.Addr().String(), User: "taskiem", Password: "s3cret"}
	f.roots = x509.NewCertPool()
	f.roots.AddCert(f.tlsSrv.Certificate())
	return f
}

func loopbackOnly(a netip.Addr) bool { return !a.IsLoopback() && BlockedAddr(a) }

func (f *upstreamFixture) client(g *Guard, hosts ...string) *http.Client {
	c := g.Client(Policy{Tenant: "t", Hosts: hosts, Purpose: "http_step"}, 5*time.Second)
	c.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: f.roots, MinVersion: tls.VersionTLS12}
	return c
}

func fetch(c *http.Client, url string) (string, error) {
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// TestUpstreamConnectsToTheVettedIP: traffic goes through the proxy, which
// is only ever asked for the vetted IP and port, and TLS is verified
// against the original host name.
func TestUpstreamConnectsToTheVettedIP(t *testing.T) {
	f := newUpstreamFixture(t)
	loop := netip.MustParseAddr("127.0.0.1")
	g := &Guard{Upstream: f.up, Blocked: loopbackOnly, Resolver: fakeResolver{"example.com": {loop}, "api.partner.test": {loop}}}
	_, tlsPort, _ := net.SplitHostPort(f.tlsSrv.Listener.Addr().String())
	_, httpPort, _ := net.SplitHostPort(f.httpSrv.Listener.Addr().String())

	// httptest's certificate is for example.com (and 127.0.0.1).
	body, err := fetch(f.client(g, "example.com"), "https://example.com:"+tlsPort+"/")
	if err != nil || !strings.HasPrefix(body, "hello example.com") {
		t.Fatalf("https through the proxy: %q %v", body, err)
	}
	body, err = fetch(f.client(g, "api.partner.test"), "http://api.partner.test:"+httpPort+"/")
	if err != nil || !strings.HasPrefix(body, "hello api.partner.test") {
		t.Fatalf("http through the proxy: %q %v", body, err)
	}
	want := []string{"127.0.0.1:" + tlsPort, "127.0.0.1:" + httpPort}
	if got := f.proxy.seen(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("proxy was asked for %v, want %v (IPs only)", got, want)
	}

	// The certificate is valid for 127.0.0.1 but not api.partner.test: TLS
	// must fail, proving it is checked against the name, not the IP dialled.
	if _, err := fetch(f.client(g, "api.partner.test"), "https://api.partner.test:"+tlsPort+"/"); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("TLS to a name the certificate does not cover: %v", err)
	}
}

// TestUpstreamStillRefusesPrivateAddresses: private and metadata
// addresses, and hosts off the allow-list, are refused before the proxy is
// asked anything.
func TestUpstreamStillRefusesPrivateAddresses(t *testing.T) {
	f := newUpstreamFixture(t)
	g := &Guard{Upstream: f.up, Resolver: fakeResolver{
		"metadata.partner.test": {netip.MustParseAddr("169.254.169.254")},
		"internal.partner.test": {netip.MustParseAddr("10.0.0.5")},
		"mixed.partner.test":    {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("192.168.1.1")},
		"v6.partner.test":       {netip.MustParseAddr("fd00:ec2::254")},
	}}
	p := Policy{Hosts: []string{"*.partner.test", "169.254.169.254", "10.0.0.5"}}
	for _, addr := range []string{"metadata.partner.test:80", "internal.partner.test:443", "mixed.partner.test:443", "v6.partner.test:80",
		"169.254.169.254:80", "10.0.0.5:443", "elsewhere.test:443"} {
		if _, err := g.DialContext(context.Background(), p, "tcp", addr); !errors.Is(err, ErrDenied) {
			t.Errorf("%s: want denial, got %v", addr, err)
		}
	}
	if got := f.proxy.seen(); len(got) != 0 {
		t.Errorf("proxy was asked for %v", got)
	}
}

// rebinder answers a public address first and the metadata address after,
// like a DNS server rebinding between check and use.
type rebinder struct{ n atomic.Int32 }

func (r *rebinder) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if r.n.Add(1) == 1 {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
}

// TestUpstreamDefeatsDNSRebinding: the name is resolved once, by the
// guard; the proxy gets the vetted IP, so a second answer never matters.
// A later lookup that turns private is refused.
func TestUpstreamDefeatsDNSRebinding(t *testing.T) {
	f := newUpstreamFixture(t)
	g := &Guard{Upstream: f.up, Blocked: loopbackOnly, Resolver: &rebinder{}}
	_, httpPort, _ := net.SplitHostPort(f.httpSrv.Listener.Addr().String())
	c := f.client(g, "rebind.partner.test")
	c.Transport.(*http.Transport).DisableKeepAlives = true
	if _, err := fetch(c, "http://rebind.partner.test:"+httpPort+"/"); err != nil {
		t.Fatal(err)
	}
	if _, err := fetch(c, "http://rebind.partner.test:"+httpPort+"/"); !errors.Is(err, ErrDenied) {
		t.Errorf("second connection after rebinding: want denial, got %v", err)
	}
	if got := f.proxy.seen(); len(got) != 1 || got[0] != "127.0.0.1:"+httpPort {
		t.Errorf("proxy was asked for %v", got)
	}
}

// TestUpstreamRevetsRedirects: a redirect is vetted like the first
// request, whether it leaves the allow-list or points at metadata.
func TestUpstreamRevetsRedirects(t *testing.T) {
	f := newUpstreamFixture(t)
	loop := netip.MustParseAddr("127.0.0.1")
	g := &Guard{Upstream: f.up, Blocked: loopbackOnly, Resolver: fakeResolver{
		"api.partner.test": {loop}, "metadata.partner.test": {netip.MustParseAddr("169.254.169.254")}, "elsewhere.test": {loop}}}
	_, httpPort, _ := net.SplitHostPort(f.httpSrv.Listener.Addr().String())
	c := f.client(g, "api.partner.test", "metadata.partner.test")
	for _, path := range []string{"/to-metadata", "/to-elsewhere"} {
		if _, err := fetch(c, "http://api.partner.test:"+httpPort+path); !errors.Is(err, ErrDenied) {
			t.Errorf("%s: want denial, got %v", path, err)
		}
	}
	for _, target := range f.proxy.seen() {
		if target != "127.0.0.1:"+httpPort {
			t.Errorf("proxy was asked for %s", target)
		}
	}
}

// TestUpstreamFailures: wrong credentials and a proxy's refusal are
// reported without reaching the destination; a refusal is fatal.
func TestUpstreamFailures(t *testing.T) {
	f := newUpstreamFixture(t)
	loop := netip.MustParseAddr("127.0.0.1")
	_, httpPort, _ := net.SplitHostPort(f.httpSrv.Listener.Addr().String())
	p := Policy{Hosts: []string{"api.partner.test"}}
	bad := *f.up
	bad.Password = "wrong"
	g := &Guard{Upstream: &bad, Blocked: loopbackOnly, Resolver: fakeResolver{"api.partner.test": {loop}}}
	if _, err := g.DialContext(context.Background(), p, "tcp", "api.partner.test:"+httpPort); err == nil || errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "authentication") {
		t.Errorf("bad credentials: %v", err)
	}
	f.proxy.refuse = func(string) bool { return true }
	g.Upstream = f.up
	if _, err := g.DialContext(context.Background(), p, "tcp", "api.partner.test:"+httpPort); !errors.Is(err, ErrDenied) {
		t.Errorf("proxy refusal: %v", err)
	}
	if _, err := f.up.dial(context.Background(), "api.partner.test:443", time.Second); !errors.Is(err, ErrDenied) {
		t.Errorf("a name as the CONNECT target: %v", err)
	}
	if _, err := g.DialContext(context.Background(), p, "udp", "api.partner.test:"+httpPort); !errors.Is(err, ErrDenied) {
		t.Errorf("udp through the proxy: %v", err)
	}
}

// TestUpstreamLoopbackExceptionIsDirect: an operator's loopback exception
// (a fake provider on the same machine) is not sent to the proxy.
func TestUpstreamLoopbackExceptionIsDirect(t *testing.T) {
	f := newUpstreamFixture(t)
	host, port, _ := net.SplitHostPort(f.httpSrv.Listener.Addr().String())
	g := &Guard{Upstream: f.up, Loopback: map[string]string{"connector:termii": port}}
	conn, err := g.DialContext(context.Background(), Policy{Hosts: []string{host}, Purpose: "connector:termii"}, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if got := f.proxy.seen(); len(got) != 0 {
		t.Errorf("proxy was asked for %v", got)
	}
}

// TestUpstreamOverTLS: an https:// proxy is reached over TLS, verified
// against its own name.
func TestUpstreamOverTLS(t *testing.T) {
	f := newUpstreamFixture(t)
	ps := httptest.NewTLSServer(f.proxy)
	t.Cleanup(ps.Close)
	roots := x509.NewCertPool()
	roots.AddCert(ps.Certificate())
	up := &Upstream{Addr: ps.Listener.Addr().String(), TLS: true, User: "taskiem", Password: "s3cret", TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	g := &Guard{Upstream: up, Blocked: loopbackOnly, Resolver: fakeResolver{"example.com": {netip.MustParseAddr("127.0.0.1")}}}
	_, tlsPort, _ := net.SplitHostPort(f.tlsSrv.Listener.Addr().String())
	if body, err := fetch(f.client(g, "example.com"), "https://example.com:"+tlsPort+"/"); err != nil || !strings.HasPrefix(body, "hello") {
		t.Fatalf("through a TLS proxy: %q %v", body, err)
	}
	up.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12} // system roots do not trust the test proxy
	if _, err := g.DialContext(context.Background(), Policy{Hosts: []string{"example.com"}}, "tcp", "example.com:"+tlsPort); err == nil {
		t.Error("an untrusted proxy certificate was accepted")
	}
}

func TestUpstreamFromEnv(t *testing.T) {
	env := func(kv ...string) func(string) (string, bool) {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	if u, err := UpstreamFromEnv(env("HTTPS_PROXY", "http://proxy:3128", "HTTP_PROXY", "http://proxy:3128")); u != nil || err != nil {
		t.Errorf("HTTPS_PROXY must not configure the egress proxy: %v %v", u, err)
	}
	u, err := UpstreamFromEnv(env("TASKIEM_EGRESS_PROXY", "http://proxy.internal:3128", "TASKIEM_EGRESS_PROXY_USER", "taskiem", "TASKIEM_EGRESS_PROXY_PASSWORD", "pw"))
	if err != nil || u.Addr != "proxy.internal:3128" || u.TLS || u.User != "taskiem" || u.Password != "pw" || u.String() != "http://proxy.internal:3128" {
		t.Errorf("%+v %v", u, err)
	}
	if u, err := UpstreamFromEnv(env("TASKIEM_EGRESS_PROXY", "https://[fd00::1]:8443/")); err != nil || !u.TLS || u.Addr != "[fd00::1]:8443" {
		t.Errorf("%+v %v", u, err)
	}
	for _, bad := range []map[string]string{
		{"TASKIEM_EGRESS_PROXY": "proxy.internal:3128"},
		{"TASKIEM_EGRESS_PROXY": "env"},
		{"TASKIEM_EGRESS_PROXY": "http://proxy.internal"},
		{"TASKIEM_EGRESS_PROXY": "socks5://proxy.internal:1080"},
		{"TASKIEM_EGRESS_PROXY": "http://user:pw@proxy.internal:3128"},
		{"TASKIEM_EGRESS_PROXY": "http://proxy.internal:3128/path"},
		{"TASKIEM_EGRESS_PROXY": "http://:3128"},
		{"TASKIEM_EGRESS_PROXY": "http://proxy.internal:3128", "TASKIEM_EGRESS_PROXY_PASSWORD": "pw"},
		{"TASKIEM_EGRESS_PROXY_USER": "taskiem"},
	} {
		lookup := func(k string) (string, bool) { v, ok := bad[k]; return v, ok }
		if u, err := UpstreamFromEnv(lookup); err == nil {
			t.Errorf("%v: accepted as %+v", bad, u)
		}
	}
}

// TestSetUpstreamIsTheDefault: a guard without its own proxy uses the
// process-wide one.
func TestSetUpstreamIsTheDefault(t *testing.T) {
	f := newUpstreamFixture(t)
	SetUpstream(f.up)
	t.Cleanup(func() { SetUpstream(nil) })
	g := &Guard{Blocked: loopbackOnly, Resolver: fakeResolver{"api.partner.test": {netip.MustParseAddr("127.0.0.1")}}}
	_, httpPort, _ := net.SplitHostPort(f.httpSrv.Listener.Addr().String())
	if _, err := fetch(f.client(g, "api.partner.test"), "http://api.partner.test:"+httpPort+"/"); err != nil {
		t.Fatal(err)
	}
	if got := f.proxy.seen(); len(got) != 1 {
		t.Errorf("proxy was asked for %v", got)
	}
}
