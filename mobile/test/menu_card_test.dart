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
import 'package:taskiem/chat/menu_card.dart' show ActionFormSheet;
import 'package:taskiem/chat/tool_sheet.dart' show ToolSheet;
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
        {
          ..._a('riders.add', 'Add a rider', 'Name, phone, and the areas they cover', 'rider'),
          'fields': [
            {'key': 'name', 'label': "What's the rider's name?", 'kind': 'text'},
            {'key': 'phone', 'label': "What's their phone number?", 'kind': 'phone'},
            {'key': 'areas', 'label': 'Which areas do they cover?', 'kind': 'zone', 'optional': true},
          ],
        },
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
        if (req.method == 'GET' && req.url.path == '/v1/chat/actions/riders.add/choices') {
          return http.Response(jsonEncode({'fields': {'areas': ['Ikeja', 'Yaba']}}), 200);
        }
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
    // Said something else: the older folds to what happened, no way back in.
    expect(find.text('Show again'), findsNothing);
    expect(find.text('Riders · Not used'), findsOneWidget);
    await looks(t, 'taskiem-menu-riders');

    // A row that takes values: its form (M4), the areas offered as chips;
    // sent as form:, the values as the words; the card folds.
    await t.ensureVisible(find.text('Add a rider').last);
    await settle(t);
    await t.tap(find.text('Add a rider').last);
    await settle(t);
    await t.pump(const Duration(milliseconds: 600)); // the sheet arrives
    expect(find.byType(ActionFormSheet), findsOneWidget);
    expect(find.text('Which areas do they cover? (optional)'), findsOneWidget);
    final boxes = find.descendant(of: find.byType(ActionFormSheet), matching: find.byType(EditableText));
    await t.enterText(boxes.at(0), 'Musa Bello');
    await t.enterText(boxes.at(1), '08031234567');
    await t.tap(find.text('Yaba'));
    await settle(t);
    await looks(t, 'taskiem-menu-form');
    await t.tap(find.descendant(of: find.byType(ActionFormSheet), matching: find.text('Add a rider')).last);
    await settle(t);
    expect(said.last['reply'], startsWith('form:riders.add:'));
    expect(jsonDecode((said.last['reply'] as String).substring('form:riders.add:'.length)), {'name': 'Musa Bello', 'phone': '08031234567', 'areas': 'Yaba'});
    expect(said.last['text'], 'Add a rider: Musa Bello, 08031234567, Yaba');
    expect(find.text('Riders · You chose Add a rider'), findsOneWidget);
    expect(find.text('Riders · Not used'), findsOneWidget);
  });

  testWidgets('a menu closed with its ✕ folds to "Closed, nothing done", told to the server; one the server kept as chosen stays so', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    final closed = <String, Object?>{};
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/chat/actions') return http.Response(jsonEncode(_catalogue), 200);
        if (req.method == 'POST' && req.url.path.startsWith('/v1/taskiem-chat/menus/')) {
          closed[req.url.path] = req.body.isEmpty ? null : jsonDecode(req.body);
          return http.Response('', 204);
        }
        if (req.method == 'GET' && req.url.path == '/v1/taskiem-chat') {
          return http.Response(
            jsonEncode({
              'messages': [
                _menu('m0', {'topic': 'riders', 'done': 'chose', 'chose': 'riders.list'}, '2026-11-11T13:20:00Z'),
                {'id': 'i1', 'channel': 'web', 'inbound': true, 'kind': 'text', 'text': 'help', 'created_at': '2026-11-11T13:29:00Z'},
                _menu('m1', {'line': 'Pick a topic.'}, '2026-11-11T13:30:00Z'),
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
    expect(find.text("Riders · You chose Your riders, and who's free"), findsOneWidget);
    expect(find.text('What would you like to do?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Close this menu'));
    await settle(t);
    expect(find.text('What would you like to do?'), findsNothing);
    expect(find.text('Topics · Closed, nothing done'), findsOneWidget);
    expect(closed.keys, ['/v1/taskiem-chat/menus/m1/close']);
    await looks(t, 'taskiem-menu-closed');
  });

  testWidgets('review fixes: a failed menu tries again; a picked row keeps its values; an add row opens the add form', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    var fail = true;
    final catalogue = {
      'topics': [
        {
          'id': 'sales', 'name': 'Sales', 'icon': 'sell', 'color': 'lime',
          'actions': [
            {
              ..._a('sales.sell', 'Record a sale', 'Items, amount, and how they paid', 'sell', opens: 'form', tool: 'sell'),
              'fields': [
                {'key': 'what', 'label': 'What did you sell?', 'kind': 'text'},
                {'key': 'amount', 'label': 'For how much in all?', 'kind': 'money'},
              ],
            },
            {..._a('products.add', 'Add a product', 'Name, price, photo, sizes', 'products', opens: 'form', tool: 'products'), 'add': true},
          ],
        },
      ],
    };
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/chat/actions') {
          return fail ? http.Response(jsonEncode({'error': 'try again'}), 503) : http.Response(jsonEncode(catalogue), 200);
        }
        if (req.method == 'POST' && req.url.path == '/v1/taskiem-chat') {
          return http.Response(
            jsonEncode({
              'messages': [
                _menu('p1', {'topic': 'sales', 'pick': 'sales.sell', 'values': {'what': '3 bags of rice'}, 'line': 'About Sales.'}, '2026-11-11T13:40:00Z'),
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
    await t.enterText(find.byType(EditableText), 'I sold 3 bags of rice');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);

    // The catalogue could not be read: said, and tried again.
    expect(find.text('The menu could not be loaded.'), findsOneWidget);
    fail = false;
    await t.tap(find.text('Try again'));
    await settle(t);
    expect(find.text('Record a sale'), findsOneWidget);

    // The picked row with values: its form with them in, not the sell tool.
    await t.ensureVisible(find.text('Record a sale'));
    await settle(t);
    await t.tap(find.text('Record a sale'));
    await settle(t);
    await t.pump(const Duration(milliseconds: 600));
    expect(find.byType(ActionFormSheet), findsOneWidget);
    expect(find.text('3 bags of rice'), findsOneWidget);
    Navigator.of(t.element(find.byType(ActionFormSheet))).pop();
    await settle(t);
    await t.pump(const Duration(milliseconds: 600));

    // An add row: the tool's sheet, opened on its add form (the form above
    // was let go unsent, so the card is still open).
    await t.ensureVisible(find.text('Add a product'));
    await settle(t);
    await t.tap(find.text('Add a product'));
    await settle(t);
    expect(find.byWidgetPredicate((w) => w is ToolSheet && w.add), findsOneWidget);
  });
}
