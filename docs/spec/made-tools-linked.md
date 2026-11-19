# Made tools for linked businesses

Status: **plan, for later** (written 19 November 2026 with the founder). Nothing here is built, and it is **not to be started** until members' tools (M1 to M4) and customers' tools (M5) of [the build plan](build-plan-saved-tools-payments.md) are in use. The founder chose to write it now and build it later ([made tools](made-tools.md) §14, 5). It is phase M7 there. §14 lists what to decide when it is taken up.

Builds on: [made tools](made-tools.md) (the `tool` block, the answer, care), [business chat tools](business-chat-tools.md) (the panel in a business's conversation, built), links between businesses (`engine/links`), and customers' tools (M5), which meet the same questions in a simpler form.

## 1. Summary

Two linked businesses share a conversation. Today that conversation has what Taskiem built for it: the other's shop, orders, invoices and bills, the statement, deals.

A made tool **for linked businesses** lets a business add its own. Bayo Rice Mills makes "Check stock", "Ask for a quote" or "Book a delivery slot" from a workflow of its own. Ada's Kitchen, linked to Bayo as a customer, sees them under **Their tools** in the conversation with Bayo, fills a short form, says **yes**, and gets the answer there.

The run is Bayo's: it runs with Bayo's connections and counts against Bayo's limits. Ada's staff start it and see only what Bayo marked to be shown. That is why this audience is the hardest, and why it comes last.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| The offering business | The one whose workflow it is, and whose run it is (Bayo) |
| The calling business | The linked business whose staff use the tool (Ada) |
| Caller | The person in the calling business who used it |
| Their tools | The group in a business conversation's panel where the other side's made tools show |
| Public output | An output of the workflow marked `public: true`: the only thing an answer for outsiders may show (made tools §11) |

## 3. What exists, and what is missing

| Today | Where | What this needs |
| --- | --- | --- |
| A link between two businesses, with a role each way (supplier, customer, both) and states (active, ended, blocked) | `engine/links/links.go` | Tools show only on an **active** link, to the role they are offered to |
| The panel in a business's conversation: their shop, Order, Book, Orders, Invoices and bills, Statement, and ours about them | [business chat tools](business-chat-tools.md) §3 (built; web and app) | A **Their tools** group, after Taskiem's own |
| Each side's key for the other (`link:<business>`), used on orders, invoices and the customer record | `engine/links`, `api/links.go` | The run's starter, and the trusted "who is asking" a workflow reads (§6) |
| The `tool` block with `for: "linked"` named and reserved | [made tools](made-tools.md) §5, §11 | Its rules, here |
| Customers' tools: an outsider, a form, always **yes**, public outputs only | Made tools §11 (M5) | The same rules, for a business in place of a person |
| Nothing lets one business start a run in another | | The whole of this plan |

## 4. What a linked tool is

A workflow started by hand, with a `tool` block whose `for` includes `"linked"`. Everything a member's tool has, it has: a command, a name, a line about it, a form from its inputs, an answer.

What differs, because the caller is outside the business:

1. **Inputs by form only.** No model reads another business's words into the offering business's workflow. The caller picks the tool and fills the form.
2. **Always yes.** A summary of what will be sent, then **yes**, whatever the tool's care. A run costs the offering business each time.
3. **Public outputs only** in the answer. The validator refuses a linked tool whose answer reads anything else (made tools §12).
4. **Never money out.** A tool with a step that moves money out of the business is refused for linked businesses (made tools §12, from the review of 19 November 2026).
5. **Who is asking is given, not typed** (§6).

## 5. Who it is offered to, and who may use it

**The offering business chooses, when it publishes:**

- **All linked customers**, **all linked suppliers**, or **both**; or
- **Named businesses** only, picked from its links, as a price list is shared today.

Proposed default: linked customers. A supplier offering "Check stock" to those who buy from it is the common case.

**In the calling business,** a person sees and may use a tool when they could already act towards that business: those who may order see ordering-like tools. Proposed: one permission, **Use their tools** (`link.tools`), given to owners, admins and whoever holds `link.manage`, and grantable to a role. The offering business cannot see or set the calling business's roles; it sees only the business.

**Turning it off for one business.** On a link's page, the offering business may switch its tools off for that business without ending the link (someone using them too much, a dispute).

## 6. The run

- **Whose:** the offering business's. It appears in its runs, with "Started by Ada's Kitchen (Chioma)" and the conversation it came from. It reads with the offering business's connections, never the caller's.
- **The starter:** `link:<business>`, the offering business's own key for the calling one, with no roles in the offering business.
- **Who is asking:** the workflow may read `caller.business` (that key, the same one on the linked customer's orders and invoices), `caller.name` (the business's name) and `caller.person` (the caller's name, as chat messages show it). Taskiem fills these; the form cannot set them. So "What do I owe?" answers for the business that asked and no other.
- **The validator warns** when a linked tool reads a table or a record with no step that uses `caller.business`: a tool that looks up "the customer" from a typed name would let one business ask about another.
- **Limits:** it counts against the offering business's runs for the day and month, as any run (made tools §14, 9). Each calling business has its own limit on top (proposed: 30 a day for a business, 3 at once), so one cannot use up another's month.
- **A run that waits** (an approval in the offering business, a question to its staff) says so in the conversation ("Bayo Rice Mills is checking. You'll get the answer here.") and answers when it finishes.
- **A question inside a run** (`to: "starter"`, made tools §9) may ask the caller in the conversation, by form.

## 7. Where it shows, and what both sides see

- **The panel** in the business's conversation gains **Their tools**, after Taskiem's own tools for that business, each a tile with its name and line. An empty group is not shown.
- **`/`** in that conversation lists them after Taskiem's own (`/order`, `/invoice`); a made tool can never take one of those words (made tools §12).
- **The call and its answer are part of the conversation**, as orders are: both sides see a card, folded to a row ("Ada's Kitchen · Check stock · Long grain rice 50kg"), that opens to the form's values and the answer. A conversation between two businesses has no private side.
- **Buttons on an answer** (made tools §10) may start another of the offering business's linked tools, or one of Taskiem's own actions between the two (Order this, Ask for a quote).
- **Web and app first.** The business-scoped panel is the web's and the app's. On WhatsApp, Telegram and Slack, "their tools" comes later (§13, L3), by a numbered pick and a form link.

## 8. What the offering business sees and controls

- **Publishing** says in plain words who will be able to use it and what it gives back: "Businesses that buy from you (14) will be able to use **Check stock**. They will see: item, in stock, next delivery." A change to who it is for, or to a public output, is called out to the approver where the business has four-eyes.
- **A list of use** on the tool's page: which business, when, how it ended. Not the caller's other activity.
- **Off for one business**, and **off for all** (unpublish), at once.

## 9. When the link changes

| What happens | What follows |
| --- | --- |
| The link ends, or either side blocks | The tools are gone from the panel at once. A run already going finishes in the offering business; its answer is kept there and **not** sent. Cards already in the conversation stay, as records |
| The role changes (customer to supplier) | The tools offered to the new role show; the others go |
| The tool is unpublished or changed | The panel follows the deployed version, as for members. A button on an old answer to a tool no longer there says "This is not available" |
| The calling person loses **Use their tools** | They no longer see the group; a press on an old button is refused |

## 10. Starter tools (recipes)

For the builder, beside the eight members' recipes:

| Tool | Asks | Answers |
| --- | --- | --- |
| Check stock | An item | In stock or not, how many where the storefront shows it, the next delivery |
| Ask for a quote | Items and amounts, by when | "Sent. You'll get the quote here", then the quote as a deal |
| Price for an amount | An item, how many | The caller's price at that amount |
| Book a delivery slot | An order, a day | The slot, or the nearest free ones |
| Report a problem | An order, what went wrong, a photo | A reference, and who will answer |
| What do I owe? | Nothing | The caller's balance and oldest unpaid bill, from `caller.business` |

## 11. Storage and API

- **`workflow_tools`** (made tools §18) already holds the audience. For `linked` it gains: the roles it is offered to, and whether it is for named businesses only.
- **`workflow_tool_links`**: for named businesses, a row for each link a tool is offered on; and a row for each link where the offering business turned its tools off.
- **Reading across:** the calling business sees the other's linked tools through the link mirror, as it sees the other's shop: name, line, icon, the form's fields, never the workflow.
- **API (calling side):** `GET /v1/links/{id}/tools`; `POST /v1/links/{id}/tools/{command}/start` with the form's values and the caller's **yes**; the answer arrives as a card event in the conversation.
- **API (offering side):** the tool's audience on the workflow's publish; `PUT /v1/links/{id}/tools-off`; the tool's use.
- **Row-level security:** the run, its inputs and its history are the offering business's alone. The calling business holds only the card (the form's values it sent and the public answer).

## 12. Security and abuse

| Risk | What stops it |
| --- | --- |
| One business asking about another | `caller.business` is filled by Taskiem; the form cannot set it; the validator warns where a linked tool reads records without it (§6) |
| An answer showing more than was meant | Public outputs only, refused at validation otherwise; publishing shows exactly what will be shown, to whom (§8) |
| A calling business using up the offering one's runs | A limit for each calling business, on top of the offering business's own; off for one business (§5, §6) |
| Another business's words steering a workflow or a model | Inputs by form only; no model reads them; an `ai` step in a linked tool gets them as data, redacted, as customers' tools do |
| Money moved by an outsider | A step that moves money out is refused for linked tools (made tools §12). Paying the other business stays with bills and F1 |
| A forged or forwarded button | Signed, with a nonce; a press acts as the presser, in their own business (made tools §10) |
| Using a tool after the link ended | Checked when the run is started and again when the answer is sent (§9) |
| The offering business learning about the caller's staff | It sees the business and the caller's name as on a chat message, nothing else |

## 13. Phases, when it is taken up

| Phase | What | Done when |
| --- | --- | --- |
| L1 | `for: "linked"` in the validator and the publish screen; the audience by role; **Their tools** in the panel and on `/` (web and app); the form, **yes**, the `text`, `card` and `list` answers as a card both sides see; `caller`; limits; off for one business; the link's changes (§9) | Ada's staff use Bayo's "Check stock" from the conversation with Bayo and both sides see the card; a business not offered it does not see it; ending the link removes it |
| L2 | Named businesses; buttons from an answer to Taskiem's own actions (Order this); a question inside a run; the recipes | "Ask for a quote" returns a quote Ada can accept as a deal |
| L3 | WhatsApp, Telegram and Slack: "their tools" by a numbered pick and a form link | A buyer on WhatsApp checks a supplier's stock |

## 14. To decide when this is taken up

| # | Question | Proposed |
| --- | --- | --- |
| 1 | Does the calling business see the call and answer only, or do both sides? | **Both**, as a card in the conversation (§7) |
| 2 | Who in the calling business may use them? | A permission, **Use their tools**, with owners, admins and `link.manage` holding it (§5) |
| 3 | Offered to roles, to named businesses, or both? | **Both**; by role in L1, named in L2 |
| 4 | The limit for each calling business | 30 a day and 3 at once, set after M5's counts are seen |
| 5 | May a linked tool ask the caller a question mid-run? | **Yes**, by form (L2) |
| 6 | May a business charge for a tool's use? | **No**, not in this plan |
| 7 | What M5 taught: what customers' tools got wrong or right about outsiders, public outputs and limits | Read before L1 starts, and this plan changed to match |

## 15. Tests, when built

- **Server:** a business not offered a tool cannot list or start it; `caller.business` cannot be set by the form; a non-public output never reaches the calling tenant (isolation test); limits for each calling business; an ended or blocked link refuses a start and holds back an answer; a money step refused at validation; off for one business.
- **Web end-to-end and app:** Their tools in the panel; the form, **yes**, the card on both sides; a tool gone when the link ends; 360 px, both themes, axe.
