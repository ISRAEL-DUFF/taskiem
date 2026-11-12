// Taskiem's conversation, as a person uses it: opened from the chats; its
// history (diary lines, the chart, Taskiem's named bubbles); words sent
// (shown at once with a clock, then Taskiem's answer, the ticks blue once
// answered); a reply button pressed (its id, its title as the words); a
// send that fails (the words back in the box); what Taskiem said since the
// person last looked (the unread mark, the list's count).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/alerts.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/give_task.dart';
import 'package:taskiem/chat/thread.dart';
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

  testWidgets('an answer comes in view with the cards still below it', (t) async {
    await open(t, answer: [_msg('a1', 'All *good* today.', at: '2026-11-11T13:22:01Z')]);
    await t.enterText(find.byType(EditableText), 'status');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    final thread = t.state<ThreadViewState<Object?>>(find.byWidgetPredicate((w) => w is ThreadView));
    final view = t.getRect(find.byWidgetPredicate((w) => w is ThreadView));
    final answer = t.getRect(find.textContaining('All good today.', findRichText: true));
    expect(answer.bottom, lessThanOrEqualTo(view.bottom), reason: 'the answer is in sight');
    expect(answer.top, greaterThanOrEqualTo(view.top));
    expect(thread.scroll.offset, lessThan(thread.scroll.position.maxScrollExtent - 80), reason: 'Taskiem\'s cards are left below it');
  });

  testWidgets('"Give this to…" a low stock item: who, then the task given with what it is about', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    final given = <Map<String, dynamic>>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/staff/tasks') {
          return http.Response(
            jsonEncode({
              'give': true,
              'tasks': [],
              'repeats': [],
              'people': [
                {'id': 'u-bola', 'name': 'Bola Ade', 'roles': ['cashier']},
                {'id': 'u-ada', 'name': 'Ada Obi', 'roles': ['owner']},
              ],
            }),
            200,
            headers: {'content-type': 'application/json'},
          );
        }
        if (req.method == 'POST' && req.url.path == '/v1/staff/tasks') {
          given.add(jsonDecode(req.body) as Map<String, dynamic>);
          return http.Response('{}', 200, headers: {'content-type': 'application/json'});
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await settle(t);
    final give = find.bySemanticsLabel('Give this to…: Palm oil 25L is running low');
    await t.ensureVisible(give);
    await t.pumpAndSettle();
    await t.tap(give);
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('Give this to someone'), findsOneWidget);
    expect(find.text('Restock Palm oil 25L'), findsOneWidget, reason: 'what it is about, already said');
    await t.tap(find.text('Give the task'));
    await t.pumpAndSettle();
    expect(find.text('Choose who it is for.'), findsOneWidget);
    expect(given, isEmpty);
    await t.tap(find.text('Choose…'));
    await t.pumpAndSettle();
    expect(find.text('Bola Ade'), findsOneWidget);
    expect(find.text('Any owner'), findsNothing, reason: 'not the owner\'s role');
    await t.tap(find.text('Any cashier'));
    await t.pumpAndSettle();
    await t.tap(find.text('A photo when it is done'));
    await t.pump();
    await t.tap(find.text('Give the task'));
    await settle(t);
    await t.pumpAndSettle();
    expect(given.single['what'], 'Restock Palm oil 25L');
    expect(given.single['to_role'], 'cashier');
    expect(given.single['photo'], true);
    expect(given.single['link'], {'kind': 'stock', 'id': isNotEmpty, 'name': 'Palm oil 25L'});
    expect(find.text('Give this to someone'), findsNothing);
    expect(find.text('Task given. They hear in their chat.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  test('a task\'s words and what is sent, as tasks.ts says them', () {
    expect(linkedWhat((kind: 'order', id: 'o1', name: 'ORD-7')), 'Pack order ORD-7');
    expect(linkedWhat((kind: 'stock', id: 's1', name: null)), 'Restock this item');
    expect(roleWords('shift_manager'), 'Shift manager');
    expect(roleWords('store_keeper'), 'Store keeper');
    final f = TaskForm('2026-11-11')
      ..what = ' Count the float '
      ..who = 'user:u1';
    expect(taskProblem(f), isNull);
    expect(taskRequest(f, null).toJson(), {'what': 'Count the float', 'photo': false, 'to_user': 'u1'});
    f.when = 'by';
    expect(taskProblem(f), 'Choose the time it is due.');
    f.time = '09:30';
    expect(taskRequest(f, null).toJson()['due_at'], '2026-11-11T08:30:00.000Z');
    f
      ..when = 'week'
      ..day = 'fri';
    expect(taskRequest(f, null).toJson()['repeat'], {'at': '09:30', 'day': 'fri', 'every': 'week'});
    expect(staffTile(3, 0), '3 in');
    expect(staffTile(3, 1), '3 in · 1 late');
    expect(tasksTile(0, 0), 'None due');
    expect(tasksTile(2, 1), '2 due today · 1 late');
  });

  /// Taskiem's conversation from the fixture, every request kept, [also]
  /// answering first.
  Future<List<String>> screen(WidgetTester t, {Future<http.Response?> Function(http.Request req, Fixture f)? also, ValueChanged<String>? onOpenCustomer}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    final asked = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        asked.add('${req.method} ${req.url.path}${req.url.query.isEmpty ? '' : '?${req.url.query}'}');
        return also?.call(req, f);
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false), onOpenCustomer: onOpenCustomer)));
    await settle(t);
    return asked;
  }

  http.Response json(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});

  testWidgets('G7: "Open the order" on a customer\'s draft order opens their conversation', (t) async {
    final opened = <String>[];
    await screen(
      t,
      onOpenCustomer: opened.add,
      also: (req, f) async => req.url.path == '/v1/chat-alerts'
          ? json({
              'alerts': [
                {'key': 'order_draft:c1', 'kind': 'order_draft', 'at': '2026-11-11T13:00:00Z', 'title': 'Chidi wants to order', 'line': '2 baskets of tomatoes', 'id': 'c1'},
              ],
            })
          : null,
    );
    final open = find.bySemanticsLabel('Open the order: Chidi wants to order');
    await t.ensureVisible(open);
    await t.pumpAndSettle();
    await t.tap(open);
    await t.pump();
    expect(opened, ['c1']);
  });

  testWidgets('G9: a Taskiem message held: Copy puts its words on the clipboard', (t) async {
    String? copied;
    t.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') copied = (call.arguments as Map)['text'] as String;
      return null;
    });
    await screen(t);
    final said = find.textContaining('how did we do this week?', findRichText: true);
    await t.ensureVisible(said);
    await t.pumpAndSettle();
    await t.longPress(said);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Copy'));
    await t.pump();
    expect(copied, 'how did we do this week?');
    expect(find.bySemanticsLabel('Copied'), findsOneWidget);
  });

  testWidgets('G10: "briefing" typed brings the card back, put away or not, and is not sent', (t) async {
    final asked = await screen(
      t,
      also: (req, f) async => req.url.path == '/v1/chat-briefing' && req.url.queryParameters['show'] == '1' ? json(f.json('/v1/chat-briefing')) : null,
    );
    final gotIt = find.bySemanticsLabel('Got it: put the briefing away for today');
    await t.ensureVisible(gotIt);
    await t.tap(gotIt);
    await settle(t);
    expect(find.text('Got it'), findsNothing, reason: 'put away');
    await t.enterText(find.byType(EditableText), 'Show me the briefing');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(asked, contains('GET /v1/chat-briefing?show=1'));
    expect(asked.where((a) => a == 'POST /v1/taskiem-chat'), isEmpty, reason: 'asked for by name, not said to Taskiem');
    expect(find.text('Got it'), findsOneWidget, reason: 'the card back');
    expect(asksBriefing('morning briefing!'), isTrue);
    expect(asksBriefing('what is in the briefing'), isFalse);
    expect(asksBriefing('briefings'), isFalse);
  });

  test('the cards\' words, as alerts.ts, approvals.ts and staff.ts say them', () {
    final now = DateTime.utc(2026, 11, 11, 12);
    expect(ago(now.subtract(const Duration(seconds: 30)), now), 'Just now');
    expect(ago(now.subtract(const Duration(minutes: 3)), now), '3 min ago');
    expect(ago(now.subtract(const Duration(hours: 2)), now), '2 h ago');
    expect(ago(now.subtract(const Duration(hours: 30)), now), 'Yesterday');
    expect(ago(DateTime.utc(2026, 11, 2, 9), now), '2 Nov');
    final w = subjectWords({'account_name': 'Bayo Rice Mills', 'account_number': '0123456789', 'bank': 'GTBank', 'amount_kobo': 45000000});
    expect((w.amount, w.to, w.account), ('₦450,000', 'Bayo Rice Mills', 'GTBank ••6789'));
    expect(subjectWords({'amount_kobo': 100, 'bank_code': '058', 'due_on': '2026-11-12', 'urgent': true, 'n': 1200}).rest,
        [('Bank code', '058'), ('Due on', '2026-11-12'), ('Urgent', 'Yes'), ('N', '1,200')]);
    expect(subjectWords({'total': 1500}).amount, '1,500');
    expect(subjectWords('not a map').amount, isNull);
    expect(firstName('Ada Obi', 'ada@demo.test'), 'Ada');
    expect(firstName(null, 'ada@demo.test'), 'ada');
  });

  Future<List<Map<String, dynamic>>> openApproval(WidgetTester t, String button, {bool stepUp = false}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    final decided = <Map<String, dynamic>>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path == '/v1/approvals/01a1cac4-7150-72ec-bfd6-2ac7455b506b/ok') {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          decided.add(body);
          // A rule asking for a confirmation: the server says which.
          if (stepUp && body['totp'] == null) {
            return http.Response(jsonEncode({'error': 'This needs your passkey or a code.', 'step_up': 'passkey_or_totp'}), 403);
          }
          return http.Response('{}', 200);
        }
        if (req.method == 'POST') return http.Response('{}', 200);
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    await settle(t);
    final b = find.bySemanticsLabel(RegExp('^$button: Pay Bayo'));
    await t.ensureVisible(b);
    await t.pumpAndSettle();
    await t.tap(b);
    await t.pumpAndSettle();
    return decided;
  }

  testWidgets('approving from the card: the sheet, the decision with its comment, the tick, the card gone', (t) async {
    final decided = await openApproval(t, 'Approve');
    expect(find.text('Approve it?'), findsOneWidget);
    expect(find.text('Bank code'), findsOneWidget);
    expect(find.text('First payment to this account.'), findsOneWidget);
    await t.enterText(find.byType(EditableText).last, 'Checked with Bayo');
    await t.tap(find.bySemanticsLabel('Approve'));
    await settle(t);
    expect(decided.single, {'decision': 'approved', 'comment': 'Checked with Bayo'});
    expect(find.text('Approved'), findsOneWidget, reason: 'the tick');
    await t.pump(const Duration(milliseconds: 1300));
    await t.pumpAndSettle();
    await settle(t);
    expect(find.text('Approve it?'), findsNothing);
    expect(find.bySemanticsLabel(RegExp('^Needs your OK')), findsNothing);
  });

  testWidgets('a rule asking for a code: the field comes, and the code goes with the decision', (t) async {
    final decided = await openApproval(t, 'Approve', stepUp: true);
    await t.tap(find.bySemanticsLabel('Approve'));
    await settle(t);
    expect(find.text('This needs your passkey or a code.'), findsOneWidget);
    expect(find.text('Or a code from your authenticator app'), findsOneWidget);
    await t.enterText(find.byType(EditableText).last, '12345');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Approve'));
    await settle(t);
    expect(decided.length, 1, reason: 'not five digits');
    await t.enterText(find.byType(EditableText).last, '123456');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Approve'));
    await settle(t);
    expect(decided.last, {'decision': 'approved', 'totp': '123456'});
    await t.pump(const Duration(seconds: 2));
    await t.pumpAndSettle();
  });

  testWidgets('declining: the red Decline, and Not now leaves it be', (t) async {
    var decided = await openApproval(t, 'Decline');
    expect(find.text('Decline it?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Not now'));
    await t.pumpAndSettle();
    expect(find.text('Decline it?'), findsNothing);
    expect(decided, isEmpty);
    final b = find.bySemanticsLabel(RegExp('^Decline: Pay Bayo'));
    await t.tap(b);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Decline'));
    await settle(t);
    expect(decided.single, {'decision': 'rejected'});
    expect(find.text('Declined'), findsOneWidget);
    await t.pump(const Duration(seconds: 2));
    await t.pumpAndSettle();
  });
}
