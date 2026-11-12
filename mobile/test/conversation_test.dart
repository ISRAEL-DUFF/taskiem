// A conversation with a linked business, as a person uses it: opened from
// the chats, a message sent (a clock, then the server's copy with its two
// ticks), and one that could not be sent (Retry, then sent).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/bills_page.dart' show BillPage;
import 'package:taskiem/kit/sheet.dart' show TkSheet;
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

  testWidgets('the keyboard up: the message box sits just above it, as on WhatsApp', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    // A phone's gesture bar, and then its keyboard (which covers the bar).
    t.view.padding = const FakeViewPadding(bottom: 24);
    addTearDown(t.view.reset);
    final f = Fixture('chat-bayo');
    final chats = ChatsData(f.auth(), now: () => f.at)..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    await t.pumpAndSettle();
    final box = find.bySemanticsLabel(RegExp(r'^Message$'));
    expect(t.getRect(box.first).bottom, lessThan(844 - 24), reason: 'above the gesture bar');
    t.view.viewInsets = const FakeViewPadding(bottom: 320);
    await t.pumpAndSettle();
    final r = t.getRect(box.first);
    expect(r.bottom, lessThanOrEqualTo(844 - 320), reason: 'not under the keyboard');
    expect(r.bottom, greaterThan(844 - 320 - 40), reason: 'just above it, no gap for the gesture bar');
  });

  testWidgets('cards: opened at the unread, the round button goes to the newest, Pay opens the invoice', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-bayo');
    final chats = ChatsData(f.auth(), now: () => f.at)..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    final opened = <LinkDocCard>[];
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: ConversationScreen(data: ConversationData(chats, conv, follow: false), onOpenDoc: opened.add),
    ));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('5 unread messages'), findsOneWidget);
    final jump = find.bySemanticsLabel('Scroll to the newest messages');
    expect(jump, findsOneWidget, reason: 'opened at the unread, not at the bottom');
    expect(find.text('Today'), findsNWidgets(2), reason: 'the day stays at the top, over its own place');
    await t.tap(jump);
    await t.pumpAndSettle();
    expect(jump, findsNothing);
    final pay = find.bySemanticsLabel('Pay INV-0001');
    expect(pay, findsOneWidget);
    expect(find.text('Pay ₦450,000'), findsOneWidget);
    await t.tap(pay);
    expect(opened.single.reference, 'INV-0001');
    await t.tap(find.bySemanticsLabel('View invoice INV-0001'));
    expect(opened.length, 2);
  });

  testWidgets('M8: a bill card’s Pay opens the bill, in its tool or on its screen, not "coming"', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-bayo');
    final chats = ChatsData(f.auth(), now: () => f.at)..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Scroll to the newest messages'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Pay INV-0001'));
    await t.pumpAndSettle();
    expect(find.text('This is coming to the app soon.'), findsNothing);
    expect(find.byType(TkSheet).evaluate().isNotEmpty || find.byType(BillPage).evaluate().isNotEmpty, isTrue);
    await t.pump(const Duration(seconds: 5));
  });
}
