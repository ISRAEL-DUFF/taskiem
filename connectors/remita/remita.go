// Package remita is the Remita (SystemSpecs) connector: invoice (RRR)
// generation, status and cancellation on the first-generation "echannel"
// APIs, and Funds Transfer (single and bulk payments, status, bank list,
// name enquiry) on the RemitaConnect gateway. Built from Remita's public API
// documentation at api.remita.net (docs/integrations/remita.md).
//
// Authentication: gateway calls carry the RemitaConnect secret key in a
// secretKey header. Invoice calls carry "Authorization:
// remitaConsumerKey=<merchantId>,remitaConsumerToken=<apiHash>", the
// apiHash being the lowercase hex SHA-512 of fields concatenated in the
// order Remita gives for each call (see Hash).
//
// Amounts: Taskiem works in kobo; Remita takes and returns naira. Whole
// naira are sent without decimals ("21000"), as in Remita's examples;
// conversions are exact (connectors/internal/money).
//
// Duplicates: an invoice's orderId and a payment's paymentIdentifier are
// the engine's key. Remita refuses an orderId it has seen (028, 055) and a
// repeated payment (23, 26, 94); the connector then looks the invoice or
// payment up by that reference and reports it, and parks the step when
// Remita has nothing under it.
package remita

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Hosts Remita documents. The live invoice host is not in Remita's public
// documentation, so live invoice calls are refused until it is confirmed.
const (
	DemoGatewayURL  = "https://api-demo.systemspecsng.com/services/connect-gateway"
	DemoInvoiceURL  = "https://demo.remita.net/remita/exapp/api/v1/send/api"
	DemoCancelURL   = "https://remitademo.net/remita/exapp/api/v1/send/api"
	liveInvoiceNote = "Remita's public documentation names only the demo host for its invoice APIs; confirm the live host with Remita before using invoices live"
)

// Options configure the connector; BaseURL overrides every host (tests).
type Options struct {
	BaseURL string
}

type hosts struct{ gateway, invoice, cancel string }

// New returns the Remita connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: hosts{gateway: strings.TrimRight(m.BaseURL, "/")},
		demo: hosts{gateway: DemoGatewayURL, invoice: DemoInvoiceURL, cancel: DemoCancelURL}}
	if o.BaseURL != "" {
		b := strings.TrimRight(o.BaseURL, "/")
		c.live, c.demo = hosts{b, b, b}, hosts{b, b, b}
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_banks":        connector.ActionFunc(c.listBanks),
		"resolve_account":   connector.ActionFunc(c.resolveAccount),
		"transfer":          connector.ActionFunc(c.transfer),
		"get_transfer":      connector.ActionFunc(c.getTransfer),
		"bulk_transfer":     connector.ActionFunc(c.bulkTransfer),
		"get_bulk_transfer": connector.ActionFunc(c.getBulkTransfer),
		"generate_invoice":  connector.ActionFunc(c.generateInvoice),
		"get_invoice":       connector.ActionFunc(c.getInvoice),
		"cancel_invoice":    connector.ActionFunc(c.cancelInvoice),
	}}
}

type client struct{ live, demo hosts }

func (c *client) hosts(req connector.Request) hosts {
	switch strings.ToLower(strings.TrimSpace(req.Credentials["environment"])) {
	case "demo", "sandbox", "test":
		return c.demo
	}
	return c.live
}

// apiError is a non-success answer Remita explained.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("remita %d (code %s): %s", e.status, e.code, e.message)
	}
	return fmt.Sprintf("remita %d: %s", e.status, e.message)
}

func remitaError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// Hash is Remita's apiHash: lowercase hex SHA-512 of the parts
// concatenated, e.g. merchantId+serviceTypeId+orderId+amount+apiKey for an
// invoice, rrr+apiKey+merchantId for its status.
func Hash(parts ...string) string {
	sum := sha512.Sum512([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

// send makes one request and returns the 2xx body. Failures are classified
// like connector.DoJSON: transport errors by ClassifyTransport, 429 and 503
// retryable, other 5xx unknown outcome, other 4xx fatal.
func send(ctx context.Context, hc *http.Client, method, rawURL string, headers map[string]string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("remita: encode request: %w: %w", err, effects.ErrFatal)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return nil, fmt.Errorf("remita: %w: %w", err, effects.ErrFatal)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, connector.ClassifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("remita: read response: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return raw, nil
	}
	var e struct {
		Status     string `json:"status"`
		StatusCode string `json:"statuscode"`
		Message    string `json:"message"`
	}
	_ = json.Unmarshal(unwrapJSONP(raw), &e)
	// Gateway errors: {status: code, message}. Invoice errors: {statuscode:
	// code, status: message}.
	ae := &apiError{status: resp.StatusCode, code: e.Status, message: e.Message}
	if e.StatusCode != "" {
		ae.code = e.StatusCode
		if ae.message == "" {
			ae.message = e.Status
		}
	}
	if ae.message == "" {
		ae.message = strings.TrimSpace(string(raw))
		if len(ae.message) > 300 {
			ae.message = ae.message[:300]
		}
	}
	kind := effects.ErrFatal
	switch {
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusServiceUnavailable:
		kind = effects.ErrRetryable
	case resp.StatusCode >= 500:
		kind = effects.ErrUnknownOutcome
	}
	return nil, fmt.Errorf("%w: %w", ae, kind)
}

// unwrapJSONP strips the "jsonp (…)" wrapper some invoice responses carry.
func unwrapJSONP(raw []byte) []byte {
	t := bytes.TrimSpace(raw)
	if !bytes.HasPrefix(t, []byte("jsonp")) {
		return t
	}
	i, j := bytes.IndexByte(t, '('), bytes.LastIndexByte(t, ')')
	if i < 0 || j <= i {
		return t
	}
	return bytes.TrimSpace(t[i+1 : j])
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

// unreadable is a response whose amounts cannot be read exactly. Remita may
// still have acted, so it is never a failure.
func unreadable(err error) error {
	return fmt.Errorf("remita: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
}

func positiveKobo(v any, what string) (int64, error) {
	n, err := money.Minor(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("remita: %s must be a positive whole number of kobo, got %v: %w", what, v, effects.ErrFatal)
	}
	return n, nil
}

// nairaText renders kobo as Remita's invoice amount: whole naira without
// decimals ("21000"), otherwise two decimal places ("21000.50").
func nairaText(kobo int64) string {
	if kobo%100 == 0 {
		return strconv.FormatInt(kobo/100, 10)
	}
	return money.Format(kobo, 2)
}

// nairaNumber is nairaText as a JSON number, for Funds Transfer amounts.
func nairaNumber(kobo int64) json.Number { return json.Number(nairaText(kobo)) }

// ---- Funds Transfer (gateway) ----

type envelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e envelope) hasData() bool {
	return len(e.Data) > 0 && string(e.Data) != "null"
}

// gateway sends one Funds Transfer request and decodes the envelope. A
// non-"00" envelope is returned as is, for the caller to read.
func (c *client) gateway(ctx context.Context, req connector.Request, method, path string, body any) (envelope, error) {
	key := req.Credentials["secret_key"]
	if key == "" {
		return envelope{}, fmt.Errorf("remita: the connection has no secret_key (RemitaConnect): %w", effects.ErrFatal)
	}
	raw, err := send(ctx, req.HTTP, method, c.hosts(req).gateway+path, map[string]string{"secretKey": key}, body)
	if err != nil {
		return envelope{}, err
	}
	var env envelope
	if err := json.Unmarshal(bytes.TrimSpace(raw), &env); err != nil {
		return envelope{}, fmt.Errorf("remita: unreadable response: %w: %w", err, effects.ErrUnknownOutcome)
	}
	return env, nil
}

// readGateway is gateway for reads and lookups: anything but "00" is an
// error carrying Remita's code.
func (c *client) readGateway(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	env, err := c.gateway(ctx, req, method, path, body)
	if err != nil {
		return err
	}
	if env.Status != "00" {
		kind := effects.ErrFatal
		if !failedCodes[env.Status] && !notFoundCodes[env.Status] {
			kind = effects.ErrUnknownOutcome
		}
		return fmt.Errorf("%w: %w", &apiError{status: 200, code: env.Status, message: env.Message}, kind)
	}
	if out != nil && env.hasData() {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("remita: unreadable data: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	var d struct {
		Banks []struct {
			BankCode string `json:"bankCode"`
			BankName string `json:"bankName"`
		} `json:"banks"`
	}
	if err := c.readGateway(ctx, req, http.MethodGet, "/api/v1/interbank/transaction/bank/list", nil, &d); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, len(d.Banks))
	for i, b := range d.Banks {
		out[i] = map[string]any{"code": b.BankCode, "name": b.BankName}
	}
	return connector.Response{Output: map[string]any{"banks": out}}, nil
}

func (c *client) resolveAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	acct, bank := str(req.Input, "account_number"), str(req.Input, "bank_code")
	if acct == "" || bank == "" {
		return connector.Response{}, fmt.Errorf("remita: resolve_account needs account_number and bank_code: %w", effects.ErrFatal)
	}
	var d struct {
		Status        string `json:"status"`
		StatusMessage string `json:"statusMessage"`
		NameOnAccount string `json:"nameOnAccount"`
		AccountNumber string `json:"accountNumber"`
		BankCode      string `json:"bankCode"`
		Valid         *bool  `json:"valid"`
	}
	body := map[string]any{"destinationBankCode": bank, "destinationAccountNumber": acct}
	if err := c.readGateway(ctx, req, http.MethodPost, "/api/v1/interbank/name/enquiry", body, &d); err != nil {
		return connector.Response{}, err
	}
	if (d.Valid != nil && !*d.Valid) || (d.Status != "" && d.Status != "00") || d.NameOnAccount == "" {
		return connector.Response{}, fmt.Errorf("remita: account %s at bank %s could not be verified (%s %s): %w", acct, bank, d.Status, d.StatusMessage, effects.ErrFatal)
	}
	return connector.Response{Output: map[string]any{"account_name": d.NameOnAccount, "account_number": d.AccountNumber, "bank_code": d.BankCode}}, nil
}

// Single-payment response codes (Funds Transfer > Response Codes). Codes
// Remita does not list are pending, as it advises.
var (
	pendingCodes = map[string]bool{"01": true, "09": true}
	failedCodes  = map[string]bool{
		"03": true, "05": true, "06": true, "07": true, "08": true, "12": true, "13": true, "14": true, "15": true, "16": true,
		"17": true, "18": true, "19": true, "21": true, "22": true, "23": true, "24": true, "25": true, "26": true, "30": true,
		"31": true, "33": true, "34": true, "35": true, "36": true, "37": true, "38": true, "39": true, "40": true, "51": true,
		"57": true, "58": true, "61": true, "63": true, "65": true, "68": true, "69": true, "70": true, "71": true, "80": true,
		"81": true, "82": true, "91": true, "92": true, "94": true, "96": true, "97": true, "102": true, "104": true,
	}
	// A repeated paymentIdentifier: "Payment Reference Exist", "Duplicate
	// record", "Duplicate transaction". The original may well have paid.
	duplicateCodes = map[string]bool{"23": true, "26": true, "94": true}
	// "Payment Reference Cannot Be Found", "Unable to locate record".
	notFoundCodes = map[string]bool{"19": true, "25": true}
)

func outcome(code string) string {
	switch {
	case code == "00":
		return "successful"
	case pendingCodes[code]:
		return "pending"
	case failedCodes[code]:
		return "failed"
	}
	return "pending"
}

type transferData struct {
	Amount              any    `json:"amount"`
	PaymentIdentifier   string `json:"paymentIdentifier"`
	AuthorizationID     string `json:"authorizationId"`
	TransactionState    string `json:"transactionState"`
	ResponseCode        string `json:"responseCode"`
	ResponseMessage     string `json:"responseMessage"`
	DebitCompleted      *bool  `json:"debitCompleted"`
	Reversed            *bool  `json:"reversed"`
	DestinationAccount  string `json:"destinationAccount"`
	SourceAccountNumber string `json:"sourceAccountNumber"`
}

// transferOutput reads a single payment from an envelope. The transfer's
// own code is data.responseCode; without one, a failure code in the
// envelope counts, and anything else is pending.
func transferOutput(ref string, env envelope) (map[string]any, error) {
	var d transferData
	if env.hasData() {
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return nil, fmt.Errorf("remita: unreadable data: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	code, msg := d.ResponseCode, d.ResponseMessage
	if code == "" && failedCodes[env.Status] {
		code, msg = env.Status, env.Message
	}
	status := "pending"
	if code != "" {
		status = outcome(code)
	}
	var rd money.Reader
	out := map[string]any{"payment_identifier": ref, "remita_payment_identifier": d.PaymentIdentifier, "status": status,
		"response_code": code, "response_message": msg, "transaction_state": d.TransactionState,
		"amount": rd.Minor(d.Amount, 2), "authorization_id": d.AuthorizationID,
		"debit_completed": d.DebitCompleted != nil && *d.DebitCompleted, "reversed": d.Reversed != nil && *d.Reversed}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

// settled fails the step for a payment Remita reports failed: no money
// moved, and Remita's reason is the error.
func settled(out map[string]any) (connector.Response, error) {
	if out["status"] == "failed" {
		return connector.Response{}, fmt.Errorf("remita: payment %s failed (code %s): %s: %w",
			out["payment_identifier"], out["response_code"], out["response_message"], effects.ErrFatal)
	}
	return connector.Response{Output: out}, nil
}

func source(req connector.Request, body map[string]any, keys [3]string) {
	for i, field := range []string{"source_account_number", "source_bank_code", "source_account_name"} {
		v := str(req.Input, field)
		if v == "" {
			v = strings.TrimSpace(req.Credentials[field])
		}
		if v != "" {
			body[keys[i]] = v
		}
	}
}

func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "payment_identifier")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("remita: transfer needs the engine's payment_identifier: %w", effects.ErrFatal)
	}
	amount, err := positiveKobo(in["amount"], "amount")
	if err != nil {
		return connector.Response{}, err
	}
	for _, f := range []string{"account_number", "bank_code", "account_name", "narration"} {
		if str(in, f) == "" {
			return connector.Response{}, fmt.Errorf("remita: transfer needs %s: %w", f, effects.ErrFatal)
		}
	}
	body := map[string]any{"destinationAccountNumber": str(in, "account_number"), "destinationBankCode": str(in, "bank_code"),
		"destinationAccountName": str(in, "account_name"), "amount": nairaNumber(amount), "transactionDescription": str(in, "narration"),
		"paymentIdentifier": ref, "channel": "1", "isApproval": false, "payByTransfer": false}
	source(req, body, [3]string{"sourceAccountNumber", "sourceBankCode", "sourceAccountName"})
	env, err := refusedAsEnvelope(c.gateway(ctx, req, http.MethodPost, "/api/v1/interbank/fund/transfer", body))
	if err != nil {
		return connector.Response{}, err
	}
	out, err := transferOutput(ref, env)
	if err != nil {
		return connector.Response{}, err
	}
	if duplicateCodes[out["response_code"].(string)] {
		// Already sent under this reference: report that payment.
		found, gerr := c.transferByReference(ctx, req, ref)
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("remita refused payment %s as a duplicate (code %s: %s) and reports nothing under it; check it in Remita, then resolve the step: %w",
				ref, out["response_code"], out["response_message"], effects.ErrIndeterminate)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return settled(found)
	}
	return settled(out)
}

func (c *client) transferByReference(ctx context.Context, req connector.Request, ref string) (map[string]any, error) {
	env, err := c.gateway(ctx, req, http.MethodGet, "/api/v1/interbank/query-transaction/"+url.PathEscape(ref), nil)
	if ae := remitaError(err); ae != nil && ae.status == http.StatusNotFound {
		return nil, fmt.Errorf("remita: no payment %s: %w", ref, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if !env.hasData() {
		if notFoundCodes[env.Status] {
			return nil, fmt.Errorf("remita: no payment %s (%s): %w", ref, env.Message, connector.ErrNotFound)
		}
		return nil, fmt.Errorf("remita: status query for %s answered %s %q without the payment: %w", ref, env.Status, env.Message, effects.ErrUnknownOutcome)
	}
	return transferOutput(ref, env)
}

// refusedAsEnvelope reads a 4xx that carries a duplicate code as the
// envelope it is, so a repeated reference is looked up rather than failed.
func refusedAsEnvelope(env envelope, err error) (envelope, error) {
	if ae := remitaError(err); ae != nil && ae.status < 500 && duplicateCodes[ae.code] {
		return envelope{Status: ae.code, Message: ae.message}, nil
	}
	return env, err
}

func (c *client) getTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	ref := str(req.Input, "payment_identifier")
	if ref == "" {
		return connector.Response{}, fmt.Errorf("remita: get_transfer needs payment_identifier: %w", effects.ErrFatal)
	}
	out, err := c.transferByReference(ctx, req, ref)
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

// ItemIdentifier derives a bulk item's paymentIdentifier from the batch's:
// the first 32 hex characters of SHA-256("<batch>:<index>"), so a resent
// batch carries the same item references.
func ItemIdentifier(batch string, i int) string {
	sum := sha256.Sum256([]byte(batch + ":" + strconv.Itoa(i)))
	return hex.EncodeToString(sum[:])[:32]
}

// Bulk states (Funds Transfer > Response Codes > Bulk Payment).
func bulkOutcome(state string) string {
	switch strings.ToUpper(state) {
	case "SUCCESS", "CREDIT_SUCCESS":
		return "successful"
	case "FAILED", "DEBIT_FAILED", "CREDIT_FAILED":
		return "failed"
	}
	return "pending"
}

func (c *client) bulkTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	batch := str(in, "batch_payment_identifier")
	if batch == "" {
		return connector.Response{}, fmt.Errorf("remita: bulk_transfer needs the engine's batch_payment_identifier: %w", effects.ErrFatal)
	}
	items, _ := in["transfers"].([]any)
	if len(items) == 0 || str(in, "narration") == "" {
		return connector.Response{}, fmt.Errorf("remita: bulk_transfer needs transfers and narration: %w", effects.ErrFatal)
	}
	var total int64
	txs := make([]any, len(items))
	sent := make([]any, len(items))
	for i, it := range items {
		m, _ := it.(map[string]any)
		amount, err := positiveKobo(m["amount"], fmt.Sprintf("transfers[%d].amount", i))
		if err != nil {
			return connector.Response{}, err
		}
		for _, f := range []string{"account_number", "bank_code", "account_name"} {
			if str(m, f) == "" {
				return connector.Response{}, fmt.Errorf("remita: transfers[%d] needs %s: %w", i, f, effects.ErrFatal)
			}
		}
		id := ItemIdentifier(batch, i)
		narration := str(m, "narration")
		if narration == "" {
			narration = str(in, "narration")
		}
		total += amount
		txs[i] = map[string]any{"amount": nairaNumber(amount), "paymentIdentifier": id, "destinationAccount": str(m, "account_number"),
			"destinationAccountName": str(m, "account_name"), "destinationBankCode": str(m, "bank_code"), "destinationNarration": narration}
		sent[i] = map[string]any{"payment_identifier": id, "status": "", "outcome": "pending", "account_number": str(m, "account_number"), "amount": amount}
	}
	body := map[string]any{"batchPaymentIdentifier": batch, "totalAmount": nairaNumber(total), "currency": "NGN",
		"sourceNarration": str(in, "narration"), "approval": false, "payByTransfer": false, "transactions": txs}
	if r := str(in, "custom_reference"); r != "" {
		body["customReference"] = r
	}
	source(req, body, [3]string{"sourceAccount", "sourceBankCode", "sourceAccountName"})
	env, err := refusedAsEnvelope(c.gateway(ctx, req, http.MethodPost, "/api/v1/interbank/bulk/fund-transfer", body))
	if err != nil {
		return connector.Response{}, err
	}
	if env.Status != "00" {
		msg := strings.ToLower(env.Message)
		if duplicateCodes[env.Status] || strings.Contains(msg, "duplicate") || strings.Contains(msg, "exist") {
			found, gerr := c.bulkByReference(ctx, req, batch)
			if errors.Is(gerr, connector.ErrNotFound) {
				return connector.Response{}, fmt.Errorf("remita refused batch %s as a duplicate (code %s: %s) and reports nothing under it; check it in Remita, then resolve the step: %w",
					batch, env.Status, env.Message, effects.ErrIndeterminate)
			}
			if gerr != nil {
				return connector.Response{}, gerr
			}
			return bulkSettled(found)
		}
		ae := &apiError{status: 200, code: env.Status, message: env.Message}
		if failedCodes[env.Status] {
			return connector.Response{}, fmt.Errorf("remita: batch %s refused: %w: %w", batch, ae, effects.ErrFatal)
		}
		return connector.Response{}, fmt.Errorf("remita: batch %s: %w: %w", batch, ae, effects.ErrUnknownOutcome)
	}
	var d struct {
		TotalAmount            any    `json:"totalAmount"`
		BatchPaymentIdentifier string `json:"batchPaymentIdentifier"`
		TransactionState       string `json:"transactionState"`
		TotalTransactions      any    `json:"totalTransactions"`
	}
	if env.hasData() {
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return connector.Response{}, fmt.Errorf("remita: unreadable data: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	var rd money.Reader
	out := map[string]any{"batch_payment_identifier": batch, "remita_batch_identifier": d.BatchPaymentIdentifier,
		"status": d.TransactionState, "settled": false, "total_amount": rd.Minor(d.TotalAmount, 2),
		"total_debit_amount": int64(0), "total_credited_amount": int64(0), "transaction_count": rd.Minor(d.TotalTransactions, 0),
		"successful_transactions": int64(0), "failed_transactions": int64(0), "transfers": sent, "more_transfers": false}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return bulkSettled(out)
}

// bulkSettled fails the step when the whole batch failed before paying
// anyone (the debit failed): nothing moved.
func bulkSettled(out map[string]any) (connector.Response, error) {
	switch strings.ToUpper(fmt.Sprint(out["status"])) {
	case "DEBIT_FAILED":
		return connector.Response{}, fmt.Errorf("remita: batch %s failed: the debit failed: %w", out["batch_payment_identifier"], effects.ErrFatal)
	case "FAILED":
		if credited, _ := out["total_credited_amount"].(int64); credited == 0 && out["successful_transactions"] == int64(0) {
			return connector.Response{}, fmt.Errorf("remita: batch %s failed and credited nothing: %w", out["batch_payment_identifier"], effects.ErrFatal)
		}
	}
	return connector.Response{Output: out}, nil
}

func (c *client) bulkByReference(ctx context.Context, req connector.Request, batch string) (map[string]any, error) {
	var s struct {
		BatchPaymentIdentifier string `json:"batchPaymentIdentifier"`
		Status                 string `json:"status"`
		TotalDebitAmount       any    `json:"totalDebitAmount"`
		TotalCreditedAmount    any    `json:"totalCreditedAmount"`
		TransactionCount       any    `json:"transactionCount"`
		SuccessfulTransactions any    `json:"successfulTransactions"`
		FailedTransactions     any    `json:"failedTransactions"`
	}
	err := c.readGateway(ctx, req, http.MethodGet, "/api/v1/interbank/bulk/query-transaction/"+url.PathEscape(batch), nil, &s)
	if ae := remitaError(err); ae != nil && (ae.status == http.StatusNotFound || notFoundCodes[ae.code]) {
		return nil, fmt.Errorf("remita: no batch %s: %w", batch, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if s.Status == "" && s.BatchPaymentIdentifier == "" {
		return nil, fmt.Errorf("remita: no batch %s: %w", batch, connector.ErrNotFound)
	}
	var d struct {
		Transactions []struct {
			PaymentIdentifier string `json:"paymentIdentifier"`
			Status            string `json:"status"`
		} `json:"transactions"`
		Pagination struct {
			TotalPages int `json:"totalPages"`
		} `json:"pagination"`
	}
	if err := c.readGateway(ctx, req, http.MethodGet, "/api/v1/interbank/bulk/transaction/detail/"+url.PathEscape(batch), nil, &d); err != nil {
		return nil, err
	}
	items := make([]any, len(d.Transactions))
	for i, t := range d.Transactions {
		items[i] = map[string]any{"payment_identifier": t.PaymentIdentifier, "status": t.Status, "outcome": bulkOutcome(t.Status), "account_number": "", "amount": int64(0)}
	}
	var rd money.Reader
	state := strings.ToUpper(s.Status)
	out := map[string]any{"batch_payment_identifier": batch, "remita_batch_identifier": s.BatchPaymentIdentifier, "status": s.Status,
		"settled":      state == "COMPLETED" || state == "SUCCESS" || state == "FAILED" || state == "DEBIT_FAILED",
		"total_amount": rd.Minor(s.TotalDebitAmount, 2), "total_debit_amount": rd.Minor(s.TotalDebitAmount, 2),
		"total_credited_amount": rd.Minor(s.TotalCreditedAmount, 2), "transaction_count": rd.Minor(s.TransactionCount, 0),
		"successful_transactions": rd.Minor(s.SuccessfulTransactions, 0), "failed_transactions": rd.Minor(s.FailedTransactions, 0),
		"transfers": items, "more_transfers": d.Pagination.TotalPages > 1}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

func (c *client) getBulkTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	batch := str(req.Input, "batch_payment_identifier")
	if batch == "" {
		return connector.Response{}, fmt.Errorf("remita: get_bulk_transfer needs batch_payment_identifier: %w", effects.ErrFatal)
	}
	out, err := c.bulkByReference(ctx, req, batch)
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

// ---- Invoices (echannel) ----

// Invoice status codes (Invoice Generation > Response Codes).
const (
	codeRRRGenerated = "025"
	codeDuplicateRef = "028" // Duplicate Order Ref
	codeRRRExists    = "055" // RRR Already Exist for the orderId
)

var (
	// "Error Processing Request", internal error, "Unknown
	// Error": Remita may have acted.
	invoiceUnknownCodes = map[string]bool{"25": true, "998": true, "999": true}
	// "Invalid RRR", "Invalid Merchant or OrderId", "No such Order
	// Request", "NO AVAILABLE RECORD".
	invoiceNotFoundCodes = map[string]bool{"022": true, "023": true, "026": true, "074": true}
	invoiceFailedCodes   = map[string]bool{"02": true, "012": true, "046": true, "059": true}
)

func invoiceStatus(code string) string {
	switch {
	case code == "00" || code == "01":
		return "paid" // Remita: "00" and "01" mean the RRR is paid
	case invoiceFailedCodes[code]:
		return "failed"
	}
	return "pending"
}

type invoiceCreds struct{ merchant, apiKey string }

func (c *client) invoiceBase(req connector.Request, cancel bool) (string, invoiceCreds, error) {
	ic := invoiceCreds{merchant: strings.TrimSpace(req.Credentials["merchant_id"]), apiKey: req.Credentials["api_key"]}
	if ic.merchant == "" || ic.apiKey == "" {
		return "", ic, fmt.Errorf("remita: invoices need the connection's merchant_id and api_key: %w", effects.ErrFatal)
	}
	h := c.hosts(req)
	base := h.invoice
	if cancel {
		base = h.cancel
	}
	if base == "" {
		return "", ic, fmt.Errorf("remita: %s: %w", liveInvoiceNote, effects.ErrFatal)
	}
	return base, ic, nil
}

func consumer(merchant, hash string) map[string]string {
	return map[string]string{"Authorization": "remitaConsumerKey=" + merchant + ",remitaConsumerToken=" + hash}
}

// invoiceJSON decodes an invoice response, with or without its jsonp
// wrapper.
func invoiceJSON(raw []byte, out any) error {
	if err := json.Unmarshal(unwrapJSONP(raw), out); err != nil {
		return fmt.Errorf("remita: unreadable response: %w: %w", err, effects.ErrUnknownOutcome)
	}
	return nil
}

func (c *client) generateInvoice(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	order := str(in, "order_id")
	if order == "" {
		return connector.Response{}, fmt.Errorf("remita: generate_invoice needs the engine's order_id: %w", effects.ErrFatal)
	}
	amount, err := positiveKobo(in["amount"], "amount")
	if err != nil {
		return connector.Response{}, err
	}
	for _, f := range []string{"payer_name", "payer_email", "payer_phone", "description"} {
		if str(in, f) == "" {
			return connector.Response{}, fmt.Errorf("remita: generate_invoice needs %s: %w", f, effects.ErrFatal)
		}
	}
	base, ic, err := c.invoiceBase(req, false)
	if err != nil {
		return connector.Response{}, err
	}
	service := str(in, "service_type_id")
	if service == "" {
		service = strings.TrimSpace(req.Credentials["service_type_id"])
	}
	if service == "" {
		return connector.Response{}, fmt.Errorf("remita: no service_type_id in the step or the connection: %w", effects.ErrFatal)
	}
	amountText := nairaText(amount)
	body := map[string]any{"serviceTypeId": service, "amount": amountText, "orderId": order, "payerName": str(in, "payer_name"),
		"payerEmail": str(in, "payer_email"), "payerPhone": str(in, "payer_phone"), "description": str(in, "description")}
	if d := str(in, "expiry_date"); d != "" {
		body["expiryDate"] = d
	}
	if items, _ := in["line_items"].([]any); len(items) > 0 {
		lines, err := lineItems(items, amount)
		if err != nil {
			return connector.Response{}, err
		}
		body["lineItems"] = lines
	}
	if fields, _ := in["custom_fields"].([]any); len(fields) > 0 {
		cf := make([]any, len(fields))
		for i, f := range fields {
			m, _ := f.(map[string]any)
			if str(m, "name") == "" {
				return connector.Response{}, fmt.Errorf("remita: custom_fields[%d] needs a name: %w", i, effects.ErrFatal)
			}
			t := str(m, "type")
			if t == "" {
				t = "ALL"
			}
			cf[i] = map[string]any{"name": str(m, "name"), "value": str(m, "value"), "type": t}
		}
		body["customFields"] = cf
	}
	hash := Hash(ic.merchant, service, order, amountText, ic.apiKey)
	raw, err := send(ctx, req.HTTP, http.MethodPost, base+"/echannelsvc/merchant/api/paymentinit", consumer(ic.merchant, hash), body)
	var r struct {
		StatusCode string `json:"statuscode"`
		RRR        string `json:"RRR"`
		Status     string `json:"status"`
	}
	if ae := remitaError(err); ae != nil && ae.status < 500 && (ae.code == codeDuplicateRef || ae.code == codeRRRExists) {
		r.StatusCode, r.Status = ae.code, ae.message
	} else if err != nil {
		return connector.Response{}, err
	} else if err := invoiceJSON(raw, &r); err != nil {
		return connector.Response{}, err
	}
	switch {
	case r.StatusCode == codeRRRGenerated && r.RRR != "":
		return connector.Response{Output: map[string]any{"rrr": r.RRR, "order_id": order, "status": "pending", "status_code": r.StatusCode,
			"message": r.Status, "amount": amount, "payment_date": ""}}, nil
	case r.StatusCode == codeDuplicateRef || r.StatusCode == codeRRRExists:
		// Already generated under this orderId: report that invoice.
		found, gerr := c.invoiceBy(ctx, req, "", order)
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("remita refused order %s (%s %s) and reports no invoice under it; check it in Remita, then resolve the step: %w",
				order, r.StatusCode, r.Status, effects.ErrIndeterminate)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		if got, _ := found["amount"].(int64); got != 0 && got != amount {
			return connector.Response{}, fmt.Errorf("remita: order %s already has invoice %s for %d kobo, not %d: %w", order, found["rrr"], got, amount, effects.ErrFatal)
		}
		return connector.Response{Output: found}, nil
	case invoiceUnknownCodes[r.StatusCode]:
		return connector.Response{}, fmt.Errorf("remita: invoice %s: %w: %w", order, &apiError{status: 200, code: r.StatusCode, message: r.Status}, effects.ErrUnknownOutcome)
	case r.StatusCode == codeRRRGenerated:
		return connector.Response{}, fmt.Errorf("remita: invoice %s generated without an RRR in the response: %w", order, effects.ErrUnknownOutcome)
	}
	return connector.Response{}, fmt.Errorf("remita: invoice %s refused: %w: %w", order, &apiError{status: 200, code: r.StatusCode, message: r.Status}, effects.ErrFatal)
}

func lineItems(items []any, total int64) ([]any, error) {
	var sum int64
	bearers := 0
	out := make([]any, len(items))
	for i, it := range items {
		m, _ := it.(map[string]any)
		amount, err := positiveKobo(m["amount"], fmt.Sprintf("line_items[%d].amount", i))
		if err != nil {
			return nil, err
		}
		for _, f := range []string{"id", "beneficiary_name", "beneficiary_account", "bank_code"} {
			if str(m, f) == "" {
				return nil, fmt.Errorf("remita: line_items[%d] needs %s: %w", i, f, effects.ErrFatal)
			}
		}
		fee := "0"
		if b, _ := m["bears_fee"].(bool); b {
			fee = "1"
			bearers++
		}
		sum += amount
		out[i] = map[string]any{"lineItemsId": str(m, "id"), "beneficiaryName": str(m, "beneficiary_name"), "beneficiaryAccount": str(m, "beneficiary_account"),
			"bankCode": str(m, "bank_code"), "beneficiaryAmount": nairaText(amount), "deductFeeFrom": fee}
	}
	if sum != total {
		return nil, fmt.Errorf("remita: line items add up to %d kobo, the invoice is %d: %w", sum, total, effects.ErrFatal)
	}
	if bearers != 1 {
		return nil, fmt.Errorf("remita: exactly one line item must bear the fee (bears_fee), got %d: %w", bearers, effects.ErrFatal)
	}
	return out, nil
}

// invoiceBy looks an invoice up by RRR or, without one, by orderId.
func (c *client) invoiceBy(ctx context.Context, req connector.Request, rrr, order string) (map[string]any, error) {
	base, ic, err := c.invoiceBase(req, false)
	if err != nil {
		return nil, err
	}
	var path, hash string
	switch {
	case rrr != "":
		hash = Hash(rrr, ic.apiKey, ic.merchant)
		path = "/echannelsvc/" + url.PathEscape(ic.merchant) + "/" + url.PathEscape(rrr) + "/" + hash + "/status.reg"
	case order != "":
		hash = Hash(order, ic.apiKey, ic.merchant)
		path = "/echannelsvc/" + url.PathEscape(ic.merchant) + "/" + url.PathEscape(order) + "/" + hash + "/orderstatus.reg"
	default:
		return nil, fmt.Errorf("remita: give rrr or order_id: %w", effects.ErrFatal)
	}
	raw, err := send(ctx, req.HTTP, http.MethodGet, base+path, consumer(ic.merchant, hash), nil)
	if ae := remitaError(err); ae != nil && ae.status == http.StatusNotFound {
		return nil, fmt.Errorf("remita: no invoice %s%s: %w", rrr, order, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var r struct {
		Amount      any    `json:"amount"`
		RRR         string `json:"RRR"`
		OrderID     string `json:"orderId"`
		Message     string `json:"message"`
		PaymentDate string `json:"paymentDate"`
		Status      string `json:"status"`
	}
	if err := invoiceJSON(raw, &r); err != nil {
		return nil, err
	}
	if r.RRR == "" {
		if invoiceNotFoundCodes[r.Status] {
			return nil, fmt.Errorf("remita: no invoice %s%s (%s %s): %w", rrr, order, r.Status, r.Message, connector.ErrNotFound)
		}
		return nil, fmt.Errorf("remita: status of %s%s: %w: %w", rrr, order, &apiError{status: 200, code: r.Status, message: r.Message}, effects.ErrUnknownOutcome)
	}
	var rd money.Reader
	out := map[string]any{"rrr": r.RRR, "order_id": r.OrderID, "status": invoiceStatus(r.Status), "status_code": r.Status,
		"message": r.Message, "amount": rd.Minor(r.Amount, 2), "payment_date": r.PaymentDate}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

func (c *client) getInvoice(ctx context.Context, req connector.Request) (connector.Response, error) {
	out, err := c.invoiceBy(ctx, req, str(req.Input, "rrr"), str(req.Input, "order_id"))
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) cancelInvoice(ctx context.Context, req connector.Request) (connector.Response, error) {
	rrr := str(req.Input, "rrr")
	if rrr == "" {
		return connector.Response{}, fmt.Errorf("remita: cancel_invoice needs rrr: %w", effects.ErrFatal)
	}
	base, ic, err := c.invoiceBase(req, true)
	if err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"rrr": rrr, "merchantId": ic.merchant, "hash": Hash(rrr, ic.apiKey, ic.merchant)}
	raw, err := send(ctx, req.HTTP, http.MethodPost, base+"/echannelsvc/v2/api/deactivate.json", nil, body)
	if err != nil {
		return connector.Response{}, err
	}
	var r struct {
		StatusCode string `json:"statuscode"`
		Status     string `json:"status"`
	}
	if err := invoiceJSON(raw, &r); err != nil {
		return connector.Response{}, err
	}
	switch {
	case r.StatusCode == "00":
		return connector.Response{Output: map[string]any{"rrr": rrr, "status_code": r.StatusCode, "message": r.Status}}, nil
	case invoiceUnknownCodes[r.StatusCode]:
		return connector.Response{}, fmt.Errorf("remita: cancel %s: %w: %w", rrr, &apiError{status: 200, code: r.StatusCode, message: r.Status}, effects.ErrUnknownOutcome)
	}
	return connector.Response{}, fmt.Errorf("remita: cancel %s refused: %w: %w", rrr, &apiError{status: 200, code: r.StatusCode, message: r.Status}, effects.ErrFatal)
}
