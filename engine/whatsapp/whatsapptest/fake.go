// Package whatsapptest is a fake WhatsApp Cloud API for tests: it accepts
// sends from the platform number, records them, and builds webhook
// deliveries as Meta would send them.
package whatsapptest

import (
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
	To       string
	Type     string // text, interactive or template
	Text     string // the body; a template rendered from the library
	Template string
	Buttons  []whatsapp.Button // interactive reply buttons
	Payloads []string          // template quick-reply payloads
}

// Graph is the fake Graph API.
type Graph struct {
	*httptest.Server
	PhoneNumberID, Token string

	mu   sync.Mutex
	sent []Sent
	// Fail, when set, answers a send with this Graph error code.
	Fail int
}

// New starts a fake for the given phone number id and token.
func New(t testing.TB, phoneNumberID, token string) *Graph {
	g := &Graph{PhoneNumberID: phoneNumberID, Token: token}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

func (g *Graph) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+g.Token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid OAuth access token.","type":"OAuthException","code":190}}`))
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/"+g.PhoneNumberID+"/messages" {
		http.NotFound(w, r)
		return
	}
	var m struct {
		To          string                `json:"to"`
		Type        string                `json:"type"`
		Text        struct{ Body string } `json:"text"`
		Interactive struct {
			Body   struct{ Text string } `json:"body"`
			Action struct {
				Buttons []struct {
					Reply struct{ ID, Title string } `json:"reply"`
				} `json:"buttons"`
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
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"refused by the fake","code":%d,"fbtrace_id":"fake"}}`, fail)
		return
	}
	s := Sent{To: m.To, Type: m.Type}
	switch m.Type {
	case "text":
		s.Text = m.Text.Body
	case "interactive":
		s.Text = m.Interactive.Body.Text
		for _, b := range m.Interactive.Action.Buttons {
			s.Buttons = append(s.Buttons, whatsapp.Button{ID: b.Reply.ID, Title: b.Reply.Title})
		}
	case "template":
		s.Template = m.Template.Name
		tpl, ok := whatsapp.TemplateNamed(m.Template.Name)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Template name does not exist in the translation","code":132001}}`))
			return
		}
		vars := map[string]string{}
		for _, c := range m.Template.Components {
			switch {
			case c.Type == "body":
				if len(c.Parameters) != len(tpl.Vars) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":{"message":"Number of parameters does not match the expected number of params","code":132000}}`))
					return
				}
				for i, p := range c.Parameters {
					if strings.ContainsAny(p.Text, "\n\t") || strings.Contains(p.Text, "     ") {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":{"message":"Param text cannot have new-line/tab characters or more than 4 consecutive spaces","code":132018}}`))
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
