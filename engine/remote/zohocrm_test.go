package remote_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/zohocrm"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/remote"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A Zoho CRM watch (K6) is made by the reconciler with an access token it
// refreshes and stores on the way (RemoteCall.Rotate), and renewed before
// its week runs out.
func TestZohoCRMWatchIsMadeAndRenewed(t *testing.T) {
	e := rt.New(t)
	var mu sync.Mutex
	issued, patches := 0, 0
	var expiry, channel string
	zoho := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/oauth/v2/token":
			issued++
			_, _ = io.WriteString(w, `{"access_token":"1000.fresh","expires_in":3600,"api_domain":"https://www.zohoapis.com"}`)
		case r.Header.Get("Authorization") != "Zoho-oauthtoken 1000.fresh":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"INVALID_TOKEN","message":"invalid oauth token","status":"error"}`)
		case r.URL.Path == "/actions/watch" && r.Method == http.MethodGet && channel == "":
			_, _ = io.WriteString(w, `{"watch":[]}`)
		case r.URL.Path == "/actions/watch" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"watch": []any{map[string]any{"channel_id": json.Number(channel), "events": []any{map[string]any{"channel_expiry": expiry}}}}})
		case r.URL.Path == "/actions/watch":
			var body struct {
				Watch []map[string]any `json:"watch"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			expiry, _ = body.Watch[0]["channel_expiry"].(string)
			channel, _ = body.Watch[0]["channel_id"].(string)
			if r.Method == http.MethodPatch {
				patches++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"watch": []any{map[string]any{"code": "SUCCESS", "status": "success", "details": map[string]any{"events": []any{map[string]any{"channel_expiry": expiry}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer zoho.Close()
	c := zohocrm.New(zohocrm.Options{BaseURL: zoho.URL, AccountsServer: zoho.URL})
	if err := e.Registry.Register(c); err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, e.Tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{"refresh_token": "1000.r", "client_id": "1000.c", "client_secret": "s", "accounts_server": "https://accounts.zoho.com", "api_domain": "https://www.zohoapis.com",
		"access_token": "1000.expired", "access_expires_at": "2020-01-01T00:00:00Z"}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "zoho_crm", "main", "oauth2", creds, "test"); err != nil {
		t.Fatal(err)
	}
	doc := `{"schema":"wd/v1","id":"wf_deals","version":1,"name":"deals","trigger":{"type":"connector_event","config":{"connector":"zoho_crm@1","trigger":"record_changes","events":["update"],"options":{"module":"Deals"}}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.item.id"}}]}`
	wf := e.Publish(t, doc)
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(ctx, tx, e.Tenant, wf, []string{"prod"}, 1, def, e.Registry, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	rec := &remote.Reconciler{Pool: e.DB.App, Vault: e.Vault, Registry: e.Registry, Egress: e.Egress, HooksURL: "https://hooks.example/hooks"}
	if _, err := rec.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	var expires *time.Time
	if err := e.DB.Admin.QueryRow(ctx, `SELECT COALESCE(remote_status, state), expires_at FROM remote_subscriptions WHERE workflow_id = $1`, wf).Scan(&status, &expires); err != nil {
		t.Fatal(err)
	}
	if status != "healthy" || expires == nil || expires.Before(time.Now().Add(5*24*time.Hour)) {
		t.Fatalf("after create: %s, expires %v", status, expires)
	}
	stored, err := e.Vault.Credentials(ctx, e.Tenant, "prod", "zoho_crm", "main")
	if err != nil || stored["access_token"] != "1000.fresh" || issued != 1 {
		t.Fatalf("the refreshed token was not kept: %v (%d issued) %v", stored["access_token"], issued, err)
	}
	// Due for renewal: applied again, which renews it.
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE remote_subscriptions SET next_attempt_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if patches != 1 || !strings.HasPrefix(expiry, time.Now().UTC().Add(6*24*time.Hour).Format("2006-01-02")) || issued != 1 {
		t.Errorf("renewals %d, expiry %s, tokens %d", patches, expiry, issued)
	}
}
