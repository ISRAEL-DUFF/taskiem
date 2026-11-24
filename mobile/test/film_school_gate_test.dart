// A Schools film: resumption day at the gate (Episode 8, Bright Hill Academy).
// Mr Ibe opens School, Cleared (Clearance), picks a class, opens Amaka Obi and
// clears her by an override with a reason (the app's own Clear button); then
// the film cuts to Mallam Audu's phone, signed in with the gate role only,
// where the gate screen is the whole app: a name, then CLEARED or SEE THE
// BURSAR, never an amount. Set-up copied from school_test.dart; the answers
// that change after a send are in film_bh_ep78_world.dart.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_school_gate_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/give_task.dart' show TkPillChoice;
import 'package:taskiem/kit/mobile.dart' show TkChips;
import 'package:taskiem/main.dart';
import 'package:taskiem/school/school_clearance.dart' show GateScreen;
import 'package:taskiem/school/school_tab.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_bh_ep78_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: school, resumption day at the gate', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final world = BhWorld()
      ..planState = 'approved' // Episode 7's plan for Tobi Lawal, approved
      ..planInstalments = [{'amount_kobo': 5100000, 'due_on': '2026-11-27'}, {'amount_kobo': 5100000, 'due_on': '2027-01-08'}];
    final auth = f.auth(also: world.server);
    bhMe(auth, f);
    final chats = ChatsData(auth, now: () => f.at);
    await bhOpen(t, TaskiemApp(mode: TkMode.light, home: bhHome(SchoolTab(auth: chats.auth))));
    await t.tap(find.bySemanticsLabel('School'));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump(const Duration(milliseconds: 300));
    }
    await t.pump(const Duration(seconds: 1));

    final film = Film.start(t, 'school-gate');
    film.say('Resumption day. Mr Ibe opens School and taps the Cleared tile.', who: 'Mr Ibe, bursar · Bright Hill Academy');
    await film.hold(2.8);
    await film.tap(find.text('341 of 418'), then: 1.0);
    await film.settle();
    await film.hold(0.6);

    film.say('Clearance counts who is cleared. It is worked out, never set by hand.');
    await film.hold(3.6);

    film.say('He taps JSS 1A to see one class.');
    await film.tap(find.widgetWithText(TkPillChoice, 'JSS 1A'), then: 0.8);
    await film.settle();
    await film.hold(3.0);

    film.say('Amaka owes a balance, but her family met the principal.');
    await film.hold(0.4);
    await film.tap(find.text('Amaka Obi'), then: 1.0);
    await film.settle();
    await film.hold(1.2);

    film.say('Under Clearance he taps Clear.');
    await film.drag(find.byType(Scrollable).first, const Offset(0, -700), seconds: 0.8);
    await film.hold(1.6);
    await film.tap(find.bySemanticsLabel('Clear by override'), then: 1.0);
    await film.settle();
    await film.hold(2.0);

    film.say('He types the reason. It is kept with his name.');
    final box = find.byType(EditableText);
    await film.tap(box.last, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(box.last, 'Principal agreed; fees by Friday', perKey: 0.07);
    await film.hold(0.8);
    await film.keyboard(false);
    await film.hold(1.2);

    film.say('He leaves the day empty, so it lasts to the end of term.');
    await film.hold(2.6);
    film.say('He taps Clear them.');
    await film.tap(find.text('Clear them'), then: 0.8);
    await film.settle();
    await film.hold(3.4);

    film.say('Amaka is cleared, with the reason and the day on record.');
    await film.hold(3.0);

    film.say('Back in Clearance, she now sits under Cleared.');
    await film.drag(find.byType(Scrollable).first, const Offset(0, 900), seconds: 0.6);
    await film.tap(find.bySemanticsLabel('Back to Students'), then: 1.0);
    await film.settle();
    await film.hold(0.8);
    await film.tap(find.descendant(of: find.byType(TkChips<String>), matching: find.text('Cleared')), then: 0.8);
    await film.hold(3.4);

    // Cut: Mallam Audu's phone. He has the gate role only; the gate is the app.
    film.say("Cut to the gate. Mallam Audu's phone shows only the gate screen.", who: 'Mallam Audu, gateman · Bright Hill Academy');
    final gate = f.auth(also: world.server);
    bhMe(gate, f, gate: true);
    final gchats = ChatsData(gate, now: () => f.at);
    await t.pumpWidget(TaskiemApp(key: UniqueKey(), mode: TkMode.light, home: GateScreen(auth: gchats.auth, onSignOut: () {})));
    await film.hold(3.2);

    film.say('A child arrives. He types the first letters, then taps the name.');
    final q = find.byType(EditableText);
    await film.tap(q, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(q, 'ama', perKey: 0.25);
    await film.hold(1.4);
    await film.tap(find.text('Amaka Obi'), then: 0.3);
    await film.keyboard(false);
    await film.hold(0.6);

    film.say('CLEARED: come in. A name and a class, never an amount.');
    await film.hold(3.8);

    film.say('He taps Check another child, and types the next name.');
    await film.tap(find.text('Check another child'), then: 0.8);
    await film.tap(q, then: 0.2);
    await film.keyboard(true, kind: 'text');
    await film.type(q, 'kem', perKey: 0.25);
    await film.hold(1.4);
    await film.tap(find.text('Kemi Bello'), then: 0.3);
    await film.keyboard(false);
    await film.hold(0.6);

    film.say('SEE THE BURSAR: she is sent to the office. No amount.');
    await film.hold(4.0);
    await film.finish();
  }, skip: !filming);
}
