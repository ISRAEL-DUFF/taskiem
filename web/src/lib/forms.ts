// Hosted forms in the app (docs/forms.md, docs/spec/forms.md): the field
// editor's model over an inputs schema (x-form display hints), the widget
// each field is shown as, the deploy checks mirrored for friendly errors
// (engine/wd/form.go is the authority), a tiny show_if evaluator, and
// naira formatting.
import type { Problem } from "../api";

type Json = Record<string, unknown>;

/** Display hints on a schema property (forms 6.2). */
export interface XForm {
  label?: string;
  help?: string;
  placeholder?: string;
  widget?: "textarea" | "consent";
  /** A consent field's text to agree to. */
  text?: string;
  prefill?: boolean;
  hidden?: boolean;
  from?: "member.name" | "member.email";
  accept?: string[];
  max_mb?: number;
  order?: number;
  unit?: "naira";
  [k: string]: unknown;
}

export interface FieldSchema {
  type?: string;
  title?: string;
  description?: string;
  format?: string;
  enum?: unknown[];
  items?: FieldSchema;
  "x-pii"?: string;
  "x-form"?: XForm;
  [k: string]: unknown;
}

export interface FormSchema {
  type?: string;
  required?: string[];
  properties?: Record<string, FieldSchema>;
  [k: string]: unknown;
}

export interface FormPage {
  title?: string;
  fields: string[];
  show_if?: string;
}

export interface FormAfter {
  mode?: "message" | "redirect" | "result";
  message?: string;
  url?: string;
  status?: boolean;
}

export interface FormTriggerConfig {
  title?: string;
  description?: string;
  access?: "public" | "link" | "members";
  roles?: string[];
  verify?: "phone" | "email";
  pages?: FormPage[];
  after?: FormAfter;
  once_per?: "phone" | "email" | "member";
}

export interface FormStepConfig {
  title?: string;
  description?: string;
  to?: { phone?: string; email?: string; role?: string };
  via?: string[];
  verify?: boolean;
  schema?: FormSchema;
  prefill?: Record<string, unknown>;
  timeout?: string;
  remind?: string[];
  on_timeout?: "fail" | "continue";
}

// --- field types ---

export type FieldType =
  | "text"
  | "long_text"
  | "number"
  | "money"
  | "email"
  | "phone"
  | "url"
  | "date"
  | "time"
  | "date_time"
  | "choice"
  | "choices"
  | "yes_no"
  | "consent"
  | "file"
  | "hidden";

export const FIELD_TYPES: [FieldType, string][] = [
  ["text", "Short text"],
  ["long_text", "Long text"],
  ["number", "Number"],
  ["money", "Money (naira)"],
  ["email", "Email"],
  ["phone", "Phone (Nigeria)"],
  ["url", "Web address"],
  ["date", "Date"],
  ["time", "Time"],
  ["date_time", "Date and time"],
  ["choice", "Choice"],
  ["choices", "Several choices"],
  ["yes_no", "Yes or no"],
  ["consent", "Consent"],
  ["file", "File"],
  ["hidden", "Hidden"],
];

const STRING_FORMATS: Partial<Record<FieldType, string>> = { email: "email", phone: "phone-ng", url: "uri", date: "date", time: "time", date_time: "date-time", file: "file" };

/** The editor's type for a schema property. */
export function fieldTypeOf(p: FieldSchema): FieldType {
  const h = p["x-form"] ?? {};
  if (h.hidden === true) return "hidden";
  if (p.format === "file") return "file";
  if (p.type === "boolean") return h.widget === "consent" ? "consent" : "yes_no";
  if (p.type === "array" && p.items?.enum) return "choices";
  if (p.enum) return "choice";
  if (p.type === "integer" || p.type === "number") return h.unit === "naira" ? "money" : "number";
  for (const [t, f] of Object.entries(STRING_FORMATS)) if (p.format === f) return t as FieldType;
  if (h.widget === "textarea") return "long_text";
  return "text";
}

/** Categories offered for personal fields (x-pii). */
export const PII_CATEGORIES: [string, string][] = [
  ["name", "Name"],
  ["phone", "Phone number"],
  ["email", "Email address"],
  ["address", "Address"],
  ["dob", "Date of birth"],
  ["bvn", "BVN"],
  ["nin", "NIN"],
  ["account_number", "Bank account number"],
  ["passport", "Passport number"],
  ["drivers_licence", "Driver's licence"],
];

/** The x-pii category a field's name or type suggests, or "". */
export function suggestPii(name: string, type?: FieldType): string {
  const n = name.toLowerCase();
  const word = (re: string) => new RegExp(`(^|_)(${re})($|_)`).test(n);
  if (word("bvn")) return "bvn";
  if (word("nin")) return "nin";
  if (word("account_?number|account_?no|acct_?no|nuban")) return "account_number";
  if (type === "phone" || word("phone|mobile|msisdn|tel|telephone")) return "phone";
  if (type === "email" || word("e?mail")) return "email";
  if (word("passport")) return "passport";
  if (word("drivers?_?licen[cs]e")) return "drivers_licence";
  if (word("dob|date_of_birth|birthday")) return "dob";
  if (word("address|home_address")) return "address";
  if (word("name|full_name|first_name|last_name|surname|other_names")) return "name";
  return "";
}

// --- the field editor's model ---

export interface FieldDraft {
  name: string;
  label: string;
  type: FieldType;
  required: boolean;
  help: string;
  placeholder: string;
  /** May be set from the link (?name=value). */
  prefill: boolean;
  /** Members forms: filled from the member's account and locked. */
  from: "" | "member.name" | "member.email";
  /** A choice's values (strings, or as the schema had them). */
  choices: unknown[];
  consentText: string;
  accept: string[];
  maxMb?: number;
  pii: string;
  /** Numbers: whole numbers only. */
  integer: boolean;
  /** Which page (index) it is on; undefined: none. */
  page?: number;
  /** Schema keys the editor does not manage (minimum, pattern, ...), kept while the type stays restType. */
  rest: Json;
  restType: FieldType;
  /** x-form keys the editor does not manage (options_from, ...). */
  xrest: Json;
  /** Whether the label is x-form.label (else title), and the help description (else x-form.help). */
  labelInHint: boolean;
  helpInDescription: boolean;
}

const MANAGED = new Set(["type", "title", "description", "format", "enum", "items", "x-pii", "x-form"]);
const MANAGED_X = new Set(["label", "help", "placeholder", "widget", "text", "prefill", "hidden", "from", "accept", "max_mb", "unit"]);

/** A new field of a type, with an x-pii suggestion. */
export function newField(name: string, type: FieldType = "text", label = ""): FieldDraft {
  return {
    name,
    label,
    type,
    required: false,
    help: "",
    placeholder: "",
    prefill: type === "hidden",
    from: "",
    choices: type === "choice" || type === "choices" ? ["Option 1", "Option 2"] : [],
    consentText: "",
    accept: type === "file" ? ["application/pdf", "image/*"] : [],
    pii: suggestPii(name, type),
    integer: type === "money",
    rest: {},
    restType: type,
    xrest: {},
    labelInHint: false,
    helpInDescription: false,
  };
}

/** A field draft of one schema property. */
export function fieldOf(name: string, p: FieldSchema, required: boolean): FieldDraft {
  const h = p["x-form"] ?? {};
  const type = fieldTypeOf(p);
  const rest: Json = {};
  for (const [k, v] of Object.entries(p)) if (!MANAGED.has(k)) rest[k] = v;
  // A hidden field keeps its own type and format.
  if (type === "hidden") {
    if (p.type !== undefined) rest.type = p.type;
    if (p.format !== undefined) rest.format = p.format;
    if (p.enum !== undefined) rest.enum = p.enum;
    if (p.items !== undefined) rest.items = p.items;
  }
  const xrest: Json = {};
  for (const [k, v] of Object.entries(h)) if (!MANAGED_X.has(k)) xrest[k] = v;
  return {
    name,
    label: h.label ?? p.title ?? "",
    type,
    required,
    help: h.help ?? p.description ?? "",
    placeholder: h.placeholder ?? "",
    prefill: h.prefill === true,
    from: h.from ?? "",
    choices: (type === "choices" ? p.items?.enum : p.enum) ?? [],
    consentText: h.text ?? "",
    accept: Array.isArray(h.accept) ? h.accept : [],
    maxMb: typeof h.max_mb === "number" ? h.max_mb : undefined,
    pii: p["x-pii"] ?? "",
    integer: p.type === "integer",
    rest,
    restType: type,
    xrest,
    labelInHint: h.label !== undefined,
    helpInDescription: h.help === undefined && p.description !== undefined,
  };
}

/** The schema property a field draft writes (x-form.order aside). */
export function propertyOf(f: FieldDraft): FieldSchema {
  const keep = f.type === f.restType ? f.rest : {};
  const p: FieldSchema = {};
  const x: XForm = { ...(f.type === f.restType ? f.xrest : {}) };
  switch (f.type) {
    case "text":
      p.type = "string";
      break;
    case "long_text":
      p.type = "string";
      x.widget = "textarea";
      break;
    case "number":
      p.type = f.integer ? "integer" : "number";
      break;
    case "money":
      p.type = f.integer ? "integer" : "number";
      x.unit = "naira";
      break;
    case "choice":
      p.type = "string";
      if (f.choices.some((c) => typeof c !== "string")) delete p.type;
      p.enum = f.choices;
      break;
    case "choices":
      p.type = "array";
      p.items = { type: f.choices.some((c) => typeof c !== "string") ? undefined : "string", enum: f.choices };
      if (p.items.type === undefined) delete p.items.type;
      break;
    case "yes_no":
      p.type = "boolean";
      break;
    case "consent":
      p.type = "boolean";
      x.widget = "consent";
      if (f.consentText) x.text = f.consentText;
      break;
    case "file":
      p.type = "string";
      p.format = "file";
      if (f.accept.length) x.accept = f.accept;
      if (f.maxMb !== undefined) x.max_mb = f.maxMb;
      break;
    case "hidden":
      x.hidden = true;
      break;
    default:
      p.type = "string";
      p.format = STRING_FORMATS[f.type];
  }
  const out: FieldSchema = { ...p, ...keep };
  if (f.type === "hidden" && out.type === undefined) out.type = "string";
  if (f.label) {
    if (f.labelInHint) x.label = f.label;
    else out.title = f.label;
  }
  if (f.help) {
    if (f.helpInDescription) out.description = f.help;
    else x.help = f.help;
  }
  if (f.placeholder) x.placeholder = f.placeholder;
  if (f.prefill) x.prefill = true;
  if (f.from) x.from = f.from;
  if (f.pii) out["x-pii"] = f.pii;
  if (Object.keys(x).length) out["x-form"] = x;
  return out;
}

/**
 * A schema's properties in the order the platform asks them (mirrors
 * wd.SchemaOrder): x-form.order when given, then required ones, then the
 * rest by name.
 */
export function schemaOrder(schema: FormSchema | undefined): string[] {
  const props = schema?.properties ?? {};
  const req = new Set(schema?.required ?? []);
  const order = (k: string) => {
    const o = props[k]?.["x-form"]?.order;
    return typeof o === "number" ? Math.trunc(o) : undefined;
  };
  return Object.keys(props).sort((a, b) => {
    const oa = order(a);
    const ob = order(b);
    if (oa !== undefined && ob !== undefined && oa !== ob) return oa - ob;
    if ((oa !== undefined) !== (ob !== undefined)) return oa !== undefined ? -1 : 1;
    if (req.has(a) !== req.has(b)) return req.has(a) ? -1 : 1;
    return a < b ? -1 : a > b ? 1 : 0;
  });
}

export interface PageDraft {
  title: string;
  show_if: string;
}

/** The editor's fields (and pages) of a schema and a form's pages: fields in the order asked, page by page. */
export function loadFields(schema: FormSchema | undefined, pages?: FormPage[]): { fields: FieldDraft[]; pages: PageDraft[] } {
  const props = schema?.properties ?? {};
  const req = new Set(schema?.required ?? []);
  const pageOf = new Map<string, number>();
  const ordered: string[] = [];
  (pages ?? []).forEach((pg, i) => {
    for (const n of pg.fields ?? []) {
      if (!(n in props) || pageOf.has(n)) continue;
      pageOf.set(n, i);
      ordered.push(n);
    }
  });
  for (const n of schemaOrder(schema)) if (!pageOf.has(n)) ordered.push(n);
  const fields = ordered.map((n) => {
    const f = fieldOf(n, props[n] ?? {}, req.has(n));
    f.page = pageOf.get(n);
    return f;
  });
  return { fields, pages: (pages ?? []).map((p) => ({ title: p.title ?? "", show_if: p.show_if ?? "" })) };
}

/** The schema (and pages) the editor's fields write; base keeps the schema's other keys. */
export function saveFields(fields: FieldDraft[], base?: FormSchema, pageDrafts?: PageDraft[]): { schema: FormSchema; pages?: FormPage[] } {
  const rest: Json = { ...(base ?? {}) };
  delete rest.properties;
  delete rest.required;
  const properties: Record<string, FieldSchema> = {};
  for (const f of fields) properties[f.name] = propertyOf(f);
  // Required in the order the schema had it, new ones after.
  const want = fields.filter((f) => f.required).map((f) => f.name);
  const required = [...(base?.required ?? []).filter((n) => want.includes(n)), ...want.filter((n) => !(base?.required ?? []).includes(n))];
  const schema: FormSchema = { ...rest, type: "object", ...(required.length && { required }), properties };
  // Pages list their fields in the editor's order.
  const pages = pageDrafts?.length
    ? pageDrafts.map((p, i) => {
        const pg: FormPage = { fields: fields.filter((f) => f.page === i && f.type !== "hidden").map((f) => f.name) };
        if (p.title) pg.title = p.title;
        if (p.show_if.trim()) pg.show_if = p.show_if.trim();
        return pg;
      })
    : undefined;
  // x-form.order only when the schema's own order is not the editor's
  // (pages, when they hold every field, set the order themselves).
  if (!(pages && pagesOnly(pages, fields))) {
    const names = fields.map((f) => f.name);
    if (schemaOrder(schema).join("\0") !== names.join("\0")) {
      fields.forEach((f, i) => {
        const p = properties[f.name] as FieldSchema;
        p["x-form"] = { ...(p["x-form"] ?? {}), order: i + 1 };
      });
    }
  }
  return { schema, pages };
}

// Every non-hidden field is on a page: their order is the pages'.
function pagesOnly(pages: FormPage[], fields: FieldDraft[]): boolean {
  return fields.every((f) => f.type === "hidden" || pages.some((p) => p.fields.includes(f.name)));
}

/** A field name from a label: lower-case words joined by _, unique among taken. */
export function nameFromLabel(label: string, taken: string[]): string {
  let base = label
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .slice(0, 40);
  if (!base || !/^[a-z]/.test(base)) base = "field" + (base ? "_" + base : "");
  if (!taken.includes(base)) return base;
  for (let i = 2; ; i++) if (!taken.includes(`${base}_${i}`)) return `${base}_${i}`;
}

export const FIELD_NAME = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/;

// --- widgets ---

export type Widget =
  | "text"
  | "textarea"
  | "number"
  | "money"
  | "email"
  | "tel"
  | "url"
  | "date"
  | "time"
  | "datetime-local"
  | "radio"
  | "select"
  | "checkboxes"
  | "checkbox"
  | "consent"
  | "file"
  | "hidden";

/** How a field is shown, as on the form host: up to 5 choices are radio buttons, more a list. */
export function widgetOf(p: FieldSchema): Widget {
  switch (fieldTypeOf(p)) {
    case "long_text":
      return "textarea";
    case "number":
      return "number";
    case "money":
      return "money";
    case "email":
      return "email";
    case "phone":
      return "tel";
    case "url":
      return "url";
    case "date":
      return "date";
    case "time":
      return "time";
    case "date_time":
      return "datetime-local";
    case "choice":
      return (p.enum?.length ?? 0) > 5 ? "select" : "radio";
    case "choices":
      return "checkboxes";
    case "yes_no":
      return "checkbox";
    case "consent":
      return "consent";
    case "file":
      return "file";
    case "hidden":
      return "hidden";
  }
  return "text";
}

/** A field's label: x-form.label, the title, or the name made readable. */
export function labelOf(name: string, p: FieldSchema): string {
  const l = p["x-form"]?.label ?? p.title;
  if (l) return l;
  const s = name.replace(/_/g, " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** A form's pages: its own, or one page with every field that is not hidden. */
export function pagesOf(pages: FormPage[] | undefined | null, schema: FormSchema | undefined): FormPage[] {
  const props = schema?.properties ?? {};
  if (pages?.length) return pages.map((p) => ({ ...p, fields: p.fields.filter((n) => n in props) }));
  return [{ fields: schemaOrder(schema).filter((n) => props[n]?.["x-form"]?.hidden !== true) }];
}

/** Fields on pages before page i: what its show_if may read. */
export function earlierFields(pages: FormPage[], i: number): string[] {
  return pages.slice(0, i).flatMap((p) => p.fields);
}

// --- naira ---

function group(digits: string): string {
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/** ₦1,234,567 (whole naira), ₦1,234.50 with kobo. */
export function formatNaira(n: number, sign = true): string {
  if (!Number.isFinite(n)) return "";
  const neg = n < 0;
  const [whole = "0", frac] = Math.abs(n).toFixed(2).split(".");
  const out = group(whole) + (frac && frac !== "00" ? "." + frac : "");
  return (neg ? "-" : "") + (sign ? "₦" : "") + out;
}

/** The amount in typed text ("₦1,234.50", "1 234"); undefined when empty, NaN when not a number. */
export function parseNaira(text: string): number | undefined {
  const t = text.replace(/[₦,\s]/g, "").replace(/^NGN/i, "");
  if (t === "") return undefined;
  return /^-?\d+(\.\d{1,2})?$/.test(t) ? Number(t) : NaN;
}

// --- answers ---

const PHONE_NG = /^(\+?234|0)?[789]\d{9}$/;
const EMAIL = /^[^@\s]+@[^@\s]+\.[A-Za-z]{2,}$/;

/** A local date-time (from an input) as RFC 3339 with the browser's offset. */
export function localToRFC3339(local: string, offsetMinutes = new Date(local).getTimezoneOffset()): string {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/.test(local)) return local;
  const base = local.length === 16 ? local + ":00" : local;
  const off = -offsetMinutes;
  const sign = off >= 0 ? "+" : "-";
  const a = Math.abs(off);
  return `${base}${sign}${String(Math.floor(a / 60)).padStart(2, "0")}:${String(a % 60).padStart(2, "0")}`;
}

/** What a field's raw input (text, a checkbox, choices) sends: undefined for nothing. */
export function coerceAnswer(p: FieldSchema, raw: unknown): unknown {
  if (raw === undefined || raw === null || raw === "") return undefined;
  const w = widgetOf(p);
  if (typeof raw !== "string") return Array.isArray(raw) && raw.length === 0 ? undefined : raw;
  switch (w) {
    case "number": {
      const n = Number(raw.replace(/,/g, ""));
      return raw.trim() === "" ? undefined : Number.isFinite(n) ? n : raw;
    }
    case "money": {
      const n = parseNaira(raw);
      return n === undefined ? undefined : Number.isNaN(n) ? raw : n;
    }
    case "tel":
      return raw.replace(/[\s()-]/g, "");
    case "datetime-local":
      return localToRFC3339(raw);
    default:
      return p.type === "integer" || p.type === "number" ? (Number.isFinite(Number(raw)) ? Number(raw) : raw) : raw;
  }
}

/** A friendly problem with one answer, or "". */
export function answerProblem(p: FieldSchema, value: unknown, required: boolean): string {
  const w = widgetOf(p);
  const empty = value === undefined || (Array.isArray(value) && value.length === 0) || ((w === "checkbox" || w === "consent") && value !== true && required);
  if (empty) {
    if (!required) return "";
    return w === "consent" ? "Tick the box to agree." : "This question needs an answer.";
  }
  if (w === "number" || w === "money") {
    if (typeof value !== "number") return "Enter a number.";
    if (p.type === "integer" && !Number.isInteger(value)) return w === "money" ? "Enter whole naira." : "Enter a whole number.";
    if (typeof p.minimum === "number" && value < p.minimum) return `At least ${w === "money" ? formatNaira(p.minimum) : p.minimum}.`;
    if (typeof p.maximum === "number" && value > p.maximum) return `At most ${w === "money" ? formatNaira(p.maximum) : p.maximum}.`;
  }
  if (typeof value === "string") {
    if (w === "email" && !EMAIL.test(value)) return "Enter an email address, like name@example.com.";
    if (w === "tel" && !PHONE_NG.test(value)) return "Enter a Nigerian phone number, like 0803 123 4567.";
    if (w === "url" && !/^https?:\/\/\S+\.\S+/.test(value)) return "Enter a web address starting with https://.";
    if (typeof p.maxLength === "number" && value.length > p.maxLength) return `At most ${p.maxLength} characters.`;
    if (typeof p.minLength === "number" && value.length < p.minLength) return `At least ${p.minLength} characters.`;
    if (typeof p.pattern === "string") {
      try {
        if (!new RegExp(p.pattern).test(value)) return p["x-form"]?.help ? `Check the format: ${p["x-form"].help}` : "Check the format of this answer.";
      } catch {
        // a pattern JavaScript cannot read: the server checks it
      }
    }
  }
  return "";
}

/** The answers (coerced) of a raw answer set. */
export function coerceAnswers(schema: FormSchema | undefined, raw: Record<string, unknown>): Record<string, unknown> {
  const props = schema?.properties ?? {};
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(raw)) {
    const p = props[k];
    if (!p) continue;
    const c = coerceAnswer(p, v);
    if (c !== undefined) out[k] = c;
  }
  return out;
}

/** The pages shown for these answers: a show_if the evaluator cannot read shows its page. */
export function shownPages(pages: FormPage[], answers: Record<string, unknown>): number[] {
  const out: number[] = [];
  pages.forEach((p, i) => {
    if (i === 0 || !p.show_if?.trim()) return out.push(i);
    if (evalShowIf(p.show_if, answers) !== false) out.push(i);
  });
  return out;
}

/** The answers to send: fields on shown pages, and hidden fields, with values. */
export function answersToSend(schema: FormSchema | undefined, pages: FormPage[], raw: Record<string, unknown>): Record<string, unknown> {
  const answers = coerceAnswers(schema, raw);
  const shown = new Set(shownPages(pages, answers).flatMap((i) => pages[i]?.fields ?? []));
  const props = schema?.properties ?? {};
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(answers)) if (shown.has(k) || props[k]?.["x-form"]?.hidden === true) out[k] = v;
  return out;
}

// --- show_if: a tiny evaluator ---

type Tok = { t: "num" | "str" | "id" | "op" | "kw"; v: string };

function tokens(src: string): Tok[] {
  const out: Tok[] = [];
  const re = /\s*(?:(\d+(?:\.\d+)?)|"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|(==|!=|<=|>=|&&|\|\||[<>!()[\],.])|([A-Za-z_][A-Za-z0-9_]*))/y;
  let i = 0;
  const s = src.trim();
  while (i < s.length) {
    re.lastIndex = i;
    const m = re.exec(s);
    if (!m || m[0].length === 0) throw new Error("unreadable");
    i = re.lastIndex;
    if (m[1] !== undefined) out.push({ t: "num", v: m[1] });
    else if (m[2] !== undefined) out.push({ t: "str", v: m[2].replace(/\\(.)/g, "$1") });
    else if (m[3] !== undefined) out.push({ t: "str", v: m[3].replace(/\\(.)/g, "$1") });
    else if (m[4] !== undefined) out.push({ t: "op", v: m[4] });
    else if (m[5] !== undefined) out.push({ t: ["true", "false", "null", "in"].includes(m[5]) ? "kw" : "id", v: m[5] });
  }
  return out;
}

/**
 * Evaluates a simple show_if over the answers: answers.x (or answers["x"])
 * compared with == != < <= > >= to numbers, strings, true, false or null,
 * `in [..]`, joined by && || ! and brackets. Returns undefined for anything
 * beyond that (the page is then shown; the server decides).
 */
export function evalShowIf(src: string, answers: Record<string, unknown>): boolean | undefined {
  const s = src.trim().replace(/^=/, "");
  if (s === "") return true;
  let toks: Tok[];
  try {
    toks = tokens(s);
  } catch {
    return undefined;
  }
  let i = 0;
  const peek = () => toks[i];
  const eat = (v: string) => {
    if (toks[i]?.v === v && toks[i]?.t !== "str") {
      i++;
      return true;
    }
    return false;
  };
  const fail = (): never => {
    throw new Error("beyond");
  };
  const primary = (): unknown => {
    const t = toks[i++];
    if (!t) return fail();
    if (t.t === "num") return Number(t.v);
    if (t.t === "str") return t.v;
    if (t.t === "kw") return t.v === "true" ? true : t.v === "false" ? false : t.v === "null" ? null : fail();
    if (t.t === "op" && t.v === "(") {
      const v = or();
      if (!eat(")")) fail();
      return v;
    }
    if (t.t === "op" && t.v === "[") {
      const list: unknown[] = [];
      if (!eat("]")) {
        do list.push(primary());
        while (eat(","));
        if (!eat("]")) fail();
      }
      return list;
    }
    if (t.t === "id" && t.v === "answers") {
      let key: string | undefined;
      if (eat(".")) {
        const n = toks[i++];
        if (n?.t !== "id" && n?.t !== "kw") fail();
        key = n?.v;
      } else if (eat("[")) {
        const n = toks[i++];
        if (n?.t !== "str") fail();
        key = n?.v;
        if (!eat("]")) fail();
      } else fail();
      if (peek()?.v === "." || peek()?.v === "(") fail(); // methods, nested fields
      const v = answers[key as string];
      return v === undefined ? null : v;
    }
    return fail();
  };
  const unary = (): unknown => {
    if (eat("!")) {
      const v = unary();
      if (typeof v !== "boolean") fail();
      return !v;
    }
    return primary();
  };
  const cmp = (): unknown => {
    const a = unary();
    const op = peek();
    if (op && (op.t === "op" || op.t === "kw") && ["==", "!=", "<", "<=", ">", ">=", "in"].includes(op.v)) {
      i++;
      const b = unary();
      switch (op.v) {
        case "==":
          return same(a, b);
        case "!=":
          return !same(a, b);
        case "in":
          return Array.isArray(b) ? b.some((x) => same(a, x)) : fail();
      }
      if (typeof a !== "number" || typeof b !== "number") {
        if (a === null || b === null) return false;
        if (typeof a !== "string" || typeof b !== "string") fail();
      }
      const x = a as number | string;
      const y = b as number | string;
      return op.v === "<" ? x < y : op.v === "<=" ? x <= y : op.v === ">" ? x > y : x >= y;
    }
    return a;
  };
  const and = (): unknown => {
    let v = cmp();
    while (eat("&&")) {
      const w = cmp();
      if (typeof v !== "boolean" || typeof w !== "boolean") fail();
      v = (v as boolean) && (w as boolean);
    }
    return v;
  };
  const or = (): unknown => {
    let v = and();
    while (eat("||")) {
      const w = and();
      if (typeof v !== "boolean" || typeof w !== "boolean") fail();
      v = (v as boolean) || (w as boolean);
    }
    return v;
  };
  try {
    const v = or();
    if (i !== toks.length || typeof v !== "boolean") return undefined;
    return v;
  } catch {
    return undefined;
  }
}

function same(a: unknown, b: unknown): boolean {
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((x, i) => same(x, b[i]));
  return a === b;
}

/** The answers a show_if reads (answers.x, answers["x"]). */
export function answerRefs(src: string): string[] {
  const out: string[] = [];
  for (const m of src.matchAll(/answers(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[["']([^"']+)["']\])/g)) out.push(m[1] ?? m[2] ?? "");
  return out;
}

// --- the deploy checks, mirrored (engine/wd/form.go) ---

const REFUSED_NAME = /(^|_)(password|passwd|passcode|pin|cvv|cvc|otp|token|secret)($|_)|card_?number|card_?no|pan$|internet_?banking|bank_?login/i;
const REFUSED_PII = ["card", "card_number", "cvv", "pin", "password", "otp", "credential", "bank_login"];
const SENSITIVE_PII = ["bvn", "nin", "account_number", "passport", "drivers_licence"];
const SENSITIVE_NAME = /(^|_)(bvn|nin|account_?number|acct_?no|nuban)($|_)/i;

/** Whether a field name is one a form never asks for. */
export const neverCollected = (name: string, pii = "") => REFUSED_NAME.test(name) || REFUSED_PII.includes(pii);

/** Problems with a form's fields (FormFieldProblems): anonymous is a public form without verify. */
export function fieldProblems(schema: FormSchema | undefined, anonymous: boolean, path: string): Problem[] {
  const out: Problem[] = [];
  const props = schema?.properties ?? {};
  for (const name of schemaOrder(schema)) {
    const p = props[name] ?? {};
    const at = `${path}/properties/${name}`;
    const cat = p["x-pii"] ?? "";
    if (neverCollected(name, cat)) {
      out.push({ path: at, message: `a form never asks for passwords, PINs, card details or one-time codes ("${name}")` });
      continue;
    }
    if (SENSITIVE_PII.includes(cat) || SENSITIVE_NAME.test(name)) {
      if (cat === "") out.push({ path: at, message: `"${name}" looks like an identifier: mark it with its x-pii category so it is sealed` });
      else if (anonymous) out.push({ path: at, message: `"${name}" may be asked only on a link or members form, or a public form with verify on` });
    }
    if (p.type === "object" || (p.type === "array" && !p.items?.enum)) {
      out.push({ path: at, message: `"${name}": nested fields and lists are not in forms yet; use plain fields or a list of choices` });
    }
    if (p.format === "file") {
      const mb = p["x-form"]?.max_mb;
      if (typeof mb === "number" && (mb < 1 || mb > 25)) out.push({ path: `${at}/x-form/max_mb`, message: "a file is at most 25 MB" });
    }
    if ((p.enum && p.enum.length === 0) || (p.type === "array" && p.items?.enum?.length === 0)) {
      out.push({ path: at, message: `"${name}" needs at least one choice` });
    }
  }
  return out;
}

/** Problems with a form trigger (checkFormTrigger), at the validator's paths. */
export function formProblems(cfg: FormTriggerConfig, schema: FormSchema | undefined): Problem[] {
  const out: Problem[] = [];
  const add = (path: string, message: string) => out.push({ path: "/trigger/config" + path, message });
  const access = cfg.access ?? "public";
  if (!cfg.title?.trim()) add("/title", "a form needs a title");
  if (cfg.verify && access === "members") add("/verify", "verify is phone or email, and members forms need none");
  if (cfg.after?.mode === "redirect" && !cfg.after.url?.startsWith("https://")) add("/after/url", "a redirect needs an https URL on one of the organisation's verified websites");
  const props = schema?.properties ?? {};
  if (Object.keys(props).length === 0) add("", "a form needs fields: give the workflow an inputs schema");
  const seen = new Map<string, number>();
  const pages = pagesOf(cfg.pages, schema);
  const raw = cfg.pages?.length ? cfg.pages : pages;
  raw.forEach((pg, i) => {
    for (const name of pg.fields ?? []) {
      if (!(name in props)) {
        add(`/pages/${i}/fields`, `"${name}" is not in the inputs schema`);
        continue;
      }
      const j = seen.get(name);
      if (j !== undefined) add(`/pages/${i}/fields`, `"${name}" is on page ${j + 1} already`);
      seen.set(name, i);
    }
    const c = (pg.show_if ?? "").trim().replace(/^=/, "");
    if (c) {
      if (i === 0) add(`/pages/${i}/show_if`, "the first page is always shown");
      else {
        const earlier = earlierFields(raw, i);
        for (const r of answerRefs(c)) if (!earlier.includes(r)) add(`/pages/${i}/show_if`, `the condition reads "${r}", which is not on an earlier page`);
      }
    }
  });
  for (const name of Object.keys(props)) {
    if (!seen.has(name) && props[name]?.["x-form"]?.hidden !== true) add("/pages", `"${name}" is on no page (put it on one, or hide it)`);
  }
  out.push(...fieldProblems(schema, access === "public" && !cfg.verify, "/inputs/schema"));
  return out;
}

const DURATION = /^(\d+(ms|s|m|h|d))+$/;

/** A wd/v1 duration in milliseconds ("72h", "1d12h"), or undefined. */
export function parseDuration(s: string): number | undefined {
  if (!DURATION.test(s)) return undefined;
  const unit: Record<string, number> = { ms: 1, s: 1000, m: 60_000, h: 3_600_000, d: 86_400_000 };
  let total = 0;
  for (const m of s.matchAll(/(\d+)(ms|s|m|h|d)/g)) total += Number(m[1]) * (unit[m[2] as string] ?? 0);
  return total;
}

/** Problems with a form step's config (checkFormStep), at paths under path. */
export function stepProblems(cfg: FormStepConfig, path = ""): Problem[] {
  const out: Problem[] = [];
  const add = (sub: string, message: string) => out.push({ path: path + sub, message });
  if (!cfg.title?.trim()) add("/title", "a form step needs a title");
  const to = cfg.to ?? {};
  const set = [to.phone, to.email, to.role].filter((v) => v !== undefined && v !== "");
  if (set.length !== 1) add("/to", "to is exactly one of phone, email or role");
  for (const v of cfg.via ?? []) if (v !== "sms" && v !== "email") add("/via", "via is sms and email");
  if (cfg.verify && to.role) add("/verify", "members are signed in: verify is for a phone or email");
  let timeout = 7 * 86_400_000;
  if (cfg.timeout) {
    const d = parseDuration(cfg.timeout);
    if (d === undefined) add("/timeout", "a duration, like 72h or 7d");
    else timeout = d;
  }
  (cfg.remind ?? []).forEach((r, i) => {
    const d = parseDuration(r);
    if (d === undefined || d <= 0 || d >= timeout) add(`/remind/${i}`, "a reminder is a duration shorter than the timeout");
  });
  if (Object.keys(cfg.schema?.properties ?? {}).length === 0) add("/schema", "a form step needs questions: a schema with properties");
  out.push(...fieldProblems(cfg.schema, false, path + "/schema"));
  return out;
}

/** Problems from both lists, each once. */
export function mergeProblems(a: Problem[], b: Problem[]): Problem[] {
  const seen = new Set<string>();
  return [...a, ...b].filter((p) => {
    const k = p.path + "\0" + p.message;
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
}

/** A masked answer from the API ({masked, category}) or a sealed envelope, shown; else the value as text. */
export function showAnswer(v: unknown, p?: FieldSchema): string {
  if (v === undefined || v === null) return "—";
  if (typeof v === "object" && !Array.isArray(v)) {
    const o = v as Json;
    if ("masked" in o) return `${String(o.masked)} (${String(o.category ?? "personal")})`;
    if ("$pii" in o) return `••• (${String(o.$pii)})`;
    if ("name" in o && "size" in o) return `${String(o.name)} (${Math.max(1, Math.round(Number(o.size) / 1024))} KB)`;
    return JSON.stringify(v);
  }
  if (Array.isArray(v)) return v.map((x) => showAnswer(x)).join(", ");
  if (typeof v === "boolean") return v ? "Yes" : "No";
  if (typeof v === "number" && p && fieldTypeOf(p) === "money") return formatNaira(v);
  return String(v);
}

/** Masks a phone (+234 803 *** 4567) or an email (a***@example.com) for display. */
export function maskContact(v: string): string {
  if (v.includes("@")) {
    const [u = "", d = ""] = v.split("@");
    return `${u.slice(0, 1)}***@${d}`;
  }
  return v.length > 6 ? `${v.slice(0, v.length - 7)}***${v.slice(-4)}` : "***";
}

/** Links (one per line) as CSV: url, to, expires_at. */
export function linksCSV(links: { url: string; to?: string; expires_at: string }[]): string {
  const q = (s: string) => (/[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s);
  return ["url,to,expires_at", ...links.map((l) => [l.url, l.to ?? "", l.expires_at].map(q).join(","))].join("\n") + "\n";
}

/** Recipients pasted one per line (or comma-separated): phones and emails. */
export function parseRecipients(text: string): { to: ({ phone: string } | { email: string })[]; bad: string[] } {
  const to: ({ phone: string } | { email: string })[] = [];
  const bad: string[] = [];
  for (const raw of text.split(/[\n,;]+/)) {
    const s = raw.trim();
    if (!s) continue;
    if (s.includes("@")) {
      if (EMAIL.test(s)) to.push({ email: s });
      else bad.push(s);
      continue;
    }
    const p = s.replace(/[\s()-]/g, "");
    if (PHONE_NG.test(p)) to.push({ phone: p });
    else bad.push(s);
  }
  return { to, bad };
}

/** The frame to paste on a website. */
export function embedSnippet(url: string, title: string): string {
  const t = title.replace(/"/g, "&quot;");
  return `<iframe src="${url}" title="${t}" style="width:100%;min-height:640px;border:0" loading="lazy"></iframe>`;
}
