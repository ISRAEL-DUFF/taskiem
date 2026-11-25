package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Follow-ups of forms left half-filled go by text, email or WhatsApp
// (docs/spec/forms-that-sell.md §6.4, L4). On WhatsApp: the built-in
// template (whatsapp.TplFinishOrder) from the business's own number,
// never the shared number and never a free-form message, where Meta holds
// it approved in the business's account with our words and the owner
// lets follow-ups go there; nothing in the deployment's environment
// switches it (the founder, 25 November 2026). STOP, typed or the
// template's button, stops them; and a send WhatsApp later reports failed
// goes by text.

// WAFollowups reaches a buyer's typed phone by the follow-up's template
// from the business's own number (forms.FollowupChat).
func WAFollowups(wa *whatsapp.Platform) forms.FollowupChat {
	return waFollowups{wa: wa}
}

type waFollowups struct {
	wa *whatsapp.Platform
}

// followupWA is how a business's follow-ups may go on WhatsApp: the
// category Meta gave the follow-up's template in its active own number's
// account, where it is approved with our words in a category the form can
// ask consent for (utility or marketing; "" otherwise), and the owner's
// choice to let follow-ups go there (on unless turned off). The public
// host asks the same of taskiem_public_form_followup_whatsapp.
func followupWA(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, language string) (category string, on bool, err error) {
	var c *string
	err = tx.QueryRow(ctx, `SELECT (SELECT lower(w.meta_category) FROM whatsapp_numbers n JOIN whatsapp_templates w ON w.waba_id = n.waba_id AND w.tenant_id = n.tenant_id
		  WHERE n.tenant_id = $1 AND n.status = 'active' AND w.name = $2 AND w.language = $3 AND w.status = 'approved' AND w.meta_matches
		    AND lower(w.meta_category) IN ('utility', 'marketing') LIMIT 1),
		COALESCE((SELECT followup_whatsapp FROM storefronts WHERE tenant_id = $1), true)`,
		tenant, whatsapp.TplFinishOrder.Name, language).Scan(&c, &on)
	if c != nil {
		category = *c
	}
	return category, on, err
}

// own is the business's own number, or nil when it has none or its
// credentials do not read (the text goes, rather than the follow-up
// waiting on a number that cannot send).
func (f waFollowups) own(ctx context.Context, tenant uuid.UUID) *whatsapp.Platform {
	wa, err := f.wa.ForTenant(ctx, tenant)
	if err != nil {
		if f.wa.Logger != nil {
			f.wa.Logger.Warn("forms: a follow-up's own WhatsApp number", "tenant", tenant, "err", err)
		}
		return nil
	}
	if !wa.Own() {
		return nil
	}
	return wa
}

// Ready: the category Meta gave the template in the business's own
// number's account (whatsapp.Platform.TemplateCategory), where it is
// approved with our body and buttons, the number's credentials read and
// the owner lets follow-ups go there; "" otherwise.
func (f waFollowups) Ready(ctx context.Context, tenant uuid.UUID) (string, error) {
	wa := f.own(ctx, tenant)
	if wa == nil {
		return "", nil
	}
	category, err := wa.TemplateCategory(ctx, whatsapp.TplFinishOrder)
	if err != nil || category == "" {
		return "", err
	}
	var on bool
	if err := db.InTenantTx(ctx, wa.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		_, on, err = followupWA(ctx, tx, tenant, wa.Config.Language)
		return err
	}); err != nil || !on {
		return "", err
	}
	return category, nil
}

// Send sends the template once. Whatever stops it before it leaves (no
// own number, the template no longer ready or filed by Meta where the
// buyer's consent no longer covers it, the owner's choice turned off,
// held back past the business's allowance, a link its button cannot
// open) and WhatsApp's refusal are forms.ErrChatNotSent: the text goes
// instead. Anything else (a timeout, a server error) leaves it unclear
// whether WhatsApp took it.
func (f waFollowups) Send(ctx context.Context, m forms.FollowupMessage) (string, error) {
	wa := f.own(ctx, m.Tenant)
	if wa == nil || m.Token == "" {
		return "", forms.ErrChatNotSent
	}
	category, err := f.Ready(ctx, m.Tenant)
	switch {
	case err != nil:
		return "", fmt.Errorf("%w: %w", forms.ErrChatNotSent, err)
	case !forms.ChatConsented(category, m.Asked):
		return "", fmt.Errorf("%w: the template is not ready in the account for this buyer", forms.ErrChatNotSent)
	}
	// Counted, and held back past the allowance, in Meta's category.
	tpl := whatsapp.TplFinishOrder
	tpl.Category = strings.ToUpper(category)
	what := m.Product
	if m.Detail != "" {
		what += " (" + m.Detail + ")"
	}
	vars := map[string]string{"business": m.Business, "what": what, "link": wa.Client.Hosts.Of(whatsapp.HostForms) + whatsapp.FollowupLinkPath + m.Token}
	payloads := []string{whatsapp.FollowupStopPayload}
	lang := wa.Config.Language
	if _, err := tpl.Payload(lang, vars, payloads, wa.Client.Hosts); err != nil {
		return "", fmt.Errorf("%w: %w", forms.ErrChatNotSent, err)
	}
	if wa.Meter != nil {
		switch ok, err := wa.Meter.Allow(ctx, m.Tenant, tpl); {
		case err != nil:
			return "", fmt.Errorf("%w: %w", forms.ErrChatNotSent, err)
		case !ok:
			return "", fmt.Errorf("%w: %w", forms.ErrChatNotSent, whatsapp.ErrTemplateBlocked)
		}
	}
	id, err := wa.Client.SendTemplate(ctx, m.To, tpl, lang, vars, payloads)
	var ge *whatsapp.GraphError
	switch {
	case err == nil:
		if wa.Meter != nil {
			if merr := wa.Meter.Record(ctx, m.Tenant, tpl); merr != nil && f.wa.Logger != nil {
				f.wa.Logger.Error("whatsapp: counting a template", "template", tpl.Name, "err", merr)
			}
		}
		return id, nil
	case errors.As(err, &ge) && ge.Status < 500:
		// WhatsApp answered no (the template paused or not approved, the
		// number refused): nothing went.
		return "", fmt.Errorf("%w: %w", forms.ErrChatNotSent, err)
	}
	return "", err
}

// followupStatuses passes WhatsApp's word that a message from an own
// number failed to the follow-ups, which text the buyer instead when it
// was one (or record a stop, for 131050).
func (s *Server) followupStatuses(ctx context.Context, hook *whatsapp.Platform, sts []whatsapp.StatusUpdate) {
	if s.Forms == nil {
		return
	}
	for _, u := range sts {
		if u.Status != "failed" {
			continue
		}
		wa := hook
		if u.PhoneNumberID != hook.Config.PhoneNumberID {
			if hook.Own() {
				continue
			}
			var err error
			if wa, err = s.WhatsApp.ForPhoneNumberID(ctx, u.PhoneNumberID); err != nil || wa == nil || wa.OwnApp {
				continue
			}
		}
		if !wa.Own() {
			continue
		}
		if err := s.Forms.FollowupFailed(ctx, wa.Tenant, u.ID, u.Code); err != nil {
			s.Logger.Warn("forms: a WhatsApp follow-up that failed, not texted", "err", err)
		}
	}
}

// followupStop takes the follow-up template's Stop button, and STOP typed
// to a business's own number: every follow-up from that business to that
// number stops. The button is answered here; a typed STOP is answered
// as the follow-ups' stop when the number has no thread with the business
// (whose STOP answers otherwise) and the business had followed it up in
// the last 7 days: had says so. Neither touches the reach book's updates,
// which have their own STOP.
func (s *Server) followupStop(ctx context.Context, wa *whatsapp.Platform, number string, in whatsapp.Inbound) (button, had bool, err error) {
	if s.Forms == nil || !wa.Own() {
		return false, false, nil
	}
	button = in.Reply == whatsapp.FollowupStopPayload
	word := strings.Trim(strings.ToLower(strings.TrimSpace(in.Text)), "/ .!")
	if !button && (in.Type != "text" || word != "stop" && word != "unsubscribe") {
		return false, false, nil
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{wa.Tenant}, func(tx pgx.Tx) error {
		var err error
		had, err = s.Forms.StopFollowups(ctx, tx, wa.Tenant, number)
		return err
	})
	if err != nil || !button {
		return button, had, err
	}
	return true, had, s.followupStopped(ctx, wa, number)
}

// followupStopped tells the number its follow-ups stopped.
func (s *Server) followupStopped(ctx context.Context, wa *whatsapp.Platform, number string) error {
	var name string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{wa.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, wa.Tenant).Scan(&name)
	}); err != nil {
		return err
	}
	return s.waSay(ctx, wa, number, "", tr(s.chooseLang(ctx, wa.Tenant, number, "").Tag, "form.followup.stopped", "business", name))
}
