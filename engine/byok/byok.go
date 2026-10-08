// Package byok talks to key-management services that tenants control, so
// a tenant's key-encryption key can be wrapped by a key the platform does
// not hold (bring your own key, spec 14.1; decision 0019; docs/byok.md).
//
// A Provider wraps and unwraps a few dozen bytes under the customer's key.
// Four are implemented, each written from the service's public HTTP API
// documentation only (no SDK or server source was consulted):
//
//   - vault_transit: OpenBao or HashiCorp Vault, the transit secrets
//     engine's encrypt and decrypt endpoints, with a token or AppRole login
//     (openbao.org/api-docs/secret/transit, openbao.org/api-docs/auth/approle).
//   - aws_kms: AWS KMS Encrypt and Decrypt over its JSON protocol, signed
//     with Signature Version 4 (docs.aws.amazon.com/kms/latest/APIReference,
//     docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html).
//   - gcp_kms: Google Cloud KMS cryptoKeys.encrypt and decrypt, with a
//     service account key exchanged for a token by the JWT bearer grant
//     (cloud.google.com/kms/docs/reference/rest,
//     developers.google.com/identity/protocols/oauth2/service-account).
//   - azure_key_vault: Azure Key Vault wrapkey and unwrapkey (RSA-OAEP-256)
//     with a client-credentials token from Microsoft Entra ID
//     (learn.microsoft.com/rest/api/keyvault/keys/wrap-key,
//     learn.microsoft.com/entra/identity-platform/v2-oauth2-client-creds-grant-flow).
//
// Every call goes through the egress guard: a tenant-supplied address can
// never reach the platform's own network unless the operator allows
// private addresses (dedicated single-tenant deployments only).
package byok

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/egress"
)

// Provider names.
const (
	VaultTransit  = "vault_transit"
	AWSKMS        = "aws_kms"
	GCPKMS        = "gcp_kms"
	AzureKeyVault = "azure_key_vault"
)

// Providers lists the supported providers.
var Providers = []string{VaultTransit, AWSKMS, GCPKMS, AzureKeyVault}

// ErrUnavailable wraps every failure to wrap or unwrap with the customer's
// key: refused, disabled, deleted, unreachable, or a ciphertext it will not
// open. Callers fail closed on it.
var ErrUnavailable = errors.New("customer key unavailable")

// ErrInvalid wraps configuration and credential problems found before any
// call is made.
var ErrInvalid = errors.New("invalid key configuration")

// Provider wraps and unwraps small secrets under a customer's key.
type Provider interface {
	// Wrap encrypts plaintext (at most a few hundred bytes) and returns a
	// printable ciphertext that names its provider.
	Wrap(ctx context.Context, plaintext []byte) (string, error)
	// Unwrap reverses Wrap.
	Unwrap(ctx context.Context, ciphertext string) ([]byte, error)
}

// Config locates a customer's key. It holds no credentials: those are
// passed separately and stored sealed by the platform's key.
type Config struct {
	Provider string `json:"provider"`
	// Address is the OpenBao/Vault address (https://bao.example.com:8200)
	// or the Azure vault URL (https://NAME.vault.azure.net).
	Address string `json:"address,omitempty"`
	// Mount is the transit mount (default "transit").
	Mount string `json:"mount,omitempty"`
	// Namespace is a Vault Enterprise or OpenBao namespace (optional).
	Namespace string `json:"namespace,omitempty"`
	// AuthMount is the AppRole mount (default "approle").
	AuthMount string `json:"auth_mount,omitempty"`
	// CACert is a PEM bundle to trust in addition to the system roots, for
	// a Vault behind a private CA. Verification is never turned off.
	CACert string `json:"ca_cert,omitempty"`
	// Key is the transit key name, the AWS key id, ARN or alias, the Google
	// crypto key resource name, or the Azure key name.
	Key string `json:"key"`
	// KeyVersion is the Azure key version new wraps use (required);
	// unwrapping uses the version recorded with each ciphertext.
	KeyVersion string `json:"key_version,omitempty"`
	// Region is the AWS region (af-south-1, eu-west-1, ...).
	Region string `json:"region,omitempty"`
}

// Credential fields per provider. Required fields are checked by Validate.
var credentialFields = map[string][][]string{
	VaultTransit:  {{"token"}, {"role_id", "secret_id"}},
	AWSKMS:        {{"access_key_id", "secret_access_key"}},
	GCPKMS:        {{"service_account_json"}},
	AzureKeyVault: {{"tenant_id", "client_id", "client_secret"}},
}

// optionalCredentials may accompany the required ones.
var optionalCredentials = map[string][]string{AWSKMS: {"session_token"}}

var (
	namePart     = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
	mountPart    = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`)
	awsRegion    = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]{1,2}$`)
	awsKey       = regexp.MustCompile(`^(arn:aws[a-z-]*:kms:[a-z0-9-]+:[0-9]{12}:(key|alias)/[A-Za-z0-9/_+=.@-]+|alias/[A-Za-z0-9/_-]+|[0-9a-fA-F-]{36}|mrk-[0-9a-f]{32})$`)
	gcpKeyName   = regexp.MustCompile(`^projects/[a-z0-9-]{4,30}/locations/[a-z0-9-]+/keyRings/[A-Za-z0-9_-]{1,63}/cryptoKeys/[A-Za-z0-9_-]{1,63}$`)
	azureVault   = regexp.MustCompile(`^https://[a-z0-9-]{3,24}\.(vault\.azure\.net|managedhsm\.azure\.net)$`)
	azureVersion = regexp.MustCompile(`^[0-9a-f]{32}$`)
	azureTenant  = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
)

// Normalise trims the configuration and applies defaults.
func (c Config) Normalise() Config {
	c.Provider = strings.TrimSpace(c.Provider)
	c.Address = strings.TrimRight(strings.TrimSpace(c.Address), "/")
	c.Mount = strings.Trim(strings.TrimSpace(c.Mount), "/")
	c.Namespace = strings.Trim(strings.TrimSpace(c.Namespace), "/")
	c.AuthMount = strings.Trim(strings.TrimSpace(c.AuthMount), "/")
	c.CACert = strings.TrimSpace(c.CACert)
	c.Key = strings.TrimSpace(c.Key)
	c.KeyVersion = strings.TrimSpace(c.KeyVersion)
	c.Region = strings.TrimSpace(c.Region)
	if c.Provider == VaultTransit {
		if c.Mount == "" {
			c.Mount = "transit"
		}
		if c.AuthMount == "" {
			c.AuthMount = "approle"
		}
	}
	return c
}

// Validate checks a normalised configuration and its credentials.
func Validate(c Config, creds map[string]string) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
	}
	switch c.Provider {
	case VaultTransit:
		u, err := url.Parse(c.Address)
		if err != nil || u.Scheme != "https" || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
			return bad("address must be an https URL with no path, such as https://bao.example.com:8200")
		}
		if !namePart.MatchString(c.Key) {
			return bad("key must be a transit key name")
		}
		if !mountPart.MatchString(c.Mount) || strings.Contains(c.Mount, "..") {
			return bad("mount %q is not a mount path", c.Mount)
		}
		if !mountPart.MatchString(c.AuthMount) || strings.Contains(c.AuthMount, "..") {
			return bad("auth_mount %q is not a mount path", c.AuthMount)
		}
		if c.Namespace != "" && (!mountPart.MatchString(c.Namespace) || strings.Contains(c.Namespace, "..")) {
			return bad("namespace %q is not a namespace path", c.Namespace)
		}
		if c.CACert != "" {
			if _, err := certPool(c.CACert); err != nil {
				return bad("ca_cert: %v", err)
			}
		}
	case AWSKMS:
		if !awsRegion.MatchString(c.Region) {
			return bad("region must be an AWS region such as eu-west-1")
		}
		if !awsKey.MatchString(c.Key) {
			return bad("key must be a KMS key id, key ARN, alias name (alias/...) or alias ARN")
		}
	case GCPKMS:
		if !gcpKeyName.MatchString(c.Key) {
			return bad("key must be a crypto key name: projects/P/locations/L/keyRings/R/cryptoKeys/K")
		}
	case AzureKeyVault:
		if !azureVault.MatchString(c.Address) {
			return bad("address must be the vault URL, https://NAME.vault.azure.net (or a Managed HSM, https://NAME.managedhsm.azure.net)")
		}
		if !namePart.MatchString(c.Key) || strings.ContainsAny(c.Key, "._") {
			return bad("key must be a Key Vault key name (letters, digits and dashes)")
		}
		if !azureVersion.MatchString(c.KeyVersion) {
			return bad("key_version must be the key's 32-character version id (from the portal or az keyvault key show)")
		}
		if !azureTenant.MatchString(creds["tenant_id"]) {
			return bad("tenant_id must be the directory (tenant) id or domain")
		}
	default:
		return bad("provider must be one of %s", strings.Join(Providers, ", "))
	}
	if c.Provider != VaultTransit && (c.CACert != "" || c.Mount != "" || c.Namespace != "" || c.AuthMount != "") {
		return bad("ca_cert, mount, namespace and auth_mount apply only to %s", VaultTransit)
	}
	if c.Provider != AWSKMS && c.Region != "" {
		return bad("region applies only to %s", AWSKMS)
	}
	if c.Provider != AzureKeyVault && c.KeyVersion != "" {
		return bad("key_version applies only to %s", AzureKeyVault)
	}
	if c.Provider == AWSKMS || c.Provider == GCPKMS {
		if c.Address != "" {
			return bad("address does not apply to %s: the provider's public endpoint is used", c.Provider)
		}
	}
	return validateCreds(c.Provider, creds)
}

func validateCreds(provider string, creds map[string]string) error {
	sets := credentialFields[provider]
	allowed := map[string]bool{}
	for _, set := range sets {
		for _, f := range set {
			allowed[f] = true
		}
	}
	for _, f := range optionalCredentials[provider] {
		allowed[f] = true
	}
	for k := range creds {
		if !allowed[k] {
			return fmt.Errorf("%w: credential field %q does not apply to %s", ErrInvalid, k, provider)
		}
	}
	matched := 0
	for _, set := range sets {
		full := true
		for _, f := range set {
			if strings.TrimSpace(creds[f]) == "" {
				full = false
			}
		}
		if full {
			matched++
		}
	}
	if matched != 1 {
		var alts []string
		for _, set := range sets {
			alts = append(alts, strings.Join(set, " and "))
		}
		return fmt.Errorf("%w: credentials for %s must be exactly one of: %s", ErrInvalid, provider, strings.Join(alts, "; or "))
	}
	return nil
}

// Describe names the key without credentials, for audit entries and pages.
func (c Config) Describe() string {
	switch c.Provider {
	case VaultTransit:
		ns := ""
		if c.Namespace != "" {
			ns = " (namespace " + c.Namespace + ")"
		}
		return fmt.Sprintf("transit key %s/%s at %s%s", c.Mount, c.Key, c.Address, ns)
	case AWSKMS:
		return fmt.Sprintf("AWS KMS key %s in %s", c.Key, c.Region)
	case GCPKMS:
		return "Cloud KMS key " + c.Key
	case AzureKeyVault:
		return fmt.Sprintf("Key Vault key %s in %s", c.Key, c.Address)
	}
	return c.Provider
}

// CredentialDigest is a short fingerprint of credentials, so a page can show
// which set is stored without revealing any of it.
func CredentialDigest(creds map[string]string) string {
	keys := make([]string, 0, len(creds))
	for k := range creds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k + "\x00" + creds[k] + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// Factory builds providers with the platform's egress rules.
type Factory struct {
	// Guard vets every connection (nil: a default guard).
	Guard *egress.Guard
	// AllowPrivate lets a provider address resolve to private ranges:
	// dedicated single-tenant deployments whose customer KMS is on their
	// own network (TASKIEM_BYOK_ALLOW_PRIVATE). Loopback, link-local and
	// metadata addresses stay refused.
	AllowPrivate bool
	// Endpoints overrides the fixed cloud endpoints (tests): provider name
	// to base URL, plus "aws_kms_token", "gcp_token", "azure_token".
	Endpoints map[string]string
	// CACert is a PEM bundle trusted for every provider in addition to the
	// system roots (an operator's private CA; tests).
	CACert string
	// Timeout bounds each call (default 10s).
	Timeout time.Duration
	// Now is the clock (tests).
	Now func() time.Time
}

// Options are per-tenant.
type Options struct {
	// Tenant is bound into the encryption context where the provider
	// supports one (AWS, Google), so the customer's own audit logs show
	// which tenant each call was for.
	Tenant string
}

// New builds a provider for a validated configuration.
func (f *Factory) New(c Config, creds map[string]string, o Options) (Provider, error) {
	c = c.Normalise()
	if err := Validate(c, creds); err != nil {
		return nil, err
	}
	switch c.Provider {
	case VaultTransit:
		return newVault(f, c, creds, o)
	case AWSKMS:
		return newAWS(f, c, creds, o)
	case GCPKMS:
		return newGCP(f, c, creds, o)
	case AzureKeyVault:
		return newAzure(f, c, creds, o)
	}
	return nil, fmt.Errorf("%w: unknown provider %q", ErrInvalid, c.Provider)
}

func (f *Factory) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Factory) endpoint(name, def string) string {
	if v := f.Endpoints[name]; v != "" {
		return strings.TrimRight(v, "/")
	}
	return def
}

// client returns an egress-guarded client limited to rawURL's host, with
// caPEM trusted in addition to the system roots.
func (f *Factory) client(tenant, purpose string, hosts []string, caPEM string) (*http.Client, error) {
	g := f.Guard
	if g == nil {
		g = &egress.Guard{}
	}
	if f.AllowPrivate {
		inner := g
		g = &egress.Guard{Resolver: inner.Resolver, Logger: inner.Logger, Timeout: inner.Timeout, Blocked: func(a netip.Addr) bool {
			if a.Unmap().IsPrivate() {
				return false // RFC 1918 and ULA ranges; loopback, link-local and metadata stay refused
			}
			if inner.Blocked != nil {
				return inner.Blocked(a)
			}
			return egress.BlockedAddr(a)
		}}
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	pol := egress.Policy{Tenant: tenant, Hosts: hosts, Purpose: purpose}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if pemData := strings.TrimSpace(f.CACert + "\n" + caPEM); pemData != "" {
		pool, err := certPool(pemData)
		if err != nil {
			return nil, fmt.Errorf("%w: ca_cert: %w", ErrInvalid, err)
		}
		tlsCfg.RootCAs = pool
	}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return g.DialContext(ctx, pol, network, addr)
		},
		TLSClientConfig:       tlsCfg,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("%w: key services are not followed through redirects", egress.ErrDenied)
	}}, nil
}

// certPool returns the system roots plus pemData.
func certPool(pemData string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM([]byte(pemData)) {
		return nil, errors.New("no PEM certificate found")
	}
	return pool, nil
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// unavailable wraps a provider failure.
func unavailable(provider, format string, a ...any) error {
	return fmt.Errorf("%s: %s: %w", provider, fmt.Sprintf(format, a...), ErrUnavailable)
}

// clip shortens a provider's error text for messages and audit entries.
func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
