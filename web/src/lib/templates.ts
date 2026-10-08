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
}

export interface TemplateLine {
  number: string;
  depth: number;
  text: string;
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
