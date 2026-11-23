// The Stock screen (M6 part T; web StorefrontStock.tsx, lib/stock.ts):
// today's sales, what is low, each item, every change; a sale recorded,
// stock recorded with a reason, an item added and changed.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/api/api.g.dart' show ListStockItemsResponse, Me;
import 'package:taskiem/chat/stock_items.dart';
import 'package:taskiem/chat/stock_page.dart';
import 'package:taskiem/kit/modal.dart' show TkModal;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'stock_items_fixture.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  test('the stock at cost, short; the sales by the hour', () {
    expect(stockValue([
      {'stock': 4, 'cost_kobo': 850000},
      {'stock': 20},
      {'stock': -1, 'cost_kobo': 100},
    ]), 3400000);
    expect(compactNaira(124000000), '₦1.24M');
    expect(compactNaira(200000000), '₦2M');
    expect(compactNaira(86500000), '₦865k');
    expect(compactNaira(8650000), '₦86,500', reason: 'under ₦100,000, as it is');
    expect(compactNaira(950000), '₦9,500');
    final h = salesByHour([
      {'sold_at': '2026-11-12T09:00:00Z', 'amount_kobo': 1900000},
      {'sold_at': '2026-11-12T09:40:00Z', 'amount_kobo': 100000},
    ]);
    expect(h[10], 2000000, reason: '9:00 UTC is 10 am in Lagos');
    expect(h.where((x) => x > 0).length, 1);
  });

  test('the words for an item and a change', () {
    expect(plural('box'), 'boxes');
    expect(plural('battery'), 'batteries');
    expect(stockQuantity(1, 'carton'), '1 carton');
    expect(stockLine({'stock': null}), 'No count');
    expect(movementTitle({'item': 'Indomie', 'reason': 'received', 'change': 50}), 'Indomie: received +50');
    expect(movementTitle({'item': 'Rice', 'reason': 'count', 'set_to': 12}), 'Rice: counted 12');
  });

  test('what a stock item is sold as, in words; a product’s sizes under it', () {
    final rows = ListStockItemsResponse.fromJson(stockItems).items;
    String sub(String id) => stockItemSub(rows.firstWhere((r) => r.id == id));
    expect(sub('si-indomie'), 'Sold as Indomie · cost ₦8,500 · low at 10 · Dangote · order 46 cartons');
    expect(sub('si-malt'), 'Sold as Malt, Malt, pack of 6 (6 a sale)', reason: 'uses said when more than one');
    expect(sub('si-nylon'), 'Not sold · low at 50');
    expect(soldAsLine(rows.firstWhere((r) => r.id == 'si-shirt-l')), 'Sold as Ankara shirt, L', reason: 'a size with its product');
    final grouped = stockRows(rows);
    expect([for (final x in grouped) x.item?.name ?? '${x.group!.name}: ${x.of.map((r) => r.name).join(', ')}'], [
      'Ankara shirt: Ankara shirt (L), Ankara shirt (M)',
      'Indomie',
      'Malt',
      'Nylon bags',
      'Rice',
    ]);
    expect([for (final r in rows) if (shows(StockShow.notSold, r)) r.name], ['Nylon bags']);
    expect(rows.where((r) => shows(StockShow.sold, r)).length, 5);
  });

  Future<List<String>> open(WidgetTester t, {Future<http.Response?> Function(http.Request req)? also, List<String>? perms}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 4000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        final own = await also?.call(req);
        if (own != null) {
          if (req.method != 'GET') sent.add('${req.method} $p ${req.body}');
          return own;
        }
        if (req.method == 'GET') {
          if (p == '/v1/stock') {
            return ok({
              'keeps_stock': true,
              'catalogue': 'Products',
              'items': [
                {'id': 'indomie', 'name': 'Indomie', 'available': true, 'stock': 4, 'unit': 'carton', 'reorder_level': 10, 'low': true, 'price_kobo': 950000},
                {'id': 'rice', 'name': 'Rice', 'available': true, 'stock': 20, 'unit': 'bag'},
              ],
            });
          }
          if (p == '/v1/stock/stock-items') {
            sent.add('GET $p ${req.url.query}');
            return ok(stockItems);
          }
          if (p == '/v1/stock/sales') {
            return ok({
              'day': '2026-11-12',
              'total_kobo': 1900000,
              'by_how': {'cash': 1900000},
              'count': 1,
              'waiting': 0,
              'top': <Object>[],
              'unsold': [
                {'entry_id': 'E1', 'amount_kobo': 950000, 'channel': 'pos', 'at': '2026-11-12T10:00:00Z'},
              ],
              'sales': [
                {
                  'id': 'S1',
                  'reference': 'SL-1',
                  'lines': [
                    {'item': 'Indomie', 'quantity': 2, 'unit': 'carton'},
                  ],
                  'amount_kobo': 1900000,
                  'paid_how': 'cash',
                  'status': 'recorded',
                  'sold_at': '2026-11-12T09:00:00Z',
                },
              ],
            });
          }
          if (p == '/v1/stock/shrinkage') {
            return ok({
              'from': '2026-11-01',
              'to': '2026-11-12',
              'received': 40,
              'sold': 30,
              'lost': 3,
              'lost_kobo': 2550000,
              'items': [
                {'item_id': 'indomie', 'item': 'Indomie', 'received': 20, 'sold': 15, 'lost': 3, 'lost_kobo': 2550000},
              ],
            });
          }
          if (p == '/v1/stock/counts') {
            return ok({
              'counts': [
                {'id': 'K1', 'reference': 'CT-1', 'lines': [{}, {}], 'full': true, 'reason': 'rats', 'lost_kobo': 2550000, 'by': 'Ada', 'counted_at': '2026-11-10T09:00:00Z', 'changed': 1},
              ],
            });
          }
          if (p == '/v1/stock/settings') return ok({'settings': {'purchase_limit_kobo': null, 'count_limit_kobo': null}});
          if (p == '/v1/stock/movements') {
            sent.add('GET $p ${req.url.query}');
            return ok({
              'movements': [
                {'id': 'M1', 'item_id': 'rice', 'item': 'Rice', 'reason': 'received', 'status': 'done', 'change': 20, 'before': 0, 'after': 20, 'created_at': '2026-11-12T08:00:00Z'},
              ],
            });
          }
          return null;
        }
        sent.add('${req.method} $p ${req.body}');
        return ok({});
      }),
      now: () => f.at,
    );
    if (perms != null) chats.auth.me = Me.fromJson({...f.json('/v1/me'), 'permissions': perms});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: StockPage(auth: chats.auth)));
    await settle(t);
    return sent;
  }

  testWidgets('what the page shows', (t) async {
    await open(t);
    await looks(t, 'stock');
    expect(find.text('₦19,000'), findsNWidgets(2), reason: 'today’s sales, and the sale');
    expect(find.text('1 sale'), findsOneWidget);
    expect(find.text('Cash ₦19,000'), findsOneWidget);
    expect(find.text('Terminal sales with no items'), findsOneWidget);
    expect(find.text('2 cartons Indomie'), findsOneWidget);
    expect(find.text('Running low'), findsOneWidget);
    expect(find.text('Order 2'), findsOneWidget, reason: 'the Low tile: Indomie and a shirt’s L');
    expect(find.text('4 cartons'), findsNWidgets(2), reason: 'low, and among the items');
    expect(find.text('Rice: received +20'), findsOneWidget);
  });

  testWidgets('Add something to count only for someone who may change tables', (t) async {
    await open(t);
    expect(find.bySemanticsLabel('Add something to count'), findsOneWidget);
    await open(t, perms: const ['table.read', 'invoice.read']);
    expect(find.text('Running low'), findsOneWidget, reason: 'the page is still shown');
    expect(find.bySemanticsLabel('Add something to count'), findsNothing);
  });

  testWidgets('the stock items failing to load says so, not that nothing is low', (t) async {
    await open(t, also: (req) async => req.url.path == '/v1/stock/stock-items' ? http.Response('{"error":"Stock items could not be read"}', 500, headers: {'content-type': 'application/json'}) : null);
    expect(find.text('What you count did not load'), findsOneWidget);
    expect(find.text('None'), findsNothing, reason: 'no Low tile saying none');
    expect(find.text('nothing at its low level'), findsNothing);
    expect(find.text('Running low'), findsNothing);
    expect(find.text('Give what you count a low level, and it shows here when it runs low.'), findsNothing);
  });

  testWidgets('stock recorded: some spoilt, taken off, with why', (t) async {
    final sent = await open(t);
    sent.clear();
    // An item's row opens what can be done to it.
    await t.tap(find.text('Indomie').first);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Record stock'));
    await t.pumpAndSettle();
    await t.tap(find.text('Some were spoilt, lost or found'));
    await t.pump();
    final inModal = find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText));
    await t.enterText(inModal.at(0), '2');
    await t.enterText(inModal.at(1), 'spoilt');
    await t.pump();
    expect(find.text('The count goes from 4 → 2 cartons.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Record'));
    await settle(t);
    expect(sent.first, 'POST /v1/stock/movements {"item_id":"indomie","reason":"adjustment","note":"spoilt","change":-2}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a terminal sale said: its item, its amount and entry', (t) async {
    final sent = await open(t);
    sent.clear();
    await t.tap(find.bySemanticsLabel('Say what was sold for ₦9,500'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel(RegExp('^What was sold: ')));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Indomie (4 cartons)'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Record the sale'));
    await settle(t);
    expect(
      sent.first,
      'POST /v1/stock/sales {"lines":[{"item_id":"indomie","quantity":1}],"paid_how":"pos","amount_kobo":950000,"entry_id":"E1","note":""}',
    );
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('an item changed: only what changed sent; History filters every change', (t) async {
    final sent = await open(t);
    sent.clear();
    await t.tap(find.text('Rice').first);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Change'));
    await t.pumpAndSettle();
    // Name, unit, cost, price, reorder at, order up to, supplier.
    await t.enterText(find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText)).at(4), '5');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    expect(sent.first, 'PATCH /v1/stock/items/rice {"reorder_level":5}');
    sent.clear();
    await t.tap(find.text('Rice').first);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('History'));
    await settle(t);
    expect(sent.single, 'GET /v1/stock/movements limit=50&item=rice');
    expect(find.text('Rice: every change'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('counts and losses: the month, the latest count; the shelf counted with why; the limit saved', (t) async {
    final sent = await open(t);
    expect(find.text('This month: 40 in, 30 sold, 3 lost (₦25,500 at cost). Lost most: Indomie (3, ₦25,500).'), findsOneWidget);
    expect(find.text('CT-1 · 2 items, every item'), findsOneWidget);
    expect(find.text('₦25,500 missing'), findsNWidgets(2), reason: 'the last count’s tile, and the count');
    sent.clear();
    await t.tap(find.bySemanticsLabel('Count the shelf'));
    await t.pumpAndSettle();
    final boxes = find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText));
    await t.enterText(boxes.at(0), '1');
    await t.pump();
    expect(find.text('4 → 1 carton (3 fewer)'), findsOneWidget);
    await t.enterText(boxes.at(2), 'rats');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Record the count'));
    await settle(t);
    expect(sent.first, 'POST /v1/stock/counts {"lines":[{"item_id":"indomie","count":1}],"reason":"rats"}');
    await t.pump(const Duration(seconds: 5));
    sent.clear();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^A count missing more')), matching: find.byType(EditableText)), '10000');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save the limit'));
    await settle(t);
    expect(sent.where((x) => !x.startsWith('GET')), ['PUT /v1/stock/settings {"count_limit_kobo":1000000}']);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('counts: the month’s figures failing leaves the counts; a refused limit and count say why where they were', (t) async {
    http.Response bad(String words, int status) => http.Response(jsonEncode({'error': words}), status, headers: {'content-type': 'application/json'});
    final sent = await open(
      t,
      also: (req) async => switch ((req.method, req.url.path)) {
        ('GET', '/v1/stock/shrinkage') => bad('The month could not be added up.', 500),
        ('PUT', '/v1/stock/settings') => bad('A limit is at most ₦10,000,000.', 400),
        ('POST', '/v1/stock/counts') => bad('Rice is not counted by the shelf.', 400),
        _ => null,
      },
    );
    expect(find.text('The month could not be added up.'), findsOneWidget);
    expect(find.text('CT-1 · 2 items, every item'), findsOneWidget, reason: 'the counts read on their own');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^A count missing more')), matching: find.byType(EditableText)), '99999999');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save the limit'));
    await settle(t);
    final why = find.text('A limit is at most ₦10,000,000.');
    expect(why, findsOneWidget);
    expect(t.getTopLeft(why).dy, greaterThan(t.getTopLeft(find.bySemanticsLabel('Save the limit')).dy), reason: 'under its button');
    sent.clear();
    await t.tap(find.bySemanticsLabel('Count the shelf'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText)).at(1), '18');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Record the count'));
    await settle(t);
    expect(sent.single, 'POST /v1/stock/counts {"lines":[{"item_id":"rice","count":18}],"reason":""}');
    expect(find.text('Rice is not counted by the shelf.'), findsOneWidget);
    expect(find.byType(TkModal), findsOneWidget, reason: 'kept open, to try again');
  });

  testWidgets('what you count: each stock item sold as its products, or Not sold; All, Sold, Not sold', (t) async {
    final sent = await open(t);
    expect(sent.where((x) => x.startsWith('GET /v1/stock/stock-items')), ['GET /v1/stock/stock-items '], reason: 'every one, filtered here');
    final items = find.byKey(const ValueKey('items'));
    Finder inItems(String s) => find.descendant(of: items, matching: find.text(s));
    expect(find.text('What you count'), findsOneWidget);
    expect(inItems('Sold as Indomie · cost ₦8,500 · low at 10 · Dangote · order 46 cartons'), findsOneWidget);
    expect(inItems('Sold as Malt, Malt, pack of 6 (6 a sale)'), findsOneWidget);
    expect(inItems('Not sold · low at 50'), findsOneWidget);
    expect(inItems('200 bags'), findsOneWidget);
    // A product's sizes, grouped under it.
    expect(inItems('Ankara shirt'), findsOneWidget);
    expect(inItems('Sold in 2 sizes and colours · 1 low'), findsOneWidget);
    expect(inItems('4 pieces'), findsOneWidget, reason: 'its sizes together');
    expect(inItems('M'), findsOneWidget);
    expect(inItems('L'), findsOneWidget);
    expect(find.text('₦83,000'), findsOneWidget, reason: 'the stock at cost, as the server adds it up');

    await t.tap(find.bySemanticsLabel('Not sold'));
    await t.pumpAndSettle();
    expect(inItems('Nylon bags'), findsOneWidget);
    expect(inItems('Malt'), findsNothing);
    expect(inItems('Ankara shirt'), findsNothing);

    await t.tap(find.bySemanticsLabel('Sold'));
    await t.pumpAndSettle();
    expect(inItems('Nylon bags'), findsNothing);
    expect(inItems('Malt'), findsOneWidget);
    expect(inItems('Ankara shirt'), findsOneWidget);
  });

  testWidgets('a stock item’s actions go to the product it is sold as, or the material itself', (t) async {
    final sent = await open(t);
    sent.clear();
    final items = find.byKey(const ValueKey('items'));
    await t.tap(find.descendant(of: items, matching: find.text('Nylon bags')));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Change'));
    await t.pumpAndSettle();
    expect(find.text('Selling price (₦)'), findsNothing, reason: 'a material has no price');
    // Name, unit, cost, low at, order up to, supplier.
    await t.enterText(find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText)).at(3), '60');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    expect(sent.first, 'PATCH /v1/stock/items/si-nylon {"reorder_level":60}');
    sent.clear();
    await t.tap(find.descendant(of: items, matching: find.text('Malt')));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('History'));
    await settle(t);
    expect(sent.where((x) => x.contains('movements')).single, 'GET /v1/stock/movements limit=50&item=malt');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Add something to count asks Do you sell it?: No makes a material, Yes a product with its price', (t) async {
    final sent = await open(t);
    sent.clear();
    expect(find.bySemanticsLabel('Add items'), findsNothing, reason: 'the old way in is gone');
    await t.tap(find.bySemanticsLabel('Add something to count'));
    await t.pumpAndSettle();
    expect(find.text('Do you sell it?'), findsOneWidget);
    expect(find.text('Selling price'), findsNothing, reason: 'not before Yes');
    final boxes = find.descendant(of: find.byType(AddToCountSheet), matching: find.byType(EditableText));
    await t.enterText(boxes.at(0), 'Ankara fabric');
    await t.enterText(boxes.at(1), '20');
    await t.enterText(boxes.at(2), 'yard');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Add it'));
    await t.pumpAndSettle();
    expect(sent, isEmpty, reason: 'nothing goes until the question is answered');
    await t.tap(find.text('No, I don’t'));
    await t.pump();
    expect(find.text('Selling price'), findsNothing);
    await t.tap(find.bySemanticsLabel('Add it'));
    await settle(t);
    expect(sent.first, 'POST /v1/stock/items {"name":"Ankara fabric","quantity":20,"unit":"yard","material":true}');
    expect(find.text('Ankara fabric added'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));

    sent.clear();
    await t.tap(find.bySemanticsLabel('Add something to count'));
    await t.pumpAndSettle();
    await t.tap(find.text('Yes, I sell it'));
    await t.pump();
    expect(find.text('Selling price'), findsOneWidget);
    await t.enterText(boxes.at(0), 'Zobo');
    await t.enterText(boxes.at(1), '12');
    await t.enterText(boxes.at(2), 'bottle');
    await t.enterText(boxes.at(3), '1,500');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Add it'));
    await settle(t);
    expect(sent.first, 'POST /v1/stock/items {"name":"Zobo","quantity":12,"unit":"bottle","price_kobo":150000}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('what you count and Add something to count: large text cuts nothing off; each thing labelled and big enough', (t) async {
    // No counts: the latest counts' older card (chats.dart LinkSection) is
    // not this check's.
    await open(t, also: (req) async => req.url.path == '/v1/stock/counts' ? http.Response('{"counts":[]}', 200, headers: {'content-type': 'application/json'}) : null);
    t.view.physicalSize = const Size(360, 640);
    await t.pumpAndSettle();
    t.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(t.platformDispatcher.clearTextScaleFactorTestValue);
    await t.pumpAndSettle();
    await t.scrollUntilVisible(find.text('Not sold').first, 200, scrollable: find.byType(Scrollable).first);
    await t.pumpAndSettle();
    await expectPressable(t);
    await t.tap(find.bySemanticsLabel('Add something to count'));
    await t.pumpAndSettle();
    await t.tap(find.text('No, I don’t'));
    await t.pumpAndSettle();
    await expectPressable(t);
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pumpAndSettle();
}
