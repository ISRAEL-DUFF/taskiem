import { describe, expect, it } from "vitest";
import {
  answerProblem,
  answersToSend,
  coerceAnswer,
  embedSnippet,
  evalShowIf,
  fieldTypeOf,
  formProblems,
  formatNaira,
  linksCSV,
  loadFields,
  localToRFC3339,
  nameFromLabel,
  newField,
  parseDuration,
  parseNaira,
  parseRecipients,
  propertyOf,
  saveFields,
  schemaOrder,
  shownPages,
  stepProblems,
  suggestPii,
  widgetOf,
  type FieldType,
  type FormSchema,
} from "./forms";

const loan: FormSchema = {
  type: "object",
  required: ["full_name", "phone", "amount_naira"],
  properties: {
    full_name: { type: "string", title: "Full name", maxLength: 100, "x-pii": "name" },
    phone: { type: "string", format: "phone-ng", "x-pii": "phone" },
    bvn: { type: "string", pattern: "^[0-9]{11}$", "x-pii": "bvn", "x-form": { help: "11 digits" } },
    amount_naira: { type: "integer", minimum: 10000, "x-form": { unit: "naira" } },
    employed: { type: "boolean", title: "Are you employed?" },
    purpose: { type: "string", enum: ["school", "rent", "business"], "x-form": { label: "What is it for?" } },
    payslip: { type: "string", format: "file", "x-form": { accept: ["application/pdf", "image/*"], max_mb: 5 } },
    source: { type: "string", "x-form": { hidden: true, prefill: true } },
    reason: { type: "string", "x-form": { widget: "textarea", placeholder: "Tell us more", options_from: { table: "x" } } },
    agree: { type: "boolean", "x-form": { widget: "consent", text: "I agree to the terms" } },
    days: { type: "array", items: { type: "string", enum: ["mon", "tue"] } },
  },
};

describe("field types and widgets", () => {
  const cases: [string, FieldType, string][] = [
    ["full_name", "text", "text"],
    ["phone", "phone", "tel"],
    ["amount_naira", "money", "money"],
    ["employed", "yes_no", "checkbox"],
    ["purpose", "choice", "radio"],
    ["payslip", "file", "file"],
    ["source", "hidden", "hidden"],
    ["reason", "long_text", "textarea"],
    ["agree", "consent", "consent"],
    ["days", "choices", "checkboxes"],
  ];
  it.each(cases)("%s is %s, shown as %s", (name, type, widget) => {
    const p = loan.properties?.[name] ?? {};
    expect(fieldTypeOf(p)).toBe(type);
    expect(widgetOf(p)).toBe(widget);
  });
  it("shows more than five choices as a list", () => {
    expect(widgetOf({ type: "string", enum: ["a", "b", "c", "d", "e", "f"] })).toBe("select");
    expect(widgetOf({ type: "string", format: "date-time" })).toBe("datetime-local");
    expect(widgetOf({ type: "number" })).toBe("number");
  });
});

describe("schema and field editor", () => {
  it("orders like the platform: x-form.order, then required, then by name", () => {
    expect(schemaOrder(loan)).toEqual(["amount_naira", "full_name", "phone", "agree", "bvn", "days", "employed", "payslip", "purpose", "reason", "source"]);
    expect(schemaOrder({ properties: { b: {}, a: { "x-form": { order: 2 } }, c: { "x-form": { order: 1 } } } })).toEqual(["c", "a", "b"]);
  });
  it("round-trips a schema, x-form and all", () => {
    const { fields, pages } = loadFields(loan);
    expect(pages).toEqual([]);
    expect(saveFields(fields, loan).schema).toEqual(loan);
  });
  it("round-trips pages", () => {
    const pg = [
      { title: "About you", fields: ["full_name", "phone", "bvn"] },
      { title: "The loan", fields: ["amount_naira", "purpose", "employed", "reason", "agree", "days"] },
      { title: "Your employer", fields: ["payslip"], show_if: "answers.employed == true" },
    ];
    const { fields, pages } = loadFields(loan, pg);
    expect(fields.map((f) => f.name).slice(0, 4)).toEqual(["full_name", "phone", "bvn", "amount_naira"]);
    expect(fields.find((f) => f.name === "payslip")?.page).toBe(2);
    const out = saveFields(fields, loan, pages);
    expect(out.schema).toEqual(loan);
    expect(out.pages).toEqual(pg);
  });
  it("writes x-form.order when fields are moved", () => {
    const { fields } = loadFields({ type: "object", properties: { a: { type: "string" }, b: { type: "string" } } });
    const out = saveFields([...fields].reverse()).schema;
    expect(out.properties?.b?.["x-form"]?.order).toBe(1);
    expect(schemaOrder(out)).toEqual(["b", "a"]);
  });
  it("keeps other keys while the type stays, and drops them when it changes", () => {
    const f = loadFields(loan).fields.find((x) => x.name === "full_name");
    if (!f) throw new Error("no field");
    expect(propertyOf(f).maxLength).toBe(100);
    expect(propertyOf({ ...f, type: "number" })).toEqual({ type: "number", title: "Full name", "x-pii": "name" });
  });
  it("writes each type", () => {
    expect(propertyOf({ ...newField("x", "money"), label: "Amount" })).toEqual({ type: "integer", title: "Amount", "x-form": { unit: "naira" } });
    expect(propertyOf(newField("x", "choices"))).toEqual({ type: "array", items: { type: "string", enum: ["Option 1", "Option 2"] } });
    expect(propertyOf({ ...newField("x", "consent"), consentText: "I agree" })).toEqual({ type: "boolean", "x-form": { widget: "consent", text: "I agree" } });
    expect(propertyOf(newField("x", "file"))).toEqual({ type: "string", format: "file", "x-form": { accept: ["application/pdf", "image/*"] } });
    expect(propertyOf(newField("x", "hidden"))).toEqual({ type: "string", "x-form": { hidden: true, prefill: true } });
    expect(propertyOf(newField("x", "date_time"))).toEqual({ type: "string", format: "date-time" });
    expect(propertyOf({ ...newField("e", "email"), from: "member.email" })).toEqual({ type: "string", format: "email", "x-pii": "email", "x-form": { from: "member.email" } });
  });
  it("names fields from labels", () => {
    expect(nameFromLabel("Your full name", [])).toBe("your_full_name");
    expect(nameFromLabel("Reason", ["reason"])).toBe("reason_2");
    expect(nameFromLabel("2 dates", [])).toBe("field_2_dates");
  });
  it("suggests personal-data categories", () => {
    expect(suggestPii("phone")).toBe("phone");
    expect(suggestPii("contact", "email")).toBe("email");
    expect(suggestPii("customer_bvn")).toBe("bvn");
    expect(suggestPii("nin")).toBe("nin");
    expect(suggestPii("account_number")).toBe("account_number");
    expect(suggestPii("full_name")).toBe("name");
    expect(suggestPii("reason")).toBe("");
    expect(suggestPii("pinned_note")).toBe("");
  });
});

describe("client-side checks", () => {
  const schema: FormSchema = { type: "object", properties: { a: { type: "string" }, b: { type: "boolean" }, c: { type: "string" } } };
  it("needs a title and every field on a page", () => {
    const ps = formProblems({ pages: [{ fields: ["a"] }] }, schema);
    expect(ps.map((p) => p.message)).toEqual(['a form needs a title', '"b" is on no page (put it on one, or hide it)', '"c" is on no page (put it on one, or hide it)']);
  });
  it("finds duplicates and unknown fields", () => {
    const ps = formProblems({ title: "T", pages: [{ fields: ["a", "b", "zz"] }, { fields: ["a", "c"] }] }, schema);
    expect(ps).toEqual([
      { path: "/trigger/config/pages/0/fields", message: '"zz" is not in the inputs schema' },
      { path: "/trigger/config/pages/1/fields", message: '"a" is on page 1 already' },
    ]);
  });
  it("lets show_if read earlier pages only", () => {
    const ps = formProblems({ title: "T", pages: [{ fields: ["a"], show_if: "true" }, { fields: ["b"], show_if: "answers.c == 'x'" }, { fields: ["c"], show_if: "answers.b" }] }, schema);
    expect(ps.map((p) => `${p.path} ${p.message}`)).toEqual([
      "/trigger/config/pages/0/show_if the first page is always shown",
      '/trigger/config/pages/1/show_if the condition reads "c", which is not on an earlier page',
    ]);
  });
  it("refuses fields a form never asks for, and unsealed or anonymous identifiers", () => {
    const s: FormSchema = { type: "object", properties: { pin: { type: "string" }, card_number: { type: "string" }, bvn: { type: "string" }, nin: { type: "string", "x-pii": "nin" } } };
    const pub = formProblems({ title: "T" }, s).map((p) => p.message);
    expect(pub).toContain('a form never asks for passwords, PINs, card details or one-time codes ("pin")');
    expect(pub).toContain('a form never asks for passwords, PINs, card details or one-time codes ("card_number")');
    expect(pub).toContain('"bvn" looks like an identifier: mark it with its x-pii category so it is sealed');
    expect(pub).toContain('"nin" may be asked only on a link or members form, or a public form with verify on');
    const verified = formProblems({ title: "T", verify: "phone" }, s).map((p) => p.message);
    expect(verified.some((m) => m.includes('"nin" may be asked'))).toBe(false);
  });
  it("checks redirects, verify on members forms, and file sizes", () => {
    const s: FormSchema = { type: "object", properties: { f: { type: "string", format: "file", "x-form": { max_mb: 40 } } } };
    const ps = formProblems({ title: "T", access: "members", verify: "email", after: { mode: "redirect", url: "http://x" } }, s).map((p) => p.path);
    expect(ps).toEqual(["/trigger/config/verify", "/trigger/config/after/url", "/inputs/schema/properties/f/x-form/max_mb"]);
  });
  it("checks form steps", () => {
    expect(stepProblems({ title: "", to: { phone: "=x", role: "ops" }, verify: true, timeout: "48h", remind: ["24h", "72h"], schema: {} }, "/steps/0/config").map((p) => p.path)).toEqual([
      "/steps/0/config/title",
      "/steps/0/config/to",
      "/steps/0/config/verify",
      "/steps/0/config/remind/1",
      "/steps/0/config/schema",
    ]);
    expect(stepProblems({ title: "Bank", to: { role: "ops" }, schema: { type: "object", properties: { bank: { type: "string" } } } })).toEqual([]);
    expect(parseDuration("1d12h")).toBe(36 * 3_600_000);
    expect(parseDuration("soon")).toBeUndefined();
  });
});

describe("show_if", () => {
  const a = { employed: true, amount: 50000, purpose: "rent", name: "Ada" };
  it.each([
    ["answers.employed == true", true],
    ["=answers.employed", true],
    ["answers.employed != true", false],
    ["answers.amount >= 50000 && answers.purpose == 'rent'", true],
    ["answers.amount > 50000 || answers.purpose == \"rent\"", true],
    ["!(answers.employed) || false", false],
    ["answers['purpose'] in ['rent', 'school']", true],
    ["answers.missing == null", true],
    ["answers.missing == true", false],
    ["(answers.amount < 10)", false],
  ])("%s is %s", (src, want) => expect(evalShowIf(src, a)).toBe(want));
  it.each(["answers.name.startsWith('A')", "size(answers.name) > 2", "answers.amount + 1 > 2", "answers.amount", "answers.employed &&"])(
    "%s is beyond it",
    (src) => expect(evalShowIf(src, a)).toBeUndefined(),
  );
  it("shows pages whose condition holds, or cannot be read", () => {
    const pages = [{ fields: ["a"] }, { fields: ["b"], show_if: "answers.a == 'yes'" }, { fields: ["c"], show_if: "size(answers.a) > 0" }];
    expect(shownPages(pages, { a: "no" })).toEqual([0, 2]);
    expect(shownPages(pages, { a: "yes" })).toEqual([0, 1, 2]);
  });
  it("sends only the answers on shown pages, and hidden fields", () => {
    const schema: FormSchema = { properties: { a: { type: "string" }, b: { type: "integer" }, src: { type: "string", "x-form": { hidden: true } } } };
    const pages = [{ fields: ["a"] }, { fields: ["b"], show_if: "answers.a == 'yes'" }];
    expect(answersToSend(schema, pages, { a: "no", b: "4", src: "ad" })).toEqual({ a: "no", src: "ad" });
    expect(answersToSend(schema, pages, { a: "yes", b: "4", src: "" })).toEqual({ a: "yes", b: 4 });
  });
});

describe("answers", () => {
  it("formats and reads naira", () => {
    expect(formatNaira(1234567)).toBe("₦1,234,567");
    expect(formatNaira(1234.5)).toBe("₦1,234.50");
    expect(formatNaira(0)).toBe("₦0");
    expect(formatNaira(-500, false)).toBe("-500");
    expect(parseNaira("₦1,234.50")).toBe(1234.5);
    expect(parseNaira(" 10 000 ")).toBe(10000);
    expect(parseNaira("")).toBeUndefined();
    expect(parseNaira("ten")).toBeNaN();
  });
  it("coerces raw input to what the schema wants", () => {
    expect(coerceAnswer({ type: "integer", "x-form": { unit: "naira" } }, "₦50,000")).toBe(50000);
    expect(coerceAnswer({ type: "number" }, "2.5")).toBe(2.5);
    expect(coerceAnswer({ type: "string", format: "phone-ng" }, "0803 123 4567")).toBe("08031234567");
    expect(coerceAnswer({ type: "string" }, "")).toBeUndefined();
    expect(coerceAnswer({ type: "array", items: { enum: ["a"] } }, [])).toBeUndefined();
    expect(localToRFC3339("2026-10-20T09:30", -60)).toBe("2026-10-20T09:30:00+01:00");
  });
  it("explains problems in plain words", () => {
    expect(answerProblem({ type: "string" }, undefined, true)).toBe("This question needs an answer.");
    expect(answerProblem({ type: "boolean", "x-form": { widget: "consent" } }, false, true)).toBe("Tick the box to agree.");
    expect(answerProblem({ type: "string", format: "email" }, "ada@", false)).toMatch(/email address/);
    expect(answerProblem({ type: "string", format: "phone-ng" }, "08031234567", true)).toBe("");
    expect(answerProblem({ type: "integer", minimum: 10000, "x-form": { unit: "naira" } }, 500, true)).toBe("At least ₦10,000.");
    expect(answerProblem({ type: "string", pattern: "^[0-9]{11}$", "x-form": { help: "11 digits" } }, "123", true)).toBe("Check the format: 11 digits");
  });
});

describe("links and embedding", () => {
  it("reads pasted recipients", () => {
    expect(parseRecipients("0803 123 4567\nada@example.com, nope\n\n+2348091234567")).toEqual({
      to: [{ phone: "08031234567" }, { email: "ada@example.com" }, { phone: "+2348091234567" }],
      bad: ["nope"],
    });
  });
  it("writes links as CSV", () => {
    expect(linksCSV([{ url: "https://f/r/a", to: "+234 803 ***", expires_at: "2026-10-27" }])).toBe("url,to,expires_at\nhttps://f/r/a,+234 803 ***,2026-10-27\n");
  });
  it("makes the embed snippet", () => {
    expect(embedSnippet("https://forms.x/embed/f/abc", 'A "loan"')).toBe(
      '<iframe src="https://forms.x/embed/f/abc" title="A &quot;loan&quot;" style="width:100%;min-height:640px;border:0" loading="lazy"></iframe>',
    );
  });
});
