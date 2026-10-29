# Leaks

A leak is money a business may be losing: money that came in and belongs to nothing, money owed and not paid, money counted twice, money taken in fees beyond what was agreed. Taskiem looks for leaks in the [money log](money-log.md) and the business's invoices every 15 minutes, following [decision 0045](decisions/0045-tally-in-taskiem.md) and the [build plan](spec/invoices-and-money-log.md) (§9.1, T6).

## What Taskiem looks for

| Leak | When | Amount |
| --- | --- | --- |
| **Money in with nothing to match** | A payment, a credit to an account or a statement line that has been in the log for a day and counts towards no invoice | The payment |
| **Invoice overdue** | An invoice past its due date and not paid in full | What is still due |
| **Duplicate** | The same reference and amount on one connection twice, as two kinds of event (one movement the payment service reported twice). A payment into a customer's own account number is known by the payment service's id for that payment, not the account's reference, so two equal fee payments into one student's account are two payments | The extra copies |
| **Overpaid** | An invoice paid more than it asked for | The excess |
| **Paid after it was voided** | Money paid into an account number Taskiem gave for an invoice that was then voided | The payment |
| **Fee above the agreed rate** | A payment whose fee is above the rate the owner set for its connection | The difference |
| **Cash a rider owes** | Cash a rider took at the door and has not handed in, above the business's limit ([dispatch](dispatch.md#paying-at-the-door)) | What they owe |
| **Sold more than the stock** | A sale of more of an item than the stock held ([stock](stock.md#counts-shrinkage-and-leaks)) | What was sold beyond it, at cost |
| **Stock below zero** | An item's count went below zero in the catalogue table (a hand edit) | The count below zero, at cost |
| **Missing at a count** | A count found fewer than recorded, worth more than the business's limit at cost (₦5,000 unless set; always, for an item with no cost price) | What was missing, at cost |

Stock leaks are valued at the item's cost price when it moved (else its last known one; ₦0, in units only, when it never had one), and stay open until closed: they are about something that happened, not a balance that can come right.

POS sales and cash are takings, not money owed, so they are never "money in with nothing to match". Statement lines are not checked for duplicates: the same line uploaded twice is kept once, and lines without a reference (bank charges) repeat by nature.

Each leak is opened once for what it is about (the payment or the invoice) and shows its amount in naira and a sentence saying what Taskiem saw, such as "₦7,777 came in on paystack/main on 23 Oct 2026 and matches no invoice". While a leak is open, its amount follows what changes (a part payment of an overdue invoice lowers it).

## Closing a leak

People with `invoice.manage` can give a leak to a member, and close it as:

- **Recovered**, with the amount got back: it counts towards the total recovered;
- **Written off**: the money is not coming back;
- **Not a leak**: Taskiem was wrong, for instance a payment that was never for an invoice.

A note can say why. A closed leak cannot be changed.

A leak whose cause goes away before anyone closes it closes itself as **resolved**: the money is matched to an invoice, the overdue or overpaid invoice is put right. Money paid after an invoice was voided stays a leak until a person closes it (refunded and recovered, or not a leak).

## Agreed fees

Under the Money pages, the owner sets the fee agreed with each connection: a percent of the amount, plus a flat fee, up to a cap. A payment whose fee is above that rate is a leak for the difference. Payment services report fees only on some events, so a missing fee is not a leak.

## API

`GET /v1/money/leaks?status=open|closed|all` (with the open total and the amount recovered), `POST /v1/money/leaks/{id}/assign` with `{"member": "<user id>"}` (or `null`), `POST /v1/money/leaks/{id}/close` with `{"status": "recovered", "recovered_kobo": 100000, "note": "…"}`, and `GET` and `PUT /v1/money/fee-rates`. People with `invoice.read` can see leaks and rates.

## Not yet

- Short or late settlement, and a customer debited while the business was never credited: these need each payment service's settlement report, rail by rail.
- Leaks are not sent in chat; the daily summary (T7) will include the ones opened.
