// A film: reorder from a supplier (Food, Episode 7, "Your suppliers"). A low-stock
// card in Taskiem's chat says Rice, 25kg bag, is running low; its Reorder button
// opens an order to Bayo Rice Mills, linked as a supplier, with the item already in
// the bag; Ada reviews it and places it from the supplier's price list.
// Set-up copied from taskiem_test.dart (the chat's alert cards) and
// order_sheet_test.dart (OrderSheet), on recorded answers; the names, prices
// and the low item are the story's. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_food_reorder_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/food-reorder --out food-reorder.mp4 --captions

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
  'business': 'Bayo Rice Mills',
  'name': 'For kitchens',
  'updated_at': '2026-11-01T00:00:00Z',
  'items': [
    {'item_id': 'rice', 'name': 'Rice, 25kg bag', 'unit': 'bag', 'category': 'Grains', 'price_kobo': 3250000, 'min_quantity': 1, 'available': true},
    {'item_id': 'beans', 'name': 'Oloyin beans, 25kg bag', 'unit': 'bag', 'category': 'Grains', 'price_kobo': 3000000, 'min_quantity': 1, 'available': true},
    {'item_id': 'gari', 'name': 'Ijebu gari, 25kg bag', 'unit': 'bag', 'category': 'Flour', 'price_kobo': 2000000, 'min_quantity': 1, 'available': true},
  ],
};

void main() {
  setUpAll(loadFonts);

  testWidgets('film: food reorder', (t) async {
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
                      'key': 'low_stock:rice',
                      'kind': 'low_stock',
                      'at': '2026-11-12T08:30:00Z',
                      'title': 'Rice, 25kg bag, is running low',
                      'line': '4 left · you reorder at 10',
                      'id': 'si-rice',
                      'reorder': {'link': 'L1', 'price_kobo': 3250000, 'quantity': 20, 'seller_item_id': 'rice', 'supplier': 'Bayo Rice Mills'},
                    },
                ],
              }),
              '/v1/supplier-prices' => ok({'suppliers': [_list]}),
              '/v1/item-links/L1' => ok({
                'item_links': [memory('Rice, 25kg bag', 'stock', sellerItem: 'rice', stockItemId: 'si-rice', stockItem: 'Rice, 25kg bag')],
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
    chats.auth.me = Me.fromJson({...f.json('/v1/me'), 'tenant_name': 'Ada’s Kitchen', 'user': {...(f.json('/v1/me')['user'] as Map<String, dynamic>), 'name': 'Ada Obi', 'email': 'ada@adaskitchen.test'}});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await _settle(t);
    final film = Film.start(t, 'food-reorder');
    film.say('Rice, 25kg bag, is low: 4 left. Taskiem tells Ada.', who: 'Ada · Ada’s Kitchen');
    await film.play(4.2);
    film.say('Bayo Rice Mills is her rice supplier. Tap Reorder 20.');
    await film.hold(0.6);
    await film.tap(find.bySemanticsLabel(RegExp('^Reorder 20')), then: 1.6);
    await _settle(t);
    film.say('Bayo’s price list opens, with 20 bags of rice in the order.');
    await film.hold(4.2);
    film.say('She could add more from the list. Tap Review order.');
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('Review order'), then: 1.4);
    await _settle(t);
    film.say('Twenty bags at ₦32,500 comes to ₦650,000. Tap Place order.');
    await film.hold(3.6);
    await film.tap(find.bySemanticsLabel('Place order'), then: 1.0);
    film.say('The order goes to Bayo. Taskiem tells Ada it is sent.');
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
