// Package sftp is the SFTP connector: files on an SSH server, reached only
// through the engine's egress-guarded dialer (docs/integrations/sftp.md).
//
// Host keys are always verified against the connection's pinned host_key.
// A connection without one is refused after the key exchange and before
// any credential is sent, with the server's key in the error so an
// administrator can check and pin it.
package sftp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

const (
	// MaxUploadBytes caps upload_file's decoded content.
	MaxUploadBytes = 10 << 20
	// MaxDownloadBytes is the largest max_bytes download_file accepts.
	MaxDownloadBytes    = 10 << 20
	defaultDownload     = 1 << 20
	handshakeTimeout    = 30 * time.Second
	posixRenameExt      = "posix-rename@openssh.com"
	clientVersion       = "SSH-2.0-Taskiem"
	maxTempNameBaseSize = 100
)

// New returns the SFTP connector.
func New() *connector.Connector {
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"upload_file":    connector.ActionFunc(uploadFile),
		"download_file":  connector.ActionFunc(downloadFile),
		"list_directory": connector.ActionFunc(listDirectory),
		"stat":           connector.ActionFunc(stat),
		"rename":         connector.ActionFunc(rename),
		"delete":         connector.ActionFunc(deleteFile),
		"mkdir":          connector.ActionFunc(mkdir),
	}}
}

func fatalf(format string, args ...any) error {
	return fmt.Errorf("sftp: "+format+": %w", append(args, effects.ErrFatal)...)
}

// ---- host key pinning ----

// pin is one acceptable host key: a full key, or a SHA256 fingerprint.
type pin struct {
	key ssh.PublicKey
	fp  string
}

// parsePins reads host_key: one or more lines, each a known_hosts line, an
// authorized_keys line ("ssh-ed25519 AAAA... comment"), a bare base64 key,
// or a "SHA256:..." fingerprint as ssh-keygen -l prints it.
func parsePins(s string) ([]pin, error) {
	var out []pin
	for _, line := range strings.Split(strings.ReplaceAll(s, `\n`, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if fp, ok := strings.CutPrefix(line, "SHA256:"); ok {
			fp = strings.TrimRight(fp, "=")
			if raw, err := base64.RawStdEncoding.DecodeString(fp); err != nil || len(raw) != sha256.Size {
				return nil, fmt.Errorf("host_key fingerprint %q is not a SHA256 fingerprint", line)
			}
			out = append(out, pin{fp: "SHA256:" + fp})
			continue
		}
		if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line)); err == nil {
			out = append(out, pin{key: k})
			continue
		}
		if marker, _, k, _, _, err := ssh.ParseKnownHosts([]byte(line)); err == nil {
			if marker != "" {
				return nil, fmt.Errorf("host_key: @%s lines are not supported; pin the server's own key", marker)
			}
			out = append(out, pin{key: k})
			continue
		}
		if raw, err := base64.StdEncoding.DecodeString(line); err == nil {
			if k, err := ssh.ParsePublicKey(raw); err == nil {
				out = append(out, pin{key: k})
				continue
			}
		}
		return nil, fmt.Errorf("host_key line %q is not a known_hosts line, public key or SHA256 fingerprint", truncate(line, 60))
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// HostKeyError is a server key that is not pinned.
type HostKeyError struct {
	Presented ssh.PublicKey
	Pinned    []string // fingerprints of the pinned keys; empty when none was set
}

func (e *HostKeyError) Error() string {
	fp := ssh.FingerprintSHA256(e.Presented)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(e.Presented)))
	if len(e.Pinned) == 0 {
		return fmt.Sprintf("sftp: the connection has no host_key, so the server was not trusted and no credentials were sent. "+
			"The server presented %s %s. Confirm this fingerprint with the server's administrator, then set host_key to: %s",
			e.Presented.Type(), fp, line)
	}
	return fmt.Sprintf("sftp: host key mismatch: the server presented %s %s, which is not the pinned key (%s). "+
		"No credentials were sent. If the server's key was changed deliberately, set host_key to: %s; otherwise the connection may be intercepted",
		e.Presented.Type(), fp, strings.Join(e.Pinned, ", "), line)
}

func (e *HostKeyError) Unwrap() error { return effects.ErrFatal }

func hostKeyCallback(pins []pin) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		var pinned []string
		for _, p := range pins {
			if p.key != nil {
				if bytes.Equal(p.key.Marshal(), key.Marshal()) {
					return nil
				}
				pinned = append(pinned, ssh.FingerprintSHA256(p.key))
			} else {
				if p.fp == fp {
					return nil
				}
				pinned = append(pinned, p.fp)
			}
		}
		return &HostKeyError{Presented: key, Pinned: pinned}
	}
}

// hostKeyAlgorithms asks the server for the pinned key types, so a server
// with several host keys presents the one that was pinned. Fingerprint pins
// do not say their type, so they leave the default order.
func hostKeyAlgorithms(pins []pin) []string {
	var out []string
	seen := map[string]bool{}
	add := func(a ...string) {
		for _, x := range a {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	for _, p := range pins {
		if p.key == nil {
			return nil
		}
		if p.key.Type() == ssh.KeyAlgoRSA {
			add(ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA)
		} else {
			add(p.key.Type())
		}
	}
	return out
}

// ---- connecting ----

func authMethods(c map[string]string) ([]ssh.AuthMethod, error) {
	var out []ssh.AuthMethod
	if pk := c["private_key"]; strings.TrimSpace(pk) != "" {
		if !strings.Contains(pk, "\n") {
			pk = strings.ReplaceAll(pk, `\n`, "\n")
		}
		var signer ssh.Signer
		var err error
		if pp := c["private_key_passphrase"]; pp != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(pk), []byte(pp))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(pk))
		}
		var missing *ssh.PassphraseMissingError
		switch {
		case errors.As(err, &missing):
			return nil, fatalf("private_key is encrypted: set private_key_passphrase")
		case err != nil:
			return nil, fatalf("private_key cannot be read (%v)", err)
		}
		out = append(out, ssh.PublicKeys(signer))
	}
	if pw := c["password"]; pw != "" {
		out = append(out, ssh.Password(pw), ssh.KeyboardInteractive(func(_, _ string, questions []string, echos []bool) ([]string, error) {
			// Servers often ask for the password this way; answer hidden
			// prompts with it and leave visible ones empty.
			ans := make([]string, len(questions))
			for i := range questions {
				if !echos[i] {
					ans[i] = pw
				}
			}
			return ans, nil
		}))
	}
	if len(out) == 0 {
		return nil, fatalf("the connection needs a password or a private_key")
	}
	return out, nil
}

type session struct {
	ssh  *ssh.Client
	c    *pkgsftp.Client
	stop func() bool
}

func (s *session) Close() {
	s.stop()
	_ = s.c.Close()
	_ = s.ssh.Close()
}

// open connects, verifies the host key, authenticates and starts SFTP.
// Every failure here happens before any file operation, so it is either
// fatal (configuration, host key, credentials) or not_sent.
func open(ctx context.Context, req connector.Request) (*session, error) {
	cr := req.Credentials
	host, user := strings.TrimSpace(cr["host"]), strings.TrimSpace(cr["username"])
	if host == "" || user == "" {
		return nil, fatalf("the connection needs host and username")
	}
	if req.Dial == nil {
		return nil, fatalf("no egress dialer")
	}
	port := strings.TrimSpace(cr["port"])
	if port == "" {
		port = "22"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, fatalf("port %q is not a port number", port)
	}
	pins, err := parsePins(cr["host_key"])
	if err != nil {
		return nil, fatalf("%v", err)
	}
	auth, err := authMethods(cr)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User: user, Auth: auth, ClientVersion: clientVersion,
		HostKeyCallback:   hostKeyCallback(pins),
		HostKeyAlgorithms: hostKeyAlgorithms(pins),
	}
	addr := net.JoinHostPort(host, port)
	conn, err := req.Dial(ctx, "tcp", addr)
	if err != nil {
		if errors.Is(err, effects.ErrFatal) || errors.Is(err, effects.ErrNotSent) {
			return nil, fmt.Errorf("sftp: connect %s: %w", addr, err)
		}
		return nil, fmt.Errorf("sftp: connect %s: %w: %w", addr, err, effects.ErrNotSent)
	}
	deadline := time.Now().Add(handshakeTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		stop()
		_ = conn.Close()
		return nil, handshakeError(err)
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(sc, chans, reqs)
	sf, err := pkgsftp.NewClient(client)
	if err != nil {
		stop()
		_ = client.Close()
		if strings.Contains(err.Error(), "subsystem request failed") {
			return nil, fatalf("the server does not offer the sftp subsystem")
		}
		return nil, fmt.Errorf("sftp: starting sftp: %w: %w", err, effects.ErrNotSent)
	}
	return &session{ssh: client, c: sf, stop: stop}, nil
}

func handshakeError(err error) error {
	var hk *HostKeyError
	if errors.As(err, &hk) {
		return hk
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate"):
		return fatalf("authentication failed for this username with the given password or key (%v)", err)
	case strings.Contains(msg, "no common algorithm"):
		return fatalf("the server and Taskiem share no SSH algorithm (%v)", err)
	}
	return fmt.Errorf("sftp: ssh handshake: %w: %w", err, effects.ErrNotSent)
}

// opError classifies a failed file operation. The server's own answers
// (missing, permission, failure) are fatal; a connection lost mid-operation
// leaves a read retryable and a write's outcome unknown.
func opError(op, p string, err error, write bool) error {
	var se *pkgsftp.StatusError
	switch {
	case errors.Is(err, effects.ErrFatal), errors.Is(err, effects.ErrRetryable), errors.Is(err, effects.ErrUnknownOutcome):
		return err
	case errors.Is(err, fs.ErrNotExist):
		return fatalf("%s %s: no such file or directory", op, p)
	case errors.Is(err, fs.ErrPermission):
		return fatalf("%s %s: permission denied", op, p)
	case errors.As(err, &se):
		switch se.FxCode() {
		case pkgsftp.ErrSSHFxNoConnection, pkgsftp.ErrSSHFxConnectionLost:
		default:
			return fatalf("%s %s: %v", op, p, err)
		}
	}
	if write {
		return fmt.Errorf("sftp: %s %s: %w: %w", op, p, err, effects.ErrUnknownOutcome)
	}
	return fmt.Errorf("sftp: %s %s: %w: %w", op, p, err, effects.ErrRetryable)
}

// ---- inputs ----

func str(in map[string]any, k string) string {
	s, _ := in[k].(string)
	return s
}

func boolArg(in map[string]any, k string, def bool) bool {
	if b, ok := in[k].(bool); ok {
		return b
	}
	return def
}

func intArg(in map[string]any, k string, def int64) int64 {
	switch v := in[k].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case interface{ Int64() (int64, error) }:
		if n, err := v.Int64(); err == nil {
			return n
		}
	}
	return def
}

func pathArg(in map[string]any, k, def string) (string, error) {
	p := str(in, k)
	if p == "" {
		p = def
	}
	if p == "" {
		return "", fatalf("%s is required", k)
	}
	if strings.ContainsRune(p, 0) || !utf8.ValidString(p) {
		return "", fatalf("%s must be UTF-8 text without NUL", k)
	}
	return path.Clean(p), nil
}

func decode(content, enc string) ([]byte, error) {
	switch enc {
	case "", "text":
		return []byte(content), nil
	case "base64":
		b, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fatalf("content is not valid base64: %v", err)
		}
		return b, nil
	}
	return nil, fatalf("encoding %q must be text or base64", enc)
}

func fileType(fi os.FileInfo) string {
	switch m := fi.Mode(); {
	case m.IsRegular():
		return "file"
	case m.IsDir():
		return "directory"
	case m&os.ModeSymlink != 0:
		return "symlink"
	}
	return "other"
}

func info(p string, fi os.FileInfo) map[string]any {
	return map[string]any{
		"path": p, "type": fileType(fi), "size": fi.Size(),
		"mode": fmt.Sprintf("%04o", fi.Mode().Perm()), "modified": fi.ModTime().UTC().Format(time.RFC3339),
	}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func logKey(req connector.Request, action, p string) {
	if req.Logger != nil {
		req.Logger.Info("sftp write", "action", action, "path", p, "idempotency_key", req.IdempotencyKey)
	}
}

// exists reports whether p exists (without following a final symlink).
func exists(c *pkgsftp.Client, p string) (os.FileInfo, bool, error) {
	fi, err := c.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return fi, true, nil
}

// readCapped reads at most limit bytes of p, failing if it is larger.
func readCapped(c *pkgsftp.Client, p string, limit int64) ([]byte, error) {
	f, err := c.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fatalf("%s is over max_bytes %d", p, limit)
	}
	return data, nil
}

// tempName is a hidden name beside dst: derived from the engine's key, so
// a retry reuses (and truncates) its own leftover rather than adding another.
func tempName(dst, key string) string {
	base := path.Base(dst)
	if len(base) > maxTempNameBaseSize {
		base = base[:maxTempNameBaseSize]
	}
	suffix := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return -1
	}, key)
	if suffix == "" {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		suffix = hex.EncodeToString(b)
	}
	return path.Join(path.Dir(dst), "."+base+"."+suffix+".part")
}

// replace moves src onto dst. With overwrite it uses posix-rename (atomic
// replace) when the server offers it, else removes dst first (not atomic).
func replace(c *pkgsftp.Client, src, dst string, overwrite bool) (atomic bool, err error) {
	if !overwrite {
		return true, c.Rename(src, dst)
	}
	if _, ok := c.HasExtension(posixRenameExt); ok {
		return true, c.PosixRename(src, dst)
	}
	if err := c.Rename(src, dst); err == nil {
		return true, nil
	}
	if _, there, _ := exists(c, dst); !there {
		return true, c.Rename(src, dst)
	}
	if err := c.Remove(dst); err != nil {
		return false, err
	}
	return false, c.Rename(src, dst)
}

// ---- actions ----

func uploadFile(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	dst, err := pathArg(in, "path", "")
	if err != nil {
		return connector.Response{}, err
	}
	content, ok := in["content"].(string)
	if !ok {
		return connector.Response{}, fatalf("content must be a string")
	}
	data, err := decode(content, str(in, "encoding"))
	if err != nil {
		return connector.Response{}, err
	}
	if len(data) > MaxUploadBytes {
		return connector.Response{}, fatalf("content is %d bytes; upload_file takes at most %d", len(data), MaxUploadBytes)
	}
	var mode os.FileMode
	if m := str(in, "mode"); m != "" {
		n, err := strconv.ParseUint(m, 8, 32)
		if err != nil || n > 0o7777 {
			return connector.Response{}, fatalf("mode %q is not an octal permission such as 0640", m)
		}
		mode = os.FileMode(n)
	}
	overwrite := boolArg(in, "overwrite", true)
	s, err := open(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer s.Close()
	c := s.c
	logKey(req, "upload_file", dst)
	out := map[string]any{"path": dst, "size": int64(len(data)), "sha256": sum(data), "atomic": true, "unchanged": false}

	if dir := path.Dir(dst); boolArg(in, "create_dirs", false) && dir != "." && dir != "/" {
		if err := c.MkdirAll(dir); err != nil {
			return connector.Response{}, opError("mkdir -p", dir, err, true)
		}
	}
	if !overwrite {
		fi, there, err := exists(c, dst)
		if err != nil {
			return connector.Response{}, opError("stat", dst, err, false)
		}
		if there {
			// A retry of an upload that succeeded finds its own content.
			if fi.Mode().IsRegular() && fi.Size() == int64(len(data)) {
				got, err := readCapped(c, dst, int64(len(data)))
				if err != nil {
					return connector.Response{}, opError("read", dst, err, false)
				}
				if bytes.Equal(got, data) {
					out["unchanged"] = true
					return connector.Response{Output: out}, nil
				}
			}
			return connector.Response{}, fatalf("%s already exists with other content and overwrite is false", dst)
		}
	}
	tmp := tempName(dst, req.IdempotencyKey)
	cleanup := func() { _ = c.Remove(tmp) }
	f, err := c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return connector.Response{}, opError("create", tmp, err, true)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return connector.Response{}, opError("write", tmp, err, true)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return connector.Response{}, opError("close", tmp, err, true)
	}
	if fi, err := c.Stat(tmp); err != nil || fi.Size() != int64(len(data)) {
		cleanup()
		if err == nil {
			err = fmt.Errorf("wrote %d bytes, server has %d", len(data), fi.Size())
		}
		return connector.Response{}, opError("verify", tmp, err, true)
	}
	if mode != 0 {
		if err := c.Chmod(tmp, mode); err != nil {
			cleanup()
			return connector.Response{}, opError("chmod", tmp, err, true)
		}
	}
	atomic, err := replace(c, tmp, dst, overwrite)
	if err != nil {
		cleanup()
		if !overwrite {
			if _, there, _ := exists(c, dst); there {
				return connector.Response{}, fatalf("%s was created by someone else during the upload and overwrite is false", dst)
			}
		}
		return connector.Response{}, opError("rename into place", dst, err, true)
	}
	out["atomic"] = atomic
	return connector.Response{Output: out}, nil
}

func downloadFile(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	p, err := pathArg(in, "path", "")
	if err != nil {
		return connector.Response{}, err
	}
	enc := str(in, "encoding")
	if enc == "" {
		enc = "text"
	}
	if enc != "text" && enc != "base64" {
		return connector.Response{}, fatalf("encoding %q must be text or base64", enc)
	}
	limit := intArg(in, "max_bytes", defaultDownload)
	if limit < 1 || limit > MaxDownloadBytes {
		return connector.Response{}, fatalf("max_bytes must be 1 to %d", MaxDownloadBytes)
	}
	s, err := open(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer s.Close()
	fi, err := s.c.Stat(p)
	if err != nil {
		return connector.Response{}, opError("stat", p, err, false)
	}
	if !fi.Mode().IsRegular() {
		return connector.Response{}, fatalf("%s is a %s, not a file", p, fileType(fi))
	}
	if fi.Size() > limit {
		return connector.Response{}, fatalf("%s is %d bytes, over max_bytes %d", p, fi.Size(), limit)
	}
	data, err := readCapped(s.c, p, limit)
	if err != nil {
		return connector.Response{}, opError("read", p, err, false)
	}
	out := info(p, fi)
	delete(out, "type")
	out["size"], out["sha256"], out["encoding"] = int64(len(data)), sum(data), enc
	if enc == "text" {
		if !utf8.Valid(data) {
			return connector.Response{}, fatalf("%s is not UTF-8 text; download it with encoding base64", p)
		}
		out["content"] = string(data)
	} else {
		out["content"] = base64.StdEncoding.EncodeToString(data)
	}
	return connector.Response{Output: out}, nil
}

func listDirectory(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	p, err := pathArg(in, "path", ".")
	if err != nil {
		return connector.Response{}, err
	}
	limit := intArg(in, "max_entries", 1000)
	if limit < 1 || limit > 10000 {
		return connector.Response{}, fatalf("max_entries must be 1 to 10000")
	}
	s, err := open(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer s.Close()
	fis, err := s.c.ReadDir(p)
	if err != nil {
		return connector.Response{}, opError("list", p, err, false)
	}
	sort.Slice(fis, func(i, j int) bool { return fis[i].Name() < fis[j].Name() })
	truncated := int64(len(fis)) > limit
	if truncated {
		fis = fis[:limit]
	}
	entries := make([]any, 0, len(fis))
	for _, fi := range fis {
		e := info(path.Join(p, fi.Name()), fi)
		e["name"] = fi.Name()
		entries = append(entries, e)
	}
	return connector.Response{Output: map[string]any{"entries": entries, "count": len(entries), "truncated": truncated}}, nil
}

func stat(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := pathArg(req.Input, "path", ".")
	if err != nil {
		return connector.Response{}, err
	}
	s, err := open(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer s.Close()
	fi, err := s.c.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return connector.Response{Output: map[string]any{"exists": false, "path": p}}, nil
	}
	if err != nil {
		return connector.Response{}, opError("stat", p, err, false)
	}
	out := info(p, fi)
	out["exists"] = true
	return connector.Response{Output: out}, nil
}

func rename(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	from, err := pathArg(in, "from", "")
	if err != nil {
		return connector.Response{}, err
	}
	to, err := pathArg(in, "to", "")
	if err != nil {
		return connector.Response{}, err
	}
	if from == to {
		return connector.Response{}, fatalf("from and to are the same path")
	}
	overwrite := boolArg(in, "overwrite", false)
	s, err := open(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer s.Close()
	c := s.c
	logKey(req, "rename", from+" -> "+to)
	out := map[string]any{"from": from, "to": to, "already_done": false, "atomic": true}
	_, srcThere, err := exists(c, from)
	if err != nil {
		return connector.Response{}, opError("stat", from, err, false)
	}
	_, dstThere, err := exists(c, to)
	if err != nil {
		return connector.Response{}, opError("stat", to, err, false)
	}
	switch {
	case !srcThere && dstThere:
		// The rename already happened (a retry after a lost reply).
		out["already_done"] = true
		return connector.Response{Output: out}, nil
	case !srcThere:
		return connector.Response{}, fatalf("rename %s: no such file or directory", from)
	case dstThere && !overwrite:
		return connector.Response{}, fatalf("%s already exists and overwrite is false", to)
	}
	if dir := path.Dir(to); boolArg(in, "create_dirs", false) && dir != "." && dir != "/" {
		if err := c.MkdirAll(dir); err != nil {
			return connector.Response{}, opError("mkdir -p", dir, err, true)
		}
	}
	atomic, err := replace(c, from, to, overwrite)
	if err != nil {
		return connector.Response{}, opError("rename", from, err, true)
	}
	out["atomic"] = atomic
	return connector.Response{Output: out}, nil
}

func deleteFile(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	p, err := pathArg(in, "path", "")
	if err != nil {
		return connector.Response{}, err
	}
	if p == "." || p == "/" {
		return connector.Response{}, fatalf("refusing to delete %q", p)
	}
	s, err := open(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer s.Close()
	logKey(req, "delete", p)
	fi, there, err := exists(s.c, p)
	if err != nil {
		return connector.Response{}, opError("stat", p, err, false)
	}
	if !there {
		if !boolArg(in, "missing_ok", true) {
			return connector.Response{}, fatalf("delete %s: no such file or directory", p)
		}
		return connector.Response{Output: map[string]any{"path": p, "deleted": false}}, nil
	}
	if fi.IsDir() {
		err = s.c.RemoveDirectory(p)
	} else {
		err = s.c.Remove(p)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Gone between the check and the delete.
			return connector.Response{Output: map[string]any{"path": p, "deleted": false}}, nil
		}
		return connector.Response{}, opError("delete", p, err, true)
	}
	return connector.Response{Output: map[string]any{"path": p, "deleted": true}}, nil
}

func mkdir(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	p, err := pathArg(in, "path", "")
	if err != nil {
		return connector.Response{}, err
	}
	s, err := open(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer s.Close()
	logKey(req, "mkdir", p)
	fi, there, err := exists(s.c, p)
	if err != nil {
		return connector.Response{}, opError("stat", p, err, false)
	}
	if there {
		if !fi.IsDir() {
			return connector.Response{}, fatalf("%s exists and is not a directory", p)
		}
		return connector.Response{Output: map[string]any{"path": p, "created": false}}, nil
	}
	if boolArg(in, "parents", true) {
		err = s.c.MkdirAll(p)
	} else {
		err = s.c.Mkdir(p)
	}
	if err != nil {
		if fi, serr := s.c.Stat(p); serr == nil && fi.IsDir() {
			return connector.Response{Output: map[string]any{"path": p, "created": false}}, nil
		}
		return connector.Response{}, opError("mkdir", p, err, true)
	}
	return connector.Response{Output: map[string]any{"path": p, "created": true}}, nil
}
