// A film: selling at the counter (episode 3a). Dami opens Sell from the
// chat's tools, picks the Ankara dress's exact size and colour (12, Gold),
// taps Next, says how it was paid (Transfer) and records the sale. Set-up
// copied from sell_sheet_test.dart (the sheet) and tools_panel_test.dart
// (reaching it from the chat). Skipped unless VIDEO_DIR is set.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_zuri_shop_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: sell at the counter', (t) async {
    final f = Fixture('chat-tools');
    const sizes = ['10', '12', '14', '16'];
    const colours = ['Green', 'Gold', 'Blue'];
    final items = <Map<String, dynamic>>[
      {
        'id': 'dress1',
        'name': 'Ankara dress',
        'category': 'Dresses',
        'available': true,
        'stock': 28,
        'unit': 'piece',
        'price_kobo': 2800000,
        'choices': [
          {'name': 'Size', 'values': sizes},
          {'name': 'Colour', 'values': colours},
        ],
      },
      {'id': 'k1', 'name': 'Ankara skirt', 'category': 'Skirts', 'available': true, 'stock': 6, 'unit': 'piece', 'price_kobo': 1800000},
      {'id': 'k2', 'name': 'Lace blouse', 'category': 'Tops', 'available': true, 'stock': 4, 'unit': 'piece', 'price_kobo': 1500000},
      for (final s in sizes)
        for (final c in colours)
          {
            'id': 'dv-$s-$c',
            'name': 'Ankara dress – $s $c',
            'variant_of': 'dress1',
            'variant': {'Size': s, 'Colour': c},
            'available': true,
            'stock': s == '12' && c == 'Gold' ? 5 : 2,
            'price_kobo': 2800000,
          },
    ];
    final sent = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        final base = zuriBase(req, f, person: 'Dami Ajayi');
        if (base != null) return base;
        if (req.method == 'GET' && req.url.path == '/v1/stock') {
          return okJson({'keeps_stock': true, 'catalogue': 'products', 'low': 0, 'published': false, 'stuck': <Object>[], 'items': items});
        }
        if (req.method != 'GET') {
          sent.add('${req.method} ${req.url.path} ${req.body}');
          return okJson({});
        }
        return null;
      }),
      now: () => f.at,
    );
    zuriMe(chats.auth, f, person: 'Dami Ajayi');
    chats.load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'sell-at-counter');
    Finder tile(String n) => find.bySemanticsLabel(RegExp('^$n, '));

    film.say('A customer buys a dress at the counter.', who: 'Dami · Zuri Atelier');
    await film.play(2.2);
    await film.tap(find.bySemanticsLabel('Open tools'), then: 1.0);
    await film.settle();
    film.say('Dami opens Sell from the tools.');
    await film.hold(0.7);
    await film.tap(tile('Sell'), then: 1.2);
    await film.settle();
    await film.hold(0.8);

    film.say('The Ankara dress comes in sizes and colours. Tap Choose.');
    await film.tap(find.bySemanticsLabel('Choose a size or colour of Ankara dress'), then: 1.0);
    await film.settle();
    await film.hold(0.6);
    film.say('She picks the exact size, 12, and the colour, Gold.');
    await film.tap(find.text('12'), then: 0.9);
    await film.tap(find.text('Gold'), then: 1.0);
    await film.settle();
    await film.hold(0.8);
    film.say('It shows 12, Gold and how many are left. Add one.');
    await film.tap(find.bySemanticsLabel('One more Ankara dress, 12, Gold'), then: 1.6);
    await film.settle();
    await film.hold(1.0);

    film.say('The bar shows one thing and ₦28,000. Tap Next.');
    await film.tap(find.bySemanticsLabel('Next'), then: 1.2);
    await film.settle();
    await film.hold(0.8);
    film.say('Choose how it was paid: Transfer.');
    await film.tap(find.bySemanticsLabel('Transfer'), then: 1.0);
    await film.hold(0.6);
    film.say('Tap Record the sale. It is saved, and stock goes down.');
    await film.tap(find.bySemanticsLabel('Record the sale'), then: 0.8);
    await film.settle();
    await film.play(2.5);
    await film.finish();
    expect(sent.single, 'POST /v1/stock/sales {"lines":[{"item_id":"dv-12-Gold","quantity":1}],"paid_how":"transfer"}');
  }, skip: !filming);
}
