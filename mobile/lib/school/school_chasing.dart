// Chasing (docs/schools.md §11; web SchoolDebtors.tsx, SchoolFamily.tsx):
// who owes, by how late; reminders on a ladder of three; a promise to pay
// by a day; a payment plan a family asked for, decided.

import 'dart:typed_data';

import 'package:flutter/material.dart' show showDatePicker;
import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../chat/approve.dart' show TkTextBox;
import '../chat/attachments.dart' show handOffFile;
import '../chat/give_task.dart' show TkPillChoice;
import '../chat/words.dart' show naira;
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading;
import '../kit/mobile.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, TkLines, showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'school_tab.dart' show AgeBars, pushSchool, said;
import 'school_words.dart';
import 'students_page.dart' show SchoolPills, StudentPage, loadClasses;

/// The CSV the server makes, handed to the phone to share (Q2).
Future<void> shareCsv(BuildContext context, Auth auth, String path, String name) async {
  try {
    final bytes = await auth.transport.bytes(path);
    await handOffFile(Uint8List.fromList(bytes), name, 'text/csv', null);
  } on Object catch (e) {
    if (context.mounted) said(context, errorWords(e), error: true);
  }
}

// --- Debtors ---

class DebtorsPage extends StatefulWidget {
  const DebtorsPage({super.key, required this.auth, this.bucket});
  final Auth auth;
  final String? bucket;

  @override
  State<DebtorsPage> createState() => _DebtorsPageState();
}

class _DebtorsPageState extends State<DebtorsPage> {
  late String? _bucket = widget.bucket;
  String _has = '';
  String _class = '';
  List<Map<String, dynamic>> _classes = const [];
  Map<String, dynamic>? _r;
  final _chosen = <String>{};
  String? _error;
  bool _busy = false;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    loadClasses(_auth).then((c) {
      if (mounted) setState(() => _classes = c);
    }, onError: (_) {});
    _load();
  }

  Map<String, String> get _query => {
    if (_class.isNotEmpty) 'class': _class,
    'bucket': ?_bucket,
    if (_has.isNotEmpty) 'has': _has,
  };

  Future<void> _load() async {
    try {
      final r = mapOf(await _auth.transport.send('GET', '$schoolApi/debtors', query: {..._query, if (_auth.mayReveal) 'reveal': 'true'}));
      if (mounted) setState(() => (_r = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _check() async {
    setState(() => _busy = true);
    try {
      final r = mapOf(await _auth.transport.send('POST', '$schoolApi/chasing/check', body: const {}));
      if (mounted) said(context, 'Checked: ${kobo(r?['promises_kept'])} promises kept, ${kobo(r?['promises_missed'])} missed, ${kobo(r?['arrears'])} plans behind');
      await _load();
    } on Object catch (e) {
      if (mounted) said(context, errorWords(e), error: true);
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final r = _r;
    final debtors = maps(r?['debtors']);
    final manage = _auth.schoolManage;
    final chosen = debtors.where((d) => _chosen.contains(str(d['student_id']))).toList();
    final days = chosen.fold(0, (n, d) => kobo(d['days_past_due']) > n ? kobo(d['days_past_due']) : n);
    final query = Uri(queryParameters: {..._query, 'format': 'csv'}).query;
    return TkPageScreen(
      title: 'Debtors',
      titleSize: 30,
      backLabel: 'Back to School',
      onRefresh: _load,
      action: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (manage) TkHeadButton(icon: 'b-bolt', label: 'Check promises and plans now', onTap: _busy ? () {} : _check),
          if (manage) const SizedBox(width: 8),
          TkHeadButton(icon: 'b-share', label: 'Share as a spreadsheet', onTap: () => shareCsv(context, _auth, '$schoolApi/debtors?$query', 'debtors.csv')),
        ],
      ),
      children: [
        if (r != null) AgeBars(totals: mapOf(r['totals']) ?? const {}, on: _bucket, onPick: (b) {
          setState(() {
            _bucket = b;
            _chosen.clear();
          });
          _load();
        }),
        SchoolPills<String>(
          label: 'Show',
          choices: const [('', 'Everyone'), ('promise', 'With a promise'), ('missed', 'Missed a promise'), ('plan', 'On a plan'), ('arrears', 'Behind on a plan'), ('none', 'Nothing yet')],
          on: _has,
          onPick: (v) {
            setState(() {
              _has = v;
              _chosen.clear();
            });
            _load();
          },
        ),
        if (_classes.isNotEmpty)
          SchoolPills<String>(
            label: 'Class',
            choices: [('', 'All classes'), for (final c in _classes) (str(c['id']), str(c['label']))],
            on: _class,
            onPick: (v) {
              setState(() {
              _class = v;
              _chosen.clear();
            });
              _load();
            },
          ),
        if (_error != null) TkErrorBox(_error!),
        if (r == null && _error == null) const TkLoading(rows: 4, label: 'Loading who owes…'),
        if (r != null) Text('${kobo(r['students'])} owe ${naira(kobo(r['balance_kobo']))}${_auth.mayReveal ? '' : ' · names hidden'}', style: tkText(size: 13, color: t.muted)),
        if (r != null)
          TkSection(
            inset: 56,
            empty: 'Nobody here owes anything.',
            children: [
              for (final d in debtors)
                TkListRow(
                  title: nameOr(d),
                  sub: [
                    '${str(d['class'])} · ${lateWords(kobo(d['days_past_due']), dueOn: str(d['due_on']))}',
                    _chaseLine(d),
                  ].where((x) => x.isNotEmpty).join('\n'),
                  amount: naira(kobo(d['balance_kobo'])),
                  semanticsLabel: '${nameOr(d)}, ${naira(kobo(d['balance_kobo']))}${_chosen.contains(str(d['student_id'])) ? ', chosen' : ''}',
                  icon: manage ? null : 'b-person',
                  subLines: 3,
                  leading: manage
                      ? _Tick(
                          on: _chosen.contains(str(d['student_id'])),
                          label: 'Choose ${nameOr(d)}',
                          onTap: () => setState(() {
                            final id = str(d['student_id']);
                            if (!_chosen.remove(id) && _chosen.length < 50) _chosen.add(id);
                          }),
                        )
                      : null,
                  onTap: () => pushSchool<void>(context, StudentPage(auth: _auth, id: str(d['student_id']))).then((_) => _load()),
                ),
            ],
          ),
        if (manage && chosen.isNotEmpty)
          _SendBar(
            words: '${chosen.length} ${chosen.length == 1 ? 'family' : 'families'} chosen',
            line: 'Reminder ${rungFor(days)}, ${rungName(rungFor(days)).toLowerCase()}',
            look: look,
            onSend: () async {
              final done = await showTkSheet<bool>(
                context,
                (_) => RemindSheet(auth: _auth, students: chosen, balances: [for (final d in chosen) kobo(d['balance_kobo'])], rung: rungFor(days)),
              );
              if (done == true) {
                setState(_chosen.clear);
                await _load();
              }
            },
          ),
      ],
    );
  }

  String _chaseLine(Map<String, dynamic> d) {
    final last = mapOf(d['last_reminder']);
    final p = mapOf(d['promise']);
    final plan = mapOf(d['plan']);
    return [
      if (last != null) 'Reminder ${kobo(last['rung'])} · ${dayWords(str(last['at']), weekday: false)}${last['nobody'] == true ? ' (nobody reached)' : ''}',
      if (p != null) d['promise_missed'] == true ? 'missed a promise' : 'promised by ${dayWords(str(p['promised_on']))}',
      if (plan != null) str(plan['state']) == 'arrears' ? 'plan behind' : 'on a plan',
    ].join(' · ');
  }
}

class _Tick extends StatelessWidget {
  const _Tick({required this.on, required this.label, required this.onTap});
  final bool on;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Semantics(
    checked: on,
    label: label,
    onTap: onTap,
    excludeSemantics: true,
    child: GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      child: Padding(padding: const EdgeInsets.fromLTRB(0, 8, 4, 8), child: TkAppLook(child: TkCheckbox(checked: on))),
    ),
  );
}

class _SendBar extends StatelessWidget {
  const _SendBar({required this.words, required this.line, required this.look, required this.onSend});
  final String words;
  final String line;
  final TkLook look;
  final VoidCallback onSend;

  @override
  Widget build(BuildContext context) {
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 12, 12, 12),
      decoration: BoxDecoration(color: dark ? const Color(0xFF1D3A2D) : const Color(0xFF0D3B2E), borderRadius: BorderRadius.circular(18)),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(words, style: tkText(size: 15, weight: 700, color: const Color(0xFFFFFFFF))),
                Text(line, style: tkText(size: 12.5, color: const Color(0xBFFFFFFF))),
              ],
            ),
          ),
          TkSmallButton(label: 'Send', semantics: 'Send a reminder', primary: true, onPressed: onSend),
        ],
      ),
    );
  }
}

// --- A reminder ---

class RemindSheet extends StatefulWidget {
  const RemindSheet({super.key, required this.auth, required this.students, required this.balances, this.rung = 1});
  final Auth auth;
  final List<Map<String, dynamic>> students;
  final List<int> balances;
  final int rung;

  @override
  State<RemindSheet> createState() => _RemindSheetState();
}

class _RemindSheetState extends State<RemindSheet> {
  late int _rung = widget.rung;
  bool _busy = false;
  String? _error;
  final _key = uuid4();

  Future<void> _send() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await widget.auth.transport.send(
        'POST',
        '$schoolApi/reminders',
        body: {
          'students': [for (final s in widget.students) str(s['student_id']).isEmpty ? str(s['id']) : str(s['student_id'])],
          'rung': _rung,
        },
        headers: {'Idempotency-Key': '$_key-$_rung'},
      ));
      if (!mounted) return;
      final sent = kobo(r?['sent']);
      final skipped = kobo(r?['skipped']);
      said(context, 'Sent to $sent ${sent == 1 ? 'family' : 'families'}${skipped > 0 ? '; $skipped not sent' : ''}');
      Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final n = widget.students.length;
    final r = rungs.firstWhere((x) => x.n == _rung);
    return TkFormSheet(
      title: 'Send a reminder',
      crumb: n == 1 ? nameOr(widget.students.single) : '$n families',
      action: _busy ? 'Sending…' : (n == 1 ? 'Send it' : 'Send to $n'),
      onAction: _busy ? null : _send,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkChips<int>(label: 'Which reminder', padding: EdgeInsets.zero, chips: [for (final x in rungs) (x.n, '${x.n} ${x.name}')], on: _rung, onPick: (v) => setState(() => _rung = v)),
        Text('${r.says} Each family gets it on WhatsApp, or by SMS where WhatsApp cannot reach them.', style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted)),
        TkFormGroup(children: [TkLines([for (var i = 0; i < n; i++) ('${nameOr(widget.students[i])} · ${str(widget.students[i]['class'])}', naira(widget.balances[i]))])]),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

// --- A promise to pay ---

class PromiseSheet extends StatefulWidget {
  const PromiseSheet({super.key, required this.auth, required this.student, required this.balance, this.now});
  final Auth auth;
  final Map<String, dynamic> student;
  final int balance;
  final DateTime Function()? now;

  @override
  State<PromiseSheet> createState() => _PromiseSheetState();
}

class _PromiseSheetState extends State<PromiseSheet> {
  final _note = TextEditingController();
  late DateTime _day = _days.first;
  bool _busy = false;
  String? _error;

  DateTime get _today {
    final n = (widget.now ?? DateTime.now)();
    return DateTime(n.year, n.month, n.day);
  }

  /// The next five school days.
  List<DateTime> get _days {
    final out = <DateTime>[];
    var d = _today.add(const Duration(days: 1));
    while (out.length < 5) {
      if (d.weekday <= 5) out.add(d);
      d = d.add(const Duration(days: 1));
    }
    return out;
  }

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('POST', '$schoolApi/students/${str(widget.student['id'])}/promise', body: {
        'date': dayKey(_day),
        if (_note.text.trim().isNotEmpty) 'note': _note.text.trim(),
      });
      if (!mounted) return;
      said(context, 'Promise recorded for ${dayWords(dayKey(_day))}');
      Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  Future<void> _pick() async {
    final d = await showDatePicker(context: context, initialDate: _day, firstDate: _today, lastDate: _today.add(const Duration(days: 365)));
    if (d != null) setState(() => _day = d);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final days = _days;
    return TkFormSheet(
      title: 'A promise to pay',
      crumb: '${nameOr(widget.student)} · ${str(widget.student['class'])}',
      action: _busy ? 'Recording…' : 'Record the promise',
      onAction: _busy ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        Text('${naira(widget.balance)} is owed. If it is not paid by the day, the promise is marked missed and reminders carry on.', style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted)),
        Text('PROMISED TO PAY BY', style: tkText(size: 11.5, weight: 700, letterSpacing: 0.6, color: t.muted)),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final d in days) TkPillChoice(label: dayWords(dayKey(d)).split(' ').take(2).join(' '), semantics: dayWords(dayKey(d)), large: true, on: dayKey(d) == dayKey(_day), onTap: () => setState(() => _day = d)),
            if (!days.any((d) => dayKey(d) == dayKey(_day))) TkPillChoice(label: dayWords(dayKey(_day)), large: true, on: true, onTap: _pick),
            TkPillChoice(label: 'Another day', semantics: 'Another day', large: true, on: false, onTap: _pick),
          ],
        ),
        TkFormGroup(children: [TkKitField(label: 'Note (optional)', child: TkTextBox(controller: _note, max: 300, size: 15, height: 46))]),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

// --- A payment plan asked for ---

class PlanSheet extends StatefulWidget {
  const PlanSheet({super.key, required this.auth, required this.plan});
  final Auth auth;
  final Map<String, dynamic> plan;

  @override
  State<PlanSheet> createState() => _PlanSheetState();
}

class _PlanSheetState extends State<PlanSheet> {
  final _note = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _note.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  Future<void> _decide(String d) async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('POST', '$schoolApi/plans/${str(widget.plan['id'])}', body: {
        'decision': d,
        if (_note.text.trim().isNotEmpty) 'note': _note.text.trim(),
      });
      if (!mounted) return;
      said(context, d == 'approved' ? 'Plan approved; the family is told' : 'Plan declined; the family is told');
      Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = widget.plan;
    return TkFormSheet(
      title: 'A payment plan asked for',
      crumb: [nameOr(p), str(p['class'])].where((x) => x.isNotEmpty).join(' · '),
      action: _busy ? 'Saving…' : 'Approve the plan',
      onAction: _busy ? null : () => _decide('approved'),
      second: 'Decline',
      secondDanger: true,
      onSecond: _busy || _note.text.trim().isEmpty ? null : () => _decide('declined'),
      children: [
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(18)),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (str(p['message']).isNotEmpty) Text('“${str(p['message'])}”', style: tkText(size: 14.5, lineHeight: 1.4, color: t.text)),
              const SizedBox(height: 4),
              Text('From the family page · ${maps(p['instalments']).length} instalments · ${dayWords(str(p['created_at']))}', style: tkText(size: 12.5, color: t.muted)),
            ],
          ),
        ),
        // What they asked: each part by its day. Approved as asked; to
        // change the parts, the web.
        TkFormGroup(children: [TkLines([for (final i in maps(p['instalments'])) (naira(kobo(i['amount_kobo'])), 'by ${dayWords(str(i['due_on']))}')])]),
        Text('Approving tells the family at once; declining needs a note, which they are told.', style: tkText(size: 13, color: t.muted)),
        TkFormGroup(children: [TkKitField(label: 'Note to the family (needed to decline)', child: TkTextBox(controller: _note, max: 300, size: 15, height: 46))]),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}
