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

// Switching business: the session moves to the other business, the same in
// every way but the business; never a fresh sign-in.
func TestSwitchBusiness(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.tenant(t, "Ada Foods", "ada@switch.test")
	bayo := w.tenant(t, "Bayo Rice", "bayo@switch.test")
	var ada, adaFoods, bayoRice string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM users WHERE email = 'ada@switch.test'`).Scan(&ada); err != nil {
		t.Fatal(err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'Ada Foods'`).Scan(&adaFoods); err != nil {
		t.Fatal(err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'Bayo Rice'`).Scan(&bayoRice); err != nil {
		t.Fatal(err)
	}
	// Ada also works at Bayo Rice.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'viewer')`, bayoRice, ada); err != nil {
		t.Fatal(err)
	}
	anon := &client{t: t, base: w.base}
	out := anon.must(200, "POST", "/v1/auth/login", map[string]any{"email": "ada@switch.test", "password": "correct horse battery", "bearer": true, "tenant_id": adaFoods,
		"device": map[string]any{"id": "0192f000-0000-7000-8000-00000000b001", "name": "Ada's Tecno", "platform": "android"}})
	phone := &client{t: t, base: w.base, token: out["token"].(string)}

	list := phone.must(200, "GET", "/v1/me/businesses", nil)["businesses"].([]any)
	if len(list) != 2 {
		t.Fatalf("businesses: %v", list)
	}
	for _, b := range list {
		m := b.(map[string]any)
		if (m["current"] == true) != (m["id"] == adaFoods) {
			t.Fatalf("current marked wrong: %v", list)
		}
	}
	// It signed in 100 days ago: switching must not make it recent.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE sessions SET created_at = now() - interval '100 days' WHERE token_hash = sha256($1::bytea)`, []byte(phone.token)); err != nil {
		t.Fatal(err)
	}

	sw := phone.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": bayoRice})
	moved := &client{t: t, base: w.base, token: sw["token"].(string)}
	if got := moved.must(200, "GET", "/v1/me", nil)["tenant_id"]; got != bayoRice {
		t.Fatalf("switched to %v", got)
	}
	if st, _ := phone.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("the session switched from still works: %d", st)
	}
	var signed time.Time
	var device *string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT created_at, device_id::text FROM sessions WHERE token_hash = sha256($1::bytea)`, []byte(moved.token)).Scan(&signed, &device); err != nil {
		t.Fatal(err)
	}
	if time.Since(signed) < 99*24*time.Hour || device == nil || *device != "0192f000-0000-7000-8000-00000000b001" {
		t.Fatalf("not the same session in another business: signed in %v ago, device %v", time.Since(signed), device)
	}
	// Still one phone under Your phones.
	if got := moved.must(200, "GET", "/v1/me/devices", nil)["devices"].([]any); len(got) != 1 {
		t.Fatalf("phones after switching: %v", got)
	}
	var audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'auth.switch' AND tenant_id = $1`, bayoRice).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audited %d (%v)", audited, err)
	}

	// Not a business of theirs: refused, and nothing ends.
	other := w.tenant(t, "Chi Stores", "chi@switch.test")
	var chiStores string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'Chi Stores'`).Scan(&chiStores); err != nil {
		t.Fatal(err)
	}
	moved.must(404, "POST", "/v1/me/switch", map[string]any{"tenant_id": chiStores})
	moved.must(200, "GET", "/v1/me", nil)
	_ = other
	_ = bayo
}

// What the review found (findings 1, 2 and 4): signing in again on a phone
// ends its session in every business; one live session per person per
// phone even when two race; a single sign-on session does not switch; two
// switches from one session leave one; a role ending ends the phone's
// session there.
func TestDeviceSessionsAcrossBusinesses(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.tenant(t, "Ada Foods", "ada@across.test")
	bayo := w.tenant(t, "Bayo Rice", "bayo@across.test")
	var ada, adaFoods, bayoRice string
	for q, dst := range map[string]*string{
		`SELECT id FROM users WHERE email = 'ada@across.test'`: &ada,
		`SELECT id FROM tenants WHERE name = 'Ada Foods'`:      &adaFoods,
		`SELECT id FROM tenants WHERE name = 'Bayo Rice'`:      &bayoRice,
	} {
		if err := w.env.DB.Admin.QueryRow(ctx, q).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'viewer')`, bayoRice, ada); err != nil {
		t.Fatal(err)
	}
	anon := &client{t: t, base: w.base}
	device := map[string]any{"id": "0192f000-0000-7000-8000-00000000c001", "name": "Ada's Tecno", "platform": "android"}
	signIn := func(tenant string) *client {
		t.Helper()
		out := anon.must(200, "POST", "/v1/auth/login", map[string]any{"email": "ada@across.test", "password": "correct horse battery", "bearer": true, "tenant_id": tenant, "device": device})
		return &client{t: t, base: w.base, token: out["token"].(string)}
	}

	// Signed in to Bayo Rice on the phone, then again to Ada Foods: the Bayo
	// Rice session ends too, though it is in another business.
	inBayo := signIn(bayoRice)
	inAda := signIn(adaFoods)
	if st, _ := inBayo.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("the phone's session in the other business survived signing in again: %d", st)
	}
	inAda.must(200, "GET", "/v1/me", nil)

	// The database holds it too: a second live session for the same person
	// on the same phone is refused, so two sign-ins racing cannot leave two.
	_, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, tenant_id, expires_at, auth_method, device_id, device_name, device_platform)
		VALUES (sha256('race'::bytea), $1, $2, now() + interval '1 day', 'password', $3, 'x', 'android')`, ada, adaFoods, device["id"])
	if err == nil {
		t.Fatal("two live sessions for one person on one phone")
	}

	// Two switches from one session at once: one moves it, the other finds
	// nothing to move.
	results := make(chan int, 2)
	for range 2 {
		go func() {
			st, _ := inAda.do("POST", "/v1/me/switch", map[string]any{"tenant_id": bayoRice})
			results <- st
		}()
	}
	a, b := <-results, <-results
	if a+b != 200+401 || (a != 200 && b != 200) {
		t.Fatalf("two switches from one session: %d and %d", a, b)
	}
	var live int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE device_id = $1 AND revoked_at IS NULL`, device["id"]).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live sessions on the phone after racing switches: %d (%v)", live, err)
	}

	// A session that signed in with single sign-on does not switch: it would
	// carry one business's sign-on into another's rules.
	viaSSO := signIn(adaFoods)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE sessions SET auth_method = 'sso' WHERE token_hash = sha256($1::bytea)`, []byte(viaSSO.token)); err != nil {
		t.Fatal(err)
	}
	viaSSO.must(409, "POST", "/v1/me/switch", map[string]any{"tenant_id": bayoRice})
	viaSSO.must(200, "GET", "/v1/me", nil)

	// Bayo removes Ada's role: her phone's session in Bayo Rice ends.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE sessions SET auth_method = 'password' WHERE token_hash = sha256($1::bytea)`, []byte(viaSSO.token)); err != nil {
		t.Fatal(err)
	}
	sw := viaSSO.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": bayoRice})
	inBayoAgain := &client{t: t, base: w.base, token: sw["token"].(string)}
	inBayoAgain.must(200, "GET", "/v1/me", nil)
	bayo.must(204, "DELETE", "/v1/members/"+ada+"/roles/viewer", nil, "X-Taskiem-Request", "1")
	if st, _ := inBayoAgain.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("the phone's session outlived Ada's role at Bayo Rice: %d", st)
	}
}
