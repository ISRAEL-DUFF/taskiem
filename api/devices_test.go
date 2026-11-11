package api_test

import (
	"context"
	"testing"
	"time"
)

// Staying signed in on a phone (docs/spec/mobile-app.md §5.2): a device
// session lasts 30 days, is renewed by use up to 180 days from signing in,
// is listed under Your phones and ended from there, replaces that phone's
// earlier session, and ends with everything that ends sessions.
func TestDeviceSessions(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@devices.test")
	ctx := context.Background()
	anon := &client{t: t, base: w.base}
	phone := map[string]any{"id": "0192f000-0000-7000-8000-00000000a001", "name": "Ada's Tecno", "platform": "android"}
	signIn := func(device map[string]any) *client {
		t.Helper()
		out := anon.must(200, "POST", "/v1/auth/login", map[string]any{"email": "ada@devices.test", "password": "correct horse battery", "bearer": true, "device": device})
		return &client{t: t, base: w.base, token: out["token"].(string)}
	}
	expiry := func(c *client) (time.Duration, time.Time) {
		t.Helper()
		var exp, signed time.Time
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT expires_at, created_at FROM sessions WHERE token_hash = sha256($1::bytea)`, []byte(c.token)).Scan(&exp, &signed); err != nil {
			t.Fatal(err)
		}
		return time.Until(exp), signed
	}

	// A device session lasts 30 days; a page's or the CLI's, 12 hours.
	app := signIn(phone)
	if d, _ := expiry(app); d < 29*24*time.Hour || d > 30*24*time.Hour {
		t.Fatalf("a phone's session lasts %v", d)
	}
	if d, _ := expiry(owner); d > 13*time.Hour {
		t.Fatalf("a bearer session without a device lasts %v", d)
	}
	app.must(200, "GET", "/v1/me", nil)

	// Only as a bearer token, and only a phone it can name.
	anon.must(400, "POST", "/v1/auth/login", map[string]any{"email": "ada@devices.test", "password": "correct horse battery", "device": phone})
	anon.must(400, "POST", "/v1/auth/login", map[string]any{"email": "ada@devices.test", "password": "correct horse battery", "bearer": true,
		"device": map[string]any{"id": phone["id"], "name": "x", "platform": "windows"}})
	anon.must(400, "POST", "/v1/auth/login", map[string]any{"email": "ada@devices.test", "password": "correct horse battery", "bearer": true,
		"device": map[string]any{"id": phone["id"], "name": "", "platform": "ios"}})

	// Renewed by use: a session last used two hours ago, 29 days from its
	// end, is put back to 30 days; but never past 180 days from signing in.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE sessions SET expires_at = now() + interval '1 day', last_used_at = now() - interval '2 hours' WHERE token_hash = sha256($1::bytea)`, []byte(app.token)); err != nil {
		t.Fatal(err)
	}
	app.must(200, "GET", "/v1/me", nil)
	if d, _ := expiry(app); d < 29*24*time.Hour {
		t.Fatalf("not renewed by use: %v left", d)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE sessions SET created_at = now() - interval '179 days', expires_at = now() + interval '1 day', last_used_at = now() - interval '2 hours' WHERE token_hash = sha256($1::bytea)`, []byte(app.token)); err != nil {
		t.Fatal(err)
	}
	app.must(200, "GET", "/v1/me", nil)
	if d, _ := expiry(app); d > 25*time.Hour {
		t.Fatalf("renewed past 180 days from signing in: %v left", d)
	}
	// Renewal is not a new sign-in: step-up still reads when it signed in.
	if _, signed := expiry(app); time.Since(signed) < 178*24*time.Hour {
		t.Fatal("renewing moved when the session signed in")
	}

	// Signing in again on the same phone ends its earlier session.
	again := signIn(phone)
	if st, _ := app.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("the phone's earlier session still works: %d", st)
	}
	iphone := map[string]any{"id": "0192f000-0000-7000-8000-00000000a002", "name": "Ada's iPhone", "platform": "ios"}
	other := signIn(iphone)

	// Your phones: both, the one asking marked; a page's session is not one.
	out := again.must(200, "GET", "/v1/me/devices", nil)
	devices := out["devices"].([]any)
	if len(devices) != 2 {
		t.Fatalf("Your phones: %v", devices)
	}
	this := 0
	for _, d := range devices {
		if m := d.(map[string]any); m["this"] == true {
			this++
			if m["name"] != "Ada's Tecno" || m["platform"] != "android" {
				t.Fatalf("this phone: %v", m)
			}
		}
	}
	if this != 1 {
		t.Fatalf("this phone marked %d times", this)
	}
	if got := owner.must(200, "GET", "/v1/me/devices", nil)["devices"].([]any); len(got) != 2 {
		t.Fatalf("from the web: %v", got)
	}

	// Signed out from anywhere: the iPhone, from the Tecno.
	again.must(204, "DELETE", "/v1/me/devices/"+iphone["id"].(string), nil)
	if st, _ := other.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("a phone signed out still works: %d", st)
	}
	again.must(404, "DELETE", "/v1/me/devices/"+iphone["id"].(string), nil)
	var audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'auth.device_removed'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audited %d (%v)", audited, err)
	}

	// Another person cannot see or end Ada's phones.
	bayo := w.tenant(t, "Bayo Rice", "bayo@devices.test")
	if got := bayo.must(200, "GET", "/v1/me/devices", nil)["devices"].([]any); len(got) != 0 {
		t.Fatalf("Bayo sees %v", got)
	}
	bayo.must(404, "DELETE", "/v1/me/devices/"+phone["id"].(string), nil)
	again.must(200, "GET", "/v1/me", nil)

	// A password changed ends every session, the phone's too.
	owner.must(204, "POST", "/v1/me/password", map[string]any{"current_password": "correct horse battery", "new_password": "a brand new battery staple"}, "X-Taskiem-Request", "1")
	if st, _ := again.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("a phone outlives a password change: %d", st)
	}
}
