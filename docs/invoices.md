# Invoices

Invoices are what a business sends its customers to ask for money: a number, who it is for, what for, how much, and by when. Taskiem sends each one by SMS and email with a link to its page, and keeps it up to date as money comes in. They are not your Taskiem subscription's invoices, which are under [plans and billing](billing.md).

Invoices follow [decision 0045](decisions/0045-tally-in-taskiem.md): **an invoice is marked paid only by a payment counted towards it.** No one, in the web app, the API or a workflow, can set an invoice to paid by hand. A payment counted by mistake is undone, and both the payment and its undoing stay in the invoice's history.

## Making and sending one

Under **Invoices**, **New invoice** asks for:

- **The customer**: a name, and a phone number or an email (or both) to send it to. Over the API, `customer.key` is the business's own key for them (`student:42`, up to 100 letters, digits and `:._/-`): invoices with the same key are one customer's in [matching](matching.md#customers-own-account-numbers), else invoices to the same phone (else email) are.
- **What it is for**: an amount with a short description, or a list of items, each with a quantity and a price.
- **When it is due** (optional), and a note for the customer (optional).

It is saved as a **draft** with the business's next number (`INV-0001` upwards). A draft can be changed. **Send** sends it to the customer by SMS and email, with the link to its page, and the invoice becomes **sent**. Sending again sends the same link. A customer who has had many messages in the last hour (five an hour, twenty a day, from any business) is not sent another: the invoice is sent all the same, its page works, and it says which messages went.

A customer who asked the business for updates on WhatsApp or Telegram also gets it there, with buttons to pay ([below](#on-whatsapp-and-telegram)).

Once sent, what the customer was sent is never edited: it is **amended** ([below](#amending-a-sent-invoice)), or **voided** with the reason and a new one sent.

Over the API a line can also say who it is collected for (`pass_through_for`, such as `WAEC`): money the business collects on someone else's behalf, which its page shows as "collected on behalf of WAEC" and the [money reports](money-reports.md#pass-through) keep out of the business's own income. A line of `kind: "discount"` (quantity 1, a negative price, never pass-through) takes money off the business's own items, and says why in `reason`.

## Statuses

| Status | Means |
| --- | --- |
| Draft | Saved, not sent |
| Recorded | A notebook debt recorded in chat (`Ada owes 5,000`): owed and open, but not sent ([the debtor book](#the-debtor-book)) |
| Sent | Sent, nothing paid yet |
| Part-paid | Some of it paid |
| Paid | Paid in full |
| Overdue | Past its due date and not paid in full |
| Void | Cancelled, with the reason; it takes no new payment |

Taskiem works the status out from the invoice and its payments; an invoice becomes overdue the day after its due date (Lagos time). The list's **Show** filter has each status and **Notebook debts** (`GET /v1/invoices?kind=credit`); a notebook debt's own page says it was recorded in chat, and **Send** sends it like a draft.

## The debtor book

Money customers owe is kept as invoices, whether it was invoiced or only written down ([decision 0060](decisions/0060-no-sheet-needed.md)). A notebook debt is an invoice of kind **credit** in the status **recorded**: it is open and owed everywhere an invoice is (matching, a customer's payments counted oldest first, credit, the list of overdue invoices and the customer's statement), it takes part payments whatever **Customers may pay part** says, and nothing is sent to the customer unless the business sends it. Payments move it to part-paid and paid as they move a sent invoice, and it is overdue the day after its due date.

- **Customers without a phone.** A customer of the book is a record of their own, keyed `book:<id>` on every line of theirs, so their payments and credit group by it. Their name is kept sealed, with a lower-case copy for finding them by name, which is erased with them; two customers of the same name are two records. A phone given later is set on the record and on their lines not yet sent.
- **When a line is due.** The owner chooses how long customers have to pay (**Credit terms**: a number of days, or no fixed time). Until they choose, and with no fixed time, a line has no due date and is never overdue. A line's due date is set when it is recorded; a change of terms moves the open lines only when the owner asks for that.
- **Receipts.** `notify_customer` on a line with no phone or email sends nothing and answers `sent: false` (`reason: no_address`), which is not a failure.
- **Credit.** Money a customer paid beyond what they owed is their credit, and it pays their next line as soon as the line is recorded, as it pays an invoice when it is sent.
- **Their balance in one message.** `send Ada her balance` in chat sends the customer one SMS (and email, when they have one, and on WhatsApp or Telegram when they asked the business for updates there, as the `taskiem_balance` [template](whatsapp.md#templates) outside WhatsApp's 24-hour window once Meta approves it) naming their open invoices and debts with what is due on each, the total, and one way to pay it: their own account number when they have one, else their payment link (below). Their recorded lines are sent with it, together; nothing is folded into another invoice or voided, so each keeps its history. It needs a phone or an email; without one, chat asks for it first.

### A customer's payment link

A customer's payment link, `https://<form host>/c/<token>`, opens a page of what they owe the business across their open invoices and debts: each with its number, what it is for, its due date and what is still due on it, in the order their money pays them, and the total. It is made the first time it is sent and kept with the customer, so the same link works for every later balance; it shows nothing about the customer, and a customer who owes nothing, an unknown link and a mistyped one all answer "There is nothing to pay on this page".

**Pay what you owe** works as an invoice's **Pay this invoice** does ([paying from the page](#paying-from-the-page)): a one-time account number or a card link from the business's payments connection (the one of the customer's oldest open line), for the total or any part of it from ₦1,000, with the same limits per link. Money paid through it is the customer's: [matching](matching.md#customers-payment-links) counts it towards their open invoices and debts oldest first, with no one asked, and keeps what is left as their credit.

## Amending a sent invoice

A sent invoice (sent, part-paid, overdue or paid) changes only by an **amendment**: a recorded change with a reason, which becomes a line of its own at the end of the invoice, on its page too. On the invoice's page, **Amend…** offers three:

| Amendment | Line it adds |
| --- | --- |
| **Add a line** (what it is for, an amount, optionally who it is collected for) | The line, marked "Added after the invoice was sent" |
| **Give a discount** (an amount, optionally a description) | A negative line, "Discount" or what you called it. A discount comes off the business's own items, never off money collected for someone else |
| **Take a line out** (one of the invoice's items) | A negative line, "Taken out: …", for that item's amount. A line is taken out once |

The total is then the sum of the lines, and the status follows from it and the payments counted, as always: nothing marks an invoice paid but its payments.

- The total can never go below what has been paid: such an amendment is refused (409). Refund the customer instead.
- A paid invoice whose total rises is **part-paid** again, for what the amendment added; a discount that brings the total down to what was paid makes it **paid**, and starts the workflows that start on an invoice being paid, once, as a payment would.
- The total stays at least ₦1: to cancel what is left, void the invoice.
- A draft is not amended: change it instead.
- Every amendment is in the audit log (`invoice.amended`, with who, the amount and the reason) and in the [invoices export](money-reports.md#invoices) (`amended`).
- Where the [Schools](schools.md) area is on, a discount or a line taken out at or above the school's discount threshold is recorded as a request for a second person instead of made (202, `code: needs_approval`), and made when they approve it ([discounts](schools.md#discounts)). What is weighed is what the amendment takes off the invoice together with what earlier amendments took off it without a second person and, on a student's invoice, with what the student was given without one within the current term (and at least the last 24 hours): their standing discounts and the reductions of their other invoices ([the second person](schools.md#the-second-person)).

The database keeps the rule whoever writes: a sent invoice's lines can only grow, each new line naming a recorded amendment of that invoice (once), its total is recomputed from its lines, and never below what was paid.

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

**Outside the 24-hour window.** WhatsApp lets a business message someone only within 24 hours of their last message unless it uses a template Meta has approved. Outside that window the invoice goes as the `taskiem_invoice` [template](whatsapp.md#templates) once Meta has approved it in the number's WhatsApp Business Account: the same words, **Pay by transfer** as a quick reply carrying the same signed button (an account number follows in the chat as above), and **Open invoice** opening the invoice's page. Until it is approved the invoice goes by SMS and email only, and the chat message says so ([the template registry](whatsapp.md#the-template-registry)); on Telegram there is no window.

## From chat

A member with `invoice.manage` who linked WhatsApp or Telegram can make and send an invoice there: `invoice Ada 25,000 for 5 bags of rice` (or `bill Chi 10k`) asks for the customer's phone number or email, shows the invoice and sends it after **yes** ([WhatsApp commands](whatsapp.md#for-people-using-taskiem-on-whatsapp)). In Pidgin, Yoruba, Hausa or Igbo, where that language is on, its own words work too, such as `ìwé owó Ada 25,000 fún ìrẹsì` or `takardar kuɗi Musa 25,000 don shinkafa`; they are unreviewed drafts ([the words](whatsapp.md#invoices-and-money-in-other-languages)). The English words work in every language.

## Payments

Each payment counted towards an invoice is listed on it: the amount, the payment's reference at the payments provider, how it was matched to the invoice ("Paid into the account Taskiem gave"; "Suggested, confirmed by" the person who confirmed it; "Matched automatically"; or "Matched by hand"), the reasons a suggested or automatic match was made, and who matched it. **Undo** takes a payment out of an invoice when it was matched to the wrong one.

Payments into an account number or payment link Taskiem gave for an invoice are counted automatically: every 30 seconds Taskiem asks the payments provider about each one still waiting (for up to 48 hours after an account number expires, and 7 days for a link), and counts a payment once, as matched exactly, by the system. When that makes the invoice paid, a run waiting on `invoice.paid` continues, and the workflows that [start when an invoice is paid](#starting-a-workflow-when-an-invoice-is-paid) start. Other money that comes in (a transfer to the business's usual account, a POS sale, cash) is matched as below.

The account numbers and links come from the prod connection named in **Payments connection** (`main` by default; write `paystack/main` when several providers have a connection called `main`). It needs a provider that can open one-time account numbers (Paystack with Pay with Transfer turned on, Flutterwave, OPay) or make payment links. For now the business pays the provider's fee: the "customer pays the fee" setting is kept but not yet applied, since the providers' fee rules are not yet confirmed.

## Matching

Money that comes in without the reference Taskiem gave gets up to three suggestions among the business's open invoices, each with a score and its reasons ("same amount", "payer ADA OBI matches the customer"). How suggestions are made and scored is in [Matching money to invoices](matching.md).

**Invoices > To match** lists the payments waiting, newest first, with the number waiting shown on the tab. Each payment is a card: the amount, when it was received, where it came in, its reference and what kind of money it was, then the invoices it may be for, strongest first. A score reads **Strong** (85 or more), **Likely** (60 or more) or **Possible**. For each one:

- **Match** counts the payment towards that invoice; the payment's other suggestions close.
- **Not this one** turns the suggestion down; it is not offered again.
- **Match to another invoice…** picks any other invoice that is sent and not yet fully paid, when none of the suggestions is right.

A match from here can be undone on the invoice like any payment. People with `invoice.read` see the list; matching and turning down need `invoice.manage`. When the server refuses (the payment was matched meanwhile, or the invoice is paid or void), its reason is shown on the card.

A payment that is more than is due on the invoice suggested can be **split** across the same customer's open invoices, oldest first, with the amounts editable; each part is a match of its own, and what is left over is the customer's credit or waits to be matched ([split payments](matching.md#split-payments)). A credit held for a person (paid into a customer's account number with a reference that disagrees) says why on its card.

Each sent invoice shows **The customer's money**: **What they owe**, the customer's statement (every open invoice and notebook debt of theirs, oldest first, as their money pays them, with what is due on each and the total; `open` and `owed_kobo` in `GET /v1/invoices/{id}/customer`), the customer's credit (paid and not yet counted towards an invoice), their own account numbers (each with its reference), and the payers remembered for them, each with **Forget this payer** for people with `invoice.manage` ([payer memory](matching.md#payer-memory)). A customer's credit counts towards the next invoice sent to them: Send says how much of it did, and the message to the customer says what is left to pay.

**Give this customer an account number** (`invoice.manage`) gives the invoice's customer a permanent account number of their own, so every transfer into it is matched to their open invoices, oldest first, and the rest kept as their credit ([customers' own account numbers](matching.md#customers-own-account-numbers)). Choose one of the business's connections that can issue account numbers (the form says what each needs: the customer's email, their BVN or NIN typed in and never kept, or the wallet the number pays into), or **Enter one by hand**: the connection, the account number and the reference a payments provider already gave the business for this customer. The same customer and connection again shows the account already issued. One per customer, never one per invoice: an invoice's page offers its own.

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
| Credit terms | Not set | How long customers have to pay a notebook debt, in days, or no fixed time; until set, notebook debts have no due date and are never reminded ([the debtor book](#the-debtor-book)). Its own card, **Save credit terms** (`PUT /v1/invoice-settings/credit-terms`), with **Also date the open debts by these terms** to date the open ones from the day each was recorded |

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
| `notify_customer` | An SMS, and an email when there is one, to the invoice's customer: `{link}` becomes the invoice's page link, `{number}` its number, `{due}` what is still due, `{customer_due}` and `{customer_lines}` what the customer owes on everything overdue and on how many, `{customer_link}` the customer's [payment link](#a-customers-payment-link), and `{fee_line}` a sentence on the bank's fee ("You sent ₦25,000; ₦24,650 reached Ada Cakes after the bank's fee of ₦350.", in the business's language for its customers) when the rail said what it kept, or nothing |
| `list` | `status: overdue` (sent or part paid and past the due date, oldest due first, with `days_overdue`, Lagos time) or `status: paid` (paid within `paid_within_hours`, 1 to 168, default 24); at most `limit` (default 50, up to 200). Numbers, totals, what is due and dates only: no customer details |
| `register_account` | A permanent account number of the customer's own ([matching](matching.md#customers-own-account-numbers)): `customer` (as their invoices have them: `key` when given, else `phone`, else `email`) and `connection` (`paystack/main`), then either `issue: true` to issue one on a connection that can (with `account` for the wallet on a rail that holds several, `bvn` or `nin` where the rail needs one, `account_name` where it lets the business name it) or `account_number` and `reference` (and `bank_name`) for one the business already has. The same customer and connection again answers the account already issued, so a retry of the step is safe. Answers the number, bank, reference and connection. Never one per invoice |
| `customer_link` | The [payment link](#a-customers-payment-link) of the customer of `number` (any of their open invoices or debts), made the first time and the same after, with what they owe in all (`due_kobo`, `due`). Fails when they owe nothing |

There is no action that marks an invoice paid. A run that waits for payment waits for the `invoice.paid` signal, correlated by the invoice's number; the signal resumes a waiting run and starts none. A workflow that should start when an invoice is paid has the invoice trigger instead ([below](#starting-a-workflow-when-an-invoice-is-paid)).

**Templates.** `send-invoice` makes and sends an invoice and waits for `invoice.paid`. `chase-overdue-invoices` runs daily: `list` the overdue invoices and `notify_customer` each one, at most once per reminder (reminder *n* covers the days `(n−1)×every` to `n×every−1` after the first overdue day, and its `effect.idempotency_seed` is the invoice and *n*, so a second run that day, or a run after a missed day, sends no duplicate), then a note to the owner once. `invoice-paid-receipt` starts on the invoice trigger: the moment an invoice is paid in full it thanks the customer with `notify_customer` (with `{fee_line}`, so a transfer's receipt says what was sent and what arrived) and tells the owner, each seeded by the invoice, so the receipt goes at once and none is missed. A workflow made from its earlier hourly version (which looked at the invoices paid in the last three hours) keeps running as it was; making it again from the template gives the immediate one, with the same **Tell me too** setting.

## Starting a workflow when an invoice is paid

A workflow whose trigger is

```json
{"type": "invoice", "config": {"event": "paid"}}
```

starts when one of the business's invoices is paid in full, by whatever counted the last payment: a payment into the account number or link Taskiem gave, a suggestion confirmed, a match made by hand or an automatic match. In the editor's read-back it says "When one of your invoices is paid in full". It is how a receipt, a thank-you or a note to the owner goes the moment the money is counted.

- **Once per invoice.** Each workflow starts once for each invoice, whatever happens after: the same payment reported again, another payment on the paid invoice, a restart, or the invoice paid again after a wrong match was undone start no second run. A payment undone before its run could start (within a second or so) starts none; when the invoice is paid again, it starts then.
- **In prod only.** Payments are counted in prod, so only the version deployed to prod starts. Deployed to staging only, or never deployed, a workflow starts nothing; any number of workflows can start on the same invoice.
- **Durable.** The runs to start are written in the same transaction as the payment that makes the invoice paid, so a payment never commits without them and a crash loses none; Taskiem's API server starts them within a second. A start the plan's limits or a suspended organisation refuse is not tried again; the reason is kept.

The run's `trigger.body` is the invoice:

| Field | Is |
| --- | --- |
| `id`, `number` | The invoice's id and number (`INV-0001`) |
| `amount_kobo`, `amount` | Its total, in kobo and in words (`₦25,000`) |
| `paid_kobo` | What was paid towards it (at least the total) |
| `currency` | `NGN` |
| `customer` | `{name, phone, email}`, as the invoice has them (phone and email only when it has them) |
| `paid_at` | When it became paid (RFC 3339) |
| `link` | The invoice's page link, which now shows it paid |
| `fee_kobo`, `fee`, `received_kobo`, `received` | The bank's fee on the transfers that paid it and what reached the business, in kobo and in words; only when every payment standing was a whole transfer whose fee the rail reported (iswallet's do), so never for cash or a transfer split over invoices |
| `status`, `event` | `paid` |

The customer's name, phone and email, and the link, are personal data: they are sealed in the run's history like a form's marked fields and shown masked. To message the customer, use `notify_customer` with `"number": "=trigger.body.number"`, which finds their phone and email itself.

### Every payment, not only paid in full

A workflow whose trigger is `{"type": "invoice", "config": {"event": "payment"}}` starts for **every payment counted** towards one of the business's invoices, paid in full or not: each part of a split, each cash posting, each credit to a student's account number. It is how a school sends a receipt the moment any money comes in ([receipts](schools.md#receipts)). In the editor's read-back it says "When a payment is counted towards one of your invoices".

- **Once per payment part.** Each workflow starts once for each part counted, however often the payment is reported again. A part undone before its run could start starts none; a payment counted again after an undo is a new part and starts again. It is otherwise kept as the `paid` event is: prod only, written in the payment's transaction, started within a second.
- `trigger.body` is the invoice as above (`status` and `paid_kobo` as they are after this payment; `event` is `payment`), with `due_kobo`, what is still due; `payment`: `{id, amount_kobo, amount, how, paid_at}`, the part counted and how it was matched (`exact`, `suggested`, `suggested_auto` or `manual`), with `fee_kobo`, `fee`, `received_kobo` and `received` when the part was a whole transfer whose fee the rail reported (the invoice-wide figures of `paid` are not in this body); and, when the invoice is a school's term invoice, `receipt`: `{id, number, link}`, the receipt issued for this payment and its public verification page, and `family_link`, a fresh link to [the family's page](schools.md#the-family-page). The receipt and family links are sealed like the invoice link.
- A `notify@1` `message` step sends the family the receipt with `"message": "='Receipt ' + trigger.body.receipt.number + ' for ' + trigger.body.payment.amount + ': ' + trigger.body.receipt.link"` (to the number in `trigger.body.customer.phone`), or `invoices@1` `notify_customer` reaches the invoice's customer by its number.

## API

`GET` and `POST /v1/invoices`, `GET` and `PUT /v1/invoices/{id}` (a draft), `POST /v1/invoices/{id}/send`, `POST /v1/invoices/{id}/void`, `POST /v1/invoices/{id}/amendments` (`{kind: "add" | "remove" | "discount", description, amount_kobo, pass_through_for, line, reason}`), `POST /v1/invoices/{id}/payments/{payment}/undo`, `GET` and `PUT /v1/invoice-settings`, and for matching `GET /v1/money/suggestions`, `POST /v1/money/suggestions/{id}/confirm` and `/reject`, and `POST /v1/money/entries/{id}/match` (with `{"invoice": "<id or number>"}`, or `{"parts": [...]}` to split), `GET /v1/invoices/{id}/customer` (the customer's credit, accounts and remembered payers), `POST /v1/customer-accounts` (a customer's permanent account, by their details), `POST /v1/invoices/{id}/customer-accounts` (the invoice's customer's, issued with `issue: true` or given), `GET /v1/customer-accounts/issuers` (the connections that can issue one) and `DELETE /v1/money/payers/{id}`; `{id}` is the invoice's id or number. Creating and sending take an `Idempotency-Key` header. The full description is in the API reference (`api/openapi/openapi.yaml`, tag Invoices).
