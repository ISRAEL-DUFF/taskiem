# Embedding Payrolla

Show Payrolla's payroll screens inside your own product. Your users run, review, approve and pay payroll without leaving it. The embed is a Payrolla page in an `<iframe>`; a small SDK on your page handles the handshake.

**Base URL:** `https://synledger.name.ng/payrolla/api/v1` (for example `https://synledger.name.ng/payrolla/api/v1`). Every path below is relative to it.

One guide covers all three kinds of host. Only the launch call and one SDK option differ:

| Host | Who | Credential | Launch call | SDK option |
|---|---|---|---|---|
| **Platform** | A product that hosts payroll for many companies | Platform API key + secret | `POST /platform/launch-token` | `platformId` |
| **Connected app** | An app one company uses, e.g. Taskiem | Company API key (`pyk_live_…`) with `embed:launch` | `POST /connect/embed/launch-token` | `hostId` |
| **Own product** | A company's own software | Company API key with `embed:launch` | `POST /connect/embed/launch-token` | `hostId` |

Connected-app and own-product hosts are "company hosts". They are set up per company by that company's owner, and everything below works the same for both.

## 1. Owner setup (company hosts)

The company owner sets this up in Payrolla, under **Settings → Connected apps → the app → Embedding**:

| Setting | Meaning |
|---|---|
| **Where it may appear** | The origins allowed to frame Payrolla, e.g. `https://app.taskiem.com`, or `https://*.example.com`. Empty means embedding is off. |
| **What it may show** | Full payroll app, single screens, or both; and which screens. |
| **Who can sign in** | Only people already on the Payrolla team (matched by email), or also new people, added as at most Viewer, Approver or Admin. Never Owner. |
| **Approving and paying** | **Confirm with a code** (default) or **Trust the app's sign-in**. See [§5](#5-approving-and-paying-inside-the-embed). |
| **Branding** | Display name, logo and main colour. |

Platforms set their origins and branding through `PUT /platform/me`.

## 2. Launch: your backend

Each time you show the embed, your **backend** asks Payrolla for a short-lived, single-use launch token for the person who is signed in to your product. Never call this from the browser: it needs your key.

**Company host**

```http
POST /connect/embed/launch-token
Authorization: Bearer pyk_live_…
Content-Type: application/json

{
  "scope": "FULL",
  "user": { "external_ref": "u_42", "email": "ngozi@acme.ng", "name": "Ngozi Eze", "role": "APPROVER" }
}
```

```json
{ "data": { "session_token": "…", "expires_at": "2026-10-22T10:05:00Z", "host_id": "hst_4f9a…" } }
```

**Platform**

```http
POST /platform/launch-token
Authorization: Bearer <platform api key>
X-Platform-Secret: <platform secret>

{ "company_id": "…", "scope": "FULL", "user": { … } }
```

| Field | |
|---|---|
| `scope` | `FULL` (the whole app) or `LAUNCH_CONTEXT` (one screen; see below). Default `FULL`. |
| `context` | For `LAUNCH_CONTEXT`: `screen` is `run-review`, `team`, `payslip` or `payout`. Add `run_id` for `run-review` (optional) and `employee_id` for `payslip` (required). |
| `user` | **Required for company hosts.** The person signed in to your product. |
| `ttl_seconds` | 1–600, default 300. The token only needs to live until the frame loads. |

**Who the user becomes.** Payrolla matches `user.external_ref` first, then `user.email` within the company.

- **Unknown person:** if the owner allows adding people, they are added with the lower of `role` and the owner's maximum. If not, the frame says the person isn't on the company's Payrolla team and should ask the owner for an invite, and it sends `EMBED_ERROR`.
- **Existing person:** they keep the role they already have. A launch never raises it.
- **`external_ref`:** use an id that never changes. For company hosts it is kept separately per key, so two apps can't collide.

**Errors to handle**

| Code | Meaning |
|---|---|
| `EMBED_NOT_CONFIGURED` | The owner hasn't added any allowed origin. |
| `EMBED_MODE_NOT_ALLOWED` / `EMBED_SCREEN_NOT_ALLOWED` | The owner hasn't allowed that mode or screen. |
| `EMBED_HOST_REVOKED` | The key was revoked or embedding was switched off. |
| `LAUNCH_USER_REQUIRED` | No `user` was given. |
| `INVALID_LAUNCH_CONTEXT` | Missing or unknown screen, or an invalid id. |

## 3. Mount: your page

Load the SDK — about 2 KB, no dependencies — from Payrolla:

```html
<script src="https://synledger.name.ng/payrolla/widget/sdk/payroller-embed.umd.js"></script>
<!-- window.PayrollerEmbed.mount(…) -->
```

or as an ES module from `https://synledger.name.ng/payrolla/widget/sdk/payroller-embed.mjs`, then mount it into an element:

```js
import { mount } from 'https://synledger.name.ng/payrolla/widget/sdk/payroller-embed.mjs'

const embed = mount(document.getElementById('payroll'), {
  embedUrl: 'https://synledger.name.ng/payrolla/api/v1/embed',
  hostId: 'hst_4f9a…',            // company hosts; platforms pass platformId instead
  mode: 'full',                    // 'full', 'launch' or 'payout'
  getSessionToken: async () => {
    const r = await fetch('/api/payrolla/launch-token', { method: 'POST', credentials: 'include' })
    return (await r.json()).session_token
  },
  theme: { primary: '#0B6E4F' },   // optional; any CSS colours
  onReady: () => {},
  onError: (message) => showError(message),
  onClose: () => closePanel(),
  onEvent: (type, payload) => {},  // e.g. EMBED_NAV
})

// later
embed.setTheme({ primary: '#7C3AED' })
embed.destroy()
```

`hostId` isn't secret; it is the `host_id` from the launch response. The frame is served with a `frame-ancestors` policy listing your allowed origins only, so Payrolla can't be framed anywhere else.

**What happens:**
1. The frame loads and asks for a session.
2. The SDK calls your `getSessionToken`.
3. The frame exchanges the token, checking your page's origin.
4. The person is signed in to Payrolla for this company, inside the frame.

Session tokens are single use. Call `getSessionToken` fresh each time; don't cache it.

### Modes and screens

| SDK `mode` | Launch `scope` | Shows |
|---|---|---|
| `full` | `FULL` | The whole payroll app: runs, team, payslips and settings, limited to what the person's role allows. |
| `launch` | `LAUNCH_CONTEXT` | One screen: `run-review` (review, approve and pay one run), `team`, or `payslip` (one employee's payslips). |
| `payout` | `LAUNCH_CONTEXT` with `screen: "payout"`, or `FULL` | Pay a list of people — contractors, agents, vendors — from the company wallet. |

The screens a session may reach are fixed at launch and enforced by Payrolla's server, not just hidden in the page.

### Payout mode

The person adds people one at a time or pastes rows from a spreadsheet (`account number ⇥ bank ⇥ amount`).

1. **Accounts:** each account is checked with its bank. The name shown is the bank's, never a typed one.
2. **Review:** Payrolla previews the batch, including whether the wallet covers it.
3. **Confirm:** the person confirms that exact list, with a code under the Verified policy. Change an amount after confirming and the confirmation no longer applies.
4. **Follow:** the frame then shows each payment until it settles. Failed payments can be retried; a retry resends the same payments, so it needs no new code.

Only people allowed to pay (Owner and Approver roles) can submit. The owner must allow the **Pay people** screen for a `LAUNCH_CONTEXT` payout launch.

Payout mode doesn't offer tax withholding. Use Payrolla itself for payees who need it.

The frame sends `EMBED_PAYOUT_SUBMITTED` `{ batch_id, total_kobo, payee_count }` when the batch is accepted. It sends `EMBED_PAYOUT_COMPLETE` `{ batch_id, status, paid_count, failed_count }` when it settles.

### Messages from the frame

| Message | Payload | When |
|---|---|---|
| `EMBED_READY` | `{ mode }` | Signed in and showing |
| `EMBED_NAV` | `{ screen }` | The person moved to another screen |
| `EMBED_ERROR` | — | The session couldn't start: an expired or used token, an origin that isn't allowed, a revoked host, or a person who isn't on the team. The frame shows the reason itself. |
| `EMBED_CLOSE` | — | The person closed the embed |
| `EMBED_PAYOUT_SUBMITTED` | `{ batch_id, total_kobo, payee_count }` | Payout mode: the batch was accepted |
| `EMBED_PAYOUT_COMPLETE` | `{ batch_id, status, paid_count, failed_count }` | Payout mode: the batch settled |

The SDK sends `EMBED_SESSION`, `EMBED_MODE` and `EMBED_BRANDING` to the frame for you.

## 4. Branding

For company hosts, the name, logo and colour the owner set in Payrolla apply automatically. They come from Payrolla when the session starts, so changing them needs no new tokens.

`theme` in the SDK can also re-colour the frame to follow your product's light or dark mode.

## 5. Approving and paying inside the embed

Every payroll action works inside the frame: approve, lock, pay, retry, vendor payments, and payouts to a list of people. As in Payrolla, each one needs a fresh confirmation from the person doing it. Separation of duties, role checks and the frozen-at-lock rules all apply as usual.

How that confirmation works depends on the host and on the owner's choice:

| Host | Setting | The person confirms with |
|---|---|---|
| Platform | — | Nothing extra. Platforms are trusted hosts. |
| Company host | **Confirm with a code** (default) | A one-time code sent to their own phone or email, or from their authenticator app |
| Company host | **Trust the app's sign-in** | Nothing extra |

### Confirm with a code

When the person approves, Payrolla shows a sheet inside the frame, for example "Enter the code we sent to •••• 4567 to approve September payroll":
- if they have an authenticator app, they enter a code from it, and nothing is sent;
- otherwise Payrolla texts or emails a 6-digit code.

Codes last 5 minutes and allow 5 attempts, and sends are rate-limited. A code works only for that action on that run.

The code goes only to something set up **in Payrolla, outside the embed**:
- an authenticator app or verified email on their Payrolla account;
- or a phone number the owner added and confirmed.

Your product can't set, change or see it. Not even the email you send in the launch counts. That is the point: if your product, or someone's account in it, is compromised, payroll still can't be approved or paid.

### When someone can't confirm yet

People added through the embed usually have no factor at first. When they try to approve, they see: "Payment confirmation isn't set up for you. Ask your Payrolla owner to add your phone number." Everything else keeps working.

The owner fixes this once, in **Payrolla → Settings → Connected apps → Payment confirmation**:
1. They pick the person and enter their phone.
2. They confirm with their own step-up.
3. They enter the code the person receives.

The Embedding tab also lists everyone who can't confirm yet, with a **Set up** link. None of this can be done from inside an embed.

**Changing or removing a payment phone:**
- A new number takes effect only once its code is entered. Until then the current phone keeps working.
- When a change or removal takes effect, the person is told by SMS on the old phone and by email. If they didn't expect it, they find out somewhere the new phone can't hide it.

There is also an alternative to approving in the frame: the `approval_url` in `run.computed` (and from `GET /connect/runs/{id}/approval-link`) opens the run in Payrolla itself.

### Trust the app's sign-in

The owner can choose this per app, behind a warning and a step-up, and the choice is audited. Payrolla then treats your product's sign-in as enough, the same as for a platform. Ask the owner for it only if your product already has strong sign-in for the people who approve payroll.

## 6. Revocation

- **New launches:** revoking a key, removing all origins or switching embedding off makes new launches and exchanges fail immediately.
- **Open sessions:** sessions already open end on their next request.

## 7. Checklist

- [ ] The launch call runs on your server; the key never reaches the browser.
- [ ] `user.external_ref` is stable; `email` is the person's real address.
- [ ] Your production origin is in the owner's allowed origins.
- [ ] `onError` handles a session that couldn't start, e.g. by offering to retry or to ask for an invite.
- [ ] Approvers have payment confirmation set up, or the owner has knowingly chosen to trust your sign-in.

See also: [`payrolla-connect-v1.yaml`](payrolla-connect-v1.yaml) for the launch call's exact schema, and [`webhooks.md`](webhooks.md) to react to runs and payouts.
