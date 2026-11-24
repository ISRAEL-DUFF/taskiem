// A film: reorder from a supplier (Episode 7, "Your suppliers"). A low-stock
// card in Taskiem's chat says Ankara fabric is running low; its Reorder button
// opens an order to Alhaja Sade, linked as a supplier, with the item already in
// the bag; Zainab reviews it and places it from the supplier's price list.
// Set-up copied from taskiem_test.dart (the chat's alert cards) and
// order_sheet_test.dart (OrderSheet), on recorded answers; the names, prices
// and the low item are the story's. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_reorder_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/reorder --out reorder.mp4 --captions

import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';
import 'treat_fixture.dart';

const _list = {
  'link': 'L1',
  'business': 'Alhaja Sade Fabrics',
  'name': 'For tailors',
  'updated_at': '2026-11-01T00:00:00Z',
  'items': [
    {
      'item_id': 'ankara',
      'name': 'Ankara print',
      'unit': 'yard',
      'category': 'Fabric',
      'price_kobo': 350000,
      'min_quantity': 10,
      'available': true,
      'breaks': [
        {'min_quantity': 50, 'price_kobo': 320000},
      ],
    },
    {'item_id': 'lace', 'name': 'French lace', 'unit': 'yard', 'category': 'Fabric', 'price_kobo': 650000, 'min_quantity': 5, 'available': true},
    {'item_id': 'lining', 'name': 'Cotton lining', 'unit': 'yard', 'category': 'Fabric', 'price_kobo': 150000, 'min_quantity': 10, 'available': true},
    {'item_id': 'zips', 'name': 'Zips, 20cm', 'unit': 'pack', 'category': 'Notions', 'price_kobo': 80000, 'min_quantity': 1, 'available': true},
  ],
};

void main() {
  setUpAll(loadFonts);

  testWidgets('film: reorder', (t) async {
    final f = Fixture('chat-taskiem');
    var sent = false;
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          final treat = treatAnswers(req);
          if (treat != null) return treat;
          if (req.method == 'GET') {
            return switch (p) {
              '/v1/taskiem-chat' => ok({'messages': <Object>[]}),
              '/v1/chat-briefing' => ok({'ready': false}),
              '/v1/chat-alerts' => ok({
                'alerts': [
                  if (!sent)
                    {
                      'key': 'low_stock:ankara',
                      'kind': 'low_stock',
                      'at': '2026-11-12T08:30:00Z',
                      'title': 'Ankara fabric is running low',
                      'line': '4 yards left · you reorder at 10',
                      'id': 'si-ank',
                      'reorder': {'link': 'L1', 'price_kobo': 350000, 'quantity': 20, 'seller_item_id': 'ankara', 'supplier': 'Alhaja Sade Fabrics'},
                    },
                ],
              }),
              '/v1/supplier-prices' => ok({'suppliers': [_list]}),
              '/v1/item-links/L1' => ok({
                'item_links': [memory('Ankara print', 'stock', sellerItem: 'ankara', stockItemId: 'si-ank', stockItem: 'Ankara fabric')],
              }),
              '/v1/stock/suppliers' => ok({'suppliers': <Object>[]}),
              '/v1/stock/purchases' => ok({'purchases': <Object>[]}),
              _ => null,
            };
          }
          if (p == '/v1/stock/suppliers') return ok({'supplier': {'id': 'S1'}});
          if (p == '/v1/supplier-prices/L1/order') return ok({'purchase': {'id': 'P1', 'reference': 'PO-1'}});
          if (p == '/v1/stock/purchases/P1/send') {
            sent = true;
            return ok({});
          }
          return null;
        },
      ),
      now: () => f.at,
    )..load();
    chats.auth.me = Me.fromJson({...f.json('/v1/me'), 'tenant_name': 'Zuri Atelier', 'user': {...(f.json('/v1/me')['user'] as Map<String, dynamic>), 'name': 'Zainab Yusuf', 'email': 'zainab@zuri.test'}});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await _settle(t);
    final film = Film.start(t, 'reorder');
    film.say('Ankara fabric is low at Zuri Atelier. Taskiem tells Zainab.', who: 'Zainab · Zuri Atelier');
    await film.play(4.2);
    film.say('Alhaja Sade is her fabric supplier. Tap Reorder 20.');
    await film.hold(0.6);
    await film.tap(find.bySemanticsLabel(RegExp('^Reorder 20')), then: 1.6);
    await _settle(t);
    film.say('Her price list opens, with 20 yards of Ankara in the order.');
    await film.hold(4.2);
    film.say('She could add more from the list. Tap Review order.');
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('Review order'), then: 1.4);
    await _settle(t);
    film.say('Twenty yards at ₦3,500 comes to ₦70,000. Tap Place order.');
    await film.hold(3.6);
    await film.tap(find.bySemanticsLabel('Place order'), then: 1.0);
    film.say('The order goes to Alhaja Sade. Taskiem tells Zainab it is sent.');
    await film.hold(2.8);
    await film.finish();
    await t.pump(const Duration(seconds: 6));
  }, skip: !filming);
}

Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
