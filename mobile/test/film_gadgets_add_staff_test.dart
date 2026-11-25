// A film: add staff (Gadgets, Episode 6, "Your team"). Ikenna adds Maryam to Techline
// Gadgets from the Team page: Add staff, her name, her mobile number, what he
// does (Shift manager, so she can hand the shift over), Send the invitation. Then, a cut: the Team page after Maryam has joined.
// Joining is on WhatsApp, which is not a phone-app screen, so it is not drawn.
// Set-up copied from team_pages_test.dart (StaffPage on recorded answers).
//
//   VIDEO_DIR=/tmp/films flutter test test/film_gadgets_add_staff_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/gadgets-add-staff --out gadgets-add-staff.mp4 --captions

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/team_pages.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

String _in(int days) => DateTime.now().add(Duration(days: days)).toUtc().toIso8601String();

void main() {
  setUpAll(loadFonts);

  // Dami has joined (the second scene) or has not.
  var joined = false;
  var invited = false;

  Map<String, dynamic> staff() => {
    'people': [
      {'user_id': 'U1', 'name': 'Ikenna Nwachukwu', 'phone': '+2348031230142', 'roles': ['owner'], 'chat': true, 'you': true},
      if (joined) {'user_id': 'U2', 'name': 'Maryam', 'phone': '+2348035550163', 'roles': ['shift_manager'], 'chat': true},
    ],
    'invites': [
      if (invited && !joined)
        {
          'id': 'I1',
          'name': 'Maryam',
          'phone': '+2348035550163',
          'roles': ['shift_manager'],
          'inviter': 'Ikenna',
          'sent_at': '2026-11-12T09:00:00Z',
          'expires_at': _in(7),
          'created_at': '2026-11-12T09:00:00Z',
        },
    ],
  };

  Future<void> open(WidgetTester t) async {
    await t.pumpWidget(const SizedBox());
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET') {
            return switch (p) {
              '/v1/staff' => ok(staff()),
              '/v1/staff/clock' => ok({
                'day': '2026-11-12',
                'shifts': [
                  if (joined) {'id': 'S1', 'user_id': 'U2', 'name': 'Maryam', 'in_at': '2026-11-12T07:12:00Z'},
                  {'id': 'S0', 'user_id': 'U1', 'name': 'Ikenna Nwachukwu', 'in_at': '2026-11-12T06:58:00Z'},
                ],
                'in': <Object>[],
                'late': <Object>[],
              }),
              '/v1/roles' => ok({'roles': [{'name': 'owner'}, {'name': 'cashier'}]}),
              '/v1/staff/tasks' => ok({'give': true, 'people': <Object>[], 'tasks': <Object>[], 'repeats': <Object>[]}),
              _ => null,
            };
          }
          if (p == '/v1/staff/invites') {
            invited = true;
            return ok({'id': 'I1', 'sent': true});
          }
          return ok({'ok': true});
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: StaffPage(auth: chats.auth)));
    await settle(t);
  }

  testWidgets('film: gadgets add staff', (t) async {
    await open(t);
    final f = Film.start(t, 'gadgets-add-staff');
    f.say('Ikenna runs Techline Gadgets. He adds Maryam on the Team page.', who: 'Ikenna · Techline Gadgets');
    await f.play(2.8);
    f.say('Tap Add staff to invite someone to join his team.');
    await f.tap(find.bySemanticsLabel('Add staff'), then: 2.2);
    final name = find.descendant(of: find.bySemanticsLabel(RegExp('^Their name')), matching: find.byType(EditableText));
    final phone = find.descendant(of: find.bySemanticsLabel(RegExp('^Their mobile number')), matching: find.byType(EditableText));
    f.say('He types Maryam’s name, then her mobile number.');
    await f.tap(name, then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(name, 'Maryam', perKey: 0.18);
    await f.hold(0.3);
    await f.tap(phone, then: 0.2);
    await f.keyboard(true, kind: 'number', swap: true);
    await f.type(phone, '08035550163', perKey: 0.13);
    await f.hold(0.5);
    await f.keyboard(false);
    f.say('What she does: a shift manager, who also hands the shift over.');
    await f.tap(find.bySemanticsLabel('What they do: Cashier'), then: 1.2);
    await f.tap(find.text('Shift manager'), then: 1.4);
    f.say('He taps Send the invitation. Taskiem messages Maryam on WhatsApp.');
    await f.hold(1.0);
    await f.tap(find.bySemanticsLabel('Send the invitation'), then: 0.8);
    await f.hold(2.4);
    f.say('Maryam waits under Waiting to join until she says yes.');
    await f.hold(2.6);
    // Cut: Maryam has said yes on WhatsApp; the Team page as it is then.
    joined = true;
    await open(t);
    f.say('Once Maryam joins, she is on the Team page, in today.');
    await f.hold(3.2);
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
