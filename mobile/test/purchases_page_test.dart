// Purchases and suppliers (M8 part P; web TradeOrders.tsx PurchasePage,
// StockPurchases.tsx, StorefrontSuppliers.tsx; lib/purchases.ts,
// lib/tradeorders.ts): what each step sends, and what each screen says.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/purchases_page.dart';
import 'package:taskiem/kit/page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

Map<String, dynamic> _purchase({
  String id = 'P1',
  String status = 'sent',
  String? seller,
  String link = 'L1',
  List<Map<String, dynamic>>? change,
  int owed = 0,
}) => {
  'id': id,
  'reference': 'PO-0012',
  'supplier': 'Bayo Rice Mills',
  'status': status,
  'seller_status': ?seller,
  'link_id': link.isEmpty ? null : link,
  'total_kobo': 15600000,
  'received_kobo': 0,
  'owed_kobo': owed,
  'paid_kobo': 0,
  'created_at': '2026-11-10T09:00:00Z',
  'sent_at': '2026-11-10T09:05:00Z',
  'delivered_at': seller == 'delivered' ? '2026-11-12T08:00:00Z' : null,
  'change': ?change,
  'lines': [
    {'item_id': 'IT1', 'item': 'Rice 50kg', 'quantity': 2, 'unit': 'bags', 'cost_kobo': 7800000, 'received': 0, 'delivered': 2},
  ],
};

void main() {
  setUpAll(loadFonts);

  test('an order in words; what may be done with it', () {
    expect(sellerLine(_purchase(seller: 'changed')), 'Bayo Rice Mills asks to change it.');
    expect(sellerLine(_purchase(link: '')), '');
    expect(sellerLine(_purchase()), 'On its way to Bayo Rice Mills on Taskiem.');
    expect(purchaseStatus('part_received').label, 'Part received');
    final unlinked = purchaseActions(_purchase(link: ''));
    expect((unlinked.confirm, unlinked.receive, unlinked.cancel), (true, true, true));
    final linked = purchaseActions(_purchase(seller: 'accepted'));
    expect((linked.confirm, linked.receive), (false, false), reason: 'a linked supplier’s order is received by its own steps');
    expect(purchaseActions(_purchase(seller: 'delivered')).answerDelivery, isTrue);
    expect(purchaseSub(_purchase(link: '', owed: 100000)), contains('owed ₦1,000'));
    expect(receiveProblem(['', 'x']), 'How many came is a whole number, like 20.');
    expect(receiveProblem(['0']), 'Say how many came of at least one item.');
    expect(receiveProblem(['3']), isNull);
    expect(payProblem(''), isNotNull);
    expect(payProblem('4250'), isNull);
    expect(supplierProblem('Dayo', '0803', true), 'Enter a mobile number, like 0803 123 4567.');
    expect(supplierProblem('Dayo', '', false), isNull);
    expect(
      supplierSub({'phone': '0803', 'reached': 'link', 'open': 2}, 50000),
      '0803 · Linked on Taskiem: orders go straight to their business · 2 open orders · you owe ₦500',
    );
  });

  Future<List<String>> open(
    WidgetTester t,
    Widget Function(ChatsData c) home, {
    Map<String, dynamic>? purchase,
    List<Map<String, dynamic>>? list,
    bool keeps = true,
    bool linksFail = false,
  }) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 2600);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET') {
            return switch (p) {
              '/v1/stock/purchases' => ok({
                'purchases': list ?? [purchase ?? _purchase()],
              }),
              '/v1/stock' => ok({
                'keeps_stock': keeps,
                'items': [
                  {'id': 'MY1', 'name': 'rice 50kg'},
                  {'id': 'MY2', 'name': 'Beans'},
                ],
              }),
              '/v1/stock/suppliers' => ok({
                'suppliers': [
                  {'id': 'S1', 'name': 'Bayo Rice Mills', 'phone': '0803', 'reached': 'link', 'link_id': 'L1', 'open': 1, 'owed_kobo': 0},
                  {'id': 'S2', 'name': 'Dangote Depot', 'phone': '0805', 'reached': 'sms', 'open': 0, 'owed_kobo': 300000},
                ],
                'settings': {'purchase_limit_kobo': 50000000},
              }),
              '/v1/bills' => ok({
                'bills': [
                  {'link': 'L1', 'supplier': 'B', 'supplier_name': 'Bayo Rice Mills', 'status': 'open', 'due_kobo': 2000000},
                ],
              }),
              '/v1/stock/deliver-to' => ok({'address': '', 'phone': ''}),
              '/v1/links' when linksFail => http.Response(jsonEncode({'error': 'Something broke.'}), 500, headers: {'content-type': 'application/json'}),
              '/v1/links' => ok({
                'links': [
                  {'id': 'L9', 'name': 'Dangote Depot Ltd', 'state': 'active', 'role': 'supplier'},
                ],
              }),
              _ when p.startsWith('/v1/stock/purchases/') => ok({'purchase': purchase ?? _purchase()}),
              _ => null,
            };
          }
          sent.add('${req.method} $p ${req.body}');
          if (p == '/v1/stock/purchases/low') {
            return ok({
              'purchases': [],
              'no_supplier': ['Garri'],
            });
          }
          return ok({'ok': true});
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('their draft confirmed: each line is one of our items', (t) async {
    final sent = await open(
      t,
      (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false),
      purchase: _purchase(status: 'drafted', seller: 'proposed'),
    );
    expect(find.textContaining('drafted this order for you'), findsOneWidget);
    expect(find.bySemanticsLabel('Their Rice 50kg: rice 50kg'), findsOneWidget, reason: 'the same name, chosen');
    await t.tap(find.bySemanticsLabel('Confirm the order'));
    await settle(t);
    await snap(t, 'purchases_page-0');
    expect(sent.single, 'POST /v1/stock/purchases/P1/confirm-proposed {"items":["MY1"]}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('their change accepted; a cancel asked after yes', (t) async {
    final sent = await open(
      t,
      (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false),
      purchase: _purchase(
        seller: 'changed',
        change: [
          {'item_id': 'IT1', 'item': 'Rice 50kg', 'quantity': 1, 'unit': 'bags', 'cost_kobo': 8000000},
        ],
      ),
    );
    expect(find.text('Bayo Rice Mills can supply it like this'), findsOneWidget);
    expect(find.text('₦80,000'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Accept the change'));
    await settle(t);
    await snap(t, 'purchases_page-1');
    expect(sent.last, 'POST /v1/stock/purchases/P1/accept-change {}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Cancel the order'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Cancel the order').last);
    await settle(t);
    expect(sent.last, 'POST /v1/stock/purchases/P1/cancel {}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('delivered: not all came, with our count', (t) async {
    final sent = await open(
      t,
      (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false),
      purchase: _purchase(seller: 'delivered'),
    );
    expect(find.bySemanticsLabel('All came'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Not all came'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp(r'^Rice 50kg \(delivered 2\)')), matching: find.byType(EditableText)), '1');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Record what came'));
    await settle(t);
    await snap(t, 'purchases_page-2');
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/stock/purchases/P1/not-all-came {"counts":[1]}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the Stock screen’s orders: what is low ordered; goods in; a payment', (t) async {
    final sent = await open(
      t,
      (c) => TkPageScreen(
        title: 'Stock',
        children: [PurchasesCard(auth: c.auth, items: const [], onChanged: () {})],
      ),
      purchase: _purchase(link: '', owed: 15600000),
    );
    await t.tap(find.bySemanticsLabel('Order what is low'));
    await settle(t);
    expect(sent.last, 'POST /v1/stock/purchases/low {}');
    expect(find.textContaining('Garri has no supplier Taskiem knows'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Goods came in on PO-0012'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Record'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/stock/purchases/P1/receive {"lines":[{"item_id":"IT1","quantity":2}]}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Record a payment for PO-0012'));
    await t.pumpAndSettle();
    await t.tap(find.text('Cash'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Record the payment'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/stock/purchases/P1/pay {"amount_kobo":15600000,"paid_how":"cash"}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('suppliers: owed, linked, retired; the limit', (t) async {
    final sent = await open(t, (c) => SuppliersPage(auth: c.auth));
    expect(find.text('Owed ₦20,000'), findsOneWidget, reason: 'a linked supplier is owed its open bills');
    expect(find.text('Owed ₦3,000'), findsOneWidget);
    expect(find.bySemanticsLabel('Statement with Bayo Rice Mills'), findsOneWidget);
    expect(find.bySemanticsLabel('Statement with Dangote Depot'), findsNothing, reason: 'not linked');
    await t.tap(find.bySemanticsLabel('On Taskiem for Dangote Depot'));
    await t.pumpAndSettle();
    await t.tap(find.text('Dangote Depot Ltd'));
    await settle(t);
    expect(sent.last, 'PUT /v1/stock/suppliers/S2/link {"link_id":"L9"}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Retire Dangote Depot'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Retire').last);
    await settle(t);
    expect(sent.last, 'POST /v1/stock/suppliers/S2/retire {}');
    await t.pump(const Duration(seconds: 5));
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^A second person')), matching: find.byType(EditableText)), '');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save the limit'));
    await settle(t);
    expect(sent.last, 'PUT /v1/stock/settings {"purchase_limit_kobo":null}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a supplier added with what they supply', (t) async {
    final sent = await open(t, (c) => SuppliersPage(auth: c.auth));
    await t.tap(find.bySemanticsLabel('Add a supplier'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Name')), matching: find.byType(EditableText)), 'Ada Oils');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Mobile number')), matching: find.byType(EditableText)), '0803 123 4567');
    await t.tap(find.text('Beans'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Add the supplier'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/stock/suppliers {"name":"Ada Oils","phone":"0803 123 4567","items":["MY2"]}');
    expect(find.text('Ada Oils added; the link is on its way by SMS'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('M8 fixes 5, 21: no stock kept on Taskiem: no items offered, and Stock leads there', (t) async {
    await open(
      t,
      (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false),
      purchase: _purchase(status: 'drafted', seller: 'proposed'),
      keeps: false,
    );
    expect(find.text('Keep your stock on Taskiem to confirm it, so what comes is received into your items.'), findsOneWidget);
    expect(find.bySemanticsLabel('Confirm the order'), findsNothing);
    expect(find.text('Stock'), findsOneWidget);
  });

  testWidgets('M8 fixes 3: the links failing to read is said, not "not linked"', (t) async {
    await open(t, (c) => SuppliersPage(auth: c.auth), linksFail: true);
    await t.tap(find.bySemanticsLabel('On Taskiem for Dangote Depot'));
    await settle(t);
    expect(find.text('Something broke.'), findsOneWidget);
    expect(find.textContaining('You are not linked'), findsNothing);
  });

  testWidgets('M8 fixes 2: the Stock card read again on return from an order', (t) async {
    final f = <String>[];
    final sent = await open(
      t,
      (c) => TkPageScreen(
        title: 'Stock',
        children: [PurchasesCard(auth: c.auth, items: const [], onChanged: () => f.add('changed'))],
      ),
      purchase: _purchase(seller: 'accepted'),
    );
    await t.tap(find.bySemanticsLabel('Open PO-0012'));
    await t.pumpAndSettle();
    await settle(t);
    await t.tap(find.bySemanticsLabel('Back').first);
    await t.pumpAndSettle();
    await settle(t);
    expect(f, contains('changed'), reason: 'the card and the screen read again');
    expect(sent, isNotNull);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async => switch (req.url.path) {
          '/v1/stock/purchases' => ok({
            'purchases': [_purchase(link: '', owed: 100)],
          }),
          '/v1/stock/purchases/P1' => ok({'purchase': _purchase(seller: 'delivered')}),
          '/v1/stock/suppliers' => ok({
            'suppliers': [
              {'id': 'S1', 'name': 'Bayo Rice Mills', 'phone': '0803', 'reached': 'sms', 'open': 1, 'owed_kobo': 100},
            ],
            'settings': {'purchase_limit_kobo': null},
          }),
          '/v1/bills' => ok({'bills': []}),
          '/v1/stock/deliver-to' => ok({'address': '', 'phone': ''}),
          _ => null,
        },
      ),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: PurchasePage(auth: chats.auth, id: 'P1', follow: false),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: TkPageScreen(
          title: 'Stock',
          children: [PurchasesCard(auth: chats.auth, items: const [], onChanged: () {})],
        ),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: SuppliersPage(auth: chats.auth),
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
