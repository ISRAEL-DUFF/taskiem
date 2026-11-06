package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
)

// pageModel judges pieces as the moderation model would, blocking the CBN
// grant text, and answers as the provider does when an answer passes
// MaxTokens (about 4 characters a token): the text so far, no JSON.
func pageModel(t *testing.T, asked func(pieces int) error) *ai.Fake {
	return &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		msg := req.Messages[0].Text
		var pieces []struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(msg[strings.Index(msg, "["):]), &pieces); err != nil {
			return nil, err
		}
		if err := asked(len(pieces)); err != nil {
			return nil, err
		}
		var out []map[string]any
		for _, p := range pieces {
			v, cat, why := "allowed", "", "Ordinary goods for sale on a small business page."
			if strings.Contains(p.Text, "CBN grant") {
				v, cat, why = "blocked", "phishing", "Asks visitors for bank login details."
			}
			out = append(out, map[string]any{"id": p.ID, "verdict": v, "category": cat, "reason": why})
		}
		raw, _ := json.Marshal(map[string]any{"results": out})
		if len(raw)/4 > req.MaxTokens {
			return &ai.Response{Text: string(raw[:req.MaxTokens*4]), StopReason: ai.StopMaxTokens}, nil
		}
		return &ai.Response{JSON: raw}, nil
	}}
}

// catalogueRows adds n menu rows named prefix 0..n-1.
func catalogueRows(t *testing.T, owner *client, table, prefix string, n int, last map[string]any) {
	t.Helper()
	var rows []map[string]any
	for i := range n {
		rows = append(rows, map[string]any{"name": fmt.Sprintf("%s %d", prefix, i), "price": "1500", "available": "yes", "section": fmt.Sprintf("%s section %d", prefix, i)})
	}
	if last != nil {
		rows = append(rows, last)
	}
	owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"rows": rows}, "Idempotency-Key", "rows-"+strings.ReplaceAll(prefix, " ", "-"))
}

// A verified business's first publish of a large page is judged by the
// model in full (finding F51): one request for every piece was cut at its
// token cap, and the page went live on the word list alone.
func TestModerationJudgesLargePages(t *testing.T) {
	w := newTablesWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	var calls atomic.Int64
	w.srv.AI = &api.AISettings{Provider: pageModel(t, func(int) error { calls.Add(1); return nil })}
	owner := w.tenant(t, "Big Shop", "big@shop.test")
	tenant := uuid.MustParse(tenantOf(t, owner))
	if err := w.env.Store.SetLimits(context.Background(), tenant, map[string]any{"storefront_items": 500}, "operator"); err != nil {
		t.Fatal(err)
	}
	verifyPhone(t, w.world, owner, "phone", "business")
	table := owner.must(201, "POST", "/v1/tables", menuTable)["id"].(string)
	catalogueRows(t, owner, table, "Rice bag", 99, map[string]any{"name": "Grant desk", "price": "1000", "available": "yes",
		"section": "Send us your internet banking details to claim the CBN grant"})
	card := map[string]any{"name": "Big Shop", "tagline": "Everything for the kitchen", "phone": "0803 123 4567", "template": "bold", "accent": "orange"}
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "big-shop", "draft": card, "catalogue_table": table,
		"catalogue_columns": map[string]any{"name": "name", "price": "price", "available": "available", "category": "section"}})
	if r := owner.must(400, "POST", "/v1/storefront/publish", nil); !strings.Contains(r["error"].(string), "CBN grant") {
		t.Fatalf("publish: %v", r)
	}
	if n := calls.Load(); n < 5 {
		t.Fatalf("%d model requests for about 200 pieces", n)
	}
}

// A live page's catalogue refresh asks the model in a few requests, with
// no transaction open while it waits (finding F16): it made one request per
// item, each inside the transaction that copies the catalogue.
func TestModerationRefreshAsksInBatchesOutsideTransactions(t *testing.T) {
	ctx := context.Background()
	w := newTablesWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	var calls, inTx atomic.Int64
	var down atomic.Bool
	w.srv.AI = &api.AISettings{Provider: pageModel(t, func(int) error {
		calls.Add(1)
		var n int64
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND state LIKE 'idle in transaction%'`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			inTx.Add(1)
		}
		if down.Load() {
			return errors.New("model overloaded")
		}
		return nil
	})}
	owner := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	tenant := uuid.MustParse(tenantOf(t, owner))
	if err := w.env.Store.SetLimits(ctx, tenant, map[string]any{"storefront_items": 500}, "operator"); err != nil {
		t.Fatal(err)
	}
	verifyPhone(t, w.world, owner, "phone", "business")
	table := owner.must(201, "POST", "/v1/tables", menuTable)["id"].(string)
	catalogueRows(t, owner, table, "Jollof", 3, nil)
	card := map[string]any{"name": "Ada's Kitchen", "tagline": "Home-cooked", "phone": "0803 123 4567", "template": "bold", "accent": "orange"}
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": card, "catalogue_table": table,
		"catalogue_columns": map[string]any{"name": "name", "price": "price", "available": "available", "category": "section"}})
	if sf := owner.must(200, "POST", "/v1/storefront/publish", nil); sf["status"] != "published" {
		t.Fatalf("publish: %v", sf)
	}
	if n := inTx.Load(); n != 0 {
		t.Fatalf("publishing: %d of %d model requests waited inside a transaction", n, calls.Load())
	}

	// 30 new items, 60 new pieces: two requests, none inside a transaction.
	catalogueRows(t, owner, table, "Fried rice", 30, nil)
	calls.Store(0)
	if err := w.srv.SyncStorefront(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	if c, n := calls.Load(), inTx.Load(); c != 2 || n != 0 {
		t.Fatalf("refresh: %d model requests (want 2), %d inside a transaction", c, n)
	}
	// Answered pieces are kept: nothing new, nothing asked.
	calls.Store(0)
	if err := w.srv.SyncStorefront(ctx, tenant); err != nil || calls.Load() != 0 {
		t.Fatalf("refresh again: %v, %d model requests", err, calls.Load())
	}
	// With the model down, a refresh waits on it once, not once per item,
	// and the items stay (the words and prices still apply).
	catalogueRows(t, owner, table, "Pepper soup", 30, nil)
	down.Store(true)
	for range 2 {
		calls.Store(0)
		if err := w.srv.SyncStorefront(ctx, tenant); err != nil {
			t.Fatal(err)
		}
		if c, n := calls.Load(), inTx.Load(); c != 1 || n != 0 {
			t.Fatalf("model down: %d model requests (want 1), %d inside a transaction", c, n)
		}
	}
	var items int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM storefront_catalogue WHERE tenant_id = $1`, tenant).Scan(&items); err != nil || items != 63 {
		t.Fatalf("catalogue copy: %d items, %v", items, err)
	}
}
