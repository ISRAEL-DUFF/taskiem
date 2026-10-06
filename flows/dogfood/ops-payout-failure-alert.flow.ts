import { connectorEvent, http, transform, workflow } from "@taskiem/sdk";
import { termii } from "@taskiem/connectors";

export default workflow({
  id: "wf_opsPayoutFailureAlert",
  name: "Ops: alert on failed or reversed iswallet payouts",
  description: "When iswallet reports a bank payout as failed, or reverses one it had confirmed, alerts the on-call engineer by SMS and posts to the ops Slack channel.",
  trigger: connectorEvent({
    connector: "iswallet@1",
    trigger: "outflow_event",
    events: ["wallet.outflow.failed", "wallet.outflow.reversed"],
  }),
  settings: { timeout: "1h" },
})
  .step("message", transform({
    text: "='iswallet payout ' + (trigger.event == 'wallet.outflow.reversed' ? 'REVERSED after it was confirmed' : 'failed') + ': ' + string(double(trigger.body.data.amount) / 100.0) + ' NGN, outflow ' + trigger.body.data.outflow_id + ', wallet ' + trigger.body.data.wallet_id + (has(trigger.body.data.failure_reason) ? ' (' + trigger.body.data.failure_reason + ')' : '')",
  }))
  .next("sms_on_call", termii.send_sms({
    to: ({ env }) => env.ops_on_call_phone,
    sms: ({ steps }) => steps.message.output.text,
    channel: "dnd",
  }))
  .step("post_slack", http({
    method: "POST",
    url: ({ secrets }) => secrets.ops_slack_webhook_url,
    body: { text: ({ steps }) => steps.message.output.text },
    class: "unsafe_write",
  }), { needs: ["message"] });
