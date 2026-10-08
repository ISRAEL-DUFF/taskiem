// The operator console's client (api/ops.go): its own session cookie,
// scoped to /v1/ops; the CSRF header on every request; a passkey step-up
// on every write.

import { ApiError } from "../api";
import { assertFrom, createFrom, type CreationJSON, type RequestJSON } from "../passkeys";

export async function opsApi<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/v1/ops${path}`, {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-Taskiem-Request": "1" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: Record<string, unknown>;
  try {
    data = text ? (JSON.parse(text) as Record<string, unknown>) : {};
  } catch {
    data = { error: text };
  }
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith("/auth/")) window.dispatchEvent(new Event("taskiem:ops-unauthorised"));
    throw new ApiError(res.status, (data.error as string) ?? res.statusText, [], data);
  }
  return data as T;
}

export const opsGet = <T>(path: string) => opsApi<T>("GET", path);
export const opsPost = <T>(path: string, body?: unknown) => opsApi<T>("POST", path, body ?? {});

/** Sends a write with a passkey assertion bound to its operation and
 * target: the server refuses the write without it. */
export async function withStepUp<T>(operation: string, target: string, path: string, body: Record<string, unknown>): Promise<T> {
  const { publicKey } = await opsPost<{ publicKey: RequestJSON }>("/step-up/options", { operation, target });
  const passkey = await assertFrom(publicKey);
  return opsPost<T>(path, { ...body, step_up: { passkey } });
}

export async function opsPasskeySignIn() {
  const { publicKey } = await opsPost<{ publicKey: RequestJSON }>("/auth/passkey/options");
  await opsPost("/auth/passkey", { credential: await assertFrom(publicKey) });
}

export async function opsEnrol(token: string, name: string) {
  const { publicKey } = await opsPost<{ publicKey: CreationJSON }>("/auth/enrol/options", { token });
  await opsPost("/auth/enrol", { token, name, credential: await createFrom(publicKey) });
}

// --- response shapes ---

export interface OpsMe {
  operator: { id: string; email: string; name: string; auth_method: string; expires_at: string };
  reviewer: boolean;
}

export interface QueueItem {
  id: string;
  publisher: string;
  connector: string;
  version: string;
  state: string;
  licence: string;
  submitted_by: string;
  submitted_at: string;
  checks_passed: boolean;
  reviewed_by: string | null;
}

export interface CheckResult {
  name: string;
  pass: boolean;
  detail?: string;
}

export interface CaseResult {
  case: string;
  action: string;
  pass: boolean;
  problems?: string[];
}

export interface SubmissionDetail {
  submission: {
    id: string;
    publisher_tenant: string;
    publisher: string;
    connector: string;
    version: string;
    state: string;
    manifest: string;
    module_sha256: string;
    package_digest: string;
    key_id: string;
    licence: string;
    source_url: string | null;
    attestation: Record<string, unknown>;
    checks: {
      passed?: boolean;
      checks?: CheckResult[];
      conformance?: { results?: CaseResult[]; uncovered?: string[]; key_not_sent?: string[]; read_writes?: string[] };
    };
    submitted_by: string;
    submitted_at: string;
    reviewed_by: string | null;
    reviewed_at: string | null;
    review_note: string | null;
  };
  summary?: { name: string; description: string; hosts: string[]; actions: Record<string, string>; pii: string[]; triggers: string[] };
  lint: { level: string; path: string; message: string }[];
  checklist: { key: string; text: string }[];
  history: { event: string; actor: string; note: string | null; at: string }[];
}

export interface Publisher {
  tenant_id: string;
  slug: string;
  name: string;
  key_id: string;
  status: string;
  requested_at: string;
  verified_by: string | null;
  verified_at: string | null;
  status_note: string | null;
}

export interface Incident {
  id: string;
  kind: string;
  title: string;
  components: string[];
  starts_at?: string;
  ends_at?: string;
  created_at: string;
  status: string;
  impact: string;
  closed: boolean;
  updates: { status: string; impact: string; message: string; at: string; actor?: string }[];
}

export interface TenantRow {
  id: string;
  name: string;
  parent_id: string | null;
  status: string;
  created_at: string;
  plan: string | null;
  subscription_status: string | null;
  pool: string;
}

export interface AuditEntry {
  seq: number;
  actor_type: string;
  actor_id: string;
  action: string;
  target: string;
  detail: Record<string, unknown>;
  at: string;
}
