# WhatsApp

Taskiem's own WhatsApp number is a client of the platform (spec 11): people who link their number to their account approve requests with buttons, ask what ran and what failed, and start workflows from a chat. Every action goes through the same permissions, approval policies, plan limits and audit log as the web app. Inputs are filled in WhatsApp forms (Flows), approvals whose policy allows it are confirmed with a WhatsApp PIN, an organisation can use its own WhatsApp number, template messages are counted against the plan, and an organisation can offer a public self-service menu on its number. These are milestones A1 and A2 of [Phase 3](phase-3-status.md).

This is separate from the [`whatsapp@1` connector](integrations/whatsapp.md), which tenants use inside their own workflows with their own WhatsApp Business credentials.

Built from Meta's public documentation only, under the [clean-room policy](clean-room-policy.md): the WhatsApp Cloud API (developers.facebook.com/docs/whatsapp/cloud-api: messages, interactive reply buttons, templates, authentication templates, webhooks and their signatures), read 6 October 2026; and, read 7 October 2026, WhatsApp Flows ([implementing endpoints](https://developers.facebook.com/docs/whatsapp/flows/guides/implementingyourflowendpoint), [endpoint error codes](https://developers.facebook.com/docs/whatsapp/flows/reference/error-codes), [sending a Flow](https://developers.facebook.com/docs/whatsapp/flows/guides/sendingaflow), [receiving the response](https://developers.facebook.com/documentation/business-messaging/whatsapp/flows/guides/receiveflowresponse), [components](https://developers.facebook.com/docs/whatsapp/flows/reference/flowjson/components), [changelog](https://developers.facebook.com/docs/whatsapp/flows/changelogs)), [Flows encryption](https://developers.facebook.com/docs/whatsapp/cloud-api/reference/whatsapp-business-encryption) and [pricing](https://developers.facebook.com/docs/whatsapp/pricing); and, read 26 October 2026, the Business Management API for templates ([creating templates](https://developers.facebook.com/docs/whatsapp/business-management-api/message-templates/), [template components](https://developers.facebook.com/documentation/business-messaging/whatsapp/templates/components), [authentication templates](https://developers.facebook.com/docs/whatsapp/business-management-api/authentication-templates/), [template management](https://developers.facebook.com/documentation/business-messaging/whatsapp/templates/template-management) and the [`message_template_status_update` webhook](https://developers.facebook.com/documentation/business-messaging/whatsapp/webhooks/reference/message_template_status_update)).

## For operators: setting up the platform number

1. In the Meta App Dashboard, create a Business app with the WhatsApp product, linked to Taskiem's WhatsApp Business Account. Business verification is needed for production volumes ([needs people](needs-people.md#phase-3), W1).
2. Register the platform number and note its **Phone number ID**.
3. Create a system user with `whatsapp_business_messaging` and `whatsapp_business_management` (the second submits and reads [templates](#templates)) and generate a token for it. Note the **WhatsApp Business Account ID** too.
4. Copy the app's **App secret** (App settings > Basic). Choose a **verify token** (any random string).
5. Submit the [templates](#templates) for approval (W2): `taskiem whatsapp templates submit all` once the settings below are in place, or by hand in WhatsApp Manager under the same names. Until a template is approved, its message goes out inside a person's 24-hour window only, and falls back outside it.
6. Put the settings in the deployment's Secret (`existingSecret` in the Helm chart); never in chart values or the database:

   | Variable | |
   | --- | --- |
   | `TASKIEM_WHATSAPP_PHONE_NUMBER_ID` | Turns the channel on |
   | `TASKIEM_WHATSAPP_ACCESS_TOKEN` | The system user's token |
   | `TASKIEM_WHATSAPP_APP_SECRET` | Verifies webhook signatures |
   | `TASKIEM_WHATSAPP_VERIFY_TOKEN` | Answers Meta's subscription handshake |
   | `TASKIEM_WHATSAPP_TOKEN_KEY` | 32 bytes, base64 (`openssl rand -base64 32`): signs decision tokens. Same value on every `api` and `edge` pod. Rotating it voids buttons already sent |
   | `TASKIEM_WHATSAPP_WABA_ID` | The WhatsApp Business Account the number belongs to: where the [templates](#templates) are submitted and their statuses tracked. Without it the registry is off for the platform number and every template is sent as before, Meta refusing the unapproved (not secret) |
   | `TASKIEM_WHATSAPP_TEMPLATES_ASSUME_APPROVED` | Development and tests only (`true`): every template counts as approved without asking Meta, so the e2e stack sends them to its fake Graph API. Never on a deployment that talks to Meta |
   | `TASKIEM_WHATSAPP_DISPLAY_NUMBER` | The number as people should save it, shown on the Account page and used by [update links](updates.md) (not secret) |
   | `TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE` | Language code the templates were approved in (`en`) |
   | `TASKIEM_WHATSAPP_DEFAULT_COUNTRY` | Calling code for numbers typed with a leading 0 (`234`) |
   | `TASKIEM_WHATSAPP_FLOWS_PRIVATE_KEY` | Optional: the Flows endpoint's RSA private key (PEM), turning on [forms and the PIN](#flows) |
   | `TASKIEM_WHATSAPP_GRAPH_URL` | Tests only: a fake Graph API. A loopback IP address here is the only way the platform reaches loopback through the egress guard |

   With `TASKIEM_WHATSAPP_PHONE_NUMBER_ID` set, the first five are required and the process refuses to start without them.
7. In the App Dashboard > WhatsApp > Configuration, set the **Callback URL** to `<public URL>/channels/whatsapp` and the **Verify token**, then subscribe to the **messages** and **message_template_status_update** fields (the second tells Taskiem when Meta approves, rejects or pauses a template). The ingress sends `/channels/` to the `edge` role (with `--role all`, the API serves it).
8. `TASKIEM_PUBLIC_URL` must be set: links in messages (runs, step-up) point at it.
9. For forms and the PIN: generate the Flows key pair, upload the public key to the number, and create and publish the [two Flows](#flows) with the endpoint `<public URL>/channels/whatsapp/flows`.

Which role does what:

| Role | WhatsApp work |
| --- | --- |
| `edge` | `GET /channels/whatsapp` (handshake) and `POST /channels/whatsapp` (messages, and template status updates): verifies, deduplicates and answers each message; the same under `/channels/whatsapp/n/{phone number id}` for own numbers on their own Meta apps; the Flows data endpoint `POST /channels/whatsapp/flows[/{phone number id}]` |
| `api` | Account > WhatsApp (binding, PIN), Settings > WhatsApp number (own number, public menu, templates), the step-up page, the notifier: approval requests to approvers, and how runs started from chat ended (every 5 seconds), and the hourly check of template statuses with Meta |
| `scheduler` | Alerts to WhatsApp alert channels |

Outbound messages go to `graph.facebook.com` through the egress guard. Sends are not retried: a message that fails is recorded (an approval request's error is kept with it) and the person can ask again (`approvals`).

## For people: using Taskiem on WhatsApp

**No account yet?** Where the operator turned it on, write to Taskiem's number and choose **Start my business**: the business and your account are made from the chat ([signing up from chat](onboarding.md#signing-up-from-chat)).

**Link your number.** Under Account > WhatsApp, enter your number with its country code and confirm it is you (passkey, authenticator code or password, as when adding a passkey). Taskiem sends a 6-digit code to that number on WhatsApp: type it on the page, or send it back to Taskiem's number. The code works for 10 minutes; five wrong codes spend it, and at most three codes are sent an hour. A number can be linked to one account, and an account has one number. Unlink it on the same page.

**What you can send:**

| Message | What happens |
| --- | --- |
| `help` | What you can do here, given your roles |
| `status`, `what failed today?` | Runs in the last 24 hours by status, the latest failures, and approvals waiting for you (needs `run.read`) |
| `run <workflow>` | Matches the name of a workflow deployed in `prod` (or its id); sends a form for its required inputs (or, where forms are not set up or the inputs do not fit one, asks each in turn), checked against the workflow's input schema; then shows exactly what will run and waits for **yes**. Needs `run.start`; plan limits apply. You hear how the run ended |
| `approvals` | Requests waiting for you, sent again with fresh buttons (three at most) |
| `build <what to automate>`, or just the goal ("every Friday text my customers who owe me") | Drafts a workflow with AI, shows it as plain steps, asks for any missing details, and saves it as a draft after your **yes** ([below](#build)). Needs `workflow.edit` |
| `tables` | Your organisation's [data tables](data-tables.md), numbered, with how many rows each has (an estimate, when the project answers in time). Needs `table.read` |
| `add to <table>` | Matches a table by name or title (a numbered pick when several match); sends a form for its columns (or asks each in turn in chat), checks every value as the Tables page does, shows the row with personal values masked, and adds it after your **yes** ([below](#data-tables)). Needs `table.edit` |
| `invoice <name> <amount> [for <what>]` | An invoice to a customer (`invoice Ada 25,000 for 5 bags of rice`, `bill Chi 10k`): asks for the customer's phone number or email, shows the invoice with the address masked, and sends it by SMS or email after your **yes** ([invoices](invoices.md)). The name and address wait sealed in the session. In another language that is on, its own words work too ([below](#invoices-and-money-in-other-languages)). Needs `invoice.manage` |
| `spent <amount> on <what> [at <where>]`, `expense <amount> <what>`, `cash in <amount> [<what>]`, `cash out <amount> [<what>]` | Cash or an expense for the [money log](money-log.md) (`spent 12k on diesel at Total`, `cash in 20,000 till`): the amount as for an invoice (`₦`, commas, `k`), the vendor after the last `at`, and for an expense a category from a short word list (diesel, petrol, fuel, gas: `fuel`; rent: `rent`; salary, wages: `salary`; transport, fare, taxi, bus, okada, keke: `transport`; food, lunch, breakfast, dinner: `food`; data, airtime, recharge: `phone`; anything else `other`). Shown (`Record ₦12,000 spent on diesel at Total (fuel)?`) with **Yes** and **No**, and recorded on **yes** as an `entered` row through `cash`, attributed to you; the confirmation's key makes a repeated **yes** record it once. In another language that is on, its own words work too ([below](#invoices-and-money-in-other-languages)). Needs `invoice.manage` |
| `find <table> <text>` | Up to five rows of the table whose first text column contains the text (or `column:text` for another column), as short lines with personal values masked, and how many more there are. Needs `table.read` |
| `make my page`, `my page`, `add product Jollof rice 2000` (or a photo with that caption), `price`, `hide`, `show`, `stock`, `orders`, `order <ref> ready`, `hours mon-sat 8-6`, `add service`, `close shop`, `open shop`, `qr` | Set up and run the organisation's [storefront](storefronts.md#from-chat): each change shown and confirmed with **yes**. Needs `storefront.manage` (and `table.edit` for the catalogue) |
| `add 50 cartons of Indomie at 8,500`, `stock indomie`, `how many bags of rice do I have?`, `stock`, `adjust rice -2 spoilt` | The shop's [stock](stock.md#in-chat): add or top up an item, its count and last change, what is low, an adjustment with a reason; each change shown and confirmed with **yes**. Needs `table.read` to see and `table.edit` to change |
| `bookings`, `today`, `booking <ref> cancel`, `move <ref> to Sat 11:00`, `off 25 Dec`, `refund <ref>` | The storefront's [bookings](storefronts.md#changes). Needs `storefront.manage` |
| `switch <organisation>` | Work in another organisation you belong to; `switch` alone lists them |
| `cancel` | Drops what is in progress |
| `language`, `language <name>` | Which languages are on, or choose one (`language yoruba`, `language english`), where an operator or the organisation turned languages beyond English on ([Languages](languages.md); drafts until native speakers review them) |
| A voice note | Where voice notes are on (a beta): transcribed and read as if typed, to start something; the reply says what was heard. Answers and **yes** are typed ([Languages](languages.md#voice-notes)) |

On the shared number every message from Taskiem starts with the organisation's name in brackets: the number is shared by every organisation on this Taskiem. An organisation with [its own number](#own-numbers) writes to you from that number, without the brackets, and messages you send to it act in that organisation only (`switch` does not apply there).

**Forms.** Where inputs are needed, Taskiem sends a form: tap **Fill in**, enter the details, tap **Continue**. Answers that do not fit are marked in the form. Nothing starts until you reply **yes** to the summary in the chat. Writing in the chat instead closes the form and asks the inputs one by one.

**Approving.** When a request needs your role, Taskiem sends its details with **Approve** and **Reject** buttons. A button works once, only from your number, for 24 hours or until the request times out. The same rules as in the web app apply: you cannot approve what you started, wrote or published; with distinct approvers you approve one level at most; a delegation lets you cover for someone. When the policy needs step-up:

- if the policy accepts a WhatsApp PIN and you have set one, Taskiem sends a form for this decision only: enter your PIN within 10 minutes;
- otherwise Taskiem sends a link: open it (signed in to Taskiem in that organisation) and confirm with your passkey or authenticator code within 10 minutes.

The decision is then recorded, and Taskiem confirms it in the chat.

**Your approval PIN.** Under Account > WhatsApp, once your number is linked and you have a passkey or authenticator, set a six-digit PIN (not one digit repeated or a run like 123456), confirming with your passkey or authenticator code. Five wrong PINs in 15 minutes lock it for 15 minutes; meanwhile approvals send the web link. Remove it on the same page; unlinking your number removes it too.

**What messages never show.** Secrets, and personal data unmasked: account and card numbers show their last four digits, phone numbers their country code and last four, BVN and NIN not at all, names their first letter, and anything under a field named like a credential is hidden. The full details stay in the web app, behind its permissions.

**Matching payments.** See [below](#matching-payments): a payment Taskiem could not match to an invoice on its own comes with **Yes, match** and **No**.

**Yesterday's money.** Owners get a summary every morning from 08:00 Lagos: money in, settled and pending, by channel, money out, invoices paid and leaks opened, with a link to the Money page; a quiet day sends nothing. Outside the 24-hour window it comes as the `taskiem_daily_summary` [template](#templates) with an **Open** button (as the alert template, cut to one line, until that one is approved). See [Money reports](money-reports.md#the-daily-summary-in-chat).

## Matching payments

When money comes in that Taskiem cannot match to an invoice by itself (a transfer with no reference Taskiem gave, a POS sale, a statement line), it suggests up to three open invoices it may be for ([the spec](spec/invoices-and-money-log.md#82-suggested)). Unless the business has automatic matching on and the best suggestion is strong and clear, every member who holds `invoice.manage` and has linked WhatsApp or Telegram gets one message about the payment:

> [Ada Foods] ₦25,000 came in on 23 Oct 14:05 through Paystack (main).
>
> Is this for INV-0104, ₦25,000 due from the customer? Same amount as what is due on INV-0104 (₦25,000), the payer's name matches the customer's, 2 days after INV-0104 was sent.

with **Yes, match** and **No**. The time is Lagos time; the reasons are the matcher's, in English. Names are never shown: neither the payer's nor the customer's (the web app shows them masked too).

- **Yes, match** records the match as confirming the suggestion in the web app does (`how: suggested`, by you), and answers "Matched to INV-0104. It is now paid." or "... It is now part-paid (₦x still due)."
- **No** turns the suggestion down (it is not offered again) and shows the next possible invoice for the payment, if there is one; otherwise the payment is left to match by hand in the web app, with the link.
- A suggestion already decided, by you, another member or in the web app, answers "Already matched to INV-0105" or "Already turned down".

A button works only from the chat it was sent to, for a member of that business who holds `invoice.manage` when pressing it; its payload is signed (below). Exact matches (a payment into an account Taskiem gave for the invoice) and automatic matches ask no one. The question goes through the [outbox](#delivery-and-retries) (kind `match`) and shows the payment's best open suggestion as it stands when sent. Outside WhatsApp's 24-hour window it goes as the `taskiem_match_question` [template](#templates), the same facts in English with **Yes, match** and **No** as quick replies carrying the same signed buttons, once Meta has approved it in the number's account; until then it is not sent and the suggestion waits in the web app.

## Invoices and money in other languages

Where Pidgin, Yoruba, Hausa or Igbo is on ([Languages](languages.md)), a person in that language can also start an invoice, an expense or cash with its own words; the English ones work in every language. What follows the words is read as in English (the name, then the amount, then "for" and what it is for), with the language's word for "for" as well as English's. The vendor of an expense comes after English `at` only. Yes and No are the language's usual words (`bẹ́ẹ̀ni`, `na'am`, `ee`, `yes o`) or the buttons.

| | Invoice | Expense | Cash in | Cash out | "for" |
| --- | --- | --- | --- | --- | --- |
| Pidgin | `send invoice give`, `send bill give` | `I spend`, `I don spend` | `cash enter`, `money enter` | `cash comot`, `money comot` | `for` |
| Yoruba | `ìwé owó`, `ìwé ìsanwó`, `fi ìwé owó ránṣẹ́ sí` | `mo ná`, `mo ti ná`, `ìnáwó` | `owó wọlé` | `owó jáde` | `fún` |
| Hausa | `takardar kuɗi`, `aika takardar kuɗi ga` | `na kashe`, `kashewa` | `kuɗi ya shigo` | `kuɗi ya fita` | `don` |
| Igbo | `akwụkwọ ụgwọ`, `ziga akwụkwọ ụgwọ nye` | `emefuru m`, `mmefu` | `ego batara` | `ego pụrụ` | `maka` |

For example `ìwé owó Ada 25,000 fún àpò ìrẹsì 5`, `na kashe 12,000 don diesel at Total`, `ego batara 20,000 till`. Words are matched without case, accents or tone marks (`iwe owo` works) and after a polite word (`abeg`, `biko`, `don allah`). An expense or cash word counts only when an amount follows it at once, so a message such as "I spend the whole day for market" is ordinary chat. Every word here is an unreviewed draft, in `engine/lang/intents/<language>.json` (`invoice`, `expense`, `cash_in`, `cash_out` and `for_words`); a speaker reviews them as the rest of the list ([reviewing a language](languages.md#reviewing-a-language)).

## Data tables

`tables`, `add to <table>` and `find <table> <text>` work on the organisation's [data tables](data-tables.md) with the same permissions as the Tables page (`table.read` to list and find, `table.edit` to add) and through the same path: the connector's actions on the `data_tables` connection, the values checked by the same code, the connection reads recorded under the purpose `tables.chat`.

**Adding a row.** `add to customers` (the table's name or title; a numbered list to pick from when several match) collects one row. Where forms are set up, the columns come as a `taskiem_inputs` form, each column a field labelled with its title, required ones marked, number boxes for `integer` and `number` columns, a Yes/No list for `boolean`, a text box for the rest with a hint for dates, phone numbers and emails; a table with more than ten columns gets its first ten in the form (required ones first) and the rest asked in chat. Without forms, each column is asked in turn, numbered, and an optional column takes `skip`. An answer that does not fit (a phone without digits, a date not as `YYYY-MM-DD`, a whole number with decimals) is refused and the column asked again. Then Taskiem shows the row and waits for **yes**: nothing is added before that, and **no** or `cancel` drops it. The row is added under the `table.row.add` audit action (`channel: whatsapp`, the table and the row's id, never a value) and Taskiem replies with the row's id.

**What is masked.** A `phone` column shows its country code and last four digits, an `email` its first letter and domain, any other personal column its first letter (text) or nothing; the other columns pass through the same redactor as every message. The values of personal columns wait sealed in the conversation's state (or the form's row) until you confirm, as a run's `x-pii` inputs do, for 30 minutes at most. `find` shows rows the same way: the person has `table.read`, so the Tables page shows the full record, but a chat is not the page.

## Build

Spec 11.1 (Build, SME tier): describe an automation in a message and get a draft workflow back.

1. Send `build` and what you want, or write it as you would say it, starting with when it happens: "every Friday text my customers who owe me", "when a customer pays on Paystack send them a thank you", "remind me before salaries are due every month" (four words or more starting with *every*, *each*, *when*, *whenever*, *daily*, *weekly*, *monthly*, *remind me*, *text my*, *let me know*, *tell me when* and similar). Taskiem answers at once that it is working on it.
2. In the background, the [AI builder](ai.md) drafts the workflow under your permissions, starting from the [template library](templates.md) when a template fits, and checks it as if it were being published, then dry-runs it with every step mocked. This takes up to a couple of minutes; anything you send meanwhile gets "still drafting", and `cancel` drops it (the draft is then never sent).
3. Taskiem sends the workflow back **as plain numbered steps** ("1. Every Friday at 10:00 (Africa/Lagos time) 2. Read the debtors sheet (Google Sheets) 3. For each customer on the sheet: 3a. Text the customer a reminder of what they owe (Termii), only when its condition holds"), never JSON or expressions, with any warning (a payment without an approval, a failing dry run) and the variables to set (such as `owner_phone`).
4. If it started from a template and your message did not give every detail the template needs (the spreadsheet, how customers should pay), it asks for them **one at a time**, checks each answer (a time like 9am, a day like Friday, a number), and shows the steps again with your details.
5. It ends with **"Save this as a draft workflow? Reply yes or no."** (with buttons). Only **yes** saves, and only a **draft**: a new workflow in your name with the AI as co-author, audited as `ai.save` with `channel: whatsapp`, with a link to open it. **no** saves nothing.
6. Then chat goes on (no-technical-steps N6, decision N0-7): the tables the draft proposed are made (with `table.edit`), the settings it reads are asked for **one at a time** and saved in prod (with `secret.manage`), and what still blocks a publish is listed in plain words, a connection with a link to the one screen that makes it. With nothing blocking, Taskiem offers **"Publish it now?"**. **yes** publishes it as the web app does: `workflow.publish`, [readiness](readiness.md), the publishing checks, four-eyes (a request waits for a second person under Approvals) and the audit (`workflow.publish`, with the channel). An organisation that reviews changes in Git publishes in the app. Later, `publish <workflow name>` publishes the newest draft of that workflow the same way.

If the draft cannot pass Taskiem's checks, Taskiem says so and suggests describing it differently or building in the web app. Building needs `workflow.edit` (checked when you ask, when you answer and when you save), AI configured on the deployment, a plan with the `ai` feature (as the web app's AI building), and this month's AI budget: over budget, Taskiem says so before any model call. Builds are limited per number (three, then one every 20 seconds) and share the organisation's limit with the web app (five, then one every 20 seconds). Your message is redacted before it reaches the model and stored redacted, like every AI build ([data handling](ai.md#data-handling)); details you type for a template are not personal data by design (phone numbers and emails are tenant variables) and wait in the conversation's state, under the organisation's row-level security, for 30 minutes at most.

**People outside a business** (customers, staff who don't sign in) can get a business's workflow messages from the platform number, or the business's own number, without a Taskiem account: they open its link and send the message it fills in ([updates](updates.md)). To them the number only sends updates, and understands STOP and START. `TASKIEM_WHATSAPP_DISPLAY_NUMBER` must be set for those links. A business's invoices to them come there too, with **Pay by transfer** and **Open invoice** buttons, which they may press ([invoices](invoices.md#on-whatsapp-and-telegram)); outside the 24-hour window the invoice goes as the `taskiem_invoice` [template](#templates) once approved, and by SMS and email only until then.

## Signing in on the web

`sign in` (or `web link`) sends a one-time link, good for an hour, to choose a password and sign in to the web app: the owner of a business a [field agent](agents.md) made has none yet, and the chat is already bound to them. It is audited (`auth.signin_link`); an organisation held to single sign-on gets its sign-in page instead.

**An email, the plan and paying** ([onboarding](onboarding.md#one-person-many-doors), the same on Telegram and Slack). `email` says the account's email, or, for an account made in chat, how to add one: `email ada@example.com` sends a confirmation link there, and opening it (no sign-in needed) makes it the account's email, confirmed. `plan` says the business's plan, the trial's days left or when it renews, and the price, with **Pay** for someone who manages billing; **Pay** sends a checkout link (card or bank transfer), asking first for an email to send receipts to when the account has none (`pay ada@example.com`).

## Templates

WhatsApp only delivers free-form messages within 24 hours of the person's last message. Outside that window Taskiem sends a template, so every notification type has one. They are defined in code (`engine/whatsapp/templates.go`) and submitted to Meta under these names, in the language of `TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE`, with these bodies and buttons (the [registry](#the-template-registry) below submits them and follows their approval):

| Name | Category | Body | Buttons | Sent when |
| --- | --- | --- | --- | --- |
| `taskiem_otp` | Authentication | `{{1}} is your verification code. For your security, do not share this code. This code expires in 10 minutes.` (Meta's authentication format, with its security recommendation and expiry options) | Copy code | Binding a number |
| `taskiem_approval_request` | Utility | `[{{1}}] Approval needed: {{2}}. {{3}} Environment: {{4}}. Tap Approve or Reject, or decide in Taskiem.` | Quick replies **Approve**, **Reject** (the payloads are decision tokens) | An approval is waiting for an approver whose window is closed |
| `taskiem_stepup_link` | Utility | `[{{1}}] This decision on {{2}} needs your passkey or authenticator code. Confirm it in Taskiem within 10 minutes: {{3}}` | — | Step-up (normally inside the window, since the person just tapped a button) |
| `taskiem_run_failed` | Utility | `[{{1}}] {{2}} failed in {{3}}. Details: {{4}}` | — | Alert rule `run_failed`; a run started from chat failed |
| `taskiem_run_completed` | Utility | `[{{1}}] {{2}} completed in {{3}}. Details: {{4}}` | — | A run started from chat completed |
| `taskiem_needs_reconciliation` | Utility | `[{{1}}] {{2}} needs reconciliation in {{3}}: a call's outcome is unknown and someone must check with the provider. Details: {{4}}` | — | Alert rule `needs_reconciliation` |
| `taskiem_approval_waiting` | Utility | `[{{1}}] {{2}} Details: {{3}}` | — | Alert rule `stuck_approval` |
| `taskiem_alert` | Utility | `[{{1}}] {{2}} Details: {{3}}` | — | Every other alert rule; a `notify@1` **Tell me** message (its title and message on the one line); the daily money summary and the school's daily close until their own templates are approved |
| `taskiem_update` | Utility | `[{{1}}] Update on {{2}}: {{3}}` | — | A workflow's message to someone outside the business who asked for its updates ([updates](updates.md)); {{2}} is what they asked about. Also a school family message with no template of its own |
| `taskiem_invoice` | Utility | `[{{1}}] Invoice {{2}}: {{3}} to pay. Tap Pay by transfer for an account number to pay into, or Open invoice to see the invoice and pay.` | Quick reply **Pay by transfer** (the payload is the signed invoice button, as inside the window); URL **Open invoice** → `https://<form host>/i/{{1}}` (the invoice page's token) | An invoice to a customer who gets the business's updates on WhatsApp ([invoices](invoices.md#on-whatsapp-and-telegram)); {{2}} the number, {{3}} what is due |
| `taskiem_invite` | Utility | `[{{1}}] {{2}} invited you to work with {{1}} on Taskiem, as {{3}}. Tap Join to accept. If you do not want this, tap Stop and nothing more is sent.` | Quick replies **Join** and **Stop** (payloads name the invitation) | An owner's `invite +234… as <role>` from chat, to a number that may never have written to Taskiem ([onboarding](onboarding.md#one-person-many-doors)); the owner's invitation is the opt-in |
| `taskiem_delivery_offer` | Utility | `[{{1}}] A delivery for you: {{2}}. Tap Accept to take it or Can't to pass it on.` | Quick replies **Accept** and **Can't** (the payloads are the signed buttons, as inside the window) | An order offered to one of the business's riders outside the window ([dispatch](dispatch.md)); {{2}} the order, the area, the items and the fee |
| `taskiem_match_question` | Utility | `[{{1}}] {{2}} came in on {{3}} through {{4}}. Is this for {{5}}, {{6}} due? {{7}} Tap Yes, match or No, or decide in Taskiem.` | Quick replies **Yes, match**, **No** (the payloads are the signed match buttons) | A payment to [match](#matching-payments), to a member with `invoice.manage`; {{2}} the amount, {{3}} when (Lagos), {{4}} the channel, {{5}} the invoice or order, {{6}} what is due, {{7}} the matcher's reasons (and the split, for more than is due) |
| `taskiem_fee_reminder` | Utility | `[{{1}}] School fees, {{2}}: {{3}} is outstanding on {{4}}'s fees, due {{5}}. Pay by transfer to the account number {{6}}. Your fees page has the details and lets you ask for a payment plan.` | URL **Open fees page** → `https://<form host>/s/{{1}}` (the family page's token) | A school [reminder](schools.md#the-ladder) to a family; {{2}} is `reminder 1 of 3`, `reminder 2 of 3` or `final notice`, {{3}} the balance, {{4}} the children, {{5}} the due date, {{6}} the account number. Meta approves fixed wording, so the school's own rung wording goes inside the window and by SMS only |
| `taskiem_receipt` | Utility | `[{{1}}] Receipt {{2}}: we received {{3}} from {{4}} towards {{5}}. {{6}} Tap Check receipt to confirm it is valid.` | URL **Check receipt** → `https://<form host>/r/{{1}}` (the receipt's token) | A [receipt](schools.md#receipts) to a family, sent by `schools@1` `send_receipt` (the `school-receipt` workflow template); {{4}} the child, {{5}} the invoice, {{6}} what is left on it or that it is paid. By SMS, with the same facts and the check link, until approved |
| `taskiem_account_number` | Utility | `[{{1}}] {{2}}'s own account number for payments is {{3}} at {{4}}, in the name {{5}}. Pay by transfer to it: the money is matched by itself and a receipt follows.` | URL **Open fees page** → `https://<form host>/s/{{1}}` | A student's own [account number](schools.md#account-numbers) to the family, sent by `schools@1` `send_account_number` (the `school-issue-accounts` workflow template, and **Send the account number to the family** on the student's page); {{2}} the child, {{3}} the number, {{4}} the bank, {{5}} the name to expect. By SMS until approved |
| `taskiem_plan_decision` | Utility | `[{{1}}] Your payment plan request for {{2}}'s school fees: {{3}}. {{4}} Open your fees page for the details.` | URL **Open fees page** → `https://<form host>/s/{{1}}` | A [payment plan](schools.md#payment-plans) decided; {{2}} the child's admission number, {{3}} `approved` or `not approved`, {{4}} the instalments in one line (`2 instalment(s): ₦20,000 by 15 Nov 2026; ₦25,000 by 15 Dec 2026.`) and the note |
| `taskiem_family_link` | Utility | `[{{1}}] {{2}} Tap Open fees page for your family's school fees page: each child's fees, payments and account number. The link works for {{3}} days.` | URL **Open fees page** → `https://<form host>/s/{{1}}` | A fresh [family page](schools.md#the-family-page) link: **Send me a new link** on an expired page, or **Send a link** by staff; {{2}} the staff's message or `Here is a new link to your family's fees page.`, {{3}} the link's life |
| `taskiem_daily_summary` | Utility | `[{{1}}] Yesterday's money, {{2}}: {{3}} Tap Open for the Money page.` | URL **Open** → `<public URL>/money` | An owner's [daily money summary](money-reports.md#the-daily-summary-in-chat); {{2}} the day, {{3}} the summary line |
| `taskiem_daily_close` | Utility | `[{{1}}] Daily close, {{2}}: {{3}} Tap Open for every item and where to fix it.` | URL **Open** → `<public URL>/schools/overview` | A school's [daily close](schools.md#the-daily-close) to the owners: the `school-daily-close` workflow template's `notify@1` **Tell me** names it as its `template` ([Tell me](integrations/notify.md#telling-the-businesss-people-tell)); {{2}} the day, {{3}} the close in one line (`daily_close`'s `summary`). As `taskiem_alert` cut to one line until approved |

Variables are filled with one-line text (Meta refuses new lines in them): the request summary in `taskiem_approval_request` joins its masked fields with semicolons. Every body has fixed text around its variables, and a URL button's dynamic part is the token at the end of the page's link, so a link not under the button's address is never sent through it. The button addresses are the deployment's: `<form host>` is the public form host (where invoice, receipt and family pages are served) and `<public URL>` is `TASKIEM_PUBLIC_URL`; Taskiem fills them in when it submits. Meta charges per template message: see [template costs](#template-costs). An organisation with its own number must have the same templates approved in its own WhatsApp Business Account ([below](#the-template-registry)).

### The template registry

Each WhatsApp Business Account Taskiem sends from, the platform number's (`TASKIEM_WHATSAPP_WABA_ID`) and each own number's (the account id given when it was connected), has a registry of what Meta knows of each template (`whatsapp_templates`, migration 00224): its status, Meta's id for it, the reason for a rejection or pause, when Taskiem submitted it and when the status was last checked. Statuses:

| Status | Meaning |
| --- | --- |
| `not_submitted` | Meta does not hold a template of that name in that language (never submitted, or deleted) |
| `pending` | Submitted; Meta is reviewing it (or an appeal is) |
| `approved` | Meta approved it: it is sent outside the window |
| `rejected` | Meta refused it; the reason is kept (`INVALID_FORMAT`, `TAG_CONTENT_MISMATCH`, …) and shown |
| `paused` | Meta paused it for low quality; it is not sent until Meta reinstates it |
| `disabled` | Meta disabled or locked it |

**Submitting.** `taskiem whatsapp templates submit NAME` (or `all`, the ones not yet submitted) submits from the platform account through the Business Management API (`POST /{waba-id}/message_templates`, with the name, language, category, the body and an example for each variable, and the buttons; `taskiem_otp` in Meta's authentication format). For an own number, Settings > WhatsApp number lists each template with **Submit** beside the ones Meta does not hold; the token given when the number was connected needs `whatsapp_business_management` for this. The founder may submit by hand in WhatsApp Manager under the same names instead: the registry learns the status either way. `taskiem whatsapp templates list` shows the platform account's registry.

**Statuses.** Meta's decision arrives on the `message_template_status_update` webhook field (subscribe to it beside `messages`; a delivery is verified with the app's secret like every other and names the account, so an own number on its own Meta app gets its statuses on its own path) and is recorded in that account's registry. Taskiem also asks Meta (`GET /{waba-id}/message_templates`): when a number is connected, every hour on the `api` role, with `taskiem whatsapp templates check`, and with **Check now** on the Channels page, so a template approved while Taskiem was not listening, or submitted by hand, is known within the hour. The Channels page says in one line what cannot go out of the window until the templates not yet approved are.

**Sending.** Outside the window a template is sent only when its account's registry holds it `approved`; otherwise the message is not sent as a template (`ErrTemplateNotApproved`) and the caller does what it did before the template existed, recording why on the outbox message: an invoice went by SMS and email only, a matching question waits in the web app, a family message (a reminder, a receipt, an account number, a plan decision, a link) goes by SMS, the daily summary and a Tell me message with a template of its own (the school's daily close) go as `taskiem_alert`, the message marked sent with the reason. Meta's own refusals of a template (its errors 132000 to 132016) fall back the same way. An account not tracked (the platform number without `TASKIEM_WHATSAPP_WABA_ID`) sends every template and lets Meta decide, as before the registry. `TASKIEM_WHATSAPP_TEMPLATES_ASSUME_APPROVED=true` makes every template count as approved everywhere: for development and the e2e stack against a fake Graph API only. Template messages count against the allowance as before ([template costs](#template-costs)).

## Flows

WhatsApp Flows are forms inside WhatsApp. Taskiem uses three, published once in each WhatsApp Business Account that sends them (Taskiem's, and each own number's) under these names, with the Flow JSON in [`docs/whatsapp-flows/`](whatsapp-flows/) (generated by `engine/whatsapp`, and checked by `TestFlowJSONDocumented`; regenerate with `go test ./engine/whatsapp -run TestFlowJSONDocumented -update`):

| Flow | Screen | What it is | Used for |
| --- | --- | --- | --- |
| `taskiem_inputs` | `INPUTS` | A generic form of 10 slots, each a text box, a number box and a list, shown, labelled and required by the screen's data; **Continue** sends the form to the data endpoint | A workflow's inputs (`run <workflow>`), a data table's row (`add to <table>`), public menus |
| `taskiem_pin` | `PIN` | One six-digit passcode box under the decision's title and summary; **Confirm** sends it to the data endpoint | Step-up by the WhatsApp PIN |
| `taskiem_connect` | `CONNECT` | Up to five fields, each a password box (a secret) or a text box, labelled with the service's own help; **Connect** sends them to the data endpoint, which checks them with the service before saving | Connecting a key-based service from the chat ([below](#connecting-a-service-from-the-chat)) |

Flow JSON version 7.3, data API 3.0, one terminal screen each. In WhatsApp Manager create each Flow with these names, paste the JSON, set the endpoint URL (below), pass the health check and publish.

**From schema to form.** The required fields of a workflow's input schema (as for chat: `string`, `integer`, `number`, `boolean`, enums, `$ref` to the definition's types) go to slots in order: text fields to text boxes, integers and numbers to number boxes, enums and booleans to lists (Yes/No), with the field's title as the label (20 characters; the full title or description below it). Ten fields at most and lists of at most 200 entries; nested objects and arrays, or more fields, are asked in chat as before. A data table's columns go through the same builder, as a schema made from the columns, with optional columns left unrequired ([data tables](#data-tables)). The form is sent with the `navigate` action and its data, so it opens without a round trip; opening it again (`INIT`, `BACK`) is answered from the schema.

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

## Connecting a service from the chat

Where chat used to send a link to the web's Connections page (a draft's blockers when publishing, `make my page` without Paystack), it now says `connect <service>`, and that works on its own: `connect paystack`, `connect google sheets` (the service's id or name). It needs `connection.manage`, which owners hold. The connection is the business's `main` one in `prod` ([docs/spec/chat-way-in.md §7](spec/chat-way-in.md#7-c2-connecting-from-chat), decision 0058).

- **A key-based service that fits the form** (Paystack, Moniepoint, OPay, Termii and the rest: five fields at most, none spanning lines, no mode to choose by address) gets the `taskiem_connect` form, where Flows are set up. Each value is checked by its start (a public key is refused with the service's own words) and then with the service, before anything is saved; a refusal comes back in the form.
- **Anything else** gets a **connect link**: `https://<public url>/c/<token>`, one-time, for 15 minutes, for that service, business and person. It needs no web sign-in: the token is the authority, and the person must still manage connections when it is used. The page is the web's guided form for a key, or **Continue to Google** (or the service) for one that signs in, with the state, browser cookie and PKCE of [decision 0034](decisions/0034-oauth-connections.md); the provider's callback then ends on the connect page instead of Connections, with the way back to the chat. A refused key or a consent not finished leaves the link usable until its time is up; it is spent when the connection is made.
- **Afterwards** the chat says so ("Paystack is connected to Ada Foods"), audited as `connection.from_chat` with `via` (`connect_link` or `form`) and the channel. When the draft that was waiting is known (the one whose publishing listed the connection), the chat carries on with it: what it still needs, or **Publish it now?**
- **A key never travels as a message.** A message with a word starting the way a connector's secret does (`sk_live_`, `sk_test_` and the rest), or, while a connect link is waiting for that chat, any token-shaped message (one word of 20 or more letters, digits, `-` and `_`), is dropped before anything reads or keeps it, and the person is told to use the form and to replace the key at the service, since the chat app has it now.

## The chat PIN and dormant chats

**The chat PIN.** A person whose chat is their only credential (no password, passkey or authenticator: someone who [signed up from chat](onboarding.md#signing-up-from-chat)) sends `pin` and sets their six-digit PIN in the PIN form (`pin_set`; encrypted to Taskiem, never a message; weak PINs refused). Anyone else sets it on the web, behind their passkey or authenticator, as above. It is the same PIN (`whatsapp_pins`), so it also approves under `whatsapp_pin` policies. On Telegram it comes with the Mini App (C2).

**Dormant chats.** Operators reissue numbers that fall silent, so every chat binding, however it was made, silent for `TASKIEM_BINDING_DORMANT_DAYS` (90; `0` is off) is dormant: `taskiem_wa_numbers` and `taskiem_tg_accounts` leave it out, so it is sent nothing that carries a business's data (approvals go to the person's other channels or the web, as when no chat is linked), and its messages act on nothing. The next message gets the PIN form (`pin_wake`) when the person has a PIN, else how to link it again from the web; linking again, or the right PIN (wrong ones count toward the lock), wakes it (`binding.wake`). A binding dormant as long again is removed, and the address is a stranger again. The sweep runs with the notifier, at most hourly.

## Own numbers

An organisation can connect its own WhatsApp Business number (Settings > WhatsApp number, `secret.manage`): `PUT /v1/whatsapp/number` with the phone number id, WhatsApp Business Account id and a system user token with `whatsapp_business_messaging`; and, for a number on the organisation's own Meta app (the default), the app secret and a verify token of its choosing. Taskiem checks the token against the Graph API (`GET /{phone-number-id}`), uploads the Flows public key, stores the credentials in the organisation's vault (environment `_whatsapp`; reads recorded as connection reads) and the number in `whatsapp_numbers` (one number per organisation, one organisation per number). `DELETE` disconnects it. Both are audited (`whatsapp.number.connect`, `whatsapp.number.disconnect`).

In the organisation's Meta app, set the callback URL to `<edge URL>/channels/whatsapp/n/<phone number id>` with that verify token, subscribe to **messages** and **message_template_status_update**, set the Flows endpoint to `<edge URL>/channels/whatsapp/flows/<phone number id>`, and publish the two Flows in its account; the templates are submitted from the same page, which shows each one's status ([the registry](#the-template-registry); `GET /v1/whatsapp/number/templates`, `POST /v1/whatsapp/number/templates/{name}/submit`, `POST /v1/whatsapp/number/templates/check`, all `secret.manage`; a submission is audited as `whatsapp.template.submit`). A number connected with `own_app: false` is on Taskiem's app (as embedded signup would connect it): its webhooks and Flows come through `/channels/whatsapp` and `/channels/whatsapp/flows`.

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
| Utility | approval requests, step-up links, run outcomes, alerts, updates, invoices, matching questions, school messages, daily summaries and closes | Sent, counted as overage |
| Marketing | none today | Held back (`ErrTemplateBlocked`), counted as held back |

Approvals, codes, step-up and alerts never wait on a bill. Usage is in `GET /v1/limits` (`usage.whatsapp_templates_this_month`: `sent`, `by_category`, `overage`, `blocked`), Settings > Plan limits and `taskiem tenants limits`. There is no billing system: overage is recorded and reported for pass-through billing. Counts are taken when Meta accepts a send, not on delivery receipts.

## Public menus

An organisation with its own number may offer a menu to anyone who writes to it without a linked account (Settings > WhatsApp number, `PUT /v1/whatsapp/public-menu`, `workflow.publish`): up to nine workflows it marks public, each with a short label. A message gets the numbered menu; a number picks one; its inputs come as a form (`taskiem_inputs`, never asked in chat), checked against the schema; a summary and **yes**; then a run of the version deployed in `prod`, started as `whatsapp_public:<number>` (audited by the system actor with the number masked), and a reference. Nothing else is reachable: no status, no approvals, no other workflows. Limits: six messages then one every 10 seconds per number, three starts then one every 20 minutes per number, and per-organisation ceilings, then the plan limits. Menus are never offered on the shared number (`Server.WhatsAppPublic` remains the hook there).

## Alerts to WhatsApp

An alert channel of kind **WhatsApp** names members by email (Alerts > Channels). Each alert goes to those of them who have linked a number: as text when they wrote in the last 24 hours, otherwise as the template for the rule's kind. A delivery fails, with the reason, when none of them has a number. Otherwise the alert is handed to the [outbox](#delivery-and-retries), one message per member: the delivery is done once queued, and a member whose message fails is retried on their own, so the others never get it twice.

## Delivery and retries

Messages that must arrive go through a durable outbox (`whatsapp_outbox`, migration 00086): **approval requests**, **how runs started from WhatsApp ended**, **alerts** to WhatsApp channels, and workflows' **Tell me** messages ([notify@1](integrations/notify.md), kind `notice`, rendered from the step's input in the run's history), messages to people outside the business (kind `message`), and invoices to customers who linked a chat app (kind `invoice`, naming the invoice and the contact; dropped when the invoice is paid or void, the person stopped, or WhatsApp's window is closed with the invoice template not approved, the SMS and email having gone already), and payments to match (kind `match`, naming the money-in entry and the member; dropped when nothing is open for the payment any more, the member no longer holds `invoice.manage`, or WhatsApp's window is closed with the question template not approved), and owners' daily money summaries (kind `money_daily`, naming the summary and the owner; dropped when the person is no longer an owner or it waited more than a day; outside the window it goes as the summary template, or the alert template until that is approved), and a school's messages to a family's guardian (kind `school_family`, naming the sealed message and the guardian's contact; outside the window as the message's own template, or by SMS until it is approved). Each is one row per person, naming what to send (the approval, the run, the alert) and to whom (the person, not their number). The message is rendered at each attempt, from the record as it then stands: an approval request gets fresh decision tokens, so the database never holds message text, tokens or numbers.

- **Claimed once.** Each attempt claims its row (`FOR UPDATE SKIP LOCKED`, a five-minute lease), so two notifiers never send the same message at once. The `api` role's notifier drains the outbox every five seconds; an alert is also tried at once by the role that raised it.
- **Backoff.** A failed attempt waits 1, 4, 16, 64 and 256 minutes, then six hours, up to eight attempts. Ordering across messages is not kept.
- **Dead letters.** After the last attempt, or at once when waiting cannot help (a marketing template held back by the allowance, a message with no template outside the window), the message is **dead**. `GET /v1/whatsapp/outbox` (`alert.manage`) lists dead letters by default, or `?status=pending|sent|dropped`, with the person (by email), what it was about, the attempts and the last error (numbers masked); `POST /v1/whatsapp/outbox/{id}/retry` sends a dead letter again from a fresh set of attempts (audited, `whatsapp.outbox.retry`).
- **Dropped.** A message that became moot is dropped, not sent: the approval was decided or expired, the person no longer decides approvals, unlinked their number, or the alert or run is gone.
- **Not queued.** One-time codes, chat replies, hand-off links and the approvals a person asks for (`approvals`) are sent directly and not retried: the person is waiting and asks again, and a code or reply arriving minutes later would only confuse.

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
| Forged or misused invoice buttons | An invoice's buttons carry the business, invoice and contact under an HMAC-SHA256 with the same key; a press counts only from that contact's own number or account (not stopped), for an invoice sent to that contact's phone, while it is open ([invoices](invoices.md#on-whatsapp-and-telegram)) |
| Forged or misused match buttons | **Yes, match** and **No** carry Yes or No, the business, the suggestion and the member they were sent to, under an HMAC-SHA256 with the same key; a press counts only from that member's linked number or account, in a business they belong to, while they hold `invoice.manage` and the suggestion is open; the decision is the web app's (`matching.Matcher` `Confirm` and `Reject`) |
| Replay | A token's row is marked used when tapped; the Approve and Reject of one request share a notice, so either spends both. Hand-off links are used once |
| A button forwarded or tapped by someone else | The tapping number must be bound to the approver the token names; refusals are audited (`approval.token.refused`) |
| Approving what you should not | The decision is `runtime.Store.VoteApproval`, the web app's path: role or delegation, makers cannot approve, distinct approvers, levels; a refusal is audited (`approval.decide.refused`) |
| Step-up | Never satisfied by chat text. Under a `whatsapp_pin` policy, by the person's PIN in a form bound to that one decision (Argon2id, five wrong in 15 minutes lock it; never enough for `totp` or `passkey` policies). Otherwise the hand-off link carries a separate 10-minute, single-use token in the URL fragment (kept out of logs and `Referer`); the page needs a signed-in session as the same person in the same organisation, then a passkey assertion or TOTP code for that exact decision |
| A compromised phone | Per-number limits: 20 messages then one every 3 seconds (one warning a minute), three run starts then one every 20 seconds, and every start needs an explicit **yes** after a summary of exactly what will run. Builds: three then one every 20 seconds, within the AI budget, and a build only ever saves a draft after an explicit **yes**; nothing built over WhatsApp can publish or run. Rows added to data tables: three then one every 20 seconds, each after an explicit **yes**, and chat can only add rows, never change or delete them, nor change a table. Unlinking the number in the web app ends it |
| Personal data in messages | Masked as above; free text passed through the PII redactor; inputs typed in chat are sealed (`x-pii` fields) while they wait for confirmation, and so are the values of a data table's personal columns, typed or sent in a form |
| A row added twice | The insert carries an idempotency key taken from the conversation's nonce when the summary is shown: a **yes** repeated after a lost answer replays Xenibase's first attempt instead of writing again, and once the row is added the session is gone, so a later **yes** has nothing to confirm |
| Tenant isolation | Bindings, codes, the window and message ids are a person's, not a tenant's: no tenant reads them; functions do one narrow thing each. Conversation state, tokens, flows, notices, run watches, own numbers, public sessions and template usage are tenant data under forced row-level security, read only with the conversation's current organisation (or the token's) in scope. The PIN is a person's, like the binding |
| Own numbers' credentials | In the organisation's vault, never in tables, responses or logs; connecting needs `secret.manage` and a token the Graph API accepts for that number |
| Retried sends | The outbox holds only references (kind, person, run, step, level, alert): no text, decision token or number; rows are tenant data under forced row-level security |
| Audit | `whatsapp.code_sent`, `whatsapp.bind`, `whatsapp.unbind`, `whatsapp.pin.set`, `whatsapp.pin.remove` (in every organisation the person belongs to, with the number masked), `whatsapp.pin.fail`, `whatsapp.pin.locked`, `approval.decide` with `channel: whatsapp` and `via: button`, `handoff` or `flow_pin`, `run.start` with `channel: whatsapp` (or by `whatsapp_public:<masked number>`), `table.row.add` with `channel: whatsapp` (the table and the row's id, no values), `whatsapp.number.connect`, `whatsapp.number.disconnect`, `whatsapp.public_menu.update`, `whatsapp.outbox.retry`; the approval's history records the channel and the step-up used |

Limits are per `edge` replica (in memory), like the sign-in limiter.

## Not yet

- Embedded signup (W3): the hook is documented above.
- Delivery receipts: templates are counted when Meta accepts them, not from the `pricing` object of status webhooks.
- Run outcomes for public-menu runs (the person gets a reference only), and menus on the shared number.
- Flows for inputs beyond ten flat fields (nested objects, lists), date pickers.
- Languages beyond English and voice notes are built (A4, [Languages](languages.md)) but their texts are unreviewed drafts, off by default; templates stay in the language Meta approved them in.
- Building: changing an existing workflow by chat (`build` creates new drafts only), and naming the draft in the chat (it takes the template's or the model's name).
- Data tables: creating or changing a table from chat (the web app does that), editing or deleting rows (chat only adds), and questions over rows ("who owes me?") rather than `find <table> <text>`. The model layer does not route the table commands in other languages yet; the word lists do.
