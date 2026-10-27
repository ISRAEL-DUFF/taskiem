# 0043 — Taskiem's brand: the handing-on "t", in pine and lime

Date: 2026-10-22 · Status: **Accepted** 2026-10-22 ([brand guide](../brand/README.md))

## Context

Taskiem had two marks that did not match. The website used a green rounded square with a tick in a circle (a to-do app's sign) and a stray dot; the app used a plain "T" in a green square. The green was close to WhatsApp's, where Taskiem mostly lives, so the bot's picture looked like an imitation. With [themes](0042-public-page-themes.md), every business's pages now look like the business; Taskiem's own look was the weakest part of what people saw.

The mark has to work small first: the most-seen version is a round WhatsApp profile picture, then a 16 px browser tab. It has to work in one colour (printed QR cards, stickers by a POS), and to step back on businesses' pages.

## Decision

1. **The mark is a "t" whose foot hands on to a dot**: work passed along, done without the owner chasing it. Lime on a pine rounded square, the dot white. At 16 to 24 px the dot is dropped and the strokes thickened.
2. **The wordmark is "taskiem"**, lower case, Plus Jakarta Sans ExtraBold (the website's typeface), outlined so it needs no font. The name is said "task-YEM".
3. **Pine `#0d3b2e` and lime `#c8f169`**, with white and mist `#f1f4ef`. Pine is dark enough that it reads as nothing like WhatsApp's green; lime is used only on pine.
4. **Applied** to the web app (accent and primary colours, the tab and home-screen icons, the mark in the sidebar and top bar, the sign-in page, the agent and operator consoles), the website mockup and the docs site, and the profile picture and link image in `docs/brand/relay/`.
5. **The other two directions are kept** (`docs/brand/seal/`, `docs/brand/signal/`, drawn by the same `make.py`), and the web app shows either with `?brand=seal` or `?brand=signal`, for this browser only, so they can be compared on the real product before the brand is final.

**Amended 27 October 2026.** The mark's dot moves from the end of the foot to the "t"'s top right, where signal's square is, and stays round and white; the 16 to 24 px mark keeps it. The wordmark's i takes a round dot of its own in the same place as signal's: pine on light backgrounds, lime on the reversed wordmark.

## Alternatives considered

Three directions were drawn and tried on the website (the brand canvas): a round seal with a tilted T (approvals), this mark, and a black-and-white wordmark with a saffron square for the i's dot. This mark was also drawn in five other colour pairs. The seal's red read as "error" across a whole site; the square wordmark was calm but had no mark of its own for a profile picture; pine and lime is the mark as first drawn.

## Consequences

- The WhatsApp and Telegram bot profile pictures are set by hand from `docs/brand/taskiem-whatsapp-profile.png` (Meta Business Manager, BotFather).
- A business's default colour on its public pages stays its own default (`brand.DefaultColor`), not Taskiem's.
- The trademark check of the name and the mark (NIPO) is the founders' to do before the mark is printed.
