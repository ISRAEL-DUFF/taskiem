package api_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
)

// Bringing people in from chat (docs/spec/chat-way-in.md 10.2): an owner
// invites a number on WhatsApp (the taskiem_invite template, Join after
// the terms), a number on Telegram (a forwarded link, joined only with the
// invited number as the person's own contact), and a Slack member by
// mention; a Slack member in no business asks to join and an owner
// approves with a role, or declines.

// memberRoles are a person's roles in the business named, by their user.
func memberRoles(t *testing.T, w *world, user uuid.UUID, business string) string {
	t.Helper()
	var roles string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT COALESCE(string_agg(m.role, ',' ORDER BY m.role), '') FROM memberships m JOIN tenants t ON t.id = m.tenant_id AND t.name = $2
		WHERE m.user_id = $1`, user, business).Scan(&roles); err != nil {
		t.Fatal(err)
	}
	return roles
}

func TestInviteOnWhatsApp(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const ownerNumber, ada, bayo = "+2348031239301", "+2348031239302", "+2348031239303"
	w.bind(t, owner, ownerNumber)

	if got := w.say(t, ownerNumber, "invite me"); !strings.Contains(got, "invite +2348012345678 as viewer") {
		t.Fatalf("how: %q", got)
	}
	if got := w.say(t, ownerNumber, "invite "+ada+" as wizard"); !strings.Contains(got, "There is no role called wizard") {
		t.Fatalf("unknown role: %q", got)
	}
	if got := w.say(t, ownerNumber, "invite "+ada+" as viewer"); !strings.Contains(got, "Invite "+ada+" to Acme as viewer?") {
		t.Fatalf("confirm: %q", got)
	}
	if got := w.say(t, ownerNumber, "yes"); !strings.Contains(got, "Invitation sent to "+ada) {
		t.Fatalf("sent: %q", got)
	}
	tpl := w.graph.Last(t, ada)
	if tpl.Template != "taskiem_invite" || len(tpl.Payloads) != 2 || !strings.HasPrefix(tpl.Payloads[0], "inv:join:") || !strings.Contains(tpl.Text, "as viewer") {
		t.Fatalf("the invitation: %+v", tpl)
	}
	// Join: the terms first, nothing made before Yes.
	terms := w.press(t, ada, tpl.Payloads[0])
	if len(terms) != 1 || !strings.Contains(terms[0].Text, testTerms.URL) || len(terms[0].Buttons) != 2 || !strings.HasPrefix(terms[0].Buttons[0].ID, "inv:yes:") {
		t.Fatalf("terms: %+v", terms)
	}
	if u, _ := w.wa.UserOf(ctx, ada); u != uuid.Nil {
		t.Fatal("an account before Yes")
	}
	if got := w.press(t, ada, terms[0].Buttons[0].ID); len(got) != 1 || !strings.Contains(got[0].Text, "You are in Acme") {
		t.Fatalf("joined: %+v", got)
	}
	user, _ := w.wa.UserOf(ctx, ada)
	if user == uuid.Nil || memberRoles(t, w.world, user, "Acme") != "viewer" {
		t.Fatalf("membership: %v %q", user, memberRoles(t, w.world, user, "Acme"))
	}
	if got := w.say(t, ada, "help"); !strings.Contains(got, "status") {
		t.Fatalf("the conversation: %q", got)
	}
	// Pressed again: it was answered.
	if got := w.press(t, ada, tpl.Payloads[0]); len(got) != 1 || !strings.Contains(got[0].Text, "ended or was answered") {
		t.Fatalf("again: %+v", got)
	}

	// Stop makes nothing; a viewer cannot invite.
	w.say(t, ownerNumber, "invite "+bayo+" as viewer")
	w.say(t, ownerNumber, "yes")
	stop := w.graph.Last(t, bayo).Payloads[1]
	if got := w.press(t, bayo, stop); len(got) != 1 || !strings.Contains(got[0].Text, "you did not join") {
		t.Fatalf("stop: %+v", got)
	}
	var state string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT state FROM chat_invites WHERE address = $1`, bayo).Scan(&state); err != nil || state != "declined" {
		t.Fatalf("declined: %q %v", state, err)
	}
	if got := w.say(t, ada, "invite "+bayo+" as viewer"); !strings.Contains(got, "Only someone who manages the people") {
		t.Fatalf("a viewer invites: %q", got)
	}
}

func TestInviteOnTelegram(t *testing.T) {
	ctx := context.Background()
	w := newTGWorld(t)
	chatSignupOn(w.world, "telegram")
	owner := w.tenant(t, "Bisi Salon", "bisi@salon.test")
	const ownerAcct, chidi, other = int64(8301), int64(8302), int64(8303)
	w.link(t, owner, ownerAcct, "bisi")
	w.say(t, ownerAcct, "invite +2348035551234 as operator")
	got := w.say(t, ownerAcct, "yes")
	i := strings.Index(got.Text, "https://t.me/")
	if i < 0 {
		t.Fatalf("no link: %q", got.Text)
	}
	link, err := url.Parse(strings.Fields(got.Text[i:])[0])
	if err != nil || !strings.HasPrefix(link.Query().Get("start"), "inv_") {
		t.Fatalf("link: %q", got.Text)
	}
	press := func(acct int64, call telegramtest.Call, text string) []telegramtest.Call {
		for _, row := range call.Keyboard {
			for _, b := range row {
				if b.Text == text {
					return w.update(t, acct, func(id int64) []byte { return telegramtest.Callback(id, acct, call.MessageID, "q", b.Data) })
				}
			}
		}
		t.Fatalf("no %q button: %+v", text, call)
		return nil
	}
	offer := w.update(t, chidi, func(id int64) []byte { return telegramtest.Start(id, chidi, "chidi", link.Query().Get("start")) })
	if len(offer) != 1 || !strings.Contains(offer[0].Text, "invited you to work with Bisi Salon") {
		t.Fatalf("offer: %+v", offer)
	}
	terms := press(chidi, offer[0], "Join")
	if len(terms) != 1 || !strings.Contains(terms[0].Text, testTerms.URL) {
		t.Fatalf("terms: %+v", terms)
	}
	if ask := press(chidi, terms[0], "Yes"); len(ask) != 1 || !ask[0].AskContact {
		t.Fatalf("contact asked: %+v", ask)
	}
	contact := func(phone string, of int64) []telegramtest.Call {
		return w.update(t, chidi, func(id int64) []byte { return telegramtest.ContactMessage(id, chidi, phone, of) })
	}
	// The link reached someone else, or they share another number.
	if got := contact("+2348035559999", chidi); len(got) != 1 || !strings.Contains(got[0].Text, "not the number this invitation was for") {
		t.Fatalf("another number: %+v", got)
	}
	if got := contact("+2348035551234", other); len(got) != 1 || !strings.Contains(got[0].Text, "not the number this invitation was for") {
		t.Fatalf("someone else's contact: %+v", got)
	}
	if got := contact("+2348035551234", chidi); len(got) != 1 || !strings.Contains(got[0].Text, "You are in Bisi Salon") {
		t.Fatalf("joined: %+v", got)
	}
	user, _ := w.tg.UserOf(ctx, chidi)
	if user == uuid.Nil || memberRoles(t, w.world, user, "Bisi Salon") != "operator" {
		t.Fatalf("membership: %v", user)
	}
}

func TestSlackInviteAndAskToJoin(t *testing.T) {
	w := newSlackInstallWorld(t)
	w.install(t)
	for i, s := range []string{"yes", "Acme Foods", "1", "2"} {
		w.event(t, dm("Ev"+string(rune('a'+i)), "U0ADA", s), slackSecret)
	}
	w.fake.people["U0EVE"] = [2]string{"Eve Uche", "eve@acme.test"}
	w.fake.people["U0FAY"] = [2]string{"Fay Bello", "fay@acme.test"}

	// An invitation by mention.
	w.event(t, dm("Ev1", "U0ADA", "invite <@U0BEN> as viewer"), slackSecret)
	if s := w.says("U0ADA", len(w.fake.to("U0ADA"))-1); !strings.Contains(s[0], "Invite Ben Eze to Acme Foods as viewer?") {
		t.Fatalf("confirm: %v", s)
	}
	w.event(t, dm("Ev2", "U0ADA", "yes"), slackSecret)
	offer := w.fake.to("U0BEN")
	if len(offer) != 1 || !strings.Contains(offer[0]["text"].(string), "invited you to work with Acme Foods") {
		t.Fatalf("offer: %v", offer)
	}
	join := buttons(offer[0])[0]
	w.click(t, "U0BEN", "taskiem.reply.0", join, slackSecret)
	terms := w.fake.to("U0BEN")
	yes := buttons(terms[len(terms)-1])[0]
	if !strings.HasPrefix(yes, "inv:yes:") {
		t.Fatalf("terms: %v", terms[len(terms)-1])
	}
	w.click(t, "U0BEN", "taskiem.reply.0", yes, slackSecret)
	if s := w.says("U0BEN", len(w.fake.to("U0BEN"))-1); !strings.Contains(s[0], "You are in Acme Foods") {
		t.Fatalf("joined: %v", s)
	}
	if v := w.scalar(t, `SELECT u.email || '|' || (u.email_verified_at IS NULL) || '|' || m.role FROM slack_bindings b JOIN users u ON u.id = b.user_id
		JOIN memberships m ON m.user_id = u.id AND m.tenant_id = b.tenant_id WHERE b.slack_user_id = 'U0BEN' AND b.verified_at IS NOT NULL`); v != "ben@acme.test|true|viewer" {
		t.Fatalf("Ben: %v", v)
	}
	n := len(w.fake.to("U0BEN"))
	w.event(t, dm("Ev3", "U0BEN", "help"), slackSecret)
	if s := w.says("U0BEN", n); len(s) != 1 || !strings.Contains(s[0], "status") {
		t.Fatalf("Ben's conversation: %v", s)
	}

	// Asking to join: an owner approves with a role.
	w.event(t, dm("Ev4", "U0EVE", "hello"), slackSecret)
	ask := w.fake.to("U0EVE")
	if len(ask) != 1 || !strings.Contains(ask[0]["text"].(string), "run by Acme Foods") || !strings.HasPrefix(buttons(ask[0])[0], "inv:ask:") {
		t.Fatalf("ask offer: %v", ask)
	}
	before := len(w.fake.to("U0ADA"))
	w.click(t, "U0EVE", "taskiem.reply.0", buttons(ask[0])[0], slackSecret)
	if s := w.says("U0EVE", 1); len(s) != 1 || !strings.Contains(s[0], "Your request went to the owners of Acme Foods") {
		t.Fatalf("asked: %v", s)
	}
	toOwner := w.fake.to("U0ADA")[before:]
	if len(toOwner) != 1 || !strings.Contains(toOwner[0]["text"].(string), "Eve Uche in your Slack workspace asks to join Acme Foods") || len(buttons(toOwner[0])) != 3 {
		t.Fatalf("to the owner: %v", toOwner)
	}
	if u := w.scalar(t, `SELECT count(*) FROM memberships m JOIN slack_bindings b ON b.user_id = m.user_id WHERE b.slack_user_id = 'U0EVE'`); u != int64(0) {
		t.Fatal("a member by asking alone")
	}
	w.click(t, "U0ADA", "taskiem.reply.0", buttons(toOwner[0])[0], slackSecret)
	if s := w.says("U0ADA", before+1); len(s) != 1 || !strings.Contains(s[0], "Eve Uche was invited as viewer") {
		t.Fatalf("approved: %v", s)
	}
	eve := w.fake.to("U0EVE")
	w.click(t, "U0EVE", "taskiem.reply.0", buttons(eve[len(eve)-1])[0], slackSecret)
	eve = w.fake.to("U0EVE")
	w.click(t, "U0EVE", "taskiem.reply.0", buttons(eve[len(eve)-1])[0], slackSecret)
	if s := w.says("U0EVE", len(w.fake.to("U0EVE"))-1); !strings.Contains(s[0], "You are in Acme Foods") {
		t.Fatalf("Eve joined: %v", s)
	}

	// Declined.
	w.event(t, dm("Ev5", "U0FAY", "hello"), slackSecret)
	before = len(w.fake.to("U0ADA"))
	w.click(t, "U0FAY", "taskiem.reply.0", buttons(w.fake.to("U0FAY")[0])[0], slackSecret)
	deny := buttons(w.fake.to("U0ADA")[before])[2]
	w.click(t, "U0ADA", "taskiem.reply.0", deny, slackSecret)
	if s := w.says("U0FAY", len(w.fake.to("U0FAY"))-1); !strings.Contains(s[0], "did not let you in") {
		t.Fatalf("declined: %v", s)
	}
}
