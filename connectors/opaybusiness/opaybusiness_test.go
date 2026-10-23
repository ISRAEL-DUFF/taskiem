package opaybusiness

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

const ref = "tskab12cd34ef56gh78ij90kl12mn34op56"

// Two key pairs, made once: OPay's and the business's.
var (
	keysOnce         sync.Once
	opayKey, shopKey *rsa.PrivateKey
	opayPub, shopPEM string
)

func testKeys(t testing.TB) {
	t.Helper()
	keysOnce.Do(func() {
		var err error
		if opayKey, err = rsa.GenerateKey(rand.Reader, 1024); err != nil { //nolint:gosec // OPay documents 1024-bit keys
			panic(err)
		}
		if shopKey, err = rsa.GenerateKey(rand.Reader, 1024); err != nil { //nolint:gosec // as above
			panic(err)
		}
		opayPub, _ = PublicKeyPEM(&opayKey.PublicKey)
		der, _ := x509.MarshalPKCS8PrivateKey(shopKey)
		shopPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	})
}

func creds(t testing.TB) map[string]string {
	testKeys(t)
	return map[string]string{"head_merchant_id": "256622040884147", "merchant_id": "256622040884148", "client_auth_key": "b819c755d80b4c268f831f0cef69a22e",
		"opay_public_key": opayPub, "merchant_private_key": shopPEM, "sub_scene": "RETAIL"}
}

// exchange is one expected call: the plaintext OPay should receive at path,
// and the code, message and plaintext data it answers with.
type exchange struct {
	path    string
	want    map[string]any
	code    string
	message string
	data    any
	// unsigned answers without OPay's signature; raw answers a non-JSON body.
	unsigned bool
	raw      string
}

// fakeOPay is OPay's side of the envelope: it decrypts each request with
// OPay's private key, checks the business's signature over paramContent +
// timestamp and the headers, compares the plaintext with the expectation,
// and answers data encrypted with the business's public key and signed by
// OPay over the sorted fields.
func fakeOPay(t *testing.T, exchanges ...exchange) *httptest.Server {
	t.Helper()
	testKeys(t)
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if len(exchanges) == 0 {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		ex := exchanges[0]
		exchanges = exchanges[1:]
		if r.URL.Path != ex.path {
			t.Errorf("call %s, want %s", r.URL.Path, ex.path)
		}
		if r.Header.Get("clientAuthKey") != "b819c755d80b4c268f831f0cef69a22e" || r.Header.Get("version") != "V1.0.1" || r.Header.Get("bodyFormat") != "JSON" {
			t.Errorf("headers %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		var env struct{ ParamContent, Sign string }
		if err := json.Unmarshal(body, &env); err != nil || env.ParamContent == "" || env.Sign == "" {
			t.Errorf("request body %s", body)
		}
		if err := Verify(&shopKey.PublicKey, env.ParamContent+r.Header.Get("timestamp"), env.Sign); err != nil {
			t.Errorf("request signature: %v", err)
		}
		plain, err := Decrypt(opayKey, env.ParamContent)
		if err != nil {
			t.Errorf("request decrypt: %v", err)
		}
		var got map[string]any
		_ = json.Unmarshal(plain, &got)
		if ex.want != nil && !reflect.DeepEqual(norm(got), norm(ex.want)) {
			t.Errorf("%s: plaintext %s, want %v", ex.path, plain, ex.want)
		}
		if ex.raw != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, ex.raw)
			return
		}
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		fields := map[string]any{"code": ex.code, "message": ex.message, "timestamp": ts}
		if ex.data != nil {
			raw, _ := canonicalJSON(ex.data)
			enc, err := Encrypt(&shopKey.PublicKey, raw)
			if err != nil {
				t.Fatal(err)
			}
			fields["data"] = enc
		}
		if !ex.unsigned {
			sig, err := Sign(opayKey, ResponseSigned(fields))
			if err != nil {
				t.Fatal(err)
			}
			fields["sign"] = sig
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fields)
	}))
	t.Cleanup(func() {
		srv.Close()
		if len(exchanges) > 0 {
			t.Errorf("%d expected call(s) never arrived, next %s", len(exchanges), exchanges[0].path)
		}
	})
	return srv
}

// norm makes numbers comparable after a JSON round trip.
func norm(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func call(t *testing.T, action string, input map[string]any, exchanges ...exchange) (connector.Response, error) {
	t.Helper()
	srv := fakeOPay(t, exchanges...)
	c := New(Options{BaseURL: srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), IdempotencyKey: ref, Attempt: 1, Credentials: creds(t)})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

// docOrder is the transaction of OPay's Query Order Details example.
var docOrder = map[string]any{"outOrderNo": "2334345345345211", "orderNo": "20220704741380171865833472", "payNo": "2334345345345245",
	"headMerchantId": "256622022480363", "merchantId": "256622022480364", "status": "SUCCESS", "errorMsg": "", "senderName": "John",
	"senderAccount": "23234324*****23423", "senderBank": "", "recipientName": "Michael", "recipientAccount": "+2345656567878", "recipientBank": "",
	"amount": "100.00", "fee": "10.00", "feePattern": "OUT_DEDUCT", "payMethod": "BalancePayment", "settlementAmount": "90.00", "settlementFee": "10.00",
	"settlementFeePattern": "OUT_DEDUCT", "currency": "NGN", "isSplit": "N", "productInfo": "", "sn": "POS-SN-001", "remark": nil,
	"transactionTime": "1692773950143", "completedTime": "1692773950143"}

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("opay_business@1")
	if !ok {
		t.Fatal("opay_business@1 not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 1 || h[0] != "payapi.opayweb.com" {
		t.Errorf("hosts %v", h)
	}
	if c.Verifiers["payment"] == nil || c.Enrichers["payment"] == nil || c.CheckCredentials == nil {
		t.Error("the payment trigger needs a verifier and an enricher, and credentials a check")
	}
	var pk *connector.AuthField
	for i := range c.Manifest.Auth.Fields {
		if c.Manifest.Auth.Fields[i].Key == "merchant_private_key" {
			pk = &c.Manifest.Auth.Fields[i]
		}
	}
	if pk == nil || !pk.Multiline || pk.Generate != "rsa_key_pair" {
		t.Errorf("the private key field is generated for the person: %+v", pk)
	}
}

func TestCheckCredentials(t *testing.T) {
	c := creds(t)
	if err := checkCredentials(c); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{"opay_public_key": "not a key", "merchant_private_key": c["merchant_private_key"]}
	if err := checkCredentials(bad); err == nil || !strings.Contains(err.Error(), "OPay's public key") {
		t.Errorf("bad public key: %v", err)
	}
	// Keys as Base64 DER, as the dashboard may show them, are accepted too.
	der, _ := x509.MarshalPKIXPublicKey(&opayKey.PublicKey)
	b64 := strings.TrimSpace(strings.Join(strings.Split(strings.TrimSuffix(strings.TrimPrefix(opayPub, "-----BEGIN PUBLIC KEY-----"), "-----END PUBLIC KEY-----\n"), "\n"), ""))
	if _, err := ParsePublicKey(b64); err != nil || len(der) == 0 {
		t.Errorf("base64 DER public key: %v", err)
	}
}

func TestBalanceIsTheConnectionTest(t *testing.T) {
	r, err := call(t, "get_balance", nil, exchange{path: "/openApi/merchant/queryMerchantBalance", want: map[string]any{"opayMerchantId": "256622040884148"},
		code: "00000", message: "SUCCESSFUL", data: map[string]any{"opayMerchantId": "256622040884148", "opayMerchantName": "Mama Nkechi", "usableAmount": "1000000",
			"currency": "NGN", "opayAccountNo": "6126390625", "queryTime": "2026-10-22 08:47:55"}})
	if err != nil || out(r)["available_balance"] != int64(1000000) || out(r)["account_number"] != "6126390625" {
		t.Errorf("balance %v %v", r.Output, err)
	}
	// Without a branch, the business id is asked about.
	c := creds(t)
	delete(c, "merchant_id")
	srv := fakeOPay(t, exchange{path: "/openApi/merchant/queryMerchantBalance", want: map[string]any{"opayMerchantId": "256622040884147"}, code: "00000", data: map[string]any{"usableAmount": "5"}})
	if _, err := New(Options{BaseURL: srv.URL}).Actions["get_balance"].Execute(context.Background(), connector.Request{HTTP: srv.Client(), Credentials: c}); err != nil {
		t.Errorf("head only: %v", err)
	}
	// OPay's refusals and its "try again" codes.
	_, err = call(t, "get_balance", nil, exchange{path: "/openApi/merchant/queryMerchantBalance", code: "C_1111", message: "Fail to query merchant key"})
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "C_1111") {
		t.Errorf("refused: %v", err)
	}
	_, err = call(t, "get_balance", nil, exchange{path: "/openApi/merchant/queryMerchantBalance", code: "10000", message: "System busy"})
	if effects.Classify(err) != effects.KindRetryable {
		t.Errorf("busy: %v", err)
	}
	// An answer OPay did not sign, or signed with another key, is not trusted.
	_, err = call(t, "get_balance", nil, exchange{path: "/openApi/merchant/queryMerchantBalance", code: "00000", data: map[string]any{"usableAmount": "5"}, unsigned: true})
	if effects.Classify(err) != effects.KindUnknownOutcome || !strings.Contains(err.Error(), "signature") {
		t.Errorf("unsigned answer: %v", err)
	}
	_, err = call(t, "get_balance", nil, exchange{path: "/openApi/merchant/queryMerchantBalance", raw: `{"code":"00000","message":"ok","sign":"AAAA","timestamp":"1","data":"AAAA"}`})
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("forged answer: %v", err)
	}
}

func TestTransactionsAndTerminals(t *testing.T) {
	r, err := call(t, "get_transaction", map[string]any{"order_no": "20220704741380171865833472"},
		exchange{path: "/openApi/order/queryDetail", want: map[string]any{"headMerchantId": "256622040884147", "merchantId": "256622040884148", "orderType": "PAY", "orderNo": "20220704741380171865833472"},
			code: "00000", data: docOrder})
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	if o["amount"] != int64(10000) || o["fee"] != int64(1000) || o["settlement_amount"] != int64(9000) || o["status"] != "SUCCESS" || o["terminal_serial"] != "POS-SN-001" ||
		o["transaction_time"] != "2023-08-23T06:59:10Z" || o["sender_account"] != "23234324*****23423" || o["recipient_account"] != "+2345656567878" {
		t.Errorf("transaction %v", o)
	}
	// Half a kobo cannot be read: an error, never a guess.
	odd := map[string]any{}
	for k, v := range docOrder {
		odd[k] = v
	}
	odd["amount"] = "100.005"
	_, err = call(t, "get_transaction", map[string]any{"order_no": "x"}, exchange{path: "/openApi/order/queryDetail", code: "00000", data: odd})
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("unreadable amount: %v", err)
	}

	r, err = call(t, "get_terminal", map[string]any{"terminal_serial": "POS-SN-001"},
		exchange{path: "/openApi/tn/queryBySn", want: map[string]any{"headMerchantId": "256622040884147", "merchantId": "256622040884148", "sn": "POS-SN-001"},
			code: "00000", data: map[string]any{"sn": "POS-SN-001", "tn": "6123456789", "tnStatus": "ACTIVE", "recipientName": nil, "receiptBank": "OPay", "boundTime": "Aug 26,2026 11:00:00"}})
	if err != nil || out(r)["tn"] != "6123456789" || out(r)["status"] != "ACTIVE" {
		t.Errorf("terminal %v %v", r.Output, err)
	}
}

func TestListTerminalTransactions(t *testing.T) {
	rec := func(payNo, amount string) map[string]any {
		return map[string]any{"payNo": payNo, "orderNo": "o" + payNo, "outOrderNo": "", "headMerchantId": "256622040884147", "merchantId": "256622040884148",
			"sn": "POS-SN-001", "tn": "6123456789", "status": "SUCCESS", "amount": amount, "currency": "NGN", "senderName": "John", "transactionTime": "1693645200000"}
	}
	// The terminal's account is looked up, then two pages are joined.
	first := map[string]any{"headMerchantId": "256622040884147", "merchantId": "256622040884148", "tn": "6123456789", "sn": "POS-SN-001",
		"startTime": "20260902", "endTime": "20260903", "status": "SUCCESS", "pageNum": 1, "pageSize": 2}
	second := map[string]any{}
	for k, v := range first {
		second[k] = v
	}
	second["pageNum"] = 2
	r, err := call(t, "list_terminal_transactions", map[string]any{"terminal_serial": "POS-SN-001", "from": "2026-09-02", "to": "2026-09-03", "page_size": 2, "all": true},
		exchange{path: "/openApi/tn/queryBySn", code: "00000", data: map[string]any{"tn": "6123456789", "tnStatus": "ACTIVE"}},
		exchange{path: "/openApi/tn/queryTransactionRecords", want: first, code: "00000", data: map[string]any{"records": []any{rec("p1", "100.00"), rec("p2", "250.50")}, "total": 3}},
		exchange{path: "/openApi/tn/queryTransactionRecords", want: second, code: "00000", data: map[string]any{"records": []any{rec("p3", "1000.00")}, "total": 3}})
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	recs := o["records"].([]any)
	if len(recs) != 3 || o["total"] != 3 || o["tn"] != "6123456789" || recs[1].(map[string]any)["amount"] != int64(25050) {
		t.Errorf("records %v", o)
	}
	if _, ok := o["next_page"]; ok {
		t.Errorf("no page after the last: %v", o)
	}
	// One page, with the account given: no lookup, and the next page named.
	r, err = call(t, "list_terminal_transactions", map[string]any{"terminal_serial": "POS-SN-001", "from": "20260902", "to": "2026-09-02", "tn": "6123456789", "page_size": 1},
		exchange{path: "/openApi/tn/queryTransactionRecords", code: "00000", data: map[string]any{"records": []any{rec("p1", "100.00")}, "total": 2}})
	if err != nil || out(r)["next_page"] != 2 || len(out(r)["records"].([]any)) != 1 {
		t.Errorf("one page: %v %v", r.Output, err)
	}
	// An empty day.
	r, err = call(t, "list_terminal_transactions", map[string]any{"terminal_serial": "POS-SN-001", "from": "2026-09-02", "to": "2026-09-02", "tn": "6123456789"},
		exchange{path: "/openApi/tn/queryTransactionRecords", code: "00000", data: map[string]any{"records": []any{}, "total": 0}})
	if err != nil || len(out(r)["records"].([]any)) != 0 {
		t.Errorf("empty: %v %v", r.Output, err)
	}
	for name, in := range map[string]map[string]any{
		"eight days": {"terminal_serial": "P", "from": "2026-09-01", "to": "2026-09-08", "tn": "1"},
		"backwards":  {"terminal_serial": "P", "from": "2026-09-08", "to": "2026-09-01", "tn": "1"},
		"not a date": {"terminal_serial": "P", "from": "yesterday", "to": "2026-09-01", "tn": "1"},
		"no serial":  {"from": "2026-09-01", "to": "2026-09-01"},
	} {
		if _, err := call(t, "list_terminal_transactions", in); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRequestPayment(t *testing.T) {
	in := map[string]any{"terminal_serial": "POS-SN-001", "amount": 250050, "remark": "Order 17", "out_order_no": ref}
	create := map[string]any{"headMerchantId": "256622040884147", "merchantId": "256622040884148", "outOrderNo": ref, "amount": "2500.50", "currency": "NGN",
		"orderExpireTime": 300, "sceneEnum": "CASH_API", "subSceneEnum": "RETAIL", "sn": "POS-SN-001", "isSplit": "N", "remark": "Order 17"}
	lookup := map[string]any{"headMerchantId": "256622040884147", "merchantId": "256622040884148", "outOrderNo": ref}
	pending := map[string]any{"outOrderNo": ref, "orderNo": "20260704741380171865833472", "status": "PENDING", "amount": "2500.50", "currency": "NGN", "sn": "POS-SN-001", "payType": "POS"}

	r, err := call(t, "request_payment", in,
		exchange{path: "/openApi/order/checkout/createOrder", want: create, code: "00000", data: map[string]any{"orderNo": "20260704741380171865833472"}},
		exchange{path: "/openApi/order/checkout/qryOrderDetail", want: lookup, code: "00000", data: pending})
	if err != nil || out(r)["status"] != "PENDING" || out(r)["order_no"] != "20260704741380171865833472" || out(r)["amount"] != int64(250050) {
		t.Errorf("created: %v %v", r.Output, err)
	}
	// Created, but the read back failed: the order stands.
	r, err = call(t, "request_payment", in,
		exchange{path: "/openApi/order/checkout/createOrder", code: "00000", data: map[string]any{"orderNo": "20260704741380171865833472"}},
		exchange{path: "/openApi/order/checkout/qryOrderDetail", code: "10000", message: "System busy"})
	if err != nil || out(r)["status"] != "INITIAL" || out(r)["order_no"] != "20260704741380171865833472" {
		t.Errorf("created without a readable status: %v %v", r.Output, err)
	}
	// A resend after a lost answer: refused as a duplicate, reported from the record.
	paid := map[string]any{}
	for k, v := range pending {
		paid[k] = v
	}
	paid["status"], paid["payNo"], paid["payMethod"] = "SUCCESS", "P1", "BankCard"
	r, err = call(t, "request_payment", in,
		exchange{path: "/openApi/order/checkout/createOrder", code: "00005", message: "Duplicate business order number"},
		exchange{path: "/openApi/order/checkout/qryOrderDetail", want: lookup, code: "00000", data: paid})
	if err != nil || out(r)["status"] != "SUCCESS" || out(r)["pay_no"] != "P1" {
		t.Errorf("duplicate: %v %v", r.Output, err)
	}
	// Refused as a duplicate, yet nothing under the reference: unknown.
	_, err = call(t, "request_payment", in,
		exchange{path: "/openApi/order/checkout/createOrder", code: "00005", message: "Duplicate business order number"},
		exchange{path: "/openApi/order/checkout/qryOrderDetail", code: "00004", message: "Invalid request parameters"})
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("duplicate but missing: %v", err)
	}
	// OPay busy on the create: it may have made the order, so unknown.
	_, err = call(t, "request_payment", in, exchange{path: "/openApi/order/checkout/createOrder", code: "10000", message: "System busy"})
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("busy: %v", err)
	}
	// A plain refusal.
	_, err = call(t, "request_payment", in, exchange{path: "/openApi/order/checkout/createOrder", code: "C1009", message: "The bank code is not supported"})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("refused: %v", err)
	}
	// Without OPay's sub-scene nothing is sent.
	c := creds(t)
	delete(c, "sub_scene")
	srv := fakeOPay(t)
	_, err = New(Options{BaseURL: srv.URL}).Actions["request_payment"].Execute(context.Background(), connector.Request{Input: in, HTTP: srv.Client(), Credentials: c})
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "sub_scene") {
		t.Errorf("no sub-scene: %v", err)
	}
	for name, bad := range map[string]map[string]any{
		"no terminal": {"amount": 100, "out_order_no": ref},
		"half a kobo": {"terminal_serial": "P", "amount": 100.5, "out_order_no": ref},
		"no key":      {"terminal_serial": "P", "amount": 100},
	} {
		if _, err := call(t, "request_payment", bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s: %v", name, err)
		}
	}
	r, err = call(t, "get_payment_request", map[string]any{"order_no": "20260704741380171865833472"},
		exchange{path: "/openApi/order/checkout/qryOrderDetail", want: map[string]any{"headMerchantId": "256622040884147", "merchantId": "256622040884148", "orderNo": "20260704741380171865833472"}, code: "00000", data: paid})
	if err != nil || out(r)["status"] != "SUCCESS" {
		t.Errorf("get by order_no: %v %v", r.Output, err)
	}
	if _, err := call(t, "get_payment_request", map[string]any{}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("get without a reference: %v", err)
	}
}

// docWebhook is the notification of OPay's General Payment Webhook example.
const docWebhook = `{"data":{"outOrderNo":"2334345345345211","orderNo":"20220704741380171865833472","payNo":"2334345345345245","headMerchantId":"256622022480363",
"merchantId":"256622022480364","status":"SUCCESS","errorMsg":"","senderName":"LUJUN SUN","senderBank":"ubaBank","senderAccount":"2131****09","recipientName":"LUJUN SUN",
"receiptBank":"opay","receiptAccount":"6119787331","amount":"300.00","fee":"1.50","feePattern":"IN_DEDUCT","payMethod":"BankCard","settlementAmount":"298.50",
"settlementFee":"0.00","settlementFeePattern":"OUT_DEDUCT","currency":"NGN","isSplit":"N","productInfo":"","sn":"POS-SN-001","remark":"static virtual account",
"transactionTime":"1692773950143","completedTime":"1692773950143","additionalInformation":"{\"pnr\":\"123\",\"rrn\":\"310137872249\"}"}}`

// delivery wraps a notification in OPay's envelope: encrypted with the
// business's public key, signed by OPay over paramContent + timestamp.
func delivery(t *testing.T, plain string, ts time.Time, authKey string, signer *rsa.PrivateKey) []byte {
	t.Helper()
	testKeys(t)
	content, err := Encrypt(&shopKey.PublicKey, []byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	stamp := strconv.FormatInt(ts.UnixMilli(), 10)
	sig, err := Sign(signer, content+stamp)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"clientAuthKey": authKey, "version": "V1.0.1", "bodyFormat": "JSON", "timestamp": stamp, "paramContent": content, "sign": sig})
	return raw
}

func TestDeliveriesAreVerifiedAndDecrypted(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	c := newConnector(Options{}, func() time.Time { return now })
	verify, enrich := c.Verifiers["payment"], c.Enrichers["payment"]
	cr := creds(t)
	good := delivery(t, docWebhook, now.Add(-time.Minute), cr["client_auth_key"], opayKey)
	if err := verify(cr["opay_public_key"], http.Header{}, good); err != nil {
		t.Fatalf("a genuine delivery: %v", err)
	}
	for name, body := range map[string][]byte{
		"signed by someone else": delivery(t, docWebhook, now, cr["client_auth_key"], shopKey),
		"too old":                delivery(t, docWebhook, now.Add(-6*time.Minute), cr["client_auth_key"], opayKey),
		"from the future":        delivery(t, docWebhook, now.Add(6*time.Minute), cr["client_auth_key"], opayKey),
		"not an envelope":        []byte(docWebhook),
		"tampered":               []byte(strings.Replace(string(good), `"version":"V1.0.1"`, `"version":"V1.0.2"`, 1)),
		"empty":                  nil,
	} {
		if err := verify(cr["opay_public_key"], http.Header{}, body); !errors.Is(err, connector.ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := verify("", http.Header{}, good); !errors.Is(err, connector.ErrBadSignature) {
		t.Errorf("no key: %v", err)
	}

	// The enricher gives the run the plain notification, unwrapped.
	var body any
	_ = json.Unmarshal(good, &body)
	connect := func(creds map[string]string) connector.EventCall {
		return connector.EventCall{Connect: func() (map[string]string, *http.Client, error) { return creds, nil, nil }}
	}
	plain, err := enrich(context.Background(), connect(cr), body)
	if err != nil {
		t.Fatal(err)
	}
	m := plain.(map[string]any)
	if m["payNo"] != "2334345345345245" || m["amount"] != "300.00" || m["sn"] != "POS-SN-001" || m["status"] != "SUCCESS" {
		t.Errorf("decrypted %v", m)
	}
	// The decrypted notification is the normalised sale; the envelope is none.
	act := func(b any) map[string]any {
		return map[string]any{"body": b, "headers": map[string]any{}, "query": map[string]any{}}
	}
	if name, fields, err := c.Manifest.EventOf("payment", act(plain)); err != nil || name != "pos.sale" ||
		fields["reference"] != "2334345345345245" || fields["amount_kobo"] != int64(30000) || fields["settled_kobo"] != int64(29850) {
		t.Errorf("decrypted notification as an event: %s %v %v", name, fields, err)
	}
	if _, _, err := c.Manifest.EventOf("payment", act(body)); !errors.Is(err, connector.ErrNoEvent) {
		t.Errorf("the encrypted envelope as an event: %v", err)
	}
	// Another business's client auth key, or private key: refused.
	other := map[string]string{}
	for k, v := range cr {
		other[k] = v
	}
	other["client_auth_key"] = "someone-else"
	if _, err := enrich(context.Background(), connect(other), body); err == nil || !strings.Contains(err.Error(), "client auth key") {
		t.Errorf("other business's key: %v", err)
	}
	wrongKey, _ := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test key
	der, _ := x509.MarshalPKCS8PrivateKey(wrongKey)
	other["client_auth_key"] = cr["client_auth_key"]
	other["merchant_private_key"] = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if _, err := enrich(context.Background(), connect(other), body); err == nil {
		t.Error("another private key decrypted the notification")
	}
	// A notification without the data wrapper passes through as it is.
	flat := delivery(t, `{"status":"FAIL","orderNo":"1","errorMsg":"Not sufficient funds"}`, now, cr["client_auth_key"], opayKey)
	_ = json.Unmarshal(flat, &body)
	plain, err = enrich(context.Background(), connect(cr), body)
	if err != nil || plain.(map[string]any)["errorMsg"] != "Not sufficient funds" {
		t.Errorf("flat notification: %v %v", plain, err)
	}
}

func TestRoundTripsOPaysOwnExamples(t *testing.T) {
	// The documented response signature string: fields but sign, sorted,
	// nulls left out.
	got := ResponseSigned(map[string]any{"code": "00000", "message": "SUCCESSFUL", "sign": "x", "timestamp": "1283203360637", "data": "Kshn", "extra": nil})
	if got != "code=00000&data=Kshn&message=SUCCESSFUL&timestamp=1283203360637" {
		t.Errorf("signed string %q", got)
	}
	testKeys(t)
	// Long plaintext spans several blocks.
	long := strings.Repeat(`{"k":"v"},`, 100)
	enc, err := Encrypt(&shopKey.PublicKey, []byte(long))
	if err != nil {
		t.Fatal(err)
	}
	dec, err := Decrypt(shopKey, enc)
	if err != nil || string(dec) != long {
		t.Errorf("round trip: %v", err)
	}
	if _, err := Decrypt(shopKey, enc[:40]); err == nil {
		t.Error("a truncated ciphertext decrypted")
	}
	if _, err := Decrypt(shopKey, "!!"); err == nil {
		t.Error("non-Base64 decrypted")
	}
	pemPub, err := PublicKeyPEM(&shopKey.PublicKey)
	if err != nil || !strings.HasPrefix(pemPub, "-----BEGIN PUBLIC KEY-----") {
		t.Errorf("public key PEM: %v", err)
	}
	if _, err := ParsePublicKey(pemPub); err != nil {
		t.Errorf("the PEM reads back: %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	fixture.Capabilities(t, func(base string) *connector.Connector { return New(Options{BaseURL: base}) })
}
