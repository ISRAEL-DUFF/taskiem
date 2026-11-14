package push

// Firebase Cloud Messaging for the phone app (docs/spec/mobile-app-m4.md
// N1): the same pushes as Web Push, to the app's registration token, through
// FCM's HTTP v1 API, which also carries them to iPhones through Apple's
// service. Authorised by an OAuth token from the operator's service account,
// signed here (RS256) with the standard library, and kept until it expires.

import (
	"bytes"
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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
)

// FCMClient is what the app needs to start Firebase itself: the project's
// public client values (the same as in any app's google-services.json), so
// turning notifications on needs no new build of the app.
type FCMClient struct {
	APIKey       string `json:"api_key"`
	AppIDAndroid string `json:"app_id_android"`
	AppIDIOS     string `json:"app_id_ios,omitempty"`
	ProjectID    string `json:"project_id"`
	SenderID     string `json:"sender_id"`
}

// FCM sends to the app's devices with the operator's service account.
type FCM struct {
	// Client is told to the app (GET /v1/app/config).
	Client FCMClient

	project  string
	email    string
	key      *rsa.PrivateKey
	tokenURI string
	// BaseURL is FCM's (tests point it at a fake).
	BaseURL string

	mu      sync.Mutex // the token kept
	fetch   sync.Mutex // one fetch at a time, without holding mu
	access  string
	expires time.Time
}

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// ParseFCM reads the service account's key (its JSON, as Google gives it)
// and the client values (JSON, FCMClient's fields). Neither set is off (nil,
// nil); one without the other is an error.
func ParseFCM(credentials, client string) (*FCM, error) {
	credentials, client = strings.TrimSpace(credentials), strings.TrimSpace(client)
	if credentials == "" && client == "" {
		return nil, nil
	}
	if credentials == "" || client == "" {
		return nil, errors.New("push: TASKIEM_FCM_CREDENTIALS and TASKIEM_FCM_CLIENT are set together, or not at all")
	}
	var sa struct {
		Type        string `json:"type"`
		ProjectID   string `json:"project_id"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal([]byte(credentials), &sa); err != nil || sa.Type != "service_account" || sa.ClientEmail == "" || sa.PrivateKey == "" || sa.ProjectID == "" {
		return nil, errors.New("push: TASKIEM_FCM_CREDENTIALS is a Firebase service account's key, as Google gives it (JSON)")
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, errors.New("push: the service account's private key cannot be read")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("push: the service account's private key cannot be read")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("push: the service account's private key is not an RSA key")
	}
	var c FCMClient
	if err := json.Unmarshal([]byte(client), &c); err != nil || c.APIKey == "" || (c.AppIDAndroid == "" && c.AppIDIOS == "") || c.SenderID == "" {
		return nil, errors.New("push: TASKIEM_FCM_CLIENT is the app's Firebase values as JSON: api_key, project_id, sender_id, and app_id_android or app_id_ios (or both)")
	}
	if c.ProjectID == "" {
		c.ProjectID = sa.ProjectID
	}
	if c.ProjectID != sa.ProjectID {
		return nil, errors.New("push: TASKIEM_FCM_CLIENT and TASKIEM_FCM_CREDENTIALS are for different Firebase projects")
	}
	tokenURI := sa.TokenURI
	if tokenURI == "" {
		tokenURI = "https://oauth2.googleapis.com/token" //nolint:gosec // G101: Google's public token address, not a credential
	}
	if u, err := url.Parse(tokenURI); err != nil || u.Scheme != "https" || u.Hostname() != "oauth2.googleapis.com" {
		return nil, errors.New("push: the service account's token_uri is Google's (https://oauth2.googleapis.com/token)")
	}
	return &FCM{Client: c, project: sa.ProjectID, email: sa.ClientEmail, key: key, tokenURI: tokenURI, BaseURL: "https://fcm.googleapis.com"}, nil
}

// NewFCMForTest is an FCM with a key and addresses of the test's own.
func NewFCMForTest(project, email string, key *rsa.PrivateKey, tokenURI, baseURL string) *FCM {
	return &FCM{Client: FCMClient{ProjectID: project}, project: project, email: email, key: key, tokenURI: tokenURI, BaseURL: baseURL}
}

func (f *FCM) hosts() []string {
	var hs []string
	for _, raw := range []string{f.BaseURL, f.tokenURI} {
		if u, err := url.Parse(raw); err == nil {
			hs = append(hs, u.Hostname())
		}
	}
	return hs
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// assertion is the signed request for an access token (RFC 7523).
func (f *FCM) assertion(now time.Time) (string, error) {
	head := b64([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iss": f.email, "scope": fcmScope, "aud": f.tokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	unsigned := head + "." + b64(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + b64(sig), nil
}

// token is an access token, kept until a minute before it expires; forget
// drops it (a 401 from FCM).
func (f *FCM) token(ctx context.Context, client *http.Client, forget bool) (string, error) {
	kept := func() string {
		f.mu.Lock()
		defer f.mu.Unlock()
		if forget {
			f.access, forget = "", false
		}
		if f.access != "" && time.Now().Before(f.expires) {
			return f.access
		}
		return ""
	}
	if a := kept(); a != "" {
		return a, nil
	}
	// One fetch for all the senders waiting; the others then find it kept.
	f.fetch.Lock()
	defer f.fetch.Unlock()
	if a := kept(); a != "" {
		return a, nil
	}
	a, err := f.assertion(time.Now())
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {a}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("push: Google refused the service account (%d)", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil || out.AccessToken == "" {
		return "", errors.New("push: Google's answer for the service account cannot be read")
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 3600
	}
	f.mu.Lock()
	f.access, f.expires = out.AccessToken, time.Now().Add(time.Duration(out.ExpiresIn)*time.Second-time.Minute)
	f.mu.Unlock()
	return out.AccessToken, nil
}

// Message is the FCM message for a device: a notification the phone shows
// itself (one per tag, the icon's count, the kind's Android channel), and
// the data the app reads when it is tapped.
func (f *FCM) Message(t Target, p Payload) map[string]any {
	p = p.For(t)
	p.Title, p.Body = cut(p.Title, 200), cut(p.Body, 400)
	channel := p.Kind
	if channel == "" {
		channel = KindBusiness
	}
	data := map[string]string{"v": strconv.Itoa(Version), "link": p.Link, "tag": p.Tag, "kind": channel, "unread": strconv.Itoa(p.Unread)}
	if p.Business != "" {
		data["business"] = p.Business
	}
	android := map[string]any{
		"priority":     "HIGH",
		"ttl":          strconv.Itoa(TTL) + "s",
		"collapse_key": Topic(p.Tag),
		"notification": map[string]any{
			"tag":                p.Tag,
			"channel_id":         channel,
			"notification_count": p.Unread,
		},
	}
	apns := map[string]any{
		"headers": map[string]string{"apns-collapse-id": Topic(p.Tag), "apns-priority": "10", "apns-expiration": strconv.FormatInt(time.Now().Add(TTL*time.Second).Unix(), 10)},
		"payload": map[string]any{"aps": map[string]any{"badge": p.Unread, "sound": "default", "thread-id": Topic(p.Tag)}},
	}
	return map[string]any{"message": map[string]any{
		"token":        t.Endpoint,
		"notification": map[string]string{"title": p.Title, "body": p.Body},
		"data":         data,
		"android":      android,
		"apns":         apns,
	}}
}

// fcmError reads FCM's error: whether the device is gone for good.
func fcmGone(status int, body []byte) bool {
	var e struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	for _, d := range e.Error.Details {
		if d.ErrorCode == "UNREGISTERED" {
			return true
		}
	}
	// A 404 alone is not a device that is gone (a wrong project or address
	// answers it for every device): only FCM's UNREGISTERED is.
	// A token FCM does not take at all (not one of its own).
	return status == http.StatusBadRequest && strings.Contains(strings.ToLower(e.Error.Message), "registration token")
}

// send sends one payload to one app: a retry after a network error, a 429 or
// a 5xx, a fresh access token after a 401, then the outcome.
func (f *FCM) send(ctx context.Context, g *egress.Guard, tenant uuid.UUID, t Target, p Payload, retry time.Duration) (Outcome, int, error) {
	body, _ := json.Marshal(f.Message(t, p))
	client := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: f.hosts(), Purpose: "fcm"}, SendTimeout)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var status int
	var last error
	forget := false
	for try := 0; try < 3; try++ {
		if try > 0 && !forget {
			select {
			case <-ctx.Done():
				return Later, status, ctx.Err()
			case <-time.After(retry):
			}
		}
		access, err := f.token(ctx, client, forget)
		forget = false
		if err != nil {
			last = err
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.BaseURL+"/v1/projects/"+url.PathEscape(f.project)+"/messages:send", bytes.NewReader(body))
		if err != nil {
			return Failed, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		answer, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		status = resp.StatusCode
		switch {
		case status >= 200 && status < 300:
			return Sent, status, nil
		case fcmGone(status, answer):
			return Gone, status, nil
		case status == http.StatusUnauthorized && try == 0:
			forget = true
			last = errors.New("FCM refused the access token")
			continue
		case status == http.StatusTooManyRequests || status >= 500:
			last = fmt.Errorf("FCM answered %d", status)
			continue
		default:
			return Failed, status, fmt.Errorf("FCM answered %d", status)
		}
	}
	// The token not got, FCM unreachable or busy, or still refusing the
	// token: this server's or Google's trouble, not the phone's.
	return Later, status, last
}
