// Package opay is the OPay (Nigeria) merchant connector: Cashier and
// bank-transfer collections, payment status, cancellation, refunds and
// payment callbacks. Built from OPay's public documentation at
// documentation.opaycheckout.com (docs/integrations/opay.md).
//
// Authentication: Cashier create carries "Authorization: Bearer <public
// key>"; every other call carries "Authorization: Bearer <signature>", the
// lowercase hex HMAC-SHA512 of the JSON body (keys in alphabetical order)
// keyed with the secret key. Both carry the MerchantId header. The connector
// signs exactly the bytes it sends.
//
// Amounts: OPay takes and returns integer kobo ("cent unit"); a response
// amount that is not a whole number is an error, never a zero.
//
// Duplicates: a reference OPay has seen is refused (code 02004, "the
// payment reference already exists"), after which the connector looks the
// payment (or refund) up by reference and reports it, so resending after a
// lost response never creates a second payment or refund.
package opay

import (
	"context"
	"crypto/hmac"
	"crypto/sha3"
	"crypto/sha512"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"strings"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// SandboxURL is used for connections whose environment is "sandbox".
const SandboxURL = "https://testapi.opaycheckout.com/api/v1/international"

// Options configure the connector; BaseURL overrides both environments
// (tests).
type Options struct {
	BaseURL string
}

// New returns the OPay connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: strings.TrimRight(m.BaseURL, "/"), sandbox: SandboxURL}
	if o.BaseURL != "" {
		c.live, c.sandbox = strings.TrimRight(o.BaseURL, "/"), strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"create_cashier_payment":       connector.ActionFunc(c.createCashierPayment),
		"create_bank_transfer_payment": connector.ActionFunc(c.createBankTransferPayment),
		"get_payment":                  connector.ActionFunc(c.getPayment),
		"close_payment":                connector.ActionFunc(c.closePayment),
		"refund_payment":               connector.ActionFunc(c.refundPayment),
		"get_refund":                   connector.ActionFunc(c.getRefund),
		"verify_callback":              connector.ActionFunc(verifyCallback),
	}}
}

type client struct{ live, sandbox string }

func (c *client) base(req connector.Request) string {
	if strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox") {
		return c.sandbox
	}
	return c.live
}

// OPay's response codes (cashier-create, query-payment-status,
// payment-refund pages).
const (
	codeOK             = "00000"
	codeDuplicateRef   = "02004"
	codeNotFound       = "02006"
	codeServiceDown    = "50003"
	codeNotFoundLegacy = "00012"
	codeNotFoundAlt    = "02812"
)

// refusals are codes after which OPay has done nothing: bad credentials,
// bad parameters, an unsupported method, a merchant not set up for it.
var refusals = map[string]bool{"02000": true, "02001": true, "02002": true, "02003": true, "02007": true}

// apiError is a non-success answer OPay explained.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("opay %d (code %s): %s", e.status, e.code, e.message)
	}
	return fmt.Sprintf("opay %d: %s", e.status, e.message)
}

func (e *apiError) notFound() bool {
	return e != nil && (e.code == codeNotFound || e.code == codeNotFoundLegacy || e.code == codeNotFoundAlt)
}

func (e *apiError) duplicate() bool {
	if e == nil {
		return false
	}
	msg := strings.ToLower(e.message)
	return e.code == codeDuplicateRef || (strings.Contains(msg, "reference") && strings.Contains(msg, "already exist"))
}

func opayError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// Sign is OPay's request signature: lowercase hex HMAC-SHA512 of body keyed
// with the secret key.
func Sign(secretKey string, body []byte) string {
	m := hmac.New(sha512.New, []byte(secretKey))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// auth selects how a call authenticates.
type auth int

const (
	signed    auth = iota // Bearer HMAC-SHA512 of the body (secret key)
	publicKey             // Bearer public key (Cashier create)
)

// do sends one POST and decodes the response's data into out. Failures are
// classified for the engine; answers OPay explains carry *apiError.
func (c *client) do(ctx context.Context, req connector.Request, path string, how auth, body map[string]any, out any) error {
	merchant := strings.TrimSpace(req.Credentials["merchant_id"])
	if merchant == "" {
		return fmt.Errorf("opay: the connection has no merchant_id: %w", effects.ErrFatal)
	}
	// encoding/json writes map keys in alphabetical order (OPay's sorted
	// payload); these exact bytes are signed and sent.
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("opay: encode request: %w: %w", err, effects.ErrFatal)
	}
	var bearer string
	switch how {
	case publicKey:
		bearer = req.Credentials["public_key"]
		if bearer == "" {
			return fmt.Errorf("opay: the connection has no public_key: %w", effects.ErrFatal)
		}
	default:
		key := req.Credentials["secret_key"]
		if key == "" {
			return fmt.Errorf("opay: the connection has no secret_key: %w", effects.ErrFatal)
		}
		bearer = Sign(key, raw)
	}
	var env struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	err = connector.DoJSON(ctx, req.HTTP, http.MethodPost, c.base(req)+path,
		map[string]string{"Authorization": "Bearer " + bearer, "MerchantId": merchant}, json.RawMessage(raw), &env)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(he.Body, &e)
		ae := &apiError{status: he.Status, code: e.Code, message: e.Message}
		if ae.message == "" {
			ae.message = strings.TrimSpace(string(he.Body))
		}
		if he.Status < 500 && (ae.duplicate() || ae.notFound()) {
			return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
		}
		// Keep the transport's classification (429/503 retryable, other
		// 5xx unknown outcome, 4xx fatal) and add OPay's explanation.
		return fmt.Errorf("%w: %w", ae, he)
	}
	if err != nil {
		return err
	}
	if env.Code != codeOK {
		ae := &apiError{status: 200, code: env.Code, message: env.Message}
		switch {
		case refusals[env.Code], ae.duplicate(), ae.notFound():
			return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
		default:
			// 50003 (service not available) and codes OPay does not list:
			// it may have acted, so the outcome is unknown. Reads retry;
			// idempotent writes resend and land on the duplicate path.
			return fmt.Errorf("%w: %w", ae, effects.ErrUnknownOutcome)
		}
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("opay: unreadable data: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func strOr(m map[string]any, k, def string) string {
	if s := str(m, k); s != "" {
		return s
	}
	return def
}

// unreadable is a response whose amounts cannot be read exactly. OPay may
// still have acted, so it is never a failure.
func unreadable(err error) error {
	return fmt.Errorf("opay: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
}

type amount struct {
	Total    any    `json:"total"`
	Currency string `json:"currency"`
}

// payment is OPay's payment record (create, status and close responses).
type payment struct {
	Reference   string `json:"reference"`
	OrderNo     string `json:"orderNo"`
	CashierURL  string `json:"cashierUrl"`
	Status      string `json:"status"`
	OrderStatus string `json:"orderStatus"` // close answers with orderStatus
	Amount      amount `json:"amount"`
	Vat         amount `json:"vat"`
	FailureCode string `json:"failureCode"`
	FailureMsg  string `json:"failureReason"`
	IsVoided    *bool  `json:"isVoided"`
	CreateTime  any    `json:"createTime"`
	NextAction  *struct {
		ActionType            string `json:"actionType"`
		TransferAccountNumber string `json:"transferAccountNumber"`
		TransferBankName      string `json:"transferBankName"`
		ExpiredTimestamp      any    `json:"expiredTimestamp"`
	} `json:"nextAction"`
}

func (p payment) output() (map[string]any, error) {
	status := p.Status
	if status == "" {
		status = p.OrderStatus
	}
	var rd money.Reader
	out := map[string]any{"reference": p.Reference, "order_no": p.OrderNo, "status": strings.ToUpper(status),
		"amount": rd.Minor(p.Amount.Total, 0), "vat": rd.Minor(p.Vat.Total, 0), "currency": p.Amount.Currency,
		"cashier_url": p.CashierURL, "account_number": "", "bank_name": "", "expires_at": int64(0),
		"failure_code": p.FailureCode, "failure_reason": p.FailureMsg, "voided": p.IsVoided != nil && *p.IsVoided,
		"created_at_ms": rd.Minor(p.CreateTime, 0)}
	if a := p.NextAction; a != nil {
		out["account_number"], out["bank_name"] = a.TransferAccountNumber, a.TransferBankName
		out["expires_at"] = rd.Minor(a.ExpiredTimestamp, 0)
	}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

// created reports a payment just created (or found under the reference).
// One OPay reports FAIL or CLOSE did not take money: the step fails with
// OPay's reason. An existing payment for another amount is not this one.
func created(out map[string]any, amount int64) (connector.Response, error) {
	if got, _ := out["amount"].(int64); got != 0 && got != amount {
		return connector.Response{}, fmt.Errorf("opay: reference %s already belongs to a payment of %d kobo, not %d: %w",
			out["reference"], got, amount, effects.ErrFatal)
	}
	switch out["status"] {
	case "FAIL", "CLOSE":
		return connector.Response{}, fmt.Errorf("opay: payment %s is %s: %s %s: %w",
			out["reference"], out["status"], out["failure_code"], out["failure_reason"], effects.ErrFatal)
	}
	return connector.Response{Output: out}, nil
}

func positiveKobo(in map[string]any) (int64, error) {
	n, err := money.Minor(in["amount"])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("opay: amount must be a positive whole number of kobo, got %v: %w", in["amount"], effects.ErrFatal)
	}
	return n, nil
}

func needReference(in map[string]any, what string) (string, error) {
	ref := str(in, "reference")
	if ref == "" {
		return "", fmt.Errorf("opay: %s needs the engine's reference: %w", what, effects.ErrFatal)
	}
	return ref, nil
}

func userInfo(in map[string]any, keys map[string]string) map[string]any {
	u := map[string]any{}
	for field, key := range keys {
		if v := str(in, field); v != "" {
			u[key] = v
		}
	}
	return u
}

func (c *client) createCashierPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, err := needReference(in, "create_cashier_payment")
	if err != nil {
		return connector.Response{}, err
	}
	amt, err := positiveKobo(in)
	if err != nil {
		return connector.Response{}, err
	}
	if str(in, "product_name") == "" || str(in, "product_description") == "" || str(in, "return_url") == "" {
		return connector.Response{}, fmt.Errorf("opay: a Cashier payment needs product_name, product_description and return_url: %w", effects.ErrFatal)
	}
	body := map[string]any{"country": "NG", "reference": ref,
		"amount":    map[string]any{"total": amt, "currency": strOr(in, "currency", "NGN")},
		"returnUrl": str(in, "return_url"),
		"product":   map[string]any{"name": str(in, "product_name"), "description": str(in, "product_description")}}
	for field, key := range map[string]string{"callback_url": "callbackUrl", "cancel_url": "cancelUrl", "pay_method": "payMethod", "display_name": "displayName"} {
		if v := str(in, field); v != "" {
			body[key] = v
		}
	}
	if v, ok := in["expire_minutes"]; ok && v != nil {
		n, err := money.Minor(v)
		if err != nil || n < 1 {
			return connector.Response{}, fmt.Errorf("opay: expire_minutes must be a positive whole number: %w", effects.ErrFatal)
		}
		body["expireAt"] = n
	}
	if u := userInfo(in, map[string]string{"customer_id": "userId", "customer_name": "userName", "customer_mobile": "userMobile", "customer_email": "userEmail"}); len(u) > 0 {
		body["userInfo"] = u
	}
	return c.create(ctx, req, "/cashier/create", publicKey, body, ref, amt)
}

func (c *client) createBankTransferPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, err := needReference(in, "create_bank_transfer_payment")
	if err != nil {
		return connector.Response{}, err
	}
	amt, err := positiveKobo(in)
	if err != nil {
		return connector.Response{}, err
	}
	if str(in, "product_name") == "" {
		return connector.Response{}, fmt.Errorf("opay: a bank-transfer payment needs product_name: %w", effects.ErrFatal)
	}
	product := map[string]any{"name": str(in, "product_name")}
	if d := str(in, "product_description"); d != "" {
		product["description"] = d
	}
	body := map[string]any{"country": "NG", "reference": ref, "payMethod": "BankTransfer",
		"amount": map[string]any{"total": amt, "currency": strOr(in, "currency", "NGN")}, "product": product}
	for field, key := range map[string]string{"callback_url": "callbackUrl", "customer_name": "customerName", "customer_phone": "userPhone"} {
		if v := str(in, field); v != "" {
			body[key] = v
		}
	}
	if v, ok := in["expire_minutes"]; ok && v != nil {
		n, err := money.Minor(v)
		if err != nil || n < 1 {
			return connector.Response{}, fmt.Errorf("opay: expire_minutes must be a positive whole number: %w", effects.ErrFatal)
		}
		body["expireAt"] = n
	}
	if u := userInfo(in, map[string]string{"customer_id": "userId", "customer_name": "userName", "customer_phone": "userMobile", "customer_email": "userEmail"}); len(u) > 0 {
		body["userInfo"] = u
	}
	return c.create(ctx, req, "/payment/create", signed, body, ref, amt)
}

// create sends a payment creation; on OPay refusing the reference as a
// duplicate it reports the payment already made under it.
func (c *client) create(ctx context.Context, req connector.Request, path string, how auth, body map[string]any, ref string, amt int64) (connector.Response, error) {
	var p payment
	err := c.do(ctx, req, path, how, body, &p)
	if ae := opayError(err); ae.duplicate() {
		out, gerr := c.paymentByReference(ctx, req, map[string]any{"reference": ref})
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("opay refused reference %s (%s) and has no payment under it: %w", ref, ae.message, effects.ErrIndeterminate)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return created(out, amt)
	}
	if err != nil {
		return connector.Response{}, err
	}
	if p.Reference == "" {
		p.Reference = ref
	}
	out, err := p.output()
	if err != nil {
		return connector.Response{}, err
	}
	return created(out, amt)
}

func lookupBody(in map[string]any) (map[string]any, error) {
	body := map[string]any{"country": "NG"}
	switch {
	case str(in, "reference") != "":
		body["reference"] = str(in, "reference")
	case str(in, "order_no") != "":
		body["orderNo"] = str(in, "order_no")
	default:
		return nil, fmt.Errorf("opay: give reference or order_no: %w", effects.ErrFatal)
	}
	return body, nil
}

func (c *client) paymentByReference(ctx context.Context, req connector.Request, in map[string]any) (map[string]any, error) {
	body, err := lookupBody(in)
	if err != nil {
		return nil, err
	}
	var p payment
	err = c.do(ctx, req, "/cashier/status", signed, body, &p)
	if ae := opayError(err); ae.notFound() {
		return nil, fmt.Errorf("opay: no payment %v: %w", body, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return p.output()
}

func (c *client) getPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	out, err := c.paymentByReference(ctx, req, req.Input)
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) closePayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	body, err := lookupBody(req.Input)
	if err != nil {
		return connector.Response{}, err
	}
	var p payment
	if err := c.do(ctx, req, "/payment/close", signed, body, &p); err != nil {
		return connector.Response{}, err
	}
	out, err := p.output()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"reference": out["reference"], "order_no": out["order_no"], "status": out["status"], "amount": out["amount"]}}, nil
}

// refund is OPay's refund record.
type refund struct {
	Reference         string `json:"reference"`
	OriginalReference string `json:"originalReference"`
	OrderNo           string `json:"orderNo"`
	OriginalOrderNo   string `json:"originalOrderNo"`
	RefundAmount      amount `json:"refundAmount"`
	Amount            amount `json:"amount"` // the status query's name for it
	OrderStatus       string `json:"orderStatus"`
	ErrorCode         string `json:"errorCode"`
	ErrorMsg          string `json:"errorMsg"`
}

func (r refund) output() (map[string]any, error) {
	a := r.RefundAmount
	if a.Total == nil {
		a = r.Amount
	}
	var rd money.Reader
	out := map[string]any{"reference": r.Reference, "original_reference": r.OriginalReference, "order_no": r.OrderNo,
		"original_order_no": r.OriginalOrderNo, "status": strings.ToUpper(r.OrderStatus), "amount": rd.Minor(a.Total, 0),
		"currency": a.Currency, "error_code": r.ErrorCode, "error_message": r.ErrorMsg}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

// refunded fails the step for a refund OPay reports FAIL (nothing was
// returned), and for a reference already used by a different refund.
func refunded(out map[string]any, original string, amount int64) (connector.Response, error) {
	if got, _ := out["amount"].(int64); (got != 0 && got != amount) || (out["original_reference"] != "" && out["original_reference"] != original) {
		return connector.Response{}, fmt.Errorf("opay: refund reference %s already belongs to a refund of %d kobo on %s: %w",
			out["reference"], got, out["original_reference"], effects.ErrFatal)
	}
	if out["status"] == "FAIL" {
		return connector.Response{}, fmt.Errorf("opay: refund %s failed: %s %s: %w", out["reference"], out["error_code"], out["error_message"], effects.ErrFatal)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) refundPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, err := needReference(in, "refund_payment")
	if err != nil {
		return connector.Response{}, err
	}
	amt, err := positiveKobo(in)
	if err != nil {
		return connector.Response{}, err
	}
	original := str(in, "original_reference")
	if original == "" {
		return connector.Response{}, fmt.Errorf("opay: refund_payment needs original_reference: %w", effects.ErrFatal)
	}
	cb := strOr(in, "callback_url", strings.TrimSpace(req.Credentials["callback_url"]))
	if cb == "" {
		return connector.Response{}, fmt.Errorf("opay: OPay requires a callback URL for refunds; set callback_url on the step or the connection: %w", effects.ErrFatal)
	}
	way := strOr(in, "refund_way", "Original")
	body := map[string]any{"country": "NG", "reference": ref, "originalReference": original, "refundWay": way, "callbackUrl": cb,
		"amount": map[string]any{"total": amt, "currency": strOr(in, "currency", "NGN")}}
	switch way {
	case "Original":
	case "BankAccount":
		if str(in, "bank_code") == "" || str(in, "bank_account_no") == "" {
			return connector.Response{}, fmt.Errorf("opay: a BankAccount refund needs bank_code and bank_account_no: %w", effects.ErrFatal)
		}
		body["receiver"] = map[string]any{"bankCode": str(in, "bank_code"), "bankAccountNo": str(in, "bank_account_no")}
	default:
		return connector.Response{}, fmt.Errorf("opay: refund_way must be Original or BankAccount, got %q: %w", way, effects.ErrFatal)
	}
	if r := str(in, "refund_reason"); r != "" {
		body["refundReason"] = r
	}
	var r refund
	err = c.do(ctx, req, "/payment/refund/create", signed, body, &r)
	if ae := opayError(err); ae.duplicate() {
		out, gerr := c.refundByReference(ctx, req, ref)
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("opay refused refund reference %s (%s) and has no refund under it: %w", ref, ae.message, effects.ErrIndeterminate)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return refunded(out, original, amt)
	}
	if err != nil {
		return connector.Response{}, err
	}
	if r.Reference == "" {
		r.Reference = ref
	}
	out, err := r.output()
	if err != nil {
		return connector.Response{}, err
	}
	return refunded(out, original, amt)
}

func (c *client) refundByReference(ctx context.Context, req connector.Request, ref string) (map[string]any, error) {
	var r refund
	err := c.do(ctx, req, "/payment/refund/query", signed, map[string]any{"country": "NG", "reference": ref}, &r)
	if ae := opayError(err); ae.notFound() {
		return nil, fmt.Errorf("opay: no refund with reference %s: %w", ref, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return r.output()
}

func (c *client) getRefund(ctx context.Context, req connector.Request) (connector.Response, error) {
	ref := str(req.Input, "reference")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("opay: get_refund needs reference: %w", effects.ErrFatal)
	}
	out, err := c.refundByReference(ctx, req, ref)
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

// CallbackSignature is OPay's callback signature: lowercase hex
// HMAC-SHA3-512, keyed with the secret key, of
//
//	{Amount:"<amount>",Currency:"<currency>",Reference:"<reference>",Refunded:<t|f>,Status:"<status>",Timestamp:"<timestamp>",Token:"<token>",TransactionID:"<transactionId>"}
//
// built from the callback's payload (callback-signature page).
func CallbackSignature(secretKey string, payload map[string]any) string {
	refunded := "f"
	if isRefunded(payload["refunded"]) {
		refunded = "t"
	}
	text := func(k string) string {
		switch v := payload[k].(type) {
		case nil:
			return ""
		case string:
			return v
		default:
			return money.Decimal(v)
		}
	}
	signed := fmt.Sprintf(`{Amount:"%s",Currency:"%s",Reference:"%s",Refunded:%s,Status:"%s",Timestamp:"%s",Token:"%s",TransactionID:"%s"}`,
		text("amount"), text("currency"), text("reference"), refunded, text("status"), text("timestamp"), text("token"), text("transactionId"))
	m := hmac.New(func() hash.Hash { return sha3.New512() }, []byte(secretKey))
	m.Write([]byte(signed))
	return hex.EncodeToString(m.Sum(nil))
}

func verifyCallback(_ context.Context, req connector.Request) (connector.Response, error) {
	key := req.Credentials["secret_key"]
	if key == "" {
		return connector.Response{}, fmt.Errorf("opay: the connection has no secret_key: %w", effects.ErrFatal)
	}
	payload, _ := req.Input["payload"].(map[string]any)
	got := strings.ToLower(str(req.Input, "sha512"))
	if payload == nil || got == "" {
		return connector.Response{}, fmt.Errorf("opay: verify_callback needs the callback's payload and sha512: %w", effects.ErrFatal)
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(CallbackSignature(key, payload))) != 1 {
		return connector.Response{}, fmt.Errorf("opay: callback signature does not match; do not act on it: %w", effects.ErrFatal)
	}
	return connector.Response{Output: map[string]any{"valid": true, "reference": str(payload, "reference"),
		"transaction_id": str(payload, "transactionId"), "status": str(payload, "status"), "refunded": isRefunded(payload["refunded"])}}, nil
}

// isRefunded reads the payload's refunded flag: false in OPay's example,
// though its field table types it as a string.
func isRefunded(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true") || x == "t"
	}
	return false
}
