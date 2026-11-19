// A throwaway preview: draws screens to PNG files, light and dark, at the
// top and scrolled down, so they can be looked at. Not committed.
//
//   cp .claude/skills/mobile-screen/scripts/preview_test.dart mobile/test/zz_preview_test.dart
//   (edit `screens` and `answer` below)
//   cd mobile && flutter test test/zz_preview_test.dart --dart-define=OUT=/abs/out/dir
//   rm test/zz_preview_test.dart
//
// Files: OUT/<name>-<light|dark>-<0|1>.png (1 = scrolled down), 340×800 at
// twice the pixels.

import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/kit/kit.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

// Import the screens you draw, e.g.:
// import 'package:taskiem/screens/you.dart';

const _out = String.fromEnvironment('OUT', defaultValue: '/tmp/preview');

http.Response ok(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

/// What the stand-in server answers beyond the recorded 'chats' fixture
/// (test/parity/fixtures/chats). Give every field the API marks required
/// (api/openapi/openapi.yaml), or the screen shows "Something went wrong".
Future<http.Response?> answer(http.Request req) async => switch ('${req.method} ${req.url.path}') {
      // 'GET /v1/me/businesses' => ok({'businesses': [...]}),
      _ => null,
    };

/// The screens to draw, by name.
final screens = <String, Widget Function(ChatsData c)>{
  // 'settings': (c) => YouSettingsScreen(auth: c.auth),
};

void main() {
  setUpAll(loadFonts);
  setUp(() => TkField.debugHideCaret = true);
  for (final mode in TkMode.values) {
    for (final e in screens.entries) {
      for (final down in [false, true]) {
        testWidgets('preview ${e.key} ${mode.name} ${down ? 'down' : 'top'}', (t) async {
          t.view.physicalSize = const Size(340 * 2, 800 * 2);
          t.view.devicePixelRatio = 2;
          addTearDown(t.view.reset);
          debugDisableShadows = false;
          final f = Fixture('chats');
          final chats = ChatsData(f.auth(also: answer), now: () => f.at);
          final shot = GlobalKey();
          await t.pumpWidget(RepaintBoundary(key: shot, child: TaskiemApp(mode: mode, home: e.value(chats))));
          // Real futures (the stand-in server) need real time.
          for (var i = 0; i < 8; i++) {
            await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
            await t.pump();
          }
          await t.pump(const Duration(seconds: 2));
          if (down) {
            final s = find.byWidgetPredicate((w) => w is Scrollable && w.axisDirection == AxisDirection.down);
            if (s.evaluate().isNotEmpty) {
              final st = t.state<ScrollableState>(s.first);
              st.position.jumpTo(st.position.maxScrollExtent);
              await t.pump(const Duration(seconds: 1));
            }
          }
          final b = t.renderObject(find.byKey(shot)) as RenderRepaintBoundary;
          await t.runAsync(() async {
            final img = await b.toImage(pixelRatio: 2);
            final png = await img.toByteData(format: ui.ImageByteFormat.png);
            Directory(_out).createSync(recursive: true);
            File('$_out/${e.key}-${mode.name}-${down ? 1 : 0}.png').writeAsBytesSync(png!.buffer.asUint8List());
          });
          debugDisableShadows = true;
        });
      }
    }
  }
}
