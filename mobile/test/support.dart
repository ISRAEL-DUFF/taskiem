import 'dart:ui' as ui;
import 'dart:convert';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/menu.dart' show SelectionBar, SelectionHead;
import 'package:taskiem/chat/thread.dart' show ThreadView, ThreadViewState;
import 'package:taskiem/theme/theme.dart';

/// Loads the app's own fonts, so a test draws the web's Inter, not the test
/// font.
Future<void> loadFonts() async {
  const files = {
    'Inter': 'Inter-latin.ttf',
    'InterLatinExt': 'Inter-latin-ext.ttf',
    'InterVietnamese': 'Inter-vietnamese.ttf',
    'InterGreek': 'Inter-greek.ttf',
    'InterGreekExt': 'Inter-greek-ext.ttf',
    'InterCyrillic': 'Inter-cyrillic.ttf',
    'InterCyrillicExt': 'Inter-cyrillic-ext.ttf',
  };
  assert([...files.keys.skip(1), 'NotoColorEmoji'].toString() == tkFallback.toString());
  // The emoji as Chromium draws them here, when the machine has the font.
  final emoji = File('/usr/share/fonts/truetype/noto/NotoColorEmoji.ttf');
  if (emoji.existsSync()) {
    await (FontLoader('NotoColorEmoji')..addFont(Future.value(ByteData.sublistView(emoji.readAsBytesSync())))).load();
  }
  for (final e in files.entries) {
    final loader = FontLoader(e.key)
      ..addFont(Future.value(ByteData.sublistView(File('assets/fonts/${e.value}').readAsBytesSync())));
    await loader.load();
  }
  final tight = FontLoader('InterTight');
  for (final w in const [500, 600, 700]) {
    tight.addFont(Future.value(ByteData.sublistView(File('assets/fonts/InterTight-$w.ttf').readAsBytesSync())));
  }
  await tight.load();
}

/// A parity fixture (`test/parity/fixtures/NAME`, recorded from the demo
/// world by tool/capture.mjs): its answers by path, and when they were taken.
class Fixture {
  Fixture(this.name) {
    final j = jsonDecode(File('$dir/answers.json').readAsStringSync()) as Map<String, dynamic>;
    at = DateTime.parse(j['at'] as String);
    answers = (j['answers'] as Map<String, dynamic>).cast<String, Map<String, dynamic>>();
  }

  final String name;
  late final DateTime at;
  late final Map<String, Map<String, dynamic>> answers;

  String get dir => 'test/parity/fixtures/$name';

  /// The recorded answer for a request, as the web half finds it.
  Map<String, dynamic>? answerFor(Uri u) => answers[u.path + (u.query.isEmpty ? '' : '?${u.query}').replaceAll(RegExp(r'after=[^&]*'), 'after=*')];

  /// A stand-in server answering from the fixture; anything else is 404,
  /// unless [also] answers it first (a test's sends).
  http.Client client({Future<http.Response?> Function(http.Request req)? also}) => MockClient((req) async {
        final own = await also?.call(req);
        if (own != null) return own;
        // What the screen sends (a read mark) is taken, as the web half
        // takes it.
        if (req.method != 'GET') return http.Response('{}', 200, headers: {'content-type': 'application/json'});
        final a = answerFor(req.url);
        if (a == null) return http.Response(jsonEncode({'error': 'not in the fixture'}), 404);
        return http.Response.bytes(File('$dir/${a['file']}').readAsBytesSync(), a['status'] as int, headers: {'content-type': a['type'] as String});
      });

  /// A recorded answer's JSON.
  Map<String, dynamic> json(String path) => jsonDecode(File('$dir/${answers[path]!['file']}').readAsStringSync()) as Map<String, dynamic>;

  /// Signed in as the fixture's person, who-am-I read from it.
  Auth auth({Future<http.Response?> Function(http.Request req)? also}) {
    final a = Auth(
      transport: Transport(base: 'http://localhost', token: 'fixture', client: client(also: also)),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    final me = answers['/v1/me'];
    if (me != null) a.me = Me.fromJson(jsonDecode(File('$dir/${me['file']}').readAsStringSync()) as Map<String, dynamic>);
    a.state = AuthState.signedIn;
    return a;
  }
}

/// SNAP: the screen as a picture, when SNAP_DIR is set (looking at pages
/// while changing their look); nothing otherwise.
Future<void> snap(dynamic t, String name) async {
  final dir = Platform.environment['SNAP_DIR'];
  if (dir == null) return;
  // ignore: avoid_dynamic_calls
  await t.pump(const Duration(seconds: 1));
  // ignore: avoid_dynamic_calls
  await t.runAsync(() async {
    // ignore: avoid_dynamic_calls
    final rv = t.binding.renderViews.first;
    // ignore: avoid_dynamic_calls
    final img = await (rv.debugLayer as dynamic).toImage(rv.paintBounds, pixelRatio: 1.0);
    // ignore: avoid_dynamic_calls
    final png = await img.toByteData(format: ui.ImageByteFormat.png);
    // ignore: avoid_dynamic_calls
    File('$dir/$name.png').writeAsBytesSync(png!.buffer.asUint8List());
  });
}

/// What the selection bar offers for the messages held now (a message
/// held is selected, as WhatsApp's): its buttons, then those under its ⋮.
List<String> heldActions(WidgetTester t) {
  final head = find.byType(SelectionHead);
  if (head.evaluate().isEmpty) return const [];
  return [for (final a in t.widget<SelectionHead>(head.first).selection.actions()) a.label];
}

/// Chooses [label] for the messages held: its button on the selection bar,
/// or from under the bar's ⋮.
Future<void> chooseHeld(WidgetTester t, String label) async {
  final bar = find.byType(SelectionBar);
  expect(bar, findsOneWidget, reason: 'a message held');
  final button = find.descendant(of: bar, matching: find.bySemanticsLabel(label));
  if (button.evaluate().isNotEmpty) {
    await t.tap(button.first);
  } else {
    await t.tap(find.descendant(of: bar, matching: find.bySemanticsLabel('More options')));
    await t.pumpAndSettle();
    await t.tap(find.text(label).last);
  }
  await t.pump();
}

/// The chat's thread moved until [f] is built and in sight: the thread
/// builds only what is near the view, as WhatsApp's. Found where it is now,
/// it stays; else from the top of the thread down.
Future<void> seeInThread(WidgetTester t, Finder f) async {
  if (f.evaluate().isNotEmpty) return;
  final thread = t.state<ThreadViewState<dynamic>>(find.byWidgetPredicate((w) => w is ThreadView).first);
  final s = thread.scroll;
  // Its top: measured as it comes near, so found in a few steps.
  for (var i = 0; i < 20 && f.evaluate().isEmpty; i++) {
    final top = s.position.minScrollExtent;
    s.jumpTo(top);
    await t.pump();
    if (s.position.minScrollExtent == top) break;
  }
  for (var i = 0; i < 200 && f.evaluate().isEmpty && s.position.pixels < s.position.maxScrollExtent; i++) {
    s.jumpTo((s.position.pixels + 300).clamp(s.position.minScrollExtent, s.position.maxScrollExtent));
    await t.pump();
  }
  // Once it is built, its whole self in sight.
  if (f.evaluate().isNotEmpty) await t.ensureVisible(f.first);
  await t.pump();
}
