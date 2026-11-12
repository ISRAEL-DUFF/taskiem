// The team (web Staff.tsx, StaffPerson.tsx, Tasks.tsx; lib/team.ts,
// lib/tasks.ts): what adding, removing, a person's profile and the task
// steps send, and what each screen says.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/team_pages.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

final _now = DateTime.utc(2026, 11, 12, 12);

final _staff = {
  'people': [
    {
      'user_id': 'U1',
      'name': 'Ada Obi',
      'email': 'ada@demo.test',
      'roles': ['owner'],
      'chat': true,
      'you': true,
    },
    {
      'user_id': 'U2',
      'name': 'Tunde',
      'phone': '+2348031110000',
      'roles': ['cashier'],
      'chat': true,
    },
  ],
  'invites': [
    {
      'id': 'I1',
      'name': 'Bisi',
      'phone': '+2348032220000',
      'roles': ['shift_manager'],
      'inviter': 'Ada',
      'sent_at': '2026-11-11T09:00:00Z',
      'expires_at': '2026-11-18T09:00:00Z',
      'created_at': '2026-11-11T09:00:00Z',
    },
  ],
};

final _clock = {
  'day': '2026-11-12',
  'shifts': [
    {'id': 'S1', 'user_id': 'U2', 'name': 'Tunde', 'in_at': '2026-11-12T07:25:00Z'},
  ],
  'in': [
    {'id': 'S1'},
  ],
  'late': [
    {'user_id': 'U3', 'name': 'Chidi', 'due': '08:00'},
  ],
};

final _person = {
  'user_id': 'U2',
  'name': 'Tunde',
  'phone': '+2348031110000',
  'roles': ['cashier'],
  'chat': true,
  'profile': {
    'title': 'Cashier',
    'started_on': '2026-01-05',
    'note': '',
    'pay_kobo': 8000000,
    'pay_per': 'month',
    'rota': [
      {'day': 'mon', 'start': '08:00', 'end': '16:00'},
      {'day': 'tue', 'start': '08:00', 'end': '16:00'},
      {'day': 'sat', 'start': '10:00', 'end': '18:00'},
    ],
  },
};

final _figures = {
  'period': 'week',
  'minutes': 485,
  'shifts': 1,
  'late_days': ['2026-11-10'],
  'missed_days': [],
  'sales': {
    'count': 4,
    'total_kobo': 1200000,
    'by_how': {'cash': 700000, 'transfer': 500000},
  },
  'handovers': [
    {'at': '2026-11-12T16:00:00Z', 'diff_kobo': -50000},
  ],
  'orders': 2,
  'tasks': {'done': 3, 'done_late': 1, 'cant': 0, 'open': 1, 'late': 0},
};

Map<String, dynamic> _tasks({bool give = true}) => {
  'give': give,
  'people': [
    {
      'id': 'U2',
      'name': 'Tunde',
      'roles': ['cashier'],
    },
  ],
  'tasks': [
    {
      'id': 'T1',
      'what': 'Count the cash float',
      'given_by': 'U1',
      'giver': 'Ada',
      'to_user': 'U1',
      'person': 'Ada',
      'due_at': '2026-11-12T15:00:00Z',
      'photo': false,
      'state': 'open',
      'created_at': '2026-11-12T08:00:00Z',
      'late': false,
    },
    {
      'id': 'T2',
      'what': 'Sweep the shop',
      'given_by': 'U1',
      'giver': 'Ada',
      'to_role': 'cashier',
      'photo': true,
      'state': 'open',
      'created_at': '2026-11-12T08:00:00Z',
      'late': true,
    },
  ],
  'repeats': [
    {'id': 'R1', 'what': 'Open the shutters', 'to_role': 'cashier', 'photo': false, 'every': 'day', 'at': '07:00'},
  ],
};

void main() {
  setUpAll(loadFonts);

  test('the team and tasks in words', () {
    expect(staffProblem('', '0803', 'cashier'), 'Give their name.');
    expect(staffProblem('Bisi', '0803', 'cashier'), 'Give their mobile number, like 0803 123 4567.');
    expect(staffProblem('Bisi', '0803 123 4567', 'cashier'), isNull);
    expect(personSub(_staff['people']![1] as Map<String, dynamic>), 'Cashier · +2348031110000');
    expect(rotaWords((_person['profile']! as Map)['rota'] as List<Map<String, dynamic>>), 'Mon to Tue, 08:00 to 16:00; Sat, 10:00 to 18:00');
    expect(payWords(_person['profile']! as Map<String, dynamic>), '₦80,000 a month');
    expect(lengthWords(const Duration(minutes: 485)), '8 hours 5 minutes');
    expect(shiftWords({'in_at': '2026-11-12T07:25:00Z'}), 'Since 08:25');
    expect(
      shiftWords({'in_at': '2026-11-12T07:25:00Z', 'out_at': '2026-11-12T15:30:00Z', 'out_how': 'handover'}),
      '08:25 to 16:30 · 8 hours 5 minutes · handed over',
    );
    final rows = figureRows(_figures);
    expect(rows, contains(('Sales', '₦12,000 in 4 sales (Cash ₦7,000, Transfer ₦5,000)')));
    expect(rows, contains(('Handovers', '1, ₦500 short')));
    expect(rows, contains(('Time keeping', 'Late on 1 day')));
    expect(rows.last, ('Tasks', '3 done (1 late), 1 open'));
    final t2 = (_tasks()['tasks'] as List)[1] as Map<String, dynamic>;
    expect(taskWho(t2), 'any cashier');
    expect(taskSub(t2, true, _now), 'for any cashier · photo when done · from Ada');
    expect(dueWords('2026-11-12T15:00:00Z', _now), 'by 16:00');
    expect(dueWords('2026-11-13T15:00:00Z', _now), 'by tomorrow 16:00');
    expect(taskBadge(t2)?.text, 'Late');
    expect(repeatWords({'every': 'week', 'day': 'mon', 'at': '09:00'}), 'Every Monday at 09:00');
  });

  Future<List<String>> open(WidgetTester t, Widget Function(ChatsData c) home) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 2600);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET') {
            return switch (p) {
              '/v1/staff' => ok(_staff),
              '/v1/staff/clock' => ok(_clock),
              '/v1/roles' => ok({
                'roles': [
                  {'name': 'owner'},
                  {'name': 'cashier'},
                  {'name': 'packer'},
                ],
              }),
              '/v1/staff/U2' => ok(_person),
              '/v1/staff/U2/figures' => ok({..._figures, 'period': req.url.queryParameters['period']}),
              '/v1/staff/tasks' => ok(_tasks()),
              _ => null,
            };
          }
          sent.add('${req.method} $p ${req.body}');
          if (p == '/v1/staff/invites') return ok({'id': 'I9', 'sent': true});
          if (p.endsWith('/send')) return ok({'sent': true});
          return ok({'ok': true});
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('staff: who is in, who is late; someone added with their own role; an invitation sent again', (t) async {
    final sent = await open(t, (c) => StaffPage(auth: c.auth));
    expect(find.text('Since 08:25'), findsOneWidget);
    expect(find.text('Not in yet'), findsOneWidget);
    expect(find.text('Ada Obi (you)'), findsOneWidget);
    expect(find.bySemanticsLabel('Remove Ada Obi'), findsNothing, reason: 'not yourself');
    await t.tap(find.bySemanticsLabel('Send again to Bisi'));
    await settle(t);
    expect(sent.last, 'POST /v1/staff/invites/I1/send {}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Add staff'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Their name')), matching: find.byType(EditableText)), 'Kemi');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Their mobile number')), matching: find.byType(EditableText)), '0803 555 1234');
    await t.tap(find.bySemanticsLabel('What they do: Cashier'));
    await t.pumpAndSettle();
    await t.tap(find.text('Packer'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Send the invitation'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/staff/invites {"name":"Kemi","phone":"0803 555 1234","roles":["packer"]}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Remove Tunde'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Remove').last);
    await settle(t);
    expect(sent.last, 'POST /v1/staff/U2/remove {}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a person: their hours and pay; this month’s figures; a Saturday taken off', (t) async {
    final sent = await open(t, (c) => StaffPersonPage(auth: c.auth, user: 'U2'));
    expect(find.text('Mon to Tue, 08:00 to 16:00; Sat, 10:00 to 18:00'), findsOneWidget);
    expect(find.text('₦80,000 a month'), findsOneWidget);
    expect(find.text('₦12,000 in 4 sales (Cash ₦7,000, Transfer ₦5,000)'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('This month'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Change Tunde'));
    await t.pumpAndSettle();
    await t.tap(find.text('Saturday'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    await t.pumpAndSettle();
    expect(
      sent.last,
      'PUT /v1/staff/U2/profile {"title":"Cashier","started_on":"2026-01-05","note":"","pay_kobo":8000000,"pay_per":"month","rota":[{"day":"mon","start":"08:00","end":"16:00"},{"day":"tue","start":"08:00","end":"16:00"}]}',
    );
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('tasks: done; taken from a role and done with a photo; can’t with why; everyone’s, reminded and stopped', (t) async {
    final was = pickTaskPhoto;
    pickTaskPhoto = () async => (bytes: [1, 2, 3], name: 'shop.jpg');
    addTearDown(() => pickTaskPhoto = was);
    final sent = await open(t, (c) => TasksPage(auth: c.auth, now: () => _now));
    expect(find.textContaining('by 16:00'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Done: Count the cash float'));
    await settle(t);
    expect(sent.last, 'POST /v1/staff/tasks/T1/done {}');
    expect(find.text('Done. Thank you.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('I’ll do it: Sweep the shop'));
    await settle(t);
    expect(sent.last, 'POST /v1/staff/tasks/T2/take {}');
    await t.tap(find.bySemanticsLabel('Done, with a photo: Sweep the shop'));
    await settle(t);
    expect(sent.last, startsWith('POST /v1/staff/tasks/T2/done '), reason: 'the photo sent with it');
    await t.tap(find.bySemanticsLabel('Can’t: Count the cash float'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Why')), matching: find.byType(EditableText)), 'No float today');
    await t.tap(find.bySemanticsLabel('Send it back'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/staff/tasks/T1/cant {"why":"No float today"}');
    await t.tap(find.bySemanticsLabel('Everyone’s'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Remind any cashier: Sweep the shop'));
    await settle(t);
    expect(sent.last, 'POST /v1/staff/tasks/T2/remind {}');
    expect(find.text('Open the shutters'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Stop: Open the shutters'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Stop it'));
    await settle(t);
    expect(sent.last, 'DELETE /v1/staff/task-repeats/R1 ');
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async => switch (req.url.path) {
          '/v1/staff' => ok(_staff),
          '/v1/staff/clock' => ok(_clock),
          '/v1/staff/U2' => ok(_person),
          '/v1/staff/U2/figures' => ok(_figures),
          '/v1/staff/tasks' => ok(_tasks()),
          _ => null,
        },
      ),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: StaffPage(auth: chats.auth),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: StaffPersonPage(auth: chats.auth, user: 'U2'),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: TasksPage(auth: chats.auth, now: () => _now),
      ),
      drive: settle,
    );
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
