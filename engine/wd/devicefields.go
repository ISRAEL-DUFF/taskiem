package wd

import (
	"encoding/json"
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
	DeviceSignature: {"ask_name"},
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
		if v, ok := p["ask_name"]; ok {
			if _, ok := v.(bool); !ok {
				add(at+"/ask_name", "%q: ask_name is true or false", name)
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
// workflow's inputs schema but one place: the top level of the inputs of
// a workflow started by hand (a member's tool, or the Run's). There a
// scan (the phone's scan from the home screen fills it, device fields §7,
// D2), a location, a photo and a signature (build-plan-gaps H3) are asked
// as a form step asks them, one of each. A form that starts a workflow
// cannot take one, and a tool for customers or a linked business takes
// none: the founder's answer (build-plan-gaps §14) is members' tools only.
func deviceFieldsOutsideSteps(d *Definition) []Problem {
	schema, types := d.InputsSchema()
	var out []Problem
	top := map[string]any{}
	var walk func(v any, path string, depth int)
	walk = func(v any, path string, depth int) {
		m, ok := v.(map[string]any)
		if !ok || depth > 32 {
			return
		}
		if k := DeviceKind(m); k != "" {
			atTop := depth == 1 && strings.HasPrefix(path, "/inputs/schema/properties/")
			switch {
			case d.Tool.Audience() != ToolForMembers:
				out = append(out, Problem{Path: path, Message: notForCustomers(k, d.Tool.Audience())})
			case atTop && d.Trigger.Type == "manual":
				top[strings.TrimPrefix(path, "/inputs/schema/properties/")] = m
			case k == DeviceScan:
				out = append(out, Problem{Path: path, Message: "A scan in a workflow's inputs is one of its top-level inputs, in a workflow started by hand (a manual trigger): the code the phone read"})
			default:
				out = append(out, Problem{Path: path, Message: fmt.Sprintf("%s in a workflow's inputs is one of its top-level inputs, in a workflow started by hand (a manual trigger, a tool for members); elsewhere it is asked by a form step", upperFirst(deviceNoun(k)))})
			}
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
	if len(top) > 0 {
		// Their why and options, and one of each, as a form step's. A
		// required signature is fine: the app and the web have a pad, and
		// a chat sends it unavailable.
		one := map[string]any{"type": "object", "properties": top}
		for _, p := range DeviceFieldProblems(one, FormTo{}, "/inputs/schema") {
			if strings.Contains(p.Message, "both ask for a scan") {
				p.Message = "a workflow's inputs take at most one scan: the code the phone read"
			}
			out = append(out, p)
		}
	}
	return out
}

// notForCustomers is why a tool for customers (or for a linked business)
// cannot ask a device.
func notForCustomers(kind, audience string) string {
	who := "a tool for customers"
	if audience == ToolForLinked {
		who = "a tool for linked businesses"
	}
	switch kind {
	case DeviceScan:
		return who + " cannot take a scan: WhatsApp has no scanner"
	case DeviceLocation:
		return who + " cannot ask for a location: a customer gives one only by sending WhatsApp's own location message, which a workflow reads as trigger.body.location"
	}
	return fmt.Sprintf("%s cannot ask for %s: only a tool for members takes one", who, deviceNoun(kind))
}

func upperFirst(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// DeviceInputs are a workflow's device inputs (build-plan-gaps H3): its
// top-level inputs of a device kind, in a workflow started by hand for
// members, name to kind. Empty for any other workflow.
func DeviceInputs(d *Definition) map[string]string {
	if d.Trigger.Type != "manual" || d.Tool.Audience() != ToolForMembers {
		return nil
	}
	raw, _ := d.InputsSchema()
	schema, _ := raw.(map[string]any)
	return DeviceFields(schema)
}

// ScanInput is a workflow's scan input (device fields §7, D2): its first
// top-level input of type scan, in a workflow started by hand.
func ScanInput(d *Definition) (string, map[string]any, bool) {
	if d.Trigger.Type != "manual" || d.Tool.Audience() != ToolForMembers {
		return "", nil, false
	}
	raw, _ := d.InputsSchema()
	schema, _ := raw.(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	for _, name := range SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		if DeviceKind(p) == DeviceScan {
			return name, p, true
		}
	}
	return "", nil, false
}

// ScanTakes reports whether a scan field takes a code of this format: one
// its formats name, or any the scanner reads when it names none. A code
// typed (no format) is taken unless allow_typed is false.
func ScanTakes(p map[string]any, format string) bool {
	if format == "" {
		return p["allow_typed"] != false
	}
	if !slices.Contains(ScanFormats, format) {
		return false
	}
	fs, ok := p["formats"].([]any)
	return !ok || slices.Contains(fs, any(format))
}

// MaxScanText is how long a scanned code may be, in characters: a QR code
// holds a few thousand at most, a barcode a few dozen.
const MaxScanText = 2000

// ScanTextProblem checks a code's text: not empty, at most MaxScanText
// characters, and no control characters (a code is read, then shown).
func ScanTextProblem(text string) string {
	switch {
	case strings.TrimSpace(text) == "":
		return "a scan is the code's text"
	case len([]rune(text)) > MaxScanText:
		return fmt.Sprintf("a code is at most %d characters", MaxScanText)
	}
	for _, r := range text {
		if (r < 0x20 && r != '\t') || r == 0x7f {
			return "a code has no control characters"
		}
	}
	return ""
}

// scanInputSchema is what a scan input takes before the run reads it: the
// code typed (a string), {text, format} as the phone read it, or a
// refusal.
func scanInputSchema() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{"anyOf": []any{
		map[string]any{"type": "string", "minLength": 1, "maxLength": MaxScanText},
		map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"text": map[string]any{"type": "string", "minLength": 1, "maxLength": MaxScanText}, "format": str,
			"state": map[string]any{"enum": []any{"answered", "refused", "unavailable"}}, "source": str,
			"reason": map[string]any{"type": "string", "maxLength": 500}}},
	}}
}

// deviceInputSchema is what a device input takes before the run reads it
// (build-plan-gaps H3): a location as the device read it ({lat, lng,
// accuracy_m, at, fix}) or an address typed; a photo or a signature as
// the file the app or the web sent first (POST /v1/tools/{command}/files:
// {file}, a signature's signer beside it); any of them refused or
// unavailable, with a reason.
func deviceInputSchema(kind string) map[string]any {
	if kind == DeviceScan {
		return scanInputSchema()
	}
	str := map[string]any{"type": "string"}
	props := map[string]any{
		"state":  map[string]any{"enum": []any{"answered", "refused", "unavailable"}},
		"reason": map[string]any{"type": "string", "maxLength": 500},
	}
	if kind == DeviceLocation {
		props["lat"] = map[string]any{"type": "number", "minimum": -90, "maximum": 90}
		props["lng"] = map[string]any{"type": "number", "minimum": -180, "maximum": 180}
		props["accuracy_m"] = map[string]any{"type": "number", "minimum": 0}
		props["at"] = str
		props["fix"] = map[string]any{"enum": []any{"gps", "network"}}
		props["address"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 500}
		props["source"] = str
	} else {
		props["file"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 64}
		if kind == DeviceSignature {
			props["signed_by"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 120}
		}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props}
}

// scanInputProblems checks a scan input beyond its schema: a code, of a
// format the input takes.
func (d *Definition) scanInputProblems(body []byte) []string {
	name, p, ok := ScanInput(d)
	if !ok {
		return nil
	}
	var in map[string]any
	if json.Unmarshal(body, &in) != nil {
		return nil
	}
	text, format := "", ""
	switch t := in[name].(type) {
	case string:
		text = t
	case map[string]any:
		if st, _ := t["state"].(string); st == "refused" || st == "unavailable" {
			return nil
		}
		text, _ = t["text"].(string)
		format, _ = t["format"].(string)
	default:
		return nil
	}
	if why := ScanTextProblem(text); why != "" {
		return []string{fmt.Sprintf("%s: %s", name, why)}
	}
	if !ScanTakes(p, format) {
		if format == "" {
			return []string{fmt.Sprintf("%s: this needs the code scanned; a code typed is not taken", name)}
		}
		return []string{fmt.Sprintf("%s: this does not take %s codes", name, format)}
	}
	return nil
}

// ReadScanInput puts a manual start's scan input as the run keeps it,
// {state, source, text, format}: a code with a format was read by the
// camera, one without was typed. The source is set here, never taken
// from the body (device fields §3). Answers whether body changed.
func (d *Definition) ReadScanInput(body map[string]any) bool {
	name, _, ok := ScanInput(d)
	if !ok || body == nil {
		return false
	}
	out := map[string]any{"state": "answered"}
	switch t := body[name].(type) {
	case string:
		out["text"], out["source"] = t, "typed"
	case map[string]any:
		if st, _ := t["state"].(string); st == "refused" || st == "unavailable" {
			out = map[string]any{"state": st}
			if r, _ := t["reason"].(string); strings.TrimSpace(r) != "" {
				out["reason"] = r
			}
			break
		}
		out["text"], _ = t["text"].(string)
		out["source"] = "typed"
		if f, _ := t["format"].(string); f != "" {
			out["format"], out["source"] = f, "camera"
		}
	default:
		return false
	}
	body[name] = out
	return true
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
		if kind == DeviceSignature {
			props["signed_by"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 120}
		}
	}
	return map[string]any{"type": "object", "required": []any{"state"}, "additionalProperties": false, "properties": props}
}
