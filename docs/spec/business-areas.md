# Business areas: each business turns on only what it uses

Status: **Plan** (drafted 4 November 2026; phase 1 built, the rest not) · Builds on: the Schools switch (`api/schools.go`, `engine/schools`), [storefronts](../storefronts.md), [stock](stock.md), [invoices and the money log](invoices-and-money-log.md), [business links](business-links.md)

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

**Principle: blocks are built in, recipes are assembled** (founder, 4 November). Taskiem is a workflow tool in the way n8n and Zapier are, so a feature that is a joining-together of existing triggers and actions ships as a **recipe** (a template in `templates/library` that an owner installs by answering a few plain questions), not as new built-in code. Built in, and kept small and solid, are the things where a mistake costs money or leaks data and so must not depend on how a business wired its steps: payment matching (a payment counts only when matched; no action marks an invoice paid), tenant isolation, audit, the outbound-traffic guard, approvals, and the connectors themselves. Before building any new feature, ask whether it can be a recipe over existing blocks; add a block only when it cannot. **Who assembles (founder, 4 November):** it does not matter whether the owner can assemble a workflow. A field agent (`docs/agents.md`) assembles it for them, so the job is to build every block that is needed and a very large library of recipes, to the point that agents and owners seldom have to do more than pick one and fill in a few answers. The plain-words rule of `docs/spec/no-technical-steps.md` still holds on the owner's own screens; it does not limit the agent's tools, which may show steps.

| Area key | What it covers | Depends on |
| --- | --- | --- |
| `storefront` | The storefront and its look, fronts, bookings, riders, deliveries, trust and verification, the public page | `orders` |
| `orders` | Storefront orders, refunds and quotes; purchase orders to and from linked businesses (a link carries them only where both businesses have `orders` on) | none |
| `stock` | Stock, suppliers, price lists and item links, stock by chat (price lists cross a link only where both businesses have `stock` on) | none |
| `money` | Invoices, the money log and matching, bills, payments settings and reports (invoices and bills cross a link only where both businesses have `money` on) | none |
| `links` | Business links (decision 0061): finding and linking businesses, the Linked businesses page, and the conversations between linked businesses. A link carries each kind of document only where both sides have that document's area on | none |
| `schools` | The Schools area (unchanged behaviour) | `money` (fees are invoices) |

Always on, never switchable, since they are the product: workflows, forms, tables, runs and activity, approvals, connections, chat, templates, members, billing, audit, alerts and settings.

Turning an area on turns its dependencies on; turning one off is refused while another on area depends on it, with a plain sentence naming it. Turning an area off **hides and refuses; it never deletes**. Data stays, and turning it back on restores everything (as `schools.SetEnabled` already promises).

**Decided:** `orders` is its own switch (founder, 4 November). `links` is its own switch (founder, 4 November), with each document type following its own area (§9).

## 3. Storage

One table, `business_areas (tenant_id, area, enabled, updated_by, updated_at, PRIMARY KEY (tenant_id, area))`, under the same row-level security as other tenant tables.

- **Backfill, in the same migration:** for every existing tenant, insert `enabled = true` for `storefront`, `orders`, `stock`, `money` and `links`, and `schools` copied from `school_settings.enabled`. No existing business changes behaviour.
- **New tenants** get no rows, which reads as off. The signup question (phase 4) inserts rows.
- **Schools:** keep `school_settings.enabled` as the source for `schools` in phase 1, and have the areas API read and write it, so the existing endpoint, the chasing job's `taskiem_schools_enabled_tenants()` and the bursar role keep working. Folding it into `business_areas` can be a later clean-up.
- **A definer for background jobs** (`taskiem_area_enabled_tenants(area)`, modelled on `taskiem_schools_enabled_tenants()` in migration 00219, with an entry in `engine/db/definers.tsv`) is built in phase 3 with the jobs that use it, not in phase 1. It has to apply the same default as the code (below).
- **Default for a business with no row:** a single function, `areas.DefaultOn` in `engine/areas`, says on for everything but Schools. Phase 1 ships with it on, so a business made between phase 1 and phase 4 still has everything and nothing is stranded before the Settings page exists (phase 2). Phase 4 first writes "on" rows for every business that has none, then flips the default to off; field-agent sign-ups (`POST /v1/agent/businesses`) and partner-created sub-tenants need their rows written at creation by then.
- Built as migration `00305_business_areas.sql`; the backfill runs before row-level security is turned on for the table, so it does not depend on the migration role.

## 4. API

- `GET /v1/settings/areas` returns every area with its state, what it covers in plain words and what it depends on. Any signed-in member may read it.
- `PUT /v1/settings/areas` takes `{ "areas": { "stock": true, "money": false }, "confirm": false }` and applies the dependency rules. **Only an owner** changes it, as for the Schools switch. It is atomic: all the changes apply or none. Each change is audited (`settings.areas`, with what changed) in the tenant's chain; the Schools switch is written through the existing `schools.SetEnabled` and audited as `settings.schools`. Turning one off that another on area needs, or asking for an area on and its need off at once, is 409. The answer is the same list `GET` gives.
- `GET /v1/me` gains `areas: { storefront, orders, stock, money, links, schools }` (booleans). `schools` stays in `/me` for the old client.
- Taskiem Ops: the tenant page shows each tenant's areas, read-only, as it is read-only today.

## 5. Server enforcement

The rule: **permission and area must both pass.** A permission alone no longer opens an area that is off.

- **Built as one central gate, not per-route middleware.** `areaGate` runs in `authenticate` (`api/auth.go`), after the caller is known, and refuses a request whose path is in an area that is off. `areaPaths` in `api/areas.go` maps path prefixes to areas, longest prefix first, so `/v1/storefront/orders` is Orders and `/v1/storefront` the shop. This is how the gate role is already refused (`gateAllowed`), and it means a route added later under a mapped path is covered without anyone remembering. The answer is **404** with a plain sentence and `area_off`, so an off area looks the way a part that was never there looks. One extra indexed read per request to a mapped path (not cached, so a switch takes effect at once).
- Apply it to whole route groups, not route by route, so a new route cannot be forgotten:
  - `storefront`: the `/storefront/*` routes except orders, `/riders*`, bookings, deliveries, fronts.
  - `orders`: `/storefront/orders*`, `linkOrderRoutes`.
  - `stock`: stock and supplier routes, `priceListRoutes`.
  - `money`: `invoiceRoutes`, `billRoutes`, money and reports routes.
  - `links`: `linkRoutes` and `linkChatRoutes`. `linkOrderRoutes` sit under `orders` and `priceListRoutes` under `stock`, so an off area refuses its documents even over a live link.
  - Cross-business writes (the `taskiem_link_enter` path of decision 0061) also check the area on **both** sides, so a mirror is never written into a business that has turned the area off; the sender is told in plain words that the other business does not use it.
  - `schools`: already enforced by `schools.need`; leave as is.
- **Other doors into the same data** must be gated too, or the switch leaks:
  - WhatsApp and chat commands that act on an area (`whatsapp_storefront.go`, `whatsapp_stock.go`, `whatsapp_bookings.go`, `invoice_chat.go`, the money chat words): reply with a plain "turn on Stock in Settings" when it is off.
  - Public pages and webhooks: a published storefront whose area is turned off stops serving (or shows closed) rather than taking orders no one can see.
  - Background jobs: the money digest, bill and payout ticks and reminders skip tenants without `money`, through the new definer.
  - Workflow steps and connectors that read or write an area (storefront, stock, invoice, link steps): refuse with the same sentence when the area is off, so a workflow cannot reach into an area the business turned off. The list of such steps has to be found in the connector registry first.
  - Embedded sub-tenants (partners), checked against the code: a partner creates one with `POST /v1/partner/sub-tenants` (`createSubTenant`, `api/partner.go`), which inserts the tenant, a workspace and the `dev` and `prod` environments. A sub-tenant is an ordinary tenant (decision 0015), so it gets its own `business_areas` rows and starts with none on. Embedded end users reach a sub-tenant only through `/v1/embed/{app}/...`, a separate route tree whose end-user permissions are capped at `workflow.*` and `run.*`, so they can never reach storefront, stock or money routes; the switches change nothing for them. What the switches do affect is members a partner invites to a sub-tenant (`inviteToSubTenant`), who sign in to the web app like anyone else and would otherwise see shop screens. So: sub-tenants default off; `createSubTenant` takes an optional `areas` list so a partner can start one with areas on; existing sub-tenants are backfilled on like every existing tenant; and a partner changes a sub-tenant's areas only by entering it (`taskiem_partner_enter`), the same way it does everything else there, with both audit trails written.
- `TestAreaRoutesAreMapped` walks the router and fails for a route whose path names an area's family (storefront, stock, invoice, money, bill, link, rider, deliver, dispatch, price, trade, supplier) but is in no area, unless it is listed as always on with the reason. The first run found the routes left out on purpose: Taskiem's own `/v1/billing/*`, `/v1/receive-money/*` and `/v1/settings/receive-money` (whether receiving money with Taskiem belongs to Money is open, phase 3), the Schools routes, form links and a person's own chat link.

## 6. Web app

- Read `areas` from `/me` into one helper, `lib/areas.ts` with `areaOn(me.areas, key)`, modelled on `lib/schools.ts` and tested the same way.
- **Sidebar and phone tab bar** (`App.tsx`): each item's visibility becomes `can(perm) && areaOn(...)`. Orders, Storefront, Money, Bills and Invoices go behind their areas; the phone tab bar's four slots fill from what is on (a business with nothing on sees Home, Workflows, Activity).
- **Routes:** a guard component sends someone who types an off area's URL to Settings, with the plain sentence, instead of a page of 404 errors.
- **Home** (`pages/Home.tsx`): load orders, sales and money only when those areas are on; with none on, Home leads with workflows, runs and approvals.
- **Get started checklist** (`onboarding.tsx`): steps that mention the shop, stock or invoices appear only when their area is on.
- **Settings:** a new "What your business uses" page of switches, one row per area with a plain sentence about it (no words from Appendix C of `docs/spec/no-technical-steps.md`). Owner only; turning one off asks for a confirmation that names what hides and says nothing is deleted. The Schools switch moves here and keeps its own page for school settings.
- Follow `docs/brand/screens.md` (rows with `Items`/`Item`, phone first at 360 and 390 px), add the page to `e2e/accessibility.spec.ts` and to `src/lib/plainwords.test.ts`.

## 7. Phases

1. **Switches and enforcement.** **Built:** migration `00305` with the backfill, the areas API, `/me.areas`, the central `areaGate`, the `engine/areas` package and tests. Ships with no visible change for existing businesses, because everything is backfilled on.
2. **Web.** `lib/areas.ts`, nav and tab bar, route guard, Home, the Settings page, the checklist. Existing businesses still see no change; a business that switches an area off sees it disappear.
3. **Other doors. Built.** Chosen so that an area that is off is refused where it is used, not only on the web API:
   - **Workflow steps.** The worker refuses a connector step of an area that is off (`storefront`, `riders` and the shop trigger's actions: Online shop; `stock`: Stock; `invoices`, `bills`: Money; `link`: Linked businesses; `schools`: Schools) before anything is sent, failing the step in plain words. Triggers are not stopped: an event that happens (a payment on an invoice that exists) still starts its workflow, whose steps then stop at the part that is off. Turning an area off lists the deployed workflows and triggers that use it (`areas.Affected`), and needs `confirm`.
   - **Chat.** On WhatsApp, Telegram and the web chat, the words of a part that is off are not commands: they pass on as ordinary chat. This covers English and the other languages' intents. A flow already in progress when its part is turned off is not interrupted.
   - **The public page.** Turning the shop off closes the page with "This shop is not taking orders at the moment."; turning it on opens it again, but only if the area closed it (a page its owner closed stays closed). Invoice pages already sent keep working, so customers can still pay what they were billed.
   - **Background jobs.** The money summaries, the money-leak check and the evening stock total skip a business whose Money or Stock is off (`taskiem_area_rows`, migration 00306, read through `areas.Only`). Jobs that finish money in flight (matching, payment checks, payouts) are not skipped.
   - **Across a link.** `links.EnterTx` checks that the other business has the part a document belongs to turned on (invoice or bill: Money; purchase order: Orders; price list: Stock; message or read: Linked businesses). If not, nothing crosses; an invoice stays due to be mirrored and crosses, after the usual wait, when the other side turns the part back on. The sender is not told in this phase.
   - **Not done:** receiving money with Taskiem stays always on (open question); the workflow builder and templates still offer steps for a part that is off (phase 5); the sender of an invoice or order to a business that does not use Money or Orders gets no word of it.
4. **Signup question.** "What does your business do?" with a shop, a school, a service business, or "just workflows", each switching on its areas; new signups start with the rest off. Ops shows areas on the tenant page.

5. **Service businesses.** The templates, signup choice and decisions in §9, with an ISP as the first test: build its monthly billing, reminder, cut-off and outage workflows from the templates and fix what gets in the way.

6. **Agents assemble workflows for owners. Status: in review; not to be implemented yet (founder, 4 November), but this is the goal the other phases serve.** Today an agent's session is limited to the storefront page and its tables, for 24 hours, as a viewer in a workspace of their own (`docs/agents.md`), so an agent cannot build a workflow in a business at all. This phase adds an agent session that can create, edit and test workflows, install recipes and set a business's areas, with the owner's agreement (the same `AGREE` code), a time limit, an audit trail in the business's chain, and **publishing left to the owner's approval** unless the owner says otherwise. It is security-sensitive and needs its own design and decision before building. Alongside it, the recipe library grows continuously: each business type gets its set (service businesses first, see §9).

Phases 1 and 2 are the first change to review; 3, 4 and 5 follow. Phase 6 stays in review until it has its own design and decision.

## 8. Tests

- Go: an area off refuses its routes with 404 and a plain body, on and off again restores them with data intact; a member with the permission but without the area is refused; dependency rules in both directions; only an owner can change areas; audit entry written; backfill leaves every existing tenant's areas on (migration test); the definer returns only enabled tenants; the router-walk test above.
- Web: `lib/areas.test.ts`; nav and tab bar with each area off; route guard; Home with nothing on; Settings page switches; accessibility and plain-words lists.
- e2e: a business with nothing on sees workflows only, turns Stock on, sees it appear, uses it, turns it off, and the data is still there when it is turned back on.

## 9. A service business as the test: an ISP or cloud provider

The point of the switches is that businesses that do not sell goods can use Taskiem cleanly. An internet provider is the test case: it bills subscribers monthly, chases late payers, cuts off and reconnects service, and tells customers about outages. This section records what the code already gives such a business and what is missing. It was checked on 4 November 2026 by reading the workflow schema, the connector manifests, the template library and decision 0013; nothing was run, and no ISP workflow was built.

**What exists today**

- **Steps** (`schemas/wd-v1.schema.json`): `connector`, `code`, `container`, a generic **`http`** step (method, URL, headers, query, body), `branch`, `parallel`, `foreach`, `wait`, `signal`, `approval`, `subflow`, `transform`, `ai`, `form`, `template`, `callback`, `respond`. So calling a provider's own system over HTTPS, looping over customers and asking a person to approve are all there.
- **Triggers**: `webhook`, **`schedule`** (a cron expression and a timezone), `connector_event`, `polling`, `ussd`, `storefront`, **`invoice`** (a run starts when an invoice is paid in full, or on each payment), `link` and `form`. A schedule drives the monthly run; a webhook takes a monitoring tool's outage alert; the invoice trigger can reconnect a customer when payment lands.
- **Invoices connector** (`connectors/invoices/manifest.yaml`): `create`, `send`, `get`, `void`, `list`, `notify_customer`, `customer_link`, `register_account`. Customers pay to their own account number, and payments are matched to the oldest invoice. No action marks an invoice paid; only a counted payment does, which is the right rule for a billing business.
- **Messages and payments**: SMS (Termii, Africa's Talking), WhatsApp, email, Telegram, Slack; Paystack, Flutterwave, Opay, Moniepoint, Interswitch, Lenco and others. KYC and BVN checks (Prembly, Dojah, Youverify).
- **Patterns to copy**: `chase-overdue-invoices` (schedule, foreach, connector and transform steps), `debtor-reminder-sms` and `debtor-reminder-whatsapp`, `invoice-paid-receipt`, `payment-received-tell-owner`, `failed-transfer-alert`.

**What is missing or unverified**

1. **No recurring invoices, and they will be a recipe (decided).** Nothing in the invoices connector, its docs or the templates bills a subscriber every month. The recipe is a schedule trigger, a foreach over a customers table, and `invoices.create` then `send` for each one due. Recurring billing does not become a built-in invoices action; if the recipe shows a gap in the blocks (for example, a dependable "who is due this month" read over a table), only that block is added. The recipe asks in plain words which table holds the customers, how much and how often, so an agent or an owner can install it; an agent can also open and adjust the steps.
2. **A provider's private systems are unreachable.** The egress guard (`engine/egress`) refuses private, loopback and link-local addresses, so a billing panel, RADIUS server or database on a private IP (the usual place for an ISP's subscriber system) cannot be called by the `http` step or the Postgres and MySQL connectors. Decision 0013 designs an SSH tunnel through the customer's bastion, and its header says "Proposed (design; not built)". That date is October 2026, so whether it has been built since has to be checked in the code. Until it is, only systems with a public HTTPS API are reachable.
3. **No cut-off and reconnect pattern.** The pieces exist (schedule or invoice trigger, `branch`, `approval`, `http`), but there is no template that suspends service after a set number of days overdue, with an optional approval, and reconnects on payment.
4. **No outage notice.** Messaging a list of customers exists, but there is no template that takes an alert from a monitoring tool by webhook and texts or messages every affected customer.
5. **Templates and words are retail- and school-flavoured**: `dispatch-orders`, `low-stock-alert`, `daily-sales-summary`, the school templates. A service business finds nothing aimed at it, and `docs/spec/new-business.md` and `no-technical-steps.md` do not mention service businesses.
6. **The `http` step's name and form** for a non-technical owner ("call another system") are unchecked against the plain-words rules.
7. **Not checked:** whether a data table can hold subscriber, plan and expiry fields comfortably, and how a workflow reads "everyone whose plan ends within seven days".

**Work this adds**, as phase 5 below: service-business recipes (monthly invoices to subscribers, renewal reminders before expiry, suspend on overdue with an optional approval and reconnect on payment, an outage notice to customers), a "service business" choice in the signup question, and a check of whether the private-network tunnel exists.

## 10. Risks and open questions

- **Agent access to workflows is the largest new risk.** An agent who can publish a workflow can send messages and move money through the owner's connections. Hence owner approval to publish, time-limited sessions, step-up for anything that touches payments, and the agent's name on every audit entry. Designed in phase 6, not assumed here.

- **Missed doors.** The worst bug is an area that looks off but still takes orders or sends money reminders. Hence group-level middleware, the router-walk test and phase 3 being its own phase.
- **Existing workflows** that use a step from an area the business later turns off. **Decided: warn.** Turning an area off checks the tenant's published workflows (and deployed versions) for steps that belong to the area. If any do, `PUT /v1/settings/areas` answers 409 with the list (workflow name, step, environment) and changes nothing; the web app shows that list in its confirmation ("These workflows use Stock and will stop at that step: ...") and resends with `confirm: true` if the owner still wants it off. After confirming, those steps fail with the plain sentence rather than silently skip. The step-to-area map is built in phase 3, from the connector registry; phase 1 ships the 409 and `confirm` contract with an empty map so the API does not change later.
- **Backfill assumption:** "existing businesses keep everything on" is applied literally, including businesses that never used stock or storefronts. They can turn areas off themselves; Ops could later flag tenants with no data in an area.
- **Business links** (decided): `links` is its own area. Business links let two businesses trade as mirrored records (an invoice becomes the other side's bill, a purchase order their order, price lists are shared, and the two talk in one conversation), which spans money, orders, stock and chat. Tying links to `money` would stop a business that never issues invoices from linking at all, and would take linked chats away when Money is turned off. So `links` gates finding and linking businesses and their conversations; each document type follows its own area, and a link carries a document only where both sides have its area on. Turning `links` off ends nothing: existing links and mirrors stay as they are and come back when it is turned on; while it is off, no new link or message crosses and nothing is mirrored.
- **Embedding and partners:** settled in §5.
- **Naming:** the owner-facing words ("What your business uses", the area names) need a pass against the plain-words rules and, ideally, a Nigerian small-business owner's ear.
