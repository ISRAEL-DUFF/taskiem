// A Schools film (Bright Hill Academy, Episode 6): get paid. Mr Ibe opens a
// student (Tobi Lawal) and sees his own account number; records Mr Danjuma's
// cash for Dayo at the desk, an amount at or above the owner's amount, so it
// waits for a second person; then the film cuts to Mrs Fashola's phone, where
// the Taskiem chat has the Approve card, and she approves. Set-up copied from
// school_test.dart (SchoolTab, StudentPage, the cash sheet, the chat card) and
// film_food_approve_a_payment_test.dart (the chat and its card).
//
//   VIDEO_DIR=/tmp/films flutter test test/film_school_get_paid_test.dart

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/school/school_tab.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_bh_ep56_world.dart';
import 'support.dart';

http.Response _ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});

void main() {
  setUpAll(loadFonts);

  testWidgets('film: school, get paid', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final chats = ChatsData(f.auth(also: (req) => bhServer(req, invoiced: true)), now: () => f.at);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SchoolTab(auth: chats.auth)));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(seconds: 1));

    final film = Film.start(t, 'school-get-paid');
    film.say('Mr Ibe, the bursar, opens School and taps Students.', who: 'Mr Ibe · Bright Hill Academy');
    await film.hold(2.2);
    final students = find.text('Students');
    await t.ensureVisible(students.last);
    await t.pump();
    await film.hold(0.6);
    await film.tap(students.last, then: 0.8);
    await film.settle();

    film.say('He taps Tobi Lawal, a JSS 1A student.');
    await film.hold(1.2);
    await film.tap(find.text('Tobi Lawal'), then: 0.8);
    await film.settle();

    film.say('Tobi has his own account number. A transfer lands on him.');
    await film.hold(1.0);
    await film.drag(find.text('Tobi Lawal').first, const Offset(0, -150));
    await film.hold(3.2);

    film.say('Send to the family shares the number with his parent.');
    await film.tap(find.bySemanticsLabel('Send the account number to the family'), then: 2.6);
    await film.settle();

    film.say('Back in Students, Mr Danjuma has brought cash for Dayo.');
    await film.tap(find.bySemanticsLabel('Back to Students'), then: 1.2);
    await film.settle();
    await film.hold(0.8);
    await film.tap(find.text('Dayo Danjuma'), then: 0.8);
    await film.settle();

    film.say('He taps Record cash on Dayo’s page.');
    await film.hold(1.6);
    await film.tap(find.text('Record cash'), then: 0.8);
    await film.settle();

    film.say('He types the amount: sixty thousand naira.');
    final amount = find.byType(EditableText).first;
    await film.tap(amount, then: 0.2);
    await film.keyboard(true, kind: 'number');
    await film.type(amount, '60000', perKey: 0.25);
    await film.hold(0.6);
    await film.keyboard(false);
    await film.hold(0.4);

    film.say('Over the owner’s amount, so a second person must approve.');
    await film.hold(3.6);

    film.say('He taps Post the payment. It is sent for approval.');
    await film.tap(find.text('Post the payment'), then: 0.5);
    await film.play(3.0);
    await film.settle();
    await film.hold(0.6);

    // Cut to Mrs Fashola's phone: the Taskiem chat of the school's owner.
    final g = Fixture('chat-taskiem');
    var open = true;
    final auth = g.auth(also: (req) async {
      if (req.method == 'POST' && req.url.path.startsWith('/v1/schools/approvals/')) {
        open = false;
        bhSent.add(jsonDecode(req.body) as Map<String, dynamic>);
        return _ok({});
      }
      if (req.method != 'GET') return null;
      return switch (req.url.path) {
        '/v1/chat-alerts' => _ok({'alerts': open ? [bhApprovalAlert] : []}),
        '/v1/taskiem-chat' => _ok({'messages': []}),
        '/v1/chat-briefing' => _ok({'ready': false, 'day': '2026-11-11', 'key': 'briefing:2026-11-11', 'put_off': true, 'part': 'afternoon', 'now': '13:15'}),
        _ => null,
      };
    });
    final j = g.json('/v1/me');
    j['tenant_name'] = 'Bright Hill Academy';
    j['user'] = {...(j['user'] as Map<String, dynamic>), 'name': 'Tolulope Fashola', 'email': 'tolu@brighthill.example'};
    auth.me = Me.fromJson(j);
    final taskiem = ChatsData(auth, now: () => g.at)..load();
    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(taskiem, follow: false))));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(seconds: 1));

    film.say('Cut to Mrs Fashola’s phone. Her Taskiem chat shows Needs your OK.', who: 'Mrs Fashola · the proprietor');
    final approve = find.text('Approve');
    await t.ensureVisible(approve.first);
    await t.pump();
    await film.hold(3.8);

    film.say('The card says who asks, what for, and how much.');
    await film.hold(3.0);

    film.say('She taps Approve to open the details.');
    await film.tap(approve.first, then: 1.0);
    await film.settle();
    await film.hold(2.6);

    film.say('She is the second person. She taps Approve.');
    await film.tap(find.text('Approve').last, then: 0.5);
    await film.play(2.0);
    await film.settle();
    film.say('Approved. The card clears; the cash counts and a receipt goes out.');
    await film.hold(3.0);
    await film.finish();
    expect(bhSent.last['decision'], 'approved');
  }, skip: !filming);
}
