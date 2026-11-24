// A Schools film (Episode 10, "Keep it safe, and start next term"): a clerk
// who may not see names opens Students and sees admission numbers; cut to
// Mr Ibe, who may, and sees the names; then at the end of the school year he
// promotes each class (with an exception), copies the fees from the term
// before and starts the next term. Set-up copied from school_test.dart and
// school_fixture.dart (promotion, students, sessions), the story's numbers
// and the two people in film_bh_ep910_world.dart.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_school_safe_next_term_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/school-safe-next-term --out safe.mp4 --captions --time 10:30

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_bh_ep910_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: school safe, next term', (t) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final world = BhWorld();

    Future<void> phone(bool reveal) async {
      await t.pumpWidget(const SizedBox());
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: bhHome(bhChats(world, reveal: reveal, now: DateTime.utc(2027, 7, 24, 9, 30)))));
      await bhLoad(t);
    }

    final scroll = find.byType(Scrollable).first;
    await phone(false);
    final f = Film.start(t, 'school-safe-next-term');
    f.say('Funmi, a clerk who may not see names, taps School.', who: 'Funmi · Bright Hill Academy');
    await f.hold(1.6);
    await f.tap(find.bySemanticsLabel('School').last, then: 0.5);
    await bhLoad(t);
    await f.drag(scroll, const Offset(0, -800), seconds: 0.8);
    await f.hold(0.8);
    await f.tap(find.text('Students'), then: 0.4);
    await bhLoad(t);
    f.say('Students says names hidden; each child shows as an admission number.');
    await f.hold(3.6);
    await f.tap(find.textContaining('JSS1A/014').first, then: 0.4);
    await bhLoad(t);
    f.say('Even the parent’s phone number is masked on her screen.');
    await f.hold(3.4);

    await phone(true);
    f.say('On Mr Ibe’s phone, his role may show names, so they show.');
    await f.hold(1.2);
    await f.tap(find.bySemanticsLabel('School').last, then: 0.5);
    await bhLoad(t);
    await f.drag(scroll, const Offset(0, -800), seconds: 0.8);
    await f.hold(0.6);
    await f.tap(find.text('Students'), then: 0.4);
    await bhLoad(t);
    await f.hold(3.0);
    await f.tap(find.text('Amaka Obi').first, then: 0.4);
    await bhLoad(t);
    f.say('His student page shows the full name and the parent’s phone.');
    await f.hold(3.4);

    // The year ends: promotion.
    await phone(true);
    f.say('At the end of the year, Mr Ibe taps School, then Classes, terms and fees.');
    await f.hold(1.2);
    await f.tap(find.bySemanticsLabel('School').last, then: 0.5);
    await bhLoad(t);
    await f.drag(scroll, const Offset(0, -800), seconds: 0.8);
    await f.hold(0.6);
    await f.tap(find.text('Classes, terms and fees'), then: 0.4);
    await bhLoad(t);
    await f.hold(1.6);
    f.say('He taps Promotion at the end of 2026/2027.');
    await t.ensureVisible(find.text('Promotion at the end of 2026/2027'));
    await t.pump();
    await f.hold(0.8);
    await f.tap(find.text('Promotion at the end of 2026/2027'), then: 0.4);
    await bhLoad(t);
    f.say('Each class is listed with the class it moves up to.');
    await f.hold(3.4);
    f.say('Exceptions list students; he taps one to change it.');
    await f.drag(scroll, const Offset(0, -260), seconds: 0.8);
    await f.hold(0.8);
    await f.tap(find.text('Move up').last, then: 0.5);
    await f.hold(1.0);
    f.say('He chooses Repeat, so Dami stays in Primary 4.');
    await f.tap(find.text('Repeat'), then: 0.5);
    await f.hold(2.4);
    f.say('He taps Promote 2026/2027, and confirms. It happens once.');
    await f.drag(scroll, const Offset(0, -300), seconds: 0.8);
    await f.tap(find.textContaining('Promote 2026/2027'), then: 0.5);
    await f.hold(2.2);
    await f.tap(find.text('Promote').last, then: 0.5);
    await bhLoad(t);
    await f.hold(2.8);

    // The next term.
    f.say('He goes back to Classes and terms.');
    await f.tap(find.bySemanticsLabel('Back to Classes and terms'), then: 0.5);
    await bhLoad(t);
    await f.hold(1.4);
    f.say('He taps the new year’s First term, planned, and then Fees.');
    await f.drag(scroll, const Offset(0, -300), seconds: 0.8);
    await f.tap(find.textContaining('6 Sep to').first, then: 0.5);
    await bhLoad(t);
    await f.hold(1.0);
    await f.tap(find.text('Fees'), then: 0.5);
    await bhLoad(t);
    f.say('Its fees are empty. He taps Copy from Third term.');
    await f.hold(2.4);
    await f.tap(find.text('Copy from Third term'), then: 0.5);
    await bhLoad(t);
    await f.hold(3.0);
    f.say('The fees are copied. He closes the sheet and goes back.');
    await f.hold(1.0);
    await f.tap(find.text('Cancel'), then: 0.6);
    await bhLoad(t);
    await f.hold(0.6);
    await f.tap(find.text('Classes and terms').last, then: 0.5);
    await bhLoad(t);
    f.say('He taps Start beside First term, and confirms.');
    await f.hold(0.8);
    await f.drag(scroll, const Offset(0, -300), seconds: 0.8);
    await f.hold(0.6);
    await f.tap(find.text('Start'), then: 0.5);
    await f.hold(2.0);
    await f.tap(find.text('Start it'), then: 0.5);
    await bhLoad(t);
    f.say('The term starts: every active student is enrolled, and it is Current.');
    await f.hold(3.6);
    await f.finish();
  }, skip: !filming);
}
