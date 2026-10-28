// Package iswallet is the iswallet connector: transfers between wallets,
// bank payouts, balances, and wallet events. Behaviour follows iswallet
// engineering's answers of 5 October 2026 (docs/integrations/iswallet.md).
//
// Duplicate protection on iswallet has two layers: for 24 hours a repeated
// Idempotency-Key replays the original response; after that the repeat is
// refused by a constraint, but reported as a 500. So this connector never
// resends a key more than ReplayWindow after it was first sent without
// first asking iswallet what became of it (GET /v1/requests/{key}, shipped
// 12 October 2026): a request found there is answered from the record, and
// only one iswallet has no record of is sent again.
package iswallet

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// ReplayWindow is how long after a key's first use a resend is still
// answered from iswallet's replay cache (24 hours), less a safety margin.
const ReplayWindow = 23 * time.Hour

// Options configure the connector. LiveURL replaces iswallet's production
// address (TASKIEM_ISWALLET_URL), TestURL its sandbox's
// (TASKIEM_ISWALLET_TEST_URL, tests). BaseURL is where connections made
// before modes go: TASKIEM_ISWALLET_URL when set, else the sandbox.
type Options struct {
	LiveURL, TestURL string
	BaseURL          string
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// New returns the iswallet connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	m.SetModeURL("live", o.LiveURL)
	m.SetModeURL("test", o.TestURL)
	c := &client{m: m, now: o.Now}
	if c.now == nil {
		c.now = time.Now
	}
	registrars := map[string]connector.Registrar{}
	for name, t := range m.Triggers {
		if t.Registered() {
			registrars[name] = registrar{c: c, trigger: name}
		}
	}
	return &connector.Connector{Manifest: m, Registrars: registrars, Actions: map[string]connector.Action{
		"get_balance":  connector.ActionFunc(c.getBalance),
		"transfer":     connector.ActionFunc(c.transfer),
		"payout":       connector.ActionFunc(c.payout),
		"get_payout":   connector.ActionFunc(c.getPayout),
		"name_enquiry": connector.ActionFunc(c.nameEnquiry),
		"list_banks":   connector.ActionFunc(c.listBanks),

		"issue_virtual_account":  connector.ActionFunc(c.issueVirtualAccount),
		"issue_virtual_accounts": connector.ActionFunc(c.issueVirtualAccounts),
		"create_dynamic_account": connector.ActionFunc(c.createDynamicAccount),
		"list_providers":         connector.ActionFunc(c.listProviders),
		"create_wallet":          connector.ActionFunc(c.createWallet),
		"list_transactions":      connector.ActionFunc(c.listTransactions),
	}, CheckCredentials: CheckCredentials}
}

// The allow-list's error codes. Both refusals are fatal and happen before
// any request leaves Taskiem.
var (
	// ErrWalletNotAllowed: the call names a wallet (or iswallet returned
	// one) outside the connection's wallet_ids.
	ErrWalletNotAllowed = errors.New("WALLET_NOT_ALLOWED")
	// ErrAllowListMissing: the connection says its key is a shared
	// client's but lists no wallets, so nothing that touches a wallet runs.
	ErrAllowListMissing = errors.New("WALLET_ALLOW_LIST_MISSING")
)

// CheckCredentials refuses a shared client's connection without wallets,
// and a shared_client value other than true or false.
func CheckCredentials(creds map[string]string) error {
	switch creds["shared_client"] {
	case "", "false":
		return nil
	case "true":
		if len(connector.ListValues(creds["wallet_ids"])) == 0 {
			return fmt.Errorf("iswallet: a shared client's key reaches every customer's wallets; list this customer's wallets in wallet_ids (%w)", ErrAllowListMissing)
		}
		return nil
	}
	return fmt.Errorf("iswallet: shared_client must be true or false, not %q", creds["shared_client"])
}

// allowWallet checks a wallet against the connection's allow-list (decision
// 0031). A connection without one may use any wallet its key reaches,
// unless it is marked as a shared client's.
func allowWallet(req connector.Request, field, wallet string) error {
	list := connector.ListValues(req.Credentials["wallet_ids"])
	if len(list) == 0 {
		if req.Credentials["shared_client"] == "true" {
			return fmt.Errorf("iswallet %w: this connection is a shared client's and lists no wallet_ids, so it may use no wallet; nothing was sent: %w", ErrAllowListMissing, effects.ErrFatal)
		}
		return nil
	}
	if wallet == "" || !slices.Contains(list, wallet) {
		return fmt.Errorf("iswallet %w: %s %q is not one of this connection's wallet_ids; nothing was sent: %w", ErrWalletNotAllowed, field, wallet, effects.ErrFatal)
	}
	return nil
}

type client struct {
	m   *connector.Manifest // its modes' addresses, read on each call
	now func() time.Time
}

// apiError is iswallet's error envelope.
type apiError struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

// Codes after which nothing happened and the same request may be sent again.
var retryable = map[string]bool{"RATE_LIMITED": true, "CUSTODY_UNAVAILABLE": true, "SUBACCOUNT_SYNCING": true, "LIQUIDITY_EXHAUSTED": true}

// do sends one request and classifies failures for the engine (§B4).
func (c *client) do(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	_, err := c.doID(ctx, req, method, path, body, out)
	return err
}

// doID is do, also returning iswallet's id for the request (X-Request-ID,
// on every response): a report to iswallet quotes it (27 October 2026).
// A failure's message carries it too.
func (c *client) doID(ctx context.Context, req connector.Request, method, path string, body, out any) (string, error) {
	headers := map[string]string{"Authorization": "Bearer " + req.Credentials["api_key"]}
	write := method == http.MethodPost && req.IdempotencyKey != ""
	if write {
		headers["Idempotency-Key"] = req.IdempotencyKey
	}
	base, err := c.m.BaseFor(req.Credentials)
	if err != nil {
		return "", err
	}
	h, err := connector.DoJSONHeader(ctx, req.HTTP, method, base+path, headers, body, out)
	var he *connector.HTTPError
	if err == nil || !errors.As(err, &he) {
		return h.Get("X-Request-ID"), err // success, or a transport error DoJSONHeader already classified
	}
	var ae apiError
	_ = json.Unmarshal(he.Body, &ae)
	code, msg := ae.Error.Code, ae.Error.Message
	id := he.Header.Get("X-Request-ID")
	if id == "" {
		id = ae.Error.RequestID
	}
	quote := ""
	if id != "" {
		quote = ", request " + id
	}
	desc := fmt.Sprintf("iswallet %d %s: %s%s", he.Status, code, msg, quote)
	if code == "KYC_REQUIRED" {
		desc = fmt.Sprintf("iswallet needs %s before it can issue account numbers (%s: %s%s)", kycField(msg), code, msg, quote)
	}
	return id, c.classify(he, code, desc)
}

// kycField says in plain words what KYC_REQUIRED's message names: an email
// on every wallet, a director's BVN on a business wallet (27 October 2026).
func kycField(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "bvn"):
		return "the director's BVN"
	case strings.Contains(m, "email"):
		return "the wallet's email"
	}
	return "more about the wallet's owner"
}

// classify says what a refusal means for the engine.
func (c *client) classify(he *connector.HTTPError, code, desc string) error {
	switch {
	case retryable[code] || he.Status == http.StatusTooManyRequests || he.Status == http.StatusServiceUnavailable:
		return waitError{fmt.Errorf("%s: %w", desc, effects.ErrRetryable), c.retryWait(he)}
	case code == "PROVIDER_REJECTED":
		// The bank partner refused and nothing was created (27 October
		// 2026): final, and sending it again unchanged fails the same way.
		return fmt.Errorf("%s: %w", desc, effects.ErrFatal)
	case code == "IDEMPOTENCY_KEY_REUSED":
		// This key was already used for a request: the payment it carried
		// may have happened. Only a ledger check can say.
		return fmt.Errorf("%s: %w", desc, effects.ErrIndeterminate)
	case he.Status >= 500:
		// Within the replay window the same key is resent; past it, the
		// next attempt looks the key up first (settle).
		return fmt.Errorf("%s: %w", desc, effects.ErrUnknownOutcome)
	case he.Status == http.StatusNotFound && (code == "NOT_FOUND" || strings.HasSuffix(code, "_NOT_FOUND")):
		return fmt.Errorf("%s: %w: %w", desc, connector.ErrNotFound, effects.ErrFatal)
	}
	return fmt.Errorf("%s: %w", desc, effects.ErrFatal)
}

// retryWait is how long to wait before sending again. iswallet sends no
// Retry-After; X-RateLimit-Reset, on every response, is when its
// one-minute window reopens (unix seconds).
func (c *client) retryWait(he *connector.HTTPError) time.Duration {
	if he.RetryAfter > 0 {
		return he.RetryAfter
	}
	n, err := strconv.ParseInt(he.Header.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		return 0
	}
	// A reset more than two windows away is a clock disagreement; the
	// engine's own backoff applies instead.
	if d := time.Unix(n, 0).Sub(c.now()); d > 0 && d <= 2*time.Minute {
		return d + time.Second
	}
	return 0
}

// waitError carries how long the engine should wait before the next attempt.
type waitError struct {
	err  error
	wait time.Duration
}

func (e waitError) Error() string                  { return e.err.Error() }
func (e waitError) Unwrap() error                  { return e.err }
func (e waitError) RetryAfterDelay() time.Duration { return e.wait }

// requestRecord is what iswallet recorded under one of our idempotency keys.
type requestRecord struct {
	Kind       string `json:"kind"`
	ResourceID string `json:"resource_id"`
	Status     string `json:"status"`
	Terminal   bool   `json:"terminal"`
	WalletID   string `json:"wallet_id"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
}

// settle asks iswallet what became of a write that is about to be resent
// more than ReplayWindow after it was first sent, when a resend would no
// longer be answered from iswallet's replay cache. nil and no error means
// iswallet has no record of the key: nothing was posted (iswallet records
// the key in the same transaction as the first ledger entry), so sending
// it again is safe.
func (c *client) settle(ctx context.Context, req connector.Request, kind string) (*requestRecord, error) {
	if req.IdempotencyKey == "" || req.Attempt <= 1 || req.KeyFirstSent.IsZero() || c.now().Sub(req.KeyFirstSent) <= ReplayWindow {
		return nil, nil
	}
	var rec requestRecord
	err := c.do(ctx, req, http.MethodGet, "/v1/requests/"+url.PathEscape(req.IdempotencyKey), nil, &rec)
	if errors.Is(err, connector.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("iswallet: looking up this request, first sent %s ago, before sending it again: %w", c.now().Sub(req.KeyFirstSent).Round(time.Minute), err)
	}
	if rec.Kind != kind {
		return nil, fmt.Errorf("iswallet: this request's key is recorded for a %q, not a %s; check the wallet's ledger, then resolve the step: %w", rec.Kind, kind, effects.ErrIndeterminate)
	}
	return &rec, nil
}

func str(in map[string]any, k string) string {
	s, _ := in[k].(string)
	return s
}

func (c *client) getBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r struct {
		WalletID string `json:"wallet_id"`
		Balances []struct {
			Currency  string `json:"currency"`
			Total     int64  `json:"total"`
			Available int64  `json:"available"`
			Pending   int64  `json:"pending"`
			Scale     int    `json:"scale"`
		} `json:"balances"`
	}
	if err := allowWallet(req, "wallet_id", str(req.Input, "wallet_id")); err != nil {
		return connector.Response{}, err
	}
	if err := c.do(ctx, req, http.MethodGet, "/v1/wallets/"+url.PathEscape(str(req.Input, "wallet_id"))+"/balance", nil, &r); err != nil {
		return connector.Response{}, err
	}
	bs := make([]any, len(r.Balances))
	for i, b := range r.Balances {
		bs[i] = map[string]any{"currency": b.Currency, "total": b.Total, "available": b.Available, "pending": b.Pending, "scale": b.Scale}
	}
	return connector.Response{Output: map[string]any{"wallet_id": r.WalletID, "balances": bs}}, nil
}

func currency(in map[string]any) string {
	if c := str(in, "currency"); c != "" {
		return c
	}
	return "NGN"
}

func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := allowWallet(req, "from_wallet_id", str(in, "from_wallet_id")); err != nil {
		return connector.Response{}, err
	}
	body := map[string]any{"from_wallet_id": in["from_wallet_id"], "to_wallet_id": in["to_wallet_id"], "amount": in["amount"], "currency": currency(in)}
	if n := str(in, "narration"); n != "" {
		body["narration"] = n
	}
	rec, err := c.settle(ctx, req, "transfer")
	if err != nil {
		return connector.Response{}, err
	}
	if rec != nil {
		// A transfer is synchronous, so a record not yet terminal is
		// still being posted: wait for it rather than send again.
		if !rec.Terminal {
			return connector.Response{}, fmt.Errorf("iswallet: transfer %s is still %s: %w", rec.ResourceID, rec.Status, effects.ErrRetryable)
		}
		return connector.Response{Output: map[string]any{"transfer_id": rec.ResourceID, "status": rec.Status, "amount": rec.Amount, "currency": rec.Currency}}, nil
	}
	var r map[string]any
	if err := c.do(ctx, req, http.MethodPost, "/v1/transfers", body, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: pick(r, "transfer_id", "txn_id", "status", "amount", "currency", "completed_at")}, nil
}

func (c *client) payout(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if err := allowWallet(req, "wallet_id", str(in, "wallet_id")); err != nil {
		return connector.Response{}, err
	}
	if cur := currency(in); cur != "NGN" {
		return connector.Response{}, fmt.Errorf("iswallet pays out to banks in NGN only, not %s: %w", cur, effects.ErrFatal)
	}
	// Never send fee_amount: iswallet would treat it as an assertion.
	body := map[string]any{
		"wallet_id": in["wallet_id"], "amount": in["amount"], "currency": "NGN",
		"destination": map[string]any{"bank_code": in["bank_code"], "account_number": in["account_number"], "account_name": in["account_name"]},
	}
	if n := str(in, "narration"); n != "" {
		body["narration"] = n
	}
	if m, ok := in["metadata"].(map[string]any); ok {
		body["metadata"] = m
	}
	rec, err := c.settle(ctx, req, "outflow")
	if err != nil {
		return connector.Response{}, err
	}
	if rec != nil {
		// The payout was accepted when first sent; its outcome with the
		// bank follows as for any payout (get_payout, outflow_event).
		return connector.Response{Output: map[string]any{"outflow_id": rec.ResourceID, "status": rec.Status, "amount": rec.Amount, "idempotency_key": req.IdempotencyKey}}, nil
	}
	var r map[string]any
	if err := c.do(ctx, req, http.MethodPost, "/v1/outflows", body, &r); err != nil {
		return connector.Response{}, err
	}
	out := pick(r, "outflow_id", "status", "amount", "fee", "net_amount", "provider_reference")
	out["idempotency_key"] = req.IdempotencyKey
	return connector.Response{Output: out}, nil
}

func (c *client) getPayout(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r map[string]any
	// A shared client's connection without wallets reads no payout either.
	if err := allowWallet(req, "wallet_id", ""); errors.Is(err, ErrAllowListMissing) {
		return connector.Response{}, err
	}
	if err := c.do(ctx, req, http.MethodGet, "/v1/outflows/"+url.PathEscape(str(req.Input, "outflow_id")), nil, &r); err != nil {
		return connector.Response{}, err
	}
	// An outflow id names any payout of the client: one debited from a
	// wallet outside the allow-list is not this connection's to read.
	if len(connector.ListValues(req.Credentials["wallet_ids"])) > 0 {
		if w, ok := r["wallet_id"].(string); ok && !slices.Contains(connector.ListValues(req.Credentials["wallet_ids"]), w) {
			return connector.Response{}, fmt.Errorf("iswallet %w: payout %s was debited from wallet %q, which is not one of this connection's wallet_ids; its details are withheld: %w",
				ErrWalletNotAllowed, str(req.Input, "outflow_id"), w, effects.ErrFatal)
		}
	}
	return connector.Response{Output: pick(r, "outflow_id", "wallet_id", "status", "amount", "fee", "provider_reference", "confirmed_at")}, nil
}

func (c *client) nameEnquiry(ctx context.Context, req connector.Request) (connector.Response, error) {
	body := map[string]any{"bank_code": req.Input["bank_code"], "account_number": req.Input["account_number"]}
	var r map[string]any
	if err := c.do(ctx, req, http.MethodPost, "/v1/name-enquiry", body, &r); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: pick(r, "account_name", "account_number", "bank_code")}, nil
}

func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r struct {
		Banks []any `json:"banks"`
		Data  []any `json:"data"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/v1/banks", nil, &r); err != nil {
		return connector.Response{}, err
	}
	banks := r.Banks
	if banks == nil {
		banks = r.Data
	}
	if banks == nil {
		banks = []any{}
	}
	return connector.Response{Output: map[string]any{"banks": banks}}, nil
}

// issueVirtualAccount issues one permanent account number on a wallet
// (POST /v1/wallets/{id}/virtual-account, type permanent; iswallet,
// 27 October 2026). The reference is our key, which makes the call
// idempotent: asked again, iswallet answers the account it issued, never
// a second.
func (c *client) issueVirtualAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	wallet, ref := str(in, "wallet_id"), str(in, "reference")
	if err := allowWallet(req, "wallet_id", wallet); err != nil {
		return connector.Response{}, err
	}
	if ref == "" {
		return connector.Response{}, fmt.Errorf("issue_virtual_account needs a reference: %w", effects.ErrFatal)
	}
	body := map[string]any{"type": "permanent", "reference": ref}
	for _, k := range []string{"account_name", "provider"} {
		if v := str(in, k); v != "" {
			body[k] = v
		}
	}
	var a map[string]any
	id, err := c.doID(ctx, req, http.MethodPost, "/v1/wallets/"+url.PathEscape(wallet)+"/virtual-account", body, &a)
	if err != nil {
		return connector.Response{}, err
	}
	if n, _ := a["account_number"].(string); n == "" {
		return connector.Response{}, fmt.Errorf("iswallet: account %s has no number yet (status %v, request %s): %w", ref, a["status"], id, effects.ErrIndeterminate)
	}
	if r, _ := a["reference"].(string); r != "" && r != ref {
		return connector.Response{}, fmt.Errorf("iswallet: asked for reference %s, answered %s (request %s): %w", ref, r, id, effects.ErrIndeterminate)
	}
	out := pick(a, "account_number", "account_name", "bank_name", "status")
	out["reference"], out["wallet_id"], out["request_id"] = ref, wallet, id
	return connector.Response{Output: out}, nil
}

// maxBatch is iswallet's limit on one batch (27 October 2026).
const maxBatch = 500

// issueVirtualAccounts issues many permanent account numbers on one wallet
// (POST /v1/wallets/{id}/virtual-accounts/batch): idempotent per
// reference, a result per account.
func (c *client) issueVirtualAccounts(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	wallet := str(in, "wallet_id")
	if err := allowWallet(req, "wallet_id", wallet); err != nil {
		return connector.Response{}, err
	}
	list, _ := in["accounts"].([]any)
	if len(list) == 0 || len(list) > maxBatch {
		return connector.Response{}, fmt.Errorf("iswallet: a batch is 1 to %d accounts, not %d: %w", maxBatch, len(list), effects.ErrFatal)
	}
	items := make([]any, 0, len(list))
	for i, x := range list {
		m, _ := x.(map[string]any)
		ref := str(m, "reference")
		if ref == "" {
			return connector.Response{}, fmt.Errorf("iswallet: account %d has no reference; every account needs its own: %w", i+1, effects.ErrFatal)
		}
		item := map[string]any{"reference": ref}
		if n := str(m, "account_name"); n != "" {
			item["account_name"] = n
		}
		items = append(items, item)
	}
	var r map[string]any
	id, err := c.doID(ctx, req, http.MethodPost, "/v1/wallets/"+url.PathEscape(wallet)+"/virtual-accounts/batch", map[string]any{"accounts": items}, &r)
	if err != nil {
		return connector.Response{}, err
	}
	accounts, _ := r["accounts"].([]any)
	if accounts == nil {
		accounts = []any{}
	}
	out := pick(r, "created", "existing", "failed")
	out["wallet_id"], out["accounts"], out["request_id"] = wallet, accounts, id
	return connector.Response{Output: out}, nil
}

// OneTimeRefPrefix starts the reference of every one-time number Taskiem
// opens (the action's key, manifest.yaml): the payment into it carries the
// reference, and the prefix tells it from a credit to a customer's
// permanent number (whose reference is tsk_ and the key).
const OneTimeRefPrefix = "tot_"

// The window a one-time number is shown for when none is asked.
const defaultOneTimeMinutes = 60

// createDynamicAccount opens an account number for one payment on the
// connection's one wallet (POST /v1/wallets/{id}/virtual-account, type
// dynamic, amount required). iswallet does not deduplicate these: every
// call, a retry too, opens another account, so the action is unsafe and an
// unknown outcome parks. iswallet's own expiry is advisory, so expires_at
// is Taskiem's window, from when the request was sent.
func (c *client) createDynamicAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "reference")
	if ref == "" {
		ref = req.IdempotencyKey
	}
	if !strings.HasPrefix(ref, OneTimeRefPrefix) {
		return connector.Response{}, fmt.Errorf("iswallet: create_dynamic_account needs the engine's key (%s…), got %q: %w", OneTimeRefPrefix, ref, effects.ErrFatal)
	}
	wallets := connector.ListValues(req.Credentials["wallet_ids"])
	if len(wallets) != 1 {
		return connector.Response{}, fmt.Errorf("iswallet: a one-time number opens on the connection's wallet, so its wallet_ids must list exactly one (it lists %d); nothing was sent: %w", len(wallets), effects.ErrFatal)
	}
	wallet := wallets[0]
	amount, err := money.Minor(in["amount"])
	if err != nil || amount < 1 {
		return connector.Response{}, fmt.Errorf("iswallet: amount must be kobo, at least 1, got %v: %w", in["amount"], effects.ErrFatal)
	}
	minutes := defaultOneTimeMinutes
	if m, err := money.Minor(in["expires_minutes"]); err == nil && m > 0 {
		minutes = int(m)
	}
	body := map[string]any{"type": "dynamic", "amount": amount, "reference": ref}
	if n := str(in, "account_name"); n != "" {
		body["account_name"] = n
	}
	sent := c.now()
	var a map[string]any
	id, err := c.doID(ctx, req, http.MethodPost, "/v1/wallets/"+url.PathEscape(wallet)+"/virtual-account", body, &a)
	if err != nil {
		return connector.Response{}, err
	}
	n, _ := a["account_number"].(string)
	if n == "" {
		return connector.Response{}, fmt.Errorf("iswallet: the one-time account %s has no number (status %v, request %s): %w", ref, a["status"], id, effects.ErrIndeterminate)
	}
	out := pick(a, "account_number", "account_name", "bank_name")
	out["wallet_id"], out["reference"], out["amount"], out["request_id"] = wallet, ref, amount, id
	out["expires_at"] = sent.Add(time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339)
	return connector.Response{Output: out}, nil
}

// listProviders lists the partners that can issue account numbers (GET
// /v1/providers); with no error, the key works.
func (c *client) listProviders(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r struct {
		Providers []struct {
			Code            string   `json:"code"`
			Currencies      []string `json:"currencies"`
			VirtualAccounts struct {
				Permanent bool `json:"permanent"`
				Dynamic   bool `json:"dynamic"`
			} `json:"virtual_accounts"`
		} `json:"providers"`
	}
	id, err := c.doID(ctx, req, http.MethodGet, "/v1/providers", nil, &r)
	if err != nil {
		return connector.Response{}, err
	}
	list := make([]any, 0, len(r.Providers))
	for _, p := range r.Providers {
		cur := make([]any, 0, len(p.Currencies))
		for _, c := range p.Currencies {
			cur = append(cur, c)
		}
		list = append(list, map[string]any{"code": p.Code, "currencies": cur, "permanent": p.VirtualAccounts.Permanent, "dynamic": p.VirtualAccounts.Dynamic})
	}
	return connector.Response{Output: map[string]any{"providers": list, "request_id": id}}, nil
}

// walletIdentity are the identity fields create_wallet sends, flat at the
// top level: a body with a kyc_profile object is refused (27 October 2026).
var walletIdentity = []string{"email", "business_email", "bvn", "first_name", "last_name", "business_name"}

// createWallet makes a wallet (POST /v1/wallets), one per owner_ref and
// currency, under the step's key as its Idempotency-Key (required), so a
// retry never makes a second. The answer's account_number is iswallet's
// own handle for the wallet, which nobody can pay into: it is not
// returned, and internal_reference (the same value) is.
func (c *client) createWallet(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("iswallet: create_wallet needs the engine's key: %w", effects.ErrFatal)
	}
	// A shared client's key reaches other customers' wallets: a business
	// holding one (a partner's, or Taskiem's own, decision 0052) makes no
	// wallets with it. Taskiem makes a business's wallet under its own
	// client itself, without the shared mark.
	if req.Credentials["shared_client"] == "true" {
		return connector.Response{}, fmt.Errorf("iswallet: this connection is a shared client's, which cannot create wallets; nothing was sent: %w", effects.ErrFatal)
	}
	switch t := str(in, "owner_type"); t {
	case "client_end_user", "client_end_user_business", "client_end_business":
	default:
		return connector.Response{}, fmt.Errorf("iswallet: owner_type %q is not client_end_user, client_end_user_business or client_end_business: %w", t, effects.ErrFatal)
	}
	if str(in, "owner_ref") == "" {
		return connector.Response{}, fmt.Errorf("iswallet: create_wallet needs an owner_ref: %w", effects.ErrFatal)
	}
	body := map[string]any{"owner_type": in["owner_type"], "owner_ref": in["owner_ref"], "currency": currency(in)}
	for _, k := range walletIdentity {
		if v := str(in, k); v != "" {
			body[k] = v
		}
	}
	var w map[string]any
	id, err := c.doID(ctx, req, http.MethodPost, "/v1/wallets", body, &w)
	if err != nil {
		return connector.Response{}, err
	}
	wallet, _ := w["wallet_id"].(string)
	if wallet == "" {
		return connector.Response{}, fmt.Errorf("iswallet: the answer names no wallet (request %s): %w", id, effects.ErrIndeterminate)
	}
	out := pick(w, "wallet_id", "internal_reference", "tier", "owner_type")
	if _, ok := out["internal_reference"]; !ok {
		// Older answers carry the handle only as account_number.
		if n, ok := w["account_number"]; ok {
			out["internal_reference"] = n
		}
	}
	out["request_id"] = id
	return connector.Response{Output: out}, nil
}

// maxTransactionPages bounds one listing: at 100 a page, 2,000
// transactions; more is reported as truncated.
const maxTransactionPages = 20

// listTransactions lists a wallet's transactions in a window, every page
// (GET /v1/wallets/{id}/transactions, cursor-paged, 100 a page).
func (c *client) listTransactions(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	wallet := str(in, "wallet_id")
	if err := allowWallet(req, "wallet_id", wallet); err != nil {
		return connector.Response{}, err
	}
	q := url.Values{"limit": {"100"}}
	for _, k := range []string{"from", "to"} {
		if v := str(in, k); v != "" {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				return connector.Response{}, fmt.Errorf("iswallet: %s must be a time (RFC 3339): %w", k, effects.ErrFatal)
			}
			q.Set(k, v)
		}
	}
	list := []any{}
	truncated := false
	for page := 0; ; page++ {
		if page == maxTransactionPages {
			truncated = true
			break
		}
		var r struct {
			Transactions []any `json:"transactions"`
			Pagination   struct {
				HasMore    bool   `json:"has_more"`
				NextCursor string `json:"next_cursor"`
			} `json:"pagination"`
		}
		if err := c.do(ctx, req, http.MethodGet, "/v1/wallets/"+url.PathEscape(wallet)+"/transactions?"+q.Encode(), nil, &r); err != nil {
			return connector.Response{}, err
		}
		list = append(list, r.Transactions...)
		if !r.Pagination.HasMore || r.Pagination.NextCursor == "" {
			break
		}
		q.Set("cursor", r.Pagination.NextCursor)
	}
	return connector.Response{Output: map[string]any{"wallet_id": wallet, "transactions": list, "truncated": truncated}}, nil
}

// pick keeps the named fields that are present; numbers stay JSON numbers.
func pick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}
