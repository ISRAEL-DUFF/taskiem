// A message's menu, as a person uses it: held, then a reaction (given, and
// taken back from its chip), a star, a reply (its quote, reply_to sent),
// Copy, Message info, Forward to another business, and Delete for me
// (asked first, then gone, said in a toast).

import 'dart:convert';

import 'package:flutter/services.dart';
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
const _first = '01a1ca87-3b17-7eb0-85d8-4fd9c674a645';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  Future<List<(String, String, Object?)>> open(WidgetTester t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final calls = <(String, String, Object?)>[];
    http.Response answer(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET') return null;
        // Opening reads it: not the menu's doing.
        if (req.url.path.endsWith('/read')) return answer({'read': 2});
        final body = req.body.isEmpty ? null : jsonDecode(req.body);
        calls.add((req.method, req.url.path, body));
        final p = req.url.path;
        if (p.endsWith('/reaction')) {
          final emoji = req.method == 'PUT' ? (body as Map)['emoji'] : null;
          return answer({
            'reactions': [
              if (emoji != null) {'emoji': emoji, 'count': 1, 'mine': true, 'by': ['You']},
            ],
          });
        }
        if (p.endsWith('/star')) return answer({'starred': req.method == 'PUT'});
        if (p.endsWith('/messages') && req.method == 'POST') {
          final b = body as Map;
          return answer({
            'message': {'id': 'r1', 'link': _link, 'mine': true, 'kind': 'text', 'body': b['text'], 'created_at': '2026-11-11T12:20:00Z'},
          }, 201);
        }
        return answer(<String, Object>{});
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    return calls;
  }

  Future<void> hold(WidgetTester t) async {
    await t.longPress(find.textContaining('Good morning ma', findRichText: true));
    await t.pumpAndSettle();
  }

  testWidgets('a reaction given from the menu, and taken back from its chip', (t) async {
    final calls = await open(t);
    await hold(t);
    await t.tap(find.bySemanticsLabel('React with heart'));
    await settle(t);
    expect((calls.single.$1, calls.single.$2), ('PUT', '/v1/link-chats/$_link/messages/$_first/reaction'));
    expect(calls.single.$3, {'emoji': '❤️'});
    final chip = find.bySemanticsLabel(RegExp('^❤️ 1: You. Tap to take yours back'));
    expect(chip, findsOneWidget);
    await t.tap(chip);
    await settle(t);
    expect(calls.last.$1, 'DELETE');
    expect(chip, findsNothing);
  });

  testWidgets('a star, shown by the time', (t) async {
    final calls = await open(t);
    await hold(t);
    await t.tap(find.bySemanticsLabel('Star'));
    await settle(t);
    expect((calls.single.$1, calls.single.$2), ('PUT', '/v1/link-chats/$_link/messages/$_first/star'));
    // Said with the message (its sender's words around it).
    expect(find.bySemanticsLabel(RegExp('Starred')), findsOneWidget);
    await hold(t);
    expect(find.bySemanticsLabel('Unstar'), findsOneWidget);
  });

  testWidgets('a reply: the bar over the box, the quote in what is sent, reply_to', (t) async {
    final calls = await open(t);
    await hold(t);
    await t.tap(find.bySemanticsLabel('Reply'));
    await t.pumpAndSettle();
    expect(find.text('Replying to Good morning ma, tomatoes are cheaper today. ₦28,000 a basket.'), findsOneWidget);
    await t.enterText(find.byType(EditableText), 'Send 4 baskets');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await t.pump();
    expect(find.textContaining('Replying to'), findsNothing);
    expect(find.text('Mama Tunde\'s Market'), findsWidgets, reason: 'the quote says whose words');
    await settle(t);
    expect(calls.single.$3, {'text': 'Send 4 baskets', 'client_id': isNotEmpty, 'reply_to': _first});
  });

  testWidgets('a swipe right replies too', (t) async {
    await open(t);
    await t.drag(find.textContaining('Good morning ma', findRichText: true), const Offset(90, 0));
    await t.pumpAndSettle();
    expect(find.textContaining('Replying to Good morning'), findsOneWidget);
  });

  testWidgets('Copy: the words to the clipboard, the menu saying so', (t) async {
    String? copied;
    t.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') copied = (call.arguments as Map)['text'] as String;
      return null;
    });
    await open(t);
    await hold(t);
    await t.tap(find.bySemanticsLabel('Copy'));
    await t.pump();
    expect(copied, 'Good morning ma, tomatoes are cheaper today. ₦28,000 a basket.');
    expect(find.bySemanticsLabel('Copied'), findsOneWidget);
  });

  testWidgets('Delete for me: asked first, then gone, said in a toast', (t) async {
    final calls = await open(t);
    await hold(t);
    await t.tap(find.bySemanticsLabel('Delete for me'));
    await t.pumpAndSettle();
    expect(find.text('Delete this message?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Cancel'));
    await t.pumpAndSettle();
    expect(calls, isEmpty);
    await hold(t);
    await t.tap(find.bySemanticsLabel('Delete for me'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Delete for me').last);
    await settle(t);
    expect((calls.single.$1, calls.single.$2), ('POST', '/v1/link-chats/$_link/messages/$_first/hide'));
    expect(find.textContaining('Good morning ma', findRichText: true), findsNothing);
    expect(find.text('Deleted for you'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Message info and Forward', (t) async {
    final calls = await open(t);
    await hold(t);
    await t.tap(find.bySemanticsLabel('Message info'));
    await t.pumpAndSettle();
    expect(find.text('Received'), findsOneWidget);
    expect(find.text('Read by you'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Close'));
    await t.pumpAndSettle();
    await hold(t);
    await t.tap(find.bySemanticsLabel('Forward'));
    await t.pumpAndSettle();
    expect(find.text('Forward to'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Bayo Rice Mills'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Forward'));
    await settle(t);
    expect(calls.single.$2, '/v1/link-chats/$_link/forward');
    expect((calls.single.$3 as Map)['message'], _first);
    expect(((calls.single.$3 as Map)['to'] as List).length, 1);
    expect(find.text('Forwarded to Bayo Rice Mills'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });
}
