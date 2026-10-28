package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// groupRoles are the roles the migrations make. Nothing signs in as them.
var groupRoles = map[string]bool{"taskiem_app": true, "taskiem_dispatch": true, "taskiem_public": true}

// EnsureAppLogin makes the user of appDSN a login that is a member of
// taskiem_app and owns nothing, creating it if it does not exist and
// setting its password to appDSN's (none when appDSN has none). It runs as
// the schema owner (ownerDSN), which needs CREATEROLE, after migrating, as
// the VM kit does for its <database>_app login: the pods then never hold
// the owner's login, which could RESET ROLE past row-level security. The
// two logins must differ. It returns the login's name.
func EnsureAppLogin(ctx context.Context, ownerDSN, appDSN string) (string, error) {
	app, err := pgx.ParseConfig(appDSN)
	if err != nil {
		return "", fmt.Errorf("the app login's URL: %w", err)
	}
	login, password := app.User, app.Password
	if login == "" {
		return "", errors.New("the app login's URL names no user")
	}
	if groupRoles[login] {
		return "", fmt.Errorf("the app login may not be %s, a role the migrations make; name it, for example, taskiem_login", login)
	}
	cfg, err := pgx.ParseConfig(ownerDSN)
	if err != nil {
		return "", err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close(ctx) }()

	var owner string
	var exists, super, owns bool
	err = conn.QueryRow(ctx, `SELECT current_user,
		EXISTS (SELECT FROM pg_roles WHERE rolname = $1),
		COALESCE((SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = $1), false),
		EXISTS (SELECT FROM pg_class c JOIN pg_roles r ON r.oid = c.relowner WHERE r.rolname = $1)`, login).Scan(&owner, &exists, &super, &owns)
	if err != nil {
		return "", err
	}
	switch {
	case login == owner:
		return "", fmt.Errorf("the app login %s is the schema owner: give the pods a login of their own (docs/kubernetes.md#database-logins)", login)
	case super:
		return "", fmt.Errorf("the app login %s is a superuser or bypasses row-level security", login)
	case owns:
		return "", fmt.Errorf("the app login %s owns tables: it could bypass row-level security", login)
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	var stmt string
	if password != "" {
		err = conn.QueryRow(ctx, "SELECT format('"+verb+" ROLE %I LOGIN NOBYPASSRLS PASSWORD %L', $1::text, $2::text)", login, password).Scan(&stmt)
	} else {
		err = conn.QueryRow(ctx, "SELECT format('"+verb+" ROLE %I LOGIN NOBYPASSRLS', $1::text)", login).Scan(&stmt)
	}
	if err != nil {
		return "", err
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return "", fmt.Errorf("%s ROLE %s (the owner needs CREATEROLE): %w", verb, login, err)
	}
	if _, err := conn.Exec(ctx, "GRANT taskiem_app TO "+pgx.Identifier{login}.Sanitize()); err != nil {
		return "", err
	}
	return login, nil
}
