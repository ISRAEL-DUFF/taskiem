package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// fakePaystackRail is Paystack enough for payroll: a balance, name
// enquiry, recipients, transfers kept by reference (a repeat answers the
// first), and their lookup.
type fakePaystackRail struct {
	mu        sync.Mutex
	transfers map[string]map[string]any // reference -> transfer
	sends     map[string]int            // reference -> POST /transfer calls
}

func newFakePaystackRail(t *testing.T) (*fakePaystackRail, *httptest.Server) {
	t.Helper()
	f := &fakePaystackRail{transfers: map[string]map[string]any{}, sends: map[string]int{}}
	names := map[string]string{"0690000031": "OBI ADA", "0123456789": "BOLA ADE", "0999999999": "SOMEONE ELSE"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ok := func(data any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "message": "ok", "data": data})
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/balance":
			ok([]any{map[string]any{"currency": "NGN", "balance": 90000000}})
		case r.URL.Path == "/bank/resolve":
			n := r.URL.Query().Get("account_number")
			ok(map[string]any{"account_number": n, "account_name": names[n]})
		case r.URL.Path == "/transferrecipient":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			sum := sha256.Sum256([]byte(in["account_number"].(string))) // opaque, as Paystack's codes are
			ok(map[string]any{"recipient_code": "RCP_" + hex.EncodeToString(sum[:6]), "name": in["name"]})
		case r.URL.Path == "/transfer" && r.Method == http.MethodPost:
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			ref := in["reference"].(string)
			f.sends[ref]++
			if _, dup := f.transfers[ref]; !dup {
				f.transfers[ref] = map[string]any{"reference": ref, "amount": in["amount"], "recipient": in["recipient"], "status": "pending", "transfer_code": "TRF_" + ref[4:10]}
			}
			ok(f.transfers[ref])
		case strings.HasPrefix(r.URL.Path, "/transfer/verify/"):
			if t, found := f.transfers[strings.TrimPrefix(r.URL.Path, "/transfer/verify/")]; found {
				ok(t)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":false,"message":"Transfer not found"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":false,"message":"no route"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

// Run payroll on the real stack (spec 5.1): from the Start page's template
// with data tables and a Paystack connection, the payroll waits for
// approval, pays each matched person once under their own key, ends on
// Paystack's events, records every payment and the payroll, and a
// reversal is recorded without paying anyone again.
func TestRunPayrollEndToEnd(t *testing.T) {
	w := newWorld(t)
	w.env.CallTimeout = 5 * time.Second // the fake data tables do real work
	f, ps := newFakePaystackRail(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := builtin.Register(w.env.Registry, builtin.Options{PaystackURL: ps.URL, XenibaseURL: pg.URL}); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	owner := w.tenant(t, "Adunni Bakes", "ada@adunni.test")
	cfo := addMember(t, w, owner, "cfo", "approver")
	tenant := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})

	// The template makes its tables; the staff go in the staff table.
	made := owner.must(201, "POST", "/v1/templates/run-payroll/instantiate", map[string]any{"params": map[string]any{"business_name": "Adunni Bakes", "approver_role": "approver"}})
	if probs := made["problems"].([]any); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	wf := made["id"].(string)
	tables := map[string]string{}
	for _, tb := range owner.must(200, "GET", "/v1/tables", nil)["tables"].([]any) {
		m := tb.(map[string]any)
		tables[m["name"].(string)] = m["id"].(string)
	}
	if tables["staff"] == "" || tables["payroll_runs"] == "" || tables["payroll_lines"] == "" {
		t.Fatalf("tables %v, made %s", tables, toJSON(made["tables"]))
	}
	for i, p := range []map[string]any{
		{"name": "Ada Obi", "net_pay": 300000, "bank_code": "044", "account_number": "0690000031", "account_name": "Ada Obi", "active": true},
		{"name": "Bola Ade", "net_pay": 200000, "bank_code": "058", "account_number": "0123456789", "account_name": "Bola Ade", "active": true},
		{"name": "Chidi Eze", "net_pay": 150000, "bank_code": "058", "account_number": "0999999999", "account_name": "Chidi Eze", "active": true},
		{"name": "Dayo Ojo", "net_pay": 100000, "bank_code": "058", "account_number": "0123456780", "account_name": "Dayo Ojo", "active": false},
	} {
		owner.must(201, "POST", "/v1/tables/"+tables["staff"]+"/rows", map[string]any{"row": p}, "Idempotency-Key", "staff-"+string(rune('a'+i)))
	}
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	started := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"period": "2026-10"}})
	run := started["run_id"].(string)
	ref := runtime.RunRef{ID: uuid.MustParse(run), TenantID: tenant}
	w.env.Drain(t)
	f.mu.Lock()
	if len(f.sends) != 0 {
		t.Fatal("money moved before approval")
	}
	f.mu.Unlock()
	inbox := toJSON(cfo.must(200, "GET", "/v1/approvals", nil))
	if !strings.Contains(inbox, `"people":2`) || !strings.Contains(inbox, `"total_naira":500000`) || !strings.Contains(inbox, "Chidi Eze: the bank has this account as SOMEONE ELSE") {
		t.Fatalf("approval: %s", inbox)
	}
	if st, _ := owner.do("POST", "/v1/approvals/"+run+"/approve", map[string]any{"decision": "approved"}); st != 403 {
		t.Errorf("the author approved their own payroll: %d", st)
	}
	cfo.must(200, "POST", "/v1/approvals/"+run+"/approve", map[string]any{"decision": "approved"})

	// Each matched person is paid once, in Paystack's own words.
	defer func() {
		if t.Failed() {
			h, _ := w.env.Store.RunHistory(context.Background(), ref)
			for _, ev := range h {
				if ev.Type == "StepFailed" || ev.Type == "RunFailed" {
					t.Logf("%s %s %s", ev.Type, ev.StepID, ev.Payload)
				}
			}
		}
	}()
	rt.WaitFor(t, 20*time.Second, "the transfers", func() bool {
		w.env.Drain(t)
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.transfers) == 2
	})
	f.mu.Lock()
	var refs []string
	for r, n := range f.sends {
		if n != 1 {
			t.Errorf("reference %s sent %d times", r, n)
		}
		refs = append(refs, r)
	}
	f.mu.Unlock()

	// Paystack's events settle both; the run then records and watches.
	for _, r := range refs {
		if woke, err := w.env.Store.DeliverSignal(context.Background(), tenant, "prod", "paystack@1:payout.settled", r,
			map[string]any{"event": "payout.settled", "fields": map[string]any{"payout_id": r}}); err != nil || len(woke) != 1 {
			t.Fatalf("settled %s: %v %v", r, woke, err)
		}
	}
	rt.WaitFor(t, 20*time.Second, "the records", func() bool {
		w.env.Drain(t)
		return strings.Contains(toJSON(owner.must(200, "GET", "/v1/tables/"+tables["payroll_runs"]+"/rows", nil)), `"period":"2026-10"`)
	})
	runs := toJSON(owner.must(200, "GET", "/v1/tables/"+tables["payroll_runs"]+"/rows", nil))
	lines := toJSON(owner.must(200, "GET", "/v1/tables/"+tables["payroll_lines"]+"/rows", nil))
	if !strings.Contains(runs, `"paid":2`) || !strings.Contains(runs, `"approved_by"`) || strings.Count(lines, `"status":"paid"`) != 2 {
		t.Fatalf("records: runs %s\nlines %s", runs, lines)
	}
	if st := w.env.Status(t, ref); st == "completed" || st == "failed" {
		t.Fatalf("the run ended while it should watch for reversals: %s", st)
	}

	// A reversal is recorded; nobody is paid again.
	if woke, err := w.env.Store.DeliverSignal(context.Background(), tenant, "prod", "paystack@1:payout.reversed", refs[0],
		map[string]any{"event": "payout.reversed", "fields": map[string]any{"payout_id": refs[0], "reason": "Account closed"}}); err != nil || len(woke) != 1 {
		t.Fatalf("reversed: %v %v", woke, err)
	}
	rt.WaitFor(t, 20*time.Second, "the reversal", func() bool {
		w.env.Drain(t)
		return strings.Contains(toJSON(owner.must(200, "GET", "/v1/tables/"+tables["payroll_lines"]+"/rows", nil)), `"status":"reversed"`)
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.transfers) != 2 {
		t.Errorf("the watch paid someone: %v", f.transfers)
	}
	for r, n := range f.sends {
		if n != 1 {
			t.Errorf("reference %s sent %d times", r, n)
		}
	}
	// Personal data stays sealed in the run's history.
	for _, ev := range owner.must(200, "GET", "/v1/runs/"+run, nil)["events"].([]any) {
		if s := toJSON(ev); strings.Contains(s, "0690000031") {
			t.Errorf("an account number is in the run's history in plain text: %s", s)
		}
	}
}

// The Paystack-only salary template is replaced by Run payroll: it leaves
// the gallery, workflows made from it keep working, and each is offered
// the upgrade.
func TestSupersededTemplateOffersTheUpgrade(t *testing.T) {
	w := newWorld(t)
	if err := builtin.Register(w.env.Registry, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Adunni Bakes", "ada@adunni.test")
	if strings.Contains(toJSON(owner.must(200, "GET", "/v1/templates", nil)), `"id":"salary-payout-approval"`) {
		t.Error("the replaced template is still offered")
	}
	old := owner.must(200, "GET", "/v1/templates/salary-payout-approval", nil)
	if old["superseded_by"] != "run-payroll" {
		t.Fatalf("template: %v", old["superseded_by"])
	}
	made := owner.must(201, "POST", "/v1/templates/salary-payout-approval/instantiate", map[string]any{"params": map[string]any{
		"business_name": "Adunni Bakes", "spreadsheet_id": "1StAfFsHeEtIdEnTiFiEr0123456789abcd"}})
	got := owner.must(200, "GET", "/v1/workflows/"+made["id"].(string), nil)
	if up, _ := got["upgrade"].(map[string]any); up["template"] != "run-payroll" || up["title"] == "" {
		t.Fatalf("upgrade: %v", got["upgrade"])
	}
}
