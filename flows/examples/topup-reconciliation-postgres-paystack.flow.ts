import { branch, cel, foreach, http, schedule, steps, workflow } from "@taskiem/sdk";
import { paystack, postgres } from "@taskiem/connectors";

export default workflow({
  id: "wf_ispendTopupReconciliation",
  name: "iSpend: reconcile stuck wallet top-ups",
  description: "Every 15 minutes, checks top-ups that have been pending for more than 15 minutes against Paystack and credits or fails them in iSpend.",
  trigger: schedule({ cron: "*/15 * * * *", timezone: "Africa/Lagos" }),
  settings: { timeout: "14m", concurrency_key: "='ispend-topup-reconciliation'" },
})
  .step("find_pending", postgres.query({
    sql: "SELECT id, paystack_reference, amount_kobo, wallet_id FROM topups WHERE status = 'pending' AND created_at < now() - interval '15 minutes' ORDER BY created_at LIMIT 500",
    params: [],
  }, { connection: "ispend_readonly" }))
  .next("reconcile_each", foreach({ items: ({ steps }) => steps.find_pending.output.rows, max_concurrency: 10, max_items: 500 }, steps()
    .step("verify", paystack.verify_charge({ reference: ({ item }) => item.paystack_reference }), { retry: { max: 3, backoff: "exponential", initial: "2s" } })
    .next("decide", branch({
      paths: [
        {
          name: "paid",
          when: ({ steps, item }) => steps.verify.output.status === "success" && steps.verify.output.amount === item.amount_kobo,
          steps: steps()
          .step("credit_wallet", http({
            method: "POST",
            url: ({ env, item }) => env.ispend_api_url + "/internal/topups/" + item.id + "/complete",
            headers: { Authorization: ({ secrets }) => "Bearer " + secrets.ispend_api_token },
            body: {
              paystack_reference: ({ item }) => item.paystack_reference,
              amount_kobo: ({ item }) => item.amount_kobo,
            },
            class: "idempotent_write",
            idempotency_header: "Idempotency-Key",
          }, { effect: { idempotency_seed: ({ item }) => item.paystack_reference } }), { retry: { max: 5, backoff: "exponential", initial: "2s" } }),
        },
        {
          name: "amount_mismatch",
          when: ({ steps, item }) => steps.verify.output.status === "success" && steps.verify.output.amount !== item.amount_kobo,
          steps: steps()
          .step("flag_mismatch", http({
            method: "POST",
            url: ({ env, item }) => env.ispend_api_url + "/internal/topups/" + item.id + "/flag",
            headers: { Authorization: ({ secrets }) => "Bearer " + secrets.ispend_api_token },
            body: { reason: "amount_mismatch", paid_kobo: ({ steps }) => steps.verify.output.amount },
            class: "idempotent_write",
            idempotency_header: "Idempotency-Key",
          }, { effect: { idempotency_seed: ({ item }) => item.paystack_reference } })),
        },
        {
          name: "failed",
          when: ({ steps }) => cel.in(steps.verify.output.status, ["failed", "abandoned", "reversed"]),
          steps: steps()
          .step("fail_topup", http({
            method: "POST",
            url: ({ env, item }) => env.ispend_api_url + "/internal/topups/" + item.id + "/fail",
            headers: { Authorization: ({ secrets }) => "Bearer " + secrets.ispend_api_token },
            body: { paystack_status: ({ steps }) => steps.verify.output.status },
            class: "idempotent_write",
            idempotency_header: "Idempotency-Key",
          }, { effect: { idempotency_seed: ({ item }) => item.paystack_reference } })),
        },
      ],
    }))));
