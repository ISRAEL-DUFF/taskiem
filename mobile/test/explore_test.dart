// Find a business, as a person uses it: Connect (a request at once, then
// Requested and Withdraw), an ask answered from its card, a linked
// business messaged, a phone number offered a request, and nothing found.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/explore.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

http.Response _answer(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
}

/// Explore's list (the screen on top), not a card's photos, which swipe across.
final _list = find.byWidgetPredicate((w) => w is Scrollable && w.axisDirection == AxisDirection.down).last;

void main() {
  test('what a card offers, and its line (explore.test.ts)', () {
    expect(cardOffer('none'), 'connect');
    expect(cardOffer('ended'), 'connect');
    expect(cardOffer('not_accepted'), 'connect');
    expect(cardOffer('requested'), 'requested');
    expect(cardOffer('active'), 'message');
    expect(cardOffer('asked_us'), 'asked');
    expect(cardOffer('blocked'), isNull);
    ExploreCard c({String? tagline, String? area, bool? delivers, String? vehicle, List<String>? areas, String? available}) => ExploreCard(
      business: 'b',
      name: 'B',
      logo: false,
      photos: 0,
      captions: const [],
      state: 'none',
      v: '1',
      tagline: tagline,
      area: area,
      delivers: delivers,
      vehicle: vehicle,
      deliveryAreas: areas,
      available: available,
    );
    expect(exploreLine(c(tagline: 'Cement and sand', area: 'Ikeja')), 'Cement and sand · Ikeja');
    expect(exploreLine(c(area: 'Ikeja')), 'Ikeja');
    expect(exploreLine(c()), '');
    expect(exploreLine(c(delivers: true, vehicle: 'bike', areas: ['Lekki', 'Ajah'], available: 'free')), 'Delivers by bike · Lekki, Ajah · Free now');
  });

  group('the screen', () {
    setUpAll(loadFonts);

    Future<(List<(String, String, Object?)>, List<LinkConversation>)> open(WidgetTester t, {Future<http.Response?> Function(http.Request)? also}) async {
      t.view.physicalSize = const Size(390, 844);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final f = Fixture('explore');
      final sent = <(String, String, Object?)>[];
      final opened = <LinkConversation>[];
      final chats = ChatsData(
        f.auth(also: (req) async {
          final own = await also?.call(req);
          if (own != null) return own;
          if (req.method == 'GET') return null;
          sent.add((req.method, req.url.path, req.body.isEmpty ? null : jsonDecode(req.body)));
          return _answer(<String, Object>{});
        }),
        now: () => f.at,
      );
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatsScreen(data: chats, onOpen: opened.add)));
      await _settle(t);
      await t.tap(find.bySemanticsLabel('Find a business to link with'));
      await t.pumpAndSettle();
      await _settle(t);
      return (sent, opened);
    }

    Future<void> tapIn(WidgetTester t, Finder f) async {
      // Down the list until its card is built, then into sight.
      final list = t.state<ScrollableState>(_list);
      while (f.evaluate().isEmpty && list.position.pixels < list.position.maxScrollExtent) {
        list.position.jumpTo((list.position.pixels + 300).clamp(0, list.position.maxScrollExtent));
        await t.pump();
      }
      await t.ensureVisible(f.first);
      await t.pump();
      await t.tap(f.first);
      await _settle(t);
    }

    testWidgets('Connect: asked at once, then Requested, and withdrawn', (t) async {
      final (sent, _) = await open(t);
      expect(find.textContaining('Near you', findRichText: true), findsOneWidget);
      await tapIn(t, find.bySemanticsLabel('Connect with Akoka Print Hub'));
      expect((sent.single.$1, sent.single.$2), ('POST', '/v1/links'));
      expect(sent.single.$3, {'business': '01a1cc22-8835-7982-af30-d991abf8ce92', 'role': 'both'});
      expect(find.text('Asked Akoka Print Hub to link'), findsOneWidget);
      expect(find.bySemanticsLabel('Connect with Akoka Print Hub'), findsNothing, reason: 'Requested now, in every section');
      await t.pump(const Duration(seconds: 5));
    });

    testWidgets('a request of ours withdrawn from its card', (t) async {
      final (sent, _) = await open(t);
      await tapIn(t, find.bySemanticsLabel('Withdraw request to Ifeoma Packaging'));
      expect(sent.single.$2, matches(RegExp(r'^/v1/links/[^/]+/cancel$')));
      expect(find.text('Request withdrawn'), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });

    testWidgets('their ask accepted from its card', (t) async {
      final (sent, _) = await open(t);
      await tapIn(t, find.bySemanticsLabel("Accept Kola's Bakery's request to link"));
      expect(sent.single.$2, matches(RegExp(r'^/v1/links/[^/]+/accept$')));
      expect(find.text("Linked with Kola's Bakery"), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });

    testWidgets('a request to link arriving while Explore is open: its card offers Accept at once', (t) async {
      final f = Fixture('explore');
      var arrived = false;
      await open(
        t,
        also: (req) async {
          if (req.method != 'GET' || req.url.path != '/v1/links') return null;
          final j = f.json('/v1/links');
          if (!arrived) (j['links'] as List).removeWhere((l) => (l as Map)['name'] == "Kola's Bakery");
          return _answer(j);
        },
      );
      final accept = find.bySemanticsLabel("Accept Kola's Bakery's request to link");
      final list = t.state<ScrollableState>(_list);
      Future<bool> seen() async {
        list.position.jumpTo(0);
        await t.pump();
        while (accept.evaluate().isEmpty && list.position.pixels < list.position.maxScrollExtent) {
          list.position.jumpTo((list.position.pixels + 300).clamp(0, list.position.maxScrollExtent));
          await t.pump();
        }
        return accept.evaluate().isNotEmpty;
      }

      expect(await seen(), isFalse, reason: 'not asked yet');
      arrived = true;
      await t.widget<ChatsScreen>(find.byType(ChatsScreen, skipOffstage: false)).data.load();
      await _settle(t);
      expect(await seen(), isTrue);
    });

    testWidgets('searched: a linked business messaged, in Explore\'s place', (t) async {
      final (_, opened) = await open(t);
      await t.enterText(find.byType(EditableText), 'rice');
      await t.pump(const Duration(milliseconds: 300));
      await _settle(t);
      await t.tap(find.bySemanticsLabel('Message Bayo Rice Mills'));
      await _settle(t);
      await t.pumpAndSettle();
      expect(opened.single.name, 'Bayo Rice Mills');
      expect(find.bySemanticsLabel('Search businesses'), findsNothing, reason: 'Explore gone');
    });

    testWidgets('a phone number: a request sent to it; nothing found says so', (t) async {
      final (sent, _) = await open(
        t,
        also: (req) async => req.url.path == '/v1/explore' && req.url.queryParameters.containsKey('q')
            ? _answer({
                'results': <Object>[],
                'near': <Object>[],
                'near_more': false,
                'fresh': <Object>[],
                'more': false,
                'delivery': <Object>[],
                'delivery_more': false,
                if (req.url.queryParameters['q'] == '08035550101') 'blind': {'kind': 'phone', 'shown': '+234 803 555 0101'},
              })
            : null,
      );
      await t.enterText(find.byType(EditableText), 'zzz');
      await t.pump(const Duration(milliseconds: 300));
      await _settle(t);
      expect(find.text('No business found for “zzz”. Only businesses that chose to be found are listed.'), findsOneWidget);
      await t.enterText(find.byType(EditableText), '08035550101');
      await t.pump(const Duration(milliseconds: 300));
      await _settle(t);
      expect(find.text('Send a link request to +234 803 555 0101'), findsOneWidget);
      await t.tap(find.bySemanticsLabel('Send request'));
      await _settle(t);
      expect(sent.single.$3, {'phone': '08035550101', 'role': 'both'});
      expect(find.text('Request sent'), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });
  });

  group('never empty', () {
    setUpAll(loadFonts);

    // A business with no links yet, asked once whether others may find it,
    // that said no.
    Future<List<(String, String, Object?)>> chatsWithNoLinks(WidgetTester t, {Object? explore}) async {
      t.view.physicalSize = const Size(390, 1200);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final f = Fixture('explore');
      final sent = <(String, String, Object?)>[];
      var findable = false;
      final chats = ChatsData(
        f.auth(also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET') {
            return switch (p) {
              '/v1/link-chats' => _answer({'conversations': <Object>[]}),
              '/v1/links' => _answer({'links': <Object>[], 'sent': <Object>[]}),
              '/v1/link-settings' => _answer({'findable': findable, 'findable_now': findable, 'asked_at': '2026-11-01T09:00:00Z'}),
              '/v1/explore' when explore != null && !req.url.queryParameters.containsKey('q') => _answer(explore),
              _ => null,
            };
          }
          sent.add((req.method, p, req.body.isEmpty ? null : jsonDecode(req.body)));
          if (p == '/v1/link-settings') findable = true;
          return _answer(<String, Object>{});
        }),
        now: () => f.at,
      );
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatsScreen(data: chats)));
      await _settle(t);
      return sent;
    }

    testWidgets('no links: businesses to link with, Connect, the switch to be found, Find a business', (t) async {
      final sent = await chatsWithNoLinks(t);
      expect(find.text('Link with the businesses you trade with'), findsOneWidget);
      expect(find.bySemanticsLabel('Connect with Akoka Print Hub'), findsOneWidget, reason: 'near us first');
      expect(find.text('Other businesses cannot find you yet.'), findsOneWidget);
      await t.tap(find.bySemanticsLabel('Let them find me'));
      await _settle(t);
      expect((sent.last.$1, sent.last.$2), ('PUT', '/v1/link-settings'));
      expect(sent.last.$3, {'findable': true});
      expect(find.text('Other businesses cannot find you yet.'), findsNothing);
      await t.tap(find.bySemanticsLabel('Connect with Akoka Print Hub'));
      await _settle(t);
      expect((sent.last.$1, sent.last.$2), ('POST', '/v1/links'));
      await t.tap(find.bySemanticsLabel('Find a business').last);
      await t.pumpAndSettle();
      expect(find.byType(ExploreScreen), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });

    testWidgets('Explore with no one to show says why, and offers to be found', (t) async {
      final sent = await chatsWithNoLinks(t, explore: {
        'results': <Object>[], 'near': <Object>[], 'near_more': false, 'fresh': <Object>[], 'more': false, 'delivery': <Object>[], 'delivery_more': false,
      });
      expect(find.bySemanticsLabel(RegExp('^Connect with')), findsNothing);
      await t.tap(find.bySemanticsLabel('Find a business').last);
      await t.pumpAndSettle();
      await _settle(t);
      expect(find.textContaining('No other business can be found yet'), findsOneWidget);
      await t.tap(find.bySemanticsLabel('Let other businesses find me'));
      await _settle(t);
      expect((sent.last.$1, sent.last.$2), ('PUT', '/v1/link-settings'));
      expect(sent.last.$3, {'findable': true});
      await t.pump(const Duration(seconds: 5));
    });

    testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
      final f = Fixture('explore');
      final chats = ChatsData(
        f.auth(also: (req) async => switch (req.url.path) {
          '/v1/link-chats' when req.method == 'GET' => _answer({'conversations': <Object>[]}),
          '/v1/links' when req.method == 'GET' => _answer({'links': <Object>[], 'sent': <Object>[]}),
          '/v1/link-settings' when req.method == 'GET' => _answer({'findable': false, 'findable_now': false, 'asked_at': '2026-11-01T09:00:00Z'}),
          _ => null,
        }),
        now: () => f.at,
      );
      await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: ChatsScreen(data: chats)), drive: _settle);
    });
  });
}
