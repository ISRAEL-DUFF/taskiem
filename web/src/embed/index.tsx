// The embeddable bundle (/embed/v1/taskiem.js; docs/embedding.md). It
// defines three custom elements, each rendering into its own shadow root:
//
//   <taskiem-builder app="…" token="…">  the builder
//   <taskiem-runs app="…" token="…">     the run list and live run view
//   <taskiem-frame …>                    the frame page's root (iframe mode)
//
// The first two run on the partner's page and call the embed API on the
// bundle's own origin with the end user's token. The third runs in the
// frame page and receives its token by postMessage from the page that
// framed it, checking that page's origin against the app's.
import { StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import xyflowCss from "@xyflow/react/dist/style.css?inline";
import consoleCss from "../styles.css?inline";
import embedCss from "./embed.css?inline";
import { EmbedClient, type ExpiringDetail } from "./client";
import { EmbedApp, type Emit } from "./EmbedApp";
import { checkMessage, frameTargets, type FrameMessage } from "./frame";

// The API is wherever the bundle was loaded from: the platform, or the
// app's custom domain.
const scriptOrigin = (() => {
  const s = document.currentScript as HTMLScriptElement | null;
  try {
    return s?.src ? new URL(s.src).origin : location.origin;
  } catch {
    return location.origin;
  }
})();

const css = xyflowCss + consoleCss.replaceAll(":root", ":host") + embedCss;

type View = "builder" | "runs";

/** Mounts the builder in a shadow root and relays its events. */
class Mount {
  private root: Root;
  constructor(host: HTMLElement) {
    const shadow = host.shadowRoot ?? host.attachShadow({ mode: "open" });
    shadow.replaceChildren();
    const style = document.createElement("style");
    style.textContent = css;
    const div = document.createElement("div");
    div.style.height = "100%";
    shadow.append(style, div);
    this.root = createRoot(div);
  }
  render(client: EmbedClient, view: View, emit: Emit) {
    this.root.render(
      <StrictMode>
        <EmbedApp client={client} view={view} emit={emit} />
      </StrictMode>,
    );
  }
  unmount() {
    this.root.unmount();
  }
}

/**
 * <taskiem-builder> and <taskiem-runs>. Attributes: app (required), token
 * (or call setToken), base (the API's origin; default the bundle's).
 * Events (bubbling, composed): taskiem-loaded, taskiem-token-expiring
 * (detail: expiresAt, remaining), taskiem-token-expired, taskiem-published,
 * taskiem-publish-requested (four-eyes: a version waits for approval),
 * taskiem-run-started, taskiem-run-completed, taskiem-run-ended.
 */
abstract class TaskiemElement extends HTMLElement {
  static observedAttributes = ["token", "app", "base"];
  protected abstract view: View;
  private mount?: Mount;
  private client?: EmbedClient;
  private token = "";

  /** Gives the element a token (a new one before the current expires). */
  setToken(token: string) {
    this.token = token;
    if (this.client) this.client.setToken(token);
    else this.start();
  }

  connectedCallback() {
    this.token ||= this.getAttribute("token") ?? "";
    this.start();
  }

  disconnectedCallback() {
    this.client?.close();
    this.mount?.unmount();
    this.client = undefined;
    this.mount = undefined;
  }

  attributeChangedCallback(name: string, old: string | null, value: string | null) {
    if (old === value || !this.isConnected) return;
    if (name === "token" && value && this.client) return this.setToken(value);
    // A different app or API: start over.
    this.disconnectedCallback();
    if (name === "token" && value) this.token = value;
    this.start();
  }

  private emit: Emit = (name, detail) => {
    this.dispatchEvent(new CustomEvent(`taskiem-${name}`, { detail, bubbles: true, composed: true }));
  };

  private start() {
    const app = this.getAttribute("app");
    if (this.client || !app || !this.token) return;
    const client = new EmbedClient({ base: (this.getAttribute("base") ?? scriptOrigin).replace(/\/+$/, ""), app, token: this.token });
    client.addEventListener("token-expiring", (e) => this.emit("token-expiring", { ...(e as CustomEvent<ExpiringDetail>).detail }));
    client.addEventListener("token-expired", () => this.emit("token-expired", {}));
    this.client = client;
    this.mount = new Mount(this);
    this.mount.render(client, this.view, this.emit);
  }
}

class TaskiemBuilder extends TaskiemElement {
  protected view: View = "builder";
}
class TaskiemRuns extends TaskiemElement {
  protected view: View = "runs";
}

/**
 * <taskiem-frame app view parent-origins>: the frame page's root. It
 * announces itself to the page that framed it (taskiem:ready), takes a
 * token only from that page and only if its origin is one of the app's
 * (taskiem:token), and from then on talks to that one origin alone.
 */
class TaskiemFrame extends HTMLElement {
  private parentOrigin = "";
  private client?: EmbedClient;
  private mount?: Mount;
  private origins: string[] = [];

  private onMessage = (e: MessageEvent) => {
    const msg = checkMessage(e, window.parent, this.origins, this.parentOrigin);
    if (!msg) return;
    if (!this.client) {
      this.parentOrigin = e.origin;
      this.start(msg.token);
    } else {
      this.client.setToken(msg.token);
    }
  };

  private post(m: FrameMessage, targets: string[]) {
    for (const o of targets) window.parent.postMessage(m, o);
  }

  private emit: Emit = (name, detail) => {
    if (this.parentOrigin) this.post({ type: `taskiem:${name}`, ...detail }, [this.parentOrigin]);
  };

  connectedCallback() {
    this.origins = (this.getAttribute("parent-origins") ?? "").split(" ").filter(Boolean);
    if (window.parent === window || this.origins.length === 0) {
      this.textContent = "This page is shown inside a partner's product.";
      return;
    }
    window.addEventListener("message", this.onMessage);
    const ancestors = (location as Location & { ancestorOrigins?: DOMStringList }).ancestorOrigins;
    this.post({ type: "taskiem:ready", app: this.getAttribute("app") ?? "" }, frameTargets(this.origins, ancestors?.[0]));
  }

  disconnectedCallback() {
    window.removeEventListener("message", this.onMessage);
    this.client?.close();
    this.mount?.unmount();
  }

  private start(token: string) {
    const app = this.getAttribute("app") ?? "";
    const client = new EmbedClient({ base: location.origin, app, token, parentOrigin: this.parentOrigin });
    client.addEventListener("token-expiring", (e) => this.emit("token-expiring", { ...(e as CustomEvent<ExpiringDetail>).detail }));
    client.addEventListener("token-expired", () => this.emit("token-expired", {}));
    this.client = client;
    this.mount = new Mount(this);
    this.mount.render(client, this.getAttribute("view") === "runs" ? "runs" : "builder", this.emit);
  }
}

for (const [name, cls] of [
  ["taskiem-builder", TaskiemBuilder],
  ["taskiem-runs", TaskiemRuns],
  ["taskiem-frame", TaskiemFrame],
] as const) {
  if (!customElements.get(name)) customElements.define(name, cls);
}
