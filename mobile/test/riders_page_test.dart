// Riders (M8 part R; web StorefrontRiders.tsx, Dispatch.tsx RidersPayPage,
// stepup.tsx; lib/riders.ts, lib/dispatch.ts): what adding, changing,
// retiring, cash handed in, a bank account (confirmed with a code), the
// weekly pay and a payout not paid send.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/riders_page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

final _riders = {
  'riders': [
    {
      'id': 'R1',
      'name': 'Musa',
      'phone': '0803 123 4567',
      'zones': ['Lekki'],
      'fee_kobo': 100000,
      'active': true,
      'reached': 'whatsapp',
      'presence': {'state': 'busy', 'until': '2026-11-12T14:00:00Z'},
      'rating': 4.5,
      'ratings': 2,
    },
    {
      'id': 'R2',
      'name': 'Tolu',
      'phone': '0805 000 0000',
      'zones': [],
      'fee_kobo': 0,
      'active': false,
      'reached': 'sms',
      'presence': {'state': 'free'},
    },
  ],
  'zones': [
    {'name': 'Lekki', 'fee_kobo': 150000},
    {'name': 'Ajah', 'fee_kobo': 0},
  ],
  'tap_in_url': 'https://taskiem.test/r/abc',
};

void main() {
  setUpAll(loadFonts);

  test('riders and their pay in words', () {
    final r = (_riders['riders'] as List).cast<Map<String, dynamic>>();
    expect(stateLine(r[0]), 'Busy till 15:00');
    expect(stateLine(r[1]), 'On leave');
    expect(riderSub(r[0]), 'Lekki · ₦1,000 a delivery · On WhatsApp · rated 4.5 of 5 (2)');
    expect(riderSub(r[1]), 'All areas · By SMS until they tap the link');
    expect(riderProblem(name: 'Ada', phone: '0803', fee: '', adding: true), 'Enter the rider’s mobile number, like 0803 123 4567.');
    expect(riderProblem(name: 'Ada', phone: '', fee: '200000', adding: false), 'The fee per delivery is an amount from ₦0 to ₦100,000.');
    expect(zoneLine({'name': 'Ajah', 'fee_kobo': 0}), 'Ajah, free delivery');
    expect(
      payLineSub({'deliveries': 1, 'fees_kobo': 100000, 'owed_kobo': 30000, 'bank': ''}),
      '1 delivery, fees ₦1,000 · less ₦300 cash they owe · No bank account yet',
    );
    expect(bankProblem('', '0123456789'), 'Choose the rider’s bank.');
    expect(bankProblem('058', '123'), 'An account number is 10 digits.');
    expect(remitProblem('9000', 500000), 'They owe ₦5,000, less than that.');
    expect(payoutStatus('failed').label, 'Not paid');
  });

  Future<List<String>> open(WidgetTester t, Widget Function(ChatsData c) home, {bool stepUp = false}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 2600);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET') {
            return switch (p) {
              '/v1/riders' => ok(_riders),
              '/v1/riders/owed' => ok({
                'owed': [
                  {'rider_id': 'R1', 'rider': 'Musa', 'owed_kobo': 500000, 'deliveries': 2},
                ],
              }),
              '/v1/riders/pay' => ok({
                'lines': [
                  {'rider_id': 'R1', 'rider': 'Musa', 'deliveries': 3, 'fees_kobo': 300000, 'owed_kobo': 0, 'net_kobo': 300000, 'bank': ''},
                ],
                'payouts': [
                  {
                    'payout_id': 'PO1',
                    'rider_id': 'R1',
                    'rider': 'Musa',
                    'deliveries': 2,
                    'fees_kobo': 200000,
                    'owed_kobo': 0,
                    'amount_kobo': 200000,
                    'status': 'pending',
                  },
                ],
                'workflow_id': 'W1',
              }),
              '/v1/receive-money/banks' => ok({
                'banks': [
                  {'code': '058', 'name': 'GTBank'},
                ],
              }),
              _ => null,
            };
          }
          sent.add('${req.method} $p ${req.body}');
          if (stepUp && p.endsWith('/bank') && !req.body.contains('totp')) {
            return ok({
              'error': 'Confirm it is you.',
              'step_up': 'required',
              'operation': 'rider.bank',
              'methods': ['totp'],
            }, 403);
          }
          return ok({'ok': true});
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('riders: each with their state; one added with their areas; one retired', (t) async {
    final sent = await open(t, (c) => RidersPage(auth: c.auth));
    expect(find.text('Busy till 15:00'), findsOneWidget);
    expect(find.text('https://taskiem.test/r/abc'), findsOneWidget);
    expect(find.bySemanticsLabel('Send the link again to Tolu'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Add a rider'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Name')), matching: find.byType(EditableText)), 'Chidi  Obi');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Mobile number')), matching: find.byType(EditableText)), '0803 555 1234');
    await t.tap(find.text('Ajah, free delivery'));
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp(r'^Fee per delivery')), matching: find.byType(EditableText)), '1200');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Add the rider'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/riders {"name":"Chidi Obi","zones":["Ajah"],"fee_kobo":120000,"phone":"0803 555 1234"}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Retire Musa'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Retire').last);
    await settle(t);
    expect(sent.last, 'POST /v1/riders/R1/retire {}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a rider put on leave', (t) async {
    final sent = await open(t, (c) => RidersPage(auth: c.auth));
    await t.tap(find.bySemanticsLabel('Change Musa'));
    await t.pumpAndSettle();
    await t.tap(find.text('On leave: keep them, but offer them nothing'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'PATCH /v1/riders/R1 {"name":"Musa","zones":["Lekki"],"fee_kobo":100000,"active":false}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('pay: cash handed in; a payout not paid; paying now', (t) async {
    final sent = await open(t, (c) => RidersPayPage(auth: c.auth));
    expect(find.text('₦3,000'), findsWidgets);
    await t.tap(find.bySemanticsLabel('Record cash handed in by Musa'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Record it'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/riders/R1/remit {"amount_kobo":500000,"how":"cash"}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Mark not paid for Musa'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Mark not paid').last);
    await settle(t);
    expect(sent.last, 'POST /v1/riders/payouts/PO1/not-paid {}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Pay riders now'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Pay them'));
    await settle(t);
    expect(sent.last, 'POST /v1/riders/pay {}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a bank account, confirmed with a code from the authenticator app', (t) async {
    final sent = await open(t, (c) => RidersPayPage(auth: c.auth), stepUp: true);
    await t.tap(find.bySemanticsLabel('Add bank account for Musa'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Bank: none chosen'));
    await t.pumpAndSettle();
    await t.tap(find.text('GTBank'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Account number')), matching: find.byType(EditableText)), '0123456789');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('Confirm: Musa’s bank account'), findsOneWidget);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Code from your authenticator')), matching: find.byType(EditableText)), '123456');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Confirm'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent, [
      'PUT /v1/riders/R1/bank {"bank_code":"058","bank_name":"GTBank","account_number":"0123456789"}',
      'PUT /v1/riders/R1/bank {"bank_code":"058","bank_name":"GTBank","account_number":"0123456789","totp":"123456"}',
    ]);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('weekly pay set up', (t) async {
    final sent = await open(t, (c) => RidersPayPage(auth: c.auth));
    await t.tap(find.bySemanticsLabel('Change the weekly pay'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Day: Friday'));
    await t.pumpAndSettle();
    await t.tap(find.text('Monday'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Save'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'PUT /v1/riders/pay/setup {"pay_day":1,"pay_time":"17:00"}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async => switch (req.url.path) {
          '/v1/riders' => ok(_riders),
          '/v1/riders/owed' => ok({'owed': []}),
          '/v1/riders/pay' => ok({'lines': [], 'payouts': [], 'workflow_id': null}),
          _ => null,
        },
      ),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: RidersPage(auth: chats.auth),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: RidersPayPage(auth: chats.auth),
      ),
      drive: settle,
    );
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
