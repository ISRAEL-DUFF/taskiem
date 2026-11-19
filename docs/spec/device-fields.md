# The app as a node: a device's senses in a workflow

Status: **plan; not built** (19 November 2026). Taken from the founder's discussion recorded in pull request #211, where it was §13 of the made tools plan, and made a plan of its own because it is about the form step and needs little from made tools. Two points were settled by the founder there (§12, 1 and 2); one is still to decide (§12, 3). Three things were added on review (§11). Its phases are D0 to D2 in [the build plan](build-plan-saved-tools-payments.md).

Builds on: [forms](../forms.md) (the form step, files), [made tools](made-tools.md) (a tool's inputs, `to: "starter"`), [dispatch](dispatch.md) and [riders in the chat](chat-riders.md) (the delivery to be proved), [offline](offline.md), [privacy](../privacy.md).

## 1. The idea

A workflow can send to WhatsApp, Telegram and Slack as steps, and be started by what arrives there. The Taskiem app, and the web, are a channel too, and the device running them knows things no message carries: where it is, what its camera sees, what a barcode says, what a hand signed.

- A delivery is proved by where it was handed over and a photo of the parcel at the door.
- A stock count is a scanned code and a number.
- A meter reading, a site visit, an attendance register, a signed handover of equipment are the same.

This plan makes the device a node of the workflow in the one way that already exists for asking a person something: **the form step**, with **fields the device answers** when the person taps. There is no silent reading. A device answers a question, and the question says why it asks.

It serves what Taskiem is for: a business's work runs with less effort, beside its conversations. A rider on WhatsApp answers with WhatsApp's own location and photo messages; a rider on the app taps three buttons.

## 2. Who is asked

Every account is a person's (decision 0068), and a person is reached wherever they are. `to` on a form step, and on a tool's question (made tools §9), names a person, not a channel:

| `to` | Who | Reached |
| --- | --- | --- |
| `"starter"` | The person who started this run from a chat or a page (made tools M4) | In that chat; in the app or the web if they started it there |
| `{ "member": "<id>" }`, `{ "role": "<role>" }` | A member of the business, or everyone with a role | The app or the web first (a form request, with a push), then WhatsApp or Telegram if they are linked |
| `{ "link": "<business>" }` | The person on the other side of a link who is working the conversation: a rider, a supplier's clerk | In their side of the conversation, through the door the rider steps already use |
| `{ "phone": "=..." }`, `{ "email": "=..." }` | Anyone, by a one-time link, as today | SMS or email, then WhatsApp or Telegram where the number is on Taskiem |

The channel decides which device fields can be answered (§3). A question with a device field that the channel cannot answer offers the nearest thing (type the code; attach a photo) or, where there is none, says so and the field is `unavailable` in the answer.

## 3. Device fields

New field types in the forms schema, usable in a form step's `schema` and, once made tools exist, in a tool's `inputs`. Each is a field like any other: it can be required, it is validated, it is sealed and masked by its kind.

| Field | What the device gives | The app | The web | WhatsApp | Telegram | SMS and email link |
| --- | --- | --- | --- | --- | --- | --- |
| `location` | `{ lat, lng, accuracy_m, at, source }` | The phone's location, asked at the tap, once | The browser's prompt | WhatsApp's location message, sent by the person | Telegram's location, sent by the person | The browser's prompt |
| `photo` | A file (`{ id, name, type, size, sha256 }` as forms keep files), with `source` | The camera, not the gallery | The camera where the browser allows, else a file | A photo sent in the chat | The same | The browser's camera or a file |
| `scan` | `{ text, format }` of a barcode or QR code | The camera | The camera where allowed; else typed | Typed (WhatsApp has no scanner) | Typed | The camera where allowed; else typed |
| `signature` | A PNG file of what was drawn | A pad on the screen | A pad | `unavailable` | `unavailable` | A pad |

**`source` says how far to trust it** (added on review, §11). A workflow that needs proof must be able to tell a reading from a claim:

| `source` | On | Means |
| --- | --- | --- |
| `device` | `location` | Read from the phone or browser at the tap (`gps` or `network` is kept beside it) |
| `sent` | `location` | A pin the person sent on WhatsApp or Telegram. They may place it anywhere; it is what they say, not what the phone read |
| `typed` | `location`, `scan` | Typed by the person |
| `camera` | `photo` | Taken in the app or the browser's camera at the tap |
| `sent` | `photo` | A photo sent in a chat, or a file chosen in a browser. It may be an old one |

A field may ask for `"require_source": "device"` (or `"camera"`): an answer from a weaker source is then offered a link to answer in the app or the browser, and until then the field is `unverified` in the answer, which the workflow handles as it handles `refused`.

Every answer, device fields or not, carries `device: { app, version, online, at, received_at }`: which app answered (the phone app, the web, WhatsApp, Telegram, a link), its version where it has one, whether it was online when the answer was made, the time by the device's clock, and **the time the server received it**. A device's clock can be wrong or changed, so `received_at` is the one to rely on; where the two differ by more than the time offline could explain, the answer is marked `clock_differs`.

Options on a field:

| Option | On | What |
| --- | --- | --- |
| `why` | every device field, **required** | One sentence the person reads before the device is asked: "so the delivery is recorded where it was handed over". Shown above the system's own prompt |
| `require_source` | `location`, `photo` | As above |
| `max_accuracy_m` | `location` | An answer less accurate than this (a coarse network fix indoors) is marked `coarse: true`; the workflow decides whether that will do |
| `max_age` | `location` | A location older than this (a cached fix) is asked for again |
| `formats` | `scan` | Which code formats are accepted; others are refused with a plain message |
| `allow_typed` | `location`, `scan` | Whether the person may type an address or a code where the device cannot answer; default true |

## 4. How a device is asked

- **Always at the person's tap, never silently.** A device field is a button in the form ("Use my location", "Take a photo", "Scan", "Sign"). The device is asked only when it is pressed, after the field's `why`, and the system's own permission prompt follows. The app never asks for a permission before the person has pressed the field that needs it.
- **A refusal is an answer.** A person who refuses, or whose device cannot answer, sends the form with that field `refused` or `unavailable`. A required device field holds the form until it is answered or the person says why they cannot; the workflow handles both, as it handles `timed_out`.
- **One answer, one reading.** A `location` is read once per question. There is no following, no repeat reading, no background location (§12, 1). A route is many questions, each at a stop.
- **Offline.** The phone app takes the location, the photo and the scan offline and sends the answer when it is back, with `online: false`, so a rider in a dead spot is not stuck. The run waits as it does for any form step.
- **The web and the chat apps** ask the same way, with what they have (§3).

## 5. What the run gets, and what is kept

- The answer is the form step's output, as today: `steps.<id>.output.location`, `.photo`, `.scan`, `.signature`, `.respondent`, `.device`.
- **A location is personal data.** It is sealed in the run's history under `x-pii: location` as account numbers and phones are, masked in every message and card to the nearest 100 m and the area's name where Taskiem knows it, and shown exactly only to a person who may open the run. It is deleted with the run's data when retention purges it, and when the person is erased.
- **A photo and a signature are files** and follow the forms' rules: private storage, scanned for malware, cleaned of their metadata (so a photo's own location tag never travels with it), the same limits and the same deletion.
- **A scan is text** and is kept as any answer, unless the field marks it `x-pii`.
- A card or an answer that shows a location shows a map pin at the masked point and the area's name, never coordinates in words.
- **The AI never sees a location, a photo or a signature.** A model step is given that one was answered, and its `source`, nothing more.

## 6. Example: the delivery proved

The dispatch plan's **Delivered** step asks the rider for a photo when the business wants proof. With device fields it is one question, to the rider as a person, wherever they are:

```json
{ "id": "handover", "type": "form", "config": {
  "title": "Delivered",
  "to": { "link": "=trigger.body.rider" },
  "schema": { "type": "object", "required": ["where", "proof"], "properties": {
    "where": { "type": "location", "why": "so the delivery is recorded where it was handed over", "max_accuracy_m": 100 },
    "proof": { "type": "photo", "why": "a photo of the parcel at the door, for the shop's records" },
    "signed": { "type": "signature", "why": "if the customer will sign for it" } } },
  "timeout": "2h", "on_timeout": "continue" } }
```

The next step compares `steps.handover.output.where` with the order's address and marks the delivery `confirmed_at_door`, `far_from_address` or, where the location's `source` is `sent`, `said_by_rider`. The Confirm card (riders plan, phase 4) shows the pin and the photo, and says which it was. The rider on the app taps three buttons; on WhatsApp they send their location and a photo and skip the signature, which is `unavailable`.

## 7. A device as a trigger

The other direction, a workflow started by something the device does, is the smaller and later part:

| Trigger | What | In this plan |
| --- | --- | --- |
| **A scan from the home screen** | The app's scanner, outside any workflow, finds a code; a workflow with a `device` trigger of `kind: scan` whose `formats` match is offered, with the code as `trigger.body.scan` | **Proposed, D2** (§12, 3). It is a tap: the person chose to scan |
| **A tool from a shortcut** | The app puts a made tool on the phone's home screen or in a widget | **Proposed, D2**, as a way to call a tool |
| **Entering or leaving a place**, **moving**, **the app opened** | The device reports without a tap | **No.** Each is background sensing, with the stores' rules, battery and consent at stake. Its own decision when there is a case for it |

## 8. Limits, and what the validator refuses

| Limit | Value |
| --- | --- |
| Device fields a question | 4 (one of each) |
| Photos a question | As forms: 10 files, 25 MB each; a photo is cleaned to at most 2,000 px on its long side |
| Location readings | One a question; a question is not re-asked for a fresh reading more than once a minute |
| Scan formats | The common ones the scanner library reads (QR, EAN, UPC, Code 128); `formats` names a subset |

| Refused | Because |
| --- | --- |
| A device field without `why` | The person must be told before their device is asked |
| A `signature` required of `to: { phone }` or on a customer's tool | No surface they may be on can draw a pad; make it optional |
| A `location` asked on a tool `for: "customers"` | Customers are asked only what WhatsApp's own location message gives, by sending it; a workflow may read `trigger.body.location` when a customer sends one, never ask for it |
| `require_source: "device"` on a question that can only be answered in WhatsApp or Telegram | It could never be met |
| A `device` trigger of a kind in §7's last row | Not in this plan |

A `to` that names no person (an empty role, a link with no one working the conversation) cannot be known when validating: the deploy warns, and the run says so in its waiting message.

## 9. What this is not

- **Not tracking.** No background location, no continuous reporting, no reading of the phone's contacts, files, messages or other apps. A device answers a question the person sees, once, at their tap.
- **Not a new step.** The form step and a tool's inputs carry the fields; the run model and `decide()` are unchanged. The app, the web and the chat renderers learn four field types.
- **Not only for riders.** The example is a delivery because the dispatch plan asked for proof; a stock count, a site visit, a meter reading, an attendance register and a signed handover are the same question with the same fields.
- **Not proof beyond doubt.** A phone's location can be faked by a determined person, and a photo staged. `source`, `accuracy_m` and `received_at` say how far an answer can be trusted; a business that needs more (a customer's own confirmation, a code at the door) adds a step.

## 10. Phases

| Phase | What | Needs | Done when |
| --- | --- | --- | --- |
| D0 | The four field types and their options in the forms schema and `wd/v1`; `to` as a person on the form step; the validator's rows (§8); `device` and `source` on every answer; the sealing and masking of a location | Nothing | A form step with a `location` field validates; a location in a run's history is sealed and masked |
| D1 | `location` and `photo` in the phone app (offline too), on the web and on WhatsApp and Telegram, in form steps and in tools' inputs; `to` as `member`, `role`, `link` and `"starter"`; `require_source`; the delivery fixture (§6) | D0; M4 for `"starter"`; M1 for tools' inputs | A rider on WhatsApp and a rider on the app each prove a delivery; the Confirm card shows the pin and the photo and says which was read by the phone and which was sent |
| D2 | `scan` and `signature`; if agreed, the scan from the home screen and tools on the home screen (§7) | D1 | A stock count by scanning works in the app; a customer signs for a delivery on the rider's phone |

The phone app's parts are built with its screens guide and picture tests, in light and dark, as every screen is. The stores' forms are updated before D1 ships: location "while using the app" only, the camera, each with its reason in plain words.

## 11. Added on review (19 November 2026)

| Was | Is |
| --- | --- |
| A location's `source` was `gps`, `network` or `typed`, so a pin sent on WhatsApp, which the person may place anywhere, read like a reading | `source` is `device`, `sent` or `typed`; a photo has `camera` or `sent`; a field may `require_source`, and the example marks a sent location `said_by_rider` (§3, §6) |
| An answer carried one time, the device's | `received_at`, the server's time, beside it, and `clock_differs` (§3) |
| Nothing on what a model may see | A model is never given a location, a photo or a signature (§5) |
| Part of the made tools plan, with phases after T4 | Its own plan. D0 needs nothing; only `"starter"` and tools' inputs wait for made tools (§10) |

## 12. Decisions

| # | Question | Answer |
| --- | --- | --- |
| 1 | Does a device ever answer without the person's tap (background location, the app opened, entering a place)? | **No, asked only.** Settled by the founder on 19 November 2026: "ask for now". Background sensing is its own later decision |
| 2 | Who holds the device: a member, a rider, a customer, the author alone? | **Any person.** The question goes to a person and reaches them wherever they are; the channel decides which fields it can answer. Settled by the founder on 19 November 2026 |
| 3 | A scan from the home screen, and a tool on the phone's home screen, as ways in (§7)? | **To decide.** Recommended: yes, in D2. Both are a tap by the person; neither senses anything on its own |

## 13. Tests, when built

- **Go:** each field's validation and options; `why` required; a location sealed in history and masked to 100 m in messages, cards and logs; `source` set by the channel and never by the answer's own body; `require_source` holding a sent answer as `unverified`; `received_at` set by the server and `clock_differs`; a refused and an unavailable field; a photo's metadata removed; nothing of a location, photo or signature in a model's prompt (a test reads what is sent); erasure removing a person's locations.
- **The phone app:** the permission asked only at the tap, after `why`; an answer made offline and sent later; the camera, not the gallery; picture tests of the form in both themes.
- **Web end-to-end:** the browser's prompts allowed and refused; a file in place of the camera marked `sent`.
- **WhatsApp and Telegram:** a location and a photo message answering a question; a signature `unavailable`.
