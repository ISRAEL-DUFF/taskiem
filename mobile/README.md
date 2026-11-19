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

`assets/fonts/InterTight-*.ttf` are not generated: Google Fonts' Inter Tight,
for the headings and figures of the pages away from the chat
([mobile-app-look.md](../docs/spec/mobile-app-look.md)), licence in
`assets/fonts/LICENSE-OFL-InterTight.txt`.

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

**Signed-in screens are shown with fixed answers.** Times and data change, so
a chat screen is shown in both halves with the same recorded API answers
(`test/parity/fixtures/<name>/`) and the clock stopped at the moment they were
recorded, in UTC. The web half answers the page's requests from them; the app
half's stand-in server does the same (`Fixture` in `test/support.dart`). The
web app's own parts around the chat (the tab bar to Home, Money and Orders;
its banners) are hidden in the web picture: the app does not have them. To
record a fixture, start the demo world and run the recorder:

```sh
bash web/e2e/demo/serve-demo.sh &
node web/e2e/demo/seed.ts
node mobile/tool/capture.mjs chats /chat      # the chats, as Ada
```

## Releasing to Google Play

Play takes an **app bundle** (`.aab`) signed with the **upload key**. Google
then signs what it gives phones with its own app signing key (Play App
Signing). The upload key never goes in git (`key.properties`, `*.jks` and
`*.keystore` are ignored).

1. **Make the upload key once**, on your own computer, and keep it and its
   password safe (a password manager, and a second copy offline). Losing it
   means asking Google to reset it:

   ```sh
   keytool -genkeypair -v -keystore taskiem-upload.jks -alias upload \
     -keyalg RSA -keysize 2048 -validity 10000
   ```

2. **Point the build at it** in `android/key.properties`:

   ```properties
   storeFile=/full/path/to/taskiem-upload.jks
   storePassword=…
   keyAlias=upload
   keyPassword=…
   ```

   In CI the same four values come from `TASKIEM_UPLOAD_STORE_FILE`,
   `TASKIEM_UPLOAD_STORE_PASSWORD`, `TASKIEM_UPLOAD_KEY_ALIAS` and
   `TASKIEM_UPLOAD_KEY_PASSWORD`. Without them a release build is signed with
   the debug key (a warning says so), which is fine on a phone and refused
   by Play.

3. **Build the bundle** for the production server, with a version code one
   higher than the last one uploaded (Play refuses a code it has seen):

   ```sh
   flutter build appbundle --release \
     --dart-define=TASKIEM_URL=https://<production host> \
     --dart-define=TASKIEM_APP_VERSION=1.0.0 \
     --build-name=1.0.0 --build-number=<version code>
   ```

   It is at `build/app/outputs/bundle/release/app-release.aab`.

4. **After the first upload,** copy the **app signing** key's SHA-256 from
   Play Console (Test and release › App integrity) into the server's
   `TASKIEM_ANDROID_APP` ([operations](../docs/operations.md)), beside the
   upload key's, so the server's links open the app and passkeys made in it
   sign in.
