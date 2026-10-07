package byok

// AWS KMS, from the public API reference: POST https://kms.REGION.amazonaws.com/
// with Content-Type application/x-amz-json-1.1 and X-Amz-Target
// TrentService.Encrypt or TrentService.Decrypt. Encrypt takes KeyId,
// Plaintext (base64) and EncryptionContext and answers CiphertextBlob;
// Decrypt takes CiphertextBlob, KeyId and the same EncryptionContext and
// answers Plaintext. Errors are {"__type":"...Exception","message":"..."}
// (DisabledException, KMSInvalidStateException, AccessDeniedException,
// NotFoundException, ...). Requests are signed with Signature Version 4,
// written here from the IAM User Guide's description of the algorithm.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type awsKMS struct {
	f       *Factory
	c       Config
	creds   map[string]string
	client  *http.Client
	baseURL string
	context map[string]string
}

func newAWS(f *Factory, c Config, creds map[string]string, o Options) (*awsKMS, error) {
	base := f.endpoint(AWSKMS, "https://kms."+c.Region+".amazonaws.com")
	hc, err := f.client(o.Tenant, "byok:aws_kms", []string{hostOf(base)}, "")
	if err != nil {
		return nil, err
	}
	ctxMap := map[string]string{"taskiem:purpose": "tenant-key"}
	if o.Tenant != "" {
		ctxMap["taskiem:tenant"] = o.Tenant
	}
	return &awsKMS{f: f, c: c, creds: creds, client: hc, baseURL: base, context: ctxMap}, nil
}

func (a *awsKMS) call(ctx context.Context, target string, body, out any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/", bytes.NewReader(raw))
	if err != nil {
		return unavailable(AWSKMS, "%v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "TrentService."+target)
	signV4(req, raw, sigv4{
		AccessKeyID: a.creds["access_key_id"], SecretAccessKey: a.creds["secret_access_key"], SessionToken: a.creds["session_token"],
		Region: a.c.Region, Service: "kms",
	}, a.f.now())
	resp, err := a.client.Do(req)
	if err != nil {
		return unavailable(AWSKMS, "cannot reach KMS in %s: %v", a.c.Region, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Type    string `json:"__type"`
			Message string `json:"message"`
			Msg     string `json:"Message"`
		}
		_ = json.Unmarshal(b, &e)
		kind := e.Type
		if i := strings.LastIndex(kind, "#"); i >= 0 {
			kind = kind[i+1:]
		}
		msg := e.Message
		if msg == "" {
			msg = e.Msg
		}
		return unavailable(AWSKMS, "%s: http %d: %s %s", target, resp.StatusCode, kind, clip(msg))
	}
	if err := json.Unmarshal(b, out); err != nil {
		return unavailable(AWSKMS, "%s: unreadable answer", target)
	}
	return nil
}

func (a *awsKMS) Wrap(ctx context.Context, plaintext []byte) (string, error) {
	var out struct {
		CiphertextBlob string `json:"CiphertextBlob"`
	}
	err := a.call(ctx, "Encrypt", map[string]any{"KeyId": a.c.Key, "Plaintext": base64.StdEncoding.EncodeToString(plaintext), "EncryptionContext": a.context}, &out)
	if err != nil {
		return "", err
	}
	if out.CiphertextBlob == "" {
		return "", unavailable(AWSKMS, "Encrypt returned no ciphertext")
	}
	return "aws:" + out.CiphertextBlob, nil
}

func (a *awsKMS) Unwrap(ctx context.Context, ciphertext string) ([]byte, error) {
	blob, ok := strings.CutPrefix(ciphertext, "aws:")
	if !ok {
		return nil, fmt.Errorf("%s: not a KMS ciphertext: %w", AWSKMS, ErrUnavailable)
	}
	var out struct {
		Plaintext string `json:"Plaintext"`
	}
	// KeyId is sent so KMS refuses a ciphertext made under another key.
	if err := a.call(ctx, "Decrypt", map[string]any{"CiphertextBlob": blob, "KeyId": a.c.Key, "EncryptionContext": a.context}, &out); err != nil {
		return nil, err
	}
	pt, err := base64.StdEncoding.DecodeString(out.Plaintext)
	if err != nil {
		return nil, unavailable(AWSKMS, "Decrypt returned unreadable plaintext")
	}
	return pt, nil
}

// sigv4 holds what Signature Version 4 needs.
type sigv4 struct {
	AccessKeyID, SecretAccessKey, SessionToken string
	Region, Service                            string
}

// signV4 signs a request whose URL has no query string. Every header set
// on req so far is signed, plus host, X-Amz-Date and, with temporary
// credentials, X-Amz-Security-Token.
func signV4(req *http.Request, body []byte, s sigv4, now time.Time) {
	t := now.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	if s.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", s.SessionToken)
	}
	vals := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		vals[strings.ToLower(k)] = strings.Join(strings.Fields(strings.Join(v, ",")), " ")
	}
	names := make([]string, 0, len(vals))
	for k := range vals {
		names = append(names, k)
	}
	sort.Strings(names)
	var canon strings.Builder
	for _, n := range names {
		canon.WriteString(n + ":" + vals[n] + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	bodyHash := sha256.Sum256(body)
	creq := strings.Join([]string{req.Method, path, "", canon.String(), signed, hex.EncodeToString(bodyHash[:])}, "\n")
	creqHash := sha256.Sum256([]byte(creq))
	scope := day + "/" + s.Region + "/" + s.Service + "/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(creqHash[:])
	mac := func(key []byte, data string) []byte {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(data))
		return m.Sum(nil)
	}
	k := mac([]byte("AWS4"+s.SecretAccessKey), day)
	k = mac(k, s.Region)
	k = mac(k, s.Service)
	k = mac(k, "aws4_request")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.AccessKeyID+"/"+scope+", SignedHeaders="+signed+", Signature="+hex.EncodeToString(mac(k, sts)))
}
