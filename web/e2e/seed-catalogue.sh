#!/usr/bin/env bash
# Seeds the public connector catalogue for catalogue.spec.ts: the E2E
# tenant as a verified publisher ("e2e") with the example ledger connector
# published as p_e2e_ledger 1.0.0, reviewed by a reviewer outside it; and,
# for ops.spec.ts, p_e2e_ledger 1.1.0 waiting in the review queue. The
# automated checks and the review run through their own tests; here the
# rows are written directly, as the database superuser. Usage:
#   seed-catalogue.sh DSN TENANT_ID
set -euo pipefail
dsn=$1 tenant=$2
root=$(cd "$(dirname "$0")/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
"$root/bin/taskiem" connector build -o "$tmp/connector.wasm" "$root/examples/wasm-connector" >/dev/null
sed 's/x_example_ledger/p_e2e_ledger/' "$root/examples/wasm-connector/manifest.yaml" >"$tmp/manifest.yaml"
sed 's/^version: 1.0.0$/version: 1.1.0/' "$tmp/manifest.yaml" >"$tmp/manifest11.yaml"
base64 -w0 "$tmp/connector.wasm" >"$tmp/module.b64"
base64 -w0 "$tmp/manifest.yaml" >"$tmp/manifest.b64"
base64 -w0 "$tmp/manifest11.yaml" >"$tmp/manifest11.b64"
psql "$dsn" -v ON_ERROR_STOP=1 -q -v tenant="$tenant" <<SQL
CREATE TEMP TABLE seed (module text, manifest text, manifest11 text);
\copy seed (module) FROM '$tmp/module.b64'
\copy seed (manifest) FROM '$tmp/manifest.b64'
\copy seed (manifest11) FROM '$tmp/manifest11.b64'
INSERT INTO connector_publishers (tenant_id, slug, name, public_key, key_id, requested_by)
  VALUES (:'tenant', 'e2e', 'E2E Ledgerworks', decode(repeat('00', 32), 'hex'), 'e2e', 'seed');
SELECT taskiem_catalogue_set_publisher('e2e', 'verified', 'seed', '');
INSERT INTO catalogue_versions (id, publisher_tenant, publisher, connector_id, version, manifest, module, module_digest, package_digest,
    key_id, signature, licence, conformance, attestation, checks, state, submitted_by)
  SELECT gen_random_uuid(), :'tenant', 'e2e', 'p_e2e_ledger', '1.0.0', convert_from(decode(max(manifest), 'base64'), 'UTF8'),
    decode(max(module), 'base64'), sha256(decode(max(module), 'base64')), sha256(decode(max(module), 'base64')),
    'e2e', '\x01', 'Apache-2.0', '{}', '{"original_work": true, "contact": "dev@e2e.test"}', '{"passed": true}', 'in_review', 'seed'
  FROM seed;
SELECT taskiem_catalogue_set_reviewer('reviewer@taskiem.test', true, 'seed');
SELECT taskiem_catalogue_review(id, 'reviewer@taskiem.test', true, 'seeded for the browser tests',
  '{"identity": true, "classes": true, "hosts": true, "pii": true, "credentials": true, "conformance": true, "licence": true, "docs": true}') FROM catalogue_versions WHERE connector_id = 'p_e2e_ledger';
UPDATE catalogue_versions SET state = 'published', published_at = now() WHERE connector_id = 'p_e2e_ledger';
INSERT INTO catalogue_versions (id, publisher_tenant, publisher, connector_id, version, manifest, module, module_digest, package_digest,
    key_id, signature, licence, conformance, attestation, checks, state, submitted_by)
  SELECT gen_random_uuid(), :'tenant', 'e2e', 'p_e2e_ledger', '1.1.0', convert_from(decode(max(manifest11), 'base64'), 'UTF8'),
    decode(max(module), 'base64'), sha256(decode(max(module), 'base64')), sha256(decode(max(manifest11), 'base64')),
    'e2e', '\x01', 'Apache-2.0', '{}', '{"original_work": true, "contact": "dev@e2e.test"}',
    '{"passed": true, "checks": [{"name": "signature", "pass": true}, {"name": "conformance", "pass": true, "detail": "2 cases"}]}', 'in_review', 'seed'
  FROM seed;
SQL
