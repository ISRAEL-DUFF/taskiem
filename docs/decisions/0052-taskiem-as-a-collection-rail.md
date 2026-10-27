# 0052 — Taskiem receives money for any business through iswallet: a platform client, a wallet per business, segregated or pooled by the business's choice

Date: 2026-10-27 · Status: **Proposed** · Plan: [iswallet account numbers](../spec/iswallet-account-numbers.md), workstream W

## Context

Until now a business receives money in Taskiem only through a payments service it already has: Paystack, Flutterwave, Moniepoint, OPay or an iswallet client of its own ([decision 0044](0044-rails-and-products.md)). A business with none of these cannot send an invoice its customer can pay, give its customers account numbers, or run a school, until it has signed up somewhere else.

iswallet, Payrolla and Taskiem are products of one company (iSpend). iswallet issues API clients; a client holds wallets, and a wallet holds account numbers its customers pay into. iswallet confirmed on 27 October 2026 ([guide](../integrations/iswallet-answers-2026-10-27-account-numbers.md)) that one client may hold wallets for many unrelated businesses, that each business may be held **segregated** (its balance in its own account at the bank partner; per-student or per-customer credits reach it by a sweep, with the ledger right at once) or **pooled** (tracked on iswallet's ledger), and that Payrolla integrated as segregated on its own side. Taskiem already has the machinery for one client shared by many tenants: a wallet allow-list per connection, events routed by `wallet_id` and unrouted events alerted ([decision 0031](0031-shared-provider-clients.md)), and remote registration of the one subscription ([decision 0021](0021-remote-trigger-registration.md)).

## Decision

1. **Taskiem holds an iswallet client of its own**, live and test, as platform secrets (like the platform WhatsApp number), with one webhook subscription per environment registered by Taskiem and routed into tenants by `wallet_id`. The business never sees iswallet's key or dashboard.
2. **Any business can turn on "Receive money with Taskiem"** and gets a wallet created by Taskiem under the platform client, as a connection `iswallet/taskiem` that lists only its wallet. Everything that works on a business's own rail works on it: invoices, customers' account numbers, the money log, matching, the Schools area.
3. **Where the money is held is the business's choice**, made when it turns the rail on and changed only with iswallet: **segregated** (`client_end_user_business`: a school taking fees, anyone holding other people's money, or any business that wants its balance in its own account at the bank partner) or **pooled** (`client_end_business`). Taskiem says plainly what each means, including that per-customer credits reach a segregated account by iswallet's sweep while the balance shown is right at once. The default is segregated for schools and pooled otherwise.
4. **KYC is collected once, sealed, and sent once**: the business's email, and for a business wallet the director's BVN, as iswallet requires before an account number can be issued. Taskiem keeps the BVN only as sealed PII and never shows it again.
5. **Collection works as on every rail.** The business's wallet has its own "pay me" number (the wallet's single unreferenced account); an invoice gets a **one-time account number by default**, since many customers are billed once; a customer the business bills regularly can be given a permanent number of their own, and a school's students always have one. An invoice on this rail is payable from day one.
6. **Money leaves through the existing payout path**: the balance is shown, and "Withdraw to my bank" is an iswallet payout to the business's own bank account, resolved by name enquiry, under an approval policy by default, recorded in the money log like any payout.
7. **Fees**: iswallet's partner fee is passed to the payer by default, shown on receipts as the difference between what was sent and what landed; whether Taskiem charges on top, and on which plans the rail is on, is the founder's to set in billing.

## Alternatives considered

| Option | Why not |
| --- | --- |
| Keep requiring a rail of the business's own | A new business cannot receive a naira on day one, which is the moment it decides whether Taskiem is for it |
| One Taskiem wallet, every business's money pooled in it, tracked by Taskiem | Taskiem would become the ledger that 0045 and 0046 chose not to build; iswallet's wallets already do it, with segregation at the bank where a business wants it |
| Segregated for everyone | Pooled is cheaper and simpler for a business holding only its own money; the choice is theirs, as it is for Payrolla's customers |

## Consequences

- A platform client, its secrets, its subscription and its routing are operations items (`TASKIEM_ISWALLET_CLIENT_KEY`, live and test).
- The Schools area's issuing setting can say "Taskiem" with no wallet id to paste.
- Billing meters the rail; plans decide who has it.
- iswallet is asked to confirm the sweep's behaviour for pooled-to-segregated at volume, and whether a business may move from pooled to segregated later.

## Provenance

From the founder on 27 October 2026, after iswallet's guide and its answers: Taskiem is a client of iswallet and can create a wallet for any business and account numbers for it or its customers, so a business with no payments account receives money from day one; one client may hold wallets for many unrelated businesses; segregated or pooled is the business's choice, as Payrolla chose segregated; iswallet, Payrolla and Taskiem are one company.
