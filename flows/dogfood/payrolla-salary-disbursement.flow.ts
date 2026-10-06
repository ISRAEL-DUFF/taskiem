import { approval, cel, foreach, http, signal, steps, transform, webhook, workflow } from "@taskiem/sdk";
import { iswallet } from "@taskiem/connectors";

export default workflow({
  id: "wf_payrollaSalaryDisbursement",
  name: "Payrolla: disburse approved payroll",
  description: "Pays every employee in an approved Payrolla payroll from the company's iswallet wallet, after maker-checker approval: by wallet transfer to employees with an iswallet wallet, by bank payout otherwise. A refused payment does not stop the others; every outcome, paid or failed, is reported to Payrolla, and so is a rejected or expired approval.",
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
  settings: { timeout: "96h", concurrency_key: ({ trigger }) => trigger.body.payroll_id },
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
    .step("employee", transform({ employee_id: ({ item }) => item.employee_id }))
    .step("to_wallet", iswallet.transfer({
      from_wallet_id: ({ trigger }) => trigger.body.funding_wallet_id,
      to_wallet_id: ({ item }) => item.wallet_id,
      amount: ({ item }) => item.net_pay_kobo,
      narration: ({ trigger, item }) => "Salary " + trigger.body.period + " " + trigger.body.payroll_id + " " + item.employee_id,
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
      narration: ({ trigger }) => "Salary " + trigger.body.period,
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
      results: ({ steps }) => steps.pay_all.output,
    },
    class: "idempotent_write",
    idempotency_header: "Idempotency-Key",
  }), {
    when: ({ steps }) => steps.approve.output.decision === "approved",
    retry: { max: 5, backoff: "exponential", initial: "2s" },
  })
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
      results: [],
    },
    class: "idempotent_write",
    idempotency_header: "Idempotency-Key",
  }), {
    needs: ["approve"],
    when: ({ steps }) => steps.approve.output.decision !== "approved",
    retry: { max: 5, backoff: "exponential", initial: "2s" },
  });
