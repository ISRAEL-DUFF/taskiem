# A second business on the web: New business, and switching between businesses

Status: **Parked** 30 October 2026. The case that led to this plan turned out to be one business selling more than one thing, which [fronts](fronts.md) answers; this plan stands for the day an owner really has two businesses. Draft, not for building yet. Revised 30 October 2026: [chat as the way in](chat-way-in.md) (merged) already built the shared function and `new business` in chat, so what is left is the web · Builds on: [onboarding](../onboarding.md) ([0022](../decisions/0022-self-serve-onboarding.md)), [field agents](../agents.md), [governance: members and invitations](../governance.md#members-and-invitations), [WhatsApp](../whatsapp.md) (`switch`, `new business`), [billing](../billing.md), [chat as the way in](chat-way-in.md) (`makeBusiness`)

## 1. Summary

An owner with a shop on Taskiem opens a restaurant and wants it on Taskiem too.

**What works today.** In chat they can make it: `new business` on WhatsApp, Telegram or Slack makes another business for a linked person, through the same function the field agent and chat sign-up use (`makeBusiness`, [chat as the way in](chat-way-in.md)), and `switch` moves the conversation between them.

**What doesn't.** On the web they cannot:
- signing up again with their email is refused ("that email already has an account");
- there is no **New business** once signed in;
- a person with two businesses still sees only one, because a session belongs to one business, sign-in picks the first, and nothing switches.

This piece adds the web's half, in two parts:

1. **Switching.** The business's name in the header becomes a menu of the person's businesses. Choosing one switches the session.
2. **New business.** **New business** at the end of the menu makes one from a two-field form, through `makeBusiness`, and opens on **Get started**.

Each business stays its own tenant: its own storefront and order form, riders, money log, members, trial and plan. Nothing is shared between a person's businesses but the person.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Business | A tenant: what an owner sees as one shop, restaurant or school. The owner's word; `tenant` stays the code's |
| Person | A user account: one email or chat identity, with its passkeys, in any number of businesses |
| Switch | Working in another of the person's businesses: on the web a new session for it; in chat the conversation's current business |

## 3. Where we are

| Exists | Where |
| --- | --- |
| A person may belong to any number of businesses (`memberships`); passkeys, TOTP and email confirmation are the person's, in every business | [governance](../governance.md#passkeys), `taskiem_auth_tenant_scope` |
| Sign-in makes a session for one business: `tenant_id` in the request, else the first in the person's list; the answer lists every business (`tenants`), which the web app ignores | `api/passkeys.go` (`createSession`, `startSession`), `web/src/auth.tsx` |
| No way to change a session's business; `GET /v1/me` names the one it is in (`tenant_name`) | `api/auth.go` (`me`) |
| Web signup makes a business and its owner at once, in its own code; an email that has an account is refused (`exists`) | `api/onboarding.go` (`signup`, `createTenant`) |
| One function makes a business for an owner who may already exist: tenant, owner membership, default workspace, `dev` and `prod`, `tenant_onboarding` (with `source` and the chat identity's digest for chat's limits), the audit entry `tenant.create`, then the trial. The field agent's sign-up, chat sign-up and chat's `new business` call it | `api/chat_signup.go` (`makeBusiness`, `newBusiness`), `api/agents.go` |
| Invitations: a business grants roles to someone who already has an account by inviting them; they accept under Account | [governance](../governance.md#members-and-invitations) |
| Chat: `switch <business>` moves the conversation between the person's businesses; `new business` makes another (the sign-up's questions without the terms and the name), and a linked person with none left is offered **Start my business** | `api/whatsapp_chat.go` (`waSwitch`), `api/chat_signup.go`, [onboarding](../onboarding.md#signing-up-from-chat) |
| The checklist and the guided first workflow for self-serve businesses (`source = 'signup'`); the email-confirmation hold is the person's (`users.email_verified_at`), not the business's | [onboarding](../onboarding.md), `api/onboarding.go` |
| Each tenant has its own trial and plan | [billing](../billing.md) |
| `tenant_onboarding.source` allows `signup`, `existing`, `agent`, `whatsapp`, `telegram`, `slack` | migrations 00184, 00266 |

## 4. What stays as it is

- **Signing up** on the web, by an agent and from chat: unchanged. This piece is for a person who already exists.
- **Chat.** `new business` and `switch` are built and unchanged.
- **Invitations** stay the way a business brings an existing person in. Making a business and inviting someone to one are different things and stay apart.
- **Isolation.** A new business is a tenant like any other: its rows under row-level security, its own secrets, keys, connections and money. The person is the only link, and a session is still in one business at a time.
- **Billing per business.** A second business starts its own trial and chooses its own plan. Whether one owner's several businesses get a shared plan or a discount is a founder's decision (§10) and changes nothing here.

## 5. Making the business

The web's **New business** calls `makeBusiness` (`api/chat_signup.go`) for the signed-in person, as chat's `new business` does. Nothing new is needed in it but a source:
- `tenant_onboarding.source` gains `web` (a business made on the web by someone who already had an account). The checklist's `self_serve` is true for it, as for `signup` and the chat sources, so a second business gets **Get started** and the guided first workflow.
- The name is checked as the web signup checks it (2 to 80 characters, no links or email addresses).
- The email-confirmation hold looks at the person, so an owner with a confirmed email is not held in their second business.

Moving the web signup onto `makeBusiness` too would leave one way to make a business. It is worth doing in O1 if it stays small, and is not needed for this plan.

**Limits.** One rule for the web and chat, which `makeBusiness` callers check before calling it:
- `TASKIEM_BUSINESSES_PER_PERSON` (default 10). Over it, the answer is "you have as many businesses as this Taskiem allows; ask support".
- Five businesses made a day per person, counted in the database from `tenant_onboarding.signed_up_by`.
- Chat's per-identity limit (C1) stays as it is.
- Every outcome is counted in `taskiem_businesses_total{outcome, via}`.

**Who may.** Any signed-in person, since owning a shop does not take a role in another business. An API key cannot, because a key is a business's. A session in enrol-only mode (an administrator who must add a passkey first) cannot until it has.

## 6. The web

### 6.1 The businesses a person is in

`GET /v1/me` gains `tenants`: `[{id, name, roles}]` for every active membership, in the order sign-in uses, so the app knows the list without a second call. The sign-in answer's `tenants` is kept.

### 6.2 Switching

`POST /v1/auth/switch {tenant_id}` makes a new session for that business for the same person and ends the current one: the membership is checked (`errNotMember` otherwise, `403`), the new session keeps the old one's `auth_method` and `signed_in_at` so step-up freshness and the passkey rule for administrators are what they were, and the cookie (or bearer token) is replaced. An enrol-only session cannot switch. Audited in both businesses as `auth.switch`. Limited per person like sign-in.

In the app, the business's name in the sidebar header (`business` in `App.tsx`, shown on every page and in the phone menu) becomes a menu when the person is in more than one business: the list, the current one marked, and **New business** at the end. Choosing one calls switch and reloads to Home. With one business the name stays plain text and the menu holds only **New business**. The last business used is kept in the browser (`localStorage`), and the sign-in page sends it as `tenant_id` when it is in the answer's list, so a person who works mostly in their restaurant does not land in the shop every morning.

### 6.3 New business

`/businesses/new`, reached from the menu and from Secrets & settings. Two fields: the business's name, and what it does (food, provisions, fashion, phones, school, salon, services, other: the same list the chat way in asks, kept on `tenant_onboarding.kind` for the templates the checklist offers first). **Create** calls `POST /v1/businesses {name, kind}`, which makes the business through §5 with `source = 'web'` and switches the session to it in the same answer (the switch of §6.2, so one call), and the app opens **Get started** there. The page follows [screens](../brand/screens.md): a form card, plain words, works at 360 px.

What a new business does not copy from the old one: nothing. Connections, members, the look, the storefront and the WhatsApp number are each business's own, and the checklist says so where it matters ("Connect Paystack for this business").

### 6.4 Accept

- A person in two businesses sees both in the header menu; switching shows the other's workflows and money, and the first's are not reachable without switching back; the audit log of each has the switch.
- **New business** makes one, lands on its Get started, and the old business is unchanged; the eleventh is refused with the message; an API key gets `403`.
- Sign-in lands in the last business used; a person in one business sees no menu, only **New business**.
- `web/e2e/businesses.spec.ts`; the onboarding and agent tests unchanged; the pages pass axe in both themes and the plain-words test.

## 7. Chat

Built, and unchanged by this plan: `new business` and `switch` on WhatsApp, Telegram and Slack ([onboarding: another business](../onboarding.md#signing-up-from-chat)). The only change is that chat's `new business` checks the per-person limits of §5, so the web and chat share one count.

## 8. Security

| Concern | Control |
| --- | --- |
| A switch into a business the person is not in | The membership is checked in the switch; the new session is made by the same function sign-in uses, so row-level security, roles and the passkey rule apply as at sign-in |
| Step-up freshness after a switch | The new session carries `auth_method` and `signed_in_at` from the old; a passkey proof given in one business is not a proof in another, since step-up records are per session and the session is new |
| A person making businesses to spend trials or to flood | Per person total and per day, counted in the database; every outcome counted; the audit entry names the person in the new business and the way it was made |
| A key or an embed app making businesses | Only a person's session or a linked chat identity may; keys and embed sessions get `403` |
| Data crossing between a person's businesses | Nothing is copied; a session is in one business; the switch is audited in both |

## 9. Testing

- **API**: `POST /v1/businesses` makes the rows `makeBusiness` makes for chat (one table of expectations, `source = 'web'`); switch refuses non-members and enrol-only sessions and keeps freshness; the limits, shared with chat's `new business`; `me.tenants`.
- **Web** (Playwright): the accepts of §6.4.
- **Chat**: `new business` refused past the shared limits.
- **Unchanged**: `web/e2e/onboarding.spec.ts`, `api/agents_test.go`, the agent's app.

## 10. Needs a person

| Item | Who | Blocks |
| --- | --- | --- |
| Whether an owner's several businesses share a plan or get a discount ([billing](../billing.md)); today each has its own trial and plan | Founder | Nothing |
| The default for `TASKIEM_BUSINESSES_PER_PERSON` in production | Founder | Nothing: 10 until said otherwise |

## 11. Milestones

| Id | Milestone | Size |
| --- | --- | --- |
| O1 | `source = 'web'`, `tenant_onboarding.kind`, the per-person limits shared with chat and the metric, `POST /v1/businesses` over `makeBusiness`, `GET /v1/me` with `tenants`, `POST /v1/auth/switch` | S |
| O2 | The web: the header menu and switching, the last business remembered at sign-in, the New business page and Get started there, the e2e | S |
| O3 | Docs: [onboarding](../onboarding.md) (a second business on the web), [governance](../governance.md) (sessions and switching), the owners' guide ("Your second business") | S |

One branch and one PR for O1 to O3.

## 12. Out of scope

Merging two businesses or moving a workflow, table or storefront between them; a shared plan across businesses (§10); one storefront for two businesses (a business has one); anything in chat, which is built ([chat as the way in](chat-way-in.md)); a group or holding view across businesses (totals for all of an owner's shops) which the daily summary could grow later.
