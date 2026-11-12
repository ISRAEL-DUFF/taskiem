// A linked business's statement (web Statement.tsx, lib/statements.ts):
// what they owe us (our open invoices to them) and what we owe them (their
// open bills), each with the terms and the credit limit, oldest due first.
// Both businesses' statements add up to the same numbers.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow;
import '../kit/page.dart';
import '../theme/theme.dart';
import 'bills_page.dart' show openBill;
import 'invoices_page.dart' show openInvoice;
import 'money_sheets.dart' show dateLabel;
import 'words.dart' show CardTone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

/// statements.ts termsWords.
String termsWords(int? days) {
  if (days == null) return '';
  if (days == 0) return 'pay at once';
  return days == 1 ? '1 day to pay' : '$days days to pay';
}

/// Opens the statement with a linked business.
void openStatement(BuildContext context, Auth auth, String link) => Navigator.of(context).push(
  PageRouteBuilder<void>(
    pageBuilder: (_, _, _) => StatementPage(auth: auth, link: link),
  ),
);

class StatementPage extends StatefulWidget {
  const StatementPage({super.key, required this.auth, required this.link});
  final Auth auth;
  final String link;

  @override
  State<StatementPage> createState() => _StatementPageState();
}

class _StatementPageState extends State<StatementPage> {
  Map<String, dynamic>? _st;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/links/${Uri.encodeComponent(widget.link)}/statement') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_st = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final st = _st;
    final business = _str(st?['business']);
    final theyOwe = st?['they_owe'] is Map ? (st!['they_owe'] as Map).cast<String, dynamic>() : null;
    final weOwe = st?['we_owe'] is Map ? (st!['we_owe'] as Map).cast<String, dynamic>() : null;
    return TkPageScreen(
      title: st == null ? 'Statement' : 'Statement with $business',
      description: 'What is open between your two businesses, oldest due first. Theirs shows the same numbers.',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (st == null && _error == null) const TkLoading(rows: 3, label: 'Loading the statement…'),
        if (st != null && theyOwe == null && weOwe == null) TkEmpty(icon: 'invoices', title: 'Nothing is open between you and $business.'),
        if (theyOwe != null) ..._side(theyOwe, '$business owes you', 'Your invoices to $business', 'invoice'),
        if (weOwe != null) ..._side(weOwe, 'You owe $business', '$business’s bills to you', 'bill'),
      ],
    );
  }

  List<Widget> _side(Map<String, dynamic> side, String title, String docsTitle, String kind) {
    final t = TkTheme.tokens(context);
    final avail = side['available_kobo'] == null ? null : _int(side['available_kobo']);
    final foot = [
      if (_int(side['overdue_kobo']) > 0) 'Overdue ${naira(_int(side['overdue_kobo']))}',
      if (_str(side['oldest_due']).isNotEmpty) 'Oldest due ${dateLabel(_str(side['oldest_due']))}',
      if (side['terms_days'] != null) 'Terms: ${termsWords(_int(side['terms_days']))}',
      if (side['credit_limit_kobo'] != null) 'Credit limit ${naira(_int(side['credit_limit_kobo']))}',
      if (avail != null) 'Left to use ${naira(avail < 0 ? 0 : avail)}',
    ];
    final docs = _maps(side['documents']);
    return [
      TkHero(
        label: title,
        tag: avail != null && avail < 0 ? 'Over the limit' : null,
        amount: naira(_int(side['owed_kobo'])),
        sub: foot.isEmpty ? null : foot.join(' · '),
        semanticsLabel: title,
      ),
      TkPageCard(
        title: docsTitle,
        children: [
          if (docs.isEmpty) Text('None open.', style: tkHint(t)),
          for (final d in docs)
            TkRow(
              name: _str(d['reference']),
              icon: 'invoices',
              tone: d['overdue'] == true ? CardTone.warn : (d['disputed'] == true ? CardTone.danger : null),
              sub: [
                _str(d['due_on']).isNotEmpty ? '${d['overdue'] == true ? 'Was due' : 'Due'} ${dateLabel(_str(d['due_on']))}' : 'No due date',
                if (_int(d['paid_kobo']) > 0) '${naira(_int(d['paid_kobo']))} of ${naira(_int(d['total_kobo']))} paid',
                if (d['disputed'] == true) 'queried',
              ].join(' · '),
              pill: TkPill(naira(_int(d['due_kobo'])), tone: CardTone.plain),
              onTap: () => kind == 'invoice' ? openInvoice(context, widget.auth, _str(d['id'])) : openBill(context, widget.auth, _str(d['reference'])),
            ),
        ],
      ),
    ];
  }
}
