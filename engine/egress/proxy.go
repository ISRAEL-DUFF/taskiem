package egress

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Proxy is an HTTP forward proxy for container steps (docs/container-steps.md):
// CONNECT for HTTPS, absolute-form requests for plain HTTP. A container
// never gets a route of its own; with network "egress" it is given the
// proxy's address and a token, and every connection it makes goes through
// the guard under the policy granted for that token: the step's hosts and
// the environment's allow-list, public addresses only, logged.
//
// A token is valid from Grant until its revoke function runs; revoking
// closes the connections still open under it.
type Proxy struct {
	Guard  *Guard
	Logger *slog.Logger
	// Ports a container may connect to; default 443 and 80.
	Ports []string
	// IdleTimeout closes a tunnel with no traffic either way; default 2m.
	IdleTimeout time.Duration

	mu     sync.Mutex
	grants map[[32]byte]*grant
}

type grant struct {
	policies []Policy
	conns    map[io.Closer]struct{}
	closed   bool
}

func (p *Proxy) logger() *slog.Logger {
	if p.Logger == nil {
		return slog.Default()
	}
	return p.Logger
}

// Grant lets the holder of the returned token connect to hosts every one
// of the policies allows (the first is the one logged). Call revoke when
// the step ends.
func (p *Proxy) Grant(policies ...Policy) (token string, revoke func()) {
	var b [32]byte
	_, _ = rand.Read(b[:])
	token = hex.EncodeToString(b[:])
	key := sha256.Sum256([]byte(token))
	g := &grant{policies: policies, conns: map[io.Closer]struct{}{}}
	p.mu.Lock()
	if p.grants == nil {
		p.grants = map[[32]byte]*grant{}
	}
	p.grants[key] = g
	p.mu.Unlock()
	return token, func() {
		p.mu.Lock()
		delete(p.grants, key)
		g.closed = true
		conns := g.conns
		g.conns = nil
		p.mu.Unlock()
		for c := range conns {
			_ = c.Close()
		}
	}
}

// lookup finds the grant for a request's Proxy-Authorization (basic; the
// password is the token, the user name is ignored).
func (p *Proxy) lookup(r *http.Request) *grant {
	auth := r.Header.Get("Proxy-Authorization")
	scheme, cred, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "basic") {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cred))
	if err != nil {
		return nil
	}
	_, token, ok := strings.Cut(string(raw), ":")
	if !ok {
		return nil
	}
	key := sha256.Sum256([]byte(token))
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.grants[key]
}

// track registers an open connection under its grant; false if the grant
// was revoked meanwhile (the connection is closed).
func (p *Proxy) track(g *grant, c io.Closer) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g.closed {
		_ = c.Close()
		return false
	}
	g.conns[c] = struct{}{}
	return true
}

func (p *Proxy) untrack(g *grant, c io.Closer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g.conns != nil {
		delete(g.conns, c)
	}
}

func (p *Proxy) portAllowed(port string) bool {
	ports := p.Ports
	if len(ports) == 0 {
		ports = []string{"443", "80"}
	}
	return slices.Contains(ports, port)
}

// allowed checks the host against every policy but the first, which the
// guard applies itself when dialling.
func (g *grant) allowed(host string) bool {
	for _, pol := range g.policies[1:] {
		if !pol.Allows(host) {
			return false
		}
	}
	return true
}

func (p *Proxy) deny(w http.ResponseWriter, g *grant, host, why string, code int) {
	tenant, purpose := "", ""
	if g != nil && len(g.policies) > 0 {
		tenant, purpose = g.policies[0].Tenant, g.policies[0].Purpose
	}
	p.logger().Warn("egress denied", "tenant", tenant, "purpose", purpose, "host", host, "reason", why)
	http.Error(w, "egress denied: "+why, code)
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g := p.lookup(r)
	if g == nil || len(g.policies) == 0 {
		w.Header().Set("Proxy-Authenticate", `Basic realm="taskiem-egress"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		p.connect(w, r, g)
		return
	}
	p.forward(w, r, g)
}

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request, g *grant) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		p.deny(w, g, r.Host, "bad CONNECT target", http.StatusBadRequest)
		return
	}
	if !p.portAllowed(port) {
		p.deny(w, g, host, "port "+port+" is not allowed", http.StatusForbidden)
		return
	}
	if !g.allowed(host) {
		p.deny(w, g, host, "host is not on the allow-list", http.StatusForbidden)
		return
	}
	upstream, err := p.Guard.DialContext(r.Context(), g.policies[0], "tcp", r.Host)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, ErrDenied) {
			code = http.StatusForbidden
		}
		http.Error(w, "egress: "+err.Error(), code)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "CONNECT unsupported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	// The server's deadlines stay on a hijacked connection; the tunnel
	// keeps its own idle timeout instead.
	_ = client.SetDeadline(time.Time{})
	if !p.track(g, client) || !p.track(g, upstream) {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	defer p.untrack(g, client)
	defer p.untrack(g, upstream)
	defer func() { _ = client.Close(); _ = upstream.Close() }()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	idle := p.IdleTimeout
	if idle <= 0 {
		idle = 2 * time.Minute
	}
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn, r io.Reader) {
		b := make([]byte, 32<<10)
		for {
			_ = src.SetReadDeadline(time.Now().Add(idle))
			n, err := r.Read(b)
			if n > 0 {
				if _, werr := dst.Write(b[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if tc, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}
	// Bytes the client sent after its CONNECT request are in buf.
	go pipe(upstream, client, buf)
	go pipe(client, upstream, upstream)
	<-done
	<-done
}

// hopHeaders are not forwarded (RFC 9110 7.6.1), nor is the proxy's own
// authorization.
var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, g *grant) {
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		p.deny(w, g, r.URL.Host, "only absolute http:// requests and CONNECT are proxied", http.StatusBadRequest)
		return
	}
	host := r.URL.Hostname()
	port := r.URL.Port()
	if port == "" {
		port = "80"
	}
	if !p.portAllowed(port) {
		p.deny(w, g, host, "port "+port+" is not allowed", http.StatusForbidden)
		return
	}
	if !g.allowed(host) {
		p.deny(w, g, host, "host is not on the allow-list", http.StatusForbidden)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	tr := p.Guard.Client(g.policies[0], 0).Transport.(*http.Transport)
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	closer := &cancelCloser{cancel}
	if !p.track(g, closer) {
		return
	}
	defer p.untrack(g, closer)
	resp, err := tr.RoundTrip(out.WithContext(ctx))
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, ErrDenied) {
			code = http.StatusForbidden
		}
		http.Error(w, "egress: "+err.Error(), code)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// cancelCloser closes a forwarded request (its context) on revoke; a
// pointer, so it can key the grant's set.
type cancelCloser struct{ cancel func() }

func (c *cancelCloser) Close() error { c.cancel(); return nil }
