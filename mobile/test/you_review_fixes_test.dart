// Stage 3's review of the app (docs/spec/taskiem-for-everyone.md, "As
// built: stage 3, app review fixes"): Discover's Show more after a search
// that came late, and paging the search that is shown; one chat or pay
// page for a double tap; Orders kept in place and read again, and said
// when it cannot be; an earlier order's short row; bookings by kind; the
// check step's address; a day's every opening; Discover's settings
// keeping what was typed; the web's words.

import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart' show DiscoverSettings;
import 'package:taskiem/app/applinks.dart' show LinkInbox;
import 'package:taskiem/app/config.dart' show taskiemUrl;
import 'package:taskiem/app/links.dart';
import 'package:taskiem/screens/you.dart' show YouHome;
import 'package:taskiem/chat/discover.dart';
import 'package:taskiem/chat/discover_settings.dart';
import 'package:taskiem/chat/door.dart' show yourOrderStatus;
import 'package:taskiem/chat/words.dart' show listTime;
import 'package:taskiem/chat/your_orders.dart';
import 'package:taskiem/kit/kit.dart' show TkErrorBox;
import 'package:taskiem/kit/sheet.dart' show TkKitField;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'my_chats_test.dart' show Api, answer, at, chat, conversation, drain, openChat, phone, press, settle, typeIn;
import 'support.dart' show loadFonts;
import 'you_stage3_test.dart' show address, openYou, order, result, you;

Finder field(String label) => find.ancestor(of: find.text(label), matching: find.byType(TkKitField));

/// Discover served: [search] answers each search.
Api discoverApi(Object? Function(Map<String, dynamic> body) search) {
  final api = you();
  api.routes['GET /v1/me/orders'] = (_) => {'orders': <Object>[], 'next': null, 'waiting_count': 0};
  api.routes['GET /v1/discover/categories'] = (_) => {
    'categories': [
      {'key': 'food', 'name': 'Food and drinks'},
    ],
  };
  api.routes['POST /v1/discover/search'] = (r) => search(api.body(r));
  return api;
}

Future<void> openDiscover(WidgetTester t, Api api) async {
  await openYou(t, api);
  await press(t, find.text('Discover').first);
}

Map<String, dynamic> discoverSettings({String? area, String? category, Map<String, dynamic>? hours}) => {
  'listed': true,
  'chosen': null,
  'default': true,
  'area': area,
  'category': category,
  'hours': hours,
  'missing': <String>[],
  'shown': true,
  'listing': {
    'business': 't1',
    'name': 'Mama’s Kitchen',
    'area': area ?? 'Yaba',
    'category': category ?? 'food',
    'can_chat': true,
    'has_shop': true,
    'has_book': false,
    'hours': <String, dynamic>{},
    'photos': <Object>[],
  },
};

void main() {
  setUpAll(loadFonts);

  group('Discover', () {
    testWidgets('Show more pages through the search shown, not what is typed since', (t) async {
      phone(t);
      final api = discoverApi((b) {
        if (b['offset'] == 2) return {'results': [result('b9', 'Zainab Cakes')], 'more': false};
        return {'results': [result('b1', 'Mama’s Kitchen'), result('b2', 'Bola Stores')], 'more': true};
      });
      await openDiscover(t, api);
      // Typed, and Show more pressed before the typing settles.
      await t.enterText(find.descendant(of: find.bySemanticsLabel('Search by name or what you want'), matching: find.byType(EditableText)), 'rice');
      await t.pump();
      await t.tap(find.text('Show more businesses'));
      await settle(t);
      final sent = api.sent('POST', '/v1/discover/search').map(api.body).toList();
      expect(sent.where((b) => b['offset'] == 2).single, {'offset': 2}, reason: 'the page of the search shown, without the words typed since');
      await settle(t);
      expect(api.body(api.sent('POST', '/v1/discover/search').last), {'q': 'rice'}, reason: 'then the words are searched');
      await drain(t);
    });

    testWidgets('a page that comes after a newer search is let go, and Show more works again', (t) async {
      phone(t);
      final held = Completer<Object?>();
      var pages = 0;
      final api = discoverApi((b) {
        if (b['offset'] != null) return ++pages == 1 ? held.future : {'results': [result('b8', 'Rice Palace')], 'more': false};
        return {'results': [result('b1', 'Mama’s Kitchen'), result('b2', 'Bola Stores')], 'more': true};
      });
      await openDiscover(t, api);
      await t.tap(find.text('Show more businesses'));
      await t.pump();
      expect(find.text('Loading…'), findsOneWidget);
      // A new search meanwhile.
      await typeIn(t, find.bySemanticsLabel('Search by name or what you want'), 'rice');
      await settle(t);
      await settle(t);
      held.complete({'results': [result('b9', 'Zainab Cakes')], 'more': false});
      await settle(t);
      expect(find.text('Zainab Cakes'), findsNothing, reason: 'the old search\'s page is let go');
      expect(find.text('Loading…'), findsNothing);
      await press(t, find.text('Show more businesses'));
      expect(api.body(api.sent('POST', '/v1/discover/search').last), {'q': 'rice', 'offset': 2});
      expect(find.text('Rice Palace'), findsOneWidget);
      await drain(t);
    });

    testWidgets('a page that fails says so and Show more can be pressed again', (t) async {
      phone(t);
      var fail = true;
      final api = discoverApi((b) {
        if (b['offset'] != null) {
          return fail ? answer({'error': 'You are searching too fast. Wait a moment and try again.'}, 429) : {'results': [result('b9', 'Zainab Cakes')], 'more': false};
        }
        return {'results': [result('b1', 'Mama’s Kitchen')], 'more': true};
      });
      await openDiscover(t, api);
      await press(t, find.text('Show more businesses'));
      expect(find.text('You are searching too fast. Wait a moment and try again.'), findsOneWidget);
      fail = false;
      await press(t, find.text('Show more businesses'));
      expect(find.text('Zainab Cakes'), findsOneWidget);
      await drain(t);
    });

    testWidgets('nothing found says what was searched', (t) async {
      phone(t);
      final api = discoverApi((_) => {'results': <Object>[], 'more': false});
      await openDiscover(t, api);
      expect(find.text('No business is listed here yet. Only businesses that chose to be found are listed.'), findsOneWidget);
      await press(t, find.text('Food and drinks'));
      await typeIn(t, find.bySemanticsLabel('Search by name or what you want'), 'jollof');
      await typeIn(t, find.bySemanticsLabel('Area (Yaba, Lekki…)'), 'Yaba');
      await settle(t);
      await settle(t);
      expect(
        find.text('No business found for “jollof” Food and drinks in Yaba. Try other words, or another area. Only businesses that chose to be found are listed.'),
        findsOneWidget,
      );
      await drain(t);
    });

    testWidgets('Chat pressed twice on a row starts one chat', (t) async {
      phone(t);
      final held = Completer<Object?>();
      final api = discoverApi((_) => {'results': [result('b1', 'Mama’s Kitchen')], 'more': false});
      api.routes['POST /v1/me/chats'] = (_) => held.future;
      await openDiscover(t, api);
      await t.tap(find.bySemanticsLabel('Chat with Mama’s Kitchen'));
      await t.pump();
      await t.tap(find.bySemanticsLabel('Chat with Mama’s Kitchen'));
      await t.pump();
      held.complete(chat(id: 'c1', offers: 'no', ask: false));
      await settle(t);
      expect(api.sent('POST', '/v1/me/chats'), hasLength(1));
      expect(find.text('Open now, closes at 18:00'), findsOneWidget, reason: 'one conversation open');
      await drain(t);
    });

    testWidgets('a profile no longer in Discover; one not taking chats links its page', (t) async {
      phone(t);
      final opened = <Uri>[];
      final was = openInBrowser;
      openInBrowser = (u) async {
        opened.add(u);
        return true;
      };
      addTearDown(() => openInBrowser = was);
      final api = discoverApi((_) => {'results': [result('b1', 'Mama’s Kitchen'), result('b2', 'Bola Stores', canChat: false)], 'more': false});
      api.routes['GET /v1/discover/b1'] = (_) => answer({'error': 'This business is not in Discover any more.'}, 404);
      api.routes['GET /v1/discover/b2'] = (_) => {
        'business': 'b2',
        'name': 'Bola Stores',
        'category_name': 'Food and drinks',
        'photos': <Object>[],
        'hours': <String, dynamic>{},
        'open_now': false,
        'line': 'Closed now. Opens tomorrow at 09:00',
        'shop': 'https://bola.taskiem.page',
        'can_chat': false,
        'has_shop': true,
        'has_book': false,
      };
      await openDiscover(t, api);
      await press(t, find.text('Mama’s Kitchen'));
      expect(find.text('Not in Discover'), findsOneWidget);
      expect(find.text('This business is not in Discover any more.'), findsOneWidget);
      await press(t, find.text('Back to Discover').last);
      await press(t, find.text('Bola Stores'));
      expect(find.text('Closed now'), findsOneWidget);
      expect(find.text('Closed now. Opens tomorrow at 09:00'), findsOneWidget);
      expect(find.text('Bola Stores is not taking chats in Taskiem right now. You can still visit their page.'), findsOneWidget);
      await press(t, find.text('Visit their page'));
      expect(opened, [Uri.parse('https://bola.taskiem.page')]);
      await drain(t);
    });

    test('words, as the web\'s', () {
      expect(kmWords(0.73), '700 m away');
      expect(kmWords(0.02), '100 m away');
      expect(kmWords(2.44), '2.4 km away');
      expect(kmWords(15.4), '15 km away');
      expect(openNowWords(false), 'Closed now');
      expect(nothingFound(q: 'cake', area: '', categoryName: null), 'No business found for “cake”. Try other words, or another area. Only businesses that chose to be found are listed.');
    });
  });

  group('Orders', () {
    testWidgets('back from an order: only its row is read again, the older pages stay', (t) async {
      phone(t);
      final api = you();
      var state = 'accepted';
      api.routes['GET /v1/me/orders'] = (r) => r.url.queryParameters['before'] == 'cur-2'
          ? {'orders': [order(ref: 'BS-1', business: 'b2', name: 'Bola Stores', state: 'completed', made: '2026-09-01T10:00:00Z')], 'next': null, 'waiting_count': 0}
          : {'orders': [order(chatId: 'c1', state: state)], 'next': 'cur-2', 'waiting_count': 0};
      api.routes['GET /v1/me/orders/b1/MK-1042'] = (_) => {...order(chatId: 'c1', state: state), 'lines': <Object>[], 'paid_kobo': 0};
      await openYou(t, api);
      await press(t, find.text('Orders').first);
      await press(t, find.text('Show older orders'));
      expect(find.text('Bola Stores'), findsOneWidget);
      final lists = api.sent('GET', '/v1/me/orders').length;
      await press(t, find.text('Mama’s Kitchen'));
      state = 'dispatched';
      await press(t, find.bySemanticsLabel('Back to Orders'));
      expect(api.sent('GET', '/v1/me/orders'), hasLength(lists), reason: 'the list is not read from its start');
      expect(find.text('Bola Stores'), findsOneWidget, reason: 'the older page stays');
      expect(api.sent('GET', '/v1/me/orders/b1/MK-1042'), hasLength(2), reason: 'its row read again');
      expect(find.text(yourOrderStatus('dispatched')), findsOneWidget);
      await drain(t);
    });

    testWidgets('read again when shown, and a failure said as a toast', (t) async {
      phone(t);
      final api = you();
      var fail = false;
      api.routes['GET /v1/me/orders'] = (_) => fail ? answer({'error': 'The server is busy. Try again.'}, 503) : {'orders': [order()], 'next': null, 'waiting_count': 0};
      api.routes['GET /v1/discover/categories'] = (_) => {'categories': <Object>[]};
      api.routes['POST /v1/discover/search'] = (_) => {'results': <Object>[], 'more': false};
      await openYou(t, api);
      await press(t, find.text('Orders').first);
      expect(api.sent('GET', '/v1/me/orders'), hasLength(1));
      await press(t, find.text('Chats').last);
      await press(t, find.text('Orders').first);
      expect(api.sent('GET', '/v1/me/orders'), hasLength(2), reason: 'shown again: read again');
      fail = true;
      await press(t, find.text('Discover').first);
      await press(t, find.text('Orders').first);
      expect(find.text('The server is busy. Try again.'), findsOneWidget, reason: 'said, not hidden');
      expect(find.text('Mama’s Kitchen'), findsOneWidget, reason: 'what was shown stays');
      await drain(t);
    });

    testWidgets('Pay pressed twice opens one page; back in the app, the order is read again', (t) async {
      phone(t);
      final opened = <Uri>[];
      final was = openInBrowser;
      openInBrowser = (u) async {
        opened.add(u);
        return true;
      };
      addTearDown(() => openInBrowser = was);
      final api = you();
      final held = Completer<Object?>();
      var paid = false;
      api.routes['GET /v1/me/orders'] = (_) => {'orders': [order(pay: true)], 'next': null, 'waiting_count': 0};
      api.routes['GET /v1/me/orders/b1/MK-1042'] = (_) => {...order(pay: !paid, state: paid ? 'paid' : 'accepted'), 'lines': <Object>[], 'paid_kobo': paid ? 450000 : 0};
      api.routes['GET /v1/me/orders/b1/MK-1042/pay'] = (_) => held.future;
      await openYou(t, api);
      await press(t, find.text('Orders').first);
      await press(t, find.text('Mama’s Kitchen'));
      await t.tap(find.text('Pay ₦4,500'));
      await t.pump();
      await t.tap(find.text('Pay ₦4,500'));
      await t.pump();
      held.complete({'reference': 'MK-1042', 'due_kobo': 450000, 'url': 'https://app.taskiem.com/o/abc'});
      await settle(t);
      expect(api.sent('GET', '/v1/me/orders/b1/MK-1042/pay'), hasLength(1));
      expect(opened, hasLength(1));
      paid = true;
      t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await settle(t);
      expect(api.sent('GET', '/v1/me/orders/b1/MK-1042'), hasLength(2));
      expect(find.text('Pay ₦4,500'), findsNothing);
      expect(find.text('₦4,500 paid · Made ${listTime(DateTime.parse('2026-11-15T10:00:00Z'), at)}'), findsOneWidget);
      await drain(t);
    });

    testWidgets('Pay refused: a closed order said plainly', (t) async {
      phone(t);
      final api = you();
      api.routes['GET /v1/me/orders'] = (_) => {'orders': [order(pay: true)], 'next': null, 'waiting_count': 0};
      api.routes['GET /v1/me/orders/b1/MK-1042'] = (_) => {...order(pay: true), 'lines': <Object>[], 'paid_kobo': 0};
      api.routes['GET /v1/me/orders/b1/MK-1042/pay'] = (_) => answer({'error': 'This order is closed.'}, 409);
      await openYou(t, api);
      await press(t, find.text('Orders').first);
      await press(t, find.text('Mama’s Kitchen'));
      await press(t, find.text('Pay ₦4,500'));
      expect(find.text('This order is closed.'), findsOneWidget);
      expect(payProblem(Exception()), 'Something went wrong. Try again.');
      await drain(t);
    });

    testWidgets('an earlier order: a short row, only Chat about this, nothing more read', (t) async {
      phone(t);
      final api = you();
      api.routes['GET /v1/me/orders'] = (_) => {
        'orders': [
          {...order(ref: 'OLD-1', business: 'b4', name: 'Ada Foods', state: 'completed'), 'summary': '', 'earlier': true},
          {...order(ref: 'HR-2', business: 'b5', name: 'Party Rentals', kind: 'booking'), 'booking_type': 'hire', 'summary': '2 × Canopy, 3 days'},
        ],
        'next': null,
        'waiting_count': 0,
      };
      api.routes['POST /v1/me/chats'] = (_) => chat(id: 'c1', offers: 'no', ask: false);
      await openYou(t, api);
      await press(t, find.text('Orders').first);
      final when = listTime(DateTime.parse('2026-11-15T10:00:00Z'), at);
      expect(find.text('Made before this number was yours · $when'), findsOneWidget);
      expect(find.text('Hire · 2 × Canopy, 3 days · $when'), findsOneWidget);
      await press(t, find.text('Ada Foods'));
      expect(find.text('Order OLD-1'), findsOneWidget);
      expect(find.textContaining('so only this much of it shows'), findsOneWidget);
      expect(find.textContaining('Pay'), findsNothing);
      expect(api.sent('GET', '/v1/me/orders/b4/OLD-1'), isEmpty, reason: 'its row is all there is');
      await press(t, find.text('Chat about this'));
      expect(api.body(api.sent('POST', '/v1/me/chats').single), {'business_id': 'b4', 'from': 'order'});
      expect((t.widget(find.byType(EditableText)) as EditableText).controller.text, 'Hello, about my order OLD-1: ');
      await drain(t);
    });

    testWidgets('Chat about this pressed twice opens one chat', (t) async {
      phone(t);
      final api = you();
      final held = Completer<Object?>();
      api.routes['GET /v1/me/orders'] = (_) => {'orders': [order()], 'next': null, 'waiting_count': 0};
      api.routes['GET /v1/me/orders/b1/MK-1042'] = (_) => {...order(), 'lines': <Object>[], 'paid_kobo': 0};
      api.routes['POST /v1/me/chats'] = (_) => held.future;
      await openYou(t, api);
      await press(t, find.text('Orders').first);
      await press(t, find.text('Mama’s Kitchen'));
      await t.tap(find.text('Chat about this'));
      await t.pump();
      await t.tap(find.text('Chat about this'));
      await t.pump();
      held.complete(chat(id: 'c1', offers: 'no', ask: false));
      await settle(t);
      expect(api.sent('POST', '/v1/me/chats'), hasLength(1));
      await drain(t);
    });
  });

  group('the check step', () {
    Api shop({List<Map<String, dynamic>>? saved, Future<Object?>? late}) {
      final api = conversation(c: chat(offers: 'no', ask: false));
      api.routes['GET /v1/me/chats/c1/shop'] = (_) => {
        'open': true,
        'payment': 'at_order',
        'pickup': false,
        'delivery': true,
        'zones': [
          {'name': 'Yaba', 'fee_kobo': 50000},
        ],
        'items': [
          {'id': 'i1', 'name': 'Jollof rice', 'price_kobo': 250000, 'available': true},
        ],
      };
      api.routes['GET /v1/me/addresses'] = (_) => late ?? {'addresses': saved ?? <Object>[]};
      api.routes['POST /v1/me/chats/c1/orders'] = (_) => answer({
        'order': {'reference': 'MK-1051', 'kind': 'app', 'status': 'sent', 'summary': '1 × Jollof rice', 'total_kobo': 300000, 'due_kobo': 0, 'pay': false, 'created_at': '2026-11-16T10:30:00Z'},
      }, 201);
      api.routes['POST /v1/me/addresses'] = (_) => answer(address('a3', 'Home'), 201);
      return api;
    }

    Future<void> toCheck(WidgetTester t) async {
      await press(t, find.bySemanticsLabel('Chat tools'));
      await press(t, find.text('Their shop'));
      await press(t, find.bySemanticsLabel('One more Jollof rice'));
      await press(t, find.text('Review order'));
    }

    testWidgets('a typed address saved: named Home, its zone as its area, kept to 200', (t) async {
      phone(t);
      final api = shop();
      await openChat(t, api);
      await toCheck(t);
      await press(t, find.text('Yaba · ₦500'));
      await press(t, find.text('Save this address'));
      expect(find.text('Name it'), findsOneWidget);
      await typeIn(t, field('Address'), '7 Allen Avenue, Ikeja');
      await press(t, find.textContaining('Place order'));
      expect(api.body(api.sent('POST', '/v1/me/addresses').single), {'label': 'Home', 'line': '7 Allen Avenue, Ikeja', 'area': 'Yaba'});
      await drain(t);
    });

    testWidgets('an address typed too long to save is said', (t) async {
      phone(t);
      final api = shop();
      await openChat(t, api);
      await toCheck(t);
      await press(t, find.text('Yaba · ₦500'));
      await typeIn(t, field('Address'), 'x' * 250);
      await press(t, find.text('Save this address'));
      await press(t, find.textContaining('Place order'));
      expect(find.text('To save it, keep the address to 200 letters.'), findsOneWidget);
      expect(api.sent('POST', '/v1/me/chats/c1/orders'), isEmpty);
      await drain(t);
    });

    testWidgets('no Save at 20 addresses', (t) async {
      phone(t);
      final api = shop(saved: [for (var i = 0; i < 20; i++) address('a$i', 'Place $i', def: i == 0)]);
      await openChat(t, api);
      await toCheck(t);
      await press(t, find.text('Another address'));
      expect(find.text('Save this address'), findsNothing);
      await drain(t);
    });

    testWidgets('addresses that come late leave the choice made', (t) async {
      phone(t);
      final late = Completer<Object?>();
      final api = shop(late: late.future);
      await openChat(t, api);
      await toCheck(t);
      await press(t, find.text('Yaba · ₦500'));
      await typeIn(t, field('Address'), '7 Allen Avenue, Ikeja');
      late.complete({'addresses': [address('a1', 'Home', def: true)]});
      await settle(t);
      await press(t, find.textContaining('Place order'));
      final body = api.body(api.sent('POST', '/v1/me/chats/c1/orders').single);
      expect(body['address'], '7 Allen Avenue, Ikeja', reason: 'what was typed, not the default that came late');
      expect(body.containsKey('address_id'), isFalse);
      await drain(t);
    });
  });

  group('Settings > Discover', () {
    testWidgets('every opening of a day shown, kept, added and taken away', (t) async {
      phone(t);
      Map<String, dynamic>? got;
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: Builder(
          builder: (context) => GestureDetector(
            onTap: () async => got = await Navigator.of(context).push<Map<String, dynamic>>(
              PageRouteBuilder(
                pageBuilder: (_, _, _) => const DiscoverHoursSheet(hours: {
                  'days': [
                    [
                      {'open': '08:00', 'close': '12:00'},
                      {'open': '14:00', 'close': '18:00'},
                    ],
                    [
                      {'open': '09:00', 'close': '17:00'},
                    ],
                    [], [], [], [], [],
                  ],
                }),
              ),
            ),
            child: const Text('open'),
          ),
        ),
      ));
      await press(t, find.text('open'));
      expect(find.text('14:00'), findsOneWidget, reason: 'the second opening shows');
      expect(find.text('Opens again'), findsOneWidget);
      await press(t, find.text('Add an opening on Tuesday'));
      await typeIn(t, field('Closes').at(3), '20:00');
      await typeIn(t, field('Opens again').at(1), '18:00');
      await press(t, find.bySemanticsLabel('Take away Monday’s opening 1'));
      await press(t, find.text('Save'));
      expect((got!['days'] as List)[0], [
        {'open': '14:00', 'close': '18:00'},
      ]);
      expect((got!['days'] as List)[1], [
        {'open': '09:00', 'close': '17:00'},
        {'open': '18:00', 'close': '20:00'},
      ]);
      await drain(t);
    });

    testWidgets('a typed area stays when another setting changes; refusals said; back to "From my profile"', (t) async {
      phone(t);
      final api = Api();
      var s = discoverSettings(category: 'food');
      var refuse = false;
      api.routes['GET /v1/settings/discover'] = (_) => s;
      api.routes['PUT /v1/settings/discover'] = (r) {
        if (refuse) return answer({'error': 'That area is too long.'}, 400);
        final b = api.body(r);
        if (b.containsKey('category')) s = discoverSettings(category: (b['category'] as String).isEmpty ? null : b['category'] as String, area: s['area'] as String?);
        if (b.containsKey('area')) s = discoverSettings(category: s['category'] as String?, area: b['area'] as String);
        return s;
      };
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: DiscoverSettingsPage(auth: api.auth(permissions: ['storefront.manage'], personal: false))));
      await settle(t);
      expect(find.text('Listed'), findsOneWidget);
      await typeIn(t, field('Where customers find you'), 'Surulere');
      await press(t, find.text('Category'));
      await press(t, find.text('From my profile'));
      expect(api.body(api.sent('PUT', '/v1/settings/discover').last), {'category': ''});
      expect(find.text('Surulere'), findsOneWidget, reason: 'typed and not saved: kept');
      expect(find.text('From my profile: Food and drinks'), findsOneWidget);

      refuse = true;
      await press(t, find.text('Save'));
      expect(find.text('That area is too long.'), findsOneWidget, reason: 'said under the area');
      expect(find.text('Surulere'), findsOneWidget);
      await press(t, find.text('Category'));
      await press(t, find.text('Beauty and personal care'));
      expect(find.text('That area is too long.'), findsNWidgets(2), reason: 'the category\'s refusal as a toast');
      refuse = false;
      await t.pump(const Duration(seconds: 10));
      await press(t, find.text('Save'));
      expect(api.body(api.sent('PUT', '/v1/settings/discover').last), {'area': 'Surulere'});
      expect(find.descendant(of: find.byType(TkErrorBox), matching: find.text('That area is too long.')), findsNothing);
      await drain(t);
    });

    test('where a listing stands, as the web says', () {
      expect(discoverStanding(DiscoverSettings.fromJson(discoverSettings())).tag, 'Listed');
    });
  });

  testWidgets('links to You open its tabs and pages: an order, Discover, a profile, a chat to start', (t) async {
    phone(t);
    final api = discoverApi((_) => {'results': [result('b1', 'Mama’s Kitchen')], 'more': false});
    api.routes['GET /v1/me/orders/b1/MK-1042'] = (_) => {...order(), 'lines': <Object>[], 'paid_kobo': 0};
    api.routes['GET /v1/discover/b1'] = (_) => answer({'error': 'This business is not in Discover any more.'}, 404);
    api.routes['POST /v1/me/chats'] = (_) => chat(id: 'c1', offers: 'no', ask: false);
    final links = StreamController<Uri>();
    final inbox = LinkInbox(links: links.stream, first: () async => null);
    addTearDown(() {
      inbox.dispose();
      links.close();
    });
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), links: inbox, now: () => at)));
    await settle(t);
    Future<void> link(String path) async {
      links.add(Uri.parse('$taskiemUrl$path'));
      await settle(t);
      await settle(t);
    }

    await link('/you/orders/b1/MK-1042');
    expect(find.text('Order MK-1042'), findsOneWidget);
    await link('/you/discover/b1');
    expect(find.text('Order MK-1042'), findsNothing, reason: 'what was open is closed first');
    expect(find.text('This business is not in Discover any more.'), findsOneWidget);
    await link('/you/discover');
    expect(find.text('Mama’s Kitchen'), findsOneWidget, reason: 'the Discover tab');
    await link('/you/chats/new?business=b1&ref=MK-1042');
    expect(api.body(api.sent('POST', '/v1/me/chats').single), {'business_id': 'b1', 'from': 'order'});
    expect((t.widget(find.byType(EditableText)) as EditableText).controller.text, 'Hello, about my order MK-1042: ');
    await drain(t);
  });

  testWidgets('an order from a link: read by its business and reference', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/orders/b1/MK-1042'] = (_) => {...order(), 'lines': <Object>[], 'paid_kobo': 0};
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: YourOrderPage.of(auth: api.auth(), business: 'b1', reference: 'MK-1042', onOpenChat: (id, {notice, draft = '', tool}) async {}, now: () => at),
    ));
    await settle(t);
    expect(find.text('Order MK-1042'), findsOneWidget);
    expect(find.text('Mama’s Kitchen'), findsOneWidget);
    await drain(t);
  });
}
