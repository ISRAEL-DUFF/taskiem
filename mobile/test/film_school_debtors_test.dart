// A Schools film: chase what is owed (Episode 7, Bright Hill Academy). Mr Ibe
// opens School, Debtors and reminders; reads the ages; filters 7 to 14 days;
// chooses two families; the bar names the reminder their lateness calls for
// (2, Firmer); sends it; opens Chidi Okafor and records a promise; opens Tobi
// Lawal and approves the payment plan his mother asked for in fewer parts.
// Set-up copied from school_test.dart (the app's own stand-in server); the
// answers that change after a send are in film_bh_ep78_world.dart.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_school_debtors_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/give_task.dart' show TkPillChoice;
import 'package:taskiem/main.dart';
import 'package:taskiem/school/school_tab.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_bh_ep78_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: school, chase what is owed', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final world = BhWorld();
    final auth = f.auth(also: world.server);
    bhMe(auth, f);
    final chats = ChatsData(auth, now: () => f.at);
    await bhOpen(t, TaskiemApp(mode: TkMode.light, home: bhHome(SchoolTab(auth: chats.auth))));
    // Before the film: the person is already on the School tab.
    await t.tap(find.bySemanticsLabel('School'));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump(const Duration(milliseconds: 300));
    }
    await t.pump(const Duration(seconds: 1));

    final film = Film.start(t, 'school-debtors');
    film.say('Mr Ibe opens School: collected so far, and what is still owed.', who: 'Mr Ibe, bursar · Bright Hill Academy');
    await film.hold(3.2);

    film.say('He scrolls down and taps Debtors and reminders.');
    await film.drag(find.byType(Scrollable).first, const Offset(0, -520), seconds: 0.8);
    await film.hold(1.2);
    final row = find.text('Debtors and reminders');
    await t.ensureVisible(row);
    await t.pump();
    await film.tap(row, then: 1.0);
    await film.settle();
    await film.hold(0.6);

    film.say('Families are aged by how late they are, from the bars down.');
    await film.hold(3.4);

    film.say('He taps the 7 to 14 days bar to see only those families.');
    await film.tap(find.bySemanticsLabel(RegExp('^7 to 14:')), then: 0.8);
    await film.settle();
    await film.hold(2.2);

    film.say('He ticks the box beside two families to choose them.');
    await film.tap(find.bySemanticsLabel('Choose Tobi Lawal'), then: 0.9);
    await film.tap(find.bySemanticsLabel('Choose Chidi Okafor'), then: 1.2);

    film.say('The bar names the reminder their lateness calls for: Firmer.');
    await film.hold(3.4);

    film.say('He taps Send, checks the reminder, and taps Send to 2.');
    await film.tap(find.text('Send'), then: 0.8);
    await film.settle();
    await film.hold(3.0);
    await film.tap(find.text('Send to 2'), then: 0.8);
    await film.settle();
    film.say('Both families are told. Each row now shows Reminder 2.');
    await film.hold(3.4);

    film.say('Mr Okafor replies with a day. Mr Ibe taps his name.');
    await film.tap(find.text('Chidi Okafor'), then: 1.0);
    await film.settle();
    await film.hold(1.8);

    film.say('He taps A promise, and picks the day Mr Okafor named.');
    await film.tap(find.bySemanticsLabel('Record a promise to pay'), then: 1.0);
    await film.settle();
    await film.hold(1.6);
    await film.tap(find.byType(TkPillChoice).at(2), then: 1.4);

    film.say('He taps Record the promise.');
    await film.tap(find.text('Record the promise'), then: 0.8);
    await film.settle();
    await film.hold(1.0);

    film.say('Under Chasing: the reminder sent, and the promise open.');
    await film.drag(find.byType(Scrollable).first, const Offset(0, -620), seconds: 0.8);
    await film.hold(3.6);

    film.say('He goes back to Debtors, and taps Tobi Lawal.');
    await film.drag(find.byType(Scrollable).first, const Offset(0, 900), seconds: 0.6);
    await film.hold(0.6);
    await film.tap(find.bySemanticsLabel('Back to Students'), then: 1.0);
    await film.settle();
    await film.hold(1.0);
    await film.tap(find.text('Tobi Lawal'), then: 1.0);
    await film.settle();
    await film.hold(1.2);

    film.say('His mother asked for a payment plan. It waits under Chasing.');
    await film.drag(find.byType(Scrollable).first, const Offset(0, -620), seconds: 0.8);
    await film.hold(3.0);
    film.say('He taps the plan: three parts of ₦34,000 were asked.');
    await film.tap(find.text('A payment plan asked for'), then: 1.0);
    await film.settle();
    await film.hold(3.4);

    film.say('He taps 2 to approve it in fewer parts.');
    await film.tap(find.bySemanticsLabel(RegExp(r'^2$')).first, then: 0.8);
    await film.hold(3.2);

    film.say('Same total, two parts of ₦51,000. He taps Approve the plan.');
    await film.tap(find.text('Approve the plan'), then: 0.8);
    await film.settle();
    await film.hold(1.2);

    film.say('The family is told, and Tobi now shows On a payment plan.');
    await film.hold(3.8);
    await film.finish();
  }, skip: !filming);
}
