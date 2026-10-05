// TypeScript view of the wd/v1 contract (schemas/wd-v1.schema.json).
// The JSON Schema is the source of truth; these types must not be looser.

/** A CEL expression: a string starting with "=". */
export type Expression = `=${string}`;

/** "<n><unit>" repeated; units ms, s, m, h, d. */
export type Duration = string;

export type ActionClass = "read" | "idempotent_write" | "reconcilable_write" | "unsafe_write";

/** Literals or expressions, at any depth. */
export type Values = { [key: string]: unknown };

export interface Retry {
  max: number;
  backoff?: "fixed" | "exponential";
  initial?: Duration;
  max_delay?: Duration;
  max_duration?: Duration;
}

export interface SubFlow {
  steps: Step[];
}

interface StepBase {
  id: string;
  name?: string;
  description?: string;
  needs?: string[];
  when?: Expression;
  retry?: Retry;
  timeout?: Duration;
  on_error?: SubFlow;
}

export interface ConnectorStep extends StepBase {
  type: "connector";
  connector: `${string}@${number}`;
  action: string;
  connection?: string;
  input?: Values;
  effect?: { idempotency_seed?: Expression };
  compensate?: { action: string; input?: Values };
}

export interface CodeStep extends StepBase {
  type: "code";
  input?: Values;
  config:
    | { language: "javascript" | "typescript" | "python"; source: string; secrets?: string[]; limits?: { memory_mb?: number; cpu?: Duration } }
    | { language: "wasm"; module: `sha256:${string}`; secrets?: string[]; limits?: { memory_mb?: number; cpu?: Duration } };
}

export interface HttpStep extends StepBase {
  type: "http";
  effect?: { idempotency_seed?: Expression };
  config:
    | { method: "GET" | "HEAD"; url: string; headers?: Values; query?: Values; body?: unknown; class?: ActionClass; idempotency_header?: string }
    | { method: "POST" | "PUT" | "PATCH" | "DELETE"; url: string; headers?: Values; query?: Values; body?: unknown; class: Exclude<ActionClass, "read">; idempotency_header?: string };
}

export interface BranchStep extends StepBase {
  type: "branch";
  config: { paths: { name: string; when: Expression; steps: Step[] }[]; default?: SubFlow };
}

export interface ParallelStep extends StepBase {
  type: "parallel";
  config: { branches: { name: string; steps: Step[] }[]; join?: "all" | "any"; max_concurrency?: number };
}

export interface ForeachStep extends StepBase {
  type: "foreach";
  config: { items: Expression; max_concurrency?: number; max_items?: number; steps: Step[] };
}

export interface WaitStep extends StepBase {
  type: "wait";
  config: { duration: Duration } | { until: Expression };
}

export interface SignalStep extends StepBase {
  type: "signal";
  config: { event: string; correlation: Expression; timeout?: Duration };
}

export interface ApprovalStep extends StepBase {
  type: "approval";
  config: {
    policy?: string;
    role?: string;
    count?: number;
    timeout?: Duration;
    on_timeout?: "reject" | "fail" | `escalate:${string}`;
    subject?: Values;
  };
}

export interface SubflowStep extends StepBase {
  type: "subflow";
  input?: Values;
  config: { workflow: `wf_${string}`; version: number; wait?: boolean };
}

export interface TransformStep extends StepBase {
  type: "transform";
  config: { output: unknown };
}

export interface AiStep extends StepBase {
  type: "ai";
  input?: Values;
  config: { prompt: string; output_schema: object | boolean; model?: string };
}

export type Step =
  | ConnectorStep | CodeStep | HttpStep | BranchStep | ParallelStep | ForeachStep
  | WaitStep | SignalStep | ApprovalStep | SubflowStep | TransformStep | AiStep;

export type StepType = Step["type"];

export interface Trigger {
  type: "webhook" | "schedule" | "connector_event" | "polling" | "database_change" | "manual" | "whatsapp" | "ussd" | "email" | "subflow";
  config?: Record<string, unknown>;
}

export interface WorkflowDefinition {
  schema: "wd/v1";
  id: `wf_${string}`;
  version: number;
  name: string;
  description?: string;
  trigger: Trigger;
  inputs?: { schema?: object | boolean };
  steps: Step[];
  types?: Record<string, object | boolean>;
  settings?: { timeout?: Duration; concurrency_key?: Expression; max_concurrency?: number; retention?: Duration };
}
