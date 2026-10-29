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
| `TASKIEM_DOCS_URL` | — | The public docs site, the website's `/docs`. Help panels link to `<url>/<page>`; without it they show only their in-product text |

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

When the deployment has terms (`TASKIEM_TERMS_VERSION` and `TASKIEM_PRIVACY_VERSION`, [below](#the-terms)), the form also asks the person to agree to them, and signup without `accept_terms: true` is `400`.

Every outcome is counted in `taskiem_signups_total{source, outcome}`, with `source` `signup` for this form (as `tenant_onboarding.source` names it), `agent` for [field agents](agents.md), and `whatsapp` or `telegram` for [signing up from chat](#signing-up-from-chat).

## Signing up from chat

Beside the web form, a person can sign up from Taskiem's shared WhatsApp number or its Telegram bot, without a laptop, an email or a password ([the plan](spec/chat-way-in.md), [decision 0058](decisions/0058-chat-as-a-way-in.md)). The web form, linking a chat after it and field agents stay as they are.

| Variable | Default | Meaning |
| --- | --- | --- |
| `TASKIEM_CHAT_SIGNUP` | — (off) | The channels that offer sign-up to strangers: `whatsapp`, `telegram`, comma-separated. Off, a stranger is told how to link a chat, as before. Needs the terms' versions |
| `TASKIEM_CHAT_SIGNUP_PER_HOUR` | `60` | Businesses chat may make an hour, every channel and replica together; over it, "try again in an hour" and a warning in the logs |
| `TASKIEM_CHAT_SIGNUP_COUNTRIES` | `234` | Calling codes sign-up is open to, comma-separated; a number elsewhere is told sign-up is not open there yet |
| `TASKIEM_TERMS_URL` | `<public url>/terms` | Where the terms and privacy notice are read |
| `TASKIEM_TERMS_VERSION`, `TASKIEM_PRIVACY_VERSION` | — | The versions people agree to (at most 32 characters each). Set both or neither |

**The conversation.** A message from a number (or Telegram account) linked to no one, that nothing else claims (a business's updates, invoices, riders and suppliers are handled first), gets what Taskiem is and three buttons: **Start my business**, **I already use Taskiem** (how to link a chat to a web account) and **Not now**. Typing a language's name chooses it, as `language` does. **Start my business** (or `start`, `sign up`) shows the terms and privacy notice with **Yes** and **No**: nothing is made before **Yes**. Then, one at a time, with `cancel` at any point: the business's name and the person's (the web form's checks), what they sell (a numbered list) and how customers pay (several numbers). On Telegram one more step: **Share my number**, which must be the person's own contact (Telegram tells), in a country sign-up is open to, and not a number a WhatsApp-linked person already has. Then the business is made and the chat says so, with the trial's days when billing is on, and three templates that fit what was answered, as buttons; the first, **Remind me every day**, needs nothing connected. A template tapped goes into the build conversation without the AI: what it still needs, one detail at a time, its steps, and **yes** to save the draft ([WhatsApp: build](whatsapp.md#build)). Or the person says what they want in their own words.

**What is made**, in one transaction shared with the field agent's sign-up: the person, with a sign-in name that receives no mail (`owner+…@phone.taskiem.invalid` by number, `…@telegram.taskiem.invalid` by Telegram account) and no password; the business, its workspace and environments; `tenant_onboarding` with `source` `whatsapp` or `telegram`; the channel bound to the person (only an address that wrote in the last hour and is bound to no one); the acceptance of the terms; `tenant.create` and `terms.accept` in the audit log. Then the trial starts, as for every way in. A person an agent signed up who never agreed is found by their number and is the same person.

**Limits.** One sign-up in progress per address (its answers wait in `chat_signups`, a person's record reached only through narrow functions, for 30 minutes); three businesses a day per number or Telegram account; `TASKIEM_CHAT_SIGNUP_PER_HOUR` across everyone; the countries. Every outcome is counted in `taskiem_signups_total` (`created`, `declined` for No to the terms, `cancelled`, `rate_limited`, `country`, `exists`).

**Another business.** A linked person sends `new business` (the same questions, without the terms and their name); one with no business left is offered **Start my business**.

**The holds.** A web signup is held back until its email is confirmed ([below](#confirming-the-email)). A chat-born owner proved their channel instead, which counts for API keys; what sends email for the business (inviting by email, email alert channels) waits for a confirmed email of their own.

**The chat PIN and dormant chats.** Until an email is added, the chat is the account's only credential: a chat-born person sets a six-digit PIN from the chat with `pin`, in the PIN form ([WhatsApp](whatsapp.md#the-chat-pin-and-dormant-chats)). Every chat binding silent for `TASKIEM_BINDING_DORMANT_DAYS` (90) is dormant until it is proven again.

## The terms

With `TASKIEM_TERMS_VERSION` and `TASKIEM_PRIVACY_VERSION` set, every way in records which versions a person agreed to, when and where (`terms_acceptances`: `web`, `whatsapp`, `telegram`): the web form's checkbox, **Yes** in a chat sign-up. An account from before them is asked once: on the web, a banner with **I agree** and **Not now** (`terms` on `GET /v1/me`, answered with `POST /v1/me/terms`); in chat, one message with **I agree**. Nothing is held back meanwhile, and a new version is asked for once in whichever door the person uses next.

## Confirming the email

When the deployment can send email, signup sends a link to `/verify-email#token=…`. The link works once, for 24 hours, for the person it was sent to, signed in to the organisation they created; it locks after five wrong secrets, and asking again (Get started > "Send the link again", three, then one every 20 minutes) replaces it. The token is in the URL fragment, which browsers send to no server.

Until the person who signed up confirms their email, their organisation can build, publish and run, but cannot:

- invite or add members (`POST /v1/members`), which sends email to other people;
- create API keys (`POST /v1/api-keys`), which lets automation act outside the browser;
- create email alert channels (`POST /v1/alerts/channels` with `kind: email`), which make the platform's mail server write to any address.

All three answer `403` with "confirm your email address first". Tenants created by `taskiem bootstrap` or before this release are not held back.

## Getting started

**Get started** (`/start`) shows the checklist and the guided first workflow. Organisations that signed themselves up also see it in the navigation, with their progress, until every step is done or someone with `workflow.edit` hides it. It stays at `/start` either way.

The checklist is worked out from what the organisation has done, not from stored ticks:

| Step | Done when |
| --- | --- |
| Confirm your email | The signed-in person's email is confirmed |
| Link WhatsApp or Telegram | The signed-in person has linked one |
| Make your page | The organisation's storefront is published |
| Pick a template or describe what you want | It has a workflow |
| Connect what it needs | The organisation has a connection, or a published workflow (publishing checks it has what it needs, so one that needs nothing counts) |
| Publish it | It has a published version |
| Run it once | It has a completed run |
| Invite a teammate | It has a second member, or an invitation waiting |

The guided first workflow (`/start/guide?template=<id>`) takes one [template](templates.md) through four steps on one page: its details; what it connects, on the guided screen (saved as the name the template uses, `main` by default, in prod), and the variables it reads; create and publish; and a test run with an input sampled from the workflow's inputs schema, whose status updates until it ends. Starter templates are offered first: the first, **Remind me every day**, needs nothing connected, **Send an invoice** and **Chase overdue invoices** are among the first six, and templates that need a developer come last. A template's page in **Templates** links to the same guide.

Beside the checklist, **Get paid and see your money** offers **Send your first invoice** (to [New invoice](invoices.md#making-and-sending-one), for members with `invoice.manage`) and **See your money** (to [Money](money-log.md#money), for `invoice.read`). They are links, not steps: not every business invoices, so they do not count towards the checklist.

## In-product help

Get started, Templates, Connections and the Variables section of Settings have a help panel: a few plain sentences on what the page is for. With `TASKIEM_DOCS_URL` set, each panel also links to its page of the owners' guide on the docs site ([`docs/guide/`](guide/index.md), plain how-tos, not the developer docs): `/guide/start`, `/guide/templates`, `/guide/connections`, `/guide/tables` and `/guide/settings`. `make docs` builds that site, with the same URLs ([decision 0025](decisions/0025-public-docs-and-api-reference.md)).

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
