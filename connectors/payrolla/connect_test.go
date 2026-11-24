package payrolla_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
	"github.com/israel-duff/taskiem/engine/connector"
)

const redirect = "https://app.taskiem.test/v1/payrolla/callback"

var taskiemApp = payrollatest.Client{ID: "pyc_taskiem", Secret: "pys_secret", Redirects: []string{redirect}}

var allScopes = "payroll:read employees:read employees:write payroll:write payroll:approve payslips:read payslips:links self_service reports:read webhooks:manage"

// store is a connection's credentials kept as the vault keeps them: a
// refresh runs under one lock and what it returns is stored before the
// lock goes.
type store struct {
	mu    sync.Mutex
	creds map[string]string
	saves int
}

func (s *store) rotate(_ context.Context, fn connector.RotateFunc) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := fn(maps.Clone(s.creds))
	if err != nil {
		return nil, err
	}
	if !maps.Equal(next, s.creds) {
		s.creds, s.saves = next, s.saves+1
	}
	return maps.Clone(s.creds), nil
}

func (s *store) get() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.creds)
}

// connectOAuth goes through Payrolla's consent page and token exchange as
// Taskiem's OAuth machinery does (PKCE S256, the client in HTTP Basic), and
// returns the connection's credentials.
func connectOAuth(t *testing.T, f *payrollatest.Fake) map[string]string {
	t.Helper()
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {taskiemApp.ID}, "redirect_uri": {redirect}, "scope": {allScopes}, "state": {"st"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(f.App() + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	back, _ := url.Parse(resp.Header.Get("Location"))
	code := back.Query().Get("code")
	if code == "" || back.Query().Get("state") != "st" {
		t.Fatalf("consent: %s", resp.Header.Get("Location"))
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	req, _ := http.NewRequest(http.MethodPost, f.API()+"/oauth/token", strings.NewReader(form.Encode()))
	req.SetBasicAuth(taskiemApp.ID, taskiemApp.Secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Body.Close() }()
	var ans map[string]any
	_ = json.NewDecoder(tr.Body).Decode(&ans)
	if ans["access_token"] == nil {
		t.Fatalf("token: %v", ans)
	}
	return map[string]string{"refresh_token": ans["refresh_token"].(string), "access_token": ans["access_token"].(string),
		"access_expires_at": time.Now().Add(50 * time.Minute).UTC().Format(time.RFC3339), "client_id": taskiemApp.ID, "client_secret": taskiemApp.Secret,
		"company_id": ans["company_id"].(string), "company_name": ans["company_name"].(string), "scopes": ans["scope"].(string), connector.ModeKey: "test"}
}

func newConnector(f *payrollatest.Fake) *connector.Connector {
	return payrolla.New(payrolla.Options{TestURL: f.API(), LiveURL: f.API(), BaseURL: f.API(), TestAppURL: f.App()})
}

// The fake serves only what Payrolla's current OpenAPI file has: its
// routes, its error codes and its OAuth endpoints.
func TestFakeFollowsTheOpenAPIFile(t *testing.T) {
	raw, err := os.ReadFile("../../docs/integrations/payrolla-reference/api/payrolla-connect-v1-2026-11-24.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	for _, r := range payrollatest.Routes() {
		method, path, _ := strings.Cut(r, " ")
		block := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(path) + `:\n((?:    .*\n|\n)*)`).FindStringSubmatch(spec)
		if block == nil || !strings.Contains(block[1], "    "+strings.ToLower(method)+":") {
			t.Errorf("the fake serves %s, which Payrolla's OpenAPI file does not have", r)
		}
	}
	for _, c := range payrollatest.ErrorCodes() {
		if !strings.Contains(spec, c) {
			t.Errorf("the fake answers %s, which Payrolla's OpenAPI file does not name", c)
		}
	}
	for _, p := range []string{"/oauth/authorize", "{server}/oauth/token", "{server}/oauth/revoke", "company.disconnected", "code_challenge_method=S256"} {
		if !strings.Contains(spec, p) {
			t.Errorf("Payrolla's OpenAPI file no longer says %s", p)
		}
	}
}

// Ten workers whose access token lapsed at once refresh once: the first
// spends the refresh token and stores the new one; the others find it.
func TestRefreshRotatesOnceUnderTheLock(t *testing.T) {
	f := payrollatest.New(taskiemApp)
	defer f.Close()
	st := &store{creds: connectOAuth(t, f)}
	c := newConnector(f)
	f.ExpireAccessTokens()
	st.creds["access_expires_at"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	first := st.get()["refresh_token"]

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cl := payrolla.NewClient(c, st.get(), http.DefaultClient, st.rotate)
			if _, err := cl.Company(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if f.Refreshes() != 1 || st.saves != 1 || st.get()["refresh_token"] == first {
		t.Fatalf("refreshes %d, saves %d, refresh token replaced %v", f.Refreshes(), st.saves, st.get()["refresh_token"] != first)
	}
}

// A spent refresh token used again ends the chain: the connection has
// ended, and the owner connects again.
func TestReusedRefreshTokenEndsTheConnection(t *testing.T) {
	f := payrollatest.New(taskiemApp)
	defer f.Close()
	creds := connectOAuth(t, f)
	c := newConnector(f)
	old := maps.Clone(creds)
	st := &store{creds: creds}
	f.ExpireAccessTokens()
	st.creds["access_expires_at"] = "2000-01-01T00:00:00Z"
	if _, err := payrolla.NewClient(c, st.get(), http.DefaultClient, st.rotate).Company(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A copy of the old credentials (restored from a backup, say) spends
	// the old refresh token again.
	stale := &store{creds: old}
	stale.creds["access_expires_at"] = "2000-01-01T00:00:00Z"
	_, err := payrolla.NewClient(c, stale.get(), http.DefaultClient, stale.rotate).Company(context.Background())
	if !payrolla.Ended(err) || !errors.Is(err, payrolla.ErrConnectionEnded) || !errors.Is(err, connector.ErrCredentialsRejected) {
		t.Fatalf("reused: %v", err)
	}
	// The chain is over for the current tokens too.
	f.ExpireAccessTokens()
	cur := st.get()
	cur["access_expires_at"] = "2000-01-01T00:00:00Z"
	st.creds = cur
	if _, err := payrolla.NewClient(c, st.get(), http.DefaultClient, st.rotate).Company(context.Background()); !payrolla.Ended(err) {
		t.Fatalf("after the chain ended: %v", err)
	}
	// Nothing can refresh where credentials cannot be stored.
	if _, err := payrolla.NewClient(c, old, http.DefaultClient, nil).Company(context.Background()); err == nil || payrolla.Ended(err) {
		t.Fatalf("without a store: %v", err)
	}
}

// A key connection works as it always did: the key is the bearer token,
// and nothing is refreshed.
func TestKeyConnectionUnchanged(t *testing.T) {
	f := payrollatest.New(taskiemApp)
	defer f.Close()
	key := f.AddKey("payroll:read", "employees:read")
	cl := payrolla.NewClient(newConnector(f), map[string]string{"api_key": key, connector.ModeKey: "test"}, http.DefaultClient, nil)
	if cl.OAuth() {
		t.Fatal("a key connection is not OAuth")
	}
	v, err := cl.Company(context.Background())
	if err != nil || v.Company.Name != "Amara Foods Ltd" || f.Refreshes() != 0 {
		t.Fatalf("company %v, err %v", v, err)
	}
	f.RevokeKey(key)
	if _, err := cl.Company(context.Background()); !payrolla.Ended(err) || payrolla.Code(err) != "API_KEY_INVALID" {
		t.Fatalf("revoked: %v", err)
	}
	if err := payrolla.CheckCredentials(map[string]string{"api_key": "k"}); err != nil {
		t.Fatal(err)
	}
	if payrolla.CheckCredentials(map[string]string{}) == nil || payrolla.CheckCredentials(map[string]string{"api_key": "k", "refresh_token": "r"}) == nil {
		t.Fatal("a connection is a key or a sign-in")
	}
}

// Linking an approver with one code, and each of Payrolla's refusals; every
// write carries its key and the acting person.
func TestLinkingApprovers(t *testing.T) {
	f := payrollatest.New(taskiemApp)
	defer f.Close()
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000001", Name: "Ngozi Eze", Email: "ngozi@amara.test", Role: "OWNER", CanApprove: true, CanPay: true, CanConfirm: true})
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000002", Name: "Musa Bello", Email: "musa@amara.test", Role: "APPROVER", CanApprove: true})
	st := &store{creds: connectOAuth(t, f)}
	cl := payrolla.NewClient(newConnector(f), st.get(), http.DefaultClient, st.rotate)
	ctx := context.Background()
	me := uuid.MustParse("5e000000-0000-4000-8000-000000000001")
	w := func(key string) payrolla.Write { return payrolla.Write{Key: key, User: me, Name: "Ngozi Eze"} }
	ngozi, musa := "a0000000-0000-4000-8000-000000000001", "a0000000-0000-4000-8000-000000000002"

	// No factor to send a code to.
	if _, err := cl.StartLink(ctx, musa, w("k1")); payrolla.Code(err) != "PAYMENT_CONFIRMATION_NOT_SET_UP" {
		t.Fatalf("no factor: %v", err)
	}
	ch, err := cl.StartLink(ctx, ngozi, w("k2"))
	if err != nil || ch.ID == "" || ch.Channel != "SMS" {
		t.Fatalf("start: %v %v", ch, err)
	}
	// The same key with the same request answers once; another request
	// under it is Taskiem's bug.
	if again, err := cl.StartLink(ctx, ngozi, w("k2")); err != nil || again.ID != ch.ID {
		t.Fatalf("replayed: %v %v", again, err)
	}
	if _, err := cl.ConfirmLink(ctx, ngozi, ch.ID, "000000", w("k2")); !errors.Is(err, payrolla.ErrBug) {
		t.Fatalf("reused key: %v", err)
	}
	// A wrong code, then the right one; a code works once.
	if _, err := cl.ConfirmLink(ctx, ngozi, ch.ID, "000000", w("k3")); payrolla.Code(err) != "CODE_INVALID" {
		t.Fatalf("wrong code: %v", err)
	}
	a, err := cl.ConfirmLink(ctx, ngozi, ch.ID, f.Code(ngozi), w("k4"))
	if err != nil || a.LinkedUser() != me || a.LinkedAs != payrolla.ActingUser(me) {
		t.Fatalf("linked: %v %v", a, err)
	}
	if _, err := cl.ConfirmLink(ctx, ngozi, ch.ID, f.Code(ngozi), w("k5")); payrolla.Code(err) != "CODE_USED" {
		t.Fatalf("used: %v", err)
	}
	// One Payrolla user per person.
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000003", Name: "Ada", Role: "APPROVER", CanApprove: true, CanConfirm: true})
	if _, err := cl.StartLink(ctx, "a0000000-0000-4000-8000-000000000003", w("k6")); payrolla.Code(err) != "ALREADY_LINKED" {
		t.Fatalf("already linked: %v", err)
	}
	if err := cl.Unlink(ctx, ngozi, w("k7")); err != nil {
		t.Fatal(err)
	}
	list, err := cl.Approvers(ctx)
	if err != nil || len(list) != 3 || list[0].LinkedAs != "" {
		t.Fatalf("approvers: %v %v", list, err)
	}

	// Five wrong tries lock a code; an expired code says so.
	ch, _ = cl.StartLink(ctx, ngozi, w("k8"))
	for i := range 5 {
		_, err = cl.ConfirmLink(ctx, ngozi, ch.ID, "000000", w("k9-"+string(rune('a'+i))))
	}
	if payrolla.Code(err) != "CODE_LOCKED" {
		t.Fatalf("locked: %v", err)
	}
	ch, _ = cl.StartLink(ctx, ngozi, w("k10"))
	now := time.Now()
	f.Now = func() time.Time { return now.Add(6 * time.Minute) }
	if _, err := cl.ConfirmLink(ctx, ngozi, ch.ID, f.Code(ngozi), w("k11")); payrolla.Code(err) != "CODE_EXPIRED" {
		t.Fatalf("expired: %v", err)
	}
	// At most five codes in ten minutes.
	for i := range 5 {
		_, err = cl.StartLink(ctx, ngozi, w("k12-"+string(rune('a'+i))))
	}
	if payrolla.Code(err) != "TOO_MANY_CODES" {
		t.Fatalf("too many: %v", err)
	}

	for _, wr := range f.Writes() {
		if wr.Key == "" || wr.ActingUser != "tsk_user_"+me.String() || wr.ActingName != "Ngozi Eze" {
			t.Fatalf("a write without its headers: %+v", wr)
		}
		if strings.Contains(wr.Path, "confirm") && wr.Body != "" && !strings.Contains(wr.Body, "challenge_id") {
			t.Fatalf("confirm body: %s", wr.Body)
		}
	}
	// A write without its key or person is never sent.
	if _, err := cl.StartLink(ctx, ngozi, payrolla.Write{User: me}); err == nil {
		t.Fatal("a write without its key")
	}
}

// company.disconnected names the connection that ended.
func TestDisconnectedEvent(t *testing.T) {
	key, reason, ok := payrolla.Disconnected([]byte(`{"id":"evt_1","type":"company.disconnected","data":{"key_id":"k-1","reason":"revoked_by_owner"}}`))
	if !ok || key != "k-1" || reason != "revoked_by_owner" {
		t.Fatalf("%v %v %v", key, reason, ok)
	}
	if _, _, ok := payrolla.Disconnected([]byte(`{"type":"run.locked","data":{}}`)); ok {
		t.Fatal("not a disconnection")
	}
}
