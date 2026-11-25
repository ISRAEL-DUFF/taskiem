// Money › Go to › Transactions (docs/spec/transactions-page.md; the web's
// pages/Transactions.tsx, lib/transactions.ts): every movement of the
// business's money log, newest first, by day, with the totals for what is
// shown; a period, money in or out, an account, matched or not, and a
// search (who, a reference, an amount); the period's CSV through the
// phone's share sheet; and one transaction on its own page: its amount and
// status, its fee, account, reference and who, what it paid for, and each
// status it had.

import 'dart:async' show Timer;
import 'dart:typed_data';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show GetMoneyEntryResponse, ListMoneyEntriesResponse, ListMoneyEntriesResponseEntriesItem, MoneyTotals;
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/list.dart' show TkEmpty, TkLoading, TkSearchBox;
import '../kit/look.dart' show TkCallout, TkEyebrow, TkSquare, TkTopCard, tkFigure;
import '../kit/mobile.dart' show TkChips, TkFormGroup, TkHeadButton, TkListRow, TkRoute, TkSection;
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart' show TkPageScreen;
import '../kit/sheet.dart' show TkKitField, TkLines;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart' show handOffFile;
import 'book_sheet.dart' show lagosDay, lagosTime;
import 'cards.dart' show CardPill;
import 'groups.dart' show pickOneOf;
import 'invoice_match.dart' show InvoiceMatchPage, channelWords;
import 'invoices_page.dart' show openInvoice;
import 'money_page.dart' show Period, lagosDate, periodFor, periodProblem, periodWords;
import 'money_sheets.dart' show dateLabel;
import 'storefront_orders_page.dart' show openStorefrontOrder;
import 'words.dart' show CardTone, listTime, naira;

typedef Movement = ListMoneyEntriesResponseEntriesItem;

void _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Opens Transactions.
void openTransactions(BuildContext context, Auth auth, {String period = 'month', Period? custom, DateTime Function()? now}) =>
    _push(context, MoneyTransactionsPage(auth: auth, period: period, custom: custom, now: now));

/// Transactions for the period Money shows: the same choice, or its dates.
({String period, Period? custom}) txPeriodFrom(String choice, Period p) =>
    choice != 'custom' && txPeriods.any((c) => c.$1 == choice) ? (period: choice, custom: null) : (period: 'custom', custom: p);

// --- the filters ---

/// The periods offered: Money's, and everything.
const txPeriods = [
  ('today', 'Today'),
  ('week', 'This week'),
  ('month', 'This month'),
  ('last_month', 'Last month'),
  ('all', 'Everything'),
  ('custom', 'Choose dates'),
];

const txDirections = [('', 'All'), ('in', 'Money in'), ('out', 'Money out')];

const txMatched = [
  ('', 'Matched or not', 'Every transaction'),
  ('yes', 'Matched', 'Counted towards an invoice, order or sale'),
  ('no', 'Not matched', 'Money in counted towards nothing yet'),
];

/// What the list is narrowed to.
class TxFilter {
  const TxFilter({this.period = 'month', this.custom = (from: '', to: ''), this.direction = '', this.channel = '', this.matched = '', this.q = ''});
  final String period;
  final Period custom;
  final String direction;
  final String channel;
  final String matched;
  final String q;

  TxFilter copy({String? period, Period? custom, String? direction, String? channel, String? matched, String? q}) => TxFilter(
    period: period ?? this.period,
    custom: custom ?? this.custom,
    direction: direction ?? this.direction,
    channel: channel ?? this.channel,
    matched: matched ?? this.matched,
    q: q ?? this.q,
  );

  /// The period it means, or null for everything.
  Period? periodAt(DateTime now) => period == 'all' ? null : periodFor(period, now, custom);

  /// Why the chosen dates will be refused, or null.
  String? problemAt(DateTime now) => period == 'custom' ? periodProblem(custom) : null;

  /// Whether anything but the period narrows it.
  bool get narrowed => direction.isNotEmpty || channel.isNotEmpty || matched.isNotEmpty || q.trim().isNotEmpty;

  /// The period in words: "This month", "1 Oct 2026 to 31 Oct 2026".
  String periodWordsAt(DateTime now) {
    final p = periodAt(now);
    if (p == null) return 'Everything';
    if (period == 'custom') return periodWords(p);
    return txPeriods.firstWhere((c) => c.$1 == period, orElse: () => (period, periodWords(p))).$2;
  }
}

// --- words (lib/transactions.ts) ---

const _kindWords = {
  'payment': 'Payment',
  'account_credit': 'Transfer to an account',
  'payout': 'Paid out',
  'refund': 'Refund',
  'pos_sale': 'Card sale',
  'fee': 'Fee',
  'statement': 'From a statement',
  'cash': 'Cash',
  'expense': 'Expense',
  'correction': 'Correction',
};

/// What kind of money it is, in words.
String kindWords(String kind) => _kindWords[kind] ?? kind.replaceAll('_', ' ');

/// Where it moved, in words; "Taskiem wallet" for Taskiem's own.
String whereWords(String? channel) {
  if (channel == null || channel.isEmpty) return '';
  if (channel == 'iswallet/taskiem') return 'Taskiem wallet';
  return channelWords(channel);
}

/// A row's title: what was typed or the vendor, else the kind.
String txTitle({String? description, String? vendor, required String kind}) =>
    (description ?? '').isNotEmpty ? description! : ((vendor ?? '').isNotEmpty ? vendor! : kindWords(kind));

/// Its amount with which way it went: "+₦4,500", "−₦1,200", "No amount".
String txAmount({int? amountKobo, required String direction, String? currency}) {
  if (amountKobo == null) return 'No amount';
  final amount = (currency ?? '').isNotEmpty ? '$currency ${(amountKobo / 100).toStringAsFixed(2)}' : naira(amountKobo);
  return '${direction == 'in' ? '+' : '−'}$amount';
}

/// Taskiem's own references in the money log (`entered:` and an id, `sale:` and a key,
/// `posting:` and an id…): keys it keeps to record each movement once, which mean
/// nothing to a person (as the web's shownReference).
final _internalReference = RegExp(r'^(entered|chat|sale|rider-fee|remittance|posting|delivery|delivery-fee|tnt|payment|payments|purchase|thing-sale|withdraw):');

/// The reference as a person sees it: '' for one of Taskiem's own.
String shownReference(String? ref) => ref == null || _internalReference.hasMatch(ref) ? '' : ref;

/// A row's line: "Paystack (main) · P-1 · invoice INV-0104".
String txSub(Movement m) {
  final typed = (m.description ?? '').isNotEmpty || (m.vendor ?? '').isNotEmpty;
  final parts = <String>[
    if (typed) kindWords(m.kind),
    whereWords(m.channel),
    if (shownReference(m.reference).isNotEmpty && m.reference != m.description) m.reference!,
    if ((m.towards ?? '').isNotEmpty) m.towards!,
  ].where((s) => s.isNotEmpty);
  return parts.toSet().join(' · ');
}

/// A status in a word or two, and its pill's tone.
({String word, CardTone tone}) txStatus({required String status, required bool toMatch, String? towards, required String direction, String? currency}) {
  switch (status) {
    case 'failed':
      return (word: 'Failed', tone: CardTone.danger);
    case 'reversed':
      return (word: 'Reversed', tone: CardTone.plain);
    case 'pending':
      return (word: 'Pending', tone: CardTone.warn);
  }
  if (toMatch) return (word: 'To match', tone: CardTone.warn);
  if ((towards ?? '').isNotEmpty) return (word: 'Matched', tone: CardTone.lime);
  if (direction == 'in' && (currency ?? '').isEmpty) return (word: 'Not matched', tone: CardTone.plain);
  return (word: direction == 'in' ? 'Received' : 'Paid', tone: CardTone.lime);
}

({String word, CardTone tone}) _statusOf(Movement m) => txStatus(status: m.status, toMatch: m.toMatch, towards: m.towards, direction: m.direction, currency: m.currency);

/// The day a row falls on: "Today", "Yesterday", "24 Nov 2026".
String txDay(DateTime when, DateTime now) {
  final d = lagosDay(when.toUtc().toIso8601String());
  final today = lagosDate(now);
  if (d == today) return 'Today';
  if (d == lagosDate(now.subtract(const Duration(days: 1)))) return 'Yesterday';
  return dateLabel(d);
}

/// The rows by their Lagos day, newest first as they come.
List<({String day, List<Movement> rows})> txByDay(List<Movement> list, DateTime now) {
  final out = <({String day, List<Movement> rows})>[];
  for (final m in list) {
    final day = txDay(m.when, now);
    if (out.isNotEmpty && out.last.day == day) {
      out.last.rows.add(m);
    } else {
      out.add((day: day, rows: [m]));
    }
  }
  return out;
}

/// The top card: money out when only money out is shown, else money in,
/// with the rest under it.
({String label, int kobo, String line}) txTotalsLine(MoneyTotals t, String direction) {
  final out = direction == 'out';
  return (
    label: out ? 'Money out' : 'Money in',
    kobo: out ? t.outSettledKobo : t.inSettledKobo,
    line: [
      if (direction.isEmpty) '${naira(t.outSettledKobo)} out',
      if (t.feesKobo > 0) '${naira(t.feesKobo)} in fees',
      t.count == 1 ? '1 transaction' : '${t.count} transactions',
    ].join(' · '),
  );
}

/// What is still pending, and money in another currency, or ''.
String txAsideLine(MoneyTotals t) => [
  if (t.inPendingKobo > 0 || t.outPendingKobo > 0)
    '${[if (t.inPendingKobo > 0) '${naira(t.inPendingKobo)} in', if (t.outPendingKobo > 0) '${naira(t.outPendingKobo)} out'].join(' and ')} still pending',
  if (t.otherCurrencies == 1) '1 in another currency is not in these sums',
  if (t.otherCurrencies > 1) '${t.otherCurrencies} in other currencies are not in these sums',
].join('. ');

const _sourceWords = {
  'event': 'Told by the payments account',
  'lookup': 'Checked with the payments account',
  'step': 'From a workflow',
  'upload': 'From an uploaded statement',
  'entered': 'Entered by hand',
};

/// How it came into the log, in words.
String sourceWords(String s) => _sourceWords[s] ?? s;

const _statusWords = {'settled': 'Settled', 'pending': 'Pending', 'failed': 'Failed', 'reversed': 'Reversed'};

// --- the list ---

class MoneyTransactionsPage extends StatefulWidget {
  const MoneyTransactionsPage({super.key, required this.auth, this.period = 'month', this.custom, this.now});
  final Auth auth;

  /// The period to open on (Money's own, when it has one here), and the
  /// dates of a period chosen by hand.
  final String period;
  final Period? custom;
  final DateTime Function()? now;

  @override
  State<MoneyTransactionsPage> createState() => _MoneyTransactionsPageState();
}

class _MoneyTransactionsPageState extends State<MoneyTransactionsPage> {
  late TxFilter _f = TxFilter(
    period: txPeriods.any((c) => c.$1 == widget.period) && (widget.period != 'custom' || widget.custom != null) ? widget.period : 'month',
    custom: widget.custom ?? (from: '', to: ''),
  );
  final _q = TextEditingController();
  final _from = TextEditingController();
  final _to = TextEditingController();
  Timer? _typing;
  List<Movement>? _rows;
  MoneyTotals? _totals;
  List<String> _channels = const [];
  String? _next;
  String? _error;
  bool _more = false;
  int _asked = 0;

  DateTime get _now => (widget.now ?? DateTime.now)();

  @override
  void initState() {
    super.initState();
    _from.text = _f.custom.from;
    _to.text = _f.custom.to;
    _q.addListener(() {
      if (_q.text == _f.q) return;
      _typing?.cancel();
      _typing = Timer(const Duration(milliseconds: 350), () => _set(_f.copy(q: _q.text)));
    });
    for (final c in [_from, _to]) {
      c.addListener(() {
        final custom = (from: _from.text.trim(), to: _to.text.trim());
        if (_f.period == 'custom' && custom != _f.custom) _set(_f.copy(custom: custom));
      });
    }
    _load();
  }

  @override
  void dispose() {
    _typing?.cancel();
    _q.dispose();
    _from.dispose();
    _to.dispose();
    super.dispose();
  }

  void _set(TxFilter f) {
    setState(() => _f = f);
    _load();
  }

  Future<ListMoneyEntriesResponse> _ask({String? cursor}) {
    final p = _f.periodAt(_now);
    final q = _f.q.trim();
    return widget.auth.api.listMoneyEntries(
      movements: 'true',
      from: p?.from,
      to: p?.to,
      direction: _f.direction.isEmpty ? null : _f.direction,
      channel: _f.channel.isEmpty ? null : _f.channel,
      matched: _f.matched.isEmpty ? null : _f.matched,
      q: q.isEmpty ? null : q,
      cursor: cursor,
    );
  }

  Future<void> _load() async {
    if (_f.problemAt(_now) != null) {
      setState(() => (_rows = null, _totals = null, _next = null));
      return;
    }
    final asked = ++_asked;
    try {
      final r = await _ask();
      if (!mounted || asked != _asked) return;
      setState(() {
        _rows = r.entries;
        _totals = r.totals;
        _channels = r.channels ?? _channels;
        _next = r.next;
        _error = null;
      });
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _older() async {
    final next = _next;
    if (next == null || _more) return;
    final asked = _asked;
    setState(() => _more = true);
    try {
      final r = await _ask(cursor: next);
      if (!mounted || asked != _asked) return;
      setState(() => (_rows = [...?_rows, ...r.entries], _next = r.next));
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _more = false);
    }
  }

  void _period(String v) {
    if (v == 'custom' && _f.period != 'custom') {
      final p = _f.periodAt(_now) ?? periodFor('month', _now);
      _from.text = p.from;
      _to.text = p.to;
      _set(_f.copy(period: v, custom: p));
      return;
    }
    _set(_f.copy(period: v));
  }

  Future<void> _pickChannel() async {
    final v = await pickOneOf(
      context,
      title: 'Which account',
      value: _f.channel,
      choices: [('', 'Every account', ''), for (final c in _channels) (c, whereWords(c), c == 'cash' ? 'Cash taken and paid by hand' : '')],
    );
    if (v != null && mounted) _set(_f.copy(channel: v));
  }

  Future<void> _pickMatched() async {
    final v = await pickOneOf(context, title: 'Matched or not', value: _f.matched, choices: txMatched);
    if (v != null && mounted) _set(_f.copy(matched: v));
  }

  Future<void> _download() async {
    final p = _f.periodAt(_now);
    if (p == null) return;
    try {
      final q = Uri(queryParameters: {'from': p.from, 'to': p.to}).query;
      final bytes = await widget.auth.transport.bytes('/v1/money/exports/log?$q');
      await handOffFile(Uint8List.fromList(bytes), 'log-${p.from}-to-${p.to}.csv', 'text/csv', null);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    }
  }

  Future<void> _open(Movement m) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => MoneyTransactionPage(auth: widget.auth, id: m.id, row: m, now: widget.now)));

  Widget _row(BuildContext context, Movement m) {
    final look = TkLook.of(context);
    final s = _statusOf(m);
    final title = txTitle(description: m.description, vendor: m.vendor, kind: m.kind);
    final amount = txAmount(amountKobo: m.amountKobo, direction: m.direction, currency: m.currency);
    final sub = [txSub(m), lagosTime(m.when.toUtc().toIso8601String())].where((x) => x.isNotEmpty).join(' · ');
    return TkListRow(
      key: ValueKey('tx ${m.movement ?? m.id}'),
      title: title,
      sub: sub,
      icon: m.direction == 'in' ? 'b-cash' : 'b-send',
      square: m.direction == 'in' ? look.teal : look.orange,
      amount: amount,
      status: s.word,
      statusTone: s.tone,
      waiting: m.toMatch,
      semanticsLabel: '$title, ${m.direction == 'in' ? 'money in' : 'money out'} $amount, $sub, ${s.word}',
      onTap: () => _open(m),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final now = _now;
    final problem = _f.problemAt(now);
    final rows = _rows;
    final totals = _totals;
    final period = _f.periodAt(now);
    return TkPageScreen(
      title: 'Transactions',
      titleSize: 30,
      backLabel: 'Back to Money',
      description: 'Every payment in and out of the business, newest first. Open one to see its fee, its reference and what it paid for.',
      action: period != null && problem == null ? TkHeadButton(icon: 'file', label: 'Download the period as a spreadsheet', onTap: _download) : null,
      under: TkChips<String>(label: 'Period', chips: txPeriods, on: _f.period, onPick: _period),
      onRefresh: _load,
      children: [
        if (_f.period == 'custom')
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(child: TkKitField(label: 'From', child: TkTextBox(controller: _from, hint: '2026-11-01'))),
                  const SizedBox(width: 10),
                  Expanded(child: TkKitField(label: 'To', child: TkTextBox(controller: _to, hint: '2026-11-25'))),
                ],
              ),
              if (problem != null)
                Semantics(
                  liveRegion: true,
                  child: Padding(padding: const EdgeInsets.only(top: 8), child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger))),
                ),
            ],
          ),
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error!, color: look.red, subLines: 3),
        if (totals != null && problem == null) _top(context, totals, now),
        TkSearchBox(controller: _q, hint: 'Who, reference or amount', label: 'Search transactions', margin: EdgeInsets.zero),
        TkChips<String>(label: 'In or out', chips: txDirections, on: _f.direction, onPick: (v) => _set(_f.copy(direction: v)), padding: EdgeInsets.zero),
        TkSection(
          inset: 56,
          children: [
            TkListRow(title: 'Account', sub: _f.channel.isEmpty ? 'Every account' : whereWords(_f.channel), icon: 'b-bank', square: look.violet, onTap: _pickChannel),
            TkListRow(
              title: 'Matched',
              sub: txMatched.firstWhere((m) => m.$1 == _f.matched).$2,
              icon: 'b-receipt',
              square: look.blue,
              onTap: _pickMatched,
            ),
          ],
        ),
        if (rows == null && _error == null && problem == null) const TkLoading(rows: 4, label: 'Loading the transactions…'),
        if (rows != null && rows.isEmpty)
          TkEmpty(
            icon: 'money',
            title: _f.narrowed ? 'Nothing matches' : 'No money moved',
            line: _f.narrowed ? 'Try another search, or fewer filters.' : 'Nothing came in or went out in ${_f.periodWordsAt(now).toLowerCase()}.',
          ),
        if (rows != null)
          for (final g in txByDay(rows, now)) TkSection(title: g.day, children: [for (final m in g.rows) _row(context, m)]),
        if (_next != null && rows != null)
          Center(child: TkSmallButton(label: _more ? 'Loading…' : 'Show more', onPressed: _more ? null : _older)),
      ],
    );
  }

  Widget _top(BuildContext context, MoneyTotals totals, DateTime now) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final x = txTotalsLine(totals, _f.direction);
    final aside = txAsideLine(totals);
    final words = _f.periodWordsAt(now);
    return TkTopCard(
      semanticsLabel: '${x.label}, $words: ${naira(x.kobo)}. ${x.line}${aside.isEmpty ? '' : '. $aside'}',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow(x.label, color: x.label == 'Money out' ? look.orangeInk : look.limeInk, icon: x.label == 'Money out' ? 'b-send' : 'b-cash', trailing: _f.narrowed ? '$words · filtered' : words),
          const SizedBox(height: 6),
          FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text(naira(x.kobo), maxLines: 1, style: tkFigure(context, size: 40))),
          Text(x.line, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
          if (aside.isNotEmpty) ...[const SizedBox(height: 4), Text(aside, style: tkText(size: 13, lineHeight: 1.4, color: t.muted))],
        ],
      ),
    );
  }
}

// --- one transaction ---

class MoneyTransactionPage extends StatefulWidget {
  const MoneyTransactionPage({super.key, required this.auth, required this.id, this.row, this.now});
  final Auth auth;
  final String id;

  /// Its row in the list, shown while the rest is read.
  final Movement? row;
  final DateTime Function()? now;

  @override
  State<MoneyTransactionPage> createState() => _MoneyTransactionPageState();
}

class _MoneyTransactionPageState extends State<MoneyTransactionPage> {
  GetMoneyEntryResponse? _a;
  String? _error;
  bool _reveal = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.getMoneyEntry(id: widget.id, reveal: _reveal ? 'true' : null);
      if (mounted) setState(() => (_a = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final now = (widget.now ?? DateTime.now)();
    final a = _a;
    final d = a?.entry;
    final row = widget.row;
    final kind = d?.kind ?? row?.kind ?? 'payment';
    final direction = d?.direction ?? row?.direction ?? 'in';
    final title = d != null
        ? txTitle(description: d.description, vendor: d.vendor, kind: d.kind)
        : row != null
        ? txTitle(description: row.description, vendor: row.vendor, kind: row.kind)
        : 'Transaction';
    final amount = txAmount(amountKobo: d?.amountKobo ?? row?.amountKobo, direction: direction, currency: d?.currency ?? row?.currency);
    final status = d != null
        ? txStatus(status: d.status, toMatch: d.toMatch, towards: d.towards, direction: d.direction, currency: d.currency)
        : row != null
        ? _statusOf(row)
        : null;
    final when = d?.when ?? row?.when;
    final who = (a?.payer ?? '').isNotEmpty ? a!.payer! : (d?.vendor ?? '');
    final out = direction == 'out';
    return TkPageScreen(
      title: title,
      titleSize: 22,
      backLabel: 'Back to Transactions',
      onRefresh: _load,
      children: [
        TkTopCard(
          semanticsLabel: '${out ? 'Money out' : 'Money in'}: $amount${status == null ? '' : ', ${status.word}'}',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Expanded(child: TkEyebrow(out ? 'Money out' : 'Money in', color: out ? look.orangeInk : look.limeInk, icon: out ? 'b-send' : 'b-cash')),
                  if (status != null) CardPill(status.word, status.tone),
                ],
              ),
              const SizedBox(height: 6),
              FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text(amount, maxLines: 1, style: tkFigure(context, size: 40))),
              Text(
                [kindWords(kind), whereWords(d?.channel ?? row?.channel), if (when != null) listTime(when, now)].where((x) => x.isNotEmpty).join(' · '),
                style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
              ),
            ],
          ),
        ),
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error!, color: look.red, subLines: 3),
        if (d == null && _error == null) const TkLoading(rows: 3, label: 'Loading the transaction…'),
        if (d != null && d.toMatch && widget.auth.can('invoice.manage'))
          TkCallout(
            leading: TkSquare('b-receipt', color: look.orange),
            title: 'Not matched yet',
            sub: 'Match it to the invoice or order it paid for.',
            color: look.orange,
            button: 'Match',
            semantics: 'Match it to an invoice or order',
            onPressed: () => _push(context, InvoiceMatchPage(auth: widget.auth)),
          ),
        if (d != null)
          TkFormGroup(
            title: 'Details',
            children: [
              TkLines([
                ('What', kindWords(d.kind)),
                if (who.isNotEmpty) (out ? 'To' : 'From', [who, if ((a?.payerAccount ?? '').isNotEmpty) a!.payerAccount!].join(' · ')),
                ('Fee', (d.feeKobo ?? 0) > 0 ? naira(d.feeKobo!) : 'None'),
                ('Account', whereWords(d.channel).isEmpty ? '—' : whereWords(d.channel)),
                if (shownReference(d.reference).isNotEmpty) ('Reference', d.reference!),
                if ((d.description ?? '').isNotEmpty && d.description != title) ('Description', d.description!),
                if ((d.category ?? '').isNotEmpty) ('Label', d.category!),
                if ((d.note ?? '').isNotEmpty) ('Note', d.note!),
                if (d.direction == 'in' && (d.currency ?? '').isEmpty && (d.leftKobo ?? 0) > 0) ('Not counted yet', naira(d.leftKobo!)),
                if ((d.currency ?? '').isNotEmpty) ('Currency', '${d.currency}: not in your naira totals'),
                ('When', listTime(d.when, now)),
                ('Heard', '${listTime(d.recordedAt, now)} · ${sourceWords(d.source)}'),
              ]),
              if ((a?.payer ?? '').isNotEmpty && a?.revealed != true && widget.auth.can('pii.reveal')) ...[
                const SizedBox(height: 10),
                Align(
                  alignment: Alignment.centerLeft,
                  child: TkSmallButton(
                    label: 'Show who paid',
                    semantics: 'Show who paid. It is recorded in the audit log',
                    onPressed: () {
                      setState(() => _reveal = true);
                      _load();
                    },
                  ),
                ),
              ],
            ],
          ),
        if (d != null)
          TkSection(
            title: 'What it paid for',
            inset: 56,
            children: [
              if (d.counts.isEmpty)
                TkListRow(
                  title: d.direction == 'in' ? 'Not matched yet' : 'Nothing',
                  sub: d.direction == 'in' ? 'Not counted towards an invoice or order' : 'Money out is not matched',
                  icon: 'b-receipt',
                  square: t.muted,
                  chevron: false,
                ),
              for (final c in d.counts)
                TkListRow(
                  title: '${c.what == 'invoice' ? 'Invoice' : c.what == 'order' ? 'Order' : 'Sale'} ${c.number}',
                  icon: c.what == 'invoice' ? 'b-receipt' : 'b-bag',
                  square: c.what == 'invoice' ? look.blue : look.teal,
                  amount: naira(c.amountKobo),
                  chevron: c.what != 'sale',
                  onTap: switch (c.what) {
                    'invoice' => () => openInvoice(context, widget.auth, c.id),
                    'order' => () => openStorefrontOrder(context, widget.auth, c.number),
                    _ => null,
                  },
                ),
            ],
          ),
        if (d != null && d.history.length > 1)
          TkSection(
            title: 'What happened',
            inset: 56,
            children: [
              for (final h in d.history)
                TkListRow(
                  title: _statusWords[h.status] ?? h.status,
                  sub: listTime(h.at, now),
                  icon: 'b-clock',
                  square: switch (h.status) {
                    'failed' => look.red,
                    'pending' => look.blue,
                    'reversed' => t.muted,
                    _ => look.lime,
                  },
                  amount: h.amountKobo == null ? null : naira(h.amountKobo!),
                  chevron: false,
                ),
            ],
          ),
      ],
    );
  }
}
