// A film of a price list for resellers (Gadgets, "Sell to dealers"): Ikenna
// prices the Nova X5, 128 GB on the list shared with Kehinde Alabi, with a
// lower price from 10 phones, then sets the lowest price staff cannot go
// below. Adapted from film_gadgets_price_list_test.dart.
// Set-up copied from price_lists_page_test.dart and product_form_test.dart;
// the filming is film.dart's. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_gadgets_price_list_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/gadgets-price-list --out gadgets-price-list.mp4 --captions

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/price_lists_page.dart';
import 'package:taskiem/chat/product_form.dart';
import 'package:taskiem/kit/forms.dart' show TkMoneyField;
import 'package:taskiem/kit/sheet.dart' show TkSheet;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

http.Response _ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

final _pack = {'id': 'pack', 'name': 'Nova X5, 128 GB', 'unit': 'phone', 'available': true, 'stock': 40, 'price_kobo': 18500000, 'category': 'Phones'};

Future<void> _load(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 600));
}

void main() {
  setUpAll(loadFonts);

  Future<void> open(WidgetTester t, Widget Function(ChatsData chats) home) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method != 'GET') return _ok({});
        return switch (req.url.path) {
          '/v1/price-lists' => _ok({
            'lists': [
              {
                'id': 'PL1',
                'name': 'Price list for resellers',
                'links': ['K1'],
                'items': [
                  {'item_id': 'pack', 'price_kobo': 18500000, 'min_quantity': 1, 'available': true},
                ],
              },
            ],
          }),
          '/v1/links' => _ok({
            'links': [
              {'id': 'K1', 'name': 'Kehinde Alabi', 'state': 'active'},
              {'id': 'K2', 'name': 'Okoro & Sons Ltd', 'state': 'active'},
            ],
            'sent': <Object>[],
          }),
          '/v1/stock' => _ok({
            'keeps_stock': true,
            'items': [
              _pack,
              {'id': 'volt', 'name': 'Volt power bank 20,000 mAh', 'unit': 'piece', 'available': true, 'stock': 30, 'price_kobo': 1250000, 'category': 'Chargers'},
            ],
          }),
          '/v1/trade-floors' => _ok({'floors': <String, Object>{}, 'max_discount_pct': null}),
          '/v1/storefront/catalogue' => _ok({'items': <Object>[], 'synced_at': null, 'error': null}),
          _ => null,
        };
      }),
      now: () => f.at,
    );
    final me = Map<String, dynamic>.from(f.json('/v1/me'));
    me['tenant_name'] = 'Techline Gadgets';
    chats.auth.me = Me.fromJson(me);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await _load(t);
  }

  testWidgets('film: a price list', (t) async {
    await open(t, (chats) => PriceListsPage(auth: chats.auth));
    final f = Film.start(t, 'gadgets-price-list');
    f.say('Ikenna shares a price list for resellers with Kehinde Alabi.', who: 'Ikenna · Techline Gadgets');
    await f.hold(3.4);

    f.say('He opens the list to give a lower price for more.');
    await f.hold(1.0);
    await f.tap(find.bySemanticsLabel(RegExp('^Items of Price list')), then: 1.2);
    await _load(t);
    f.say('Under Nova X5, he taps Lower price for more.');
    final lower = find.text('+ Lower price for more');
    await t.ensureVisible(lower);
    await t.pump();
    await f.hold(1.6);
    await f.tap(lower, then: 0.8);
    final inSheet = find.descendant(of: find.byType(TkSheet), matching: find.byType(EditableText));
    f.say('From 10 phones, the price is ₦178,000 each.');
    await f.tap(inSheet.at(2), then: 0.2);
    await f.keyboard(true);
    await f.type(inSheet.at(2), '10');
    await f.tap(inSheet.at(3), then: 0.2);
    await f.type(inSheet.at(3), '178000');
    await f.hold(0.8);
    await f.keyboard(false);
    f.say('He taps Save, and the list now shows the lower price.');
    final save = find.descendant(of: find.byType(TkSheet), matching: find.bySemanticsLabel('Save'));
    await f.hold(0.6);
    await f.tap(save, then: 1.2);
    await _load(t);
    await f.hold(1.0);

    // Next, the product itself: its lowest price.
    await open(t, (chats) => TkSheet(title: 'Product', body: ProductForm(auth: chats.auth, item: _pack, onSaved: (_) {}, onCancel: () {})));
    f.say('On the product, he sets a lowest price: ₦172,500 each.');
    await f.hold(1.4);
    final lowest = find.ancestor(of: find.text('Lowest price'), matching: find.byType(TkMoneyField)).first;
    await t.ensureVisible(lowest);
    await t.pump();
    await f.hold(0.8);
    await f.tap(lowest, then: 0.2);
    await f.keyboard(true);
    await f.type(find.descendant(of: lowest, matching: find.byType(EditableText)), '172500');
    await f.hold(1.2);
    await f.keyboard(false);
    f.say('He taps Save changes to keep the lowest price.');
    await f.hold(2.4);
    // The film ends on the tap: the stand-in server never completes this save
    // (the button would stick on "Saving..."), so nothing after it is filmed.
    await f.tap(find.text('Save changes'));
    await f.finish();
  }, skip: !filming);
}
