# Storefronts

A storefront is a business's one-page public page: who it is, what it sells, and how to reach it ([spec](spec/storefronts.md), decision 0036). This is Phase 5, S2 and S3: the page, its catalogue, and ordering, questions and call-backs. Booking comes with S4; setting up from chat and with field agents with S5; the business's own domain and embedding with S6.

It is built and off by default: pages are served only by the [public host](public-host.md), and the web app's **Storefront** page needs `storefront.manage` (owners and admins).

## The page

| Part | What it is |
| --- | --- |
| Business card | Name, tagline, about, logo, area and address, a link to directions (never an embedded map), a **Call** button, a **Chat on WhatsApp** button, opening hours with "Open now" or "Closed now" in Lagos time |
| Catalogue | Items from one of the organisation's [data tables](data-tables.md), grouped by a category column: name, price in naira, description, photo, and "Unavailable" when its available column says so |
| Template | **Classic**, a list that suits a menu or a price list; **bold**, a photo grid that suits a shop |
| Accent | One of eight colours, each readable as text on white and as a button |

Pages are rendered on the server and work without JavaScript. They send no cookies and allow no inline code. Each carries a short safety line. Shared links show the name, tagline and logo (Open Graph), so a link sent on WhatsApp shows a card.

## Editing and publishing

1. **Draft.** The owner edits the card, picks a template and accent, and chooses a **slug**: 3 to 30 letters, digits and hyphens, the page's address `<slug>.<preview domain>`.
   - Reserved words and protected names (banks, payment providers, agencies, Taskiem, and look-alikes of them) are refused ([public host](public-host.md#building-blocks-for-the-next-milestones)).
   - Saving a draft changes nothing anyone sees.
2. **Catalogue.** The owner picks a data table and maps its columns:

   | Mapping | Column type |
   | --- | --- |
   | name (required) | text |
   | price | integer or number, in naira or kobo |
   | description, category | text |
   | photo | text: a photo's id, set by **Add photo** |
   | available | boolean |
   | sort | integer or number |

   Personal columns (phone, email, or any marked personal) can never be mapped: a storefront never shows personal data.
3. **Publish.** Taskiem refreshes the catalogue copy, checks the card, and writes it as the next **version**, an immutable snapshot.
   - **Refused at publishing:** a protected name, links or email addresses in the text, phone numbers that are not Nigerian mobiles, a rejected logo.
   - Publishing claims the preview address. A new slug moves it, and the old address stops serving.
4. **Close for now.** The page shows the owner's message (up to 200 characters, no links) and its card instead of the catalogue, until **Reopen**.

Every step is audited: `storefront.save`, `storefront.publish`, `storefront.close`, `storefront.reopen` and `storefront.media_add`.

## The catalogue copy

Page views never reach Xenibase. Taskiem keeps a copy of the mapped columns of each published storefront's table, and pages read the copy.

**When it refreshes:**
- when the storefront is published;
- when the owner presses **Refresh**;
- every 15 minutes for every published storefront, which catches rows changed in Xenibase, in workflows or from chat.

If the table cannot be read, the old copy stays, the page keeps showing it, and the error is shown to the owner.

### Stock

Mapping an integer **stock** column tracks how many of each item are left:
- the page shows an item with none left as **sold out**, and the order form refuses it;
- the form offers no more of an item than is left, and refuses an order for more;
- **accepting** an order lowers the stock in the table (never below zero), and the copy at once.

The table is changed only while it still holds the count that was read, so two orders accepted at the same moment both count; each change carries an Idempotency-Key and is recorded on the order before it is sent, so a retried step changes the stock once. An order accepted with too little left lowers it to zero, and its note says which items were short (`decide` returns them as `short`). Declining or cancelling puts nothing back: change the table if the items return to the shelf. Stock is lowered only on Accept, so an order paid when ordering holds none while it waits. A catalogue holds at most 500 items until the storefront plans set their own (S8).

## Photos

Logos and product photos are stored in the organisation's Xenibase storage ([media](media.md)):
- checked by their contents;
- turned upright;
- stripped of their metadata (GPS location, camera);
- stored at 2000, 800 and 320 pixels wide.

Pages load the size the screen needs, lazily.

**Moderation.** Each photo is recorded with a moderation state: `unreviewed`, `approved` or `rejected`.
- A rejected photo is never shown, and uploading it again does not clear it.
- The moderation pipeline that sets these states is S8's (storefronts 7.4). Until it exists, photos are recorded as unreviewed and shown.

**Add photo** on a catalogue item stores the photo and writes its id into the item's row.

## Orders, questions and call-backs

The page offers up to four **forms**: **order** (the catalogue as a basket), **enquiry** (a question), **call-back** (a request to be called) and **booking** (a time for a service, [below](#bookings)). Each starts a workflow.

### Setting up

Under Storefront > Forms, **Set up** adds the form's default workflow from the template library (`storefront-order`, `storefront-enquiry`, `storefront-callback`) and publishes it. A form can be switched on only once a workflow with a storefront trigger for it (`{"type": "storefront", "config": {"action": "order"}}`) is deployed to prod, and only one workflow per form and environment. The owner can edit the workflow like any other.

**Order settings:**
- **Payment:**
  - after the owner accepts (the default);
  - when ordering;
  - no online payment.
- **Fulfilment:** pickup and/or delivery, with a note.
- **Paystack connection:** which of the organisation's connections takes payments (default `main`).

Saving a form re-publishes a published page, so its buttons match.

### How an order travels

1. **The customer sends the form.**
   - The public host checks it: a signed one-time token, the honeypot, the time taken, the per-address limit, a Nigerian phone number, no card numbers.
   - It prices the basket from the **catalogue copy**, never from the form, and refuses more than the stock left ([stock](#stock)). The prices are checked against the table again before the workflow starts ([the price check](#the-price-check)).
   - It writes the submission, **encrypted**, to an inbox, and shows the customer a status page.

   The public host holds no key to the vault, so it cannot seal personal data itself.
2. **Taskiem records it.** Within a second, the `api` role takes it from the inbox:
   - seals the customer's details under their personal-data subject (erasing the customer erases them);
   - records the order with a reference such as `AK-7QX4KD`;
   - deletes the inbox row.
3. **Paid when ordering:** a Paystack checkout page is made at once. The customer's status page takes them to it, and Paystack sends them back.
   - Taskiem asks Paystack about waiting payments every 30 seconds, so no webhook needs setting up in the organisation's Paystack account.
   - Once paid, the workflow starts. An order not paid within 30 minutes expires and starts nothing.
4. **The workflow runs.** The default order workflow:
   - **Asks the owner to accept or decline**, in WhatsApp, Telegram or Approvals, showing the items, the total and how it is collected. No answer within a day declines it.
   - **Declined:** the customer is texted. An order already paid is left **refund due**, and the owner is asked whether to refund it now ([refunds](#refunds)).
   - **Accepted and paid on ordering:** the customer is texted that it is confirmed.
   - **Accepted and paid after acceptance:**
     - a payment link is made and texted;
     - the workflow waits for the payment;
     - paid, the customer is told; not paid within a day, the order expires and the customer is told.
   - Then it **asks the owner to mark it delivered or collected**, which completes the order.

   The enquiry and call-back workflows acknowledge the customer by text, and ask the owner to mark them answered or called back.

**Messages to customers** go by SMS from the business's name, and by email when they gave one ([messages](messages.md)). The workflow names the order; the recipient is always the order's customer.

### Workflow actions

The **storefront** platform connector (`storefront@1`, no connection needed) is what a workflow uses on orders.

| Action | Does |
| --- | --- |
| `decide` | `accept`, `decline`, `complete` or `cancel` the order (`reference`, optional `note`). Accepting lowers the [stock](#stock), returning the items that were `short`. A decision the order's status does not allow fails the step |
| `payment_link` | The checkout page for an accepted order paid after acceptance, made once. The run can then wait for the `storefront.payment` signal, correlated by the reference, whose payload's `status` is `paid` or `expired` |
| `notify_customer` | An SMS, and an email when there is one, to the order's customer (`reference`, `text`, optional `subject`). `{link}` in the text becomes the customer's status link, where they confirm new prices or move and cancel a booking; no run sees the link itself |
| `get_booking` | A booking's status, service and time (`when`, as customers read it), when its reminders fall (`remind_day_at`, `remind_hours_at`, and whether each is still to come) and its payment page |
| `refund` | Refunds a refund-due order or booking in full through Paystack and tells the customer; put an approval before it ([refunds](#refunds)). A refusal fails the step and leaves it refund due |

`decide` also takes `no_show` for a booking the customer did not come to; declining or cancelling a booking frees its time.

It acts only for the organisation running the workflow, whatever the input says, and only inside a run.

The run's `trigger.body` holds:
- `reference` and `kind`;
- `customer` (`name`, `phone`, `email`, `address`, `message`, `best_time`; sealed where the inputs schema marks them `x-pii`, as the templates do);
- `basket`, `total_kobo`, `total` (₦) and `summary`;
- `fulfilment`;
- `payment` (`mode`, and `status`: `paid`, `due_after_accept` or `none`).

### Orders in the web app

**Storefront > Orders** lists orders, questions and call-backs, newest first, with the customer's details masked. Someone with `pii.reveal` can show them (audited), with links to call or WhatsApp the customer. Accepting and declining happen in the approval request.

**Statuses:**
- `received`, `awaiting_payment`, `paid`, `accepted`;
- `price_changed`: waiting for the customer to confirm new prices ([the price check](#the-price-check));
- `declined`, `refund_due`, `refunded`, `expired`, `cancelled`, `completed`, and `no_show` for a booking.

### Refunds

A paid order or booking that is declined or cancelled is left **refund due**. Taskiem never refunds unasked: the owner confirms, and then Taskiem refunds the payment in full through the business's own Paystack connection, from its Paystack balance.

- **In the default workflows:** after a paid order or booking is declined, the owner is asked "Refund the customer?": approve to refund now, reject to refund it yourself.
- **In chat:** `refund AK-7QX4KD`, confirmed with **yes** (audited; `storefront.manage`). Cancelling a paid booking in chat says so.
- **In the web app:** the order page's **Refund through Paystack**.
- **In a workflow of your own:** `storefront@1` `refund`, after an approval.

The order becomes **refunded** and the customer is told the money reaches them in about 5 to 10 working days. If Paystack refuses (most often, too little balance), the order stays refund due with Paystack's reason; try again later, or refund in Paystack. A second refund of the same payment is never made.

**Bookings cancelled by the customer:** a booking form chooses what a customer cancelling a paid booking gets back: everything (the default), or nothing of the deposit (**Keep the deposit**).

### The price check

Forms price from the catalogue copy, which can be up to a refresh behind the table. So before an order's or booking's workflow starts, Taskiem prices it again from the table itself:
- **Same prices:** it goes ahead.
- **A price moved, or an item went:** it waits as `price_changed`. The customer is texted the new total and a link to confirm or cancel within a day. Confirmed, it goes ahead at the new prices (a payment page is made then when paying on ordering); not confirmed in a day, it expires.
- **Nothing left** (every item, or a booking's service, gone): it is cancelled and the customer told.
- **The table does not answer** (Xenibase paused or unreachable): it is tried again for 10 minutes, then goes ahead at the form's prices with a note on the order, so no order is lost.

## Bookings

For services (a barber, a salon, a tailor's fitting, a tutor), customers book a time instead of ordering. One calendar per business: one chair, or one team booked as a whole.

### Setting up

Under Storefront > Bookings:
- **The services table.** A data table with a name and a **duration** column (minutes, 5 to 480), and optionally a price (a service without one shows "Price on the day"), a description, a photo, availability, a **capacity** (customers at once, default 1: a class may take 10) and a **buffer** (minutes kept free after each booking). It is copied like the catalogue and refreshed with it.
- **When bookings are taken:**
  - booking hours per day, up to three periods (for a break); empty means the opening hours;
  - days off;
  - how far ahead (default 30 days, at most 180) and how soon (default 2 hours from now);
  - the change cut-off: how close to the time a customer may still move or cancel (default 2 hours);
  - **confirm automatically**, or ask the owner each time (the default).
- **The booking form** under Storefront > Forms, with the default workflow `storefront-booking`. **Payment:** when booking (all or a deposit, a percentage of the price), after the owner confirms, or at the appointment.

### How a booking travels

1. **The customer picks** a service, a day and a free time, and gives their name and phone. Free times are the booking hours cut into the service's length plus its buffer, less what is taken; a class shares a time until its capacity is reached.
2. **The time is held** the moment they send the form, in the same database transaction that records it, so two customers can never take the same time. The hold lasts 10 minutes while the booking is recorded, until payment when paying on booking (30 minutes), and then for as long as the booking lives.
3. **The workflow runs.** The default one:
   - asks the owner to accept or decline in WhatsApp, Telegram or Approvals (or confirms at once); no answer within a day declines it, and a booking still unanswered when its time comes expires;
   - texts the customer that it is booked, with their link to move or cancel it, and a payment link when they pay after the owner confirms (not paid within a day, the booking is cancelled and the time freed);
   - reminds them a day before and two hours before (wait steps in the run, so they survive restarts);
   - after the appointment, asks the owner whether they came: approve for attended, reject for a no-show.
4. **The owner** gets the day's bookings at 07:00 (Lagos) in their alert channels.

### Changes

- **The customer**, from the link in their messages, up to the cut-off: **move** it to another free time, or **cancel** it. The owner is told in their alert channels.
- **The owner**, in chat or in the web app: `bookings` or `today` to see them; `booking AD-7QX4KD cancel`; `move AD-7QX4KD to Sat 11:00` (to any free time, even outside the booking hours); `off 25 Dec` for a day off. Each is shown and confirmed with **yes**, needs `storefront.manage`, and is audited. The customer is told.

A **moved** booking's run is ended and a new one started with the new time (`trigger.body.event` is `rescheduled`; one already confirmed is not asked about again), so its reminders follow the booking. A **cancelled** booking's run is ended. A paid booking cancelled by either side is left **refund due** for the owner to [refund](#refunds), unless the customer cancelled and the booking form keeps the deposit.

A booking run's `trigger.body` also holds `booking` (`service_id`, `service`, `starts_at`, `ends_at` in Lagos time, `when`, `auto_confirm`, the reminder times) and `event` (`new` or `rescheduled`).

## QR code and printable card

The web app shows the storefront's address as a QR code (`GET /v1/storefront/qr`, SVG or PNG) and prints an A6 card with the name, the QR code, the address and the phone, for the stall, the counter or the bag.

## API

| Endpoint | |
| --- | --- |
| `GET /v1/storefront`, `PUT /v1/storefront` | The storefront and its draft |
| `POST /v1/storefront/publish`, `/close`, `/reopen` | Publish the draft; close and reopen |
| `GET /v1/storefront/catalogue`, `POST /v1/storefront/catalogue/refresh` | The catalogue copy, and refreshing it |
| `POST /v1/storefront/media` | Store a logo or photo (the image as the body) |
| `POST /v1/storefront/catalogue/rows/{row}/photo` | Store a photo and set it on a row |
| `GET /v1/storefront/qr` | The address as a QR code |
| `GET /v1/storefront/actions`, `PUT /v1/storefront/actions/{kind}` | The forms and their settings |
| `GET /v1/storefront/orders`, `GET /v1/storefront/orders/{reference}` | Orders, questions, call-backs and bookings; `?reveal=true` with `pii.reveal` |
| `PUT /v1/storefront/bookings` | The services table, its mapping and when bookings are taken |
| `GET /v1/storefront/services` | The services copy and the availability in effect |
| `GET /v1/storefront/bookings?from=&to=` | The bookings holding their time (Lagos dates; today and the next 30 days by default) |
| `POST /v1/storefront/bookings/{reference}/cancel`, `/move` | The owner's changes (`move` takes `starts_at`) |
| `POST /v1/storefront/orders/{reference}/refund` | The owner's refund of a paid order or booking that is refund due |
| `GET /v1/storefront/image?src=` | One of the storefront's photos, for the web app, whose Content-Security-Policy allows images from its own origin only. Only the organisation's own public storefront images are fetched (its project host, its images path), through the egress guard |

`preview_url` is where the **published** version serves: a slug changed in the draft serves once published (`published_slug`). The OpenAPI document has the shapes.
