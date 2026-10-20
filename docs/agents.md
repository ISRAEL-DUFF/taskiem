# Field agents

A **field agent** is a person Taskiem has checked and registered who signs small businesses up in person: they sit with the owner at the shop or stall, make the business's page with them, take a photo of the place, and leave the owner running it from WhatsApp. This page is what is built (Phase 5, S5); the decisions are [0036](decisions/0036-storefronts.md) (9, 15 and 16).

An agent never holds an account in the businesses they help. They sign in as themselves and work in a business only through a **session** the owner agreed to, for a limited time, on the page and its tables only.

## Registering an agent (operators)

Operators register agents in the [operator console](operator-console.md), after checking them in person:

| | |
| --- | --- |
| Email, name, phone, region | The agent's own. A new person is emailed a link to choose their password (good for three days); someone who already has a Taskiem account keeps it |
| Checks | The ID seen (`id_type`, and `id_checked`, which must be true) and a referee's name and phone |

The agent gets a code such as `AG-7KQ2M`, printed on their card, and a workspace of their own where they land when they sign in (as a viewer, so never an administrator). Every registration, suspension, reactivation and retirement needs the operator's passkey and is in the platform audit chain. Suspending or retiring an agent ends their open sessions at once; a retired agent stays retired.

The console lists the agents with what they have done: businesses signed up, pages published, businesses visited and verified, and sessions open now, and each agent's businesses.

## Signing a business up

1. **The agent enters** the business's name, the owner's name and the owner's WhatsApp number (`POST /v1/agent/businesses`). The business is made there and then, with the owner as its owner. An owner with no Taskiem account gets one, with no password: they work through WhatsApp.
2. **The owner agrees** by sending `AGREE` and the 6-digit code on the agent's screen to Taskiem's WhatsApp number, from their own phone. The agent's app shows a link (or its QR code) that opens WhatsApp with the message written. The code works for 30 minutes; five wrong codes lock it, and the agent can make a new one.

   This proves the owner holds the number and agrees, without the owner ever reading a code to anyone: Taskiem's own codes always say never to share them, and this keeps that true.
3. **The agent works** on the page for the next 24 hours: the storefront, its catalogue table, photos and opening hours, sending `X-Taskiem-Agent-Session: <session id>` with their own sign-in.
4. **The agent records the visit** (`POST /v1/agent/sessions/{id}/attest`): a photo of the shop or stall and where it was taken. The photo is kept in the business's private storage, cleaned of its metadata. The business is then **agent onboarded** (one of the verification levels, beside a confirmed phone and a verified business).
5. **Publishing ends the session.** The owner gets a WhatsApp message with the page's address and how to change it: send `MY PAGE`.

## What an agent may do in a business

In a session, an agent may use the storefront's and the data tables' API: the page, its catalogue and photos, its actions and booking settings, and the tables behind it. They may not:

- see customers' orders or bookings, or refund anything;
- export a table, or reach the storage project;
- manage members, connections, secrets, roles or anything else in the organisation;
- reveal personal data.

Everything the agent does is audited in the business's chain as `agent:<code>`, beside who they are.

## The owner's side

- **On WhatsApp,** `STOP AGENT` ends every agent session on the businesses the owner owns.
- **In the app,** the owner (or anyone with `member.manage`) sees every agent session on the organisation (who, when, and how it ended), ends one, or **invites an agent back** for a support visit: the code on the agent's card and 1 to 8 hours (default 2). A support visit is active at once, with the same limits.

## API

| Endpoint | |
| --- | --- |
| `GET /v1/agent/me` | The signed-in agent |
| `POST /v1/agent/businesses` | Sign a business up; returns the onboarding and the consent code |
| `GET /v1/agent/sessions`, `GET /v1/agent/sessions/{id}` | The agent's sessions; poll one to see the owner agree |
| `POST /v1/agent/sessions/{id}/code` | A new consent code |
| `POST /v1/agent/sessions/{id}/attest?lat=&lng=&note=` | The visit's photo (the body) and place |
| `POST /v1/agent/sessions/{id}/end` | Leave the business |
| `GET /v1/agent-visits`, `POST /v1/agent-visits`, `POST /v1/agent-visits/{id}/end` | The owner: agent sessions, inviting one back, ending one (`member.manage`) |
| `GET /v1/ops/agents`, `POST /v1/ops/agents`, `GET /v1/ops/agents/{id}/businesses`, `POST /v1/ops/agents/{id}/{suspend,reactivate,retire}` | Operators |

A request in a session that has ended, or is not the agent's, is refused with `403` and `agent_session_ended: true`.

## Not yet

- Owners without WhatsApp: the owner agrees from WhatsApp only.
- Paying agents (commission on businesses that stay active), and agents' own performance pages.
- Owners of agent-made businesses signing in to the web app: they have no email or password until they add them.
