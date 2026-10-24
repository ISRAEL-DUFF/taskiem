import { cel, schedule, template, workflow } from "@taskiem/sdk";
import { notify } from "@taskiem/connectors";

export default workflow({
  id: "wf_monthEndPay",
  name: "Month end: payroll, then suppliers",
  description: "Runs payroll with the Run payroll template, then pays the month's suppliers with the Pay a list template, then tells the owner how both went in one message.",
  trigger: schedule({ cron: "0 8 25 * *", timezone: "Africa/Lagos" }),
})
  .step("payroll", template("run-payroll", { business_name: "Ada Foods", approver_role: "owner" }), { name: "Run payroll" })
  .next("suppliers", template("pay-a-list", { list_table: "suppliers", approver_role: "owner" }, { batch: ({ steps }) => "suppliers-" + steps.payroll.output.period }), { name: "Pay the month's suppliers" })
  .next("tell_owner", notify.tell({
    to: "owner",
    title: ({ steps }) => "Month end " + steps.payroll.output.period,
    message: ({ steps }) => "Payroll: " + cel.string(steps.payroll.output.paid) + " staff paid (NGN " + cel.string(steps.payroll.output.paid_naira) + "). Suppliers: " + cel.string(steps.suppliers.output.paid) + " paid (NGN " + cel.string(steps.suppliers.output.paid_naira) + ").",
  }), { name: "Tell the owner how the month went" });
