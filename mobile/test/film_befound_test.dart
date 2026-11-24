// A film: be found in Explore (Episode 1 extra). Zainab opens "Your business
// in Explore", turns on Be found in Explore, types her area, picks Fashion and
// saves; then, scene two, a buyer opens Find a business and sees the card.
// Set-up copied from profile_screen_test.dart (the profile and its stand-in
// server) and explore_test.dart (Explore's list). The two scenes are two
// screens of the app, a cut between them (different people's phones).
//
//   VIDEO_DIR=/tmp/films flutter test test/film_befound_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/be-found --out be-found.mp4 --captions

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/auth.dart' show Auth;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/composer.dart';
import 'package:taskiem/chat/profile_screen.dart';
import 'package:taskiem/kit/forms.dart' show TkSwitch;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

http.Response _ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

Map<String, Object?> _card(String id, String name, String tagline, String area) => {
  'business': id,
  'name': name,
  'logo': false,
  'photos': 0,
  'captions': <Object>[],
  'state': 'none',
  'v': '1',
  'tagline': tagline,
  'area': area,
  'categories': ['fashion'],
};

/// The business and person in the story, in place of the recorded ones.
void zuri(Auth auth, Fixture f) {
  final j = f.json('/v1/me');
  j['tenant_name'] = 'Zuri Atelier';
  j['user'] = {...(j['user'] as Map<String, dynamic>), 'name': 'Zainab Yusuf', 'email': 'zainab@zuriatelier.example'};
  auth.me = Me.fromJson(j);
}

void main() {
  setUpAll(loadFonts);

  testWidgets('film: be found in Explore', (t) async {
    final f = Fixture('chats');
    var profile = <String, dynamic>{
      'name': 'Zuri Atelier',
      'tagline': 'Ankara, aso oke and made-to-measure',
      'area': '',
      'about': '',
      'logo': null,
      'gallery': <Object>[],
      'max_gallery': 12,
      'delivers': false,
      'vehicle': '',
      'delivery_areas': <String>[],
      'categories': <String>[],
    };
    var found = false;
    final auth = f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/business-profile') return _ok(profile);
        if (req.method == 'GET' && p == '/v1/link-settings') return _ok({'findable': found, 'findable_now': found});
        if (req.method == 'GET') return null;
        if (p == '/v1/link-settings') found = (jsonDecode(req.body) as Map)['findable'] == true;
        if (p == '/v1/business-profile') profile = {...profile, ...(jsonDecode(req.body) as Map<String, dynamic>)};
        return _ok(profile);
      });
    zuri(auth, f);
    final chats = ChatsData(auth, now: () => f.at)..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ProfileScreen(chats: chats, pickers: const Pickers())));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();

    final film = Film.start(t, 'be-found');
    film.say('Zainab wants buyers to find Zuri Atelier in Explore.', who: 'Zainab · Zuri Atelier');
    await film.play(2.6);

    film.say('Turn on Be found in Explore.');
    await film.tap(find.byType(TkSwitch), then: 1.2);
    await film.settle();
    await film.hold(1.6);

    film.say('Type your area, so buyers near you find you.');
    final scroll = find.byType(Scrollable).first;
    await film.drag(scroll, const Offset(0, -420), seconds: 0.8);
    final area = find.byType(EditableText).at(2);
    await t.ensureVisible(area);
    await t.pump();
    await film.hold(0.6);
    await film.tap(area, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(area, 'Surulere, Lagos', perKey: 0.08);
    await film.hold(0.6);
    await film.keyboard(false);
    await film.hold(0.5);

    film.say('Pick what you sell: Fashion.');
    final fashion = find.bySemanticsLabel('Fashion');
    await Scrollable.ensureVisible(t.element(fashion), alignment: 0.4);
    await t.pump();
    await film.hold(0.8);
    await film.tap(fashion, then: 1.2);

    film.say('Tap Save. Your card is now in Explore.');
    final save = find.bySemanticsLabel('Save');
    await Scrollable.ensureVisible(t.element(save), alignment: 0.6);
    await t.pump();
    await film.hold(0.6);
    await film.tap(save, then: 1.0);
    await film.settle();
    await film.hold(0.8);

    film.say('Her card now shows the line and area buyers will see.');
    await film.drag(scroll, const Offset(0, 1400), seconds: 1.0);
    await film.hold(2.6);

    // Scene two: a buyer's phone, Find a business.
    final g = Fixture('explore');
    final buyerAuth = g.auth(also: (req) async {
        if (req.url.path == '/v1/explore' && req.method == 'GET' && req.url.queryParameters.isEmpty) {
          return _ok({
            'results': <Object>[],
            'near': [
              _card('z1', 'Zuri Atelier', 'Ankara, aso oke and made-to-measure', 'Surulere, Lagos'),
              _card('z2', 'Adire Corner', 'Hand-dyed adire by the yard', 'Yaba'),
              _card('z3', 'Lagos Lace House', 'Lace and aso ebi', 'Ikeja'),
            ],
            'near_more': false,
            'fresh': <Object>[],
            'more': false,
            'delivery': <Object>[],
            'delivery_more': false,
            'categories': [
              {'key': 'fashion', 'name': 'Fashion', 'n': 3},
            ],
          });
        }
        return null;
      });
    final bj = g.json('/v1/me');
    bj['tenant_name'] = 'Nneka Eze Styling';
    bj['user'] = {...(bj['user'] as Map<String, dynamic>), 'name': 'Nneka Eze', 'email': 'nneka@example.com'};
    buyerAuth.me = Me.fromJson(bj);
    final buyer = ChatsData(buyerAuth, now: () => g.at);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatsScreen(data: buyer, onOpen: (_) {})));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    film.say('Later, a buyer taps Find a business in Taskiem.');
    await film.hold(1.6);
    await film.tap(find.bySemanticsLabel('Find a business to link with'), then: 1.0);
    await film.settle();
    film.say('Zuri Atelier is listed, with its line, its area and Fashion.');
    await film.hold(4.5);
    await film.finish();
  }, skip: !filming);
}
