package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/israel-duff/taskiem/engine/egress"
)

// Sizes and colours on the public page (phase 4): the copy carries a
// product's sizes on sale on the product (never as items of their own),
// each with its values, price and count; a sale of one updates its count
// in the copy at once; a colour's photo is set on each of its sizes, and
// taken out again.
func TestSizesOnStorefront(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	w.srv.Egress = &egress.Guard{Blocked: func(netip.Addr) bool { return false }}
	pic := photo(t)
	web := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "image/jpeg")
		_, _ = rw.Write(pic)
	}))
	t.Cleanup(web.Close)
	owner := w.tenant(t, "Zuri Atelier", "ada@zuri.test")
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	owner.must(200, "POST", "/v1/stock/setup", nil)
	id := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Air Max 90", "quantity": 0, "unit": "pair", "price_kobo": 4500000},
		"Idempotency-Key", "p")["item"].(map[string]any)["id"].(string)
	choices := []any{map[string]any{"name": "Size", "values": []string{"42"}}, map[string]any{"name": "Colour", "values": []string{"Black", "White", "Red"}}}
	cell := func(c string, n int) map[string]any {
		return map[string]any{"variant": map[string]any{"Size": "42", "Colour": c}, "count": n}
	}
	white := cell("White", 2)
	white["price_kobo"] = 4800000
	vs := owner.must(200, "PUT", "/v1/stock/items/"+id+"/variants", map[string]any{"choices": choices, "cells": []any{cell("Black", 3), white}},
		"Idempotency-Key", "v1")["variants"].([]any)
	black := ""
	for _, x := range vs {
		if v := x.(map[string]any); v["name"] == "Air Max 90 – 42, Black" {
			black = v["id"].(string)
		}
	}
	product := func() map[string]any {
		t.Helper()
		items := owner.must(200, "GET", "/v1/storefront/catalogue", nil)["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("the copy lists the product only: %v", items)
		}
		return items[0].(map[string]any)
	}
	p := product()
	sizes := p["sizes"].([]any)
	cs := p["choices"].([]any)
	if len(sizes) != 2 || p["stock"] != float64(5) || len(cs[1].(map[string]any)["values"].([]any)) != 2 {
		t.Fatalf("the sizes on the product, Red (none sold) left out of its choices: %v", p)
	}
	for _, x := range sizes {
		z := x.(map[string]any)
		if z["variant"].(map[string]any)["Colour"] == "White" && z["price_kobo"] != float64(4800000) || z["variant"].(map[string]any)["Colour"] == "Black" && z["price_kobo"] != nil {
			t.Fatalf("a size's own price, the rest the product's: %v", z)
		}
	}

	// A sale of 42 Black: its count in the copy at once, the product's too.
	owner.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": black, "quantity": 2}}, "paid_how": "cash"},
		"Idempotency-Key", "sale")
	p = product()
	for _, x := range p["sizes"].([]any) {
		if z := x.(map[string]any); z["id"] == black && z["stock"] != float64(1) {
			t.Fatalf("the size's count in the copy: %v", z)
		}
	}
	if p["stock"] != float64(3) {
		t.Fatalf("the product's count, theirs together: %v", p["stock"])
	}

	// Black's own photo: on its size, not White's; then taken out.
	base := "/v1/stock/items/" + id + "/variants/photo?choice=colour&value=Black"
	owner.must(400, "POST", "/v1/stock/items/"+id+"/variants/photo?choice=Colour&value=Purple", map[string]any{"url": web.URL + "/b.jpg"})
	if out := owner.must(200, "POST", base, map[string]any{"url": web.URL + "/b.jpg"}); out["sizes"] != float64(1) {
		t.Fatalf("set on Black's sizes: %v", out)
	}
	photoOf := func(colour string) any {
		for _, x := range product()["sizes"].([]any) {
			if z := x.(map[string]any); z["variant"].(map[string]any)["Colour"] == colour {
				return z["photo"]
			}
		}
		return nil
	}
	if photoOf("Black") == nil || photoOf("White") != nil {
		t.Fatalf("Black's photo: %v / %v", photoOf("Black"), photoOf("White"))
	}
	owner.must(204, "DELETE", base, nil)
	if photoOf("Black") != nil {
		t.Fatal("Black's photo taken out")
	}
}
