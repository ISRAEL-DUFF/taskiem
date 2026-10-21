package inmail

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type plainSeal struct{}

func (plainSeal) SealBytes(what string, raw []byte) ([]byte, error) {
	return append([]byte(what+"|"), raw...), nil
}

type received struct {
	id, endpoint    uuid.UUID
	idem, meta, raw []byte
}

type fakeStore struct {
	mu        sync.Mutex
	endpoints map[string]uuid.UUID
	got       []received
	full      bool
}

func (f *fakeStore) Recipient(_ context.Context, id string) (uuid.UUID, bool, error) {
	ep, ok := f.endpoints[id]
	return ep, ok, nil
}

func (f *fakeStore) Receive(_ context.Context, id, ep uuid.UUID, idem, meta, raw []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.full {
		return false, nil
	}
	f.got = append(f.got, received{id, ep, idem, meta, raw})
	return true, nil
}

// noDNS answers every lookup with "no such record".
type noDNS struct{}

var errNoRecord = &net.DNSError{Err: "no such host", IsNotFound: true}

func (noDNS) LookupTXT(context.Context, string) ([]string, error)        { return nil, errNoRecord }
func (noDNS) LookupMX(context.Context, string) ([]*net.MX, error)        { return nil, errNoRecord }
func (noDNS) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) { return nil, errNoRecord }
func (noDNS) LookupAddr(context.Context, string) ([]string, error)       { return nil, errNoRecord }

func startServer(t *testing.T, st *fakeStore) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{Domain: "in.taskiem.test", Store: st, Seal: plainSeal{}, Resolver: noDNS{}}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop")
		}
	})
	return l.Addr().String()
}

// send delivers a message as another mail server would, without TLS
// (the test's certificate is self-signed).
func send(addr, from, to string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
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
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func code(err error) int {
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return 0
}

func TestServerTakesMailForKnownAddressesOnly(t *testing.T) {
	ep := uuid.New()
	st := &fakeStore{endpoints: map[string]uuid.UUID{"k3x9q2m7a1bz": ep}}
	addr := startServer(t, st)

	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if ok, _ := c.Extension("STARTTLS"); !ok {
		t.Error("STARTTLS is not offered")
	}
	if err := c.Mail("alerts@firstbank.example"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("nobodyhere12@in.taskiem.test"); code(err) != 550 {
		t.Errorf("unknown address: %v", err)
	}
	if err := c.Rcpt("someone@elsewhere.example"); code(err) != 550 {
		t.Errorf("another domain: %v", err)
	}
	if err := c.Rcpt("K3X9Q2M7A1BZ+bank@IN.taskiem.test"); err != nil {
		t.Fatalf("known address (any case, with a tag): %v", err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(gmailForwarded); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("data refused: %v", err)
	}
	_ = c.Quit()

	if len(st.got) != 1 {
		t.Fatalf("stored %d messages", len(st.got))
	}
	g := st.got[0]
	if g.endpoint != ep || len(g.idem) != 32 {
		t.Errorf("stored for %v, idem %d bytes", g.endpoint, len(g.idem))
	}
	raw := strings.TrimPrefix(string(g.raw), RawAAD(g.id)+"|")
	if !strings.Contains(raw, "Message-ID: <alert-123@firstbank.example>") {
		t.Error("the raw message was not kept")
	}
	var meta Meta
	if err := json.Unmarshal([]byte(strings.TrimPrefix(string(g.meta), MetaAAD(g.id)+"|")), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Address != "k3x9q2m7a1bz@in.taskiem.test" || meta.MailFrom != "alerts@firstbank.example" || meta.RemoteIP != "127.0.0.1" ||
		meta.Auth.Aligned || meta.Reference != g.id.String() {
		t.Errorf("meta = %+v", meta)
	}
}

func TestServerSameMessageSameIdem(t *testing.T) {
	ep := uuid.New()
	st := &fakeStore{endpoints: map[string]uuid.UUID{"k3x9q2m7a1bz": ep}}
	addr := startServer(t, st)
	for range 2 {
		if err := send(addr, "alerts@firstbank.example", "k3x9q2m7a1bz@in.taskiem.test", gmailForwarded); err != nil {
			t.Fatal(err)
		}
	}
	if len(st.got) != 2 || string(st.got[0].idem) != string(st.got[1].idem) {
		t.Errorf("a message sent twice has two identities")
	}
}

func TestServerAsksToRetryWhenFull(t *testing.T) {
	st := &fakeStore{endpoints: map[string]uuid.UUID{"k3x9q2m7a1bz": uuid.New()}, full: true}
	addr := startServer(t, st)
	err := send(addr, "a@b.example", "k3x9q2m7a1bz@in.taskiem.test", gmailForwarded)
	if code(err) != 451 {
		t.Errorf("want 451 (try later), got %v", err)
	}
}
