// Money at the school (docs/schools.md §8, §9; web SchoolPayments.tsx,
// SchoolReceipts.tsx, Approvals.tsx): cash at the desk (a second person
// at or above the owner's amount), payments and discounts waiting or
// decided, receipts with their PDF, and deciding a request.

import 'dart:typed_data';

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../app/passkey.dart' show PasskeyCancelled;
import '../app/session.dart' show uuid4;
import '../chat/approve.dart' show TkTextBox;
import '../chat/attachments.dart' show handOffFile;
import '../chat/words.dart' show naira;
import '../kit/forms.dart' show errorWords, nairaKobo;
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading, TkSearchBox;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, TkLines, TkSheet, TkFormBody, TkFootTwo, TkGhostButton, TkCta, TkSavedTick, showTkSheet;
import '../theme/look.dart';
import '../chat/words.dart' show CardTone;
import '../theme/theme.dart';
import 'school_tab.dart' show pushSchool, said;
import 'school_words.dart';
import 'students_page.dart' show SchoolPills, StudentPage;

// --- Record a payment at the desk ---

class CashSheet extends StatefulWidget {
  const CashSheet({super.key, required this.auth, required this.student, this.balance = 0, this.now});
  final Auth auth;
  final Map<String, dynamic> student;
  final int balance;
  final DateTime Function()? now;

  @override
  State<CashSheet> createState() => _CashSheetState();
}

class _CashSheetState extends State<CashSheet> {
  final _amount = TextEditingController();
  final _note = TextEditingController();
  String _how = 'cash';
  int _daysAgo = 0;
  int? _threshold;
  bool _busy = false;
  String? _error;
  final _key = uuid4();

  @override
  void initState() {
    super.initState();
    _amount.addListener(() => setState(() {}));
    widget.auth.transport.send('GET', '/v1/settings/schools').then((r) {
      final n = mapOf(r)?['cash_threshold_naira'];
      if (mounted && n is num) setState(() => _threshold = n.toInt() * 100);
    }, onError: (Object _) {});
  }

  @override
  void dispose() {
    _amount.dispose();
    _note.dispose();
    super.dispose();
  }

  DateTime get _today => (widget.now ?? DateTime.now)();

  Future<void> _post() async {
    final k = nairaKobo(_amount.text);
    if (k == null || k <= 0) return;
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await widget.auth.transport.send(
        'POST',
        '$schoolApi/payments',
        body: {
          'student': str(widget.student['id']),
          'amount_kobo': k,
          'how': _how,
          'date': dayKey(_today.subtract(Duration(days: _daysAgo))),
          if (_note.text.trim().isNotEmpty) 'note': _note.text.trim(),
        },
        headers: {'Idempotency-Key': _key},
      ));
      if (!mounted) return;
      said(context, str(r?['code']) == 'needs_approval' ? 'Sent for a second person to approve' : 'Posted ${naira(k)}; the receipt goes to the family');
      Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final k = nairaKobo(_amount.text) ?? 0;
    final waits = _threshold != null && k >= _threshold!;
    return TkFormSheet(
      title: 'Record a payment',
      crumb: '${nameOr(widget.student)} · ${str(widget.student['admission_number'])}',
      action: _busy ? 'Posting…' : 'Post the payment',
      onAction: _busy || k <= 0 ? null : _post,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkTopCard(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Expanded(child: Text('AMOUNT', style: tkText(size: 11, weight: 700, letterSpacing: 0.6, color: t.muted))),
                  SizedBox(width: 168, child: TkChips<int>(label: 'When', padding: EdgeInsets.zero, chips: const [(0, 'Today'), (1, 'Yesterday')], on: _daysAgo, onPick: (v) => setState(() => _daysAgo = v))),
                ],
              ),
              const SizedBox(height: 6),
              TkTextBox(controller: _amount, money: true, hint: '₦ 0', size: 30, height: 56),
              if (widget.balance > 0) Text('${naira(widget.balance)} still to pay', style: tkText(size: 13, color: t.muted)),
            ],
          ),
        ),
        TkFormGroup(
          children: [
            TkKitField(label: 'How it was paid', field: false, child: SchoolPills<String>(label: 'How it was paid', choices: deskWays, on: _how, onPick: (v) => setState(() => _how = v))),
            TkKitField(label: 'Note (optional)', child: TkTextBox(controller: _note, max: 300, size: 15, height: 46)),
          ],
        ),
        if (waits)
          TkCallout(
            leading: TkSquare('b-person', color: look.orange),
            title: 'A second person approves this',
            sub: 'Cash at or above ${naira(_threshold!)} waits for an owner; then it counts and the receipt goes out.',
            color: look.orange,
          ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

// --- Payments and cash ---

String _kindWords(String k) => const {
  'cash_posting': 'Cash',
  'cash_match': 'Cash matched',
  'discount': 'Discount',
  'discount_amendment': 'Discount on a sent invoice',
  'term_invoice': 'Term invoice',
}[k] ?? k;

class PaymentsPage extends StatefulWidget {
  const PaymentsPage({super.key, required this.auth, this.status = ''});
  final Auth auth;
  final String status;

  @override
  State<PaymentsPage> createState() => _PaymentsPageState();
}

class _PaymentsPageState extends State<PaymentsPage> {
  late String _status = widget.status;
  List<Map<String, dynamic>>? _rows;
  String? _error;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = mapOf(await _auth.transport.send('GET', '$schoolApi/payments', query: {if (_status.isNotEmpty) 'status': _status, 'limit': '200', if (_auth.mayReveal) 'reveal': 'true'}));
      if (mounted) setState(() => (_rows = maps(r?['postings']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final rows = _rows;
    return TkPageScreen(
      title: 'Payments and cash',
      titleSize: 30,
      backLabel: 'Back to School',
      onRefresh: _load,
      description: 'Cash posted at the desk and discounts given; those at or above the owner’s amount wait for a second person.',
      children: [
        TkChips<String>(
          label: 'Show',
          padding: EdgeInsets.zero,
          chips: const [('', 'All'), ('pending', 'Waiting'), ('posted', 'Posted'), ('declined', 'Declined')],
          on: _status,
          onPick: (v) {
            setState(() => (_status = v, _rows = null));
            _load();
          },
        ),
        if (_error != null) TkErrorBox(_error!),
        if (rows == null && _error == null) const TkLoading(rows: 4, label: 'Loading payments…'),
        if (rows != null)
          TkSection(
            empty: _status == 'pending' ? 'Nothing waiting for a second person.' : 'No payments here yet.',
            children: [for (final p in rows) _posting(p)],
          ),
      ],
    );
  }

  Widget _posting(Map<String, dynamic> p) {
    final st = str(p['status']);
    final d = mapOf(p['details']) ?? const {};
    final who = [nameOr(p), str(p['class']), str(p['invoice_number'])].where((x) => x.isNotEmpty).join(' · ');
    final by = [
      'By ${_asker(p)} ${dayWords(str(p['requested_at']))} ${lagosClock(str(p['requested_at']))}',
      if (str(d['how']).isNotEmpty) deskWay(str(d['how'])).toLowerCase(),
      if (str(d['reason']).isNotEmpty) str(d['reason']),
      if (st == 'declined' && str(p['decision_reason']).isNotEmpty) 'declined: ${str(p['decision_reason'])}',
      if (st == 'posted' && str(p['decided_email']).isNotEmpty) 'approved by ${str(p['decided_email'])}',
    ].join(' · ');
    final canDecide = st == 'pending' && _auth.schoolApprove && p['yours'] != true;
    return TkListRow(
      title: '${_kindWords(str(p['kind']))} · ${naira(kobo(p['amount_kobo']))}',
      sub: '$who\n$by',
      letters: nameOr(p),
      status: st == 'pending' ? 'Waiting' : (st == 'posted' ? 'Posted' : 'Declined'),
      statusTone: st == 'pending' ? CardTone.warn : (st == 'posted' ? CardTone.lime : CardTone.danger),
      onTap: canDecide
          ? () async {
              final done = await showTkSheet<bool>(context, (_) => SchoolApproveSheet(auth: _auth, request: p));
              if (done == true) await _load();
            }
          : (str(p['student_id']).isEmpty ? null : () => pushSchool<void>(context, StudentPage(auth: _auth, id: str(p['student_id'])))),
    );
  }
}

// --- Deciding a request ---

class SchoolApproveSheet extends StatefulWidget {
  const SchoolApproveSheet({super.key, required this.auth, required this.request, this.declining = false});
  final Auth auth;
  final Map<String, dynamic> request;

  /// Opened by Decline (a chat card's): the reason asked for at once.
  final bool declining;

  @override
  State<SchoolApproveSheet> createState() => _SchoolApproveSheetState();
}

class _SchoolApproveSheetState extends State<SchoolApproveSheet> {
  final _reason = TextEditingController();
  late bool _declining = widget.declining;
  bool _busy = false;
  bool _saved = false;
  String? _error;

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

  Future<void> _decide(String decision) async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('POST', '$schoolApi/approvals/${str(widget.request['id'])}', body: {
        'decision': decision,
        if (decision == 'declined') 'reason': _reason.text.trim(),
      });
      setState(() => _saved = true);
      await Future<void>.delayed(const Duration(milliseconds: 900));
      if (mounted) Navigator.of(context).pop(true);
    } on PasskeyCancelled {
      if (mounted) setState(() => _busy = false);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final p = widget.request;
    final d = mapOf(p['details']) ?? const {};
    final kind = str(p['kind']);
    final rows = <(String, String)>[
      ('Amount', naira(kobo(p['amount_kobo']))),
      ('Student', [str(p['admission_number']), str(p['class'])].where((x) => x.isNotEmpty).join(' · ')),
      if (str(d['how']).isNotEmpty) ('How', '${deskWay(str(d['how']))}${str(d['date']).isEmpty ? '' : ', ${dayWords(str(d['date']))}'}'),
      if (str(d['kind']).isNotEmpty) ('Discount', '${str(d['kind']).replaceAll('_', ' ')}${d['percent'] != null ? ' ${kobo(d['percent'])}%' : ''}'),
      if (str(d['reason']).isNotEmpty) ('Why', str(d['reason'])),
      if (str(d['note']).isNotEmpty) ('Note', str(d['note'])),
      if (str(p['invoice_number']).isNotEmpty) ('Invoice', str(p['invoice_number'])),
    ];
    return TkAppLook(
      child: TkSheet(
        title: 'Approve ${_kindWords(kind).toLowerCase()}?',
        crumb: 'School · asked by ${_asker(p)} at ${lagosClock(str(p['requested_at']))}',
        overlay: _saved ? const TkSavedTick() : null,
        body: TkFormBody(
          children: [
            TkFormGroup(children: [TkLines(rows)]),
            TkCallout(
              leading: TkSquare('b-person', color: look.orange),
              title: 'You are the second person',
              sub: kind.startsWith('cash')
                  ? 'Approved, it is counted on the invoice and the receipt goes to the family.'
                  : 'Approved, the discount is on the student’s invoice.',
              color: look.orange,
            ),
            if (_declining) TkFormGroup(children: [TkKitField(label: 'Why decline (the person who asked is told)', child: TkTextBox(controller: _reason, max: 300, size: 15, height: 46))]),
            if (_error != null) TkErrorBox(_error!),
          ],
        ),
        footer: TkFootTwo(
          firstOwn: 38,
          secondOwn: 28,
          first: TkGhostButton(
            label: _declining ? 'Decline it' : 'Decline',
            danger: _declining,
            onTap: _busy || (_declining && _reason.text.trim().isEmpty) ? null : () => _declining ? _decide('declined') : setState(() => _declining = true),
          ),
          second: TkCta(label: 'Approve', onTap: _busy ? null : () => _decide('approved')),
        ),
      ),
    );
  }
}

// --- Receipts ---

class ReceiptsPage extends StatefulWidget {
  const ReceiptsPage({super.key, required this.auth, this.number, this.student});
  final Auth auth;
  final String? number;
  final String? student;

  @override
  State<ReceiptsPage> createState() => _ReceiptsPageState();
}

class _ReceiptsPageState extends State<ReceiptsPage> {
  late final _find = TextEditingController(text: widget.number ?? '');
  List<Map<String, dynamic>>? _rows;
  String? _error;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _find.addListener(() {
      if (_find.text.trim().isEmpty || _find.text.trim().length >= 3) _load();
    });
    _load();
  }

  @override
  void dispose() {
    _find.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final r = mapOf(await _auth.transport.send('GET', '$schoolApi/receipts', query: {
        if (_find.text.trim().isNotEmpty) 'number': _find.text.trim(),
        if ((widget.student ?? '').isNotEmpty) 'student': widget.student!,
        'limit': '50',
        if (_auth.mayReveal) 'reveal': 'true',
      }));
      if (mounted) setState(() => (_rows = maps(r?['receipts']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _pdf(Map<String, dynamic> rc) async {
    try {
      final bytes = await _auth.transport.bytes('$schoolApi/receipts/${Uri.encodeComponent(str(rc['id']))}/pdf');
      await handOffFile(Uint8List.fromList(bytes), 'receipt-${str(rc['number'])}.pdf', 'application/pdf', null);
    } on Object catch (e) {
      if (mounted) said(context, errorWords(e), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final rows = _rows;
    return TkPageScreen(
      title: 'Receipts',
      titleSize: 30,
      backLabel: widget.student == null ? 'Back to School' : 'Back',
      onRefresh: _load,
      children: [
        if (widget.student == null) TkSearchBox(controller: _find, hint: 'Receipt number', margin: EdgeInsets.zero),
        if (_error != null) TkErrorBox(_error!),
        if (rows == null && _error == null) const TkLoading(rows: 3, label: 'Loading receipts…'),
        if (rows != null)
          TkSection(
            inset: 56,
            empty: 'No receipts here yet.',
            children: [
              for (final r in rows)
                TkListRow(
                  title: str(r['number']),
                  sub: [
                    [naira(kobo(r['amount_kobo'])), nameOr(r), str(r['class']), str(r['invoice_number'])].where((x) => x.isNotEmpty).join(' · '),
                    '${str(r['how']) == 'account' ? 'Into their account' : deskWay(str(r['how']))} · ${dayWords(str(r['paid_at']))} ${lagosClock(str(r['paid_at']))}',
                  ].join('\n'),
                  icon: 'b-receipt',
                  square: r['voided'] == true ? const Color(0xFF8E8E93) : look.lime,
                  amount: r['voided'] == true ? null : (kobo(r['balance_after_kobo']) <= 0 ? 'Paid in full' : '${naira(kobo(r['balance_after_kobo']))} left'),
                  status: r['voided'] == true ? 'Voided' : null,
                  statusTone: CardTone.danger,
                  semanticsLabel: 'Receipt ${str(r['number'])}, ${naira(kobo(r['amount_kobo']))}: open its PDF',
                  onTap: () => _pdf(r),
                ),
            ],
          ),
      ],
    );
  }
}

/// Who asked for a request, in words: their email, else how the server
/// names them; never their id.
String _asker(Map<String, dynamic> p) {
  for (final k in ['requested_email', 'requested_by']) {
    final v = str(p[k]);
    if (v.isNotEmpty && !RegExp(r'^[0-9a-f-]{36}$').hasMatch(v)) return v.startsWith('user:') ? 'someone' : v;
  }
  return 'someone';
}
