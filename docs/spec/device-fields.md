# The app as a node: a device's senses in a workflow

Status: **D0 built** (22 November 2026; [as built](#14-as-built-d0)); **D1 built** (22 November 2026; [as built](#15-as-built-d1)) but for `"starter"` and tools' inputs, which wait for made tools M4 and M1; D2 is a plan. First written 19 November 2026. Taken from the founder's discussion recorded in pull request #211, where it was §13 of the made tools plan, and made a plan of its own because it is about the form step and needs little from made tools. Two points were settled by the founder there (§12, 1 and 2), and the third on the same day (§12, 3). Three things were added on review (§11). Its phases are D0 to D2 in [the build plan](build-plan-saved-tools-payments.md).

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
| **A scan from the home screen** | The app's scanner, outside any workflow, finds a code; a workflow with a `device` trigger of `kind: scan` whose `formats` match is offered, with the code as `trigger.body.scan` | **Yes, D2** (settled, §12, 3). It is a tap: the person chose to scan |
| **A tool from a shortcut** | The app puts a made tool on the phone's home screen or in a widget | **Yes, D2** (settled), as a way to call a tool |
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
| D2 | `scan` and `signature`; the scan from the home screen, with the `device` trigger of `kind: scan`; tools on the phone's home screen (§7) | D1; M1 for tools on the home screen | A stock count by scanning works in the app; a customer signs for a delivery on the rider's phone; a code scanned from the home screen, with no workflow open, offers the workflow that takes it |

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
| 3 | A scan from the home screen, and a tool on the phone's home screen, as ways in (§7)? | **Yes, in D2.** Settled by the founder on 19 November 2026. Both are a tap by the person; neither senses anything on its own |

## 13. Tests, when built

- **Go:** each field's validation and options; `why` required; a location sealed in history and masked to 100 m in messages, cards and logs; `source` set by the channel and never by the answer's own body; `require_source` holding a sent answer as `unverified`; `received_at` set by the server and `clock_differs`; a refused and an unavailable field; a photo's metadata removed; nothing of a location, photo or signature in a model's prompt (a test reads what is sent); erasure removing a person's locations.
- **The phone app:** the permission asked only at the tap, after `why`; an answer made offline and sent later; the camera, not the gallery; picture tests of the form in both themes.
- **Web end-to-end:** the browser's prompts allowed and refused; a file in place of the camera marked `sent`.
- **WhatsApp and Telegram:** a location and a photo message answering a question; a signature `unavailable`.

## 14. As built (D0)

Built 22 November 2026. No migration: a form request's `recipient` is already JSON, and an answer's `device` travels with the answer.

**The declaration.** A device field is a property of a form step's `schema` whose `type` is `location`, `photo`, `scan` or `signature` (`engine/wd/devicefields.go`). Such a schema is not plain JSON Schema, so it is never compiled as written: `wd.AnswerSchema` gives the schema the server checks its answers against (each device field the object it keeps), and `wd.WithoutDeviceFields` the one a page that cannot ask a device shows. `wd.Validate` checks:

| Refused | Message, in short |
| --- | --- |
| A device field without `why`, or a `why` over 200 characters | say why in one sentence, so the person knows before their device is asked |
| An option the kind does not take (`max_age` on a signature), a wrong `require_source` (`device` on a location, `camera` on a photo), `max_accuracy_m` not in (0, 100000], `max_age` not a duration, `allow_typed` not a boolean, `formats` empty or naming a code the scanner does not read (`qr`, `ean_13`, `ean_8`, `upc_a`, `upc_e`, `code_128`), `x-pii` on a location other than `location` or on a photo or signature | names the option |
| Two fields of one kind, or more than four device fields | at most one of each |
| A required `signature` with `to: {phone}` or `{email}` | they may answer in WhatsApp or Telegram, where there is no pad; make it optional |
| A device field in a workflow's inputs (any trigger, a form's included) or its types | asked by a form step; inputs cannot ask a device yet |

`require_source: "device"` where the question can only be answered in WhatsApp or Telegram: no `to` today is answered only there (a phone or an email gets a one-time link that opens in a browser, which can read a location; members answer in the app or the web), so the row refuses nothing yet. A link's person may be reached only in a chat, which is known when the run asks, not at validation: when `to: {link}` arrives in D1 it is a deploy warning and the run's waiting message, as §8 says for an empty role. The rows for a `location` on a customers' tool and a `device` trigger wait for made tools (M0, M1) and D2.

**Who is asked.** `to` is `{phone}`, `{email}`, `{role}`, `{member}` (a member's id, or an expression giving one: the step fails at run time if it is not a UUID) or `{link}`, or the string `"starter"`. A member's request is listed for them alone under To fill in (`GET /v1/form-requests` carries `member`), with an alert naming them; a member not in the business gets a note on the request and no alert. `{link}` and `"starter"` are in the schema so the contract is stable, and refused by the validator until D1 and M4.

**The answer.** The server reads each device field (`engine/forms/device.go`):

- `state` is `answered`, `refused` or `unavailable` (sent so by the person, with an optional `reason`), or `unverified` when the source is weaker than `require_source`.
- `source` is set from the channel, never from the body: a location read in the app, the web or a link's browser is `device`, an address typed is `typed`, one sent in WhatsApp or Telegram `sent`; a photo from the phone app is `camera`, any other `sent`; a scan with a format is `camera`, without one `typed`; a signature `drawn`.
- A location is `{lat, lng, accuracy_m, at, fix}` or `{address}` (when `allow_typed`, the default); `coarse: true` when its accuracy is unknown or worse than `max_accuracy_m`; refused when read longer than `max_age` before the answer was made. Keys the field does not keep are dropped.
- A device field a channel cannot answer yet, left out, is `unavailable`: today the member API takes a location and a scan as values, and photos and signatures need an upload it does not take yet; the form host's page asks no device before D1. A required location left out of the member API is refused as any missing answer is.
- Every answer, of a form step (`steps.<id>.output.device`) and of a form (`trigger.form.device`), carries `device: {app, version, online, at, received_at}` and `clock_differs`. `app` is `app` or `web` from the member API (what the client says; anything else is `web`), `link` from the form host; `received_at` is the server's clock; `at` is the device's from the member API, else the server's. `clock_differs` when the device's clock is more than 5 minutes ahead, or, online, more than 5 minutes behind: an answer made offline may be any age.

**Kept and shown.** `pii.SchemaPaths` seals a location's `lat`, `lng` and `address` as `location` (each coordinate its own value, so once opened it is remembered as personal); its state, source and accuracy stay readable. `whatsapp.MaskValue` masks a coordinate to three decimal places (`≈6.452`, about 110 m), and a typed address wholly; the run view without reveal shows the sealed value, and reveal shows it exactly. An ai step's input passes through `pii.HideLocations` before anything else, even fields it lets personal data through: a location becomes `{location: answered, state, source}`; coordinates in its prompt are masked by the run's known values. A location goes with the run's data when retention purges it. Photos and signatures are files under the forms' rules (scanned, cleaned, private; JPEG or PNG, a signature PNG), and a form step that asks for one needs the malware scanner to deploy.

**The web and the form host.** Not asked yet: the form host leaves device fields off its page and the web's form shows a line saying this page cannot read it; both send them `unavailable`. The web sends `device` with each answer. The phone app lists requests but does not answer them in the app, so it is unchanged.

**Left for D1.** The fields asked in the app (offline too), the web and WhatsApp and Telegram; `to: {link}` delivered through the rider steps' door and `"starter"` (M4); photo uploads on the member API; the map pin and area name on cards; and a location sealed under its respondent's own subject, so erasing a person erases where they were. That needs the run's taint to carry a subject: a copy of a location in a later step is sealed again from the taint, under the value's own subject, so binding only the answer would have erased one copy of several. Until then a location goes with the run's data when retention purges it, and with the submission when its respondent is erased.

**Tests.** `engine/wd/devicefields_test.go` (each row, the options, `to`, the answer schemas); `engine/forms/device_test.go` (`device`, `clock_differs`, `source` from the channel, states, refusals); `engine/forms/device_db_test.go` (a location sealed in history, masked to 100 m, opened exactly; gone with the run's purge; nothing of it in what an ai step sends); `engine/pii/location_test.go`; `api/forms_device_test.go` (a form step to one member, the alert, a location answered from the app); `web/src/lib/forms.test.ts`.

## 15. As built (D1)

Built 22 November 2026. Migration 00521.

**Who is asked.** `to: {link}` takes a business link's id, the linked business's id, or an expression giving either (the §6 fixture's `=trigger.body.rider`); `verify` is refused with it. When the run asks, the api role finds the active link in the asking business's scope and goes through the door (`links.EnterTx`, `link.form.asked`, audited on both sides): the linked business keeps a note of the request (`form_request_doors`, its id the request's) for the person working the conversation, **its owner** (a rider's business is one person; a dispatch company's owner hands it on), with an alert. The request itself stays in the asking business. The person finds it under To fill in (`GET /v1/form-requests` lists it with `from`, the asking business's name), opens it and answers it with the same endpoints; the answer is the asking business's, its respondent `{kind: linked, member_id, business}`. A link that has ended, or a linked business with no owner, is noted on the request and asked of nobody, as an empty role is. `"starter"` stays refused until made tools M4.

Members by name or role, and a link's person, are asked **in the app and the web first** (To fill in, an alert and a notice), and **then in WhatsApp and Telegram** where they are linked (`forms.Service.Ask`, the api's `FormAsk`): one message with each location and photo the question asks, its why, and the app's link. A question that also requires something typed is given the link alone.

**WhatsApp and Telegram.** A location message and a photo message from a person with a form step open for them (theirs, their role's, or put to them through a link) answer its location and photo fields, newest question first (`api/forms_chat.go`, called in the conversation before the chat's other uses of a location or a photo; the chat command dispatch is unchanged). What is sent is kept in the asking business (`form_chat_answers`, a location sealed as the question seals it) until the question is whole: every required location and photo, or every one when none is required. Then it is sent as any answer is, its channel the chat's: each field's source `sent`, a signature or a scan `unavailable` ("a signature cannot be given in WhatsApp"), the photo fetched from the chat app and taken through the forms' file rules. What a chat sent towards a question goes when it is answered, withdrawn or expires (see On review below).

**`require_source`.** A chat answer to a field that requires the device's reading or the camera is held (`held`), and the person is told why, given the app's link, and offered **Send as it is** (or the word `send`). Sent so, the field is `unverified`; answered in the app meanwhile, it is a reading. `require_source` can always be met by a link's person (they can answer in the app), so it is no deploy warning.

**The app and the web.** `POST /v1/form-requests/{id}/answers` takes a multipart form: `answers` and `device` as JSON fields and each photo, signature or file as a part named by its question (at most 10, 25 MB each), scanned, cleaned and kept as the forms' files are. The web's form step page (`FormRequestPage`, `forms/DeviceField.tsx`) asks the browser only at the tap, after the why: **Use my location** (the browser's own prompt; a refusal is sent `refused`, a failure `unavailable`; an address may be typed where the field allows), a photo by the camera where the browser offers it (`capture`) or a file, which the server marks `sent` either way (it cannot tell them apart), a signature pad, a code typed; **I can't answer this** on each. The phone app's screen (`mobile/lib/chat/form_request.dart`, from Today) reads the location at the tap only, takes the photo with the camera (never the gallery, cleaned to 2,000 px), sends a signature or a scan `unavailable` until D2, and keeps an answer made with no connection on the phone (the photo in its media) to send when it is back, with `online: false`.

**The delivery proved.** `riders@1 record_proof` takes the §6 step's answers (`where`, `photo`) and an order's reference: the delivery keeps the location only at its masked point with its source (`deliveries.proof_where`) and a mark (`proof_mark`: `confirmed_at_door` when read by the phone within 150 m of the customer's own pin and not coarse, `far_from_address`, `said_by_rider` for a pin sent in a chat, `read_by_phone` when the customer gave no pin, `no_location`), and the photo beside its other photos with its source (`delivery_proofs.source`). The Confirm card (`GET /v1/chat-alerts`, kind `confirm`) carries `proof`: the masked pin, the photo's link, the mark and the photo's source; its line says which was read by the phone and which was sent; the web's card shows the photo, the pin as a map link (never coordinates in words) and the words. The fixture is the test workflow `proveDelivery` in `api/forms_device_d1_test.go` (§6's step, then `record_proof`); no template yet, because a delivery's run does not know its rider's link until the rider holds it.

**On review (22 November 2026, migration 00522).**

- *A photo is this answer's file.* A photo or a signature field takes from `answers` only `{state: refused | unavailable}`; the file reference comes only from the parts uploaded with this submission (`Service.files`), so the reference of an earlier photo (returned in an earlier 201) cannot be answered again as "taken in the app". `record_proof` takes a photo only when it is a file of a submission to the recording run (one of its form steps' requests, or the form that started it). `source: camera` from the phone app is still the client's word (`device.app`), as D0 says; a reused file is what is ruled out.
- *Answered once.* `Process` marks the request answered before recording the submission and requires that it was pending (`RowsAffected = 1`); otherwise the submission is refused `the form is no longer taking answers` and its stored files discarded, unless this same reference answered it (its signal is sent again). In a chat, a message is kept under the request's row lock (`ChatAdd`, `SELECT … FOR UPDATE`): the person's draft is read again under it, merged (`answers || new field`) and decided, so a location and a photo arriving together make the question whole once, with both, and two photos for a one-photo question send it once (the other is told it was answered already).
- *Theirs only.* A chat message answers only requests to a role the person literally holds, to them by name, or put to them through a link: no owner wildcard (an owner's product photo is not a cashier's answer). Who was messaged is not tracked per person, so it is not a further condition.
- *Each person's own draft.* `form_chat_answers` is keyed by `(request_id, user_id)`; each person reads, adds to, clears and sends only their own.
- *An ended link.* A door's note is listed, read (with its opened prefill) and answered, in the app, the web and the chat, only while the link between the two businesses is active (`forms.LinkedTx`, the active links `links.ListTx` shows, which is what `links.EnterTx` requires to cross). What a door request returns carries no `run_id` of the asking business (the nil UUID).
- *Drafts go with the request.* Every chat draft of a request is deleted in `Process`'s answer transaction (whichever channel answered), when its step or run is withdrawn (`runtime` cancels), and when it expires (the forms tick); 00522 deletes those already orphaned.
- *Nits.* A held photo is fetched from the chat app it was sent in (its stored channel), not the one the completing message came in on; `POST …/answers` checks the caller may answer the request before reading a multipart body of up to 60 MB.

Tests: `engine/forms/device_test.go` (a forged file reference refused), `engine/forms/chat_db_test.go` (two chat answers at once whole once with both fields; two cashiers' drafts apart; the owner's chat not matched to a cashier's question; drafts gone when answered in the app and when the run is cancelled; two answers at once, one refused and its file discarded), `api/forms_device_d1_test.go` (a reused photo id refused in the app and by `record_proof`; no `run_id` through a link; an ended link refuses list, read, answer and the chat).

**Left.** `"starter"` (M4) and tools' inputs (M1); the form host's one-time page (`to: {phone}`, `{email}`) asks no device yet; the phone app shows the Confirm card's words but not its pin and photo; a signature and a scan in the app, and scanning anywhere, are D2; a location sealed under its respondent's subject (D0's note) is unchanged.

**Tests.** `api/forms_device_d1_test.go` (a pin and a photo answering on WhatsApp and Telegram, source from the channel, a signature unavailable; require_source held and sent unverified; a delivery proved on WhatsApp and in the app through a link, with the Confirm card); `engine/forms/device_test.go` (the chat channels); `engine/wd/devicefields_test.go` (`to: {link}`); `engine/dispatch/proof_test.go` (the marks); `web/src/lib/forms.test.ts`, `web/src/lib/alerts.test.ts`, `web/e2e/device-fields.spec.ts` (the browser's prompt allowed and refused, a file marked sent, the Confirm card, axe in both themes); `mobile/test/form_request_test.dart` (the location at the tap only, the camera, Can't, offline and sent later; pictures `form-request-asked` and `form-request`).
