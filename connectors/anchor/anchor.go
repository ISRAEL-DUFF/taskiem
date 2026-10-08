// Package anchor is the Anchor connector (docs.getanchor.co): NIP and book
// transfers, balances, name enquiry and transfer events. Built from Anchor's
// public documentation (docs/integrations/anchor.md).
//
// Amounts are kobo on both sides. Writes carry the engine's key twice: in
// the x-anchor-idempotent-key header (Anchor replays the first successful
// answer for 24 hours) and as the transfer's reference. Before resending,
// the connector asks Anchor for the transfer by that reference, so a resend
// after the replay window can never pay twice.
package anchor

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// SandboxURL is used for connections whose environment is "sandbox".
const SandboxURL = "https://api.sandbox.getanchor.co/api/v1"

// Options configure the connector; BaseURL overrides both environments.
type Options struct {
	BaseURL string
}

// New returns the Anchor connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: strings.TrimRight(m.BaseURL, "/"), sandbox: SandboxURL}
	if o.BaseURL != "" {
		c.live, c.sandbox = strings.TrimRight(o.BaseURL, "/"), strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"get_balance":               connector.ActionFunc(c.getBalance),
		"list_banks":                connector.ActionFunc(c.listBanks),
		"verify_account":            connector.ActionFunc(c.verifyAccount),
		"transfer":                  connector.ActionFunc(c.transfer),
		"book_transfer":             connector.ActionFunc(c.bookTransfer),
		"get_transfer":              connector.ActionFunc(c.getTransfer),
		"get_transfer_by_reference": connector.ActionFunc(c.getTransferByReference),
	}}
}

type client struct{ live, sandbox string }

func (c *client) base(req connector.Request) string {
	if strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox") {
		return c.sandbox
	}
	return c.live
}

// apiError is Anchor's error envelope: {"errors":[{title, status, detail}]}.
type apiError struct {
	status int
	title  string
	detail string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("anchor %d %s: %s", e.status, e.title, e.detail)
}

func (c *client) do(ctx context.Context, req connector.Request, method, path string, body, out any) error {
	key := req.Credentials["api_key"]
	if key == "" {
		return fmt.Errorf("anchor: the connection has no api_key: %w", effects.ErrFatal)
	}
	headers := map[string]string{"x-anchor-key": key}
	if method == http.MethodPost && req.IdempotencyKey != "" {
		headers["x-anchor-idempotent-key"] = req.IdempotencyKey
	}
	err := connector.DoJSON(ctx, req.HTTP, method, c.base(req)+path, headers, body, out)
	var he *connector.HTTPError
	if !errors.As(err, &he) {
		return err
	}
	var env struct {
		Errors []struct {
			Title  string `json:"title"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	ae := &apiError{status: he.Status}
	if json.Unmarshal(he.Body, &env) == nil && len(env.Errors) > 0 {
		ae.title, ae.detail = env.Errors[0].Title, env.Errors[0].Detail
	} else {
		ae.detail = strings.TrimSpace(string(he.Body))
	}
	return fmt.Errorf("%w: %w", ae, he)
}

func statusOf(err error) int {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.status
	}
	return 0
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func (c *client) accountID(req connector.Request) (string, error) {
	if id := str(req.Input, "account_id"); id != "" {
		return id, nil
	}
	if id := req.Credentials["account_id"]; id != "" {
		return id, nil
	}
	return "", fmt.Errorf("anchor: no account_id in the step or the connection: %w", effects.ErrFatal)
}

// unreadable is a response whose amounts cannot be read exactly (Anchor
// amounts are kobo; "1000.0000", as in older samples, reads as 1000). Anchor
// may still have acted, so it is never a failure.
func unreadable(err error) error {
	return fmt.Errorf("anchor: unreadable amount: %w: %w", err, effects.ErrUnknownOutcome)
}

func (c *client) getBalance(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := c.accountID(req)
	if err != nil {
		return connector.Response{}, err
	}
	var r struct {
		Data struct {
			AvailableBalance any `json:"availableBalance"`
			LedgerBalance    any `json:"ledgerBalance"`
			Hold             any `json:"hold"`
			Pending          any `json:"pending"`
		} `json:"data"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/accounts/balance/"+url.PathEscape(id), nil, &r); err != nil {
		return connector.Response{}, err
	}
	d := r.Data
	var rd money.Reader
	out := map[string]any{"account_id": id, "available_balance": rd.Minor(d.AvailableBalance, 0),
		"ledger_balance": rd.Minor(d.LedgerBalance, 0), "hold": rd.Minor(d.Hold, 0), "pending": rd.Minor(d.Pending, 0)}
	if rd.Err != nil {
		return connector.Response{}, unreadable(rd.Err)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) listBanks(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Name    string `json:"name"`
				NIPCode string `json:"nipCode"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/banks", nil, &r); err != nil {
		return connector.Response{}, err
	}
	banks := make([]any, len(r.Data))
	for i, b := range r.Data {
		banks[i] = map[string]any{"id": b.ID, "name": b.Attributes.Name, "nip_code": b.Attributes.NIPCode}
	}
	return connector.Response{Output: map[string]any{"banks": banks}}, nil
}

func (c *client) verifyAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	var r struct {
		Data struct {
			Attributes struct {
				AccountName   string `json:"accountName"`
				AccountNumber string `json:"accountNumber"`
				Bank          struct {
					Name    string `json:"name"`
					NIPCode string `json:"nipCode"`
				} `json:"bank"`
			} `json:"attributes"`
		} `json:"data"`
	}
	path := "/payments/verify-account/" + url.PathEscape(str(req.Input, "bank_code")) + "/" + url.PathEscape(str(req.Input, "account_number"))
	if err := c.do(ctx, req, http.MethodGet, path, nil, &r); err != nil {
		return connector.Response{}, err
	}
	a := r.Data.Attributes
	return connector.Response{Output: map[string]any{"account_name": a.AccountName, "account_number": a.AccountNumber,
		"bank_name": a.Bank.Name, "bank_code": a.Bank.NIPCode}}, nil
}

// counterparty creates the recipient with Anchor's name check (Anchor
// returns the existing record for a repeat) and checks the bank's name.
func (c *client) counterparty(ctx context.Context, req connector.Request) (string, error) {
	in := req.Input
	if id := str(in, "counterparty_id"); id != "" {
		return id, nil
	}
	number, bank, name := str(in, "account_number"), str(in, "bank_code"), str(in, "account_name")
	if number == "" || bank == "" || name == "" {
		return "", fmt.Errorf("anchor: transfer needs counterparty_id, or account_number, bank_code and account_name: %w", effects.ErrFatal)
	}
	body := map[string]any{"data": map[string]any{"type": "CounterParty", "attributes": map[string]any{
		"bankCode": bank, "accountName": name, "accountNumber": number, "verifyName": true}}}
	var r struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				AccountName string `json:"accountName"`
			} `json:"attributes"`
		} `json:"data"`
	}
	// Not a payment: no idempotency key, and safe to repeat.
	cp := req
	cp.IdempotencyKey = ""
	if err := c.do(ctx, cp, http.MethodPost, "/counterparties", body, &r); err != nil {
		return "", fmt.Errorf("anchor: counterparty: %w", err)
	}
	check, set := in["check_name"].(bool)
	if (check || !set) && !SameName(name, r.Data.Attributes.AccountName) {
		return "", fmt.Errorf("anchor: the bank's name for account %s is %q, not %q; nothing was sent: %w",
			number, r.Data.Attributes.AccountName, name, effects.ErrFatal)
	}
	if r.Data.ID == "" {
		return "", fmt.Errorf("anchor: counterparty created without an id: %w", effects.ErrUnknownOutcome)
	}
	return r.Data.ID, nil
}

// SameName reports whether two account names are the same person or
// business: every word of the shorter is in the longer, ignoring case,
// punctuation and order ("Ada N. Obi" and "OBI ADA NGOZI").
func SameName(a, b string) bool {
	wa, wb := words(a), words(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	if len(wa) > len(wb) {
		wa, wb = wb, wa
	}
	have := map[string]int{}
	for _, w := range wb {
		have[w]++
	}
	matched := 0
	for _, w := range wa {
		if len(w) == 1 { // an initial matches any word it starts
			for x := range have {
				if strings.HasPrefix(x, w) && have[x] > 0 {
					have[x]--
					matched++
					break
				}
			}
			continue
		}
		if have[w] > 0 {
			have[w]--
			matched++
		}
	}
	return matched == len(wa) && (len(wa) >= 2 || len(wb) == 1)
}

func words(s string) []string {
	f := strings.FieldsFunc(strings.ToUpper(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	sort.Strings(f)
	return f
}

// transferResource is a transfer as Anchor returns it.
type transferResource struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Reference     string `json:"reference"`
			Amount        any    `json:"amount"`
			Currency      string `json:"currency"`
			Status        string `json:"status"`
			FailureReason string `json:"failureReason"`
			SessionID     string `json:"sessionId"`
		} `json:"attributes"`
	} `json:"data"`
}

func (t transferResource) output() (map[string]any, error) {
	a := t.Data.Attributes
	var rd money.Reader
	out := map[string]any{"transfer_id": t.Data.ID, "type": t.Data.Type, "reference": a.Reference, "status": a.Status,
		"amount": rd.Minor(a.Amount, 0), "currency": a.Currency, "failure_reason": a.FailureReason, "session_id": a.SessionID}
	if rd.Err != nil {
		return nil, unreadable(rd.Err)
	}
	return out, nil
}

// settled fails the step for a transfer Anchor reports FAILED: no money
// moved, and Anchor's reason is the error.
func settled(t transferResource) (connector.Response, error) {
	out, err := t.output()
	if err != nil {
		return connector.Response{}, err
	}
	if out["status"] == "FAILED" {
		return connector.Response{}, fmt.Errorf("anchor: transfer %s failed: %s: %w", out["transfer_id"], out["failure_reason"], effects.ErrFatal)
	}
	return connector.Response{Output: out}, nil
}

// send creates a transfer, unless one already exists under this key.
func (c *client) send(ctx context.Context, req connector.Request, kind string, amount int64, rel map[string]any) (connector.Response, error) {
	ref := req.IdempotencyKey
	if ref == "" {
		return connector.Response{}, fmt.Errorf("anchor: transfers need the engine's key: %w", effects.ErrFatal)
	}
	if req.Attempt > 1 {
		// A resend: ask first. Anchor's replay of the first answer lasts 24
		// hours; the reference lasts as long as the transfer.
		t, err := c.byReference(ctx, req, ref)
		if err == nil {
			return settled(t)
		}
		if !errors.Is(err, connector.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("anchor: checking for an earlier attempt before resending: %w", err)
		}
	}
	body := map[string]any{"data": map[string]any{"type": kind,
		"attributes":    map[string]any{"amount": amount, "currency": "NGN", "reason": str(req.Input, "reason"), "reference": ref},
		"relationships": rel}}
	var t transferResource
	err := c.do(ctx, req, http.MethodPost, "/transfers", body, &t)
	if statusOf(err) == http.StatusConflict {
		// Undocumented; most likely this reference or key is in use. Report
		// the transfer that holds it, or leave the outcome unknown.
		if found, ferr := c.byReference(ctx, req, ref); ferr == nil {
			return settled(found)
		}
		// Not err's own classification (a 4xx reads as fatal): whether
		// this transfer happened is not known.
		return connector.Response{}, fmt.Errorf("anchor: %s: %w", err.Error(), effects.ErrUnknownOutcome)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return settled(t)
}

func amountOf(req connector.Request, min int64) (int64, error) {
	n, err := money.Minor(req.Input["amount"])
	if err != nil || n < min {
		return 0, fmt.Errorf("anchor: amount must be a whole number of kobo, at least %d; got %v: %w", min, req.Input["amount"], effects.ErrFatal)
	}
	return n, nil
}

func (c *client) transfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	amount, err := amountOf(req, 100)
	if err != nil {
		return connector.Response{}, err
	}
	from, err := c.accountID(req)
	if err != nil {
		return connector.Response{}, err
	}
	cp, err := c.counterparty(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	return c.send(ctx, req, "NIPTransfer", amount, map[string]any{
		"account":      map[string]any{"data": map[string]any{"id": from, "type": "DepositAccount"}},
		"counterParty": map[string]any{"data": map[string]any{"id": cp, "type": "CounterParty"}},
	})
}

func (c *client) bookTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	amount, err := amountOf(req, 1)
	if err != nil {
		return connector.Response{}, err
	}
	from, err := c.accountID(req)
	if err != nil {
		return connector.Response{}, err
	}
	destType := str(req.Input, "destination_type")
	if destType == "" {
		destType = "DepositAccount"
	}
	return c.send(ctx, req, "BookTransfer", amount, map[string]any{
		"account":            map[string]any{"data": map[string]any{"id": from, "type": "DepositAccount"}},
		"destinationAccount": map[string]any{"data": map[string]any{"id": str(req.Input, "destination_account_id"), "type": destType}},
	})
}

func (c *client) byReference(ctx context.Context, req connector.Request, ref string) (transferResource, error) {
	var t transferResource
	err := c.do(ctx, req, http.MethodGet, "/transfers/by-reference/"+url.PathEscape(ref), nil, &t)
	if statusOf(err) == http.StatusNotFound {
		return t, fmt.Errorf("anchor: no transfer with reference %s: %w", ref, connector.ErrNotFound)
	}
	if err == nil && t.Data.ID == "" {
		return t, fmt.Errorf("anchor: no transfer with reference %s: %w", ref, connector.ErrNotFound)
	}
	return t, err
}

func (c *client) getTransfer(ctx context.Context, req connector.Request) (connector.Response, error) {
	var t transferResource
	if err := c.do(ctx, req, http.MethodGet, "/transfers/verify/"+url.PathEscape(str(req.Input, "transfer_id")), nil, &t); err != nil {
		if statusOf(err) == http.StatusNotFound {
			return connector.Response{}, fmt.Errorf("%w: %w: %w", err, connector.ErrNotFound, effects.ErrFatal)
		}
		return connector.Response{}, err
	}
	out, err := t.output()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

func (c *client) getTransferByReference(ctx context.Context, req connector.Request) (connector.Response, error) {
	t, err := c.byReference(ctx, req, str(req.Input, "reference"))
	if errors.Is(err, connector.ErrNotFound) {
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return connector.Response{}, err
	}
	out, err := t.output()
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}
