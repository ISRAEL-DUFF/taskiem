import { cel, connectorEvent, http, transform, workflow } from "@taskiem/sdk";

export default workflow({
  id: "wf_ispendCreditExceptions",
  name: "iSpend: handle rejected and reversed wallet credits",
  description: "When iswallet refuses a customer's top-up (refunding the sender) or claws back a credit it had already posted, tells iSpend so it can update the customer's wallet view and receipt, and alerts ops.",
  trigger: connectorEvent({
    connector: "iswallet@1",
    trigger: "credit_event",
    events: ["wallet.credit.rejected", "wallet.credit.reversed"],
  }),
  settings: { timeout: "24h" },
})
  .step("describe", transform({
    kind: ({ trigger }) => trigger.event === "wallet.credit.reversed" ? "reversed" : "rejected",
    wallet_id: ({ trigger }) => trigger.body.wallet_id,
    amount_kobo: ({ trigger }) => cel.has(trigger.body.data.amount) ? trigger.body.data.amount : 0,
    text: "='iSpend top-up ' + (trigger.event == 'wallet.credit.reversed' ? 'REVERSED after it was credited' : 'rejected and refunded to the sender') + ': wallet ' + trigger.body.wallet_id + (has(trigger.body.data.amount) ? ', ' + string(double(trigger.body.data.amount) / 100.0) + ' NGN' : '') + ', event ' + trigger.body.id",
  }))
  .next("notify_ispend", http({
    method: "POST",
    url: ({ env }) => env.ispend_api_url + "/internal/wallet-credit-exceptions",
    headers: { Authorization: ({ secrets }) => "Bearer " + secrets.ispend_api_token },
    body: {
      event_id: ({ trigger }) => trigger.body.id,
      kind: ({ steps }) => steps.describe.output.kind,
      wallet_id: ({ steps }) => steps.describe.output.wallet_id,
      amount_kobo: ({ steps }) => steps.describe.output.amount_kobo,
      occurred_at: ({ trigger }) => trigger.body.occurred_at,
      iswallet: ({ trigger }) => trigger.body.data,
    },
    class: "idempotent_write",
    idempotency_header: "Idempotency-Key",
  }), { retry: { max: 10, backoff: "exponential", initial: "5s", max_delay: "10m" } })
  .step("post_slack", http({
    method: "POST",
    url: ({ secrets }) => secrets.ops_slack_webhook_url,
    body: { text: ({ steps }) => steps.describe.output.text },
    class: "unsafe_write",
  }), { needs: ["describe"] });
