package forms_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/publicweb"
)

// Forms that sell L0 (docs/spec/forms-that-sell.md §6, §10): a keep the
// public host writes is taken by the api role into one record per form
// and contact, sealed and keyed, replaced by a later keep; the records are
// reached through their narrow functions only; a submission within the
// wait forgets one and one after the follow-up marks it came back; the
// restore link gives back the choice once; and everything is purged past
// its 7 days.
func TestFormLeftKeptAndPurged(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.deploy(intake)
	var endpoint uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM form_endpoints WHERE workflow_id = $1 AND environment = 'prod'`, wf).Scan(&endpoint); err != nil {
		t.Fatal(err)
	}
	keeps := publicweb.DBKeeps(w.env.DB.PublicPool(t, 1))
	keep := func(k forms.Keep) bool {
		t.Helper()
		k.At = time.Now()
		payload, err := publicweb.SealKeep(&w.svc.Keys, endpoint, k)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := keeps.Keep(ctx, &endpoint, nil, payload)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	// Only a contact that reads, and a choice in bounds, is sealed.
	if _, err := publicweb.SealKeep(&w.svc.Keys, endpoint, forms.Keep{Contact: "not a number"}); err == nil {
		t.Fatal("a keep without a valid contact was sealed")
	}
	if _, err := publicweb.SealKeep(&w.svc.Keys, endpoint, forms.Keep{Contact: "08031234567", Choice: forms.Choice{Package: 500}}); err == nil {
		t.Fatal("a keep with a choice out of bounds was sealed")
	}
	// Neither a form nor an action, or both: refused.
	if ok, err := keeps.Keep(ctx, nil, nil, []byte("x")); err != nil || ok {
		t.Fatalf("a keep for nothing: %v %v", ok, err)
	}

	if !keep(forms.Keep{Contact: "0803 123 4567", Choice: forms.Choice{Package: 2, Zone: "Lekki"}, Site: "https://adakitchen.ng"}) {
		t.Fatal("the keep was not taken")
	}
	// A later keep from the same number replaces it; another number is
	// its own record.
	if !keep(forms.Keep{Contact: "+2348031234567", Choice: forms.Choice{Package: 3, Zone: "Yaba"}}) || !keep(forms.Keep{Contact: "bola@example.com", Choice: forms.Choice{Package: 1}}) {
		t.Fatal("later keeps were not taken")
	}
	// A keep that does not open (sealed for another form) is dropped.
	stray, _ := publicweb.SealKeep(&w.svc.Keys, uuid.New(), forms.Keep{Contact: "08039990000", At: time.Now()})
	if ok, err := keeps.Keep(ctx, &endpoint, nil, stray); err != nil || !ok {
		t.Fatalf("stray keep: %v %v", ok, err)
	}
	if err := w.svc.DrainKeeps(ctx); err != nil {
		t.Fatal(err)
	}
	type rec struct {
		id                uuid.UUID
		kind, choice, raw string
		site              *string
		expires, last     time.Time
	}
	read := func() map[string]rec {
		t.Helper()
		rows, err := w.env.DB.Admin.Query(ctx, `SELECT id, contact_kind, choice::text, contact::text, site, expires_at, last_keep_at FROM form_left WHERE endpoint_id = $1`, endpoint)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]rec{}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rec, error) {
			var x rec
			return x, r.Scan(&x.id, &x.kind, &x.choice, &x.raw, &x.site, &x.expires, &x.last)
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range list {
			out[x.kind] = x
		}
		if len(out) != len(list) {
			t.Fatalf("one record per contact: %d rows", len(list))
		}
		return out
	}
	got := read()
	if len(got) != 2 {
		t.Fatalf("records: %v", got)
	}
	ph := got["phone"]
	// Sealed: the number is not in the row; the latest choice; the site
	// kept from the first keep; 7 days from the last keep.
	if strings.Contains(ph.raw, "8031234567") || !strings.Contains(ph.choice, `"package": 3`) || !strings.Contains(ph.choice, "Yaba") ||
		ph.site == nil || *ph.site != "https://adakitchen.ng" || ph.expires.Sub(ph.last) != forms.LeftFor {
		t.Fatalf("the phone's record: %+v", ph)
	}
	var waiting int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_left_inbox`).Scan(&waiting); err != nil || waiting != 0 {
		t.Fatalf("keeps left in the inbox: %d %v", waiting, err)
	}

	// The api role reads nothing of it directly, even in its own tenant;
	// nor can the public role.
	if _, err := w.env.DB.App.Exec(ctx, `SELECT 1 FROM form_left`); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("form_left read directly: %v", err)
	}
	if _, err := w.env.DB.PublicPool(t, 1).Exec(ctx, `SELECT 1 FROM form_left`); err == nil {
		t.Error("the public role read form_left")
	}
	if _, err := w.env.DB.PublicPool(t, 1).Exec(ctx, `SELECT taskiem_form_left_inbox_claim(1)`); err == nil {
		t.Error("the public role claimed keeps")
	}

	// The email sent the form within the wait: its record is forgotten.
	w.in(func(tx pgx.Tx) error {
		out, err := w.svc.Sent(ctx, tx, w.env.Tenant, endpoint, "Bola@Example.com")
		if err != nil || out != "deleted" {
			t.Fatalf("sent within the wait: %q %v", out, err)
		}
		return nil
	})
	// The phone is followed up: its record now lasts 7 days from then,
	// and its restore link gives back the choice once, never the contact.
	var token string
	w.in(func(tx pgx.Tx) error {
		var ok bool
		var err error
		if token, ok, err = w.svc.FollowedUp(ctx, tx, w.env.Tenant, ph.id); err != nil || !ok || token == "" {
			t.Fatalf("followed up: %v %v", ok, err)
		}
		if _, again, err := w.svc.FollowedUp(ctx, tx, w.env.Tenant, ph.id); err != nil || again {
			t.Fatalf("followed up twice: %v %v", again, err)
		}
		return nil
	})
	r, ok, err := keeps.Restore(ctx, forms.TokenHash(token))
	if err != nil || !ok || r.Endpoint == nil || *r.Endpoint != endpoint || r.Choice.Package != 3 || r.Choice.Zone != "Yaba" {
		t.Fatalf("restored: %+v %v %v", r, ok, err)
	}
	if _, ok, err := keeps.Restore(ctx, forms.TokenHash(token)); err != nil || ok {
		t.Fatalf("restored twice: %v %v", ok, err)
	}
	var restoreRow string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT row_to_json(r)::text FROM form_restores r`).Scan(&restoreRow); err != nil || strings.Contains(restoreRow, "8031234567") {
		t.Fatalf("the restore row: %s %v", restoreRow, err)
	}
	// After the follow-up, sending the form marks the record came back
	// and keeps it.
	w.in(func(tx pgx.Tx) error {
		out, err := w.svc.Sent(ctx, tx, w.env.Tenant, endpoint, "08031234567")
		if err != nil || out != "came_back" {
			t.Fatalf("sent after the follow-up: %q %v", out, err)
		}
		return nil
	})
	var cameBack *time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT came_back_at FROM form_left WHERE id = $1`, ph.id).Scan(&cameBack); err != nil || cameBack == nil {
		t.Fatalf("came back: %v %v", cameBack, err)
	}

	// Purged once its time is past, with its restore link; nothing else.
	if n, err := w.svc.PurgeLeft(ctx); err != nil || n != 0 {
		t.Fatalf("purged too soon: %d %v", n, err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE form_left SET expires_at = now() - interval '1 second'; UPDATE form_restores SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := w.svc.PurgeLeft(ctx); err != nil || n != 2 {
		t.Fatalf("purged: %d %v", n, err)
	}
	if got := read(); len(got) != 0 {
		t.Fatalf("left after the purge: %v", got)
	}
}
