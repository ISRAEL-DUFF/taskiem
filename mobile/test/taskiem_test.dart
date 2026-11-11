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
import 'package:taskiem/chat/alerts.dart';
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
    await t.ensureVisible(find.bySemanticsLabel('Yes'));
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

  testWidgets('the cards: Later puts one off, Send reminder sends and puts it off a day, Got it puts the briefing away', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    final posts = <String, List<Object?>>{};
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST') {
          posts.putIfAbsent(req.url.path, () => []).add(req.body.isEmpty ? null : jsonDecode(req.body));
          return http.Response('{}', 200);
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await settle(t);
    final later = find.bySemanticsLabel('Later: A payment of ₦37,500 is not matched yet');
    await t.ensureVisible(later);
    await t.tap(later);
    await settle(t);
    expect(posts['/v1/chat-alerts/later']!.single, {'key': 'unmatched:2d7bbc40-0d31-460b-af3e-9a833cc5cae8'});
    final remind = find.bySemanticsLabel('Send reminder: Invoice INV-0002 is overdue');
    await t.ensureVisible(remind);
    await t.tap(remind);
    await settle(t);
    expect(posts['/v1/invoices/a3da21d9-9d94-4443-bade-0a95d57bf3f4/send'], hasLength(1));
    expect(posts['/v1/chat-alerts/later']!.last, {'key': 'overdue:a3da21d9-9d94-4443-bade-0a95d57bf3f4', 'hours': 24});
    final gotIt = find.bySemanticsLabel('Got it: put the briefing away for today');
    await t.ensureVisible(gotIt);
    await t.tap(gotIt);
    await settle(t);
    expect(posts['/v1/chat-alerts/later']!.last, {'key': 'briefing:2026-11-11', 'hours': 24});
    expect(find.byType(BriefingCard), findsOneWidget);
    expect(find.text('Got it'), findsNothing, reason: 'put away');
  });

  test('the cards\' words, as alerts.ts, approvals.ts and staff.ts say them', () {
    final now = DateTime.utc(2026, 11, 11, 12);
    expect(ago(now.subtract(const Duration(seconds: 30)), now), 'Just now');
    expect(ago(now.subtract(const Duration(minutes: 3)), now), '3 min ago');
    expect(ago(now.subtract(const Duration(hours: 2)), now), '2 h ago');
    expect(ago(now.subtract(const Duration(hours: 30)), now), 'Yesterday');
    expect(ago(DateTime.utc(2026, 11, 2, 9), now), '2 Nov');
    final w = subjectWords({'account_name': 'Bayo Rice Mills', 'account_number': '0123456789', 'bank': 'GTBank', 'amount_kobo': 45000000});
    expect(w, (amount: '₦450,000', to: 'Bayo Rice Mills', account: 'GTBank ••6789'));
    expect(subjectWords({'total': 1500}).amount, '1,500');
    expect(subjectWords('not a map'), (amount: null, to: null, account: null));
    expect(firstName('Ada Obi', 'ada@demo.test'), 'Ada');
    expect(firstName(null, 'ada@demo.test'), 'ada');
  });
}
