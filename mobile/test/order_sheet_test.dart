// Ordering from a linked business (M5 part O; web ChatBuyer.tsx
// OrderSheet): − and + fill the bag, the review asks what it must, Place
// order drafts and sends each step once (a retry after a failure part way
// does not make a second supplier or order), an offer is under their price.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/buyer.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/order_sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

final _list = {
  'link': 'L1',
  'business': 'Bayo Rice Mills',
  'name': 'Wholesale',
  'updated_at': '2020-01-01T00:00:00Z',
  'items': [
    {
      'item_id': 'rice',
      'name': 'Ofada rice',
      'unit': 'bag',
      'category': 'Grains',
      'price_kobo': 4500000,
      'min_quantity': 2,
      'available': true,
      'breaks': [
        {'min_quantity': 10, 'price_kobo': 4000000},
      ],
    },
    {'item_id': 'beans', 'name': 'Oloyin beans', 'unit': 'bag', 'category': 'Grains', 'price_kobo': 3000000, 'min_quantity': 1, 'available': true},
    {'item_id': 'gari', 'name': 'Ijebu gari', 'unit': 'bag', 'category': 'Flour', 'price_kobo': 2000000, 'min_quantity': 1, 'available': false},
    {
      'item_id': 'sack',
      'name': 'Jute sack',
      'unit': 'sack',
      'category': 'Flour',
      'price_kobo': 50000,
      'min_quantity': 1,
      'available': true,
      'choices': [
        {'name': 'Size', 'values': ['S', 'L']},
      ],
      'sizes': [
        {'item_id': 'sackS', 'name': 'Jute sack – S', 'variant': {'Size': 'S'}, 'available': true},
        {'item_id': 'sackL', 'name': 'Jute sack – L', 'variant': {'Size': 'L'}, 'available': true},
      ],
    },
  ],
};

void main() {
  setUpAll(loadFonts);

  group('the bag', () {
    final items = [for (final j in _list['items'] as List) PriceItem.fromJson(j as Map<String, dynamic>)];
    test('+ from nothing goes to the minimum; − under it takes it off', () {
      expect(stepQty(0, 2, 1), 2);
      expect(stepQty(2, 2, 1), 3);
      expect(stepQty(2, 2, -1), 0);
      expect(stepQty(3, 2, -1), 2);
    });
    test('a quantity price from its break; the count of items', () {
      final b = Bag(items, {'rice': 10, 'beans': 1});
      expect(b.total, 10 * 4000000 + 3000000);
      expect(b.count, '2 items in your order');
      expect(Bag(items, {}).count, 'Nothing yet');
      expect(breakWords(items[0]), 'from 10 bags ₦40,000');
      expect(rowLine(items[2]), 'Not available now');
    });
    test('sections by category, Other last; search by name first', () {
      final s = orderSections([...items, const PriceItem(id: 'x', name: 'Salt', price: 1)]);
      expect(s.map((x) => x.label), ['Grains', 'Flour', 'Other']);
      expect(orderShown(items, null, 'grain').map((x) => x.id), ['rice', 'beans']);
      expect(priceChips(items).map((c) => c.label), ['Flour', 'Grains']);
    });
    test('sizes as lines of their own, at the product\'s price unless their own', () {
      final shoe = PriceItem.fromJson({
        'item_id': 'shoe',
        'name': 'Sneaker',
        'unit': 'pair',
        'price_kobo': 1000,
        'min_quantity': 3,
        'available': true,
        'choices': [
          {'name': 'Size', 'values': ['42', '43']},
        ],
        'sizes': [
          {'item_id': 's42', 'name': 'Sneaker – 42', 'variant': {'Size': '42'}, 'available': true},
          {'item_id': 's43', 'name': 'Sneaker – 43', 'variant': {'Size': '43'}, 'price_kobo': 1200, 'available': true},
        ],
      });
      expect(soldInSizes(shoe), isTrue);
      expect(shoe.sizes.first.label, '42');
      final b = Bag([shoe], {'s42': 1, 's43': 1});
      expect(b.total, 2200);
      expect(shortOf([shoe], {'s42': 1, 's43': 1})?.id, 'shoe');
      expect(sizesTaken(shoe, {'s42': 2, 's43': 1}), '3 pairs in 2 sizes and colours');
    });
    test('a quote asked as a message', () {
      expect(quoteText(Bag(items, {'rice': 2}).lines), 'Could you quote me for:\n• 2 × Ofada rice');
    });
  });

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  Future<List<String>> open(
    WidgetTester t, {
    bool failSend = false,
    bool browse = false,
    OrderStart? start,
    List<Object> purchases = const [],
    void Function(String)? onSent,
    Duration sendTakes = Duration.zero,
  }) async {
    t.view.physicalSize = const Size(390, 1400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    var failing = failSend;
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET') {
          return switch (p) {
            '/v1/supplier-prices' => ok({'suppliers': [_list]}),
            '/v1/item-links/L1' => ok({'item_links': <Object>[]}),
            '/v1/stock/suppliers' => ok({'suppliers': <Object>[]}),
            '/v1/stock/purchases' => ok({'purchases': purchases}),
            '/v1/stock' => ok({'keeps_stock': true, 'items': <Object>[]}),
            _ => null,
          };
        }
        final key = req.headers['Idempotency-Key'] ?? req.headers['idempotency-key'];
        sent.add('${req.method} $p${key != null ? ' (once)' : ''}');
        if (p == '/v1/stock/suppliers') return ok({'supplier': {'id': 'S1'}});
        if (p == '/v1/stock/items') return ok({'item': {'id': 'I1'}});
        if (p == '/v1/supplier-prices/L1/order') return ok({'purchase': {'id': 'P1', 'reference': 'PO-1'}});
        if (p == '/v1/stock/purchases/P1/send') await Future<void>.delayed(sendTakes);
        if (p == '/v1/stock/purchases/P1/send' && failing) {
          failing = false;
          return http.Response(jsonEncode({'error': 'Something went wrong'}), 500, headers: {'content-type': 'application/json'});
        }
        return ok({});
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: OrderSheet(chats: chats, link: 'L1', business: 'Bayo Rice Mills', browse: browse, start: start, onSent: onSent),
      ),
    );
    await settle(t);
    return sent;
  }

  testWidgets('pick, review, place: each step once though tried twice', (t) async {
    final sent = await open(t, failSend: true);
    expect(find.text('Order from Bayo Rice Mills'), findsOneWidget);
    expect(find.text('Your prices · as they share them'), findsOneWidget);
    expect(find.text('Nothing yet'), findsOneWidget);
    // + goes to the minimum, 2 bags.
    await t.tap(find.bySemanticsLabel('One more Ofada rice'));
    await t.pump();
    expect(find.text('1 item in your order'), findsOneWidget);
    expect(find.text('₦90,000'), findsOneWidget);
    // Not available: no +.
    await t.tap(find.bySemanticsLabel('One more Ijebu gari'));
    await t.pump();
    expect(find.text('1 item in your order'), findsOneWidget);

    await t.tap(find.bySemanticsLabel('Review order'));
    await settle(t);
    expect(find.text('Review your order'), findsOneWidget);
    expect(find.text('Bayo Rice Mills is not one of your suppliers yet. Orders and bills from them are kept with your other suppliers.'), findsOneWidget);
    expect(find.text("Add Bayo Rice Mills's phone number to place the order."), findsOneWidget);
    await t.enterText(find.bySemanticsLabel(RegExp("phone number")), '08031234567');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Place order'));
    await settle(t);
    // The send failed: tried again, the supplier, item and order are not made twice.
    expect(find.text('Something went wrong'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Place order'));
    await settle(t);
    expect(sent, [
      'POST /v1/stock/suppliers (once)',
      'PUT /v1/stock/suppliers/S1/link',
      'POST /v1/stock/items (once)',
      'POST /v1/supplier-prices/L1/order (once)',
      'POST /v1/stock/purchases/P1/send',
      'POST /v1/stock/purchases/P1/send',
    ]);
    expect(find.text('Order sent to Bayo Rice Mills: ₦90,000'), findsOneWidget);
    await t.pump(const Duration(seconds: 2));
    await t.pumpAndSettle();
  });

  testWidgets('an offer: under their price, said how far; a quote asked in the chat', (t) async {
    final sent = await open(t, start: const OrderStart(itemId: 'beans', offer: true));
    // Opened on its item: in the bag already.
    expect(find.text('1 item in your order'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Review order'));
    await settle(t);
    expect(find.text('Your offer'), findsOneWidget);
    await t.enterText(find.bySemanticsLabel(RegExp('Your price for the whole order')), '40k');
    await t.pump();
    expect(find.text('An offer is under their price, ₦30,000.'), findsOneWidget);
    await t.enterText(find.bySemanticsLabel(RegExp('Your price for the whole order')), '27k');
    await t.pump();
    expect(find.text('10% off their price'), findsOneWidget);
    expect(find.bySemanticsLabel('Send offer'), findsOneWidget);
    // Their price instead; then a quote.
    await t.tap(find.bySemanticsLabel('Pay their price instead'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Ask for a quote'));
    await settle(t);
    expect(sent, ['POST /v1/link-chats/L1/messages']);
    await t.pump(const Duration(seconds: 2));
    await t.pumpAndSettle();
  });

  testWidgets('looking around: their shop, no bag and no steppers', (t) async {
    await open(t, browse: true);
    expect(find.text("Bayo Rice Mills's shop"), findsOneWidget);
    expect(find.bySemanticsLabel('One more Ofada rice'), findsNothing);
    expect(find.bySemanticsLabel('Review order'), findsNothing);
    // A chip narrows it.
    await t.tap(find.bySemanticsLabel('Grains').first);
    await t.pump();
    expect(find.text('Ijebu gari'), findsNothing);
  });

  testWidgets('sizes: typed, the keyboard stays for the next digit; Same as last time fills the boxes', (t) async {
    await open(
      t,
      purchases: [
        {
          'id': 'P0',
          'reference': 'PO-0001',
          'link_id': 'L1',
          'status': 'received',
          'seller_status': 'delivered',
          'created_at': '2026-11-01T00:00:00Z',
          'lines': [
            {'item': 'Jute sack – L', 'quantity': 4},
          ],
        },
      ],
    );
    await t.tap(find.bySemanticsLabel('Choose sizes of Jute sack'));
    await t.pumpAndSettle();
    final s = find.bySemanticsLabel(RegExp(r'^S: how many'));
    await t.tap(s);
    await t.pump();
    t.testTextInput.enterText('1');
    await t.pump();
    // Still the box being typed in, after the bag changed with it.
    final focused = FocusManager.instance.primaryFocus;
    t.testTextInput.enterText('12');
    await t.pump();
    expect(FocusManager.instance.primaryFocus, same(focused));
    expect(find.text('12 sacks in 1 size or colour'), findsOneWidget);
    // The bag emptied, and filled again from the last order: L's box says 4.
    t.testTextInput.enterText('');
    await t.pump();
    await t.tap(find.bySemanticsLabel(RegExp('^Same as last time')));
    await t.pump();
    expect(find.text('4'), findsWidgets);
    expect(find.text('4 sacks in 1 size or colour'), findsOneWidget);
  });

  testWidgets('closed while the order is sending: it is still said it was sent', (t) async {
    final said = <String>[];
    await open(t, start: const OrderStart(itemId: 'beans'), onSent: said.add, sendTakes: const Duration(milliseconds: 300));
    await t.tap(find.bySemanticsLabel('Review order'));
    await settle(t);
    await t.enterText(find.bySemanticsLabel(RegExp('phone number')), '08031234567');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Place order'));
    await t.pump();
    // The sheet goes before the answer.
    await t.pumpWidget(const SizedBox());
    for (var i = 0; i < 10; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump(const Duration(milliseconds: 100));
    }
    expect(said, ['Order sent to Bayo Rice Mills: ₦30,000']);
    expect(t.takeException(), isNull);
  });
}
