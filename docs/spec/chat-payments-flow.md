# The payments flow

Status: draft, started 10 November 2026. Each part is added as it is agreed.

## Summary

Money moves inside the chat, the way orders and deliveries already do. A
buyer pays a bill without leaving the conversation, and both sides see
the same card move from "To pay" to "Paid".

Part 1, below, is the first piece: **pay a bill by transfer, in the chat**.
Today, Pay on a bill card opens the seller's checkout page in a new tab.
Instead, Pay gives the buyer an account number for that bill. The chat
shows the number, how much to send and how long the number is good for,
and the card updates by itself when the money lands.

## What exists today

- **A bill card in a business chat** (L5, phase 3a) has Pay, which is one
  of these:
  - **Pay ₦…**: the seller's `pay_link`, their checkout page, opened in
    a new tab. The buyer pays there and comes back. Nothing in the chat
    shows the payment until the seller's side counts it.
  - **Pay from Taskiem**: when the buyer's business has a payments
    account. It starts the buyer's pay-linked-bill workflow, which a
    person approves (`POST /v1/bills/{id}/pay`).
  - **How to pay**: neither of the above. It opens the bill page.
- **One-time account numbers for invoices** (decision 0045; iswallet I2,
  N3): `engine/invoicing/accounts.go`.
  - `Issue` gives an invoice a number on the seller's rail, for an amount,
    with Taskiem's own valid-until time (iswallet's expiry is only
    advisory).
  - Requests are queued and drained (`Request`, `Drain`), because the
    public role holds no rail keys.
  - It has limits per invoice and per hour (`ErrLimit`).
  - `CheckPayments` asks the rail every 30 seconds about each number still
    open. `Received` counts a payment towards the invoice, including late
    and wrong amounts (`on_late`, `on_wrong_amount`).
  - The customer invoice page, WhatsApp's **Pay by transfer** and the
    storefront order page (`IssueForOrder`) already use it.
- **The seller's invoice is the buyer's bill.** A linked seller's invoice
  is mirrored to the buyer as a bill (`links.Bill`, `pay_link`,
  `due_kobo`), and payments the seller counts reach the buyer's bill.

So the number, its expiry and the counting all exist on the seller's
side. What is missing is the buyer asking for a number from the chat,
the card that shows it, and both cards following the payment.

## Part 1: pay a bill by transfer, in the chat

### The buyer's side

1. **Pay.** On a bill card with something left to pay, **Pay ₦83,000**
   opens a small sheet over the chat, not a new tab:
   - **Pay by transfer** (the default, when the seller's rail gives
     one-time numbers);
   - **Pay from Taskiem** (as today, when the buyer has a payments
     account);
   - **Pay on their checkout** (the `pay_link`, as today), for those who
     want a card.
   - With only one of these available, Pay goes straight to it.
2. **The number.** Pay by transfer asks for a number for this bill (see
   "Issuing" below). While it comes, the sheet says "Getting an account
   number…". A transfer card then shows:
   - the bank, account number and account name, each with a copy button
     (the number copies without spaces);
   - **the amount to send, exactly**: "Send exactly ₦83,000";
   - a countdown: "Good for 29:12". In the last 5 minutes it turns to
     the warning colour and says "Expires in 4:05";
   - what happens next: "We'll mark it paid here as soon as it lands."
3. **Waiting.** The card stays in the chat as the newest card of the bill,
   so the buyer can leave, open the bank app and come back. It checks for
   the payment while open (as live cards do) and also hears the server's
   change (the chat's live updates). It says "Waiting for your transfer"
   with a quiet moving dot, never a spinner over the number.
4. **Paid.** When the rail reports the transfer, the card turns to
   "Paid ₦83,000 · 14:52" with the paid tick and the good-moment sound
   (if on), the countdown goes, and the bill card above it shows Paid.
   - **Part paid** (less arrived): "Received ₦50,000 of ₦83,000.
     ₦33,000 left" with **Pay the rest**, which asks for a new number
     for what is left.
   - **More than due**: counted as the rail's `on_wrong_amount` says
     today; the card says what was received and that the seller has been
     told.
5. **Expired.** At zero the card says "This number has expired. Don't
   send to it" and offers **Get a new number**. A transfer that arrives
   late is still counted, as today; the card turns to Paid if it does.
6. **Elsewhere.** The same card shows on the bill's page on the web. On
   WhatsApp and Telegram, "pay INV-0001" (and the bill message's Pay
   button) answers with the number, the amount and the time in words, and
   "Paid, thank you" when it lands.

### The seller's side

- The seller sees nothing to do. Their invoice card in the chat follows
  the payment: "Ada's Kitchen is paying by transfer" while a number is
  open, then "Paid ₦83,000" with the diary line and the money log entry,
  as any invoice payment has today.
- The number is on the seller's rail and the money is theirs. The buyer
  never sees the seller's keys or other numbers.

### Issuing

- **One open number per bill at a time.** Pay again while one is open
  shows the same number and countdown, never a second. A new one is given
  only when the last has expired, or for "Pay the rest".
- **The amount is what is due now.** The number is for the bill's
  `due_kobo` when it is asked for; part payments and queries change what
  the next number is for.
- **How long it is good for**: 30 minutes, as invoice numbers are today
  (`AccountMinutes`, kept within the rail's own range). Whether a
  business may set its own is open question 5.
- **Who may ask**: someone in the buyer's business with leave to manage
  bills (`invoice.manage`), as for the bill page. The request crosses to
  the seller's side the way link actions do (a definer function owned by
  `taskiem_dispatch`), as a request on the seller's invoice, so the
  seller's rail keys never leave the seller.
- **Limits**: the invoice limits that exist (per invoice, per hour). Past
  them: "Too many numbers asked for this bill. Try again in an hour, or
  pay on their checkout."
- **A rail without one-time numbers**, or the seller's rail down: Pay by
  transfer isn't offered. If it fails part way, the sheet says so in
  plain words and offers the checkout or Pay from Taskiem.
- **A customer with a permanent number** on the seller's wallet (iswallet
  rule): that number is shown, with no countdown, and payments to it are
  matched as today.
- **A disputed bill** (queried): Pay by transfer waits for the query to
  be settled, as Pay does today.

### The card's states

| State | Buyer's card says | Seller's card says |
| --- | --- | --- |
| Asking | "Getting an account number…" | (nothing yet) |
| Open | Bank, number, name, "Send exactly ₦83,000", "Good for 29:12" | "Ada's Kitchen is paying by transfer" |
| Last 5 minutes | "Expires in 4:05" (warning colour) | as Open |
| Part paid | "Received ₦50,000 of ₦83,000", Pay the rest | "Received ₦50,000 of ₦83,000" |
| Paid | "Paid ₦83,000 · 14:52" | "Paid ₦83,000 · 14:52" |
| Expired | "Expired. Don't send to it", Get a new number | (back to To pay) |
| Couldn't get one | Why, in plain words, with the other ways to pay | (nothing) |

### Data, engine and API

- **Engine**: the invoices' `Issue`, `Request`, `Drain`, `CheckPayments`
  and `Received`, unchanged in what they do. A request records who asked
  (the buyer's business, by link) so the seller's audit and money log say
  "asked for by Ada's Kitchen in the chat".
- **API (buyer)**:
  - `POST /v1/bills/{id}/transfer` asks for, or returns, the open number:
    `{account: {bank, number, name, amount_kobo, expires_at, state}}`,
    `202` while it is being issued.
  - `GET /v1/bills/{id}/transfer` reads it.
  - The bill's view gains `transfer` (the open one, if any) and
    `ways_to_pay` (`transfer`, `taskiem`, `checkout`).
- **Live**: the issued number, a payment and expiry reach both chats as
  card events (`bill.transfer.open`, `.paid`, `.part`, `.expired`), so
  cards update without a reload.
- **Mirror**: the buyer's bill shows the seller's account through the
  link mirror, the same way the bill itself arrives.

### Safety and plain words

- The number is shown only to the buyer's people who may manage bills,
  and only for that bill. It is never put in a diary line, a
  notification preview or the AI's context.
- "Send exactly" is always the amount on the number. A wrong amount is
  counted as the rail reports it, never silently "paid".
- The countdown follows the server's `expires_at`, not the phone's
  clock, and stops at zero.
- Words: no "virtual account", "dynamic", "rail" or "reference" on
  screen. "Account number", "Good for", "Send exactly", "Paid".

### Tests

- **Server**: asking twice gives one number; expiry then a new one; part
  paid then Pay the rest; late payment after expiry counted; a disputed
  bill refuses; someone without `invoice.manage` refused; limits; the
  seller's keys never reach the buyer's tenant (isolation test).
- **Browser** at 390 and 360 px, both themes, with axe: Pay → sheet →
  number with copy buttons and countdown → paid by the fake rail → both
  cards Paid; the last-5-minutes warning; expired → Get a new number;
  part paid → Pay the rest.
- **WhatsApp**: "pay INV-0001" answers with the number in words; paid
  says so.

### Open questions

1. **Fees.** If the seller's rail charges per transfer, who bears it? Today
   the invoice fee rule (N1) applies to the seller. Keep it so?
2. **Default way to pay.** Should Pay by transfer be first even when the
   buyer has Pay from Taskiem? Paying from Taskiem needs an approval; a
   transfer from the buyer's own bank needs none in Taskiem.
3. **Several bills at once.** A buyer owing three bills to one supplier
   may want one number for all three. Leave that for part 2
   (statements), or include it here?
4. **Customers (not linked businesses).** The customer thread's invoice
   cards could use the same transfer card. Same part, or the next?
5. **How long a number is good for.** Fixed at 30 minutes today. A buyer
   paying from a business bank account with approvals may need longer.
   Should the seller be able to set it (within the rail's range)?
