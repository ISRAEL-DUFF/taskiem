import { approval, cel, foreach, http, parallel, signal, steps, transform, wait, webhook, workflow } from "@taskiem/sdk";
import { iswallet } from "@taskiem/connectors";

export default workflow({
  id: "wf_payrollaSalaryDisbursement",
  name: "Payrolla: disburse approved payroll",
  description: "Pays every employee in an approved Payrolla payroll from the company's iswallet wallet, after maker-checker approval: by wallet transfer to employees with an iswallet wallet, by bank payout otherwise. A refused payment does not stop the others; every outcome, paid or failed, is reported to Payrolla with the payroll's totals, and so is a rejected or expired approval. After the report, the run watches each confirmed bank payout for a reversal (35 days from the payroll's arrival by default) and sends Payrolla a reversal notice for each one; nothing more is paid.",
  trigger: webhook({
    path: "/payrolla/payroll-approved",
    auth: "hmac",
    dedup: ({ trigger }) => trigger.body.payroll_id,
  }),
  inputs: { schema: { $ref: "#/types/PayrollApproved" } },
  types: {
    PayrollApproved: {
      type: "object",
      required: ["payroll_id", "period", "funding_wallet_id", "total_kobo", "employees"],
      properties: {
        payroll_id: { type: "string" },
        period: { type: "string" },
        funding_wallet_id: { type: "string", description: "Payrolla's company wallet on iswallet" },
        total_kobo: { type: "integer", minimum: 0 },
        employees: {
          type: "array",
          items: {
            type: "object",
            required: ["employee_id", "net_pay_kobo"],
            properties: {
              employee_id: { type: "string" },
              net_pay_kobo: { type: "integer", minimum: 100 },
              wallet_id: {
                type: "string",
                description: "The employee's iswallet wallet under the Payrolla client; omit to pay to a bank account",
              },
              bank_code: { type: "string" },
              account_number: { type: "string", "x-pii": "account_number" },
              account_name: { type: "string", "x-pii": "name" },
            },
            oneOf: [
              { required: ["wallet_id"] },
              { required: ["bank_code", "account_number", "account_name"] },
            ],
          },
        },
      },
    },
  },
  settings: { timeout: "70d", concurrency_key: ({ trigger }) => trigger.body.payroll_id },
})
  .step("summarise", transform({
    payroll_id: ({ trigger }) => trigger.body.payroll_id,
    employees: ({ trigger }) => cel.size(trigger.body.employees),
    to_bank: ({ trigger }) => cel.size(trigger.body.employees.filter((e) => !cel.has(e.wallet_id))),
    total_kobo: ({ trigger }) => trigger.body.total_kobo,
  }))
  .step("check_balance", iswallet.get_balance({ wallet_id: ({ trigger }) => trigger.body.funding_wallet_id }), { retry: { max: 3, backoff: "exponential", initial: "2s" } })
  .step("approve", approval({
    role: "payroll_approver",
    timeout: "24h",
    on_timeout: "reject",
    subject: {
      payroll_id: ({ steps }) => steps.summarise.output.payroll_id,
      period: ({ trigger }) => trigger.body.period,
      employees: ({ steps }) => steps.summarise.output.employees,
      paid_to_bank: ({ steps }) => steps.summarise.output.to_bank,
      total_kobo: ({ steps }) => steps.summarise.output.total_kobo,
      available_kobo: ({ steps }) => steps.check_balance.output.balances.filter((b) => b.currency === "NGN").map((b) => b.available)[0],
    },
  }), { needs: ["summarise", "check_balance"] })
  .next("pay_all", foreach({ items: ({ trigger }) => trigger.body.employees, max_concurrency: 5, max_items: 5000 }, steps()
    .step("employee", transform({ employee_id: ({ item }) => item.employee_id, net_pay_kobo: ({ item }) => item.net_pay_kobo }))
    .step("to_wallet", iswallet.transfer({
      from_wallet_id: ({ trigger }) => trigger.body.funding_wallet_id,
      to_wallet_id: ({ item }) => item.wallet_id,
      amount: ({ item }) => item.net_pay_kobo,
      narration: "=('Salary ' + trigger.body.period + ' ' + trigger.body.payroll_id + ' ' + item.employee_id).substring(0, math.least(255, size('Salary ' + trigger.body.period + ' ' + trigger.body.payroll_id + ' ' + item.employee_id)))",
    }, {
      effect: { idempotency_seed: ({ trigger, item }) => trigger.body.payroll_id + ":" + item.employee_id },
    }), {
      when: ({ item }) => cel.has(item.wallet_id),
      retry: { max: 8, backoff: "exponential", initial: "5s", max_delay: "10m", max_duration: "6h" },
      on_error: steps()
        .step("wallet_failed", transform({
          employee_id: ({ item }) => item.employee_id,
          rail: "wallet",
          error: ({ steps }) => steps.to_wallet.error,
        })),
    })
    .step("to_bank", iswallet.payout({
      wallet_id: ({ trigger }) => trigger.body.funding_wallet_id,
      amount: ({ item }) => item.net_pay_kobo,
      bank_code: ({ item }) => item.bank_code,
      account_number: ({ item }) => item.account_number,
      account_name: ({ item }) => item.account_name,
      narration: "=('Salary ' + trigger.body.period + ' ' + trigger.body.payroll_id + ' ' + item.employee_id).substring(0, math.least(255, size('Salary ' + trigger.body.period + ' ' + trigger.body.payroll_id + ' ' + item.employee_id)))",
      metadata: {
        payroll_id: ({ trigger }) => trigger.body.payroll_id,
        employee_id: ({ item }) => item.employee_id,
      },
    }, {
      effect: { idempotency_seed: ({ trigger, item }) => trigger.body.payroll_id + ":" + item.employee_id },
    }), {
      when: ({ item }) => !cel.has(item.wallet_id),
      retry: { max: 8, backoff: "exponential", initial: "5s", max_delay: "10m", max_duration: "6h" },
      on_error: steps()
        .step("bank_failed", transform({
          employee_id: ({ item }) => item.employee_id,
          rail: "bank",
          error: ({ steps }) => steps.to_bank.error,
        })),
    })
    .next("settle", signal({
      event: "iswallet@1:outflow_event",
      correlation: ({ steps }) => steps.to_bank.output.outflow_id,
      timeout: "72h",
    }), {
      when: ({ steps }) => steps.to_bank.output.status === "pending",
      on_error: steps()
        .step("bank_unconfirmed", transform({
          employee_id: ({ item }) => item.employee_id,
          rail: "bank",
          error: ({ steps }) => steps.settle.error,
        })),
    })), { when: ({ steps }) => steps.approve.output.decision === "approved" })
  .next("summary", transform({
    paid: "=size(steps.pay_all.output.filter(r, has(r.to_wallet) || (has(r.settle) && r.settle.event == 'wallet.outflow.confirmed')))",
    failed: "=size(steps.pay_all.output.filter(r, has(r.wallet_failed) || has(r.bank_failed) || has(r.bank_unconfirmed) || (has(r.settle) && r.settle.event != 'wallet.outflow.confirmed')))",
    totals: {
      paid_kobo: "=sum(steps.pay_all.output.filter(r, has(r.to_wallet) || (has(r.settle) && r.settle.event == 'wallet.outflow.confirmed')).map(r, r.employee.net_pay_kobo))",
      bank_fees_kobo: "=sum(steps.pay_all.output.filter(r, has(r.settle) && r.settle.event == 'wallet.outflow.confirmed' && has(r.to_bank.fee)).map(r, r.to_bank.fee))",
      paid_to_wallet: ({ steps }) => cel.size(steps.pay_all.output.filter((r) => cel.has(r.to_wallet))),
      paid_to_bank: ({ steps }) => cel.size(steps.pay_all.output.filter((r) => cel.has(r.settle) && r.settle.event === "wallet.outflow.confirmed")),
      refused: ({ steps }) => cel.size(steps.pay_all.output.filter((r) => cel.has(r.wallet_failed) || cel.has(r.bank_failed))),
      failed_at_bank: ({ steps }) => cel.size(steps.pay_all.output.filter((r) => cel.has(r.settle) && r.settle.event !== "wallet.outflow.confirmed")),
      unconfirmed: ({ steps }) => cel.size(steps.pay_all.output.filter((r) => cel.has(r.bank_unconfirmed))),
    },
  }))
  .next("report", http({
    method: "POST",
    url: ({ trigger, env }) => env.payrolla_api_url + "/payrolls/" + trigger.body.payroll_id + "/disbursement-report",
    headers: { Authorization: ({ secrets }) => "Bearer " + secrets.payrolla_api_token },
    body: {
      payroll_id: ({ trigger }) => trigger.body.payroll_id,
      status: "completed",
      paid: ({ steps }) => steps.summary.output.paid,
      failed: ({ steps }) => steps.summary.output.failed,
      totals: ({ steps }) => steps.summary.output.totals,
      results: ({ steps }) => steps.pay_all.output,
    },
    class: "idempotent_write",
    idempotency_header: "Idempotency-Key",
  }), {
    when: ({ steps }) => steps.approve.output.decision === "approved",
    retry: { max: 5, backoff: "exponential", initial: "2s" },
  })
  .next("watching_for_reversals", transform({
    note: "Report sent; nothing more is paid. Each confirmed bank payout is watched for a reversal until watch_until, then the run ends.",
    watch_until: "=string(timestamp(run.started_at) + duration(string(24 * math.least(60, math.greatest(1, has(env.payrolla_reversal_watch_days) && string(env.payrolla_reversal_watch_days).matches('^[0-9]{1,3}$') ? int(string(env.payrolla_reversal_watch_days)) : 35))) + 'h'))",
    payouts: ({ steps }) => cel.size(steps.pay_all.output.filter((r) => cel.has(r.settle) && r.settle.event === "wallet.outflow.confirmed")),
    outflow_ids: ({ steps }) => steps.pay_all.output.filter((r) => cel.has(r.settle) && r.settle.event === "wallet.outflow.confirmed").map((r) => r.to_bank.outflow_id),
  }), {
    name: "Watching for reversals (report sent)",
    description: "The report has been sent and nothing more is paid. Each confirmed bank payout is watched for a reversal until watch_until: by default 35 days after the payroll arrived, so at least 30 days after the last confirmation (approval 24h, payments 6h, settlement 72h). The variable payrolla_reversal_watch_days changes it (1 to 60). The watch only waits and posts reversal notices to Payrolla; it never calls iswallet.",
  })
  .next("watch", foreach({
    items: ({ steps }) => [0, 1, 2, 3, 4].filter((l) => l * 1000 < steps.watching_for_reversals.output.payouts),
    max_concurrency: 5,
    max_items: 5,
  }, steps()
    .step("watch_lane", foreach({
      items: "=steps.pay_all.output.filter(r, has(r.settle) && r.settle.event == 'wallet.outflow.confirmed').slice(item * 1000, math.least((item + 1) * 1000, steps.watching_for_reversals.output.payouts))",
      max_concurrency: 1000,
      max_items: 1000,
    }, steps()
      .step("watch_payout", parallel({
        branches: {
          reversed: steps()
          .step("reversal_event", signal({ event: "iswallet@1:outflow_event", correlation: ({ item }) => item.to_bank.outflow_id })),
          deadline: steps()
          .step("watch_ends", wait({ until: ({ steps }) => steps.watching_for_reversals.output.watch_until })),
        },
        join: "any",
      }), {
        description: "A reversal of this payout races the end of the watch. The notice comes after the race, so the deadline can never cancel a notice being sent.",
      })
      .next("reversal", transform({
        payroll_id: ({ trigger }) => trigger.body.payroll_id,
        employee_id: ({ item }) => item.employee.employee_id,
        outflow_id: ({ item }) => item.to_bank.outflow_id,
        amount_kobo: ({ steps, item }) => cel.has(steps.watch_payout.output.reversed.reversal_event.body.data.amount) ? steps.watch_payout.output.reversed.reversal_event.body.data.amount : item.employee.net_pay_kobo,
        fee_kobo: ({ steps, item }) => cel.has(steps.watch_payout.output.reversed.reversal_event.body.data.fee) ? steps.watch_payout.output.reversed.reversal_event.body.data.fee : cel.has(item.to_bank.fee) ? item.to_bank.fee : 0,
        reversed_at: ({ steps }) => cel.has(steps.watch_payout.output.reversed.reversal_event.body.data.reversed_at) ? steps.watch_payout.output.reversed.reversal_event.body.data.reversed_at : cel.has(steps.watch_payout.output.reversed.reversal_event.body.occurred_at) ? steps.watch_payout.output.reversed.reversal_event.body.occurred_at : null,
        event_id: ({ steps }) => cel.has(steps.watch_payout.output.reversed.reversal_event.body.id) ? steps.watch_payout.output.reversed.reversal_event.body.id : null,
        iswallet: ({ steps }) => steps.watch_payout.output.reversed.reversal_event.body.data,
      }), {
        when: ({ steps, item }) => cel.has(steps.watch_payout.output.reversed) && steps.watch_payout.output.reversed.reversal_event.event === "wallet.outflow.reversed" && steps.watch_payout.output.reversed.reversal_event.body.data.outflow_id === item.to_bank.outflow_id,
      })
      .next("reversal_notice", http({
        method: "POST",
        url: ({ trigger, env }) => env.payrolla_api_url + "/payrolls/" + trigger.body.payroll_id + "/reversal-notice",
        headers: { Authorization: ({ secrets }) => "Bearer " + secrets.payrolla_api_token },
        body: ({ steps }) => steps.reversal.output,
        class: "idempotent_write",
        idempotency_header: "Idempotency-Key",
      }), {
        retry: { max: 10, backoff: "exponential", initial: "30s", max_delay: "1h" },
        on_error: steps()
          .step("notice_failed", transform({
            employee_id: ({ item }) => item.employee.employee_id,
            outflow_id: ({ item }) => item.to_bank.outflow_id,
            error: ({ steps }) => steps.reversal_notice.error,
          })),
      })))))
  .step("report_rejected", http({
    method: "POST",
    url: ({ trigger, env }) => env.payrolla_api_url + "/payrolls/" + trigger.body.payroll_id + "/disbursement-report",
    headers: { Authorization: ({ secrets }) => "Bearer " + secrets.payrolla_api_token },
    body: {
      payroll_id: ({ trigger }) => trigger.body.payroll_id,
      status: "rejected",
      reason: ({ steps }) => cel.has(steps.approve.output.reason) && steps.approve.output.reason === "timeout" ? "approval_timed_out" : "rejected_by_approver",
      paid: 0,
      failed: 0,
      totals: {
        paid_kobo: 0,
        bank_fees_kobo: 0,
        paid_to_wallet: 0,
        paid_to_bank: 0,
        refused: 0,
        failed_at_bank: 0,
        unconfirmed: 0,
      },
      results: [],
    },
    class: "idempotent_write",
    idempotency_header: "Idempotency-Key",
  }), {
    needs: ["approve"],
    when: ({ steps }) => steps.approve.output.decision !== "approved",
    retry: { max: 5, backoff: "exponential", initial: "2s" },
  });
