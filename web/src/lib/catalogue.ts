// The public connector catalogue (docs/connector-submissions.md): what a
// version asks a tenant to consent to, and how an upgrade changes it.

export interface Consent {
  hosts: string[];
  writes: Record<string, string>;
}

export interface CatalogueVersion {
  id: string;
  version: string;
  ref: string;
  name: string;
  description: string;
  category: string;
  hosts: string[];
  actions: Record<string, string>;
  pii: string[];
  triggers: string[];
  publisher: string;
  publisher_name: string;
  licence: string;
  source_url?: string;
  digest: string;
  state?: string;
  published_at?: string;
  revoke_reason?: string;
  consent: Consent;
}

export interface CatalogueEntry {
  id: string;
  name: string;
  description: string;
  category: string;
  publisher: string;
  publisher_name: string;
  versions: CatalogueVersion[]; // newest first
  installed: Record<string, string>; // major -> version
}

export interface Install {
  connector: string;
  major: number;
  ref: string;
  version: string;
  state: string; // published, revoked, unavailable
  revoke_reason?: string;
  latest?: string;
  consent: Consent;
  installed_by: string;
  installed_at: string;
  updated_at?: string;
}

export interface ClassChange {
  action: string;
  from: string;
  to: string;
}

export interface Diff {
  from: string;
  to: string;
  added_hosts?: string[];
  removed_hosts?: string[];
  added_actions?: string[];
  removed_actions?: string[];
  class_changes?: ClassChange[];
  added_writes?: string[];
  removed_fields?: string[];
  added_pii?: string[];
  removed_pii?: string[];
  needs_consent: boolean;
}

export function major(version: string): string {
  return version.split(".")[0] ?? version;
}

/** The newest version of each major, newest major first. */
export function newestPerMajor(
  versions: CatalogueVersion[],
): CatalogueVersion[] {
  const seen = new Set<string>();
  const out: CatalogueVersion[] = [];
  for (const v of versions) {
    const m = major(v.version);
    if (!seen.has(m)) {
      seen.add(m);
      out.push(v);
    }
  }
  return out;
}

const classWords: Record<string, string> = {
  idempotent_write: "changes things; safe to retry",
  reconcilable_write: "changes things; checked before any retry",
  unsafe_write: "changes things; never retried after a timeout",
};

/** One line per write action, saying what its class means. */
export function writeLines(c: Consent): string[] {
  return Object.keys(c.writes)
    .sort()
    .map((a) => {
      const cls = c.writes[a] ?? "";
      return `${a}: ${classWords[cls] ?? cls}`;
    });
}

/** What an upgrade changes, in words, most important first. */
export function diffLines(d: Diff): string[] {
  const out: string[] = [];
  for (const h of d.added_hosts ?? []) out.push(`New host: ${h}`);
  for (const c of d.class_changes ?? []) out.push(`${c.action} changes from ${c.from.replaceAll("_", " ")} to ${c.to.replaceAll("_", " ")}`);
  for (const a of d.added_writes ?? []) out.push(`New action that changes things: ${a}`);
  for (const p of d.added_pii ?? []) out.push(`Now treated as personal data: ${p}`);
  for (const a of (d.added_actions ?? []).filter((x) => !(d.added_writes ?? []).includes(x))) out.push(`New action: ${a}`);
  for (const a of d.removed_actions ?? []) out.push(`Removed action: ${a}`);
  for (const f of d.removed_fields ?? []) out.push(`Removed field: ${f}`);
  for (const h of d.removed_hosts ?? []) out.push(`No longer reaches: ${h}`);
  if (out.length === 0) out.push("No change to hosts, actions or classes");
  return out;
}
