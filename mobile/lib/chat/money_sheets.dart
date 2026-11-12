// The chat's Invoices, Bills and Money tools, a row opened in its sheet (web
// ChatMoney.tsx InvoiceView, InvoiceAdd, MoneyEntryView, MoneyAdd;
// ChatBuyer.tsx BillView; lib/invoices.ts, lib/bills.ts, lib/money.ts,
// lib/chatmoney.ts): an invoice looked at, sent, paid or cancelled, and a
// new one made; a bill paid (on the supplier's page, or from Taskiem after
// an approval), questioned or said paid outside Taskiem; the money log's
// entries matched to an invoice, and cash or an expense added. Each change
// is told as a diary line by the server.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show errorWords, nairaKobo;
import '../kit/kit.dart';
import '../kit/list.dart' show TkPill;
import '../kit/modal.dart' show openOrSay, showComingSoon, showTkModalChoices;
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'product_form.dart' show SaveKey, formKey;
import 'words.dart' show CardTone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const _weekdays = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'];

/// invoices.ts dateLabel: "24 Oct 2026" from 2026-10-24; "" when not a date.
String dateLabel(String? d) {
  if (d == null || !RegExp(r'^\d{4}-\d{2}-\d{2}').hasMatch(d)) return '';
  final t = DateTime.parse('${d.substring(0, 10)}T12:00:00Z');
  return '${t.day} ${_months[t.month - 1]} ${t.year}';
}

// --- invoices (lib/invoices.ts) ---

/// invoices.ts INVOICE_STATUS: a status in a word, with its tone.
({String label, CardTone tone}) invoiceStatus(String s) => switch (s) {
  'draft' => (label: 'Draft', tone: CardTone.plain),
  'recorded' => (label: 'Recorded', tone: CardTone.plain),
  'sent' => (label: 'Sent', tone: CardTone.plain),
  'part_paid' => (label: 'Part-paid', tone: CardTone.warn),
  'paid' => (label: 'Paid', tone: CardTone.lime),
  'overdue' => (label: 'Overdue', tone: CardTone.warn),
  'void' => (label: 'Void', tone: CardTone.plain),
  _ => (label: s.replaceAll('_', ' '), tone: CardTone.plain),
};

/// chatmoney.ts owing: sent and not paid in full.
bool owing(Map<String, dynamic> x) => _int(x['due_kobo']) > 0 && const {'sent', 'part_paid', 'overdue', 'recorded'}.contains(x['status']);

typedef InvoiceActions = ({bool edit, bool send, bool sendAgain, bool voids, bool undo, bool reveal, bool amend});

/// invoices.ts invoiceActions: what a person may do with an invoice in its
/// status.
InvoiceActions invoiceActions(String status, bool Function(String) can) {
  final manage = can('invoice.manage');
  return (
    edit: manage && status == 'draft',
    send: manage && (status == 'draft' || status == 'recorded'),
    sendAgain: manage && const {'sent', 'part_paid', 'overdue'}.contains(status),
    voids: manage && status != 'paid' && status != 'void',
    undo: manage,
    reveal: can('pii.reveal'),
    amend: manage && status != 'draft' && status != 'void',
  );
}

String invoicePath(String id, [String rest = '']) => '/v1/invoices/${Uri.encodeComponent(id)}$rest';

// --- bills (lib/bills.ts) ---

/// bills.ts billStatus.
({String label, CardTone tone}) billStatus(String s) => switch (s) {
  'paid' => (label: 'Paid', tone: CardTone.lime),
  'part_paid' => (label: 'Part-paid', tone: CardTone.warn),
  'paying' => (label: 'Being paid', tone: CardTone.plain),
  'disputed' => (label: 'Queried', tone: CardTone.warn),
  'void' => (label: 'Cancelled', tone: CardTone.plain),
  _ => (label: 'To pay', tone: CardTone.warn),
};

/// bills.ts billHero: the figure that matters, what it is, how much paid.
({String label, int kobo, String sub, double paid}) billHero(Map<String, dynamic> b) {
  final amount = _int(b['amount_kobo']);
  final paidKobo = _int(b['paid_kobo']);
  final paid = amount > 0 ? (paidKobo / amount).clamp(0.0, 1.0) : 0.0;
  return switch (b['status']) {
    'void' => (label: 'Cancelled', kobo: amount, sub: '${_str(b['supplier_name'])} cancelled this invoice', paid: paid),
    'paid' => (label: 'Paid in full', kobo: amount, sub: 'Nothing left to pay', paid: 1.0),
    _ => (label: 'To pay', kobo: _int(b['due_kobo']), sub: 'of ${naira(amount)} · ${naira(paidKobo)} paid', paid: paid),
  };
}

/// bills.ts sentLine: what was sent from Taskiem not counted yet.
String sentLine(Map<String, dynamic> b) {
  final sent = _int(b['sent_kobo']), paid = _int(b['paid_kobo']);
  if (sent <= paid) return '';
  return 'You sent ${naira(sent)} from Taskiem; ${_str(b['supplier_name'])} has counted ${naira(paid)} so far.';
}

/// bills.ts canPay: Pay from Taskiem offered on a bill.
bool canPay(Map<String, dynamic> b) =>
    _int(b['due_kobo']) > 0 && _str(b['pay_account_number']).isNotEmpty && (b['status'] == 'open' || b['status'] == 'part_paid');

// --- the money log (lib/chatmoney.ts, lib/money.ts) ---

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

/// chatmoney.ts entryName.
String entryName(Map<String, dynamic> e) => [
  _str(e['description']),
  _str(e['vendor']),
  _kindWords[e['kind']] ?? _str(e['kind']).replaceAll('_', ' '),
].firstWhere((x) => x.isNotEmpty, orElse: () => '');

/// chatmoney.ts entryAmount: "+₦4,500", "−₦1,200".
String entryAmount(Map<String, dynamic> e) {
  if (e['amount_kobo'] == null) return 'No amount';
  return '${e['direction'] == 'in' ? '+' : '−'}${naira(_int(e['amount_kobo']))}';
}

/// chatmoney.ts entryPill.
({String text, CardTone tone})? entryPill(Map<String, dynamic> e) {
  if (e['status'] == 'failed' || e['status'] == 'reversed') return (text: e['status'] == 'failed' ? 'Failed' : 'Reversed', tone: CardTone.plain);
  if (e['status'] == 'pending') return (text: 'Pending', tone: CardTone.plain);
  if (e['to_match'] == true) return (text: 'To match', tone: CardTone.warn);
  if (_str(e['towards']).isNotEmpty) return (text: 'Counted', tone: CardTone.lime);
  return null;
}

/// chatmoney.ts matchable: money in with something left to count.
bool matchable(Map<String, dynamic> e) => e['direction'] == 'in' && e['status'] == 'settled' && _int(e['left_kobo']) > 0;

/// chatmoney.ts towardsWords.
String towardsWords(Map<String, dynamic> e) {
  if (_str(e['towards']).isNotEmpty) return 'Counted towards ${e['towards']}.';
  if (e['to_match'] == true) return 'Not matched to an invoice or order yet.';
  return '';
}

/// money.ts ENTRY_KINDS.
const entryKinds = [
  ('expense', 'Expense', 'Money the business spent: diesel, rent, supplies.'),
  ('cash_in', 'Cash in', 'Cash the business took.'),
  ('cash_out', 'Cash out', 'Cash taken out of the till or the safe.'),
];

String _today(DateTime now) => '${now.year}-${now.month.toString().padLeft(2, '0')}-${now.day.toString().padLeft(2, '0')}';

/// money.ts entryProblem: why an entry will be refused, or null.
String? entryProblem({required String kind, required String amount, required String description, required String date, String vendor = '', DateTime? now}) {
  if (amount.trim().isEmpty) return 'Give the amount.';
  final k = nairaKobo(amount);
  if (k == null || k <= 0) return 'The amount is a number of naira, such as 12000 or 12000.50.';
  if (description.trim().isEmpty) return 'Say what it was for.';
  if (description.trim().length > 200) return 'The description is at most 200 characters.';
  if (date.isNotEmpty && !RegExp(r'^\d{4}-\d{2}-\d{2}$').hasMatch(date)) return 'The date does not look right.';
  if (date.isNotEmpty && date.compareTo(_today((now ?? DateTime.now()).add(const Duration(days: 1)))) > 0) return 'The date is in the future.';
  if (kind == 'expense' && vendor.trim().length > 120) return 'The vendor is at most 120 characters.';
  return null;
}

// --- the parts the sheets share ---

/// `.tk-bill-hero`: pine, a kicker, the figure large, a line, a pill.
class BillHero extends StatelessWidget {
  const BillHero({super.key, required this.kicker, required this.amount, required this.sub, this.pill, this.label});
  final String kicker;
  final String amount;
  final String sub;
  final ({String text, CardTone tone})? pill;
  final String? label;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      label: label,
      child: Container(
        padding: const EdgeInsets.all(20),
        decoration: BoxDecoration(color: t.brand, borderRadius: BorderRadius.circular(22)),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(kicker.toUpperCase(), style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.brandDark)),
                  const SizedBox(height: 4),
                  Text(amount, style: tkText(size: 32, weight: 800, letterSpacing: -0.64, color: t.tkOnPine)),
                  const SizedBox(height: 4),
                  Text(sub, style: tkText(size: 14, color: t.tkOnPine.withValues(alpha: 0.82))),
                ],
              ),
            ),
            if (pill != null) ...[const SizedBox(width: 8), TkPill(pill!.text, tone: pill!.tone)],
          ],
        ),
      ),
    );
  }
}

/// `.tk-foot-two`: side by side; with large text one under another.
Widget sheetFoot(BuildContext context, List<Widget> buttons) => TkSheetFoot(
  child: buttons.length > 1 && MediaQuery.textScalerOf(context).scale(10) > 15
      ? Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(height: 10), buttons[i]],
          ],
        )
      : Row(
          children: [
            for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(width: 10), Expanded(child: buttons[i])],
          ],
        ),
);

/// A `<select>` on a phone: the choice now, pressed to choose another.
Widget choiceBox(BuildContext context, {required String label, required String value, required List<(String, String)> choices, required ValueChanged<String> onPick}) =>
    TkKitField(
      field: false,
      label: label,
      child: Align(
        alignment: Alignment.centerLeft,
        child: TkSmallButton(
          label: choices.where((c) => c.$1 == value).map((c) => c.$2).firstOrNull ?? value,
          semantics: '$label: ${choices.where((c) => c.$1 == value).map((c) => c.$2).firstOrNull ?? value}',
          onPressed: () async {
            final pick = await showTkModalChoices(context, title: label, value: value, choices: choices);
            if (pick != null) onPick(pick);
          },
        ),
      ),
    );

/// `.tk-form-now`: a line of what is now.
Widget _now(TkTokens t, String s) => Text(s, style: tkText(size: 14, color: t.muted));

// --- InvoiceView ---

/// One invoice: what it is for, what is paid, and what can be done.
class InvoiceView extends StatefulWidget {
  const InvoiceView({super.key, required this.auth, required this.invoice, required this.onSaved, this.canEdit = true, this.onOpen});
  final Auth auth;
  final Map<String, dynamic> invoice;
  final void Function(String? line) onSaved;
  final bool canEdit;

  /// Its own screen (Open the invoice); null says it is coming.
  final void Function(BuildContext context, String id)? onOpen;

  @override
  State<InvoiceView> createState() => _InvoiceViewState();
}

class _InvoiceViewState extends State<InvoiceView> {
  String _mode = '';
  late final _amount = TextEditingController(text: _int(_x['due_kobo']) > 0 ? _feeText(_int(_x['due_kobo'])) : '');
  final _reason = TextEditingController();
  String _how = 'cash';
  bool _busy = false;
  String? _error;
  // One payment per form, one send: a retry is the same, not a second.
  final _payKey = formKey();
  final _sendKey = formKey();

  Map<String, dynamic> get _x => widget.invoice;
  String get _number => _str(_x['number']);

  static String _feeText(int kobo) => kobo % 100 == 0 ? '${kobo ~/ 100}' : '${kobo ~/ 100}.${(kobo % 100).toString().padLeft(2, '0')}';

  @override
  void initState() {
    super.initState();
    _amount.addListener(() => setState(() {}));
    _reason.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _amount.dispose();
    _reason.dispose();
    super.dispose();
  }

  Future<void> _run(Future<void> Function() f, String words) async {
    setState(() => (_busy = true, _error = null));
    try {
      await f();
      widget.onSaved(words);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final x = _x;
    final acts = invoiceActions(_str(x['status']), widget.auth.can);
    final st = invoiceStatus(_str(x['status']));
    final owed = owing(x);
    final kobo = nairaKobo(_amount.text);
    final t2 = _str(_x['id']);
    final buttons = <Widget>[
      if (_mode.isNotEmpty) TkGhostButton(label: 'Not now', onTap: _busy ? null : () => setState(() => (_mode = '', _error = null))),
      if (_mode == 'pay')
        TkCta(
          label: 'Record ${kobo != null && kobo > 0 ? naira(kobo) : 'it'}',
          onTap: _busy || kobo == null || kobo <= 0
              ? null
              : () => _run(() async {
                  final r = (await widget.auth.transport.send(
                    'POST',
                    '/v1/money/entries',
                    body: {
                      'kind': 'cash',
                      'direction': 'in',
                      'amount_kobo': kobo,
                      'description': 'Payment for $_number',
                      'channel': _how == 'cash' ? 'cash' : 'transfer',
                    },
                    headers: {'Idempotency-Key': _payKey},
                  ) as Map).cast<String, dynamic>();
                  await widget.auth.transport.send('POST', '/v1/money/entries/${Uri.encodeComponent(_str(r['entry_id']))}/match', body: {'invoice': _number});
                }, 'Recorded ${naira(kobo)} paid on $_number'),
        )
      else if (_mode == 'void')
        TkCta(
          label: 'Cancel $_number',
          onTap: _busy || _reason.text.trim().isEmpty
              ? null
              : () => _run(() => widget.auth.transport.send('POST', invoicePath(t2, '/void'), body: {'reason': _reason.text.trim()}), 'Cancelled $_number'),
        )
      else if (acts.send || acts.sendAgain)
        TkCta(
          label: acts.sendAgain ? 'Send a reminder' : 'Send it',
          onTap: _busy
              ? null
              : () => _run(
                  () => widget.auth.transport.send('POST', invoicePath(t2, '/send'), body: const {}, headers: {'Idempotency-Key': _sendKey}),
                  acts.sendAgain ? 'Sent a reminder for $_number' : 'Sent $_number',
                ),
        ),
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              BillHero(
                kicker: '${owed ? 'To pay' : 'Total'}${_str(x['due_on']).isNotEmpty && owed ? ' · due ${dateLabel(_str(x['due_on']))}' : ''}',
                amount: naira(owed ? _int(x['due_kobo']) : _int(x['total_kobo'])),
                sub: _str((x['customer'] as Map?)?['name']),
                pill: (text: st.label, tone: st.tone),
              ),
              if (_int(x['paid_kobo']) > 0) _now(t, '${naira(_int(x['paid_kobo']))} of ${naira(_int(x['total_kobo']))} paid.'),
              TkLines([
                for (final l in _maps(x['lines']))
                  ('${l['quantity'] ?? 1} × ${_str(l['description'])}', naira(((l['quantity'] as num? ?? 1) * _int(l['unit_kobo'])).round())),
              ]),
              if (x['status'] == 'void' && _str(x['void_reason']).isNotEmpty) _now(t, 'Cancelled: ${x['void_reason']}'),
              if (_mode == 'pay') ...[
                TkKitField(
                  label: 'What they paid (₦)',
                  hint: 'It goes in the money log and counts towards this invoice.',
                  error: _amount.text.trim().isNotEmpty && kobo == null ? 'An amount in naira, like 25,000.' : null,
                  child: TkTextBox(controller: _amount, money: true, hint: '25000'),
                ),
                choiceBox(context, label: 'How', value: _how, choices: const [('cash', 'In cash'), ('transfer', 'By transfer')], onPick: (v) => setState(() => _how = v)),
              ],
              if (_mode == 'void')
                TkKitField(
                  label: 'Why cancel $_number?',
                  hint: 'Kept with the invoice. The customer can no longer pay it.',
                  child: TkTextBox(controller: _reason, lines: 2, hint: 'Made by mistake', max: 500),
                ),
              if (_error != null) TkErrorBox(_error!),
              if (widget.canEdit && _mode.isEmpty && (owed || acts.voids))
                Wrap(
                  spacing: 10,
                  runSpacing: 10,
                  children: [
                    if (owed) TkSmallButton(label: 'Record a payment', onPressed: () => setState(() => _mode = 'pay')),
                    if (acts.voids) TkSmallButton(label: 'Cancel invoice', onPressed: () => setState(() => _mode = 'void')),
                  ],
                ),
              Align(
                alignment: Alignment.centerLeft,
                child: TkLink(
                  label: 'Open the invoice',
                  onTap: widget.onOpen == null ? () => showComingSoon(context) : () => widget.onOpen!(context, t2),
                ),
              ),
            ],
          ),
        ),
        if (widget.canEdit && buttons.isNotEmpty) sheetFoot(context, buttons),
      ],
    );
  }
}

// --- InvoiceAdd ---

/// A new invoice: who, what for and how much; sent at once or kept. [to] a
/// linked business (Invoice them): its name and key, so it reaches them on
/// Taskiem as a bill; no phone is asked.
class InvoiceAdd extends StatefulWidget {
  const InvoiceAdd({super.key, required this.auth, required this.onSaved, this.onCancel, this.to});
  final Auth auth;
  final void Function(String? line) onSaved;
  final VoidCallback? onCancel;
  final ({String name, String key})? to;

  @override
  State<InvoiceAdd> createState() => _InvoiceAddState();
}

class _InvoiceAddState extends State<InvoiceAdd> {
  late final _name = TextEditingController(text: widget.to?.name ?? '');
  final _phone = TextEditingController();
  final _what = TextEditingController();
  final _amount = TextEditingController();
  bool _busy = false;
  String? _error;
  final _key = SaveKey();

  @override
  void initState() {
    super.initState();
    for (final c in [_name, _phone, _what, _amount]) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final c in [_name, _phone, _what, _amount]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _save(bool send) async {
    final to = widget.to;
    final body = {
      'customer': to != null ? {'name': to.name, 'key': to.key} : {'name': _name.text.trim(), 'phone': _phone.text.trim()},
      'description': _what.text.trim(),
      'amount_kobo': nairaKobo(_amount.text),
    };
    setState(() => (_busy = true, _error = null));
    try {
      final key = _key.of(body);
      final x = (await widget.auth.transport.send('POST', '/v1/invoices', body: body, headers: {'Idempotency-Key': key}) as Map).cast<String, dynamic>();
      if (send) await widget.auth.transport.send('POST', invoicePath(_str(x['id']), '/send'), body: const {}, headers: {'Idempotency-Key': '$key:send'});
      widget.onSaved(send ? 'Sent ${x['number']}' : 'Made ${x['number']}');
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final to = widget.to;
    final kobo = nairaKobo(_amount.text);
    final ready = _name.text.trim().isNotEmpty && (to != null || _phone.text.trim().isNotEmpty) && _what.text.trim().isNotEmpty && kobo != null && kobo > 0;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              if (to != null)
                _now(t, 'To ${to.name}: it reaches them on Taskiem, with their bills.')
              else ...[
                TkKitField(label: 'Customer’s name', child: TkTextBox(controller: _name, hint: 'Bisi Okafor', max: 120)),
                TkKitField(label: 'Their phone number', hint: 'The invoice is sent here.', child: TkTextBox(controller: _phone, phone: true, hint: '0803 000 0000')),
              ],
              TkKitField(label: 'What it is for', child: TkTextBox(controller: _what, hint: 'Catering for 50', max: 200)),
              TkKitField(
                label: 'Amount (₦)',
                error: _amount.text.trim().isNotEmpty && kobo == null ? 'An amount in naira, like 45,000.' : null,
                child: TkTextBox(controller: _amount, money: true, hint: '45000'),
              ),
              if (_error != null) TkErrorBox(_error!),
            ],
          ),
        ),
        sheetFoot(context, [
          if (widget.onCancel != null) TkGhostButton(label: 'Cancel', onTap: _busy ? null : widget.onCancel),
          TkGhostButton(label: 'Keep as draft', onTap: _busy || !ready ? null : () => _save(false)),
          TkCta(label: 'Send it', onTap: _busy || !ready ? null : () => _save(true)),
        ]),
      ],
    );
  }
}

// --- BillView ---

/// A bill from a linked supplier: what is due, Pay (their page, or from
/// Taskiem after an approval), question it, or say it was paid outside.
class BillView extends StatefulWidget {
  const BillView({super.key, required this.auth, required this.bill, required this.onSaved, this.canEdit = true, this.onOpen});
  final Auth auth;
  final Map<String, dynamic> bill;
  final void Function(String? line) onSaved;
  final bool canEdit;

  /// The bill's own screen (How to pay, Open the bill); null: coming.
  final void Function(BuildContext context, String id)? onOpen;

  @override
  State<BillView> createState() => _BillViewState();
}

class _BillViewState extends State<BillView> {
  Map<String, dynamic>? _settings;
  String _mode = '';
  final _reason = TextEditingController();
  final _amount = TextEditingController();
  String _how = 'transfer';
  bool _busy = false;
  String? _error;

  Map<String, dynamic> get _b => widget.bill;
  String get _ref => _str(_b['reference']);
  String get _supplier => _str(_b['supplier_name']);

  @override
  void initState() {
    super.initState();
    _reason.addListener(() => setState(() {}));
    _amount.addListener(() => setState(() {}));
    widget.auth.transport.send('GET', '/v1/bills/pay').then((r) {
      if (mounted) setState(() => _settings = (r as Map).cast<String, dynamic>());
    }, onError: (Object _) {});
  }

  @override
  void dispose() {
    _reason.dispose();
    _amount.dispose();
    super.dispose();
  }

  Future<void> _run(String path, Object body, String words) async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('POST', '/v1/bills/${Uri.encodeComponent(_str(_b['id']))}/$path', body: body);
      widget.onSaved(words);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  void _open() => widget.onOpen == null ? showComingSoon(context) : widget.onOpen!(context, _str(_b['id']));

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = _b;
    final hero = billHero(b);
    final st = billStatus(_str(b['status']));
    final due = _int(b['due_kobo']);
    final dueOn = DateTime.tryParse(_str(b['due_on']));
    final dueWords = dueOn == null ? '' : '${_weekdays[dueOn.weekday - 1]} ${dueOn.day} ${_months[dueOn.month - 1]}';
    final fromTaskiem = widget.canEdit && canPay(b) && _str(_settings?['workflow_id']).isNotEmpty;
    final kobo = nairaKobo(_amount.text);
    final link = _str(b['pay_link']);
    final buttons = <Widget>[
      if (_mode.isNotEmpty) TkGhostButton(label: 'Not now', onTap: _busy ? null : () => setState(() => (_mode = '', _error = null))),
      if (_mode == 'query')
        TkCta(label: 'Send the question', onTap: _busy || _reason.text.trim().isEmpty ? null : () => _run('query', {'reason': _reason.text.trim()}, 'Questioned $_ref'))
      else if (_mode == 'paid')
        TkCta(
          label: 'Tell $_supplier',
          onTap: _busy || kobo == null || kobo <= 0
              ? null
              : () => _run('declare', {'amount_kobo': kobo, 'how': _how}, 'Paid ${naira(kobo)} of $_ref outside Taskiem'),
        )
      else if (due > 0) ...[
        if (link.isNotEmpty)
          (fromTaskiem ? TkGhostButton.new : TkCta.new)(
            label: 'Pay ${naira(due)}',
            onTap: () => openOrSay(context, () => openInBrowser(Uri.parse(link)), '$_supplier’s page could not be opened.'),
          ),
        if (fromTaskiem) TkCta(label: 'Pay from Taskiem', onTap: _busy ? null : () => _run('pay', const {}, 'Paying $_ref from Taskiem')),
        if (link.isEmpty && !fromTaskiem) TkCta(label: 'How to pay', onTap: _open),
      ] else
        TkCta(label: 'Open the bill', onTap: _open),
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              BillHero(
                kicker: '${hero.label}${dueWords.isNotEmpty && due > 0 ? ' · due $dueWords' : ''}',
                amount: naira(hero.kobo),
                sub: hero.sub,
                pill: st.label != hero.label ? (text: st.label, tone: st.tone) : null,
              ),
              if (sentLine(b).isNotEmpty) _now(t, sentLine(b)),
              TkLines([
                for (final l in _maps(b['lines']))
                  ('${l['quantity'] ?? 1} × ${_str(l['description'])}', naira(((l['quantity'] as num? ?? 1) * _int(l['unit_kobo'])).round())),
              ]),
              if (_str(b['pay_account_number']).isNotEmpty && due > 0)
                _now(
                  t,
                  'Or transfer to ${b['pay_account_number']}${_str(b['pay_bank_name']).isNotEmpty ? ' (${b['pay_bank_name']})' : ''} with $_ref in the note: it is counted by itself.',
                ),
              if (_mode == 'query')
                TkKitField(
                  label: 'What is wrong with $_ref?',
                  hint: '$_supplier is told, and it is not paid from Taskiem until it is settled.',
                  child: TkTextBox(controller: _reason, lines: 3, hint: 'Ten bags were torn', max: 500),
                ),
              if (_mode == 'paid') ...[
                TkKitField(
                  label: 'What you paid outside Taskiem (₦)',
                  hint: '$_supplier is told. It counts as paid when their Taskiem matches the money.',
                  error: _amount.text.trim().isNotEmpty && kobo == null ? 'An amount in naira, like 25,000.' : null,
                  child: TkTextBox(controller: _amount, money: true, hint: '25,000'),
                ),
                choiceBox(
                  context,
                  label: 'How',
                  value: _how,
                  choices: const [('transfer', 'By transfer'), ('cash', 'In cash'), ('other', 'Another way')],
                  onPick: (v) => setState(() => _how = v),
                ),
              ],
              if (_error != null) TkErrorBox(_error!),
              if (widget.canEdit && _mode.isEmpty && due > 0)
                Wrap(
                  spacing: 10,
                  runSpacing: 10,
                  children: [
                    TkSmallButton(label: b['disputed'] == true ? 'Questioned' : 'Question it', onPressed: b['disputed'] == true ? null : () => setState(() => _mode = 'query')),
                    TkSmallButton(label: 'I paid outside Taskiem', onPressed: () => setState(() => _mode = 'paid')),
                  ],
                ),
            ],
          ),
        ),
        sheetFoot(context, buttons),
      ],
    );
  }
}

// --- MoneyEntryView ---

/// One entry of the money log, and matching it to an invoice.
class MoneyEntryView extends StatefulWidget {
  const MoneyEntryView({super.key, required this.auth, required this.entry, required this.onSaved, this.canEdit = true});
  final Auth auth;
  final Map<String, dynamic> entry;
  final void Function(String? line) onSaved;
  final bool canEdit;

  @override
  State<MoneyEntryView> createState() => _MoneyEntryViewState();
}

class _MoneyEntryViewState extends State<MoneyEntryView> {
  bool _picking = false;
  List<Map<String, dynamic>>? _owed;
  String _chosen = '';
  bool _busy = false;
  String? _error;

  Map<String, dynamic> get _e => widget.entry;

  Future<void> _pick() async {
    setState(() => (_picking = true, _error = null));
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/invoices', query: {'limit': '200'}) as Map).cast<String, dynamic>();
      if (mounted) setState(() => _owed = _maps(r['invoices']).where(owing).toList());
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _match() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('POST', '/v1/money/entries/${Uri.encodeComponent(_str(_e['id']))}/match', body: {'invoice': _chosen});
      widget.onSaved('Matched ${entryAmount(_e)} to $_chosen');
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final e = _e;
    final pill = entryPill(e);
    final when = _str(e['when']);
    final words = towardsWords(e);
    final owed = _owed;
    final left = _int(e['left_kobo']);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              BillHero(
                kicker: '${e['direction'] == 'in' ? 'Came in' : 'Went out'} · ${dateLabel(when)}',
                amount: entryAmount(e),
                sub: entryName(e),
                pill: pill,
              ),
              TkLines([
                if (_str(e['channel']).isNotEmpty) ('Where', e['channel'] == 'cash' ? 'Cash' : _str(e['channel'])),
                if (_str(e['reference']).isNotEmpty) ('Reference', _str(e['reference'])),
                if (_str(e['vendor']).isNotEmpty) ('Paid to', _str(e['vendor'])),
                if (_str(e['category']).isNotEmpty) ('Kind of spending', _str(e['category'])),
                if (_int(e['fee_kobo']) > 0) ('Fee', naira(_int(e['fee_kobo']))),
                if (left > 0 && e['amount_kobo'] != null && left < _int(e['amount_kobo'])) ('Left to match', naira(left)),
              ]),
              if (words.isNotEmpty) _now(t, words),
              if (_picking)
                if (owed == null)
                  // Its error, when reading them failed, says so instead.
                  (_error == null ? _now(t, 'Loading invoices…') : const SizedBox.shrink())
                else if (owed.isEmpty)
                  _now(t, 'No invoices are waiting to be paid.')
                else
                  choiceBox(
                    context,
                    label: 'The invoice it pays',
                    value: _chosen.isEmpty ? '' : _chosen,
                    choices: [
                      if (_chosen.isEmpty) ('', 'Choose an invoice'),
                      for (final x in owed) (_str(x['number']), '${x['number']} · ${_str((x['customer'] as Map?)?['name'])} · ${naira(_int(x['due_kobo']))} to pay'),
                    ],
                    onPick: (v) => setState(() => _chosen = v),
                  ),
              if (_error != null) TkErrorBox(_error!),
            ],
          ),
        ),
        if (widget.canEdit && matchable(e))
          sheetFoot(
            context,
            _picking
                ? [
                    TkGhostButton(label: 'Not now', onTap: _busy ? null : () => setState(() => (_picking = false, _error = null))),
                    TkCta(label: 'Match it', onTap: _busy || _chosen.isEmpty ? null : _match),
                  ]
                : [TkCta(label: 'Match to an invoice', onTap: _pick)],
          ),
      ],
    );
  }
}

// --- MoneyAdd ---

/// Cash in or out, or an expense, added to the money log.
class MoneyAdd extends StatefulWidget {
  const MoneyAdd({super.key, required this.auth, required this.onSaved, this.onCancel, this.now});
  final Auth auth;
  final void Function(String? line) onSaved;
  final VoidCallback? onCancel;
  final DateTime Function()? now;

  @override
  State<MoneyAdd> createState() => _MoneyAddState();
}

class _MoneyAddState extends State<MoneyAdd> {
  String _kind = 'expense';
  final _amount = TextEditingController();
  final _description = TextEditingController();
  final _vendor = TextEditingController();
  late final _date = TextEditingController(text: _today((widget.now ?? DateTime.now)()));
  bool _tried = false;
  bool _busy = false;
  String? _error;
  final _key = SaveKey();

  List<TextEditingController> get _boxes => [_amount, _description, _vendor, _date];

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
    description: _description.text,
    date: _date.text.trim(),
    vendor: _vendor.text,
    now: (widget.now ?? DateTime.now)(),
  );

  Future<void> _save() async {
    setState(() => _tried = true);
    if (_problem != null) return;
    final kobo = nairaKobo(_amount.text)!;
    final body = <String, Object?>{
      if (_kind == 'expense') ...{'kind': 'expense'} else ...{'kind': 'cash', 'direction': _kind == 'cash_in' ? 'in' : 'out'},
      'amount_kobo': kobo,
      'description': _description.text.trim(),
      if (_date.text.trim().isNotEmpty) 'occurred_at': _date.text.trim(),
      if (_kind == 'expense' && _vendor.text.trim().isNotEmpty) 'vendor': _vendor.text.trim(),
    };
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('POST', '/v1/money/entries', body: body, headers: {'Idempotency-Key': _key.of(body)});
      widget.onSaved('${entryKinds.firstWhere((k) => k.$1 == _kind).$2} of ${naira(kobo)} added');
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final kind = entryKinds.firstWhere((k) => k.$1 == _kind);
    final problem = _problem;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              choiceBox(context, label: 'What it is', value: _kind, choices: [for (final k in entryKinds) (k.$1, k.$2)], onPick: (v) => setState(() => _kind = v)),
              Text(kind.$3, style: tkHint(t)),
              TkKitField(label: 'Amount (₦)', child: TkTextBox(controller: _amount, money: true, hint: '12000')),
              TkKitField(
                label: 'What it was for',
                child: TkTextBox(controller: _description, hint: _kind == 'expense' ? 'Diesel for the generator' : 'Cash sales', max: 200),
              ),
              if (_kind == 'expense') TkKitField(label: 'Paid to (if you like)', child: TkTextBox(controller: _vendor, hint: 'Total filling station', max: 120)),
              TkKitField(label: 'Date', hint: 'As 2026-11-12', child: TkTextBox(controller: _date, hint: _today((widget.now ?? DateTime.now)()))),
              if (_tried && problem != null) Semantics(liveRegion: true, child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger))),
              if (_error != null) TkErrorBox(_error!),
            ],
          ),
        ),
        sheetFoot(context, [
          if (widget.onCancel != null) TkGhostButton(label: 'Cancel', onTap: _busy ? null : widget.onCancel),
          TkCta(label: 'Add it', onTap: _busy ? null : _save),
        ]),
      ],
    );
  }
}
