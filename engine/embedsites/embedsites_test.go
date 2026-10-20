package embedsites

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

func TestNormaliseOrigin(t *testing.T) {
	platform := []string{"app.taskiem.com", "shops.taskiem.com"}
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"adakitchen.ng", "https://adakitchen.ng", true},
		{"https://www.AdaKitchen.ng/shop?x=1", "https://www.adakitchen.ng", true},
		{"https://adakitchen.ng:443", "https://adakitchen.ng", true},
		{" adakitchen.com.ng ", "https://adakitchen.com.ng", true},
		{"http://adakitchen.ng", "", false},
		{"https://adakitchen.ng:8443", "", false},
		{"https://user:pw@adakitchen.ng", "", false},
		{"https://102.89.1.2", "", false},
		{"https://localhost", "", false},
		{"https://xn--80ak6aa92e.com", "", false},
		{"https://gtbank-support.com", "", false},
		{"https://ada.shops.taskiem.com", "", false},
		{"https://app.taskiem.com", "", false},
		{"https://firs.gov.ng", "", false},
		{"", "", false},
	} {
		got, err := NormaliseOrigin(c.in, platform)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("NormaliseOrigin(%q) = %q, %v", c.in, got, err)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v is not ErrInvalid", c.in, err)
		}
	}
}

func TestProveDNS(t *testing.T) {
	site := Site{Origin: "https://adakitchen.ng", Method: DNS, Token: NewToken()}
	for name, c := range map[string]struct {
		records      []string
		err          error
		ok, inconcl  bool
		wantErrInMsg string
	}{
		"holds it":      {records: []string{"v=spf1 -all", site.Token}, ok: true},
		"another value": {records: []string{"taskiem-site-verify=other"}, wantErrInMsg: "does not hold"},
		"no record":     {err: &net.DNSError{Err: "no such host", IsNotFound: true}, wantErrInMsg: "no TXT record"},
		"timeout":       {err: &net.DNSError{Err: "i/o timeout", IsTimeout: true}, inconcl: true},
	} {
		var asked string
		p := &Prover{LookupTXT: func(_ context.Context, name string) ([]string, error) { asked = name; return c.records, c.err }}
		ok, inconcl, err := p.Prove(context.Background(), site)
		if ok != c.ok || inconcl != c.inconcl || (c.wantErrInMsg != "" && (err == nil || !strings.Contains(err.Error(), c.wantErrInMsg))) {
			t.Errorf("%s: ok=%v inconclusive=%v err=%v", name, ok, inconcl, err)
		}
		if asked != "_taskiem-verify.adakitchen.ng" {
			t.Errorf("%s: looked up %q", name, asked)
		}
	}
}

func TestProveFile(t *testing.T) {
	token := NewToken()
	status, body := 200, "# Taskiem\n"+token+"\n"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != WellKnownPath {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	p := &Prover{Client: srv.Client()}
	site := Site{Origin: srv.URL, Method: File, Token: token}
	if ok, _, err := p.Prove(context.Background(), site); !ok {
		t.Fatalf("holds it: %v", err)
	}
	body = "taskiem-site-verify=someone-elses"
	if ok, inconcl, err := p.Prove(context.Background(), site); ok || inconcl || err == nil {
		t.Fatal("another value")
	}
	status = 404
	if ok, inconcl, _ := p.Prove(context.Background(), site); ok || inconcl {
		t.Fatal("a 404 is a failed proof")
	}
	status = 503
	if ok, inconcl, _ := p.Prove(context.Background(), site); ok || !inconcl {
		t.Fatal("a 503 says nothing")
	}
	if !strings.HasPrefix(site.Instructions()["file_url"], srv.URL+WellKnownPath) {
		t.Fatal(site.Instructions())
	}
}

type fakeProofs struct {
	ok, inconcl bool
	calls       int
}

func (f *fakeProofs) Prove(context.Context, Site) (bool, bool, error) {
	f.calls++
	if f.ok {
		return true, false, nil
	}
	return false, f.inconcl, errors.New("the TXT record does not hold the value")
}

func scoped(t *testing.T, d *dbtest.DB, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	t.Helper()
	return db.InTenantTx(context.Background(), d.App, []uuid.UUID{tenant}, fn)
}

func TestSitesInTheDatabase(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	t1, t2 := d.SeedTenant(t, nil).ID, d.SeedTenant(t, nil).ID
	var site Site
	if err := scoped(t, d, t1, func(tx pgx.Tx) error {
		var err error
		site, err = Add(ctx, tx, t1, "https://adakitchen.ng", DNS, "user:ada")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := scoped(t, d, t1, func(tx pgx.Tx) error {
		_, err := Add(ctx, tx, t1, "https://adakitchen.ng", File, "user:ada")
		return err
	}); !errors.Is(err, ErrExists) {
		t.Fatalf("a site twice: %v", err)
	}
	// Another tenant may claim the same site; each proves it for itself.
	if err := scoped(t, d, t2, func(tx pgx.Tx) error {
		_, err := Add(ctx, tx, t2, "https://adakitchen.ng", DNS, "user:bob")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := scoped(t, d, t2, func(tx pgx.Tx) error {
		_, err := Get(ctx, tx, t2, site.ID)
		return err
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant's site: %v", err)
	}
	if err := scoped(t, d, t1, func(tx pgx.Tx) error {
		for i := range MaxSites - 1 {
			if _, err := Add(ctx, tx, t1, fmt.Sprintf("https://site%d.ng", i), DNS, "user:ada"); err != nil {
				return err
			}
		}
		_, err := Add(ctx, tx, t1, "https://onemore.ng", DNS, "user:ada")
		return err
	}); !errors.Is(err, ErrLimit) {
		t.Fatalf("past the limit: %v", err)
	}

	// Only verified sites of an active tenant reach the public host.
	pub := d.PublicPool(t, 1)
	origins := func(tenant uuid.UUID) []string {
		var o []string
		if err := pub.QueryRow(ctx, `SELECT taskiem_public_embed_origins($1)`, tenant).Scan(&o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	if o := origins(t1); len(o) != 0 {
		t.Fatalf("unverified sites reached the public host: %v", o)
	}
	if err := scoped(t, d, t1, func(tx pgx.Tx) error { return MarkVerified(ctx, tx, t1, site.ID) }); err != nil {
		t.Fatal(err)
	}
	if o := origins(t1); len(o) != 1 || o[0] != "https://adakitchen.ng" {
		t.Fatalf("%v", o)
	}
	if o := origins(t2); len(o) != 0 {
		t.Fatalf("one tenant's proof served another: %v", o)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET status = 'suspended' WHERE id = $1`, t1); err != nil {
		t.Fatal(err)
	}
	if o := origins(t1); len(o) != 0 {
		t.Fatalf("a suspended tenant's sites: %v", o)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET status = 'active' WHERE id = $1`, t1); err != nil {
		t.Fatal(err)
	}
	if err := scoped(t, d, t1, func(tx pgx.Tx) error {
		_, err := Remove(ctx, tx, t1, site.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if o := origins(t1); len(o) != 0 {
		t.Fatalf("a removed site: %v", o)
	}
}

func TestCheckerUnverifiesAfterGrace(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	tenant := d.SeedTenant(t, nil).ID
	var site Site
	if err := scoped(t, d, tenant, func(tx pgx.Tx) error {
		var err error
		if site, err = Add(ctx, tx, tenant, "https://adakitchen.ng", DNS, "user:ada"); err != nil {
			return err
		}
		return MarkVerified(ctx, tx, tenant, site.ID)
	}); err != nil {
		t.Fatal(err)
	}
	proofs := &fakeProofs{}
	now := time.Now()
	c := &Checker{Pool: d.App, Prover: proofs, Now: func() time.Time { return now }}
	verified := func() bool {
		var v *time.Time
		if err := d.Admin.QueryRow(ctx, `SELECT verified_at FROM embed_sites WHERE id = $1`, site.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v != nil
	}
	tick := func(advance time.Duration) {
		t.Helper()
		now = now.Add(advance)
		if err := c.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tick(time.Hour) // not due yet
	if proofs.calls != 0 {
		t.Fatal("checked before it was due")
	}
	// Inconclusive checks are not counted.
	proofs.inconcl = true
	for range 6 {
		tick(7 * time.Hour)
	}
	if !verified() {
		t.Fatal("unverified by checks that said nothing")
	}
	// Four failures in a row, but within the grace period, keep it.
	proofs.inconcl = false
	for range 4 {
		tick(time.Minute)
		_, _ = d.Admin.Exec(ctx, `UPDATE embed_sites SET checked_at = $2 WHERE id = $1`, site.ID, now.Add(-7*time.Hour))
	}
	if !verified() {
		t.Fatal("unverified within the grace period")
	}
	// A success resets it.
	proofs.ok = true
	tick(7 * time.Hour)
	var failures int
	_ = d.Admin.QueryRow(ctx, `SELECT check_failures FROM embed_sites WHERE id = $1`, site.ID).Scan(&failures)
	if failures != 0 || !verified() {
		t.Fatalf("after a success: %d", failures)
	}
	// Failing for more than a day, four times in a row: unverified, audited.
	proofs.ok = false
	for range 5 {
		tick(7 * time.Hour)
	}
	if verified() {
		t.Fatal("still verified after failing for over a day")
	}
	var n int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'embed_site.unverified'`, tenant).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit %d %v", n, err)
	}
}
