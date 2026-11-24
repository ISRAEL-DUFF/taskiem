// The SME template library as the web app sees it (GET /v1/templates,
// docs/templates.md): types, and the parameter form's values.

export interface TemplateParam {
  name: string;
  /** command: a recipe's tool's word after "/" (made tools §13). */
  type: "command" | "string" | "text" | "integer" | "number" | "boolean" | "time" | "weekday" | "connection";
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
  /** The services it works with that a business connects, each the
   * connectors that serve it, any one of which will do (the books:
   * QuickBooks or Zoho Books): offered once each is connected. */
  services?: string[][];
  /** For a template another workflow can use as a step (decision 0047): its interface. */
  callable?: CallableInterface;
  /** For a recipe (made tools §13): the tool it makes, used from the chat. */
  tool?: RecipeTool;
}

/** The tool a recipe makes, as the library shows it. */
export interface RecipeTool {
  name: string;
  about: string;
  for: "members" | "customers" | "linked";
  icon?: string;
  /** What it says back: text, card, list, or "" when it only says it ran. */
  answer: "text" | "card" | "list" | "";
  /** The word it is called by unless another is chosen. */
  command: string;
  /** How much it asks before it runs, worked out from its steps. */
  care: "answers" | "yes" | "step_up";
  /** The schedule installed beside it, whose button opens the tool. */
  companion?: string;
}

/** What installing a recipe gave (POST /v1/templates/{id}/instantiate's tool). */
export interface RecipeInstalled {
  asked: string;
  command: string;
  changed: boolean;
  companion?: { id: string; name: string };
}

/** A recipe's tool in plain words: who uses it, how much it asks, what it says back. */
export function recipeWords(t: RecipeTool): string[] {
  const out = [t.for === "customers" ? "Your customers use it" : "Your team uses it"];
  out.push(t.care === "answers" ? "Answers at once: it only reads" : t.care === "step_up" ? "Asks yes and a passkey first" : "Asks yes first");
  out.push(t.answer === "card" ? "Says back a card" : t.answer === "list" ? "Says back a list" : t.answer === "text" ? "Says back a sentence" : "Says it ran");
  if (t.companion) out.push(`Comes with "${t.companion}", a schedule whose button opens it`);
  return out;
}

/** A word typed for a tool, as the server writes it: "/Add Sale" is add-sale. */
export function commandOf(typed: string): string {
  return typed.trim().replace(/^\//, "").toLowerCase().split(/\s+/).filter(Boolean).join("-");
}

/** What installing a recipe says: its command, and the next free one when the one asked for was taken. */
export function installedWords(r: RecipeInstalled): string {
  const said = r.changed ? `/${r.asked} is taken, so it is called with /${r.command}.` : `It is called with /${r.command}.`;
  return r.companion ? `${said} Its schedule, ${r.companion.name}, was saved as a draft too.` : said;
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
      case "command":
        out[p.name] = commandOf(raw);
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
  if (l.number === "•") return `• ${l.text}`;
  return l.number ? `${l.number}. ${l.text}` : l.text;
}
