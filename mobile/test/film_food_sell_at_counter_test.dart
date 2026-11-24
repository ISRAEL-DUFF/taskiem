// A film: selling at the counter (episode 3a), for Ada's Kitchen. Tunde opens
// Sell from the chat's tools, adds two jollof rice and a zobo, taps Next, says
// how it was paid (Transfer) and records the sale. Adapted from
// film_sell_at_counter_test.dart; set-up from sell_sheet_test.dart and
// tools_panel_test.dart. Skipped unless VIDEO_DIR is set.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_ada_ep23_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: sell at the counter', (t) async {
    final f = Fixture('chat-tools');
    final items = <Map<String, dynamic>>[
      {'id': '1', 'name': 'Jollof rice', 'available': true, 'stock': 34, 'unit': 'plate', 'price_kobo': 350000},
      {'id': '27', 'name': 'Zobo', 'available': true, 'stock': 30, 'unit': 'bottle', 'price_kobo': 150000},
      {'id': '13', 'name': 'Moi moi', 'available': true, 'stock': 40, 'unit': 'portion', 'price_kobo': 100000},
      {'id': '14', 'name': 'Fried plantain (dodo)', 'available': true, 'stock': 45, 'unit': 'portion', 'price_kobo': 100000},
    ];
    final sent = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        final base = adaBase(req, f, person: 'Tunde');
        if (base != null) return base;
        if (req.method == 'GET' && req.url.path == '/v1/stock') {
          return adaOk({'keeps_stock': true, 'catalogue': 'products', 'low': 0, 'published': false, 'stuck': <Object>[], 'items': items});
        }
        if (req.method != 'GET') {
          sent.add('${req.method} ${req.url.path} ${req.body}');
          return adaOk({});
        }
        return null;
      }),
      now: () => f.at,
    );
    adaMe(chats.auth, f, person: 'Tunde');
    chats.load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'food-sell-at-counter');
    Finder tile(String n) => find.bySemanticsLabel(RegExp('^$n, '));

    film.say('A customer at the counter orders lunch. Tunde takes it.', who: "Tunde · Ada's Kitchen");
    await film.play(2.4);
    await film.tap(find.bySemanticsLabel('Open tools'), then: 1.0);
    await film.settle();
    film.say('Tunde opens Sell from the tools to ring up the order.');
    await film.hold(1.0);
    await film.tap(tile('Sell'), then: 1.2);
    await film.settle();
    await film.hold(1.2);

    film.say('Two jollof rice at ₦3,500 each: tap One more twice.');
    await film.tap(find.bySemanticsLabel('One more Jollof rice'), then: 0.8);
    await film.tap(find.bySemanticsLabel('One more Jollof rice'), then: 1.4);
    film.say('One zobo at ₦1,500: tap One more once.');
    await film.hold(0.6);
    await film.tap(find.bySemanticsLabel('One more Zobo'), then: 1.6);
    await film.settle();
    await film.hold(1.0);

    film.say('The bar shows three things and ₦8,500. Tap Next.');
    await film.hold(1.0);
    await film.tap(find.bySemanticsLabel('Next'), then: 1.2);
    await film.settle();
    await film.hold(0.8);
    film.say('Tunde chooses how it was paid: Transfer, as the customer did.');
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('Transfer'), then: 1.4);
    await film.hold(0.6);
    film.say('Tap Record the sale. It is saved, and stock goes down.');
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('Record the sale'), then: 0.8);
    await film.settle();
    await film.play(2.8);
    await film.finish();
    expect(sent.single, 'POST /v1/stock/sales {"lines":[{"item_id":"1","quantity":2},{"item_id":"27","quantity":1}],"paid_how":"transfer"}');
  }, skip: !filming);
}
