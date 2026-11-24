// Invoices (web Invoices.tsx, lib/invoices.ts): what customers are asked to
// pay, newest first; one invoice with what is due, its lines, amendments,
// details, customer and payments (each undone after yes), sent or voided,
// its page shared from the phone; a new invoice and a draft changed; the
// invoice settings and credit terms. Each invoice is marked paid only by
// the payments counted towards it.

import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:share_plus/share_plus.dart';

import '../app/lock.dart' show stepOut;
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show TkSwitch, TkSwitchRow, errorWords, nairaKobo;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkPill;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField;
import '../kit/look.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart' show handOffFile;
import 'doc_thread.dart' show DocThread;
import 'invoice_match.dart' show CustomerMatching, InvoiceMatchPage, groupSuggestions, suggestionsPath;
import 'money_sheets.dart' show InvoiceActions, dateLabel, invoiceActions, invoicePath, invoiceStatus, owing;
import 'product_form.dart' show SaveKey, formKey;
import 'saved_fill.dart' show SavedButton, pickSaved, pickSavedDate;
import 'storefront_orders_page.dart' show isMasked;
import 'words.dart' show CardTone, naira;
import '../kit/mobile.dart';
import '../kit/icon.dart';
import '../kit/tap_area.dart';
import 'order_sheet.dart' show QtyStepper;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';
Map<String, dynamic> _map(Object? v) => (v as Map?)?.cast<String, dynamic>() ?? const {};

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Opens one invoice (its id or number) on its own screen.
Future<void> openInvoice(BuildContext context, Auth auth, String id) => _push(context, InvoicePage(auth: auth, id: id));

/// Opens Invoices.
Future<void> openInvoices(BuildContext context, Auth auth) => _push(context, InvoicesPage(auth: auth));

/// invoices.ts INVOICE_FILTERS: "" is all; credit, the notebook debts. In
/// the app those owed come first (docs/spec/mobile-app-look.md).
const invoiceFilters = [
  ('', 'All'),
  ('sent', 'Sent'),
  ('part_paid', 'Part-paid'),
  ('overdue', 'Overdue'),
  ('paid', 'Paid'),
  ('draft', 'Draft'),
  ('recorded', 'Recorded'),
  ('credit', 'Notebook debts'),
  ('void', 'Void'),
];

const _mon = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// A date short, as a phone says it: "12 Nov", with the year only when it
/// is not this one.
String shortDay(String ymd, [DateTime? now]) {
  final d = DateTime.tryParse('${ymd.length >= 10 ? ymd.substring(0, 10) : ymd}T00:00:00Z');
  if (d == null) return '';
  final year = (now ?? DateTime.now()).toUtc().add(const Duration(hours: 1)).year;
  return '${d.day} ${_mon[d.month - 1]}${d.year == year ? '' : ' ${d.year}'}';
}

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
  if ((l['amendment'] == true || _str(l['amendment']).isNotEmpty) && _str(l['kind']).isEmpty) 'Added after the invoice was sent.',
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

/// An invoice's status in the accents of the pages away from the chat:
/// overdue red, part-paid orange, sent blue, paid lime; draft and void
/// grey (null).
({Color fill, Color ink})? invoiceAccent(TkLook look, String status) => switch (status) {
  'overdue' => (fill: look.red, ink: look.redInk),
  'part_paid' => (fill: look.orange, ink: look.orangeInk),
  'sent' || 'recorded' => (fill: look.blue, ink: look.blueInk),
  'paid' => (fill: look.lime, ink: look.limeInk),
  _ => null,
};

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
  const InvoicesPage({super.key, required this.auth, this.backLabel = 'Back'});
  final Auth auth;

  /// "Back to Money": the head's link says where it goes back to.
  final String backLabel;

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
    await Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => InvoiceEditorPage(auth: widget.auth)));
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final list = _list;
    final manage = widget.auth.can('invoice.manage');
    final all = _status.isEmpty;
    // What customers still owe, across the list, overdue or not yet due.
    final owed = list == null ? const <Map<String, dynamic>>[] : list.where(owing).toList();
    final overdue = owed.where((x) => x['status'] == 'overdue').fold(0, (n, x) => n + _int(x['due_kobo']));
    final due = owed.fold(0, (n, x) => n + _int(x['due_kobo']));
    Widget row(Map<String, dynamic> x) {
      final c = _map(x['customer']);
      final name = _str(c['name']).isNotEmpty ? _str(c['name']) : (_str(c['phone']).isNotEmpty ? _str(c['phone']) : _str(c['email']));
      final st = invoiceStatus(_str(x['status']));
      final part = x['status'] != 'void' && x['status'] != 'paid' && _int(x['due_kobo']) != _int(x['total_kobo']);
      return TkListRow(
        title: name.isEmpty ? '—' : name,
        letters: name.isEmpty ? '?' : name,
        sub: [
          '${x['number']}',
          if (part) '${naira(_int(x['due_kobo']))} still due' else if (_str(x['due_on']).isNotEmpty) 'due ${shortDay(_str(x['due_on']))}',
        ].join(' · '),
        amount: naira(_int(x['total_kobo'])),
        status: st.label,
        statusTone: _tone(_str(x['status'])),
        statusAccent: invoiceAccent(look, _str(x['status'])),
        waiting: x['status'] == 'overdue',
        onTap: () async {
          await Navigator.of(context).push(
            TkRoute<void>(pageBuilder: (_, _, _) => InvoicePage(auth: widget.auth, id: _str(x['number']), backLabel: 'Back to Invoices')),
          );
          await _load();
        },
      );
    }

    // On All, the invoices by what they wait for; a filter, as they come.
    final groups = !all || list == null
        ? [('', list ?? const <Map<String, dynamic>>[])]
        : [
            ('Overdue', list.where((x) => x['status'] == 'overdue').toList()),
            ('Waiting for payment', list.where((x) => const {'sent', 'part_paid', 'recorded'}.contains(x['status'])).toList()),
            ('Drafts', list.where((x) => x['status'] == 'draft').toList()),
            ('Paid', list.where((x) => x['status'] == 'paid').toList()),
            ('Void', list.where((x) => x['status'] == 'void').toList()),
            ('Other', list.where((x) => !const {'overdue', 'sent', 'part_paid', 'recorded', 'draft', 'paid', 'void'}.contains(x['status'])).toList()),
          ];
    return TkPageScreen(
      title: 'Invoices',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'What you ask your customers to pay, newest first. Each one is marked paid only by the payments counted towards it.',
      onRefresh: _load,
      action: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          TkHeadButton(icon: 'settings', label: 'Invoice settings', onTap: () => _push(context, InvoiceSettingsPage(auth: widget.auth))),
          if (manage) ...[const SizedBox(width: 8), TkHeadButton(icon: 'b-plus', label: 'New invoice', primary: true, onTap: _new)],
        ],
      ),
      under: TkChips<String>(
        label: 'Show',
        chips: invoiceFilters,
        on: _status,
        onPick: (pick) async {
          if (pick == _status) return;
          setState(() => (_status = pick, _list = null));
          await _load();
        },
      ),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (list == null && _error == null) const TkSkeleton(rows: 4, label: 'Loading invoices…'),
        if (list != null && all && (owed.isNotEmpty || _waiting > 0))
          TkTopCard(
            semanticsLabel: 'Still due: ${naira(due)}',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Still due', color: look.orangeInk, trailing: owed.length == 1 ? '1 invoice' : '${owed.length} invoices'),
                const SizedBox(height: 6),
                FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(due, style: tkFigure(context, size: 40))),
                if (due > 0) ...[
                  const SizedBox(height: 12),
                  TkSplitBar(
                    parts: [
                      (label: 'Not due yet ${naira(due - overdue)}', share: due == 0 ? 0 : (100 * (due - overdue) / due).round(), color: look.teal),
                      (label: 'Overdue ${naira(overdue)}', share: due == 0 ? 0 : (100 * overdue / due).round(), color: look.red),
                    ],
                    percent: false,
                  ),
                ],
                if (_waiting > 0) ...[
                  const SizedBox(height: 14),
                  TkCallout(
                    leading: TkSquare('b-cash', color: look.orange),
                    title: _waiting == 1 ? '1 payment to match' : '$_waiting payments to match',
                    sub: 'Money in with no invoice yet',
                    color: look.orange,
                    button: 'Match',
                    semantics: 'To match: $_waiting ${_waiting == 1 ? 'payment waiting' : 'payments waiting'}',
                    onPressed: () async {
                      await Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => InvoiceMatchPage(auth: widget.auth)));
                      await _load();
                    },
                  ),
                ],
              ],
            ),
          ),
        if (list != null && list.isEmpty)
          TkEmpty(
            icon: 'invoices',
            title: _status.isNotEmpty ? 'No invoices with this status.' : 'No invoices yet',
            line: _status.isNotEmpty ? null : 'Make one, send it to your customer by SMS and email, and see it marked paid as the money comes in.',
            action: _status.isEmpty && manage ? TkSmallButton(label: 'New invoice', primary: true, onPressed: _new) : null,
          ),
        for (final (title, rows) in groups)
          if (rows.isNotEmpty)
            TkSection(
              title: title.isEmpty ? null : title,
              count: title == 'Overdue' ? rows.length : null,
              children: [for (final x in rows) row(x)],
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
  String? _voidError;
  Map<String, Map<String, dynamic>> _receipts = const {};
  String? _undoError;
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
      // A school's receipts for the payments counted (docs/schools.md#receipts).
      final x = _map(r['invoice']);
      if (_auth.me?.schools == true && x['status'] != 'draft') {
        try {
          final rc = (await _auth.transport.send('GET', '/v1/schools/receipts', query: {'invoice': _str(x['id'])}) as Map).cast<String, dynamic>();
          if (mounted && asked == _asked) setState(() => _receipts = {for (final rr in _maps(rc['receipts'])) _str(rr['part_id']): rr});
        } on Object {
          // The payments show without their receipts.
        }
      }
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _error = errorWords(e));
    }
  }

  /// A receipt's PDF, handed to the phone to open or keep.
  Future<void> _receiptPdf(Map<String, dynamic> rc) async {
    try {
      final bytes = await _auth.transport.bytes('/v1/schools/receipts/${Uri.encodeComponent(_str(rc['id']))}/pdf');
      await handOffFile(Uint8List.fromList(bytes), 'receipt-${rc['number']}.pdf', 'application/pdf', null);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    }
  }

  /// An action, its failure said where it was pressed ([fail]: its card),
  /// else under the buttons at the top.
  Future<void> _run(Future<void> Function() f, String done, {void Function(String? words)? fail}) async {
    final said = fail ?? (String? w) => _actError = w;
    setState(() {
      _busy = true;
      said(null);
    });
    try {
      await f();
      if (mounted && done.isNotEmpty) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => said(errorWords(e)));
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
    final may = x == null ? null : invoiceActions(_str(x['status']), _auth.can);
    final link = _str(_sent?['link']);
    return TkPageScreen(
      title: x == null ? widget.id : _str(x['number']),
      titleSize: 22,
      backLabel: widget.backLabel,
      onRefresh: _load,
      action: x == null
          ? null
          : Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (link.isNotEmpty)
                  TkHeadButton(
                    icon: 'b-share',
                    label: 'Share its page',
                    onTap: () => stepOut(() => SharePlus.instance.share(ShareParams(text: link, subject: 'Invoice ${x['number']}'))),
                  ),
                if (may!.edit || (may.voids && !_voiding)) TkHeadButton(icon: 'b-more', label: 'More about this invoice', onTap: () => _more(x, may)),
              ],
            ),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (x == null && _error == null) Text('Loading the invoice…', style: tkHint(t)),
        if (x != null) ..._invoice(t, x),
      ],
    );
  }

  Future<void> _edit(Map<String, dynamic> x) async {
    final out = await Navigator.of(context).push<({Map<String, dynamic>? sent})>(
      TkRoute(pageBuilder: (_, _, _) => InvoiceEditorPage(auth: _auth, existing: x)),
    );
    if (out?.sent != null && mounted) setState(() => _sent = out!.sent);
    await _load();
  }

  /// The head's ⋯: what is done to the invoice less often (Edit, Void).
  Future<void> _more(Map<String, dynamic> x, InvoiceActions may) async {
    final pick = await showTkModalChoices(
      context,
      title: 'Invoice ${x['number']}',
      choices: [if (may.edit) ('edit', 'Edit'), if (may.voids && !_voiding) ('void', 'Void')],
    );
    if (!mounted) return;
    if (pick == 'edit') await _edit(x);
    if (pick == 'void') setState(() => _voiding = true);
  }

  /// The top card: what is left (or paid, or to ask for) large, how much
  /// is paid as a bar, and the next thing to do.
  Widget _top(BuildContext context, Map<String, dynamic> x, ({String label, int kobo, String sub, double paid}) h, InvoiceActions may, String link) {
    final look = TkLook.of(context);
    final t = TkTheme.tokens(context);
    final status = _str(x['status']);
    final colour = switch (status) {
      'overdue' => look.redInk,
      'paid' => look.limeInk,
      'draft' || 'void' => t.muted,
      _ => look.orangeInk,
    };
    final due = shortDay(_str(x['due_on']));
    final when = due.isEmpty ? null : (status == 'overdue' ? 'was due $due' : (status == 'paid' || status == 'void' ? null : 'due $due'));
    final buttons = <Widget>[
      if (may.send) TkSmallButton(label: 'Send', primary: true, onPressed: _busy ? null : () => _send(x)),
      if (may.sendAgain) TkSmallButton(label: 'Send again', primary: true, onPressed: _busy ? null : () => _send(x)),
      if (may.edit && !may.send) TkSmallButton(label: 'Edit', onPressed: () => _edit(x)),
      if (link.isNotEmpty) TkSmallButton(label: 'Its page', onPressed: () => openOrSay(context, () => openInBrowser(Uri.parse(link)), 'Its page could not be opened.')),
    ];
    return TkTopCard(
      semanticsLabel: 'What is due',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow(h.label, color: colour, trailing: when ?? invoiceStatus(status).label),
          const SizedBox(height: 6),
          FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(h.kobo, style: tkFigure(context, size: 40))),
          Text(
            '${h.sub}${_str(_map(x['customer'])['name']).isEmpty ? '' : ' · ${_map(x['customer'])['name']}'}',
            style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
          ),
          if (status != 'draft' && status != 'void') ...[
            const SizedBox(height: 12),
            TkProgress(h.paid, color: look.lime, semanticsLabel: '${(h.paid * 100).round()}% paid'),
          ],
          if (buttons.isNotEmpty) ...[
            const SizedBox(height: 14),
            Row(
              children: [
                for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(width: 8), Expanded(child: buttons[i])],
              ],
            ),
          ],
        ],
      ),
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
      _top(context, x, h, may, link),
      // The customer: one card, the name with how to reach them, and Call.
      TkPanel(
        padding: const EdgeInsets.fromLTRB(14, 12, 10, 12),
        children: [
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  TkFace(tkInitials(_str(c['name']).isEmpty ? '?' : _str(c['name'])), size: 40, colors: tkFaceFor(_str(c['name']))),
                  const SizedBox(width: 12),
                  Expanded(
                    child: MergeSemantics(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(_str(c['name']).isEmpty ? '—' : _str(c['name']), maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 16, weight: 600, color: t.text)),
                          if (_str(c['phone']).isNotEmpty || _str(c['email']).isNotEmpty)
                            Text(
                              [_str(c['phone']), _str(c['email'])].where((x) => x.isNotEmpty).join(' · '),
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                              style: tkText(size: 12.5, color: t.muted),
                            ),
                          // Where they are, printed on the invoice (a saved
                          // person's or business's place: build plan H6).
                          if (_str(c['address']).isNotEmpty)
                            Text(
                              _str(c['address']),
                              key: const Key('invoice-customer-address'),
                              maxLines: 3,
                              overflow: TextOverflow.ellipsis,
                              style: tkText(size: 12.5, color: t.muted),
                            ),
                        ],
                      ),
                    ),
                  ),
                  if (_str(c['email']).isNotEmpty && !isMasked(_str(c['email'])))
                    TkHeadButton(
                      icon: 'mail',
                      onCard: true,
                      label: 'Email ${c['email']}',
                      onTap: () => openOrSay(context, () => mailTo(_str(c['email'])), 'Your mail app could not be opened.'),
                    ),
                  if (_str(c['phone']).isNotEmpty && !isMasked(_str(c['phone'])))
                    TkHeadButton(
                      icon: 'b-phone',
                      onCard: true,
                      label: 'Call ${c['phone']}',
                      onTap: () => openOrSay(context, () => callPhone(_str(c['phone'])), 'That number could not be called.'),
                    ),
                ],
              ),
              if (_revealed == null && may.reveal) ...[
                // As the order's card: the whole width, then the note under it.
                const SizedBox(height: 12),
                Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    TkSmallButton(
                      label: 'Show customer details',
                      icon: 'b-eye',
                      onPressed: _busy
                          ? null
                          : () => _run(() async {
                              final r = (await _auth.transport.send('GET', invoicePath(_str(x['id'])), query: {'reveal': 'true'}) as Map).cast<String, dynamic>();
                              if (mounted) setState(() => _revealed = _map(_map(r['invoice'])['customer']));
                            }, ''),
                    ),
                    const SizedBox(height: 6),
                    Text('Showing them is recorded in the audit log.', textAlign: TextAlign.center, style: tkText(size: 12, color: t.muted)),
                  ],
                ),
              ],
              if (_revealed == null && !may.reveal && (isMasked(_str(c['phone'])) || isMasked(_str(c['email']))))
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text('The customer’s details are hidden. Someone who may see personal data can show them.', style: tkHint(t)),
                ),
            ],
          ),
        ],
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
        ]),
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
                          _run(
                            () async {
                              await _auth.transport.send('POST', invoicePath(_str(x['id']), '/void'), body: {'reason': _reason.text.trim()});
                              if (mounted) setState(() => (_voiding = false, _voidTried = false, _reason.text = ''));
                            },
                            'Invoice voided',
                            fail: (w) => _voidError = w,
                          );
                        },
                ),
                TkSmallButton(label: 'Keep it', onPressed: () => setState(() => (_voiding = false, _voidTried = false, _voidError = null))),
              ],
            ),
            if (_voidError != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_voidError!)),
          ],
        ),
      // As drawn: "Ankara fabric × 2 … ₦36,000" on a card, the total large.
      TkSection(
        title: 'What it is for',
        children: [
          if (lines.isEmpty)
            Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: _text(t, 'No items.', hint: true))
          else
            TkBill(
              lines: [
                for (final l in lines)
                  (
                    name: '${_str(l['description'])}${_str(l['kind']).isEmpty && (l['quantity'] as num? ?? 1) != 1 ? ' × ${_qty(l['quantity'])}' : ''}',
                    sub: lineNote(l),
                    under: null,
                    amount: naira(((l['quantity'] as num? ?? 1) * _int(l['unit_kobo'])).round()),
                  ),
              ],
              total: naira(_int(x['total_kobo'])),
            ),
        ],
      ),
      // The payments under what it is for (as drawn).
      TkPageCard(title: 'Payments', children: [_payments(t, x, may.undo)]),
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
      if (x['status'] != 'draft') CustomerMatching(key: ValueKey('money $number'), auth: _auth, invoice: x),
    ];
  }

  Widget _payments(TkTokens t, Map<String, dynamic> x, bool canUndo) {
    final rows = paymentRows(_maps(x['payments']));
    if (rows.isEmpty) {
      return Text(
        'No payments yet. Payments are counted here as they come in from your payments connection, or when money received is matched to this invoice on To match. Nobody marks an invoice paid without a payment.',
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
                  if (_receipts[_str(p['id'])] case final rc?)
                    Wrap(
                      spacing: 8,
                      crossAxisAlignment: WrapCrossAlignment.center,
                      children: [
                        Text('Receipt ${rc['number']}${rc['voided'] == true ? ' (voided)' : ''}', style: tkHint(t)),
                        Semantics(
                          label: 'Open PDF of receipt ${rc['number']}',
                          excludeSemantics: true,
                          child: TkLink(label: 'PDF', onTap: () => _receiptPdf(rc)),
                        ),
                      ],
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
                                      : () => _run(
                                          () async {
                                            await _auth.transport.send(
                                              'POST',
                                              invoicePath(_str(x['id']), '/payments/${Uri.encodeComponent(_str(p['id']))}/undo'),
                                              body: const {},
                                            );
                                            if (mounted) setState(() => _undoing = null);
                                          },
                                          'Payment undone',
                                          fail: (w) => _undoError = w,
                                        ),
                                ),
                                TkSmallButton(label: 'Keep it', onPressed: () => setState(() => (_undoing = null, _undoError = null))),
                              ],
                            ),
                            if (_undoError != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_undoError!)),
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
          'Money that does not name this invoice is not counted towards it until you settle the query. Their reason is in your conversation with them below.',
          style: tkHint(t),
        ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        if (widget.auth.can('invoice.manage')) ...[
          const SizedBox(height: 10),
          Text('How do you settle it?', style: tkText(size: 13, weight: 600, color: t.muted)),
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

  /// What a Saved button is reading and picking from (a person, a date):
  /// one picker at a time.
  String? _picking;
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
      // A draft changed from its own screen: back there, with what Send did.
      if (widget.existing != null) {
        Navigator.of(context).pop<({Map<String, dynamic>? sent})>((sent: sent));
        return;
      }
      Navigator.of(context).pushReplacement(
        PageRouteBuilder<void>(
          pageBuilder: (_, _, _) => InvoicePage(auth: widget.auth, id: _str(x['number']), sent: sent, backLabel: 'Back to Invoices'),
        ),
      );
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  /// A linked business picked, or '' for someone else.
  void _pickBuyer(String pick) {
    final b = _buyers.where((x) => x['business'] == pick).firstOrNull;
    setState(() {
      _linked = b == null ? null : pick;
      _name.text = b == null ? (_linked == null ? _name.text : '') : _str(b['name']);
      if (b != null) {
        _phone.clear();
        _email.clear();
      }
    });
  }

  Future<void> _fromContacts() async {
    final c = await pickContact();
    if (c == null || !mounted) return;
    setState(() {
      if (c.name.isNotEmpty) _name.text = c.name;
      if (c.phone.isNotEmpty) _phone.text = c.phone;
    });
  }

  /// A Saved button's list read and picked from: one at a time.
  Future<void> _pick(String what, Future<void> Function() pick) async {
    if (_picking != null) return;
    setState(() => _picking = what);
    try {
      await pick();
    } finally {
      if (mounted) setState(() => _picking = null);
    }
  }

  /// A saved person (saved objects S2) is the whole customer: their name,
  /// phone and email, empty where they have none (not the last one's).
  Future<void> _fromSaved() => _pick('person', () async {
    final x = await pickSaved(context, widget.auth, kind: 'person', title: 'Saved people');
    if (x == null || !mounted) return;
    setState(() {
      _name.text = x.title.isNotEmpty ? x.title : x.name;
      _phone.text = (x.details.phone ?? '').trim();
      _email.text = (x.details.email ?? '').trim();
    });
  });

  /// A date saved on one of the owner's things, as the due date.
  Future<void> _dueFromSaved() => _pick('date', () async {
    final d = await pickSavedDate(context, widget.auth);
    if (d != null && mounted) setState(() => _dueOn.text = d);
  });

  Widget _whoFace(TkTokens t, String name, bool on, VoidCallback tap) {
    final look = TkLook.of(context);
    final other = name == 'Someone else';
    return TapArea(
      child: Semantics(
        button: true,
        selected: on,
        label: other ? 'Who is it for: Someone else' : 'Who is it for: $name (linked)',
        excludeSemantics: true,
        onTap: tap,
        child: GestureDetector(
          onTap: tap,
          child: SizedBox(
            width: 66,
            child: Column(
              children: [
                Container(
                  padding: const EdgeInsets.all(2.5),
                  decoration: BoxDecoration(shape: BoxShape.circle, border: Border.all(color: on ? (TkTheme.of(context).mode == TkMode.dark ? look.lime : t.brand) : const Color(0x00000000), width: 2)),
                  child: other
                      ? Container(
                          width: 44,
                          height: 44,
                          decoration: BoxDecoration(color: t.tkSoft, shape: BoxShape.circle),
                          alignment: Alignment.center,
                          child: TkIcon('b-people', size: 20, color: t.text),
                        )
                      : TkFace(tkInitials(name), size: 44, colors: tkFaceFor(name)),
                ),
                const SizedBox(height: 4),
                Text(name, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 11.5, weight: on ? 700 : 500, color: on ? t.text : t.muted)),
              ],
            ),
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final x = widget.existing;
    final manage = widget.auth.can('invoice.manage');
    return TkPageScreen(
      title: x == null ? 'New invoice' : 'Change ${x['number']}',
      titleSize: 26,
      backLabel: 'Back to Invoices',
      children: [
        if (!manage)
          Text('You can see invoices, but not make or change them. Someone who manages invoices can.', style: tkHint(t))
        else if (x != null && x['status'] != 'draft')
          _Notice([_text(t, 'Only a draft can be changed. This invoice has been sent: to correct it, void it with the reason and send a new one.')])
        else if (!_ready)
          Text('Loading…', style: tkHint(t))
        else ...[
          if (_hidden && _linked == null) Text('The customer’s details are hidden from you. Enter them again to save your changes.', style: tkHint(t)),
          // As drawn (docs/spec/mobile-app-look.md, "The remaining screens"):
          // a linked business picked by its face, or someone else, picked
          // from the phone's contacts or typed; one amount or the items, each
          // with − n +.
          // Saved in the head, as the web has it: the name's row keeps
          // its room at large text.
          TkSection(
            title: 'Customer',
            trailing: _linked == null
                ? SavedButton(semantics: 'Pick from your saved people', picking: _picking == 'person', busy: _busy || _picking != null, onPressed: _fromSaved)
                : null,
          ),
          Container(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
            decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(20)),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (_buyers.isNotEmpty) ...[
                  SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: Row(
                      children: [
                        for (final b in _buyers) _whoFace(t, _str(b['name']), _linked == _str(b['business']), () => _pickBuyer(_str(b['business']))),
                        _whoFace(t, 'Someone else', _linked == null, () => _pickBuyer('')),
                      ],
                    ),
                  ),
                  if (_linked != null)
                    Padding(
                      padding: const EdgeInsets.only(top: 8),
                      child: Text('A linked business: the invoice reaches it as a bill in its business.', style: tkHint(t)),
                    ),
                  const SizedBox(height: 10),
                ],
                if (_linked == null) ...[
                  TkKitField(
                    label: 'Name',
                    child: Row(
                      children: [
                        Expanded(child: TkTextBox(controller: _name, max: 120)),
                        const SizedBox(width: 8),
                        TkHeadButton(icon: 'b-people', label: 'Pick from your contacts', onCard: true, onTap: _fromContacts),
                      ],
                    ),
                  ),
                  const SizedBox(height: 10),
                  TkButtonRow([
                    TkKitField(label: 'Phone number', child: TkTextBox(controller: _phone, phone: true)),
                    TkKitField(label: 'Email', child: TkTextBox(controller: _email, plain: true)),
                  ]),
                  const SizedBox(height: 6),
                  Text('Give a phone number or an email, or both.', style: tkHint(t)),
                ],
              ],
            ),
          ),
          TkSection(title: 'What it is for', children: const []),
          TkChips<String>(
            label: 'What it is for',
            padding: EdgeInsets.zero,
            chips: const [('amount', 'One amount'), ('items', 'A list of items')],
            on: _mode,
            onPick: (v) => setState(() => _mode = v),
          ),
          Container(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
            decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(20)),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (_mode == 'amount') ...[
                  TkKitField(label: 'Amount (₦)', child: TkTextBox(controller: _amount, money: true, hint: '25,000')),
                  const SizedBox(height: 10),
                  TkKitField(
                    label: 'Description',
                    hint: 'What it is for, such as Catering for 12 October. Optional.',
                    child: TkTextBox(controller: _description, max: 200),
                  ),
                ] else ...[
                  for (var i = 0; i < _items.length; i++)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 12),
                      child: Semantics(
                        container: true,
                        label: 'Item ${i + 1}',
                        explicitChildNodes: true,
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            Row(
                              children: [
                                Expanded(child: TkKitField(label: 'Description', child: TkTextBox(controller: _items[i].description, max: 200))),
                                const SizedBox(width: 10),
                                Padding(padding: const EdgeInsets.only(top: 18), child: Text(naira(_items[i].total), style: tkText(size: 15, weight: 700, color: t.text).copyWith(fontFeatures: tkTabular))),
                              ],
                            ),
                            const SizedBox(height: 8),
                            Row(
                              crossAxisAlignment: CrossAxisAlignment.end,
                              children: [
                                Builder(builder: (_) {
                                  final q = int.tryParse(_items[i].quantity.text.trim()) ?? 1;
                                  return QtyStepper(
                                    name: 'item ${i + 1}',
                                    n: q,
                                    on: true,
                                    onLess: q > 1 ? () => _items[i].quantity.text = '${q - 1}' : null,
                                    onMore: () => _items[i].quantity.text = '${q + 1}',
                                  );
                                }),
                                const SizedBox(width: 10),
                                Expanded(child: TkKitField(label: 'Price each (₦)', child: TkTextBox(controller: _items[i].price, money: true, hint: '1,500'))),
                              ],
                            ),
                            if (_items.length > 1)
                              Align(
                                alignment: Alignment.centerLeft,
                                child: TkLink(
                                  label: 'Remove',
                                  onTap: () {
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
                    Align(alignment: Alignment.centerLeft, child: TkLink(label: '+ Add an item', onTap: () => setState(() => _items.add(_Item(_changed))))),
                ],
                const SizedBox(height: 10),
                Container(height: 1, color: TkLook.of(context).line),
                const SizedBox(height: 10),
                Semantics(
                  liveRegion: true,
                  label: 'Total ${naira(_total)}',
                  excludeSemantics: true,
                  child: Row(
                    children: [
                      Expanded(child: Text('Total', style: tkText(size: 15, weight: 700, color: t.text))),
                      Text(naira(_total), style: tkDisplay(size: 20, letterSpacing: -0.4, color: t.text).copyWith(fontFeatures: tkTabular)),
                    ],
                  ),
                ),
              ],
            ),
          ),
          TkPageCard(
            title: 'When and a note',
            children: [
              TkKitField(
                label: 'Due date',
                hint: 'Optional, as 2026-12-01. It is overdue the day after.',
                child: Row(
                  children: [
                    Expanded(child: TkTextBox(controller: _dueOn, hint: '2026-12-01')),
                    const SizedBox(width: 8),
                    SavedButton(semantics: 'Pick a date from your saved things', picking: _picking == 'date', busy: _busy || _picking != null, onPressed: _dueFromSaved),
                  ],
                ),
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
              TkButton(label: 'Save draft', enabled: !_busy, onPressed: () => _save(false)),
              TkButton(label: 'Save and send', primary: true, enabled: !_busy, onPressed: () => _save(true)),
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
    _fillSettings(s);
    _fillCredit(s);
  }

  /// The settings card's form, from what was saved; the other card's
  /// unsaved choices are left as they are.
  void _fillSettings(Map<String, dynamic> s) {
    _saved = {...?_saved, ...s};
    _prefix.text = _str(s['prefix']);
    _next.text = '${_int(s['next_number'])}';
    _connection.text = _str(s['connection']);
    _fee = s['fee_paid_by'] == 'customer' ? 'customer' : 'business';
    _part = s['allow_part_payments'] == true;
    _auto = s['auto_match'] == true;
  }

  /// The credit terms card's form, from what was saved.
  void _fillCredit(Map<String, dynamic> s) {
    _saved = {...?_saved, ...s};
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
    final look = TkLook.of(context);
    Widget heading(String words) => Padding(
      padding: const EdgeInsets.fromLTRB(4, 6, 4, 0),
      child: Semantics(header: true, child: Text(words, style: tkDisplay(size: 20, letterSpacing: -0.4, color: t.text))),
    );
    Widget card(List<Widget> children) => Container(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: children),
    );
    final fee = feeChoices.where((f) => f.$1 == _fee).firstOrNull;
    // As drawn (docs/spec/mobile-app-look.md, "Settings"): the next
    // invoice's number large, from the prefix and the next number; getting
    // paid; credit terms.
    return TkPageScreen(
      title: 'Invoice settings',
      titleSize: 26,
      description: 'How new invoices are numbered, their usual payment options, and how money is matched to them. Each invoice can change the payment options for itself.',
      backLabel: 'Back to Invoices',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (s == null && _error == null) Text('Loading the settings…', style: tkHint(t)),
        if (s != null) ...[
          if (!manage) Text('You can see these settings. Someone who manages invoices can change them.', style: tkHint(t)),
          IgnorePointer(
            ignoring: !manage,
            child: TkTopCard(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  TkEyebrow('Your next invoice', color: look.blueInk, trailing: 'numbered in order'),
                  const SizedBox(height: 6),
                  FittedBox(
                    fit: BoxFit.scaleDown,
                    alignment: Alignment.centerLeft,
                    child: Text(numberPreview(_prefix.text, int.tryParse(_next.text.trim())), maxLines: 1, style: tkFigure(context, size: 26)),
                  ),
                  const SizedBox(height: 10),
                  TkButtonRow([
                    TkKitField(label: 'Number prefix', child: TkTextBox(controller: _prefix, max: 12, plain: true, enabled: manage)),
                    TkKitField(label: 'Next number', child: TkTextBox(controller: _next, digits: 9, enabled: manage)),
                  ]),
                  const SizedBox(height: 6),
                  Text(
                    'Up to 12 letters, digits, -, _ or /. The number moves forward, never back, so none is given twice. The next invoice is ${numberPreview(_prefix.text, int.tryParse(_next.text.trim()))}.',
                    style: tkText(size: 12, lineHeight: 1.35, color: t.muted),
                  ),
                ],
              ),
            ),
          ),
          heading('Getting paid'),
          IgnorePointer(
            ignoring: !manage,
            child: card([
              TkKitField(
                label: 'Payments connection',
                hint: 'Paystack, Flutterwave, OPay … that takes payments for invoices.',
                child: TkTextBox(controller: _connection, hint: 'main', plain: true, enabled: manage),
              ),
              const SizedBox(height: 10),
              Text('Who pays the transfer fee', style: tkText(size: 12.5, weight: 600, color: t.muted)),
              const SizedBox(height: 6),
              TkChips<String>(
                label: 'Who pays the transfer fee',
                chips: [for (final (v, words, _) in feeChoices) (v, words)],
                on: _fee,
                onPick: (v) => setState(() => _fee = v),
                padding: EdgeInsets.zero,
              ),
              if (fee != null) Padding(padding: const EdgeInsets.only(top: 6), child: Text(fee.$3, style: tkText(size: 12, lineHeight: 1.35, color: t.muted))),
            ]),
          ),
          IgnorePointer(
            ignoring: !manage,
            child: TkSection(
              inset: 56,
              children: [
                TkListRow(
                  title: 'Customers may pay part of an invoice',
                  icon: 'b-clock',
                  square: look.lime,
                  trailing: TkSwitch(label: 'Customers may pay part of an invoice', on: _part, onChanged: manage ? (v) => setState(() => _part = v) : null),
                  onTap: manage ? () => setState(() => _part = !_part) : null,
                ),
                TkListRow(
                  title: 'Match strong suggestions automatically',
                  sub: 'Each automatic match lists its reasons on the invoice, and can be undone there.',
                  icon: 'b-bolt',
                  square: look.violet,
                  trailing: TkSwitch(label: 'Match strong suggestions automatically', on: _auto, onChanged: manage ? (v) => setState(() => _auto = v) : null),
                  onTap: manage ? () => setState(() => _auto = !_auto) : null,
                ),
              ],
            ),
          ),
          card([
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
                                    _fillSettings(out);
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
          ]),
          heading('Credit terms'),
          card([
              Text(
                'How long customers have to pay a debt you write down in chat (Ada owes 5,000). A debt is due that many days after it is recorded, and reminders go only once it is overdue. Now: ${creditTermsWords(s)}',
                style: tkHint(t),
              ),
              const SizedBox(height: 10),
              Text('Customers have', style: tkText(size: 12.5, weight: 600, color: t.muted)),
              const SizedBox(height: 6),
              IgnorePointer(
                ignoring: !manage,
                child: TkChips<String>(
                  label: 'Customers have',
                  chips: const [('7', '7 days'), ('14', '14 days'), ('30', '30 days'), ('none', 'No fixed time'), ('other', 'Other')],
                  on: _choice,
                  onPick: (v) => setState(() => _choice = v),
                  padding: EdgeInsets.zero,
                ),
              ),
              if (_choice == 'none') Padding(padding: const EdgeInsets.only(top: 6), child: Text('No reminders go.', style: tkText(size: 12, color: t.muted))),
              const SizedBox(height: 6),
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
                                    _fillCredit(_map(r['settings']));
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

/// A quantity as written: 2, or 1.5.
String _qty(Object? q) {
  final n = q as num? ?? 1;
  return n == n.roundToDouble() ? '${n.toInt()}' : '$n';
}
