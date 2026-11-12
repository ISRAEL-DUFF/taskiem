# Phone app M6: selling

The plan for M6 of the [phone app](mobile-app.md) (§10, §3.3): what is
built, in which order, and what each part copies from the web. Written 12
November 2026, after M5.

**Done when** (§10): a shop keeps its products, stock and prices from the
app alone for a week.

## What is there already

From M5: the tools panel, each tool's sheet with its list (search, chips,
rows), a row opened in its sheet (`ToolDetail`), Recent, and Your orders.
A row of Products, Stock, Services or Prices, their Add buttons, the Sell
and Handover tiles, and each sheet's **Open …** line say the screens are
coming.

## What M6 builds

| # | Part | Copies (web) | Notes |
| --- | --- | --- | --- |
| F | **The forms' kit**: − n + stepper, money box (₦, typed as "45k"), switch line, category field (pick one of theirs or type a new one), photo strip, the sheet's two-button foot | `ChatToolForms.tsx` `Stepper`, `Money`, `Switch`, `CategoryField`; `kit.css` | Used by every form below |
| P | **A product**: open from Products — price, in stock, reorder level, category, photos, show on the storefront, sizes and colours (each size's stock and price), hide; **Add product**; **Make one product with sizes and colours** from two or more | `ProductForm`, `SizesRow`, `ProductAdd`, `ChatTools.tsx` `GroupForm` | Photos from the gallery or the camera |
| K | **Stock**: open from Stock — stock in (how many came, from whom, cost), taken off or added with a reason; low and reorder level | `StockInForm` | |
| S | **Sell and Handover**: the Sell sheet (search, the bag, sizes, cash or transfer, the sale's tick and diary line) and the shift's Handover (cash counted, what was sold, sent to the owner) | `ChatStaff.tsx` `SellSheet`, `HandoverSheet` | The tiles say they are coming until this part |
| V | **A service** and **Add service**: name, price or "on the day", minutes, show on the storefront | `ServiceForm` | |
| B | **Book with a linked business**: their services, a day and a time, a note; the booking's card | `ChatBook.tsx` `BookSheet` | The **Book** tile in their conversation |
| L | **A price list** from Prices: its items and prices, quantity breaks, who sees it | `PriceListForm` | |
| T | **The Stock page** (Open stock): every item with its stock, low first, today's sales, a sale recorded, an item's every change, stock moved | `StorefrontStock.tsx` (`SalesToday`, `SaleModal`, `ItemModal`, `MoveModal`) | |
| O | **The Bookings page** (Open bookings): upcoming and past, move, cancel; booking hours and days off | `StorefrontBookings.tsx` (`BookingList`, `BookingActions`, `MoveDialog`, `CancelDialog`, `AvailabilityEditor`, `WindowEditor`) | The services table mapping stays on the web: it is set up once, by talking to `@Taskiem` or on a computer |
| R | **The Price lists page** (Open price lists): lists, their items and breaks, who each is shared with, buyers' offers, staff's discount limit | `PriceLists.tsx` (`StorefrontPriceLists`, `ListItemsModal`, `ShareModal`, `LinkPricesModal`, `OffersCard`) | |

Not in M6: Availability and deliveries (M7, with orders and deliveries), a
customer's order (M7), Invoices and Money (M8), Tables (M8).

## Order of the work

F first (every form uses it), then P, K, S (the everyday selling), V, B,
L, then the pages T, O, R. Each part: the web read first; built to the
parity test where the web has a picture of it (new parity screens for a
product's form, Sell, a service, the Stock page, the Bookings page, the
Price lists page); widget tests for what each button sends; large text
and touch targets as every screen; committed on its own. After the last
part, an audit as M5's.
