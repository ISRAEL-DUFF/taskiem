// The app half of the parity test (docs/spec/mobile-app.md §4.1): each
// screen the web half kept (test/parity/web/, written by web/e2e/parity.spec.ts)
// drawn by the app at the same size and theme, and compared. A pixel counts
// as different only when nothing within one pixel of it in the other picture
// is close: two engines never draw a letter's edge alike.
//
// On a failure the app's picture and the difference are written to
// build/parity/ beside the web's. Run on Linux, where the web half's
// pictures are taken: macOS draws text with its own engine.

@Tags(['parity'])
library;

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/material.dart' show TextField;
import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/kit/kit.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/sign_in.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

/// How much may differ: under 1% of the screen.
const allowed = 0.01;

/// What the server says when a code is asked for (api/chat_signin.go).
const codeSent = 'If this number is on a Taskiem account, we have sent a code to it on WhatsApp or Telegram.';

/// Signing in against a stand-in for the server, answering as it does.
Auth fakeAuth() {
  final client = MockClient((req) async {
    switch ('${req.method} ${req.url.path}') {
      case 'POST /v1/auth/chat/start':
        return http.Response(jsonEncode({'expires_in': 600, 'status': codeSent}), 202);
      case 'POST /v1/auth/login':
        return http.Response(jsonEncode({'error': 'invalid email or password'}), 401);
    }
    return http.Response(jsonEncode({'error': 'not found'}), 404);
  });
  return Auth(
    transport: Transport(base: 'http://localhost', client: client),
    store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
  );
}

/// A screen of the app, and what a person does to bring it to the state the
/// web half's picture shows (web/e2e/parity.spec.ts, the same steps).
typedef Screen = ({Widget Function() build, Future<void> Function(WidgetTester t) drive});

Future<void> _none(WidgetTester t) async {}

Future<void> _tap(WidgetTester t, String words) async {
  await t.tap(find.text(words));
  await t.pumpAndSettle();
}

/// The app's screens by the web half's names.
final screens = <String, Screen>{
  'sign-in': (build: () => SignInScreen(auth: fakeAuth()), drive: _none),
  'sign-in-chat': (
    build: () => SignInScreen(auth: fakeAuth()),
    drive: (t) => _tap(t, 'Sign in with WhatsApp or Telegram'),
  ),
  'sign-in-chat-code': (
    build: () => SignInScreen(auth: fakeAuth()),
    drive: (t) async {
      await _tap(t, 'Sign in with WhatsApp or Telegram');
      await t.enterText(find.byType(TextField).first, '08031234567');
      await _tap(t, 'Send me a code');
    },
  ),
  'sign-in-wrong': (
    build: () => SignInScreen(auth: fakeAuth()),
    drive: (t) async {
      await t.enterText(find.byType(TextField).at(0), 'ada@parity.test');
      await t.enterText(find.byType(TextField).at(1), 'not the password');
      await _tap(t, 'Sign in');
    },
  ),
};

final kept = RegExp(r'^([a-z-]+)-(light|dark)-(\d+)x(\d+)\.png$');

void main() {
  setUpAll(loadFonts);
  setUp(() => TkField.debugHideCaret = true);
  tearDown(() => TkField.debugHideCaret = false);

  final files = Directory('test/parity/web').listSync().whereType<File>().toList()
    ..sort((a, b) => a.path.compareTo(b.path));
  test('every screen the web half keeps has an app screen', () {
    for (final f in files) {
      final m = kept.firstMatch(f.uri.pathSegments.last);
      expect(m, isNotNull, reason: f.path);
      expect(screens.containsKey(m!.group(1)), isTrue, reason: 'no app screen for ${m.group(1)}');
    }
  });

  for (final f in files) {
    final m = kept.firstMatch(f.uri.pathSegments.last);
    if (m == null || !screens.containsKey(m.group(1))) continue;
    final name = f.uri.pathSegments.last;
    testWidgets('parity: $name', (tester) async {
      final size = Size(double.parse(m.group(3)!), double.parse(m.group(4)!));
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final shot = GlobalKey();
      final mode = m.group(2) == 'dark' ? TkMode.dark : TkMode.light;
      // Shadows as the app draws them (flutter_test makes them solid shapes
      // otherwise), from the first frame; put back before the test ends.
      debugDisableShadows = false;
      await tester.pumpWidget(RepaintBoundary(
        key: shot,
        child: TaskiemApp(mode: mode, home: screens[m.group(1)]!.build()),
      ));
      await tester.pumpAndSettle();
      await screens[m.group(1)]!.drive(tester);
      final boundary = tester.renderObject(find.byKey(shot)) as RenderRepaintBoundary;
      final result = await tester.runAsync(() async {
        final app = await boundary.toImage();
        final a = await _rgba(app);
        final w = await _rgba(await _decode(f.readAsBytesSync()));
        final share = differs(a, w, app.width, app.height);
        if (share >= allowed) {
          final out = Directory('build/parity')..createSync(recursive: true);
          final png = await app.toByteData(format: ui.ImageByteFormat.png);
          File('${out.path}/app-$name').writeAsBytesSync(png!.buffer.asUint8List());
          f.copySync('${out.path}/web-$name');
        }
        return share;
      });
      debugDisableShadows = true;
      expect(result, lessThan(allowed),
          reason: '$name: the app looks different from the web (${(result! * 100).toStringAsFixed(2)}% of the screen); see build/parity/');
    });
  }
}

Future<ui.Image> _decode(Uint8List png) async {
  final codec = await ui.instantiateImageCodec(png);
  return (await codec.getNextFrame()).image;
}

Future<Uint8List> _rgba(ui.Image i) async =>
    (await i.toByteData(format: ui.ImageByteFormat.rawRgba))!.buffer.asUint8List();

/// The share of pixels that differ, as web/e2e/parity.spec.ts counts them.
double differs(Uint8List a, Uint8List b, int w, int h) {
  if (a.length != b.length) return 1;
  bool near(Uint8List s, Uint8List t, int i, int j) {
    final o = (j * w + i) * 4;
    for (var dy = -1; dy <= 1; dy++) {
      for (var dx = -1; dx <= 1; dx++) {
        final ii = i + dx, jj = j + dy;
        if (ii < 0 || jj < 0 || ii >= w || jj >= h) continue;
        final k = (jj * w + ii) * 4;
        if ((s[o] - t[k]).abs() <= 24 && (s[o + 1] - t[k + 1]).abs() <= 24 && (s[o + 2] - t[k + 2]).abs() <= 24) {
          return true;
        }
      }
    }
    return false;
  }

  var n = 0;
  for (var j = 0; j < h; j++) {
    for (var i = 0; i < w; i++) {
      if (!near(a, b, i, j) && !near(b, a, i, j)) n++;
    }
  }
  return n / (w * h);
}
