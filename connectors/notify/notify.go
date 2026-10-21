// Package notify is the Tell me connector (notify@1): a message to the
// business's own people, on the WhatsApp or Telegram they linked to
// Taskiem, or by email (no-technical-steps N4). Like storefront@1 it acts
// for the run's tenant only (connector.CallerFrom) and needs no
// connection, and it only ever reaches the tenant's members.
package notify

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Message is one Tell me step's message.
type Message struct {
	Tenant      uuid.UUID
	Run         uuid.UUID
	Step        string
	Environment string
	// To is owner, role or members; Role and Members say which.
	To      string
	Role    string
	Members []string
	Title   string
	Text    string
	// Key makes it idempotent: a retry reaches nobody twice.
	Key string
}

// Result is who it went to.
type Result struct {
	People, Chat, Email int
}

// Text is one SMS to a customer.
type Text struct {
	Tenant uuid.UUID
	To     string
	Text   string
	Key    string
}

// Teller delivers messages: Notifier in the platform.
type Teller interface {
	Tell(ctx context.Context, m Message) (Result, error)
	// Text queues an SMS to a customer; over is true when the month's
	// texts are used up (nothing is sent, the step still succeeds).
	Text(ctx context.Context, t Text) (over bool, err error)
}

// New returns the connector; t nil makes it fail plainly.
func New(t Teller) *connector.Connector {
	m := connector.MustParse(manifest)
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"tell": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			c, ok := connector.CallerFrom(ctx)
			if !ok {
				return connector.Response{}, fmt.Errorf("Tell me runs only in a workflow run: %w", effects.ErrFatal)
			}
			if t == nil {
				return connector.Response{}, fmt.Errorf("Tell me is not available on this deployment: %w", effects.ErrFatal)
			}
			msg := Message{Tenant: c.Tenant, Run: c.Run, Step: c.Step, Environment: c.Environment, Key: req.IdempotencyKey}
			msg.To, _ = req.Input["to"].(string)
			msg.Role, _ = req.Input["role"].(string)
			msg.Title, _ = req.Input["title"].(string)
			msg.Text, _ = req.Input["message"].(string)
			if list, ok := req.Input["members"].([]any); ok {
				for _, v := range list {
					if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
						msg.Members = append(msg.Members, strings.TrimSpace(s))
					}
				}
			}
			if msg.To == "" {
				msg.To = "owner"
			}
			switch {
			case strings.TrimSpace(msg.Text) == "":
				return connector.Response{}, fmt.Errorf("a message is required: %w", effects.ErrFatal)
			case msg.To == "role" && msg.Role == "":
				return connector.Response{}, fmt.Errorf("to: role needs the role: %w", effects.ErrFatal)
			case msg.To == "members" && len(msg.Members) == 0:
				return connector.Response{}, fmt.Errorf("to: members needs their emails: %w", effects.ErrFatal)
			}
			res, err := t.Tell(ctx, msg)
			if err != nil {
				return connector.Response{}, err
			}
			return connector.Response{Output: map[string]any{"people": res.People, "chat": res.Chat, "email": res.Email, "nobody": res.People == 0}}, nil
		}),
		"text": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			c, ok := connector.CallerFrom(ctx)
			if !ok {
				return connector.Response{}, fmt.Errorf("texting a customer runs only in a workflow run: %w", effects.ErrFatal)
			}
			if t == nil {
				return connector.Response{}, fmt.Errorf("texting is not available on this deployment: %w", effects.ErrFatal)
			}
			to, _ := req.Input["to"].(string)
			msg, _ := req.Input["message"].(string)
			if strings.TrimSpace(to) == "" || strings.TrimSpace(msg) == "" {
				return connector.Response{}, fmt.Errorf("a phone number and a message are required: %w", effects.ErrFatal)
			}
			over, err := t.Text(ctx, Text{Tenant: c.Tenant, To: to, Text: msg, Key: req.IdempotencyKey})
			if err != nil {
				return connector.Response{}, err
			}
			return connector.Response{Output: map[string]any{"queued": !over, "over_allowance": over}}, nil
		}),
	}}
}
