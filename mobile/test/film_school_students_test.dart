// A film: students and families (Schools, Episode 3; Bright Hill Academy). Mr Ibe, the bursar, opens the
// School tab, Students, imports a CSV, then adds Tobi Lawal by hand with his mother's phone chosen from
// Saved. World: film_bh_ep34_world.dart; set-up as school_test.dart (students, a student added from a saved person).
//
//   VIDEO_DIR=<out> flutter test test/film_school_students_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/school/students_page.dart' show AddStudentSheet;

import 'film.dart';
import 'film_bh_ep34_world.dart';
import 'saved_stub.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: students and families', (t) async {
    final world = BrightHill();
    final stub = SavedStub(mine: [savedThing('M1', 'mine', 'person', 'mrs-lawal', 'Mrs Bimpe Lawal', '••3318', {'phone': '08034503318'})]);
    pickCsv('bright-hill-students.csv');
    await world.open(t, also: (r) async => stub.answer(r));
    final f = Film.start(t, 'school-students');
    Finder fields() => find.descendant(of: find.byType(AddStudentSheet), matching: find.byType(EditableText));

    f.say('Bright Hill Academy has no students in Taskiem yet. Mr Ibe opens School.', who: 'Mr Ibe · Bright Hill Academy');
    await f.play(2.4);
    await f.tap(find.text('School'), then: 1.0);
    await f.settle();
    await f.hold(0.8);

    f.say('Tap Students, the first row under Go to.');
    await f.tap(find.text('Students'), then: 1.0);
    await f.settle();
    await f.hold(1.0);

    f.say('Tap the upload button at the top to import students.');
    await f.tap(find.bySemanticsLabel('Import students'), then: 1.0);
    await f.settle();
    await f.hold(1.0);

    f.say('Tap Choose the CSV file, then pick your list.');
    await f.tap(find.bySemanticsLabel('Choose the CSV file'), then: 1.0);
    await f.settle();
    await f.hold(2.4);

    f.say('Taskiem checks every line and groups children into families.');
    await f.hold(2.4);

    f.say('Tap Save 399 students.');
    await f.tap(find.text('Save 399 students'), then: 1.0);
    await f.settle();
    await f.hold(2.6);

    f.say('The list now holds the school. Tap the plus to add one more.');
    await f.tap(find.bySemanticsLabel('Add a student'), then: 1.0);
    await f.settle();
    await f.hold(0.9);

    f.say('Type the student: Tobi Lawal, admission number JSS1A/041.');
    await f.tap(fields().at(0), then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(fields().at(0), 'Tobi Lawal', perKey: 0.1);
    await f.tap(fields().at(1), then: 0.2);
    await f.type(fields().at(1), 'JSS1A/041', perKey: 0.1);
    await f.hold(0.5);
    await f.keyboard(false);
    await f.hold(0.5);

    f.say('His class is JSS 1A. Now the guardian: Mrs Bimpe Lawal.');
    await t.ensureVisible(fields().at(2));
    await t.pump();
    await f.hold(1.0);
    await f.tap(fields().at(2), then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(fields().at(2), 'Mrs Bimpe Lawal', perKey: 0.1);
    await f.keyboard(false);
    await f.hold(0.5);

    f.say('For her phone, tap Saved and choose Mrs Bimpe Lawal.');
    await t.ensureVisible(find.bySemanticsLabel('Pick a saved phone number for the guardian'));
    await t.pump();
    await f.hold(0.6);
    await f.tap(find.bySemanticsLabel('Pick a saved phone number for the guardian'), then: 1.0);
    await f.settle();
    await f.hold(1.2);
    await f.tap(find.text('Mrs Bimpe Lawal').last, then: 1.0);
    await f.settle();
    await f.hold(1.2);

    f.say('Her number fills in. Type her relationship: Mother.');
    for (var i = 0; i < 4 && find.text('Relationship').evaluate().isEmpty; i++) {
      await f.drag(find.text('Guardian').first, const Offset(0, -200), seconds: 0.6);
      await f.hold(0.4);
    }
    await f.tap(fields().last, then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(fields().last, 'Mother', perKey: 0.12);
    await f.keyboard(false);
    await f.hold(0.6);

    f.say('Tap Add student. Tobi opens with his family attached.');
    await f.tap(find.text('Add student'), then: 1.0);
    await f.settle();
    await f.hold(3.0);
    await f.finish();
    expect(world.sent, contains('POST /v1/schools/students'));
  }, skip: !filming);
}
