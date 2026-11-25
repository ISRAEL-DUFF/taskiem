// A film: tasks and handover (Gadgets, Episode 6, "Your team"). Ikenna opens
// Tasks from the Team page and gives Maryam a task ("Count the chargers by
// 2pm"; the app has no due-date field, so the time is in the words). A cut to
// Maryam's phone: Today, Your tasks, she taps Done. A second cut: at closing
// she opens Open tools, Handover (the real route; Handover is a Shift
// manager's tool), types the cash she counted and taps Hand over.
// Adapted from film_food_tasks_and_handover_test.dart. Skipped unless VIDEO_DIR:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_gadgets_tasks_and_handover_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/gadgets-tasks-and-handover --out gadgets-tasks.mp4 --captions --time 13:00

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/chat/team_pages.dart';
import 'package:taskiem/chat/today_tab.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

// Thursday 12 November 2026, 13:00 in Lagos.
final _now = DateTime.utc(2026, 11, 12, 12);

void main() {
  setUpAll(loadFonts);

  var viewer = 'ikenna';
  final tasks = <Map<String, dynamic>>[];

  http.Response ok(Object b) => http.Response.bytes(utf8.encode(jsonEncode(b)), 200, headers: {'content-type': 'application/json; charset=utf-8'});

  Map<String, dynamic> me(Fixture f) {
    final m = Map<String, dynamic>.from(f.json('/v1/me'));
    m['tenant_name'] = 'Techline Gadgets';
    final owner = viewer == 'ikenna';
    m['roles'] = owner ? ['owner'] : ['shift_manager'];
    m['user'] = {...(m['user'] as Map<String, dynamic>), 'name': owner ? 'Ikenna Nwachukwu' : 'Maryam Sani', 'email': 'team@techline.example'};
    return m;
  }

  ChatsData make(Fixture f) {
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET') {
            switch (p) {
              case '/v1/me':
                return ok(me(f));
              case '/v1/taskiem-chat':
                return ok({'messages': <Object>[]});
              case '/v1/chat-alerts':
                return ok({'alerts': <Object>[]});
              case '/v1/chat-refs':
                return ok({'refs': <Object>[]});
              case '/v1/chat-briefing':
                return ok({'ready': false, 'day': '2026-11-12', 'key': 'briefing:2026-11-12', 'put_off': true});
              case '/v1/staff':
                return ok({
                  'people': [
                    {'user_id': 'U1', 'name': 'Ikenna Nwachukwu', 'phone': '+2348031230142', 'roles': ['owner'], 'chat': true, 'you': viewer == 'ikenna'},
                    {'user_id': 'U2', 'name': 'Maryam', 'phone': '+2348035550163', 'roles': ['shift_manager'], 'chat': true, 'you': viewer == 'maryam'},
                  ],
                  'invites': <Object>[],
                });
              case '/v1/staff/clock':
                return ok({
                  'day': '2026-11-12',
                  'shifts': [
                    {'id': 'S1', 'user_id': 'U2', 'name': 'Maryam', 'in_at': '2026-11-12T07:12:00Z'},
                    {'id': 'S0', 'user_id': 'U1', 'name': 'Ikenna Nwachukwu', 'in_at': '2026-11-12T06:58:00Z'},
                  ],
                  'in': <Object>[],
                  'late': <Object>[],
                });
              case '/v1/roles':
                return ok({'roles': [{'name': 'owner'}, {'name': 'cashier'}, {'name': 'shift_manager'}]});
              case '/v1/staff/tasks':
                final all = req.url.queryParameters['all'] == 'true';
                return ok({
                  'give': viewer == 'ikenna',
                  'people': [
                    {'id': 'U1', 'name': 'Ikenna Nwachukwu', 'roles': ['owner']},
                    {'id': 'U2', 'name': 'Maryam', 'roles': ['shift_manager']},
                  ],
                  'tasks': [
                    for (final x in tasks)
                      if (all || (viewer == 'maryam' ? x['to_user'] == 'U2' : x['to_user'] == 'U1')) x,
                  ],
                  'repeats': <Object>[],
                });
              case '/v1/chat/actions':
                return ok({
                  'topics': [
                    for (final (id, name, icon, color) in [('sales', 'Sales', 'sell', 'lime'), ('stock', 'Stock', 'stock', 'orange'), ('orders', 'Orders', 'orders', 'blue'), ('staff', 'Staff and tasks', 'tasks', 'blue')])
                      {'id': id, 'name': name, 'icon': icon, 'color': color, 'actions': <Object>[]},
                  ],
                });
              case '/v1/money/summary':
                return ok({'in_settled_kobo': 0, 'out_settled_kobo': 0, 'buckets': <Object>[]});
              case '/v1/money/unmatched':
                return ok({'payments': <Object>[]});
              case '/v1/storefront/orders':
                return ok({'orders': <Object>[]});
              case '/v1/runs':
                return ok({'runs': <Object>[]});
              case '/v1/dashboard':
                return ok({'runs': {'total': 0, 'failed': 0}});
              case '/v1/approvals':
                return ok({'approvals': <Object>[]});
              case '/v1/form-requests':
                return ok({'requests': <Object>[]});
              case '/v1/workflows':
                return ok({'workflows': <Object>[]});
              case '/v1/onboarding':
                return ok({'items': <Object>[], 'done': 8, 'total': 8, 'complete': true, 'dismissed': true, 'self_serve': true});
              case '/v1/chat-handover':
                return ok({
                  'day': '2026-11-12',
                  'sales': {
                    'total_kobo': 42850000,
                    'count': 11,
                    'by_how': {'cash': 18600000, 'transfer': 24250000},
                    'waiting': 0,
                  },
                  'low': ['Nova X5, 128 GB, Blue'],
                });
            }
            return null;
          }
          if (p == '/v1/staff/tasks') {
            final b = jsonDecode(req.body) as Map<String, dynamic>;
            tasks.add({
              'id': 'T1',
              'what': b['what'],
              'given_by': 'U1',
              'giver': 'Ikenna',
              'to_user': b['to_user'],
              'person': 'Maryam',
              'photo': false,
              'state': 'open',
              'created_at': '2026-11-12T12:00:00Z',
              'late': false,
            });
            return ok({'id': 'T1'});
          }
          if (p == '/v1/staff/tasks/T1/done') {
            tasks.first
              ..['state'] = 'done'
              ..['done_at'] = '2026-11-12T12:30:00Z';
            return ok({});
          }
          return ok({});
        },
      ),
      now: () => f.at,
    );
    chats.auth.me = Me.fromJson(me(f));
    return chats;
  }

  Future<void> show(WidgetTester t, Widget home) async {
    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home));
    await settle(t);
  }

  testWidgets('film: gadgets tasks and handover', (t) async {
    await t.pumpWidget(const SizedBox());
    final fx = Fixture('chats');
    viewer = 'ikenna';
    var chats = make(fx);
    await show(t, StaffPage(auth: chats.auth));
    final f = Film.start(t, 'gadgets-tasks-and-handover');

    f.say('Ikenna gives Maryam a task. On the Team page, he taps Tasks.', who: 'Ikenna · Techline Gadgets');
    await f.play(2.4);
    await f.tap(find.bySemanticsLabel(RegExp('^Tasks: ')), then: 1.4);
    await f.settle();
    f.say('On Tasks, he taps Give a task.');
    await f.hold(2.0);
    await f.tap(find.bySemanticsLabel('Give a task').first, then: 1.4);
    final what = find.descendant(of: find.bySemanticsLabel(RegExp('^What is to be done')), matching: find.byType(EditableText));
    f.say('He writes the task, with the time in the words.');
    await f.tap(what, then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(what, 'Count the chargers by 2pm', perKey: 0.09);
    await f.hold(0.4);
    await f.keyboard(false);
    f.say('He chooses who it is for: Maryam, his sales attendant.');
    await f.tap(find.bySemanticsLabel('Maryam'), then: 2.0);
    f.say('Tap Give the task. Maryam hears it in her chat.');
    await f.hold(0.6);
    await f.tap(find.text('Give the task'), then: 2.0);
    f.say('Under Everyone’s, the task is open, for Maryam.');
    await f.tap(find.bySemanticsLabel(RegExp('^Everyone’s')), then: 2.8);

    // Cut to Maryam's phone: Today, Your tasks.
    viewer = 'maryam';
    chats = make(fx);
    await show(t, TodayTab(auth: chats.auth, now: () => _now));
    f.say('This is Maryam’s phone: the Today tab. She has a task waiting.', who: 'Maryam · Techline Gadgets');
    await f.hold(2.6);
    final row = find.text('Your tasks');
    f.say('She scrolls to Your team and taps Your tasks.');
    await f.drag(find.byType(Scrollable).first, const Offset(0, -500));
    await f.drag(find.byType(Scrollable).first, const Offset(0, -500));
    await f.settle();
    await f.hold(0.6);
    await f.tap(row.first, then: 1.6);
    await f.settle();
    f.say('She counts the chargers, then taps Done on the task.');
    await f.hold(2.2);
    await f.tap(find.bySemanticsLabel('Done: Count the chargers by 2pm'), then: 2.4);

    // Cut: closing time, Maryam's chat.
    chats = make(fx)..load();
    await show(t, TaskiemScreen(data: TaskiemData(chats, follow: false)));
    f.say('At closing, Maryam opens the tools in her chat.', who: 'Maryam · Techline Gadgets');
    await f.hold(2.4);
    await f.tap(find.bySemanticsLabel('Open tools'), then: 1.2);
    await f.settle();
    f.say('She taps Handover to hand the shift over to Ikenna.');
    await f.hold(1.2);
    await f.tap(find.bySemanticsLabel(RegExp('^Handover, ')), then: 1.4);
    await f.settle();
    f.say('Taskiem has added up the shift: ₦428,500 in 11 sales.');
    await f.hold(2.8);
    final cash = find.descendant(of: find.bySemanticsLabel(RegExp('^Cash counted')), matching: find.byType(EditableText));
    f.say('She counts the cash drawer and types what is there.');
    await f.tap(cash, then: 0.2);
    await f.keyboard(true, kind: 'number');
    await f.type(cash, '185500', perKey: 0.18);
    await f.hold(0.8);
    f.say('₦500 short of the cash sold. Tap Hand over.');
    await f.hold(2.2);
    await f.keyboard(false);
    await f.tap(find.bySemanticsLabel('Hand over'), then: 2.6);
    await f.finish();
  }, skip: !filming);
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
