# The public host

The public host serves pages anyone on the internet can open: [storefronts](spec/storefronts.md) and [hosted forms](spec/forms.md) (Phase 5, milestone S1). It is built and off by default. Until the storefront and form pages arrive (S2, S7), a verified host shows "Coming soon".

## What keeps it apart

| Concern | Control |
| --- | --- |
| A flood or a viral page slowing payments, chat or the console | Its own role (`taskiem serve --role public`), deployment, scaling and connection pool (`TASKIEM_PUBLIC_DATABASE_POOL`, 5), apart from `edge`, which takes webhooks |
| A bug reading tenant data | It connects as `taskiem_public`, which can read no table and execute only `taskiem_public_host` and the narrow functions later milestones add (such as `taskiem_public_storefront_domain`, S6). A test fails the build if the role gains any other table privilege or privileged function |
| The platform's secrets | `--role public` needs only `TASKIEM_PUBLIC_DATABASE_URL`: no KMS, no `TASKIEM_DATABASE_URL`, no provider keys. The Helm chart gives its pods `public.existingSecret` alone |
| Cookies on a business's domain | None are set: any a handler tries to set is dropped and logged |
| Script injection and framing | `Content-Security-Policy` allows no inline script or style and no framing (`httpsec.PublicCSP`); a test fails any template that uses inline code. Only `/embed/` pages may be framed, and only by the organisation's [proven websites](embed-sites.md) |
| Floods of page views | A per-address limit (300 a minute, bursts of 100), generous because mobile networks put many phones behind one address; static files are not counted |
| Floods of made-up host names | Host lookups are cached in a bounded cache (10,000 hosts, 30 seconds; unknown hosts 10 seconds) |
| Certificates for names it does not serve | `/tls/ask?domain=` answers 200 only for a verified host. It is on the internal metrics port, never on the public listener |

## Setting it up

1. **A login of its own.** As the schema owner:

```sql
CREATE ROLE taskiem_public_login LOGIN PASSWORD '…' NOINHERIT;
GRANT taskiem_public TO taskiem_public_login;
```

   Then set `TASKIEM_PUBLIC_DATABASE_URL` to that login. The server sets `taskiem_public` on every connection, and warns at start if it shares the other roles' login.
2. **Run it.** Use `taskiem serve --role public`, or set `TASKIEM_PUBLIC_ENABLED=true` with `--role all`. It listens on `TASKIEM_PUBLIC_LISTEN` (`:8082`). Metrics, `/healthz`, `/readyz` and `/tls/ask` are on `TASKIEM_METRICS_LISTEN`. `/readyz` answers 503 while shutting down, or while its database does not answer a ping within 2 seconds, since every page and form needs it; the `mail` role's does the same.
3. **Kubernetes.** Set `public.enabled: true` and create the `public.existingSecret` Secret (default `taskiem-public`) holding `TASKIEM_PUBLIC_DATABASE_URL` and `TASKIEM_PUBLIC_KEY`. The main Secret holds the same `TASKIEM_PUBLIC_KEY`: the `api` role reads the orders the forms send ([storefronts](storefronts.md#orders-questions-and-call-backs)). With `networkPolicy.enabled`, set `networkPolicy.tlsProxyNamespace` to the namespace of the proxy that issues certificates.
4. **In front of it**, a proxy that terminates TLS:
   - The preview domain uses one wildcard certificate (`*.<preview domain>`).
   - Businesses' own domains get certificates on demand, after asking `/tls/ask`. With Caddy, use `on_demand_tls { ask http://<public host>:9090/tls/ask }`. The [VM kit](vm.md#public-pages-storefronts-and-forms) does this for you.
   - Set `TASKIEM_TRUST_PROXY=true` behind it, so limits apply to visitors' addresses.

## Themes

Every page is drawn from the business's theme's own templates (`engine/publicweb/templates/clean/`, `bold/`, `editorial/`; [decision 0042](decisions/0042-public-page-themes.md)). What is the same in all of them, the head's tags, a form's hidden fields and the icons, is in `templates/shared/`. A page loads its theme's stylesheet and its colour's, `/_brand/1/<rrggbb>.css`: custom properties made from the colour's name alone, so it is the same for every business and cached for good.

`/_preview/<page>?p=` draws a page in a look not yet saved, with sample content, for the web app's **Look** tab: the api role seals the look with the key both roles hold (`TASKIEM_PUBLIC_KEY`), it works for an hour, and only the app (`TASKIEM_PUBLIC_URL`) may frame it.

The test suite runs once per theme in CI (`TASKIEM_TEST_THEME=bold go test ./engine/publicweb/`). `TASKIEM_PREVIEW_DIR=<folder> go test -run TestWritePreview ./engine/publicweb/` writes the main pages in every theme to a folder to look at (`python3 -m http.server` in it).

## Hosts

`public_hosts` maps a host name to the site it serves. A host serves only once verified. A suspended host, or one whose organisation is suspended, answers 410 "This page is unavailable". Suspension takes effect on every replica within 30 seconds, with no signal between them.

Rows are written when storefronts are published (their preview address, S2) and when a business's [own domain](storefronts.md#your-own-domain) is verified (S6: the domain and its `www.` twin, through `taskiem_storefront_claim_domain`; removed when the domain is removed or unverified). While a storefront is published or closed and has a verified own domain, its other hosts answer page views (`GET`, `HEAD`, outside `/embed/`) with a 301 to the primary domain, cached for an hour; the public role reads that domain with `taskiem_public_storefront_domain`. Operators set where businesses point their domains with `TASKIEM_PUBLIC_CNAME` and `TASKIEM_PUBLIC_ADDRESSES`.

Operators can also add a row by hand to try the host:

```sql
INSERT INTO public_hosts (host, tenant_id, kind, site_id, verified_at)
VALUES ('ada.shops.example.com', '<tenant id>', 'storefront', gen_random_uuid(), now());
```

## Hosted forms

[Hosted forms](spec/forms.md) (S7) are served on a platform host of their own, the **form host**: `forms.<first of TASKIEM_PUBLIC_DOMAINS>` by default, or `TASKIEM_PUBLIC_FORM_HOST`. It is not a `public_hosts` row: requests with that `Host` go straight to the forms. The preview domain's wildcard certificate covers it, and `/tls/ask` answers 200 for it. Without `TASKIEM_PUBLIC_KEY`, the form host answers "Forms are not open yet". (A `public_hosts` row of kind `form`, an organisation's own form domain, still shows "Coming soon".)

| Path | What it serves |
| --- | --- |
| `GET/POST /f/{public_id}` | A form trigger's form, for `access: public`. A `link` form says it opens from the link sent; a `members` form says it opens in the app, linking to `TASKIEM_PUBLIC_URL/forms/{public_id}` |
| `POST /f/{public_id}/code` | Asks for a 6-digit code (`verify: phone` or `email`) |
| `GET /f/{public_id}/done/{reference}?k=` | The result page (`after.mode: result`): refreshes every 2 seconds, up to 30, for the workflow's message |
| `/embed/f/{public_id}…` | The same, framed only by the organisation's verified [embed sites](embed-sites.md). It loads `form-embed.js`, which posts `form:height` and `form:submitted` (the reference only) to those sites, and records which of them framed the form |
| `GET/POST /r/{token}`, `POST /r/{token}/code` | A `link` form through its one-time link. A link bound to a person asks for the code sent to them first |
| `GET/POST /q/{token}`, `POST /q/{token}/code` | A form step's request: its questions, prefilled from the run; the code first when the step asks for one |
| `GET /s/{reference}?k=` | A status link (`after.status: true`): the state in plain words and the workflow's latest message, nothing else |
| `GET /i/{token}` | An [invoice's page](invoices.md#the-invoices-page), read-only, in the business's look: its lines, totals, status, due date and note, nothing about the customer. A malformed, unknown or draft's token answers the same 404 "This invoice was not found". Read with `taskiem_public_customer_invoice` (and `taskiem_public_brand` for the name); it does not need `TASKIEM_PUBLIC_KEY` |
| `GET /r/{token}` | Also a school receipt's check ([receipts](schools.md#checking-a-receipt)): Valid or Voided, the school, the amount and the date, by `taskiem_public_school_receipt`; a token that is no receipt's falls through to the form link |
| `GET /s/{token}`, `POST /s/{token}/paid`, `/plan`, `/link` | A school [family's page](schools.md#the-family-page) when the token is a family link's (32 characters; a status reference falls through). `taskiem_public_school_family` finds the link, expired or not; `_students`, `_invoices`, `_receipts` and `_requests` answer the family's record for a link that has not expired, with no names (they are in the link's view, sealed under a key made from the token, which only the page holds). The forms leave an ask in `school_family_inbox` through `taskiem_public_school_family_ask`, sealed under the inbox key (so they need `TASKIEM_PUBLIC_KEY`) and rate-limited per family in the function; the api role takes it (`schools.DrainFamilyInbox`) and records the claim or the plan request, or sends the new link. An expired link says "ask the school for a new link" and offers one; an unknown token gets the same words with no button |
| `POST /report` | Reports a form. For now it is logged (`form host: a form was reported`) and answered 204; the operator console's queue comes later |

How the pages behave:

- **No session.** A multi-page form carries the answers so far, and a verified address, in a hidden field sealed under the public key and bound to the form and its one-time token. Conditions (`show_if`) are evaluated on the server between pages; one that reads an answer not given hides its page. A page not shown sends nothing, and its fields are not required.
- **Fields** follow [forms 6.2](spec/forms.md#62-field-types). Inputs are named `a.<field>`, so no field can clash with the form's own inputs. A `naira` number is typed in naira; the run receives it as typed, or in kobo when the field's name ends `_kobo`. Not yet: `x-form.options_from` (shown as a text box) and `x-form.blocks` (not shown).
- **Prefill** from the link's query takes only fields marked `x-form.prefill`; hidden fields take a value only that way, or from a form step's run.
- **Checks** are the schema's (required, types, choices, lengths, patterns, limits), in plain words, and again in full before sending. Free text that holds a card number, BVN or NIN is refused unless the field is marked `x-pii: bvn` or `nin`. A submission is at most 256 KB without its files. A file is at most its field's `max_mb` (10 by default, 25 at most), of a type in its `accept` judged by its content, and at most 10 go with a form. Files on a page before the last are carried, sealed, through the later pages: together they may be at most 10 MB, so large files belong on the last page.
- **Codes** go through `taskiem_public_form_code`, which the `api` role sends. `once_per: phone` or `email` applies when the respondent verified that kind of address.
- **Limits:** 50 submissions and code requests an hour per client address (bursts of 20), on top of the page-view limit, the form token's fill time, the honeypot, and the database's own limits on codes (3 per address and 20 per form in 10 minutes, 5 attempts each).
- **After sending:** the message (minimal Markdown: paragraphs, `**bold**`, links only to the organisation's verified websites), a redirect (only to `https://` on a verified website; an embedded form says thank you instead, so the frame stays on the form), or the result page. Each shows the reference.
- **References** are `F-` and 8 letters and digits, drawn from the form token's idempotency key under the public key: a double tap or a retry answers with the first reference and writes nothing more.


## Building blocks for the next milestones

- **`engine/sender`:** SMS and email to customers and respondents through the platform's providers, from a durable outbox ([messages](messages.md)).

- **`engine/formguard`:** what every public form uses.
  - Signed one-time form tokens, with a minimum fill time, a maximum age and an idempotency key.
  - A honeypot field.
  - Optional proof-of-work (`pow.js`, under 1.5 KB).
  - Daily-rotating keyed hashes of client addresses, IPv6 by /64, so raw addresses are never stored.
  - Bounded per-address limiters.
- **`engine/publicnames`:** checks slugs, display names and domains.
  - Reserved platform words.
  - Protected names (banks, payment providers, agencies, Taskiem), including look-alikes: digits for letters, Cyrillic and Greek homoglyphs, accents, doubled letters.
  - Punycode and government domains refused.
