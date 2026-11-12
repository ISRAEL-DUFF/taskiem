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
import 'package:taskiem/kit/list.dart' show TkRow;
import 'package:taskiem/main.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/attachments.dart';
import 'package:taskiem/chat/composer.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/deal.dart';
import 'package:taskiem/chat/customer.dart';
import 'package:taskiem/chat/alerts.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/screens/sign_in.dart';
import 'package:taskiem/screens/sign_up.dart';
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
      case 'GET /v1/signup':
        // As web/e2e/serve.sh's server: open, with no terms to agree to.
        return http.Response(jsonEncode({'enabled': true}), 200);
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
  final f = find.text(words);
  // Scrolled to only when it is off the screen (large text pushes it down):
  // where it is in sight the picture stays as the web took it.
  final c = t.getCenter(f);
  final size = t.view.physicalSize / t.view.devicePixelRatio;
  if (c.dy < 0 || c.dy > size.height || c.dx < 0 || c.dx > size.width) {
    await t.ensureVisible(f);
    await t.pumpAndSettle();
  }
  await t.tap(f);
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
  'chats': (
    build: () {
      final f = Fixture('chats');
      return ChatsScreen(data: ChatsData(f.auth(), now: () => f.at));
    },
    drive: (t) async {
      // The logos: fetched and decoded for real, outside the test's clock.
      for (var i = 0; i < 5; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
        await t.pump();
      }
      await t.pumpAndSettle();
    },
  ),
  'chats-starred': (
    build: () {
      final f = Fixture('chats-starred');
      return ChatsScreen(data: ChatsData(f.auth(), now: () => f.at));
    },
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel(RegExp('^Starred')));
      await _loaded(t);
    },
  ),
  'chats-search': (
    build: () {
      final f = Fixture('chats-search');
      return ChatsScreen(data: ChatsData(f.auth(), now: () => f.at));
    },
    drive: (t) async {
      await _loaded(t);
      await t.enterText(find.byType(EditableText), 'rice');
      // The search waits 150 ms for the typing to stop.
      await t.pump(const Duration(milliseconds: 200));
      await _loaded(t);
      FocusManager.instance.primaryFocus?.unfocus();
      await t.pumpAndSettle();
    },
  ),
  'explore': (
    build: () {
      final f = Fixture('explore');
      return ChatsScreen(data: ChatsData(f.auth(), now: () => f.at));
    },
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('Find a business to link with'));
      await _loaded(t);
      await _loaded(t);
    },
  ),
  'explore-search': (
    build: () {
      final f = Fixture('explore');
      return ChatsScreen(data: ChatsData(f.auth(), now: () => f.at));
    },
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('Find a business to link with'));
      await _loaded(t);
      await t.enterText(find.byType(EditableText), 'rice');
      // Searched 250 ms after the typing stops.
      await t.pump(const Duration(milliseconds: 300));
      await _loaded(t);
      await _loaded(t);
      FocusManager.instance.primaryFocus?.unfocus();
      await t.pumpAndSettle();
    },
  ),
  'chat-tunde': (
    build: () {
      final f = Fixture('chat-tunde');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == "Mama Tunde's Market");
      return ConversationScreen(data: ConversationData(chats, conv, follow: false));
    },
    drive: (t) async {
      for (var i = 0; i < 5; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
        await t.pump();
      }
      await t.pumpAndSettle();
    },
  ),
  'chat-bayo': (
    build: () {
      final f = Fixture('chat-bayo');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
      return ConversationScreen(data: ConversationData(chats, conv, follow: false));
    },
    drive: (t) async {
      for (var i = 0; i < 5; i++) {
        await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
        await t.pump();
      }
      await t.pumpAndSettle();
    },
  ),
  'chat-files': (build: () => _conversation('chat-files', "Mama Tunde's Market"), drive: _loaded),
  'chat-files-end': (
    build: () => _conversation('chat-files', "Mama Tunde's Market"),
    drive: (t) async {
      await _loaded(t);
      // As the web half scrolls the thread to its end.
      final s = t.widget<SingleChildScrollView>(find.byType(SingleChildScrollView)).controller!;
      s.jumpTo(s.position.maxScrollExtent);
      await t.pumpAndSettle();
    },
  ),
  'chat-attach': (
    build: () => _conversation('chat-files', "Mama Tunde's Market"),
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('Attach'));
      await t.pumpAndSettle();
    },
  ),
  'chat-caption': (
    build: () => _conversation('chat-files', "Mama Tunde's Market"),
    drive: (t) async {
      await _loaded(t);
      // The fixture's own photo, as the web half chooses it.
      final bytes = File('test/parity/fixtures/chat-files/025.img').readAsBytesSync();
      t.state<ComposerState>(find.byType(Composer)).choose(PickedFile(bytes: bytes, name: 'tomatoes.jpg', type: 'image/jpeg'));
      await _loaded(t);
    },
  ),
  'chat-taskiem': (
    build: () {
      final f = Fixture('chat-taskiem');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      return TaskiemScreen(data: TaskiemData(chats, follow: false));
    },
    drive: _loaded,
  ),
  // The tools panel over Taskiem's conversation (M5), and a tool's sheet.
  'chat-tools': (
    build: () {
      final f = Fixture('chat-tools');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      return TaskiemScreen(data: TaskiemData(chats, follow: false));
    },
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('Open tools'));
      await _loaded(t);
    },
  ),
  'chat-tool-sheet': (
    build: () {
      final f = Fixture('chat-tools');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      return TaskiemScreen(data: TaskiemData(chats, follow: false));
    },
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('Open tools'));
      await _loaded(t);
      await t.tap(find.bySemanticsLabel(RegExp('^Products')));
      await _loaded(t);
    },
  ),
  'chat-product': (
    build: () {
      final f = Fixture('chat-product');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      return TaskiemScreen(data: TaskiemData(chats, follow: false));
    },
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('Open tools'));
      await _loaded(t);
      await t.tap(find.bySemanticsLabel(RegExp('^Products')));
      await _loaded(t);
      await t.tap(find.byType(TkRow).first);
      await _loaded(t);
    },
  ),
  'chat-sell': (
    build: () {
      final f = Fixture('chat-sell');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      return TaskiemScreen(data: TaskiemData(chats, follow: false));
    },
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('Open tools'));
      await _loaded(t);
      await t.tap(find.bySemanticsLabel(RegExp('^Sell')));
      await _loaded(t);
      await t.tap(find.bySemanticsLabel(RegExp('^One more ')).first);
      await _loaded(t);
    },
  ),
  'chat-order': (build: () => _conversation('chat-order', 'Bayo Rice Mills'), drive: (t) => _shop(t)),
  'chat-order-review': (build: () => _conversation('chat-order', 'Bayo Rice Mills'), drive: (t) => _shop(t, review: true)),
  'chat-taskiem-cards': (
    build: () {
      final f = Fixture('chat-taskiem');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      return TaskiemScreen(data: TaskiemData(chats, follow: false));
    },
    drive: (t) async {
      await _loaded(t);
      // As the web half: the thread alone scrolled, the briefing at its top.
      final thread = t.state<ThreadViewState<TaskiemLine>>(find.byType(ThreadView<TaskiemLine>));
      final view = t.renderObject<RenderBox>(find.byType(SingleChildScrollView));
      final card = t.renderObject<RenderBox>(find.byType(BriefingCard));
      final top = card.localToGlobal(Offset.zero, ancestor: view).dy;
      thread.scroll.jumpTo(thread.scroll.offset + top);
      await t.pumpAndSettle();
    },
  ),
  'chat-taskiem-approval': (
    build: () {
      final f = Fixture('chat-taskiem');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      return TaskiemScreen(data: TaskiemData(chats, follow: false));
    },
    drive: (t) async {
      await _loaded(t);
      final thread = t.state<ThreadViewState<TaskiemLine>>(find.byType(ThreadView<TaskiemLine>));
      final view = t.renderObject<RenderBox>(find.byType(SingleChildScrollView));
      final card = t.renderObject<RenderBox>(find.bySemanticsLabel(RegExp('^Needs your OK')));
      final top = card.localToGlobal(Offset.zero, ancestor: view).dy;
      thread.scroll.jumpTo(thread.scroll.offset + top);
      await t.pumpAndSettle();
    },
  ),
  'chat-taskiem-approve': (
    build: () {
      final f = Fixture('chat-taskiem');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      return TaskiemScreen(data: TaskiemData(chats, follow: false));
    },
    drive: (t) async {
      await _loaded(t);
      // As the web half: the approval at the thread's top, then Approve.
      final thread = t.state<ThreadViewState<TaskiemLine>>(find.byType(ThreadView<TaskiemLine>));
      final view = t.renderObject<RenderBox>(find.byType(SingleChildScrollView));
      final card = t.renderObject<RenderBox>(find.bySemanticsLabel(RegExp('^Needs your OK')));
      thread.scroll.jumpTo(thread.scroll.offset + card.localToGlobal(Offset.zero, ancestor: view).dy);
      await t.pumpAndSettle();
      await t.tap(find.bySemanticsLabel(RegExp('^Approve: ')));
      await t.pumpAndSettle();
    },
  ),
  'chats-customers': (
    build: () {
      final f = Fixture('customers');
      return ChatsScreen(data: ChatsData(f.auth(), now: () => f.at));
    },
    drive: _loaded,
  ),
  'customer-chidi': (
    build: () {
      final f = Fixture('customer-chidi');
      final chats = ChatsData(f.auth(), now: () => f.at)..load();
      return CustomerScreen(data: CustomerData(chats, '01a1caf1-bffd-7bf6-b932-a1e10d4b7bab', follow: false));
    },
    drive: _loaded,
  ),
  'chat-menu': (build: () => _conversation('chat-tunde', "Mama Tunde's Market"), drive: (t) => _hold(t)),
  'chat-long': (build: () => _conversation('chat-long', "Mama Tunde's Market"), drive: _loaded),
  'chat-search': (
    build: () => _conversation('chat-search', "Mama Tunde's Market"),
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('Search this conversation'));
      await t.pumpAndSettle();
      await t.enterText(find.byType(EditableText).last, 'tomato');
      // Searched 250 ms after the typing stops.
      await t.pump(const Duration(milliseconds: 300));
      await _loaded(t);
      FocusManager.instance.primaryFocus?.unfocus();
      await t.pumpAndSettle();
    },
  ),
  'chat-calendar': (
    build: () => _conversation('chat-search', "Mama Tunde's Market"),
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('Search this conversation'));
      await t.pumpAndSettle();
      await t.tap(find.bySemanticsLabel('Search by date'));
      await t.pumpAndSettle();
    },
  ),
  'chat-info': (build: () => _conversation('chat-info', "Mama Tunde's Market"), drive: (t) => _contact(t)),
  'chat-info-media': (
    build: () => _conversation('chat-info', "Mama Tunde's Market"),
    drive: (t) async {
      await _contact(t);
      await t.tap(find.bySemanticsLabel('Media and files'));
      await _loaded(t);
    },
  ),
  'chat-info-starred': (
    build: () => _conversation('chat-info', "Mama Tunde's Market"),
    drive: (t) async {
      await _contact(t);
      await t.tap(find.bySemanticsLabel('Starred messages'));
      await t.pumpAndSettle();
    },
  ),
  'chat-head-menu': (
    build: () => _conversation('chat-info', "Mama Tunde's Market"),
    drive: (t) async {
      await _loaded(t);
      await t.tap(find.bySemanticsLabel('More about this conversation'));
      await t.pumpAndSettle();
    },
  ),
  'chat-tagged': (
    build: () => _conversation('chat-tag', 'Bayo Rice Mills'),
    drive: (t) async {
      await _loaded(t);
      // To its end, where the message is.
      final thread = t.state<ThreadViewState<ThreadMessage>>(find.byType(ThreadView<ThreadMessage>));
      thread.scroll.jumpTo(thread.scroll.position.maxScrollExtent);
      await _loaded(t);
    },
  ),
  'chat-tag': (build: () => _conversation('chat-tag', 'Bayo Rice Mills'), drive: (t) => _typeTag(t, '@')),
  'chat-tag-hash': (build: () => _conversation('chat-tag', 'Bayo Rice Mills'), drive: (t) => _typeTag(t, '#')),
  'chat-deal': (build: () => _conversation('chat-deal', 'Bayo Rice Mills'), drive: (t) => _toEnd(t)),
  'chat-deal-counter': (
    build: () => _conversation('chat-deal', 'Bayo Rice Mills'),
    drive: (t) async {
      await _toEnd(t);
      await t.tap(find.bySemanticsLabel('Counter P-9XTV54'));
      await t.pump();
      final boxes = find.descendant(of: find.byType(DealActions), matching: find.byType(EditableText));
      await t.enterText(boxes.first, '755k');
      await t.enterText(boxes.last, 'Meet me halfway');
      FocusManager.instance.primaryFocus?.unfocus();
      await _toEnd(t);
    },
  ),
  'chat-pick': (
    build: () => _conversation('chat-tunde', "Mama Tunde's Market"),
    drive: (t) async {
      await _hold(t);
      await t.tap(find.bySemanticsLabel('More reactions'));
      await t.pumpAndSettle();
    },
  ),
  'chat-reply': (
    build: () => _conversation('chat-tunde', "Mama Tunde's Market"),
    drive: (t) async {
      await _hold(t);
      await t.tap(find.bySemanticsLabel('Reply'));
      await t.pumpAndSettle();
    },
  ),
  'chat-delete': (
    build: () => _conversation('chat-tunde', "Mama Tunde's Market"),
    drive: (t) async {
      await _hold(t);
      await t.tap(find.bySemanticsLabel('Delete for me'));
      await t.pumpAndSettle();
    },
  ),
  'sign-up': (
    build: () => SignUpScreen(auth: fakeAuth()),
    drive: (t) async {
      await _tap(t, 'I bill customers for a service');
      FocusManager.instance.primaryFocus?.unfocus();
      await t.pumpAndSettle();
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

/// A conversation of a fixture, shown as the app shows it.
Widget _conversation(String fixture, String name) {
  final f = Fixture(fixture);
  final chats = ChatsData(f.auth(), now: () => f.at)..load();
  final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == name);
  return ConversationScreen(data: ConversationData(chats, conv, follow: false));
}

/// Types a tag's mark in the box, as the web half fills it; the keyboard
/// put away (the web half's box is blurred).
Future<void> _typeTag(WidgetTester t, String mark) async {
  await _loaded(t);
  await t.enterText(find.byType(EditableText), mark);
  await t.pump();
  FocusManager.instance.primaryFocus?.unfocus();
  await _loaded(t);
}

/// The thread at its end, what is fetched for real loaded.
Future<void> _toEnd(WidgetTester t) async {
  await _loaded(t);
  final thread = t.state<ThreadViewState<ThreadMessage>>(find.byType(ThreadView<ThreadMessage>));
  thread.scroll.jumpTo(thread.scroll.position.maxScrollExtent);
  await _loaded(t);
}

/// Opens the conversation's contact info, its card, stars and photos read.
Future<void> _contact(WidgetTester t) async {
  await _loaded(t);
  await t.tap(find.bySemanticsLabel("About Mama Tunde's Market"));
  await _loaded(t);
}

/// Waits for what is fetched and decoded for real (logos, photos), outside
/// the test's clock.
Future<void> _loaded(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
    await t.pump();
  }
  await t.pumpAndSettle();
}

/// Holds the conversation's first message, as the web half right-clicks it.
Future<void> _hold(WidgetTester t) async {
  await _loaded(t);
  await t.longPress(find.textContaining('Good morning ma', findRichText: true));
  await t.pumpAndSettle();
}

/// Their shop from the tools panel, the first item it can add in the bag;
/// with [review], its review.
Future<void> _shop(WidgetTester t, {bool review = false}) async {
  await _loaded(t);
  await t.tap(find.bySemanticsLabel('Open tools'));
  await _loaded(t);
  await t.tap(find.bySemanticsLabel(RegExp('^Their shop')));
  await _loaded(t);
  // At large text the head can fill the sheet: the first row scrolled to.
  final more = find.bySemanticsLabel(RegExp('^One more '));
  for (var i = 0; i < 20 && more.evaluate().isEmpty; i++) {
    await t.drag(find.byType(Scrollable).last, const Offset(0, -80));
    await t.pumpAndSettle();
  }
  await t.tap(more.first);
  await _loaded(t);
  if (review) {
    await t.tap(find.bySemanticsLabel('Review order'));
    await _loaded(t);
  }
}
