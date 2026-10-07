package dbtest

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Proxy is a TCP proxy in front of the test database, for failover tests
// (decision 0024): it can cut every connection, refuse new ones, or cut a
// connection around its next COMMIT.
type Proxy struct {
	ln     net.Listener
	target string

	mu    sync.Mutex
	conns map[net.Conn]bool
	down  bool
	// onCommit: "" nothing; "before": cut before forwarding the next
	// COMMIT (it never reaches the server); "after": forward it and cut
	// before the answer (it commits, the client never hears).
	onCommit string
}

// NewProxy proxies to the test database's host and port until the test ends.
func (d *DB) NewProxy(t testing.TB) *Proxy {
	t.Helper()
	u, err := url.Parse(d.DSN)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &Proxy{ln: ln, target: u.Host, conns: map[net.Conn]bool{}}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close(); p.Cut() })
	return p
}

// DSN is the test database's DSN through the proxy.
func (p *Proxy) DSN(d *DB) string {
	u, _ := url.Parse(d.DSN)
	u.Host = p.ln.Addr().String()
	return u.String()
}

// Pool opens a pool through the proxy as role, with runtime parameters
// (application_name, default_transaction_read_only).
func (p *Proxy) Pool(t testing.TB, d *DB, role string, size int32, params map[string]string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(p.DSN(d))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = size
	for k, v := range params {
		cfg.ConnConfig.RuntimeParams[k] = v
	}
	if role != "" {
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET ROLE "+role)
			return err
		}
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Cut closes every open connection, as a crashed or failed-over primary
// does.
func (p *Proxy) Cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.conns {
		_ = c.Close()
	}
	p.conns = map[net.Conn]bool{}
}

// SetDown refuses (true) or accepts (false) new connections.
func (p *Proxy) SetDown(down bool) {
	p.mu.Lock()
	p.down = down
	p.mu.Unlock()
	if down {
		p.Cut()
	}
}

// CutAtCommit arms a cut around the next COMMIT: "before" or "after".
func (p *Proxy) CutAtCommit(when string) {
	p.mu.Lock()
	p.onCommit = when
	p.mu.Unlock()
}

func (p *Proxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		down := p.down
		p.mu.Unlock()
		if down {
			_ = c.Close()
			continue
		}
		s, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = c.Close()
			continue
		}
		p.mu.Lock()
		p.conns[c], p.conns[s] = true, true
		p.mu.Unlock()
		go func() { _, _ = io.Copy(c, s); _ = c.Close() }()
		go p.upstream(c, s)
	}
}

var commit = []byte("commit")

// upstream copies client to server, watching for an armed COMMIT.
func (p *Proxy) upstream(c, s net.Conn) {
	closeServer := true
	defer func() {
		if closeServer {
			_ = s.Close()
		}
	}()
	buf := make([]byte, 32<<10)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			p.mu.Lock()
			when := p.onCommit
			hit := when != "" && bytes.Contains(bytes.ToLower(chunk), commit)
			if hit {
				p.onCommit = ""
			}
			p.mu.Unlock()
			if hit && when == "before" {
				_ = c.Close()
				return
			}
			if _, werr := s.Write(chunk); werr != nil {
				return
			}
			if hit && when == "after" {
				// Close the client side before it hears the answer; the
				// server side stays open a moment so the server reads
				// and commits it.
				_ = c.Close()
				closeServer = false
				time.AfterFunc(2*time.Second, func() { _ = s.Close() })
				return
			}
		}
		if err != nil {
			return
		}
	}
}
