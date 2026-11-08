# Chat: product categories and variants

Status: **phases 1 (categories), 2 (photos), 3 (sizes and colours for the shop
itself) and 4 (sizes on the storefront and in trade) built**; phase 5 is the plan (8 November 2026; photos, Part C and
phase 2, added the same day).
Written with the founder; the live doc is [the shared plan](https://claude.ai/artifact/HA9Y7M1vLM4PprjoyRidaX),
and this file is its copy beside the code. What phase 1 built, and where it differs
from the plan, is in [As built: phase 1](#as-built-phase-1-categories).

## Summary

We give products a category the chat can search and browse, photos taken or picked without leaving the chat, and let one product carry sizes and colours, each with its own stock. They ship in five phases, each usable on its own.

A shoe shop has hundreds of items, and today each size and colour is a separate product: one trainer in 10 sizes and 3 colours is 30 near-identical rows. Finding the right one in the @ picker, the Products sheet or a supplier's price list means scrolling or guessing names.

- **Categories** (Sneakers, Sandals, Bags) let people find things whose names don't say what they are, and browse a kind of thing. No typed path: search reads categories, and chips filter the sheets.
- **Variants** (size 42, black) make the 30 rows one product with a grid. Stock, Sell, orders, price lists and deals pick the exact variant, and low stock is told per variant.
- **Photos** come from the phone's camera, its pictures, or a link, wherever a product's photo is set in the chat, so nobody has to put a picture somewhere else first to get an address for it.

Categories go first because they are small and help every business. Variants touch everything that holds an item, so they come in phases, each one usable on its own.

Nothing here is tied to shoes or fashion: a category is any label the owner types, and a choice is any name with any values. Shoes are the running example because they need both the most. The same build serves:

| Business | Categories | Choices (variants) |
| --- | --- | --- |
| Shoes, clothing | Sneakers, Sandals, Dresses | Size × Colour |
| Drinks, provisions | Soft drinks, Grains | Size: 35cl, 50cl, 1L; 25kg, 50kg bag |
| Phones, electronics | Phones, Chargers | Storage × Colour: 128GB, 256GB × Black, Blue |
| Paint, building materials | Emulsion, Rods | Size × Colour: 4L, 20L × 12 colours; 10mm, 12mm, 16mm |
| Beauty | Hair care, Perfume | Strength: Regular, Super; 50ml, 100ml |
| Furniture | Chairs, Tables | Wood × Cover: Oak, Walnut × Fabric, Leather |
| Packaging | Takeaway packs | Size: Small, Medium, Large |

## What exists today

Every item is one row of the business's catalogue table, and everything else points at it by that row's id ("23"). Nothing exists below the product: no sizes or colours with their own stock.

| Part | What it does today | What it lacks |
| --- | --- | --- |
| Catalogue table (Xenibase, `storefront.Columns`) | Name, price, photos, stock, unit, reorder level, and an optional Category column | The table chat creates has no category column |
| Stock (`stock.Item`, `stock_movements`) | Counts and moves stock per row id; low stock per row (`stock_low`) | No category; nothing per size or colour |
| @ picker (`web/src/lib/mentions.ts`) | Matches names in the browser, word starts first; at most 4 products shown | Doesn't read categories; no browsing |
| Products sheet, order sheet | A search box; All, Low stock and Hidden chips | No category chips or groups |
| Price lists, lowest prices (`price_list_items`, `trade_floors`) | A price, minimum and up to 5 quantity prices per row id | Breaks count one row, so 50 pairs in mixed sizes don't add up |
| Orders between businesses (`link_orders`, `purchase_orders`) | Lines carry the seller's row id and a name; buyers map each supplier item to one of theirs (`item_links`) | Mapping 30 sizes one by one would be unbearable |
| Sales (`/v1/stock/sales`) | A line per row id; stock goes down per row | No size or colour to pick |
| Storefront orders | Lines per row id; food "options" (Large, Plantain) change the price only | Options have no stock of their own and no form to edit them |
| Invoices | Text lines ("Rice, 25kg bag"), no item id | Nothing needed: a variant's name reads right as text |
| WhatsApp, Telegram | "stock rice", "sold 2 …", "add 50 cartons of …" matched by name, with a numbered pick | No category words |

## Part A: categories

One category per product, set in the chat, read by search and shown as chips. No typed path and no tree: a brand goes in the name or description, and search finds it.

1. **Set it in the product form.** A Category field under the name, suggesting the categories already used ("Sneakers · 48"), so spellings don't drift. The chat's catalogue table gets a `category` column the first time one is set; a table mapped by hand that has none says "Add a category column to use categories" with the step to do it.
2. **Search reads it.** The @ picker, the Products sheet and the order sheet match a word against the name, the category and the description, names first. "@nike" finds Nike shoes even when "Nike" is only in the description.
3. **The picker offers the category.** When what is typed matches a category, the picker's first row is a chip, "Sneakers · 48 items". Tapping it opens the Products sheet (or, in a supplier's chat, their price list) filtered to Sneakers. The picker stays a quick pick of 4; the sheet is where people browse.
4. **Chips in the sheets.** The Products sheet and the order sheet get a row of category chips after All, Low stock and Hidden, scrolling sideways on a phone, most-used first. A category with one item gets no chip.
5. **The order sheet groups.** A supplier's list shows under category headings, "Other" last, with the chips jumping to each.
6. **Shared with buyers.** A price list carries each item's category, so a buyer's order sheet groups and filters the same way. The category is the seller's; the buyer's own item keeps its own.
7. **Storefront.** The public page already groups by category; nothing changes there except that categories set in chat now reach it.

Out of scope: sub-categories, several categories per product, and category pictures.

## Part B: variants

A product can have up to three choices (Size, Colour, Material), and each combination is a variant with its own stock, and its own price only when it differs. People pick a variant wherever an exact item matters: selling, stock in, ordering, a storefront cart.

**Setting them up, in the product form:**

1. "Sizes and colours" adds a choice: its name and its values. Any name and any values can be typed. Presets only save typing: shoe sizes EU 36–46, UK 3–12, clothing XS–XXL, kids' ages, S/M/L, common bottle and bag sizes, and the business's own choices from its other products.
2. A grid of every combination appears (Size down, Colour across), each cell its count in stock. A cell left empty is a combination not sold, so no variant is made.
3. Price is the product's for every variant. "Different price" on a row, column or cell sets its own (size 46 at ₦48,000).
4. A colour can take its own photo; the product's photos show for the rest.
5. Limits: 3 choices, 20 values each, 100 variants per product.

**Using them:**

- **Products sheet:** one row per product, "12 sizes and colours · 3 low". Opening it shows the grid with counts; low cells in amber.
- **Stock in and counts:** the grid, typing counts into cells. "Record stock in" from a low-stock card opens the grid on the low cells.
- **Sell:** tap the product, then the variant (size chips, then colour chips; one tap when there is one choice), then the quantity. Sizes with none left are greyed out.
- **@ picker and the live card:** the product, not its 30 variants. The card shows "Sizes in stock: 40 41 43 44" and its Order button opens the grid.
- **Ordering from a price list:** the product with a quantity per cell, "36 pairs in 7 sizes", one line per variant in the order.
- **Storefront:** a size and colour pick before "Add to bag", what is out greyed out.

**Grouping existing items:** a shop that already has "Air Max 90 – 42 Black" as separate products selects them in the Products sheet and taps "Make one product with sizes and colours". Taskiem proposes the choices from the names ("42", "Black") for the owner to confirm. Their stock, history, prices and links stay with each row.

## Part C: product photos from the chat

**Today.** A product's photo can be set from the chat in one place only, and only one way:

| Where | What it does today | What it lacks |
| --- | --- | --- |
| Product form (chat tools) | "+" picks a file from the device (`POST /v1/storefront/catalogue/rows/{row}/photo`) | No camera; no link; no way to remove a photo or choose the main one, though the API has both |
| Add product (chat tools) | Name, price, unit, count, category | No photo at all: a new product gets one only after it is saved and opened again |
| Tables tool, the products table | Every column is a text box, the photo column too | The owner is asked for the web address of a picture already somewhere else |
| WhatsApp, Telegram | A photo with "add product" as its caption | Nothing missing; unchanged |

**One photo picker, everywhere a product's photo is set in the chat.** Add product, the product form, and the Tables tool's photo columns all open the same sheet, with three ways in:

1. **Take a photo:** the device's camera, through the camera sheet the composer already uses (`CameraSheet`): the back camera on a phone, the webcam on a computer. A device with no camera, or one the browser is not allowed to use, says so and offers the other two.
2. **From your phone** (or computer): the device's pictures, several at once.
3. **Paste a link:** the web address of a picture. Taskiem fetches it once and keeps its own copy, so the shop's page never shows a picture from someone else's site that could vanish or change later.

**What happens to a picture:**

- **Made small on the device first.** A camera photo is resized before it is sent (longest side 1600 px, JPEG), so a 12 MB photo is a few hundred KB on a slow connection. Progress is shown; a failed one says so with Try again.
- **Checked like any upload:** JPEG, PNG or WebP, at most the upload limit (`media.MaxImageBytes`), checked and kept as the business's media, as the "+" does today.
- **Shown before it is saved,** with Remove; the first is the main photo, and up to `storefront.MaxGallery` more follow it.
- **In the product form,** each photo has *Make main* and *Remove* (the API's `PUT …/photo/main` and `DELETE …/photo`).

**Add product with photos.** The photos chosen are held in the form until the product exists, then sent to it (the row's id comes back from `POST /v1/stock/items`). If a photo fails, the product is still added and the form says which photo did not go, with Try again.

**The Tables tool.** A column the catalogue maps as its photo or gallery shows the product's pictures and the picker, not a text box; what is saved is what the storefront writes today. Every other column stays as it is.

**A link, on the server.** A new `POST /v1/storefront/catalogue/rows/{row}/photo` body, `{"url": "https://…"}`, beside the file upload:

- fetched through the egress guard (`engine/egress`): https or http only, public addresses only (a redirect to a private one is refused), a short timeout, the size limit enforced while reading;
- the bytes must be a picture (checked by content, not by the address's ending);
- kept as the business's media and set on the row exactly as an upload is; audited the same way, with the link;
- a link that is not a picture answers 400, "That link is not a picture we can use"; one that cannot be reached, "We could not open that link".

**Who may:** whoever may change the storefront (`storefront.manage`), as today.

Out of scope: editing a picture (crop, rotate, filters), a photo per size and colour (phase 3, variants), and photos for services.

## Data model and engine

Each variant is its own catalogue row, pointing at its product's row. Everything keyed by row id (stock movements, low stock, sales, price lists, lowest prices, order lines, storefront carts) keeps working unchanged for a variant, and the product row is what people see and search.

The other way, a list of variants inside one row, would put a second key beside every item id in stock, sales, orders, price lists and floors: months of change and risk across code that works today.

**Columns added to the catalogue table** (made by chat when first needed; mapped in `storefront.Columns` like the rest):

| Column | On | Holds |
| --- | --- | --- |
| `category` | product | "Sneakers" (Part A) |
| `variant_of` | variant | the product's row id |
| `variant` | variant | its choices, `{"Size":"42","Colour":"Black"}` |
| `choices` | product | the choices and their order, `[{"name":"Size","values":["40","41",…]},…]` |

A variant's `name` is written out in full, "Air Max 90 – 42, Black", so text-only places (invoices, diary lines, WhatsApp, old order lines) read right with no change. Its price, unit and reorder level are copied from the product when made and can differ. A product with variants keeps no stock of its own; its count is the sum.

**Engine:**

- `storefront.Item` and `stock.Item` gain `Category`, `VariantOf`, `Variant` and (on products) `Choices` and `Variants`.
- `stock` lists a product with its variants nested; `Low()` is per variant; a new `LowVariants()` groups them under the product.
- A new `variants` package makes, renames and retires the rows for a grid, in one go: a renamed size renames its row; a removed one is hidden (`available` off), never deleted, so history keeps its id.
- Food menu options (price add-ons, `storefront.ItemOption`) stay as they are: a choice that changes the price but not the stock ("Large") is an option; one with its own stock ("size 42") is a variant.

**Existing data:** nothing moves. A product without variants is a row exactly as today. Grouping (Part B) only writes `variant_of`, `variant` and `choices` onto rows that already exist.

## Everywhere an item appears

Most places need nothing new, because a variant is a row. The real work is in trade between businesses, where a list holds the product but an order holds variants.

| Place | With categories | With variants |
| --- | --- | --- |
| Stock, movements, counts | `stock.Item.Category` | Unchanged: per variant row. Product total = sum |
| Low stock and its card | — | Grouped per product: "Air Max 90: sizes 42 and 43 are running low", one card, opening the grid on those cells |
| Sell (`/v1/stock/sales`) | Chips in the sheet | Pick the variant; the line is the variant's row id. Unchanged on the server |
| Price lists (`price_list_items`) | Item carries its category | The product is listed once; its variants share its price unless a variant has its own. Ordering picks variants |
| Quantity prices (breaks) | — | Counted across all variants of the product: 50 pairs in mixed sizes reach the 50 price. `links.PriceAt` takes the product's total |
| Lowest price (`trade_floors`) | — | Set on the product, applies to each variant; a variant may have its own |
| Orders between businesses | — | A line per variant, named in full; the order card says "36 pairs, 7 sizes" and lists them when opened |
| Deals (offers, counters) | — | On the whole order, as today: no change |
| Buyer's item links (`item_links`) | — | Linked product to product; variants match by their choices (42 to 42). A size the buyer lacks is added to their product on receipt |
| Invoices | — | Text lines read "Air Max 90 – 42, Black": no change |
| Storefront (public page, cart) | Already groups by category | Size and colour pick; the cart line is the variant's row id. Unchanged stock path |
| Diary lines | "Category of Air Max 90: Sneakers" | "Stock in: Air Max 90 – 42, Black +6"; a grid saved at once is one line, "Stock in: Air Max 90, 7 sizes, +36" |
| @ picker, live product card | Category chip | The product; the card shows sizes in stock |
| Briefing, "how did we do" | — | Top sellers per product, not per size |

## WhatsApp, Telegram and Slack

The words people already type keep working, and a variant is found by its full name, so "sold 2 air max 42 black" needs no new rule. Three things are added (`api/whatsapp_stock.go`, `engine/stock/words.go`):

- **A category as a request:** "sneakers", "show me sneakers" or "stock sneakers" lists that category, 10 at a time with "more".
- **A product with variants, answered as a grid in words:** "stock air max 90" answers "Air Max 90, black: 40 × 3, 41 × 0, 42 × 5 … white: …" with the low ones marked.
- **Picking a size when one is missing:** "sold 2 air max 90" asks "Which size? 40, 41, 42 …" then "Which colour? Black or white", one question at a time, the same numbered pick used today. "add 12 air max 90 size 42" works in one go.

The model's request sorter (`api/chat_tool_requests.go`) learns that a category and a size or colour can be part of what is named, and hands back the product and the choices it heard; matching stays in our code.

## Phases

Five phases, each one a PR that ships on its own and leaves nothing half-built. Categories and photos first because they are small and help every business; trade with variants last because it needs the rest. Photos come before variants because a colour's own photo (phase 3) uses the same picker.

1. **Categories.** The `category` column and field; `Category` on stock items, price list items and link-chat items; search over name, category and description; the picker's category chip; chips in the Products and order sheets; grouped order sheet; WhatsApp category requests.
   - Done when: a shop with 300 products finds "all sneakers" in two taps, in its own chat and in a buyer's.
2. **Photos from the chat** (Part C). The photo picker (camera, the device's pictures, a link) with resizing on the device; photos in Add product; Make main and Remove in the product form; the Tables tool's photo columns; the server's photo from a link.
   - Done when: an owner adds a product with a photo from the camera, one from their pictures and one from a link, without leaving the chat, at 390 and 360 px; and a photo set in the Tables tool needs no web address.
3. **Variants for the shop itself.** The columns and the `variants` package; the grid in the product form (choices, presets, counts, own prices, a photo per colour); the Products sheet's grouped rows; stock in and counts by grid; Sell's size and colour pick; low stock grouped per product; diary lines.
   - Done when: a shoe shop sets up one trainer in 10 sizes and 3 colours in under two minutes, sells size 42 black, and is told when 42 runs low.
4. **Variants on the storefront and in trade.** The storefront pick; price lists listing the product once; ordering by grid; quantity prices counted across variants; lowest prices on the product; order lines and cards; buyer links matched by choices.
   - Done when: a retailer orders 36 pairs in 7 sizes from a supplier in one sheet and gets the 50-pair price when the sizes add up to 50.
5. **Chat apps, grouping, and the demo.** WhatsApp and Telegram words; the model's requests; "Make one product with sizes and colours" for existing items; Zuri Atelier (already in the demo world) seeded as a shoe and clothing seller; the recording's new act.
   - Done when: a variant can be sold and counted from WhatsApp, and existing look-alike items are grouped without losing their history.

## Tests and checks

Each phase carries its own tests and passes the repo's checks before it ships.

- **Go:** a photo from a link (a picture; not a picture; too big; a private address, directly and by redirect; unreachable), kept as media and set on the row; the `variants` package (make, rename, retire a grid; limits), stock totals and grouped low stock, `PriceAt` counting across variants, floors per variant, item links matching by choices, the WhatsApp words (including the one-question-at-a-time size pick). Database tests against Postgres like the rest.
- **Browser (e2e):** per phase, at 390 and 360 px wide, in both themes with axe: set a category and filter by it; add a product with a photo from each of the three ways (the camera with Chromium's fake camera), and a photo in the Tables tool; build a grid and sell a variant; order by grid from a price list and reach a quantity price; group existing items.
- **Plain words:** every new owner-facing line through `src/lib/plainwords.test.ts`, with no technical words such as SKU or variant on owners' screens ("sizes and colours", "choices").
- **The recording:** a new act in `video/recording/` for Zuri Atelier: a category chip, the grid, a sale of size 42, an order of 36 pairs. It doubles as a check that the whole flow runs on the real app, with timings.

## Open questions

The plan assumes the answer in brackets; each changes a phase if it goes the other way.

- [ ] Quantity prices across sizes: does 50 pairs in mixed sizes earn the 50-pair price? (Yes, counted per product.)
- [ ] Size labels: one system per product (EU or UK or US), or several shown side by side? (One per product, the owner's choice.)
- [ ] Does a size or colour ever need its own lowest price, or is the product's enough? (The product's, with a per-variant override.)
- [ ] Barcodes or codes per size and colour, for shops that scan: now or later? (Later.)
- [ ] Should a buyer see the supplier's category, or only their own? (The supplier's, in the supplier's price list.)
- [ ] Clothing beyond size and colour (length, fit): is a third choice enough? (Yes: three choices.)
- [ ] Photos: should the picker also offer simple editing (crop to square, rotate) before saving? (Not now: the storefront already shows photos square, cropped to fit.)
- [ ] A photo from a link: should Taskiem keep a note of where it came from, for the owner to see? (Yes, in the audit only.)

## As built: phase 1 (categories)

Migration `00408_item_categories.sql`; `api/stock.go`, `api/whatsapp_stock.go`,
`engine/links/prices.go`; on the web `src/lib/mentions.ts`, `src/lib/tools.ts`,
`ChatToolForms.tsx`, `ChatTools.tsx`, `ChatBuyer.tsx`, `ChatComposer.tsx`.

- **The column.** `POST /v1/stock/items` and `PATCH /v1/stock/items/{id}` take
  `category` (up to 60 characters, spaces tidied; `""` clears it). The first time
  one is set, a text column named `category` already in the table is mapped, or
  one is added (audited as `table.column.add`). Where one cannot be added the
  answer is 409 `no_category_column`, saying what to do: a table Taskiem may not
  change (add the column on the Tables page), or one with as many columns as the
  plan allows (remove one first).
- **The items.** `stock.Item` carries `category` and, when the catalogue maps one,
  `description`, so search can read both. A change of category is a diary line
  ("Category of Air Max 90: Sneakers"; "… is no longer in a category").
- **Price lists.** `price_list_items` and the buyer's `supplier_prices` hold the
  seller's category and description, copied when an item is put on a list; a category changed
  later is written on every list holding the item and mirrored to the buyers
  again (`links.SetItemCategoryTx`).
- **Search.** The @ picker, the Products sheet and the order sheet match the name
  first, then the category and a product's description ("@nike" finds a shoe
  whose description says Nike). A description exists only where the catalogue
  maps one (a catalogue made on the web); a table made from chat has none.
- **The picker.** Words that start a category's name offer it first, under
  "Categories" ("Sneakers · 48 items", two at most). A tap opens Products filtered
  to it, or, in a supplier's conversation, their list filtered to it; the words
  typed for it go. Enter still tags an item (a category is not a tag). In the
  Taskiem chat the categories offered are the business's own (from `/v1/stock`).
  A category is only offered where a tap can open it: a supplier's to someone
  who may order from them, our own to someone who may see our products.
- **Chips.** The Products sheet has a chip per category of two items or more,
  most used first, after All, Low stock and Hidden; the order sheet the same after
  All. The category being shown always has its chip.
- **The order sheet** shows a supplier's list under its categories' headings, in
  the list's order, "Other" last. The plan's chips "jumping to each" heading are
  filters instead, as in Products, so both sheets work the same way.
- **The product form** (add and change) has a Category field under the name,
  with the categories already used offered as chips ("Sneakers · 14").
- **WhatsApp and Telegram.** "sneakers", "show me sneakers" and "stock sneakers"
  list the category's items with their counts and "(low)", 10 at a time, with
  *more* for the rest; anything else lets the list go. A category's name alone is
  only read as one when the catalogue has a category column, and an item called
  the same as a category is still the item.
- **The shop's page** shows the category set in chat (it groups items by it).
- **Tests.** Go: `api/categories_test.go` (the column and no room for it, the
  items and the shop's page, the price list and its mirror with descriptions,
  the words on WhatsApp and Telegram). Web: `src/lib/categories.test.ts`, and the
  chat tool forms, sheets and order sheet in `plainwords.test.ts`;
  `e2e/chat-categories.spec.ts` at 390 and 360 px in both themes with axe,
  including staff who may not order.

## As built: phase 2 (photos)

`api/storefront_photo_link.go` and `storeStorefrontImage` in `api/storefront.go`; on
the web `src/lib/photos.ts`, `PhotoAdd.tsx`, `ProductPhotos.tsx`, `CameraSheet.tsx`
(a `facing` choice) and the product forms and Tables tool in `ChatToolForms.tsx`.

- **A photo from a link.** `POST /v1/storefront/catalogue/rows/{row}/photo` also takes
  `{"url": "…"}`. The picture is fetched once through the egress guard (purpose
  `photo_link`: http or https, never a private address; 15 s; the upload limit enforced
  while reading), decided to be a picture by its bytes, kept as the business's media
  like any upload, and the audit's `storefront.media_add` says where it came from.
  A redirect (a short link, a shared file, a shop's image host) is followed, up to
  three hops, each to its own host and checked again, so a redirect to a private
  address is still refused. One person fetches at most 20 pictures from links a minute
  (429). Answers: "paste a link that starts
  with https://", "that link is a page, not a picture: open the picture itself and copy
  its address", "we could not open that link (it answered 404)", "that link is not a
  picture we can use"; 409 where this deployment fetches nothing.
- **"Add a photo"**, one group of three buttons wherever a product's photo is set in
  the chat: *Take a photo* (the camera app on a phone, the webcam on a computer, the
  back camera where there are two), *From your phone* (or computer; several at once)
  and *Paste a link* (checked before it is held).
- **Made small first.** A photo larger than 1600 px on its longest side, or over
  1.5 MB, is drawn again as a JPEG at 1600 px before it is sent, and only then held to
  the 10 MB upload limit, so a phone's 12 MB photo goes; one the browser cannot draw
  goes as it is if it is within the limit, for the server to check.
- **The same photo again** is no change; one taken out can be added back (each change to
  a row's photos is keyed by its own request, not by the photo).
- **Add product** holds the photos chosen, shown with Remove, and sends them once the
  product exists, the first as its main photo. One that does not go leaves the product
  added and the form open, listing which photo and why, with *Send the photos again*
  (the product is never added twice). While they go, the form says "Sending photo 2 of
  3…".
- **The product form** shows the photos with *Make main* and *Remove*, and the three
  ways to add one, up to the storefront's limit (a main photo and 5 more).
- **The Tables tool**, on the shop's products table, shows the photo column as the
  product's photos and the three ways (a new row's photos sent once the row exists);
  it is no longer a text box, and the row is never written with a photo as text. Its
  list of rows says the photos as a count ("2 photos"), not their references.
- **The diary:** a photo added, taken out or made main is a line in it ("New photo of
  Air Max 90"), as a price or a category is.
- **Who may:** changing photos needs `storefront.manage`, as on the storefront page;
  someone without it is told who adds photos.
- **Tests.** Go: `api/storefront_photo_link_test.go` (a picture, kept and audited; one
  with no Content-Type; a page; a missing picture; not a picture; a private address,
  directly and by redirect; a short link to another host; a link that goes round; a bad
  link; no fetching at all; the same picture again, and one taken out and added back;
  the diary lines; the limit a minute). Web: `src/lib/photos.test.ts`; `e2e/chat-photos.spec.ts`
  at 390 and 360 px in both themes with axe (the three ways with Chromium's fake camera,
  a picture over 10 MB sent small, "Sending photo 2 of 3…", a photo that does not go, Make main and Remove, the Tables
  tool's photo column and its "2 photos" in the list).

## As built: phase 3 (sizes and colours for the shop itself)

`engine/variants`; the mapping and items in `engine/storefront` and `engine/stock`;
`api/stock_variants.go`, `api/chat_alerts.go`; on the web `src/lib/variants.ts`,
`SizesForms.tsx`, the product and stock forms in `ChatToolForms.tsx`, Sell in
`ChatStaff.tsx`, the lists in `src/lib/tools.ts`.

- **Rows.** Each size and colour is a catalogue row of its own: `variant_of` (the
  product's row id) and `variant` (its values as JSON); the product holds `choices`.
  The three text columns, and a true/false `available` column where the catalogue has
  none, are added the first time a product has sizes and colours (409
  `no_variant_columns` where there is no room); the product's `choices` also records
  which of them have a price of their own (`own_price`). A size's name is written out in full
  ("Air Max 90 – 42, Black") so every place that shows only text reads right. Limits:
  3 choices, 20 values each, 100 sizes and colours per product, and the catalogue's 500
  rows in all.
- **Setting them up:** `PUT /v1/stock/items/{id}/variants` takes the choices and the
  grid. `engine/variants` plans it: rows kept (renamed where a value was corrected in
  place, so 40 corrected to 43 keeps its row and history), brought back, made (one
  insert), or retired (hidden, never deleted). Everything asked is checked before
  anything is written, and one save of a product's sizes and colours runs at a time
  (409 for a second). A kept one's count is a count. While the product holds stock of
  its own (the first time), a new one's count is **moved** from it (a movement of its
  own, `moved`: neither received nor lost, so the month's losses and goods in are
  untouched); beyond what the product held it is goods received, and what the product
  held beyond what they took is counted off it as missing. The editor says so as the
  boxes are filled ("Air Max 90 has 12 on the shelf now… 2 not counted into one will be
  recorded as missing"). **A product with sizes and colours keeps no stock of its own and
  is never low itself.**
- **The product leads them.** Each new row takes the product's unit, cost, levels,
  supplier and category, and its price unless it has its own; later changes of the
  product (from its form, or *price* on WhatsApp) reach them: a new name in theirs ("Air
  Max 95 – 42, Black"), its unit, cost, levels, supplier and category, and its price to
  those without one of their own. A size given a price of its own, in the editor or on
  its own, keeps it from then on, even one equal to the product's.
- **Stock in and counts by grid:** `POST /v1/stock/movements/grid` (received or count)
  for several of one product's sizes, told as one diary line ("Stock in: Air Max 90, 7
  sizes and colours, +36"). A count by grid is made in the sizes and colours form (each
  box is the count), which sends the changed ones as counts.
- **Low stock:** one card per product ("Air Max 90 is running low": "42, Black and 42,
  White are low"), carrying their ids; *Record stock in* opens the grid with the low
  ones marked.
- **In the chat tools:** the Products and Stock sheets show the product once ("5 sizes
  and colours · 2 low", its stock theirs together), never each size; its form shows
  *Sizes and colours* with *Set up* or *Change*, and no count of its own. The editor:
  each choice's name and values (typed with commas, or ready-made: shoe sizes EU and
  UK, clothing, small/medium/large, children's ages, bottles, bags, colours), every
  combination on sale at 0 to start, *Same number for all*, a box emptied for one not
  sold, and *Some cost more or less* for their own prices; *Stop selling it in sizes
  and colours* takes them off sale. The grid is groups of boxes that wrap, so nothing
  scrolls sideways at 360 px.
- **WhatsApp and Telegram:** a sale, stock in, a change of stock, a count or a delivery
  that names the product is about its sizes and colours: in a sale or stock change up
  to 9 are offered as a numbered list; more, and those in a count or a delivery, are
  asked for by name ("Air Max 90 comes in 33 sizes and colours. Say which…
  like *Air Max 90 – 42, Black*"; typing "air max 90 42 black" finds it). A size taken
  off sale is offered nowhere and takes no stock in. A full shelf count no longer waits
  for the product itself, which keeps no count.
- **Price lists:** a product sold in sizes heads them on the Price lists page and is
  not itself one to tick; its sizes on sale are put on lists as items of their own. The
  API refuses the product on a list (and a size no longer sold), and setting up sizes
  takes the product off sale on the lists that held it, so a buyer orders a size.
- **Sell:** a product with sizes has *Choose*: a row of buttons per choice, what is off
  sale or has none left greyed out; the size picked gets its own − n +, and those chosen
  are listed under the product. The sale's lines are the sizes' own rows.
- **The public page (until phase 4):** it lists the product, not each size, with its
  stock theirs together, and does not take it in a basket: an order on the product
  itself would take stock it does not keep. Phase 4 lets the customer pick a size.
- **Where it differs from the plan:** a photo per colour is left to phase 4, where the
  public page's size pick needs it; the desk Stock page (outside the chat) still lists
  each size as a row of its own, by its full name.
- **Words:** "sizes and colours" and "choices" on owners' screens; "variant" is in the
  plain-words test's list, so it cannot reach one.
- **Tests.** Go: `engine/variants` (choices, names, keys, the plan), `engine/storefront`
  (the roll-up and the public items), `api/stock_variants_test.go` (set up, a sale, one
  low card, stock in and a count as one line, a rename and a retirement, prices
  following the product, the public page, taking them off sale);
  `api/stock_variants_fixes_test.go` (the product's 12 moved into two sizes with 2
  counted missing and nothing received again, the `available` column mapped, the
  product off its price list and refused on it, a rename and levels and price
  following, a size's own price kept, WhatsApp asking which size and selling one,
  nothing written for a count out of bounds, a size off sale refused stock in). Web:
  `src/lib/variants.test.ts`; `e2e/chat-sizes.spec.ts` at 390 and 360 px in both themes
  with axe (setting up 11 sizes × 3 colours with one not sold and one dearer; one row in
  Products; Sell picking 42 then black with white greyed out; the low card's grid; the
  shelf shared out as the boxes fill; own prices as recorded; a price list heading the
  sizes with their product).

## As built: phase 4 (sizes on the storefront and in trade)

`engine/storefront` (`Size`, `WithSizes`, `CopySizeCount`), `engine/publicweb/sizes.go`
and the shared `choosesize` template, `engine/links` (`PriceSize`, `Flat`, `ProductOf`,
`SizeParentsTx`, `SetListSizesTx`), migration `00410_price_list_sizes.sql`,
`api/trade_sizes.go`; on the web `src/lib/buyer.ts`, `ChatBuyer.tsx` (`SizesQty`),
`PriceLists.tsx`, `ChatLiveCard.tsx`, `StorefrontStock.tsx`.

- **The public page.** The product is listed once and its page has a size and colour
  pick (radios per choice, in every theme; every theme's list sends the product to its
  page): a value with none left in any size is struck through; one that does not go
  with another pick stays pickable and clears that pick. The price, the most that can
  be ordered and the line (`q.<size>`, so adding to a bag that holds something keeps
  it) follow the size picked, and a colour's photo takes the main place (the product's
  for a colour without one). The sizes reach the script as JSON (`data-sizes`). Without
  the script the pick still works: the server turns the product's pick and quantity
  into the size's own line once (`takeSizePicks`) and prices it; a pick that is not
  sold refuses the order in words, never leaving the line out. The public copy carries
  the sizes inside the product (catalogue and front lists alike), and a sale updates
  the size's count there, on the catalogue's table only (`CopySizeCount`). The product
  is sold out only when every size on sale keeps a count and has none left. Sizes'
  own prices and the choices' values are checked with the product before publishing.
  Page budgets and the CSP are unchanged.
- **A photo per colour:** *Photo for Black* in the sizes and colours form
  (`POST`/`DELETE /v1/stock/items/{id}/variants/photo?choice=Colour&value=Black`) sets
  it on every size of that colour, those off sale too, in one write; a choice or value
  the product does not have is refused. The form offers only colours some size has,
  and shows the photos from the table (`photo` on a size in `GET /v1/stock`).
- **Price lists list the product once.** A product with sizes is ticked as one row;
  its sizes on sale go with it (`sizes` on the list's row: the choices and each size,
  at the product's price unless it is given its own under *Prices by size*). The lists
  that hold the product keep its sizes on sale as the catalogue has them after every
  catalogue sync, however a size changed (its form, WhatsApp, the Tables tool, a
  front's show and hide); a list already right is left alone and its buyers are not
  told again. A size's own price and its being off one list stay through those
  refreshes. With no size on sale the list still says the product comes in sizes, and
  it cannot be ordered as an item of its own. A size given alone is refused (400).
- **Ordering by grid.** The buyer's order sheet has *Choose sizes* on such a product: a
  box per size, "36 pairs, 7 sizes" as they fill. Quantity prices and the minimum
  count the product's sizes together, so 36 pairs are at the list price and 50 at the
  50-pair price; each size is a line of its own in the order. Everything is checked
  before anything is written (`checkPriceLines`): an order that cannot be placed adds no
  size to the buyer's product and links nothing. When the seller matches a line by hand,
  every line of that product is priced again at the order's total, so the break reaches
  them all. Order words, the trade orders page, the buyer's orders and the live card
  group sizes under their product ("Air Max 90: 36 pairs, 7 sizes"; the card's "Sizes
  on sale: 40 41 43…"), by each size's product, never by names that merely start alike
  ("Indomie – Chicken" and "Indomie – Onion" stay apart).
- **Other ways of ordering.** *Order again* gives each size its own box again. A product
  whose sizes run low gets one card with a reorder of the supplier's sizes of the same
  values, at least the supplier's minimum together, opening the grid filled in. The
  chat tools' low list does the same per supplier, and their order action takes a size
  by its full name ("Air Max 90 – 42, Black"), adding it to the buyer's product as the
  order sheet does. Naming the product itself, on WhatsApp or to the chat tools, is
  refused in words: it is ordered by its sizes in the chat on Taskiem.
- **Lowest prices** are the product's: an offer is checked against the product's
  lowest price for each of its sizes, each size's product read from the catalogue
  (so a size no list holds now is still held to it).
- **The buyer's own items.** The buyer's item linked to the supplier's product is
  matched to sizes by their choices; a size the buyer does not have yet is added to
  their product when the order is placed (with no stock), and linked; the buyer's
  sizes with prices of their own keep them. The grid is built under the product's lock
  from the catalogue as it is then, so two orders adding different sizes at once both
  keep theirs. A line naming one of the buyer's sizes must be the same size as the
  supplier's (their 42 never into the buyer's 44), and all the lines of one product
  name one product of the buyer's. Where the
  buyer's product has different choices the order is refused (409 `sizes_differ`); a
  buyer's item with no sizes linked to a sized product must be set up first (409
  `sizes_first`).
- **The desk Stock page** groups each product's sizes after it; the briefing and the
  handover count low stock by product as the cards do.
- **Where it differs from the plan:** missing sizes are added when the order is placed,
  not when it is received; the desk supplier prices page sends a sized product to the
  chat to order it by grid; ordering sizes from WhatsApp and Telegram is phase 5.
- **Tests.** Go: `engine/publicweb/sizes_test.go`, `api/storefront_sizes_test.go` (the
  pick with and without the script, a sold-out size refused, the colour photo),
  `engine/links/deals_test.go` (`TestFlatSizes`, floors by product),
  `api/trade_sizes_test.go` (36 pairs in 7 sizes, the 50-pair price, links made and
  matched, floors, `sizes_first` and `sizes_differ`); after the review,
  `api/trade_sizes_fixes_test.go` (a refused order adds and links nothing, own size
  prices kept, the same size only, a size off sale leaving the list and the buyer's
  copy, none on sale not orderable), `api/trade_sizes_internal_test.go` (order words,
  the grid kept, low stock by product, the low sizes' reorder and top-up),
  `engine/links/deals_test.go` (`TestRepriceTogether`, `TestFlatNoSizeOnSale`,
  `TestSameSizes`), `engine/publicweb/sizes_test.go` and `api/storefront_sizes_test.go`
  (a bad pick refusing a mixed bag, the home page's catalogue, a hidden or sold-out
  product, the colour photo's refusals), `api/moderation_sizes_internal_test.go`. Web:
  `src/lib/buyer.test.ts`,
  `src/lib/purchases.test.ts`; `e2e/trade-sizes.spec.ts` at 390 and 360 px with axe
  (the grid, the minimum, ₦1,456,000 for 36 pairs and ₦1,832,000 for 50, review and
  place) and `e2e/chat-sizes.spec.ts` (a price list ticking the product with a size's
  own price).
