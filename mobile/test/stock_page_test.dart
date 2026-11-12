// The Stock screen (M6 part T; web StorefrontStock.tsx, lib/stock.ts):
// today's sales, what is low, each item, every change; a sale recorded,
// stock recorded with a reason, an item added and changed.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/stock_page.dart';
import 'package:taskiem/kit/modal.dart' show TkModal;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  test('the words for an item and a change', () {
    expect(plural('box'), 'boxes');
    expect(plural('battery'), 'batteries');
    expect(stockQuantity(1, 'carton'), '1 carton');
    expect(stockLine({'stock': null}), 'No count');
    expect(itemSub({'reorder_level': 10, 'price_kobo': 950000, 'supplier': 'Dangote', 'low': true, 'suggest': 40, 'unit': 'carton'}), 'reorder at 10 · sells at ₦9,500 · Dangote · order 40 cartons');
    expect(movementTitle({'item': 'Indomie', 'reason': 'received', 'change': 50}), 'Indomie: received +50');
    expect(movementTitle({'item': 'Rice', 'reason': 'count', 'set_to': 12}), 'Rice: counted 12');
  });

  Future<List<String>> open(WidgetTester t) async {
    t.view.physicalSize = const Size(390, 4000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
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
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: StockPage(auth: chats.auth)));
    await settle(t);
    return sent;
  }

  testWidgets('what the page shows', (t) async {
    await open(t);
    expect(find.text('₦19,000'), findsNWidgets(2), reason: 'today’s sales, and the sale');
    expect(find.text('1 sale · Cash ₦19,000'), findsOneWidget);
    expect(find.text('Terminal sales with no items'), findsOneWidget);
    expect(find.text('2 cartons Indomie'), findsOneWidget);
    expect(find.text('Low on stock'), findsOneWidget);
    expect(find.text('4 cartons'), findsNWidgets(2), reason: 'low, and among the items');
    expect(find.text('Rice: received +20'), findsOneWidget);
  });

  testWidgets('stock recorded: some spoilt, taken off, with why', (t) async {
    final sent = await open(t);
    sent.clear();
    await t.tap(find.bySemanticsLabel('Record stock of Indomie').first);
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
    await t.tap(find.bySemanticsLabel('Change Rice'));
    await t.pumpAndSettle();
    // Name, unit, cost, price, reorder at, order up to, supplier.
    await t.enterText(find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText)).at(4), '5');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    expect(sent.first, 'PATCH /v1/stock/items/rice {"reorder_level":5}');
    sent.clear();
    await t.tap(find.bySemanticsLabel('History of Rice'));
    await settle(t);
    expect(sent.single, 'GET /v1/stock/movements limit=50&item=rice');
    expect(find.text('Rice: every change'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('counts and losses: the month, the latest count; the shelf counted with why; the limit saved', (t) async {
    final sent = await open(t);
    expect(find.text('This month: 40 in, 30 sold, 3 lost (₦25,500 at cost). Lost most: Indomie (3, ₦25,500).'), findsOneWidget);
    expect(find.text('CT-1 · 2 items, every item'), findsOneWidget);
    expect(find.text('₦25,500 missing'), findsOneWidget);
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
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pumpAndSettle();
}
