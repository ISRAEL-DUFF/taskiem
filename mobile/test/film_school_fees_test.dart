// A film: set the fees (Schools, Episode 4; Bright Hill Academy). Mr Ibe opens School, Classes, terms and
// fees, Fees for First term, copies JSS 1A's fees from the Third term, looks at what is required and
// what is optional, changes Tuition and saves. World: film_bh_ep34_world.dart; set-up as school_test.dart
// ("a class's fees: an amount changed and saved").
//
//   VIDEO_DIR=<out> flutter test test/film_school_fees_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/school/school_setup.dart' show ClassFeesSheet;

import 'film.dart';
import 'film_bh_ep34_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: set the fees', (t) async {
    final world = BrightHill(fresh: false);
    await world.open(t);
    final f = Film.start(t, 'school-fees');
    Finder amounts() => find.descendant(of: find.byType(ClassFeesSheet), matching: find.byType(EditableText));

    f.say('First term needs its fees. Mr Ibe starts from the School tab.', who: 'Mr Ibe · Bright Hill Academy');
    await f.play(2.4);
    await f.tap(find.text('School'), then: 1.0);
    await f.settle();
    await f.hold(0.8);

    f.say('Scroll to Classes, terms and fees, and tap it.');
    await f.drag(find.text('Go to'), const Offset(0, -300), seconds: 0.7);
    await f.hold(0.6);
    await f.tap(find.text('Classes, terms and fees'), then: 1.0);
    await f.settle();
    await f.hold(1.0);

    f.say('Tap Fees for First term, under the school year.');
    await f.tap(find.text('Fees for First term'), then: 1.0);
    await f.settle();
    await f.hold(1.6);

    f.say('JSS 1A has no fees yet. Tap Copy from Third term.');
    await f.tap(find.text('Copy from Third term'), then: 1.0);
    await f.settle();
    await f.hold(2.8);

    f.say('Last term’s items come in: three required, Transport optional.');
    await f.hold(2.4);

    f.say('Tap Transport to see who pays it.');
    await f.tap(find.bySemanticsLabel('Transport, optional: change'), then: 1.0);
    await f.settle();
    await f.hold(2.4);

    f.say('Transport is Optional. Every student pays the other items. Tap Done.');
    await f.tap(find.text('Done').last, then: 1.0);
    await f.settle();
    await f.hold(0.8);

    f.say('Tuition rises to ₦85,000. Tap its amount and type it.');
    await f.tap(amounts().first, then: 0.2);
    await f.keyboard(true, kind: 'number');
    await f.type(amounts().first, '85000', perKey: 0.2);
    await f.hold(0.6);
    await f.keyboard(false);
    await f.hold(1.8);

    f.say('Tap Save JSS 1A’s fees. Every student pays ₦102,000.');
    await f.tap(find.text('Save JSS 1A’s fees'), then: 1.0);
    await f.settle();
    await f.hold(3.0);
    await f.finish();
    expect(world.saved, isTrue);
  }, skip: !filming);
}
