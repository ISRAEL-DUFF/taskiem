package egress

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

// proxyFixture: an upstream HTTPS and HTTP server on loopback, names that
// resolve to it (and one that resolves to a private address), and a proxy
// whose guard lets loopback through only for these tests.
type proxyFixture struct {
	proxy    *Proxy
	proxyURL string
	tlsSrv   *httptest.Server
	httpSrv  *httptest.Server
}

func newProxyFixture(t *testing.T) *proxyFixture {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("Proxy-Authorization reached the upstream")
		}
		_, _ = fmt.Fprintf(w, "hello %s", r.URL.Path)
	})
	f := &proxyFixture{tlsSrv: httptest.NewTLSServer(handler), httpSrv: httptest.NewServer(handler)}
	t.Cleanup(f.tlsSrv.Close)
	t.Cleanup(f.httpSrv.Close)
	loop := netip.MustParseAddr("127.0.0.1")
	g := &Guard{
		Resolver: fakeResolver{"example.com": {loop}, "other.example.com": {loop}, "internal.example.com": {netip.MustParseAddr("10.0.0.5")}},
		Blocked:  func(a netip.Addr) bool { return a != loop && BlockedAddr(a) },
	}
	_, tlsPort, _ := net.SplitHostPort(f.tlsSrv.Listener.Addr().String())
	_, httpPort, _ := net.SplitHostPort(f.httpSrv.Listener.Addr().String())
	f.proxy = &Proxy{Guard: g, Ports: []string{tlsPort, httpPort}}
	ps := httptest.NewServer(f.proxy)
	t.Cleanup(ps.Close)
	f.proxyURL = ps.URL
	return f
}

func (f *proxyFixture) client(t *testing.T, token string) *http.Client {
	t.Helper()
	pu, _ := url.Parse(f.proxyURL)
	if token != "" {
		pu.User = url.UserPassword("taskiem", token)
	}
	tr := f.tlsSrv.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(pu)
	tr.TLSClientConfig = &tls.Config{RootCAs: f.tlsSrv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, ServerName: "example.com", MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

func (f *proxyFixture) url(scheme, host, path string) string {
	srv := f.httpSrv
	if scheme == "https" {
		srv = f.tlsSrv
	}
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return scheme + "://" + host + ":" + port + path
}

func get(t *testing.T, c *http.Client, u string) (int, string) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		// A refused CONNECT surfaces as a transport error naming the status.
		return -1, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestProxyAllowsGrantedHosts(t *testing.T) {
	f := newProxyFixture(t)
	step := Policy{Tenant: "t1", Purpose: "container_step", Hosts: []string{"example.com", "internal.example.com", "other.example.com"}}
	env := Policy{Hosts: []string{"example.com", "*.example.com"}}
	token, revoke := f.proxy.Grant(step, env)
	defer revoke()
	c := f.client(t, token)

	if code, body := get(t, c, f.url("https", "example.com", "/tls")); code != 200 || body != "hello /tls" {
		t.Fatalf("https through CONNECT: %d %q", code, body)
	}
	if code, body := get(t, c, f.url("http", "example.com", "/plain")); code != 200 || body != "hello /plain" {
		t.Fatalf("plain http: %d %q", code, body)
	}
}

func TestProxyDenies(t *testing.T) {
	f := newProxyFixture(t)
	step := Policy{Tenant: "t1", Purpose: "container_step", Hosts: []string{"example.com", "internal.example.com", "other.example.com"}}
	env := Policy{Hosts: []string{"example.com", "internal.example.com"}} // other.example.com is not on the environment's list
	token, revoke := f.proxy.Grant(step, env)
	defer revoke()
	c := f.client(t, token)

	cases := map[string]string{
		"not on the step's list":        f.url("http", "nope.example.com", "/"),
		"not on the environment's list": f.url("http", "other.example.com", "/"),
		"private address":               f.url("http", "internal.example.com", "/"),
		"private address over CONNECT":  f.url("https", "internal.example.com", "/"),
		"ip literal":                    f.url("http", "127.0.0.1", "/"),
		"metadata":                      "http://169.254.169.254/latest/meta-data/",
	}
	for name, u := range cases {
		code, body := get(t, c, u)
		if code == 200 || (code != 403 && !strings.Contains(body, "Forbidden")) {
			t.Errorf("%s: %d %q, want refused with 403", name, code, body)
		}
	}
	// Unauthenticated, or with a wrong token: 407.
	for _, tok := range []string{"", "wrong"} {
		if code, body := get(t, f.client(t, tok), f.url("http", "example.com", "/")); code != http.StatusProxyAuthRequired {
			t.Errorf("token %q: %d %q, want 407", tok, code, body)
		}
	}
}

func TestProxyRevokeClosesTunnels(t *testing.T) {
	f := newProxyFixture(t)
	token, revoke := f.proxy.Grant(Policy{Tenant: "t1", Hosts: []string{"example.com"}})
	// Open a raw tunnel and keep it.
	conn, err := net.Dial("tcp", strings.TrimPrefix(f.proxyURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	target := strings.TrimPrefix(f.url("https", "example.com", ""), "https://")
	auth := "Basic " + basic("x", token)
	_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", target, target, auth)
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil || !strings.Contains(line, "200") {
		t.Fatalf("CONNECT: %q %v", line, err)
	}
	_, _ = br.ReadString('\n')
	revoke()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadByte(); err == nil {
		t.Fatal("tunnel still open after revoke")
	} else if ne := net.Error(nil); errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("tunnel not closed by revoke")
	}
	if code, _ := get(t, f.client(t, token), f.url("http", "example.com", "/")); code != http.StatusProxyAuthRequired {
		t.Fatalf("revoked token still works: %d", code)
	}
}

func basic(user, pass string) string {
	r, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	r.SetBasicAuth(user, pass)
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
}
