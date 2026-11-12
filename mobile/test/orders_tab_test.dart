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
    expect(find.text('From your customers'), findsOneWidget);
    expect(find.text('From businesses you supply'), findsOneWidget);
    expect(find.text('To your suppliers'), findsOneWidget);
    expect(find.bySemanticsLabel('Back'), findsNothing, reason: 'a tab of its own');
    expect(find.text('1 waiting for you · 2 open'), findsOneWidget);
    expect(find.textContaining('Bisi'), findsNothing, reason: 'delivered: on its screen, not here');
    expect(t.getTopLeft(find.textContaining('Tunde')).dy, lessThan(t.getTopLeft(find.textContaining('Ngozi')).dy));
    await t.tap(find.textContaining('Rivers Foods'));
    await t.pumpAndSettle();
    expect(find.byType(TradeOrderPage), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp('^Back')).first);
    await t.pumpAndSettle();
    await t.tap(find.text('Bayo Rice Mills · PO-0012'));
    await t.pumpAndSettle();
    expect(find.byType(PurchasePage), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp('^Back')).first);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('See all orders from your customers'));
    await t.pumpAndSettle();
    expect(find.byType(StorefrontOrdersPage), findsOneWidget);
  });

  testWidgets('only what the person may see; no storefront said', (t) async {
    final c = await chats(permissions: const ['table.read'], noShop: true);
    await show(t, c);
    expect(find.text('From your customers'), findsNothing);
    expect(find.text('From businesses you supply'), findsNothing);
    expect(find.text('To your suppliers'), findsOneWidget);
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
  await t.pump(const Duration(milliseconds: 400));
}
