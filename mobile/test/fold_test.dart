// A business's long message, folded as the web folds it: what fits the
// first view, "… Read more", and the rest when it is tapped.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

void main() {
  setUpAll(loadFonts);

  testWidgets('a long message: folded, then all of it', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final long = List.generate(40, (i) => 'Line ${i + 1} of the price list: tomatoes, peppers, onions.').join('\n');
    http.Response answer(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/link-chats/$_link/messages') {
          return answer({
            'messages': [
              {'id': 'long', 'link': _link, 'mine': false, 'kind': 'text', 'body': long, 'created_at': '2026-11-11T12:00:00Z', 'read_at': '2026-11-11T12:01:00Z'},
            ],
          });
        }
        if (req.method == 'GET') return null;
        return answer(<String, Object>{});
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    expect(find.textContaining('Line 10 of', findRichText: true), findsOneWidget, reason: 'the first ten lines');
    expect(find.textContaining('Line 11 of', findRichText: true), findsNothing);
    expect(find.textContaining('… Read more', findRichText: true), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp('Read more')));
    await t.pump();
    expect(find.textContaining('Line 30 of', findRichText: true), findsOneWidget, reason: 'twenty more');
    expect(find.textContaining('Line 31 of', findRichText: true), findsNothing);
    // Taller than the screen now: down to its end, where "Read more" is.
    await t.drag(find.byType(SingleChildScrollView).first, const Offset(0, -2000));
    await t.pump();
    final words = find.textContaining('… Read more', findRichText: true);
    await t.tapAt(t.getBottomLeft(words) + const Offset(40, -10));
    await t.pump();
    expect(find.textContaining('Line 40 of', findRichText: true), findsOneWidget);
    expect(find.textContaining('Read more', findRichText: true), findsNothing, reason: 'all of it');
  });
}
