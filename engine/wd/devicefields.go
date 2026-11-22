package wd

import (
	"fmt"
	"slices"
	"strings"
)

// Device fields (docs/spec/device-fields.md §3): questions of a form step
// that the device answers at the person's tap. Each is a property of the
// step's schema whose "type" is location, photo, scan or signature, with
// options of its own; `why` is required on every one. Such a schema is
// not plain JSON Schema: AnswerSchema turns it into the schema the
// answers are checked against, and WithoutDeviceFields into one a page
// that cannot ask a device shows.

// Device field kinds.
const (
	DeviceLocation  = "location"
	DevicePhoto     = "photo"
	DeviceScan      = "scan"
	DeviceSignature = "signature"
)

// DeviceKinds are the device fields, in the order a form shows them.
var DeviceKinds = []string{DeviceLocation, DevicePhoto, DeviceScan, DeviceSignature}

// ScanFormats are the code formats a scan field may accept (§8: the
// common ones the scanner reads).
var ScanFormats = []string{"qr", "ean_13", "ean_8", "upc_a", "upc_e", "code_128"}

// MaxDeviceFields is how many device fields one question may have: one
// of each (§8).
const MaxDeviceFields = 4

// deviceOptions are the options each kind takes besides the common ones.
var deviceOptions = map[string][]string{
	DeviceLocation:  {"require_source", "max_accuracy_m", "max_age", "allow_typed"},
	DevicePhoto:     {"require_source"},
	DeviceScan:      {"formats", "allow_typed"},
	DeviceSignature: {},
}

// commonDeviceOptions are what every device field may carry.
var commonDeviceOptions = []string{"type", "why", "title", "description", "x-form", "x-pii"}

// requireSources is the source each kind may require.
var requireSources = map[string]string{DeviceLocation: "device", DevicePhoto: "camera"}

// DeviceKind is a schema property's device field kind, or "".
func DeviceKind(prop any) string {
	p, _ := prop.(map[string]any)
	t, _ := p["type"].(string)
	if slices.Contains(DeviceKinds, t) {
		return t
	}
	return ""
}

// DeviceFields are a schema's device fields: name to kind.
func DeviceFields(schema map[string]any) map[string]string {
	props, _ := schema["properties"].(map[string]any)
	out := map[string]string{}
	for name, p := range props {
		if k := DeviceKind(p); k != "" {
			out[name] = k
		}
	}
	return out
}

// deviceNoun names a kind in a sentence.
func deviceNoun(kind string) string {
	switch kind {
	case DeviceLocation:
		return "a location"
	case DevicePhoto:
		return "a photo"
	case DeviceScan:
		return "a scan"
	}
	return "a signature"
}

// DeviceFieldProblems checks a form step's device fields (§3, §8): their
// options, `why` on each, at most one of each kind, and a required
// signature asked of someone who may answer where nothing can draw.
func DeviceFieldProblems(schema map[string]any, to FormTo, path string) []Problem {
	var out []Problem
	add := func(at, msg string, args ...any) {
		out = append(out, Problem{Path: path + at, Message: fmt.Sprintf(msg, args...)})
	}
	props, _ := schema["properties"].(map[string]any)
	req := map[string]bool{}
	if r, ok := schema["required"].([]any); ok {
		for _, x := range r {
			if s, ok := x.(string); ok {
				req[s] = true
			}
		}
	}
	seen := map[string]string{}
	n := 0
	for _, name := range SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		kind := DeviceKind(p)
		if kind == "" {
			continue
		}
		n++
		at := "/properties/" + name
		if other, dup := seen[kind]; dup {
			add(at, "%q and %q both ask for %s: a question asks for at most one of each device field", other, name, deviceNoun(kind))
		}
		seen[kind] = name
		for k := range p {
			if !slices.Contains(commonDeviceOptions, k) && !slices.Contains(deviceOptions[kind], k) {
				add(at+"/"+k, "%q: %s field has no option %q", name, deviceNoun(kind), k)
			}
		}
		why, _ := p["why"].(string)
		switch {
		case strings.TrimSpace(why) == "":
			add(at+"/why", "%q asks for %s: say why in one sentence (why), so the person knows before their device is asked", name, deviceNoun(kind))
		case len([]rune(why)) > 200:
			add(at+"/why", "%q: why is one sentence of at most 200 characters", name)
		}
		if v, ok := p["require_source"]; ok {
			s, _ := v.(string)
			if want, ok := requireSources[kind]; ok && s != want {
				add(at+"/require_source", "%q: require_source on %s field is %q", name, deviceNoun(kind), want)
			}
		}
		if v, ok := p["max_accuracy_m"]; ok {
			if m, ok := num(v); !ok || m <= 0 || m > 100000 {
				add(at+"/max_accuracy_m", "%q: max_accuracy_m is a number of metres, more than 0 and at most 100000", name)
			}
		}
		if v, ok := p["max_age"]; ok {
			s, _ := v.(string)
			if d, err := ParseDuration(s); err != nil || d <= 0 {
				add(at+"/max_age", "%q: max_age is a duration, like 5m", name)
			}
		}
		if v, ok := p["allow_typed"]; ok {
			if _, ok := v.(bool); !ok {
				add(at+"/allow_typed", "%q: allow_typed is true or false", name)
			}
		}
		if v, ok := p["formats"]; ok {
			list, _ := v.([]any)
			if len(list) == 0 {
				add(at+"/formats", "%q: formats lists the codes it takes, from %s", name, strings.Join(ScanFormats, ", "))
			}
			for _, f := range list {
				if s, _ := f.(string); !slices.Contains(ScanFormats, s) {
					add(at+"/formats", "%q: %v is not a code format the scanner reads (%s)", name, f, strings.Join(ScanFormats, ", "))
				}
			}
		}
		if v, ok := p["x-pii"]; ok {
			s, _ := v.(string)
			switch kind {
			case DeviceLocation:
				if s != "location" {
					add(at+"/x-pii", "%q: a location is always sealed as a location; leave x-pii out", name)
				}
			case DeviceScan:
				if s == "" {
					add(at+"/x-pii", "%q: x-pii is a category, like other", name)
				}
			default:
				add(at+"/x-pii", "%q: %s is a file, kept privately; it takes no x-pii", name, deviceNoun(kind))
			}
		}
		if kind == DeviceSignature && req[name] && (to.Phone != "" || to.Email != "") {
			add(at, "%q: a signature cannot be required of someone asked by phone or email, who may answer in WhatsApp or Telegram where there is no pad to draw on; make it optional", name)
		}
	}
	if n > MaxDeviceFields {
		add("/properties", "a question has at most %d device fields, one of each", MaxDeviceFields)
	}
	// require_source "device" where the question can only be answered in
	// WhatsApp or Telegram could never be met (§8). No `to` is answered
	// only there today: a phone or an email gets a one-time link that
	// opens in a browser, which can read a location; members answer in
	// the app or the web, and so does a link's person (`to: {link}`, D1):
	// the linked business's owner, who answers in the app or the web as
	// well as in their chat. So the row refuses nothing.
	return out
}

// deviceFieldsOutsideSteps refuses device fields anywhere in a
// workflow's inputs schema: a form that starts a workflow, a manual run
// and an API call cannot ask a device yet (device fields §3: form steps,
// and tools' inputs once made tools exist).
func deviceFieldsOutsideSteps(d *Definition) []Problem {
	schema, types := d.InputsSchema()
	var out []Problem
	var walk func(v any, path string, depth int)
	walk = func(v any, path string, depth int) {
		m, ok := v.(map[string]any)
		if !ok || depth > 32 {
			return
		}
		if k := DeviceKind(m); k != "" {
			out = append(out, Problem{Path: path, Message: fmt.Sprintf("%s field is asked by a form step; a workflow's inputs cannot ask a device yet", strings.ToUpper(deviceNoun(k)[:1])+deviceNoun(k)[1:])})
			return
		}
		if props, ok := m["properties"].(map[string]any); ok {
			for _, k := range sortedKeys(props) {
				walk(props[k], path+"/properties/"+k, depth+1)
			}
		}
		if items, ok := m["items"]; ok {
			walk(items, path+"/items", depth+1)
		}
	}
	walk(schema, "/inputs/schema", 0)
	for _, k := range sortedKeys(types) {
		walk(types[k], "/types/"+k, 0)
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// HasDeviceFields reports whether a schema asks a device.
func HasDeviceFields(schema map[string]any) bool { return len(DeviceFields(schema)) > 0 }

// WithoutDeviceFields is a schema without its device fields, for a page
// that cannot ask a device yet (the form host before D1): they are not
// shown, and the answer has them `unavailable`.
func WithoutDeviceFields(schema map[string]any) map[string]any {
	dev := DeviceFields(schema)
	if len(dev) == 0 {
		return schema
	}
	return withProps(schema, func(name string, p any) (any, bool) {
		_, isDev := dev[name]
		return p, !isDev
	})
}

// withProps copies a schema with each property mapped (dropped when keep
// is false), and required trimmed to the properties kept.
func withProps(schema map[string]any, fn func(name string, p any) (any, bool)) map[string]any {
	out := map[string]any{}
	for k, v := range schema {
		out[k] = v
	}
	props, _ := schema["properties"].(map[string]any)
	np := map[string]any{}
	for name, p := range props {
		if q, keep := fn(name, p); keep {
			np[name] = q
		}
	}
	out["properties"] = np
	if req, ok := schema["required"].([]any); ok {
		var keep []any
		for _, r := range req {
			if s, _ := r.(string); np[s] != nil {
				keep = append(keep, r)
			}
		}
		if len(keep) == 0 {
			delete(out, "required")
		} else {
			out["required"] = keep
		}
	}
	return out
}

// Device answer states (§4): answered, or refused and unavailable (the
// person or their device could not answer), or unverified (answered from
// a weaker source than the field requires).
var DeviceStates = []string{"answered", "refused", "unavailable", "unverified"}

// AnswerSchema is the plain JSON Schema a form step's answers are
// checked against once the server has read them (engine/forms): each
// device field is the object its answer is kept as.
func AnswerSchema(schema map[string]any) map[string]any {
	if !HasDeviceFields(schema) {
		return schema
	}
	return withProps(schema, func(_ string, p any) (any, bool) {
		kind := DeviceKind(p)
		if kind == "" {
			return p, true
		}
		return deviceAnswerSchema(kind), true
	})
}

func deviceAnswerSchema(kind string) map[string]any {
	str := map[string]any{"type": "string"}
	props := map[string]any{
		"state":  map[string]any{"enum": []any{"answered", "refused", "unavailable", "unverified"}},
		"source": str,
		"reason": map[string]any{"type": "string", "maxLength": 500},
	}
	switch kind {
	case DeviceLocation:
		props["lat"] = map[string]any{"type": "number", "minimum": -90, "maximum": 90}
		props["lng"] = map[string]any{"type": "number", "minimum": -180, "maximum": 180}
		props["accuracy_m"] = map[string]any{"type": "number", "minimum": 0}
		props["at"] = map[string]any{"type": "string", "format": "date-time"}
		props["fix"] = map[string]any{"enum": []any{"gps", "network"}}
		props["address"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 500}
		props["coarse"] = map[string]any{"type": "boolean"}
	case DeviceScan:
		props["text"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 2000}
		props["format"] = str
	case DevicePhoto, DeviceSignature:
		for _, k := range []string{"id", "name", "type", "sha256"} {
			props[k] = str
		}
		props["size"] = map[string]any{"type": "integer"}
	}
	return map[string]any{"type": "object", "required": []any{"state"}, "additionalProperties": false, "properties": props}
}
