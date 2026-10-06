package remita

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

const (
	pid   = "ab12cd34ef56ab12cd34ef56ab12cd34"
	order = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	batch = "11223344556677889900aabbccddeeff"
	rrr   = "140008260136"
)

var creds = map[string]string{"secret_key": "rmt_sk_test", "merchant_id": "2547916", "api_key": "1946", "service_type_id": "4430731",
	"source_account_number": "0119288291", "source_bank_code": "011", "source_account_name": "John Nomel", "environment": "demo"}

func call(t *testing.T, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), Attempt: 1, Credentials: creds})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func kind(err error) effects.ErrorKind { return effects.Classify(err) }

var transferIn = map[string]any{"amount": 250050, "account_number": "0586957398", "bank_code": "058", "account_name": "Paul Reed",
	"narration": "Sept salary", "payment_identifier": pid}

var invoiceIn = map[string]any{"amount": 2100000, "payer_name": "John Doe", "payer_email": "doe@example.com", "payer_phone": "09062067384",
	"description": "September fees", "order_id": order}

var bulkIn = map[string]any{"narration": "October payroll", "batch_payment_identifier": batch, "transfers": []any{
	map[string]any{"amount": 100000, "account_number": "0037475942", "bank_code": "058", "account_name": "Sam Alkinson"},
	map[string]any{"amount": 250025, "account_number": "0702232975", "bank_code": "070", "account_name": "Ibrahim Lawal"}}}

func TestHostsAndEnvironments(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("remita@1")
	if h := c.Manifest.Hosts(); len(h) != 4 || h[0] != "api-gateway.remita.net" || h[1] != "api-demo.systemspecsng.com" {
		t.Errorf("hosts %v", h)
	}
	cl := New(Options{})
	// Live invoices are refused: Remita does not document their live host.
	_, err := cl.Actions["generate_invoice"].Execute(context.Background(), connector.Request{Input: invoiceIn,
		Credentials: map[string]string{"merchant_id": "1", "api_key": "k", "service_type_id": "s"}})
	if kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "live host") {
		t.Errorf("live invoice: %v", err)
	}
	inner := &client{live: hosts{gateway: "L"}, demo: hosts{gateway: "D"}}
	if inner.hosts(connector.Request{Credentials: map[string]string{"environment": "Demo"}}).gateway != "D" || inner.hosts(connector.Request{}).gateway != "L" {
		t.Error("environment selection")
	}
}

// TestHashes pins Remita's apiHash: lowercase hex SHA-512 of the documented
// concatenations (values computed independently).
func TestHashes(t *testing.T) {
	if h := Hash("2547916", "4430731", order, "21000", "1946"); h != "949c2c31adff04d3d54a8fbf895625a74b2c4d45d08fbc085ce61698c6e3f9dd2fcd4c64674fb8d79b61fd04e0d592035280ccb33c1a342d38d55b43cb7e7ce7" {
		t.Errorf("invoice hash %s", h)
	}
	if h := Hash(rrr, "1946", "2547916"); h != "7cbc7ffa3a6a220d96f081742192301b2156cc815902163293df7bfc3003d180cac62db28d190cbc73927b04e95b639e46d66063789439da42b2afcef933f333" {
		t.Errorf("status hash %s", h)
	}
	if ItemIdentifier(batch, 0) != "dc68783eab5da3d2039f95a9004e455c" || ItemIdentifier(batch, 1) != "8b500d607843fcace79193a4668730e5" {
		t.Error("bulk item identifiers")
	}
	if nairaText(2100000) != "21000" || nairaText(2100050) != "21000.50" || nairaText(5) != "0.05" {
		t.Error("naira text")
	}
}

func TestBanksAndNameEnquiry(t *testing.T) {
	r, err := call(t, "list_banks", nil, "banks")
	if err != nil || len(out(r)["banks"].([]any)) != 3 {
		t.Errorf("banks %v %v", r.Output, err)
	}
	r, err = call(t, "resolve_account", map[string]any{"account_number": "0586957398", "bank_code": "058"}, "name_enquiry")
	if err != nil || out(r)["account_name"] != "Paul Reed" {
		t.Errorf("resolve %v %v", r.Output, err)
	}
	if _, ok := out(r)["bvn"]; ok {
		t.Error("BVN leaked into the output")
	}
	_, err = call(t, "resolve_account", map[string]any{"account_number": "0586957398", "bank_code": "058"}, "name_enquiry_invalid")
	if kind(err) != effects.KindFatal {
		t.Errorf("invalid: %v", err)
	}
}

func TestTransfer(t *testing.T) {
	r, err := call(t, "transfer", transferIn, "transfer_pending")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "pending" || o["amount"] != int64(250050) || o["payment_identifier"] != pid || o["remita_payment_identifier"] != "000078250820160031801468554954" {
		t.Errorf("output %v", o)
	}
	// A documented failure code: nothing moved; the step fails with the reason.
	_, err = call(t, "transfer", transferIn, "transfer_failed")
	if kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "Routing error") {
		t.Errorf("failed: %v", err)
	}
	// A code Remita does not list is pending, as Remita advises.
	r, err = call(t, "transfer", transferIn, "transfer_unlisted_code")
	if err != nil || out(r)["status"] != "pending" || out(r)["transaction_state"] != "FAILED" {
		t.Errorf("unlisted: %v %v", r.Output, err)
	}
	for name, want := range map[string]effects.ErrorKind{
		"transfer_server_error": effects.KindUnknownOutcome,
		"transfer_bad_key":      effects.KindFatal,
		"transfer_unreadable":   effects.KindUnknownOutcome,
	} {
		if _, err := call(t, "transfer", transferIn, name); kind(err) != want {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]any{"amount": 10.5, "account_number": "1", "bank_code": "1", "account_name": "x", "narration": "x", "payment_identifier": pid}
	if _, err := call(t, "transfer", bad); kind(err) != effects.KindFatal {
		t.Errorf("fractional kobo: %v", err)
	}
	if _, err := call(t, "transfer", map[string]any{"amount": 100, "narration": "x", "payment_identifier": pid}); kind(err) != effects.KindFatal {
		t.Errorf("no destination: %v", err)
	}
}

func TestTransferRepeatReportsTheOriginal(t *testing.T) {
	// Resend after a lost response: refused as a duplicate, looked up.
	r, err := call(t, "transfer", transferIn, "transfer_duplicate", "query_success")
	if err != nil || out(r)["status"] != "successful" || out(r)["debit_completed"] != true {
		t.Errorf("repeat: %v %v", r.Output, err)
	}
	// The duplicate refusal may come as a 4xx.
	r, err = call(t, "transfer", transferIn, "transfer_duplicate_http", "query_success")
	if err != nil || out(r)["status"] != "successful" {
		t.Errorf("repeat (4xx): %v %v", r.Output, err)
	}
	_, err = call(t, "transfer", transferIn, "transfer_duplicate", "query_failed")
	if kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "No sufficient funds") {
		t.Errorf("failed original: %v", err)
	}
	_, err = call(t, "transfer", transferIn, "transfer_duplicate", "query_not_found")
	if kind(err) != effects.KindIndeterminate {
		t.Errorf("refused and absent: %v", err)
	}
}

func TestGetTransfer(t *testing.T) {
	r, err := call(t, "get_transfer", map[string]any{"payment_identifier": pid}, "query_failed")
	if err != nil || out(r)["status"] != "failed" || out(r)["response_code"] != "51" {
		t.Errorf("failed read: %v %v", r.Output, err)
	}
	_, err = call(t, "get_transfer", map[string]any{"payment_identifier": pid}, "query_not_found")
	if !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
}

func TestBulkTransfer(t *testing.T) {
	r, err := call(t, "bulk_transfer", bulkIn, "bulk_initiated")
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	items := o["transfers"].([]any)
	if o["status"] != "INITIATED" || o["total_amount"] != int64(350025) || len(items) != 2 || items[1].(map[string]any)["payment_identifier"] != ItemIdentifier(batch, 1) {
		t.Errorf("output %v", o)
	}
	if _, err := call(t, "bulk_transfer", bulkIn, "bulk_debit_failed"); kind(err) != effects.KindFatal {
		t.Errorf("debit failed: %v", err)
	}
	if _, err := call(t, "bulk_transfer", bulkIn, "bulk_server_error"); kind(err) != effects.KindUnknownOutcome {
		t.Errorf("500: %v", err)
	}
	r, err = call(t, "bulk_transfer", bulkIn, "bulk_duplicate", "bulk_status", "bulk_detail")
	if err != nil || out(r)["status"] != "COMPLETED" || out(r)["settled"] != true || out(r)["total_credited_amount"] != int64(100000) {
		t.Errorf("repeat: %v %v", r.Output, err)
	}
	if err == nil {
		tr := out(r)["transfers"].([]any)
		if tr[0].(map[string]any)["outcome"] != "successful" || tr[1].(map[string]any)["outcome"] != "failed" {
			t.Errorf("items %v", tr)
		}
	}
	if _, err := call(t, "bulk_transfer", bulkIn, "bulk_duplicate", "bulk_status_not_found"); kind(err) != effects.KindIndeterminate {
		t.Errorf("refused and absent: %v", err)
	}
	if _, err := call(t, "get_bulk_transfer", map[string]any{"batch_payment_identifier": batch}, "bulk_status_not_found"); !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
}

func TestInvoice(t *testing.T) {
	r, err := call(t, "generate_invoice", invoiceIn, "invoice_generate")
	if err != nil || out(r)["rrr"] != rrr || out(r)["status"] != "pending" || out(r)["amount"] != int64(2100000) {
		t.Errorf("generate: %v %v", r.Output, err)
	}
	split := map[string]any{"amount": 2100050, "payer_name": "John Doe", "payer_email": "doe@example.com", "payer_phone": "09062067384",
		"description": "September fees", "order_id": order,
		"line_items": []any{
			map[string]any{"id": "itemid1", "beneficiary_name": "Alozie Michael", "beneficiary_account": "6020067886", "bank_code": "058", "amount": 2000000, "bears_fee": true},
			map[string]any{"id": "itemid2", "beneficiary_name": "Folivi Joshua", "beneficiary_account": "0360883515", "bank_code": "058", "amount": 100050}},
		"custom_fields": []any{map[string]any{"name": "Student Number", "value": "561561516"}}}
	if r, err := call(t, "generate_invoice", split, "invoice_generate_split"); err != nil || out(r)["rrr"] != "160007846369" {
		t.Errorf("split: %v %v", r.Output, err)
	}
	split["line_items"].([]any)[1].(map[string]any)["amount"] = 100000
	if _, err := call(t, "generate_invoice", split); kind(err) != effects.KindFatal {
		t.Errorf("line items not adding up: %v", err)
	}
	for name, want := range map[string]effects.ErrorKind{
		"invoice_bad_hash":       effects.KindFatal,
		"invoice_internal_error": effects.KindUnknownOutcome,
		"invoice_server_error":   effects.KindUnknownOutcome,
	} {
		if _, err := call(t, "generate_invoice", invoiceIn, name); kind(err) != want {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestInvoiceRepeatReportsTheOriginal(t *testing.T) {
	r, err := call(t, "generate_invoice", invoiceIn, "invoice_duplicate", "orderstatus_pending")
	if err != nil || out(r)["rrr"] != rrr || out(r)["status"] != "pending" {
		t.Errorf("repeat: %v %v", r.Output, err)
	}
	r, err = call(t, "generate_invoice", invoiceIn, "invoice_duplicate_http", "orderstatus_pending")
	if err != nil || out(r)["rrr"] != rrr {
		t.Errorf("repeat (4xx): %v %v", r.Output, err)
	}
	if _, err := call(t, "generate_invoice", invoiceIn, "invoice_duplicate", "orderstatus_other_amount"); kind(err) != effects.KindFatal {
		t.Errorf("other amount: %v", err)
	}
	if _, err := call(t, "generate_invoice", invoiceIn, "invoice_duplicate", "orderstatus_not_found"); kind(err) != effects.KindIndeterminate {
		t.Errorf("refused and absent: %v", err)
	}
}

func TestInvoiceStatusAndCancel(t *testing.T) {
	r, err := call(t, "get_invoice", map[string]any{"rrr": rrr}, "status_paid")
	if err != nil || out(r)["status"] != "paid" || out(r)["amount"] != int64(2100000) || out(r)["payment_date"] == "" {
		t.Errorf("paid: %v %v", r.Output, err)
	}
	if _, err := call(t, "get_invoice", map[string]any{"rrr": rrr}, "status_unreadable"); kind(err) != effects.KindUnknownOutcome {
		t.Errorf("unreadable: %v", err)
	}
	if _, err := call(t, "get_invoice", map[string]any{"order_id": order}, "orderstatus_not_found"); !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
	if r, err := call(t, "cancel_invoice", map[string]any{"rrr": rrr}, "cancel"); err != nil || out(r)["status_code"] != "00" {
		t.Errorf("cancel: %v %v", r.Output, err)
	}
	if _, err := call(t, "cancel_invoice", map[string]any{"rrr": rrr}, "cancel_refused"); kind(err) != effects.KindFatal {
		t.Errorf("cancel refused: %v", err)
	}
}

// Some invoice responses come wrapped as jsonp (…), as Remita's examples show.
func TestJSONPResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`jsonp ({"statuscode":"025","RRR":"130007846382","status":"Payment Reference generated"})`))
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL})
	r, err := c.Actions["generate_invoice"].Execute(context.Background(), connector.Request{Input: invoiceIn, HTTP: srv.Client(), Credentials: creds})
	if err != nil || out(r)["rrr"] != "130007846382" {
		t.Errorf("jsonp: %v %v", r.Output, err)
	}
	if string(unwrapJSONP([]byte(`{"a":1}`))) != `{"a":1}` {
		t.Error("plain JSON altered")
	}
}

func TestNotificationTrigger(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["invoice_payment"]
	if spec.Verify == nil || spec.Verify.Scheme != "none" {
		t.Fatalf("verify %+v", spec.Verify)
	}
	if spec.Dedup != "=body[0].rrr" || spec.Correlation != "=body[0].orderId" || len(spec.Events) != 1 || spec.Events[0] != "payment_notification" {
		t.Errorf("trigger %+v", spec)
	}
}
