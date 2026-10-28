package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/publicnames"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Hosted forms (docs/forms.md): the organisation's forms and their
// settings, one-time links, submissions and their files, and the forms
// members fill in in the app (members forms, and form steps addressed to
// a role).

// formsDeployable refuses deploying a definition whose forms this
// deployment cannot serve, or which the organisation may not put live.
func (s *Server) formsDeployable(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, d *wd.Definition) error {
	trigger := d.Trigger.Type == "form"
	var steps []*wd.Step
	var walk func([]*wd.Step)
	walk = func(list []*wd.Step) {
		for _, st := range list {
			if st.Type == "form" && st.Form != nil {
				steps = append(steps, st)
			}
			for _, c := range st.Children() {
				walk(c)
			}
		}
	}
	walk(d.Steps)
	if !trigger && len(steps) == 0 {
		return nil
	}
	if s.Forms == nil {
		return fmt.Errorf("%w: forms are not available on this deployment (TASKIEM_PUBLIC_KEY is unset)", errConflict)
	}
	files := false
	for _, st := range steps {
		files = files || wd.HasFileFields(st.Form.Schema)
	}
	if trigger {
		f, err := d.FormTriggerConfig()
		if err != nil {
			return fmt.Errorf("%w: %w", errBadRequest, err)
		}
		if err := publicnames.CheckDisplayName(f.Title); err != nil {
			return fmt.Errorf("%w: the form's title: %s", errBadRequest, plainError(err))
		}
		raw, _ := d.InputsSchema()
		schema, _ := raw.(map[string]any)
		files = files || wd.HasFileFields(schema)
		if f.Access == "public" {
			var confirmed bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
				WHERE m.tenant_id = $1 AND m.role = 'owner' AND u.email_verified_at IS NOT NULL)`, tenant).Scan(&confirmed); err != nil {
				return err
			}
			if !confirmed {
				return fmt.Errorf("%w: confirm the organisation's email before putting a public form live", errConflict)
			}
		}
	}
	if files && s.Forms.Scanner == nil {
		return fmt.Errorf("%w: this form asks for files, and files need a malware scanner (TASKIEM_CLAMD_ADDR) on this deployment", errConflict)
	}
	return nil
}

type formOut struct {
	ID             uuid.UUID  `json:"id"`
	PublicID       string     `json:"public_id"`
	WorkflowID     uuid.UUID  `json:"workflow_id"`
	Workflow       string     `json:"workflow"`
	Environment    string     `json:"environment"`
	Version        int        `json:"version"`
	Title          string     `json:"title"`
	Access         string     `json:"access"`
	Status         string     `json:"status"`
	OpensAt        *time.Time `json:"opens_at"`
	ClosesAt       *time.Time `json:"closes_at"`
	MaxSubmissions *int       `json:"max_submissions"`
	Submissions    int        `json:"submissions"`
	Week           int        `json:"submissions_this_week"`
	ClosedMessage  *string    `json:"closed_message"`
	URL            string     `json:"url"`
	EmbedURL       string     `json:"embed_url"`
}

const formCols = `e.id, e.public_id, e.workflow_id, w.name, e.environment, e.version, COALESCE(v.definition->'trigger'->'config'->>'title', ''),
	COALESCE(v.definition->'trigger'->'config'->>'access', 'public'), e.status, e.opens_at, e.closes_at, e.max_submissions, e.submissions,
	(SELECT count(*) FROM form_submissions s WHERE s.endpoint_id = e.id AND s.created_at > now() - interval '7 days')::int, e.closed_message`

const formFrom = ` FROM form_endpoints e JOIN workflows w ON w.id = e.workflow_id JOIN workflow_versions v ON v.workflow_id = e.workflow_id AND v.version = e.version`

func (s *Server) scanForm(r pgx.Row) (formOut, error) {
	var f formOut
	err := r.Scan(&f.ID, &f.PublicID, &f.WorkflowID, &f.Workflow, &f.Environment, &f.Version, &f.Title, &f.Access, &f.Status, &f.OpensAt, &f.ClosesAt,
		&f.MaxSubmissions, &f.Submissions, &f.Week, &f.ClosedMessage)
	switch {
	case err != nil || s.Forms == nil:
	case f.Access == "members":
		// Members fill it in in the app, whatever the form host.
		f.URL = strings.TrimRight(s.Forms.AppURL, "/") + "/forms/" + f.ID.String()
	case s.Forms.FormHost != "":
		f.URL = "https://" + s.Forms.FormHost + "/f/" + f.PublicID
		f.EmbedURL = "https://" + s.Forms.FormHost + "/embed/f/" + f.PublicID
	}
	return f, err
}

func (s *Server) listForms(w http.ResponseWriter, r *http.Request) {
	var list []formOut
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+formCols+formFrom+` ORDER BY w.name, e.environment`)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (formOut, error) { return s.scanForm(row) })
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []formOut{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"forms": list})
}

func (s *Server) getForm(ctx context.Context, tx pgx.Tx, id string) (formOut, error) {
	fid, err := uuid.Parse(id)
	if err != nil {
		return formOut{}, pgx.ErrNoRows
	}
	return s.scanForm(tx.QueryRow(ctx, `SELECT `+formCols+formFrom+` WHERE e.id = $1`, fid))
}

// patchForm pauses, reopens or closes a form, and sets its opening times,
// its limit and its closed message.
func (s *Server) patchForm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status         *string    `json:"status"`
		OpensAt        *time.Time `json:"opens_at"`
		ClosesAt       *time.Time `json:"closes_at"`
		MaxSubmissions *int       `json:"max_submissions"`
		ClosedMessage  *string    `json:"closed_message"`
		Clear          []string   `json:"clear"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Status != nil && !slices.Contains([]string{"live", "paused", "closed"}, *req.Status) {
		writeErr(w, http.StatusBadRequest, "status is live, paused or closed")
		return
	}
	if req.MaxSubmissions != nil && *req.MaxSubmissions < 1 {
		writeErr(w, http.StatusBadRequest, "max_submissions is at least 1")
		return
	}
	if req.ClosedMessage != nil && (len([]rune(*req.ClosedMessage)) > 300 || hasLinkText(*req.ClosedMessage)) {
		writeErr(w, http.StatusBadRequest, "a closed message is up to 300 characters, with no links")
		return
	}
	for _, c := range req.Clear {
		if !slices.Contains([]string{"opens_at", "closes_at", "max_submissions", "closed_message"}, c) {
			writeErr(w, http.StatusBadRequest, "clear names opens_at, closes_at, max_submissions or closed_message")
			return
		}
	}
	var f formOut
	err := s.tx(r, func(tx pgx.Tx) error {
		cur, err := s.getForm(r.Context(), tx, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		if cur.Status == "suspended" {
			return fmt.Errorf("%w: this form was suspended by Taskiem's operators", errConflict)
		}
		clear := func(c string) bool { return slices.Contains(req.Clear, c) }
		if _, err := tx.Exec(r.Context(), `UPDATE form_endpoints SET status = COALESCE($2, status),
			opens_at = CASE WHEN $7 THEN NULL ELSE COALESCE($3, opens_at) END, closes_at = CASE WHEN $8 THEN NULL ELSE COALESCE($4, closes_at) END,
			max_submissions = CASE WHEN $9 THEN NULL ELSE COALESCE($5, max_submissions) END,
			closed_message = CASE WHEN $10 THEN NULL ELSE COALESCE($6, closed_message) END, updated_at = now() WHERE id = $1`,
			cur.ID, req.Status, req.OpensAt, req.ClosesAt, req.MaxSubmissions, req.ClosedMessage,
			clear("opens_at"), clear("closes_at"), clear("max_submissions"), clear("closed_message")); err != nil {
			return err
		}
		action := "form.updated"
		if req.Status != nil && *req.Status != cur.Status {
			action = "form." + map[string]string{"live": "live", "paused": "paused", "closed": "closed"}[*req.Status]
		}
		if err := auditTx(r, tx, action, "form:"+cur.PublicID, nil); err != nil {
			return err
		}
		f, err = s.getForm(r.Context(), tx, cur.ID.String())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func hasLinkText(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "http://") || strings.Contains(l, "https://") || strings.Contains(l, "www.")
}

// mintFormLinks makes one-time links to a link form: a number of open
// ones, or one per person, each bound to their phone or email (the link
// then also asks for the code sent there). The tokens are shown once.
func (s *Server) mintFormLinks(w http.ResponseWriter, r *http.Request) {
	if s.Forms == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Count int `json:"count"`
		To    []struct {
			Phone string `json:"phone"`
			Email string `json:"email"`
		} `json:"to"`
		ExpiresInDays int `json:"expires_in_days"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.ExpiresInDays == 0 {
		req.ExpiresInDays = 7
	}
	if req.ExpiresInDays < 1 || req.ExpiresInDays > 30 {
		writeErr(w, http.StatusBadRequest, "expires_in_days is 1 to 30")
		return
	}
	n := req.Count
	if len(req.To) > 0 {
		n = len(req.To)
	}
	if n < 1 || n > 500 || (req.Count > 0 && len(req.To) > 0) {
		writeErr(w, http.StatusBadRequest, "give count (1 to 500) or to (up to 500 people), not both")
		return
	}
	type link struct {
		ID        uuid.UUID `json:"id"`
		URL       string    `json:"url"`
		To        string    `json:"to,omitempty"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	var out []link
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		f, err := s.getForm(r.Context(), tx, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		if f.Access != "link" {
			return fmt.Errorf("%w: one-time links are for forms with access link", errConflict)
		}
		expires := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		for i := 0; i < n; i++ {
			id := uuid.Must(uuid.NewV7())
			token := forms.NewLinkToken()
			var bound map[string]any
			var view []byte
			hint := ""
			if len(req.To) > 0 {
				t := req.To[i]
				channel, addr := "", ""
				switch {
				case t.Phone != "" && t.Email == "":
					norm, err := sender.NormalisePhone(t.Phone, []string{"234"})
					if err != nil {
						return fmt.Errorf("%w: link %d: %s", errBadRequest, i+1, plainError(err))
					}
					channel, addr, hint = "sms", norm, forms.MaskPhone(norm)
				case t.Email != "" && t.Phone == "" && strings.Contains(t.Email, "@"):
					channel, addr, hint = "email", strings.ToLower(strings.TrimSpace(t.Email)), forms.MaskEmail(strings.ToLower(t.Email))
				default:
					return fmt.Errorf("%w: link %d needs a phone or an email", errBadRequest, i+1)
				}
				env, err := s.Vault.SealTx(r.Context(), tx, p.TenantID, map[string]string{"sms": "phone", "email": "email"}[channel], addr)
				if err != nil {
					return err
				}
				bound = map[string]any{map[string]string{"sms": "phone", "email": "email"}[channel]: env}
				if view, err = s.Forms.Keys.Seal(forms.LinkViewAAD(id.String()), forms.LinkView{Channel: channel, Address: addr, Hint: hint}); err != nil {
					return err
				}
			}
			var b []byte
			if bound != nil {
				b, _ = json.Marshal(bound)
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO form_links (id, tenant_id, endpoint_id, token_hash, bound, bound_view, expires_at, created_by)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, p.TenantID, f.ID, forms.TokenHash(token), b, view, expires, p.Actor()); err != nil {
				return err
			}
			out = append(out, link{ID: id, URL: "https://" + s.Forms.FormHost + "/r/" + token, To: hint, ExpiresAt: expires})
		}
		return auditTx(r, tx, "form.links_minted", "form:"+f.PublicID, map[string]any{"count": n, "bound": len(req.To) > 0})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"links": out})
}

type submissionOut struct {
	ID         uuid.UUID      `json:"id"`
	Reference  string         `json:"reference"`
	FormID     *uuid.UUID     `json:"form_id"`
	RequestID  *uuid.UUID     `json:"request_id"`
	Answers    map[string]any `json:"answers"`
	Respondent map[string]any `json:"respondent"`
	Channel    string         `json:"channel"`
	RunID      *uuid.UUID     `json:"run_id"`
	Status     string         `json:"status"`
	Message    *string        `json:"message"`
	Note       *string        `json:"note"`
	CreatedAt  time.Time      `json:"created_at"`
	Files      []formFileOut  `json:"files,omitempty"`
}

type formFileOut struct {
	ID    uuid.UUID `json:"id"`
	Field string    `json:"field"`
	Name  string    `json:"name"`
	Type  string    `json:"type"`
	Size  int64     `json:"size"`
}

const formSubmissionCols = `id, reference, endpoint_id, request_id, answers, respondent, channel, run_id, status, message, note, created_at`

func scanFormSubmission(r pgx.Row) (submissionOut, error) {
	var x submissionOut
	err := r.Scan(&x.ID, &x.Reference, &x.FormID, &x.RequestID, &x.Answers, &x.Respondent, &x.Channel, &x.RunID, &x.Status, &x.Message, &x.Note, &x.CreatedAt)
	return x, err
}

// maskSealed replaces sealed values with their category, masked.
func maskSealed(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch {
		case pii.IsEnvelope(v):
			cat, _ := v.(map[string]any)["$pii"].(string)
			out[k] = map[string]any{"masked": "•••", "category": cat}
		default:
			if sub, ok := v.(map[string]any); ok {
				out[k] = maskSealed(sub)
			} else {
				out[k] = v
			}
		}
	}
	return out
}

func (s *Server) listFormSubmissions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + formSubmissionCols + ` FROM form_submissions`
	args := []any{}
	if f := r.URL.Query().Get("form"); f != "" {
		id, err := uuid.Parse(f)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "form is a form's id")
			return
		}
		q += ` WHERE endpoint_id = $1`
		args = append(args, id)
	}
	var list []submissionOut
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), q+fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d`, limit), args...)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (submissionOut, error) { return scanFormSubmission(row) })
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range list {
		list[i].Answers, list[i].Respondent = maskSealed(list[i].Answers), maskSealed(list[i].Respondent)
	}
	if list == nil {
		list = []submissionOut{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"submissions": list})
}

// getFormSubmission is one submission with its files. Personal answers
// are masked unless the caller holds pii.reveal and asks with
// ?reveal=true, which is audited.
func (s *Server) getFormSubmission(w http.ResponseWriter, r *http.Request) {
	reveal := r.URL.Query().Get("reveal") == "true"
	p := principalFrom(r.Context())
	if reveal && !p.Can(PermPIIReveal) {
		writeErr(w, http.StatusForbidden, "requires "+PermPIIReveal)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var x submissionOut
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = scanFormSubmission(tx.QueryRow(r.Context(), `SELECT `+formSubmissionCols+` FROM form_submissions WHERE id = $1`, id)); err != nil {
			return err
		}
		rows, err := tx.Query(r.Context(), `SELECT id, field, name, type, size FROM form_files WHERE submission_id = $1 ORDER BY created_at`, id)
		if err != nil {
			return err
		}
		if x.Files, err = pgx.CollectRows(rows, pgx.RowToStructByPos[formFileOut]); err != nil {
			return err
		}
		if !reveal {
			return nil
		}
		for _, m := range []*map[string]any{&x.Answers, &x.Respondent} {
			v, err := pii.Open(r.Context(), s.Vault, tx, p.TenantID, *m, pii.Taint{})
			if err != nil {
				return err
			}
			*m, _ = v.(map[string]any)
		}
		return auditTx(r, tx, "pii.reveal", "form_submission:"+x.Reference, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !reveal {
		x.Answers, x.Respondent = maskSealed(x.Answers), maskSealed(x.Respondent)
	}
	writeJSON(w, http.StatusOK, map[string]any{"submission": x, "revealed": reveal})
}

// formFileURL is a short-lived download link for a form file, with
// pii.reveal, audited.
func (s *Server) formFileURL(w http.ResponseWriter, r *http.Request) {
	if s.Forms == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	out, err := s.Forms.FileURL(r.Context(), p.TenantID, chi.URLParam(r, "id"), 5*time.Minute)
	if errors.Is(err, forms.ErrNoRequest) {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "form.file_download", "form_file:"+chi.URLParam(r, "id"), nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// --- forms members fill in, in the app ---

// memberForm is a members form as the app renders it.
func (s *Server) memberForm(w http.ResponseWriter, r *http.Request) {
	var out map[string]any
	err := s.tx(r, func(tx pgx.Tx) error {
		f, d, err := s.memberFormDef(r.Context(), tx, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		cfg, _ := d.FormTriggerConfig()
		raw, _ := d.InputsSchema()
		member, err := s.memberPrefill(r, tx)
		if err != nil {
			return err
		}
		out = map[string]any{"form": f, "title": cfg.Title, "description": cfg.Description, "schema": raw, "pages": cfg.Pages, "after": cfg.After, "member": member}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) memberFormDef(ctx context.Context, tx pgx.Tx, id string) (formOut, *wd.Definition, error) {
	f, err := s.getForm(ctx, tx, id)
	if err != nil {
		return f, nil, err
	}
	if f.Access != "members" {
		return f, nil, pgx.ErrNoRows
	}
	if f.Status != "live" {
		return f, nil, fmt.Errorf("%w: this form is not taking answers now", errConflict)
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, f.WorkflowID, f.Version).Scan(&raw); err != nil {
		return f, nil, err
	}
	d, err := wd.Load(raw)
	if err != nil {
		return f, nil, err
	}
	cfg, _ := d.FormTriggerConfig()
	p := principalFrom(ctx)
	if len(cfg.Roles) > 0 && !slices.ContainsFunc(cfg.Roles, p.HasRole) {
		return f, nil, pgx.ErrNoRows
	}
	return f, d, nil
}

func (s *Server) memberPrefill(r *http.Request, tx pgx.Tx) (map[string]any, error) {
	var email, name string
	p := principalFrom(r.Context())
	if err := tx.QueryRow(r.Context(), `SELECT email, COALESCE(name, '') FROM users WHERE id = $1`, p.UserID).Scan(&email, &name); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "email": email, "member_id": p.UserID.String()}, nil
}

// lockMemberFields sets the fields filled from the member's account
// (x-form.from member.name or member.email), which they cannot change.
func lockMemberFields(schema map[string]any, answers, member map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	for name, v := range props {
		p, _ := v.(map[string]any)
		h, _ := p["x-form"].(map[string]any)
		switch h["from"] {
		case "member.name":
			answers[name] = member["name"]
		case "member.email":
			answers[name] = member["email"]
		}
	}
}

// submitMemberForm takes a members form's answers and starts its run as
// the member, so approvals, four-eyes and audit apply to them.
func (s *Server) submitMemberForm(w http.ResponseWriter, r *http.Request) {
	if s.Forms == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Answers map[string]any `json:"answers"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Answers == nil {
		req.Answers = map[string]any{}
	}
	p := principalFrom(r.Context())
	var f formOut
	var d *wd.Definition
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if f, d, err = s.memberFormDef(r.Context(), tx, chi.URLParam(r, "id")); err != nil {
			return err
		}
		member, err := s.memberPrefill(r, tx)
		if err != nil {
			return err
		}
		raw, _ := d.InputsSchema()
		schema, _ := raw.(map[string]any)
		lockMemberFields(schema, req.Answers, member)
		body, _ := json.Marshal(req.Answers)
		if msgs := d.ValidateInputs(body); len(msgs) > 0 {
			return fmt.Errorf("%w: %s", errBadRequest, strings.Join(msgs, "; "))
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.processMember(w, r, forms.Incoming{Tenant: p.TenantID, Endpoint: &f.ID, StartedBy: p.Actor(),
		Submission: forms.Submission{Answers: req.Answers, Respondent: forms.Respondent{Kind: "member", MemberID: p.UserID.String()}, Channel: "app", Submitted: time.Now()}})
}

func (s *Server) processMember(w http.ResponseWriter, r *http.Request, in forms.Incoming) {
	in.Reference = forms.NewReference()
	key := make([]byte, 18)
	_, _ = rand.Read(key)
	h := sha256.Sum256([]byte(hex.EncodeToString(key)))
	in.StatusHash = h[:]
	if err := s.Forms.Process(r.Context(), in); err != nil {
		s.fail(w, r, err)
		return
	}
	var x submissionOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = scanFormSubmission(tx.QueryRow(r.Context(), `SELECT `+formSubmissionCols+` FROM form_submissions WHERE reference = $1`, in.Reference))
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	x.Answers, x.Respondent = maskSealed(x.Answers), maskSealed(x.Respondent)
	if x.Status == "refused" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": deref(x.Note), "submission": x})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"submission": x})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type requestOut struct {
	ID          uuid.UUID      `json:"id"`
	RunID       uuid.UUID      `json:"run_id"`
	Title       string         `json:"title"`
	Description *string        `json:"description"`
	Schema      map[string]any `json:"schema"`
	Role        string         `json:"role"`
	Status      string         `json:"status"`
	ExpiresAt   time.Time      `json:"expires_at"`
	CreatedAt   time.Time      `json:"created_at"`
	Prefill     map[string]any `json:"prefill,omitempty"`
}

// listFormRequests are the form steps waiting for members of the
// caller's roles.
func (s *Server) listFormRequests(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var list []requestOut
	err := s.tx(r, func(tx pgx.Tx) error {
		// The caller's roles in the query, so a page of requests for
		// other roles does not hide theirs.
		rows, err := tx.Query(r.Context(), `SELECT id, run_id, title, description, schema, recipient->>'role', status, expires_at, created_at FROM form_requests
			WHERE status = 'pending' AND expires_at > now() AND recipient ? 'role' AND ($1 OR recipient->>'role' = ANY($2))
			ORDER BY created_at DESC LIMIT 100`, p.HasRole("owner"), p.Roles)
		if err != nil {
			return err
		}
		all, err := pgx.CollectRows(rows, pgx.RowToStructByPos[requestOutRow])
		for _, x := range all {
			list = append(list, x.out())
		}
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []requestOut{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": list})
}

type requestOutRow struct {
	ID          uuid.UUID
	RunID       uuid.UUID
	Title       string
	Description *string
	Schema      map[string]any
	Role        *string
	Status      string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

func (x requestOutRow) out() requestOut {
	role := ""
	if x.Role != nil {
		role = *x.Role
	}
	return requestOut{ID: x.ID, RunID: x.RunID, Title: x.Title, Description: x.Description, Schema: x.Schema, Role: role, Status: x.Status,
		ExpiresAt: x.ExpiresAt, CreatedAt: x.CreatedAt}
}

func (s *Server) memberRequest(ctx context.Context, tx pgx.Tx, id string) (requestOut, error) {
	rid, err := uuid.Parse(id)
	if err != nil {
		return requestOut{}, pgx.ErrNoRows
	}
	var x requestOutRow
	var prefill any
	if err := tx.QueryRow(ctx, `SELECT id, run_id, title, description, schema, recipient->>'role', status, expires_at, created_at FROM form_requests WHERE id = $1`, rid).
		Scan(&x.ID, &x.RunID, &x.Title, &x.Description, &x.Schema, &x.Role, &x.Status, &x.ExpiresAt, &x.CreatedAt); err != nil {
		return requestOut{}, err
	}
	if x.Role == nil || !principalFrom(ctx).HasRole(*x.Role) {
		return requestOut{}, pgx.ErrNoRows
	}
	out := x.out()
	if err := tx.QueryRow(ctx, `SELECT prefill FROM form_requests WHERE id = $1`, rid).Scan(&prefill); err != nil {
		return out, err
	}
	if prefill != nil {
		v, err := pii.Open(ctx, s.Vault, tx, principalFrom(ctx).TenantID, prefill, pii.Taint{})
		if err != nil {
			return out, err
		}
		out.Prefill, _ = v.(map[string]any)
	}
	return out, nil
}

func (s *Server) getFormRequest(w http.ResponseWriter, r *http.Request) {
	var out requestOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = s.memberRequest(r.Context(), tx, chi.URLParam(r, "id"))
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// answerFormRequest answers a form step addressed to the member's role.
func (s *Server) answerFormRequest(w http.ResponseWriter, r *http.Request) {
	if s.Forms == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Answers map[string]any `json:"answers"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x requestOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.memberRequest(r.Context(), tx, chi.URLParam(r, "id"))
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if x.Status != "pending" || time.Now().After(x.ExpiresAt) {
		writeErr(w, http.StatusConflict, "this request is no longer waiting for an answer")
		return
	}
	if req.Answers == nil {
		req.Answers = map[string]any{}
	}
	s.processMember(w, r, forms.Incoming{Tenant: p.TenantID, Request: &x.ID,
		Submission: forms.Submission{Answers: req.Answers, Respondent: forms.Respondent{Kind: "member", MemberID: p.UserID.String()}, Channel: "app", Submitted: time.Now()}})
}

// RunForms takes form submissions from the inbox every second, and every
// 10 seconds sends form steps' links, reminders and codes.
func (s *Server) RunForms(ctx context.Context) error {
	if s.Forms == nil {
		<-ctx.Done()
		return nil
	}
	drain, tick := time.NewTicker(time.Second), time.NewTicker(10*time.Second)
	defer drain.Stop()
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-drain.C:
			if err := s.Forms.Drain(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("forms: inbox", "err", err)
			}
		case <-tick.C:
			if err := s.Forms.Tick(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("forms: links and codes", "err", err)
			}
		}
	}
}
