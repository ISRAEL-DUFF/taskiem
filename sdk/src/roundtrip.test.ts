import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import * as sdk from "./index.js";
import * as connectors from "./connectors.gen.js";
import { CONNECTOR_ACTIONS, CONNECTOR_HELPERS } from "./connectors.gen.js";
import type { WorkflowDefinition } from "./wd.js";

/** Runs generated flow code (plain JavaScript) and builds its workflow. */
export function evaluate(code: string): WorkflowDefinition {
  const body = code
    .replace(/^import \{([^}]*)\} from "@taskiem\/sdk";$/m, "const {$1} = sdk;")
    .replace(/^import \{([^}]*)\} from "@taskiem\/connectors";$/m, "const {$1} = connectors;")
    .replace("export default ", "return ");
  const wf = new Function("sdk", "connectors", body)(sdk, connectors) as sdk.Workflow;
  return wf.build();
}

function workflows(dir: string): string[] {
  const out: string[] = [];
  for (const f of readdirSync(dir)) {
    const p = join(dir, f);
    if (statSync(p).isDirectory()) out.push(...workflows(p));
    else if (f.endsWith(".wd.json")) out.push(p);
  }
  return out.sort();
}

describe("code round trip", () => {
  const files = workflows(join(import.meta.dirname, "../../flows"));
  it("finds the repository's workflows", () => expect(files.length).toBeGreaterThanOrEqual(4));
  for (const f of files) {
    it(`${f.split("/flows/")[1]} builds back to the same definition`, () => {
      const def = JSON.parse(readFileSync(f, "utf8")) as WorkflowDefinition;
      const code = sdk.generate(def, { connectors: CONNECTOR_HELPERS, actions: CONNECTOR_ACTIONS });
      expect(evaluate(code)).toEqual(def);
      // Generation is deterministic and stable under a second pass.
      expect(sdk.generate(evaluate(code), { connectors: CONNECTOR_HELPERS, actions: CONNECTOR_ACTIONS })).toBe(code);
    });
  }
});

describe("builder", () => {
  it("compiles the spec's example", () => {
    const { workflow, webhook, approval, http } = sdk;
    const def = workflow({ id: "wf_disburseApprovedLoan", name: "Disburse approved loan", trigger: webhook({ path: "/loans/approved", auth: "hmac" }) })
      .step("verify_bvn", connectors.dojah.lookup_bvn({ bvn: ({ trigger }) => trigger.body.bvn }))
      .next("approve", approval({ role: "credit_officer" }))
      .next("pay", connectors.iswallet.transfer({ from_wallet_id: "w_float", to_wallet_id: ({ trigger }) => trigger.body.wallet_id, amount: ({ trigger }) => trigger.body.amount_kobo }), {
        when: ({ steps }) => steps.approve.output.decision === "approved",
      })
      .step("notify", http({ method: "POST", url: "=env.hook", class: "unsafe_write", body: { id: ({ run }) => run.id } }), { needs: ["pay"] })
      .build();
    expect(def.steps.map((s) => [s.id, s.needs])).toEqual([["verify_bvn", undefined], ["approve", ["verify_bvn"]], ["pay", ["approve"]], ["notify", ["pay"]]]);
    expect(def.steps[2]).toMatchObject({ type: "connector", connector: "iswallet@1", action: "transfer", when: "=steps.approve.output.decision == 'approved'",
      input: { to_wallet_id: "=trigger.body.wallet_id", amount: "=trigger.body.amount_kobo", from_wallet_id: "w_float" } });
    expect(def.steps[3]).toMatchObject({ config: { url: "=env.hook", body: { id: "=run.id" } } });
  });

  it("names the field an expression error is in", () => {
    const wf = sdk.workflow({ id: "wf_x", name: "x", trigger: sdk.manual() }).step("a", sdk.transform({ v: ({ trigger }: sdk.Context) => trigger.a ?? 1 }));
    expect(() => wf.build()).toThrow(/steps\.a\.config\.output\.v: \?\? is not supported/);
  });

  it("nests steps in foreach, branch, parallel and on_error", () => {
    const { workflow, manual, foreach, branch, parallel, steps, transform, wait } = sdk;
    const def = workflow({ id: "wf_n", name: "n", trigger: manual() })
      .step("each", foreach({ items: ({ trigger }) => trigger.xs }, steps().step("double", transform(({ item }) => item * 2))))
      .step("route", branch({ paths: [{ name: "big", when: ({ trigger }) => trigger.n > 10, steps: steps().step("big_one", transform(1)) }], default: steps().step("small_one", transform(0)) }))
      .step("race", parallel({ join: "any", branches: { a: steps().step("wa", wait({ duration: "1m" })), b: steps().step("wb", wait({ duration: "2m" })) } }), {
        on_error: steps().step("recover", transform("failed")),
      })
      .build();
    const json = JSON.parse(JSON.stringify(def));
    expect(json.steps[0].config).toEqual({ items: "=trigger.xs", steps: [{ id: "double", type: "transform", config: { output: "=item * 2" } }] });
    expect(json.steps[1].config.default.steps[0].id).toBe("small_one");
    expect(json.steps[2].config.branches.map((b: { name: string }) => b.name)).toEqual(["a", "b"]);
    expect(json.steps[2].on_error.steps[0].id).toBe("recover");
    expect(evaluate(sdk.generate(def, { connectors: CONNECTOR_HELPERS, actions: CONNECTOR_ACTIONS }))).toEqual(def);
  });
});

describe("round trip edge cases", () => {
  const opts = { connectors: CONNECTOR_HELPERS, actions: CONNECTOR_ACTIONS };
  const base = (steps: unknown[]) => ({ schema: "wd/v1", id: "wf_e", version: 1, name: "e", trigger: { type: "manual" }, steps, settings: {} }) as unknown as WorkflowDefinition;

  it("keeps a key named __proto__ as a plain field", () => {
    const def = JSON.parse(`{"schema":"wd/v1","id":"wf_e","version":1,"name":"e","trigger":{"type":"manual"},"steps":[{"id":"t","type":"transform","config":{"output":{"__proto__":{"a":1},"b":"=run.id"}}}],"settings":{}}`);
    const code = sdk.generate(def, opts);
    expect(code).toContain(`["__proto__"]: { a: 1 }`);
    expect(evaluate(code)).toEqual(def);
    expect(Object.keys((evaluate(code).steps[0] as { config: { output: object } }).config.output)).toEqual(["__proto__", "b"]);
  });

  it("uses connector(...) for an action the helper does not have", () => {
    for (const action of ["constructor", "toString", "not_in_the_helper"]) {
      const def = base([{ id: "p", type: "connector", connector: "paystack@1", action, input: { a: 1 } }]);
      const code = sdk.generate(def, opts);
      expect(code).toContain(`connector("paystack@1", "${action}"`);
      expect(evaluate(code)).toEqual(def);
    }
    expect(sdk.generate(base([{ id: "p", type: "connector", connector: "paystack@1", action: "transfer", input: {} }]), opts)).toContain("paystack.transfer({})");
  });

  it("writes capability steps with their helpers, and back", () => {
    const def = base([
      { id: "pay", type: "capability", uses: "pay_bank", connection: "rail",
        input: { amount_kobo: 500000, bank_code: "058", account_number: "0123456789", account_name: "Bola Ade", narration: "Salary" },
        effect: { idempotency_seed: "=run.id" } },
      { id: "settle", type: "capability", uses: "await_settlement", needs: ["pay"], timeout: "72h", input: { payout_id: "=steps.pay.output.payout_id" } },
      { id: "other", type: "capability", uses: "pay_moon", input: { a: 1 } },
    ]);
    const code = sdk.generate(def, opts);
    expect(code).toContain("pay.bank({");
    expect(code).toContain("waitFor.settlement({");
    expect(code).toContain(`capability("pay_moon"`);
    expect(evaluate(code)).toEqual(def);
  });

  it("writes template steps unexpanded, and back", () => {
    const def = base([
      { id: "names", type: "transform", config: { output: { batch: "may-1" } } },
      { id: "pay", type: "template", template: "pay-a-list", needs: ["names"], params: { approver_role: "finance", approval_hours: 12 },
        input: { batch: "=steps.names.output.batch" } },
      { id: "inv", type: "template", template: "send-invoice", input: { name: "Ada", amount_naira: 5000, for: "Rice" } },
      { id: "tell", type: "transform", needs: ["pay"], config: { output: { paid: "=steps.pay.output.paid" } } },
    ]);
    const code = sdk.generate(def, opts);
    expect(code).toContain(`template("pay-a-list", {`);
    expect(code).toContain(`template("send-invoice", undefined, {`);
    expect(evaluate(code)).toEqual(def);
  });

  it("writes a workflow's outputs and callWorkflow steps, and back", () => {
    const def = {
      ...base([
        { id: "total", type: "transform", config: { output: { kobo: "=trigger.body.amount_kobo * 2" } } },
        { id: "sub", type: "template", workflow: "2f1a4d8e-3c0b-4e6f-9a21-5b7c8d9e0f12", needs: ["total"], input: { amount_kobo: "=steps.total.output.kobo" } },
      ]),
      inputs: { schema: { type: "object", properties: { amount_kobo: { type: "integer" } }, required: ["amount_kobo"] } },
      outputs: [{ name: "kobo", type: "integer", title: "Total", description: "Twice the amount", value: "=steps.total.output.kobo" }],
    } as WorkflowDefinition;
    const code = sdk.generate(def, opts);
    expect(code).toContain(`callWorkflow("2f1a4d8e-3c0b-4e6f-9a21-5b7c8d9e0f12", {`);
    expect(code).toContain("outputs:");
    expect(evaluate(code)).toEqual(def);
  });

  it("writes subflow steps, and back", () => {
    const def = base([
      { id: "sub", type: "subflow", config: { workflow: "2f1a4d8e-3c0b-4e6f-9a21-5b7c8d9e0f12" }, input: { amount_kobo: "=trigger.body.amount_kobo" } },
      { id: "bg", type: "subflow", needs: ["sub"], config: { workflow: "2f1a4d8e-3c0b-4e6f-9a21-5b7c8d9e0f12", version: 3, wait: false } },
      { id: "after", type: "transform", needs: ["sub"], config: { output: { kobo: "=steps.sub.output.outputs.kobo" } } },
    ]);
    const code = sdk.generate(def, opts);
    expect(code).toContain(`subflow({`);
    expect(evaluate(code)).toEqual(def);
  });

  it("writes callback and respond steps, and back", () => {
    const def = base([
      { id: "send", type: "transform", config: { output: { url: "=run.callbacks.verified" } } },
      { id: "verified", type: "callback", needs: ["send"], config: { timeout: "2h", on_timeout: "continue" } },
      { id: "plain", type: "callback", needs: ["send"] },
      { id: "answer", type: "respond", needs: ["verified"], config: { status: 201, headers: { "X-Ref": "=run.id" }, body: { ok: true, status: "=steps.verified.output.body.status" } } },
    ]);
    def.trigger = { type: "webhook", config: { path: "/kyc", auth: "hmac", respond: true } };
    const code = sdk.generate(def, opts);
    expect(code).toContain(`callback({ timeout: "2h", on_timeout: "continue" })`);
    expect(code).toContain(`callback()`);
    expect(code).toContain(`respond({`);
    expect(evaluate(code)).toEqual(def);
  });

  it("types numbers in code by value, so bundling cannot change them", () => {
    // What esbuild prints for 100000 and 2.0.
    expect(sdk.compileFunction("({ trigger }) => trigger.body.amount > 1e5")).toBe("trigger.body.amount > 100000");
    expect(sdk.compileFunction("({ trigger }) => trigger.body.rate * 2")).toBe("trigger.body.rate * 2");
    expect(sdk.compileFunction("({ trigger }) => trigger.body.rate * 1.5")).toBe("trigger.body.rate * 1.5");
    // A CEL double with an integral value, or text code would print otherwise, stays a CEL string.
    for (const cel of ["=trigger.body.rate * 2.0", "=trigger.body.rate * 1e5", "=trigger.body.rate * 1.50", "=trigger.body.n == 0x10"]) {
      const def = base([{ id: "t", type: "transform", config: { output: cel } }]);
      const code = sdk.generate(def, opts);
      expect(code).toContain(JSON.stringify(cel));
      expect(evaluate(code)).toEqual(def);
    }
  });
});

describe("ussd menus", () => {
  it("lower arrow-function conditions to CEL and print them back", () => {
    const wf = sdk
      .workflow({
        id: "wf_ussd",
        name: "ussd",
        trigger: sdk.ussd({
          service_code: "*384*1#",
          screens: [
            { id: "amount", type: "input", text: "Amount", input: "amount", validate: { type: "integer", min: 1, when: ({ trigger }) => trigger.body.amount < 5000 }, next: "pick" },
            { id: "pick", type: "menu", text: "Speed", input: "speed", options: [{ label: "Fast", next: "ok", value: "fast", when: ({ trigger }) => trigger.body.amount > 100 }] },
            { id: "ok", type: "confirm", text: "Pay {{amount}}?" },
          ],
        }),
      })
      .step("t", sdk.transform({ amount: ({ trigger }) => trigger.body.amount }));
    const def = wf.build();
    const cfg = def.trigger.config as unknown as sdk.UssdMenu;
    expect(cfg.screens[0]?.validate?.when).toBe("=trigger.body.amount < 5000");
    expect(cfg.screens[1]?.options?.[0]?.when).toBe("=trigger.body.amount > 100");
    const code = sdk.generate(def);
    expect(code).toContain("trigger: ussd({");
    expect(code).toContain("when: ({ trigger }) => trigger.body.amount < 5000");
    expect(evaluate(code)).toEqual(def);
  });
});

describe("container steps", () => {
  it("build with the helper and print back to it", () => {
    const image = `registry.example.com/tools/pdf@sha256:${"0f".repeat(32)}`;
    const def = sdk
      .workflow({ id: "wf_render", name: "render", trigger: sdk.manual() })
      .step(
        "render",
        sdk.container(
          { image, command: ["/bin/render"], secrets: ["api_key"], class: "idempotent_write", network: "egress", hosts: ["api.example.com"], limits: { cpu: "1.5", memory_mb: 1024, timeout: "10m" } },
          { html: ({ trigger }) => trigger.body.html },
          { effect: { idempotency_seed: ({ trigger }) => trigger.body.id } },
        ),
      )
      .build();
    expect(def.steps[0]).toEqual({
      id: "render",
      type: "container",
      input: { html: "=trigger.body.html" },
      effect: { idempotency_seed: "=trigger.body.id" },
      config: { image, command: ["/bin/render"], secrets: ["api_key"], class: "idempotent_write", network: "egress", hosts: ["api.example.com"], limits: { cpu: "1.5", memory_mb: 1024, timeout: "10m" } },
    });
    const code = sdk.generate(def);
    expect(code).toContain("container(");
    expect(evaluate(code)).toEqual(def);
    expect(sdk.generate(evaluate(code))).toBe(code);
  });
});

describe("forms", () => {
  it("build a form trigger and a form step and print back to them", () => {
    const def = sdk
      .workflow({
        id: "wf_apply",
        name: "apply",
        trigger: sdk.formTrigger({ title: "Apply for a loan", access: "link", verify: "phone", pages: [{ title: "About you", fields: ["full_name", "phone"] }] }),
        inputs: { schema: { type: "object", required: ["phone"], properties: { full_name: { type: "string" }, phone: { type: "string", "x-pii": "phone" } } } },
      })
      .step(
        "ask",
        sdk.form({
          title: "Confirm your bank",
          to: { phone: ({ trigger }) => trigger.body.phone },
          schema: { type: "object", properties: { bank: { type: "string" } } },
          prefill: { bank: "=trigger.body.full_name" },
          timeout: "72h",
          on_timeout: "continue",
        }),
      )
      .build();
    expect(def.trigger).toEqual({ type: "form", config: { title: "Apply for a loan", access: "link", verify: "phone", pages: [{ title: "About you", fields: ["full_name", "phone"] }] } });
    expect(def.steps[0]).toMatchObject({ type: "form", config: { to: { phone: "=trigger.body.phone" }, prefill: { bank: "=trigger.body.full_name" } } });
    const code = sdk.generate(def);
    expect(code).toContain("formTrigger(");
    expect(code).toContain("form(");
    expect(evaluate(code)).toEqual(def);
    expect(sdk.generate(evaluate(code))).toBe(code);
  });
});

describe("invoice trigger", () => {
  it("builds a workflow that starts when an invoice is paid and prints back to it", () => {
    const def = sdk
      .workflow({ id: "wf_receipt", name: "receipt", trigger: sdk.invoiceTrigger({ event: "paid" }) })
      .step("thanks", sdk.connector("invoices@1", "notify_customer", { number: "=trigger.body.number", text: "Thank you: {link}" }))
      .build();
    expect(def.trigger).toEqual({ type: "invoice", config: { event: "paid" } });
    const code = sdk.generate(def);
    expect(code).toContain("invoiceTrigger(");
    expect(evaluate(code)).toEqual(def);
    expect(sdk.generate(evaluate(code))).toBe(code);
  });
});
