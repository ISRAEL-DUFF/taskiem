// ignore_for_file: depend_on_referenced_packages
// A film: put your goods in (Gadgets), for Techline Gadgets. Ikenna opens
// Products from the chat's tools, adds the Nova X5 with a photo, the category
// Phones and the price N185,000, then sets up its sizes and colours: two
// choices, Storage (64, 128, 256 GB) by Colour (Black, Blue, Gold), a count in
// each box of the grid. Adapted from film_food_put_goods_in_test.dart.
// Skipped unless VIDEO_DIR:
//
//   VIDEO_DIR=<out> flutter test test/film_gadgets_put_goods_in_test.dart

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
import 'film_tech_ep23_world.dart';
import 'support.dart';

/// The phone's camera, standing in: it hands back the Nova X5 photo.
class _Camera extends ImagePickerPlatform with MockPlatformInterfaceMixin {
  _Camera(this.bytes);
  final Uint8List bytes;
  @override
  Future<XFile?> getImageFromSource({required ImageSource source, ImagePickerOptions options = const ImagePickerOptions()}) async =>
      XFile.fromData(bytes, name: 'nova-x5.png', mimeType: 'image/png');
}

void main() {
  setUpAll(loadFonts);

  testWidgets('film: put your goods in', (t) async {
    final was = ImagePickerPlatform.instance;
    ImagePickerPlatform.instance = _Camera(await novaPhoto(t));
    addTearDown(() => ImagePickerPlatform.instance = was);

    final f = Fixture('chat-tools');
    // Techline's own products (invented), as the Products list shows them.
    final items = <Map<String, dynamic>>[
      {'id': 'p1', 'name': 'Pulse tablet', 'category': 'Tablets', 'available': true, 'stock': 9, 'unit': 'piece', 'price_kobo': 12000000},
      {'id': 'p2', 'name': 'Volt power bank 20,000 mAh', 'category': 'Power', 'available': true, 'stock': 25, 'unit': 'piece', 'price_kobo': 1250000},
      {'id': 'p3', 'name': 'Charger', 'category': 'Power', 'available': true, 'stock': 40, 'unit': 'piece', 'price_kobo': 650000},
    ];
    Map<String, dynamic> stock() => {'keeps_stock': true, 'catalogue': 'products', 'low': 0, 'published': false, 'stuck': <Object>[], 'items': items};
    final chats = ChatsData(
      f.auth(also: (req) async {
        final base = techBase(req, f);
        if (base != null) return base;
        final p = req.url.path;
        if (req.method == 'GET') {
          if (p == '/v1/stock') return techOk(stock());
          if (p == '/v1/stock/stock-items') return techOk({'items': <Object>[]});
          if (p == '/v1/storefront/catalogue') return techOk({'items': <Object>[], 'synced_at': null, 'error': null});
          if (p == '/v1/price-lists') return techOk({'lists': <Object>[]});
          return null;
        }
        if (p == '/v1/stock/items') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          items.add({'id': 'nova1', 'name': b['name'], 'category': b['category'], 'available': true, 'stock': 0, 'unit': b['unit'], 'price_kobo': b['price_kobo']});
          return techOk({'item': {'id': 'nova1'}});
        }
        if (p == '/v1/stock/items/nova1/variants') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          final it = items.firstWhere((x) => x['id'] == 'nova1');
          it['choices'] = b['choices'];
          return techOk({});
        }
        return techOk({});
      }),
      now: () => f.at,
    );
    techMe(chats.auth, f);
    chats.load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'gadgets-put-goods-in');
    Finder tile(String n) => find.bySemanticsLabel(RegExp('^$n, '));
    Future<void> beat([double s = 0.6]) => film.hold(s);

    film.say('Ikenna opens Taskiem to put the Nova X5 phone on sale.', who: 'Ikenna · Techline Gadgets');
    await film.play(2.6);
    await film.tap(find.bySemanticsLabel('Open tools'), then: 1.0);
    await film.settle();
    film.say('He opens Products, then taps Add product to start.');
    await film.hold(2.0);
    await film.tap(tile('Products'), then: 1.2);
    await film.settle();
    await beat();
    await film.tap(find.bySemanticsLabel('Add product'), then: 1.2);
    await film.settle();

    film.say('He takes a photo of the Nova X5 with the camera.');
    await beat(1.2);
    await film.tap(find.descendant(of: find.byType(ProductAdd), matching: find.byWidgetPredicate((w) => w is TkIcon && w.name == 'camera')), then: 1.0);
    await film.settle();
    expect(find.bySemanticsLabel('Photo 1 to add'), findsOneWidget);
    await beat(1.4);

    film.say('He types the name, the category Phones, then the price.');
    final boxes = find.descendant(of: find.byType(ProductAdd), matching: find.byType(EditableText));
    await film.tap(boxes.first, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(boxes.first, 'Nova X5', perKey: 0.08);
    await film.tap(boxes.at(1), then: 0.2);
    await film.type(boxes.at(1), 'Phones', perKey: 0.08);
    await film.keyboard(false);
    await t.ensureVisible(boxes.at(2));
    await t.pump();
    await beat(0.6);
    final price = boxes.at(2);
    await film.tap(price, then: 0.2);
    await film.keyboard(true);
    await film.type(price, '185000', perKey: 0.12);
    await beat(0.8);
    await film.keyboard(false);
    final addNow = find.bySemanticsLabel('Add product').last;
    await t.ensureVisible(addNow);
    await t.pump();
    film.say('He taps Add product. The Nova X5 is now in his products.');
    await beat(1.4);
    await film.tap(addNow, then: 0.8);
    await film.settle();
    await film.play(2.2);
    await film.settle();

    film.say('To sell it in sizes and colours, he opens the Nova X5.');
    await film.tap(find.bySemanticsLabel('Open tools'), then: 0.8);
    await film.settle();
    await film.tap(tile('Products'), then: 1.0);
    await film.settle();
    await t.ensureVisible(find.text('Nova X5'));
    await t.pump();
    await beat(1.2);
    await film.tap(find.text('Nova X5'), then: 1.0);
    await film.settle();
    await t.ensureVisible(find.bySemanticsLabel('Set up sizes and colours'));
    await t.pump();
    await beat(0.8);
    await film.tap(find.bySemanticsLabel('Set up sizes and colours'), then: 1.0);
    await film.settle();

    film.say('First choice: he names it Storage and types its three sizes.');
    await film.tap(find.bySemanticsLabel(RegExp('^Change Size')), then: 0.8);
    await film.settle();
    // The choice's sheet is on top of the chat: its boxes are the last two.
    final nameBox = find.byType(EditableText).at(find.byType(EditableText).evaluate().length - 2);
    await film.tap(nameBox, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await t.enterText(nameBox, '');
    await film.type(nameBox, 'Storage', perKey: 0.08);
    final valueBox = find.byType(EditableText).last;
    await film.tap(valueBox, then: 0.2);
    await film.type(valueBox, '64 GB, 128 GB, 256 GB', perKey: 0.07);
    await film.keyboard(false);
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('Done'), then: 0.8);
    await film.settle();

    film.say('He taps Add a choice, and types the colours: Black, Blue, Gold.');
    await t.ensureVisible(find.text('+ Add a choice'));
    await t.pump();
    await film.hold(0.8);
    await film.tap(find.text('+ Add a choice'), then: 1.0);
    await film.settle();
    final colourBox = find.byType(EditableText).last;
    await film.tap(colourBox, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(colourBox, 'Black, Blue, Gold', perKey: 0.07);
    await film.keyboard(false);
    await film.hold(0.8);
    await film.tap(find.bySemanticsLabel('Done'), then: 0.8);
    await film.settle();
    await t.ensureVisible(find.bySemanticsLabel('Fill'));
    await t.pump();
    await film.hold(2.0);

    film.say('The grid has a box for every pair. He fills each with 5.');
    final same = find.descendant(of: find.bySemanticsLabel(RegExp('^Same number for all')), matching: find.byType(EditableText));
    await film.tap(same, then: 0.2);
    await film.keyboard(true);
    await film.type(same, '5');
    await film.keyboard(false);
    await film.tap(find.bySemanticsLabel('Fill'), then: 1.6);

    film.say('Then he changes 128 GB, Blue to 12, his best seller.');
    final blue = find.descendant(of: find.bySemanticsLabel('128 GB, Blue: how many'), matching: find.byType(EditableText));
    await t.ensureVisible(blue);
    await t.pump();
    await film.hold(0.6);
    await film.tap(blue, then: 0.2);
    await film.keyboard(true);
    await t.enterText(blue, '');
    await film.type(blue, '12');
    await film.keyboard(false);
    await film.hold(2.0);

    film.say('He taps Save changes. Nine sizes and colours are on sale.');
    await t.ensureVisible(find.bySemanticsLabel('Save changes'));
    await t.pump();
    await film.hold(1.0);
    await film.tap(find.bySemanticsLabel('Save changes'), then: 0.8);
    await film.settle();
    await film.play(2.4);
    await film.finish();
  }, skip: !filming);
}
