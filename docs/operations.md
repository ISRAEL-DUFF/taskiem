# Operating Taskiem

One binary runs every role (spec 2.1, 15.4). A small install runs `taskiem serve` (all roles in one process); larger ones run each role separately and scale workers.

## Commands

| Command | What it does |
| --- | --- |
| `taskiem migrate` | Applies database migrations as the schema owner. Forward-only. |
| `taskiem bootstrap --tenant NAME --email EMAIL [--name NAME]` | Creates the first tenant, its owner, a Default workspace, and the `dev` and `prod` environments. Password from `TASKIEM_BOOTSTRAP_PASSWORD` (12+ characters). |
| `taskiem serve [--role ROLE]` | Runs `api`, `edge`, `orchestrator`, `scheduler`, `worker`, or `all` (default; also `TASKIEM_ROLE`). |
| `taskiem tenants limits TENANT_ID [--set KEY=VALUE]...` | Shows a tenant's plan limits, usage and recent limit hits; `--set` changes a limit (`default` returns it to the platform default). Audited as `limits.set`. See [plan limits](#plan-limits). |
| `taskiem tenants partner TENANT_ID [--max-subtenants N] [--subtenant-runs-per-day N] [--subtenant-runs-per-month N] [--disable]` | Makes a tenant a partner, which may create sub-tenants and embed apps through the partner admin API, and sets its partner-wide caps (0: no cap; flags left out keep their value). `--disable` stops it being one: its sub-tenants are kept, unreachable, and their end-user tokens stop. Audited as `partner.enable` and `partner.disable`. See [embedding](embedding.md). |
| `taskiem tenants keys TENANT_ID [status \| rotate [--wait] \| rewrap \| check]` | Shows a tenant's encryption keys: tenant key versions and what wraps them, its own key's health (BYOK), re-wrapping progress and parked steps. `rotate` adds a tenant key version (`--wait` re-wraps at once), `rewrap` re-wraps now, `check` checks the keys and resumes steps parked while they were unavailable. Audited in the tenant's log as `platform_admin`. See [BYOK](byok.md#operators). |
| `taskiem pools [assign POOL \| unassign] [--tenant TENANT_ID \| --plan PLAN] [--force]` | Lists live workers by pool and queue, queue depth by pool, and every routing; `assign` routes a tenant (with its sub-tenants) or a plan's tenants to a dedicated worker pool, refused while no live worker serves it unless `--force`; `unassign` removes the routing. Audited in the tenant's log as `platform_admin`; every change kept in `worker_pool_changes`. See [cloud](cloud.md#dedicated-worker-pools). |
| `taskiem audit verify FILE` | Recomputes every hash and link of an audit export (`GET /v1/audit/export`) without the database. Exits non-zero on a broken chain. |
| `taskiem validate FILE...` | Validates `*.wd.json` definitions and connector manifests. |
| `taskiem healthcheck` | Probes the local API's `/readyz` (for images without a shell). |

## Roles

| Role | Runs | Listens |
| --- | --- | --- |
| `api` | REST API (`/v1`), web app (`TASKIEM_WEB_DIR`), the WhatsApp notifier (approval requests, how chat-started runs ended), the USSD notifier (hand-off backstop, outcome SMS, session purge; [USSD](ussd.md)); with `all`, webhooks too (`/hooks`, `/channels/whatsapp`, `/channels/ussd`) | `TASKIEM_LISTEN` (`:8080`) |
| `edge` | Webhook and connector-event ingest, Git push hooks, the platform WhatsApp number's webhook (`/channels/whatsapp`, [WhatsApp](whatsapp.md)), and USSD aggregators' callbacks (`/channels/ussd`, [USSD](ussd.md)) | `TASKIEM_EDGE_LISTEN` (`:8081`) |
| `orchestrator` | Decides runs left with undecided events (most decisions are inline) | — |
| `scheduler` | Timers, lease recovery, the orchestrator sweep, cron triggers, admission of queued runs, retention purge, partitions, audit anchoring, alerts, partner webhooks ([embedding](embedding.md#6-partner-webhooks)), and the hourly digest of secret reads into the audit chain ([compliance](compliance.md#secret-use)) | — |
| `worker` | Steps from `TASKIEM_WORKER_QUEUES` (`connector,sandbox`; `container` for container steps, on their own pool with `TASKIEM_CONTAINER_*`, see [container steps](container-steps.md#operator-setup)), for the tenants of its worker pool (`TASKIEM_WORKER_POOL`, [cloud](cloud.md#dedicated-worker-pools)); drains in-flight steps for up to 30 s on shutdown | — |

Every role serves Prometheus metrics (`/metrics`) and a liveness check (`/healthz`) on `TASKIEM_METRICS_LISTEN` (`:9090`), so roles without the API can be probed too. Running several schedulers or orchestrators is safe: every claim uses `SKIP LOCKED` and every firing is deduplicated.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `TASKIEM_DATABASE_URL` | — | Required. For a highly available Postgres, every host with `target_session_attrs=read-write` ([cloud](cloud.md#connection-strings)). |
| `TASKIEM_DATABASE_READ_URL`, `TASKIEM_DATABASE_READ_MAX_LAG` | —, `10s` | A read replica for the run list and dashboard (`api` role), set aside while it lags beyond the bound or fails ([cloud](cloud.md#read-replica)) |
| `TASKIEM_DATABASE_RETRY_WINDOW` | `30s` | How long a worker keeps a step's outcome through a database failover before giving up to lease expiry ([cloud](cloud.md#what-happens-during-a-failover)) |
| `TASKIEM_WORKER_POOL` | `shared` | The worker pool this worker serves ([cloud](cloud.md#dedicated-worker-pools)) |
| `TASKIEM_DATABASE_ROLE` | `taskiem_app` | Role set on every connection (`SET ROLE`); row-level security applies to it. Empty disables. |
| `TASKIEM_DATABASE_POOL` | `20` | Connections per process. |
| `TASKIEM_KMS` | `local` | `openbao` in production: transit key `TASKIEM_KMS_KEY` (`taskiem`) at `TASKIEM_OPENBAO_ADDR` with `TASKIEM_OPENBAO_TOKEN`. `local` takes a 32-byte base64 `TASKIEM_LOCAL_KMS_KEY` and is for development. |
| `TASKIEM_ARCHIVE_DIR` | — | Where runs past retention are archived (gzipped JSON lines) before purging. Unset: nothing is purged. |
| `TASKIEM_ANCHOR_KEY` | — | Ed25519 seed (32 bytes, base64: `openssl rand -base64 32`) that signs the daily audit-chain anchors. Keep it like a KMS key; its public half is served at `GET /v1/audit/anchors`. Unset: no anchoring. |
| `TASKIEM_ANCHOR_DIR` | — | Where the scheduler role appends each tenant's anchors (`<tenant>.jsonl`). Point it at write-once storage (an object-lock bucket, an append-only volume): anchors are only worth as much as their copy outside the database. |
| `TASKIEM_SMTP_URL` | — | Mail server for alert emails and password-reset links: `smtp://user:pass@host:587` (STARTTLS, required when a password is given) or `smtps://user:pass@host:465`. Without it, email alert deliveries fail with a reason ([alerts](alerts.md)), and "Forgot password?" sends nothing (it still answers, and logs a warning; [passwords](governance.md#passwords)) |
| `TASKIEM_ALERT_FROM` | — | Sender address of alert and password emails; required with `TASKIEM_SMTP_URL` |
| `TASKIEM_WASM_CACHE` | the user cache directory | Where workers keep compiled WebAssembly (the Python interpreter takes seconds to compile the first time). Point it at a writable directory that survives restarts |
| `TASKIEM_LOGIN_BURST` | 10 | Sign-in attempts one address may make at once, then one every six seconds |
| `TASKIEM_WEB_DIR` | — | Built web app to serve (`/web` in the image). |
| `TASKIEM_SECURE_COOKIES` | `true` | Set `false` only for plain-HTTP local use. |
| `TASKIEM_PUBLIC_URL` | — | Where people reach the web app (`https://…`, or `http://localhost:…`). Turns on passkeys, which are bound to it; password-reset links point at it (none are sent without it) |
| `TASKIEM_HOOKS_URL` | `TASKIEM_PUBLIC_URL` + `/hooks` | Where providers reach the edge's `/hooks` (`https://hooks.example.com/hooks`). Taskiem points the subscriptions it registers itself at it (PGDock webhooks, [decision 0021](decisions/0021-remote-trigger-registration.md)); without it none is created and each shows why. Set it on the api and scheduler roles |
| `TASKIEM_PGDOCK_URL` | — | The platform's PGDock server, for PGDock connections that name none ([PGDock](integrations/pgdock.md#connection)). PGDock has not published its cloud address, so there is no default |
| `TASKIEM_PASSKEY_RP_ID` | the URL's host | The passkey domain, if passkeys should work across subdomains (a parent of the URL's host) |
| `TASKIEM_REQUIRE_ADMIN_PASSKEYS` | `true` with a public URL | Hold administrators to passkeys ([governance](governance.md#passkeys)) |
| `TASKIEM_HSTS` | `max-age=31536000` with an https public URL | The `Strict-Transport-Security` header on the platform's own host (never on partners' custom domains): a value starting `max-age=` (add `includeSubDomains` only if every subdomain is HTTPS), or `off` |
| `TASKIEM_TENANT_CODE_CONCURRENCY` | the CPUs, at least 2 | How much tenant code (flow compile, code checks, catalogue and connector checks) one API process runs at once; more waits up to 5 s, then gets 503 `code_checks_busy` |
| `TASKIEM_TENANT_CODE_PER_TENANT` | half of the above, at least 1 | How much of it one tenant holds at once; more gets 429 `tenant_code_busy`. Refusals: `taskiem_tenant_code_refused_total{reason}` |
| `TASKIEM_TRUST_PROXY` | `false` | Take the client address from the last `X-Forwarded-For` hop (behind a load balancer only). |
| `TASKIEM_ALLOW_SIGNUP` | `false` | Self-serve signup ([onboarding](onboarding.md)). Needs email (`TASKIEM_SMTP_URL`, `TASKIEM_ALERT_FROM`, a public URL) for confirmation links |
| `TASKIEM_SIGNUP_PER_ADDRESS` | `5` | Signups per client address a day, across replicas; negative: no limit |
| `TASKIEM_SIGNUP_BLOCKED_DOMAINS` | — | Email domains that may not sign up, besides the built-in throwaway services (comma-separated) |
| `TASKIEM_DOCS_URL` | — | The public docs site the web app's help panels link to |
| `TASKIEM_ISWALLET_URL`, `TASKIEM_PAYSTACK_URL`, `TASKIEM_TERMII_URL`, `TASKIEM_DOJAH_URL`, `TASKIEM_FLUTTERWAVE_URL`, `TASKIEM_GETANCHOR_URL`, `TASKIEM_LENCO_URL`, `TASKIEM_BREET_URL` | provider defaults | Point connectors at another environment; the egress allow-list follows. Tests only: an IP-literal loopback URL (`http://127.0.0.1:PORT`) lets that connector, and only it, reach that port on loopback (a warning is logged at start). iswallet defaults to its **sandbox**; set its production URL at go-live. Anchor and Lenco connections choose their provider's sandbox themselves (`environment: sandbox`), Breet's with the same field, Flutterwave's by the key. |
| `TASKIEM_WHATSAPP_PHONE_NUMBER_ID` | — | Turns on the platform WhatsApp number ([WhatsApp](whatsapp.md)). With it, `TASKIEM_WHATSAPP_ACCESS_TOKEN` (system user token), `TASKIEM_WHATSAPP_APP_SECRET` (verifies webhook signatures), `TASKIEM_WHATSAPP_VERIFY_TOKEN` (subscription handshake) and `TASKIEM_WHATSAPP_TOKEN_KEY` (32 bytes, base64: signs approval decision tokens; the same on every `api` and `edge` pod) are required. Put them in the Secret, never in chart values. Set them on the `api`, `edge` and `scheduler` roles |
| `TASKIEM_WHATSAPP_DISPLAY_NUMBER`, `TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE`, `TASKIEM_WHATSAPP_DEFAULT_COUNTRY` | —, `en`, `234` | The number as shown on the Account page; the language the templates were approved in; the calling code for numbers typed with a leading 0 |
| `TASKIEM_WHATSAPP_FLOWS_PRIVATE_KEY` | — | Turns on WhatsApp Flows (input forms and the approval PIN, [WhatsApp](whatsapp.md#flows)): the PEM of an unencrypted RSA private key of at least 2048 bits (PKCS#1 or PKCS#8; `\n` may stand for new lines). Generate it with `openssl genrsa -out flows.pem 2048` and upload the public half (`openssl rsa -in flows.pem -pubout`) to the shared number with `POST /{phone-number-id}/whatsapp_business_encryption`; Taskiem uploads it to tenants' own numbers when they connect. Secret only; never logged. Set it on the `edge` and `api` roles (with `all`, once). Without it inputs are asked in chat and step-up uses the web link |
| `TASKIEM_WHATSAPP_GRAPH_URL` | `https://graph.facebook.com/v25.0` | Tests only: a fake Graph API (a loopback address here is allowed through the egress guard for the platform number only) |
| `TASKIEM_DEFAULT_<LIMIT>` | see [plan limits](#plan-limits) | Platform default for one plan limit, for tenants without their own (for example `TASKIEM_DEFAULT_RUNS_PER_MONTH=100000`). Set the same values on every role. |
| `ANTHROPIC_API_KEY` | — | Turns AI building on with Claude ([AI](ai.md#configuration)). Keep it in a secret; it is read from the environment only. |
| `TASKIEM_AI_PROVIDER`, `TASKIEM_AI_MODEL`, `TASKIEM_AI_BASE_URL`, `TASKIEM_AI_API_KEY`, `TASKIEM_AI_EFFORT`, `TASKIEM_AI_MAX_TOKENS`, `TASKIEM_AI_FALLBACKS` | Claude `claude-opus-5-5` when `ANTHROPIC_API_KEY` is set; otherwise off | The model provider for AI building: `anthropic`, `selfhosted` (an OpenAI-compatible endpoint at `TASKIEM_AI_BASE_URL`) or `off`; the model; thinking effort (`high`); max tokens per answer (32000); server-side refusal fallbacks (on). Set on the `api` role. See [AI](ai.md#configuration). |
| `TASKIEM_BILLING` | `off` | `on` turns plans, subscriptions and naira payments on ([billing](billing.md)); off, every tenant is on the internal `self_hosted` plan (the defaults below, every feature). With it: `TASKIEM_BILLING_PLANS` (`deploy/plans.yaml`), `TASKIEM_BILLING_PROVIDER` (`paystack`), `TASKIEM_BILLING_PAYSTACK_SECRET_KEY`, `TASKIEM_BILLING_FLUTTERWAVE_SECRET_KEY` and `TASKIEM_BILLING_FLUTTERWAVE_WEBHOOK_HASH` (the platform's merchant accounts, from the Secret). Set on the `api` and `scheduler` roles |
| `TASKIEM_BYOK_CACHE_TTL` | `5m` | How long a tenant key that depends on a tenant's own key (BYOK) stays unwrapped in memory: the bound on how long revoking it takes. Set on every role ([BYOK](byok.md#operators)) |
| `TASKIEM_KEY_CHECK_INTERVAL`, `TASKIEM_KEY_DESTROY_AFTER` | `1m`, `24h` | How often the scheduler's key job checks tenants' own keys, re-wraps after rotations and resumes parked steps; how long a retired, unused tenant key version is kept before its material is destroyed |
| `TASKIEM_BYOK_ALLOW_PRIVATE` | off | Let tenants' own KMS addresses resolve to private ranges. Dedicated single-tenant deployments only |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | Enables OpenTelemetry trace export (OTLP/HTTP); standard `OTEL_*` variables apply. |

## Database roles

Migrations create `taskiem_app` (the application, under forced row-level security) and `taskiem_dispatch` (owner of the cross-tenant claim and authentication functions). Neither can log in. In production create a login user for the engine and grant it the app role only:

```sql
CREATE ROLE taskiem LOGIN PASSWORD '...';
GRANT taskiem_app TO taskiem;
```

Run `taskiem migrate` as the schema owner, and `taskiem serve` as `taskiem`.

## Webhooks

- Webhook triggers: `POST /hooks/{tenant}/{path}?env=prod`. `hmac` expects `X-Taskiem-Signature: sha256=<hex HMAC-SHA256 of the body>`; `bearer` expects `Authorization: Bearer <token>`. The key is the environment secret `webhook_<WD id>`; workflows cannot read it (nor `git_credentials` or `git_webhook_secret`): an expression or code step naming one fails.
- The platform WhatsApp number: `GET` and `POST /channels/whatsapp` (Meta's callback URL), verified with `TASKIEM_WHATSAPP_APP_SECRET`, separate from tenants' connector triggers ([WhatsApp](whatsapp.md)). Tenants' own numbers on their own Meta apps: `/channels/whatsapp/n/{phone number id}`, verified with that app's secret. The WhatsApp Flows data endpoint: `POST /channels/whatsapp/flows` (Taskiem's app) and `/channels/whatsapp/flows/{phone number id}` (an own app). All are served by the `edge` role.
- USSD: `POST /channels/ussd/{tenant}/{provider}?token=<channel token>` (Africa's Talking's callback URL for the service code), served by the `edge` role. The token is made by `PUT /v1/ussd/channels/{provider}` and shown once; keep query strings out of ingress logs. Every callback is answered within 2 s with `CON`/`END` text, never a 5xx: a slow database gets "busy, try again". Sessions live in `ussd_sessions` (purged a day after expiry); edge replicas share them through the database, so no sticky sessions are needed. Behind a load balancer set `TASKIEM_TRUST_PROXY` for channels with an address allow-list ([USSD](ussd.md#setting-up)).
- Connector events: `POST /hooks/{tenant}/connectors/{connector}/{trigger}?env=prod`, verified with the connection's credentials. `GET /v1/workflows/{id}/triggers` lists the exact URLs.
- Replies: 202 with the run id (a duplicate returns the original; `"queued": true` when the tenant is above its soft ingest rate and the run waits for admission), 401 on a bad signature, 413 above the tenant's payload limit, 422 when the body does not match the inputs schema, 429 with `Retry-After` and a `code` above the tenant's ingest ceiling (`rate_limited`), with a full backlog (`backlog_full`) or past a run quota (`quota_exceeded`), 503 with `Retry-After` when nothing was recorded. Nothing is recorded with a 429, so the provider's retry is not lost.

## Metrics

| Metric | Labels |
| --- | --- |
| `taskiem_http_requests_total`, `taskiem_http_request_duration_seconds` | route pattern, method, status |
| `taskiem_ingest_deliveries_total` | kind (`webhook`, `connector`, `schedule`), result |
| `taskiem_steps_total`, `taskiem_step_duration_seconds` | queue, target (connector or step type), outcome |
| `taskiem_queue_ready`, `taskiem_queue_leased`, `taskiem_queue_oldest_ready_seconds` | queue |
| `taskiem_pool_ready`, `taskiem_pool_leased`, `taskiem_pool_oldest_ready_seconds` | pool, queue ([worker pools](cloud.md#dedicated-worker-pools)) |
| `taskiem_db_retries_total` | outcome (`retried`, `recovered`, `gave_up`): transactions retried through a failover ([cloud](cloud.md#what-happens-during-a-failover)) |
| `taskiem_db_replica_lag_seconds`, `taskiem_db_replica_in_use`, `taskiem_db_reads_total` | `taskiem_db_reads_total`: target (`replica`, `fallback`, `no_replica`) ([read replica](cloud.md#read-replica)) |
| `taskiem_timers_fired_total`, `taskiem_lease_expiries_total`, `taskiem_runs_swept_total` | — |
| `taskiem_connector_drift_total` | connector, action, kind (`type`, `enum`, `missing`) |
| `taskiem_tenant_limit_hits_total` | limit (no tenant label: which tenant is in `GET /v1/limits`, the CLI and `limit` alerts) |
| `taskiem_queued_runs`, `taskiem_queued_tenants`, `taskiem_queued_runs_max_tenant` | reason (`tenant`: held by plan limits; `workflow`: concurrency settings). Totals, the number of tenants with a backlog and the largest single backlog, so series stay few however many tenants there are |
| `taskiem_runs_admitted_total` | — |
| `taskiem_signups_total` | outcome (`created`, `rate_limited`, `blocked_domain`, `invalid`, `exists`, `honeypot`) |
| `taskiem_onboarding_first_run_seconds` | — (a histogram: signup to a self-serve tenant's first successful run, once per tenant; gate G4 is the share at or under 900 s, [onboarding](onboarding.md#measuring-gate-g4)) |
| `taskiem_tenant_key_checks_failed_total` | wrapped_by (`customer`: a tenant's own key, BYOK; `platform`: Taskiem's KMS). Which tenant is in the logs and the tenant's audit log ([BYOK](byok.md#when-the-key-is-unavailable)) |

**Contract drift.** After every successful connector call the worker compares the output with the action's declared output schema (`engine/drift`). A departure (a field of another type, a value outside an enum, a required field missing) is recorded per tenant (`connector_drift`; tenants see it under Connections), counted in `taskiem_connector_drift_total`, and logged at warning level the first time. The step still completes. Alert on any increase for built-in connectors: it means a provider changed its API and the connector needs updating.

Traces span API requests, ingest, and worker steps, with `tenant_id`, `run_id`, and `step_id` attributes.

## Plan limits

Every tenant has plan limits (spec 8.3, 16): the platform defaults below, changed for all tenants with `TASKIEM_DEFAULT_<LIMIT>` and for one tenant with `taskiem tenants limits`. Zero means no limit. Tenants see theirs, with their usage and the limits they reached lately, at `GET /v1/limits` (any member) and in Settings; nothing in the API changes them, and the application's database role cannot write `tenant_limits` (the CLI goes through an audited function). A change reaches running processes within a minute.

| Limit | Default | What happens at the limit |
| --- | --- | --- |
| `ingest_rate`, `ingest_burst` | 20/s, 100 | Soft limit. Deliveries above it are still verified, deduplicated, recorded and answered 202 (`"queued": true`); the runs they start wait as `queued` and the scheduler admits them, oldest first, at this rate. While a tenant has runs waiting, its new runs (also manual and scheduled ones) queue behind them. Signals to waiting runs are delivered at once |
| `ingest_ceiling`, `ingest_ceiling_burst` | 50/s, 200 | Hard limit: 429 `rate_limited` with `Retry-After` |
| `max_running_runs` | 0 | Runs in status running at once, including those waiting on a signal, approval or timer. More are accepted and queued, and admitted as runs end |
| `max_queued_runs` | 10,000 | The backlog. A start that would queue beyond it gets 429 `backlog_full` with `Retry-After` |
| `runs_per_day`, `runs_per_month` | 0 | Run quotas (UTC), counting every accepted run. Beyond them starts get 429 `quota_exceeded` with `Retry-After` until the period ends (webhooks, connector events and `POST /v1/workflows/{id}/runs` alike); a schedule fire is skipped, logged and counted, and the schedule moves on. Off by default: spec 16 prices plans flat |
| `max_workflows` | 0 | Creating another workflow (API or Git sync) gets 429 `limit_exceeded` |
| `max_steps_per_run` | 100,000 | Steps one run may schedule (foreach items and retries count). A run going over fails (`RunFailed`, kind `limit`) in the transaction that scheduled the extra steps, so none of them runs |
| `worker_concurrency` | 32 | A tenant's tasks executing at once per queue (`connector`, `sandbox`). Workers claim round-robin across tenants and skip a tenant at its cap, so one tenant's backlog cannot hold every worker slot. A single-tenant install with more worker slots than this should raise it |
| `max_payload_bytes` | 1 MiB | Larger deliveries get 413 (at most 10 MiB for any tenant) |
| `max_secrets`, `max_connections` | 0 | Adding another named secret or active connection gets 429 `limit_exceeded` |
| `ai_monthly_tokens` | 2,000,000 | Tokens AI building may use per UTC month (every token a model call processes, cache reads included). Beyond it `POST /v1/ai/build` gets 429 `ai_budget_exhausted` and people build on the canvas; runs are never affected ([AI](ai.md#budgets)) |
| `max_retention_days` | 0 | Days an ended run's history is kept at most, whatever its workflow (`settings.retention`) or the governance default asks; then it is archived and purged as usual. Plans set it (spec 16.2) |
| `whatsapp_templates_monthly` | 1,000 | WhatsApp template messages included per UTC month (Meta charges per template). Beyond it templates are still sent and counted as overage for pass-through billing (`GET /v1/limits` `usage.whatsapp_templates_this_month`, `taskiem tenants limits`); approvals, codes, step-up links and alerts are never held back; marketing templates are ([WhatsApp](whatsapp.md#template-costs)). A sub-tenant inherits its partner's |
| `container_minutes_monthly` | 0 (**off**) | Container-step time per UTC month, from the sandbox's start and finish times. Unlike other limits, 0 turns container steps off: they fail with a clear message. Beyond the minutes, container steps fail until next month (`usage.container_seconds_this_month`). A partner's 0 turns them off for its sub-tenants ([container steps](container-steps.md#limits)) |
| `container_concurrency` | 2 | A tenant's container steps running at once, enforced when the `container` queue's tasks are claimed (the lower of a sub-tenant's and its partner's; 0 is the platform default) |

```sh
taskiem tenants limits 0190f0c2-... --set runs_per_month=100000 --set max_running_runs=25
taskiem tenants limits 0190f0c2-... --set runs_per_month=default   # back to the platform default
```

Limits reached are recorded per tenant and day (`tenant_limit_hits`), counted in `taskiem_tenant_limit_hits_total`, and delivered to tenants who add an alert rule of kind `limit` ([alerts](alerts.md)). Ingest limiters are per edge process, so with several edge replicas a tenant's rates apply to each; admission, quotas, the backlog and worker caps are enforced in the database and hold across replicas and restarts. Which tiers exist and their values are a business decision ([needs people](needs-people.md), B1).

**Plans** (billing on, [billing](billing.md)): a tenant's limits are the platform defaults, then its plan's limits (`deploy/plans.yaml`), then its own overrides set here, which still win. `taskiem tenants limits` shows the result. With billing off the plan layer is absent. `taskiem billing grant TENANT PLAN [--until]` puts a tenant on a plan without payment.

**Sub-tenants** (embedding) have no platform defaults of their own: a sub-tenant's limits are its partner's effective limits, lowered by whatever the partner sets for it through the partner API, and never above the partner's. Lowering a partner's limits with `taskiem tenants limits` lowers its sub-tenants' within a minute. A partner's caps across all its sub-tenants (`max_subtenants`, `subtenant_runs_per_day`, `subtenant_runs_per_month`) are set with `taskiem tenants partner`. See [embedding](embedding.md#2-create-sub-tenants).

## Production cloud

High availability Postgres (synchronous standby, failover, point-in-time recovery and restore drills), dedicated worker pools, the read replica and fixed egress addresses are in [cloud](cloud.md).

## Docker Compose

`deploy/docker-compose.yml` runs Postgres, OpenBao (dev mode), and Taskiem with every role; see the comments at its top. OpenBao dev mode keeps keys in memory: restarting it loses every encrypted secret. Use a persistent, unsealed OpenBao in any shared environment.

## Kubernetes

A Helm chart (`deploy/helm/taskiem`) and manifests rendered from it (`deploy/kubernetes/taskiem.yaml`); see [kubernetes.md](kubernetes.md).
