package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Lookups for the pickers (no-technical-steps N2, N8): a sheet's tabs from
// its link, the banks and an account's name for paying people, and the
// business's approved WhatsApp templates. Each asks the service through
// the business's own connection in prod, so nobody copies an id, a code
// or a template name.

// errNoAccountID is a WhatsApp connection without its business account.
var errNoAccountID = fmt.Errorf("%w: add your WhatsApp Business Account ID to the WhatsApp connection to choose a template by what it says", errConflict)

// connectorLookup runs action of connector conn (an id without its
// version) with the business's connection named connection, or its only
// one, and returns the action's output. title is the service's name in
// the errors a person reads.
func (s *Server) connectorLookup(r *http.Request, conn, title, connection, action, purpose string, input map[string]any) (map[string]any, error) {
	p := principalFrom(r.Context())
	const env = "prod"
	var name string
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT name FROM connections WHERE environment = $1 AND connector = $2 AND status = 'active' AND ($3 = '' OR name = $3)`,
			env, conn, connection)
		if err != nil {
			return err
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		switch len(names) {
		case 0:
			return fmt.Errorf("%w: connect %s first", errConflict, title)
		case 1:
			name = names[0]
			return nil
		}
		return fmt.Errorf("%w: you have more than one %s connection; choose which one", errBadRequest, title)
	}); err != nil {
		return nil, err
	}
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		return nil, err
	}
	c, ok := connectorByID(reg, conn)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not available here", errBadRequest, title)
	}
	creds, err := s.Vault.Credentials(secrets.WithUse(r.Context(), secrets.Use{Kind: secrets.KindConnection, Purpose: purpose, Actor: p.Actor()}), p.TenantID, env, conn, name)
	if err != nil {
		return nil, err
	}
	if conn == "whatsapp" && action == "list_templates" && strings.TrimSpace(creds["business_account_id"]) == "" {
		return nil, errNoAccountID
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: p.TenantID.String(), Hosts: c.Manifest.HostsFor(creds), Purpose: "connector:" + conn}, 20*time.Second)
	resp, err := c.Actions[action].Execute(r.Context(), connector.Request{Input: input, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
	if err != nil {
		return nil, &lookupError{err: err}
	}
	out, _ := resp.Output.(map[string]any)
	return out, nil
}

// lookupError is the service refusing or failing a lookup; the handler
// says what it means for the person.
type lookupError struct{ err error }

func (e *lookupError) Error() string { return e.err.Error() }
func (e *lookupError) Unwrap() error { return e.err }

// listBanks answers GET /v1/paystack/banks: the banks Paystack pays into.
func (s *Server) listBanks(w http.ResponseWriter, r *http.Request) {
	out, err := s.connectorLookup(r, "paystack", "Paystack", r.URL.Query().Get("connection"), "list_banks", "paystack.banks", map[string]any{})
	if isLookup(err) {
		err = fmt.Errorf("%w: Paystack did not give the list of banks; try again in a minute", errBadRequest)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"banks": nonNil(anyList(out["banks"]))})
}

// resolveAccount answers POST /v1/paystack/resolve with {"account_number",
// "bank_code"}: the name on the account, for the person to confirm before
// it is paid.
func (s *Server) resolveAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountNumber string `json:"account_number"`
		BankCode      string `json:"bank_code"`
		Connection    string `json:"connection"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	acct := digitsOnly(req.AccountNumber)
	if len(acct) != 10 || req.BankCode == "" {
		s.fail(w, r, fmt.Errorf("%w: an account number has 10 digits; choose its bank too", errBadRequest))
		return
	}
	out, err := s.connectorLookup(r, "paystack", "Paystack", req.Connection, "resolve_account", "paystack.resolve", map[string]any{"account_number": acct, "bank_code": req.BankCode})
	if isLookup(err) {
		err = fmt.Errorf("%w: that bank has no account with that number. Check the number and the bank", errBadRequest)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account_name": out["account_name"], "account_number": acct})
}

// createRecipient answers POST /v1/paystack/recipients with
// {"account_number", "bank_code", "name"}: the person or business saved in
// Paystack to be paid, and the code a transfer names them by. Paystack
// answers the same code for an account saved before.
func (s *Server) createRecipient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountNumber string `json:"account_number"`
		BankCode      string `json:"bank_code"`
		Name          string `json:"name"`
		Connection    string `json:"connection"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	acct := digitsOnly(req.AccountNumber)
	if len(acct) != 10 || req.BankCode == "" || strings.TrimSpace(req.Name) == "" {
		s.fail(w, r, fmt.Errorf("%w: give the account number, its bank and the name on it", errBadRequest))
		return
	}
	out, err := s.connectorLookup(r, "paystack", "Paystack", req.Connection, "create_recipient", "paystack.recipient",
		map[string]any{"account_number": acct, "bank_code": req.BankCode, "name": strings.TrimSpace(req.Name)})
	if isLookup(err) {
		err = fmt.Errorf("%w: Paystack did not save that account. Check the number and the bank", errBadRequest)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	code, _ := out["recipient_code"].(string)
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "paystack.recipient", code, map[string]any{"bank_code": req.BankCode, "account_last4": acct[6:]})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipient_code": code, "name": out["name"]})
}

// listWhatsAppTemplates answers GET /v1/whatsapp/templates: the business's
// approved templates, by what they say.
func (s *Server) listWhatsAppTemplates(w http.ResponseWriter, r *http.Request) {
	out, err := s.connectorLookup(r, "whatsapp", "WhatsApp", r.URL.Query().Get("connection"), "list_templates", "whatsapp.templates", map[string]any{})
	if isLookup(err) {
		err = fmt.Errorf("%w: WhatsApp did not give your templates. Check the WhatsApp Business Account ID on the connection", errBadRequest)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": nonNil(anyList(out["templates"]))})
}

func isLookup(err error) bool {
	var le *lookupError
	return errors.As(err, &le)
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
