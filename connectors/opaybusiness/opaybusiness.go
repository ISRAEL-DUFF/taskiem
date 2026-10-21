// Package opaybusiness is the OPay Business connector: OPay's offline
// payments APIs for a business with POS terminals and wallets. It receives
// every payment on the account from OPay's general payment webhook, reads
// the balance, any transaction and a terminal's successful transactions by
// day, and pushes payment requests to a terminal. A different product from
// OPay Checkout (package opay). Built from OPay's public documentation at
// documentation.opayweb.com (docs/integrations/opay_business.md).
//
// Auth: RSA (crypto.go). Every request is the plaintext JSON encrypted with
// OPay's public key and signed with the business's private key; every
// answer and every webhook delivery is verified with OPay's public key and
// decrypted with the business's private key. The client auth key names the
// business in a header.
//
// Amounts: OPay's offline APIs give naira with two decimals ("300.00") for
// transactions and kobo for the balance; the connector speaks kobo
// throughout and refuses an amount it cannot convert exactly.
//
// Duplicates: an outOrderNo OPay has seen is refused with code 00005, after
// which the connector looks the order up and reports it, so resending after
// a lost response never asks a terminal twice.
package opaybusiness

import (
	"context"
	"crypto/rsa"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Options configure the connector; BaseURL overrides the host (tests).
type Options struct {
	BaseURL string
}

// New returns the OPay Business connector.
func New(o Options) *connector.Connector { return newConnector(o, time.Now) }

func newConnector(o Options, now func() time.Time) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{base: strings.TrimRight(m.BaseURL, "/"), now: now}
	if o.BaseURL != "" {
		c.base = strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_balance":                connector.ActionFunc(c.getBalance),
		"get_transaction":            connector.ActionFunc(c.getTransaction),
		"get_terminal":               connector.ActionFunc(c.getTerminal),
		"list_terminal_transactions": connector.ActionFunc(c.listTerminalTransactions),
		"request_payment":            connector.ActionFunc(c.requestPayment),
		"get_payment_request":        connector.ActionFunc(c.getPaymentRequest),
	},
		Verifiers:        map[string]connector.WebhookVerifier{"payment": c.verifyDelivery},
		Enrichers:        map[string]connector.Enricher{"payment": decryptDelivery},
		CheckCredentials: checkCredentials,
	}
}

type client struct {
	base string
	now  func() time.Time
}

// keys are a connection's parsed key material.
type keys struct {
	opay       *rsa.PublicKey
	merchant   *rsa.PrivateKey
	clientAuth string
}

func loadKeys(creds map[string]string) (keys, error) {
	var k keys
	var err error
	if k.opay, err = ParsePublicKey(creds["opay_public_key"]); err != nil {
		return k, fmt.Errorf("opay business: OPay's public key: %w: %w", err, effects.ErrFatal)
	}
	if k.merchant, err = ParsePrivateKey(creds["merchant_private_key"]); err != nil {
		return k, fmt.Errorf("opay business: the business's private key: %w: %w", err, effects.ErrFatal)
	}
	k.clientAuth = strings.TrimSpace(creds["client_auth_key"])
	if k.clientAuth == "" {
		return k, fmt.Errorf("opay business: the connection needs client_auth_key: %w", effects.ErrFatal)
	}
	return k, nil
}

// checkCredentials refuses keys that cannot be read, before a connection is
// saved.
func checkCredentials(creds map[string]string) error {
	if _, err := ParsePublicKey(creds["opay_public_key"]); err != nil {
		return fmt.Errorf("OPay's public key: %w", err)
	}
	if _, err := ParsePrivateKey(creds["merchant_private_key"]); err != nil {
		return fmt.Errorf("the business's private key: %w", err)
	}
	return nil
}

// apiError is OPay's answer code when it is not 00000.
type apiError struct {
	code, message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("opay business: %s (code %s)", e.message, e.code)
}

func posError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// answer is OPay's response envelope.
type answer struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Sign      string          `json:"sign"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// call sends one plaintext request inside OPay's envelope and decodes the
// verified, decrypted answer into out. OPay's own codes become *apiError
// with the engine's classification: 10000 ("system busy"), 11004, 11005 and
// A_1001 (expired) are retried for reads and unknown for writes (unknown
// here means the caller decides); anything else is a refusal.
func (c *client) call(ctx context.Context, req connector.Request, k keys, path string, params map[string]any, out any) error {
	plain, err := canonicalJSON(params)
	if err != nil {
		return fmt.Errorf("opay business: encode request: %w: %w", err, effects.ErrFatal)
	}
	content, err := Encrypt(k.opay, plain)
	if err != nil {
		return fmt.Errorf("opay business: encrypt request: %w: %w", err, effects.ErrFatal)
	}
	ts := strconv.FormatInt(c.now().UnixMilli(), 10)
	sig, err := Sign(k.merchant, content+ts)
	if err != nil {
		return fmt.Errorf("opay business: sign request: %w: %w", err, effects.ErrFatal)
	}
	headers := map[string]string{"clientAuthKey": k.clientAuth, "version": version, "bodyFormat": bodyFormat, "timestamp": ts}
	var a answer
	if err := connector.DoJSON(ctx, req.HTTP, http.MethodPost, c.base+path, headers, map[string]string{"paramContent": content, "sign": sig}, &a); err != nil {
		var he *connector.HTTPError
		if errors.As(err, &he) {
			return fmt.Errorf("opay business: %w", he)
		}
		return err
	}
	// Whatever the code, an answer is trusted only once its signature is
	// checked; an unsigned refusal could be anyone's.
	var fields map[string]any
	if raw, err := json.Marshal(a); err == nil {
		_ = json.Unmarshal(raw, &fields)
	}
	if a.Sign == "" || Verify(k.opay, ResponseSigned(fields), a.Sign) != nil {
		return fmt.Errorf("opay business: the answer's signature does not verify with OPay's public key (code %s): %w", a.Code, effects.ErrUnknownOutcome)
	}
	if a.Code != "00000" {
		return &apiError{code: a.Code, message: strings.TrimSpace(a.Message)}
	}
	if out == nil {
		return nil
	}
	var enc string
	if err := json.Unmarshal(a.Data, &enc); err != nil || enc == "" {
		return fmt.Errorf("opay business: the answer carries no encrypted data: %w", effects.ErrUnknownOutcome)
	}
	plainOut, err := Decrypt(k.merchant, enc)
	if err != nil {
		return fmt.Errorf("opay business: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if err := json.Unmarshal(plainOut, out); err != nil {
		return fmt.Errorf("opay business: unreadable answer: %w: %w", err, effects.ErrUnknownOutcome)
	}
	return nil
}

// classify turns an *apiError into the engine's kinds for a read.
func classify(err error) error {
	ae := posError(err)
	if ae == nil {
		return err
	}
	switch ae.code {
	case "10000", "11004", "11005", "A_1001":
		return fmt.Errorf("%w: %w", ae, effects.ErrRetryable)
	}
	return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func (c *client) ids(req connector.Request) (head, branch string, err error) {
	head = strings.TrimSpace(req.Credentials["head_merchant_id"])
	if head == "" {
		return "", "", fmt.Errorf("opay business: the connection needs head_merchant_id: %w", effects.ErrFatal)
	}
	branch = str(req.Input, "merchant_id")
	if branch == "" {
		branch = strings.TrimSpace(req.Credentials["merchant_id"])
	}
	if branch == "" {
		branch = head
	}
	return head, branch, nil
}

func (c *client) getBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	k, err := loadKeys(req.Credentials)
	if err != nil {
		return connector.Response{}, err
	}
	_, branch, err := c.ids(req)
	if err != nil {
		return connector.Response{}, err
	}
	var d struct {
		MerchantID   string `json:"opayMerchantId"`
		MerchantName string `json:"opayMerchantName"`
		Usable       any    `json:"usableAmount"`
		Currency     string `json:"currency"`
		Account      string `json:"opayAccountNo"`
		QueryTime    string `json:"queryTime"`
	}
	if err := c.call(ctx, req, k, "/openApi/merchant/queryMerchantBalance", map[string]any{"opayMerchantId": branch}, &d); err != nil {
		return connector.Response{}, classify(err)
	}
	kobo, err := money.Minor(d.Usable)
	if err != nil {
		return connector.Response{}, fmt.Errorf("opay business: balance: %w: %w", err, effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{"merchant_id": d.MerchantID, "merchant_name": d.MerchantName, "available_balance": kobo,
		"currency": d.Currency, "account_number": d.Account, "queried_at": d.QueryTime}}, nil
}

// transaction is a transaction as OPay's order query, TN records, POS
// query and webhook all describe one (amounts in naira text).
type transaction map[string]any

// output converts a transaction to the connector's shape, kobo throughout.
func (t transaction) output() (map[string]any, error) {
	r := &money.Reader{}
	out := map[string]any{
		"order_no": str(t, "orderNo"), "out_order_no": str(t, "outOrderNo"), "pay_no": str(t, "payNo"),
		"head_merchant_id": str(t, "headMerchantId"), "merchant_id": str(t, "merchantId"),
		"status": strings.ToUpper(str(t, "status")), "error_message": str(t, "errorMsg"),
		"fee_pattern": str(t, "feePattern"), "settlement_fee_pattern": str(t, "settlementFeePattern"),
		"currency": str(t, "currency"), "pay_method": str(t, "payMethod"), "transaction_type": str(t, "transactionType"),
		"transaction_name": str(t, "transactionName"), "pay_type": str(t, "payType"), "terminal_serial": str(t, "sn"), "tn": str(t, "tn"),
		"remark": str(t, "remark"), "sender_name": str(t, "senderName"), "sender_bank": str(t, "senderBank"), "sender_account": str(t, "senderAccount"),
		"recipient_name": str(t, "recipientName"), "recipient_bank": firstOf(t, "recipientBank", "receiptBank"), "recipient_account": firstOf(t, "recipientAccount", "receiptAccount"),
		"transaction_time": rfc3339(t["transactionTime"]), "completed_time": rfc3339(t["completedTime"]),
	}
	if s, ok := t["additionalInformation"].(string); ok {
		out["additional_information"] = s
	}
	for in, key := range map[string]string{"amount": "amount", "fee": "fee", "settlementAmount": "settlement_amount", "settlementFee": "settlement_fee"} {
		if v, ok := t[in]; ok && v != nil && v != "" {
			out[key] = r.Minor(v, 2)
		}
	}
	if r.Err != nil {
		return nil, fmt.Errorf("opay business: %w: %w", r.Err, effects.ErrUnknownOutcome)
	}
	return out, nil
}

func firstOf(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := str(m, k); s != "" {
			return s
		}
	}
	return ""
}

// rfc3339 renders OPay's millisecond epoch (a string or number) as RFC
// 3339 in UTC; anything else passes through as text.
func rfc3339(v any) string {
	var ms int64
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			return strings.TrimSpace(x)
		}
		ms = n
	case float64:
		ms = int64(x)
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return x.String()
		}
		ms = n
	default:
		return fmt.Sprint(v)
	}
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func (c *client) getTransaction(ctx context.Context, req connector.Request) (connector.Response, error) {
	k, err := loadKeys(req.Credentials)
	if err != nil {
		return connector.Response{}, err
	}
	head, branch, err := c.ids(req)
	if err != nil {
		return connector.Response{}, err
	}
	orderNo := str(req.Input, "order_no")
	if orderNo == "" {
		return connector.Response{}, fmt.Errorf("opay business: get_transaction needs order_no: %w", effects.ErrFatal)
	}
	var t transaction
	if err := c.call(ctx, req, k, "/openApi/order/queryDetail", map[string]any{"headMerchantId": head, "merchantId": branch, "orderType": "PAY", "orderNo": orderNo}, &t); err != nil {
		return connector.Response{}, classify(err)
	}
	out, err := t.output()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

type terminal struct {
	Serial    string `json:"sn"`
	TN        string `json:"tn"`
	Status    string `json:"tnStatus"`
	Recipient string `json:"recipientName"`
	Bank      string `json:"receiptBank"`
	BoundAt   string `json:"boundTime"`
	UpdatedAt string `json:"updatedTime"`
}

func (c *client) terminalBySerial(ctx context.Context, req connector.Request, k keys, head, branch, serial string) (terminal, error) {
	var t terminal
	err := c.call(ctx, req, k, "/openApi/tn/queryBySn", map[string]any{"headMerchantId": head, "merchantId": branch, "sn": serial}, &t)
	if err != nil {
		return t, classify(err)
	}
	if t.Status == "" {
		t.Status = "UNBOUND"
	}
	return t, nil
}

func (c *client) getTerminal(ctx context.Context, req connector.Request) (connector.Response, error) {
	k, err := loadKeys(req.Credentials)
	if err != nil {
		return connector.Response{}, err
	}
	head, branch, err := c.ids(req)
	if err != nil {
		return connector.Response{}, err
	}
	serial := str(req.Input, "terminal_serial")
	if serial == "" {
		return connector.Response{}, fmt.Errorf("opay business: get_terminal needs terminal_serial: %w", effects.ErrFatal)
	}
	t, err := c.terminalBySerial(ctx, req, k, head, branch, serial)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"terminal_serial": serial, "tn": t.TN, "status": t.Status, "recipient_name": t.Recipient,
		"bank": t.Bank, "bound_at": t.BoundAt, "updated_at": t.UpdatedAt}}, nil
}

// day reads a YYYY-MM-DD (or yyyyMMdd) date as OPay's yyyyMMdd.
func day(s string) (string, time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("20060102"), t, nil
		}
	}
	return "", time.Time{}, fmt.Errorf("%q is not a date (YYYY-MM-DD)", s)
}

func (c *client) listTerminalTransactions(ctx context.Context, req connector.Request) (connector.Response, error) {
	k, err := loadKeys(req.Credentials)
	if err != nil {
		return connector.Response{}, err
	}
	head, branch, err := c.ids(req)
	if err != nil {
		return connector.Response{}, err
	}
	in := req.Input
	serial := str(in, "terminal_serial")
	if serial == "" {
		return connector.Response{}, fmt.Errorf("opay business: list_terminal_transactions needs terminal_serial: %w", effects.ErrFatal)
	}
	from, fromT, err := day(str(in, "from"))
	if err != nil {
		return connector.Response{}, fmt.Errorf("opay business: from: %w: %w", err, effects.ErrFatal)
	}
	to, toT, err := day(str(in, "to"))
	if err != nil {
		return connector.Response{}, fmt.Errorf("opay business: to: %w: %w", err, effects.ErrFatal)
	}
	if toT.Before(fromT) || toT.Sub(fromT) > 6*24*time.Hour {
		return connector.Response{}, fmt.Errorf("opay business: the days must run from %s to at most six days later, got %s: %w", from, to, effects.ErrFatal)
	}
	tn := str(in, "tn")
	if tn == "" {
		t, err := c.terminalBySerial(ctx, req, k, head, branch, serial)
		if err != nil {
			return connector.Response{}, err
		}
		if t.TN == "" {
			return connector.Response{}, fmt.Errorf("opay business: OPay has no account bound to terminal %s: %w", serial, effects.ErrFatal)
		}
		tn = t.TN
	}
	page, size := 1, 100
	if n, err := money.Minor(in["page"]); err == nil && n >= 1 {
		page = int(n)
	}
	if n, err := money.Minor(in["page_size"]); err == nil && n >= 1 && n <= 100 {
		size = int(n)
	}
	all, _ := in["all"].(bool)
	var records []any
	total := 0
	var next *int
	for {
		params := map[string]any{"headMerchantId": head, "merchantId": branch, "tn": tn, "sn": serial, "startTime": from, "endTime": to,
			"status": "SUCCESS", "pageNum": page, "pageSize": size}
		if p := str(in, "pay_no"); p != "" {
			params["payNo"] = p
		}
		var d struct {
			Records []transaction `json:"records"`
			Total   any           `json:"total"`
		}
		if err := c.call(ctx, req, k, "/openApi/tn/queryTransactionRecords", params, &d); err != nil {
			return connector.Response{}, classify(err)
		}
		for _, t := range d.Records {
			o, err := t.output()
			if err != nil {
				return connector.Response{}, err
			}
			records = append(records, o)
		}
		if n, err := money.Minor(d.Total); err == nil {
			total = int(n)
		}
		next = nil
		if len(d.Records) == size && page*size < total {
			p := page + 1
			next = &p
		}
		if !all || next == nil || len(records) >= 1000 {
			break
		}
		page = *next
	}
	out := map[string]any{"terminal_serial": serial, "tn": tn, "total": total, "page": page, "records": records}
	if records == nil {
		out["records"] = []any{}
	}
	if next != nil {
		out["next_page"] = *next
	}
	return connector.Response{Output: out}, nil
}

// paymentRequest looks a terminal order up by out_order_no or order_no.
func (c *client) paymentRequest(ctx context.Context, req connector.Request, k keys, head, branch, outOrderNo, orderNo string) (transaction, error) {
	params := map[string]any{"headMerchantId": head, "merchantId": branch}
	if outOrderNo != "" {
		params["outOrderNo"] = outOrderNo
	} else {
		params["orderNo"] = orderNo
	}
	var t transaction
	if err := c.call(ctx, req, k, "/openApi/order/checkout/qryOrderDetail", params, &t); err != nil {
		return nil, err
	}
	return t, nil
}

func (c *client) getPaymentRequest(ctx context.Context, req connector.Request) (connector.Response, error) {
	k, err := loadKeys(req.Credentials)
	if err != nil {
		return connector.Response{}, err
	}
	head, branch, err := c.ids(req)
	if err != nil {
		return connector.Response{}, err
	}
	outNo, orderNo := str(req.Input, "out_order_no"), str(req.Input, "order_no")
	if outNo == "" && orderNo == "" {
		return connector.Response{}, fmt.Errorf("opay business: get_payment_request needs out_order_no or order_no: %w", effects.ErrFatal)
	}
	t, err := c.paymentRequest(ctx, req, k, head, branch, outNo, orderNo)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out, err := t.output()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) requestPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	k, err := loadKeys(req.Credentials)
	if err != nil {
		return connector.Response{}, err
	}
	head, branch, err := c.ids(req)
	if err != nil {
		return connector.Response{}, err
	}
	in := req.Input
	ref := str(in, "out_order_no")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("opay business: request_payment needs the engine's out_order_no: %w", effects.ErrFatal)
	}
	serial := str(in, "terminal_serial")
	if serial == "" {
		return connector.Response{}, fmt.Errorf("opay business: request_payment needs terminal_serial: %w", effects.ErrFatal)
	}
	amount, err := money.Minor(in["amount"])
	if err != nil || amount < 1 {
		return connector.Response{}, fmt.Errorf("opay business: amount must be a positive whole number of kobo, got %v: %w", in["amount"], effects.ErrFatal)
	}
	scene := strings.TrimSpace(req.Credentials["sub_scene"])
	if scene == "" {
		return connector.Response{}, fmt.Errorf("opay business: payment requests need the connection's sub_scene, the value OPay assigned when it enabled them: %w", effects.ErrFatal)
	}
	expires := int64(300)
	if n, err := money.Minor(in["expires_in"]); err == nil && n >= 30 && n <= 3600 {
		expires = n
	}
	params := map[string]any{"headMerchantId": head, "merchantId": branch, "outOrderNo": ref, "amount": money.Format(amount, 2), "currency": "NGN",
		"orderExpireTime": expires, "sceneEnum": "CASH_API", "subSceneEnum": scene, "sn": serial, "isSplit": "N"}
	if r := str(in, "remark"); r != "" {
		params["remark"] = r
	}
	var created struct {
		OrderNo string `json:"orderNo"`
	}
	err = c.call(ctx, req, k, "/openApi/order/checkout/createOrder", params, &created)
	ae := posError(err)
	switch {
	case ae != nil && ae.code == "00005":
		// Already ordered under this reference: report the order that exists.
		t, gerr := c.paymentRequest(ctx, req, k, head, branch, ref, "")
		if gerr != nil {
			if posError(gerr) != nil {
				return connector.Response{}, fmt.Errorf("opay business refused %s as a duplicate and has no order under it (%w): %w", ref, gerr, effects.ErrUnknownOutcome)
			}
			return connector.Response{}, gerr
		}
		out, oerr := t.output()
		if oerr != nil {
			return connector.Response{}, oerr
		}
		return connector.Response{Output: out}, nil
	case ae != nil:
		switch ae.code {
		case "10000", "11004", "11005":
			// OPay may have made the order before failing: the engine
			// resends under the same reference and lands on 00005.
			return connector.Response{}, fmt.Errorf("%w: %w", ae, effects.ErrUnknownOutcome)
		case "A_1001":
			return connector.Response{}, fmt.Errorf("%w: %w", ae, effects.ErrRetryable)
		}
		return connector.Response{}, fmt.Errorf("%w: %w", ae, effects.ErrFatal)
	case err != nil:
		return connector.Response{}, err
	}
	// Made: read it back so the output has the status; the order stands
	// whether or not the read succeeds.
	if t, gerr := c.paymentRequest(ctx, req, k, head, branch, ref, ""); gerr == nil {
		if out, oerr := t.output(); oerr == nil {
			if out["order_no"] == "" {
				out["order_no"] = created.OrderNo
			}
			return connector.Response{Output: out}, nil
		}
	}
	return connector.Response{Output: map[string]any{"order_no": created.OrderNo, "out_order_no": ref, "head_merchant_id": head, "merchant_id": branch,
		"status": "INITIAL", "amount": amount, "currency": "NGN", "terminal_serial": serial}}, nil
}

// envelope is a webhook delivery as OPay sends it.
type envelope struct {
	ClientAuthKey string `json:"clientAuthKey"`
	Version       string `json:"version"`
	BodyFormat    string `json:"bodyFormat"`
	Timestamp     string `json:"timestamp"`
	ParamContent  string `json:"paramContent"`
	Sign          string `json:"sign"`
}

// deliveryTolerance is how far a delivery's timestamp may be from now:
// OPay's common parameters say five minutes.
const deliveryTolerance = 5 * time.Minute

// verifyDelivery checks a delivery to the payment trigger: a complete
// envelope, a timestamp within five minutes, and OPay's signature over
// paramContent + timestamp (secret is the connection's OPay public key).
func (c *client) verifyDelivery(secret string, _ http.Header, body []byte) error {
	pub, err := ParsePublicKey(secret)
	if err != nil {
		return fmt.Errorf("%w: %w", connector.ErrBadSignature, err)
	}
	var e envelope
	if err := json.Unmarshal(body, &e); err != nil || e.ParamContent == "" || e.Sign == "" || e.Timestamp == "" || e.ClientAuthKey == "" {
		return fmt.Errorf("%w: not OPay's envelope", connector.ErrBadSignature)
	}
	if e.Version != version || e.BodyFormat != bodyFormat {
		return fmt.Errorf("%w: envelope version %s/%s", connector.ErrBadSignature, e.Version, e.BodyFormat)
	}
	ms, err := strconv.ParseInt(e.Timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: timestamp", connector.ErrBadSignature)
	}
	if d := c.now().Sub(time.UnixMilli(ms)); d > deliveryTolerance || d < -deliveryTolerance {
		return fmt.Errorf("%w: timestamp outside five minutes", connector.ErrBadSignature)
	}
	if err := Verify(pub, e.ParamContent+e.Timestamp, e.Sign); err != nil {
		return fmt.Errorf("%w: %w", connector.ErrBadSignature, err)
	}
	return nil
}

// decryptDelivery is the payment trigger's enricher: it checks the client
// auth key against the connection's and decrypts the notification with
// the business's private key, so a run sees OPay's plain fields (a top
// level "data" object is unwrapped).
func decryptDelivery(_ context.Context, call connector.EventCall, body any) (any, error) {
	m, ok := body.(map[string]any)
	if !ok {
		return nil, errors.New("opay business: the delivery is not an object")
	}
	creds, _, err := call.Connect()
	if err != nil {
		return nil, err
	}
	k, err := loadKeys(creds)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(str(m, "clientAuthKey")), []byte(k.clientAuth)) != 1 {
		return nil, errors.New("opay business: the delivery's client auth key is not this connection's")
	}
	plain, err := Decrypt(k.merchant, str(m, "paramContent"))
	if err != nil {
		return nil, fmt.Errorf("opay business: %w", err)
	}
	var v any
	if err := json.Unmarshal(plain, &v); err != nil {
		return nil, fmt.Errorf("opay business: the decrypted notification is not JSON: %w", err)
	}
	if obj, ok := v.(map[string]any); ok && len(obj) == 1 {
		if data, ok := obj["data"].(map[string]any); ok {
			return data, nil
		}
	}
	return v, nil
}
