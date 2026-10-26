// What can start a workflow, in plain words (docs/workflow-builder.md):
// the cards on "When should it run?" and the choices under Settings → When
// it runs. Each makes the trigger with settings that work as they are;
// Settings changes them later.
import type { WorkflowDefinition } from "@sdk/wd";
import { startFormTrigger } from "../canvas/FormEditor";

export interface TriggerChoice {
  type: WorkflowDefinition["trigger"]["type"];
  title: string;
  sub: string;
  icon: string;
  /** The definition with this trigger, its other parts kept. */
  apply: (def: WorkflowDefinition) => WorkflowDefinition;
}

const plain =
  (type: WorkflowDefinition["trigger"]["type"], config?: Record<string, unknown>) =>
  (def: WorkflowDefinition): WorkflowDefinition => ({ ...def, trigger: config ? { type, config } : { type } });

export const TRIGGER_CHOICES: TriggerChoice[] = [
  { type: "schedule", title: "On a schedule", sub: "Every morning, every Monday, on the 1st of the month", icon: "clock", apply: plain("schedule", { cron: "0 9 * * 1-5", timezone: "Africa/Lagos" }) },
  { type: "capability_event", title: "A payment comes in", sub: "Money lands in your account, whichever bank or provider", icon: "card", apply: plain("capability_event", { events: ["payment.received"] }) },
  { type: "storefront", title: "An order comes in", sub: "Someone orders on your storefront", icon: "orders", apply: plain("storefront", { action: "order" }) },
  { type: "form", title: "Someone fills in a form", sub: "A form you share by link or QR code", icon: "forms", apply: (def) => startFormTrigger(def) as WorkflowDefinition },
  { type: "whatsapp", title: "A WhatsApp message comes in", sub: "A customer writes to your WhatsApp number", icon: "chat", apply: plain("whatsapp") },
  { type: "email", title: "An email comes in", sub: "Mail to the workflow's own address", icon: "mail", apply: plain("email", {}) },
  { type: "manual", title: "When you start it", sub: "With a button here, or by asking the assistant", icon: "play", apply: plain("manual") },
  {
    type: "webhook",
    title: "Another app tells it",
    sub: "Your website or another service calls its web address",
    icon: "code",
    apply: (def) => plain("webhook", { path: "/" + def.id.replace(/^wf_/, ""), auth: "hmac" })(def),
  },
];

/** A new workflow with nothing in it yet but what starts it. */
export function blankWorkflow(id: WorkflowDefinition["id"], name: string, choice: TriggerChoice): WorkflowDefinition {
  return choice.apply({ schema: "wd/v1", id, version: 1, name, trigger: { type: "manual" }, steps: [] });
}
