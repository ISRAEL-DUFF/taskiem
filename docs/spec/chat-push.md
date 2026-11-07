# Chat push: a notification when the app is closed

Status: **spec; step 1 (the server) built, steps 2 and 3 not.** Written 2026-11-07, after the profile, logo,
Explore and Contact info work of [business links](business-links.md) (§16.9 and §16.10 there, and §5.5 for
the conversation).

## 1. Summary

A message from a linked business should reach the person who answers it on
their phone or computer **when the Taskiem tab or app is closed**, the way a
WhatsApp message does: a notification with the business's name and a line of
the message, that opens that conversation when tapped, and an unread count on
the app's icon where the device allows it.

Today a message reaches a person in four ways: the page, when it is open
(the live stream, and a browser notification while the tab is hidden); the
**bell** (a notice, in the app); and a message on **WhatsApp, Telegram or
Slack** for people who linked one. The first needs the tab to be open and
the last three need the person to have set up a chat app. This adds a fifth,
**Web Push**, which a browser delivers to a closed tab through its maker's
push service. It is one more channel of the function that already tells a
business's people (`tellHoldersSent`), not a new system.

## 2. What exists, and what it leaves out

| Where a person hears of a message | How | Needs |
| --- | --- | --- |
| The page | the live stream (`GET /v1/chat/stream`) and `lib/chatNotify.ts`: a browser notification and a sound while the tab is hidden, never for a muted chat or the one in front of the person | the tab open |
| The bell | a notice (`notices.AddTx`) for each member whose role holds `chat.trade` | the app open |
| WhatsApp, Telegram, Slack | `tellHoldersSent` sends "[Dangote Stores] Musa: …" to each member with a linked address | a linked chat app |
| **Push (this)** | a message to each browser the member allowed | nothing open |

What the first three have in common, and push keeps: **who** (members whose
role holds `chat.trade`), **when** (a message from the other side, in an
active link, not muted by the receiving business: `links.MutedTx`), **what it
says** (the `words` function of `tellLinkMessage`: the sender's business, the
member's name, the text or what the file is, "about INV-0001" for a reply to a
document) and **where it goes** (`/chat?with=<link>`).

What none of them has: a notification for a person whose browser is closed
and who has no chat app linked. On a phone that is most of the day.

## 3. Decisions

Settled with the owner on 2026-11-07 (the owner answered "your suggestions"
to nine questions):

| # | Question | Decision |
| --- | --- | --- |
| 1 | Who gets a push | Members whose role holds `chat.trade` **and** who allowed it on a device. Allowing is per business (§5.1) |
| 2 | Where the keys go | `TASKIEM_VAPID_PRIVATE_KEY`, `TASKIEM_VAPID_PUBLIC_KEY` and `TASKIEM_VAPID_SUBJECT`; `taskiem push keys` makes a pair. With no keys the feature is off and the app says nothing about it |
| 3 | Preview | The same words as the WhatsApp message by default (sender's business, member, the line), with a switch **per device** for "New message" only |
| 4 | Taskiem's own chat | Not in the first version |
| 5 | iPhone | Web Push works only for an app added to the Home Screen: the settings page says so, in plain words, and asks for permission only there |
| 6 | Grouping | One notification per conversation, replaced by the next (the `tag`), not stacked; the push service is told the same (`Topic`) so a phone that was off gets one |
| 7 | Badge on the icon | In the first build (§6.5), where the device has the Badging API |
| 8 | Quiet hours, reply from the notification | Not in the first version |
| 9 | Where it is written | This file, before any code |

## 4. Corrections to the earlier plan

Two things I said when I first described this were wrong, and the design
here does not rely on them:

- **The server does not know which conversation a person has open.** The live
  stream polls per business and is told nothing about the page's focus or its
  open chat, and a phone that froze a tab leaves its connection looking open
  for minutes. So the server cannot "skip a person who has the chat open".
  Instead it **always** sends (subject to §5.2), and the **service worker**
  decides on the device (§6.2).
- **A push does not need its own queue or trigger.** `tellLinkMessage`
  already runs after the message commits, in the background, and already finds
  the members and the mute. Push is one more thing `tellHoldersSent` does for
  each member.

## 5. Design

### 5.1 Who allows what

A browser subscribes with its push service (Google's, Mozilla's, Apple's,
Microsoft's) and gets an **endpoint** and two keys. Taskiem keeps them for the
person who allowed it, **in the business they were in** (a person in two
businesses allows each separately: each has its own chats, its own muting and
its own `chat.trade`).

- Allowing is a **switch on this device** in Notifications settings and the
  "ask once" banner the chat page already has (`ChatLiveProvider`). The
  browser asks for permission only when the person turns it on.
- A **device list** under the switch names each browser (a label made from
  the browser and system, which the person may rename) with when it was last
  used and **Remove**. Signing out of a browser removes its subscription.
- Each device has its own **"Show what the message says"** switch (on), and a
  short plain note that the words pass through the browser maker's servers
  (§8).

### 5.2 When a push is sent

For a message from the other side that `tellLinkMessage` tells, to each member
who holds `chat.trade`, has a subscription in this business and is not the
sender:

1. The receiving business has not **muted** the conversation (the same
   `links.MutedTx` check as the phone messages).
2. The link is **active** (never a message of an ended link).
3. The subscription is not marked **gone** (§5.4).
4. The words are the `words` function's, except that a device with the preview
   off is sent "New message" under the sender's business name.
5. A document card says only what it is, as `lib/chatNotify.ts` does today: no
   references, amounts or names ("Dangote Stores sent an invoice").

What is **not** a condition: the member's page being open (§4). The service
worker and the same `tag` as the page's own notification make a second one
replace the first, not repeat it.

### 5.3 The data

One migration:

```sql
CREATE TABLE push_subscriptions (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  user_id      uuid NOT NULL,
  endpoint     text NOT NULL,
  endpoint_hash bytea NOT NULL,       -- sha256(endpoint): finds a device without a scan
  p256dh       text NOT NULL,
  auth         text NOT NULL,
  label        text NOT NULL,
  preview      boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_sent_at timestamptz,
  last_ok_at   timestamptz,
  failed_since timestamptz,           -- the first failure of the run in progress
  UNIQUE (tenant_id, user_id, endpoint_hash)
);
```

Row-level security by `tenant_id`, as every tenant table; the application role
reads and writes only its own business's rows, and a person reads only their
own (`user_id` of the session) through the API. `endpoint`, `p256dh` and
`auth` are **never returned** by any endpoint (write-only), are deleted with
the member, and are listed in the data map for the threat model.

At most **10 devices per person per business**: an eleventh replaces the
oldest.

### 5.4 The API

All under `/v1/me/push`, for the signed-in person, in the business they are in;
none needs a permission beyond being a member, and a sender must still hold
`chat.trade` at send time (§5.2).

| | |
| --- | --- |
| `GET /v1/me/push/key` | the public key, or 404 when the feature is off (no keys set) |
| `GET /v1/me/push` | this person's devices: id, label, preview, created, last sent, last ok (no keys, no endpoint) |
| `PUT /v1/me/push` | `{endpoint, keys: {p256dh, auth}, label, preview}`: adds this browser, or updates its label and preview. **The endpoint must be `https` and on a push service's host** (§8) |
| `PATCH /v1/me/push/{id}` | rename a device, switch its preview |
| `DELETE /v1/me/push/{id}` | remove a device |
| `DELETE /v1/me/push` | `{endpoint}`: this browser took itself off (sign-out, the switch) |

A device is dropped when the push service answers **404 or 410** (gone), and
marked **failed** by any other lasting error; a run of failures for three days
drops it. Each change is audited (`push.add`, `push.remove`), without the
endpoint.

### 5.5 Sending

- **Library:** a small, maintained Web Push library (RFC 8030, 8188 and 8291
  for the encryption, 8292 for VAPID). The candidate is
  `github.com/SherClockHolmes/webpush-go` (MIT, on the allow-list of
  `tools/licencecheck/policy.json`); the build must show its tree passes the
  licence gate and `govulncheck`, and a design that resembles an existing
  product is recorded in `docs/decisions/` (Web Push is an open standard, so
  the record is short). If the library is not acceptable, the encryption is
  about 300 lines of standard library and is written here instead.
- **VAPID:** the private key signs each request; `TASKIEM_VAPID_SUBJECT` is a
  `mailto:` or `https:` address for the push service to reach us.
- **Egress:** each send goes through the egress guard (`engine/egress`), with
  the purpose `webpush` and the host of the subscription's endpoint, so it is
  allow-listed, logged and bounded in time (10 s) like every outbound call.
- **Payload:** JSON, at most 2 KB (the limit is 4 KB), encrypted for the
  device: `{title, body, link, tag, v}`. `title` is the other business's name,
  `body` the line, `link` `/chat?with=<link>`, `tag` the link id, `v` the
  payload version. No message id, no reference, no amount.
- **Headers:** `TTL: 43200` (12 hours: after that the message is stale
  anyway), `Urgency: high`, and `Topic` the first 32 characters of the
  base64url SHA-256 of the link id, so an undelivered push is replaced at the
  push service by the next one for the same conversation.
- **Timing and failure:** in the background after the commit, like the phone
  messages (`s.background`), each device on its own, one retry after 5 s for a
  network error or a 429/5xx, then the failure is recorded (§5.3). There is
  **no durable queue in the first version**: a push lost to a server restart in
  the second after a message is not retried (the bell and the page still have
  it). This is the same promise the WhatsApp channel makes today.
- **Metrics:** `taskiem_push_total{result=sent|gone|failed}` and the latency of
  the push service's answer.

## 6. On the device

### 6.1 The service worker

One file, `/sw.js`, served from the root with `Service-Worker-Allowed: /`,
`Cache-Control: no-cache` and `Content-Type: text/javascript`; the app's
content policy already covers a worker through `script-src 'self'`, and
gains an explicit `worker-src 'self'` so that stays true if the script rule changes. It is registered only when the
person turns push on (a person who never does runs no worker). It has no
fetch handler (no offline mode is promised or built here).

### 6.2 `push`

1. Parse the payload; an unknown `v` or a bad one shows a generic "New
   message".
2. **If a window of the app is visible and focused**, and the browser lets a
   push be silent when one is (Chrome on a computer does; Firefox only within
   a quota, so it is shown), show nothing: the page has the message. **Otherwise show it, always:** Safari and
   iOS end a subscription that shows nothing.
3. `showNotification(title, {body, tag, renotify: false, icon, badge,
   data: {link}})`. The `tag` is the link id: the page's own notification
   (`lib/chatNotify.ts`) already uses `tag: m.link`, so a hidden tab and a push
   for the same message are one notification, not two.
4. `icon`: the app's own icon (`/brand/relay/icon-192.png`) in the first
   build. **The business's logo** (`/v1/links/{business}/logo`) needs the
   browser to fetch it with the person's cookie, which not every browser does
   for a notification's icon; it is tried in the build and used only if it
   works in Chrome, Firefox and Safari.

### 6.3 `notificationclick`

Close the notification; focus an open window of the app and send it a message
to go to `link`; if none, `clients.openWindow(link)`. A person in two
businesses is taken to the address and the app's own switch of business
applies (to be checked in the build: §10).

### 6.4 `pushsubscriptionchange`

A browser may replace its subscription (its keys expired): the worker
subscribes again with the public key and `PUT`s the new one, `DELETE`s the old
by endpoint.

### 6.5 The badge

The unread count, on the app's icon where the device has the Badging API
(`navigator.setAppBadge`: installed apps on Chrome, Edge and Safari). The page
sets it from the unread count it already has (the tab title carries it today);
the push payload carries `unread` (the number of conversations with something
unread, as the Chat tab's badge counts them) so the worker sets it too while
no page is open, and a page clears or corrects it when it opens. Where the API
is missing nothing changes.

### 6.6 The words in the app

Plain words on an owner's screen (no "endpoint", "subscription", "service
worker" or "VAPID"), tested by `lib/plainwords.test.ts`:

- The switch: **Notify me on this device when a business writes**.
- Under it: a line saying what it does, "You get a notification here even when
  Taskiem is closed. Turn it off any time."
- iPhone, in Safari and not installed: "To get notifications on an iPhone, add
  Taskiem to your Home Screen first: tap Share, then Add to Home Screen." The
  switch is not offered until it is installed.
- The preview switch: **Show what the message says** with "Off: the
  notification says only that a business wrote."
- A blocked permission: "Notifications are blocked for Taskiem in this
  browser. Allow them in the browser's settings for this site."
- The device list: "This device", other devices by their label, with **Remove**.

## 7. Configuration and deployment

- Three settings (§3, 2) and `taskiem push keys`, which prints a pair to put
  in the operator's secrets. A pair is made once for a deployment: **changing
  it invalidates every subscription**, which then fail and are dropped (§5.4)
  until each person turns the switch on again; the docs say so.
- With no keys: `GET /v1/me/push/key` is 404, the switch and the device list
  are not drawn, nothing is sent. The other channels are untouched.
- `/sw.js` and the CSP change ship with the web build, not behind the keys, so
  turning the keys on needs no web deploy.
- `docs/operations.md` gets a section: the keys, the subject, the push services'
  hosts the server must reach (§8) and the note about changing a pair.

## 8. Privacy and security

| Concern | Control |
| --- | --- |
| A subscription's endpoint used to make the server call anywhere (**SSRF**) | `PUT` accepts only an `https` URL whose host is on a short list of the push services (`fcm.googleapis.com`, `*.push.services.mozilla.com`, `*.push.apple.com`, `*.notify.windows.com`) with no user information, no non-default port and no redirect followed; the send goes through the egress guard with the endpoint's own host as the only host allowed. A new service is added by a change to the list, not by a setting |
| The words of a message read by the browser makers' servers | The body is **encrypted** to the device (RFC 8291): the push service carries it but cannot read it. It does see that a push was sent, to which device, when, and how big. The device preview switch (§5.1) keeps even the words off the screen of a locked phone |
| Message text on a locked phone's screen | The per-device preview switch; documents say only what they are (§5.2). The same exposure already exists for a member who linked WhatsApp, which the person chose |
| One business receiving another's messages | A push is sent only to subscriptions of the **receiving** business's members, found by `tenant_id`; the subscription table is row-level secured, and no endpoint returns another person's device |
| A stolen subscription (endpoint and keys) used to push to a browser | A push needs the VAPID private key as well: the push service rejects one not signed by the key the browser subscribed with. The keys stay in the operator's secrets |
| A member who loses `chat.trade` or is removed still gets pushes | The role is read **at send time** (§5.2), and a removed member's rows are deleted with them |
| A shared computer | Signing out removes that browser's subscription; the device list shows and removes any |
| Replaying or forging a click's target | `link` is a path inside the app, opened by the app's own router; the app checks the conversation belongs to the business that is signed in, as any `/chat?with=` does |
| Abuse: a business flooding another with messages becoming a flood of pushes | The send limits on the messages themselves (per day, per burst) bound the pushes; one notification per conversation is shown (the tag) |

Data map: `push_subscriptions` holds an address of a browser and keys that
let anyone with the VAPID key send to it: personal data, kept only while the
person has the switch on, deleted with the member, never in logs (the audit
entry and the metrics carry no endpoint).

## 9. Tests

- **Go**: the endpoint allow-list (an `http` address, a foreign host, a
  user-info address, a redirect: each refused); one device added twice is one;
  the eleventh replaces the oldest; a person reads and removes only their own;
  a viewer without `chat.trade` is not sent; a muted conversation, an ended
  link, the sender and a preview-off device (the words) each as §5.2; a 410
  drops the device and a run of failures drops it after three days; the
  payload is under 2 KB and holds no reference or amount; the `Topic` is
  stable per link; a fake push service (like the fake storage the media tests
  use) checks the encryption headers and the VAPID signature; row-level
  security (a member of another business reads nothing); with no keys nothing
  is sent and the key endpoint is 404. The definers allow-list and the
  dispatch-columns test pass unchanged (no new definer is planned).
- **vitest**: the plain words, the device label, the badge count, the
  decision of the worker's `push` (visible and focused: nothing; else show),
  the payload parser.
- **Playwright**: the switch and the device list against the real server with
  a stand-in for the browser's push API (`PushManager` in the test browser
  needs a fake endpoint: the test registers the worker, subscribes to a local
  push endpoint it controls and sends one), the iPhone note (a user agent),
  the blocked-permission words, a phone width and the accessibility check in
  both themes. **The real delivery to a locked phone cannot be tested from the
  build machine**: the build ends with a short list for the owner to try on
  Android and iPhone.

## 10. Risks and open questions

- **A person in two businesses.** The click opens `/chat?with=<link>` in
  whichever business the app has active; if the link belongs to the other one,
  the app must switch or say so. To be checked in the build; if it does not
  today, the notification carries the business and the app switches first.
- **The business logo as the icon** may not load with the cookie (§6.2.4): the
  fallback is the app's icon.
- **iPhone behaviour** (a silent push ends the subscription) is why §6.2.2
  always shows on Apple devices; this needs a real device to confirm.
- **No durable queue** (§5.5): a restart in the wrong second loses a push. If
  that matters, the answer is the same queue the phone messages would get
  (`sender.Queue`), for both, in one change.
- **Quotas.** Push services throttle a sender that pushes too much to a
  device; one notification per conversation and the message limits keep us far
  from it, and a 429 is retried once and then recorded.
- **Other kinds of notice.** Bills, orders and reminders also use
  `tellHolders`. They are not pushed in the first version (only a chat
  message is); adding one is a call from the same function with its own words.

## 11. Build order

Three changes, each with its tests, in this order:

1. **Server**: the migration, the endpoints, the sender with the allow-list
   and the egress guard, the call from `tellLinkMessage`, the keys command,
   the config, the metrics, `openapi`, the threat model row and this file's
   "As built". Nothing visible yet: no worker subscribes.
2. **The device**: `/sw.js` and the CSP, the switch, the ask-once banner, the
   device list, the iPhone note and the plain words, the `push` and
   `notificationclick` handlers.
3. **Polish**: the badge, the business logo as the icon if it works, the
   "As built" and the owner's device checklist.

## 12. As built (step 1, the server)

The server half: nothing is visible to a person yet, because no browser
subscribes until step 2. Migration 00402.

- **The package** `engine/push`: `Config` (the keys; with none the feature is
  off), `ValidEndpoint` (the allow-list of §8: `https`, the host a push
  service's, no user information, no other port, no fragment), `Subscription`
  and the database functions (`AddTx`, `ListTx`, `UpdateTx`, `RemoveTx`,
  `RemoveEndpointTx`, `TargetsTx`, `RecordTx`, `DropOrphansTx`), `Payload`
  (JSON, at most 2 KB; a browser with the preview off is sent "New message"),
  `Topic`, and `Sender`. The sender uses `github.com/SherClockHolmes/webpush-go`
  (MIT; the licence gate passes with it, 81 dependencies, none failing) with
  the egress guard's client (purpose `webpush`, the endpoint's own host the only
  one allowed, no redirect followed), TTL 12 hours, `Urgency: high`, a `Topic`
  per conversation, one retry after a network error, a 429 or a 5xx, and 404
  and 410 as gone. The VAPID subject goes to the library without its `mailto:`
  (it adds it).
- **The table** `push_subscriptions` as §5.3, row-level secured by business;
  `endpoint`, `p256dh` and `auth` are never returned. A browser that answers
  404 or 410 is deleted; one that fails is marked (`failed_since`) and dropped
  after 72 hours of failing. A person's last role in the business ending
  (revoking a role, SCIM, single sign-on) deletes their browsers there.
- **The API** `/v1/me/push` as §5.4 (`GET /key`, `GET`, `PUT`, `PATCH /{id}`,
  `DELETE /{id}`, `DELETE` by address), for a person and not an API key; 404
  for all of it when the server has no keys. The address is checked with
  `Sender.Check` before it is kept; each change is audited (`push.add`,
  `push.remove`) without the address.
- **Sending:** `tellLinkMessage` (after the message commits, in the
  background) calls `pushMessage` first and the phone messages after.
  `pushMessage` finds the members whose role holds `chat.trade`, their
  browsers, and the number of conversations with something unread, sends to up
  to eight browsers at once, and records each outcome. The body is the line the
  phone messages carry **without the "about INV-0001" prefix** (a push carries
  no document reference, §5.2.5), and a document card is "Sent you a
  document". The mute is the one `tellLinkMessage` already checks. (A link
  that has ended cannot receive a message, so the "active link" rule of §5.2.2
  holds by what the message API already refuses.)
- **Settings and command:** `TASKIEM_VAPID_PUBLIC_KEY`,
  `TASKIEM_VAPID_PRIVATE_KEY`, `TASKIEM_VAPID_SUBJECT` (both keys or neither;
  the subject with them), `taskiem push keys`; metrics `taskiem_push_total`
  and `taskiem_push_seconds`; the rows in [operations](../operations.md).
- **Tests:** `engine/push` (the allow-list, the payload and its cut at a
  letter's edge, the topic, the configuration, and a fake push service that
  checks the headers, the encryption and the VAPID signature, each outcome and
  that a redirect is not followed), `TestChatPush` (no keys: 404; the
  allow-list at the API; one browser once; each person sees and changes only
  their own; a message pushed to the member who writes and not to the viewer,
  **decrypted as a browser does** and not readable by another's keys; the
  preview off; mute; failure kept, 410 dropped; ten at most; a browser taking
  itself off; a role ending), `cmd/taskiem` (the settings and `push keys`).
- **Not yet:** the service worker, the switch and the device list (step 2),
  the badge (step 3). No real push service was reached: a real delivery needs
  the keys on a deployment and a real browser, and is on the owner's checklist
  at the end of step 3.
