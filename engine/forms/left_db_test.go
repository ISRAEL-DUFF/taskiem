package forms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/publicweb"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// leftWorld is the forms world with a published storefront whose order
// form follows up (forms-that-sell L3).
type leftWorld struct {
	*world
	action uuid.UUID
	keeps  publicweb.Keeps
}

func newLeftWorld(t *testing.T) *leftWorld {
	w := &leftWorld{world: newWorld(t)}
	w.svc.ShopDomain = "shops.test"
	ctx := context.Background()
	tpl, _ := templates.Default().Get("storefront-order")
	def, err := tpl.Instantiate(map[string]any{}, "Storefront order")
	if err != nil {
		t.Fatal(err)
	}
	wf := w.env.Publish(t, string(def))
	w.in(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action) VALUES ($1, $2, $3, 1, 'prod', 'storefront', 'order')`,
			uuid.New(), w.env.Tenant, wf); err != nil {
			return err
		}
		if _, err := storefront.Save(ctx, tx, w.env.Tenant, "ada-kitchen", storefront.Card{Name: "Ada's Kitchen", Address: "4 Akoka Road, Yaba"}, nil, storefront.Columns{}, "user:ada"); err != nil {
			return err
		}
		if _, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "order", Enabled: true, Label: "Order jollof", Payment: "after_accept",
			Options: storefront.ActionOptions{Delivery: true, Zones: []storefront.Zone{{Name: "Lekki", FeeKobo: 150000}}}}); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id FROM storefront_actions WHERE kind = 'order' AND builtin`).Scan(&w.action)
	})
	w.publish()
	w.keeps = publicweb.DBKeeps(w.env.DB.PublicPool(t, 1))
	return w
}

func (w *leftWorld) publish() {
	ctx := context.Background()
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.Publish(ctx, tx, w.env.Tenant, "shops.test", "user:ada", false)
		return err
	})
}

func (w *leftWorld) admin(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.env.DB.Admin.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatal(err)
	}
}

// keep writes a keep as the public host does, for a page.
func (w *leftWorld) keep(k forms.Keep, page []byte) bool {
	w.t.Helper()
	k.At, k.Page = time.Now(), page
	payload, err := publicweb.SealKeep(&w.svc.Keys, w.action, k)
	if err != nil {
		w.t.Fatal(err)
	}
	ok, err := w.keeps.Keep(context.Background(), w.action, payload, page)
	if err != nil {
		w.t.Fatal(err)
	}
	return ok
}

type leftRec struct {
	id                   uuid.UUID
	kind, outcome, ch    string
	choice, raw          string
	site                 *string
	expires, last        time.Time
	left, followed, back *time.Time
}

func (w *leftWorld) records() map[string]leftRec {
	w.t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT id, contact_kind, COALESCE(outcome, ''), COALESCE(channel, ''), choice::text, contact::text, site,
		expires_at, last_keep_at, left_at, followed_up_at, came_back_at FROM form_left WHERE action_id = $1`, w.action)
	if err != nil {
		w.t.Fatal(err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (leftRec, error) {
		var x leftRec
		return x, r.Scan(&x.id, &x.kind, &x.outcome, &x.ch, &x.choice, &x.raw, &x.site, &x.expires, &x.last, &x.left, &x.followed, &x.back)
	})
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[string]leftRec{}
	for _, x := range list {
		out[x.kind] = x
	}
	if len(out) != len(list) {
		w.t.Fatalf("one record per contact kind here: %d rows", len(list))
	}
	return out
}

// past moves every waiting record's last keep before its wait.
func (w *leftWorld) past() {
	w.admin(`UPDATE form_left SET last_keep_at = now() - interval '31 minutes', claimed_until = NULL WHERE action_id = $1 AND followed_up_at IS NULL`, w.action)
}

func (w *leftWorld) followUps() {
	w.t.Helper()
	if err := w.svc.FollowUps(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

func (w *leftWorld) sent(channel string) (string, int) {
	w.t.Helper()
	ctx := context.Background()
	var text string
	var n int
	w.in(func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE channel = $1`, channel).Scan(&n); err != nil || n == 0 {
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT body FROM platform_messages WHERE channel = $1 ORDER BY created_at DESC LIMIT 1`, channel).Scan(&raw); err != nil {
			return err
		}
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		v, err := w.env.Vault.OpenTx(ctx, tx, w.env.Tenant, env)
		if err != nil {
			return err
		}
		text, _ = v.(map[string]any)["text"].(string)
		return nil
	})
	return text, n
}

func page(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// Forms that sell L0 and L3 (docs/spec/forms-that-sell.md §6, §10): a keep
// is taken only for an order form that follows up; the api role keeps one
// record per form and contact, sealed and keyed, the phone's when a page
// gave both; the records are reached through their narrow functions only;
// a submission within the wait forgets one and one after the follow-up
// marks it came back; the restore link gives back the choice once; and
// everything is purged past its 7 days.
func TestFormLeftKeptAndPurged(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	// Only a contact that reads, and a choice in bounds, is sealed.
	if _, err := publicweb.SealKeep(&w.svc.Keys, w.action, forms.Keep{Contact: "not a number"}); err == nil {
		t.Fatal("a keep without a valid contact was sealed")
	}
	if _, err := publicweb.SealKeep(&w.svc.Keys, w.action, forms.Keep{Contact: "08031234567", Choice: forms.Choice{Package: 500}}); err == nil {
		t.Fatal("a keep with a choice out of bounds was sealed")
	}
	// The form does not follow up: no keep is taken (the review of L0).
	if w.keep(forms.Keep{Contact: "08031234567"}, page(1)) {
		t.Fatal("a keep taken for a form that does not follow up")
	}
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	// Nor without its page's key.
	if ok, err := w.keeps.Keep(ctx, w.action, []byte("x"), nil); err != nil || ok {
		t.Fatalf("a keep without its page: %v %v", ok, err)
	}

	// An email typed first on page 1, then the phone on it: one record,
	// the phone's. Bola on page 2 is her own.
	if !w.keep(forms.Keep{Contact: "ada@example.com", Choice: forms.Choice{Zone: "Lekki"}}, page(1)) {
		t.Fatal("the keep was not taken")
	}
	if err := w.svc.DrainKeeps(ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.records(); len(got) != 1 || got["email"].id == uuid.Nil {
		t.Fatalf("the email's record: %v", got)
	}
	if !w.keep(forms.Keep{Contact: "0803 123 4567", Choice: forms.Choice{Package: 2, Zone: "Lekki"}, Site: "https://adakitchen.ng"}, page(1)) ||
		!w.keep(forms.Keep{Contact: "+2348031234567", Choice: forms.Choice{Package: 3, Zone: "Yaba"}}, page(1)) ||
		!w.keep(forms.Keep{Contact: "ada@example.com"}, page(1)) {
		t.Fatal("later keeps were not taken")
	}
	// A keep that does not open (sealed for another form) is dropped.
	stray, _ := publicweb.SealKeep(&w.svc.Keys, uuid.New(), forms.Keep{Contact: "08039990000", At: time.Now()})
	if ok, err := w.keeps.Keep(ctx, w.action, stray, page(9)); err != nil || !ok {
		t.Fatalf("stray keep: %v %v", ok, err)
	}
	if err := w.svc.DrainKeeps(ctx); err != nil {
		t.Fatal(err)
	}
	got := w.records()
	ph := got["phone"]
	if len(got) != 1 || ph.id == uuid.Nil {
		t.Fatalf("one record for the page, the phone's: %v", got)
	}
	// Sealed: the number is not in the row; the latest choice; the site
	// kept from the first keep; 7 days from the last keep; masked.
	if strings.Contains(ph.raw, "8031234567") || !strings.Contains(ph.choice, `"package": 3`) || !strings.Contains(ph.choice, "Yaba") ||
		ph.site == nil || *ph.site != "https://adakitchen.ng" || ph.expires.Sub(ph.last) != forms.LeftFor {
		t.Fatalf("the phone's record: %+v", ph)
	}
	var mask string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT contact_mask FROM form_left WHERE id = $1`, ph.id).Scan(&mask); err != nil || mask != "… 4567" {
		t.Fatalf("masked: %q %v", mask, err)
	}
	var waiting int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_left_inbox`).Scan(&waiting); err != nil || waiting != 0 {
		t.Fatalf("keeps left in the inbox: %d %v", waiting, err)
	}

	// The api role reads nothing of them directly, even in its own tenant;
	// nor can the public role.
	for _, table := range []string{"form_left", "form_left_stats", "form_followup_contacts", "form_followup_stops", "form_restores"} {
		if _, err := w.env.DB.App.Exec(ctx, `SELECT 1 FROM `+table); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s read directly: %v", table, err)
		}
		if _, err := w.env.DB.PublicPool(t, 1).Exec(ctx, `SELECT 1 FROM `+table); err == nil {
			t.Errorf("the public role read %s", table)
		}
	}
	if _, err := w.env.DB.PublicPool(t, 1).Exec(ctx, `SELECT taskiem_form_left_inbox_claim(1)`); err == nil {
		t.Error("the public role claimed keeps")
	}

	// Bola sent the form within the wait: her record is forgotten.
	if !w.keep(forms.Keep{Contact: "bola@example.com"}, page(2)) {
		t.Fatal("bola's keep")
	}
	if err := w.svc.DrainKeeps(ctx); err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		if err := w.svc.OrderSent(ctx, tx, w.env.Tenant, w.action, "", "Bola@Example.com"); err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if got := w.records(); len(got) != 1 {
		t.Fatalf("sent within the wait: %v", got)
	}
	// The phone is followed up: its record now lasts 7 days from then,
	// and its restore link gives back the choice once, never the contact.
	var token string
	w.in(func(tx pgx.Tx) error {
		var ok bool
		var err error
		if token, ok, err = w.svc.FollowedUp(ctx, tx, w.env.Tenant, ph.id, "sms"); err != nil || !ok || token == "" {
			t.Fatalf("followed up: %v %v", ok, err)
		}
		if _, again, err := w.svc.FollowedUp(ctx, tx, w.env.Tenant, ph.id, "sms"); err != nil || again {
			t.Fatalf("followed up twice: %v %v", again, err)
		}
		return nil
	})
	if ok, err := w.keeps.RestoreOK(ctx, forms.TokenHash(token)); err != nil || !ok {
		t.Fatalf("the link's page: %v %v", ok, err)
	}
	r, ok, err := w.keeps.Restore(ctx, forms.TokenHash(token))
	if err != nil || !ok || r.Action == nil || *r.Action != w.action || r.Choice.Package != 3 || r.Choice.Zone != "Yaba" {
		t.Fatalf("restored: %+v %v %v", r, ok, err)
	}
	if _, ok, err := w.keeps.Restore(ctx, forms.TokenHash(token)); err != nil || ok {
		t.Fatalf("restored twice: %v %v", ok, err)
	}
	if ok, err := w.keeps.RestoreOK(ctx, forms.TokenHash(token)); err != nil || ok {
		t.Fatalf("the link's page once used: %v %v", ok, err)
	}
	var restoreRow string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT row_to_json(r)::text FROM form_restores r`).Scan(&restoreRow); err != nil || strings.Contains(restoreRow, "8031234567") {
		t.Fatalf("the restore row: %s %v", restoreRow, err)
	}
	// After the follow-up, sending the form marks the record came back,
	// once, and keeps it; it is counted.
	for range 2 {
		w.in(func(tx pgx.Tx) error {
			return w.svc.OrderSent(ctx, tx, w.env.Tenant, w.action, "08031234567", "")
		})
	}
	if rec := w.records()["phone"]; rec.back == nil {
		t.Fatalf("came back: %+v", rec)
	}
	w.in(func(tx pgx.Tx) error {
		sum, list, err := forms.Left(ctx, tx, w.env.Tenant, w.action, time.Now().AddDate(0, 0, -6))
		if err != nil || sum.Followed != 1 || sum.CameBack != 1 || sum.Left != 1 || len(list) != 1 || list[0].Contact != "… 4567" || list[0].Channel != "sms" {
			t.Fatalf("the owner's view: %+v %+v %v", sum, list, err)
		}
		return nil
	})

	// Purged once its time is past, with its restore link; nothing else.
	if n, err := w.svc.PurgeLeft(ctx); err != nil || n != 0 {
		t.Fatalf("purged too soon: %d %v", n, err)
	}
	w.admin(`UPDATE form_left SET expires_at = now() - interval '1 second'; UPDATE form_restores SET expires_at = now() - interval '1 second'`)
	if n, err := w.svc.PurgeLeft(ctx); err != nil || n != 2 {
		t.Fatalf("purged: %d %v", n, err)
	}
	if got := w.records(); len(got) != 0 {
		t.Fatalf("left after the purge: %v", got)
	}
	// The counts outlast the records.
	w.in(func(tx pgx.Tx) error {
		sum, _, err := forms.Left(ctx, tx, w.env.Tenant, w.action, time.Now().AddDate(0, 0, -6))
		if err != nil || sum.CameBack != 1 {
			t.Fatalf("came back, kept as a number: %+v %v", sum, err)
		}
		return nil
	})
}

// The follow-up (§6.3 to §6.5): after the wait, one message by the
// channel the buyer gave (a typed phone by SMS, an email by email), with
// the restore link; a form that stopped following up, a contact who
// stopped them, the caps across forms, per form and the plan's month, and
// the hold on a run of keeps each keep it from being sent, and say why.
func TestFormLeftFollowedUp(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	drain := func() {
		t.Helper()
		if err := w.svc.DrainKeeps(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if !w.keep(forms.Keep{Contact: "08031234567", Choice: forms.Choice{Zone: "Lekki"}}, page(1)) || !w.keep(forms.Keep{Contact: "bola@example.com"}, page(2)) {
		t.Fatal("keeps")
	}
	drain()
	// Within the wait: nothing.
	w.followUps()
	if _, n := w.sent("sms"); n != 0 {
		t.Fatal("followed up within the wait")
	}
	w.past()
	w.followUps()
	got := w.records()
	if got["phone"].ch != "sms" || got["phone"].followed == nil || got["email"].ch != "email" || got["email"].followed == nil {
		t.Fatalf("followed up: %+v", got)
	}
	sms, n := w.sent("sms")
	if n != 1 || !strings.HasPrefix(sms, "Ada's Kitchen: You started an order for Order jollof and did not finish. Finish it here: https://ada-kitchen.shops.test/back/") {
		t.Fatalf("the text: %d %q", n, sms)
	}
	mail, n := w.sent("email")
	if n != 1 || !strings.Contains(mail, "https://ada-kitchen.shops.test/back/") || !strings.Contains(mail, "Stop these messages") {
		t.Fatalf("the email: %d %q", n, mail)
	}
	// Once only: again, nothing more.
	w.past()
	w.followUps()
	if _, n := w.sent("sms"); n != 1 {
		t.Fatalf("a second follow-up: %d", n)
	}
	// The link's Stop stops this business's follow-ups to the number.
	token := strings.TrimSpace(sms[strings.Index(sms, "/back/")+len("/back/"):])
	token = strings.Fields(token)[0]
	if ok, err := w.keeps.Stop(ctx, forms.TokenHash(token)); err != nil || !ok {
		t.Fatalf("stop: %v %v", ok, err)
	}

	// Each reason not to send, with a fresh contact on its own page.
	var p byte = 10
	try := func(contact string, setup func(), want string) {
		t.Helper()
		p++
		if !w.keep(forms.Keep{Contact: contact}, page(p)) {
			t.Fatalf("%s: keep refused", want)
		}
		drain()
		if setup != nil {
			setup()
		}
		w.past()
		w.followUps()
		var outcome string
		var left *time.Time
		hash := w.svc.ContactHash(map[bool]string{true: "email", false: "phone"}[strings.Contains(contact, "@")], normal(t, contact))
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(outcome, ''), left_at FROM form_left WHERE contact_hash = $1`, hash).Scan(&outcome, &left); err != nil {
			t.Fatal(err)
		}
		if outcome != want || left == nil {
			t.Fatalf("%s: outcome %q, left %v", contact, outcome, left)
		}
	}
	// A new keep from the stopped number: stopped.
	w.admin(`DELETE FROM form_left WHERE contact_kind = 'phone'`)
	try("08031234567", nil, "stopped")
	// A number followed up by another business today: capped across them.
	try("08035550001", func() {
		w.admin(`INSERT INTO form_followup_contacts (contact_hash) VALUES ($1)`, w.svc.ContactHash("phone", "+2348035550001"))
	}, "capped")
	// Fifty today on the form: capped.
	try("08035550002", func() {
		w.admin(`INSERT INTO form_left (id, tenant_id, action_id, contact, contact_hash, contact_kind, last_keep_at, expires_at, followed_up_at, outcome)
			SELECT gen_random_uuid(), $1, $2, '{}', sha256(i::text::bytea), 'phone', now(), now() + interval '7 days', now(), 'followed' FROM generate_series(1, 50) i`, w.env.Tenant, w.action)
	}, "capped")
	w.admin(`DELETE FROM form_left WHERE contact = '{}'`)
	// The plan's month: limit.
	w.svc.FollowupLimit = func(context.Context, uuid.UUID) (int64, error) { return 2, nil }
	try("08035550003", nil, "limit")
	w.svc.FollowupLimit = nil
	// The form stopped following up: off.
	try("08035550004", func() { w.admin(`UPDATE storefront_actions SET followup_on = false WHERE id = $1`, w.action) }, "off")
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)

	// A run of keeps holds the form's follow-ups, with the alert's mark;
	// released, the record is followed up.
	try("08035550005", func() {
		w.admin(`UPDATE form_left_stats SET kept = 40 WHERE scope_id = $1`, w.action)
	}, "")
	var held *time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT followup_held_at FROM storefront_actions WHERE id = $1`, w.action).Scan(&held); err != nil || held == nil {
		t.Fatalf("held: %v %v", held, err)
	}
	before, _ := w.sent("sms")
	w.admin(`UPDATE storefront_actions SET followup_held_at = NULL, followup_released_at = now() WHERE id = $1`, w.action)
	w.past()
	w.followUps()
	if after, n := w.sent("sms"); after == before || n != 2 {
		t.Fatalf("released: %d %q", n, after)
	}

	// Erasing the person erases their record.
	var subject string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT contact->>'subject' FROM form_left WHERE contact_kind = 'email'`).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if err := w.env.Vault.Erase(ctx, w.env.Tenant, subject, "user:ada"); err != nil {
		t.Fatal(err)
	}
	var emails int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_left WHERE contact_kind = 'email'`).Scan(&emails); err != nil || emails != 0 {
		t.Fatalf("erased: %d %v", emails, err)
	}
}

func normal(t *testing.T, contact string) string {
	t.Helper()
	_, v, err := forms.Keep{Contact: contact}.ContactOf()
	if err != nil {
		t.Fatal(err)
	}
	return v
}
