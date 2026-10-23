# Invoices and the money log: what is not covered yet

Everything built for [decision 0045](../decisions/0045-tally-in-taskiem.md) so far (T1 to T8 of the [build plan](invoices-and-money-log.md)) that is not done, not verified, or done differently from the plan. Each item says what is missing, why, and what would close it. Update this list as items close; the plan's "Built" notes say how each milestone differs from it.

Status as of 23 October 2026: T1 to T8 are all built. What is left is below.

## Needs people, not code

| Item | Why it matters | What closes it | Who |
| --- | --- | --- | --- |
| **Sandbox runs on each rail** (Paystack, Flutterwave, OPay) | T4's "done when": a customer pays from the page and from WhatsApp on each rail. Everything is tested against fakes only | Sandbox `main` connections on staging; pay an invoice by transfer from the page and from WhatsApp; a transfer after expiry; a sixth request in an hour | Founders (credentials via the Connections page), then engineering |
| **Paystack's Pay with Transfer must be turned on** by Paystack support for each business | Without it, `collect_transfer` on Paystack fails | Ask Paystack support; the connection page could say so (readiness) | Each business; engineering for the readiness note |
| **A WhatsApp template for invoices with buttons**, and one for matching questions | Outside WhatsApp's 24-hour window neither message is sent: invoices go by SMS and email only, and matching questions wait in the web app | Submit utility templates to Meta and send them when the window is closed | Founders (Meta Business account), then engineering |
| **Publishing the guides** `invoices.md`, `money-log.md`, `matching.md`, `leaks.md`, `money-reports.md` | They are internal (`tools/docsite/pages.go`) until the sandbox runs pass | Remove them from the internal list | Engineering, after the sandbox runs |
| **Rail facts still unknown** (OPay and iswallet: late and wrong-amount transfers; every rail's fee for one-time accounts) | Pages say "unknown" where a rail does not document it; the fee setting cannot be applied | Confirm with each rail (questions are in the plan's §4.3) | Founders / partnerships |
| **Reviewing the chat translations** | New chat messages in Pidgin, Yoruba, Hausa and Igbo are unreviewed drafts (`reviewed: false`), and so are T8's command words for invoices, expenses and cash (`ìwé owó`, `mo ná`, `takardar kuɗi`…) in `engine/lang/intents/<language>.json` | A speaker reviews each catalogue entry, and the invoice, expense and cash words and `for_words` in each word list ([words](../languages.md#reviewing-a-language)) | Founders / reviewers |

## Invoices and paying them (T3, T4)

- **The customer cannot be made to pay the fee.** The "customer pays the fee" setting is stored but not applied: no rail documents its dynamic-account fee in a form Taskiem can show as a line. Needs the rail facts above, then a fee line on the page and in the amount asked.
- **"Pay by card" is always shown** on the invoice page, because the public page cannot tell whether the connection makes payment links. A connection that cannot answers "Card payment is not available" after the press. Fix: record each rail's capabilities where the public role can read them.
- **Payments are found by polling `get_payment` every 30 seconds**, not from rails' events (OPay maps no event, and webhooks need setting up per business). Exact matches now also come from events where a rail sends them; OPay one-time accounts still rely on polling.
- **Paystack needs an email**: a customer without one is sent to Paystack as `invoices+<number>@<page host>`.
- **The members' "invoice" chat command was off on the edge server** before T4 gave it invoices; check on staging that it works as expected.
- **Editing a draft without `pii.reveal`** asks for the customer's details again, since the API returns them masked; the payments connection is typed by name, not picked.
- **Chat command words in other languages are drafts** (T8): Pidgin, Yoruba, Hausa and Igbo words for an invoice, an expense, cash in and cash out, and for "for", are in each language's word list, unreviewed; what remains is a review by speakers (above). The vendor is still read after English `at` only, and expense categories from English words only.
- **Number of messages to one customer**: a customer who has had five messages in an hour (twenty a day, from any business) is not sent another; the invoice is still sent and its page works. The web app and chat say no message went, not why.

## The money log and kept tables (T2)

- **The record of reference is in Taskiem's database** (`money_entries`); the `money_log` data table is a copy. A business that brings its own Xenibase project can still edit the copy there, outside Taskiem. This follows from data tables having no unique constraint.
- **Not recorded yet:**
  - payouts a rail reports as settled at once with no event after (`pay_bank` answering `settled`);
  - refunds (the capability set has no refund event).
- **Paystack takes one webhook URL per account** while Taskiem has two Paystack triggers: transfer events reach the log through the 5-minute poll, not instantly. Fix: one Paystack trigger for both kinds of event.
- **Moniepoint POS:** bill payments, airtime, BOOM, pay codes and collections are left out as money paid out of the balance; that is a reading of the docs, not confirmed. Moniepoint gives no fee or settled amount.
- **POS statuses matched by name, not documented:** "reversed" for both POS rails, and OPay's FAIL and CLOSE.
- **A POS delivery an expression cannot read** (an OPay FAIL without an amount, malformed Moniepoint `metaData`) is skipped by the log, not failed.
- **Kept tables cannot be given back:** nothing clears `kept_by`, though the plan says columns stay fixed only "while the feature that keeps it is on". Needs a function that releases a table.
- **`EnsureKept` adopts** an unrecorded table with a kept name in a business's own Xenibase project, and does not check the plan's column limit.
- **Guard cost:** one database transaction per Xenibase write step in a workflow, to check for kept tables.
- **The connection page does not check** that the field a rail verifies webhooks with is filled in (Flutterwave's webhook hash is optional; deliveries are refused until it is set).

## Matching (T5)

- **Storefront orders are not matched through suggestions**; they keep their own payment check. Spec 8.1 also covers orders.
- **The permanent-account rule is not built**: no customer's permanent account (`virtual_account`) is linked to invoices, so a payment into one is suggested, not matched exactly to the customer's oldest open invoice.
- **The score is Taskiem's own** (amount, a reference naming the invoice, the payer's name, days since sending), not Tally's; weights are in [matching](../matching.md). Tune with real data.
- **Most payments carry no payer name** (`payment.received` has none), so suggestions mostly rest on the amount and timing.
- **One payment counts towards one invoice**: a single transfer that pays two invoices cannot be split.
- **Chat:**
  - matching questions have no setting of their own (the existing notification settings cover failures, setup and finished runs only);
  - a crash between saving suggestions and sending the question loses that one question (the suggestion stays in the web app);
  - reasons are in English in every language.
- **Web app:**
  - the "match by hand" picker lists the first page of invoices only;
  - "confirmed by" shows the member's id, not their name;
  - the To match count counts payments with suggestions, not those left for a person.

## Leaks, statements, cash and expenses (T6)

- **Two leaks of §9.1 are later:** short or late settlement, and a customer debited while the business was never credited. Both need each rail's settlement report as a capability, rail by rail.
- **Statement formats:** the common format, and any bank's columns matched to it once; no bank's own export is parsed without matching yet (Tally's parsers).
- **Statements give no payer name**, so matching statement lines rests on amount and timing.
- **Duplicates are narrow:** one movement reported as two kinds of event on one connection. A payment seen both as an event and on an uploaded statement (two channels) is not flagged; deciding that two channels are the same account needs the owner to link them.
- **Leaks are not sent in chat** one by one; the daily summary counts those opened.
- **Leaks use the invoice permissions** (`invoice.read`, `invoice.manage`); a money permission of its own may be wanted.
- **Agreed fees are set by hand** per connection; no rail's published rates are filled in.
- **Templates:**
  - `invoice-paid-receipt` sends within the hour, not at once, since no trigger starts a run when an invoice is paid; if more than two hourly runs in a row are missed, invoices paid early in the gap get no receipt. A trigger on `invoice.paid` would fix both.
  - `chase-overdue-invoices` skips straight to the owner's note for an invoice already past all its reminder windows when the workflow first runs.
  - Adding them moved the offline builder eval's ungated `connector_recall` from 0.549 to 0.537 (five cases scoring just under the template threshold now start from a template); the gated measures did not regress.
- **Expenses in chat** use a short fixed word list for categories, and record the member's bare user id, as the API does, where the invoice chat records `user:<id>`.
- **Web app (Money pages):**
  - no list of accounts statements were uploaded for, so the account field suggests only those seen in fee rates and unmatched money;
  - assigning a leak to someone else needs `member.manage` (the members list); others can assign to themselves or no one, and leaks show member ids, not names;
  - fee rates can be changed but not removed (no delete);
  - a leak's link to its invoice says "See the invoice", not its number.

## Summaries and exports (T7)

- **The monthly email has no attachments**: it carries the summary and a link to the Money page's downloads, which needs a sign-in.
- **Owners only, and no opt-out**: members with `invoice.read` who are not owners get no summary; a money group in the notification settings needs `engine/notices` and the web Settings page changed together.
- **Missed days are not caught up**: a scheduler down all day skips that day's chat summary; the monthly email is tried until the 3rd. An owner who links a chat later that day gets nothing for it.
- **Outside WhatsApp's 24-hour window** the summary is sent as the `taskiem_alert` template (counted against the business's allowance), cut to 200 characters; a template of its own needs Meta's approval.
- **The monthly email is in English**; the chat text is in five languages, four unreviewed.
- **The journal uses fixed account names** and no VAT; mapping to a business's own chart of accounts (or a bookkeeping tool's import format) is later.
- **The Overview's channel table has no limit** on rows, and shows connection names, not the business's own names for accounts.

## Templates and the AI builder (T8)

- **Measured offline only.** The real-model eval of the new invoice and money log cases (`evals/builder/invoices.jsonl`) needs the AI account (AI1); the offline heuristic answers the money log cases from unrelated templates or by guessing, since no template reads `money_log`.
- **No template reads the money log** (a daily "who paid", money not matched, POS totals from the log): the builder writes those from the instructions; the daily chat summary (T7) covers the first for owners.
- **The invoice-table check is narrow**: only a table the draft proposes, named with `invoice` or `invoices` and with a status or paid column, is warned about, and the warning is not fed back to the model. A payment link or account number made per invoice is caught by the instructions only.
- **Invoice requests naming another system** (a Remita RRR, invoices in MySQL or a billing system) meet the invoice templates too; the model is told to use the named tool, but the offline heuristic starts from the template.
- **The money log's columns are written into the instructions by hand**; `engine/moneylog`'s test fails if a column is renamed without them.
- **The best template's connectors' schemas** are added to every prompt that offers a template, which adds about a tenth to the offline eval's input tokens.
