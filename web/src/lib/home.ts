// Home (pages/Home.tsx) and the workflows list: what a business's day
// looks like, in plain words.
import type { Trigger } from "@sdk/wd";
import type { Order } from "./orders";
import { scheduleOf, scheduleWords } from "./plaincanvas";

const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();

/** Money taken today: orders paid since midnight, local time. */
export function moneyToday(orders: Pick<Order, "paid_at" | "total_kobo" | "status">[], now = new Date()): { kobo: number; count: number } {
  let kobo = 0;
  let count = 0;
  for (const o of orders) {
    if (!o.paid_at || o.status === "refunded" || !sameDay(new Date(o.paid_at), now)) continue;
    kobo += o.total_kobo;
    count++;
  }
  return { kobo, count };
}

/** Orders that came in today, and how many of them wait for the owner. */
export function ordersToday(orders: Pick<Order, "created_at" | "status">[], now = new Date()): { count: number; waiting: number } {
  const today = orders.filter((o) => sameDay(new Date(o.created_at), now));
  return { count: today.length, waiting: orders.filter((o) => o.status === "received" || o.status === "price_changed" || o.status === "refund_due").length };
}

const service = (ref: unknown) => {
  const id = typeof ref === "string" ? ref.split("@")[0] ?? "" : "";
  return id ? id.charAt(0).toUpperCase() + id.slice(1) : "a service";
};

/** What starts a workflow, as its list and Home say it. */
export function triggerWords(t: Trigger | null | undefined): string {
  if (!t) return "Not set up yet";
  const c = t.config ?? {};
  switch (t.type) {
    case "schedule": {
      const s = typeof c.cron === "string" ? scheduleOf(c.cron) : null;
      return s ? scheduleWords(s) : "On a schedule";
    }
    case "manual":
      return "When someone starts it";
    case "storefront":
      return "An order comes in";
    case "form":
      return "Someone fills in a form";
    case "invoice":
      return c.event === "payment" ? "A payment is counted towards an invoice" : "An invoice is paid";
    case "whatsapp":
      return "A WhatsApp message comes in";
    case "ussd":
      return "Someone dials your USSD code";
    case "email":
      return "An email comes in";
    case "webhook":
      return "Another app tells it";
    case "connector_event":
      return `${service(c.connector)} tells it something happened`;
    case "capability_event":
      return "Your payments account tells it about a payment";
    case "polling":
      return `It checks ${service(c.connector)} regularly`;
    case "database_change":
      return "A database changes";
    case "subflow":
      return "Another workflow starts it";
    default:
      return "Something starts it";
  }
}

/** A run's status as a sentence's end: "finished", "failed", … */
export function runWords(status: string): string {
  switch (status) {
    case "completed":
      return "finished";
    case "failed":
      return "failed";
    case "waiting":
      return "is waiting";
    case "queued":
    case "running":
      return "is running";
    case "needs_reconciliation":
      return "needs checking";
    case "cancelled":
      return "was stopped";
    default:
      return status.replaceAll("_", " ");
  }
}

/** Who or what started a run, for "Started by …": a member's name, else
 * what its trigger is ("its schedule", "an order"); an email or a form
 * says so (email:…, form:…), and so does an invoice paid (invoice:…);
 * another id that is no member's is an app or API key. */
export function startedByWords(startedBy: string | null, triggerType: string | undefined, names: Map<string, string>): string {
  if (startedBy?.startsWith("email:")) return "an email";
  if (startedBy?.startsWith("form:")) return "a form";
  if (startedBy?.startsWith("invoice:")) return "a paid invoice";
  if (startedBy?.startsWith("subflow:")) return "another workflow";
  if (startedBy) return names.get(startedBy) ?? "an app or API key";
  switch (triggerType) {
    case "schedule":
      return "its schedule";
    case "storefront":
      return "an order";
    case "form":
      return "a form";
    case "whatsapp":
      return "a WhatsApp message";
    case "ussd":
      return "a USSD session";
    case "email":
      return "an email";
    case "webhook":
    case "connector_event":
    case "capability_event":
    case "polling":
      return "another app";
    case "subflow":
      return "another workflow";
    default:
      return "Taskiem";
  }
}
