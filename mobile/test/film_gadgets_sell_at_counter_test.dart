// A film: selling at the counter (Gadgets), for Techline Gadgets. Maryam opens
// Sell from the chat's tools, chooses a Nova X5, 128 GB, Blue and adds a
// charger, taps Next, says how it was paid (Transfer) and records the sale.
// Adapted from film_food_sell_at_counter_test.dart and film_sell_at_counter_test.dart. Skipped unless VIDEO_DIR is set.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_tech_ep23_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: sell at the counter', (t) async {
    final f = Fixture('chat-tools');
    const storage = ['64 GB', '128 GB', '256 GB'];
    const colours = ['Black', 'Blue', 'Gold'];
    final items = <Map<String, dynamic>>[
      {
        'id': 'nova1',
        'name': 'Nova X5',
        'category': 'Phones',
        'available': true,
        'stock': 45,
        'unit': 'piece',
        'price_kobo': 18500000,
        'choices': [
          {'name': 'Storage', 'values': storage},
          {'name': 'Colour', 'values': colours},
        ],
      },
      {'id': 'c1', 'name': 'Charger', 'category': 'Power', 'available': true, 'stock': 40, 'unit': 'piece', 'price_kobo': 650000},
      {'id': 'p1', 'name': 'Pulse tablet', 'category': 'Tablets', 'available': true, 'stock': 9, 'unit': 'piece', 'price_kobo': 12000000},
      {'id': 'p2', 'name': 'Volt power bank 20,000 mAh', 'category': 'Power', 'available': true, 'stock': 25, 'unit': 'piece', 'price_kobo': 1250000},
      for (final s in storage)
        for (final c in colours)
          {
            'id': 'nv-$s-$c',
            'name': 'Nova X5 – $s, $c',
            'variant_of': 'nova1',
            'variant': {'Storage': s, 'Colour': c},
            'available': true,
            'stock': s == '128 GB' && c == 'Blue' ? 12 : 5,
            'price_kobo': 18500000,
          },
    ];
    final sent = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        final base = techBase(req, f, person: 'Maryam Sani');
        if (base != null) return base;
        if (req.method == 'GET' && req.url.path == '/v1/stock') {
          return techOk({'keeps_stock': true, 'catalogue': 'products', 'low': 0, 'published': false, 'stuck': <Object>[], 'items': items});
        }
        if (req.method != 'GET') {
          sent.add('${req.method} ${req.url.path} ${req.body}');
          return techOk({});
        }
        return null;
      }),
      now: () => f.at,
    );
    techMe(chats.auth, f, person: 'Maryam Sani');
    chats.load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'gadgets-sell-at-counter');
    Finder tile(String n) => find.bySemanticsLabel(RegExp('^$n, '));

    film.say('A customer buys a phone and a charger. Maryam takes it.', who: 'Maryam · Techline Gadgets');
    await film.play(2.6);
    await film.tap(find.bySemanticsLabel('Open tools'), then: 1.0);
    await film.settle();
    film.say('Maryam opens Sell from the tools to ring up the order.');
    await film.hold(1.6);
    await film.tap(tile('Sell'), then: 1.2);
    await film.settle();
    await film.hold(1.2);

    film.say('The Nova X5 comes in storage and colours. Tap Choose.');
    await film.tap(find.bySemanticsLabel('Choose a size or colour of Nova X5'), then: 1.0);
    await film.settle();
    await film.hold(1.2);
    film.say('She picks the storage, 128 GB, and the colour, Blue.');
    await film.tap(find.text('128 GB').first, then: 0.9);
    await film.tap(find.text('Blue').first, then: 1.0);
    await film.settle();
    await film.hold(1.0);
    film.say('It shows 128 GB, Blue and how many are left. Add one.');
    await film.tap(find.bySemanticsLabel('One more Nova X5, 128 GB, Blue'), then: 1.8);
    await film.settle();
    await film.hold(1.0);
    await film.tap(find.bySemanticsLabel('Done a size or colour of Nova X5'), then: 0.8);
    await film.settle();

    film.say('One charger at ₦6,500: tap One more once.');
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('One more Charger'), then: 2.0);
    await film.settle();
    await film.hold(0.6);

    film.say('The bar shows two things and ₦191,500. Tap Next.');
    await film.hold(1.4);
    await film.tap(find.bySemanticsLabel('Next'), then: 1.2);
    await film.settle();
    await film.hold(0.8);
    film.say('Maryam chooses how it was paid: Transfer, as the customer did.');
    await film.hold(1.2);
    await film.tap(find.bySemanticsLabel('Transfer'), then: 1.4);
    await film.hold(0.6);
    film.say('Tap Record the sale. It is saved, and stock goes down.');
    await film.hold(1.2);
    await film.tap(find.bySemanticsLabel('Record the sale'), then: 0.8);
    await film.settle();
    await film.play(2.8);
    await film.finish();
    expect(sent.single, 'POST /v1/stock/sales {"lines":[{"item_id":"c1","quantity":1},{"item_id":"nv-128 GB-Blue","quantity":1}],"paid_how":"transfer"}');
  }, skip: !filming);
}
