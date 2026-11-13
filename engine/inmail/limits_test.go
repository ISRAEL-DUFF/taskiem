package inmail

import (
	"bufio"
	"context"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// startLimited is startServer with the server's limits set by the test.
func startLimited(t *testing.T, st *fakeStore, set func(*Server)) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{Domain: "in.taskiem.test", Store: st, Seal: plainSeal{}, Resolver: noDNS{}}
	set(s)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("the server did not stop")
		}
	})
	return l.Addr().String()
}

// dialFrom connects from a loopback address of the test's choosing, as
// another host would, and reads the greeting line.
func dialFrom(t *testing.T, local, addr string) (net.Conn, string) {
	t.Helper()
	d := net.Dialer{Timeout: 3 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(local)}}
	c, err := d.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial from %s: %v", local, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, _ := bufio.NewReader(c).ReadString('\n')
	return c, line
}

// sendFrom delivers a message from a loopback address, within a few
// seconds or not at all.
func sendFrom(local, addr, from, to string, msg []byte) error {
	d := net.Dialer{Timeout: 3 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(local)}}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	c, err := smtp.NewClient(conn, "in.taskiem.test")
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

var plainMessage = []byte("From: a@sender.example\r\nTo: k3x9q2m7a1bz@in.taskiem.test\r\nSubject: hello\r\nMessage-ID: <1@sender.example>\r\n\r\nhello\r\n")

// TestOneHostCannotHoldEveryConnection (E9): one address holding idle
// connections up to the server's ceiling does not keep other senders'
// mail out, and its idle connections are ended.
func TestOneHostCannotHoldEveryConnection(t *testing.T) {
	st := &fakeStore{endpoints: map[string]uuid.UUID{"k3x9q2m7a1bz": uuid.New()}}
	addr := startLimited(t, st, func(s *Server) {
		s.MaxConnections = 6
		s.MaxConnectionsPerAddress = 3
		s.IdleTimeout = 700 * time.Millisecond
	})
	// One host opens as many connections as the server takes in all and
	// says nothing on them.
	var greeted, refused int
	var held []net.Conn
	for range 6 {
		c, line := dialFrom(t, "127.0.0.1", addr)
		switch {
		case strings.HasPrefix(line, "220"):
			greeted++
			held = append(held, c)
		case strings.HasPrefix(line, "421"):
			refused++
		}
	}
	if greeted != 3 || refused != 3 {
		t.Errorf("one address: %d connections taken, %d refused; want 3 and 3", greeted, refused)
	}
	// Another sender's mail still arrives.
	if err := sendFrom("127.0.0.2", addr, "a@sender.example", "k3x9q2m7a1bz@in.taskiem.test", plainMessage); err != nil {
		t.Fatalf("another sender's mail while one host holds its connections: %v", err)
	}
	if len(st.got) != 1 {
		t.Fatalf("stored: %d", len(st.got))
	}
	// The idle connections are ended well before the two-minute timeout
	// of a conversation under way.
	for _, c := range held {
		_ = c.SetReadDeadline(time.Now().Add(4 * time.Second))
		buf := make([]byte, 256)
		for {
			if _, err := c.Read(buf); err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					t.Fatal("an idle connection was still open after 4 seconds")
				}
				break
			}
		}
	}
}

// TestMailLimitsPerAddressAndAtOnce (E9): an address is limited in how
// many messages it starts, and only so many messages are in transfer at
// once; the rest are told to try again.
func TestMailLimitsPerAddressAndAtOnce(t *testing.T) {
	st := &fakeStore{endpoints: map[string]uuid.UUID{"k3x9q2m7a1bz": uuid.New()}}
	addr := startLimited(t, st, func(s *Server) {
		s.MessagesPerMinute = 2
		s.MaxTransfers = 1
	})
	const to = "k3x9q2m7a1bz@in.taskiem.test"
	// A message in transfer, held open.
	d := net.Dialer{Timeout: 3 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.3")}}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	slow, err := smtp.NewClient(conn, "in.taskiem.test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slow.Close() }()
	if err := slow.Mail("a@sender.example"); err != nil {
		t.Fatal(err)
	}
	if err := slow.Rcpt(to); err != nil {
		t.Fatal(err)
	}
	w, err := slow.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plainMessage[:40]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	// A second transfer meanwhile is told to try again, and nothing of it
	// is kept.
	if err := sendFrom("127.0.0.4", addr, "b@sender.example", to, plainMessage); code(err) != 451 {
		t.Fatalf("a second transfer at once: %v", err)
	}
	if _, err := w.Write(plainMessage[40:]); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("the first transfer: %v", err)
	}
	if len(st.got) != 1 {
		t.Fatalf("stored: %d", len(st.got))
	}
	// One address starts two messages a minute here; the third waits.
	other := []byte(strings.Replace(string(plainMessage), "<1@", "<2@", 1))
	if err := sendFrom("127.0.0.5", addr, "c@sender.example", to, other); err != nil {
		t.Fatalf("first: %v", err)
	}
	other = []byte(strings.Replace(string(plainMessage), "<1@", "<3@", 1))
	if err := sendFrom("127.0.0.5", addr, "c@sender.example", to, other); err != nil {
		t.Fatalf("second: %v", err)
	}
	if err := sendFrom("127.0.0.5", addr, "c@sender.example", to, other); code(err) != 451 {
		t.Fatalf("a third message in the minute: %v", err)
	}
}
