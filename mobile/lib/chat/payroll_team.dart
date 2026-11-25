// Staff and pay from Taskiem (P4b-2, docs/spec/chat-pay-people.md):
// Payroll → Team (everyone in Payrolla, pay to those who may see it), Add
// someone with their account checked, Change (name, email, department,
// pay from a date; never the phone or a job title), a new salary account
// held for another approver, End employment (today); Payroll → Account
// changes, approved with Payrolla's code or rejected with a note; and My
// pay, where a staff member asks for their own new account and types the
// code Payrolla texts them. Payrolla keeps the pay and the accounts; an
// account number and a code go to Payrolla once and are kept nowhere.

import 'dart:async';

import 'package:flutter/material.dart' show showDatePicker;
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords, nairaKobo;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart';
import '../screens/sign_in.dart' show CodeBoxes;
import '../theme/look.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart' show CardPill;
import 'groups.dart' show askSure, pickAction, pickOneOf;
import 'payroll_settings.dart' show payrollKey;
import 'payslip.dart' show MyPayslipsSection;
import 'words.dart' show CardTone, naira;

Map<String, dynamic>? _map(Object? v) => v is Map ? v.cast<String, dynamic>() : null;
String _str(Object? v) => v is String ? v : '';

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Opens Payroll → Team.
Future<void> openPayrollTeam(BuildContext context, Auth auth, {String backLabel = 'Back to Payroll'}) => _push(context, PayrollTeamPage(auth: auth, backLabel: backLabel));

/// Opens Payroll → Account changes.
Future<void> openPayrollChanges(BuildContext context, Auth auth, {String backLabel = 'Back to Payroll'}) =>
    _push(context, PayrollChangesPage(auth: auth, backLabel: backLabel));

/// Opens My pay.
Future<void> openMyPay(BuildContext context, Auth auth, {String backLabel = 'Back'}) => _push(context, MyPayPage(auth: auth, backLabel: backLabel));

/// "••6789".
String masked(String last4) => '••$last4';

/// "Guaranty Trust Bank ••6789", or that there is none.
String payrollBankLine(Map<String, dynamic>? b) => b == null ? 'No account yet' : '${_str(b['bank'])} ${masked(_str(b['last4']))}';

/// How a held change was asked for, in words.
String payrollViaWords(String via, String? askedBy) => switch (via) {
  'in_taskiem' => (askedBy ?? '').isEmpty ? 'Asked in Taskiem' : 'Asked by $askedBy in Taskiem',
  'by_employee' => 'Asked by the employee',
  _ => 'Asked in Payrolla',
};

/// A person's line on Team: their department, their account, and whether
/// they left or have a change waiting.
String payrollMemberSub(PayrollTeamMember m) => [
  if ((m.department ?? '').isNotEmpty) m.department!,
  payrollBankLine(_map(m.bank)),
  if (m.status == 'left') 'Has left',
  if (m.waitingChange != null) 'Account change waiting',
].join(' · ');

String _today() {
  final n = DateTime.now();
  String p(int v) => v.toString().padLeft(2, '0');
  return '${n.year}-${p(n.month)}-${p(n.day)}';
}

/// The banks a salary account can be at, read once.
List<ListPayrollBanksResponseBanksItem>? _banks;

/// Picks a bank from Payrolla's list, searched by name.
Future<ListPayrollBanksResponseBanksItem?> _pickBank(BuildContext context, Auth auth, String? value) async {
  final banks = _banks ??= (await auth.api.listPayrollBanks()).banks;
  if (!context.mounted) return null;
  final code = await pickOneOf(context, title: 'Bank', value: value, searchHint: 'Search banks', choices: [for (final b in banks) (b.code, b.name, '')]);
  return code == null ? null : banks.firstWhere((b) => b.code == code);
}

/// The bank and the account number, with the bank's name for the account
/// checked and shown before saving.
class _AccountFields extends StatefulWidget {
  const _AccountFields({required this.auth, required this.account, required this.bank, required this.onBank, this.check = true});
  final Auth auth;
  final TextEditingController account;
  final ListPayrollBanksResponseBanksItem? bank;
  final ValueChanged<ListPayrollBanksResponseBanksItem> onBank;
  final bool check;

  @override
  State<_AccountFields> createState() => _AccountFieldsState();
}

class _AccountFieldsState extends State<_AccountFields> {
  PayrollNameCheckResponse? _checked;
  String? _problem;
  bool _busy = false;

  String get _digits => widget.account.text.replaceAll(RegExp(r'[\s-]'), '');

  Future<void> _check() async {
    final b = widget.bank;
    if (b == null || _digits.length != 10) return;
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.payrollNameCheck(body: PayrollNameCheckRequest(bankCode: b.code, accountNumber: _digits));
      if (mounted) setState(() => _checked = r);
    } on Object catch (e) {
      if (mounted) setState(() => _problem = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final c = _checked;
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      TkFormGroup(title: 'Salary account', children: [
        TkKitField(
          label: 'Bank',
          field: false,
          child: TkPickBox(
            label: 'Bank',
            shown: widget.bank?.name ?? 'Choose the bank',
            onTap: () async {
              final b = await _pickBank(context, widget.auth, widget.bank?.code);
              if (b != null) {
                widget.onBank(b);
                setState(() => _checked = null);
              }
            },
          ),
        ),
        TkKitField(
          label: 'Account number',
          child: TkTextBox(key: const ValueKey('payroll-account'), controller: widget.account, digits: 10, size: 15, height: 46),
        ),
      ]),
      if (widget.check && c == null) ...[
        const SizedBox(height: 10),
        Align(
          alignment: AlignmentDirectional.centerStart,
          child: TkSmallButton(label: _busy ? 'Checking…' : 'Check the account', onPressed: _busy || widget.bank == null ? null : () => unawaited(_check())),
        ),
      ],
      if (c != null) ...[
        const SizedBox(height: 10),
        c.checked
            ? TkCallout(leading: TkSquare('b-check', color: look.lime), title: c.accountName ?? '', sub: 'The bank’s name for this account', color: look.lime)
            : TkCallout(leading: TkSquare('b-query', color: look.teal), title: 'Checked when you save', sub: c.words, subLines: 3, color: look.teal),
      ],
      if (_problem != null) ...[const SizedBox(height: 10), TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red)],
    ]);
  }
}

/// Add someone to payroll; from a staff member when [from] is given.
class PayrollAddSheet extends StatefulWidget {
  const PayrollAddSheet({super.key, required this.auth, this.from});
  final Auth auth;
  final GetPayrollTeamResponseStaffItem? from;

  @override
  State<PayrollAddSheet> createState() => _PayrollAddSheetState();
}

class _PayrollAddSheetState extends State<PayrollAddSheet> {
  final _first = TextEditingController();
  final _last = TextEditingController();
  final _phone = TextEditingController();
  final _email = TextEditingController();
  final _dept = TextEditingController();
  final _gross = TextEditingController();
  final _account = TextEditingController();
  ListPayrollBanksResponseBanksItem? _bank;
  final String _key = payrollKey();
  String? _problem;
  bool _busy = false;
  PayrollTeamMember? _done;

  @override
  void initState() {
    super.initState();
    final f = widget.from;
    if (f != null) {
      final words = f.name.trim().split(RegExp(r'\s+'));
      _first.text = words.length > 1 ? words.sublist(0, words.length - 1).join(' ') : f.name.trim();
      _last.text = words.length > 1 ? words.last : '';
      _phone.text = f.phone ?? '';
      _email.text = f.email ?? '';
    }
  }

  @override
  void dispose() {
    for (final c in [_first, _last, _phone, _email, _dept, _gross, _account]) {
      c.dispose();
    }
    super.dispose();
  }

  String? _missing() {
    if (_first.text.trim().isEmpty || _last.text.trim().isEmpty) return 'Give their first and last names.';
    if (_phone.text.trim().isEmpty && _email.text.trim().isEmpty) return 'Give a phone number or an email, for their payslip.';
    final g = nairaKobo(_gross.text);
    if (g == null || g <= 0) return 'Give their monthly pay.';
    if (_bank == null || _account.text.replaceAll(RegExp(r'[\s-]'), '').length != 10) return 'Choose their bank and type the 10-digit account number.';
    return null;
  }

  Future<void> _save() async {
    final missing = _missing();
    if (missing != null) {
      setState(() => _problem = missing);
      return;
    }
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.addPayrollEmployee(
        body: AddPayrollEmployeeRequest(
          key: _key,
          firstName: _first.text.trim(),
          lastName: _last.text.trim(),
          phone: _phone.text.trim().isEmpty ? null : _phone.text.trim(),
          email: _email.text.trim().isEmpty ? null : _email.text.trim(),
          department: _dept.text.trim().isEmpty ? null : _dept.text.trim(),
          startDate: _today(),
          pay: PayrollPayInput(grossKobo: nairaKobo(_gross.text)!, pensionable: true),
          bank: AddPayrollEmployeeRequestBank(bankCode: _bank!.code, accountNumber: _account.text.replaceAll(RegExp(r'[\s-]'), '')),
          staffUser: widget.from?.userId,
        ),
      );
      if (mounted) setState(() => _done = r.employee);
    } on Object catch (e) {
      if (mounted) setState(() => _problem = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final done = _done;
    if (done != null) {
      final b = _map(done.bank);
      return TkFormSheet(
        title: 'Added to payroll',
        action: 'Done',
        onAction: () => Navigator.of(context).pop(true),
        children: [
          TkCallout(leading: TkSquare('b-check', color: look.lime), title: _str(b?['account_name']), sub: '${done.name} is paid into ${payrollBankLine(b)}', subLines: 3, color: look.lime),
        ],
      );
    }
    return TkFormSheet(
      title: 'Add someone to payroll',
      crumb: widget.from?.name,
      action: _busy ? 'Adding…' : 'Add to payroll',
      onAction: _busy ? null : () => unawaited(_save()),
      children: [
        TkFormGroup(title: 'Who', children: [
          TkKitField(label: 'First name', child: TkTextBox(controller: _first, max: 80, size: 15, height: 46)),
          TkKitField(label: 'Last name', child: TkTextBox(controller: _last, max: 80, size: 15, height: 46)),
          TkKitField(label: 'Phone', hint: 'A phone or an email is needed, for their payslip.', child: TkTextBox(controller: _phone, phone: true, size: 15, height: 46)),
          TkKitField(label: 'Email', child: TkTextBox(controller: _email, plain: true, size: 15, height: 46)),
          TkKitField(label: 'Department', child: TkTextBox(controller: _dept, max: 80, size: 15, height: 46)),
        ]),
        TkFormGroup(title: 'Pay', children: [
          TkKitField(label: 'Monthly pay', child: TkTextBox(key: const ValueKey('payroll-gross'), controller: _gross, money: true, prefix: '₦', size: 15, height: 46)),
        ]),
        _AccountFields(auth: widget.auth, account: _account, bank: _bank, onBank: (b) => setState(() => _bank = b)),
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}

/// Change someone's details or their pay from a date.
class PayrollChangeSheet extends StatefulWidget {
  const PayrollChangeSheet({super.key, required this.auth, required this.member});
  final Auth auth;
  final PayrollTeamMember member;

  @override
  State<PayrollChangeSheet> createState() => _PayrollChangeSheetState();
}

class _PayrollChangeSheetState extends State<PayrollChangeSheet> {
  late final _dept = TextEditingController(text: widget.member.department ?? '');
  late final _email = TextEditingController(text: widget.member.email ?? '');
  late final _gross = TextEditingController(text: widget.member.pay == null ? '' : (widget.member.pay!.grossKobo ~/ 100).toString());
  DateTime _from = DateTime.now();
  final String _key = payrollKey();
  String? _problem;
  bool _busy = false;

  @override
  void dispose() {
    for (final c in [_dept, _email, _gross]) {
      c.dispose();
    }
    super.dispose();
  }

  String _ymd(DateTime d) => '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

  Future<void> _save() async {
    final m = widget.member;
    final gross = nairaKobo(_gross.text);
    final pay = gross != null && gross > 0 && gross != m.pay?.grossKobo ? PayrollPayInput(grossKobo: gross, from: _ymd(_from)) : null;
    final dept = _dept.text.trim() != (m.department ?? '') ? _dept.text.trim() : null;
    final email = _email.text.trim() != (m.email ?? '') ? _email.text.trim() : null;
    if (pay == null && dept == null && email == null) {
      setState(() => _problem = 'Nothing to change.');
      return;
    }
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.changePayrollEmployee(employee: m.id, body: ChangePayrollEmployeeRequest(key: _key, department: dept, email: email, pay: pay));
      if (mounted) Navigator.of(context).pop(r.words ?? 'Saved');
    } on Object catch (e) {
      if (mounted) setState(() => _problem = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final m = widget.member;
    return TkFormSheet(
      title: 'Change',
      crumb: m.name,
      action: _busy ? 'Saving…' : 'Save',
      onAction: _busy ? null : () => unawaited(_save()),
      children: [
        TkFormGroup(children: [
          TkKitField(label: 'Department', child: TkTextBox(key: const ValueKey('payroll-change-department'), controller: _dept, max: 80, size: 15, height: 46)),
          TkKitField(label: 'Email', child: TkTextBox(controller: _email, plain: true, size: 15, height: 46)),
        ]),
        TkFormGroup(title: 'Pay', children: [
          TkKitField(label: 'Monthly pay', child: TkTextBox(key: const ValueKey('payroll-change-gross'), controller: _gross, money: true, prefix: '₦', size: 15, height: 46)),
          TkKitField(
            label: 'New pay starts',
            field: false,
            child: TkPickBox(
              label: 'New pay starts',
              shown: _ymd(_from) == _today() ? 'Today' : _ymd(_from),
              onTap: () async {
                final now = DateTime.now();
                final d = await showDatePicker(context: context, initialDate: _from, firstDate: DateTime(now.year - 1), lastDate: DateTime(now.year + 2));
                if (d != null) setState(() => _from = d);
              },
            ),
          ),
        ]),
        TkCallout(
          leading: TkSquare('b-phone', color: look.teal),
          title: 'Phone: ${(m.phone ?? '').isEmpty ? 'none' : m.phone}',
          sub: 'A phone number is changed in Payrolla. Payrolla keeps no job title.',
          subLines: 3,
          color: look.teal,
        ),
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}

/// A new salary account for someone, held for another approver.
class PayrollBankSheet extends StatefulWidget {
  const PayrollBankSheet({super.key, required this.auth, required this.member});
  final Auth auth;
  final PayrollTeamMember member;

  @override
  State<PayrollBankSheet> createState() => _PayrollBankSheetState();
}

class _PayrollBankSheetState extends State<PayrollBankSheet> {
  final _account = TextEditingController();
  ListPayrollBanksResponseBanksItem? _bank;
  final String _key = payrollKey();
  String? _problem;
  bool _busy = false;

  @override
  void dispose() {
    _account.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final digits = _account.text.replaceAll(RegExp(r'[\s-]'), '');
    if (_bank == null || digits.length != 10) {
      setState(() => _problem = 'Choose the bank and type the 10-digit account number.');
      return;
    }
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.askPayrollBankChange(employee: widget.member.id, body: AskPayrollBankChangeRequest(key: _key, bankCode: _bank!.code, accountNumber: digits));
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
    final b = _map(widget.member.bank);
    return TkFormSheet(
      title: 'New salary account',
      crumb: widget.member.name,
      action: _busy ? 'Asking…' : 'Ask for the change',
      onAction: _busy ? null : () => unawaited(_save()),
      children: [
        TkCallout(
          leading: TkSquare('b-lock', color: look.teal),
          title: 'Held for another approver',
          sub: 'They’re paid into ${b == null ? 'their current account' : masked(_str(b['last4']))} until another approver approves it.',
          subLines: 3,
          color: look.teal,
        ),
        _AccountFields(auth: widget.auth, account: _account, bank: _bank, onBank: (b) => setState(() => _bank = b)),
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}

/// Payroll → Team.
class PayrollTeamPage extends StatefulWidget {
  const PayrollTeamPage({super.key, required this.auth, this.backLabel = 'Back to Payroll'});
  final Auth auth;
  final String backLabel;

  @override
  State<PayrollTeamPage> createState() => _PayrollTeamPageState();
}

class _PayrollTeamPageState extends State<PayrollTeamPage> {
  GetPayrollTeamResponse? _team;
  Object? _error;
  String? _said;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final t = await _auth.api.getPayrollTeam();
      if (mounted) setState(() => (_team = t, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _after(Object? r) async {
    if (r is String && mounted) setState(() => _said = r);
    if (r != null) await _load();
  }

  Future<void> _add([GetPayrollTeamResponseStaffItem? from]) async {
    final r = await showTkSheet<bool>(context, (_) => PayrollAddSheet(auth: _auth, from: from));
    await _after(r == true ? true : null);
  }

  Future<void> _act(PayrollTeamMember m) async {
    final what = await pickAction(context, title: m.name, actions: const [
      ('change', 'Change details or pay', 'b-note', false),
      ('bank', 'New salary account', 'b-bank', false),
      ('end', 'End employment', 'b-x', true),
    ]);
    if (!mounted || what == null) return;
    switch (what) {
      case 'change':
        await _after(await showTkSheet<String>(context, (_) => PayrollChangeSheet(auth: _auth, member: m)));
      case 'bank':
        await _after(await showTkSheet<String>(context, (_) => PayrollBankSheet(auth: _auth, member: m)));
      case 'end':
        final yes = await askSure(context,
            title: 'End ${m.name}’s employment?', body: 'This takes effect today. Payrolla can’t set a last day.', ok: 'Yes, end it today', danger: true);
        if (!yes || !mounted) return;
        try {
          final r = await _auth.api.endPayrollEmployee(employee: m.id, body: EndPayrollEmployeeRequest(key: payrollKey(), confirm: 'yes'));
          await _after(r.words);
        } on Object catch (e) {
          if (mounted) showTkToast(context, errorWords(e), error: true);
        }
    }
  }

  Widget _row(PayrollTeamMember m, bool prepare) {
    final look = TkLook.of(context);
    final pay = m.pay;
    return TkListRow(
      title: m.name,
      letters: m.name,
      sub: payrollMemberSub(m),
      subLines: 2,
      amount: pay == null ? null : naira(pay.grossKobo),
      status: m.waitingChange != null ? 'Change waiting' : (m.status == 'left' ? 'Left' : null),
      statusTone: m.waitingChange != null ? CardTone.warn : CardTone.plain,
      square: m.staff != null ? look.lime : null,
      chevron: prepare && m.status != 'left',
      semanticsLabel: '${m.name}: ${payrollMemberSub(m)}${pay == null ? '' : ', ${naira(pay.grossKobo)} a month'}',
      onTap: prepare && m.status != 'left' ? () => unawaited(_act(m)) : null,
    );
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final t = _team;
    final prepare = t?.may.prepare ?? false;
    return TkPageScreen(
      title: 'Team',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Everyone in your Payrolla, and the account each is paid into. Payrolla keeps the pay and the accounts.',
      onRefresh: _load,
      children: [
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'Your team did not load', sub: errorWords(_error!), subLines: 4, color: look.red),
        if (t == null && _error == null) const TkSkeleton(rows: 4, label: 'Loading your team…'),
        if (_said != null) TkCallout(leading: TkSquare('b-check', color: look.lime), title: _said!, subLines: 3, color: look.lime),
        if (t != null && prepare) TkCta(label: 'Add someone', onTap: () => unawaited(_add())),
        if (t != null)
          TkSection(title: 'In Payrolla', inset: 66, empty: 'No one is in your Payrolla yet.', children: [for (final m in t.team) _row(m, prepare)]),
        if (t != null && prepare && t.staff.isNotEmpty)
          TkSection(title: 'Staff not in Payrolla', inset: 66, children: [
            for (final s in t.staff)
              TkListRow(
                title: s.name.isEmpty ? (s.email ?? 'Someone') : s.name,
                letters: s.name,
                sub: 'Add to payroll',
                subLines: 1,
                semanticsLabel: 'Add ${s.name} to payroll',
                onTap: () => unawaited(_add(s)),
              ),
          ]),
      ],
    );
  }
}

/// Approve a held change with the code Payrolla sends the approver.
class PayrollApproveSheet extends StatefulWidget {
  const PayrollApproveSheet({super.key, required this.auth, required this.change});
  final Auth auth;
  final PayrollBankChange change;

  @override
  State<PayrollApproveSheet> createState() => _PayrollApproveSheetState();
}

class _PayrollApproveSheetState extends State<PayrollApproveSheet> {
  final _code = TextEditingController();
  StartBankChangeCodeResponse? _sent;
  String? _problem;
  bool _needNew = false;
  bool _busy = false;

  @override
  void dispose() {
    _code.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.startBankChangeCode(change: widget.change.id, body: StartBankChangeCodeRequest(key: payrollKey()));
      _code.clear();
      if (mounted) setState(() => (_sent = r, _needNew = false));
    } on Object catch (e) {
      if (mounted) setState(() => _problem = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _approve() async {
    final sent = _sent;
    final code = _code.text.replaceAll(RegExp(r'\D'), '');
    if (sent == null || code.length != 6) return;
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.approveBankChange(
          change: widget.change.id, body: ApproveBankChangeRequest(challengeId: sent.challenge.challengeId, code: code, key: payrollKey()));
      if (mounted) Navigator.of(context).pop(r.words);
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
    final c = widget.change;
    final sent = _sent;
    return TkFormSheet(
      title: 'Approve the account change',
      crumb: c.employee,
      action: sent == null || _needNew ? (_busy ? 'Sending…' : (_needNew ? 'Send a new code' : 'Send me a code')) : 'Approve',
      onAction: _busy ? null : () => unawaited(sent == null || _needNew ? _send() : _approve()),
      second: sent != null && !_needNew ? 'Send a new code' : null,
      onSecond: sent != null && !_needNew && !_busy ? () => unawaited(_send()) : null,
      children: [
        TkCallout(
          leading: TkSquare('b-bank', color: look.orange),
          title: c.accountName,
          sub: '${c.bank} ${masked(c.last4)} · ${payrollViaWords(c.via, c.askedBy)}',
          subLines: 3,
          color: look.orange,
        ),
        if (sent != null) ...[
          TkCallout(leading: TkSquare('b-lock', color: look.teal), title: sent.sentTo, sub: 'Type it here. Taskiem sends it to Payrolla once and keeps it nowhere.', subLines: 4, color: look.teal),
          TkFormGroup(children: [
            TkKitField(
              label: 'The code from Payrolla',
              hint: sent.safer ? 'A code from an authenticator app is safer: set one up in Payrolla.' : null,
              field: false,
              child: CodeBoxes(key: const ValueKey('payroll-approve-code'), controller: _code, hint: 'The code from Payrolla', onChanged: (_) => setState(() {})),
            ),
          ]),
        ],
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}

/// Reject a held change with a short note.
class PayrollRejectSheet extends StatefulWidget {
  const PayrollRejectSheet({super.key, required this.auth, required this.change});
  final Auth auth;
  final PayrollBankChange change;

  @override
  State<PayrollRejectSheet> createState() => _PayrollRejectSheetState();
}

class _PayrollRejectSheetState extends State<PayrollRejectSheet> {
  final _note = TextEditingController();
  final String _key = payrollKey();
  String? _problem;
  bool _busy = false;

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  Future<void> _reject() async {
    if (_note.text.trim().isEmpty) {
      setState(() => _problem = 'Say in a few words why.');
      return;
    }
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.rejectBankChange(change: widget.change.id, body: RejectBankChangeRequest(key: _key, note: _note.text.trim()));
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
      title: 'Reject the account change',
      crumb: widget.change.employee,
      action: _busy ? 'Rejecting…' : 'Reject',
      onAction: _busy ? null : () => unawaited(_reject()),
      children: [
        TkFormGroup(children: [TkKitField(label: 'Why, in a few words', child: TkTextBox(key: const ValueKey('payroll-reject-note'), controller: _note, max: 300, size: 15, height: 46))]),
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}

/// Payroll → Account changes.
class PayrollChangesPage extends StatefulWidget {
  const PayrollChangesPage({super.key, required this.auth, this.backLabel = 'Back to Payroll'});
  final Auth auth;
  final String backLabel;

  @override
  State<PayrollChangesPage> createState() => _PayrollChangesPageState();
}

class _PayrollChangesPageState extends State<PayrollChangesPage> {
  GetPayrollBankChangesResponse? _list;
  Object? _error;
  String? _said;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final l = await widget.auth.api.getPayrollBankChanges();
      if (mounted) setState(() => (_list = l, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _act(PayrollBankChange c) async {
    final what = await pickAction(context, title: c.employee ?? 'Account change', note: c.mine ? 'You asked for this, so someone else must approve it.' : null, actions: [
      if (!c.mine) ('approve', 'Approve with a code', 'b-check', false),
      ('reject', 'Reject', 'b-x', true),
    ]);
    if (!mounted || what == null) return;
    final r = await showTkSheet<String>(
        context, (_) => what == 'approve' ? PayrollApproveSheet(auth: widget.auth, change: c) : PayrollRejectSheet(auth: widget.auth, change: c));
    if (r != null && mounted) {
      setState(() => _said = r);
      await _load();
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final l = _list;
    final approve = l?.may.approve ?? false;
    return TkPageScreen(
      title: 'Account changes',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'New salary accounts wait here until an approver other than whoever asked approves them, with a code Payrolla sends.',
      onRefresh: _load,
      children: [
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'Account changes did not load', sub: errorWords(_error!), subLines: 4, color: look.red),
        if (l == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading…'),
        if (_said != null) TkCallout(leading: TkSquare('b-check', color: look.lime), title: _said!, subLines: 3, color: look.lime),
        if (l != null)
          TkSection(title: 'Waiting', inset: 66, empty: 'No account change is waiting.', children: [
            for (final c in l.changes)
              TkListRow(
                title: c.employee ?? 'Someone',
                letters: c.employee ?? '?',
                sub: '${c.accountName}, ${c.bank} ${masked(c.last4)} · ${payrollViaWords(c.via, c.askedBy)}',
                subLines: 3,
                chevron: approve,
                trailing: c.mine ? const CardPill('You asked', CardTone.plain) : null,
                semanticsLabel: '${c.employee}: ${c.accountName}, ${c.bank} ${masked(c.last4)}, ${payrollViaWords(c.via, c.askedBy)}',
                onTap: approve ? () => unawaited(_act(c)) : null,
              ),
          ]),
      ],
    );
  }
}

/// Change my salary account: the bank's name for it first, then the code
/// Payrolla texts me.
class MyPayAccountSheet extends StatefulWidget {
  const MyPayAccountSheet({super.key, required this.auth});
  final Auth auth;

  @override
  State<MyPayAccountSheet> createState() => _MyPayAccountSheetState();
}

class _MyPayAccountSheetState extends State<MyPayAccountSheet> {
  final _account = TextEditingController();
  final _code = TextEditingController();
  ListPayrollBanksResponseBanksItem? _bank;
  StartMyBankChangeResponse? _sent;
  String? _problem;
  bool _needNew = false;
  bool _busy = false;

  @override
  void dispose() {
    _account.dispose();
    _code.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final digits = _account.text.replaceAll(RegExp(r'[\s-]'), '');
    if (_bank == null || digits.length != 10) {
      setState(() => _problem = 'Choose the bank and type the 10-digit account number.');
      return;
    }
    setState(() => (_busy = true, _problem = null));
    try {
      final r = await widget.auth.api.startMyBankChange(body: StartMyBankChangeRequest(key: payrollKey(), bankCode: _bank!.code, accountNumber: digits));
      _code.clear();
      if (mounted) setState(() => (_sent = r, _needNew = false));
    } on Object catch (e) {
      if (mounted) setState(() => _problem = errorWords(e));
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
      final r = await widget.auth.api.confirmMyBankChange(body: ConfirmMyBankChangeRequest(challengeId: sent.challenge.challengeId, code: code, key: payrollKey()));
      if (mounted) Navigator.of(context).pop(r.words);
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
    return TkFormSheet(
      title: 'Change my salary account',
      action: sent == null || _needNew ? (_busy ? 'Checking…' : (_needNew ? 'Send a new code' : 'Continue')) : 'Yes, it’s mine',
      onAction: _busy ? null : () => unawaited(sent == null || _needNew ? _send() : _confirm()),
      second: sent != null && !_needNew ? 'Send a new code' : null,
      onSecond: sent != null && !_needNew && !_busy ? () => unawaited(_send()) : null,
      children: [
        if (sent == null) _AccountFields(auth: widget.auth, account: _account, bank: _bank, onBank: (b) => setState(() => _bank = b), check: false),
        if (sent == null)
          TkCallout(leading: TkSquare('b-lock', color: look.teal), title: 'A code by text', sub: 'Payrolla checks the account with the bank and texts you a code.', subLines: 3, color: look.teal),
        if (sent != null) ...[
          TkCallout(leading: TkSquare('b-bank', color: look.lime), title: '${sent.accountName} ${masked(sent.last4)}', sub: 'Is this your account? ${sent.bank}', subLines: 2, color: look.lime),
          TkCallout(leading: TkSquare('b-lock', color: look.teal), title: sent.sentTo, sub: 'If this is your account, type the code here.', subLines: 3, color: look.teal),
          TkFormGroup(children: [
            TkKitField(
              label: 'The code from Payrolla',
              field: false,
              child: CodeBoxes(key: const ValueKey('mypay-code'), controller: _code, hint: 'The code from Payrolla', onChanged: (_) => setState(() {})),
            ),
          ]),
        ],
        if (_problem != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _problem!, subLines: 3, color: look.red),
      ],
    );
  }
}

/// My pay: a staff member's own salary account in Payrolla.
class MyPayPage extends StatefulWidget {
  const MyPayPage({super.key, required this.auth, this.backLabel = 'Back'});
  final Auth auth;
  final String backLabel;

  @override
  State<MyPayPage> createState() => _MyPayPageState();
}

class _MyPayPageState extends State<MyPayPage> {
  GetMyPayResponse? _mine;
  Object? _error;
  String? _said;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final m = await widget.auth.api.getMyPay();
      if (mounted) setState(() => (_mine = m, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _change() async {
    final r = await showTkSheet<String>(context, (_) => MyPayAccountSheet(auth: widget.auth));
    if (r != null && mounted) {
      setState(() => _said = r);
      await _load();
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final m = _mine;
    final me = m?.me;
    final bank = _map(me?.bank);
    final waiting = _map(me?.waitingChange);
    return TkPageScreen(
      title: 'My pay',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'The account your salary is paid into, as Payrolla has it. A new account waits for your employer to approve it.',
      onRefresh: _load,
      children: [
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'My pay did not load', sub: errorWords(_error!), subLines: 4, color: look.red),
        if (m == null && _error == null) const TkSkeleton(rows: 2, label: 'Loading…'),
        if (_said != null) TkCallout(leading: TkSquare('b-check', color: look.lime), title: _said!, subLines: 3, color: look.lime),
        if (m != null && !m.linked)
          TkCallout(
            leading: TkSquare('b-query', color: look.teal),
            title: 'Not linked to Payrolla yet',
            sub: 'Ask the owner to add you to payroll in this business.',
            subLines: 3,
            color: look.teal,
          ),
        if (me != null) ...[
          TkSection(title: 'Salary account', inset: 56, children: [
            TkListRow(title: _str(bank?['account_name']).isEmpty ? 'No account yet' : _str(bank?['account_name']), sub: payrollBankLine(bank), icon: 'b-bank', square: look.lime, chevron: false),
            if (waiting != null)
              TkListRow(
                title: 'Waiting for your employer',
                sub: '${_str(waiting['account_name'])}, ${_str(waiting['bank'])} ${masked(_str(waiting['last4']))}',
                icon: 'b-clock',
                square: look.orange,
                chevron: false,
              ),
            TkListRow(
              title: 'Your phone in Payrolla',
              sub: '${(me.phone ?? '').isEmpty ? 'None' : me.phone} · your employer changes it in Payrolla',
              icon: 'b-phone',
              square: look.teal,
              chevron: false,
            ),
          ]),
          if (me.status != 'left') TkCta(label: 'Change my salary account', onTap: () => unawaited(_change())),
          MyPayslipsSection(auth: widget.auth),
        ],
      ],
    );
  }
}
