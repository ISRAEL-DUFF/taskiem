# Matching payments to invoices

Matching decides which invoice a payment is for. Taskiem looks at every payment the [money log](money-log.md) records as money in and settled, once, and either matches it, suggests where it may belong, or leaves it for a person. It follows [decision 0045](decisions/0045-tally-in-taskiem.md) and the [build plan](spec/invoices-and-money-log.md) (§8, T5).

A match is a payment counted towards an invoice, listed on the invoice with how it was matched and why. **Nothing else marks an invoice paid.**

## Exact matches

A payment into an account number or payment link Taskiem gave for an invoice ([paying an invoice](invoices.md#paying-from-the-page)) is matched to that invoice at once, with no one asked. Taskiem matches it when the payment service tells it about the payment, or when its own check every 30 seconds finds it, whichever comes first.

## Suggestions

Any other money in gets up to three suggestions among the business's open invoices (sent, part-paid or overdue). Each suggestion has a score from 0 to 100 and its reasons:

| Reason | Adds |
| --- | --- |
| Same amount as what is due | 50 |
| Part of what is due, on an invoice that takes part payments | 15 |
| The payment's reference names the invoice ("INV-0104", "inv0104") | 40 |
| The payer's name matches the customer's (every word of it, in any order) | 35 |
| The payer shares a name with the customer | 20 |
| Paid within 3 days of the invoice being sent | 15 |
| Within 14 days | 10 |
| Within 60 days | 5 |

The score is the sum of the reasons, at most 100. Invoices scoring under 40 are not suggested. A payment made before an invoice was sent is never suggested for it.

Reasons say what matched, never anyone's name: "the payer's name matches the customer's".

## Confirming, rejecting and matching by hand

People with `invoice.manage` work through the suggestions:

- **Confirm** a suggestion: the payment counts towards that invoice, and the payment's other suggestions close.
- **Reject** a suggestion: it is not offered again.
- **Match by hand** to any open invoice, when no suggestion fits.

People with `invoice.read` can see the suggestions.

Over the API: `GET /v1/money/suggestions`, `POST /v1/money/suggestions/{id}/confirm` and `/reject`, and `POST /v1/money/entries/{id}/match` with `{"invoice": "INV-0104"}`.

## Automatic matching

Under **Invoices > Settings**, **Match strong suggestions automatically** is off by default. When on, a suggestion scoring 85 or more is matched without asking, provided the next suggestion for the same payment scores at least 15 less. Two invoices of the same amount with nothing else to tell them apart are left for a person. An automatic match is listed as matched automatically, with its reasons, and can be undone like any other.

## Undoing a match

**Undo** on an invoice's payment takes the payment out of the invoice: the invoice moves back (to sent, part-paid or overdue), and both the payment and its undoing stay in the invoice's history. The money goes back to be matched. The suggestion that led to the wrong match is not offered again, and the payment's other suggestions are offered again.

One payment counts towards one invoice at a time: to move it to another invoice, undo the match first.

## The money_matches table

Every match, and every undoing of one, is copied to a [kept table](data-tables.md#kept-tables) named `money_matches`, alongside `money_log`:

| Column | Holds |
| --- | --- |
| `match_id` | The match's id |
| `entry_id` | The money log entry it counts |
| `target` | What it counts towards, such as `invoice:INV-0104` |
| `amount_kobo` | The amount |
| `how` | `exact`, `suggested`, `suggested_auto` or `manual` |
| `by` | Who matched it (a person, or the system for exact and automatic matches) |
| `reasons` | Why, joined with "; " |
| `undoes` | For an undoing, the match it undoes |
| `matched_at` | When |

An invoice's paid amount is the sum of its matches that are not undone.

## Not yet

- Storefront orders are matched by their own payment check, not through suggestions.
- Customers' permanent account numbers (`virtual_account`) are not linked to invoices yet, so a payment into one is suggested rather than matched exactly.
- Leaks, statements, cash and expenses come in the next milestone (T6).
