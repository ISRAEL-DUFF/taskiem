# Embedding Taskiem (partner guide)

A **partner** puts Taskiem inside its own product so that its customers automate their work without signing up to Taskiem (spec 13.1, 13.4). Each of the partner's customers is a **sub-tenant**: a tenant of its own, with its own workflows, runs, audit chain and keys, isolated from the partner's other customers and from the partner's own staff. The people who use it are **end users**: the partner's users, known to Taskiem only by the id the partner gives them, acting through short-lived tokens the partner's server mints.

This page covers milestone C1: sub-tenants, the partner admin API, embed apps, end-user tokens, headless use of the API, and partner webhooks. The embedded builder web component and iframe, theming, custom domains and white-label (C2), and the partner's API as a pre-authenticated connector (C3) build on it; see [what comes next](#what-comes-next). The design is in [decision 0015](decisions/0015-embedding-tenancy.md).

## Overview

```
 partner's server ──partner API key──▶ /v1/partner/...          (create sub-tenants, apps, mint tokens, observe)
        │ mints
        ▼
 end-user token ──▶ partner's page (browser) ──CORS──▶ /v1/embed/{app}/...   (build and run, inside one sub-tenant)
                    or partner's server (headless) ──▶ /v1/embed/{app}/...
 Taskiem ──signed webhooks──▶ partner's server                  (run outcomes, publishes, usage)
```

## 1. Become a partner

An operator makes a tenant a partner and sets its partner-wide caps (0: no cap):

```sh
taskiem tenants partner 0190f0c2-... --max-subtenants 500 --subtenant-runs-per-day 100000 --subtenant-runs-per-month 2000000
taskiem tenants partner 0190f0c2-... --disable   # stops the partner API and every end-user token; sub-tenants are kept
```

A sub-tenant cannot itself be a partner. Every change is audited (`partner.enable`, `partner.disable`).

The partner's owner then creates an API key for its server with the partner permissions (only owners hold them among the built-in roles; a custom role can carry them too):

```sh
curl -X POST $TASKIEM_URL/v1/api-keys -H "Authorization: Bearer $SESSION" \
  -d '{"name":"partner server","permissions":["partner.read","partner.manage"]}'
```

| Permission | Allows |
| --- | --- |
| `partner.read` | List sub-tenants; read a sub-tenant's workflows, runs (redacted) and usage; list embed apps and webhook deliveries |
| `partner.manage` | Create, suspend and resume sub-tenants and set their limits; register and change embed apps; rotate webhook secrets; mint and revoke end-user tokens; retry deliveries |

The partner API accepts only API keys that reach every environment: not sessions, and not environment-limited keys.

## 2. Create sub-tenants

```http
POST /v1/partner/sub-tenants
{"name": "Customer 1042", "region": "ng-lagos", "limits": {"max_workflows": 20, "runs_per_day": 500}}
→ 201 {"id": "...", "name": "Customer 1042", "status": "active", "limits": {...effective limits...}}
```

A sub-tenant gets a default workspace and the `dev` and `prod` environments. It copies the partner's plan and, unless given, its region.

**Limits.** A sub-tenant's limits default to the partner's own effective limits ([plan limits](operations.md#plan-limits)), and can only be lower: a value above the partner's, or `0` (no limit) where the partner has one, is refused with 400. They are checked when set and capped again whenever they are read, so lowering the partner's limits lowers every sub-tenant's at once. `null` returns a limit to the partner's value. On top of each sub-tenant's own quotas, the partner-wide caps (`subtenant_runs_per_day`, `subtenant_runs_per_month`) count the runs of all the partner's sub-tenants together, and `max_subtenants` caps how many it may create (429 `limit_exceeded`, `"limit": "max_subtenants"`).

| Endpoint | Permission | Does |
| --- | --- | --- |
| `GET /v1/partner` | read | The partner's caps, its sub-tenants' total usage today and this month, and the ceiling of their limits |
| `GET /v1/partner/sub-tenants` | read | Sub-tenants: id, name, region, status |
| `POST /v1/partner/sub-tenants` | manage | Create one (above) |
| `PUT /v1/partner/sub-tenants/{sub}/limits` | manage | Change its limits, within the partner's: `{"runs_per_day": 100, "max_workflows": null}` |
| `POST /v1/partner/sub-tenants/{sub}/suspend`, `/resume` | manage | Suspend or resume it |
| `GET /v1/partner/sub-tenants/{sub}/workflows` | read | Its workflows: names and versions, not definitions |
| `GET /v1/partner/sub-tenants/{sub}/runs` | read | Its runs, redacted to their outcome: workflow, version, environment, status, who started them, timing, and for a failure the error's kind. Never inputs, outputs, step data or error messages. Filters: `status`, `before`, `limit` |
| `GET /v1/partner/sub-tenants/{sub}/usage` | read | Its effective limits, usage and recent limit hits |

**Suspending** a sub-tenant stops at once every end-user token and session in it, revokes its outstanding tokens (resuming does not revive them: mint new ones), and refuses new tokens with 409. The partner can still read it, and resume it. Its triggers (schedules and webhooks) keep firing in C1; holding them is a follow-up.

## 3. Register an embed app

An embed app is one place the partner embeds Taskiem: a web app, an admin console, or a server (headless).

```http
POST /v1/partner/embed-apps
{
  "name": "payroll web",
  "allowed_origins": ["https://app.payrolla.com"],
  "branding": {"colours": {"primary": "#0a7d5a", "background": "#ffffff"}, "font_family": "Inter, sans-serif",
               "logo_url": "https://cdn.payrolla.com/logo.svg", "radius": "8px", "mode": "auto"},
  "allowed_connectors": ["paystack", "termii", "http"],
  "allowed_templates": ["salary-reminder"],
  "end_user_permissions": ["workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"],
  "headless": false,
  "webhook_url": "https://api.payrolla.com/taskiem/webhooks",
  "webhook_events": ["run.completed", "run.failed", "workflow.published", "usage.threshold"]
}
→ 201 {"id": "...", "webhook_secret": "whsec_...", "app": {...}}
```

| Field | Rules |
| --- | --- |
| `allowed_origins` | Exact origins, `https://host[:port]` (http only for `localhost`), no paths or wildcards; at most 20. Required unless `headless` |
| `branding` | Theming tokens for the embedded builder (C2): `colours` (`primary`, `on_primary`, `background`, `surface`, `text`, `muted`, `border`, `accent`, `danger`, `success`; `#rgb`, `#rrggbb` or `#rrggbbaa`), `font_family` (letters, digits, spaces, commas, hyphens), `font_url` and `logo_url` (https), `radius` (`8px`), `mode` (`light`, `dark`, `auto`). Anything else is refused, so a token can never carry markup or a style injection |
| `allowed_connectors` | Connector ids from the platform catalogue (`GET /v1/connectors`), plus `http`, `code` and `ai` to allow those step types, which reach the network or run code without a connector's declared actions. Empty: only control-flow and `transform` steps |
| `allowed_templates` | Template ids end users may start from (the template library arrives with B4; C1 checks the id) |
| `end_user_permissions` | What the app's end users may be given, a subset of `workflow.read`, `workflow.edit`, `workflow.publish`, `run.read`, `run.start`, `run.cancel`. Default: `workflow.read`, `workflow.edit`, `run.read`, `run.start` |
| `headless` | Tokens may be used without an `Origin` (from the partner's servers) |
| `webhook_url`, `webhook_events` | Where [partner webhooks](#6-partner-webhooks) go (https; http only for localhost) and which events |
| `status` | `active` or `disabled`; disabling stops every token of the app at once |

`PUT /v1/partner/embed-apps/{app}` replaces the settings. Narrowing an app's connectors or end-user permissions applies at once to tokens already minted: they are checked against the app on every request. The webhook secret is shown once, when the app first gets a webhook URL; `POST /v1/partner/embed-apps/{app}/webhook-secret` rotates it (shown once). `GET /v1/partner/embed-apps` and `GET /v1/partner/embed-apps/{app}` show the rest, never the secret.

## 4. Mint end-user tokens on your server

When one of your users opens the embedded builder, your **server** asks for a token and hands it to your front end. Never mint from the browser: the partner key must not leave your servers.

```http
POST /v1/partner/embed-apps/{app}/tokens
{"end_user_id": "user-8812", "sub_tenant": "0190f0c3-...", "permissions": ["workflow.read", "workflow.edit", "run.read", "run.start"],
 "ttl": 900, "origin": "https://app.payrolla.com"}
→ 201 {"token": "tsk_eut_...", "expires_at": "...", "end_user": {"id": "...", "external_id": "user-8812"}, ...}
```

- `end_user_id` is your id for the user (letters, digits and `._:@|-`, up to 128). Taskiem records them as `end_user:<app>/<end_user_id>` in the sub-tenant's audit chain and in run and workflow records. There is no platform account and no login.
- `permissions` must be among the app's `end_user_permissions`.
- `ttl` is in seconds: default 900 (15 minutes), at most 3600. Mint a new token before it expires; tokens cannot be refreshed.
- `origin`, optional, binds the token to one of the app's origins; without it, any of the app's origins may use it.
- The sub-tenant must be yours and active (409 when suspended).

Revoke a user's tokens (all of them, for this app and sub-tenant), for example when they sign out or lose access in your product:

```http
POST /v1/partner/embed-apps/{app}/tokens/revoke
{"sub_tenant": "0190f0c3-...", "end_user_id": "user-8812"}
→ 200 {"revoked": 2}
```

## 5. The embed API (and headless mode)

End users call `/v1/embed/{app}/...` with `Authorization: Bearer <token>`. The token must be for that app (its audience); nothing else in the API accepts it. A browser request must come from one of the app's allowed origins (and the token's bound origin); a request without an `Origin` is accepted only for a `headless` app. CORS answers only those origins, only on these routes, and allows no credentials (tokens are sent as headers, never cookies).

| Route | Permission | Notes |
| --- | --- | --- |
| `GET /me` | — | The end user, sub-tenant, permissions, the app's allowed connectors and templates, branding tokens, expiry |
| `GET /connectors` | — | The connectors the app allows, with their actions and schemas, and the allowed gated step types |
| `GET /workflows`, `GET /workflows/{wf}`, `GET /workflows/{wf}/versions/{v}` | `workflow.read` | As `/v1/workflows` |
| `POST /workflows` | `workflow.edit` | `{"name", "definition", "template"?}`; drafts may be incomplete and come back with `problems` |
| `POST /workflows/{wf}/versions` | `workflow.edit` | Save a version (three-way merge with `parent_digest`, as `/v1`) |
| `PUT /workflows/{wf}/versions/{v}/layout` | `workflow.edit` | Canvas layout |
| `POST /validate` | `workflow.edit` | Validate a definition |
| `POST /workflows/{wf}/versions/{v}/publish` | `workflow.publish` | Publish |
| `POST /workflows/{wf}/runs` | `run.start` | Start a run (`Idempotency-Key` supported) |
| `GET /runs`, `GET /runs/{run}` | `run.read` | Runs and their history |
| `GET /runs/{run}/stream` | `run.read` | Server-sent events of a run's history (use `fetch` with the header; `EventSource` cannot send it) |
| `POST /runs/{run}/cancel` | `run.cancel` | Cancel |

**Allowed connectors and templates.** Every definition an end user saves, validates or publishes is checked against the app: a connector, or an `http`, `code` or `ai` step, that the app does not allow (anywhere, nested steps and the trigger included) is refused with 403 and `"not_allowed": [...]`. Starting a run checks the version that would run too, so narrowing an app later stops runs of what it no longer allows. A `template` must be one of the app's.

**Headless mode** is the same API without a browser: register the app with `"headless": true` and call these routes from your servers with an end-user token, to build your own UI on the engine. `TestHeadlessFlow` (`api/embed_test.go`) walks it end to end: list connectors, create from a template, validate, save a second version, lay out, publish, start a run, read it, stream it, list runs, and see the outcome, redacted, through the partner API.

What end users cannot do, by design: manage secrets, variables, connections, members, roles, policies, Git or alerts; read the audit log; reveal or erase personal data; decide approvals. Those stay with the partner and platform (C3 brings pre-authenticated partner connections). With four-eyes publishing on, a publish needs a person, so end users cannot publish.

## 6. Partner webhooks

Taskiem posts events about your sub-tenants to each active app with a `webhook_url` that subscribes to them:

| Event | When | `data` |
| --- | --- | --- |
| `run.completed`, `run.failed` | A sub-tenant's run ends (queued in the transaction that ends it) | `run`: id, workflow id, version, environment, status, start and end. No inputs or outputs |
| `workflow.published` | A sub-tenant publishes a version | `workflow`: id, version, time |
| `usage.threshold` | A sub-tenant reaches 80% and 100% of its daily or monthly run quota, or all your sub-tenants reach 80% and 100% of a partner-wide cap; once per period | `usage`: scope (`sub_tenant` or `partner`), limit, period, used, cap, percent |

```http
POST https://api.payrolla.com/taskiem/webhooks
Content-Type: application/json
Taskiem-Signature: t=1791334800,v1=5f1c...
Taskiem-Event: run.completed
Taskiem-Delivery-Id: 4b1d...

{"id": "4b1d...", "event": "run.completed", "created_at": "...", "sub_tenant_id": "0190f0c3-...", "data": {"run": {...}}}
```

Verify every delivery as you would an [alert webhook](alerts.md): compute HMAC-SHA256 with your `whsec_...` secret over `<t>.` followed by the raw body, compare it in constant time with `v1`, and refuse timestamps more than five minutes old. Deduplicate on `Taskiem-Delivery-Id`: a delivery can arrive more than once.

Deliveries are retried with backoff (30 s, 2 min, 8 min, 32 min, about 2 h, then every 6 h; 10 attempts, about a day) until your endpoint answers 2xx. They go out through the egress guard, so private and internal addresses are refused. `GET /v1/partner/webhook-deliveries` (filters `app`, `status`, `event`, `before`, `limit`) is the delivery log, with each one's attempts, last HTTP status and error; `POST /v1/partner/webhook-deliveries/{id}/retry` sends a failed one again. The scheduler role sends them every 15 seconds.

## Security model

- **Isolation.** A sub-tenant is a tenant: row-level security keeps its data from every other tenant. No membership puts a sub-tenant in a session's scope, including the partner's owners. The only way from a partner to a sub-tenant is `taskiem_partner_enter`, a reviewed `SECURITY DEFINER` function that checks the sub-tenant's `parent_id` against the partner the transaction is scoped to, writes the access to both audit chains, and narrows the transaction to that one sub-tenant. A tenant's parent can never change.
- **Dual audit trail.** Every partner access to a sub-tenant (reading its workflows, runs or usage, changing its limits or status, minting or revoking tokens) is written to the partner's chain (actor: the partner's key) and to the sub-tenant's chain (actor `partner:<partner>/key:<id>`), in the same transaction as the access. End users' own actions are in the sub-tenant's chain as `end_user:<app>/<id>`.
- **Own keys.** A sub-tenant's secrets are encrypted under its own key-encryption key, created when it first stores one; it never uses the partner's.
- **Tokens.** Opaque 256-bit random values (`tsk_eut_...`) stored only as SHA-256 hashes. They expire within an hour, are bound to one app and optionally one origin, work only on `/v1/embed/{app}`, and are refused once revoked, once the sub-tenant is suspended, once the app is disabled, and once the partner stops being a partner. Their permissions are checked on every request against the app's current allow-list and the platform's fixed ceiling.
- **CORS** only on embed routes, only for the app's exact origins, never with credentials.
- **Redaction.** The partner sees outcomes, not data: run statuses and error kinds, never inputs, outputs or messages, in the API and in webhooks.

The threat model's boundary B10 covers this ([threat model](security/threat-model.md)).

## What comes next

| Milestone | Builds on |
| --- | --- |
| C2: embedded builder web component (`<taskiem-builder token="…">`) and iframe, theming, custom domains, white-label | The embed API above is its whole backend; `GET /me` returns the branding tokens it renders; an iframe will need the app's origins in `frame-ancestors` for its own pages (today every page sends `frame-ancestors 'none'`) |
| C3: partner connector bridge | `allowed_connectors` and the sub-tenant's own key: the partner's API as a connector whose credentials the partner provisions per sub-tenant through the partner API |
| C4: first embedded deployment (Payrolla) | Needs people |

Known gaps in C1: a suspended sub-tenant's schedules and webhook triggers keep firing; end users cannot hold `approval.decide` (decisions are recorded against platform users); deleting a sub-tenant is an operator task.
