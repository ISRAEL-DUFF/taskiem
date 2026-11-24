package pii

import (
	"math"
	"strconv"
)

// Locations (docs/spec/device-fields.md §5): a form's location answer is
// personal data. Its place (the coordinates, or an address typed in its
// stead) is sealed under the category "location"; wherever a masked value
// is shown it is rounded to about 100 m; a model is told only that one was
// answered and how.

// CategoryLocation is a location's x-pii category.
const CategoryLocation = "location"

// CategorySignerName is the category a signature's signer is sealed under
// (device fields D2): a person's name.
const CategorySignerName = "name"

// LocationKeys are the parts of a location answer that are sealed.
var LocationKeys = []string{"lat", "lng", "address"}

// LocationsOwnedBy gives each location path its owner, the person who
// answered it: its coordinates (and the copies a run makes of them) are
// sealed under that person's subject, so erasing them erases where they
// were. A nil owner leaves the paths as they are.
func LocationsOwnedBy(paths []Path, by *Owner) []Path {
	if by == nil {
		return paths
	}
	out := make([]Path, len(paths))
	for i, p := range paths {
		if p.Category == CategoryLocation {
			p.By = by
		}
		out[i] = p
	}
	return out
}

// MaskLocation masks one sealed part of a location: a coordinate rounded
// to three decimal places (about 110 m of latitude, less of longitude
// away from the equator), anything else (an address typed) hidden.
func MaskLocation(v any) string {
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case int64:
		f = float64(t)
	case int:
		f = float64(t)
	default:
		return "[location]"
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "[location]"
	}
	return "≈" + strconv.FormatFloat(math.Round(f*1000)/1000, 'f', 3, 64)
}

// HideLocations replaces, in a value about to leave for an AI model, every
// location answer (an object whose lat, lng or address the run knows as a
// location) with what a model may know of it: that it was answered, its
// state and its source. A copy is returned.
func HideLocations(v any, taint Taint) any {
	switch t := v.(type) {
	case map[string]any:
		if isLocation(t, taint) {
			out := map[string]any{"location": "answered"}
			for _, k := range []string{"state", "source"} {
				if s, ok := t[k].(string); ok {
					out[k] = s
				}
			}
			return out
		}
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = HideLocations(x, taint)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = HideLocations(x, taint)
		}
		return out
	}
	return v
}

func isLocation(m map[string]any, taint Taint) bool {
	for _, k := range LocationKeys {
		if key, ok := scalarKey(m[k]); ok && taint.Category(key) == CategoryLocation {
			return true
		}
	}
	return false
}
