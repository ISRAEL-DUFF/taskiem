package publicweb_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/publicweb"
)

func TestHostLookup(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	tn := d.SeedTenant(t, nil)
	site := uuid.New()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO public_hosts (host, tenant_id, kind, site_id, verified_at) VALUES
		('ada-kitchen.shops.test', $1, 'storefront', $2, now()),
		('pending.shops.test', $1, 'storefront', $2, NULL),
		('paused.shops.test', $1, 'form', $2, now())`, tn.ID, site); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE public_hosts SET status = 'suspended' WHERE host = 'paused.shops.test'`); err != nil {
		t.Fatal(err)
	}
	lookup := publicweb.DBLookup(d.PublicPool(t, 2))

	s, ok, err := lookup(ctx, "Ada-Kitchen.Shops.Test")
	if err != nil || !ok || s.Tenant != tn.ID || s.ID != site || s.Kind != "storefront" || s.Suspended {
		t.Fatalf("verified host: %+v %v %v", s, ok, err)
	}
	if _, ok, err := lookup(ctx, "pending.shops.test"); err != nil || ok {
		t.Fatalf("an unverified host must not serve: %v %v", ok, err)
	}
	if _, ok, err := lookup(ctx, "nobody.shops.test"); err != nil || ok {
		t.Fatalf("unknown host: %v %v", ok, err)
	}
	if s, ok, _ := lookup(ctx, "paused.shops.test"); !ok || !s.Suspended {
		t.Fatalf("suspended host: %+v %v", s, ok)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET status = 'suspended' WHERE id = $1`, tn.ID); err != nil {
		t.Fatal(err)
	}
	if s, ok, _ := lookup(ctx, "ada-kitchen.shops.test"); !ok || !s.Suspended {
		t.Fatalf("a suspended tenant's host must answer as suspended: %+v %v", s, ok)
	}
}

// TestPublicRoleReachesNothingElse: taskiem_public reads no table and may
// execute no privileged function but its own. A function Postgres lets
// PUBLIC execute by default would otherwise be open to it. Trigger
// functions are left out: Postgres refuses to call them directly.
func TestPublicRoleReachesNothingElse(t *testing.T) {
	d := dbtest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tn := d.SeedTenant(t, nil)
	pub := d.PublicPool(t, 2)

	for _, q := range []string{`SELECT count(*) FROM tenants`, `SELECT count(*) FROM public_hosts`, `SELECT count(*) FROM runs`, `SELECT count(*) FROM users`} {
		var n int
		if err := pub.QueryRow(ctx, q).Scan(&n); err == nil {
			t.Errorf("taskiem_public ran %q", q)
		}
	}
	if _, err := pub.Exec(ctx, `INSERT INTO public_hosts (host, tenant_id, kind, site_id, verified_at) VALUES ('x.shops.test', $1, 'form', $1, now())`, tn.ID); err == nil {
		t.Error("taskiem_public wrote a host")
	}

	var tables []string
	rows, err := d.Admin.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm')
		  AND (has_table_privilege('taskiem_public', c.oid, 'SELECT') OR has_table_privilege('taskiem_public', c.oid, 'INSERT')
		    OR has_table_privilege('taskiem_public', c.oid, 'UPDATE') OR has_table_privilege('taskiem_public', c.oid, 'DELETE'))`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	if len(tables) > 0 {
		t.Errorf("taskiem_public has privileges on %v", tables)
	}

	var definers []string
	rows, err = d.Admin.Query(ctx, `SELECT p.oid::regprocedure::text FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND p.prosecdef AND p.prorettype <> 'trigger'::regtype
		  AND has_function_privilege('taskiem_public', p.oid, 'EXECUTE')
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		definers = append(definers, n)
	}
	rows.Close()
	allowed := map[string]bool{"taskiem_public_host(text)": true, "taskiem_public_embed_origins(uuid)": true,
		"taskiem_public_storefront(uuid)": true, "taskiem_public_storefront_catalogue(uuid)": true,
		"taskiem_public_submit(uuid,text,text,bytea,bytea,bytea)": true, "taskiem_public_order_status(text,bytea)": true,
		// Bookings (S4): the services and free time, holding a slot, a booking's status and the customer's changes.
		"taskiem_public_storefront_services(uuid)": true, "taskiem_public_busy(uuid,timestamp with time zone,timestamp with time zone)": true,
		"taskiem_public_book(uuid,text,bytea,bytea,bytea,text,timestamp with time zone,timestamp with time zone,integer)": true,
		"taskiem_public_booking(text,bytea)": true,
		"taskiem_public_change(text,bytea,text,integer,timestamp with time zone,timestamp with time zone,timestamp with time zone,integer)": true}
	for _, f := range definers {
		if !allowed[f] {
			t.Errorf("taskiem_public may execute the privileged function %s", f)
		}
	}
}
