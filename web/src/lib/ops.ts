// Pure helpers for the operator console (src/ops, docs/operator-console.md).

/** A review checklist item (engine/catalogue Checklist). */
export interface ChecklistItem {
  key: string;
  text: string;
}

/** The checklist keys not yet confirmed; approving needs none left. */
export function missingItems(items: ChecklistItem[], confirmed: Record<string, boolean>): string[] {
  return items.filter((i) => !confirmed[i.key]).map((i) => i.key);
}

/** The step-up target a review decision is bound to (api/ops.go). */
export const reviewTarget = (id: string, decision: "approve" | "reject") => `${id}/${decision}`;

/** The step-up target an incident update is bound to. */
export const updateTarget = (id: string, status: string) => `${id}/${status}`;

export const incidentStatuses = ["investigating", "identified", "monitoring", "resolved"];
export const maintenanceStatuses = ["scheduled", "in_progress", "completed"];
export const impacts = ["degraded", "partial_outage", "major_outage"];

/** The statuses an incident or maintenance window can move to. */
export const statusesFor = (kind: string) => (kind === "maintenance" ? maintenanceStatuses : incidentStatuses);

/** Whether an incident is over (its last status ends it). */
export const isClosed = (kind: string, status: string) => (kind === "maintenance" ? status === "completed" : status === "resolved");

/** "operator:ada@x" -> "ada@x"; other actors as they are. */
export const actorLabel = (actor: string) => actor.replace(/^(operator|reviewer):/, "");

/** Turns a datetime-local value into RFC 3339 (UTC), or undefined. */
export function toRFC3339(local: string): string | undefined {
  if (!local) return undefined;
  const d = new Date(local);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}
