// Orders from linked businesses, the seller's side (M7 part T; web
// TradeOrders.tsx, lib/tradeorders.ts): what each step sends, and what the
// screen says.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/openers.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/trade_orders_page.dart';
import 'package:taskiem/chat/words.dart' show CardTone;
import 'package:taskiem/kit/modal.dart' show TkModal;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

Map<String, dynamic> _order(String status, {Map<String, dynamic> more = const {}}) => {
  'id': 'T1',
  'order_reference': 'AD-7Q4K2P',
  'reference': 'PO-12',
  'buyer_name': 'Rivers Foods',
  'link': 'L1',
  'status': status,
  'total_kobo': 1500000,
  'invoice_at': 'delivered',
  'auto_accept': false,
  'placed_by_seller': false,
  'created_at': '2026-11-12T09:00:00Z',
  'updated_at': '2026-11-12T09:00:00Z',
  'note': 'By Friday',
  'lines': [
    {'item': 'Rice 50kg', 'unit': 'bag', 'quantity': 2, 'price_kobo': 500000, 'item_id': 'rice', 'matched': 'name'},
    {'item': 'Beans', 'quantity': 5, 'price_kobo': 100000, 'proposed_kobo': 90000},
  ],
  ...more,
};

void main() {
  setUpAll(loadFonts);

  test('an order’s words: its status, the seller’s steps and what it asks', () {
    expect(tradeStatus('received'), (label: 'New', tone: CardTone.warn));
    expect(tradeStatus('dispatched').label, 'On its way');
    final s = sellerSteps(_order('accepted', more: {'cancel_asked_at': 'x'}));
    expect([s.accept, s.ready, s.dispatched, s.delivered, s.cancel, s.keep], [false, true, true, true, true, true]);
    expect(sellerAsk(_order('received')), 'Accept it, change it, or decline it.');
    expect(sellerAsk(_order('accepted', more: {'cancel_asked_at': 'x'})), 'Rivers Foods asks to cancel it. Cancel it, or keep it.');
    expect(sellerAsk(_order('delivered', more: {'short_at': 'x'})), 'Rivers Foods says not all of it came. Agree their count, or talk to them.');
    expect(sellerSteps(_order('delivered', more: {'short_at': 'x'})).agree, isTrue);
    expect(
      linesWords([
        {'item': 'Air Max – 42', 'quantity': 2, 'unit': 'pair', 'item_id': 'a42'},
        {'item': 'Air Max – 43', 'quantity': 3, 'unit': 'pair', 'item_id': 'a43'},
        {'item': 'Rice', 'quantity': 1, 'unit': 'bag'},
      ], {'a42': 'am', 'a43': 'am'}),
      '5 pairs Air Max in 2 sizes and colours, 1 bag Rice',
    );
    expect(daysValue(''), isNull);
    expect(daysValue('30'), 30);
    expect(daysValue('400'), -1);
    expect(daysValue('x'), -1);
  });

  Future<List<String>> open(
    WidgetTester t,
    Map<String, dynamic> order, {
    bool list = false,
    Size size = const Size(390, 2600),
    bool none = false,
    bool noBuyers = false,
    List<String>? perms,
  }) async {
    // Each opening its own screen, not the last one's kept.
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = size;
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    var o = order;
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET') {
          return switch (p) {
            '/v1/trade-orders' => ok({
              'orders': [if (!none) o],
            }),
            '/v1/trade-orders/T1' => ok({'order': o}),
            '/v1/stock' => ok({
              'keeps_stock': true,
              'items': [
                {'id': 'rice', 'name': 'Rice, 50kg bag'},
                {'id': 'beans', 'name': 'Beans, oloyin'},
              ],
            }),
            '/v1/links/L1/trade' => ok({'terms_days': 30, 'credit_limit_kobo': null}),
            '/v1/link-chats/thread' => ok({
              'messages': [
                {
                  'id': 'm1',
                  'link': 'L1',
                  'kind': 'card',
                  'mine': false,
                  'created_at': '2026-11-12T09:00:00Z',
                  'card': {'doc': 'order', 'event': 'ordered', 'reference': 'PO-12', 'id': 'T1', 'amount_kobo': 1500000},
                },
              ],
            }),
            '/v1/links' => ok({
              'links': [
                if (!noBuyers) {'id': 'L1', 'name': 'Rivers Foods', 'state': 'active', 'role': 'customer'},
                {'id': 'L2', 'name': 'Eko Supplies', 'state': 'active', 'role': 'supplier'},
              ],
              'sent': [],
            }),
            _ => null,
          };
        }
        sent.add('${req.method} $p ${req.body}');
        if (p.endsWith('/accept')) o = {...o, 'status': 'accepted'};
        return ok({});
      }),
      now: () => f.at,
    );
    if (perms != null) chats.auth.me = Me.fromJson({...f.json('/v1/me'), 'permissions': perms});
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: list ? TradeOrdersPage(auth: chats.auth) : TradeOrderPage(auth: chats.auth, id: 'T1'),
      ),
    );
    await settle(t);
    return sent;
  }

  testWidgets('a new order: what it needs, its lines; an item matched; accepted', (t) async {
    final sent = await open(t, _order('received'));
    expect(find.text('PO-12 from Rivers Foods'), findsOneWidget);
    expect(find.text('₦15,000'), findsWidgets);
    expect(find.text('Accept it, change it, or decline it.'), findsOneWidget);
    expect(find.text('Their note: “By Friday”'), findsOneWidget);
    expect(find.text('Messages about it'), findsOneWidget, reason: 'the order’s thread, under its steps');
    expect(find.text('2 bags at ₦5,000'), findsOneWidget);
    expect(find.text('5 at ₦1,000 · they proposed ₦900'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Yes, that is it for Rice 50kg'));
    await settle(t);
    expect(sent.single, 'PUT /v1/trade-orders/T1/lines/0/item {"item_id":"rice"}');
    sent.clear();
    await t.tap(find.bySemanticsLabel('Their Beans: Not one of mine'));
    await t.pumpAndSettle();
    await t.tap(find.text('Beans, oloyin'));
    await settle(t);
    expect(sent.single, 'PUT /v1/trade-orders/T1/lines/1/item {"item_id":"beans"}');
    sent.clear();
    await t.tap(find.bySemanticsLabel('Accept'));
    await settle(t);
    expect(sent.single, 'POST /v1/trade-orders/T1/accept {}');
    expect(find.text('PO-12 accepted'), findsOneWidget);
    expect(find.text('Accepted'), findsWidgets);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a change at our prices, and a decline with why', (t) async {
    final sent = await open(t, _order('received'));
    await t.tap(find.bySemanticsLabel('Change'));
    await t.pumpAndSettle();
    final boxes = find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText));
    await t.enterText(boxes.at(2), '0');
    await t.enterText(boxes.at(4), 'Beans are out');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send the change'));
    await settle(t);
    expect(
      sent.single,
      'POST /v1/trade-orders/T1/change {"lines":[{"item":"Rice 50kg","unit":"bag","quantity":2,"price_kobo":500000},{"item":"Beans","quantity":0,"price_kobo":100000}],"note":"Beans are out"}',
    );
    await t.pump(const Duration(seconds: 5));
    sent.clear();
    await t.tap(find.bySemanticsLabel('Decline'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText)), 'No stock');
    await t.tap(find.bySemanticsLabel('Decline the order'));
    await settle(t);
    expect(sent.single, 'POST /v1/trade-orders/T1/decline {"note":"No stock"}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('an accepted order: ready, delivered with what went, cancelled after yes', (t) async {
    final sent = await open(t, _order('accepted'));
    expect(find.bySemanticsLabel('Accept'), findsNothing);
    await t.tap(find.bySemanticsLabel('It is ready'));
    await settle(t);
    expect(sent.single, 'POST /v1/trade-orders/T1/ready {}');
    await t.pump(const Duration(seconds: 5));
    sent.clear();
    await t.tap(find.bySemanticsLabel('Delivered'));
    await t.pumpAndSettle();
    expect(find.text('Rice 50kg (ordered 2 bags)'), findsOneWidget);
    await t.enterText(find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText)).at(1), '4');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Record the delivery'));
    await settle(t);
    expect(sent.single, 'POST /v1/trade-orders/T1/delivered {"delivered":[2,4]}');
    await t.pump(const Duration(seconds: 5));
    sent.clear();
    await t.tap(find.bySemanticsLabel('Cancel the order'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Keep it'));
    await t.pumpAndSettle();
    expect(sent, isEmpty);
  });

  testWidgets('their terms: days to pay checked, saved; invoice raised when accepted; accepted by themselves', (t) async {
    final sent = await open(t, _order('accepted'));
    final days = find.descendant(of: find.bySemanticsLabel(RegExp('^Days to pay')), matching: find.byType(EditableText));
    expect(find.text('30'), findsOneWidget);
    await t.enterText(days, '400');
    await t.pump();
    expect(find.text('From 0 to 365 days.'), findsOneWidget);
    await t.enterText(days, '14');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp(r'^Credit limit')), matching: find.byType(EditableText)), '500,000');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save terms'));
    await settle(t);
    expect(sent.single, 'PUT /v1/links/L1/trade {"terms_days":14,"credit_limit_kobo":50000000}');
    await t.pump(const Duration(seconds: 5));
    sent.clear();
    await t.tap(find.bySemanticsLabel('Raise the invoice: Once the delivery is received'));
    await t.pumpAndSettle();
    await t.tap(find.text('When I accept an order'));
    await settle(t);
    expect(sent.single, 'PUT /v1/links/L1/trade {"invoice_at":"accepted"}');
    await t.pump(const Duration(seconds: 5));
    sent.clear();
    await t.tap(find.bySemanticsLabel('Accept their orders by themselves when every item has a price').last);
    await settle(t);
    expect(sent.single, 'PUT /v1/links/L1/trade {"auto_accept":true}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the list: each order, opened; an order drafted for a business that buys from us', (t) async {
    final sent = await open(t, _order('received'), list: true);
    expect(find.text('Rivers Foods'), findsOneWidget);
    expect(find.text('PO-12 · 2 bags Rice 50kg, 5 Beans · ₦15,000'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Draft an order for them'));
    await settle(t);
    expect(find.bySemanticsLabel('Which business: Rivers Foods'), findsOneWidget);
    final boxes = find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText));
    await t.enterText(boxes.at(0), 'Palm oil');
    await t.enterText(boxes.at(1), '12,000');
    await t.enterText(boxes.at(2), '3');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send for them to confirm'));
    await settle(t);
    expect(sent.single, 'POST /v1/trade-orders {"link":"L1","lines":[{"item":"Palm oil","quantity":3,"price_kobo":1200000}],"note":""}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.text('Rivers Foods'));
    await settle(t);
    expect(find.text('PO-12 from Rivers Foods'), findsOneWidget);
  });

  testWidgets('their terms typed and not saved stay through another change', (t) async {
    final sent = await open(t, _order('accepted'));
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Days to pay')), matching: find.byType(EditableText)), '14');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Accept their orders by themselves when every item has a price').last);
    await settle(t);
    expect(sent.single, 'PUT /v1/links/L1/trade {"auto_accept":true}');
    expect(find.text('14'), findsOneWidget, reason: 'typed, not saved: kept');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('past their credit limit: the notice, and their statement (with money, coming)', (t) async {
    await open(t, _order('received', more: {'over_limit_at': '2026-11-12T09:00:00Z'}));
    expect(find.textContaining('past the credit limit you give them'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Their statement').first);
    await t.pump();
    expect(find.text('This is coming to the app soon.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('kept after they asked to cancel; their count agreed; cancelled after yes', (t) async {
    var sent = await open(t, _order('accepted', more: {'cancel_asked_at': '2026-11-12T10:00:00Z'}));
    expect(find.text('Rivers Foods asks to cancel it. Cancel it, or keep it.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Keep the order'));
    await settle(t);
    expect(sent.single, 'POST /v1/trade-orders/T1/keep {}');
    await t.pump(const Duration(seconds: 5));
    sent.clear();
    await t.tap(find.bySemanticsLabel('Cancel the order'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Cancel the order').last);
    await settle(t);
    expect(sent.single, 'POST /v1/trade-orders/T1/cancel {}');
    await t.pump(const Duration(seconds: 5));
    sent = await open(t, _order('delivered', more: {'short_at': '2026-11-12T10:00:00Z', 'short': [2, 4]}));
    expect(find.text('2 bags at ₦5,000 · they counted 2'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Agree their count'));
    await settle(t);
    expect(sent.single, 'POST /v1/trade-orders/T1/agree {}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('without storefront.manage no answers; without link.manage no terms', (t) async {
    await open(t, _order('received'), perms: ['link.manage']);
    expect(find.text('Answer Rivers Foods'), findsNothing);
    expect(find.text('Your items'), findsNothing);
    expect(find.text('Rivers Foods’s orders'), findsOneWidget);
    await open(t, _order('received'), perms: ['storefront.manage']);
    expect(find.text('Answer Rivers Foods'), findsOneWidget);
    expect(find.text('Rivers Foods’s orders'), findsNothing);
  });

  testWidgets('Record the delivery waits for every count', (t) async {
    final sent = await open(t, _order('accepted'));
    await t.tap(find.bySemanticsLabel('Delivered'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText)).at(0), '');
    await t.pump();
    expect(find.text('How many is a whole number.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Record the delivery'));
    await settle(t);
    expect(sent, isEmpty);
  });

  testWidgets('no orders yet: Linked businesses; a draft with no buyers: Find them', (t) async {
    final opened = <String>[];
    Openers.linkedBusinesses = (_) => opened.add('links');
    Openers.findBusiness = (_) => opened.add('explore');
    addTearDown(() => (Openers.linkedBusinesses = null, Openers.findBusiness = null));
    await open(t, _order('received'), list: true, none: true, noBuyers: true);
    expect(find.text('No orders yet'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Linked businesses'));
    expect(opened, ['links']);
    await t.tap(find.bySemanticsLabel('Draft an order for them'));
    await settle(t);
    expect(find.text('You are not linked with a business that buys from you yet.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Find them'));
    await t.pumpAndSettle();
    expect(opened, ['links', 'explore']);
    expect(find.byType(TkModal), findsNothing);
  });

  testWidgets('an order drafted from our own item: the item and how many, no price', (t) async {
    final sent = await open(t, _order('received'), list: true);
    await t.tap(find.bySemanticsLabel('Draft an order for them'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Item 1, your item'));
    await t.pumpAndSettle();
    await t.tap(find.text('Rice, 50kg bag'));
    await t.pumpAndSettle();
    final boxes = find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText));
    await t.enterText(boxes.at(0), '4');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send for them to confirm'));
    await settle(t);
    expect(sent.single, 'POST /v1/trade-orders {"link":"L1","lines":[{"item_id":"rice","quantity":4}],"note":""}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async => switch (req.url.path) {
        '/v1/trade-orders/T1' => ok({'order': _order('received', more: {'over_limit_at': 'x'})}),
        '/v1/stock' => ok({'keeps_stock': true, 'items': [{'id': 'rice', 'name': 'Rice, 50kg bag'}]}),
        '/v1/links/L1/trade' => ok({'terms_days': 30}),
        _ => null,
      }),
      now: () => f.at,
    );
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: TradeOrderPage(auth: chats.auth, id: 'T1')), drive: settle);
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
