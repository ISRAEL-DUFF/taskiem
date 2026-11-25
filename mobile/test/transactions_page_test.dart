// Money › Transactions (docs/spec/transactions-page.md; the web's
// pages/Transactions.tsx): every payment in and out, by day, with the
// totals for the filter; a period, in or out, an account, matched or not
// and a search sent to the server; more on a tap; the period's CSV shared;
// one transaction on its own page; and Money's own row to it.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show ListMoneyEntriesResponseEntriesItem, MoneyTotals;
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/chat/attachments.dart' show handOffFile;
import 'package:taskiem/chat/money_page.dart' show MoneyPage;
import 'package:taskiem/chat/transactions_page.dart';
import 'package:taskiem/chat/words.dart' show CardTone;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart';

// Wednesday 25 November 2026, 10:00 in Lagos.
final _now = DateTime.utc(2026, 11, 25, 9);

Map<String, dynamic> _row(String id, {required String kind, required String direction, int? amount, String status = 'settled', String? channel, String? reference, required String when, String? description, String? vendor, String? towards, bool toMatch = false, int? fee, int statuses = 1}) => {
  'id': id,
  'movement': id,
  'kind': kind,
  'direction': direction,
  'amount_kobo': ?amount,
  'fee_kobo': ?fee,
  'status': status,
  'channel': ?channel,
  'reference': ?reference,
  'when': when,
  'description': ?description,
  'vendor': ?vendor,
  'towards': ?towards,
  'to_match': toMatch,
  'statuses': statuses,
};

final _first = [
  _row('E1', kind: 'payment', direction: 'in', amount: 2500000, fee: 37500, channel: 'paystack/main', reference: 'P-1', when: '2026-11-25T08:00:00Z', towards: 'invoice INV-0104'),
  _row('E2', kind: 'cash', direction: 'in', amount: 1234500, channel: 'cash', reference: 'Cash from Bisi', when: '2026-11-25T07:00:00Z', description: 'Cash from Bisi', toMatch: true),
  _row('E3', kind: 'expense', direction: 'out', amount: 345600, channel: 'cash', reference: 'x', when: '2026-11-24T15:00:00Z', description: 'Diesel', vendor: 'Total'),
];
final _second = [
  _row('E4', kind: 'payout', direction: 'out', amount: 4000000, status: 'reversed', channel: 'iswallet/taskiem', reference: 'T-1', when: '2026-11-20T10:00:00Z', statuses: 2),
];

final _totals = {
  'count': 4,
  'in_settled_kobo': 3734500,
  'in_pending_kobo': 0,
  'out_settled_kobo': 345600,
  'out_pending_kobo': 50000,
  'fees_kobo': 37500,
  'other_currencies': 0,
};

final _detail = {
  'revealed': false,
  'payer': 'B•••',
  'payer_account': '••6789',
  'entry': {
    ..._first.first,
    'recorded_at': '2026-11-25T08:00:02Z',
    'source': 'event',
    'history': [
      {'status': 'pending', 'kind': 'payment', 'amount_kobo': 2500000, 'at': '2026-11-25T08:00:02Z', 'source': 'event'},
      {'status': 'settled', 'kind': 'payment', 'amount_kobo': 2500000, 'at': '2026-11-25T08:05:00Z', 'source': 'event'},
    ],
    'counts': [
      {'what': 'invoice', 'id': 'I1', 'number': 'INV-0104', 'amount_kobo': 2500000},
    ],
  },
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  test('the words of a row, the totals and the days', () {
    final e1 = ListMoneyEntriesResponseEntriesItem.fromJson(_first[0]);
    final e2 = ListMoneyEntriesResponseEntriesItem.fromJson(_first[1]);
    final e3 = ListMoneyEntriesResponseEntriesItem.fromJson(_first[2]);
    final e4 = ListMoneyEntriesResponseEntriesItem.fromJson(_second[0]);
    expect(txTitle(description: e1.description, vendor: e1.vendor, kind: e1.kind), 'Payment');
    expect(txTitle(description: null, vendor: 'Total', kind: 'expense'), 'Total');
    expect(txAmount(amountKobo: 2500000, direction: 'in'), '+₦25,000');
    expect(txAmount(amountKobo: 345600, direction: 'out'), '−₦3,456');
    expect(txAmount(amountKobo: 5000, direction: 'in', currency: 'USD'), '+USD 50.00');
    expect(txAmount(amountKobo: null, direction: 'in'), 'No amount');
    expect(txSub(e1), 'Paystack (main) · P-1 · invoice INV-0104');
    expect(txSub(e2), 'Cash', reason: 'its words are its title; the kind and the account the same');
    expect(txSub(e3), 'Expense · Cash · x');
    expect(whereWords('iswallet/taskiem'), 'Taskiem wallet');
    expect(txStatus(status: e4.status, toMatch: false, direction: 'out'), (word: 'Reversed', tone: CardTone.plain));
    expect(txStatus(status: 'settled', toMatch: true, direction: 'in'), (word: 'To match', tone: CardTone.warn));
    expect(txStatus(status: 'settled', toMatch: false, towards: 'invoice X', direction: 'in'), (word: 'Matched', tone: CardTone.lime));
    expect(txStatus(status: 'settled', toMatch: false, direction: 'in'), (word: 'Not matched', tone: CardTone.plain));
    expect(txStatus(status: 'pending', toMatch: false, direction: 'out'), (word: 'Pending', tone: CardTone.warn));
    expect([for (final g in txByDay([e1, e2, e3, e4], _now)) '${g.day} ${g.rows.length}'], ['Today 2', 'Yesterday 1', '20 Nov 2026 1']);
    final totals = MoneyTotals.fromJson(_totals);
    expect(txTotalsLine(totals, ''), (label: 'Money in', kobo: 3734500, line: '₦3,456 out · ₦375 in fees · 4 transactions'));
    expect(txTotalsLine(totals, 'out').label, 'Money out');
    expect(txAsideLine(totals), '₦500 out still pending');
    const f = TxFilter();
    expect(f.periodWordsAt(_now), 'This month');
    expect(f.periodAt(_now), (from: '2026-11-01', to: '2026-11-25'));
    expect(f.copy(period: 'all').periodAt(_now), isNull);
    expect(f.copy(period: 'custom', custom: (from: '2026-11-05', to: '2026-11-01')).problemAt(_now), 'The last day is on or after the first.');
    expect(f.narrowed, isFalse);
    expect(f.copy(q: '4,500').narrowed, isTrue);
    expect(txPeriodFrom('today', (from: '2026-11-25', to: '2026-11-25')), (period: 'today', custom: null));
    expect(txPeriodFrom('yesterday', (from: '2026-11-24', to: '2026-11-24')), (period: 'custom', custom: (from: '2026-11-24', to: '2026-11-24')));
  });

  Auth signIn(List<String> sent) {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final auth = f.auth(
      also: (req) async {
        final p = req.url.path;
        if (req.method != 'GET') return null;
        sent.add('GET $p ${req.url.query}');
        final q = req.url.queryParameters;
        if (p == '/v1/money/entries' && q['movements'] == 'true') {
          if (q['cursor'] == 'c1') return ok({'entries': _second});
          final rows = [
            for (final r in _first)
              if ((q['direction'] == null || r['direction'] == q['direction']) && (q['q'] == null || jsonEncode(r).toLowerCase().contains(q['q']!.toLowerCase()))) r,
          ];
          return ok({'entries': rows, if (rows.length == _first.length) 'next': 'c1', 'totals': _totals, 'channels': ['cash', 'iswallet/taskiem', 'paystack/main']});
        }
        if (p.startsWith('/v1/money/entries/')) return ok(q['reveal'] == 'true' ? {..._detail, 'revealed': true, 'payer': 'Bola Ade', 'payer_account': '0123456789'} : _detail);
        if (p == '/v1/money/exports/log') return http.Response('date,amount\n', 200, headers: {'content-type': 'text/csv'});
        if (p == '/v1/money/summary') return ok({'in_settled_kobo': 0, 'in_pending_kobo': 0, 'out_settled_kobo': 0, 'out_pending_kobo': 0, 'fees_kobo': 0, 'channels': [], 'invoices_paid': 0, 'invoices_paid_kobo': 0, 'leaks_opened': 0, 'leaks_opened_kobo': 0, 'leaks_open': 0, 'leaks_open_kobo': 0, 'recovered_kobo': 0, 'unit': 'hour', 'buckets': []});
        return null;
      },
    );
    return auth;
  }

  Future<List<String>> open(WidgetTester t, Widget Function(Auth a) home, {double height = 1600, TkMode mode = TkMode.light}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = Size(390, height);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final sent = <String>[];
    final auth = signIn(sent);
    await t.pumpWidget(TaskiemApp(mode: mode, home: home(auth)));
    await settle(t);
    return sent;
  }

  testWidgets('the list: by day, the totals, filters and a search sent, more on a tap, the CSV shared', (t) async {
    final shared = <String>[];
    final was = handOffFile;
    handOffFile = (Uint8List bytes, String name, String type, Rect? from) async => shared.add('$name $type');
    addTearDown(() => handOffFile = was);
    final sent = await open(t, (a) => MoneyTransactionsPage(auth: a, now: () => _now));
    expect(sent.first, 'GET /v1/money/entries movements=true&from=2026-11-01&to=2026-11-25');
    expect(find.text('₦37,345'), findsOneWidget, reason: 'money in this month');
    expect(find.text('₦3,456 out · ₦375 in fees · 4 transactions'), findsOneWidget);
    expect(find.text('Today'), findsWidgets);
    expect(find.text('Yesterday'), findsOneWidget);
    expect(find.text('Cash from Bisi'), findsOneWidget);
    expect(find.text('+₦12,345'), findsOneWidget);
    expect(find.text('To match'), findsOneWidget);
    expect(find.text('Matched'), findsWidgets);
    await looks(t, 'transactions');
    await snap(t, 'transactions');

    // More, on a tap.
    await t.tap(find.text('Show more'));
    await settle(t);
    expect(sent.last, 'GET /v1/money/entries movements=true&from=2026-11-01&to=2026-11-25&cursor=c1');
    expect(find.text('Paid out'), findsOneWidget);
    expect(find.text('Reversed'), findsOneWidget);
    expect(find.text('Show more'), findsNothing);
    await snap(t, 'transactions-more');

    // Money out only; then a search.
    await t.tap(find.bySemanticsLabel('Money out'));
    await settle(t);
    expect(sent.last, 'GET /v1/money/entries movements=true&direction=out&from=2026-11-01&to=2026-11-25');
    expect(find.text('Diesel'), findsOneWidget);
    expect(find.text('Cash from Bisi'), findsNothing);
    await t.tap(find.bySemanticsLabel('All'));
    await settle(t);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('Search transactions')), matching: find.byType(EditableText)), 'bisi');
    await t.pump(const Duration(milliseconds: 400));
    await settle(t);
    expect(sent.last, 'GET /v1/money/entries movements=true&from=2026-11-01&to=2026-11-25&q=bisi');
    expect(find.text('Cash from Bisi'), findsOneWidget);
    expect(find.text('Payment'), findsNothing);

    // An account picked; then everything, which has no CSV.
    await t.tap(find.text('Account'));
    await t.pumpAndSettle();
    await t.tap(find.text('Taskiem wallet').last);
    await settle(t);
    expect(sent.last, contains('channel=iswallet%2Ftaskiem'));
    await t.tap(find.bySemanticsLabel(RegExp('^Download the period')));
    await settle(t);
    expect(shared.single, 'log-2026-11-01-to-2026-11-25.csv text/csv');
    await t.ensureVisible(find.bySemanticsLabel('Everything'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Everything'));
    await settle(t);
    expect(sent.last, startsWith('GET /v1/money/entries movements=true&channel='));
    expect(find.bySemanticsLabel(RegExp('^Download the period')), findsNothing);
  });

  testWidgets('one transaction: its amount, details, what it paid for, what happened; who paid shown on asking', (t) async {
    final sent = await open(t, (a) => MoneyTransactionPage(auth: a, id: 'E1', now: () => _now), height: 1500);
    expect(sent.first, 'GET /v1/money/entries/E1 ');
    expect(find.text('+₦25,000'), findsOneWidget);
    expect(find.text('B••• · ••6789'), findsOneWidget);
    expect(find.text('₦375'), findsOneWidget);
    expect(find.text('Invoice INV-0104'), findsOneWidget);
    expect(find.text('What happened'), findsOneWidget);
    expect(find.text('Told by the payments account'), findsNothing, reason: 'it is with the time it was heard');
    expect(find.textContaining('Told by the payments account'), findsOneWidget);
    await looks(t, 'transaction');
    await snap(t, 'transaction');
    await t.tap(find.text('Show who paid'));
    await settle(t);
    expect(sent.last, 'GET /v1/money/entries/E1 reveal=true');
    expect(find.text('Bola Ade · 0123456789'), findsOneWidget);
    expect(find.text('Show who paid'), findsNothing);
  });

  testWidgets('Money › Go to › Transactions, for the period Money shows', (t) async {
    final sent = await open(t, (a) => MoneyPage(auth: a, now: () => _now), height: 3200);
    await t.tap(find.text('Transactions'));
    await settle(t);
    expect(sent.where((s) => s.startsWith('GET /v1/money/entries')).first, 'GET /v1/money/entries movements=true&from=2026-11-25&to=2026-11-25', reason: 'today, as Money');
    expect(find.text('Transactions'), findsWidgets);
    expect(find.text('Cash from Bisi'), findsOneWidget);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final auth = signIn([]);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: MoneyTransactionsPage(auth: auth, now: () => _now)), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: MoneyTransactionPage(auth: auth, id: 'E1', now: () => _now)), drive: settle);
  });
}
