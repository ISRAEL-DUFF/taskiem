# A second business: New business on the web and in chat, and switching between them

Status: **Parked** 30 October 2026. The case that led to this plan turned out to be one business selling more than one thing, which [fronts](fronts.md) answers; this plan stands for the day an owner really has two businesses. Draft, not for building yet · Builds on: [onboarding](../onboarding.md) ([0022](../decisions/0022-self-serve-onboarding.md)), [field agents](../agents.md), [governance: members and invitations](../governance.md#members-and-invitations), [WhatsApp](../whatsapp.md) (`switch`), [billing](../billing.md), the chat way in plan (`chat-way-in.md`, in review on PR 134) (C1's `new business`, which this piece gives the function to)

## 1. Summary

An owner with a shop on Taskiem opens a restaurant and wants it on Taskiem too. Today they cannot: signing up again with their email is refused ("that email already has an account"), there is no **New business** anywhere once signed in, and only a field agent can make a business for someone who already has an account. If they did get a second business (an agent, or a second email), the web app would still show only one: a session belongs to one business, sign-in picks the first, and nothing switches.

This piece adds three things, each small:

1. **One function that makes a business** for a person who already exists, lifted from the field agent's sign-up, used by the agent, by the web and by chat, and later by the chat way in plan (`chat-way-in.md`, in review on PR 134) for strangers.
2. **On the web**: the business's name in the header becomes a menu of the person's businesses with **New business** at the end; choosing one switches the session; a new one is made from a two-field form and opens on **Get started**.
3. **In chat**: `new business` on WhatsApp and Telegram makes one after a **Yes**, and the conversation moves to it, as `switch` already moves between businesses.

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
| Web signup makes a business and its owner at once; an email that has an account is refused (`exists`) | `api/onboarding.go` (`signup`, `createTenant`) |
| The field agent's sign-up makes a business for an owner who may already exist: tenant, owner membership, default workspace, `dev` and `prod`, `tenant_onboarding` (`source = 'agent'`), the audit entry `tenant.create`, then the trial (`Billing.Ensure`) | `api/agents.go` (`agentNewBusiness`) |
| Invitations: a business grants roles to someone who already has an account by inviting them; they accept under Account | [governance](../governance.md#members-and-invitations) |
| Chat: `switch <business>` moves the conversation between the person's businesses; a linked person with none is told so (`wa.no_org`) | `api/whatsapp_chat.go` (`waSwitch`), [WhatsApp](../whatsapp.md#for-people-using-taskiem-on-whatsapp) |
| The checklist and the guided first workflow for self-serve businesses (`source = 'signup'`); the email-confirmation hold is the person's (`users.email_verified_at`), not the business's | [onboarding](../onboarding.md), `api/onboarding.go` |
| Each tenant has its own trial and plan | [billing](../billing.md) |
| `tenant_onboarding.source` allows `signup`, `existing`, `agent` | migration 00184 |

## 4. What stays as it is

- **Signing up** on the web, by an agent, and (planned) from chat: unchanged. This piece is for a person who already exists.
- **Invitations** stay the way a business brings an existing person in. Making a business and inviting someone to one are different things and stay apart.
- **Isolation.** A new business is a tenant like any other: its rows under row-level security, its own secrets, keys, connections and money. The person is the only link, and a session is still in one business at a time.
- **Billing per business.** A second business starts its own trial and chooses its own plan. Whether one owner's several businesses get a shared plan or a discount is a founder's decision (§11) and changes nothing here.

## 5. The shared function

`newBusiness(ctx, tx, in)` in `api/tenants.go`, in one transaction, exactly what `agentNewBusiness` does today:

- `tenants` (name, no plan yet), the owner's `memberships` row, the `Default` workspace, `dev` and `prod`, `tenant_onboarding` with `source` and `signed_up_by`;
- the audit entry `tenant.create` in the new tenant, with `via` (`web`, `whatsapp`, `telegram`, `agent`, `signup`), the person, and for chat the channel identity masked;
- after the transaction, `Billing.Ensure` starts the trial.

The agent's sign-up calls it (making the owner's account first when there is none, as now); the web signup calls it inside `createTenant`; the web and chat pieces below call it for the signed-in or linked person. The name check is the web signup's, lifted into one function (2 to 80 characters, no links or email addresses), so a business is named by the same rule whichever way it is made; the agent's 100-character check becomes this one.

`tenant_onboarding.source` gains `owner`: a business made by someone who already had an account, through the web or chat. The checklist's `self_serve` is true for `signup` and `owner`, so a second business gets **Get started** and the guided first workflow; the email-confirmation hold already looks at the person, so an owner with a confirmed email is not held in their second business, and one with a placeholder sign-in name (an agent's or chat-born owner) is held exactly as in their first.

**Limits.** `TASKIEM_BUSINESSES_PER_PERSON` (default 10): over it, "you have as many businesses as this Taskiem allows; ask support"; and five made a day per person, counted in the database. Every outcome in `taskiem_businesses_total{outcome, via}`.

**Who may.** Any signed-in person or linked chat identity: owning a shop does not take a role in another business. An API key cannot (a key is a business's). A session in enrol-only mode (an administrator who must add a passkey first) cannot until it has.

## 6. The web

### 6.1 The businesses a person is in

`GET /v1/me` gains `tenants`: `[{id, name, roles}]` for every active membership, in the order sign-in uses, so the app knows the list without a second call. The sign-in answer's `tenants` is kept.

### 6.2 Switching

`POST /v1/auth/switch {tenant_id}` makes a new session for that business for the same person and ends the current one: the membership is checked (`errNotMember` otherwise, `403`), the new session keeps the old one's `auth_method` and `signed_in_at` so step-up freshness and the passkey rule for administrators are what they were, and the cookie (or bearer token) is replaced. An enrol-only session cannot switch. Audited in both businesses as `auth.switch`. Limited per person like sign-in.

In the app, the business's name in the sidebar header (`business` in `App.tsx`, shown on every page and in the phone menu) becomes a menu when the person is in more than one business: the list, the current one marked, and **New business** at the end. Choosing one calls switch and reloads to Home. With one business the name stays plain text and the menu holds only **New business**. The last business used is kept in the browser (`localStorage`), and the sign-in page sends it as `tenant_id` when it is in the answer's list, so a person who works mostly in their restaurant does not land in the shop every morning.

### 6.3 New business

`/businesses/new`, reached from the menu and from Secrets & settings. Two fields: the business's name, and what it does (food, provisions, fashion, phones, school, salon, services, other: the same list the chat way in asks, kept on `tenant_onboarding.kind` for the templates the checklist offers first). **Create** calls `POST /v1/businesses {name, kind}`, which makes the business through §5 with `source = 'owner'` and switches the session to it in the same answer (the switch of §6.2, so one call), and the app opens **Get started** there. The page follows [screens](../brand/screens.md): a form card, plain words, works at 360 px.

What a new business does not copy from the old one: nothing. Connections, members, the look, the storefront and the WhatsApp number are each business's own, and the checklist says so where it matters ("Connect Paystack for this business").

### 6.4 Accept

- A person in two businesses sees both in the header menu; switching shows the other's workflows and money, and the first's are not reachable without switching back; the audit log of each has the switch.
- **New business** makes one, lands on its Get started, and the old business is unchanged; the eleventh is refused with the message; an API key gets `403`.
- Sign-in lands in the last business used; a person in one business sees no menu, only **New business**.
- `web/e2e/businesses.spec.ts`; the onboarding and agent tests unchanged; the pages pass axe in both themes and the plain-words test.

## 7. Chat

- `new business` (and `new business Ada Kitchen`), on WhatsApp and Telegram through the one conversation: asks the name if not given, then what it does (a list message or inline keyboard), then "Make **Ada Kitchen** on Taskiem? It gets its own storefront, money and members. **Yes** / **Cancel**". On **Yes** §5 runs with `via` the channel and `source = 'owner'`, the conversation moves to the new business as `waSwitch` does, and the reply offers the three templates that fit, as the checklist would (the same table the chat way in's C1 uses; until C1 lands, the `daily-reminder` starter and the two that fit `kind`).
- A linked person with no business (`wa.no_org`) is offered it in the same message, instead of only being told to sign up on the web.
- Who may: the linked person, in their own name; limits as §5. On a business's own number (`OwnTenant`) the command does not apply, as `switch` does not.
- Intents: `new business`, "open another shop", "add a business" in the five languages' word lists; the model layer may route to it as it routes to `switch`.
- **Accept:** a linked owner makes a second business from WhatsApp and from Telegram and `switch` lists both; **Cancel** makes nothing; the limit holds; a person with no business is offered it and gets one; the own-number case does nothing.

## 8. What the chat way in reuses

The chat way in plan (`chat-way-in.md`, in review on PR 134) C1 makes a business for a stranger: the person first, then this function with `source` the channel; its `new business` for a linked person is §7 here, with the terms step C1 adds in front. Building this first makes C1 smaller and gives the founder's case (a second business for an owner who already has one) now, without waiting for terms texts or the Meta items.

## 9. Security

| Concern | Control |
| --- | --- |
| A switch into a business the person is not in | The membership is checked in the switch; the new session is made by the same function sign-in uses, so row-level security, roles and the passkey rule apply as at sign-in |
| Step-up freshness after a switch | The new session carries `auth_method` and `signed_in_at` from the old; a passkey proof given in one business is not a proof in another, since step-up records are per session and the session is new |
| A person making businesses to spend trials or to flood | Per person total and per day, counted in the database; every outcome counted; the audit entry names the person in the new business and the way it was made |
| A key or an embed app making businesses | Only a person's session or a linked chat identity may; keys and embed sessions get `403` |
| Data crossing between a person's businesses | Nothing is copied; a session is in one business; the switch is audited in both |

## 10. Testing

- **API**: the function makes the same rows for agent, signup, web and chat (one table of expectations); switch refuses non-members and enrol-only sessions and keeps freshness; limits; `me.tenants`.
- **Web** (Playwright): the accepts of §6.4.
- **Chat**: the accepts of §7 through the WhatsApp and Telegram test harnesses.
- **Unchanged**: `web/e2e/onboarding.spec.ts`, `api/agents_test.go`, the agent's app.

## 11. Needs a person

| Item | Who | Blocks |
| --- | --- | --- |
| Whether an owner's several businesses share a plan or get a discount ([billing](../billing.md)); today each has its own trial and plan | Founder | Nothing |
| The default for `TASKIEM_BUSINESSES_PER_PERSON` in production | Founder | Nothing: 10 until said otherwise |

## 12. Milestones

| Id | Milestone | Size |
| --- | --- | --- |
| O1 | The shared function (agent and signup moved onto it), `source = 'owner'`, `tenant_onboarding.kind`, limits and the metric, `POST /v1/businesses`, `GET /v1/me` with `tenants`, `POST /v1/auth/switch` | S |
| O2 | The web: the header menu and switching, the last business remembered at sign-in, the New business page and Get started there, the e2e | S |
| O3 | Chat: `new business` on WhatsApp and Telegram, the offer to a linked person with no business, intents in five languages, the chat tests | S |
| O4 | Docs: [onboarding](../onboarding.md) (a second business), [WhatsApp](../whatsapp.md) and [Telegram](../telegram.md) commands, [governance](../governance.md) (sessions and switching), the owners' guide ("Your second business"), and the chat way in's C1 pointed at the function | S |

One branch and one PR for O1 to O4, as the smaller pieces were.

## 13. Out of scope

Merging two businesses or moving a workflow, table or storefront between them; a shared plan across businesses (§11); one storefront for two businesses (a business has one); signing up strangers from chat (the chat way in plan (`chat-way-in.md`, in review on PR 134)); a group or holding view across businesses (totals for all of an owner's shops) which the daily summary could grow later.
