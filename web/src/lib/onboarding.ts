// Getting started (GET /v1/onboarding, docs/onboarding.md): the checklist's
// wording, the guided first workflow's helpers, and the in-product help.
import type { JSONSchema } from "../api";
import type { Template } from "./templates";

export interface Onboarding {
  items: { id: ChecklistId; done: boolean }[];
  done: number;
  total: number;
  complete: boolean;
  dismissed: boolean;
  self_serve: boolean;
  email_verified: boolean;
  can_send_email: boolean;
  docs_url: string;
  signed_up_at: string | null;
  first_run_at: string | null;
  seconds_to_first_run: number | null;
}

export type ChecklistId = "verify_email" | "connection" | "workflow" | "publish" | "first_run" | "invite";

/** Each checklist step: what it says, and where it is done. */
export const CHECKLIST: Record<ChecklistId, { title: string; text: string; href: string; action: string }> = {
  verify_email: {
    title: "Confirm your email",
    text: "Open the link we emailed you. Until then you can build and run, but not invite people or make API keys.",
    href: "/start",
    action: "Send the link again",
  },
  connection: {
    title: "Connect an app",
    text: "Add the credentials of a service your workflow uses, such as your Termii or Paystack account. They are encrypted and never shown again.",
    href: "/connections",
    action: "Add a connection",
  },
  workflow: {
    title: "Create a workflow",
    text: "Start from a ready template for small businesses, or build your own.",
    href: "/start/guide",
    action: "Pick a template",
  },
  publish: {
    title: "Publish it",
    text: "A draft does nothing. Publishing checks it and makes it live.",
    href: "/workflows",
    action: "Open your workflows",
  },
  first_run: {
    title: "Run it once",
    text: "Start a run and watch each step complete.",
    href: "/workflows",
    action: "Start a run",
  },
  invite: {
    title: "Invite a teammate",
    text: "Add someone to approve payments, watch runs or build with you.",
    href: "/members",
    action: "Invite someone",
  },
};

/** Templates suited to a first workflow: one connector, nothing to wait
 * for, in this order when present; then the rest of the library. */
export const STARTERS = ["new-order-alert-sms", "payment-thank-you-sms", "payroll-reminder", "low-balance-alert"];

export function starterTemplates(list: Template[]): Template[] {
  const rank = (t: Template) => {
    const i = STARTERS.indexOf(t.id);
    return i < 0 ? STARTERS.length + t.connectors.length : i;
  };
  return [...list].filter((t) => t.available).sort((a, b) => rank(a) - rank(b) || a.title.localeCompare(b.title));
}

/** "4 min 05 s", "2 h 10 min", "3 days". */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s} s`;
  if (s < 3600) return `${Math.floor(s / 60)} min ${String(s % 60).padStart(2, "0")} s`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ${Math.floor((s % 3600) / 60)} min`;
  const d = Math.floor(s / 86400);
  return d === 1 ? "1 day" : `${d} days`;
}

/** A sample input for a workflow's inputs schema, for a first test run:
 * each property's default, example or first enum value, else a plain
 * value of its type. Only required properties are filled. */
export function sampleInput(schema: JSONSchema | undefined, types: Record<string, JSONSchema> = {}, depth = 0): unknown {
  if (!schema || depth > 5) return {};
  if (schema.$ref) {
    const name = schema.$ref.replace(/^#\/types\//, "");
    return sampleInput(types[name], types, depth + 1);
  }
  const s = schema as JSONSchema & { examples?: unknown[]; minLength?: number };
  if (s.default !== undefined) return s.default;
  if (Array.isArray(s.examples) && s.examples.length > 0) return s.examples[0];
  if (s.enum && s.enum.length > 0) return s.enum[0];
  const t = Array.isArray(s.type) ? s.type.find((x) => x !== "null") : s.type;
  switch (t) {
    case "object": {
      const out: Record<string, unknown> = {};
      for (const k of s.required ?? []) out[k] = sampleInput(s.properties?.[k], types, depth + 1);
      return out;
    }
    case "array":
      return [];
    case "integer":
    case "number":
      return s.minimum !== undefined && s.minimum > 0 ? s.minimum : 1000;
    case "boolean":
      return true;
    case "string":
      if (s.format === "email") return "customer@example.com";
      if (s.format === "date") return new Date().toISOString().slice(0, 10);
      return "TEST-1";
  }
  return {};
}

/** In-product help: a few plain sentences per page, and the docs page for
 * more when the deployment has a docs site. */
export type HelpTopic = "start" | "templates" | "connections" | "variables";

export const HELP: Record<HelpTopic, { title: string; body: string[]; doc: string }> = {
  start: {
    title: "How Taskiem works",
    body: [
      "A workflow is a trigger (a schedule, a web address your shop calls, a payment) and the steps that follow it.",
      "Connections hold the credentials of the services steps use. Variables hold settings such as the owner's phone number.",
      "A workflow runs only once it is published. Every run is recorded step by step under Runs.",
    ],
    doc: "onboarding",
  },
  templates: {
    title: "About templates",
    body: [
      "A template is a ready workflow. Fill in its details and it becomes a draft you can review, change and publish.",
      "Templates that move money always ask for an approval first.",
    ],
    doc: "templates",
  },
  connections: {
    title: "About connections",
    body: [
      "A connection is your account with a service, such as Termii for SMS or Paystack for payments.",
      "Each environment (prod, dev) has its own connections. Templates use the connection named main unless you choose another.",
      "Credentials are encrypted when saved and never shown again; to change them, add the connection again.",
    ],
    doc: "connector-sdk",
  },
  variables: {
    title: "About variables",
    body: [
      "Variables are settings workflows read, such as owner_phone. Phone numbers are in international format without +, e.g. 2348012345678.",
      "Secrets are for passwords and keys: they are encrypted and never shown again.",
    ],
    doc: "environments",
  },
};

/** The docs page for a help topic, or null without a docs site. */
export function helpLink(docsURL: string | undefined, doc: string): string | null {
  return docsURL ? `${docsURL.replace(/\/+$/, "")}/${doc}` : null;
}
