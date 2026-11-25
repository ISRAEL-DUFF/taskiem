// A film: reorder from a supplier (Gadgets, Episode 7, "Your suppliers"). A low-stock
// card in Taskiem's chat says Nova X5, 128 GB, Blue, is running low; its Reorder button
// opens an order to Gwarzo Distributors, linked as a supplier, with the item already in
// the bag; Ikenna reviews it and places it from the supplier's price list.
// Set-up copied from taskiem_test.dart (the chat's alert cards) and
// order_sheet_test.dart (OrderSheet), on recorded answers; the names, prices
// and the low item are the story's. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_gadgets_reorder_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/gadgets-reorder --out gadgets-reorder.mp4 --captions

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
  'business': 'Gwarzo Distributors',
  'name': 'For shops',
  'updated_at': '2026-11-01T00:00:00Z',
  'items': [
    {'item_id': 'rice', 'name': 'Nova X5, 128 GB, Blue', 'unit': 'phone', 'category': 'Phones', 'price_kobo': 14000000, 'min_quantity': 1, 'available': true},
    {'item_id': 'beans', 'name': 'Pulse tablet', 'unit': 'tablet', 'category': 'Tablets', 'price_kobo': 9500000, 'min_quantity': 1, 'available': true},
    {'item_id': 'gari', 'name': 'Volt power bank 20,000 mAh', 'unit': 'piece', 'category': 'Power banks', 'price_kobo': 800000, 'min_quantity': 1, 'available': true},
  ],
};

void main() {
  setUpAll(loadFonts);

  testWidgets('film: gadgets reorder', (t) async {
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
                      'title': 'Nova X5, 128 GB, Blue, is running low',
                      'line': '2 left · you reorder at 5',
                      'id': 'si-rice',
                      'reorder': {'link': 'L1', 'price_kobo': 14000000, 'quantity': 20, 'seller_item_id': 'rice', 'supplier': 'Gwarzo Distributors'},
                    },
                ],
              }),
              '/v1/supplier-prices' => ok({'suppliers': [_list]}),
              '/v1/item-links/L1' => ok({
                'item_links': [memory('Nova X5, 128 GB, Blue', 'stock', sellerItem: 'rice', stockItemId: 'si-rice', stockItem: 'Nova X5, 128 GB, Blue')],
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
    chats.auth.me = Me.fromJson({...f.json('/v1/me'), 'tenant_name': 'Techline Gadgets', 'user': {...(f.json('/v1/me')['user'] as Map<String, dynamic>), 'name': 'Ikenna Nwachukwu', 'email': 'ikenna@techline.example'}});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await _settle(t);
    final film = Film.start(t, 'gadgets-reorder');
    film.say('Nova X5, 128 GB, Blue, is low: 2 left. Taskiem tells Ikenna.', who: 'Ikenna · Techline Gadgets');
    await film.play(4.2);
    film.say('Gwarzo Distributors is his phone supplier. Tap Reorder 20.');
    await film.hold(0.6);
    await film.tap(find.bySemanticsLabel(RegExp('^Reorder 20')), then: 1.6);
    await _settle(t);
    film.say('Gwarzo’s price list opens, with 20 phones in the order.');
    await film.hold(4.2);
    film.say('He could add more from the list. Tap Review order.');
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('Review order'), then: 1.4);
    await _settle(t);
    film.say('Twenty phones at ₦140,000 comes to ₦2,800,000. Tap Place order.');
    await film.hold(3.6);
    await film.tap(find.bySemanticsLabel('Place order'), then: 1.0);
    film.say('The order goes to Gwarzo. Taskiem tells Ikenna it is sent.');
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
