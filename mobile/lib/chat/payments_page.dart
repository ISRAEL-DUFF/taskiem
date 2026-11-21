// Payments (P1; docs/spec/chat-pay-people.md): the payments to people,
// the newest first (GET /v1/payments), filtered by what became of them;
// one opens as a sheet, with Pay and Cancel while it is not sent yet. The
// chat's Pay topic opens it (its page /payments), and a link to one
// payment (/payments/<id>, which Taskiem sends in the chat) opens that
// payment over it. For an owner, what each role may pay without their OK
// (GET /v1/pay-limits, PUT /v1/pay-limits/{role}). Pay several people
// (P3) starts here, and the batches paid together are listed, the newest
// paid the same again in one tap (copy_of).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords, nairaText;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/list.dart' show TkEmpty;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import 'approve.dart' show TkTextBox;
import 'deal.dart' show dealKobo;
import 'give_task.dart' show roleWords;
import 'invoices_page.dart' show shortDay;
import 'pay_many.dart';
import 'pay_sheet.dart';
import 'team_pages.dart' show staffRoles;
import 'words.dart' show naira;

/// The filters: what became of them.
const paymentFilters = [('', 'All'), ('prepared', 'Not sent yet'), ('waiting_ok', 'Waiting for an OK'), ('paid', 'Paid'), ('failed', 'Not paid')];

/// A role's limit in words: "₦50,000 a payment · ₦200,000 a day", or
/// that nothing goes without the owner's OK.
String payLimitWords(PayLimit? l) {
  final one = l?.oneKobo ?? 0, day = l?.dayKobo ?? 0;
  if (one == 0 && day == 0) return 'Nothing without your OK';
  return '${naira(one)} a payment · ${naira(day)} a day';
}

/// Opens one payment by its id over [context], as a link does; true once
/// it was read.
Future<Payment?> openPayment(BuildContext context, Auth auth, String id) async {
  final Payment p;
  try {
    p = (await auth.api.getPayment(id: id)).payment;
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, errorWords(e), error: true);
    return null;
  }
  if (!context.mounted) return null;
  return showTkSheet<Payment>(context, (_) => PaymentSheet(auth: auth, payment: p));
}

class SentPaymentsPage extends StatefulWidget {
  const SentPaymentsPage({super.key, required this.auth, this.open, this.backLabel = 'Back', this.follow = true});
  final Auth auth;

  /// A payment to open over the list once it shows (a link's).
  final String? open;
  final String backLabel;
  final bool follow;

  @override
  State<SentPaymentsPage> createState() => _SentPaymentsPageState();
}

class _SentPaymentsPageState extends State<SentPaymentsPage> with Polling {
  List<Payment>? _list;
  String _filter = '';
  String? _error;
  List<PayLimit>? _limits;
  List<String> _roles = const [];
  List<PaymentBatch> _batches = const [];
  bool _copying = false;

  Auth get _auth => widget.auth;

  bool get _owner => (_auth.me?.roles as List?)?.contains('owner') ?? false;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
    if (widget.follow) startPolling(const Duration(seconds: 30), _load);
    if (_owner) unawaited(_loadLimits());
    final id = widget.open;
    if (id != null && id.isNotEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) async {
        if (!mounted) return;
        await openPayment(context, _auth, id);
        await _load();
      });
    }
  }

  Future<void> _load() async {
    try {
      final r = await _auth.api.listPayments(status: _filter.isEmpty ? null : _filter);
      if (mounted) setState(() => (_list = r.payments, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    try {
      final r = await _auth.api.listPaymentBatches();
      if (mounted) setState(() => _batches = r.batches.where((b) => b.status != 'cancelled').take(10).toList());
    } on Object {
      // The payments show without them.
    }
  }

  Future<void> _payMany() async {
    await openPayMany(context, _auth);
    await _load();
  }

  /// A batch opened: read whole (the list carries its counts alone).
  Future<void> _openBatch(PaymentBatch b) async {
    try {
      final whole = (await _auth.api.getPaymentBatch(id: b.id)).batch;
      if (!mounted) return;
      await showTkSheet<PaymentBatch>(context, (_) => BatchSheet(auth: _auth, batch: whole, follow: widget.follow));
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    }
    await _load();
  }

  /// The same as last time: [b]'s rows prepared again, to check and pay.
  Future<void> _same(PaymentBatch b) async {
    setState(() => _copying = true);
    try {
      final fresh = (await _auth.api.createPaymentBatch(body: CreatePaymentBatchRequest(copyOf: b.id, from: 'tools'))).batch;
      if (!mounted) return;
      final held = [for (final h in fresh.held ?? const <PaymentBatchHeldItem>[]) (who: h.payee ?? 'Someone', problem: h.problem ?? '')];
      await showTkSheet<PaymentBatch>(context, (_) => BatchSheet(auth: _auth, batch: fresh, fresh: true, held: held, follow: widget.follow));
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _copying = false);
    }
    await _load();
  }

  Widget _batchRow(PaymentBatch b) {
    final s = batchStatus(b);
    final when = shortDay(b.createdAt.toUtc().add(const Duration(hours: 1)).toIso8601String());
    final name = b.title.isEmpty ? peopleWords(b.count) : b.title;
    return TkListRow(
      title: name,
      icon: 'b-people',
      sub: '${peopleWords(b.count)} · $when',
      subLines: 1,
      titleLines: 1,
      amount: naira(b.totalKobo),
      status: s.words,
      statusTone: s.tone,
      waiting: b.status == 'prepared' || b.waiting > 0,
      semanticsLabel: '$name: ${peopleWords(b.count)}, ${naira(b.totalKobo)}, ${s.words}. Open',
      onTap: () => unawaited(_openBatch(b)),
    );
  }

  Future<void> _loadLimits() async {
    try {
      final r = await _auth.api.getPayLimits();
      if (mounted) setState(() => _limits = r.limits);
    } on Object {
      if (mounted) setState(() => _limits = const []);
    }
    try {
      final r = await _auth.transport.send('GET', '/v1/roles');
      final names = [for (final x in ((r as Map)['roles'] as List? ?? const [])) if (x is Map && x['name'] is String) x['name'] as String];
      if (mounted) setState(() => _roles = names);
    } on Object {
      // The staff roles Taskiem starts with, below.
    }
  }

  /// Every role but the owner's: those the business has, those with a
  /// limit, else the staff roles Taskiem starts with.
  List<String> get _limitRoles {
    final out = <String>[...(_roles.isEmpty ? [for (final r in staffRoles) r.$1] : _roles)];
    for (final l in _limits ?? const <PayLimit>[]) {
      if (l.role != null && !out.contains(l.role)) out.add(l.role!);
    }
    return out.where((r) => r != 'owner').toList();
  }

  void _pick(String f) {
    setState(() => (_filter = f, _list = null));
    unawaited(_load());
  }

  Future<void> _open(Payment p) async {
    await showTkSheet<Payment>(context, (_) => PaymentSheet(auth: _auth, payment: p));
    await _load();
  }

  Future<void> _editLimit(String role) async {
    final had = (_limits ?? const <PayLimit>[]).where((l) => l.role == role).firstOrNull;
    final saved = await showTkSheet<PayLimit>(context, (_) => PayLimitSheet(auth: _auth, role: role, limit: had));
    if (saved == null || !mounted) return;
    showTkToast(context, 'Saved: ${roleWords(role)} may pay ${payLimitWords(saved).toLowerCase()}');
    unawaited(_loadLimits());
  }

  Widget _row(Payment p) {
    final s = paymentStatus(p.status);
    final when = shortDay(p.createdAt.toUtc().add(const Duration(hours: 1)).toIso8601String());
    return TkListRow(
      title: paymentName(p),
      letters: paymentName(p),
      sub: '${p.account} · ${purposeWords(p)} · $when',
      subLines: 1,
      titleLines: 1,
      amount: naira(p.amountKobo),
      status: s.words,
      statusTone: s.tone,
      waiting: p.status == 'prepared',
      semanticsLabel: '${paymentName(p)}, ${naira(p.amountKobo)}, ${s.words}. ${purposeWords(p)}, ${p.bankName} ${p.account}.',
      onTap: () => unawaited(_open(p)),
    );
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final list = _list;
    final toConfirm = (list ?? const <Payment>[]).where((p) => p.status == 'prepared').toList();
    final rest = (list ?? const <Payment>[]).where((p) => p.status != 'prepared').toList();
    final last = _batches.where((b) => b.status == 'sent').firstOrNull;
    return TkPageScreen(
      title: 'Payments',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Money you sent to people from your payments account: each checked with the bank and confirmed with a passkey. '
          'One over a person’s limit, or to an account changed since it was last paid, waits for the owner’s OK in Approvals.',
      onRefresh: _load,
      under: TkChips<String>(chips: [for (final f in paymentFilters) (f.$1, f.$2)], on: _filter, onPick: _pick),
      children: [
        if (_error != null) TkCallout(leading: TkSquare('b-x', color: look.red), title: 'Payments did not load', sub: _error, color: look.red),
        if (list == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading the payments…'),
        TkSection(
          inset: 56,
          children: [
            TkListRow(
              title: 'Pay several people',
              sub: 'Pick them or paste a list: one passkey for all',
              subLines: 1,
              icon: 'b-people',
              square: look.teal,
              onTap: () => unawaited(_payMany()),
            ),
          ],
        ),
        if (list != null && list.isEmpty)
          TkEmpty(
            icon: 'money',
            title: _filter.isEmpty ? 'No payments yet' : 'None here',
            line: _filter.isEmpty ? 'Pay someone from the chat: say “pay”, or open Pay in Tools.' : 'Payments that are ${paymentFilters.firstWhere((f) => f.$1 == _filter).$2.toLowerCase()} show here.',
          ),
        if (toConfirm.isNotEmpty) TkSection(title: 'Not sent yet', count: toConfirm.length, children: [for (final p in toConfirm) _row(p)]),
        if (_batches.isNotEmpty)
          TkSection(
            title: 'Paid together',
            trailing: last == null
                ? null
                : TkSmallButton(
                    label: _copying ? 'Preparing…' : 'Same as last time',
                    semantics: 'Same as last time: ${last.title.isEmpty ? peopleWords(last.count) : last.title}, ${naira(last.totalKobo)}',
                    onPressed: _copying ? null : () => unawaited(_same(last)),
                  ),
            children: [for (final b in _batches) _batchRow(b)],
          ),
        if (rest.isNotEmpty) TkSection(title: toConfirm.isEmpty ? null : 'Sent', children: [for (final p in rest) _row(p)]),
        if (_owner && _limits != null)
          TkSection(
            title: 'What others may pay',
            inset: 56,
            children: [
              for (final role in _limitRoles)
                TkListRow(
                  title: roleWords(role),
                  sub: payLimitWords((_limits ?? const <PayLimit>[]).where((l) => l.role == role).firstOrNull),
                  subLines: 1,
                  icon: 'b-lock',
                  square: look.teal,
                  semanticsLabel: '${roleWords(role)}: ${payLimitWords((_limits ?? const <PayLimit>[]).where((l) => l.role == role).firstOrNull)}. Change',
                  onTap: () => unawaited(_editLimit(role)),
                ),
            ],
          ),
      ],
    );
  }
}

/// A role's limit (the owner's alone): one payment at most, and a day's
/// total; ₦0 is nothing without the owner's OK.
class PayLimitSheet extends StatefulWidget {
  const PayLimitSheet({super.key, required this.auth, required this.role, this.limit});
  final Auth auth;
  final String role;
  final PayLimit? limit;

  @override
  State<PayLimitSheet> createState() => _PayLimitSheetState();
}

class _PayLimitSheetState extends State<PayLimitSheet> {
  late final _one = TextEditingController(text: nairaText(widget.limit?.oneKobo ?? 0));
  late final _day = TextEditingController(text: nairaText(widget.limit?.dayKobo ?? 0));
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _one.addListener(() => setState(() {}));
    _day.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _one.dispose();
    _day.dispose();
    super.dispose();
  }

  int? _kobo(TextEditingController c) => c.text.trim().isEmpty ? 0 : dealKobo(c.text);

  String? get _problem {
    final one = _kobo(_one), day = _kobo(_day);
    if (one == null || day == null) return 'Type an amount, like 50,000 or 50k.';
    if (day > 0 && one > day) return 'One payment is no more than a day’s total.';
    if (day == 0 && one > 0) return 'Give a day’s total too.';
    return null;
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      final saved = await widget.auth.api.putPayLimit(role: widget.role, body: PayLimit(oneKobo: _kobo(_one), dayKobo: _kobo(_day)));
      if (mounted) Navigator.of(context).pop(saved);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final problem = _problem;
    return TkFormSheet(
      title: '${roleWords(widget.role)}: what they may pay',
      crumb: 'Payments',
      action: _busy ? 'Saving…' : 'Save',
      onAction: _busy || problem != null ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(
          children: [
            TkKitField(label: 'One payment, at most', child: TkTextBox(controller: _one, money: true, prefix: '₦', size: 15, height: 46)),
            TkKitField(label: 'A day’s total, at most', hint: 'Over either, a payment waits for your OK. ₦0: nothing goes without it.', child: TkTextBox(controller: _day, money: true, prefix: '₦', size: 15, height: 46)),
          ],
        ),
        if (problem != null) TkCallout(leading: TkSquare('b-alert', color: look.orange), title: problem, color: look.orange),
        if (_error != null) TkCallout(leading: TkSquare('b-x', color: look.red), title: 'Not saved', sub: _error, color: look.red),
      ],
    );
  }
}
