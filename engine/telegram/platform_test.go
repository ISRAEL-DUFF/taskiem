package telegram_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

func platform(t *testing.T) (*telegram.Platform, *telegramtest.Bot, *dbtest.DB) {
	d := dbtest.New(t)
	bot := telegramtest.New(t, token)
	p := telegram.New(d.App, telegram.Config{BotToken: token, APIURL: bot.URL, BotUsername: "taskiem_test_bot", WebhookSecret: "s"}, quiet, nil)
	return p, bot, d
}

func user(t *testing.T, d *dbtest.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := d.Admin.Exec(context.Background(), `INSERT INTO users (id, email) VALUES ($1, $2)`, id, id.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLinking(t *testing.T) {
	ctx := context.Background()
	p, _, d := platform(t)
	ada, bola := user(t, d), user(t, d)

	if b, err := p.Binding(ctx, ada); err != nil || b.TelegramID != 0 || b.PendingExpires != nil {
		t.Fatalf("%+v %v", b, err)
	}
	code, err := p.IssueLinkCode(ctx, ada)
	if err != nil || len(code) != 32 {
		t.Fatal(code, err)
	}
	if u := p.LinkURL(code); u != "https://t.me/taskiem_test_bot?start="+code {
		t.Error(u)
	}
	if b, err := p.Binding(ctx, ada); err != nil || b.PendingExpires == nil {
		t.Fatalf("pending: %+v %v", b, err)
	}
	// Only the hash is kept.
	var stored int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM telegram_link_codes WHERE code_hash = convert_to($1, 'UTF8')`, code).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("the code is stored as is: %d %v", stored, err)
	}
	// The app role reaches the tables only through the functions.
	if _, err := d.App.Exec(ctx, `SELECT 1 FROM telegram_link_codes`); err == nil {
		t.Error("the app role reads link codes")
	}

	u, outcome, err := p.Redeem(ctx, code, 777, "ada_o")
	if err != nil || outcome != "ok" || u != ada {
		t.Fatal(u, outcome, err)
	}
	if got, err := p.UserOf(ctx, 777); err != nil || got != ada {
		t.Fatal(got, err)
	}
	if b, err := p.Binding(ctx, ada); err != nil || b.TelegramID != 777 || b.Username != "ada_o" || b.VerifiedAt == nil || b.PendingExpires != nil {
		t.Fatalf("bound: %+v %v", b, err)
	}
	if u, outcome, err := p.Redeem(ctx, code, 777, "ada_o"); err != nil || outcome != "invalid" || u != uuid.Nil {
		t.Errorf("twice: %v %s %v", u, outcome, err)
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 32), code + "x"} {
		if _, outcome, err := p.Redeem(ctx, bad, 777, ""); err != nil || outcome != "invalid" {
			t.Errorf("%q: %s %v", bad, outcome, err)
		}
	}

	// Bola's code from Ada's account: taken, and the code stays spent.
	code2, err := p.IssueLinkCode(ctx, bola)
	if err != nil {
		t.Fatal(err)
	}
	if u, outcome, err := p.Redeem(ctx, code2, 777, "not a valid name!"); err != nil || outcome != "account_taken" || u != uuid.Nil {
		t.Errorf("taken: %v %s %v", u, outcome, err)
	}
	if got, _ := p.UserOf(ctx, 777); got != ada {
		t.Error("the binding moved")
	}

	// An expired code is invalid.
	code3, err := p.IssueLinkCode(ctx, bola)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE telegram_link_codes SET expires_at = now() - interval '1 second' WHERE user_id = $1`, bola); err != nil {
		t.Fatal(err)
	}
	if _, outcome, err := p.Redeem(ctx, code3, 888, ""); err != nil || outcome != "invalid" {
		t.Errorf("expired: %s %v", outcome, err)
	}

	// Five codes an hour (the taken code was spent, so code3 began a
	// new count).
	for i := 0; i < 4; i++ {
		if _, err := p.IssueLinkCode(ctx, bola); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := p.IssueLinkCode(ctx, bola); !errors.Is(err, telegram.ErrTooMany) {
		t.Errorf("sixth code: %v", err)
	}

	// A disabled person's account speaks for no one.
	if _, err := d.Admin.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, ada); err != nil {
		t.Fatal(err)
	}
	if got, err := p.UserOf(ctx, 777); err != nil || got != uuid.Nil {
		t.Errorf("disabled: %v %v", got, err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE users SET status = 'active' WHERE id = $1`, ada); err != nil {
		t.Fatal(err)
	}

	if id, err := p.Unbind(ctx, ada); err != nil || id != 777 {
		t.Fatal(id, err)
	}
	if id, err := p.Unbind(ctx, ada); err != nil || id != 0 {
		t.Fatal(id, err)
	}
	if got, err := p.UserOf(ctx, 777); err != nil || got != uuid.Nil {
		t.Error(got, err)
	}
}

func TestSendAndCallbacks(t *testing.T) {
	ctx := context.Background()
	p, bot, _ := platform(t)
	tok := "tk1." + strings.Repeat("x", 120) // longer than callback_data takes
	id, err := p.Send(ctx, telegram.Address(777), whatsapp.Message{Text: "[Acme] Approve?", Buttons: []whatsapp.Button{{ID: tok, Title: "Approve"}, {ID: "tk1.no", Title: "Reject"}},
		Template: &whatsapp.TplAlert, Vars: map[string]string{"x": "y"}})
	if err != nil || id == "" || id == "0" {
		t.Fatal(id, err)
	}
	s := bot.Last(t, 777)
	if s.Text != "[Acme] Approve?" || len(s.Keyboard) != 2 || len(s.Keyboard[0]) != 1 || s.Keyboard[0][0].Text != "Approve" {
		t.Fatalf("%+v", s)
	}
	data := s.Keyboard[0][0].Data
	if len(data) != 24 || !strings.HasPrefix(data, "c:") || data == s.Keyboard[1][0].Data {
		t.Fatalf("callback data %q", data)
	}
	if got, ok, err := p.ResolveCallback(ctx, 777, data); err != nil || !ok || got != tok {
		t.Fatalf("resolve: %q %v %v", got, ok, err)
	}
	if got, ok, err := p.ResolveCallback(ctx, 777, s.Keyboard[1][0].Data); err != nil || !ok || got != "tk1.no" {
		t.Fatalf("resolve 2: %q %v %v", got, ok, err)
	}
	for _, c := range []struct {
		id   int64
		data string
	}{{888, data}, {777, "c:" + strings.Repeat("A", 22)}, {777, "x:" + data[2:]}, {777, data[2:]}, {777, ""}, {0, data}} {
		if got, ok, err := p.ResolveCallback(ctx, c.id, c.data); err != nil || ok || got != "" {
			t.Errorf("%d %q resolves: %q %v %v", c.id, c.data, got, ok, err)
		}
	}
	if _, err := p.Send(ctx, "+2348012345678", whatsapp.Message{Text: "hi"}); err == nil {
		t.Error("sent to a phone number")
	}
	bot.FailNext(403, 0)
	if _, err := p.Send(ctx, telegram.Address(777), whatsapp.Message{Text: "hi"}); !errors.Is(err, telegram.ErrBlocked) {
		t.Errorf("blocked: %v", err)
	}
	if p.Prefix("Acme") != "[Acme] " || p.Prefix("") != "" {
		t.Error("prefix")
	}
}

func TestContactsAndInbound(t *testing.T) {
	ctx := context.Background()
	p, _, d := platform(t)
	tn := d.SeedTenant(t, nil)
	addr := telegram.Address(777)
	if c, err := p.Contact(ctx, addr); err != nil || c.Tenant != nil || c.LastInbound != nil {
		t.Fatalf("%+v %v", c, err)
	}
	if err := p.Touch(ctx, addr, true, false, nil); err != nil {
		t.Fatal(err)
	}
	c, err := p.Contact(ctx, addr)
	if err != nil || c.Tenant != nil || c.LastInbound == nil {
		t.Fatalf("inbound: %+v %v", c, err)
	}
	if err := p.Touch(ctx, addr, false, true, &tn.ID); err != nil {
		t.Fatal(err)
	}
	if c2, err := p.Contact(ctx, addr); err != nil || c2.Tenant == nil || *c2.Tenant != tn.ID || c2.LastInbound == nil || !c2.LastInbound.Equal(*c.LastInbound) {
		t.Fatalf("tenant: %+v %v", c2, err)
	}
	if err := p.Touch(ctx, addr, false, true, nil); err != nil {
		t.Fatal(err)
	}
	if c3, _ := p.Contact(ctx, addr); c3.Tenant != nil {
		t.Error("tenant not cleared")
	}
	if err := p.Touch(ctx, "tg:abc", true, false, nil); err == nil {
		t.Error("bad address touched")
	}
	if _, err := p.Contact(ctx, "+2348012345678"); err == nil {
		t.Error("phone number read")
	}

	fresh, err := p.ClaimInbound(ctx, 4242)
	if err != nil || !fresh {
		t.Fatal(fresh, err)
	}
	if fresh, err := p.ClaimInbound(ctx, 4242); err != nil || fresh {
		t.Error("claimed twice", err)
	}
	if fresh, err := p.ClaimInbound(ctx, 4243); err != nil || !fresh {
		t.Error("another update", err)
	}
}
