// Your business in Explore (M5 part B; web/src/pages/BusinessProfile.tsx):
// the card as others see it follows the words typed; Save sends them;
// being found switches; a photo is added, moved and removed.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/attachments.dart' show PickedFile;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/composer.dart';
import 'package:taskiem/chat/profile_screen.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

class _Picks extends Pickers {
  const _Picks();
  @override
  Future<PickedFile?> photo() async => PickedFile(bytes: Uint8List.fromList(const [1, 2, 3]), name: 'a.jpg', type: 'image/jpeg');
}

Map<String, dynamic> _photo(String id) => {
  'id': id,
  'caption': 'Caption $id',
  'photo': {'id': 'p$id', 'width': 10, 'height': 10, 'urls': <String, String>{}, 'moderation': 'approved'},
};

void main() {
  setUpAll(loadFonts);

  test('the delivery areas typed as a line, each once', () {
    expect(splitAreas('Lekki, Ajah; lekki\nVI , '), ['Lekki', 'Ajah', 'VI']);
    expect(profileLetter('  kola'), 'K');
    expect(profileLetter(''), '?');
    expect(photoPath({'urls': {'800': 'a/b c'}}), '/v1/business-profile/image?src=a%2Fb+c');
  });

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  testWidgets('the card follows the words; Save, found, and the photos send what they say', (t) async {
    t.view.physicalSize = const Size(390, 4000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    var profile = <String, dynamic>{
      'name': 'Mama Nkechi Kitchen',
      'tagline': 'Soups and swallows',
      'area': 'Yaba',
      'about': '',
      'logo': null,
      'gallery': [_photo('a'), _photo('b')],
      'max_gallery': 12,
      'delivers': false,
      'vehicle': '',
      'delivery_areas': <String>[],
    };
    var found = false;
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/business-profile') return ok(profile);
        if (req.method == 'GET' && p == '/v1/link-settings') return ok({'findable': found, 'findable_now': found});
        if (req.method == 'GET') return null;
        sent.add('${req.method} $p ${req.headers['content-type']?.startsWith('image') == true ? '<photo>' : req.body}');
        if (p == '/v1/link-settings') found = (jsonDecode(req.body) as Map)['findable'] == true;
        if (p == '/v1/business-profile') profile = {...profile, ...(jsonDecode(req.body) as Map<String, dynamic>)};
        if (p == '/v1/business-profile/gallery') profile = {...profile, 'gallery': [...profile['gallery'] as List, _photo('c')]};
        return ok(profile);
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ProfileScreen(chats: chats, pickers: const _Picks())));
    await settle(t);

    expect(find.text('Your business in Explore'), findsOneWidget);
    expect(find.text('HOW OTHER BUSINESSES SEE YOU'), findsOneWidget);
    expect(find.text('Soups and swallows · Yaba'), findsOneWidget);
    expect(find.text('2 of 12'), findsOneWidget);
    expect(find.text('First'), findsOneWidget);

    // Typed: the card says it at once; Save sends it.
    // The fields in order: Name, One line about you, ...
    await t.enterText(find.byType(EditableText).at(1), 'Pepper soup daily');
    await t.pump();
    expect(find.text('Pepper soup daily · Yaba'), findsOneWidget);
    expect(find.text('17 / 140'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    expect(sent.single, startsWith('PUT /v1/business-profile '));
    expect(sent.single, contains('"tagline":"Pepper soup daily"'));
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();

    // Being found.
    sent.clear();
    await t.tap(find.byType(TkSwitch));
    await settle(t);
    expect(sent.single, 'PUT /v1/link-settings {"findable":true}');
    expect(find.text('Businesses near you can find you and connect.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();

    // Moved, added, removed.
    sent.clear();
    await t.tap(find.bySemanticsLabel('Move photo 1 later'));
    await settle(t);
    expect(sent.single, 'PUT /v1/business-profile/gallery/order {"ids":["b","a"]}');
    sent.clear();
    await t.tap(find.bySemanticsLabel('Add a photo'));
    await settle(t);
    expect(sent.single, 'POST /v1/business-profile/gallery <photo>');
    expect(find.text('3 of 12'), findsOneWidget);
    sent.clear();
    await t.tap(find.bySemanticsLabel('Remove photo 3'));
    await settle(t);
    expect(sent.single, startsWith('DELETE /v1/business-profile/gallery/c'));
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });
}
