// A Schools film (Bright Hill Academy, Episode 5): invoice the term. Mr Ibe, the
// bursar, opens School, Classes, terms and fees, First term, Invoice the term;
// reads the plan (how many, the total, each class, who is left out and why);
// looks at Keep as drafts, then chooses Send now and sends. Set-up copied from
// school_test.dart (SchoolTab with the stand-in server); the stand-in numbers
// are in film_bh_ep56_world.dart.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_school_invoice_term_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/school/school_tab.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'film_bh_ep56_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: school, invoice the term', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final chats = ChatsData(f.auth(also: (req) => bhServer(req)), now: () => f.at);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SchoolTab(auth: chats.auth)));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(seconds: 1));

    final film = Film.start(t, 'school-invoice-term');
    film.say('Mr Ibe opens School. The term is set; no bills are out yet.', who: 'Mr Ibe · Bright Hill Academy');
    await film.hold(2.6);

    film.say('He taps Classes, terms and fees.');
    final go = find.text('Classes, terms and fees');
    await t.ensureVisible(go);
    await t.pump();
    await film.hold(0.8);
    await film.tap(go, then: 0.8);
    await film.settle();

    film.say('He taps First term, the current term.');
    await film.hold(1.0);
    await film.tap(find.text('First term').first, then: 0.8);
    await film.settle();

    film.say('He taps Invoice the term.');
    await film.hold(1.2);
    await film.tap(find.text('Invoice the term'), then: 0.8);
    await film.settle();

    film.say('The plan comes first: 396 of 400 students, and the total.');
    await film.hold(3.4);

    film.say('Each class, and who is left out and why, are listed.');
    await film.hold(3.4);

    film.say('Keep as drafts holds the invoices back; nothing is sent.');
    await film.tap(find.text('Keep as drafts'), then: 2.6);

    film.say('Or Send now by SMS and email.');
    await film.tap(find.text('Send now by SMS and email'), then: 2.2);

    film.say('He taps Invoice and send 396. Each student is invoiced once.');
    await film.hold(0.8);
    await film.tap(find.text('Invoice and send 396'), then: 0.5);
    await film.play(3.0);
    await film.settle();
    await film.hold(1.0);
    await film.finish();
    expect(bhSent.single['send'], true);
  }, skip: !filming);
}
