// A Schools film (Episode 9, "Know your school"): Mr Ibe, the bursar of
// Bright Hill Academy, taps School in the bottom bar: fees collected of
// expected, owing by how late; he opens the day's close, reads what does not
// reconcile, taps Fix it; then in the Taskiem chat reads the school's
// briefing. Set-up copied from school_test.dart and school_fixture.dart (the
// stand-in server, laid over with the story's numbers in
// film_bh_ep910_world.dart) and the Film patterns of film_food_*_test.dart.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_school_overview_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/school-overview --out overview.mp4 --captions --time 07:05

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_bh_ep910_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: school overview', (t) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final world = BhWorld();
    final chats = bhChats(world);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: bhHome(chats)));
    await bhLoad(t);

    final f = Film.start(t, 'school-overview');
    f.say('Mr Ibe opens Taskiem on Bright Hill’s phone, the bursar’s.', who: 'Mr Ibe · Bright Hill Academy');
    await f.hold(3.0);

    f.say('He taps School in the bottom bar.');
    await f.hold(0.8);
    await f.tap(find.bySemanticsLabel('School').last, then: 0.5);
    await bhLoad(t);
    f.say('Fees collected so far, of what the term expects.');
    await f.hold(3.4);

    f.say('Below, who owes, by how late they are.');
    await f.drag(find.byType(Scrollable).first, const Offset(0, -330), seconds: 0.9);
    await f.hold(3.2);

    f.say('At the bottom, the last day’s close. He taps it.');
    await f.drag(find.byType(Scrollable).first, const Offset(0, -500), seconds: 0.9);
    await f.hold(1.4);
    await f.tap(find.textContaining('2 problems'), then: 0.4);
    await bhLoad(t);

    f.say('The close says the day does not reconcile.');
    await f.hold(3.0);
    f.say('Each thing to fix is in words, with the amount.');
    await f.drag(find.byType(Scrollable).first, const Offset(0, -420), seconds: 0.9);
    await f.hold(3.4);

    f.say('He taps Fix it beside the cash waiting for a second person.');
    await f.hold(0.6);
    final fix = find.text('Fix it');
    await f.tap(fix.at(1), then: 0.4);
    await bhLoad(t);
    await f.hold(3.2);

    f.say('He goes back, and opens Chats, where Taskiem waits.');
    await f.tap(find.bySemanticsLabel(RegExp(r'^Back to')).last, then: 0.5);
    await f.tap(find.bySemanticsLabel(RegExp(r'^Back to')).last, then: 0.5);
    await f.tap(find.bySemanticsLabel('Chats').last, then: 0.5);
    await bhLoad(t);
    await f.hold(1.0);
    await f.tap(find.text('Taskiem'), then: 0.5);
    await bhLoad(t);
    f.say('Taskiem’s briefing: fees collected, and whether the close reconciles.');
    await f.hold(3.6);
    f.say('Tiles show who is cleared, and how many payments wait.');
    await f.drag(find.byType(Scrollable).first, const Offset(0, -260), seconds: 0.8);
    await f.hold(3.6);
    await f.finish();
  }, skip: !filming);
}
