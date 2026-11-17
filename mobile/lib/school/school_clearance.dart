// Clearance and the gate (docs/schools.md §12; web SchoolClearance.tsx,
// GateScreen): who is cleared this term and why, clearing one by an
// override nobody sets by hand, and the gate: a name typed, the child
// tapped, CLEARED or SEE THE BURSAR. The gate never shows an amount: the
// server sends none.

import 'dart:async';

import 'package:flutter/material.dart' show showDatePicker;
import 'package:flutter/services.dart' show SystemUiOverlayStyle;
import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../chat/approve.dart' show TkTextBox;
import '../chat/words.dart' show naira;
import '../kit/forms.dart' show errorWords;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading, TkSearchBox;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, TkCta;
import '../kit/tap_area.dart';
import '../theme/look.dart';
import '../chat/words.dart' show CardTone;
import '../chat/thread.dart' show ChatHeadBar;
import '../theme/theme.dart';
import 'school_chasing.dart' show shareCsv;
import 'school_tab.dart' show pushSchool, said;
import 'school_words.dart';
import 'students_page.dart' show SchoolPills, StudentPage, loadClasses;

// --- Clearance ---

class ClearancePage extends StatefulWidget {
  const ClearancePage({super.key, required this.auth});
  final Auth auth;

  @override
  State<ClearancePage> createState() => _ClearancePageState();
}

class _ClearancePageState extends State<ClearancePage> {
  String _show = 'not';
  String _class = '';
  List<Map<String, dynamic>> _classes = const [];
  Map<String, dynamic>? _r;
  String? _error;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    loadClasses(_auth).then((c) {
      if (mounted) setState(() => _classes = c);
    }, onError: (_) {});
    _load();
  }

  Future<void> _load() async {
    try {
      final r = mapOf(await _auth.transport.send('GET', '$schoolApi/clearance', query: {if (_class.isNotEmpty) 'class': _class, if (_auth.mayReveal) 'reveal': 'true'}));
      if (mounted) setState(() => (_r = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _withdraw(Map<String, dynamic> o) async {
    try {
      await _auth.transport.send('POST', '$schoolApi/clearance-overrides/${str(o['id'])}/withdraw', body: const {});
      if (mounted) said(context, 'Override withdrawn');
      await _load();
    } on Object catch (e) {
      if (mounted) said(context, errorWords(e), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final r = _r;
    final totals = mapOf(r?['totals']) ?? const {};
    final all = maps(r?['students']);
    final rows = all.where((s) => _show == 'all' || (_show == 'not' ? str(s['status']) != 'cleared' : str(s['status']) == 'cleared')).toList();
    final n = kobo(totals['students']);
    final cleared = kobo(totals['cleared']);
    final csv = Uri(queryParameters: {if (_class.isNotEmpty) 'class': _class, 'format': 'csv'}).query;
    return TkPageScreen(
      title: 'Clearance',
      titleSize: 30,
      backLabel: 'Back to School',
      onRefresh: _load,
      description: 'Cleared is worked out from the fees, never set by hand: paid in full, nothing to pay, a payment plan in good standing, or an override with its reason.',
      action: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          TkHeadButton(icon: 'b-gate', label: 'The gate', onTap: () => pushSchool<void>(context, GateScreen(auth: _auth))),
          const SizedBox(width: 8),
          TkHeadButton(icon: 'b-share', label: 'Share as a spreadsheet', onTap: () => shareCsv(context, _auth, '$schoolApi/clearance?$csv', 'clearance.csv')),
        ],
      ),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (r == null && _error == null) const TkLoading(rows: 4, label: 'Loading clearance…'),
        if (r != null)
          TkTopCard(
            semanticsLabel: '$cleared of $n cleared',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('Cleared', color: look.limeInk, trailing: str(r['term']).isEmpty ? 'as of today' : str(r['term'])),
                const SizedBox(height: 6),
                Text('$cleared of $n', style: tkFigure(context)),
                Text('${kobo(totals['partly_paid'])} partly paid · ${kobo(totals['not_paid'])} not paid', style: tkText(size: 13, color: t.muted)),
                const SizedBox(height: 10),
                TkProgress(n == 0 ? 0 : cleared / n, color: look.lime, semanticsLabel: 'Cleared of all'),
              ],
            ),
          ),
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
        TkChips<String>(label: 'Show', padding: EdgeInsets.zero, chips: const [('not', 'Not cleared'), ('yes', 'Cleared'), ('all', 'Everyone')], on: _show, onPick: (v) => setState(() => _show = v)),
        if (r != null)
          TkSection(
            empty: _show == 'not' ? 'Everyone is cleared.' : 'Nobody here.',
            children: [
              for (final s in rows) _row(s),
            ],
          ),
      ],
    );
  }

  Widget _row(Map<String, dynamic> s) {
    final st = str(s['status']);
    final o = mapOf(s['override']);
    final reason = str(s['reason']);
    final sub = [
      str(s['class']),
      if (kobo(s['balance_kobo']) > 0 && st != 'cleared') '${naira(kobo(s['balance_kobo']))} left',
      if (st == 'cleared' && reason != 'paid') (clearanceReasons[reason] ?? reason).toLowerCase(),
      if (str(s['student_status']).isNotEmpty && str(s['student_status']) != 'active') str(s['student_status']),
    ].join(' · ');
    return TkListRow(
      title: nameOr(s),
      sub: o == null ? sub : '$sub\nOverride: ${str(o['reason'])} · until ${dayWords(str(o['ends_on']))}',
      status: clearanceWords[st] ?? st,
      statusTone: st == 'cleared' ? CardTone.lime : (st == 'partly_paid' ? CardTone.warn : CardTone.danger),
      trailing: o != null && o['in_force'] == true && _auth.schoolApprove ? TkSmallButton(label: 'Withdraw', semantics: 'Withdraw the override', onPressed: () => _withdraw(o)) : null,
      onTap: () => pushSchool<void>(context, StudentPage(auth: _auth, id: str(s['student_id']))).then((_) => _load()),
    );
  }
}

// --- Clear by override ---

class OverrideSheet extends StatefulWidget {
  const OverrideSheet({super.key, required this.auth, required this.student, this.now});
  final Auth auth;
  final Map<String, dynamic> student;
  final DateTime Function()? now;

  @override
  State<OverrideSheet> createState() => _OverrideSheetState();
}

class _OverrideSheetState extends State<OverrideSheet> {
  final _reason = TextEditingController();
  DateTime? _until;
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

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('POST', '$schoolApi/students/${str(widget.student['id'])}/clearance-override', body: {
        'reason': _reason.text.trim(),
        if (_until != null) 'ends_on': dayKey(_until!),
      });
      if (!mounted) return;
      said(context, '${nameOr(widget.student)} is cleared');
      Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final now = (widget.now ?? DateTime.now)();
    return TkFormSheet(
      title: 'Clear by override',
      crumb: '${nameOr(widget.student)} · ${str(widget.student['class'])}',
      action: _busy ? 'Clearing…' : 'Clear them',
      onAction: _busy || _reason.text.trim().isEmpty ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        Text('They are cleared until the day you choose, whatever they owe. Your name and the reason are kept, and it can be withdrawn.', style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted)),
        TkFormGroup(
          children: [
            TkKitField(label: 'Reason', child: TkTextBox(controller: _reason, max: 300, size: 15, height: 46)),
            TkKitField(
              label: 'Until (optional)',
              field: false,
              hint: 'Left empty: the end of the term.',
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: () async {
                  final d = await showDatePicker(context: context, initialDate: _until ?? now, firstDate: DateTime(now.year, now.month, now.day), lastDate: now.add(const Duration(days: 366)));
                  if (d != null) setState(() => _until = d);
                },
                child: Semantics(
                  button: true,
                  label: 'Until',
                  value: _until == null ? 'The end of the term' : dayWords(dayKey(_until!)),
                  excludeSemantics: true,
                  child: Container(
                    constraints: const BoxConstraints(minHeight: 46),
                    padding: const EdgeInsets.symmetric(horizontal: 12),
                    decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(14)),
                    child: Row(
                      children: [
                        Expanded(child: Text(_until == null ? 'The end of the term' : dayWords(dayKey(_until!)), style: tkText(size: 15, color: t.text))),
                        TkIcon('b-calendar', size: 18, color: t.text),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

// --- The gate ---

class GateScreen extends StatefulWidget {
  const GateScreen({super.key, required this.auth, this.onSignOut});
  final Auth auth;

  /// For someone with the gate role only: the gate is the app.
  final VoidCallback? onSignOut;

  @override
  State<GateScreen> createState() => _GateScreenState();
}

class _GateScreenState extends State<GateScreen> {
  final _q = TextEditingController();
  Timer? _typing;
  List<Map<String, dynamic>> _hits = const [];
  Map<String, dynamic>? _answer;
  final _checked = <({String name, String klass, bool ok, String at})>[];
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _q.addListener(() {
      _typing?.cancel();
      _typing = Timer(const Duration(milliseconds: 300), _search);
    });
  }

  @override
  void dispose() {
    _typing?.cancel();
    _q.dispose();
    super.dispose();
  }

  Future<void> _search() async {
    final q = _q.text.trim();
    if (q.length < 3) {
      if (mounted) setState(() => (_hits = const [], _error = null));
      return;
    }
    try {
      final r = mapOf(await widget.auth.transport.send('GET', '/v1/gate/search', query: {'q': q}));
      if (mounted && q == _q.text.trim()) setState(() => (_hits = maps(r?['students']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _check(Map<String, dynamic> h) async {
    setState(() => _busy = true);
    try {
      final a = mapOf(await widget.auth.transport.send('GET', '/v1/gate/students/${Uri.encodeComponent(str(h['student_id']))}'));
      // No amount ever: an answer that has one is not shown.
      if (a == null || a.keys.any((k) => k.contains('kobo') || k.contains('amount'))) throw StateError('The answer could not be read.');
      final now = DateTime.now().toUtc().toIso8601String();
      if (!mounted) return;
      setState(() {
        _answer = a;
        _checked.insert(0, (name: str(a['name']), klass: str(a['class']), ok: str(a['status']) == 'cleared', at: lagosClock(now)));
        if (_checked.length > 20) _checked.removeLast();
        _busy = false;
      });
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  void _again() => setState(() {
    _answer = null;
    _hits = const [];
    _q.clear();
  });

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final pad = MediaQuery.paddingOf(context);
    final school = widget.auth.me?.tenantName ?? 'School';
    final a = _answer;
    final dark = TkTheme.of(context).mode == TkMode.dark;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: TkAppLook(
        child: ColoredBox(
          color: look.ground,
          child: Padding(
            padding: EdgeInsets.fromLTRB(16, pad.top + 10, 16, pad.bottom + 16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  children: [
                    if (widget.onSignOut == null) ...[
                      ChatHeadBar.iconButton('Back', TkIcon('back', size: 22, color: t.text), () => Navigator.of(context).maybePop()),
                      const SizedBox(width: 4),
                    ],
                    Container(
                      width: 38,
                      height: 38,
                      alignment: Alignment.center,
                      decoration: BoxDecoration(color: const Color(0xFFC8F169), borderRadius: BorderRadius.circular(12)),
                      child: const TkIcon('b-school', size: 20, stroke: 2, color: Color(0xFF0D3B2E)),
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Semantics(
                        header: true,
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(school, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 16, weight: 700, color: t.text)),
                            Text('Gate', style: tkText(size: 12.5, color: t.muted)),
                          ],
                        ),
                      ),
                    ),
                    if (widget.onSignOut != null) TkLink(label: 'Sign out', bold: true, onTap: widget.onSignOut),
                  ],
                ),
                const SizedBox(height: 14),
                Expanded(child: a == null ? _searching(t, look) : _verdict(a)),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _searching(TkTokens t, TkLook look) => ListView(
    padding: EdgeInsets.zero,
    children: [
      TkSearchBox(controller: _q, hint: 'Child’s name', fill: look.card, margin: EdgeInsets.zero),
      const SizedBox(height: 12),
      if (_error != null) TkErrorBox(_error!),
      if (_q.text.trim().length >= 3 && _hits.isEmpty && _error == null) Text('No child by that name.', style: tkText(size: 14, color: t.muted)),
      if (_hits.isNotEmpty)
        TkSection(
          children: [
            for (final h in _hits)
              TkListRow(title: str(h['name']), sub: str(h['class']), onTap: _busy ? null : () => _check(h), semanticsLabel: 'Check ${str(h['name'])}, ${str(h['class'])}'),
          ],
        ),
      if (_checked.isNotEmpty) ...[
        const SizedBox(height: 14),
        Text('JUST CHECKED', style: tkText(size: 11.5, weight: 700, letterSpacing: 0.6, color: t.muted)),
        const SizedBox(height: 8),
        TkSection(
          children: [
            for (final c in _checked)
              TkListRow(
                title: c.name,
                sub: '${c.klass} · ${c.at}',
                icon: c.ok ? 'b-check' : 'b-alert',
                tone: c.ok ? CardTone.lime : CardTone.warn,
                semanticsLabel: '${c.name}, ${c.ok ? 'cleared' : 'see the bursar'}, ${c.at}',
              ),
          ],
        ),
      ],
      const SizedBox(height: 14),
      Text('No amounts are shown at the gate. For the fees themselves, the bursar.', textAlign: TextAlign.center, style: tkText(size: 13, color: t.muted)),
    ],
  );

  Widget _verdict(Map<String, dynamic> a) {
    final ok = str(a['status']) == 'cleared';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Semantics(
          liveRegion: true,
          label: ok ? 'Cleared. Come in. ${str(a['name'])}, ${str(a['class'])}' : 'See the bursar. Please see the bursar before class. ${str(a['name'])}, ${str(a['class'])}',
          excludeSemantics: true,
          child: Container(
            padding: const EdgeInsets.fromLTRB(18, 28, 18, 26),
            decoration: BoxDecoration(color: ok ? const Color(0xFF2F8F4E) : const Color(0xFFE07B24), borderRadius: BorderRadius.circular(26)),
            child: Column(
              children: [
                Container(
                  width: 68,
                  height: 68,
                  alignment: Alignment.center,
                  decoration: const BoxDecoration(color: Color(0x33FFFFFF), shape: BoxShape.circle),
                  child: TkIcon(ok ? 'b-check' : 'b-alert', size: 36, stroke: 2.4, color: const Color(0xFFFFFFFF)),
                ),
                const SizedBox(height: 12),
                Text(ok ? 'CLEARED' : 'SEE THE BURSAR', textAlign: TextAlign.center, style: tkDisplay(size: 30, letterSpacing: -0.5, color: const Color(0xFFFFFFFF))),
                const SizedBox(height: 4),
                Text(ok ? 'Come in.' : 'Please see the bursar before class.', textAlign: TextAlign.center, style: tkText(size: 16, color: const Color(0xFFFFFFFF))),
                const SizedBox(height: 8),
                Text('${str(a['name'])} · ${str(a['class'])}', textAlign: TextAlign.center, style: tkText(size: 14, color: const Color(0xD9FFFFFF))),
              ],
            ),
          ),
        ),
        const SizedBox(height: 16),
        TapArea(child: TkCta(label: 'Check another child', onTap: _again)),
      ],
    );
  }
}
