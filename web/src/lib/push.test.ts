import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import {
  deviceLabel,
  isApple,
  keyBytes,
  pushSupport,
  type PushEnv,
} from "./push";

// The worker is a plain script: run it here with a stand-in `module` to get its functions.
const mod: { exports: unknown } = { exports: {} };
new Function(
  "module",
  readFileSync(new URL("../../public/sw.js", import.meta.url), "utf8"),
)(mod);
const sw = mod.exports as {
  safeLink: (l: unknown) => string;
  safeIcon: (i: unknown) => string;
  iconFor: (
    path: string,
    doFetch: (url: string, init: unknown) => Promise<unknown>,
  ) => Promise<string>;
  parsePayload: (read: () => unknown) => {
    title: string;
    body: string;
    link: string;
    tag: string;
    unread: number;
    icon: string;
  };
  notificationOf: (
    p: ReturnType<typeof sw.parsePayload>,
    icon?: string,
  ) => {
    title: string;
    options: Record<string, unknown>;
  };
  staysSilent: (
    w: { visibilityState: string; focused: boolean }[],
    ua: string,
    brands?: { brand: string }[],
  ) => boolean;
  handlesRequest: (
    method: string,
    mode: string,
    url: string,
    origin: string,
  ) => boolean;
  offlinePage: () => string;
  OFFLINE_SCRIPT: string;
  OFFLINE_SCRIPT_HASH: string;
  offlineResponse: () => Response;
  pageFor: (
    preload: Promise<Response | undefined> | undefined,
    doFetch: () => Promise<Response>,
  ) => Promise<Response>;
};

const CHROME_ANDROID =
  "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36";
const CHROME_WIN =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36";
const SAFARI_IPHONE =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1";
const SAFARI_MAC =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15";
const FIREFOX =
  "Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0";
const EDGE =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0";
const CRIOS =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/126.0.6478.153 Mobile/15E148 Safari/604.1";

const env = (over: Partial<PushEnv>): PushEnv => ({
  userAgent: CHROME_WIN,
  standalone: false,
  hasWorker: true,
  hasPush: true,
  hasNotification: true,
  ...over,
});

describe("device labels", () => {
  it("name the browser and the system in plain words", () => {
    expect(deviceLabel(CHROME_ANDROID)).toBe("Chrome on Android");
    expect(deviceLabel(CHROME_WIN)).toBe("Chrome on Windows");
    expect(deviceLabel(EDGE)).toBe("Edge on Windows");
    expect(deviceLabel(SAFARI_IPHONE)).toBe("Safari on iPhone");
    expect(deviceLabel(SAFARI_MAC)).toBe("Safari on Mac");
    expect(deviceLabel(FIREFOX)).toBe("Firefox on Linux");
    expect(deviceLabel(CRIOS)).toBe("Chrome on iPhone");
    expect(deviceLabel("")).toBe("A browser");
  });
});

describe("what a browser can do", () => {
  it("takes a push where it can", () => {
    expect(pushSupport(env({}))).toBe("yes");
    expect(pushSupport(env({ hasPush: false }))).toBe("no");
    expect(pushSupport(env({ hasWorker: false }))).toBe("no");
    expect(pushSupport(env({ hasNotification: false }))).toBe("no");
  });
  it("on an iPhone or iPad, only once it is on the Home Screen", () => {
    expect(pushSupport(env({ userAgent: SAFARI_IPHONE }))).toBe(
      "install-first",
    );
    expect(
      pushSupport(env({ userAgent: SAFARI_IPHONE, standalone: true })),
    ).toBe("yes");
    // An iPad that says it is a Mac.
    expect(isApple(SAFARI_MAC, "MacIntel", 5)).toBe(true);
    expect(
      pushSupport(
        env({ userAgent: SAFARI_MAC, platform: "MacIntel", maxTouchPoints: 5 }),
      ),
    ).toBe("install-first");
    expect(isApple(SAFARI_MAC, "MacIntel", 0)).toBe(false);
    expect(
      pushSupport(
        env({ userAgent: SAFARI_MAC, platform: "MacIntel", maxTouchPoints: 0 }),
      ),
    ).toBe("yes");
  });
});

describe("the server's key", () => {
  it("becomes the bytes a browser subscribes with", () => {
    const b = keyBytes("AQIDBA"); // 01 02 03 04
    expect(Array.from(b)).toEqual([1, 2, 3, 4]);
    expect(Array.from(keyBytes("-_8"))).toEqual([251, 255]);
  });
});

describe("the service worker's words", () => {
  it("opens only a place in the app", () => {
    expect(sw.safeLink("/chat?with=0192-abc")).toBe("/chat?with=0192-abc");
    expect(sw.safeLink("/chat")).toBe("/chat");
    for (const bad of [
      "https://evil.example/",
      "//evil.example",
      "/settings/keys",
      "/chat?with=../x",
      "javascript:alert(1)",
      "",
      null,
      5,
    ]) {
      expect(sw.safeLink(bad)).toBe("/chat");
    }
  });
  it("reads a push, or says only that there is a message", () => {
    const p = sw.parsePayload(() => ({
      v: 1,
      title: "Dangote Stores",
      body: "Musa: Rice is 45k",
      link: "/chat?with=abc",
      tag: "abc",
      unread: 3,
    }));
    expect(p).toEqual({
      title: "Dangote Stores",
      body: "Musa: Rice is 45k",
      link: "/chat?with=abc",
      tag: "abc",
      unread: 3,
      icon: "",
    });
    for (const read of [
      () => {
        throw new Error("not json");
      },
      () => null,
      () => "text",
      () => ({}),
    ]) {
      const q = sw.parsePayload(read);
      expect(q.body).toBe("New message");
      expect(q.title).toBe("Taskiem");
      expect(q.link).toBe("/chat");
    }
    // Long words are cut, a link outside the app is the chat page, a bad count is none.
    const r = sw.parsePayload(() => ({
      title: "x".repeat(500),
      body: "y".repeat(900),
      link: "https://evil.example",
      tag: "t".repeat(200),
      unread: -4,
    }));
    expect(r.title).toHaveLength(200);
    expect(r.body).toHaveLength(400);
    expect(r.link).toBe("/chat");
    expect(r.tag).toHaveLength(64);
    expect(r.unread).toBe(-1); // not said: the badge is left as it is
  });
  it("shows one notification per conversation, with the page's own tag", () => {
    const n = sw.notificationOf(
      sw.parsePayload(() => ({
        title: "Dangote Stores",
        body: "Hello",
        link: "/chat?with=abc",
        tag: "abc",
      })),
    );
    expect(n.title).toBe("Dangote Stores");
    expect(n.options).toMatchObject({
      body: "Hello",
      tag: "abc",
      renotify: false,
      data: { link: "/chat?with=abc" },
    });
  });
  it("stays silent only in Chrome with a window in front; Apple and Firefox always show", () => {
    const front = [{ visibilityState: "visible", focused: true }];
    const behind = [{ visibilityState: "visible", focused: false }];
    const hidden = [{ visibilityState: "hidden", focused: false }];
    expect(sw.staysSilent(front, CHROME_WIN)).toBe(true);
    expect(sw.staysSilent(front, CHROME_ANDROID)).toBe(true);
    expect(sw.staysSilent(front, EDGE)).toBe(true);
    expect(sw.staysSilent(behind, CHROME_WIN)).toBe(false);
    expect(sw.staysSilent(hidden, CHROME_WIN)).toBe(false);
    expect(sw.staysSilent([], CHROME_WIN)).toBe(false);
    expect(sw.staysSilent(front, SAFARI_IPHONE)).toBe(false);
    expect(sw.staysSilent(front, SAFARI_MAC)).toBe(false);
    expect(sw.staysSilent(front, FIREFOX)).toBe(false);
    expect(sw.staysSilent(front, CRIOS)).toBe(false); // Chrome on iOS is WebKit
    // Chromium says so in its brands, whatever its user agent calls it (a headless one, say).
    const brands = [{ brand: "HeadlessChrome" }, { brand: "Chromium" }];
    expect(
      sw.staysSilent(
        front,
        "Mozilla/5.0 (X11; Linux) HeadlessChrome/126.0",
        brands,
      ),
    ).toBe(true);
    expect(sw.staysSilent(behind, SAFARI_MAC, brands)).toBe(false);
    expect(sw.staysSilent(front, SAFARI_MAC, [])).toBe(false);
    expect(sw.staysSilent(front, SAFARI_MAC, undefined)).toBe(false);
  });
});

describe("the sender's logo as the icon", () => {
  const UUID = "0192e2e0-0000-7000-8000-000000000001";
  it("fetches only the logo of a linked business, by its path", () => {
    expect(sw.safeIcon(`/v1/links/${UUID}/logo?size=320`)).toBe(
      `/v1/links/${UUID}/logo?size=320`,
    );
    expect(sw.safeIcon(`/v1/links/${UUID}/logo`)).toBe(
      `/v1/links/${UUID}/logo`,
    );
    for (const bad of [
      "https://evil.example/logo.png",
      "//evil.example/x",
      `/v1/links/${UUID}/logo?size=1`,
      `/v1/links/${UUID}/logo?size=320&x=1`,
      "/v1/links/../me/logo",
      "/v1/me",
      `/v1/explore/${UUID}/logo`,
      "data:image/png;base64,AAAA",
      "",
      null,
      5,
    ]) {
      expect(sw.safeIcon(bad)).toBe("");
    }
    // A push that names another address has no icon of its own.
    expect(
      sw.parsePayload(() => ({ icon: "https://evil.example/x.png" })).icon,
    ).toBe("");
    expect(
      sw.parsePayload(() => ({ icon: `/v1/links/${UUID}/logo?size=320` })).icon,
    ).toBe(`/v1/links/${UUID}/logo?size=320`);
  });

  const png =
    (bytes: number, type = "image/png", ok = true) =>
    async () => ({
      ok,
      headers: {
        get: (h: string) => (h.toLowerCase() === "content-type" ? type : null),
      },
      arrayBuffer: async () => new Uint8Array(bytes).fill(7).buffer,
    });

  it("is the logo as a data address, fetched with the person's own session", async () => {
    let seen: { url: string; init: { credentials?: string } } | undefined;
    const got = await sw.iconFor(
      "/v1/links/x/logo?size=320",
      async (url, init) => {
        seen = { url, init: init as { credentials?: string } };
        return png(3)();
      },
    );
    expect(got).toBe(
      "data:image/png;base64," + btoa(String.fromCharCode(7, 7, 7)),
    );
    expect(seen?.url).toBe("/v1/links/x/logo?size=320");
    expect(seen?.init.credentials).toBe("same-origin");
  });

  it("is the app's own icon without a logo, or when the logo cannot be had", async () => {
    const own = "/brand/relay/icon-192.png";
    expect(await sw.iconFor("", png(3))).toBe(own);
    expect(
      await sw.iconFor("/v1/links/x/logo", png(3, "image/png", false)),
    ).toBe(own); // 404, signed out
    expect(await sw.iconFor("/v1/links/x/logo", png(3, "text/html"))).toBe(own); // not a picture
    expect(await sw.iconFor("/v1/links/x/logo", png(0))).toBe(own); // nothing
    expect(await sw.iconFor("/v1/links/x/logo", png(200001))).toBe(own); // too big
    expect(
      await sw.iconFor("/v1/links/x/logo", async () => {
        throw new Error("offline");
      }),
    ).toBe(own);
    expect(await sw.iconFor("/v1/links/x/logo", png(150000))).toMatch(
      /^data:image\/png;base64,/,
    ); // a big logo that is not too big
  });

  it("goes into the notification, else the app's own icon", () => {
    const p = sw.parsePayload(() => ({
      title: "A",
      body: "B",
      link: "/chat",
      tag: "t",
    }));
    expect(
      sw.notificationOf(p, "data:image/png;base64,AAAA").options.icon,
    ).toBe("data:image/png;base64,AAAA");
    expect(sw.notificationOf(p).options.icon).toBe("/brand/relay/icon-192.png");
  });
});

describe("the worker offline (docs/spec/offline.md)", () => {
  const origin = "https://app.taskiem.test";

  it("answers only for a page of the app being opened", () => {
    expect(sw.handlesRequest("GET", "navigate", `${origin}/chat`, origin)).toBe(true);
    expect(sw.handlesRequest("GET", "navigate", `${origin}/`, origin)).toBe(true);
    // The API, files, pictures and the page's own requests go to the network untouched.
    expect(sw.handlesRequest("GET", "cors", `${origin}/v1/me`, origin)).toBe(false);
    expect(sw.handlesRequest("GET", "no-cors", `${origin}/assets/a.js`, origin)).toBe(false);
    expect(sw.handlesRequest("GET", "same-origin", `${origin}/chat`, origin)).toBe(false);
    // A form sent (a sign-in provider posting back) and another site's page.
    expect(sw.handlesRequest("POST", "navigate", `${origin}/v1/auth/sso/saml/acs`, origin)).toBe(false);
    expect(sw.handlesRequest("GET", "navigate", "https://paystack.com/pay", origin)).toBe(false);
    expect(sw.handlesRequest("GET", "navigate", "not a url", origin)).toBe(false);
  });

  it("gives the network's answer, whatever it is, and the offline page only when there is no network", async () => {
    const ok = new Response("app", { status: 200 });
    expect(await sw.pageFor(undefined, () => Promise.resolve(ok))).toBe(ok);
    const down = new Response("down", { status: 502 });
    expect(await sw.pageFor(undefined, () => Promise.resolve(down))).toBe(down);
    const early = new Response("early");
    const never = vi.fn(() => Promise.resolve(ok));
    expect(await sw.pageFor(Promise.resolve(early), never)).toBe(early);
    expect(never).not.toHaveBeenCalled();
    // No preload (the browser has none, or gave none): fetched.
    expect(await sw.pageFor(Promise.resolve(undefined), () => Promise.resolve(ok))).toBe(ok);
    const off = await sw.pageFor(undefined, () => Promise.reject(new TypeError("Failed to fetch")));
    expect(off.status).toBe(503);
    expect(await off.text()).toContain("You\u2019re offline");
    const offEarly = await sw.pageFor(Promise.reject(new TypeError("Failed to fetch")), () =>
      Promise.reject(new TypeError("Failed to fetch")),
    );
    expect(offEarly.status).toBe(503);
  });

  it("is a page of its own: never kept, one script allowed by its hash, nothing loaded, in plain words", async () => {
    const r = sw.offlineResponse();
    expect(r.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    expect(r.headers.get("Cache-Control")).toBe("no-store");
    expect(r.headers.get("Content-Security-Policy")).toBe(
      `default-src 'none'; style-src 'unsafe-inline'; script-src '${sw.OFFLINE_SCRIPT_HASH}'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`,
    );
    const html = sw.offlinePage();
    // The one script is the one its hash allows; nothing is loaded from anywhere.
    expect(html.match(/<script>([\s\S]*?)<\/script>/g)).toEqual([`<script>${sw.OFFLINE_SCRIPT}</script>`]);
    expect(sw.OFFLINE_SCRIPT_HASH).toBe(`sha256-${createHash("sha256").update(sw.OFFLINE_SCRIPT).digest("base64")}`);
    expect(html).not.toMatch(/src=|url\(|http-equiv="refresh"/i);
    expect(html).toContain('<a href="">Try again</a>');
    expect(html).toContain('<html lang="en">');
    const words = html.replace(/<style>[\s\S]*?<\/style>/, "").replace(/<script>[\s\S]*?<\/script>/, "").replace(/<[^>]+>/g, " ");
    for (const w of [/\bserver\b/i, /\bservice worker\b/i, /\bcache\b/i, /\bnetwork\b/i, /\b(status|HTTP) [1-5]\d\d\b/])
      expect(words).not.toMatch(w);
  });

  it("draws the mark exactly as the brand's own file does", () => {
    const mark = readFileSync(new URL("../../public/brand/relay/favicon.svg", import.meta.url), "utf8");
    const inner = (svg: string) => svg.replace(/^[\s\S]*?<svg[^>]*>/, "").replace(/<\/svg>[\s\S]*$/, "").replace(/#ffffff/g, "#fff").trim();
    const page = sw.offlinePage().match(/<svg[\s\S]*?<\/svg>/)?.[0] ?? "";
    expect(inner(page)).toBe(inner(mark));
  });
});
