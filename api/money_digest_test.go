package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/moneydigest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/moneyreport"
)

// The daily money summary (T7): yesterday's money reaches the owner on
// WhatsApp, once, and not a viewer; outside the 24-hour window it goes as
// the alert template.
func TestMoneyDailyInChat(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := w.member(t, owner, "vic@adafoods.test", "viewer")
	other := w.tenant(t, "Bee Ltd", "bee@bee.test")
	const on, vn, bn = "+2348010000051", "+2348010000052", "+2348010000053"
	w.bind(t, owner, on)
	w.bind(t, viewer, vn)
	w.bind(t, other, bn)
	for _, n := range []string{on, vn} {
		w.say(t, n, "help") // the conversation window is open
	}
	tenant := tenantIDNamed(t, w.world, "Ada Foods")
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "payment.received", map[string]any{"payment_id": "P-1", "amount_kobo": int64(2500000)}); err != nil {
		t.Fatal(err)
	}
	today, _ := moneyreport.Day(time.Now())
	tomorrow := today.AddDate(0, 0, 1)
	d := &moneydigest.Digester{Pool: w.env.DB.App, Channels: []string{"whatsapp"}, PublicURL: "https://taskiem.test",
		Now: func() time.Time {
			return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 8, 5, 0, 0, moneyreport.Lagos)
		}}
	summaries := func(n string) []string {
		var out []string
		for _, s := range w.graph.Sent(n) {
			if strings.Contains(s.Text, "Yesterday: ") {
				out = append(out, s.Text)
			}
		}
		return out
	}
	for range 2 {
		if _, err := d.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		w.tick(t)
	}
	got := summaries(on)
	if len(got) != 1 || len(summaries(vn)) != 0 || len(summaries(bn)) != 0 {
		t.Fatalf("summaries: owner %q, viewer %q, other business %q", got, summaries(vn), summaries(bn))
	}
	want := "[Ada Foods] Yesterday: ₦25,000 in — Paystack ₦25,000; ₦0 out; no invoice paid; no leak opened.\n\nMoney page: https://taskiem.test/money"
	if got[0] != want {
		t.Errorf("summary\n%q\nwant\n%q", got[0], want)
	}

	// Outside the window, the next day's goes as the alert template.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_contacts SET last_inbound_at = now() - interval '25 hours' WHERE number = $1`, on); err != nil {
		t.Fatal(err)
	}
	amount, when := int64(2500000), tomorrow.Add(10*time.Hour)
	if _, _, err := log.Record(ctx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "P-2",
		Event: "payment.received", AmountKobo: &amount, OccurredAt: &when, Source: "event"}); err != nil {
		t.Fatal(err)
	}
	d.Now = func() time.Time { return tomorrow.AddDate(0, 0, 1).Add(8*time.Hour + 5*time.Minute) }
	if _, err := d.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	last := w.graph.Last(t, on)
	if last.Template != "taskiem_daily_summary" || !strings.Contains(last.Text, "Yesterday's money, ") || !strings.Contains(last.Text, ": ₦25,000 in — Paystack ₦25,000; ₦0 out") ||
		!strings.Contains(last.Text, "Tap Open for the Money page") {
		t.Errorf("outside the window: %+v", last)
	}

	// With the summary template not approved in the number's account, the
	// alert template carries the first line, as before.
	gatePlatform(t, w.graph, w.wa)
	approve(t, w.graph, w.wa, "taskiem_alert")
	when3 := tomorrow.AddDate(0, 0, 1).Add(10 * time.Hour)
	if _, _, err := log.Record(ctx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "P-3",
		Event: "payment.received", AmountKobo: &amount, OccurredAt: &when3, Source: "event"}); err != nil {
		t.Fatal(err)
	}
	d.Now = func() time.Time { return tomorrow.AddDate(0, 0, 2).Add(8*time.Hour + 5*time.Minute) }
	if _, err := d.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	last = w.graph.Last(t, on)
	if last.Template != "taskiem_alert" || !strings.Contains(last.Text, "Yesterday: ") || !strings.Contains(last.Text, "https://taskiem.test/money") {
		t.Errorf("without the summary template: %+v", last)
	}
}

// Who gets the money summaries is the money group of Settings >
// Notifications: owners unless they turn it off, others with invoice.read
// when they turn it on, nobody without invoice.read; a queued summary is
// dropped when its person turns it off before it goes.
func TestMoneySummarySettings(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := w.member(t, owner, "vic@adafoods.test", "viewer")
	builder := w.member(t, owner, "bob@adafoods.test", "builder")
	const on, vn, bn = "+2348010000061", "+2348010000062", "+2348010000063"
	for _, x := range []struct {
		c *client
		n string
	}{{owner, on}, {viewer, vn}, {builder, bn}} {
		w.bind(t, x.c, x.n)
		w.say(t, x.n, "help")
	}
	tenant := tenantIDNamed(t, w.world, "Ada Foods")

	// The settings: the money group for those who may read invoices, on by
	// default for owners only.
	group := func(c *client) map[string]any {
		t.Helper()
		for _, s := range c.must(200, "GET", "/v1/me/notification-settings", nil)["settings"].([]any) {
			if m := s.(map[string]any); m["group"] == "money" {
				return m
			}
		}
		return nil
	}
	if g := group(owner); g["chat"] != true || g["email"] != true {
		t.Fatalf("owner's default: %v", g)
	}
	if g := group(viewer); g["chat"] != false || g["email"] != false {
		t.Fatalf("viewer's default: %v", g)
	}
	if g := group(builder); g != nil {
		t.Fatalf("a builder sees the money group: %v", g)
	}
	builder.must(403, "PUT", "/v1/me/notification-settings", map[string]any{"settings": []map[string]any{{"group": "money", "chat": true, "email": true}}})
	owner.must(200, "PUT", "/v1/me/notification-settings", map[string]any{"settings": []map[string]any{{"group": "money", "chat": false, "email": true}}})
	viewer.must(200, "PUT", "/v1/me/notification-settings", map[string]any{"settings": []map[string]any{{"group": "money", "chat": true, "email": false}}})
	if g := group(viewer); g["chat"] != true || g["email"] != false {
		t.Fatalf("viewer after turning it on: %v", g)
	}
	// The builder has it on somehow (from before losing invoice.read, say).
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO notification_settings (tenant_id, user_id, kind, chat, email)
		SELECT $1, id, 'money', true, true FROM users WHERE email = 'bob@adafoods.test'`, tenant); err != nil {
		t.Fatal(err)
	}

	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "payment.received", map[string]any{"payment_id": "P-1", "amount_kobo": int64(2500000)}); err != nil {
		t.Fatal(err)
	}
	today, _ := moneyreport.Day(time.Now())
	tomorrow := today.AddDate(0, 0, 1)
	d := &moneydigest.Digester{Pool: w.env.DB.App, Channels: []string{"whatsapp"}, PublicURL: "https://taskiem.test",
		Now: func() time.Time {
			return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 8, 5, 0, 0, moneyreport.Lagos)
		}}
	summaries := func(n string) int {
		c := 0
		for _, s := range w.graph.Sent(n) {
			if strings.Contains(s.Text, "Yesterday: ") {
				c++
			}
		}
		return c
	}
	if _, err := d.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var queued []string
	rows, err := w.env.DB.Admin.Query(ctx, `SELECT u.email FROM whatsapp_outbox o JOIN users u ON u.id = o.user_id
		WHERE o.kind = 'money_daily' AND o.tenant_id = $1 ORDER BY 1`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatal(err)
		}
		queued = append(queued, e)
	}
	rows.Close()
	if strings.Join(queued, ",") != "vic@adafoods.test" {
		t.Fatalf("queued for %v, want the viewer only", queued)
	}

	// The viewer turns it off before it goes: it is dropped.
	viewer.must(200, "PUT", "/v1/me/notification-settings", map[string]any{"settings": []map[string]any{{"group": "money", "chat": false, "email": false}}})
	w.tick(t)
	if summaries(on)+summaries(vn)+summaries(bn) != 0 {
		t.Fatalf("summaries: owner %d, viewer %d, builder %d", summaries(on), summaries(vn), summaries(bn))
	}
	var status, why string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status, last_error FROM whatsapp_outbox WHERE kind = 'money_daily' AND tenant_id = $1`, tenant).Scan(&status, &why); err != nil {
		t.Fatal(err)
	}
	if status != "dropped" || !strings.Contains(why, "turned the daily summary off") {
		t.Fatalf("after turning it off: %s %q", status, why)
	}
}
