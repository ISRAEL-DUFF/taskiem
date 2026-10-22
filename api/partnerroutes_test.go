package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/iswallet"
	"github.com/israel-duff/taskiem/engine/db"
)

// TestSharedClientConnections: a partner provisions each customer's
// iswallet connection with that customer's wallets; a shared client's key
// is refused without them; the wallets are indexed for routing and follow
// rotations and deletion; the partner sees where events went.
func TestSharedClientConnections(t *testing.T) {
	w := newWorld(t)
	if err := w.env.Registry.Register(iswallet.New(iswallet.Options{})); err != nil {
		t.Fatal(err)
	}
	p := w.partner(t, "Wagely", "owner@wagely.test")
	acme := p.sub(t, "Acme", nil)
	perms := []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}
	p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{"iswallet"}, "end_user_permissions": perms})
	conns := "/v1/partner/sub-tenants/" + acme + "/connections"
	conn := func(creds map[string]string) map[string]any {
		return map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "main", "credentials": creds}
	}

	// A shared client's key without wallets is refused, before anything is stored.
	out := p.key.must(400, "POST", conns, conn(map[string]string{"api_key": "isw_wagely", "shared_client": "true"}))
	if !strings.Contains(toJSON(out), "WALLET_ALLOW_LIST_MISSING") {
		t.Errorf("refusal: %v", out)
	}
	p.key.must(400, "POST", conns, conn(map[string]string{"api_key": "isw_wagely", "shared_client": "yes", "wallet_ids": "w_acme"}))
	created := p.key.must(201, "POST", conns, conn(map[string]string{"api_key": "isw_wagely", "shared_client": "true", "wallet_ids": "w_acme, w_acme_payroll"}))
	id := created["id"].(string)

	ctx := context.Background()
	keys := func() string {
		t.Helper()
		var ks []string
		rows, err := w.env.DB.Admin.Query(ctx, `SELECT route_key FROM connection_route_keys WHERE connection_id = $1 AND tenant_id = $2 AND field = 'wallet_ids' ORDER BY route_key`, id, acme)
		if err != nil {
			t.Fatal(err)
		}
		if ks, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			t.Fatal(err)
		}
		return strings.Join(ks, ",")
	}
	if got := keys(); got != "w_acme,w_acme_payroll" {
		t.Errorf("indexed wallets %q", got)
	}
	// A rotation that drops the list is refused; one that changes it re-indexes.
	p.key.must(400, "PUT", conns+"/"+id+"/credentials", map[string]any{"credentials": map[string]string{"api_key": "isw_new", "shared_client": "true"}})
	p.key.must(200, "PUT", conns+"/"+id+"/credentials", map[string]any{"credentials": map[string]string{"api_key": "isw_new", "shared_client": "true", "wallet_ids": "w_acme2"}})
	if got := keys(); got != "w_acme2" {
		t.Errorf("after rotation %q", got)
	}

	// Where events went: the partner reads its own record.
	pid := p.id
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{pid}, func(tx pgx.Tx) error {
		for _, k := range []string{"w_acme2", "w_stray"} {
			if _, err := tx.Exec(ctx, `SELECT * FROM taskiem_partner_route_event('prod', 'iswallet@1', 'outflow_event', 'wallet_ids', $1, 'wallet.outflow.confirmed', $2)`, k, "evt_"+k); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	routes := p.key.must(200, "GET", "/v1/partner/event-routes", nil)["routes"].([]any)
	if len(routes) != 2 {
		t.Fatalf("routes %v", routes)
	}
	unrouted := p.key.must(200, "GET", "/v1/partner/event-routes?status=unrouted", nil)["routes"].([]any)
	if len(unrouted) != 1 || unrouted[0].(map[string]any)["route_key"] != "w_stray" || unrouted[0].(map[string]any)["sub_tenant_id"] != nil {
		t.Errorf("unrouted %v", unrouted)
	}
	mine := p.key.must(200, "GET", "/v1/partner/event-routes?sub_tenant="+acme, nil)["routes"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["connection"] != "main" {
		t.Errorf("acme's %v", mine)
	}
	p.key.must(400, "GET", "/v1/partner/event-routes?status=lost", nil)

	// Deleting the connection drops its wallets.
	p.key.must(204, "DELETE", conns+"/"+id, nil)
	if got := keys(); got != "" {
		t.Errorf("after delete %q", got)
	}

	// The tenant's own connections take the same checks.
	p.owner.must(400, "POST", "/v1/connections", conn(map[string]string{"api_key": "isw_wagely", "shared_client": "true"}))
	p.owner.must(201, "POST", "/v1/connections", conn(map[string]string{"api_key": "isw_wagely", "webhook_secret": "whsec", "wallet_ids": "w_funding"}))
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM connection_route_keys WHERE tenant_id = $1 AND route_key = 'w_funding'`, pid).Scan(&n); err != nil || n != 1 {
		t.Errorf("own wallets indexed: %d %v", n, err)
	}
}
