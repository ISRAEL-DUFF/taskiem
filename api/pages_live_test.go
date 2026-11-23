package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// openStream opens a server-sent events path as a client and sends what it
// hears until the test is done.
func openStream(t *testing.T, c *client, path string) (<-chan streamEvent, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("%s: %d %s", path, resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	events := make(chan streamEvent, 200)
	go readStream(resp.Body, events)
	return events, cancel
}

// pageLive opens a page's live notices as a client.
func pageLive(t *testing.T, c *client, name string) (<-chan streamEvent, func()) {
	t.Helper()
	return openStream(t, c, "/v1/pages/"+name+"/live")
}

// Pages kept up to date (§9): a row written to a table tells the open page
// that reads it, naming its parts and no data; an unrelated page and
// another business are told nothing; an order changed tells the page over
// orders; a burst is told once.
func TestPagesLive(t *testing.T) {
	w := recordsWorld(t)
	w.srv.PageLiveEvery = 400 * time.Millisecond
	owner := w.tenant(t, "Acme", "owner@acme.test")
	other := w.tenant(t, "Other", "owner@other.test")
	tbl := owner.must(201, "POST", "/v1/tables", ticketsTable)["id"].(string)
	owner.must(201, "POST", "/v1/tables", map[string]any{"name": "stock", "title": "Stock", "columns": []map[string]any{{"name": "item", "type": "text"}}})
	otherTbl := other.must(201, "POST", "/v1/tables", ticketsTable)["id"].(string)
	addOrder(t, w, owner, "AC-1", "received", "Bola Ade", "+2348031234567", 150000)
	addOrder(t, w, other, "OT-1", "received", "Dele", "+2348030000000", 7700000)

	page := func(c *client, name string, source map[string]any) {
		c.must(201, "POST", "/v1/pages", map[string]any{"document": map[string]any{"page": "page/v1", "name": name, "title": name,
			"blocks": []any{map[string]any{"id": "n", "type": "figure", "label": "How many", "value": "count", "source": source},
				map[string]any{"id": "h", "type": "heading", "title": "Hi"}}}})
		c.must(200, "POST", "/v1/pages/"+name+"/publish", nil)
	}
	page(owner, "desk", map[string]any{"table": "tickets"})
	page(owner, "stock-desk", map[string]any{"table": "stock"})
	page(owner, "orders-desk", map[string]any{"records": "orders"})
	page(other, "desk", map[string]any{"table": "tickets"})
	page(other, "orders-desk", map[string]any{"records": "orders"})

	desk, stopDesk := pageLive(t, owner, "desk")
	defer stopDesk()
	stock, stopStock := pageLive(t, owner, "stock-desk")
	defer stopStock()
	ords, stopOrds := pageLive(t, owner, "orders-desk")
	defer stopOrds()
	otherDesk, stopOther := pageLive(t, other, "desk")
	defer stopOther()
	otherOrds, stopOtherOrds := pageLive(t, other, "orders-desk")
	defer stopOtherOrds()
	for _, s := range []<-chan streamEvent{desk, stock, ords, otherDesk, otherOrds} {
		if ev := next(s, "ready", 5*time.Second, nil); ev == nil {
			t.Fatal("no ready")
		}
	}

	// A burst of rows written to tickets: one notice, for the part that
	// reads it, with no data.
	for i := 0; i < 4; i++ {
		owner.must(201, "POST", "/v1/tables/"+tbl+"/rows", map[string]any{"rows": []map[string]any{{"title": fmt.Sprintf("T%d", i), "phone": "2348012345678"}}})
	}
	ev := next(desk, "changed", 5*time.Second, nil)
	if ev == nil || ev.Data["page"] != "desk" || fmt.Sprint(ev.Data["blocks"]) != "[n]" {
		t.Fatalf("desk: %+v", ev)
	}
	if strings.Contains(mustString(t, ev.Data), "T0") || strings.Contains(mustString(t, ev.Data), "2348012345678") {
		t.Errorf("a notice carried data: %v", ev.Data)
	}
	// Debounced: the burst is at most one more notice, never one a row.
	extra := 0
	for next(desk, "changed", 1500*time.Millisecond, nil) != nil {
		extra++
	}
	if extra > 1 {
		t.Errorf("a burst of 4 rows told %d more times", extra)
	}
	if ev := next(stock, "changed", 800*time.Millisecond, nil); ev != nil {
		t.Errorf("an unrelated page was told: %+v", ev)
	}
	if ev := next(otherDesk, "changed", 10*time.Millisecond, nil); ev != nil {
		t.Errorf("another business was told: %+v", ev)
	}

	// An order changed: the orders page is told; the other business's is not.
	setOrderStatus(t, w, owner, "AC-1", "accepted")
	if ev := next(ords, "changed", 5*time.Second, nil); ev == nil || fmt.Sprint(ev.Data["blocks"]) != "[n]" {
		t.Fatalf("orders: %+v", ev)
	}
	if ev := next(otherOrds, "changed", 800*time.Millisecond, nil); ev != nil {
		t.Errorf("another business's orders page was told: %+v", ev)
	}
	// The other business's own row: its page alone.
	other.must(201, "POST", "/v1/tables/"+otherTbl+"/rows", map[string]any{"rows": []map[string]any{{"title": "Theirs"}}})
	if ev := next(otherDesk, "changed", 5*time.Second, nil); ev == nil {
		t.Error("the other business's page was not told of its own row")
	}
	if ev := next(desk, "changed", 800*time.Millisecond, nil); ev != nil {
		t.Errorf("told of another business's row: %+v", ev)
	}

	// Only a page the viewer may open.
	support := addMember(t, w.world, owner, "sam", "support")
	if s, _ := support.do("GET", "/v1/pages/missing/live", nil); s != 404 {
		t.Errorf("no such page: %d", s)
	}
	doc := owner.must(200, "GET", "/v1/pages/desk", nil)["document"].(map[string]any)
	doc["roles"] = []any{"manager"}
	owner.must(200, "PUT", "/v1/pages/desk/draft", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/desk/publish", nil)
	if s, _ := support.do("GET", "/v1/pages/desk/live", nil); s != 404 {
		t.Errorf("a page the viewer may not open: %d", s)
	}
	// The page published again: every part told.
	if ev := next(desk, "changed", 5*time.Second, func(e streamEvent) bool { return e.Data["all"] == true }); ev == nil {
		t.Error("the page published again was not told")
	}
}
