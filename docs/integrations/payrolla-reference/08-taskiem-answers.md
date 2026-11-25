# Taskiem's answers to Payrolla's questions (Phases A, B and C)

> **Draft for the founder to send.** Fill in `[STAGING HOST]` and `[YOUR NAME]` before sending. The client secret must come back through a secrets manager or a shared vault, never by email.

Taskiem → Payrolla · in reply to `05-phase-a-response.md`, `06-phase-b-response.md` and `07-phase-c-response.md`

Thank you. You've shipped everything we asked for, and the code flow is better than what we proposed. Taskiem will run payroll end to end in its own screens and chat on your Connect API. Payrolla stays the only payer. Our plan is in five parts, the first being one-step connect; we start as soon as you've registered us.

Below are answers to every question you asked across the three phases, and a few short questions of ours.

## 1. Redirect URIs

They must match exactly, so here they are in full:

| Taskiem | Redirect URI | Register it in |
|---|---|---|
| Production | `https://app.taskiem.com/v1/payrolla/callback` | Your **production** deployment (live connections) and your **sandbox** (test connections) |
| Testing (our staging) | `https://[STAGING HOST]/v1/payrolla/callback` | Your **sandbox** only |

- **Why production twice:** a business on Taskiem chooses **Live** or **Test** when it connects Payrolla, and Test connections go to your sandbox. So our production needs a registration in your sandbox as well.
- **Two pairs of credentials:** please give us one `client_id` and `client_secret` for each of your deployments. Send the secrets through a secrets manager or a shared vault, as you said.

## 2. Scopes to register

Please register Taskiem for:

```
payroll:read employees:read employees:write payroll:write payroll:approve payslips:read payslips:links self_service reports:read webhooks:manage
```

- **What we added:** `payslips:links`. Today's payslip workflows use it to send a payslip to someone who isn't on Taskiem.
- **What we left out:** `embed:launch`, because we won't embed Payrolla's screens.
- **What we ask for:** all of these when an owner connects. If the owner allows fewer, Taskiem turns off what needs the missing ones and tells them which permission to allow, in your own words ("Approve and pay").

## 3. Refresh tokens and long-lived connections

Yes, we need refresh tokens. Your design suits us as it is:
- **Refreshing:** we refresh under a lock on the connection, so only one refresh token is ever spent at a time.
- **Storing:** we store the new refresh token before we use the new access token.
- **Ending:** a refused refresh (`invalid_grant`) or `company.disconnected` ends the connection in Taskiem. The owner then sees **Reconnect Payrolla**.
- **Disconnecting:** when an owner disconnects in Taskiem, we call `/oauth/revoke` with the current refresh token.

## 4. Companies already connected with a key

- **They keep working.** We won't force anyone over.
- **The owner sees "Reconnect Payrolla"** on Taskiem's Payroll page and moves over in one step, through the OAuth flow.
- **We switch to the tokens** at once, and **set up the webhooks again** ourselves on the new connection.
- **We then ask the owner to revoke the old key** in Payrolla → Settings → Connected apps, and say where.
- **Approvers link again**, once each, with one code. Taskiem shows each of them "Link your Payrolla account" the next time they open payroll.

Some features need the OAuth connection's new scopes, such as approving. On a key connection, an owner who taps one sees "Reconnect Payrolla to do this here". Until then, that key keeps using `approval_url` as today.

If you can, please tell owners of key-connected companies that one-step connect exists, and link them to Taskiem's Payrolla connection page.

## 5. `X-Acting-User`

**Yes, it is stable for the life of the person.**

- **What it is:** Taskiem's id for the person's own account, sent as `tsk_user_<uuid>`. It is not their membership in one business.
- **Every business:** a person has the same id in every business they work in.
- **Coming back:** if they leave a business and come back, the id is the same.
- **Never reused:** an id is never given to anyone else.
- **New accounts only:** the id changes only if the person makes a new Taskiem account. You'd then see a new id, with the same name in `X-Acting-User-Name`.
- **`external_ref`:** when Taskiem adds an employee who is also a Taskiem user, it sends the same `tsk_user_<uuid>`. So their self-service requests carry an acting user that matches their `external_ref`.

## 6. The two-step code flow

**It fits our screens and our chat.** We'll take the code in three places:
- **Taskiem's web and phone app:** a code sheet, after the person taps Approve, Lock and pay, Pay now, and the rest.
- **The Taskiem thread on WhatsApp or Telegram:** the approval card has an Approve button. Taskiem asks you for a code and says "Payrolla sent a code to your phone ending 4567. Type it here". The person types the six digits into the thread.
- **Slack:** a link to the web's code sheet. Slack threads are often shared, so we won't take a code there.

**What Taskiem does with the code:**
- **Sent once:** it goes to you, once, with the action.
- **Never kept:** it isn't stored, logged or shown again, and our AI never sees it.
- **Retries:** each attempt at a code has its own `Idempotency-Key`. A retry after a timeout reuses it.

**One request: the wording of the code message.** Your messages end "Never share it", and people are rightly taught not to type a code into a chat. For a code Taskiem asked for, could the message instead say where it goes? For example:

> Your Payrolla code to approve November 2026 payroll (₦9,200,000, 48 people) is 482910. Type it into Taskiem only. Never tell it to anyone, even Payrolla or Taskiem staff. It expires in 5 minutes.

You know which challenges came from a connected app, and you have its name, so the message could name it. We'd like the same for linking, bank-change approvals and employee self-service codes.

If you'd rather keep one wording, tell us. We would then take codes only in our web and app screens and in WhatsApp's secure form, and not in the thread.

## 7. Short-wallet alerts

**The event is enough.**
- **On the day:** on `run.awaiting_funds`, Taskiem tells the owner and the approvers in their chat how much is short.
- **Earlier:** Taskiem warns from `GET /connect/funding`, three days and one day before the pay date, for any approved or locked run the wallet doesn't cover.

We need nothing else from you here.

## 8. Pay date

**A locked run is enough.** Taskiem shows locking as "Lock and pay", says that Payrolla pays on the pay date, and says that a locked payroll can't be unlocked. We'll never call anything on the pay date itself.

## Questions from us

1. **Separation of duties for runs made through the API.** Your two notes say different things:
   - **Phase A:** a run made with `POST /connect/runs` counts as prepared by "Taskiem (connected app)", so any approver may approve it.
   - **Phase B:** separation of duties includes the Taskiem person who made it, acting as their linked Payrolla user.

   Is Phase B's rule the one in force? And does a run made by a Taskiem person who isn't linked count as the app's?
2. **The OpenAPI file.** Our copy is from 23 October. We'll build from `{{PAYROLLA_API}}/connect/openapi.yaml`. Does it already cover all of Phases A to C, including the OAuth endpoints and every error code in your notes?
3. **`NO_PAYOUT_DESTINATION`.** Does the error say who has no account, or should we find them from the run's lines?
4. **A bank account the bank can't find,** when an employee is added or changed: which error code do we get?
5. **`CODE_INVALID`.** Can it say how many tries are left, so we can tell the person?
6. **Sandbox credentials.** Can the sandbox registration be ready before production, so we can build and test against it first?

[YOUR NAME]
Taskiem
