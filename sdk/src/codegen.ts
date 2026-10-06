// Code generation from a definition (spec 10.2): when a workflow is edited on
// the canvas, its .flow.ts file is rewritten. Output is deterministic and
// stable, so Git diffs show only what changed, and it always builds back to
// exactly the definition it came from: an expression is printed as an arrow
// function only when that compiles to the same CEL text, and kept as a CEL
// string otherwise.

import { celToFunction } from "./expr.js";
import type { Step, WorkflowDefinition } from "./wd.js";

export interface CodegenOptions {
  /** Connector refs with generated helpers, such as { "iswallet@1": "iswallet" }. */
  connectors?: Record<string, string>;
  /** Module the SDK is imported from (default "@taskiem/sdk"). */
  sdkModule?: string;
  /** Module connector helpers are imported from (default "@taskiem/connectors"). */
  connectorsModule?: string;
}

const WIDTH = 100;
const IDENT = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

export function generate(def: WorkflowDefinition, opts: CodegenOptions = {}): string {
  const g = new Gen(opts.connectors ?? {});
  const header: Record<string, unknown> = { id: def.id };
  if (def.version !== 1) header.version = def.version;
  header.name = def.name;
  if (def.description !== undefined) header.description = def.description;
  header.trigger = new Raw(g.trigger(def.trigger, "  "));
  for (const k of ["inputs", "types", "settings"] as const) {
    if (def[k] !== undefined) header[k] = def[k];
  }
  for (const k of Object.keys(def)) {
    if (!["schema", "id", "version", "name", "description", "trigger", "inputs", "steps", "types", "settings"].includes(k)) {
      throw new Error(`codegen: unknown top-level field "${k}"`);
    }
  }
  g.use("workflow");
  let body = `export default workflow(${g.value(header, "")})`;
  body += g.chain(def.steps, "  ");
  body += ";\n";

  let out = "";
  const sdk = [...g.sdk].sort();
  out += `import { ${sdk.join(", ")} } from "${opts.sdkModule ?? "@taskiem/sdk"}";\n`;
  const conns = [...g.conns].sort();
  if (conns.length > 0) out += `import { ${conns.join(", ")} } from "${opts.connectorsModule ?? "@taskiem/connectors"}";\n`;
  return out + "\n" + body;
}

/** Already-printed code, placed as is. */
class Raw {
  constructor(readonly code: string) {}
}

class Gen {
  sdk = new Set<string>();
  conns = new Set<string>();
  constructor(readonly connectors: Record<string, string>) {}

  use(name: string) {
    this.sdk.add(name);
  }

  trigger(t: WorkflowDefinition["trigger"], indent: string): string {
    const helper = { webhook: "webhook", schedule: "schedule", connector_event: "connectorEvent", manual: "manual" }[t.type as string];
    if (helper !== undefined && (t.config !== undefined || helper === "manual")) {
      this.use(helper);
      return `${helper}(${t.config === undefined ? "" : this.value(t.config, indent)})`;
    }
    this.use("trigger");
    return `trigger(${JSON.stringify(t.type)}${t.config === undefined ? "" : ", " + this.value(t.config, indent)})`;
  }

  /** Prints a step list as .step()/.next() calls, one per line. */
  chain(list: Step[], indent: string): string {
    let out = "";
    let prev: string | undefined;
    for (const s of list) {
      out += `\n${indent}${this.step(s, prev, indent)}`;
      prev = s.id;
    }
    return out;
  }

  nested(list: Step[], indent: string): Raw {
    this.use("steps");
    return new Raw("steps()" + this.chain(list, indent + "  "));
  }

  step(s: Step, prev: string | undefined, indent: string): string {
    const step = s as unknown as Record<string, unknown>;
    const known = new Set(["id", "name", "description", "type", "needs", "when", "retry", "timeout", "on_error"]);
    const opts: Record<string, unknown> = {};
    let method = "step";
    const needs = s.needs;
    if (needs !== undefined && needs.length > 0 && needs[0] === prev) {
      method = "next";
      if (needs.length > 1) opts.needs = needs.slice(1);
    } else if (needs !== undefined) {
      opts.needs = needs;
    }
    for (const k of ["name", "description", "when", "retry", "timeout"] as const) {
      if (step[k] !== undefined) opts[k] = step[k];
    }
    if (s.on_error !== undefined) opts.on_error = this.nested(s.on_error.steps, indent + "  ");

    const spec = this.spec(step, known, indent);
    for (const k of Object.keys(step)) {
      if (!known.has(k)) throw new Error(`codegen: step ${s.id}: unexpected field "${k}"`);
    }
    const args = [JSON.stringify(s.id), spec];
    if (Object.keys(opts).length > 0) args.push(this.value(opts, indent));
    return `.${method}(${args.join(", ")})`;
  }

  spec(s: Record<string, unknown>, known: Set<string>, indent: string): string {
    const cfg = s.config as Record<string, unknown> | undefined;
    const take = (...keys: string[]) => keys.forEach((k) => known.add(k));
    const optional = (v: unknown) => (v === undefined ? undefined : v);
    const call = (fn: string, ...args: unknown[]) => {
      while (args.length > 0 && args[args.length - 1] === undefined) args.pop();
      return `${fn}(${args.map((a) => (a === undefined ? "undefined" : this.value(a, indent))).join(", ")})`;
    };
    switch (s.type) {
      case "connector": {
        take("connector", "action", "input", "connection", "effect", "compensate");
        const extra: Record<string, unknown> = {};
        for (const k of ["connection", "effect", "compensate"]) if (s[k] !== undefined) extra[k] = s[k];
        const ref = s.connector as string;
        const action = s.action as string;
        const helper = this.connectors[ref];
        const extraArg = Object.keys(extra).length > 0 ? extra : undefined;
        if (helper !== undefined && IDENT.test(action)) {
          this.conns.add(helper);
          return call(`${helper}.${action}`, optional(s.input), extraArg);
        }
        this.use("connector");
        return `connector(${JSON.stringify(ref)}, ${JSON.stringify(action)}${s.input !== undefined || extraArg ? ", " + (s.input === undefined ? "undefined" : this.value(s.input, indent)) : ""}${extraArg ? ", " + this.value(extraArg, indent) : ""})`;
      }
      case "http":
        take("config", "effect");
        this.use("http");
        return call("http", cfg, s.effect === undefined ? undefined : { effect: s.effect });
      case "code": {
        take("config", "input");
        this.use("code");
        const c: Record<string, unknown> = { ...cfg };
        if (typeof c.source === "string") c.source = new Raw(template(c.source));
        return call("code", c, optional(s.input));
      }
      case "transform":
        take("config");
        this.use("transform");
        if (cfg === undefined || Object.keys(cfg).length !== 1 || !("output" in cfg)) throw new Error("codegen: transform config must hold only output");
        return `transform(${this.value(cfg.output, indent)})`;
      case "wait":
      case "signal":
      case "approval":
        take("config");
        this.use(s.type);
        return call(s.type, cfg);
      case "subflow":
      case "ai":
        take("config", "input");
        this.use(s.type);
        return call(s.type, cfg, optional(s.input));
      case "foreach": {
        take("config");
        this.use("foreach");
        const { steps: body, ...rest } = cfg as { steps: Step[] };
        return `foreach(${this.value(rest, indent)}, ${this.nested(body, indent).code})`;
      }
      case "branch": {
        take("config");
        this.use("branch");
        const c = cfg as { paths: { name: string; when: unknown; steps: Step[] }[]; default?: { steps: Step[] } };
        const out: Record<string, unknown> = {
          paths: c.paths.map((p) => ({ name: p.name, when: p.when, steps: this.nested(p.steps, indent + "    ") })),
        };
        if (c.default !== undefined) out.default = this.nested(c.default.steps, indent + "  ");
        for (const k of Object.keys(c)) if (k !== "paths" && k !== "default") throw new Error(`codegen: branch: unexpected field "${k}"`);
        return `branch(${this.value(out, indent)})`;
      }
      case "parallel": {
        take("config");
        this.use("parallel");
        const { branches, ...rest } = cfg as { branches: { name: string; steps: Step[] }[] };
        const b: Record<string, unknown> = {};
        for (const br of branches) b[br.name] = this.nested(br.steps, indent + "  ");
        return `parallel(${this.value({ branches: b, ...rest }, indent)})`;
      }
    }
    throw new Error(`codegen: unknown step type "${String(s.type)}"`);
  }

  /** Prints a value as a JavaScript literal; inline when it fits. */
  value(v: unknown, indent: string): string {
    if (v instanceof Raw) return v.code;
    if (typeof v === "string") {
      if (v.startsWith("=") && v.length > 1) {
        const fn = celToFunction(v.slice(1));
        if (fn !== null) {
          if (/\bcel\./.test(fn)) this.use("cel");
          return fn;
        }
      }
      return JSON.stringify(v);
    }
    if (v === null || typeof v !== "object") {
      if (typeof v === "number" && !Number.isFinite(v)) throw new Error("codegen: non-finite number");
      return JSON.stringify(v);
    }
    const inner = indent + "  ";
    if (Array.isArray(v)) {
      const items = v.map((x) => this.value(x, inner));
      const flat = `[${items.join(", ")}]`;
      if (fits(flat, indent)) return flat;
      return `[\n${items.map((x) => inner + x).join(",\n")},\n${indent}]`;
    }
    const entries = Object.entries(v as Record<string, unknown>).map(([k, x]) => `${IDENT.test(k) ? k : JSON.stringify(k)}: ${this.value(x, inner)}`);
    if (entries.length === 0) return "{}";
    const flat = `{ ${entries.join(", ")} }`;
    if (fits(flat, indent)) return flat;
    return `{\n${entries.map((x) => inner + x).join(",\n")},\n${indent}}`;
  }
}

function fits(s: string, indent: string): boolean {
  return !s.includes("\n") && indent.length + s.length <= WIDTH;
}

/** A template literal holding exactly s. */
function template(s: string): string {
  return "`" + s.replace(/\\/g, "\\\\").replace(/`/g, "\\`").replace(/\$\{/g, "\\${").replace(/\r/g, "\\r") + "`";
}
