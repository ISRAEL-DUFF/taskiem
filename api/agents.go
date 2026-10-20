package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Field agents (docs/agents.md, decision 0038): people Taskiem checks and
// registers who sign small businesses up in person. An agent signs in as
// themselves, lands in a workspace of their own, and works in a business
// only through a session the owner agreed to: by sending a code from
// their WhatsApp number (an onboarding), or by inviting the agent's code
// back for a few hours (a support visit). In a session the agent edits
// the storefront and its tables, and nothing else: no orders, refunds,
// members, connections or personal data. Everything they do is audited
// as agent:<code>.

const (
	agentSessionHeader = "X-Taskiem-Agent-Session"
	agentConsentTTL    = 30 * time.Minute // for the owner to send the code
	agentVisitMaxHours = 8
)

// AgentAccess is a field agent acting in a business through a session.
type AgentAccess struct {
	ID        uuid.UUID
	Code      string
	Name      string
	Kind      string // onboarding or support
	Session   uuid.UUID
	ExpiresAt time.Time
}

// agentPermissions are what an agent may do in a business: its page and
// the tables behind it.
var agentPermissions = []string{PermStorefrontManage, PermTableRead, PermTableEdit, PermWorkflowRead, PermWorkflowEdit, PermWorkflowPublish}

var errAgentSession = errors.New("this session with the business has ended, or is not yours")

// agentPrincipal turns an agent's own session into one acting in the
// business of the session they name, if it is theirs and live.
func (s *Server) agentPrincipal(ctx context.Context, p *Principal, header string) (*Principal, error) {
	id, err := uuid.Parse(header)
	if err != nil || p.UserID == uuid.Nil {
		return nil, errAgentSession
	}
	a := &AgentAccess{Session: id}
	var tenant uuid.UUID
	err = s.Store.Pool.QueryRow(ctx, `SELECT tenant_id, agent_id, kind, expires_at, agent_name, agent_code FROM taskiem_agent_session_scope($1, $2)`, id, p.UserID).
		Scan(&tenant, &a.ID, &a.Kind, &a.ExpiresAt, &a.Name, &a.Code)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errAgentSession
	}
	if err != nil {
		return nil, err
	}
	out := &Principal{TenantID: tenant, UserID: p.UserID, AuthMethod: p.AuthMethod, Agent: a, Permissions: map[string]bool{}, viaCookie: p.viaCookie}
	for _, x := range agentPermissions {
		out.Permissions[x] = true
	}
	return out, nil
}

// agentAllowed are the requests an agent may make in a business: the
// storefront and its tables, but not customers' orders and bookings (nor
// refunds), a table's export, or the storage project.
func agentAllowed(method, path string) bool {
	switch {
	case strings.HasPrefix(path, "/v1/storefront/orders"), strings.HasPrefix(path, "/v1/storefront/bookings/"),
		path == "/v1/storefront/bookings" && method != http.MethodPut:
		return false
	case path == "/v1/storefront", strings.HasPrefix(path, "/v1/storefront/"):
		return true
	case path == "/v1/tables/project", strings.HasPrefix(path, "/v1/tables/") && strings.HasSuffix(path, "/export"):
		return false
	case path == "/v1/tables", strings.HasPrefix(path, "/v1/tables/"):
		return true
	}
	return false
}

// --- the agent's own routes ---

type agentKey struct{}

type agentInfo struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Region    string    `json:"region"`
	Workspace uuid.UUID `json:"workspace_id"`
}

func agentFrom(ctx context.Context) *agentInfo {
	a, _ := ctx.Value(agentKey{}).(*agentInfo)
	return a
}

func (s *Server) agentRoutes(r chi.Router) {
	r.Use(s.asAgent)
	r.Get("/me", s.agentMe)
	r.Post("/businesses", s.agentNewBusiness)
	r.Get("/sessions", s.agentSessions)
	r.Get("/sessions/{id}", s.agentSession)
	r.Post("/sessions/{id}/code", s.agentNewCode)
	r.Post("/sessions/{id}/attest", s.agentAttest)
	r.Post("/sessions/{id}/end", s.agentEndSession)
}

// asAgent admits an active agent signed in as themselves.
func (s *Server) asAgent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r.Context())
		if p.UserID == uuid.Nil || p.Agent != nil || p.KeyID != uuid.Nil {
			writeErr(w, http.StatusForbidden, "field agents sign in as themselves")
			return
		}
		var a agentInfo
		err := s.Store.Pool.QueryRow(r.Context(), `SELECT id, code, name, status, region, workspace_id FROM taskiem_agent_of($1)`, p.UserID).
			Scan(&a.ID, &a.Code, &a.Name, &a.Status, &a.Region, &a.Workspace)
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusForbidden, "you are not a Taskiem field agent")
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if a.Status != "active" {
			writeErr(w, http.StatusForbidden, "your agent account is "+a.Status+": contact Taskiem")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), agentKey{}, &a)))
	})
}

func (s *Server) agentMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"agent": agentFrom(r.Context())})
}

type agentSessionOut struct {
	ID        uuid.UUID  `json:"id"`
	TenantID  uuid.UUID  `json:"tenant_id"`
	Business  string     `json:"business"`
	Kind      string     `json:"kind"`
	Status    string     `json:"status"`
	ConsentAt *time.Time `json:"consent_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	EndedAt   *time.Time `json:"ended_at"`
	EndReason *string    `json:"end_reason"`
	CreatedAt time.Time  `json:"created_at"`
}

func (s *Server) agentSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT id, tenant_id, tenant_name, kind, status, consent_at, expires_at, ended_at, end_reason, created_at
		FROM taskiem_agent_sessions($1, 100)`, agentFrom(r.Context()).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[agentSessionOut])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": nonNil(out)})
}

// agentSessionRow is one of the agent's sessions, with its owner.
type agentSessionRow struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Business    string
	Owner       uuid.UUID
	OwnerNumber *string
	Kind        string
	Status      string
	ExpiresAt   time.Time
}

func (s *Server) agentSessionOf(r *http.Request) (agentSessionRow, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return agentSessionRow{}, pgx.ErrNoRows
	}
	x := agentSessionRow{ID: id}
	err = s.Store.Pool.QueryRow(r.Context(), `SELECT tenant_id, tenant_name, owner_user, owner_number, kind, status, expires_at FROM taskiem_agent_session_get($1, $2)`,
		id, agentFrom(r.Context()).ID).Scan(&x.TenantID, &x.Business, &x.Owner, &x.OwnerNumber, &x.Kind, &x.Status, &x.ExpiresAt)
	return x, err
}

func (x agentSessionRow) out() map[string]any {
	return map[string]any{"id": x.ID, "tenant_id": x.TenantID, "business": x.Business, "kind": x.Kind, "status": x.Status, "expires_at": x.ExpiresAt}
}

func (s *Server) agentSession(w http.ResponseWriter, r *http.Request) {
	x, err := s.agentSessionOf(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": x.out()})
}

// ownerPlaceholderEmail is the sign-in name of an owner an agent signed
// up, who has only a WhatsApp number: it receives no mail (.invalid), and
// is the same each time for the number, until the number is bound.
func ownerPlaceholderEmail(number string) string {
	h := sha256.Sum256([]byte("taskiem-owner:" + number))
	return "owner+" + hex.EncodeToString(h[:8]) + "@phone.taskiem.invalid"
}

// consentHash is how the database keeps a consent code: as the WhatsApp
// code digests are kept (taskiem_agent_session_consent).
func consentHash(number, code string) []byte {
	h := sha256.Sum256([]byte(number + ":" + code))
	return h[:]
}

// consentOut is what the agent shows the owner: the code to send, and a
// link that opens WhatsApp with it written.
func (s *Server) consentOut(code string, expires time.Time) map[string]any {
	text := "AGREE " + code
	out := map[string]any{"code": code, "text": text, "expires_at": expires.UTC()}
	if n := strings.Trim(s.WhatsApp.Config.DisplayNumber, "+ "); n != "" {
		out["whatsapp_number"] = s.WhatsApp.Config.DisplayNumber
		out["link"] = "https://wa.me/" + strings.ReplaceAll(n, " ", "") + "?text=" + url.QueryEscape(text)
		// The owner scans it with their own phone, which sends from their number.
		if png, err := storefront.QRPNG(out["link"].(string)); err == nil {
			out["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	return out
}

// agentNewBusiness signs a business up for its owner, who is there with
// the agent: the business and (if they have no account) the owner's
// account are made at once, and the agent may work on it once the owner
// sends the code shown from their WhatsApp number.
func (s *Server) agentNewBusiness(w http.ResponseWriter, r *http.Request) {
	a := agentFrom(r.Context())
	ctx := r.Context()
	var req struct {
		Name       string `json:"name"`
		OwnerName  string `json:"owner_name"`
		OwnerPhone string `json:"owner_phone"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Name, req.OwnerName = strings.TrimSpace(req.Name), strings.TrimSpace(req.OwnerName)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 100 || req.OwnerName == "" || utf8.RuneCountInString(req.OwnerName) > 100 {
		s.fail(w, r, fmt.Errorf("%w: the business's name and the owner's name are needed, each at most 100 characters", errBadRequest))
		return
	}
	if s.WhatsApp == nil {
		s.fail(w, r, fmt.Errorf("%w: field agents need WhatsApp on this deployment: the owner agrees from their number", errConflict))
		return
	}
	number, err := whatsapp.Normalise(req.OwnerPhone, s.WhatsApp.Config.DefaultCountry)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
		return
	}
	if !s.limiter("agent-biz:"+a.ID.String(), 30*time.Minute, 20).Allow() || !s.limiter("agent-owner:"+number, 20*time.Minute, 3).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many businesses signed up at once; wait a while")
		return
	}
	owner, err := s.WhatsApp.UserOf(ctx, number)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	newOwner := false
	if owner == uuid.Nil {
		// Perhaps an earlier sign-up the owner never agreed to.
		err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, ownerPlaceholderEmail(number)).Scan(&owner)
		if errors.Is(err, pgx.ErrNoRows) {
			owner, newOwner = uuid.Must(uuid.NewV7()), true
		} else if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	tenant, code, actor := uuid.Must(uuid.NewV7()), otpCode(), "agent:"+a.Code
	var session uuid.UUID
	expires := time.Now().Add(agentConsentTTL)
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		stmts := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO tenants (id, name, plan_id) VALUES ($1, $2, $3)`, []any{tenant, req.Name, uuid.Nil}},
			{`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{tenant, owner}},
			{`INSERT INTO workspaces (id, tenant_id, name) VALUES ($1, $2, 'Default')`, []any{uuid.Must(uuid.NewV7()), tenant}},
			{`INSERT INTO environments (tenant_id, name) VALUES ($1, 'dev'), ($1, 'prod')`, []any{tenant}},
			{`INSERT INTO tenant_onboarding (tenant_id, source, signed_up_by) VALUES ($1, 'agent', $2)`, []any{tenant, owner}},
		}
		if newOwner {
			// No password: the owner works through WhatsApp.
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, owner, ownerPlaceholderEmail(number), req.OwnerName); err != nil {
				return err
			}
		}
		for _, st := range stmts {
			if _, err := tx.Exec(ctx, st.q, st.args...); err != nil {
				return err
			}
		}
		detail, _ := json.Marshal(map[string]any{"agent": a.Code, "owner": whatsapp.MaskNumber(number), "new_owner": newOwner, "ip": clientIP(r)})
		if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'agent', $2, 'tenant.create', $3, $4)`, tenant, actor, tenant.String(), detail); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT taskiem_agent_session_open($1, $2, 'onboarding', $3, $4, $5, $6)`, a.ID, tenant, owner, expires, number, consentHash(number, code)).Scan(&session)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Billing.Ensure(ctx, tenant, actor); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"session": agentSessionRow{ID: session, TenantID: tenant, Business: req.Name, Kind: "onboarding", Status: "pending", ExpiresAt: expires}.out(),
		"consent": s.consentOut(code, expires)})
}

// agentNewCode makes a new consent code for a pending onboarding.
func (s *Server) agentNewCode(w http.ResponseWriter, r *http.Request) {
	x, err := s.agentSessionOf(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if x.Kind != "onboarding" || x.Status != "pending" || x.OwnerNumber == nil {
		s.fail(w, r, fmt.Errorf("%w: only an onboarding waiting for the owner takes a new code", errConflict))
		return
	}
	if s.WhatsApp == nil {
		s.fail(w, r, fmt.Errorf("%w: WhatsApp is off on this deployment", errConflict))
		return
	}
	if !s.limiter("agent-code:"+x.ID.String(), 10*time.Minute, 3).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many new codes; wait a few minutes")
		return
	}
	code := otpCode()
	var expires time.Time
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_agent_session_recode($1, $2, $3)`, x.ID, agentFrom(r.Context()).ID, consentHash(*x.OwnerNumber, code)).Scan(&expires); err != nil {
		s.fail(w, r, err)
		return
	}
	x.ExpiresAt = expires
	writeJSON(w, http.StatusOK, map[string]any{"session": x.out(), "consent": s.consentOut(code, expires)})
}

// agentAttest records that the agent visited the business: a photo of
// the stall or shop (kept in the business's private storage, cleaned of
// its metadata) and where it was taken. The business is then "agent
// onboarded" (spec 7.1).
func (s *Server) agentAttest(w http.ResponseWriter, r *http.Request) {
	x, err := s.agentSessionOf(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if x.Kind != "onboarding" || x.Status != "active" {
		s.fail(w, r, fmt.Errorf("%w: a visit is recorded during an onboarding the owner agreed to", errConflict))
		return
	}
	q := r.URL.Query()
	lat, err1 := strconv.ParseFloat(q.Get("lat"), 64)
	lng, err2 := strconv.ParseFloat(q.Get("lng"), 64)
	note := strings.TrimSpace(q.Get("note"))
	if err1 != nil || err2 != nil || math.Abs(lat) > 90 || math.Abs(lng) > 180 || (lat == 0 && lng == 0) {
		s.fail(w, r, fmt.Errorf("%w: lat and lng are the place's coordinates, from the phone", errBadRequest))
		return
	}
	if utf8.RuneCountInString(note) > 200 {
		s.fail(w, r, fmt.Errorf("%w: the note is at most 200 characters", errBadRequest))
		return
	}
	if s.Media == nil {
		s.fail(w, r, fmt.Errorf("%w: storage for photos is not configured", errConflict))
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, media.MaxImageBytes+1))
	if err != nil || len(data) > media.MaxImageBytes {
		s.fail(w, r, fmt.Errorf("%w: a photo is at most %d MB", errBadRequest, media.MaxImageBytes>>20))
		return
	}
	if ct := http.DetectContentType(data); ct != "image/jpeg" && ct != "image/png" && ct != "image/webp" {
		s.fail(w, r, fmt.Errorf("%w: the body is a photo (JPEG, PNG or WebP)", errBadRequest))
		return
	}
	f, err := s.Media.PutFile(r.Context(), x.TenantID, data)
	if errors.Is(err, media.ErrFile) || errors.Is(err, media.ErrImage) {
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, plainError(err)))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	a := agentFrom(r.Context())
	id := uuid.Must(uuid.NewV7())
	detail := map[string]any{"photo": f.Path, "lat": lat, "lng": lng, "note": note, "session": x.ID, "agent": a.Code}
	err = db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{x.TenantID}, func(tx pgx.Tx) error {
		raw, _ := json.Marshal(detail)
		if _, err := tx.Exec(r.Context(), `INSERT INTO tenant_verifications (id, tenant_id, level, method, actor, detail) VALUES ($1, $2, 'agent_onboarded', 'agent_visit', $3, $4)`,
			id, x.TenantID, "agent:"+a.Code, raw); err != nil {
			return err
		}
		return agentAudit(r, tx, x.TenantID, a, "tenant.verified", id.String(), map[string]any{"level": "agent_onboarded", "session": x.ID})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"verification": map[string]any{"id": id, "level": "agent_onboarded", "method": "agent_visit"}})
}

func (s *Server) agentEndSession(w http.ResponseWriter, r *http.Request) {
	x, err := s.agentSessionOf(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	a := agentFrom(r.Context())
	var ended bool
	err = db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{x.TenantID}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT taskiem_agent_session_end($1, $2, 'agent_ended')`, x.ID, x.TenantID).Scan(&ended); err != nil || !ended {
			return err
		}
		return agentAudit(r, tx, x.TenantID, a, "agent.session_end", x.ID.String(), map[string]any{"reason": "agent_ended"})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	x.Status = "ended"
	writeJSON(w, http.StatusOK, map[string]any{"session": x.out()})
}

// agentAudit writes to a business's chain as the agent.
func agentAudit(r *http.Request, tx pgx.Tx, tenant uuid.UUID, a *agentInfo, action, target string, detail map[string]any) error {
	detail["ip"] = clientIP(r)
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(r.Context(), `SELECT taskiem_audit_append($1, 'agent', $2, $3, $4, $5)`, tenant, "agent:"+a.Code, action, target, raw)
	return err
}

// agentPublished ends an onboarding when the agent publishes the page,
// and tells the owner where it is and how to change it from WhatsApp.
func (s *Server) agentPublished(ctx context.Context, p *Principal, business, link string) {
	var ended bool
	var number *string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT owner_number FROM taskiem_agent_session_get($1, $2)`, p.Agent.Session, p.Agent.ID).Scan(&number); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT taskiem_agent_session_end($1, $2, 'published')`, p.Agent.Session, p.TenantID).Scan(&ended); err != nil || !ended {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'agent', $2, 'agent.session_end', $3, '{"reason": "published"}')`, p.TenantID, p.Actor(), p.Agent.Session.String())
		return err
	})
	if err != nil {
		s.Logger.Error("agents: ending an onboarding at publish", "err", err)
		return
	}
	if !ended || number == nil || s.WhatsApp == nil || link == "" {
		return
	}
	// The owner wrote to the platform number to agree, so this is inside
	// their conversation window.
	l := s.chooseLang(ctx, p.TenantID, *number, "").Tag
	if err := s.waSay(ctx, s.WhatsApp, *number, "", tr(l, "agent.page_live", "business", business, "link", link)); err != nil {
		s.Logger.Warn("agents: telling the owner their page is live", "err", err)
	}
}

// --- WhatsApp: the owner's side ---

var agreeRe = regexp.MustCompile(`(?i)^agree\s+(\d{6})$`)

// consentReplies answer a consent code that did not agree to anything.
var consentReplies = map[string]string{"wrong": "agent.consent_wrong", "locked": "agent.consent_locked", "none": "agent.consent_none",
	"number_taken": "agent.consent_taken"}

// waAgentMessage answers AGREE <code> (the owner agrees to an agent
// setting up their business) and STOP AGENT (the owner ends every agent
// session on their businesses), sent to the platform number. Other
// messages go on as usual.
func (s *Server) waAgentMessage(ctx context.Context, wa *whatsapp.Platform, number string, in whatsapp.Inbound) (bool, error) {
	if wa.Own() || in.Type != "text" {
		return false, nil
	}
	text := strings.Join(strings.Fields(in.Text), " ")
	l := s.chooseLang(ctx, wa.Tenant, number, "").Tag
	if m := agreeRe.FindStringSubmatch(text); m != nil {
		var outcome string
		var session, tenant, owner *uuid.UUID
		var business, agent, code *string
		var expires *time.Time
		if err := s.Store.Pool.QueryRow(ctx, `SELECT outcome, session_id, tenant_id, tenant_name, agent_name, agent_code, owner_user, expires_at
			FROM taskiem_agent_session_consent($1, $2)`, number, m[1]).Scan(&outcome, &session, &tenant, &business, &agent, &code, &owner, &expires); err != nil {
			return true, err
		}
		if outcome != "ok" {
			msg, ok := consentReplies[outcome]
			if !ok {
				msg = "agent.consent_none"
			}
			return true, s.waSay(ctx, wa, number, "", tr(l, msg))
		}
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{*tenant}, func(tx pgx.Tx) error {
			raw, _ := json.Marshal(map[string]any{"number": whatsapp.MaskNumber(number), "session": session, "agent": *code})
			if _, err := tx.Exec(ctx, `INSERT INTO tenant_verifications (id, tenant_id, level, method, actor, detail) VALUES ($1, $2, 'phone', 'whatsapp', $3, $4)`,
				uuid.Must(uuid.NewV7()), *tenant, owner.String(), raw); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'agent.session_consent', $3, $4)`, *tenant, owner.String(), session.String(), raw)
			return err
		})
		if err != nil {
			return true, err
		}
		return true, s.waSay(ctx, wa, number, "", tr(l, "agent.consent_ok", "agent", *agent, "code", *code, "business", *business))
	}
	if !strings.EqualFold(text, "stop agent") {
		return false, nil
	}
	owner, err := s.WhatsApp.UserOf(ctx, number)
	if err != nil || owner == uuid.Nil {
		return false, err
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, agent_name FROM taskiem_agent_sessions_end_for_owner($1)`, owner)
	if err != nil {
		return true, err
	}
	type ended struct {
		Tenant uuid.UUID
		Agent  string
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ended])
	if err != nil {
		return true, err
	}
	if len(list) == 0 {
		return true, s.waSay(ctx, wa, number, "", tr(l, "agent.none_to_stop"))
	}
	var names []string
	for _, e := range list {
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'agent.session_end', $3, '{"reason": "owner_ended", "via": "whatsapp"}')`, e.Tenant, owner.String(), e.Tenant.String())
			return err
		}); err != nil {
			return true, err
		}
		if !slices.Contains(names, e.Agent) {
			names = append(names, e.Agent)
		}
	}
	return true, s.waSay(ctx, wa, number, "", tr(l, "agent.stopped", "agents", strings.Join(names, ", ")))
}

// --- the owner's side, in the app ---

type agentVisit struct {
	ID        uuid.UUID  `json:"id"`
	AgentName string     `json:"agent_name"`
	AgentCode string     `json:"agent_code"`
	Kind      string     `json:"kind"`
	Status    string     `json:"status"`
	ConsentAt *time.Time `json:"consent_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	EndedAt   *time.Time `json:"ended_at"`
	EndReason *string    `json:"end_reason"`
	CreatedAt time.Time  `json:"created_at"`
}

// listAgentVisits lists the agent sessions on the business: who came,
// when, and how each ended.
func (s *Server) listAgentVisits(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT id, agent_name, agent_code, kind, status, consent_at, expires_at, ended_at, end_reason, created_at
		FROM taskiem_tenant_agent_sessions($1, 100)`, principalFrom(r.Context()).TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[agentVisit])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"visits": nonNil(out)})
}

// inviteAgent lets an agent back in for a support visit of a few hours:
// the owner gives the code on the agent's card.
func (s *Server) inviteAgent(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "an agent is invited by a person, not an API key")
		return
	}
	var req struct {
		AgentCode string `json:"agent_code"`
		Hours     int    `json:"hours"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Hours == 0 {
		req.Hours = 2
	}
	if req.Hours < 1 || req.Hours > agentVisitMaxHours {
		s.fail(w, r, fmt.Errorf("%w: a visit lasts 1 to %d hours", errBadRequest, agentVisitMaxHours))
		return
	}
	var agent uuid.UUID
	var name string
	err := s.Store.Pool.QueryRow(r.Context(), `SELECT id, name FROM taskiem_agent_by_code($1)`, strings.TrimSpace(req.AgentCode)).Scan(&agent, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, fmt.Errorf("%w: no active agent has that code; check the agent's card", errBadRequest))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var v agentVisit
	err = s.tx(r, func(tx pgx.Tx) error {
		expires := time.Now().Add(time.Duration(req.Hours) * time.Hour)
		if err := tx.QueryRow(r.Context(), `SELECT taskiem_agent_session_open($1, $2, 'support', $3, $4, NULL, NULL)`, agent, p.TenantID, p.UserID, expires).Scan(&v.ID); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `SELECT agent_name, agent_code, kind, status, consent_at, expires_at, ended_at, end_reason, created_at
			FROM taskiem_tenant_agent_sessions($1, 100) WHERE id = $2`, p.TenantID, v.ID).
			Scan(&v.AgentName, &v.AgentCode, &v.Kind, &v.Status, &v.ConsentAt, &v.ExpiresAt, &v.EndedAt, &v.EndReason, &v.CreatedAt); err != nil {
			return err
		}
		return auditTx(r, tx, "agent.visit_invite", v.ID.String(), map[string]any{"agent": v.AgentCode, "hours": req.Hours})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"visit": v})
}

// endAgentVisit ends an agent session on the business, of either kind.
func (s *Server) endAgentVisit(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		var ended bool
		if err := tx.QueryRow(r.Context(), `SELECT taskiem_agent_session_end($1, $2, 'owner_ended')`, id, principalFrom(r.Context()).TenantID).Scan(&ended); err != nil {
			return err
		}
		if !ended {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "agent.session_end", id.String(), map[string]any{"reason": "owner_ended"})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "ended"})
}

// --- operators: registering and managing agents ---

// uniqueViolation reports a unique constraint's refusal, and which.
func uniqueViolation(err error) (string, bool) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return pe.ConstraintName, true
	}
	return "", false
}

const agentCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newAgentCode() string {
	b := make([]byte, 5)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(agentCodeAlphabet))))
		b[i] = agentCodeAlphabet[n.Int64()]
	}
	return "AG-" + string(b)
}

type opsAgent struct {
	ID           uuid.UUID       `json:"id"`
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	Phone        string          `json:"phone"`
	Region       string          `json:"region"`
	Status       string          `json:"status"`
	Checks       json.RawMessage `json:"checks"`
	RegisteredBy string          `json:"registered_by"`
	CreatedAt    time.Time       `json:"created_at"`
	Businesses   int64           `json:"businesses"`
	Published    int64           `json:"published"`
	Verified     int64           `json:"verified"`
	LiveSessions int64           `json:"live_sessions"`
}

func (s *Server) opsAgents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT id, code, name, phone, region, status, checks, registered_by, created_at, businesses, published, verified, live_sessions
		FROM taskiem_ops_agents()`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[opsAgent])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": nonNil(out)})
}

func (s *Server) opsAgentBusinesses(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such agent")
		return
	}
	type business struct {
		TenantID  uuid.UUID `json:"tenant_id"`
		Name      string    `json:"name"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"created_at"`
		Published bool      `json:"published"`
		Verified  bool      `json:"verified"`
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT tenant_id, name, status, created_at, published, verified FROM taskiem_ops_agent_businesses($1)`, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[business])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"businesses": nonNil(out)})
}

// opsAddAgent registers an agent the operator has checked in person. A
// new person gets an email to choose their password.
func (s *Server) opsAddAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email  string `json:"email"`
		Name   string `json:"name"`
		Phone  string `json:"phone"`
		Region string `json:"region"`
		Checks struct {
			IDType       string `json:"id_type"`
			IDChecked    bool   `json:"id_checked"`
			RefereeName  string `json:"referee_name"`
			RefereePhone string `json:"referee_phone"`
		} `json:"checks"`
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	email, name, region := strings.TrimSpace(req.Email), strings.TrimSpace(req.Name), strings.TrimSpace(req.Region)
	if !strings.Contains(email, "@") || len(email) > 320 || name == "" || utf8.RuneCountInString(name) > 100 || utf8.RuneCountInString(region) > 100 {
		writeErr(w, http.StatusUnprocessableEntity, "an agent needs an email, a name (at most 100 characters) and may have a region")
		return
	}
	if !req.Checks.IDChecked || strings.TrimSpace(req.Checks.IDType) == "" || strings.TrimSpace(req.Checks.RefereeName) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "an agent's ID is checked in person, and a referee named, before they are registered")
		return
	}
	country := "234"
	if s.WhatsApp != nil && s.WhatsApp.Config.DefaultCountry != "" {
		country = s.WhatsApp.Config.DefaultCountry
	}
	phone, err := whatsapp.Normalise(req.Phone, country)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "the agent's phone: "+err.Error())
		return
	}
	ctx := r.Context()
	var user uuid.UUID
	var hash *string
	var status string
	err = s.Store.Pool.QueryRow(ctx, `SELECT user_id, password_hash, status FROM taskiem_auth_find_user($1)`, email).Scan(&user, &hash, &status)
	newUser := errors.Is(err, pgx.ErrNoRows)
	switch {
	case newUser:
		if !s.mailOn() {
			writeErr(w, http.StatusConflict, "this deployment cannot send email, so a new agent could not choose a password")
			return
		}
		user = uuid.Must(uuid.NewV7())
	case err != nil:
		s.fail(w, r, err)
		return
	case status != "active":
		writeErr(w, http.StatusConflict, "that account is disabled")
		return
	default:
		var exists bool
		if err := s.Store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM taskiem_agent_of($1))`, user).Scan(&exists); err != nil {
			s.fail(w, r, err)
			return
		}
		if exists {
			writeErr(w, http.StatusConflict, "that person is already an agent")
			return
		}
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.agent.add", strings.ToLower(email)) {
		return
	}
	o := operatorFrom(ctx)
	checks, _ := json.Marshal(map[string]any{"id_type": strings.TrimSpace(req.Checks.IDType), "id_checked": true,
		"referee_name": strings.TrimSpace(req.Checks.RefereeName), "referee_phone": strings.TrimSpace(req.Checks.RefereePhone)})
	id, workspace := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var code string
	for range 3 {
		code = newAgentCode()
		err = s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
			if err := widen(workspace); err != nil {
				return err
			}
			if newUser {
				if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, user, email, name); err != nil {
					return err
				}
			}
			// The agent's own workspace: where they land, as a viewer (so
			// never an administrator held to passkeys).
			for _, st := range []struct {
				q    string
				args []any
			}{
				{`INSERT INTO tenants (id, name, plan_id) VALUES ($1, $2, $3)`, []any{workspace, name + " (agent " + code + ")", uuid.Nil}},
				{`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'viewer')`, []any{workspace, user}},
				{`INSERT INTO workspaces (id, tenant_id, name) VALUES ($1, $2, 'Default')`, []any{uuid.Must(uuid.NewV7()), workspace}},
				{`INSERT INTO environments (tenant_id, name) VALUES ($1, 'dev'), ($1, 'prod')`, []any{workspace}},
				{`SELECT taskiem_ops_agent_add($1, $2, $3, $4, $5, $6, $7, $8, $9)`, []any{id, user, workspace, code, name, phone, region, checks, o.Actor()}},
			} {
				if _, err := tx.Exec(ctx, st.q, st.args...); err != nil {
					return err
				}
			}
			if err := tenantAudit(r, tx, workspace, o.Actor(), "agent.registered", code, map[string]any{"via": "console"}); err != nil {
				return err
			}
			return ops.AuditTx(ctx, tx, o.Actor(), "ops.agent.add", code, map[string]any{"agent": id, "email": email, "region": region, "new_user": newUser})
		})
		if constraint, ok := uniqueViolation(err); !ok || constraint != "agents_code_key" {
			break // a code drawn twice is drawn again
		}
	}
	if err != nil {
		if _, ok := uniqueViolation(err); ok {
			writeErr(w, http.StatusConflict, "that email was just registered")
			return
		}
		s.opsDBFail(w, r, err)
		return
	}
	sent := false
	if newUser {
		sent = s.sendAgentWelcome(ctx, user, email, name, code)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"agent": map[string]any{"id": id, "code": code, "name": name, "phone": phone, "region": region, "status": "active",
		"workspace_id": workspace}, "setup_email_sent": sent})
}

// sendAgentWelcome emails a new agent a link to choose their password
// (a password reset link, good for three days).
func (s *Server) sendAgentWelcome(ctx context.Context, user uuid.UUID, email, name, code string) bool {
	token, selector, secretHash := newResetToken()
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_auth_password_reset_issue($1, $2, $3, $4::interval, $5)`, user, selector, secretHash, (72 * time.Hour).String(), "console"); err != nil {
		s.Logger.Error("agents: a set-up link", "err", err)
		return false
	}
	link := strings.TrimRight(s.PublicURL, "/") + "/reset-password#token=" + token
	body := fmt.Sprintf(`Hello %s,

You are now a Taskiem field agent. Your agent code is %s: it is on your card, and business owners use it to invite you back.

Choose your password here. The link works once, for three days:

%s

Then sign in at %s/agent.
`, name, code, link, strings.TrimRight(s.PublicURL, "/"))
	if err := s.sendMail(ctx, email, "Welcome, Taskiem field agent", body); err != nil {
		s.Logger.Error("agents: the welcome email was not sent", "err", err)
		return false
	}
	return true
}

func (s *Server) opsAgentAction(w http.ResponseWriter, r *http.Request) {
	idText, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
	status := map[string]string{"suspend": "suspended", "reactivate": "active", "retire": "retired"}[action]
	if status == "" {
		writeErr(w, http.StatusNotFound, "the action is suspend, reactivate or retire")
		return
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such agent")
		return
	}
	var req struct {
		Note   string     `json:"note"`
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if action != "reactivate" && strings.TrimSpace(req.Note) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "say why, in a note")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.agent."+action, id.String()) {
		return
	}
	o := operatorFrom(r.Context())
	err = s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		if _, err := tx.Exec(r.Context(), `SELECT taskiem_ops_agent_set_status($1, $2)`, id, status); err != nil {
			return err
		}
		return ops.AuditTx(r.Context(), tx, o.Actor(), "ops.agent."+action, id.String(), map[string]any{"note": req.Note})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status})
}
