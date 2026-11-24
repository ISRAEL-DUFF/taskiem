package pii

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLocationPaths(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"where": map[string]any{"type": "location", "why": "w"},
		"phone": map[string]any{"type": "string", "x-pii": "phone"},
	}}
	got := map[string]string{}
	for _, p := range SchemaPaths(schema, nil) {
		got[strings.Join(p.Segments, ".")] = p.Category
	}
	if got["where.lat"] != "location" || got["where.lng"] != "location" || got["where.address"] != "location" || got["phone"] != "phone" || got["where"] != "" {
		t.Fatalf("paths: %v", got)
	}
}

func TestMaskLocation(t *testing.T) {
	for v, want := range map[any]string{6.452131: "≈6.452", 3.3958: "≈3.396", int64(7): "≈7.000", "12 Allen Avenue": "[location]"} {
		if got := MaskLocation(v); got != want {
			t.Errorf("%v: %q, want %q", v, got, want)
		}
	}
}

// A model is told that a location was answered and how, nothing more:
// not its coordinates, its accuracy or when it was read.
func TestHideLocations(t *testing.T) {
	taint := Taint{}
	taint.Add(6.452131, CategoryLocation)
	taint.Add(3.395812, CategoryLocation)
	in := map[string]any{"order": "A-12", "handover": map[string]any{
		"where": map[string]any{"lat": 6.452131, "lng": 3.395812, "accuracy_m": 8.0, "at": "2026-11-22T09:58:00Z", "source": "device", "state": "answered"},
		"note":  "left with the gateman"},
		"far": map[string]any{"lat": 1.0, "lng": 2.0}}
	out := HideLocations(in, taint)
	raw, _ := json.Marshal(out)
	s := string(raw)
	if strings.Contains(s, "6.45") || strings.Contains(s, "3.39") || strings.Contains(s, "accuracy") || strings.Contains(s, "09:58") {
		t.Fatalf("leaked: %s", s)
	}
	if !strings.Contains(s, `"where":{"location":"answered","source":"device","state":"answered"}`) || !strings.Contains(s, `"lat":1`) {
		t.Fatalf("hidden: %s", s)
	}
	// And the masker, for a coordinate written into text.
	if m := NewMasker(taint).MaskText("drop at 6.452131, 3.395812"); strings.Contains(m, "6.45") {
		t.Fatalf("text: %s", m)
	}
}
