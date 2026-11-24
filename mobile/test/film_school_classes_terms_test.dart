// A Schools film: classes and terms (Episode 2, school-classes-terms).
// Mrs Fashola, on the School tab, opens Classes, terms and fees, adds a
// class (JSS 3, arm A), a session (2026/2027), a term (First term, with its
// dates) and starts it. Set-up copied from school_test.dart and
// school_fixture.dart; the server is film_bh_ep12_world.dart.
//
//   VIDEO_DIR=/tmp/films flutter test --timeout 40m test/film_school_classes_terms_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/school/school_tab.dart';

import 'film.dart';
import 'film_bh_ep12_world.dart';
import 'support.dart';

void main() {
  setUpAll(() async {
    await loadFonts();
    await bhLoadMaterialFonts();
  });

  testWidgets('film: school classes and terms', (t) async {
    final w = BhWorld();
    final c = bhChats(w);
    await bhOpen(t, c, (c) => SchoolTab(auth: c.auth));
    final f = Film.start(t, 'school-classes-terms');
    Finder field(String label) => find.byType(EditableText).at(const {'Name': 0, 'Arm': 1, 'Term': 0}[label]!);

    f.say('Mrs Fashola sets up Bright Hill Academy’s classes and terms.', who: 'Mrs Fashola · Bright Hill Academy');
    await f.play(2.8);
    f.say('On the School tab, scroll to Classes, terms and fees. Tap it.');
    for (var i = 0; i < 2; i++) {
      await f.drag(find.byType(Scrollable).first, const Offset(0, -560), seconds: 0.7);
      await f.hold(0.3);
    }
    await f.hold(0.6);
    await f.tap(find.text('Classes, terms and fees'), then: 2.0);

    f.say('Classes are listed. Tap the plus to add a class.');
    await f.hold(1.6);
    await f.tap(find.bySemanticsLabel('Add a class, term or session'), then: 1.6);
    f.say('Choose A class.');
    await f.hold(0.8);
    await f.tap(find.text('A class'), then: 1.4);

    f.say('Type the name, JSS 3, and the arm, A.');
    await f.tap(field('Name'), then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(field('Name'), 'JSS 3', perKey: 0.15);
    await f.hold(0.3);
    await f.tap(field('Arm'), then: 0.2);
    await f.type(field('Arm'), 'A', perKey: 0.2);
    await f.hold(0.5);
    await f.keyboard(false);
    f.say('Tap Add. JSS 3A joins the classes.');
    await f.hold(0.6);
    await f.tap(find.text('Add').last, then: 2.0);
    await f.hold(1.2);

    f.say('Now the school year. Tap the plus, then A session.');
    await f.tap(find.bySemanticsLabel('Add a class, term or session'), then: 1.4);
    await f.tap(find.text('A session (school year)'), then: 1.6);
    f.say('The year is filled in: 2026/2027. Tap Add.');
    await f.hold(2.2);
    await f.tap(find.text('Add').last, then: 2.4);

    f.say('Tap the plus again, then A term.');
    await f.tap(find.bySemanticsLabel('Add a class, term or session'), then: 1.4);
    await f.tap(find.text('A term'), then: 1.6);
    f.say('Name it First term, then choose its dates.');
    await f.tap(field('Term'), then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(field('Term'), 'First term', perKey: 0.15);
    await f.hold(0.4);
    await f.keyboard(false);
    await f.hold(0.6);

    Future<void> pick(String label, int monthsBack, String day) async {
      await f.tap(find.bySemanticsLabel(label), then: 1.2);
      for (var i = 0; i < monthsBack; i++) {
        await f.tap(find.byTooltip('Previous month'), then: 0.5);
      }
      for (var i = 0; i > monthsBack; i--) {
        await f.tap(find.byTooltip('Next month'), then: 0.5);
      }
      await f.tap(find.text(day).first, then: 0.8);
      await f.tap(find.text('OK'), then: 1.0);
    }

    f.say('Starts: 7 September. Pick the day, tap OK.');
    await pick('Starts', 2, '7');
    f.say('Ends: 18 December.');
    await pick('Ends', -1, '18');
    f.say('Fees due: 21 September.');
    await pick('Fees due', 2, '21');
    f.say('Tap Add. First term is planned, not yet started.');
    await f.hold(1.2);
    await f.tap(find.text('Add').last, then: 2.6);

    f.say('Tap Start on First term to begin it.');
    await f.tap(find.bySemanticsLabel('Start First term'), then: 1.8);
    f.say('One term is current at a time. Starting enrols every student.');
    await f.hold(2.6);
    await f.tap(find.text('Start it'), then: 1.0);
    f.say('First term has started: 418 students enrolled.');
    await f.hold(3.2);
    await f.finish();
  }, skip: !filming);
}
