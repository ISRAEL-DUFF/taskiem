package byok

// OpenBao / HashiCorp Vault transit, from the public HTTP API docs: POST
// /v1/{mount}/encrypt/{key} with a base64 "plaintext" answers
// {"data":{"ciphertext":"vault:vN:..."}}; POST /v1/{mount}/decrypt/{key}
// with that "ciphertext" answers {"data":{"plaintext":"<base64>"}}. Errors
// are {"errors":["..."]} with a 4xx or 5xx status. Requests carry
// X-Vault-Token and, for namespaces, X-Vault-Namespace. AppRole: POST
// /v1/auth/{mount}/login with role_id and secret_id answers
// {"auth":{"client_token":"...","lease_duration":N}}.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type vaultTransit struct {
	f      *Factory
	c      Config
	creds  map[string]string
	client *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time // zero: a static token
}

func newVault(f *Factory, c Config, creds map[string]string, o Options) (*vaultTransit, error) {
	hc, err := f.client(o.Tenant, "byok:vault_transit", []string{hostOf(c.Address)}, c.CACert)
	if err != nil {
		return nil, err
	}
	return &vaultTransit{f: f, c: c, creds: creds, client: hc, token: strings.TrimSpace(creds["token"])}, nil
}

// post sends a JSON body to path and decodes the answer into out.
func (v *vaultTransit) post(ctx context.Context, path, token string, body, out any) (int, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.c.Address+"/v1/"+path, bytes.NewReader(raw))
	if err != nil {
		return 0, unavailable(VaultTransit, "%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	if v.c.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", v.c.Namespace)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return 0, unavailable(VaultTransit, "cannot reach %s: %v", v.c.Address, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Errors []string `json:"errors"`
		}
		_ = json.Unmarshal(b, &e)
		msg := strings.Join(e.Errors, "; ")
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return resp.StatusCode, unavailable(VaultTransit, "%s: http %d: %s", path, resp.StatusCode, clip(msg))
	}
	if err := json.Unmarshal(b, out); err != nil {
		return resp.StatusCode, unavailable(VaultTransit, "%s: unreadable answer", path)
	}
	return resp.StatusCode, nil
}

// login returns a token: the static one, or an AppRole token renewed a
// little before its lease ends. renew forces a new login.
func (v *vaultTransit) login(ctx context.Context, renew bool) (string, error) {
	if v.creds["role_id"] == "" {
		return v.token, nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !renew && v.token != "" && v.f.now().Before(v.tokenExp) {
		return v.token, nil
	}
	var out struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int64  `json:"lease_duration"`
		} `json:"auth"`
	}
	if _, err := v.post(ctx, "auth/"+v.c.AuthMount+"/login", "", map[string]string{"role_id": v.creds["role_id"], "secret_id": v.creds["secret_id"]}, &out); err != nil {
		return "", err
	}
	if out.Auth.ClientToken == "" {
		return "", unavailable(VaultTransit, "AppRole login returned no token")
	}
	lease := time.Duration(out.Auth.LeaseDuration) * time.Second
	if lease <= 0 {
		lease = 5 * time.Minute
	}
	v.token, v.tokenExp = out.Auth.ClientToken, v.f.now().Add(lease*8/10)
	return v.token, nil
}

// call runs one transit operation, logging in again once if an AppRole
// token was refused (it may have been revoked or reached its maximum TTL).
func (v *vaultTransit) call(ctx context.Context, op string, body map[string]string, out any) error {
	tok, err := v.login(ctx, false)
	if err != nil {
		return err
	}
	status, err := v.post(ctx, v.c.Mount+"/"+op+"/"+v.c.Key, tok, body, out)
	if err != nil && status == http.StatusForbidden && v.creds["role_id"] != "" {
		if tok, err = v.login(ctx, true); err != nil {
			return err
		}
		_, err = v.post(ctx, v.c.Mount+"/"+op+"/"+v.c.Key, tok, body, out)
	}
	return err
}

func (v *vaultTransit) Wrap(ctx context.Context, plaintext []byte) (string, error) {
	var out struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	if err := v.call(ctx, "encrypt", map[string]string{"plaintext": base64.StdEncoding.EncodeToString(plaintext)}, &out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.Data.Ciphertext, "vault:") {
		return "", unavailable(VaultTransit, "encrypt returned no ciphertext")
	}
	return out.Data.Ciphertext, nil
}

func (v *vaultTransit) Unwrap(ctx context.Context, ciphertext string) ([]byte, error) {
	if !strings.HasPrefix(ciphertext, "vault:") {
		return nil, fmt.Errorf("%s: not a transit ciphertext: %w", VaultTransit, ErrUnavailable)
	}
	var out struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	if err := v.call(ctx, "decrypt", map[string]string{"ciphertext": ciphertext}, &out); err != nil {
		return nil, err
	}
	pt, err := base64.StdEncoding.DecodeString(out.Data.Plaintext)
	if err != nil {
		return nil, unavailable(VaultTransit, "decrypt returned unreadable plaintext")
	}
	return pt, nil
}
