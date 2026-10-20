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

If the table cannot be read, the old copy stays, the page keeps showing it, and the error is shown to the owner. A catalogue holds at most 500 items until the storefront plans set their own (S8).

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

The page offers up to three **forms**: **order** (the catalogue as a basket), **enquiry** (a question) and **call-back** (a request to be called). Each starts a workflow.

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
   - It prices the basket from the **catalogue copy**, never from the form.
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
   - **Declined:** the customer is texted. An order already paid is left **refund due**: the owner refunds it in Paystack, because refunds are not automatic until the refund policy is decided.
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
| `decide` | `accept`, `decline`, `complete` or `cancel` the order (`reference`, optional `note`). A decision the order's status does not allow fails the step |
| `payment_link` | The checkout page for an accepted order paid after acceptance, made once. The run can then wait for the `storefront.payment` signal, correlated by the reference, whose payload's `status` is `paid` or `expired` |
| `notify_customer` | An SMS, and an email when there is one, to the order's customer (`reference`, `text`, optional `subject`) |

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
- `declined`, `refund_due`, `expired`, `cancelled`, `completed`.

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
| `GET /v1/storefront/orders`, `GET /v1/storefront/orders/{reference}` | Orders, questions and call-backs; `?reveal=true` with `pii.reveal` |
| `GET /v1/storefront/image?src=` | One of the storefront's photos, for the web app, whose Content-Security-Policy allows images from its own origin only. Only the organisation's own public storefront images are fetched (its project host, its images path), through the egress guard |

`preview_url` is where the **published** version serves: a slug changed in the draft serves once published (`published_slug`). The OpenAPI document has the shapes.
