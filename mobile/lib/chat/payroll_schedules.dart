// Payroll settings and the tax and pension schedules (P4b-5,
// docs/spec/chat-pay-people.md): how the payroll is set up in Payrolla,
// read-only with Change in Payrolla (GET /v1/payroll/settings); Tax and
// pension, each payroll's PAYE, pension, NHF and NHIS schedules (GET
// /v1/payroll/schedules); and one schedule with a line per person, its
// pension PINs and PFAs masked until a passkey shows them, and Download as
// a spreadsheet handed to the phone's share sheet (POST …/reveal,
// …/download). Read from Payrolla when opened and kept nowhere on the
// phone, as payslips are.

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../app/links.dart' show openOutside;
import '../app/passkey.dart' show passkeyProof;
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/list.dart' show TkEmpty;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show openOrSay, showTkToast;
import '../kit/page.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'attachments.dart' show handOffFile;
import 'cards.dart' show CardPill;
import 'words.dart' show CardTone, naira;

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Payroll → Tax and pension.
Future<void> openPayrollSchedules(BuildContext context, Auth auth, {String backLabel = 'Back to Payroll'}) =>
    _push(context, PayrollSchedulesPage(auth: auth, backLabel: backLabel));

/// Payroll settings → How payroll is set up.
Future<void> openPayrollSetup(BuildContext context, Auth auth, {String backLabel = 'Back to Payroll settings'}) =>
    _push(context, PayrollSetupPage(auth: auth, backLabel: backLabel));

/// "3 people".
String schedulePeople(int n) => n == 1 ? '1 person' : '$n people';

/// A schedule's row: "Due 10 Dec · 3 people".
String scheduleSub(PayrollSchedule s) => [s.when ?? s.stateWords, schedulePeople(s.people)].join(' · ');

CardTone scheduleTone(PayrollSchedule s) => switch (s.state) { 'paid' => CardTone.lime, 'to_pay' => CardTone.warn, _ => CardTone.plain };

/// Schedules by payroll month, in the order given (newest first).
List<({String words, List<PayrollSchedule> schedules})> schedulesByMonth(List<PayrollSchedule> list) {
  final out = <({String period, String words, List<PayrollSchedule> schedules})>[];
  for (final s in list) {
    if (out.isNotEmpty && out.last.period == s.period) {
      out.last.schedules.add(s);
    } else {
      out.add((period: s.period, words: s.periodWords, schedules: [s]));
    }
  }
  return [for (final m in out) (words: m.words, schedules: m.schedules)];
}

/// Payrolla hasn't allowed Taskiem to read the schedules.
bool scopeRefused(Object? e) => e is ApiError && e.body['code'] == 'scope_required';

Widget _refused(BuildContext context, Object e, String what) {
  final look = TkLook.of(context);
  if (scopeRefused(e)) {
    return TkCallout(leading: TkSquare('b-lock', color: look.orange), title: 'Payrolla hasn’t allowed this', sub: errorWords(e), subLines: 4, color: look.orange);
  }
  return TkCallout(leading: TkSquare('b-alert', color: look.red), title: '$what did not load', sub: errorWords(e), subLines: 4, color: look.red);
}

/// How the payroll is set up in Payrolla: read-only, with Change in
/// Payrolla.
class PayrollSetupPage extends StatefulWidget {
  const PayrollSetupPage({super.key, required this.auth, this.backLabel = 'Back to Payroll settings'});
  final Auth auth;
  final String backLabel;

  @override
  State<PayrollSetupPage> createState() => _PayrollSetupPageState();
}

class _PayrollSetupPageState extends State<PayrollSetupPage> {
  GetPayrollSettingsResponse? _view;
  Object? _error;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final v = await widget.auth.api.getPayrollSettings();
      if (mounted) setState(() => (_view = v, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  String _icon(PayrollSettingRow r) => switch (r.key) {
    'pay_frequency' => 'b-calendar',
    'tax_state' => 'b-pin',
    'separation_of_duties' => 'b-shield',
    'funding' => 'b-bank',
    _ => r.warn == true ? 'b-alert' : 'b-receipt',
  };

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final s = _view?.settings;
    final change = s?.changeUrl;
    return TkPageScreen(
      title: 'How payroll is set up',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Read from Payrolla when you open this. Change any of it in Payrolla.',
      onRefresh: _load,
      children: [
        if (_error != null) _refused(context, _error!, 'The setup'),
        if (s == null && _error == null) const TkSkeleton(rows: 4, label: 'Loading…'),
        if (s != null) ...[
          TkSection(
            title: 'Your payroll',
            inset: 56,
            children: [
              for (final r in s.rows)
                TkListRow(
                  title: r.label,
                  sub: r.value,
                  subLines: 3,
                  icon: _icon(r),
                  square: r.warn == true ? look.orange : look.teal,
                  chevron: false,
                ),
            ],
          ),
          if (s.notSetUp != null)
            TkCallout(leading: TkSquare('b-alert', color: look.orange), title: 'No deductions yet', sub: s.notSetUp!, subLines: 3, color: look.orange),
          if (s.deductions.isNotEmpty)
            TkSection(
              title: 'Deductions in force',
              inset: 56,
              children: [
                for (final r in s.deductions)
                  TkListRow(title: r.label, sub: r.value, subLines: 2, icon: 'b-money', square: r.on == true ? look.lime : look.violet, chevron: false),
              ],
            ),
          if (change != null && change.isNotEmpty)
            Align(
              alignment: AlignmentDirectional.centerStart,
              child: TkSmallButton(
                label: 'Change in Payrolla',
                primary: true,
                onPressed: () => unawaited(openOrSay(context, () => openOutside(Uri.parse(change)), 'Payrolla could not be opened.')),
              ),
            ),
        ],
      ],
    );
  }
}

/// Payroll → Tax and pension: each payroll's schedules, newest first.
class PayrollSchedulesPage extends StatefulWidget {
  const PayrollSchedulesPage({super.key, required this.auth, this.backLabel = 'Back to Payroll'});
  final Auth auth;
  final String backLabel;

  @override
  State<PayrollSchedulesPage> createState() => _PayrollSchedulesPageState();
}

class _PayrollSchedulesPageState extends State<PayrollSchedulesPage> {
  List<PayrollSchedule>? _list;
  Object? _error;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final v = await widget.auth.api.listPayrollSchedules();
      if (mounted) setState(() => (_list = v.schedules, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final list = _list;
    return TkPageScreen(
      title: 'Tax and pension',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'The PAYE, pension, NHF and NHIS to pay for each payroll, worked out by Payrolla once the payroll is locked.',
      onRefresh: _load,
      children: [
        if (_error != null) _refused(context, _error!, 'Tax and pension'),
        if (list == null && _error == null) const TkSkeleton(rows: 4, label: 'Loading…'),
        if (list != null && list.isEmpty)
          const TkEmpty(icon: 'b-receipt', title: 'Nothing to pay yet', line: 'Tax and pension are worked out when a payroll is locked.'),
        if (list != null)
          for (final m in schedulesByMonth(list))
            TkSection(
              title: m.words,
              inset: 56,
              children: [
                for (final s in m.schedules)
                  TkListRow(
                    title: s.title,
                    sub: scheduleSub(s),
                    subLines: 1,
                    icon: s.kind == 'pension' ? 'b-people' : 'b-receipt',
                    square: s.kind == 'pension' ? look.violet : s.kind == 'paye' ? look.teal : look.blue,
                    amount: naira(s.totalKobo),
                    status: s.stateWords,
                    statusTone: scheduleTone(s),
                    semanticsLabel: '${s.title}, ${s.periodWords}: ${naira(s.totalKobo)}, ${scheduleSub(s)}',
                    onTap: () => unawaited(_push(context, PayrollSchedulePage(auth: widget.auth, id: s.id, summary: s))),
                  ),
              ],
            ),
      ],
    );
  }
}

/// One schedule: a line per person; pension PINs and PFAs masked until a
/// passkey shows them; Download as a spreadsheet.
class PayrollSchedulePage extends StatefulWidget {
  const PayrollSchedulePage({super.key, required this.auth, required this.id, this.summary, this.backLabel = 'Back to Tax and pension'});
  final Auth auth;
  final String id;
  final PayrollSchedule? summary;
  final String backLabel;

  @override
  State<PayrollSchedulePage> createState() => _PayrollSchedulePageState();
}

class _PayrollSchedulePageState extends State<PayrollSchedulePage> {
  PayrollScheduleDetail? _d;
  Object? _error;
  bool _busy = false;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final d = await _auth.api.getPayrollSchedule(id: widget.id);
      if (mounted) setState(() => (_d = d, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<PasskeyCredential> _passkey() async => PasskeyCredential.fromJson(await passkeyProof(_auth, 'payroll.reveal', widget.id));

  Future<void> _reveal() async {
    setState(() => _busy = true);
    try {
      final full = await _auth.api.revealPayrollSchedule(id: widget.id, body: RevealPayrollScheduleRequest(passkey: await _passkey()));
      if (mounted) setState(() => _d = full);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _download() async {
    final pension = _d?.schedule.kind == 'pension';
    setState(() => _busy = true);
    try {
      final r = await _auth.api.downloadPayrollSchedule(id: widget.id, body: DownloadPayrollScheduleRequest(passkey: pension ? await _passkey() : null));
      await handOffFile(Uint8List.fromList(utf8.encode(r.csv)), r.filename, 'text/csv', null);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final d = _d;
    final s = d?.schedule ?? widget.summary;
    final pension = s?.kind == 'pension';
    return TkPageScreen(
      title: s == null ? 'Schedule' : '${s.title}, ${s.periodWords}',
      titleSize: 22,
      backLabel: widget.backLabel,
      description: 'Each person’s part. Pension PINs are hidden until you confirm with your passkey; nothing of this is kept on the phone.',
      onRefresh: _load,
      children: [
        if (s != null)
          TkTopCard(
            semanticsLabel: '${s.title}: ${naira(s.totalKobo)} for ${schedulePeople(s.people)}, ${s.when ?? s.stateWords}',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow(s.title, color: look.limeInk, icon: 'b-receipt', trailing: s.periodWords),
                const SizedBox(height: 8),
                FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text(naira(s.totalKobo), style: tkFigure(context, size: 38))),
                const SizedBox(height: 2),
                Text('${schedulePeople(s.people)} · to ${s.to}', style: tkText(size: 14, lineHeight: 1.35, color: t.muted)),
                const SizedBox(height: 12),
                Align(alignment: AlignmentDirectional.centerStart, child: CardPill(s.when ?? s.stateWords, scheduleTone(s))),
              ],
            ),
          ),
        if (_error != null) _refused(context, _error!, 'This schedule'),
        if (d == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading…'),
        if (d != null) ...[
          TkButtonRow([
            if (pension && d.masked)
              TkSmallButton(label: 'Show PINs', icon: 'b-eye', semantics: 'Show pension PINs with your passkey', onPressed: _busy ? null : () => unawaited(_reveal())),
            TkSmallButton(label: 'Download', icon: 'b-share', semantics: 'Download as a spreadsheet', onPressed: _busy ? null : () => unawaited(_download())),
          ]),
          TkSection(
            title: 'Each person',
            children: [
              for (final l in d.lines)
                TkListRow(
                  title: l.name,
                  letters: l.name,
                  sub: pension ? 'PIN ${l.pin ?? 'not on file'} · PFA ${l.pfa ?? 'not on file'}' : '',
                  subLines: 2,
                  amount: naira(l.amountKobo),
                  chevron: false,
                ),
            ],
          ),
        ],
      ],
    );
  }
}
