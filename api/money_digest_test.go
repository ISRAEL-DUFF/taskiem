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
	if last.Template != "taskiem_alert" || !strings.Contains(last.Text, "Yesterday: ₦25,000 in") || !strings.Contains(last.Text, "https://taskiem.test/money") {
		t.Errorf("outside the window: %+v", last)
	}
}
