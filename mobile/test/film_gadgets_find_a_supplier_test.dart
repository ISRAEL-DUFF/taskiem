// A film: find a supplier (Gadgets, Episode 7, "Your suppliers"). Ikenna opens Find a
// business, searches "phones", sees Gwarzo Distributors' card, taps Connect, and the
// card says Requested. Set-up copied from explore_test.dart (ExploreScreen on
// the recorded answers; the businesses are renamed to the story's). Skipped
// unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_gadgets_find_a_supplier_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/gadgets-find-a-supplier --out food-find.mp4 --captions

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

  testWidgets('film: gadgets find a supplier', (t) async {
    final f = Fixture('explore');
    var requested = false;
    Map<String, Object?> sade() => _card(
      '01a1cc22-8835-7982-af30-d991abf8ce92',
      'Gwarzo Distributors',
      'Sabon Gari Market, Kano',
      'Phones, chargers and tablets by the carton',
      'Wholesale phones, power banks and tablets for shops and resellers, sold by the carton.',
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
            if (q != null) return _answer(page(results: q.toLowerCase().contains('phones') ? [sade()] : []));
            return _answer(page(near: [
              _card('b2', 'Ikeja Accessory Hub', 'Computer Village, Ikeja', 'Cases, cables and screen guards', 'Phone cases, cables and screen guards for shops.'),
              _card('b3', 'Alaba Power Traders', 'Alaba, Lagos', 'Power banks and chargers', 'Power banks and chargers for retailers.'),
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
    final film = Film.start(t, 'gadgets-find-a-supplier');
    film.say('Ikenna needs phones for Techline Gadgets. He opens Find a business.', who: 'Ikenna · Techline Gadgets');
    await film.play(4.0);
    final box = find.byType(EditableText);
    film.say('He types phones in the search box to find suppliers.');
    await film.tap(box, then: 1.0);
    await film.keyboard(true, kind: 'text');
    await film.type(box, 'phones', perKey: 0.3);
    await film.hold(2.4);
    await film.keyboard(false);
    film.say('Gwarzo Distributors of Kano is on the list. Tap Connect.');
    await film.hold(4.0);
    await film.tap(find.bySemanticsLabel('Connect with Gwarzo Distributors'), then: 1.0);
    film.say('The request goes to Alhaji Gwarzo. His card now says Requested.');
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
