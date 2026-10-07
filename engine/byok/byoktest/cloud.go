package byoktest

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/s3"
	"github.com/israel-duff/taskiem/engine/byok"
)

// Cloud fakes AWS KMS, Google Cloud KMS and Azure Key Vault (with their
// token endpoints) on one TLS server, routed by path and headers.
type Cloud struct {
	Server *httptest.Server

	// AWS
	AccessKeyID, SecretAccessKey string
	// Google: the service account key file to configure.
	ServiceAccountJSON string
	// Azure
	AzureTenant, ClientID, ClientSecret, AzureKeyVersion string

	mu       sync.Mutex
	key      []byte // symmetric material behind the AWS and Google keys
	rsaKey   *rsa.PrivateKey
	saPub    *rsa.PublicKey
	tokens   map[string]bool
	disabled bool
	denied   bool
	calls    int
}

// GCPKey is the crypto key name the Google fake serves.
const GCPKey = "projects/acme-prod/locations/europe-west1/keyRings/taskiem/cryptoKeys/tenant-key"

// AWSKey is the key ARN the AWS fake serves.
const AWSKey = "arn:aws:kms:af-south-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"

// NewCloud starts the fake.
func NewCloud(t testing.TB) *Cloud {
	//nolint:gosec // documentation example credentials for an in-process fake
	c := &Cloud{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		AzureTenant: "contoso.onmicrosoft.com", ClientID: "app-1", ClientSecret: "app-secret", AzureKeyVersion: "0123456789abcdef0123456789abcdef",
		key: randBytes(32), tokens: map[string]bool{}}
	var err error
	if c.rsaKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	sa, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c.saPub = &sa.PublicKey
	der, _ := x509.MarshalPKCS8PrivateKey(sa)
	file, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "taskiem@acme-prod.iam.gserviceaccount.com", "private_key_id": "k1",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	c.ServiceAccountJSON = string(file)
	c.Server = httptest.NewTLSServer(http.HandlerFunc(c.serve))
	t.Cleanup(c.Server.Close)
	return c
}

// Factory returns a provider factory pointed at the fake.
func (c *Cloud) Factory() *byok.Factory {
	f := Factory(c.Server)
	u := c.Server.URL
	f.Endpoints = map[string]string{byok.AWSKMS: u, byok.GCPKMS: u, "gcp_token": u + "/gcp/token", byok.AzureKeyVault: u, "azure_token": u + "/azure"}
	return f
}

// AWS returns a configuration and credentials for the AWS fake.
func (c *Cloud) AWS() (byok.Config, map[string]string) {
	return byok.Config{Provider: byok.AWSKMS, Region: "af-south-1", Key: AWSKey},
		map[string]string{"access_key_id": c.AccessKeyID, "secret_access_key": c.SecretAccessKey}
}

// GCP returns a configuration and credentials for the Google fake.
func (c *Cloud) GCP() (byok.Config, map[string]string) {
	return byok.Config{Provider: byok.GCPKMS, Key: GCPKey}, map[string]string{"service_account_json": c.ServiceAccountJSON}
}

// Azure returns a configuration and credentials for the Azure fake.
func (c *Cloud) Azure() (byok.Config, map[string]string) {
	return byok.Config{Provider: byok.AzureKeyVault, Address: "https://acme-keys.vault.azure.net", Key: "taskiem-tenant", KeyVersion: c.AzureKeyVersion},
		map[string]string{"tenant_id": c.AzureTenant, "client_id": c.ClientID, "client_secret": c.ClientSecret}
}

// Disable makes every key refuse to work, as disabling it would.
func (c *Cloud) Disable(on bool) {
	c.mu.Lock()
	c.disabled = on
	c.mu.Unlock()
}

// Deny refuses every call as not permitted (access revoked).
func (c *Cloud) Deny(on bool) {
	c.mu.Lock()
	c.denied = on
	c.mu.Unlock()
}

// Calls counts key operations.
func (c *Cloud) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (c *Cloud) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case r.Header.Get("X-Amz-Target") != "":
		c.aws(w, r, body)
	case r.URL.Path == "/gcp/token":
		c.gcpToken(w, r, body)
	case strings.HasPrefix(r.URL.Path, "/v1/projects/"):
		c.gcp(w, r, body)
	case strings.HasPrefix(r.URL.Path, "/azure/"):
		c.azureToken(w, r, body)
	case strings.HasPrefix(r.URL.Path, "/keys/"):
		c.azure(w, r, body)
	default:
		http.NotFound(w, r)
	}
}

// --- AWS ---

func (c *Cloud) aws(w http.ResponseWriter, r *http.Request, body []byte) {
	fail := func(status int, typ, msg string) {
		writeJSON(w, status, map[string]string{"__type": typ, "message": msg})
	}
	if err := c.checkSigV4(r, body); err != nil {
		fail(http.StatusBadRequest, "InvalidSignatureException", err.Error())
		return
	}
	if r.Header.Get("Content-Type") != "application/x-amz-json-1.1" {
		fail(http.StatusBadRequest, "SerializationException", "content type")
		return
	}
	if c.denied {
		fail(http.StatusBadRequest, "AccessDeniedException", "User is not authorized to perform: kms:Decrypt")
		return
	}
	if c.disabled {
		fail(http.StatusBadRequest, "DisabledException", AWSKey+" is disabled.")
		return
	}
	var in struct {
		KeyID             string            `json:"KeyId"`
		Plaintext         string            `json:"Plaintext"`
		CiphertextBlob    string            `json:"CiphertextBlob"`
		EncryptionContext map[string]string `json:"EncryptionContext"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fail(http.StatusBadRequest, "SerializationException", "body")
		return
	}
	if in.KeyID != AWSKey {
		fail(http.StatusBadRequest, "NotFoundException", "Key '"+in.KeyID+"' does not exist")
		return
	}
	ctxJSON, _ := json.Marshal(in.EncryptionContext) // map keys marshal sorted
	c.calls++
	gcm := newGCM(c.key)
	switch r.Header.Get("X-Amz-Target") {
	case "TrentService.Encrypt":
		pt, err := base64.StdEncoding.DecodeString(in.Plaintext)
		if err != nil {
			fail(http.StatusBadRequest, "ValidationException", "Plaintext")
			return
		}
		nonce := randBytes(gcm.NonceSize())
		writeJSON(w, http.StatusOK, map[string]string{"CiphertextBlob": base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, pt, ctxJSON)), "KeyId": AWSKey})
	case "TrentService.Decrypt":
		ct, err := base64.StdEncoding.DecodeString(in.CiphertextBlob)
		if err != nil || len(ct) < gcm.NonceSize() {
			fail(http.StatusBadRequest, "InvalidCiphertextException", "")
			return
		}
		pt, err := gcm.Open(nil, ct[:gcm.NonceSize()], ct[gcm.NonceSize():], ctxJSON)
		if err != nil {
			fail(http.StatusBadRequest, "InvalidCiphertextException", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"Plaintext": base64.StdEncoding.EncodeToString(pt), "KeyId": AWSKey})
	default:
		fail(http.StatusBadRequest, "UnknownOperationException", "")
	}
}

// checkSigV4 re-signs what the request says it signed with the S3
// connector's independently tested signer and compares.
func (c *Cloud) checkSigV4(r *http.Request, body []byte) error {
	auth := r.Header.Get("Authorization")
	_, rest, ok := strings.Cut(auth, "SignedHeaders=")
	if !ok || !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+c.AccessKeyID+"/") || !strings.Contains(auth, "/af-south-1/kms/aws4_request") {
		return fmt.Errorf("bad authorization header %q", auth)
	}
	signed, _, _ := strings.Cut(rest, ",")
	t, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return fmt.Errorf("bad date")
	}
	re, _ := http.NewRequest(r.Method, "https://"+r.Host+r.URL.Path, bytes.NewReader(body)) //nolint:gosec // rebuilt to be signed, never sent
	re.Host = r.Host
	for _, h := range strings.Split(signed, ";") {
		if h != "host" && h != "x-amz-date" && h != "x-amz-security-token" {
			re.Header.Set(h, r.Header.Get(h))
		}
	}
	sum := sha256.Sum256(body)
	s3.Signer{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: r.Header.Get("X-Amz-Security-Token"), Region: "af-south-1", Service: "kms"}.
		Sign(re, fmt.Sprintf("%x", sum), t)
	if re.Header.Get("Authorization") != auth {
		return fmt.Errorf("signature does not match")
	}
	return nil
}

// --- Google ---

func (c *Cloud) gcpToken(w http.ResponseWriter, r *http.Request, body []byte) {
	form, _ := parseForm(body)
	if form["grant_type"] != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	parts := strings.Split(form["assertion"], ".")
	if len(parts) != 3 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(c.saPub, crypto.SHA256, sum[:], sig) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "Invalid JWT Signature."})
		return
	}
	var claims struct {
		Iss, Scope, Aud string
		Iat, Exp        int64
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(raw, &claims)
	if claims.Aud != "https://oauth2.googleapis.com/token" || claims.Scope != "https://www.googleapis.com/auth/cloudkms" || claims.Exp <= claims.Iat || c.denied {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	tok := "ya29." + base64.RawURLEncoding.EncodeToString(randBytes(12))
	c.tokens[tok] = true
	writeJSON(w, http.StatusOK, map[string]any{"access_token": tok, "expires_in": 3599, "token_type": "Bearer"})
}

func (c *Cloud) gcp(w http.ResponseWriter, r *http.Request, body []byte) {
	fail := func(code int, status, msg string) {
		writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg, "status": status}})
	}
	if !c.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] {
		fail(http.StatusUnauthorized, "UNAUTHENTICATED", "Request had invalid authentication credentials.")
		return
	}
	name, op, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/"), ":")
	if name != GCPKey {
		fail(http.StatusNotFound, "NOT_FOUND", name+" not found.")
		return
	}
	if c.denied {
		fail(http.StatusForbidden, "PERMISSION_DENIED", "Permission 'cloudkms.cryptoKeyVersions.useToDecrypt' denied")
		return
	}
	if c.disabled {
		fail(http.StatusBadRequest, "FAILED_PRECONDITION", name+"/cryptoKeyVersions/1 is not enabled, current state is: DISABLED.")
		return
	}
	var in struct {
		Plaintext, Ciphertext, AdditionalAuthenticatedData string
	}
	_ = json.Unmarshal(body, &in)
	aad, _ := base64.StdEncoding.DecodeString(in.AdditionalAuthenticatedData)
	c.calls++
	gcm := newGCM(c.key)
	switch op {
	case "encrypt":
		pt, _ := base64.StdEncoding.DecodeString(in.Plaintext)
		nonce := randBytes(gcm.NonceSize())
		writeJSON(w, http.StatusOK, map[string]any{"name": GCPKey + "/cryptoKeyVersions/1", "ciphertext": base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, pt, aad))})
	case "decrypt":
		ct, _ := base64.StdEncoding.DecodeString(in.Ciphertext)
		if len(ct) < gcm.NonceSize() {
			fail(http.StatusBadRequest, "INVALID_ARGUMENT", "Decryption failed")
			return
		}
		pt, err := gcm.Open(nil, ct[:gcm.NonceSize()], ct[gcm.NonceSize():], aad)
		if err != nil {
			fail(http.StatusBadRequest, "INVALID_ARGUMENT", "Decryption failed: the ciphertext is invalid.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plaintext": base64.StdEncoding.EncodeToString(pt)})
	default:
		fail(http.StatusNotFound, "NOT_FOUND", "method")
	}
}

// --- Azure ---

func (c *Cloud) azureToken(w http.ResponseWriter, r *http.Request, body []byte) {
	form, _ := parseForm(body)
	if r.URL.Path != "/azure/"+c.AzureTenant+"/oauth2/v2.0/token" || form["grant_type"] != "client_credentials" ||
		form["client_id"] != c.ClientID || form["client_secret"] != c.ClientSecret || form["scope"] != "https://vault.azure.net/.default" || c.denied {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client", "error_description": "AADSTS7000215: Invalid client secret provided."})
		return
	}
	tok := "eyJ0" + base64.RawURLEncoding.EncodeToString(randBytes(12))
	c.tokens[tok] = true
	writeJSON(w, http.StatusOK, map[string]any{"access_token": tok, "expires_in": 3599, "token_type": "Bearer"})
}

func (c *Cloud) azure(w http.ResponseWriter, r *http.Request, body []byte) {
	fail := func(status int, code, msg string) {
		writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
	}
	if !c.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] {
		fail(http.StatusUnauthorized, "Unauthorized", "AKV10000: Request is missing a Bearer or PoP token.") //nolint:misspell // the service's error code
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/keys/"), "/")
	if len(parts) != 3 || parts[0] != "taskiem-tenant" || r.URL.Query().Get("api-version") != "7.4" {
		fail(http.StatusNotFound, "KeyNotFound", "A key with (name/id) was not found in this key vault.")
		return
	}
	if parts[1] != c.AzureKeyVersion {
		fail(http.StatusNotFound, "KeyNotFound", "A key with (name/id) taskiem-tenant/"+parts[1]+" was not found in this key vault.")
		return
	}
	if c.denied {
		fail(http.StatusForbidden, "Forbidden", "The user, group or application does not have keys unwrapKey permission.")
		return
	}
	if c.disabled {
		fail(http.StatusForbidden, "Forbidden", "Operation unwrapKey is not allowed on a disabled key.")
		return
	}
	var in struct {
		Alg, Value string
	}
	_ = json.Unmarshal(body, &in)
	if in.Alg != "RSA-OAEP-256" {
		fail(http.StatusBadRequest, "BadParameter", "alg")
		return
	}
	val, err := base64.RawURLEncoding.DecodeString(in.Value)
	if err != nil {
		fail(http.StatusBadRequest, "BadParameter", "value")
		return
	}
	c.calls++
	kid := "https://acme-keys.vault.azure.net/keys/taskiem-tenant/" + c.AzureKeyVersion
	switch parts[2] {
	case "wrapkey":
		ct, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &c.rsaKey.PublicKey, val, nil)
		if err != nil {
			fail(http.StatusBadRequest, "BadParameter", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"kid": kid, "value": base64.RawURLEncoding.EncodeToString(ct)})
	case "unwrapkey":
		pt, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, c.rsaKey, val, nil)
		if err != nil {
			fail(http.StatusBadRequest, "BadParameter", "Unwrap failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"kid": kid, "value": base64.RawURLEncoding.EncodeToString(pt)})
	default:
		fail(http.StatusNotFound, "NotFound", "op")
	}
}

func parseForm(body []byte) (map[string]string, error) {
	r, _ := http.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k := range r.PostForm {
		out[k] = r.PostForm.Get(k)
	}
	return out, nil
}
