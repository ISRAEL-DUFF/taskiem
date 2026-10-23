# Invoices

Invoices are what a business sends its customers to ask for money: a number, who it is for, what for, how much, and by when. Taskiem sends each one by SMS and email with a link to its page, and keeps it up to date as money comes in. They are not your Taskiem subscription's invoices, which are under [plans and billing](billing.md).

Invoices follow [decision 0045](decisions/0045-tally-in-taskiem.md): **an invoice is marked paid only by a payment counted towards it.** No one, in the web app, the API or a workflow, can set an invoice to paid by hand. A payment counted by mistake is undone, and both the payment and its undoing stay in the invoice's history.

## Making and sending one

Under **Invoices**, **New invoice** asks for:

- **The customer**: a name, and a phone number or an email (or both) to send it to.
- **What it is for**: an amount with a short description, or a list of items, each with a quantity and a price.
- **When it is due** (optional), and a note for the customer (optional).

It is saved as a **draft** with the business's next number (`INV-0001` upwards). A draft can be changed. **Send** sends it to the customer by SMS and email, with the link to its page, and the invoice becomes **sent**. Sending again sends the same link.

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

A paid invoice says "Paid in full. Thank you."; a void one says "This invoice was cancelled." (not the reason). Payment options will be shown there with T4; until then the page says they will appear soon.

The page shows nothing about the customer: their name, phone and email stay sealed, and the public host cannot read them. A draft's page, an unknown link and a mistyped one all answer the same "This invoice was not found", so the page never tells whether a link exists. It is never cached (`Cache-Control: no-store`) or indexed (`X-Robots-Tag: noindex`).

## On WhatsApp and Telegram

When the customer's phone is one that asked the business for updates on WhatsApp or Telegram ([updates for people outside the business](updates.md)), **Send** also sends the invoice there, as well as by SMS and email: "Invoice INV-0001 from Ada Stores: ₦25,000 to pay", with two buttons.

- **Pay by transfer** answers within seconds with an account number to pay into: the account number, the bank, the account name, the amount and "Valid until 14:35" (Lagos time), with a **New account number** button for when it has expired. It is the same one-time account the invoice's page gives, issued from the business's payments connection, under the same limits (five an hour, twenty in all); while one is still open, it is given again.
- **Open invoice** answers with the invoice's page link.

If no account number can be had (the payments connection cannot open one, or the limit is reached), the answer says so and gives the page link. On an invoice that is paid, the buttons say "Invoice INV-0001 is paid. Thank you!"; on a void one, that it can no longer be paid.

These buttons are the only thing the customer can do in the chat besides STOP and START ([decision 0045](decisions/0045-tally-in-taskiem.md), point 7). Each button is signed for the business, the invoice and the contact it was sent to: it works only from that contact's own WhatsApp number or Telegram account (while they have not sent STOP), only for an invoice sent to that contact's phone, and only while the invoice is open. Anyone else pressing it is told the button no longer works. The messages hold no customer details beyond what the invoice was sent with, and nothing about the customer is logged.

**Limit.** WhatsApp lets a business message someone only within 24 hours of their last message unless it uses a template Meta has approved, and the template for an invoice with buttons is not approved yet. Outside that window the invoice goes by SMS and email only; on Telegram there is no window.

## Payments

Each payment counted towards an invoice is listed on it: the amount, the payment's reference at the payments provider, how it was matched to the invoice (exactly, by the reference Taskiem gave it; suggested; or by a person) and who matched it. **Undo** takes a payment out of an invoice when it was matched to the wrong one.

Payments are counted from your payments connection: by the account number or payment link Taskiem gives the customer for the invoice, and by matching other money that comes in. Both arrive in the milestones after this one ([build plan](spec/invoices-and-money-log.md), T4 and T5); until then an invoice shows no payments.

## Settings

Under **Invoices > Settings**:

| Setting | Default | Notes |
| --- | --- | --- |
| Number prefix | `INV-` | Up to 12 letters, digits, `-`, `_` or `/` |
| Next number | 1 | Can be moved forward, never back, so no number is given twice |
| Payments connection | `main` | The connection (Paystack, Flutterwave, OPay, …) that takes payments for invoices |
| Who pays the transfer fee | The business | Or the customer, where the payments provider takes only the exact amount ([0045](decisions/0045-tally-in-taskiem.md)) |
| Customers may pay part | Off | When on, a customer can pay part of an invoice |

Each invoice can override the connection, the fee and part payments.

## Who can do what

| Permission | Lets a person | Roles |
| --- | --- | --- |
| `invoice.read` | See invoices | Owner, admin, operator, auditor, viewer |
| `invoice.manage` | Create, change, send and void invoices, undo a payment, change the settings | Owner, admin, operator |

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

There is no action that marks an invoice paid. A run that waits for payment waits for the `invoice.paid` signal, correlated by the invoice's number.

## API

`GET` and `POST /v1/invoices`, `GET` and `PUT /v1/invoices/{id}` (a draft), `POST /v1/invoices/{id}/send`, `POST /v1/invoices/{id}/void`, `POST /v1/invoices/{id}/payments/{payment}/undo`, and `GET` and `PUT /v1/invoice-settings`; `{id}` is the invoice's id or number. Creating and sending take an `Idempotency-Key` header. The full description is in the API reference (`api/openapi/openapi.yaml`, tag Invoices).
