package db_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// TestEnsureAppLogin: migrate gives the pods a login of their own, a
// member of taskiem_app that cannot step past row-level security, and
// refuses to treat the schema owner as one.
func TestEnsureAppLogin(t *testing.T) {
	d := dbtest.New(t)
	login := "taskiem_login_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	u, err := url.Parse(d.DSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(login, "pw-"+login)
	appDSN := u.String()
	t.Cleanup(func() { _, _ = d.Admin.Exec(ctx, "DROP ROLE IF EXISTS "+login) })

	// Twice: the second run updates the login in place.
	for range 2 {
		if got, err := db.EnsureAppLogin(ctx, d.DSN, appDSN); err != nil || got != login {
			t.Fatalf("ensure: %q %v", got, err)
		}
	}
	conn, err := pgx.Connect(ctx, appDSN)
	if err != nil {
		t.Fatalf("sign in as the app login: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "SET ROLE taskiem_app"); err != nil {
		t.Fatalf("SET ROLE taskiem_app: %v", err)
	}
	// RESET ROLE leaves it as itself, which owns nothing and so is held to
	// row-level security like taskiem_app.
	if _, err := conn.Exec(ctx, "RESET ROLE"); err != nil {
		t.Fatal(err)
	}
	var super, bypass, owns bool
	if err := conn.QueryRow(ctx, `SELECT rolsuper, rolbypassrls, EXISTS (SELECT FROM pg_class WHERE relowner = r.oid) FROM pg_roles r WHERE rolname = current_user`).Scan(&super, &bypass, &owns); err != nil {
		t.Fatal(err)
	}
	if super || bypass || owns {
		t.Errorf("the app login is too strong: superuser %v, bypassrls %v, owns tables %v", super, bypass, owns)
	}
	if _, err := conn.Exec(ctx, "SET ROLE postgres"); err == nil {
		t.Error("the app login can become the owner")
	}

	// The owner's own URL, or one of the migrations' roles, is refused.
	if _, err := db.EnsureAppLogin(ctx, d.DSN, d.DSN); err == nil || !strings.Contains(err.Error(), "schema owner") {
		t.Errorf("owner as app login: %v", err)
	}
	u.User = url.UserPassword("taskiem_app", "x")
	if _, err := db.EnsureAppLogin(ctx, d.DSN, u.String()); err == nil {
		t.Error("taskiem_app accepted as a login")
	}
}

// An existing login that can already become another role is refused, not
// blessed: the pods could SET ROLE past row-level security (taskiem_dispatch
// sees every tenant's routing rows), or to the owner, a superuser (F76,
// F81). Once the memberships are revoked it is accepted.
func TestEnsureAppLoginRefusesExtraMemberships(t *testing.T) {
	d := dbtest.New(t)
	u, err := url.Parse(d.DSN)
	if err != nil {
		t.Fatal(err)
	}
	strong := "zz_owning_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := d.Admin.Exec(ctx, "CREATE ROLE "+strong+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Admin.Exec(ctx, "DROP OWNED BY "+strong+"; DROP ROLE IF EXISTS "+strong) })
	if _, err := d.Admin.Exec(ctx, "CREATE TABLE zz_owned (id int); ALTER TABLE zz_owned OWNER TO "+strong); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"taskiem_dispatch", "taskiem_public", "postgres", strong} {
		t.Run(role, func(t *testing.T) {
			login := "taskiem_login_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
			u.User = url.UserPassword(login, "pw-"+login)
			t.Cleanup(func() { _, _ = d.Admin.Exec(ctx, "DROP ROLE IF EXISTS "+login) })
			if _, err := d.Admin.Exec(ctx, "CREATE ROLE "+login+" LOGIN"); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Admin.Exec(ctx, "GRANT "+role+" TO "+login); err != nil {
				t.Fatal(err)
			}
			_, err := db.EnsureAppLogin(ctx, d.DSN, u.String())
			if err == nil || !strings.Contains(err.Error(), role) {
				t.Fatalf("a login that is a member of %s: %v", role, err)
			}
			var app bool
			if err := d.Admin.QueryRow(ctx, `SELECT pg_has_role($1, 'taskiem_app', 'MEMBER')`, login).Scan(&app); err != nil || app {
				t.Errorf("the refused login was granted taskiem_app: %v %v", app, err)
			}
			if _, err := d.Admin.Exec(ctx, "REVOKE "+role+" FROM "+login); err != nil {
				t.Fatal(err)
			}
			if _, err := db.EnsureAppLogin(ctx, d.DSN, u.String()); err != nil {
				t.Errorf("after the revoke: %v", err)
			}
		})
	}
}
