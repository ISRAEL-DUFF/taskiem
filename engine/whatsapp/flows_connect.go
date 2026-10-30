package whatsapp

import (
	"strconv"
	"strings"
)

// The key form (docs/spec/chat-way-in.md 7.2): taskiem_connect, one screen
// of up to ConnectSlots fields, each a password box (a secret) or a text
// box, labelled with the service's own help, encrypted to Taskiem's Flows
// endpoint like the PIN. A key typed here never passes through the chat.
const (
	FlowConnect   = "taskiem_connect"
	ScreenConnect = "CONNECT"
	// ConnectSlots is how many fields the key form holds; a service needing
	// more is connected on the connect page.
	ConnectSlots = 5
)

// ConnectField is one value the key form asks for.
type ConnectField struct {
	Key, Label, Help string
	Secret, Required bool
}

func connectSlot(secret bool, i int) string {
	if secret {
		return "p" + strconv.Itoa(i+1)
	}
	return "x" + strconv.Itoa(i+1)
}

// ConnectForm is the key form's screen data: each field in a password or
// text slot, the rest hidden; errs, keyed by field, show under them.
func ConnectForm(title, intro string, fields []ConnectField, errs map[string]string, problem string) (map[string]any, error) {
	if len(fields) > ConnectSlots {
		return nil, ErrFlowUnsupported
	}
	d := map[string]any{"title": clip(title, 80), "intro": clip(intro, 400), "error": clip(problem, 200), "has_error": problem != ""}
	for i := range ConnectSlots {
		for _, secret := range []bool{true, false} {
			k := connectSlot(secret, i)
			d[k+"_on"], d[k+"_req"], d[k+"_label"], d[k+"_help"], d[k+"_err"] = false, false, "-", "", ""
		}
	}
	for i, f := range fields {
		k := connectSlot(f.Secret, i)
		d[k+"_on"], d[k+"_req"], d[k+"_label"], d[k+"_help"] = true, f.Required, clip(f.Label, 20), clip(f.Help, 80)
		if e := errs[f.Key]; e != "" {
			d[k+"_err"] = clip(e, 80)
		}
	}
	return d, nil
}

// ConnectValues reads a submitted key form: each field's value, trimmed.
func ConnectValues(fields []ConnectField, form map[string]any) map[string]string {
	out := map[string]string{}
	for i, f := range fields {
		if v, ok := form[connectSlot(f.Secret, i)].(string); ok && v != "" {
			out[f.Key] = strings.TrimSpace(v)
		}
	}
	return out
}

// ConnectFlowJSON is the taskiem_connect Flow as submitted to Meta.
func ConnectFlowJSON() []byte {
	data := map[string]any{}
	sample, _ := ConnectForm("Connect Paystack", "Copy your secret key from Paystack and paste it here.", nil, nil, "")
	for k, v := range sample {
		data[k] = example(v)
	}
	children := []any{
		map[string]any{"type": "TextHeading", "text": dyn("title")},
		map[string]any{"type": "TextBody", "text": dyn("intro")},
		map[string]any{"type": "TextBody", "text": dyn("error"), "visible": dyn("has_error"), "font-weight": "bold"},
	}
	payload := map[string]any{}
	for i := range ConnectSlots {
		for _, secret := range []bool{true, false} {
			k := connectSlot(secret, i)
			payload[k] = "${form." + k + "}"
			kind := "text"
			if secret {
				kind = "password"
			}
			children = append(children, map[string]any{"type": "TextInput", "name": k, "input-type": kind, "label": dyn(k + "_label"), "required": dyn(k + "_req"),
				"visible": dyn(k + "_on"), "helper-text": dyn(k + "_help"), "error-message": dyn(k + "_err")})
		}
	}
	children = append(children, map[string]any{"type": "Footer", "label": "Connect",
		"on-click-action": map[string]any{"name": "data_exchange", "payload": payload}})
	return flowJSON(ScreenConnect, "Connect", data, children)
}
