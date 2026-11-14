package push

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
)

// A fake Google: the token endpoint checks the service account's signature;
// FCM records each message and answers as told for each token.
type fakeFCM struct {
	t      *testing.T
	key    *rsa.PublicKey
	srv    *httptest.Server
	mu     sync.Mutex
	tokens int
	sent   []map[string]any
	auth   []string
	// answers, by device token: the statuses to answer in turn, and a body.
	answers map[string][]int
	bodies  map[string]string
}

func newFakeFCM(t *testing.T, key *rsa.PublicKey) *fakeFCM {
	f := &fakeFCM{t: t, key: key, answers: map[string][]int{}, bodies: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || !f.signed(r.Form.Get("assertion")) {
				http.Error(w, "bad assertion", 400)
				return
			}
			f.tokens++
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + string(rune('0'+f.tokens)), "expires_in": 3600})
		case "/v1/projects/taskiem-test/messages:send":
			f.auth = append(f.auth, r.Header.Get("Authorization"))
			var m map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &m)
			f.sent = append(f.sent, m)
			token := m["message"].(map[string]any)["token"].(string)
			status := 200
			if q := f.answers[token]; len(q) > 0 {
				status, f.answers[token] = q[0], q[1:]
			}
			w.WriteHeader(status)
			if status != 200 {
				_, _ = w.Write([]byte(f.bodies[token]))
				return
			}
			_, _ = w.Write([]byte(`{"name":"projects/taskiem-test/messages/1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// signed checks an RS256 assertion and its claims.
func (f *fakeFCM) signed(a string) bool {
	parts := strings.Split(a, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(f.key, crypto.SHA256, sum[:], sig) != nil {
		return false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	_ = json.Unmarshal(raw, &c)
	return c["iss"] == "push@taskiem-test.iam.gserviceaccount.com" && c["scope"] == fcmScope && strings.HasSuffix(c["aud"].(string), "/token")
}

func testFCM(t *testing.T) (*Sender, *fakeFCM) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeFCM(t, &key.PublicKey)
	return &Sender{
		FCM:   NewFCMForTest("taskiem-test", "push@taskiem-test.iam.gserviceaccount.com", key, f.srv.URL+"/token", f.srv.URL),
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
		Retry: time.Millisecond,
	}, f
}

func phone(token string, preview bool) Target {
	return Target{ID: uuid.New(), Kind: App, Endpoint: token, Preview: preview, Platform: "android", Kinds: Kinds}
}

func TestFCMSend(t *testing.T) {
	ctx := context.Background()
	s, f := testFCM(t)
	tenant := uuid.New()
	p := Payload{Title: "Dangote Stores", Body: "Musa: rice is 45k", Link: "/chat?with=L1", Tag: "L1", Unread: 3, Business: tenant.String(), Kind: KindBusiness}
	for range 2 {
		if out, status, err := s.Send(ctx, tenant, phone("tok-aaaaaaaaaaaaaaaaaaaa", true), p); out != Sent || status != 200 || err != nil {
			t.Fatalf("sent: %v %d %v", out, status, err)
		}
	}
	if f.tokens != 1 {
		t.Errorf("the access token is kept until it expires: asked %d times", f.tokens)
	}
	if f.auth[0] != "Bearer at-1" {
		t.Errorf("authorised with: %q", f.auth[0])
	}
	m := f.sent[0]["message"].(map[string]any)
	n := m["notification"].(map[string]any)
	if m["token"] != "tok-aaaaaaaaaaaaaaaaaaaa" || n["title"] != "Dangote Stores" || n["body"] != "Musa: rice is 45k" {
		t.Errorf("the message: %v", m)
	}
	d := m["data"].(map[string]any)
	if d["link"] != "/chat?with=L1" || d["tag"] != "L1" || d["business"] != tenant.String() || d["kind"] != KindBusiness || d["unread"] != "3" {
		t.Errorf("the data: %v", d)
	}
	a := m["android"].(map[string]any)
	an := a["notification"].(map[string]any)
	if a["priority"] != "HIGH" || a["ttl"] != "43200s" || a["collapse_key"] != Topic("L1") || an["tag"] != "L1" || an["channel_id"] != KindBusiness || an["notification_count"] != float64(3) {
		t.Errorf("android: %v", a)
	}
	apns := m["apns"].(map[string]any)
	if apns["headers"].(map[string]any)["apns-collapse-id"] != Topic("L1") || apns["payload"].(map[string]any)["aps"].(map[string]any)["badge"] != float64(3) {
		t.Errorf("apns: %v", apns)
	}

	// The preview off: a chat says only that someone wrote; anything else
	// not even who.
	f.sent = nil
	_, _, _ = s.Send(ctx, tenant, phone("tok-bbbbbbbbbbbbbbbbbbbb", false), p)
	_, _, _ = s.Send(ctx, tenant, phone("tok-bbbbbbbbbbbbbbbbbbbb", false), Payload{Title: "Bill from Dangote: ₦450,000", Body: "Due Friday", Link: "/bills", Tag: "bill:1", Kind: KindOrders})
	_, _, _ = s.Send(ctx, tenant, phone("tok-bbbbbbbbbbbbbbbbbbbb", false), Payload{Title: "Ade Okafor wrote", Body: "Is the rice in?", Link: "/chat?with=c-1", Tag: "c-1", Kind: KindCustomers})
	_, _, _ = s.Send(ctx, tenant, phone("tok-bbbbbbbbbbbbbbbbbbbb", false), Payload{Title: "Approval waiting: Pay Bayo ₦450,000", Body: "Payroll", Link: "/approvals", Tag: "approval:1", Kind: KindApprovals})
	_, _, _ = s.Send(ctx, tenant, phone("tok-bbbbbbbbbbbbbbbbbbbb", false), Payload{Title: "A run failed: Payroll", Body: "Step 3", Link: "/runs/1", Tag: "alert:1", Kind: KindAlerts})
	words := func(i int) (string, string) {
		n := f.sent[i]["message"].(map[string]any)["notification"].(map[string]any)
		return n["title"].(string), n["body"].(string)
	}
	if ti, b := words(0); ti != "Dangote Stores" || b != "New message" {
		t.Errorf("a chat, preview off: %q %q", ti, b)
	}
	if ti, b := words(1); ti != "Taskiem" || b != "Something needs you" {
		t.Errorf("a bill, preview off: %q %q", ti, b)
	}
	if ti, b := words(2); ti != "Taskiem" || b != "A customer wrote" {
		t.Errorf("a customer, preview off: %q %q", ti, b)
	}
	for i, what := range []string{"an approval", "an alert"} {
		if ti, b := words(3 + i); ti != "Taskiem" || b != "Something needs you" {
			t.Errorf("%s, preview off: %q %q", what, ti, b)
		}
	}
}

func TestFCMOutcomes(t *testing.T) {
	ctx := context.Background()
	s, f := testFCM(t)
	tenant := uuid.New()
	p := Payload{Title: "T", Body: "B", Link: "/chat", Tag: "x", Kind: KindBusiness}
	unregistered := `{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
	f.answers["tok-gone-aaaaaaaaaaaaaaa"], f.bodies["tok-gone-aaaaaaaaaaaaaaa"] = []int{404}, unregistered
	f.answers["tok-bad-aaaaaaaaaaaaaaaa"], f.bodies["tok-bad-aaaaaaaaaaaaaaaa"] = []int{400}, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"The registration token is not a valid FCM registration token"}}`
	f.answers["tok-busy-aaaaaaaaaaaaaaa"] = []int{503, 429}
	f.answers["tok-400x-aaaaaaaaaaaaaaa"], f.bodies["tok-400x-aaaaaaaaaaaaaaa"] = []int{400}, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"Invalid value at 'message.android.ttl'"}}`
	f.answers["tok-down-aaaaaaaaaaaaaaa"] = []int{503, 503, 503}
	f.answers["tok-404x-aaaaaaaaaaaaaaa"], f.bodies["tok-404x-aaaaaaaaaaaaaaa"] = []int{404}, `{"error":{"code":404,"status":"NOT_FOUND","message":"Requested entity was not found."}}`
	f.answers["tok-stale-aaaaaaaaaaaaaa"] = []int{401}
	f.answers["tok-nope-aaaaaaaaaaaaaaa"], f.bodies["tok-nope-aaaaaaaaaaaaaaa"] = []int{403, 403, 403}, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"SenderId mismatch"}}`
	for token, want := range map[string]Outcome{
		"tok-gone-aaaaaaaaaaaaaaa":  Gone,   // the app was removed
		"tok-bad-aaaaaaaaaaaaaaaa":  Gone,   // not a token at all
		"tok-busy-aaaaaaaaaaaaaaa":  Sent,   // a 503 and a 429, then taken
		"tok-stale-aaaaaaaaaaaaaa":  Sent,   // a fresh access token after a 401
		"tok-nope-aaaaaaaaaaaaaaa":  Failed, // the project is wrong: kept, not dropped
		"tok-404x-aaaaaaaaaaaaaaa":  Failed, // a 404 that is not UNREGISTERED: kept
		"tok-400x-aaaaaaaaaaaaaaa":  Failed, // a 400 about the message, not the token: kept
		"tok-down-aaaaaaaaaaaaaaa":  Later,  // FCM down all three tries: later, not the phone's fault
		"tok-ok-aaaaaaaaaaaaaaaaaa": Sent,
	} {
		if out, _, _ := s.Send(ctx, tenant, phone(token, true), p); out != want {
			t.Errorf("%s: %v, want %v", token, out, want)
		}
	}
	if f.tokens != 2 {
		t.Errorf("a 401 asks for a new access token once: %d", f.tokens)
	}
	// Firebase not set up: a phone is not sent to, and is not dropped.
	if out, _, err := (&Sender{Guard: s.Guard}).Send(ctx, tenant, phone("tok-ok-aaaaaaaaaaaaaaaaaa", true), p); out != Off || err == nil {
		t.Errorf("no Firebase: %v %v", out, err)
	}
}

func TestParseFCM(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	creds, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "taskiem-test", "client_email": "push@taskiem-test.iam.gserviceaccount.com", "private_key": pemKey})
	client := `{"api_key":"AIza","app_id_android":"1:2:android:3","sender_id":"2"}`
	if f, err := ParseFCM("", ""); f != nil || err != nil {
		t.Errorf("neither is off: %v %v", f, err)
	}
	f, err := ParseFCM(string(creds), client)
	if err != nil || f.Client.ProjectID != "taskiem-test" || f.BaseURL != "https://fcm.googleapis.com" {
		t.Fatalf("parsed: %+v %v", f, err)
	}
	for name, c := range map[string][2]string{
		"one without the other": {string(creds), ""},
		"not a service account": {`{"type":"user"}`, client},
		"no client values":      {string(creds), `{"api_key":"AIza"}`},
		"another project":       {string(creds), `{"api_key":"AIza","app_id_android":"1:2:android:3","sender_id":"2","project_id":"other"}`},
	} {
		if _, err := ParseFCM(c[0], c[1]); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	bad, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "taskiem-test", "client_email": "x@y", "private_key": pemKey, "token_uri": "https://evil.example/token"})
	if _, err := ParseFCM(string(bad), client); err == nil {
		t.Error("a token_uri that is not Google's was taken")
	}
}

func TestValidTokenAndKinds(t *testing.T) {
	for _, bad := range []string{"", "short", strings.Repeat("a", 5000), "abc def ghi jkl mno pqr", "https://evil.example/aaaaaaaaaaaaaa"} {
		if ValidToken(bad) == nil {
			t.Errorf("%q was taken as a token", bad)
		}
	}
	if ValidToken("fGx1:APA91bH_aaaaaaaaaaaaaaaaaaaaaaaa-bbbbb") != nil {
		t.Error("a real-looking token was refused")
	}
	if k, err := CleanKinds([]string{"alerts", "business", "alerts"}); err != nil || strings.Join(k, ",") != "business,alerts" {
		t.Errorf("kinds: %v %v", k, err)
	}
	if _, err := CleanKinds([]string{"spam"}); err == nil {
		t.Error("an unknown kind was taken")
	}
	if len(DefaultKinds(true)) != 5 || len(DefaultKinds(false)) != 4 {
		t.Error("the defaults: alerts only for those who run the business")
	}
}
