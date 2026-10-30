# Chat as the way in: signing up and connecting from WhatsApp, Telegram and Slack

Status: **Built** 29 October 2026 (C1 to C6; each milestone's as-built notes follow it) · Decision: [0058](../decisions/0058-chat-as-a-way-in.md) (accepted) · Builds on: [WhatsApp](../whatsapp.md), [Telegram](../telegram.md) ([0035](../decisions/0035-telegram-channel.md)), [Slack](../integrations/slack.md) ([0051](../decisions/0051-platform-apps.md)), [onboarding](../onboarding.md) ([0022](../decisions/0022-self-serve-onboarding.md)), [field agents](../agents.md), [OAuth connections](../decisions/0034-oauth-connections.md), [no technical steps](no-technical-steps.md) (N2, N6) · Strategy: [chat-first](../strategy/chat-first-nigeria.md#32-three-product-gaps-that-keep-the-chat-from-being-chat-first) gaps 1 and 2 · Stories: [user stories](../strategy/user-stories.md) 1 to 7, 11 to 14

## 1. Summary

A trader saves Taskiem's WhatsApp number, sends "hi", and gets a business made for them in the chat: they agree to the terms with **Yes**, give the business's name and their own, say what they sell and how customers pay, and are offered three automations that fit. When one needs Paystack, Google or Moniepoint, the chat sends a form or a link that connects it without leaving the phone, and the automation is published and runs in the same conversation. The same works on Taskiem's Telegram bot, and, for businesses that work in Slack, by adding Taskiem's Slack app to the workspace.

**The web stays a way in.** Signing up on the web, confirming an email and linking WhatsApp or Telegram afterwards (as today) is unchanged, and the two meet: a person who signed up in chat can add an email and a password and use the web app; a person who signed up on the web keeps their chat as it is; the same person can use WhatsApp, Telegram and the web at once, in the same businesses.

**Every channel together.** Each milestone ships on WhatsApp and Telegram at once, through the one conversation they already share (`converse`). Slack joins as a third channel where its model allows: a Slack business is a workspace, its people are workspace members, and it has no phone numbers (§5.3).

Six milestones, C1 to C6, after the people items of C0. C1 and C2 close the strategy's first two chat-first gaps and are the pilot's prerequisites ([POS terminals §5](../strategy/pos-terminals.md#5-what-this-does-not-do-yet-and-what-comes-next)).

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Way in | How a person first gets a Taskiem account and a business: the web form (today), a field agent ([agents](../agents.md), today), or chat (this plan) |
| Chat-born | A person or business made from chat. A chat-born person has an account like anyone else's, with no password and a placeholder sign-in name until they add an email |
| Channel identity | What a chat proves about the person writing: a WhatsApp number (WhatsApp delivered the message from it), a Telegram account and the phone number it shared as its own contact, or a Slack user in a workspace that installed Taskiem's app |
| Shared channel | Taskiem's own WhatsApp number, Telegram bot and Slack app, which every business uses. A business's own WhatsApp number is not one: strangers who write to it are its customers |
| Connect link | A one-time link chat sends to connect one service to one business, opened in the phone's browser, needing no web sign-in (§7.3) |
| Secure form | Where a secret is typed: a WhatsApp form (Flow), whose answers are encrypted to Taskiem; a Telegram Mini App, a Taskiem page opened inside Telegram; or the connect page. Never a chat message |

## 3. Where we are

| Exists | Where |
| --- | --- |
| One conversation for WhatsApp, Telegram and Slack (`chatChannel` and `converse` in `api/channel.go` and `api/whatsapp_chat.go`): commands, forms, builds, tables, invoices, stock, storefronts, approvals, all under the person's roles in the business they are working in | [WhatsApp](../whatsapp.md) |
| Web signup, off by default (`TASKIEM_ALLOW_SIGNUP`), with an email to confirm; until it is confirmed the business cannot invite members, make API keys or email alert channels | [onboarding](../onboarding.md) |
| Linking a chat after web signup: a code sent to the WhatsApp number, or a one-time Telegram link, made by a signed-in person who proved it is them | [WhatsApp](../whatsapp.md#for-people-using-taskiem-on-whatsapp), [Telegram](../telegram.md#for-people-using-taskiem-on-telegram) |
| A stranger writing to the shared number or bot is told to sign up on the web and link it (`wa.unbound`, `tg.unbound`); a linked person with no business is told so (`wa.no_org`) | `api/whatsapp_chat.go`, `api/telegram.go` |
| **Businesses made in chat, by field agents**: the business and an owner with no password (`ownerPlaceholderEmail`), `tenant_onboarding.source = 'agent'`, the trial started (`Billing.Ensure`); the owner agrees from their own WhatsApp number, or on Telegram by sharing their own contact; `sign in` in chat sends a one-time link to choose a password | [agents](../agents.md), `api/agents.go`, `api/chat_publish.go` |
| Building from chat to the end: draft, tables, settings one at a time, **Publish it now?** (N6). A missing connection is a line with a link to the web app's Connections page, which needs a web sign-in | [WhatsApp: build](../whatsapp.md#build), `api/chat_publish.go` |
| Guided connecting on the web (N2): each key field's label, help, where to find it, accepted prefixes (live or test) and words for wrong values, from the manifest; the key checked with the provider (`auth.test`); `main` as the one name; one-click OAuth where Taskiem has an app (0034), started from a signed-in session with a browser cookie | [no technical steps N2](no-technical-steps.md#n2-connecting-a-service-l), `api/oauthconnections.go` |
| WhatsApp forms: `taskiem_inputs` (ten text, number or list slots) and `taskiem_pin` (a passcode box), answers encrypted to Taskiem's Flows endpoint | [WhatsApp: Flows](../whatsapp.md#flows) |
| Telegram: no forms; inputs asked one by one in chat; step-up by a web link | [Telegram](../telegram.md#differences-from-whatsapp) |
| Slack: approvals only. A business adds Taskiem's Slack app (**Add to Slack**, its own connection), members link their Slack user, approval requests arrive as direct messages with buttons; clicks come to `/channels/slack/interactions`. The bot does not read direct messages: Slack's `Contact` and `Touch` do nothing | `api/slack.go`, [Slack](../integrations/slack.md#approvals-in-slack) |
| Signup to first run measured (`tenant_onboarding`, `taskiem_onboarding_first_run_seconds`) | [onboarding](../onboarding.md#measuring-gate-g4) |

Not built, and what this plan adds: signing up from chat (gap 1), connecting from chat (gap 2), Slack as a conversation and as a way in, one person moving between the doors (adding an email to a chat-born account, linking an existing account from chat, invitations by chat), and recording that a person agreed to the terms (on the web too).

## 4. What stays as it is

- **The web form.** `POST /v1/signup`, its checks and limits, the email confirmation and its holds, the checklist and the guided first workflow: unchanged, and still behind `TASKIEM_ALLOW_SIGNUP`. It gains one thing, the terms checkbox and its record (§6.4).
- **Linking after web signup.** Account > WhatsApp and Account > Telegram, the code and the one-time link: unchanged, but for one rule that now applies to every binding, however it was made: a binding silent for 90 days goes dormant and re-proves itself before it acts (§12, recycled numbers), since a reissued number puts a web user's chat at the same risk as a chat-born one's. A web user re-proves with the email link (§10.1), or links again from the web as today.
- **Field agents.** Their flow is unchanged; a chat-born owner and an agent-made owner are the same kind of account, so an owner an agent signed up who later writes to the number is simply bound already.
- **Own WhatsApp numbers and public menus.** A stranger on a business's own number is that business's customer: they get its public menu or its updates, never a sign-up (decision 0058, point 6).
- **People outside a business** (customers who asked for updates, riders, suppliers, family pages): what they send is handled first, exactly as now (`reachStart`, the invoice, rider and supplier buttons). Only a message nothing else claims reaches the sign-up offer, and the offer never interrupts them. A number Taskiem already knows as some business's customer, rider or supplier (a contact with a tenant) is not offered a sign-up on an unclaimed message, so a customer asking about their order is not advertised to; it gets the offer only when it sends **start**.

## 5. The channels

### 5.1 What each can do

| | WhatsApp (shared number) | Telegram (platform bot) | Slack (Taskiem's app) |
| --- | --- | --- | --- |
| A stranger can start | Yes: any message | Yes: `/start`, or a `t.me/<bot>?start=signup` link | Only after the workspace installs the app (**Add to Slack**) |
| Identity it proves | The phone number | The Telegram account; the phone number once they tap **Share my number** and it is their own contact (as the agent flow checks today) | The Slack user and their workspace (`team_id`); an email Slack holds for them, **not** taken as confirmed (§9) |
| Forms | Flows (`taskiem_inputs`, and the new `taskiem_connect`) | A Mini App: a Taskiem page opened inside Telegram from a button, which knows who opened it from Telegram's signed `initData` (new, C2) | Modals (`views.open`) for ordinary inputs (C3) |
| Where a secret is typed | The `taskiem_connect` Flow (encrypted to Taskiem; Meta cannot read it) | The Mini App (sent straight to Taskiem over HTTPS) | The connect page in the browser: a modal's answers pass through Slack |
| Buttons | Reply buttons, list messages | Inline keyboards | Block Kit buttons |
| Writing first, later | Inside 24 hours of their last message; templates outside it | Any time after **Start** | Any time, to a member of an installed workspace |
| Languages | The five ([languages](../languages.md)) | The five | The five; offices mostly English |

### 5.2 One sign-up, three front doors

What a sign-up does once its channel has proved who is writing is the same everywhere (§6.3), and lives in one place in the API, called by each channel's handler. A channel brings its identity, its way of asking (buttons or typed answers) and its forms; the steps, the checks, the record and the result are shared. A test that signs up on one channel signs up on the others by changing the channel.

### 5.3 Is Slack possible?

Yes, with what Slack is. Slack's Events API sends the app every direct message to its bot (the `message.im` event, with the `im:history` scope Taskiem's app already asks for, and the App Home **Messages** tab on), so the conversation WhatsApp and Telegram have can run on Slack as a third channel. Slack's 2025 limits on apps outside its Marketplace (from 29 May 2025 for new installs) cut `conversations.history` and `conversations.replies` to one call a minute; Taskiem reads neither (each message arrives as an event), and posting and modals are not affected.

What differs, and shapes C3 and C4:

- **A business is a workspace.** Nobody can write to a Slack bot their workspace has not installed, so the way in is **Add to Slack**, by someone allowed to add apps (a workspace's admins may require approval for installs). The workspace's other members join the business by the owner's say (§10.2), never by being in the workspace alone.
- **No phone number.** A Slack-born owner has an email from Slack, a Slack user id and a workspace; SMS and WhatsApp reach them only once they link those.
- **Who it fits.** Offices that already work in Slack: SaaS companies, agencies, fintechs, schools' head offices (stories in sections G, P, Q), not traders on a phone. WhatsApp and Telegram stay the way in for them.

## 6. C1: signing up from WhatsApp and Telegram

### 6.1 The conversation

On the shared WhatsApp number, a message from a number linked to no one, that nothing else claims, gets:

> Hello! Taskiem runs your business's errands from this chat: payments, invoices, stock, reminders, your shop page. Do you want to start, for free?
>
> **Start my business** · **I already use Taskiem** · **Not now**

- The offer is in the language the message's words route to ([languages](../languages.md)), English when nothing matches; the first question is preceded by a language choice, a list message on WhatsApp (reply buttons stop at three) and an inline keyboard on Telegram. The language chosen is the person's, as today.
- **Start my business**: the privacy notice and terms, as a short message and a link: "Taskiem keeps your business's records and the details you give it. Read how: <link>. Reply **Yes** to agree and start." Nothing is made before **Yes**; the acceptance is recorded (§6.4).
- Then, one question at a time (buttons where they fit, a typed answer otherwise, `cancel` at any point):
  1. "What is your business called?" (the web form's checks: no links, no addresses, 100 characters)
  2. "And your name?"
  3. "What do you sell or do?" (food, provisions, fashion, phones, school, salon, services, other) (story 2)
  4. "How do customers pay you?" (cash, transfer, POS, Paystack, Moniepoint, OPay; several)
- The business is made (§6.3), and the chat says so: "Done: **Ada Foods** is on Taskiem, free for 14 days." (the days from billing's `trial_days`, [billing](../billing.md), 14 today) "Here are three things businesses like yours start with:" and the three [templates](../templates.md) that fit best, by the answers to 3 and 4 against each template's `category`, `tags` and `keywords` in the library (there is no matcher today; C1 adds one, a table from the two answers to template ids), each a button, the first one that needs nothing connected (**Remind me every day**) so a run can come at once (story 7). Or "say what you want in your own words".
- Picking one goes into the build conversation that exists (N6): its details one at a time, connections (C2), **Publish it now?**, and the first run.

On Telegram the same, from `/start` or the website's `t.me/<bot>?start=signup` link, with one more step before the business is made: "Tap **Share my number** so we can text you when Telegram can't." Sharing is required and must be their own contact (the check the agent flow makes), so a Telegram-born owner has a proven number like a WhatsApp-born one; tapping **Not now** stops there and says why.

**I already use Taskiem** goes to §10.1 (linking an existing account). **Not now** says how to come back ("send **start** any time") and stops.

A linked person with no business left (`wa.no_org` today) is offered **Start my business** the same way, without the account steps. A linked person in businesses can make another with `new business` (the same steps after the terms), and `switch` between them as today.

### 6.2 Who may sign up

- Off by default, per channel: `TASKIEM_CHAT_SIGNUP` lists the channels that offer it (`whatsapp,telegram`; `slack` in C4). Off, the unbound message stays as today.
- Per channel identity: one sign-up in progress at a time; three businesses made a day per number, Telegram account or Slack user; the web form's names checks.
- Per deployment: `TASKIEM_CHAT_SIGNUP_PER_HOUR` (default 60), counted in the database across replicas like `TASKIEM_SIGNUP_PER_ADDRESS`; over it, "we are busy, try again in an hour", and an operator alert.
- Countries: a WhatsApp number or shared Telegram contact outside `TASKIEM_CHAT_SIGNUP_COUNTRIES` (calling codes; default `234`) is told sign-up is not open there yet.
- Every outcome counted in `taskiem_signups_total`, which gains a `source` label (`signup` for the web form, as `tenant_onboarding.source` names it today, `agent`, `whatsapp`, `telegram`, `slack`); its `outcome` values stay as they are.

### 6.3 What a sign-up makes

In one transaction, as `agentNewBusiness` does today, lifted into a shared function both now call:

- the person, if the channel identity is linked to no one: an account with the channel's placeholder sign-in name (`ownerPlaceholderEmail` by number for WhatsApp, by account id for Telegram, by workspace and user for Slack), their name, no password;
- the business, its default workspace and environments, the person its owner;
- `tenant_onboarding` with `source` set to the channel (its check constraint, `signup`, `existing` and `agent` today, widened);
- the channel bound to the person (the WhatsApp binding by number; the Telegram binding by account; a shared contact whose number is already a WhatsApp-bound person's is §10.3's case, and makes no new account), and the conversation set to the new business;
- the audit entry `tenant.create` with the channel and the identity masked, and `terms.accept`.

Then the trial starts (`Billing.Ensure`), as for every way in.

**The holds.** A web sign-up is held back until its email is confirmed. A chat-born owner has proven their channel instead, which counts for the holds that protect other people from a stranger: inviting members (by chat, §10.2) and the API keys of the web app. What sends email for the business (email alert channels, invitations by email) still needs a confirmed email of their own (§10.4). The cleanup of unconfirmed tenants that [phase 4](../phase-4-status.md) still owes applies to chat-born tenants by the owner's last message, not by a confirmation they will never make.

**The PIN.** Until an email is added, the chat is the account's only credential, so after the first run the chat offers to set a six-digit PIN (`pin`): typed in the `taskiem_pin` form on WhatsApp, or the Mini App on Telegram (C2), and kept as the WhatsApp PIN is today (`whatsapp_pins`, [WhatsApp](../whatsapp.md#approval-pin)). Setting that PIN today needs a passkey or authenticator, which a chat-born person has neither of; for them the proven channel and the chat it is typed in are the proof. The PIN is what re-proves a dormant binding (§12) and what approval policies that accept `whatsapp_pin` use.

### 6.4 Agreeing to the terms

A new `terms_acceptances` record, the person's, not a tenant's: who, which document and version (`TASKIEM_TERMS_VERSION`, `TASKIEM_PRIVACY_VERSION`), when, and where (`web`, `whatsapp`, `telegram`, `slack`). A sign-up in chat records it on **Yes**. The web form gains the same checkbox and record, so both ways in prove consent the same way, and a new version can be asked for once in whichever door the person uses next. Accounts that exist before this have no record: the next sign-in on the web, or the next message in chat, asks once (one line with **Yes**), and nothing is held back meanwhile. The texts themselves are a people item (C0-2).

### 6.5 State before there is a business

Conversation state is tenant data today (`chat_sessions`, under row-level security). A sign-up has no tenant yet, so its few answers wait in a `chat_signups` row of the person's channel identity (hashed), for 30 minutes at most, reached only through narrow functions as bindings and contacts are; the business name and person's name are sealed as personal data. The row is deleted when the business is made or the time runs out.

### 6.6 Accept

- On WhatsApp and Telegram (each test run on both): a stranger signs up and gets the first run of **Remind me every day** without the web app; **No** to the terms makes nothing; `cancel` at each question makes nothing; the limits and the switch hold; a customer of a business who writes to the shared number about their order still gets their order, not a sign-up.
- The web form, the field agent's sign-up and linking after web signup still pass their tests unchanged (`web/e2e/onboarding.spec.ts`, `api/agents_test.go`).
- A Telegram sign-up that shares someone else's contact, or no contact, makes nothing.

### 6.7 As built (C1)

Built as above ([onboarding](../onboarding.md#signing-up-from-chat), [WhatsApp](../whatsapp.md#the-chat-pin-and-dormant-chats)), migration `00266_chat_signup`, where it differs:

- **The language.** WhatsApp has no list message the platform client sends, and reply buttons stop at three, so the offer and the terms say which languages are on and a language's name typed chooses it (as `language <name>` does); words already route the language as before.
- **What they sell and how they pay** are numbered lists in the message (several numbers for how they pay), answered by number or word.
- **The templates offered** come from a table in code (`signupTemplates`): **Remind me every day** first; then by what they sell (a shop: today's sales total; a school: fee reminders; services: send an invoice) and how they are paid (POS: the evening POS summary; Paystack: payment receipts; a shop: the low stock alert). A template tapped is built without the AI (`chatTemplateBuild`: an `ai_builds` row with provider `template`, saved with `ai_assisted: false`), so it works on a deployment or plan without AI.
- **The pre-signup record** keeps the business's and the person's names plain, not sealed: the vault seals per tenant and there is no tenant yet. It is reached only through `taskiem_chat_signup_*`, keyed by a digest of the address, and lives 30 minutes.
- **The terms' Yes** is recorded with the account, in the transaction that makes it (the moment of Yes is in the `terms.accept` audit entry's `agreed_at`), since before it there is no person to record it for.
- **A Telegram sign-up's number** proves the person and counts toward the daily limit; it is not bound as their WhatsApp too (one person on both channels is C5).
- **Not yet:** the PIN on Telegram (C2's Mini App); telling the owner on their other channel or by email when a chat goes dormant, and money actions and approvals waiting on the PIN beyond the dormant gate (they are refused with the rest); agreeing to the terms when joining by invitation (C5, with **Join**); the cleanup of unused chat-born tenants by the owner's last message (with phase 4's).

## 7. C2: connecting from chat

### 7.1 When chat asks

Wherever chat today sends a link to the Connections page (a build's blockers, `make my page` without Paystack, a run that failed for an expired key), it offers **Connect Paystack** instead, and `connect <service>` works on its own. Connecting needs `secret.manage`, which owners hold; a member without it is told who can. The connection is the business's `main` one in `prod` (N2's one name), and when it is made the conversation goes on where it stopped: the blockers are checked again and **Publish it now?** comes back.

### 7.2 A key: a form, never a message

A key-based service (Paystack, Moniepoint, OPay, Termii, Flutterwave and the rest) is connected through a secure form built from the manifest, with the same help, links, prefix choice (live or test) and refusals as the web's guided screen (N2), and the same check with the provider before anything is saved:

- **WhatsApp**: a new Flow, `taskiem_connect`, one screen of up to five fields, each a `password` or `text` box (both are Flow `TextInput` types), the help line under each, encrypted to Taskiem's Flows endpoint like the PIN. A wrong key comes back in the form with the plain message ("That is your public key. Copy the secret key from the same page: it starts with sk_.").
- **Telegram**: a Mini App. The button opens a Taskiem page inside Telegram; the page sends Telegram's signed `initData` with the form, and Taskiem checks it (HMAC-SHA-256 under a key derived from the bot token, and its `auth_date` within ten minutes) and that its user is the account the form was sent to. The page is the web's guided connect form, in Telegram's colours. The same Mini App gives Telegram the forms WhatsApp has for a run's inputs and a table's row (`run`, `add to`), where it now asks one field at a time.
- **Slack**, and **everywhere as the fallback** (WhatsApp without Flows set up, a form that does not fit five fields): the connect page (§7.3).

**A key typed into the chat** is never kept: inbound text is checked against every manifest's secret prefixes (`sk_live_`, `sk_test_`, and the rest) before it is stored or acted on, and, while a connect is pending in that chat, any token-shaped message (one word, 20 characters or more, letters, digits and `-_` only) is treated as a key too, since many providers' keys carry no prefix. The match is masked before anything keeps it: the conversation's record, the outbox, the audit entry and a voice note's transcript. The reply says to use the form and to replace the key at the provider, since the chat app has it now.

### 7.3 The connect page: OAuth, and keys in a browser

A service that signs in (Google, Zoho, QuickBooks, Mailchimp, Xenibase) needs a browser. Chat sends a connect link: `https://<public URL>/c/<token>`, a one-time token (24 random bytes, kept only as its SHA-256), for one service, one business, one person and the chat that asked, good for 15 minutes. It needs no web sign-in: the token is the authority, for that one connection only.

- Opening it shows the business's name and the service ("Connect Google to **Ada Foods**"), and **Continue** starts the provider's consent as the web does, with the browser cookie and PKCE of 0034. The token is spent when the callback completes the connection, or at 15 minutes, so a denied consent or the wrong account can try again from the same link meanwhile.
- The provider sends the browser back to the callback that exists; when the connection came from a connect link, the page ends with "Google is connected. You can go back to WhatsApp" (a `wa.me` or `t.me` link back, or Slack's), and the chat says so too: "Google is connected (ada@example.com). Carrying on with **Remind my debtors**."
- A key-based service on the connect page is the web's guided form, saved the same way.

### 7.4 Accept

- Paystack connected from WhatsApp and from Telegram with a test key, a public key refused in the form, Google connected through a connect link on each, and the build each was for published and run; the web's Connections page shows them as made by the person, `channel` in the audit.
- A connect link used twice, after 15 minutes, or by a member who lost `secret.manage` meanwhile, makes nothing; the state and cookie checks of 0034 still hold.
- A Paystack key sent as a chat message is not stored anywhere Taskiem keeps the conversation (a test reads every table that keeps chat).

### 7.5 As built (C2)

Built as above ([WhatsApp](../whatsapp.md#connecting-a-service-from-the-chat), [Telegram](../telegram.md#connecting-a-service)), migration `00267_connect_links`, where it differs:

- **Where chat offers Connect**: a draft's publishing blockers (after a build, or `publish <name>`) and **make my page** without Paystack say `connect <service>` (WhatsApp reply buttons stop at three and carry no link, so it is a word to send). The draft waiting is remembered with the conversation, so `connect` carries on with it. A run that failed for an expired key still points at the web.
- **The connect link's connection** is the first free name (`main` unless one exists), in `prod`; the web form's audit (`connection.create`) is kept, and `connection.from_chat` added beside it with `via` and the channel.
- **The Telegram Mini App** reads `initData` from the page's address (Telegram puts it in the fragment), so the page needs no script from Telegram. It is the connect form only: Telegram's forms for a run's inputs and a table's row are not built yet.
- **The pasted-key guard** drops the message before anything reads it, so there is nothing to mask: it is never stored. Keys from strangers are checked against the platform's connectors.
- **Not yet:** the account a sign-in connected as, in the chat's message ("Google is connected (ada@example.com)"); setting the chat PIN and waking a dormant chat from Telegram (the PIN form is WhatsApp's; on Telegram the Mini App is the connect form only). Slack's connect link came with C3, as a button.

## 8. C3: Slack as a conversation

- **Events.** Taskiem's Slack app gets one Events request URL, `/channels/slack/events`, verified with its signing secret as `/channels/slack/interactions` is. It answers within Slack's three seconds and handles the event after, keeps each `event_id` for a day so Slack's retries (`X-Slack-Retry-Num`) do nothing twice, and ignores the subtypes that are not a person writing (`message_changed`, `message_deleted`, `bot_message`, the bot's own messages). A direct message to the bot (`message.im`, `channel_type: im`) from a linked member goes to `converse`; every other event goes on, unchanged, to `/hooks/apps/slack@1/events`, the `slack@1` triggers (0051), as clicks already do. The operator sets the URL, subscribes to `message.im`, and turns the App Home **Messages** tab on ([needs people](#13-c0-people-and-decisions)).
- **The channel.** `slackChannel` gains `Contact` and `Touch` (a contact per `sl:<team>:<user>`, kept at the platform level as WhatsApp's and Telegram's are) so a Slack DM keeps a conversation, and its messages render the chat's buttons as Block Kit buttons and its numbered lists as menus. A DM carries only the workspace and the user, and a workspace may hold more than one business (0051; `slack_bindings` are per business), so the conversation has a current business and `switch`, as on WhatsApp: the DM reaches the business the person last worked in among those they are linked to in that workspace, not the workspace's business as an own number's `OwnTenant` would. Today's approval DMs, which name their business, are unchanged.
- **Forms.** A run's inputs and a table's row come as a modal (`views.open`, from the button's `trigger_id`), checked as the other forms are; a key goes through the connect page (§7.3).
- **Voice notes and photos**: a file shared in the DM is fetched with the bot token (`files:read` added to the app's scopes) where voice notes are on; a product photo likewise.
- **Accept:** the WhatsApp conversation tests (status, run, build, tables, invoices, stock) run on Slack through the same harness, and approvals in Slack still work as today.

### 8.1 As built (C3)

Built as above ([Slack](../integrations/slack.md#the-conversation-in-slack)), migration `00268_slack_conversation`, where it differs:

- **The channel** for a DM is its own (`slackDMChannel`): no business of its own (`OwnTenant` none), so the conversation picks among the businesses that linked the person in the workspace and `switch` works as on WhatsApp; it sends through the first of them whose Slack connection reaches the workspace (the bot is the same). Approval requests keep the business's channel, as before.
- **Buttons**: a conversation's buttons go out with the action id `taskiem.reply` and come back through `/channels/slack/interactions` as the person's answer; decision tokens keep `taskiem.decide`. A connect link is a URL button, since a plain-text section does not link.
- **Someone not linked** gets no reply rather than how to link: the bot can only write through a business's connection, and none has linked them.
- **Not yet:** modals for forms (asked one at a time, as on Telegram) and files in the DM (voice notes, photos; `files:read` not asked for yet).

## 9. C4: signing up from Slack

- **Add to Slack, from anywhere.** A public install link (the website, and Slack's Marketplace once listed) starts Slack's OAuth v2 install with no Taskiem business behind it. The install request is the platform's, not a tenant's: kept, single use, for 10 minutes, with the browser cookie and PKCE, as a tenant's OAuth request is.
- **The callback** has the workspace (`team.id`, its name), the bot token and the installer (`authed_user.id`). The token is sealed in the platform-level install record, which outlives the 10-minute request for a day; if no business is made by then, the token is given back (`auth.revoke`, as deleting a connection does) so the workspace is not left with a dead install. The bot then writes to the installer in Slack, with the same steps as §6.1: the terms with **Yes**, the business's name (the workspace's name offered), the installer's name (from Slack), what they do and how they get paid. The business is made as in §6.3 with the Slack connection as its `main` Slack connection and `source = 'slack'`.
- **The installer's email.** Slack gives it (`users:read.email`), and it becomes the account's email **unconfirmed**: a workspace's administrators can set members' emails, so Slack holding an address does not prove the person holds its mailbox. If an account with that email already exists, nothing is merged: the installer is asked to link that account (§10.1), or to start a new business under a Slack-only account.
- **A workspace already on Taskiem.** If another business already connected this workspace (0051 lets more than one), the installer is told which (its name only) and offered **Ask to join** (§10.2) or **Start a separate business**. Every connection of that workspace runs on Taskiem's one app there, so the install writes the token it returns into every connection of that workspace, as 0051's revoke events mark them all expired, and none is left holding a token that may no longer work.
- **Uninstalling** marks the connection expired, as 0051 says; the business and its people stay, and anyone linked by WhatsApp, Telegram or the web keeps working there.
- **Accept:** an install with no account makes a business whose owner can build and run from the DM; an installer whose email is someone's account is not merged; a second install of a known workspace makes nothing without the installer's choice; the platform-level install request cannot be completed in another browser.

### 9.1 As built (C4)

Built as above ([Slack](../integrations/slack.md#signing-up-from-slack), [onboarding](../onboarding.md#signing-up-from-slack)), with these choices:

- **The install link** is `GET /v1/slack/install`, offered on the web's sign-up page (`slack` in `GET /v1/signup`) when `TASKIEM_CHAT_SIGNUP` includes `slack`, the terms are set and Taskiem's Slack app is registered; ten a minute per address. Slack sends the browser back to its own callback, `/v1/slack/install/callback` (a second redirect URL on the app), not the connections callback: the request is the platform's (`slack_install_requests`), its state hashed with the browser's cookie, its PKCE verifier sealed with the platform's KMS key (`Vault.SealPlatform`, the customer-key credentials' pattern, bound to its use). Afterwards the browser is sent to the bot in Slack (`slack.com/app_redirect`), or to `/slack/added` with what went wrong.
- **The install record** (`slack_installs`, one per workspace, a day) keeps the bot token sealed the same way, the installer, and Slack's name and email for them, read with a new `slack@1` action, `get_user` (`users.info`). An hourly sweep gives back (`auth.revoke`) the token of an install no business took; an `app_uninstalled` event forgets it at once.
- **The sign-up** is the WhatsApp one on a channel that sends with the install's token: the terms, the business's name with the workspace's offered as a button, what they sell and how they are paid; the person's name is Slack's, so that question is skipped (asked when Slack gives none). The daily limit counts the Slack user. The business is made as in §6.3 with `source` `slack`, its Slack connection `main` in `prod` holding the token through Taskiem's app for the workspace (the app account, so the workspace's events route to it), and the owner's Slack link verified. The install record is dropped in the same transaction.
- **The email**: Slack's, kept unconfirmed (so nothing is emailed for the business until it is confirmed, as for chat-born owners); when it is already an account's, the installer is told so before anything is asked, with how to link that account from the web (the email link is C5) or **Start a new business** under a Slack-only account (`owner+…@slack.taskiem.invalid`).
- **A known workspace**: every connection of it through Taskiem's app, whatever its status, takes the new token and is active again; the installer is told the businesses' names and gets **Start a separate business** (**Ask to join** is C5). Their install's token is shared, so the sweep never gives it back. An installer already linked there is told the conversation carries on.
- **A linked person's `new business` in Slack** gives the new business the workspace's Slack too: the token of their current business's connection there, their link verified.
- **Not yet:** **Ask to join** (C5); Slack's Marketplace listing (a people item).

## 10. C5: one person, many doors

### 10.1 I already use Taskiem

From an unbound chat, **I already use Taskiem** asks for the email they sign in with, and sends a one-time link there (24 bytes, an hour, once), with the same answer whether or not the email has an account, so nobody can find out who is on Taskiem. Opening it while signed in to that account, or signing in on its page, links this chat (the WhatsApp number, the Telegram account, the Slack user) to the account, as the code and the Telegram link do today; the chat says so. A person can still link the way they do today, from the web.

### 10.2 Bringing people in from chat

- `invite +234… as manager` (WhatsApp and Telegram; a Slack member by mention: `invite @ada as viewer`): the owner sees the invitation and confirms with **Yes**. The person invited gets a WhatsApp message (a new template, `taskiem_invite`, outside the 24-hour window), or, on Telegram, a link the owner forwards (a bot cannot write first), or a Slack DM. Joining is a way in like any other: **Join** shows the terms and takes **Yes** (§6.4) before anything is made, then binds the person and makes them a member with that role. On Telegram the link alone is not enough, since anyone it is forwarded to holds it: the person must share their own contact (§6.1's check) and its number must be the one invited. On WhatsApp the template goes to a number that may never have written to Taskiem, so the owner's invitation is the opt-in Meta asks for, and the invitation says who invited them and that replying **Stop** ends it. The web's invitations by email stay.
- In Slack, a member of an installed workspace who writes to the bot and is not in the business is told who runs it and offered **Ask to join**; the owner gets **Approve** with a role, or **Decline**. Being in the workspace is never enough on its own.

### 10.3 One person on several channels

- A Telegram sign-up whose shared number is already a WhatsApp-bound person's makes no new account. If that account is chat-born (no email, no password), the number is its only proof and Telegram's own-contact check is the same proof, so the Telegram account binds to it and the chat carries on as a linked person's (`new business` if they want another). If the account has an email or a password, a number alone is not enough, as it is not on the web today: the chat says the number is on an account already and sends §10.1's email link, or the person starts a new business under a Telegram-only account.
- On the web, Account shows every linked channel, as today, and links or unlinks each; a dormant one is shown as such, with **Link again**, which is today's code or one-time link.

### 10.4 Email, password and paying

- **Add your email** (Account, and `email` in chat): replaces a placeholder sign-in name with a real address once its confirmation link is opened; until then the placeholder stays. It is how a chat-born owner gets email from Taskiem, and what billing needs.
- `sign in` in chat stays the way to choose a password (`waSignInLink`).
- `plan` in chat says the trial's days left and the price, and **Pay** sends a checkout link (`POST /v1/billing/checkout`), asking for the email first when there is none, since Paystack needs one (story 12).

### 10.5 Accept

The paths of §10.1 to §10.4, each on WhatsApp and Telegram, Slack where it applies; an invite by number accepted on WhatsApp; an existing web user linked from chat by the email link and not otherwise; the email link's answer the same for an address with no account.

### 10.6 As built (C5, first part)

Built as above ([onboarding](../onboarding.md#one-person-many-doors), [WhatsApp](../whatsapp.md#signing-in-on-the-web)), with these choices:

- **The email link** (`chat_link_requests`, a person's record through definers) is kept only when the email has an account; the chat's answer is the same either way, and three asks an hour per chat and per email. The link is `/link-chat#token=…` (24 bytes, in the fragment); `POST /v1/me/chat-link` takes it only for the account it was sent to, so another signed-in person neither uses nor spends it. A WhatsApp number or Telegram account is bound as the code would bind it; one already the person's (resting) is woken; one that is someone else's, or a second of its kind, is refused. A Slack user is linked in the signed-in business when its Slack connection reaches that workspace.
- **A Telegram sign-up with a WhatsApp person's number**: chat-born means no password and a `.taskiem.invalid` sign-in name. Their Telegram account binds to them and their Yes to the terms is recorded; no business is made. Otherwise the link goes to the account's email; without email on the deployment, the old answer stands. The separate Telegram-only account the plan allowed here is not offered: every chat-born owner keeps a proven number.
- **Add your email**: the confirmation link (`email_verifications.change`) is `/confirm-email#t=<business>&token=…` and works signed out, since an account made in chat may have no other sign-in; it replaces a placeholder or unconfirmed email only. Taken addresses are refused when the link is opened, not when it is asked for.
- **Resting chats** show on Account (`dormant` on `GET /v1/me/whatsapp` and `/v1/me/telegram`) with **Link again**: today's code or link.
- **`plan`, `pay` and `email`** in chat; **Pay** needs `billing.manage`, and `pay <email>` sets the address receipts go to.
- **Invitations from chat** (`chat_invites`, the business's; the person invited is found by their address through definers) need `member.manage` and a role the inviter could grant on the web (an owner only by an owner); they last seven days and work once. The Telegram link's token is 24 bytes, kept hashed. Joining makes the person with the sign-in name a chat sign-up would (an earlier one by the same chat is found and reused), binds the chat when it is bound to no one, adds the roles, and records the terms when they were just agreed. In Slack the invited member's binding is made in that business, and their Slack email kept unconfirmed when it is free.
- **Ask to join** offers at most three of the workspace's businesses, twice in ten minutes; one request per business an hour; owners are told on Slack, WhatsApp or Telegram, whichever they linked, with **Viewer**, **Operator** or **Decline** (other roles from the web). Approving turns the request into an invitation.

## 11. C6: words, templates and the gate

- Every new message in the five languages' catalogues (drafts for review, as the rest), the sign-up's words in each language's intents (`start`, "I want to open account", `new business`, `connect`, `invite`, `plan`), and the model routing for free text that means "sign me up" (the strategy's fourth gap, for these intents only).
- The WhatsApp template `taskiem_invite` and the `taskiem_connect` Flow in the registry and `docs/whatsapp-flows/`.
- Docs: [WhatsApp](../whatsapp.md), [Telegram](../telegram.md), [Slack](../integrations/slack.md), [onboarding](../onboarding.md) (the three ways in, side by side), the owners' guide (`docs/guide/`: "Start from WhatsApp"), [phase 4 status](../phase-4-status.md) (the open item closed), the [chat-first strategy](../strategy/chat-first-nigeria.md) (gaps 1 and 2 closed).
- The decision accepted with what was built.

### 11.1 As built (C6)

- **Words.** The word lists gained `signup` (whole messages: "I wan open account", "open account", "new business"), `plan`, and the prefixes `connect` and `invite`, in the five languages (drafts for review outside English, as the rest). A stranger's signup phrase in their language starts the sign-up; a linked person's English stays the existing words, so "register" does not start a second business by accident. Every message added in C1 to C5 is in the five catalogues.
- **The model** routes a stranger's free text to sign-up only (`intent.Stranger`), in any language, at most five times an hour per chat, on the platform's account: there is no business to charge or record against. Members' routing gained `connect` and `plan`; `invite` is left to the words, since its number and role must be typed.
- **Templates and Flows.** `taskiem_invite` is in the library and the registry (C5); the `taskiem_connect` Flow is in `docs/whatsapp-flows/` (C2).
- **Docs.** The owners' guide's [Start from WhatsApp](../guide/start-from-chat.md); the ways in side by side in [onboarding](../onboarding.md); [phase 4 status](../phase-4-status.md) and the [chat-first strategy](../strategy/chat-first-nigeria.md#32-three-product-gaps-that-keep-the-chat-from-being-chat-first) updated.

## 12. Security model

| Concern | Control |
| --- | --- |
| Someone signs up as another person | The channel proves the identity: WhatsApp delivers from the number; Telegram's contact must be the sender's own; Slack's user is in the installing workspace. No account is ever joined by a claim (an email typed, a name) without the existing proof: the code, the Telegram link, the email link |
| Mass sign-ups | Off by default per channel; per identity, per day and per hour limits counted in the database; the country list; names checked as on the web; every outcome counted |
| A recycled phone number | A chat-born account's number is its only credential until it adds an email, and operators reissue silent numbers, so a number's new holder writing to Taskiem would otherwise land in the old holder's businesses, not at a sign-up; a number linked after web sign-up is at the same risk. Every binding, however it was made, silent for 90 days (`TASKIEM_BINDING_DORMANT_DAYS`) is dormant: the next message gets no business until the person re-proves it, with the chat PIN (§6.3) or, if the account has an email, §10.1's link; with neither set, the owner's other channel or the web is the way back, and after a further 90 days the binding is removed and the number is a stranger again. A dormant binding also receives nothing but the prompt to re-prove: messages that carry the business's data (summaries, reminders, the low list, invoices' news) are held until it is proven again, approval requests go to the approver's other channels or the web as when no channel is linked, and the owner is told on their other channel or by email that the number went dormant. Money actions and approvals from a dormant binding wait for the same proof |
| Secrets in chat | Keys are typed only in the Flow (encrypted to Taskiem), the Telegram Mini App (straight to Taskiem) or the connect page. A key sent as a message is masked before it is kept, and the person is told to replace it |
| A connect link that leaks | One connection, one business, 15 minutes, once; its page names the business; the chat says what was connected (the service and the account it signed in as), so a wrong one is seen; making it needs `secret.manage` still held when it is opened |
| OAuth from a link | 0034's state, browser cookie and PKCE, unchanged; the link only starts it |
| Telegram Mini App data | `initData` checked with the bot token's derived key, fresh (`auth_date` within ten minutes), and its user the account the form was sent to; only then is a form accepted |
| Slack identity | Events and clicks verified with the app's signing secret; a business never grows by workspace membership alone; Slack's email is not taken as confirmed and never merges accounts |
| Cross-tenant routing | Slack events reach a business only through the workspace's connections (0051's `taskiem_app_route_event`); the pre-sign-up record is a person's, reached through narrow functions, and holds no tenant data |
| Consent | Nothing is made before **Yes** to the terms; the version and channel are recorded |
| Audit | `tenant.create` and `terms.accept` with the channel; `connection.create` with `channel` and `via: connect_link` or `form`; `member.invite` and `member.join` with the channel; `auth.link` when an existing account is linked from chat |

## 13. C0: people and decisions

| Item | Who | Blocks |
| --- | --- | --- |
| **C0-1 Chat-only tier.** Whether chat-born businesses start on the same trial and plans as the web's, or a cheaper chat-only tier ([strategy §5.1](../strategy/chat-first-nigeria.md#5-decisions-to-take-early)) | Founder | Nothing: the trial works either way |
| **C0-2 Terms and privacy texts**, short enough for a chat message with a link, NDPA-reviewed; their version numbers | Founder, counsel (P4-B4) | C1 in production |
| **C0-3 Where sign-up opens.** The countries (`234` first), and whether Slack sign-up is open to every workspace or by invitation | Founder | C1, C4 |
| **C0-4 WhatsApp.** Publish the `taskiem_connect` Flow in Taskiem's account; get `taskiem_invite` approved (W1, W2 first) | Operations | C2 on WhatsApp, C5 |
| **C0-5 Slack app.** The Events request URL `/channels/slack/events`, the `message.im` subscription, the **Messages** tab, `files:read`, public distribution on; a Marketplace listing later (Slack reviews it) | Operations | C3, C4 |
| **C0-6 Telegram.** Nothing new at BotFather: a Mini App opened from an inline button needs no setup there | — | — |

## 14. Milestones

| Id | Milestone | Size |
| --- | --- | --- |
| C1 | Sign up from WhatsApp and Telegram: the offer to strangers, the language choice, terms with **Yes**, four questions, the business made through the agent flow's function, three templates offered, `new business`, the chat PIN (WhatsApp; Telegram in C2), dormant bindings, the switches and limits, `terms_acceptances` (web and existing accounts too), `chat_signups`, the metrics by source | M |
| C2 | Connect from chat: **Connect** wherever chat linked to Connections, `connect <service>`, the `taskiem_connect` Flow, the Telegram Mini App (also for inputs and rows), the connect page and its links (OAuth and keys), the pasted-key guard, the build carrying on | L |
| C3 | Slack as a conversation: `/channels/slack/events`, DMs to `converse`, contacts, Block Kit buttons, modals, files | M |
| C4 | Sign up from Slack: the platform install, the business for a workspace, the installer's email unconfirmed and never merged, known workspaces | M |
| C5 | One person, many doors: linking an existing account from chat, invitations by chat (terms on **Join**, the number proven on Telegram), one account across WhatsApp and Telegram, **Add your email**, `plan` and **Pay** | M |
| C6 | Words in five languages, intents and routing, `taskiem_invite`, docs and the guide, the decision accepted | S |

In order, one branch and one PR for C1 to C6, as stock was. C1 and C2 are the pilot's prerequisites and are worth landing first if the pilot comes before the rest.

## 15. Testing

- **One harness, three channels.** The sign-up and connect conversations are written once and run on WhatsApp, Telegram and Slack (fake Graph API, fake Bot API, fake Slack Web API, as the channel tests already have); a capability that differs (Telegram's contact, Slack's install) has its own case.
- **The doors together.** An end-to-end test: sign up in WhatsApp, connect Paystack with a test key in the Flow, publish and run a template; then `sign in`, set a password, and see the same business in the web app; then sign up a second business on the web form with another person and invite the first by number. The web signup e2e (`onboarding.spec.ts`) and the agent tests unchanged.
- **Secrets.** A test sends keys as chat messages on each channel and searches every table that keeps conversations, outbox messages and audit details for them.
- **Abuse.** The limits per identity, per hour and per country; a stranger's flood; a Telegram contact that is not the sender's.
- **Slack isolation.** Events from a workspace two businesses share reach only the linked member's conversation; an unlinked member's DM makes nothing but the join offer.
- **G4 by source.** Signup to first run measured for each way in; the gate for chat-born businesses is the same 15 minutes, and N's 10-minute median (G-N) is checked on chat-born ones in the pilot.

## 16. Risks and open questions

| Risk or question | What we do |
| --- | --- |
| Meta treats sign-up in chat, or the invite template, as something it does not allow | Sign-up happens inside a conversation the person started, which is ordinary service messaging; the invite template is utility (an action the owner asked for). If refused, invitations go by link, as on Telegram |
| Strangers who are customers of a business write to the shared number and are offered a sign-up | Everything a customer can send is handled first (§4); the offer is one message with **Not now**, and is not repeated to the same number for a week |
| Slack-born owners are not who Taskiem is for first | Slack comes after WhatsApp and Telegram (C3, C4), and needs no change to them; it can stop at C3 (approvals plus a conversation for existing businesses) if C4 waits |
| A Telegram user without a phone they will share | They cannot sign up on Telegram (the proven number is needed for SMS, WhatsApp and limits); they can on the web or WhatsApp. Revisit if the pilot says many refuse |
| Two accounts for one person (signed up on the web, then in chat without saying so) | **I already use Taskiem** first in the offer; later, linking by the email link or by a shared number brings them to one account; businesses can be moved by inviting the other account and leaving (merging accounts is out of scope) |
| Mini Apps change | The Mini App is a page Taskiem serves; only its check of `initData` depends on Telegram, and the connect page is the fallback |
| Google refuses OAuth inside embedded web views (`403 disallowed_useragent`) | A connect link for a service that signs in must reach the phone's own browser, and Taskiem cannot rely on how a chat app opens links: developers report Telegram's in-app browser refused by Google, and WhatsApp's handling varies by phone and version. So the connect page for such a service does not start the consent where it was opened: it shows **Open in browser** (on Telegram, a Mini App button that calls `openLink`, which hands the link to the system browser), and starts the consent only in a browser that is not an app's embedded one, as far as a page can tell (best effort: Android's `wv` user agent, iOS without a Safari token); **Open in browser** is always shown, not only when the page thinks it needs to be, so a wrong guess costs one tap. Checked on real phones, both platforms, in the pilot |

## 17. Out of scope

Merging two existing accounts; a business's own Telegram bot or own Slack app; sign-up on a business's own WhatsApp number; USSD sign-up (story 10, a later plan); Meta's Embedded Signup for own numbers (W3); signing up by voice note before the first question (voice works once the questions start, where it is on); Microsoft Teams.
