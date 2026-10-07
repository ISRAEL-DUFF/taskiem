// Thin client for the Taskiem REST API (api/server.go). The session is an
// HttpOnly cookie; every request carries the CSRF header the API requires.

export interface Problem {
  path: string;
  message: string;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public problems: Problem[] | string[] = [],
    public body: Record<string, unknown> = {},
  ) {
    super(message);
  }
}

export async function api<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  return request<T>(method, path, body === undefined ? undefined : JSON.stringify(body), { "Content-Type": "application/json" });
}

/** POSTs a multipart form (file uploads); the browser sets its boundary. */
export function upload<T = unknown>(path: string, form: FormData): Promise<T> {
  return request<T>("POST", path, form, {});
}

async function request<T>(method: string, path: string, body: BodyInit | undefined, headers: Record<string, string>): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: { ...headers, "X-Taskiem-Request": "1" },
    body,
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
    if (res.status === 401 && !path.startsWith("/v1/auth/")) window.dispatchEvent(new Event("taskiem:unauthorised"));
    throw new ApiError(res.status, (data.error as string) ?? res.statusText, (data.problems as Problem[]) ?? [], data);
  }
  return data as T;
}

/** A unit both an edit and a newer version changed (409 from saving a version). */
export interface MergeConflict {
  path: string;
  kind: "both_changed" | "changed_and_deleted" | "both_added";
  base?: unknown;
  ours?: unknown;
  theirs?: unknown;
}

export const get = <T>(path: string) => api<T>("GET", path);
export const post = <T>(path: string, body?: unknown) => api<T>("POST", path, body ?? {});
export const put = <T>(path: string, body?: unknown) => api<T>("PUT", path, body ?? {});
export const del = <T>(path: string, body?: unknown) => api<T>("DELETE", path, body);

// --- response shapes ---

/** Where a connector's live output departed from its declared schema. */
export interface DriftFinding {
  connector: string;
  version: string;
  action: string;
  path: string;
  kind: "type" | "enum" | "missing";
  expected: string;
  observed: string;
  first_seen: string;
  last_seen: string;
  occurrences: number;
  last_run_id?: string;
  acknowledged_at?: string;
  acknowledged_by?: string;
}

/** A version of one of the tenant's own WebAssembly connectors. */
export interface TenantConnector {
  id: string;
  version: string;
  ref: string;
  digest: string;
  uploaded_by: string;
  uploaded_at: string;
  disabled_at?: string;
  active: boolean;
}

export interface Me {
  tenant_id: string;
  roles: string[];
  permissions: string[];
  user?: { id: string; email: string; name: string };
  /** Whether the member has an authenticator app enrolled for step-up. */
  totp?: boolean;
  /** How this session signed in. */
  auth_method?: "password" | "passkey" | "sso";
  /** An administrator who must add a passkey before anything else. */
  enrol_passkey?: boolean;
}

export interface WorkflowSummary {
  id: string;
  name: string;
  active_version: number | null;
  latest_version: number;
  created_at: string;
  key: string;
  /** Set when a Git-led repository holds this workflow: it is read-only here. */
  git_path: string | null;
}

export interface VersionInfo {
  version: number;
  state: "draft" | "published" | "deprecated" | "archived";
  digest: string;
  created_by: string | null;
  created_at: string;
  published_by: string | null;
  published_at: string | null;
  git_commit: string | null;
  git_request: string | null;
}

export interface GitSync {
  id: string;
  commit: string;
  requested_by: string;
  status: "queued" | "running" | "deployed" | "unchanged" | "failed";
  report?: { commit?: string; error?: string; problems?: string[]; tests?: { passed: number; failed: number; failures?: string[] }; workflows?: { path: string; key: string; action: string; version?: number }[] };
  requested_at: string;
  finished_at?: string;
}

export interface GitConnection {
  environment: string;
  provider: "github" | "gitlab" | "bitbucket";
  api_url: string;
  repo: string;
  branch: string;
  path: string;
  tests_path: string;
  mode: "platform_led" | "git_led";
  updated_at: string;
  webhook_url: string;
  last_sync?: GitSync;
}

export interface RunSummary {
  id: string;
  workflow_id: string;
  workflow: string;
  version: number;
  environment: string;
  status: RunStatus;
  started_by: string | null;
  started_at: string;
  ended_at: string | null;
}

export type RunStatus = "queued" | "running" | "waiting" | "needs_reconciliation" | "completed" | "failed" | "cancelled";

export interface RunEvent {
  seq: number;
  type: string;
  step_id?: string;
  attempt?: number;
  payload?: unknown;
  recorded_at: string;
  origin: string;
}

export interface ConnectorInfo {
  ref: string;
  id: string;
  version: string;
  name: string;
  auth: { type: string; fields: { key: string; label: string; secret?: boolean; required?: boolean }[] | null };
  actions: Record<string, { title: string; class: string; input?: JSONSchema; output?: JSONSchema }>;
  triggers: string[];
}

export interface Approval {
  run_id: string;
  step_id: string;
  workflow: string;
  environment: string;
  role: string | null;
  required: number;
  approvals: number;
  /** 1-based level of a multi-level approval, and how many there are. */
  level: number;
  levels: number;
  step_up: "totp" | "passkey" | null;
  policy: string | null;
  /** Set when a delegated role makes this approval the caller's. */
  on_behalf_of?: string;
  requested_at: string;
  timeout_at: string | null;
  subject: unknown;
}

export interface Delegation {
  id: string;
  from_user: string;
  from_email: string;
  to_user: string;
  to_email: string;
  roles: string[];
  starts_at: string;
  ends_at: string;
  reason: string;
  created_at: string;
  revoked_at: string | null;
}

export interface PublishRequest {
  workflow_id: string;
  workflow: string;
  version: number;
  requested_by: string;
  requested_at: string;
  status: string;
}

export interface PolicyVersion {
  name: string;
  version: number;
  document: unknown;
  state: "pending" | "active" | "superseded" | "rejected";
  created_by: string;
  created_at: string;
  decided_by: string | null;
  decided_at: string | null;
}

export interface TriggerInfo {
  id: string;
  environment: string;
  type: string;
  version: number;
  path?: string;
  auth?: string;
  secret_name?: string;
  connector?: string;
  trigger?: string;
  cron?: string;
  timezone?: string;
  next_fire_at?: string;
  url?: string;
}

// A JSON Schema as connector manifests and WD inputs use it.
export interface JSONSchema {
  type?: string | string[];
  title?: string;
  description?: string;
  properties?: Record<string, JSONSchema>;
  required?: string[];
  items?: JSONSchema;
  enum?: unknown[];
  default?: unknown;
  format?: string;
  minimum?: number;
  maximum?: number;
  $ref?: string;
}
