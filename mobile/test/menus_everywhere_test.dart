// Menus everywhere, E2 (docs/spec/menus-everywhere.md §3, §4): the tools
// panel of a business's conversation has a second view, "What would you
// like to do?", the topics' menu; a row is done through the Taskiem chat
// (POST /v1/chat/actions/{id}/run with the conversation), its answer in a
// sheet, and what Taskiem said left here as a note only we see.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/kit/sheet.dart' show TkSheet;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'support.dart';

const _bayo = 'ef99249d-9322-40ca-91c2-e24fdd450c15';

Map<String, dynamic> _a(String id, String title, String line, {String opens = 'show', List<Map<String, dynamic>>? fields}) => {
  'id': id, 'title': title, 'line': line, 'icon': 'customers', 'does': 'show', 'opens': opens, 'fields': ?fields,
};

final _catalogue = {
  'topics': [
    {
      'id': 'owe', 'name': 'Who owes you', 'icon': 'customers', 'color': 'red',
      'actions': [
        _a('owe.list', 'Who owes you', 'Everyone, biggest first'),
        _a('owe.add', 'Someone owes you', 'Write it in the book', opens: 'questions', fields: [
          {'key': 'who', 'label': 'Who owes you?', 'kind': 'text'},
          {'key': 'amount', 'label': 'How much?', 'kind': 'money'},
        ]),
      ],
    },
    {'id': 'stock', 'name': 'Stock', 'icon': 'stock', 'color': 'orange', 'actions': [_a('stock.low', "See what's low", 'Items about to run out')]},
  ],
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 600));
  }

  testWidgets('the grid panel\'s menu: a row done through the Taskiem chat, its answer in a sheet, a note left here', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tag');
    final ran = <(String, Map<String, dynamic>)>[];
    final opened = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/chat/actions') return http.Response(jsonEncode(_catalogue), 200);
        final run = RegExp(r'^/v1/chat/actions/([a-z.]+)/run$').firstMatch(req.url.path);
        if (req.method == 'POST' && run != null) {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          ran.add((run[1]!, body));
          const words = '2 customer(s) owe you ₦61,500:\n1. Tola: ₦55,000\n2. Bisi: ₦6,500';
          return http.Response.bytes(
            utf8.encode(jsonEncode({
              'messages': [
                {'id': 'h1', 'channel': 'web', 'inbound': true, 'kind': 'text', 'text': 'Who owes you', 'created_at': '2026-11-11T12:40:00Z'},
                {'id': 'h2', 'channel': 'web', 'inbound': false, 'kind': 'text', 'text': words, 'created_at': '2026-11-11T12:40:00Z'},
              ],
              'note': {'id': 'note-1', 'link': _bayo, 'mine': true, 'kind': 'taskiem', 'asked': 'Who owes you', 'body': words, 'created_at': '2026-11-11T12:40:00Z'},
            })),
            200,
            headers: {'content-type': 'application/json; charset=utf-8'},
          );
        }
        if (req.method != 'GET') return http.Response('{}', 200);
        return null;
      }),
      now: () => f.at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _bayo);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false), onOpenLink: opened.add)));
    await settle(t);

    // The grid: Tools first; the other view, the topics.
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    expect(find.text('What would you like to do?'), findsOneWidget);
    await t.tap(find.text('What would you like to do?'));
    await settle(t);
    expect(find.text('Who owes you'), findsWidgets);
    expect(find.bySemanticsLabel('Close this menu'), findsNothing, reason: 'the panel closes it, not a ✕');
    await looks(t, 'tools-panel-menu');

    // A topic, a row: done with the conversation, the answer in a sheet.
    await t.tap(find.text('Who owes you').first);
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp(r'^Who owes you\. Everyone')));
    await settle(t);
    expect(ran.single.$1, 'owe.list');
    expect(ran.single.$2, {'link': _bayo});
    expect(find.byType(TkSheet), findsOneWidget);
    expect(find.textContaining('Tola: ₦55,000'), findsWidgets);
    await looks(t, 'tools-panel-answer');
    // The note is there already, behind the sheet.
    expect(find.byKey(const ValueKey('taskiem-note'), skipOffstage: false), findsOneWidget);
    await t.tap(find.descendant(of: find.byType(TkSheet), matching: find.text('Taskiem chat')));
    await settle(t);
    expect(opened, ['taskiem']);
    // The note, here, only ours.
    expect(find.byKey(const ValueKey('taskiem-note')), findsOneWidget);

    // A row that takes values: its form first, then done with them.
    await t.tap(find.bySemanticsLabel('Open tools'));
    await settle(t);
    expect(find.text('Stock'), findsWidgets, reason: 'opened again on the view used last');
    await t.tap(find.text('Who owes you').first);
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp(r'^Someone owes you\. ')));
    await settle(t);
    final boxes = find.descendant(of: find.byType(TkSheet), matching: find.byType(EditableText));
    await t.enterText(boxes.at(0), 'Bisi');
    await t.enterText(boxes.at(1), '6500');
    await t.pump();
    await t.tap(find.descendant(of: find.byType(TkSheet), matching: find.text('Someone owes you')).last);
    await settle(t);
    expect(ran.last.$1, 'owe.add');
    expect(ran.last.$2, {'link': _bayo, 'values': {'who': 'Bisi', 'amount': '6500'}});

    // "/" typed is the tools, never the menu, though the menu was used last.
    await t.enterText(find.byType(EditableText).first, '/');
    await settle(t);
    expect(find.text('What would you like to do?'), findsNothing);
    expect(find.text('Who owes you'), findsNothing);
  });
}
