// A conversation with a linked business, as a person uses it: opened from
// the chats, a message sent (a clock, then the server's copy with its two
// ticks), and one that could not be sent (Retry, then sent).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

void main() {
  setUpAll(loadFonts);

  Widget app(ChatsData chats) => TaskiemApp(
        mode: TkMode.light,
        home: Builder(
          builder: (context) => ChatsScreen(
            data: chats,
            onOpen: (c) => Navigator.of(context).push(PageRouteBuilder<void>(
              transitionDuration: Duration.zero,
              pageBuilder: (_, _, _) => ConversationScreen(data: ConversationData(chats, c, follow: false)),
            )),
          ),
        ),
      );

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 4; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  testWidgets('opened from the chats, a message is sent and shows its ticks', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final sent = <Map<String, dynamic>>[];
    final answer = Completer<void>();
    var read = 0;
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path == '/v1/link-chats/$_link/read') {
          read++;
          return http.Response(jsonEncode({'read': 2}), 200);
        }
        if (req.method == 'POST' && req.url.path == '/v1/link-chats/$_link/messages') {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          sent.add(body);
          await answer.future;
          return http.Response(
              jsonEncode({
                'message': {'id': 'm-new', 'link': _link, 'mine': true, 'kind': 'text', 'body': body['text'], 'created_at': f.at.toIso8601String()},
              }),
              201);
        }
        return null;
      }),
      now: () => f.at,
    );
    await t.pumpWidget(app(chats));
    await settle(t);
    await t.tap(find.text("Mama Tunde's Market"));
    await settle(t);
    expect(find.text('2 unread messages'), findsOneWidget);
    expect(find.textContaining('No problem. My boy will be there before 9.', findRichText: true), findsOneWidget);
    expect(read, 1, reason: 'opened, its unread are read');
    expect(find.bySemanticsLabel('Record a voice note'), findsOneWidget);

    await t.enterText(find.byType(EditableText), 'Thank you!');
    await t.pump();
    expect(find.bySemanticsLabel('Send'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Send'));
    await t.pump();
    expect(find.bySemanticsLabel('Sending'), findsOneWidget, reason: 'shown at once, with a clock');
    expect(find.textContaining('Thank you!', findRichText: true), findsOneWidget);
    answer.complete();
    await settle(t);
    expect(sent.single['text'], 'Thank you!');
    expect(sent.single['client_id'], isNotEmpty);
    expect(find.bySemanticsLabel('Sending'), findsNothing);
    expect(find.textContaining('Thank you!', findRichText: true), findsOneWidget);
    expect(find.bySemanticsLabel('Delivered'), findsNWidgets(2));
  });

  testWidgets('a message that could not be sent is tried again with the same client id', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final ids = <String>[];
    var fail = true;
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path.endsWith('/messages')) {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          ids.add(body['client_id'] as String);
          if (fail) return http.Response(jsonEncode({'error': 'slow down'}), 429);
          return http.Response(
              jsonEncode({
                'message': {'id': 'm-new', 'link': _link, 'mine': true, 'kind': 'text', 'body': body['text'], 'created_at': f.at.toIso8601String()},
              }),
              201);
        }
        return null;
      }),
      now: () => f.at,
    );
    await t.pumpWidget(app(chats));
    await settle(t);
    await t.tap(find.text("Mama Tunde's Market"));
    await settle(t);
    await t.enterText(find.byType(EditableText), 'Are you coming?');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.bySemanticsLabel(RegExp('^Not sent. You are sending too fast')), findsOneWidget);
    fail = false;
    await t.tap(find.bySemanticsLabel('Retry'));
    await settle(t);
    expect(find.bySemanticsLabel(RegExp('^Not sent')), findsNothing);
    expect(ids.length, 2);
    expect(ids[0], ids[1], reason: 'a retry is the same message to the server');
  });
}
