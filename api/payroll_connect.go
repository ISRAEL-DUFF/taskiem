package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Connecting Payrolla in one step, keeping key connections, reconnecting,
// and linking approvers (P4b-1 of docs/spec/chat-pay-people.md; decision
// 0074). Connecting is Taskiem's OAuth machinery (oauthconnections.go):
// POST /v1/payroll/connect starts it, Payrolla sends the browser back to
// /v1/payrolla/callback (the same handler as the connections callback),
// and the tokens are sealed in the vault as every connection's
// credentials are. An owner whose connection holds a key, or has ended,
// connects again the same way: the same connection moves onto the new
// tokens, its webhooks are made again, and its approvers link again.
//
// Linking: a person with payroll.approve links themselves to their
// Payrolla user with one code Payrolla sends to that user's own factor.
// Taskiem keeps no copy of the link: Payrolla's linked_as is the record.
// The code is sent to Payrolla once and kept nowhere.

// payrollaConn is the business's Payrolla connection in prod: the active
// one, else the last that ended.
type payrollaConn struct {
	ID       uuid.UUID
	Name     string
	Status   string // active or expired
	AuthType string // api_key or oauth2
	Mode     *string
}

func (c payrollaConn) oauth() bool { return c.AuthType == "oauth2" }

func (s *Server) payrollaConnection(ctx context.Context, tenant uuid.UUID) (*payrollaConn, error) {
	var c payrollaConn
	err := s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, name, status, auth_type, mode FROM connections WHERE environment = 'prod' AND connector = $1 AND status IN ('active', 'expired')
			ORDER BY status = 'active' DESC, created_at LIMIT 1`, payrollaID).Scan(&c.ID, &c.Name, &c.Status, &c.AuthType, &c.Mode)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// payrollaClient is a Connect API client on the business's active
// Payrolla connection, refreshing its tokens under the connection's lock.
func (s *Server) payrollaClient(ctx context.Context, p *Principal, purpose string) (*payrolla.Client, *payrollaConn, error) {
	return s.payrollaClientAs(ctx, p.TenantID, p.Actor(), purpose)
}

// payrollaClientAs is a business's Payrolla client, its connection's
// credentials read for actor ("platform" for Taskiem's own checks).
func (s *Server) payrollaClientAs(ctx context.Context, tenant uuid.UUID, actor, purpose string) (*payrolla.Client, *payrollaConn, error) {
	p := &Principal{TenantID: tenant}
	conn, err := s.payrollaConnection(ctx, p.TenantID)
	if err != nil {
		return nil, nil, err
	}
	if conn == nil || conn.Status != "active" {
		return nil, conn, errPayrollaNotConnected
	}
	reg, err := s.Registry.For(ctx, p.TenantID.String())
	if err != nil {
		return nil, nil, err
	}
	c, ok := connectorByID(reg, payrollaID)
	if !ok {
		return nil, nil, fmt.Errorf("%w: Payrolla is not available here", errBadRequest)
	}
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: purpose, Actor: actor})
	creds, err := s.Vault.Credentials(use, p.TenantID, "prod", payrollaID, conn.Name)
	if err != nil {
		return nil, nil, err
	}
	hc := s.egressGuard().Client(egress.Policy{Tenant: p.TenantID.String(), Hosts: c.Manifest.HostsFor(creds), Purpose: "connector:" + payrollaID}, 20*time.Second)
	tenant, name := p.TenantID, conn.Name
	return payrolla.NewClient(c, creds, hc, func(ctx context.Context, fn connector.RotateFunc) (map[string]string, error) {
		return s.Vault.RotateCredentials(use, tenant, "prod", payrollaID, name, fn)
	}), conn, nil
}

var errPayrollaNotConnected = errors.New("payrolla not connected")

// payrollaSays turns a Payrolla refusal into words for the person (the
// plan's "Payrolla's refusals, in plain words"); Payrolla's codes are
// never shown. A connection that ended is marked expired on the way.
func (s *Server) payrollaSays(ctx context.Context, p *Principal, conn *payrollaConn, err error) error {
	owner := isOwnerOf(p)
	if errors.Is(err, errPayrollaNotConnected) || payrolla.Ended(err) {
		if conn != nil && payrolla.Ended(err) {
			if xerr := s.Vault.ExpireConnection(context.WithoutCancel(ctx), p.TenantID, "prod", payrollaID, conn.Name); xerr != nil {
				s.Logger.Warn("payrolla: marking the connection ended", "err", xerr)
			}
		}
		if owner {
			return refusePay(http.StatusConflict, "reconnect", "Payrolla is no longer connected. Reconnect Payrolla.")
		}
		return refusePay(http.StatusConflict, "reconnect", "Payroll isn't connected right now. The owner has been told.")
	}
	if errors.Is(err, payrolla.ErrBug) {
		s.Logger.Error("payrolla: Taskiem sent something Payrolla refused", "err", err)
		return refusePay(http.StatusBadGateway, "our_side", "Something went wrong on our side. Try again in a minute.")
	}
	words := map[string]struct {
		status     int
		code, text string
	}{
		"APPROVER_NOT_LINKED":             {http.StatusConflict, "not_linked", "Link your Payrolla account first. It takes one code."},
		"ALREADY_LINKED":                  {http.StatusConflict, "already_linked", "You're linked to another Payrolla account. Unlink it first."},
		"NOT_AN_APPROVER":                 {http.StatusForbidden, "not_an_approver", "In Payrolla you can't approve payroll. Ask the owner to make you an Approver in Payrolla."},
		"CANNOT_PAY":                      {http.StatusForbidden, "cannot_pay", "In Payrolla you can't send payments. Ask the owner."},
		"PAYMENT_CONFIRMATION_NOT_SET_UP": {http.StatusConflict, "no_factor", "Payrolla has nowhere to send you a code yet. Ask the owner to add your phone in Payrolla."},
		"CODE_INVALID":                    {http.StatusUnprocessableEntity, "code_invalid", "That code isn't right. Check it and type it again."},
		"CODE_EXPIRED":                    {http.StatusGone, "code_expired", "That code has expired."},
		"CHALLENGE_NOT_FOUND":             {http.StatusGone, "code_expired", "That code has expired."},
		"CODE_LOCKED":                     {http.StatusGone, "code_locked", "Too many wrong tries for that code."},
		"CODE_USED":                       {http.StatusGone, "code_used", "That code was already used."},
		"TOO_MANY_CODES":                  {http.StatusTooManyRequests, "too_many_codes", "Payrolla has sent you too many codes. Try again in 10 minutes."},
	}
	code := payrolla.Code(err)
	if wd, ok := words[code]; ok {
		return refusePay(wd.status, wd.code, wd.text)
	}
	switch {
	case code == "SCOPE_REQUIRED" && owner:
		return refusePay(http.StatusConflict, "scope_required", "Payrolla hasn't allowed Taskiem to do this. Reconnect Payrolla and allow Approve and pay.")
	case code == "SCOPE_REQUIRED":
		return refusePay(http.StatusConflict, "scope_required", "Ask the owner to allow this in Payrolla.")
	case errors.Is(err, connector.ErrNotFound):
		return pgx.ErrNoRows
	case code != "":
		s.Logger.Warn("payrolla: refused", "code", code, "err", err)
		return refusePay(http.StatusBadGateway, "payrolla_refused", "Payrolla didn't take that. Try again in a minute.")
	}
	return refusePay(http.StatusBadGateway, "payrolla_down", "Payrolla did not answer just now; try again in a minute.")
}

// payrollaKey is the Idempotency-Key of one change: a hash of the
// connection, the route and the key the form or sheet made when it
// opened (kept through its retries).
func payrollaKey(conn *payrollaConn, route, client string) (string, error) {
	client = strings.TrimSpace(client)
	if client == "" || len(client) > 100 {
		return "", fmt.Errorf("%w: key is the id the form made when it opened", errBadRequest)
	}
	sum := sha256.Sum256([]byte(conn.ID.String() + "\x00" + route + "\x00" + client))
	return "tsk_" + hex.EncodeToString(sum[:16]), nil
}

// actingName is the person's name as the business knows them.
func (s *Server) actingName(ctx context.Context, p *Principal) string {
	var name string
	_ = s.tenantTx(ctx, p.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&name)
	})
	return name
}

// scopesOf is what the owner allowed a connection, as stored with it.
func scopesOf(creds map[string]string) []string { return strings.Fields(creds["scopes"]) }

// getPayrollConnection is GET /v1/payroll/connection: how Payroll is
// connected (in one step, with a key, or ended), the company, what it may
// do, and what this person may do about it.
func (s *Server) getPayrollConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	conn, err := s.payrollaConnection(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	owner := isOwnerOf(p)
	out := map[string]any{"connected": false, "is_owner": owner, "may_link": p.Permissions[PermPayrollApprove], "modes": s.payrollaModes(ctx, p)}
	if conn == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	info := map[string]any{"id": conn.ID, "name": conn.Name, "status": conn.Status, "auth": map[bool]string{true: "oauth", false: "key"}[conn.oauth()],
		"test": conn.Mode != nil && *conn.Mode == "test"}
	if conn.Status == "active" {
		creds, err := s.Vault.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "payroll.connection", Actor: p.Actor()}), p.TenantID, "prod", payrollaID, conn.Name)
		if err == nil {
			info["company"] = map[string]any{"id": creds["company_id"], "name": creds["company_name"]}
			info["scopes"] = nonNil(scopesOf(creds))
		}
	}
	out["connected"] = conn.Status == "active"
	out["ended"] = conn.Status == "expired"
	out["connection"] = info
	writeJSON(w, http.StatusOK, out)
}

// payrollaModes are the modes Payrolla can be connected in one step here
// (an OAuth client and a sign-in page for each).
func (s *Server) payrollaModes(ctx context.Context, p *Principal) []string {
	reg, err := s.Registry.For(ctx, p.TenantID.String())
	if err != nil {
		return []string{}
	}
	c, ok := connectorByID(reg, payrollaID)
	if !ok {
		return []string{}
	}
	return nonNil(s.oauthModes(c))
}

// connectPayroll is POST /v1/payroll/connect {mode, sign_up?, company_name?,
// rc_number?, email?} (owners): the consent page to send the browser to.
// A business not on Payrolla yet (sign_up) has its name, RC number and the
// owner's email filled in for Payrolla's sign-up. An existing connection
// (a key, or one that ended) is moved onto the new tokens when Payrolla
// sends the browser back: that is Reconnect Payrolla.
func (s *Server) connectPayroll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode        string `json:"mode"`
		SignUp      bool   `json:"sign_up"`
		CompanyName string `json:"company_name"`
		RCNumber    string `json:"rc_number"`
		Email       string `json:"email"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	if !isOwnerOf(p) {
		s.payFail(w, r, refusePay(http.StatusForbidden, "owner_only", "Only the owner can connect Payrolla."))
		return
	}
	req.CompanyName, req.RCNumber, req.Email = strings.TrimSpace(req.CompanyName), strings.TrimSpace(req.RCNumber), strings.TrimSpace(req.Email)
	if utf8.RuneCountInString(req.CompanyName) > 200 || len(req.RCNumber) > 20 || len(req.Email) > 254 {
		s.fail(w, r, fmt.Errorf("%w: the company's name, RC number or email is too long", errBadRequest))
		return
	}
	reg, err := s.Registry.For(ctx, p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, ok := connectorByID(reg, payrollaID)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: Payrolla is not available here", errBadRequest))
		return
	}
	conn, err := s.payrollaConnection(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	o := oauthBegin{extra: url.Values{}}
	name := ""
	mode := req.Mode
	if conn != nil {
		o.replaces, name = &conn.ID, conn.Name
		if mode == "" && conn.Mode != nil {
			mode = *conn.Mode
		}
	}
	if req.SignUp {
		if req.CompanyName == "" || req.Email == "" {
			var tenantName, email string
			if err := s.tenantTx(ctx, p.TenantID, func(tx pgx.Tx) error {
				if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&tenantName); err != nil {
					return err
				}
				return tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&email)
			}); err != nil {
				s.fail(w, r, err)
				return
			}
			if req.CompanyName == "" {
				req.CompanyName = tenantName
			}
			if req.Email == "" {
				req.Email = email
			}
		}
		o.extra.Set("company_name", req.CompanyName)
		o.extra.Set("rc_number", req.RCNumber)
		o.extra.Set("email", req.Email)
	}
	consent, browser, _, err := s.beginOAuth(ctx, p, c, "prod", name, mode, o)
	if err != nil {
		s.failOAuth(w, r, err)
		return
	}
	s.setOAuthBrowser(w, browser, c)
	writeJSON(w, http.StatusOK, map[string]any{"url": consent, "reconnect": conn != nil, "expires_in": int(secrets.OAuthRequestTTL.Seconds())})
}

// reconnectOAuthTx moves the connection a reconnect request names onto the
// tokens just granted: its credentials replaced (the key goes), made an
// OAuth connection, active again.
//
// The key itself stays live in Payrolla: the Connect API has no way for a
// key to end itself (POST /oauth/revoke takes only OAuth tokens), so a
// Payrolla connection that held a key tells the owners to delete it in
// Payrolla, Settings → Connected apps.
func (s *Server) reconnectOAuthTx(ctx context.Context, tx pgx.Tx, req *secrets.OAuthRequest, c *connector.Connector, creds map[string]string, expiresAt *time.Time) error {
	var connID, env, name, was string
	err := tx.QueryRow(ctx, `SELECT connector, environment, name, auth_type FROM connections WHERE id = $1 FOR UPDATE`, *req.Replaces).Scan(&connID, &env, &name, &was)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (connID != c.Manifest.ID || env != req.Environment || name != req.Name) {
		return fmt.Errorf("%w: the connection you were reconnecting is gone; connect again", errConflict)
	}
	if err != nil {
		return err
	}
	if err := s.Vault.ReplaceCredentialsTx(ctx, tx, req.Tenant, *req.Replaces, creds, req.Person); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE connections SET auth_type = 'oauth2', expires_at = $2 WHERE id = $1`, *req.Replaces, expiresAt); err != nil {
		return err
	}
	if c.Manifest.ID != payrollaID || was != "api_key" {
		return nil
	}
	_, err = alerts.TellOwners(ctx, tx, req.Tenant, alerts.Alert{Kind: "payrolla_old_key", Dedup: req.ID.String(), Title: "Delete Taskiem's old Payrolla key",
		Body: "Payrolla is connected in one step now, so Taskiem no longer uses its old key. That key still works in Payrolla until you delete it: in Payrolla, Settings → Connected apps.",
		Link: "/payroll/settings"})
	return err
}

// payrollApprover is a Payrolla approver as the Approvers list shows it:
// whether they can get codes, and who in Taskiem they are linked to.
type payrollApprover struct {
	UserID     string               `json:"user_id"`
	Name       string               `json:"name"`
	Email      string               `json:"email"`
	Role       string               `json:"role"`
	CanApprove bool                 `json:"can_approve"`
	CanPay     bool                 `json:"can_pay"`
	CanConfirm bool                 `json:"can_confirm"`
	Linked     *payrollLinkedPerson `json:"linked"`
	Suggested  bool                 `json:"suggested"`
	Me         bool                 `json:"me"`
}

type payrollLinkedPerson struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
	Since  string    `json:"since,omitempty"`
}

// getPayrollApprovers is GET /v1/payroll/approvers: Payrolla's list of who
// may approve and pay, with whether each can get codes and who in Taskiem
// they are linked to; the Payrolla user with this person's email first.
func (s *Server) getPayrollApprovers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.approvers")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var list []payrolla.Approver
		if list, err = cl.Approvers(ctx); err == nil {
			s.writeApprovers(w, r, p, list)
			return
		}
	}
	s.payFail(w, r, s.payrollaSays(ctx, p, conn, err))
}

func (s *Server) writeApprovers(w http.ResponseWriter, r *http.Request, p *Principal, list []payrolla.Approver) {
	ctx := r.Context()
	people := map[uuid.UUID]string{}
	var myEmail string
	if err := s.tenantTx(ctx, p.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT u.id, u.name FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.tenant_id = $1`, p.TenantID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			people[id] = name
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&myEmail)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]payrollApprover, 0, len(list))
	var mine *payrollApprover
	for _, a := range list {
		pa := payrollApprover{UserID: a.UserID, Name: a.Name, Email: a.Email, Role: a.Role, CanApprove: a.CanApprove, CanPay: a.CanPay, CanConfirm: a.CanConfirm}
		if u := a.LinkedUser(); u != uuid.Nil {
			name, ok := people[u]
			if !ok {
				name = "Someone no longer in this business"
			}
			pa.Linked = &payrollLinkedPerson{UserID: u, Name: name, Since: a.LinkedSince}
			pa.Me = u == p.UserID
		}
		pa.Suggested = pa.Linked == nil && myEmail != "" && strings.EqualFold(strings.TrimSpace(a.Email), myEmail)
		out = append(out, pa)
	}
	// The same email first, then the rest as Payrolla lists them.
	slices.SortStableFunc(out, func(a, b payrollApprover) int {
		switch {
		case a.Suggested == b.Suggested:
			return 0
		case a.Suggested:
			return -1
		}
		return 1
	})
	for i := range out {
		if out[i].Me {
			mine = &out[i]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvers": out, "me": mine, "may_link": p.Permissions[PermPayrollApprove], "is_owner": isOwnerOf(p)})
}

// channelWords says where Payrolla sent a code, in plain words.
func channelWords(ch payrolla.Challenge) string {
	hint := strings.TrimSpace(strings.TrimLeft(ch.DestinationHint, "•· *"))
	switch ch.Channel {
	case "TOTP":
		return "Open your authenticator app for the code."
	case "SMS":
		if hint != "" {
			return "Payrolla sent a code to your phone ending " + hint + "."
		}
		return "Payrolla sent a code to your phone."
	case "EMAIL":
		if ch.DestinationHint != "" {
			return "Payrolla sent a code to your email " + ch.DestinationHint + "."
		}
		return "Payrolla sent a code to your email."
	}
	return "Payrolla sent you a code."
}

// startPayrollLink is POST /v1/payroll/approvers/{user}/link {key}
// (payroll.approve): Payrolla sends that Payrolla user a code. One
// Payrolla user per Taskiem person: someone already linked unlinks first.
func (s *Server) startPayrollLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	user := chi.URLParam(r, "user")
	if _, err := uuid.Parse(user); err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.link")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var list []payrolla.Approver
		if list, err = cl.Approvers(ctx); err == nil {
			for _, a := range list {
				if a.LinkedUser() == p.UserID && a.UserID != user {
					err = &payrolla.Error{Status: http.StatusConflict, Code: "ALREADY_LINKED"}
				}
			}
		}
	}
	if err == nil {
		var key string
		if key, err = payrollaKey(conn, "link:"+user, req.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var ch payrolla.Challenge
		if ch, err = cl.StartLink(ctx, user, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.link_code", "payrolla:"+user, map[string]any{"channel": ch.Channel})
			})
			writeJSON(w, http.StatusAccepted, map[string]any{"challenge": ch, "sent_to": channelWords(ch), "safer": ch.Channel == "SMS"})
			return
		}
	}
	s.payFail(w, r, s.payrollaSays(ctx, p, conn, err))
}

// confirmPayrollLink is POST /v1/payroll/approvers/{user}/link/confirm
// {challenge_id, code, key} (payroll.approve): the code goes to Payrolla
// once and is kept nowhere (not logged, not audited).
func (s *Server) confirmPayrollLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Challenge string `json:"challenge_id"`
		Code      string `json:"code"`
		Key       string `json:"key"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	code := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		if r == ' ' || r == '-' {
			return -1
		}
		return 'x'
	}, req.Code)
	if len(code) < 4 || len(code) > 10 || strings.Contains(code, "x") {
		s.payFail(w, r, refusePay(http.StatusUnprocessableEntity, "code_invalid", "That code isn't right. Check it and type it again."))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	user := chi.URLParam(r, "user")
	if _, err := uuid.Parse(user); err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if _, err := uuid.Parse(req.Challenge); err != nil {
		s.payFail(w, r, refusePay(http.StatusGone, "code_expired", "That code has expired."))
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.link")
	if err == nil {
		var key string
		// Each attempt at a code gets its own key (the challenge and the
		// sheet's attempt), so a wrong code can be followed by the right one.
		if key, err = payrollaKey(conn, "link-confirm:"+user+":"+req.Challenge, req.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var a payrolla.Approver
		if a, err = cl.ConfirmLink(ctx, user, req.Challenge, code, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.link", "payrolla:"+user, map[string]any{"confirmed_with_code": true})
			})
			writeJSON(w, http.StatusOK, map[string]any{"approver": map[string]any{"user_id": a.UserID, "name": a.Name, "role": a.Role, "can_approve": a.CanApprove, "can_pay": a.CanPay}})
			return
		}
	}
	s.payFail(w, r, s.payrollaSays(ctx, p, conn, err))
}

// unlinkPayroll is DELETE /v1/payroll/approvers/{user}/link {key}: owners
// unlink anyone; anyone unlinks themselves.
func (s *Server) unlinkPayroll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	user := chi.URLParam(r, "user")
	if _, err := uuid.Parse(user); err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	client := r.URL.Query().Get("key")
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.unlink")
	if err == nil {
		var list []payrolla.Approver
		if list, err = cl.Approvers(ctx); err == nil {
			i := slices.IndexFunc(list, func(a payrolla.Approver) bool { return a.UserID == user })
			switch {
			case i < 0:
				s.fail(w, r, pgx.ErrNoRows)
				return
			case list[i].LinkedUser() == uuid.Nil:
				writeJSON(w, http.StatusOK, map[string]any{"unlinked": true})
				return
			case list[i].LinkedUser() != p.UserID && !isOwnerOf(p):
				s.payFail(w, r, refusePay(http.StatusForbidden, "owner_only", "Only the owner can unlink someone else."))
				return
			}
			var key string
			if key, err = payrollaKey(conn, "unlink:"+user, client); err != nil {
				s.fail(w, r, err)
				return
			}
			if err = cl.Unlink(ctx, user, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
				_ = s.tx(r, func(tx pgx.Tx) error {
					return auditTx(r, tx, "payroll.unlink", "payrolla:"+user, map[string]any{"self": list[i].LinkedUser() == p.UserID})
				})
				writeJSON(w, http.StatusOK, map[string]any{"unlinked": true})
				return
			}
		}
	}
	s.payFail(w, r, s.payrollaSays(ctx, p, conn, err))
}

// PayrollaHeard reacts to what a Payrolla connection's own subscription
// delivers (ingest.Handler.Heard): company.disconnected for this
// connection marks it expired and tells the owners once, on their first
// linked chat. One for an old key (the business has since reconnected in
// one step) changes nothing.
func PayrollaHeard(pool *pgxpool.Pool, vault *secrets.Vault, log *slog.Logger) func(context.Context, ingest.HeardEvent) {
	return func(ctx context.Context, e ingest.HeardEvent) {
		if e.Connector != payrollaID {
			return
		}
		// A run's event tells people of it (payroll_runs_chat.go); a
		// payment's and a payslip's (payroll_payment.go).
		if payrollaRunHeard(ctx, pool, log, e) || payrollaPaymentHeard(ctx, pool, log, e) {
			return
		}
		keyID, reason, ok := payrolla.Disconnected(e.Body)
		if !ok {
			return
		}
		use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "payrolla.disconnected"})
		creds, err := vault.Credentials(use, e.Tenant, e.Environment, payrollaID, e.Connection)
		if err != nil {
			return // gone or already ended: told once already
		}
		if have := creds["connection_key_id"]; have != "" && keyID != "" && have != keyID {
			log.Info("payrolla: an old connection ended; this one stays", "tenant", e.Tenant)
			return
		}
		if creds["api_key"] != "" {
			return // a key connection: its key is the owner's to replace
		}
		if err := vault.ExpireConnection(ctx, e.Tenant, e.Environment, payrollaID, e.Connection); err != nil {
			log.Warn("payrolla: marking the connection ended", "err", err)
			return
		}
		body := "Someone removed Taskiem in Payrolla, so payroll can't be run here. Reconnect Payrolla on the Payroll page."
		if reason == "revoked_by_app" {
			body = "Payrolla was disconnected from Taskiem. Reconnect Payrolla on the Payroll page to run payroll here again."
		}
		if err := db.InTenantTx(ctx, pool, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
			_, err := alerts.TellOwners(ctx, tx, e.Tenant, alerts.Alert{Kind: "payrolla_disconnected", Dedup: e.Connection + ":" + keyID,
				Title: "Payrolla is no longer connected", Body: body, Link: "/payroll/settings"})
			return err
		}); err != nil {
			log.Warn("payrolla: telling the owners", "err", err)
		}
	}
}
