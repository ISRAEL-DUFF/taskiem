package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Cash and expenses from chat (spec 7.4, T6): shown, recorded on Yes
// once, nothing on No, and refused to a viewer.
func TestWhatsAppMoney(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	const on, vn = "+2348010000029", "+2348010000027"
	w.bind(t, owner, on)
	viewer := w.member(t, owner, "vic@adafoods.test", "viewer")
	w.bind(t, viewer, vn)
	entries := func() int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM money_entries`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if r := w.say(t, on, "help all"); !strings.Contains(r, "*spent*") {
		t.Errorf("help: %q", r)
	}
	if r := w.say(t, vn, "help"); strings.Contains(r, "*spent*") {
		t.Errorf("viewer help: %q", r)
	}
	if r := w.say(t, vn, "spent 12,000 on diesel"); !strings.Contains(r, "cannot record money") {
		t.Errorf("viewer: %q", r)
	}
	if r := w.say(t, on, "spent lots on diesel"); !strings.Contains(r, "*spent 12,000 on diesel at Total*") {
		t.Errorf("how: %q", r)
	}

	confirm := w.send(t, on, whatsapptest.Text("spent 12k on diesel at Total"))
	if len(confirm) != 1 || len(confirm[0].Buttons) != 2 || !strings.HasSuffix(confirm[0].Text, "Record ₦12,000 spent on diesel at Total (Diesel)?") {
		t.Fatalf("confirmation: %+v", confirm)
	}
	if r := w.say(t, on, "maybe"); !strings.Contains(r, "Reply *yes* to record it") {
		t.Errorf("reminder: %q", r)
	}
	if r := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes")); len(r) != 1 || !strings.HasSuffix(r[0].Text, "Recorded. ₦12,000 out, Diesel.") {
		t.Fatalf("recorded: %+v", r)
	}
	// A second Yes records nothing more.
	w.send(t, on, whatsapptest.ButtonReply("yes", "Yes"))
	if n := entries(); n != 1 {
		t.Fatalf("entries after two yeses: %d", n)
	}
	// Attributed to the member, as the API attributes entries (their user id).
	// Labelled Diesel (what a business buys §5.4, §6), the label made.
	var kind, dir, cat, vendor, desc, by, source, channel, label string
	var kobo int64
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT kind, direction, amount_kobo, category, vendor, description, entered_by, source, channel, l.name
		FROM money_entries e JOIN spend_labels l ON l.id = e.label_id`).
		Scan(&kind, &dir, &kobo, &cat, &vendor, &desc, &by, &source, &channel, &label); err != nil {
		t.Fatal(err)
	}
	if kind != "expense" || dir != "out" || kobo != 1200000 || cat != "Diesel" || label != "Diesel" || vendor != "Total" || desc != "diesel" || len(by) != 36 ||
		source != "entered" || channel != "cash" {
		t.Errorf("entry %s %s %d %s %s %s %s %s %s %s", kind, dir, kobo, cat, vendor, desc, by, source, channel, label)
	}
	// "paid 15,000 diesel": the same label, found without case; one the
	// business renamed reads in its own spelling.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE spend_labels SET name = 'DIESEL'`); err != nil {
		t.Fatal(err)
	}
	if r := w.say(t, on, "paid 15,000 diesel"); !strings.HasSuffix(r, "Record ₦15,000 spent on diesel (DIESEL)?") {
		t.Fatalf("paid: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.HasSuffix(r, "Recorded. ₦15,000 out, DIESEL.") {
		t.Fatalf("paid recorded: %q", r)
	}
	var labels, labelled int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM spend_labels), (SELECT count(*) FROM money_entries WHERE label_id IS NOT NULL AND category = 'DIESEL')`).
		Scan(&labels, &labelled); err != nil || labels != 1 || labelled != 1 {
		t.Fatalf("labels %d, labelled %d: %v", labels, labelled, err)
	}

	// No leaves nothing behind.
	if r := w.say(t, on, "cash out 3,000 float"); !strings.HasSuffix(r, "Record ₦3,000 cash out: float?") {
		t.Errorf("cash out: %q", r)
	}
	if r := w.say(t, on, "no"); !strings.HasSuffix(r, "Not recorded.") {
		t.Errorf("no: %q", r)
	}
	if n := entries(); n != 2 {
		t.Errorf("entries after no: %d", n)
	}
	// Cash in.
	w.say(t, on, "cash in 20,000 till")
	if r := w.say(t, on, "yes"); !strings.HasSuffix(r, "Recorded. ₦20,000 in, cash.") {
		t.Errorf("cash in: %q", r)
	}
	if n := entries(); n != 3 {
		t.Errorf("entries: %d", n)
	}
	// The viewer's Yes to nothing records nothing either.
	w.send(t, vn, whatsapptest.Text("yes"))
	if n := entries(); n != 3 {
		t.Errorf("entries after the viewer: %d", n)
	}
}
