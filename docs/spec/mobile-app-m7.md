# Phone app M7: orders and deliveries

The plan for M7 of the [phone app](mobile-app.md) (§10, §3.3): what is
built, in which order, and what each part copies from the web. Written 12
November 2026, after M6 and its audit.

**Done when** (§10): an order goes from a buyer's message to delivered and
received with both sides in the app.

## What is there already

From M5 and M6: the tools panel and each tool's sheet with its list; the
Orders tool lists orders from linked businesses, but a row opened says it is
coming; ordering from a linked business (the Order sheet, M5) and a
purchase's view (Your orders); a deal's offers on its card; the Stock,
Bookings and Price lists screens. Availability's tile, a trade order's card
**Open**, a customer's **Start an order**, and the briefing's Deliveries and
Orders tiles say they are coming.

## What M7 builds

| # | Part | Copies (web) | Notes |
| --- | --- | --- | --- |
| A | **Availability**: free, busy till a time, off; how many businesses see it; the deliveries held | `ChatAvailability.tsx` `AvailabilitySheet` | A rider's business; the tile's sheet |
| V | **An order in the Orders sheet**: its lines, progress, Accept and Decline; **a delivery** for a rider: pick up, deliver to, the customer, the fee, Accept / Can't, Picked up, Arrived, Delivered with a photo when proof is asked | `ChatToolForms.tsx` `OrderView`, `TradeOrderView`, `DeliveryView` | |
| T | **The order screen** (`/trade-orders/{id}`) and **Open orders**: the order's lines, what they counted, the seller's steps (accept, decline, propose a change, can't fulfil, ready, on the way, delivered), raise its invoice, its credit and days to pay; the list | `TradeOrders.tsx` `TradeOrdersPage`, `TradeOrderPage`, `SellerActions`, `TradeSettings`, `OwnItems` | A card's **Open** leads here |
| C | **Orders from a customer's messages**: in a customer's conversation, the orders read from what they wrote, changed, confirmed and sent; **Start an order** | `CustomerOrders.tsx` | |
| S | **Orders from customers** (`/storefront/orders`): the list and an order: items, payment, fulfilment, decide it | `StorefrontOrders.tsx` | |
| D | **Deliveries** (`/storefront/deliveries`): deliveries and their steps, a rider chosen, the riders | `Dispatch.tsx` `StorefrontDeliveries`, `OrderDelivery`, `ChooseRider`; `StorefrontRiders.tsx` | Paying riders is money (M8) |
| K | **Stock counts** on the Stock screen: a count, the difference and why | `StockCounts.tsx` | Left from M6 |

Not in M7: invoices, bills, money and paying riders (M8); purchases'
own screen beyond Your orders (M8).

## Order of the work

A and V first (small, in the sheets), then T (the order screen every card
leads to), C and S (customers' orders), D (deliveries), K. Each part: the
web read first; widget tests for what each button sends; large text and
touch targets as every screen; committed on its own. After the last part,
an audit as M5's and M6's.

## Where it stands

| Part | Done |
|---|---|
