// A film: Taskiem for you, episode 2, find a business. Tobi is signed in on You.
// He taps the Discover tab, picks the category Food and drinks, types the area
// Yaba, and taps Ada's Kitchen to open its profile (hours, address, Chat, Their
// shop). Set-up copied from you_stage3_test.dart ("Discover: categories, a search
// ...", "a profile ..."). The businesses and their hours are stand-ins; the
// search answers by the words, area and category as the real server filters.
// Discover has no "near me" (the app cannot ask for the phone's place), so the
// film does not show one. Skipped unless VIDEO_DIR is set.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'my_chats_test.dart' show Api;
import 'support.dart' show loadFonts;

Map<String, dynamic> _biz(String id, String name, String cat, String catName, String area, {bool open = true, String? line}) => {
  'business': id,
  'name': name,
  'photo': <String, dynamic>{},
  'category': cat,
  'category_name': catName,
  'area': area,
  'open_now': open,
  'line': line ?? (open ? 'Open now, closes at 18:00' : 'Closed now. Opens tomorrow at 09:00'),
  'can_chat': true,
};

void main() {
  setUpAll(loadFonts);

  testWidgets('film: find a business', (t) async {
    final all = [
      _biz('b1', 'Ada’s Kitchen', 'food', 'Food and drinks', 'Yaba'),
      _biz('b2', 'Bayo Rice Mills', 'food', 'Food and drinks', 'Lekki', line: 'Open now, closes at 17:00'),
      _biz('b3', 'Kola’s Barbing Salon', 'beauty', 'Beauty and personal care', 'Surulere', open: false),
    ];
    final api = Api();
    api.routes['GET /v1/me/chats'] = (_) => {'chats': <Object>[], 'unread': 0};
    api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
    api.routes['GET /v1/discover/categories'] = (_) => {
      'categories': [
        {'key': 'food', 'name': 'Food and drinks'},
        {'key': 'building', 'name': 'Building materials'},
        {'key': 'fashion', 'name': 'Fashion'},
        {'key': 'phones', 'name': 'Phones and gadgets'},
        {'key': 'electrical', 'name': 'Electrical and electronics'},
        {'key': 'beauty', 'name': 'Beauty and personal care'},
        {'key': 'services', 'name': 'Services'},
      ],
    };
    api.routes['POST /v1/discover/search'] = (r) {
      final b = api.body(r);
      final q = ((b['q'] as String?) ?? '').toLowerCase(), area = ((b['area'] as String?) ?? '').toLowerCase(), cat = b['category'] as String?;
      return {
        'results': [
          for (final x in all)
            if ((cat == null || x['category'] == cat) &&
                (area.isEmpty || (x['area'] as String).toLowerCase().contains(area)) &&
                (q.isEmpty || (x['name'] as String).toLowerCase().contains(q)))
              x,
        ],
        'more': false,
      };
    };
    api.routes['GET /v1/discover/b1'] = (_) => {
      'business': 'b1',
      'name': 'Ada’s Kitchen',
      'tagline': 'Jollof rice, zobo and more, cooked fresh',
      'category': 'food',
      'category_name': 'Food and drinks',
      'area': 'Yaba',
      'address': '12 Herbert Macaulay Way, Yaba',
      'map_link': 'https://maps.example/adas',
      'photo': null,
      'photos': <Object>[],
      'hours': {
        'days': [
          [{'open': '09:00', 'close': '18:00'}],
          [{'open': '09:00', 'close': '18:00'}],
          [{'open': '09:00', 'close': '18:00'}],
          [{'open': '09:00', 'close': '18:00'}],
          [{'open': '09:00', 'close': '18:00'}],
          [{'open': '10:00', 'close': '16:00'}],
          [],
        ],
      },
      'open_now': true,
      'line': 'Open now, closes at 18:00',
      'can_chat': true,
      'has_shop': true,
      'has_book': false,
      'chat': null,
    };
    final auth = api.auth();
    auth.me = Me.fromJson({
      'tenant_id': null,
      'personal': true,
      'roles': <String>[],
      'permissions': <String>[],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Tobi Adewale', 'email': 'p@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031234567'},
      'businesses': <Object>[],
    });
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: auth, now: () => DateTime.utc(2026, 11, 24, 12, 0))));
    final f = Film.start(t, 'you-find');
    await f.settle();

    f.say('Tobi is signed in. He taps Discover to find a business.', who: 'Tobi · Lagos');
    await f.play(2.6);
    await f.tap(find.text('Discover').first, then: 0.5);
    await f.settle();
    await f.hold(2.4);

    f.say('Discover lists businesses that chose to be found, with when they are open.');
    await f.hold(3.4);

    f.say('He taps a category: Food and drinks.');
    await f.tap(find.text('Food and drinks').first, then: 0.5);
    await f.settle();
    await f.hold(2.4);

    f.say('Only food businesses are left. He taps the Area box and types Yaba.');
    final area = find.descendant(of: find.bySemanticsLabel('Area (Yaba, Lekki…)'), matching: find.byType(EditableText));
    await f.tap(area, then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(area, 'Yaba', perKey: 0.2);
    await f.hold(1.2);
    await f.settle();
    await f.hold(1.0);
    await f.keyboard(false);
    await f.hold(1.8);

    f.say('One business is near him: Ada’s Kitchen. He taps its name.');
    await f.tap(find.text('Ada’s Kitchen').first, then: 0.5);
    await f.settle();
    await f.hold(2.0);

    f.say('Its profile shows what it sells and when it is open.');
    await f.hold(3.0);
    final shop = find.text('Their shop');
    f.say('Chat and Their shop are here; he can message the business.');
    await f.hold(3.0);
    expect(shop, findsOneWidget);
    f.say('Further down: opening hours, the address and a map link.');
    await f.drag(find.text('Ada’s Kitchen').last, const Offset(0, -300), seconds: 0.9);
    await f.hold(3.2);
    await f.finish();
  }, skip: !filming);
}
