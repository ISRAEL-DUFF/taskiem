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
import 'looks.dart';
import 'support.dart';
import 'treat_fixture.dart';

Map<String, dynamic> _purchase({
  String id = 'P1',
  String status = 'sent',
  String? seller,
  String link = 'L1',
  List<Map<String, dynamic>>? change,
  int owed = 0,
  List<Map<String, dynamic>>? lines,
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
  'lines':
      lines ??
      [
        {'item_id': 'IT1', 'item': 'Rice 50kg', 'quantity': 2, 'unit': 'bags', 'cost_kobo': 7800000, 'received': 0, 'delivered': 2},
      ],
};

/// A line as U1 has it: its place, treatment, stock item and label.
Map<String, dynamic> _line(int at, String item, {int quantity = 2, int received = 0, String treatment = 'record', String? stock, String? label, bool unconfirmed = false}) => {
  'id': 'PL$at',
  'position': at,
  'item_id': 'IT$at',
  'item': item,
  'quantity': quantity,
  'unit': 'bags',
  'cost_kobo': 7800000,
  'received': received,
  'delivered': quantity,
  'treatment': treatment,
  'stock_item_id': ?stock,
  'uses': 1,
  'label_id': ?label,
  'unconfirmed': unconfirmed,
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
    http.Response? Function(http.Request req)? answer,
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
          final treat = treatAnswers(req);
          if (treat != null) return treat;
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
              '/v1/stock/suppliers/S2/items' => ok({
                'items': [memory('Diesel', 'record', label: 'Staff welfare', labelId: 'lb-staff')],
              }),
              '/v1/item-links/L1' => ok({
                'item_links': [memory('Rice 50kg', 'stock', sellerItem: 'R50', stockItemId: 'si-rice', stockItem: 'Rice', labelId: 'lb-pack', label: 'Packaging')],
              }),
              _ when p.startsWith('/v1/stock/purchases/') => ok({'purchase': purchase ?? _purchase()}),
              _ => null,
            };
          }
          sent.add('${req.method} $p ${req.body}');
          final said = answer?.call(req);
          if (said != null) return said;
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

  testWidgets('their draft confirmed: the question on each line, a remembered one as it is', (t) async {
    final sent = await open(
      t,
      (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false),
      purchase: _purchase(
        status: 'drafted',
        seller: 'proposed',
        lines: [_line(0, 'Sewing machine', quantity: 1, unconfirmed: true), _line(1, 'Rice 50kg', treatment: 'stock', stock: 'si-rice', label: 'lb-pack')],
      ),
    );
    expect(find.textContaining('drafted this order for you'), findsOneWidget);
    // Asked: the line never ordered before; the other in a few words.
    expect(find.text('How should we treat it?'), findsOneWidget);
    expect(find.text('Add to stock: Rice · Packaging'), findsOneWidget);
    await looks(t, 'purchase-confirm');
    await t.tap(find.bySemanticsLabel(RegExp('^Keep track of it: Sewing machine')));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Label for Sewing machine'));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('Your labels'), findsOneWidget);
    expect(find.text('Suggested'), findsOneWidget, reason: 'a new name suggested after the labels by use');
    await t.tap(find.text('Machines'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Confirm the order'));
    await settle(t);
    expect(sent.single, 'POST /v1/stock/purchases/P1/confirm-proposed {"lines":[{"treatment":"track","label_id":"lb-mach"},null]}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a label typed is made; a new stock item asks Do you sell it?', (t) async {
    final sent = await open(
      t,
      (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false),
      purchase: _purchase(status: 'drafted', seller: 'proposed', lines: [_line(0, 'Ankara fabric', quantity: 20, unconfirmed: true)]),
      answer: (req) => switch (req.url.path) {
        '/v1/money/labels' => http.Response(jsonEncode(label('lb-fab', 'Fabric', 0)), 201, headers: {'content-type': 'application/json'}),
        '/v1/stock/items' => http.Response(jsonEncode({'item': {'id': 'si-ankara', 'name': 'Ankara fabric', 'material': true, 'available': true, 'stock': 0}}), 201, headers: {'content-type': 'application/json'}),
        _ => null,
      },
    );
    await t.tap(find.bySemanticsLabel(RegExp('^Add to stock: Ankara fabric')));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Stock item for Ankara fabric'));
    await t.pumpAndSettle();
    await t.tap(find.text('Make a new one'));
    await t.pumpAndSettle();
    expect(find.text('Do you sell it?'), findsOneWidget);
    await t.tap(find.text('No, I don’t'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Make it'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, startsWith('POST /v1/stock/items {"name":"Ankara fabric","quantity":0,"unit":"","material":true}'));
    await t.tap(find.bySemanticsLabel('Label for Ankara fabric'));
    await settle(t);
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('Type or pick a label')), matching: find.byType(EditableText)), 'Fabric');
    await t.pump();
    await t.tap(find.text('Make this label'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/money/labels {"name":"Fabric"}');
    await t.tap(find.bySemanticsLabel('Confirm the order'));
    await settle(t);
    expect(sent.last, 'POST /v1/stock/purchases/P1/confirm-proposed {"lines":[{"treatment":"stock","stock_item_id":"si-ankara","label_id":"lb-fab"}]}');
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
    expect(sent.last, 'POST /v1/stock/purchases/P1/accept-change {}');
    await t.pump(const Duration(seconds: 5));
    await t.ensureVisible(find.bySemanticsLabel(RegExp('^Cancel the order')).first);
    await t.tap(find.bySemanticsLabel(RegExp('^Cancel the order')).first);
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
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/stock/purchases/P1/not-all-came {"counts":[1]}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Supplies: what is low ordered; goods in; a payment', (t) async {
    final sent = await open(
      t,
      (c) => SuppliesPage(auth: c.auth, items: const []),
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
    expect(find.bySemanticsLabel(RegExp(r'^Bayo Rice Mills, you owe ₦20,000')), findsOneWidget, reason: 'a linked supplier is owed its open bills');
    expect(find.bySemanticsLabel(RegExp(r'^Dangote Depot, you owe ₦3,000')), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp(r'^You owe your suppliers ₦23,000')), findsOneWidget);
    // A supplier's row opens what can be done to them.
    await t.tap(find.text('Bayo Rice Mills').first);
    await t.pumpAndSettle();
    expect(find.bySemanticsLabel('Statement'), findsOneWidget);
    await t.tapAt(const Offset(20, 20));
    await t.pumpAndSettle();
    await t.tap(find.text('Dangote Depot'));
    await t.pumpAndSettle();
    expect(find.bySemanticsLabel('Statement'), findsNothing, reason: 'not linked');
    await t.tap(find.bySemanticsLabel('On Taskiem'));
    await t.pumpAndSettle();
    await t.tap(find.text('Dangote Depot Ltd'));
    await settle(t);
    expect(sent.last, 'PUT /v1/stock/suppliers/S2/link {"link_id":"L9"}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.text('Dangote Depot'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Retire'));
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

  testWidgets('an order by hand: its lines marked; one changed for this order; tracking many asks first; one that came stays', (t) async {
    var asked = false;
    final sent = await open(
      t,
      (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false),
      purchase: _purchase(
        link: '',
        lines: [_line(0, 'Rice 50kg', quantity: 60, treatment: 'stock', stock: 'si-rice', label: 'lb-pack'), _line(1, 'Nylon bags', received: 1, treatment: 'track', label: 'lb-mach')],
      ),
      answer: (req) {
        if (req.method != 'PATCH' || asked) return null;
        asked = true;
        return http.Response(
          jsonEncode({'error': 'put 60 Rice 50kg on Things we have, one row each?', 'code': 'track_ask', 'line': 0, 'item': 'Rice 50kg', 'quantity': 60}),
          409,
          headers: {'content-type': 'application/json'},
        );
      },
    );
    // The order's card: each line's mark and label.
    expect(find.text('To stock'), findsOneWidget);
    expect(find.text('Things we have'), findsOneWidget);
    expect(find.text('Packaging'), findsOneWidget);
    expect(find.textContaining('it came, so it stays'), findsOneWidget);
    expect(find.bySemanticsLabel('Change how Nylon bags is treated'), findsNothing);
    await looks(t, 'purchase-lines');
    await t.tap(find.bySemanticsLabel('Change how Rice 50kg is treated'));
    await t.pump();
    expect(find.text('Remember for next time'), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp('^Keep track of it: Rice 50kg')));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save how Rice 50kg is treated'));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('Put 60 Rice 50kg on Things we have, one row each?'), findsOneWidget);
    expect(find.bySemanticsLabel('Just record them'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Put them on'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent, [
      'PATCH /v1/stock/purchases/P1/lines/0 {"treatment":"track","label_id":"lb-pack"}',
      'PATCH /v1/stock/purchases/P1/lines/0 {"treatment":"track","label_id":"lb-pack","track_many":true}',
    ]);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a line confirmed: the question shuts to a few words', (t) async {
    final p = _purchase(link: '', lines: [_line(0, 'Diesel', unconfirmed: true)]);
    final sent = await open(
      t,
      (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false),
      purchase: p,
      answer: (req) {
        // Confirmed: read again, the line is no longer asked.
        if (req.method == 'PATCH') (p['lines'] as List).first['unconfirmed'] = false;
        return null;
      },
    );
    expect(find.text('How should we treat it?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Confirm how Diesel is treated'));
    await settle(t);
    expect(sent.where((s) => s.startsWith('PATCH /v1/stock/purchases/P1/lines/0')), hasLength(1));
    expect(find.text('How should we treat it?'), findsNothing, reason: 'closed once confirmed');
    expect(find.bySemanticsLabel('Confirm how Diesel is treated'), findsNothing);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a line that came meanwhile: locked, said, read again', (t) async {
    await open(
      t,
      (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false),
      purchase: _purchase(link: '', lines: [_line(0, 'Diesel', unconfirmed: true)]),
      answer: (req) => http.Response(jsonEncode({'error': 'locked', 'code': 'line_locked'}), 409, headers: {'content-type': 'application/json'}),
    );
    await t.tap(find.bySemanticsLabel('Confirm how Diesel is treated'));
    await settle(t);
    expect(find.text('Something on this line came already, so how it is treated stays.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Suppliers: what you buy from them, changed and forgotten', (t) async {
    final sent = await open(t, (c) => SuppliersPage(auth: c.auth));
    await t.tap(find.text('Dangote Depot'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('What you buy from them'));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('What you buy from Dangote Depot'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Diesel: Just record it · Staff welfare')), findsOneWidget);
    await t.tap(find.text('Diesel'));
    await settle(t);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel(RegExp('^Add to stock: Diesel')));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Stock item for Diesel'));
    await t.pumpAndSettle();
    await t.tap(find.text('Nylon bags'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'PUT /v1/stock/suppliers/S2/items {"name":"Diesel","treatment":"stock","stock_item_id":"si-nylon","label_id":"lb-staff"}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.text('Diesel'));
    await settle(t);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Forget'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Forget it'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'DELETE /v1/stock/suppliers/S2/items ');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('M8 fixes 3: the links failing to read is said, not "not linked"', (t) async {
    await open(t, (c) => SuppliersPage(auth: c.auth), linksFail: true);
    await t.tap(find.text('Dangote Depot'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('On Taskiem'));
    await settle(t);
    expect(find.text('Something broke.'), findsOneWidget);
    expect(find.textContaining('You are not linked'), findsNothing);
  });

  testWidgets('M8 fixes 2: the Stock card read again on return from an order', (t) async {
    final f = <String>[];
    final sent = await open(
      t,
      (c) => TkPageScreen(
        title: 'Supplies',
        children: [PurchasesCard(auth: c.auth, items: const [], onChanged: () => f.add('changed'), full: true)],
      ),
      purchase: _purchase(seller: 'accepted'),
    );
    await t.tap(find.bySemanticsLabel('Open PO-0012'));
    await t.pumpAndSettle();
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp('^Back')).first);
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
        home: SuppliesPage(auth: chats.auth, items: const []),
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
