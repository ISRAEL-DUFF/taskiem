# Taskiem's brand

Taskiem's look ([decision 0043](../decisions/0043-taskiem-brand.md)): a "t" whose foot hands the work on to a dot, in pine and lime, and "taskiem" whose i carries the same round dot.

Building or changing a screen in the web app? Follow [Building screens in Taskiem's look](screens.md): the colours, type, layout, components, words and checks every screen uses.

## Three brands, one in use

Three directions were drawn; relay is Taskiem's brand, and the other two are kept, with their files and colours, so they can be tried again:

| Folder | Direction |
| --- | --- |
| `relay/` | **The brand.** A lime "t" on pine whose foot hands the work on to a white dot; "taskiem" in pine, the i's dot a round one (lime on pine) |
| `seal/` | A vermilion round stamp with a tilted T; "Taskiem" in ink blue |
| `signal/` | A white "t" and a saffron square on ink; "taskiem" with a saffron square for the i's dot |

To see the web app in another one, add `?brand=seal` (or `signal`, or `relay` to go back) to any address. The browser remembers it; nothing changes for anyone else. The colours are in `web/src/styles.css` (`--brand`, under `data-brand`), the switch in `web/src/brand.ts`.

## The files

Each folder holds the same files:

| File | Use |
| --- | --- |
| `mark.svg` | The mark: app icons, the sign-in page, the app's sidebar, anywhere the name is not beside it |
| `mark-small.svg` | The mark for 16 to 24 px (browser tabs): simplified and thicker |
| `mark-reversed.svg` | On the brand's dark colour |
| `mark-mono.svg` | One colour, for print in black |
| `avatar.svg`, `whatsapp-profile.png` | Square, for a round crop: the WhatsApp and Telegram profile pictures |
| `wordmark.svg` | The name. Beside the mark, the gap is half the mark's width |
| `wordmark-reversed.svg` | White, on the brand's dark colour |
| `wordmark-mono.svg` | Black, for print |
| `og.png` | The picture a shared link shows (1200 × 630) |

Relay's and signal's wordmarks draw the i's dot themselves, placed where signal's square is: relay's is round, pine on light backgrounds and lime on pine (the reversed wordmark), so lime stays on pine. The wordmarks' letters are outlines of Plus Jakarta Sans ExtraBold (SIL Open Font License), so they look the same everywhere without the font. `make.py` draws every SVG from the font, all three brands at once; change it and rerun it rather than editing the files by hand. The PNGs are the SVGs rendered at size.

The web app serves its copies from `web/public/brand/<brand>/` (the tab icon, the phone home-screen icon, the mark, the wordmark). For relay it also serves the install icons the app's manifest (`web/public/manifest.json`) names: `icon-192.png` and `mark-512.png` from `mark.svg`, and `maskable-192.png` and `maskable-512.png` from `avatar.svg`, whose square background lets a phone cut its own shape; the "t" and the dot sit inside the middle 80% a phone keeps.

## Relay's colours

| Name | Hex | Use |
| --- | --- | --- |
| Pine | `#0d3b2e` | The brand. Buttons, links, headings on light backgrounds, colour blocks |
| Lime | `#c8f169` | On pine only: the "t", highlights, a button on a pine block. Never text on white (it does not read) |
| White | `#ffffff` | The mark's dot; text on pine |
| Mist | `#f1f4ef` | Light backgrounds |

In the app's dark theme, lime takes pine's place as the accent.

## Rules

- **The name is "taskiem"**, said "task-YEM". In sentences it is written "Taskiem".
- **Lime belongs on pine.** On a white or mist page, lime appears only inside a pine shape.
- **The mark is not redrawn.** Use the files: no new colours, outlines, shadows or rotations, and nothing inside the rounded square but the "t" and its dot.
- **On a business's pages, Taskiem steps back.** "Powered by Taskiem" is small, grey text; the business's own look leads ([themes](../decisions/0042-public-page-themes.md)).
