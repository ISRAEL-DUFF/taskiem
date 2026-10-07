# WhatsApp

Taskiem's own WhatsApp number is a client of the platform (spec 11): people who link their number to their account approve requests with buttons, ask what ran and what failed, and start workflows from a chat. Every action goes through the same permissions, approval policies, plan limits and audit log as the web app. Inputs are filled in WhatsApp forms (Flows), approvals whose policy allows it are confirmed with a WhatsApp PIN, an organisation can use its own WhatsApp number, template messages are counted against the plan, and an organisation can offer a public self-service menu on its number. These are milestones A1 and A2 of [Phase 3](phase-3-status.md).

This is separate from the [`whatsapp@1` connector](integrations/whatsapp.md), which tenants use inside their own workflows with their own WhatsApp Business credentials.

Built from Meta's public documentation only, under the [clean-room policy](clean-room-policy.md): the WhatsApp Cloud API (developers.facebook.com/docs/whatsapp/cloud-api: messages, interactive reply buttons, templates, authentication templates, webhooks and their signatures), read 6 October 2026; and, read 7 October 2026, WhatsApp Flows ([implementing endpoints](https://developers.facebook.com/docs/whatsapp/flows/guides/implementingyourflowendpoint), [endpoint error codes](https://developers.facebook.com/docs/whatsapp/flows/reference/error-codes), [sending a Flow](https://developers.facebook.com/docs/whatsapp/flows/guides/sendingaflow), [receiving the response](https://developers.facebook.com/documentation/business-messaging/whatsapp/flows/guides/receiveflowresponse), [components](https://developers.facebook.com/docs/whatsapp/flows/reference/flowjson/components), [changelog](https://developers.facebook.com/docs/whatsapp/flows/changelogs)), [Flows encryption](https://developers.facebook.com/docs/whatsapp/cloud-api/reference/whatsapp-business-encryption) and [pricing](https://developers.facebook.com/docs/whatsapp/pricing).

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
   | `TASKIEM_WHATSAPP_FLOWS_PRIVATE_KEY` | Optional: the Flows endpoint's RSA private key (PEM), turning on [forms and the PIN](#flows) |
   | `TASKIEM_WHATSAPP_GRAPH_URL` | Tests only: a fake Graph API. A loopback IP address here is the only way the platform reaches loopback through the egress guard |

   With `TASKIEM_WHATSAPP_PHONE_NUMBER_ID` set, the first five are required and the process refuses to start without them.
7. In the App Dashboard > WhatsApp > Configuration, set the **Callback URL** to `<public URL>/channels/whatsapp` and the **Verify token**, then subscribe to the **messages** field. The ingress sends `/channels/` to the `edge` role (with `--role all`, the API serves it).
8. `TASKIEM_PUBLIC_URL` must be set: links in messages (runs, step-up) point at it.
9. For forms and the PIN: generate the Flows key pair, upload the public key to the number, and create and publish the [two Flows](#flows) with the endpoint `<public URL>/channels/whatsapp/flows`.

Which role does what:

| Role | WhatsApp work |
| --- | --- |
| `edge` | `GET /channels/whatsapp` (handshake) and `POST /channels/whatsapp` (messages): verifies, deduplicates and answers each message; the same under `/channels/whatsapp/n/{phone number id}` for own numbers on their own Meta apps; the Flows data endpoint `POST /channels/whatsapp/flows[/{phone number id}]` |
| `api` | Account > WhatsApp (binding, PIN), Settings > WhatsApp number (own number, public menu), the step-up page, and the notifier: approval requests to approvers, and how runs started from chat ended (every 5 seconds) |
| `scheduler` | Alerts to WhatsApp alert channels |

Outbound messages go to `graph.facebook.com` through the egress guard. Sends are not retried: a message that fails is recorded (an approval request's error is kept with it) and the person can ask again (`approvals`).

## For people: using Taskiem on WhatsApp

**Link your number.** Under Account > WhatsApp, enter your number with its country code and confirm it is you (passkey, authenticator code or password, as when adding a passkey). Taskiem sends a 6-digit code to that number on WhatsApp: type it on the page, or send it back to Taskiem's number. The code works for 10 minutes; five wrong codes spend it, and at most three codes are sent an hour. A number can be linked to one account, and an account has one number. Unlink it on the same page.

**What you can send:**

| Message | What happens |
| --- | --- |
| `help` | What you can do here, given your roles |
| `status`, `what failed today?` | Runs in the last 24 hours by status, the latest failures, and approvals waiting for you (needs `run.read`) |
| `run <workflow>` | Matches the name of a workflow deployed in `prod` (or its id); sends a form for its required inputs (or, where forms are not set up or the inputs do not fit one, asks each in turn), checked against the workflow's input schema; then shows exactly what will run and waits for **yes**. Needs `run.start`; plan limits apply. You hear how the run ended |
| `approvals` | Requests waiting for you, sent again with fresh buttons (three at most) |
| `switch <organisation>` | Work in another organisation you belong to; `switch` alone lists them |
| `cancel` | Drops what is in progress |

On the shared number every message from Taskiem starts with the organisation's name in brackets: the number is shared by every organisation on this Taskiem. An organisation with [its own number](#own-numbers) writes to you from that number, without the brackets, and messages you send to it act in that organisation only (`switch` does not apply there).

**Forms.** Where inputs are needed, Taskiem sends a form: tap **Fill in**, enter the details, tap **Continue**. Answers that do not fit are marked in the form. Nothing starts until you reply **yes** to the summary in the chat. Writing in the chat instead closes the form and asks the inputs one by one.

**Approving.** When a request needs your role, Taskiem sends its details with **Approve** and **Reject** buttons. A button works once, only from your number, for 24 hours or until the request times out. The same rules as in the web app apply: you cannot approve what you started, wrote or published; with distinct approvers you approve one level at most; a delegation lets you cover for someone. When the policy needs step-up:

- if the policy accepts a WhatsApp PIN and you have set one, Taskiem sends a form for this decision only: enter your PIN within 10 minutes;
- otherwise Taskiem sends a link: open it (signed in to Taskiem in that organisation) and confirm with your passkey or authenticator code within 10 minutes.

The decision is then recorded, and Taskiem confirms it in the chat.

**Your approval PIN.** Under Account > WhatsApp, once your number is linked and you have a passkey or authenticator, set a six-digit PIN (not one digit repeated or a run like 123456), confirming with your passkey or authenticator code. Five wrong PINs in 15 minutes lock it for 15 minutes; meanwhile approvals send the web link. Remove it on the same page; unlinking your number removes it too.

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

Variables are filled with one-line text (Meta refuses new lines in them): the request summary in `taskiem_approval_request` joins its masked fields with semicolons. Meta charges per template message: see [template costs](#template-costs). An organisation with its own number must have the same templates approved in its own WhatsApp Business Account.

## Flows

WhatsApp Flows are forms inside WhatsApp. Taskiem uses two, published once in each WhatsApp Business Account that sends them (Taskiem's, and each own number's) under these names, with the Flow JSON in [`docs/whatsapp-flows/`](whatsapp-flows/) (generated by `engine/whatsapp`, and checked by `TestFlowJSONDocumented`; regenerate with `go test ./engine/whatsapp -run TestFlowJSONDocumented -update`):

| Flow | Screen | What it is | Used for |
| --- | --- | --- | --- |
| `taskiem_inputs` | `INPUTS` | A generic form of 10 slots, each a text box, a number box and a list, shown, labelled and required by the screen's data; **Continue** sends the form to the data endpoint | A workflow's inputs (`run <workflow>`), public menus |
| `taskiem_pin` | `PIN` | One six-digit passcode box under the decision's title and summary; **Confirm** sends it to the data endpoint | Step-up by the WhatsApp PIN |

Flow JSON version 7.3, data API 3.0, one terminal screen each. In WhatsApp Manager create each Flow with these names, paste the JSON, set the endpoint URL (below), pass the health check and publish.

**From schema to form.** The required fields of a workflow's input schema (as for chat: `string`, `integer`, `number`, `boolean`, enums, `$ref` to the definition's types) go to slots in order: text fields to text boxes, integers and numbers to number boxes, enums and booleans to lists (Yes/No), with the field's title as the label (20 characters; the full title or description below it). Ten fields at most and lists of at most 200 entries; nested objects and arrays, or more fields, are asked in chat as before. The form is sent with the `navigate` action and its data, so it opens without a round trip; opening it again (`INIT`, `BACK`) is answered from the schema.

**The data endpoint.** `POST /channels/whatsapp/flows` for Flows sent from Taskiem's app (the shared number, and own numbers connected through it), `POST /channels/whatsapp/flows/{phone number id}` for an own number on its own Meta app. In order:

1. `X-Hub-Signature-256` under that app's secret, else 432.
2. Decrypt: `encrypted_aes_key` with RSA-OAEP (SHA-256, MGF1 SHA-256) under `TASKIEM_WHATSAPP_FLOWS_PRIVATE_KEY`; `encrypted_flow_data` with AES-GCM under that key and `initial_vector` (tag appended); else 421, and the client fetches the public key again.
3. `ping` answers `{"data":{"status":"active"}}` (in the clear too, if it comes in the clear); an error notification (`data.error`) is acknowledged and logged.
4. The flow token: `wf1.` and base64url of the tenant and 24 random bytes; only the bytes' SHA-256 is stored, in the tenant's `whatsapp_flows` row with its purpose, number, person, phone number id and expiry. Unknown, spent, expired or from another app's number: 427, and the form's button is disabled.
5. The answer, encrypted with the same AES key and the IV's bits flipped, base64: the screen again with errors (`error-message` per field, a line at the top), or `SUCCESS` with `extension_message_response.params.flow_token`.

WhatsApp then posts the completion to the webhook as an `nfm_reply` message from the person's number, carrying the flow token; Taskiem takes it only from that number, and goes on in the chat: the summary and **yes** for inputs, the outcome for a PIN. Inputs marked `x-pii` are sealed in the row until then. Requests per flow token are rate-limited.

**Keys.** Generate a 2048-bit RSA key pair; the private key goes in the Secret (`TASKIEM_WHATSAPP_FLOWS_PRIVATE_KEY`, unencrypted PEM), the public key is uploaded to the shared number (`POST /{phone-number-id}/whatsapp_business_encryption`, form field `business_public_key`); Taskiem uploads it to each own number when it is connected. Without the key, inputs are asked in chat and step-up always uses the web link.

## Approval PIN

A policy rule's `step_up` names the weakest second factor it takes ([policies](governance.md)):

| `step_up` | Satisfied by |
| --- | --- |
| `whatsapp_pin` | The WhatsApp PIN in a `taskiem_pin` form for that decision, an authenticator code, or a passkey |
| `totp` | An authenticator code or a passkey (never the PIN) |
| `passkey` | A passkey only |

`runtime.StepUpSatisfies` decides, inside `VoteApproval`, for every channel. A tap on **Approve** or **Reject** under a `whatsapp_pin` policy, by someone with a PIN that is not locked, gets a PIN form bound to that decision (its run, step, level, decision and the decision token tapped, in the flow row; 10 minutes; once). The endpoint checks the person can still decide approvals, the PIN (Argon2id, like passwords), that the request is still open at that level, and then records the vote with step-up `whatsapp_pin`. Wrong PINs are counted (`whatsapp.pin.fail`); the fifth in 15 minutes locks the PIN for 15 minutes (`whatsapp.pin.locked`). Under `totp` or `passkey` policies, without a PIN, or with it locked, the web link is sent as before. The PIN is a person's, like the binding (`whatsapp_pins`, reached only through functions); setting it needs a bound number, an enrolled passkey or authenticator, and a fresh assertion or code (`PUT /v1/me/whatsapp/pin`); `DELETE` removes it; `GET /v1/me/whatsapp` shows whether it is set or locked.

## Own numbers

An organisation can connect its own WhatsApp Business number (Settings > WhatsApp number, `secret.manage`): `PUT /v1/whatsapp/number` with the phone number id, WhatsApp Business Account id and a system user token with `whatsapp_business_messaging`; and, for a number on the organisation's own Meta app (the default), the app secret and a verify token of its choosing. Taskiem checks the token against the Graph API (`GET /{phone-number-id}`), uploads the Flows public key, stores the credentials in the organisation's vault (environment `_whatsapp`; reads recorded as connection reads) and the number in `whatsapp_numbers` (one number per organisation, one organisation per number). `DELETE` disconnects it. Both are audited (`whatsapp.number.connect`, `whatsapp.number.disconnect`).

In the organisation's Meta app, set the callback URL to `<edge URL>/channels/whatsapp/n/<phone number id>` with that verify token, subscribe to **messages**, set the Flows endpoint to `<edge URL>/channels/whatsapp/flows/<phone number id>`, and publish the two Flows and the templates in its account. A number connected with `own_app: false` is on Taskiem's app (as embedded signup would connect it): its webhooks and Flows come through `/channels/whatsapp` and `/channels/whatsapp/flows`.

Then:

- inbound messages are routed by `metadata.phone_number_id`: a delivery signed with one app's secret is only taken for that app's numbers; a bound person writing to the number acts in that organisation only, if they are a member; an unbound number gets the [public menu](#public-menus), if there is one;
- binding codes, approval requests, step-up links and forms, run outcomes and alerts to the organisation's people go from its number, without the bracketed name; the 24-hour window is per business number (`whatsapp_own_contacts`);
- anything else, and other organisations, use the shared number.

**Embedded signup** needs a Business Solution Provider decision ([needs people](needs-people.md#phase-3), W3). `POST /channels/whatsapp/embedded-signup` is the hook and answers 501; when built it must bind the signup session to the organisation and admin that started it (single use), exchange the code for a business token server-side, read the phone number and WABA ids from the session, store them as above with `source: embedded_signup` and `own_app: false`, register the number and upload the Flows key.

## Template costs

Meta charges per template message delivered, by category (marketing, utility, authentication) and the recipient's country; free-form messages and utility templates inside the 24-hour window are free. Taskiem sends a template only outside the window (or when Meta says it has closed), and counts each one sent per organisation, UTC month and category (`whatsapp_template_usage`), against the plan's allowance `whatsapp_templates_monthly` (1,000 by default; [plan limits](operations.md#plan-limits); a sub-tenant inherits its partner's).

| Category | Taskiem's templates | Beyond the allowance |
| --- | --- | --- |
| Authentication | `taskiem_otp` | Sent, counted as overage |
| Utility | approval requests, step-up links, run outcomes, alerts | Sent, counted as overage |
| Marketing | none today | Held back (`ErrTemplateBlocked`), counted as held back |

Approvals, codes, step-up and alerts never wait on a bill. Usage is in `GET /v1/limits` (`usage.whatsapp_templates_this_month`: `sent`, `by_category`, `overage`, `blocked`), Settings > Plan limits and `taskiem tenants limits`. There is no billing system: overage is recorded and reported for pass-through billing. Counts are taken when Meta accepts a send, not on delivery receipts.

## Public menus

An organisation with its own number may offer a menu to anyone who writes to it without a linked account (Settings > WhatsApp number, `PUT /v1/whatsapp/public-menu`, `workflow.publish`): up to nine workflows it marks public, each with a short label. A message gets the numbered menu; a number picks one; its inputs come as a form (`taskiem_inputs`, never asked in chat), checked against the schema; a summary and **yes**; then a run of the version deployed in `prod`, started as `whatsapp_public:<number>` (audited by the system actor with the number masked), and a reference. Nothing else is reachable: no status, no approvals, no other workflows. Limits: six messages then one every 10 seconds per number, three starts then one every 20 minutes per number, and per-organisation ceilings, then the plan limits. Menus are never offered on the shared number (`Server.WhatsAppPublic` remains the hook there).

## Alerts to WhatsApp

An alert channel of kind **WhatsApp** names members by email (Alerts > Channels). Each alert goes to those of them who have linked a number: as text when they wrote in the last 24 hours, otherwise as the template for the rule's kind. A delivery fails, with the reason, when none of them has a number. Retries resend to every member of the channel.

## Security model

| Concern | Control |
| --- | --- |
| Forged webhooks | `X-Hub-Signature-256` (HMAC-SHA256 of the raw body under the app secret) is checked before parsing, in constant time; the handshake answers only with the right verify token, and echoes only an alphanumeric challenge. Messages are routed by phone number id; a delivery signed by one app is taken only for that app's numbers (Taskiem's: the shared number and own numbers connected through it; an own app: its number, on its own path) |
| Forged Flows requests | The data endpoint checks the signature under the app's secret (432) before decrypting with the operator's RSA key (421); flow tokens are random, stored only as SHA-256 per tenant, bound to purpose, number, person, phone number id and expiry, and spent on completion (427); completions are taken only from the number the form was sent to |
| Retried deliveries | Each message id is claimed in the database before it is acted on (at most once: a command is never run twice; a crash mid-message loses that message, and the person sends it again) |
| Who is speaking | A message speaks for the person whose binding holds the sender's number, in the organisation the number is working in, with that person's roles and permissions there at that moment. A disabled person, or one who left the organisation, is refused. On an own number, only that organisation. Unbound numbers get a short reply, or an own number's public menu: only workflows marked public, run as `whatsapp_public:<number>` with no roles, strictly rate-limited (`api.Server.WhatsAppPublic` remains a hook on the shared number) |
| Binding someone else's number | The code goes to the number; binding also needs proof of the account (a passkey, authenticator code or password). A number belongs to one account |
| Guessing a code | 6 digits, 10 minutes, five wrong guesses, three codes an hour and a few per number; stored only as a SHA-256 of number and code |
| Forged or altered buttons | Decision tokens: HMAC-SHA256 under `TASKIEM_WHATSAPP_TOKEN_KEY` over the tenant, nonce, purpose, decision, expiry, run, step, approval level and approver, the last four kept in the tenant's `whatsapp_tokens` row under the nonce; a token verifies only with both the key and the row |
| Replay | A token's row is marked used when tapped; the Approve and Reject of one request share a notice, so either spends both. Hand-off links are used once |
| A button forwarded or tapped by someone else | The tapping number must be bound to the approver the token names; refusals are audited (`approval.token.refused`) |
| Approving what you should not | The decision is `runtime.Store.VoteApproval`, the web app's path: role or delegation, makers cannot approve, distinct approvers, levels; a refusal is audited (`approval.decide.refused`) |
| Step-up | Never satisfied by chat text. Under a `whatsapp_pin` policy, by the person's PIN in a form bound to that one decision (Argon2id, five wrong in 15 minutes lock it; never enough for `totp` or `passkey` policies). Otherwise the hand-off link carries a separate 10-minute, single-use token in the URL fragment (kept out of logs and `Referer`); the page needs a signed-in session as the same person in the same organisation, then a passkey assertion or TOTP code for that exact decision |
| A compromised phone | Per-number limits: 20 messages then one every 3 seconds (one warning a minute), three run starts then one every 20 seconds, and every start needs an explicit **yes** after a summary of exactly what will run. Unlinking the number in the web app ends it |
| Personal data in messages | Masked as above; free text passed through the PII redactor; inputs typed in chat are sealed (`x-pii` fields) while they wait for confirmation |
| Tenant isolation | Bindings, codes, the window and message ids are a person's, not a tenant's: no tenant reads them; functions do one narrow thing each. Conversation state, tokens, flows, notices, run watches, own numbers, public sessions and template usage are tenant data under forced row-level security, read only with the conversation's current organisation (or the token's) in scope. The PIN is a person's, like the binding |
| Own numbers' credentials | In the organisation's vault, never in tables, responses or logs; connecting needs `secret.manage` and a token the Graph API accepts for that number |
| Audit | `whatsapp.code_sent`, `whatsapp.bind`, `whatsapp.unbind`, `whatsapp.pin.set`, `whatsapp.pin.remove` (in every organisation the person belongs to, with the number masked), `whatsapp.pin.fail`, `whatsapp.pin.locked`, `approval.decide` with `channel: whatsapp` and `via: button`, `handoff` or `flow_pin`, `run.start` with `channel: whatsapp` (or by `whatsapp_public:<masked number>`), `whatsapp.number.connect`, `whatsapp.number.disconnect`, `whatsapp.public_menu.update`; the approval's history records the channel and the step-up used |

Limits are per `edge` replica (in memory), like the sign-in limiter.

## Not yet

- Embedded signup (W3): the hook is documented above.
- Delivery receipts: templates are counted when Meta accepts them, not from the `pricing` object of status webhooks.
- Run outcomes for public-menu runs (the person gets a reference only), and menus on the shared number.
- Flows for inputs beyond ten flat fields (nested objects, lists), date pickers.
- Languages beyond English, voice notes (A4), building workflows by chat (B4).
