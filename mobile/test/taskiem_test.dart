// Taskiem's conversation, as a person uses it: opened from the chats; its
// history (diary lines, the chart, Taskiem's named bubbles); words sent
// (shown at once with a clock, then Taskiem's answer, the ticks blue once
// answered); a reply button pressed (its id, its title as the words); a
// send that fails (the words back in the box); what Taskiem said since the
// person last looked (the unread mark, the list's count).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

Map<String, dynamic> _msg(String id, String text, {bool inbound = false, String at = '2026-11-11T13:21:00Z', List<Map<String, dynamic>>? buttons, String channel = 'web'}) => {
  'id': id,
  'channel': channel,
  'inbound': inbound,
  'kind': 'text',
  'text': text,
  'created_at': at,
  'buttons': ?buttons,
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  Future<(ChatsData, List<Map<String, dynamic>>, Completer<void>?)> open(
    WidgetTester t, {
    bool hold = false,
    int fail = 0,
    List<Map<String, dynamic>> answer = const [],
  }) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    final said = <Map<String, dynamic>>[];
    final gate = hold ? Completer<void>() : null;
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path == '/v1/taskiem-chat') {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          said.add(body);
          if (gate != null) await gate.future;
          if (fail > 0) return http.Response(jsonEncode({'error': 'Taskiem could not answer just now.'}), fail);
          return http.Response(
            jsonEncode({
              'messages': [
                // Each exchange a minute after the last, its answer a second after it.
                _msg('said-${said.length}', body['text'] as String? ?? '', inbound: true, at: '2026-11-11T13:${22 + 2 * said.length}:00Z'),
                for (final a in answer) {...a, 'id': '${a['id']}-${said.length}', 'created_at': '2026-11-11T13:${22 + 2 * said.length}:01Z'},
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
    return (chats, said, gate);
  }

  testWidgets('its history: diary lines, Taskiem named on its answer, the chart', (t) async {
    await open(t);
    expect(find.textContaining('Sent invoice INV-0003', findRichText: true), findsOneWidget);
    expect(find.text('Taskiem'), findsWidgets);
    expect(find.text('SALES, THE LAST 7 DAYS'), findsOneWidget);
    expect(find.text('₦2,871,000'), findsOneWidget);
    expect(find.text('Up 17% on the week before'), findsOneWidget);
    expect(find.bySemanticsLabel('Read'), findsOneWidget, reason: 'answered: its ticks blue');
  });

  testWidgets('words are shown at once with a clock, then Taskiem answers', (t) async {
    final (_, said, gate) = await open(t, hold: true, answer: [_msg('a1', 'All *good* today.', at: '2026-11-11T13:22:01Z')]);
    await t.enterText(find.byType(EditableText), 'status');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await t.pump();
    expect(find.bySemanticsLabel('Sending'), findsOneWidget);
    expect(find.text('typing…'), findsOneWidget, reason: 'Taskiem preparing an answer');
    gate!.complete();
    await settle(t);
    expect(said.single['text'], 'status');
    expect(find.bySemanticsLabel('Sending'), findsNothing);
    expect(find.textContaining('All good today.', findRichText: true), findsOneWidget);
    expect(find.text('typing…'), findsNothing);
  });

  testWidgets('a reply button sends its id, its title as the words; the choice is ticked', (t) async {
    final (_, said, _) = await open(t, answer: [
      _msg('q1', 'Which one?', at: '2026-11-11T13:22:01Z', buttons: [
        {'id': 'b-yes', 'title': 'Yes'},
        {'id': 'b-no', 'title': 'No'},
      ]),
    ]);
    await t.enterText(find.byType(EditableText), 'invoice');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Yes'));
    await settle(t);
    expect(said.last, {'text': 'Yes', 'reply': 'b-yes'});
    // The first question, answered, scrolled up out of sight by the second.
    expect(find.byWidgetPredicate((w) => w is Semantics && w.properties.label == 'You chose Yes'), findsOneWidget);
  });

  testWidgets('a send that fails gives the words back to the box', (t) async {
    await open(t, fail: 500);
    await t.enterText(find.byType(EditableText), 'orders today');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.text('Taskiem could not answer just now.'), findsOneWidget);
    expect(t.widget<EditableText>(find.byType(EditableText)).controller.text, 'orders today');
  });

  test('merging: by id, a code kept as sent, ours giving way to the stored copy, buttons with ids kept', () {
    TaskiemChatMessagesItem m(String id, String text, {bool inbound = false, String at = '2026-11-11T13:00:00Z', List<Map<String, dynamic>>? buttons}) =>
        TaskiemChatMessagesItem.fromJson(_msg(id, text, inbound: inbound, at: at, buttons: buttons));
    final local = TaskiemLine(m('local-1', 'hello', inbound: true, at: '2026-11-11T13:00:05Z'), local: true);
    var list = mergeTaskiem([TaskiemLine(m('a', 'hi'))], []);
    list = mergeTaskiem([...list, local], [m('s1', 'hello', inbound: true, at: '2026-11-11T13:00:06Z')]);
    expect(list.map((x) => x.id), ['a', 's1'], reason: 'the stored copy replaces ours');
    list = mergeTaskiem(list, [m(nilId, 'code 1234', at: '2026-11-11T13:01:00Z'), m(nilId, 'code 5678', at: '2026-11-11T13:02:00Z')]);
    expect(list.where((x) => x.id == nilId).length, 2);
    list = mergeTaskiem(list, [m('q', 'Pick', buttons: [{'id': 'x', 'title': 'X'}])]);
    list = mergeTaskiem(list, [m('q', 'Pick', buttons: [{'title': 'X'}])]);
    expect(list.firstWhere((x) => x.id == 'q').m.buttons!.single.id, 'x', reason: 'the copy with the ids stays');
    expect(unreadAfter(list, DateTime.utc(2026, 11, 11, 13, 0, 30))?.count, 2);
    expect(unreadAfter(list, null), isNull);
  });
}
