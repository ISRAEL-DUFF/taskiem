// The operator console's client (api/ops.go): its own session cookie,
// scoped to /v1/ops; the CSRF header on every request; a passkey step-up
// on every write.

import { ApiError } from "../api";
import { assertFrom, createFrom, type CreationJSON, type RequestJSON } from "../passkeys";
import type { Card, Image, Item, Storefront } from "../lib/storefront";
import type { Finding, Verification } from "../lib/trust";

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

/** A field agent, with what they have done (docs/agents.md). */
export interface OpsAgent {
  id: string;
  code: string;
  name: string;
  phone: string;
  region: string;
  status: "active" | "suspended" | "retired";
  checks: { id_type?: string; id_checked?: boolean; referee_name?: string; referee_phone?: string };
  registered_by: string;
  created_at: string;
  businesses: number;
  published: number;
  verified: number;
  live_sessions: number;
}

export interface OpsAgentBusiness {
  tenant_id: string;
  name: string;
  status: string;
  created_at: string;
  published: boolean;
  verified: boolean;
}

// --- storefront trust and safety (api/trust.go, docs/moderation.md) ---

/** A storefront review in the queue. */
export interface OpsStorefrontReview {
  id: string;
  tenant_id: string;
  business: string;
  storefront_id: string;
  slug: string;
  page_status: string;
  kind: string;
  version: number | null;
  status: "pending" | "approved" | "rejected";
  findings: Finding[] | null;
  /** By kind: business {legal_name, address, cac_number, note}; appeal {message, page_status}; reports {reporters}; first_publish {verified, model_skipped}. */
  detail: Record<string, unknown> | null;
  created_by: string;
  created_at: string;
  decided_by: string | null;
  decision_note: string | null;
  decided_at: string | null;
  verified: boolean;
  agent_code: string | null;
  open_reports: number;
}

/** What a reviewer needs: the page, its published model, catalogue, photos, reports and verification. */
export interface OpsStorefrontReviewDetail {
  review: OpsStorefrontReview;
  storefront: Storefront;
  published?: { version: number; slug: string; card: Card; logo?: Image; map_link?: string } & Record<string, unknown>;
  items: Item[];
  photos: { id: string; urls: Image | null; moderation: string }[];
  reports: { id: string; reason: string; message: string; has_contact: boolean; status: string; created_at: string }[];
  verification: Verification;
}

/** A visitor's report of a page or form. */
export interface OpsReport {
  id: string;
  tenant_id: string;
  business: string;
  kind: string;
  site_id: string;
  slug: string | null;
  reason: string;
  message: string;
  has_contact: boolean;
  status: "open" | "dismissed" | "actioned";
  created_at: string;
  handled_by: string | null;
  handled_at: string | null;
  reporters_on_site: number;
  agent_code: string | null;
}

// --- people chats' safety (api/ops_people.go, api/lawful.go) ---

export interface PeopleOverview {
  open: { people_reports: number; customer_reports: number; held: number; appeals: number; lawful_requests: number };
  storage: { table: string; rows: number; bytes: number }[];
  storage_words: string;
}

export interface OpsPeopleReportRow {
  id: string;
  reporter_id: string | null;
  detached: boolean;
  reported_id: string | null;
  reported_name: string;
  reported_gone: boolean;
  reported_suspended: boolean;
  reason: string;
  note: string;
  from_request: boolean;
  files: number;
  sealed: boolean;
  status: "open" | "reviewed";
  created_at: string;
  reviewed_by: string | null;
  reviewed_at: string | null;
  reports_against: number;
}

export interface OpsCustomerReportRow {
  id: string;
  reporter_id: string | null;
  detached: boolean;
  tenant_id: string;
  business: string;
  reason: string;
  note: string;
  messages: number;
  status: "open" | "reviewed";
  created_at: string;
  reviewed_by: string | null;
  reviewed_at: string | null;
}

/** One message a report keeps, opened. */
export interface OpsReportMessage {
  id?: string;
  from_reporter: boolean;
  kind: string;
  body: string;
  card?: Record<string, unknown>;
  location?: { lat: number; lng: number; label?: string };
  attachment?: { kind?: string; type?: string; size?: number; name?: string };
  deleted?: boolean;
  file?: boolean;
  file_left_out?: boolean;
  at: string;
}

export interface OpsPeopleReport {
  report: {
    id: string;
    reporter_id: string | null;
    reported_id: string | null;
    reported_name: string;
    reason: string;
    note: string;
    from_request: boolean;
    status: "open" | "reviewed";
    created_at: string;
    reviewed_by: string | null;
    reviewed_at: string | null;
    review_note: string | null;
  };
  messages: OpsReportMessage[];
  files: { message_id: string; type: string; size: number }[];
  sealed: boolean;
}

export interface OpsCustomerReport {
  report: {
    id: string;
    reporter_id: string | null;
    tenant_id: string;
    business: string;
    reason: string;
    note: string;
    messages: { from_customer: boolean; kind: string; body: string; offer?: boolean; at: string }[] | null;
    status: "open" | "reviewed";
    created_at: string;
    reviewed_by: string | null;
    reviewed_at: string | null;
    review_note: string | null;
  };
}

export interface OpsHeldRow {
  id: string;
  sender_id: string | null;
  sender_name: string;
  recipient_id: string | null;
  recipient_name: string;
  kind: string;
  reasons: string[];
  has_file: boolean;
  state: "held" | "released" | "confirmed";
  created_at: string;
  decided_by: string | null;
  decided_at: string | null;
}

export interface OpsHeld {
  held: OpsHeldRow & { file_type: string | null; file_size: number | null; decision_note: string | null; released_message: string | null };
  message: { body?: string; card?: Record<string, unknown>; location?: { lat: number; lng: number; label?: string }; name?: string };
}

export interface OpsVerdict {
  from_person: string;
  from_name: string;
  to_person: string | null;
  to_name: string;
  verdict: string;
  reasons: string[];
  at: string;
}

export interface OpsPersonAction {
  id: string;
  action: "warn" | "limit" | "suspend" | "close";
  reason: string;
  operator: string;
  report_id: string | null;
  until: string | null;
  lifted_at: string | null;
  lifted_by: string | null;
  lift_reason: string | null;
  closed_at: string | null;
  close_note: string | null;
  created_at: string;
  appeal_id: string | null;
  appeal_state: string | null;
  appeal_text: string | null;
  appeal_at: string | null;
}

export interface OpsPerson {
  person: {
    id: string;
    name: string;
    active: boolean;
    deleted: boolean;
    suspended: boolean;
    created_at: string;
    reports_against: number;
    held: number;
    verdicts_week: number;
    device_accounts: number;
    requests_waiting: number;
    requests_accepted: number;
    requests_refused: number;
  };
  actions: OpsPersonAction[];
}

export interface OpsAppeal {
  id: string;
  action_id: string;
  person_id: string;
  name: string;
  action: "warn" | "limit" | "suspend" | "close";
  action_reason: string;
  operator: string;
  until: string | null;
  text: string;
  state: "open" | "upheld" | "overturned";
  created_at: string;
  decided_by: string | null;
  decided_at: string | null;
  decision_note: string | null;
}

export interface OpsLawfulRequest {
  id: string;
  authority: string;
  reference: string;
  legal_basis: string;
  person_id: string | null;
  person_name: string;
  from: string;
  to: string;
  with_person_id: string | null;
  files: boolean;
  scope_note: string;
  received_at: string;
  recorded_by: string;
  recorded_at: string;
  approved_by: string | null;
  approved_at: string | null;
  state: "recorded" | "approved" | "answered" | "refused";
  answered_by: string | null;
  answered_at: string | null;
  messages_given: number | null;
  refused_reason: string | null;
  told_at: string | null;
  not_told_reason: string | null;
}
