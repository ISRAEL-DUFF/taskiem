#!/usr/bin/env bash
# Starts Taskiem for the browser tests: a fresh database, the real binary,
# every role, serving the built web app. Needs TASKIEM_TEST_DATABASE_URL (a
# superuser DSN) and psql.
set -euo pipefail
: "${TASKIEM_TEST_DATABASE_URL:?set TASKIEM_TEST_DATABASE_URL to a superuser DSN}"
root=$(cd "$(dirname "$0")/../.." && pwd)
db=taskiem_e2e
psql "$TASKIEM_TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -qc "DROP DATABASE IF EXISTS $db WITH (FORCE)" -c "CREATE DATABASE $db"
dsn=$(printf '%s' "$TASKIEM_TEST_DATABASE_URL" | sed -E "s#/[^/?]*(\?.*)?\$#/$db\1#")
cd "$root"
go build -o bin/taskiem ./cmd/taskiem
export TASKIEM_DATABASE_URL="$dsn"
export TASKIEM_LOCAL_KMS_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
bin/taskiem migrate >/dev/null
TASKIEM_BOOTSTRAP_PASSWORD='correct horse battery' bin/taskiem bootstrap --tenant "E2E" --email owner@e2e.test --name Owner >/dev/null
export TASKIEM_LISTEN=127.0.0.1:18080 TASKIEM_METRICS_LISTEN=127.0.0.1:19090 TASKIEM_WEB_DIR="$root/web/dist" TASKIEM_SECURE_COOKIES=false
# Passkeys need a host name: the passkey test browses http://localhost:18080.
export TASKIEM_PUBLIC_URL=http://localhost:18080 TASKIEM_REQUIRE_ADMIN_PASSKEYS=false
# Every spec signs in from the same address.
export TASKIEM_LOGIN_BURST=100
exec bin/taskiem serve --role all
