# Embedding Taskiem (partner guide)

A **partner** puts Taskiem inside its own product so that its customers automate their work without signing up to Taskiem (spec 13.1, 13.4). Each of the partner's customers is a **sub-tenant**: a tenant of its own, with its own workflows, runs, audit chain and keys, isolated from the partner's other customers and from the partner's own staff. The people who use it are **end users**: the partner's users, known to Taskiem only by the id the partner gives them, acting through short-lived tokens the partner's server mints.

This page covers sub-tenants, the partner admin API, embed apps, end-user tokens, headless use of the API and partner webhooks (milestone C1); the embedded builder as an element or an iframe, theming, custom domains and white-label (C2); and the partner connector bridge, your own API as a pre-authenticated connector (C3). The design is in [decision 0015](decisions/0015-embedding-tenancy.md).

## Overview

```
 partner's server ──partner API key──▶ /v1/partner/...          (create sub-tenants, apps, mint tokens, observe)
        │ mints
        ▼
 end-user token ──▶ partner's page: <taskiem-builder> ──CORS──▶ /v1/embed/{app}/...   (build and run, inside one sub-tenant)
                    or an iframe of /embed/{app}/frame (token by postMessage)
                    or partner's server (headless) ──▶ /v1/embed/{app}/...
 partner's API ──as a shared connector, credentials provisioned per sub-tenant──▶ end users' workflows
 Taskiem ──signed webhooks──▶ partner's server                  (run outcomes, publishes, usage)
```

## 1. Become a partner

An operator makes a tenant a partner and sets its partner-wide caps (0: no cap):

```sh
taskiem tenants partner 0190f0c2-... --max-subtenants 500 --subtenant-runs-per-day 100000 --subtenant-runs-per-month 2000000
taskiem tenants partner 0190f0c2-... --disable   # stops the partner API and every end-user token; sub-tenants are kept
```

A sub-tenant cannot itself be a partner. Every change is audited (`partner.enable`, `partner.disable`).

The operator also sets what the partner's plan includes, replacing the list (`""` for none): `white_label` ([white-label](#11-white-label) apps) and `custom_domains` ([custom domains](#10-custom-domains)):

```sh
taskiem tenants partner 0190f0c2-... --capabilities white_label,custom_domains
```

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
| `white_label` | Leave out the platform's branding ([white-label](#11-white-label)); needs the `white_label` capability |
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
| `GET /me` | — | The end user, sub-tenant, permissions, the app's allowed connectors and templates, branding tokens, `white_label`, expiry |
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

What end users cannot do, by design: manage secrets, variables, connections, members, roles, policies, Git or alerts; read the audit log; reveal or erase personal data; decide approvals. Those stay with the partner and platform; the partner provisions connections for them ([connector bridge](#12-the-partner-connector-bridge)). With four-eyes publishing on, a publish needs a person, so end users cannot publish.

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

## 7. Embed the builder: the script tag

Load the bundle from Taskiem (or from your [custom domain](#10-custom-domains)) and place the element where the builder should appear. Give it a token minted by your server; the element never sees your partner key.

```html
<script src="https://app.taskiem.com/embed/v1/taskiem.js" defer></script>

<taskiem-builder id="automations" app="0190f0c4-..."></taskiem-builder>

<script>
  const el = document.getElementById("automations");
  // Your own endpoint, which calls POST /v1/partner/embed-apps/{app}/tokens.
  const mint = async () => (await (await fetch("/taskiem-token", { credentials: "include" })).json()).token;
  el.addEventListener("taskiem-token-expiring", async () => el.setToken(await mint()));
  el.addEventListener("taskiem-token-expired", async () => el.setToken(await mint()));
  mint().then((t) => el.setToken(t));
</script>
```

The bundle is one script (about 145 kB compressed: React and the canvas included) that defines the elements and nothing else on your page. Each element renders in its own shadow root: your page's styles do not reach the builder and the builder's do not reach your page. It calls only `/v1/embed/{app}/...` on the origin it was loaded from, with the token in the `Authorization` header (never a cookie), and streams runs with `fetch` (an `EventSource` cannot send the header), reconnecting where it left off. The bundle is served with `Cache-Control: public, max-age=300, stale-while-revalidate=86400`, `Access-Control-Allow-Origin: *` and `Cross-Origin-Resource-Policy: cross-origin`, so it also loads under `crossorigin` and Cross-Origin-Embedder-Policy; `v1` changes only compatibly. Your page's Content-Security-Policy must allow it: `script-src` and `connect-src` for the Taskiem origin, and `img-src` for your logo's host.

| Element | Shows |
| --- | --- |
| `<taskiem-builder>` | The end user's automations: list, create, the canvas editor with the step panel (connectors and step types limited to the app's), validate, save, publish, start a run and watch it live |
| `<taskiem-runs>` | Their runs, and each run live on the canvas with its steps' states |

| Attribute or method | Meaning |
| --- | --- |
| `app` | The embed app's id (required) |
| `token` | An end-user token. Prefer `setToken()`: an attribute stays in the DOM |
| `base` | The API's origin, if it is not the one the bundle came from |
| `setToken(token)` | Hands over a new token; the element re-reads `/me` and carries on without a reload |

Events bubble and cross the shadow boundary (`composed`); `detail` carries the fields shown:

| Event | When | `detail` |
| --- | --- | --- |
| `taskiem-loaded` | The first token was accepted | `app`, `end_user`, `expires_at` |
| `taskiem-token-expiring` | 60 seconds before the token expires: mint a new one and call `setToken` | `expiresAt`, `remaining` (ms) |
| `taskiem-token-expired` | A request was refused with 401 (expired or revoked token) | — |
| `taskiem-published` | The end user published a version | `workflow`, `version` |
| `taskiem-run-started` | The end user started a run | `run`, `workflow` |
| `taskiem-run-completed`, `taskiem-run-ended` | A run the end user is watching finished (completed, or failed or cancelled) | `run`, `status` |

Tokens cannot be refreshed by the browser: only your server, with the partner key, mints them. Mint for 15 minutes (the default) or less and answer `taskiem-token-expiring`.

## 8. Iframe mode

Where a script on your page is not wanted, frame the builder instead. The frame page is `https://app.taskiem.com/embed/{app}/frame` (add `?view=runs` for the run list). It is the only Taskiem page that may be framed, and only by the app's allowed origins: it is served with `Content-Security-Policy: … frame-ancestors <the app's allowed origins>` and no `X-Frame-Options`, while every other page keeps `frame-ancestors 'none'` and `X-Frame-Options: DENY`.

The token never goes in the URL. The frame and your page exchange it with `postMessage`, each checking the other's origin:

```html
<iframe id="taskiem" src="https://app.taskiem.com/embed/0190f0c4-.../frame" title="Automations"
        style="width:100%;height:720px;border:0"></iframe>
<script>
  const TASKIEM = "https://app.taskiem.com"; // or your custom domain's origin
  const frame = document.getElementById("taskiem");
  const send = async () => frame.contentWindow.postMessage({ type: "taskiem:token", token: await mint() }, TASKIEM);
  window.addEventListener("message", (e) => {
    if (e.origin !== TASKIEM || e.source !== frame.contentWindow) return; // only the frame
    if (e.data.type === "taskiem:ready" || e.data.type === "taskiem:token-expiring" || e.data.type === "taskiem:token-expired") send();
  });
</script>
```

| Message | Direction | Meaning |
| --- | --- | --- |
| `{type: "taskiem:ready", app}` | frame → page | The frame has loaded and needs a token. Sent only to your page's origin when the browser reports it (`location.ancestorOrigins`) and it is one of the app's; otherwise to each of the app's origins, of which only yours receives it |
| `{type: "taskiem:token", token}` | page → frame | A token (the first, or the next one). Accepted only from the parent window, only from one of the app's allowed origins and, after the first, only from the origin that sent the first. Post it with the Taskiem origin as `targetOrigin`, never `"*"` |
| `{type: "taskiem:token-expiring", expiresAt, remaining}`, `{type: "taskiem:token-expired"}` | frame → page | Send a new token |
| `{type: "taskiem:loaded" \| "taskiem:published" \| "taskiem:run-started" \| "taskiem:run-completed" \| "taskiem:run-ended", …}` | frame → page | As the element's events |

After the first token the frame posts only to that page's origin. Its own API calls come from the Taskiem origin and name your page's origin in an `X-Taskiem-Embed-Parent` header; the API believes that header only from its own origin (where no one else's script runs) and checks it exactly as it checks a page's `Origin`, including a token's bound origin.

## 9. Theming

The app's `branding` tokens (validated when you register the app, and again by the builder) become CSS custom properties on the builder's root inside its shadow root, or in the frame page. Nothing else from them reaches the page: no stylesheet text, no markup, no arbitrary properties.

| Token | Custom property | Used for |
| --- | --- | --- |
| `colours.primary` | `--accent` | Primary buttons, selected steps, links |
| `colours.on_primary` | `--accent-text` | Text on primary buttons |
| `colours.background` | `--bg` | The builder's background |
| `colours.surface` | `--panel` | Panels, the canvas, cards, tables |
| `colours.text` | `--text` | Text |
| `colours.muted` | `--muted` | Hints and secondary text |
| `colours.border` | `--border` | Borders |
| `colours.accent` | `--info` | Running steps, notices |
| `colours.danger` | `--danger` | Errors, failed steps |
| `colours.success` | `--ok` | Completed steps |
| `font_family` | `--tk-font` | The builder's font |
| `radius` | `--tk-radius` | Corners of buttons, inputs, panels and steps |
| `mode` | — | `light` or `dark` fixes the palette the colours above override; `auto` (the default) follows the viewer's preference |
| `logo_url` | — | Shown at the top left (an `<img>`, no referrer) |
| `font_url` | — | An https stylesheet (Google Fonts, your CDN) added once to the page's `<head>`: browsers ignore fonts declared inside a shadow root |

## 10. Custom domains

Serve the builder from your own host name, for example `automations.payrolla.com`. It needs the `custom_domains` capability on your plan (an operator sets it: `taskiem tenants partner <id> --capabilities custom_domains,white_label`).

1. Claim the domain for an app; the answer is the DNS record that proves you control it:

   ```http
   POST /v1/partner/embed-apps/{app}/domains
   {"domain": "automations.payrolla.com"}
   → 201 {"domain": "automations.payrolla.com", "record": "_taskiem-verify.automations.payrolla.com", "txt_value": "taskiem-verify=…"}
   ```

2. Add the TXT record, and point the host at Taskiem (a CNAME to the address your Taskiem operator gives you). Ask the operator to add the host to the ingress (below) so it gets a certificate.
3. Verify: `POST /v1/partner/embed-apps/{app}/domains/{domain}/verify` (409 until the record is visible; DNS can take a while). `GET /v1/partner/embed-apps/{app}` lists the app's domains and their state; `DELETE /v1/partner/embed-apps/{app}/domains/{domain}` removes one at once.

Once verified, and while your plan keeps the capability and the app is active:

- `https://automations.payrolla.com/embed/v1/taskiem.js` is the bundle, `…/embed/frame` the app's frame page (its app implied by the host), and `…/v1/embed/{app}/…` the app's embed API. Nothing else is served on that host: the console, sign-in and the rest of the API answer 404 there, so your domain never shows Taskiem's own pages.
- `https://automations.payrolla.com` is one of the app's origins (CORS answers it).

A domain is claimed per partner; any number of partners may claim one, but only one can verify it, and the platform's own host and its subdomains cannot be claimed. An app may have up to 10 domains.

**Ingress and certificates (operators).** The application never issues certificates. Each verified host needs one at the ingress. With the Helm chart:

```yaml
ingress:
  enabled: true
  embedAnnotations:
    cert-manager.io/cluster-issuer: letsencrypt   # cert-manager issues one certificate per host (HTTP-01 works once the CNAME is in place)
  embedHosts:
    - host: automations.payrolla.com              # TLS secret: automations-payrolla-com-tls unless secretName is set
```

This adds an Ingress, `<release>-embed`, sending only `/embed/` and `/v1/embed/` on those hosts to the API. Without cert-manager, put a certificate in each `secretName` (or one wildcard certificate, if your partners' hosts share a parent domain you control). The API maps a host to its app only after verification, so an unverified host on the ingress serves nothing of an app. Set `TASKIEM_PUBLIC_URL`: the platform's own host is never treated as a custom domain.

## 11. White-label

An app with `"white_label": true` leaves out the platform's branding. It needs the `white_label` capability on your plan (403 without it), and stops at once if the operator withdraws the capability.

- **The embedded builder**: no "Powered by Taskiem" (`GET /me` returns `white_label`).
- **Email** sent on a sub-tenant's behalf: alert emails lose the `[Taskiem]` subject prefix and test messages do not name the platform, when the sub-tenant's partner has the capability and an active white-label app.
- **WhatsApp**: templates are fixed text that Meta approves, and the platform's templates name Taskiem. White-label WhatsApp needs a set of partner templates without it, approved by Meta, and a decision on whose number sends them ([needs people](needs-people.md#phase-3), EM3). Until then, WhatsApp messages to a white-label sub-tenant's members keep the platform's templates.
- Protocol names stay: webhook headers (`Taskiem-Signature`), token prefixes and API paths.

## 12. The partner connector bridge

Your sub-tenants' end users can use your own product from their automations, already signed in, without ever seeing a credential (spec 13.4 step 4).

1. **Package your API as a connector.** Write a connector/v1 manifest and a WebAssembly module ([connector/v1](contracts/connector-v1.md), [WebAssembly connectors](contracts/connector-wasm-v1.md); the example in `examples/wasm-connector`) and upload it to your own tenant: `POST /v1/tenant-connectors` (a key with `connector.manage`). Its id starts with `x_`.
2. **Share it with your sub-tenants**: `PUT /v1/partner/connectors/{id}/share` (every enabled version, present and future). `GET /v1/partner/connectors` lists what you share; `DELETE /v1/partner/connectors/{id}/share` stops. Sub-tenants can use a shared connector but not read, change or disable it.
3. **Allow it** in the app: add its id to `allowed_connectors` (the app accepts shared ids like catalogue ones).
4. **Provision each sub-tenant's credentials** from your server:

   ```http
   POST /v1/partner/sub-tenants/{sub}/connections
   {"environment": "prod", "connector": "x_payrolla@1", "name": "main", "credentials": {"api_key": "pk_live_customer_1042"}}
   → 201 {"id": "…", "environment": "prod", "connector": "x_payrolla", "name": "main", "provisioned_by": "partner:<you>/key:<id>"}
   ```

   The credentials are written into that sub-tenant's vault, encrypted under its own key, in a transaction entered through `taskiem_partner_enter`: the access is in both audit chains (`partner.connection.create`), and the sub-tenant's chain also records `connection.create`. The connector must be available to the sub-tenant (the catalogue's or one you share) and allowed by at least one of your active apps.

| Endpoint | Permission | Does |
| --- | --- | --- |
| `GET /v1/partner/sub-tenants/{sub}/connections` | read | Its connections: id, environment, connector, name, status, who provisioned them. Never credentials |
| `POST /v1/partner/sub-tenants/{sub}/connections` | manage | Provision one (above) |
| `PUT /v1/partner/sub-tenants/{sub}/connections/{id}/credentials` | manage | Replace (rotate) the credentials of one you provisioned: `{"credentials": {...}}` |
| `DELETE /v1/partner/sub-tenants/{sub}/connections/{id}` | manage | Remove one you provisioned, and its credentials |

Credentials are write-only everywhere: no route returns them, to you, to the sub-tenant or to its end users (who have no connection routes at all). A run decrypts them only inside the sub-tenant that holds them, so one customer's credentials can never run in another's workflows. End users name the connection in a step (`"connection": "main"`) or, with one connection per connector and environment, leave it out.

## Security model

- **Isolation.** A sub-tenant is a tenant: row-level security keeps its data from every other tenant. No membership puts a sub-tenant in a session's scope, including the partner's owners. The only way from a partner to a sub-tenant is `taskiem_partner_enter`, a reviewed `SECURITY DEFINER` function that checks the sub-tenant's `parent_id` against the partner the transaction is scoped to, writes the access to both audit chains, and narrows the transaction to that one sub-tenant. A tenant's parent can never change.
- **Dual audit trail.** Every partner access to a sub-tenant (reading its workflows, runs or usage, changing its limits or status, minting or revoking tokens) is written to the partner's chain (actor: the partner's key) and to the sub-tenant's chain (actor `partner:<partner>/key:<id>`), in the same transaction as the access. End users' own actions are in the sub-tenant's chain as `end_user:<app>/<id>`.
- **Own keys.** A sub-tenant's secrets are encrypted under its own key-encryption key, created when it first stores one; it never uses the partner's.
- **Tokens.** Opaque 256-bit random values (`tsk_eut_...`) stored only as SHA-256 hashes. They expire within an hour, are bound to one app and optionally one origin, work only on `/v1/embed/{app}`, and are refused once revoked, once the sub-tenant is suspended, once the app is disabled, and once the partner stops being a partner. Their permissions are checked on every request against the app's current allow-list and the platform's fixed ceiling.
- **CORS** only on embed routes, only for the app's exact origins, never with credentials.
- **Framing.** Only the frame page may be framed, only by the app's origins; tokens reach it by origin-checked `postMessage`, never in a URL.
- **Custom domains** map to an app only after DNS verification, are exclusive once verified, and serve the embed surface alone.
- **Bridge credentials** are written into the sub-tenant's own vault through the audited partner path and are never readable through any API.
- **Redaction.** The partner sees outcomes, not data: run statuses and error kinds, never inputs, outputs or messages, in the API and in webhooks.

The threat model's boundary B11 covers this ([threat model](security/threat-model.md)).

## What comes next

| Milestone | Status |
| --- | --- |
| C4: first embedded deployment (Payrolla) | Needs people ([needs people](needs-people.md#phase-3), EM1) |

Known gaps: a suspended sub-tenant's schedules and webhook triggers keep firing; end users cannot hold `approval.decide` (decisions are recorded against platform users); deleting a sub-tenant is an operator task; verified custom domains are not re-verified periodically; white-label WhatsApp needs partner templates approved by Meta (EM3); connector bridge credentials are fields the partner provisions (api keys, basic), not an OAuth flow the end user goes through.
