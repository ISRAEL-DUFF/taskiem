// A Schools film: open your school (Episode 1, school-open). Only what the
// phone app has: the School settings page (the Schools switch, the second
// person), the Team page's Add staff with a role (Bursar, Gate), and the
// gate screen a gate-only member lands on. Turning Schools on and making the
// roles are done on the web; the app cannot do them. Set-up copied from
// school_test.dart and film_food_add_staff_test.dart.
//
//   VIDEO_DIR=/tmp/films flutter test --timeout 40m test/film_school_open_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/team_pages.dart';
import 'package:taskiem/school/school_clearance.dart';
import 'package:taskiem/school/school_tab.dart';

import 'film.dart';
import 'film_bh_ep12_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: school open', (t) async {
    final w = BhWorld();
    var c = bhChats(w);
    await bhOpen(t, c, (c) => SchoolTab(auth: c.auth));
    final f = Film.start(t, 'school-open');

    f.say('Mrs Fashola owns Bright Hill Academy. Schools is on for her.', who: 'Mrs Fashola · Bright Hill Academy');
    await f.play(2.8);
    f.say('Scroll the School tab down to School settings. Tap it.');
    for (var i = 0; i < 2; i++) {
      await f.drag(find.byType(Scrollable).first, const Offset(0, -560), seconds: 0.7);
      await f.hold(0.3);
    }
    await f.tap(find.text('School settings'), then: 2.0);
    f.say('The Schools switch is on: students, fees, clearance, the gate.');
    await f.hold(3.2);
    f.say('Below it, cash at or above ₦20,000 waits for a second person.');
    await f.hold(3.4);

    // Cut: the Team page (Today, Staff).
    c = bhChats(w);
    await bhOpen(t, c, (c) => StaffPage(auth: c.auth));
    f.say('On Today, Staff opens the Team page. Tap Add staff.');
    await f.hold(1.8);
    await f.tap(find.bySemanticsLabel('Add staff'), then: 2.0);

    Finder name() => find.descendant(of: find.bySemanticsLabel(RegExp('^Their name')), matching: find.byType(EditableText));
    Finder phone() => find.descendant(of: find.bySemanticsLabel(RegExp('^Their mobile number')), matching: find.byType(EditableText));

    Future<void> invite(String who, String number, String role) async {
      await f.tap(name(), then: 0.2);
      await f.keyboard(true, kind: 'text');
      await f.type(name(), who, perKey: 0.12);
      await f.hold(0.3);
      await f.tap(phone(), then: 0.2);
      await f.keyboard(true, kind: 'number', swap: true);
      await f.type(phone(), number, perKey: 0.1);
      await f.hold(0.4);
      await f.keyboard(false);
      f.say('Tap What they do, then choose $role.');
      await f.hold(0.6);
      await f.tap(find.bySemanticsLabel(RegExp('^What they do: ')), then: 1.4);
      await f.hold(1.0);
      await f.tap(find.text(role), then: 1.2);
      f.say('Tap Send the invitation. Taskiem messages them on WhatsApp.');
      await f.hold(1.0);
      await f.tap(find.bySemanticsLabel('Send the invitation'), then: 2.2);
    }

    f.say('Mr Ibe, the bursar: type his name and mobile number.');
    await invite('Chukwudi Ibe', '08035550148', 'Bursar');
    f.say('Next, Mallam Audu, the gateman, with the Gate role.');
    await f.tap(find.bySemanticsLabel('Add staff'), then: 1.6);
    await invite('Mallam Audu', '08035550171', 'Gate');
    f.say('Both wait under Waiting to join until they say yes.');
    await f.hold(3.0);

    // Cut: Mallam Audu's own phone, once he has joined and signed in.
    c = bhChats(w, gateOnly: true);
    await bhOpen(t, c, (c) => GateScreen(auth: c.auth, onSignOut: () {}));
    f.say('On Mallam Audu’s phone the app opens on the gate screen only.');
    await f.hold(3.6);
    await f.finish();
  }, skip: !filming);
}
