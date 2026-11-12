// Orders from customers and deliveries (M7 parts S and D; web
// StorefrontOrders.tsx, Dispatch.tsx, lib/orders.ts, lib/dispatch.ts): the
// words, what each button sends, and what the screens say.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/bookings_page.dart' show orderStatus;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/trade_orders_page.dart' show TradeOrdersPage;
import 'package:taskiem/kit/list.dart' show TkRow;
import 'package:taskiem/chat/storefront_orders_page.dart';
import 'package:taskiem/chat/words.dart' show CardTone, Tone;
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
    List<String>? perms,
    Future<http.Response?> Function(http.Request req)? also,
  }) async {
    // Each its own screen, not the last one's kept.
    await t.pumpWidget(const SizedBox());
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
        final own = await also?.call(req);
        if (own != null) {
          if (req.method != 'GET') sent.add('${req.method} $p ${req.body}');
          return own;
        }
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
    if (perms != null) chats.auth.me = Me.fromJson({...f.json('/v1/me'), 'permissions': perms});
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

  test('a rider’s statuses in words; the offer’s other outcomes', () {
    expect(orderStatus('dispatched').label, 'With a rider');
    expect(orderStatus('on_the_way').label, 'On the way');
    expect(orderStatus('arrived').label, 'At the door');
    expect(orderStatus('proposed').label, 'Waiting for the business to confirm');
    expect(orderStatus('changed').label, 'Waiting for the business to accept your change');
    expect(offerOutcome({'status': 'held', 'rider': 'Bayo'}), 'Bayo has it already.');
    expect(offerOutcome({'status': 'asked_owner'}), 'You were asked in chat who takes it.');
  });

  http.Response json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

  testWidgets('the delivery reads again with the order: a rider’s Delivered, and Confirm the fee, show', (t) async {
    var status = 'picked_up';
    final r = await serve(
      t,
      (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now),
      also: (req) async => req.method == 'GET' && req.url.path == '/v1/storefront/orders/OR-5K2Q/deliveries'
          ? json({
              'deliveries': [
                {'id': 'D1', 'rider': 'Bayo', 'status': status, 'fee_kobo': 150000, 'offered_at': '2026-11-12T10:00:00Z', 'delivered_at': status == 'delivered' ? '2026-11-12T11:00:00Z' : null},
              ],
              'not_ready': '',
            })
          : null,
    );
    expect(find.text('On the way'), findsOneWidget);
    status = 'delivered';
    await t.pump(const Duration(seconds: 30));
    await settle(t);
    expect(find.text('Delivered'), findsWidgets);
    await t.tap(find.bySemanticsLabel('Paid in cash'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/storefront/orders/OR-5K2Q/confirm {"how":"cash"}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the customer’s Call and WhatsApp are each pressed; a masked rider phone is words, not a call', (t) async {
    final called = <String>[];
    final was = callPhone;
    callPhone = (p) async {
      called.add(p);
      return false;
    };
    addTearDown(() => callPhone = was);
    await serve(
      t,
      (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now),
      order: _order(more: {'customer': {'name': 'Ngozi', 'phone': '08031234567', 'email': 'ngozi@example.com'}}),
      deliveries: [
        {'id': 'D1', 'rider': 'Bayo', 'rider_phone': '•••0000', 'status': 'picked_up', 'fee_kobo': 0, 'offered_at': '2026-11-12T10:00:00Z'},
      ],
    );
    final call = t.getSemantics(find.bySemanticsLabel('Call 08031234567'));
    final wa = t.getSemantics(find.bySemanticsLabel('WhatsApp'));
    expect(call.isMergedIntoParent, isFalse);
    expect(wa.isMergedIntoParent, isFalse);
    expect(identical(call, wa), isFalse);
    expect(find.bySemanticsLabel('ngozi@example.com'), findsOneWidget);
    expect(find.text('Bayo · •••0000'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp(r'^Call •••')), findsNothing);
    await t.tap(find.bySemanticsLabel('Call 08031234567'));
    await t.pump();
    expect(called.single, '08031234567');
    expect(find.text('That number could not be called.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await expectPressable(t);
  });

  testWidgets('the order’s details: whether its workflow started; where it is decided', (t) async {
    await serve(t, (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now), order: _order(more: {'run_id': 'R1'}));
    expect(find.text('Workflow'), findsOneWidget);
    expect(find.text('Started'), findsOneWidget);
    expect(find.text(decideWhere), findsOneWidget);
    await serve(t, (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now));
    expect(find.text('Not started yet'), findsOneWidget);
  });

  testWidgets('without storefront.manage: no refund, no fee to confirm, no delivery settings, no turning on', (t) async {
    await serve(
      t,
      (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now),
      order: _order(status: 'refund_due'),
      perms: ['order.handle'],
      deliveries: [
        {'id': 'D1', 'rider': 'Bayo', 'status': 'delivered', 'fee_kobo': 150000, 'offered_at': '2026-11-12T10:00:00Z', 'delivered_at': '2026-11-12T11:00:00Z'},
      ],
    );
    expect(find.textContaining('is due back to the customer'), findsOneWidget);
    expect(find.bySemanticsLabel('Mark refunded'), findsNothing);
    expect(find.bySemanticsLabel('Refund through Paystack'), findsNothing);
    expect(find.bySemanticsLabel('Confirm, pay the fee'), findsNothing);
    expect(find.bySemanticsLabel('Paid in cash'), findsNothing);
    // Not shown without pii.reveal: the words why; no Undo without invoice.manage.
    expect(find.text('The customer’s details are hidden. Someone who may see personal data can show them.'), findsOneWidget);
    expect(find.bySemanticsLabel('Undo the match'), findsNothing);
    await serve(t, (c) => DeliveriesPage(auth: c.auth), perms: ['order.handle']);
    expect(find.bySemanticsLabel('How orders are offered'), findsNothing);
    expect(find.bySemanticsLabel('Turn on deliveries'), findsNothing);
  });

  testWidgets('riders off on this Taskiem (409): no Delivery card; Deliveries says why', (t) async {
    http.Response? off(http.Request req) =>
        req.url.path.endsWith('/deliveries') ? json({'error': 'deliveries are not configured'}, 409) : null;
    await serve(t, (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now), also: (req) async => off(req));
    expect(find.text('No rider has it yet.'), findsNothing);
    expect(find.bySemanticsLabel('Offer to a free rider'), findsNothing);
    await serve(t, (c) => DeliveriesPage(auth: c.auth), also: (req) async => off(req));
    expect(find.text('Deliveries need text messages, which are not set up on this Taskiem yet.'), findsOneWidget);
  });

  testWidgets('turning on refused: the words stay in the card after the page reads again', (t) async {
    await serve(
      t,
      (c) => DeliveriesPage(auth: c.auth),
      also: (req) async => req.method == 'POST' && req.url.path == '/v1/dispatch/workflow' ? json({'error': 'Connect WhatsApp first.'}, 422) : null,
    );
    await t.tap(find.bySemanticsLabel('Turn on deliveries'));
    await settle(t);
    expect(find.text('Connect WhatsApp first.'), findsOneWidget);
    await t.pump(const Duration(seconds: 30));
    await settle(t);
    expect(find.text('Connect WhatsApp first.'), findsOneWidget);
  });

  testWidgets('under way now, then off: today’s list again, no day kept', (t) async {
    final asked = <String>[];
    await serve(
      t,
      (c) => DeliveriesPage(auth: c.auth),
      also: (req) async {
        if (req.url.path == '/v1/deliveries') asked.add(req.url.query);
        return null;
      },
    );
    await t.enterText(find.byType(EditableText), '2026-11-10');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Show that day'));
    await settle(t);
    expect(asked.last, 'day=2026-11-10');
    await t.tap(find.bySemanticsLabel('Under way now'));
    await settle(t);
    expect(asked.last, 'open=true');
    await t.tap(find.bySemanticsLabel('Under way now'));
    await settle(t);
    expect(asked.last, '');
  });

  testWidgets('the list: an error then “takes no orders” shows only the latter; waiting orders amber, a refund red', (t) async {
    var n = 0;
    await serve(
      t,
      (c) => StorefrontOrdersPage(auth: c.auth, now: () => _now),
      also: (req) async {
        if (req.url.path != '/v1/storefront/orders') return null;
        n++;
        return n == 1 ? json({'error': 'Try again.'}, 500) : json({'error': 'no storefront'}, 404);
      },
    );
    expect(find.text('Try again.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Refresh'));
    await settle(t);
    expect(find.text('Try again.'), findsNothing);
    expect(find.text('This Taskiem does not take storefront orders yet.'), findsOneWidget);
    await serve(t, (c) => StorefrontOrdersPage(auth: c.auth, now: () => _now), order: _order(status: 'received'));
    expect(t.widget<TkRow>(find.byType(TkRow)).tone, CardTone.warn);
    await serve(t, (c) => StorefrontOrdersPage(auth: c.auth, now: () => _now), order: _order(status: 'refund_due'));
    expect(t.widget<TkRow>(find.byType(TkRow)).tone, CardTone.danger);
  });

  testWidgets('from more than one front: the From filter asks for that front', (t) async {
    final r = await serve(
      t,
      (c) => StorefrontOrdersPage(auth: c.auth, now: () => _now),
      also: (req) async => req.url.path == '/v1/storefront/fronts'
          ? json({
              'fronts': [
                {'id': 'F1', 'name': 'Main'},
                {'id': 'F2', 'name': 'Lekki branch'},
              ],
            })
          : null,
    );
    await t.tap(find.bySemanticsLabel('From: Every front'));
    await t.pumpAndSettle();
    await t.tap(find.text('Lekki branch'));
    await settle(t);
    expect(r.sent.last, 'GET /v1/storefront/orders status=open&front=F2');
  });

  testWidgets('a slow read does not undo a newer one', (t) async {
    var n = 0;
    final slow = Completer<http.Response?>();
    await serve(
      t,
      (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now),
      also: (req) async {
        if (req.method != 'GET' || req.url.path != '/v1/storefront/orders/OR-5K2Q') return null;
        n++;
        if (n == 2) return slow.future;
        return n == 1 ? json({'order': _order()}) : json({'order': _order(status: 'completed')});
      },
    );
    // A poll (2nd, slow) then a pull to refresh (3rd, fast).
    await t.pump(const Duration(seconds: 30));
    await t.fling(find.byType(Scrollable).first, const Offset(0, 400), 1000);
    await settle(t);
    expect(find.text('Completed'), findsWidgets);
    slow.complete(json({'order': _order()}));
    await settle(t);
    expect(find.text('Completed'), findsWidgets);
  });

  testWidgets('Orders from linked businesses leads to all your orders; one opened from it says Back to Orders', (t) async {
    await serve(t, (c) => TradeOrdersPage(auth: c.auth));
    await t.tap(find.bySemanticsLabel('All your orders'));
    await settle(t);
    expect(find.text('Orders'), findsWidgets);
    await t.tap(find.text('Ngozi · Order'));
    await settle(t);
    expect(find.bySemanticsLabel('Back to Orders'), findsOneWidget);
    await serve(t, (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now));
    expect(find.bySemanticsLabel('Back'), findsOneWidget);
  });

  testWidgets('a booking cancelled says so', (t) async {
    await serve(
      t,
      (c) => StorefrontOrderPage(auth: c.auth, reference: 'OR-5K2Q', now: () => _now),
      order: _order(kind: 'booking', more: {'service_name': 'Braids', 'starts_at': '2099-11-14T09:00:00Z', 'ends_at': '2099-11-14T10:30:00Z', 'fulfilment': null, 'basket': null}),
      also: (req) async => req.method == 'POST' && req.url.path.endsWith('/cancel') ? json({'booking': {'reference': 'OR-5K2Q', 'status': 'cancelled'}}) : null,
    );
    await t.tap(find.bySemanticsLabel('Cancel Braids OR-5K2Q'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Cancel the booking'));
    await settle(t);
    expect(find.text('Booking cancelled'), findsOneWidget);
    expect(find.text('OR-5K2Q is cancelled; the customer is told.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the rider sheet at large text', (t) async {
    final f = Fixture('chats');
    final chats = ChatsData(
      f.auth(also: (req) async => req.url.path == '/v1/riders' ? json({'riders': [{'id': 'R1', 'name': 'Bayo', 'active': true, 'zones': ['Yaba', 'Surulere'], 'presence': {'state': 'free'}}]}) : null),
      now: () => f.at,
    );
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: RiderSheet(auth: chats.auth, reference: 'OR-5K2Q', line: 'OR-5K2Q needs a rider')), drive: settle);
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
