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
  const res = await fetch(path, {
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
export const del = <T>(path: string) => api<T>("DELETE", path);

// --- response shapes ---

export interface Me {
  tenant_id: string;
  roles: string[];
  permissions: string[];
  user?: { id: string; email: string; name: string };
}

export interface WorkflowSummary {
  id: string;
  name: string;
  active_version: number | null;
  latest_version: number;
  created_at: string;
}

export interface VersionInfo {
  version: number;
  state: "draft" | "published" | "deprecated" | "archived";
  digest: string;
  created_by: string | null;
  created_at: string;
  published_by: string | null;
  published_at: string | null;
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
  requested_at: string;
  timeout_at: string | null;
  subject: unknown;
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
