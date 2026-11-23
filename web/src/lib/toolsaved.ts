// Saved things as a tool's inputs (made tools M1: "a tool's input of a
// type a saved kind has … offers the person's and the business's Saved
// objects of that kind in its form"). Which kind an input takes: its
// x-saved hint when it has one, else its format, else its name and title.
// Only a text input is offered one (not a choice, a number or a yes/no).

/** What a Saved button can fill: as SavedFill's kinds, and an email (a
 * saved person's). */
export type ToolSavedKind = "person" | "place" | "phone" | "email" | "bank_account" | "date";

const KINDS: ToolSavedKind[] = ["person", "place", "phone", "email", "bank_account", "date"];

const FORMATS: Record<string, ToolSavedKind> = {
  phone: "phone",
  "phone-ng": "phone",
  tel: "phone",
  email: "email",
  date: "date",
};

// By the words of its name and title, in this order: a phone before a
// person ("customer phone"), an account before a person.
const WORDS: [RegExp, ToolSavedKind][] = [
  [/\b(phone|tel|telephone|mobile|whatsapp)\b/, "phone"],
  [/\b(e ?mail)\b/, "email"],
  [/\b(account number|bank account|account no|nuban)\b/, "bank_account"],
  [/\b(address|location|deliver to|delivery address|branch|place|site)\b/, "place"],
  [/\b(date|day|due|birthday|deadline)\b/, "date"],
  [/\b(customer|person|contact|recipient|client|supplier)\b/, "person"],
];

/** The saved kind an input of a tool's schema takes, or none. */
export function toolSavedKind(name: string, prop: Record<string, unknown> | undefined): ToolSavedKind | undefined {
  if (!prop) return undefined;
  const hint = prop["x-saved"];
  if (typeof hint === "string") return (KINDS as string[]).includes(hint) ? (hint as ToolSavedKind) : undefined;
  const type = prop.type;
  if (type !== undefined && type !== "string") return undefined;
  if (Array.isArray(prop.enum)) return undefined;
  const format = typeof prop.format === "string" ? prop.format : "";
  if (format) return FORMATS[format];
  const words = `${name} ${typeof prop.title === "string" ? prop.title : ""}`
    .replace(/[_-]+/g, " ")
    .replace(/([a-z])([A-Z])/g, "$1 $2")
    .toLowerCase();
  return WORDS.find(([re]) => re.test(words))?.[1];
}
