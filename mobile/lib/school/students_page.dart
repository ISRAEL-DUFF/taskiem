// Students (docs/schools.md §4; web Schools.tsx, SchoolFamily.tsx,
// SchoolAccountParts.tsx): the list by class and status; one student with
// what is left to pay, their account number, their family, their fees,
// chasing, clearance and class history; adding one.

import 'dart:async';

import 'package:flutter/material.dart' show showDatePicker;
import 'package:flutter/services.dart' show Clipboard, ClipboardData;
import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../chat/approve.dart' show TkTextBox;
import '../chat/give_task.dart' show TkPillChoice;
import '../chat/invoices_page.dart' show openInvoice;
import '../chat/words.dart' show naira;
import '../kit/forms.dart' show errorWords, nairaKobo;
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading, TkSearchBox;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkModalChoices;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, TkPickBox, showTkSheet;
import '../theme/look.dart';
import '../chat/words.dart' show CardTone;
import '../kit/forms.dart' show TkSwitch;
import '../kit/list.dart' show TkPill;
import '../theme/theme.dart';
import 'school_chasing.dart' show PromiseSheet, RemindSheet, PlanSheet;
import 'school_clearance.dart' show OverrideSheet;
import 'school_money.dart' show CashSheet, ReceiptsPage;
import 'school_setup.dart' show ImportStudentsPage;
import 'school_tab.dart' show QuietValue, pushSchool, said;
import 'school_words.dart';

/// A row of choices as pills that scroll sideways (classes, filters).
class SchoolPills<T> extends StatelessWidget {
  const SchoolPills({super.key, required this.choices, required this.on, required this.onPick, this.label = 'Show'});
  final List<(T, String)> choices;
  final T on;
  final ValueChanged<T> onPick;
  final String label;

  @override
  Widget build(BuildContext context) => Semantics(
    container: true,
    label: label,
    explicitChildNodes: true,
    child: SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      child: Row(
        children: [
          for (final (i, (v, w)) in choices.indexed) ...[if (i > 0) const SizedBox(width: 6), TkPillChoice(label: w, on: v == on, onTap: () => onPick(v))],
        ],
      ),
    ),
  );
}

/// The school's classes, read once a screen (for chips and pickers).
Future<List<Map<String, dynamic>>> loadClasses(Auth auth) async => maps(mapOf(await auth.transport.send('GET', '$schoolApi/classes'))?['classes']);

// --- Students ---

class StudentsPage extends StatefulWidget {
  const StudentsPage({super.key, required this.auth, this.classId = ''});
  final Auth auth;
  final String classId;

  @override
  State<StudentsPage> createState() => _StudentsPageState();
}

class _StudentsPageState extends State<StudentsPage> {
  late String _class = widget.classId;
  String _status = 'active';
  final _q = TextEditingController();
  Timer? _typing;
  List<Map<String, dynamic>>? _students;
  List<Map<String, dynamic>> _classes = const [];
  int _total = 0;
  String? _error;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _q.addListener(() {
      _typing?.cancel();
      _typing = Timer(const Duration(milliseconds: 300), _load);
    });
    loadClasses(_auth).then((c) {
      if (mounted) setState(() => _classes = c);
    }, onError: (_) {});
    _load();
  }

  @override
  void dispose() {
    _typing?.cancel();
    _q.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final q = {
      if (_class.isNotEmpty) 'class': _class,
      if (_status.isNotEmpty) 'status': _status,
      if (_q.text.trim().isNotEmpty) 'q': _q.text.trim(),
      'limit': '1000',
      if (_auth.mayReveal) 'reveal': 'true',
    };
    try {
      final r = mapOf(await _auth.transport.send('GET', '$schoolApi/students', query: q));
      if (mounted) setState(() => (_students = maps(r?['students']), _total = kobo(r?['total']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final s = _students;
    final manage = _auth.schoolManage;
    return TkPageScreen(
      title: 'Students',
      titleSize: 30,
      backLabel: 'Back to School',
      onRefresh: _load,
      action: manage
          ? Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                TkHeadButton(
                  icon: 'b-upload',
                  label: 'Import students',
                  onTap: () => showTkSheet<void>(context, (_) => ImportStudentsPage(auth: _auth)).then((_) => _load()),
                ),
                const SizedBox(width: 8),
                TkHeadButton(
                  icon: 'plus',
                  label: 'Add a student',
                  primary: true,
                  onTap: () async {
                    final id = await showTkSheet<String>(context, (_) => AddStudentSheet(auth: _auth, classes: _classes));
                    await _load();
                    if (id != null && context.mounted) unawaited(pushSchool<void>(context, StudentPage(auth: _auth, id: id)));
                  },
                ),
              ],
            )
          : null,
      children: [
        TkSearchBox(controller: _q, hint: 'Admission number or name', margin: EdgeInsets.zero),
        if (_classes.isNotEmpty)
          SchoolPills<String>(
            label: 'Class',
            choices: [('', 'All classes'), for (final c in _classes) (str(c['id']), str(c['label']))],
            on: _class,
            onPick: (v) {
              setState(() => _class = v);
              _load();
            },
          ),
        TkChips<String>(
          label: 'Status',
          padding: EdgeInsets.zero,
          chips: const [('active', 'Active'), ('', 'Everyone'), ('withdrawn', 'Withdrawn'), ('graduated', 'Graduated')],
          on: _status,
          onPick: (v) {
            setState(() => _status = v);
            _load();
          },
        ),
        if (_error != null) TkErrorBox(_error!),
        if (s == null && _error == null) const TkLoading(rows: 5, label: 'Loading students…'),
        if (s != null) Text('$_total ${_total == 1 ? 'student' : 'students'}${_auth.mayReveal ? '' : ' · names hidden'}', style: tkText(size: 13, color: t.muted)),
        if (s != null)
          TkSection(
            empty: _q.text.trim().isEmpty ? 'No students here yet.' : 'No student matches “${_q.text.trim()}”.',
            children: [
              for (final x in s)
                TkListRow(
                  title: nameOr(x),
                  sub: studentLine(x),
                  status: str(x['status']) == 'active' ? 'Active' : (str(x['status']) == 'withdrawn' ? 'Withdrawn' : 'Graduated'),
                  statusTone: str(x['status']) == 'active' ? CardTone.lime : CardTone.plain,
                  onTap: () => pushSchool<void>(context, StudentPage(auth: _auth, id: str(x['id']))).then((_) => _load()),
                ),
            ],
          ),
      ],
    );
  }
}

// --- Adding one ---

class AddStudentSheet extends StatefulWidget {
  const AddStudentSheet({super.key, required this.auth, required this.classes});
  final Auth auth;
  final List<Map<String, dynamic>> classes;

  @override
  State<AddStudentSheet> createState() => _AddStudentSheetState();
}

class _AddStudentSheetState extends State<AddStudentSheet> {
  final _name = TextEditingController();
  final _adm = TextEditingController();
  final _gName = TextEditingController();
  final _gPhone = TextEditingController();
  final _gEmail = TextEditingController();
  final _gRel = TextEditingController();
  String _classId = '';
  DateTime? _born;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    for (final c in [_name, _adm, _gName, _gPhone]) {
      c.addListener(() => setState(() {}));
    }
    if (widget.classes.isNotEmpty) _classId = str(widget.classes.first['id']);
  }

  @override
  void dispose() {
    for (final c in [_name, _adm, _gName, _gPhone, _gEmail, _gRel]) {
      c.dispose();
    }
    super.dispose();
  }

  bool get _ready => _name.text.trim().isNotEmpty && _adm.text.trim().isNotEmpty && _classId.isNotEmpty;

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await widget.auth.transport.send('POST', '$schoolApi/students', body: {
        'name': _name.text.trim(),
        'admission_number': _adm.text.trim(),
        'class_id': _classId,
        if (_born != null) 'date_of_birth': dayKey(_born!),
        if (_gName.text.trim().isNotEmpty || _gPhone.text.trim().isNotEmpty)
          'guardians': [
            {
              'name': _gName.text.trim(),
              'phone': _gPhone.text.trim(),
              if (_gEmail.text.trim().isNotEmpty) 'email': _gEmail.text.trim(),
              if (_gRel.text.trim().isNotEmpty) 'relationship': _gRel.text.trim(),
            },
          ],
      }));
      if (!mounted) return;
      said(context, '${_name.text.trim()} is added');
      Navigator.of(context).pop(str(r?['id']).isEmpty ? str(mapOf(r?['student'])?['id']) : str(r?['id']));
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final className = widget.classes.where((c) => str(c['id']) == _classId).map((c) => str(c['label'])).firstOrNull ?? 'Choose…';
    return TkFormSheet(
      title: 'Add a student',
      crumb: 'Students',
      action: _busy ? 'Adding…' : 'Add student',
      onAction: _busy || !_ready ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(
          children: [
            TkKitField(label: 'Name', child: TkTextBox(controller: _name, max: 120, size: 15, height: 46)),
            TkKitField(label: 'Admission number', child: TkTextBox(controller: _adm, max: 40, size: 15, height: 46)),
            TkKitField(
              label: 'Class',
              field: false,
              child: TkPickBox(
                label: 'Class',
                shown: className,
                onTap: () async {
                  final v = await showTkModalChoices(context, title: 'Class', value: _classId, choices: [for (final c in widget.classes) (str(c['id']), str(c['label']))]);
                  if (v != null) setState(() => _classId = v);
                },
              ),
            ),
            TkKitField(
              label: 'Date of birth (optional)',
              field: false,
              child: TkPickBox(
                label: 'Date of birth',
                shown: _born == null ? 'Not given' : dayWords(dayKey(_born!)),
                onTap: () async {
                  final now = DateTime.now();
                  final d = await showDatePicker(context: context, initialDate: _born ?? DateTime(now.year - 10), firstDate: DateTime(now.year - 30), lastDate: now);
                  if (d != null) setState(() => _born = d);
                },
              ),
            ),
          ],
        ),
        TkFormGroup(
          title: 'Guardian',
          children: [
            TkKitField(label: 'Name', child: TkTextBox(controller: _gName, max: 120, size: 15, height: 46)),
            TkKitField(label: 'Phone', hint: 'A family is the guardians with one phone.', child: TkTextBox(controller: _gPhone, phone: true, size: 15, height: 46)),
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: TkKitField(label: 'Email (optional)', child: TkTextBox(controller: _gEmail, plain: true, size: 15, height: 46))),
                const SizedBox(width: 10),
                Expanded(child: TkKitField(label: 'Relationship', child: TkTextBox(controller: _gRel, max: 30, size: 15, height: 46))),
              ],
            ),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

/// A field that opens a choice, with a chevron (as drawn).
// --- One student ---

class StudentPage extends StatefulWidget {
  const StudentPage({super.key, required this.auth, required this.id});
  final Auth auth;
  final String id;

  @override
  State<StudentPage> createState() => _StudentPageState();
}

class _StudentPageState extends State<StudentPage> {
  Map<String, dynamic>? _s;
  Map<String, dynamic>? _chasing;
  Map<String, dynamic>? _fees;
  String? _noTerm;
  List<Map<String, dynamic>> _accounts = const [];
  Map<String, dynamic>? _clearance;
  List<Map<String, dynamic>> _overrides = const [];
  List<Map<String, dynamic>> _discounts = const [];
  List<Map<String, dynamic>> _plans = const [];
  List<Map<String, dynamic>> _claims = const [];
  String? _error;
  bool _busy = false;

  Auth get _auth => widget.auth;
  String get _path => '$schoolApi/students/${Uri.encodeComponent(widget.id)}';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<Map<String, dynamic>?> _get(String p) async {
    try {
      return mapOf(await _auth.transport.send('GET', p));
    } on ApiError catch (e) {
      if (p.endsWith('/fees') && e.status == 409) _noTerm = e.message;
      return null;
    } on Object {
      return null;
    }
  }

  Future<void> _load() async {
    try {
      final s = mapOf(await _auth.transport.send('GET', '$_path${_auth.reveal()}'));
      final id = str(mapOf(s?['student'])?['id']);
      final base = '$schoolApi/students/${Uri.encodeComponent(id.isEmpty ? widget.id : id)}';
      final got = await Future.wait([
        _get('$base/chasing'),
        _get('$base/fees'),
        _get('$base/accounts${_auth.reveal()}'),
        _get('$base/clearance'),
        _get('$base/discounts'),
        _get('$schoolApi/plans?status=any&student=$id'),
        _get('$schoolApi/claims?status=open&student=$id'),
      ]);
      if (!mounted) return;
      setState(() {
        _s = mapOf(s?['student']);
        _chasing = got[0];
        _fees = got[1];
        _accounts = maps(got[2]?['accounts']);
        _clearance = mapOf(got[3]?['clearance']);
        _overrides = maps(got[3]?['overrides']);
        _discounts = maps(got[4]?['discounts']).where((d) => d['ended_at'] == null).toList();
        _plans = maps(got[5]?['plans']);
        _claims = maps(got[6]?['claims']);
        _error = null;
      });
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => _busy = true);
    try {
      await f();
      if (mounted) said(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) said(context, errorWords(e), error: true);
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _more() async {
    final s = _s!;
    final pick = await showTkModalChoices(context, title: nameOr(s), choices: const [('class', 'Move to another class'), ('status', 'Change the status')]);
    if (!mounted || pick == null) return;
    if (pick == 'class') {
      final classes = await loadClasses(_auth);
      if (!mounted) return;
      final c = await showTkModalChoices(context, title: 'Move to', value: str(s['class_id']), choices: [for (final c in classes) (str(c['id']), str(c['label']))]);
      if (c != null && c != str(s['class_id'])) await _run(() => _auth.transport.send('PATCH', _path, body: {'class_id': c}), 'Moved');
    } else {
      final st = await showTkModalChoices(context, title: 'Status', value: str(s['status']), choices: const [('active', 'Active'), ('withdrawn', 'Withdrawn'), ('graduated', 'Graduated')]);
      if (st != null && st != str(s['status'])) await _run(() => _auth.transport.send('PATCH', _path, body: {'status': st}), 'Status changed');
    }
  }

  @override
  Widget build(BuildContext context) {
    final s = _s;
    final manage = _auth.schoolManage;
    return TkPageScreen(
      title: s == null ? 'Student' : nameOr(s),
      titleSize: 30,
      backLabel: 'Back to Students',
      onRefresh: _load,
      action: s != null && manage ? TkHeadButton(icon: 'b-more', label: 'More for ${nameOr(s)}', onTap: _more) : null,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (s == null && _error == null) const TkLoading(rows: 4, label: 'Loading the student…'),
        if (s != null) ..._body(s),
      ],
    );
  }

  List<Widget> _body(Map<String, dynamic> s) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final manage = _auth.schoolManage;
    final c = _chasing;
    final inv = mapOf(_fees?['invoice']);
    final balance = kobo(c?['balance_kobo']);
    final days = kobo(c?['days_past_due']);
    final total = kobo(inv?['total_kobo']);
    final paid = kobo(inv?['paid_kobo']);
    final status = str(_clearance?['status']);
    final family = mapOf(s['family']);
    final id = str(s['id']);
    final account = _accounts.where((a) => str(a['status']) == 'issued').firstOrNull ?? _accounts.firstOrNull;
    final promises = maps(c?['promises']);
    final reminders = maps(c?['reminders']);
    final plan = mapOf(c?['plan']);
    final asked = _plans.where((p) => str(p['status']) == 'requested').toList();
    final lines = maps(_fees?['lines']);
    final optional = maps(_fees?['items']).where((i) => str(i['kind']) == 'optional').toList();
    return [
      Text(
        [studentLine(s), if (str(s['status']).isNotEmpty) str(s['status'])[0].toUpperCase() + str(s['status']).substring(1)].join(' · '),
        style: tkText(size: 13.5, color: t.muted),
      ),
      // What is left to pay this term (its chasing), its invoice, cleared.
      TkTopCard(
        onTap: inv == null ? null : () => openInvoice(context, _auth, str(inv['invoice_id'])),
        semanticsLabel: 'Still to pay ${naira(balance)}',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TkEyebrow(balance > 0 ? 'Still to pay' : 'Nothing to pay', color: balance > 0 ? look.orangeInk : look.limeInk, trailing: days > 0 ? lateWords(days) : null),
            const SizedBox(height: 6),
            FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text(naira(balance), style: tkFigure(context))),
            if (inv != null) Text('of ${naira(total)} for ${str(inv['term'])} · ${str(inv['number'])}', style: tkText(size: 13, color: t.muted)),
            if (inv == null && _noTerm != null) Text(_noTerm!, style: tkText(size: 13, color: t.muted)),
            if (total > 0) ...[const SizedBox(height: 10), TkProgress((paid / total).clamp(0.0, 1.0), color: balance > 0 ? look.orange : look.lime, semanticsLabel: 'Paid of the term')],
            if (status.isNotEmpty) ...[
              const SizedBox(height: 10),
              Align(
                alignment: Alignment.centerLeft,
                child: TkPill(clearanceWords[status] ?? status, tone: status == 'cleared' ? CardTone.lime : (status == 'partly_paid' ? CardTone.warn : CardTone.danger)),
              ),
            ],
          ],
        ),
      ),
      TkActionTiles([
        if (manage) (icon: 'b-cash', label: 'Record cash', semantics: 'Record a payment for ${nameOr(s)}', onTap: () => _sheet(CashSheet(auth: _auth, student: s, balance: balance))),
        if (manage && balance > 0) (icon: 'b-bell', label: 'Remind', semantics: 'Send a reminder', onTap: () => _sheet(RemindSheet(auth: _auth, students: [s], balances: [balance], rung: rungFor(days)))),
        if (manage && balance > 0) (icon: 'b-hand', label: 'A promise', semantics: 'Record a promise to pay', onTap: () => _sheet(PromiseSheet(auth: _auth, student: s, balance: balance))),
        (icon: 'b-receipt', label: 'Receipts', semantics: 'Receipts for ${nameOr(s)}', onTap: () => pushSchool<void>(context, ReceiptsPage(auth: _auth, student: id))),
      ]),
      if (account != null || (manage && _accounts.isEmpty)) _accountCard(account, manage, look, t),
      if (family != null) _familySection(family, id, manage, look),
      if (lines.isNotEmpty || optional.isNotEmpty || _discounts.isNotEmpty || manage)
        TkSection(
          title: inv == null ? 'This term’s fees' : '${str(inv['term']).split(' · ').first} fees',
          children: [
            for (final l in lines) _FeeLine(l),
            if (lines.isNotEmpty) _FeeLine({'description': 'Total', 'quantity': 1, 'unit_kobo': kobo(_fees?['total_kobo'])}, total: true),
          ],
        ),
      if (optional.isNotEmpty || _discounts.isNotEmpty || manage)
        TkSection(
          inset: 56,
          children: [
            if (optional.isNotEmpty)
              TkListRow(
                title: 'Optional items',
                icon: 'tag',
                square: look.blue,
                trailing: QuietValue('${(_fees?['chosen'] as List? ?? const []).length} of ${optional.length}'),
                onTap: manage ? () => _sheet(_OptionsSheet(auth: _auth, id: id, fees: _fees!)) : null,
              ),
            TkListRow(
              title: 'Discounts',
              icon: 'b-hand',
              square: look.teal,
              trailing: QuietValue(_discounts.isEmpty ? 'None' : _discounts.map(_discountWords).join(', ')),
              onTap: () => _sheet(_DiscountsSheet(auth: _auth, id: id, discounts: _discounts, manage: manage)),
            ),
          ],
        ),
      if (promises.isNotEmpty || reminders.isNotEmpty || plan != null || asked.isNotEmpty)
        TkSection(
          title: 'Chasing',
          children: [
            for (final p in asked)
              TkListRow(
                title: 'A payment plan asked for',
                sub: '${maps(p['instalments']).length} instalments · ${dayWords(str(p['created_at']))}',
                icon: 'b-plan',
                tone: CardTone.warn,
                status: 'Asked',
                statusTone: CardTone.warn,
                onTap: _auth.schoolApprove ? () => _sheet(PlanSheet(auth: _auth, plan: p)) : null,
              ),
            if (plan != null)
              TkListRow(
                title: 'On a payment plan',
                sub: [
                  '${maps(plan['instalments']).length} instalments',
                  if (str(plan['next_due_on']).isNotEmpty) 'next ${naira(kobo(plan['next_amount_kobo']))} by ${dayWords(str(plan['next_due_on']))}',
                ].join(' · '),
                icon: 'b-plan',
                status: str(plan['state']) == 'arrears' ? 'Behind' : (str(plan['state']) == 'settled' ? 'Settled' : 'In good standing'),
                statusTone: str(plan['state']) == 'arrears' ? CardTone.danger : CardTone.lime,
              ),
            for (final p in promises.take(3))
              TkListRow(
                title: 'Promised by ${dayWords(str(p['promised_on']))}',
                sub: ['${naira(kobo(p['balance_kobo']))} due when made', if (str(p['note']).isNotEmpty) str(p['note'])].join(' · '),
                icon: 'b-hand',
                status: str(p['status'])[0].toUpperCase() + str(p['status']).substring(1),
                statusTone: str(p['status']) == 'kept' ? CardTone.lime : (str(p['status']) == 'missed' ? CardTone.danger : (str(p['status']) == 'open' ? CardTone.warn : CardTone.plain)),
                trailing: str(p['status']) == 'open' && manage
                    ? TkSmallButton(
                        label: 'Withdraw',
                        semantics: 'Withdraw the promise',
                        onPressed: _busy ? null : () => _run(() => _auth.transport.send('POST', '$schoolApi/promises/${str(p['id'])}/withdraw', body: const {}), 'Promise withdrawn'),
                      )
                    : null,
              ),
            for (final r in reminders.take(3))
              TkListRow(
                title: 'Reminder ${kobo(r['rung'])}, ${rungName(kobo(r['rung'])).toLowerCase()}',
                sub: [dayWords(str(r['sent_on'])), r['nobody'] == true ? 'nobody reached' : (mapOf(r['channels'])?.keys.join(', ') ?? '')].where((x) => x.isNotEmpty).join(' · '),
                icon: 'b-bell',
                amount: naira(kobo(r['balance_kobo'])),
              ),
          ],
        ),
      if (_clearance != null)
        TkSection(
          title: 'Clearance',
          inset: 56,
          children: [
            TkListRow(
              title: clearanceWords[status] ?? status,
              sub: [
                clearanceReasons[str(_clearance!['reason'])] ?? str(_clearance!['reason']),
                for (final o in _overrides.where((o) => o['in_force'] == true)) 'override until ${dayWords(str(o['ends_on']))}: ${str(o['reason'])}',
              ].join(' · '),
              icon: 'b-shield',
              square: status == 'cleared' ? look.lime : (status == 'partly_paid' ? look.orange : look.red),
              trailing: status != 'cleared' && _auth.schoolApprove
                  ? TkSmallButton(label: 'Clear', semantics: 'Clear by override', onPressed: () => _sheet(OverrideSheet(auth: _auth, student: s)))
                  : null,
            ),
          ],
        ),
      if (_claims.isNotEmpty)
        TkSection(
          title: 'The family says it paid',
          children: [
            for (final cl in _claims)
              TkListRow(
                title: '${naira(kobo(cl['amount_kobo']))} by ${str(cl['how'])}',
                sub: ['on ${dayWords(str(cl['paid_on']))}', if (str(cl['reference']).isNotEmpty) 'ref ${str(cl['reference'])}', if (str(cl['note']).isNotEmpty) str(cl['note'])].join(' · '),
                icon: 'b-query',
                tone: CardTone.warn,
                trailing: manage ? TkSmallButton(label: 'Dismiss', semantics: 'Dismiss the claim', onPressed: () => _sheet(_DismissClaim(auth: _auth, claim: cl))) : null,
              ),
          ],
        ),
      if (maps(s['enrollments']).isNotEmpty)
        TkSection(
          title: 'Class history',
          children: [
            for (final e in maps(s['enrollments']))
              _FeeLine({'description': '${str(e['term'])} ${str(e['session'])}${str(e['term_state']) == 'current' ? ' (current)' : ''}', 'label': str(e['class'])}),
          ],
        ),
    ];
  }

  String _discountWords(Map<String, dynamic> d) {
    final kind = {'sibling': 'Sibling', 'staff_child': 'Staff child', 'scholarship': 'Scholarship', 'hardship': 'Hardship', 'other': 'Other'}[str(d['kind'])] ?? str(d['kind']);
    return d['percent'] != null ? '$kind ${kobo(d['percent'])}%' : '$kind ${naira(kobo(d['amount_kobo']))}';
  }

  Future<void> _sheet(Widget sheet) async {
    final done = await showTkSheet<bool>(context, (_) => sheet);
    if (done == true) await _load();
  }

  Widget _accountCard(Map<String, dynamic>? a, bool manage, TkLook look, TkTokens t) {
    final issued = a != null && str(a['status']) == 'issued';
    final number = str(a?['account_number']);
    final id = str(_s?['id']);
    final grouped = number.length == 10 ? '${number.substring(0, 4)} ${number.substring(4, 7)} ${number.substring(7)}' : number;
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text('THEIR ACCOUNT NUMBER', style: tkText(size: 11, weight: 700, letterSpacing: 0.6, color: t.muted)),
          const SizedBox(height: 2),
          if (issued) ...[
            Semantics(label: 'Account number $number', excludeSemantics: true, child: Text(grouped, style: tkDisplay(size: 24, letterSpacing: 0.5, color: t.text))),
            Text([str(a['bank_name']), str(a['account_name'])].where((x) => x.isNotEmpty).join(' · '), style: tkText(size: 12.5, color: t.muted)),
            const SizedBox(height: 10),
            TkActionTiles([
              (
                icon: 'b-copy',
                label: 'Copy',
                semantics: 'Copy the account number',
                onTap: () async {
                  await Clipboard.setData(ClipboardData(text: number));
                  if (mounted) said(context, 'Account number copied');
                },
              ),
              if (manage)
                (
                  icon: 'send',
                  label: 'Send to the family',
                  semantics: 'Send the account number to the family',
                  onTap: _busy
                      ? null
                      : () => _run(() async {
                          final r = mapOf(await _auth.transport.send('POST', '$schoolApi/students/$id/account/send', body: const {}));
                          if (r?['nobody'] == true) throw StateError('No guardian could be reached');
                        }, 'Sent to the family'),
                ),
            ]),
          ] else ...[
            Text(
              a == null ? 'Not issued yet' : (str(a['status']) == 'failed' ? 'Not issued: ${str(a['error'])}' : (str(a['status']) == 'unknown' ? 'Check with the rail' : 'Being issued…')),
              style: tkText(size: 15, weight: 600, color: t.text),
            ),
            if (manage && (a == null || str(a['status']) != 'issuing')) ...[
              const SizedBox(height: 10),
              TkSmallButton(
                label: a == null ? 'Issue the account number' : (str(a['status']) == 'unknown' ? 'I checked: issue it again' : 'Try again'),
                primary: true,
                onPressed: _busy
                    ? null
                    : () => _run(
                        () => _auth.transport.send('POST', '$schoolApi/students/$id/account', body: {if (a != null && str(a['status']) == 'unknown') 'retry': true}),
                        'Account number issued',
                      ),
              ),
            ],
          ],
        ],
      ),
    );
  }

  Widget _familySection(Map<String, dynamic> family, String self, bool manage, TkLook look) => TkSection(
    title: 'Family',
    children: [
      for (final g in maps(family['guardians']))
        TkListRow(title: nameOr(g), sub: [str(g['relationship']), str(g['phone'])].where((x) => x.isNotEmpty).join(' · ')),
      for (final o in maps(family['students']).where((x) => str(x['id']) != self))
        TkListRow(title: nameOr(o), sub: studentLine(o), onTap: () => pushSchool<void>(context, StudentPage(auth: _auth, id: str(o['id'])))),
      if (manage)
        TkListRow(
          title: 'Send the family page link',
          sub: 'Their page: balances, account numbers, receipts; no login',
          icon: 'b-family',
          square: look.blue,
          onTap: _busy
              ? null
              : () => _run(() async {
                  final r = mapOf(await _auth.transport.send('POST', '$schoolApi/families/${str(family['id'])}/link', body: const {}));
                  if (r?['nobody'] == true) throw StateError('No guardian could be reached');
                }, 'The family page link is sent'),
        ),
    ],
  );
}

/// A fee line: its words and amount; an amendment or pass-through said.
class _FeeLine extends StatelessWidget {
  const _FeeLine(this.l, {this.total = false});
  final Map<String, dynamic> l;
  final bool total;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final q = kobo(l['quantity']) == 0 ? 1 : kobo(l['quantity']);
    final amount = l['label'] != null ? str(l['label']) : naira(q * kobo(l['unit_kobo']));
    final note = [
      if (str(l['kind']) == 'optional') 'optional',
      if (str(l['pass_through_for']).isNotEmpty) 'for ${str(l['pass_through_for'])}',
      if (l['amendment'] == true) 'amended',
    ].join(' · ');
    return MergeSemantics(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(str(l['description']), style: tkText(size: 14, weight: total ? 700 : 400, color: total ? t.text : t.muted)),
                  if (note.isNotEmpty) Text(note, style: tkText(size: 12, color: t.muted)),
                ],
              ),
            ),
            Text(amount, style: tkText(size: total ? 16 : 14, weight: total ? 800 : 600, color: t.text).copyWith(fontFeatures: tkTabular)),
          ],
        ),
      ),
    );
  }
}

// --- A student's optional items and discounts ---

class _OptionsSheet extends StatefulWidget {
  const _OptionsSheet({required this.auth, required this.id, required this.fees});
  final Auth auth;
  final String id;
  final Map<String, dynamic> fees;

  @override
  State<_OptionsSheet> createState() => _OptionsSheetState();
}

class _OptionsSheetState extends State<_OptionsSheet> {
  late final _on = {for (final c in (widget.fees['chosen'] as List? ?? const [])) c is Map ? str(c['id']) : str(c)};
  bool _busy = false;
  String? _error;

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('PUT', '$schoolApi/students/${widget.id}/options', body: {'term': str(widget.fees['term_id']), 'items': _on.toList()});
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final items = maps(widget.fees['items']).where((i) => str(i['kind']) == 'optional').toList();
    return TkFormSheet(
      title: 'Optional items',
      crumb: 'This term',
      action: _busy ? 'Saving…' : 'Save',
      onAction: _busy ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkSection(
          inset: 56,
          children: [
            for (final i in items)
              TkListRow(
                title: str(i['name']),
                icon: 'tag',
                square: look.blue,
                amount: naira(kobo(i['amount_kobo'])),
                trailing: TkSwitch(label: str(i['name']), on: _on.contains(str(i['id'])), onChanged: (v) => setState(() => v ? _on.add(str(i['id'])) : _on.remove(str(i['id'])))),
              ),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

class _DiscountsSheet extends StatefulWidget {
  const _DiscountsSheet({required this.auth, required this.id, required this.discounts, required this.manage});
  final Auth auth;
  final String id;
  final List<Map<String, dynamic>> discounts;
  final bool manage;

  @override
  State<_DiscountsSheet> createState() => _DiscountsSheetState();
}

class _DiscountsSheetState extends State<_DiscountsSheet> {
  String _kind = 'sibling';
  bool _percent = true;
  final _value = TextEditingController();
  final _reason = TextEditingController();
  bool _busy = false;
  String? _error;
  bool _changed = false;

  @override
  void initState() {
    super.initState();
    _value.addListener(() => setState(() {}));
    _reason.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _value.dispose();
    _reason.dispose();
    super.dispose();
  }

  bool get _ready {
    if (_reason.text.trim().isEmpty) return false;
    if (_percent) {
      final n = int.tryParse(_value.text.trim());
      return n != null && n > 0 && n <= 100;
    }
    return (nairaKobo(_value.text) ?? 0) > 0;
  }

  Future<void> _give() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await widget.auth.transport.send('POST', '$schoolApi/students/${widget.id}/discounts', body: {
        'kind': _kind,
        if (_percent) 'percent': int.parse(_value.text.trim()) else 'amount_kobo': nairaKobo(_value.text),
        'reason': _reason.text.trim(),
      }));
      if (!mounted) return;
      said(context, str(r?['code']) == 'needs_approval' ? 'Sent for a second person to approve' : 'Discount given');
      Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  Future<void> _end(Map<String, dynamic> d) async {
    try {
      await widget.auth.transport.send('DELETE', '$schoolApi/students/${widget.id}/discounts/${str(d['id'])}');
      if (!mounted) return;
      setState(() => (widget.discounts.remove(d), _changed = true));
      said(context, 'Discount ended');
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkFormSheet(
      title: 'Discounts',
      crumb: 'This student',
      action: _busy ? 'Giving…' : 'Give the discount',
      onAction: !widget.manage || _busy || !_ready ? null : _give,
      second: 'Close',
      onSecond: () => Navigator.of(context).pop(_changed),
      children: [
        TkSection(
          inset: 56,
          empty: 'No discount now.',
          children: [
            for (final d in widget.discounts)
              TkListRow(
                title: d['percent'] != null ? '${kobo(d['percent'])}% off' : '${naira(kobo(d['amount_kobo']))} off',
                sub: '${str(d['kind']).replaceAll('_', ' ')} · ${str(d['reason'])}',
                icon: 'b-hand',
                square: look.teal,
                trailing: widget.manage ? TkSmallButton(label: 'End', semantics: 'End this discount', onPressed: () => _end(d)) : null,
              ),
          ],
        ),
        if (widget.manage)
          TkFormGroup(
            title: 'Give a discount',
            children: [
              SchoolPills<String>(
                label: 'Kind',
                choices: const [('sibling', 'Sibling'), ('staff_child', 'Staff child'), ('scholarship', 'Scholarship'), ('hardship', 'Hardship'), ('other', 'Other')],
                on: _kind,
                onPick: (v) => setState(() => _kind = v),
              ),
              TkChips<bool>(label: 'As', padding: EdgeInsets.zero, chips: const [(true, 'A percent'), (false, 'An amount')], on: _percent, onPick: (v) => setState(() => _percent = v)),
              TkKitField(label: _percent ? 'Percent off' : 'Amount off (₦)', child: TkTextBox(controller: _value, money: !_percent, digits: _percent ? 3 : null, size: 15, height: 46)),
              TkKitField(label: 'Reason', hint: 'At or above the owner’s amount, a second person approves it.', child: TkTextBox(controller: _reason, max: 300, size: 15, height: 46)),
            ],
          ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

class _DismissClaim extends StatefulWidget {
  const _DismissClaim({required this.auth, required this.claim});
  final Auth auth;
  final Map<String, dynamic> claim;

  @override
  State<_DismissClaim> createState() => _DismissClaimState();
}

class _DismissClaimState extends State<_DismissClaim> {
  final _reason = TextEditingController();
  bool _busy = false;
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

  Future<void> _go() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('POST', '$schoolApi/claims/${str(widget.claim['id'])}/resolve', body: {'reason': _reason.text.trim()});
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: 'Dismiss the claim',
    crumb: '${naira(kobo(widget.claim['amount_kobo']))} the family says it paid',
    action: _busy ? 'Dismissing…' : 'Dismiss it',
    onAction: _busy || _reason.text.trim().isEmpty ? null : _go,
    second: 'Keep it',
    onSecond: () => Navigator.of(context).pop(),
    children: [
      TkFormGroup(children: [TkKitField(label: 'Why', child: TkTextBox(controller: _reason, max: 300, size: 15, height: 46))]),
      if (_error != null) TkErrorBox(_error!),
    ],
  );
}

