import { get, post } from "../api";
import type { Table } from "../lib/tables";
import { useLoad } from "../ui";
import type { Sheet } from "./SheetChooser";

// Choosers for a template's details (no-technical-steps N8): a sheet's tab
// by its name, a table and its columns, and a WhatsApp template by what it
// says. Each lists what the business has and falls back to typing when it
// cannot be read (not connected yet, or nothing there).

/** A WhatsApp template as GET /v1/whatsapp/templates lists it. */
export interface WhatsAppTemplate {
  name: string;
  language: string;
  category: string;
  body: string;
  variables: number;
}

/** WhatsApp's languages a Nigerian business's templates are most often in. */
export const TEMPLATE_LANGUAGES: [string, string][] = [
  ["en", "English"],
  ["en_GB", "English (UK)"],
  ["en_US", "English (US)"],
  ["ha", "Hausa"],
  ["yo", "Yoruba"],
  ["ig", "Igbo"],
  ["fr", "French"],
];

/** A language code in words ("en_GB" is English (UK)). */
export function languageName(code: string): string {
  return TEMPLATE_LANGUAGES.find(([c]) => c === code)?.[1] ?? code;
}

/** A template's words with its values shown as blanks: "Hello …, you owe …". */
export function templateWords(body: string): string {
  return body.replace(/\{\{\s*\d+\s*\}\}/g, "…");
}

/** The key a template is picked by: its name and language. */
export const templateKey = (t: { name: string; language: string }) => `${t.name}|${t.language}`;

function Typed({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (v: string) => void; placeholder?: string }) {
  return <input aria-label={label} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />;
}

/** A tab of the chosen spreadsheet, by its name. */
export function TabPicker({ label, sheet, value, onChange }: { label: string; sheet: string; value: string; onChange: (v: string) => void }) {
  const tabs = useLoad(() => (sheet ? post<Sheet>("/v1/sheets/resolve", { link: sheet }).then((s) => s.tabs) : Promise.resolve<string[]>([])), [sheet]);
  const list = tabs.data ?? [];
  if (list.length === 0) return <Typed label={label} value={value} onChange={onChange} placeholder={sheet ? "The tab's name" : "Choose the spreadsheet first"} />;
  return (
    <select aria-label={label} value={list.includes(value) ? value : ""} onChange={(e) => onChange(e.target.value)}>
      <option value="">Choose a tab…</option>
      {list.map((t) => (
        <option key={t} value={t}>
          {t}
        </option>
      ))}
    </select>
  );
}

function useTables() {
  return useLoad(() => get<{ tables: Table[] }>("/v1/tables").then((r) => r.tables), []);
}

/** One of the business's tables. */
export function TablePicker({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  const tables = useTables();
  const list = tables.data ?? [];
  if (list.length === 0) return <Typed label={label} value={value} onChange={onChange} placeholder="The table's name" />;
  return (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">Choose a table…</option>
      {!list.some((t) => t.name === value) && value && <option value={value}>{value} (not made yet)</option>}
      {list.map((t) => (
        <option key={t.id} value={t.name}>
          {t.title || t.name}
        </option>
      ))}
    </select>
  );
}

/** A column of the chosen table. */
export function ColumnPicker({ label, table, value, onChange }: { label: string; table: string; value: string; onChange: (v: string) => void }) {
  const tables = useTables();
  const cols = tables.data?.find((t) => t.name === table)?.columns ?? [];
  if (cols.length === 0) return <Typed label={label} value={value} onChange={onChange} placeholder={table ? "The column's name" : "Choose the table first"} />;
  return (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">Choose a column…</option>
      {cols.map((c) => (
        <option key={c.name} value={c.name}>
          {c.title || c.name}
        </option>
      ))}
    </select>
  );
}

/**
 * One of the business's approved WhatsApp templates, chosen by what it
 * says; onTemplate gets its language too.
 */
export function WhatsAppTemplatePicker({
  label,
  value,
  language,
  onChange,
}: {
  label: string;
  value: string;
  language?: string;
  onChange: (name: string, language: string) => void;
}) {
  const list = useLoad(() => get<{ templates: WhatsAppTemplate[] }>("/v1/whatsapp/templates").then((r) => r.templates), []);
  const ts = list.data ?? [];
  if (ts.length === 0) {
    return (
      <>
        <Typed label={label} value={value} onChange={(v) => onChange(v, language ?? "")} placeholder="The template's name" />
        {list.error !== undefined && (
          <span className="hint" role="note">
            {" "}
            {list.error instanceof Error ? list.error.message : "Your templates could not be read."} You can type its name instead.
          </span>
        )}
      </>
    );
  }
  const current = ts.find((t) => t.name === value && (!language || t.language === language)) ?? ts.find((t) => t.name === value);
  return (
    <>
      <select
        aria-label={label}
        value={current ? templateKey(current) : ""}
        onChange={(e) => {
          const t = ts.find((x) => templateKey(x) === e.target.value);
          if (t) onChange(t.name, t.language);
        }}
      >
        <option value="">Choose a template…</option>
        {ts.map((t) => (
          <option key={templateKey(t)} value={templateKey(t)}>
            {templateWords(t.body) || t.name} ({languageName(t.language)})
          </option>
        ))}
      </select>
      {current && <span className="hint"> It says: “{templateWords(current.body)}”</span>}
    </>
  );
}

/** A template's language, in words. */
export function LanguagePicker({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  const known = TEMPLATE_LANGUAGES.some(([c]) => c === value);
  return (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)}>
      {!known && value && <option value={value}>{value}</option>}
      {TEMPLATE_LANGUAGES.map(([c, n]) => (
        <option key={c} value={c}>
          {n}
        </option>
      ))}
    </select>
  );
}
