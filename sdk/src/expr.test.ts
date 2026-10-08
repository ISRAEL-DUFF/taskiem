import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { celToFunction, compileFunction, ExpressionError } from "./expr.js";

describe("compileFunction", () => {
  const cases: [string, string][] = [
    ["({ trigger }) => trigger.body.amount", "trigger.body.amount"],
    ["(c) => c.trigger.body.amount * 100", "trigger.body.amount * 100"],
    ["({ trigger: t, steps }) => t.body.x === steps.a.output.y", "trigger.body.x == steps.a.output.y"],
    ["({ steps }) => steps.approve.output.decision !== 'approved'", "steps.approve.output.decision != 'approved'"],
    ['({ trigger }) => "Salary " + trigger.body.period', "'Salary ' + trigger.body.period"],
    ["({ trigger }) => `Salary ${trigger.body.period}!`", "'Salary ' + string(trigger.body.period) + '!'"],
    ["({ item }) => cel.has(item.wallet_id)", "has(item.wallet_id)"],
    ["({ item }) => !cel.has(item.wallet_id)", "!has(item.wallet_id)"],
    ["({ trigger }) => cel.size(trigger.body.employees.filter((e) => !cel.has(e.wallet_id)))", "size(trigger.body.employees.filter(e, !has(e.wallet_id)))"],
    ["({ steps }) => steps.b.output.balances.some((b) => b.currency === 'NGN')", "steps.b.output.balances.exists(b, b.currency == 'NGN')"],
    ["({ trigger }) => trigger.body.xs.map((x) => x.id)", "trigger.body.xs.map(x, x.id)"],
    ["({ trigger }) => (trigger.a + trigger.b) * 2", "(trigger.a + trigger.b) * 2"],
    ["({ trigger }) => trigger.a - (trigger.b - trigger.c)", "trigger.a - (trigger.b - trigger.c)"],
    ["({ trigger }) => trigger.ok ? 'yes' : trigger.maybe ? 'maybe' : 'no'", "trigger.ok ? 'yes' : trigger.maybe ? 'maybe' : 'no'"],
    ["({ trigger }) => ({ id: trigger.id, 'a-b': [1, 2.5] })", "{'id': trigger.id, 'a-b': [1, 2.5]}"],
    ["({ trigger }) => cel.in(trigger.code, ['000', '999'])", "trigger.code in ['000', '999']"],
    ["({ trigger }) => { return trigger.x; }", "trigger.x"],
    ["({ trigger }) => trigger.body['odd key'][0]", "trigger.body['odd key'][0]"],
    ["({ trigger }) => cel.double(trigger.body.amount) / 100.5", "double(trigger.body.amount) / 100.5"],
    ["({ trigger }) => trigger.s.startsWith('NG') && trigger.n >= -1", "trigger.s.startsWith('NG') && trigger.n >= -1"],
    ['({ trigger }) => trigger.s === "it\'s \\"x\\"\\n"', "trigger.s == 'it\\'s \"x\"\\n'"],
    ["({ trigger }) => trigger.body.amount!", "trigger.body.amount"],
    ["({ trigger }) => trigger.xs.filter((e: { ok: boolean }) => e.ok)", "trigger.xs.filter(e, e.ok)"],
  ];
  for (const [js, cel] of cases) {
    it(js, () => expect(compileFunction(js)).toBe(cel));
  }

  const errors: [string, RegExp][] = [
    ["({ trigger }) => trigger.a == 1", /strict/],
    ["({ trigger }) => trigger.a?.b", /optional chaining/],
    ["({ trigger }) => trigger.list.length", /cel.size/],
    ["({ trigger }) => Math.max(trigger.a, 1)", /not available/],
    ["({ trigger }) => foo", /not available/],
    ["({ trigger }) => trigger.a ?? 1", /cel.has/],
    ["({ ctx }) => ctx", /no "ctx"/],
    ["(c) => c", /on its own/],
    ["({ trigger }) => trigger.a === trigger.b < 1", /parentheses/],
    ["({ trigger }) => 'k' in trigger", /cel.in/],
    ["({ trigger }) => trigger.s.toLowerCase()", /no CEL equivalent/],
    ["(a, b) => a", /one parameter/],
    ["function f() { return 1 }", /not supported/],
  ];
  for (const [js, re] of errors) {
    it(`rejects ${js}`, () => {
      expect(() => compileFunction(js)).toThrow(ExpressionError);
      expect(() => compileFunction(js)).toThrow(re);
    });
  }
});

describe("celToFunction", () => {
  it("prints functions that compile back to the same CEL", () => {
    expect(celToFunction("trigger.body.payroll_id + ':' + item.employee_id")).toBe("({ trigger, item }) => trigger.body.payroll_id + \":\" + item.employee_id");
    expect(celToFunction("steps.approve.output.decision == 'approved'")).toBe('({ steps }) => steps.approve.output.decision === "approved"');
    expect(celToFunction("size(trigger.body.employees.filter(e, !has(e.wallet_id)))")).toBe("({ trigger }) => cel.size(trigger.body.employees.filter((e) => !cel.has(e.wallet_id)))");
  });
  it("keeps CEL it cannot print exactly", () => {
    for (const cel of ['trigger.x == "double quoted"', "trigger.a  +  1", "(trigger.a)", "trigger.amount * 100.0", "trigger.xs.exists_one(x, x)", "1 + 2", "trigger.n == 1u", "steps.x.output.length"]) {
      expect(celToFunction(cel), cel).toBeNull();
    }
  });

  // Every expression in the repository's workflows either round-trips
  // exactly or is kept as CEL.
  it("round-trips every expression in flows/", () => {
    const files: string[] = [];
    const walk = (d: string) => {
      for (const f of readdirSync(d)) {
        const p = join(d, f);
        if (statSync(p).isDirectory()) walk(p);
        else if (f.endsWith(".wd.json")) files.push(p);
      }
    };
    walk(join(import.meta.dirname, "../../flows"));
    let printed = 0;
    let kept = 0;
    const visit = (v: unknown) => {
      if (typeof v === "string" && v.startsWith("=")) {
        const fn = celToFunction(v.slice(1));
        if (fn === null) {
          kept++;
        } else {
          printed++;
          expect(compileFunction(fn), v).toBe(v.slice(1));
        }
      } else if (Array.isArray(v)) v.forEach(visit);
      else if (v && typeof v === "object") Object.values(v).forEach(visit);
    };
    for (const f of files) visit(JSON.parse(readFileSync(f, "utf8")));
    expect(printed).toBeGreaterThan(40);
    expect(kept).toBeLessThan(printed / 4);
  });
});

describe("bundled source", () => {
  it("recognises cel helpers renamed by bundlers", () => {
    expect(compileFunction("({ trigger }) => cel2.size(trigger.xs)")).toBe("size(trigger.xs)");
    expect(compileFunction("({ trigger }) => __vite_ssr_import_0__.cel.has(trigger.x)")).toBe("has(trigger.x)");
    expect(compileFunction("({ trigger }) => import_sdk.cel.in(trigger.x, [1])")).toBe("trigger.x in [1]");
    expect(compileFunction("({ trigger }) => trigger.cel.has(trigger.x)")).toBe("trigger.cel.has(trigger.x)"); // a field, not the helpers
  });
});
