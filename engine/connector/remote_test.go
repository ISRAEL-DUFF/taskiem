package connector

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func tv1(ts int64, body []byte, secrets ...string) string {
	t := strconv.FormatInt(ts, 10)
	parts := []string{"t=" + t}
	for _, s := range secrets {
		m := hmac.New(sha256.New, []byte(s))
		m.Write([]byte(t + "."))
		m.Write(body)
		parts = append(parts, "v1="+hex.EncodeToString(m.Sum(nil)))
	}
	return strings.Join(parts, ",")
}

// The PGDock-style header: "t=<unix>,v1=<hex HMAC-SHA256 of t.body>", with
// any v1 value accepted (rotation overlap) and a five-minute window.
func TestTimestampedTV1(t *testing.T) {
	Now = func() time.Time { return time.Unix(1791320000, 0) }
	defer func() { Now = time.Now }()
	spec := &VerifySpec{Scheme: "hmac_sha256_timestamped", Header: "PGDock-Signature", SignatureFormat: "t_v1", Tolerance: "5m"}
	body := []byte(`{"id":"evt_1","type":"INSERT"}`)
	const secret = "whsec_new"
	for name, c := range map[string]struct {
		header string
		ok     bool
	}{
		"valid":                {tv1(1791320000, body, secret), true},
		"spaces after commas":  {strings.ReplaceAll(tv1(1791320000, body, secret), ",", ", "), true},
		"rotation, new second": {tv1(1791320000, body, "whsec_old", secret), true},
		"rotation, new first":  {tv1(1791320000, body, secret, "whsec_old"), true},
		"uppercase hex":        {"t=1791320000,v1=" + strings.ToUpper(strings.TrimPrefix(tv1(1791320000, body, secret), "t=1791320000,v1=")), true},
		"only the old secret":  {tv1(1791320000, body, "whsec_old"), false},
		"4 minutes old":        {tv1(1791320000-240, body, secret), true},
		"6 minutes old":        {tv1(1791320000-360, body, secret), false},
		"6 minutes ahead":      {tv1(1791320000+360, body, secret), false},
		"no signature":         {"t=1791320000", false},
		"no timestamp":         {"v1=" + tv1(1791320000, body, secret)[16:], false},
		"empty":                {"", false},
		"another version only": {"t=1791320000,v0=abc", false},
	} {
		err := VerifyWebhook(spec, secret, header("PGDock-Signature", c.header), body)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", name, err, c.ok)
		}
	}
	if VerifyWebhook(spec, secret, header("PGDock-Signature", tv1(1791320000, body, secret)), append(body, ' ')) == nil {
		t.Error("tampered body accepted")
	}
	if VerifyWebhook(spec, "", header("PGDock-Signature", tv1(1791320000, body, "")), body) == nil {
		t.Error("an empty secret accepted")
	}
}

func TestRemoteTriggerManifestRules(t *testing.T) {
	trigger := func(verify, extra string) string {
		return head + "  get: { title: Get, class: read, input: { type: object } }\ntriggers:\n  ev:\n    type: webhook\n    registration: remote\n" +
			"    verify: " + verify + "\n" + extra
	}
	ok := trigger("{ scheme: hmac_sha256_timestamped, header: X-Sig, signature_format: t_v1 }",
		"    options: { type: object, properties: { tables: { type: array } } }\n    test_event: =body.type == 'TEST'\n")
	if _, probs := Parse([]byte(ok)); len(probs) > 0 {
		t.Fatalf("valid remote trigger refused: %v", probs)
	}
	for src, want := range map[string]string{
		trigger("{ scheme: hmac_sha256, header: X-Sig, secret_field: key }", ""):       "secret the provider returned",
		trigger("{ scheme: query_secret, query: token }", ""):                          "cannot verify a remotely registered trigger",
		trigger("{ scheme: hmac_sha256_timestamped, header: X-Sig }", ""):              "timestamp_header",
		trigger("{ scheme: hmac_sha256, header: X-Sig }", "    registration: maybe\n"): "registration",
	} {
		_, probs := Parse([]byte(src))
		if !slices.ContainsFunc(probs, func(p string) bool { return strings.Contains(p, want) }) {
			t.Errorf("want a problem containing %q, got %v", want, probs)
		}
	}
}

type nopRegistrar struct{}

func (nopRegistrar) Create(context.Context, RemoteCall, RemoteSpec) (RemoteState, error) {
	return RemoteState{}, nil
}
func (nopRegistrar) Update(context.Context, RemoteCall, string, RemoteSpec) (RemoteState, error) {
	return RemoteState{}, nil
}
func (nopRegistrar) Get(context.Context, RemoteCall, string) (RemoteState, error) {
	return RemoteState{}, nil
}
func (nopRegistrar) Find(context.Context, RemoteCall, string) (RemoteState, bool, error) {
	return RemoteState{}, false, nil
}
func (nopRegistrar) RotateSecret(context.Context, RemoteCall, string) (string, error) { return "", nil }
func (nopRegistrar) Delete(context.Context, RemoteCall, string) error                 { return nil }

func TestRemoteTriggerNeedsRegistrar(t *testing.T) {
	src := head + "  get: { title: Get, class: read, input: { type: object } }\ntriggers:\n  ev:\n    type: webhook\n    registration: remote\n" +
		"    verify: { scheme: hmac_sha256, header: X-Sig }\n  plain:\n    type: webhook\n    verify: { scheme: hmac_sha256, header: X-Sig, secret_field: k }\n"
	m, probs := Parse([]byte(src))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	get := ActionFunc(func(context.Context, Request) (Response, error) { return Response{}, nil })
	c := &Connector{Manifest: m, Actions: map[string]Action{"get": get}}
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "no registrar") {
		t.Errorf("remote trigger without a registrar: %v", err)
	}
	c.Registrars = map[string]Registrar{"ev": nopRegistrar{}, "plain": nopRegistrar{}}
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "not a remote trigger") {
		t.Errorf("registrar for a manual trigger: %v", err)
	}
	delete(c.Registrars, "plain")
	c.Enrichers = map[string]Enricher{"nope": nil}
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "enricher") {
		t.Errorf("enricher for no trigger: %v", err)
	}
	delete(c.Enrichers, "nope")
	if err := c.Check(); err != nil {
		t.Error(err)
	}
}

func TestHostsFor(t *testing.T) {
	m := &Manifest{Egress: []string{"pgdock.example.com", "${connection.base_url}", "${connection.host}"}}
	got := m.HostsFor(map[string]string{"base_url": "https://db.acme.example:8443/pgdock", "host": "10.0.0.5"})
	if want := []string{"pgdock.example.com", "db.acme.example", "10.0.0.5"}; !slices.Equal(got, want) {
		t.Errorf("hosts %v, want %v", got, want)
	}
	if got := m.HostsFor(map[string]string{"base_url": "https://pgdock.example.com"}); !slices.Equal(got, []string{"pgdock.example.com"}) {
		t.Errorf("empty and repeated hosts: %v", got)
	}
}

func TestRetryAfterDelay(t *testing.T) {
	var _ interface{ RetryAfterDelay() time.Duration } = &HTTPError{}
	if d := (&HTTPError{RetryAfter: 7 * time.Second}).RetryAfterDelay(); d != 7*time.Second {
		t.Error(d)
	}
}
