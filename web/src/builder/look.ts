// How a step looks on the builder's canvas: its icon on a tint, what it is
// called and the line under the name ("WhatsApp · Send a message"), in an
// owner's words (docs/brand/screens.md; no-technical-steps Appendix C).
import type { Step } from "@sdk/wd";

export type Tone = "brand" | "ok" | "warn" | "info" | "muted";

export interface Look {
  icon: string;
  tone: Tone;
  /** What the step is called when it has no name of its own. */
  title: string;
  /** The line under the name: the service and what it does there. */
  line: string;
}

interface ConnectorLike {
  ref: string;
  id: string;
  name: string;
  category?: string;
  actions: Record<string, { title?: string }>;
}

const words = (id: string) => (id.charAt(0).toUpperCase() + id.slice(1)).replaceAll("_", " ").replaceAll("-", " ");

/** A connector's look by what kind of service it is. */
function serviceLook(id: string, category = ""): { icon: string; tone: Tone } {
  if (["gmail", "email"].includes(id)) return { icon: "mail", tone: "info" };
  if (["googlesheets", "xenibase", "postgres", "mysql"].includes(id) || category === "database") return { icon: "tables", tone: "ok" };
  if (category === "payments" || category === "open_banking") return { icon: "card", tone: "warn" };
  if (category === "messaging") return { icon: "chat", tone: "ok" };
  if (["invoices"].includes(id)) return { icon: "invoices", tone: "warn" };
  if (["storefront"].includes(id)) return { icon: "orders", tone: "brand" };
  if (["schools"].includes(id)) return { icon: "schools", tone: "brand" };
  if (category === "identity") return { icon: "lock", tone: "muted" };
  if (category === "storage") return { icon: "inbox", tone: "muted" };
  return { icon: "connections", tone: "muted" };
}

/** A step's look on the canvas. */
export function lookOf(s: Step, connectors: ConnectorLike[] = []): Look {
  switch (s.type) {
    case "connector": {
      const id = s.connector.split("@")[0] ?? "";
      const c = connectors.find((x) => x.ref === s.connector || x.id === id);
      const service = c?.name ?? words(id);
      const action = c?.actions[s.action]?.title ?? words(s.action);
      return { ...serviceLook(id, c?.category), title: action, line: `${service} · ${action}` };
    }
    case "capability":
      return { icon: "card", tone: "warn", title: words(s.uses), line: s.connection ? `${words(s.uses)} · ${s.connection}` : `${words(s.uses)} · your payments account` };
    case "approval":
      return { icon: "approvals", tone: "brand", title: "Ask for approval", line: s.config.role ? `Approval · ${words(s.config.role)}` : "Approval" };
    case "form":
      return { icon: "forms", tone: "brand", title: "Ask someone to fill in a form", line: `Form · ${s.config.title}` };
    case "wait":
      return { icon: "clock", tone: "info", title: "Wait", line: "duration" in s.config ? `Wait · ${s.config.duration}` : "Wait until a time" };
    case "signal":
      return { icon: "inbox", tone: "info", title: "Wait for a reply", line: "Wait for a reply" };
    case "callback":
      return { icon: "inbox", tone: "info", title: "Wait to be called back", line: "Wait to be called back" };
    case "branch":
      return { icon: "split", tone: "info", title: "Is it so?", line: s.config.paths.length === 1 && s.config.default ? "If · Yes or No" : `Choose · ${s.config.paths.length + (s.config.default ? 1 : 0)} paths` };
    case "parallel":
      return { icon: "split", tone: "info", title: "Do these together", line: `Together · ${s.config.branches.length} at once` };
    case "foreach":
      return { icon: "loop", tone: "info", title: "For each", line: "For each item in a list" };
    case "ai":
      return { icon: "sparkle", tone: "brand", title: "Ask the AI", line: "AI · write, read or sort" };
    case "subflow":
      return { icon: "workflows", tone: "brand", title: "Run another workflow", line: "Your workflow · run it here" };
    case "template":
      return { icon: "templates", tone: "brand", title: s.template ? words(s.template) : "Ready workflow", line: "Ready workflow · its steps run here" };
    case "transform":
      return { icon: "code", tone: "muted", title: "Change the data", line: "Advanced · change the data" };
    case "http":
      return { icon: "code", tone: "muted", title: "Call a web address", line: `Advanced · ${s.config.method}` };
    case "code":
      return { icon: "code", tone: "muted", title: "Run code", line: `Advanced · ${s.config.language}` };
    case "respond":
      return { icon: "code", tone: "muted", title: "Answer the caller", line: "Advanced · answer the caller" };
    default:
      return { icon: "workflows", tone: "muted", title: words(s.type), line: words(s.type) };
  }
}

/** A step's name on the canvas: its own, else what it does. */
export const stepTitle = (s: Step, connectors: ConnectorLike[] = []) => s.name || lookOf(s, connectors).title;
