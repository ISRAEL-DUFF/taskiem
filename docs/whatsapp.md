# WhatsApp

Taskiem's own WhatsApp number is a client of the platform (spec 11): people who link their number to their account approve requests with buttons, ask what ran and what failed, and start workflows from a chat. Every action goes through the same permissions, approval policies, plan limits and audit log as the web app. This is milestone A1 of [Phase 3](phase-3-status.md).

This is separate from the [`whatsapp@1` connector](integrations/whatsapp.md), which tenants use inside their own workflows with their own WhatsApp Business credentials.

Built from Meta's public WhatsApp Cloud API documentation only (developers.facebook.com/docs/whatsapp/cloud-api: messages, interactive reply buttons, templates, authentication templates, webhooks and their signatures), read 6 October 2026, under the [clean-room policy](clean-room-policy.md).

## For operators: setting up the platform number

1. In the Meta App Dashboard, create a Business app with the WhatsApp product, linked to Taskiem's WhatsApp Business Account. Business verification is needed for production volumes ([needs people](needs-people.md#phase-3), W1).
2. Register the platform number and note its **Phone number ID**.
3. Create a system user with `whatsapp_business_messaging` and generate a token for it.
4. Copy the app's **App secret** (App settings > Basic). Choose a **verify token** (any random string).
5. Submit the [templates](#templates) for approval (W2). Until they are approved, only replies inside a person's 24-hour window go out.
6. Put the settings in the deployment's Secret (`existingSecret` in the Helm chart); never in chart values or the database:

   | Variable | |
   | --- | --- |
   | `TASKIEM_WHATSAPP_PHONE_NUMBER_ID` | Turns the channel on |
   | `TASKIEM_WHATSAPP_ACCESS_TOKEN` | The system user's token |
   | `TASKIEM_WHATSAPP_APP_SECRET` | Verifies webhook signatures |
   | `TASKIEM_WHATSAPP_VERIFY_TOKEN` | Answers Meta's subscription handshake |
   | `TASKIEM_WHATSAPP_TOKEN_KEY` | 32 bytes, base64 (`openssl rand -base64 32`): signs decision tokens. Same value on every `api` and `edge` pod. Rotating it voids buttons already sent |
   | `TASKIEM_WHATSAPP_DISPLAY_NUMBER` | The number as people should save it, shown on the Account page (not secret) |
   | `TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE` | Language code the templates were approved in (`en`) |
   | `TASKIEM_WHATSAPP_DEFAULT_COUNTRY` | Calling code for numbers typed with a leading 0 (`234`) |
   | `TASKIEM_WHATSAPP_GRAPH_URL` | Tests only: a fake Graph API. A loopback IP address here is the only way the platform reaches loopback through the egress guard |

   With `TASKIEM_WHATSAPP_PHONE_NUMBER_ID` set, the others (but the last four) are required and the process refuses to start without them.
7. In the App Dashboard > WhatsApp > Configuration, set the **Callback URL** to `<public URL>/channels/whatsapp` and the **Verify token**, then subscribe to the **messages** field. The ingress sends `/channels/` to the `edge` role (with `--role all`, the API serves it).
8. `TASKIEM_PUBLIC_URL` must be set: links in messages (runs, step-up) point at it.

Which role does what:

| Role | WhatsApp work |
| --- | --- |
| `edge` | `GET /channels/whatsapp` (handshake) and `POST /channels/whatsapp` (messages): verifies, deduplicates and answers each message |
| `api` | Account > WhatsApp (binding), the step-up page, and the notifier: approval requests to approvers, and how runs started from chat ended (every 5 seconds) |
| `scheduler` | Alerts to WhatsApp alert channels |

Outbound messages go to `graph.facebook.com` through the egress guard. Sends are not retried: a message that fails is recorded (an approval request's error is kept with it) and the person can ask again (`approvals`).

## For people: using Taskiem on WhatsApp

**Link your number.** Under Account > WhatsApp, enter your number with its country code and confirm it is you (passkey, authenticator code or password, as when adding a passkey). Taskiem sends a 6-digit code to that number on WhatsApp: type it on the page, or send it back to Taskiem's number. The code works for 10 minutes; five wrong codes spend it, and at most three codes are sent an hour. A number can be linked to one account, and an account has one number. Unlink it on the same page.

**What you can send:**

| Message | What happens |
| --- | --- |
| `help` | What you can do here, given your roles |
| `status`, `what failed today?` | Runs in the last 24 hours by status, the latest failures, and approvals waiting for you (needs `run.read`) |
| `run <workflow>` | Matches the name of a workflow deployed in `prod` (or its id); asks each required input in turn, checked against the workflow's input schema; then shows exactly what will run and waits for **yes**. Needs `run.start`; plan limits apply. You hear how the run ended |
| `approvals` | Requests waiting for you, sent again with fresh buttons (three at most) |
| `switch <organisation>` | Work in another organisation you belong to; `switch` alone lists them |
| `cancel` | Drops what is in progress |

Every message from Taskiem starts with the organisation's name in brackets: the number is shared by every organisation on this Taskiem.

**Approving.** When a request needs your role, Taskiem sends its details with **Approve** and **Reject** buttons. A button works once, only from your number, for 24 hours or until the request times out. The same rules as in the web app apply: you cannot approve what you started, wrote or published; with distinct approvers you approve one level at most; a delegation lets you cover for someone. When the policy needs step-up, Taskiem sends a link instead: open it (signed in to Taskiem in that organisation) and confirm with your passkey or authenticator code within 10 minutes. The decision is then recorded, and Taskiem confirms it in the chat.

**What messages never show.** Secrets, and personal data unmasked: account and card numbers show their last four digits, phone numbers their country code and last four, BVN and NIN not at all, names their first letter, and anything under a field named like a credential is hidden. The full details stay in the web app, behind its permissions.

## Templates

WhatsApp only delivers free-form messages within 24 hours of the person's last message. Outside that window Taskiem sends a template, so every notification type has one. They are defined in code (`engine/whatsapp/templates.go`) and must be submitted to Meta under these names, in the language of `TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE`, with these bodies and buttons:

| Name | Category | Body | Buttons | Sent when |
| --- | --- | --- | --- | --- |
| `taskiem_otp` | Authentication | `{{1}} is your verification code. For your security, do not share this code. This code expires in 10 minutes.` (Meta's authentication format, with its security recommendation and expiry options) | Copy code | Binding a number |
| `taskiem_approval_request` | Utility | `[{{1}}] Approval needed: {{2}}. {{3}} Environment: {{4}}. Tap Approve or Reject, or decide in Taskiem.` | Quick replies **Approve**, **Reject** (the payloads are decision tokens) | An approval is waiting for an approver whose window is closed |
| `taskiem_stepup_link` | Utility | `[{{1}}] This decision on {{2}} needs your passkey or authenticator code. Confirm it in Taskiem within 10 minutes: {{3}}` | — | Step-up (normally inside the window, since the person just tapped a button) |
| `taskiem_run_failed` | Utility | `[{{1}}] {{2}} failed in {{3}}. Details: {{4}}` | — | Alert rule `run_failed`; a run started from chat failed |
| `taskiem_run_completed` | Utility | `[{{1}}] {{2}} completed in {{3}}. Details: {{4}}` | — | A run started from chat completed |
| `taskiem_needs_reconciliation` | Utility | `[{{1}}] {{2}} needs reconciliation in {{3}}: a call's outcome is unknown and someone must check with the provider. Details: {{4}}` | — | Alert rule `needs_reconciliation` |
| `taskiem_approval_waiting` | Utility | `[{{1}}] {{2}} Details: {{3}}` | — | Alert rule `stuck_approval` |
| `taskiem_alert` | Utility | `[{{1}}] {{2}} Details: {{3}}` | — | Every other alert rule |

Variables are filled with one-line text (Meta refuses new lines in them): the request summary in `taskiem_approval_request` joins its masked fields with semicolons. Meta charges per template message; template cost accounting is milestone A2.

## Alerts to WhatsApp

An alert channel of kind **WhatsApp** names members by email (Alerts > Channels). Each alert goes to those of them who have linked a number: as text when they wrote in the last 24 hours, otherwise as the template for the rule's kind. A delivery fails, with the reason, when none of them has a number. Retries resend to every member of the channel.

## Security model

| Concern | Control |
| --- | --- |
| Forged webhooks | `X-Hub-Signature-256` (HMAC-SHA256 of the raw body under the app secret) is checked before parsing, in constant time; the handshake answers only with the right verify token, and echoes only an alphanumeric challenge. Messages for another phone number id on the same app are ignored |
| Retried deliveries | Each message id is claimed in the database before it is acted on (at most once: a command is never run twice; a crash mid-message loses that message, and the person sends it again) |
| Who is speaking | A message speaks for the person whose binding holds the sender's number, in the organisation the number is working in, with that person's roles and permissions there at that moment. A disabled person, or one who left the organisation, is refused. Unbound numbers get a short reply only (hook: `api.Server.WhatsAppPublic`, for public self-service menus later) |
| Binding someone else's number | The code goes to the number; binding also needs proof of the account (a passkey, authenticator code or password). A number belongs to one account |
| Guessing a code | 6 digits, 10 minutes, five wrong guesses, three codes an hour and a few per number; stored only as a SHA-256 of number and code |
| Forged or altered buttons | Decision tokens: HMAC-SHA256 under `TASKIEM_WHATSAPP_TOKEN_KEY` over the tenant, nonce, purpose, decision, expiry, run, step, approval level and approver, the last four kept in the tenant's `whatsapp_tokens` row under the nonce; a token verifies only with both the key and the row |
| Replay | A token's row is marked used when tapped; the Approve and Reject of one request share a notice, so either spends both. Hand-off links are used once |
| A button forwarded or tapped by someone else | The tapping number must be bound to the approver the token names; refusals are audited (`approval.token.refused`) |
| Approving what you should not | The decision is `runtime.Store.VoteApproval`, the web app's path: role or delegation, makers cannot approve, distinct approvers, levels; a refusal is audited (`approval.decide.refused`) |
| Step-up | Never satisfied in chat (a WhatsApp Flows PIN is milestone A2): the hand-off link carries a separate 10-minute, single-use token in the URL fragment (kept out of logs and `Referer`); the page needs a signed-in session as the same person in the same organisation, then a passkey assertion or TOTP code for that exact decision |
| A compromised phone | Per-number limits: 20 messages then one every 3 seconds (one warning a minute), three run starts then one every 20 seconds, and every start needs an explicit **yes** after a summary of exactly what will run. Unlinking the number in the web app ends it |
| Personal data in messages | Masked as above; free text passed through the PII redactor; inputs typed in chat are sealed (`x-pii` fields) while they wait for confirmation |
| Tenant isolation | Bindings, codes, the window and message ids are a person's, not a tenant's: no tenant reads them; functions do one narrow thing each. Conversation state, tokens, notices and run watches are tenant data under forced row-level security, read only with the conversation's current organisation (or the token's) in scope |
| Audit | `whatsapp.code_sent`, `whatsapp.bind`, `whatsapp.unbind` (in every organisation the person belongs to, with the number masked), `approval.decide` with `channel: whatsapp` and `via: button` or `handoff`, `run.start` with `channel: whatsapp`; the approval's history records the channel |

Limits are per `edge` replica (in memory), like the sign-in limiter.

## Not yet (milestone A2 and later)

- WhatsApp Flows forms for inputs and a PIN for step-up inside WhatsApp.
- Tenants' own numbers (embedded signup as, or through, a Business Solution Provider). The platform client is one `whatsapp.Platform`; an own number becomes a second platform keyed by phone number id, chosen per tenant when sending and by `metadata.phone_number_id` when receiving.
- Template cost accounting against plan allowances.
- Public self-service menus for unbound numbers (`WhatsAppPublic`), languages beyond English, voice notes (A4), building workflows by chat (B4).
