// Older messages, as a reader scrolls up: a full newest page says there are
// more; near the top the page before it is asked for and drawn above, the
// reader's place kept; a short page says there are no more.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

Map<String, Object> _msg(int n) => {
  'id': 'm$n',
  'link': _link,
  'mine': n.isEven,
  'kind': 'text',
  'body': 'Message $n',
  'created_at': DateTime.utc(2026, 11, 11, 9).add(Duration(minutes: n)).toIso8601String(),
  'read_at': '2026-11-11T12:00:00Z',
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  testWidgets('scrolled to the top, the page before is drawn above, the place kept', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final asked = <String>[];
    http.Response answer(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages') {
          final q = req.url.queryParameters;
          if (q.containsKey('after')) return answer({'messages': <Object>[]});
          if (q.containsKey('before')) {
            asked.add('${q['before']} ${q['limit']}');
            return answer({'messages': [for (var n = 40; n < 50; n++) _msg(n)]});
          }
          return answer({'messages': [for (var n = 50; n < 100; n++) _msg(n)]});
        }
        if (req.method == 'GET') return null;
        return answer(<String, Object>{});
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    final data = ConversationData(chats, conv, follow: false);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: data)));
    await settle(t);
    expect(data.hasOlder, isTrue, reason: 'a full page');
    expect(asked, isEmpty, reason: 'opened at the newest, far from the top');

    final thread = t.state<ThreadViewState<ThreadMessage>>(find.byType(ThreadView<ThreadMessage>));
    // Up to just short of the top: the mark is within 200 px.
    thread.scroll.jumpTo(150);
    await t.pump();
    final fifty = find.textContaining('Message 50', findRichText: true);
    final before = t.getTopLeft(fifty).dy;
    await settle(t);
    expect(asked, ['${DateTime.utc(2026, 11, 11, 9, 50).toIso8601String()} 50']);
    expect(data.messages!.first.id, 'm40');
    expect(t.getTopLeft(fifty).dy, moreOrLessEquals(before, epsilon: 1), reason: 'the reader stays where they were');
    expect(data.hasOlder, isFalse, reason: 'a short page');

    thread.scroll.jumpTo(0);
    await settle(t);
    expect(asked, hasLength(1), reason: 'no more to ask for');
    expect(find.textContaining('Message 40', findRichText: true), findsOneWidget);
  });
}
