package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
)

// Upstream is an explicit HTTP CONNECT proxy that tenant traffic leaves
// through (TASKIEM_EGRESS_PROXY; decisions 0024 and 0028). The guard keeps
// every check: it resolves and vets the destination as without a proxy,
// then asks the proxy to CONNECT to the vetted IP and port, never to a
// name, so the proxy cannot resolve the name again. TLS to the destination
// runs inside the tunnel and is verified against the original host name by
// the caller (http.Transport sets the server name from the request URL).
type Upstream struct {
	// Addr is the proxy's host:port.
	Addr string
	// TLS speaks TLS to the proxy itself (an https:// proxy URL); the
	// proxy's certificate is verified against its host name.
	TLS bool
	// TLSConfig is the client configuration for an https:// proxy; nil is
	// the system roots. Tests set it.
	TLSConfig *tls.Config
	// User and Password are sent as Proxy-Authorization (basic) when User
	// is set.
	User, Password string
}

// The process-wide proxy, set once at start (SetUpstream). A Guard with
// its own Upstream uses that instead.
var defaultUpstream atomic.Pointer[Upstream]

// SetUpstream makes u the proxy of every guard without one of its own
// (nil: none). Call it at start, before traffic flows.
func SetUpstream(u *Upstream) { defaultUpstream.Store(u) }

// CurrentUpstream is the process-wide proxy, or nil.
func CurrentUpstream() *Upstream { return defaultUpstream.Load() }

// ParseUpstream reads a proxy address as TASKIEM_EGRESS_PROXY gives it:
// http://host:port or https://host:port, with the port written out and
// nothing else (no path, query or credentials; credentials come from
// TASKIEM_EGRESS_PROXY_USER and TASKIEM_EGRESS_PROXY_PASSWORD). Anything
// else is refused, so a proxy is only ever one an operator named exactly.
func ParseUpstream(raw, user, password string) (*Upstream, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return nil, fmt.Errorf("TASKIEM_EGRESS_PROXY: %q is not a URL like http://proxy.internal:3128", raw)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, fmt.Errorf("TASKIEM_EGRESS_PROXY: %q must start with http:// or https:// (a CONNECT proxy)", raw)
	case u.User != nil:
		return nil, errors.New("TASKIEM_EGRESS_PROXY: put credentials in TASKIEM_EGRESS_PROXY_USER and TASKIEM_EGRESS_PROXY_PASSWORD, not in the URL")
	case u.Hostname() == "":
		return nil, fmt.Errorf("TASKIEM_EGRESS_PROXY: %q names no host", raw)
	case u.Port() == "":
		return nil, fmt.Errorf("TASKIEM_EGRESS_PROXY: %q must give the port explicitly", raw)
	case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "":
		return nil, fmt.Errorf("TASKIEM_EGRESS_PROXY: %q must be only a scheme, host and port", raw)
	case user == "" && password != "":
		return nil, errors.New("TASKIEM_EGRESS_PROXY_PASSWORD is set without TASKIEM_EGRESS_PROXY_USER")
	}
	return &Upstream{Addr: u.Host, TLS: u.Scheme == "https", User: user, Password: password}, nil
}

// UpstreamFromEnv reads TASKIEM_EGRESS_PROXY and its credentials. It
// returns nil (no proxy) when the proxy is unset, and an error when it is
// set badly or credentials are set without it. HTTP_PROXY, HTTPS_PROXY and
// NO_PROXY are never read: tenant traffic uses a proxy only when this
// variable names it.
func UpstreamFromEnv(lookup func(string) (string, bool)) (*Upstream, error) {
	get := func(k string) string { v, _ := lookup(k); return strings.TrimSpace(v) }
	raw, user, password := get("TASKIEM_EGRESS_PROXY"), get("TASKIEM_EGRESS_PROXY_USER"), get("TASKIEM_EGRESS_PROXY_PASSWORD")
	if raw == "" {
		if user != "" || password != "" {
			return nil, errors.New("TASKIEM_EGRESS_PROXY_USER or _PASSWORD is set without TASKIEM_EGRESS_PROXY")
		}
		return nil, nil
	}
	return ParseUpstream(raw, user, password)
}

// String is the proxy's address, for logs (never the credentials).
func (u *Upstream) String() string {
	if u.TLS {
		return "https://" + u.Addr
	}
	return "http://" + u.Addr
}

// dial opens a tunnel through the proxy to target, an IP:port the guard
// vetted. Failures happen before anything reaches the destination, so
// they are not-sent; a proxy that refuses the destination is fatal.
func (u *Upstream) dial(ctx context.Context, target string, timeout time.Duration) (net.Conn, error) {
	if _, err := netip.ParseAddrPort(target); err != nil {
		// Never a name: the proxy would resolve it again.
		return nil, fmt.Errorf("%w: egress proxy target %q is not an IP address: %w", ErrDenied, target, effects.ErrFatal)
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", u.Addr)
	if err != nil {
		return nil, fmt.Errorf("egress proxy %s: %w: %w", u, err, effects.ErrNotSent)
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if u.TLS {
		cfg := u.TLSConfig.Clone()
		if cfg == nil {
			cfg = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		if cfg.ServerName == "" {
			host, _, _ := net.SplitHostPort(u.Addr)
			cfg.ServerName = host
		}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("egress proxy %s: %w: %w", u, err, effects.ErrNotSent)
		}
		conn = tc
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if u.User != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(u.User+":"+u.Password)) + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("egress proxy %s: %w: %w", u, err, effects.ErrNotSent)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("egress proxy %s: %w: %w", u, err, effects.ErrNotSent)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		switch resp.StatusCode {
		case http.StatusForbidden:
			return nil, fmt.Errorf("%w: the egress proxy refused %s (%s): %w", ErrDenied, target, resp.Status, effects.ErrFatal)
		case http.StatusProxyAuthRequired:
			return nil, fmt.Errorf("egress proxy %s: authentication failed (%s); check TASKIEM_EGRESS_PROXY_USER and _PASSWORD: %w", u, resp.Status, effects.ErrNotSent)
		}
		return nil, fmt.Errorf("egress proxy %s: CONNECT %s: %s: %w", u, target, resp.Status, effects.ErrNotSent)
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// bufferedConn returns bytes the proxy sent after its answer before
// reading the connection again.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
