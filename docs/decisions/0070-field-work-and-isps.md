# 0070 — Field work is a general area whose journey is a record and whose steps are triggers; field agents work from the app only; the ISP area builds on it and signs subscribers in to PHP Radius by their proved phone; ISPOps is absorbed

Date: 2026-11-18 · Status: **Proposed** · Plans: [Field work](../spec/field-work.md), [internet providers](../spec/isps.md)

## Context

The founder, on 18 November 2026, asked for internet providers to be served fully, starting with **PHP Radius**: billing and RADIUS software its vendor hosts, giving each provider an admin dashboard and a client API that acts only as one subscriber ([reference](../integrations/phpradius-reference/README.md)). Providers want their subscribers to use that API from Taskiem's chat instead of the provider's branded portal, and to run their own staff in Taskiem: office staff making and assigning jobs, field agents going out to do them, and the office watching them.

The founder's own **ISPOps** (a Go backend, a React admin portal, a Flutter field app and a React customer app) does the staff and field half for one provider, which will start afresh on Taskiem: nothing is migrated. The founder framed it: providers' organisations are Taskiem businesses with the ISP part on; field agents and office staff are the business's staff with roles; the provider's customers are people on Taskiem who chat with it (PR #198); and the jobs, assignments and the field agent's on the way, arrived, done are a series of workflows. Having field agents and watching them is not a provider's idea only: it is general, as invoices, orders and stock are.

## Decision

1. **Field work is an area of its own, for any business** with staff who go to customers. Nothing in it is about internet providers. The **ISP area** depends on it (and on Money), as Schools depends on Money, and adds what only a provider needs.
2. **A job's journey is a record kept by the engine; each step is a trigger.** The engine keeps the state (new, assigned, on the way, arrived, working, done, closed; on hold, not finished, cancelled), who may move it, the time on the phone and at the server and the place of each step, and idempotency for steps sent again. Everything that happens because of a step (telling the customer, a rating, an invoice, an alert, a follow-up job, assignment by rule) is a workflow: the `job` trigger type in `wd/v1`, `job.<step>` signals, the `jobs@1` connector and templates. Recurring jobs are a schedule workflow, not part of the record.
3. **Field agents are members with a built-in Field agent role**, holding `job.work` only and refused by the server everywhere else, as the Gate role is. They work **from the Taskiem app only**, in a field mode of the one app (PR #198), not a second app and not by WhatsApp, Telegram or SMS, because chat apps cannot give the location the office needs.
4. **Collecting field agents' location is the business's choice, on by default**, changed by the owner only. When on, it is collected **only while on duty**, between the agent's own Start my day and End my day, in the background (an Android foreground service of type `location` started from the open app, so no background-location permission; iOS "while using" with the background mode, never "Always"), each report carrying its fix's own time, and kept for a retention the business sets (90 days by default). When off, nothing about place is collected, steps included. ISPOps collected per job and only with the app open; on duty widens it so the office can see free agents and suggest the nearest.
5. **Steps are queued offline**, with parts used as part of the Done step. This widens the app's rule that nothing changing money or stock is queued: parts used record a fact already true, and are taken from the agent's stock when the step lands, never below zero.
6. **Stock gains holders**: the store and each field agent, with `issued`, `used`, `returned` and `lost` movements. The catalogue's count stays the store's.
7. **PHP Radius is a built-in connector, and a subscriber is signed in by their proved phone.** `phpradius` acts as one linked subscriber through PHP Radius's `loginPhone`, called only with a phone number Taskiem has proved belongs to the person asking. The token is kept sealed for that person and provider. Taskiem never asks for, sees or keeps a PHP Radius password. Recharges are paid to the **provider's own** gateway with the public key PHP Radius gives, and confirmed back to PHP Radius. Money never passes through Taskiem.
8. **ISPOps is absorbed, not integrated.** Its design comes over (§20 of the Field work plan); its own sign-in, organisations, chat, notifications, wallet, embedded payroll and customer app are replaced by Taskiem's; its repositories are archived once the provider runs on Taskiem. ISPOps is the founder's own work, so carrying its design over raises nothing under the clean-room policy.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| Keep ISPOps as a service and connect Taskiem to it | Two sign-ins, two sets of roles, two chats and two apps for one provider's staff, kept in step forever. ISPOps' stack is Taskiem's (Go, Postgres, Flutter, React), so absorbing it costs little more than connecting it |
| Field work inside the ISP area | Solar, CCTV, repair and cleaning businesses have the same needs; the founder named it general. A provider gets nothing less from a general area it depends on |
| Field agents as riders (people outside the business, reached by chat) | Riders are outside the business and becoming businesses of their own (0065); field agents are staff, tracked on duty, carrying the business's stock |
| The journey built from workflows (each step a workflow writing a table) | A business could break its own job states; offline idempotency, the time and place of each step and the role's limits would rest on workflows any owner edits. The record keeps what must hold; workflows keep everything around it |
| Field agents on WhatsApp or Telegram as riders work | No dependable location from a chat app; the founder chose the app only |
| Location always, or only at each step | Always is staff surveillance off duty and fails app-store review; only at steps cannot show "on the way", the live map or a stuck agent |
| Location per job only, as ISPOps did | Cannot show free agents or suggest the nearest; a business that wants less turns location off |
| Location always on, not a setting | Some businesses will not want it, or not yet; the founder chose a setting, on by default |
| A subscriber signs in to PHP Radius with their password in chat, or Taskiem keeps their password | A password typed in chat, or kept by a third party, for every subscriber of every provider. The proved phone and PHP Radius's own phone way in avoid both |
| Wait for an admin API from PHP Radius | It does not exist; providers' subscribers can be served now through the client API, and the provider's staff keep the dashboard they have |
| PHP Radius as a WebAssembly catalogue connector | A provider-facing area depends on it, and it is Taskiem's to keep working; built-in, as Paystack is |

## Consequences

- `wd/v1` gains the `job` trigger type (a contract addition); `jobs@1` and `phpradius` are new connectors; `engine/areas` gains `fieldwork` and `isp`.
- New module schemas `fieldwork` and `isp`, and stock holders in Stock's; tables classified as records, lists and engine state as the proposed [0064](https://github.com/ISRAEL-DUFF/taskiem/blob/claude/core-cleanup-0ak3hh/docs/decisions/0064-business-records-out-of-the-core.md) asks.
- The app takes on background location, with its review and real-phone testing, and a field mode chosen by the role in the business.
- The ISP plan's subscriber side waits for PR #198 to merge; the field side does not.
- Templates over PHP Radius reach **linked** subscribers only, which providers must be told.
- Three questions go to PHP Radius's vendor before recharges ship: third-party use of `loginPhone`, token lifetime, and whether a payment reference confirmed twice is refused.
- The subscription recipes of [business areas §9](../spec/business-areas.md#9-a-service-business-as-the-test-an-isp-or-cloud-provider) stay, for providers billing from a table.
