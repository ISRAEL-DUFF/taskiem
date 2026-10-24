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
# The E2E tenant is also a partner (embed.spec.ts embeds the builder).
tenant=$(psql "$dsn" -tAc "SELECT id FROM tenants WHERE name = 'E2E'")
bin/taskiem tenants partner "$tenant" --capabilities white_label,custom_domains >/dev/null
# It builds by hand: the technical parts are shown (no-technical-steps N0-5).
psql "$dsn" -qc "UPDATE tenants SET advanced = true WHERE id = '$tenant'"
# It has the Schools area on (schools.spec.ts, accessibility.spec.ts).
psql "$dsn" -qc "INSERT INTO school_settings (tenant_id, enabled, updated_by) VALUES ('$tenant', true, 'e2e')"
# It also publishes a connector in the public catalogue (catalogue.spec.ts).
bash "$root/web/e2e/seed-catalogue.sh" "$dsn" "$tenant"
# A table Taskiem keeps, as the money log makes it (tables.spec.ts): its
# record only, as the superuser may write it; no Xenibase serves its rows.
psql "$dsn" -v ON_ERROR_STOP=1 -q \
  -c "INSERT INTO data_tables (id, tenant_id, name, title, created_by, kept_by) VALUES ('0192e2e0-0000-7000-8000-000000000001', '$tenant', 'money_log', 'Money log', 'taskiem:money_log', 'money_log')" \
  -c "INSERT INTO data_table_columns (tenant_id, table_id, name, type, required, position) VALUES ('$tenant', '0192e2e0-0000-7000-8000-000000000001', 'entry_id', 'text', true, 0), ('$tenant', '0192e2e0-0000-7000-8000-000000000001', 'amount_kobo', 'integer', false, 1)"
# An operator for ops.spec.ts: on the reviewer list, with a one-time link
# to enrol a passkey (bin/ is not committed).
TASKIEM_PUBLIC_URL=http://localhost:18080 bin/taskiem operators add ops@taskiem.test --name Ops | grep -o 'http://.*/ops/enrol#.*' >"$root/bin/e2e-ops-enrol.txt"
bin/taskiem catalogue reviewers add ops@taskiem.test >/dev/null
export TASKIEM_LISTEN=127.0.0.1:18080 TASKIEM_METRICS_LISTEN=127.0.0.1:19090 TASKIEM_WEB_DIR="$root/web/dist" TASKIEM_SECURE_COOKIES=false
# Passkeys need a host name: the passkey test browses http://localhost:18080.
export TASKIEM_PUBLIC_URL=http://localhost:18080 TASKIEM_REQUIRE_ADMIN_PASSKEYS=false
# Email goes to the SMTP sink password.spec.ts listens on.
export TASKIEM_SMTP_URL=smtp://127.0.0.1:12525 TASKIEM_ALERT_FROM=taskiem@e2e.test TASKIEM_ALERT_INTERVAL=2s
# The platform WhatsApp number talks to the fake Graph API whatsapp.spec.ts
# runs (loopback is allowed only because the URL names it).
export TASKIEM_WHATSAPP_PHONE_NUMBER_ID=106540352242922 TASKIEM_WHATSAPP_ACCESS_TOKEN=EAAe2e TASKIEM_WHATSAPP_APP_SECRET=e2e-app-secret \
  TASKIEM_WHATSAPP_VERIFY_TOKEN=e2e-verify TASKIEM_WHATSAPP_TOKEN_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY= \
  TASKIEM_WHATSAPP_GRAPH_URL=http://127.0.0.1:12626/v25.0 TASKIEM_WHATSAPP_DISPLAY_NUMBER="+1 555 000 1111"
# Every spec signs in from the same address.
export TASKIEM_LOGIN_BURST=100
# Self-serve signup, and Termii pointed at the fake onboarding.spec.ts runs
# (loopback is allowed only because the URL names it).
export TASKIEM_ALLOW_SIGNUP=true TASKIEM_TERMII_URL=http://127.0.0.1:12727
# Storefront orders (storefront.spec.ts): the public host's key turns on the inbox.
export TASKIEM_PUBLIC_KEY=ZTJlLXB1YmxpYy1rZXktMzItYnl0ZXMtbG9uZy0hISE=
# Email triggers (email.spec.ts): the mail role takes mail for in.e2e.test
# on a local port instead of 25.
export TASKIEM_MAIL_ENABLED=true TASKIEM_MAIL_DOMAIN=in.e2e.test TASKIEM_MAIL_LISTEN=127.0.0.1:12825
exec bin/taskiem serve --role all
