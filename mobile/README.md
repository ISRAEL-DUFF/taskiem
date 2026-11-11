# The Taskiem phone app

Taskiem's chat as an app for Android and iPhone, in Flutter. The plan, its
milestones and every decision are in [docs/spec/mobile-app.md](../docs/spec/mobile-app.md)
([decision 0066](../docs/decisions/0066-a-flutter-phone-app.md)).

**The app looks, moves and sounds exactly like the web chat** (the demo and
advert films are made from the web chat). The web chat is the design: the
theme is generated from its CSS, and a parity test compares the two.

## Running it

Needs Flutter (the version in `.github/workflows/mobile.yml`) and, to build
for Android, the Android SDK; for iPhone, a Mac with Xcode.

```sh
cd mobile
flutter pub get
flutter run            # on a phone or an emulator
flutter test           # every test, the parity test included (on Linux)
flutter analyze
```

## What is generated, and from where

| What | From | Written by |
| --- | --- | --- |
| `lib/theme/tokens.g.dart`: every colour light and dark, shadows, radii, motion durations and curves | `web/src/styles.css`, `kit.css`, `motion.css` | `go run ./tools/mobilegen` (from the repository root) |
| `assets/brand/*.svg`: the mark and wordmarks | `web/public/brand/relay/` | the same command |
| `assets/fonts/Inter-*.ttf`: Inter, one file per alphabet | the web's `@fontsource-variable/inter` files | `python3 mobile/tool/fonts.py` (with fontTools and brotli) |

Never edit these by hand. `go test ./tools/mobilegen` fails when the theme
or the brand files are not the web's.

## The parity test

Two halves, joined by the pictures in `test/parity/web/`:

- **The web half**, `web/e2e/parity.spec.ts` (with the browser tests),
  takes each screen the app copies as the web draws it, light and dark, at
  390 × 844 and 360 × 640, and fails when the web no longer looks like the
  kept picture.
- **The app half**, `test/parity_test.dart`, draws the same screens and
  fails when they differ from the kept pictures by 1% of the screen or more,
  a pixel counting only when nothing within one pixel of it matches (two
  engines never draw a letter's edge alike). Failures leave both pictures in
  `build/parity/`.

When the web's look changes on purpose: run the web half with
`PARITY_UPDATE=1` to keep the new pictures, then change the app until its
half passes. Both go in the same pull request.

A new screen: add it to `SCREENS` in the web half and to `screens` in the
app half under the same name.
