// "Use it from chat" (made tools §13): a workflow started by hand made a
// tool in the builder, by picking, never by typing what the answer reads.
// These helpers suggest its command, and turn what was picked into the
// tool block's answer (wd-v1 `tool.answer`) and back.

import type { Answer, Output, Tool } from "@sdk/wd";

/** A command: what follows "/" (wd-v1: 2 to 24, a letter first). */
export const COMMAND = /^[a-z][a-z0-9-]{1,23}$/;

/** A command from a name: "Log an expense" is log-an-expense. */
export function suggestCommand(name: string): string {
  let s = name
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^[^a-z]+/, "")
    .replace(/-+$/, "");
  if (s.length > 24) {
    s = s.slice(0, 24);
    // Cut where a word ends, when that keeps most of it.
    const cut = s.lastIndexOf("-");
    if (cut >= 12) s = s.slice(0, cut);
    s = s.replace(/-+$/, "");
  }
  if (s.length < 2) s = s ? `${s}-tool` : "my-tool";
  return s;
}

/** What is wrong with a command as typed, in words; undefined when it fits. */
export function commandProblem(c: string): string | undefined {
  if (c === "") return "Give it a word to call it by.";
  if (!/^[a-z]/.test(c)) return "Start it with a small letter.";
  if (!/^[a-z0-9-]*$/.test(c))
    return "Use only small letters, numbers and dashes.";
  if (c.length < 2 || c.length > 24) return "Use 2 to 24 letters.";
  return undefined;
}

/** A value the answer shows: words typed, or one of the outputs picked. */
export type Pick = { text: string } | { output: string };

/** What the answer reads for a pick. */
export function pickValue(p: Pick | undefined): string {
  if (!p) return "";
  if ("output" in p) return `=outputs.${p.output}`;
  // Words that would read as a formula are kept as words.
  return p.text.startsWith("=") ? `=${JSON.stringify(p.text)}` : p.text;
}

const OUTPUT_REF = /^=outputs\.([a-z][a-z0-9_]*)$/;

/** A pick from what the answer reads; undefined when it was made by hand. */
export function pickOf(v: string | undefined): Pick | undefined {
  if (v === undefined || v === "") return undefined;
  const m = OUTPUT_REF.exec(v);
  if (m) return { output: m[1] ?? "" };
  if (!v.startsWith("=")) return { text: v };
  try {
    const s = JSON.parse(v.slice(1)) as unknown;
    if (typeof s === "string") return { text: s };
  } catch {
    // made by hand
  }
  return undefined;
}

/** A sentence as words and outputs, in order. */
export type Part = { text: string } | { output: string };

/** A sentence's parts as the answer's text: words as they are, values
 * written as words. */
export function sentenceText(parts: Part[]): string {
  const merged: Part[] = [];
  for (const p of parts) {
    const last = merged[merged.length - 1];
    if ("text" in p) {
      if (p.text === "") continue;
      if (last && "text" in last) last.text += p.text;
      else merged.push({ text: p.text });
    } else if (p.output) merged.push({ output: p.output });
  }
  if (merged.length === 0) return "";
  if (merged.every((p) => "text" in p))
    return pickValue({ text: (merged[0] as { text: string }).text });
  return (
    "=" +
    merged
      .map((p) =>
        "text" in p ? JSON.stringify(p.text) : `string(outputs.${p.output})`,
      )
      .join(" + ")
  );
}

/** A sentence's parts from the answer's text; undefined when it was made
 * by hand (and is then left as it is). */
export function sentenceParts(text: string): Part[] | undefined {
  if (!text.startsWith("=")) return text ? [{ text }] : [];
  const src = text.slice(1);
  const out: Part[] = [];
  let i = 0;
  const space = () => {
    while (src[i] === " ") i++;
  };
  for (;;) {
    space();
    if (src[i] === '"') {
      let j = i + 1;
      while (j < src.length && src[j] !== '"') j += src[j] === "\\" ? 2 : 1;
      try {
        out.push({ text: JSON.parse(src.slice(i, j + 1)) as string });
      } catch {
        return undefined;
      }
      i = j + 1;
    } else {
      const m =
        /^string\(outputs\.([a-z][a-z0-9_]*)\)/.exec(src.slice(i)) ??
        /^outputs\.([a-z][a-z0-9_]*)/.exec(src.slice(i));
      if (!m) return undefined;
      out.push({ output: m[1] ?? "" });
      i += m[0].length;
    }
    space();
    if (i >= src.length) return out;
    if (src[i] !== "+") return undefined;
    i++;
  }
}

/** A row's field as the answer reads it ("name" is item.name). */
export function itemValue(field: string): string {
  const f = field.trim();
  if (f === "") return "";
  return /^[A-Za-z_][A-Za-z0-9_]*$/.test(f)
    ? `=item.${f}`
    : `=item[${JSON.stringify(f)}]`;
}

/** The field a row's value reads; undefined when made by hand. */
export function itemField(v: string | undefined): string | undefined {
  if (!v) return "";
  const m = /^=item\.([A-Za-z_][A-Za-z0-9_]*)$/.exec(v);
  if (m) return m[1];
  const q = /^=item\[("(?:[^"\\]|\\.)*")\]$/.exec(v);
  if (q?.[1]) return JSON.parse(q[1]) as string;
  return undefined;
}

const SCALAR = new Set(["string", "integer", "number", "boolean"]);

/** The outputs a sentence or a card's line can show (not lists or groups). */
export const scalarOutputs = (outputs: Output[]) =>
  outputs.filter((o) => SCALAR.has(o.type));

/** The outputs a list can be made of. */
export const listOutputs = (outputs: Output[]) =>
  outputs.filter((o) => o.type === "list");

/** The outputs that can be an amount (whole kobo). */
export const amountOutputs = (outputs: Output[]) =>
  outputs.filter((o) => o.type === "integer" || o.type === "number");

/** A first answer of a shape, from what the workflow gives back. */
export function firstAnswer(
  type: "text" | "card" | "list",
  outputs: Output[],
  name: string,
): Answer {
  const scalars = scalarOutputs(outputs);
  const first = scalars[0];
  if (type === "text")
    return {
      type: "text",
      text: first
        ? sentenceText([{ text: `${first.title}: ` }, { output: first.name }])
        : "Done.",
    };
  if (type === "card") {
    const money = scalars.find((o) => /_kobo$/.test(o.name));
    return {
      type: "card",
      title: name,
      lines: scalars
        .filter((o) => o !== money)
        .slice(0, 6)
        .map((o) => ({ label: o.title, value: pickValue({ output: o.name }) })),
      ...(money ? { amount: pickValue({ output: money.name }) } : {}),
    };
  }
  const list = listOutputs(outputs)[0];
  return {
    type: "list",
    from: list?.name ?? "",
    title: list?.title ?? name,
    item: { title: "=item.name" },
  };
}

/** An output's name from its words: "Total spent" is total_spent. */
export function outputName(title: string, taken: string[] = []): string {
  let base = title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^[^a-z]+/, "")
    .replace(/_+$/, "")
    .slice(0, 36);
  if (base === "") base = "result";
  let name = base;
  for (let n = 2; taken.includes(name); n++) name = `${base}_${n}`;
  return name;
}

/** A new tool block for a workflow: its name, a command from it, and what
 * it is for from its description, when it has one. */
export function newTool(name: string, outputs: Output[], description = ""): Tool {
  const n = name.trim().slice(0, 40) || "My tool";
  return {
    name: n,
    command: suggestCommand(n),
    about: (description.split("\n")[0] ?? "").trim().slice(0, 120),
    ...(outputs.length ? { answer: firstAnswer("text", outputs, n) } : {}),
  };
}

/** A problem the validator or a publish gave about the tool, in plain
 * words: a command taken by another workflow, or one of Taskiem's own. */
export function toolProblemWords(message: string): string {
  const own = /"([^"]+)" is one of Taskiem's own commands/.exec(message);
  if (own)
    return `/${own[1]} is one of Taskiem's own words. Choose another word.`;
  const starts = /"([^"]+)" starts like one of Taskiem's own commands/.exec(
    message,
  );
  if (starts)
    return `“${starts[1]}” starts like one of Taskiem's own words. Choose other words.`;
  const taken = /the command \/(\S+) (?:is already (.+?)'s|was just taken)/.exec(
    message,
  );
  if (taken)
    return taken[2]
      ? `/${taken[1]} is already used by “${taken[2]}”. Choose another word.`
      : `/${taken[1]} was just taken by another workflow. Choose another word.`;
  const m = message.replace(/^[a-z_]+: /, "");
  return m.charAt(0).toUpperCase() + m.slice(1);
}

/** A problem with the tool's own fields, by where it is: the form's own
 * words rather than the shape's. */
export function toolProblem(p: { path: string; message: string; plain?: string }): string {
  if (p.path === "/tool/about")
    return "Say in a line what it is for (at most 120 letters).";
  if (p.path === "/tool/name") return "Give it a name (at most 40 letters).";
  if (p.path === "/tool/command" && /^schema: /.test(p.message))
    return "Its word is 2 to 24 small letters, numbers and dashes, starting with a letter.";
  // A taken or reserved word in our words; else the server's plain ones.
  if (/Taskiem's own commands|the command \//.test(p.message))
    return toolProblemWords(p.message);
  return p.plain ?? toolProblemWords(p.message);
}
