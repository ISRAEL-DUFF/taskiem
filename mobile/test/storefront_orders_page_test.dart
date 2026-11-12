// Orders from customers and deliveries (M7 parts S and D; web
// StorefrontOrders.tsx, Dispatch.tsx, lib/orders.ts, lib/dispatch.ts): the
// words, what each button sends, and what the screens say.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/storefront_orders_page.dart';
import 'package:taskiem/chat/words.dart' show Tone;
import 'package:taskiem/kit/modal.dart' show TkModal;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

final _now = DateTime.utc(2026, 11, 12, 12);

Map<String, dynamic> _order({String status = 'accepted', String kind = 'order', Map<String, dynamic> more = const {}}) => {
  'id': 'S1',
  'reference': 'OR-5K2Q',
  'kind': kind,
  'status': status,
  'customer': {'name': 'Ngozi', 'phone': '•••4567'},
  'basket': [
    {'name': 'Jollof rice', 'quantity': 2, 'line_kobo': 500000},
    {'name': 'Chapman', 'quantity': 1, 'line_kobo': 150000},
  ],
  'total_kobo': 650000,
  'fulfilment': 'delivery',
  'zone': 'Yaba',
  'delivery_kobo': 100000,
  'payment': 'at_order',
  'paid_at': '2026-11-12T10:00:00Z',
  'created_at': '2026-11-12T11:55:00Z',
  ...more,
};

void main() {
  setUpAll(loadFonts);

  test('an order’s words, as the web’s', () {
    expect(ordersQuery('open'), {'status': 'open'});
    expect(ordersQuery('all', 'F2'), {'front': 'F2'});
    expect(relativeAge('2026-11-12T11:59:30Z', _now), 'just now');
    expect(relativeAge('2026-11-12T11:55:00Z', _now), '5 min ago');
    expect(relativeAge('2026-11-11T11:00:00Z', _now), 'yesterday');
    expect(relativeAge('2026-10-01T11:00:00Z', _now), '1 Oct 2026');
    expect(timeLeft('2026-11-12T12:20:00Z', _now), 'in 20 min');
    expect(timeLeft('2026-11-12T11:00:00Z', _now), 'now');
    expect(orderTotal({'kind': 'booking', 'total_kobo': 0}), 'Price on the day');
    expect(orderTotal({'kind': 'enquiry', 'total_kobo': 0}), '—');
    expect(paymentSummary(_order()), 'Pays when ordering · paid');
    expect(paymentSummary({'kind': 'order', 'payment': 'on_delivery'}), 'Pays at the door · not paid');
    expect(paymentSummary({'kind': 'booking', 'payment': 'after_accept', 'deposit_kobo': 200000}), 'Pays after you confirm · deposit ₦2,000 · not paid');
    expect(orderWhat(_order()), '2 × Jollof rice, 1 × Chapman');
    expect(refundLine(650000, 'transfer'), startsWith('₦6,500 is due back to the customer. They paid by bank transfer'));
    expect(whatsappLink('0803 123 4567'), 'https://wa.me/2348031234567');
    expect(whatsappLink('•••4567'), isNull);
    expect(deliveryStatus('picked_up'), (label: 'On the way', tone: Tone.info));
    final list = [
      {'id': 'a', 'status': 'declined', 'rider': 'Tunde'},
      {'id': 'b', 'status': 'offered', 'rider': 'Bayo'},
    ];
    expect(currentDelivery(list)?['id'], 'b');
    expect(deliveryStepsOf({'rider': 'Bayo', 'offered_at': '2026-11-12T10:00:00Z', 'delivered_at': '2026-11-12T11:00:00Z', 'paid_how': 'cash'}).map((s) => s.label), [
      'Offered to Bayo',
      'Delivered, paid in cash',
      'Confirmed, fee not paid yet',
    ].take(2));
    expect(offerOutcome({'status': 'offered', 'rider': 'Bayo'}), 'Offered to Bayo. They have a few minutes to accept.');
    expect(offerOutcome({'status': 'no_rider'}), 'No free rider covers this area now. You were told in chat.');
  });

  Future<({List<String> sent, ChatsData chats})> serve(
    WidgetTester t,
    Widget Function(ChatsData chats) home, {
    Map<String, dynamic>? order,
    List<Map<String, dynamic>> deliveries = const [],
    Map<String, Object?>? offer,
    bool replaces = false,
    Size size = const Size(390, 2000),
  }) async {
    t.view.physicalSize = size;
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    var asked = false;
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final o = order ?? _order();
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        final q = req.url.query;
        if (req.method == 'GET') {
          if (p == '/v1/storefront/orders') sent.add('GET $p $q');
          return switch (p) {
            '/v1/storefront/orders' => ok({
              'orders': [o],
            }),
            '/v1/storefront/orders/OR-5K2Q' => ok({
              'order': q.contains('reveal') ? {...o, 'customer': {'name': 'Ngozi Eze', 'phone': '08031234567'}} : o,
              'revealed': q.contains('reveal'),
              'payments': [
                {'id': 'P1', 'entry_id': 'E1', 'amount_kobo': 650000, 'how': 'suggested', 'by': 'Ada', 'reasons': ['Same amount'], 'undoable': true, 'created_at': '2026-11-12T10:00:00Z'},
              ],
            }),
            '/v1/storefront/orders/OR-5K2Q/deliveries' => ok({'deliveries': deliveries, 'not_ready': ''}),
            '/v1/storefront/fronts' => ok({'fronts': []}),
            '/v1/storefront' => ok({'published_version': 1, 'preview_url': 'https://ada.taskiem.com'}),
            '/v1/trade-orders' => ok({'orders': []}),
            '/v1/riders' => ok({
              'riders': [
                {'id': 'R2', 'name': 'Tunde', 'active': true, 'zones': [], 'presence': {'state': 'busy'}},
                {'id': 'R1', 'name': 'Bayo', 'active': true, 'zones': ['Yaba'], 'presence': {'state': 'free'}},
                {'id': 'R3', 'name': 'Gone', 'active': false, 'zones': [], 'presence': {'state': 'free'}},
              ],
            }),
            '/v1/deliveries' => ok({
              'deliveries': [
                {'id': 'D1', 'reference': 'OR-5K2Q', 'rider': 'Bayo', 'status': 'picked_up', 'fee_kobo': 150000, 'zone': 'Yaba', 'offered_at': '2026-11-12T10:00:00Z', 'late': 'arrival'},
              ],
            }),
            '/v1/dispatch/workflow' => ok({'on': false, 'workflow_id': null}),
            '/v1/dispatch/settings' => ok({
              'mode': 'rotation',
              'offer_minutes': 5,
              'pickup_minutes': 30,
              'arrive_minutes': 60,
              'proof': false,
              'owe_limit_kobo': 2000000,
              'rating_mark': 3,
            }),
            _ => null,
          };
        }
        sent.add('${req.method} $p ${req.body}');
        if (p == '/v1/dispatch/workflow' && replaces && !asked) {
          asked = true;
          return http.Response(jsonEncode({'error': 'It replaces a workflow', 'code': 'replaces_workflow', 'workflow': 'My orders'}), 409, headers: {'content-type': 'application/json'});
        }
        if (p.endsWith('/offer')) return ok(offer ?? {'status': 'offered', 'rider': 'Bayo'});
        return ok({});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return (sent: sent, chats: chats);
  }

  testWidgets('the list: each order in words; filtered by status; one opened', (t) async {
    final r = await serve(t, (c) => StorefrontOrdersPage(auth: c.auth, now: () => _now));
    expect(find.text('Ngozi · Order'), findsOneWidget);
    expect(find.text('•••4567 · OR-5K2Q · 2 × Jollof rice, 1 × Chapman · 5 min ago\n₦6,500 · Pays when ordering · paid'), findsOneWidget);
    expect(r.sent.last, 'GET /v1/storefront/orders status=open');
    await t.tap(find.bySemanticsLabel('Show: Open'));
    await t.pumpAndSettle();
    await t.tap(find.text('Refund due'));
    await settle(t);
    expect(r.sent.last, 'GET /v1/storefront/orders status=refund_due');
    await t.tap(find.text('Ngozi · Order'));
    await settle(t);
    expect(find.text('Order OR-5K2Q'), findsOneWidget);
  });

  testWidgets('an order: its details; offered to a free rider; the match undone; the customer shown', (t) async {
    final r = await serve(t, (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now));
    expect(find.text('Order total'), findsOneWidget);
    expect(find.text('2 × Jollof rice ₦5,000'), findsOneWidget);
    expect(find.text('Delivery to Yaba ₦1,000'), findsOneWidget);
    expect(find.text('No rider has it yet.'), findsOneWidget);
    expect(find.text('Suggested, confirmed by Ada'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Offer to a free rider'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/storefront/orders/OR-5K2Q/offer {}');
    expect(find.text('Offered to Bayo. They have a few minutes to accept.'), findsOneWidget);
    r.sent.clear();
    await t.tap(find.bySemanticsLabel('Choose the rider'));
    await settle(t);
    // Free first; those not working now left out.
    expect(t.getTopLeft(find.text('Bayo').last).dy, lessThan(t.getTopLeft(find.text('Tunde')).dy));
    expect(find.text('Gone'), findsNothing);
    await t.tap(find.bySemanticsLabel('Offer to Tunde'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/storefront/orders/OR-5K2Q/offer {"rider_id":"R2"}');
    r.sent.clear();
    await t.tap(find.bySemanticsLabel('Undo the match'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Undo the match').last);
    await settle(t);
    expect(r.sent.single, 'POST /v1/money/order-payments/P1/undo {}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Show customer’s details'));
    await settle(t);
    expect(find.text('Ngozi Eze'), findsOneWidget);
    expect(find.bySemanticsLabel('Call 08031234567'), findsOneWidget);
  });

  testWidgets('a rider on the way: their steps; taken back after yes; a delivered one confirmed', (t) async {
    final r = await serve(
      t,
      (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now),
      deliveries: [
        {
          'id': 'D1',
          'rider': 'Bayo',
          'rider_phone': '08030000000',
          'status': 'picked_up',
          'fee_kobo': 150000,
          'offered_at': '2026-11-12T10:00:00Z',
          'accepted_at': '2026-11-12T10:02:00Z',
          'picked_up_at': '2026-11-12T10:20:00Z',
        },
        {'id': 'D0', 'rider': 'Tunde', 'status': 'declined', 'offered_at': '2026-11-12T09:55:00Z'},
      ],
    );
    expect(find.text('On the way'), findsOneWidget);
    expect(find.text('₦1,500'), findsOneWidget);
    expect(find.textContaining('3. Picked up'), findsOneWidget);
    expect(find.bySemanticsLabel('Offer to a free rider'), findsNothing);
    await t.tap(find.bySemanticsLabel('Show earlier offers (1)'));
    await t.pump();
    expect(find.text('Said they can’t'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Take it back'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Take it back').last);
    await settle(t);
    expect(r.sent.single, 'POST /v1/storefront/orders/OR-5K2Q/release {}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a refund due by transfer: marked refunded after yes', (t) async {
    final r = await serve(
      t,
      (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now),
      order: _order(status: 'refund_due', more: {'paid_by': 'transfer'}),
    );
    expect(find.bySemanticsLabel('Refund through Paystack'), findsNothing);
    await t.tap(find.bySemanticsLabel('Mark refunded'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Not yet'));
    await t.pumpAndSettle();
    expect(r.sent, isEmpty);
    await t.tap(find.bySemanticsLabel('Mark refunded'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Mark refunded').last);
    await settle(t);
    expect(r.sent.single, 'POST /v1/storefront/orders/OR-5K2Q/refunded {}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a booking: its time, Move and Cancel', (t) async {
    await serve(
      t,
      (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now),
      order: _order(
        kind: 'booking',
        status: 'accepted',
        more: {'service_name': 'Braids', 'starts_at': '2099-11-14T09:00:00Z', 'ends_at': '2099-11-14T10:30:00Z', 'fulfilment': null, 'basket': null},
      ),
    );
    expect(find.text('Braids'), findsWidgets);
    expect(find.bySemanticsLabel('Move Braids OR-5K2Q'), findsOneWidget);
    expect(find.bySemanticsLabel('Cancel Braids OR-5K2Q'), findsOneWidget);
    expect(find.text('Delivery'), findsNothing);
  });

  testWidgets('deliveries: the day’s; under way; how orders are offered saved; turned on, replacing their workflow after yes', (t) async {
    final r = await serve(t, (c) => DeliveriesPage(auth: c.auth), replaces: true);
    expect(find.text('OR-5K2Q'), findsOneWidget);
    expect(find.textContaining('Bayo · Yaba · fee ₦1,500 · not arrived in time'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('How orders are offered'));
    await settle(t);
    final boxes = find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText));
    await t.enterText(boxes.at(0), '90');
    await t.pump();
    expect(find.text('Riders have 1 to 60 minutes to accept an offer.'), findsOneWidget);
    await t.enterText(boxes.at(0), '10');
    await t.pump();
    await t.tap(find.text('Ask me in chat who takes it'));
    await t.pump();
    await t.tap(find.descendant(of: find.byType(TkModal), matching: find.bySemanticsLabel('Save')));
    await settle(t);
    expect(
      r.sent.single,
      'PUT /v1/dispatch/settings {"mode":"ask","offer_minutes":10,"pickup_minutes":30,"arrive_minutes":60,"proof":false,"owe_limit_kobo":2000000,"rating_mark":3}',
    );
    await t.pump(const Duration(seconds: 5));
    r.sent.clear();
    await t.tap(find.bySemanticsLabel('Turn on deliveries'));
    await settle(t);
    expect(find.textContaining('“My orders”'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Go ahead'));
    await settle(t);
    expect(r.sent, ['POST /v1/dispatch/workflow {}', 'POST /v1/dispatch/workflow {"replace":true}']);
    expect(find.bySemanticsLabel('Turn on deliveries'), findsNothing);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Assign rider: the next in turn; why not when none could take it', (t) async {
    final r = await serve(
      t,
      (c) => RiderSheet(auth: c.auth, reference: 'OR-5K2Q', line: 'OR-5K2Q needs a rider'),
      offer: {'status': 'no_rider'},
    );
    expect(find.text('A rider for OR-5K2Q'), findsOneWidget);
    await t.tap(find.text('Next in turn'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/storefront/orders/OR-5K2Q/offer {}');
    expect(find.text('No rider could take it. You are told when one is free'), findsOneWidget);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async => switch (req.url.path) {
        '/v1/storefront/orders/OR-5K2Q' => ok({'order': _order(status: 'refund_due'), 'revealed': false}),
        '/v1/storefront/orders/OR-5K2Q/deliveries' => ok({
          'deliveries': [
            {'id': 'D1', 'rider': 'Bayo', 'status': 'delivered', 'fee_kobo': 150000, 'offered_at': '2026-11-12T10:00:00Z', 'delivered_at': '2026-11-12T11:00:00Z'},
          ],
          'not_ready': '',
        }),
        '/v1/storefront/orders' => ok({'orders': [_order()]}),
        '/v1/deliveries' => ok({'deliveries': []}),
        '/v1/dispatch/workflow' => ok({'on': false}),
        _ => null,
      }),
      now: () => f.at,
    );
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: StorefrontOrderPage(auth: chats.auth, reference: 'OR-5K2Q', now: () => _now)), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: StorefrontOrdersPage(auth: chats.auth, now: () => _now)), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: DeliveriesPage(auth: chats.auth)), drive: settle);
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
