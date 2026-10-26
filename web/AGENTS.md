# The web app: instructions for coding agents

Every screen in this app follows [Building screens in Taskiem's look](../docs/brand/screens.md). Read it before you build or change a page, and copy the closest finished screen it names.

The rules you must not break:

- Use the CSS variables in `src/styles.css`, never a hex colour in a component. Lime (`--brand-dark`) appears only on pine (`--brand`).
- Phone first: every page works at 390 px and 360 px wide with nothing scrolling sideways.
- Lists are rows (`Items` with `Item` or `Row` from `src/ui.tsx`); tables only hold figures and line items.
- A page about one thing with money leads with a pine hero card (`.hero`).
- Plain words on an owner's screens: none of the words in Appendix C of `docs/spec/no-technical-steps.md`. Money with `naira()`, times with `fmtTime()`, dates with `dateLabel()`.
- Every page passes axe in both themes: add new pages to `e2e/accessibility.spec.ts`, and owner pages to `src/lib/plainwords.test.ts`.
- The mark comes from `brandFile()`; never redraw or recolour it.
