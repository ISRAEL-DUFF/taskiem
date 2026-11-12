// Messages about it on the order screen (M7 fixes Part 7; web
// Conversation.tsx DocThread): the thread's cards and words, a reply to
// its latest card, and nothing to show without a card.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/doc_thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

final _thread = [
  {
    'id': 'm1',
    'link': 'L1',
    'kind': 'card',
    'mine': false,
    'created_at': '2026-11-12T09:00:00Z',
    'card': {'doc': 'order', 'event': 'ordered', 'reference': 'PO-12', 'id': 'T1', 'amount_kobo': 1500000},
  },
  {'id': 'm2', 'link': 'L1', 'kind': 'text', 'mine': false, 'sender_name': 'Tolu', 'body': 'Can it come by Friday?', 'created_at': '2026-11-12T09:05:00Z'},
  {
    'id': 'm3',
    'link': 'L1',
    'kind': 'card',
    'mine': true,
    'created_at': '2026-11-12T09:10:00Z',
    'card': {'doc': 'order', 'event': 'accept', 'reference': 'PO-12', 'id': 'T1', 'amount_kobo': 1500000},
  },
];

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 400));
  }

  Future<List<String>> open(WidgetTester t, {List<Map<String, Object?>>? thread, List<String>? perms}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 1200);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.url.path == '/v1/link-chats/thread') {
          sent.add('GET ${req.url.query}');
          return http.Response(jsonEncode({'messages': thread ?? _thread}), 200, headers: {'content-type': 'application/json'});
        }
        if (req.method == 'POST') {
          sent.add('POST ${req.url.path} ${req.body}');
          return http.Response(jsonEncode({'message': {}}), 201, headers: {'content-type': 'application/json'});
        }
        return null;
      }),
      now: () => f.at,
    );
    if (perms != null) chats.auth.me = Me.fromJson({...f.json('/v1/me'), 'permissions': perms});
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: SingleChildScrollView(child: DocThread(auth: chats.auth, doc: 'order', reference: 'PO-12', follow: false)),
      ),
    );
    await settle(t);
    return sent;
  }

  testWidgets('the order’s thread: its cards and words; a reply to its latest card', (t) async {
    final sent = await open(t);
    expect(sent.first, 'GET doc=order&reference=PO-12');
    expect(find.text('Messages about it'), findsOneWidget);
    expect(find.text('Can it come by Friday?'), findsOneWidget);
    expect(find.text('Tolu'), findsOneWidget);
    await t.enterText(find.byType(EditableText), 'Yes, Friday morning');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send the reply about PO-12'));
    await settle(t);
    final post = sent.firstWhere((x) => x.startsWith('POST'));
    expect(post, startsWith('POST /v1/link-chats/L1/messages '));
    final body = jsonDecode(post.substring('POST /v1/link-chats/L1/messages '.length)) as Map;
    expect(body['text'], 'Yes, Friday morning');
    expect(body['reply_to'], 'm3', reason: 'the latest card');
    expect(body['client_id'], isNotEmpty);
    expect(find.text('Yes, Friday morning'), findsNothing, reason: 'the box emptied once sent');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('no box for those who may not trade; nothing at all without a card', (t) async {
    await open(t, perms: ['storefront.manage']);
    expect(find.text('Messages about it'), findsOneWidget);
    expect(find.byType(EditableText), findsNothing);
    await open(t, thread: [
      {'id': 'm2', 'link': 'L1', 'kind': 'text', 'mine': false, 'body': 'Hello', 'created_at': '2026-11-12T09:05:00Z'},
    ]);
    expect(find.text('Messages about it'), findsNothing);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    final chats = ChatsData(
      f.auth(
        also: (req) async => req.url.path == '/v1/link-chats/thread'
            ? http.Response(jsonEncode({'messages': _thread}), 200, headers: {'content-type': 'application/json'})
            : null,
      ),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(mode: TkMode.dark, home: SingleChildScrollView(child: DocThread(auth: chats.auth, doc: 'order', reference: 'PO-12', follow: false))),
      drive: settle,
    );
  });
}
