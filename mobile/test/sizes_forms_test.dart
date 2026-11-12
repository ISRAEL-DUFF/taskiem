// Sizes and colours (M6 part P; web SizesForms.tsx, lib/variants.ts): the
// grid's cells, a proposal for items made one product, and what the
// editor, the grid's stock in and Make one product send.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/sizes_forms.dart';
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  test('cells, keys, values and groups', () {
    final cs = <Choice>[
      (name: 'Size', values: ['S', 'M']),
      (name: 'Colour', values: ['Black', 'Red']),
    ];
    final cells = allCells(cs);
    expect(cells.length, 4);
    expect(cells.first, {'Size': 'S', 'Colour': 'Black'});
    expect(keyOf({'Size': ' S', 'Colour': 'Black'}), 'colour=black|size=s');
    expect(variantLabel({'Colour': 'Red', 'Size': 'M'}, cs), 'M, Red');
    expect(parseValues('40, 41,41\n 42 '), ['40', '41', '42']);
    expect(gridGroups(cells, cs).map((g) => g.label), ['S', 'M']);
  });

  test('items made one product: what Taskiem proposes', () {
    final p = proposeGroup([(id: 'a', name: 'Kaftan – S Black'), (id: 'b', name: 'Kaftan – M Red')]);
    expect(p.name, 'Kaftan');
    expect(p.choices, ['Size', 'Colour']);
    expect(p.values['a'], {'Size': 'S', 'Colour': 'Black'});
  });

  Future<List<String>> open(WidgetTester t, Widget Function(ChatsData chats, List<String?> said) form, {List<Object> stock = const []}) async {
    t.view.physicalSize = const Size(390, 3000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET') {
          return req.url.path == '/v1/stock'
              ? http.Response(jsonEncode({'keeps_stock': true, 'items': stock}), 200, headers: {'content-type': 'application/json'})
              : null;
        }
        sent.add('${req.method} ${req.url.path} ${req.body}');
        return http.Response('{}', 200, headers: {'content-type': 'application/json'});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TkSheet(title: 'Sizes', body: form(chats, []))));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();
    return sent;
  }

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
  }

  testWidgets('set up: a ready-made choice, the same number for all, one box emptied', (t) async {
    final sent = await open(
      t,
      (chats, said) => SizesEditor(
        auth: chats.auth,
        item: const {'id': 'kaftan', 'name': 'Kaftan', 'price_kobo': 2500000, 'stock': 0, 'available': true},
        onSaved: said.add,
        onCancel: () {},
      ),
    );
    // Off to the side in its row of chips: slid to first.
    await t.ensureVisible(find.bySemanticsLabel('Small, medium, large'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Small, medium, large'));
    await t.pump();
    // The boxes in order: its name, its values, the same number for all.
    await t.enterText(find.byType(EditableText).at(2), '3');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Fill'));
    await t.pump();
    await t.enterText(find.descendant(of: find.bySemanticsLabel('Large: how many'), matching: find.byType(EditableText)), '');
    await t.pump();
    expect(find.text('2 sizes and colours on sale'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Save changes'));
    await settle(t);
    expect(sent.single, startsWith('PUT /v1/stock/items/kaftan/variants '));
    final body = jsonDecode(sent.single.substring(sent.single.indexOf('{'))) as Map<String, dynamic>;
    expect(body['choices'], [
      {
        'name': 'Size',
        'values': ['Small', 'Medium', 'Large'],
      },
    ]);
    expect((body['cells'] as List).map((c) => '${(c as Map)['variant']['Size']}=${c['count']}'), ['Small=3', 'Medium=3']);
  });

  testWidgets('stock in by the grid: the low ones marked, only boxes filled sent', (t) async {
    final sent = await open(
      t,
      (chats, said) => GridStockIn(
        auth: chats.auth,
        item: const {
          'id': 'kaftan',
          'name': 'Kaftan',
          'choices': [
            {
              'name': 'Size',
              'values': ['S', 'M'],
            },
          ],
        },
        onSaved: said.add,
      ),
      stock: [
        {'id': 'kS', 'name': 'Kaftan – S', 'variant_of': 'kaftan', 'variant': {'Size': 'S'}, 'available': true, 'stock': 1, 'low': true},
        {'id': 'kM', 'name': 'Kaftan – M', 'variant_of': 'kaftan', 'variant': {'Size': 'M'}, 'available': true, 'stock': 6},
      ],
    );
    expect(find.text('1 now · low'), findsOneWidget);
    await t.enterText(find.descendant(of: find.bySemanticsLabel('S: how many came in (low)'), matching: find.byType(EditableText)), '5');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Record stock in'));
    await settle(t);
    expect(sent.single, 'POST /v1/stock/movements/grid {"reason":"received","lines":[{"item_id":"kS","change":5}]}');
  });

  testWidgets('make one product: the proposal sent as it is, each item its size', (t) async {
    final sent = await open(
      t,
      (chats, said) => GroupForm(
        auth: chats.auth,
        items: const [
          {'id': 'a', 'name': 'Kaftan S'},
          {'id': 'b', 'name': 'Kaftan M'},
        ],
        onSaved: said.add,
        onCancel: () {},
      ),
    );
    await t.tap(find.bySemanticsLabel('Make one product'));
    await settle(t);
    expect(
      sent.single,
      'POST /v1/stock/group {"name":"Kaftan","choices":["Size"],"items":[{"item_id":"a","variant":{"Size":"S"}},{"item_id":"b","variant":{"Size":"M"}}]}',
    );
  });
}
