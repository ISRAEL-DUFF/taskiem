// Package mono is the Mono connector: bank account linking and account data
// (Connect), one-time DirectPay payments and Direct Debit mandates. Built
// from Mono's public documentation (docs/integrations/mono.md).
//
// Amounts: Mono works in kobo, as Taskiem does, so amounts pass through
// unchanged; an amount that is not a whole number of kobo is refused on the
// way in and is an error on the way out, never a zero. Balance enquiries on
// a mandate can carry fractions of a kobo and are rounded down.
//
// Never twice: DirectPay payments, mandates and mandate debits carry the
// engine's key as their reference. They are reconcilable writes: after an
// unknown outcome the engine looks the reference up (verify_payment or
// get_mandate) before sending again, so a lost response never debits twice.
package mono

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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

// Options configure the connector. BaseURL replaces api.withmono.com
// (tests, or a proxy); Mono's sandbox shares the live host and is chosen by
// the key.
type Options struct {
	BaseURL string
}

// New returns the Mono connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{base: strings.TrimRight(m.BaseURL, "/")}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"initiate_account_linking": connector.ActionFunc(c.initiateLinking),
		"initiate_reauthorisation": connector.ActionFunc(c.initiateReauth),
		"exchange_token":           connector.ActionFunc(c.exchangeToken),
		"unlink_account":           connector.ActionFunc(c.unlink),
		"get_account":              connector.ActionFunc(c.getAccount),
		"get_balance":              connector.ActionFunc(c.getBalance),
		"get_identity":             connector.ActionFunc(c.getIdentity),
		"list_transactions":        connector.ActionFunc(c.listTransactions),
		"get_statement":            connector.ActionFunc(c.getStatement),
		"get_statement_pdf":        connector.ActionFunc(c.getStatementPDF),
		"request_income":           connector.ActionFunc(c.requestIncome),
		"get_income_records":       connector.ActionFunc(c.incomeRecords),
		"request_creditworthiness": connector.ActionFunc(c.requestCreditworthiness),
		"list_banks":               connector.ActionFunc(c.listBanks),
		"initiate_payment":         connector.ActionFunc(c.initiatePayment),
		"verify_payment":           connector.ActionFunc(c.verifyPayment),
		"create_customer":          connector.ActionFunc(c.createCustomer),
		"create_mandate":           connector.ActionFunc(c.createMandate),
		"initiate_mandate":         connector.ActionFunc(c.initiateMandate),
		"get_mandate":              connector.ActionFunc(c.getMandate),
		"pause_mandate":            connector.ActionFunc(c.mandateAction("pause", "paused")),
		"reinstate_mandate":        connector.ActionFunc(c.mandateAction("reinstate", "active")),
		"cancel_mandate":           connector.ActionFunc(c.mandateAction("cancel", "cancelled")),
		"check_mandate_balance":    connector.ActionFunc(c.mandateBalance),
		"debit_mandate":            connector.ActionFunc(c.debitMandate),
		"get_debit":                connector.ActionFunc(c.getDebit),
	}}
}

type client struct{ base string }

// envelope is Mono's response wrapper.
type envelope struct {
	Status       string          `json:"status"`
	Message      string          `json:"message"`
	ResponseCode string          `json:"response_code"`
	Data         json.RawMessage `json:"data"`
	Meta         json.RawMessage `json:"meta"`
}

// apiError is a refusal Mono explained.
type apiError struct {
	status  int
	code    string // response_code (Direct Debit)
	message string
}

func (e *apiError) Error() string {
	if e.code != "" && e.code != strconv.Itoa(e.status) {
		return fmt.Sprintf("mono %d (code %s): %s", e.status, e.code, e.message)
	}
	return fmt.Sprintf("mono %d: %s", e.status, e.message)
}

func (e *apiError) has(words ...string) bool {
	m := strings.ToLower(e.message)
	for _, w := range words {
		if strings.Contains(m, w) {
			return true
		}
	}
	return false
}

func monoError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// request is one call to Mono.
type request struct {
	method, path string
	query        url.Values
	body         any
	realtime     bool
}

// do sends one request and returns Mono's envelope. Failures are classified
// for the engine; refusals Mono explains carry *apiError.
func (c *client) do(ctx context.Context, req connector.Request, k request) (*envelope, error) {
	key := strings.TrimSpace(req.Credentials["secret_key"])
	if key == "" {
		return nil, fmt.Errorf("mono: the connection has no secret_key: %w", effects.ErrFatal)
	}
	u := c.base + k.path
	if len(k.query) > 0 {
		u += "?" + k.query.Encode()
	}
	h := map[string]string{"mono-sec-key": key}
	if k.realtime {
		h["x-realtime"] = "true"
	}
	var raw json.RawMessage
	err := connector.DoJSON(ctx, req.HTTP, k.method, u, h, k.body, &raw)
	var he *connector.HTTPError
	if errors.As(err, &he) {
		var e envelope
		_ = json.Unmarshal(he.Body, &e)
		ae := &apiError{status: he.Status, code: e.ResponseCode, message: e.Message}
		if ae.message == "" {
			ae.message = strings.TrimSpace(string(he.Body))
		}
		if d := validation(e.Data); d != "" {
			ae.message += ": " + d
		}
		// Keep the transport's classification (429 and 503 retryable, other
		// 5xx unknown outcome, 4xx fatal) and add Mono's explanation.
		return nil, fmt.Errorf("%w: %w", ae, he)
	}
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := decode(raw, &env); err != nil {
		return nil, fmt.Errorf("mono: unreadable response: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if env.Status == "failed" {
		// A 2xx that says it did not work. Mono does not document these;
		// for a write the outcome is unknown, so let the caller decide.
		return nil, fmt.Errorf("%w: %w", &apiError{status: http.StatusOK, code: env.ResponseCode, message: env.Message}, effects.ErrUnknownOutcome)
	}
	return &env, nil
}

// validation renders Mono's field errors ("data": [{field, message}]).
func validation(raw json.RawMessage) string {
	var fields []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	var parts []string
	for _, f := range fields {
		parts = append(parts, f.Message)
	}
	return strings.Join(parts, "; ")
}

// decode reads JSON keeping numbers exact.
func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}

// data decodes an envelope's data into an object.
func (e *envelope) object() (map[string]any, error) {
	m := map[string]any{}
	if len(e.Data) == 0 || string(e.Data) == "null" {
		return m, nil
	}
	if err := decode(e.Data, &m); err != nil {
		return nil, fmt.Errorf("mono: unreadable data: %w: %w", err, effects.ErrUnknownOutcome)
	}
	return m, nil
}

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	}
	return ""
}

func obj(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	if o == nil {
		return map[string]any{}
	}
	return o
}

func flag(m map[string]any, k string) bool {
	switch v := m[k].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	}
	return false
}

// plain turns json.Number into int64 or float64 for pass-through data.
func plain(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = plain(e)
		}
	case []any:
		for i, e := range x {
			x[i] = plain(e)
		}
	}
	return v
}

func count(v any) int64 {
	n, err := money.Minor(plain(v))
	if err != nil {
		return 0
	}
	return n
}

// unreadable is a response whose amounts cannot be read exactly. Mono may
// still have acted (a debit that went through), so it is never a failure.
func unreadable(err error) error {
	return fmt.Errorf("mono: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
}

func fatalf(format string, args ...any) error {
	return fmt.Errorf("mono: "+format+": %w", append(args, effects.ErrFatal)...)
}

func need(in map[string]any, keys ...string) error {
	for _, k := range keys {
		if strings.TrimSpace(str(in, k)) == "" {
			return fatalf("%s is required", k)
		}
	}
	return nil
}

func kobo(in map[string]any, k string) (int64, bool, error) {
	v, ok := in[k]
	if !ok || v == nil {
		return 0, false, nil
	}
	n, err := money.Minor(v)
	if err != nil || n < 0 {
		return 0, true, fatalf("%s must be a whole number of kobo, got %v", k, v)
	}
	return n, true, nil
}

func accountPath(in map[string]any, suffix string) (string, error) {
	if err := need(in, "account_id"); err != nil {
		return "", err
	}
	return "/v2/accounts/" + url.PathEscape(str(in, "account_id")) + suffix, nil
}

// notExist turns Mono's "does not exist" into a clear fatal error: the id is
// wrong, and retrying cannot change that.
func notExist(err error, what string) error {
	if ae := monoError(err); ae != nil && ae.status < 500 && (ae.status == http.StatusNotFound || ae.has("does not exist", "not found", "invalid account")) {
		return fmt.Errorf("mono: no such %s (%s): %w", what, ae.message, effects.ErrFatal)
	}
	return err
}

// ---- Account linking ----

func (c *client) initiateLinking(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := need(in, "redirect_url"); err != nil {
		return connector.Response{}, err
	}
	cust := map[string]any{}
	if id := str(in, "customer_id"); id != "" {
		cust["id"] = id
	} else {
		if err := need(in, "customer_name", "customer_email"); err != nil {
			return connector.Response{}, fatalf("linking needs customer_id, or customer_name and customer_email")
		}
		cust["name"], cust["email"] = str(in, "customer_name"), str(in, "customer_email")
	}
	body := map[string]any{"customer": cust, "scope": "auth", "redirect_url": str(in, "redirect_url")}
	if r := str(in, "ref"); r != "" {
		body["meta"] = map[string]any{"ref": r}
	}
	inst := map[string]any{}
	if v := str(in, "institution_id"); v != "" {
		inst["id"] = v
	}
	if v := str(in, "auth_method"); v != "" {
		inst["auth_method"] = v
	}
	if len(inst) > 0 {
		body["institution"] = inst
	}
	env, err := c.do(ctx, req, request{method: http.MethodPost, path: "/v2/accounts/initiate", body: body})
	if err != nil {
		return connector.Response{}, err
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"mono_url": str(d, "mono_url"), "customer_id": str(d, "customer"),
		"ref": str(obj(d, "meta"), "ref"), "created_at": str(d, "created_at")}}, nil
}

func (c *client) initiateReauth(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := need(in, "account_id", "redirect_url"); err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"account": str(in, "account_id"), "scope": "reauth", "redirect_url": str(in, "redirect_url")}
	if r := str(in, "ref"); r != "" {
		body["meta"] = map[string]any{"ref": r}
	}
	env, err := c.do(ctx, req, request{method: http.MethodPost, path: "/v2/accounts/initiate", body: body})
	if err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"mono_url": str(d, "mono_url"), "account_id": str(d, "account"),
		"customer_id": str(d, "customer"), "created_at": str(d, "created_at")}}, nil
}

func (c *client) exchangeToken(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := need(req.Input, "code"); err != nil {
		return connector.Response{}, err
	}
	env, err := c.do(ctx, req, request{method: http.MethodPost, path: "/v2/accounts/auth", body: map[string]any{"code": str(req.Input, "code")}})
	if err != nil {
		return connector.Response{}, err
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	if str(d, "id") == "" {
		return connector.Response{}, fmt.Errorf("mono: token exchange returned no account id: %w", effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{"account_id": str(d, "id")}}, nil
}

func (c *client) unlink(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := accountPath(req.Input, "/unlink")
	if err != nil {
		return connector.Response{}, err
	}
	if _, err := c.do(ctx, req, request{method: http.MethodPost, path: p}); err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	return connector.Response{Output: map[string]any{"account_id": str(req.Input, "account_id"), "unlinked": true}}, nil
}

// ---- Account data ----

func (c *client) getAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := accountPath(req.Input, "")
	if err != nil {
		return connector.Response{}, err
	}
	rt, _ := req.Input["realtime"].(bool)
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: p, realtime: rt})
	if err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	a, inst, meta := obj(d, "account"), obj(obj(d, "account"), "institution"), obj(d, "meta")
	var rd money.Reader
	out := map[string]any{"account_id": str(a, "id"), "name": str(a, "name"), "account_number": str(a, "account_number"),
		"currency": str(a, "currency"), "balance": rd.Minor(a["balance"], 0), "type": str(a, "type"), "bvn_last4": str(a, "bvn"),
		"institution_name": str(inst, "name"), "bank_code": str(inst, "bank_code"), "institution_type": str(inst, "type"),
		"customer_id": str(obj(d, "customer"), "id"), "data_status": str(meta, "data_status"), "auth_method": str(meta, "auth_method"),
		"retrieved_data": stringList(meta["retrieved_data"])}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: out}, nil
}

func stringList(v any) []any {
	out := []any{}
	if l, ok := v.([]any); ok {
		for _, e := range l {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func (c *client) getBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := accountPath(req.Input, "/balance")
	if err != nil {
		return connector.Response{}, err
	}
	rt, _ := req.Input["realtime"].(bool)
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: p, realtime: rt})
	if err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	var rd money.Reader
	out := map[string]any{"account_id": str(d, "id"), "name": str(d, "name"), "account_number": str(d, "account_number"),
		"balance": rd.Minor(d["balance"], 0), "currency": str(d, "currency")}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) getIdentity(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := accountPath(req.Input, "/identity")
	if err != nil {
		return connector.Response{}, err
	}
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: p})
	if err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"full_name": str(d, "full_name"), "bvn": str(d, "bvn"), "phone": str(d, "phone"),
		"email": str(d, "email"), "gender": str(d, "gender"), "date_of_birth": str(d, "dob"), "address": str(d, "address_line1"),
		"state_of_origin": str(d, "state_of_origin"), "lga_of_origin": str(d, "lga_of_origin"),
		"marital_status": str(d, "marital_status"), "verified": flag(d, "verified")}}, nil
}

// transactions reads a list of Mono transactions (amounts in kobo).
func transactions(raw json.RawMessage) ([]any, error) {
	var list []map[string]any
	if len(raw) > 0 && string(raw) != "null" {
		if err := decode(raw, &list); err != nil {
			return nil, fmt.Errorf("mono: unreadable transactions: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	var rd money.Reader
	out := make([]any, len(list))
	for i, t := range list {
		out[i] = map[string]any{"id": str(t, "id"), "type": str(t, "type"), "amount": rd.Minor(t["amount"], 0),
			"balance": rd.Minor(t["balance"], 0), "narration": str(t, "narration"), "date": str(t, "date"), "category": str(t, "category")}
	}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

func (c *client) listTransactions(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	p, err := accountPath(in, "/transactions")
	if err != nil {
		return connector.Response{}, err
	}
	if (str(in, "start") == "") != (str(in, "end") == "") {
		return connector.Response{}, fatalf("start and end go together")
	}
	q := url.Values{}
	for _, k := range []string{"start", "end", "narration", "type"} {
		if v := str(in, k); v != "" {
			q.Set(k, v)
		}
	}
	for _, k := range []string{"limit", "page"} {
		if v, ok := in[k]; ok && v != nil {
			n, err := money.Minor(v)
			if err != nil || n < 1 {
				return connector.Response{}, fatalf("%s must be a positive whole number", k)
			}
			q.Set(k, strconv.FormatInt(n, 10))
		}
	}
	rt, _ := in["realtime"].(bool)
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: p, query: q, realtime: rt})
	if err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	txs, err := transactions(env.Data)
	if err != nil {
		return connector.Response{}, err
	}
	var meta map[string]any
	_ = decode(env.Meta, &meta)
	page := count(meta["page"])
	if page == 0 {
		page = 1
	}
	return connector.Response{Output: map[string]any{"transactions": txs, "total": count(meta["total"]), "page": page,
		"has_more": str(meta, "next") != ""}}, nil
}

func (c *client) getStatement(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	p, err := accountPath(in, "/statement")
	if err != nil {
		return connector.Response{}, err
	}
	months, err := money.Minor(in["months"])
	if err != nil || months < 1 || months > 12 {
		return connector.Response{}, fatalf("months must be 1 to 12, got %v", in["months"])
	}
	q := url.Values{"period": {"last" + strconv.FormatInt(months, 10) + "months"}}
	pdf := str(in, "output") == "pdf"
	if pdf {
		q.Set("output", "pdf")
	}
	rt, _ := in["realtime"].(bool)
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: p, query: q, realtime: rt})
	if err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	if pdf {
		d, err := env.object()
		if err != nil {
			return connector.Response{}, err
		}
		return connector.Response{Output: map[string]any{"transactions": []any{}, "job_id": str(d, "id"), "status": str(d, "status"), "pdf_url": str(d, "path")}}, nil
	}
	txs, err := transactions(env.Data)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"transactions": txs, "job_id": "", "status": "", "pdf_url": ""}}, nil
}

func (c *client) getStatementPDF(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := need(req.Input, "job_id"); err != nil {
		return connector.Response{}, err
	}
	p, err := accountPath(req.Input, "/statement/jobs/"+url.PathEscape(str(req.Input, "job_id")))
	if err != nil {
		return connector.Response{}, err
	}
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: p})
	if err != nil {
		return connector.Response{}, notExist(err, "statement job")
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"job_id": str(d, "id"), "status": str(d, "status"), "pdf_url": str(d, "path")}}, nil
}

func (c *client) requestIncome(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := accountPath(req.Input, "/income")
	if err != nil {
		return connector.Response{}, err
	}
	var q url.Values
	if v, ok := req.Input["months"]; ok && v != nil {
		n, err := money.Minor(v)
		if err != nil || n < 1 {
			return connector.Response{}, fatalf("months must be a positive whole number")
		}
		q = url.Values{"period": {strconv.FormatInt(n, 10)}}
	}
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: p, query: q})
	if err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	return connector.Response{Output: map[string]any{"accepted": true, "message": env.Message}}, nil
}

func (c *client) incomeRecords(ctx context.Context, req connector.Request) (connector.Response, error) {
	p, err := accountPath(req.Input, "/income-records")
	if err != nil {
		return connector.Response{}, err
	}
	var q url.Values
	if v, ok := req.Input["page"]; ok && v != nil {
		n, err := money.Minor(v)
		if err != nil || n < 1 {
			return connector.Response{}, fatalf("page must be a positive whole number")
		}
		q = url.Values{"page": {strconv.FormatInt(n, 10)}}
	}
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: p, query: q})
	if err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	var recs []any
	if len(env.Data) > 0 && string(env.Data) != "null" {
		if err := decode(env.Data, &recs); err != nil {
			return connector.Response{}, fmt.Errorf("mono: unreadable income records: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	if recs == nil {
		recs = []any{}
	}
	var meta map[string]any
	_ = decode(env.Meta, &meta)
	return connector.Response{Output: map[string]any{"records": plain(recs), "total": count(meta["total"]), "has_more": str(meta, "next") != ""}}, nil
}

func (c *client) requestCreditworthiness(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	p, err := accountPath(in, "/creditworthiness")
	if err != nil {
		return connector.Response{}, err
	}
	if err := need(in, "bvn"); err != nil {
		return connector.Response{}, err
	}
	principal, ok, err := kobo(in, "principal")
	if err != nil || !ok || principal < 1 {
		return connector.Response{}, fatalf("principal must be a positive whole number of kobo")
	}
	term, err := money.Minor(in["term"])
	if err != nil || term < 1 {
		return connector.Response{}, fatalf("term must be a positive whole number of months")
	}
	rate, ok := number(in["interest_rate"])
	if !ok {
		return connector.Response{}, fatalf("interest_rate must be a number")
	}
	check, ok := in["run_credit_check"].(bool)
	if !ok {
		return connector.Response{}, fatalf("run_credit_check must be true or false")
	}
	body := map[string]any{"bvn": str(in, "bvn"), "principal": principal, "interest_rate": rate, "term": term, "run_credit_check": check}
	if l, ok := in["existing_loans"].([]any); ok && len(l) > 0 {
		body["existing_loans"] = l
	}
	env, err := c.do(ctx, req, request{method: http.MethodPost, path: p, body: body})
	if err != nil {
		return connector.Response{}, notExist(err, "account")
	}
	return connector.Response{Output: map[string]any{"accepted": true, "message": env.Message}}, nil
}

func number(v any) (json.Number, bool) {
	switch x := v.(type) {
	case float64:
		return json.Number(strconv.FormatFloat(x, 'f', -1, 64)), true
	case int:
		return json.Number(strconv.Itoa(x)), true
	case int64:
		return json.Number(strconv.FormatInt(x, 10)), true
	case json.Number:
		return x, true
	}
	return "", false
}

func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: "/v3/banks/list"})
	if err != nil {
		return connector.Response{}, err
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	banks := []any{}
	if l, ok := d["banks"].([]any); ok {
		for _, e := range l {
			b, _ := e.(map[string]any)
			if b == nil {
				continue
			}
			banks = append(banks, map[string]any{"name": str(b, "name"), "bank_code": str(b, "bank_code"), "nip_code": str(b, "nip_code"), "direct_debit": flag(b, "direct_debit")})
		}
	}
	return connector.Response{Output: map[string]any{"banks": banks}}, nil
}

// ---- DirectPay ----

// reference is the engine's key in the step's reference field.
func reference(in map[string]any) (string, error) {
	ref := str(in, "reference")
	if len(ref) < 10 {
		return "", fatalf("a reference of at least 10 characters is required (the engine supplies it)")
	}
	return ref, nil
}

// duplicate reports a refusal of a reference Mono has seen before.
func duplicate(err error) bool {
	ae := monoError(err)
	return ae != nil && ae.status < 500 && (ae.status == http.StatusConflict || ae.code == "26" || ae.code == "94" ||
		ae.has("unique reference", "duplicate", "reference already"))
}

func (c *client) initiatePayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, err := reference(in)
	if err != nil {
		return connector.Response{}, err
	}
	amount, _, err := kobo(in, "amount")
	if err != nil {
		return connector.Response{}, err
	}
	if amount < 20000 {
		return connector.Response{}, fatalf("amount must be at least 20000 kobo (NGN 200), got %d", amount)
	}
	if err := need(in, "description"); err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"amount": amount, "type": "onetime-debit", "description": str(in, "description"), "reference": ref}
	for k, f := range map[string]string{"method": "method", "account": "account_id", "redirect_url": "redirect_url"} {
		if v := str(in, f); v != "" {
			body[k] = v
		}
	}
	cust := map[string]any{}
	for k, f := range map[string]string{"name": "customer_name", "email": "customer_email", "phone": "customer_phone", "address": "customer_address"} {
		if v := str(in, f); v != "" {
			cust[k] = v
		}
	}
	if b := str(in, "customer_bvn"); b != "" {
		cust["identity"] = map[string]any{"type": "bvn", "number": b}
	}
	if len(cust) > 0 {
		body["customer"] = cust
	}
	inst := map[string]any{}
	if v := str(in, "institution_id"); v != "" {
		inst["id"] = v
	}
	if v := str(in, "auth_method"); v != "" {
		inst["auth_method"] = v
	}
	if len(inst) > 0 {
		body["institution"] = inst
	}
	if m, ok := in["meta"].(map[string]any); ok {
		body["meta"] = m
	}
	env, err := c.do(ctx, req, request{method: http.MethodPost, path: "/v2/payments/initiate", body: body})
	if duplicate(err) {
		// Already initiated under this reference: report that payment.
		out, verr := c.verify(ctx, req, ref)
		if errors.Is(verr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("mono refused reference %s (%s) and has no payment under it: %w", ref, monoError(err).message, effects.ErrFatal)
		}
		if verr != nil {
			return connector.Response{}, verr
		}
		return connector.Response{Output: map[string]any{"id": out["id"], "mono_url": "", "reference": ref, "amount": out["amount"],
			"status": out["status"], "customer_id": out["customer_id"]}}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	var rd money.Reader
	out := map[string]any{"id": str(d, "id"), "mono_url": str(d, "mono_url"), "reference": ref, "amount": rd.Minor(d["amount"], 0),
		"status": "initiated", "customer_id": str(d, "customer")}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: out}, nil
}

// verify looks a payment (DirectPay or mandate debit) up by reference.
func (c *client) verify(ctx context.Context, req connector.Request, ref string) (map[string]any, error) {
	if ref == "" {
		return nil, fatalf("reference is required")
	}
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: "/v2/payments/verify/" + url.PathEscape(ref)})
	if ae := monoError(err); ae != nil && ae.status < 500 && (ae.status == http.StatusNotFound || ae.has("not found")) {
		return nil, fmt.Errorf("mono: no payment with reference %s: %w", ref, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	d, err := env.object()
	if err != nil {
		return nil, err
	}
	acct := obj(d, "account")
	inst := obj(acct, "institution")
	var rd money.Reader
	out := map[string]any{"id": str(d, "id"), "reference": str(d, "reference"), "status": str(d, "status"),
		"amount": rd.Minor(d["amount"], 0), "fee": rd.Ceil(d["fee"], 0), "currency": str(d, "currency"), "type": str(d, "type"),
		"channel": str(d, "channel"), "description": str(d, "description"), "account_name": str(acct, "name"),
		"account_number": str(acct, "accountNumber"), "bank_code": str(inst, "bankCode"), "bank_name": str(inst, "name"),
		"customer_id": str(d, "customer"), "refunded": flag(d, "refunded"), "created_at": str(d, "created_at")}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	if out["reference"] == "" {
		out["reference"] = ref
	}
	return out, nil
}

func (c *client) verifyPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	out, err := c.verify(ctx, req, str(req.Input, "reference"))
	if errors.Is(err, connector.ErrNotFound) {
		// Also what reconcile reads as "never sent".
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

// ---- Direct Debit ----

func (c *client) createCustomer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := need(in, "first_name", "last_name", "email", "phone", "address", "bvn"); err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"type": "individual", "first_name": str(in, "first_name"), "last_name": str(in, "last_name"),
		"email": str(in, "email"), "phone": str(in, "phone"), "address": str(in, "address"),
		"identity": map[string]any{"type": "bvn", "number": str(in, "bvn")}}
	env, err := c.do(ctx, req, request{method: http.MethodPost, path: "/v2/customers", body: body})
	if err != nil {
		return connector.Response{}, err
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	if str(d, "id") == "" {
		return connector.Response{}, fmt.Errorf("mono: customer created without an id: %w", effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{"id": str(d, "id"), "name": str(d, "name"), "email": str(d, "email")}}, nil
}

// mandateBody builds the fields create_mandate and initiate_mandate share.
func mandateBody(in map[string]any, ref string) (map[string]any, error) {
	if err := need(in, "customer_id", "mandate_type", "debit_type", "description", "start_date", "end_date"); err != nil {
		return nil, err
	}
	amount, _, err := kobo(in, "amount")
	if err != nil {
		return nil, err
	}
	if amount < 1 {
		return nil, fatalf("amount must be a positive whole number of kobo")
	}
	body := map[string]any{"mandate_type": str(in, "mandate_type"), "debit_type": str(in, "debit_type"), "amount": amount,
		"reference": ref, "description": str(in, "description"), "start_date": str(in, "start_date"), "end_date": str(in, "end_date")}
	for _, k := range []string{"account_number", "bank_code", "fee_bearer", "verification_method", "frequency", "initial_debit_date", "redirect_url"} {
		if v := str(in, k); v != "" {
			body[k] = v
		}
	}
	for _, k := range []string{"interval", "retrial_frequency", "grace_period"} {
		if v, ok := in[k]; ok && v != nil {
			n, err := money.Minor(v)
			if err != nil {
				return nil, fatalf("%s must be a whole number", k)
			}
			body[k] = n
		}
	}
	for _, k := range []string{"minimum_due", "initial_debit_amount"} {
		if n, ok, err := kobo(in, k); err != nil {
			return nil, err
		} else if ok {
			body[k] = n
		}
	}
	if b, ok := in["allow_partial_sweep"].(bool); ok {
		body["allow_partial_sweep"] = b
	}
	if m, ok := in["meta"].(map[string]any); ok {
		body["meta"] = m
	}
	return body, nil
}

func (c *client) createMandate(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, err := reference(in)
	if err != nil {
		return connector.Response{}, err
	}
	body, err := mandateBody(in, ref)
	if err != nil {
		return connector.Response{}, err
	}
	body["customer"] = str(in, "customer_id")
	if a := str(in, "account_id"); a != "" {
		body["account"] = a
	} else if str(in, "account_number") == "" || str(in, "bank_code") == "" {
		return connector.Response{}, fatalf("a mandate needs account_number and bank_code, or account_id")
	}
	return c.mandateWrite(ctx, req, ref, request{method: http.MethodPost, path: "/v3/payments/mandates", body: body})
}

func (c *client) initiateMandate(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, err := reference(in)
	if err != nil {
		return connector.Response{}, err
	}
	body, err := mandateBody(in, ref)
	if err != nil {
		return connector.Response{}, err
	}
	if body["amount"].(int64) < 20000 {
		return connector.Response{}, fatalf("amount must be at least 20000 kobo (NGN 200)")
	}
	body["type"], body["method"] = "recurring-debit", "mandate"
	body["customer"] = map[string]any{"id": str(in, "customer_id")}
	return c.mandateWrite(ctx, req, ref, request{method: http.MethodPost, path: "/v2/payments/initiate", body: body})
}

// mandateWrite sends a mandate creation; a reference Mono has seen reports
// the mandate already made under it.
func (c *client) mandateWrite(ctx context.Context, req connector.Request, ref string, k request) (connector.Response, error) {
	env, err := c.do(ctx, req, k)
	if duplicate(err) {
		out, gerr := c.mandate(ctx, req, ref)
		if errors.Is(gerr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("mono refused reference %s (%s) and has no mandate under it: %w", ref, monoError(err).message, effects.ErrFatal)
		}
		if gerr != nil {
			return connector.Response{}, gerr
		}
		return connector.Response{Output: out}, nil
	}
	if err != nil {
		return connector.Response{}, err
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	out, err := mandateOutput(d, env.Message)
	if err != nil {
		return connector.Response{}, err
	}
	if out["reference"] == "" {
		out["reference"] = ref
	}
	return connector.Response{Output: out}, nil
}

func mandateOutput(d map[string]any, message string) (map[string]any, error) {
	inst := obj(d, "institution")
	id := str(d, "id")
	if id == "" {
		id = str(d, "mandate_id") // the hosted flow's name for it
	}
	bank, code := str(d, "bank"), str(d, "bank_code")
	if bank == "" {
		bank = str(inst, "name")
	}
	if code == "" {
		code = str(inst, "bank_code")
	}
	if m := str(d, "message"); m != "" {
		message = m
	}
	dests := []any{}
	if l, ok := d["transfer_destinations"].([]any); ok {
		for _, e := range l {
			t, _ := e.(map[string]any)
			if t != nil {
				dests = append(dests, map[string]any{"bank_name": str(t, "bank_name"), "account_number": str(t, "account_number")})
			}
		}
	}
	var rd money.Reader
	out := map[string]any{"id": id, "reference": str(d, "reference"), "status": str(d, "status"), "approved": flag(d, "approved"),
		"ready_to_debit": flag(d, "ready_to_debit"), "mandate_type": str(d, "mandate_type"), "debit_type": str(d, "debit_type"),
		"amount": rd.Minor(d["amount"], 0), "balance": rd.Minor(d["balance"], 0), "account_name": str(d, "account_name"),
		"account_number": str(d, "account_number"), "bank_name": bank, "bank_code": code, "customer_id": str(d, "customer"),
		"mono_url": str(d, "mono_url"), "nibss_code": str(d, "nibss_code"), "start_date": str(d, "start_date"),
		"end_date": str(d, "end_date"), "message": message, "transfer_destinations": dests}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

// mandate fetches a mandate by id or reference.
func (c *client) mandate(ctx context.Context, req connector.Request, idOrRef string) (map[string]any, error) {
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: "/v3/payments/mandates/" + url.PathEscape(idOrRef)})
	if ae := monoError(err); ae != nil && ae.status < 500 && (ae.status == http.StatusNotFound || ae.has("not found", "does not exist", "incorrect, invalid")) {
		return nil, fmt.Errorf("mono: no mandate %s: %w", idOrRef, connector.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	d, err := env.object()
	if err != nil {
		return nil, err
	}
	return mandateOutput(d, "")
}

func (c *client) getMandate(ctx context.Context, req connector.Request) (connector.Response, error) {
	id := str(req.Input, "mandate_id")
	if id == "" {
		id = str(req.Input, "reference")
	}
	if id == "" {
		return connector.Response{}, fatalf("get_mandate needs mandate_id or reference")
	}
	out, err := c.mandate(ctx, req, id)
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

// mandateAction pauses, reinstates or cancels. Each is a state change Mono
// refuses to repeat ("already paused"); that refusal means the state was
// reached, so it counts as done.
func (c *client) mandateAction(verb, state string) func(context.Context, connector.Request) (connector.Response, error) {
	already := map[string][]string{"pause": {"already paused"}, "reinstate": {"already active", "not paused", "already reinstated"},
		"cancel": {"already cancelled", "already canceled"}}[verb]
	return func(ctx context.Context, req connector.Request) (connector.Response, error) {
		if err := need(req.Input, "mandate_id"); err != nil {
			return connector.Response{}, err
		}
		id := str(req.Input, "mandate_id")
		env, err := c.do(ctx, req, request{method: http.MethodPatch, path: "/v3/payments/mandates/" + url.PathEscape(id) + "/" + verb})
		if ae := monoError(err); ae != nil && ae.status < 500 && ae.has(already...) {
			return connector.Response{Output: map[string]any{"mandate_id": id, "status": state, "message": ae.message}}, nil
		}
		if ae := monoError(err); ae != nil && ae.status < 500 && ae.has("incorrect, invalid, or does not exist") {
			return connector.Response{}, fmt.Errorf("mono: no mandate %s: %w", id, effects.ErrFatal)
		}
		if err != nil {
			return connector.Response{}, err
		}
		return connector.Response{Output: map[string]any{"mandate_id": id, "status": state, "message": env.Message}}, nil
	}
}

func (c *client) mandateBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := need(in, "mandate_id"); err != nil {
		return connector.Response{}, err
	}
	amount, withAmount, err := kobo(in, "amount")
	if err != nil {
		return connector.Response{}, err
	}
	var q url.Values
	if withAmount {
		q = url.Values{"amount": {strconv.FormatInt(amount, 10)}}
	}
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: "/v3/payments/mandates/" + url.PathEscape(str(in, "mandate_id")) + "/balance-inquiry", query: q})
	if err != nil {
		return connector.Response{}, err
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	acct := obj(d, "account_details")
	out := map[string]any{"mandate_id": str(d, "id"), "account_name": str(acct, "account_name"), "account_number": str(acct, "account_number"),
		"bank_code": str(acct, "bank_code"), "bank_name": str(acct, "bank_name")}
	if withAmount {
		out["has_sufficient_balance"] = flag(d, "has_sufficient_balance")
	} else {
		var rd money.Reader
		out["balance"] = rd.Floor(d["account_balance"], 0)
		if rd.Err != nil {
			return connector.Response{}, unreadable(rd.Err)
		}
	}
	return connector.Response{Output: out}, nil
}

// pendingCodes are Direct Debit response codes that mean the bank has not
// answered yet: 01 (status unknown, wait for settlement), 09 (in progress)
// and 99 (processing, as Mono's processing event carries).
var pendingCodes = map[string]bool{"01": true, "09": true, "99": true}

func (c *client) debitMandate(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, err := reference(in)
	if err != nil {
		return connector.Response{}, err
	}
	if err := need(in, "mandate_id", "narration"); err != nil {
		return connector.Response{}, err
	}
	amount, _, err := kobo(in, "amount")
	if err != nil {
		return connector.Response{}, err
	}
	if amount < 20000 {
		return connector.Response{}, fatalf("amount must be at least 20000 kobo (NGN 200), got %d", amount)
	}
	body := map[string]any{"amount": amount, "reference": ref, "narration": str(in, "narration")}
	if v := str(in, "fee_bearer"); v != "" {
		body["fee_bearer"] = v
	}
	if n := str(in, "beneficiary_account_number"); n != "" {
		if str(in, "beneficiary_nip_code") == "" {
			return connector.Response{}, fatalf("beneficiary_account_number needs beneficiary_nip_code")
		}
		body["beneficiary"] = map[string]any{"nuban": n, "nip_code": str(in, "beneficiary_nip_code")}
	}
	if m, ok := in["meta"].(map[string]any); ok {
		body["meta"] = m
	}
	id := str(in, "mandate_id")
	env, err := c.do(ctx, req, request{method: http.MethodPost, path: "/v3/payments/mandates/" + url.PathEscape(id) + "/debit", body: body})
	if duplicate(err) {
		// A resend after a lost response: report the debit already made.
		out, verr := c.verify(ctx, req, ref)
		if errors.Is(verr, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("mono refused reference %s (%s) and has no debit under it: %w", ref, monoError(err).message, effects.ErrFatal)
		}
		if verr != nil {
			return connector.Response{}, verr
		}
		return settledDebit(map[string]any{"reference": ref, "status": out["status"], "amount": out["amount"], "fee": out["fee"],
			"mandate_id": id, "reference_number": out["id"], "session_id": "", "response_code": "", "account_name": out["account_name"],
			"account_number": out["account_number"], "bank_name": out["bank_name"], "date": out["created_at"], "message": ""})
	}
	if ae := monoError(err); ae != nil && ae.status < 500 {
		if ae.status == http.StatusTooManyRequests && ae.has("rate-limited for this mandate", "rate limited for this mandate") {
			// The same-day lockout: retrying today cannot succeed.
			return connector.Response{}, fmt.Errorf("mono: debit refused, the mandate is locked for today: %s: %w", ae.message, effects.ErrFatal)
		}
		if pendingCodes[ae.code] {
			return connector.Response{}, fmt.Errorf("mono: debit outcome unknown (code %s, %s); wait for the debit event or verify_payment: %w", ae.code, ae.message, effects.ErrIndeterminate)
		}
	}
	if err != nil {
		return connector.Response{}, err
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	acct := obj(d, "account_details")
	var rd money.Reader
	out := map[string]any{"reference": ref, "status": str(d, "status"), "amount": rd.Minor(d["amount"], 0), "fee": rd.Ceil(d["fee"], 0),
		"mandate_id": str(d, "mandate"), "reference_number": str(d, "reference_number"), "session_id": str(d, "session_id"),
		"response_code": env.ResponseCode, "account_name": str(acct, "account_name"), "account_number": str(acct, "account_number"),
		"bank_name": str(acct, "bank_name"), "date": str(d, "date"), "message": env.Message}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	if out["mandate_id"] == "" {
		out["mandate_id"] = id
	}
	return settledDebit(out)
}

// settledDebit fails a debit Mono reports failed (no money moved) and passes
// successful and processing ones through. Any other status is not
// documented for debits, so a person decides.
func settledDebit(out map[string]any) (connector.Response, error) {
	switch out["status"] {
	case "successful", "processing":
		return connector.Response{Output: out}, nil
	case "failed":
		return connector.Response{}, fmt.Errorf("mono: debit %s failed (code %s): %s: %w", out["reference"], out["response_code"], out["message"], effects.ErrFatal)
	}
	return connector.Response{}, fmt.Errorf("mono: debit %s has status %q, which Mono does not document for debits; check it in Mono, then resolve the step: %w",
		out["reference"], out["status"], effects.ErrIndeterminate)
}

func (c *client) getDebit(ctx context.Context, req connector.Request) (connector.Response, error) {
	if err := need(req.Input, "mandate_id", "reference"); err != nil {
		return connector.Response{}, err
	}
	env, err := c.do(ctx, req, request{method: http.MethodGet, path: "/v3/payments/mandates/" + url.PathEscape(str(req.Input, "mandate_id")) +
		"/debits/" + url.PathEscape(str(req.Input, "reference"))})
	if err != nil {
		return connector.Response{}, notExist(err, "debit")
	}
	d, err := env.object()
	if err != nil {
		return connector.Response{}, err
	}
	var rd money.Reader
	out := map[string]any{"reference": str(d, "reference"), "status": str(d, "status"), "amount": rd.Minor(d["amount"], 0),
		"currency": str(d, "currency"), "type": str(d, "type"), "mandate_id": str(d, "mandate"), "narration": str(d, "narration"),
		"refunded": flag(d, "refunded"), "date": str(d, "date")}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: out}, nil
}
