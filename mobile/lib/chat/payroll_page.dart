// Payroll in Taskiem, on Payrolla (P4a; docs/spec/build-plan-saved-tools-
// payments.md P4): payroll is Payrolla's. Payrolla calculates, approves and
// pays it; Taskiem shows it in its own screens through Payrolla's API
// (GET /v1/payroll, /v1/payroll/runs/{run}, /v1/payroll/staff) and never
// approves or pays from here. The runs, newest first, with the latest
// one's net pay on top; a run's lines, those not paid first, and
// Payrolla's approval link while it waits; and the staff, each with who
// they are in Payrolla, confirmed by an owner (PUT /v1/staff/{user}/
// payrolla). Shown to those with payroll.read (the owner's alone unless
// given): it says what people earn.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../app/links.dart' show openOutside;
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/list.dart' show TkEmpty;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show openOrSay, showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkCta;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'cards.dart' show CardPill;
import 'groups.dart' show askSure, pickOneOf;
import 'invoices_page.dart' show shortDay;
import 'payroll_settings.dart' show PayrollSettingsPage;
import 'team_pages.dart' show StaffPersonPage;
import 'words.dart' show CardTone, naira;

Map<String, dynamic>? _mapOrNull(Object? v) => v is Map ? v.cast<String, dynamic>() : null;
String _str(Object? v) => v is String ? v : '';

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Whether [auth] is shown payroll: it says what people earn.
bool maySeePayroll(Auth auth) => auth.me?.personal != true && auth.can('payroll.read');

/// Opens Payroll.
Future<void> openPayroll(BuildContext context, Auth auth, {String backLabel = 'Back'}) => _push(context, PayrollPage(auth: auth, backLabel: backLabel));

/// A run's status in plain words, with its pill's tone.
({String words, CardTone tone}) payrollStatus(String status) => switch (status) {
  'DRAFT' => (words: 'Draft', tone: CardTone.plain),
  'COMPUTED' => (words: 'Waiting for approval', tone: CardTone.warn),
  'APPROVED' => (words: 'Approved', tone: CardTone.plain),
  'LOCKED' => (words: 'Locked', tone: CardTone.plain),
  'DISBURSING' => (words: 'Paying', tone: CardTone.plain),
  'COMPLETED' => (words: 'Paid', tone: CardTone.lime),
  'PARTIALLY_COMPLETED' => (words: 'Partly paid', tone: CardTone.warn),
  'CANCELLED' => (words: 'Cancelled', tone: CardTone.plain),
  'FAILED' => (words: 'Failed', tone: CardTone.danger),
  _ => (words: status.isEmpty ? 'Draft' : status[0] + status.substring(1).toLowerCase().replaceAll('_', ' '), tone: CardTone.plain),
};

/// One person's pay in plain words: paid, waiting, not paid, returned.
({String words, CardTone tone}) payrollLineState(String state) => switch (state) {
  'paid' => (words: 'Paid', tone: CardTone.lime),
  'failed' => (words: 'Not paid', tone: CardTone.danger),
  'returned' => (words: 'Returned', tone: CardTone.danger),
  _ => (words: 'Waiting', tone: CardTone.plain),
};

const _monthNames = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

DateTime? _ymd(String s) => s.length < 10 ? null : DateTime.tryParse('${s.substring(0, 10)}T12:00:00Z');

/// A run's period: "November 2026" for a whole month, "1 – 15 November
/// 2026" for part of one, else from one day to the other.
/// [short]: the month in three letters ("Nov 2026"), for a row beside its
/// amount and status.
String payrollPeriod(String start, String end, {bool short = false}) {
  final a = _ymd(start), b = _ymd(end);
  String month(DateTime d) => short ? _monthNames[d.month - 1].substring(0, 3) : _monthNames[d.month - 1];
  if (a == null) return 'Payroll';
  if (b == null) return '${month(a)} ${a.year}';
  if (a.year == b.year && a.month == b.month) {
    final last = DateTime.utc(a.year, a.month + 1, 0).day;
    if (a.day == 1 && b.day == last) return '${month(a)} ${a.year}';
    return '${a.day} – ${b.day} ${month(a)} ${a.year}';
  }
  final y = a.year == b.year ? '' : ' ${a.year}';
  return '${a.day} ${_monthNames[a.month - 1].substring(0, 3)}$y – ${b.day} ${_monthNames[b.month - 1].substring(0, 3)} ${b.year}';
}

String peopleCount(int n) => n == 1 ? '1 person' : '$n people';

/// A refusal from Payrolla, in a callout's title.
String _refusalTitle(Object e, String otherwise) => switch (e is ApiError ? _str(e.body['code']) : '') {
  'scope_required' => 'Payrolla’s key cannot read payroll',
  'reconnect' => 'Reconnect Payrolla',
  'not_connected' => 'Payrolla is not connected',
  'payrolla_down' => 'Payrolla did not answer',
  _ => otherwise,
};

/// The server's words whole (the app's font has no arrow: "Settings, Connected apps").
Widget _refusal(BuildContext context, Object e, String otherwise) {
  final look = TkLook.of(context);
  final down = e is ApiError && e.body['code'] == 'payrolla_down';
  final color = down ? look.orange : look.red;
  return TkCallout(leading: TkSquare(down ? 'b-clock' : 'b-alert', color: color), title: _refusalTitle(e, otherwise), sub: errorWords(e).replaceAll(' → ', ', '), subLines: 6, color: color);
}

// --- Payroll: the runs ---

class PayrollPage extends StatefulWidget {
  const PayrollPage({super.key, required this.auth, this.backLabel = 'Back'});
  final Auth auth;
  final String backLabel;

  @override
  State<PayrollPage> createState() => _PayrollPageState();
}

class _PayrollPageState extends State<PayrollPage> {
  GetPayrollResponse? _got;
  List<PayrollRun> _runs = const [];
  String? _next;
  Object? _error;
  bool _more = false;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await _auth.api.getPayroll();
      if (mounted) setState(() => (_got = r, _runs = r.runs, _next = r.nextCursor, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _showMore() async {
    final cursor = _next;
    if (cursor == null || _more) return;
    setState(() => _more = true);
    try {
      final r = await _auth.api.getPayroll(cursor: cursor);
      if (mounted) setState(() => (_runs = [..._runs, ...r.runs.where((x) => !_runs.any((y) => y.runId == x.runId))], _next = r.nextCursor));
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _more = false);
    }
  }

  Future<void> _open(PayrollRun r) => _push(context, PayrollRunPage(auth: _auth, run: r.runId, summary: r));

  Widget _row(PayrollRun r) {
    final s = payrollStatus(r.status);
    final period = payrollPeriod(r.periodStart, r.periodEnd, short: true);
    final day = shortDay(r.payDate);
    final sub = [if (day.isNotEmpty) 'Pay day $day', peopleCount(r.employeeCount)].join(' · ');
    return TkListRow(
      title: period,
      icon: 'b-calendar',
      sub: sub,
      subLines: 2,
      titleLines: 1,
      amount: naira(r.totalNetKobo),
      status: s.words,
      statusTone: s.tone,
      waiting: r.awaitingApproval,
      semanticsLabel: '$period: $sub, ${naira(r.totalNetKobo)}, ${s.words}. Open',
      onTap: () => unawaited(_open(r)),
    );
  }

  Widget _top(BuildContext context, PayrollRun r, String company) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final s = payrollStatus(r.status);
    final period = payrollPeriod(r.periodStart, r.periodEnd);
    final day = shortDay(r.payDate);
    return TkTopCard(
      semanticsLabel: 'Latest run, $period: ${naira(r.totalNetKobo)} net to ${peopleCount(r.employeeCount)}, ${s.words}. Open',
      onTap: () => unawaited(_open(r)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow('Latest run', color: look.limeInk, icon: 'b-calendar', trailing: company.isEmpty ? null : company),
          const SizedBox(height: 8),
          FittedBox(
            fit: BoxFit.scaleDown,
            alignment: Alignment.centerLeft,
            child: Text(naira(r.totalNetKobo), style: tkFigure(context, size: 38)),
          ),
          const SizedBox(height: 2),
          Text(
            ['$period, net', peopleCount(r.employeeCount), if (day.isNotEmpty) 'pay day $day'].join(' · '),
            style: tkText(size: 14, lineHeight: 1.35, color: t.muted),
          ),
          const SizedBox(height: 12),
          Align(alignment: Alignment.centerLeft, child: CardPill(s.words, s.tone)),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final got = _got;
    final latest = _runs.firstOrNull;
    return TkPageScreen(
      title: 'Payroll',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Payroll is Payrolla’s: it works out, approves and pays salaries. Taskiem shows its runs here, and who each of your staff is in Payrolla. '
          'Nothing here pays anyone.',
      onRefresh: _load,
      children: [
        if (_error != null) _refusal(context, _error!, 'Payroll did not load'),
        if (got == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading the payroll…'),
        if (got != null && !got.connected && got.ended == true)
          TkCallout(
            leading: TkSquare('b-alert', color: look.red),
            title: got.isOwner == true ? 'Payrolla is no longer connected' : 'Payroll isn’t connected right now',
            sub: got.isOwner == true ? 'Reconnect Payrolla to see and run your payroll here.' : 'The owner has been told.',
            subLines: 4,
            color: look.red,
          )
        else if (got != null && !got.connected)
          TkCallout(
            leading: TkSquare('b-calendar', color: look.violet),
            title: 'Payroll runs in Payrolla',
            sub: 'Connect Payrolla to see its runs here: in one step, or with the key from Payrolla’s Connected apps.',
            subLines: 4,
            color: look.violet,
          ),
        if (got != null && !got.connected)
          Align(
            alignment: AlignmentDirectional.centerStart,
            child: TkSmallButton(
              label: got.ended == true ? 'Reconnect Payrolla' : 'Connect Payrolla',
              primary: true,
              onPressed: () => unawaited(_push(context, PayrollSettingsPage(auth: _auth))),
            ),
          ),
        if (got != null && got.connected && got.auth == 'key' && got.isOwner == true)
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: 'Reconnect Payrolla',
            sub: 'Connect again in one step so you can run payroll here.',
            subLines: 3,
            color: look.orange,
            button: 'Reconnect Payrolla',
            onPressed: () => unawaited(_push(context, PayrollSettingsPage(auth: _auth))),
          ),
        if (got != null && got.connected) ...[
          if (got.test == true)
            TkCallout(leading: TkSquare('b-alert', color: look.orange), title: 'Payrolla’s test mode', sub: 'These runs are practice: no one is paid.', color: look.orange),
          if (latest != null) _top(context, latest, got.company?.name ?? ''),
          if (_runs.isEmpty)
            const TkEmpty(icon: 'b-calendar', title: 'No payroll runs yet', line: 'Runs made in Payrolla show here.')
          else
            TkSection(title: 'Runs', inset: 56, children: [for (final r in _runs) _row(r)]),
          if (_next != null)
            Center(
              child: TkSmallButton(label: _more ? 'Loading…' : 'Show more', semantics: 'Show more runs', onPressed: _more ? null : () => unawaited(_showMore())),
            ),
          TkSection(
            inset: 56,
            children: [
              TkListRow(
                title: 'Staff in Payrolla',
                sub: 'Who each of your staff is there',
                subLines: 1,
                icon: 'b-people',
                square: look.teal,
                onTap: () => unawaited(_push(context, PayrollStaffPage(auth: _auth, backLabel: 'Back to Payroll'))),
              ),
              TkListRow(
                title: 'Payroll settings',
                sub: 'Connecting, and who approves',
                subLines: 1,
                icon: 'b-tools',
                square: look.violet,
                onTap: () => unawaited(_push(context, PayrollSettingsPage(auth: _auth))),
              ),
            ],
          ),
        ],
      ],
    );
  }
}

// --- one run ---

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

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.getPayrollRun(run: widget.run);
      if (mounted) setState(() => (_got = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  static const _order = {'failed': 0, 'returned': 1, 'waiting': 2, 'paid': 3};

  Widget _line(BuildContext context, GetPayrollRunResponseLinesItem l) {
    final s = payrollLineState(l.state);
    final staff = _mapOrNull(l.staff);
    final staffId = _str(staff?['user_id']);
    final staffName = _str(staff?['name']);
    final reason = (l.reason ?? '').trim();
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
      status: s.words,
      statusTone: s.tone,
      waiting: l.state == 'failed' || l.state == 'returned',
      chevron: staffId.isNotEmpty,
      semanticsLabel: '${l.name}: ${naira(l.netKobo)}, ${s.words}${reason.isNotEmpty ? '. $reason' : ''}${staffId.isNotEmpty ? '. Open their staff page' : ''}',
      onTap: staffId.isEmpty ? null : () => unawaited(_push(context, StaffPersonPage(auth: widget.auth, user: staffId, backLabel: 'Back to the run'))),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final got = _got;
    final r = got?.run.summary ?? widget.summary;
    final s = payrollStatus(r?.status ?? '');
    final lines = [...?got?.lines]..sort((a, b) => (_order[a.state] ?? 2) - (_order[b.state] ?? 2));
    final notPaid = lines.where((l) => l.state == 'failed' || l.state == 'returned').toList();
    final rest = lines.where((l) => l.state != 'failed' && l.state != 'returned').toList();
    final c = got?.counts;
    final day = r == null ? '' : shortDay(r.payDate);
    final approval = got?.approvalUrl;
    final counts = c == null
        ? const <String>[]
        : [
            if (c.paid > 0) '${c.paid} paid',
            if (c.waiting > 0) '${c.waiting} waiting',
            if (c.failed > 0) '${c.failed} not paid',
            if (c.returned > 0) '${c.returned} returned',
          ];
    return TkPageScreen(
      title: r == null ? 'Payroll run' : payrollPeriod(r.periodStart, r.periodEnd),
      titleSize: 22,
      backLabel: widget.backLabel,
      description: 'A run as Payrolla has it: each person’s net pay and whether it arrived. Approving and paying are done in Payrolla.',
      onRefresh: _load,
      children: [
        if (_error != null) _refusal(context, _error!, 'The run did not load'),
        if (r != null)
          TkTopCard(
            semanticsLabel: '${naira(r.totalNetKobo)} net to ${peopleCount(r.employeeCount)}${day.isEmpty ? '' : ', pay day $day'}. ${s.words}',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Net pay', color: look.limeInk, icon: 'b-cash', trailing: day.isEmpty ? null : 'Pay day $day'),
                const SizedBox(height: 8),
                FittedBox(
                  fit: BoxFit.scaleDown,
                  alignment: Alignment.centerLeft,
                  child: Text(naira(r.totalNetKobo), style: tkFigure(context, size: 38)),
                ),
                const SizedBox(height: 2),
                Text(
                  [peopleCount(r.employeeCount), if (r.totalGrossKobo > 0) '${naira(r.totalGrossKobo)} before deductions'].join(' · '),
                  style: tkText(size: 14, lineHeight: 1.35, color: t.muted),
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    CardPill(s.words, s.tone),
                    if (counts.isNotEmpty) ...[
                      const SizedBox(width: 10),
                      Expanded(child: Text(counts.join(' · '), maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 13, color: t.muted))),
                    ],
                  ],
                ),
              ],
            ),
          ),
        if (approval != null && approval.isNotEmpty)
          TkCta(
            label: 'Review and approve in Payrolla',
            icon: 'b-arrow',
            iconAfter: true,
            onTap: () => unawaited(openOrSay(context, () => openOutside(Uri.parse(approval)), 'Payrolla could not be opened.')),
          ),
        if (got == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading the run…'),
        if (got != null && lines.isEmpty) const TkEmpty(icon: 'b-people', title: 'No one in this run yet', line: 'Payrolla adds each person’s pay as it works the run out.'),
        if (notPaid.isNotEmpty) TkSection(title: 'Not paid', count: notPaid.length, children: [for (final l in notPaid) _line(context, l)]),
        if (rest.isNotEmpty) TkSection(title: notPaid.isEmpty ? 'People' : 'Everyone else', children: [for (final l in rest) _line(context, l)]),
      ],
    );
  }
}

// --- staff in Payrolla ---

class PayrollStaffPage extends StatefulWidget {
  const PayrollStaffPage({super.key, required this.auth, this.backLabel = 'Back'});
  final Auth auth;
  final String backLabel;

  @override
  State<PayrollStaffPage> createState() => _PayrollStaffPageState();
}

class _PayrollStaffPageState extends State<PayrollStaffPage> {
  GetPayrollStaffResponse? _got;
  Object? _error;
  String? _busy;

  Auth get _auth => widget.auth;

  bool get _owner => (_auth.me?.roles as List?)?.contains('owner') ?? false;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await _auth.api.getPayrollStaff();
      if (mounted) setState(() => (_got = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  /// [employee] linked to the staff member (null unlinks them).
  Future<void> _put(GetPayrollStaffResponseStaffItem s, String? employee, String said) async {
    setState(() => _busy = s.userId);
    try {
      await _auth.api.putStaffPayrolla(user: s.userId, body: PutStaffPayrollaRequest(employeeId: employee));
      if (!mounted) return;
      showTkToast(context, said);
      await _load();
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = null);
    }
  }

  Future<void> _choose(GetPayrollStaffResponseStaffItem s) async {
    final got = _got;
    if (got == null || _busy != null) return;
    final now = _str(_mapOrNull(s.payrolla)?['employee_id']);
    final free = [
      for (final e in got.employees)
        if (e.linkedTo == null || e.linkedTo == s.userId) e,
    ];
    if (free.isEmpty) {
      showTkToast(context, 'Everyone in Payrolla is linked to someone already.', error: true);
      return;
    }
    final id = await pickOneOf(
      context,
      title: 'Who is ${s.name} in Payrolla?',
      icon: 'b-people',
      value: now.isEmpty ? null : now,
      searchHint: free.length > 8 ? 'Search Payrolla’s employees' : null,
      choices: [
        for (final e in free) (e.id, e.name, [e.department ?? '', if (e.status == 'TERMINATED') 'Left'].where((w) => w.isNotEmpty).join(' · ')),
      ],
    );
    if (id == null || id == now || !mounted) return;
    final e = free.firstWhere((e) => e.id == id);
    await _put(s, id, 'Linked: ${s.name} is ${e.name} in Payrolla');
  }

  Future<void> _unlink(GetPayrollStaffResponseStaffItem s, String employee) async {
    final yes = await askSure(
      context,
      title: 'Unlink ${s.name} from Payrolla?',
      body: 'Payroll stops naming them as $employee, and Payrolla’s messages about their pay stop reaching their chat. Nothing changes in Payrolla.',
      ok: 'Unlink',
      cancel: 'Keep it',
      danger: true,
    );
    if (!yes || !mounted) return;
    await _put(s, null, 'Unlinked ${s.name}');
  }

  Widget _row(BuildContext context, GetPayrollStaffResponseStaffItem s) {
    final linked = _mapOrNull(s.payrolla);
    final suggested = linked == null ? _mapOrNull(s.suggested) : null;
    final busy = _busy != null;
    final owner = _owner;
    if (linked != null) {
      final emp = _str(linked['name']).isEmpty ? 'someone' : _str(linked['name']);
      final by = _str(linked['linked_by_name']);
      return TkListRow(
        title: s.name,
        letters: s.name,
        sub: ['In Payrolla: $emp', if (by.isNotEmpty) 'linked by $by'].join(' · '),
        subLines: 2,
        titleLines: 1,
        chevron: false,
        semanticsLabel: '${s.name}: $emp in Payrolla${owner ? '. Change' : ''}',
        onTap: owner && !busy ? () => unawaited(_choose(s)) : null,
        trailing: owner ? TkSmallButton(label: 'Unlink', semantics: 'Unlink ${s.name} from Payrolla', onPressed: busy ? null : () => unawaited(_unlink(s, emp))) : null,
      );
    }
    if (suggested != null) {
      final emp = _str(suggested['name']);
      final by = _str(suggested['by']) == 'phone' ? 'same phone' : 'same email';
      return TkListRow(
        title: s.name,
        letters: s.name,
        sub: 'Looks like $emp ($by)',
        subLines: 2,
        titleLines: 1,
        chevron: false,
        semanticsLabel: '${s.name}: looks like $emp in Payrolla, $by${owner ? '. Choose someone else' : ''}',
        onTap: owner && !busy ? () => unawaited(_choose(s)) : null,
        trailing: owner
            ? TkSmallButton(
                label: 'Confirm',
                primary: true,
                semantics: 'Confirm ${s.name} is $emp',
                onPressed: busy ? null : () => unawaited(_put(s, _str(suggested['employee_id']), 'Linked: ${s.name} is $emp in Payrolla')),
              )
            : null,
      );
    }
    return TkListRow(
      title: s.name,
      letters: s.name,
      sub: 'Not linked to anyone in Payrolla',
      subLines: 2,
      titleLines: 1,
      chevron: false,
      semanticsLabel: '${s.name}: not linked in Payrolla${owner ? '. Choose' : ''}',
      onTap: owner && !busy ? () => unawaited(_choose(s)) : null,
      trailing: owner ? TkSmallButton(label: 'Choose', semantics: 'Choose who ${s.name} is in Payrolla', onPressed: busy ? null : () => unawaited(_choose(s))) : null,
    );
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final got = _got;
    final staff = got?.staff ?? const <GetPayrollStaffResponseStaffItem>[];
    final toConfirm = staff.where((s) => s.payrolla == null && s.suggested != null).toList();
    final none = staff.where((s) => s.payrolla == null && s.suggested == null).toList();
    final linked = staff.where((s) => s.payrolla != null).toList();
    final free = got?.employees.where((e) => e.linkedTo == null && e.status != 'TERMINATED').length ?? 0;
    return TkPageScreen(
      title: 'Staff in Payrolla',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Who each of your staff is in Payrolla: Payroll names them, and Payrolla’s messages about their pay reach their own chat. '
          'Taskiem suggests the employee with the same email or phone; an owner confirms each one. Nothing changes in Payrolla.',
      onRefresh: _load,
      children: [
        if (_error != null) _refusal(context, _error!, 'The staff did not load'),
        if (got == null && _error == null) const TkSkeleton(rows: 4, label: 'Loading your staff…'),
        if (got != null && !_owner)
          TkCallout(leading: TkSquare('b-lock', color: look.violet), title: 'An owner links staff to Payrolla', color: look.violet),
        if (toConfirm.isNotEmpty) TkSection(title: 'To confirm', count: toConfirm.length, children: [for (final s in toConfirm) _row(context, s)]),
        if (none.isNotEmpty) TkSection(title: 'Not linked', children: [for (final s in none) _row(context, s)]),
        if (linked.isNotEmpty) TkSection(title: 'Linked', children: [for (final s in linked) _row(context, s)]),
        if (got != null && staff.isEmpty) const TkEmpty(icon: 'b-people', title: 'No staff yet', line: 'Add your staff under Team first.'),
        if (got != null && free > 0)
          TkCallout(
            leading: TkSquare('b-people', color: look.teal),
            title: free == 1 ? '1 employee in Payrolla is not linked' : '$free employees in Payrolla are not linked',
            sub: 'Add them to your staff under Team, then link them here.',
            color: look.teal,
          ),
      ],
    );
  }
}
