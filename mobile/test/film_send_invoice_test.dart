// A film: send an invoice (Episode 4, Get paid). From the Taskiem chat's tools,
// Zainab opens Invoices, taps New invoice, says who it is for, their phone
// number, what it is for and how much, and taps Send it. Set-up as
// tools_panel_test.dart and money_sheets_test.dart; the world is film_zuri_world.dart.
//
//   VIDEO_DIR=<out> flutter test test/film_send_invoice_test.dart

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/money_sheets.dart' show InvoiceAdd;

import 'film.dart';
import 'film_zuri_world.dart';
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('film: send an invoice', (t) async {
    final world = ZuriWorld();
    await openZuriChat(t, world);
    final f = Film.start(t, 'send-invoice');
    Finder fields() => find.descendant(of: find.byType(InvoiceAdd), matching: find.byType(EditableText));

    f.say("Nneka's outfit is done. Zainab asks for the money.", who: 'Zainab · Zuri Atelier');
    await f.play(2.6);

    f.say("Zainab opens the chat's tools, where her invoices live.");
    await f.tap(find.bySemanticsLabel('Open tools'), then: 1.2);
    await f.settle();
    await f.hold(0.6);

    f.say('She taps Invoices, to see what is owed and sent.');
    await f.tap(find.bySemanticsLabel(RegExp('^Invoices, ')), then: 1.2);
    await f.settle();
    await f.hold(0.8);

    f.say('Tap New invoice to make one for Nneka.');
    await f.tap(find.bySemanticsLabel('New invoice'), then: 1.2);
    await f.settle();

    f.say('Who it is for: Nneka Eze, and her phone number.');
    await f.tap(fields().at(0), then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(fields().at(0), 'Nneka Eze', perKey: 0.12);
    await f.tap(fields().at(1), then: 0.2);
    await f.keyboard(true, kind: 'number', swap: true);
    await f.type(fields().at(1), '08034120098', perKey: 0.1);
    await f.hold(0.4);

    f.say('What it is for, and how much: ₦85,000.');
    await f.tap(fields().at(2), then: 0.2);
    await f.keyboard(true, kind: 'text', swap: true);
    await f.type(fields().at(2), 'Ankara dress', perKey: 0.1);
    await f.tap(fields().at(3), then: 0.2);
    await f.keyboard(true, kind: 'number', swap: true);
    await f.type(fields().at(3), '85000', perKey: 0.2);
    await f.hold(0.6);
    await f.keyboard(false);
    await f.hold(0.6);

    f.say('Tap Send it. Taskiem texts Nneka the invoice with a pay link.');
    await f.tap(find.bySemanticsLabel('Send it'), then: 1.0);
    await f.settle();
    await f.hold(1.6);

    await f.play(1.0);
    f.say('Back in Invoices, INV-0008 for Nneka Eze shows as Sent.');
    await f.tap(find.bySemanticsLabel('Open tools'), then: 1.2);
    await f.settle();
    await f.tap(find.bySemanticsLabel(RegExp('^Invoices, ')), then: 1.2);
    await f.settle();
    await f.hold(3.2);
    await f.finish();
    expect(world.sent.first, startsWith('POST /v1/invoices '));
  }, skip: !filming);
}
