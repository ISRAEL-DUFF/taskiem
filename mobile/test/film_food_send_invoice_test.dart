// A film: send an invoice (Episode 4, Get paid; Ada's Kitchen). From the Taskiem chat's tools,
// Ada opens Invoices, taps New invoice, says who it is for (Chinedu Okonkwo's office), their phone
// number, what it is for and how much, and taps Send it. Set-up as
// tools_panel_test.dart and money_sheets_test.dart; the world is film_ada_ep45_world.dart.
//
//   VIDEO_DIR=<out> flutter test test/film_food_send_invoice_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/money_sheets.dart' show InvoiceAdd;

import 'film.dart';
import 'film_ada_ep45_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: send an invoice', (t) async {
    final world = AdaWorld();
    await openAdaChat(t, world);
    final f = Film.start(t, 'food-send-invoice');
    Finder fields() => find.descendant(of: find.byType(InvoiceAdd), matching: find.byType(EditableText));

    f.say("The office lunch for Chinedu's team is eaten. Ada asks for payment.", who: 'Ada · Ada\'s Kitchen');
    await f.play(2.6);

    f.say("Ada opens the chat's tools, where her invoices live.");
    await f.tap(find.bySemanticsLabel('Open tools'), then: 1.2);
    await f.settle();
    await f.hold(0.6);

    f.say('She taps Invoices, to see what is owed and sent.');
    await f.tap(find.bySemanticsLabel(RegExp('^Invoices, ')), then: 1.2);
    await f.settle();
    await f.hold(0.8);

    f.say("Tap New invoice to make one for Chinedu's office.");
    await f.tap(find.bySemanticsLabel('New invoice'), then: 1.2);
    await f.settle();
    await f.hold(0.9);

    f.say('Who it is for: Chinedu Okonkwo, and his phone number.');
    await f.tap(fields().at(0), then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(fields().at(0), 'Chinedu Okonkwo', perKey: 0.12);
    await f.tap(fields().at(1), then: 0.2);
    await f.keyboard(true, kind: 'number', swap: true);
    await f.type(fields().at(1), '08033120456', perKey: 0.1);
    await f.hold(0.4);

    f.say('What it is for, and how much: ₦37,500.');
    await f.tap(fields().at(2), then: 0.2);
    await f.keyboard(true, kind: 'text', swap: true);
    await f.type(fields().at(2), 'Office lunch, 15 packs', perKey: 0.1);
    await f.tap(fields().at(3), then: 0.2);
    await f.keyboard(true, kind: 'number', swap: true);
    await f.type(fields().at(3), '37500', perKey: 0.2);
    await f.hold(0.6);
    await f.keyboard(false);
    await f.hold(0.6);

    f.say('Tap Send it. Taskiem texts Chinedu the invoice with a pay link.');
    await f.tap(find.bySemanticsLabel('Send it'), then: 1.0);
    await f.settle();
    await f.hold(1.6);

    await f.play(1.0);
    f.say('Back in Invoices, INV-0014 for Chinedu Okonkwo shows as Sent.');
    await f.tap(find.bySemanticsLabel('Open tools'), then: 1.2);
    await f.settle();
    await f.tap(find.bySemanticsLabel(RegExp('^Invoices, ')), then: 1.2);
    await f.settle();
    await f.hold(3.2);
    await f.finish();
    expect(world.sent.first, startsWith('POST /v1/invoices '));
  }, skip: !filming);
}
