package whatsapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// WhatsApp Flows (spec 11.1, 11.2): forms inside WhatsApp. Taskiem
// publishes two Flows once per WhatsApp Business Account, under fixed
// names, and fills them per use from its data endpoint:
//
//   - taskiem_inputs: a generic form of FlowSlots slots, each a text box,
//     a number box or a list, shown or hidden, labelled and required by
//     the data a workflow's input schema generates (FlowForm). Submitting
//     sends the form to the data endpoint, which checks it against the
//     schema and answers with the errors or completes the Flow.
//   - taskiem_pin: one passcode box for the WhatsApp approval PIN, bound to
//     one decision by its flow token.
//
// Flow JSON is static and must be published to Meta before it is sent;
// InputsFlowJSON and PinFlowJSON generate the definitions submitted
// (docs/whatsapp-flows/). Built from Meta's public Flows documentation
// (Flow JSON, components, sending a Flow, implementing endpoints).

// Flow names, as published in the WhatsApp Business Account.
const (
	FlowInputs = "taskiem_inputs"
	FlowPin    = "taskiem_pin"
)

// Screens.
const (
	ScreenInputs = "INPUTS"
	ScreenPin    = "PIN"
)

// FlowJSONVersion and FlowDataAPIVersion are the versions the Flows are
// written for.
const (
	FlowJSONVersion    = "7.3"
	FlowDataAPIVersion = "3.0"
)

// FlowSlots is how many fields the inputs Flow can hold; a workflow
// needing more is asked field by field in chat.
const FlowSlots = 10

// maxFlowOptions is the most entries a Dropdown takes.
const maxFlowOptions = 200

// slot kinds: a text box, a number box, a list (enums and yes/no).
const (
	slotText   = "t"
	slotNumber = "n"
	slotChoice = "c"
)

var slotKinds = []string{slotText, slotNumber, slotChoice}

// ErrFlowUnsupported: the inputs cannot be shown in the inputs Flow (too
// many fields, or a list too long); chat collects them instead.
var ErrFlowUnsupported = errors.New("these inputs do not fit a WhatsApp form")

func slotKind(f Field) string {
	switch {
	case len(f.Enum) > 0 || f.Type == "boolean":
		return slotChoice
	case f.Type == "integer" || f.Type == "number":
		return slotNumber
	}
	return slotText
}

func slotKey(kind string, i int) string { return kind + strconv.Itoa(i+1) }

// FlowOption is a list entry.
type FlowOption struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func (f Field) options() []FlowOption {
	if f.Type == "boolean" && len(f.Enum) == 0 {
		return []FlowOption{{ID: "yes", Title: "Yes"}, {ID: "no", Title: "No"}}
	}
	out := make([]FlowOption, len(f.Enum))
	for i, e := range f.Enum {
		out[i] = FlowOption{ID: strconv.Itoa(i), Title: clip(fmt.Sprint(e), 30)}
	}
	return out
}

// FlowForm is the inputs Flow's screen data for fields: each field in its
// slot, in order, the rest hidden. errs, keyed by field name, show under
// their fields.
func FlowForm(title string, fields []Field, errs map[string]string) (map[string]any, error) {
	if len(fields) > FlowSlots {
		return nil, ErrFlowUnsupported
	}
	d := map[string]any{"title": clip(title, 80), "intro": "Fill in the details, then tap Continue. You confirm in the chat before anything starts.",
		"error": "", "has_error": false}
	for i := range FlowSlots {
		for _, k := range slotKinds {
			key := slotKey(k, i)
			d[key+"_on"], d[key+"_req"], d[key+"_label"], d[key+"_help"], d[key+"_err"] = false, false, "-", "", ""
			if k == slotChoice {
				d[key+"_opts"] = []FlowOption{{ID: "-", Title: "-"}}
			}
		}
	}
	for i, f := range fields {
		k := slotKind(f)
		key := slotKey(k, i)
		label := f.Title
		if label == "" {
			label = f.Name
		}
		help := f.Description
		if r := []rune(label); len(r) > 20 && help == "" {
			help = label
		}
		d[key+"_on"], d[key+"_req"], d[key+"_label"], d[key+"_help"] = true, true, clip(label, 20), clip(help, 80)
		if k == slotChoice {
			opts := f.options()
			if len(opts) > maxFlowOptions {
				return nil, ErrFlowUnsupported
			}
			d[key+"_opts"] = opts
		}
		if e := errs[f.Name]; e != "" {
			d[key+"_err"] = clip(e, 80)
		}
	}
	if len(errs) > 0 {
		d["error"], d["has_error"] = "Some answers do not fit; check the fields marked.", true
	}
	return d, nil
}

// FlowValues reads a submitted inputs form: each field's answer, parsed
// and checked against its schema. Problems are keyed by field name.
func FlowValues(fields []Field, form map[string]any) (map[string]any, map[string]string) {
	vals, errs := map[string]any{}, map[string]string{}
	for i, f := range fields {
		k := slotKind(f)
		raw, ok := form[slotKey(k, i)]
		var s string
		switch v := raw.(type) {
		case string:
			s = v
		case float64:
			s = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			s = strconv.FormatBool(v)
		case nil:
		default:
			ok = false
		}
		s = strings.TrimSpace(s)
		if !ok || s == "" {
			errs[f.Name] = "this is required"
			continue
		}
		if k == slotChoice {
			var v any
			if f.Type == "boolean" && len(f.Enum) == 0 {
				switch s {
				case "yes":
					v = true
				case "no":
					v = false
				}
			} else if n, err := strconv.Atoi(s); err == nil && n >= 0 && n < len(f.Enum) {
				v = f.Enum[n]
			}
			if v == nil {
				errs[f.Name] = "choose one of the options"
				continue
			}
			if err := f.Check(v); err != nil {
				errs[f.Name] = err.Error()
				continue
			}
			vals[f.Name] = v
			continue
		}
		v, err := f.Parse(s)
		if err != nil {
			errs[f.Name] = err.Error()
			continue
		}
		vals[f.Name] = v
	}
	return vals, errs
}

// PinForm is the PIN Flow's screen data.
func PinForm(title, summary, problem string) map[string]any {
	return map[string]any{"title": clip(title, 80), "summary": clip(summary, 1000), "error": problem, "has_error": problem != ""}
}

// --- flow tokens ---

// A flow token names one Flow sent: "wf1." and base64url of the tenant
// (16 bytes) and a random secret (24 bytes). The tenant says where to look;
// only the secret's SHA-256 is stored, in the tenant's whatsapp_flows row,
// so a token proves nothing without the row and the row cannot be turned
// back into a token.
const flowTokenPrefix = "wf1."

// ErrBadFlowToken: a flow token is malformed.
var ErrBadFlowToken = errors.New("whatsapp: invalid flow token")

// NewFlowToken returns a fresh token for a tenant and the hash to store.
func NewFlowToken(tenant uuid.UUID) (string, []byte) {
	raw := make([]byte, 40)
	copy(raw, tenant[:])
	_, _ = rand.Read(raw[16:])
	h := sha256.Sum256(raw[16:])
	return flowTokenPrefix + b64.EncodeToString(raw), h[:]
}

// ParseFlowToken reads a token's tenant and the hash to look it up by.
func ParseFlowToken(tok string) (uuid.UUID, []byte, error) {
	rest, ok := strings.CutPrefix(tok, flowTokenPrefix)
	if !ok || len(rest) > 64 {
		return uuid.Nil, nil, ErrBadFlowToken
	}
	raw, err := b64.DecodeString(rest)
	if err != nil || len(raw) != 40 {
		return uuid.Nil, nil, ErrBadFlowToken
	}
	var t uuid.UUID
	copy(t[:], raw[:16])
	h := sha256.Sum256(raw[16:])
	return t, h[:], nil
}

// --- sending ---

// FlowMessage is a Flow to send.
type FlowMessage struct {
	Flow   string // FlowInputs or FlowPin (sent by name)
	Token  string
	Body   string
	CTA    string // the button, at most 30 characters
	Screen string
	Data   map[string]any
}

// SendFlow sends a Flow (inside the window only), opening on its screen
// with its data: the "navigate" action, so the form shows without a round
// trip to the endpoint; submitting it calls the endpoint.
func (c *Client) SendFlow(ctx context.Context, to string, m FlowMessage) (string, error) {
	return c.send(ctx, to, "interactive", map[string]any{
		"type": "flow",
		"body": map[string]any{"text": clip(m.Body, 1024)},
		"action": map[string]any{"name": "flow", "parameters": map[string]any{
			"flow_message_version": "3", "flow_token": m.Token, "flow_name": m.Flow, "flow_cta": clip(m.CTA, 30),
			"flow_action": "navigate", "flow_action_payload": map[string]any{"screen": m.Screen, "data": m.Data},
		}},
	})
}

// --- Flow JSON for submission ---

func dyn(key string) string { return "${data." + key + "}" }

func example(v any) map[string]any {
	switch x := v.(type) {
	case bool:
		return map[string]any{"type": "boolean", "__example__": x}
	case []FlowOption:
		return map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
			"id": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}}}, "__example__": x}
	default:
		return map[string]any{"type": "string", "__example__": fmt.Sprint(x)}
	}
}

// InputsFlowJSON is the taskiem_inputs Flow as submitted to Meta.
func InputsFlowJSON() []byte {
	data := map[string]any{}
	sample, _ := FlowForm("Pay supplier", nil, nil)
	for k, v := range sample {
		data[k] = example(v)
	}
	children := []any{
		map[string]any{"type": "TextHeading", "text": dyn("title")},
		map[string]any{"type": "TextBody", "text": dyn("intro")},
		map[string]any{"type": "TextBody", "text": dyn("error"), "visible": dyn("has_error"), "font-weight": "bold"},
	}
	payload := map[string]any{}
	for i := range FlowSlots {
		for _, k := range slotKinds {
			key := slotKey(k, i)
			payload[key] = "${form." + key + "}"
			c := map[string]any{"name": key, "label": dyn(key + "_label"), "required": dyn(key + "_req"), "visible": dyn(key + "_on"),
				"error-message": dyn(key + "_err")}
			switch k {
			case slotText:
				c["type"], c["input-type"], c["helper-text"] = "TextInput", "text", dyn(key+"_help")
			case slotNumber:
				c["type"], c["input-type"], c["helper-text"] = "TextInput", "number", dyn(key+"_help")
			case slotChoice:
				c["type"], c["data-source"] = "Dropdown", dyn(key+"_opts")
			}
			children = append(children, c)
		}
	}
	children = append(children, map[string]any{"type": "Footer", "label": "Continue",
		"on-click-action": map[string]any{"name": "data_exchange", "payload": payload}})
	return flowJSON(ScreenInputs, "Details", data, children)
}

// PinFlowJSON is the taskiem_pin Flow as submitted to Meta.
func PinFlowJSON() []byte {
	data := map[string]any{}
	for k, v := range PinForm("Approve disburse", "Pay supplier · prod", "") {
		data[k] = example(v)
	}
	children := []any{
		map[string]any{"type": "TextHeading", "text": dyn("title")},
		map[string]any{"type": "TextBody", "text": dyn("summary")},
		map[string]any{"type": "TextInput", "name": "pin", "label": "Approval PIN", "input-type": "passcode", "required": true,
			"min-chars": strconv.Itoa(PinLength), "max-chars": strconv.Itoa(PinLength), "helper-text": "The WhatsApp PIN you set in Taskiem"},
		map[string]any{"type": "TextBody", "text": dyn("error"), "visible": dyn("has_error"), "font-weight": "bold"},
		map[string]any{"type": "Footer", "label": "Confirm", "on-click-action": map[string]any{"name": "data_exchange", "payload": map[string]any{"pin": "${form.pin}"}}},
	}
	return flowJSON(ScreenPin, "Confirm decision", data, children)
}

func flowJSON(screen, title string, data map[string]any, children []any) []byte {
	out, _ := json.MarshalIndent(map[string]any{
		"version": FlowJSONVersion, "data_api_version": FlowDataAPIVersion,
		"routing_model": map[string]any{screen: []any{}},
		"screens": []any{map[string]any{"id": screen, "title": title, "terminal": true, "data": data,
			"layout": map[string]any{"type": "SingleColumnLayout", "children": children}}},
	}, "", "  ")
	return append(out, '\n')
}

// PinLength is the WhatsApp approval PIN's length.
const PinLength = 6
