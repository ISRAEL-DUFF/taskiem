package api_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
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
	"github.com/israel-duff/taskiem/engine/push"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A fake Firebase for the server's tests: an access token for anyone, and
// every message kept by the token it was for.
type fakeFirebase struct {
	srv  *httptest.Server
	mu   sync.Mutex
	sent map[string][]map[string]any
}

func newFakeFirebase(t *testing.T) *fakeFirebase {
	f := &fakeFirebase{sent: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "expires_in": 3600})
			return
		}
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		msg := m["message"].(map[string]any)
		f.mu.Lock()
		f.sent[msg["token"].(string)] = append(f.sent[msg["token"].(string)], msg)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"name":"projects/p/messages/1"}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFirebase) to(token string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.sent[token]...)
}

func firebaseSender(t *testing.T, f *fakeFirebase) *push.Sender {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &push.Sender{
		FCM:   push.NewFCMForTest("taskiem-test", "push@taskiem-test.iam.gserviceaccount.com", key, f.srv.URL+"/token", f.srv.URL),
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
		Retry: time.Millisecond,
	}
}

func dataOf(m map[string]any) map[string]any { return m["data"].(map[string]any) }
func wordsOf(m map[string]any) (string, string) {
	n := m["notification"].(map[string]any)
	return n["title"].(string), n["body"].(string)
}

// Push everywhere (docs/spec/mobile-app-m4.md N1 to N3): the phone app as a
// device; whatever tells the bell notifies, queued with it; a customer's
// message and an approval waiting each tell; each device takes the kinds it
// chose.
func TestPushEverywhere(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 1000
	seller, buyer, _, buyerID := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	cashier := w.member(t, buyer, "cashier@bills.test", "cashier")
	fb := newFakeFirebase(t)
	const ownerPhone, cashierPhone = "fGx1:APA91bH_owner-phone-aaaaaaaaaaaa", "fGx1:APA91bH_cashier-phone-aaaaaaaaaa"
	phone := func(token string) map[string]any {
		return map[string]any{"kind": "fcm", "token": token, "platform": "android", "label": "Tecno Spark"}
	}

	// Without Firebase a phone cannot be added, and the app is told nothing.
	w.srv.Push = pushSender(t)
	if s, _ := buyer.do("PUT", "/v1/me/push", phone(ownerPhone)); s != 404 {
		t.Errorf("a phone with no Firebase: %d", s)
	}
	if c := buyer.must(200, "GET", "/v1/app/config", nil); c["push"] != nil {
		t.Errorf("the app was told of Firebase: %v", c["push"])
	}
	w.srv.Push = firebaseSender(t, fb)
	if c := buyer.must(200, "GET", "/v1/app/config", nil); c["push"].(map[string]any)["fcm"].(map[string]any)["project_id"] != "taskiem-test" {
		t.Errorf("the app's Firebase values: %v", c["push"])
	}
	// Firebase alone: no browser key, and the server says so.
	if s, _ := buyer.do("GET", "/v1/me/push/key", nil); s != 404 {
		t.Errorf("a browser key with Firebase only: %d", s)
	}

	// The owner's phone takes every kind; the cashier's all but alerts.
	if s, _ := buyer.do("PUT", "/v1/me/push", phone("not a token")); s != 400 {
		t.Errorf("a bad token: %d", s)
	}
	out := buyer.must(200, "PUT", "/v1/me/push", phone(ownerPhone))
	d := out["devices"].([]any)[0].(map[string]any)
	if d["kind"] != "fcm" || d["platform"] != "android" || len(d["kinds"].([]any)) != 5 || out["phones"] != true || out["browsers"] != false {
		t.Fatalf("the owner's phone: %v", out)
	}
	if strings.Contains(string(jsonOf(t, out)), ownerPhone) {
		t.Error("the token came back")
	}
	cd := cashier.must(200, "PUT", "/v1/me/push", phone(cashierPhone))["devices"].([]any)[0].(map[string]any)
	if k := cd["kinds"].([]any); len(k) != 4 || strings.Contains(string(jsonOf(t, k)), "alerts") {
		t.Errorf("the cashier's kinds: %v", k)
	}

	// A business chat's message: to the owner (chat.trade), with the business,
	// its kind, the conversation's tag and the icon's count.
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Rice is 45k a bag"})
	pushSettle(t, w.srv)
	got := fb.to(ownerPhone)
	if len(got) != 1 {
		t.Fatalf("the owner's phone got %d", len(got))
	}
	data := dataOf(got[0])
	if ti, b := wordsOf(got[0]); ti != "Dangote Stores" || !strings.Contains(b, "Rice is 45k a bag") || data["kind"] != "business" || data["business"] != buyerID ||
		data["link"] != "/chat?with="+link || data["tag"] != link || data["unread"] != "1" {
		t.Errorf("the message's push: %v %v", got[0]["notification"], data)
	}
	if n := len(fb.to(cashierPhone)); n != 0 {
		t.Errorf("the cashier (who does not write to businesses) got %d", n)
	}
	// The bell item that follows is not pushed again.
	pushSettle(t, w.srv)
	if n := len(fb.to(ownerPhone)); n != 1 {
		t.Errorf("a chat's bell item was pushed too: %d", n)
	}

	// Whatever tells the bell notifies: a bill, its repeat, and nothing for
	// a person's own changes or a run that finished.
	bell := func(user, kind, group, link, title string) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO notifications (id, tenant_id, user_id, kind, level, title, body, link, group_key)
			VALUES ($1, $2, $3, $4, 'info', $5, 'Due Friday', $6, $7)
			ON CONFLICT (tenant_id, user_id, group_key) WHERE read_at IS NULL DO UPDATE SET count = notifications.count + 1, title = EXCLUDED.title`,
			uuid.New(), buyerID, user, kind, title, link, group); err != nil {
			t.Fatal(err)
		}
	}
	owner := userID(t, buyer).String()
	bell(owner, "setup", "bill:b1", "/bills", "Bill from Dangote: ₦450,000")
	bell(owner, "setup", "bill:b1", "/bills", "Bill from Dangote: ₦450,000 (2 times)")
	bell(owner, "setup", "changes", "/chat?with=taskiem", "You added rice")
	bell(owner, "run_finished", "finished:x", "/runs", "Payroll finished")
	pushSettle(t, w.srv)
	got = fb.to(ownerPhone)
	if len(got) != 2 {
		t.Fatalf("after the bell: %d pushes", len(got))
	}
	if ti, _ := wordsOf(got[1]); ti != "Bill from Dangote: ₦450,000 (2 times)" || dataOf(got[1])["kind"] != "orders" || dataOf(got[1])["link"] != "/bills" || dataOf(got[1])["tag"] != "bill:b1" {
		t.Errorf("a bill's push (the newest of the two): %v %v", got[1]["notification"], dataOf(got[1]))
	}

	// Kinds: the owner turns orders off; a bill is then not pushed, and the
	// queue lets it go.
	id := d["id"].(string)
	buyer.must(200, "PATCH", "/v1/me/push/"+id, map[string]any{"kinds": []string{"business", "customers", "approvals", "alerts"}})
	if s, _ := buyer.do("PATCH", "/v1/me/push/"+id, map[string]any{"kinds": []string{"spam"}}); s != 400 {
		t.Errorf("an unknown kind: %d", s)
	}
	bell(owner, "setup", "bill:b2", "/bills", "Bill from Dangote: ₦9,000")
	pushSettle(t, w.srv)
	if n := len(fb.to(ownerPhone)); n != 2 {
		t.Errorf("a kind turned off was pushed: %d", n)
	}
	var left int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM push_outbox`).Scan(&left)
	if left != 0 {
		t.Errorf("the queue kept %d pushes nobody takes", left)
	}

	// Queued, then a restart before it is sent: still sent once the server
	// is back (the queue is the database's).
	bell(owner, "alert", "alert:a1", "/alerts", "Stock is running low: rice")
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM push_outbox`).Scan(&left)
	if left != 1 {
		t.Fatalf("queued: %d", left)
	}
	pushSettle(t, w.srv)
	if n := len(fb.to(ownerPhone)); n != 3 {
		t.Errorf("a queued push after a restart: %d", n)
	}

	// A customer's message to the business's own number: the people who
	// handle orders are told, in the bell and on their phones, once per
	// conversation while it is unread.
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	buyer.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "own_app": false})
	const customer = "+2348031112222"
	for _, text := range []string{"Do you deliver to Yaba?", "I want 2 bags"} {
		w.sendTo(t, "/channels/whatsapp", pnA, waSecret, customer, whatsapptest.Text(text))
	}
	pushSettle(t, w.srv)
	// Both waited in the queue together: the newest only, which replaces the
	// first on the phone anyway (one notification per conversation).
	cg := fb.to(cashierPhone)
	if len(cg) != 1 {
		t.Fatalf("the cashier's phone after a customer wrote twice: %d", len(cg))
	}
	t1, b1 := wordsOf(cg[0])
	if !strings.HasSuffix(t1, " wrote 2 messages") || b1 != "I want 2 bags" ||
		dataOf(cg[0])["kind"] != "customers" || !strings.HasPrefix(dataOf(cg[0])["link"].(string), "/chat?with=c-") || dataOf(cg[0])["tag"] != strings.TrimPrefix(dataOf(cg[0])["link"].(string), "/chat?with=") {
		t.Errorf("a customer's push: %q %q %v", t1, b1, dataOf(cg[0]))
	}
	// One at a time, each is sent.
	w.sendTo(t, "/channels/whatsapp", pnA, waSecret, customer, whatsapptest.Text("And 3 cartons of oil"))
	pushSettle(t, w.srv)
	if cg = fb.to(cashierPhone); len(cg) != 2 {
		t.Fatalf("a third message: %d", len(cg))
	} else if ti, b := wordsOf(cg[1]); !strings.HasSuffix(ti, " wrote 3 messages") || b != "And 3 cartons of oil" {
		t.Errorf("the third: %q %q", ti, b)
	}
	if n := len(cashier.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any)); n != 1 {
		t.Errorf("the cashier's bell: %d items", n)
	}

	// A device that lost which one it is asks the server (known): found, it
	// keeps its name; not found, it is not added again.
	k := cashier.must(200, "PUT", "/v1/me/push", map[string]any{"kind": "fcm", "token": cashierPhone, "platform": "android", "known": true})
	if k["added"] != cd["id"] || k["devices"].([]any)[0].(map[string]any)["label"] != "Tecno Spark" {
		t.Errorf("a known phone: %v", k)
	}
	if s, _ := cashier.do("PUT", "/v1/me/push", map[string]any{"kind": "fcm", "token": "fGx1:APA91bH_never-seen-aaaaaaaaaaaaa", "platform": "android", "known": true}); s != 404 {
		t.Errorf("an unknown phone asked as known: %d", s)
	}

	// Firebase changes the phone's token: the new one replaces it, keeping
	// its name and kinds.
	cashier.must(200, "PATCH", "/v1/me/push/"+cd["id"].(string), map[string]any{"kinds": []string{"customers"}})
	const renewed = "fGx1:APA91bH_cashier-renewed-aaaaaaaa"
	rv := cashier.must(200, "PUT", "/v1/me/push", map[string]any{"kind": "fcm", "token": renewed, "platform": "android", "replaces": cashierPhone})
	if ds := rv["devices"].([]any); len(ds) != 1 || ds[0].(map[string]any)["label"] != "Tecno Spark" || len(ds[0].(map[string]any)["kinds"].([]any)) != 1 {
		t.Errorf("a renewed token: %v", rv)
	}
	if s, _ := cashier.do("PUT", "/v1/me/push", map[string]any{"kind": "fcm", "token": cashierPhone, "platform": "android", "known": true}); s != 404 {
		t.Errorf("the old token is still a device: %d", s)
	}

	// Signing out takes the phone off (the app sends its token).
	cashier.must(200, "DELETE", "/v1/me/push", map[string]any{"token": renewed})
	if n := len(cashier.must(200, "GET", "/v1/me/push", nil)["devices"].([]any)); n != 0 {
		t.Errorf("the cashier's phone after taking itself off: %d", n)
	}
}

// An approval waiting is told in the bell, once per approver and level,
// whether or not a chat app is set up, and so pushed (N2).
func TestApprovalInTheBell(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "owner@acme.test", "roles": []string{"credit_officer", "approver"}})
	alice := w.member(t, owner, "alice@acme.test", "approver", "credit_officer")
	fb := newFakeFirebase(t)
	w.srv.Push = firebaseSender(t, fb)
	const alicePhone = "fGx1:APA91bH_alice-phone-aaaaaaaaaaaaaa"
	alice.must(200, "PUT", "/v1/me/push", map[string]any{"kind": "fcm", "token": alicePhone, "platform": "android"})

	wf := publishFlow(t, owner, waLoanFlow)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 5000, "account_number": "0123456789"}})
	w.tick(t)
	for range 2 {
		if err := w.srv.ApprovalBellTick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	pushSettle(t, w.srv)
	got := fb.to(alicePhone)
	if len(got) != 1 {
		t.Fatalf("alice's phone: %d pushes", len(got))
	}
	if ti, _ := wordsOf(got[0]); !strings.HasPrefix(ti, "Approval waiting: ") || dataOf(got[0])["kind"] != "approvals" || dataOf(got[0])["link"] != "/approvals" {
		t.Errorf("the approval's push: %v %v", got[0]["notification"], dataOf(got[0]))
	}
	// The maker is not asked, in the bell or anywhere.
	for _, n := range owner.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any) {
		if strings.HasPrefix(n.(map[string]any)["title"].(string), "Approval waiting") {
			t.Error("the maker was told to approve their own run")
		}
	}
}
