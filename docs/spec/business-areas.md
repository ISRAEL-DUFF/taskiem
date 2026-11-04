# Business areas: each business turns on only what it uses

Status: **Plan** (drafted 4 November 2026; nothing built) · Builds on: the Schools switch (`api/schools.go`, `engine/schools`), [storefronts](../storefronts.md), [stock](stock.md), [invoices and the money log](invoices-and-money-log.md), [business links](business-links.md)

## 1. Why

Taskiem is a workflow tool for Nigerian businesses, like n8n or Zapier but for how they work. Today it behaves as if every business sells goods: the sidebar and the phone tab bar show Orders, Storefront and Money to anyone holding the permission (`storefront.manage`, invoice access), and Home loads storefront orders and draws sales tiles whether or not the business has a shop. A consultancy, a church, a school or an NGO sees empty shop screens it can never use.

Decisions taken with the founder:

1. Each business turns areas on and off for itself.
2. **Existing businesses keep everything on**; only new businesses start with areas off.
3. New businesses say what they do at signup, and that switches on the matching areas.
4. **The server enforces it**: an area that is off refuses its API, it is not only hidden.
5. Start with the area switches; the signup question comes after.

The Schools switch already works this way for one area (a settings row, `schools` in `/me`, `showSchools()` in the web app, and `need()` refusing with `ErrOff`). This plan generalises it and does not replace it.

## 2. The areas

| Area key | What it covers | Depends on |
| --- | --- | --- |
| `storefront` | The storefront and its look, fronts, bookings, riders, deliveries, trust and verification, the public page | `orders` |
| `orders` | Storefront orders, refunds and quotes; purchase orders to and from linked businesses | none |
| `stock` | Stock, suppliers, price lists and item links, stock by chat | none |
| `money` | Invoices, the money log and matching, bills, payments settings, reports, linked-business chat and links that carry documents | none |
| `schools` | The Schools area (unchanged behaviour) | `money` (fees are invoices) |

Always on, never switchable, since they are the product: workflows, forms, tables, runs and activity, approvals, connections, chat, templates, members, billing, audit, alerts and settings.

Turning an area on turns its dependencies on; turning one off is refused while another on area depends on it, with a plain sentence naming it. Turning an area off **hides and refuses; it never deletes**. Data stays, and turning it back on restores everything (as `schools.SetEnabled` already promises).

**To confirm before building:** whether `orders` is its own switch or part of `storefront`, and whether business links belong under `money` or get their own `links` area. The plan assumes the table above.

## 3. Storage

One table, `business_areas (tenant_id, area, enabled, updated_by, updated_at, PRIMARY KEY (tenant_id, area))`, under the same row-level security as other tenant tables.

- **Backfill, in the same migration:** for every existing tenant, insert `enabled = true` for `storefront`, `orders`, `stock` and `money`, and `schools` copied from `school_settings.enabled`. No existing business changes behaviour.
- **New tenants** get no rows, which reads as off. The signup question (phase 4) inserts rows.
- **Schools:** keep `school_settings.enabled` as the source for `schools` in phase 1, and have the areas API read and write it, so the existing endpoint, the chasing job's `taskiem_schools_enabled_tenants()` and the bursar role keep working. Folding it into `business_areas` can be a later clean-up.
- **A definer for background jobs.** Scheduled work runs outside a tenant's session (money digest, bill and payout ticks, chasing). Add `taskiem_area_enabled_tenants(area text)` as a SECURITY DEFINER read returning tenant ids only, modelled on `taskiem_schools_enabled_tenants()` (migration 00219). It needs an entry in `engine/db/definers.tsv`, or `TestDefinersAllowList` fails.
- Next migration number is `00305`.

## 4. API

- `GET /v1/settings/areas` returns every area with its state, what it covers in plain words and what it depends on. Any signed-in member may read it.
- `PUT /v1/settings/areas` takes `{ "storefront": true, ... }` and applies the dependency rules. **Only an owner** changes it, as for the Schools switch. Each change is audited (`settings.areas`, with the area and the new state) in the tenant's chain.
- `GET /v1/me` gains `areas: { storefront, orders, stock, money, schools }` (booleans). `schools` stays in `/me` for the old client.
- Taskiem Ops: the tenant page shows each tenant's areas, read-only, as it is read-only today.

## 5. Server enforcement

The rule: **permission and area must both pass.** A permission alone no longer opens an area that is off.

- Add `s.area(key)` middleware beside `s.need(perm)` in `api/server.go`. It reads the tenant's row inside the request's transaction and refuses with **404** and a plain body ("this business has not turned on Stock") when the area is off. 404, not 403, so an off area looks the way a feature that was never there looks (the web app already treats a 404 as "not turned on", see `Home.tsx`).
- Apply it to whole route groups, not route by route, so a new route cannot be forgotten:
  - `storefront`: the `/storefront/*` routes except orders, `/riders*`, bookings, deliveries, fronts.
  - `orders`: `/storefront/orders*`, `linkOrderRoutes`.
  - `stock`: stock and supplier routes, `priceListRoutes`.
  - `money`: `invoiceRoutes`, `billRoutes`, money and reports routes, `linkRoutes` and `linkChatRoutes`.
  - `schools`: already enforced by `schools.need`; leave as is.
- **Other doors into the same data** must be gated too, or the switch leaks:
  - WhatsApp and chat commands that act on an area (`whatsapp_storefront.go`, `whatsapp_stock.go`, `whatsapp_bookings.go`, `invoice_chat.go`, the money chat words): reply with a plain "turn on Stock in Settings" when it is off.
  - Public pages and webhooks: a published storefront whose area is turned off stops serving (or shows closed) rather than taking orders no one can see.
  - Background jobs: the money digest, bill and payout ticks and reminders skip tenants without `money`, through the new definer.
  - Workflow steps and connectors that read or write an area (storefront, stock, invoice, link steps): refuse with the same sentence when the area is off, so a workflow cannot reach into an area the business turned off. The list of such steps has to be found in the connector registry first.
  - Embedded sub-tenants (partners): sub-tenants follow their own rows; a partner can set a new sub-tenant's areas when it creates it. To be confirmed with how embedding creates tenants.
- A test walks the router and fails if a route that belongs to an area is mounted outside its `s.area` group, in the spirit of `TestGateResponsesCarryNoAmount`.

## 6. Web app

- Read `areas` from `/me` into one helper, `lib/areas.ts` with `areaOn(me.areas, key)`, modelled on `lib/schools.ts` and tested the same way.
- **Sidebar and phone tab bar** (`App.tsx`): each item's visibility becomes `can(perm) && areaOn(...)`. Orders, Storefront, Money, Bills and Invoices go behind their areas; the phone tab bar's four slots fill from what is on (a business with nothing on sees Home, Workflows, Activity).
- **Routes:** a guard component sends someone who types an off area's URL to Settings, with the plain sentence, instead of a page of 404 errors.
- **Home** (`pages/Home.tsx`): load orders, sales and money only when those areas are on; with none on, Home leads with workflows, runs and approvals.
- **Get started checklist** (`onboarding.tsx`): steps that mention the shop, stock or invoices appear only when their area is on.
- **Settings:** a new "What your business uses" page of switches, one row per area with a plain sentence about it (no words from Appendix C of `docs/spec/no-technical-steps.md`). Owner only; turning one off asks for a confirmation that names what hides and says nothing is deleted. The Schools switch moves here and keeps its own page for school settings.
- Follow `docs/brand/screens.md` (rows with `Items`/`Item`, phone first at 360 and 390 px), add the page to `e2e/accessibility.spec.ts` and to `src/lib/plainwords.test.ts`.

## 7. Phases

1. **Switches and enforcement.** Migration `00305` with backfill and the definer, the areas API, `/me.areas`, `s.area` on the route groups, and tests. Ships with no visible change for existing businesses, because everything is backfilled on.
2. **Web.** `lib/areas.ts`, nav and tab bar, route guard, Home, the Settings page, the checklist. Existing businesses still see no change; a business that switches an area off sees it disappear.
3. **Other doors.** WhatsApp and chat commands, public storefront pages, background jobs, workflow steps and connectors.
4. **Signup question.** "What does your business do?" with a shop, a school, a service business, or "just workflows", each switching on its areas; new signups start with the rest off. Ops shows areas on the tenant page.

Phases 1 and 2 are the first change to review; 3 and 4 follow.

## 8. Tests

- Go: an area off refuses its routes with 404 and a plain body, on and off again restores them with data intact; a member with the permission but without the area is refused; dependency rules in both directions; only an owner can change areas; audit entry written; backfill leaves every existing tenant's areas on (migration test); the definer returns only enabled tenants; the router-walk test above.
- Web: `lib/areas.test.ts`; nav and tab bar with each area off; route guard; Home with nothing on; Settings page switches; accessibility and plain-words lists.
- e2e: a business with nothing on sees workflows only, turns Stock on, sees it appear, uses it, turns it off, and the data is still there when it is turned back on.

## 9. Risks and open questions

- **Missed doors.** The worst bug is an area that looks off but still takes orders or sends money reminders. Hence group-level middleware, the router-walk test and phase 3 being its own phase.
- **Existing workflows** that use a step from an area the business later turns off: they should fail with the plain sentence, not silently skip. Decide whether to warn on switching off when a published workflow uses the area.
- **Backfill assumption:** "existing businesses keep everything on" is applied literally, including businesses that never used stock or storefronts. They can turn areas off themselves; Ops could later flag tenants with no data in an area.
- **Embedding and partners:** how sub-tenants get their areas.
- **Naming:** the owner-facing words ("What your business uses", the area names) need a pass against the plain-words rules and, ideally, a Nigerian small-business owner's ear.
