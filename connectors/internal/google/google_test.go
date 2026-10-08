package google

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

// keyFile is a service account key file shaped as Google issues them.
func keyFile(t *testing.T) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key(t))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "taskiem-test", "private_key_id": "kid-1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "robot@taskiem-test.iam.gserviceaccount.com", "token_uri": "https://evil.example/token",
	})
	return string(b)
}

// verifyJWT checks an assertion's signature with the test key's public half
// and returns its header and claims.
func verifyJWT(t *testing.T, jwt string) (map[string]any, map[string]any) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key(t).PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	var h, c map[string]any
	for i, dst := range []*map[string]any{&h, &c} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			t.Fatal(err)
		}
	}
	return h, c
}

type tokenServer struct {
	*httptest.Server
	mints atomic.Int32
	forms chan url.Values
	reply func(w http.ResponseWriter, n int32)
}

func newTokenServer(t *testing.T) *tokenServer {
	ts := &tokenServer{forms: make(chan url.Values, 16)}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("token request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		_ = r.ParseForm()
		ts.forms <- r.PostForm
		n := ts.mints.Add(1)
		if ts.reply != nil {
			ts.reply(w, n)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"tok-`+string('0'+n)+`","expires_in":3599,"token_type":"Bearer"}`)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestServiceAccountAssertionAndCaching(t *testing.T) {
	ts := newTokenServer(t)
	now := time.Unix(1_790_000_000, 0)
	tokens := NewTokens(ts.URL)
	tokens.Now = func() time.Time { return now }
	c, err := FromConnection(map[string]string{FieldServiceAccount: keyFile(t), FieldSubject: "ada@example.com"}, []string{"https://www.googleapis.com/auth/gmail.modify"})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := tokens.Token(context.Background(), ts.Client(), c)
	if err != nil || tok != "tok-1" {
		t.Fatalf("token %q %v", tok, err)
	}
	form := <-ts.forms
	if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("grant_type %q", form.Get("grant_type"))
	}
	h, cl := verifyJWT(t, form.Get("assertion"))
	if h["alg"] != "RS256" || h["typ"] != "JWT" || h["kid"] != "kid-1" {
		t.Errorf("header %v", h)
	}
	// The audience is Google's token endpoint whatever URL tokens are
	// minted at; the key file's token_uri is never used.
	want := map[string]any{"iss": "robot@taskiem-test.iam.gserviceaccount.com", "sub": "ada@example.com",
		"scope": "https://www.googleapis.com/auth/gmail.modify", "aud": "https://oauth2.googleapis.com/token",
		"iat": float64(now.Unix()), "exp": float64(now.Add(time.Hour).Unix())}
	for k, v := range want {
		if cl[k] != v {
			t.Errorf("claim %s = %v, want %v", k, cl[k], v)
		}
	}

	// Cached until a minute before expiry.
	now = now.Add(57 * time.Minute)
	if tok, _ := tokens.Token(context.Background(), ts.Client(), c); tok != "tok-1" || ts.mints.Load() != 1 {
		t.Errorf("not cached: %q after %d mints", tok, ts.mints.Load())
	}
	now = now.Add(2 * time.Minute)
	if tok, _ := tokens.Token(context.Background(), ts.Client(), c); tok != "tok-2" || ts.mints.Load() != 2 {
		t.Errorf("not renewed: %q after %d mints", tok, ts.mints.Load())
	}
	<-ts.forms

	// Another subject is another credential set.
	other, _ := FromConnection(map[string]string{FieldServiceAccount: keyFile(t), FieldSubject: "grace@example.com"}, []string{"https://www.googleapis.com/auth/gmail.modify"})
	if tok, _ := tokens.Token(context.Background(), ts.Client(), other); tok != "tok-3" {
		t.Errorf("other subject shared a token: %q", tok)
	}
	_, cl = verifyJWT(t, (<-ts.forms).Get("assertion"))
	if cl["sub"] != "grace@example.com" {
		t.Errorf("sub %v", cl["sub"])
	}
}

func TestServiceAccountWithoutSubjectAndScopes(t *testing.T) {
	c, err := FromConnection(map[string]string{FieldServiceAccount: keyFile(t), FieldScopes: "b, a"}, []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	jwt, err := SignAssertion(c, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	_, cl := verifyJWT(t, jwt)
	if _, ok := cl["sub"]; ok || cl["scope"] != "a b" {
		t.Errorf("claims %v", cl)
	}
	// PKCS #1 keys are read too.
	pk1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key(t))}))
	if k, err := ParsePrivateKey(pk1); err != nil || !k.Equal(key(t)) {
		t.Errorf("pkcs1: %v", err)
	}
}

func TestRefreshTokenGrant(t *testing.T) {
	ts := newTokenServer(t)
	tokens := NewTokens(ts.URL)
	c, err := FromConnection(map[string]string{FieldClientID: "cid.apps.googleusercontent.com", FieldClientSecret: "shh", FieldRefreshToken: "1//rt"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tok, err := tokens.Token(context.Background(), ts.Client(), c); err != nil || tok != "tok-1" {
		t.Fatalf("%q %v", tok, err)
	}
	f := <-ts.forms
	if f.Get("grant_type") != "refresh_token" || f.Get("client_id") != "cid.apps.googleusercontent.com" || f.Get("client_secret") != "shh" || f.Get("refresh_token") != "1//rt" || f.Has("assertion") {
		t.Errorf("form %v", f)
	}
	if tok, _ := tokens.Token(context.Background(), ts.Client(), c); tok != "tok-1" || ts.mints.Load() != 1 {
		t.Error("refresh-token access token not cached")
	}
}

func TestConnectionProblemsAreFatal(t *testing.T) {
	for name, conn := range map[string]map[string]string{
		"empty":         {},
		"both":          {FieldServiceAccount: keyFile(t), FieldRefreshToken: "rt", FieldClientID: "c"},
		"not json":      {FieldServiceAccount: "{"},
		"wrong type":    {FieldServiceAccount: `{"type":"authorized_user","client_email":"a","private_key":"b"}`},
		"bad key":       {FieldServiceAccount: `{"type":"service_account","client_email":"a","private_key":"not pem"}`},
		"no client id":  {FieldRefreshToken: "rt"},
		"missing email": {FieldServiceAccount: `{"type":"service_account","private_key":"x"}`},
	} {
		if _, err := FromConnection(conn, nil); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTokenErrors(t *testing.T) {
	ts := newTokenServer(t)
	c, _ := FromConnection(map[string]string{FieldClientID: "c", FieldRefreshToken: "rt"}, nil)
	for _, tc := range []struct {
		status int
		body   string
		kind   effects.ErrorKind
		text   string
	}{
		{400, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`, effects.KindFatal, "invalid_grant: Token has been expired or revoked."},
		{401, `{"error":"invalid_client"}`, effects.KindFatal, "invalid_client"},
		{500, `oops`, effects.KindNotSent, "oops"},
		{429, `{}`, effects.KindNotSent, "429"},
		{200, `{"token_type":"Bearer"}`, effects.KindNotSent, "no access_token"},
	} {
		ts.reply = func(w http.ResponseWriter, _ int32) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}
		_, err := NewTokens(ts.URL).Token(context.Background(), ts.Client(), c)
		<-ts.forms
		if effects.Classify(err) != tc.kind || !strings.Contains(err.Error(), tc.text) {
			t.Errorf("%d: %v (%s)", tc.status, err, effects.Classify(err))
		}
		// A token failure never reaches the API, so every class proves
		// nothing was sent.
		if !errors.Is(err, effects.ErrNotSent) {
			t.Errorf("%d: not marked not_sent", tc.status)
		}
	}
	// Unreachable token endpoint: nothing sent.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	_, err := NewTokens(srv.URL).Token(context.Background(), http.DefaultClient, c)
	if effects.Classify(err) != effects.KindNotSent {
		t.Errorf("unreachable: %v", err)
	}
}

func TestDoRetriesOnceAfter401AndClassifies(t *testing.T) {
	ts := newTokenServer(t)
	tokens := NewTokens(ts.URL)
	c, _ := FromConnection(map[string]string{FieldClientID: "c", FieldRefreshToken: "rt"}, nil)
	var seen []string
	status := http.StatusUnauthorized
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "Bearer tok-1" {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"code":401,"message":"Invalid Credentials","errors":[{"reason":"authError"}],"status":"UNAUTHENTICATED"}}`)
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(map[string]any{"echo": in["x"]})
	}))
	defer api.Close()
	var out struct{ Echo string }
	if err := tokens.Do(context.Background(), api.Client(), c, http.MethodPost, api.URL+"/v1/x", map[string]any{"x": "hi"}, &out); err != nil || out.Echo != "hi" {
		t.Fatalf("%v %v", out, err)
	}
	if len(seen) != 2 || seen[0] != "Bearer tok-1" || seen[1] != "Bearer tok-2" {
		t.Errorf("authorizations %v", seen)
	}
	for len(ts.forms) > 0 {
		<-ts.forms
	}

	// A second 401 is final.
	api2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":401,"message":"Invalid Credentials","status":"UNAUTHENTICATED"}}`)
	}))
	defer api2.Close()
	err := NewTokens(ts.URL).Do(context.Background(), api2.Client(), c, http.MethodGet, api2.URL, nil, nil)
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Invalid Credentials") {
		t.Errorf("second 401: %v", err)
	}
}

func TestAPIErrorClasses(t *testing.T) {
	ts := newTokenServer(t)
	c, _ := FromConnection(map[string]string{FieldClientID: "c", FieldRefreshToken: "rt"}, nil)
	for _, tc := range []struct {
		status int
		body   string
		kind   effects.ErrorKind
		retry  time.Duration
	}{
		{404, `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`, effects.KindFatal, 0},
		{400, `{"error":{"code":400,"message":"Invalid range","status":"INVALID_ARGUMENT"}}`, effects.KindFatal, 0},
		{403, `{"error":{"code":403,"message":"no","errors":[{"domain":"global","reason":"domainPolicy"}]}}`, effects.KindFatal, 0},
		{403, `{"error":{"code":403,"message":"slow down","errors":[{"domain":"usageLimits","reason":"userRateLimitExceeded"}]}}`, effects.KindNotSent, 0},
		{429, `{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`, effects.KindNotSent, 30 * time.Second},
		{503, `{"error":{"code":503,"message":"The service is currently unavailable.","status":"UNAVAILABLE"}}`, effects.KindRetryable, 0},
		{500, `{"error":{"code":500,"message":"Backend Error","errors":[{"reason":"backendError"}]}}`, effects.KindUnknownOutcome, 0},
		{502, `<html>bad gateway</html>`, effects.KindUnknownOutcome, 0},
	} {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		err := NewTokens(ts.URL).Do(context.Background(), api.Client(), c, http.MethodGet, api.URL, nil, nil)
		api.Close()
		<-ts.forms
		var ae *APIError
		if effects.Classify(err) != tc.kind || !errors.As(err, &ae) || ae.Status != tc.status {
			t.Errorf("%d %s: %v (%s)", tc.status, tc.body, err, effects.Classify(err))
			continue
		}
		if tc.retry > 0 && ae.RetryAfter != tc.retry {
			t.Errorf("%d: retry after %v", tc.status, ae.RetryAfter)
		}
		if IsNotFound(err) != (tc.status == 404) {
			t.Errorf("%d: IsNotFound", tc.status)
		}
	}
}

func TestConcurrentCallersMintOnce(t *testing.T) {
	ts := newTokenServer(t)
	tokens := NewTokens(ts.URL)
	c, _ := FromConnection(map[string]string{FieldServiceAccount: keyFile(t)}, []string{"s"})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tokens.Token(context.Background(), ts.Client(), c); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := ts.mints.Load(); n != 1 {
		t.Errorf("%d mints", n)
	}
}
