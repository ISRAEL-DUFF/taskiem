// The shop (lib/chat/shop.dart; docs/spec/mobile-app-look.md, "The shop"):
// a business's items to browse from a person's chat with it, as photos two
// across; searched, filtered by category, saved with the heart, looked at
// lately; an item's own screen; the bag, checked out in the shop's sheet.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart' show MemoryKeyStore;
import 'package:taskiem/chat/my_chat.dart';
import 'package:taskiem/chat/shop.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'my_chats_test.dart' show Api, at, chat, conversation, drain, openChat, phone, press, settle;
import 'support.dart' show loadFonts;

Map<String, dynamic> item(String id, String name, int naira, {String? category, String? description, bool available = true, int? few, List<Map<String, dynamic>>? sizes}) => {
  'id': id,
  'name': name,
  'price_kobo': naira * 100,
  'available': available,
  'category': ?category,
  'description': ?description,
  'few_left': ?few,
  'sizes': ?sizes,
};

Api shopApi() {
  final api = conversation(c: chat(offers: 'no', ask: false));
  api.routes['GET /v1/me/chats/c1/shop'] = (_) => {
    'open': true,
    'payment': 'after_accept',
    'pickup': true,
    'delivery': true,
    'zones': [
      {'name': 'Yaba', 'fee_kobo': 100000},
    ],
    'items': [
      item('i1', 'Jollof rice & chicken', 4500, category: 'Rice', description: 'Smoky party jollof with a quarter chicken, plantain and coleslaw.', few: 3, sizes: [
        {'id': 'i1-s', 'variant': {'size': 'Small'}, 'price_kobo': 300000, 'available': true},
        {'id': 'i1-r', 'variant': {'size': 'Regular'}, 'price_kobo': 450000, 'available': true},
        {'id': 'i1-b', 'variant': {'size': 'Big'}, 'price_kobo': 600000, 'available': false},
      ]),
      item('i2', 'Fried rice & turkey', 5200, category: 'Rice', few: 2),
      item('i3', 'Egusi soup & pounded yam', 6000, category: 'Soups'),
      item('i4', 'Small chops platter', 3500, category: 'Snacks'),
      item('i5', 'Chapman', 1500, category: 'Drinks', available: false),
      item('i6', 'Zobo, 50cl', 800, category: 'Drinks'),
    ],
  };
  return api;
}

/// Into the shop from the chat's head.
Future<MyChatData> openShopScreen(WidgetTester t, Api api) async {
  final d = await openChat(t, api);
  await press(t, find.bySemanticsLabel('Their shop'));
  return d;
}

void main() {
  setUpAll(loadFonts);
  setUp(() => ShopBag.of('c1').clear());

  testWidgets('the shop: photos two across, prices, marks; search and categories', (t) async {
    phone(t);
    await openShopScreen(t, shopApi());
    expect(find.text('Mama’s Kitchen'), findsOneWidget);
    expect(find.text('Open · Delivery and pickup'), findsOneWidget);
    expect(find.bySemanticsLabel('Jollof rice & chicken, From ₦3,000'), findsOneWidget, reason: 'its sizes, from the cheapest');
    expect(find.bySemanticsLabel('Fried rice & turkey, ₦5,200, Only 2 left'), findsOneWidget);
    expect(find.bySemanticsLabel('Chapman, ₦1,500, Sold out'), findsOneWidget);
    expect(find.bySemanticsLabel('Add Chapman to the bag'), findsNothing, reason: 'sold out: nothing to add');
    for (final c in ['All', 'Saved', 'Rice', 'Soups', 'Snacks', 'Drinks']) {
      expect(find.text(c), findsOneWidget, reason: c);
    }
    await press(t, find.text('Drinks'));
    expect(find.bySemanticsLabel(RegExp('^Zobo')), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Egusi')), findsNothing);
    await press(t, find.text('All'));
    await t.enterText(find.byType(EditableText).first, 'egusi');
    await t.pump();
    expect(find.bySemanticsLabel(RegExp('^Egusi')), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Zobo')), findsNothing);
    await t.enterText(find.byType(EditableText).first, 'pizza');
    await t.pump();
    expect(find.text('Nothing matches'), findsOneWidget);
    await drain(t);
  });

  testWidgets('the heart saves; Saved shows them; kept on the phone, by business', (t) async {
    phone(t);
    final api = shopApi();
    final prefs = Prefs(MemoryKeyStore());
    final d = MyChatData(api.auth(), 'c1', follow: false, now: () => at, prefs: prefs);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: MyChatScreen(data: d)));
    await settle(t);
    await press(t, find.bySemanticsLabel('Their shop'));
    await press(t, find.bySemanticsLabel('Save Zobo, 50cl'));
    expect(find.bySemanticsLabel('Saved: Zobo, 50cl. Press to unsave'), findsOneWidget);
    expect(prefs.shopSaved, {'b1': ['i6']});
    expect(await prefs.keys.read('shop-saved'), '{"b1":["i6"]}', reason: 'kept on the phone');
    await press(t, find.text('Saved · 1'));
    expect(find.bySemanticsLabel(RegExp('^Zobo')), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^Egusi')), findsNothing);
    await press(t, find.bySemanticsLabel('Saved: Zobo, 50cl. Press to unsave'));
    expect(find.text('Nothing saved yet'), findsOneWidget);
    await prefs.forgetPerson();
    expect(prefs.shopSaved, isEmpty, reason: 'the person\'s own: forgotten on signing out');
    await drain(t);
  });

  testWidgets('an item: its size chosen, into the bag; looked at lately; Ask about this begins the question in the chat', (t) async {
    phone(t);
    final api = shopApi();
    await openShopScreen(t, api);
    await press(t, find.bySemanticsLabel('Jollof rice & chicken, From ₦3,000'));
    expect(find.text('RICE · MAMA’S KITCHEN'), findsOneWidget);
    expect(find.text('Smoky party jollof with a quarter chicken, plantain and coleslaw.'), findsOneWidget);
    expect(find.text('Small · ₦3,000'), findsOneWidget);
    expect(find.text('Big · ₦6,000'), findsOneWidget, reason: 'sold out, shown quiet');
    expect(find.text('Add to bag · ₦3,000'), findsOneWidget, reason: 'the first size still there');
    await press(t, find.text('Regular · ₦4,500'));
    await press(t, find.bySemanticsLabel('One more Jollof rice & chicken'));
    expect(find.text('Add to bag · ₦9,000'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Add to bag'));
    expect(ShopBag.of('c1').lines, {'i1-r': 2});
    expect(find.text('2 × Jollof rice & chicken (Regular) is in your bag'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await press(t, find.bySemanticsLabel('Back to the shop'));
    expect(find.text('Recently viewed'), findsOneWidget);
    expect(find.bySemanticsLabel('View bag: 2 items · ₦9,000'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Fried rice & turkey, ₦5,200, Only 2 left'));
    await press(t, find.bySemanticsLabel('Ask Mama’s Kitchen about Fried rice & turkey in the chat'));
    // Back in the chat, the question in its box to send.
    expect(find.text('Their shop'), findsNothing);
    expect(find.text('About Fried rice & turkey, ₦5,200: is it still available?'), findsOneWidget);
    expect(api.sent('POST', '/v1/me/chats/c1/messages'), isEmpty, reason: 'the person sends it');
    await drain(t);
  });

  testWidgets('the bag: each with its photo and how many; checked out in the shop\'s sheet', (t) async {
    phone(t);
    final api = shopApi();
    api.routes['POST /v1/me/chats/c1/orders'] = (_) => {
      'order': {'reference': 'MK-1060', 'kind': 'app', 'status': 'sent', 'summary': '2 × Small chops platter', 'total_kobo': 700000, 'due_kobo': 0, 'pay': false, 'created_at': '2026-11-16T10:30:00Z'},
    };
    await openShopScreen(t, api);
    await press(t, find.bySemanticsLabel('Add Small chops platter to the bag'));
    await press(t, find.bySemanticsLabel('One more Small chops platter'));
    await press(t, find.bySemanticsLabel('Add Zobo, 50cl to the bag'));
    await press(t, find.bySemanticsLabel('Your bag, 3 items'));
    expect(find.text('Your bag'), findsOneWidget);
    expect(find.text('₦3,500 each'), findsOneWidget);
    expect(find.text('₦7,000'), findsOneWidget);
    expect(find.text('₦7,800'), findsOneWidget, reason: 'the items together');
    await press(t, find.bySemanticsLabel('One fewer Zobo, 50cl'));
    expect(find.text('Zobo, 50cl'), findsNothing, reason: 'none left: out of the bag');
    await press(t, find.bySemanticsLabel('Check out, ₦7,000'));
    expect(find.text('2 × Small chops platter'), findsOneWidget);
    await press(t, find.textContaining('Place order'));
    expect(api.body(api.sent('POST', '/v1/me/chats/c1/orders').single), {
      'lines': [
        {'item_id': 'i4', 'quantity': 2},
      ],
      'fulfilment': 'pickup',
    });
    await t.pump(const Duration(seconds: 3));
    await t.pumpAndSettle();
    expect(find.text('Your bag'), findsNothing, reason: 'back to the chat');
    expect(ShopBag.of('c1').count, 0);
    await drain(t);
  });

  testWidgets('the shop, an item and the bag look as drawn, light and dark; pressable; large text cuts nothing off', (t) async {
    phone(t, 1000);
    final api = shopApi();
    await openShopScreen(t, api);
    await press(t, find.bySemanticsLabel('Save Egusi soup & pounded yam'));
    await press(t, find.bySemanticsLabel('Add Fried rice & turkey to the bag'));
    await expectPressable(t);
    // Its top in the picture: the search, the categories, the grid.
    await t.drag(find.byType(CustomScrollView), const Offset(0, 2000));
    await t.pumpAndSettle();
    await looks(t, 'shop');
    await press(t, find.bySemanticsLabel('Jollof rice & chicken, From ₦3,000'));
    await expectPressable(t);
    await looks(t, 'shop-item');
    await press(t, find.bySemanticsLabel('Back to the shop'));
    await press(t, find.bySemanticsLabel('View bag: 1 item · ₦5,200'));
    await expectPressable(t);
    await looks(t, 'shop-bag');
    await drain(t);
    final d = MyChatData(api.auth(), 'c1', follow: false, now: () => at);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: ShopScreen(d: d)), drive: settle);
    await expectLargeText(
      t,
      TaskiemApp(mode: TkMode.dark, home: ShopScreen(d: d, item: 'i1')),
      drive: settle,
    );
    await drain(t);
  });
}
