// Package tunneltest is an in-process SSH bastion for tests: it allows
// direct-tcpip channels to one destination for one user's key, refuses
// everything else, and records what it was asked.
package tunneltest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

// Bastion is the server.
type Bastion struct {
	Ln      net.Listener
	HostKey ssh.Signer
	// Allowed is the one "host:port" the user may open.
	Allowed  string
	userKey  ssh.PublicKey
	mu       sync.Mutex
	channels []string
	dials    []string
	Authed   atomic.Int32 // keys accepted
	Sessions atomic.Int32 // sessions open now
}

// Channels are the channel types asked for, and Dials the destinations.
func (b *Bastion) Channels() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.channels...)
}
func (b *Bastion) Dials() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.dials...)
}

// NewKey makes an ed25519 key: the signer and its OpenSSH PEM.
func NewKey(t testing.TB) (ssh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, string(pem.EncodeToMemory(blk))
}

// Start runs a bastion on loopback. The returned string is the user's
// private key (user "taskiem").
func Start(t testing.TB, allowed string) (*Bastion, string) {
	t.Helper()
	host, _ := NewKey(t)
	user, userPEM := NewKey(t)
	b := &Bastion{HostKey: host, Allowed: allowed, userKey: user.PublicKey()}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if c.User() == "taskiem" && string(k.Marshal()) == string(b.userKey.Marshal()) {
			b.Authed.Add(1)
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(host)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b.Ln = ln
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(nc, cfg)
		}
	}()
	return b, userPEM
}

func (b *Bastion) serve(nc net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		_ = nc.Close()
		return
	}
	b.Sessions.Add(1)
	defer b.Sessions.Add(-1)
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		b.mu.Lock()
		b.channels = append(b.channels, nch.ChannelType())
		b.mu.Unlock()
		if nch.ChannelType() != "direct-tcpip" {
			_ = nch.Reject(ssh.UnknownChannelType, "only direct-tcpip")
			continue
		}
		var d struct {
			Host  string
			Port  uint32
			OHost string
			OPort uint32
		}
		if err := ssh.Unmarshal(nch.ExtraData(), &d); err != nil {
			_ = nch.Reject(ssh.ConnectionFailed, "bad")
			continue
		}
		dest := net.JoinHostPort(d.Host, strconv.FormatUint(uint64(d.Port), 10))
		b.mu.Lock()
		b.dials = append(b.dials, dest)
		b.mu.Unlock()
		if dest != b.Allowed {
			_ = nch.Reject(ssh.Prohibited, "permitopen")
			continue
		}
		up, err := net.Dial("tcp", dest)
		if err != nil {
			_ = nch.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			_ = up.Close()
			continue
		}
		go ssh.DiscardRequests(creqs)
		go func() { _, _ = io.Copy(up, ch); _ = up.Close() }()
		go func() { _, _ = io.Copy(ch, up); _ = ch.Close() }()
	}
	_ = sc.Close()
}

// Pin is the bastion's host key as an authorized_keys line.
func (b *Bastion) Pin() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(b.HostKey.PublicKey())))
}

// HostPort splits the bastion's address.
func (b *Bastion) HostPort() (string, string) {
	h, p, _ := net.SplitHostPort(b.Ln.Addr().String())
	return h, p
}
