# Storefronts

A storefront is a business's one-page public page: who it is, what it sells, and how to reach it ([spec](spec/storefronts.md), decision 0036). This is Phase 5, S2: the page and its catalogue. Ordering and booking come with S3 and S4; setting up from chat and with field agents with S5; the business's own domain and embedding with S6.

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

The OpenAPI document has the shapes.
