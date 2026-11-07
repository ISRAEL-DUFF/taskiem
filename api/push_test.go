package api_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
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

	"golang.org/x/crypto/hkdf"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/push"
)

// A browser: a subscription's keys, and the private half to read what is pushed.
type pushBrowser struct {
	priv       *ecdh.PrivateKey
	auth       []byte
	p256dh, au string
}

func newPushBrowser(t *testing.T) *pushBrowser {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, 16)
	_, _ = rand.Read(a)
	return &pushBrowser{priv: k, auth: a, p256dh: base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), au: base64.RawURLEncoding.EncodeToString(a)}
}

// open reads a push as a browser does (RFC 8291, aes128gcm, one record); it
// fails for a browser the push was not made for.
func (b *pushBrowser) open(body []byte) ([]byte, error) {
	if len(body) < 86 {
		return nil, io.ErrUnexpectedEOF
	}
	salt, idlen := body[:16], int(body[20])
	sender, ct := body[21:21+idlen], body[21+idlen:]
	pub, err := ecdh.P256().NewPublicKey(sender)
	if err != nil {
		return nil, err
	}
	secret, err := b.priv.ECDH(pub)
	if err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), b.priv.PublicKey().Bytes()...), sender...)
	ikm := make([]byte, 32)
	_, _ = io.ReadFull(hkdf.New(sha256.New, secret, b.auth, info), ikm)
	key, nonce := make([]byte, 16), make([]byte, 12)
	_, _ = io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: aes128gcm\x00")), key)
	_, _ = io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: nonce\x00")), nonce)
	blk, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(blk)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	end := len(plain)
	for end > 0 && plain[end-1] == 0 {
		end--
	}
	return plain[:end-1], nil // the last byte is the record's delimiter (0x02)
}

func (b *pushBrowser) decrypt(t *testing.T, body []byte) push.Payload {
	t.Helper()
	plain, err := b.open(body)
	if err != nil {
		t.Fatalf("not for this browser: %v", err)
	}
	var p push.Payload
	if err := json.Unmarshal(plain, &p); err != nil {
		t.Fatalf("the payload: %v (%q)", err, plain)
	}
	return p
}

// A push service: records each push by path, and answers as told.
type pushService struct {
	mu     sync.Mutex
	status int
	got    map[string][]*http.Request
	bodies map[string][][]byte
	srv    *httptest.Server
}

func newPushService(t *testing.T) *pushService {
	s := &pushService{status: 201, got: map[string][]*http.Request{}, bodies: map[string][][]byte{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.got[r.URL.Path] = append(s.got[r.URL.Path], r)
		s.bodies[r.URL.Path] = append(s.bodies[r.URL.Path], b)
		w.WriteHeader(s.status)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *pushService) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got[path])
}

func (s *pushService) answer(code int) {
	s.mu.Lock()
	s.status = code
	s.mu.Unlock()
}

func pushSender(t *testing.T) *push.Sender {
	priv, pub, err := push.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	return &push.Sender{
		Config: push.Config{PublicKey: pub, PrivateKey: priv, Subject: "mailto:ops@taskiem.test"},
		Guard:  &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
		Retry:  time.Millisecond,
	}
}

// Chat push (docs/spec/chat-push.md): a person allows a browser; a message
// from a linked business is pushed to the browsers of the members who write in
// its conversations, encrypted for each, unless the conversation is muted.
func TestChatPush(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 1000
	seller, buyer, sellerID, _ := linkedPair(t, w)
	_ = sellerID
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	viewer := addMember(t, w.world, buyer, "vic", "viewer")
	svc := newPushService(t)
	br := newPushBrowser(t)
	sub := func(path string, b *pushBrowser, extra map[string]any) map[string]any {
		body := map[string]any{"endpoint": svc.srv.URL + path, "keys": map[string]any{"p256dh": b.p256dh, "auth": b.au}, "label": "Chrome on Android"}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}

	// No keys: the feature is off, and says nothing.
	for _, c := range []struct{ method, path string }{{"GET", "/v1/me/push/key"}, {"GET", "/v1/me/push"}} {
		if s, _ := buyer.do(c.method, c.path, nil); s != 404 {
			t.Errorf("%s %s with no keys: %d", c.method, c.path, s)
		}
	}
	if s, _ := buyer.do("PUT", "/v1/me/push", sub("/owner", br, nil)); s != 404 {
		t.Errorf("PUT with no keys: %d", s)
	}

	w.srv.Push = pushSender(t)
	if k := buyer.must(200, "GET", "/v1/me/push/key", nil); k["public_key"] != w.srv.Push.Config.PublicKey {
		t.Errorf("the key: %v", k)
	}

	// An address that is not a push service's is refused (the real rule).
	for _, bad := range []string{"http://fcm.googleapis.com/x", "https://evil.example/fcm.googleapis.com", "https://169.254.169.254/x", svc.srv.URL + "/owner"} {
		body := sub("/x", br, map[string]any{"endpoint": bad})
		if s, _ := buyer.do("PUT", "/v1/me/push", body); s != 400 {
			t.Errorf("%s was accepted: %d", bad, s)
		}
	}
	// (The service of the test is not one of the real hosts: its own rule from here.)
	w.srv.Push.Validate = func(string) error { return nil }

	// A browser is allowed; the same one again is one, with the new label.
	out := buyer.must(200, "PUT", "/v1/me/push", sub("/owner", br, nil))
	if d := out["devices"].([]any); len(d) != 1 || d[0].(map[string]any)["label"] != "Chrome on Android" || d[0].(map[string]any)["preview"] != true || out["max"] != float64(10) {
		t.Fatalf("one device: %v", out)
	}
	if strings.Contains(string(jsonOf(t, out)), "endpoint") || strings.Contains(string(jsonOf(t, out)), br.p256dh) {
		t.Errorf("an address or key came back: %v", out)
	}
	out = buyer.must(200, "PUT", "/v1/me/push", sub("/owner", br, map[string]any{"label": "Chrome, my phone"}))
	if d := out["devices"].([]any); len(d) != 1 || d[0].(map[string]any)["label"] != "Chrome, my phone" {
		t.Fatalf("the same browser again: %v", out)
	}
	id := out["devices"].([]any)[0].(map[string]any)["id"].(string)
	if out["added"] != id {
		t.Errorf("the browser is told which it is: %v vs %v", out["added"], id)
	}

	// Allowing the same browser again keeps what was chosen for it (the preview).
	buyer.must(200, "PATCH", "/v1/me/push/"+id, map[string]any{"preview": false})
	out = buyer.must(200, "PUT", "/v1/me/push", sub("/owner", br, nil))
	if d := out["devices"].([]any); len(d) != 1 || d[0].(map[string]any)["preview"] != false {
		t.Fatalf("the preview was reset by allowing the same browser again: %v", out)
	}
	buyer.must(200, "PATCH", "/v1/me/push/"+id, map[string]any{"preview": true})

	// A viewer (who cannot write in conversations) allows one too; it is never sent to.
	viewerBr := newPushBrowser(t)
	viewer.must(200, "PUT", "/v1/me/push", sub("/viewer", viewerBr, nil))
	// Each person sees and changes only their own.
	if d := viewer.must(200, "GET", "/v1/me/push", nil)["devices"].([]any); len(d) != 1 || d[0].(map[string]any)["id"] == id {
		t.Errorf("the viewer's own: %v", d)
	}
	if s, _ := viewer.do("DELETE", "/v1/me/push/"+id, nil); s != 404 {
		t.Errorf("the viewer took the owner's browser off: %d", s)
	}
	if s, _ := viewer.do("PATCH", "/v1/me/push/"+id, map[string]any{"preview": false}); s != 404 {
		t.Errorf("the viewer changed the owner's browser: %d", s)
	}
	// Another business sees none of it.
	if d := seller.must(200, "GET", "/v1/me/push", nil)["devices"].([]any); len(d) != 0 {
		t.Errorf("the other business: %v", d)
	}

	// A message from the seller is pushed to the owner's browser, readable only by it.
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Rice is 45k a bag"})
	w.srv.WaitBackground()
	if svc.count("/owner") != 1 || svc.count("/viewer") != 0 {
		t.Fatalf("pushes: owner %d, viewer %d", svc.count("/owner"), svc.count("/viewer"))
	}
	r := svc.got["/owner"][0]
	if r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") != "43200" || r.Header.Get("Topic") != push.Topic(link) || !strings.HasPrefix(r.Header.Get("Authorization"), "vapid t=") {
		t.Errorf("headers: %v", r.Header)
	}
	if strings.Contains(string(svc.bodies["/owner"][0]), "Rice") {
		t.Error("the words travel in the clear")
	}
	p := br.decrypt(t, svc.bodies["/owner"][0])
	if p.Title != "Dangote Stores" || !strings.Contains(p.Body, "Rice is 45k a bag") || p.Link != "/chat?with="+link || p.Tag != link || p.Unread != 1 || p.Version != push.Version {
		t.Errorf("the payload: %+v", p)
	}
	// Another browser's keys cannot read it.
	if _, err := newPushBrowser(t).open(svc.bodies["/owner"][0]); err == nil {
		t.Error("another browser read a push that was not for it")
	}

	// A browser with the preview off is told only that a business wrote.
	buyer.must(200, "PATCH", "/v1/me/push/"+id, map[string]any{"preview": false})
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Second message"})
	w.srv.WaitBackground()
	if svc.count("/owner") != 2 {
		t.Fatalf("pushes: %d", svc.count("/owner"))
	}
	if p := br.decrypt(t, svc.bodies["/owner"][1]); p.Body != "New message" || p.Title != "Dangote Stores" || strings.Contains(p.Body, "Second") {
		t.Errorf("preview off: %+v", p)
	}

	// A muted conversation is not pushed.
	buyer.must(200, "PUT", "/v1/link-chats/"+link, map[string]any{"muted": true})
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Third"})
	w.srv.WaitBackground()
	if svc.count("/owner") != 2 {
		t.Errorf("a muted conversation was pushed: %d", svc.count("/owner"))
	}
	buyer.must(200, "PUT", "/v1/link-chats/"+link, map[string]any{"muted": false})

	// The buyer's own message is not pushed to the buyer: the other side is told.
	buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Thanks"})
	w.srv.WaitBackground()
	if svc.count("/owner") != 2 {
		t.Errorf("a message pushed to its own sender: %d", svc.count("/owner"))
	}

	// A failure is recorded and tried again later; the browser is kept.
	svc.answer(500)
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Fourth"})
	w.srv.WaitBackground()
	var failed bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT failed_since IS NOT NULL FROM push_subscriptions WHERE id = $1`, id).Scan(&failed); err != nil || !failed {
		t.Errorf("a failure was not recorded: %v %v", failed, err)
	}
	// A browser that is gone (410) is taken off.
	svc.answer(410)
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Fifth"})
	w.srv.WaitBackground()
	if d := buyer.must(200, "GET", "/v1/me/push", nil)["devices"].([]any); len(d) != 0 {
		t.Errorf("a gone browser stayed: %v", d)
	}

	// A person who loses their last role in the business loses their browsers there.
	var viewerID string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id::text FROM users WHERE email = 'vic@acme.test'`).Scan(&viewerID); err != nil {
		t.Fatal(err)
	}
	buyer.must(204, "DELETE", "/v1/members/"+viewerID+"/roles/viewer", nil)
	var left int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions WHERE endpoint LIKE '%/viewer'`).Scan(&left); err != nil || left != 0 {
		t.Errorf("a person with no role left kept %d browsers: %v", left, err)
	}

	// Ten at most: an eleventh replaces the oldest.
	svc.answer(201)
	for i := range 11 {
		buyer.must(200, "PUT", "/v1/me/push", sub("/b"+string(rune('a'+i)), newPushBrowser(t), nil))
	}
	d := buyer.must(200, "GET", "/v1/me/push", nil)["devices"].([]any)
	if len(d) != 10 {
		t.Errorf("devices: %d, want 10", len(d))
	}
	// A browser takes itself off by its address.
	buyer.must(200, "DELETE", "/v1/me/push", map[string]any{"endpoint": svc.srv.URL + "/bb"})
	if d := buyer.must(200, "GET", "/v1/me/push", nil)["devices"].([]any); len(d) != 9 {
		t.Errorf("after a browser took itself off: %d", len(d))
	}
	// One that was not there is not an error.
	buyer.must(200, "DELETE", "/v1/me/push", map[string]any{"endpoint": svc.srv.URL + "/never"})
	// A device on its own can be taken off by id.
	one := buyer.must(200, "GET", "/v1/me/push", nil)["devices"].([]any)[0].(map[string]any)["id"].(string)
	if out := buyer.must(200, "DELETE", "/v1/me/push/"+one, nil); len(out["devices"].([]any)) != 8 {
		t.Errorf("after one was taken off: %v", out)
	}
}

func jsonOf(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
