// Taskiem's service worker. It exists for one thing: to show a chat message
// when the app is closed (docs/spec/chat-push.md §6). It has no fetch handler
// and keeps no cache: no offline mode is promised or built here. It is
// registered only when a person turns notifications on.
//
// The logic is in plain functions at the top so a test can run them without a
// browser; the event handlers at the bottom only wire them up.
"use strict";

var ICON = "/brand/relay/icon-192.png";

/** A link inside the app that a notification may open; anything else is the chat page. */
function safeLink(link) {
  if (typeof link === "string" && /^\/chat(\?with=[A-Za-z0-9-]{1,64})?$/.test(link)) return link;
  return "/chat";
}

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
    tag: typeof p.tag === "string" && p.tag ? p.tag.slice(0, 64) : "taskiem-chat",
    // How many conversations have something unread, for the icon's badge; -1 when the push does not say.
    unread: typeof p.unread === "number" && p.unread >= 0 ? Math.floor(p.unread) : -1,
    icon: safeIcon(p.icon),
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
      data: { link: p.link },
    },
  };
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

if (typeof module !== "undefined") {
  module.exports = { safeLink: safeLink, safeIcon: safeIcon, parsePayload: parsePayload, notificationOf: notificationOf, iconFor: iconFor, staysSilent: staysSilent };
}

// --- the events: only in a service worker ---

if (typeof self !== "undefined" && typeof self.addEventListener === "function" && typeof ServiceWorkerGlobalScope !== "undefined") {
  self.addEventListener("install", function () {
    self.skipWaiting();
  });
  self.addEventListener("activate", function (event) {
    event.waitUntil(self.clients.claim());
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
    var link = safeLink(event.notification.data && event.notification.data.link);
    event.waitUntil(
      (async function () {
        var windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
        var mine = windows.find(function (w) {
          return new URL(w.url).origin === self.location.origin;
        });
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
  // again with the same key and tell the server, then forget the old one.
  self.addEventListener("pushsubscriptionchange", function (event) {
    event.waitUntil(
      (async function () {
        var old = event.oldSubscription;
        var key = (old && old.options && old.options.applicationServerKey) || (event.newSubscription && event.newSubscription.options && event.newSubscription.options.applicationServerKey);
        var sub = event.newSubscription || (key ? await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }) : null);
        if (!sub) return;
        var json = sub.toJSON();
        var headers = { "Content-Type": "application/json", "X-Taskiem-Request": "1" };
        await fetch("/v1/me/push", {
          method: "PUT",
          credentials: "same-origin",
          headers: headers,
          body: JSON.stringify({ endpoint: json.endpoint, keys: json.keys, label: "A browser" }),
        });
        if (old && old.endpoint && old.endpoint !== json.endpoint) {
          await fetch("/v1/me/push", { method: "DELETE", credentials: "same-origin", headers: headers, body: JSON.stringify({ endpoint: old.endpoint }) });
        }
      })().catch(function () {
        // Signed out or offline: the person turns it on again.
      }),
    );
  });
}
