package moneydigest_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneydigest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
)

var ctx = context.Background()

type mailer struct {
	mu   sync.Mutex
	sent []string // "to|message"
}

func (m *mailer) Send(_ context.Context, _ string, to []string, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, strings.Join(to, ",")+"|"+string(msg))
	return nil
}

func (m *mailer) to(addr string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, s := range m.sent {
		if strings.HasPrefix(s, addr+"|") {
			out = append(out, s)
		}
	}
	return out
}

func TestDigests(t *testing.T) {
	e := rt.New(t)
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenants SET name = 'Ada Foods' WHERE id = $1`, e.Tenant)
		return err
	})
	person := func(role, number string) (uuid.UUID, string) {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		email := id.String() + "@adafoods.test"
		in(func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, 'm')`, id, email); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3)`, e.Tenant, id, role)
			return err
		})
		if number != "" {
			if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO whatsapp_bindings (user_id, number) VALUES ($1, $2)`, id, number); err != nil {
				t.Fatal(err)
			}
		}
		return id, email
	}
	n := time.Now().UnixNano() % 1000000
	owner, ownerEmail := person("owner", "+234801"+pad(n))
	viewer, viewerEmail := person("viewer", "+234802"+pad(n))
	_, quietOwnerEmail := person("owner", "") // email only: no chat
	// Settings > Notifications, the money group: chat is the daily summary,
	// email the monthly email.
	choose := func(user uuid.UUID, daily, monthly bool) {
		t.Helper()
		in(func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO notification_settings (tenant_id, user_id, kind, chat, email) VALUES ($1, $2, 'money', $3, $4)
				ON CONFLICT (tenant_id, user_id, kind) DO UPDATE SET chat = EXCLUDED.chat, email = EXCLUDED.email`, e.Tenant, user, daily, monthly)
			return err
		})
	}
	// An owner who turned the daily summary off but kept the email.
	offOwner, offOwnerEmail := person("owner", "+234803"+pad(n))
	choose(offOwner, false, true)
	// A viewer who turned the daily summary on, not the email.
	keen, keenEmail := person("viewer", "+234804"+pad(n))
	choose(keen, true, false)
	// A builder may not read invoices: nothing, whatever the setting says.
	builder, builderEmail := person("builder", "+234805"+pad(n))
	choose(builder, true, true)
	// A custom role with invoice.read, who turned the email on.
	in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO roles (tenant_id, name, permissions, created_by) VALUES ($1, 'bookkeeper', '{invoice.read}', 'test')`, e.Tenant)
		return err
	})
	bookkeeper, bookkeeperEmail := person("bookkeeper", "")
	choose(bookkeeper, false, true)
	queued := func(user uuid.UUID) int {
		t.Helper()
		var c int
		in(func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM whatsapp_outbox WHERE kind = 'money_daily' AND user_id = $1`, user).Scan(&c)
		})
		return c
	}

	// Today: ₦250,000 in on Paystack with a ₦3,750 fee, ₦50,000 on a POS,
	// ₦20,000 on its way, ₦12,000 out in cash, an invoice paid and a leak.
	log := &moneylog.Log{Pool: e.DB.App, PII: e.Vault}
	inv := &invoicing.Invoices{Pool: e.DB.App, Vault: e.Vault, PageBase: "https://forms.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: e.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
	fee, amount, pending := int64(375000), int64(25000000), int64(2000000)
	if _, _, err := log.Record(ctx, e.Tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "P-1",
		Event: "payment.received", AmountKobo: &amount, FeeKobo: &fee, Source: "event"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := log.Record(ctx, e.Tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "pending", Channel: "paystack/main", Reference: "P-2",
		Event: "payment.pending", AmountKobo: &pending, Source: "event"}); err != nil {
		t.Fatal(err)
	}
	if _, err := log.RecordEvent(ctx, e.Tenant, "prod", "moniepoint_pos", "shop", "pos.sale", map[string]any{"reference": "S-1", "amount_kobo": int64(5000000), "status": "approved"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := log.Enter(ctx, e.Tenant, "user:ada", moneylog.Entered{Kind: "expense", AmountKobo: 1200000, Description: "Diesel", Category: "fuel", Key: "x"}); err != nil {
		t.Fatal(err)
	}
	var x invoicing.Invoice
	in(func(tx pgx.Tx) error {
		var err error
		x, err = inv.Create(ctx, tx, e.Tenant, invoicing.Draft{Customer: invoicing.Customer{Name: "Bola", Email: "bola@example.com"}, AmountKobo: 25000000}, "user:ada")
		return err
	})
	if _, err := inv.Send(ctx, e.Tenant, x.Number, "user:ada", "k"); err != nil {
		t.Fatal(err)
	}
	in(func(tx pgx.Tx) error {
		var entry uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE reference = 'P-1'`).Scan(&entry); err != nil {
			return err
		}
		if _, _, err := inv.RecordPayment(ctx, tx, e.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 25000000, PaymentID: "P-1", EntryID: &entry, How: "exact", By: "system"}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO money_leaks (id, tenant_id, kind, subject, amount_kobo, detail) VALUES (gen_random_uuid(), $1, 'overdue', 'x', 777700, 'late')`, e.Tenant)
		return err
	})

	today, _ := moneyreport.Day(time.Now())
	at := func(day time.Time, hour int) func() time.Time {
		return func() time.Time {
			return time.Date(day.Year(), day.Month(), day.Day(), hour, 30, 0, 0, moneyreport.Lagos)
		}
	}
	tomorrow := today.AddDate(0, 0, 1)

	// Before 08:00 nothing goes.
	d := &moneydigest.Digester{Pool: e.DB.App, Channels: []string{"whatsapp", "telegram"}, PublicURL: "https://app.test", Now: at(tomorrow, 7)}
	if _, err := d.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if queued(owner) != 0 {
		t.Fatal("sent before eight")
	}
	// After: one message to the owner with a chat, none to the viewer;
	// a second look, and a second scheduler, send nothing more.
	d.Now = at(tomorrow, 8)
	other := &moneydigest.Digester{Pool: e.DB.App, Channels: d.Channels, Now: d.Now}
	for _, dg := range []*moneydigest.Digester{d, d, other} {
		if _, err := dg.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if queued(owner) != 1 || queued(viewer) != 0 || queued(offOwner) != 0 || queued(keen) != 1 || queued(builder) != 0 {
		t.Fatalf("queued: owner %d, viewer %d, owner who turned it off %d, viewer who turned it on %d, builder %d",
			queued(owner), queued(viewer), queued(offOwner), queued(keen), queued(builder))
	}
	// Whether each still gets it, as the outbox asks before sending.
	for _, c := range []struct {
		who  uuid.UUID
		want bool
	}{{owner, true}, {viewer, false}, {offOwner, false}, {keen, true}, {builder, false}} {
		var got bool
		in(func(tx pgx.Tx) error {
			var err error
			got, err = moneydigest.Gets(ctx, tx, e.Tenant, c.who, moneydigest.Daily)
			return err
		})
		if got != c.want {
			t.Errorf("Gets(%s) = %v", c.who, got)
		}
	}
	// Someone who turns it on after the day was taken waits for the next
	// day's: the recipients were decided when it was taken.
	choose(viewer, true, false)
	if _, err := d.Tick(ctx); err != nil || queued(viewer) != 0 {
		t.Fatalf("turned on later: %v, queued %d", err, queued(viewer))
	}
	choose(viewer, false, false)
	var outcome string
	in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT outcome FROM money_digests WHERE tenant_id = $1 AND kind = 'daily' AND period = $2`, e.Tenant, today.Format("2006-01-02")).Scan(&outcome)
	})
	if outcome != "sent" {
		t.Errorf("outcome %q", outcome)
	}

	// The words.
	var s moneyreport.Summary
	in(func(tx pgx.Tx) error {
		var err error
		s, err = moneyreport.Summarise(ctx, tx, e.Tenant, today, tomorrow)
		return err
	})
	text := moneydigest.DailyText(lang.EN, s, d.Link())
	want := "Yesterday: ₦300,000 in (₦20,000 pending) — Paystack ₦250,000, Moniepoint POS ₦50,000; ₦12,000 out; 1 invoice paid (₦250,000); 1 leak opened (₦7,777).\n\nMoney page: https://app.test/money"
	if text != want {
		t.Errorf("text\n%q\nwant\n%q", text, want)
	}
	for _, l := range []lang.Tag{lang.YO, lang.HA, lang.IG, lang.PCM} {
		if got := moneydigest.DailyText(l, s, d.Link()); strings.Contains(got, "{") || !strings.Contains(got, "₦300,000") {
			t.Errorf("%s: %q", l, got)
		}
	}

	// A quiet day sends nothing.
	d.Now = at(tomorrow.AddDate(0, 0, 1), 9)
	if r, err := d.Tick(ctx); err != nil || queued(owner) != 1 {
		t.Fatalf("quiet day: %+v %v, queued %d", r, err, queued(owner))
	}

	// The monthly email, on the 1st after 09:00, to the owners: once, and
	// only when email is set up.
	_, next := moneyreport.Month(time.Now())
	m := &mailer{}
	month := &moneydigest.Digester{Pool: e.DB.App, PublicURL: "https://app.test", Now: at(next, 9)}
	if _, err := month.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	month.Mailer, month.From = m, "Taskiem <noreply@taskiem.test>"
	month.Now = at(next, 8)
	if _, err := month.Tick(ctx); err != nil || len(m.to(ownerEmail)) != 0 {
		t.Fatal("emailed before nine")
	}
	month.Now = at(next, 9)
	for range 2 {
		if _, err := month.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got := m.to(ownerEmail)
	if len(got) != 1 || len(m.to(quietOwnerEmail)) != 1 || len(m.to(viewerEmail)) != 0 {
		t.Fatalf("emails: owner %d, second owner %d, viewer %d", len(got), len(m.to(quietOwnerEmail)), len(m.to(viewerEmail)))
	}
	// The email follows its own setting, not the daily summary's.
	if len(m.to(offOwnerEmail)) != 1 || len(m.to(keenEmail)) != 0 || len(m.to(builderEmail)) != 0 || len(m.to(bookkeeperEmail)) != 1 {
		t.Fatalf("emails: owner with the daily off %d, viewer with the daily on %d, builder %d, bookkeeper %d",
			len(m.to(offOwnerEmail)), len(m.to(keenEmail)), len(m.to(builderEmail)), len(m.to(bookkeeperEmail)))
	}
	mail := got[0]
	monthName := today.Format("January 2006")
	for _, w := range []string{"Subject: [Ada Foods] money in " + monthName, "Money in: ₦300,000 settled, ₦20,000 pending", "Money out: ₦12,000 settled",
		"Fees: ₦3,750 (1.50% of", "Paystack (paystack/main): in ₦250,000 settled", "Invoices paid: 1 (₦250,000)", "Leaks opened: 1 (₦7,777)",
		"https://app.test/money"} {
		if !strings.Contains(mail, w) {
			t.Errorf("email lacks %q:\n%s", w, mail)
		}
	}
	if strings.Contains(mail, "bola") || strings.Contains(mail, "Bola") {
		t.Error("the email names a customer")
	}
}

// pad is n as six digits and a one: a number no other test binds.
func pad(n int64) string {
	b := []byte("000000")
	for i := 5; i >= 0 && n > 0; i-- {
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b) + "1"
}
