// The SME template library as the web app sees it (GET /v1/templates,
// docs/templates.md): types, and the parameter form's values.

export interface TemplateParam {
  name: string;
  type: "string" | "text" | "integer" | "number" | "boolean" | "time" | "weekday" | "connection";
  title: string;
  description: string;
  required: boolean;
  default?: unknown;
  example: unknown;
  enum?: string[];
  pattern?: string;
  minimum?: number;
  maximum?: number;
  max_length?: number;
  connector?: string;
  /** For a connection: the capabilities its rail must have (pay_bank); any rail that can is offered. */
  capability?: string[];
  /** How the value is chosen instead of typed (no-technical-steps N8). */
  picker?: "sheet" | "sheet_tab" | "table" | "column" | "role" | "whatsapp_template" | "whatsapp_language" | "slack_channel" | "choice";
  /** The parameter a picker depends on: a tab's sheet, a column's table, a language's template. */
  from?: string;
  /** With picker choice: the connector's picker it lists from ("mailchimp:audiences"). */
  choices?: string;
}

export interface TemplateLine {
  number: string;
  depth: number;
  text: string;
}

export interface TemplateTable {
  name: string;
  title?: string;
  description?: string;
  columns: { name: string; title?: string; type: string; required?: boolean; personal?: boolean }[];
}

export interface Template {
  id: string;
  title: string;
  summary: string;
  description: string;
  category: string;
  tags: string[];
  connectors: string[];
  variables: { name: string; why: string }[];
  params: TemplateParam[];
  steps: TemplateLine[];
  available: boolean;
  /** It starts from a web address another system calls: someone technical sets it up. */
  needs_developer?: boolean;
  /** Data tables the workflow works on; Taskiem makes them when the template is used if they do not exist. */
  tables?: TemplateTable[];
  /** The template that replaces this one, which is no longer offered. */
  superseded_by?: string;
  /** The product it works around (payrolla): offered to businesses that connected it. */
  product?: string;
  /** For a template another workflow can use as a step (decision 0047): its interface. */
  callable?: CallableInterface;
}

/** What a template step gives a callable template, and gets back. */
export interface CallableInterface {
  /** The run inputs, a JSON Schema. */
  inputs: Record<string, unknown>;
  /** The parameters a template step fills. */
  params: string[];
  outputs: { name: string; type: string; title: string; description: string; value: string }[];
  effect: TemplateEffect;
}

/** One of the business's own workflows another may use as a step (decision 0048): its published version's interface, as GET /v1/workflows/callable lists it. */
export interface CallableWorkflow {
  id: string;
  name: string;
  version: number;
  inputs: Record<string, unknown>;
  outputs: CallableInterface["outputs"];
  effect: TemplateEffect;
}

/** What a template does at most, computed from its steps. */
export interface TemplateEffect {
  class: "read" | "write" | "moves_money";
  waits_for_person: boolean;
  waits_for_event: boolean;
  timeout?: string;
}

/** A template's effect in plain words: "Moves money · waits for a person to approve". */
export function effectWords(e: TemplateEffect): string {
  const parts = [e.class === "moves_money" ? "Moves money" : e.class === "write" ? "Changes things in your services" : "Only reads"];
  if (e.waits_for_person) parts.push("waits for a person to approve or answer");
  if (e.waits_for_event) parts.push("waits for something to happen (a payment, a reply)");
  return parts.join(" · ");
}

export const WEEKDAYS = ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"];

/** The form's starting values: each parameter's default, as text. */
export function initialValues(params: TemplateParam[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of params) {
    out[p.name] = p.default === undefined || p.default === null ? "" : String(p.default);
  }
  return out;
}

/** The input a parameter is edited with. */
export function inputKind(p: TemplateParam): "text" | "textarea" | "number" | "time" | "select" | "checkbox" {
  if (p.enum && p.enum.length > 0) return "select";
  switch (p.type) {
    case "text":
      return "textarea";
    case "integer":
    case "number":
      return "number";
    case "time":
      return "time";
    case "weekday":
      return "select";
    case "boolean":
      return "checkbox";
    default:
      return "text";
  }
}

/** Form values to the instantiate request's params: blanks left out (the
 * server applies defaults and names what is missing), numbers as numbers,
 * yes/no as booleans. The server checks every value again. */
export function toParams(params: TemplateParam[], values: Record<string, string>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const p of params) {
    const raw = (values[p.name] ?? "").trim();
    if (raw === "") continue;
    switch (p.type) {
      case "integer":
      case "number": {
        const n = Number(raw);
        out[p.name] = Number.isFinite(n) ? n : raw;
        break;
      }
      case "boolean":
        out[p.name] = raw === "true";
        break;
      default:
        out[p.name] = raw;
    }
  }
  return out;
}

/** Templates grouped by category, categories and templates in order. */
export function byCategory(list: Template[]): [string, Template[]][] {
  const groups = new Map<string, Template[]>();
  for (const t of list) {
    const g = groups.get(t.category) ?? [];
    g.push(t);
    groups.set(t.category, g);
  }
  return [...groups.entries()].sort(([a], [b]) => a.localeCompare(b));
}

/** A plain step line as the gallery shows it ("3a. Text the customer"). */
export function lineLabel(l: TemplateLine): string {
  return l.number ? `${l.number}. ${l.text}` : l.text;
}
