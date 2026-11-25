package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// The template registry (docs/whatsapp.md#templates): per WhatsApp
// Business Account (the platform number's, TASKIEM_WHATSAPP_WABA_ID, and
// each own number's), what Meta knows of each template in the library
// (whatsapp_templates, migration 00224): submitted or not, pending,
// approved, rejected with the reason, paused, disabled. A template is
// submitted from here through the Business Management API (POST
// /{waba-id}/message_templates), its status comes back on the
// message_template_status_update webhook and from a poll (GET
// /{waba-id}/message_templates), on demand and hourly. Outside the
// 24-hour window only a template the registry holds as approved is sent;
// the callers fall back as they did before the template existed.
//
// Built from Meta's public documentation only (docs/whatsapp.md names
// the pages).

// Template statuses in the registry.
const (
	TemplateNotSubmitted = "not_submitted"
	TemplatePending      = "pending"
	TemplateApproved     = "approved"
	TemplateRejected     = "rejected"
	TemplatePaused       = "paused"
	TemplateDisabled     = "disabled"
)

// ErrTemplateNotApproved: the registry does not hold the template as
// approved in the number's WhatsApp Business Account, so it was not sent.
var ErrTemplateNotApproved = errors.New("whatsapp: the template is not approved in this WhatsApp Business Account")

var graphIDRe = regexp.MustCompile(`^[0-9]{5,32}$`)

// TemplateStatus is a library template's standing in one account.
type TemplateStatus struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Language string `json:"language"`
	// Status is one of the Template* statuses.
	Status string `json:"status"`
	// Reason is Meta's reason for a rejection, pause or disabling ("" when none).
	Reason string `json:"reason,omitempty"`
	// MetaID is the template's id in Meta's account once submitted.
	MetaID      string     `json:"meta_id,omitempty"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	Purpose     string     `json:"purpose"`
	// MetaCategory is the category Meta gave the template in this
	// account (utility, marketing, authentication), which may differ
	// from the one it was submitted in; "" until Meta lists it.
	MetaCategory string `json:"meta_category,omitempty"`
	// Matches says whether the body and buttons Meta holds are the
	// library's; nil until Meta lists them.
	Matches *bool `json:"matches,omitempty"`
}

// StatusFromMeta maps a status or webhook event Meta reports to the
// registry's statuses; false for an event that changes no status (a
// quality flag, an edit).
func StatusFromMeta(s string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "APPROVED", "REINSTATED", "UNARCHIVED":
		return TemplateApproved, true
	case "PENDING", "IN_APPEAL":
		return TemplatePending, true
	case "REJECTED":
		return TemplateRejected, true
	case "PAUSED":
		return TemplatePaused, true
	case "DISABLED", "LOCKED", "LIMIT_EXCEEDED":
		return TemplateDisabled, true
	case "DELETED", "PENDING_DELETION", "ARCHIVED":
		return TemplateNotSubmitted, true
	}
	return "", false
}

// TemplateUsable reports whether a template may be sent from this
// number now: with no account tracked (no WABA id), or the dev flag on,
// every template may; otherwise only one the registry holds as approved
// (ErrTemplateNotApproved).
func (p *Platform) TemplateUsable(ctx context.Context, t Template) error {
	if p.Config.AssumeApproved || p.WABA == "" {
		return nil
	}
	var status string
	err := p.registry(ctx, func(q querier) error {
		err := q.QueryRow(ctx, `SELECT status FROM whatsapp_templates WHERE waba_id = $1 AND name = $2 AND language = $3`, p.WABA, t.Name, p.Config.Language).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			status = TemplateNotSubmitted
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	if status != TemplateApproved {
		return fmt.Errorf("%w: %s is %s", ErrTemplateNotApproved, t.Name, strings.ReplaceAll(status, "_", " "))
	}
	return nil
}

// TemplateUnusable reports whether a send failed because the template
// cannot carry the message now, so the caller should fall back: not
// approved in the registry, a link its button cannot open, or Meta's
// template errors (132000 to 132016: missing, not approved, paused, or
// parameters that do not fit).
func TemplateUnusable(err error) bool {
	if errors.Is(err, ErrTemplateNotApproved) || errors.Is(err, ErrTemplateLink) {
		return true
	}
	var ge *GraphError
	return errors.As(err, &ge) && ge.Code >= 132000 && ge.Code <= 132016
}

// querier is a pool or a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type pgconnCommandTag = interface{ RowsAffected() int64 }

type poolQuerier struct{ *pgxpool.Pool }

func (q poolQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error) {
	return q.Pool.Exec(ctx, sql, args...)
}

type txQuerier struct{ pgx.Tx }

func (q txQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error) {
	return q.Tx.Exec(ctx, sql, args...)
}

// registry runs fn on the account's rows: the platform account's rows
// have no tenant and are read from the pool; an own number's are its
// tenant's, under row-level security.
func (p *Platform) registry(ctx context.Context, fn func(q querier) error) error {
	if !p.Own() {
		return fn(poolQuerier{p.Pool})
	}
	return db.InTenantTx(ctx, p.Pool, []uuid.UUID{p.Tenant}, func(tx pgx.Tx) error { return fn(txQuerier{tx}) })
}

func (p *Platform) tenantRef() *uuid.UUID {
	if !p.Own() {
		return nil
	}
	t := p.Tenant
	return &t
}

// ErrNoWABA: the number's WhatsApp Business Account is not known, so its
// templates cannot be submitted or checked.
var ErrNoWABA = errors.New("whatsapp: the number's WhatsApp Business Account id is not set (TASKIEM_WHATSAPP_WABA_ID)")

// TemplateStatuses lists the library with each template's standing in
// this number's account; every template is not_submitted when nothing is
// recorded.
func (p *Platform) TemplateStatuses(ctx context.Context) ([]TemplateStatus, error) {
	lib := p.Library()
	out := make([]TemplateStatus, 0, len(lib))
	for _, t := range lib {
		out = append(out, TemplateStatus{Name: t.Name, Category: Category(t), Language: p.Config.Language, Status: TemplateNotSubmitted, Purpose: t.Purpose})
	}
	if p.WABA == "" {
		return out, nil
	}
	err := p.registry(ctx, func(q querier) error {
		rows, err := q.Query(ctx, `SELECT name, language, status, reason, COALESCE(meta_id, ''), submitted_at, checked_at, lower(COALESCE(meta_category, '')), meta_matches
			FROM whatsapp_templates WHERE waba_id = $1`, p.WABA)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name, language string
			var st TemplateStatus
			if err := rows.Scan(&name, &language, &st.Status, &st.Reason, &st.MetaID, &st.SubmittedAt, &st.CheckedAt, &st.MetaCategory, &st.Matches); err != nil {
				return err
			}
			for i := range out {
				if out[i].Name == name && out[i].Language == language {
					out[i].Status, out[i].Reason, out[i].MetaID, out[i].SubmittedAt, out[i].CheckedAt = st.Status, st.Reason, st.MetaID, st.SubmittedAt, st.CheckedAt
					out[i].MetaCategory, out[i].Matches = st.MetaCategory, st.Matches
				}
			}
		}
		return rows.Err()
	})
	return out, err
}

// NotApprovedNote says what cannot go out of the window until the
// templates not yet approved are: one line for the Channels page and the
// operator command; "" when every template is approved.
func NotApprovedNote(statuses []TemplateStatus) string {
	var names []string
	for _, s := range statuses {
		if s.Status != TemplateApproved {
			names = append(names, s.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "Until these templates are approved, their messages do not go on WhatsApp outside a person's 24-hour window and fall back (SMS, email, the web app): " + strings.Join(names, ", ") + "."
}

// metaView is what Meta lists of a template beyond its status: its
// category in the account and whether its body and buttons are ours. A
// nil one keeps what was recorded (a status webhook carries neither); an
// empty one forgets it (Meta no longer holds the template).
type metaView struct {
	category string
	matches  bool
}

// record writes a template's standing as Meta reported it.
func (p *Platform) record(ctx context.Context, q querier, name, language, metaID, status, reason string, submitted bool, meta *metaView) error {
	var submittedAt *time.Time
	if submitted {
		now := p.now()
		submittedAt = &now
	}
	var mid *string
	if metaID != "" {
		mid = &metaID
	}
	if !p.Own() {
		// The platform number's rows have no tenant; tenants may read but
		// not write them, so they are written through a definer that works
		// only outside any tenant scope (migration 00353).
		_, err := q.Exec(ctx, `SELECT taskiem_wa_platform_template_record($1, $2, $3, $4, $5, $6, $7)`,
			p.WABA, name, language, mid, status, clip(reason, 1000), submittedAt)
		return err
	}
	// An own number's row is its tenant's: the upsert never moves a row
	// between owners (row-level security refuses another owner's row).
	// Meta's category and whether its words are ours are kept as last
	// listed when this answer does not carry them.
	var category *string
	var matches *bool
	if meta != nil && meta.category != "" {
		c := clip(strings.ToLower(meta.category), 40)
		category, matches = &c, &meta.matches
	}
	_, err := q.Exec(ctx, `INSERT INTO whatsapp_templates (waba_id, name, language, tenant_id, meta_id, status, reason, submitted_at, checked_at, meta_category, meta_matches)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), $9, $10)
		ON CONFLICT (waba_id, name, language) DO UPDATE SET meta_id = COALESCE(EXCLUDED.meta_id, whatsapp_templates.meta_id),
		  status = EXCLUDED.status, reason = EXCLUDED.reason, submitted_at = COALESCE(EXCLUDED.submitted_at, whatsapp_templates.submitted_at), checked_at = now(), updated_at = now(),
		  meta_category = CASE WHEN $11 THEN EXCLUDED.meta_category ELSE whatsapp_templates.meta_category END,
		  meta_matches = CASE WHEN $11 THEN EXCLUDED.meta_matches ELSE whatsapp_templates.meta_matches END
		WHERE whatsapp_templates.tenant_id = EXCLUDED.tenant_id`,
		p.WABA, name, language, p.tenantRef(), mid, status, clip(reason, 1000), submittedAt, category, matches, meta != nil)
	return err
}

// SubmitTemplate submits a library template to the number's account in
// the configured language, and records it as pending (or as Meta answers,
// when it answers a status at once). A template Meta already holds under
// that name is refused by Meta; CheckTemplates then records its status.
func (p *Platform) SubmitTemplate(ctx context.Context, name string) (TemplateStatus, error) {
	t, ok := p.TemplateNamed(name)
	if !ok {
		return TemplateStatus{}, fmt.Errorf("whatsapp: no template named %q", name)
	}
	if p.WABA == "" {
		return TemplateStatus{}, ErrNoWABA
	}
	def, err := t.Submission(p.Config.Language, p.Client.Hosts)
	if err != nil {
		return TemplateStatus{}, err
	}
	id, status, category, err := p.Client.CreateTemplate(ctx, p.WABA, def)
	if err != nil {
		return TemplateStatus{}, err
	}
	st, ok := StatusFromMeta(status)
	if !ok {
		st = TemplatePending
	}
	if err := p.registry(ctx, func(q querier) error {
		// Submitted as the library has it: ours until Meta lists it
		// otherwise; the category is the one Meta answered, if any.
		var meta *metaView
		if category != "" {
			meta = &metaView{category: category, matches: true}
		}
		return p.record(ctx, q, t.Name, p.Config.Language, id, st, "", true, meta)
	}); err != nil {
		return TemplateStatus{}, err
	}
	return p.templateStatus(ctx, t.Name)
}

func (p *Platform) templateStatus(ctx context.Context, name string) (TemplateStatus, error) {
	all, err := p.TemplateStatuses(ctx)
	if err != nil {
		return TemplateStatus{}, err
	}
	for _, s := range all {
		if s.Name == name {
			return s, nil
		}
	}
	return TemplateStatus{}, fmt.Errorf("whatsapp: no template named %q", name)
}

// CheckTemplates asks Meta for the account's templates and records the
// standing of each library template in the configured language: what
// Meta lists is recorded as it stands; a library template Meta does not
// list is recorded as not submitted.
func (p *Platform) CheckTemplates(ctx context.Context) ([]TemplateStatus, error) {
	if p.WABA == "" {
		return nil, ErrNoWABA
	}
	listed, err := p.Client.ListTemplates(ctx, p.WABA)
	if err != nil {
		return nil, err
	}
	byName := map[string]MetaTemplate{}
	for _, m := range listed {
		byName[m.Name+"\x00"+m.Language] = m
	}
	err = p.registry(ctx, func(q querier) error {
		for _, t := range p.Library() {
			language := p.Config.Language
			m, ok := byName[t.Name+"\x00"+language]
			if !ok {
				if err := p.record(ctx, q, t.Name, language, "", TemplateNotSubmitted, "", false, &metaView{}); err != nil {
					return err
				}
				continue
			}
			st, known := StatusFromMeta(m.Status)
			if !known {
				st = TemplatePending
			}
			meta := &metaView{category: m.Category, matches: ComponentsMatch(t, language, p.Client.Hosts, m.Components)}
			if err := p.record(ctx, q, t.Name, language, m.ID, st, m.Reason(), false, meta); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p.TemplateStatuses(ctx)
}

// TemplateStatusUpdate is one message_template_status_update webhook
// event (webhook.go).
type TemplateStatusUpdate struct {
	WABA     string
	Event    string
	MetaID   string
	Name     string
	Language string
	Reason   string
}

// ApplyTemplateStatus records a status event for whichever account the
// WABA is: the platform's, or an own number's tenant (found through
// taskiem_wa_waba_tenant). Events for templates not in the library, in
// another language, or for an unknown account are ignored.
func (p *Platform) ApplyTemplateStatus(ctx context.Context, u TemplateStatusUpdate) error {
	if _, ok := TemplateNamed(u.Name); !ok || u.WABA == "" || u.Language != p.Config.Language {
		return nil
	}
	st, ok := StatusFromMeta(u.Event)
	if !ok {
		return nil
	}
	q := p
	switch {
	case u.WABA == p.WABA && p.WABA != "":
	case p.Own():
		return nil
	default:
		var tenant uuid.UUID
		err := p.Pool.QueryRow(ctx, `SELECT taskiem_wa_waba_tenant($1)`, u.WABA).Scan(&tenant)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && tenant == uuid.Nil {
			return nil
		}
		if err != nil {
			return err
		}
		if q, err = p.ForTenant(ctx, tenant); err != nil {
			return err
		}
		if q.WABA != u.WABA {
			return nil
		}
	}
	if _, ok := q.TemplateNamed(u.Name); !ok {
		return nil // an own number's template, on the shared number's account
	}
	return q.registry(ctx, func(qq querier) error {
		return q.record(ctx, qq, u.Name, u.Language, u.MetaID, st, u.Reason, false, nil)
	})
}

// PollTemplates checks every tracked account against Meta: the platform
// number's (when its WABA id is set) and each active own number's. Run it
// hourly, and once at start, so a template approved while Taskiem was not
// listening is known within the hour.
func (p *Platform) PollTemplates(ctx context.Context) error {
	var errs []error
	if p.WABA != "" {
		if _, err := p.CheckTemplates(ctx); err != nil {
			errs = append(errs, fmt.Errorf("platform account: %w", err))
		}
	}
	if p.Own() || p.Secrets == nil {
		return errors.Join(errs...)
	}
	rows, err := p.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_wa_own_numbers()`)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, t := range tenants {
		q, err := p.ForTenant(ctx, t)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !q.Own() {
			continue
		}
		if _, err := q.CheckTemplates(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return errors.Join(errs...)
}

// RunTemplatePolls polls at start and then every interval until ctx ends
// (the api role).
func (p *Platform) RunTemplatePolls(ctx context.Context, interval time.Duration) {
	for {
		if err := p.PollTemplates(ctx); err != nil && ctx.Err() == nil && p.Logger != nil {
			p.Logger.Warn("whatsapp: checking template statuses", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// --- the Business Management API ---

// MetaTemplate is a template as Meta lists it.
type MetaTemplate struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Language       string `json:"language"`
	Status         string `json:"status"`
	Category       string `json:"category"`
	RejectedReason string `json:"rejected_reason"`
	// Components are the body and buttons as Meta holds them.
	Components []MetaComponent `json:"components,omitempty"`
}

// MetaComponent is one component of a template as Meta lists it: the
// body's text, or the buttons.
type MetaComponent struct {
	Type    string       `json:"type"`
	Text    string       `json:"text,omitempty"`
	Buttons []MetaButton `json:"buttons,omitempty"`
}

// MetaButton is a template's button as Meta lists it.
type MetaButton struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
}

// Reason is the rejection reason worth recording ("" for NONE).
func (m MetaTemplate) Reason() string {
	if strings.EqualFold(m.RejectedReason, "NONE") {
		return ""
	}
	return m.RejectedReason
}

// CreateTemplate submits a template to a WhatsApp Business Account (POST
// /{waba-id}/message_templates): Meta's id for it and the status it
// answers (PENDING until reviewed), and the category it answers.
func (c *Client) CreateTemplate(ctx context.Context, waba string, def map[string]any) (id, status, category string, err error) {
	if !graphIDRe.MatchString(waba) {
		return "", "", "", ErrNoWABA
	}
	body, err := json.Marshal(def)
	if err != nil {
		return "", "", "", err
	}
	raw, err := c.call(ctx, http.MethodPost, "/"+url.PathEscape(waba)+"/message_templates", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", "", err
	}
	var out struct {
		ID       json.RawMessage `json:"id"`
		Status   string          `json:"status"`
		Category string          `json:"category"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", "", errors.New("whatsapp: unreadable template submission answer")
	}
	return anyID(out.ID), out.Status, out.Category, nil
}

// ListTemplates lists an account's templates (GET
// /{waba-id}/message_templates), following the paging cursor.
func (c *Client) ListTemplates(ctx context.Context, waba string) ([]MetaTemplate, error) {
	if !graphIDRe.MatchString(waba) {
		return nil, ErrNoWABA
	}
	var out []MetaTemplate
	after := ""
	for range 20 {
		q := url.Values{"fields": {"id,name,language,status,category,rejected_reason,components"}, "limit": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		raw, err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(waba)+"/message_templates?"+q.Encode(), "", nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Data []struct {
				ID             json.RawMessage `json:"id"`
				Name           string          `json:"name"`
				Language       string          `json:"language"`
				Status         string          `json:"status"`
				Category       string          `json:"category"`
				RejectedReason string          `json:"rejected_reason"`
				Components     []MetaComponent `json:"components"`
			} `json:"data"`
			Paging struct {
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, errors.New("whatsapp: unreadable template list")
		}
		for _, d := range page.Data {
			out = append(out, MetaTemplate{ID: anyID(d.ID), Name: d.Name, Language: d.Language, Status: d.Status, Category: d.Category, RejectedReason: d.RejectedReason,
				Components: d.Components})
		}
		if page.Paging.Next == "" || page.Paging.Cursors.After == "" || page.Paging.Cursors.After == after {
			break
		}
		after = page.Paging.Cursors.After
	}
	return out, nil
}

// anyID reads an id Meta gives as a string or a number, without going
// through a float.
func anyID(v json.RawMessage) string {
	s := strings.TrimSpace(string(v))
	if strings.HasPrefix(s, `"`) {
		var out string
		if json.Unmarshal(v, &out) == nil {
			return out
		}
		return ""
	}
	if s == "null" {
		return ""
	}
	return s
}

// ComponentsMatch reports whether the body and buttons Meta holds for a
// template in an account are the library's (as Submission would send
// them in lang under hosts): the same body text, no header or footer of
// its own, and the same buttons in order, each of the same type, words
// and address. An authentication template's words are Meta's own, so
// any match.
func ComponentsMatch(t Template, lang string, hosts Hosts, comps []MetaComponent) bool {
	if t.Category == "AUTHENTICATION" {
		return true
	}
	def, err := t.Submission(lang, hosts)
	if err != nil || len(comps) == 0 {
		return false
	}
	var want []MetaButton
	for _, c := range def["components"].([]any) {
		m := c.(map[string]any)
		if m["type"] != "BUTTONS" {
			continue
		}
		for _, b := range m["buttons"].([]any) {
			bm := b.(map[string]any)
			u, _ := bm["url"].(string)
			want = append(want, MetaButton{Type: bm["type"].(string), Text: bm["text"].(string), URL: u})
		}
	}
	body, buttons := false, false
	for _, c := range comps {
		switch strings.ToUpper(c.Type) {
		case "BODY":
			if body || c.Text != t.Body {
				return false
			}
			body = true
		case "BUTTONS":
			if buttons || len(c.Buttons) != len(want) {
				return false
			}
			for i, b := range c.Buttons {
				if !strings.EqualFold(b.Type, want[i].Type) || b.Text != want[i].Text || b.URL != want[i].URL {
					return false
				}
			}
			buttons = true
		default:
			return false
		}
	}
	return body && buttons == (len(want) > 0)
}

// TemplateCategory is the category Meta gave a template in this own
// number's account ("utility", "marketing"), where Meta holds it approved
// with the library's body and buttons (CheckTemplates records both); ""
// where it does not, or on the shared number. A template whose category
// decides who may get it (the follow-up of a form left half-filled) is
// sent, or promised, only on this answer.
func (p *Platform) TemplateCategory(ctx context.Context, t Template) (string, error) {
	if !p.Own() || p.WABA == "" {
		return "", nil
	}
	var category string
	err := p.registry(ctx, func(q querier) error {
		err := q.QueryRow(ctx, `SELECT lower(meta_category) FROM whatsapp_templates WHERE waba_id = $1 AND name = $2 AND language = $3 AND status = 'approved'
			AND meta_category IS NOT NULL AND meta_matches`, p.WABA, t.Name, p.Config.Language).Scan(&category)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return category, err
}
