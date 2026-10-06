import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import * as sdk from "./index.js";
import * as connectors from "./connectors.gen.js";
import { CONNECTOR_HELPERS } from "./connectors.gen.js";
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
      const code = sdk.generate(def, { connectors: CONNECTOR_HELPERS });
      expect(evaluate(code)).toEqual(def);
      // Generation is deterministic and stable under a second pass.
      expect(sdk.generate(evaluate(code), { connectors: CONNECTOR_HELPERS })).toBe(code);
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
    expect(evaluate(sdk.generate(def, { connectors: CONNECTOR_HELPERS }))).toEqual(def);
  });
});
