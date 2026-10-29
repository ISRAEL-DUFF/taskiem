# Storefronts

A storefront is a business's one-page public page: who it is, what it sells, and how to reach it ([spec](spec/storefronts.md), decision 0036). This is Phase 5, S2 and S3: the page, its catalogue, and ordering, questions and call-backs. Booking came with S4 ([below](#bookings)); setting up and running it [from chat](#from-chat) with S5, and field agents later in S5; the business's [own domain](#your-own-domain) with S6, and embedding later.

It is built and off by default: pages are served only by the [public host](public-host.md), and the web app's **Storefront** page needs `storefront.manage` (owners and admins).

## The page

| Part | What it is |
| --- | --- |
| Business card | Name, tagline, about, logo, area and address, a link to directions (never an embedded map), a **Call** button, a **Chat on WhatsApp** button, opening hours with "Open now" or "Closed now" in Lagos time |
| Catalogue | Items from one of the organisation's [data tables](data-tables.md), grouped by a category column: name, price in naira, description, photo, and "Unavailable" when its available column says so |
| Look | The business's [look](#look): one of three themes, its colour, logo and cover photo. The same look draws its forms, status pages and hosted forms |

Pages are rendered on the server and work without JavaScript. They send no cookies and allow no inline code. Each carries a short safety line. Shared links show the name, tagline and logo (Open Graph), so a link sent on WhatsApp shows a card.

## Look

How every public page of the business looks: the storefront, its order, booking, question and call-back forms, the status pages customers come back to, [hosted forms](forms.md), and the pages around them (closed, not found, report). It is set on **Storefront > Look** and belongs to the business, not to one page ([decision 0042](decisions/0042-public-page-themes.md)).

| Choice | What it is |
| --- | --- |
| Theme | **Clean**: light cards, a cover photo with the logo over it, soft corners. **Bold**: the colour fills the top of every page, with big type, a photo grid and dark summary panels. **Editorial**: black and white with serif headings, fine rules and square corners. Each theme lays every page out its own way |
| Colour | Any colour. Text on it is white, or near-black on a light colour; the colour as text is darkened until it reads at 4.5:1 |
| Logo | Square works best. Without one, the business's initials are shown |
| Cover photo | Shown across the top of the storefront (Clean and Editorial; behind the colour in Bold) |

The tab previews the real pages (storefront, order form, booking, form) in the look being chosen, with sample products and times, before it is saved. Saved, pages show it within 30 seconds.

A storefront that existed before themes kept its look: its classic layout became Clean, its bold layout Bold, its colour and logo the business's.

## Editing and publishing

1. **Draft.** The owner edits the card and chooses a **slug**: 3 to 30 letters, digits and hyphens, the page's address `<slug>.<preview domain>`.
   - Reserved words and protected names (banks, payment providers, agencies, Taskiem, and look-alikes of them) are refused ([public host](public-host.md#building-blocks-for-the-next-milestones)).
   - Saving a draft changes nothing anyone sees.
2. **Catalogue.** **Make my catalogue** (and **Make my services** for bookings; `POST /v1/storefront/catalogue/make`) makes the table and its mapping as chat does, and the owner then adds items in the editor with a name and a price in naira, and a photo, removing them there too: nobody opens the Tables page. A business that brings its own table picks it under **Use a table you already have** and maps its columns:

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

The table is changed only while it still holds the count that was read, so two orders accepted at the same moment both count; each change carries an Idempotency-Key and is recorded on the order before it is sent, so a retried step changes the stock once. An order accepted with too little left lowers it to zero, and its note says which items were short (`decide` returns them as `short`). Declining or cancelling puts nothing back: change the table if the items return to the shelf. Where Taskiem keeps the shop's [stock](stock.md), each lowering is a stock movement (reason `order`), recorded before it is sent, so the count can always be explained, and the Stock page adds sales at the counter, suppliers and purchase orders, counts and shrinkage. Stock is lowered only on Accept, so an order paid when ordering holds none while it waits. A catalogue holds at most 500 items, and the plan's `storefront_items` ([moderation](moderation.md#plans)).

## Photos

Logos and product photos are stored in the organisation's Xenibase storage ([media](media.md)):
- checked by their contents;
- turned upright;
- stripped of their metadata (GPS location, camera);
- stored at 2000, 800 and 320 pixels wide.

Pages load the size the screen needs, lazily.

**Moderation.** Each photo is recorded with a moderation state: `unreviewed`, `approved` or `rejected`.
- A rejected photo is never shown, and uploading it again does not clear it.
- Photos are recorded as unreviewed and shown; a reviewer approves them with the page, or rejects single ones ([trust and safety](moderation.md)). Publishing, and every catalogue item, also goes through the checks there: a page needs a verified phone, and a first publish waits for review unless the business is verified.

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
- **Fulfilment:** pickup and/or delivery, with a note; with delivery, the **areas you deliver to**, each with its fee: the customer chooses one and its fee is added to the total ([dispatch](dispatch.md#delivery-areas)).
- **Payments connection:** which of the organisation's connections takes payments (default `main`). A Paystack connection takes card payments and bank transfers; any other that opens account numbers for one payment (iswallet, including a business's wallet with Taskiem, Flutterwave, OPay) takes bank transfers only, named `connector/name` (`iswallet/taskiem`). With no other such rail, the page offers the Paystack connections as before.

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
3. **Paid when ordering:** on a Paystack form, a checkout page is made at once. The customer's status page takes them to it, and Paystack sends them back.
   - Taskiem asks Paystack about waiting payments every 30 seconds, so no webhook needs setting up in the organisation's Paystack account. When the account does send its events, the payment is also in the [money log](money-log.md), and whichever Taskiem hears first marks the order paid: once ([payments and matching](#payments-and-matching)).
   - **By bank transfer** ([below](#paying-by-bank-transfer)): the status page offers **Pay by bank transfer** beside the card page, or alone on another rail's form.
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
   - **With riders of its own** ([dispatch](dispatch.md)), a business offers a confirmed order for delivery to a rider from its page or chat, or turns on deliveries so its workflow does. The rider's taps move the order to **With a rider**, **On the way** and **At the door**, tell the customer at each step, and their **Delivered** completes it. A customer may **pay at the door**, by an account number issued when the rider arrives, or in cash.

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

### Payments and matching

An order's payment is [matched](matching.md) to it as an invoice's is, and listed on the order page under **Money matched**, with how it was matched.

- **Through its checkout page:** matched exactly, with no one asked. The payment check and the Paystack event (in the money log) take one path: the money log entry, its match `order:AK-7QX4KD`, and the order moved to paid are written together, under the money's lock and the order's, so whichever comes first wins and the other finds it done. The order's paid flow (the workflow started, or the waiting workflow told it is paid, and the status page) happens once.
- **Any other money** (a bank transfer naming the order, a POS sale, a statement line) is suggested for orders waiting for money, beside open invoices, under **Invoices > To match**: an order waiting for its checkout payment (`awaiting_payment`), or one taken with **no online payment** that is not yet paid, cancelled, expired or decided against. An order paid after acceptance is not suggested until its payment link is made (its workflow sends the link and waits for that payment). Confirmed, matched by hand or matched automatically, the money must be at least what is paid online (the deposit, or the total); the order is then paid exactly as by its checkout page. An order taken with no online payment keeps its status (`received`, or `accepted` becomes `paid`); its workflow is already running and waits for nothing.

**Undoing a match** takes the money out of the order and puts it back to be matched. It is refused when the order went on with the payment:
- the payment completed the order's checkout (it was `awaiting_payment`), so its workflow was started or told it was paid;
- the order has been decided since (accepted, completed, declined, refund due).

Then cancel or [refund](#refunds) the order instead. Otherwise the order goes back to unpaid: `received`, or `accepted` again. Money matched to an order that was already paid by its own check is undone without changing the order.

Money that reached a paid order by bank transfer has no Paystack payment to refund: an order declined after it is left refund due, to be refunded by the business itself.

### Paying by bank transfer

An order or booking waiting for its payment can be paid by transfer into a one-time account number on the form's rail ([plan N3](spec/iswallet-account-numbers.md#103-pay-by-transfer-on-the-storefront-n3)), as an invoice's page offers one:

- **On the status page**, **Pay by bank transfer** (`POST /o/{reference}/pay`) asks for the number. The public host only queues the request; within a second the `api` role checks the order is still waiting, lengthens its hold so the customer has time (at least an hour for an order, 30 minutes for a booking's time; the page says until when), and issues the number with the business's connection, keyed to the order so a retry is the same number. The page shows the number, bank, name, amount and minutes left, refreshing itself until the money arrives. At most 5 numbers an hour and 20 in all per order.
- **The numbers are the invoices' accounts** (`customer_invoice_accounts`, holding an order instead of an invoice): the payment id is recorded before the rail is asked, rails that can be asked are checked every 30 seconds, and the rail's payment event (in the money log) is matched exactly. Either way the order is paid along the path its card payment takes, once, and its workflow starts (or the waiting one is told).
- **Paid after acceptance** on another rail's form: the workflow's payment link is the order's status page, held for 24 hours.
- **Late or wrong amounts** are each rail's to return or report, as for invoices. A payment that reaches an order no longer waiting (expired, or paid another way) stays in the money log for a person (To match).

### Refunds

A paid order or booking that is declined or cancelled is left **refund due**. Taskiem never refunds unasked: the owner confirms, and then Taskiem refunds the payment in full through the business's own Paystack connection, from its Paystack balance.

**Paid by bank transfer** (or by money matched to it; the order's `paid_by` is `transfer` or `other`), there is no card payment to refund and Taskiem cannot send money back on those rails: the owner sends it back from their own bank and marks the order refunded (**Mark refunded** on the order page, `refunded AK-7QX4KD` in chat, or `POST /v1/storefront/orders/{reference}/refunded`, with an optional note). The customer is told. The workflow's refund step answers `by_hand: true` with what to do, and the order says so until it is marked.

- **In the default workflows:** after a paid order or booking is declined, the owner is asked "Refund the customer?": approve to refund now, reject to refund it yourself.
- **In chat:** `refund AK-7QX4KD`, confirmed with **yes** (audited; `storefront.manage`). Cancelling a paid booking in chat says so.
- **In the web app:** the order page's **Refund through Paystack**, or **Mark refunded** for one refunded outside Taskiem.
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

## Your own domain

A business that already owns a domain can serve its page there instead of the preview address ([spec 6.6](spec/storefronts.md#66-addresses-domains-and-sharing)): `adakitchen.ng`, or a part of a domain it uses for something else, such as `shop.example.com`. This is Phase 5, S6. Buying a domain through the domain platform waits for its contract; embedding the page on a website comes later.

1. **Add it** (the editor's **Your own domain**, or `POST /v1/storefront/domains` with `{"domain": "adakitchen.ng"}`). What was typed is tidied to a host name (`https://AdaKitchen.ng/` is `adakitchen.ng`). Refused, as for [embed sites](embed-sites.md#adding-a-site):
   - IP addresses, punycode names and public suffixes themselves (`com.ng`);
   - government and protected names;
   - the platform's own domains: the web app's (`TASKIEM_PUBLIC_URL`) and the public host's (`TASKIEM_PUBLIC_DOMAINS`, so never a preview address).

   A registrable domain brings its `www.` twin with it, and `www.adakitchen.ng` brings `adakitchen.ng`; a subdomain such as `shop.example.com` is just itself. A storefront may have up to 5 domains.
2. **Create the records** the answer lists (`records`), at the company the domain was bought from:

   | Record | Why |
   | --- | --- |
   | TXT `_taskiem-verify.adakitchen.ng` = `taskiem-domain-verify=…` | Proves the business controls the domain. One record covers the domain and its `www.` twin. Keep it: it is checked again |
   | A / AAAA `adakitchen.ng` → the public host's addresses | Sends visitors to the page. A registrable domain cannot be a CNAME; without addresses configured, the answer says to use the DNS provider's ALIAS, ANAME or CNAME flattening to the CNAME target instead |
   | CNAME `www.adakitchen.ng` (or `shop.example.com`) → `pages.<preview domain>` | The same, for a subdomain |

3. **Check it** (`POST /v1/storefront/domains/{id}/verify`). The answer is 409 until the TXT record holds the value; DNS can take a few hours. Once verified the domain serves the page on every replica within 30 seconds, the proxy obtains its certificate the first time it is visited, and the page's address becomes the domain.

**Check now** (`POST /v1/storefront/domains/{id}/check`, no-technical-steps N9) looks each record up and says in plain words which is missing or wrong ("The TXT record named _taskiem-verify is not there yet", "adakitchen.ng points somewhere else (198.51.100.1)…"), with `all_ok` and `proven`; it changes nothing. Each record also has `short`, its name as registrars ask for it (`_taskiem-verify`, `@`, `www`). The web app's **Check now** calls it, then verify once the proof is there, and shows step-by-step guides for Whogohost, Qservers, DomainKing, GoDaddy, Namecheap and any other registrar, with each record's boxes filled in ([owners' guide](guide/own-domain.md)).

Each step needs `storefront.manage` and is audited: `storefront_domain.add`, `storefront_domain.verify`, `storefront_domain.primary`, `storefront_domain.remove`.

**Who gets a domain.** Any number of organisations may add the same domain, each with its own value to publish; the first to prove it serves it. Another can prove it only once the first removes it or it is unverified. Nothing is taken from a page serving it.

**One primary.** The first domain verified is the **primary**; `POST /v1/storefront/domains/{id}/primary` makes another verified domain the primary. While the page is published or closed, every other address of it (the preview address, the `www.` twin, other domains) answers a page view with a **301** to the same path on the primary, kept by browsers for an hour only (`Cache-Control: max-age=3600`), so a domain that later stops serving is not followed for long. Forms being sent (`POST`) and `/embed/` views are served where they are. A draft is not served on any address; a suspended page answers 410 on every address, with no redirect.

**Proven again.** As embed sites are, every 6 hours: a domain whose TXT record no longer holds its value, 4 checks in a row over at least 24 hours, is **unverified**. It stops serving at once (the proxy's `/tls/ask` answers 404 for it), the preview address serves the page again with no redirect, and `storefront_domain.unverified` is audited with the reason by `storefront-domain-checker`. A check that says nothing (a DNS timeout) is not counted. The editor shows the reason; putting the record back and checking again restores it. The owners hear of it on the chat they linked (WhatsApp, else Telegram) and on the alert channels: a `domain_unverified` alert ([alerts](alerts.md)) saying which record to check.

**Removing** a domain (`DELETE /v1/storefront/domains/{id}`) stops it serving at once, within the public host's 30-second cache.

**Links.** The storefront's `url` is its primary domain once verified, else `preview_url`. The web app's links, the QR code and printed card, the page's `og:url` and canonical link, the public host's `/qr.png` and the chat's links all use it. Status links already sent on the preview address keep working: they redirect.

**On the public host** a business's domain is a `public_hosts` row like the preview address, served with the same headers: no cookies, the strict Content-Security-Policy, no framing ([spec 8.3](spec/storefronts.md#83-isolation-from-the-app)). It serves nothing of the web app or the API.

**Operators** set where domains point:

| Variable | |
| --- | --- |
| `TASKIEM_PUBLIC_CNAME` | The name subdomains point at. Default `pages.<first of TASKIEM_PUBLIC_DOMAINS>`, which the preview domain's wildcard record already sends to the public host |
| `TASKIEM_PUBLIC_ADDRESSES` | The public host's IPv4 and IPv6 addresses (comma-separated), for registrable domains' A and AAAA records. Unset, owners are told to use ALIAS or CNAME flattening |

Certificates are issued on demand by the proxy in front of the public host ([public host](public-host.md#setting-it-up)).

## QR code and printable card

The web app shows the storefront's address as a QR code (`GET /v1/storefront/qr`, SVG or PNG) and prints an A6 card with the name, the QR code, the address and the phone, for the stall, the counter or the bag.

The public host serves the same code as `https://<host>/qr.png` on every published storefront's host: a PNG of the page's address (its [own domain](#your-own-domain) once live, else `<slug>.<preview domain>`, whichever of the storefront's hosts is asked), cacheable for an hour, holding nothing but the address. A draft has none. That is the image the chat sends.

## From chat

The owner runs the storefront from WhatsApp or Telegram (spec [5.1](spec/storefronts.md#51-the-owner-sets-up-from-chat) and [6.8](spec/storefronts.md#68-managing-it-from-chat)). Every command needs `storefront.manage`; those that change the catalogue's rows also need `table.edit`. Every change is shown and waits for **yes** (**no** or `cancel` drops it), goes through the same code as the web app's, and is audited with `channel` set to the chat.

| Message | What happens |
| --- | --- |
| `make my page` | With no storefront yet, five questions, one at a time: the business's name, one line of what it sells, the area or address, opening hours, a photo for the top of the page (`skip` leaves any but the name out). Each answer is checked as publishing checks it. Then the draft is saved at the first free address the name gives (`adas-kitchen`, then `adas-kitchen-yaba`, then `adas-kitchen-2`…), a **products** table (`name`, `price` in naira, `photo`, `available`, `stock`) is made in the organisation's Xenibase project and mapped as the catalogue, and Taskiem shows the summary and asks to publish. Where data tables are not available, it says so and the page is saved without a catalogue. On **yes**: the catalogue copy is refreshed, the draft published, and Taskiem sends the link, the QR code image and a status text to paste (`Order from Ada's Kitchen here: https://…`), then offers **Turn on orders?** |
| `turn on orders` (or **yes** to the offer) | The order form's workflow (and the booking form's, when the page has services) is made from its template and published from chat with the same checks as the Forms page and the [one-person approval rule](governance.md#one-person-businesses), the form switched on, and a published page published again. Payment after acceptance when Paystack is connected, otherwise none (paid on collection or delivery), with a link to connect Paystack |
| `my page`, `make my page` (once there is one) | The link, whether it is a draft, published or closed, and today's orders |
| `publish` | Publishes the draft as it is, on **yes** |
| `qr` | The QR code image, its address, and where the printable card is in the web app |
| `add product Jollof rice 2000`, or a photo captioned `Jollof rice ₦2,000` | Prices are read forgivingly (`2000`, `₦2,000`, `N2000`, `2k`, `2.5k`). Shown, then added to the catalogue table on **yes**, the photo stored as an upload is (resized, stripped, moderated: a rejected photo adds nothing) and its id written to the photo column; the copy is refreshed. A storefront without a catalogue gets the products table then. A name already there is refused with a hint to change its price |
| `price <item> <price>`, `hide <item>`, `show <item>`, `stock <item> <n>` | The item is found by name, case-insensitively: the exact name, else the one the words start, else one containing them; when several match, a numbered list to pick from. Shown old to new, then written by row id on **yes** to the mapped columns (an owner's own table keeps its own column names; a mapping without the column is refused), and the copy refreshed. `hide` marks the item unavailable. Where Taskiem keeps the shop's [stock](stock.md), `stock` is a count: a movement setting the stock |
| `hours mon-sat 8-6` | Opening hours, read forgivingly: `mon-sat 8-6`, `daily 9am-9pm`, `weekdays 8:30-17:30, weekends 2-6`, `mon-fri 9-1 2-6`, `mon-sat 8-6, closed sunday`, `open 24 hours`. Times without am or pm are a day's opening (`8-6` is 08:00 to 18:00). Saved in the draft, and a published page is published again |
| `add service Haircut 30 2000` | The name, the minutes (`30`, `45min`, `1h`) and the price. A storefront without a services table gets a **services** table (`name`, `price`, `duration_minutes`, `capacity`), mapped for [bookings](#bookings) with the availability kept; the service is added, the copies refreshed and a published page published again. Customers book it once the booking form is on |
| `close shop`, `close shop back on monday`, `close shop today` | [Close](#editing-and-publishing) with that message (or the default one). `close shop today` closes with "Closed today. We open again tomorrow." and reopens by itself at 06:00 Lagos time the next day (the scheduler's reopener; audited as `storefront.reopen`), which the confirmation says |
| `open shop` | Reopen |
| `orders` | The ten newest open orders, questions and call-backs: reference, items (or the service and time), total, status in words, the customer masked |
| `order AK-7QX4KD` | One order, the customer masked |
| `order AK-7QX4KD ready` | For an accepted or paid order: texts the customer that it is ready (by SMS, and email when they gave one), and leaves its status: it is not done until it is collected |
| `order AK-7QX4KD delivered` (or `collected`, `done`) | Completes it, as the order workflow's "mark it delivered or collected" step does. A run still waiting on that step finds it completed |
| `order AK-7QX4KD to Musa`, `release AK-7QX4KD`, `deliveries`, `turn on deliveries` | A business's own riders ([dispatch](dispatch.md#in-chat)) |

Photos are fetched from the chat (WhatsApp's media API, Telegram's `getFile`), at most 10 MB, held in memory only. A photo sent while something else is being answered is refused.

The commands work in the organisation's languages too (word lists in `engine/lang/intents`, drafts until reviewed): `àwọn àṣẹ mi` (orders) in Yoruba, `rufe shagona` (close shop) in Hausa, `mepee ụlọ ahịa m` (open shop) in Igbo, `wetin people order` in Pidgin, and `ti ṣetán AK-7QX4KD` (ready). Changing the hours or adding a service from chat republishes the page; when the draft held other changes not yet published, the reply says they went live too.

Customers' texts are in the business's language (its default chat language, Settings > Languages): the order and booking templates name Taskiem's customer messages (`notify_customer` with `say`, such as `order_confirmed_pickup`), and "your order is ready" from chat is the same.

## API

| Endpoint | |
| --- | --- |
| `GET /v1/storefront`, `PUT /v1/storefront` | The storefront and its draft |
| `POST /v1/storefront/publish`, `/close`, `/reopen` | Publish the draft; close and reopen |
| `GET /v1/storefront/catalogue`, `POST /v1/storefront/catalogue/refresh` | The catalogue copy, and refreshing it |
| `POST /v1/storefront/media` | Store a logo, cover or photo (the image as the body) |
| `GET /v1/settings/brand`, `PUT /v1/settings/brand` | The business's [look](#look): `theme`, `color`, `logo` and `cover` (media ids) |
| `POST /v1/settings/brand/preview` | The addresses of the public pages drawn in a look not yet saved, for an hour |
| `POST /v1/storefront/catalogue/rows/{row}/photo` | Store a photo and set it on a row |
| `GET /v1/storefront/qr` | The address as a QR code |
| `GET /v1/storefront/domains`, `POST /v1/storefront/domains` | The business's [own domains](#your-own-domain), and adding one |
| `POST /v1/storefront/domains/{id}/verify`, `/primary`, `DELETE /v1/storefront/domains/{id}` | Checking a domain's TXT record, making it the primary, removing it |
| `GET /v1/storefront/actions`, `PUT /v1/storefront/actions/{kind}` | The forms and their settings |
| `GET /v1/storefront/orders`, `GET /v1/storefront/orders/{reference}` | Orders, questions, call-backs and bookings; `?reveal=true` with `pii.reveal`. One order comes with the money matched to it (`payments`; undone with `POST /v1/money/order-payments/{id}/undo`, [matching](matching.md)) |
| `PUT /v1/storefront/bookings` | The services table, its mapping and when bookings are taken |
| `GET /v1/storefront/services` | The services copy and the availability in effect |
| `GET /v1/storefront/bookings?from=&to=` | The bookings holding their time (Lagos dates; today and the next 30 days by default) |
| `POST /v1/storefront/bookings/{reference}/cancel`, `/move` | The owner's changes (`move` takes `starts_at`) |
| `POST /v1/storefront/orders/{reference}/refund` | The owner's refund of a paid order or booking that is refund due |
| `GET /v1/storefront/image?src=` | One of the storefront's photos, for the web app, whose Content-Security-Policy allows images from its own origin only. Only the organisation's own public storefront images are fetched (its project host, its images path), through the egress guard |

`preview_url` is where the **published** version serves on the preview domain: a slug changed in the draft serves once published (`published_slug`). `url` is the page's address to share: its primary [own domain](#your-own-domain) once verified (`domain`), else `preview_url`. The OpenAPI document has the shapes.
