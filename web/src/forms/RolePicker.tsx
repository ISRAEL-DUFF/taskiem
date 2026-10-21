import { useState } from "react";
import { get } from "../api";
import { useLoad } from "../ui";

/** A role an approval or form step can name (GET /v1/approver-roles). */
export interface ApproverRole {
  name: string;
  builtin: boolean;
  holders: number;
}

const roleWords = (s: string) => s.replace(/_/g, " ");

/** A role machine name from words: "Head of credit" is head_of_credit. */
export function roleName(words: string): string {
  return words
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .replace(/^([0-9])/, "r_$1")
    .slice(0, 63);
}

/** How a role reads in a picker: its words and who holds it. */
export function roleLabel(r: ApproverRole): string {
  if (r.name === "owner") return "Owner (the business's owners)";
  if (r.name === "approver") return `Approver${r.holders ? ` (${r.holders})` : " (nobody yet)"}`;
  return `${roleWords(r.name)}${r.holders ? ` (${r.holders})` : " (nobody yet)"}`;
}

/**
 * Picks a role from the business's roles, so nobody types one that must
 * match (no-technical-steps N3); a new one is typed in words. allowNone
 * offers "Anyone who approves" (no role).
 */
export function RolePicker({ value, onChange, allowNone = false, label = "Role" }: { value: string; onChange: (role: string) => void; allowNone?: boolean; label?: string }) {
  const roles = useLoad(() => get<{ roles: ApproverRole[]; members: number }>("/v1/approver-roles"), []);
  const known = roles.data?.roles ?? [];
  const listed = value === "" || known.some((r) => r.name === value);
  const [adding, setAdding] = useState(false);
  const [words, setWords] = useState("");
  if (adding || (!listed && roles.data)) {
    return (
      <span className="toolbar">
        <input
          aria-label={`${label}: a new role`}
          placeholder="e.g. Head of credit"
          value={adding ? words : roleWords(value)}
          onChange={(e) => {
            setWords(e.target.value);
            onChange(roleName(e.target.value));
          }}
        />
        <button type="button" className="link" onClick={() => (setAdding(false), setWords(""), onChange(known[0]?.name ?? "owner"))}>
          Choose an existing role
        </button>
      </span>
    );
  }
  return (
    <select
      aria-label={label}
      value={value}
      onChange={(e) => {
        if (e.target.value === "__new") {
          setAdding(true);
          onChange("");
          return;
        }
        onChange(e.target.value);
      }}
    >
      {allowNone && <option value="">Anyone who approves</option>}
      {!allowNone && value === "" && <option value="">Choose…</option>}
      {known.map((r) => (
        <option key={r.name} value={r.name}>
          {roleLabel(r)}
        </option>
      ))}
      <option value="__new">A new role…</option>
    </select>
  );
}
