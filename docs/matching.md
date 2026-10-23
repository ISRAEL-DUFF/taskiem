# Matching payments to invoices and orders

Matching decides which invoice or [storefront order](storefronts.md#payments-and-matching) a payment is for. Taskiem looks at every payment the [money log](money-log.md) records as money in and settled, once, and either matches it, suggests where it may belong, or leaves it for a person. It follows [decision 0045](decisions/0045-tally-in-taskiem.md) and the [build plan](spec/invoices-and-money-log.md) (§8, T5).

A match is a payment counted towards an invoice or an order, listed on it with how it was matched and why. **Nothing else marks an invoice paid**, and an order is marked paid along the same path whether its own payment check or matching hears of the money first.

## Exact matches

A payment into an account number or payment link Taskiem gave for an invoice ([paying an invoice](invoices.md#paying-from-the-page)) is matched to that invoice at once, with no one asked. Taskiem matches it when the payment service tells it about the payment, or when its own check every 30 seconds finds it, whichever comes first.

A payment through a storefront order's Paystack checkout page (its reference is the one Taskiem gave the page) is matched to that order the same way: when the Paystack event reaches the money log, or when the order's own payment check finds it, whichever comes first. Both write the same money log entry, its match, and the order's move to paid together, under the money's lock and the order's, so the order is matched and marked paid once, and its workflow is started or told once. An order cancelled or expired before its money came, or already paid by other money, is left for a person.

## Suggestions

Any other money in gets up to three suggestions among the business's open invoices (sent, part-paid or overdue) and the storefront orders waiting for money (waiting for their checkout payment, or taken with no online payment and not yet paid, cancelled, expired or decided against). Each suggestion has a score from 0 to 100 and its reasons:

| Reason | Adds |
| --- | --- |
| Same amount as what is due (for an order, what is paid online: the deposit, or the total) | 50 |
| Part of what is due, on an invoice that takes part payments (orders take none) | 15 |
| The payment's reference names the invoice or order ("INV-0104", "inv0104", "AK-7QX4KD") | 40 |
| The payer's name matches the customer's (every word of it, in any order) | 35 |
| The payer shares a name with the customer | 20 |
| Paid within 3 days of the invoice being sent, or the order placed | 15 |
| Within 14 days | 10 |
| Within 60 days | 5 |

The score is the sum of the reasons, at most 100. Invoices and orders scoring under 40 are not suggested. A payment made before an invoice was sent, or an order placed, is never suggested for it. Only the business's own invoices and orders are ever suggested.

Reasons say what matched, never anyone's name: "the payer's name matches the customer's".

## Confirming, rejecting and matching by hand

People with `invoice.manage` work through the suggestions:

- **Confirm** a suggestion: the payment counts towards that invoice or order, and the payment's other suggestions close.
- **Reject** a suggestion: it is not offered again.
- **Match by hand** to any open invoice, or any order waiting for money, when no suggestion fits. An order takes one payment, of at least what is due.

People with `invoice.read` can see the suggestions, orders' included; customers' names are masked (the first letter), unless someone with `pii.reveal` asks to see them, which is audited.

In the web app this is **Invoices > To match**: one card per payment with the invoices and orders it may be for ("Order AK-7QX4KD", with what is due and the customer masked), and below them, under **No invoice suggested**, money that had nothing to suggest or whose suggestions were all turned down. **Match to another invoice or order…** lists both while the business has orders waiting for money.

In chat, an order is asked about as one: "Is this for storefront order AK-7QX4KD, ₦3,000 to pay?", and **Yes, match** answers "Matched to order AK-7QX4KD. It is now paid."

Over the API: `GET /v1/money/suggestions` (each with `target_kind`, `target` such as `order:AK-7QX4KD`, `number`, `due_kobo` and `customer`), `GET /v1/money/unmatched` (the payments left for a person, and the `orders` they can be matched to), `POST /v1/money/suggestions/{id}/confirm` and `/reject`, and `POST /v1/money/entries/{id}/match` with `{"invoice": "INV-0104"}` or `{"order": "AK-7QX4KD"}`; `?reveal=true` with `pii.reveal` on the two lists. Confirming or matching to an order answers with the order and its payments.

## Automatic matching

Under **Invoices > Settings**, **Match strong suggestions automatically** is off by default. When on, a suggestion scoring 85 or more is matched without asking, provided the next suggestion for the same payment scores at least 15 less, whether it is for an invoice or an order. Two invoices or orders of the same amount with nothing else to tell them apart are left for a person. An automatic match is listed as matched automatically, with its reasons, and can be undone like any other.

## Undoing a match

**Undo** on an invoice's payment takes the payment out of the invoice: the invoice moves back (to sent, part-paid or overdue), and both the payment and its undoing stay in the invoice's history. The money goes back to be matched. The suggestion that led to the wrong match is not offered again, and the payment's other suggestions are offered again.

**Undo the match** on a storefront order's page (Storefront > Orders, under **Money matched**; `POST /v1/money/order-payments/{id}/undo`, `invoice.manage`) does the same for an order, and the order goes back to unpaid (`received`, or `accepted`). It is refused, with the reason, once the order went on with the payment: when the payment completed the order's checkout (its workflow was started or told it was paid), or when the order has been decided since (accepted, completed, declined, refund due). Cancel or [refund](storefronts.md#refunds) the order instead.

One payment counts towards one invoice or order at a time: to move it elsewhere, undo the match first.

## The money_matches table

Every match, and every undoing of one, is copied to a [kept table](data-tables.md#kept-tables) named `money_matches`, alongside `money_log`:

| Column | Holds |
| --- | --- |
| `match_id` | The match's id |
| `entry_id` | The money log entry it counts |
| `target` | What it counts towards, such as `invoice:INV-0104` or `order:AK-7QX4KD` |
| `amount_kobo` | The amount |
| `how` | `exact`, `suggested`, `suggested_auto` or `manual` |
| `by` | Who matched it (a person, or the system for exact and automatic matches) |
| `reasons` | Why, joined with "; " |
| `undoes` | For an undoing, the match it undoes |
| `matched_at` | When |

An invoice's paid amount is the sum of its matches that are not undone; an order is paid by one match that is not undone (or by its own payment check, when the money log does not have the payment).

## Not yet

- An order paid after acceptance is not suggested until its payment link is made: its workflow sends the link and waits for that payment.
- Money matched to an order by bank transfer has no Paystack payment to refund: an order declined after it is refunded by the business itself.
- Seeing and confirming orders' suggestions uses the invoice permissions (`invoice.read`, `invoice.manage`), not `storefront.manage`.
- Customers' permanent account numbers (`virtual_account`) are not linked to invoices yet, so a payment into one is suggested rather than matched exactly.
- Leaks, statements, cash and expenses come in the next milestone (T6).
