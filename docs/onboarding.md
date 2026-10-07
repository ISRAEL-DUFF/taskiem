# Onboarding

Self-serve signup, the getting-started checklist, the guided first workflow, in-product help, and how gate G4's "signup to first successful run in under 15 minutes" is measured (Phase 4, P4-2; [decision 0022](decisions/0022-self-serve-onboarding.md)).

## Turning it on

Signup is off by default. Before turning it on for the public:

1. Make sure the deployment can send email: `TASKIEM_SMTP_URL`, `TASKIEM_ALERT_FROM` and `TASKIEM_PUBLIC_URL` ([operations](operations.md#configuration)). Without them nobody can confirm an email, so nothing is held back for unconfirmed signups and the server logs a warning at each signup.
2. With billing on, each new tenant starts its trial ([billing](billing.md)).
3. Set `TASKIEM_ALLOW_SIGNUP=true`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `TASKIEM_ALLOW_SIGNUP` | `false` | Offers "Create an account" on the sign-in page and accepts `POST /v1/signup` |
| `TASKIEM_SIGNUP_PER_ADDRESS` | `5` | Signups one client address may make a day, counted in the database across every API replica; negative means no limit. Behind a load balancer, set `TASKIEM_TRUST_PROXY=true` so the address is the client's |
| `TASKIEM_SIGNUP_BLOCKED_DOMAINS` | — | Comma-separated email domains (and their subdomains) that may not sign up, besides the built-in list of throwaway mailbox services |
| `TASKIEM_DOCS_URL` | — | The public docs site. Help panels link to `<url>/<page>`; without it they show only their in-product text |

## Signup

The sign-in page offers "Create an account" when signup is on. The form asks for the organisation's name, the person's name, an email and a password. The person is signed in at once and lands on **Get started**.

What signup refuses, all without a third-party service:

| Check | Answer |
| --- | --- |
| A body that is not JSON (a cross-site form) | `415` |
| Anything in the form's hidden field (bots fill it) | `400` |
| An address that is not one plain address with a dotted domain | `400` |
| A throwaway mailbox domain (built-in list, plus `TASKIEM_SIGNUP_BLOCKED_DOMAINS`), `.invalid` or `.localhost` | `400` |
| An organisation or person name with a link, a domain-like word, an email address, angle brackets or control characters (names appear in invitation emails), or too long | `400` |
| A password under 12 or over 1024 characters, or equal to the email | `400` |
| Over the sign-in burst from one address (per replica) | `429` |
| Over `TASKIEM_SIGNUP_PER_ADDRESS` from one address today (every replica) | `429` |
| An email that already has an account | `409` |

Every outcome is counted in `taskiem_signups_total{outcome}`.

## Confirming the email

When the deployment can send email, signup sends a link to `/verify-email#token=…`. The link works once, for 24 hours, for the person it was sent to, signed in to the organisation they created; it locks after five wrong secrets, and asking again (Get started > "Send the link again", three, then one every 20 minutes) replaces it. The token is in the URL fragment, which browsers send to no server.

Until the person who signed up confirms their email, their organisation can build, publish and run, but cannot:

- invite or add members (`POST /v1/members`), which sends email to other people;
- create API keys (`POST /v1/api-keys`), which lets automation act outside the browser.

Both answer `403` with "confirm your email address first". Tenants created by `taskiem bootstrap` or before this release are not held back.

## Getting started

**Get started** (`/start`) shows the checklist and the guided first workflow. Organisations that signed themselves up also see it in the navigation, with their progress, until every step is done or someone with `workflow.edit` hides it. It stays at `/start` either way.

The checklist is worked out from what the organisation has done, not from stored ticks:

| Step | Done when |
| --- | --- |
| Confirm your email | The signed-in person's email is confirmed |
| Connect an app | The organisation has a connection |
| Create a workflow | It has a workflow |
| Publish it | It has a published version |
| Run it once | It has a completed run |
| Invite a teammate | It has a second member, or an invitation waiting |

The guided first workflow (`/start/guide?template=<id>`) takes one [template](templates.md) through four steps on one page: its details; its connection (saved as the name the template uses, `main` by default, in prod) and the variables it reads (such as `owner_phone`); create and publish; and a test run with an input sampled from the workflow's inputs schema, whose status updates until it ends. Starter templates (one connector, nothing to wait for) are offered first. A template's page in **Templates** links to the same guide.

## In-product help

Get started, Templates, Connections and the Variables section of Settings have a help panel: a few plain sentences on what the page is for. With `TASKIEM_DOCS_URL` set, each panel also links to its page on the docs site: `/onboarding`, `/templates`, `/connector-sdk` and `/environments`. `make docs` builds that site, with the same URLs ([decision 0025](decisions/0025-public-docs-and-api-reference.md)).

## Measuring gate G4

Gate G4 asks for self-serve signup to first successful run in under 15 minutes.

- Each self-serve tenant has a row in `tenant_onboarding` with `signed_up_at`. When its first run completes, the orchestrator stamps `first_run_at` and `first_run_id` in the same transaction, once.
- The same moment is observed in the histogram `taskiem_onboarding_first_run_seconds`. The share of signups that made it in time is `taskiem_onboarding_first_run_seconds_bucket{le="900"} / taskiem_onboarding_first_run_seconds_count` (over a window, use `increase`). Tenants that never complete a run are not observed: compare the count with `taskiem_signups_total{outcome="created"}` for the share that never got there.
- The tenant sees the same figure on Get started ("Your first successful run came 4 min 05 s after you signed up"), and `GET /v1/onboarding` answers `seconds_to_first_run`.
- `web/e2e/onboarding.spec.ts` runs the whole path in a browser (signup, template, a fake Termii, publish, run) and checks the measured time is under 15 minutes.

## API

| Route | Who | What it does |
| --- | --- | --- |
| `GET /v1/signup` | anyone | `{"enabled": bool}` |
| `POST /v1/signup` | anyone (when on) | `{tenant, email, name?, password, bearer?}`: creates the organisation, signs the owner in (a session cookie; with `bearer: true`, `token` in the answer instead), starts the trial; `201 {tenant_id, user_id, verification_sent}` (and `token` with `bearer`) |
| `POST /v1/me/email/verify` | the person | `{token}` from the link |
| `POST /v1/me/email/verify/resend` | the person | A new link (`202`), or `200` when already confirmed |
| `GET /v1/onboarding` | any member | `items` (`[{id, done}]`), `done`, `total`, `complete`, `dismissed`, `self_serve`, `email_verified`, `can_send_email`, `docs_url`, `signed_up_at`, `first_run_at`, `seconds_to_first_run` |
| `POST /v1/onboarding/dismiss` | `workflow.edit` | Hides the checklist from the navigation for the organisation |
