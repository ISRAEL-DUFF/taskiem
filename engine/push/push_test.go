package push

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
)

func TestValidEndpoint(t *testing.T) {
	for _, ok := range []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/abc",
		"https://web.push.apple.com/QGx",
		"https://wns2-par02p.notify.windows.com/w/?token=abc",
		"https://FCM.googleapis.com:443/fcm/send/abc",
		"https://autopush.push.services.mozilla.com/x",
	} {
		if err := ValidEndpoint(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"",
		"http://fcm.googleapis.com/fcm/send/abc", // not https
		"https://evil.example/fcm.googleapis.com",                  // another host
		"https://fcm.googleapis.com.evil.example/x",                // a host that only starts like one
		"https://evilfcm.googleapis.com/x",                         // not the exact host
		"https://user:pw@fcm.googleapis.com/x",                     // user information
		"https://fcm.googleapis.com:8443/x",                        // another port
		"https://fcm.googleapis.com/x#frag",                        // a fragment
		"https://127.0.0.1/x",                                      // an address
		"https://push.apple.com/x",                                 // the suffix alone, no host before it
		"https://.push.apple.com/x",                                // an empty label
		"ftp://fcm.googleapis.com/x",                               // not a web scheme
		"//fcm.googleapis.com/x",                                   // no scheme
		"https://" + strings.Repeat("a", 3000) + ".push.apple.com", // too long
	} {
		if err := ValidEndpoint(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestConfig(t *testing.T) {
	if (Config{}).Enabled() || (Config{}).Validate() != nil {
		t.Error("no keys: off, and not an error")
	}
	if (Config{PublicKey: "a"}).Validate() == nil || (Config{PrivateKey: "a"}).Validate() == nil {
		t.Error("one key without the other is an error")
	}
	if (Config{PublicKey: "a", PrivateKey: "b"}).Validate() == nil || (Config{PublicKey: "a", PrivateKey: "b", Subject: "ada"}).Validate() == nil {
		t.Error("keys without a subject, or a subject that is no address, are an error")
	}
	priv, pub, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	c := Config{PublicKey: pub, PrivateKey: priv, Subject: "mailto:ops@taskiem.test"}
	if !c.Enabled() || c.Validate() != nil {
		t.Errorf("a pair and a subject: %v", c.Validate())
	}
	if b, err := base64.RawURLEncoding.DecodeString(pub); err != nil || len(b) != 65 || b[0] != 4 {
		t.Errorf("the public key is a 65-byte point: %d %v", len(b), err)
	}
}

func TestPayload(t *testing.T) {
	p := Payload{Title: "Dangote Stores", Body: "Musa: Rice is 45k a bag", Link: "/chat?with=abc", Tag: "abc", Unread: 3}
	var got Payload
	if err := json.Unmarshal(p.Encode(), &got); err != nil || got.Version != Version || got.Title != "Dangote Stores" || got.Unread != 3 {
		t.Fatalf("the payload: %+v %v", got, err)
	}
	// A browser with the preview off hears only that a business wrote.
	if q := p.For(Target{Preview: false}); q.Body != "New message" || q.Title != "Dangote Stores" {
		t.Errorf("preview off: %+v", q)
	}
	if q := p.For(Target{Preview: true}); q.Body != p.Body {
		t.Errorf("preview on: %+v", q)
	}
	// A long message is shortened to fit, at a letter's edge.
	long := Payload{Title: "A", Body: strings.Repeat("é", 5000), Link: "/chat", Tag: "t"}
	b := long.Encode()
	if len(b) > MaxPayload || !json.Valid(b) {
		t.Errorf("a long body: %d bytes, valid %v", len(b), json.Valid(b))
	}
	var l Payload
	_ = json.Unmarshal(b, &l)
	if !strings.HasSuffix(l.Body, "…") || strings.ContainsRune(l.Body, '�') {
		t.Errorf("cut badly: %q", l.Body[len(l.Body)-8:])
	}
}

func TestTopic(t *testing.T) {
	a, b := Topic("link-a"), Topic("link-b")
	if len(a) != 32 || a == b || a != Topic("link-a") {
		t.Errorf("topics: %q %q", a, b)
	}
	if strings.Trim(a, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		t.Errorf("not base64url: %q", a)
	}
}

// browser is a push subscription with its own keys, as a browser makes them.
func browser(t *testing.T) (p256dh, auth string) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, 16)
	_, _ = rand.Read(a)
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(a)
}

// A push service that records what it was sent and answers as told.
type service struct {
	mu     sync.Mutex
	status []int // answers in turn; the last repeats
	got    []*http.Request
	bodies [][]byte
	srv    *httptest.Server
}

func newService(t *testing.T, status ...int) *service {
	s := &service{status: status}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.got = append(s.got, r)
		s.bodies = append(s.bodies, b)
		code := s.status[min(len(s.got)-1, len(s.status)-1)]
		w.WriteHeader(code)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func sender(t *testing.T) *Sender {
	priv, pub, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	return &Sender{
		Config:   Config{PublicKey: pub, PrivateKey: priv, Subject: "mailto:ops@taskiem.test"},
		Guard:    &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
		Validate: func(string) error { return nil }, // a test service is not a push service's host
		Retry:    time.Millisecond,
	}
}

func TestSend(t *testing.T) {
	svc := newService(t, 201)
	s := sender(t)
	p256dh, auth := browser(t)
	tg := Target{ID: uuid.New(), User: uuid.New(), Endpoint: svc.srv.URL + "/send/abc", P256dh: p256dh, Auth: auth, Preview: true}
	out, status, err := s.Send(context.Background(), uuid.New(), tg, Payload{Title: "Dangote Stores", Body: "Musa: Rice is 45k a bag", Link: "/chat?with=abc", Tag: "abc"})
	if out != Sent || status != 201 || err != nil {
		t.Fatalf("send: %v %d %v", out, status, err)
	}
	r := svc.got[0]
	if r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") != "43200" || r.Header.Get("Urgency") != "high" || r.Header.Get("Topic") != Topic("abc") {
		t.Errorf("headers: %v", r.Header)
	}
	// The body is encrypted: the words are nowhere in it.
	if len(svc.bodies[0]) < 86 || strings.Contains(string(svc.bodies[0]), "Rice") || strings.Contains(string(svc.bodies[0]), "Dangote") {
		t.Errorf("the body is not an encrypted record: %d bytes", len(svc.bodies[0]))
	}
	// The VAPID header is signed by the operator's key, for the service's own origin.
	auth1 := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth1, "vapid t=") || !strings.Contains(auth1, ", k="+s.Config.PublicKey) {
		t.Fatalf("authorization: %q", auth1)
	}
	tok := strings.TrimPrefix(strings.SplitN(auth1, ", k=", 2)[0], "vapid t=")
	raw, _ := base64.RawURLEncoding.DecodeString(s.Config.PublicKey)
	px, py := elliptic.Unmarshal(elliptic.P256(), raw)
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(tok, claims, func(*jwt.Token) (any, error) { return &ecdsa.PublicKey{Curve: elliptic.P256(), X: px, Y: py}, nil }, jwt.WithValidMethods([]string{"ES256"})); err != nil {
		t.Fatalf("the VAPID token does not verify: %v", err)
	}
	if claims["sub"] != "mailto:ops@taskiem.test" || claims["aud"] != svc.srv.URL {
		t.Errorf("claims: %v", claims)
	}
}

func TestSendOutcomes(t *testing.T) {
	p256dh, auth := browser(t)
	for _, c := range []struct {
		name   string
		status []int
		want   Outcome
		calls  int
	}{
		{"a 410 is gone, at once", []int{410}, Gone, 1},
		{"a 404 is gone, at once", []int{404}, Gone, 1},
		{"a 400 fails, not retried", []int{400}, Failed, 1},
		{"a 503 then a 201 is sent after a retry", []int{503, 201}, Sent, 2},
		{"a 429 twice fails after one retry", []int{429, 429}, Failed, 2},
		{"a 500 then a 410 is gone", []int{500, 410}, Gone, 2},
	} {
		svc := newService(t, c.status...)
		out, _, _ := sender(t).Send(context.Background(), uuid.New(), Target{Endpoint: svc.srv.URL + "/x", P256dh: p256dh, Auth: auth, Preview: true}, Payload{Tag: "t"})
		if out != c.want || len(svc.got) != c.calls {
			t.Errorf("%s: outcome %v, %d calls", c.name, out, len(svc.got))
		}
	}
}

func TestSendRefusesAnAddressThatIsNotAPushService(t *testing.T) {
	p256dh, auth := browser(t)
	s := sender(t)
	s.Validate = nil // the real rule
	out, _, err := s.Send(context.Background(), uuid.New(), Target{Endpoint: "https://169.254.169.254/latest/meta-data", P256dh: p256dh, Auth: auth}, Payload{Tag: "t"})
	if out != Gone || err == nil {
		t.Errorf("an address that is not a push service's: %v %v", out, err)
	}
}

func TestSendDoesNotFollowARedirect(t *testing.T) {
	other := newService(t, 201)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.srv.URL+"/elsewhere", http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()
	p256dh, auth := browser(t)
	out, _, _ := sender(t).Send(context.Background(), uuid.New(), Target{Endpoint: redirecting.URL + "/x", P256dh: p256dh, Auth: auth, Preview: true}, Payload{Tag: "t"})
	if out != Failed || len(other.got) != 0 {
		t.Errorf("a redirect: %v, followed %d times", out, len(other.got))
	}
}
