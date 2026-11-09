package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
)

// Chat tools beyond the web (chat_tool_requests.go): "tools" and "/…" on
// WhatsApp answer with links to the tools; a typed request read by the
// model comes back as the tool and its form, filled in, from the web and
// from the chat; a thing two products could be is never guessed, and a
// question is no request.
func TestChatToolRequests(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		said := strings.ToLower(req.Messages[len(req.Messages)-1].Text)
		a := map[string]any{"tool": "none", "thing": "", "price_naira": 0, "count": 0, "name": "", "minutes": 0}
		switch {
		case strings.Contains(said, "price") && strings.Contains(said, "80k"):
			a["tool"], a["price_naira"] = "price", 80000
			a["thing"] = strings.TrimSpace(strings.Split(strings.Split(said, "price of ")[1], " to ")[0])
		case strings.Contains(said, "came in"):
			a["tool"], a["count"], a["thing"] = "stock_in", 20, "rice 50kg"
		case strings.Contains(said, "service"):
			a["tool"], a["name"], a["minutes"], a["price_naira"] = "new_service", "Rice milling", 30, 2500
		}
		raw, _ := json.Marshal(a)
		return &ai.Response{JSON: raw, Text: string(raw)}, nil
	}}}
	owner := w.tenant(t, "Bayo Rice", "bayo@requests.test")
	const on = "+2348010000751"
	w.bind(t, owner, on)
	w.say(t, on, "links")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	rice := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice, 50kg bag", "quantity": 3, "unit": "bag", "price_kobo": 7600000},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice, 25kg bag", "quantity": 3, "unit": "bag", "price_kobo": 4100000}, "Idempotency-Key", "r2")
	ask := func(text string) map[string]any {
		t.Helper()
		r, _ := owner.must(200, "POST", "/v1/chat-tools/request", map[string]any{"text": text})["request"].(map[string]any)
		return r
	}

	// Off: nothing, and "tools" is no command.
	if r := ask("change the price of rice 50kg to 80k"); r != nil {
		t.Fatalf("with chat tools off: %v", r)
	}
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})

	r := ask("change the price of rice 50kg to 80k")
	if r == nil || r["tool"] != "products" || r["row_id"] != rice || r["prefill"].(map[string]any)["price_kobo"] != float64(8000000) ||
		r["words"] != "Change the price of Rice, 50kg bag to ₦80,000" {
		t.Fatalf("a price: %v", r)
	}
	if r := ask("change the price of rice to 80k"); r != nil {
		t.Fatalf("two products could be it: %v", r)
	}
	if r := ask("how much is rice 50kg?"); r != nil {
		t.Fatalf("a question: %v", r)
	}
	r = ask("add a service rice milling for 30 minutes at 2500")
	if r == nil || r["tool"] != "services" || r["add"] != true || r["prefill"].(map[string]any)["minutes"] != float64(30) {
		t.Fatalf("a service: %v", r)
	}

	// On WhatsApp: the tools as links, one by its start, and a request
	// answered with its form's link, filled in.
	got := w.say(t, on, "tools")
	if !strings.Contains(got, "• Products: ") || !strings.Contains(got, "/chat?tool=products&with=taskiem") || !strings.Contains(got, "• Orders: ") ||
		!strings.Contains(got, "• Your orders: ") || !strings.Contains(got, "/chat?tool=bills&with=taskiem") {
		t.Fatalf("tools: %q", got)
	}
	if got := w.say(t, on, "/bills"); !strings.Contains(got, "tool=bills") || strings.Contains(got, "Products") {
		t.Fatalf("/bills: %q", got)
	}
	if got := w.say(t, on, "/pro"); strings.Contains(got, "Orders") || !strings.Contains(got, "tool=products") {
		t.Fatalf("/pro: %q", got)
	}
	got = w.say(t, on, "20 bags of rice 50kg came in")
	if !strings.Contains(got, "Record 20 of Rice, 50kg bag coming in?") || !strings.Contains(got, "tool=stock") || !strings.Contains(got, "count=20") ||
		!strings.Contains(got, "row="+rice) {
		t.Fatalf("a request on WhatsApp: %q", got)
	}
	// Nothing was changed by the words.
	if st := owner.must(200, "GET", "/v1/stock", nil)["items"].([]any); toJSON(st) == "" {
		t.Fatal("no stock")
	}
	for _, x := range owner.must(200, "GET", "/v1/stock", nil)["items"].([]any) {
		if m := x.(map[string]any); m["id"] == rice && m["stock"] != float64(3) {
			t.Fatalf("the words changed the stock: %v", m)
		}
	}
}

// The model's requests know sizes, colours and categories (phase 5): the
// product named with its category, its size and colour heard as choices,
// is that size; the size not said, the product (its grid); a colour heard
// under another name is still its colour.
func TestChatToolRequestsSizes(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		said := strings.ToLower(req.Messages[len(req.Messages)-1].Text)
		a := map[string]any{"tool": "stock_in", "thing": "", "price_naira": 0, "count": 12, "name": "", "minutes": 0, "choices": []any{}}
		switch said {
		case "12 nike sneakers air max size 42 in black came in":
			a["thing"] = "sneakers air max"
			a["choices"] = []any{map[string]any{"choice": "size", "value": "42"}, map[string]any{"choice": "color", "value": "black"}}
		case "12 air max 90 came in":
			a["thing"] = "air max 90"
		case "12 air max 90 black came in":
			a["thing"] = "air max 90"
			a["choices"] = []any{map[string]any{"choice": "shade", "value": "Black"}}
		}
		raw, _ := json.Marshal(a)
		return &ai.Response{JSON: raw, Text: string(raw)}, nil
	}}}
	owner := w.tenant(t, "Zuri Atelier", "ada@requests-sizes.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	am := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair", "price_kobo": 4500000, "category": "Sneakers"},
		"Idempotency-Key", "am")["item"].(map[string]any)["id"].(string)
	vs := owner.must(200, "PUT", "/v1/stock/items/"+am+"/variants", map[string]any{"choices": []any{
		map[string]any{"name": "Size", "values": []string{"42", "43"}}, map[string]any{"name": "Colour", "values": []string{"Black", "White"}}},
		"cells": []any{
			map[string]any{"variant": map[string]any{"Size": "42", "Colour": "Black"}, "count": 1},
			map[string]any{"variant": map[string]any{"Size": "42", "Colour": "White"}, "count": 1},
			map[string]any{"variant": map[string]any{"Size": "43", "Colour": "Black"}, "count": 1},
		}}, "Idempotency-Key", "v")["variants"].([]any)
	b42 := ""
	for _, x := range vs {
		if x.(map[string]any)["name"] == "Air Max 90 – 42, Black" {
			b42 = x.(map[string]any)["id"].(string)
		}
	}
	ask := func(text string) map[string]any {
		t.Helper()
		r, _ := owner.must(200, "POST", "/v1/chat-tools/request", map[string]any{"text": text})["request"].(map[string]any)
		return r
	}
	if r := ask("12 nike sneakers air max size 42 in black came in"); r == nil || r["tool"] != "stock" || r["row_id"] != b42 ||
		r["words"] != "Record 12 of Air Max 90 – 42, Black coming in" {
		t.Fatalf("a size by its category, size and colour: %v", r)
	}
	if r := ask("12 air max 90 came in"); r == nil || r["row_id"] != am {
		t.Fatalf("the size not said: the product: %v", r)
	}
	if r := ask("12 air max 90 black came in"); r == nil || r["row_id"] != am {
		t.Fatalf("black alone is two sizes: the product: %v", r)
	}
}
