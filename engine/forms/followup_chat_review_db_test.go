package forms_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
)

// The review of forms that sell L4 (docs/spec/forms-that-sell.md §19,
// Review fixes).

// scriptChat is a FollowupChat whose Send does what a test says.
type scriptChat struct {
	optIn bool
	ready bool
	sends int
	send  func(m forms.FollowupMessage) (string, error)
}

func (c *scriptChat) Ready(context.Context, uuid.UUID) (string, error) {
	return category(c.ready, c.optIn), nil
}

func (c *scriptChat) Send(_ context.Context, m forms.FollowupMessage) (string, error) {
	c.sends++
	return c.send(m)
}

// timeoutChat is the reviewer's: WhatsApp's answer never comes, so it is
// unclear whether the template went.
type timeoutChat struct{ sends int }

func (c *timeoutChat) Ready(context.Context, uuid.UUID) (string, error) { return "utility", nil }
func (c *timeoutChat) Send(context.Context, forms.FollowupMessage) (string, error) {
	c.sends++
	return "", errors.New("Post graph.facebook.com: context deadline exceeded")
}

// waNamed is a keep from a form that named WhatsApp.
func waNamed(contact string) forms.Keep {
	return forms.Keep{Contact: contact, Choice: forms.Choice{WhatsAppNamed: true}}
}

// waState is the record of a phone: its channel, outcome and WhatsApp
// state.
func (w *leftWorld) waState(contact string) (channel, outcome, state string) {
	w.t.Helper()
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT COALESCE(channel, ''), COALESCE(outcome, ''), COALESCE(wa_state, '') FROM form_left WHERE contact_hash = $1`,
		w.svc.ContactHash("phone", normal(w.t, contact))).Scan(&channel, &outcome, &state); err != nil {
		w.t.Fatal(err)
	}
	return channel, outcome, state
}

// Review of L4 (finding 1, the reviewer's test): a send whose answer is
// unclear (a timeout after Meta may have taken it) rolled everything
// back, and every pass sent the template again. It is claimed before it
// goes and never sent twice.
func TestReviewL4TemplateResentAfterAmbiguousError(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	chat := &timeoutChat{}
	w.svc.FollowupChat = chat
	if !w.keep(waNamed("0803 111 2222"), page(90)) {
		t.Fatal("keep refused")
	}
	if err := w.svc.DrainKeeps(ctx); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.past()
		_ = w.svc.FollowUps(ctx)
	}
	if chat.sends > 1 {
		t.Fatalf("the same buyer was sent the WhatsApp template %d times", chat.sends)
	}
	// Taken as sent: on WhatsApp, followed up, no text.
	if ch, out, st := w.waState("08031112222"); ch != "whatsapp" || out != "followed" || st != "sent" {
		t.Fatalf("the record: %s %s %s", ch, out, st)
	}
	if _, n := w.sent("sms"); n != 0 {
		t.Fatal("texted after an unclear send")
	}
}

// Review of L4 (finding 1): the business's lock is not held while
// WhatsApp is asked; WhatsApp clearly refusing goes by text, once; a
// crash after the claim (a record left sending) is never sent again; a
// text the outbox then refuses gives the follow-up back.
func TestReviewL4ClaimedBeforeSending(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	var p byte = 100
	leave := func(contact string) {
		t.Helper()
		p++
		if !w.keep(waNamed(contact), page(p)) {
			t.Fatal("keep refused")
		}
		w.drain()
		w.past()
		w.followUps()
	}

	// While WhatsApp is asked, another replica may take the business's
	// lock: the caps were spent and the record claimed, committed.
	var lockFree bool
	var claimed string
	chat := &scriptChat{ready: true, send: func(m forms.FollowupMessage) (string, error) {
		tx, err := w.env.DB.Admin.Begin(ctx)
		if err != nil {
			return "", err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1::text, 901))`, w.env.Tenant.String()).Scan(&lockFree); err != nil {
			return "", err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(wa_state, '') FROM form_left WHERE contact_hash = $1`, w.svc.ContactHash("phone", m.To)).Scan(&claimed); err != nil {
			return "", err
		}
		return "wamid.lock", nil
	}}
	w.svc.FollowupChat = chat
	leave("08031110001")
	if !lockFree || claimed != "sending" {
		t.Fatalf("while WhatsApp was asked: the lock free %v, the record %q", lockFree, claimed)
	}
	if ch, out, st := w.waState("08031110001"); ch != "whatsapp" || out != "followed" || st != "sent" {
		t.Fatalf("sent: %s %s %s", ch, out, st)
	}

	// WhatsApp clearly refused it: a text, once, with the link made.
	chat.send = func(forms.FollowupMessage) (string, error) {
		return "", fmt.Errorf("%w: whatsapp 400 (code 132001): template paused", forms.ErrChatNotSent)
	}
	before := chat.sends
	leave("08031110002")
	sms, n := w.sent("sms")
	if chat.sends != before+1 || n != 1 || !strings.Contains(sms, "https://ada-kitchen.shops.test/back/") {
		t.Fatalf("the text instead: %d sends, %d texts %q", chat.sends-before, n, sms)
	}
	if ch, out, st := w.waState("08031110002"); ch != "sms" || out != "followed" || st != "texted" {
		t.Fatalf("texted: %s %s %s", ch, out, st)
	}
	for range 2 {
		w.past()
		w.followUps()
	}
	if _, n := w.sent("sms"); n != 1 || chat.sends != before+1 {
		t.Fatal("sent again")
	}

	// A record left sending (the process died while WhatsApp was asked)
	// is never claimed again.
	chat.send = func(forms.FollowupMessage) (string, error) { return "wamid.x", nil }
	w.admin(`UPDATE form_left SET wa_state = 'sending' WHERE contact_hash = $1`, w.svc.ContactHash("phone", "+2348031110001"))
	before = chat.sends
	for range 2 {
		w.past()
		w.followUps()
	}
	if chat.sends != before {
		t.Fatal("a record left sending was sent again")
	}

	// WhatsApp refused it and the outbox refuses the text (the person's
	// limits): not followed up after all, and nothing of the person's
	// allowance across businesses spent.
	chat.send = func(forms.FollowupMessage) (string, error) { return "", forms.ErrChatNotSent }
	w.in(func(tx pgx.Tx) error {
		for range 5 {
			if _, err := w.svc.Messages.TakeRecipient(ctx, tx, "sms", "+2348031110003"); err != nil {
				return err
			}
		}
		return nil
	})
	followedBefore := w.count(`SELECT COALESCE(sum(followed), 0)::int FROM form_left_stats WHERE tenant_id = $1`, w.env.Tenant)
	leave("08031110003")
	if ch, out, st := w.waState("08031110003"); ch != "" || out != "capped" || st != "" {
		t.Fatalf("the text refused: %s %s %s", ch, out, st)
	}
	hash := w.svc.ContactHash("phone", "+2348031110003")
	if w.count(`SELECT count(*) FROM form_followup_contacts WHERE contact_hash = $1`, hash) != 0 ||
		w.count(`SELECT count(*) FROM form_restores r JOIN form_left l ON l.id = r.left_id WHERE l.contact_hash = $1`, hash) != 0 ||
		w.count(`SELECT COALESCE(sum(followed), 0)::int FROM form_left_stats WHERE tenant_id = $1`, w.env.Tenant) != followedBefore {
		t.Fatal("the refused follow-up was counted")
	}
}

// Review of L4 (finding 3): a failure WhatsApp reports before the
// message's id is written (its status crossed the send's answer) is
// texted once the id is.
func TestReviewL4FailedBeforeTheIDIsWritten(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	chat := &scriptChat{ready: true, send: func(forms.FollowupMessage) (string, error) {
		// WhatsApp's failed status arrives first.
		if err := w.svc.FollowupFailed(ctx, w.env.Tenant, "wamid.early", 131026); err != nil {
			return "", err
		}
		return "wamid.early", nil
	}}
	w.svc.FollowupChat = chat
	if !w.keep(waNamed("08031112222"), page(91)) {
		t.Fatal("keep refused")
	}
	w.drain()
	w.past()
	w.followUps()
	if ch, _, st := w.waState("08031112222"); ch != "sms" || st != "texted" {
		t.Fatalf("the early failure: %s %s", ch, st)
	}
	if _, n := w.sent("sms"); n != 1 {
		t.Fatalf("texts: %d", n)
	}
	if w.count(`SELECT count(*) FROM form_left_wa_early`) != 0 {
		t.Fatal("the early failure was kept")
	}
	// A failure for a message that is no follow-up, with none sending,
	// is not remembered.
	if err := w.svc.FollowupFailed(ctx, w.env.Tenant, "wamid.chat", 131026); err != nil {
		t.Fatal(err)
	}
	if w.count(`SELECT count(*) FROM form_left_wa_early`) != 0 {
		t.Fatal("a stranger's failure was kept")
	}
}

// Review of L4 (finding 3): WhatsApp's failures by kind. 131050 (the
// person stopped the business's marketing) is a stop, not a text; 131049
// (Meta's marketing limit) is neither texted nor tried again; and any
// other failure is not texted to a contact who stopped, on a form no
// longer following up, or for a page whose order came in since.
func TestReviewL4FailuresByKind(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	var seq int
	chat := &scriptChat{ready: true, send: func(forms.FollowupMessage) (string, error) {
		seq++
		return fmt.Sprintf("wamid.k%d", seq), nil
	}}
	w.svc.FollowupChat = chat
	var p byte = 120
	leave := func(contact string) string {
		t.Helper()
		p++
		if !w.keep(waNamed(contact), page(p)) {
			t.Fatal("keep refused")
		}
		w.drain()
		w.past()
		w.followUps()
		return fmt.Sprintf("wamid.k%d", seq)
	}
	fail := func(ref string, code int) {
		t.Helper()
		if err := w.svc.FollowupFailed(ctx, w.env.Tenant, ref, code); err != nil {
			t.Fatal(err)
		}
	}

	ref := leave("08031120001")
	fail(ref, forms.WAStoppedMarketing)
	if _, n := w.sent("sms"); n != 0 {
		t.Fatal("131050 texted")
	}
	if ch, out, st := w.waState("08031120001"); ch != "whatsapp" || out != "followed" || st != "stopped" {
		t.Fatalf("131050: %s %s %s", ch, out, st)
	}
	if w.count(`SELECT count(*) FROM form_followup_stops WHERE tenant_id = $1 AND contact_hash = $2`, w.env.Tenant, w.svc.ContactHash("phone", "+2348031120001")) != 1 {
		t.Fatal("131050 is no stop")
	}

	ref = leave("08031120002")
	fail(ref, forms.WAMarketingLimit)
	fail(ref, 131026)
	if _, n := w.sent("sms"); n != 0 {
		t.Fatal("131049 texted")
	}
	if ch, out, st := w.waState("08031120002"); ch != "whatsapp" || out != "followed" || st != "limited" {
		t.Fatalf("131049: %s %s %s", ch, out, st)
	}

	// Stopped since (the link's Stop): not texted.
	ref = leave("08031120003")
	w.in(func(tx pgx.Tx) error {
		_, err := w.svc.StopFollowups(ctx, tx, w.env.Tenant, "+2348031120003")
		return err
	})
	fail(ref, 131026)
	// The page's order came in since: not texted.
	ref2 := leave("08031120004")
	var sf uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT storefront_id FROM storefront_actions WHERE id = $1`, w.action).Scan(&sf); err != nil {
		t.Fatal(err)
	}
	w.admin(`INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload, action_id)
		VALUES (gen_random_uuid(), $1, $2, 'order', 'AB-CDEFGJ', $3, $4, '\x00', $5)`, sf, w.env.Tenant, page(200), page(p), w.action)
	fail(ref2, 131026)
	// Follow-ups switched off since: not texted.
	ref3 := leave("08031120005")
	w.admin(`UPDATE storefront_actions SET followup_on = false WHERE id = $1`, w.action)
	fail(ref3, 131026)
	if _, n := w.sent("sms"); n != 0 {
		t.Fatal("texted after a stop, an order or follow-ups off")
	}
	for _, c := range []string{"08031120003", "08031120004", "08031120005"} {
		if ch, _, st := w.waState(c); ch != "whatsapp" || st != "failed" {
			t.Fatalf("%s: %s %s", c, ch, st)
		}
	}
	// On again: a failure is texted, once.
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	ref = leave("08031120006")
	fail(ref, 131026)
	fail(ref, 131026)
	if _, n := w.sent("sms"); n != 1 {
		t.Fatalf("texts: %d", n)
	}
}

// Review of L4 (finding 4): WhatsApp only where the form named it. A keep
// from a form whose line did not name WhatsApp (the public host not
// knowing the template, or the business not ready when it was drawn) is
// texted, though the chat could carry it now.
func TestReviewL4OnlyWhereTheFormSaidWhatsApp(t *testing.T) {
	w := newLeftWorld(t)
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	chat := &scriptChat{ready: true, send: func(forms.FollowupMessage) (string, error) { return "wamid.n", nil }}
	w.svc.FollowupChat = chat
	if !w.keep(forms.Keep{Contact: "08031130001"}, page(140)) {
		t.Fatal("keep refused")
	}
	w.drain()
	w.past()
	w.followUps()
	if ch, _, _ := w.waState("08031130001"); ch != "sms" || chat.sends != 0 {
		t.Fatalf("not named: %s, %d sends", ch, chat.sends)
	}
}

// Review of L4 (finding 5): a WhatsApp follow-up is under the outbox's
// limits per person (§6.5), as a text is: a number that had its fill of
// messages this hour is not sent the template, and the person's
// allowance across businesses is not spent.
func TestReviewL4PerPersonLimits(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	chat := &scriptChat{ready: true, send: func(forms.FollowupMessage) (string, error) { return "wamid.p", nil }}
	w.svc.FollowupChat = chat
	w.in(func(tx pgx.Tx) error {
		for range 5 {
			if _, err := w.svc.Messages.TakeRecipient(ctx, tx, "chat", "+2348031140001"); err != nil {
				return err
			}
		}
		return nil
	})
	if !w.keep(waNamed("08031140001"), page(150)) {
		t.Fatal("keep refused")
	}
	w.drain()
	w.past()
	w.followUps()
	if chat.sends != 0 || w.outcome("08031140001") != "capped" {
		t.Fatalf("past the person's limits: %d sends, %q", chat.sends, w.outcome("08031140001"))
	}
	if w.count(`SELECT count(*) FROM form_followup_contacts WHERE contact_hash = $1`, w.svc.ContactHash("phone", "+2348031140001")) != 0 {
		t.Fatal("the person's allowance was spent")
	}
}
