package forms_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The review of forms that sell L3 (docs/spec/forms-that-sell.md §18,
// Review fixes).

func (w *leftWorld) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func (w *leftWorld) drain() {
	w.t.Helper()
	if err := w.svc.DrainKeeps(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

// outcome is what became of the record of a contact.
func (w *leftWorld) outcome(contact string) string {
	w.t.Helper()
	kind := "phone"
	if len(contact) > 0 && contact[0] != '0' && contact[0] != '+' {
		kind = "email"
	}
	var o string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT COALESCE(outcome, '') FROM form_left WHERE contact_hash = $1`,
		w.svc.ContactHash(kind, normal(w.t, contact))).Scan(&o); err != nil {
		w.t.Fatal(err)
	}
	return o
}

// followed adds n follow-ups sent at an age on a form (rows of no one).
func (w *leftWorld) followed(scope uuid.UUID, n int, ago time.Duration, salt string) {
	w.admin(`INSERT INTO form_left (id, tenant_id, action_id, contact, contact_hash, contact_kind, last_keep_at, expires_at, followed_up_at, outcome)
		SELECT gen_random_uuid(), $1, $2, '{}', sha256((i::text || $3)::bytea), 'phone', now(), now() + interval '7 days', now() - $4::interval, 'followed'
		  FROM generate_series(1, $5) i`, w.env.Tenant, scope, salt, fmt.Sprintf("%d seconds", int(ago.Seconds())), n)
}

// enquiry is a second form of the business, to count follow-ups on.
func (w *leftWorld) enquiry() uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	w.in(func(tx pgx.Tx) error {
		if _, err := storefront.SaveAction(context.Background(), tx, w.env.Tenant, storefront.Action{Kind: "enquiry", Label: "Ask us"}); err != nil {
			return err
		}
		return tx.QueryRow(context.Background(), `SELECT id FROM storefront_actions WHERE kind = 'enquiry' AND builtin`).Scan(&id)
	})
	return id
}

// Review of L3 (finding 1): a buyer types a wrong number, leaves the
// field (a keep), corrects it and sends the order. One record a page: the
// corrected number takes the typo's place, and the order forgets it; the
// stranger whose number was typed first is never texted.
func TestReviewTypoNumberIsTexted(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	if !w.keep(forms.Keep{Contact: "08031111111"}, page(7)) || !w.keep(forms.Keep{Contact: "08032222222"}, page(7)) {
		t.Fatal("keeps")
	}
	w.drain()
	if n := w.count(`SELECT count(*) FROM form_left WHERE action_id = $1`, w.action); n != 1 {
		t.Errorf("one page, two phones: %d records, want 1", n)
	}
	// The order is sent with the corrected number.
	w.in(func(tx pgx.Tx) error { return w.svc.OrderSent(ctx, tx, w.env.Tenant, w.action, "08032222222", "", nil) })
	w.past()
	w.followUps()
	if _, sms := w.sent("sms"); sms != 0 {
		t.Errorf("the order from this page was sent, yet %d text(s) went to the number typed first", sms)
	}
}

// Review of L3 (finding 2): one page (one keep token) names thirty
// strangers' numbers. A page may name three contacts in all, each taking
// the last one's place: one record, one text at most.
func TestReviewOnePageThirtyStrangers(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	for i := 0; i < 30; i++ {
		if !w.keep(forms.Keep{Contact: fmt.Sprintf("0803556%04d", i)}, page(9)) {
			t.Fatal("keep")
		}
	}
	w.drain()
	var mask string
	var names int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT contact_mask, page_names FROM form_left WHERE action_id = $1`, w.action).Scan(&mask, &names); err != nil {
		t.Fatal(err)
	}
	if mask != "… 0002" || names != forms.PageContacts {
		t.Errorf("the page's record: %q named %d, want the third number and %d", mask, names, forms.PageContacts)
	}
	w.past()
	w.followUps()
	var held bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT followup_held_at IS NOT NULL FROM storefront_actions WHERE id = $1`, w.action).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if _, sms := w.sent("sms"); sms > 1 {
		t.Errorf("one page, one token: %d texts to %d different numbers (held: %v)", sms, sms, held)
	}
}

// One record a page, as a buyer uses it: a typo corrected is followed up
// at the corrected number only; an email then a phone then another email
// keeps the phone; a page whose order is recorded (in the inbox, or sent
// with another contact) is done and never followed up.
func TestFormLeftOnePage(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	if !w.keep(forms.Keep{Contact: "0803 111 1111", Choice: forms.Choice{Zone: "Lekki"}}, page(1)) ||
		!w.keep(forms.Keep{Contact: "08031112222", Choice: forms.Choice{Zone: "Lekki"}}, page(1)) {
		t.Fatal("keeps")
	}
	if !w.keep(forms.Keep{Contact: "ada@example.com"}, page(2)) || !w.keep(forms.Keep{Contact: "08033334444"}, page(2)) ||
		!w.keep(forms.Keep{Contact: "ade@example.com"}, page(2)) {
		t.Fatal("keeps")
	}
	w.drain()
	got := w.count(`SELECT count(*) FROM form_left WHERE action_id = $1`, w.action)
	if got != 2 || w.outcome("08031112222") != "" || w.outcome("08033334444") != "" {
		t.Fatalf("two pages, two records: %d", got)
	}
	if n := w.count(`SELECT count(*) FROM form_left WHERE contact_hash = ANY ($1)`, [][]byte{w.svc.ContactHash("phone", "+2348031111111"),
		w.svc.ContactHash("email", "ada@example.com"), w.svc.ContactHash("email", "ade@example.com")}); n != 0 {
		t.Fatalf("a contact replaced on its page is still kept: %d", n)
	}
	// Counted as kept once a page.
	if n := w.count(`SELECT COALESCE(sum(kept), 0)::int FROM form_left_stats WHERE scope_id = $1`, w.action); n != 2 {
		t.Fatalf("kept: %d", n)
	}

	// Page 3's order waits in the inbox (sent, not yet recorded); page 4's
	// was sent with an email, not the phone kept.
	if !w.keep(forms.Keep{Contact: "08035550003"}, page(3)) || !w.keep(forms.Keep{Contact: "08035550004"}, page(4)) {
		t.Fatal("keeps")
	}
	w.drain()
	var sf uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT storefront_id FROM storefront_actions WHERE id = $1`, w.action).Scan(&sf); err != nil {
		t.Fatal(err)
	}
	w.admin(`INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload, action_id)
		VALUES (gen_random_uuid(), $1, $2, 'order', 'AB-CDEFGH', $3, $4, '\x00', $5)`, sf, w.env.Tenant, page(33), page(3), w.action)
	w.in(func(tx pgx.Tx) error {
		return w.svc.OrderSent(ctx, tx, w.env.Tenant, w.action, "", "bola@example.com", page(4))
	})
	if n := w.count(`SELECT count(*) FROM form_left WHERE page_hash = $1`, page(4)); n != 0 {
		t.Fatal("the page's order did not forget its record")
	}
	w.past()
	w.followUps()
	if n := w.count(`SELECT count(*) FROM form_left WHERE page_hash = $1`, page(3)); n != 0 {
		t.Fatal("a record whose page's order waits in the inbox was kept")
	}
	_, sms := w.sent("sms")
	if sms != 2 || w.outcome("08031112222") != "followed" || w.outcome("08033334444") != "followed" {
		t.Fatalf("texts: %d", sms)
	}
	// Once followed up, the page names no one else.
	if !w.keep(forms.Keep{Contact: "08039998888"}, page(1)) {
		t.Fatal("keep")
	}
	w.drain()
	if n := w.count(`SELECT count(*) FROM form_left WHERE page_hash = $1`, page(1)); n != 1 || w.outcome("08031112222") != "followed" {
		t.Fatal("a followed-up page took another contact")
	}
}

// Review of L3 (finding 3): the person's allowance across businesses is
// taken only when a message is sent; a record that turns out stale (its
// form gone from the page) uses none of it.
func TestFormLeftTakeOnlyWhenSent(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	if !w.keep(forms.Keep{Contact: "08035550001"}, page(1)) {
		t.Fatal("keep")
	}
	w.drain()
	// The form is taken off the page (published without it).
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE storefront_actions SET enabled = false WHERE id = $1`, w.action)
		return err
	})
	w.publish()
	w.past()
	w.followUps()
	if o := w.outcome("08035550001"); o != "stale" {
		t.Fatalf("outcome %q", o)
	}
	if n := w.count(`SELECT count(*) FROM form_followup_contacts`); n != 0 {
		t.Fatalf("a stale record used the person's allowance: %d", n)
	}
}

// Review of L3 (finding 4): a restore link and its Stop are the
// business's whose site serves them: on another business's site the link
// is unknown, nothing is used up and nothing stopped.
func TestFormLeftLinkOfItsBusiness(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	if !w.keep(forms.Keep{Contact: "08035550001", Choice: forms.Choice{Zone: "Lekki"}}, page(1)) {
		t.Fatal("keep")
	}
	w.drain()
	var token string
	var id uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM form_left WHERE action_id = $1`, w.action).Scan(&id); err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		var ok bool
		var err error
		token, ok, err = w.svc.FollowedUp(ctx, tx, w.env.Tenant, id, "sms")
		if !ok {
			t.Fatal("not followed up")
		}
		return err
	})
	hash, other := forms.TokenHash(token), uuid.New()
	if _, ok, err := w.keeps.RestoreOK(ctx, other, hash); err != nil || ok {
		t.Fatalf("another business's link restores: %v %v", ok, err)
	}
	if _, ok, err := w.keeps.Restore(ctx, other, hash); err != nil || ok {
		t.Fatalf("another business's link claimed: %v %v", ok, err)
	}
	if ok, err := w.keeps.Stop(ctx, other, hash); err != nil || ok {
		t.Fatalf("another business's link stopped: %v %v", ok, err)
	}
	if n := w.count(`SELECT count(*) FROM form_followup_stops`); n != 0 {
		t.Fatalf("stopped: %d", n)
	}
	// Its own business's: still unused.
	if _, ok, err := w.keeps.Restore(ctx, w.env.Tenant, hash); err != nil || !ok {
		t.Fatalf("the link was used up elsewhere: %v %v", ok, err)
	}
	if ok, err := w.keeps.Stop(ctx, w.env.Tenant, hash); err != nil || !ok {
		t.Fatalf("stop: %v %v", ok, err)
	}
}

// Review of L3 (finding 5): the day's and the month's caps are read and
// spent under a lock of the business, so two of the api's replicas
// following up at once do not both send the hundredth.
func TestFormLeftCapsUnderLock(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	if !w.keep(forms.Keep{Contact: "08035550001"}, page(1)) || !w.keep(forms.Keep{Contact: "08035550002"}, page(2)) {
		t.Fatal("keeps")
	}
	w.drain()
	w.past()
	// 99 follow-ups in the business today: room for one more.
	w.followed(w.action, 30, time.Hour, "a")
	w.followed(w.enquiry(), 69, time.Hour, "b")
	w.svc.AfterCaps(func() { time.Sleep(300 * time.Millisecond) })
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() { errs[i] = w.svc.FollowUpsOf(ctx, 1) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, sms := w.sent("sms"); sms != 1 {
		t.Fatalf("two workers at once: %d texts past the business's 100 a day", sms)
	}
	if a, b := w.outcome("08035550001"), w.outcome("08035550002"); a+b != "followedcapped" && a+b != "cappedfollowed" {
		t.Fatalf("outcomes %q %q", a, b)
	}
}

// The caps' windows: 100 a day per business; a follow-up counts for 24
// hours on its form and business, a contact's one a day for 24 hours and
// its three a week for 7 days; the owner's week is today and the six days
// before it.
func TestFormLeftCapWindows(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	other := w.enquiry()
	var p byte
	try := func(contact string, setup func()) string {
		t.Helper()
		p++
		if !w.keep(forms.Keep{Contact: contact}, page(p)) {
			t.Fatal("keep")
		}
		w.drain()
		setup()
		w.past()
		w.followUps()
		w.admin(`DELETE FROM form_left WHERE contact = '{}'`)
		return w.outcome(contact)
	}
	// The business's 100 a day, its forms together (the form has 30).
	if o := try("08035550001", func() { w.followed(w.action, 30, time.Hour, "a"); w.followed(other, 69, time.Hour, "b") }); o != "followed" {
		t.Fatalf("the 100th: %q", o)
	}
	if o := try("08035550002", func() { w.followed(w.action, 29, time.Hour, "a"); w.followed(other, 70, time.Hour, "b") }); o != "capped" {
		t.Fatalf("the 101st: %q", o)
	}
	// The form's 50: a minute inside the 24 hours counts, a minute past
	// does not.
	if o := try("08035550003", func() { w.followed(w.action, 50, 24*time.Hour-time.Minute, "c") }); o != "capped" {
		t.Fatalf("inside the day: %q", o)
	}
	if o := try("08035550004", func() { w.followed(w.action, 50, 24*time.Hour+time.Minute, "d") }); o != "followed" {
		t.Fatalf("past the day: %q", o)
	}
	// The contact's one a day and three a week, across businesses.
	sent := func(contact string, ago ...time.Duration) func() {
		return func() {
			for _, a := range ago {
				w.admin(`INSERT INTO form_followup_contacts (contact_hash, sent_at) VALUES ($1, now() - $2::interval)`,
					w.svc.ContactHash("phone", normal(t, contact)), fmt.Sprintf("%d seconds", int(a.Seconds())))
			}
		}
	}
	day := 24 * time.Hour
	for _, c := range []struct {
		contact string
		ago     []time.Duration
		want    string
	}{
		{"08035550005", []time.Duration{day - time.Minute}, "capped"},
		{"08035550006", []time.Duration{day + time.Minute}, "followed"},
		{"08035550007", []time.Duration{2 * day, 4 * day, 7*day - time.Minute}, "capped"},
		{"08035550008", []time.Duration{2 * day, 4 * day, 7*day + time.Minute}, "followed"},
	} {
		if o := try(c.contact, sent(c.contact, c.ago...)); o != c.want {
			t.Fatalf("%s sent %v ago: %q, want %q", c.contact, c.ago, o, c.want)
		}
	}
	// The owner's week: since six days ago (UTC), not seven.
	w.admin(`DELETE FROM form_left_stats`)
	w.admin(`INSERT INTO form_left_stats (tenant_id, scope_id, day, left_n, followed, came_back) VALUES
		($1, $2, (now() AT TIME ZONE 'UTC')::date - 6, 1, 1, 1), ($1, $2, (now() AT TIME ZONE 'UTC')::date - 7, 10, 10, 10)`, w.env.Tenant, w.action)
	w.in(func(tx pgx.Tx) error {
		sum, _, err := forms.Left(ctx, tx, w.env.Tenant, w.action, time.Now().UTC().AddDate(0, 0, -6))
		if err != nil || sum.Left != 1 || sum.Followed != 1 || sum.CameBack != 1 {
			t.Fatalf("the week: %+v %v", sum, err)
		}
		return nil
	})
}

// Review of L3 (finding 6): the public host's list of forms that follow
// up is a published storefront's of an active business only.
func TestFormLeftFollowupsPublishedOnly(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	var sf uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT storefront_id FROM storefront_actions WHERE id = $1`, w.action).Scan(&sf); err != nil {
		t.Fatal(err)
	}
	pub := w.env.DB.PublicPool(t, 1)
	list := func() []uuid.UUID {
		t.Helper()
		var ids []uuid.UUID
		if err := pub.QueryRow(ctx, `SELECT taskiem_public_form_followups($1)`, sf).Scan(&ids); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	if ids := list(); len(ids) != 1 || ids[0] != w.action {
		t.Fatalf("published: %v", ids)
	}
	w.admin(`UPDATE storefronts SET status = 'draft' WHERE id = $1`, sf)
	if ids := list(); len(ids) != 0 {
		t.Fatalf("a draft storefront's: %v", ids)
	}
	w.admin(`UPDATE storefronts SET status = 'published' WHERE id = $1`, sf)
	w.admin(`UPDATE tenants SET status = 'suspended' WHERE id = $1`, w.env.Tenant)
	if ids := list(); len(ids) != 0 {
		t.Fatalf("a suspended business's: %v", ids)
	}
}

// Review of L3 (finding 7): a buyer who claims the link just before it
// runs out and orders after is still counted as came back: a record whose
// link was claimed lasts a day past the claim.
func TestFormLeftClaimedKept(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	if !w.keep(forms.Keep{Contact: "08035550001"}, page(1)) {
		t.Fatal("keep")
	}
	w.drain()
	w.past()
	w.followUps()
	sms, _ := w.sent("sms")
	token := strings.Fields(sms[strings.Index(sms, "/back/")+len("/back/"):])[0]
	if _, ok, err := w.keeps.Restore(ctx, w.env.Tenant, forms.TokenHash(token)); err != nil || !ok {
		t.Fatalf("restore %q: %v %v", token, ok, err)
	}
	// The link and the record run out a moment after the claim.
	w.admin(`UPDATE form_left SET expires_at = now() - interval '1 second'; UPDATE form_restores SET expires_at = now() - interval '1 second'`)
	if n, err := w.svc.PurgeLeft(ctx); err != nil || n != 0 {
		t.Fatalf("purged within a day of the claim: %d %v", n, err)
	}
	w.in(func(tx pgx.Tx) error { return w.svc.OrderSent(ctx, tx, w.env.Tenant, w.action, "08035550001", "", nil) })
	w.in(func(tx pgx.Tx) error {
		sum, _, err := forms.Left(ctx, tx, w.env.Tenant, w.action, time.Now().AddDate(0, 0, -6))
		if err != nil || sum.CameBack != 1 {
			t.Fatalf("came back: %+v %v", sum, err)
		}
		return nil
	})
	// A day after the claim, both go.
	w.admin(`UPDATE form_restores SET used_at = now() - interval '25 hours'`)
	if n, err := w.svc.PurgeLeft(ctx); err != nil || n != 2 {
		t.Fatalf("purged a day after the claim: %d %v", n, err)
	}
}

// An order form of a One product front follows up from the start, unless
// it is made with follow-ups off; any other form does not.
func TestFormLeftOneProductDefault(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	on := map[string]bool{}
	w.in(func(tx pgx.Tx) error {
		f, err := storefront.CreateFront(ctx, tx, w.env.Tenant, storefront.NewFront{Name: "Ear cleaner", Kind: "one-product"})
		if err != nil {
			return err
		}
		var sf uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT storefront_id FROM storefront_fronts WHERE id = $1`, f.ID).Scan(&sf); err != nil {
			return err
		}
		add := func(slug, kind string, front uuid.UUID, followup *bool) error {
			cols, vals := "", ""
			args := []any{uuid.New(), sf, front, w.env.Tenant, kind, slug}
			if followup != nil {
				cols, vals = ", followup_on", ", $7"
				args = append(args, *followup)
			}
			_, err := tx.Exec(ctx, `INSERT INTO storefront_actions (id, storefront_id, front_id, tenant_id, kind, slug, builtin, enabled, label, payment, options, position`+cols+`)
				VALUES ($1, $2, $3, $4, $5, $6, false, false, 'x', 'none', '{}', 9`+vals+`)`, args...)
			return err
		}
		off, yes := false, true
		var home uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT front_id FROM storefront_actions WHERE id = $1`, w.action).Scan(&home); err != nil {
			return err
		}
		for _, err := range []error{add("o-plain", "order", f.ID, nil), add("o-off", "order", f.ID, &off), add("e-plain", "enquiry", f.ID, nil),
			add("h-plain", "order", home, nil), add("h-on", "order", home, &yes)} {
			if err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT slug, followup_on FROM storefront_actions WHERE front_id IN ($1, $2)`, f.ID, home)
		if err != nil {
			return err
		}
		for rows.Next() {
			var slug string
			var o bool
			if err := rows.Scan(&slug, &o); err != nil {
				return err
			}
			on[slug] = o
		}
		return rows.Err()
	})
	want := map[string]bool{"o-plain": true, "o-off": false, "e-plain": false, "h-plain": false, "h-on": true}
	for slug, v := range want {
		if on[slug] != v {
			t.Errorf("%s: follows up %v, want %v (%v)", slug, on[slug], v, on)
		}
	}
	// The kind's own order form, made with the front.
	n := 0
	for slug, o := range on {
		if _, mine := want[slug]; !mine && o {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the One product front's own order form: %v", on)
	}
}

// The review's fixes on L4's WhatsApp path: a typo corrected on the page
// is followed up on WhatsApp at the corrected number only; the person's
// allowance is taken once for the record whether WhatsApp carries it or
// the text goes in the same pass, and none for a record that goes stale;
// the caps' lock holds whichever channel is used.
func TestFormLeftReviewOnWhatsApp(t *testing.T) {
	w := newLeftWorld(t)
	ctx := context.Background()
	w.admin(`UPDATE storefront_actions SET followup_on = true WHERE id = $1`, w.action)
	chat := &fakeChat{can: true}
	w.svc.FollowupChat = chat
	if !w.keep(forms.Keep{Contact: "08031111111"}, page(1)) || !w.keep(forms.Keep{Contact: "08031112222"}, page(1)) {
		t.Fatal("keeps")
	}
	w.drain()
	w.past()
	w.followUps()
	if len(chat.sent) != 1 || chat.sent[0].To != "+2348031112222" {
		t.Fatalf("on WhatsApp: %+v", chat.sent)
	}
	takes := func() int {
		return w.count(`SELECT count(*) FROM form_followup_contacts`)
	}
	if n := takes(); n != 1 {
		t.Fatalf("taken on WhatsApp: %d", n)
	}
	// WhatsApp cannot: the text in the same pass, one take.
	chat.can = false
	if !w.keep(forms.Keep{Contact: "08035550002"}, page(2)) {
		t.Fatal("keep")
	}
	w.drain()
	w.past()
	w.followUps()
	if _, sms := w.sent("sms"); sms != 1 || takes() != 2 || w.outcome("08035550002") != "followed" {
		t.Fatalf("the text when WhatsApp cannot: %d texts, %d takes", sms, takes())
	}
	// Two replicas at once, WhatsApp carrying both: one past the 100.
	chat.can = true
	if !w.keep(forms.Keep{Contact: "08035550003"}, page(3)) || !w.keep(forms.Keep{Contact: "08035550004"}, page(4)) {
		t.Fatal("keeps")
	}
	w.drain()
	w.past()
	w.followed(w.action, 28, time.Hour, "a")
	w.followed(w.enquiry(), 69, time.Hour, "b")
	w.svc.AfterCaps(func() { time.Sleep(300 * time.Millisecond) })
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := w.svc.FollowUpsOf(ctx, 1); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(chat.sent) != 2 {
		t.Fatalf("two workers at once on WhatsApp: %d sent in all, want 2", len(chat.sent))
	}
	w.svc.AfterCaps(nil)
	// Stale (the form gone from the page): no WhatsApp, no take.
	before := takes()
	w.admin(`DELETE FROM form_left WHERE contact = '{}'`)
	if !w.keep(forms.Keep{Contact: "08035550005"}, page(5)) {
		t.Fatal("keep")
	}
	w.drain()
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE storefront_actions SET enabled = false WHERE id = $1`, w.action)
		return err
	})
	w.publish()
	w.past()
	w.followUps()
	if w.outcome("08035550005") != "stale" || takes() != before || len(chat.sent) != 2 {
		t.Fatalf("stale: %q, takes %d to %d", w.outcome("08035550005"), before, takes())
	}
}
