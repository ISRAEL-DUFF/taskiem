// ignore_for_file: depend_on_referenced_packages
// A film: put your goods in (episode 2). Zainab opens Products from the
// chat's tools, adds an Ankara dress with a photo, then sets up its sizes and
// colours: Size 10, 12, 14, 16 by Colour Green, Gold, Blue, a count in each
// box. Set-up copied from tools_panel_test.dart (the chat and its tools),
// product_form_test.dart and sizes_forms_test.dart. Skipped unless VIDEO_DIR:
//
//   VIDEO_DIR=<out> flutter test test/film_put_goods_in_test.dart
//   python3 video/phone/compose.py --frames <out>/put-goods-in --out p.mp4 --captions

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
import 'dart:convert';
import 'dart:typed_data';

import 'film.dart';
import 'film_zuri_shop_world.dart';
import 'support.dart';

/// The phone's camera, standing in: it hands back the dress photo.
class _Camera extends ImagePickerPlatform with MockPlatformInterfaceMixin {
  _Camera(this.bytes);
  final Uint8List bytes;
  @override
  Future<XFile?> getImageFromSource({required ImageSource source, ImagePickerOptions options = const ImagePickerOptions()}) async =>
      XFile.fromData(bytes, name: 'dress.png', mimeType: 'image/png');
}

void main() {
  setUpAll(loadFonts);

  testWidgets('film: put your goods in', (t) async {
    final was = ImagePickerPlatform.instance;
    ImagePickerPlatform.instance = _Camera(await ankaraDressPhoto(t));
    addTearDown(() => ImagePickerPlatform.instance = was);

    final f = Fixture('chat-tools');
    final items = <Map<String, dynamic>>[
      {'id': 'k1', 'name': 'Ankara skirt', 'category': 'Skirts', 'available': true, 'stock': 6, 'unit': 'piece', 'price_kobo': 1800000},
      {'id': 'k2', 'name': 'Lace blouse', 'category': 'Tops', 'available': true, 'stock': 4, 'unit': 'piece', 'price_kobo': 1500000},
      {'id': 'k3', 'name': 'Wrap dress', 'category': 'Dresses', 'available': true, 'stock': 3, 'unit': 'piece', 'price_kobo': 3200000},
      {'id': 'k4', 'name': 'Aso-oke gown', 'category': 'Dresses', 'available': true, 'stock': 2, 'unit': 'piece', 'price_kobo': 6500000},
    ];
    Map<String, dynamic> stock() => {'keeps_stock': true, 'catalogue': 'products', 'low': 0, 'published': false, 'stuck': <Object>[], 'items': items};
    final chats = ChatsData(
      f.auth(also: (req) async {
        final base = zuriBase(req, f);
        if (base != null) return base;
        final p = req.url.path;
        if (req.method == 'GET') {
          if (p == '/v1/stock') return okJson(stock());
          if (p == '/v1/stock/stock-items') return okJson({'items': <Object>[]});
          if (p == '/v1/storefront/catalogue') return okJson({'items': <Object>[], 'synced_at': null, 'error': null});
          if (p == '/v1/price-lists') return okJson({'lists': <Object>[]});
          return null;
        }
        if (p == '/v1/stock/items') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          items.add({'id': 'dress1', 'name': b['name'], 'category': b['category'], 'available': true, 'stock': 0, 'unit': b['unit'], 'price_kobo': b['price_kobo']});
          return okJson({'item': {'id': 'dress1'}});
        }
        if (p == '/v1/stock/items/dress1/variants') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          final it = items.firstWhere((x) => x['id'] == 'dress1');
          it['choices'] = b['choices'];
          return okJson({});
        }
        return okJson({});
      }),
      now: () => f.at,
    );
    zuriMe(chats.auth, f);
    chats.load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'put-goods-in');
    Finder tile(String n) => find.bySemanticsLabel(RegExp('^$n, '));
    Future<void> beat([double s = 0.6]) => film.hold(s);

    film.say('Zainab opens Taskiem to list the dresses she sells.', who: 'Zainab · Zuri Atelier');
    await film.play(2.2);
    await film.tap(find.bySemanticsLabel('Open tools'), then: 1.0);
    await film.settle();
    film.say('She opens Products, then taps Add product.');
    await beat();
    await film.tap(tile('Products'), then: 1.2);
    await film.settle();
    await beat();
    await film.tap(find.bySemanticsLabel('Add product'), then: 1.2);
    await film.settle();

    film.say('She takes a photo of the Ankara dress.');
    await beat();
    await film.tap(find.descendant(of: find.byType(ProductAdd), matching: find.byWidgetPredicate((w) => w is TkIcon && w.name == 'camera')), then: 1.0);
    await film.settle();
    expect(find.bySemanticsLabel('Photo 1 to add'), findsOneWidget);
    await beat(0.8);

    film.say('She types the name, picks the category Dresses, adds the price.');
    final boxes = find.descendant(of: find.byType(ProductAdd), matching: find.byType(EditableText));
    await film.tap(boxes.first, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(boxes.first, 'Ankara dress', perKey: 0.1);
    await film.keyboard(false);
    await t.ensureVisible(find.textContaining('Dresses ·'));
    await t.pump();
    await beat(0.4);
    await film.tap(find.textContaining('Dresses ·'), then: 0.6);
    final price = boxes.at(2);
    await film.tap(price, then: 0.2);
    await film.keyboard(true);
    await film.type(price, '28000', perKey: 0.12);
    await beat(0.4);
    await film.keyboard(false);
    final addNow = find.bySemanticsLabel('Add product').last;
    await t.ensureVisible(addNow);
    await t.pump();
    film.say('She taps Add product. The dress is in her products.');
    await beat(0.6);
    await film.tap(addNow, then: 0.8);
    await film.settle();
    await film.play(1.8);
    await film.settle();

    film.say('To sell it in sizes and colours, she opens the dress.');
    await film.tap(find.bySemanticsLabel('Open tools'), then: 0.8);
    await film.settle();
    await film.tap(tile('Products'), then: 1.0);
    await film.settle();
    await film.tap(find.text('Ankara dress'), then: 1.0);
    await film.settle();
    await t.ensureVisible(find.bySemanticsLabel('Set up sizes and colours'));
    await t.pump();
    await beat(0.5);
    await film.tap(find.bySemanticsLabel('Set up sizes and colours'), then: 1.0);
    await film.settle();

    film.say('Size: she types the values 10, 12, 14, 16.');
    await film.tap(find.bySemanticsLabel(RegExp('^Change Size')), then: 0.8);
    await film.settle();
    final valueBox = find.byType(EditableText).last;
    await film.tap(valueBox, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(valueBox, '10, 12, 14, 16', perKey: 0.1);
    await film.keyboard(false);
    await film.tap(find.bySemanticsLabel('Done'), then: 0.6);
    await film.settle();

    film.say('Colour: Green, Gold, Blue. The grid appears.');
    await film.tap(find.text('+ Add a choice'), then: 0.8);
    await film.settle();
    final colourBox = find.byType(EditableText).last;
    await film.tap(colourBox, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(colourBox, 'Green, Gold, Blue', perKey: 0.1);
    await film.keyboard(false);
    await film.tap(find.bySemanticsLabel('Done'), then: 0.8);
    await film.settle();
    await t.ensureVisible(find.bySemanticsLabel('Fill'));
    await t.pump();

    film.say('She types 3 for all, taps Fill, then changes Gold size 12.');
    final same = find.descendant(of: find.bySemanticsLabel(RegExp('^Same number for all')), matching: find.byType(EditableText));
    await film.tap(same, then: 0.2);
    await film.keyboard(true);
    await film.type(same, '3');
    await film.keyboard(false);
    await film.tap(find.bySemanticsLabel('Fill'), then: 1.2);
    final gold12 = find.descendant(of: find.bySemanticsLabel('12, Gold: how many'), matching: find.byType(EditableText));
    await film.tap(gold12, then: 0.2);
    await film.keyboard(true);
    await t.enterText(gold12, '');
    await film.type(gold12, '5');
    await film.keyboard(false);
    await film.hold(1.5);

    film.say('Save changes. Twelve sizes and colours are on sale.');
    await t.ensureVisible(find.bySemanticsLabel('Save changes'));
    await t.pump();
    await film.hold(0.5);
    await film.tap(find.bySemanticsLabel('Save changes'), then: 0.8);
    await film.settle();
    await film.play(2.0);
    await film.finish();
  }, skip: !filming);
}
