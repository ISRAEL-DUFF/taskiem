// Invoices (web Invoices.tsx, lib/invoices.ts): what customers are asked to
// pay, newest first; one invoice with what is due, its lines, amendments,
// details, customer and payments (each undone after yes), sent or voided,
// its page shared from the phone; a new invoice and a draft changed; the
// invoice settings and credit terms. Each invoice is marked paid only by
// the payments counted towards it.

import 'package:flutter/widgets.dart';
import 'package:share_plus/share_plus.dart';

import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show TkSwitchRow, errorWords, nairaKobo;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkPill, TkRow;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'doc_thread.dart' show DocThread;
import 'invoice_match.dart' show CustomerMatching, InvoiceMatchPage, groupSuggestions, suggestionsPath;
import 'money_sheets.dart' show dateLabel, invoiceActions, invoicePath, invoiceStatus;
import 'product_form.dart' show SaveKey, formKey;
import 'storefront_orders_page.dart' show isMasked;
import 'words.dart' show CardTone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';
Map<String, dynamic> _map(Object? v) => (v as Map?)?.cast<String, dynamic>() ?? const {};

void _push(BuildContext context, Widget page) => Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => page));

/// Opens one invoice (its id or number) on its own screen.
void openInvoice(BuildContext context, Auth auth, String id) => _push(context, InvoicePage(auth: auth, id: id));

/// Opens Invoices.
void openInvoices(BuildContext context, Auth auth) => _push(context, InvoicesPage(auth: auth));

/// invoices.ts INVOICE_FILTERS: "" is all; credit, the notebook debts.
const invoiceFilters = [
  ('', 'All'),
  ('credit', 'Notebook debts'),
  ('draft', 'Draft'),
  ('recorded', 'Recorded'),
  ('sent', 'Sent'),
  ('part_paid', 'Part-paid'),
  ('paid', 'Paid'),
  ('overdue', 'Overdue'),
  ('void', 'Void'),
];

/// invoices.ts invoicesPath, as the query.
Map<String, String> invoicesQuery(String status) => status == 'credit' ? {'kind': 'credit'} : (status.isEmpty ? const {} : {'status': status});

/// invoices.ts invoiceHero: the figure that matters for its status.
({String label, int kobo, String sub, double paid}) invoiceHero(Map<String, dynamic> x) {
  final total = _int(x['total_kobo']), paidKobo = _int(x['paid_kobo']), due = _int(x['due_kobo']);
  final paid = total > 0 ? (paidKobo / total).clamp(0.0, 1.0) : 0.0;
  return switch (x['status']) {
    'draft' => (label: 'To ask for', kobo: total, sub: 'Not sent yet', paid: paid),
    'void' => (label: 'Void', kobo: total, sub: paidKobo > 0 ? '${naira(paidKobo)} was paid before it was voided' : 'It takes no payment', paid: paid),
    'paid' => (label: 'Paid in full', kobo: total, sub: 'Nothing left to pay', paid: 1.0),
    _ => (label: x['status'] == 'overdue' ? 'Overdue' : 'Still due', kobo: due, sub: 'of ${naira(total)} · ${naira(paidKobo)} paid', paid: paid),
  };
}

/// invoices.ts sentLines: what Send did, in words.
List<String> sentLines(Map<String, dynamic> s) {
  final q = ((s['queued'] as List?) ?? const []).whereType<String>().toList();
  final by = [if (q.contains('sms')) 'SMS', if (q.contains('email')) 'email'];
  return [
    by.isNotEmpty ? 'Sent by ${by.join(' and ')}.' : 'Nothing was sent: the customer has no phone number or email we can use.',
    if (s['sms_over_allowance'] == true) 'SMS not sent: plan allowance used.',
    if (_int(s['credit_kobo']) > 0) '${naira(_int(s['credit_kobo']))} of the customer’s credit counts towards it.',
  ];
}

/// invoices.ts lineNote.
String lineNote(Map<String, dynamic> l) => [
  if (_str(l['pass_through_for']).isNotEmpty) 'Collected on behalf of ${l['pass_through_for']}.',
  if (l['amendment'] == true && _str(l['kind']).isEmpty) 'Added after the invoice was sent.',
  if (_str(l['reason']).isNotEmpty) _str(l['reason']),
].join(' ');

/// invoices.ts voidProblem.
String? voidProblem(String reason) {
  if (reason.trim().isEmpty) return 'Say why it is void. The customer’s page shows the reason.';
  if (reason.trim().length > 500) return 'The reason is at most 500 characters.';
  return null;
}

/// invoices.ts removableLines: the items a removal may take out.
List<({int index, String label})> removableLines(Map<String, dynamic> x) {
  final gone = {
    for (final a in _maps(x['amendments']))
      if (a['removes'] is num) (a['removes'] as num).toInt(),
  };
  final lines = _maps(x['lines']);
  return [
    for (var i = 0; i < lines.length; i++)
      if (_str(lines[i]['kind']).isEmpty && _int(lines[i]['unit_kobo']) > 0 && !gone.contains(i))
        (index: i, label: '${_str(lines[i]['description'])} (${naira(((lines[i]['quantity'] as num? ?? 1) * _int(lines[i]['unit_kobo'])).round())})'),
  ];
}

/// invoices.ts amendmentProblem.
String? amendmentProblem({required String kind, required String amount, required String description, required String line, required String reason}) {
  if (reason.trim().isEmpty) return 'Say why the invoice is amended. The customer’s page shows the reason.';
  if (reason.trim().length > 300) return 'The reason is at most 300 characters.';
  if (kind == 'remove') return line.isEmpty ? 'Choose the item to take out.' : null;
  final k = nairaKobo(amount);
  if (k == null || k < 100) return 'Give the amount, at least ₦1.';
  if (kind == 'add' && description.trim().isEmpty) return 'Say what the added line is for.';
  return null;
}

/// invoices.ts paymentRows: each payment, and its undoing when undone.
List<({Map<String, dynamic> payment, Map<String, dynamic>? undoneBy})> paymentRows(List<Map<String, dynamic>> list) {
  final undoings = {
    for (final p in list)
      if (_str(p['undoes']).isNotEmpty) _str(p['undoes']): p,
  };
  return [
    for (final p in list)
      if (_str(p['undoes']).isEmpty)
        (
          payment: p,
          undoneBy:
              undoings[_str(p['id'])] ??
              (_str(p['undone_by']).isNotEmpty ? (list.where((x) => x['id'] == p['undone_by']).firstOrNull ?? {'id': p['undone_by']}) : null),
        ),
  ];
}

/// invoices.ts howMatched.
String howMatched(String how, String by) => switch (how) {
  'exact' => 'Paid into the account Taskiem gave',
  'suggested' => by.isNotEmpty ? 'Suggested, confirmed by $by' : 'Suggested, then confirmed',
  'suggested_auto' => 'Matched automatically',
  'manual' => 'Matched by hand',
  _ => how.replaceAll('_', ' '),
};

const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// ui.tsx fmtTime, in Lagos: "12 Nov 2026, 14:05".
String fmtTime(String iso) {
  final t = DateTime.tryParse(iso);
  if (t == null) return '';
  final l = t.toUtc().add(const Duration(hours: 1));
  return '${l.day} ${_months[l.month - 1]} ${l.year}, ${l.hour.toString().padLeft(2, '0')}:${l.minute.toString().padLeft(2, '0')}';
}

CardTone _tone(String status) => switch (status) {
  'paid' => CardTone.lime,
  'overdue' || 'part_paid' => CardTone.warn,
  _ => CardTone.plain,
};

/// `.notice`.
class _Notice extends StatelessWidget {
  const _Notice(this.children);
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      liveRegion: true,
      container: true,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        decoration: BoxDecoration(color: t.tkLimeSoft, borderRadius: BorderRadius.circular(14)),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: children),
      ),
    );
  }
}

/// `dl.kv`: each word, then what it is.
Widget _kv(TkTokens t, List<(String, Widget)> rows) => Column(
  crossAxisAlignment: CrossAxisAlignment.stretch,
  children: [
    for (final (k, v) in rows)
      Padding(
        padding: const EdgeInsets.only(bottom: 10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(k, style: tkText(size: 13, weight: 600, color: t.muted)),
            const SizedBox(height: 2),
            v,
          ],
        ),
      ),
  ],
);

Widget _text(TkTokens t, String s, {bool hint = false}) => Text(s, style: hint ? tkHint(t) : tkText(size: 15, color: t.text));

// --- the list ---

/// Invoices: newest first, by status, read again while seen.
class InvoicesPage extends StatefulWidget {
  const InvoicesPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<InvoicesPage> createState() => _InvoicesPageState();
}

class _InvoicesPageState extends State<InvoicesPage> with Polling {
  List<Map<String, dynamic>>? _list;
  String? _error;
  String _status = '';
  int _asked = 0;
  int _waiting = 0;

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _load() async {
    final asked = ++_asked;
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/invoices', query: invoicesQuery(_status)) as Map).cast<String, dynamic>();
      if (mounted && asked == _asked) setState(() => (_list = _maps(r['invoices']), _error = null));
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _error = errorWords(e));
    }
    try {
      // How many payments wait (a refusal leaves the button without one).
      final r = (await widget.auth.transport.send('GET', suggestionsPath) as Map).cast<String, dynamic>();
      if (mounted) setState(() => _waiting = groupSuggestions(_maps(r['suggestions'])).length);
    } on Object {
      // No number, then.
    }
  }

  Future<void> _new() async {
    await Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => InvoiceEditorPage(auth: widget.auth)));
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final list = _list;
    final manage = widget.auth.can('invoice.manage');
    return TkPageScreen(
      title: 'Invoices',
      description: 'What you ask your customers to pay, newest first. Each one is marked paid only by the payments counted towards it.',
      onRefresh: _load,
      action: manage ? TkSmallButton(label: 'New', semantics: 'New invoice', primary: true, onPressed: _new) : null,
      children: [
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkSmallButton(
              label: 'Show: ${invoiceFilters.firstWhere((f) => f.$1 == _status).$2}',
              onPressed: () async {
                final pick = await showTkModalChoices(context, title: 'Show', value: _status, choices: invoiceFilters);
                if (pick == null || pick == _status) return;
                setState(() => (_status = pick, _list = null));
                await _load();
              },
            ),
            TkSmallButton(
              label: _waiting > 0 ? 'To match ($_waiting)' : 'To match',
              semantics: _waiting > 0 ? 'To match: $_waiting ${_waiting == 1 ? 'payment waiting' : 'payments waiting'}' : 'To match',
              onPressed: () async {
                await Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => InvoiceMatchPage(auth: widget.auth)));
                await _load();
              },
            ),
            TkSmallButton(
              label: 'Settings',
              semantics: 'Invoice settings',
              onPressed: () => _push(context, InvoiceSettingsPage(auth: widget.auth)),
            ),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
        if (list == null && _error == null) Text('Loading invoices…', style: tkHint(t)),
        if (list != null && list.isEmpty)
          TkEmpty(
            icon: 'invoices',
            title: _status.isNotEmpty ? 'No invoices with this status.' : 'No invoices yet',
            line: _status.isNotEmpty ? null : 'Make one, send it to your customer by SMS and email, and see it marked paid as the money comes in.',
            action: _status.isEmpty && manage ? TkSmallButton(label: 'New invoice', primary: true, onPressed: _new) : null,
          ),
        if (list != null && list.isNotEmpty)
          TkPageCard(
            children: [
              for (final x in list)
                () {
                  final c = _map(x['customer']);
                  final who = [_str(c['name']), _str(c['phone']).isNotEmpty ? _str(c['phone']) : _str(c['email'])].where((s) => s.isNotEmpty).join(' · ');
                  final st = invoiceStatus(_str(x['status']));
                  return TkRow(
                    name: who.isEmpty ? '—' : who,
                    icon: 'invoices',
                    tone: x['status'] == 'overdue' ? CardTone.danger : null,
                    sub: [
                      '${x['number']}${_str(x['due_on']).isNotEmpty ? ' · due ${dateLabel(_str(x['due_on']))}' : ''}',
                      [
                        naira(_int(x['total_kobo'])),
                        if (x['status'] != 'void' && x['status'] != 'paid' && _int(x['due_kobo']) != _int(x['total_kobo']))
                          '${naira(_int(x['due_kobo']))} still due',
                      ].join(' · '),
                    ].join('\n'),
                    pill: TkPill(st.label, tone: _tone(_str(x['status']))),
                    onTap: () async {
                      await Navigator.of(context).push(
                        PageRouteBuilder<void>(
                          pageBuilder: (_, _, _) => InvoicePage(auth: widget.auth, id: _str(x['number']), backLabel: 'Back to Invoices'),
                        ),
                      );
                      await _load();
                    },
                  );
                }(),
            ],
          ),
      ],
    );
  }
}

// --- one invoice ---

/// One invoice: what is due, what it is for, its details, its customer and
/// its payments; sent, voided, amended.
class InvoicePage extends StatefulWidget {
  const InvoicePage({super.key, required this.auth, required this.id, this.backLabel = 'Back', this.sent, this.follow = true});
  final Auth auth;
  final String id;
  final String backLabel;

  /// What Send answered, when it was sent just now (from the editor).
  final Map<String, dynamic>? sent;
  final bool follow;

  @override
  State<InvoicePage> createState() => _InvoicePageState();
}

class _InvoicePageState extends State<InvoicePage> with Polling {
  Map<String, dynamic>? _view;
  String? _error;
  late Map<String, dynamic>? _sent = widget.sent;
  String _sendKey = formKey();
  bool _voiding = false;
  bool _voidTried = false;
  final _reason = TextEditingController();
  String? _undoing;
  Map<String, dynamic>? _revealed;
  bool _busy = false;
  String? _actError;
  int _asked = 0;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _reason.addListener(() => setState(() {}));
    _load();
    if (widget.follow) startPolling(const Duration(seconds: 30), _load);
  }

  @override
  void dispose() {
    _reason.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final asked = ++_asked;
    try {
      final r = (await _auth.transport.send('GET', invoicePath(widget.id)) as Map).cast<String, dynamic>();
      if (mounted && asked == _asked) setState(() => (_view = r, _error = null));
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _actError = null));
    try {
      await f();
      if (mounted && done.isNotEmpty) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _send(Map<String, dynamic> x) => _run(() async {
    final r = (await _auth.transport.send('POST', invoicePath(_str(x['id']), '/send'), body: const {}, headers: {'Idempotency-Key': _sendKey}) as Map)
        .cast<String, dynamic>();
    if (mounted) setState(() => (_sent = r, _sendKey = formKey()));
  }, 'Invoice sent');

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final x = _view == null ? null : _map(_view!['invoice']);
    return TkPageScreen(
      title: 'Invoice ${x == null ? widget.id : _str(x['number'])}',
      backLabel: widget.backLabel,
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (x == null && _error == null) Text('Loading the invoice…', style: tkHint(t)),
        if (x != null) ..._invoice(t, x),
      ],
    );
  }

  List<Widget> _invoice(TkTokens t, Map<String, dynamic> x) {
    final may = invoiceActions(_str(x['status']), _auth.can);
    final h = invoiceHero(x);
    final c = _revealed ?? _map(x['customer']);
    final number = _str(x['number']);
    final lines = _maps(x['lines']);
    final key = _str(_map(x['customer'])['key']);
    final sent = _sent;
    final link = _str(sent?['link']);
    final reasonProblem = voidProblem(_reason.text);
    return [
      TkHero(
        label: h.label,
        tag: invoiceStatus(_str(x['status'])).label,
        amount: naira(h.kobo),
        sub:
            '${h.sub}${x['status'] != 'draft' && x['status'] != 'void' ? ' · ${(h.paid * 100).round()}% paid' : ''}\nCustomer ${_str(_map(x['customer'])['name']).isEmpty ? '—' : _map(x['customer'])['name']} · Due ${dateLabel(_str(x['due_on'])).isEmpty ? 'no date set' : dateLabel(_str(x['due_on']))}',
        semanticsLabel: 'What is due',
      ),
      if (x['disputed'] == true) _SettleQuery(auth: _auth, number: number, onDone: _load),
      if (key.startsWith('link:')) DocThread(key: ValueKey('thread $number'), auth: _auth, doc: 'invoice', reference: number, follow: widget.follow),
      if (x['kind'] == 'credit')
        Text(
          'A notebook debt, recorded in chat: owed, and counted with the customer’s invoices, but not sent to them${_str(x['sent_at']).isNotEmpty ? ' until it was sent with their balance' : ''}.',
          style: tkHint(t),
        ),
      if (sent != null)
        _Notice([
          for (final l in sentLines(sent)) _text(t, l),
          if (link.isNotEmpty) ...[
            const SizedBox(height: 6),
            Wrap(
              spacing: 12,
              runSpacing: 4,
              children: [
                TkLink(label: 'Its page', onTap: () => openOrSay(context, () => openInBrowser(Uri.parse(link)), 'Its page could not be opened.')),
                TkLink(
                  label: 'Share its page',
                  onTap: () => SharePlus.instance.share(ShareParams(text: link, subject: 'Invoice $number')),
                ),
              ],
            ),
          ],
        ]),
      if (may.edit || may.send || may.sendAgain || may.voids)
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            if (may.edit)
              TkSmallButton(
                label: 'Edit',
                onPressed: () async {
                  await Navigator.of(context).push(
                    PageRouteBuilder<void>(
                      pageBuilder: (_, _, _) => InvoiceEditorPage(auth: _auth, existing: x),
                    ),
                  );
                  await _load();
                },
              ),
            if (may.send) TkSmallButton(label: 'Send', primary: true, onPressed: _busy ? null : () => _send(x)),
            if (may.sendAgain) TkSmallButton(label: 'Send again', onPressed: _busy ? null : () => _send(x)),
            if (may.voids && !_voiding) TkSmallButton(label: 'Void', danger: true, onPressed: () => setState(() => _voiding = true)),
          ],
        ),
      if (_actError != null) TkErrorBox(_actError!),
      if (_voiding && may.voids)
        TkPageCard(
          title: 'Void $number?',
          children: [
            _text(
              t,
              'A void invoice takes no new payment, and its page tells the customer why. It cannot be undone: to ask for the money again, send a new invoice.',
            ),
            const SizedBox(height: 10),
            TkKitField(
              label: 'Reason',
              error: _voidTried ? reasonProblem : null,
              child: TkTextBox(controller: _reason, lines: 2, max: 500),
            ),
            const SizedBox(height: 10),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                TkSmallButton(
                  label: 'Void invoice',
                  danger: true,
                  onPressed: _busy
                      ? null
                      : () {
                          setState(() => _voidTried = true);
                          if (reasonProblem != null) return;
                          _run(() async {
                            await _auth.transport.send('POST', invoicePath(_str(x['id']), '/void'), body: {'reason': _reason.text.trim()});
                            if (mounted) setState(() => (_voiding = false, _voidTried = false, _reason.text = ''));
                          }, 'Invoice voided');
                        },
                ),
                TkSmallButton(label: 'Keep it', onPressed: () => setState(() => (_voiding = false, _voidTried = false))),
              ],
            ),
          ],
        ),
      TkPageCard(
        title: 'What it is for',
        children: [
          if (lines.isEmpty)
            _text(t, 'No items.', hint: true)
          else
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
                            _text(t, _str(l['description'])),
                            Text(
                              '${l['quantity'] ?? 1}${_str(l['kind']).isEmpty ? ' × ${naira(_int(l['unit_kobo']))}' : ''}${lineNote(l).isNotEmpty ? ' · ${lineNote(l)}' : ''}',
                              style: tkHint(t),
                            ),
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
                Text(naira(_int(x['total_kobo'])), style: tkText(size: 15, weight: 700, color: t.text)),
              ],
            ),
          ),
        ],
      ),
      if (may.amend) _Amend(key: ValueKey('amend $number'), auth: _auth, invoice: x, onDone: _load),
      TkPageCard(
        title: 'Details',
        children: [
          _kv(t, [
            (
              'Status',
              Align(
                alignment: Alignment.centerLeft,
                child: TkPill(invoiceStatus(_str(x['status'])).label, tone: _tone(_str(x['status']))),
              ),
            ),
            ('Due date', _text(t, dateLabel(_str(x['due_on'])).isEmpty ? 'None' : dateLabel(_str(x['due_on'])), hint: dateLabel(_str(x['due_on'])).isEmpty)),
            ('Made', _text(t, fmtTime(_str(x['created_at'])))),
            ('Sent', _text(t, _str(x['sent_at']).isEmpty ? 'Not yet' : fmtTime(_str(x['sent_at'])), hint: _str(x['sent_at']).isEmpty)),
            if (_str(x['paid_at']).isNotEmpty) ('Paid', _text(t, fmtTime(_str(x['paid_at'])))),
            if (_str(x['voided_at']).isNotEmpty)
              ('Voided', _text(t, '${fmtTime(_str(x['voided_at']))}${_str(x['void_reason']).isNotEmpty ? '\n${x['void_reason']}' : ''}')),
            if (_str(x['note']).isNotEmpty) ('Note', _text(t, _str(x['note']))),
            ('Payments connection', _text(t, _str(x['connection']))),
            ('Transfer fee', _text(t, x['fee_paid_by'] == 'customer' ? 'Paid by the customer' : 'Paid by the business')),
            ('Part payments', _text(t, x['allow_part_payments'] == true ? 'Allowed' : 'Not allowed')),
          ]),
        ],
      ),
      TkPageCard(
        title: 'Customer',
        children: [
          _kv(t, [
            ('Name', _text(t, _str(c['name']).isEmpty ? '—' : _str(c['name']))),
            (
              'Phone',
              _str(c['phone']).isEmpty
                  ? _text(t, '—', hint: true)
                  : (isMasked(_str(c['phone']))
                        ? _text(t, _str(c['phone']))
                        : Align(
                            alignment: Alignment.centerLeft,
                            child: TkLink(
                              label: 'Call ${c['phone']}',
                              onTap: () => openOrSay(context, () => callPhone(_str(c['phone'])), 'That number could not be called.'),
                            ),
                          )),
            ),
            (
              'Email',
              _str(c['email']).isEmpty
                  ? _text(t, '—', hint: true)
                  : (isMasked(_str(c['email']))
                        ? _text(t, _str(c['email']))
                        : Align(
                            alignment: Alignment.centerLeft,
                            child: TkLink(
                              label: _str(c['email']),
                              onTap: () => openOrSay(context, () => mailTo(_str(c['email'])), 'Your mail app could not be opened.'),
                            ),
                          )),
            ),
          ]),
          if (_revealed == null && may.reveal) ...[
            Align(
              alignment: Alignment.centerLeft,
              child: TkSmallButton(
                label: 'Show customer details',
                onPressed: _busy
                    ? null
                    : () => _run(() async {
                        final r = (await _auth.transport.send('GET', invoicePath(_str(x['id'])), query: {'reveal': 'true'}) as Map).cast<String, dynamic>();
                        if (mounted) setState(() => _revealed = _map(_map(r['invoice'])['customer']));
                      }, ''),
              ),
            ),
            const SizedBox(height: 6),
            Text('Showing them is recorded in the audit log.', style: tkHint(t)),
          ],
          if (_revealed == null && !may.reveal) Text('The customer’s details are hidden. Someone who may see personal data can show them.', style: tkHint(t)),
        ],
      ),
      if (x['status'] != 'draft') CustomerMatching(key: ValueKey('money $number'), auth: _auth, invoice: x),
      TkPageCard(title: 'Payments', children: [_payments(t, x, may.undo)]),
    ];
  }

  Widget _payments(TkTokens t, Map<String, dynamic> x, bool canUndo) {
    final rows = paymentRows(_maps(x['payments']));
    if (rows.isEmpty) {
      return Text(
        'No payments yet. Payments are counted here as they come in from your payments connection, or when money received is matched to this invoice. Nobody marks an invoice paid without a payment.',
        style: tkHint(t),
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final r in rows)
          () {
            final p = r.payment;
            final undone = r.undoneBy != null;
            final strike = undone ? TextDecoration.lineThrough : null;
            return Padding(
              padding: const EdgeInsets.only(bottom: 14),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  MergeSemantics(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          '${naira(_int(p['amount_kobo']))}${undone ? ' · Undone' : ''}',
                          style: tkText(size: 15, weight: 700, color: t.text).copyWith(decoration: strike),
                        ),
                        Text(
                          [
                            _str(p['payment_id']),
                            howMatched(_str(p['how']), _str(p['by'])),
                            if (_str(p['by']).isNotEmpty) 'by ${p['by']}',
                            fmtTime(_str(p['created_at'])),
                          ].where((s) => s.isNotEmpty).join(' · '),
                          style: tkHint(t).copyWith(decoration: strike),
                        ),
                        for (final why in ((p['reasons'] as List?) ?? const []).whereType<String>()) Text('· $why', style: tkHint(t)),
                        if (undone && _str(r.undoneBy!['created_at']).isNotEmpty)
                          Text(
                            'Undone ${fmtTime(_str(r.undoneBy!['created_at']))}${_str(r.undoneBy!['by']).isNotEmpty ? ' by ${r.undoneBy!['by']}' : ''}',
                            style: tkHint(t),
                          ),
                      ],
                    ),
                  ),
                  if (canUndo && !undone && _undoing != p['id'])
                    Padding(
                      padding: const EdgeInsets.only(top: 6),
                      child: TkSmallButton(
                        label: 'Undo',
                        semantics: 'Undo the payment ${p['payment_id']}',
                        onPressed: () => setState(() => _undoing = _str(p['id'])),
                      ),
                    ),
                  if (_undoing == p['id'] && !undone)
                    Container(
                      margin: const EdgeInsets.only(top: 8),
                      padding: const EdgeInsets.all(12),
                      decoration: BoxDecoration(color: t.tintWarn, borderRadius: BorderRadius.circular(12)),
                      child: Semantics(
                        container: true,
                        label: 'Undo this payment',
                        explicitChildNodes: true,
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              'Take ${naira(_int(p['amount_kobo']))} out of this invoice? Do this when it was matched to the wrong invoice. The payment and its undoing both stay in the history.',
                              style: tkText(size: 14, color: t.tintWarnText),
                            ),
                            const SizedBox(height: 8),
                            Wrap(
                              spacing: 8,
                              runSpacing: 8,
                              children: [
                                TkSmallButton(
                                  label: 'Undo payment',
                                  danger: true,
                                  onPressed: _busy
                                      ? null
                                      : () => _run(() async {
                                          await _auth.transport.send(
                                            'POST',
                                            invoicePath(_str(x['id']), '/payments/${Uri.encodeComponent(_str(p['id']))}/undo'),
                                            body: const {},
                                          );
                                          if (mounted) setState(() => _undoing = null);
                                        }, 'Payment undone'),
                                ),
                                TkSmallButton(label: 'Keep it', onPressed: () => setState(() => _undoing = null)),
                              ],
                            ),
                          ],
                        ),
                      ),
                    ),
                ],
              ),
            );
          }(),
      ],
    );
  }
}

/// Amend: a sent invoice is not edited but amended, with a reason; each
/// amendment shows as its own line to the customer.
class _Amend extends StatefulWidget {
  const _Amend({super.key, required this.auth, required this.invoice, required this.onDone});
  final Auth auth;
  final Map<String, dynamic> invoice;
  final Future<void> Function() onDone;

  @override
  State<_Amend> createState() => _AmendState();
}

class _AmendState extends State<_Amend> {
  bool _open = false;
  String _kind = 'add';
  String _line = '';
  final _description = TextEditingController();
  final _amount = TextEditingController();
  final _for = TextEditingController();
  final _reason = TextEditingController();
  bool _tried = false;
  bool _busy = false;
  String? _error;
  String? _waiting;

  List<TextEditingController> get _boxes => [_description, _amount, _for, _reason];

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

  Future<void> _save() async {
    setState(() => _tried = true);
    final problem = amendmentProblem(kind: _kind, amount: _amount.text, description: _description.text, line: _line, reason: _reason.text);
    if (problem != null) return;
    final body = _kind == 'remove'
        ? {'kind': 'remove', 'line': int.parse(_line), 'reason': _reason.text.trim()}
        : {
            'kind': _kind,
            'amount_kobo': nairaKobo(_amount.text),
            'reason': _reason.text.trim(),
            if (_description.text.trim().isNotEmpty) 'description': _description.text.trim(),
            if (_kind == 'add' && _for.text.trim().isNotEmpty) 'pass_through_for': _for.text.trim(),
          };
    setState(() => (_busy = true, _error = null));
    try {
      final out =
          (await widget.auth.transport.send('POST', invoicePath(_str(widget.invoice['id']), '/amendments'), body: body) as Map?)?.cast<String, dynamic>() ??
          const {};
      if (!mounted) return;
      // A school's discount over its threshold waits for a second person.
      setState(() {
        _waiting = out['code'] == 'needs_approval'
            ? '${_kind == 'remove' ? 'Taking this line out' : 'This discount'} lowers the invoice by as much as the school’s threshold or more, with what it was lowered by before: it is recorded and waits for a second person to approve it before it changes the invoice.'
            : 'Invoice amended.';
        _open = false;
        _tried = false;
        _kind = 'add';
        _line = '';
        for (final c in _boxes) {
          c.clear();
        }
      });
      await widget.onDone();
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final done = _maps(widget.invoice['amendments']);
    final removable = removableLines(widget.invoice);
    final problem = amendmentProblem(kind: _kind, amount: _amount.text, description: _description.text, line: _line, reason: _reason.text);
    return TkPageCard(
      title: 'Amendments',
      children: [
        if (_waiting != null) Padding(padding: const EdgeInsets.only(bottom: 10), child: _Notice([_text(t, _waiting!)])),
        Text(
          'A sent invoice is not edited: it is amended, with a reason, and each amendment shows as its own line to the customer. The total never goes below what was paid; a paid invoice whose total rises is part-paid again.',
          style: tkHint(t),
        ),
        for (final a in done)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: _text(
              t,
              '${_str(_map(a['line'])['description'])}: ${naira(_int(a['amount_kobo']))}. ${_str(a['reason'])} (${fmtTime(_str(a['created_at']))})',
            ),
          ),
        const SizedBox(height: 10),
        if (!_open)
          Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(label: 'Amend…', semantics: 'Amend the invoice', onPressed: () => setState(() => (_open = true, _waiting = null))),
          )
        else ...[
          TkKitField(
            field: false,
            label: 'Change',
            child: Align(
              alignment: Alignment.centerLeft,
              child: TkSmallButton(
                label: switch (_kind) {
                  'discount' => 'Give a discount',
                  'remove' => 'Take a line out',
                  _ => 'Add a line',
                },
                semantics:
                    'Change: ${switch (_kind) {
                      'discount' => 'Give a discount',
                      'remove' => 'Take a line out',
                      _ => 'Add a line',
                    }}',
                onPressed: () async {
                  final pick = await showTkModalChoices(
                    context,
                    title: 'Change',
                    value: _kind,
                    choices: [('add', 'Add a line'), ('discount', 'Give a discount'), if (removable.isNotEmpty) ('remove', 'Take a line out')],
                  );
                  if (pick != null) setState(() => _kind = pick);
                },
              ),
            ),
          ),
          const SizedBox(height: 10),
          if (_kind == 'remove')
            TkKitField(
              field: false,
              label: 'Line to take out',
              child: Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(
                  label: removable.where((r) => '${r.index}' == _line).map((r) => r.label).firstOrNull ?? 'Choose…',
                  semantics: 'Line to take out: ${removable.where((r) => '${r.index}' == _line).map((r) => r.label).firstOrNull ?? 'none chosen'}',
                  onPressed: () async {
                    final pick = await showTkModalChoices(
                      context,
                      title: 'Line to take out',
                      value: _line,
                      choices: [for (final r in removable) ('${r.index}', r.label)],
                    );
                    if (pick != null) setState(() => _line = pick);
                  },
                ),
              ),
            )
          else ...[
            TkKitField(
              label: _kind == 'add' ? 'What it is for' : 'Description (optional)',
              child: TkTextBox(controller: _description, max: 200),
            ),
            const SizedBox(height: 10),
            TkKitField(
              label: 'Amount (₦)',
              child: TkTextBox(controller: _amount, money: true),
            ),
            if (_kind == 'add') ...[
              const SizedBox(height: 10),
              TkKitField(
                label: 'Collected on behalf of (optional)',
                hint: 'For money passed on to someone else, such as an exam body.',
                child: TkTextBox(controller: _for, max: 60),
              ),
            ],
          ],
          const SizedBox(height: 10),
          TkKitField(
            label: 'Reason',
            child: TkTextBox(controller: _reason, lines: 2, max: 300),
          ),
          if (_tried && problem != null)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger)),
            ),
          if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              TkSmallButton(label: 'Amend the invoice', primary: true, onPressed: _busy ? null : _save),
              TkSmallButton(label: 'Cancel', onPressed: () => setState(() => (_open = false, _tried = false, _error = null))),
            ],
          ),
        ],
      ],
    );
  }
}

/// SettleQuery: a linked buyer queried this invoice; money that does not
/// name it is not counted until the query is settled.
class _SettleQuery extends StatefulWidget {
  const _SettleQuery({required this.auth, required this.number, required this.onDone});
  final Auth auth;
  final String number;
  final Future<void> Function() onDone;

  @override
  State<_SettleQuery> createState() => _SettleQueryState();
}

class _SettleQueryState extends State<_SettleQuery> {
  String _how = 'as_it_stands';
  final _amount = TextEditingController();
  final _note = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _amount.addListener(() => setState(() {}));
    _note.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _amount.dispose();
    _note.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final kobo = nairaKobo(_amount.text);
    final ready = _how == 'as_it_stands' || (kobo != null && kobo > 0 && _note.text.trim().isNotEmpty);
    return TkPageCard(
      title: 'Queried by the customer',
      children: [
        Text(
          'Money that does not name this invoice is not counted towards it until you settle the query. Their reason is in your conversation with them.',
          style: tkHint(t),
        ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        if (widget.auth.can('invoice.manage')) ...[
          const SizedBox(height: 10),
          for (final (v, words) in const [('as_it_stands', 'The invoice stands as it is'), ('discount', 'Take an amount off')])
            TkChoiceRow(
              control: TkRadio(checked: _how == v),
              label: Text(words, style: tkText(size: 15, color: t.text)),
              onTap: () => setState(() => _how = v),
            ),
          if (_how == 'discount') ...[
            TkKitField(
              label: 'Amount off (₦)',
              child: TkTextBox(controller: _amount, money: true, hint: '9,000'),
            ),
            const SizedBox(height: 10),
          ],
          TkKitField(
            label: _how == 'discount' ? 'Why' : 'What you tell them',
            hint: _how == 'discount' ? 'Shown on the invoice, and to them.' : 'Optional.',
            child: TkTextBox(controller: _note, max: 500),
          ),
          const SizedBox(height: 10),
          Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(
              label: 'Settle the query',
              primary: true,
              onPressed: _busy || !ready
                  ? null
                  : () async {
                      setState(() => (_busy = true, _error = null));
                      try {
                        await widget.auth.transport.send(
                          'POST',
                          invoicePath(widget.number, '/settle-query'),
                          body: _how == 'discount' ? {'how': _how, 'amount_kobo': kobo, 'note': _note.text.trim()} : {'how': _how, 'note': _note.text.trim()},
                        );
                        if (context.mounted) showTkToast(context, 'Query settled');
                        await widget.onDone();
                      } on Object catch (e) {
                        if (mounted) setState(() => _error = errorWords(e));
                      }
                      if (mounted) setState(() => _busy = false);
                    },
            ),
          ),
        ],
      ],
    );
  }
}

// --- the editor ---

class _Item {
  _Item(VoidCallback changed, {String description = '', String quantity = '1', String price = ''})
    : description = TextEditingController(text: description),
      quantity = TextEditingController(text: quantity),
      price = TextEditingController(text: price) {
    for (final c in [this.description, this.quantity, this.price]) {
      c.addListener(changed);
    }
  }
  final TextEditingController description;
  final TextEditingController quantity;
  final TextEditingController price;
  void dispose() {
    description.dispose();
    quantity.dispose();
    price.dispose();
  }

  int? get qty => RegExp(r'^\d+$').hasMatch(quantity.text.trim()) && int.parse(quantity.text.trim()) >= 1 ? int.parse(quantity.text.trim()) : null;

  /// invoices.ts itemTotal.
  int get total {
    final p = nairaKobo(price.text);
    return qty != null && p != null && p > 0 ? qty! * p : 0;
  }
}

String _koboInput(int kobo) => kobo % 100 == 0 ? '${kobo ~/ 100}' : '${kobo ~/ 100}.${(kobo % 100).toString().padLeft(2, '0')}';

/// A new invoice (InvoiceNew) or a draft changed (InvoiceEdit): the
/// customer (a linked business first), one amount or a list of items, a
/// due date and a note, the payment options; saved, or saved and sent.
class InvoiceEditorPage extends StatefulWidget {
  const InvoiceEditorPage({super.key, required this.auth, this.existing});
  final Auth auth;

  /// A draft to change; null for a new invoice.
  final Map<String, dynamic>? existing;

  @override
  State<InvoiceEditorPage> createState() => _InvoiceEditorPageState();
}

class _InvoiceEditorPageState extends State<InvoiceEditorPage> {
  String? _linked;
  final _name = TextEditingController();
  final _phone = TextEditingController();
  final _email = TextEditingController();
  String _mode = 'amount';
  final _amount = TextEditingController();
  final _description = TextEditingController();
  late List<_Item> _items = [_Item(_changed)];
  final _dueOn = TextEditingController();
  final _note = TextEditingController();
  final _connection = TextEditingController();
  String _fee = 'business';
  bool _part = false;
  bool _more = false;
  List<Map<String, dynamic>> _buyers = const [];
  bool _tried = false;
  bool _busy = false;
  String? _error;
  bool _ready = false;
  bool _hidden = false;
  final _createKey = SaveKey();
  final _sendKey = formKey();
  Map<String, dynamic>? _saved;

  List<TextEditingController> get _boxes => [_name, _phone, _email, _amount, _description, _dueOn, _note, _connection];

  void _changed() => setState(() {});

  @override
  void initState() {
    super.initState();
    for (final c in _boxes) {
      c.addListener(_changed);
    }
    _start();
  }

  Future<void> _start() async {
    final t = widget.auth.transport;
    final x = widget.existing;
    if (x != null) {
      _saved = x;
      // The customer's details in full, for those who may see them.
      var inv = x;
      if (widget.auth.can('pii.reveal')) {
        try {
          final r = (await t.send('GET', invoicePath(_str(x['id'])), query: {'reveal': 'true'}) as Map).cast<String, dynamic>();
          inv = _map(r['invoice']);
        } on Object {
          _hidden = true;
        }
      } else {
        _hidden = true;
      }
      _fill(inv);
    } else {
      try {
        final s = (await t.send('GET', '/v1/invoice-settings') as Map).cast<String, dynamic>();
        _fee = s['fee_paid_by'] == 'customer' ? 'customer' : 'business';
        _part = s['allow_part_payments'] == true;
      } on Object {
        // The usual options, then.
      }
    }
    try {
      final r = (await t.send('GET', '/v1/links') as Map).cast<String, dynamic>();
      _buyers = _maps(r['links']).where((l) => l['state'] == 'active' && (l['role'] == 'customer' || l['role'] == 'both')).toList();
    } on Object {
      // No linked buyers to offer.
    }
    if (mounted) setState(() => _ready = true);
  }

  /// invoices.ts formFromInvoice: one line of quantity 1 is one amount.
  void _fill(Map<String, dynamic> x) {
    final c = _map(x['customer']);
    final lines = _maps(x['lines']);
    final single = lines.length == 1 && lines.first['quantity'] == 1;
    final key = _str(c['key']);
    if (key.startsWith('link:')) _linked = key.substring(5);
    _name.text = _hidden && !key.startsWith('link:') ? '' : _str(c['name']);
    _phone.text = _hidden ? '' : _str(c['phone']);
    _email.text = _hidden ? '' : _str(c['email']);
    _mode = single || lines.isEmpty ? 'amount' : 'items';
    if (single) {
      _amount.text = _koboInput(_int(lines.first['unit_kobo']));
      _description.text = _str(lines.first['description']);
    } else if (lines.isNotEmpty) {
      for (final it in _items) {
        it.dispose();
      }
      _items = [
        for (final l in lines) _Item(_changed, description: _str(l['description']), quantity: '${l['quantity'] ?? 1}', price: _koboInput(_int(l['unit_kobo']))),
      ];
    }
    _dueOn.text = _str(x['due_on']).length >= 10 ? _str(x['due_on']).substring(0, 10) : '';
    _note.text = _str(x['note']);
    _connection.text = _str(x['connection']);
    _fee = x['fee_paid_by'] == 'customer' ? 'customer' : 'business';
    _part = x['allow_part_payments'] == true;
  }

  @override
  void dispose() {
    for (final c in _boxes) {
      c.dispose();
    }
    for (final it in _items) {
      it.dispose();
    }
    super.dispose();
  }

  int get _total => _mode == 'amount' ? (nairaKobo(_amount.text) ?? 0).clamp(0, 1 << 52) : _items.fold(0, (a, i) => a + i.total);

  /// invoices.ts invoiceProblem.
  String? get _problem {
    if (_name.text.trim().isEmpty) return 'Give the customer’s name.';
    if (_name.text.trim().length > 120) return 'The customer’s name is at most 120 characters.';
    if (_linked == null && _phone.text.trim().isEmpty && _email.text.trim().isEmpty) {
      return 'Give the customer’s phone number or email, or both, so the invoice can be sent.';
    }
    if (_email.text.trim().isNotEmpty && !RegExp(r'^[^@\s]+@[^@\s]+\.[^@\s]+$').hasMatch(_email.text.trim())) return 'The email does not look right.';
    if (_mode == 'amount') {
      if (_amount.text.trim().isEmpty) return 'Give the amount.';
      final k = nairaKobo(_amount.text);
      if (k == null || k <= 0) return 'The amount is a number of naira, such as 25000 or 25000.50.';
    } else {
      if (_items.isEmpty) return 'Add at least one item.';
      for (var n = 0; n < _items.length; n++) {
        final i = _items[n];
        final which = _items.length > 1 ? 'Item ${n + 1}' : 'The item';
        if (i.description.text.trim().isEmpty) return '$which needs a description.';
        if (i.qty == null) return '$which’s quantity is a whole number, 1 or more.';
        final p = nairaKobo(i.price.text);
        if (p == null || p <= 0) return '$which’s price is a number of naira, such as 1500 or 1500.50.';
      }
    }
    if (_dueOn.text.trim().isNotEmpty && !RegExp(r'^\d{4}-\d{2}-\d{2}$').hasMatch(_dueOn.text.trim())) return 'The due date does not look right.';
    return null;
  }

  /// invoices.ts draftRequest.
  Map<String, Object?> get _request => {
    'customer': _linked != null
        ? {'name': _name.text.trim(), 'key': 'link:$_linked'}
        : {
            'name': _name.text.trim(),
            if (_phone.text.trim().isNotEmpty) 'phone': _phone.text.trim(),
            if (_email.text.trim().isNotEmpty) 'email': _email.text.trim(),
          },
    if (_mode == 'amount') ...{
      'amount_kobo': nairaKobo(_amount.text) ?? 0,
      if (_description.text.trim().isNotEmpty) 'description': _description.text.trim(),
    } else
      'lines': [
        for (final i in _items) {'description': i.description.text.trim(), 'quantity': i.qty, 'unit_kobo': nairaKobo(i.price.text) ?? 0},
      ],
    if (_dueOn.text.trim().isNotEmpty) 'due_on': _dueOn.text.trim(),
    if (_note.text.trim().isNotEmpty) 'note': _note.text.trim(),
    if (_connection.text.trim().isNotEmpty) 'connection': _connection.text.trim(),
    'fee_paid_by': _fee,
    'allow_part_payments': _part,
  };

  Future<void> _save(bool send) async {
    setState(() => _tried = true);
    if (_problem != null) return;
    final t = widget.auth.transport;
    final body = _request;
    setState(() => (_busy = true, _error = null));
    try {
      final saved = _saved;
      // A draft this form saved already (a send that then failed): later
      // saves change it, not make another.
      final x = saved != null
          ? (await t.send('PUT', invoicePath(_str(saved['id'])), body: body) as Map).cast<String, dynamic>()
          : (await t.send('POST', '/v1/invoices', body: body, headers: {'Idempotency-Key': _createKey.of(body)}) as Map).cast<String, dynamic>();
      _saved = x;
      Map<String, dynamic>? sent;
      if (send) {
        final r = await t.send('POST', invoicePath(_str(x['id']), '/send'), body: const {}, headers: {'Idempotency-Key': _sendKey});
        sent = (r as Map).cast<String, dynamic>();
      }
      if (!mounted) return;
      showTkToast(context, send ? 'Invoice sent' : 'Draft saved');
      Navigator.of(context).pushReplacement(
        PageRouteBuilder<void>(
          pageBuilder: (_, _, _) => InvoicePage(auth: widget.auth, id: _str(x['number']), sent: sent, backLabel: 'Back to Invoices'),
        ),
      );
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final x = widget.existing;
    final manage = widget.auth.can('invoice.manage');
    return TkPageScreen(
      title: x == null ? 'New invoice' : 'Change ${x['number']}',
      backLabel: 'Back',
      children: [
        if (!manage)
          Text('You can see invoices, but not make or change them. Someone who manages invoices can.', style: tkHint(t))
        else if (x != null && x['status'] != 'draft')
          _Notice([_text(t, 'Only a draft can be changed. This invoice has been sent: to correct it, void it with the reason and send a new one.')])
        else if (!_ready)
          Text('Loading…', style: tkHint(t))
        else ...[
          if (_hidden) Text('The customer’s details are hidden from you. Enter them again to save your changes.', style: tkHint(t)),
          TkPageCard(
            title: 'Customer',
            children: [
              if (_buyers.isNotEmpty) ...[
                TkKitField(
                  field: false,
                  label: 'Who is it for?',
                  hint: _linked != null ? 'A linked business: the invoice reaches it as a bill in its business.' : null,
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: TkSmallButton(
                      label: _linked == null
                          ? 'Someone else'
                          : '${_buyers.where((b) => b['business'] == _linked).map((b) => _str(b['name'])).firstOrNull ?? _name.text} (linked)',
                      semantics: 'Who is it for: ${_linked == null ? 'Someone else' : _name.text}',
                      onPressed: () async {
                        final pick = await showTkModalChoices(
                          context,
                          title: 'Who is it for?',
                          value: _linked ?? '',
                          choices: [('', 'Someone else'), for (final b in _buyers) (_str(b['business']), '${b['name']} (linked)')],
                        );
                        if (pick == null) return;
                        final b = _buyers.where((x) => x['business'] == pick).firstOrNull;
                        setState(() {
                          _linked = b == null ? null : pick;
                          _name.text = b == null ? '' : _str(b['name']);
                          if (b != null) {
                            _phone.clear();
                            _email.clear();
                          }
                        });
                      },
                    ),
                  ),
                ),
                const SizedBox(height: 10),
              ],
              if (_linked == null) ...[
                TkKitField(
                  label: 'Name',
                  child: TkTextBox(controller: _name, max: 120),
                ),
                const SizedBox(height: 10),
                TkKitField(
                  label: 'Phone number',
                  hint: 'For the SMS.',
                  child: TkTextBox(controller: _phone, phone: true),
                ),
                const SizedBox(height: 10),
                TkKitField(
                  label: 'Email',
                  hint: 'For the email.',
                  child: TkTextBox(controller: _email, plain: true),
                ),
                const SizedBox(height: 6),
                Text('Give a phone number or an email, or both.', style: tkHint(t)),
              ],
            ],
          ),
          TkPageCard(
            title: 'What it is for',
            children: [
              for (final (v, words) in const [('amount', 'One amount'), ('items', 'A list of items')])
                TkChoiceRow(
                  control: TkRadio(checked: _mode == v),
                  label: Text(words, style: tkText(size: 15, color: t.text)),
                  onTap: () => setState(() => _mode = v),
                ),
              if (_mode == 'amount') ...[
                TkKitField(
                  label: 'Amount (₦)',
                  child: TkTextBox(controller: _amount, money: true, hint: '25,000'),
                ),
                const SizedBox(height: 10),
                TkKitField(
                  label: 'Description',
                  hint: 'What it is for, such as Catering for 12 October. Optional.',
                  child: TkTextBox(controller: _description, max: 200),
                ),
              ] else ...[
                for (var i = 0; i < _items.length; i++)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 14),
                    child: Semantics(
                      container: true,
                      label: 'Item ${i + 1}',
                      explicitChildNodes: true,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          TkKitField(
                            label: 'Description',
                            child: TkTextBox(controller: _items[i].description, max: 200),
                          ),
                          const SizedBox(height: 8),
                          Wrap(
                            spacing: 10,
                            runSpacing: 8,
                            crossAxisAlignment: WrapCrossAlignment.end,
                            children: [
                              SizedBox(
                                width: 100,
                                child: TkKitField(
                                  label: 'Quantity',
                                  child: TkTextBox(controller: _items[i].quantity, digits: 6),
                                ),
                              ),
                              SizedBox(
                                width: 150,
                                child: TkKitField(
                                  label: 'Price each (₦)',
                                  child: TkTextBox(controller: _items[i].price, money: true, hint: '1,500'),
                                ),
                              ),
                              Padding(padding: const EdgeInsets.only(bottom: 14), child: _text(t, naira(_items[i].total))),
                            ],
                          ),
                          if (_items.length > 1)
                            Align(
                              alignment: Alignment.centerLeft,
                              child: TkSmallButton(
                                label: 'Remove',
                                danger: true,
                                semantics: 'Remove item ${i + 1}',
                                onPressed: () {
                                  final gone = _items[i];
                                  setState(() => _items.removeAt(i));
                                  WidgetsBinding.instance.addPostFrameCallback((_) => gone.dispose());
                                },
                              ),
                            ),
                        ],
                      ),
                    ),
                  ),
                if (_items.length < 50)
                  Align(
                    alignment: Alignment.centerLeft,
                    child: TkSmallButton(label: 'Add an item', onPressed: () => setState(() => _items.add(_Item(_changed)))),
                  ),
              ],
              const SizedBox(height: 10),
              Semantics(
                liveRegion: true,
                child: Text('Total ${naira(_total)}', style: tkText(size: 16, weight: 700, color: t.text)),
              ),
            ],
          ),
          TkPageCard(
            title: 'When and a note',
            children: [
              TkKitField(
                label: 'Due date',
                hint: 'Optional, as 2026-12-01. It is overdue the day after.',
                child: TkTextBox(controller: _dueOn, hint: '2026-12-01'),
              ),
              const SizedBox(height: 10),
              TkKitField(
                label: 'Note for the customer',
                hint: 'Optional.',
                child: TkTextBox(controller: _note, lines: 2, max: 500),
              ),
              const SizedBox(height: 10),
              Align(
                alignment: Alignment.centerLeft,
                child: TkLink(label: _more ? 'Fewer payment options' : 'More payment options', onTap: () => setState(() => _more = !_more)),
              ),
              if (_more) ...[
                const SizedBox(height: 10),
                TkKitField(
                  label: 'Payments connection',
                  hint: 'The connection that takes payments for this invoice. Leave it empty for your usual one.',
                  child: TkTextBox(controller: _connection, hint: 'main', plain: true),
                ),
                const SizedBox(height: 10),
                Text('Who pays the transfer fee', style: tkText(size: 13, weight: 600, color: t.muted)),
                const SizedBox(height: 6),
                for (final (v, words, help) in feeChoices)
                  TkChoiceRow(
                    control: TkRadio(checked: _fee == v),
                    label: Text('$words. $help', style: tkText(size: 14, color: t.text)),
                    onTap: () => setState(() => _fee = v),
                  ),
                TkSwitchRow(label: 'The customer may pay part of it', on: _part, onChanged: (v) => setState(() => _part = v)),
              ],
            ],
          ),
          if (_tried && _problem != null)
            Semantics(
              liveRegion: true,
              child: Text(_problem!, style: tkText(size: 13, weight: 600, color: t.danger)),
            ),
          if (_error != null) TkErrorBox(_error!),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              TkButton(label: 'Save draft', onPressed: _busy ? null : () => _save(false)),
              TkButton(label: 'Save and send', primary: true, onPressed: _busy ? null : () => _save(true)),
            ],
          ),
          Text(
            _linked != null
                ? 'Sending puts it in ${_name.text}’s bills on Taskiem. Once sent, it cannot be changed.'
                : 'Sending sends it to the customer by SMS and email, with a link to its page. Once sent, it cannot be changed.',
            style: tkHint(t),
          ),
        ],
      ],
    );
  }
}

/// invoices.ts FEE_CHOICES.
const feeChoices = [
  ('business', 'The business', 'The customer pays the invoice’s amount; the fee comes out of what you receive.'),
  ('customer', 'The customer', 'The fee is added to what the customer pays, where the payments provider allows it.'),
];

// --- the settings ---

/// invoices.ts numberPreview: the next number, as INV-0001.
String numberPreview(String prefix, int? next) => '${prefix.trim()}${(next != null && next >= 1 ? next : 1).toString().padLeft(4, '0')}';

/// invoices.ts creditTermsWords.
String creditTermsWords(Map<String, dynamic> s) {
  if (_str(s['credit_terms_at']).isEmpty) return 'Not chosen yet: notebook debts have no due date until you choose.';
  if (s['credit_days'] == null) return 'No fixed time: notebook debts are never overdue or reminded.';
  final d = _int(s['credit_days']);
  return '$d ${d == 1 ? 'day' : 'days'} from the day a debt is recorded.';
}

/// Invoice settings: numbering, the usual payment options, matching, and
/// the credit terms for notebook debts.
class InvoiceSettingsPage extends StatefulWidget {
  const InvoiceSettingsPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<InvoiceSettingsPage> createState() => _InvoiceSettingsPageState();
}

class _InvoiceSettingsPageState extends State<InvoiceSettingsPage> {
  Map<String, dynamic>? _saved;
  String? _error;
  final _prefix = TextEditingController();
  final _next = TextEditingController();
  final _connection = TextEditingController();
  String _fee = 'business';
  bool _part = false;
  bool _auto = false;
  bool _tried = false;
  bool _busy = false;
  String? _saveError;
  // Credit terms.
  String _choice = '14';
  final _other = TextEditingController();
  bool _applyOpen = false;
  String _said = '';
  String? _creditError;

  List<TextEditingController> get _boxes => [_prefix, _next, _connection, _other];

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

  void _fill(Map<String, dynamic> s) {
    _saved = s;
    _prefix.text = _str(s['prefix']);
    _next.text = '${_int(s['next_number'])}';
    _connection.text = _str(s['connection']);
    _fee = s['fee_paid_by'] == 'customer' ? 'customer' : 'business';
    _part = s['allow_part_payments'] == true;
    _auto = s['auto_match'] == true;
    // invoices.ts creditChoice.
    if (_str(s['credit_terms_at']).isEmpty) {
      _choice = '14';
    } else if (s['credit_days'] == null) {
      _choice = 'none';
    } else if (const ['7', '14', '30'].contains('${_int(s['credit_days'])}')) {
      _choice = '${_int(s['credit_days'])}';
    } else {
      _choice = 'other';
      _other.text = '${_int(s['credit_days'])}';
    }
  }

  Future<void> _load() async {
    try {
      final s = (await widget.auth.transport.send('GET', '/v1/invoice-settings') as Map).cast<String, dynamic>();
      if (mounted) {
        setState(() {
          _fill(s);
          _error = null;
        });
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  /// invoices.ts settingsProblem.
  String? get _problem {
    final prefix = _prefix.text.trim();
    if (prefix.length > 12) return 'The prefix is at most 12 characters.';
    if (prefix.isNotEmpty && !RegExp(r'^[A-Za-z0-9_/-]+$').hasMatch(prefix)) return 'The prefix uses letters, digits, -, _ or / only.';
    final n = int.tryParse(_next.text.trim());
    if (!RegExp(r'^\d+$').hasMatch(_next.text.trim()) || n == null || n < 1) return 'The next number is a whole number, 1 or more.';
    final was = _int(_saved?['next_number']);
    if (n < was) return 'The next number can only move forward, so no number is given twice. It is $was or more.';
    return null;
  }

  String? get _creditProblem {
    if (_choice != 'other') return null;
    final n = int.tryParse(_other.text.trim());
    return n != null && n >= 1 && n <= 365 ? null : 'Type a number of days from 1 to 365.';
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final s = _saved;
    final manage = widget.auth.can('invoice.manage');
    return TkPageScreen(
      title: 'Invoice settings',
      description: 'How new invoices are numbered, their usual payment options, and how money is matched to them. Each invoice can change the payment options for itself.',
      backLabel: 'Back to Invoices',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (s == null && _error == null) Text('Loading the settings…', style: tkHint(t)),
        if (s != null) ...[
          TkPageCard(
            children: [
              if (!manage)
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: Text('You can see these settings. Someone who manages invoices can change them.', style: tkHint(t)),
                ),
              IgnorePointer(
                ignoring: !manage,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    TkKitField(
                      label: 'Number prefix',
                      hint: 'Up to 12 letters, digits, -, _ or /.',
                      child: TkTextBox(controller: _prefix, max: 12, plain: true, enabled: manage),
                    ),
                    const SizedBox(height: 10),
                    TkKitField(
                      label: 'Next number',
                      hint:
                          'Can move forward, never back, so no number is given twice. The next invoice is ${numberPreview(_prefix.text, int.tryParse(_next.text.trim()))}.',
                      child: TkTextBox(controller: _next, digits: 9, enabled: manage),
                    ),
                    const SizedBox(height: 10),
                    TkKitField(
                      label: 'Payments connection',
                      hint: 'The connection (Paystack, Flutterwave, OPay, …) that takes payments for invoices.',
                      child: TkTextBox(controller: _connection, hint: 'main', plain: true, enabled: manage),
                    ),
                    const SizedBox(height: 10),
                    Text('Who pays the transfer fee', style: tkText(size: 13, weight: 600, color: t.muted)),
                    const SizedBox(height: 6),
                    for (final (v, words, help) in feeChoices)
                      TkChoiceRow(
                        control: TkRadio(checked: _fee == v),
                        label: Text('$words. $help', style: tkText(size: 14, color: t.text)),
                        onTap: manage ? () => setState(() => _fee = v) : null,
                      ),
                    TkSwitchRow(label: 'Customers may pay part of an invoice', on: _part, onChanged: manage ? (v) => setState(() => _part = v) : null),
                    TkSwitchRow(
                      label: 'Match strong suggestions automatically',
                      hint: 'Off by default. A payment that clearly fits one invoice is matched without asking; each automatic match lists its reasons on the invoice and can be undone there.',
                      on: _auto,
                      onChanged: manage ? (v) => setState(() => _auto = v) : null,
                    ),
                  ],
                ),
              ),
              if (_tried && _problem != null)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(_problem!, style: tkText(size: 13, weight: 600, color: t.danger)),
                ),
              if (_saveError != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_saveError!)),
              if (manage)
                Padding(
                  padding: const EdgeInsets.only(top: 10),
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: TkSmallButton(
                      label: 'Save settings',
                      primary: true,
                      onPressed: _busy
                          ? null
                          : () async {
                              setState(() => _tried = true);
                              if (_problem != null) return;
                              setState(() => (_busy = true, _saveError = null));
                              try {
                                final out = (await widget.auth.transport.send(
                                  'PUT',
                                  '/v1/invoice-settings',
                                  body: {
                                    'prefix': _prefix.text.trim(),
                                    'next_number': int.parse(_next.text.trim()),
                                    'connection': _connection.text.trim().isEmpty ? 'main' : _connection.text.trim(),
                                    'fee_paid_by': _fee,
                                    'allow_part_payments': _part,
                                    'auto_match': _auto,
                                  },
                                ) as Map).cast<String, dynamic>();
                                if (mounted) {
                                  showTkToast(this.context, 'Settings saved');
                                  setState(() {
                                    _fill(out);
                                    _tried = false;
                                  });
                                }
                              } on Object catch (e) {
                                if (mounted) setState(() => _saveError = errorWords(e));
                              }
                              if (mounted) setState(() => _busy = false);
                            },
                    ),
                  ),
                ),
            ],
          ),
          TkPageCard(
            title: 'Credit terms',
            children: [
              Text(
                'How long customers have to pay a debt you write down in chat (Ada owes 5,000). A debt is due that many days after it is recorded, and reminders go only once it is overdue. Now: ${creditTermsWords(s)}',
                style: tkHint(t),
              ),
              const SizedBox(height: 10),
              Text('Customers have', style: tkText(size: 13, weight: 600, color: t.muted)),
              const SizedBox(height: 6),
              for (final (v, words) in const [
                ('7', '7 days'),
                ('14', '14 days'),
                ('30', '30 days'),
                ('none', 'No fixed time (no reminders)'),
                ('other', 'Another number of days'),
              ])
                TkChoiceRow(
                  control: TkRadio(checked: _choice == v),
                  label: Text(words, style: tkText(size: 15, color: t.text)),
                  onTap: manage ? () => setState(() => _choice = v) : null,
                ),
              if (_choice == 'other') ...[
                TkKitField(
                  label: 'Days',
                  child: TkTextBox(controller: _other, digits: 3, enabled: manage),
                ),
                const SizedBox(height: 10),
              ],
              TkSwitchRow(
                label: 'Also date the open debts by these terms',
                hint: 'Each from the day it was recorded. Otherwise only new debts follow them.',
                on: _applyOpen,
                onChanged: manage ? (v) => setState(() => _applyOpen = v) : null,
              ),
              if (_creditProblem != null && _choice == 'other' && _other.text.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(_creditProblem!, style: tkText(size: 13, weight: 600, color: t.danger)),
                ),
              if (_said.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: _Notice([_text(t, _said)])),
              if (_creditError != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_creditError!)),
              if (manage)
                Padding(
                  padding: const EdgeInsets.only(top: 10),
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: TkSmallButton(
                      label: 'Save credit terms',
                      primary: true,
                      onPressed: _busy || _creditProblem != null
                          ? null
                          : () async {
                              setState(() => (_busy = true, _creditError = null));
                              try {
                                final r = (await widget.auth.transport.send(
                                  'PUT',
                                  '/v1/invoice-settings/credit-terms',
                                  body: {
                                    'credit_days': _choice == 'none' ? null : (_choice == 'other' ? int.parse(_other.text.trim()) : int.parse(_choice)),
                                    'apply_open': _applyOpen,
                                  },
                                ) as Map).cast<String, dynamic>();
                                final applied = _int(r['applied']);
                                if (mounted) {
                                  showTkToast(this.context, 'Credit terms saved');
                                  setState(() {
                                    _said = _applyOpen ? '$applied open ${applied == 1 ? 'debt' : 'debts'} now follow these terms.' : '';
                                    _applyOpen = false;
                                    _fill({...?_saved, ..._map(r['settings'])});
                                  });
                                }
                              } on Object catch (e) {
                                if (mounted) setState(() => _creditError = errorWords(e));
                              }
                              if (mounted) setState(() => _busy = false);
                            },
                    ),
                  ),
                ),
            ],
          ),
        ],
      ],
    );
  }
}
