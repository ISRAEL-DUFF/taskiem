import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, type RunEvent } from "../api";
import { EmbedClient, SSEParser, type ExpiringDetail } from "./client";
import { checkMessage, frameTargets } from "./frame";
import { parseBranding, themeVariables } from "./theme";

interface Call {
  url: string;
  init: RequestInit;
}

/** A fetch that records calls and answers from a queue of responders. */
function fakeFetch(...answers: ((c: Call) => Response)[]) {
  const calls: Call[] = [];
  const f = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const c = { url: String(input), init: init ?? {} };
    calls.push(c);
    const a = answers.shift();
    if (!a) throw new Error("unexpected fetch " + c.url);
    return a(c);
  });
  return { f: f as unknown as typeof fetch, calls };
}

const json = (body: unknown, status = 200) => () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const header = (c: Call | undefined, name: string) => new Headers(c?.init.headers).get(name);

/** A streamed body delivered in the given chunks. */
function stream(chunks: string[]): Response {
  const enc = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(ctl) {
      for (const c of chunks) ctl.enqueue(enc.encode(c));
      ctl.close();
    },
  });
  return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } });
}

const me = (expires: string) => ({
  end_user: { id: "e", external_id: "u1" },
  actor: "end_user:app/u1",
  app_id: "app",
  sub_tenant_id: "s",
  permissions: ["workflow.read"],
  allowed_connectors: [],
  allowed_templates: [],
  branding: {},
  white_label: false,
  expires_at: expires,
});

afterEach(() => {
  vi.useRealTimers();
});

describe("EmbedClient", () => {
  it("sends the token as a header, never cookies, to the app's embed API only", async () => {
    const { f, calls } = fakeFetch(json({ workflows: [] }), json({ id: "w" }, 201));
    const c = new EmbedClient({ base: "https://app.taskiem.test", app: "a1", token: "tsk_eut_one", fetch: f });
    await c.get("/workflows");
    c.setToken("tsk_eut_two");
    await c.post("/workflows", { name: "x" });
    expect(calls.map((x) => x.url)).toEqual(["https://app.taskiem.test/v1/embed/a1/workflows", "https://app.taskiem.test/v1/embed/a1/workflows"]);
    expect(header(calls[0], "Authorization")).toBe("Bearer tsk_eut_one");
    expect(header(calls[1], "Authorization")).toBe("Bearer tsk_eut_two");
    expect(header(calls[1], "Content-Type")).toBe("application/json");
    expect(calls.every((x) => x.init.credentials === "omit")).toBe(true);
    expect(header(calls[0], "X-Taskiem-Embed-Parent")).toBeNull();
  });

  it("names the framing page in frame mode", async () => {
    const { f, calls } = fakeFetch(json({}));
    const c = new EmbedClient({ base: "https://app.taskiem.test", app: "a1", token: "t", parentOrigin: "https://app.partner.test", fetch: f });
    await c.get("/connectors");
    expect(header(calls[0], "X-Taskiem-Embed-Parent")).toBe("https://app.partner.test");
  });

  it("warns before the token expires, and again after a new token", async () => {
    vi.useFakeTimers();
    const now = Date.parse("2026-10-07T10:00:00Z");
    vi.setSystemTime(now);
    const { f } = fakeFetch(json(me("2026-10-07T10:01:30Z")), json(me("2026-10-07T10:15:00Z")));
    const c = new EmbedClient({ base: "", app: "a", token: "t1", fetch: f, warnBefore: 60_000 });
    const seen: ExpiringDetail[] = [];
    c.addEventListener("token-expiring", (e) => seen.push((e as CustomEvent<ExpiringDetail>).detail));
    let reloads = 0;
    c.addEventListener("token-set", () => reloads++);
    await c.me();
    await vi.advanceTimersByTimeAsync(29_000);
    expect(seen).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(seen).toEqual([{ expiresAt: "2026-10-07T10:01:30Z", remaining: 60_000 }]);
    // The partner's page hands over a new token; the builder reads /me again.
    c.setToken("t2");
    expect(reloads).toBe(1);
    await c.me();
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    expect(seen).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(4 * 60_000);
    expect(seen).toHaveLength(2);
    c.close();
  });

  it("reports an expired or revoked token", async () => {
    const { f } = fakeFetch(json({ error: "a valid end-user token for this app is required" }, 401));
    const c = new EmbedClient({ base: "", app: "a", token: "t", fetch: f });
    const expired = vi.fn();
    c.addEventListener("token-expired", expired);
    await expect(c.get("/me")).rejects.toBeInstanceOf(ApiError);
    expect(expired).toHaveBeenCalledOnce();
  });

  it("streams a run with fetch, resuming after the last event when the connection drops", async () => {
    const ev = (seq: number, type: string) => `id: ${seq}\nevent: run_event\ndata: ${JSON.stringify({ seq, type, recorded_at: "t", origin: "engine" })}\n\n`;
    const { f, calls } = fakeFetch(
      // Split mid-event and mid-line; a comment and the retry field are skipped.
      () => stream(["retry: 2000\n\n" + ev(1, "RunStarted").slice(0, 20), ev(1, "RunStarted").slice(20) + ": keep-alive\n\n" + ev(2, "StepScheduled")]),
      () => stream([ev(3, "StepCompleted"), ev(4, "RunCompleted"), "event: end\ndata: {}\n\n", ev(5, "never read")]),
    );
    const c = new EmbedClient({ base: "https://x.test", app: "a", token: "tok", fetch: f, retryDelay: 1 });
    const got: RunEvent[] = [];
    await c.streamRun("r1", (e) => got.push(e));
    expect(got.map((e) => e.type)).toEqual(["RunStarted", "StepScheduled", "StepCompleted", "RunCompleted"]);
    expect(calls).toHaveLength(2);
    expect(calls[0]?.url).toBe("https://x.test/v1/embed/a/runs/r1/stream");
    expect(header(calls[0], "Authorization")).toBe("Bearer tok");
    expect(header(calls[0], "Last-Event-ID")).toBeNull();
    expect(header(calls[1], "Last-Event-ID")).toBe("2");
  });

  it("does not retry a refused stream", async () => {
    const { f, calls } = fakeFetch(json({ error: "forbidden" }, 403));
    const c = new EmbedClient({ base: "", app: "a", token: "t", fetch: f, retryDelay: 1 });
    await expect(c.streamRun("r", () => undefined)).rejects.toBeInstanceOf(ApiError);
    expect(calls).toHaveLength(1);
  });
});

describe("SSEParser", () => {
  it("parses fields, multi-line data and CRLF", () => {
    const p = new SSEParser();
    expect(p.push("event: a\r\ndata: one\r\ndata: two\r\n")).toEqual([]);
    expect(p.push("\r\n:comment\n\nid: 7\ndata:x\n\n")).toEqual([
      { event: "a", data: "one\ntwo" },
      { event: "message", data: "x", id: "7" },
    ]);
  });
});

describe("theme", () => {
  it("maps branding tokens to custom properties", () => {
    expect(themeVariables({ colours: { primary: "#0a7d5a", on_primary: "#fff", background: "#ffffffee" }, font_family: "Inter, sans-serif", radius: "12px" })).toEqual({
      "--accent": "#0a7d5a",
      "--accent-text": "#fff",
      "--bg": "#ffffffee",
      "--tk-font": "Inter, sans-serif",
      "--tk-radius": "12px",
    });
    const dark = themeVariables({ mode: "dark", colours: { primary: "#123456" } });
    expect(dark["--bg"]).toBe("#111315");
    expect(dark["--accent"]).toBe("#123456");
    expect(dark["color-scheme"]).toBe("dark");
  });

  it("drops anything that could carry CSS or markup", () => {
    expect(
      themeVariables({
        colours: { primary: "red;}body{display:none", evil: "#fff", text: "url(x)" },
        font_family: 'Inter"; } * { display: none',
        radius: "calc(1px)",
        mode: "neon",
        css: "body{}",
      }),
    ).toEqual({});
    expect(parseBranding({ logo_url: "javascript:alert(1)", font_url: "http://cdn.test/f.css" })).toEqual({});
    expect(parseBranding({ logo_url: "https://cdn.test/l.svg" })).toEqual({ logo_url: "https://cdn.test/l.svg" });
  });
});

describe("frame messages", () => {
  const parent = {};
  const origins = ["https://app.partner.test", "https://admin.partner.test"];
  const token = "tsk_eut_" + "a".repeat(43);
  const msg = (over: Partial<{ source: unknown; origin: string; data: unknown }>) => ({
    source: parent,
    origin: "https://app.partner.test",
    data: { type: "taskiem:token", token },
    ...over,
  });

  it("takes a token only from the parent, from an allowed origin", () => {
    expect(checkMessage(msg({}), parent, origins, "")).toEqual({ token });
    expect(checkMessage(msg({ source: {} }), parent, origins, "")).toBeNull();
    expect(checkMessage(msg({ origin: "https://evil.test" }), parent, origins, "")).toBeNull();
    expect(checkMessage(msg({ origin: "https://admin.partner.test" }), parent, origins, "https://app.partner.test")).toBeNull();
    expect(checkMessage(msg({ data: { type: "taskiem:token", token: "x" } }), parent, origins, "")).toBeNull();
    expect(checkMessage(msg({ data: { type: "other", token } }), parent, origins, "")).toBeNull();
    expect(checkMessage(msg({ data: "taskiem:token" }), parent, origins, "")).toBeNull();
  });

  it("announces itself only to allowed origins", () => {
    expect(frameTargets(origins, "https://admin.partner.test")).toEqual(["https://admin.partner.test"]);
    expect(frameTargets(origins, "https://evil.test")).toEqual([]);
    expect(frameTargets(origins, undefined)).toEqual(origins);
  });
});
