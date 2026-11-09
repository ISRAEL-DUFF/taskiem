package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/whatsapp"
)

// The quality watch on an assistant's WhatsApp number (decision 0062 V10,
// C23): read every few hours, kept, and the owners told once when it gets
// worse; a number that stays the same, or gets better, tells no one.
func TestWhatsAppQualityWatch(t *testing.T) {
	var mu sync.Mutex
	health := map[string]any{"quality_rating": "GREEN", "messaging_limit_tier": "TIER_1K", "status": "CONNECTED"}
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]any{"id": "106540352242922", "display_phone_number": "+234 801 000 0000", "verified_name": "Mama Put"}
		for k, v := range health {
			out[k] = v
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(meta.Close)
	w := newWorld(t)
	if err := w.env.Registry.Register(whatsapp.New(whatsapp.Options{BaseURL: meta.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	tenant := uuid.MustParse(tenantOf(t, owner))
	ctx := context.Background()
	if _, err := w.env.Vault.CreateConnection(ctx, tenant, "prod", "whatsapp", "main", "custom",
		map[string]string{"access_token": "EAAtest", "phone_number_id": "106540352242922", "app_secret": "s", "verify_token": "v"}, "test"); err != nil {
		t.Fatal(err)
	}
	publishFlow(t, owner, `{"schema":"wd/v1","id":"wf_assistant","version":1,"name":"Assistant",
	  "trigger":{"type":"connector_event","config":{"connector":"whatsapp@1","trigger":"messages","connection":"main","guard":true}},
	  "steps":[{"id":"t","type":"transform","config":{"output":1}}]}`)
	alerts := func() int {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = 'whatsapp_quality'`, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	check := func() {
		t.Helper()
		if n, err := w.srv.CheckWhatsAppQuality(ctx); err != nil || n != 1 {
			t.Fatalf("read %d numbers: %v", n, err)
		}
	}
	check()
	if alerts() != 0 {
		t.Fatal("a green number told the owners")
	}
	mu.Lock()
	health["quality_rating"] = "YELLOW"
	mu.Unlock()
	check()
	check()
	if alerts() != 1 {
		t.Fatalf("yellow: %d alerts, want one", alerts())
	}
	var rating string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT quality_rating FROM whatsapp_number_health WHERE tenant_id = $1 AND connection = 'main'`, tenant).Scan(&rating); err != nil || rating != "YELLOW" {
		t.Fatalf("kept: %q %v", rating, err)
	}
	mu.Lock()
	health["quality_rating"], health["messaging_limit_tier"] = "GREEN", "TIER_250"
	mu.Unlock()
	check()
	if alerts() != 2 {
		t.Fatalf("a lower limit: %d alerts, want two", alerts())
	}
	mu.Lock()
	health["messaging_limit_tier"] = "TIER_1K"
	mu.Unlock()
	check()
	if alerts() != 2 {
		t.Fatalf("better again told the owners: %d", alerts())
	}
}
