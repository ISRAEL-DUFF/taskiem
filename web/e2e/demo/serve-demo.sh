#!/usr/bin/env bash
# Starts Taskiem for the designer's demo world (web/e2e/demo/seed.ts fills
# it): the e2e binary on a database of its own, taskiem_demo, at
# http://localhost:18090, serving the built web app (web/dist).
#
#   bash web/e2e/demo/serve-demo.sh          # a fresh, empty database
#   bash web/e2e/demo/serve-demo.sh --keep   # keep what is already seeded
#
# Needs psql and Postgres; the superuser DSN defaults to the local one.
# The environment is web/e2e/serve.sh's, minus the fakes the browser tests
# run (SMTP sink, fake WhatsApp Graph API, Termii, iswallet): nothing here
# listens for them, so mail and messages simply queue.
set -euo pipefail
keep=false
for a in "$@"; do
  case "$a" in
    --keep) keep=true ;;
    *) echo "usage: serve-demo.sh [--keep]" >&2; exit 2 ;;
  esac
done
super=${TASKIEM_TEST_DATABASE_URL:-postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable}
root=$(cd "$(dirname "$0")/../../.." && pwd)
db=taskiem_demo
if ! pg_isready -q -h 127.0.0.1 -p 5432; then
  service postgresql start >/dev/null
  for _ in $(seq 30); do pg_isready -q -h 127.0.0.1 -p 5432 && break; sleep 1; done
fi
if $keep; then
  exists=$(psql "$super" -tAc "SELECT 1 FROM pg_database WHERE datname = '$db'")
  [ "$exists" = 1 ] || psql "$super" -v ON_ERROR_STOP=1 -qc "CREATE DATABASE $db"
else
  psql "$super" -v ON_ERROR_STOP=1 -qc "DROP DATABASE IF EXISTS $db WITH (FORCE)" -c "CREATE DATABASE $db"
fi
dsn=$(printf '%s' "$super" | sed -E "s#/[^/?]*(\?.*)?\$#/$db\1#")
cd "$root"
taskiem=bin/taskiem-e2e
go build -tags e2e -o "$taskiem" ./cmd/taskiem
export TASKIEM_DATABASE_URL="$dsn"
export TASKIEM_LOCAL_KMS_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
"$taskiem" migrate >/dev/null
export TASKIEM_LISTEN=127.0.0.1:18090 TASKIEM_METRICS_LISTEN=127.0.0.1:19190 TASKIEM_WEB_DIR="$root/web/dist" TASKIEM_SECURE_COOKIES=false
export TASKIEM_PUBLIC_URL=http://localhost:18090 TASKIEM_REQUIRE_ADMIN_PASSKEYS=false
export TASKIEM_ALERT_FROM=taskiem@demo.test
# Messages to people outside a business (customers, riders).
export TASKIEM_MESSAGE_HASH_KEY=cnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnI=
# The seed signs up and signs in many times from one address.
export TASKIEM_LOGIN_BURST=1000 TASKIEM_ALLOW_SIGNUP=true TASKIEM_SIGNUP_PER_ADDRESS=1000
# Tables, stock, the catalogue and photos live in Xenibase: a fake one
# (web/e2e/demo/_fakexenibase, the Go tests' fake plus file storage), kept
# in memory by a process of its own that outlives this one. Taskiem reaches
# it through the CONNECT proxy that process runs (TASKIEM_EGRESS_PROXY),
# since it refuses loopback addresses for tenant traffic. The same process
# stands in for the malware scanner (TASKIEM_CLAMD_ADDR), passing every
# file, so the demo's chats can carry photos, PDFs and voice notes. --keep with a
# fake that was restarted keeps the database but not the stock and photos.
fake=bin/demo-fakexenibase
fakeenv="$root/bin/demo-fakexenibase.env"
if ! { exec 3<>/dev/tcp/127.0.0.1/18094; } 2>/dev/null || [ ! -s "$fakeenv" ]; then
  go build -o "$fake" ./web/e2e/demo/_fakexenibase
  # DEMO_PASS: loopback addresses the proxy also lets through (the how-to
  # videos' stand-ins for WhatsApp and the model, video/howto/world.sh).
  setsid nohup "$fake" -ca "$root/bin/demo-fakexenibase.pem" -pass "${DEMO_PASS:-}" >"$fakeenv" 2>"$root/bin/demo-fakexenibase.log" </dev/null &
  for _ in $(seq 50); do [ -s "$fakeenv" ] && break; sleep 0.2; done
else
  exec 3>&-
fi
# shellcheck disable=SC2046
export $(cat "$fakeenv")
# Its self-signed certificate is trusted by this process only.
cat /etc/ssl/certs/ca-certificates.crt "$root/bin/demo-fakexenibase.pem" >"$root/bin/demo-ca-bundle.pem"
export SSL_CERT_FILE="$root/bin/demo-ca-bundle.pem"
unset HTTPS_PROXY HTTP_PROXY https_proxy http_proxy
# Storefront orders: the public host's key turns on the inbox.
export TASKIEM_PUBLIC_KEY=ZTJlLXB1YmxpYy1rZXktMzItYnl0ZXMtbG9uZy0hISE=
exec "$taskiem" serve --role all
