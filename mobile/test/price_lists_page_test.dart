// The Price lists screen (M6 part R; web PriceLists.tsx): lists made,
// their items priced with lower prices for more, shared, deleted after
// yes; the staff discount limit.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/price_lists_page.dart';
import 'package:taskiem/kit/modal.dart' show TkModal;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  test('quantity prices: each for more, each cheaper', () {
    expect(breaksProblem(1, 4000, [(qty: 10, price: 3900), (qty: 50, price: 3800)]), '');
    expect(breaksProblem(5, 4000, [(qty: 5, price: 3900)]), 'Each quantity is more than the one before it.');
    expect(breaksProblem(1, 4000, [(qty: 10, price: 4000)]), 'Each price is lower than the one before it.');
  });

  Future<List<String>> open(WidgetTester t) async {
    t.view.physicalSize = const Size(390, 3000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET') {
          return switch (p) {
            '/v1/price-lists' => ok({
              'lists': [
                {
                  'id': 'PL1',
                  'name': 'Trade',
                  'links': ['K1'],
                  'items': [
                    {'item_id': 'rice', 'price_kobo': 4000000, 'min_quantity': 1, 'available': true},
                  ],
                },
              ],
            }),
            '/v1/links' => ok({
              'links': [
                {'id': 'K1', 'name': 'Rivers Foods', 'state': 'active'},
                {'id': 'K2', 'name': 'Eko Events', 'state': 'active'},
              ],
            }),
            '/v1/stock' => ok({
              'keeps_stock': true,
              'items': [
                {'id': 'rice', 'name': 'Rice', 'unit': 'bag', 'available': true, 'stock': 9, 'price_kobo': 4500000},
                {'id': 'beans', 'name': 'Beans', 'unit': 'bag', 'available': true, 'stock': 3, 'price_kobo': 3000000},
              ],
            }),
            '/v1/trade-floors' => ok({'floors': <String, Object>{}, 'max_discount_pct': null}),
            _ => null,
          };
        }
        sent.add('${req.method} $p ${req.body}');
        return ok({});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PriceListsPage(auth: chats.auth)));
    await settle(t);
    return sent;
  }

  testWidgets('a list’s items: one added, a lower price for more', (t) async {
    final sent = await open(t);
    expect(find.text('1 item · shared with Rivers Foods'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Items of Trade'));
    await settle(t);
    await snap(t, 'price_lists_page-0');
    await t.tap(find.text('Beans (bag)'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('+ Lower price for more of Rice'));
    await t.pump();
    final inModal = find.descendant(of: find.byType(TkModal), matching: find.byType(EditableText));
    // Rice: price, at least, from, its price; then Beans: price, at least.
    await t.enterText(inModal.at(2), '10');
    await t.enterText(inModal.at(3), '38,000');
    await t.pump();
    await t.tap(find.descendant(of: find.byType(TkModal), matching: find.bySemanticsLabel('Save')));
    await settle(t);
    expect(
      sent.single,
      'PUT /v1/price-lists/PL1/items {"items":[{"item_id":"rice","price_kobo":4000000,"min_quantity":1,"available":true,"breaks":[{"min_quantity":10,"price_kobo":3800000}]},'
      '{"item_id":"beans","price_kobo":3000000,"min_quantity":1,"available":true,"breaks":[]}]}',
    );
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('shared with one more; a new list made; the discount limit', (t) async {
    final sent = await open(t);
    await t.tap(find.bySemanticsLabel('Share Trade'));
    await settle(t);
    await snap(t, 'price_lists_page-1');
    await t.tap(find.text('Eko Events'));
    await t.pump();
    await t.tap(find.descendant(of: find.byType(TkModal), matching: find.bySemanticsLabel('Save')));
    await settle(t);
    expect(sent.single, 'PUT /v1/price-lists/PL1/links {"links":["K1","K2"]}');
    sent.clear();
    await t.enterText(find.bySemanticsLabel('New price list’s name'), 'VIP');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Make a price list'));
    await settle(t);
    expect(sent.single, 'POST /v1/price-lists {"name":"VIP"}');
    sent.clear();
    await t.enterText(find.byType(EditableText).last, '95');
    await t.pump();
    expect(find.text('A whole number from 1 to 90.'), findsOneWidget);
    await t.enterText(find.byType(EditableText).last, '10');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    expect(sent.single, 'PUT /v1/trade-floors {"set_discount":true,"max_discount_pct":10}');
    await t.pump(const Duration(seconds: 5));
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pumpAndSettle();
}
