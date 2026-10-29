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
