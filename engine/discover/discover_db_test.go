package discover

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// listedBusiness makes a business with a complete listing of its own (a
// category, an area, hours and a logo), turned on, with no storefront.
func listedBusiness(t *testing.T, d *dbtest.DB, name string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	media := uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants (id, name, plan_id) VALUES ($1, $2, gen_random_uuid())`, []any{id, name}},
		{`INSERT INTO storefront_media (id, tenant_id, hash, width, height, type, urls, paths, created_by)
		 VALUES ($1, $2, $3, 512, 512, 'image/jpeg', '{"512": "https://cdn.test/logo.jpg"}', '{}', 'test')`, []any{media, id, media.String()}},
		{`INSERT INTO business_profile (tenant_id, updated_by, logo_media) VALUES ($1, 'test', $2)`, []any{id, media}},
		{`INSERT INTO discover_settings (tenant_id, listed, category, area, hours, updated_by)
		 VALUES ($1, true, 'food', 'Yaba', '{"days": [[{"open": "00:00", "close": "23:59"}], [], [], [], [], [], []]}', 'test')`, []any{id}},
	} {
		if _, err := d.Admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	return id
}

func refresh(t *testing.T, d *dbtest.DB, tenant uuid.UUID) Settings {
	t.Helper()
	var st Settings
	if err := db.InTenantTx(context.Background(), d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		st, err = RefreshTx(context.Background(), tx, tenant, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func indexed(t *testing.T, d *dbtest.DB, tenant uuid.UUID) bool {
	t.Helper()
	var n int
	if err := d.Admin.QueryRow(context.Background(), `SELECT count(*) FROM discover_index WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func public(t *testing.T, d *dbtest.DB, tenant uuid.UUID) bool {
	t.Helper()
	var n int
	if err := d.Admin.QueryRow(context.Background(), `SELECT count(*) FROM taskiem_discover_search(NULL, NULL, NULL, NULL, NULL, 21, 0) WHERE business = $1`, tenant).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// The sweep wrote a listing from a read made before its owner turned it
// off: with the Discover lock, the owner's change waits for the sweep's
// transaction, then takes the listing out; it is not public after, nor
// after the next sweep (the owner's change marked it).
func TestRefreshWaitsForTheOwner(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	biz := listedBusiness(t, d, "Bola Bakes")
	if st := refresh(t, d, biz); !st.Shown {
		t.Fatalf("not shown: %+v", st)
	}

	read := make(chan struct{})
	release := make(chan struct{})
	afterRead = func(tenant uuid.UUID) error {
		if tenant == biz {
			close(read)
			<-release
		}
		return nil
	}
	defer func() { afterRead = nil }()

	sweep := make(chan error, 1)
	go func() {
		sweep <- db.InTenantTx(ctx, d.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
			_, err := RefreshTx(ctx, tx, biz, "")
			return err
		})
	}()
	<-read // the sweep has read "on"
	afterRead = nil
	off := false
	owner := make(chan error, 1)
	go func() {
		owner <- db.InTenantTx(ctx, d.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
			_, err := SaveTx(ctx, tx, biz, SettingsIn{Listed: &off}, "user:owner", "")
			return err
		})
	}()
	select {
	case err := <-owner:
		t.Fatalf("the owner's change did not wait for the sweep's transaction: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-sweep; err != nil {
		t.Fatal(err)
	}
	if err := <-owner; err != nil {
		t.Fatal(err)
	}
	if indexed(t, d, biz) || public(t, d, biz) {
		t.Fatal("a listing turned off is in the index")
	}
	var marked int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM discover_dirty WHERE tenant_id = $1`, biz).Scan(&marked); err != nil || marked != 1 {
		t.Fatalf("the owner's change did not mark the sweep: %d %v", marked, err)
	}
	sw := &Sweeper{Pool: d.App}
	if _, err := sw.Sweep(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if indexed(t, d, biz) || public(t, d, biz) {
		t.Fatal("the sweep put back a listing turned off")
	}
}

// Defence in depth: a row in the index whose settings say off or removed
// is not shown, whatever its state column says; and the state column
// follows the settings at once.
func TestOpenAsksTheSettings(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	biz := listedBusiness(t, d, "Ada Rice")
	refresh(t, d, biz)
	if !public(t, d, biz) {
		t.Fatal("not public")
	}
	var hidden *string
	for _, c := range []struct{ set, want string }{
		{`UPDATE discover_settings SET listed = false WHERE tenant_id = $1`, "off"},
		{`UPDATE discover_settings SET listed = true, removed_at = now(), removed_by = 'ops', removed_note = 'x' WHERE tenant_id = $1`, "removed"},
	} {
		if _, err := d.Admin.Exec(ctx, c.set, biz); err != nil {
			t.Fatal(err)
		}
		if err := d.Admin.QueryRow(ctx, `SELECT hidden FROM discover_index WHERE tenant_id = $1`, biz).Scan(&hidden); err != nil || hidden == nil || *hidden != c.want {
			t.Fatalf("%s: hidden %v (%v)", c.set, hidden, err)
		}
		// The state column forced back, as if written without it: the
		// one-business check still asks the settings.
		tx, err := d.Admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var open bool
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE discover_index SET hidden = NULL WHERE tenant_id = $1`, biz); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `SELECT taskiem_discover_open($1)`, biz).Scan(&open); err != nil || open {
			t.Fatalf("%s: open %v (%v)", c.set, open, err)
		}
		_ = tx.Rollback(ctx)
	}
}

// The business's own role writes only its own fields of its settings and
// its listing; removing, restoring and the notice go through definers
// that refuse a business's scope.
func TestTheBusinessCannotRemoveOrRestore(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	biz := listedBusiness(t, d, "Chi Hair")
	refresh(t, d, biz)
	for _, q := range []string{
		`UPDATE discover_settings SET removed_at = NULL WHERE tenant_id = $1`,
		`UPDATE discover_settings SET told_at = now() WHERE tenant_id = $1`,
		`UPDATE discover_index SET hidden = NULL WHERE tenant_id = $1`,
		`UPDATE discover_index SET reporters = 0 WHERE tenant_id = $1`,
		`SELECT taskiem_discover_moderate($1, true, '', 'user:me')`,
		`SELECT taskiem_discover_moderate($1, false, 'gone', 'user:me')`,
	} {
		err := db.InTenantTx(ctx, d.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, q, biz)
			return err
		})
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Errorf("%s from the business's scope: %v", q, err)
		}
	}
	// Its own fields it may.
	off := false
	if err := db.InTenantTx(ctx, d.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
		_, err := SaveTx(ctx, tx, biz, SettingsIn{Listed: &off}, "user:owner", "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// Two replicas sweeping the same marks at once write each listing once:
// marks are claimed with FOR UPDATE SKIP LOCKED.
func TestSweepOnTwoReplicas(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	var all []uuid.UUID
	for range 12 {
		all = append(all, listedBusiness(t, d, "Shop"))
	}
	var mu sync.Mutex
	writes := map[uuid.UUID]int{}
	afterRead = func(tenant uuid.UUID) error {
		mu.Lock()
		writes[tenant]++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond) // long enough for the other replica to reach it
		return nil
	}
	defer func() { afterRead = nil }()
	a, b := &Sweeper{Pool: d.App}, &Sweeper{Pool: d.App}
	var wg sync.WaitGroup
	counts := make([]int, 2)
	for i, sw := range []*Sweeper{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := sw.Sweep(ctx, 100)
			if err != nil {
				t.Error(err)
			}
			counts[i] = n
		}()
	}
	wg.Wait()
	if counts[0]+counts[1] != len(all) {
		t.Fatalf("the replicas wrote %v listings, want %d in all", counts, len(all))
	}
	for _, id := range all {
		if writes[id] != 1 {
			t.Errorf("%s written %d times", id, writes[id])
		}
		if !public(t, d, id) {
			t.Errorf("%s not public", id)
		}
	}
}

// A business whose listing fails to write goes to the back of the queue,
// so a sweep of one takes the next business, not the same one again.
func TestSweepBacksOffAFailure(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	bad := listedBusiness(t, d, "Broken")
	good := listedBusiness(t, d, "Fine")
	if _, err := d.Admin.Exec(ctx, `DELETE FROM discover_dirty`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO discover_dirty (tenant_id, at) VALUES ($1, now() - interval '2 minutes'), ($2, now() - interval '1 minute')`, bad, good); err != nil {
		t.Fatal(err)
	}
	afterRead = func(tenant uuid.UUID) error {
		if tenant == bad {
			return errors.New("broken")
		}
		return nil
	}
	defer func() { afterRead = nil }()
	sw := &Sweeper{Pool: d.App}
	if _, err := sw.Sweep(ctx, 1); err == nil {
		t.Fatal("the failure was not reported")
	}
	if n, err := sw.Sweep(ctx, 1); err != nil || n != 1 || !public(t, d, good) {
		t.Fatalf("the next sweep did not take the next business: %d %v", n, err)
	}
	var left []uuid.UUID
	rows, _ := d.Admin.Query(ctx, `SELECT tenant_id FROM discover_dirty`)
	left, _ = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if len(left) != 1 || left[0] != bad {
		t.Fatalf("marks left: %v", left)
	}
}

// Rejected photos are never a listing's: the profile's logo, and the
// published page's logo resolved through storefront_media
// (taskiem_business_photo, which a person's Orders use too).
func TestBusinessPhotoLeavesOutRejected(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	biz := listedBusiness(t, d, "Logo Shop")
	photo := func() string {
		t.Helper()
		var urls *string
		if err := db.InTenantTx(ctx, d.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT taskiem_business_photo($1)::text`, biz).Scan(&urls)
		}); err != nil {
			t.Fatal(err)
		}
		if urls == nil {
			return ""
		}
		return *urls
	}
	if photo() == "" {
		t.Fatal("no photo")
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE storefront_media SET moderation = 'rejected' WHERE tenant_id = $1`, biz); err != nil {
		t.Fatal(err)
	}
	if p := photo(); p != "" {
		t.Fatalf("a rejected logo: %s", p)
	}
	if st := refresh(t, d, biz); st.Shown || len(st.Missing) != 1 || st.Missing[0] != MissingPhoto {
		t.Fatalf("a listing whose only photo was rejected: %+v", st)
	}
}

// A search over many listings stays fast: what may be shown is a plain
// column of the index (no per-row function, no count over the reports),
// and searches with no words, a category, an area or a place use the
// index's own indexes. Paging stops at MaxOffset.
func TestSearchAtScale(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	const n = 2000
	if _, err := d.Admin.Exec(ctx, `
		WITH t AS (INSERT INTO tenants (id, name, plan_id) SELECT gen_random_uuid(), 'Shop ' || g, gen_random_uuid() FROM generate_series(1, $1::int) g
		           RETURNING id, name)
		INSERT INTO discover_index (tenant_id, name, category, area, photo, hours, lat, lng, words, can_chat, has_shop, has_book)
		SELECT t.id, t.name, (ARRAY['food', 'fashion', 'beauty', 'farm'])[1 + abs(hashtext(t.id::text)) % 4],
		       (ARRAY['Yaba', 'Ikeja GRA', 'Lekki Phase 1', 'Surulere'])[1 + abs(hashtext(t.name)) % 4],
		       '{"512": "x"}', '{"days": [[{"open": "00:00", "close": "23:59"}], [], [], [], [], [], []]}',
		       CASE WHEN abs(hashtext(t.name)) % 2 = 0 THEN 6.4 + (abs(hashtext(t.id::text)) % 300) / 1000.0 END,
		       CASE WHEN abs(hashtext(t.name)) % 2 = 0 THEN 3.3 + (abs(hashtext(t.id::text || 'x')) % 300) / 1000.0 END,
		       lower(t.name) || ' cakes jollof rice dress', true, false, false
		  FROM t`, n); err != nil {
		t.Fatal(err)
	}
	// A tenth turned off by their owners: never found.
	if _, err := d.Admin.Exec(ctx, `INSERT INTO discover_settings (tenant_id, listed, updated_by)
		SELECT tenant_id, false, 'test' FROM discover_index WHERE abs(hashtext(tenant_id::text)) % 10 = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `ANALYZE discover_index`); err != nil {
		t.Fatal(err)
	}
	var def string
	if err := d.Admin.QueryRow(ctx, `SELECT pg_get_functiondef('taskiem_discover_search(text, text, text, double precision, double precision, int, int)'::regprocedure)`).
		Scan(&def); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "d.hidden IS NULL") || strings.Contains(def, "taskiem_discover_open") || strings.Contains(def, "page_reports") {
		t.Fatal("the search does not filter on the index's state column alone")
	}
	var off int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM discover_index WHERE hidden = 'off'`).Scan(&off); err != nil || off == 0 {
		t.Fatalf("turned off: %d %v", off, err)
	}
	for _, q := range []Query{
		{},
		{Category: "food"},
		{Area: "ikeja"},
		{Words: "jollof"},
		{Words: "cakes", Category: "beauty", Area: "lekki"},
		{Near: &Location{Lat: 6.5, Lng: 3.35}},
		{Near: &Location{Lat: 6.5, Lng: 3.35}, Category: "farm", Offset: 40},
	} {
		best := time.Hour
		var got []Result
		for range 3 {
			start := time.Now()
			var err error
			got, _, err = Search(ctx, d.Admin, q, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			best = min(best, time.Since(start))
		}
		if len(got) != PageSize {
			t.Errorf("%+v: %d results", q, len(got))
		}
		if best > 150*time.Millisecond {
			t.Errorf("%+v took %v", q, best)
		}
		t.Logf("%+v: %v", q, best)
		for _, r := range got {
			var hidden *string
			if err := d.Admin.QueryRow(ctx, `SELECT hidden FROM discover_index WHERE tenant_id = $1`, r.Business).Scan(&hidden); err != nil || hidden != nil {
				t.Fatalf("%+v found a hidden listing (%v)", q, hidden)
			}
		}
	}
	// Near first: in order of km, those with a place before the rest.
	got, _, err := Search(ctx, d.Admin, Query{Near: &Location{Lat: 6.5, Lng: 3.35}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Km == nil || *got[i].Km < *got[i-1].Km {
			t.Fatalf("not nearest first at %d", i)
		}
	}
	// Paging: more while the next page is within 200.
	for _, c := range []struct {
		offset int
		more   bool
	}{{0, true}, {180, true}, {200, false}} {
		if _, more, err := Search(ctx, d.Admin, Query{Offset: c.offset}, time.Now()); err != nil || more != c.more {
			t.Errorf("offset %d: more %v (%v)", c.offset, more, err)
		}
	}
}
