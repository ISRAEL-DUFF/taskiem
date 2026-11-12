// Orders from customers' messages (M7 part C; web CustomerOrders.tsx,
// lib/customerorders.ts): the card's words, and what each answer sends.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/customer_orders.dart';
import 'package:taskiem/chat/words.dart' show CardTone;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

Map<String, dynamic> _draft({String status = 'open', int questions = 1, bool ready = false, Map<String, dynamic> more = const {}}) => {
  'id': 'OD1',
  'chat_id': 'C1',
  'status': status,
  'by_reader': true,
  'version': 3,
  'questions': questions,
  'ready': ready,
  'waits': ready ? null : 'Answer the questions first.',
  'fulfilment': 'delivery',
  'address': '4 Herbert Macaulay',
  'delivery_kobo': 100000,
  'items_kobo': 1000000,
  'total_kobo': 1100000,
  'when': 'tomorrow',
  'created_at': '2026-11-12T10:00:00Z',
  'updated_at': '2026-11-12T10:00:00Z',
  'lines': [
    {'id': 'L1', 'said': 'jollof x2', 'item_id': 'jr', 'name': 'Jollof rice', 'quantity': 2, 'price_kobo': 500000},
    {'id': 'L2', 'said': 'drink', 'ask': {'kind': 'which', 'text': 'Which drink?', 'choices': [{'item_id': 'ch', 'name': 'Chapman', 'price_kobo': 150000}, {'item_id': 'co', 'name': 'Coke'}]}},
    {'id': 'L3', 'said': 'small chops'},
    {'id': 'L4', 'said': 'moi moi', 'ask': {'kind': 'how_many', 'text': 'How many moi moi?'}},
  ],
  ...more,
};

void main() {
  setUpAll(loadFonts);

  test('the card’s words, as customerorders.ts says them', () {
    expect(orderPill(_draft()), (word: '1 question', tone: CardTone.warn));
    expect(orderPill(_draft(questions: 0, ready: true)).word, 'Ready to confirm');
    expect(orderPill(_draft(questions: 0)).word, 'Needs you');
    expect(orderPill(_draft(status: 'confirmed', more: {'order_status': 'expired'})), (word: 'Not paid in time', tone: CardTone.danger));
    expect(payChoices({'fulfilment': 'pickup'}, true), ['transfer', 'later']);
    expect(payChoices({'fulfilment': 'delivery'}, false), ['on_delivery', 'later']);
    expect(payNow({'fulfilment': 'pickup', 'pay': 'on_delivery'}, false), 'later');
    expect(lineWords({'item_id': 'x', 'name': 'Jollof', 'quantity': 2}), '2 × Jollof');
    expect(lineWords({'said': 'small chops'}), '“small chops”');
    expect(lineKobo({'item_id': 'x', 'quantity': 2, 'price_kobo': 500}), 1000);
    expect(lineNote({'said': 'x'}), 'Not in your catalogue. Pick the item, or remove it.');
    expect(lineNote({'item_id': 'x', 'out': true, 'left': 2}), 'Only 2 left.');
    expect(workingOn([_draft(status: 'confirmed'), _draft(status: 'asked')])?['status'], 'asked');
  });

  Future<List<String>> open(WidgetTester t, List<Map<String, dynamic>> orders, {bool canSend = true, Map<String, Object?> confirm = const {}}) async {
    t.view.physicalSize = const Size(390, 2400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/customer-chats/C1/orders') {
          return ok({
            'orders': orders,
            'zones': [
              {'name': 'Yaba', 'fee_kobo': 100000},
              {'name': 'Lekki', 'fee_kobo': 250000},
            ],
            'pay_page': true,
            'items': [
              {'id': 'jr', 'name': 'Jollof rice', 'price_kobo': 500000},
              {'id': 'sc', 'name': 'Small chops', 'price_kobo': 300000},
            ],
          });
        }
        if (req.method == 'GET') return null;
        sent.add('${req.method} $p ${req.body}');
        return ok(p.endsWith('/confirm') ? confirm : {});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: ColoredBox(
          color: const Color(0xFFFFFFFF),
          child: SafeArea(
            child: SingleChildScrollView(child: CustomerOrders(auth: chats.auth, chatId: 'C1', canSend: canSend, follow: false)),
          ),
        ),
      ),
    );
    await settle(t);
    return sent;
  }

  testWidgets('the order read from their message: each answer sent on the version shown', (t) async {
    final sent = await open(t, [_draft()]);
    expect(find.text('ORDER FROM THEIR MESSAGE'), findsOneWidget);
    expect(find.text('4 items · ₦11,000'), findsOneWidget);
    expect(find.text('They wrote “jollof x2”'), findsOneWidget);
    expect(find.text('Not in your catalogue. Pick the item, or remove it.'), findsOneWidget);
    expect(find.text('tomorrow'), findsOneWidget);
    // Answered: a choice, an item picked, how many, one more.
    await t.tap(find.bySemanticsLabel('Chapman · ₦1,500'));
    await settle(t);
    expect(sent.last, 'PUT /v1/order-drafts/OD1 {"version":3,"op":"choose","line":1,"item_id":"ch"}');
    await t.tap(find.bySemanticsLabel('The item for “small chops”'));
    await t.pumpAndSettle();
    await t.tap(find.text('Small chops'));
    await settle(t);
    expect(sent.last, 'PUT /v1/order-drafts/OD1 {"version":3,"op":"choose","line":2,"item_id":"sc"}');
    await t.enterText(find.descendant(of: find.bySemanticsLabel('How many moi moi'), matching: find.byType(EditableText)), '3');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Set how many moi moi'));
    await settle(t);
    expect(sent.last, 'PUT /v1/order-drafts/OD1 {"version":3,"op":"quantity","line":3,"quantity":3}');
    await t.tap(find.bySemanticsLabel('One more Jollof rice'));
    await settle(t);
    expect(sent.last, 'PUT /v1/order-drafts/OD1 {"version":3,"op":"quantity","line":0,"quantity":3}');
    await t.tap(find.bySemanticsLabel('Remove small chops'));
    await settle(t);
    expect(sent.last, 'PUT /v1/order-drafts/OD1 {"version":3,"op":"remove","line":2}');
    // Where it goes, and how they pay.
    await t.tap(find.bySemanticsLabel('Delivery area: none picked'));
    await t.pumpAndSettle();
    await t.tap(find.text('Lekki · ₦2,500'));
    await settle(t);
    expect(sent.last, 'PUT /v1/order-drafts/OD1 {"version":3,"op":"details","zone":"Lekki","delivery_kobo":250000}');
    await t.ensureVisible(find.text('They pay when it arrives'));
    await t.pumpAndSettle();
    await t.tap(find.text('They pay when it arrives'));
    await settle(t);
    expect(sent.last, 'PUT /v1/order-drafts/OD1 {"version":3,"op":"details","pay":"on_delivery"}');
    // Not ready: Confirm waits, and says why; Ask them sends the questions.
    expect(find.text('Answer the questions first.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Ask them'));
    await settle(t);
    expect(sent.last, 'POST /v1/order-drafts/OD1/ask {"version":3}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('ready: confirmed; a send that failed said, and sent again', (t) async {
    final sent = await open(t, [_draft(questions: 0, ready: true)], confirm: {'told': false, 'told_error': 'Outside the 24 hours'});
    await t.tap(find.bySemanticsLabel('Confirm'));
    await settle(t);
    expect(sent.last, 'POST /v1/order-drafts/OD1/confirm {"version":3}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('nothing being worked on: the last confirmed, and Start an order', (t) async {
    final sent = await open(t, [_draft(status: 'confirmed', more: {'reference': 'OR-1', 'order_status': 'paid'})]);
    expect(find.text('ORDER OR-1'), findsOneWidget);
    expect(find.text('Paid'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Start an order'));
    await settle(t);
    expect(sent.single, 'POST /v1/customer-chats/C1/orders {}');
  });

  testWidgets('a customer who sent STOP: no Start an order', (t) async {
    await open(t, const [], canSend: false);
    expect(find.bySemanticsLabel('Start an order'), findsNothing);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async => req.url.path == '/v1/customer-chats/C1/orders'
          ? ok({'orders': [_draft()], 'zones': [{'name': 'Yaba', 'fee_kobo': 100000}], 'pay_page': true, 'items': [{'id': 'jr', 'name': 'Jollof rice'}]})
          : null),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: ColoredBox(color: const Color(0xFF000000), child: SafeArea(child: SingleChildScrollView(child: CustomerOrders(auth: chats.auth, chatId: 'C1', canSend: true, follow: false)))),
      ),
      drive: settle,
    );
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
