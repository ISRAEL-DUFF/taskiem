// A film: send an invoice (Episode 4, Get paid; Techline Gadgets). From the Taskiem chat's tools,
// Ikenna opens Invoices, taps New invoice, says who it is for (Okoro & Sons Ltd), their phone
// number, what it is for and how much, and taps Send it. Set-up as
// tools_panel_test.dart and money_sheets_test.dart; the world is film_tech_ep45_world.dart.
//
//   VIDEO_DIR=<out> flutter test test/film_gadgets_send_invoice_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/money_sheets.dart' show InvoiceAdd;

import 'film.dart';
import 'film_tech_ep45_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: send an invoice', (t) async {
    final world = TechWorld();
    await openTechChat(t, world);
    final f = Film.start(t, 'gadgets-send-invoice');
    Finder fields() => find.descendant(of: find.byType(InvoiceAdd), matching: find.byType(EditableText));

    f.say("Okoro & Sons Ltd bought ten tablets. Ikenna invoices them.", who: 'Ikenna · Techline Gadgets');
    await f.play(2.6);

    f.say("Ikenna opens the chat's tools, where his invoices live.");
    await f.tap(find.bySemanticsLabel('Open tools'), then: 1.2);
    await f.settle();
    await f.hold(0.6);

    f.say('He taps Invoices, to see what is owed and sent.');
    await f.tap(find.bySemanticsLabel(RegExp('^Invoices, ')), then: 1.2);
    await f.settle();
    await f.hold(0.8);

    f.say("Tap New invoice to make one for Okoro & Sons.");
    await f.tap(find.bySemanticsLabel('New invoice'), then: 1.2);
    await f.settle();
    await f.hold(0.9);

    f.say('Who it is for: Okoro & Sons Ltd, and their phone number.');
    await f.tap(fields().at(0), then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(fields().at(0), 'Okoro & Sons Ltd', perKey: 0.12);
    await f.tap(fields().at(1), then: 0.2);
    await f.keyboard(true, kind: 'number', swap: true);
    await f.type(fields().at(1), '08031234567', perKey: 0.1);
    await f.hold(0.4);

    f.say('What it is for, and how much: ₦1,200,000.');
    await f.tap(fields().at(2), then: 0.2);
    await f.keyboard(true, kind: 'text', swap: true);
    await f.type(fields().at(2), '10 Pulse tablets', perKey: 0.1);
    await f.tap(fields().at(3), then: 0.2);
    await f.keyboard(true, kind: 'number', swap: true);
    await f.type(fields().at(3), '1200000', perKey: 0.2);
    await f.hold(0.6);
    await f.keyboard(false);
    await f.hold(0.6);

    f.say('Tap Send it. Taskiem texts Okoro & Sons the invoice and pay link.');
    await f.tap(find.bySemanticsLabel('Send it'), then: 1.0);
    await f.settle();
    await f.hold(1.6);

    await f.play(1.0);
    f.say('Back in Invoices, INV-0014 for Okoro & Sons Ltd shows as Sent.');
    await f.tap(find.bySemanticsLabel('Open tools'), then: 1.2);
    await f.settle();
    await f.tap(find.bySemanticsLabel(RegExp('^Invoices, ')), then: 1.2);
    await f.settle();
    await f.hold(3.2);
    await f.finish();
    expect(world.sent.first, startsWith('POST /v1/invoices '));
  }, skip: !filming);
}
