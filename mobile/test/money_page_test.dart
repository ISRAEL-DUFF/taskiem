// Money (M8 part M; web Money.tsx, MoneyParts.tsx, lib/money.ts,
// lib/moneyChart.ts): the period, the figures, the bars, the exports; the
// leaks assigned and closed; a statement uploaded and its columns
// matched; cash and expenses; the fees.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/attachments.dart' show handOffFile;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/money_page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

// Thursday 12 November 2026, 13:00 in Lagos.
final _now = DateTime.utc(2026, 11, 12, 12);

final _summary = {
  'from': '2026-11-12',
  'to': '2026-11-12',
  'in_settled_kobo': 5000000,
  'in_pending_kobo': 100000,
  'out_settled_kobo': 1200000,
  'out_pending_kobo': 0,
  'fees_kobo': 75000,
  'fee_rate_bp': 150,
  'channels': [
    {
      'channel': 'paystack/main',
      'in_settled_kobo': 4000000,
      'in_pending_kobo': 100000,
      'out_settled_kobo': 0,
      'out_pending_kobo': 0,
      'fees_kobo': 75000,
      'fee_rate_bp': 150,
      'movements': 4,
    },
    {'channel': 'cash', 'in_settled_kobo': 1000000, 'in_pending_kobo': 0, 'out_settled_kobo': 1200000, 'out_pending_kobo': 0, 'fees_kobo': 0, 'movements': 2},
  ],
  'invoices_paid': 2,
  'invoices_paid_kobo': 3000000,
  'leaks_opened': 1,
  'leaks_opened_kobo': 50000,
  'leaks_open': 3,
  'leaks_open_kobo': 120000,
  'recovered_kobo': 0,
  'unit': 'hour',
  'buckets': [
    for (var h = 0; h < 3; h++)
      {'from': '2026-11-12T${(8 + h).toString().padLeft(2, '0')}:00:00Z', 'in_settled_kobo': 1000000 * h, 'out_settled_kobo': 0, 'payments': h},
  ],
  'riders': [
    {
      'rider_id': 'R1',
      'rider': 'Musa',
      'delivered': 4,
      'door_transfer_kobo': 850000,
      'cash_kobo': 300000,
      'remitted_kobo': 0,
      'owed_kobo': 300000,
      'fees_kobo': 400000,
      'rating': 4.5,
      'ratings': 2,
    },
  ],
};

void main() {
  setUpAll(loadFonts);

  test('the period, in Lagos days; its words', () {
    expect(periodFor('today', _now), (from: '2026-11-12', to: '2026-11-12'));
    expect(periodFor('yesterday', _now), (from: '2026-11-11', to: '2026-11-11'));
    expect(periodFor('week', _now), (from: '2026-11-09', to: '2026-11-12'), reason: 'from Monday');
    expect(periodFor('month', _now), (from: '2026-11-01', to: '2026-11-12'));
    expect(periodFor('last_month', _now), (from: '2026-10-01', to: '2026-10-31'));
    expect(lagosDate(DateTime.utc(2026, 11, 12, 23, 30)), '2026-11-13', reason: 'Lagos is an hour ahead');
    expect(periodProblem((from: '2026-11-12', to: '2026-11-01')), 'The last day is on or after the first.');
    expect(periodProblem((from: '2026-02-30', to: '2026-03-01')), 'The first day does not look right.');
    expect(periodProblem((from: '2024-01-01', to: '2026-01-01')), 'A period is at most 400 days.');
    expect(periodWords((from: '2026-11-01', to: '2026-11-12')), '1 Nov 2026 to 12 Nov 2026');
    expect(feeRatePercent(150), '1.50%');
    expect(bucketLong('2026-11-12T13:00:00Z', 'hour', _now), '2 pm to 3 pm');
    expect(bucketLong('2026-11-12T00:00:00Z', 'day', _now), 'Today');
    expect(bucketLong('2026-11-09T00:00:00Z', 'day', _now), 'Monday 9 November');
    expect(
      riderDayLine((_summary['riders'] as List).first as Map<String, dynamic>),
      '4 delivered · ₦8,500 paid at the door · ₦3,000 cash · fees ₦4,000 · rated 4.5',
    );
  });

  test('leaks, statements, entries and fees in words', () {
    expect(leakKindWords('rider_owes'), 'Cash a rider owes');
    expect(whoWords('system:matcher', const {}, null), 'Taskiem');
    expect(whoWords('U1', const {}, 'U1'), 'you');
    expect(closedLine({'status': 'recovered', 'recovered_kobo': 2000000, 'closed_by': 'U1'}, const {}, 'U1'), 'Recovered ₦20,000, closed by you');
    expect(closeProblem('', '', ''), 'Choose how it was closed.');
    expect(closeProblem('recovered', '', ''), 'Say how much was recovered.');
    expect(closeProblem('written_off', '', ''), isNull);
    expect(csvHeader('﻿"Trans Date",Narration,"Debit, NGN",Credit\n1,2,3,4'), ['Trans Date', 'Narration', 'Debit, NGN', 'Credit']);
    final f = columnsForm(['Trans Date', 'Narration', 'Debit', 'Credit', 'Balance'], null);
    expect((f['date'], f['description'], f['split'], f['in'], f['out'], f['balance']), ('Trans Date', 'Narration', true, 'Credit', 'Debit', 'Balance'));
    expect(columnsProblem(f), isNull);
    expect(columnsRequest(f), {'date': 'Trans Date', 'description': 'Narration', 'in': 'Credit', 'out': 'Debit', 'balance': 'Balance'});
    expect(columnsProblem({...f, 'in': 'Debit'}), 'Each column can be used for one thing only.');
    expect(channelProblem('GTBank'), startsWith('The account is written in small letters'));
    expect(channelProblem('', optional: true), isNull);
    expect(addedLine({'added': 1, 'skipped': 3, 'waiting': 2}), 'Added 1 line, skipped 3 already in the log. 2 lines wait for you below.');
    expect(entryProblem(kind: 'expense', amount: '500', date: '2026-11-14', description: 'Diesel', now: _now), 'The date is in the future.');
    expect(entryProblem(kind: 'cash_in', amount: '500', date: '2026-11-12', description: 'Sales', channel: 'cash', now: _now), isNull);
    expect(percentToBp('1.5%'), 150);
    expect(percentToBp('101'), -1);
    expect(bpToPercent(150), '1.5');
    expect(bpToPercent(5), '0.05');
    expect(rateWords({'percent_bp': 150, 'flat_kobo': 10000, 'cap_kobo': 200000}), '1.5% + ₦100, at most ₦2,000');
    expect(feeProblem('paystack/main', '', '', ''), 'Give the percent, the flat fee, or both.');
  });

  Future<List<String>> open(WidgetTester t, Widget Function(ChatsData c) home, {Object? answer, double height = 3200}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = Size(390, height);
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
            sent.add('GET $p ${req.url.query}');
            return switch (p) {
              '/v1/money/summary' => ok(_summary),
              '/v1/money/unmatched' => ok({
                'payments': [
                  {'entry_id': 'E1', 'amount_kobo': 300000},
                ],
              }),
              '/v1/settings/receive-money' => ok({
                'on': true,
                'account': {'account_number': '9900000001', 'bank_name': 'Wema Bank', 'account_name': 'Ada’s Kitchen'},
              }),
              '/v1/money/exports/log' => http.Response('date,amount\n', 200, headers: {'content-type': 'text/csv'}),
              '/v1/money/leaks' => ok({
                'leaks': [
                  {
                    'id': 'K1',
                    'kind': 'fee',
                    'amount_kobo': 50000,
                    'detail': 'a fee of ₦500 above the agreed rate',
                    'status': 'open',
                    'opened_at': '2026-11-10T09:00:00Z',
                  },
                ],
                'totals': {'open': 1, 'open_kobo': 50000, 'recovered_kobo': 0},
              }),
              '/v1/money/statement-waiting' => ok({'lines': []}),
              '/v1/money/statement-format' => ok({'columns': null}),
              '/v1/money/fee-rates' => ok({
                'fee_rates': [
                  {'channel': 'paystack/main', 'percent_bp': 150, 'flat_kobo': 10000, 'cap_kobo': 200000},
                ],
              }),
              _ => null,
            };
          }
          sent.add('${req.method} $p ${req.body}');
          if (answer case final Object a) return a is http.Response ? a : ok(a);
          if (p == '/v1/money/fee-rates') return ok(jsonDecode(req.body) as Object);
          if (p == '/v1/money/entries') return ok({'entry_id': 'E9', 'created': true}, 201);
          return ok({'ok': true});
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('the overview: today’s figures, a bar pressed, a period chosen, a CSV shared', (t) async {
    final shared = <String>[];
    final was = handOffFile;
    handOffFile = (Uint8List bytes, String name, String type, Rect? from) async => shared.add('$name $type ${utf8.decode(bytes)}');
    addTearDown(() => handOffFile = was);
    final sent = await open(t, (c) => MoneyPage(auth: c.auth, now: () => _now));
    expect(sent.where((s) => s.startsWith('GET /v1/money/summary')).first, 'GET /v1/money/summary from=2026-11-12&to=2026-11-12');
    expect(find.text('₦50,000'), findsWidgets);
    expect(find.text('₦1,000 more pending'), findsOneWidget);
    expect(find.text('₦38,000'), findsOneWidget, reason: 'kept');
    expect(find.text('9900 000 001'), findsOneWidget);
    expect(find.text('1 payment not matched'), findsOneWidget);
    expect(find.text('Fees taken: ₦750'), findsOneWidget);
    expect(find.textContaining('1.50% of the money they were taken on'), findsOneWidget);
    expect(find.textContaining('4 delivered · ₦8,500 paid at the door'), findsOneWidget);
    expect(find.text('owes ₦3,000'), findsOneWidget);
    // The last bar that has begun is shown; another pressed.
    expect(find.text('11 am to noon  In ₦20,000 · Out ₦0'), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp('^9 am to 10 am')));
    await t.pump();
    expect(find.text('9 am to 10 am  In ₦0 · Out ₦0'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('This week'));
    await settle(t);
    expect(sent.where((s) => s.startsWith('GET /v1/money/summary')).last, 'GET /v1/money/summary from=2026-11-09&to=2026-11-12');
    await t.tap(find.bySemanticsLabel('Download money log'));
    await settle(t);
    expect(shared.single, 'log-2026-11-09-to-2026-11-12.csv text/csv date,amount\n');
  });

  testWidgets('dates chosen: a bad one said, nothing asked', (t) async {
    final sent = await open(t, (c) => MoneyPage(auth: c.auth, now: () => _now));
    await t.tap(find.bySemanticsLabel(RegExp('^Other period')));
    await t.pumpAndSettle();
    await t.tap(find.text('Choose dates'));
    await t.pumpAndSettle();
    await settle(t);
    final asked = sent.where((s) => s.startsWith('GET /v1/money/summary')).length;
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^To')), matching: find.byType(EditableText)), '2026-10-01');
    await t.pump();
    expect(find.text('The last day is on or after the first.'), findsOneWidget);
    expect(sent.where((s) => s.startsWith('GET /v1/money/summary')).length, asked, reason: sent.join('\n'));
  });

  testWidgets('a leak assigned, then closed as recovered', (t) async {
    final sent = await open(t, (c) => MoneyLeaksPage(auth: c.auth));
    expect(find.text('A fee of ₦500 above the agreed rate.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Assign to: No one'));
    await t.pumpAndSettle();
    await t.tap(find.textContaining('Me ('));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Assign: Fee above the agreed rate'));
    await settle(t);
    expect(sent.where((s) => !s.startsWith('GET')).last, startsWith('POST /v1/money/leaks/K1/assign {"member":"'));
    expect(find.text('Fee above the agreed rate assigned to you.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Close: Fee above the agreed rate'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Close leak'));
    await t.pump();
    expect(find.text('Choose how it was closed.'), findsOneWidget);
    await t.tap(find.textContaining('Recovered. The money came back'));
    await t.pump();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Amount recovered')), matching: find.byType(EditableText)), '500');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Close leak'));
    await settle(t);
    expect(sent.where((s) => !s.startsWith('GET')).last, 'POST /v1/money/leaks/K1/close {"status":"recovered","recovered_kobo":50000}');
    expect(find.text('Fee above the agreed rate closed: recovered ₦500.'), findsOneWidget);
  });

  testWidgets('a statement refused for its columns: matched by hand, sent again', (t) async {
    final was = pickStatement;
    pickStatement = () async => (name: 'gtb.csv', bytes: Uint8List.fromList(utf8.encode('Trans Date,Narration,Debit,Credit\n2026-11-10,Diesel,5000,\n')));
    addTearDown(() => pickStatement = was);
    var tries = 0;
    final sent = <String>[];
    t.view.physicalSize = const Size(390, 3200);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    // The server: the first try lacks columns; the second is added.
    final f = Fixture('chats');
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          if (req.method == 'GET') {
            return http.Response(
              jsonEncode(req.url.path == '/v1/money/statement-format' ? {'columns': null} : {'lines': []}),
              200,
              headers: {'content-type': 'application/json'},
            );
          }
          sent.add('${req.method} ${req.url.path} ${req.body}');
          tries++;
          return tries == 1
              ? http.Response(
                  jsonEncode({
                    'code': 'statement_unreadable',
                    'error': 'the file has no column date',
                    'missing': ['date', 'amount'],
                  }),
                  422,
                  headers: {'content-type': 'application/json'},
                )
              : http.Response(
                  jsonEncode({'channel': 'gtbank/current', 'added': 1, 'skipped': 0, 'columns': {}}),
                  200,
                  headers: {'content-type': 'application/json'},
                );
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: MoneyStatementsPage(auth: chats.auth),
      ),
    );
    await settle(t);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Account')), matching: find.byType(EditableText)), 'gtbank/current');
    await t.tap(find.bySemanticsLabel('Statement file: none chosen'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Upload statement'));
    await settle(t);
    expect(find.text('The file has no column date.'), findsOneWidget);
    expect(find.text('Match the file’s columns'), findsOneWidget);
    expect(find.bySemanticsLabel('Money in: Credit'), findsOneWidget, reason: 'guessed from the names');
    await t.tap(find.bySemanticsLabel('Upload statement'));
    await settle(t);
    final body = jsonDecode(sent.last.substring('POST /v1/money/statements '.length)) as Map;
    expect(body['columns'], {'date': 'Trans Date', 'description': 'Narration', 'in': 'Credit', 'out': 'Debit'});
    expect(find.text('Added 1 line, skipped 0 already in the log.'), findsOneWidget);
  });

  testWidgets('cash and expenses: an expense added once', (t) async {
    final sent = await open(t, (c) => MoneyEntriesPage(auth: c.auth, now: () => _now));
    await t.tap(find.bySemanticsLabel('Add to the money log'));
    await t.pump();
    expect(find.text('Give the amount.'), findsOneWidget);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp(r'^Amount')), matching: find.byType(EditableText)), '12000');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Description')), matching: find.byType(EditableText)), 'Diesel');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp(r'^Vendor')), matching: find.byType(EditableText)), 'Total');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Add to the money log'));
    await settle(t);
    expect(
      sent.where((s) => !s.startsWith('GET')).last,
      'POST /v1/money/entries {"kind":"expense","amount_kobo":1200000,"description":"Diesel","occurred_at":"2026-11-12","vendor":"Total"}',
    );
    expect(find.text('Expense of ₦12,000 added to the money log.'), findsOneWidget);
  });

  testWidgets('fees: one changed', (t) async {
    final sent = await open(t, (c) => MoneyFeesPage(auth: c.auth));
    expect(find.textContaining('Percent 1.5% · Flat fee ₦100 · Cap ₦2,000'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Change the fee for paystack/main'));
    await t.pump();
    expect(find.text('Change the fee for paystack/main'), findsOneWidget);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Percent')), matching: find.byType(EditableText)), '1.25');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save fee'));
    await settle(t);
    expect(
      sent.where((s) => !s.startsWith('GET')).last,
      'PUT /v1/money/fee-rates {"channel":"paystack/main","percent_bp":125,"flat_kobo":10000,"cap_kobo":200000}',
    );
    expect(find.text('Fee for paystack/main saved: 1.25% + ₦100, at most ₦2,000.'), findsOneWidget);
  });

  testWidgets('M8 fixes 17: Choose dates again starts from the period chosen, not the old dates', (t) async {
    await open(t, (c) => MoneyPage(auth: c.auth, now: () => _now));
    Future<void> other(String words) async {
      await t.tap(find.bySemanticsLabel(RegExp('^Other period')));
      await t.pumpAndSettle();
      await t.tap(find.text(words));
      await t.pumpAndSettle();
      await settle(t);
    }

    await other('Choose dates');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^From')), matching: find.byType(EditableText)), '2026-10-01');
    await t.pump();
    await t.tap(find.bySemanticsLabel('This month'));
    await settle(t);
    await other('Choose dates');
    final from = t.widget<EditableText>(find.descendant(of: find.bySemanticsLabel(RegExp('^From')), matching: find.byType(EditableText)));
    expect(from.controller.text, '2026-11-01', reason: 'this month’s first day, not 1 October');
  });

  testWidgets('M8 fixes 26, 27, 28: the account card says where withdrawals are; names used before offered; the tab has no Back', (t) async {
    await open(t, (c) => MoneyPage(auth: c.auth, now: () => _now, tab: true));
    expect(find.text('Your balance and withdrawals are on the web, under Settings, Payments.'), findsOneWidget);
    expect(find.bySemanticsLabel('Back'), findsNothing);
    await open(t, (c) => MoneyEntriesPage(auth: c.auth, now: () => _now));
    expect(find.text('Used before:'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Use paystack/main'));
    await t.pump();
    expect(find.text('paystack/main'), findsWidgets);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async => switch (req.url.path) {
          '/v1/money/summary' => ok(_summary),
          '/v1/money/unmatched' => ok({'payments': []}),
          '/v1/money/leaks' => ok({
            'leaks': [
              {'id': 'K1', 'kind': 'overdue', 'amount_kobo': 50000, 'detail': 'overdue', 'status': 'open', 'opened_at': '2026-11-10T09:00:00Z'},
            ],
            'totals': {'open': 1, 'open_kobo': 50000, 'recovered_kobo': 0},
          }),
          '/v1/money/fee-rates' => ok({'fee_rates': []}),
          '/v1/money/statement-waiting' => ok({'lines': []}),
          _ => null,
        },
      ),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: MoneyPage(auth: chats.auth, now: () => _now),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: MoneyLeaksPage(auth: chats.auth),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: MoneyStatementsPage(auth: chats.auth),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: MoneyEntriesPage(auth: chats.auth, now: () => _now),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: MoneyFeesPage(auth: chats.auth),
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
