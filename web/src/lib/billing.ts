// Billing helpers for the Billing page (docs/billing.md): money in kobo,
// usage against plan limits, and downgrade blockers.

export interface Plan {
  id: string;
  name: string;
  tier: string;
  monthly_kobo: number;
  annual_kobo: number;
  limits: Record<string, number>;
  features: Record<string, boolean>;
  public: boolean;
}

export interface Subscription {
  plan: string;
  interval: "monthly" | "annual";
  status: "trial" | "active" | "past_due" | "degraded" | "cancelled" | "comped";
  period_start: string;
  period_end: string;
  trial_end?: string;
  comp_until?: string;
  past_due_since?: string;
  cancel_at_period_end: boolean;
  pending_plan?: string;
  pending_interval?: string;
  credit_kobo: number;
  billing_email?: string;
}

export interface InvoiceLine {
  kind: string;
  description: string;
  quantity: number;
  unit_kobo: number;
  amount_kobo: number;
}

export interface Invoice {
  id: string;
  number: string;
  kind: string;
  plan: string;
  interval: string;
  period_start: string;
  period_end: string;
  lines: InvoiceLine[];
  subtotal_kobo: number;
  vat_rate_bp: number;
  vat_kobo: number;
  total_kobo: number;
  status: string;
  issued_at: string;
  due_at: string;
  paid_at?: string;
}

export interface Blocker {
  limit: string;
  used: number;
  allowed: number;
  remove: string;
}

export interface Banner {
  level: "warning" | "error";
  message: string;
}

export interface Snapshot {
  day: string;
  runs_started: number;
  steps: number;
  active_workflows: number;
  running_runs: number;
  stored_runs: number;
  whatsapp_templates: number;
  whatsapp_overage: number;
  ai_tokens: number;
}

export interface BillingOverview {
  enabled: boolean;
  placeholder_prices: boolean;
  plan: Plan;
  subscription: Subscription | null;
  inherited: boolean;
  plans: Plan[];
  limits: Record<string, number>;
  usage: {
    runs_today: number;
    runs_this_month: number;
    running_runs: number;
    queued_runs: number;
    workflows: number;
    secrets: number;
    connections: number;
    ai_tokens_this_month: number;
    whatsapp_templates_this_month?: { sent: number; overage: number };
  };
  history: Snapshot[];
  subtenant_usage?: (Snapshot & { subtenants: number })[];
  invoices: Invoice[];
  open_invoice?: Invoice;
  pending_plan?: Plan;
  banner?: Banner;
  vat_percent: number;
  providers: string[] | null;
  payment_method?: { provider: string; brand: string; last4: string; exp: string };
}

/** Kobo as naira: ₦12,500.00. */
export function naira(kobo: number): string {
  const neg = kobo < 0;
  const abs = Math.abs(Math.trunc(kobo));
  const whole = Math.floor(abs / 100).toLocaleString("en-NG");
  const frac = String(abs % 100).padStart(2, "0");
  return `${neg ? "-" : ""}₦${whole}.${frac}`;
}

export interface Meter {
  key: string;
  label: string;
  used: number;
  limit: number; // 0: no limit
  /** 0..1 of the limit, or null when there is none. */
  fraction: number | null;
  level: "ok" | "warn" | "full";
}

const meterDefs: [key: string, label: string, used: (u: BillingOverview["usage"]) => number][] = [
  ["max_running_runs", "Runs running now", (u) => u.running_runs],
  ["max_workflows", "Workflows", (u) => u.workflows],
  ["max_queued_runs", "Runs queued", (u) => u.queued_runs],
  ["runs_per_month", "Runs this month", (u) => u.runs_this_month],
  ["ai_monthly_tokens", "AI tokens this month", (u) => u.ai_tokens_this_month],
  ["whatsapp_templates_monthly", "WhatsApp templates this month", (u) => u.whatsapp_templates_this_month?.sent ?? 0],
  ["max_secrets", "Secrets", (u) => u.secrets],
  ["max_connections", "Connections", (u) => u.connections],
];

/** Usage against the plan's limits, one meter per counted limit. */
export function meters(limits: Record<string, number>, usage: BillingOverview["usage"]): Meter[] {
  return meterDefs.map(([key, label, used]) => {
    const limit = limits[key] ?? 0;
    const n = used(usage);
    const fraction = limit > 0 ? Math.min(n / limit, 1) : null;
    const level = fraction === null ? "ok" : n >= limit ? "full" : fraction >= 0.8 ? "warn" : "ok";
    return { key, label, used: n, limit, fraction, level };
  });
}

/** Whether moving from one plan to another is an upgrade (paid now) or a
 * downgrade (at period end), as the server decides it. */
export function isUpgrade(tiers: string[], from: Plan, fromInterval: string, to: Plan, toInterval: string): boolean {
  if (from.id === to.id) return fromInterval === "monthly" && toInterval === "annual";
  const a = tiers.indexOf(from.tier);
  const b = tiers.indexOf(to.tier);
  if (a !== b) return b > a;
  return to.monthly_kobo > from.monthly_kobo;
}

export const tiers = ["internal", "starter", "growth", "business", "enterprise"];

export const featureNames: Record<string, string> = {
  sso: "Single sign-on",
  scim: "SCIM provisioning",
  custom_roles: "Custom roles",
  white_label: "White-label domains",
  byok: "Bring your own key",
  git: "Git integration",
  ai: "AI building",
  embedded: "Embedding and partner API",
};

export const statusText: Record<Subscription["status"], string> = {
  trial: "Trial",
  active: "Active",
  past_due: "Past due",
  degraded: "Unpaid: new runs paused",
  cancelled: "Cancelled",
  comped: "Granted by your operator",
};

/** The text of a 409 downgrade_blocked answer, one line per blocker. */
export function blockerLines(body: Record<string, unknown>): string[] {
  const b = body.blockers;
  if (!Array.isArray(b)) return [];
  return (b as Blocker[]).map((x) => x.remove);
}
