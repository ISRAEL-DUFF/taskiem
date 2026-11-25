package api

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Follow-ups of forms left half-filled on WhatsApp
// (docs/spec/forms-that-sell.md §6.4, L4): the approved template from the
// business's own number, never the shared number and never a free-form
// message; STOP, typed or the template's button, stops them; and a send
// WhatsApp later reports failed goes by text.

// WAFollowups reaches a buyer's typed phone by the follow-up's template
// from the business's own number (forms.FollowupChat).
func WAFollowups(wa *whatsapp.Platform, t whatsapp.Template) forms.FollowupChat {
	return waFollowups{wa: wa, tpl: t}
}

type waFollowups struct {
	wa  *whatsapp.Platform
	tpl whatsapp.Template
}

func (f waFollowups) OptIn() bool { return f.tpl.OptIn() }

// FollowUp sends the template; sent false when the business has no own
// number, the template is not approved in its account or is held back
// past its allowance, or WhatsApp refuses it: the text goes instead.
func (f waFollowups) FollowUp(ctx context.Context, _ pgx.Tx, m forms.FollowupMessage) (string, bool, error) {
	wa, err := f.wa.ForTenant(ctx, m.Tenant)
	if err != nil {
		// The own number's credentials do not read: the text goes, rather
		// than the follow-up waiting on a number that cannot send.
		if f.wa.Logger != nil {
			f.wa.Logger.Warn("forms: a follow-up's own WhatsApp number", "tenant", m.Tenant, "err", err)
		}
		return "", false, nil
	}
	if !wa.Own() || m.Token == "" {
		return "", false, nil
	}
	what := m.Product
	if m.Detail != "" {
		what += " (" + m.Detail + ")"
	}
	link := wa.Client.Hosts.Of(whatsapp.HostForms) + whatsapp.FollowupLinkPath + m.Token
	id, err := wa.SendTemplate(ctx, m.Tenant, m.To, f.tpl, map[string]string{"business": m.Business, "what": what, "link": link},
		[]string{whatsapp.FollowupStopPayload})
	var ge *whatsapp.GraphError
	switch {
	case err == nil:
		return id, true, nil
	case whatsapp.TemplateUnusable(err), errors.Is(err, whatsapp.ErrTemplateBlocked), errors.As(err, &ge):
		return "", false, nil
	}
	return "", false, err
}

// followupStatuses passes WhatsApp's word that a message from an own
// number failed to the follow-ups, which text the buyer instead when it
// was one.
func (s *Server) followupStatuses(ctx context.Context, hook *whatsapp.Platform, sts []whatsapp.StatusUpdate) {
	if s.Forms == nil || s.FollowupTemplate == nil {
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
		if err := s.Forms.FollowupFailed(ctx, wa.Tenant, u.ID); err != nil {
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
