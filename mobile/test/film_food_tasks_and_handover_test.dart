// A film: tasks and handover (Food, Episode 6, "Your team"). Ada gives Tunde a
// task on the Tasks page ("Restock the drinks fridge by 2pm"; the app has no
// due-date field, so the time is in the words); on Tunde's phone he marks it
// Done; at closing he hands the shift over: what was sold, the cash he counted,
// and how far short or over it is.
// Set-up copied from team_pages_test.dart (TasksPage) and sell_sheet_test.dart
// (HandoverSheet) on recorded answers. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_food_tasks_and_handover_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/food-tasks-and-handover --out food-tasks.mp4 --captions

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/sell_sheet.dart';
import 'package:taskiem/chat/team_pages.dart';
import 'package:taskiem/kit/sheet.dart' show showTkSheet;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

// Thursday 12 November 2026, 13:00 in Lagos.
final _now = DateTime.utc(2026, 11, 12, 12);

void main() {
  setUpAll(loadFonts);

  // Whose phone it is: Ada's (gives tasks) or Tunde's.
  var viewer = 'ada';
  final tasks = <Map<String, dynamic>>[];

  Future<ChatsData> open(WidgetTester t) async {
    await t.pumpWidget(const SizedBox());
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET') {
            if (p == '/v1/staff/tasks') {
              final all = req.url.queryParameters['all'] == 'true';
              return ok({
                'give': viewer == 'ada',
                'people': [
                  {'id': 'U1', 'name': 'Ada Obi', 'roles': ['owner']},
                  {'id': 'U2', 'name': 'Tunde', 'roles': ['cashier']},
                ],
                'tasks': [
                  for (final x in tasks)
                    if (all || (viewer == 'tunde' ? x['to_user'] == 'U2' : x['to_user'] == 'U1')) x,
                ],
                'repeats': <Object>[],
              });
            }
            if (p == '/v1/chat-handover') {
              return ok({
                'day': '2026-11-12',
                'sales': {
                  'total_kobo': 14850000,
                  'count': 23,
                  'by_how': {'cash': 9600000, 'transfer': 5250000},
                  'waiting': 0,
                },
                'low': ['Rice, 25kg bag'],
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
              'giver': 'Ada',
              'to_user': b['to_user'],
              'person': 'Tunde',
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
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TasksPage(auth: chats.auth, now: () => _now, backLabel: 'Back to Team')));
    await settle(t);
    return chats;
  }

  testWidgets('film: food tasks and handover', (t) async {
    var chats = await open(t);
    final f = Film.start(t, 'food-tasks-and-handover');

    f.say('Ada gives Tunde a task. On Tasks, she taps Give a task.', who: 'Ada · Ada’s Kitchen');
    await f.play(2.4);
    await f.tap(find.bySemanticsLabel('Give a task').first, then: 1.4);
    final what = find.descendant(of: find.bySemanticsLabel(RegExp('^What is to be done')), matching: find.byType(EditableText));
    f.say('She writes the task, with the time in the words.');
    await f.tap(what, then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(what, 'Restock the drinks fridge by 2pm', perKey: 0.09);
    await f.hold(0.4);
    await f.keyboard(false);
    f.say('She chooses who it is for: Tunde, her cashier.');
    await f.tap(find.bySemanticsLabel('Tunde'), then: 2.0);
    f.say('Tap Give the task. Tunde hears it in his chat.');
    await f.hold(0.6);
    await f.tap(find.text('Give the task'), then: 2.0);
    f.say('Under Everyone’s, the task is open, for Tunde.');
    await f.tap(find.bySemanticsLabel(RegExp('^Everyone’s')), then: 2.8);

    // Cut to Tunde's phone.
    viewer = 'tunde';
    chats = await open(t);
    f.say('On Tunde’s phone, the task is waiting. He taps Done when finished.', who: 'Tunde · Ada’s Kitchen');
    await f.hold(2.6);
    await f.tap(find.bySemanticsLabel('Done: Restock the drinks fridge by 2pm'), then: 2.4);

    f.say('At closing, Tunde hands the shift over to Ada.');
    final context = t.element(find.byType(TasksPage));
    showTkSheet<void>(context, (_) => HandoverSheet(auth: chats.auth));
    await f.play(2.2);
    f.say('Taskiem has added up the shift: ₦148,500 sold in 23 sales.');
    await f.hold(2.6);
    final cash = find.byType(EditableText).first;
    f.say('He counts the cash drawer and types what is there.');
    await f.tap(cash, then: 0.2);
    await f.keyboard(true, kind: 'number');
    await f.type(cash, '95500', perKey: 0.18);
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
