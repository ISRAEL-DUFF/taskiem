package api_test

import (
	"context"
	"strings"
	"testing"
)

// Kinds an owner makes (saved objects S1, part 5): a business's Car with a
// sensitive chassis number never shared; a car of it, the number sealed and
// masked, in full only by reveal; a field made sensitive later sealed at
// once; told by @name in the chat, masked; a kind in use not removed; a person's own kinds apart
// from the business's; staff without saved.manage only read them.
func TestSavedMadeUpKinds(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Bola Stores", "bola@kinds.test")

	list := owner.must(200, "GET", "/v1/saved-kinds", nil)
	if len(list["kinds"].([]any)) != 0 || len(list["starters"].([]any)) != 4 || list["limit"] != float64(40) {
		t.Fatalf("starting: %v", list)
	}
	car := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Car", "fields": []map[string]any{
		{"label": "Plate number", "type": "text", "required": true},
		{"label": "Insurance due", "type": "date"},
		{"label": "Chassis number", "type": "text", "never_share": true},
		{"label": "Driver's phone", "type": "phone"},
	}})["kind"].(map[string]any)
	owner.must(409, "POST", "/v1/saved-kinds", map[string]any{"name": "car", "fields": []map[string]any{{"label": "A", "type": "text"}}})
	owner.must(400, "POST", "/v1/saved-kinds", map[string]any{"name": "Truck", "fields": []map[string]any{{"label": "A", "type": "colour"}}})
	owner.must(404, "PUT", "/v1/saved-kinds/01a1f840-0000-7000-8000-000000000000", map[string]any{"name": "Truck", "fields": []map[string]any{{"label": "A", "type": "text"}}})

	add := map[string]any{"name": "van", "title": "Delivery van", "kind": "custom", "kind_id": car["id"],
		"values": map[string]any{"plate_number": "LND 123 XY", "insurance_due": "2027-03-03", "chassis_number": "JTDBR32E720123456", "drivers_phone": "0803 112 0099"}}
	owner.must(400, "POST", "/v1/saved", map[string]any{"name": "van2", "title": "Van", "kind": "custom", "kind_id": car["id"], "values": map[string]any{"insurance_due": "2027-03-03"}})
	van := owner.must(201, "POST", "/v1/saved", add)["saved"].(map[string]any)
	vals := van["values"].(map[string]any)
	if van["kind"] != "custom" || van["kind_name"] != "Car" || vals["chassis_number"] != "••3456" || vals["drivers_phone"] != "+2348031120099" || van["hint"] != "LND 123 XY · 3 Mar 2027" {
		t.Fatalf("the van: %v", van)
	}
	var plain int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_objects WHERE details::text LIKE '%JTDBR32E720123456%' OR sealed::text LIKE '%JTDBR32E720123456%'`).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("the chassis number in the clear: %d %v", plain, err)
	}

	// The driver's phone made sensitive: sealed and masked at once.
	fields := car["fields"].([]any)
	fields[3].(map[string]any)["sensitive"] = true
	owner.must(200, "PUT", "/v1/saved-kinds/"+car["id"].(string), map[string]any{"name": "Car", "fields": fields})
	got := owner.must(200, "GET", "/v1/saved/"+van["id"].(string), nil)
	if got["values"].(map[string]any)["drivers_phone"] != "••0099" || got["values"].(map[string]any)["plate_number"] != "LND 123 XY" {
		t.Fatalf("after the change: %v", got)
	}
	// A change keeps what was not given; the sensitive in full only by reveal.
	changed := owner.must(200, "PUT", "/v1/saved/"+van["id"].(string), map[string]any{"values": map[string]any{"insurance_due": "2027-04-01"}})["saved"].(map[string]any)
	if changed["values"].(map[string]any)["chassis_number"] != "••3456" || changed["values"].(map[string]any)["insurance_due"] != "2027-04-01" {
		t.Fatalf("changed: %v", changed)
	}

	// By @name in the chat: masked.
	said := taskiemSays(t, owner, "@van")
	if !strings.Contains(said, "Plate number: LND 123 XY") || !strings.Contains(said, "Chassis number: ••3456") || strings.Contains(said, "JTDBR32E720123456") {
		t.Fatalf("in the chat: %q", said)
	}

	// In use: not removed; a person's own kinds are theirs.
	if e := owner.must(409, "DELETE", "/v1/saved-kinds/"+car["id"].(string), nil); e["in_use"] != float64(1) {
		t.Fatalf("in use: %v", e)
	}
	if n := len(owner.must(200, "GET", "/v1/me/saved-kinds", nil)["kinds"].([]any)); n != 0 {
		t.Fatalf("the person's own: %d", n)
	}
	owner.must(201, "POST", "/v1/me/saved-kinds", map[string]any{"name": "Car", "fields": []map[string]any{{"label": "Plate number", "type": "text"}}})
	owner.must(204, "DELETE", "/v1/saved/"+van["id"].(string), nil)
	owner.must(204, "DELETE", "/v1/saved-kinds/"+car["id"].(string), nil)
	if n := len(owner.must(200, "GET", "/v1/saved-kinds", nil)["kinds"].([]any)); n != 0 {
		t.Fatalf("removed: %d", n)
	}
}

// Saved addresses are the person's own places (saved objects S1, part 6):
// one added on /v1/me/addresses is on Saved as @home; changed on Saved, it
// is changed as an address; the default removed on Saved, the newest place
// is the default; @home in a message to Taskiem tells it.
func TestSavedAddressesArePlaces(t *testing.T) {
	w := ordersWorld(t)
	tolu, _ := w.personIn(t, "+2348031100777")
	home := tolu.must(201, "POST", "/v1/me/addresses", map[string]any{"label": "Home", "line": "12 Allen Avenue", "area": "Ikeja"})
	tolu.must(201, "POST", "/v1/me/addresses", map[string]any{"label": "Home", "line": "4 Bode Thomas Street"})
	var place map[string]any
	names := []string{}
	for _, x := range tolu.must(200, "GET", "/v1/me/saved", nil)["saved"].([]any) {
		m := x.(map[string]any)
		names = append(names, m["name"].(string))
		if m["id"] == home["id"] {
			place = m
		}
	}
	if place == nil || place["kind"] != "place" || place["name"] != "home" || place["title"] != "Home" || strings.Join(names, ",") != "home,home-2" {
		t.Fatalf("on Saved: %v %v", place, names)
	}
	tolu.must(200, "PUT", "/v1/me/saved/"+home["id"].(string), map[string]any{"title": "Our house", "details": map[string]any{"landmark": "by the blue gate"}})
	list := tolu.must(200, "GET", "/v1/me/addresses", nil)["addresses"].([]any)
	first := list[0].(map[string]any)
	if first["id"] != home["id"] || first["label"] != "Our house" || first["landmark"] != "by the blue gate" || first["default"] != true {
		t.Fatalf("as an address: %v", list)
	}
	tolu.must(204, "DELETE", "/v1/me/saved/"+home["id"].(string), nil)
	list = tolu.must(200, "GET", "/v1/me/addresses", nil)["addresses"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["default"] != true {
		t.Fatalf("the default moved: %v", list)
	}
}

// A kind's change that shows what was kept hidden needs a passkey and is
// recorded; a field removed takes its values (and file) with it; an account
// in a thing of one's own kind is changed with a passkey and recorded
// masked; the note is checked as any note is.
func TestSavedKindsHiddenKept(t *testing.T) {
	ctx := context.Background()
	w, owner := savedWorld(t)
	biz := tenantOf(t, owner)
	gen := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Generator", "fields": []map[string]any{
		{"label": "Model", "type": "text"},
		{"label": "Serial", "type": "text", "sensitive": true},
		{"label": "Unused secret", "type": "text", "sensitive": true},
		{"label": "Old code", "type": "text", "sensitive": true},
		{"label": "Pay to", "type": "bank_account"},
	}})["kind"].(map[string]any)
	kid := gen["id"].(string)
	owner.must(400, "POST", "/v1/saved", map[string]any{"name": "g0", "title": "G", "kind": "custom", "kind_id": kid, "note": strings.Repeat("x", 201)})
	g := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "big-gen", "title": "Big gen", "kind": "custom", "kind_id": kid, "note": "  in   the yard ",
		"values": map[string]any{"model": "Mikano 20", "serial": "SN-99887766", "old_code": "CODE-12345678", "pay_to": "Zenith Bank 0123456789"}})["saved"].(map[string]any)
	gid := g["id"].(string)
	if g["details"].(map[string]any)["note"] != "in the yard" {
		t.Fatalf("note: %v", g)
	}
	fields := func(edit func([]any) []any) map[string]any {
		fs := owner.must(200, "GET", "/v1/saved-kinds", nil)["kinds"].([]any)[0].(map[string]any)["fields"].([]any)
		return map[string]any{"name": "Generator", "fields": edit(fs)}
	}

	// Sensitive off for a field no object fills: no passkey asked.
	owner.must(200, "PUT", "/v1/saved-kinds/"+kid, fields(func(fs []any) []any { fs[2].(map[string]any)["sensitive"] = false; return fs }))
	// Sensitive off for one with a value: a passkey first.
	off := fields(func(fs []any) []any { fs[1].(map[string]any)["sensitive"] = false; return fs })
	if e := owner.must(403, "PUT", "/v1/saved-kinds/"+kid, off); e["step_up"] != "passkey" || e["operation"] != "saved.kind" || e["target"] != kid {
		t.Fatalf("asked: %v", e)
	}
	if v := owner.must(200, "GET", "/v1/saved/"+gid, nil)["values"].(map[string]any); v["serial"] != "••7766" {
		t.Fatalf("still hidden: %v", v)
	}
	key := addPasskey(t, owner, "Phone")
	// A passkey for another thing does not do.
	wrong := challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.change", "target": gid}))
	off["passkey"] = key.Assert(wrong).JSON()
	owner.must(403, "PUT", "/v1/saved-kinds/"+kid, off)
	ch := challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.kind", "target": kid}))
	off["passkey"] = key.Assert(ch).JSON()
	owner.must(200, "PUT", "/v1/saved-kinds/"+kid, off)
	if v := owner.must(200, "GET", "/v1/saved/"+gid, nil)["values"].(map[string]any); v["serial"] != "SN-99887766" {
		t.Fatalf("shown: %v", v)
	}
	if a := auditDetails(t, w, biz, "saved.kind.changed"); !strings.Contains(a[len(a)-1], `"shown": ["Serial"]`) || !strings.Contains(a[len(a)-1], `"confirmed": "passkey"`) {
		t.Fatalf("kind audit: %v", a)
	}
	if a := auditDetails(t, w, biz, "saved.changed"); len(a) == 0 || !strings.Contains(a[len(a)-1], `"shown": ["Serial"]`) {
		t.Fatalf("object audit: %v", a)
	}

	// Old code removed: its value deleted, not made plain, and not back if
	// a field of that key comes again.
	owner.must(200, "PUT", "/v1/saved-kinds/"+kid, fields(func(fs []any) []any { return append(fs[:3], fs[4]) }))
	var left int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_objects WHERE id = $1 AND (details::text LIKE '%CODE-12345678%' OR details->'masked' ? 'old_code')`, gid).Scan(&left); err != nil || left != 0 {
		t.Fatalf("a removed field's value kept: %d %v", left, err)
	}
	back := fields(func(fs []any) []any {
		return append(fs, map[string]any{"key": "old_code", "label": "Old code", "type": "text", "sensitive": true})
	})
	owner.must(200, "PUT", "/v1/saved-kinds/"+kid, back)
	revealCh := challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.reveal", "target": gid}))
	if r := owner.must(200, "POST", "/v1/saved/"+gid+"/reveal", map[string]any{"passkey": key.Assert(revealCh).JSON()}); strings.Contains(toJSON(r), "CODE-12345678") {
		t.Fatalf("came back: %v", r)
	}

	// The account in it: changed only with a passkey, recorded masked.
	move := map[string]any{"values": map[string]any{"pay_to": "Zenith Bank 0123450000"}}
	if e := owner.must(403, "PUT", "/v1/saved/"+gid, move); e["operation"] != "saved.change" {
		t.Fatalf("account change asked: %v", e)
	}
	ch = challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.change", "target": gid}))
	move["passkey"] = key.Assert(ch).JSON()
	owner.must(200, "PUT", "/v1/saved/"+gid, move)
	a := auditDetails(t, w, biz, "saved.changed")
	last := a[len(a)-1]
	if !strings.Contains(last, "Zenith Bank ••6789") || !strings.Contains(last, "Zenith Bank ••0000") || strings.Contains(last, "0123450000") || !strings.Contains(last, `"confirmed": "passkey"`) {
		t.Fatalf("account change audit: %v", last)
	}
	// Other values change without one.
	owner.must(200, "PUT", "/v1/saved/"+gid, map[string]any{"values": map[string]any{"model": "Mikano 30"}})
}
