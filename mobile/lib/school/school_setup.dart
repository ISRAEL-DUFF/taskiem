// The school's setup (docs/schools.md §4 to §6; web Schools.tsx,
// SchoolFees.tsx, SchoolAccounts.tsx): classes and terms, a class's fees,
// invoicing the term (what it will do first, then confirmed), importing
// students from a CSV (checked first), account numbers, promotion, and
// the owner's settings. The school's wallet is set up on the web.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart' show showDatePicker;
import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../chat/approve.dart' show TkTextBox;
import '../chat/money_page.dart' show pickStatement;
import '../chat/words.dart' show naira;
import '../kit/forms.dart' show errorWords, nairaKobo, nairaText;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart';
import '../kit/tap_area.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkConfirm, showTkModalChoices;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, TkLines, TkCta, showTkSheet;
import '../theme/look.dart';
import '../chat/words.dart' show CardTone;
import '../kit/forms.dart' show TkSwitch;
import '../theme/theme.dart';
import 'school_tab.dart' show QuietValue, pushSchool, said;
import 'school_words.dart';
import 'students_page.dart' show SchoolPills, StudentsPage, StudentPage, loadClasses;

// --- Classes and terms ---

class TermsPage extends StatefulWidget {
  const TermsPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<TermsPage> createState() => _TermsPageState();
}

class _TermsPageState extends State<TermsPage> {
  List<Map<String, dynamic>>? _sessions;
  List<Map<String, dynamic>> _classes = const [];
  String? _error;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = mapOf(await _auth.transport.send('GET', '$schoolApi/sessions'));
      final c = await loadClasses(_auth);
      if (mounted) setState(() => (_sessions = maps(r?['sessions']), _classes = c, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _start(Map<String, dynamic> term) async {
    final ok = await showTkConfirm(
      context,
      title: 'Start ${str(term['name'])} now?',
      body: 'Every active student is enrolled in their class for it, and it becomes the current term.',
      ok: 'Start it',
      cancel: 'Not now',
    );
    if (!ok) return;
    try {
      final r = mapOf(await _auth.transport.send('POST', '$schoolApi/terms/${str(term['id'])}/start', body: const {}));
      if (mounted) said(context, '${str(term['name'])} has started: ${kobo(r?['enrolled'])} enrolled');
      await _load();
    } on Object catch (e) {
      if (mounted) said(context, errorWords(e), error: true);
    }
  }

  Future<void> _add() async {
    final pick = await showTkModalChoices(context, title: 'Add', choices: const [('class', 'A class'), ('term', 'A term'), ('session', 'A session (school year)')]);
    if (!mounted || pick == null) return;
    final done = await showTkSheet<bool>(context, (_) => _AddSheet(auth: _auth, what: pick, sessions: _sessions ?? const [], nextOrder: _classes.length + 1));
    if (done == true) await _load();
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final s = _sessions;
    final manage = _auth.schoolManage;
    return TkPageScreen(
      title: 'Classes and terms',
      titleSize: 30,
      backLabel: 'Back to School',
      onRefresh: _load,
      action: manage ? TkHeadButton(icon: 'plus', label: 'Add a class, term or session', primary: true, onTap: _add) : null,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (s == null && _error == null) const TkLoading(rows: 3, label: 'Loading terms…'),
        if (s != null && s.isEmpty) Text('No school year yet. Add a session, then its terms.', style: tkText(size: 14, color: TkTheme.tokens(context).muted)),
        for (final x in s ?? const <Map<String, dynamic>>[]) ...[
          TkSection(
            title: str(x['label']),
            inset: 56,
            children: [
              for (final term in maps(x['terms']))
                TkListRow(
                  title: str(term['name']),
                  sub: '${dayWords(str(term['starts_on']), weekday: false)} to ${dayWords(str(term['ends_on']), weekday: false)} · fees due ${dayWords(str(term['fees_due_on']), weekday: false)}${kobo(term['enrolled']) > 0 ? ' · ${kobo(term['enrolled'])} enrolled' : ''}',
                  icon: 'b-term',
                  square: look.violet,
                  status: str(term['state']) == 'current' ? 'Current' : (str(term['state']) == 'planned' ? 'Planned' : 'Past'),
                  statusTone: str(term['state']) == 'current' ? CardTone.lime : CardTone.plain,
                  trailing: str(term['state']) == 'planned' && manage ? TkSmallButton(label: 'Start', semantics: 'Start ${str(term['name'])}', onPressed: () => _start(term)) : null,
                  onTap: () => pushSchool<void>(context, _TermPage(auth: _auth, term: term, session: x)).then((_) => _load()),
                ),
            ],
          ),
          TkSection(
            inset: 56,
            children: [
              for (final term in maps(x['terms']).where((t) => str(t['state']) == 'current')) ...[
                TkListRow(
                  title: 'Fees for ${str(term['name'])}',
                  icon: 'b-fees',
                  square: look.violet,
                  trailing: Text('${_classes.length} classes', style: tkText(size: 14, color: TkTheme.tokens(context).muted)),
                  onTap: () => openClassFees(context, _auth, term, '${str(term['name'])} · ${str(x['label'])}').then((_) => _load()),
                ),
                TkListRow(
                  title: 'Invoices for ${str(term['name'])}',
                  icon: 'b-receipt',
                  square: look.lime,
                  onTap: () => pushSchool<void>(context, TermInvoicesPage(auth: _auth, term: term)),
                ),
              ],
              if (x['promoted_at'] != null)
                TkListRow(title: 'Promoted', sub: dayWords(str(x['promoted_at'])), icon: 'b-up', square: look.blue)
              else
                TkListRow(
                  title: 'Promotion at the end of ${str(x['label'])}',
                  icon: 'b-up',
                  square: look.blue,
                  onTap: () => pushSchool<void>(context, PromotionPage(auth: _auth, session: x)).then((_) => _load()),
                ),
            ],
          ),
        ],
        TkSection(
          title: 'Classes',
          empty: 'No classes yet.',
          children: [
            for (final c in _classes)
              TkListRow(
                title: str(c['label']),
                sub: '${kobo(c['students'])} ${kobo(c['students']) == 1 ? 'student' : 'students'}',
                letters: str(c['label']),
                onTap: () => pushSchool<void>(context, StudentsPage(auth: _auth, classId: str(c['id']))),
              ),
          ],
        ),
      ],
    );
  }
}

/// One term: its fees, invoicing it, its invoices.
class _TermPage extends StatelessWidget {
  const _TermPage({required this.auth, required this.term, required this.session});
  final Auth auth;
  final Map<String, dynamic> term;
  final Map<String, dynamic> session;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final current = str(term['state']) == 'current';
    final name = '${str(term['name'])} · ${str(session['label'])}';
    return TkPageScreen(
      title: str(term['name']),
      titleSize: 30,
      backLabel: 'Back to Classes and terms',
      children: [
        Text(
          '${str(session['label'])} · ${dayWords(str(term['starts_on']), weekday: false)} to ${dayWords(str(term['ends_on']), weekday: false)} · fees due ${dayWords(str(term['fees_due_on']), weekday: false)}',
          style: tkText(size: 13.5, color: TkTheme.tokens(context).muted),
        ),
        TkSection(
          inset: 56,
          children: [
            TkListRow(title: 'Fees', sub: 'Each class’s items for the term', icon: 'b-fees', square: const Color(0xFF8E8E93), onTap: () => openClassFees(context, auth, term, name)),
            if (current && auth.schoolManage)
              TkListRow(
                title: 'Invoice the term',
                sub: 'One invoice a student, from their class’s fees',
                icon: 'b-receipt',
                square: look.lime,
                onTap: () => showTkSheet<bool>(context, (_) => InvoiceTermSheet(auth: auth, term: term, termName: name)),
              ),
            if (str(term['state']) != 'planned')
              TkListRow(title: 'Invoices', sub: 'Who has been billed, and what is still due', icon: 'b-receipt', square: look.teal, onTap: () => pushSchool<void>(context, TermInvoicesPage(auth: auth, term: term))),
          ],
        ),
      ],
    );
  }
}

/// Adding a class, a term or a session.
class _AddSheet extends StatefulWidget {
  const _AddSheet({required this.auth, required this.what, required this.sessions, required this.nextOrder});
  final Auth auth;
  final String what;
  final List<Map<String, dynamic>> sessions;
  final int nextOrder;

  @override
  State<_AddSheet> createState() => _AddSheetState();
}

class _AddSheetState extends State<_AddSheet> {
  final _a = TextEditingController();
  final _b = TextEditingController();
  late final _order = TextEditingController(text: '${widget.nextOrder}');
  late String _session = widget.sessions.isEmpty ? '' : str(widget.sessions.last['id']);
  final _dates = <String, DateTime?>{'starts_on': null, 'ends_on': null, 'fees_due_on': null};
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _a.addListener(() => setState(() {}));
    if (widget.what == 'session') {
      final y = DateTime.now().year;
      _a.text = '$y/${y + 1}';
    }
  }

  @override
  void dispose() {
    _a.dispose();
    _b.dispose();
    _order.dispose();
    super.dispose();
  }

  bool get _ready => switch (widget.what) {
    'term' => _a.text.trim().isNotEmpty && _session.isNotEmpty && !_dates.values.contains(null),
    _ => _a.text.trim().isNotEmpty,
  };

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      final (path, body) = switch (widget.what) {
        'class' => ('$schoolApi/classes', {'name': _a.text.trim(), 'arm': _b.text.trim(), 'order': int.tryParse(_order.text.trim()) ?? widget.nextOrder}),
        'session' => ('$schoolApi/sessions', {'label': _a.text.trim()}),
        _ => ('$schoolApi/sessions/$_session/terms', {'name': _a.text.trim(), for (final e in _dates.entries) e.key: dayKey(e.value!)}),
      };
      await widget.auth.transport.send('POST', path, body: body);
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  Widget _date(String key, String label) {
    final t = TkTheme.tokens(context);
    final d = _dates[key];
    return TkKitField(
      label: label,
      field: false,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: () async {
          final now = DateTime.now();
          final p = await showDatePicker(context: context, initialDate: d ?? now, firstDate: DateTime(now.year - 1), lastDate: DateTime(now.year + 2));
          if (p != null) setState(() => _dates[key] = p);
        },
        child: Semantics(
          button: true,
          label: label,
          value: d == null ? 'Choose' : dayWords(dayKey(d)),
          excludeSemantics: true,
          child: Container(
            constraints: const BoxConstraints(minHeight: 46),
            padding: const EdgeInsets.symmetric(horizontal: 12),
            decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(14)),
            child: Row(
              children: [
                Expanded(child: Text(d == null ? 'Choose' : dayWords(dayKey(d)), style: tkText(size: 15, color: d == null ? t.muted : t.text))),
                TkIcon('b-calendar', size: 18, color: t.text),
              ],
            ),
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final title = {'class': 'Add a class', 'term': 'Add a term', 'session': 'Add a session'}[widget.what]!;
    return TkFormSheet(
      title: title,
      crumb: 'Classes and terms',
      action: _busy ? 'Adding…' : 'Add',
      onAction: _busy || !_ready ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        if (widget.what == 'class')
          TkFormGroup(children: [
            TkKitField(label: 'Name', hint: 'JSS 1, Primary 4', child: TkTextBox(controller: _a, max: 40, size: 15, height: 46)),
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: TkKitField(label: 'Arm (optional)', child: TkTextBox(controller: _b, max: 10, size: 15, height: 46))),
                const SizedBox(width: 10),
                Expanded(child: TkKitField(label: 'Order', child: TkTextBox(controller: _order, digits: 3, size: 15, height: 46))),
              ],
            ),
          ]),
        if (widget.what == 'session') TkFormGroup(children: [TkKitField(label: 'School year', hint: 'As 2026/2027', child: TkTextBox(controller: _a, max: 20, size: 15, height: 46))]),
        if (widget.what == 'term') ...[
          if (widget.sessions.length > 1)
            SchoolPills<String>(label: 'Session', choices: [for (final s in widget.sessions) (str(s['id']), str(s['label']))], on: _session, onPick: (v) => setState(() => _session = v)),
          TkFormGroup(children: [
            TkKitField(label: 'Term', hint: 'First term, Second term', child: TkTextBox(controller: _a, max: 40, size: 15, height: 46)),
            Row(children: [Expanded(child: _date('starts_on', 'Starts')), const SizedBox(width: 10), Expanded(child: _date('ends_on', 'Ends'))]),
            _date('fees_due_on', 'Fees due'),
          ]),
        ],
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

// --- A term's fees ---

/// Opens a class's fees for a term, as drawn: a sheet with the classes
/// across its top.
Future<void> openClassFees(BuildContext context, Auth auth, Map<String, dynamic> term, String termName) =>
    showTkSheet<bool>(context, (_) => ClassFeesSheet(auth: auth, term: term, termName: termName));

class _ItemRow {
  _ItemRow(Map<String, dynamic>? i)
    : name = str(i?['name']),
      amount = TextEditingController(text: i == null ? '' : nairaText(kobo(i['amount_kobo']))),
      forWhom = str(i?['pass_through_for']),
      optional = str(i?['kind']) == 'optional';
  String name;
  final TextEditingController amount;
  String forWhom;
  bool optional;
  int get kobo_ => nairaKobo(amount.text) ?? 0;
}

/// A class's fees for a term (as drawn): the classes across the top, each
/// item with its amount, what every student pays, and copying.
class ClassFeesSheet extends StatefulWidget {
  const ClassFeesSheet({super.key, required this.auth, required this.term, required this.termName});
  final Auth auth;
  final Map<String, dynamic> term;
  final String termName;

  @override
  State<ClassFeesSheet> createState() => _ClassFeesSheetState();
}

class _ClassFeesSheetState extends State<ClassFeesSheet> {
  Map<String, dynamic>? _r;
  String _class = '';
  List<_ItemRow> _rows = [];
  bool _changed = false;
  bool _busy = false;
  String? _error;

  String get _base => '$schoolApi/terms/${str(widget.term['id'])}/fees';
  bool get _editable => widget.auth.schoolManage && str(widget.term['state']) != 'past';
  List<Map<String, dynamic>> get _classes => maps(_r?['classes']);
  Map<String, dynamic>? get _c => _classes.where((x) => str(x['class_id']) == _class).firstOrNull;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    for (final r in _rows) {
      r.amount.dispose();
    }
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final r = mapOf(await widget.auth.transport.send('GET', _base));
      if (!mounted) return;
      setState(() {
        _r = r;
        if (_class.isEmpty && _classes.isNotEmpty) _class = str(_classes.first['class_id']);
        _show();
        _error = null;
      });
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  void _show() {
    for (final r in _rows) {
      r.amount.dispose();
    }
    _rows = [
      for (final i in maps(_c?['items'])) _ItemRow(i)..amount.addListener(_touched),
    ];
    _changed = false;
  }

  void _touched() {
    if (mounted && !_changed) setState(() => _changed = true);
  }

  Future<void> _pick(String id) async {
    if (id == _class) return;
    if (_changed) {
      final go = await showTkConfirm(context, title: 'Leave ${str(_c?['class'])}’s changes?', body: 'They are not saved.', ok: 'Leave them', cancel: 'Stay');
      if (!go) return;
    }
    setState(() {
      _class = id;
      _show();
    });
  }

  Future<void> _put(Future<Object?> Function() f, String done) async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await f());
      if (!mounted) return;
      final drafts = kobo(r?['drafts_updated']);
      said(context, drafts > 0 ? '$done; $drafts drafts updated' : done);
      setState(() => _busy = false);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  void _save() {
    final bad = _rows.where((r) => (nairaKobo(r.amount.text) ?? -1) < 0).firstOrNull;
    if (bad != null) {
      setState(() => _error = '${bad.name} needs an amount.');
      return;
    }
    _put(
      () => widget.auth.transport.send('PUT', '$_base/${Uri.encodeComponent(_class)}', body: {
        'items': [
          for (final r in _rows)
            {'name': r.name, 'amount_kobo': r.kobo_, 'kind': r.optional ? 'optional' : 'required', if (r.forWhom.isNotEmpty) 'pass_through_for': r.forWhom},
        ],
      }),
      '${str(_c?['class'])}’s fees saved',
    );
  }

  Future<void> _edit(_ItemRow? r) async {
    final got = await showTkSheet<Object>(context, (_) => _ItemSheet(item: r));
    if (!mounted || got == null) return;
    setState(() {
      _changed = true;
      if (got == 'remove' && r != null) {
        _rows.remove(r);
        r.amount.dispose();
      } else if (got is _ItemRow && r == null) {
        _rows.add(got..amount.addListener(_touched));
      }
    });
  }

  Future<void> _copyClass() async {
    final from = await showTkModalChoices(context, title: 'Copy from', choices: [for (final c in _classes.where((c) => str(c['class_id']) != _class)) (str(c['class_id']), str(c['class']))]);
    if (from == null) return;
    await _put(() => widget.auth.transport.send('POST', '$_base/${Uri.encodeComponent(_class)}/copy', body: {'from_term': str(widget.term['id']), 'from_class': from}), 'Copied');
  }

  String _totals() {
    final required = _rows.where((r) => !r.optional).fold(0, (n, r) => n + r.kobo_);
    final pass = _rows.where((r) => !r.optional && r.forWhom.isNotEmpty).fold(0, (n, r) => n + r.kobo_);
    final optional = _rows.where((r) => r.optional).fold(0, (n, r) => n + r.kobo_);
    final whom = {for (final r in _rows) if (r.forWhom.isNotEmpty) r.forWhom}.join(' and ');
    final words = [
      'Every student pays ${naira(required)}${pass > 0 ? ', of which ${naira(pass)} is for $whom' : ''}',
      if (optional > 0) '${naira(optional)} more is optional',
    ].join('; ');
    return '$words.';
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final c = _c;
    final prev = mapOf(_r?['previous_term']);
    final name = str(c?['class']);
    return TkFormSheet(
      title: c == null ? 'Fees' : '$name fees',
      crumb: widget.termName,
      action: !_editable ? 'Done' : (_busy ? 'Saving…' : 'Save $name’s fees'),
      onAction: !_editable ? () => Navigator.of(context).pop() : (_busy || c == null ? null : _save),
      second: _editable ? 'Cancel' : null,
      onSecond: () => Navigator.of(context).pop(),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (_r == null && _error == null) const TkLoading(rows: 3, label: 'Loading fees…'),
        if (_classes.isNotEmpty)
          // As drawn: each class with what its students pay.
          Wrap(
            spacing: 4,
            runSpacing: 4,
            children: [
              for (final x in _classes)
                TapArea(
                  child: Semantics(
                    button: true,
                    selected: str(x['class_id']) == _class,
                    label: '${str(x['class'])}${kobo(x['required_kobo']) > 0 ? ', ${naira(kobo(x['required_kobo']))}' : ', no fees yet'}',
                    excludeSemantics: true,
                    child: GestureDetector(
                      behavior: HitTestBehavior.opaque,
                      onTap: () => _pick(str(x['class_id'])),
                      child: Container(
                        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
                        decoration: BoxDecoration(color: str(x['class_id']) == _class ? look.card : null, borderRadius: BorderRadius.circular(999)),
                        child: Text(
                          '${str(x['class'])}${kobo(x['required_kobo']) > 0 ? ' ${naira(kobo(x['required_kobo']))}' : ''}',
                          style: tkText(size: 13, weight: str(x['class_id']) == _class ? 700 : 600, color: str(x['class_id']) == _class ? t.text : t.muted),
                        ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
        if (c != null) ...[
          Container(
            padding: const EdgeInsets.fromLTRB(14, 4, 14, 12),
            decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
            child: TkTheme.recolour(
              context,
              (x) => x.copyWith(field: look.ground),
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (_rows.isEmpty) Padding(padding: const EdgeInsets.symmetric(vertical: 12), child: Text('No fees yet for $name.', style: tkText(size: 14, color: t.muted))),
                  for (final (i, r) in _rows.indexed)
                    Container(
                      padding: const EdgeInsets.symmetric(vertical: 8),
                      decoration: BoxDecoration(border: i == 0 ? null : Border(top: BorderSide(color: t.border))),
                      child: Row(
                        children: [
                          Expanded(
                            child: TapArea(
                              child: Semantics(
                                button: _editable,
                                label: '${r.name}, ${r.optional ? 'optional' : 'required'}${r.forWhom.isEmpty ? '' : ', collected for ${r.forWhom}'}${_editable ? ': change' : ''}',
                                excludeSemantics: true,
                                child: GestureDetector(
                                  behavior: HitTestBehavior.opaque,
                                  onTap: _editable ? () => _edit(r).then((_) => setState(() {})) : null,
                                  child: Column(
                                    crossAxisAlignment: CrossAxisAlignment.start,
                                    children: [
                                      Text(r.name, style: tkText(size: 15.5, weight: 500, lineHeight: 1.3, color: t.text)),
                                      Text(
                                        [r.optional ? 'Optional' : 'Required', if (r.forWhom.isNotEmpty) 'collected for ${r.forWhom}'].join(' · '),
                                        style: tkText(size: 12.5, lineHeight: 1.3, color: t.muted),
                                      ),
                                    ],
                                  ),
                                ),
                              ),
                            ),
                          ),
                          const SizedBox(width: 10),
                          SizedBox(
                            width: 136,
                            child: Semantics(
                              label: '${r.name}, amount',
                              child: TkTextBox(controller: r.amount, money: true, prefix: '₦', size: 15, height: 44, enabled: _editable),
                            ),
                          ),
                        ],
                      ),
                    ),
                  if (_editable)
                    Padding(
                      padding: const EdgeInsets.only(top: 8),
                      child: Align(alignment: Alignment.centerLeft, child: TkLink(label: '+ Add an item', bold: true, onTap: () => _edit(null))),
                    ),
                ],
              ),
            ),
          ),
          Text(_totals(), style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
          if (_editable)
            TkSection(
              inset: 56,
              children: [
                if (prev != null)
                  TkListRow(
                    title: 'Copy from ${str(prev['name'])}',
                    sub: str(prev['session']),
                    icon: 'b-copy',
                    square: look.violet,
                    onTap: _busy ? null : () => _put(() => widget.auth.transport.send('POST', '$_base/${Uri.encodeComponent(_class)}/copy', body: const {}), 'Copied'),
                  ),
                if (_classes.length > 1) TkListRow(title: 'Copy from another class', icon: 'b-copy', square: look.violet, onTap: _busy ? null : _copyClass),
              ],
            ),
        ],
      ],
    );
  }
}

/// One fee item: its name, whether every student pays it, and who it is
/// collected for; answers the item (new), 'remove', or null.
class _ItemSheet extends StatefulWidget {
  const _ItemSheet({required this.item});
  final _ItemRow? item;

  @override
  State<_ItemSheet> createState() => _ItemSheetState();
}

class _ItemSheetState extends State<_ItemSheet> {
  late final _name = TextEditingController(text: widget.item?.name ?? '');
  late final _amount = TextEditingController(text: widget.item?.amount.text ?? '');
  late final _for = TextEditingController(text: widget.item?.forWhom ?? '');
  late bool _optional = widget.item?.optional ?? false;

  @override
  void initState() {
    super.initState();
    _name.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _name.dispose();
    _amount.dispose();
    _for.dispose();
    super.dispose();
  }

  void _done() {
    final it = widget.item;
    if (it != null) {
      it
        ..name = _name.text.trim()
        ..forWhom = _for.text.trim()
        ..optional = _optional;
      Navigator.of(context).pop(it);
      return;
    }
    Navigator.of(context).pop(
      _ItemRow({'name': _name.text.trim(), 'amount_kobo': nairaKobo(_amount.text) ?? 0, 'kind': _optional ? 'optional' : 'required', 'pass_through_for': _for.text.trim()}),
    );
  }

  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: widget.item == null ? 'An item' : widget.item!.name,
    crumb: 'Fees',
    action: widget.item == null ? 'Add it' : 'Done',
    onAction: _name.text.trim().isEmpty ? null : _done,
    second: widget.item == null ? 'Cancel' : 'Remove',
    secondDanger: widget.item != null,
    onSecond: () => Navigator.of(context).pop(widget.item == null ? null : 'remove'),
    children: [
      TkFormGroup(
        children: [
          TkKitField(label: 'Item', child: TkTextBox(controller: _name, max: 80, size: 15, height: 46)),
          if (widget.item == null) TkKitField(label: 'Amount', child: TkTextBox(controller: _amount, money: true, prefix: '₦', size: 15, height: 46)),
          TkChips<bool>(label: 'Who pays', padding: EdgeInsets.zero, chips: const [(false, 'Every student'), (true, 'Optional')], on: _optional, onPick: (v) => setState(() => _optional = v)),
          TkKitField(label: 'Collected for (optional)', hint: 'WAEC, NECO: not the school’s income.', child: TkTextBox(controller: _for, max: 60, size: 15, height: 46)),
        ],
      ),
    ],
  );
}

// --- Invoicing the term ---

class InvoiceTermSheet extends StatefulWidget {
  const InvoiceTermSheet({super.key, required this.auth, required this.term, required this.termName});
  final Auth auth;
  final Map<String, dynamic> term;
  final String termName;

  @override
  State<InvoiceTermSheet> createState() => _InvoiceTermSheetState();
}

class _InvoiceTermSheetState extends State<InvoiceTermSheet> {
  Map<String, dynamic>? _plan;
  bool _send = true;
  bool _busy = false;
  String? _error;

  String get _path => '$schoolApi/terms/${str(widget.term['id'])}/invoice';

  @override
  void initState() {
    super.initState();
    _check();
  }

  Future<void> _check() async {
    try {
      final r = mapOf(await widget.auth.transport.send('POST', _path, body: const {}));
      if (mounted) setState(() => (_plan = mapOf(r?['plan']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _confirm({bool drafts = false}) async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await widget.auth.transport.send('POST', _path, body: {'confirm': str(_plan!['hash']), 'send': drafts || _send}));
      if (!mounted) return;
      said(context, '${kobo(r?['created'])} invoiced${kobo(r?['sent']) > 0 ? ', ${kobo(r?['sent'])} sent' : ''}${kobo(r?['requested']) > 0 ? '; ${kobo(r?['requested'])} wait for a second person' : ''}');
      Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _plan;
    final n = kobo(p?['to_invoice']);
    final waiting = [
      ('no_contact', 'No guardian to send to', 'b-person'),
      ('needs_approval', 'Waiting for a discount to be approved', 'b-hand'),
      ('no_fees', 'Their class has no fees yet', 'b-fees'),
      ('nothing_to_pay', 'Nothing to pay', 'b-check'),
    ];
    return TkFormSheet(
      title: 'Invoice ${str(widget.term['name'])}',
      crumb: widget.termName.split(' · ').last,
      action: _busy ? 'Invoicing…' : (n == 0 ? 'Nothing to invoice' : (_send ? 'Invoice and send $n' : 'Invoice $n as drafts')),
      onAction: _busy || p == null || n == 0 ? null : _confirm,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (p == null && _error == null) const TkLoading(rows: 3, label: 'Working out what invoicing will do…'),
        if (p != null) ...[
          TkTopCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('To invoice', color: look.limeInk, trailing: '$n of ${kobo(p['enrolled'])}'),
                const SizedBox(height: 6),
                Text(naira(kobo(p['total_kobo'])), style: tkFigure(context)),
                Text(
                  [
                    if (kobo(p['pass_through_kobo']) > 0) '${naira(kobo(p['pass_through_kobo']))} of it collected for others',
                    '${kobo(p['already_invoiced'])} already invoiced',
                  ].join(' · '),
                  style: tkText(size: 13, color: t.muted),
                ),
              ],
            ),
          ),
          if (maps(p['classes']).isNotEmpty)
            TkFormGroup(children: [TkLines([for (final c in maps(p['classes'])) ('${str(c['class'])} · ${kobo(c['students'])} ${kobo(c['students']) == 1 ? 'student' : 'students'}', naira(kobo(c['total_kobo'])))])]),
          if (waiting.any((w) => maps(p[w.$1]).isNotEmpty))
            TkSection(
              inset: 56,
              children: [
                for (final (k, w, i) in waiting)
                  if (maps(p[k]).isNotEmpty)
                    TkListRow(
                      title: w,
                      icon: i,
                      square: look.orange,
                      trailing: QuietValue('${maps(p[k]).length}'),
                      titleLines: 2,
                      // As drawn: the count; who they are, a tap away.
                      onTap: () async {
                        final id = await showTkModalChoices(context, title: w, choices: [for (final x in maps(p[k])) (str(x['student_id']), nameOr(x))]);
                        if (id != null && context.mounted) await pushSchool<void>(context, StudentPage(auth: widget.auth, id: id));
                      },
                    ),
              ],
            ),
          if (n > 0) TkChips<bool>(label: 'Then', padding: EdgeInsets.zero, chips: const [(false, 'Keep as drafts'), (true, 'Send now by SMS and email')], on: _send, onPick: (v) => setState(() => _send = v)),
        ],
      ],
    );
  }
}

/// The term's invoices: who has been billed and what is still due.
class TermInvoicesPage extends StatefulWidget {
  const TermInvoicesPage({super.key, required this.auth, required this.term});
  final Auth auth;
  final Map<String, dynamic> term;

  @override
  State<TermInvoicesPage> createState() => _TermInvoicesPageState();
}

class _TermInvoicesPageState extends State<TermInvoicesPage> {
  List<Map<String, dynamic>>? _rows;
  String _status = '';
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = mapOf(await widget.auth.transport.send('GET', '$schoolApi/terms/${str(widget.term['id'])}/invoices', query: {if (_status.isNotEmpty) 'status': _status, if (widget.auth.mayReveal) 'reveal': 'true'}));
      if (mounted) setState(() => (_rows = maps(r?['invoices']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final rows = _rows;
    final billed = (rows ?? const []).fold(0, (n, r) => n + kobo(r['total_kobo']));
    final due = (rows ?? const []).fold(0, (n, r) => n + kobo(r['due_kobo']));
    return TkPageScreen(
      title: 'Invoices',
      titleSize: 30,
      backLabel: 'Back to Classes and terms',
      onRefresh: _load,
      children: [
        SchoolPills<String>(
          label: 'Status',
          choices: const [('', 'All'), ('draft', 'Draft'), ('sent', 'Sent'), ('part_paid', 'Part-paid'), ('overdue', 'Overdue'), ('paid', 'Paid')],
          on: _status,
          onPick: (v) {
            setState(() => (_status = v, _rows = null));
            _load();
          },
        ),
        if (_error != null) TkErrorBox(_error!),
        if (rows == null && _error == null) const TkLoading(rows: 4, label: 'Loading invoices…'),
        if (rows != null) Text('${rows.length} invoices · ${naira(billed)} billed · ${naira(due)} still due', style: tkText(size: 13, color: TkTheme.tokens(context).muted)),
        if (rows != null)
          TkSection(
            empty: 'No invoices here.',
            children: [
              for (final r in rows)
                TkListRow(
                  title: nameOr(r),
                  sub: '${str(r['number'])} · ${str(r['class'])}',
                  amount: naira(kobo(r['due_kobo']) > 0 ? kobo(r['due_kobo']) : kobo(r['total_kobo'])),
                  status: {'draft': 'Draft', 'sent': 'Sent', 'part_paid': 'Part-paid', 'overdue': 'Overdue', 'paid': 'Paid', 'void': 'Void'}[str(r['status'])] ?? str(r['status']),
                  statusTone: str(r['status']) == 'paid' ? CardTone.lime : (str(r['status']) == 'overdue' ? CardTone.danger : (str(r['status']) == 'part_paid' ? CardTone.warn : CardTone.plain)),
                  onTap: () => pushSchool<void>(context, StudentPage(auth: widget.auth, id: str(r['student_id']))),
                ),
            ],
          ),
      ],
    );
  }
}

// --- Import students ---

class ImportStudentsPage extends StatefulWidget {
  const ImportStudentsPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<ImportStudentsPage> createState() => _ImportStudentsPageState();
}

class _ImportStudentsPageState extends State<ImportStudentsPage> {
  String? _name;
  String? _csv;
  Map<String, dynamic>? _plan;
  Map<String, dynamic>? _refused;
  bool _busy = false;
  String? _error;

  Future<void> _pick() async {
    final f = await pickStatement();
    if (f == null) return;
    setState(() => (_name = f.name, _csv = utf8.decode(f.bytes, allowMalformed: true), _plan = null, _refused = null, _error = null, _busy = true));
    try {
      final r = mapOf(await widget.auth.transport.send('POST', '$schoolApi/students/import', body: {'csv': _csv}));
      if (mounted) setState(() => (_plan = r, _busy = false));
    } on ApiError catch (e) {
      if (mounted) setState(() => (_refused = e.body, _error = e.message, _busy = false));
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await widget.auth.transport.send('POST', '$schoolApi/students/import', body: {'csv': _csv, 'confirm': str(_plan!['hash'])}));
      if (!mounted) return;
      said(context, '${kobo(r?['added'])} added, ${kobo(r?['updated'])} updated');
      Navigator.of(context).pop();
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _plan;
    final problems = maps(_refused?['problems']);
    final n = kobo(p?['added']) + kobo(p?['updated']);
    // As drawn: a sheet over Students, the file first, Not now and Save.
    return TkFormSheet(
      title: 'Import students',
      crumb: 'Students',
      action: _busy && p != null ? 'Saving…' : (p == null ? 'Save students' : 'Save $n students'),
      onAction: _busy || p == null || n == 0 ? null : _save,
      second: 'Not now',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        Semantics(
          button: true,
          label: _name == null ? 'Choose the CSV file' : 'Choose another file; $_name chosen',
          onTap: _busy ? null : _pick,
          excludeSemantics: true,
          child: GestureDetector(
            onTap: _busy ? null : _pick,
            child: Container(
              padding: const EdgeInsets.all(18),
              decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(18), border: Border.all(color: t.borderStrong, width: 1.2)),
              child: Column(
                children: [
                  TkIcon('b-upload', size: 26, stroke: 2, color: look.limeInk),
                  const SizedBox(height: 6),
                  Text(_name ?? 'Choose the CSV file', style: tkText(size: 15, weight: 700, color: t.text)),
                  if (_busy) Text('Checking…', style: tkText(size: 12.5, color: t.muted)),
                  if (p != null && !_busy) Text('${maps(p['students']).length} lines · checked', style: tkText(size: 12.5, color: t.muted)),
                ],
              ),
            ),
          ),
        ),
        Text(
          'Name, admission number, class, guardian’s name and phone; email, relationship and date of birth if you have them. A student with two guardians is two lines. A file with any bad line adds nothing.',
          style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
        ),
        if (_error != null) TkErrorBox(_error!),
        if (problems.isNotEmpty)
          TkSection(
            title: 'What to fix',
            children: [
              for (final pr in problems.take(30)) TkListRow(title: 'Line ${kobo(pr['row'])}', sub: str(pr['why'])),
              if (kobo(_refused?['total']) > 30) TkListRow(title: 'And ${kobo(_refused?['total']) - 30} more'),
            ],
          ),
        if (p != null) ...[
          if (maps(p['families']).isNotEmpty)
            TkSection(
              title: 'Families, by guardians’ phones',
              children: [
                for (final f in maps(p['families']).take(20))
                  TkListRow(
                    title: maps(f['guardians']).map((g) => str(g['name'])).join(', '),
                    sub: '${f['existing'] == true ? 'A family already here' : 'A new family'} · ${(f['students'] as List? ?? const []).join(', ')}',
                    letters: maps(f['guardians']).map((g) => str(g['name'])).firstOrNull,
                  ),
              ],
            ),
          TkFormGroup(children: [TkLines([('To add', '${kobo(p['added'])}'), ('To update', '${kobo(p['updated'])}')])]),
        ],
      ],
    );
  }
}

// --- Account numbers ---

class AccountNumbersPage extends StatefulWidget {
  const AccountNumbersPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<AccountNumbersPage> createState() => _AccountNumbersPageState();
}

class _AccountNumbersPageState extends State<AccountNumbersPage> {
  Map<String, dynamic>? _plan;
  Map<String, dynamic>? _job;
  Timer? _poll;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _check();
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _check() async {
    try {
      final r = mapOf(await widget.auth.transport.send('POST', '$schoolApi/accounts/issue', body: const {}));
      final jobs = maps(mapOf(await widget.auth.transport.send('GET', '$schoolApi/accounts/jobs'))?['jobs']);
      if (!mounted) return;
      setState(() => (_plan = mapOf(r?['plan']), _job = jobs.firstOrNull, _error = null));
      if (str(_job?['status']) == 'running') _follow();
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  void _follow() {
    _poll?.cancel();
    _poll = Timer.periodic(const Duration(seconds: 2), (_) async {
      try {
        final j = mapOf(await widget.auth.transport.send('GET', '$schoolApi/accounts/jobs/${str(_job?['id'])}'));
        final job = mapOf(j?['job']) ?? j;
        if (!mounted) return;
        setState(() => _job = job);
        if (str(job?['status']) != 'running') {
          _poll?.cancel();
          await _check();
        }
      } on Object {
        _poll?.cancel();
      }
    });
  }

  Future<void> _issue() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await widget.auth.transport.send('POST', '$schoolApi/accounts/issue', body: {'confirm': str(_plan!['hash'])}));
      if (!mounted) return;
      setState(() => (_job = mapOf(r?['job']) ?? r, _busy = false));
      _follow();
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _plan;
    final issuer = mapOf(p?['issuer']);
    final active = kobo(p?['active']);
    final issued = kobo(p?['already_issued']);
    final job = _job;
    final running = str(job?['status']) == 'running';
    return TkPageScreen(
      title: 'Account numbers',
      titleSize: 30,
      backLabel: 'Back to School',
      onRefresh: _check,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (p == null && _error == null) const TkLoading(rows: 3, label: 'Checking who gets one…'),
        if (p != null) ...[
          TkTopCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Issued', color: look.limeInk, trailing: str(issuer?['connection'])),
                const SizedBox(height: 6),
                Text('$issued of $active', style: tkFigure(context)),
                Text('Each child’s own account; a transfer into it lands on them', style: tkText(size: 13, color: t.muted)),
                const SizedBox(height: 10),
                TkProgress(active == 0 ? 0 : issued / active, color: look.lime, semanticsLabel: 'Issued of all'),
              ],
            ),
          ),
          if (running && job != null)
            TkCallout(
              leading: TkSquare('b-clock', color: look.teal),
              title: 'Issuing now: ${kobo(job['issued'])} of ${kobo(job['total'])}',
              sub: '${kobo(job['failed'])} not issued · ${kobo(job['pending'])} waiting on the rail',
              color: look.teal,
            ),
          TkFormGroup(children: [TkLines([('To issue now', '${kobo(p['to_issue'])}'), ('Waiting on the rail', '${maps(p['waiting']).length}'), ('Cannot yet', '${maps(p['cannot']).length}')])]),
          if (maps(p['cannot']).isNotEmpty)
            TkSection(
              title: 'Cannot be issued yet',
              children: [for (final c in maps(p['cannot']).take(30)) TkListRow(title: str(c['admission_number']), sub: str(c['reason']), onTap: () => pushSchool<void>(context, StudentPage(auth: widget.auth, id: str(c['student_id']))))],
            ),
          if (str(issuer?['requirement']).isNotEmpty) Text(str(issuer?['requirement']), style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
          if (str(issuer?['fee']).isNotEmpty) Text('The rail charges per payment, not per account: ${str(issuer?['fee'])}.', style: tkText(size: 13, color: t.muted)),
          if (widget.auth.schoolManage && kobo(p['to_issue']) > 0 && !running)
            TkCta(label: _busy ? 'Issuing…' : 'Issue ${kobo(p['to_issue'])} account numbers', onTap: _busy ? null : _issue),
        ],
      ],
    );
  }
}

// --- Promotion ---

class PromotionPage extends StatefulWidget {
  const PromotionPage({super.key, required this.auth, required this.session});
  final Auth auth;
  final Map<String, dynamic> session;

  @override
  State<PromotionPage> createState() => _PromotionPageState();
}

class _PromotionPageState extends State<PromotionPage> {
  Map<String, dynamic>? _p;
  final _exceptions = <String, String>{};
  bool _busy = false;
  String? _error;

  String get _path => '$schoolApi/sessions/${str(widget.session['id'])}/promotion';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = mapOf(await widget.auth.transport.send('GET', '$_path${widget.auth.reveal()}'));
      if (mounted) setState(() => (_p = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _promote() async {
    final ok = await showTkConfirm(context, title: 'Promote ${str(widget.session['label'])} now?', body: 'Every class moves up once, with the exceptions you chose. It happens once.', ok: 'Promote', cancel: 'Not now');
    if (!ok) return;
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await widget.auth.transport.send('POST', _path, body: {'exceptions': _exceptions}));
      if (!mounted) return;
      setState(() => (_p = r, _busy = false));
      said(context, '${str(widget.session['label'])} is promoted');
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final p = _p;
    final done = p?['done'] == true;
    String action(Map<String, dynamic> s) => _exceptions[str(s['id'])] ?? str(s['action']);
    String words(String a, Map<String, dynamic> s) => switch (a) {
      'repeat' => 'Repeat',
      'leave' => 'Leave',
      'graduate' => 'Graduate',
      _ => str(s['next']).isEmpty ? 'Move up' : 'Move up to ${str(s['next'])}',
    };
    return TkPageScreen(
      title: 'Promotion',
      titleSize: 30,
      backLabel: 'Back to Classes and terms',
      children: [
        // As drawn: what promotion does, under the title.
        Text(
          'At the end of ${str(widget.session['label']).isEmpty ? 'a school year' : str(widget.session['label'])} every class moves up once. Choose who repeats, leaves or graduates first; it happens once.',
          style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted),
        ),
        if (_error != null) TkErrorBox(_error!),
        if (p == null && _error == null) const TkLoading(rows: 4, label: 'Loading classes…'),
        if (p != null) ...[
          if (done) Text('Promoted ${dayWords(str(p['promoted_at']))}.', style: tkText(size: 14, color: t.muted)),
          // As drawn: each class and where it goes, an arrow between.
          TkSection(
            inset: 16,
            children: [
              for (final c in maps(p['classes']))
                _MoveRow(from: str(c['class']), to: str(c['next']).isEmpty ? 'Graduate' : str(c['next']), count: kobo(c['students'])),
            ],
          ),
          if (!done)
            TkSection(
              title: 'Exceptions',
              children: [
                for (final s in maps(p['students']))
                  TkListRow(
                    title: nameOr(s),
                    sub: studentLine(s),
                    trailing: _ChoicePill(
                      label: words(action(s), s).startsWith('Move up') ? 'Move up' : words(action(s), s),
                      semantics: '${nameOr(s)}: ${words(action(s), s)}; change',
                      onTap: () async {
                        final v = await showTkModalChoices(context, title: nameOr(s), value: action(s), choices: [('promote', words('promote', s)), ('repeat', 'Repeat'), ('leave', 'Leave'), ('graduate', 'Graduate')]);
                        if (v != null) setState(() => _exceptions[str(s['id'])] = v);
                      },
                    ),
                  ),
              ],
            ),
          if (!done && widget.auth.schoolManage) TkCta(label: _busy ? 'Promoting…' : 'Promote ${str(widget.session['label'])}…', onTap: _busy ? null : _promote),
        ],
      ],
    );
  }
}

// --- School settings ---

class SchoolSettingsPage extends StatefulWidget {
  const SchoolSettingsPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<SchoolSettingsPage> createState() => _SchoolSettingsPageState();
}

class _SchoolSettingsPageState extends State<SchoolSettingsPage> {
  Map<String, dynamic>? _s;
  String? _error;

  Auth get _auth => widget.auth;
  bool get _owner => (_auth.me?.roles as List?)?.contains('owner') ?? false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = mapOf(await _auth.transport.send('GET', '/v1/settings/schools'));
      if (mounted) setState(() => (_s = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _put(Map<String, Object?> change, String done) async {
    try {
      final r = mapOf(await _auth.transport.send('PUT', '/v1/settings/schools', body: {'enabled': _s?['enabled'] ?? true, ...change}));
      if (!mounted) return;
      setState(() => _s = r ?? _s);
      said(context, done);
      if (r == null) await _load();
    } on Object catch (e) {
      if (mounted) said(context, errorWords(e), error: true);
    }
  }

  Future<void> _number(String title, String hint, String key, {bool naira_ = true, bool clearable = false, int? max}) async {
    final v = await showTkSheet<String>(context, (_) => _NumberSheet(title: title, hint: hint, value: _s?[key] == null ? '' : '${_s![key]}', money: naira_, clearable: clearable));
    if (v == null) return;
    if (v.isEmpty && clearable) {
      await _put({key: null}, 'Saved');
      return;
    }
    final n = int.tryParse(v.replaceAll(RegExp(r'[^\d]'), ''));
    if (n == null || (max != null && n > max)) return;
    await _put({key: n}, 'Saved');
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final s = _s;
    final owner = _owner;
    final cash = s?['cash_threshold_naira'];
    final issuing = mapOf(s?['issuing']);
    final wording = mapOf(s?['reminder_wording']) ?? const {};
    return TkPageScreen(
      title: 'School settings',
      titleSize: 30,
      backLabel: 'Back to School',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (s == null && _error == null) const TkLoading(rows: 4, label: 'Loading settings…'),
        if (s != null && !owner) Text('Only an owner changes these.', style: tkText(size: 13.5, color: TkTheme.tokens(context).muted)),
        if (s != null) ...[
          TkSection(
            inset: 56,
            children: [
              TkListRow(
                title: 'Schools',
                sub: s['enabled'] == true ? 'On: students, fees, clearance, the gate' : 'Off',
                icon: 'b-school',
                square: look.violet,
                trailing: TkSwitch(label: 'Schools', on: s['enabled'] == true, onChanged: owner ? (v) => _put({'enabled': v}, v ? 'Schools is on' : 'Schools is off') : null),
              ),
            ],
          ),
          TkSection(
            title: 'A second person',
            inset: 56,
            children: [
              TkListRow(
                title: 'Cash at or above',
                icon: 'b-cash',
                square: look.orange,
                trailing: QuietValue(cash == null ? 'Never needed' : naira(kobo(cash) * 100)),
                onTap: owner ? () => _number('Cash at or above', 'Cash posted at or above this amount waits for a second person. Empty: never.', 'cash_threshold_naira', clearable: true) : null,
              ),
              TkListRow(
                title: 'Discounts at or above',
                icon: 'b-hand',
                square: look.orange,
                trailing: QuietValue(naira(kobo(s['discount_threshold_naira']) * 100)),
                onTap: owner ? () => _number('Discounts at or above', 'A discount at or above this amount waits for a second person.', 'discount_threshold_naira') : null,
              ),
            ],
          ),
          TkSection(
            title: 'Families',
            inset: 56,
            children: [
              TkListRow(
                title: 'The family page link lasts',
                icon: 'b-family',
                square: look.blue,
                trailing: QuietValue('${kobo(s['family_link_days'])} days'),
                onTap: owner ? () => _number('The family page link lasts', 'Days, 1 to 30.', 'family_link_days', naira_: false, max: 30) : null,
              ),
              TkListRow(
                title: 'Siblings share credit',
                sub: 'What one pays over goes to a brother’s or sister’s fees',
                icon: 'b-family',
                square: look.blue,
                trailing: TkSwitch(
                  label: 'Siblings share credit',
                  on: issuing?['siblings_share_credit'] == true,
                  onChanged: owner && issuing != null ? (v) => _put({'issuing': {...issuing, 'siblings_share_credit': v}}, 'Saved') : null,
                ),
              ),
              TkListRow(
                title: 'Grace on a plan’s instalment',
                icon: 'b-plan',
                square: look.red,
                trailing: QuietValue('${kobo(s['plan_grace_days'])} days'),
                onTap: owner ? () => _number('Grace on a plan’s instalment', 'Days, 0 to 60, before an instalment is behind.', 'plan_grace_days', naira_: false, max: 60) : null,
              ),
            ],
          ),
          TkSection(
            title: 'Reminder wording',
            inset: 56,
            children: [
              for (final r in rungs)
                TkListRow(
                  title: '${r.n} ${r.name}',
                  icon: 'b-bell',
                  square: look.red,
                  trailing: QuietValue(str(wording['${r.n}']).isEmpty ? 'Default' : 'Yours'),
                  onTap: owner
                      ? () async {
                          final v = await showTkSheet<String>(
                            context,
                            (_) => _WordingSheet(rung: r.n, value: str(wording['${r.n}']).isEmpty ? str(mapOf(s['reminder_defaults'])?['${r.n}']) : str(wording['${r.n}'])),
                          );
                          if (v != null) await _put({'reminder_wording': {...wording, '${r.n}': v}}, 'Saved');
                        }
                      : null,
                ),
            ],
          ),
          TkSection(
            title: 'Account numbers',
            inset: 56,
            children: [
              TkListRow(
                title: 'Issued by',
                icon: 'b-bank',
                square: look.teal,
                trailing: QuietValue(str(issuing?['connection']).isEmpty ? 'Not set' : str(issuing?['connection'])),
                onTap: () => pushSchool<void>(context, AccountNumbersPage(auth: _auth)),
              ),
            ],
          ),
          Text('The school’s wallet is set up once, on the web: the director’s BVN and the business’s details.', style: tkText(size: 13, color: TkTheme.tokens(context).muted)),
        ],
      ],
    );
  }
}

class _NumberSheet extends StatefulWidget {
  const _NumberSheet({required this.title, required this.hint, required this.value, required this.money, required this.clearable});
  final String title;
  final String hint;
  final String value;
  final bool money;
  final bool clearable;

  @override
  State<_NumberSheet> createState() => _NumberSheetState();
}

class _NumberSheetState extends State<_NumberSheet> {
  late final _c = TextEditingController(text: widget.value);

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: widget.title,
    crumb: 'School settings',
    action: 'Save',
    onAction: () => Navigator.of(context).pop(_c.text.trim()),
    second: 'Cancel',
    onSecond: () => Navigator.of(context).pop(),
    children: [
      TkFormGroup(children: [TkKitField(label: widget.money ? 'Amount (₦)' : 'Days', hint: widget.hint, child: TkTextBox(controller: _c, digits: 10, size: 15, height: 46))]),
    ],
  );
}

class _WordingSheet extends StatefulWidget {
  const _WordingSheet({required this.rung, required this.value});
  final int rung;
  final String value;

  @override
  State<_WordingSheet> createState() => _WordingSheetState();
}

class _WordingSheetState extends State<_WordingSheet> {
  late final _c = TextEditingController(text: widget.value);

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: 'Reminder ${widget.rung}, ${rungName(widget.rung).toLowerCase()}',
    crumb: 'School settings',
    action: 'Save',
    onAction: () => Navigator.of(context).pop(_c.text.trim()),
    second: 'Cancel',
    onSecond: () => Navigator.of(context).pop(),
    children: [
      TkFormGroup(children: [TkKitField(label: 'What families are sent', hint: 'Its {placeholders} are filled in for each family.', child: TkTextBox(controller: _c, lines: 6, max: 1000, size: 15))]),
    ],
  );
}

/// A class and where it goes at promotion: "JSS 1A → JSS 2A   32".
class _MoveRow extends StatelessWidget {
  const _MoveRow({required this.from, required this.to, required this.count});
  final String from, to;
  final int count;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      label: '$from to $to, $count students',
      excludeSemantics: true,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 13, 16, 13),
        child: Row(
          children: [
            Text(from, style: tkText(size: 14.5, color: t.muted)),
            Padding(padding: const EdgeInsets.symmetric(horizontal: 6), child: TkIcon('b-arrow', size: 15, stroke: 2, color: t.muted)),
            Expanded(child: Text(to, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14.5, color: t.muted))),
            Text('$count', style: tkText(size: 14.5, weight: 700, color: t.text)),
          ],
        ),
      ),
    );
  }
}

/// A small choice that opens its list: its words and a caret (as drawn).
class _ChoicePill extends StatelessWidget {
  const _ChoicePill({required this.label, required this.semantics, required this.onTap});
  final String label, semantics;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      label: semantics,
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.fromLTRB(12, 7, 9, 7),
          decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(999)),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(label, style: tkText(size: 13, weight: 650, color: t.text)),
              const SizedBox(width: 3),
              TkIcon('b-caret', size: 14, stroke: 2.2, color: t.text),
            ],
          ),
        ),
      ),
    );
  }
}
