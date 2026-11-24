// Filming the app's own screens, for the explainer and how-to videos
// (video/recording-app.md). A film is a test that sets the screen up as the
// app's other tests do, then drives it with a [Film]: tap, drag, hold, say.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_today_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/today --out today.mp4
//
// A film writes a picture every 33 ms of the app's own clock (30 frames a
// second, two pixels to a point) and `cues.json`, which says where and when
// each tap, drag and caption was, so the phone, the status bar, the tap ring
// and the captions are drawn afterwards (video/phone/compose.py). Without
// VIDEO_DIR a film is skipped: it never slows the normal test run.
//
// The screen is a 390 x 844 phone with a status bar (47 points) and a
// gesture bar (24 points) kept clear, so the app lays out as on a real one;
// compose.py draws both.

import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/rendering.dart' show OffsetLayer;
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';

/// Films run only when VIDEO_DIR is set.
final bool filming = (Platform.environment['VIDEO_DIR'] ?? '').isNotEmpty;

/// VIDEO_DRY=1: play the script without writing pictures (to check it runs).
final bool _dry = (Platform.environment['VIDEO_DRY'] ?? '').isNotEmpty;

const double phoneWidth = 390;
const double phoneHeight = 844;
const double statusBar = 47;
const double gestureBar = 24;
const int fps = 30;

class Film {
  Film._(this.t, this.dir, this.name, this._shadows);

  final WidgetTester t;
  final String dir;
  final String name;
  final bool _shadows;
  int _n = 0;
  final List<Map<String, Object?>> _cues = [];

  /// Starts a film called [name]: the screen set to a phone's, the app's
  /// shadows drawn as it draws them (flutter_test makes them solid).
  /// Call after the app is pumped; call [finish] last.
  static Film start(WidgetTester t, String name) {
    final root = Platform.environment['VIDEO_DIR']!;
    final dir = '$root/$name';
    final old = Directory(dir);
    if (old.existsSync()) old.deleteSync(recursive: true);
    old.createSync(recursive: true);
    final saved = debugDisableShadows;
    debugDisableShadows = false;
    t.view.physicalSize = const Size(phoneWidth, phoneHeight);
    t.view.devicePixelRatio = 1;
    const pad = FakeViewPadding(top: statusBar, bottom: gestureBar);
    t.view.padding = pad;
    t.view.viewPadding = pad;
    return Film._(t, dir, name, saved);
  }

  /// Seconds into the film.
  double get now => _n / fps;

  /// Plays [seconds] of the app as it is: animations run, a picture each frame.
  Future<void> play(double seconds) async {
    final frames = (seconds * fps).round();
    for (var i = 0; i < frames; i++) {
      await _step();
    }
  }

  /// A beat with nothing moving: the same as [play], named for what it is for.
  Future<void> hold(double seconds) => play(seconds);

  /// Lets what the app is waiting for (an answer from the recorded server)
  /// come in and finish animating, without a picture: use it only where a
  /// real person would see nothing change (a bar already full).
  Future<void> settle([int rounds = 6]) async {
    for (var i = 0; i < rounds; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 1000));
  }

  /// A finger touches [target]: a ring grows there, and 0.2 s later the app
  /// takes the tap. [then] plays on afterwards (the app's answer).
  Future<void> tap(Finder target, {double then = 0.0}) async {
    final at = t.getCenter(target.first);
    _cues.add({'kind': 'tap', 't': now, 'x': at.dx, 'y': at.dy});
    await play(0.2);
    await t.tap(target.first, warnIfMissed: false);
    if (then > 0) await play(then);
  }

  /// A finger drags on [target] by [by] over [seconds] (to scroll).
  Future<void> drag(Finder target, Offset by, {double seconds = 0.6}) async {
    final from = t.getCenter(target.first);
    final frames = (seconds * fps).round();
    _cues.add({'kind': 'drag', 't': now, 'x': from.dx, 'y': from.dy, 'x2': from.dx + by.dx, 'y2': from.dy + by.dy, 'seconds': seconds});
    final g = await t.startGesture(from);
    for (var i = 0; i < frames; i++) {
      await g.moveBy(by / frames.toDouble());
      await _step();
    }
    await g.up();
  }


  /// The phone's keyboard slides up ([show]) or away: the app's layout moves
  /// with it as on a real phone, and compose.py draws the keys. [kind] is
  /// 'number' or 'text'. With [swap], the keyboard stays up and changes kind
  /// at once (a person taps ?123).
  Future<void> keyboard(bool show, {String kind = 'number', double height = 290, bool swap = false}) async {
    _cues.add({'kind': 'keyboard', 't': now, 'show': show, 'type': kind, 'height': height, if (swap) 'instant': true});
    if (swap) return;
    const frames = 8;
    for (var i = 1; i <= frames; i++) {
      final u = Curves.easeOutCubic.transform(i / frames);
      t.view.viewInsets = FakeViewPadding(bottom: height * (show ? u : 1 - u));
      await _step();
    }
    if (!show) t.view.resetViewInsets();
  }

  /// Types [text] into [field] a key at a time, each key lit on the keyboard
  /// (a capital lights its letter).
  Future<void> type(Finder field, String text, {double perKey = 0.2}) async {
    for (var i = 1; i <= text.length; i++) {
      _cues.add({'kind': 'key', 't': now, 'key': text[i - 1]});
      await t.enterText(field, text.substring(0, i));
      await play(perKey);
    }
  }

  /// The phone's own dialog over the app (a fingerprint check): drawn by
  /// compose.py from [name] ('biometric'), shown for [seconds].
  void system(String name, double seconds, {String? title, String? line}) {
    _cues.add({'kind': 'system', 't': now, 'name': name, 'seconds': seconds, 'title': ?title, 'line': ?line});
  }

  /// A caption from now until the next one (or the end), with [who] over it.
  void say(String text, {String? who}) {
    _cues.add({'kind': 'say', 't': now, 'text': text, 'who': ?who});
  }

  /// Ends the film: the screen put back, `cues.json` written.
  Future<void> finish() async {
    debugDisableShadows = _shadows;
    t.view.resetPadding();
    t.view.resetViewPadding();
    t.view.resetViewInsets();
    File('$dir/cues.json').writeAsStringSync(const JsonEncoder.withIndent(' ').convert({
      'name': name,
      'fps': fps,
      'frames': _n,
      'width': phoneWidth,
      'height': phoneHeight,
      'statusBar': statusBar,
      'gestureBar': gestureBar,
      'cues': _cues,
    }));
  }

  Future<void> _step() async {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 2)));
    await t.pump(const Duration(milliseconds: 1000 ~/ fps));
    if (_dry) {
      _n++;
      return;
    }
    await t.runAsync(() async {
      final view = t.binding.renderViews.first;
      // ignore: invalid_use_of_protected_member
      final layer = view.debugLayer! as OffsetLayer;
      final image = await layer.toImage(const Rect.fromLTWH(0, 0, phoneWidth, phoneHeight), pixelRatio: 2);
      final png = await image.toByteData(format: ui.ImageByteFormat.png);
      File('$dir/f${(_n).toString().padLeft(5, '0')}.png').writeAsBytesSync(png!.buffer.asUint8List());
      image.dispose();
    });
    _n++;
  }
}
