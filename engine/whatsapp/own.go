package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Tenants' own numbers (spec 11.4). A tenant connects its own WhatsApp
// Business number; its credentials go in the tenant's vault under OwnEnv,
// and a Platform for it is built on demand from the shared one: the same
// signing key, Flows key, meter and Graph API, the tenant's phone number
// id and token. Messages to the tenant's people go from it; webhooks for
// it are routed by phone number id.

// OwnEnv is the vault environment holding own numbers' credentials.
const OwnEnv = "_whatsapp"

// Vault names of an own number's credentials.
const (
	OwnAccessToken = "access_token"
	OwnAppSecret   = "app_secret"
	OwnVerifyToken = "verify_token"
)

// Secrets reads a tenant's vault (secrets.Vault).
type Secrets interface {
	Get(ctx context.Context, tenant uuid.UUID, env, name string) (string, error)
}

// ownTTL is how long a process keeps an own number's Platform; a change
// made elsewhere takes effect within it.
const ownTTL = 30 * time.Second

type ownEntry struct {
	p  *Platform // nil: none
	at time.Time
}

func (p *Platform) cached(key string) (*Platform, bool) {
	v, ok := p.own.Get(key)
	if !ok || time.Since(v.at) > ownTTL {
		return nil, false
	}
	return v.p, true
}

// Forget drops what this process knows of own numbers (after a change).
func (p *Platform) Forget() { p.own.Clear() }

// ForTenant is the number a tenant's messages go from: its own, when it
// has one active, otherwise the shared number (p).
func (p *Platform) ForTenant(ctx context.Context, tenant uuid.UUID) (*Platform, error) {
	if p.Own() || p.Secrets == nil || tenant == uuid.Nil {
		return p, nil
	}
	if q, ok := p.cached("t:" + tenant.String()); ok {
		if q == nil {
			return p, nil
		}
		return q, nil
	}
	var pnid, display string
	var ownApp bool
	err := db.InTenantTx(ctx, p.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT phone_number_id, display_number, own_app FROM whatsapp_numbers WHERE tenant_id = $1 AND status = 'active'`, tenant).
			Scan(&pnid, &display, &ownApp)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		p.own.Put("t:"+tenant.String(), ownEntry{at: time.Now()})
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	q, err := p.build(ctx, tenant, pnid, display, ownApp)
	if err != nil {
		return nil, err
	}
	p.own.Put("t:"+tenant.String(), ownEntry{p: q, at: time.Now()})
	p.own.Put("n:"+pnid, ownEntry{p: q, at: time.Now()})
	return q, nil
}

// ForPhoneNumberID is the Platform a webhook for a phone number id is for:
// the shared number, a tenant's active own number, or nil.
func (p *Platform) ForPhoneNumberID(ctx context.Context, pnid string) (*Platform, error) {
	if pnid == p.Config.PhoneNumberID {
		return p, nil
	}
	if p.Own() || p.Secrets == nil || pnid == "" {
		return nil, nil
	}
	if q, ok := p.cached("n:" + pnid); ok {
		return q, nil
	}
	var tenant uuid.UUID
	var ownApp bool
	err := p.Pool.QueryRow(ctx, `SELECT tenant_id, own_app FROM taskiem_wa_number_route($1)`, pnid).Scan(&tenant, &ownApp)
	if errors.Is(err, pgx.ErrNoRows) {
		p.own.Put("n:"+pnid, ownEntry{at: time.Now()})
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	q, err := p.ForTenant(ctx, tenant)
	if err != nil || q == p || q.Config.PhoneNumberID != pnid {
		return nil, err
	}
	return q, nil
}

// build makes the Platform for an own number from the vault.
func (p *Platform) build(ctx context.Context, tenant uuid.UUID, pnid, display string, ownApp bool) (*Platform, error) {
	ctx = secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "whatsapp.number"})
	get := func(name string) (string, error) {
		v, err := p.Secrets.Get(ctx, tenant, OwnEnv, name)
		if err != nil {
			return "", fmt.Errorf("whatsapp: the own number's %s: %w", name, err)
		}
		return v, nil
	}
	cfg := p.Config
	cfg.PhoneNumberID, cfg.DisplayNumber = pnid, display
	var err error
	if cfg.AccessToken, err = get(OwnAccessToken); err != nil {
		return nil, err
	}
	if ownApp {
		if cfg.AppSecret, err = get(OwnAppSecret); err != nil {
			return nil, err
		}
		if cfg.VerifyToken, err = get(OwnVerifyToken); err != nil {
			return nil, err
		}
	}
	return &Platform{Pool: p.Pool, Config: cfg, Logger: p.Logger, Now: p.Now, Signer: p.Signer, Meter: p.Meter, Tenant: tenant, OwnApp: ownApp,
		Client: &Client{BaseURL: p.Client.BaseURL, PhoneNumberID: pnid, Token: cfg.AccessToken, Egress: p.Client.Egress, HTTP: p.Client.HTTP}}, nil
}

// --- Graph API calls for connecting a number ---

// NumberInfo is what the Graph API says of a phone number.
type NumberInfo struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	VerifiedName       string `json:"verified_name"`
}

// PhoneNumberInfo reads the client's phone number: it proves the token
// reaches that number.
func (c *Client) PhoneNumberInfo(ctx context.Context) (NumberInfo, error) {
	var out NumberInfo
	raw, err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(c.PhoneNumberID)+"?fields=display_phone_number,verified_name", "", nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, errors.New("whatsapp: unreadable phone number")
	}
	return out, nil
}

// SetFlowsPublicKey uploads the Flows endpoint's public key to the
// client's phone number (Flows encryption: one per phone number).
func (c *Client) SetFlowsPublicKey(ctx context.Context, publicPEM string) error {
	form := url.Values{"business_public_key": {publicPEM}}
	_, err := c.call(ctx, http.MethodPost, "/"+url.PathEscape(c.PhoneNumberID)+"/whatsapp_business_encryption",
		"application/x-www-form-urlencoded", bytes.NewReader([]byte(form.Encode())))
	return err
}

func (c *Client) call(ctx context.Context, method, path, contentType string, body io.Reader) ([]byte, error) {
	if c.PhoneNumberID == "" || c.Token == "" {
		return nil, errors.New("whatsapp: the number is not configured")
	}
	hc, err := c.client()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("whatsapp: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error *GraphError `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != nil {
			e.Error.Status = resp.StatusCode
			return nil, e.Error
		}
		return nil, fmt.Errorf("whatsapp: Graph API answered %s", resp.Status)
	}
	return raw, nil
}
