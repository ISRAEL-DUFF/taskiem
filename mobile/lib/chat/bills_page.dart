// Bills (web Bills.tsx, lib/bills.ts): linked suppliers' invoices, in the
// buyer's business, kept up to date by Taskiem. The list (to pay by due
// date, then paid) and one bill: what is left to pay, Pay from Taskiem
// (sent for approval, then approved with a code in Taskiem's conversation),
// how to pay it otherwise, its lines, paid outside Taskiem, a query, and
// the messages about it.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../app/openers.dart';
import '../app/links.dart';
import '../kit/forms.dart' show errorWords, nairaKobo;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'doc_thread.dart' show DocThread;
import 'invoices_page.dart' show fmtTime;
import 'money_sheets.dart' show billHero, billStatus, canPay, dateLabel, sentLine;
import 'words.dart' show naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

String billPath(String id, [String rest = '']) => '/v1/bills/${Uri.encodeComponent(id)}$rest';

/// bills.ts billsDue: what is left to pay on them, the cancelled left out.
int billsDue(List<Map<String, dynamic>> list) => list.fold(0, (n, b) => n + (b['status'] == 'void' ? 0 : _int(b['due_kobo'])));

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => page));

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
  const BillsPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<BillsPage> createState() => _BillsPageState();
}

class _BillsPageState extends State<BillsPage> with Polling {
  List<Map<String, dynamic>>? _open;
  List<Map<String, dynamic>> _paid = const [];
  String? _error;

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

  Widget _row(Map<String, dynamic> b) {
    final st = billStatus(_str(b['status']));
    return TkRow(
      name: _str(b['supplier_name']),
      icon: 'invoices',
      tone: st.tone,
      sub:
          '${b['reference']} · ${_str(b['due_on']).isNotEmpty ? 'due ${dateLabel(_str(b['due_on']))}' : 'no due date'}\n${naira(b['status'] == 'paid' ? _int(b['amount_kobo']) : _int(b['due_kobo']))}',
      pill: TkPill(st.label, tone: st.tone),
      onTap: () async {
        await Navigator.of(context).push(
          PageRouteBuilder<void>(
            pageBuilder: (_, _, _) => BillPage(auth: widget.auth, id: _str(b['id']), backLabel: 'Back to Bills'),
          ),
        );
        await _load();
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    final list = _open;
    return TkPageScreen(
      title: 'Bills',
      description: 'Invoices from linked suppliers on Taskiem. What they say is the supplier’s own, kept up to date.',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (list == null && _error == null) const TkLoading(rows: 3, label: 'Loading the bills…'),
        if (list != null) ...[
          TkHero(
            label: 'Left to pay',
            tag: '${list.length} ${list.length == 1 ? 'bill' : 'bills'}',
            amount: naira(billsDue(list)),
            sub: 'To linked suppliers on Taskiem',
            semanticsLabel: 'Left to pay',
          ),
          if (list.isEmpty)
            TkEmpty(
              icon: 'invoices',
              title: 'No bills to pay',
              line: 'An invoice a linked supplier sends you arrives here.',
              action: Openers.linkedBusinesses == null ? null : TkSmallButton(label: 'Linked businesses', onPressed: () => Openers.linkedBusinesses!(context)),
            )
          else
            TkPageCard(children: [for (final b in list) _row(b)]),
          if (_paid.isNotEmpty) TkPageCard(title: 'Paid', children: [for (final b in _paid) _row(b)]),
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

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
    if (widget.follow) startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _load() async {
    try {
      final b = (await _auth.transport.send('GET', billPath(widget.id)) as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_bill = b, _error = null));
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
    return [
      TkHero(
        label: h.label,
        tag: billStatus(_str(b['status'])).label,
        amount: naira(h.kobo),
        sub:
            '${h.sub}${b['status'] != 'void' ? ' · ${(h.paid * 100).round()}% paid' : ''}\nSupplier $supplier · Due ${dateLabel(_str(b['due_on'])).isEmpty ? 'no date set' : dateLabel(_str(b['due_on']))}',
        semanticsLabel: 'What is left to pay',
      ),
      if (_int(b['paying_kobo']) > 0)
        _box(t, 'A payment of ${naira(_int(b['paying_kobo']))} from Taskiem is under way: it waits for approval, then for the bank.'),
      if (sent.isNotEmpty) Text(sent, style: tkHint(t)),
      if (manage && canPay(b)) _payFromTaskiem(t, b),
      if (b['disputed'] == true) _box(t, 'You queried this bill. It is not paid from Taskiem until $supplier settles the query.', warn: true),
      if (_int(b['due_kobo']) > 0 && (_str(b['pay_account_number']).isNotEmpty || _str(b['pay_link']).isNotEmpty))
        TkPageCard(
          title: 'How to pay',
          children: [
            if (_str(b['pay_account_number']).isNotEmpty)
              Text.rich(
                TextSpan(
                  style: tkText(size: 15, color: t.text),
                  children: [
                    const TextSpan(text: 'Transfer to '),
                    TextSpan(
                      text: _str(b['pay_account_number']),
                      style: tkText(size: 15, weight: 700, color: t.text),
                    ),
                    TextSpan(text: '${_str(b['pay_bank_name']).isNotEmpty ? ' (${b['pay_bank_name']})' : ''}, the account $supplier gave your business, with '),
                    TextSpan(
                      text: _str(b['reference']),
                      style: tkText(size: 15, weight: 700, color: t.text),
                    ),
                    TextSpan(
                      text:
                          ' in the transfer’s note: it is counted towards this invoice by itself. Without it, it counts towards your oldest invoice from $supplier first.',
                    ),
                  ],
                ),
              ),
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
      TkPageCard(
        title: 'What it is for',
        children: [
          for (final l in lines)
            Padding(
              padding: const EdgeInsets.only(bottom: 10),
              child: MergeSemantics(
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(_str(l['description']), style: tkText(size: 15, color: t.text)),
                          Text('${l['quantity'] ?? 1} × ${naira(_int(l['unit_kobo']))}', style: tkHint(t)),
                        ],
                      ),
                    ),
                    const SizedBox(width: 12),
                    Text(naira(((l['quantity'] as num? ?? 1) * _int(l['unit_kobo'])).round()), style: tkText(size: 15, weight: 600, color: t.text)),
                  ],
                ),
              ),
            ),
          Container(height: 1, color: t.border),
          const SizedBox(height: 10),
          MergeSemantics(
            child: Row(
              children: [
                Expanded(
                  child: Text('Total', style: tkText(size: 15, weight: 700, color: t.text)),
                ),
                Text(naira(_int(b['amount_kobo'])), style: tkText(size: 15, weight: 700, color: t.text)),
              ],
            ),
          ),
          const SizedBox(height: 8),
          Text('Brought up to date ${fmtTime(_str(b['mirrored_at']))}. The supplier’s invoice is the record: a change to it shows here.', style: tkHint(t)),
        ],
      ),
      if (manage && b['status'] != 'void' && b['status'] != 'paid')
        TkPageCard(
          title: 'Something else',
          children: [
            if (_int(b['declared_paid_kobo']) > 0)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Text('You said you paid ${naira(_int(b['declared_paid_kobo']))} outside Taskiem.', style: tkHint(t)),
              ),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                TkSmallButton(label: 'Paid outside Taskiem', onPressed: () => _declare(b)),
                if (b['disputed'] != true) TkSmallButton(label: 'Query this bill', onPressed: () => _query(b)),
              ],
            ),
          ],
        ),
      if (_actError != null) TkErrorBox(_actError!),
      DocThread(key: ValueKey('thread ${b['reference']}'), auth: _auth, doc: 'bill', reference: _str(b['reference']), follow: widget.follow),
    ];
  }

  Widget _payFromTaskiem(TkTokens t, Map<String, dynamic> b) {
    final s = _pay;
    if (s == null) return _payError != null ? TkErrorBox(_payError!) : const SizedBox.shrink();
    final ready = _str(s['workflow_id']).isNotEmpty;
    return TkPageCard(
      title: 'Pay from Taskiem',
      children: [
        Text(
          ready
              ? 'It is paid from your Taskiem wallet once someone with the ${s['approver_role']} role approves it, and ${b['supplier_name']}’s Taskiem counts it towards ${b['reference']} by itself.'
              : 'Pay bills from linked suppliers from your Taskiem wallet, each after one approval.',
          style: tkHint(t),
        ),
        const SizedBox(height: 10),
        Align(
          alignment: Alignment.centerLeft,
          child: ready
              ? TkSmallButton(
                  label: 'Pay ${naira(_int(b['due_kobo']))}',
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
