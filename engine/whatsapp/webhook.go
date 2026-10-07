package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// VerifySignature checks Meta's X-Hub-Signature-256 header: "sha256=" and
// the hex HMAC-SHA256 of the raw body under the app secret.
func VerifySignature(appSecret string, body []byte, header string) bool {
	if appSecret == "" {
		return false
	}
	got, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}

// Sign computes the header value for body (tests, the fake Graph API).
func Sign(appSecret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Inbound is one message a person sent the platform number.
type Inbound struct {
	ID            string // the wamid: deduplication
	From          string // the sender's WhatsApp ID (digits, no +)
	PhoneNumberID string // the business number it was sent to
	Type          string // text, interactive, button, ...
	Text          string // text body, or a button's title
	// Reply is a tapped button's id (interactive.button_reply.id) or a
	// template quick reply's payload (button.payload).
	Reply string
	// Flow is a completed Flow's response_json (interactive.nfm_reply):
	// it carries the flow token.
	Flow string
}

// FlowToken is the flow token of a completed Flow's response.
func (in Inbound) FlowToken() string {
	var r struct {
		Token string `json:"flow_token"`
	}
	if in.Flow == "" || json.Unmarshal([]byte(in.Flow), &r) != nil {
		return ""
	}
	return r.Token
}

// Parse extracts the inbound messages of a webhook delivery. Statuses and
// other fields are ignored: Meta sends everything the app subscribes to
// to one URL, and may batch many messages in one delivery.
func Parse(body []byte) ([]Inbound, error) {
	var d struct {
		Object string `json:"object"`
		Entry  []struct {
			Changes []struct {
				Field string `json:"field"`
				Value struct {
					Metadata struct {
						PhoneNumberID string `json:"phone_number_id"`
					} `json:"metadata"`
					Messages []struct {
						ID   string `json:"id"`
						From string `json:"from"`
						Type string `json:"type"`
						Text struct {
							Body string `json:"body"`
						} `json:"text"`
						Interactive struct {
							Type        string `json:"type"`
							ButtonReply struct {
								ID    string `json:"id"`
								Title string `json:"title"`
							} `json:"button_reply"`
							ListReply struct {
								ID    string `json:"id"`
								Title string `json:"title"`
							} `json:"list_reply"`
							NfmReply struct {
								Name         string `json:"name"`
								Body         string `json:"body"`
								ResponseJSON string `json:"response_json"`
							} `json:"nfm_reply"`
						} `json:"interactive"`
						Button struct {
							Payload string `json:"payload"`
							Text    string `json:"text"`
						} `json:"button"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, err
	}
	var out []Inbound
	for _, e := range d.Entry {
		for _, c := range e.Changes {
			if c.Field != "messages" {
				continue
			}
			for _, m := range c.Value.Messages {
				in := Inbound{ID: m.ID, From: m.From, PhoneNumberID: c.Value.Metadata.PhoneNumberID, Type: m.Type}
				switch m.Type {
				case "text":
					in.Text = m.Text.Body
				case "interactive":
					switch m.Interactive.Type {
					case "button_reply":
						in.Reply, in.Text = m.Interactive.ButtonReply.ID, m.Interactive.ButtonReply.Title
					case "list_reply":
						in.Reply, in.Text = m.Interactive.ListReply.ID, m.Interactive.ListReply.Title
					case "nfm_reply":
						in.Flow, in.Text = m.Interactive.NfmReply.ResponseJSON, m.Interactive.NfmReply.Body
					}
				case "button":
					in.Reply, in.Text = m.Button.Payload, m.Button.Text
				}
				out = append(out, in)
			}
		}
	}
	return out, nil
}
