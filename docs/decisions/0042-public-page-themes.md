# 0042 — Themes for public pages: three looks, each with its own markup

Date: 2026-10-22 · Status: **Accepted** 2026-10-22 (built: N13, [storefronts](../storefronts.md#look))

## Context

A business's public pages are its storefront, its order, booking and enquiry forms, the status pages a customer comes back to, hosted forms, and the small pages around them (closed, not found, report). They all ran on one stylesheet of about 116 lines, written to be "small, readable on a cheap phone, no web fonts". It managed that, and nothing more:

- **No identity.** A storefront got a 96px round logo, the name in the system font, and a coloured line from 8 named colours. Forms, order and booking pages did not get the logo. Hosted forms were always green.
- **No hierarchy.** It was one 40rem column of default-size text, with outline buttons and grey dividers, and products as list rows.
- **Pages that belong together did not look it.** The storefront, its order page and its thank-you page each looked different.

Owners said, rightly, that they could not imagine customers using such pages, and certainly not as the face of their business.

The constraints behind the old stylesheet still hold. Cheap Android phones and expensive data mean pages must stay under the byte budgets in [storefronts 8.4](../spec/storefronts.md). The public host's CSP allows no inline style or script. Pages must work without JavaScript. None of that requires plain pages.

## Decision

1. **Three themes: Clean, Bold and Editorial.** Each was designed as a whole set of pages first (the mockups), and the owner picks one.
   - **Clean:** light cards on a pale ground, a cover photo with the logo over it, rounded corners. It suits almost any business.
   - **Bold:** the business's colour fills the top of every page, with heavy type, a photo grid, pills and dark summary panels.
   - **Editorial:** black and white with the colour used sparingly, serif headings, hairline rules, square corners and small capitals.
2. **Each theme has its own markup for every page** (`engine/publicweb/templates/<theme>/`), not one markup styled three ways.
   - The designs differ in structure, not only in colour:
     - Bold writes each page's heading into the coloured block.
     - Editorial lists a catalogue as a menu with one feature photo per section.
     - Clean puts the logo over a cover photo.
   - CSS over one markup could only fake that with reordering and hidden duplicates, and each fake would drift from its design.
   - All 14 page types have templates in all three themes, embedded forms included.
3. **What is the same is shared, so three markups do not mean three times the bugs:**
   - **Data:** the Go code that builds each page's data runs once; a theme only draws it.
   - **The page contract:** field names, the form token and spam trap, error messages and the payment hand-off are identical in every theme. The head's tags, the hidden fields and the icons are shared templates (`templates/shared/`).
   - **Behaviour:** one `order.js` adds the total and the −/+ buttons by data attributes, whatever the markup.
   - **Tests:**
     - The whole public host suite runs once per theme (`TASKIEM_TEST_THEME`, in CI).
     - A test fails a theme that lacks a page or a partial.
     - The CSP and byte-budget tests cover every theme's templates and stylesheet.
4. **The look belongs to the business, not to the storefront.** `tenants.brand` holds:
   - the theme;
   - a colour: any `#rrggbb`, no longer 8 names;
   - a logo and a cover photo: storefront media ids, with their URLs copied in when set.

   Every public page of the business uses it, hosted forms included. A storefront's layout, colour name and logo became the brand when this shipped (`classic` became Clean).
5. **Any colour, always readable.** The server makes a palette from the colour (`brand.PaletteOf`):
   - text on the colour is white, or near-black on a light colour;
   - the colour as text is darkened until it reads at 4.5:1 on white;
   - two tints are made for highlights.

   The palette is served as a stylesheet, `/_brand/1/<rrggbb>.css`, made from its name alone. It is the same for every business with that colour and cached for good, and the CSP stays `style-src 'self'`.
6. **Owners see the real pages as they choose.** The app's **Storefront > Look** tab frames the public host's own pages, drawn in the look being chosen, with sample content and the business's name.
   - The api role seals what to show with the key it shares with the public host, so only a signed-in owner can make a preview, and only for their own name and images.
   - A preview works for an hour and can be framed by the app only.
7. **Light only.** The themes are drawn light, as a business's printed material is. The old stylesheet followed the phone's dark mode, which made a business's colours change with each visitor's setting.

## Alternatives considered

| Option | Why not |
| --- | --- |
| One markup, three stylesheets | Cheaper to keep, but cannot reproduce the designs. Their structure differs (see 2) |
| A per-storefront look, apart from the business's | Hosted forms have no storefront, so they would stay generic. A second place to set a look is a second thing to explain |
| Owners write their own CSS | Breaks the readability guarantees and the byte budget, and is a technical step (no-technical-steps) |
| Web fonts for the themes | 20 to 60 KB a font on 3G. The phone's own sans and serif (Georgia, or Noto Serif on Android) carry the designs |
| Inline `style` for the colour, with a nonce | Weakens the public CSP for every page to save one cached request |
| Theme previews drawn in React | A fourth copy of each design, which would drift from the pages it imitates |

## Consequences

- Each new public page needs three templates. The page test fails until all three exist.
- A design change in one theme is made in that theme's templates and stylesheet only.
- Each theme's stylesheet is about 12 KB, and a page loads one. The per-page CSS budget is now "the theme's and the colour's together, under 15 KB".
- `Card.Template` and `Card.Accent` are still accepted so older drafts publish, but nothing draws with them.
