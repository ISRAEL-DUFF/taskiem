# Taskiem's brand

Taskiem's look ([decision 0043](../decisions/0043-taskiem-brand.md)): a "t" whose foot hands the work on to a dot, in pine and lime.

## The mark and the wordmark

| File | Use |
| --- | --- |
| `taskiem-mark.svg` | The mark: a lime "t" on pine, handing on to a white dot. App icons, the sign-in page, anywhere the name is not beside it |
| `taskiem-mark-small.svg` | The mark for 16 to 24 px (browser tabs): thicker strokes, no dot |
| `taskiem-mark-reversed.svg` | On pine: a pine "t" on lime |
| `taskiem-mark-mono.svg` | One colour, for print in black |
| `taskiem-avatar.svg`, `taskiem-whatsapp-profile.png` | Square, for a round crop: the WhatsApp and Telegram profile pictures |
| `taskiem-wordmark.svg` | "taskiem", lower case, in pine. Beside the mark, the gap is half the mark's width |
| `taskiem-wordmark-reversed.svg` | White, on pine |
| `taskiem-wordmark-mono.svg` | Black, for print |
| `taskiem-og.png` | The picture a shared link shows (1200 × 630) |

The wordmark's letters are outlines of Plus Jakarta Sans ExtraBold (SIL Open Font License), so it looks the same everywhere without the font. `make.py` draws every SVG from the font; rerun it rather than editing them by hand. The PNGs are the SVGs rendered at size.

The web app serves its copies from `web/public/brand/` (the tab icon, the phone home-screen icon, the mark).

## Colours

| Name | Hex | Use |
| --- | --- | --- |
| Pine | `#0d3b2e` | The brand. Buttons, links, headings on light backgrounds, colour blocks |
| Lime | `#c8f169` | On pine only: the "t", highlights, a button on a pine block. Never text on white (it does not read) |
| White | `#ffffff` | The dot; text on pine |
| Mist | `#f1f4ef` | Light backgrounds |

In the app's dark theme, lime takes pine's place as the accent.

## Rules

- **The name is "taskiem"**, said "task-YEM". In sentences it is written "Taskiem".
- **Lime belongs on pine.** On a white or mist page, lime appears only inside a pine shape.
- **The mark is not redrawn.** Use the files: no new colours, outlines, shadows or rotations, and nothing inside the rounded square but the "t" and its dot.
- **On a business's pages, Taskiem steps back.** "Powered by Taskiem" is small, grey text; the business's own look leads ([themes](../decisions/0042-public-page-themes.md)).
