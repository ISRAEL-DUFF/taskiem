// Money (web Money.tsx, MoneyParts.tsx, ReceiveMoney.tsx ReceiveMoneyCard;
// lib/money.ts, lib/moneyChart.ts): a period's money in and out, settled
// and pending, its bars and where it came from, what to look at (money to
// match, leaks, fees, invoices paid, money collected for others), by
// channel and by rider, and its CSV exports through the phone's share
// sheet; the leaks, assigned and closed; a bank statement uploaded from
// the phone's files, its columns matched, the lines that wait; cash and
// expenses; the fees agreed with each connection.

import 'dart:convert';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/services.dart' show Clipboard, ClipboardData;
import 'package:flutter/widgets.dart';

import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords, nairaKobo;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow;
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart' show handOffFile;
import 'invoice_match.dart' show InvoiceMatchPage, channelWords, unmatchedPath;
import 'invoices_page.dart' show InvoicesPage, openInvoice;
import 'money_sheets.dart' show dateLabel;
import 'product_form.dart' show formKey;
import 'stock_page.dart' show feeInput;
import 'words.dart' show CardTone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

void _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Opens Money.
void openMoney(BuildContext context, Auth auth) => _push(context, MoneyPage(auth: auth));

/// money.ts counted.
String counted(int n, String one, [String? many]) => '$n ${n == 1 ? one : (many ?? '${one}s')}';

/// money.ts feeRatePercent: 150 basis points is "1.50%".
String feeRatePercent(Object? bp) => bp is num ? '${(bp / 100).toStringAsFixed(2)}%' : '';

// --- the period ---

typedef Period = ({String from, String to});

const periodChoices = [
  ('today', 'Today'),
  ('yesterday', 'Yesterday'),
  ('week', 'This week'),
  ('month', 'This month'),
  ('last_month', 'Last month'),
  ('custom', 'Choose dates'),
];

String _day(DateTime d) => '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

/// money.ts lagosDate: the date in Lagos at a moment.
String lagosDate(DateTime now) => _day(now.toUtc().add(const Duration(hours: 1)));

String _addDays(String day, int n) => _day(DateTime.parse('${day}T00:00:00Z').add(Duration(days: n)));

/// money.ts isDay.
bool isDay(String text) {
  if (!RegExp(r'^\d{4}-\d{2}-\d{2}$').hasMatch(text)) return false;
  final d = DateTime.tryParse('${text}T00:00:00Z');
  return d != null && _day(d) == text;
}

/// money.ts periodFor: a week starts on Monday; days are Lagos's.
Period periodFor(String choice, DateTime now, [Period custom = (from: '', to: '')]) {
  final today = lagosDate(now);
  switch (choice) {
    case 'yesterday':
      final y = _addDays(today, -1);
      return (from: y, to: y);
    case 'week':
      final weekday = DateTime.parse('${today}T00:00:00Z').weekday - 1;
      return (from: _addDays(today, -weekday), to: today);
    case 'month':
      return (from: '${today.substring(0, 8)}01', to: today);
    case 'last_month':
      final end = _addDays('${today.substring(0, 8)}01', -1);
      return (from: '${end.substring(0, 8)}01', to: end);
    case 'custom':
      return custom;
    default:
      return (from: today, to: today);
  }
}

/// money.ts periodProblem.
String? periodProblem(Period p) {
  if (p.from.isEmpty) return 'Choose the first day.';
  if (!isDay(p.from)) return 'The first day does not look right.';
  if (p.to.isEmpty) return 'Choose the last day.';
  if (!isDay(p.to)) return 'The last day does not look right.';
  if (p.to.compareTo(p.from) < 0) return 'The last day is on or after the first.';
  final days = DateTime.parse('${p.to}T00:00:00Z').difference(DateTime.parse('${p.from}T00:00:00Z')).inDays + 1;
  if (days > 400) return 'A period is at most 400 days.';
  return null;
}

/// money.ts periodWords.
String periodWords(Period p) => p.from == p.to ? dateLabel(p.from) : '${dateLabel(p.from)} to ${dateLabel(p.to)}';

/// money.ts riderDayLine.
String riderDayLine(Map<String, dynamic> r) => [
  '${_int(r['delivered'])} delivered',
  if (_int(r['door_transfer_kobo']) > 0) '${naira(_int(r['door_transfer_kobo']))} paid at the door',
  if (_int(r['cash_kobo']) > 0) '${naira(_int(r['cash_kobo']))} cash',
  if (_int(r['remitted_kobo']) > 0) '${naira(_int(r['remitted_kobo']))} handed in',
  if (_int(r['fees_kobo']) > 0) 'fees ${naira(_int(r['fees_kobo']))}',
  if (r['rating'] is num) 'rated ${(r['rating'] as num).toStringAsFixed(1)}',
].join(' · ');

/// money.ts collectedForLine.
String collectedForLine(Map<String, dynamic> s) => [
  for (final b in _maps(s['collected_for']))
    if (_int(b['kobo']) > 0) '${naira(_int(b['kobo']))} for ${b['body']}',
].join(', ');

const _weekdays = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const _weekdaysLong = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'];
const _monthsLong = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

DateTime _lagos(String iso) => (DateTime.tryParse(iso) ?? DateTime.utc(1970)).toUtc().add(const Duration(hours: 1));

/// moneyChart.ts hourWords.
String hourWords(int h) => h == 0 || h == 24 ? 'midnight' : (h == 12 ? 'noon' : (h < 12 ? '$h am' : '${h - 12} pm'));

/// moneyChart.ts bucketShort.
String bucketShort(String from, String unit) => unit == 'hour' ? '${_lagos(from).hour}' : _weekdays[_lagos(from).weekday - 1];

/// moneyChart.ts bucketLong.
String bucketLong(String from, String unit, DateTime now) {
  final d = _lagos(from);
  if (unit == 'hour') return '${hourWords(d.hour)} to ${hourWords(d.hour + 1)}';
  if (_day(d) == lagosDate(now)) return 'Today';
  return '${_weekdaysLong[d.weekday - 1]} ${d.day} ${_monthsLong[d.month - 1]}';
}

/// moneyChart.ts openBucket: the last that has begun.
int openBucket(List<Map<String, dynamic>> buckets, DateTime now) {
  for (var i = buckets.length - 1; i >= 0; i--) {
    final d = DateTime.tryParse(_str(buckets[i]['from']));
    if (d != null && !d.isAfter(now)) return i;
  }
  return buckets.length - 1;
}

/// money.ts EXPORTS.
const moneyExports = [
  ('log', 'Money log', 'Every movement of money in the period, as the log holds it.'),
  ('invoices', 'Invoices', 'The invoices sent in the period, with what was paid on each.'),
  ('journal', 'Accounting journal', 'Double-entry lines a bookkeeping tool can import; debits equal credits.'),
];

Widget _notice(TkTokens t, String text) => Semantics(
  liveRegion: true,
  container: true,
  child: Container(
    width: double.infinity,
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
    decoration: BoxDecoration(color: t.tkLimeSoft, borderRadius: BorderRadius.circular(14)),
    child: Text(text, style: tkText(size: 15, color: t.text)),
  ),
);

Widget _problemText(TkTokens t, String text) => Semantics(
  liveRegion: true,
  child: Padding(
    padding: const EdgeInsets.only(top: 8),
    child: Text(text, style: tkText(size: 13, weight: 600, color: t.danger)),
  ),
);

// --- overview ---

/// Money > Overview: a period's money.
class MoneyPage extends StatefulWidget {
  const MoneyPage({super.key, required this.auth, this.now, this.tab = false});
  final Auth auth;

  /// The bottom bar's Money tab: no Back.
  final bool tab;

  /// The clock (a test sets it).
  final DateTime Function()? now;

  @override
  State<MoneyPage> createState() => _MoneyPageState();
}

class _MoneyPageState extends State<MoneyPage> with Polling {
  String _choice = 'today';
  final _from = TextEditingController();
  final _to = TextEditingController();
  Map<String, dynamic>? _sum;
  String? _error;
  List<Map<String, dynamic>> _waiting = const [];
  Map<String, dynamic>? _receive;
  int _asked = 0;

  DateTime get _now => (widget.now ?? DateTime.now)();
  Period get _period => periodFor(_choice, _now, (from: _from.text.trim(), to: _to.text.trim()));
  String? get _periodProblem => _choice == 'custom' ? periodProblem(_period) : null;

  @override
  void initState() {
    super.initState();
    // Read again when a date's text changes (not its cursor), once it is a
    // period the server takes.
    var was = '';
    for (final c in [_from, _to]) {
      c.addListener(() {
        final now = '${_from.text}|${_to.text}';
        if (now == was) return;
        was = now;
        setState(() {});
        if (_choice == 'custom' && _periodProblem == null) _load();
      });
    }
    _load();
    _loadSide();
    startPolling(const Duration(seconds: 30), _load);
  }

  @override
  void dispose() {
    _from.dispose();
    _to.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    if (_periodProblem != null) return;
    final p = _period;
    final asked = ++_asked;
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/money/summary', query: {'from': p.from, 'to': p.to}) as Map).cast<String, dynamic>();
      if (mounted && asked == _asked) setState(() => (_sum = r, _error = null));
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _loadSide() async {
    final t = widget.auth.transport;
    try {
      final r = (await t.send('GET', unmatchedPath) as Map).cast<String, dynamic>();
      if (mounted) setState(() => _waiting = _maps(r['payments']));
    } on Object {
      // Nothing waiting, as far as is known.
    }
    try {
      final r = (await t.send('GET', '/v1/settings/receive-money') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _receive = r);
    } on Object {
      // No card.
    }
  }

  void _choose(String v) {
    // Choose dates starts from the period chosen before (its old dates are
    // dropped on leaving it, as the web's address drops them).
    if (v == 'custom' && _choice != 'custom') {
      final p = _period;
      _from.text = p.from;
      _to.text = p.to;
    }
    setState(() => (_choice = v, _sum = null));
    _load();
  }

  Future<void> _export(String kind, Period p, bool reveal) async {
    try {
      final q = Uri(queryParameters: {'from': p.from, 'to': p.to, if (reveal && kind == 'invoices') 'reveal': 'true'}).query;
      final bytes = await widget.auth.transport.bytes('/v1/money/exports/$kind?$q');
      await handOffFile(Uint8List.fromList(bytes), '$kind-${p.from}-to-${p.to}.csv', 'text/csv', null);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    }
  }

  void _exports() {
    final p = _period;
    showTkModal<void>(
      context,
      title: 'Download ${periodWords(p)}',
      builder: (_) => _Exports(reveals: widget.auth.can('pii.reveal'), export: (kind, reveal) => _export(kind, p, reveal)),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final p = _period;
    final problem = _periodProblem;
    final s = problem == null ? _sum : null;
    final channels = s == null ? const <Map<String, dynamic>>[] : _maps(s['channels']);
    final riders = s == null ? const <Map<String, dynamic>>[] : _maps(s['riders']);
    final buckets = s == null ? const <Map<String, dynamic>>[] : _maps(s['buckets']);
    final rate = feeRatePercent(s?['fee_rate_bp']);
    final inPending = _int(s?['in_pending_kobo']);
    final outPending = _int(s?['out_pending_kobo']);
    final unmatched = _waiting.fold(0, (n, w) => n + (w['left_kobo'] is num ? _int(w['left_kobo']) : _int(w['amount_kobo'])));
    return TkScreen(
      title: 'Money',
      back: !widget.tab,
      actions: [if (problem == null) TkHeadButton(icon: 'file', label: 'Download as a spreadsheet', onTap: _exports)],
      under: TkChips<String>(
        label: 'Period',
        chips: [for (final (v, words) in periodChoices) (v, v == 'custom' ? 'Pick dates' : words)],
        on: _choice,
        onPick: _choose,
      ),
      onRefresh: () async {
        await _load();
        await _loadSide();
      },
      children: [
        if (_choice == 'custom')
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(
                    child: TkKitField(
                      label: 'From',
                      child: TkTextBox(controller: _from, hint: '2026-11-01'),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: TkKitField(
                      label: 'To',
                      child: TkTextBox(controller: _to, hint: '2026-11-12'),
                    ),
                  ),
                ],
              ),
              if (problem != null) _problemText(t, problem),
            ],
          ),
        if (_error != null) TkErrorBox(_error!),
        if (problem == null && s == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading the period’s money…'),
        if (s != null)
          TkSummary(
            key: ValueKey('sum ${p.from} ${p.to}'),
            label: 'Kept · ${periodWords(p)}',
            kobo: _int(s['in_settled_kobo']) - _int(s['out_settled_kobo']),
            figures: [
              (label: 'Money in', value: naira(_int(s['in_settled_kobo'])), dot: t.chartIn),
              (label: 'Money out', value: naira(_int(s['out_settled_kobo'])), dot: t.chartOut),
            ],
            foot: inPending > 0 || outPending > 0
                ? [if (inPending > 0) '${naira(inPending)} more coming in', if (outPending > 0) '${naira(outPending)} more going out'].join(' · ')
                : null,
          ),
        TkTiles(
          tiles: [
            (icon: 'split', label: 'Match payments', badge: _waiting.length, onTap: () => _push(context, InvoiceMatchPage(auth: widget.auth)), semantics: null),
            (icon: 'alerts', label: 'Leaks', badge: _int(s?['leaks_open']), onTap: () => _push(context, MoneyLeaksPage(auth: widget.auth)), semantics: null),
            (icon: 'invoices', label: 'Invoices', badge: 0, onTap: () => _push(context, InvoicesPage(auth: widget.auth)), semantics: null),
            (icon: 'file', label: 'Bank statement', badge: 0, onTap: () => _push(context, MoneyStatementsPage(auth: widget.auth)), semantics: 'Upload statement'),
            (icon: 'note', label: 'Cash and expenses', badge: 0, onTap: () => _push(context, MoneyEntriesPage(auth: widget.auth)), semantics: null),
            (icon: 'card', label: 'Fees', badge: 0, onTap: () => _push(context, MoneyFeesPage(auth: widget.auth)), semantics: null),
          ],
        ),
        if (s != null && _str(s['unit']).isNotEmpty && buckets.length > 1)
          TkSection(
            title: _str(s['unit']) == 'hour' ? 'Hour by hour' : 'Day by day',
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(14, 14, 14, 10),
                child: MoneyBars(key: ValueKey('${p.from} ${p.to}'), buckets: buckets, unit: _str(s['unit']), now: _now),
              ),
            ],
          ),
        if (s != null && (_waiting.isNotEmpty || _int(s['leaks_open']) > 0))
          TkSection(
            title: 'Needs a look',
            children: [
              if (_waiting.isNotEmpty)
                TkListRow(
                  title: _waiting.length == 1 ? '1 payment not matched' : '${_waiting.length} payments not matched',
                  sub: 'No invoice for it yet',
                  icon: 'split',
                  tone: CardTone.warn,
                  amount: naira(unmatched),
                  onTap: () => _push(context, InvoiceMatchPage(auth: widget.auth)),
                ),
              if (_int(s['leaks_open']) > 0)
                TkListRow(
                  title: '${counted(_int(s['leaks_open']), 'leak')} open',
                  sub: 'Recovered so far: ${naira(_int(s['recovered_kobo']))}',
                  icon: 'alerts',
                  tone: CardTone.warn,
                  amount: naira(_int(s['leaks_open_kobo'])),
                  onTap: () => _push(context, MoneyLeaksPage(auth: widget.auth)),
                ),
            ],
          ),
        if (s != null && _int(s['in_settled_kobo']) > 0 && channels.any((c) => _int(c['in_settled_kobo']) > 0))
          TkSection(
            title: 'Where it came from',
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 14, 16, 4),
                child: Column(
                  children: [
                    for (final c in channels.where((c) => _int(c['in_settled_kobo']) > 0)) _share(t, c, _int(s['in_settled_kobo'])),
                  ],
                ),
              ),
            ],
          ),
        if (s != null)
          TkSection(
            title: 'In this period',
            children: [
              TkListRow(
                title: 'Fees taken',
                sub: rate.isNotEmpty ? '$rate of the money they were taken on' : 'What each payments account charges',
                icon: 'card',
                amount: naira(_int(s['fees_kobo'])),
                onTap: () => _push(context, MoneyFeesPage(auth: widget.auth)),
              ),
              TkListRow(
                title: 'Invoices paid',
                sub: counted(_int(s['invoices_paid']), 'invoice'),
                icon: 'invoices',
                amount: naira(_int(s['invoices_paid_kobo'])),
                onTap: () => _push(context, InvoicesPage(auth: widget.auth)),
              ),
              if (_int(s['leaks_opened']) > 0)
                TkListRow(
                  title: 'Leaks found',
                  sub: counted(_int(s['leaks_opened']), 'leak'),
                  icon: 'alerts',
                  amount: naira(_int(s['leaks_opened_kobo'])),
                  onTap: () => _push(context, MoneyLeaksPage(auth: widget.auth)),
                ),
              if (_int(s['collected_for_others_kobo']) > 0)
                TkListRow(
                  title: 'Collected for others',
                  sub: '${collectedForLine(s)}. Your own: ${naira(_int(s['own_in_settled_kobo']))}',
                  icon: 'schools',
                  amount: naira(_int(s['collected_for_others_kobo'])),
                ),
            ],
          ),
        if (s != null)
          TkSection(
            title: 'By channel',
            empty: 'No money moved in this period.',
            children: [
              for (final c in channels)
                TkListRow(
                  title: channelWords(_str(c['channel'])),
                  letters: channelWords(_str(c['channel'])),
                  semanticsLabel: '${channelWords(_str(c['channel']))}, ${c['channel']}',
                  sub: [
                    'Out ${naira(_int(c['out_settled_kobo']))}',
                    'fees ${naira(_int(c['fees_kobo']))}',
                    counted(_int(c['movements']), 'movement'),
                  ].join(' · '),
                  amount: naira(_int(c['in_settled_kobo'])),
                  status: _int(c['in_pending_kobo']) > 0 ? '${naira(_int(c['in_pending_kobo']))} pending' : null,
                  statusTone: CardTone.warn,
                ),
            ],
          ),
        if (riders.isNotEmpty)
          TkSection(
            title: 'Riders',
            children: [
              for (final r in riders)
                TkListRow(
                  title: _str(r['rider']),
                  sub: riderDayLine(r),
                  status: _int(r['owed_kobo']) > 0 ? 'owes ${naira(_int(r['owed_kobo']))}' : null,
                  statusTone: CardTone.warn,
                ),
            ],
          ),
        if (_receive?['on'] == true) _ReceiveCard(r: _receive!),
      ],
    );
  }

  /// A channel's share of the money in, as a bar.
  Widget _share(TkTokens t, Map<String, dynamic> c, int total) {
    final share = (100 * _int(c['in_settled_kobo']) / total).round();
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: MergeSemantics(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(channelWords(_str(c['channel'])), maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14.5, weight: 600, color: t.text)),
                ),
                Text('$share%  ', style: tkText(size: 13, color: t.muted)),
                Text(naira(_int(c['in_settled_kobo'])), style: tkText(size: 14.5, weight: 700, color: t.text)),
              ],
            ),
            const SizedBox(height: 6),
            Semantics(
              label: '$share% of the money in',
              child: ClipRRect(
                borderRadius: BorderRadius.circular(6),
                child: SizedBox(
                  height: 8,
                  child: Stack(
                    children: [
                      Container(color: t.tkSoft),
                      TweenAnimationBuilder<double>(
                        tween: Tween(begin: 0, end: (share < 2 ? 2 : share) / 100),
                        duration: (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : const Duration(milliseconds: 700),
                        curve: Curves.easeOutCubic,
                        builder: (context, w, _) => FractionallySizedBox(
                          widthFactor: w,
                          child: Container(
                            decoration: BoxDecoration(color: t.chartIn, borderRadius: BorderRadius.circular(6)),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The CSV files of a period, in a modal: each shared through the phone's
/// share sheet.
class _Exports extends StatefulWidget {
  const _Exports({required this.reveals, required this.export});
  final bool reveals;
  final Future<void> Function(String kind, bool reveal) export;

  @override
  State<_Exports> createState() => _ExportsState();
}

class _ExportsState extends State<_Exports> {
  bool _reveal = false;
  String? _getting;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('A CSV file for your accountant or a spreadsheet: share it, or keep it in your files.', style: tkHint(t)),
        const SizedBox(height: 8),
        for (final (kind, label, help) in moneyExports)
          TkListRow(
            title: _getting == kind ? 'Getting it…' : label,
            sub: help,
            icon: 'file',
            semanticsLabel: 'Download ${label.toLowerCase()}',
            onTap: _getting != null
                ? null
                : () async {
                    setState(() => _getting = kind);
                    await widget.export(kind, _reveal);
                    if (mounted) setState(() => _getting = null);
                  },
          ),
        if (widget.reveals)
          TkChoiceRow(
            control: TkCheckbox(checked: _reveal),
            label: Text('Invoices with customers’ names (the access is recorded)', style: tkText(size: 15, color: t.text)),
            onTap: () => setState(() => _reveal = !_reveal),
          ),
      ],
    );
  }
}

/// ReceiveMoneyCard: the business's own account number, with Taskiem.
class _ReceiveCard extends StatelessWidget {
  const _ReceiveCard({required this.r});
  final Map<String, dynamic> r;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = r['account'] is Map ? (r['account'] as Map).cast<String, dynamic>() : null;
    final number = _str(a?['account_number']);
    final spaced = number.length == 10 ? '${number.substring(0, 4)} ${number.substring(4, 7)} ${number.substring(7)}' : number;
    final sub = a == null
        ? (_str(r['account_problem']).isNotEmpty ? _str(r['account_problem']) : 'Asking the bank for your number…')
        : [_str(a['bank_name']), _str(a['account_name'])].where((x) => x.isNotEmpty).join(' · ');
    return TkSection(
      title: 'Your account number',
      trailing: a == null
          ? null
          : TkHeadButton(
              icon: 'copy',
              label: 'Copy your account number',
              onTap: () async {
                await Clipboard.setData(ClipboardData(text: number));
                if (context.mounted) showTkToast(context, 'Copied');
              },
            ),
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(a == null ? 'Not ready yet' : spaced, style: tkText(size: 24, weight: 700, letterSpacing: 0.5, color: t.text)),
              if (sub.isNotEmpty) Text(sub, style: tkText(size: 14, color: t.muted)),
              const SizedBox(height: 8),
              Text('Your balance and withdrawals are on the web, under Settings, Payments.', style: tkHint(t)),
            ],
          ),
        ),
      ],
    );
  }
}

/// MoneyBars: a period's money in and out, a pair of bars a day (or an
/// hour); pressing one shows its figures. Up to 14 can be pressed; more
/// are drawn, the figures in the list under them.
class MoneyBars extends StatefulWidget {
  const MoneyBars({super.key, required this.buckets, required this.unit, required this.now});
  final List<Map<String, dynamic>> buckets;
  final String unit;
  final DateTime now;

  @override
  State<MoneyBars> createState() => _MoneyBarsState();
}

class _MoneyBarsState extends State<MoneyBars> {
  late int _picked = openBucket(widget.buckets, widget.now);
  bool _figures = false;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = widget.buckets;
    if (b.isEmpty) return const SizedBox.shrink();
    final max = b.fold<int>(1, (m, x) => [m, _int(x['in_settled_kobo']), _int(x['out_settled_kobo'])].reduce((a, c) => a > c ? a : c));
    final pressable = b.length <= 14;
    final at = b[_picked.clamp(0, b.length - 1)];
    double h(int k) => k <= 0 ? 0 : (112 * k / max).clamp(3, 112).toDouble();
    Widget bar(int k, Color c) => Container(
      width: 6,
      height: h(k),
      decoration: BoxDecoration(
        color: c,
        borderRadius: const BorderRadius.vertical(top: Radius.circular(2)),
      ),
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (pressable)
          Semantics(
            liveRegion: true,
            child: Text.rich(
              TextSpan(
                children: [
                  TextSpan(
                    text: '${bucketLong(_str(at['from']), widget.unit, widget.now)}  ',
                    style: tkText(size: 14, weight: 700, color: t.text),
                  ),
                  TextSpan(text: 'In ${naira(_int(at['in_settled_kobo']))} · Out ${naira(_int(at['out_settled_kobo']))}', style: tkHint(t)),
                ],
              ),
            ),
          ),
        const SizedBox(height: 8),
        ExcludeSemantics(
          excluding: !pressable,
          child: SizedBox(
            height: 132,
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                for (var i = 0; i < b.length; i++)
                  Expanded(
                    child: pressable
                        ? Semantics(
                            button: true,
                            selected: i == _picked,
                            label:
                                '${bucketLong(_str(b[i]['from']), widget.unit, widget.now)}: in ${naira(_int(b[i]['in_settled_kobo']))}, out ${naira(_int(b[i]['out_settled_kobo']))}',
                            excludeSemantics: true,
                            onTap: () => setState(() => _picked = i),
                            child: GestureDetector(
                              behavior: HitTestBehavior.opaque,
                              onTap: () => setState(() => _picked = i),
                              child: _barPair(t, b[i], i, bar),
                            ),
                          )
                        : _barPair(t, b[i], i, bar),
                  ),
              ],
            ),
          ),
        ),
        ExcludeSemantics(
          child: Row(
            children: [
              for (var i = 0; i < b.length; i++)
                Expanded(
                  child: Text(
                    b.length > 14 && i % (b.length / 7).ceil() != 0 ? '' : bucketShort(_str(b[i]['from']), widget.unit),
                    textAlign: TextAlign.center,
                    maxLines: 1,
                    overflow: TextOverflow.clip,
                    style: tkText(size: 11, weight: i == _picked && pressable ? 700 : 500, color: i == _picked && pressable ? t.text : t.muted),
                  ),
                ),
            ],
          ),
        ),
        const SizedBox(height: 8),
        Align(
          alignment: Alignment.centerLeft,
          child: Semantics(
            expanded: _figures,
            child: TkLink(label: _figures ? 'Hide the figures' : 'See the figures', onTap: () => setState(() => _figures = !_figures)),
          ),
        ),
        if (_figures)
          for (final x in b)
            MergeSemantics(
              child: Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Text(
                  '${bucketLong(_str(x['from']), widget.unit, widget.now)}: in ${naira(_int(x['in_settled_kobo']))}, out ${naira(_int(x['out_settled_kobo']))}, ${counted(_int(x['payments']), 'payment')}',
                  style: tkText(size: 14, color: t.text),
                ),
              ),
            ),
      ],
    );
  }

  Widget _barPair(TkTokens t, Map<String, dynamic> x, int i, Widget Function(int, Color) bar) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 1, vertical: 4),
    decoration: BoxDecoration(color: i == _picked && widget.buckets.length <= 14 ? t.tkSoft : null, borderRadius: BorderRadius.circular(6)),
    child: Row(
      mainAxisAlignment: MainAxisAlignment.center,
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [bar(_int(x['in_settled_kobo']), t.chartIn), const SizedBox(width: 1), bar(_int(x['out_settled_kobo']), t.chartOut)],
    ),
  );
}

// --- leaks ---

/// money.ts leakKindWords.
String leakKindWords(String kind) => switch (kind) {
  'unmatched' => 'Money with no invoice',
  'overdue' => 'Overdue invoice',
  'duplicate' => 'Recorded twice',
  'overpaid' => 'Invoice paid too much',
  'void_paid' => 'Paid for a void invoice',
  'fee' => 'Fee above the agreed rate',
  'rider_owes' => 'Cash a rider owes',
  'stock_short' => 'Sold more than the stock',
  'stock_negative' => 'Stock below zero',
  'stock_count' => 'Missing at a count',
  _ => kind.replaceAll('_', ' '),
};

/// money.ts leakStatus.
({String label, CardTone tone}) leakStatus(String s) => switch (s) {
  'open' => (label: 'Open', tone: CardTone.warn),
  'recovered' => (label: 'Recovered', tone: CardTone.lime),
  'written_off' => (label: 'Written off', tone: CardTone.plain),
  'not_a_leak' => (label: 'Not a leak', tone: CardTone.plain),
  'resolved' => (label: 'Resolved by itself', tone: CardTone.plain),
  _ => (label: s.replaceAll('_', ' '), tone: CardTone.plain),
};

/// money.ts whoWords.
String whoWords(String actor, Map<String, String> names, String? me) {
  if (actor.isEmpty) return '';
  if (me != null && actor == me) return 'you';
  if (actor.startsWith('system:')) return 'Taskiem';
  if (actor.startsWith('key:')) return 'an API key';
  if (actor.startsWith('agent:')) return 'field agent ${actor.substring(6)}';
  return names[actor] ?? 'a member';
}

/// money.ts closedLine.
String closedLine(Map<String, dynamic> l, Map<String, String> names, String? me) {
  final s = _str(l['status']);
  if (s == 'open') return '';
  if (s == 'resolved') return 'Closed by itself: the cause went away (the money was matched or the invoice paid).';
  final by = whoWords(_str(l['closed_by']), names, me);
  final how = s == 'recovered' ? 'Recovered ${naira(_int(l['recovered_kobo']))}' : leakStatus(s).label;
  return by.isNotEmpty ? '$how, closed by $by' : how;
}

/// money.ts closeProblem.
String? closeProblem(String status, String recovered, String note) {
  if (status.isEmpty) return 'Choose how it was closed.';
  if (status == 'recovered') {
    if (recovered.trim().isEmpty) return 'Say how much was recovered.';
    final k = nairaKobo(recovered);
    if (k == null || k <= 0) return 'The amount recovered is a number of naira, such as 25000 or 25000.50.';
  }
  if (note.trim().length > 500) return 'The note is at most 500 characters.';
  return null;
}

const closeChoices = [
  ('recovered', 'Recovered', 'The money came back. Say how much.'),
  ('written_off', 'Written off', 'It is lost, and you have stopped chasing it.'),
  ('not_a_leak', 'Not a leak', 'Nothing was lost: Taskiem was wrong about this one.'),
];

/// The server's words as a sentence (invoices.ts plainMessage): its capital.
String plainSentence(String s) {
  final t = s.trim();
  if (t.isEmpty) return t;
  final first = t[0].toUpperCase() + t.substring(1);
  return RegExp(r'[.!?]$').hasMatch(first) ? first : '$first.';
}

/// Money > Leaks: what Taskiem found, the totals, assigned and closed.
class MoneyLeaksPage extends StatefulWidget {
  const MoneyLeaksPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<MoneyLeaksPage> createState() => _MoneyLeaksPageState();
}

class _MoneyLeaksPageState extends State<MoneyLeaksPage> with Polling {
  String _status = 'open';
  List<Map<String, dynamic>>? _leaks;
  Map<String, dynamic>? _totals;
  String? _error;
  List<Map<String, dynamic>> _people = const [];
  String _said = '';
  int _asked = 0;

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 30), _load);
    if (widget.auth.can('member.manage')) {
      widget.auth.transport.send('GET', '/v1/members').then((r) {
        if (mounted) setState(() => _people = _maps((r as Map)['members']));
      }, onError: (Object _) {});
    }
  }

  Future<void> _load() async {
    final asked = ++_asked;
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/money/leaks', query: {'status': _status}) as Map).cast<String, dynamic>();
      if (mounted && asked == _asked) setState(() => (_leaks = _maps(r['leaks']), _totals = (r['totals'] as Map?)?.cast<String, dynamic>(), _error = null));
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final leaks = _leaks;
    final tt = _totals;
    final manage = widget.auth.can('invoice.manage');
    final user = widget.auth.me?.user;
    final myId = user?.id;
    final names = {for (final m in _people) _str(m['user_id']): _str(m['name']).isNotEmpty ? _str(m['name']) : _str(m['email'])};
    return TkPageScreen(
      title: 'Leaks',
      description: 'Money your business may be losing, found by Taskiem: payments with no invoice, overdue invoices, money paid twice or too much, and fees above what you agreed.',
      backLabel: 'Back to Money',
      onRefresh: _load,
      children: [
        if (tt != null)
          TkPageCard(
            children: [
              for (final (k, v) in [
                ('Open leaks', '${_int(tt['open'])}'),
                ('Open, in naira', naira(_int(tt['open_kobo']))),
                ('Recovered so far', naira(_int(tt['recovered_kobo']))),
              ])
                MergeSemantics(
                  child: Padding(
                    padding: const EdgeInsets.only(bottom: 6),
                    child: Row(
                      children: [
                        Expanded(
                          child: Text(k, style: tkText(size: 14, color: t.muted)),
                        ),
                        Text(v, style: tkText(size: 17, weight: 700, color: t.text)),
                      ],
                    ),
                  ),
                ),
            ],
          ),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final (v, words) in const [('open', 'Open'), ('closed', 'Closed'), ('all', 'All')])
              Semantics(
                selected: _status == v,
                child: TkSmallButton(
                  label: words,
                  primary: _status == v,
                  onPressed: () async {
                    if (v == _status) return;
                    setState(() => (_status = v, _leaks = null));
                    await _load();
                  },
                ),
              ),
          ],
        ),
        if (_said.isNotEmpty) _notice(t, _said),
        if (_error != null) TkErrorBox(_error!),
        if (leaks == null && _error == null) const TkLoading(rows: 3, label: 'Loading leaks…'),
        if (leaks != null && leaks.isEmpty)
          TkEmpty(
            icon: 'money',
            title: _status == 'open' ? 'No open leaks' : 'No leaks to show.',
            line: _status == 'open' ? 'Taskiem looks every 15 minutes and lists here anything that may be costing you money.' : null,
          ),
        if (leaks != null)
          for (final l in leaks)
            _LeakCard(
              key: ValueKey(l['id']),
              auth: widget.auth,
              leak: l,
              manage: manage,
              me: myId == null ? null : (id: myId, name: (user?.name ?? '').isNotEmpty ? user!.name! : (user?.email ?? '')),
              people: _people,
              names: names,
              onDone: (line) {
                setState(() => _said = line);
                _load();
              },
            ),
        if (!manage && leaks != null && leaks.isNotEmpty) Text('You can see these. Someone who manages invoices can assign and close them.', style: tkHint(t)),
      ],
    );
  }
}

class _LeakCard extends StatefulWidget {
  const _LeakCard({
    super.key,
    required this.auth,
    required this.leak,
    required this.manage,
    required this.me,
    required this.people,
    required this.names,
    required this.onDone,
  });
  final Auth auth;
  final Map<String, dynamic> leak;
  final bool manage;
  final ({String id, String name})? me;
  final List<Map<String, dynamic>> people;
  final Map<String, String> names;
  final void Function(String line) onDone;

  @override
  State<_LeakCard> createState() => _LeakCardState();
}

class _LeakCardState extends State<_LeakCard> {
  late String _to = _str(widget.leak['assigned_to']);
  bool _closing = false;
  String _how = '';
  final _recovered = TextEditingController();
  final _note = TextEditingController();
  bool _tried = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _recovered.addListener(() => setState(() {}));
    _note.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _recovered.dispose();
    _note.dispose();
    super.dispose();
  }

  Future<void> _run(Future<String> Function() f) async {
    setState(() => (_busy = true, _error = null));
    try {
      final line = await f();
      if (mounted) setState(() => _busy = false);
      widget.onDone(line);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final l = widget.leak;
    final id = _str(l['id']);
    final s = leakStatus(_str(l['status']));
    final open = l['status'] == 'open';
    final kind = leakKindWords(_str(l['kind']));
    final me = widget.me;
    final choices = <({String id, String name})>[
      if (widget.people.isNotEmpty)
        for (final m in widget.people) (id: _str(m['user_id']), name: _str(m['name']).isNotEmpty ? _str(m['name']) : _str(m['email']))
      else
        ?me,
    ];
    final assigned = _str(l['assigned_to']);
    if (assigned.isNotEmpty && !choices.any((c) => c.id == assigned)) choices.add((id: assigned, name: whoWords(assigned, widget.names, me?.id)));
    String choiceLabel(String v) =>
        v.isEmpty ? 'No one' : (v == me?.id ? 'Me (${me!.name})' : (choices.where((c) => c.id == v).map((c) => c.name).firstOrNull ?? 'a member'));
    final problem = closeProblem(_how, _recovered.text, _note.text);
    Widget kv(String k, Widget v) => Padding(
      padding: const EdgeInsets.only(top: 6),
      child: MergeSemantics(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(k, style: tkText(size: 13, weight: 600, color: t.muted)),
            v,
          ],
        ),
      ),
    );
    return TkPageCard(
      children: [
        Wrap(
          spacing: 8,
          runSpacing: 4,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Text(naira(_int(l['amount_kobo'])), style: tkText(size: 18, weight: 700, color: t.text)),
            Text(kind, style: tkText(size: 15, color: t.text)),
            TkPill(s.label, tone: s.tone),
          ],
        ),
        const SizedBox(height: 6),
        Text(plainSentence(_str(l['detail'])), style: tkText(size: 15, color: t.text)),
        kv('Opened', Text(dateLabel(_str(l['opened_at'])), style: tkText(size: 15, color: t.text))),
        if (_str(l['invoice_id']).isNotEmpty)
          kv(
            'Invoice',
            Align(
              alignment: Alignment.centerLeft,
              child: TkLink(label: 'See the invoice', onTap: () => openInvoice(context, widget.auth, _str(l['invoice_id']))),
            ),
          ),
        if (open)
          kv(
            'Assigned to',
            Text(
              assigned.isNotEmpty ? whoWords(assigned, widget.names, me?.id) : 'No one',
              style: assigned.isNotEmpty ? tkText(size: 15, color: t.text) : tkHint(t),
            ),
          ),
        if (!open)
          kv(
            'Closed',
            Text(
              '${closedLine(l, widget.names, me?.id)}${_str(l['closed_at']).isNotEmpty ? ' on ${dateLabel(_str(l['closed_at']))}' : ''}',
              style: tkText(size: 15, color: t.text),
            ),
          ),
        if (_str(l['note']).isNotEmpty) kv('Note', Text(_str(l['note']), style: tkText(size: 15, color: t.text))),
        if (widget.manage && open) ...[
          const SizedBox(height: 12),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              TkSmallButton(
                label: 'Assign to: ${choiceLabel(_to)}',
                onPressed: () async {
                  final v = await showTkModalChoices(
                    context,
                    title: 'Assign to',
                    value: _to,
                    choices: [('', 'No one'), for (final c in choices) (c.id, c.id == me?.id ? 'Me (${c.name})' : c.name)],
                  );
                  if (v != null) setState(() => _to = v);
                },
              ),
              TkSmallButton(
                label: 'Assign',
                semantics: 'Assign: $kind',
                onPressed: _busy
                    ? null
                    : () => _run(() async {
                        await widget.auth.transport.send(
                          'POST',
                          '/v1/money/leaks/${Uri.encodeComponent(id)}/assign',
                          body: {'member': _to.isEmpty ? null : _to},
                        );
                        final who = _to == me?.id ? 'you' : (choices.where((c) => c.id == _to).map((c) => c.name).firstOrNull ?? 'a member');
                        return _to.isNotEmpty ? '$kind assigned to $who.' : '$kind assigned to no one.';
                      }),
              ),
              if (!_closing) TkSmallButton(label: 'Close…', semantics: 'Close: $kind', onPressed: () => setState(() => _closing = true)),
            ],
          ),
          if (_closing) ...[
            const SizedBox(height: 10),
            Text('How it was closed', style: tkText(size: 13, weight: 600, color: t.muted)),
            for (final (v, label, help) in closeChoices)
              TkChoiceRow(
                control: TkRadio(checked: _how == v),
                label: Text('$label. $help', style: tkText(size: 15, color: t.text)),
                onTap: () => setState(() => _how = v),
              ),
            if (_how == 'recovered')
              Padding(
                padding: const EdgeInsets.only(bottom: 10),
                child: TkKitField(
                  label: 'Amount recovered (₦)',
                  hint: 'The leak is ${naira(_int(l['amount_kobo']))}.',
                  child: TkTextBox(controller: _recovered, money: true),
                ),
              ),
            TkKitField(
              label: 'Note (optional)',
              child: TkTextBox(controller: _note, lines: 2, max: 500),
            ),
            if (_tried && problem != null) _problemText(t, problem),
            const SizedBox(height: 10),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                TkSmallButton(
                  label: 'Close leak',
                  primary: true,
                  onPressed: _busy
                      ? null
                      : () {
                          setState(() => _tried = true);
                          if (problem != null) return;
                          final kobo = _how == 'recovered' ? nairaKobo(_recovered.text) ?? 0 : null;
                          _run(() async {
                            await widget.auth.transport.send(
                              'POST',
                              '/v1/money/leaks/${Uri.encodeComponent(id)}/close',
                              body: {'status': _how, 'recovered_kobo': ?kobo, if (_note.text.trim().isNotEmpty) 'note': _note.text.trim()},
                            );
                            return _how == 'recovered'
                                ? '$kind closed: recovered ${naira(kobo!)}.'
                                : '$kind closed as ${leakStatus(_how).label.toLowerCase()}.';
                          });
                        },
                ),
                TkSmallButton(
                  label: 'Cancel',
                  onPressed: () => setState(() {
                    _closing = false;
                    _tried = false;
                    _how = '';
                    _recovered.clear();
                    _note.clear();
                  }),
                ),
              ],
            ),
          ],
        ],
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
      ],
    );
  }
}

// --- statements ---

/// money.ts CHANNEL_RE, channelProblem.
String? channelProblem(String channel, {bool optional = false}) {
  final c = channel.trim();
  if (c.isEmpty) return optional ? null : 'Name the account, such as gtbank/current.';
  if (!RegExp(r'^[a-z0-9][a-z0-9_.-]{0,40}(/[a-z0-9][a-z0-9_.-]{0,40})?$').hasMatch(c)) {
    return 'The account is written in small letters, digits, - or _, with one / between the bank and the account: gtbank/current.';
  }
  return null;
}

/// money.ts csvHeader: the column names in the file's first line.
List<String> csvHeader(String text) {
  final t = text.replaceFirst('﻿', '');
  final fields = <String>[];
  var field = StringBuffer();
  var quoted = false;
  for (var i = 0; i < t.length; i++) {
    final c = t[i];
    if (quoted) {
      if (c == '"') {
        if (i + 1 < t.length && t[i + 1] == '"') {
          field.write('"');
          i++;
        } else {
          quoted = false;
        }
      } else {
        field.write(c);
      }
      continue;
    }
    if (c == '"') {
      quoted = true;
    } else if (c == ',') {
      fields.add(field.toString());
      field = StringBuffer();
    } else if (c == '\n' || c == '\r') {
      if (fields.isNotEmpty || field.toString().trim().isNotEmpty) break;
    } else {
      field.write(c);
    }
  }
  fields.add(field.toString());
  final seen = <String>{};
  return [
    for (final raw in fields)
      if (raw.trim().isNotEmpty && seen.add(raw.trim())) raw.trim(),
  ];
}

String _norm(String s) => s.trim().toLowerCase().split(RegExp(r'\s+')).join(' ');

final _guess = <String, List<RegExp>>{
  'date': [RegExp(r'^(trans(action)?\.? ?)?date$'), RegExp(r'^(posting|post|value|txn) date$'), RegExp('date')],
  'description': [RegExp(r'^description$'), RegExp('narration|narrative|remarks?|details|particulars|description')],
  'amount': [RegExp(r'^amount$'), RegExp(r'^(transaction |txn )?amount( \(ngn\)| ngn)?$')],
  'in': [
    RegExp(r'^(money in|credit|credits|cr|deposit|deposits|lodgement|lodgements|paid in|inflow)( \(ngn\)| amount)?$'),
    RegExp('credit|money in|deposit|inflow'),
  ],
  'out': [RegExp(r'^(money out|debit|debits|dr|withdrawal|withdrawals|paid out|outflow)( \(ngn\)| amount)?$'), RegExp('debit|money out|withdrawal|outflow')],
  'reference': [RegExp(r'^(reference|ref|ref\.? ?no\.?|reference number|transaction reference|session id)$'), RegExp('ref')],
  'balance': [RegExp(r'^(balance|closing balance|running balance)$'), RegExp('balance')],
};

/// money.ts columnsForm: the account's saved matching where the file has
/// those columns, else a guess from the names.
Map<String, Object> columnsForm(List<String> headers, Map<String, dynamic>? saved) {
  final taken = <String>{};
  String pick(String field) {
    final want = _str(saved?[field]);
    var h = want.isEmpty ? '' : (headers.where((x) => _norm(x) == _norm(want)).firstOrNull ?? '');
    if (h.isEmpty) {
      for (final re in _guess[field]!) {
        h = headers.where((x) => !taken.contains(x) && re.hasMatch(_norm(x))).firstOrNull ?? '';
        if (h.isNotEmpty) break;
      }
    }
    if (h.isNotEmpty) taken.add(h);
    return h;
  }

  final date = pick('date');
  final description = pick('description');
  final reference = pick('reference');
  final balance = pick('balance');
  final savedSplit = saved != null && _str(saved['amount']).isEmpty && (_str(saved['in']).isNotEmpty || _str(saved['out']).isNotEmpty);
  final amount = savedSplit ? '' : pick('amount');
  var inCol = '', outCol = '';
  if (amount.isEmpty) {
    inCol = pick('in');
    outCol = pick('out');
  }
  return {
    'date': date,
    'description': description,
    'split': amount.isEmpty && (inCol.isNotEmpty || outCol.isNotEmpty),
    'amount': amount,
    'in': inCol,
    'out': outCol,
    'reference': reference,
    'balance': balance,
  };
}

/// money.ts columnsProblem.
String? columnsProblem(Map<String, Object> f) {
  String v(String k) => f[k] as String? ?? '';
  final split = f['split'] == true;
  if (v('date').isEmpty) return 'Choose the column with each line’s date.';
  if (v('description').isEmpty) return 'Choose the column that says what each line was.';
  if (split) {
    if (v('in').isEmpty) return 'Choose the column with money in.';
    if (v('out').isEmpty) return 'Choose the column with money out.';
  } else if (v('amount').isEmpty) {
    return 'Choose the column with the amount.';
  }
  final used = [
    v('date'),
    v('description'),
    if (split) ...[v('in'), v('out')] else v('amount'),
    v('reference'),
    v('balance'),
  ].where((x) => x.isNotEmpty).toList();
  if (used.toSet().length != used.length) return 'Each column can be used for one thing only.';
  return null;
}

/// money.ts columnsRequest.
Map<String, String> columnsRequest(Map<String, Object> f) {
  String v(String k) => f[k] as String? ?? '';
  return {
    'date': v('date'),
    'description': v('description'),
    if (f['split'] == true) ...{'in': v('in'), 'out': v('out')} else 'amount': v('amount'),
    if (v('reference').isNotEmpty) 'reference': v('reference'),
    if (v('balance').isNotEmpty) 'balance': v('balance'),
  };
}

/// money.ts onlyOptionalMissing.
bool onlyOptionalMissing(List<String> missing, Map<String, dynamic> used) {
  final optional = [_str(used['reference']), _str(used['balance'])].where((c) => c.isNotEmpty).map((c) => c.trim().toLowerCase()).toList();
  return missing.isNotEmpty && missing.every((m) => optional.contains(m.trim().toLowerCase()));
}

/// money.ts statementRefusal: a 422 statement_unreadable answer.
({String message, List<String> missing, List<({int row, String why})> problems})? statementRefusal(Object e) {
  if (e is! ApiError || e.status != 422 || e.body['code'] != 'statement_unreadable') return null;
  return (
    message: plainSentence(_str(e.body['error']).isNotEmpty ? _str(e.body['error']) : e.message),
    missing: ((e.body['missing'] as List?) ?? const []).whereType<String>().toList(),
    problems: [
      for (final p in ((e.body['problems'] as List?) ?? const []).whereType<Map>())
        if (p['row'] is num) (row: (p['row'] as num).toInt(), why: '${p['why'] ?? ''}'),
    ],
  );
}

String _lines(int n) => '$n ${n == 1 ? 'line' : 'lines'}';

/// money.ts addedLine.
String addedLine(Map<String, dynamic> r) {
  var s = 'Added ${_lines(_int(r['added']))}, skipped ${_int(r['skipped'])} already in the log.';
  final paired = _int(r['paired']), waiting = _int(r['waiting']);
  if (paired > 0) s += ' ${_lines(paired)} confirmed ${paired == 1 ? 'a transfer' : 'transfers'} entered by hand.';
  if (waiting > 0) s += ' ${_lines(waiting)} ${waiting == 1 ? 'waits' : 'wait'} for you below.';
  return s;
}

const _commonColumns = {'date': 'date', 'description': 'description', 'amount': 'amount', 'reference': 'reference', 'balance': 'balance'};

const _columnFields = [
  ('date', 'Date', false, null),
  ('description', 'Description', false, null),
  ('amount', 'Amount', false, false),
  ('in', 'Money in', false, true),
  ('out', 'Money out', false, true),
  ('reference', 'Reference', true, null),
  ('balance', 'Balance', true, null),
];

/// Picks the statement file (a test stands in for it).
typedef StatementPick = Future<({String name, Uint8List bytes})?> Function();

StatementPick pickStatement = () async {
  final x = await openFile(
    acceptedTypeGroups: const [
      XTypeGroup(
        label: 'CSV',
        extensions: ['csv'],
        mimeTypes: ['text/csv', 'text/comma-separated-values'],
        uniformTypeIdentifiers: ['public.comma-separated-values-text'],
      ),
    ],
  );
  if (x == null) return null;
  return (name: x.name, bytes: await x.readAsBytes());
};

/// Money > Upload statement: a bank statement as a CSV from the phone's
/// files, its columns matched once per account; the lines that wait.
class MoneyStatementsPage extends StatefulWidget {
  const MoneyStatementsPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<MoneyStatementsPage> createState() => _MoneyStatementsPageState();
}

class _MoneyStatementsPageState extends State<MoneyStatementsPage> {
  final _channel = TextEditingController();
  ({String name, Uint8List bytes})? _file;
  List<String>? _headers;
  Map<String, Object>? _cols;
  ({String message, List<String> missing, List<({int row, String why})> problems})? _refusal;
  String _said = '';
  bool _tried = false;
  bool _busy = false;
  String? _error;
  List<Map<String, dynamic>> _waiting = const [];
  String? _waitError;

  @override
  void initState() {
    super.initState();
    _channel.addListener(() => setState(() {}));
    _loadWaiting();
  }

  @override
  void dispose() {
    _channel.dispose();
    super.dispose();
  }

  Future<void> _loadWaiting() async {
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/money/statement-waiting') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _waiting = _maps(r['lines']));
    } on Object {
      // None shown.
    }
  }

  String get _text => utf8.decode(_file!.bytes, allowMalformed: true);

  String? get _fileProblem =>
      _file == null ? 'Choose the statement file.' : (_file!.bytes.length > (5 << 20) ? 'The file is larger than 5 MB; upload it in parts.' : null);

  Future<({List<String> headers, Map<String, dynamic> used, Map<String, Object> form})> _matching() async {
    final h = csvHeader(_text);
    Map<String, dynamic>? saved;
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/money/statement-format', query: {'channel': _channel.text.trim()}) as Map)
          .cast<String, dynamic>();
      saved = (r['columns'] as Map?)?.cast<String, dynamic>();
    } on Object {
      // Nothing saved can be read: a guess from the names.
    }
    return (headers: h, used: saved ?? _commonColumns, form: columnsForm(h, saved));
  }

  Future<Map<String, dynamic>> _send(Map<String, String>? columns) async =>
      (await widget.auth.transport.send('POST', '/v1/money/statements', body: {'channel': _channel.text.trim(), 'csv': _text, 'columns': ?columns}) as Map)
          .cast<String, dynamic>();

  void _done(Map<String, dynamic> r) {
    setState(() {
      _said = addedLine(r);
      _cols = null;
      _headers = null;
      _file = null;
      _tried = false;
    });
    if (_int(r['waiting']) > 0) _loadWaiting();
  }

  Future<void> _upload() async {
    setState(() => (_busy = true, _error = null, _said = '', _refusal = null));
    try {
      try {
        _done(await _send(_cols == null ? null : columnsRequest(_cols!)));
      } on Object catch (e) {
        var why = statementRefusal(e);
        if (why == null) rethrow;
        if (why.missing.isNotEmpty && _cols == null) {
          final m = await _matching();
          // Only an optional column is missing: sent again with those it has.
          if (onlyOptionalMissing(why.missing, m.used) && columnsProblem(m.form) == null) {
            try {
              _done(await _send(columnsRequest(m.form)));
              return;
            } on Object catch (e2) {
              why = statementRefusal(e2);
              if (why == null) rethrow;
            }
          }
          if (why.missing.isNotEmpty && mounted) setState(() => (_headers = m.headers, _cols = m.form));
        }
        if (mounted) setState(() => _refusal = why);
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final manage = widget.auth.can('invoice.manage');
    final cols = _cols;
    final problem = channelProblem(_channel.text) ?? _fileProblem ?? (cols == null ? null : columnsProblem(cols));
    return TkPageScreen(
      title: 'Upload a statement',
      description: manage
          ? 'For a bank account Taskiem has no connection to: download its statement as a CSV and upload it here. Lines already in the money log are skipped, so uploading the same days again adds nothing twice.'
          : null,
      backLabel: 'Back to Money',
      children: [
        if (!manage)
          Text('You can see money, but not add to it. Someone who manages invoices can upload statements.', style: tkHint(t))
        else ...[
          if (_said.isNotEmpty) _notice(t, _said),
          if (_waiting.isNotEmpty) _waitingCard(t),
          TkPageCard(
            children: [
              TkKitField(
                label: 'Account',
                hint: 'The bank and the account, in small letters with a / between them: gtbank/current, access/savings.',
                child: TkTextBox(controller: _channel, plain: true, hint: 'gtbank/current'),
              ),
              KnownChannels(auth: widget.auth, controller: _channel),
              const SizedBox(height: 10),
              TkKitField(
                field: false,
                label: 'Statement file (CSV)',
                hint: 'The common format has the columns date, description and amount (money in positive, money out negative, in naira), and optionally reference and balance. A file with other columns is matched once, then remembered for this account.',
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: TkSmallButton(
                    label: _file == null ? 'Choose the file' : _file!.name,
                    semantics: 'Statement file: ${_file?.name ?? 'none chosen'}',
                    onPressed: () async {
                      final f = await pickStatement();
                      if (f != null && mounted) setState(() => (_file = f, _refusal = null, _said = ''));
                    },
                  ),
                ),
              ),
              if (cols != null && _headers != null) ..._columns(t, cols, _headers!),
              if (_tried && problem != null) _problemText(t, problem),
              if (_refusal != null)
                Semantics(
                  liveRegion: true,
                  container: true,
                  child: Container(
                    margin: const EdgeInsets.only(top: 10),
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(color: t.tkDangerBg, borderRadius: BorderRadius.circular(12)),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(_refusal!.message, style: tkText(size: 14, weight: 600, color: t.tkDangerInk)),
                        if (_refusal!.missing.isNotEmpty && cols != null)
                          Text('Match the file’s columns above, then upload again.', style: tkText(size: 14, color: t.tkDangerInk)),
                        for (final p in _refusal!.problems) Text('Line ${p.row}: ${p.why}', style: tkText(size: 14, color: t.tkDangerInk)),
                      ],
                    ),
                  ),
                ),
              if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  TkSmallButton(
                    label: _busy ? 'Uploading…' : 'Upload statement',
                    primary: true,
                    onPressed: _busy
                        ? null
                        : () {
                            setState(() => _tried = true);
                            if (problem != null) return;
                            _upload();
                          },
                  ),
                  if (cols == null && _file != null && _channel.text.trim().isNotEmpty && channelProblem(_channel.text) == null)
                    TkSmallButton(
                      label: 'Match columns myself',
                      onPressed: _busy
                          ? null
                          : () async {
                              final m = await _matching();
                              if (mounted) setState(() => (_headers = m.headers, _cols = m.form));
                            },
                    ),
                ],
              ),
            ],
          ),
        ],
      ],
    );
  }

  List<Widget> _columns(TkTokens t, Map<String, Object> cols, List<String> headers) {
    final split = cols['split'] == true;
    return [
      const SizedBox(height: 12),
      Text('Match the file’s columns', style: tkText(size: 16, weight: 700, color: t.text)),
      Text(
        'Choose which of the file’s columns holds each part of a line. This is remembered for ${_channel.text.trim().isEmpty ? 'this account' : _channel.text.trim()}.',
        style: tkHint(t),
      ),
      const SizedBox(height: 6),
      for (final (v, words) in const [(false, 'One column, money out negative'), (true, 'Two columns: money in and money out')])
        TkChoiceRow(
          control: TkRadio(checked: split == v),
          label: Text(words, style: tkText(size: 15, color: t.text)),
          onTap: () => setState(() => cols['split'] = v),
        ),
      for (final (key, label, optional, forSplit) in _columnFields)
        if (forSplit == null || forSplit == split)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: TkKitField(
              field: false,
              label: optional ? '$label (optional)' : label,
              child: Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(
                  label: (cols[key] as String? ?? '').isEmpty ? (optional ? 'Not in the file' : 'Choose a column') : cols[key] as String,
                  semantics: '$label: ${(cols[key] as String? ?? '').isEmpty ? 'none' : cols[key]}',
                  onPressed: () async {
                    final v = await showTkModalChoices(
                      context,
                      title: label,
                      value: cols[key] as String? ?? '',
                      choices: [('', optional ? 'Not in the file' : 'Choose a column'), for (final h in headers) (h, h)],
                    );
                    if (v != null) setState(() => cols[key] = v);
                  },
                ),
              ),
            ),
          ),
    ];
  }

  Widget _waitingCard(TkTokens t) => TkPageCard(
    title: 'Transfers to confirm',
    children: [
      Text(
        'These lines of your statement may be money someone already recorded by hand. Same money counts it once; Different money adds it as new money.',
        style: tkHint(t),
      ),
      if (_waitError != null) TkErrorBox(_waitError!),
      for (final l in _waiting)
        Padding(
          padding: const EdgeInsets.only(top: 10),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TkRow(
                name: '${naira(_int(l['amount_kobo']))} on ${dateLabel(_str(l['occurred_at']))}',
                icon: 'money',
                tone: CardTone.warn,
                sub: '${_str(l['description']).isNotEmpty ? l['description'] : 'No description'} · ${l['channel']}',
              ),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final c in _maps(l['candidates']))
                    TkSmallButton(
                      label:
                          'Same money: ${naira(_int(c['amount_kobo']))} on ${dateLabel(_str(c['occurred_at']))}${_str(c['description']).isNotEmpty ? ', ${c['description']}' : ''}',
                      onPressed: _busy ? null : () => _decide(_str(l['id']), _str(c['entry_id'])),
                    ),
                  TkSmallButton(label: 'Different money', onPressed: _busy ? null : () => _decide(_str(l['id']), null)),
                ],
              ),
            ],
          ),
        ),
    ],
  );

  Future<void> _decide(String id, String? entry) async {
    setState(() => (_busy = true, _waitError = null));
    try {
      await widget.auth.transport.send(
        'POST',
        '/v1/money/statement-waiting/${Uri.encodeComponent(id)}',
        body: entry == null ? {'decision': 'different'} : {'decision': 'same', 'entry_id': entry},
      );
      await _loadWaiting();
    } on Object catch (e) {
      if (mounted) setState(() => _waitError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }
}

// --- cash and expenses ---

const entryKinds = [
  ('expense', 'Expense', 'Money the business spent: diesel, rent, supplies.'),
  ('cash_in', 'Cash in', 'Cash the business took.'),
  ('cash_out', 'Cash out', 'Cash taken out of the till or the safe.'),
];

/// money.ts entryProblem.
String? entryProblem({
  required String kind,
  required String amount,
  required String date,
  required String description,
  String vendor = '',
  String category = '',
  String channel = '',
  required DateTime now,
}) {
  if (amount.trim().isEmpty) return 'Give the amount.';
  final k = nairaKobo(amount);
  if (k == null || k <= 0) return 'The amount is a number of naira, such as 12000 or 12000.50.';
  if (description.trim().isEmpty) return 'Say what it was for.';
  if (description.trim().length > 300) return 'The description is at most 300 characters.';
  if (date.isNotEmpty && !RegExp(r'^\d{4}-\d{2}-\d{2}$').hasMatch(date)) return 'The date does not look right.';
  if (date.isNotEmpty && date.compareTo(_day(now.add(const Duration(days: 1)))) > 0) return 'The date is in the future.';
  if (kind == 'expense') {
    if (vendor.trim().length > 120) return 'The vendor is at most 120 characters.';
    if (category.trim().length > 60) return 'The category is at most 60 characters.';
  }
  return channelProblem(channel, optional: true);
}

/// Money > Cash and expenses: what the money log cannot hear from a
/// connection, under the person's name.
class MoneyEntriesPage extends StatefulWidget {
  const MoneyEntriesPage({super.key, required this.auth, this.now});
  final Auth auth;
  final DateTime Function()? now;

  @override
  State<MoneyEntriesPage> createState() => _MoneyEntriesPageState();
}

class _MoneyEntriesPageState extends State<MoneyEntriesPage> {
  DateTime get _now => (widget.now ?? DateTime.now)();
  String _kind = 'expense';
  late final _amount = TextEditingController();
  late final _date = TextEditingController(text: _day(_now));
  final _description = TextEditingController();
  final _vendor = TextEditingController();
  final _category = TextEditingController();
  final _channel = TextEditingController();
  String _key = formKey();
  bool _tried = false;
  bool _busy = false;
  String _said = '';
  String? _error;

  List<TextEditingController> get _boxes => [_amount, _date, _description, _vendor, _category, _channel];

  @override
  void initState() {
    super.initState();
    for (final c in _boxes) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final c in _boxes) {
      c.dispose();
    }
    super.dispose();
  }

  String? get _problem => entryProblem(
    kind: _kind,
    amount: _amount.text,
    date: _date.text.trim(),
    description: _description.text,
    vendor: _vendor.text,
    category: _category.text,
    channel: _channel.text,
    now: _now,
  );

  Future<void> _save() async {
    setState(() => _tried = true);
    if (_problem != null) return;
    final kobo = nairaKobo(_amount.text)!;
    final body = {
      if (_kind == 'expense') 'kind': 'expense' else ...{'kind': 'cash', 'direction': _kind == 'cash_in' ? 'in' : 'out'},
      'amount_kobo': kobo,
      'description': _description.text.trim(),
      if (_date.text.trim().isNotEmpty) 'occurred_at': _date.text.trim(),
      if (_kind == 'expense' && _vendor.text.trim().isNotEmpty) 'vendor': _vendor.text.trim(),
      if (_kind == 'expense' && _category.text.trim().isNotEmpty) 'category': _category.text.trim(),
      if (_channel.text.trim().isNotEmpty) 'channel': _channel.text.trim(),
    };
    setState(() => (_busy = true, _error = null));
    try {
      final r = (await widget.auth.transport.send('POST', '/v1/money/entries', body: body, headers: {'Idempotency-Key': _key}) as Map).cast<String, dynamic>();
      final what = entryKinds.firstWhere((k) => k.$1 == _kind).$2;
      if (mounted) {
        setState(() {
          _said = r['created'] == false
              ? '$what of ${naira(kobo)} was already in the money log; nothing was added twice.'
              : '$what of ${naira(kobo)} added to the money log.';
          _key = formKey();
          _tried = false;
          _amount.clear();
          _description.clear();
          _vendor.clear();
          _category.clear();
          _date.text = _day(_now);
        });
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final manage = widget.auth.can('invoice.manage');
    return TkPageScreen(
      title: 'Cash and expenses',
      description: manage
          ? 'Money that moves without a connection Taskiem hears from: cash taken and paid out, and what the business spends. Each entry is added to the money log under your name.'
          : null,
      backLabel: 'Back to Money',
      children: [
        if (!manage)
          Text('You can see money, but not add to it. Someone who manages invoices can enter cash and expenses.', style: tkHint(t))
        else ...[
          if (_said.isNotEmpty) _notice(t, _said),
          TkPageCard(
            children: [
              Text('What it is', style: tkText(size: 13, weight: 600, color: t.muted)),
              for (final (v, label, help) in entryKinds)
                TkChoiceRow(
                  control: TkRadio(checked: _kind == v),
                  label: Text('$label. $help', style: tkText(size: 15, color: t.text)),
                  onTap: () => setState(() => _kind = v),
                ),
              const SizedBox(height: 6),
              TkKitField(
                label: 'Amount (₦)',
                child: TkTextBox(controller: _amount, money: true, hint: '12000'),
              ),
              const SizedBox(height: 10),
              TkKitField(
                label: 'Date',
                hint: 'As 2026-11-12.',
                child: TkTextBox(controller: _date),
              ),
              const SizedBox(height: 10),
              TkKitField(
                label: 'Description',
                hint: 'What it was for: diesel for the generator, the day’s cash sales.',
                child: TkTextBox(controller: _description, max: 300),
              ),
              if (_kind == 'expense') ...[
                const SizedBox(height: 10),
                TkKitField(
                  label: 'Vendor (optional)',
                  hint: 'Who was paid.',
                  child: TkTextBox(controller: _vendor, max: 120),
                ),
                const SizedBox(height: 10),
                TkKitField(
                  label: 'Category (optional)',
                  hint: 'Such as fuel, rent or supplies.',
                  child: TkTextBox(controller: _category, max: 60),
                ),
              ],
              const SizedBox(height: 10),
              TkKitField(
                label: 'Account (optional)',
                hint: 'Where the money moved. Leave it empty for cash; or name an account, such as gtbank/current.',
                child: TkTextBox(controller: _channel, plain: true, hint: 'cash'),
              ),
              KnownChannels(auth: widget.auth, controller: _channel),
              if (_tried && _problem != null) _problemText(t, _problem!),
              if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
              const SizedBox(height: 12),
              Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(label: 'Add to the money log', primary: true, onPressed: _busy ? null : _save),
              ),
            ],
          ),
        ],
      ],
    );
  }
}

// --- fees ---

/// money.ts percentToBp: null when empty, -1 when not a percent.
int? percentToBp(String text) {
  final t = text.replaceAll(RegExp(r'[%\s]'), '');
  if (t.isEmpty) return null;
  if (!RegExp(r'^\d+(\.\d{1,2})?$').hasMatch(t)) return -1;
  final bp = (double.parse(t) * 100).round();
  return bp <= 10000 ? bp : -1;
}

/// money.ts bpToPercent.
String bpToPercent(int bp) {
  final whole = bp ~/ 100, frac = bp % 100;
  if (frac == 0) return '$whole';
  return '$whole.${frac.toString().padLeft(2, '0').replaceFirst(RegExp(r'0$'), '')}';
}

/// money.ts rateWords.
String rateWords(Map<String, dynamic> r) {
  final bp = _int(r['percent_bp']), flat = _int(r['flat_kobo']);
  final parts = [if (bp > 0 || flat == 0) '${bpToPercent(bp)}%', if (flat > 0) naira(flat)];
  return parts.join(' + ') + (r['cap_kobo'] is num ? ', at most ${naira(_int(r['cap_kobo']))}' : '');
}

/// money.ts feeProblem.
String? feeProblem(String channel, String percent, String flat, String cap) {
  final c = channel.trim();
  if (c.isEmpty) return 'Name the connection, such as paystack/main.';
  if (c.length > 200) return 'The connection’s name is at most 200 characters.';
  final bp = percentToBp(percent);
  if (bp == -1) return 'The percent is a number from 0 to 100, such as 1.5.';
  if (flat.trim().isNotEmpty && nairaKobo(flat) == null) return 'The flat fee is a number of naira, such as 100.';
  if (cap.trim().isNotEmpty && nairaKobo(cap) == null) return 'The cap is a number of naira, such as 2000.';
  if (bp == null && flat.trim().isEmpty) return 'Give the percent, the flat fee, or both.';
  return null;
}

/// Money > Fees: the fee agreed with each payments connection.
class MoneyFeesPage extends StatefulWidget {
  const MoneyFeesPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<MoneyFeesPage> createState() => _MoneyFeesPageState();
}

class _MoneyFeesPageState extends State<MoneyFeesPage> {
  List<Map<String, dynamic>>? _rates;
  String? _error;
  final _channel = TextEditingController();
  final _percent = TextEditingController();
  final _flat = TextEditingController();
  final _cap = TextEditingController();
  bool _editing = false;
  bool _tried = false;
  bool _busy = false;
  String _said = '';
  String? _saveError;

  List<TextEditingController> get _boxes => [_channel, _percent, _flat, _cap];

  @override
  void initState() {
    super.initState();
    for (final c in _boxes) {
      c.addListener(() => setState(() {}));
    }
    _load();
  }

  @override
  void dispose() {
    for (final c in _boxes) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/money/fee-rates') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_rates = _maps(r['fee_rates']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  void _clear() => setState(() {
    for (final c in _boxes) {
      c.clear();
    }
    _editing = false;
    _tried = false;
  });

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final manage = widget.auth.can('invoice.manage');
    final list = _rates;
    final problem = feeProblem(_channel.text, _percent.text, _flat.text, _cap.text);
    return TkPageScreen(
      title: 'Fees',
      description: 'The fee you agreed with each payments connection: a percent of the amount, a flat fee, or both, up to a cap.',
      backLabel: 'Back to Money',
      onRefresh: _load,
      children: [
        _notice(t, 'A fee above the agreed rate becomes a leak, so you can claim the difference back.'),
        if (_said.isNotEmpty) _notice(t, _said),
        if (_error != null) TkErrorBox(_error!),
        if (list == null && _error == null) const TkLoading(rows: 2, label: 'Loading the fees…'),
        if (list != null && list.isEmpty)
          const TkEmpty(icon: 'money', title: 'No fees agreed yet', line: 'Add the rate from your agreement with each connection.'),
        if (list != null && list.isNotEmpty)
          TkPageCard(
            children: [
              for (final r in list)
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      TkRow(
                        name: '${channelWords(_str(r['channel']))} · ${r['channel']}',
                        letters: channelWords(_str(r['channel'])),
                        sub:
                            'Percent ${bpToPercent(_int(r['percent_bp']))}% · Flat fee ${naira(_int(r['flat_kobo']))} · Cap ${r['cap_kobo'] is num ? naira(_int(r['cap_kobo'])) : 'None'}',
                      ),
                      if (manage)
                        Align(
                          alignment: Alignment.centerLeft,
                          child: TkSmallButton(
                            label: 'Change',
                            semantics: 'Change the fee for ${r['channel']}',
                            onPressed: () => setState(() {
                              _channel.text = _str(r['channel']);
                              _percent.text = bpToPercent(_int(r['percent_bp']));
                              _flat.text = _int(r['flat_kobo']) > 0 ? feeInput(_int(r['flat_kobo'])) : '';
                              _cap.text = r['cap_kobo'] is num ? feeInput(_int(r['cap_kobo'])) : '';
                              _editing = true;
                              _tried = false;
                            }),
                          ),
                        ),
                    ],
                  ),
                ),
            ],
          ),
        if (!manage)
          Text('Someone who manages invoices can add and change fees.', style: tkHint(t))
        else
          TkPageCard(
            title: _editing ? 'Change the fee for ${_channel.text}' : 'Add a fee',
            children: [
              TkKitField(
                label: 'Connection',
                hint: 'As the money log names it: the rail and the connection, such as paystack/main.',
                child: TkTextBox(controller: _channel, plain: true, hint: 'paystack/main', enabled: !_editing),
              ),
              if (!_editing) KnownChannels(auth: widget.auth, controller: _channel),
              const SizedBox(height: 10),
              TkKitField(
                label: 'Percent of the amount',
                hint: 'Such as 1.5.',
                child: TkTextBox(controller: _percent, money: true),
              ),
              const SizedBox(height: 10),
              TkKitField(
                label: 'Flat fee (₦)',
                hint: 'Added to each payment.',
                child: TkTextBox(controller: _flat, money: true),
              ),
              const SizedBox(height: 10),
              TkKitField(
                label: 'Cap (₦, optional)',
                hint: 'The most one payment’s fee can be.',
                child: TkTextBox(controller: _cap, money: true),
              ),
              if (_tried && problem != null) _problemText(t, problem),
              if (_saveError != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_saveError!)),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  TkSmallButton(
                    label: 'Save fee',
                    primary: true,
                    onPressed: _busy
                        ? null
                        : () async {
                            setState(() => _tried = true);
                            if (problem != null) return;
                            setState(() => (_busy = true, _saveError = null));
                            try {
                              final r = (await widget.auth.transport.send(
                                'PUT',
                                '/v1/money/fee-rates',
                                body: {
                                  'channel': _channel.text.trim(),
                                  'percent_bp': percentToBp(_percent.text) ?? 0,
                                  'flat_kobo': nairaKobo(_flat.text) ?? 0,
                                  if (_cap.text.trim().isNotEmpty) 'cap_kobo': nairaKobo(_cap.text),
                                },
                              ) as Map).cast<String, dynamic>();
                              if (mounted) setState(() => _said = 'Fee for ${r['channel']} saved: ${rateWords(r)}.');
                              _clear();
                              await _load();
                            } on Object catch (e) {
                              if (mounted) setState(() => _saveError = errorWords(e));
                            }
                            if (mounted) setState(() => _busy = false);
                          },
                  ),
                  if (_editing) TkSmallButton(label: 'Cancel', onPressed: _clear),
                ],
              ),
            ],
          ),
      ],
    );
  }
}

/// money.ts knownChannels: names of accounts already used, each once, in
/// order, cash left out.
List<String> knownChannels(Iterable<String> a, Iterable<String> b) => ({...a, ...b}..removeWhere((c) => c.isEmpty || c == 'cash')).toList()..sort();

/// Money.tsx useKnownChannels: the accounts already used (the fees' and
/// the money waiting to be matched), offered under an account's box; one
/// pressed is typed in.
class KnownChannels extends StatefulWidget {
  const KnownChannels({super.key, required this.auth, required this.controller});
  final Auth auth;
  final TextEditingController controller;

  @override
  State<KnownChannels> createState() => _KnownChannelsState();
}

class _KnownChannelsState extends State<KnownChannels> {
  List<String> _names = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    Future<List<String>> read(String path, String key) async {
      try {
        final r = (await widget.auth.transport.send('GET', path) as Map).cast<String, dynamic>();
        return [for (final x in _maps(r[key])) _str(x['channel'])];
      } on Object {
        return const [];
      }
    }

    final got = await Future.wait([read('/v1/money/fee-rates', 'fee_rates'), read(unmatchedPath, 'payments')]);
    if (mounted) setState(() => _names = knownChannels(got[0], got[1]));
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (_names.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Wrap(
        spacing: 8,
        runSpacing: 8,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          Text('Used before:', style: tkHint(t)),
          for (final n in _names) TkSmallButton(label: n, semantics: 'Use $n', onPressed: () => widget.controller.text = n),
        ],
      ),
    );
  }
}
