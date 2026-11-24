# Recording the phone app's own screens

Proof of method, 24 November 2026. The app's screens are drawn by Flutter's
test runner on Linux (the way the Google Play screenshots were made), one
picture every 33 ms of the app's own clock, then joined into an MP4.

```sh
# Flutter 3.47.9 (the CI's version and checksum, .github/workflows/mobile.yml)
cd mobile && flutter pub get --enforce-lockfile
VIDEO_DIR=/tmp/frames flutter test test/video_proof_test.dart
ffmpeg -framerate 30 -i /tmp/frames/f%04d.png \
  -vf "scale=1080:2336:flags=lanczos,format=yuv420p" -c:v libx264 -crf 18 out.mp4
```

`mobile/test/video_proof_test.dart` copies the Today tab's test set-up (recorded
server answers, `today_tab_test.dart`), then scripts: open, wait, tap Sales,
tap a row. It is skipped unless `VIDEO_DIR` is set.

## How it works now (24 November 2026)

Three parts:

1. **`mobile/test/film.dart`**, the helper a film uses: `Film.start`, `play`,
   `hold`, `tap(finder)`, `drag(finder, by)`, `say(text, who:)`, `settle`,
   `finish`. It sets a 390 x 844 phone with the status bar (47 pt) and gesture
   bar (24 pt) kept clear, writes a picture every 33 ms at 3 px to a point, and
   `cues.json` (where and when each tap, drag and caption was).
2. **A film**, a test that sets the screen up and drives it:
   `mobile/test/film_today_test.dart` is the first. Skipped unless `VIDEO_DIR`
   is set.
3. **`video/phone/compose.py`**, which puts the frames into a phone and draws the
   tap ring, status bar, captions and joins them into an MP4.

```sh
export PATH=<flutter 3.47.9>/bin:$PATH
cd mobile && VIDEO_DIR=/tmp/films flutter test test/film_today_test.dart
cd .. && python3 video/phone/compose.py --frames /tmp/films/today \
    --out video/out/today.mp4 --captions            # 1080x1920, light theme
python3 video/phone/compose.py --frames /tmp/films/today --out frame.png --still 4.5   # one picture
```

Options: `--canvas 1920x1080`, `--theme dark`, `--time 13:00`, `--bg "#efeae2"`,
`--phone-height`, `--top`, `--captions`, `--keep DIR`.

**The phone** is a generic Android flagship (no maker's name): a graphite metal
frame with a bright edge and antenna breaks, a volume rocker and power key on
the right, a black glass border, a rounded screen, a punch-hole camera, a status
bar (time, signal, wifi, battery), a gesture bar, a glass sheen, and a soft
shadow. **The tap ring** is lime with a dark halo and a fingertip dot, and
grows from where each tap lands 0.2 s before the app reacts.

## To add a film

Copy `film_today_test.dart`, keep its set-up (or copy the set-up of the screen
you are filming from that screen's test), and replace the `film:` test with
your sequence. Check with `flutter analyze test/<your film>`.

## What works
- The real app's own screens and animations (counts up, charts draw, sheets
  rise), in light or dark, on a phone-sized screen at 3 px to a point.
- Deterministic and repeatable: re-run when the app changes.
- Runs here (and in CI); no phone or emulator.

## Not yet
- The on-screen keyboard (a test runner draws none): a film that types needs one drawn in the compositor.
- Live data: this uses recorded answers. The app's tests against the real
  server (`mobile/test/server/`) could feed it the demo world.
- Shared set-ups: each film still copies a screen test's set-up (its recorded
  server answers). A small library of set-ups per screen would help.
- Things that need a real phone (push notifications, passkey, camera, scan):
  record those on a device (Android emulator or phone).
