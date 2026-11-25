package forms_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
)

// fakeChat is a FollowupChat that records what it was asked to send.
type fakeChat struct {
	optIn bool
	can   bool
	sent  []forms.FollowupMessage
}

func (f *fakeChat) OptIn() bool { return f.optIn }

func (f *fakeChat) FollowUp(_ context.Context, _ pgx.Tx, m forms.FollowupMessage) (string, bool, error) {
	if !f.can {
		return "", false, nil
	}
	f.sent = append(f.sent, m)
	return "wamid." + m.Token[:8], true, nil
}

// Forms that sell L4 (docs/spec/forms-that-sell.md §6.4, §6.5): a typed
// phone is followed up on WhatsApp where the chat can carry the template,
// else by text in the same pass; the caps, the hold and STOP are the
// record's whatever the channel; a WhatsApp send reported failed goes by
// text once, with a link of its own; a marketing template reaches only a
// buyer who asked for WhatsApp on the form.
func TestFormLeftFollowedUpOnWhatsApp(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	chat := &fakeChat{can: true}
	w.svc.FollowupChat = chat
	var p byte = 40
	leave := func(contact string, c forms.Choice) {
		t.Helper()
		p++
		if !w.keep(forms.Keep{Contact: contact, Choice: c}, page(p)) {
			t.Fatal("keep refused")
		}
		if err := w.svc.DrainKeeps(ctx); err != nil {
			t.Fatal(err)
		}
		w.past()
		w.followUps()
	}
	rec := func(contact string) (channel, outcome string, waID *string) {
		t.Helper()
		hash := w.svc.ContactHash("phone", normal(t, contact))
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(channel, ''), COALESCE(outcome, ''), wa_id FROM form_left WHERE contact_hash = $1`, hash).Scan(&channel, &outcome, &waID); err != nil {
			t.Fatal(err)
		}
		return channel, outcome, waID
	}

	// On WhatsApp: the template's message, the link's token, no text.
	leave("0803 111 2222", forms.Choice{Zone: "Lekki"})
	if len(chat.sent) != 1 {
		t.Fatalf("chat: %d", len(chat.sent))
	}
	m := chat.sent[0]
	if m.To != "+2348031112222" || m.Business != "Ada's Kitchen" || m.Token == "" || !strings.HasSuffix(m.Link, "/back/"+m.Token) ||
		!strings.HasPrefix(m.Link, "https://ada-kitchen.shops.test/back/") {
		t.Fatalf("the message: %+v", m)
	}
	ch, out, wa := rec("08031112222")
	if ch != "whatsapp" || out != "followed" || wa == nil || *wa != "wamid."+m.Token[:8] {
		t.Fatalf("the record: %s %s %v", ch, out, wa)
	}
	if _, n := w.sent("sms"); n != 0 {
		t.Fatal("a text beside the WhatsApp message")
	}
	if ok, err := w.keeps.RestoreOK(ctx, forms.TokenHash(m.Token)); err != nil || !ok {
		t.Fatalf("the WhatsApp link: %v %v", ok, err)
	}
	if slug, ok, err := w.keeps.Where(ctx, forms.TokenHash(m.Token)); err != nil || !ok || slug != "ada-kitchen" {
		t.Fatalf("where: %q %v %v", slug, ok, err)
	}
	if _, ok, err := w.keeps.Where(ctx, forms.TokenHash("nonesuchnonesuchnonesuchnonesuch")); err != nil || ok {
		t.Fatalf("where, unknown: %v %v", ok, err)
	}

	// WhatsApp says it failed (not on WhatsApp): one text, its own link.
	if err := w.svc.FollowupFailed(ctx, w.env.Tenant, *wa); err != nil {
		t.Fatal(err)
	}
	sms, n := w.sent("sms")
	if n != 1 || !strings.HasPrefix(sms, "Ada's Kitchen: You started an order for Order jollof and did not finish. Finish it here: https://ada-kitchen.shops.test/back/") ||
		strings.Contains(sms, m.Token) {
		t.Fatalf("the text after the failure: %d %q", n, sms)
	}
	if ch, _, wa := rec("08031112222"); ch != "sms" || wa != nil {
		t.Fatalf("after the failure: %s %v", ch, wa)
	}
	token := strings.Fields(sms[strings.Index(sms, "/back/")+len("/back/"):])[0]
	if ok, err := w.keeps.RestoreOK(ctx, forms.TokenHash(token)); err != nil || !ok {
		t.Fatalf("the text's link: %v %v", ok, err)
	}
	// Once: the same failure again, or an unknown message, texts nothing.
	if err := w.svc.FollowupFailed(ctx, w.env.Tenant, "wamid."+m.Token[:8]); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.FollowupFailed(ctx, w.env.Tenant, "wamid.other"); err != nil {
		t.Fatal(err)
	}
	if _, n := w.sent("sms"); n != 1 {
		t.Fatalf("texted again: %d", n)
	}

	// The caps are across channels: the same number on another page the
	// same day is not followed up again, on WhatsApp or by text.
	w.admin(`DELETE FROM form_left WHERE action_id = $1`, w.action)
	leave("08031112222", forms.Choice{})
	if _, out, _ := rec("08031112222"); out != "capped" || len(chat.sent) != 1 {
		t.Fatalf("capped across channels: %s %d", out, len(chat.sent))
	}

	// The chat cannot carry it (no own number, not approved): a text, in
	// the same pass, never claimed again and again.
	chat.can = false
	leave("08032223333", forms.Choice{})
	if ch, out, _ := rec("08032223333"); ch != "sms" || out != "followed" {
		t.Fatalf("the text instead: %s %s", ch, out)
	}

	// A marketing template: only a buyer who asked for WhatsApp.
	chat.can, chat.optIn = true, true
	leave("08034445555", forms.Choice{})
	if ch, _, _ := rec("08034445555"); ch != "sms" || len(chat.sent) != 1 {
		t.Fatalf("not asked: %s %d", ch, len(chat.sent))
	}
	leave("08035556666", forms.Choice{WhatsApp: true})
	if ch, _, _ := rec("08035556666"); ch != "whatsapp" || len(chat.sent) != 2 {
		t.Fatalf("asked: %s %d", ch, len(chat.sent))
	}

	// STOP on WhatsApp: says the business followed the number up, and
	// stops it.
	w.in(func(tx pgx.Tx) error {
		had, err := w.svc.StopFollowups(ctx, tx, w.env.Tenant, "+2348035556666")
		if err != nil || !had {
			t.Fatalf("stop: %v %v", had, err)
		}
		had, err = w.svc.StopFollowups(ctx, tx, w.env.Tenant, "+2348039990000")
		if err != nil || had {
			t.Fatalf("stop, never followed up: %v %v", had, err)
		}
		return nil
	})
	w.admin(`DELETE FROM form_left WHERE action_id = $1`, w.action)
	w.admin(`DELETE FROM form_followup_contacts`)
	leave("08035556666", forms.Choice{WhatsApp: true})
	if _, out, _ := rec("08035556666"); out != "stopped" || len(chat.sent) != 2 {
		t.Fatalf("stopped: %s %d", out, len(chat.sent))
	}
}
