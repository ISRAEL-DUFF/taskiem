// Package tunnel reaches a host on a customer's private network through an
// SSH bastion the customer runs (decision 0013, mechanism 1).
//
// The worker never opens a socket to the private host. It dials the
// bastion, through the egress guard like any other public host, checks the
// bastion's host key against the key the connection pinned, signs in, and
// asks the bastion for one direct-tcpip channel to the connection's
// host:port. The bastion makes that last hop, on the customer's own
// network and under the customer's own firewall rules. No shell, agent
// forwarding or other channel is ever requested, and the tunnel lives for
// one call: closing the returned connection (or the call's context ending)
// closes the SSH session.
package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
)

// The connection fields that make a connection reach its host through a
// tunnel. The first is the switch: a connection without it dials directly.
const (
	FieldHost       = "tunnel_host"
	FieldPort       = "tunnel_port"
	FieldUser       = "tunnel_user"
	FieldPrivateKey = "tunnel_private_key"
	FieldPassphrase = "tunnel_passphrase"
	FieldHostKey    = "tunnel_host_key"
)

const handshakeTimeout = 30 * time.Second

// DialFunc opens a connection to the bastion. It is the egress guard's
// dialer under a policy that allows the bastion alone.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Config is a connection's tunnel.
type Config struct {
	Host, Port, User string
	PrivateKey       string
	Passphrase       string
	HostKey          string
}

func fatalf(format string, args ...any) error {
	return fmt.Errorf("tunnel: "+format+": %w", append(args, effects.ErrFatal)...)
}

// FromCredentials reads the tunnel a connection names, or nil when it names
// none.
func FromCredentials(c map[string]string) (*Config, error) {
	host := strings.TrimSpace(c[FieldHost])
	if host == "" {
		return nil, nil
	}
	t := &Config{
		Host: host, Port: strings.TrimSpace(c[FieldPort]), User: strings.TrimSpace(c[FieldUser]),
		PrivateKey: c[FieldPrivateKey], Passphrase: c[FieldPassphrase], HostKey: c[FieldHostKey],
	}
	if t.Port == "" {
		t.Port = "22"
	}
	if n, err := strconv.Atoi(t.Port); err != nil || n < 1 || n > 65535 {
		return nil, fatalf("tunnel_port %q is not a port number", t.Port)
	}
	if t.User == "" {
		return nil, fatalf("a tunnel needs tunnel_user")
	}
	if strings.TrimSpace(t.PrivateKey) == "" {
		return nil, fatalf("a tunnel needs tunnel_private_key")
	}
	return t, nil
}

// Addr is the bastion's address.
func (t *Config) Addr() string { return net.JoinHostPort(t.Host, t.Port) }

// Dial opens a connection to target ("host:port", as the connection names
// it) through the bastion. via dials the bastion itself.
func (t *Config) Dial(ctx context.Context, via DialFunc, network, target string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fatalf("only tcp connections can go through a tunnel, not %q", network)
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return nil, fatalf("the destination %q is not host:port", target)
	}
	pins, err := ParsePins(t.HostKey)
	if err != nil {
		return nil, fatalf("%v", err)
	}
	auth, err := t.auth()
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User: t.User, Auth: auth, ClientVersion: "SSH-2.0-Taskiem",
		HostKeyCallback:   hostKeyCallback(pins),
		HostKeyAlgorithms: hostKeyAlgorithms(pins),
	}
	conn, err := via(ctx, "tcp", t.Addr())
	if err != nil {
		if errors.Is(err, effects.ErrFatal) || errors.Is(err, effects.ErrNotSent) {
			return nil, fmt.Errorf("tunnel: connect to %s: %w", t.Addr(), err)
		}
		return nil, fmt.Errorf("tunnel: connect to %s: %w: %w", t.Addr(), err, effects.ErrNotSent)
	}
	deadline := time.Now().Add(handshakeTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	sc, chans, reqs, err := ssh.NewClientConn(conn, t.Addr(), cfg)
	if err != nil {
		stop()
		_ = conn.Close()
		return nil, handshakeError(err)
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(sc, chans, reqs)
	// The one channel this session ever opens.
	ch, err := client.Dial("tcp", target)
	if err != nil {
		stop()
		_ = client.Close()
		return nil, fmt.Errorf("tunnel: the bastion could not reach %s: %w: %w", target, err, effects.ErrNotSent)
	}
	return &tunnelled{Conn: ch, client: client, stop: stop}, nil
}

// tunnelled is a connection through the bastion. Closing it ends the SSH
// session, so a tunnel never outlives the call that opened it.
type tunnelled struct {
	net.Conn
	client *ssh.Client
	stop   func() bool
	once   sync.Once
}

func (t *tunnelled) Close() error {
	var err error
	t.once.Do(func() {
		t.stop()
		err = t.Conn.Close()
		_ = t.client.Close()
	})
	return err
}

func (t *Config) auth() ([]ssh.AuthMethod, error) {
	pk := t.PrivateKey
	if !strings.Contains(pk, "\n") {
		pk = strings.ReplaceAll(pk, `\n`, "\n")
	}
	var signer ssh.Signer
	var err error
	if t.Passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(pk), []byte(t.Passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(pk))
	}
	var missing *ssh.PassphraseMissingError
	switch {
	case errors.As(err, &missing):
		return nil, fatalf("tunnel_private_key is encrypted: set tunnel_passphrase")
	case err != nil:
		return nil, fatalf("tunnel_private_key cannot be read (%v)", err)
	}
	return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
}

func handshakeError(err error) error {
	var hk *HostKeyError
	if errors.As(err, &hk) {
		return hk
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate"):
		return fatalf("the bastion refused this user and key (%v)", err)
	case strings.Contains(msg, "no common algorithm"):
		return fatalf("the bastion and Taskiem share no SSH algorithm (%v)", err)
	}
	return fmt.Errorf("tunnel: ssh handshake with the bastion: %w: %w", err, effects.ErrNotSent)
}

// ---- host key pinning ----

// Pin is one acceptable host key: a full key, or a SHA256 fingerprint.
type Pin struct {
	key ssh.PublicKey
	fp  string
}

// ParsePins reads tunnel_host_key: one or more lines, each a known_hosts
// line, an authorized_keys line, a bare base64 key, or a "SHA256:..."
// fingerprint as ssh-keygen -l prints it.
func ParsePins(s string) ([]Pin, error) {
	var out []Pin
	for _, line := range strings.Split(strings.ReplaceAll(s, `\n`, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if fp, ok := strings.CutPrefix(line, "SHA256:"); ok {
			fp = strings.TrimRight(fp, "=")
			if raw, err := base64.RawStdEncoding.DecodeString(fp); err != nil || len(raw) != sha256.Size {
				return nil, fmt.Errorf("tunnel_host_key fingerprint %q is not a SHA256 fingerprint", line)
			}
			out = append(out, Pin{fp: "SHA256:" + fp})
			continue
		}
		if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line)); err == nil {
			out = append(out, Pin{key: k})
			continue
		}
		if marker, _, k, _, _, err := ssh.ParseKnownHosts([]byte(line)); err == nil {
			if marker != "" {
				return nil, fmt.Errorf("tunnel_host_key: @%s lines are not supported; pin the bastion's own key", marker)
			}
			out = append(out, Pin{key: k})
			continue
		}
		if raw, err := base64.StdEncoding.DecodeString(line); err == nil {
			if k, err := ssh.ParsePublicKey(raw); err == nil {
				out = append(out, Pin{key: k})
				continue
			}
		}
		return nil, fmt.Errorf("tunnel_host_key line %q is not a known_hosts line, public key or SHA256 fingerprint", truncate(line, 60))
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// HostKeyError is a bastion key that is not pinned.
type HostKeyError struct {
	Presented ssh.PublicKey
	Pinned    []string // fingerprints of the pinned keys; empty when none was set
}

func (e *HostKeyError) Error() string {
	fp := ssh.FingerprintSHA256(e.Presented)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(e.Presented)))
	if len(e.Pinned) == 0 {
		return fmt.Sprintf("tunnel: the connection has no tunnel_host_key, so the bastion was not trusted and no credentials were sent. "+
			"The bastion presented %s %s. Confirm this fingerprint with whoever runs it, then set tunnel_host_key to: %s",
			e.Presented.Type(), fp, line)
	}
	return fmt.Sprintf("tunnel: host key mismatch: the bastion presented %s %s, which is not the pinned key (%s). "+
		"No credentials were sent. If its key was changed deliberately, set tunnel_host_key to: %s; otherwise the connection may be intercepted",
		e.Presented.Type(), fp, strings.Join(e.Pinned, ", "), line)
}

func (e *HostKeyError) Unwrap() error { return effects.ErrFatal }

func hostKeyCallback(pins []Pin) ssh.HostKeyCallback {
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

// hostKeyAlgorithms asks the bastion for the pinned key types, so a server
// with several host keys presents the one that was pinned. Fingerprint pins
// do not say their type, so they leave the default order.
func hostKeyAlgorithms(pins []Pin) []string {
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

// Dialer is the dial function a connector call gets: the egress guard under
// pol, or, when the connection names a tunnel, the tunnel. Through a tunnel
// the destination must still be a host the connector may reach (pol's
// allow-list, so a connection reaches the host it names and no other), and
// only the bastion is dialled from the worker, under a policy of its own
// that allows that one public host.
func Dialer(g *egress.Guard, pol egress.Policy, creds map[string]string) (DialFunc, error) {
	t, err := FromCredentials(creds)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return g.DialContext(ctx, pol, network, addr)
		}, nil
	}
	via := egress.Policy{Tenant: pol.Tenant, Hosts: []string{t.Host}, Purpose: "tunnel:" + strings.TrimPrefix(pol.Purpose, "connector:")}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || !pol.Allows(host) {
			return nil, fmt.Errorf("%w: %s: host is not on the allow-list: %w", egress.ErrDenied, addr, effects.ErrFatal)
		}
		return t.Dial(ctx, func(ctx context.Context, network, addr string) (net.Conn, error) {
			return g.DialContext(ctx, via, network, addr)
		}, network, addr)
	}, nil
}
