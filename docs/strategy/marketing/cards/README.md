# Business cards and staff ID cards

Two cards in the relay brand ([brand](../../../brand/README.md)), from one design: `card-maker.html`.

| Card | Size | Bleed | Pages |
| --- | --- | --- | --- |
| Business card | 85 × 55 mm | 3 mm | Front, then back |
| Staff ID card | CR80, 54 × 85.6 mm, upright | 2 mm | Front, then back |

## For anyone: the card maker

The [card maker](https://claude.ai/artifact/8cR1TFT5U9RiVB6RpwiruJ) is `card-maker.html` published as a page. Type a person's details, add a photo, and both cards update as you type. **Download business card PDF** and **Download staff ID PDF** save print-ready files. An empty contact field takes its line off the card. The page remembers the last details in that browser only. It is private to the founder's account until shared from its Share menu.

Its PDFs are pictures of each side at about 770 dpi, which print sharply. For files with vector type, use the script below.

## For engineering: many cards at once

```sh
cp docs/strategy/marketing/cards/people.example.json people.json   # one entry per person
node docs/strategy/marketing/cards/make-cards.mjs people.json out/
```

It writes `taskiem-business-card-<name>.pdf` and `taskiem-staff-id-<name>.pdf` for each person, with vector type and the repository's own fonts, so it needs no network. It uses Playwright with Chromium from the web app's dependencies (`pnpm install`, then `pnpm --filter web exec playwright install chromium` once).

Each person in the list takes these fields; leave out a contact field to take its line off the business card:

| Field | On |
| --- | --- |
| `name`, `title` | Both cards |
| `phone`, `whatsapp`, `email`, `web`, `office` | Business card back |
| `staffNo`, `team`, `issued`, `expires` | ID card front |
| `photo` | ID card front: a JPEG or PNG path, relative to the list file |
| `returnTo`, `returnPhone`, `emergency` | ID card back |
| `slug` | The file name, instead of one made from `name` |

Keep real lists out of the repository: they hold staff phone numbers and photos.

## Changing the design

Edit `card-maker.html`. It is the page and the script's template both, so a change shows in both. Then republish the page from it and check a card with the script.
