// The screenshot tests (mobile/CLAUDE.md, "When a screen is done"): how the
// app's redesigned screens look, kept as pictures in test/looks/ and
// compared on every run, so a change that spoils a screen's look fails.
//
// A test calls `await looks(t, 'name')` where its screen is as a person
// would see it, with the test's own data. The screen is taken in the theme
// the test shows, then the app is switched to the other theme, taken again,
// and switched back, so the test carries on as before: every picture is
// checked light and dark.
//
// After a change to a screen's look made on purpose, write its pictures
// again and look at them before committing:
//
//   flutter test --update-goldens test/today_tab_test.dart
//
// Pictures are compared on Linux only (the CI's machine; macOS draws text
// with its own engine), as the parity test is. A pixel counts as different
// when any of its colours is more than a shade off, and a screen fails when
// more than [looksAllowed] pixels differ. On a failure
// the new picture and one marking what differs are written to build/looks/.

import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/rendering.dart' show OffsetLayer;
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

/// How many pixels may differ: a stray one or two. The same screen drawn
/// again differs by none; one letter added to a row's title, by 54.
const looksAllowed = 8;

/// The tallest picture kept: a long page is kept to its first screens.
const _tallest = 2000.0;

/// Takes the screen as it is now, in both themes, and compares each with
/// its picture in test/looks/ (or writes it, with --update-goldens). The
/// test's root widget must be the [TaskiemApp] it pumped.
Future<void> looks(WidgetTester t, String name) async {
  final app = t.widget<TaskiemApp>(find.byType(TaskiemApp).first);
  final first = app.mode;
  final other = first == TkMode.light ? TkMode.dark : TkMode.light;
  final shadows = debugDisableShadows;
  // Shadows as the app draws them (flutter_test makes them solid otherwise).
  debugDisableShadows = false;
  try {
    await _settle(t);
    await _check(t, '$name-${first.name}');
    await t.pumpWidget(_withMode(app, other));
    await _settle(t);
    await _check(t, '$name-${other.name}');
  } finally {
    await t.pumpWidget(_withMode(app, first));
    await _settle(t);
    debugDisableShadows = shadows;
  }
}

TaskiemApp _withMode(TaskiemApp a, TkMode mode) => TaskiemApp(
      key: a.key,
      mode: mode,
      home: a.home,
      auth: a.auth,
      unlocker: a.unlocker,
      prefs: a.prefs,
      notify: a.notify,
      links: a.links,
      now: a.now,
    );

/// Long enough for what rises in and counts up to finish; not
/// pumpAndSettle, which a shimmer or a typing dot would never let end.
Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 4; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    await t.pump(const Duration(milliseconds: 500));
  }
}

/// Store listings (mobile/README.md, "Releasing to Google Play"): with
/// STORE_DIR set, each screen is also written there at a phone's sharpness
/// (three pixels to a point), not compared with anything.
Future<void> _store(WidgetTester t, String name) async {
  final dir = Platform.environment['STORE_DIR'];
  if (dir == null || dir.isEmpty) return;
  await t.runAsync(() async {
    final view = t.binding.renderViews.first;
    final size = view.paintBounds;
    // ignore: invalid_use_of_protected_member
    final layer = view.debugLayer! as OffsetLayer;
    final image = await layer.toImage(Rect.fromLTWH(0, 0, size.width, size.height.clamp(0, _tallest)), pixelRatio: 3);
    final png = await image.toByteData(format: ui.ImageByteFormat.png);
    Directory(dir).createSync(recursive: true);
    File('$dir/$name.png').writeAsBytesSync(png!.buffer.asUint8List());
  });
}

Future<void> _check(WidgetTester t, String name) async {
  await _store(t, name);
  final file = File('test/looks/$name.png');
  final shot = await t.runAsync(() async {
    final view = t.binding.renderViews.first;
    final size = view.paintBounds;
    final bounds = Rect.fromLTWH(0, 0, size.width, size.height.clamp(0, _tallest));
    // ignore: invalid_use_of_protected_member
    final layer = view.debugLayer! as OffsetLayer;
    final image = await layer.toImage(bounds);
    final png = (await image.toByteData(format: ui.ImageByteFormat.png))!.buffer.asUint8List();
    final rgba = (await image.toByteData(format: ui.ImageByteFormat.rawRgba))!.buffer.asUint8List();
    return (png: png, rgba: rgba, w: image.width, h: image.height);
  });
  if (autoUpdateGoldenFiles) {
    file.parent.createSync(recursive: true);
    file.writeAsBytesSync(shot!.png);
    return;
  }
  if (!Platform.isLinux) return;
  expect(file.existsSync(), isTrue, reason: 'no picture for $name in test/looks/: write it with `flutter test --update-goldens <this test>`, and look at it');
  final kept = await t.runAsync(() async {
    final codec = await ui.instantiateImageCodec(file.readAsBytesSync());
    final image = (await codec.getNextFrame()).image;
    return (rgba: (await image.toByteData(format: ui.ImageByteFormat.rawRgba))!.buffer.asUint8List(), w: image.width, h: image.height);
  });
  final same = kept!.w == shot!.w && kept.h == shot.h;
  final marks = same ? Uint8List.fromList(shot.rgba) : null;
  final n = same ? _differs(shot.rgba, kept.rgba, shot.w, shot.h, marks!) : shot.w * shot.h;
  if (n > looksAllowed) {
    final out = Directory('build/looks')..createSync(recursive: true);
    File('${out.path}/$name-new.png').writeAsBytesSync(shot.png);
    file.copySync('${out.path}/$name-kept.png');
    if (marks != null) {
      await t.runAsync(() async {
        final buffer = await ui.ImmutableBuffer.fromUint8List(marks);
        final desc = ui.ImageDescriptor.raw(buffer, width: shot.w, height: shot.h, pixelFormat: ui.PixelFormat.rgba8888);
        final image = (await (await desc.instantiateCodec()).getNextFrame()).image;
        final png = await image.toByteData(format: ui.ImageByteFormat.png);
        File('${out.path}/$name-differs.png').writeAsBytesSync(png!.buffer.asUint8List());
      });
    }
  }
  expect(
    n,
    lessThanOrEqualTo(looksAllowed),
    reason: same
        ? '$name looks different from its picture ($n pixels); see build/looks/. '
            'If the change is meant, write the picture again with --update-goldens and look at it.'
        : '$name is a different size (${shot.w}×${shot.h}, its picture ${kept.w}×${kept.h}); see build/looks/',
  );
}

/// How many pixels differ by more than a shade in any colour;
/// each one painted red in [marks]. The same engine drew both pictures, so
/// nothing near is let pass, as it is between the web and the app.
int _differs(Uint8List a, Uint8List b, int w, int h, Uint8List marks) {
  var n = 0;
  for (var k = 0; k < w * h * 4; k += 4) {
    if ((a[k] - b[k]).abs() > 8 || (a[k + 1] - b[k + 1]).abs() > 8 || (a[k + 2] - b[k + 2]).abs() > 8) {
      n++;
      marks
        ..[k] = 255
        ..[k + 1] = 0
        ..[k + 2] = 0
        ..[k + 3] = 255;
    }
  }
  return n;
}
