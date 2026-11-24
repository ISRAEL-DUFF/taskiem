// A film: find a supplier (Food, Episode 7, "Your suppliers"). Ada opens Find a
// business, searches "rice", sees Bayo Rice Mills' card, taps Connect, and the
// card says Requested. Set-up copied from explore_test.dart (ExploreScreen on
// the recorded answers; the businesses are renamed to the story's). Skipped
// unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_food_find_a_supplier_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/food-find-a-supplier --out food-find.mp4 --captions

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/explore.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

http.Response _answer(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

Map<String, Object?> _card(String id, String name, String area, String tagline, String about, [String state = 'none']) => {
  'business': id,
  'name': name,
  'area': area,
  'tagline': tagline,
  'about': about,
  'logo': false,
  'photos': 0,
  'captions': <Object>[],
  'state': state,
  'v': '1',
};

void main() {
  setUpAll(loadFonts);

  testWidgets('film: food find a supplier', (t) async {
    final f = Fixture('explore');
    var requested = false;
    Map<String, Object?> sade() => _card(
      '01a1cc22-8835-7982-af30-d991abf8ce92',
      'Bayo Rice Mills',
      'Mile 12 Market, Lagos',
      'Rice and beans by the bag',
      'Rice for restaurants and caterers: Ofada, long grain and parboiled, sold by the bag.',
      requested ? 'requested' : 'none',
    );
    Map<String, Object?> page({List<Object> results = const [], List<Object> near = const []}) => {
      'results': results,
      'near': near,
      'near_more': false,
      'fresh': <Object>[],
      'more': false,
      'delivery': <Object>[],
      'delivery_more': false,
    };
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET' && p == '/v1/links') return _answer({'links': <Object>[], 'sent': <Object>[]});
          if (req.method == 'GET' && p == '/v1/explore') {
            final q = req.url.queryParameters['q'];
            if (q != null) return _answer(page(results: q.toLowerCase().contains('rice') ? [sade()] : []));
            return _answer(page(near: [
              _card('b2', 'Mushin Spice Traders', 'Mushin, Lagos', 'Pepper, spices and seasoning', 'Pepper, curry, thyme and seasoning for kitchens.'),
              _card('b3', 'Yaba Pack Supplies', 'Yaba, Lagos', 'Foil, boxes and packaging', 'Food boxes, foil and take-away packaging for kitchens.'),
            ]));
          }
          if (req.method == 'POST' && p == '/v1/links') {
            requested = true;
            return _answer(<String, Object>{});
          }
          return null;
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ExploreScreen(chats: chats, onOpen: (_) {})));
    await _settle(t);
    final film = Film.start(t, 'food-find-a-supplier');
    film.say('Ada needs rice for Ada’s Kitchen. She opens Find a business.', who: 'Ada · Ada’s Kitchen');
    await film.play(4.0);
    final box = find.byType(EditableText);
    film.say('She types rice in the search box to find suppliers.');
    await film.tap(box, then: 1.0);
    await film.keyboard(true, kind: 'text');
    await film.type(box, 'rice', perKey: 0.3);
    await film.hold(2.4);
    await film.keyboard(false);
    film.say('Bayo Rice Mills is on the list, at Mile 12. Tap Connect.');
    await film.hold(4.0);
    await film.tap(find.bySemanticsLabel('Connect with Bayo Rice Mills'), then: 1.0);
    film.say('The request goes to Bayo. His card now says Requested.');
    await film.hold(5.5);
    await film.finish();
    await t.pump(const Duration(seconds: 6));
  }, skip: !filming);
}

Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
