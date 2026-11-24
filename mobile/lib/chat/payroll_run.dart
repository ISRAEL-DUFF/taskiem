// This month's payroll in Taskiem (P4b-3, docs/spec/chat-pay-people.md):
// one payroll, each person's pay and its breakdown, a bonus or a
// deduction (before tax), leaving someone out and putting them back,
// Recalculate and Cancel before approval, the wallet against the total,
// and Approve, Reject and Lock and pay, each step that commits money
// confirmed with the code Payrolla sends to the approver
// (GET /v1/payroll/runs/{run}, POST …/adjustments, …/exclusions,
// …/compute, …/cancel, …/confirmations, …/approve, …/lock, …/reject).
// Also the payroll's card in the Taskiem chat. The code is sent to
// Payrolla once and kept nowhere.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../app/links.dart' show openOutside;
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkAppLook, TkSmallButton;
import '../kit/list.dart' show TkEmpty;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show openOrSay, showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart';
import '../screens/sign_in.dart' show CodeBoxes;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart' show CardPill;
import 'groups.dart' show askSure, pickOneOf;
import 'invoices_page.dart' show shortDay;
import 'payroll_page.dart' show payrollLineState, payrollPeriod, payrollRefusal, payrollStatus, peopleCount;
import 'payroll_settings.dart' show PayrollSettingsPage, payrollKey;
import 'team_pages.dart' show StaffPersonPage;
import 'words.dart' show naira;

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

Future<T?> _sheet<T>(BuildContext context, Widget sheet) => showTkSheet<T>(context, (_) => TkAppLook(child: sheet));

/// The decision next for one who may decide: approve a payroll waiting
/// for approval, lock an approved one.
String? payrollNextDecision(String status) => switch (status) { 'COMPUTED' => 'approve', 'APPROVED' => 'lock', _ => null };

/// "Approve", "Lock and pay".
String payrollActionWords(String action) => action == 'lock' ? 'Lock and pay' : 'Approve';

/// Said before a lock's code is asked for.
String payrollLockWarning(String payDay) => 'Locking pays everyone on $payDay. A locked payroll can’t be unlocked.';

/// What changed since last month, a line each.
List<String> payrollChanges(GetPayrollRunResponseChanges? c) => c == null
    ? const []
    : [
        if (c.new_.isNotEmpty) 'New this month: ${c.new_.join(', ')}',
        if (c.left.isNotEmpty) 'Not paid this month: ${c.left.join(', ')}',
        for (final p in c.payChanged) '${p.name}: pay ${naira(p.fromKobo)} to ${naira(p.toKobo)}',
      ];

/// Naira typed (digits, commas) as kobo, or null.
int? payrollKobo(String typed) {
  final t = typed.replaceAll(RegExp(r'[₦,\s]'), '');
  final n = double.tryParse(t);
  if (n == null || n < 1) return null;
  return (n * 100).round();
}

// --- the code sheet ---

/// Approve or Lock and pay: Payrolla sends the person a code, they type it
/// here, and it is sent to Payrolla once. Closes with what to say.
class PayrollCodeSheet extends StatefulWidget {
  const PayrollCodeSheet({super.key, required this.auth, required this.run, required this.action});
  final Auth auth;
  final PayrollRun run;
  final String action;

  @override
  State<PayrollCodeSheet> createState() => _PayrollCodeSheetState();
}

class _PayrollCodeSheetState extends State<PayrollCodeSheet> {
  final _code = TextEditingController();
  StartPayrollRunCodeResponse? _sent;
  String? _problem;
  bool _needNew = false;
  bool _notLinked = false;
  bool _busy = false;

  @override
  void dispose() {
    _code.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.startPayrollRunCode(run: widget.run.runId, body: StartPayrollRunCodeRequest(action: widget.action, key: payrollKey()));
      if (!mounted) return;
      _code.clear();
      setState(() => (_sent = r, _needNew = false));
    } on Object catch (e) {
      if (mounted) setState(() => (_problem = errorWords(e), _notLinked = e is ApiError && e.body['code'] == 'not_linked'));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _confirm() async {
    final sent = _sent;
    final code = _code.text.replaceAll(RegExp(r'\D'), '');
    if (sent == null || code.length != 6) return;
    setState(() => (_busy = true, _problem = null));
    try {
      // Each try at a code is its own submission.
      final id = sent.challenge.challengeId;
      final String words;
      if (widget.action == 'lock') {
        words = (await widget.auth.api.lockPayrollRun(run: widget.run.runId, body: LockPayrollRunRequest(challengeId: id, code: code, key: payrollKey()))).words;
      } else {
        words = (await widget.auth.api.approvePayrollRun(run: widget.run.runId, body: ApprovePayrollRunRequest(challengeId: id, code: code, key: payrollKey()))).words;
      }
      if (mounted) Navigator.of(context).pop(words);
    } on Object catch (e) {
      final c = e is ApiError ? e.body['code'] : null;
      if (mounted) setState(() => (_problem = errorWords(e), _needNew = c == 'code_expired' || c == 'code_locked' || c == 'code_used'));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final sent = _sent;
    final r = widget.run;
    final lock = widget.action == 'lock';
    final day = shortDay(r.payDate);
    return TkFormSheet(
      title: payrollActionWords(widget.action),
      crumb: '${payrollPeriod(r.periodStart, r.periodEnd)} payroll',
      action: sent == null || _needNew ? (_busy ? 'Sending…' : (_needNew ? 'Send a new code' : 'Send me a code')) : payrollActionWords(widget.action),
      onAction: _busy ? null : () => unawaited(sent == null || _needNew ? _send() : _confirm()),
      second: sent != null && !_needNew ? 'Send a new code' : null,
      onSecond: sent != null && !_needNew && !_busy ? () => unawaited(_send()) : null,
      foot: '${peopleCount(r.employeeCount)} · ${naira(r.totalNetKobo)}',
      children: [
        if (lock)
          TkCallout(
            key: const ValueKey('payroll-lock-warning'),
            leading: TkSquare('b-lock', color: look.orange),
            title: payrollLockWarning(day.isEmpty ? 'pay day' : day),
            subLines: 3,
            color: look.orange,
          ),
        if (sent == null)
          TkCallout(
            leading: TkSquare('b-lock', color: look.teal),
            title: 'A code from Payrolla',
            sub: 'Payrolla sends a code to your own phone, email or authenticator app for this one step.',
            subLines: 4,
            color: look.teal,
          ),
        if (sent != null) ...[
          TkCallout(leading: TkSquare('b-lock', color: look.teal), title: sent.sentTo, sub: 'Type it here. Taskiem sends it to Payrolla once and keeps it nowhere.', subLines: 4, color: look.teal),
          TkFormGroup(children: [
            TkKitField(
              label: 'The code from Payrolla',
              hint: sent.safer ? 'A code from an authenticator app is safer: set one up in Payrolla.' : null,
              field: false,
              child: CodeBoxes(key: const ValueKey('payroll-run-code'), controller: _code, hint: 'The code from Payrolla', onChanged: (_) => setState(() {})),
            ),
          ]),
        ],
        if (_problem != null)
          TkCallout(
            leading: TkSquare('b-alert', color: look.red),
            title: _problem!,
            subLines: 3,
            color: look.red,
            button: _notLinked ? 'Link my Payrolla account' : null,
            onPressed: _notLinked ? () => unawaited(_push(context, PayrollSettingsPage(auth: widget.auth))) : null,
          ),
      ],
    );
  }
}

/// Reject: back to whoever prepared it, with a short note. No code.
class PayrollRejectSheet extends StatefulWidget {
  const PayrollRejectSheet({super.key, required this.auth, required this.run});
  final Auth auth;
  final PayrollRun run;

  @override
  State<PayrollRejectSheet> createState() => _PayrollRejectSheetState();
}

class _PayrollRejectSheetState extends State<PayrollRejectSheet> {
  final _note = TextEditingController();
  final _key = payrollKey();
  String? _problem;
  bool _busy = false;

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final note = _note.text.trim();
    if (note.isEmpty) return;
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.rejectPayrollRun(run: widget.run.runId, body: RejectPayrollRunRequest(note: note, key: _key));
      if (mounted) Navigator.of(context).pop(r.words);
    } on Object catch (e) {
      if (mounted) setState(() => _problem = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkFormSheet(
      title: 'Send it back',
      crumb: '${payrollPeriod(widget.run.periodStart, widget.run.periodEnd)} payroll',
      action: _busy ? 'Sending…' : 'Send it back',
      onAction: _busy ? null : () => unawaited(_send()),
      children: [
        TkFormGroup(children: [
          TkKitField(label: 'What needs fixing', hint: 'Whoever prepared it is told, with your note. Nothing is paid.', child: TkTextBox(key: const ValueKey('payroll-reject-note'), controller: _note, max: 300)),
        ]),
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}

/// A bonus or a deduction for one person, with why. Closes with true.
class PayrollAdjustSheet extends StatefulWidget {
  const PayrollAdjustSheet({super.key, required this.auth, required this.run, required this.lines});
  final Auth auth;
  final String run;
  final List<GetPayrollRunResponseLinesItem> lines;

  @override
  State<PayrollAdjustSheet> createState() => _PayrollAdjustSheetState();
}

class _PayrollAdjustSheetState extends State<PayrollAdjustSheet> {
  final _amount = TextEditingController();
  final _reason = TextEditingController();
  final _key = payrollKey();
  late String _who = widget.lines.firstOrNull?.employeeId ?? '';
  String _type = 'bonus';
  String? _problem;
  bool _busy = false;

  @override
  void dispose() {
    _amount.dispose();
    _reason.dispose();
    super.dispose();
  }

  String get _whoName => widget.lines.where((l) => l.employeeId == _who).firstOrNull?.name ?? 'Choose';

  Future<void> _pick() async {
    final id = await pickOneOf(context, title: 'Who', icon: 'b-people', value: _who, choices: [for (final l in widget.lines) (l.employeeId, l.name, naira(l.netKobo))]);
    if (id != null && mounted) setState(() => _who = id);
  }

  Future<void> _save() async {
    final kobo = payrollKobo(_amount.text);
    final reason = _reason.text.trim();
    if (kobo == null || reason.isEmpty || _who.isEmpty) {
      setState(() => _problem = 'Say how much, and why.');
      return;
    }
    setState(() => (_busy = true, _problem = null));
    try {
      await widget.auth.api.adjustPayrollRun(run: widget.run, body: AdjustPayrollRunRequest(employeeId: _who, type: _type, amountKobo: kobo, reason: reason, key: _key));
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => _problem = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkFormSheet(
      title: 'A bonus or deduction',
      action: _busy ? 'Adding…' : (_type == 'bonus' ? 'Add the bonus' : 'Add the deduction'),
      onAction: _busy ? null : () => unawaited(_save()),
      children: [
        TkFormGroup(children: [
          TkKitField(label: 'Who', field: false, child: TkPickBox(label: 'Who it is for', shown: _whoName, onTap: () => unawaited(_pick()))),
          TkKitField(
            label: 'Which',
            field: false,
            child: TkChips<String>(
              label: 'Which',
              padding: EdgeInsets.zero,
              chips: const [('bonus', 'A bonus'), ('deduction', 'A deduction')],
              on: _type,
              onPick: (v) => setState(() => _type = v),
            ),
          ),
          TkKitField(
            label: 'How much',
            hint: 'Before tax: tax and pension apply as usual.',
            child: TkTextBox(key: const ValueKey('payroll-adjust-amount'), controller: _amount, money: true, prefix: '₦', size: 15, height: 46),
          ),
          TkKitField(
            label: 'Why',
            hint: _type == 'deduction' ? 'An advance repaid is a deduction.' : null,
            child: TkTextBox(key: const ValueKey('payroll-adjust-reason'), controller: _reason, max: 200, size: 15, height: 46),
          ),
        ]),
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}

/// One person's pay, line by line; Leave out this month for those who may.
class PayrollLineSheet extends StatefulWidget {
  const PayrollLineSheet({super.key, required this.auth, required this.run, required this.line, required this.mayChange});
  final Auth auth;
  final String run;
  final GetPayrollRunResponseLinesItem line;
  final bool mayChange;

  @override
  State<PayrollLineSheet> createState() => _PayrollLineSheetState();
}

class _PayrollLineSheetState extends State<PayrollLineSheet> {
  final _reason = TextEditingController();
  final _key = payrollKey();
  bool _leaving = false;
  bool _busy = false;
  String? _problem;

  @override
  void dispose() {
    _reason.dispose();
    super.dispose();
  }

  Future<void> _leave() async {
    final reason = _reason.text.trim();
    if (reason.isEmpty) {
      setState(() => _problem = 'Say why.');
      return;
    }
    setState(() => (_busy = true, _problem = null));
    try {
      await widget.auth.api.excludePayrollRun(run: widget.run, body: ExcludePayrollRunRequest(employeeId: widget.line.employeeId, reason: reason, key: _key));
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => _problem = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final l = widget.line;
    final items = l.items ?? const <GetPayrollRunResponseLinesItemItemsItem>[];
    final earn = items.where((i) => i.kind == 'earning');
    final ded = items.where((i) => i.kind == 'deduction');
    final emp = items.where((i) => i.kind == 'employer');
    final rows = <(String, String)>[
      for (final i in earn) (i.label, naira(i.amountKobo)),
      ('Gross', naira(l.totalEarningsKobo ?? l.grossKobo ?? 0)),
      for (final i in ded) (i.label, '−${naira(i.amountKobo)}'),
      ('Net pay', naira(l.netKobo)),
    ];
    return TkFormSheet(
      title: l.name.isEmpty ? 'Someone' : l.name,
      crumb: 'This month’s pay',
      action: _leaving ? (_busy ? 'Leaving out…' : 'Leave out') : 'Done',
      onAction: _busy ? null : () => _leaving ? unawaited(_leave()) : Navigator.of(context).pop(false),
      second: widget.mayChange && !_leaving ? 'Leave out this month' : null,
      onSecond: widget.mayChange && !_leaving ? () => setState(() => _leaving = true) : null,
      children: [
        if (items.isEmpty) TkCallout(leading: TkSquare('b-cash', color: look.teal), title: 'Net pay ${naira(l.netKobo)}', color: look.teal) else TkFormGroup(children: [TkLines(rows)]),
        if (emp.isNotEmpty) TkFormGroup(title: 'The business also pays', children: [TkLines([for (final i in emp) (i.label, naira(i.amountKobo))])]),
        if (_leaving)
          TkFormGroup(children: [
            TkKitField(
              label: 'Why leave them out',
              hint: 'They’re not paid in this payroll only. Next month they’re in it again.',
              child: TkTextBox(key: const ValueKey('payroll-leave-reason'), controller: _reason, max: 200, size: 15, height: 46),
            ),
          ]),
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}

// --- one payroll ---

class PayrollRunPage extends StatefulWidget {
  const PayrollRunPage({super.key, required this.auth, required this.run, this.summary, this.backLabel = 'Back to Payroll'});
  final Auth auth;
  final String run;

  /// The run as the list had it, shown while the whole run is read.
  final PayrollRun? summary;
  final String backLabel;

  @override
  State<PayrollRunPage> createState() => _PayrollRunPageState();
}

class _PayrollRunPageState extends State<PayrollRunPage> {
  GetPayrollRunResponse? _got;
  Object? _error;
  String? _said;
  bool _busy = false;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await _auth.api.getPayrollRun(run: widget.run);
      if (mounted) setState(() => (_got = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _do(Future<Object?> Function() fn, String said) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await fn();
      if (!mounted) return;
      showTkToast(context, said);
      await _load();
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _decide(PayrollRun r, String action) async {
    final words = await _sheet<String>(context, action == 'reject' ? PayrollRejectSheet(auth: _auth, run: r) : PayrollCodeSheet(auth: _auth, run: r, action: action));
    if (words == null || !mounted) return;
    setState(() => _said = words);
    await _load();
  }

  Future<void> _openLine(GetPayrollRunResponseLinesItem l, bool may) async {
    final changed = await _sheet<bool>(context, PayrollLineSheet(auth: _auth, run: widget.run, line: l, mayChange: may));
    if (changed == true && mounted) {
      showTkToast(context, '${l.name} left out this month');
      await _load();
    }
  }

  Future<void> _adjust(List<GetPayrollRunResponseLinesItem> lines) async {
    final added = await _sheet<bool>(context, PayrollAdjustSheet(auth: _auth, run: widget.run, lines: lines));
    if (added == true && mounted) await _load();
  }

  Future<void> _cancel(PayrollRun r) async {
    final yes = await askSure(
      context,
      title: 'Cancel ${payrollPeriod(r.periodStart, r.periodEnd)} payroll?',
      body: 'No one is paid from it. You can prepare it again.',
      ok: 'Cancel this payroll',
      cancel: 'Keep it',
      danger: true,
    );
    if (!yes || !mounted) return;
    await _do(() => _auth.api.cancelPayrollRun(run: widget.run, body: CancelPayrollRunRequest(key: payrollKey())), 'Payroll cancelled');
  }

  static const _order = {'failed': 0, 'returned': 1, 'waiting': 2, 'paid': 3};

  Widget _line(BuildContext context, GetPayrollRunResponseLinesItem l, bool open, bool may) {
    final s = payrollLineState(l.state);
    final staff = l.staff is Map ? (l.staff! as Map).cast<String, dynamic>() : null;
    final staffId = staff?['user_id'] is String ? staff!['user_id'] as String : '';
    final staffName = staff?['name'] is String ? staff!['name'] as String : '';
    final reason = (l.reason ?? '').trim();
    final breakdown = (l.items ?? const []).isNotEmpty;
    final sub = [
      if (reason.isNotEmpty) reason,
      if (reason.isEmpty && staffId.isNotEmpty && staffName.isNotEmpty && staffName != l.name) 'Staff: $staffName',
      if (reason.isEmpty && staffId.isEmpty) 'Not linked to your staff',
    ].join(' · ');
    return TkListRow(
      title: l.name.isEmpty ? 'Someone' : l.name,
      letters: l.name,
      sub: sub,
      subLines: reason.isNotEmpty ? 3 : 1,
      titleLines: 1,
      amount: naira(l.netKobo),
      status: open && breakdown ? null : s.words,
      statusTone: s.tone,
      waiting: l.state == 'failed' || l.state == 'returned',
      chevron: breakdown || staffId.isNotEmpty,
      semanticsLabel: '${l.name}: ${naira(l.netKobo)}, ${s.words}${reason.isNotEmpty ? '. $reason' : ''}'
          '${breakdown ? '. See the breakdown' : staffId.isNotEmpty ? '. Open their staff page' : ''}',
      onTap: breakdown
          ? () => unawaited(_openLine(l, may))
          : staffId.isEmpty
              ? null
              : () => unawaited(openPayrollStaff(context, _auth, staffId)),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final got = _got;
    final r = got?.run.summary ?? widget.summary;
    final st = payrollStatus(r?.status ?? '');
    final lines = [...?got?.lines]..sort((a, b) => (_order[a.state] ?? 2) - (_order[b.state] ?? 2));
    final notPaid = lines.where((l) => l.state == 'failed' || l.state == 'returned').toList();
    final rest = lines.where((l) => l.state != 'failed' && l.state != 'returned').toList();
    final c = got?.counts;
    final counts = c == null
        ? const <String>[]
        : [
            if (c.paid > 0) '${c.paid} paid',
            if (c.waiting > 0) '${c.waiting} waiting',
            if (c.failed > 0) '${c.failed} not paid',
            if (c.returned > 0) '${c.returned} returned',
          ];
    final day = r == null ? '' : shortDay(r.payDate);
    final may = got?.may;
    final open = r != null && (r.status == 'DRAFT' || r.status == 'COMPUTED');
    final prep = (may?.prepare ?? false) && open;
    final next = r != null && (may?.approve ?? false) ? payrollNextDecision(r.status) : null;
    final approval = got?.approvalUrl;
    final changes = payrollChanges(got?.changes);
    final short = got?.funding;
    final adjustments = got?.adjustments ?? const [];
    final out = got?.exclusions ?? const [];
    return TkPageScreen(
      title: r == null ? 'Payroll' : payrollPeriod(r.periodStart, r.periodEnd),
      titleSize: 22,
      backLabel: widget.backLabel,
      description: 'Each person’s pay as Payrolla works it out, and approving it. Each step that pays is confirmed with a code Payrolla sends you.',
      onRefresh: _load,
      children: [
        if (_error != null)
          payrollRefusal(context, _error!, 'The run did not load'),
        if (r != null)
          TkTopCard(
            semanticsLabel: '${naira(r.totalNetKobo)} net to ${peopleCount(r.employeeCount)}${day.isEmpty ? '' : ', pay day $day'}. ${st.words}',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Net pay', color: look.limeInk, icon: 'b-cash', trailing: day.isEmpty ? null : 'Pay day $day'),
                const SizedBox(height: 8),
                FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text(naira(r.totalNetKobo), style: tkFigure(context, size: 38))),
                const SizedBox(height: 2),
                Text(
                  [peopleCount(r.employeeCount), if (r.totalGrossKobo > 0) '${naira(r.totalGrossKobo)} before deductions'].join(' · '),
                  style: tkText(size: 14, lineHeight: 1.35, color: t.muted),
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    CardPill(got?.paysOn ?? st.words, st.tone),
                    if (counts.isNotEmpty) ...[
                      const SizedBox(width: 10),
                      Expanded(child: Text(counts.join(' · '), maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 13, color: t.muted))),
                    ],
                  ],
                ),
              ],
            ),
          ),
        if (_said != null) TkCallout(key: const ValueKey('payroll-said'), leading: TkSquare('b-check', color: look.teal), title: _said!, subLines: 3, color: look.teal),
        if (got?.notWorkedOut != null)
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: 'Not worked out yet',
            sub: got!.notWorkedOut,
            subLines: 5,
            color: look.orange,
            button: (may?.prepare ?? false) ? 'Recalculate' : null,
            onPressed: (may?.prepare ?? false) ? () => unawaited(_do(() => _auth.api.computePayrollRun(run: widget.run, body: ComputePayrollRunRequest(key: payrollKey())), 'Worked out again')) : null,
          ),
        if (short != null && short.shortByKobo > 0)
          TkCallout(
            key: const ValueKey('payroll-short'),
            leading: TkSquare('b-alert', color: look.orange),
            title: 'Short by ${naira(short.shortByKobo)}',
            sub: short.words,
            subLines: 5,
            color: look.orange,
          ),
        if (r != null && next != null) ...[
          if (next == 'lock') TkCallout(leading: TkSquare('b-lock', color: look.violet), title: 'Approved: lock and pay next', sub: payrollLockWarning(day.isEmpty ? 'pay day' : day), subLines: 3, color: look.violet),
          if (!(may?.linked ?? false))
            TkCallout(
              leading: TkSquare('b-alert', color: look.orange),
              title: 'Link your Payrolla account first',
              sub: 'It takes one code.',
              color: look.orange,
              button: 'Link my Payrolla account',
              onPressed: () => unawaited(_push(context, PayrollSettingsPage(auth: _auth))),
            ),
          TkButtonRow([
            TkSmallButton(label: payrollActionWords(next), primary: true, semantics: '${payrollActionWords(next)} ${payrollPeriod(r.periodStart, r.periodEnd)} payroll', onPressed: () => unawaited(_decide(r, next))),
            if (next == 'approve') TkSmallButton(label: 'Reject', semantics: 'Reject: send it back', onPressed: () => unawaited(_decide(r, 'reject'))),
          ]),
        ],
        if (!(may?.approve ?? false) && approval != null && approval.isNotEmpty)
          TkCta(
            label: 'Review and approve in Payrolla',
            icon: 'b-arrow',
            iconAfter: true,
            onTap: () => unawaited(openOrSay(context, () => openOutside(Uri.parse(approval)), 'Payrolla could not be opened.')),
          ),
        if (may?.reconnect ?? false)
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: 'Reconnect Payrolla to do this here',
            color: look.orange,
            button: 'Payroll settings',
            onPressed: () => unawaited(_push(context, PayrollSettingsPage(auth: _auth))),
          ),
        if (changes.isNotEmpty)
          TkSection(
            title: 'Since ${got?.changes?.since ?? 'last month'}',
            inset: 56,
            children: [for (final c in changes) TkListRow(title: c, icon: 'b-people', square: look.teal, titleLines: 2, chevron: false)],
          ),
        if (got == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading the payroll…'),
        if (got != null && lines.isEmpty) const TkEmpty(icon: 'b-people', title: 'No one in this payroll yet', line: 'Payrolla adds each person’s pay as it works the payroll out.'),
        if (notPaid.isNotEmpty) TkSection(title: 'Not paid', count: notPaid.length, children: [for (final l in notPaid) _line(context, l, open, prep)]),
        if (rest.isNotEmpty) TkSection(title: notPaid.isEmpty ? 'Each person' : 'Everyone else', children: [for (final l in rest) _line(context, l, open, prep)]),
        if (adjustments.isNotEmpty || prep)
          TkSection(
            title: 'Bonuses and deductions',
            inset: 56,
            empty: 'None this month.',
            children: [
              for (final a in adjustments)
                TkListRow(
                  title: a.name.isEmpty ? 'Someone' : a.name,
                  sub: '${naira(a.amountKobo)} ${a.type == 'bonus' ? 'bonus' : 'deduction'}, ${a.reason}',
                  subLines: 2,
                  icon: a.type == 'bonus' ? 'b-plus' : 'b-cash',
                  square: a.type == 'bonus' ? look.teal : look.orange,
                  chevron: false,
                  trailing: prep
                      ? TkSmallButton(
                          label: 'Remove',
                          semantics: 'Remove ${a.name}’s ${a.type}',
                          onPressed: _busy ? null : () => unawaited(_do(() => _auth.api.unadjustPayrollRun(run: widget.run, id: a.id, key: payrollKey()), 'Removed')),
                        )
                      : null,
                ),
            ],
          ),
        if (prep) Center(child: TkSmallButton(label: 'Add a bonus or deduction', semantics: 'Add a bonus or deduction', onPressed: () => unawaited(_adjust(lines)))),
        if (out.isNotEmpty)
          TkSection(
            title: 'Left out this month',
            children: [
              for (final x in out)
                TkListRow(
                  title: x.name,
                  letters: x.name,
                  sub: x.reason,
                  subLines: 2,
                  chevron: false,
                  trailing: prep
                      ? TkSmallButton(
                          label: 'Put back',
                          semantics: 'Put ${x.name} back',
                          onPressed: _busy ? null : () => unawaited(_do(() => _auth.api.putBackPayrollRun(run: widget.run, id: x.id, key: payrollKey()), '${x.name} is back in')),
                        )
                      : null,
                ),
            ],
          ),
        if (prep)
          TkButtonRow([
            TkSmallButton(
              label: 'Recalculate',
              semantics: 'Recalculate',
              onPressed: _busy ? null : () => unawaited(_do(() => _auth.api.computePayrollRun(run: widget.run, body: ComputePayrollRunRequest(key: payrollKey())), 'Worked out again')),
            ),
            TkSmallButton(label: 'Cancel this payroll', danger: true, semantics: 'Cancel this payroll', onPressed: _busy ? null : () => unawaited(_cancel(r))),
          ]),
      ],
    );
  }
}

/// The Payroll page's This month: its payroll, or Prepare for those who
/// may. Opens the payroll once prepared.
class PayrollThisMonth extends StatefulWidget {
  const PayrollThisMonth({super.key, required this.auth, required this.month, required this.mayPrepare, required this.onChanged});
  final Auth auth;
  final GetPayrollResponseThisMonth month;
  final bool mayPrepare;
  final VoidCallback onChanged;

  @override
  State<PayrollThisMonth> createState() => _PayrollThisMonthState();
}

class _PayrollThisMonthState extends State<PayrollThisMonth> {
  final _key = payrollKey();
  bool _busy = false;

  Future<void> _prepare() async {
    setState(() => _busy = true);
    try {
      final out = await widget.auth.api.preparePayrollRun(body: PreparePayrollRunRequest(period: widget.month.period, key: _key));
      if (!mounted) return;
      widget.onChanged();
      await _push(context, PayrollRunPage(auth: widget.auth, run: out.run.runId, summary: out.run));
    } on Object catch (e) {
      final existing = e is ApiError && e.body['run_id'] is String ? e.body['run_id'] as String : null;
      if (!mounted) return;
      showTkToast(context, errorWords(e), error: existing == null);
      if (existing != null) await _push(context, PayrollRunPage(auth: widget.auth, run: existing));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final m = widget.month;
    final r = m.run;
    if (r != null) {
      final s = payrollStatus(r.status);
      return TkSection(
        title: 'This month',
        inset: 56,
        children: [
          TkListRow(
            title: '${m.label} payroll',
            sub: '${peopleCount(r.employeeCount)} · ${naira(r.totalNetKobo)}',
            icon: 'b-calendar',
            square: look.lime,
            status: s.words,
            statusTone: s.tone,
            onTap: () => unawaited(_push(context, PayrollRunPage(auth: widget.auth, run: r.runId, summary: r))),
          ),
        ],
      );
    }
    if (!widget.mayPrepare) return const SizedBox.shrink();
    return Column(
      key: const ValueKey('payroll-this-month'),
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkSection(
          title: 'This month',
          inset: 56,
          children: [
            TkListRow(
              title: '${m.label} payroll',
              sub: 'Not prepared yet. Payrolla works out each person’s pay, tax and pension.',
              subLines: 2,
              icon: 'b-calendar',
              square: look.violet,
              chevron: false,
            ),
          ],
        ),
        Center(
          child: TkSmallButton(
            label: _busy ? 'Preparing…' : 'Prepare ${m.month} payroll',
            primary: true,
            semantics: 'Prepare ${m.month} payroll',
            onPressed: _busy ? null : () => unawaited(_prepare()),
          ),
        ),
      ],
    );
  }
}

/// The chat's card as the Taskiem conversation keeps it: the run and
/// its period.
({String run, String period})? payrollCardOf(String text) {
  try {
    final v = jsonDecode(text);
    if (v is Map && v['run_id'] is String && (v['run_id'] as String).isNotEmpty) {
      return (run: v['run_id'] as String, period: v['period'] is String ? v['period'] as String : '');
    }
  } on FormatException {
    return null;
  }
  return null;
}

/// A payroll's card in the Taskiem chat, drawn from Payrolla when shown,
/// with Approve (or Lock and pay), Reject and Open.
class PayrollChatCard extends StatefulWidget {
  const PayrollChatCard({super.key, required this.auth, required this.run, required this.period});
  final Auth auth;
  final String run;
  final String period;

  @override
  State<PayrollChatCard> createState() => _PayrollChatCardState();
}

class _PayrollChatCardState extends State<PayrollChatCard> {
  GetPayrollRunResponse? _got;
  bool _failed = false;
  String? _said;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.getPayrollRun(run: widget.run);
      if (mounted) setState(() => _got = r);
    } on Object {
      if (mounted) setState(() => _failed = true);
    }
  }

  Future<void> _decide(PayrollRun r, String action) async {
    final words = await _sheet<String>(
        context, action == 'reject' ? PayrollRejectSheet(auth: widget.auth, run: r) : PayrollCodeSheet(auth: widget.auth, run: r, action: action));
    if (words == null || !mounted) return;
    setState(() => _said = words);
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final got = _got;
    final r = got?.run.summary;
    final next = r != null && (got?.may?.approve ?? false) ? payrollNextDecision(r.status) : null;
    final st = payrollStatus(r?.status ?? '');
    return TkSection(
      title: '${widget.period.isEmpty ? 'This month’s' : widget.period} payroll',
      inset: 56,
      children: [
        if (_failed) const TkListRow(title: 'You can’t see this payroll here', icon: 'b-lock', chevron: false),
        if (r != null) ...[
          TkListRow(
            title: naira(r.totalNetKobo),
            sub: [peopleCount(r.employeeCount), 'pay day ${shortDay(r.payDate)}', ...payrollChanges(got?.changes)].join(' · '),
            subLines: 3,
            icon: 'b-calendar',
            square: look.lime,
            status: got?.paysOn ?? st.words,
            statusTone: st.tone,
            semanticsLabel: '${widget.period} payroll: ${naira(r.totalNetKobo)}, ${peopleCount(r.employeeCount)}, ${got?.paysOn ?? st.words}. Open',
            onTap: () => unawaited(_push(context, PayrollRunPage(auth: widget.auth, run: r.runId, summary: r, backLabel: 'Back to the chat'))),
          ),
          if ((got?.funding?.shortByKobo ?? 0) > 0)
            TkListRow(title: 'Your Payrolla wallet is short by ${naira(got!.funding!.shortByKobo)}', icon: 'b-alert', square: look.orange, titleLines: 2, chevron: false),
          if (_said != null) TkListRow(title: _said!, icon: 'b-check', square: look.teal, titleLines: 3, chevron: false),
          if (next != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
              child: TkButtonRow([
                TkSmallButton(label: payrollActionWords(next), primary: true, semantics: '${payrollActionWords(next)} ${widget.period} payroll', onPressed: () => unawaited(_decide(r, next))),
                if (next == 'approve') TkSmallButton(label: 'Reject', semantics: 'Reject ${widget.period} payroll', onPressed: () => unawaited(_decide(r, 'reject'))),
              ]),
            ),
        ],
        if (r == null && !_failed) Padding(padding: const EdgeInsets.all(12), child: Text('Loading the payroll…', style: tkText(size: 14, color: t.muted))),
      ],
    );
  }
}

/// Opens a staff member's page from a payroll (kept for the lines that
/// name one).
Future<void> openPayrollStaff(BuildContext context, Auth auth, String user) => _push(context, StaffPersonPage(auth: auth, user: user, backLabel: 'Back to the run'));
