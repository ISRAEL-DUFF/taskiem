# Operating Taskiem

One binary runs every role (spec 2.1, 15.4). A small install runs `taskiem serve` (all roles in one process); larger ones run each role separately and scale workers.

## Commands

| Command | What it does |
| --- | --- |
| `taskiem migrate` | Applies database migrations as the schema owner. Forward-only. |
| `taskiem bootstrap --tenant NAME --email EMAIL [--name NAME]` | Creates the first tenant, its owner, a Default workspace, and the `dev` and `prod` environments. Password from `TASKIEM_BOOTSTRAP_PASSWORD` (12+ characters). |
| `taskiem serve [--role ROLE]` | Runs `api`, `edge`, `orchestrator`, `scheduler`, `worker`, or `all` (default; also `TASKIEM_ROLE`). |
| `taskiem audit verify FILE` | Recomputes every hash and link of an audit export (`GET /v1/audit/export`) without the database. Exits non-zero on a broken chain. |
| `taskiem validate FILE...` | Validates `*.wd.json` definitions and connector manifests. |
| `taskiem healthcheck` | Probes the local API's `/readyz` (for images without a shell). |

## Roles

| Role | Runs | Listens |
| --- | --- | --- |
| `api` | REST API (`/v1`), web app (`TASKIEM_WEB_DIR`); with `all`, webhooks too (`/hooks`) | `TASKIEM_LISTEN` (`:8080`) |
| `edge` | Webhook and connector-event ingest only | `TASKIEM_EDGE_LISTEN` (`:8081`) |
| `orchestrator` | Decides runs left with undecided events (most decisions are inline) | — |
| `scheduler` | Timers, lease recovery, the orchestrator sweep, cron triggers, retention purge, partitions | — |
| `worker` | Steps from `TASKIEM_WORKER_QUEUES` (`connector,sandbox`); drains in-flight steps for up to 30 s on shutdown | — |

Every role serves Prometheus metrics on `TASKIEM_METRICS_LISTEN` (`:9090`). Running several schedulers or orchestrators is safe: every claim uses `SKIP LOCKED` and every firing is deduplicated.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `TASKIEM_DATABASE_URL` | — | Required. |
| `TASKIEM_DATABASE_ROLE` | `taskiem_app` | Role set on every connection (`SET ROLE`); row-level security applies to it. Empty disables. |
| `TASKIEM_DATABASE_POOL` | `20` | Connections per process. |
| `TASKIEM_KMS` | `local` | `openbao` in production: transit key `TASKIEM_KMS_KEY` (`taskiem`) at `TASKIEM_OPENBAO_ADDR` with `TASKIEM_OPENBAO_TOKEN`. `local` takes a 32-byte base64 `TASKIEM_LOCAL_KMS_KEY` and is for development. |
| `TASKIEM_ARCHIVE_DIR` | — | Where runs past retention are archived (gzipped JSON lines) before purging. Unset: nothing is purged. |
| `TASKIEM_ANCHOR_KEY` | — | Ed25519 seed (32 bytes, base64: `openssl rand -base64 32`) that signs the daily audit-chain anchors. Keep it like a KMS key; its public half is served at `GET /v1/audit/anchors`. Unset: no anchoring. |
| `TASKIEM_ANCHOR_DIR` | — | Where the scheduler role appends each tenant's anchors (`<tenant>.jsonl`). Point it at write-once storage (an object-lock bucket, an append-only volume): anchors are only worth as much as their copy outside the database. |
| `TASKIEM_WEB_DIR` | — | Built web app to serve (`/web` in the image). |
| `TASKIEM_SECURE_COOKIES` | `true` | Set `false` only for plain-HTTP local use. |
| `TASKIEM_PUBLIC_URL` | — | Where people reach the web app (`https://…`, or `http://localhost:…`). Turns on passkeys, which are bound to it |
| `TASKIEM_PASSKEY_RP_ID` | the URL's host | The passkey domain, if passkeys should work across subdomains (a parent of the URL's host) |
| `TASKIEM_REQUIRE_ADMIN_PASSKEYS` | `true` with a public URL | Hold administrators to passkeys ([governance](governance.md#passkeys)) |
| `TASKIEM_TRUST_PROXY` | `false` | Take the client address from the last `X-Forwarded-For` hop (behind a load balancer only). |
| `TASKIEM_ALLOW_SIGNUP` | `false` | Self-serve `POST /v1/signup`. |
| `TASKIEM_ISWALLET_URL`, `TASKIEM_PAYSTACK_URL`, `TASKIEM_TERMII_URL`, `TASKIEM_DOJAH_URL`, `TASKIEM_FLUTTERWAVE_URL`, `TASKIEM_GETANCHOR_URL`, `TASKIEM_LENCO_URL`, `TASKIEM_BREET_URL` | provider defaults | Point connectors at another environment; the egress allow-list follows. iswallet defaults to its **sandbox**; set its production URL at go-live. Anchor and Lenco connections choose their provider's sandbox themselves (`environment: sandbox`), Breet's with the same field, Flutterwave's by the key. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | Enables OpenTelemetry trace export (OTLP/HTTP); standard `OTEL_*` variables apply. |

## Database roles

Migrations create `taskiem_app` (the application, under forced row-level security) and `taskiem_dispatch` (owner of the cross-tenant claim and authentication functions). Neither can log in. In production create a login user for the engine and grant it the app role only:

```sql
CREATE ROLE taskiem LOGIN PASSWORD '...';
GRANT taskiem_app TO taskiem;
```

Run `taskiem migrate` as the schema owner, and `taskiem serve` as `taskiem`.

## Webhooks

- Webhook triggers: `POST /hooks/{tenant}/{path}?env=prod`. `hmac` expects `X-Taskiem-Signature: sha256=<hex HMAC-SHA256 of the body>`; `bearer` expects `Authorization: Bearer <token>`. The key is the environment secret `webhook_<WD id>`.
- Connector events: `POST /hooks/{tenant}/connectors/{connector}/{trigger}?env=prod`, verified with the connection's credentials. `GET /v1/workflows/{id}/triggers` lists the exact URLs.
- Replies: 202 with the run id (a duplicate returns the original), 401 on a bad signature, 422 when the body does not match the inputs schema, 429 with `Retry-After` above the tenant's ingest ceiling, 503 with `Retry-After` when nothing was recorded.

## Metrics

| Metric | Labels |
| --- | --- |
| `taskiem_http_requests_total`, `taskiem_http_request_duration_seconds` | route pattern, method, status |
| `taskiem_ingest_deliveries_total` | kind (`webhook`, `connector`, `schedule`), result |
| `taskiem_steps_total`, `taskiem_step_duration_seconds` | queue, target (connector or step type), outcome |
| `taskiem_queue_ready`, `taskiem_queue_leased`, `taskiem_queue_oldest_ready_seconds` | queue |
| `taskiem_timers_fired_total`, `taskiem_lease_expiries_total`, `taskiem_runs_swept_total` | — |
| `taskiem_connector_drift_total` | connector, action, kind (`type`, `enum`, `missing`) |

**Contract drift.** After every successful connector call the worker compares the output with the action's declared output schema (`engine/drift`). A departure (a field of another type, a value outside an enum, a required field missing) is recorded per tenant (`connector_drift`; tenants see it under Connections), counted in `taskiem_connector_drift_total`, and logged at warning level the first time. The step still completes. Alert on any increase for built-in connectors: it means a provider changed its API and the connector needs updating.

Traces span API requests, ingest, and worker steps, with `tenant_id`, `run_id`, and `step_id` attributes.

## Docker Compose

`deploy/docker-compose.yml` runs Postgres, OpenBao (dev mode), and Taskiem with every role; see the comments at its top. OpenBao dev mode keeps keys in memory: restarting it loses every encrypted secret. Use a persistent, unsealed OpenBao in any shared environment.
