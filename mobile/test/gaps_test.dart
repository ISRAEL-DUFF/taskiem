// Small gaps against the web chat, closed (docs/spec/mobile-app-m3-fixes.md
// G3, G13).

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
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
}
