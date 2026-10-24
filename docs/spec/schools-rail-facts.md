# Schools: what each rail does for customers' account numbers (B0)

Status: **B0 of the [schools plan](schools.md)**, 23 October 2026 · Decision: [0046](../decisions/0046-balans-in-taskiem.md)

What iswallet, Paystack, Flutterwave and Moniepoint do for **permanent account numbers** (one per student or customer), read from each rail's public documentation (the [clean-room policy](../clean-room-policy.md)) and, for iswallet, from its engineering's written answers. "Not documented" means it must be asked or tested in sandbox; nothing undocumented is mapped. The mappings built from these facts are in each connector's `manifest.yaml`, with conformance cases under `testdata/capabilities`; the capabilities are in [`schemas/capabilities-v1.json`](../../schemas/capabilities-v1.json) and [connector/v1 rule 16](../contracts/connector-v1.md).

## What Taskiem asks of a rail

| | |
| --- | --- |
| `virtual_account` (issue) | Input: `customer` (`name`, `email`, `phone`, `bvn`, and now `nin`), `account_name` (the name payers should see, where the rail lets the business set it; added in B0), `account` (the wallet the number pays into, on rails that hold several; added in B0). Output: `account_number`, `bank_name`, `reference`, and `account_name` where the rail reports it (no longer required: Flutterwave does not) |
| `virtual_account.credited` (event) | Correlated on `reference` (the account's). Fields (`$defs/credit`): `reference`, **`credit_id`** (new, required: the rail's own id for this one credit), `amount_kobo` (what the payer sent), `fee_kobo` and `settled_kobo` (where the rail reports them), `currency`, `account_number`, `payer_name`, `payer_account`, `payer_bank`, `narration`, `occurred_at` |
| `list_credits` (new lookup) | Input: `since` (required), `until`, `account`. Output: `credits` (each with the fields of the event and the same `credit_id`), `truncated` (the rail had more than was read: read a shorter window). For the overlapping look-back of the plan's §6.5 |

**Why `credit_id`.** A permanent account takes many credits under one reference. The money log knew a movement by connection, event and reference, so a second payment into the same student's account would have been taken for a repeat of the first and dropped. The log now keys an account credit on its `credit_id` too (and never treats one credit as a later status of another), so each payment is one entry, and the same credit heard by webhook, by polling or by the look-back is still one.

**Gross and net.** `amount_kobo` is always what the payer sent, so a family is credited what they paid (plan §7.4); `settled_kobo` and `fee_kobo` say what landed and the fee where the rail says so. A rail that reports only what landed and no fee gives that as `amount_kobo` (plan §18: counted as landed and flagged).

## iswallet

Sources: iswallet's OpenAPI at `https://synledger.name.ng/iwallet/openapi.json` (served as `/iwallet/docs/doc.json`), read 23 October 2026: "Issue virtual account", "Bulk-issue virtual accounts", "List virtual accounts", "List transactions", "Get transaction", "Look up a transaction by provider reference"; iswallet's answers of [5 October](../integrations/iswallet-answers-2026-10-05.md) (§E1, §F1 to §F3) and [12 October](../integrations/iswallet-answers-2026-10-12-pgdock.md) (items 7, 8, 22, 28).

| Fact | iswallet |
| --- | --- |
| Product | Virtual accounts (NUBANs) on a wallet, issued by iswallet's partner bank (its example: Flutterwave behind it, Wema Bank). A wallet holds several, one per provider and client reference |
| Issuing | `POST /v1/wallets/{id}/virtual-accounts/batch` with `accounts: [{reference, account_name}]` (up to 500 a call, per-item results `created`, `existing`, `failed` with `error_code`), **idempotent per reference**. Single issue `POST /v1/wallets/{id}/virtual-account` exists, but its request is not in the OpenAPI (the answers show `is_permanent`, `client_reference`). **Mapped:** the batch with one account |
| Customer fields, KYC | None per customer. **A business wallet needs its director's BVN** before any number is issued (`422 KYC_REQUIRED`: missing email, or the director's BVN) |
| Business must be enabled | Not stated beyond the director's BVN; `PROVIDER_NOT_CONFIGURED` is a documented refusal |
| Naming | The batch request takes `account_name`, but the answers of 12 October say "Account name and bank are the provider's, not ours to format. Expect a derived name." Length limit not documented. **Question** |
| Reference tying a credit to the account | Our reference (the batch's `reference`, Taskiem's key); a listed transaction carries `virtual_account.reference` and `virtual_account.account_number` |
| Credit webhook | `wallet.credit.posted` with `source_type: va_deposit`; `data`: `wallet_id`, `amount` (**gross**, what the payer sent), `amount_credited` (**net**, what landed), `currency`, `balance_after`, `txn_id`, `operation_id`, `source_ref`. The OpenAPI says the webhook resolves the account "exactly as" the transaction list does and carries the same `provider_reference`, but **does not say in which fields**. **Not mapped**: question |
| Lookup for a window | `GET /v1/wallets/{id}/transactions` with `from`, `to` (RFC 3339), `limit` (up to 100), cursor-paged (`pagination.has_more`, `next_cursor`). Each item: `amount` (on this route the ledger movement, not the gross), `amount_credited`, `fee` (iswallet's), `fee_charged` (the provider's, when disclosed; absent means unknown, never zero), `counterparty` (`name`, `account_number`, `bank_code`, `bank_name`), `narration`, `occurred_at`, `provider_reference` ("the SAME id the credit webhook carries"), `virtual_account` (`account_number`, `reference`). **Mapped** to `list_credits`: `credit_id` = `provider_reference`; `settled_kobo` = `amount_credited`; `fee_kobo` = `fee_charged`; `amount_kobo` = their sum (only what landed when `fee_charged` is absent) |
| Gross or net | Settles **net** by default ("pass-through": the payer bears the provider's fee); `absorb` (credited in full, iswallet bears it) is a commercial setting |
| Fees | Deposits: the provider's 1.4%, capped at ₦2,000 (₦50,000 sent credits ₦49,300). No fee per account issued is documented |
| Limits | Tier limits (TIER_1 ₦50,000 a day) apply to payouts, not deposits. 100 requests a minute per key; batch of 500 per call |
| Lost credit | A deposit iswallet cannot attribute is **parked** and not visible to a client key; webhooks are retried 10 times over 8 to 11 hours, then dead-lettered |

## Paystack

Sources, read 23 October 2026 (Paystack's documentation mirror `docs-v2.paystack.com`, as in T0): [Dedicated virtual accounts guide](https://docs-v2.paystack.com/docs/payments/dedicated-virtual-accounts/), [Dedicated Virtual Account API](https://docs-v2.paystack.com/docs/api/dedicated-virtual-account/), [Customer API](https://docs-v2.paystack.com/docs/api/customer/), [Transaction API](https://docs-v2.paystack.com/docs/api/transaction/) (List Transactions), and the support article [Dedicated virtual accounts](https://support.paystack.com/hc/en-us/articles/360009881760).

| Fact | Paystack |
| --- | --- |
| Product | Dedicated virtual accounts (DVA), at Wema Bank or Titan-Paystack (`GET /dedicated_account/available_providers`); `test-bank` with a test key |
| Who can | "Only available to registered businesses in Nigeria" that have completed go-live. Businesses in **financial services, betting and general services** must validate each customer (BVN or bank account) before an account is issued. The customer's consent is expected |
| Issuing | Multi-step: `POST /customer` (`email`, `first_name`, `last_name`, `phone`, `metadata`), then `POST /dedicated_account` (`customer` code, `preferred_bank`, optional `first_name`, `last_name`, `phone`, split), answering the account at once. Single-step `POST /dedicated_account/assign` answers only "in progress" (then `dedicatedaccount.assign.success` or `.failed`). **Mapped:** fetch the customer by email (`GET /customer/:email_or_code`, which carries `dedicated_account`), create the customer only if Paystack does not know it, then `POST /dedicated_account`. Unsafe: Paystack documents neither our reference on these calls nor what a second create does |
| Customer fields | Email, first and last name, phone (the support article: phone, full name and email). **One customer per email**, so each student needs an email of their own |
| Naming | **Not set by the business**: "Product Name / Customer Name" (Paystack-Titan `Business Name/ Customer Name`, Wema with a space before the slash). A custom naming format is a request to Paystack's support, for businesses offering accounts as a service. Length limit not documented |
| Reference | Paystack's customer code (`CUS_…`): every payment's `data.customer.customer_code` |
| Credit webhook | `charge.success` with `authorization.channel: dedicated_nuban`; the guide's example shows `authorization.sender_name`, `sender_bank`, `sender_bank_account_number` (**masked**, `XXXXXX0011`), `narration`, `receiver_bank_account_number`, `receiver_bank`. **Mapped** to `virtual_account.credited` (and no longer to `payment.received`): `reference` = customer code, `credit_id` = `data.reference`, `amount_kobo` = `data.amount` |
| Lookup for a window | `GET /transaction` with `from`, `to`, `status`, `customer` (id), `perPage`, `page` (`meta.pageCount`). **Mapped** to `list_credits`, keeping `dedicated_nuban` payments. `GET /dedicated_account/requery` (`account_number`, `provider_slug`, `date`) makes Paystack re-send webhooks for unprocessed transfers, at most once every ten minutes per account; not used. Also: the money log's existing poll of payments now records dedicated-account payments as account credits |
| Gross or net | `amount` is the transaction's amount; `fees` is in the transaction object, but neither its unit nor whether it is taken from the amount at settlement is documented. **Fee not mapped**: question |
| Fees | 1% per payment, capped at ₦300 (support article). No fee per account documented |
| Limits | 1,000 dedicated accounts per business by default; more on request to Paystack's support |

## Flutterwave

Sources, read 23 October 2026: [NGN virtual accounts](https://developer.flutterwave.com/v3.0/docs/ngn-virtual-accounts), [Get all transactions](https://developer.flutterwave.com/v3.0/reference/get-all-transactions), [Webhooks](https://developer.flutterwave.com/v3.0/docs/webhooks), [pricing](https://flutterwave.com/ng/pricing). The connector is on API v3, as before.

| Fact | Flutterwave |
| --- | --- |
| Product | Static (permanent) virtual accounts: `is_permanent: true` (Flutterwave MFB in the example) |
| Issuing | `POST /v3/virtual-account-numbers` with `email`, `is_permanent`, `bvn` or `nin`, `tx_ref`, `firstname`, `lastname`, `phonenumber`, `narration`; answers `account_number`, `bank_name`, `order_ref`, `flw_ref`, `note` at once. Bulk: `POST /v3/bulk-virtual-account-numbers` (`batch_ref`, `bulk_data`), queued, read with `GET /v3/bulk-virtual-account-numbers/{batch_id}`. **No lookup of one account**, so the mapped single issue is **unsafe** (an unknown outcome parks), as the one-time account is |
| KYC | "To create a static NGN Virtual Account, you need your customer's **NIN or BVN**": for a student, a parent's. The bulk example reuses one BVN for two accounts without comment; whether one BVN may hold many accounts is not documented |
| Naming | Not documented: `narration` "The name should be shown when the account details are fetched", and the answer has no account name (only `note`: "Please make a bank transfer to …"). **Mapped:** `account_name` sent as `narration`; `account_name` not answered. Question |
| Reference | `tx_ref`: "The transaction reference that should be returned whenever a transfer is made into the account". Taskiem sends `tva_` and the step's key, which also tells these credits from one-time accounts' payments |
| Credit webhook | `charge.completed` with `data`: `id`, `tx_ref`, `flw_ref` (the session id, for reconciliation), `amount`, `currency`, `charged_amount`, `app_fee`, `merchant_fee`, `narration`, `status`, `payment_type: bank_transfer`, `created_at`, `account_id`, `customer`; top-level `meta_data`: `originatorname`, `originatoraccountnumber` (masked), `bankname`, `bankcode`, `originatoramount` (`N/A` in the example); top-level `event.type: BANK_TRANSFER_TRANSACTION`. **Mapped** to `virtual_account.credited` (and no longer to `payment.received`): `credit_id` = `data.id`, `amount_kobo` = `data.amount` in naira × 100 |
| Lookup for a window | `GET /v3/transactions` with `from`, `to` (dates, YYYY-MM-DD), `page`, `status`, `tx_ref`, `customer_email`; items carry `id`, `tx_ref`, `amount`, `amount_settled`, `app_fee`, `narration`, `created_at`, `account` (`nuban`, `bank`, meaning not stated). Page size not documented. **Mapped** to `list_credits` (whole days; payer not in the list) |
| Gross or net | **Not documented.** The example credit has `amount` 100, `charged_amount` 102, `app_fee` 2; the card example `amount` = `charged_amount` 100 with `app_fee` 1.4. Which is what the payer sent, and whether the fee is taken from it, is not said. `amount_settled` (list only) is not defined. **Fee and settled not mapped**: question |
| Fees | Local collections 2% (1.4% transaction + 0.6% platform), plus 7.5% VAT, no cap stated; nothing specific to virtual accounts or per account |
| Underpayment | Documented for accounts with an amount: less is reversed, the excess of more is reversed. A static account without `amount` (as Taskiem opens it) is not described; it is expected to take any amount, to confirm in sandbox |
| Limits | 429 per merchant, no numbers. Webhooks retried 3 times, 30 minutes apart, when retries are on |

## Moniepoint (naming only)

Sources: Monnify documentation (`teamapt.atlassian.net/wiki/spaces/MON`), read 23 October 2026: "Reserve Account with Limit" (field reference) and "Getting all transactions on a reserved account"; `developers.monnify.com` refused the request (403), so the wiki was used, as for the connector itself.

| Fact | Moniepoint (Monnify) |
| --- | --- |
| Naming | `accountName` is mandatory: "The name you want to be attached to the reserved account. This will be displayed during name inquiry." **The business sets it.** No length limit and no merchant prefix are documented. **Mapped:** `account_name`, or the customer's name when none is given |
| KYC | `bvn` or `nin`, depending on the account's tier (already mapped; `nin` now from `customer.nin`) |
| Credit | `SUCCESSFUL_TRANSACTION` with `product.type: RESERVED_ACCOUNT` (already mapped); `credit_id` is now `eventData.transactionReference` |
| Lookup | `GET /api/v1/bank-transfer/reserved-accounts/transactions?accountReference=&page=&size=` lists one account's transactions. Not mapped in B0 (Moniepoint is naming only) |

## Naming, side by side

| Rail | Business sets the name | What payers see | Limit |
| --- | --- | --- | --- |
| iswallet | Sent, but iswallet says its partner names the account | The partner's derived name | Not documented |
| Paystack | No (custom naming on request to support) | `<business name on Paystack>/<customer's name>` | Not documented |
| Flutterwave | Not documented (`narration`, "the name should be shown") | Not documented | Not documented |
| Moniepoint | **Yes** (`accountName`) | The name given | Not documented |

So the plan's naming (§6.3: the school's name shortens, the child's never does) applies as written on Moniepoint only. On Paystack the school's name is its Paystack business name and the child's is the customer name, which Taskiem sends; on iswallet and Flutterwave the family page must show the name parents will see once the rails confirm it.

## What B0 changes for later milestones

- **B1 and B4** read `credit_id`, `account_number`, `payer_name` and `payer_account` from the event. The money log records the fee and the payer's account where given, but keeps no column for the account number credited; matching on account number and reference together (§6.4) needs it stored (B4).
- **B4's look-back** runs `list_credits` on iswallet, Paystack and Flutterwave. On iswallet it is the **only** way credits reach the log until the webhook's account field is known.
- **The issuing connection's requirements** differ: iswallet needs the wallet (`account`) and the director's BVN; Paystack an email per student, and a business in an allowed category; Flutterwave a BVN or NIN per account.

## Questions for the founder

| Rail | Question |
| --- | --- |
| iswallet | Which fields of `wallet.credit.posted` carry the virtual account's `reference` and `account_number` and the `provider_reference` (is `source_ref` the provider reference?), and the payer's name and account? Until answered, credits reach the log only through the look-back |
| iswallet | Does the batch's `account_name` change what payers see, and what is its length limit? |
| iswallet | Does iswallet charge its own fee (`fee`) on deposits, besides the provider's `fee_charged`? Which `type`, `direction` and `status` values does a deposit have, and can a listed deposit be pending? |
| iswallet | Is there a limit on numbers per wallet, or a fee per number? Is the request body of the single-issue endpoint (`is_permanent`, `client_reference`) to be preferred over the batch? |
| Paystack | Is the school's category one that must validate customers (BVN or bank account per student)? |
| Paystack | One account per email: may Taskiem give each student a derived email (for example at the school's domain), or must each be a real address? Does Paystack email the customer? |
| Paystack | Is `fees` in kobo, and is it deducted from what the business is settled (net) or charged separately? |
| Paystack | What does `POST /dedicated_account` answer for a customer who already has an account, and what does `GET /customer/:email` answer for an unknown email (404)? (Sandbox) |
| Paystack | The account name's length limit; whether custom naming ("school / student") is available to a school |
| Flutterwave | May one parent's BVN or NIN hold several static accounts (siblings), or one per student? |
| Flutterwave | In `charge.completed` for a static account, which of `amount` and `charged_amount` is what the payer sent, is `app_fee` deducted from it, and what is `amount_settled`? Is `amount` in naira? |
| Flutterwave | Does `narration` set the account name payers see, and with what limit? Is there a lookup of one static account by `tx_ref` or `order_ref`? |
| Flutterwave | Does a static account opened without `amount` take any amount, any number of times? |
| Flutterwave | Is v3 still the API for new accounts, or should static accounts be built on v4? |
| Moniepoint | Does Monnify prefix the merchant's name to `accountName`, and what is its length limit? |
