package sftp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
)

const testKey = "tsk_abcdefghijklmnopqrstuvwxyz"

// ---- an in-process SSH server with an in-memory SFTP filesystem ----

type server struct {
	addr       string
	port       string
	hostKey    ssh.Signer
	clientKey  ssh.Signer
	authTries  atomic.Int32
	failPosix  atomic.Bool
	handlers   pkgsftp.Handlers
	clientPriv ed25519.PrivateKey
}

// cmd wraps the in-memory FileCmder so tests can make the final rename fail.
type cmd struct {
	inner pkgsftp.FileCmder
	s     *server
}

func (c cmd) Filecmd(r *pkgsftp.Request) error { return c.inner.Filecmd(r) }

func (c cmd) PosixRename(r *pkgsftp.Request) error {
	if c.s.failPosix.Load() {
		return errors.New("injected rename failure")
	}
	return c.inner.(pkgsftp.PosixRenameFileCmder).PosixRename(r)
}

func newSigner(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

func startServer(t *testing.T) *server {
	t.Helper()
	s := &server{}
	s.hostKey, _ = newSigner(t)
	s.clientKey, s.clientPriv = newSigner(t)
	mem := pkgsftp.InMemHandler()
	s.handlers = pkgsftp.Handlers{FileGet: mem.FileGet, FilePut: mem.FilePut, FileList: mem.FileList, FileCmd: cmd{inner: mem.FileCmd, s: s}}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(m ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			s.authTries.Add(1)
			if m.User() == "alice" && string(pw) == "s3cret" {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(m ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			s.authTries.Add(1)
			if m.User() == "bob" && string(k.Marshal()) == string(s.clientKey.PublicKey().Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		KeyboardInteractiveCallback: func(m ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			s.authTries.Add(1)
			ans, err := ch(m.User(), "", []string{"Password: "}, []bool{false})
			if err == nil && m.User() == "carol" && len(ans) == 1 && ans[0] == "s3cret" {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(s.hostKey)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s.addr = ln.Addr().String()
	_, s.port, _ = net.SplitHostPort(s.addr)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.serve(c, cfg)
			}()
		}
	}()
	return s
}

func (s *server) serve(c net.Conn, cfg *ssh.ServerConfig) {
	defer func() { _ = c.Close() }()
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for r := range creqs {
				ok := r.Type == "subsystem" && len(r.Payload) > 4 && string(r.Payload[4:]) == "sftp"
				_ = r.Reply(ok, nil)
				if ok {
					rs := pkgsftp.NewRequestServer(ch, s.handlers)
					_ = rs.Serve()
					_ = rs.Close()
					return
				}
			}
		}()
	}
}

func (s *server) authorizedLine() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.hostKey.PublicKey())))
}

// ---- harness ----

// dialer is the engine's egress-guarded dialer for these credentials,
// loosened only to reach loopback.
func dialer(c *connector.Connector, cr map[string]string) func(context.Context, string, string) (net.Conn, error) {
	var pol egress.Policy
	for _, h := range c.Manifest.Hosts() {
		if h == "${connection.host}" {
			h = cr["host"]
		}
		pol.Hosts = append(pol.Hosts, h)
	}
	guard := &egress.Guard{Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && egress.BlockedAddr(a) }}
	return func(ctx context.Context, n, a string) (net.Conn, error) { return guard.DialContext(ctx, pol, n, a) }
}

func (s *server) creds() map[string]string {
	return map[string]string{"host": "127.0.0.1", "port": s.port, "username": "alice", "password": "s3cret", "host_key": s.authorizedLine()}
}

func run(t *testing.T, cr map[string]string, action string, in map[string]any) (map[string]any, error) {
	t.Helper()
	c := New()
	r, err := c.Actions[action].Execute(context.Background(), connector.Request{Input: in, Credentials: cr, Dial: dialer(c, cr), IdempotencyKey: testKey, Attempt: 1})
	if err != nil {
		return nil, err
	}
	return r.Output.(map[string]any), nil
}

func must(t *testing.T, cr map[string]string, action string, in map[string]any) map[string]any {
	t.Helper()
	out, err := run(t, cr, action, in)
	if err != nil {
		t.Fatalf("%s %v: %v", action, in, err)
	}
	return out
}

func TestRegisters(t *testing.T) {
	if err := connector.NewRegistry().Register(New()); err != nil {
		t.Fatal(err)
	}
	if h := New().Manifest.Hosts(); len(h) != 1 || h[0] != "${connection.host}" {
		t.Errorf("hosts %v", h)
	}
}

func TestHostKeyPinning(t *testing.T) {
	s := startServer(t)
	pub := s.hostKey.PublicKey()
	formats := map[string]string{
		"authorized_keys line": s.authorizedLine() + " server@example",
		"known_hosts line":     "[127.0.0.1]:" + s.port + " " + s.authorizedLine(),
		"base64 key":           base64.StdEncoding.EncodeToString(pub.Marshal()),
		"fingerprint":          ssh.FingerprintSHA256(pub),
		"fingerprint, padded":  ssh.FingerprintSHA256(pub) + "=",
		"rotation list":        "# old and new\n" + ssh.FingerprintSHA256(s.clientKey.PublicKey()) + "\n" + s.authorizedLine(),
	}
	for name, hk := range formats {
		cr := s.creds()
		cr["host_key"] = hk
		if out, err := run(t, cr, "stat", nil); err != nil || out["exists"] != true || out["type"] != "directory" {
			t.Errorf("%s: %v %v", name, out, err)
		}
	}

	before := s.authTries.Load()
	cr := s.creds()
	delete(cr, "host_key")
	_, err := run(t, cr, "stat", nil)
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), ssh.FingerprintSHA256(pub)) ||
		!strings.Contains(err.Error(), s.authorizedLine()) || !strings.Contains(err.Error(), "no host_key") {
		t.Errorf("missing host_key: %v", err)
	}
	var hk *HostKeyError
	if !errors.As(err, &hk) || len(hk.Pinned) != 0 {
		t.Errorf("want *HostKeyError: %v", err)
	}

	cr["host_key"] = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.clientKey.PublicKey())))
	_, err = run(t, cr, "stat", nil)
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "mismatch") || !strings.Contains(err.Error(), ssh.FingerprintSHA256(pub)) {
		t.Errorf("wrong key: %v", err)
	}
	cr["host_key"] = ssh.FingerprintSHA256(s.clientKey.PublicKey())
	if _, err = run(t, cr, "stat", nil); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("wrong fingerprint: %v", err)
	}
	if n := s.authTries.Load(); n != before {
		t.Errorf("credentials were offered to an unverified server (%d attempts)", n-before)
	}
	for _, bad := range []string{"SHA256:short", "not a key", "@cert-authority *.example.com " + s.authorizedLine()} {
		cr["host_key"] = bad
		if _, err := run(t, cr, "stat", nil); effects.Classify(err) != effects.KindFatal {
			t.Errorf("host_key %q accepted: %v", bad, err)
		}
	}
}

func pemKey(t *testing.T, priv ed25519.PrivateKey, pass string) string {
	t.Helper()
	var b *pem.Block
	var err error
	if pass == "" {
		b, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		b, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(pass))
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(b))
}

func TestAuthentication(t *testing.T) {
	s := startServer(t)
	ok := func(name string, mod func(map[string]string)) {
		t.Helper()
		cr := s.creds()
		mod(cr)
		if _, err := run(t, cr, "stat", nil); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	fatal := func(name, text string, mod func(map[string]string)) {
		t.Helper()
		cr := s.creds()
		mod(cr)
		if _, err := run(t, cr, "stat", nil); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), text) {
			t.Errorf("%s: %v", name, err)
		}
	}
	ok("password", func(map[string]string) {})
	ok("private key", func(c map[string]string) {
		c["username"], c["password"], c["private_key"] = "bob", "", pemKey(t, s.clientPriv, "")
	})
	ok("private key with escaped newlines", func(c map[string]string) {
		c["username"], c["password"], c["private_key"] = "bob", "", strings.ReplaceAll(pemKey(t, s.clientPriv, ""), "\n", `\n`)
	})
	ok("encrypted private key", func(c map[string]string) {
		c["username"], c["password"] = "bob", ""
		c["private_key"], c["private_key_passphrase"] = pemKey(t, s.clientPriv, "pass phrase"), "pass phrase"
	})
	ok("keyboard-interactive", func(c map[string]string) { c["username"] = "carol" })
	ok("key and password both set", func(c map[string]string) {
		c["username"], c["private_key"] = "bob", pemKey(t, s.clientPriv, "")
	})
	fatal("wrong password", "authentication failed", func(c map[string]string) { c["password"] = "nope" })
	fatal("no credentials", "password or a private_key", func(c map[string]string) { c["password"] = "" })
	fatal("encrypted key without passphrase", "private_key_passphrase", func(c map[string]string) {
		c["username"], c["password"], c["private_key"] = "bob", "", pemKey(t, s.clientPriv, "pass phrase")
	})
	fatal("garbage key", "private_key cannot be read", func(c map[string]string) { c["private_key"] = "-----BEGIN NOTHING-----" })
	fatal("no username", "username", func(c map[string]string) { c["username"] = "" })
	fatal("bad port", "port", func(c map[string]string) { c["port"] = "ssh" })
}

func TestUploadDownloadAtomically(t *testing.T) {
	s := startServer(t)
	cr := s.creds()
	out := must(t, cr, "upload_file", map[string]any{"path": "/outbox/2026/10/pay.csv", "content": "id,amount\n1,500\n", "create_dirs": true, "mode": "0640"})
	if out["size"] != int64(16) || out["atomic"] != true || out["unchanged"] != false || out["path"] != "/outbox/2026/10/pay.csv" {
		t.Errorf("upload %v", out)
	}
	// Only the final file is left: the temporary name was renamed away.
	ls := must(t, cr, "list_directory", map[string]any{"path": "/outbox/2026/10"})
	if es := ls["entries"].([]any); len(es) != 1 || es[0].(map[string]any)["name"] != "pay.csv" || es[0].(map[string]any)["type"] != "file" {
		t.Errorf("directory after upload %v", ls)
	}
	dl := must(t, cr, "download_file", map[string]any{"path": "/outbox/2026/10/pay.csv"})
	if dl["content"] != "id,amount\n1,500\n" || dl["sha256"] != out["sha256"] || dl["encoding"] != "text" {
		t.Errorf("download %v", dl)
	}

	// Without create_dirs a missing directory fails.
	if _, err := run(t, cr, "upload_file", map[string]any{"path": "/nowhere/x.txt", "content": "x"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("missing directory: %v", err)
	}

	// Binary content round trip.
	bin := []byte{0, 1, 2, 0xff, 0xfe}
	must(t, cr, "upload_file", map[string]any{"path": "/outbox/blob.bin", "content": base64.StdEncoding.EncodeToString(bin), "encoding": "base64"})
	dl = must(t, cr, "download_file", map[string]any{"path": "/outbox/blob.bin", "encoding": "base64"})
	if dl["content"] != base64.StdEncoding.EncodeToString(bin) || dl["size"] != int64(5) {
		t.Errorf("binary %v", dl)
	}
	if _, err := run(t, cr, "download_file", map[string]any{"path": "/outbox/blob.bin"}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "base64") {
		t.Errorf("binary as text: %v", err)
	}
	if _, err := run(t, cr, "download_file", map[string]any{"path": "/outbox/blob.bin", "encoding": "base64", "max_bytes": int64(4)}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "max_bytes") {
		t.Errorf("size cap: %v", err)
	}
	if _, err := run(t, cr, "download_file", map[string]any{"path": "/outbox"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("directory download: %v", err)
	}
	if _, err := run(t, cr, "download_file", map[string]any{"path": "/outbox/none"}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("missing file: %v", err)
	}

	// Overwrite (the default) replaces; repeating is harmless.
	for range 2 {
		must(t, cr, "upload_file", map[string]any{"path": "/outbox/2026/10/pay.csv", "content": "v2"})
	}
	if dl := must(t, cr, "download_file", map[string]any{"path": "/outbox/2026/10/pay.csv"}); dl["content"] != "v2" {
		t.Errorf("overwrite %v", dl)
	}
	// overwrite=false: identical content is a completed retry, different
	// content is refused.
	out = must(t, cr, "upload_file", map[string]any{"path": "/outbox/2026/10/pay.csv", "content": "v2", "overwrite": false})
	if out["unchanged"] != true {
		t.Errorf("identical retry %v", out)
	}
	if _, err := run(t, cr, "upload_file", map[string]any{"path": "/outbox/2026/10/pay.csv", "content": "v3", "overwrite": false}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("overwrite false: %v", err)
	}
	must(t, cr, "upload_file", map[string]any{"path": "/outbox/new.csv", "content": "n", "overwrite": false})

	// A failed final rename leaves the old file whole and no temporary file.
	s.failPosix.Store(true)
	if _, err := run(t, cr, "upload_file", map[string]any{"path": "/outbox/2026/10/pay.csv", "content": "half-written v4"}); err == nil {
		t.Fatal("upload should fail")
	}
	s.failPosix.Store(false)
	if dl := must(t, cr, "download_file", map[string]any{"path": "/outbox/2026/10/pay.csv"}); dl["content"] != "v2" {
		t.Errorf("after failed rename %v", dl)
	}
	if ls := must(t, cr, "list_directory", map[string]any{"path": "/outbox/2026/10"}); ls["count"] != 1 {
		t.Errorf("temporary file left behind: %v", ls)
	}
	if _, err := run(t, cr, "upload_file", map[string]any{"path": "/outbox/x", "content": strings.Repeat("a", MaxUploadBytes+1)}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("oversize upload: %v", err)
	}
	if _, err := run(t, cr, "upload_file", map[string]any{"path": "/outbox/x", "content": "x", "mode": "999"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("bad mode: %v", err)
	}
}

func TestListStatRenameDeleteMkdir(t *testing.T) {
	s := startServer(t)
	cr := s.creds()
	if out := must(t, cr, "mkdir", map[string]any{"path": "/in/a/b"}); out["created"] != true {
		t.Errorf("mkdir -p %v", out)
	}
	if out := must(t, cr, "mkdir", map[string]any{"path": "/in/a/b"}); out["created"] != false {
		t.Errorf("mkdir again %v", out)
	}
	if _, err := run(t, cr, "mkdir", map[string]any{"path": "/x/y", "parents": false}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("mkdir without parents: %v", err)
	}
	for _, n := range []string{"c.txt", "a.txt", "b.txt"} {
		must(t, cr, "upload_file", map[string]any{"path": "/in/" + n, "content": n})
	}
	if _, err := run(t, cr, "mkdir", map[string]any{"path": "/in/a.txt"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("mkdir over a file: %v", err)
	}
	ls := must(t, cr, "list_directory", map[string]any{"path": "/in", "max_entries": int64(3)})
	es := ls["entries"].([]any)
	if ls["truncated"] != true || len(es) != 3 || es[0].(map[string]any)["name"] != "a" || es[0].(map[string]any)["type"] != "directory" ||
		es[1].(map[string]any)["path"] != "/in/a.txt" || es[1].(map[string]any)["size"] != int64(5) {
		t.Errorf("list %v", ls)
	}
	if st := must(t, cr, "stat", map[string]any{"path": "/in/b.txt"}); st["exists"] != true || st["type"] != "file" || st["size"] != int64(5) {
		t.Errorf("stat %v", st)
	}
	if st := must(t, cr, "stat", map[string]any{"path": "/in/zzz"}); st["exists"] != false {
		t.Errorf("stat missing %v", st)
	}
	if _, err := run(t, cr, "list_directory", map[string]any{"path": "/nope"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("list missing: %v", err)
	}

	// Rename, and its retry after a lost reply.
	r := must(t, cr, "rename", map[string]any{"from": "/in/a.txt", "to": "/done/2026/a.txt", "create_dirs": true})
	if r["already_done"] != false {
		t.Errorf("rename %v", r)
	}
	if r := must(t, cr, "rename", map[string]any{"from": "/in/a.txt", "to": "/done/2026/a.txt"}); r["already_done"] != true {
		t.Errorf("rename retry %v", r)
	}
	if _, err := run(t, cr, "rename", map[string]any{"from": "/in/b.txt", "to": "/in/c.txt"}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("rename onto existing: %v", err)
	}
	if r := must(t, cr, "rename", map[string]any{"from": "/in/b.txt", "to": "/in/c.txt", "overwrite": true}); r["atomic"] != true {
		t.Errorf("rename overwrite %v", r)
	}
	if dl := must(t, cr, "download_file", map[string]any{"path": "/in/c.txt"}); dl["content"] != "b.txt" {
		t.Errorf("after overwrite %v", dl)
	}
	if _, err := run(t, cr, "rename", map[string]any{"from": "/in/ghost", "to": "/in/ghost2"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("rename missing: %v", err)
	}

	// Delete: files, empty directories, and missing paths.
	if d := must(t, cr, "delete", map[string]any{"path": "/in/c.txt"}); d["deleted"] != true {
		t.Errorf("delete %v", d)
	}
	if d := must(t, cr, "delete", map[string]any{"path": "/in/c.txt"}); d["deleted"] != false {
		t.Errorf("delete again %v", d)
	}
	if _, err := run(t, cr, "delete", map[string]any{"path": "/in/c.txt", "missing_ok": false}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("delete missing strict: %v", err)
	}
	if d := must(t, cr, "delete", map[string]any{"path": "/in/a/b"}); d["deleted"] != true {
		t.Errorf("delete dir %v", d)
	}
	if _, err := run(t, cr, "delete", map[string]any{"path": "/"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("delete root: %v", err)
	}
}

func TestDialAndTransportErrors(t *testing.T) {
	s := startServer(t)
	cr := s.creds()
	c := New()
	// No dialer: never dial directly.
	if _, err := c.Actions["stat"].Execute(context.Background(), connector.Request{Credentials: cr}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("nil Dial: %v", err)
	}
	// The default guard refuses loopback: fatal, nothing sent.
	strict := &egress.Guard{}
	dial := func(ctx context.Context, n, a string) (net.Conn, error) {
		return strict.DialContext(ctx, egress.Policy{Hosts: []string{cr["host"]}}, n, a)
	}
	if _, err := c.Actions["stat"].Execute(context.Background(), connector.Request{Credentials: cr, Dial: dial}); effects.Classify(err) != effects.KindFatal || !errors.Is(err, egress.ErrDenied) {
		t.Errorf("egress refusal: %v", err)
	}
	// Nothing listening: retryable, provably not sent.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_, closed, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	cr["port"] = closed
	if _, err := run(t, cr, "upload_file", map[string]any{"path": "/x", "content": "x"}); effects.Classify(err) != effects.KindNotSent {
		t.Errorf("refused: %v (%v)", err, effects.Classify(err))
	}
	// A server that hangs up during the handshake: not sent.
	ln, _ = net.Listen("tcp", "127.0.0.1:0")
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	_, cr["port"], _ = net.SplitHostPort(ln.Addr().String())
	if _, err := run(t, cr, "stat", nil); effects.Classify(err) != effects.KindNotSent {
		t.Errorf("hang-up: %v (%v)", err, effects.Classify(err))
	}
}

func TestOpErrorClassification(t *testing.T) {
	if effects.Classify(opError("write", "/x", io.ErrUnexpectedEOF, true)) != effects.KindUnknownOutcome {
		t.Error("a write cut off mid-way is an unknown outcome")
	}
	if effects.Classify(opError("read", "/x", io.ErrUnexpectedEOF, false)) != effects.KindRetryable {
		t.Error("a read cut off mid-way is retryable")
	}
	if effects.Classify(opError("read", "/x", &pkgsftp.StatusError{Code: uint32(pkgsftp.ErrSSHFxConnectionLost)}, false)) != effects.KindRetryable {
		t.Error("connection lost is retryable for reads")
	}
	if effects.Classify(opError("write", "/x", &pkgsftp.StatusError{Code: uint32(pkgsftp.ErrSSHFxFailure)}, true)) != effects.KindFatal {
		t.Error("a server failure status is fatal")
	}
	if got := tempName("/out/report.csv", testKey); got != "/out/.report.csv."+testKey+".part" {
		t.Errorf("temp name %s", got)
	}
	if got := tempName("/out/"+strings.Repeat("n", 300), ""); len(got) > 200 || !strings.HasSuffix(got, ".part") {
		t.Errorf("long temp name %s", got)
	}
}
