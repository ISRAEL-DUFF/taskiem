// Bills (web Bills.tsx, lib/bills.ts): linked suppliers' invoices, in the
// buyer's business, kept up to date by Taskiem. The list (to pay by due
// date, then paid) and one bill: what is left to pay, Pay from Taskiem
// (sent for approval, then approved with a code in Taskiem's conversation),
// how to pay it otherwise, its lines, paid outside Taskiem, a query, and
// the messages about it.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show SpendLabel;
import '../app/auth.dart';
import '../app/openers.dart';
import '../app/links.dart';
import '../kit/forms.dart' show errorWords, nairaKobo;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/look.dart';
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'doc_thread.dart' show DocThread;
import 'invoices_page.dart' show fmtTime, shortDay;
import 'money_sheets.dart' show billHero, billStatus, canPay, sentLine;
import 'pay_transfer.dart' show BillTransferSource, TransferLive, payBill, payWayWords;
import 'things_page.dart' show ThingsPage;
import 'treat_line.dart' show LineMark;
import 'words.dart' show naira;
import '../kit/mobile.dart' show TkCountUp, TkHeadButton, TkListRow, TkRoute, TkSection, TkSkeleton, tkInitials;
import 'package:flutter/services.dart' show Clipboard, ClipboardData;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

String billPath(String id, [String rest = '']) => '/v1/bills/${Uri.encodeComponent(id)}$rest';

/// bills.ts billsDue: what is left to pay on them, the cancelled left out.
int billsDue(List<Map<String, dynamic>> list) => list.fold(0, (n, b) => n + (b['status'] == 'void' ? 0 : _int(b['due_kobo'])));

/// A ten-digit account number as it is read out: "0123 456 789".
String spacedAccount(String n) => n.length == 10 ? '${n.substring(0, 4)} ${n.substring(4, 7)} ${n.substring(7)}' : n;

String _ymd(DateTime d) => '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

/// Days from today (Lagos) to a due date: 0 today, 1 tomorrow, below 0
/// late; null with none.
int? daysUntil(String dueOn, DateTime now) {
  final d = DateTime.tryParse('${dueOn}T00:00:00Z');
  if (d == null) return null;
  final today = DateTime.parse('${_ymd(now.toUtc().add(const Duration(hours: 1)))}T00:00:00Z');
  return d.difference(today).inDays;
}

const _days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

/// "today", "tomorrow", "Mon", "late since 12 Nov", or the date.
String dueWords(String dueOn, DateTime now) {
  final n = daysUntil(dueOn, now);
  if (n == null) return '';
  if (n == 0) return 'today';
  if (n == 1) return 'tomorrow';
  if (n < 0) return 'late since ${shortDay(dueOn, now)}';
  if (n < 7) return _days[DateTime.parse('${dueOn}T00:00:00Z').weekday - 1];
  return shortDay(dueOn, now);
}

/// What falls due each day of the week ahead, today first (what is late
/// counted today).
List<({String label, int kobo})> weekDue(List<Map<String, dynamic>> bills, DateTime now) {
  final today = DateTime.parse('${_ymd(now.toUtc().add(const Duration(hours: 1)))}T00:00:00Z');
  return [
    for (var i = 0; i < 7; i++)
      (
        label: i == 0 ? 'Today' : _days[today.add(Duration(days: i)).weekday - 1],
        kobo: bills.where((b) {
          final n = daysUntil(_str(b['due_on']), now);
          return n != null && (i == 0 ? n <= 0 : n == i);
        }).fold(0, (s, b) => s + _int(b['due_kobo'])),
      ),
  ];
}

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Opens Bills.
Future<void> openBills(BuildContext context, Auth auth) => _push(context, BillsPage(auth: auth));

/// Opens one bill.
Future<void> openBill(BuildContext context, Auth auth, String id) => _push(context, BillPage(auth: auth, id: id));

Widget _box(TkTokens t, String text, {bool warn = false}) => Semantics(
  liveRegion: !warn,
  container: true,
  child: Container(
    width: double.infinity,
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
    decoration: BoxDecoration(color: warn ? t.tintWarn : t.tkLimeSoft, borderRadius: BorderRadius.circular(14)),
    child: Text(text, style: tkText(size: 15, color: warn ? t.tintWarnText : t.text)),
  ),
);

/// The bills from linked suppliers: those still to pay, by due date; then
/// those paid.
class BillsPage extends StatefulWidget {
  const BillsPage({super.key, required this.auth, this.backLabel = 'Back', this.now});
  final Auth auth;

  /// "Back to Money": the head's link says where it goes back to.
  final String backLabel;

  /// The clock (a test sets it).
  final DateTime Function()? now;

  @override
  State<BillsPage> createState() => _BillsPageState();
}

class _BillsPageState extends State<BillsPage> with Polling {
  List<Map<String, dynamic>>? _open;
  List<Map<String, dynamic>> _paid = const [];
  String? _error;

  DateTime get _now => (widget.now ?? DateTime.now)();

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _load() async {
    final t = widget.auth.transport;
    try {
      final r = await Future.wait([
        t.send('GET', '/v1/bills'),
        t.send('GET', '/v1/bills', query: {'status': 'paid'}),
      ]);
      if (mounted) setState(() => (_open = _maps((r[0] as Map)['bills']), _paid = _maps((r[1] as Map)['bills']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  /// A bill's status in the accents: late red, due within two days or
  /// queried or part-paid orange, being paid violet, paid lime; else blue.
  ({String label, ({Color fill, Color ink}) accent}) _state(TkLook look, Map<String, dynamic> b, DateTime now) {
    final st = billStatus(_str(b['status']));
    final days = daysUntil(_str(b['due_on']), now);
    return switch (b['status']) {
      'paid' => (label: st.label, accent: (fill: look.lime, ink: look.limeInk)),
      'paying' => (label: st.label, accent: (fill: look.violet, ink: look.violetInk)),
      'disputed' || 'part_paid' => (label: st.label, accent: (fill: look.orange, ink: look.orangeInk)),
      'void' => (label: st.label, accent: (fill: TkTheme.tokens(context).muted, ink: TkTheme.tokens(context).muted)),
      _ when days != null && days < 0 => (label: 'Overdue', accent: (fill: look.red, ink: look.redInk)),
      _ when days != null && days <= 2 => (label: 'Due soon', accent: (fill: look.orange, ink: look.orangeInk)),
      _ => (label: st.label, accent: (fill: look.blue, ink: look.blueInk)),
    };
  }

  Widget _row(Map<String, dynamic> b) {
    final look = TkLook.of(context);
    final now = _now;
    final s = _state(look, b, now);
    final paid = b['status'] == 'paid';
    return TkListRow(
      title: _str(b['supplier_name']),
      letters: _str(b['supplier_name']),
      sub: '${b['reference']} · ${paid ? 'paid' : (_str(b['due_on']).isNotEmpty ? 'due ${dueWords(_str(b['due_on']), now)}' : 'no due date')}',
      amount: naira(paid ? _int(b['amount_kobo']) : _int(b['due_kobo'])),
      status: s.label,
      statusAccent: s.accent,
      waiting: !paid && (daysUntil(_str(b['due_on']), now) ?? 9) <= 2,
      onTap: () => _openBill(b),
    );
  }

  Future<void> _openBill(Map<String, dynamic> b) async {
    await Navigator.of(context).push(
      TkRoute<void>(pageBuilder: (_, _, _) => BillPage(auth: widget.auth, id: _str(b['id']), backLabel: 'Back to Bills')),
    );
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final now = _now;
    final list = _open;
    final toPay = (list ?? const <Map<String, dynamic>>[]).where((b) => b['status'] != 'void' && _int(b['due_kobo']) > 0).toList()
      ..sort((a, b) => _str(a['due_on']).isEmpty ? 1 : (_str(b['due_on']).isEmpty ? -1 : _str(a['due_on']).compareTo(_str(b['due_on']))));
    final week = weekDue(toPay, now);
    final next = toPay.isEmpty ? null : toPay.first;
    return TkPageScreen(
      title: 'Bills',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Invoices from linked suppliers on Taskiem. What they say is the supplier’s own, kept up to date.',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (list == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading the bills…'),
        if (list != null) ...[
          TkTopCard(
            semanticsLabel: 'Left to pay: ${naira(billsDue(list))}',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Left to pay', color: look.orangeInk, trailing: '${list.length} ${list.length == 1 ? 'bill' : 'bills'}'),
                const SizedBox(height: 6),
                FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(billsDue(list), style: tkFigure(context, size: 40))),
                Text('To linked suppliers on Taskiem', style: tkText(size: 13, lineHeight: 1.4, color: TkTheme.tokens(context).muted)),
                if (toPay.isNotEmpty) ...[
                  const SizedBox(height: 14),
                  Semantics(
                    label: 'Due this week: ${[for (final d in week) if (d.kobo > 0) '${d.label} ${naira(d.kobo)}'].join(', ')}',
                    child: TkMiniBars(
                      values: [for (final d in week) d.kobo.toDouble()],
                      labels: [for (final d in week) d.label],
                      color: look.orange,
                      picked: week.indexWhere((d) => d.kobo > 0),
                      height: 64,
                    ),
                  ),
                ],
                if (next != null) ...[
                  const SizedBox(height: 14),
                  TkCallout(
                    leading: TkFace(tkInitials(_str(next['supplier_name'])), size: 34, colors: tkFaceFor(_str(next['supplier_name']))),
                    title: '${next['supplier_name']}${_str(next['due_on']).isEmpty ? '' : ', ${dueWords(_str(next['due_on']), now)}'}',
                    sub: '${next['reference']} · ${naira(_int(next['due_kobo']))}',
                    color: look.orange,
                    button: 'Pay',
                    semantics: 'Pay the bill from ${next['supplier_name']}',
                    onPressed: () => _openBill(next),
                  ),
                ],
              ],
            ),
          ),
          if (list.isEmpty)
            TkEmpty(
              icon: 'invoices',
              title: 'No bills to pay',
              line: 'An invoice a linked supplier sends you arrives here.',
              action: Openers.linkedBusinesses == null ? null : TkSmallButton(label: 'Linked businesses', onPressed: () => Openers.linkedBusinesses!(context)),
            )
          else
            TkSection(title: 'To pay', count: toPay.length, children: [for (final b in [...toPay, ...list.where((b) => !toPay.contains(b))]) _row(b)]),
          if (_paid.isNotEmpty) TkSection(title: 'Paid', children: [for (final b in _paid) _row(b)]),
        ],
      ],
    );
  }
}

/// One bill: what is left to pay first, then its lines.
class BillPage extends StatefulWidget {
  const BillPage({super.key, required this.auth, required this.id, this.backLabel = 'Back', this.follow = true});
  final Auth auth;
  final String id;
  final String backLabel;
  final bool follow;

  @override
  State<BillPage> createState() => _BillPageState();
}

class _BillPageState extends State<BillPage> with Polling {
  Map<String, dynamic>? _bill;
  String? _error;
  Map<String, dynamic>? _pay;
  String? _payError;
  bool _busy = false;
  String? _actError;

  // Each line's treatment and label (what a business buys §8, §9): by the
  // order line of the same seller item, on the order it was raised from;
  // read once, for the buyer's eyes only.
  final Map<String, Map<String, dynamic>> _orders = {};
  List<SpendLabel>? _labels;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
    if (widget.follow) startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _loadMarks(Map<String, dynamic> b) async {
    if (!_auth.can('table.read')) return;
    final ids = {for (final l in _maps(b['lines'])) _str(l['purchase_id'])}.where((x) => x.isNotEmpty && !_orders.containsKey(x)).toList();
    if (ids.isEmpty && _labels != null) return;
    try {
      _labels ??= (await _auth.api.listSpendLabels()).labels;
      for (final id in ids) {
        final r = (await _auth.transport.send('GET', '/v1/stock/purchases/${Uri.encodeComponent(id)}') as Map).cast<String, dynamic>();
        _orders[id] = (r['purchase'] as Map).cast<String, dynamic>();
      }
      if (mounted) setState(() {});
    } on Object {
      // The marks are a help; the bill is there without them.
    }
  }

  /// A bill line's order line: same order, same seller item.
  Map<String, dynamic>? _lineOf(Map<String, dynamic> l) =>
      _maps(_orders[_str(l['purchase_id'])]?['lines']).where((x) => _str(x['seller_item_id']).isNotEmpty && x['seller_item_id'] == l['seller_item_id']).firstOrNull;

  Widget? _mark(Map<String, dynamic> l) {
    if (_labels == null) return null;
    final o = _lineOf(l);
    // Not matched to an order line (no seller item, or not on it): nothing
    // is known of how it is treated, so no mark, not "Not labelled".
    if (o == null) return null;
    // The server names the label kept, so one merged since shows as it.
    final label = _str(o['label']).isNotEmpty ? _str(o['label']) : (_labels!.where((x) => x.id == o['label_id']).map((x) => x.name).firstOrNull ?? '');
    return LineMark(how: _str(o['treatment']), label: label);
  }

  Future<void> _load() async {
    try {
      final b = (await _auth.transport.send('GET', billPath(widget.id)) as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_bill = b, _error = null));
      await _loadMarks(b);
      if (_pay == null && _auth.can('invoice.manage') && canPay(b)) await _loadPay();
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _loadPay() async {
    try {
      final s = (await _auth.transport.send('GET', '/v1/bills/pay') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_pay = s, _payError = null));
    } on Object catch (e) {
      if (mounted) setState(() => _payError = errorWords(e));
    }
  }

  Future<bool> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _actError = null));
    try {
      await f();
      if (mounted) showTkToast(context, done);
      await _load();
      return true;
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
      return false;
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = _bill;
    return TkPageScreen(
      title: b == null ? 'Bill' : '${b['reference']} from ${b['supplier_name']}',
      titleSize: 22,
      backLabel: widget.backLabel,
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (b == null && _error == null) const TkLoading(rows: 3, label: 'Loading the bill…'),
        if (b != null) ..._page(t, b),
      ],
    );
  }

  List<Widget> _page(TkTokens t, Map<String, dynamic> b) {
    final h = billHero(b);
    final supplier = _str(b['supplier_name']);
    final manage = _auth.can('invoice.manage');
    final sent = sentLine(b);
    final lines = _maps(b['lines']);
    final transfer = b['transfer'] is Map ? _str((b['transfer'] as Map)['state']) : '';
    return [
      _top(context, b, h, manage),
      // Paying by transfer: the number asked for, kept up to date (the
      // card the chat shows).
      if (manage && transfer.isNotEmpty && transfer != 'paid')
        TkSection(
          title: 'By transfer',
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
              child: TransferLive(
                key: ValueKey('transfer ${b['id']}'),
                source: BillTransferSource(_auth, _str(b['id']), number: _str(b['reference'])),
                onNext: (next) => openBill(context, _auth, next),
              ),
            ),
          ],
        ),
      if (_int(b['paying_kobo']) > 0)
        _box(t, 'A payment of ${naira(_int(b['paying_kobo']))} from Taskiem is under way: it waits for approval, then for the bank.'),
      if (sent.isNotEmpty) Text(sent, style: tkHint(t)),
      if (b['disputed'] == true) _box(t, 'You queried this bill. It is not paid from Taskiem until $supplier settles the query.', warn: true),
      if (_int(b['due_kobo']) > 0 && (_str(b['pay_account_number']).isNotEmpty || _str(b['pay_link']).isNotEmpty))
        TkSection(
          title: 'Or pay them yourself',
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 10, 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (_str(b['pay_account_number']).isNotEmpty)
                    Row(
                      children: [
                        Expanded(
                          child: MergeSemantics(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  [_str(b['pay_bank_name']), supplier].where((x) => x.isNotEmpty).join(' · '),
                                  style: tkText(size: 12.5, color: t.muted),
                                ),
                                Text(spacedAccount(_str(b['pay_account_number'])), style: tkDisplay(size: 22, letterSpacing: 0.4, color: t.text)),
                              ],
                            ),
                          ),
                        ),
                        TkHeadButton(
                          icon: 'copy',
                          onCard: true,
                          label: 'Copy the account number',
                          onTap: () async {
                            await Clipboard.setData(ClipboardData(text: _str(b['pay_account_number'])));
                            if (mounted) showTkToast(context, 'Copied');
                          },
                        ),
                      ],
                    ),
                  if (_str(b['pay_account_number']).isNotEmpty) ...[
                    const SizedBox(height: 8),
                    Text(
                      'Put ${b['reference']} in the transfer’s note, so it counts towards this bill.',
                      style: tkHint(t),
                    ),
                  ],
                  if (_str(b['pay_link']).isNotEmpty) ...[
                    const SizedBox(height: 10),
                    Align(
                      alignment: Alignment.centerLeft,
                      child: TkSmallButton(
                        label: 'Pay on the invoice’s page',
                        onPressed: () => openOrSay(context, () => openInBrowser(Uri.parse(_str(b['pay_link']))), 'The invoice’s page could not be opened.'),
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ],
        ),
      // As drawn: "Ofada rice, 50 kg × 3 … ₦126,000" on a card, the total large.
      TkSection(
        title: 'What it is for',
        children: [
          TkBill(
            lines: [
              for (final l in lines)
                (
                  name: '${_str(l['description'])}${(l['quantity'] as num? ?? 1) != 1 ? ' × ${(l['quantity'] as num).toInt() == l['quantity'] ? (l['quantity'] as num).toInt() : l['quantity']}' : ''}',
                  sub: '',
                  under: _mark(l),
                  amount: naira(((l['quantity'] as num? ?? 1) * _int(l['unit_kobo'])).round()),
                ),
            ],
            total: naira(_int(b['amount_kobo'])),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 8, 4, 0),
            child: Text('Brought up to date ${fmtTime(_str(b['mirrored_at']))}. The supplier’s invoice is the record: a change to it shows here.', style: tkHint(t)),
          ),
        ],
      ),
      // Lines kept track of on the orders it bills: their rows on Things
      // we have (what a business buys §9), for someone who may see them.
      if (_auth.can('table.read'))
        for (final o in {for (final l in lines) _str(l['purchase_id'])}.where((id) => _maps(_orders[id]?['lines']).any((x) => x['treatment'] == 'track' && _int(x['received']) > 0)))
        TkSection(
          inset: 56,
          children: [
            TkListRow(
              title: 'On Things we have',
              sub: 'From ${_str(_orders[o]?['reference']).isEmpty ? 'its order' : _str(_orders[o]?['reference'])}',
              icon: 'b-hash',
              square: TkLook.of(context).violet,
              onTap: () => Navigator.of(context).push(
                TkRoute<void>(
                  pageBuilder: (_, _, _) =>
                      ThingsPage(auth: _auth, order: o, orderReference: _str(_orders[o]?['reference']).isEmpty ? null : _str(_orders[o]?['reference']), backLabel: 'Back to the bill'),
                ),
              ),
            ),
          ],
        ),
      if (manage && b['status'] != 'void' && b['status'] != 'paid')
        TkSection(
          inset: 56,
          children: [
            TkListRow(
              title: 'Paid outside Taskiem',
              sub: _int(b['declared_paid_kobo']) > 0 ? 'You said you paid ${naira(_int(b['declared_paid_kobo']))} outside Taskiem.' : '',
              icon: 'b-check',
              square: TkLook.of(context).teal,
              onTap: () => _declare(b),
            ),
            if (b['disputed'] != true) TkListRow(title: 'Query this bill', icon: 'b-query', square: TkLook.of(context).orange, onTap: () => _query(b)),
          ],
        ),
      if (_actError != null) TkErrorBox(_actError!),
      DocThread(key: ValueKey('thread ${b['reference']}'), auth: _auth, doc: 'bill', reference: _str(b['reference']), follow: widget.follow),
    ];
  }

  /// The top card: what is left to pay large, how much is paid, and Pay
  /// from Taskiem (sent for approval, approved with a code in Taskiem's
  /// conversation) or setting it up.
  Widget _top(BuildContext context, Map<String, dynamic> b, ({String label, int kobo, String sub, double paid}) h, bool manage) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final status = _str(b['status']);
    final days = daysUntil(_str(b['due_on']), DateTime.now());
    final colour = switch (status) {
      'paid' => look.limeInk,
      'void' => t.muted,
      _ => days != null && days < 0 ? look.redInk : look.orangeInk,
    };
    final due = _str(b['due_on']).isEmpty ? '' : (days != null && days < 0 ? 'was due ${shortDay(_str(b['due_on']))}' : 'due ${dueWords(_str(b['due_on']), DateTime.now())}');
    final s = _pay;
    final ready = s != null && _str(s['workflow_id']).isNotEmpty;
    final pays = manage && canPay(b);
    final ways = [for (final w in (b['ways_to_pay'] as List? ?? const [])) if (w is String) w];
    return TkTopCard(
      semanticsLabel: 'What is left to pay',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow(h.label, color: colour, trailing: status == 'paid' || status == 'void' || due.isEmpty ? billStatus(status).label : due),
          const SizedBox(height: 6),
          FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(h.kobo, style: tkFigure(context, size: 40))),
          Text(h.sub, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
          if (status != 'void') ...[
            const SizedBox(height: 12),
            TkProgress(h.paid, color: look.lime, semanticsLabel: '${(h.paid * 100).round()}% paid'),
          ],
          // The ways it can be paid now (the payments flow, part 1): Pay
          // opens them in a small sheet, by transfer first.
          if (manage && ways.isNotEmpty) ...[
            const SizedBox(height: 14),
            Row(
              children: [
                Expanded(
                  child: TkSmallButton(
                    label: 'Pay ${naira(_int(b['due_kobo']))}',
                    primary: true,
                    onPressed: _busy
                        ? null
                        : () => payBill(
                            context,
                            auth: _auth,
                            bill: _str(b['id']),
                            onSent: _load,
                            onHowToPay: (_, _) {},
                          ),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              ways.length == 1 ? payWayWords(ways.first).title : '${payWayWords(ways.first).title}, or ${ways.length == 2 ? 'one other way' : '${ways.length - 1} other ways'}.',
              style: tkText(size: 12.5, lineHeight: 1.4, color: t.muted),
            ),
          ],
          if (pays && ways.isEmpty && s == null && _payError != null) ...[const SizedBox(height: 12), TkErrorBox(_payError!)],
          if (pays && ways.isEmpty && s != null) ...[
            const SizedBox(height: 14),
            Row(
              children: [
                Expanded(
                  child: ready
                      ? TkSmallButton(
                          label: 'Pay from Taskiem',
                          semantics: 'Pay ${naira(_int(b['due_kobo']))}',
                          primary: true,
                          onPressed: _busy
                              ? null
                              : () => _run(() => _auth.transport.send('POST', billPath(_str(b['id']), '/pay'), body: const {}), 'Sent for approval'),
                        )
                      : TkSmallButton(
                          label: 'Set up paying bills',
                          onPressed: _busy
                              ? null
                              : () => _run(() async {
                                  await _auth.transport.send('PUT', '/v1/bills/pay/setup', body: const {});
                                  await _loadPay();
                                }, 'Paying bills is set up'),
                        ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              ready
                  ? 'From your Taskiem wallet, once the ${s['approver_role']} approves it in their Taskiem chat.'
                  : 'Pay bills from linked suppliers from your Taskiem wallet, each after one approval.',
              style: tkText(size: 12.5, lineHeight: 1.4, color: t.muted),
            ),
          ],
        ],
      ),
    );
  }

  Future<void> _declare(Map<String, dynamic> b) => showTkModal<void>(
    context,
    title: 'Paid outside Taskiem',
    builder: (ctx) => _Declare(
      supplier: _str(b['supplier_name']),
      onSave: (kobo, how) => _sent(
        _auth.transport.send('POST', billPath(_str(b['id']), '/declare'), body: {'amount_kobo': kobo, 'how': how}),
        'Recorded, and the supplier is told',
      ),
    ),
  );

  Future<void> _query(Map<String, dynamic> b) => showTkModal<void>(
    context,
    title: 'Query this bill',
    builder: (ctx) => _Query(
      supplier: _str(b['supplier_name']),
      onSend: (reason) => _sent(_auth.transport.send('POST', billPath(_str(b['id']), '/query'), body: {'reason': reason}), 'Query sent'),
    ),
  );

  /// A modal's request: said and read again once done; its failure is the
  /// modal's to show.
  Future<void> _sent(Future<Object?> f, String done) async {
    await f;
    if (mounted) showTkToast(context, done);
    await _load();
  }
}

/// A modal's button and its error: pressed once, busy till done.
mixin _Sending<T extends StatefulWidget> on State<T> {
  bool busy = false;
  String? error;

  Future<void> sending(Future<void> Function() f) async {
    // A second press before the button shows it is busy sends nothing.
    if (busy) return;
    setState(() => (busy = true, error = null));
    try {
      await f();
      if (mounted) Navigator.of(context).pop();
    } on Object catch (e) {
      if (mounted) setState(() => (error = errorWords(e), busy = false));
    }
  }
}

/// Paid outside Taskiem: the amount and how; its own record, the supplier told.
class _Declare extends StatefulWidget {
  const _Declare({required this.supplier, required this.onSave});
  final String supplier;
  final Future<void> Function(int kobo, String how) onSave;

  @override
  State<_Declare> createState() => _DeclareState();
}

class _DeclareState extends State<_Declare> with _Sending {
  final _amount = TextEditingController();
  String _how = 'transfer';

  @override
  void dispose() {
    _amount.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text('Your own record, and ${widget.supplier} is told. It counts as paid when their Taskiem matches the money.', style: tkHint(t)),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Amount (₦)',
          child: TkTextBox(controller: _amount, money: true, hint: '25,000'),
        ),
        const SizedBox(height: 10),
        Text('How', style: tkText(size: 13, weight: 600, color: t.muted)),
        for (final (v, words) in const [('transfer', 'By transfer'), ('cash', 'In cash'), ('other', 'Another way')])
          TkChoiceRow(
            control: TkRadio(checked: _how == v),
            label: Text(words, style: tkText(size: 15, color: t.text)),
            onTap: () => setState(() => _how = v),
          ),
        const SizedBox(height: 10),
        if (error != null) Padding(padding: const EdgeInsets.only(bottom: 10), child: TkErrorBox(error!)),
        TkButton(label: 'Record it', primary: true, enabled: !busy, onPressed: () => sending(() => widget.onSave(nairaKobo(_amount.text) ?? 0, _how))),
      ],
    );
  }
}

/// Query this bill: what is wrong; the supplier told.
class _Query extends StatefulWidget {
  const _Query({required this.supplier, required this.onSend});
  final String supplier;
  final Future<void> Function(String reason) onSend;

  @override
  State<_Query> createState() => _QueryState();
}

class _QueryState extends State<_Query> with _Sending {
  final _reason = TextEditingController();

  @override
  void initState() {
    super.initState();
    _reason.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _reason.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text('${widget.supplier} is told why. Until they settle it, the bill is not paid from Taskiem.', style: tkHint(t)),
        const SizedBox(height: 10),
        TkKitField(
          label: 'What is wrong',
          child: TkTextBox(controller: _reason, lines: 3, max: 500),
        ),
        const SizedBox(height: 10),
        if (error != null) Padding(padding: const EdgeInsets.only(bottom: 10), child: TkErrorBox(error!)),
        TkButton(
          label: 'Send the query',
          primary: true,
          enabled: !busy && _reason.text.trim().isNotEmpty,
          onPressed: () => sending(() => widget.onSend(_reason.text.trim())),
        ),
      ],
    );
  }
}
