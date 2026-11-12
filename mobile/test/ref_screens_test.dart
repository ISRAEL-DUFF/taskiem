// The cards of what is tagged, "/" tagging, businesses not linked from
// "@", and Taskiem's tags, as a person uses them (G6, G8, G14 of
// docs/spec/mobile-app-m3-fixes.md).

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/ref_card.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _tunde = '3af349e2-b776-4306-bdb8-10df59687c55';

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

const _invoiceCard = {
  'kind': 'invoice',
  'id': 'inv-1',
  'name': 'INV-0001',
  'party': "Mama Tunde's Market",
  'when': '2026-11-03T10:00:00Z',
  'total_kobo': 4500000,
  'paid_kobo': 1500000,
  'due_kobo': 3000000,
  'due_on': '2026-11-20',
  'status': 'overdue',
  'lines': [
    {'name': 'Rice, 50kg bag', 'quantity': 2, 'total_kobo': 4500000},
  ],
  'action': 'remind',
};

const _chidiRef = {'kind': 'customer', 'id': 'c1', 'name': 'Bisi Okafor', 'party': '•••0555', 'when': '2026-11-03T10:00:00Z'};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  void phone(WidgetTester t) {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  /// Tunde's conversation, its messages [messages] (or the recorded ones);
  /// every request kept.
  Future<(List<(String, String, Object?, String?)>, ConversationData)> tunde(WidgetTester t, {List<Map<String, dynamic>>? messages}) async {
    phone(t);
    final f = Fixture('chat-tunde');
    final asked = <(String, String, Object?, String?)>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        asked.add((req.method, '$p${req.url.query.isEmpty ? '' : '?${req.url.query}'}', req.body.isEmpty ? null : jsonDecode(req.body), req.headers['Idempotency-Key']));
        if (req.method == 'GET' && p == '/v1/link-chats/$_tunde/messages' && messages != null && !req.url.queryParameters.containsKey('after')) return _json({'messages': messages});
        if (req.method == 'GET' && p == '/v1/link-chats/$_tunde/card') return _json(_invoiceCard);
        if (req.method == 'GET' && p == '/v1/link-chats/$_tunde/refs') {
          return _json({
            'refs': [
              {'kind': 'invoice', 'id': 'inv-1', 'name': 'INV-0001', 'party': "Mama Tunde's Market", 'when': '2026-11-03T10:00:00Z', 'total_kobo': 4500000, 'status': 'overdue'},
              {'kind': 'order', 'id': 'o-1', 'name': 'P-7QX', 'party': "Mama Tunde's Market", 'when': '2026-11-02T10:00:00Z', 'total_kobo': 900000, 'status': 'delivered'},
            ],
          });
        }
        if (req.method == 'GET' && p == '/v1/explore' && req.url.queryParameters['q'] != null) {
          return _json({
            'results': [
              {'business': 'kola-1', 'name': "Kola's Bakery", 'logo': false, 'photos': 0, 'captions': <String>[], 'state': 'none', 'v': '1', 'area': 'Yaba'},
            ],
            'near': <Object>[],
            'near_more': false,
            'fresh': <Object>[],
            'more': false,
            'delivery': <Object>[],
            'delivery_more': false,
          });
        }
        if (req.method == 'POST' && p == '/v1/link-chats/$_tunde/messages') {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          return _json({
            'message': {'id': 'sent-1', 'link': _tunde, 'mine': true, 'kind': 'text', 'body': body['text'], 'created_at': '2026-11-11T13:00:00Z'},
          }, 201);
        }
        if (req.method == 'POST') return _json(<String, Object>{});
        return null;
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _tunde);
    final d = ConversationData(chats, conv, follow: false);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: d)));
    await settle(t);
    return (asked, d);
  }

  testWidgets('G6: an invoice named in a message is its card, read now; its reminder sent once', (t) async {
    final (asked, _) = await tunde(
      t,
      messages: [
        {
          'id': 'm1',
          'link': _tunde,
          'mine': false,
          'kind': 'text',
          'body': 'Paying #INV-0001 this week',
          'created_at': '2026-11-11T12:00:00Z',
          'read_at': '2026-11-11T12:01:00Z',
          'mentions': [
            {'kind': 'invoice', 'name': 'INV-0001', 'ref': 'inv-1', 'status': 'sent'},
          ],
        },
      ],
    );
    expect(asked.map((a) => a.$2), contains('/v1/link-chats/$_tunde/card?kind=invoice&name=INV-0001'));
    expect(find.byType(RefCard), findsOneWidget);
    expect(find.text('INVOICE'), findsOneWidget);
    expect(find.text('Overdue'), findsOneWidget);
    expect(find.text('₦30,000'), findsOneWidget, reason: 'what is still to pay');
    expect(find.text('₦15,000 paid'), findsOneWidget);
    expect(find.text('2 × Rice, 50kg bag'), findsOneWidget);
    expect(find.bySemanticsLabel('Give this to… Invoice INV-0001'), findsOneWidget);
    final remind = find.bySemanticsLabel('Send a reminder Invoice INV-0001');
    await t.ensureVisible(remind);
    await t.pumpAndSettle();
    await t.tap(remind);
    await settle(t);
    final sent = asked.where((a) => a.$1 == 'POST' && a.$2 == '/v1/invoices/inv-1/send').toList();
    expect(sent, hasLength(1));
    expect(sent.single.$4, isNotEmpty, reason: 'one press, one reminder');
    expect(find.text('Reminder sent for INV-0001'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  testWidgets('G6: a card that cannot be read leaves the words as they are', (t) async {
    phone(t);
    final f = Fixture('chat-tunde');
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.url.path == '/v1/link-chats/$_tunde/card') return _json({'error': 'not found'}, 404);
        if (req.url.path == '/v1/link-chats/$_tunde/messages' && !req.url.queryParameters.containsKey('after')) {
          return _json({
            'messages': [
              {'id': 'm1', 'link': _tunde, 'mine': false, 'kind': 'text', 'body': 'About #P-OLD', 'created_at': '2026-11-11T12:00:00Z', 'read_at': '2026-11-11T12:01:00Z', 'mentions': [
                {'kind': 'order', 'name': 'P-OLD'},
              ]},
            ],
          });
        }
        if (req.method == 'POST') return _json(<String, Object>{});
        return null;
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _tunde);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    expect(find.textContaining('About', findRichText: true), findsOneWidget);
    expect(find.text('ORDER'), findsNothing);
  });

  testWidgets('G6: "/invoice" tags an invoice between us, sent by its id', (t) async {
    final (asked, _) = await tunde(t);
    await t.enterText(find.byType(EditableText), 'Paid /invoice INV');
    await t.pump();
    await t.pumpAndSettle();
    expect(find.text('INVOICES AND BILLS'), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp(r'^INV-0001, ')));
    await t.pumpAndSettle();
    expect(t.widget<EditableText>(find.byType(EditableText)).controller.text, 'Paid #INV-0001 ');
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    final body = asked.lastWhere((a) => a.$1 == 'POST' && a.$2 == '/v1/link-chats/$_tunde/messages').$3 as Map<String, dynamic>;
    expect(body['mentions'], [
      {'kind': 'invoice', 'ref': 'inv-1'},
    ]);
  });

  testWidgets('G14: "@" finds a business not linked; picked, its card with Connect', (t) async {
    final (asked, _) = await tunde(t);
    await t.enterText(find.byType(EditableText), 'Ask @ko');
    await t.pump();
    await t.pump(const Duration(milliseconds: 300));
    await settle(t);
    expect(asked.map((a) => a.$2), contains('/v1/explore?q=ko'));
    final kola = find.bySemanticsLabel("Kola's Bakery, Not linked · Yaba: see their card");
    expect(kola, findsOneWidget);
    await t.tap(kola);
    await t.pumpAndSettle();
    expect(t.widget<EditableText>(find.byType(EditableText)).controller.text, 'Ask @ko', reason: 'the words stay as typed');
    await t.tap(find.bySemanticsLabel("Connect with Kola's Bakery"));
    await settle(t);
    expect(asked.where((a) => a.$1 == 'POST' && a.$2 == '/v1/links').single.$3, {'business': 'kola-1', 'role': 'both'});
    expect(find.text("Asked Kola's Bakery to link"), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  testWidgets('G8: Taskiem\'s "@" and "#" offer its own; its answers mark what it names and card it', (t) async {
    phone(t);
    final f = Fixture('chat-taskiem');
    final asked = <String>[];
    final opened = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        asked.add('${req.method} $p${req.url.query.isEmpty ? '' : '?${req.url.query}'}');
        if (req.method == 'GET' && p == '/v1/chat-refs') {
          return _json({
            'refs': [
              {'kind': 'invoice', 'id': 'inv-1', 'name': 'INV-0001', 'party': "Ada's Kitchen", 'when': '2026-11-03T10:00:00Z', 'total_kobo': 4500000, 'status': 'overdue'},
              _chidiRef,
            ],
          });
        }
        if (req.method == 'GET' && p == '/v1/chat-card') return _json(_invoiceCard);
        if (req.method == 'POST' && p == '/v1/taskiem-chat') {
          return _json({
            'messages': [
              {'id': 'q1', 'channel': 'web', 'inbound': true, 'kind': 'text', 'text': 'who owes?', 'created_at': '2026-11-11T13:30:00Z'},
              {'id': 'a1', 'channel': 'web', 'inbound': false, 'kind': 'text', 'text': '#Bisi Okafor owes on #INV-0001.', 'created_at': '2026-11-11T13:30:01Z'},
            ],
          });
        }
        if (req.method == 'POST') return _json(<String, Object>{});
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false), onOpenCustomer: opened.add)));
    await settle(t);
    expect(asked, containsAll(['GET /v1/chat-refs', 'GET /v1/supplier-prices']));
    // "/customer" offers the customers it may see.
    await t.enterText(find.byType(EditableText), '/customer bi');
    await t.pump();
    await t.pumpAndSettle();
    expect(find.text('CUSTOMERS'), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp(r'^Bisi Okafor, ')));
    await t.pumpAndSettle();
    expect(t.widget<EditableText>(find.byType(EditableText)).controller.text, '#Bisi Okafor ');
    await t.enterText(find.byType(EditableText), 'who owes?');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(asked, contains('GET /v1/chat-card?kind=invoice&id=inv-1'));
    expect(find.byType(RefCard), findsNWidgets(2), reason: 'the invoice and the customer, once each');
    await t.tapOnText(find.textRange.ofSubstring('#Bisi Okafor'));
    await t.pump();
    expect(opened, ['c1']);
  });
}
