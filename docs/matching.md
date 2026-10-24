# Matching payments to invoices and orders

Matching decides which invoice or [storefront order](storefronts.md#payments-and-matching) a payment is for. Taskiem looks at every payment the [money log](money-log.md) records as money in and settled, once, and either matches it, suggests where it may belong, or leaves it for a person. It follows [decision 0045](decisions/0045-tally-in-taskiem.md) and the [build plan](spec/invoices-and-money-log.md) (§8, T5); customers' own account numbers, split payments, payer memory and counting what the payer sent follow [decision 0046](decisions/0046-balans-in-taskiem.md) (points 5 to 7) and the [schools plan](spec/schools.md) (§7, B1), and work for every business.

A match is a payment counted towards an invoice or an order, listed on it with how it was matched and why. **Nothing else marks an invoice paid**, and an order is marked paid along the same path whether its own payment check or matching hears of the money first.

## Exact matches

A payment into an account number or payment link Taskiem gave for an invoice ([paying an invoice](invoices.md#paying-from-the-page)) is matched to that invoice at once, with no one asked. Taskiem matches it when the payment service tells it about the payment, or when its own check every 30 seconds finds it, whichever comes first.

A payment through a storefront order's Paystack checkout page (its reference is the one Taskiem gave the page) is matched to that order the same way: when the Paystack event reaches the money log, or when the order's own payment check finds it, whichever comes first. Both write the same money log entry, its match, and the order's move to paid together, under the money's lock and the order's, so the order is matched and marked paid once, and its workflow is started or told once. An order cancelled or expired before its money came, or already paid by other money, is left for a person.

## Customers' own account numbers

A customer can have a permanent account number of their own: a `virtual_account` issued for them on one of the business's connections (for a school, a student's account). Registering it links the connection, the account number and the account's reference to the customer (`POST /v1/customer-accounts`, `invoice.manage`; `invoicing.Invoices.RegisterAccount` in the engine, which issuing students' accounts will call). The same account for the same customer again is the same registration; an account already another customer's, or registered with another number or reference, is refused.

**Who a customer is.** Invoices carry the customer inline, so matching knows a customer by the business's own key for them when the invoice gives one (`customer.key`, such as `student:42`), else by their phone number, else their email, as the pseudonym of their personal data (never the number itself). Invoices with the same key, or without a key and the same phone, are one customer's. Siblings invoiced under their parent's phone are one customer unless each has a key.

Money paid into a registered account is matched **exactly**, with no one asked, when its connection, its account number and its reference all agree with the registration (a payment service that does not say which account number was paid into, as Flutterwave's and Moniepoint's notices do not, is taken at the reference it issued for the account): to the customer's oldest open invoice (sent, part-paid or overdue, by due date, then when sent), as much as is due, then the next, until the money runs out. Each invoice's part says "paid into the customer's own account number" and "the customer's oldest open invoice" (or "what was left after the customer's older open invoices"); reasons never name anyone.

For a school's students, where the school shares family credit (**Schools > Settings**, off by default; [schools](schools.md#brothers-and-sisters)), what is left after the student's own open invoices then counts towards their brothers' and sisters' open invoices (the other students of their family, by admission number), each one's oldest first: "for a sibling (the school shares family credit)". Each part is counted once, however often the money is looked at.

What is left is the customer's **credit**. It is shown on each of their invoices (**The customer's money**, and `GET /v1/invoices/{id}/customer`), and counted towards the next invoice sent to them: when an invoice is sent, the customer's credit pays it first, oldest money first, each part a match of the money it came from ("the customer's credit from an earlier payment"), and then, where the school shares family credit, a brother's or sister's ("a sibling's credit from an earlier payment"); the message sent says what is left to pay, or that nothing is. Undoing a part of a customer's money makes it their credit again, rather than money to match.

A credit naming a registered account by its reference but paid into another account number, paid into a registered account number with another account's reference, or naming the account by its reference without saying which account number it was paid into, is **held** for a person: it waits under **No invoice suggested** in To match with the reason, and nothing is counted. A credit into an account Taskiem does not know is suggested as any other money. Another business's accounts are never looked at.

Issuing students' account numbers registers each one ([schools](schools.md#account-numbers)); credits that never arrive as a webhook are read back from the rail every 15 minutes ([the look-back](schools.md#the-look-back)) and matched the same way.

## Split payments

One payment can count towards several invoices and storefront orders (two children's fees, two terms), each part a match of its own, listed on its invoice and undone on its own; undoing one part leaves the others. The parts never add up to more than the payment counts for: the database refuses a part that would, under the lock every writer takes on the payment, so two people splitting it at once cannot count it twice.

When a suggestion's payment is more than is due on its invoice, To match offers **Split across the customer's invoices…**: the same customer's open invoices oldest first, each as much as is due, the amounts editable. What the parts leave over is kept as the customer's credit (the default), or waits in To match to be matched by hand (left_kobo says how much). In chat the question says so ("It is more than that, so it would be split across the customer's open invoices, oldest first: INV-0101 ₦20,000, INV-0104 ₦30,000, ₦10,000 left as the customer's credit") and **Yes, match** matches each part.

Over the API, `POST /v1/money/suggestions/{id}/confirm` and `POST /v1/money/entries/{id}/match` take `{"parts": [{"invoice": "INV-0101", "amount_kobo": 2000000}, {"order": "AK-7QX4KD", "amount_kobo": 300000}], "credit_rest": true}` (credit_rest only when every part is one customer's invoices), and confirm takes `{"split": true}` to split as offered; each suggestion lists its offered `split`. Without parts, what is left of the payment counts towards the one invoice or order, as before. A second part of one payment towards the same invoice is not added: undo the part and split again.

## Payer memory

When a person confirms a suggestion or matches money by hand to a customer's invoice, Taskiem remembers the payer for that customer: their name as the rail sent it and, where the rail gives it, the paying account. Payers are personal data: what the rail sent is kept sealed, and the payer is found again by a pseudonym derived from their name (its words, in any order and case) or account (its digits) with the business's own key. Each business's payers are its own.

The next payment from a remembered payer suggests that customer's open invoices with "this payer paid for this customer before" (or "this paying account paid for this customer before"), a strong reason: with the business's **automatic matching** on, a remembered payer's payment made within 3 days of the invoice being sent is matched on its own when it is the clear best. **Forget this payer** on an invoice's customer (`DELETE /v1/money/payers/{id}`, `invoice.manage`) forgets them; their next payment is suggested as anyone's. Remembered payers are listed masked unless someone with `pii.reveal` shows them, which is audited.

## What the payer sent

Money counts towards invoices for what the payer sent. Credits are recorded with what the payer sent as their amount (the rails' mappings add the fee back where a rail settles net); an entry recorded as what landed with the fee already taken (`net`) counts for its amount plus its fee (`moneylog.CountableKobo`, and the database's `taskiem_money_entry_countable`). The fee is a cost to the business: the journal posts what landed to the bank, the fee to fees, and the whole amount to what the payment counts towards.

## Suggestions

Any other money in gets up to three suggestions among the business's open invoices (sent, part-paid or overdue) and the storefront orders waiting for money (waiting for their checkout payment, or taken with no online payment and not yet paid, cancelled, expired or decided against). Each suggestion has a score from 0 to 100 and its reasons:

| Reason | Adds |
| --- | --- |
| Same amount as what is due (for an order, what is paid online: the deposit, or the total) | 50 |
| Part of what is due, on an invoice that takes part payments (orders take none) | 15 |
| The payment's reference names the invoice or order ("INV-0104", "inv0104", "AK-7QX4KD") | 40 |
| The payer's name matches the customer's (every word of it, in any order) | 35 |
| The payer shares a name with the customer | 20 |
| The paying account paid for this customer before ([payer memory](#payer-memory)) | 75 |
| The payer (by name) paid for this customer before | 70 |
| Paid within 3 days of the invoice being sent, or the order placed | 15 |
| Within 14 days | 10 |
| Within 60 days | 5 |

The score is the sum of the reasons, at most 100. A remembered payer alone with a payment within 3 days scores 85 (70 + 15), enough to be matched automatically when it is clear; with the same amount it scores 100. A remembered paying account within 14 days scores 85. Invoices and orders scoring under 40 are not suggested. A payment made before an invoice was sent, or an order placed, is never suggested for it. Only the business's own invoices and orders are ever suggested.

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

A school's families can also say on [their page](schools.md#the-family-page) that they paid some other way: **To match** lists those claims ("Families say they paid", `claims` in `GET /v1/money/unmatched`) beside the money, so the bursar can find the payment they describe and match or post it. A claim never counts as a payment; one the school cannot find is dismissed with a reason.

## Automatic matching

Under **Invoices > Settings**, **Match strong suggestions automatically** is off by default. When on, a suggestion scoring 85 or more is matched without asking, provided the next suggestion for the same payment scores at least 15 less, whether it is for an invoice or an order. Two invoices or orders of the same amount with nothing else to tell them apart are left for a person. An automatic match is listed as matched automatically, with its reasons, and can be undone like any other.

## Undoing a match

**Undo** on an invoice's payment takes the payment out of the invoice: the invoice moves back (to sent, part-paid or overdue), and both the payment and its undoing stay in the invoice's history. The money goes back to be matched. The suggestion that led to the wrong match is not offered again, and the payment's other suggestions are offered again.

**Undo the match** on a storefront order's page (Storefront > Orders, under **Money matched**; `POST /v1/money/order-payments/{id}/undo`, `invoice.manage`) does the same for an order, and the order goes back to unpaid (`received`, or `accepted`). It is refused, with the reason, once the order went on with the payment: when the payment completed the order's checkout (its workflow was started or told it was paid), or when the order has been decided since (accepted, completed, declined, refund due). Cancel or [refund](storefronts.md#refunds) the order instead.

A payment's parts are undone one by one; to move money elsewhere, undo its part first.

## Money that went back

Money can go back to the payer after it counted: the rail returns or reverses a payment (`payment.returned`, a POS sale reversed), or the business refunds it (a refund that settled, `refund.processed`, naming the payment by its reference, or a credit into a customer's account by the rail's own id for it). Taskiem takes each such movement once, against the payment it takes back (decided in the [schools plan](spec/schools.md#18-risks-and-open-questions), for every business):

- **its matches are undone**, newest first, each invoice back to sent, part-paid or overdue, with the payment and its undoing kept in the invoice's history ("system:reversal");
- the payment now counts for what it was **less what went back**, in the database too, so nothing can count it again, and it is never anyone's credit: refunded all, it leaves To match ("the money went back to the payer"); refunded in part, what is left is matched again by the permanent-account rule when it was paid into a customer's account (oldest open invoice first), otherwise it waits in To match with the reason "part of this payment went back to the payer; match what is left";
- **the owners are told** (in their notifications, on their linked chat, and on the business's alert channels), and so are the people who match money (those with `invoice.manage`, such as the Bursar), naming the invoices now open again, never anyone's name; once per movement;
- a school's [receipts](schools.md#receipts) for the undone parts are voided, never deleted (the matcher's `Reversed` hook);
- a payment heard to have gone back before it was ever looked at is never offered for matching.

Parts of the payment counted towards storefront orders are left: an order has its own [refunds](storefronts.md#refunds).

## The money_matches table

Every match, and every undoing of one, is copied to a [kept table](data-tables.md#kept-tables) named `money_matches`, alongside `money_log`:

| Column | Holds |
| --- | --- |
| `match_id` | The match's id |
| `entry_id` | The money log entry it counts |
| `target` | What it counts towards, such as `invoice:INV-0104` or `order:AK-7QX4KD` |
| `amount_kobo` | The amount: for a split, this part's |
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
- A customer's credit can be used only by the next invoice sent to them (or a sibling's, where the school shares family credit), or matched by hand; it cannot yet be refunded from Taskiem.
- Two partial refunds of one payment are one movement in the money log (a refund is known by the payment it refunds), so only the first one's amount is taken back.
- Payers are remembered only for invoices' customers, not storefront orders'.
- Leaks, statements, cash and expenses come in the next milestone (T6).
