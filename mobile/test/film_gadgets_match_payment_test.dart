// A film: match a payment to an invoice (Episode 4, Get paid; Techline Gadgets). Okoro & Sons' transfer
// has come in naming no invoice. From the Taskiem chat's tools, Ikenna opens
// Money, its To match list, the payment, Match to an invoice, picks INV-0014,
// Match it; then Invoices shows INV-0014 as Paid. Set-up as
// tools_panel_test.dart and money_sheets_test.dart; the world is film_tech_ep45_world.dart.
//
//   VIDEO_DIR=<out> flutter test test/film_gadgets_match_payment_test.dart

import 'package:flutter_test/flutter_test.dart';

import 'film.dart';
import 'film_tech_ep45_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: match a payment', (t) async {
    final world = TechWorld(okoroInvoice: true, transferIn: true);
    await openTechChat(t, world);
    final f = Film.start(t, 'gadgets-match-payment');

    f.say("A transfer of ₦1,200,000 arrives, but names no invoice.", who: 'Ikenna · Techline Gadgets');
    await f.play(2.6);

    f.say("Ikenna opens the chat's tools, then taps Money.");
    await f.tap(find.bySemanticsLabel('Open tools'), then: 1.2);
    await f.settle();
    await f.hold(0.6);
    await f.tap(find.bySemanticsLabel(RegExp('^Money, ')), then: 1.2);
    await f.settle();
    await f.hold(0.6);

    f.say('He taps To match: money that is not on an invoice yet.');
    await f.tap(find.bySemanticsLabel(RegExp('^To match')), then: 1.0);
    await f.settle();
    await f.hold(1.0);

    f.say("He opens the payment: ₦1,200,000, TRF FROM OKORO AND SONS.");
    await f.tap(find.text('TRF FROM OKORO AND SONS'), then: 1.2);
    await f.settle();
    await f.hold(1.6);

    f.say('Tap Match to an invoice, to say what it pays.');
    await f.tap(find.bySemanticsLabel('Match to an invoice'), then: 1.0);
    await f.settle();
    await f.hold(1.0);

    f.say('Pick the invoice it pays: INV-0014, Okoro & Sons Ltd.');
    await f.tap(find.bySemanticsLabel('The invoice it pays: Choose an invoice'), then: 1.0);
    await f.settle();
    await f.hold(0.6);
    await f.tap(find.text('INV-0014 · Okoro & Sons Ltd · ₦1,200,000 to pay'), then: 0.9);
    await f.settle();

    f.say('Tap Match it, and the payment counts towards INV-0014.');
    await f.hold(0.6);
    await f.tap(find.bySemanticsLabel('Match it'), then: 1.0);
    await f.settle();
    await f.hold(1.4);
    await f.play(1.0);

    f.say('Back in Invoices, INV-0014 for Okoro & Sons Ltd now shows Paid.');
    await f.tap(find.bySemanticsLabel('Open tools'), then: 1.2);
    await f.settle();
    await f.tap(find.bySemanticsLabel(RegExp('^Invoices, ')), then: 1.2);
    await f.settle();
    await f.hold(3.0);
    await f.finish();
    expect(world.sent.any((s) => s.startsWith('POST /v1/money/entries/M9/match')), isTrue);
  }, skip: !filming);
}
