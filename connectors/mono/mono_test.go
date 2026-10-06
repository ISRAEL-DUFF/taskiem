package mono

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

const (
	acc = "661d759280dbf646242634cc"
	mmc = "mmc_682b9c5803c0b736078889a3"
	ref = "AbCdEfGhIjKlMnOpQrStUv"
)

func call(t *testing.T, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	return New(Options{BaseURL: srv.URL}).Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(),
		IdempotencyKey: ref, Attempt: 1, Credentials: map[string]string{"secret_key": "test_sk_abc", "webhook_secret": "whsec_1"}})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func kind(t *testing.T, err error, want effects.ErrorKind, what string) {
	t.Helper()
	if got := effects.Classify(err); err == nil || got != want {
		t.Errorf("%s: want %s, got %v (%v)", what, want, got, err)
	}
}

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("mono@1")
	if !ok {
		t.Fatal("mono@1 not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 1 || h[0] != "api.withmono.com" {
		t.Errorf("hosts %v", h)
	}
	for name := range c.Manifest.Actions {
		if c.Actions[name] == nil {
			t.Errorf("action %s has no handler", name)
		}
	}
	if len(c.Actions) != len(c.Manifest.Actions) {
		t.Errorf("%d handlers for %d actions", len(c.Actions), len(c.Manifest.Actions))
	}
}

func TestClasses(t *testing.T) {
	m := New(Options{}).Manifest
	want := map[string]effects.Class{
		"initiate_payment": effects.ReconcilableWrite, "create_mandate": effects.ReconcilableWrite, "initiate_mandate": effects.ReconcilableWrite,
		"debit_mandate": effects.ReconcilableWrite, "unlink_account": effects.UnsafeWrite, "exchange_token": effects.UnsafeWrite,
		"cancel_mandate": effects.UnsafeWrite, "create_customer": effects.UnsafeWrite, "get_identity": effects.Read, "request_creditworthiness": effects.Read,
	}
	for a, c := range want {
		if m.Actions[a].Class != c {
			t.Errorf("%s is %s, want %s", a, m.Actions[a].Class, c)
		}
	}
	for _, a := range []string{"initiate_payment", "debit_mandate"} {
		if m.Actions[a].Reconcile != "verify_payment" || m.Actions[a].Idempotency.Field != "reference" {
			t.Errorf("%s: reconcile %q", a, m.Actions[a].Reconcile)
		}
	}
	if m.Actions["create_mandate"].Reconcile != "get_mandate" {
		t.Error("create_mandate reconciles with get_mandate")
	}
}

func TestPIIDeclared(t *testing.T) {
	m := New(Options{}).Manifest
	want := map[string]map[string]string{
		"initiate_account_linking": {"customer_name": "name", "customer_email": "email"},
		"request_creditworthiness": {"bvn": "bvn"},
		"initiate_payment":         {"customer_name": "name", "customer_email": "email", "customer_phone": "phone", "customer_address": "address", "customer_bvn": "bvn"},
		"create_customer":          {"first_name": "name", "last_name": "name", "email": "email", "phone": "phone", "address": "address", "bvn": "bvn"},
		"create_mandate":           {"account_number": "account_number"},
		"initiate_mandate":         {"account_number": "account_number"},
		"debit_mandate":            {"beneficiary_account_number": "account_number"},
	}
	for action, fields := range want {
		got := map[string]string{}
		for _, p := range m.Actions[action].PII {
			got[p.Field] = p.Category
		}
		for f, cat := range fields {
			if got[f] != cat {
				t.Errorf("%s.%s: pii %q, want %q", action, f, got[f], cat)
			}
		}
	}
}

func TestLinking(t *testing.T) {
	r, err := call(t, "initiate_account_linking", map[string]any{"customer_name": "Samuel Olamide", "customer_email": "samuel@neem.com",
		"ref": "99008877TEST", "redirect_url": "https://mono.co"}, "initiate_linking")
	if err != nil || out(r)["mono_url"] != "https://link.mono.co/ALGSTO222222WE" || out(r)["ref"] != "99008877TEST" {
		t.Errorf("initiate: %v %v", r.Output, err)
	}
	_, err = call(t, "initiate_account_linking", map[string]any{"redirect_url": "https://mono.co"})
	kind(t, err, effects.KindFatal, "no customer")
	r, err = call(t, "initiate_reauthorisation", map[string]any{"account_id": acc, "redirect_url": "https://mono.co"}, "initiate_reauth")
	if err != nil || out(r)["mono_url"] != "https://link.mono.co/ALH0IX10JO89" {
		t.Errorf("reauth: %v %v", r.Output, err)
	}
	r, err = call(t, "exchange_token", map[string]any{"code": "code_xyz"}, "exchange_token")
	if err != nil || out(r)["account_id"] != acc {
		t.Errorf("exchange: %v %v", r.Output, err)
	}
	_, err = call(t, "exchange_token", map[string]any{"code": "code_xyz"}, "exchange_token_invalid")
	kind(t, err, effects.KindFatal, "invalid code")
	if !strings.Contains(err.Error(), "Invalid authorization code") {
		t.Errorf("message: %v", err)
	}
	r, err = call(t, "unlink_account", map[string]any{"account_id": acc}, "unlink")
	if err != nil || out(r)["unlinked"] != true {
		t.Errorf("unlink: %v %v", r.Output, err)
	}
}

func TestAccountData(t *testing.T) {
	r, err := call(t, "get_account", map[string]any{"account_id": acc, "realtime": true}, "account")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["balance"] != int64(73573) || o["bvn_last4"] != "6115" || o["bank_code"] != "058" || o["data_status"] != "AVAILABLE" || len(o["retrieved_data"].([]any)) != 3 {
		t.Errorf("account %v", o)
	}
	r, err = call(t, "get_balance", map[string]any{"account_id": acc}, "balance")
	if err != nil || out(r)["balance"] != int64(35232) {
		t.Errorf("balance %v %v", r.Output, err)
	}
	_, err = call(t, "get_balance", map[string]any{"account_id": acc}, "balance_fraction")
	kind(t, err, effects.KindUnknownOutcome, "fractional kobo balance")
	r, err = call(t, "get_identity", map[string]any{"account_id": acc}, "identity")
	if err != nil || out(r)["bvn"] != "22000000012" || out(r)["date_of_birth"] != "1997-08-07" || out(r)["verified"] != true {
		t.Errorf("identity %v %v", r.Output, err)
	}
	r, err = call(t, "list_transactions", map[string]any{"account_id": acc, "start": "01-10-2023", "end": "31-12-2023", "type": "debit", "limit": 2}, "transactions")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["total"] != int64(307) || o["has_more"] != true || len(o["transactions"].([]any)) != 2 {
		t.Errorf("transactions %v", o)
	} else if tx := o["transactions"].([]any)[1].(map[string]any); tx["amount"] != int64(1000) || tx["balance"] != int64(2000) {
		t.Errorf("transaction %v", tx)
	}
	_, err = call(t, "list_transactions", map[string]any{"account_id": acc, "start": "01-10-2023"})
	kind(t, err, effects.KindFatal, "start without end")
}

func TestAccountErrors(t *testing.T) {
	_, err := call(t, "get_account", map[string]any{"account_id": acc}, "account_not_found")
	kind(t, err, effects.KindFatal, "unknown account")
	if !strings.Contains(err.Error(), "no such account") {
		t.Errorf("message: %v", err)
	}
	_, err = call(t, "get_account", map[string]any{"account_id": acc}, "account_server_error")
	kind(t, err, effects.KindUnknownOutcome, "502")
	_, err = call(t, "get_account", map[string]any{"account_id": acc}, "account_rate_limited")
	kind(t, err, effects.KindRetryable, "429")
	_, err = call(t, "get_account", map[string]any{})
	kind(t, err, effects.KindFatal, "no account id")
	// No key: refused before anything is sent.
	srv := fixture.Serve(t)
	_, err = New(Options{BaseURL: srv.URL}).Actions["get_balance"].Execute(context.Background(), connector.Request{Input: map[string]any{"account_id": acc}, HTTP: srv.Client()})
	kind(t, err, effects.KindFatal, "no secret key")
}

func TestStatementsIncomeCredit(t *testing.T) {
	r, err := call(t, "get_statement", map[string]any{"account_id": acc, "months": 6}, "statement_json")
	if err != nil || len(out(r)["transactions"].([]any)) != 1 || out(r)["transactions"].([]any)[0].(map[string]any)["balance"] != int64(0) {
		t.Errorf("statement %v %v", r.Output, err)
	}
	r, err = call(t, "get_statement", map[string]any{"account_id": acc, "months": 12, "output": "pdf"}, "statement_pdf")
	if err != nil || out(r)["job_id"] != "2ojHeRva4Bfk9vvIuCuO" || out(r)["status"] != "BUILDING" {
		t.Errorf("pdf %v %v", r.Output, err)
	}
	r, err = call(t, "get_statement_pdf", map[string]any{"account_id": acc, "job_id": "2ojHeRva4Bfk9vvIuCuO"}, "statement_job")
	if err != nil || out(r)["status"] != "BUILT" || !strings.HasSuffix(out(r)["pdf_url"].(string), ".pdf") {
		t.Errorf("job %v %v", r.Output, err)
	}
	_, err = call(t, "get_statement", map[string]any{"account_id": acc, "months": 13})
	kind(t, err, effects.KindFatal, "13 months")
	r, err = call(t, "request_income", map[string]any{"account_id": acc, "months": 6}, "income")
	if err != nil || out(r)["accepted"] != true {
		t.Errorf("income %v %v", r.Output, err)
	}
	_, err = call(t, "request_income", map[string]any{"account_id": acc}, "income_not_covered")
	kind(t, err, effects.KindFatal, "plan does not cover income")
	r, err = call(t, "get_income_records", map[string]any{"account_id": acc}, "income_records")
	if err != nil || out(r)["total"] != int64(17) || out(r)["has_more"] != true {
		t.Errorf("records %v %v", r.Output, err)
	} else if rec := out(r)["records"].([]any)[0].(map[string]any)["income"].(map[string]any); rec["number_of_income_streams"] != int64(1) {
		t.Errorf("record %v", rec)
	}
	r, err = call(t, "request_creditworthiness", map[string]any{"account_id": acc, "bvn": "22000000012", "principal": 30000000, "interest_rate": 5.0,
		"term": 12, "run_credit_check": true}, "creditworthiness")
	if err != nil || out(r)["accepted"] != true {
		t.Errorf("creditworthiness %v %v", r.Output, err)
	}
	_, err = call(t, "request_creditworthiness", map[string]any{"account_id": acc, "bvn": "22000000012", "principal": 300.5, "interest_rate": 5.0,
		"term": 12, "run_credit_check": true})
	kind(t, err, effects.KindFatal, "fractional principal")
	r, err = call(t, "list_banks", nil, "banks")
	if err != nil || len(out(r)["banks"].([]any)) != 2 || out(r)["banks"].([]any)[1].(map[string]any)["direct_debit"] != true {
		t.Errorf("banks %v %v", r.Output, err)
	}
}

var payIn = map[string]any{"amount": 21000, "description": "Ticket", "reference": ref, "redirect_url": "https://mono.co",
	"customer_name": "Samuel Olamide", "customer_email": "samuel@neem.com", "customer_bvn": "22110033445"}

func TestDirectPay(t *testing.T) {
	r, err := call(t, "initiate_payment", payIn, "payment_initiated")
	if err != nil || out(r)["mono_url"] != "https://checkout.mono.co/ODW2QV0WLIDG" || out(r)["amount"] != int64(21000) || out(r)["status"] != "initiated" {
		t.Errorf("initiate %v %v", r.Output, err)
	}
	// A resend after a lost response: Mono refuses the reference and the
	// connector reports the payment made under it.
	r, err = call(t, "initiate_payment", payIn, "payment_duplicate", "verify_successful")
	if err != nil || out(r)["status"] != "successful" || out(r)["reference"] != ref {
		t.Errorf("duplicate %v %v", r.Output, err)
	}
	_, err = call(t, "initiate_payment", payIn, "payment_duplicate", "verify_not_found")
	kind(t, err, effects.KindFatal, "refused and absent")
	small := map[string]any{"amount": 19999, "description": "x", "reference": ref}
	_, err = call(t, "initiate_payment", small)
	kind(t, err, effects.KindFatal, "below NGN 200")
	_, err = call(t, "initiate_payment", map[string]any{"amount": 20000.5, "description": "x", "reference": ref})
	kind(t, err, effects.KindFatal, "fractional kobo")
	r, err = call(t, "verify_payment", map[string]any{"reference": ref}, "verify_successful")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["fee"] != int64(5500) || o["account_number"] != "0123456789" || o["bank_code"] != "035" || o["channel"] != "mandate" {
		t.Errorf("verify %v", o)
	}
	_, err = call(t, "verify_payment", map[string]any{"reference": ref}, "verify_not_found")
	if !errors.Is(err, connector.ErrNotFound) || effects.Classify(err) != effects.KindFatal {
		t.Errorf("not found must be not_found for reconcile: %v", err)
	}
	// Reconcile is called with only the reference.
	r, err = New(Options{}).Actions["verify_payment"].Execute(context.Background(), connector.Request{Input: map[string]any{}})
	kind(t, err, effects.KindFatal, "no reference")
	_ = r
}

var mandateIn = map[string]any{"customer_id": "68274fb5565971e625b9115c", "mandate_type": "emandate", "debit_type": "variable", "amount": 5000000,
	"reference": ref, "account_number": "0123456789", "bank_code": "035", "description": "Loan repayment", "start_date": "2025-05-28", "end_date": "2026-05-29"}

func TestMandates(t *testing.T) {
	r, err := call(t, "create_customer", map[string]any{"first_name": "Samuel", "last_name": "Olamide", "email": "samuel@olamide.com",
		"phone": "08012345678", "address": "23 shittu animashaun street", "bvn": "22110033445"}, "customer")
	if err != nil || out(r)["id"] != "6578295bbf09b0a505bc567c" {
		t.Errorf("customer %v %v", r.Output, err)
	}
	_, err = call(t, "create_customer", map[string]any{"first_name": "Samuel", "last_name": "Olamide", "email": "samuel@olamide.com",
		"phone": "08012345678", "address": "23 shittu animashaun street", "bvn": "22110033445"}, "customer_exists")
	kind(t, err, effects.KindFatal, "customer exists")
	r, err = call(t, "create_mandate", mandateIn, "mandate_created")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["id"] != mmc || o["status"] != "initiated" || o["amount"] != int64(5000000) || !strings.Contains(o["message"].(string), "N50.00") ||
		o["transfer_destinations"].([]any)[0].(map[string]any)["account_number"] != "9020025928" {
		t.Errorf("mandate %v", o)
	}
	_, err = call(t, "create_mandate", mandateIn, "mandate_no_customer")
	kind(t, err, effects.KindFatal, "no customer")
	noAcct := map[string]any{}
	for k, v := range mandateIn {
		noAcct[k] = v
	}
	delete(noAcct, "bank_code")
	_, err = call(t, "create_mandate", noAcct)
	kind(t, err, effects.KindFatal, "no bank code")
	hosted := map[string]any{"customer_id": "65eb623b0000900009e5c1f21cd", "mandate_type": "emandate", "debit_type": "variable", "amount": 9190030,
		"reference": ref, "description": "Repayment", "start_date": "2024-03-29", "end_date": "2024-08-04", "redirect_url": "https://mono.co"}
	r, err = call(t, "initiate_mandate", hosted, "mandate_initiated")
	if err != nil || out(r)["id"] != mmc || out(r)["mono_url"] != "https://authorise.mono.co/RD3044259" {
		t.Errorf("hosted %v %v", r.Output, err)
	}
	_, err = call(t, "initiate_mandate", hosted, "mandate_validation")
	kind(t, err, effects.KindFatal, "validation")
	if !strings.Contains(err.Error(), "at least today") {
		t.Errorf("validation detail missing: %v", err)
	}
	// Reconcile: by the reference the engine used.
	r, err = call(t, "get_mandate", map[string]any{"reference": ref}, "mandate_by_reference")
	if err != nil || out(r)["status"] != "approved" || out(r)["bank_name"] != "Kuda Bank" || out(r)["balance"] != int64(80030) {
		t.Errorf("get %v %v", r.Output, err)
	}
	_, err = call(t, "get_mandate", map[string]any{"reference": ref}, "mandate_not_found")
	if !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
	r, err = call(t, "pause_mandate", map[string]any{"mandate_id": mmc}, "mandate_pause")
	if err != nil || out(r)["status"] != "paused" {
		t.Errorf("pause %v %v", r.Output, err)
	}
	r, err = call(t, "pause_mandate", map[string]any{"mandate_id": mmc}, "mandate_already_paused")
	if err != nil || out(r)["status"] != "paused" {
		t.Errorf("already paused counts as done: %v %v", r.Output, err)
	}
	r, err = call(t, "reinstate_mandate", map[string]any{"mandate_id": mmc}, "mandate_reinstate")
	if err != nil || out(r)["status"] != "active" {
		t.Errorf("reinstate %v %v", r.Output, err)
	}
	r, err = call(t, "cancel_mandate", map[string]any{"mandate_id": mmc}, "mandate_cancel")
	if err != nil || out(r)["status"] != "cancelled" {
		t.Errorf("cancel %v %v", r.Output, err)
	}
	_, err = call(t, "cancel_mandate", map[string]any{"mandate_id": mmc}, "mandate_cancel_unknown")
	kind(t, err, effects.KindFatal, "unknown mandate")
	r, err = call(t, "check_mandate_balance", map[string]any{"mandate_id": mmc}, "mandate_balance")
	if err != nil || out(r)["balance"] != int64(506000551) {
		t.Errorf("balance rounds down: %v %v", r.Output, err)
	}
	r, err = call(t, "check_mandate_balance", map[string]any{"mandate_id": mmc, "amount": 200000}, "mandate_sufficient")
	if err != nil || out(r)["has_sufficient_balance"] != true || out(r)["balance"] != nil {
		t.Errorf("sufficiency %v %v", r.Output, err)
	}
}

var debitIn = map[string]any{"mandate_id": mmc, "amount": 20000, "narration": "Subscription", "reference": ref}

func TestDebits(t *testing.T) {
	r, err := call(t, "debit_mandate", debitIn, "debit_successful")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "successful" || o["fee"] != int64(5500) || o["session_id"] == "" || o["reference"] != ref || o["response_code"] != "00" {
		t.Errorf("debit %v", o)
	}
	r, err = call(t, "debit_mandate", debitIn, "debit_processing")
	if err != nil || out(r)["status"] != "processing" {
		t.Errorf("processing %v %v", r.Output, err)
	}
	_, err = call(t, "debit_mandate", debitIn, "debit_insufficient")
	kind(t, err, effects.KindFatal, "insufficient funds")
	if !strings.Contains(err.Error(), "code 51") {
		t.Errorf("code: %v", err)
	}
	_, err = call(t, "debit_mandate", debitIn, "debit_locked")
	kind(t, err, effects.KindFatal, "same-day lockout")
	_, err = call(t, "debit_mandate", debitIn, "debit_server_error")
	kind(t, err, effects.KindUnknownOutcome, "504")
	// Duplicate reference: the original debit is reported, or its failure.
	r, err = call(t, "debit_mandate", debitIn, "debit_duplicate", "verify_successful")
	if err != nil || out(r)["status"] != "successful" || out(r)["amount"] != int64(20000) {
		t.Errorf("duplicate %v %v", r.Output, err)
	}
	_, err = call(t, "debit_mandate", debitIn, "debit_duplicate", "verify_failed")
	kind(t, err, effects.KindFatal, "original failed")
	_, err = call(t, "debit_mandate", map[string]any{"mandate_id": mmc, "amount": 100, "narration": "x", "reference": ref})
	kind(t, err, effects.KindFatal, "below minimum")
	_, err = call(t, "debit_mandate", map[string]any{"mandate_id": mmc, "amount": 20000, "narration": "x", "reference": "short"})
	kind(t, err, effects.KindFatal, "short reference")
	r, err = call(t, "get_debit", map[string]any{"mandate_id": mmc, "reference": ref}, "debit_get")
	if err != nil || out(r)["status"] != "successful" || out(r)["amount"] != int64(20000) {
		t.Errorf("get_debit %v %v", r.Output, err)
	}
}

func TestWebhookSecret(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"].Verify
	body := []byte(`{"event":"mono.events.account_connected","event_id":"e1","data":{"id":"a1"}}`)
	h := http.Header{}
	h.Set("mono-webhook-secret", "whsec_1")
	if err := connector.VerifyWebhook(spec, "whsec_1", h, body); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "whsec_2", h, body) == nil {
		t.Error("wrong secret accepted")
	}
	if connector.VerifyWebhook(spec, "whsec_1", http.Header{}, body) == nil {
		t.Error("missing header accepted")
	}
}

func TestTriggerExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"]
	e := expr.MustNewWithRoots("body", "headers", "query")
	for _, c := range []struct{ body, event, dedup, correlation string }{
		{`{"event":"mono.events.account_connected","event_id":"jU4i","data":{"id":"6979","customer":"6961","meta":{"data_status":"PROCESSING"}}}`,
			"mono.events.account_connected", "jU4i", "6979"},
		{`{"event":"mono.events.account_updated","event_id":"tDZC","data":{"account":{"_id":"6979","balance":22967},"meta":{"data_status":"AVAILABLE"}}}`,
			"mono.events.account_updated", "tDZC", "6979"},
		{`{"event":"mono.events.account_unlinked","data":{"account":{"id":"6077"}}}`,
			"mono.events.account_unlinked", "mono.events.account_unlinked:6077:", "6077"},
		{`{"event":"mono.events.account_income","timestamp":"2026-01-27T21:00:38.856Z","data":{"account":"6660","income_streams":[]}}`,
			"mono.events.account_income", "mono.events.account_income:6660:2026-01-27T21:00:38.856Z", "6660"},
		{`{"event":"mono.events.account_credit_worthiness","data":{"account":"679a","summary":{"can_afford":true}}}`,
			"mono.events.account_credit_worthiness", "mono.events.account_credit_worthiness:679a:", "679a"},
		{`{"event":"direct_debit.payment_successful","event_id":"Psm1","data":{"type":"onetime-debit","object":{"id":"txd_1","status":"successful","reference":"ref123"}}}`,
			"direct_debit.payment_successful", "Psm1", "ref123"},
		{`{"event":"events.mandates.ready","event_id":"m1","data":{"id":"mmc_1","reference":"mref","ready_to_debit":true}}`,
			"events.mandates.ready", "m1", "mmc_1"},
		{`{"event":"events.mandate.action.pause","event_id":"m2","data":{"mandate":"mmc_1","status":"success"}}`,
			"events.mandate.action.pause", "m2", "mmc_1"},
		{`{"event":"events.mandates.debit.successful","event_id":"d1","data":{"status":"successful","mandate":"mmc_1","reference_number":"dref"}}`,
			"events.mandates.debit.successful", "d1", "dref"},
		{`{"event":"events.mandates.debit_attempt.successful","data":{"status":"successful","mandate":"mmc_1","reference_number":"dref"}}`,
			"events.mandates.debit_attempt.successful", "events.mandates.debit_attempt.successful:dref:", "dref"},
		{`{"data":{"event":"mono.transaction.dispute_initiated","event_id":"tHLh","data":{"reference":"ll5n","amount":"40000"}}}`,
			"mono.transaction.dispute_initiated", "tHLh", "ll5n"},
	} {
		body, err := expr.DecodeJSON([]byte(c.body))
		if err != nil {
			t.Fatal(err)
		}
		act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}}
		for _, f := range []struct{ name, src, want string }{{"event_type", spec.EventType, c.event}, {"dedup", spec.Dedup, c.dedup}, {"correlation", spec.Correlation, c.correlation}} {
			v, err := e.Eval(f.src, act)
			if err != nil {
				t.Errorf("%s %s: %v", c.event, f.name, err)
				continue
			}
			if got, _ := v.(string); got != f.want {
				t.Errorf("%s %s = %v, want %q", c.event, f.name, v, f.want)
			}
		}
		found := false
		for _, ev := range spec.Events {
			found = found || ev == c.event
		}
		if !found {
			t.Errorf("%s is not in the trigger's events", c.event)
		}
	}
}
