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
