// Search everywhere (docs/spec/menus-everywhere.md §6, E4): one box; the
// results in groups, three of each, "Show more" to the group's chip;
// invoices, orders, customers, services and saved things searched on the
// server; a saved thing shown by its name, kind and owner only; an action
// done as from Today; a chat opened by its path.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/saved_accounts.dart' show SavedAccountsPage;
import 'package:taskiem/chat/search_everywhere.dart';
import 'package:taskiem/kit/sheet.dart' show TkSheet;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'support.dart';

Map<String, dynamic> _act(String id, String title, String line, {String? shortcut}) => {
  'id': id, 'title': title, 'line': line, 'icon': 'rider', 'does': 'show', 'opens': 'show', 'shortcut': ?shortcut,
};

final _catalogue = {
  'topics': [
    {
      'id': 'riders', 'name': 'Riders', 'icon': 'rider', 'color': 'teal',
      'actions': [
        _act('riders.list', "Your riders, and who's free", 'Who is out, who can go now', shortcut: 'riders'),
        _act('riders.add', 'Add a rider', 'Name, phone, and the areas they cover', shortcut: 'add rider Musa 0803…'),
        _act('riders.today', "Today's deliveries", 'Out, done, and waiting'),
      ],
    },
    {
      'id': 'sales', 'name': 'Sales', 'icon': 'sell', 'color': 'lime',
      'actions': [_act('sales.today', "Today's sales", 'What came in today')],
    },
  ],
};

/// Saved things as the server answers a search: name, kind and owner only.
Map<String, dynamic> _saved(String id, String name, String kind, String owner, {String? kindName}) => {
  'kind': 'saved', 'id': id, 'name': name, 'when': '2026-11-20T10:00:00Z', 'saved_kind': kind, 'owner': owner, 'kind_name': ?kindName,
};

final _found = [
  _saved('m1', 'mum', 'bank_account', 'mine'),
  _saved('m2', 'mum-shop', 'place', 'business'),
  _saved('m3', 'mum-generator', 'custom', 'business', kindName: 'Generator'),
  _saved('m4', 'mums-pharmacy', 'place', 'business'),
  {'kind': 'service', 'id': 's1', 'name': "Mum's haircut", 'when': '0001-01-01T00:00:00Z', 'total_kobo': 300000, 'minutes': 30, 'status': 'available'},
];

Map<String, dynamic> _inv(int i) => {
  'kind': 'invoice', 'id': 'i$i', 'name': 'INV-${(100 + i).toString().padLeft(4, '0')}', 'party': 'Ada Rice',
  'when': '2026-11-${(10 - i % 9).toString().padLeft(2, '0')}T10:00:00Z', 'total_kobo': 450000, 'status': 'sent',
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    await t.pump(const Duration(milliseconds: 20));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 600));
  }

  testWidgets('one box: actions, chats and invoices in groups; Show more; an action done; a chat opened', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final asked = <String>[];
    final opened = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/chat/actions') {
          return http.Response.bytes(utf8.encode(jsonEncode(_catalogue)), 200, headers: {'content-type': 'application/json; charset=utf-8'});
        }
        if (req.method == 'GET' && req.url.path == '/v1/chat-refs' && req.url.query.isNotEmpty) {
          asked.add(req.url.query);
          final q = req.url.queryParameters['q'] ?? '';
          final kinds = (req.url.queryParameters['kind'] ?? '').split(',');
          final all = [for (var i = 0; i < 9; i++) _inv(i)]
              .where((r) => kinds.contains(r['kind']) && '${r['name']} ${r['party']}'.toLowerCase().contains(q.toLowerCase()))
              .toList();
          return http.Response(jsonEncode({'refs': all, 'cursor': ''}), 200);
        }
        if (req.method == 'POST' && req.url.path == '/v1/chat/actions/riders.list/run') {
          return http.Response(
            jsonEncode({
              'messages': [
                {'id': 'h2', 'channel': 'web', 'inbound': false, 'kind': 'text', 'text': 'Musa is free. Bola is out.', 'created_at': '2026-11-12T13:00:00Z'},
              ],
            }),
            200,
          );
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SearchEverywhere(chats: chats, onPath: opened.add, debounce: Duration.zero)));
    await settle(t);
    expect(find.textContaining('Search what Taskiem can do'), findsOneWidget);

    // "rider": the actions, three, and Show more; nothing on the server.
    await t.enterText(find.byType(EditableText), 'rider');
    await settle(t);
    expect(find.byKey(const ValueKey('search-action')), findsOneWidget);
    expect(find.text('Add a rider'), findsOneWidget);
    expect(asked.last, contains('q=rider'));
    // "ada": the invoices from the server, three of nine, and Show more.
    await t.enterText(find.byType(EditableText), 'ada');
    await settle(t);
    expect(find.text('Invoices and bills'), findsOneWidget);
    expect(find.textContaining(RegExp(r'^INV-01\d\d$')), findsNWidgets(3));
    await looks(t, 'search-everywhere');
    await t.tap(find.bySemanticsLabel('Show more Invoices and bills'));
    await settle(t);
    expect(find.textContaining(RegExp(r'^INV-01\d\d$'), skipOffstage: false), findsNWidgets(8), reason: 'the Invoices chip, eight');

    // An action: done, its answer in a sheet.
    await t.tap(find.text('All').first);
    await t.enterText(find.byType(EditableText), "who's free");
    await settle(t);
    await t.tap(find.text("Your riders, and who's free"));
    await settle(t);
    expect(find.byType(TkSheet), findsOneWidget);
    expect(find.text('Musa is free. Bola is out.'), findsOneWidget);
  });

  testWidgets('saved things and services: by name, kind and owner; three, then Show more; a saved thing opens its list', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final kinds = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/chat/actions') {
          return http.Response(jsonEncode({'topics': []}), 200);
        }
        if (req.method == 'GET' && req.url.path == '/v1/chat-refs' && req.url.query.isNotEmpty) {
          final kind = req.url.queryParameters['kind'] ?? '';
          kinds.add(kind);
          final q = (req.url.queryParameters['q'] ?? '').toLowerCase();
          final refs = _found.where((r) => kind.split(',').contains(r['kind']) && '${r['name']}'.toLowerCase().contains(q)).toList();
          return http.Response.bytes(utf8.encode(jsonEncode({'refs': refs, 'cursor': ''})), 200, headers: {'content-type': 'application/json; charset=utf-8'});
        }
        if (req.method == 'GET' && req.url.path == '/v1/me/saved') {
          return http.Response(jsonEncode({'saved': [], 'limit': 500}), 200);
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SearchEverywhere(chats: chats, onPath: (_) {}, debounce: Duration.zero)));
    await settle(t);
    await t.enterText(find.byType(EditableText), 'mum');
    await settle(t);
    // All asks the refs, the services and the saved things each on its own.
    expect(kinds, containsAll(['invoice,bill,order,purchase,customer', 'service', 'saved']));
    expect(find.text('Services'), findsWidgets);
    expect(find.text("Mum's haircut"), findsOneWidget);
    expect(find.text('₦3,000 · 30 min'), findsOneWidget);
    expect(find.text('@mum'), findsOneWidget);
    expect(find.text('A bank account · Yours'), findsOneWidget);
    expect(find.text('A place · The business’s'), findsOneWidget);
    expect(find.text('A generator · The business’s'), findsOneWidget);
    expect(find.text('@mums-pharmacy'), findsNothing, reason: 'three in All');
    await looks(t, 'search-everywhere-saved');
    // Show more: the Saved chip, every one.
    await t.ensureVisible(find.bySemanticsLabel('Show more Saved'));
    await t.tap(find.bySemanticsLabel('Show more Saved'));
    await settle(t);
    expect(kinds.last, 'saved');
    expect(find.text('@mums-pharmacy'), findsOneWidget);
    expect(find.text("Mum's haircut"), findsNothing);
    // A saved thing of one's own opens one's own saved things.
    await t.tap(find.text('@mum'));
    await settle(t);
    expect(find.byType(SavedAccountsPage), findsOneWidget);
    expect(t.widget<SavedAccountsPage>(find.byType(SavedAccountsPage)).mine, isTrue);
  });
}
