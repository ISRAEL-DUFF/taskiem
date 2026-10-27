package connector

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // testing the HMAC-SHA1 scheme
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"
	"time"
)

func header(name, value string) http.Header {
	h := http.Header{}
	h.Set(name, value)
	return h
}

func TestWebhookSchemes(t *testing.T) {
	body := []byte(`{"event":"transaction.successful","data":{"id":"t1"}}`)
	const secret = "api-token"

	// Lenco style: HMAC-SHA512 keyed with the hex SHA-256 of the secret.
	k := sha256.Sum256([]byte(secret))
	m := hmac.New(sha512.New, []byte(hex.EncodeToString(k[:])))
	m.Write(body)
	derived := &VerifySpec{Scheme: "hmac_sha512", Header: "X-Sig", KeyDerivation: "sha256_hex"}
	if err := VerifyWebhook(derived, secret, header("X-Sig", hex.EncodeToString(m.Sum(nil))), body); err != nil {
		t.Errorf("derived key: %v", err)
	}
	plain := hmac.New(sha512.New, []byte(secret))
	plain.Write(body)
	if VerifyWebhook(derived, secret, header("X-Sig", hex.EncodeToString(plain.Sum(nil))), body) == nil {
		t.Error("derived-key scheme accepted a MAC keyed with the raw secret")
	}

	// Base64 of the hex digest, HMAC-SHA1.
	m1 := hmac.New(sha1.New, []byte(secret))
	m1.Write(body)
	b64hex := base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(m1.Sum(nil))))
	spec := &VerifySpec{Scheme: "hmac_sha1", Header: "X-Sig", Encoding: "base64_of_hex"}
	if err := VerifyWebhook(spec, secret, header("X-Sig", b64hex), body); err != nil {
		t.Errorf("base64 of hex: %v", err)
	}
	if VerifyWebhook(spec, secret, header("X-Sig", b64hex), append(body, ' ')) == nil {
		t.Error("tampered body accepted")
	}
	raw64 := base64.StdEncoding.EncodeToString(m1.Sum(nil))
	if err := VerifyWebhook(&VerifySpec{Scheme: "hmac_sha1", Header: "X-Sig", Encoding: "base64"}, secret, header("X-Sig", raw64), body); err != nil {
		t.Errorf("base64: %v", err)
	}

	// Shared secret in a header.
	hs := &VerifySpec{Scheme: "header_secret", Header: "X-Webhook-Secret"}
	if err := VerifyWebhook(hs, secret, header("X-Webhook-Secret", secret), body); err != nil {
		t.Errorf("header secret: %v", err)
	}
	for _, got := range []string{"", "api-toke", "api-token2"} {
		if VerifyWebhook(hs, secret, header("X-Webhook-Secret", got), body) == nil {
			t.Errorf("header secret %q accepted", got)
		}
	}
	if VerifyWebhook(hs, "", header("X-Webhook-Secret", ""), body) == nil {
		t.Error("empty secret accepted")
	}
}

func TestSlackV0Scheme(t *testing.T) {
	body := []byte(`{"type":"event_callback","event_id":"Ev1"}`)
	ts := "1700000000"
	Now = func() time.Time { return time.Unix(1700000030, 0) }
	defer func() { Now = time.Now }()
	m := hmac.New(sha256.New, []byte("signing-secret"))
	m.Write([]byte("v0:" + ts + ":"))
	m.Write(body)
	h := http.Header{}
	h.Set("X-Slack-Signature", "v0="+hex.EncodeToString(m.Sum(nil)))
	h.Set("X-Slack-Request-Timestamp", ts)
	spec := &VerifySpec{Scheme: "slack_v0", Header: "X-Slack-Signature", TimestampHeader: "X-Slack-Request-Timestamp"}
	if err := VerifyWebhook(spec, "signing-secret", h, body); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if VerifyWebhook(spec, "signing-secret", h, append(body, ' ')) == nil {
		t.Error("tampered body accepted")
	}
	Now = func() time.Time { return time.Unix(1700000000+600, 0) }
	if VerifyWebhook(spec, "signing-secret", h, body) == nil {
		t.Error("stale timestamp accepted")
	}
}

func TestBasicAndQuerySecret(t *testing.T) {
	spec := &VerifySpec{Scheme: "basic"}
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("hook:pw")))
	if err := VerifyWebhook(spec, "hook:pw", h, nil); err != nil {
		t.Errorf("basic: %v", err)
	}
	if VerifyWebhook(spec, "hook:other", h, nil) == nil || VerifyWebhook(spec, "", http.Header{}, nil) == nil {
		t.Error("basic accepted wrong credentials")
	}
	qs := &VerifySpec{Scheme: "query_secret", Query: "token"}
	if VerifyWebhook(qs, "s", http.Header{}, nil) == nil {
		t.Error("query_secret must be checked against the URL, not passed by VerifyWebhook")
	}
	ps := &VerifySpec{Scheme: "path_secret"}
	if VerifyWebhook(ps, "s", http.Header{}, nil) == nil {
		t.Error("path_secret must be checked against the URL path, not passed by VerifyWebhook")
	}
	if VerifyPathSecret("s3cret", "s3cret") != nil {
		t.Error("path_secret: the right token refused")
	}
	for _, c := range [][2]string{{"s3cret", "guess"}, {"s3cret", ""}, {"", ""}, {"", "x"}} {
		if VerifyPathSecret(c[0], c[1]) == nil {
			t.Errorf("path_secret accepted %q for %q", c[1], c[0])
		}
	}
}

func TestVerifySvix(t *testing.T) {
	// Svix's published example (docs.svix.com, "Verifying payloads manually").
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	body := []byte(`{"test": 2432232314}`)
	hdr := func(id, ts, sig string) http.Header {
		h := http.Header{}
		h.Set("svix-id", id)
		h.Set("svix-timestamp", ts)
		h.Set("svix-signature", sig)
		return h
	}
	defer func(n func() time.Time) { Now = n }(Now)
	Now = func() time.Time { return time.Unix(1614265330, 0) }
	spec := &VerifySpec{Scheme: "svix"}
	good := "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
	if err := VerifyWebhook(spec, secret, hdr("msg_p5jXN8AQM9LWM0D4loKWxJek", "1614265330", good), body); err != nil {
		t.Fatalf("the published example: %v", err)
	}
	// While the sender rotates: an old signature and the good one.
	if err := VerifyWebhook(spec, secret, hdr("msg_p5jXN8AQM9LWM0D4loKWxJek", "1614265330", "v1,bm90IHRoaXMgb25l "+good), body); err != nil {
		t.Errorf("rotating: %v", err)
	}
	// webhook-* headers (Standard Webhooks) are the same.
	h := http.Header{}
	h.Set("webhook-id", "msg_p5jXN8AQM9LWM0D4loKWxJek")
	h.Set("webhook-timestamp", "1614265330")
	h.Set("webhook-signature", good)
	if err := VerifyWebhook(spec, secret, h, body); err != nil {
		t.Errorf("webhook-* headers: %v", err)
	}
	for name, c := range map[string]struct {
		secret string
		h      http.Header
		body   []byte
	}{
		"changed body":    {secret, hdr("msg_p5jXN8AQM9LWM0D4loKWxJek", "1614265330", good), []byte(`{"test": 2432232315}`)},
		"another id":      {secret, hdr("msg_other", "1614265330", good), body},
		"wrong secret":    {"whsec_" + "c2VjcmV0", hdr("msg_p5jXN8AQM9LWM0D4loKWxJek", "1614265330", good), body},
		"not a whsec key": {"!!", hdr("msg_p5jXN8AQM9LWM0D4loKWxJek", "1614265330", good), body},
		"v2 only":         {secret, hdr("msg_p5jXN8AQM9LWM0D4loKWxJek", "1614265330", "v2,"+good[3:]), body},
		"missing headers": {secret, http.Header{}, body},
		"stale timestamp": {secret, hdr("msg_p5jXN8AQM9LWM0D4loKWxJek", "1614264000", good), body},
	} {
		if err := VerifyWebhook(spec, c.secret, c.h, c.body); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
