package tunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/tunnel/tunneltest"
)

func startBastion(t *testing.T, allowed string) (*tunneltest.Bastion, string) {
	return tunneltest.Start(t, allowed)
}

func newKey(t *testing.T) (ssh.Signer, string) { return tunneltest.NewKey(t) }

func cfgOf(b *tunneltest.Bastion, userPEM string) *Config {
	host, port := b.HostPort()
	return &Config{Host: host, Port: port, User: "taskiem", PrivateKey: userPEM, HostKey: b.Pin()}
}

func direct(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// private is a "private network" service: a loopback HTTP server, which the
// worker itself would never be allowed to dial.
func private(t *testing.T) (string, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "hello from inside") }))
	t.Cleanup(s.Close)
	return s.Listener.Addr().String(), s
}

func TestTunnelCarriesOneConnectionToTheDestination(t *testing.T) {
	dest, _ := private(t)
	b, key := startBastion(t, dest)
	conn, err := cfgOf(b, key).Dial(context.Background(), direct, "tcp", dest)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil }}}
	resp, err := cl.Get("http://" + dest + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from inside" {
		t.Fatalf("body %q", body)
	}
	if ch := b.Channels(); len(ch) != 1 || ch[0] != "direct-tcpip" {
		t.Fatalf("channels asked: %v, want exactly one direct-tcpip", ch)
	}
	if d := b.Dials(); len(d) != 1 || d[0] != dest {
		t.Fatalf("destinations asked: %v", d)
	}
}

func TestWrongHostKeyFailsBeforeAnyCredentialIsSent(t *testing.T) {
	dest, _ := private(t)
	b, key := startBastion(t, dest)
	c := cfgOf(b, key)
	other, _ := newKey(t)
	c.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(other.PublicKey())))
	_, err := c.Dial(context.Background(), direct, "tcp", dest)
	var hk *HostKeyError
	if !errors.As(err, &hk) || !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("want a fatal host key error, got %v", err)
	}
	if n := b.Authed.Load(); n != 0 {
		t.Fatalf("the private key was offered %d time(s) to a bastion with the wrong key", n)
	}
	// No pin at all is the same, and the error names the key to pin.
	c.HostKey = ""
	_, err = c.Dial(context.Background(), direct, "tcp", dest)
	if !errors.As(err, &hk) || !strings.Contains(err.Error(), "set tunnel_host_key to: ssh-ed25519") {
		t.Fatalf("want the key to pin in the error, got %v", err)
	}
	if b.Authed.Load() != 0 {
		t.Fatal("credentials sent without a pinned key")
	}
}

func TestFingerprintPinWorks(t *testing.T) {
	dest, _ := private(t)
	b, key := startBastion(t, dest)
	c := cfgOf(b, key)
	c.HostKey = ssh.FingerprintSHA256(b.HostKey.PublicKey())
	conn, err := c.Dial(context.Background(), direct, "tcp", dest)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func TestOtherDestinationsAreRefusedByTheBastionAndNothingElseIsOpened(t *testing.T) {
	dest, _ := private(t)
	b, key := startBastion(t, dest)
	_, err := cfgOf(b, key).Dial(context.Background(), direct, "tcp", "10.9.9.9:80")
	if err == nil || !errors.Is(err, effects.ErrNotSent) {
		t.Fatalf("want a not-sent refusal, got %v", err)
	}
	for _, ch := range b.Channels() {
		if ch != "direct-tcpip" {
			t.Fatalf("a %q channel was requested", ch)
		}
	}
}

func TestTunnelNeverOutlivesItsCall(t *testing.T) {
	dest, _ := private(t)
	b, key := startBastion(t, dest)
	conn, err := cfgOf(b, key).Dial(context.Background(), direct, "tcp", dest)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return b.Sessions.Load() == 1 })
	_ = conn.Close()
	waitFor(t, func() bool { return b.Sessions.Load() == 0 })

	// A cancelled call closes the session too, even if the connection is
	// never closed.
	ctx, cancel := context.WithCancel(context.Background())
	_, err = cfgOf(b, key).Dial(ctx, direct, "tcp", dest)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return b.Sessions.Load() == 1 })
	cancel()
	waitFor(t, func() bool { return b.Sessions.Load() == 0 })
}

func TestTheBastionIsDialledOnlyThroughTheGivenDialer(t *testing.T) {
	dest, _ := private(t)
	b, key := startBastion(t, dest)
	var asked []string
	deny := func(ctx context.Context, network, addr string) (net.Conn, error) {
		asked = append(asked, addr)
		return nil, errors.Join(errors.New("egress: denied"), effects.ErrFatal)
	}
	_, err := cfgOf(b, key).Dial(context.Background(), deny, "tcp", dest)
	if err == nil || !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("want the guard's refusal, got %v", err)
	}
	if len(asked) != 1 || asked[0] != b.Ln.Addr().String() {
		t.Fatalf("dialled %v: only the bastion may be dialled", asked)
	}
	if len(b.Dials()) != 0 {
		t.Fatal("the destination was asked for after the bastion was refused")
	}
}

func TestCredentialsMustBeWhole(t *testing.T) {
	if c, err := FromCredentials(map[string]string{}); c != nil || err != nil {
		t.Fatalf("no tunnel_host means no tunnel: %v %v", c, err)
	}
	for name, in := range map[string]map[string]string{
		"no user":  {"tunnel_host": "b.example", "tunnel_private_key": "x"},
		"no key":   {"tunnel_host": "b.example", "tunnel_user": "u"},
		"bad port": {"tunnel_host": "b.example", "tunnel_user": "u", "tunnel_private_key": "x", "tunnel_port": "ssh"},
	} {
		if _, err := FromCredentials(in); err == nil || !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%s: want a fatal error, got %v", name, err)
		}
	}
	c, err := FromCredentials(map[string]string{"tunnel_host": "b.example", "tunnel_user": "u", "tunnel_private_key": "x"})
	if err != nil || c.Port != "22" {
		t.Fatalf("default port: %v %v", c, err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func credsOf(b *tunneltest.Bastion, userPEM, target string) map[string]string {
	host, port := b.HostPort()
	h, _, _ := net.SplitHostPort(target)
	return map[string]string{
		"host": h, "tunnel_host": host, "tunnel_port": port, "tunnel_user": "taskiem",
		"tunnel_private_key": userPEM, "tunnel_host_key": b.Pin(),
	}
}

func TestDialerRefusesAPrivateOrUnlistedBastionBeforeAnyDial(t *testing.T) {
	dest, _ := private(t)
	b, key := startBastion(t, dest)
	creds := credsOf(b, key, dest)
	h, _, _ := net.SplitHostPort(dest)
	// A guard that does not loosen loopback: the bastion on 127.0.0.1 is a
	// private address, however it was named.
	g := &egress.Guard{}
	d, err := Dialer(g, egress.Policy{Tenant: "t", Hosts: []string{h}, Purpose: "connector:privatehttp"}, creds)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d(context.Background(), "tcp", dest); !errors.Is(err, egress.ErrDenied) {
		t.Fatalf("want the guard to refuse a private bastion, got %v", err)
	}
	if b.Authed.Load() != 0 || len(b.Channels()) != 0 {
		t.Fatal("the bastion was reached")
	}
}

func TestDialerKeepsToTheConnectionsOwnHost(t *testing.T) {
	dest, _ := private(t)
	b, key := startBastion(t, dest)
	_, port := b.HostPort()
	g := &egress.Guard{Loopback: map[string]string{"tunnel:privatehttp": port}}
	creds := credsOf(b, key, dest)
	// The connector's policy names another host: the destination is not on it.
	d, err := Dialer(g, egress.Policy{Tenant: "t", Hosts: []string{"somewhere.else"}, Purpose: "connector:privatehttp"}, creds)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d(context.Background(), "tcp", dest); !errors.Is(err, egress.ErrDenied) {
		t.Fatalf("want a refusal for a host the connector may not reach, got %v", err)
	}
	if len(b.Dials()) != 0 {
		t.Fatal("the bastion was asked for a destination the policy does not allow")
	}
	// The connection's own host goes through.
	h, _, _ := net.SplitHostPort(dest)
	d, _ = Dialer(g, egress.Policy{Tenant: "t", Hosts: []string{h}, Purpose: "connector:privatehttp"}, creds)
	c, err := d(context.Background(), "tcp", dest)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestDialerWithoutATunnelIsTheGuard(t *testing.T) {
	dest, _ := private(t)
	h, _, _ := net.SplitHostPort(dest)
	d, err := Dialer(&egress.Guard{}, egress.Policy{Hosts: []string{h}, Purpose: "connector:x"}, map[string]string{"host": h})
	if err != nil {
		t.Fatal(err)
	}
	// 127.0.0.1 is private: with no tunnel it is refused, as it always was.
	if _, err := d(context.Background(), "tcp", dest); !errors.Is(err, egress.ErrDenied) {
		t.Fatalf("want a refusal, got %v", err)
	}
}
