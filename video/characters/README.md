# Taskiem cast

The full written design is in [bible.md](bible.md) (the source of truth, tool-agnostic). Below: the first-pass flat sheet: open `sheet.html` (the SVGs are drawn in the page,
so a change to a face or a colour is a change to one line), or see
`cast-sheet-v1.png`. Names, businesses and prices come from the demo world
(`web/e2e/demo/seed.ts`) so the films and the app recordings show the same people.

## Style rules
- Flat shapes, no outlines, round heads, dot eyes, one-line smile. Same face for everyone;
  hair, colour and one prop tell them apart.
- Brand colours only (`kit.css`): pine, lime, paper, peach, warn. Lime is the accent and
  appears on every character once (Ada's wrap pin, Tunde's lanyard, Emeka's cap).
- Skin tones are warm deep browns, varied person to person, never one flat tone.
- Characters appear as bust portraits in chat avatars, and full-body (to do) beside the phone.

## Cast
| Character | Role in the story | Look | Used in |
| --- | --- | --- | --- |
| Ada Obi | Hero, owner of Ada's Kitchen, Yaba | Headwrap with lime pin, pine top, lime apron | Every film |
| Tunde | Cashier; staff within limits | Short hair, pine top, lime lanyard | Staff, Deals |
| Bayo Adeyemi | Supplier, Bayo Rice Mills | Cap, beard, rice-sack brown | Trade, Reorder |
| Ibinabo George | Buyer, Rivers Foods | Hair puff, glasses, white shirt | Deals |
| Kola Adebayo | Neighbour, Kola's Bakery | Fade, orange shirt with flour | Explore |
| Emeka Nwosu | Rider, CoolBox Logistics | Lime helmet, delivery box | Deliveries |

## To do
- Expressions per character (worried, relieved, pleased, haggling) as swappable mouths and brows.
- Full-body poses and hands holding a phone, for the phone-cut scenes.
- Move the final art into `video/kit/` so scenes can use it like the existing `.avatar`.
- Voice and wardrobe notes for the live-action shoot, if we film real people.
