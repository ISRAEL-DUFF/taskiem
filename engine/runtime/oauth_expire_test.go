package runtime_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A provider that refuses a connection's token as revoked or expired
// (the connector wraps connector.ErrCredentialsRejected) expires an OAuth
// connection at that very step (decision 0034): runs stop using it and the
// Connections page shows it. Pasted credentials are left as they are.
func TestRejectedTokenExpiresOAuthConnection(t *testing.T) {
	e := rt.New(t)
	man := strings.Replace(strings.Replace(rt.FakepayManifest, "id: fakepay", "id: revpay", 1),
		"auth: { type: none }", "auth: { type: api_key, fields: [{ key: token, label: Token, secret: true }] }", 1)
	rejected := connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
		return connector.Response{}, fmt.Errorf("revpay auth (http 401, invalid_token): %w: %w", effects.ErrFatal, connector.ErrCredentialsRejected)
	})
	conn := e.Provider.Connector()
	conn.Manifest = connector.MustParse([]byte(man))
	conn.Actions["notify"] = rejected
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	for name, authType := range map[string]string{"linked": "oauth2", "pasted": "api_key"} {
		if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "revpay", name, authType, map[string]string{"token": "xb_" + name}, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"linked", "pasted"} {
		wf := e.Publish(t, wfDoc(`{"id":"notify","type":"connector","connector":"revpay@1","action":"notify","connection":"`+name+`","input":{"logical_id":"n1"}}`, ""))
		ref := e.Start(t, wf, map[string]any{})
		e.Drain(t)
		if st := e.Status(t, ref); st != "failed" {
			t.Fatalf("%s: status %s: %s", name, st, types(events(t, e, ref)))
		}
	}
	status := map[string]string{}
	rows, err := e.DB.Admin.Query(ctx, `SELECT name, status FROM connections WHERE tenant_id = $1 AND connector = 'revpay'`, e.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n, s string
		if err := rows.Scan(&n, &s); err != nil {
			t.Fatal(err)
		}
		status[n] = s
	}
	if status["linked"] != "expired" || status["pasted"] != "active" {
		t.Fatalf("statuses %v, want linked expired and pasted active", status)
	}
	var audited int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'connection.expire'`, e.Tenant).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("%d connection.expire audit entries, want 1", audited)
	}
}
