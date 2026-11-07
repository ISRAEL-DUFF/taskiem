import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
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
  parsePayload: (read: () => unknown) => {
    title: string;
    body: string;
    link: string;
    tag: string;
    unread: number;
  };
  notificationOf: (p: ReturnType<typeof sw.parsePayload>) => {
    title: string;
    options: Record<string, unknown>;
  };
  staysSilent: (
    w: { visibilityState: string; focused: boolean }[],
    ua: string,
    brands?: { brand: string }[],
  ) => boolean;
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
    expect(r.unread).toBe(0);
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
