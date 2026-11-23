// The home's Orders tab: every order in one place, from customers, from
// linked businesses and to suppliers; those waiting first; each leading to
// its screen and to its full list.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/orders_tab.dart';
import 'package:taskiem/chat/purchases_page.dart' show PurchasePage;
import 'package:taskiem/chat/storefront_orders_page.dart' show StorefrontOrdersPage;
import 'package:taskiem/chat/trade_orders_page.dart' show TradeOrderPage;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'guidelines.dart';
import 'support.dart';

final _customers = [
  {
    'reference': 'AK-1',
    'kind': 'order',
    'status': 'paid',
    'customer': {'name': 'Ngozi'},
    'total_kobo': 500000,
    'created_at': '2026-11-12T09:00:00Z',
    'lines': [],
  },
  {
    'reference': 'AK-2',
    'kind': 'order',
    'status': 'received',
    'customer': {'name': 'Tunde'},
    'total_kobo': 300000,
    'created_at': '2026-11-12T10:00:00Z',
    'lines': [],
  },
  {
    'reference': 'AK-3',
    'kind': 'order',
    'status': 'delivered',
    'customer': {'name': 'Bisi'},
    'total_kobo': 100000,
    'created_at': '2026-11-11T10:00:00Z',
    'lines': [],
  },
];

final _trade = [
  {'id': 'T1', 'reference': 'PO-9', 'buyer_name': 'Rivers Foods', 'status': 'received', 'total_kobo': 2000000, 'lines': []},
];

final _purchases = [
  {
    'id': 'P1',
    'reference': 'PO-0012',
    'supplier': 'Bayo Rice Mills',
    'status': 'sent',
    'seller_status': 'delivered',
    'link_id': 'L1',
    'total_kobo': 15600000,
    'lines': [],
  },
];

void main() {
  setUpAll(loadFonts);

  test('what waits for the business comes first; done ones are left to their screens', () {
    final list = firstWaiting(_customers, customerWaiting);
    expect(list.map((o) => o['reference']), ['AK-2', 'AK-1']);
    expect(tradeWaiting(_trade.single), isTrue);
    expect(tradeWaiting({'status': 'accepted'}), isFalse);
    expect(tradeWaiting({'status': 'delivered', 'short_at': '2026-11-12T09:00:00Z'}), isTrue, reason: 'their count to agree');
    expect(purchaseWaiting(_purchases.single), isTrue, reason: 'say what came');
    expect(purchaseWaiting({'status': 'drafted'}), isTrue);
    expect(purchaseWaiting({'status': 'sent', 'seller_status': 'accepted'}), isFalse);
  });

  test('where an order stands, and how long it has waited', () {
    expect(sellStage({'status': 'received'}, trade: false), 'new');
    expect(sellStage({'status': 'paid'}, trade: false), 'ready');
    expect(sellStage({'status': 'on_the_way'}, trade: false), 'way');
    expect(sellStage({'status': 'completed'}, trade: false), isNull);
    expect(sellStage({'status': 'ready'}, trade: true), 'ready');
    expect(sellStage({'status': 'dispatched'}, trade: true), 'way');
    expect(sellStage({'status': 'delivered', 'short_at': '2026-11-12T09:00:00Z'}, trade: true), 'new', reason: 'their count to agree');
    expect(buyStage({'status': 'drafted'}), 'answer');
    expect(buyStage({'status': 'sent'}), 'sent');
    expect(buyStage({'status': 'confirmed', 'seller_status': 'dispatched'}), 'coming');
    expect(buyStage({'status': 'received'}), isNull);
    final now = DateTime.utc(2026, 11, 12, 12);
    expect(waitedWords('2026-11-12T11:59:40Z', now), 'a moment');
    expect(waitedWords('2026-11-12T11:20:00Z', now), '40 min');
    expect(waitedWords('2026-11-12T11:00:00Z', now), '1 hour');
    expect(waitedWords('2026-11-10T11:00:00Z', now), '2 days');
    expect(busiestHour([
      {'created_at': '2026-11-12T09:10:00Z'},
      {'created_at': '2026-11-12T11:00:00Z'},
      {'created_at': '2026-11-12T11:40:00Z'},
      {'created_at': '2026-11-11T11:40:00Z'},
    ], now), (hour: 12, count: 2), reason: 'Lagos hours; yesterday left out');
    expect(busiestHour([{'created_at': '2026-11-11T11:40:00Z'}], now), isNull);
  });

  Future<ChatsData> chats({List<String> permissions = const [], bool noShop = false}) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final c = ChatsData(
      f.auth(
        also: (req) async => switch (req.url.path) {
          '/v1/storefront/orders' when noShop => http.Response(jsonEncode({'error': 'No storefront.'}), 404, headers: {'content-type': 'application/json'}),
          '/v1/storefront/orders' => ok({'orders': _customers}),
          '/v1/trade-orders' => ok({'orders': _trade}),
          '/v1/stock/purchases' => ok({'purchases': _purchases}),
          _ => null,
        },
      ),
      now: () => f.at,
    );
    if (permissions.isNotEmpty) c.auth.me = Me.fromJson({...f.json('/v1/me'), 'permissions': permissions});
    return c;
  }

  Future<void> show(WidgetTester t, ChatsData c) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 2400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: OrdersTab(auth: c.auth, now: () => DateTime.utc(2026, 11, 12, 12)),
      ),
    );
    await settle(t);
  }

  testWidgets('three kinds, waiting first; each row and See all lead to their screens', (t) async {
    final c = await chats();
    await show(t, c);
    await looks(t, 'orders');
    expect(find.text('From customers'), findsOneWidget);
    expect(find.text('From businesses'), findsOneWidget);
    expect(find.text('To suppliers'), findsOneWidget);
    expect(find.bySemanticsLabel('Back'), findsNothing, reason: 'a tab of its own');
    expect(find.bySemanticsLabel('3 waiting for you, 4 open'), findsOneWidget);
    expect(find.text('4 orders open'), findsOneWidget);
    expect(find.text('₦3,000'), findsOneWidget, reason: 'the amount at the row’s end');
    expect(find.textContaining('Bisi'), findsNothing, reason: 'delivered: on its screen, not here');
    expect(t.getTopLeft(find.textContaining('Tunde').last).dy, lessThan(t.getTopLeft(find.textContaining('Ngozi')).dy));
    expect(find.bySemanticsLabel('New: 2, Getting ready: 1, On the way: 0'), findsOneWidget, reason: 'Tunde and Rivers Foods new, Ngozi paid');
    expect(find.text('Tunde has waited 2 hours'), findsOneWidget, reason: 'the oldest of those waiting with a time');
    expect(find.bySemanticsLabel('Busiest today: 10 am, 1 order'), findsOneWidget, reason: 'Ngozi at 10 am and Tunde at 11, Lagos: the earlier on a tie');
    await t.tap(find.textContaining('Rivers Foods'));
    await t.pumpAndSettle();
    expect(find.byType(TradeOrderPage), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp('^Back')).first);
    await t.pumpAndSettle();
    await t.tap(find.text('Bayo Rice Mills'));
    await t.pumpAndSettle();
    expect(find.byType(PurchasePage), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp('^Back')).first);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('See all orders from your customers'));
    await t.pumpAndSettle();
    expect(find.byType(StorefrontOrdersPage), findsOneWidget);
  });

  testWidgets('one kind chosen: its section alone, and its own flow', (t) async {
    await show(t, await chats());
    expect(find.bySemanticsLabel('All · 4'), findsOneWidget, reason: 'the delivered one is left to its screen');
    await t.ensureVisible(find.bySemanticsLabel('Suppliers · 1'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Suppliers · 1'));
    await settle(t);
    expect(find.text('To suppliers'), findsOneWidget);
    expect(find.text('From customers'), findsNothing);
    expect(find.text('From businesses'), findsNothing);
    expect(find.bySemanticsLabel('To answer: 1, Sent: 0, Coming: 0'), findsOneWidget);
    expect(find.textContaining('has waited'), findsNothing);
    await t.ensureVisible(find.bySemanticsLabel('All · 4'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('All · 4'));
    await settle(t);
    expect(find.text('From customers'), findsOneWidget);
  });

  testWidgets('only what the person may see; no storefront said', (t) async {
    final c = await chats(permissions: const ['table.read'], noShop: true);
    await show(t, c);
    expect(find.text('From customers'), findsNothing);
    expect(find.text('From businesses'), findsNothing);
    expect(find.text('To suppliers'), findsOneWidget);
    final c2 = await chats(noShop: true);
    await show(t, c2);
    expect(find.textContaining('No storefront yet'), findsOneWidget);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final c = await chats();
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: OrdersTab(auth: c.auth, now: () => DateTime.utc(2026, 11, 12, 12)),
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
  await t.pump(const Duration(milliseconds: 800));
}
