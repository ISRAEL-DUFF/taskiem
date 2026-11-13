// A product's form and Add product (M6 part P; web ChatToolForms.tsx
// ProductForm, ProductAdd): each change sends what it says, and only what
// changed.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/product_form.dart';
import 'package:taskiem/api/transport.dart' show ApiError, Offline;
import 'package:taskiem/chat/attachments.dart' show PickedFile;
import 'package:taskiem/kit/forms.dart';
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  test('a save’s key: the same for a retry, new once what is sent changes', () {
    final k = SaveKey();
    final a = k.of({'name': 'Garri'});
    expect(k.of({'name': 'Garri'}), a);
    final b = k.of({'name': 'Garri, Ijebu'});
    expect(b, isNot(a));
    expect(k.of({'name': 'Garri, Ijebu'}), b);
  });

  test('a photo is checked before it goes; what went wrong in words', () {
    PickedFile f(String type, int size) => PickedFile(bytes: Uint8List(size), name: 'x', type: type);
    expect(sendProblem(f('image/jpeg', 1000)), isNull);
    expect(sendProblem(f('image/heic', 1000)), 'Choose a JPEG, PNG or WebP photo.');
    expect(sendProblem(f('image/png', (10 << 20) + 1)), 'That photo is larger than 10 MB, even made smaller. Choose a smaller one.');
    expect(errorWords(ApiError(422, {'error': 'Name is taken'})), 'Name is taken');
    expect(errorWords(const Offline()), 'You’re offline. Try again when you’re back online.');
    expect(errorWords(FormProblem('Say a price.')), 'Say a price.');
    expect(errorWords(StateError('x')), 'That did not go. Try again.');
  });

  test('naira typed, and back', () {
    expect(nairaKobo('78,000'), 7800000);
    expect(nairaKobo('₦1,250.5'), 125050);
    expect(nairaKobo('78k'), isNull);
    expect(nairaText(7800000), '78,000');
    expect(nairaText(125050), '1,250.50');
    expect(listLabel('Trade'), 'Trade price list');
    expect(listLabel('Trade prices'), 'Trade prices');
    expect(productLine('Garri', 3200000, 'bag'), 'New product: Garri, ₦32,000 a bag');
  });

  Future<({List<String> sent, List<String?> said})> open(WidgetTester t, Widget Function(ChatsData chats, List<String?> said) form) async {
    t.view.physicalSize = const Size(390, 2400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    final said = <String?>[];
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET') {
          return switch (p) {
            '/v1/price-lists' => ok({
              'lists': [
                {
                  'id': 'PL1',
                  'name': 'Trade',
                  'links': ['K1'],
                  'items': [
                    {'item_id': 'rice', 'price_kobo': 4000000, 'min_quantity': 1, 'available': true},
                  ],
                },
              ],
            }),
            '/v1/links' => ok({
              'links': [
                {'id': 'K1', 'name': 'Rivers Foods', 'state': 'active'},
                {'id': 'K2', 'name': 'Eko Events', 'state': 'active'},
              ],
              'sent': <Object>[],
            }),
            '/v1/trade-floors' => ok({'floors': <String, Object>{}}),
            '/v1/stock' => ok({
              'keeps_stock': true,
              'items': [
                {'id': 'rice', 'name': 'Rice', 'category': 'Grains', 'available': true, 'stock': 4},
                {'id': 'beans', 'name': 'Beans', 'category': 'Grains', 'available': true, 'stock': 2},
              ],
            }),
            '/v1/storefront/catalogue' => ok({'items': <Object>[], 'synced_at': null, 'error': null}),
            _ => null,
          };
        }
        sent.add('${req.method} $p ${req.body}');
        if (p == '/v1/stock/items') return ok({'item': {'id': 'new1'}});
        return ok({});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TkSheet(title: 'Product', body: form(chats, said))));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pumpAndSettle();
    return (sent: sent, said: said);
  }

  final rice = {'id': 'rice', 'name': 'Rice', 'price_kobo': 7800000, 'available': true, 'stock': 4, 'category': 'Grains', 'unit': 'bag'};

  testWidgets('a product: price, count and its trade price; only what changed is sent', (t) async {
    final r = await open(t, (chats, said) => ProductForm(auth: chats.auth, item: rice, onSaved: said.add, onCancel: () {}));
    expect(find.text('Trade price list'), findsOneWidget);
    expect(find.text('Rivers Foods'), findsOneWidget, reason: 'who sees the list');
    // Nothing changed: Save tells nothing.
    await t.tap(find.bySemanticsLabel('Save changes'));
    await t.pumpAndSettle();
    expect(r.sent, isEmpty);
    expect(r.said.single, isNull);
  });

  testWidgets('a product: each change sent', (t) async {
    final r = await open(t, (chats, said) => ProductForm(auth: chats.auth, item: rice, onSaved: said.add, onCancel: () {}));
    await t.enterText(find.bySemanticsLabel(RegExp('^Storefront price in naira')), '80,000');
    await t.enterText(find.bySemanticsLabel(RegExp('^Trade price list in naira')), '41,000');
    await t.tap(find.bySemanticsLabel('One more'));
    await t.pump();
    await t.tap(find.byType(TkSwitch));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save changes'));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    expect(r.sent, [
      'PATCH /v1/stock/items/rice {"price_kobo":8000000,"available":false}',
      'PUT /v1/price-lists/PL1/items {"items":[{"item_id":"rice","price_kobo":4100000,"min_quantity":1,"available":true,"breaks":[]}]}',
      'POST /v1/stock/movements {"item_id":"rice","reason":"count","count":5,"note":"Counted in chat"}',
    ]);
    expect(r.said.single, 'Rice: saved');
  });

  testWidgets('add product: its category from the ones used; the line it posts', (t) async {
    final r = await open(t, (chats, said) => ProductAdd(auth: chats.auth, onSaved: said.add));
    expect(find.bySemanticsLabel('Add product'), findsOneWidget);
    await t.enterText(find.byType(EditableText).first, 'Garri');
    await t.tap(find.bySemanticsLabel('Grains · 2'));
    await t.pump();
    await t.enterText(find.bySemanticsLabel(RegExp('^Price in naira')), '32,000');
    await t.enterText(find.byType(EditableText).at(3), 'bag');
    await t.tap(find.bySemanticsLabel('One more'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Add product'));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    expect(r.sent.single, 'POST /v1/stock/items {"name":"Garri","price_kobo":3200000,"unit":"bag","quantity":1,"category":"Grains"}');
    expect(r.said.single, 'New product: Garri, ₦32,000 a bag');
  });

  testWidgets('someone who may only look: no Save, said why', (t) async {
    await open(t, (chats, said) => ProductForm(auth: chats.auth, item: rice, onSaved: said.add, canEdit: false));
    expect(find.text('You can see this. Changing it needs someone who may edit it.'), findsOneWidget);
    expect(find.bySemanticsLabel('Save'), findsNothing);
    expect(find.byType(TkStepper), findsOneWidget);
  });
}
