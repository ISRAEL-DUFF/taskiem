// ignore_for_file: depend_on_referenced_packages
// A film: put your goods in (episode 2), for Ada's Kitchen. Ada opens Products
// from the chat's tools, adds the Party pack, 10 plates with a photo, the
// category Party packs and the price N48,000, then sets up its sizes: one
// choice, Size: 5 plates, 10 plates, 20 plates, with a count in each box.
// Adapted from film_put_goods_in_test.dart; set-up from tools_panel_test.dart,
// product_form_test.dart and sizes_forms_test.dart. Skipped unless VIDEO_DIR:
//
//   VIDEO_DIR=<out> flutter test test/film_food_put_goods_in_test.dart

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker_platform_interface/image_picker_platform_interface.dart';
import 'package:plugin_platform_interface/plugin_platform_interface.dart' show MockPlatformInterfaceMixin;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/product_form.dart' show ProductAdd;
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/kit/icon.dart' show TkIcon;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_ada_ep23_world.dart';
import 'support.dart';

/// The phone's camera, standing in: it hands back the party pack photo.
class _Camera extends ImagePickerPlatform with MockPlatformInterfaceMixin {
  _Camera(this.bytes);
  final Uint8List bytes;
  @override
  Future<XFile?> getImageFromSource({required ImageSource source, ImagePickerOptions options = const ImagePickerOptions()}) async =>
      XFile.fromData(bytes, name: 'party-pack.png', mimeType: 'image/png');
}

void main() {
  setUpAll(loadFonts);

  testWidgets('film: put your goods in', (t) async {
    final was = ImagePickerPlatform.instance;
    ImagePickerPlatform.instance = _Camera(await partyPackPhoto(t));
    addTearDown(() => ImagePickerPlatform.instance = was);

    final f = Fixture('chat-tools');
    // Ada's Kitchen's own products, as recorded.
    // (a short list of them, so the new product shows in the tools panel).
    const keep = ['Jollof rice', 'Fried rice and turkey', 'Moi moi', 'Chapman'];
    final items = [
      for (final i in f.json('/v1/stock')['items'] as List)
        if (keep.contains((i as Map)['name'])) Map<String, dynamic>.from(i),
    ];
    Map<String, dynamic> stock() => {'keeps_stock': true, 'catalogue': 'products', 'low': 0, 'published': false, 'stuck': <Object>[], 'items': items};
    final chats = ChatsData(
      f.auth(also: (req) async {
        final base = adaBase(req, f);
        if (base != null) return base;
        final p = req.url.path;
        if (req.method == 'GET') {
          if (p == '/v1/stock') return adaOk(stock());
          if (p == '/v1/stock/stock-items') return adaOk({'items': <Object>[]});
          if (p == '/v1/storefront/catalogue') return adaOk({'items': <Object>[], 'synced_at': null, 'error': null});
          if (p == '/v1/price-lists') return adaOk({'lists': <Object>[]});
          return null;
        }
        if (p == '/v1/stock/items') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          items.add({'id': 'pack1', 'name': b['name'], 'category': b['category'], 'available': true, 'stock': 0, 'unit': b['unit'], 'price_kobo': b['price_kobo']});
          return adaOk({'item': {'id': 'pack1'}});
        }
        if (p == '/v1/stock/items/pack1/variants') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          final it = items.firstWhere((x) => x['id'] == 'pack1');
          it['choices'] = b['choices'];
          return adaOk({});
        }
        return adaOk({});
      }),
      now: () => f.at,
    );
    adaMe(chats.auth, f);
    chats.load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'food-put-goods-in');
    Finder tile(String n) => find.bySemanticsLabel(RegExp('^$n, '));
    Future<void> beat([double s = 0.6]) => film.hold(s);

    film.say('Ada opens Taskiem to put her party pack on the menu.', who: "Ada · Ada's Kitchen");
    await film.play(2.4);
    await film.tap(find.bySemanticsLabel('Open tools'), then: 1.0);
    await film.settle();
    film.say('She opens Products, then taps Add product to start.');
    await film.hold(1.6);
    await film.tap(tile('Products'), then: 1.2);
    await film.settle();
    await beat();
    await film.tap(find.bySemanticsLabel('Add product'), then: 1.2);
    await film.settle();

    film.say('She takes a photo of the party pack of jollof rice.');
    await beat(1.0);
    await film.tap(find.descendant(of: find.byType(ProductAdd), matching: find.byWidgetPredicate((w) => w is TkIcon && w.name == 'camera')), then: 1.0);
    await film.settle();
    expect(find.bySemanticsLabel('Photo 1 to add'), findsOneWidget);
    await beat(1.2);

    film.say('She types the name and the category, Party packs, then the price.');
    final boxes = find.descendant(of: find.byType(ProductAdd), matching: find.byType(EditableText));
    await film.tap(boxes.first, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(boxes.first, 'Party pack, 10 plates', perKey: 0.08);
    await film.tap(boxes.at(1), then: 0.2);
    await film.type(boxes.at(1), 'Party packs', perKey: 0.08);
    await film.keyboard(false);
    await t.ensureVisible(boxes.at(2));
    await t.pump();
    await beat(0.4);
    final price = boxes.at(2);
    await film.tap(price, then: 0.2);
    await film.keyboard(true);
    await film.type(price, '48000', perKey: 0.12);
    await beat(0.4);
    await film.keyboard(false);
    final addNow = find.bySemanticsLabel('Add product').last;
    await t.ensureVisible(addNow);
    await t.pump();
    film.say('She taps Add product. The party pack is in her products.');
    await beat(1.0);
    await film.tap(addNow, then: 0.8);
    await film.settle();
    await film.play(2.0);
    await film.settle();

    film.say('To sell it in sizes, she opens the party pack.');
    await film.tap(find.bySemanticsLabel('Open tools'), then: 0.8);
    await film.settle();
    await film.tap(tile('Products'), then: 1.0);
    await film.settle();
    await t.ensureVisible(find.text('Party pack, 10 plates'));
    await t.pump();
    await beat(0.5);
    await film.tap(find.text('Party pack, 10 plates'), then: 1.0);
    await film.settle();
    await t.ensureVisible(find.bySemanticsLabel('Set up sizes and colours'));
    await t.pump();
    await beat(0.6);
    await film.tap(find.bySemanticsLabel('Set up sizes and colours'), then: 1.0);
    await film.settle();

    film.say('Size: she types the values 5 plates, 10 plates, 20 plates.');
    await film.tap(find.bySemanticsLabel(RegExp('^Change Size')), then: 0.8);
    await film.settle();
    final valueBox = find.byType(EditableText).last;
    await film.tap(valueBox, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(valueBox, '5 plates, 10 plates, 20 plates', perKey: 0.07);
    await film.keyboard(false);
    await film.tap(find.bySemanticsLabel('Done'), then: 0.8);
    await film.settle();
    await t.ensureVisible(find.bySemanticsLabel('Fill'));
    await t.pump();
    await beat(0.6);

    film.say('She types 10 for all, taps Fill, then changes 20 plates.');
    final same = find.descendant(of: find.bySemanticsLabel(RegExp('^Same number for all')), matching: find.byType(EditableText));
    await film.tap(same, then: 0.2);
    await film.keyboard(true);
    await film.type(same, '10');
    await film.keyboard(false);
    await film.tap(find.bySemanticsLabel('Fill'), then: 1.2);
    final big = find.descendant(of: find.bySemanticsLabel('20 plates: how many'), matching: find.byType(EditableText));
    await film.tap(big, then: 0.2);
    await film.keyboard(true);
    await t.enterText(big, '');
    await film.type(big, '4');
    await film.keyboard(false);
    await film.hold(1.5);

    film.say('She taps Save changes. Three sizes are on sale.');
    await t.ensureVisible(find.bySemanticsLabel('Save changes'));
    await t.pump();
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('Save changes'), then: 0.8);
    await film.settle();
    await film.play(2.2);
    await film.finish();
  }, skip: !filming);
}
