// A film: tasks and handover (Episode 6, "Your team"). Zainab gives Dami a task
// on the Tasks page ("Finish 12 aprons by Friday"); on Dami's phone she marks
// it Done; at the end of her shift she hands the shift over: what was sold, the
// cash she counted, and how far short or over it is.
// Set-up copied from team_pages_test.dart (TasksPage) and sell_sheet_test.dart
// (HandoverSheet) on recorded answers. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_tasks_and_handover_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/tasks-and-handover --out tasks.mp4 --captions

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

  // Whose phone it is: Zainab's (gives tasks) or Dami's.
  var viewer = 'zainab';
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
                'give': viewer == 'zainab',
                'people': [
                  {'id': 'U1', 'name': 'Zainab Yusuf', 'roles': ['owner']},
                  {'id': 'U2', 'name': 'Dami', 'roles': ['shift_manager']},
                ],
                'tasks': [
                  for (final x in tasks)
                    if (all || (viewer == 'dami' ? x['to_user'] == 'U2' : x['to_user'] == 'U1')) x,
                ],
                'repeats': <Object>[],
              });
            }
            if (p == '/v1/chat-handover') {
              return ok({
                'day': '2026-11-12',
                'sales': {
                  'total_kobo': 8650000,
                  'count': 7,
                  'by_how': {'cash': 5200000, 'transfer': 3450000},
                  'waiting': 0,
                },
                'low': ['Ankara fabric, 6 yards'],
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
              'giver': 'Zainab',
              'to_user': b['to_user'],
              'person': 'Dami',
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

  testWidgets('film: tasks and handover', (t) async {
    var chats = await open(t);
    final f = Film.start(t, 'tasks-and-handover');

    f.say('Zainab gives Dami a task. On Tasks, she taps Give a task.', who: 'Zainab · Zuri Atelier');
    await f.play(2.4);
    await f.tap(find.bySemanticsLabel('Give a task').first, then: 1.4);
    final what = find.descendant(of: find.bySemanticsLabel(RegExp('^What is to be done')), matching: find.byType(EditableText));
    f.say('She writes what is to be done, and by when.');
    await f.tap(what, then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(what, 'Finish 12 aprons by Friday', perKey: 0.09);
    await f.hold(0.4);
    await f.keyboard(false);
    f.say('She chooses who it is for: Dami, her apprentice.');
    await f.tap(find.bySemanticsLabel('Dami'), then: 1.2);
    f.say('Tap Give the task. Dami hears it in her chat.');
    await f.hold(0.6);
    await f.tap(find.text('Give the task'), then: 2.0);
    f.say('Under Everyone’s, the task is open, for Dami.');
    await f.tap(find.bySemanticsLabel(RegExp('^Everyone’s')), then: 2.8);

    // Cut to Dami's phone.
    viewer = 'dami';
    chats = await open(t);
    f.say('On Dami’s phone, the task is waiting. She taps Done when finished.', who: 'Dami · Zuri Atelier');
    await f.hold(2.6);
    await f.tap(find.bySemanticsLabel('Done: Finish 12 aprons by Friday'), then: 2.4);

    f.say('At the end of her shift, Dami hands the shift over.');
    final context = t.element(find.byType(TasksPage));
    showTkSheet<void>(context, (_) => HandoverSheet(auth: chats.auth));
    await f.play(1.6);
    f.say('Taskiem has added up the shift: ₦86,500 sold in 7 sales.');
    await f.hold(2.6);
    final cash = find.byType(EditableText).first;
    f.say('She counts the cash drawer and types what is there.');
    await f.tap(cash, then: 0.2);
    await f.keyboard(true, kind: 'number');
    await f.type(cash, '51500', perKey: 0.18);
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
