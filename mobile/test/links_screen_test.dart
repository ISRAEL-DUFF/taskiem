// Linked businesses (M5; web/src/pages/Links.tsx LinkedBusinessesPage):
// every link and request with its state, and what can be done with each;
// being found; reached from the chats' ⋯ menu.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/links_screen.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  Future<List<String>> open(WidgetTester t) async {
    // Tall enough for every link at once.
    t.view.physicalSize = const Size(390, 2400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET') return null;
        sent.add('${req.method} ${req.url.path} ${req.body}');
        return http.Response(jsonEncode(<String, Object>{}), 200, headers: {'content-type': 'application/json'});
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: LinksScreen(chats: chats)));
    await settle(t);
    return sent;
  }

  testWidgets('every link with its state and what can be done; a request answered, a link ended after yes', (t) async {
    final sent = await open(t);
    expect(find.text('Linked businesses'), findsOneWidget);
    expect(find.text("Kola's Bakery"), findsOneWidget);
    expect(find.bySemanticsLabel("Accept Kola's Bakery"), findsOneWidget);
    // The rest from a tap on the row (as drawn).
    expect(find.bySemanticsLabel(RegExp('^More for Ifeoma Packaging')), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('^More for Bayo Rice Mills')), findsOneWidget);

    await t.tap(find.bySemanticsLabel("Accept Kola's Bakery"));
    await settle(t);
    expect(sent.single, startsWith('POST /v1/links/'));
    expect(sent.single, contains('/accept'));

    // Ending a link asks first; Cancel sends nothing.
    sent.clear();
    final row = find.bySemanticsLabel(RegExp('^More for Bayo Rice Mills'));
    Future<void> end() async {
      await t.ensureVisible(row);
      await t.pumpAndSettle();
      await t.tap(row);
      await t.pumpAndSettle();
      await t.tap(find.text('End link'));
      await t.pumpAndSettle();
    }

    await end();
    expect(find.text('End the link with Bayo Rice Mills?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Cancel'));
    await t.pumpAndSettle();
    expect(sent, isEmpty);
    await end();
    await t.tap(find.bySemanticsLabel('End link').last);
    await settle(t);
    expect(sent.single, contains('/end'));
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  testWidgets('being found: the line says how, and the box turns it on or off', (t) async {
    final sent = await open(t);
    expect(find.text('Let other businesses find us'), findsOneWidget);
    await t.tap(find.textContaining(RegExp('^(On|Off)')).first);
    await settle(t);
    expect(sent.single, startsWith('PUT /v1/link-settings'));
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  testWidgets('linked businesses by name; a failed read said, not "No links yet"', (t) async {
    t.view.physicalSize = const Size(390, 2400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    var fail = false;
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (fail && req.url.path == '/v1/links') return http.Response(jsonEncode({'error': 'Links are resting.'}), 500, headers: {'content-type': 'application/json'});
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: LinksScreen(chats: chats)));
    await settle(t);
    // Linked ones, A to Z.
    final linked = [for (final l in chats.links) if (l.state == 'active') l.name]..sort((a, b) => a.toLowerCase().compareTo(b.toLowerCase()));
    final ys = [for (final n in linked) t.getTopLeft(find.text(n).first).dy];
    expect(ys, [...ys]..sort());
    // Read again as it opens: a failure shows.
    fail = true;
    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: LinksScreen(chats: chats)));
    await settle(t);
    expect(find.text('Links are resting.'), findsOneWidget);
  });

  testWidgets('no links yet: Find a business', (t) async {
    final f = Fixture('chats');
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.url.path == '/v1/links') {
          return http.Response(jsonEncode({'links': <Object>[], 'sent': <Object>[]}), 200, headers: {'content-type': 'application/json'});
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    var found = 0;
    t.view.physicalSize = const Size(390, 2400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: LinksScreen(chats: chats, onFind: (_) => found++)));
    await settle(t);
    expect(find.text('No links yet.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Find a business'));
    expect(found, 1);
  });

  // Pages H5 (g-pages-tab): a page placed under Linked businesses is a tab
  // after the screen's own; pressed, it opens the page.
  testWidgets('a page placed under Linked businesses is a tab', (t) async {
    t.view.physicalSize = const Size(390, 900);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    Map<String, Object?> summary(String name, String title, String? place) => {
      'name': name,
      'title': title,
      'icon': 'tables',
      'about': '',
      'roles': <String>[],
      'pinned': false,
      'published': true,
      'has_draft': false,
      'version': 1,
      'updated_at': '2026-11-24T10:00:00Z',
      'place': ?place,
    };
    http.Response json(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.url.path == '/v1/pages') {
          return json({
            'pages': [summary('trade-today', 'Trade today', 'links'), summary('cash', 'Cash count', 'money'), summary('tickets', 'Tickets', null)],
            'can_edit': false,
            'can_publish': false,
            'max_pinned': 5,
            'max_pages': 10,
          });
        }
        if (req.url.path == '/v1/pages/trade-today/view') {
          return json({'page': summary('trade-today', 'Trade today', 'links'), 'draft': false, 'can_edit': false, 'drawn_at': '2026-11-24T10:00:00Z', 'filters': <Object>[],
            'blocks': [{'id': 'h', 'type': 'heading', 'title': 'Orders to and from linked businesses'}]});
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: LinksScreen(chats: chats)));
    await settle(t);
    expect(find.bySemanticsLabel(RegExp('Linked businesses')), findsWidgets);
    expect(find.text('Links'), findsOneWidget);
    expect(find.text('Trade today'), findsOneWidget);
    expect(find.text('Cash count'), findsNothing);
    expect(find.text('Tickets'), findsNothing);
    await looks(t, 'links-page-tabs');
    await t.tap(find.text('Trade today'));
    await settle(t);
    expect(find.text('Orders to and from linked businesses'), findsOneWidget);
  });
}
