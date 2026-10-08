package byok

// Azure Key Vault, from the public REST reference: POST
// {vault}/keys/{name}/{version}/wrapkey?api-version=7.4 with
// {"alg":"RSA-OAEP-256","value":"<base64url>"} answers {"kid":...,
// "value":"<base64url>"}; unwrapkey is the reverse. Errors are
// {"error":{"code":"...","message":"..."}} (Forbidden, KeyNotFound,
// a disabled key: "Operation ... is not allowed on a disabled key").
// Tokens come from Microsoft Entra ID's client-credentials grant: POST
// https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token with
// client_id, client_secret and scope https://vault.azure.net/.default.
//
// Wrapping uses an RSA key, so what is wrapped must be small (RSA-OAEP-256
// with a 2048-bit key takes at most 190 bytes); a tenant key wrapped by the
// platform's key is under 100.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	azureLogin      = "https://login.microsoftonline.com"
	azureAPIVersion = "7.4"
)

type azureKV struct {
	f        *Factory
	c        Config
	creds    map[string]string
	client   *http.Client
	baseURL  string
	tokenURL string
	scope    string
	tok      tokenCache
}

func newAzure(f *Factory, c Config, creds map[string]string, o Options) (*azureKV, error) {
	base := f.endpoint(AzureKeyVault, c.Address)
	tokenURL := f.endpoint("azure_token", azureLogin) + "/" + url.PathEscape(creds["tenant_id"]) + "/oauth2/v2.0/token"
	hc, err := f.client(o.Tenant, "byok:azure_key_vault", []string{hostOf(base), hostOf(tokenURL)}, "")
	if err != nil {
		return nil, err
	}
	scope := "https://vault.azure.net/.default"
	if strings.HasSuffix(c.Address, ".managedhsm.azure.net") {
		scope = "https://managedhsm.azure.net/.default"
	}
	return &azureKV{f: f, c: c, creds: creds, client: hc, baseURL: base, tokenURL: tokenURL, scope: scope}, nil
}

func (a *azureKV) token(ctx context.Context) (string, error) {
	return a.tok.get(a.f.now(), func() (string, time.Duration, error) {
		form := url.Values{"grant_type": {"client_credentials"}, "client_id": {a.creds["client_id"]}, "client_secret": {a.creds["client_secret"]}, "scope": {a.scope}}
		return mintToken(ctx, a.client, AzureKeyVault, a.tokenURL, form)
	})
}

func (a *azureKV) call(ctx context.Context, version, op string, value []byte) ([]byte, string, error) {
	tok, err := a.token(ctx)
	if err != nil {
		return nil, "", err
	}
	raw, _ := json.Marshal(map[string]string{"alg": "RSA-OAEP-256", "value": base64.RawURLEncoding.EncodeToString(value)})
	u := a.baseURL + "/keys/" + url.PathEscape(a.c.Key) + "/" + url.PathEscape(version) + "/" + op + "?api-version=" + azureAPIVersion
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return nil, "", unavailable(AzureKeyVault, "%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, "", unavailable(AzureKeyVault, "cannot reach %s: %v", a.c.Address, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			a.tok.drop(tok)
		}
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return nil, "", unavailable(AzureKeyVault, "%s: http %d: %s %s", op, resp.StatusCode, e.Error.Code, clip(e.Error.Message))
	}
	var out struct {
		Kid   string `json:"kid"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, "", unavailable(AzureKeyVault, "%s: unreadable answer", op)
	}
	v, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(out.Value, "="))
	if err != nil || len(v) == 0 {
		return nil, "", unavailable(AzureKeyVault, "%s: unreadable value", op)
	}
	return v, out.Kid, nil
}

// Wrap records the key version with the ciphertext: azure:VERSION:VALUE.
func (a *azureKV) Wrap(ctx context.Context, plaintext []byte) (string, error) {
	ct, _, err := a.call(ctx, a.c.KeyVersion, "wrapkey", plaintext)
	if err != nil {
		return "", err
	}
	return "azure:" + a.c.KeyVersion + ":" + base64.RawURLEncoding.EncodeToString(ct), nil
}

func (a *azureKV) Unwrap(ctx context.Context, ciphertext string) ([]byte, error) {
	rest, ok := strings.CutPrefix(ciphertext, "azure:")
	version, value, ok2 := strings.Cut(rest, ":")
	if !ok || !ok2 || !azureVersion.MatchString(version) {
		return nil, fmt.Errorf("%s: not a Key Vault ciphertext: %w", AzureKeyVault, ErrUnavailable)
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s: not a Key Vault ciphertext: %w", AzureKeyVault, ErrUnavailable)
	}
	pt, _, err := a.call(ctx, version, "unwrapkey", raw)
	return pt, err
}
