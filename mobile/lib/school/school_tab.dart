// The School tab (docs/spec/mobile-app-look.md, "The school"; web
// SchoolOverview.tsx): the term's fees collected against what was
// expected, this week, what is owed and how late, how parents paid, who
// owes most, the last close; then the school's pages. In Orders' place in
// the bar for a business with Schools on.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../chat/words.dart' show naira;
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading, TkEmpty;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'school_words.dart';
import 'students_page.dart';
import 'school_money.dart';
import 'school_chasing.dart';
import 'school_clearance.dart';
import 'school_setup.dart';
import 'close_page.dart';

/// Pushes a school page.
Future<T?> pushSchool<T>(BuildContext context, Widget page) => Navigator.of(context).push<T>(TkRoute<T>(pageBuilder: (_, _, _) => page));

class SchoolTab extends StatefulWidget {
  const SchoolTab({super.key, required this.auth});
  final Auth auth;

  @override
  State<SchoolTab> createState() => _SchoolTabState();
}

class _SchoolTabState extends State<SchoolTab> {
  Map<String, dynamic>? _ov;
  int? _waiting;
  String? _error;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = mapOf(await _auth.transport.send('GET', '$schoolApi/overview${_auth.reveal()}'));
      int? waiting;
      try {
        final p = mapOf(await _auth.transport.send('GET', '$schoolApi/payments?status=pending'));
        waiting = maps(p?['postings']).length;
      } on Object {
        waiting = null;
      }
      if (mounted) setState(() => (_ov = mapOf(r?['overview']), _waiting = waiting, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  void _go(Widget page) => pushSchool<void>(context, page).then((_) => _load());

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final o = _ov;
    final term = str(o?['term']);
    return TkScreen(
      title: 'School',
      back: false,
      head: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (term.isNotEmpty) Text(term.toUpperCase(), style: tkText(size: 11.5, weight: 700, letterSpacing: 0.7, color: t.muted)),
          Text('School', style: tkDisplay(size: 30, letterSpacing: -1.1, color: t.text)),
        ],
      ),
      actions: [TkHeadButton(icon: 'b-gate', label: 'The gate', onTap: () => _go(GateScreen(auth: _auth)))],
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (o == null && _error == null) const TkLoading(rows: 4, label: 'Loading the school…'),
        if (o != null) ..._overview(o, t, look),
        TkSection(
          title: 'Go to',
          inset: 56,
          children: [
            TkListRow(title: 'Students', icon: 'b-person', square: look.violet, amount: o == null ? null : '${kobo(o['enrolled'])}', onTap: () => _go(StudentsPage(auth: _auth))),
            TkListRow(title: 'Debtors and reminders', icon: 'b-bell', square: look.red, amount: o == null || kobo(o['debtors']) == 0 ? null : '${kobo(o['debtors'])}', onTap: () => _go(DebtorsPage(auth: _auth))),
            TkListRow(
              title: 'Clearance',
              icon: 'b-shield',
              square: look.lime,
              amount: () {
                final c = mapOf(o?['clearance']);
                final n = kobo(c?['students']) - kobo(c?['cleared']);
                return c == null || n <= 0 ? null : '$n not cleared';
              }(),
              onTap: () => _go(ClearancePage(auth: _auth)),
            ),
            TkListRow(title: 'Payments and cash', icon: 'b-cash', square: look.teal, amount: (_waiting ?? 0) > 0 ? '$_waiting waiting' : null, onTap: () => _go(PaymentsPage(auth: _auth))),
            TkListRow(title: 'Receipts', icon: 'b-receipt', square: look.orange, onTap: () => _go(ReceiptsPage(auth: _auth))),
            TkListRow(title: 'Classes, terms and fees', icon: 'b-fees', square: const Color(0xFF8E8E93), onTap: () => _go(TermsPage(auth: _auth))),
            TkListRow(title: 'School settings', icon: 'gear', square: const Color(0xFF8E8E93), onTap: () => _go(SchoolSettingsPage(auth: _auth))),
          ],
        ),
      ],
    );
  }

  List<Widget> _overview(Map<String, dynamic> o, TkTokens t, TkLook look) {
    final expected = kobo(o['expected_kobo']);
    final collected = kobo(o['collected_kobo']);
    final aging = mapOf(o['aging']) ?? const {};
    final top = maps(o['top_debtors']);
    final how = maps(o['how_paid']).where((h) => kobo(h['kobo']) > 0).toList();
    final total = how.fold(0, (n, h) => n + kobo(h['kobo']));
    final clearance = mapOf(o['clearance']);
    final last = mapOf(o['last_close']);
    final colours = [look.lime, look.teal, look.orange, look.blue, look.violet];
    final most = buckets.map((b) => kobo(mapOf(aging[b])?['students'])).fold(0, (a, b) => a > b ? a : b);
    return [
      TkTopCard(
        semanticsLabel: 'Collected ${naira(collected)} of ${naira(expected)}',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TkEyebrow('Collected', color: look.limeInk, trailing: '${kobo(o['collected_percent'])}% of expected'),
            const SizedBox(height: 6),
            FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text(naira(collected), style: tkFigure(context))),
            Text('of ${naira(expected)} · ${kobo(o['invoiced'])} of ${kobo(o['enrolled'])} invoiced', style: tkText(size: 13, color: t.muted)),
            const SizedBox(height: 12),
            TkProgress(expected == 0 ? 0 : (collected / expected).clamp(0.0, 1.0), color: look.lime, semanticsLabel: 'Collected of expected'),
            const SizedBox(height: 10),
            Row(
              children: [
                Expanded(child: _kv(t, 'Still owed', naira(kobo(o['outstanding_kobo'])))),
                Expanded(child: _kv(t, 'Due by today', naira(kobo(o['expected_by_now_kobo'])))),
              ],
            ),
          ],
        ),
      ),
      TkHighlights(
        tiles: [
          TkHighlight(label: 'This week', icon: 'b-calendar', color: look.tealInk, value: naira(kobo(o['this_week_kobo'])), line: 'collected since Monday'),
          TkHighlight(
            label: 'Owed',
            icon: 'b-clock',
            color: look.redInk,
            value: naira(kobo(o['debtors_kobo'])),
            line: kobo(o['overdue_kobo']) > 0 ? '${naira(kobo(o['overdue_kobo']))} overdue' : 'none overdue',
            onTap: () => _go(DebtorsPage(auth: _auth)),
          ),
          TkHighlight(label: 'Matched', icon: 'b-check', color: look.limeInk, value: '${kobo(mapOf(o['matched'])?['auto_percent'])}%', line: 'by itself'),
          if (clearance != null)
            TkHighlight(
              label: 'Cleared',
              icon: 'b-shield',
              color: look.violetInk,
              value: '${kobo(clearance['cleared'])} of ${kobo(clearance['students'])}',
              line: kobo(clearance['students']) == 0 ? 'no students yet' : '${(100 * kobo(clearance['cleared']) / kobo(clearance['students'])).round()}% of students',
              onTap: () => _go(ClearancePage(auth: _auth)),
            ),
          if (total > 0)
            TkHighlight(
              wide: true,
              label: 'How parents paid',
              icon: 'b-bank',
              color: look.orangeInk,
              chart: TkSplitBar(
                parts: [
                  for (var i = 0; i < how.length; i++)
                    (label: howPaidWords[str(how[i]['how'])] ?? str(how[i]['how']), share: (100 * kobo(how[i]['kobo']) / total).round(), color: colours[i % colours.length]),
                ],
              ),
            ),
          if (kobo(o['pass_through_kobo']) > 0)
            TkHighlight(
              wide: true,
              label: 'Collected for others',
              icon: 'schools',
              color: look.blueInk,
              value: naira(kobo(o['pass_through_kobo'])),
              line: maps(o['pass_through']).map((b) => '${str(b['body'])} ${naira(kobo(b['kobo']))}').join(' · '),
            ),
        ],
      ),
      if (kobo(o['debtors']) > 0)
        TkSection(
          title: 'Owing, by how late',
          onSeeAll: () => _go(DebtorsPage(auth: _auth)),
          seeAllLabel: 'Debtors',
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 10, 16, 10),
              child: Column(
                children: [
                  for (final (i, b) in buckets.indexed)
                    _AgeBar(
                      label: bucketWords[b]!.replaceFirst(' days', ''),
                      students: kobo(mapOf(aging[b])?['students']),
                      most: most,
                      color: [look.teal, look.lime, look.orange, look.orange, look.red][i],
                      onTap: () => _go(DebtorsPage(auth: _auth, bucket: b)),
                    ),
                ],
              ),
            ),
          ],
        ),
      if (top.isNotEmpty)
        TkSection(
          title: 'Owing the most',
          children: [
            for (final d in top)
              TkListRow(
                title: nameOr(d),
                sub: '${str(d['class'])} · ${lateWords(kobo(d['days_past_due']), dueOn: str(d['due_on']))}',
                amount: naira(kobo(d['balance_kobo'])),
                onTap: () => _go(StudentPage(auth: _auth, id: str(d['student_id']))),
              ),
          ],
        ),
      TkSection(
        title: 'The day’s close',
        inset: 56,
        children: [
          if (last == null)
            TkListRow(title: 'No day closed yet', sub: 'Taskiem closes each day after midnight', icon: 'b-clock', square: const Color(0xFF8E8E93), onTap: () => _go(CloseDayPage(auth: _auth))),
          if (last != null)
            TkListRow(
              title: last['reconciles'] == true ? '${dayWords(str(last['day']))} reconciles' : '${dayWords(str(last['day']))}: ${kobo(last['problems'])} ${kobo(last['problems']) == 1 ? 'problem' : 'problems'}',
              sub: 'Taken ${lagosClock(str(last['ran_at']))} by ${str(last['ran_by']) == 'platform' ? 'Taskiem' : 'a person'}',
              icon: last['reconciles'] == true ? 'b-check' : 'b-alert',
              square: last['reconciles'] == true ? look.lime : look.red,
              onTap: () => _go(CloseDayPage(auth: _auth, day: str(last['day']))),
            ),
        ],
      ),
    ];
  }

  Widget _kv(TkTokens t, String k, String v) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(k, style: tkText(size: 12, color: t.muted)),
      Text(v, style: tkText(size: 14, weight: 650, color: t.text).copyWith(fontFeatures: tkTabular)),
    ],
  );
}

/// One age of what is owed: its words, a bar for how many, the count.
class _AgeBar extends StatelessWidget {
  const _AgeBar({required this.label, required this.students, required this.most, required this.color, this.onTap, this.on = false});
  final String label;
  final int students;
  final int most;
  final Color color;
  final VoidCallback? onTap;
  final bool on;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: onTap != null,
      selected: on,
      label: '$label: $students ${students == 1 ? 'student' : 'students'}',
      excludeSemantics: true,
      onTap: onTap,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 5),
          child: Row(
            children: [
              SizedBox(width: 88, child: Text(label, style: tkText(size: 13, weight: on ? 700 : 400, color: t.text))),
              Expanded(
                child: Container(
                  height: 8,
                  decoration: BoxDecoration(color: t.text.withValues(alpha: 0.07), borderRadius: BorderRadius.circular(99)),
                  alignment: Alignment.centerLeft,
                  child: FractionallySizedBox(
                    widthFactor: most == 0 ? 0 : (students / most).clamp(0.0, 1.0),
                    child: Container(decoration: BoxDecoration(color: color, borderRadius: BorderRadius.circular(99))),
                  ),
                ),
              ),
              SizedBox(width: 34, child: Text('$students', textAlign: TextAlign.right, style: tkText(size: 13, weight: 700, color: t.text))),
            ],
          ),
        ),
      ),
    );
  }
}

/// The ages of what is owed, each tappable (Debtors' filter).
class AgeBars extends StatelessWidget {
  const AgeBars({super.key, required this.totals, this.on, required this.onPick});
  final Map<String, dynamic> totals;
  final String? on;
  final ValueChanged<String?> onPick;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final most = buckets.map((b) => kobo(mapOf(totals[b])?['students'])).fold(0, (a, b) => a > b ? a : b);
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 10, 16, 10),
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
      child: Column(
        children: [
          for (final (i, b) in buckets.indexed)
            _AgeBar(
              label: bucketWords[b]!.replaceFirst(' days', ''),
              students: kobo(mapOf(totals[b])?['students']),
              most: most,
              on: on == b,
              color: [look.teal, look.lime, look.orange, look.orange, look.red][i],
              onTap: () => onPick(on == b ? null : b),
            ),
        ],
      ),
    );
  }
}

/// Says something went through, at the foot.
void said(BuildContext context, String words, {bool error = false}) => showTkToast(context, words, error: error);

/// Nothing yet, with an icon (the kit's empty panel).
Widget nothing(String title, {String? line, String icon = 'info'}) => TkEmpty(icon: icon, title: title, line: line);
