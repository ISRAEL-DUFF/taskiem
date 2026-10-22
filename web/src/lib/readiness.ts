// Readiness (no-technical-steps N3): what a workflow needs before it can
// run where it is published, each with the fix it takes. The server finds
// them (GET /v1/workflows/{wf}/versions/{v}/readiness, POST /v1/readiness);
// this decides how the web app offers each one.

export interface ReadyNeed {
  kind: "connection" | "variable" | "secret" | "table" | "role" | "field";
  /** "" for a connection a step does not name. */
  name: string;
  connector?: string;
  service?: string;
  problem: "missing" | "expired" | "disabled" | "ambiguous" | "nobody" | "chat_id" | "cannot";
  environment: string;
  steps: { id: string; name: string }[];
  /** Publishing waits for it: a run would fail at once. */
  blocking: boolean;
  message: string;
  /** A missing table as the workflow uses it, to make in one tap. */
  table?: TableSpec;
  /** For the connection a capability step needs: what its rail must do, and the rails (connector ids) that can. */
  capability?: string;
  rails?: string[];
}

export interface TableSpec {
  name: string;
  title: string;
  columns: { name: string; title: string; type: string; required: boolean }[];
}

export interface Readiness {
  ready: boolean;
  needs: ReadyNeed[];
}

/** The fix a need offers, done in place. */
export type Fix =
  | { kind: "connect"; connector: string; environment: string; name: string; service: string }
  | { kind: "connect_rail"; rails: string[]; environment: string; name: string }
  | { kind: "choose_connection"; connector: string; service: string }
  | { kind: "set_variable"; environment: string; name: string }
  | { kind: "set_secret"; environment: string; name: string }
  | { kind: "make_table"; name: string; table?: TableSpec }
  | { kind: "give_role"; role: string }
  | { kind: "ask_phone"; field: string };

/** The connection name a new connection takes for a need. */
export const DEFAULT_CONNECTION = "main";

export function fixFor(n: ReadyNeed): Fix {
  switch (n.kind) {
    case "connection":
      if (n.capability && n.problem !== "ambiguous") return { kind: "connect_rail", rails: n.rails ?? [], environment: n.environment, name: n.name || DEFAULT_CONNECTION };
      if (n.capability) return { kind: "choose_connection", connector: "", service: n.service ?? "" };
      if (n.problem === "ambiguous") return { kind: "choose_connection", connector: n.connector ?? "", service: n.service ?? n.connector ?? "" };
      return { kind: "connect", connector: n.connector ?? "", environment: n.environment, name: n.name || DEFAULT_CONNECTION, service: n.service ?? n.connector ?? "" };
    case "variable":
      return { kind: "set_variable", environment: n.environment, name: n.name };
    case "secret":
      return { kind: "set_secret", environment: n.environment, name: n.name };
    case "table":
      return n.table ? { kind: "make_table", name: n.name, table: n.table } : { kind: "make_table", name: n.name };
    case "role":
      return { kind: "give_role", role: n.name };
    case "field":
      return { kind: "ask_phone", field: n.name };
  }
}

/** A machine name in words: payout_note is "payout note". */
export const words = (s: string) => s.replace(/_/g, " ");

/**
 * The needs to show, production first: a need in dev (a rehearsal) only
 * when the same thing is not already listed for production, and never
 * blocking.
 */
export function needsToShow(needs: ReadyNeed[], advanced = false): ReadyNeed[] {
  const key = (n: ReadyNeed) => `${n.kind}/${n.name}/${n.connector ?? ""}`;
  const seen = new Set<string>();
  const out: ReadyNeed[] = [];
  const sorted = [...needs].sort((a, b) => Number(b.blocking) - Number(a.blocking) || Number(b.environment === "prod") - Number(a.environment === "prod"));
  for (const n of sorted) {
    if (n.environment !== "prod" && !n.blocking && !advanced) continue;
    if (seen.has(key(n))) continue;
    seen.add(key(n));
    out.push(n);
  }
  return out;
}

/** "Accept or decline the order" and 2 more: the steps that need it. */
export function stepsText(n: ReadyNeed): string {
  const names = n.steps.map((s) => s.name || s.id);
  if (names.length === 0) return "";
  if (names.length === 1) return names[0] ?? "";
  return `${names[0] ?? ""} and ${names.length - 1} more`;
}

/** A variable typed as plain text, kept as text (N5 infers types). */
export function variableValue(text: string): string {
  return text.trim();
}
