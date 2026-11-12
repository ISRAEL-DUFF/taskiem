// Matching money (M8 part H; web Invoices.tsx InvoiceMatch, WaitingCard,
// HandSplit, SplitForm, CustomerMatching, GiveAccount; ChatAlerts.tsx
// MatchSheet; lib/invoices.ts): what each match, turn-down, split and
// account number sends, and what the screen says after.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/invoice_match.dart';
import 'package:taskiem/kit/page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

Map<String, dynamic> _suggestion(String id, String number, int score, {Map<String, dynamic>? split, String kind = 'invoice'}) => {
  'id': id,
  'entry_id': 'E1',
  'target_kind': kind,
  'invoice_id': kind == 'invoice' ? 'I-$number' : null,
  'number': number,
  'due_kobo': 2000000,
  'customer': 'B••• A••',
  'score': score,
  'reasons': ['Same amount as due'],
  'amount_kobo': 3000000,
  'left_kobo': 3000000,
  'channel': 'paystack/main',
  'reference': 'TRF-55',
  'received_at': '2026-11-12T08:00:00Z',
  'kind': 'payment',
  'split': ?split,
};

final _invoices = [
  {
    'id': 'I-INV-0003',
    'number': 'INV-0003',
    'status': 'sent',
    'due_kobo': 1500000,
    'customer': {'name': 'Chidi'},
  },
  {
    'id': 'I-INV-0004',
    'number': 'INV-0004',
    'status': 'paid',
    'due_kobo': 0,
    'customer': {'name': 'Dayo'},
  },
];

void main() {
  setUpAll(loadFonts);

  test('payments grouped; the words of a match, a split and an account', () {
    final g = groupSuggestions([_suggestion('S1', 'INV-0001', 60), _suggestion('S2', 'INV-0002', 90)]);
    expect(g.single.candidates.map((c) => c['number']), ['INV-0002', 'INV-0001']);
    expect(scoreWords(90).label, 'Strong');
    expect(scoreWords(60).label, 'Likely');
    expect(scoreWords(10).label, 'Possible');
    expect(channelWords('paystack/main'), 'Paystack (main)');
    expect(channelWords('upload'), 'Uploaded statement');
    expect(kindWords('pos_sale'), 'POS sale');
    expect(leftWords(3000000, 1000000), startsWith('₦10,000 of it is left to match'));
    expect(leftWords(3000000, 3000000), '');
    expect(openInvoicesOf(_invoices).map((x) => x['number']), ['INV-0003']);
    final choices = matchChoices(openInvoicesOf(_invoices), [
      {'number': 'AK-7QX', 'due_kobo': 500000, 'customer': 'Ngozi'},
    ], const []);
    expect(choices.map((c) => c.label), ['INV-0003 · ₦15,000 due · Chidi', 'Order AK-7QX · ₦5,000 to pay · Ngozi']);
    expect(matchRequest('order:AK-7QX'), {'order': 'AK-7QX'});
    expect(matchRequest('I1'), {'invoice': 'I1'});
    expect(matchedLine(3000000, 'INV-0002'), '₦30,000 matched to INV-0002. If that is wrong, undo it on the invoice.');
    expect(rejectedLine('AK-7QX', 'order'), 'Order AK-7QX will not be suggested for this payment again.');
    expect(
      heldMatchLine({
        'code': 'needs_approval',
        'request': {'amount_kobo': 5000000},
      }),
      contains('₦50,000 is at or above'),
    );
    expect(heldMatchLine({'ok': true}), isNull);
    expect(handProblem([(name: 'INV-1', amount: '', picked: false)], 100), 'Tick the invoices or orders this payment is for.');
    expect(handProblem([(name: 'INV-1', amount: 'x', picked: true)], 100), 'Give INV-1 its part as naira, like 20,000.');
    expect(handProblem([(name: 'INV-1', amount: '20', picked: true)], 1000), contains('more than the ₦10 left'));
    expect(handRest([(amount: '5', picked: true), (amount: '9', picked: false)], 1000), 500);
    expect(splitProblem(['', ''], 100), 'Give at least one invoice an amount.');
    expect(splitProblem(['abc'], 100), 'Type each amount as naira, like 20,000.');
    expect(splitProblem(['1'], 100), isNull);
    expect(
      splitLine({
        'parts': [
          {'target': 'invoice:INV-1', 'amount_kobo': 100000},
          {'target': 'order:AK', 'amount_kobo': 50000},
        ],
        'credit_kobo': 20000,
        'left_kobo': 0,
      }),
      'Matched in parts: INV-1 ₦1,000, order AK ₦500. ₦200 is the customer’s credit, counted towards their next invoice.',
    );
    expect(creditWords(0), 'None');
    expect(payerWords({'payer': 'A•••', 'kind': 'name', 'times': 1}), 'A••• (name), paid once');
    expect(
      accountWords({'account_number': '9900000001', 'bank_name': 'Wema Bank', 'channel': 'iswallet/main', 'reference': 'tsk_ab12'}),
      '9900000001 · Wema Bank (iswallet/main), reference tsk_ab12',
    );
    expect(accountProblem(byHand: true, issuer: null, f: {'connection': 'Paystack'}), 'Name the connection the account is on, like paystack/main.');
    expect(
      accountProblem(byHand: false, issuer: {'rail': 'Paystack', 'needs_bvn': true}, f: {'bvn': '123'}),
      'Paystack needs the customer’s BVN or NIN (11 digits).',
    );
    expect(accountRequest(byHand: false, connection: 'paystack/main', f: {'bvn': '12345678901'}), {
      'connection': 'paystack/main',
      'issue': true,
      'bvn': '12345678901',
    });
  });

  Future<List<String>> open(
    WidgetTester t,
    Widget Function(ChatsData c) home, {
    List<Map<String, dynamic>>? suggestions,
    List<Map<String, dynamic>> unmatched = const [],
    Object? answer,
  }) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 3200);
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
              '/v1/money/suggestions' => ok({
                'suggestions': suggestions ?? [_suggestion('S1', 'INV-0001', 92)],
              }),
              '/v1/money/unmatched' => ok({
                'payments': unmatched,
                'orders': [
                  {'number': 'AK-7QX', 'due_kobo': 500000, 'customer': 'Ngozi'},
                ],
              }),
              '/v1/invoices' => ok({'invoices': _invoices}),
              '/v1/invoices/I1/customer' => ok({
                'open': [
                  {'id': 'I1', 'number': 'INV-0007', 'kind': 'invoice', 'status': 'overdue', 'due_kobo': 2200000, 'due_on': '2026-11-01'},
                ],
                'owed_kobo': 2200000,
                'credit_kobo': 50000,
                'accounts': [],
                'payers': [
                  {'id': 'PY1', 'kind': 'name', 'payer': 'B••• A••', 'times': 2},
                ],
              }),
              '/v1/customer-accounts/issuers' => ok({
                'issuers': [
                  {'connection': 'paystack/main', 'rail': 'Paystack', 'needs_bvn': true},
                ],
              }),
              '/v1/chat-alerts/match-options' => ok({
                'options': [
                  {'kind': 'invoice', 'number': 'INV-0009', 'customer': 'Far', 'due_kobo': 9900000},
                  {'kind': 'invoice', 'number': 'INV-0008', 'customer': 'Near', 'due_kobo': 3000000},
                ],
              }),
              _ => null,
            };
          }
          sent.add('${req.method} $p ${req.body}');
          return ok(answer ?? {'ok': true});
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('To match: a suggestion matched, then said', (t) async {
    final sent = await open(t, (c) => InvoiceMatchPage(auth: c.auth));
    expect(find.text('Strong'), findsOneWidget);
    expect(find.text('Paystack (main)'), findsNothing, reason: 'it is in a line with its word');
    expect(find.textContaining('Paystack (main)'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Match to INV-0001'));
    await settle(t);
    expect(sent.single, 'POST /v1/money/suggestions/S1/confirm {}');
    expect(find.text('₦30,000 matched to INV-0001. If that is wrong, undo it on the invoice.'), findsOneWidget);
  });

  testWidgets('To match: not this one; then matched by hand to an order', (t) async {
    final sent = await open(t, (c) => InvoiceMatchPage(auth: c.auth));
    await t.tap(find.bySemanticsLabel('Not this one: INV-0001'));
    await settle(t);
    expect(sent.last, 'POST /v1/money/suggestions/S1/reject {}');
    expect(find.text('INV-0001 will not be suggested for this payment again.'), findsOneWidget);
    await t.tap(find.textContaining('Match to another invoice or order…'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Match to this one'));
    await t.pump();
    expect(find.text('Choose the invoice or order this payment is for.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Invoice or order: none chosen'));
    await t.pumpAndSettle();
    await t.tap(find.text('Order AK-7QX · ₦5,000 to pay · Ngozi'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Match to this one'));
    await settle(t);
    expect(sent.last, 'POST /v1/money/entries/E1/match {"order":"AK-7QX"}');
    expect(find.text('₦30,000 matched to order AK-7QX. If that is wrong, undo it on the order.'), findsOneWidget);
  });

  testWidgets('split by hand: each picked its part', (t) async {
    final sent = await open(
      t,
      (c) => InvoiceMatchPage(auth: c.auth),
      answer: {
        'parts': [
          {'target': 'invoice:INV-0003', 'amount_kobo': 1500000},
        ],
        'left_kobo': 1500000,
        'credit_kobo': 0,
      },
    );
    await t.tap(find.textContaining('Match to another invoice or order…'));
    await t.pump();
    await t.tap(find.textContaining('Split by hand across several…'));
    await t.pump();
    await t.tap(find.text('INV-0003 · ₦15,000 due · Chidi').last);
    await t.pump();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Part for INV-0003')), matching: find.byType(EditableText)), '15,000');
    await t.pump();
    expect(find.text('₦15,000 left to match afterwards.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Match these parts'));
    await settle(t);
    expect(sent.last, 'POST /v1/money/entries/E1/match {"parts":[{"invoice":"I-INV-0003","amount_kobo":1500000}]}');
    expect(find.text('Matched in parts: INV-0003 ₦15,000. ₦15,000 is left to match.'), findsOneWidget);
  });

  testWidgets('split across the customer’s invoices, the rest as credit', (t) async {
    final sent = await open(
      t,
      (c) => InvoiceMatchPage(auth: c.auth),
      suggestions: [
        _suggestion(
          'S1',
          'INV-0001',
          92,
          split: {
            'parts': [
              {'number': 'INV-0001', 'due_kobo': 2000000, 'amount_kobo': 2000000},
              {'number': 'INV-0002', 'due_kobo': 800000, 'amount_kobo': 0},
            ],
            'credit_kobo': 0,
            'over': true,
          },
        ),
      ],
    );
    await t.tap(find.textContaining('Split across the customer’s invoices…'));
    await t.pump();
    expect(find.textContaining('The payment is more than is due on INV-0001.'), findsOneWidget);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp(r'^INV-0002 \(')), matching: find.byType(EditableText)), '8000');
    await t.pump();
    expect(find.text('₦2,000 not counted towards these invoices.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Match INV-0001 in parts'));
    await settle(t);
    expect(
      sent.last,
      'POST /v1/money/suggestions/S1/confirm {"parts":[{"invoice":"INV-0001","amount_kobo":2000000},{"invoice":"INV-0002","amount_kobo":800000}],"credit_rest":true}',
    );
  });

  testWidgets('a match held for a second person says so', (t) async {
    await open(
      t,
      (c) => InvoiceMatchPage(auth: c.auth),
      answer: {
        'code': 'needs_approval',
        'request': {'amount_kobo': 3000000},
      },
    );
    await t.tap(find.bySemanticsLabel('Match to INV-0001'));
    await settle(t);
    expect(find.textContaining('the match waits for a second person'), findsOneWidget);
  });

  testWidgets('money nothing was suggested for, under its heading', (t) async {
    await open(
      t,
      (c) => InvoiceMatchPage(auth: c.auth),
      suggestions: const [],
      unmatched: [
        {
          'entry_id': 'E2',
          'amount_kobo': 700000,
          'channel': 'cash',
          'reference': '',
          'received_at': '2026-11-12T08:00:00Z',
          'kind': 'cash',
          'held_reason': 'entered by hand',
        },
      ],
    );
    expect(find.text('No invoice suggested'), findsOneWidget);
    expect(find.text('Held for you: entered by hand. Nothing was counted towards an invoice.'), findsOneWidget);
  });

  testWidgets('the customer’s money: owed, credit, a payer forgotten, an account issued', (t) async {
    final sent = await open(
      t,
      (c) => TkPageScreen(
        title: 'x',
        children: [
          CustomerMatching(auth: c.auth, invoice: const {'id': 'I1', 'paid_kobo': 0}),
        ],
      ),
    );
    expect(find.text('₦22,000 in all, on 1 bill. Their money pays the oldest first.'), findsOneWidget);
    expect(find.textContaining('₦500: paid and not yet counted'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Forget this payer: B••• A••'));
    await settle(t);
    expect(sent.last, 'DELETE /v1/money/payers/PY1 ');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Give this customer an account number').first);
    await settle(t);
    expect(find.textContaining('Paystack (paystack/main): named for your business and the customer'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Issue the account number'));
    await t.pump();
    expect(find.text('Paystack needs the customer’s BVN or NIN (11 digits).'), findsOneWidget);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^The customer’s BVN')), matching: find.byType(EditableText)), '12345678901');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Issue the account number'));
    await settle(t);
    expect(sent.last, 'POST /v1/invoices/I1/customer-accounts {"connection":"paystack/main","issue":true,"bvn":"12345678901"}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the alert’s Match it: nearest the amount first; one tapped is matched', (t) async {
    var done = 0;
    final sent = await open(t, (c) => MatchSheet(auth: c.auth, entry: 'E1', amount: 3000000, line: '₦30,000 came in', onDone: () => done++));
    expect(find.text('₦30,000 came in. What does it pay?'), findsOneWidget);
    expect(t.getTopLeft(find.text('INV-0008')).dy, lessThan(t.getTopLeft(find.text('INV-0009')).dy));
    expect(find.text('Same amount'), findsOneWidget);
    await t.enterText(find.byType(EditableText), 'far');
    await t.pump();
    expect(find.text('INV-0008'), findsNothing);
    await t.enterText(find.byType(EditableText), '');
    await t.pump();
    await t.tap(find.text('INV-0008'));
    await settle(t);
    expect(sent.single, 'POST /v1/money/entries/E1/match {"invoice":"INV-0008"}');
    expect(done, 1);
    await t.pump(const Duration(seconds: 3));
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async => switch (req.url.path) {
          '/v1/money/suggestions' => ok({
            'suggestions': [_suggestion('S1', 'INV-0001', 92)],
          }),
          '/v1/money/unmatched' => ok({'payments': [], 'orders': []}),
          '/v1/invoices' => ok({'invoices': _invoices}),
          '/v1/invoices/I1/customer' => ok({'credit_kobo': 0, 'accounts': [], 'payers': []}),
          '/v1/chat-alerts/match-options' => ok({
            'options': [
              {'kind': 'invoice', 'number': 'INV-0008', 'customer': 'Near', 'due_kobo': 3000000},
            ],
          }),
          _ => null,
        },
      ),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: InvoiceMatchPage(auth: chats.auth),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: TkPageScreen(
          title: 'x',
          children: [
            CustomerMatching(auth: chats.auth, invoice: const {'id': 'I1'}),
          ],
        ),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: MatchSheet(auth: chats.auth, entry: 'E1', amount: 3000000),
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
