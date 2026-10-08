# Flutterwave integration

The connector is `connectors/flutterwave` (`flutterwave@1`), built on Flutterwave's API v3 from its public documentation at developer.flutterwave.com (read 6 October 2026). v3 is Flutterwave's stable version and is not deprecated; v4 is in public beta, and an account uses one version or the other. A v4 connector can follow when v4 is generally available.

## Connection

| Field | |
| --- | --- |
| `secret_key` | `FLWSECK_TEST-…` (test) or `FLWSECK-…` (live); the key selects the mode |
| `webhook_hash` | The secret hash set under Settings → Webhooks |

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_balance` | `GET /balances/{currency}` | read | |
| `list_banks` | `GET /banks/{country}` | read | Also the connection test |
| `resolve_account` | `POST /accounts/resolve` | read | Run before paying a new account |
| `get_transfer_fee` | `GET /transfers/fee` | read | |
| `transfer` | `POST /transfers` | idempotent write | NGN payout to a bank account; returns `NEW` |
| `get_transfer` | `GET /transfers/{id}` | read | |
| `get_transfer_by_reference` | `GET /transfers?reference=` | read | |
| `verify_payment` | `GET /transactions/{id}/verify`, `/transactions/verify_by_reference` | read | Confirm a collection before giving value |

**Amounts.** Taskiem amounts are minor units; Flutterwave v3 uses major units (naira). Its payout `amount` is an integer, so payouts must be whole naira: the connector refuses an amount that is not a multiple of 100 kobo rather than round someone's pay. Fees, which Flutterwave quotes to fractions of a kobo (₦26.875), round up.

**Never twice.** The engine's key is the payout `reference`. Flutterwave refuses a repeated reference ("Payout with this ref already exists"); the connector then finds the payout by reference and reports it. Flutterwave answers a request that timed out on its side with 503 after about 28 seconds, so a 503 on a payout is treated as an unknown outcome (resent with the same reference, which is safe), not as a refusal. A `FAILED` payout fails the step with Flutterwave's `complete_message`. If the account requires payout approval, the output shows `requires_approval` until someone approves it in the dashboard.

## Webhooks

A Flutterwave account has one webhook URL for every event, so there is one trigger, `event`. Set that URL from the workflow's Endpoints tab (`/hooks/{tenant}/connectors/flutterwave@1/event?env=prod&connection=<name>`) and a secret hash in the Flutterwave dashboard, and put the hash in the connection. v3 sends the hash itself in `verif-hash`; Taskiem compares it in constant time. v3 events have no id, so repeats are dropped on event, transaction id and status. Turn on webhook retries in the dashboard (Flutterwave retries three times, 30 minutes apart).

| Events | Correlation (signal steps wait for `flutterwave@1:event`) |
| --- | --- |
| `transfer.completed` (successful or failed: see `data.status`) | the payout's `reference` |
| `charge.completed` | your `tx_ref` |

Verify a charge with `verify_payment` before giving value, as Flutterwave advises.

## To confirm in the sandbox before go-live

1. That NGN payouts are in naira (every v3 example is in major units, but the field description does not say).
2. How long a reference stays unique.
3. Sandbox payouts stay `PENDING` unless the reference ends `_PMCK`; the engine's references do not, so test settlement with `event` deliveries or Flutterwave's dashboard.
