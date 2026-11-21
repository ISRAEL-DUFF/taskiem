// Menus in Taskiem's conversation (docs/spec/chat-menus.md, M1; the web's
// pages/ChatMenu.tsx): help answered with the topics as tiles; a tile
// turns the card into that topic's menu, five rows and "All N" in a sheet;
// a row that asks is sent back as "act:<id>" with its title, and the card
// folds to its line; a newer menu folds the older. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/kit/sheet.dart' show TkSheet;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'support.dart';

Map<String, dynamic> _a(String id, String title, String line, String icon, {String opens = 'questions', String? tool}) => {
  'id': id,
  'title': title,
  'line': line,
  'icon': icon,
  'does': 'changes',
  'opens': opens,
  'tool': ?tool,
};

final _catalogue = {
  'topics': [
    {
      'id': 'sales', 'name': 'Sales', 'icon': 'sell', 'color': 'lime',
      'actions': [_a('sales.sell', 'Record a sale', 'Items, amount, and how they paid', 'sell', opens: 'form', tool: 'sell')],
    },
    {
      'id': 'riders', 'name': 'Riders', 'icon': 'rider', 'color': 'teal',
      'actions': [
        _a('riders.list', "Your riders, and who's free", 'Who is out, who can go now', 'rider', opens: 'show'),
        _a('riders.add', 'Add a rider', 'Name, phone, and the areas they cover', 'rider'),
        _a('riders.give', 'Give an order to a rider', 'Pick the order, then the rider', 'rider'),
        _a('riders.today', "Today's deliveries", 'Out, done, and waiting', 'rider', opens: 'show'),
        _a('riders.back', 'Take an order back', "From a rider who can't go", 'rider'),
        _a('riders.auto', 'Offer every order to riders', 'Riders take them as they come', 'rider'),
      ],
    },
    for (final (id, name, icon, color) in [
      ('stock', 'Stock', 'stock', 'orange'),
      ('orders', 'Orders', 'orders', 'blue'),
      ('money', 'Money', 'money', 'lime'),
      ('owe', 'Who owes you', 'customers', 'red'),
      ('staff', 'Staff and tasks', 'tasks', 'blue'),
      ('auto', 'Automations', 'workflows', 'teal'),
    ])
      {'id': id, 'name': name, 'icon': icon, 'color': color, 'actions': [_a('$id.x', 'Something', 'A line', icon)]},
  ],
};

Map<String, dynamic> _menu(String id, Map<String, dynamic> m, String at) => {
  'id': id,
  'channel': 'web',
  'inbound': false,
  'kind': 'menu',
  'text': jsonEncode(m),
  'created_at': at,
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  testWidgets('help shows the topics; a topic its rows; a row asks and the card folds', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    final said = <Map<String, dynamic>>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/chat/actions') return http.Response(jsonEncode(_catalogue), 200);
        if (req.method == 'POST' && req.url.path == '/v1/taskiem-chat') {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          said.add(body);
          final n = said.length;
          final at = '2026-11-11T13:${30 + 2 * n}:00Z';
          final answer = switch (body['text']) {
            'help' => [_menu('m$n', {'line': 'Pick a topic, or just tell me what you want in your own words.'}, at)],
            'help riders' => [_menu('m$n', {'topic': 'riders'}, at)],
            _ => [
              {'id': 'q$n', 'channel': 'web', 'inbound': false, 'kind': 'text', 'text': "What's the rider's name?", 'created_at': at},
            ],
          };
          return http.Response(
            jsonEncode({
              'messages': [
                {'id': 's$n', 'channel': 'web', 'inbound': true, 'kind': 'text', 'text': body['text'], 'created_at': at},
                ...answer,
              ],
            }),
            200,
          );
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await settle(t);

    // help: the topics, as tiles.
    await t.enterText(find.byType(EditableText), 'help');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.text('What would you like to do?'), findsOneWidget);
    expect(find.text('Who owes you'), findsOneWidget);
    await looks(t, 'taskiem-menu-topics');

    // A tile: that topic's menu in place, five rows and All 6.
    await t.ensureVisible(find.text('Riders'));
    await settle(t);
    await t.tap(find.text('Riders'));
    await settle(t);
    expect(find.text('Add a rider'), findsOneWidget);
    expect(find.text('Offer every order to riders'), findsNothing, reason: 'the sixth is behind All 6');
    await t.ensureVisible(find.text('All 6 for Riders'));
    await settle(t);
    await t.tap(find.text('All 6 for Riders'));
    await settle(t);
    expect(find.byType(TkSheet), findsOneWidget);
    expect(find.text('Offer every order to riders'), findsOneWidget);
    Navigator.of(t.element(find.byType(TkSheet))).pop();
    await settle(t);

    // help riders: a newer menu; the older folds to its line.
    await t.enterText(find.byType(EditableText), 'help riders');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.text('Show again'), findsOneWidget);
    await looks(t, 'taskiem-menu-riders');

    // A row that asks: sent back as act:, its title as the words; folded.
    await t.ensureVisible(find.text('Add a rider').last);
    await settle(t);
    await t.tap(find.text('Add a rider').last);
    await settle(t);
    expect(said.last, {'text': 'Add a rider', 'reply': 'act:riders.add'});
    expect(find.textContaining("What's the rider's name?", findRichText: true), findsOneWidget);
    expect(find.text('Show again'), findsNWidgets(2));
  });
}
