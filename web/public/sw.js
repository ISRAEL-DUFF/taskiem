// Taskiem's service worker. It does two things: it shows a chat message when
// the app is closed (docs/spec/chat-push.md §6), and when a page of the app
// cannot be reached because the device is offline it shows a plain page that
// says so, instead of the browser's error (docs/spec/offline.md). It keeps no
// cache: every page and every answer comes from the server, and the offline
// page is written here. The app registers it for everyone (lib/worker.ts).
//
// The logic is in plain functions at the top so a test can run them without a
// browser; the event handlers at the bottom only wire them up.
"use strict";

var ICON = "/brand/relay/icon-192.png";

/** A path inside the app a notification may open (a page, and at most one plain question such as ?with=); anything else is the app's start. */
var LINK = /^\/(?!\/)[A-Za-z0-9_/-]{0,120}(\?[a-z_]{1,20}=[A-Za-z0-9-]{1,80})?$/;
function safeLink(link) {
  if (typeof link === "string" && LINK.test(link)) return link;
  return "/";
}

/** The business a push is for (a person can be in more than one), or "". */
var UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
function safeBusiness(b) {
  return typeof b === "string" && UUID.test(b) ? b : "";
}

/** The address a tap opens: the link, in its business (the page switches to it first). */
function openLink(link, business) {
  var l = safeLink(link);
  var b = safeBusiness(business);
  if (!b) return l;
  return l + (l.indexOf("?") < 0 ? "?" : "&") + "business=" + b;
}

var KINDS = ["business", "customers", "orders", "approvals", "alerts"];

/** The sender's logo, a path inside the app the worker may fetch with the person's own session (nothing else is fetched). */
var ICON_PATH = /^\/v1\/links\/[0-9a-f-]{36}\/logo(\?size=(320|800|2000))?$/;
function safeIcon(icon) {
  return typeof icon === "string" && ICON_PATH.test(icon) ? icon : "";
}

/** What a push says, read from its data (a function that gives the parsed JSON, or throws). Anything unreadable is a plain "New message". */
function parsePayload(read) {
  var p;
  try {
    p = read();
  } catch {
    p = null;
  }
  if (!p || typeof p !== "object") p = {};
  return {
    title: typeof p.title === "string" && p.title ? p.title.slice(0, 200) : "Taskiem",
    body: typeof p.body === "string" && p.body ? p.body.slice(0, 400) : "New message",
    link: safeLink(p.link),
    tag: typeof p.tag === "string" && p.tag ? p.tag.slice(0, 200) : "taskiem-chat",
    // How many conversations have something unread, for the icon's badge; -1 when the push does not say.
    unread: typeof p.unread === "number" && p.unread >= 0 ? Math.floor(p.unread) : -1,
    icon: safeIcon(p.icon),
    business: safeBusiness(p.business),
    kind: KINDS.indexOf(p.kind) >= 0 ? p.kind : "business",
  };
}

/** The notification for a payload: one per conversation (the tag replaces the last), not stacked, and the same tag the page's own notification uses. `icon` is the sender's logo as the worker fetched it (a data address), else the app's own. */
function notificationOf(p, icon) {
  return {
    title: p.title,
    options: {
      body: p.body,
      tag: p.tag,
      renotify: false,
      icon: icon || ICON,
      badge: ICON,
      data: { link: p.link, business: p.business },
    },
  };
}

/** The window a tap goes to: the one in front, else one that is seen, else any. */
function pickWindow(windows) {
  return (
    windows.find(function (w) { return w.focused; }) ||
    windows.find(function (w) { return w.visibilityState === "visible"; }) ||
    windows[0]
  );
}

/** Where a tap is written down for the page (lib/pushOpen.ts reads it once). */
var TAP_CACHE = "taskiem-push-tap";
var TAP_KEY = "/__taskiem/push-tap";
async function markTap(business) {
  // The worker's own Cache API (the page reads the same, same origin).
  var store = typeof self !== "undefined" ? self.caches : undefined;
  if (!safeBusiness(business) || !store) return;
  try {
    var c = await store.open(TAP_CACHE);
    await c.put(TAP_KEY, new Response(JSON.stringify({ business: business, at: Date.now() }), { headers: { "Content-Type": "application/json" } }));
  } catch {
    // Not written: the page asks before it switches.
  }
}

/** What the worker tells the server when the browser renews its subscription. */
function renewal(json, oldEndpoint) {
  var body = { endpoint: json.endpoint, keys: json.keys };
  if (oldEndpoint && oldEndpoint !== json.endpoint) body.replaces = oldEndpoint;
  return body;
}

function toBase64(bytes) {
  var out = "";
  for (var i = 0; i < bytes.length; i += 8192) out += String.fromCharCode.apply(null, bytes.subarray(i, i + 8192));
  return btoa(out);
}

/**
 * The sender's logo as a data address a notification can show: fetched with the
 * person's own session (a browser's own fetch of a notification's icon does
 * not always send it), at most 3 seconds and 200 KB, a picture and nothing else.
 * Any failure is the app's own icon: a notification is never held for a logo.
 */
async function iconFor(path, doFetch) {
  if (!path) return ICON;
  var timer;
  try {
    var control = typeof AbortController !== "undefined" ? new AbortController() : null;
    if (control) timer = setTimeout(function () { control.abort(); }, 3000);
    var res = await doFetch(path, { credentials: "same-origin", signal: control ? control.signal : undefined });
    var type = ((res.headers && res.headers.get("Content-Type")) || "").split(";")[0];
    if (!res.ok || !/^image\/(png|jpeg|webp)$/.test(type)) return ICON;
    var buf = new Uint8Array(await res.arrayBuffer());
    if (buf.length === 0 || buf.length > 200000) return ICON;
    return "data:" + type + ";base64," + toBase64(buf);
  } catch {
    return ICON;
  } finally {
    if (timer) clearTimeout(timer);
  }
}

/**
 * Whether to show nothing: a window of the app is visible and focused, so the
 * page has the message, and the browser lets a push be silent when one is
 * (Chromium does; Safari and iOS end a subscription that shows nothing, and
 * Firefox allows it only within a quota, so those always show).
 */
function staysSilent(windows, userAgent, brands) {
  // Chromium says so in its brands (Chrome, Edge, Opera); the user agent is the fallback.
  var byBrand =
    Array.isArray(brands) &&
    brands.some(function (b) {
      return b && /Chrom/.test(b.brand || "");
    });
  var byAgent = /\bChrom(e|ium)\//.test(userAgent || "") && !/\bFxiOS\b|\bCriOS\b/.test(userAgent || "");
  if (!byBrand && !byAgent) return false;
  return windows.some(function (w) {
    return w.visibilityState === "visible" && w.focused === true;
  });
}

// --- offline: a page that says so, never a copy of what was shown ---

// The offline page's one script, and its hash for the page's policy (a test
// checks the two agree). A timed refresh would be simpler, but it fails
// WCAG 2.2.1: the page reloads when the device says it is online instead.
var OFFLINE_SCRIPT = 'addEventListener("online",function(){location.reload()})';
var OFFLINE_SCRIPT_HASH = "sha256-wWRhLfctTqxc2ufC6e5xO9BfruHxK8KLCMIaxgvv9TA=";

/** Whether the worker answers this request: a page of the app being opened (a navigation, a GET to this origin). Everything else goes to the network untouched. */
function handlesRequest(method, mode, url, origin) {
  if (method !== "GET" || mode !== "navigate") return false;
  try {
    return new URL(url).origin === origin;
  } catch {
    return false;
  }
}

/** The page shown when a page of the app cannot be reached: self-contained, the brand's mark drawn inline, light or dark as the device is. It opens the page again when the device is back online (its one line of script), or on a press. */
function offlinePage() {
  return (
    '<!doctype html><html lang="en"><head><meta charset="utf-8">' +
    '<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">' +
    '<meta name="theme-color" content="#0d3b2e"><title>Taskiem: offline</title><style>' +
    ":root{--bg:#f1f4ef;--fg:#0d3b2e;--muted:#4a5d55;--btn:#0d3b2e;--btn-fg:#fff;color-scheme:light dark}" +
    "@media (prefers-color-scheme:dark){:root{--bg:#0b1411;--fg:#e8efe9;--muted:#a9b8b0;--btn:#c8f169;--btn-fg:#0d3b2e}}" +
    "*{box-sizing:border-box}html,body{margin:0;height:100%}" +
    "body{background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,'Segoe UI',Roboto,sans-serif;display:flex;align-items:center;justify-content:center;padding:24px 16px}" +
    "main{max-width:22rem;text-align:center}svg{width:64px;height:64px}" +
    "h1{font-size:1.375rem;margin:20px 0 8px}p{margin:0 0 24px;color:var(--muted)}" +
    "a{display:inline-block;min-height:44px;padding:10px 24px;border-radius:999px;background:var(--btn);color:var(--btn-fg);text-decoration:none;font-weight:600}" +
    "a:focus-visible{outline:3px solid var(--fg);outline-offset:3px}" +
    "</style></head><body><main>" +
    '<svg viewBox="0 0 100 100" aria-hidden="true"><rect width="100" height="100" rx="24" fill="#0d3b2e"/><path d="M24 40H64M42 18V58Q42 76 60 76H62" stroke="#c8f169" stroke-width="16" stroke-linecap="round" fill="none"/><circle cx="81" cy="76" r="9" fill="#fff"/></svg>' +
    "<h1>You\u2019re offline</h1>" +
    "<p>Taskiem needs the internet to show your business. It will open by itself when you\u2019re back online.</p>" +
    '<a href="">Try again</a>' +
    "</main><script>" + OFFLINE_SCRIPT + "</script></body></html>"
  );
}

/** The offline page as an answer: never kept by the browser, under a content policy that allows its styles and its one script, by hash, and nothing else. */
function offlineResponse() {
  return new Response(offlinePage(), {
    status: 503,
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; script-src '" + OFFLINE_SCRIPT_HASH + "'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
    },
  });
}

/**
 * A page of the app: the network's answer, whatever it is (an error page, a
 * redirect), started early when the browser can (navigation preload); the
 * offline page only when the network cannot be reached at all.
 */
async function pageFor(preload, doFetch) {
  try {
    var early = preload ? await preload : undefined;
    if (early) return early;
    return await doFetch();
  } catch {
    return offlineResponse();
  }
}

if (typeof module !== "undefined") {
  module.exports = {
    safeLink: safeLink, safeBusiness: safeBusiness, openLink: openLink, renewal: renewal, pickWindow: pickWindow, safeIcon: safeIcon, parsePayload: parsePayload, notificationOf: notificationOf, iconFor: iconFor, staysSilent: staysSilent,
    handlesRequest: handlesRequest, offlinePage: offlinePage, OFFLINE_SCRIPT: OFFLINE_SCRIPT, OFFLINE_SCRIPT_HASH: OFFLINE_SCRIPT_HASH, offlineResponse: offlineResponse, pageFor: pageFor,
  };
}

// --- the events: only in a service worker ---

if (typeof self !== "undefined" && typeof self.addEventListener === "function" && typeof ServiceWorkerGlobalScope !== "undefined") {
  self.addEventListener("install", function () {
    self.skipWaiting();
  });
  self.addEventListener("activate", function (event) {
    event.waitUntil(
      (async function () {
        // Pages start loading while the worker wakes, where the browser can.
        if (self.registration.navigationPreload) await self.registration.navigationPreload.enable().catch(function () {});
        await self.clients.claim();
      })(),
    );
  });

  self.addEventListener("fetch", function (event) {
    var req = event.request;
    if (!handlesRequest(req.method, req.mode, req.url, self.location.origin)) return;
    event.respondWith(
      pageFor(event.preloadResponse, function () {
        return fetch(req);
      }),
    );
  });

  self.addEventListener("push", function (event) {
    event.waitUntil(
      (async function () {
        var p = parsePayload(function () {
          return event.data ? event.data.json() : null;
        });
        var windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
        var data = self.navigator.userAgentData;
        if (staysSilent(windows, self.navigator.userAgent, data && data.brands)) return;
        var icon = await iconFor(p.icon, function (url, init) {
          return self.fetch(url, init);
        });
        var n = notificationOf(p, icon);
        await self.registration.showNotification(n.title, n.options);
        // The count on the app's icon, where the device has one (the page
        // corrects it when it opens).
        var nav = self.navigator;
        if (p.unread >= 0 && nav && typeof nav.setAppBadge === "function") {
          await (p.unread > 0 ? nav.setAppBadge(p.unread) : nav.clearAppBadge()).catch(function () {});
        }
      })(),
    );
  });

  self.addEventListener("notificationclick", function (event) {
    event.notification.close();
    var data = event.notification.data || {};
    var link = openLink(data.link, data.business);
    event.waitUntil(
      (async function () {
        // A tap is what may move the page to another business: said where
        // the page can read it, so a link from elsewhere cannot.
        // At most a moment: a browser lets a click open a window only soon after it.
        await Promise.race([markTap(data.business), new Promise(function (r) { setTimeout(r, 300); })]);
        var windows = (await self.clients.matchAll({ type: "window", includeUncontrolled: true })).filter(function (w) {
          return new URL(w.url).origin === self.location.origin;
        });
        var mine = pickWindow(windows);
        if (mine) {
          await mine.focus();
          mine.postMessage({ type: "taskiem-go", link: link });
          return;
        }
        await self.clients.openWindow(link);
      })(),
    );
  });

  // A browser may replace its subscription (its keys expired): subscribe
  // again with the same key and tell the server, which moves the old one's
  // name and choices to it and forgets the old one.
  self.addEventListener("pushsubscriptionchange", function (event) {
    event.waitUntil(
      (async function () {
        var old = event.oldSubscription;
        var key = (old && old.options && old.options.applicationServerKey) || (event.newSubscription && event.newSubscription.options && event.newSubscription.options.applicationServerKey);
        var sub = event.newSubscription || (key ? await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }) : null);
        if (!sub) return;
        await fetch("/v1/me/push", {
          method: "PUT",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json", "X-Taskiem-Request": "1" },
          body: JSON.stringify(renewal(sub.toJSON(), old && old.endpoint)),
        });
      })().catch(function () {
        // Signed out or offline: the person turns it on again.
      }),
    );
  });
}
