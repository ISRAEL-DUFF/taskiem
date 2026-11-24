// Search everywhere (docs/spec/menus-everywhere.md §6, E4): one box; the
// results in groups, three of each, "Show more" to the group's chip;
// invoices, orders and customers searched on the server; an action done as
// from Today; a chat opened by its path.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
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
          final all = [for (var i = 0; i < 9; i++) _inv(i)].where((r) => '${r['name']} ${r['party']}'.toLowerCase().contains(q.toLowerCase())).toList();
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
}
