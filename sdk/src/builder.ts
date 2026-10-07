// The code SDK (spec 10.1): a declarative builder that produces a wd/v1
// definition. It mirrors the definition one to one, so code and canvas
// round-trip; arbitrary logic belongs in code steps.
//
//   export default workflow({ id: "wf_payout", name: "Payout", trigger: manual() })
//     .step("check", iswallet.get_balance({ wallet_id: ({ trigger }) => trigger.body.wallet }))
//     .next("pay", iswallet.transfer({ ... }), { when: ({ steps }) => steps.check.output.ok });
//
// Values may be literals, raw CEL strings ("=trigger.body.x"), or arrow
// functions over the expression context, compiled to CEL by build().

import { compileFunction, ExpressionError } from "./expr.js";
import type { ActionClass, Duration, Retry, Step, Trigger, WorkflowDefinition } from "./wd.js";

/** What an expression can read (wd/v1 rule 10). Values are untyped. */
export interface Context {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  trigger: any;
  /** steps.<id>.output, or steps.<id>.error in an on_error flow. */
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  steps: any;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  env: Record<string, any>;
  run: { id: string; tenant_id: string; workflow_id: string; version: number; environment: string; started_at: string };
  secrets: Record<string, string>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  item: any;
  index: number;
}

/** An expression: an arrow function over the context, or a CEL string. */
export type Expr<T = unknown> = ((ctx: Context) => T) | `=${string}`;

/** A literal or an expression, at any depth. */
export type Val<T = unknown> = T extends object ? Expr<T> | { [K in keyof T]: Val<T[K]> } : T | Expr<T>;

/** A value: a literal or an expression, at any depth. */
export type Value = Expr | string | number | boolean | null | Value[] | { [key: string]: Value };

/** Values by name: literals or expressions at any depth. */
export type Values = { [key: string]: Value };

// ------------------------------------------------------------ CEL helpers

const stub = (name: string) => (): never => {
  throw new Error(`cel.${name}() only works inside an expression function, which is compiled to CEL`);
};

/** CEL functions for use inside expression functions: cel.has(x.y) and so on. */
export const cel = {
  has: stub("has") as (field: unknown) => boolean,
  size: stub("size") as (value: unknown) => number,
  string: stub("string") as (value: unknown) => string,
  int: stub("int") as (value: unknown) => number,
  uint: stub("uint") as (value: unknown) => number,
  double: stub("double") as (value: unknown) => number,
  bool: stub("bool") as (value: unknown) => boolean,
  bytes: stub("bytes") as (value: unknown) => unknown,
  timestamp: stub("timestamp") as (value: unknown) => unknown,
  duration: stub("duration") as (value: unknown) => unknown,
  type: stub("type") as (value: unknown) => unknown,
  dyn: stub("dyn") as (value: unknown) => unknown,
  matches: stub("matches") as (value: string, re: string) => boolean,
  in: stub("in") as (value: unknown, list: unknown) => boolean,
};

// -------------------------------------------------------------- triggers

export function webhook(config: { path: string; auth: "hmac" | "bearer" | "mtls" | "none"; dedup?: Expr; secret?: string } & Values): Trigger {
  return { type: "webhook", config };
}
export function schedule(config: { cron: string; timezone?: string } & Values): Trigger {
  return { type: "schedule", config };
}
export function connectorEvent(config: { connector: string; trigger: string; events?: string[] } & Values): Trigger {
  return { type: "connector_event", config };
}
export function manual(config?: Values): Trigger {
  return config === undefined ? { type: "manual" } : { type: "manual", config };
}
/** Any trigger type, for those without a helper. */
export function trigger(type: Trigger["type"], config?: Values): Trigger {
  return config === undefined ? { type } : { type, config };
}

// ----------------------------------------------------------------- steps

/** A step without its id and common options: what .step() takes. */
export type StepSpec = { type: Step["type"] } & Record<string, unknown>;

export interface StepOptions {
  name?: string;
  description?: string;
  needs?: string[];
  when?: Expr<boolean>;
  retry?: Retry;
  timeout?: Duration;
  on_error?: Steps;
}

export interface ConnectorExtra {
  connection?: string;
  effect?: { idempotency_seed?: Expr };
  compensate?: { action: string; input?: Values };
}

export function connector(ref: `${string}@${number}`, action: string, input?: object, extra?: ConnectorExtra): StepSpec {
  return { type: "connector", connector: ref, action, ...(input === undefined ? {} : { input }), ...extra };
}

export interface HttpConfig {
  method: "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE";
  url: Val<string>;
  headers?: Values;
  query?: Values;
  body?: Value;
  class?: ActionClass;
  idempotency_header?: string;
}

export function http(config: HttpConfig, extra?: { effect?: { idempotency_seed?: Expr } }): StepSpec {
  return { type: "http", ...extra, config };
}

export interface CodeConfig {
  language: "javascript" | "typescript" | "python" | "wasm";
  source?: string;
  module?: string;
  secrets?: string[];
  limits?: { memory_mb?: number; cpu?: Duration };
}

export function code(config: CodeConfig, input?: Values): StepSpec {
  return { type: "code", ...(input === undefined ? {} : { input }), config };
}

export function transform(output: Value): StepSpec {
  return { type: "transform", config: { output } };
}

export function wait(config: { duration: Duration } | { until: Expr<string> }): StepSpec {
  return { type: "wait", config };
}

export function signal(config: { event: string; correlation: Expr; timeout?: Duration }): StepSpec {
  return { type: "signal", config };
}

export interface ApprovalConfig {
  policy?: string;
  role?: string;
  count?: number;
  timeout?: Duration;
  on_timeout?: "reject" | "fail" | `escalate:${string}`;
  subject?: Values;
}

export function approval(config: ApprovalConfig): StepSpec {
  return { type: "approval", config };
}

export function branch(config: { paths: { name: string; when: Expr<boolean>; steps: Steps }[]; default?: Steps }): StepSpec {
  return { type: "branch", config };
}

/** Branches run side by side; the object's key order is the branch order. */
export function parallel(config: { branches: Record<string, Steps>; join?: "all" | "any"; max_concurrency?: number }): StepSpec {
  return { type: "parallel", config };
}

export function foreach(config: { items: Expr<unknown[]>; max_concurrency?: number; max_items?: number }, body: Steps): StepSpec {
  return { type: "foreach", config: { ...config, steps: body } };
}

export function subflow(config: { workflow: `wf_${string}`; version: number; wait?: boolean }, input?: Values): StepSpec {
  return { type: "subflow", ...(input === undefined ? {} : { input }), config };
}

export function ai(config: { prompt: string; output_schema: object | boolean; model?: string }, input?: Values): StepSpec {
  return { type: "ai", ...(input === undefined ? {} : { input }), config };
}

// ------------------------------------------------------------ step lists

/** An ordered list of steps: a workflow's, or a nested scope's. */
export class Steps {
  protected readonly list: { id: string; spec: StepSpec; opts: StepOptions }[] = [];

  /** Adds a step. It runs as soon as the steps it needs have settled. */
  step(id: string, spec: StepSpec, opts: StepOptions = {}): this {
    if (this.list.some((s) => s.id === id)) throw new Error(`step ${id} is defined twice`);
    this.list.push({ id, spec, opts });
    return this;
  }

  /** Adds a step that needs the previous one (and any others in opts.needs). */
  next(id: string, spec: StepSpec, opts: StepOptions = {}): this {
    const prev = this.list[this.list.length - 1];
    if (!prev) throw new Error(`.next("${id}") needs a step before it; use .step()`);
    const needs = [prev.id, ...(opts.needs ?? []).filter((n) => n !== prev.id)];
    return this.step(id, spec, { ...opts, needs });
  }

  /** The steps as wd/v1, with expressions compiled. */
  build(path = "steps"): Step[] {
    return this.list.map(({ id, spec, opts }) => buildStep(id, spec, opts, `${path}.${id}`));
  }
}

/** A list of nested steps, for foreach bodies, branch paths and on_error. */
export function steps(): Steps {
  return new Steps();
}

export interface WorkflowOptions {
  id: `wf_${string}`;
  name: string;
  version?: number;
  description?: string;
  trigger: Trigger;
  inputs?: { schema?: object | boolean };
  types?: Record<string, object | boolean>;
  settings?: { timeout?: Duration; concurrency_key?: Expr; max_concurrency?: number; retention?: Duration };
}

export class Workflow extends Steps {
  constructor(readonly options: WorkflowOptions) {
    super();
  }

  /** The workflow definition (wd/v1). */
  build(): WorkflowDefinition;
  build(path: string): Step[];
  build(path?: string): WorkflowDefinition | Step[] {
    if (path !== undefined) return super.build(path);
    const o = this.options;
    const def: Record<string, unknown> = { schema: "wd/v1", id: o.id, version: o.version ?? 1, name: o.name };
    if (o.description !== undefined) def.description = o.description;
    def.trigger = lowerAt(o.trigger, "trigger");
    if (o.inputs !== undefined) def.inputs = o.inputs;
    def.steps = super.build("steps");
    if (o.types !== undefined) def.types = o.types;
    if (o.settings !== undefined) def.settings = lowerAt(o.settings, "settings");
    return def as unknown as WorkflowDefinition;
  }

  toJSON(): WorkflowDefinition {
    return this.build();
  }
}

export function workflow(options: WorkflowOptions): Workflow {
  return new Workflow(options);
}

// Key order of a built step, so the JSON committed next to the code is
// stable and reads in the order an author thinks.
const STEP_KEYS = ["id", "name", "description", "type", "needs", "when", "connector", "action", "connection", "input", "effect", "compensate", "config", "retry", "timeout", "on_error"];

function buildStep(id: string, spec: StepSpec, opts: StepOptions, path: string): Step {
  const raw: Record<string, unknown> = { id, ...spec };
  for (const [k, v] of Object.entries(opts)) {
    if (v !== undefined) raw[k] = v;
  }
  const out: Record<string, unknown> = {};
  for (const k of STEP_KEYS) {
    const v = raw[k];
    if (v === undefined) continue;
    switch (k) {
      case "on_error":
        out[k] = { steps: asSteps(v, `${path}.on_error`).build(`${path}.on_error`) };
        break;
      case "config":
        out[k] = buildConfig(spec.type, v as Record<string, unknown>, `${path}.config`);
        break;
      default:
        out[k] = lowerAt(v, `${path}.${k}`);
    }
  }
  for (const k of Object.keys(raw)) {
    if (!STEP_KEYS.includes(k)) throw new Error(`${path}: unknown step field "${k}"`);
  }
  return out as unknown as Step;
}

function buildConfig(type: string, cfg: Record<string, unknown>, path: string): unknown {
  switch (type) {
    case "foreach": {
      const { steps: body, ...rest } = cfg;
      return { ...(lowerAt(rest, path) as object), steps: asSteps(body, `${path}.steps`).build(`${path}.steps`) };
    }
    case "branch": {
      const paths = (cfg.paths as { name: string; when: unknown; steps: unknown }[]).map((p, i) => ({
        name: p.name,
        when: lowerAt(p.when, `${path}.paths[${i}].when`),
        steps: asSteps(p.steps, `${path}.paths[${i}]`).build(`${path}.paths.${p.name}`),
      }));
      const out: Record<string, unknown> = { paths };
      if (cfg.default !== undefined) out.default = { steps: asSteps(cfg.default, `${path}.default`).build(`${path}.default`) };
      return out;
    }
    case "parallel": {
      const { branches, ...rest } = cfg;
      return {
        branches: Object.entries(branches as Record<string, unknown>).map(([name, b]) => ({
          name,
          steps: asSteps(b, `${path}.branches.${name}`).build(`${path}.branches.${name}`),
        })),
        ...(lowerAt(rest, path) as object),
      };
    }
  }
  return lowerAt(cfg, path);
}

function asSteps(v: unknown, path: string): Steps {
  if (v instanceof Steps) return v;
  throw new Error(`${path}: expected steps() with .step(...) calls`);
}

/** Compiles expression functions to CEL strings, at any depth. */
export function lowerAt(v: unknown, path: string): unknown {
  if (typeof v === "function") {
    try {
      return "=" + compileFunction(v.toString());
    } catch (e) {
      if (e instanceof ExpressionError) throw new ExpressionError(`${path}: ${e.message}`);
      throw e;
    }
  }
  if (v instanceof Steps) throw new Error(`${path}: steps are not allowed here`);
  if (Array.isArray(v)) return v.map((x, i) => lowerAt(x, `${path}[${i}]`));
  if (v !== null && typeof v === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, x] of Object.entries(v)) {
      // defineProperty, so a key named __proto__ stays a plain property.
      if (x !== undefined) Object.defineProperty(out, k, { value: lowerAt(x, `${path}.${k}`), enumerable: true, writable: true, configurable: true });
    }
    return out;
  }
  return v;
}
