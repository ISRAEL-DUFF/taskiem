// A business's shop (lib/chat/business_shop.dart; docs/spec/mobile-app-look.md,
// "The business shop"): a linked supplier's prices as the person's shop
// draws a storefront. + goes to their minimum, the item shows their
// quantity prices, a product's sizes are typed in the order sheet's grid,
// the bag is checked out in the order sheet's review as a purchase order,
// the plain list is a press away, the hearts are kept by the business we
// act for and the supplier; those who may not order look around.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show ListLinkChatsResponse;
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart' show MemoryKeyStore;
import 'package:taskiem/chat/business_shop.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/shop.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart';
import 'treat_fixture.dart';

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
      'description': 'Stone-free local rice, 50kg.',
      'price_kobo': 4500000,
      'min_quantity': 2,
      'available': true,
      'breaks': [
        {'min_quantity': 10, 'price_kobo': 4000000},
        {'min_quantity': 50, 'price_kobo': 3880000},
      ],
    },
    {'item_id': 'beans', 'name': 'Oloyin beans', 'unit': 'bag', 'category': 'Grains', 'price_kobo': 3000000, 'min_quantity': 1, 'available': true},
    {'item_id': 'gari', 'name': 'Ijebu gari', 'unit': 'bag', 'category': 'Flour', 'price_kobo': 2000000, 'min_quantity': 1, 'available': false, 'left': 0},
    {
      'item_id': 'shoe',
      'name': 'Canvas sneaker',
      'unit': 'pair',
      'category': 'Shoes',
      'price_kobo': 800000,
      'min_quantity': 6,
      'available': true,
      'breaks': [
        {'min_quantity': 50, 'price_kobo': 700000},
      ],
      'choices': [
        {'name': 'Size', 'values': ['42', '43']},
      ],
      'sizes': [
        {'item_id': 's42', 'name': 'Canvas sneaker – 42', 'variant': {'Size': '42'}, 'available': true},
        {'item_id': 's43', 'name': 'Canvas sneaker – 43', 'variant': {'Size': '43'}, 'available': true},
      ],
    },
  ],
};

http.Response _ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

/// The answers a supplier's shop and the order sheet read; what is sent,
/// written down.
Future<http.Response?> Function(http.Request) _server(List<String> sent, List<String> bodies) => (req) async {
  final p = req.url.path;
  final treat = treatAnswers(req);
  if (treat != null) return treat;
  if (req.method == 'GET') {
    return switch (p) {
      '/v1/supplier-prices' => _ok({'suppliers': [_list]}),
      '/v1/item-links/L1' => _ok({'item_links': <Object>[]}),
      '/v1/stock/suppliers' => _ok({
        'suppliers': [
          {'id': 'S1', 'name': 'Bayo Rice Mills', 'link_id': 'L1'},
        ],
      }),
      '/v1/stock/purchases' => _ok({'purchases': <Object>[]}),
      '/v1/stock' => _ok({'keeps_stock': true, 'items': <Object>[]}),
      _ => null,
    };
  }
  sent.add('${req.method} $p');
  if (p == '/v1/supplier-prices/L1/order') {
    bodies.add(req.body);
    return _ok({'purchase': {'id': 'P1', 'reference': 'PO-1'}});
  }
  return _ok({});
};

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
    await t.pump();
  }
  await t.pumpAndSettle();
}

Future<void> press(WidgetTester t, Finder f) async {
  await t.ensureVisible(f.first);
  await t.pumpAndSettle();
  await t.tap(f.first);
  await settle(t);
}

void main() {
  setUpAll(loadFonts);
  setUp(() => ShopBag.of('link:L1').clear());

  final sent = <String>[];
  final bodies = <String>[];
  final asked = <String>[];

  BusinessShop source(ChatsData chats, {bool supplies = true}) => BusinessShop(
    chats: chats,
    link: 'L1',
    business: 'B1',
    name: 'Bayo Rice Mills',
    face: () => const ChatAvatar(name: 'Bayo Rice Mills', size: 40, letterSize: 15),
    supplies: supplies,
    onAsk: asked.add,
  );

  /// Their shop, opened as the conversation opens it.
  Future<ChatsData> open(WidgetTester t, {bool supplies = true, Prefs? prefs, TkMode mode = TkMode.light, String? item, Size size = const Size(390, 844)}) async {
    t.view.physicalSize = size;
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    sent.clear();
    bodies.clear();
    asked.clear();
    final f = Fixture('chats');
    final chats = ChatsData(f.auth(also: _server(sent, bodies)), now: () => f.at, prefs: prefs)..load();
    await t.pumpWidget(TaskiemApp(mode: mode, home: ShopScreen(source: source(chats, supplies: supplies), item: item)));
    await settle(t);
    return chats;
  }

  testWidgets('their prices as a shop: photos two across, + to their minimum, the bag at their quantity prices', (t) async {
    await open(t);
    expect(find.text('Bayo Rice Mills'), findsOneWidget);
    expect(find.text('Your prices'), findsOneWidget);
    expect(find.bySemanticsLabel('Ofada rice, ₦45,000'), findsOneWidget);
    expect(find.bySemanticsLabel('Ijebu gari, ₦20,000, Sold out'), findsOneWidget);
    for (final c in ['All', 'Saved', 'Grains', 'Flour', 'Shoes']) {
      expect(find.text(c), findsOneWidget, reason: c);
    }
    // + from nothing: their minimum, 2 bags.
    await press(t, find.bySemanticsLabel('Add Ofada rice to the bag'));
    expect(ShopBag.of('link:L1').lines, {'rice': 2});
    expect(find.bySemanticsLabel('View bag: 2 items · ₦90,000'), findsOneWidget);
    // − under the minimum takes it off.
    await press(t, find.bySemanticsLabel('One fewer Ofada rice'));
    expect(ShopBag.of('link:L1').lines, isEmpty);
    // A product sold in sizes: chosen on its own screen.
    expect(find.bySemanticsLabel('Choose a size of Canvas sneaker'), findsOneWidget);
    await t.pumpWidget(const SizedBox());
  });

  testWidgets('an item: its quantity prices, the one reached marked; Add to bag at that price; Ask about this begins the question', (t) async {
    await open(t);
    await press(t, find.bySemanticsLabel('Ofada rice, ₦45,000'));
    expect(find.text('GRAINS · BAYO RICE MILLS'), findsOneWidget);
    expect(find.text('Stone-free local rice, 50kg.'), findsOneWidget);
    expect(find.text('Quantity prices'), findsOneWidget);
    expect(find.bySemanticsLabel('2 to 9 bags: ₦45,000 each, your price now'), findsOneWidget);
    expect(find.bySemanticsLabel('10 to 49 bags: ₦40,000 each'), findsOneWidget);
    expect(find.bySemanticsLabel('50 bags and more: ₦38,800 each'), findsOneWidget);
    expect(find.text('At least 2 bags an order.'), findsOneWidget);
    // Starts at their minimum.
    expect(find.text('Add to bag · ₦90,000'), findsOneWidget);
    for (var i = 0; i < 8; i++) {
      await t.tap(find.bySemanticsLabel('One more Ofada rice'));
      await t.pump();
    }
    // 10 bags: the 10-bag price.
    expect(find.text('Add to bag · ₦400,000'), findsOneWidget);
    expect(find.bySemanticsLabel('10 to 49 bags: ₦40,000 each, your price now'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Add to bag'));
    expect(ShopBag.of('link:L1').lines, {'rice': 10});
    await t.pump(const Duration(seconds: 5));
    await press(t, find.bySemanticsLabel('Ask Bayo Rice Mills about Ofada rice in the chat'));
    expect(asked, ['About Ofada rice, ₦45,000: is it still available?']);
    await t.pumpWidget(const SizedBox());
  });

  testWidgets('sizes typed in the grid go in the bag; the bag says what is short, then Review order places a purchase order', (t) async {
    await open(t);
    await press(t, find.bySemanticsLabel('Choose a size of Canvas sneaker'));
    expect(find.text('Quantity prices'), findsOneWidget);
    expect(find.text('Its sizes count together: 50 pairs across sizes get the 50-pair price.'), findsOneWidget);
    expect(find.text('How many of each'), findsOneWidget);
    expect(find.bySemanticsLabel('Type how many of each size'), findsOneWidget);
    await t.ensureVisible(find.bySemanticsLabel(RegExp(r'^42: how many')));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel(RegExp(r'^42: how many')));
    await t.pump();
    t.testTextInput.enterText('3');
    await t.pump();
    expect(ShopBag.of('link:L1').lines, {'s42': 3});
    FocusManager.instance.primaryFocus?.unfocus();
    await press(t, find.bySemanticsLabel(RegExp('^View bag')));
    expect(find.text('Your bag'), findsOneWidget);
    expect(find.text('Canvas sneaker: at least 6 in all, its sizes together.'), findsOneWidget);
    // One more of each size, to 6: then it can go on.
    await press(t, find.bySemanticsLabel('One more Canvas sneaker'));
    await press(t, find.bySemanticsLabel('One more Canvas sneaker'));
    await press(t, find.bySemanticsLabel('One more Canvas sneaker'));
    expect(ShopBag.of('link:L1').lines, {'s42': 6});
    expect(find.bySemanticsLabel('Review order, ₦48,000'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Review order, ₦48,000'));
    // The order sheet's own review.
    expect(find.text('Review your order'), findsOneWidget);
    expect(find.text('6 × Canvas sneaker – 42'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Place order'));
    expect(sent, ['POST /v1/supplier-prices/L1/order', 'POST /v1/stock/purchases/P1/send']);
    expect(jsonDecode(bodies.single)['lines'], [
      {'seller_item_id': 's42', 'quantity': 6, 'treatment': 'record', 'label_id': 'lb-pack'},
    ]);
    expect(find.text('Order sent to Bayo Rice Mills: ₦48,000'), findsOneWidget);
    await t.pump(const Duration(seconds: 2));
    await settle(t);
    // Placed: the bag empty, back where the shop was opened from.
    expect(ShopBag.of('link:L1').lines, isEmpty);
    expect(find.text('Your bag'), findsNothing);
    await t.pumpWidget(const SizedBox());
  });

  testWidgets('List: the plain list of the same, its bag the shop’s', (t) async {
    await open(t);
    await press(t, find.bySemanticsLabel('Add Oloyin beans to the bag'));
    await press(t, find.bySemanticsLabel('List view: their prices as a list'));
    expect(find.text('Order from Bayo Rice Mills'), findsOneWidget);
    expect(find.text('1 item in your order'), findsOneWidget);
    await press(t, find.bySemanticsLabel('One more Ofada rice'));
    expect(ShopBag.of('link:L1').lines, {'beans': 1, 'rice': 2}, reason: 'what the list changes is the shop’s bag');
    await t.pumpWidget(const SizedBox());
  });

  testWidgets('the hearts: kept on the phone by the business we act for and the supplier; forgotten on signing out', (t) async {
    final prefs = Prefs(MemoryKeyStore());
    final chats = await open(t, prefs: prefs);
    await press(t, find.bySemanticsLabel('Save Oloyin beans'));
    final key = 'business:${chats.auth.me?.tenantId ?? ''}:B1';
    expect(prefs.shopSaved, {
      key: ['beans'],
    });
    await press(t, find.text('Saved · 1'));
    expect(find.bySemanticsLabel(RegExp('^Oloyin beans')), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Ofada rice')), findsNothing);
    await press(t, find.bySemanticsLabel('Oloyin beans, ₦30,000'));
    await press(t, find.bySemanticsLabel('Back to the shop'));
    expect(prefs.shopRecent[key], ['beans']);
    await prefs.forgetPerson();
    expect(prefs.shopSaved, isEmpty);
    expect(prefs.shopRecent, isEmpty);
    await t.pumpWidget(const SizedBox());
  });

  testWidgets('one who may not order looks around: no bag, no +', (t) async {
    await open(t, supplies: false);
    expect(find.text('You can look around. Placing orders with Bayo Rice Mills is for those on your team who may.'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Your bag')), findsNothing);
    expect(find.bySemanticsLabel('Add Ofada rice to the bag'), findsNothing);
    await press(t, find.bySemanticsLabel('Ofada rice, ₦45,000'));
    expect(find.bySemanticsLabel('Add to bag'), findsNothing);
    await t.pumpWidget(const SizedBox());
  });

  testWidgets('the conversation: Their shop in the head, and from the tools', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-bayo');
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          if (req.url.path.endsWith('/items') && req.url.path.startsWith('/v1/link-chats/')) {
            return _ok({...f.json(req.url.path), 'whose': 'theirs'});
          }
          if (req.url.path == '/v1/supplier-prices') return _ok({'suppliers': [_list]});
          return null;
        },
      ),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Their shop'));
    await settle(t);
    expect(find.bySemanticsLabel('Ofada rice, ₦45,000'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Back to the chats').first);
    await settle(t);
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp('^Their shop, ')));
    await settle(t);
    expect(find.bySemanticsLabel('Ofada rice, ₦45,000'), findsOneWidget);
    await t.pumpWidget(const SizedBox());
  });


  testWidgets('the shop, an item with quantity prices, its sizes, the bag, the review and the list look as drawn, light and dark; pressable', (t) async {
    await open(t);
    await press(t, find.bySemanticsLabel('Add Ofada rice to the bag'));
    await press(t, find.bySemanticsLabel('Save Oloyin beans'));
    await expectPressable(t);
    await looks(t, 'business-shop');
    await press(t, find.bySemanticsLabel(RegExp('^Ofada rice, ')));
    await expectPressable(t);
    await looks(t, 'business-shop-item');
    await press(t, find.bySemanticsLabel('Back to the shop'));
    await press(t, find.bySemanticsLabel('Choose a size of Canvas sneaker'));
    await expectPressable(t);
    await looks(t, 'business-shop-sizes');
    await press(t, find.bySemanticsLabel('Back to the shop'));
    await press(t, find.bySemanticsLabel(RegExp('^View bag')));
    await expectPressable(t);
    await looks(t, 'business-shop-bag');
    await press(t, find.bySemanticsLabel(RegExp('^Review order, ')));
    await looks(t, 'business-shop-review');
    await t.pumpWidget(const SizedBox());
    await open(t);
    await press(t, find.bySemanticsLabel('Add Oloyin beans to the bag'));
    await press(t, find.bySemanticsLabel('List view: their prices as a list'));
    await looks(t, 'business-shop-list');
    await t.pumpWidget(const SizedBox());
  });

  testWidgets('large text cuts nothing off: the shop and an item', (t) async {
    final chats = await open(t);
    await t.pumpWidget(const SizedBox());
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: ShopScreen(source: source(chats))), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: ShopScreen(source: source(chats), item: 'rice')), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: ShopScreen(source: source(chats), item: 'shoe')), drive: settle);
    await t.pumpWidget(const SizedBox());
  });
}
