# Phone app M5: linked businesses

The plan for M5 of the [phone app](mobile-app.md) (§10): what is built, in
which order, and what each part copies from the web. Written 12 November
2026, after M3 and with M4 moved after M8 (it needs the Firebase project).

**Done when** (§10): two businesses find each other and link from the app
alone; and, as far as this milestone goes, a buyer orders from a linked
supplier from the app alone.

## What is there already

From M3: Explore (find a business, its card, Connect, Withdraw, Accept),
link requests on the chats list (Accept, Decline, Block; Request link with
how we trade; Cancel; "Let other businesses find you?"), End link in a
conversation's contact info, and the documents' cards with their steps.

## What M5 builds

| # | Part | Copies (web) | Notes |
| --- | --- | --- | --- |
| K | **The kit's parts the tools need**: the pill search box, chips ("Label · n"), a row (tile, name, sub, pill), the kit's pill, empty and loading (shimmer) states, a sheet's count, sub-line and "Open …" line | `kit.tsx`, `kit.css`, `chat.css` | Shared by every tool sheet and the pages below; the private copies in the chats list and Explore are left as they are |
| P | **The tools panel**: the grid over the composer from the tools button, Recent, "/" alone opening it and "/word" filtering it, who sees which tool (permissions, staff roles, a business's conversation shows its own tiles) | `ChatTools.tsx` `ToolsPanel`, `lib/tools.ts` `toolsFor` | In the Taskiem conversation and in a linked business's conversation |
| S | **Each tool's sheet, its list**: rows, chips, search, badge, Try again, empty; for every tool | `ChatTools.tsx` `ToolSheet`, `lib/tools.ts` | A row's form and the Add buttons come with the tool's milestone (M6 selling, M7 orders and deliveries, M8 money); until then a row says the tool's screens are coming, as the tools button does today |
| L | **Linked businesses**: every link and request with its state and how we trade; Accept, Decline, Block, End link, Withdraw, Take off the list, Unblock; Let other businesses find us; your QR code and link, shared through the phone's share sheet. From the chats' ⋯ menu (**Linked businesses**, **Your business in Explore**) | `Links.tsx` `LinkedBusinessesPage`, `LinkActions`, `CodeCard` | |
| B | **Your business profile**: be found in Explore; the card as others see it; logo; name, tagline, area, about; delivering for other businesses; photos (add, remove, order, caption) | `BusinessProfile.tsx` | Photos from the gallery or the camera |
| O | **Ordering from a linked business**: Their shop (their prices, photos, the bag, review, Place order, Ask for a quote, Make an offer), the head's Order button, Order on a live card, Your orders in their conversation with each order's steps (confirm, not all came, accept a change, all came, cancel), Reorder | `ChatBuyer.tsx` `OrderSheet`, `PurchaseView`; `Conversation.tsx` head Order and business tiles | The supplier's side of an order (accept, decline) is on its card already; its full screens are M7 |

Not in M5: Book with a linked business (bookings, M6), the Statement and a
link's trade terms (M7, with orders), Invoice them (M8), price lists shared
with a link (M6), `/staff` and `/tasks` (with the briefing's tiles, M6).

## Order of the work

K and P first (every later part uses them), then S, L, B, O. Each part:
the web read first, the app built to the parity test where the web has a
picture of it (new parity screens: the panel, a tool's sheet, Linked
businesses, the profile, Their shop), widget tests, large text and touch
targets as for every screen; committed on its own.
