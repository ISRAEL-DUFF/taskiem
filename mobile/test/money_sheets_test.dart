// The money tools' rows in their sheets (M8 part W; web ChatMoney.tsx,
// ChatBuyer.tsx BillView): the words, and what each button sends.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/money_sheets.dart';
import 'package:taskiem/chat/words.dart' show CardTone;
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

final _invoice = {
  'id': 'I1',
  'number': 'INV-0042',
  'status': 'sent',
  'total_kobo': 4500000,
  'paid_kobo': 1000000,
  'due_kobo': 3500000,
  'due_on': '2026-11-20',
  'customer': {'name': 'Bisi Okafor'},
  'lines': [
    {'description': 'Catering for 50', 'quantity': 1, 'unit_kobo': 4500000},
  ],
};

final _bill = {
  'id': 'B1',
  'reference': 'INV-0104',
  'supplier_name': 'Bayo Rice Mills',
  'status': 'open',
  'amount_kobo': 2000000,
  'paid_kobo': 0,
  'due_kobo': 2000000,
  'sent_kobo': 0,
  'disputed': false,
  'pay_account_number': '0123456789',
  'pay_bank_name': 'Wema',
  'lines': [
    {'description': 'Rice 50kg', 'quantity': 2, 'unit_kobo': 1000000},
  ],
};

void main() {
  setUpAll(loadFonts);

  test('the words, as invoices.ts, bills.ts and chatmoney.ts say them', () {
    expect(dateLabel('2026-11-20'), '20 Nov 2026');
    expect(dateLabel('soon'), '');
    expect(invoiceStatus('part_paid'), (label: 'Part-paid', tone: CardTone.warn));
    expect(owing(_invoice), isTrue);
    expect(owing({..._invoice, 'status': 'draft'}), isFalse);
    final a = invoiceActions('sent', (p) => p == 'invoice.manage');
    expect([a.send, a.sendAgain, a.voids, a.amend], [false, true, true, true]);
    expect(billStatus('disputed').label, 'Queried');
    expect(billHero(_bill).sub, 'of ₦20,000 · ₦0 paid');
    expect(sentLine({..._bill, 'sent_kobo': 2000000, 'paid_kobo': 500000}), 'You sent ₦20,000 from Taskiem; Bayo Rice Mills has counted ₦5,000 so far.');
    expect(canPay(_bill), isTrue);
    expect(canPay({..._bill, 'pay_account_number': null}), isFalse);
    final e = {'id': 'E1', 'kind': 'payment', 'direction': 'in', 'amount_kobo': 450000, 'status': 'settled', 'left_kobo': 450000, 'to_match': true, 'when': '2026-11-12T09:00:00Z'};
    expect(entryName(e), 'Payment');
    expect(entryAmount(e), '+₦4,500');
    expect(entryPill(e)?.text, 'To match');
    expect(matchable(e), isTrue);
    expect(towardsWords(e), 'Not matched to an invoice or order yet.');
    expect(entryProblem(kind: 'expense', amount: '', description: 'x', date: ''), 'Give the amount.');
    expect(entryProblem(kind: 'expense', amount: '12000', description: '', date: ''), 'Say what it was for.');
    expect(entryProblem(kind: 'expense', amount: '12000', description: 'Diesel', date: '2099-01-01'), 'The date is in the future.');
  });

  Future<({List<String> sent, List<String> keys, List<String?> lines})> open(
    WidgetTester t,
    Widget Function(ChatsData chats, void Function(String? line) saved) body, {
    Future<http.Response?> Function(http.Request req)? also,
  }) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 1000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    final keys = <String>[];
    final lines = <String?>[];
    http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final own = await also?.call(req);
        if (req.method != 'GET') {
          sent.add('${req.method} ${req.url.path} ${req.body}');
          keys.add(req.headers['Idempotency-Key'] ?? '');
        }
        if (own != null) return own;
        if (req.method == 'GET' && req.url.path == '/v1/bills/pay') return ok({'workflow_id': 'W1', 'connection': 'paystack', 'approver_role': 'owner'});
        if (req.method == 'GET' && req.url.path == '/v1/invoices') return ok({'invoices': [_invoice, {..._invoice, 'id': 'I2', 'number': 'INV-0043', 'status': 'paid', 'due_kobo': 0}]});
        if (req.method == 'GET') return null;
        if (req.url.path == '/v1/money/entries') return ok({'entry_id': 'E9'}, 201);
        if (req.url.path == '/v1/invoices') return ok({'id': 'I7', 'number': 'INV-0050'}, 201);
        return ok({});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TkSheet(title: 'Sheet', body: body(chats, lines.add))));
    await settle(t);
    return (sent: sent, keys: keys, lines: lines);
  }

  testWidgets('an invoice: a payment recorded and matched to it, once', (t) async {
    final r = await open(t, (c, saved) => InvoiceView(auth: c.auth, invoice: _invoice, onSaved: saved));
    expect(find.text('₦35,000'), findsOneWidget);
    expect(find.text('TO PAY · DUE 20 NOV 2026'), findsOneWidget);
    expect(find.text('₦10,000 of ₦45,000 paid.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Record a payment'));
    await t.pump();
    expect(find.text('35000'), findsOneWidget, reason: 'what is due, already in');
    await t.tap(find.bySemanticsLabel('How: In cash'));
    await t.pumpAndSettle();
    await t.tap(find.text('By transfer'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Record ₦35,000'));
    await settle(t);
    expect(r.sent, [
      'POST /v1/money/entries {"kind":"cash","direction":"in","amount_kobo":3500000,"description":"Payment for INV-0042","channel":"transfer"}',
      'POST /v1/money/entries/E9/match {"invoice":"INV-0042"}',
    ]);
    expect(r.keys.first, isNotEmpty);
    expect(r.lines.single, 'Recorded ₦35,000 paid on INV-0042');
  });

  testWidgets('an invoice: a reminder sent; cancelled with why', (t) async {
    var r = await open(t, (c, saved) => InvoiceView(auth: c.auth, invoice: _invoice, onSaved: saved));
    await t.tap(find.bySemanticsLabel('Send a reminder'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/invoices/I1/send {}');
    expect(r.lines.single, 'Sent a reminder for INV-0042');
    r = await open(t, (c, saved) => InvoiceView(auth: c.auth, invoice: _invoice, onSaved: saved));
    await t.tap(find.bySemanticsLabel('Cancel invoice'));
    await t.pump();
    await t.enterText(find.byType(EditableText), 'Made by mistake');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Cancel INV-0042'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/invoices/I1/void {"reason":"Made by mistake"}');
    expect(r.lines.single, 'Cancelled INV-0042');
  });

  testWidgets('a new invoice to a customer kept as a draft; to a linked business, sent', (t) async {
    var r = await open(t, (c, saved) => InvoiceAdd(auth: c.auth, onSaved: saved));
    final boxes = find.byType(EditableText);
    await t.enterText(boxes.at(0), 'Bisi Okafor');
    await t.enterText(boxes.at(1), '0803 000 0000');
    await t.enterText(boxes.at(2), 'Catering for 50');
    await t.enterText(boxes.at(3), '45,000');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Keep as draft'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/invoices {"customer":{"name":"Bisi Okafor","phone":"0803 000 0000"},"description":"Catering for 50","amount_kobo":4500000}');
    expect(r.lines.single, 'Made INV-0050');
    r = await open(t, (c, saved) => InvoiceAdd(auth: c.auth, onSaved: saved, to: (name: 'Rivers Foods', key: 'link:B2')));
    expect(find.text('To Rivers Foods: it reaches them on Taskiem, with their bills.'), findsOneWidget);
    await t.enterText(find.byType(EditableText).at(0), 'Rice, 2 bags');
    await t.enterText(find.byType(EditableText).at(1), '90000');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send it'));
    await settle(t);
    expect(r.sent, [
      'POST /v1/invoices {"customer":{"name":"Rivers Foods","key":"link:B2"},"description":"Rice, 2 bags","amount_kobo":9000000}',
      'POST /v1/invoices/I7/send {}',
    ]);
    expect(r.keys[1], '${r.keys[0]}:send');
    expect(r.lines.single, 'Sent INV-0050');
  });

  testWidgets('a bill: paid from Taskiem; questioned; paid outside', (t) async {
    var r = await open(t, (c, saved) => BillView(auth: c.auth, bill: _bill, onSaved: saved));
    expect(find.text('Or transfer to 0123456789 (Wema) with INV-0104 in the note: it is counted by itself.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Pay from Taskiem'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/bills/B1/pay {}');
    expect(r.lines.single, 'Paying INV-0104 from Taskiem');
    r = await open(t, (c, saved) => BillView(auth: c.auth, bill: _bill, onSaved: saved));
    await t.tap(find.bySemanticsLabel('Question it'));
    await t.pump();
    await t.enterText(find.byType(EditableText), 'Ten bags were torn');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send the question'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/bills/B1/query {"reason":"Ten bags were torn"}');
    r = await open(t, (c, saved) => BillView(auth: c.auth, bill: _bill, onSaved: saved));
    await t.tap(find.bySemanticsLabel('I paid outside Taskiem'));
    await t.pump();
    await t.enterText(find.byType(EditableText), '20,000');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Tell Bayo Rice Mills'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/bills/B1/declare {"amount_kobo":2000000,"how":"transfer"}');
    expect(r.lines.single, 'Paid ₦20,000 of INV-0104 outside Taskiem');
  });

  testWidgets('a bill with the supplier’s page: Pay opens it', (t) async {
    final opened = <Uri>[];
    final was = openInBrowser;
    openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openInBrowser = was);
    await open(
      t,
      (c, saved) => BillView(auth: c.auth, bill: {..._bill, 'pay_account_number': null, 'pay_link': 'https://pay.example/inv-0104'}, onSaved: saved),
    );
    await t.tap(find.bySemanticsLabel('Pay ₦20,000'));
    await t.pump();
    expect(opened.single.toString(), 'https://pay.example/inv-0104');
  });

  testWidgets('money that came in, matched to an invoice still owed', (t) async {
    final r = await open(
      t,
      (c, saved) => MoneyEntryView(
        auth: c.auth,
        entry: {'id': 'E1', 'kind': 'payment', 'direction': 'in', 'amount_kobo': 3500000, 'status': 'settled', 'left_kobo': 3500000, 'to_match': true, 'channel': 'Wema', 'reference': 'TRF123', 'when': '2026-11-12T09:00:00Z'},
        onSaved: saved,
      ),
    );
    expect(find.text('+₦35,000'), findsOneWidget);
    expect(find.text('TRF123'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Match to an invoice'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('The invoice it pays: Choose an invoice'));
    await t.pumpAndSettle();
    expect(find.textContaining('INV-0043'), findsNothing, reason: 'paid: not offered');
    await t.tap(find.text('INV-0042 · Bisi Okafor · ₦35,000 to pay'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Match it'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/money/entries/E1/match {"invoice":"INV-0042"}');
    expect(r.lines.single, 'Matched +₦35,000 to INV-0042');
  });

  testWidgets('cash or an expense added; what is missing said first', (t) async {
    final r = await open(t, (c, saved) => MoneyAdd(auth: c.auth, onSaved: saved, now: () => DateTime(2026, 11, 12)));
    await t.tap(find.bySemanticsLabel('Add it'));
    await t.pump();
    expect(find.text('Give the amount.'), findsOneWidget);
    expect(r.sent, isEmpty);
    final boxes = find.byType(EditableText);
    await t.enterText(boxes.at(0), '12,000');
    await t.enterText(boxes.at(1), 'Diesel for the generator');
    await t.enterText(boxes.at(2), 'Total');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Add it'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/money/entries {"kind":"expense","amount_kobo":1200000,"description":"Diesel for the generator","occurred_at":"2026-11-12","vendor":"Total"}');
    expect(r.lines.single, 'Expense of ₦12,000 added');
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    final chats = ChatsData(
      f.auth(also: (req) async => req.url.path == '/v1/bills/pay' ? http.Response(jsonEncode({'workflow_id': 'W1'}), 200, headers: {'content-type': 'application/json'}) : null),
      now: () => f.at,
    );
    for (final w in <Widget>[
      InvoiceView(auth: chats.auth, invoice: _invoice, onSaved: (_) {}),
      BillView(auth: chats.auth, bill: _bill, onSaved: (_) {}),
      InvoiceAdd(auth: chats.auth, onSaved: (_) {}),
      MoneyAdd(auth: chats.auth, onSaved: (_) {}),
    ]) {
      await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: TkSheet(title: 'Sheet', body: w)), drive: settle);
    }
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
