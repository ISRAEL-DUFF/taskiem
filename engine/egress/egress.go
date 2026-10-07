// Package egress is the outbound-traffic guard (spec 14.2). Every connector
// call, http step, sandbox fetch, and database connection dials through it.
// It enforces a per-call host allow-list, refuses private, loopback,
// link-local, and cloud-metadata addresses (SSRF), pins the vetted IP for
// the connection so DNS cannot be re-bound between check and dial, and logs
// every connection.
package egress

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
)

// ErrDenied wraps every refusal. It is fatal: retrying cannot help.
var ErrDenied = errors.New("egress denied")

// Policy says where one call may go.
type Policy struct {
	Tenant string
	// Hosts are exact host names or "*.example.com" wildcards (which match
	// subdomains, not the apex). Empty means nothing is allowed.
	Hosts []string
	// Purpose is logged: "connector:paystack", "http_step", "sandbox_fetch".
	Purpose string
}

// Allows reports whether host matches the allow-list.
func (p Policy) Allows(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range p.Hosts {
		h = strings.ToLower(h)
		if h == host {
			return true
		}
		if suffix, ok := strings.CutPrefix(h, "*."); ok && strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// Resolver looks up host addresses; net.DefaultResolver satisfies it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Guard dials outbound connections under a policy.
type Guard struct {
	Resolver Resolver
	Logger   *slog.Logger
	// Blocked decides whether an address is off limits. Defaults to
	// BlockedAddr; tests can loosen it to reach httptest servers.
	Blocked func(netip.Addr) bool
	// Loopback lets one purpose reach loopback on one port: an operator
	// pointing a connector at a fake provider on the same machine (browser
	// tests; "connector:termii" -> "12727"). The policy's host allow-list
	// still applies, and nothing else private becomes reachable.
	Loopback map[string]string
	Timeout  time.Duration
}

// loopbackOK reports whether a is loopback and the purpose may reach it on
// port.
func (g *Guard) loopbackOK(p Policy, a netip.Addr, port string) bool {
	want, ok := g.Loopback[p.Purpose]
	return ok && want != "" && want == port && a.Unmap().IsLoopback()
}

func (g *Guard) resolver() Resolver {
	if g.Resolver == nil {
		return net.DefaultResolver
	}
	return g.Resolver
}

func (g *Guard) blocked(a netip.Addr) bool {
	if g.Blocked != nil {
		return g.Blocked(a)
	}
	return BlockedAddr(a)
}

func (g *Guard) logger() *slog.Logger {
	if g.Logger == nil {
		return slog.Default()
	}
	return g.Logger
}

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8",
		"fd00:ec2::254/128", // AWS IMDS over IPv6
		// IPv6 forms that carry an IPv4 address a relay or stack may route
		// to: 6to4, Teredo, IPv4-compatible; and deprecated site-local.
		"2002::/16", "2001::/32", "::/96", "fec0::/10",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// BlockedAddr reports whether an address is private, loopback, link-local,
// metadata, documentation, multicast, or otherwise not a public destination.
// IPv4-mapped IPv6 addresses are checked as IPv4.
func BlockedAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsUnspecified() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// DialContext connects to addr ("host:port") if the policy allows the host
// and every address it resolves to is public. It dials the vetted address.
func (g *Guard) DialContext(ctx context.Context, p Policy, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: bad address %q", ErrDenied, addr)
	}
	deny := func(why string) (net.Conn, error) {
		g.logger().Warn("egress denied", "tenant", p.Tenant, "purpose", p.Purpose, "host", host, "port", port, "reason", why)
		return nil, fmt.Errorf("%w: %s: %s: %w", ErrDenied, host, why, effects.ErrFatal)
	}
	if !p.Allows(host) {
		return deny("host is not on the allow-list")
	}
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		addrs, err = g.resolver().LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w: %w", host, err, effects.ErrNotSent)
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses: %w", host, effects.ErrNotSent)
	}
	for _, a := range addrs {
		if g.blocked(a) && !g.loopbackOK(p, a, port) {
			return deny("resolves to non-public address " + a.String())
		}
	}
	d := net.Dialer{Timeout: g.Timeout}
	if d.Timeout == 0 {
		d.Timeout = 10 * time.Second
	}
	var lastErr error
	for _, a := range addrs {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			g.logger().Info("egress", "tenant", p.Tenant, "purpose", p.Purpose, "host", host, "ip", a.String(), "port", port)
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("dial %s: %w: %w", host, lastErr, effects.ErrNotSent)
}

// Client returns an HTTP client whose every connection, including
// redirects, goes through the guard under p. Proxies from the environment
// are ignored: the guard is the proxy.
func (g *Guard) Client(p Policy, timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return g.DialContext(ctx, p, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("%w: too many redirects", ErrDenied)
			}
			if !p.Allows(req.URL.Hostname()) {
				return fmt.Errorf("%w: redirect to %s is not on the allow-list: %w", ErrDenied, req.URL.Hostname(), effects.ErrFatal)
			}
			return nil
		},
	}
}
