package opay

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

const (
	ref       = "tskab12cd34ef56gh78ij90kl12mn34op5"
	refundRef = "tskrf12cd34ef56gh78ij90kl12mn34op5"
	secret    = "OPAYPRV_test_secret"
)

var creds = map[string]string{"merchant_id": "256621051120756", "public_key": "OPAYPUB_test", "secret_key": secret}

func call(t *testing.T, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), IdempotencyKey: ref, Attempt: 1, Credentials: creds})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func kind(err error) effects.ErrorKind { return effects.Classify(err) }

var cashierIn = map[string]any{"amount": 150000, "product_name": "Shoes", "product_description": "Order 17", "return_url": "https://shop.example/return",
	"callback_url": "https://hooks.example/opay", "pay_method": "BankCard", "expire_minutes": 30, "customer_email": "ada@example.com", "customer_name": "Ada",
	"reference": ref}

var bankTransferIn = map[string]any{"amount": 2300, "product_name": "Invoice 9", "callback_url": "https://hooks.example/opay",
	"customer_name": "Ada Obi", "customer_phone": "+2348011112222", "reference": ref}

var refundIn = map[string]any{"original_reference": "order-17", "amount": 40000, "refund_reason": "Out of stock",
	"callback_url": "https://hooks.example/opay", "reference": refundRef}

func TestRegistersBothHosts(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("opay@1")
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "liveapi.opaycheckout.com" || h[1] != "testapi.opaycheckout.com" {
		t.Errorf("hosts %v", h)
	}
	cl := &client{live: "L", sandbox: "S"}
	if cl.base(connector.Request{Credentials: map[string]string{"environment": "Sandbox"}}) != "S" || cl.base(connector.Request{}) != "L" {
		t.Error("environment selection")
	}
}

// TestRequestSignature pins OPay's documented scheme: Authorization is
// "Bearer " + hex HMAC-SHA512 of the JSON body with keys in alphabetical
// order, keyed with the secret key; MerchantId names the merchant.
func TestRequestSignature(t *testing.T) {
	var gotAuth, gotMerchant, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotAuth, gotMerchant, gotBody = r.Header.Get("Authorization"), r.Header.Get("MerchantId"), string(raw)
		_, _ = w.Write([]byte(`{"code":"00000","message":"SUCCESSFUL","data":{"reference":"96315454","orderNo":"1","status":"PENDING","amount":{"total":400,"currency":"NGN"}}}`))
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL})
	_, err := c.Actions["get_payment"].Execute(context.Background(), connector.Request{Input: map[string]any{"reference": "96315454"}, HTTP: srv.Client(), Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	// The status page's example body, sorted.
	if want := `{"country":"NG","reference":"96315454"}`; gotBody != want {
		t.Errorf("body %s, want %s", gotBody, want)
	}
	m := hmac.New(sha512.New, []byte(secret))
	m.Write([]byte(gotBody))
	if want := "Bearer " + hex.EncodeToString(m.Sum(nil)); gotAuth != want || gotAuth != "Bearer "+Sign(secret, []byte(gotBody)) {
		t.Errorf("authorization %s, want %s", gotAuth, want)
	}
	if gotMerchant != "256621051120756" {
		t.Errorf("merchant %q", gotMerchant)
	}
	// A fixed vector: HMAC-SHA512("OPAYPRV_test_secret", that body),
	// computed independently.
	if s := Sign(secret, []byte(`{"country":"NG","reference":"96315454"}`)); s != "57a6d24dc5562213429d3793e07d84b231b57099a88f313b3ac74adacad0b22b50a3b21de790793a4dbaa5d99a6de10d59fa9b2560a0e2c5b7429320a9ba6342" {
		t.Errorf("signature %s", s)
	}
}

func TestCashierPayment(t *testing.T) {
	r, err := call(t, "create_cashier_payment", cashierIn, "cashier_create")
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	if o["status"] != "INITIAL" || o["amount"] != int64(150000) || o["order_no"] != "211009140896553163" || !strings.HasPrefix(o["cashier_url"].(string), "https://") {
		t.Errorf("output %v", o)
	}
}

func TestCashierRepeatReportsTheOriginal(t *testing.T) {
	// A resend after a lost response: OPay refuses the reference (02004)
	// and the connector reports the payment already made under it.
	r, err := call(t, "create_cashier_payment", cashierIn, "cashier_create_duplicate", "status_success")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "SUCCESS" || o["amount"] != int64(150000) || o["created_at_ms"] != int64(1633788085000) {
		t.Errorf("output %v", o)
	}
	// The original failed: the step fails with OPay's reason.
	_, err = call(t, "create_cashier_payment", cashierIn, "cashier_create_duplicate", "status_fail")
	if kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "Insufficient funds") {
		t.Errorf("failed original: %v", err)
	}
	// The reference belongs to a payment of another amount: not ours.
	_, err = call(t, "create_cashier_payment", cashierIn, "cashier_create_duplicate", "status_other_amount")
	if kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "99900") {
		t.Errorf("other amount: %v", err)
	}
	// Refused, yet the status API has nothing under it: a person decides.
	_, err = call(t, "create_cashier_payment", cashierIn, "cashier_create_duplicate", "status_not_found")
	if kind(err) != effects.KindIndeterminate {
		t.Errorf("refused and absent: %v", err)
	}
}

func TestCashierErrors(t *testing.T) {
	for name, want := range map[string]effects.ErrorKind{
		"cashier_create_server_error": effects.KindUnknownOutcome, // 502: OPay may have acted
		"cashier_create_service_down": effects.KindUnknownOutcome, // 50003
		"cashier_create_auth_failed":  effects.KindFatal,          // 02000
		"cashier_create_unreadable":   effects.KindUnknownOutcome, // 1500.5 kobo
	} {
		if _, err := call(t, "create_cashier_payment", cashierIn, name); kind(err) != want {
			t.Errorf("%s: %v (kind %v, want %v)", name, err, kind(err), want)
		}
	}
	_, err := call(t, "create_cashier_payment", cashierIn, "cashier_create_auth_failed")
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("reason lost: %v", err)
	}
	// Fractional kobo, missing fields and missing credentials never reach OPay.
	bad := map[string]any{"amount": 100.5, "product_name": "x", "product_description": "x", "return_url": "https://x", "reference": ref}
	if _, err := call(t, "create_cashier_payment", bad); kind(err) != effects.KindFatal {
		t.Errorf("fractional kobo: %v", err)
	}
	if _, err := call(t, "create_cashier_payment", map[string]any{"amount": 100, "reference": ref}); kind(err) != effects.KindFatal {
		t.Errorf("missing product: %v", err)
	}
	c := New(Options{BaseURL: "http://127.0.0.1:1"})
	_, err = c.Actions["get_payment"].Execute(context.Background(), connector.Request{Input: map[string]any{"reference": ref}, Credentials: map[string]string{"merchant_id": "1"}})
	if kind(err) != effects.KindFatal {
		t.Errorf("no secret key: %v", err)
	}
}

func TestBankTransferPayment(t *testing.T) {
	r, err := call(t, "create_bank_transfer_payment", bankTransferIn, "bank_transfer_create")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["account_number"] != "7827845341" || o["bank_name"] != "WEMA BANK" || o["expires_at"] != int64(1641773850) || o["status"] != "PENDING" || o["amount"] != int64(2300) {
		t.Errorf("output %v", o)
	}
	r, err = call(t, "create_bank_transfer_payment", bankTransferIn, "bank_transfer_duplicate", "status_bank_transfer")
	if err != nil || out(r)["status"] != "SUCCESS" || out(r)["order_no"] != "220110144664537659" {
		t.Errorf("repeat: %v %v", r.Output, err)
	}
}

func TestGetPayment(t *testing.T) {
	r, err := call(t, "get_payment", map[string]any{"order_no": "211009140896593010"}, "status_by_order_no")
	if err != nil || out(r)["status"] != "PENDING" || out(r)["voided"] != false {
		t.Errorf("by order no: %v %v", r.Output, err)
	}
	_, err = call(t, "get_payment", map[string]any{"reference": ref}, "status_not_found")
	if !errors.Is(err, connector.ErrNotFound) || kind(err) != effects.KindFatal {
		t.Errorf("not found: %v", err)
	}
	_, err = call(t, "get_payment", map[string]any{"reference": ref}, "status_unreadable")
	if kind(err) != effects.KindUnknownOutcome {
		t.Errorf("unreadable amount: %v", err)
	}
	if _, err := call(t, "get_payment", map[string]any{}); kind(err) != effects.KindFatal {
		t.Errorf("no key: %v", err)
	}
}

func TestClosePayment(t *testing.T) {
	r, err := call(t, "close_payment", map[string]any{"reference": ref}, "close")
	if err != nil || out(r)["status"] != "CLOSE" || out(r)["amount"] != int64(150000) {
		t.Errorf("close: %v %v", r.Output, err)
	}
	if _, err := call(t, "close_payment", map[string]any{"reference": ref}, "close_server_error"); kind(err) != effects.KindUnknownOutcome {
		t.Errorf("504: %v", err)
	}
}

func TestRefund(t *testing.T) {
	r, err := call(t, "refund_payment", refundIn, "refund_create")
	if err != nil || out(r)["status"] != "PENDING" || out(r)["amount"] != int64(40000) || out(r)["original_order_no"] != "211003140885503763" {
		t.Errorf("refund: %v %v", r.Output, err)
	}
	_, err = call(t, "refund_payment", refundIn, "refund_create_fail")
	if kind(err) != effects.KindFatal {
		t.Errorf("FAIL: %v", err)
	}
	if _, err := call(t, "refund_payment", refundIn, "refund_server_error"); kind(err) != effects.KindUnknownOutcome {
		t.Errorf("500: %v", err)
	}
	// Lost response: the resend is refused and the refund is reported.
	r, err = call(t, "refund_payment", refundIn, "refund_duplicate", "refund_query_success")
	if err != nil || out(r)["status"] != "SUCCESS" {
		t.Errorf("repeat: %v %v", r.Output, err)
	}
	_, err = call(t, "refund_payment", refundIn, "refund_duplicate", "refund_query_fail")
	if kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "refund error") {
		t.Errorf("repeat of a failed refund: %v", err)
	}
	_, err = call(t, "refund_payment", refundIn, "refund_duplicate", "refund_query_not_found")
	if kind(err) != effects.KindIndeterminate {
		t.Errorf("refused and absent: %v", err)
	}
	// A different original payment under the same refund reference.
	if _, err := refunded(map[string]any{"reference": refundRef, "original_reference": "order-17", "amount": int64(40000), "status": "SUCCESS"}, "order-18", 40000); kind(err) != effects.KindFatal {
		t.Errorf("other original: %v", err)
	}
	// OPay requires a callback URL; BankAccount refunds need the account.
	noCB := map[string]any{"original_reference": "order-17", "amount": 40000, "reference": refundRef}
	if _, err := call(t, "refund_payment", noCB); kind(err) != effects.KindFatal {
		t.Errorf("no callback: %v", err)
	}
	toBank := map[string]any{"original_reference": "order-17", "amount": 40000, "reference": refundRef, "callback_url": "https://x", "refund_way": "BankAccount"}
	if _, err := call(t, "refund_payment", toBank); kind(err) != effects.KindFatal {
		t.Errorf("bank refund without account: %v", err)
	}
	r, err = call(t, "get_refund", map[string]any{"reference": refundRef}, "refund_query_fail")
	if err != nil || out(r)["status"] != "FAIL" || out(r)["error_code"] != "91" {
		t.Errorf("get_refund: %v %v", r.Output, err)
	}
}

// The callback example from OPay's callback-signature page, signed with the
// private key its Java sample verifies it with.
const (
	docKey      = "OPAYPRV16498196872570.13953388019021462"
	docCallback = `{"payload":{"amount":"49160","channel":"Web","country":"NG","currency":"NGN","displayedFailure":"","fee":"737","feeCurrency":"NGN","instrumentType":"BankCard","reference":"10023","refunded":false,"status":"SUCCESS","timestamp":"2022-05-07T06:20:46Z","token":"220507145660712931829","transactionId":"220507145660712931829","updated_at":"2022-05-07T07:20:46Z"},"sha512":"9f605d69f04e94172875dc156537071cead060bbcaeaca94a7b8805af9f89611e2fdf6836713c9c90b028ca7e4470b1356e996975f2abc862315aaa9b7f2ae2d","type":"transaction-status"}`
)

func TestCallbackSignatureMatchesOPaysExample(t *testing.T) {
	var cb struct {
		Payload map[string]any `json:"payload"`
		SHA512  string         `json:"sha512"`
	}
	if err := json.Unmarshal([]byte(docCallback), &cb); err != nil {
		t.Fatal(err)
	}
	if got := CallbackSignature(docKey, cb.Payload); got != cb.SHA512 {
		t.Fatalf("signature %s, want %s", got, cb.SHA512)
	}
	c := New(Options{})
	r, err := c.Actions["verify_callback"].Execute(context.Background(), connector.Request{
		Input: map[string]any{"payload": cb.Payload, "sha512": cb.SHA512}, Credentials: map[string]string{"secret_key": docKey}})
	if err != nil || out(r)["valid"] != true || out(r)["reference"] != "10023" || out(r)["refunded"] != false {
		t.Errorf("verify: %v %v", r.Output, err)
	}
	// Any signed field changed, or another key: refused.
	tampered := map[string]any{}
	for k, v := range cb.Payload {
		tampered[k] = v
	}
	tampered["status"] = "FAIL"
	_, err = c.Actions["verify_callback"].Execute(context.Background(), connector.Request{
		Input: map[string]any{"payload": tampered, "sha512": cb.SHA512}, Credentials: map[string]string{"secret_key": docKey}})
	if kind(err) != effects.KindFatal {
		t.Errorf("tampered accepted: %v", err)
	}
	_, err = c.Actions["verify_callback"].Execute(context.Background(), connector.Request{
		Input: map[string]any{"payload": cb.Payload, "sha512": cb.SHA512}, Credentials: map[string]string{"secret_key": "OPAYPRV_other"}})
	if kind(err) != effects.KindFatal {
		t.Errorf("wrong key accepted: %v", err)
	}
}

func TestCallbackTrigger(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["payment"]
	// OPay's body signature cannot be checked by the engine: the trigger
	// accepts deliveries and the manifest says so; workflows re-query.
	if spec.Verify == nil || spec.Verify.Scheme != "none" {
		t.Fatalf("verify %+v", spec.Verify)
	}
	if err := connector.VerifyWebhook(spec.Verify, "", http.Header{}, []byte(docCallback)); err != nil {
		t.Error(err)
	}
	if spec.EventType != "=body.type" || spec.Correlation != "=body.payload.reference" || spec.Dedup != "=body.sha512" || len(spec.Events) != 1 || spec.Events[0] != "transaction-status" {
		t.Errorf("trigger %+v", spec)
	}
}
