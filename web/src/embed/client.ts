// The embedded builder's client for the embed API (/v1/embed/{app}/...;
// docs/embedding.md). It sends the end user's token as a header, never a
// cookie, streams runs with fetch (EventSource cannot send the header),
// and warns the host page before the token expires so the partner's
// server can mint the next one: tokens are minted server-side by the
// partner and cannot be refreshed here.
import { ApiError, type Problem, type RunEvent } from "../api";

export interface EmbedMe {
  end_user: { id: string; external_id: string };
  actor: string;
  app_id: string;
  sub_tenant_id: string;
  permissions: string[];
  allowed_connectors: string[];
  allowed_templates: string[];
  branding: unknown;
  white_label: boolean;
  expires_at: string;
}

export interface ClientOptions {
  /** The API's origin, e.g. https://app.taskiem.com (no trailing slash). */
  base: string;
  app: string;
  token: string;
  /** In the frame page: the partner page that framed it (sent as
   * X-Taskiem-Embed-Parent; the API checks it against the app's origins). */
  parentOrigin?: string;
  /** How long before expiry to warn; default 60 s. */
  warnBefore?: number;
  /** Milliseconds between stream reconnections, growing per attempt; default 2000. */
  retryDelay?: number;
  fetch?: typeof fetch;
  now?: () => number;
}

/** Details of the "token-expiring" event. */
export interface ExpiringDetail {
  expiresAt: string;
  /** Milliseconds left. */
  remaining: number;
}

/** One server-sent event. */
export interface SSEMessage {
  id?: string;
  event: string;
  data: string;
}

/**
 * Parses server-sent events from text as it arrives: give it each chunk;
 * it returns the complete events and keeps the rest for the next chunk.
 */
export class SSEParser {
  private buf = "";
  push(chunk: string): SSEMessage[] {
    this.buf += chunk.replace(/\r\n?/g, "\n");
    const out: SSEMessage[] = [];
    let i: number;
    while ((i = this.buf.indexOf("\n\n")) >= 0) {
      const block = this.buf.slice(0, i);
      this.buf = this.buf.slice(i + 2);
      const msg: SSEMessage = { event: "message", data: "" };
      const data: string[] = [];
      let any = false;
      for (const line of block.split("\n")) {
        if (line === "" || line.startsWith(":")) continue;
        const c = line.indexOf(":");
        const field = c < 0 ? line : line.slice(0, c);
        let value = c < 0 ? "" : line.slice(c + 1);
        if (value.startsWith(" ")) value = value.slice(1);
        if (field === "data") {
          data.push(value);
          any = true;
        } else if (field === "event") {
          msg.event = value;
          any = true;
        } else if (field === "id") msg.id = value;
      }
      if (!any) continue;
      msg.data = data.join("\n");
      out.push(msg);
    }
    return out;
  }
}

export class EmbedClient extends EventTarget {
  private token: string;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private readonly f: typeof fetch;
  private readonly now: () => number;

  constructor(private readonly opts: ClientOptions) {
    super();
    this.token = opts.token;
    this.f = opts.fetch ?? ((input, init) => fetch(input, init));
    this.now = opts.now ?? Date.now;
  }

  get app(): string {
    return this.opts.app;
  }

  /** Replaces the token (the partner minted a new one). Listeners of
   * "token-set" (the builder) read /me again, which re-arms the expiry
   * warning. */
  setToken(token: string): void {
    this.token = token;
    this.dispatchEvent(new Event("token-set"));
  }

  /** Stops the expiry timer. */
  close(): void {
    clearTimeout(this.timer);
  }

  private headers(json: boolean): Record<string, string> {
    const h: Record<string, string> = { Authorization: `Bearer ${this.token}` };
    if (json) h["Content-Type"] = "application/json";
    if (this.opts.parentOrigin) h["X-Taskiem-Embed-Parent"] = this.opts.parentOrigin;
    return h;
  }

  url(path: string): string {
    return `${this.opts.base}/v1/embed/${encodeURIComponent(this.opts.app)}${path}`;
  }

  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await this.f(this.url(path), {
      method,
      headers: this.headers(body !== undefined),
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "omit",
      mode: "cors",
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
      if (res.status === 401) this.dispatchEvent(new CustomEvent("token-expired"));
      throw new ApiError(res.status, (data.error as string) ?? res.statusText, (data.problems as Problem[]) ?? [], data);
    }
    return data as T;
  }

  get = <T>(path: string) => this.request<T>("GET", path);
  post = <T>(path: string, body?: unknown) => this.request<T>("POST", path, body ?? {});
  put = <T>(path: string, body?: unknown) => this.request<T>("PUT", path, body ?? {});

  /** Who the token is for, and arms the expiry warning. */
  async me(): Promise<EmbedMe> {
    const me = await this.get<EmbedMe>("/me");
    this.arm(me.expires_at);
    return me;
  }

  /** Schedules "token-expiring" warnBefore ms before expiresAt (at once if
   * that has passed). */
  arm(expiresAt: string): void {
    clearTimeout(this.timer);
    const at = Date.parse(expiresAt);
    if (Number.isNaN(at)) return;
    const warn = this.opts.warnBefore ?? 60_000;
    const delay = Math.max(0, at - warn - this.now());
    this.timer = setTimeout(() => {
      const detail: ExpiringDetail = { expiresAt, remaining: Math.max(0, at - this.now()) };
      this.dispatchEvent(new CustomEvent("token-expiring", { detail }));
    }, delay);
  }

  /**
   * Streams a run's events until it ends (or signal aborts), calling
   * onEvent for each. It reconnects after a dropped connection, resuming
   * after the last event seen (Last-Event-ID), up to `retries` times.
   */
  async streamRun(run: string, onEvent: (e: RunEvent) => void, signal?: AbortSignal, retries = 5): Promise<void> {
    let last = "";
    for (let attempt = 0; ; attempt++) {
      const headers = this.headers(false);
      if (last) headers["Last-Event-ID"] = last;
      try {
        const res = await this.f(this.url(`/runs/${encodeURIComponent(run)}/stream`), { headers, signal, credentials: "omit", mode: "cors" });
        if (!res.ok || !res.body) {
          if (res.status === 401) this.dispatchEvent(new CustomEvent("token-expired"));
          throw new ApiError(res.status, `stream: ${res.status}`);
        }
        const reader = res.body.getReader();
        const dec = new TextDecoder();
        const parser = new SSEParser();
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          for (const m of parser.push(dec.decode(value, { stream: true }))) {
            if (m.id) last = m.id;
            if (m.event === "end") {
              await reader.cancel().catch(() => undefined);
              return;
            }
            if (m.event === "run_event") onEvent(JSON.parse(m.data) as RunEvent);
          }
        }
      } catch (e) {
        if (signal?.aborted || (e instanceof ApiError && e.status >= 400 && e.status < 500) || attempt >= retries) throw e;
      }
      if (signal?.aborted) return;
      const step = this.opts.retryDelay ?? 2000;
      await new Promise((r) => setTimeout(r, Math.min(step * (attempt + 1), 5 * step)));
    }
  }
}
