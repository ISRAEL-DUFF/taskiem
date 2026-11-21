// Purchases and suppliers (M8 part P; web TradeOrders.tsx PurchasePage,
// StockPurchases.tsx, StorefrontSuppliers.tsx; lib/purchases.ts,
// lib/tradeorders.ts): what each step sends, and what each screen says.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/purchases_page.dart';
import 'package:taskiem/kit/page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'saved_stub.dart';
import 'support.dart';

Map<String, dynamic> _saved(String id, String owner, String kind, String name, String title, String hint, Map<String, dynamic> details) => {
  'id': id,
  'owner': owner,
  'name': name,
  'title': title,
  'kind': kind,
  'hint': hint,
  'details': details,
  'check': {'state': 'none', 'account_name': null, 'via': null, 'at': null, 'problem': null},
  'to_change': true,
  'created_at': '2026-11-20T10:00:00Z',
  'updated_at': '2026-11-20T10:00:00Z',
};

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
    Map<String, List<Map<String, dynamic>>> saved = const {},
    bool savedFails = false,
    SavedStub? stub,
    bool deliverToFails = false,
    List<String>? perms,
    // 201, 409 (the name taken) or 'name_needed'.
    List<Object> placeAnswers = const [201],
  }) async {
    var placeAsked = 0;
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
          if (req.method == 'POST' && p == '/v1/saved') {
            sent.add('${req.method} $p ${req.body}');
            final status = placeAnswers[placeAsked < placeAnswers.length ? placeAsked : placeAnswers.length - 1];
            placeAsked++;
            if (status == 'name_needed') {
              return http.Response(jsonEncode({'error': 'Give it a name to call it up by.', 'code': 'name_needed', 'name_needed': true}), 409, headers: {'content-type': 'application/json'});
            }
            if (status == 409) return http.Response(jsonEncode({'error': 'That name is taken: choose another.', 'name_taken': true}), 409, headers: {'content-type': 'application/json'});
            final b = jsonDecode(req.body) as Map<String, dynamic>;
            return http.Response(
              jsonEncode({'saved': savedThing('B9', 'business', 'place', b['name'] as String, b['title'] as String, '', (b['details'] as Map).cast<String, dynamic>())}),
              201,
              headers: {'content-type': 'application/json'},
            );
          }
          if (stub?.answer(req) case final r?) return r;
          if (req.method == 'GET') {
            return switch (p) {
              _ when p.endsWith('/deliver-to') && p != '/v1/stock/deliver-to' => ok({'address': '', 'phone': '+2348031110000', 'own': false}),
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
              '/v1/me/saved' when savedFails => http.Response(jsonEncode({'error': 'Saved things cannot be read now.'}), 500, headers: {'content-type': 'application/json'}),
              _ when saved.containsKey(p) => ok({'saved': saved[p], 'limit': 200, 'can_check': false}),
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
          if (req.method == 'PUT' && p.startsWith('/v1/stock/purchases/') && p.endsWith('/deliver-to')) {
            if (deliverToFails) {
              return http.Response(jsonEncode({'error': 'PO-0012 has reached the supplier already: tell them in your conversation with them'}), 409, headers: {'content-type': 'application/json'});
            }
            return ok({...(jsonDecode(req.body) as Map<String, dynamic>), 'own': true});
          }
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
    if (perms != null) chats.auth.me = Me.fromJson({...f.json('/v1/me'), 'permissions': perms});
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

  testWidgets('where linked suppliers deliver: a saved place fills it', (t) async {
    final sent = await open(
      t,
      (c) => SuppliersPage(auth: c.auth),
      saved: {
        '/v1/me/saved': [_saved('M1', 'mine', 'place', 'home', 'Home', 'Yaba', {'line': '3 Herbert Macaulay Way', 'area': 'Yaba'})],
        '/v1/saved': [
          _saved('B1', 'business', 'place', 'warehouse', 'Warehouse', 'Lekki', {'line': '12 Admiralty Way', 'area': 'Lekki', 'landmark': 'by the filling station', 'phone': '+2348031120099'}),
          _saved('B2', 'business', 'person', 'tolu', 'Tolu', '••0099', {'phone': '+2348031120099'}),
        ],
      },
    );
    await t.tap(find.bySemanticsLabel('Pick from your saved places'));
    await t.pumpAndSettle();
    expect(find.text('Saved places'), findsOneWidget);
    expect(find.text('Tolu'), findsNothing, reason: 'a person is not a place');
    await t.tap(find.text('Warehouse'));
    await t.pumpAndSettle();
    expect(find.text('12 Admiralty Way, Lekki (by the filling station)'), findsOneWidget);
    expect(find.text('+2348031120099'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Pick from your saved places'));
    await t.pumpAndSettle();
    await t.tap(find.text('Home'));
    await t.pumpAndSettle();
    expect(find.text('3 Herbert Macaulay Way, Yaba'), findsOneWidget);
    expect(find.text('+2348031120099'), findsOneWidget, reason: 'no phone saved: the one there stays');
    await t.tap(find.bySemanticsLabel('Save where they deliver'));
    await settle(t);
    expect(sent.last, 'PUT /v1/stock/deliver-to {"address":"3 Herbert Macaulay Way, Yaba","phone":"+2348031120099"}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('where linked suppliers deliver: a list that cannot be read is said', (t) async {
    await open(t, (c) => SuppliersPage(auth: c.auth), savedFails: true);
    await t.tap(find.bySemanticsLabel('Pick from your saved places'));
    await t.pump();
    expect(find.text('Saved places'), findsNothing);
    expect(find.textContaining('Saved things cannot be read now.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('where linked suppliers deliver: a long saved place fits the 300 characters, and says what was left out', (t) async {
    final long = 'Plot ${'1' * 4} ${'Admiralty Way ' * 13}'.trim();
    final stub = SavedStub(
      mine: [
        savedThing('M1', 'mine', 'place', 'depot', 'Depot', 'Lekki', {'line': long, 'area': 'Lekki Phase 1', 'landmark': 'opposite the big blue gate by the filling station' * 2}),
        savedThing('M2', 'mine', 'place', 'gate', 'Gate', '', {'landmark': 'The blue gate'}),
      ],
    );
    await open(t, (c) => SuppliersPage(auth: c.auth), stub: stub);
    await t.tap(find.bySemanticsLabel('Pick from your saved places'));
    await t.pumpAndSettle();
    await t.tap(find.text('Depot'));
    await t.pump();
    expect(find.text('$long, Lekki Phase 1'), findsOneWidget);
    expect(find.text('Left out the landmark: an address here is up to 300 characters.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Pick from your saved places'));
    await t.pumpAndSettle();
    await t.tap(find.text('Gate'));
    await t.pumpAndSettle();
    expect(find.text('The blue gate'), findsOneWidget, reason: 'no street: the landmark alone, not “ (The blue gate)”');
  });

  testWidgets('a supplier’s number from a saved person', (t) async {
    final stub = SavedStub(mine: [savedThing('M1', 'mine', 'person', 'dayo', 'Dayo', '••0099', {'phone': '+2348031120099'})]);
    await open(t, (c) => SuppliersPage(auth: c.auth), stub: stub);
    await t.tap(find.bySemanticsLabel('Add a supplier'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Pick a saved phone number'));
    await t.pumpAndSettle();
    expect(find.text('Saved phone numbers'), findsOneWidget);
    await t.tap(find.text('Dayo'));
    await t.pumpAndSettle();
    expect(find.text('+2348031120099'), findsOneWidget);
  });

  testWidgets('an order not yet with its linked supplier: delivered to a saved place', (t) async {
    final stub = SavedStub(
      business: [savedThing('B1', 'business', 'place', 'warehouse', 'Warehouse', 'Lekki', {'line': '12 Admiralty Way', 'area': 'Lekki'})],
    );
    final sent = await open(t, (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false), stub: stub);
    expect(find.text('Where it is delivered'), findsOneWidget);
    expect(find.text('You pick it up'), findsOneWidget);
    await t.tap(find.text('Deliver to a saved place'));
    await t.pumpAndSettle();
    await t.tap(find.text('Warehouse'));
    await settle(t);
    expect(sent.last, 'PUT /v1/stock/purchases/P1/deliver-to {"address":"12 Admiralty Way, Lekki","phone":"+2348031110000"}', reason: 'no phone saved: the number there now');
    expect(find.text('12 Admiralty Way, Lekki'), findsOneWidget);
    expect(find.textContaining('This order’s own place'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('an order the supplier has already: its refusal is said; one they hold offers no place', (t) async {
    final stub = SavedStub(mine: [savedThing('M1', 'mine', 'place', 'home', 'Home', 'Yaba', {'line': '3 Herbert Macaulay Way', 'phone': '+2348031120099'})]);
    await open(t, (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false), stub: stub, deliverToFails: true);
    await t.tap(find.text('Deliver to a saved place'));
    await t.pumpAndSettle();
    await t.tap(find.text('Home'));
    await settle(t);
    expect(find.text('PO-0012 has reached the supplier already: tell them in your conversation with them'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await open(t, (c) => PurchasePage(auth: c.auth, id: 'P1', follow: false), purchase: _purchase(seller: 'accepted'));
    expect(find.text('Deliver to a saved place'), findsNothing);
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

  // After a delivery to a new address (saved objects S3): the place typed
  // is offered to save, once; a saved place picked is not.
  const savedPerms = ['order.handle', 'storefront.manage', 'saved.manage'];
  Finder field(String label) => find.descendant(of: find.bySemanticsLabel(RegExp('^$label')), matching: find.byType(EditableText));

  testWidgets('where linked suppliers deliver: a typed address saved, then offered as a place, and saved', (t) async {
    final sent = await open(t, (c) => SuppliersPage(auth: c.auth), perms: savedPerms);
    await t.enterText(field('Address'), '12 Admiralty Way, Lekki');
    await t.enterText(field('Number the rider calls'), '+2348031120099');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save where they deliver'));
    await settle(t);
    expect(sent.last, 'PUT /v1/stock/deliver-to {"address":"12 Admiralty Way, Lekki","phone":"+2348031120099"}');
    expect(find.text('Save this place for next time?'), findsOneWidget);
    expect(find.text('As @12-admiralty-way, among your saved places'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Save this place as @12-admiralty-way'));
    await settle(t);
    expect(
      sent.last,
      'POST /v1/saved {"details":{"line":"12 Admiralty Way, Lekki","phone":"+2348031120099"},"kind":"place","name":"12-admiralty-way","title":"12 Admiralty Way"}',
    );
    expect(find.text('Saved as @12-admiralty-way'), findsOneWidget);
    expect(find.text('Save this place for next time?'), findsNothing, reason: 'offered once');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('where linked suppliers deliver: the place’s name taken, another asked for', (t) async {
    final sent = await open(t, (c) => SuppliersPage(auth: c.auth), perms: savedPerms, placeAnswers: [409, 201]);
    await t.enterText(field('Address'), 'Shop 4, Ikota Market');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save where they deliver'));
    await settle(t);
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Save this place as @shop-4'));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('@shop-4 is taken'), findsOneWidget);
    await t.enterText(field('Name to call it up by'), '@ikota-shop');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save').last);
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/saved {"details":{"line":"Shop 4, Ikota Market"},"kind":"place","name":"ikota-shop","title":"Shop 4"}');
    expect(find.text('Saved as @ikota-shop'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('where linked suppliers deliver: a saved place picked is not offered; nor to one who may not manage saved things', (t) async {
    final stub = SavedStub(business: [savedThing('B1', 'business', 'place', 'warehouse', 'Warehouse', 'Lekki', {'line': '12 Admiralty Way', 'area': 'Lekki'})]);
    await open(t, (c) => SuppliersPage(auth: c.auth), perms: savedPerms, stub: stub);
    await t.tap(find.bySemanticsLabel('Pick from your saved places'));
    await t.pumpAndSettle();
    await t.tap(find.text('Warehouse'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Save where they deliver'));
    await settle(t);
    expect(find.textContaining('Save this place'), findsNothing);
    await t.pump(const Duration(seconds: 5));
    await open(t, (c) => SuppliersPage(auth: c.auth));
    await t.enterText(field('Address'), '12 Admiralty Way, Lekki');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save where they deliver'));
    await settle(t);
    expect(find.textContaining('Save this place'), findsNothing);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('where linked suppliers deliver: a long address saved as a place cut to its 200, at a comma', (t) async {
    final sent = await open(t, (c) => SuppliersPage(auth: c.auth), perms: savedPerms);
    final long = 'Plot 12 Admiralty Way, ${'Behind the big blue gate ' * 6}Lekki Phase 1, Opposite the filling station, Lagos';
    expect(long.length, inInclusiveRange(201, 300));
    await t.enterText(field('Address'), long);
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save where they deliver'));
    await settle(t);
    expect(sent.last, contains(long), reason: 'the address itself kept whole');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Save this place as @plot-12-admiralty-way'));
    await settle(t);
    final line = ((jsonDecode(sent.last.substring('POST /v1/saved '.length)) as Map)['details'] as Map)['line'] as String;
    expect(line.length, lessThanOrEqualTo(200));
    expect(line, 'Plot 12 Admiralty Way, ${'Behind the big blue gate ' * 6}Lekki Phase 1');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('where linked suppliers deliver: only the number changed after: the place still offered, with the new number', (t) async {
    final sent = await open(t, (c) => SuppliersPage(auth: c.auth), perms: savedPerms);
    await t.enterText(field('Address'), '12 Admiralty Way, Lekki');
    await t.enterText(field('Number the rider calls'), '+2348031120099');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save where they deliver'));
    await settle(t);
    await t.pump(const Duration(seconds: 5));
    expect(find.text('Save this place for next time?'), findsOneWidget);
    await t.enterText(field('Number the rider calls'), '+2348039990000');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save where they deliver'));
    await settle(t);
    await t.pump(const Duration(seconds: 5));
    expect(sent.last, 'PUT /v1/stock/deliver-to {"address":"12 Admiralty Way, Lekki","phone":"+2348039990000"}');
    expect(find.text('Save this place for next time?'), findsOneWidget, reason: 'the place offered stays');
    await t.tap(find.bySemanticsLabel('Save this place as @12-admiralty-way'));
    await settle(t);
    expect(
      sent.last,
      'POST /v1/saved {"details":{"line":"12 Admiralty Way, Lekki","phone":"+2348039990000"},"kind":"place","name":"12-admiralty-way","title":"12 Admiralty Way"}',
    );
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('where linked suppliers deliver: no name could be made: one asked for, the sheet empty', (t) async {
    final sent = await open(t, (c) => SuppliersPage(auth: c.auth), perms: savedPerms, placeAnswers: ['name_needed', 201]);
    await t.enterText(field('Address'), 'Shop 4, Ikota Market');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save where they deliver'));
    await settle(t);
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Save this place as @shop-4'));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('Give it a name to call\u00A0it\u00A0up\u00A0by'), findsOneWidget);
    expect((t.widget<EditableText>(field('Name to call it up by'))).controller.text, '');
    await t.enterText(field('Name to call it up by'), '@ikota-shop');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save').last);
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/saved {"details":{"line":"Shop 4, Ikota Market"},"kind":"place","name":"ikota-shop","title":"Shop 4"}');
    expect(find.text('Saved as @ikota-shop'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
