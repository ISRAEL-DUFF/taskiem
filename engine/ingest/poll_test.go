package ingest_test

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/gmail"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/connectors/zohobooks"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/runtime"
)

const onChargeFlow = `{"schema":"wd/v1","id":"wf_thanks","version":1,"name":"thanks",
  "trigger":{"type":"connector_event","config":{"connector":"paystack@1","trigger":"charge_event","events":["charge.success"]}},
  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.data.reference"}}]}`

// A business that never set Paystack's webhook up still has its payments
// noticed (no-technical-steps N4): the poller asks Paystack from where it
// last looked, and each payment is delivered as Paystack would have sent
// it; a payment the webhook also delivered starts nothing twice.
func TestPollerDeliversPaystackPayments(t *testing.T) {
	w := newWorld(t)
	var asked atomic.Int64
	var from atomic.Value
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test_x" || r.URL.Path != "/transaction" || r.URL.Query().Get("status") != "success" {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		asked.Add(1)
		from.Store(r.URL.Query().Get("from"))
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"status":true,"data":[{"reference":"P-1","amount":500000,"customer":{"email":"bola@example.com"}},{"reference":"P-2","amount":250000}]}`))
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(paystack.New(paystack.Options{BaseURL: fake.URL})); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
		t.Fatal(err)
	}
	wf := w.publish(t, onChargeFlow)
	now := time.Now()
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}

	// The first look only marks where to start: nothing before is replayed.
	if n, err := p.Tick(ctx); err != nil || n != 0 || asked.Load() != 0 {
		t.Fatalf("first tick: %d %v (asked %d)", n, err, asked.Load())
	}
	// Five minutes on: it asks from then (less the overlap), and both
	// payments start the workflow.
	now = now.Add(6 * time.Minute)
	if n, err := p.Tick(ctx); err != nil || n != 2 {
		t.Fatalf("second tick: %d %v", n, err)
	}
	if f, _ := from.Load().(string); f == "" || !strings.HasSuffix(f, "Z") {
		t.Errorf("asked from %q", f)
	}
	if n := w.runCount(t, wf); n != 2 {
		t.Fatalf("runs: %d", n)
	}
	// Not due yet: nothing is asked.
	if n, _ := p.Tick(ctx); n != 0 || asked.Load() != 1 {
		t.Errorf("tick before due: %d (asked %d)", n, asked.Load())
	}
	// The webhook delivers P-1 too, and the next poll lists both again:
	// nothing starts twice.
	body := []byte(`{"data":{"amount":500000,"customer":{"email":"bola@example.com"},"reference":"P-1"},"event":"charge.success"}`)
	if st, out := w.post(t, "/connectors/paystack@1/charge_event", body, "x-paystack-signature", sign(sha512.New, "sk_test_x", body)); st != 202 || out["duplicate"] != true {
		t.Errorf("the webhook after the poll: %d %v", st, out)
	}
	now = now.Add(6 * time.Minute)
	if _, err := p.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.runCount(t, wf); n != 2 {
		t.Errorf("runs after polling again: %d", n)
	}
	// Where it got to is recorded.
	var errText *string
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT last_error FROM trigger_polls WHERE connector = 'paystack@1' AND trigger_name = 'charge_event'`).Scan(&errText)
	})
	if err != nil || errText != nil {
		t.Errorf("poll state: %v %v", errText, err)
	}
}

// The money log's own intake (spec 7.3): with no workflow at all, a
// connected Paystack is still asked about its payments, and each one is
// logged once.
func TestPollerFeedsTheMoneyLog(t *testing.T) {
	w := newWorld(t)
	w.h.MoneyLog = &moneylog.Log{Pool: w.DB.App, PII: w.Vault}
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/transaction" {
			_, _ = rw.Write([]byte(`{"status":true,"data":[{"reference":"P-7","amount":120000}]}`))
			return
		}
		_, _ = rw.Write([]byte(`{"status":true,"data":[]}`))
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(paystack.New(paystack.Options{BaseURL: fake.URL})); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h, MoneyLog: true,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}
	for range 3 {
		if _, err := p.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(6 * time.Minute)
	}
	got := w.moneyLog(t)
	if len(got) != 1 || got[0].reference != "P-7" || got[0].kind != "payment" {
		t.Fatalf("logged %+v", got)
	}
	// Without the log's intake, nothing is asked: no workflow wants it.
	w2 := newWorld(t)
	p2 := &ingest.Poller{Store: w2.Store, Registry: w2.Registry, Connections: w2.Vault, Hooks: w2.h}
	if n, err := p2.Tick(ctx); err != nil || n != 0 {
		t.Errorf("no targets: %d %v", n, err)
	}
}

// The money log's intake polls money events only: a connected Zoho Books
// (books events, records of money) is not asked anything, which would
// spend its organisation's daily quota, until a workflow listens.
func TestPollerLeavesTheBooksToWorkflows(t *testing.T) {
	w := newWorld(t)
	w.h.MoneyLog = &moneylog.Log{Pool: w.DB.App, PII: w.Vault}
	var asked atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"code":0,"message":"success","invoices":[],"customerpayments":[]}`))
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(zohobooks.New(zohobooks.Options{BaseURL: fake.URL, AccountsServer: fake.URL})); err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{"refresh_token": "1000.r", "client_id": "1000.c", "client_secret": "s", "accounts_server": "https://accounts.zoho.com", "api_domain": "https://www.zohoapis.com",
		"organization_id": "1", "access_token": "1000.a", "access_expires_at": "2099-01-01T00:00:00Z"}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "zoho_books", "main", "oauth2", creds, "test"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h, MoneyLog: true,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}
	for range 3 {
		if _, err := p.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(16 * time.Minute)
	}
	if asked.Load() != 0 {
		t.Errorf("asked Zoho %d times", asked.Load())
	}
}

// Polling Zoho Books with an expired access token refreshes it and keeps
// it, as a workflow step would (RotateCredentials), and the change it finds
// starts the workflow.
func TestPollerRefreshesZohoTokens(t *testing.T) {
	w := newWorld(t)
	var issued atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/oauth/v2/token":
			issued.Add(1)
			_, _ = rw.Write([]byte(`{"access_token":"1000.fresh","expires_in":3600}`))
		case r.Header.Get("Authorization") != "Zoho-oauthtoken 1000.fresh":
			rw.WriteHeader(http.StatusUnauthorized)
			_, _ = rw.Write([]byte(`{"code":57,"message":"You are not authorized to perform this operation"}`))
		case r.URL.Path == "/invoices":
			_, _ = rw.Write([]byte(`{"code":0,"message":"success","invoices":[{"invoice_id":"460000000027017","invoice_number":"INV-000042","status":"paid","total":53750,"balance":0,"created_time":"2026-10-20T10:00:00+0100","last_modified_time":"2026-10-26T11:00:00+0100"}]}`))
		default:
			_, _ = rw.Write([]byte(`{"code":0,"message":"success","customerpayments":[]}`))
		}
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(zohobooks.New(zohobooks.Options{BaseURL: fake.URL, AccountsServer: fake.URL})); err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{"refresh_token": "1000.r", "client_id": "1000.c", "client_secret": "s", "accounts_server": "https://accounts.zoho.com", "api_domain": "https://www.zohoapis.com",
		"organization_id": "1", "access_token": "1000.expired", "access_expires_at": "2020-01-01T00:00:00Z"}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "zoho_books", "main", "oauth2", creds, "test"); err != nil {
		t.Fatal(err)
	}
	wf := w.publish(t, `{"schema":"wd/v1","id":"wf_paid","version":1,"name":"paid","trigger":{"type":"connector_event","config":{"connector":"zoho_books@1","trigger":"changes","events":["invoice.paid"]}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.data.id"}}]}`)
	now := time.Now()
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}
	for range 2 {
		if _, err := p.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(16 * time.Minute)
	}
	stored, err := w.Vault.Credentials(ctx, w.Tenant, "prod", "zoho_books", "main")
	if err != nil || stored["access_token"] != "1000.fresh" || issued.Load() != 1 {
		t.Fatalf("token %q, issued %d: %v", stored["access_token"], issued.Load(), err)
	}
	if n := w.runCount(t, wf); n != 1 {
		t.Errorf("runs: %d", n)
	}
}

// A poll action that never answers (a tenant's own connector looping, or
// a provider holding the connection) is cut off after CallTimeout: the
// tick finishes, the target records the failure, and every other target
// is still polled.
func TestPollerCutsOffAStuckTarget(t *testing.T) {
	w := newWorld(t)
	w.h.MoneyLog = &moneylog.Log{Pool: w.DB.App, PII: w.Vault}
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"status":true,"data":[{"reference":"P-9","amount":120000}]}`))
	}))
	t.Cleanup(fake.Close)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	ps := paystack.New(paystack.Options{BaseURL: fake.URL})
	ps.Actions["list_transfers"] = connector.ActionFunc(func(ctx context.Context, _ connector.Request) (connector.Response, error) {
		select {
		case <-ctx.Done():
			return connector.Response{}, ctx.Err()
		case <-stop:
			return connector.Response{}, errors.New("test over")
		}
	})
	if err := w.Registry.Register(ps); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h, MoneyLog: true, CallTimeout: 300 * time.Millisecond,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}
	for range 2 {
		done := make(chan error, 1)
		go func() { _, err := p.Tick(ctx); done <- err }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a stuck poll action held the tick")
		}
		now = now.Add(6 * time.Minute)
	}
	if got := w.moneyLog(t); len(got) != 1 || got[0].reference != "P-9" {
		t.Errorf("logged %+v", got)
	}
	var errText *string
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT last_error FROM trigger_polls WHERE connector = 'paystack@1' AND trigger_name = 'transfer_event'`).Scan(&errText)
	})
	if err != nil || errText == nil || !strings.Contains(*errText, "deadline") {
		t.Errorf("stuck target's state: %v %v", errText, err)
	}
}

// A ticket workflow on the business's own Gmail (gmail@1 new_mail, polled
// as Zoho Mail's is): the customer's reply wakes the run waiting on its
// thread, each message starts each workflow whose options take it once,
// and the business's own mail is never news.
func TestPollerWakesAGmailThread(t *testing.T) {
	w := newWorld(t)
	first := time.Now().UTC().Truncate(time.Second)
	at := func(d time.Duration) string { return strconv.FormatInt(first.Add(d).UnixMilli(), 10) }
	message := func(id, thread, from string, d time.Duration, labels ...string) string {
		b, _ := json.Marshal(map[string]any{"id": id, "threadId": thread, "labelIds": labels, "snippet": "hi", "internalDate": at(d),
			"payload": map[string]any{"mimeType": "text/plain", "headers": []any{map[string]string{"name": "From", "value": from}, map[string]string{"name": "Message-ID", "value": "<" + id + "@example.com>"}},
				"body": map[string]any{"data": base64.RawURLEncoding.EncodeToString([]byte("Thanks, a refund is fine.\n\nOn Fri, Ada Cakes wrote:\n> sorry"))}}})
		return string(b)
	}
	messages := map[string]string{
		"m1":   message("m1", "t1", "Ada Obi <ada@example.com>", 2*time.Minute, "INBOX", "UNREAD"),
		"m2":   message("m2", "t2", "bayo@example.org", 3*time.Minute, "INBOX", "Label_7"),
		"sent": message("sent", "t1", "help@adacakes.ng", 4*time.Minute, "SENT"),
		"self": message("self", "t1", "Ada Cakes <help@adacakes.ng>", 4*time.Minute, "INBOX"),
	}
	var searches atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch p := r.URL.Path; {
		case p == "/token":
			_, _ = rw.Write([]byte(`{"access_token":"ya29.t","expires_in":3600}`))
		case r.Header.Get("Authorization") != "Bearer ya29.t":
			rw.WriteHeader(http.StatusUnauthorized)
		case p == "/users/me/profile":
			_, _ = rw.Write([]byte(`{"emailAddress":"help@adacakes.ng"}`))
		case p == "/users/me/messages":
			searches.Add(1)
			_, _ = rw.Write([]byte(`{"messages":[{"id":"sent"},{"id":"self"},{"id":"m2"},{"id":"m1"}]}`))
		default:
			_, _ = rw.Write([]byte(messages[strings.TrimPrefix(p, "/users/me/messages/")]))
		}
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(gmail.New(gmail.Options{BaseURL: fake.URL, TokenURL: fake.URL + "/token"})); err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{"client_id": "cid", "client_secret": "s", "refresh_token": "1//rt"}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "gmail", "main", "oauth2", creds, "test"); err != nil {
		t.Fatal(err)
	}
	ticket := w.Publish(t, `{"schema":"wd/v1","id":"wf_ticket","version":1,"name":"ticket","trigger":{"type":"manual"},
	  "steps":[{"id":"reply","type":"signal","config":{"event":"gmail@1:new_mail","correlation":"=trigger.body.thread","timeout":"1h"}}]}`)
	customers := w.publish(t, `{"schema":"wd/v1","id":"wf_customers","version":1,"name":"customers",
	  "trigger":{"type":"connector_event","config":{"connector":"gmail@1","trigger":"new_mail","options":{"senders":["example.com"]}}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.data.thread_id"}}]}`)
	labelled := w.publish(t, `{"schema":"wd/v1","id":"wf_labelled","version":1,"name":"labelled",
	  "trigger":{"type":"connector_event","config":{"connector":"gmail@1","trigger":"new_mail","options":{"label_ids":["Label_7"]}}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.data.id"}}]}`)
	waiting := w.Start(t, ticket, map[string]any{"body": map[string]any{"thread": "t1"}})

	now := first
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}
	if n, err := p.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("first tick: %d %v", n, err)
	}
	now = now.Add(6 * time.Minute)
	if n, err := p.Tick(ctx); err != nil || n != 2 {
		t.Fatalf("second tick: %d %v", n, err)
	}
	if s := w.Status(t, waiting); s != "completed" {
		t.Fatalf("the run waiting on the thread: %s", s)
	}
	reply, _ := w.stepOutput(t, waiting, "reply")["body"].(map[string]any)
	if data, _ := reply["data"].(map[string]any); data["id"] != "m1" {
		t.Errorf("the run woke on %v, want the customer's reply m1", reply)
	}
	if a, b := w.runCount(t, customers), w.runCount(t, labelled); a != 1 || b != 1 {
		t.Errorf("runs: customers %d (from example.com), labelled %d (Label_7)", a, b)
	}
	// The next poll lists the same mail again: nothing starts twice.
	now = now.Add(6 * time.Minute)
	if _, err := p.Tick(ctx); err != nil || searches.Load() != 2 {
		t.Fatalf("third tick: %v (searches %d)", err, searches.Load())
	}
	if a, b := w.runCount(t, customers), w.runCount(t, labelled); a != 1 || b != 1 {
		t.Errorf("runs after polling again: %d %d", a, b)
	}
}

// A busy mailbox: more mail than one poll reads (200) arrives between
// polls. Every message starts its run once, oldest first, over the next
// polls, and none is read from Gmail twice, though each poll looks back
// over the last ten minutes.
func TestPollerCatchesUpABusyGmail(t *testing.T) {
	w := newWorld(t)
	first := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	const total = 250
	type msg struct {
		id string
		ms int64
	}
	var mail []msg
	for i := range total {
		mail = append(mail, msg{fmt.Sprintf("m%03d", i), first.Add(time.Minute + time.Duration(i)*time.Second).UnixMilli()})
	}
	var mu sync.Mutex
	reads := map[string]int{}
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch p := r.URL.Path; {
		case p == "/token":
			_, _ = rw.Write([]byte(`{"access_token":"ya29.t","expires_in":3600}`))
		case p == "/users/me/profile":
			_, _ = rw.Write([]byte(`{"emailAddress":"help@adacakes.ng"}`))
		case p == "/users/me/messages":
			// Newest first, after: and before: in seconds, in pages.
			var after, before int64 = 0, 1 << 62
			for _, f := range strings.Fields(r.URL.Query().Get("q")) {
				if v, ok := strings.CutPrefix(f, "after:"); ok {
					after, _ = strconv.ParseInt(v, 10, 64)
				}
				if v, ok := strings.CutPrefix(f, "before:"); ok {
					before, _ = strconv.ParseInt(v, 10, 64)
				}
			}
			var refs []any
			for i := len(mail) - 1; i >= 0; i-- {
				if s := mail[i].ms / 1000; s > after && s < before {
					refs = append(refs, map[string]any{"id": mail[i].id, "threadId": mail[i].id})
				}
			}
			b, _ := json.Marshal(map[string]any{"messages": refs})
			_, _ = rw.Write(b)
		default:
			id := strings.TrimPrefix(p, "/users/me/messages/")
			mu.Lock()
			reads[id]++
			mu.Unlock()
			var at int64
			for _, m := range mail {
				if m.id == id {
					at = m.ms
				}
			}
			b, _ := json.Marshal(map[string]any{"id": id, "threadId": id, "labelIds": []string{"INBOX"}, "snippet": "hi", "internalDate": strconv.FormatInt(at, 10),
				"payload": map[string]any{"mimeType": "text/plain", "headers": []any{map[string]string{"name": "From", "value": "ada@example.com"}},
					"body": map[string]any{"data": base64.RawURLEncoding.EncodeToString([]byte("Hello"))}}})
			_, _ = rw.Write(b)
		}
	}))
	t.Cleanup(fake.Close)
	if err := w.Registry.Register(gmail.New(gmail.Options{BaseURL: fake.URL, TokenURL: fake.URL + "/token"})); err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{"client_id": "cid", "client_secret": "s", "refresh_token": "1//rt"}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "gmail", "main", "oauth2", creds, "test"); err != nil {
		t.Fatal(err)
	}
	inbox := w.publish(t, `{"schema":"wd/v1","id":"wf_inbox","version":1,"name":"inbox",
	  "trigger":{"type":"connector_event","config":{"connector":"gmail@1","trigger":"new_mail"}},
	  "steps":[{"id":"a","type":"transform","config":{"output":{"id":"=trigger.body.data.id"}}}]}`)

	now := first
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: func() time.Time { return now }}
	if _, err := p.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	// The mail arrives; the next poll reads the oldest 200 and is due
	// again on the next tick, which reads the rest; later polls read
	// nothing again.
	now = now.Add(6 * time.Minute)
	var got []int
	for range 4 {
		n, err := p.Tick(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
		now = now.Add(time.Minute)
	}
	now = now.Add(10 * time.Minute)
	if n, err := p.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("a later poll: %d %v", n, err)
	}
	if got[0] != 200 || got[1] != total-200 {
		t.Fatalf("delivered per tick %v", got)
	}
	if n := w.runCount(t, inbox); n != total {
		t.Fatalf("%d runs for %d messages", n, total)
	}
	var runs []uuid.UUID
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM runs WHERE workflow_id = $1 ORDER BY id`, inbox)
		if err != nil {
			return err
		}
		runs, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Each run started for its message, in the order the mail came.
	for i, id := range runs {
		evs, err := w.Store.RunHistory(ctx, runtime.RunRef{ID: id, TenantID: w.Tenant})
		if err != nil || len(evs) == 0 {
			t.Fatal(err)
		}
		var started struct {
			Trigger struct {
				Body struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"body"`
			} `json:"trigger"`
		}
		_ = json.Unmarshal(evs[0].Payload, &started)
		if got := started.Trigger.Body.Data.ID; got != mail[i].id {
			t.Fatalf("run %d is for %s, want %s: not each once, oldest first", i, got, mail[i].id)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reads) != total {
		t.Errorf("%d messages read, want %d", len(reads), total)
	}
	for id, n := range reads {
		if n != 1 {
			t.Errorf("%s read %d times", id, n)
		}
	}
}
