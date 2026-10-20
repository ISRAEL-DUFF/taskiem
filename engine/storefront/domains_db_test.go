package storefront_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/embedsites"
	"github.com/israel-duff/taskiem/engine/publicweb"
	"github.com/israel-duff/taskiem/engine/storefront"
)

func TestNormaliseDomain(t *testing.T) {
	platform := []string{"app.taskiem.test", "shops.taskiem.test"}
	for in, want := range map[string][2]string{
		"AdaKitchen.ng":                   {"adakitchen.ng", "www.adakitchen.ng"},
		"https://www.adakitchen.ng/menu":  {"www.adakitchen.ng", "adakitchen.ng"},
		"adakitchen.com.ng":               {"adakitchen.com.ng", "www.adakitchen.com.ng"},
		"shop.example.com":                {"shop.example.com", ""},
		"www.shop.example.com":            {"www.shop.example.com", ""},
		"https://order.adakitchen.ng:443": {"order.adakitchen.ng", ""},
	} {
		host, twin, err := storefront.NormaliseDomain(in, platform)
		if err != nil || host != want[0] || twin != want[1] {
			t.Errorf("%q: %q %q %v", in, host, twin, err)
		}
	}
	for _, bad := range []string{"", "http://adakitchen.ng", "102.89.1.2", "com.ng", "ng", "xn--80ak6aa92e.com", "ada.shops.taskiem.test",
		"app.taskiem.test", "shops.taskiem.test", "gtbank-support.com", "lagosstate.gov.ng", "adakitchen.ng:8443"} {
		if _, _, err := storefront.NormaliseDomain(bad, platform); !errors.Is(err, storefront.ErrInvalid) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

func TestDomainRecords(t *testing.T) {
	twin := "www.adakitchen.ng"
	d := storefront.Domain{Host: "adakitchen.ng", Twin: &twin, Token: "taskiem-domain-verify=x"}
	recs := d.Records(storefront.DomainTarget{CNAME: "pages.shops.test", Addresses: []string{"203.0.113.7", "2001:db8::7"}})
	want := []storefront.Record{
		{Type: "TXT", Name: "_taskiem-verify.adakitchen.ng", Value: "taskiem-domain-verify=x", Purpose: "proof"},
		{Type: "A", Name: "adakitchen.ng", Value: "203.0.113.7", Purpose: "point"},
		{Type: "AAAA", Name: "adakitchen.ng", Value: "2001:db8::7", Purpose: "point"},
		{Type: "CNAME", Name: "www.adakitchen.ng", Value: "pages.shops.test", Purpose: "point"},
	}
	if len(recs) != len(want) {
		t.Fatalf("%+v", recs)
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, recs[i], want[i])
		}
	}
	// www. first: the proof is still at the registrable domain.
	apex := "adakitchen.ng"
	w := storefront.Domain{Host: "www.adakitchen.ng", Twin: &apex, Token: "v"}
	if w.ProofHost() != "adakitchen.ng" || w.Site().Host() != "adakitchen.ng" {
		t.Fatalf("%s", w.ProofHost())
	}
	// Without addresses, the apex is an ALIAS to the CNAME target.
	recs = w.Records(storefront.DomainTarget{CNAME: "pages.shops.test"})
	if len(recs) != 3 || recs[1].Type != "CNAME" || recs[2].Type != "ALIAS" || recs[2].Name != "adakitchen.ng" {
		t.Fatalf("%+v", recs)
	}
}

type fakeDomainProofs struct {
	ok, inconcl bool
	calls       int
}

func (f *fakeDomainProofs) Prove(context.Context, embedsites.Site) (bool, bool, error) {
	f.calls++
	if f.ok {
		return true, false, nil
	}
	return false, f.inconcl, errors.New("the TXT record does not hold the value")
}

func publishedShop(t *testing.T, d *dbtest.DB, tenant uuid.UUID, slug string) storefront.Storefront {
	t.Helper()
	var sf storefront.Storefront
	if err := db.InTenantTx(context.Background(), d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		ctx := context.Background()
		if _, err := storefront.Save(ctx, tx, tenant, slug, storefront.Card{Name: "Shop " + slug}, nil, storefront.Columns{}, "user:o"); err != nil {
			return err
		}
		var err error
		sf, err = storefront.Publish(ctx, tx, tenant, "shops.test", "user:o")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return sf
}

func TestDomainsInTheDatabase(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	t1, t2 := d.SeedTenant(t, nil).ID, d.SeedTenant(t, nil).ID
	in := func(tenant uuid.UUID, fn func(pgx.Tx) error) error {
		return db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, fn)
	}
	// A domain needs a storefront.
	if err := in(t1, func(tx pgx.Tx) error {
		_, err := storefront.AddDomain(ctx, tx, t1, "adakitchen.ng", "www.adakitchen.ng", "user:ada")
		return err
	}); !errors.Is(err, storefront.ErrNotFound) {
		t.Fatalf("no storefront: %v", err)
	}
	sf1 := publishedShop(t, d, t1, "ada-kitchen")
	sf2 := publishedShop(t, d, t2, "ada-copy")
	var mine, theirs storefront.Domain
	if err := in(t1, func(tx pgx.Tx) (err error) {
		mine, err = storefront.AddDomain(ctx, tx, t1, "adakitchen.ng", "www.adakitchen.ng", "user:ada")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := in(t1, func(tx pgx.Tx) error {
		_, err := storefront.AddDomain(ctx, tx, t1, "www.adakitchen.ng", "adakitchen.ng", "user:ada")
		return err
	}); !errors.Is(err, storefront.ErrDomainExists) {
		t.Fatalf("the twin again: %v", err)
	}
	// Another organisation may add it too; the first to prove it serves it.
	if err := in(t2, func(tx pgx.Tx) (err error) {
		theirs, err = storefront.AddDomain(ctx, tx, t2, "adakitchen.ng", "www.adakitchen.ng", "user:bob")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	lookup := publicweb.DBLookup(d.PublicPool(t, 2))
	site := func(host string) (publicweb.Site, bool) {
		t.Helper()
		s, ok, err := lookup(ctx, host)
		if err != nil {
			t.Fatal(err)
		}
		return s, ok
	}
	if _, ok := site("adakitchen.ng"); ok {
		t.Fatal("an unproven domain serves")
	}
	if s, ok := site("ada-kitchen.shops.test"); !ok || s.Primary != "" {
		t.Fatalf("preview before any domain: %+v", s)
	}
	if err := in(t1, func(tx pgx.Tx) error {
		v, err := storefront.VerifyDomain(ctx, tx, t1, mine.ID)
		if err == nil && (!v.Primary || v.VerifiedAt == nil) {
			t.Errorf("the first verified domain is the primary: %+v", v)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := in(t2, func(tx pgx.Tx) error {
		_, err := storefront.VerifyDomain(ctx, tx, t2, theirs.ID)
		return err
	}); !errors.Is(err, storefront.ErrDomainTaken) {
		t.Fatalf("a domain another page serves: %v", err)
	}
	for _, h := range []string{"adakitchen.ng", "www.adakitchen.ng", "ada-kitchen.shops.test"} {
		if s, ok := site(h); !ok || s.ID != sf1.ID || s.Primary != "adakitchen.ng" {
			t.Errorf("%s: %+v %v", h, s, ok)
		}
	}
	if s, ok := site("ada-copy.shops.test"); !ok || s.ID != sf2.ID || s.Primary != "" {
		t.Errorf("the other page: %+v", s)
	}
	var got storefront.Storefront
	if err := in(t1, func(tx pgx.Tx) (err error) {
		got, err = storefront.Get(ctx, tx, t1)
		return err
	}); err != nil || got.Domain == nil || *got.Domain != "adakitchen.ng" || got.URL("shops.test") != "https://adakitchen.ng" {
		t.Fatalf("%v %+v", err, got.Domain)
	}

	// A closed page still redirects; a draft (never published) would not
	// be served at all. Suspended tenants: no redirect.
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET status = 'suspended' WHERE id = $1`, t1); err != nil {
		t.Fatal(err)
	}
	if s, ok := site("adakitchen.ng"); !ok || !s.Suspended || s.Primary != "" {
		t.Fatalf("suspended: %+v", s)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET status = 'active' WHERE id = $1`, t1); err != nil {
		t.Fatal(err)
	}

	// Removed: it stops serving, and the other organisation can prove it.
	if err := in(t1, func(tx pgx.Tx) error {
		_, err := storefront.RemoveDomain(ctx, tx, t1, mine.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := site("adakitchen.ng"); ok {
		t.Fatal("a removed domain serves")
	}
	if s, ok := site("ada-kitchen.shops.test"); !ok || s.Primary != "" {
		t.Fatalf("the preview serves again: %+v", s)
	}
	if err := in(t2, func(tx pgx.Tx) error {
		_, err := storefront.VerifyDomain(ctx, tx, t2, theirs.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if s, ok := site("www.adakitchen.ng"); !ok || s.ID != sf2.ID {
		t.Fatalf("taken over: %+v", s)
	}
	// One tenant's release cannot touch another's hosts.
	if err := in(t1, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_storefront_release_domain($1, $2)`, sf2.ID, []string{"adakitchen.ng"})
		return err
	}); err == nil {
		t.Fatal("released another tenant's domain")
	}
	if err := in(t1, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_storefront_claim_domain($1)`, theirs.ID)
		return err
	}); err == nil {
		t.Fatal("claimed another tenant's domain")
	}
}

func TestDomainCheckerUnverifies(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	tenant := d.SeedTenant(t, nil).ID
	publishedShop(t, d, tenant, "ada-kitchen")
	var dom storefront.Domain
	if err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if dom, err = storefront.AddDomain(ctx, tx, tenant, "adakitchen.ng", "www.adakitchen.ng", "user:ada"); err != nil {
			return err
		}
		_, err = storefront.VerifyDomain(ctx, tx, tenant, dom.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	proofs := &fakeDomainProofs{}
	now := time.Now()
	c := &storefront.DomainChecker{Pool: d.App, Prover: proofs, Now: func() time.Time { return now }}
	tick := func(advance time.Duration) {
		t.Helper()
		now = now.Add(advance)
		if err := c.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	served := func() bool {
		var n int
		if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM public_hosts WHERE host IN ('adakitchen.ng', 'www.adakitchen.ng')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 2
	}
	tick(time.Hour)
	if proofs.calls != 0 {
		t.Fatal("checked before it was due")
	}
	proofs.inconcl = true
	for range 6 {
		tick(7 * time.Hour)
	}
	if !served() {
		t.Fatal("unverified by checks that said nothing")
	}
	proofs.inconcl = false
	// Four failures in a row, but within a day (21 hours), keep it.
	for range 4 {
		tick(7 * time.Hour)
	}
	if !served() {
		t.Fatal("unverified within the grace period")
	}
	tick(7 * time.Hour)
	if served() {
		t.Fatal("still served after failing for over a day")
	}
	var unverified *time.Time
	var reason *string
	if err := d.Admin.QueryRow(ctx, `SELECT unverified_at, last_check_error FROM storefront_domains WHERE id = $1`, dom.ID).Scan(&unverified, &reason); err != nil ||
		unverified == nil || reason == nil {
		t.Fatalf("%v %v %v", unverified, reason, err)
	}
	var n int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'storefront_domain.unverified'`, tenant).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit %d %v", n, err)
	}
	if s, ok, err := publicweb.DBLookup(d.PublicPool(t, 1))(ctx, "ada-kitchen.shops.test"); err != nil || !ok || s.Primary != "" {
		t.Fatalf("the preview serves again: %+v %v", s, err)
	}
}
