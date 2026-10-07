// Package whatsapptest is a fake WhatsApp Cloud API for tests: it accepts
// sends from the platform number (and tenants' own numbers, each with its
// token), records them, answers the calls made when a number is connected,
// and builds webhook deliveries and Flows requests as Meta would send them.
package whatsapptest

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Sent is one message the fake accepted.
type Sent struct {
	From     string // the sending phone number id
	To       string
	Type     string // text, interactive or template
	Text     string // the body; a template rendered from the library
	Template string
	Buttons  []whatsapp.Button // interactive reply buttons
	Payloads []string          // template quick-reply payloads
	// A Flow (interactive type "flow").
	Flow       string
	FlowToken  string
	FlowScreen string
	FlowData   map[string]any
	FlowAction string
}

// Graph is the fake Graph API.
type Graph struct {
	*httptest.Server
	PhoneNumberID, Token string

	mu      sync.Mutex
	numbers map[string]string // phone number id -> token
	display map[string]string // phone number id -> display number
	keys    map[string]string // phone number id -> Flows public key uploaded
	sent    []Sent
	// Fail, when set, answers a send with this Graph error code.
	Fail int
}

// New starts a fake for the given phone number id and token.
func New(t testing.TB, phoneNumberID, token string) *Graph {
	g := &Graph{PhoneNumberID: phoneNumberID, Token: token, numbers: map[string]string{phoneNumberID: token},
		display: map[string]string{phoneNumberID: "+1 555-000-1111"}, keys: map[string]string{}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

// AddNumber makes the fake know another phone number (a tenant's own).
func (g *Graph) AddNumber(phoneNumberID, token, display string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.numbers[phoneNumberID], g.display[phoneNumberID] = token, display
}

// FlowsKey is the Flows public key uploaded to a number ("" if none).
func (g *Graph) FlowsKey(phoneNumberID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.keys[phoneNumberID]
}

func refuse(w http.ResponseWriter, status int, code int, msg string) {
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"message":%q,"code":%d,"fbtrace_id":"fake"}}`, msg, code)
}

func (g *Graph) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) > 0 && strings.HasPrefix(parts[0], "v") && len(parts) > 1 {
		parts = parts[1:] // a version prefix in the base URL
	}
	g.mu.Lock()
	token, known := g.numbers[parts[0]]
	g.mu.Unlock()
	if !known || r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid OAuth access token.","type":"OAuthException","code":190}}`))
		return
	}
	pnid := parts[0]
	switch {
	case r.Method == http.MethodGet && len(parts) == 1:
		g.mu.Lock()
		d := g.display[pnid]
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"display_phone_number":%q,"verified_name":"Fake","id":%q}`, d, pnid) //nolint:gosec // a test fake answering JSON
		return
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "whatsapp_business_encryption":
		if err := r.ParseForm(); err != nil || !strings.Contains(r.PostForm.Get("business_public_key"), "PUBLIC KEY") {
			refuse(w, http.StatusBadRequest, 100, "Invalid parameter")
			return
		}
		g.mu.Lock()
		g.keys[pnid] = r.PostForm.Get("business_public_key")
		g.mu.Unlock()
		_, _ = w.Write([]byte(`{"success":true}`))
		return
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "messages":
	default:
		http.NotFound(w, r)
		return
	}
	var m struct {
		To          string                `json:"to"`
		Type        string                `json:"type"`
		Text        struct{ Body string } `json:"text"`
		Interactive struct {
			Type   string                `json:"type"`
			Body   struct{ Text string } `json:"body"`
			Action struct {
				Name    string `json:"name"`
				Buttons []struct {
					Reply struct{ ID, Title string } `json:"reply"`
				} `json:"buttons"`
				Parameters struct {
					Version string `json:"flow_message_version"`
					Token   string `json:"flow_token"`
					Name    string `json:"flow_name"`
					CTA     string `json:"flow_cta"`
					Action  string `json:"flow_action"`
					Payload struct {
						Screen string         `json:"screen"`
						Data   map[string]any `json:"data"`
					} `json:"flow_action_payload"`
				} `json:"parameters"`
			} `json:"action"`
		} `json:"interactive"`
		Template struct {
			Name       string `json:"name"`
			Components []struct {
				Type       string `json:"type"`
				SubType    string `json:"sub_type"`
				Parameters []struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Payload string `json:"payload"`
				} `json:"parameters"`
			} `json:"components"`
		} `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	g.mu.Lock()
	fail := g.Fail
	g.mu.Unlock()
	if fail != 0 && (fail != 131047 || m.Type != "template") {
		refuse(w, http.StatusBadRequest, fail, "refused by the fake")
		return
	}
	s := Sent{From: pnid, To: m.To, Type: m.Type}
	switch m.Type {
	case "text":
		s.Text = m.Text.Body
	case "interactive":
		s.Text = m.Interactive.Body.Text
		for _, b := range m.Interactive.Action.Buttons {
			s.Buttons = append(s.Buttons, whatsapp.Button{ID: b.Reply.ID, Title: b.Reply.Title})
		}
		if m.Interactive.Type == "flow" {
			p := m.Interactive.Action.Parameters
			if m.Interactive.Action.Name != "flow" || p.Version != "3" || p.Token == "" || p.Name == "" || p.CTA == "" || len(p.CTA) > 30 ||
				(p.Action != "navigate" && p.Action != "data_exchange") || (p.Action == "navigate" && (p.Payload.Screen == "" || len(p.Payload.Data) == 0)) {
				refuse(w, http.StatusBadRequest, 131009, "Parameter value is not valid")
				return
			}
			s.Flow, s.FlowToken, s.FlowScreen, s.FlowData, s.FlowAction = p.Name, p.Token, p.Payload.Screen, p.Payload.Data, p.Action
		}
	case "template":
		s.Template = m.Template.Name
		tpl, ok := whatsapp.TemplateNamed(m.Template.Name)
		if !ok {
			refuse(w, http.StatusBadRequest, 132001, "Template name does not exist in the translation")
			return
		}
		vars := map[string]string{}
		for _, c := range m.Template.Components {
			switch {
			case c.Type == "body":
				if len(c.Parameters) != len(tpl.Vars) {
					refuse(w, http.StatusBadRequest, 132000, "Number of parameters does not match the expected number of params")
					return
				}
				for i, p := range c.Parameters {
					if strings.ContainsAny(p.Text, "\n\t") || strings.Contains(p.Text, "     ") {
						refuse(w, http.StatusBadRequest, 132018, "Param text cannot have new-line/tab characters or more than 4 consecutive spaces")
						return
					}
					vars[tpl.Vars[i]] = p.Text
				}
			case c.Type == "button" && c.SubType == "quick_reply":
				for _, p := range c.Parameters {
					s.Payloads = append(s.Payloads, p.Payload)
				}
			}
		}
		s.Text = tpl.Render(vars)
	}
	g.mu.Lock()
	g.sent = append(g.sent, s)
	n := len(g.sent)
	g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"messaging_product":"whatsapp","contacts":[{"input":%q,"wa_id":%q}],"messages":[{"id":"wamid.fake%d"}]}`, m.To, strings.TrimPrefix(m.To, "+"), n)
}

// SetFail makes sends fail with a Graph error code (0: succeed). 131047
// fails free-form messages only, as when the window has closed.
func (g *Graph) SetFail(code int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Fail = code
}

// Sent lists the messages to a number (all with "").
func (g *Graph) Sent(to string) []Sent {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Sent
	for _, s := range g.sent {
		if to == "" || s.To == to {
			out = append(out, s)
		}
	}
	return out
}

// Last is the latest message to a number; it fails the test if none.
func (g *Graph) Last(t testing.TB, to string) Sent {
	t.Helper()
	s := g.Sent(to)
	if len(s) == 0 {
		t.Fatalf("nothing sent to %s", to)
	}
	return s[len(s)-1]
}

// Reset forgets what was sent.
func (g *Graph) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sent = nil
}

// Delivery builds a webhook body with one inbound message from a number
// (E.164) to the platform number. msg is the message object without from
// and id.
func Delivery(phoneNumberID, from, id string, msg map[string]any) []byte {
	msg["from"] = strings.TrimPrefix(from, "+")
	msg["id"] = id
	msg["timestamp"] = "1759800000"
	body, _ := json.Marshal(map[string]any{"object": "whatsapp_business_account", "entry": []any{map[string]any{
		"id": "WABA", "changes": []any{map[string]any{"field": "messages", "value": map[string]any{
			"messaging_product": "whatsapp",
			"metadata":          map[string]any{"display_phone_number": "15550001111", "phone_number_id": phoneNumberID},
			"contacts":          []any{map[string]any{"profile": map[string]any{"name": "Someone"}, "wa_id": strings.TrimPrefix(from, "+")}},
			"messages":          []any{msg},
		}}},
	}}})
	return body
}

// Text is an inbound text message.
func Text(body string) map[string]any {
	return map[string]any{"type": "text", "text": map[string]any{"body": body}}
}

// ButtonReply is a tapped interactive reply button.
func ButtonReply(id, title string) map[string]any {
	return map[string]any{"type": "interactive", "interactive": map[string]any{"type": "button_reply", "button_reply": map[string]any{"id": id, "title": title}}}
}

// QuickReply is a tapped template quick-reply button.
func QuickReply(payload, text string) map[string]any {
	return map[string]any{"type": "button", "button": map[string]any{"payload": payload, "text": text}}
}

// FlowCompleted is the message WhatsApp sends when a person completes a
// Flow (interactive nfm_reply), carrying the flow token.
func FlowCompleted(token string) map[string]any {
	resp, _ := json.Marshal(map[string]any{"flow_token": token})
	return map[string]any{"type": "interactive", "context": map[string]any{"from": "15550001111", "id": "wamid.flow"},
		"interactive": map[string]any{"type": "nfm_reply", "nfm_reply": map[string]any{"name": "flow", "body": "Sent", "response_json": string(resp)}}}
}

// FlowRequest encrypts a Flows data endpoint request as the WhatsApp
// client does (version 3.0); open decrypts the endpoint's answer.
func FlowRequest(t testing.TB, pub *rsa.PublicKey, action, screen, token string, data map[string]any) (body []byte, open func([]byte) map[string]any) {
	t.Helper()
	req := map[string]any{"version": "3.0", "action": action, "flow_token": token}
	if screen != "" {
		req["screen"] = screen
	}
	if data != nil {
		req["data"] = data
	}
	body, dec, err := whatsapp.EncryptFlowRequest(pub, req)
	if err != nil {
		t.Fatal(err)
	}
	return body, func(resp []byte) map[string]any {
		t.Helper()
		plain, err := dec(resp)
		if err != nil {
			t.Fatalf("the endpoint's answer does not decrypt: %v (%q)", err, resp)
		}
		var out map[string]any
		if err := json.Unmarshal(plain, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
}
