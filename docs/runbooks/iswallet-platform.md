# Taskiem's own iswallet client

The runbook for the iswallet client Taskiem holds for businesses that receive money with Taskiem ([decision 0052](../decisions/0052-taskiem-as-a-collection-rail.md); [plan](../spec/iswallet-account-numbers.md), workstream W). For the team running staging and production.

## What it is

- One iswallet client per mode, provisioned by iswallet for iSpend: **live** (real money) and **test** (iswallet's sandbox). Every business's wallet sits under it; a business never sees the key.
- A business that turns on **Settings > Payments** gets a wallet under the client and a connection `iswallet/taskiem` (`connections.platform = 'taskiem'`, `provisioned_by = 'taskiem:receive_money'`). The connection stores only its wallet's id; the vault hands its connector the platform key for the connection's mode, marked as a shared client, so the connector refuses any wallet but that one (`WALLET_NOT_ALLOWED`, decision 0031). Nobody can replace its credentials or delete it through the API.
- The mode a new wallet is made in: live when the live key is set, else test. A staging Taskiem with only the test key makes test wallets for its "prod" businesses.
- One webhook subscription per mode carries every business's wallet events to `TASKIEM_HOOKS_URL/platform/iswallet@1?mode=live` (or `test`). The edge verifies it with the mode's secret and routes each event by its `wallet_id` to the one business whose Taskiem connection holds that wallet in that mode (`taskiem_platform_route_event`), as if it came to that business's own URL: its money log, its subscribed workflows, its limits.

## Configuration

Set on every process (`api`, `worker`, `scheduler`, `edge`, `orchestrator`, or `all`), from the secrets store:

| Variable | What |
| --- | --- |
| `TASKIEM_ISWALLET_CLIENT_KEY` | The live client's API key |
| `TASKIEM_ISWALLET_WEBHOOK_SECRET` | The live subscription's signing secret (`taskiem iswallet subscribe`) |
| `TASKIEM_ISWALLET_TEST_CLIENT_KEY` | The test client's API key |
| `TASKIEM_ISWALLET_TEST_WEBHOOK_SECRET` | The test subscription's signing secret |
| `TASKIEM_HOOKS_URL` | The public https address of `/hooks` (iswallet refuses private and loopback addresses) |

Without a mode's key, receiving money with Taskiem is not offered in that mode, and the Payments page says it is not available on this Taskiem.

## Provisioning a mode

1. Put the client's key in the secrets store as `TASKIEM_ISWALLET_CLIENT_KEY` (or `_TEST_CLIENT_KEY`) and roll the processes.
2. From any process with the same configuration, make the subscription:

   ```
   taskiem iswallet subscribe -mode live
   ```

   It subscribes every event the routed triggers send (`wallet.credit.*`, `wallet.outflow.*`, `wallet.transfer.*`) to the platform URL and prints the subscription's id and its signing secret. **The secret is shown once.** Store it as `TASKIEM_ISWALLET_WEBHOOK_SECRET` (or `_TEST_WEBHOOK_SECRET`) and roll the processes. Until then, deliveries to the platform URL answer 404 and iswallet retries them (10 times, then dead-letters).
3. Check it: `taskiem iswallet status -mode live sub_…` says whether the subscription is active and, when iswallet's last delivery got anything but a 2xx from us, what we answered.

## Rotating the key or the secret

- **The key**: ask iswallet for a new key, set it, roll the processes, then ask iswallet to revoke the old one. Connections hold no key, so nothing else changes.
- **The secret**: iswallet cannot rotate a subscription's secret. Make a new subscription (`subscribe`), set its secret, roll the processes, then remove the old one (`taskiem iswallet unsubscribe -mode live sub_old`). Between setting the new secret and removing the old subscription, deliveries signed by the old one are refused (401) and iswallet retries them; the new subscription delivers the same events, which are deduplicated.

## Events for a wallet no business holds

An event whose wallet no business's Taskiem connection holds in that mode is acknowledged (200), counted in `app_event_drops` (connector `iswallet@1`) and logged at error level ("a platform client's event names an account no business holds", with the wallet). Causes: a wallet made outside Taskiem under the client; a business whose connection was removed by hand; the wrong mode's subscription pointing here. Look up the wallet in `receive_money` and with iswallet. The event is not stored; for a business's own wallet, the look-back (every 15 minutes over 48 hours, `money_lookbacks`) reads its credits again, so a credit whose event went astray is still recorded once.

## Money and plans

- Billing's daily usage snapshot counts each business's credits and payouts on the rail (`usage_snapshots.rail_credits`, `rail_payouts` and their sums), from its money log.
- The plan feature `receive_money` (`deploy/plans.yaml`) gates turning it on. Which plans include it, and any margin over iswallet's fee, are the founder's to set; every plan has it for now.
- **Limits.** Every business shares the client's key and iswallet's limit on it (100 requests a minute). Taskiem's own calls for a business (its Payments page, chat answers) take at most 20 a minute; a business's own workflows that call iswallet are not limited beyond iswallet's answer (`RATE_LIMITED` is retried). Watch for 429s from iswallet in the logs.
- **Turning it on again later.** The wallet is keyed by the business and what it sent. iswallet keeps one wallet per `owner_ref` (`tenant:<id>`): a business whose first attempt went unanswered and who asks again more than a day later is answered by iswallet's rule for that owner; if iswallet refuses, find the wallet by its `owner_ref` with iswallet and record it by hand.
- A business closing its wallet: withdraw its balance first (Settings > Payments), then remove the `receive_money` row and the connection as an operator; iswallet closes the wallet on request.
