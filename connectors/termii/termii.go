// Package termii is the first-party Termii messaging connector (spec 6.5).
package termii

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

type Options struct{ BaseURL string }

func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	base := strings.TrimRight(m.BaseURL, "/")
	c := &client{base: base}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"send_sms":      connector.ActionFunc(c.sendSMS),
		"check_balance": connector.ActionFunc(c.balance),
	}}
}

type client struct{ base string }

func (c *client) sendSMS(ctx context.Context, req connector.Request) (connector.Response, error) {
	key := req.Credentials["api_key"]
	if key == "" {
		return connector.Response{}, fmt.Errorf("missing api_key: %w", effects.ErrFatal)
	}
	from, _ := req.Input["from"].(string)
	if from == "" {
		from = req.Credentials["sender_id"]
	}
	channel, _ := req.Input["channel"].(string)
	if channel == "" {
		channel = "generic"
	}
	body := map[string]any{"api_key": key, "to": req.Input["to"], "from": from, "sms": req.Input["sms"], "type": "plain", "channel": channel}
	var out struct {
		MessageID string  `json:"message_id"`
		Message   string  `json:"message"`
		Balance   float64 `json:"balance"`
	}
	if err := connector.DoJSON(ctx, req.HTTP, http.MethodPost, c.base+"/api/sms/send", nil, body, &out); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"message_id": out.MessageID, "message": out.Message, "balance": out.Balance}}, nil
}

func (c *client) balance(ctx context.Context, req connector.Request) (connector.Response, error) {
	var out struct {
		Balance  float64 `json:"balance"`
		Currency string  `json:"currency"`
	}
	u := c.base + "/api/get-balance?api_key=" + url.QueryEscape(req.Credentials["api_key"])
	if err := connector.DoJSON(ctx, req.HTTP, http.MethodGet, u, nil, nil, &out); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"balance": out.Balance, "currency": out.Currency}}, nil
}
