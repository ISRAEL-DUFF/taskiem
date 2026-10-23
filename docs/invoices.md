# Invoices

Invoices are what a business sends its customers to ask for money: a number, who it is for, what for, how much, and by when. Taskiem sends each one by SMS and email with a link to its page, and keeps it up to date as money comes in. They are not your Taskiem subscription's invoices, which are under [plans and billing](billing.md).

Invoices follow [decision 0045](decisions/0045-tally-in-taskiem.md): **an invoice is marked paid only by a payment counted towards it.** No one, in the web app, the API or a workflow, can set an invoice to paid by hand. A payment counted by mistake is undone, and both the payment and its undoing stay in the invoice's history.

## Making and sending one

Under **Invoices**, **New invoice** asks for:

- **The customer**: a name, and a phone number or an email (or both) to send it to.
- **What it is for**: an amount with a short description, or a list of items, each with a quantity and a price.
- **When it is due** (optional), and a note for the customer (optional).

It is saved as a **draft** with the business's next number (`INV-0001` upwards). A draft can be changed. **Send** sends it to the customer by SMS and email, with the link to its page, and the invoice becomes **sent**. Sending again sends the same link. A customer who has had many messages in the last hour (five an hour, twenty a day, from any business) is not sent another: the invoice is sent all the same, its page works, and it says which messages went.

A customer who asked the business for updates on WhatsApp or Telegram also gets it there, with buttons to pay ([below](#on-whatsapp-and-telegram)).

Once sent, what the customer was sent cannot change: to correct an invoice, **void** it with the reason and send a new one.

## Statuses

| Status | Means |
| --- | --- |
| Draft | Saved, not sent |
| Sent | Sent, nothing paid yet |
| Part-paid | Some of it paid |
| Paid | Paid in full |
| Overdue | Past its due date and not paid in full |
| Void | Cancelled, with the reason; it takes no new payment |

Taskiem works the status out from the invoice and its payments; an invoice becomes overdue the day after its due date (Lagos time).

## The invoice's page

The link in the SMS and email opens the invoice's page, `https://<form host>/i/<token>` on the [public host](public-host.md#hosted-forms), drawn in the business's [look](storefronts.md#look). It shows:

- the business's name and the invoice's number;
- its status in plain words: **Awaiting payment** (sent), **Part-paid**, **Paid**, **Overdue** or **Void**;
- each line (what, quantity × unit price, line total), the total, what was paid and what is still due;
- the due date ("Due 31 Jan 2099") and the note.

A paid invoice says "Paid in full. Thank you."; a void one says "This invoice was cancelled." (not the reason).

### Paying from the page

While something is due, the page has **Pay this invoice**:

- **Pay by bank transfer** gets a one-time account number from the business's payments connection, for this invoice and the amount due. The page shows the account number, bank and account name, the amount, "Valid until 14:35 (Lagos time)" with the minutes left, and what the payments provider does with a transfer of a different amount or after that time. It updates itself every 30 seconds (no script), so it shows the payment when it arrives. Once the number expires, **Get a new account number** issues another. A transfer into an expired number that the provider still accepts is counted.
- **Pay by card** gets a payment link from the same connection and offers **Continue to card payment**. A connection that cannot make links answers "Card payment is not available for this invoice just now."
- When the invoice takes part payments, an **Amount to pay** box takes any amount from ₦1,000 (or the balance, when less) up to what is due; left empty, the whole balance.

Asking again for the same amount while a number is still valid shows the same number. An invoice gets at most 5 account numbers an hour and 20 in all (the same again for card links); past that the page says to try later or contact the business. The page only queues the request: the public host holds no credentials, and Taskiem's API server asks the payments provider within a second or two, while the page shows "Getting your account number…".

The page shows nothing about the customer: their name, phone and email stay sealed, and the public host cannot read them. A draft's page, an unknown link and a mistyped one all answer the same "This invoice was not found", so the page never tells whether a link exists. It is never cached (`Cache-Control: no-store`) or indexed (`X-Robots-Tag: noindex`).

## On WhatsApp and Telegram

When the customer's phone is one that asked the business for updates on WhatsApp or Telegram ([updates for people outside the business](updates.md)), **Send** also sends the invoice there, as well as by SMS and email: "Invoice INV-0001 from Ada Stores: ₦25,000 to pay", with two buttons.

- **Pay by transfer** answers within seconds with an account number to pay into: the account number, the bank, the account name, the amount and "Valid until 14:35" (Lagos time), with a **New account number** button for when it has expired. It is the same one-time account the invoice's page gives, issued from the business's payments connection, under the same limits (five an hour, twenty in all); while one is still open, it is given again.
- **Open invoice** answers with the invoice's page link.

If no account number can be had (the payments connection cannot open one, or the limit is reached), the answer says so and gives the page link. On an invoice that is paid, the buttons say "Invoice INV-0001 is paid. Thank you!"; on a void one, that it can no longer be paid.

These buttons are the only thing the customer can do in the chat besides STOP and START ([decision 0045](decisions/0045-tally-in-taskiem.md), point 7). Each button is signed for the business, the invoice and the contact it was sent to: it works only from that contact's own WhatsApp number or Telegram account (while they have not sent STOP), only for an invoice sent to that contact's phone, and only while the invoice is open. Anyone else pressing it is told the button no longer works. The messages hold no customer details beyond what the invoice was sent with, and nothing about the customer is logged.

**Limit.** WhatsApp lets a business message someone only within 24 hours of their last message unless it uses a template Meta has approved, and the template for an invoice with buttons is not approved yet. Outside that window the invoice goes by SMS and email only; on Telegram there is no window.

## From chat

A member with `invoice.manage` who linked WhatsApp or Telegram can make and send an invoice there: `invoice Ada 25,000 for 5 bags of rice` (or `bill Chi 10k`) asks for the customer's phone number or email, shows the invoice and sends it after **yes** ([WhatsApp commands](whatsapp.md#for-people-using-taskiem-on-whatsapp)). In Pidgin, Yoruba, Hausa or Igbo, where that language is on, its own words work too, such as `ìwé owó Ada 25,000 fún ìrẹsì` or `takardar kuɗi Musa 25,000 don shinkafa`; they are unreviewed drafts ([the words](whatsapp.md#invoices-and-money-in-other-languages)). The English words work in every language.

## Payments

Each payment counted towards an invoice is listed on it: the amount, the payment's reference at the payments provider, how it was matched to the invoice ("Paid into the account Taskiem gave"; "Suggested, confirmed by" the person who confirmed it; "Matched automatically"; or "Matched by hand"), the reasons a suggested or automatic match was made, and who matched it. **Undo** takes a payment out of an invoice when it was matched to the wrong one.

Payments into an account number or payment link Taskiem gave for an invoice are counted automatically: every 30 seconds Taskiem asks the payments provider about each one still waiting (for up to 48 hours after an account number expires, and 7 days for a link), and counts a payment once, as matched exactly, by the system. When that makes the invoice paid, a run waiting on `invoice.paid` continues. Other money that comes in (a transfer to the business's usual account, a POS sale, cash) is matched as below.

The account numbers and links come from the prod connection named in **Payments connection** (`main` by default; write `paystack/main` when several providers have a connection called `main`). It needs a provider that can open one-time account numbers (Paystack with Pay with Transfer turned on, Flutterwave, OPay) or make payment links. For now the business pays the provider's fee: the "customer pays the fee" setting is kept but not yet applied, since the providers' fee rules are not yet confirmed.

## Matching

Money that comes in without the reference Taskiem gave gets up to three suggestions among the business's open invoices, each with a score and its reasons ("same amount", "payer ADA OBI matches the customer"). How suggestions are made and scored is in [Matching money to invoices](matching.md).

**Invoices > To match** lists the payments waiting, newest first, with the number waiting shown on the tab. Each payment is a card: the amount, when it was received, where it came in, its reference and what kind of money it was, then the invoices it may be for, strongest first. A score reads **Strong** (85 or more), **Likely** (60 or more) or **Possible**. For each one:

- **Match** counts the payment towards that invoice; the payment's other suggestions close.
- **Not this one** turns the suggestion down; it is not offered again.
- **Match to another invoice…** picks any other invoice that is sent and not yet fully paid, when none of the suggestions is right.

A match from here can be undone on the invoice like any payment. People with `invoice.read` see the list; matching and turning down need `invoice.manage`. When the server refuses (the payment was matched meanwhile, or the invoice is paid or void), its reason is shown on the card.

**Match strong suggestions automatically**, under Settings, is off by default. When on, a payment whose best suggestion is strong, and clearly ahead of the next one, is matched without asking; the invoice shows it as "Matched automatically", with its reasons, and it can be undone.

## Settings

Under **Invoices > Settings**:

| Setting | Default | Notes |
| --- | --- | --- |
| Number prefix | `INV-` | Up to 12 letters, digits, `-`, `_` or `/` |
| Next number | 1 | Can be moved forward, never back, so no number is given twice |
| Payments connection | `main` | The connection (Paystack, Flutterwave, OPay, …) that takes payments for invoices |
| Who pays the transfer fee | The business | Or the customer, where the payments provider takes only the exact amount ([0045](decisions/0045-tally-in-taskiem.md)) |
| Customers may pay part | Off | When on, a customer can pay part of an invoice |
| Match strong suggestions automatically | Off | See [Matching](#matching) |

Each invoice can override the connection, the fee and part payments.

## Who can do what

| Permission | Lets a person | Roles |
| --- | --- | --- |
| `invoice.read` | See invoices and the payments waiting to be matched | Owner, admin, operator, auditor, viewer |
| `invoice.manage` | Create, change, send and void invoices, match money to them, undo a payment, change the settings | Owner, admin, operator |

Customers' names, phone numbers and emails are masked in lists. People with `pii.reveal` can show them on an invoice, which is recorded in the audit log.

## In a workflow

The **Invoices** platform connector (`invoices@1`, no connection needed) acts on the business's own invoices only:

| Action | Does |
| --- | --- |
| `create` | A draft, from `customer` and `amount_kobo` (with `description`) or `lines`, optionally `due_on`, `note` and the payment settings. A retry of the step finds the invoice it made rather than taking another number |
| `send` | Sends it, as above |
| `get` | Its status, total, what was paid and what is still due |
| `void` | Voids it, with a `reason` |
| `notify_customer` | An SMS, and an email when there is one, to the invoice's customer: `{link}` becomes the invoice's page link, `{number}` its number and `{due}` what is still due |
| `list` | `status: overdue` (sent or part paid and past the due date, oldest due first, with `days_overdue`, Lagos time) or `status: paid` (paid within `paid_within_hours`, 1 to 168, default 24); at most `limit` (default 50, up to 200). Numbers, totals, what is due and dates only: no customer details |

There is no action that marks an invoice paid. A run that waits for payment waits for the `invoice.paid` signal, correlated by the invoice's number; the signal resumes a waiting run and starts none.

**Templates.** `send-invoice` makes and sends an invoice and waits for `invoice.paid`. `chase-overdue-invoices` runs daily: `list` the overdue invoices and `notify_customer` each one, at most once per reminder (reminder *n* covers the days `(n−1)×every` to `n×every−1` after the first overdue day, and its `effect.idempotency_seed` is the invoice and *n*, so a second run that day, or a run after a missed day, sends no duplicate), then a note to the owner once. `invoice-paid-receipt` runs hourly over the invoices paid in the last three hours, thanking each customer once (seeded by the invoice): since `invoice.paid` starts no run, polling with a stable key is how a receipt follows payment without a new trigger type; a receipt can come up to an hour after payment, and if more than two hourly runs in a row are missed, invoices paid early in that gap get no receipt.

## API

`GET` and `POST /v1/invoices`, `GET` and `PUT /v1/invoices/{id}` (a draft), `POST /v1/invoices/{id}/send`, `POST /v1/invoices/{id}/void`, `POST /v1/invoices/{id}/payments/{payment}/undo`, `GET` and `PUT /v1/invoice-settings`, and for matching `GET /v1/money/suggestions`, `POST /v1/money/suggestions/{id}/confirm` and `/reject`, and `POST /v1/money/entries/{id}/match` (with `{"invoice": "<id or number>"}`); `{id}` is the invoice's id or number. Creating and sending take an `Idempotency-Key` header. The full description is in the API reference (`api/openapi/openapi.yaml`, tag Invoices).
