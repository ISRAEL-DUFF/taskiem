package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/africastalking"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/ussd"
)

const (
	ussdPhone   = "+2348031234567"
	ussdAccount = "0123456789"
	ussdCode    = "*384*123#"
)

// A bill payment over USSD: product, a meter number (personal), an
// amount, a confirmation; the outcome by SMS. The run copies the
// caller's number into its output, to check that it stays sealed.
const ussdBillFlow = `{"schema":"wd/v1","id":"wf_bill","version":1,"name":"Bill","trigger":{"type":"ussd","config":{
  "service_code":"*384*123#",
  "screens":[
    {"id":"main","type":"menu","text":"Acme bills","input":"product","options":[{"label":"Electricity","next":"account","value":"power"},{"label":"Help","next":"help"}]},
    {"id":"account","type":"input","text":"Meter number","input":"account_number","validate":{"pattern":"[0-9]{10}"},"next":"amount"},
    {"id":"amount","type":"input","text":"Amount","input":"amount","validate":{"type":"integer","min":100,"max":50000},"next":"ok"},
    {"id":"ok","type":"confirm","text":"Pay N{{amount}} to {{account_number}}?","done":"Paid soon. Ref {{reference}}"},
    {"id":"help","type":"end","text":"Call us."}],
  "notify":{"sms":true,"completed":"Paid N{{amount}}. Ref {{reference}}.","failed":"Payment failed. Ref {{reference}}."}}},
  "inputs":{"schema":{"type":"object","required":["product","account_number","amount"],"properties":{
    "product":{"type":"string"},"account_number":{"type":"string","x-pii":"account_number"},"amount":{"type":"integer"}}}},
  "steps":[{"id":"t","type":"transform","config":{"output":{"amount":"=trigger.body.amount","to":"=trigger.caller.phone_number"}}}]}`

// fakeAT is Africa's Talking's SMS API, recording what it is sent.
type fakeAT struct {
	*httptest.Server
	mu   sync.Mutex
	sent []map[string]any
}

func newFakeAT(t *testing.T) *fakeAT {
	f := &fakeAT{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version1/messaging/bulk" || r.Header.Get("apiKey") != "at-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.sent = append(f.sent, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		num, _ := body["phoneNumbers"].([]any)
		_, _ = fmt.Fprintf(w, `{"SMSMessageData":{"Message":"Sent to 1/1 Total Cost: NGN 4.0000","Recipients":[{"statusCode":101,"number":%q,"status":"Success","cost":"NGN 4.0000","messageId":"ATXid_1"}]}}`, num[0])
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAT) messages() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.sent...)
}

// syncBuffer collects log output from several goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type ussdRig struct {
	w      *world
	owner  *client
	tenant uuid.UUID
	token  string
	at     *fakeAT
	logs   *syncBuffer
	edges  []*api.Server // edge replicas sharing the database
	urls   []string
}

func newUSSDRig(t *testing.T, edges int) *ussdRig {
	e := rt.New(t)
	at := newFakeAT(t)
	if err := e.Registry.Register(africastalking.New(africastalking.Options{BaseURL: at.URL})); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := &api.Server{Store: e.Store, Vault: e.Vault, Registry: e.Registry, Connectors: e.Connectors, AllowSignup: true, Egress: e.Egress, Logger: logger}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	w := &world{env: e, base: ts.URL, srv: srv}
	r := &ussdRig{w: w, at: at, logs: logs}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(logs.String())
		}
	})
	r.owner = w.tenant(t, "Acme", "owner@acme.test")
	r.tenant = tenantID(t, r.owner)
	out := r.owner.must(200, "PUT", "/v1/ussd/channels/africastalking", map[string]any{})
	r.token = out["token"].(string)
	if out["callback_url"] != "/channels/ussd/"+r.tenant.String()+"/africastalking?token="+r.token {
		t.Fatalf("callback: %v", out)
	}
	r.owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "africastalking@1", "name": "main",
		"credentials": map[string]string{"username": "acme", "api_key": "at-key", "sender_id": "ACME"}})
	for range edges {
		r.addEdge(t, srv)
	}
	return r
}

// addEdge starts an edge replica: its own process state, the same
// database. The first one is the API server itself.
func (r *ussdRig) addEdge(t *testing.T, first *api.Server) *api.Server {
	s := first
	if len(r.edges) > 0 {
		s = &api.Server{Store: r.w.env.Store, Vault: r.w.env.Vault, Registry: r.w.env.Registry, Egress: r.w.env.Egress, Logger: first.Logger}
	}
	ts := httptest.NewServer(s.USSDHooks())
	t.Cleanup(ts.Close)
	t.Cleanup(s.WaitBackground)
	r.edges = append(r.edges, s)
	r.urls = append(r.urls, ts.URL)
	return s
}

// dial posts one Africa's Talking callback to edge i.
func (r *ussdRig) dial(t *testing.T, i int, session, phone, code, text string) (int, string) {
	t.Helper()
	return r.dialToken(t, i, r.token, session, phone, code, text)
}

func (r *ussdRig) dialToken(t *testing.T, i int, token, session, phone, code, text string) (int, string) {
	t.Helper()
	form := url.Values{"sessionId": {session}, "serviceCode": {code}, "phoneNumber": {phone}, "networkCode": {"62130"}, "text": {text}}
	u := r.urls[i] + "/" + r.tenant.String() + "/africastalking"
	if token != "" {
		u += "?token=" + url.QueryEscape(token)
	}
	resp, err := http.Post(u, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (r *ussdRig) wait() {
	for _, s := range r.edges {
		s.WaitBackground()
	}
}

func (r *ussdRig) runs(t *testing.T) []runRow {
	t.Helper()
	rows, err := r.w.env.DB.Admin.Query(context.Background(), `SELECT id, status, COALESCE(started_by, '') FROM runs WHERE tenant_id = $1 ORDER BY started_at`, r.tenant)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (runRow, error) {
		var x runRow
		return x, row.Scan(&x.id, &x.status, &x.by)
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type runRow struct {
	id         uuid.UUID
	status, by string
}

func (r *ussdRig) session(t *testing.T, sid string) (state, notice string, data []byte, path []byte) {
	t.Helper()
	var n *string
	if err := r.w.env.DB.Admin.QueryRow(context.Background(), `SELECT state, notice, data::text, path::text FROM ussd_sessions WHERE tenant_id = $1 AND session_id = $2`,
		r.tenant, sid).Scan(&state, &n, &data, &path); err != nil {
		t.Fatal(err)
	}
	if n != nil {
		notice = *n
	}
	return state, notice, data, path
}

func publishUSSD(t *testing.T, c *client, name, doc string) string {
	t.Helper()
	out := c.must(201, "POST", "/v1/workflows", map[string]any{"name": name, "definition": json.RawMessage(doc)})
	if probs := out["problems"].([]any); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	id := out["id"].(string)
	c.must(200, "POST", "/v1/workflows/"+id+"/versions/1/publish", nil)
	return id
}

func TestUSSDEndToEnd(t *testing.T) {
	r := newUSSDRig(t, 1)
	wf := publishUSSD(t, r.owner, "bill", ussdBillFlow)
	if trig := toJSON(r.owner.must(200, "GET", "/v1/workflows/"+wf+"/triggers", nil)); !strings.Contains(trig, `"service_code":"*384*123#"`) || !strings.Contains(trig, `"type":"ussd"`) {
		t.Fatalf("triggers: %s", trig)
	}
	ref := ussd.Reference(r.tenant.String(), "africastalking", "ATUid_1")

	// The token is required, and checked before anything else.
	if st, _ := r.dialToken(t, 0, "", "ATUid_1", ussdPhone, ussdCode, ""); st != 401 {
		t.Fatalf("no token: %d", st)
	}
	if st, _ := r.dialToken(t, 0, "wrong", "ATUid_1", ussdPhone, ussdCode, ""); st != 401 {
		t.Fatalf("wrong token: %d", st)
	}
	steps := []struct{ text, want string }{
		{"", "CON Acme bills\n1. Electricity\n2. Help"},
		{"1", "CON Meter number\n0. Back"},
		{"1*123", "CON " + ussd.DefaultInputError + "\nMeter number\n0. Back"},
		{"1*123*" + ussdAccount, "CON Amount\n0. Back"},
		{"1*123*" + ussdAccount + "*5000", "CON Pay N5000 to ****6789?\n1. Yes\n2. Cancel\n0. Back"},
		{"1*123*" + ussdAccount + "*5000*1", "END Paid soon. Ref " + ref},
	}
	for _, s := range steps {
		st, body := r.dial(t, 0, "ATUid_1", ussdPhone, ussdCode, s.text)
		if st != 200 || body != s.want {
			t.Fatalf("text %q: %d %q, want %q", s.text, st, body, s.want)
		}
	}
	// The aggregator retries the confirmation: the same answer, one run.
	if _, body := r.dial(t, 0, "ATUid_1", ussdPhone, ussdCode, "1*123*"+ussdAccount+"*5000*1"); body != "END Paid soon. Ref "+ref {
		t.Fatalf("retry: %q", body)
	}
	r.wait()
	runs := r.runs(t)
	if len(runs) != 1 || runs[0].by != "ussd:"+ref {
		t.Fatalf("runs: %+v", runs)
	}
	r.w.env.Drain(t)
	if st := r.w.env.Status(t, runtime.RunRef{ID: runs[0].id, TenantID: r.tenant}); st != "completed" {
		t.Fatalf("run %s", st)
	}
	// The run had the inputs and the caller's number; history holds
	// neither the number nor the meter number in clear.
	h, err := r.w.env.Store.OpenedHistory(context.Background(), runtime.RunRef{ID: runs[0].id, TenantID: r.tenant})
	if err != nil {
		t.Fatal(err)
	}
	var opened string
	for _, e := range h {
		opened += string(e.Payload)
	}
	if !strings.Contains(opened, ussdPhone) || !strings.Contains(opened, ussdAccount) || !strings.Contains(opened, `"amount":5000`) {
		t.Fatalf("the run did not get its inputs: %s", opened)
	}
	var stored string
	if err := r.w.env.DB.Admin.QueryRow(context.Background(), `SELECT string_agg(payload::text, ' ') FROM run_events WHERE run_id = $1`, runs[0].id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"8031234567", ussdAccount} {
		if strings.Contains(stored, secret) {
			t.Errorf("history holds %s in clear", secret)
		}
	}
	// Sealed in the session until the outcome is sent.
	state, _, data, _ := r.session(t, "ATUid_1")
	if state != "started" || strings.Contains(string(data), "8031234567") || strings.Contains(string(data), ussdAccount) || !strings.Contains(string(data), `"$pii"`) {
		t.Fatalf("session %s: %s", state, data)
	}
	// The outcome by SMS, through the tenant's Africa's Talking connection.
	if err := r.edges[0].USSDTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	msgs := r.at.messages()
	if len(msgs) != 1 || fmt.Sprint(msgs[0]["phoneNumbers"]) != "["+ussdPhone+"]" || msgs[0]["message"] != "Paid N5000. Ref "+ref+"." || msgs[0]["senderId"] != "ACME" {
		t.Fatalf("sms: %v", msgs)
	}
	state, notice, data, _ := r.session(t, "ATUid_1")
	if state != "started" || notice != "sent" || data != nil {
		t.Fatalf("after the SMS: %s %s %s", state, notice, data)
	}
	// Once only.
	if err := r.edges[0].USSDTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.at.messages()) != 1 {
		t.Fatalf("sent twice")
	}
	// Audited by the system as ussd:<reference>; nothing personal in logs.
	var actor string
	if err := r.w.env.DB.Admin.QueryRow(context.Background(), `SELECT actor_id FROM audit_log WHERE tenant_id = $1 AND action = 'run.start'`, r.tenant).Scan(&actor); err != nil || actor != "ussd:"+ref {
		t.Fatalf("audit: %q %v", actor, err)
	}
	if logs := r.logs.String(); strings.Contains(logs, "8031234567") || strings.Contains(logs, ussdAccount) {
		t.Errorf("logs hold personal data:\n%s", logs)
	}
	// The admin view.
	view := r.owner.must(200, "GET", "/v1/ussd", nil)
	routes := view["routes"].([]any)
	if len(view["channels"].([]any)) != 1 || len(routes) < 1 || routes[0].(map[string]any)["service_code"] != ussdCode {
		t.Fatalf("view: %v", view)
	}
	if strings.Contains(toJSON(view), r.token) {
		t.Fatal("the token is shown again")
	}
	// Help ends the session without starting anything; a session past its
	// end answers so.
	if _, body := r.dial(t, 0, "ATUid_2", ussdPhone, ussdCode, "2"); body != "END Call us." {
		t.Fatalf("help: %q", body)
	}
	if _, body := r.dial(t, 0, "ATUid_2", ussdPhone, ussdCode, "1*1*0123456789*500*1"); !strings.HasPrefix(body, "END ") || len(r.runs(t)) != 1 {
		t.Fatalf("after the end: %q", body)
	}
	// Another number cannot take over a session.
	if _, body := r.dial(t, 0, "ATUid_1", "+2348039999999", ussdCode, "1"); !strings.HasPrefix(body, "END ") {
		t.Fatalf("another number: %q", body)
	}
}

// Two edge replicas share sessions through Postgres: a session started on
// one continues on the other, pinned to the version it began on, and
// confirmations racing on both start one run.
func TestUSSDReplicasAndIdempotentConfirm(t *testing.T) {
	r := newUSSDRig(t, 2)
	id := publishUSSD(t, r.owner, "bill", ussdBillFlow)
	if _, body := r.dial(t, 0, "S1", ussdPhone, ussdCode, ""); !strings.HasPrefix(body, "CON Acme bills") {
		t.Fatalf("start: %q", body)
	}
	// A new version changes the menu mid-session.
	v2 := strings.Replace(ussdBillFlow, `"text":"Meter number"`, `"text":"Your meter"`, 1)
	r.owner.must(201, "POST", "/v1/workflows/"+id+"/versions", map[string]any{"definition": json.RawMessage(v2)})
	r.owner.must(200, "POST", "/v1/workflows/"+id+"/versions/2/publish", nil)
	if _, body := r.dial(t, 1, "S1", ussdPhone, ussdCode, "1"); body != "CON Meter number\n0. Back" {
		t.Fatalf("replica B, pinned version: %q", body)
	}
	if _, body := r.dial(t, 0, "S2", ussdPhone, ussdCode, "1"); body != "CON Your meter\n0. Back" {
		t.Fatalf("a new session gets the new version: %q", body)
	}
	final := "1*" + ussdAccount + "*700*1"
	var wg sync.WaitGroup
	answers := make(chan string, 10)
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, body := r.dial(t, i%2, "S1", ussdPhone, ussdCode, final)
			answers <- body
		}()
	}
	wg.Wait()
	close(answers)
	ref := ussd.Reference(r.tenant.String(), "africastalking", "S1")
	for a := range answers {
		if a != "END Paid soon. Ref "+ref {
			t.Errorf("answer %q", a)
		}
	}
	r.wait()
	// The notifier finds nothing left to hand off.
	if err := r.edges[0].USSDTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runs := r.runs(t); len(runs) != 1 {
		t.Fatalf("%d runs", len(runs))
	}
	var version int
	if err := r.w.env.DB.Admin.QueryRow(context.Background(), `SELECT version FROM runs WHERE tenant_id = $1`, r.tenant).Scan(&version); err != nil || version != 1 {
		t.Fatalf("version %d %v", version, err)
	}
}

// The answer never waits on the engine: with a slow start the caller gets
// the reference within the budget, and the run follows.
func TestUSSDDeadline(t *testing.T) {
	r := newUSSDRig(t, 1)
	srv := r.edges[0]
	srv.USSD.Budget = 800 * time.Millisecond
	release := make(chan struct{})
	srv.SetUSSDStart(func(ctx context.Context, req runtime.StartRequest) (runtime.RunRef, bool, error) {
		<-release
		return r.w.env.Store.StartRun(ctx, req)
	})
	publishUSSD(t, r.owner, "bill", ussdBillFlow)
	t0 := time.Now()
	_, body := r.dial(t, 0, "SLOW", ussdPhone, ussdCode, "1*"+ussdAccount+"*700*1")
	if d := time.Since(t0); d > srv.USSD.Budget || body != "END Paid soon. Ref "+ussd.Reference(r.tenant.String(), "africastalking", "SLOW") {
		t.Fatalf("answered in %s: %q", d, body)
	}
	if len(r.runs(t)) != 0 {
		t.Fatal("started before the engine answered")
	}
	close(release)
	r.wait()
	if runs := r.runs(t); len(runs) != 1 {
		t.Fatalf("%d runs", len(runs))
	}

	// A database stuck on the session row: the caller is told nothing was
	// taken, in time, and nothing is recorded.
	if _, body := r.dial(t, 0, "LOCKED", ussdPhone, ussdCode, ""); !strings.HasPrefix(body, "CON ") {
		t.Fatalf("start: %q", body)
	}
	hold, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = db.InTenantTx(context.Background(), r.w.env.Store.Pool, []uuid.UUID{r.tenant}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(context.Background(), `SELECT 1 FROM ussd_sessions WHERE session_id = 'LOCKED' FOR UPDATE`); err != nil {
				return err
			}
			hold <- struct{}{}
			<-hold
			return nil
		})
	}()
	<-hold
	t0 = time.Now()
	_, body = r.dial(t, 0, "LOCKED", ussdPhone, ussdCode, "1*"+ussdAccount+"*700*1")
	if d := time.Since(t0); d > srv.USSD.Budget+200*time.Millisecond || !strings.HasPrefix(body, "END Sorry, we could not take your request") {
		t.Fatalf("stuck database: %s %q", d, body)
	}
	hold <- struct{}{}
	<-done
	if state, _, _, _ := r.session(t, "LOCKED"); state != "active" {
		t.Fatalf("state %s", state)
	}
	// Dialling the confirmation again now goes through.
	if _, body := r.dial(t, 0, "LOCKED", ussdPhone, ussdCode, "1*"+ussdAccount+"*700*1"); !strings.HasPrefix(body, "END Paid soon") {
		t.Fatalf("again: %q", body)
	}
}

// Rate limits per number (in process and across replicas), per-number
// confirmations, plan limits with the failure SMS, failed runs, the
// address allow-list, disabled and unknown channels, and service codes
// no workflow serves.
func TestUSSDLimitsAndSafety(t *testing.T) {
	r := newUSSDRig(t, 2)
	quick := `{"schema":"wd/v1","id":"wf_quick","version":1,"name":"Quick","trigger":{"type":"ussd","config":{"service_code":"*1#",
	  "screens":[{"id":"ok","type":"confirm","text":"Go?"}],"notify":{"sms":true}}},
	  "steps":[{"id":"t","type":"transform","config":{"output":{"x":"=1"}}}]}`
	publishUSSD(t, r.owner, "quick", quick)

	// Unknown provider and tenant; a code nobody serves.
	if st, _ := r.dialToken(t, 0, r.token, "N1", ussdPhone, "*9#", ""); st != 200 {
		t.Fatalf("status %d", st)
	}
	if _, body := r.dial(t, 0, "N1", ussdPhone, "*9#", ""); body != "END This service is not available." {
		t.Fatalf("unserved code: %q", body)
	}
	resp, err := http.Post(r.urls[0]+"/"+r.tenant.String()+"/nosuch?token="+r.token, "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil || resp.StatusCode != 404 {
		t.Fatalf("unknown provider: %v %v", resp.StatusCode, err)
	}
	_ = resp.Body.Close()
	resp, err = http.Post(r.urls[0]+"/"+uuid.NewString()+"/africastalking?token="+r.token, "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil || resp.StatusCode != 404 {
		t.Fatalf("unknown tenant: %v %v", resp.StatusCode, err)
	}
	_ = resp.Body.Close()
	// Malformed callbacks.
	if st, _ := r.dial(t, 0, "bad id!", ussdPhone, "*1#", ""); st != 400 {
		t.Fatalf("bad session id: %d", st)
	}

	// Five confirmations per number an hour, counted across replicas.
	for i := range 5 {
		if _, body := r.dial(t, i%2, fmt.Sprintf("Q%d", i), ussdPhone, "*1#", "1"); !strings.HasPrefix(body, "END Thank you") {
			t.Fatalf("confirm %d: %q", i, body)
		}
	}
	if _, body := r.dial(t, 1, "Q5", ussdPhone, "*1#", "1"); body != "END Too many requests from this number. Please try again later." {
		t.Fatalf("sixth confirmation: %q", body)
	}
	r.wait()
	if n := len(r.runs(t)); n != 5 {
		t.Fatalf("%d runs", n)
	}
	// A burst from one number on one replica.
	other := "+2348030000001"
	limited := false
	for i := range 30 {
		if _, body := r.dial(t, 0, "B1", other, "*1#", strings.Repeat("0*", i%3)); strings.Contains(body, "Too many") {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("no per-number limit on a burst")
	}

	// Plan limits: the run is refused, the session ends refused, and the
	// caller hears by SMS.
	if err := r.w.env.Store.SetLimits(context.Background(), r.tenant, map[string]any{"runs_per_day": 5}, "test"); err != nil {
		t.Fatal(err)
	}
	r.w.env.Store.ForgetLimits(r.tenant)
	third := "+2348030000002"
	if _, body := r.dial(t, 1, "P1", third, "*1#", "1"); !strings.HasPrefix(body, "END Thank you") {
		t.Fatalf("over the plan: %q", body)
	}
	r.wait()
	if state, _, _, _ := r.session(t, "P1"); state != "refused" {
		t.Fatalf("over the plan: %s", state)
	}
	if err := r.edges[0].USSDTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var refusedSMS bool
	for _, m := range r.at.messages() {
		if fmt.Sprint(m["phoneNumbers"]) == "["+third+"]" && strings.Contains(m["message"].(string), "could not be completed") {
			refusedSMS = true
		}
	}
	if !refusedSMS {
		t.Errorf("no SMS for the refused request: %v", r.at.messages())
	}

	// Disabled channel, then an address allow-list (edge 0 is the API
	// server, which forgets its cached channel on a change).
	fourth := "+2348030000003"
	r.owner.must(200, "PUT", "/v1/ussd/channels/africastalking", map[string]any{"disabled": true})
	if st, _ := r.dial(t, 0, "D1", fourth, "*1#", ""); st != 404 {
		t.Fatalf("disabled: %d", st)
	}
	r.owner.must(200, "PUT", "/v1/ussd/channels/africastalking", map[string]any{"allowed_cidrs": []string{"203.0.113.0/24"}})
	if st, _ := r.dial(t, 0, "D1", fourth, "*1#", ""); st != 401 {
		t.Fatalf("outside the allow-list: %d", st)
	}
	r.owner.must(200, "PUT", "/v1/ussd/channels/africastalking", map[string]any{"allowed_cidrs": []string{"127.0.0.1"}})
	if st, body := r.dial(t, 0, "D1", fourth, "*1#", ""); st != 200 || !strings.HasPrefix(body, "CON Go?") {
		t.Fatalf("inside the allow-list: %d %q", st, body)
	}
	// Rotating the token retires the old one.
	tok := r.owner.must(200, "PUT", "/v1/ussd/channels/africastalking", map[string]any{"rotate_token": true})["token"].(string)
	if st, _ := r.dial(t, 0, "D2", fourth, "*1#", ""); st != 401 {
		t.Fatalf("old token: %d", st)
	}
	if st, _ := r.dialToken(t, 0, tok, "D2", fourth, "*1#", ""); st != 200 {
		t.Fatalf("new token: %d", st)
	}
	// Only secret.manage changes channels.
	dev := addMember(t, r.w, r.owner, "dev", "developer")
	if st, _ := dev.do("PUT", "/v1/ussd/channels/africastalking", map[string]any{}); st != 403 {
		t.Fatalf("developer: %d", st)
	}
}

// A run that fails: the caller hears that it failed.
func TestUSSDFailedRunSMS(t *testing.T) {
	r := newUSSDRig(t, 1)
	failing := `{"schema":"wd/v1","id":"wf_fail","version":1,"name":"Fail","trigger":{"type":"ussd","config":{"service_code":"*2#",
	  "screens":[{"id":"n","type":"input","text":"Number","input":"n","validate":{"type":"integer"},"next":"ok"},{"id":"ok","type":"confirm","text":"Divide by {{n}}?"}],
	  "notify":{"sms":true,"failed":"Sorry, {{reference}} failed."}}},
	  "steps":[{"id":"t","type":"transform","config":{"output":{"x":"=10 / trigger.body.n"}}}]}`
	publishUSSD(t, r.owner, "fail", failing)
	// On the first screen "0" is an input, not Back.
	if _, body := r.dial(t, 0, "F1", ussdPhone, "*2#", "0"); body != "CON Divide by 0?\n1. Yes\n2. Cancel\n0. Back" {
		t.Fatalf("answer: %q", body)
	}
	if _, body := r.dial(t, 0, "F1", ussdPhone, "*2#", "0*1"); !strings.HasPrefix(body, "END Thank you") {
		t.Fatalf("answer: %q", body)
	}
	r.wait()
	runs := r.runs(t)
	if len(runs) != 1 {
		t.Fatalf("%d runs", len(runs))
	}
	r.w.env.Drain(t)
	if st := r.w.env.Status(t, runtime.RunRef{ID: runs[0].id, TenantID: r.tenant}); st != "failed" {
		t.Fatalf("run %s", st)
	}
	if err := r.edges[0].USSDTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	ref := ussd.Reference(r.tenant.String(), "africastalking", "F1")
	msgs := r.at.messages()
	if len(msgs) != 1 || msgs[0]["message"] != "Sorry, "+ref+" failed." {
		t.Fatalf("failure SMS: %v", msgs)
	}
}

// An aggregator that sends only the latest input: the path is kept in
// the session, sealed.
type incrementalAdapter struct{}

func (incrementalAdapter) Name() string      { return "incr" }
func (incrementalAdapter) Incremental() bool { return true }
func (incrementalAdapter) Parse(r *http.Request, body []byte) (ussd.Request, error) {
	var in struct{ Session, Code, MSISDN, Input string }
	if err := json.Unmarshal(body, &in); err != nil {
		return ussd.Request{}, ussd.ErrBadRequest
	}
	return ussd.Request{SessionID: in.Session, ServiceCode: in.Code, Phone: in.MSISDN, Input: in.Input}, nil
}
func (incrementalAdapter) Reply(w http.ResponseWriter, end bool, text string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"end": end, "text": text})
}

func TestUSSDIncrementalAdapter(t *testing.T) {
	r := newUSSDRig(t, 1)
	r.edges[0].USSD.Adapters = map[string]ussd.Adapter{"incr": incrementalAdapter{}}
	publishUSSD(t, r.owner, "bill", ussdBillFlow)
	tok := r.owner.must(200, "PUT", "/v1/ussd/channels/incr", map[string]any{})["token"].(string)
	send := func(input string) map[string]any {
		body, _ := json.Marshal(map[string]string{"Session": "I1", "Code": ussdCode, "MSISDN": ussdPhone, "Input": input})
		resp, err := http.Post(r.urls[0]+"/"+r.tenant.String()+"/incr?token="+tok, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	for _, in := range []string{"", "1", ussdAccount, "800"} {
		send(in)
	}
	_, _, _, path := r.session(t, "I1")
	if strings.Contains(string(path), ussdAccount) || !strings.Contains(string(path), `"$pii"`) {
		t.Fatalf("path in clear: %s", path)
	}
	out := send("1")
	if out["end"] != true || !strings.HasPrefix(out["text"].(string), "Paid soon. Ref ") {
		t.Fatalf("confirm: %v", out)
	}
	r.wait()
	if len(r.runs(t)) != 1 {
		t.Fatal("no run")
	}
}

// Publishing checks menus, and a service code serves one workflow per
// environment.
func TestUSSDPublishChecks(t *testing.T) {
	r := newUSSDRig(t, 1)
	bad := strings.Replace(ussdBillFlow, `"text":"Acme bills"`, `"text":"`+strings.Repeat("A", 170)+`"`, 1)
	out := r.owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "bad", "definition": json.RawMessage(bad)})
	if !strings.Contains(toJSON(out["problems"]), "/trigger/config/screens/0/text") {
		t.Fatalf("problems: %v", out["problems"])
	}
	if st, _ := r.owner.do("POST", "/v1/workflows/"+out["id"].(string)+"/versions/1/publish", nil); st < 400 {
		t.Fatalf("published a bad menu: %d", st)
	}
	publishUSSD(t, r.owner, "bill", ussdBillFlow)
	twin := strings.Replace(ussdBillFlow, `"id":"wf_bill"`, `"id":"wf_twin"`, 1)
	id := r.owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "twin", "definition": json.RawMessage(twin)})["id"].(string)
	st, body := r.owner.do("POST", "/v1/workflows/"+id+"/versions/1/publish", nil)
	if st < 400 || !strings.Contains(toJSON(body), "already used") {
		t.Fatalf("second workflow on the same code: %d %v", st, body)
	}
}
