# Operating Taskiem

One binary runs every role (spec 2.1, 15.4). A small install runs `taskiem serve` (all roles in one process); larger ones run each role separately and scale workers.

## Commands

| Command | What it does |
| --- | --- |
| `taskiem migrate` | Applies database migrations as the schema owner. Forward-only. |
| `taskiem bootstrap --tenant NAME --email EMAIL [--name NAME]` | Creates the first tenant, its owner, a Default workspace, and the `dev` and `prod` environments. Password from `TASKIEM_BOOTSTRAP_PASSWORD` (12+ characters). |
| `taskiem serve [--role ROLE]` | Runs `api`, `edge`, `orchestrator`, `scheduler`, `worker`, `public`, or `all` (default; also `TASKIEM_ROLE`). |
| `taskiem tenants limits TENANT_ID [--set KEY=VALUE]...` | Shows a tenant's plan limits, usage and recent limit hits; `--set` changes a limit (`default` returns it to the platform default). Audited as `limits.set`. See [plan limits](#plan-limits). |
| `taskiem tenants partner TENANT_ID [--max-subtenants N] [--subtenant-runs-per-day N] [--subtenant-runs-per-month N] [--disable]` | Makes a tenant a partner, which may create sub-tenants and embed apps through the partner admin API, and sets its partner-wide caps (0: no cap; flags left out keep their value). `--disable` stops it being one: its sub-tenants are kept, unreachable, and their end-user tokens stop. Audited as `partner.enable` and `partner.disable`. See [embedding](embedding.md). |
| `taskiem tenants keys TENANT_ID [status \| rotate [--wait] \| rewrap \| check]` | Shows a tenant's encryption keys: tenant key versions and what wraps them, its own key's health (BYOK), re-wrapping progress and parked steps. `rotate` adds a tenant key version (`--wait` re-wraps at once), `rewrap` re-wraps now, `check` checks the keys and resumes steps parked while they were unavailable. Audited in the tenant's log as `platform_admin`. See [BYOK](byok.md#operators). |
| `taskiem pools [assign POOL \| unassign] [--tenant TENANT_ID \| --plan PLAN] [--force]` | Lists live workers by pool and queue, queue depth by pool, and every routing; `assign` routes a tenant (with its sub-tenants) or a plan's tenants to a dedicated worker pool, refused while no live worker serves it unless `--force`; `unassign` removes the routing. Audited in the tenant's log as `platform_admin`; every change kept in `worker_pool_changes`. See [cloud](cloud.md#dedicated-worker-pools). |
| `taskiem audit verify FILE` | Recomputes every hash and link of an audit export (`GET /v1/audit/export`) without the database. Exits non-zero on a broken chain. |
| `taskiem validate FILE...` | Validates `*.wd.json` definitions and connector manifests. |
| `taskiem messages test --sms NUMBER\|--email ADDRESS [--provider NAME]` | Sends one test message now through the platform's own SMS providers or mail server, to check their configuration. See [messages](messages.md). |
| `taskiem status show\|list\|open\|maintenance\|update\|resolve\|token` | Declares incidents and maintenance on the public status page, recorded with the operator's name; `token NAME` makes an operator token for the admin API. See [reliability](reliability.md#status-page). |
| `taskiem canary setup\|probe\|run` | Sets up the synthetic end-to-end probe in an internal tenant, runs probes now, or probes forever from outside the cluster. See [reliability](reliability.md#synthetic-canary). |
| `taskiem media check TENANT_ID` | A round trip through a tenant's Xenibase storage: makes its buckets, stores and downloads a photo and a private PDF, then deletes them. See [media](media.md#checking-a-tenants-storage). |
| `taskiem healthcheck [ADDR]` | Probes the local API's `/readyz`, or that of `ADDR` (`:9090` for the public role, whose readiness is on its metrics port), for images without a shell. |

## Roles

| Role | Runs | Listens |
| --- | --- | --- |
| `api` | REST API (`/v1`), web app (`TASKIEM_WEB_DIR`), the WhatsApp notifier (approval requests, how chat-started runs ended), the USSD notifier (hand-off backstop, outcome SMS, session purge; [USSD](ussd.md)); with `all`, webhooks too (`/hooks`, `/channels/whatsapp`, `/channels/ussd`) | `TASKIEM_LISTEN` (`:8080`) |
| `edge` | Webhook and connector-event ingest, Git push hooks, the platform WhatsApp number's webhook (`/channels/whatsapp`, [WhatsApp](whatsapp.md)), and USSD aggregators' callbacks (`/channels/ussd`, [USSD](ussd.md)) | `TASKIEM_EDGE_LISTEN` (`:8081`) |
| `public` | The [public host](public-host.md) for storefronts and hosted forms, on its own database login (`taskiem_public`); off in `all` unless `TASKIEM_PUBLIC_ENABLED` | `TASKIEM_PUBLIC_LISTEN` (`:8082`) |
| `mail` | The SMTP server for workflows' [email triggers](email.md): takes mail for `<id>@TASKIEM_MAIL_DOMAIN` and nothing else, on the public host's database login (`taskiem_public`); off in `all` unless `TASKIEM_MAIL_ENABLED` | `TASKIEM_MAIL_LISTEN` (`:2525`); map port 25 to it |
| `orchestrator` | Decides runs left with undecided events (most decisions are inline) | — |
| `scheduler` | Timers, lease recovery, the synthetic canary when configured ([reliability](reliability.md#synthetic-canary)), the orchestrator sweep, cron triggers, admission of queued runs, retention purge, partitions, audit anchoring, alerts, partner webhooks ([embedding](embedding.md#6-partner-webhooks)), and the hourly digest of secret reads into the audit chain ([compliance](compliance.md#secret-use)) | — |
| `worker` | Steps from `TASKIEM_WORKER_QUEUES` (`connector,sandbox,ai`, where `ai` is [AI steps](ai.md#ai-steps): a list without it leaves them queued until their runs time out; `container` for container steps, on their own pool with `TASKIEM_CONTAINER_*`, see [container steps](container-steps.md#operator-setup)), for the tenants of its worker pool (`TASKIEM_WORKER_POOL`, [cloud](cloud.md#dedicated-worker-pools)); drains in-flight steps for up to `TASKIEM_WORKER_DRAIN` (30 s) on shutdown, then releases what it still holds | — |

Every role serves Prometheus metrics (`/metrics`), a liveness check (`/healthz`) and a readiness check (`/readyz`, 503 while shutting down) on `TASKIEM_METRICS_LISTEN` (`:9090`), so roles without the API can be probed too. On SIGTERM readiness flips first and the process keeps serving for `TASKIEM_SHUTDOWN_DELAY` before it stops ([graceful shutdown](reliability.md#graceful-shutdown)). Running several schedulers or orchestrators is safe: every claim uses `SKIP LOCKED` and every firing is deduplicated.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `TASKIEM_DATABASE_URL` | — | Required. For a highly available Postgres, every host with `target_session_attrs=read-write` ([cloud](cloud.md#connection-strings)). |
| `TASKIEM_DATABASE_READ_URL`, `TASKIEM_DATABASE_READ_MAX_LAG` | —, `10s` | A read replica for the run list and dashboard (`api` role), set aside while it lags beyond the bound or fails ([cloud](cloud.md#read-replica)) |
| `TASKIEM_PUBLIC_DATABASE_URL`, `TASKIEM_PUBLIC_DATABASE_ROLE`, `TASKIEM_PUBLIC_DATABASE_POOL` | `TASKIEM_DATABASE_URL`, `taskiem_public`, `5` | The [public host](public-host.md)'s own login (a member of `taskiem_public` only), the role it sets, and its pool's size |
| `TASKIEM_SMS_*`, `TASKIEM_MESSAGE_FROM`, `TASKIEM_MESSAGE_HASH_KEY` | — | The platform's own SMS providers and no-reply address for [messages to people outside an organisation](messages.md); the hash key also turns on [updates on WhatsApp and Telegram](updates.md) for them |
| `TASKIEM_PUBLIC_KEY` | — | 32 bytes, base64 (`openssl rand -base64 32`), the same on the `public` and `api` roles. It signs the [storefront](storefronts.md)'s forms and [hosted forms](forms.md), and encrypts submissions on their way from the public host to the `api` role. Without it, no order or form can arrive, and workflows with forms cannot be deployed |
| `TASKIEM_PUBLIC_DOMAINS` | — | The public host's own domains (the preview domain, the form host), comma-separated: never accepted as an [embed site](embed-sites.md) |
| `TASKIEM_PUBLIC_FORM_HOST` | `forms.` + the first of `TASKIEM_PUBLIC_DOMAINS` | The form host's name, where [hosted forms](public-host.md#hosted-forms) are served (`/f/…`, `/r/…`, `/q/…`, `/s/…`) |
| `TASKIEM_PUBLIC_CNAME` | `pages.` + the first of `TASKIEM_PUBLIC_DOMAINS` | Where businesses point the subdomains they bring as a storefront's [own domain](storefronts.md#your-own-domain) (a CNAME); it must resolve to the public host |
| `TASKIEM_PUBLIC_ADDRESSES` | — | The public host's IPv4 and IPv6 addresses, comma-separated, for businesses' registrable domains' A and AAAA records. Unset, owners are told to use their DNS provider's ALIAS or CNAME flattening |
| `TASKIEM_CLAMD_ADDR` | — | host:port of a ClamAV daemon (clamd) the operator runs, with no network of its own, on the `api` role. Every file sent in a [form](forms.md#files) is scanned there before the submission is accepted; unset, forms that ask for files cannot be deployed |
| `TASKIEM_MAIL_DOMAIN` | — | The mail domain for [email triggers](email.md) (`in.example.com`): workflows' addresses are `<id>@` it. Its MX record names the `mail` role's host. Set on the `mail` and `api` roles (the api role shows addresses and drains received mail); unset, email triggers cannot be published |
| `TASKIEM_MAIL_ENABLED` | `false` | Runs the `mail` role inside `--role all` too, with the public host's login (`TASKIEM_PUBLIC_DATABASE_URL`, or the platform's, which must then be a member of `taskiem_public`). `--role mail` always runs it |
| `TASKIEM_MAIL_LISTEN` | `:2525` | The `mail` role's SMTP listener; publish it as port 25 |
| `TASKIEM_MAIL_HOSTNAME` | `mx.` + the mail domain | The name the `mail` role greets with, which the MX record points at |
| `TASKIEM_MAIL_TLS_CERT`, `TASKIEM_MAIL_TLS_KEY` | — | PEM files of the certificate offered with STARTTLS; both or neither. Unset, a self-signed one, which sending servers accept |
| `TASKIEM_PUBLIC_LISTEN`, `TASKIEM_PUBLIC_ENABLED` | `:8082`, `false` | The public host's listener, and whether `--role all` runs it |
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
| `TASKIEM_ALERT_INTERVAL` | `30s` | How often the scheduler evaluates alert rules and tells people how runs ended ([notifications](alerts.md#notifications-with-nothing-set-up)); 1s to 1h |
| `TASKIEM_WASM_CACHE` | the user cache directory | Where workers keep compiled WebAssembly (the Python interpreter takes seconds to compile the first time). Point it at a writable directory that survives restarts |
| `TASKIEM_LOGIN_BURST` | 10 | Sign-in attempts one address may make at once, then one every six seconds |
| `TASKIEM_WEB_DIR` | — | Built web app to serve (`/web` in the image). |
| `TASKIEM_SECURE_COOKIES` | `true` | Set `false` only for plain-HTTP local use. |
| `TASKIEM_PUBLIC_URL` | — | Where people reach the web app (`https://…`, or `http://localhost:…`). Turns on passkeys, which are bound to it; password-reset links point at it (none are sent without it) |
| `TASKIEM_HOOKS_URL` | `TASKIEM_PUBLIC_URL` + `/hooks` | Where providers reach the edge's `/hooks` (`https://hooks.example.com/hooks`). Taskiem points the subscriptions it registers itself at it (Xenibase webhooks, [decision 0021](decisions/0021-remote-trigger-registration.md)); without it none is created and each shows why. Set it on the api and scheduler roles |
| `TASKIEM_OAUTH_XENIBASE_CLIENT_ID`, `TASKIEM_OAUTH_XENIBASE_CLIENT_SECRET` | — | Taskiem's registration as an OAuth app with the platform's Xenibase (`TASKIEM_XENIBASE_URL`), so people connect a project with one click instead of pasting a token ([Connect with one click](integrations/xenibase.md#connect-with-one-click), [decision 0034](decisions/0034-oauth-connections.md)). A Xenibase platform admin registers the app (`POST /api/v1/admin/oauth/clients`, confidential, scopes `read` and `write`) with the redirect URI `<TASKIEM_PUBLIC_URL>/v1/connections/oauth/callback`, exactly, and the pair is the `client_id` (`xbapp_…`) and the `client_secret` (`xbc_…`) it answers; the secret is kept in the process, never in a tenant's vault, never logged. Both or neither (one alone refuses to start); without them Xenibase connections are made by pasting a token and the API answers 503 `oauth_unavailable` to a start. The same pattern, `TASKIEM_OAUTH_<CONNECTOR>_CLIENT_ID` and `_CLIENT_SECRET`, serves any connector that declares `auth.oauth2`. Set on the `api` role; `TASKIEM_PUBLIC_URL` is required with them |
| `TASKIEM_OAUTH_<CLIENT>_CLIENT_ID`, `_CLIENT_SECRET`, `_WEBHOOK_SECRET`, `_VERIFY_TOKEN` | — | Taskiem's platform app with a provider ([decision 0051](decisions/0051-platform-apps.md); connector/v1 rule 18), named by the connector's `auth.oauth2.client` (its id by default; one app may serve several connectors). The client id and secret let businesses connect with one click, as for Xenibase above. The webhook secret is what the provider signs the app's deliveries with (Slack's signing secret, Meta's app secret, Intuit's verifier token); with it, `/hooks/apps/<connector>/<trigger>` takes them and routes each to the connections holding its account, and without it that URL answers 404. The verify token answers the provider's endpoint check where it has one (Meta's). Process secrets: set them on the `api` and `edge` roles from the Secret, never in chart values; they never reach a tenant's vault |
| `TASKIEM_XENIBASE_URL`, `TASKIEM_XENIBASE_TOKEN`, `TASKIEM_XENIBASE_ORG` | — | The platform's Xenibase for [data tables](data-tables.md#for-operators): the server (also the default for Xenibase connections that name none, [connection](integrations/xenibase.md#connection); Xenibase has not published its cloud address, so there is no default), an unrestricted token with the `read`, `write` and `admin` scopes (kept in the process, never in a tenant's vault, never logged; rotate it yearly), and the ID of the organisation projects are made in (not its name: `GET /api/v1/me` with the token answers it). Without all three, creating a table answers 503 `tables_unavailable`. Set them on the `api` and `scheduler` roles |
| `TASKIEM_XENIBASE_REGION` | Xenibase's home region | The region data table projects are made in |
| `TASKIEM_PASSKEY_RP_ID` | the URL's host | The passkey domain, if passkeys should work across subdomains (a parent of the URL's host) |
| `TASKIEM_REQUIRE_ADMIN_PASSKEYS` | `true` with a public URL | Hold administrators to passkeys ([governance](governance.md#passkeys)) |
| `TASKIEM_HSTS` | `max-age=31536000` with an https public URL | The `Strict-Transport-Security` header on the platform's own host (never on partners' custom domains): a value starting `max-age=` (add `includeSubDomains` only if every subdomain is HTTPS), or `off` |
| `TASKIEM_TENANT_CODE_CONCURRENCY` | the CPUs, at least 2 | How much tenant code (flow compile, code checks, catalogue and connector checks) one API process runs at once; more waits up to 5 s, then gets 503 `code_checks_busy` |
| `TASKIEM_TENANT_CODE_PER_TENANT` | half of the above, at least 1 | How much of it one tenant holds at once, across every API replica; more gets 429 `tenant_code_busy`. Refusals: `taskiem_tenant_code_refused_total{reason}` |
| `TASKIEM_TENANT_CODE_SHARE` | `cluster` | `cluster`: the per-tenant share holds across API replicas (two-minute leases in `tenant_code_leases`, renewed while the work runs). `replica`: each replica grants it again, as before |
| `TASKIEM_EGRESS_PROXY`, `TASKIEM_EGRESS_PROXY_USER`, `TASKIEM_EGRESS_PROXY_PASSWORD` | — | An explicit `CONNECT` proxy for tenant traffic (`http://host:port` or `https://host:port`, port required; credentials from the Secret). The egress guard still vets every destination and connects to the vetted IP, never a name. A badly set value stops the process. Set on every role ([cloud](cloud.md#an-explicit-egress-proxy), decision 0028) |
| `TASKIEM_TRUST_PROXY` | `false` | Take the client address from the last `X-Forwarded-For` hop (behind a load balancer only). |
| `TASKIEM_ALLOW_SIGNUP` | `false` | Self-serve signup ([onboarding](onboarding.md)). Needs email (`TASKIEM_SMTP_URL`, `TASKIEM_ALERT_FROM`, a public URL) for confirmation links |
| `TASKIEM_SIGNUP_PER_ADDRESS` | `5` | Signups per client address a day, across replicas; negative: no limit |
| `TASKIEM_SIGNUP_BLOCKED_DOMAINS` | — | Email domains that may not sign up, besides the built-in throwaway services (comma-separated) |
| `TASKIEM_DOCS_URL` | — | The public docs site the web app's help panels link to: the site's `/docs` (`https://example.ng/docs`) |
| `TASKIEM_ISWALLET_URL`, `TASKIEM_ISWALLET_TEST_URL`, `TASKIEM_PAYSTACK_URL`, `TASKIEM_TERMII_URL`, `TASKIEM_DOJAH_URL`, `TASKIEM_FLUTTERWAVE_URL`, `TASKIEM_GETANCHOR_URL`, `TASKIEM_LENCO_URL`, `TASKIEM_BREET_URL`, `TASKIEM_PAYROLLA_URL`, `TASKIEM_PAYROLLA_TEST_URL` | provider defaults | Point connectors at another environment; the egress allow-list follows. Tests only: an IP-literal loopback URL (`http://127.0.0.1:PORT`) lets that connector, and only it, reach that port on loopback (a warning is logged at start). iswallet and Payrolla connections are **live or test**, chosen when they are made ([connector/v1](contracts/connector-v1.md) rule 17): `TASKIEM_ISWALLET_URL` replaces iswallet's production address (`https://ispend.name.ng/iwallet`) and `TASKIEM_ISWALLET_TEST_URL` its sandbox's (`https://synledger.name.ng/iwallet`). `TASKIEM_PAYROLLA_URL` replaces Payrolla's production address (`https://ispend.name.ng/payrolla/api/v1`) and `TASKIEM_PAYROLLA_TEST_URL` its sandbox's (`https://synledger.name.ng/payrolla/api/v1`). Connections made before modes go to `TASKIEM_ISWALLET_URL` or `TASKIEM_PAYROLLA_URL` when set, else the sandbox, as they always did. Empty placeholders for all four are in `deploy/docker-compose.yml` and the Helm chart's `config`; `taskiem-vps setup` asks for the two live ones. None needs setting: the defaults are the providers' own. Anchor and Lenco connections choose their provider's sandbox themselves (`environment: sandbox`), Breet's with the same field, Flutterwave's by the key. |
| `TASKIEM_WHATSAPP_PHONE_NUMBER_ID` | — | Turns on the platform WhatsApp number ([WhatsApp](whatsapp.md)). With it, `TASKIEM_WHATSAPP_ACCESS_TOKEN` (system user token), `TASKIEM_WHATSAPP_APP_SECRET` (verifies webhook signatures), `TASKIEM_WHATSAPP_VERIFY_TOKEN` (subscription handshake) and `TASKIEM_WHATSAPP_TOKEN_KEY` (32 bytes, base64: signs approval decision tokens; the same on every `api` and `edge` pod) are required. Put them in the Secret, never in chart values. Set them on the `api`, `edge` and `scheduler` roles |
| `TASKIEM_TELEGRAM_BOT_TOKEN` | — | Turns on the platform Telegram bot ([Telegram](telegram.md)). With it, `TASKIEM_TELEGRAM_BOT_USERNAME` (without `@`) and `TASKIEM_TELEGRAM_WEBHOOK_SECRET` (1 to 256 of `A-Z a-z 0-9 _ -`); without WhatsApp, also `TASKIEM_CHAT_TOKEN_KEY` (32 bytes, base64), which signs approval buttons. The `api` role registers the webhook itself when `TASKIEM_PUBLIC_URL` is https. Secrets: set them on every role from the Secret |
| `TASKIEM_OAUTH_SLACK_WEBHOOK_SECRET` (with `_CLIENT_ID`, `_CLIENT_SECRET`) | — | Taskiem's Slack app ([Slack](integrations/slack.md#add-to-slack-one-click)): Add to Slack, its events at `/hooks/apps/slack@1/events`, and approvals in Slack, whose clicks come to `/channels/slack/interactions` on the `edge` role (and `api` when it runs every role). Without WhatsApp, `TASKIEM_CHAT_TOKEN_KEY` (32 bytes, base64) is then required: it signs the approval buttons. Secrets: set them from the Secret |
| `TASKIEM_OAUTH_QUICKBOOKS_CLIENT_ID`, `_CLIENT_SECRET`, `_WEBHOOK_SECRET`; `TASKIEM_OAUTH_QUICKBOOKS_SANDBOX_*` | — | Taskiem's Intuit app ([QuickBooks](integrations/quickbooks.md#connecting-one-click)): its production keys and verifier token connect real companies, its development keys (`_SANDBOX_`) Intuit's sandbox companies; each mode is offered once its keys are set. Both apps' change notifications come to `/hooks/apps/quickbooks@1/changes` on the `edge` role. Secrets: set them from the Secret |
| `TASKIEM_OAUTH_META_CLIENT_ID`, `_CLIENT_SECRET`, `_WEBHOOK_SECRET`, `_VERIFY_TOKEN` | — | Taskiem's Meta app ([Messenger and Instagram](integrations/messenger-and-instagram.md#connecting-one-click)): its app id and secret; the app secret again as the webhook secret (Meta signs with it); and the verify token its webhook settings carry. Its Page and Instagram webhooks come to `/hooks/apps/messenger@1/messages` and `/hooks/apps/instagram@1/messages` on the `edge` role. Secrets: set them from the Secret |
| `TASKIEM_META_GRAPH_URL` | `https://graph.facebook.com/v26.0` | Tests only: a fake Graph API for Messenger and Instagram |
| `TASKIEM_OAUTH_ZOHO_CLIENT_ID`, `_CLIENT_SECRET` | — | Taskiem's Zoho client, a server-based application with Multi-DC on ([Zoho Books](integrations/zoho-books.md#connecting-one-click)), shared by every Zoho connector. Secrets: set them from the Secret |
| `TASKIEM_ZOHO_BOOKS_URL`, `TASKIEM_ZOHO_ACCOUNTS_URL` | each account's own | Tests and proxies only: Zoho Books' API and Zoho's accounts server for every connection, instead of each account's data centre |
| `TASKIEM_QUICKBOOKS_URL`, `TASKIEM_QUICKBOOKS_TEST_URL`, `TASKIEM_QUICKBOOKS_TOKEN_URL` | Intuit's | Tests and proxies only: QuickBooks' production and sandbox API hosts and Intuit's token endpoint |
| `TASKIEM_TELEGRAM_API_URL` | `https://api.telegram.org` | Tests only: a fake Bot API (a loopback address here is allowed through the egress guard for the bot only) |
| `TASKIEM_WHATSAPP_DISPLAY_NUMBER`, `TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE`, `TASKIEM_WHATSAPP_DEFAULT_COUNTRY` | —, `en`, `234` | The number as shown on the Account page, and where [update links](updates.md) for people outside a business point (+234…); the language the templates were approved in; the calling code for numbers typed with a leading 0 |
| `TASKIEM_WHATSAPP_FLOWS_PRIVATE_KEY` | — | Turns on WhatsApp Flows (input forms and the approval PIN, [WhatsApp](whatsapp.md#flows)): the PEM of an unencrypted RSA private key of at least 2048 bits (PKCS#1 or PKCS#8; `\n` may stand for new lines). Generate it with `openssl genrsa -out flows.pem 2048` and upload the public half (`openssl rsa -in flows.pem -pubout`) to the shared number with `POST /{phone-number-id}/whatsapp_business_encryption`; Taskiem uploads it to tenants' own numbers when they connect. Secret only; never logged. Set it on the `edge` and `api` roles (with `all`, once). Without it inputs are asked in chat and step-up uses the web link |
| `TASKIEM_WHATSAPP_GRAPH_URL` | `https://graph.facebook.com/v25.0` | Tests only: a fake Graph API (a loopback address here is allowed through the egress guard for the platform number only) |
| `TASKIEM_DEFAULT_<LIMIT>` | see [plan limits](#plan-limits) | Platform default for one plan limit, for tenants without their own (for example `TASKIEM_DEFAULT_RUNS_PER_MONTH=100000`). Set the same values on every role. |
| `ANTHROPIC_API_KEY` | — | Turns AI building on with Claude ([AI](ai.md#configuration)). Keep it in a secret; it is read from the environment only. |
| `TASKIEM_AI_PROVIDER`, `TASKIEM_AI_MODEL`, `TASKIEM_AI_BASE_URL`, `TASKIEM_AI_API_KEY`, `TASKIEM_AI_EFFORT`, `TASKIEM_AI_MAX_TOKENS`, `TASKIEM_AI_FALLBACKS` | Claude `claude-opus-5-5` when `ANTHROPIC_API_KEY` is set; otherwise off | The model provider for AI building: `anthropic`, `selfhosted` (an OpenAI-compatible endpoint at `TASKIEM_AI_BASE_URL`) or `off`; the model; thinking effort (`high`); max tokens per answer (32000); server-side refusal fallbacks (on). Set on the `api` role. See [AI](ai.md#configuration). |
| `TASKIEM_AI_STEP_MODELS`, `TASKIEM_AI_RETENTION` | —; — | For [AI steps](ai.md#ai-steps): other models a workflow's AI steps may name besides the default (comma-separated), and what the provider keeps of what it is sent, shown to every business's owners under Secrets & settings, AI steps (state it; without it they read "not stated"). AI steps run on the `ai` worker queue with the same `ANTHROPIC_API_KEY` and `TASKIEM_AI_*` settings, so set these on the `api` role and on `worker` roles serving `ai`. |
| `TASKIEM_LANGUAGES` | — (English only) | Languages beyond English turned on for every tenant on WhatsApp, USSD and SMS: any of `pcm`, `yo`, `ha`, `ig`. Unreviewed drafts until native speakers review them. Set on the `api` and `edge` roles. See [Languages](languages.md). |
| `TASKIEM_TRANSCRIBE_PROVIDER`, `TASKIEM_TRANSCRIBE_API_KEY`, `TASKIEM_TRANSCRIBE_BASE_URL`, `TASKIEM_TRANSCRIBE_MODEL`, `TASKIEM_TRANSCRIBE_MAX_SECONDS`, `TASKIEM_TRANSCRIBE_MAX_BYTES`, `TASKIEM_TRANSCRIBE_RETENTION` | off; —; `https://api.openai.com`; `whisper-1`; 60; 1 MiB; `168h` | WhatsApp voice-note transcription (a beta): `openai` (the OpenAI-compatible API, or a self-hosted server speaking it); the key (from the Secret); caps; how long sealed transcripts are kept. Each tenant also turns voice notes on. Set on the `api` and `edge` roles. See [Languages](languages.md#voice-notes). |
| `TASKIEM_BILLING` | `off` | `on` turns plans, subscriptions and naira payments on ([billing](billing.md)); off, every tenant is on the internal `self_hosted` plan (the defaults below, every feature). With it: `TASKIEM_BILLING_PLANS` (`deploy/plans.yaml`), `TASKIEM_BILLING_PROVIDER` (`paystack`), `TASKIEM_BILLING_PAYSTACK_SECRET_KEY`, `TASKIEM_BILLING_FLUTTERWAVE_SECRET_KEY` and `TASKIEM_BILLING_FLUTTERWAVE_WEBHOOK_HASH` (the platform's merchant accounts, from the Secret). Set on the `api` and `scheduler` roles |
| `TASKIEM_BYOK_CACHE_TTL` | `5m` | How long a tenant key that depends on a tenant's own key (BYOK) stays unwrapped in memory: the bound on how long revoking it takes. Set on every role ([BYOK](byok.md#operators)) |
| `TASKIEM_KEY_CHECK_INTERVAL`, `TASKIEM_KEY_DESTROY_AFTER` | `1m`, `24h` | How often the scheduler's key job checks tenants' own keys, re-wraps after rotations and resumes parked steps; how long a retired, unused tenant key version is kept before its material is destroyed |
| `TASKIEM_BYOK_ALLOW_PRIVATE` | off | Let tenants' own KMS addresses resolve to private ranges. Dedicated single-tenant deployments only |
| `TASKIEM_SHUTDOWN_DELAY`, `TASKIEM_WORKER_DRAIN` | `0`, `30s` | On SIGTERM: how long to keep serving after readiness flips (set it above your load balancer's deregistration time; the chart sets 10s), and how long workers let in-flight steps finish before releasing them ([graceful shutdown](reliability.md#graceful-shutdown)) |
| `TASKIEM_STATUS_PAGE`, `TASKIEM_STATUS_CANARY`, `TASKIEM_STATUS_TOKENS` | on, on, — | The public status page on the `api` role; whether it shows the canary's results; operators' admin tokens as `name:sha256hex,...` ([status page](reliability.md#status-page)) |
| `TASKIEM_OPS_CONSOLE`, `TASKIEM_OPS_SESSION_TTL`, `TASKIEM_OPS_OIDC_ISSUER`, `TASKIEM_OPS_OIDC_CLIENT_ID`, `TASKIEM_OPS_OIDC_CLIENT_SECRET`, `TASKIEM_OPS_OIDC_NAME` | on, `1h`, —, —, —, `single sign-on` | The operator console's API on the `api` role, its session length (5m to 8h), and optional single sign-on for operators from one issuer ([operator console](operator-console.md#configuration)) |
| `TASKIEM_CANARY_HOOK_URL`, `TASKIEM_CANARY_SECRET`, `TASKIEM_CANARY_API_URL`, `TASKIEM_CANARY_API_KEY`, `TASKIEM_CANARY_INTERVAL`, `TASKIEM_CANARY_TIMEOUT` | —, —, `TASKIEM_PUBLIC_URL`, —, `1m`, `1m` | The synthetic canary, run by the `scheduler` role when the hook URL is set; `taskiem canary setup` prints the first four. Keep the secret and key in the Secret ([canary](reliability.md#synthetic-canary)) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | Enables OpenTelemetry trace export (OTLP/HTTP); standard `OTEL_*` variables apply. |

## Unexpected errors

An error the API did not expect answers 500 with `{"error": "Something went wrong on our side. Reference E-7K2Q9M", "code": "internal", "reference": "E-7K2Q9M"}`. The web app shows the reference, and the server logs the error with the same `reference` attribute, so the line someone quotes can be found: `docker compose logs taskiem | grep E-7K2Q9M` (`sudo taskiem-vps logs taskiem` on the VM kit).

## Database roles

Migrations create `taskiem_app` (the application, under forced row-level security) and `taskiem_dispatch` (owner of the cross-tenant claim and authentication functions). Neither can log in. In production create a login user for the engine and grant it the app role only:

```sql
CREATE ROLE taskiem LOGIN PASSWORD '...';
GRANT taskiem_app TO taskiem;
```

Run `taskiem migrate` as the schema owner, and `taskiem serve` as `taskiem`.

## Webhooks

- Webhook triggers: `POST /hooks/{tenant}/{path}?env=prod` (or the `GET`, `PUT`, `PATCH` and `DELETE` a trigger's `methods` take; `OPTIONS` answers browsers' CORS preflights for triggers with `cors`). `hmac` expects `X-Taskiem-Signature: sha256=<hex HMAC-SHA256 of the body>`; `bearer` expects `Authorization: Bearer <token>`; `basic` expects `Authorization: Basic` with the secret's `user:password`; `jwt` expects `Authorization: Bearer <JWT>` signed with the secret (a shared key, or a PEM public key) and carrying `exp` ([wd/v1](contracts/wd-v1.md#webhook-triggers)). The key is the environment secret `webhook_<WD id>`; workflows cannot read it (nor `git_credentials` or `git_webhook_secret`): an expression or code step naming one fails. Test calls from the editor's "Listen for a test call" arrive at `/hooks/{tenant}/_listen/{id}/{token}` and start nothing; keep that path out of ingress logs, since its token is the address's only key.
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
| `taskiem_step_dispatch_delay_seconds` | queue (a histogram: ready to claimed, database clock; SLO `step_dispatch`) |
| `taskiem_run_start_seconds` | — (a histogram: trigger accepted to first worker step claimed; SLO `run_start`) |
| `taskiem_scheduler_lateness_seconds` | kind (`timer`, `schedule`; SLO `scheduler_timeliness`) |
| `taskiem_scheduler_last_tick_timestamp_seconds` | mode (`scheduler`, `sweep`) |
| `taskiem_draining`, `taskiem_leases_released_total` | — (shutdown: 1 while draining; leases handed back instead of left to expire) |
| `taskiem_canary_probes_total`, `taskiem_canary_duration_seconds`, `taskiem_canary_last_success_timestamp_seconds` | result (`ok`, `accept`, `complete`, `output`); phase (`accept`, `complete`) |
| `taskiem_tenant_key_checks_failed_total` | wrapped_by (`customer`: a tenant's own key, BYOK; `platform`: Taskiem's KMS). Which tenant is in the logs and the tenant's audit log ([BYOK](byok.md#when-the-key-is-unavailable)) |

**SLOs.** The recording rules, burn-rate alerts and dashboard built on these metrics are in `deploy/prometheus` and `deploy/grafana` (or the chart's `prometheusRule` and `grafanaDashboard`), generated from `engine/slo`; definitions, alerts and runbooks are in [reliability](reliability.md).

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
| `max_data_tables` | 20 | Creating another [data table](data-tables.md#limits) gets 429 `limit_exceeded`. Plans: starter 10, growth 50, business and above no limit |
| `max_data_table_columns` | 50 | Columns one data table may have: a wider table is refused and adding a column past it gets 429 `limit_exceeded` |
| `storefront_items` | 0 (no limit) | Products and services a storefront shows: a publish with more is refused, and the copied catalogue stops at it ([moderation](moderation.md#plans)). Plans: shop 50, shop plus 500, starter and above 2,000 |
| `storefront_orders_monthly`, `storefront_bookings_monthly` | 0 (no limit) | Storefront orders and bookings per UTC month: beyond them the forms say orders are paused. Plans: shop 300, shop plus 1,500 |
| `sms_monthly` | 0 (no limit) | SMS to customers per UTC month: beyond it only verification codes are sent. Plans: shop 100, shop plus 500, starter and above 1,000 |
| `embed_sites` | 10 | Websites that may embed the storefront and forms ([embed sites](embed-sites.md)). Plans: shop 1, shop plus 3 |

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

For staging or a first production environment on one VM, use `deploy/vm` instead: one command installs it, and the server upgrades, rolls back, backs up and watches itself (persistent auto-unsealed OpenBao, HTTPS through Caddy, an external Postgres, container steps off) ([one VM](vm.md), [decision 0032](decisions/0032-deployment-target.md)).

## Kubernetes

A Helm chart (`deploy/helm/taskiem`) and manifests rendered from it (`deploy/kubernetes/taskiem.yaml`); see [kubernetes.md](kubernetes.md).
