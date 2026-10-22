// Thin client for the Taskiem REST API (api/server.go). The session is an
// HttpOnly cookie; every request carries the CSRF header the API requires.
// A field agent working in a business adds its session's header (below).

import { parseSession, sessionEnded, withAgentHeader, type AgentSession } from "./lib/agent";
import type { Trigger } from "@sdk/wd";

export interface Problem {
  path: string;
  message: string;
  /** In plain words, naming the step by its name (no-technical-steps N5). */
  plain?: string;
  /** Where it is put right: a web app path, or #step=<id> in the editor. */
  fix?: { label: string; path: string };
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

// --- a field agent's session in a business (docs/agents.md) ---
//
// While an agent works in a business, every request but their own
// (/v1/agent/*, sign-in, /v1/me) carries X-Taskiem-Agent-Session, so the
// storefront and table pages work unchanged inside the session. The
// session is kept for the tab (sessionStorage) so a reload stays in it.

const AGENT_KEY = "taskiem.agent-session";
let agentSession: AgentSession | null = null;
const agentListeners = new Set<() => void>();
try {
  agentSession = parseSession(globalThis.sessionStorage?.getItem(AGENT_KEY) ?? null);
} catch {
  // storage blocked: no session survives a reload
}

/** The business session the agent is working in, if any. */
export function currentAgentSession(): AgentSession | null {
  return agentSession;
}

/** Starts (or, with null, leaves) working in a business. */
export function setAgentSession(s: AgentSession | null) {
  agentSession = s;
  try {
    if (s) globalThis.sessionStorage?.setItem(AGENT_KEY, JSON.stringify(s));
    else globalThis.sessionStorage?.removeItem(AGENT_KEY);
  } catch {
    // storage blocked: kept in memory only
  }
  for (const fn of agentListeners) fn();
}

/** For useSyncExternalStore. */
export function subscribeAgentSession(fn: () => void): () => void {
  agentListeners.add(fn);
  return () => agentListeners.delete(fn);
}

function headersFor(path: string, headers: Record<string, string>): Record<string, string> {
  return withAgentHeader(path, { ...headers, "X-Taskiem-Request": "1" }, agentSession?.id);
}

/** A refusal saying the session ended: leave it, and tell the agent app. */
function noteEnded(status: number, body: Record<string, unknown>) {
  if (!agentSession || !sessionEnded(status, body)) return;
  const was = agentSession;
  setAgentSession(null);
  window.dispatchEvent(new CustomEvent("taskiem:agent-session-ended", { detail: was }));
}

export async function api<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  return request<T>(method, path, body === undefined ? undefined : JSON.stringify(body), { "Content-Type": "application/json" });
}

/** POSTs a multipart form (file uploads); the browser sets its boundary. */
export function upload<T = unknown>(path: string, form: FormData): Promise<T> {
  return request<T>("POST", path, form, {});
}

/** POSTs a file as the raw request body, with its own Content-Type (storefront photos). */
export function uploadFile<T = unknown>(path: string, file: Blob): Promise<T> {
  return request<T>("POST", path, file, { "Content-Type": file.type || "application/octet-stream" });
}

/** GETs a binary response (an image) with the session, as a Blob. */
export async function getBlob(path: string): Promise<Blob> {
  const res = await fetch(path, { credentials: "same-origin", headers: headersFor(path, {}) });
  if (!res.ok) {
    const text = await res.text();
    let msg = text || res.statusText;
    try {
      const body = JSON.parse(text) as { error?: string };
      msg = body.error || msg;
      noteEnded(res.status, body);
    } catch {
      // not JSON: the text itself
    }
    if (res.status === 401) window.dispatchEvent(new Event("taskiem:unauthorised"));
    throw new ApiError(res.status, msg);
  }
  return res.blob();
}

/** The message for a request that never reached Taskiem. */
export const OFFLINE = "You're offline, or Taskiem can't be reached. Check your connection and try again.";

async function request<T>(method: string, path: string, body: BodyInit | undefined, headers: Record<string, string>): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: headersFor(path, headers),
      body,
    });
  } catch {
    throw new ApiError(0, OFFLINE);
  }
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
    noteEnded(res.status, data);
    throw new ApiError(res.status, (data.error as string) ?? res.statusText, (data.problems as Problem[]) ?? [], data);
  }
  // Publishing ends an onboarding on the server: the agent app checks.
  if (agentSession && method === "POST" && path === "/v1/storefront/publish") {
    window.dispatchEvent(new CustomEvent("taskiem:agent-published", { detail: agentSession }));
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
export const patch = <T>(path: string, body?: unknown) => api<T>("PATCH", path, body ?? {});
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
  /** Null for a person with no membership answering invitations. */
  tenant_id: string | null;
  /** Signed in only to answer invitations: no organisation yet. */
  invitations_only?: boolean;
  roles: string[];
  permissions: string[];
  user?: { id: string; email: string; name: string; email_verified?: boolean };
  /** Whether the member has an authenticator app enrolled for step-up. */
  totp?: boolean;
  /** How this session signed in. */
  auth_method?: "password" | "passkey" | "sso";
  /** An administrator who must add a passkey before anything else. */
  enrol_passkey?: boolean;
  /** What the member proves themselves with to add or remove a passkey or
   * authenticator: a passkey or authenticator if they have one, else their
   * password. */
  factors?: { passkey: boolean; totp: boolean; password: boolean };
  /** A Taskiem field agent signed in as themselves: they land on the agent app. */
  field_agent?: boolean;
  /** Whether the business sees the technical parts (the Advanced switch, N0-5). */
  advanced?: boolean;
  /** The business's name, for the sidebar. */
  tenant_name?: string;
}

/** A tenant inviting the signed-in person (GET /v1/me/invitations). */
export interface Invitation {
  tenant_id: string;
  tenant: string;
  roles: string[];
  invited_at: string;
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
  /** What starts it: the published version's trigger, else the latest draft's. */
  trigger?: Trigger | null;
  /** Runs started on each of the last 14 days, oldest first. */
  recent?: number[];
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
  /** The AI build this version was saved from (the AI co-authored it). */
  ai_build?: string | null;
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

/** One credential a connection takes, with what guides the person pasting it. */
export interface AuthField {
  key: string;
  label: string;
  secret?: boolean;
  required?: boolean;
  /** One line of plain help. */
  help?: string;
  /** Where it is in the provider's dashboard. */
  where?: string;
  /** The provider's page that shows it. */
  link?: string;
  /** What a right value starts with, per mode (live, test). */
  prefixes?: { prefix: string; mode?: string; label?: string }[];
  /** Wrong values, with what to tell the person. */
  refuse?: { prefix: string; message: string }[];
  /** The value spans lines (a PEM key): a text area. */
  multiline?: boolean;
  /** A value Taskiem makes for the person: rsa_key_pair makes a key pair in the browser, keeps the private key and shows the public key to paste at the provider. */
  generate?: "rsa_key_pair";
}

export interface ConnectorInfo {
  ref: string;
  id: string;
  version: string;
  name: string;
  auth: {
    type: string;
    fields: AuthField[] | null;
    /** Declared by a connector that connects by OAuth (docs/connector-sdk.md). */
    oauth2?: {
      authorize_url: string;
      token_url: string;
      revoke_url?: string;
      scopes: string[];
      pkce: boolean;
      fields_from_token?: Record<string, string>;
      platform_client_fields?: Record<string, string>;
    };
  };
  /** For a connector with `auth.oauth2`: whether it can be connected by OAuth on this platform. */
  oauth_linkable?: boolean;
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
  step_up: "whatsapp_pin" | "totp" | "passkey" | null;
  policy: string | null;
  /** Set when a delegated role makes this approval the caller's. */
  on_behalf_of?: string;
  /** The step's name (its id when it has none). */
  step_name: string;
  /** Nobody else can decide it, so the caller may approve even what they made. */
  sole_approver: boolean;
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
  /** A remotely registered trigger: the subscription Taskiem keeps at the provider (decision 0021). */
  remote?: RemoteSubscription;
  /** Taskiem also asks the provider every few minutes, so setting the address up there is optional. */
  polled?: boolean;
  /** The connector's name, for a connector event. */
  service?: string;
  /** An email trigger's address (decision 0038). */
  email?: string;
}

/** A mail provider's request to confirm forwarding to a workflow's address. */
export interface EmailConfirmation {
  provider: string;
  code?: string;
  link?: string;
  for?: string;
  received_at: string;
}

/** A message that arrived at a workflow's address: what came of it, not its content. */
export interface EmailMessage {
  id: string;
  sender_domain: string | null;
  size: number;
  attachments: number;
  status: "started" | "refused" | "confirmation";
  note: string | null;
  run_id: string | null;
  received_at: string;
}

/** A workflow's email address in one environment. */
export interface EmailEndpoint {
  id: string;
  environment: string;
  address: string;
  status: "live" | "closed" | "suspended";
  version: number;
  senders: string[];
  attachments: "keep" | "ignore";
  confirmation: EmailConfirmation | null;
  messages: EmailMessage[];
}

/** A provider subscription Taskiem created for a trigger (a Xenibase webhook). */
export interface RemoteSubscription {
  id: string;
  environment: string;
  connector: string;
  trigger: string;
  /** ok, pending, failed, failing, paused, broken, missing or removing */
  health: string;
  status_reason: string | null;
  last_error: string | null;
  checked_at: string | null;
  last_test_at: string | null;
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
