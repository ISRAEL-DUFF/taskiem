package inmail

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/google/uuid"
)

// dkimDNS answers one DKIM key record and nothing else.
type dkimDNS struct {
	noDNS
	name, txt string
}

func (d dkimDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if strings.TrimSuffix(name, ".") == d.name {
		return []string{d.txt}, nil
	}
	return nil, errNoRecord
}

// A DKIM signature covers the lowest From and Message-Id; a reader takes
// the top one. So a message validly signed by example.com for
// attacker@example.com must not pass a senders list as boss@example.com
// because a second From was put on top after signing, and a copy of a
// signed message must not start a second run because its Message-Id was
// changed. Sent through the real SMTP session and the parse and
// SenderAllowed path the service uses.
func TestSecondFromHeaderIsRefused(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	dns := dkimDNS{name: "s1._domainkey.example.com", txt: "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pub)}
	sign := func(plain string, keys ...string) string {
		t.Helper()
		var signed bytes.Buffer
		// Each header signed once, as most providers do (no over-signing).
		if err := dkim.Sign(&signed, strings.NewReader(plain), &dkim.SignOptions{Domain: "example.com", Selector: "s1", Signer: crypto.Signer(key), Hash: crypto.SHA256,
			HeaderKeys: keys}); err != nil {
			t.Fatal(err)
		}
		return signed.String()
	}

	plain := "From: attacker@example.com\r\nTo: k3x9q2m7a1bz@in.taskiem.test\r\nSubject: pay the supplier\r\n" +
		"Date: Fri, 13 Nov 2026 10:00:00 +0000\r\nMessage-Id: <original-1@example.com>\r\n\r\nPlease pay 5,000,000 to account 0123456789.\r\n"
	original := sign(plain, "From", "To", "Subject", "Date", "Message-Id")
	// A signer that leaves Message-Id out of what it signs: anyone may
	// then change it and the signature still holds.
	loose := sign(plain, "From", "To", "Subject", "Date")
	other := sign(strings.Replace(plain, "5,000,000", "7,000,000", 1), "From", "To", "Subject", "Date", "Message-Id")

	ep := uuid.New()
	st := &fakeStore{endpoints: map[string]uuid.UUID{"k3x9q2m7a1bz": ep}}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{Domain: "in.taskiem.test", Store: st, Seal: plainSeal{}, Resolver: dns}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	defer func() { cancel(); <-done }()

	senders := []string{"boss@example.com"} // the trigger's allow-list
	type result struct {
		from    string
		aligned bool
		allowed bool
		own     bool // passes a list that names the real signer
		idem    string
	}
	stored := func() int {
		st.mu.Lock()
		defer st.mu.Unlock()
		return len(st.got)
	}
	// The envelope sender is another domain, so only DKIM can align.
	deliver := func(msg string) error {
		return send(l.Addr().String(), "bounce@elsewhere.test", "k3x9q2m7a1bz@in.taskiem.test", []byte(msg))
	}
	// take delivers a message that must be accepted and reads it as the
	// service does.
	take := func(name, msg string) result {
		t.Helper()
		before := stored()
		if err := deliver(msg); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if stored() != before+1 {
			t.Fatalf("%s: not stored", name)
		}
		st.mu.Lock()
		got := st.got[before]
		st.mu.Unlock()
		var meta Meta
		if err := json.Unmarshal(got.meta[bytes.IndexByte(got.meta, '{'):], &meta); err != nil {
			t.Fatal(err)
		}
		raw := got.raw[bytes.Index(got.raw, []byte("|"))+1:]
		parsed, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		auth := meta.Auth.For(parsed.From.Address)
		return result{parsed.From.Address, auth.Aligned, SenderAllowed(senders, parsed.From.Address, auth),
			SenderAllowed([]string{"attacker@example.com"}, parsed.From.Address, auth), base64.StdEncoding.EncodeToString(got.idem)}
	}
	// refuse delivers a message that must be turned away for good, with
	// nothing stored.
	refuse := func(name, msg, header string) {
		t.Helper()
		before := stored()
		err := deliver(msg)
		if code(err) != 550 {
			t.Errorf("%s: want 550, nothing stored; got %v", name, err)
		} else if !strings.Contains(err.Error(), "more than one "+header+" line") {
			t.Errorf("%s: the refusal must say why in plain words: %v", name, err)
		}
		if stored() != before {
			t.Errorf("%s: a refused message was stored", name)
		}
		if _, perr := Parse([]byte(msg)); !errors.Is(perr, ErrMalformed) {
			t.Errorf("%s: the service must not read such a message either: %v", name, perr)
		}
	}

	first := take("original", original)
	if !first.aligned || first.allowed || !first.own || first.from != "attacker@example.com" {
		t.Fatalf("the original must verify as attacker@example.com, and pass only a list that names it: %+v", first)
	}
	// Control: the signed From itself changed does not verify.
	if r := take("signed From edited", strings.Replace(original, "From: attacker@example.com", "From: boss@example.com", 1)); r.aligned || r.allowed {
		t.Fatalf("an edited From must not verify: %+v", r)
	}

	refuse("second From on top", "From: boss@example.com\r\n"+original, "From")
	refuse("second From below", strings.Replace(original, "\r\nTo: ", "\r\nFrom: boss@example.com\r\nTo: ", 1), "From")
	refuse("second From, odd spelling", "FROM : boss@example.com\r\n"+original, "From")
	refuse("second Message-Id on top", "Message-Id: <replay-2@example.com>\r\n"+original, "Message-ID")
	refuse("second Subject on top", "Subject: pay me instead\r\n"+original, "Subject")
	refuse("second Reply-To", "Reply-To: a@evil.test\r\nReply-To: b@evil.test\r\n"+original, "Reply-To")

	// The same signed message is one message, whatever is added around it
	// or changed in what the signature does not cover.
	if r := take("unsigned header added", "X-Note: again\r\n"+original); !r.aligned || r.idem != first.idem {
		t.Errorf("a copy with an unsigned header added must have the original's dedup key: %+v", r)
	}
	a := take("Message-Id not signed", loose)
	b := take("Message-Id not signed, changed", strings.Replace(loose, "<original-1@example.com>", "<replay-2@example.com>", 1))
	if !a.aligned || !b.aligned {
		t.Fatalf("setup: both must verify: %+v %+v", a, b)
	}
	if a.idem != b.idem {
		t.Errorf("a signed message with its unsigned Message-Id changed must keep its dedup key, or it can be replayed")
	}
	// And another signed message is another message.
	if r := take("another message", other); !r.aligned || r.idem == first.idem {
		t.Errorf("a different signed message must have its own dedup key: %+v", r)
	}

	// What was proven was proven of one From: a reader that saw another
	// address is not told it is proven.
	if (Auth{Aligned: true, From: "attacker@example.com"}).For("boss@example.com").Aligned {
		t.Errorf("checks made for one From must not vouch for another")
	}
}

// Without a signature the key is the Message-Id, as before.
func TestUnsignedDedupKeyIsTheMessageID(t *testing.T) {
	ep := uuid.New()
	st := &fakeStore{endpoints: map[string]uuid.UUID{"k3x9q2m7a1bz": ep}}
	addr := startServer(t, st)
	msg := func(body string) []byte {
		return []byte("From: a@example.com\r\nMessage-Id: <m1@example.com>\r\n\r\n" + body + "\r\n")
	}
	for _, body := range []string{"one", "two"} {
		if err := send(addr, "a@example.com", "k3x9q2m7a1bz@in.taskiem.test", msg(body)); err != nil {
			t.Fatal(err)
		}
	}
	if len(st.got) != 2 || !bytes.Equal(st.got[0].idem, st.got[1].idem) {
		t.Fatalf("the same Message-Id must give the same key")
	}
}
