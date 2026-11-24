// Small gaps against the web chat, closed (docs/spec/mobile-app-m3-fixes.md
// G3, G4, G11, G13).

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/links.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _tunde = '3af349e2-b776-4306-bdb8-10df59687c55';

void main() {
  group('G13: where Taskiem\'s conversation was last seen is kept on the phone', () {
    test('a first time begins now, and is kept', () async {
      final keys = MemoryKeyStore();
      final prefs = Prefs(keys);
      final f = Fixture('chat-taskiem');
      final chats = ChatsData(f.auth(), now: () => f.at, prefs: prefs);
      await prefs.load();
      await Future<void>.delayed(Duration.zero);
      expect(chats.taskiemSeen, f.at);
      expect(DateTime.parse(keys.values['taskiem-seen']!), f.at);
    });

    test('opened again: from where it was last seen; seeing it moves it on, never back', () async {
      final keys = MemoryKeyStore()..values['taskiem-seen'] = '2026-11-10T08:00:00.000Z';
      final prefs = Prefs(keys);
      final f = Fixture('chat-taskiem');
      final chats = ChatsData(f.auth(), now: () => f.at, prefs: prefs);
      await prefs.load();
      await Future<void>.delayed(Duration.zero);
      expect(chats.taskiemSeen, DateTime.utc(2026, 11, 10, 8));
      chats.markTaskiemSeen(DateTime.utc(2026, 11, 11, 12));
      await Future<void>.delayed(Duration.zero);
      expect(keys.values['taskiem-seen'], '2026-11-11T12:00:00.000Z');
      chats.markTaskiemSeen(DateTime.utc(2026, 11, 11, 9));
      await Future<void>.delayed(Duration.zero);
      expect(keys.values['taskiem-seen'], '2026-11-11T12:00:00.000Z');
    });
  });

  testWidgets('G3: typing tells them, at most every 3 seconds; "@Taskiem" and "/" do not', (t) async {
    await loadFonts();
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    var now = f.at;
    final told = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path.endsWith('/typing')) {
          told.add(req.url.path);
          return http.Response(jsonEncode(<String, Object>{}), 200, headers: {'content-type': 'application/json'});
        }
        return null;
      }),
      now: () => now,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _tunde);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    final box = find.byType(EditableText);
    await t.enterText(box, 'S');
    await t.pump();
    await t.enterText(box, 'Se');
    await t.enterText(box, 'Send');
    await t.pump();
    expect(told, ['/v1/link-chats/$_tunde/typing']);
    now = now.add(const Duration(seconds: 4));
    await t.enterText(box, 'Send 4');
    await t.pump();
    expect(told, hasLength(2));
    now = now.add(const Duration(seconds: 4));
    await t.enterText(box, '');
    await t.pump();
    expect(told, hasLength(2), reason: 'nothing typed');
    if (chats.chatTools) {
      await t.enterText(box, '@Taskiem how much is rice');
      await t.pump();
      expect(told, hasLength(2), reason: 'asking Taskiem does not reach them');
    }
  });

  Future<(ChatsData, ConversationData)> tunde(WidgetTester t, {Future<http.Response?> Function(http.Request req)? also}) async {
    await loadFonts();
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final chats = ChatsData(
      f.auth(also: (req) async {
        final own = await also?.call(req);
        if (own != null) return own;
        if (req.method == 'POST') return http.Response(jsonEncode(<String, Object>{}), 200, headers: {'content-type': 'application/json'});
        return null;
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _tunde);
    final data = ConversationData(chats, conv, follow: false);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: data)));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    return (chats, data);
  }

  testWidgets('G4: words left unsent are kept, back in the box when it opens again, gone once sent', (t) async {
    final (chats, data) = await tunde(
      t,
      also: (req) async => req.method == 'POST' && req.url.path == '/v1/link-chats/$_tunde/messages'
          ? http.Response(
              jsonEncode({
                'message': {'id': 'sent-1', 'link': _tunde, 'mine': true, 'kind': 'text', 'body': 'Send 4 baskets', 'created_at': '2026-11-11T13:00:00Z'},
              }),
              201,
              headers: {'content-type': 'application/json'},
            )
          : null,
    );
    await t.enterText(find.byType(EditableText), 'Send 4 baskets');
    await t.pump();
    expect(chats.draft(_tunde), 'Send 4 baskets');
    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, data.conv, follow: false))));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    expect(t.widget<EditableText>(find.byType(EditableText)).controller.text, 'Send 4 baskets');
    await t.tap(find.bySemanticsLabel('Send'));
    await t.pump();
    expect(chats.draft(_tunde), '');
    expect(chats.drafts, isEmpty);
    // "/pro" opening the tools is not a draft.
    if (chats.chatTools) {
      await t.enterText(find.byType(EditableText), '/pro');
      await t.pump();
      expect(chats.drafts, isEmpty);
    }
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  });

  testWidgets('G4: the list says "Draft:" with the words', (t) async {
    await loadFonts();
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final chats = ChatsData(f.auth(), now: () => f.at)..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatsScreen(data: chats)));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    final bayo = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    chats.setDraft(bayo.link, 'How much is the 50kg?');
    chats.setDraft('taskiem', 'status');
    await t.pump();
    expect(find.textContaining('Draft: How much is the 50kg?', findRichText: true), findsOneWidget);
    expect(find.textContaining('Draft: status', findRichText: true), findsOneWidget);
    chats.setDraft(bayo.link, '  ');
    await t.pump();
    expect(find.textContaining('Draft: How much', findRichText: true), findsNothing);
  });

  testWidgets('G11: a link\'s preview in its bubble opens the page in the browser tab', (t) async {
    final opened = <Uri>[];
    final was = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openInBrowser = was);
    await tunde(
      t,
      also: (req) async => req.method == 'GET' && req.url.path == '/v1/link-chats/$_tunde/messages'
          ? http.Response(
              jsonEncode({
                'messages': [
                  {
                    'id': 'p1',
                    'link': _tunde,
                    'mine': false,
                    'kind': 'text',
                    'body': 'Our new prices: https://tunde.example/prices',
                    'created_at': '2026-11-11T12:00:00Z',
                    'read_at': '2026-11-11T12:01:00Z',
                    'preview': {'url': 'https://tunde.example/prices', 'host': 'tunde.example', 'title': 'November prices', 'description': 'Tomatoes, peppers and onions'},
                  },
                ],
              }),
              200,
              headers: {'content-type': 'application/json'},
            )
          : null,
    );
    expect(find.text('November prices'), findsOneWidget);
    expect(find.text('Tomatoes, peppers and onions'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('November prices, tunde.example'));
    await t.pump();
    expect(opened, [Uri.parse('https://tunde.example/prices')]);
  });
}
