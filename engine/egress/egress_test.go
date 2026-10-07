package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"

	"github.com/israel-duff/taskiem/engine/effects"
)

func TestBlockedAddr(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0",
		"::1", "fe80::1", "fc00::1", "fd00:ec2::254", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "224.0.0.1", "198.18.0.1",
		"2002:a9fe:a9fe::1", "2002:7f00:1::", // 6to4 around 169.254.169.254 and 127.0.0.1
		"2001:0:4136:e378:8000:63bf:3fff:fdd2", // Teredo
		"::7f00:1", "::a9fe:a9fe",              // IPv4-compatible 127.0.0.1, 169.254.169.254
		"fec0::1"} {
		if !BlockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "41.58.1.1", "2606:4700:4700::1111", "2001:4860:4860::8888", "2a00:1450::1"} {
		if BlockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestAllowList(t *testing.T) {
	p := Policy{Hosts: []string{"api.paystack.co", "*.termii.com"}}
	for host, want := range map[string]bool{
		"api.paystack.co": true, "API.PAYSTACK.CO.": true, "evil.paystack.co": false,
		"api.ng.termii.com": true, "termii.com": false, "termii.com.evil.io": false,
	} {
		if got := p.Allows(host); got != want {
			t.Errorf("Allows(%s) = %v", host, got)
		}
	}
	if (Policy{}).Allows("anything") {
		t.Error("empty allow-list allows")
	}
}

// fakeResolver maps names to addresses, like a DNS server under attack.
type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func TestDialDeniesSSRF(t *testing.T) {
	g := &Guard{Resolver: fakeResolver{
		"metadata.attacker.io": {netip.MustParseAddr("169.254.169.254")},
		"mixed.attacker.io":    {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.5")},
	}}
	p := Policy{Hosts: []string{"*.attacker.io", "127.0.0.1", "[::1]", "::1"}}
	for _, addr := range []string{"metadata.attacker.io:80", "mixed.attacker.io:443", "127.0.0.1:5432", "[::1]:80", "notlisted.io:443"} {
		_, err := g.DialContext(context.Background(), p, "tcp", addr)
		if !errors.Is(err, ErrDenied) || effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s: want fatal denial, got %v", addr, err)
		}
	}
	if _, err := g.DialContext(context.Background(), p, "tcp", "nxdomain.attacker.io:443"); effects.Classify(err) != effects.KindNotSent {
		t.Errorf("DNS failure should be not_sent: %v", err)
	}
}

func TestClientReachesAllowedHostAndBlocksRedirectEscape(t *testing.T) {
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "secret metadata") }))
	defer inner.Close()
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, inner.URL, http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer outer.Close()
	outerHost := hostOf(t, outer.URL)
	loopbackOK := func(a netip.Addr) bool { return !a.IsLoopback() && BlockedAddr(a) }
	g := &Guard{Blocked: loopbackOK, Resolver: fakeResolver{"api.partner.test": {netip.MustParseAddr(outerHost)}}}

	c := g.Client(Policy{Hosts: []string{"api.partner.test"}}, 0)
	port := portOf(t, outer.URL)
	resp, err := c.Get("http://api.partner.test:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp, err := c.Get("http://api.partner.test:" + port + "/redirect"); !errors.Is(err, ErrDenied) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Errorf("redirect off the allow-list should be denied, got %v", err)
	}
	// With the default block list, even an allow-listed name pointing at loopback is refused.
	strict := &Guard{Resolver: fakeResolver{"api.partner.test": {netip.MustParseAddr(outerHost)}}}
	if resp, err := strict.Client(Policy{Hosts: []string{"api.partner.test"}}, 0).Get("http://api.partner.test:" + port + "/"); !errors.Is(err, ErrDenied) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Errorf("loopback via DNS should be denied, got %v", err)
	}
}

func hostOf(t *testing.T, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

func portOf(t *testing.T, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}
