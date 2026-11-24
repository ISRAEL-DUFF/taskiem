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

## What works
- The real app's own screens and animations (counts up, charts draw, sheets
  rise), in light or dark, on a phone-sized screen at 3 px to a point.
- Deterministic and repeatable: re-run when the app changes.
- Runs here (and in CI); no phone or emulator.

## Not yet
- A tap indicator (the lime ring our web recordings draw).
- A phone frame, status bar and keyboard (a test runner draws none).
- Live data: this uses recorded answers. The app's tests against the real
  server (`mobile/test/server/`) could feed it the demo world.
- A reusable harness: each video would copy a screen test's set-up. Better: a
  small library of "filming" helpers (open a screen, tap by label, hold, film).
- Things that need a real phone (push notifications, passkey, camera, scan):
  record those on a device (Android emulator or phone).
