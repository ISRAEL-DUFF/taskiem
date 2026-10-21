import type { WorkflowDefinition } from "@sdk/wd";
import { workflowID } from "./aitables";

/** What a new form does with each answer (no-technical-steps N8). */
export type AfterAnswer = "tell" | "collect";

/**
 * The workflow behind a new form: a form trigger asking for a name and a
 * phone number, and, for "tell", a step that tells the owner of each
 * answer on WhatsApp or by email (Tell me).
 */
export function formWorkflow(title: string, after: AfterAnswer): WorkflowDefinition {
  const name = title.trim();
  const def: WorkflowDefinition = {
    schema: "wd/v1",
    id: workflowID(name),
    version: 1,
    name,
    trigger: { type: "form", config: { title: name } },
    inputs: {
      schema: {
        type: "object",
        required: ["full_name"],
        properties: {
          full_name: { type: "string", title: "Full name", "x-pii": "name" },
          phone: { type: "string", title: "Phone number", "x-pii": "phone" },
        },
      },
    },
    steps:
      after === "tell"
        ? [
            {
              id: "tell_me",
              type: "connector",
              name: "Tell me about the answer",
              connector: "notify@1",
              action: "tell",
              input: { title: `New answer to ${name}`, message: "=trigger.body.full_name + ' filled in the form.'" },
            },
          ]
        : [{ id: "keep", type: "transform", name: "Keep the answer", config: { output: { received: true } } }],
  } as WorkflowDefinition;
  return def;
}
