package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/remote"
)

// payrollaWorld is a world whose Payrolla sandbox is the fake, with
// Taskiem's sandbox app registered there, Taskiem's reconciler making
// subscriptions, and an edge that receives Payrolla's deliveries.
type payrollaWorld struct {
	*waWorld
	fake *payrollatest.Fake
}

func newPayrollaWorld(t *testing.T) *payrollaWorld {
	w := newWAWorld(t)
	w.srv.Egress = &egress.Guard{Blocked: func(netip.Addr) bool { return false }}
	f := payrollatest.New(payrollatest.Client{ID: "pyc_taskiem_sandbox", Secret: "pys_sandbox", Redirects: []string{w.base + "/v1/payrolla/callback"}})
	t.Cleanup(f.Close)
	if err := w.env.Registry.Register(payrolla.New(payrolla.Options{BaseURL: f.API(), TestURL: f.API(), TestAppURL: f.App()})); err != nil {
		t.Fatal(err)
	}
	w.srv.PublicURL = w.base
	w.srv.OAuthClients = map[string]api.OAuthClient{"payrolla_sandbox": {ID: "pyc_taskiem_sandbox", Secret: "pys_sandbox"}}
	h := &ingest.Handler{Store: w.env.Store, Secrets: w.env.Vault, Connections: w.env.Vault, Registry: w.env.Registry, Egress: w.env.Egress,
		Heard: api.PayrollaHeard(w.env.DB.App, w.env.Vault, slog.New(slog.DiscardHandler))}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", h))
	edge := httptest.NewServer(mux)
	t.Cleanup(edge.Close)
	w.srv.Remote = &remote.Reconciler{Pool: w.env.DB.App, Vault: w.env.Vault, Registry: w.env.Registry, Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
		HooksURL: edge.URL + "/hooks", Check: time.Nanosecond}
	// The company's runs (none yet), and this month's run (P4b-3); staff
	// and pay (P4b-2), whose tests take the same Team with NewTeam.
	f.ServeRuns()
	payrollatest.NewTeam(f)
	return &payrollaWorld{waWorld: w, fake: f}
}

// connect starts Connect Payrolla as c, goes through Payrolla's consent
// page, and comes back to Taskiem's callback; it returns where the browser
// ends up.
func (w *payrollaWorld) connect(t *testing.T, c *client, body map[string]any) (url.Values, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", w.base+"/v1/payroll/connect", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("connect: %d %v", resp.StatusCode, out)
	}
	var browser *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "taskiem_oauth" {
			if ck.Path != "/v1/payrolla/callback" || !ck.HttpOnly {
				t.Fatalf("binding cookie %+v", ck)
			}
			browser = ck
		}
	}
	consent, _ := url.Parse(out["url"].(string))
	q := consent.Query()
	if !strings.HasPrefix(consent.String(), w.fake.App()+"/oauth/authorize?") || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		q.Get("state") == "" || q.Get("redirect_uri") != w.base+"/v1/payrolla/callback" || !strings.Contains(q.Get("scope"), "payroll:approve") {
		t.Fatalf("consent %s", consent)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cr, err := noFollow.Get(consent.String())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(cr.Body)
	_ = cr.Body.Close()
	if cr.StatusCode != http.StatusFound {
		t.Fatalf("Payrolla's consent page: %d %s", cr.StatusCode, page)
	}
	cb, _ := http.NewRequest("GET", cr.Header.Get("Location"), nil)
	cb.AddCookie(&http.Cookie{Name: browser.Name, Value: browser.Value})
	back, err := noFollow.Do(cb)
	if err != nil {
		t.Fatal(err)
	}
	_ = back.Body.Close()
	loc, _ := url.Parse(back.Header.Get("Location"))
	if !strings.HasPrefix(loc.String(), w.base+"/payroll/settings?") {
		t.Fatalf("the callback sends the browser to %s", loc)
	}
	return loc.Query(), out
}

// An owner new to Payrolla connects and signs up in one step; the tokens
// are refreshed as they lapse; approvers link with one code, with each of
// Payrolla's refusals in words; revoking Taskiem in Payrolla ends the
// connection and tells the owner; reconnecting moves the same connection
// back; disconnecting from Taskiem revokes it at Payrolla.
func TestPayrollaConnectInOneStep(t *testing.T) {
	w := newPayrollaWorld(t)
	f := w.fake
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	staff := w.member(t, owner, "musa@amara.test", "admin")

	got := owner.must(200, "GET", "/v1/payroll/connection", nil)
	if got["connected"] != false || got["is_owner"] != true || !slices.Equal(anyStrings(got["modes"]), []string{"test"}) {
		t.Fatalf("before: %v", got)
	}
	// Only the owner connects.
	if e := staff.must(403, "POST", "/v1/payroll/connect", map[string]any{"mode": "test"}); e["code"] != "owner_only" {
		t.Fatalf("staff: %v", e)
	}
	// The owner says no in Payrolla: nothing changes.
	f.Deny = true
	back, _ := w.connect(t, owner, map[string]any{"mode": "test"})
	if back.Get("error") != "You didn't allow Taskiem in Payrolla. Nothing changed" {
		t.Fatalf("denied: %v", back)
	}
	f.Deny = false
	// A business not on Payrolla yet signs up in the same step.
	back, out := w.connect(t, owner, map[string]any{"mode": "test", "sign_up": true, "rc_number": "RC 1234567"})
	if back.Get("linked") == "" || out["reconnect"] != false {
		t.Fatalf("connected: %v %v", back, out)
	}
	connID := back.Get("linked")
	if su := f.SignUps(); len(su) != 1 || su[0].Get("company_name") != "Amara Foods" || su[0].Get("email") != "ngozi@amara.test" || su[0].Get("rc_number") != "RC 1234567" {
		t.Fatalf("sign-up fields %v", su)
	}
	got = owner.must(200, "GET", "/v1/payroll/connection", nil)
	conn := got["connection"].(map[string]any)
	if got["connected"] != true || conn["auth"] != "oauth" || conn["id"] != connID || conn["test"] != true ||
		conn["company"].(map[string]any)["name"] != "Amara Foods Ltd" || !slices.Contains(anyStrings(conn["scopes"]), "payroll:approve") {
		t.Fatalf("connected: %v", got)
	}
	// Taskiem's own subscription to every event, on the new tokens.
	pc := f.OAuthConn()
	if hooks := f.Webhooks(pc); len(hooks) != 1 || !slices.Equal(hooks[0].Events, []string{"*"}) {
		t.Fatalf("webhooks %v", hooks)
	}
	if page := owner.must(200, "GET", "/v1/payroll", nil); page["connected"] != true || page["auth"] != "oauth" {
		t.Fatalf("payroll page: %v", page)
	}
	// An hour later the access token has lapsed: it is refreshed once.
	f.ExpireAccessTokens()
	owner.must(200, "GET", "/v1/payroll", nil)
	owner.must(200, "GET", "/v1/payroll", nil)
	if f.Refreshes() != 1 {
		t.Fatalf("refreshes %d", f.Refreshes())
	}

	// Linking. Ngozi (the owner's email) first; Musa has no factor.
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000002", Name: "Musa Bello", Email: "musa@amara.test", Role: "APPROVER", CanApprove: true})
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000001", Name: "Ngozi Eze", Email: "ngozi@amara.test", Role: "OWNER", CanApprove: true, CanPay: true, CanConfirm: true})
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000003", Name: "Ada Obi", Email: "ada@amara.test", Role: "APPROVER", CanApprove: true, CanConfirm: true, Channel: "TOTP"})
	ngozi, musa, ada := "a0000000-0000-4000-8000-000000000001", "a0000000-0000-4000-8000-000000000002", "a0000000-0000-4000-8000-000000000003"
	list := owner.must(200, "GET", "/v1/payroll/approvers", nil)
	first := list["approvers"].([]any)[0].(map[string]any)
	if first["user_id"] != ngozi || first["suggested"] != true || list["me"] != nil || list["may_link"] != true {
		t.Fatalf("approvers: %v", list)
	}
	// Staff without payroll.approve cannot link.
	staff.must(403, "POST", "/v1/payroll/approvers/"+ngozi+"/link", map[string]any{"key": "s1"})
	sent := owner.must(202, "POST", "/v1/payroll/approvers/"+ngozi+"/link", map[string]any{"key": "sheet-1"})
	ch := sent["challenge"].(map[string]any)
	if sent["sent_to"] != "Payrolla sent a code to your phone ending 4567." || sent["safer"] != true {
		t.Fatalf("sent: %v", sent)
	}
	if e := owner.must(422, "POST", "/v1/payroll/approvers/"+ngozi+"/link/confirm", map[string]any{"challenge_id": ch["challenge_id"], "code": "000 000", "key": "sheet-1a"}); e["code"] != "code_invalid" ||
		e["error"] != "That code isn't right. Check it and type it again." {
		t.Fatalf("wrong code: %v", e)
	}
	code := f.Code(ngozi)
	owner.must(200, "POST", "/v1/payroll/approvers/"+ngozi+"/link/confirm", map[string]any{"challenge_id": ch["challenge_id"], "code": code[:3] + " " + code[3:], "key": "sheet-1b"})
	if e := owner.must(410, "POST", "/v1/payroll/approvers/"+ngozi+"/link/confirm", map[string]any{"challenge_id": ch["challenge_id"], "code": code, "key": "sheet-1c"}); e["code"] != "code_used" {
		t.Fatalf("used: %v", e)
	}
	list = owner.must(200, "GET", "/v1/payroll/approvers", nil)
	me := list["me"].(map[string]any)
	if me["user_id"] != ngozi || me["linked"].(map[string]any)["name"] != "Owner" {
		t.Fatalf("linked: %v", list)
	}
	// Every write said who did it: the owner's own account id.
	ownerID := personID(t, owner)
	for _, wr := range f.Writes() {
		if wr.ActingUser != payrolla.ActingUser(ownerID) || wr.Key == "" || strings.Contains(wr.Key, "sheet") {
			t.Fatalf("write %+v", wr)
		}
	}
	// One Payrolla account per person.
	if e := owner.must(409, "POST", "/v1/payroll/approvers/"+ada+"/link", map[string]any{"key": "sheet-2"}); e["code"] != "already_linked" {
		t.Fatalf("already linked: %v", e)
	}
	// A person with payroll.approve links themselves; only the owner
	// unlinks someone else.
	owner.must(201, "PUT", "/v1/roles/payroll", map[string]any{"permissions": []string{"payroll.read", "payroll.approve"}})
	ada1 := w.member(t, owner, "ada@amara.test", "payroll")
	sent = ada1.must(202, "POST", "/v1/payroll/approvers/"+ada+"/link", map[string]any{"key": "a-1"})
	if sent["sent_to"] != "Open your authenticator app for the code." || sent["safer"] != false {
		t.Fatalf("TOTP: %v", sent)
	}
	ada1.must(200, "POST", "/v1/payroll/approvers/"+ada+"/link/confirm", map[string]any{"challenge_id": sent["challenge"].(map[string]any)["challenge_id"], "code": f.Code(ada), "key": "a-2"})
	if e := ada1.must(403, "DELETE", "/v1/payroll/approvers/"+ngozi+"/link?key=a-3", nil); e["code"] != "owner_only" {
		t.Fatalf("unlink someone else: %v", e)
	}
	owner.must(200, "DELETE", "/v1/payroll/approvers/"+ada+"/link?key=o-1", nil)
	owner.must(200, "DELETE", "/v1/payroll/approvers/"+ngozi+"/link?key=o-2", nil)
	// No factor, then too many codes, then an expired code.
	if e := owner.must(409, "POST", "/v1/payroll/approvers/"+musa+"/link", map[string]any{"key": "m-1"}); e["code"] != "no_factor" {
		t.Fatalf("no factor: %v", e)
	}
	var last map[string]any
	for i := range 4 {
		last = owner.must(202, "POST", "/v1/payroll/approvers/"+ngozi+"/link", map[string]any{"key": "n-" + string(rune('a'+i))})
	}
	if e := owner.must(429, "POST", "/v1/payroll/approvers/"+ngozi+"/link", map[string]any{"key": "n-z"}); e["code"] != "too_many_codes" ||
		e["error"] != "Payrolla has sent you too many codes. Try again in 10 minutes." {
		t.Fatalf("too many: %v", e)
	}
	now := time.Now()
	f.Now = func() time.Time { return now.Add(6 * time.Minute) }
	if e := owner.must(410, "POST", "/v1/payroll/approvers/"+ngozi+"/link/confirm", map[string]any{"challenge_id": last["challenge"].(map[string]any)["challenge_id"], "code": f.Code(ngozi), "key": "n-y"}); e["code"] != "code_expired" {
		t.Fatalf("expired: %v", e)
	}
	f.Now = time.Now

	// The owner revokes Taskiem in Payrolla: company.disconnected ends the
	// connection, and the owners are told once.
	f.RevokeByOwner()
	got = owner.must(200, "GET", "/v1/payroll/connection", nil)
	if got["connected"] != false || got["ended"] != true {
		t.Fatalf("after revoking: %v", got)
	}
	if page := owner.must(200, "GET", "/v1/payroll", nil); page["ended"] != true || page["is_owner"] != true {
		t.Fatalf("payroll page after revoking: %v", page)
	}
	if e := ada1.must(409, "GET", "/v1/payroll/approvers", nil); e["error"] != "Payroll isn't connected right now. The owner has been told." {
		t.Fatalf("staff after revoking: %v", e)
	}
	var told int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM alerts WHERE kind = 'payrolla_disconnected'`).Scan(&told); err != nil || told != 1 {
		t.Fatalf("owners told %d times: %v", told, err)
	}

	// Reconnect: the same connection, on new tokens, with its webhook again.
	back, out = w.connect(t, owner, map[string]any{})
	if back.Get("linked") != connID || out["reconnect"] != true {
		t.Fatalf("reconnected: %v %v", back, out)
	}
	if hooks := f.Webhooks(f.OAuthConn()); len(hooks) != 1 {
		t.Fatalf("webhooks after reconnecting %v", hooks)
	}
	owner.must(200, "GET", "/v1/payroll", nil)

	// A refresh token Payrolla refuses ends the connection.
	f.EndTokenChains()
	f.ExpireAccessTokens()
	if e := owner.must(409, "GET", "/v1/payroll", nil); e["code"] != "reconnect" {
		t.Fatalf("refused refresh: %v", e)
	}
	if got := owner.must(200, "GET", "/v1/payroll/connection", nil); got["ended"] != true {
		t.Fatalf("after a refused refresh: %v", got)
	}

	// Reconnect, then disconnect from Taskiem: the tokens are revoked at
	// Payrolla.
	back, _ = w.connect(t, owner, map[string]any{})
	if back.Get("linked") != connID {
		t.Fatalf("reconnected: %v", back)
	}
	owner.must(204, "DELETE", "/v1/connections/"+connID+"?force=true", nil)
	if f.OAuthConn() != nil {
		t.Fatal("disconnecting did not revoke Taskiem at Payrolla")
	}
}

// A key connection keeps working; its owner reconnects in one step and the
// same connection moves onto the tokens. company.disconnected for the old
// key does not end the new connection.
func TestPayrollaKeyConnectionAndReconnect(t *testing.T) {
	w := newPayrollaWorld(t)
	f := w.fake
	f.SetWorkers(payrollatest.Worker{ID: zainabE, First: "Zainab", Last: "Bello", GrossKobo: 45000000})
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	key := f.AddKey("payroll:read", "employees:read")
	conn := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "payrolla@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"api_key": key}})
	page := owner.must(200, "GET", "/v1/payroll", nil)
	if page["connected"] != true || page["auth"] != "key" || page["is_owner"] != true {
		t.Fatalf("key connection: %v", page)
	}
	// payrolla-tell-staff keeps sending a key connection's linked staff
	// Payrolla's payslip link: Taskiem hears no payslip.issued of its own.
	tenant := uuid.MustParse(tenantOf(t, owner))
	if got := w.templateEmployee(t, tenant, "main"); got["taskiem_payslips"] != false {
		t.Fatalf("a key connection's employee: %v", got)
	}
	got := owner.must(200, "GET", "/v1/payroll/connection", nil)
	if got["connection"].(map[string]any)["auth"] != "key" {
		t.Fatalf("connection: %v", got)
	}
	// A key without Approve and pay cannot list approvers: the owner is
	// told to reconnect.
	if e := owner.must(409, "GET", "/v1/payroll/approvers", nil); e["code"] != "scope_required" || !strings.Contains(e["error"].(string), "Reconnect Payrolla") {
		t.Fatalf("approvers on a key: %v", e)
	}
	back, out := w.connect(t, owner, map[string]any{})
	if back.Get("linked") != conn["id"] || out["reconnect"] != true {
		t.Fatalf("reconnected: %v %v", back, out)
	}
	got = owner.must(200, "GET", "/v1/payroll/connection", nil)
	if c := got["connection"].(map[string]any); c["auth"] != "oauth" || c["status"] != "active" || c["test"] != true {
		t.Fatalf("after reconnecting: %v", got)
	}
	if hooks := f.Webhooks(f.OAuthConn()); len(hooks) != 1 {
		t.Fatalf("webhooks on the new tokens: %v", hooks)
	}
	owner.must(200, "GET", "/v1/payroll/approvers", nil)
	// Connected in one step, Taskiem gives linked staff its own payslip
	// card (payslip.issued), and the template no longer sends them the link.
	if got := w.templateEmployee(t, tenant, "main"); got["taskiem_payslips"] != true {
		t.Fatalf("a one-step connection's employee: %v", got)
	}
	// The owner removes the old key in Payrolla, as Taskiem asks: the old
	// key's last event names it, not this connection.
	body, _ := json.Marshal(map[string]any{"id": "evt_old", "type": "company.disconnected", "data": map[string]any{"key_id": "some-old-key", "reason": "revoked_by_owner"}})
	api.PayrollaHeard(w.env.DB.App, w.env.Vault, slog.New(slog.DiscardHandler))(context.Background(),
		ingest.HeardEvent{Tenant: tenant, Environment: "prod", Connector: "payrolla", Trigger: "connection_event", Connection: "main", Event: "company.disconnected", Body: body})
	if got := owner.must(200, "GET", "/v1/payroll/connection", nil); got["connected"] != true {
		t.Fatalf("the old key's end ended the new connection: %v", got)
	}
}

// templateEmployee runs the Payrolla connector's get_employee on the
// business's stored connection, as a template's step does
// (payrolla-tell-staff's "Find the employee"), for Zainab Bello.
func (w *payrollaWorld) templateEmployee(t *testing.T, tenant uuid.UUID, name string) map[string]any {
	t.Helper()
	ctx := context.Background()
	var c *connector.Connector
	for _, x := range w.env.Registry.List() {
		if x.Manifest.ID == "payrolla" {
			c = x
		}
	}
	if c == nil {
		t.Fatal("no payrolla connector")
	}
	creds, err := w.env.Vault.Credentials(ctx, tenant, "prod", "payrolla", name)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Actions["get_employee"].Execute(ctx, connector.Request{Input: map[string]any{"employee_id": zainabE}, Credentials: creds, HTTP: http.DefaultClient,
		Rotate: func(ctx context.Context, fn connector.RotateFunc) (map[string]string, error) {
			return w.env.Vault.RotateCredentials(ctx, tenant, "prod", "payrolla", name, fn)
		}})
	if err != nil {
		t.Fatal(err)
	}
	out := res.Output.(map[string]any)
	if emp, _ := out["employee"].(map[string]any); emp == nil || emp["id"] != zainabE {
		t.Fatalf("employee: %v", out)
	}
	return out
}
