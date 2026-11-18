// One day's close (docs/schools.md §13.2; web SchoolOverview.tsx
// CloseReport): whether the day reconciles, money in by where it came
// from and how it was matched, then each item that needs someone, with
// Fix it to its page. Close yesterday again, for a manager.

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../chat/invoice_match.dart' show InvoiceMatchPage;
import '../chat/invoices_page.dart' show openInvoice;
import '../chat/words.dart' show naira;
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkCta;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'school_money.dart' show PaymentsPage, ReceiptsPage;
import 'school_tab.dart' show pushSchool, said;
import 'school_words.dart';

class CloseDayPage extends StatefulWidget {
  const CloseDayPage({super.key, required this.auth, this.day});
  final Auth auth;

  /// The day (YYYY-MM-DD); none: the last close.
  final String? day;

  @override
  State<CloseDayPage> createState() => _CloseDayPageState();
}

class _CloseDayPageState extends State<CloseDayPage> {
  Map<String, dynamic>? _close;
  bool _none = false;
  bool _busy = false;
  String? _error;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      var day = widget.day;
      if (day == null) {
        final list = maps(mapOf(await _auth.transport.send('GET', '$schoolApi/closes'))?['closes']);
        if (list.isEmpty) {
          if (mounted) setState(() => _none = true);
          return;
        }
        day = str(list.first['day']);
      }
      final r = mapOf(await _auth.transport.send('GET', '$schoolApi/closes/${Uri.encodeComponent(day)}'));
      if (mounted) setState(() => (_close = mapOf(r?['close']), _none = false, _error = null));
    } on ApiError catch (e) {
      if (mounted) setState(() => e.status == 404 ? _none = true : _error = e.message);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _closeNow() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = mapOf(await _auth.transport.send('POST', '$schoolApi/close', body: const {}));
      if (!mounted) return;
      setState(() => (_close = mapOf(r?['close']), _none = false, _busy = false));
      said(context, r?['taken_now'] == true ? 'Yesterday is closed' : 'Yesterday was already closed');
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  /// Fix it: the item's page, here where the app has it.
  void _fix(Map<String, dynamic> item) {
    final path = str(item['path']);
    final Widget? page = path.startsWith('/invoices/match')
        ? InvoiceMatchPage(auth: _auth)
        : path.startsWith('/approvals')
        ? PaymentsPage(auth: _auth, status: 'pending')
        : path.startsWith('/schools/receipts')
        ? ReceiptsPage(auth: _auth, number: Uri.tryParse(path)?.queryParameters['number'])
        : null;
    if (page != null) {
      pushSchool<void>(context, page).then((_) => _load());
      return;
    }
    final inv = RegExp(r'^/invoices/([^/?]+)').firstMatch(path);
    if (inv != null) {
      openInvoice(context, _auth, inv[1]!);
      return;
    }
    said(context, 'Open this on the web: $path');
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final c = _close;
    final manage = _auth.schoolManage;
    Map<String, dynamic> sum(String k) => mapOf(c?[k]) ?? const {};
    String line(String k) => '${kobo(sum(k)['count'])} · ${naira(kobo(sum(k)['kobo']))}';
    final problems = kobo(c?['problems']);
    return TkPageScreen(
      title: c == null ? 'The day’s close' : 'Close of ${dayWords(str(c['day']))}',
      titleSize: 30,
      backLabel: 'Back to School',
      description: 'Each night after midnight Taskiem checks the day’s school money: what came in, how it was matched, and anything that does not add up.',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (c == null && !_none && _error == null) const TkLoading(rows: 3, label: 'Loading the close…'),
        if (_none) Text('That day has not been closed yet.', style: tkText(size: 14, color: t.muted)),
        if (c != null) ...[
          TkTopCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow(
                  c['reconciles'] == true ? 'Reconciles' : 'Does not reconcile',
                  color: c['reconciles'] == true ? look.limeInk : look.redInk,
                  trailing: problems == 0 ? null : '$problems ${problems == 1 ? 'problem' : 'problems'}',
                ),
                const SizedBox(height: 6),
                FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text('${naira(kobo(sum('money_in')['kobo']))} in', style: tkFigure(context))),
                Text(
                  '${kobo(sum('money_in')['count'])} payments · taken ${lagosClock(str(c['ran_at']))} by ${str(c['ran_by']) == 'platform' ? 'Taskiem' : 'a person'}',
                  style: tkText(size: 13, color: t.muted),
                ),
              ],
            ),
          ),
          TkSection(
            children: [
              for (final (k, w) in const [
                ('into_accounts', 'Into students’ accounts'),
                ('at_the_desk', 'At the desk'),
                ('recorded_late', 'Recorded late'),
                ('matched_auto', 'Matched by itself'),
                ('matched_person', 'Matched by a person'),
                ('unmatched', 'Not matched'),
                ('lookback_found', 'Found by the look-back'),
              ])
                if (kobo(sum(k)['count']) > 0 || k == 'into_accounts' || k == 'at_the_desk') _Line(words: w, value: line(k)),
            ],
          ),
          for (final (k, title) in const [
            ('unmatched_list', 'Not matched'),
            ('pending', 'Waiting for a second person'),
            ('claims', 'Families say they paid'),
            ('problem_list', 'Does not reconcile'),
          ])
            if (maps(c[k]).isNotEmpty)
              TkSection(
                title: title,
                children: [
                  for (final item in maps(c[k]))
                    _Item(
                      message: str(item['message']),
                      amount: item['amount_kobo'] == null ? '' : naira(kobo(item['amount_kobo'])),
                      onFix: () => _fix(item),
                    ),
                ],
              ),
        ],
        if (manage) TkCta(label: _busy ? 'Closing…' : (c == null ? 'Close yesterday now' : 'Close yesterday again'), onTap: _busy ? null : _closeNow),
      ],
    );
  }
}

class _Line extends StatelessWidget {
  const _Line({required this.words, required this.value});
  final String words;
  final String value;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return MergeSemantics(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 11),
        child: Row(
          children: [
            Expanded(child: Text(words, style: tkText(size: 14, color: t.muted))),
            Text(value, style: tkText(size: 14, weight: 600, color: t.text).copyWith(fontFeatures: tkTabular)),
          ],
        ),
      ),
    );
  }
}

class _Item extends StatelessWidget {
  const _Item({required this.message, required this.amount, required this.onFix});
  final String message;
  final String amount;
  final VoidCallback onFix;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    String up(String w) => w.isEmpty ? w : '${w[0].toUpperCase()}${w.substring(1)}';
    final cut = message.indexOf(': ');
    final title = up(cut < 0 ? message : message.substring(0, cut));
    final sub = cut < 0 ? '' : up(message.substring(cut + 2));
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: tkText(size: 15, weight: 500, lineHeight: 1.3, color: t.text)),
                if (sub.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 2), child: Text(sub, style: tkText(size: 12.5, lineHeight: 1.3, color: t.muted))),
              ],
            ),
          ),
          const SizedBox(width: 10),
          Column(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              if (amount.isNotEmpty) Text(amount, style: tkText(size: 14, weight: 700, color: t.text)),
              TkLink(label: 'Fix it', bold: true, onTap: onFix),
            ],
          ),
        ],
      ),
    );
  }
}

