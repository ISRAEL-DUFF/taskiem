// Unsaved builder edits kept in the tab's sessionStorage while there are
// any, so they outlive what the leave warning cannot stop: the browser's
// Back button, a sign-out (a 401 sends the person to sign in again) and a
// closed or crashed page. Opening the workflow again offers them back.
// Storage can be missing or full (private windows, quotas): every access is
// guarded, and the builder works without it.

/** Edits kept for a workflow: the definition and layout, and the version
 * (and its digest) they were made on, so saving them merges with any
 * version saved since. */
export interface KeptDraft<D = unknown> {
  definition: D;
  layout: Record<string, unknown>;
  version: number;
  digest?: string;
  /** When they were kept, as an ISO time. */
  at: string;
}

const key = (workflow: string) => `taskiem:draft:${workflow}`;

function store(): Storage | undefined {
  try {
    return window.sessionStorage;
  } catch {
    return undefined;
  }
}

/** Keeps the unsaved edits for a workflow, replacing any kept before. */
export function keepDraft<D>(workflow: string, draft: KeptDraft<D>, s: Storage | undefined = store()): void {
  try {
    s?.setItem(key(workflow), JSON.stringify(draft));
  } catch {
    // Full or refused: the edits are only on the page.
  }
}

/** The edits kept for a workflow, or undefined. */
export function keptDraft<D = unknown>(workflow: string, s: Storage | undefined = store()): KeptDraft<D> | undefined {
  try {
    const raw = s?.getItem(key(workflow));
    if (!raw) return undefined;
    const v = JSON.parse(raw) as Partial<KeptDraft<D>> | null;
    if (!v || typeof v !== "object" || typeof v.definition !== "object" || v.definition === null || typeof v.version !== "number") return undefined;
    return { definition: v.definition, layout: v.layout && typeof v.layout === "object" ? v.layout : {}, version: v.version, digest: typeof v.digest === "string" ? v.digest : undefined, at: typeof v.at === "string" ? v.at : "" };
  } catch {
    return undefined;
  }
}

/** Forgets the edits kept for a workflow: saved, or thrown away. */
export function dropDraft(workflow: string, s: Storage | undefined = store()): void {
  try {
    s?.removeItem(key(workflow));
  } catch {
    // Nothing to do.
  }
}
