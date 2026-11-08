# Offline: a page that says so, for a page of the app opened with no network

Status: **built** (2026-11-08), on top of [chat push](chat-push.md). Owner's
guide: [Taskiem on your phone](../guide/on-your-phone.md#when-you-are-offline).

## 1. Summary

Opened from a phone's home screen with no network, Taskiem showed the
browser's own error (on Android, "You're not connected" over a blank app),
which reads as a broken app. Now the service worker answers that one case
with a plain page in Taskiem's look: **You're offline**, what will happen
(it opens by itself once back online), and **Try again**.

The worker is the one [chat push](chat-push.md) added, `web/public/sw.js`,
and it is now registered for everyone, not only for people who turn
notifications on.

## 2. Decisions

| # | Question | Decision |
| --- | --- | --- |
| 1 | What is shown offline | A page that says so. Not a copy of what was last shown: that would keep a business's figures, orders and messages on the phone, readable by the next person to pick it up and shown after someone else signs in, with no way to tell how old they are |
| 2 | What the worker keeps | Nothing: no cache at all. The offline page is written in the worker. The app's files keep their year-long browser caching (`/assets/*`, `api/static.go`), so a repeat visit already loads little |
| 3 | What the worker answers | Only a navigation (a page of the app being opened: `GET`, `mode: navigate`, this origin). The API, files, pictures and other sites go to the network untouched, so pages online are exactly as they were, and tests that stand in for the API still see every call |
| 4 | When the offline page is shown | Only when the network cannot be reached at all (the fetch rejects). Any answer from the server, an error page or a redirect included, is passed through as it came |
| 5 | Speed | Navigation preload is turned on where the browser has it, so a page starts loading while the worker wakes |
| 6 | Trying again | When the browser says it is online again, the page reloads itself (one line of script, allowed by its hash). A timed refresh would need no script but fails WCAG 2.2.1, and axe says so |
| 7 | Who runs the worker | Everyone, registered by `lib/worker.ts` once the page has loaded, in the built app only (not under Vite's dev server) |

## 3. The page

Status `503`, `Cache-Control: no-store`, and a policy of its own:
`default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-…'`
(nothing loaded from anywhere). Its colours are written in it, light and dark
as the device is, since the app's stylesheet cannot be fetched; the mark is
the brand's own `favicon.svg`, inline (a test checks they are the same). It
fits a 360 px phone and passes axe in both themes.

## 4. Tests

- `web/src/lib/push.test.ts`, *the worker offline*: which requests it answers;
  the network's answer passed through (a `502` too), the preload used, the
  offline page only on a failed fetch; the page's headers, its one script and
  its hash, no refresh, plain words; the mark matching the brand's file.
- `web/src/lib/worker.test.ts`: registered after load, with scope `/`; not in
  development or without workers; a failed registration ignored.
- `web/e2e/offline.spec.ts`, with the real worker: it starts for everyone and
  keeps nothing (`caches.keys()` is empty); online pages and the API are as
  before; offline, a page of the app is the offline page (axe in both themes,
  nothing sideways at 390 px), it opens itself once online, and Try again
  while still offline shows it again rather than the browser's error.

- **Browser tests block the worker** (`web/playwright.config.ts`,
  `serviceWorkers: "block"`), except the specs about it (`offline`,
  `chat-push`). Once the worker controls a page, Chromium can send that page's
  requests past `page.route`, so a test's stand-in for the server was skipped
  about one run in ten under load (`link-chat-taskiem`, "a message that did not
  go comes back to the box", reached the real server). People's browsers are
  not affected: their requests still reach the server untouched.

## 5. Not built

- **The app opening offline.** A copy of the app's files would open the app,
  but every page of it needs the server; without decision 1's kept data it
  would only show its own errors. To revisit if a page is ever built to work
  offline (stock taken in a market with no signal, say), with its own
  decision on what is kept on the phone.
- **The other origins.** Businesses' own domains and the public host are other
  origins: the worker does not reach them.
