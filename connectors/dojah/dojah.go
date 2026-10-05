// Package dojah is the first-party Dojah identity connector (spec 6.5).
package dojah

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

// SandboxURL is used when the connection sets sandbox=true.
const SandboxURL = "https://sandbox.dojah.io"

type Options struct{ BaseURL string }

func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	m.OverrideBaseURL(o.BaseURL)
	c := &client{base: strings.TrimRight(m.BaseURL, "/"), override: strings.TrimRight(o.BaseURL, "/")}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"lookup_bvn":    connector.ActionFunc(c.lookupBVN),
		"lookup_nin":    connector.ActionFunc(c.lookupNIN),
		"check_balance": connector.ActionFunc(c.balance),
	}}
}

type client struct{ base, override string }

func (c *client) get(ctx context.Context, req connector.Request, path string, q url.Values) (map[string]any, error) {
	if req.Credentials["app_id"] == "" || req.Credentials["secret_key"] == "" {
		return nil, fmt.Errorf("missing app_id or secret_key: %w", effects.ErrFatal)
	}
	base := c.base
	if req.Credentials["sandbox"] == "true" {
		base = SandboxURL
	}
	if c.override != "" {
		base = c.override
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var out struct {
		Entity map[string]any `json:"entity"`
	}
	// Dojah takes the secret key as-is in Authorization (no Bearer prefix).
	err := connector.DoJSON(ctx, req.HTTP, http.MethodGet, u, map[string]string{"AppId": req.Credentials["app_id"], "Authorization": req.Credentials["secret_key"]}, nil, &out)
	if connector.StatusOf(err) == http.StatusNotFound {
		return nil, fmt.Errorf("identity not found: %w: %w", err, effects.ErrFatal)
	}
	return out.Entity, err
}

func (c *client) lookupBVN(ctx context.Context, req connector.Request) (connector.Response, error) {
	bvn, _ := req.Input["bvn"].(string)
	e, err := c.get(ctx, req, "/api/v1/kyc/bvn/full", url.Values{"bvn": {bvn}})
	if err != nil {
		return connector.Response{}, err
	}
	if inc, _ := req.Input["include_image"].(bool); !inc {
		delete(e, "image")
	}
	return connector.Response{Output: e}, nil
}

func (c *client) lookupNIN(ctx context.Context, req connector.Request) (connector.Response, error) {
	nin, _ := req.Input["nin"].(string)
	e, err := c.get(ctx, req, "/api/v1/kyc/nin", url.Values{"nin": {nin}})
	if err != nil {
		return connector.Response{}, err
	}
	delete(e, "photo")
	return connector.Response{Output: e}, nil
}

func (c *client) balance(ctx context.Context, req connector.Request) (connector.Response, error) {
	e, err := c.get(ctx, req, "/api/v1/balance", nil)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"wallet_balance": e["wallet_balance"]}}, nil
}
