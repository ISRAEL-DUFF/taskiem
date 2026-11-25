package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Forms left half-filled, the owner's side (docs/spec/forms-that-sell.md
// §6.6, §7, §10): an order form's follow-up settings (on or off, the
// wait, a hold released), the week's counts (left without ordering,
// followed up, came back) and the list, each row the product, the
// package, when, the channel and the contact masked; the chat alert; and
// the chat words follow-ups on and follow-ups off. Nothing of a record
// reaches the AI builder or a page's or tool's reads: only these narrow
// functions read it.

// followupSettings are an order form's follow-up settings.
type followupSettings struct {
	On          bool       `json:"on"`
	WaitMinutes int        `json:"wait_minutes"`
	HeldAt      *time.Time `json:"held_at,omitempty"`
	// WhatsApp: a follow-up may go on WhatsApp, by the deployment's
	// template from the business's own number (L4); WhatsAppOptIn: only to
	// a buyer who asks for it on the form (a marketing template).
	WhatsApp      bool `json:"whatsapp,omitempty"`
	WhatsAppOptIn bool `json:"whatsapp_opt_in,omitempty"`
}

// followupChannels says whether the business's follow-ups may go on
// WhatsApp: the deployment names the template and the business has its
// own number.
func (s *Server) followupChannels(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, st *followupSettings) error {
	if s.FollowupTemplate == nil || s.Forms == nil {
		return nil
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM whatsapp_numbers WHERE tenant_id = $1 AND status = 'active')`, tenant).Scan(&st.WhatsApp); err != nil {
		return err
	}
	st.WhatsAppOptIn = st.WhatsApp && s.FollowupTemplate.OptIn()
	return nil
}

// leftView is GET …/left.
type leftView struct {
	Action    uuid.UUID         `json:"action_id"`
	Label     string            `json:"label"`
	Followups followupSettings  `json:"followups"`
	Week      forms.LeftSummary `json:"week"`
	Left      []forms.LeftRow   `json:"left"`
}

// orderAction reads an order action of the caller's business.
func orderAction(ctx context.Context, tx pgx.Tx, id uuid.UUID) (label string, form []byte, st followupSettings, err error) {
	err = tx.QueryRow(ctx, `SELECT label, form, followup_on, followup_wait, followup_held_at FROM storefront_actions WHERE id = $1 AND kind = 'order'`, id).
		Scan(&label, &form, &st.On, &st.WaitMinutes, &st.HeldAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = fmt.Errorf("%w: no such order form", runtime.ErrNotFound)
	}
	return label, form, st, err
}

func (s *Server) leftOf(ctx context.Context, tx pgx.Tx, tenant, action uuid.UUID) (leftView, error) {
	label, form, st, err := orderAction(ctx, tx, action)
	if err != nil {
		return leftView{}, err
	}
	out := leftView{Action: action, Label: label, Followups: st}
	if err := s.followupChannels(ctx, tx, tenant, &out.Followups); err != nil {
		return leftView{}, err
	}
	week := time.Now().UTC().AddDate(0, 0, -6)
	if out.Week, out.Left, err = forms.Left(ctx, tx, tenant, action, week); err != nil {
		return leftView{}, err
	}
	// The product each row was buying, from the page's list.
	item := storefront.OneItem(storefront.EffectiveForm(form, "order"))
	names := map[string]string{}
	name := func(row string) string {
		if n, ok := names[row]; ok {
			return n
		}
		var n string
		_ = tx.QueryRow(ctx, `SELECT COALESCE(c.item->>'name', '') FROM storefront_catalogue c JOIN storefronts s ON s.id = c.storefront_id
			WHERE s.tenant_id = $1 AND c.row_id = $2 LIMIT 1`, tenant, row).Scan(&n)
		names[row] = n
		return n
	}
	for i := range out.Left {
		if item != "" {
			out.Left[i].Product = name(item)
		}
	}
	if out.Left == nil {
		out.Left = []forms.LeftRow{}
	}
	return out, nil
}

// getActionLeft is GET /v1/storefront/actions/{id}/left.
func (s *Server) getActionLeft(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: no such order form", runtime.ErrNotFound))
		return
	}
	p := principalFrom(r.Context())
	var out leftView
	if err := s.tx(r, func(tx pgx.Tx) error {
		out, err = s.leftOf(r.Context(), tx, p.TenantID, id)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// getFormLeft is GET /v1/forms/{id}/left: a hosted form that sells is an
// order form of the storefront, and its records are that form's.
func (s *Server) getFormLeft(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: no such form", runtime.ErrNotFound))
		return
	}
	p := principalFrom(r.Context())
	var out leftView
	if err := s.tx(r, func(tx pgx.Tx) error {
		var action *uuid.UUID
		err := tx.QueryRow(r.Context(), `SELECT action_id FROM form_endpoints WHERE id = $1`, id).Scan(&action)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no such form", runtime.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if action == nil {
			return fmt.Errorf("%w: this form sells nothing, so it follows up no one; Sell something with this form first", errConflict)
		}
		out, err = s.leftOf(r.Context(), tx, p.TenantID, *action)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type patchActionIn struct {
	FollowupOn   *bool `json:"followup_on"`
	FollowupWait *int  `json:"followup_wait"`
	// ReleaseHold sends the follow-ups held after a run of keeps.
	ReleaseHold bool `json:"release_hold"`
}

// patchStorefrontAction is PATCH /v1/storefront/actions/{id}: an order
// form's follow-up settings. Audited as storefront.followups.
func (s *Server) patchStorefrontAction(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: no such order form", runtime.ErrNotFound))
		return
	}
	var in patchActionIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.FollowupWait != nil && (*in.FollowupWait < 10 || *in.FollowupWait > 1440) {
		s.fail(w, r, fmt.Errorf("%w: the wait is 10 minutes to 24 hours (1440 minutes)", errBadRequest))
		return
	}
	var out followupSettings
	if err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if _, _, _, err := orderAction(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_actions SET followup_on = COALESCE($2, followup_on), followup_wait = COALESCE($3, followup_wait),
			followup_held_at = CASE WHEN $4 OR NOT COALESCE($2, followup_on) THEN NULL ELSE followup_held_at END,
			followup_released_at = CASE WHEN $4 AND followup_held_at IS NOT NULL THEN now() ELSE followup_released_at END WHERE id = $1`,
			id, in.FollowupOn, in.FollowupWait, in.ReleaseHold); err != nil {
			return err
		}
		var err error
		if _, _, out, err = orderAction(ctx, tx, id); err != nil {
			return err
		}
		if err := s.followupChannels(ctx, tx, principalFrom(ctx).TenantID, &out); err != nil {
			return err
		}
		return auditTx(r, tx, "storefront.followups", id.String(), map[string]any{"on": out.On, "wait_minutes": out.WaitMinutes, "released": in.ReleaseHold})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	// The public host draws the consent line within its page cache's
	// minute.
	writeJSON(w, http.StatusOK, map[string]any{"followups": out})
}

// leftAlerts are the chat's alerts of forms left half-filled today, and
// of a form whose follow-ups are held (§6.5, §6.6).
func (s *Server) leftAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermStorefrontManage) {
		return nil, nil
	}
	var out []chatAlert
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT t.scope_id, t.left_n, t.followed, a.label, a.followup_held_at FROM taskiem_form_left_today($1) t
			JOIN storefront_actions a ON a.id = t.scope_id`, p.TenantID)
		if err != nil {
			return err
		}
		type today struct {
			id           uuid.UUID
			left, follow int
			label        string
			held         *time.Time
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (today, error) {
			var x today
			return x, r.Scan(&x.id, &x.left, &x.follow, &x.label, &x.held)
		})
		if err != nil {
			return err
		}
		seen := map[uuid.UUID]bool{}
		for _, x := range list {
			seen[x.id] = true
			out = append(out, leftAlert(x.id, x.label, x.left, x.follow, x.held))
		}
		// A form held with nobody left today yet is said too.
		held, err := tx.Query(ctx, `SELECT id, label, followup_held_at FROM storefront_actions WHERE followup_held_at IS NOT NULL AND followup_on`)
		if err != nil {
			return err
		}
		more, err := pgx.CollectRows(held, func(r pgx.CollectableRow) (chatAlert, error) {
			var id uuid.UUID
			var label string
			var at time.Time
			if err := r.Scan(&id, &label, &at); err != nil {
				return chatAlert{}, err
			}
			return leftAlert(id, label, 0, 0, &at), nil
		})
		for _, a := range more {
			if id, _ := uuid.Parse(a.ID); !seen[id] {
				out = append(out, a)
			}
		}
		return err
	})
	return out, err
}

// leftAlert is one form's card: "3 people left your order form today; 2
// were followed up", or its follow-ups held.
func leftAlert(id uuid.UUID, label string, left, followed int, held *time.Time) chatAlert {
	people := strconv.Itoa(left) + " people"
	if left == 1 {
		people = "1 person"
	}
	were := strconv.Itoa(followed) + " were followed up"
	if followed == 1 {
		were = "1 was followed up"
	}
	day := time.Now().UTC().Format("2006-01-02")
	a := chatAlert{Key: "left:" + id.String() + ":" + day, Kind: "left", At: time.Now(), ID: id.String(),
		Title: people + " left your order form today", Line: were + " · " + strings.TrimSpace(label)}
	if held != nil {
		a.Key, a.At = "left_held:"+id.String()+":"+held.UTC().Format(time.RFC3339), *held
		a.Title = "Follow-ups held on " + strings.TrimSpace(label)
		a.Line = "Many people left it at once, so no more messages go out until you release them."
	}
	return a
}
