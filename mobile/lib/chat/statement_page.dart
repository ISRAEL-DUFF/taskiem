// A linked business's statement (web Statement.tsx, lib/statements.ts):
// what they owe us (our open invoices to them) and what we owe them (their
// open bills), each with the terms and the credit limit, oldest due first.
// Both businesses' statements add up to the same numbers.

import 'package:flutter/widgets.dart';

import '../kit/polling.dart';
import '../app/auth.dart';
import '../app/openers.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'bills_page.dart' show openBill;
import 'invoices_page.dart' show openInvoice;
import 'money_sheets.dart' show dateLabel;
import 'words.dart' show naira;

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
Future<void> openStatement(BuildContext context, Auth auth, String link) => Navigator.of(context).push(
  TkRoute<void>(
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

class _StatementPageState extends State<StatementPage> with Polling {
  Map<String, dynamic>? _st;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 60), _load);
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
      titleSize: 22,
      backLabel: 'Back to Chats',
      description: 'What is open between your two businesses, oldest due first. Theirs shows the same numbers.',
      onRefresh: _load,
      action: Openers.conversation == null
          ? null
          : TkHeadButton(
              icon: 'b-chat',
              label: 'Chat with ${business.isEmpty ? 'them' : business}',
              onTap: () {
                if (!Openers.conversation!(context, widget.link)) showTkToast(context, 'Their conversation could not be found.', error: true);
              },
            ),
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (st == null && _error == null) const TkLoading(rows: 3, label: 'Loading the statement…'),
        if (st != null && theyOwe == null && weOwe == null) TkEmpty(icon: 'invoices', title: 'Nothing is open between you and $business.'),
        if (theyOwe != null) ..._side(theyOwe, 'They owe you', 'Your invoices to them', 'invoice'),
        if (weOwe != null) ..._side(weOwe, 'You owe them', 'Their bills to you', 'bill'),
        if (theyOwe != null && weOwe == null) Text('Nothing open from them to you.', style: tkText(size: 13, color: TkTheme.tokens(context).muted)),
      ],
    );
  }

  List<Widget> _side(Map<String, dynamic> side, String title, String docsTitle, String kind) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final avail = side['available_kobo'] == null ? null : _int(side['available_kobo']);
    final limit = side['credit_limit_kobo'] == null ? null : _int(side['credit_limit_kobo']);
    final owed = _int(side['owed_kobo']);
    final sub = [
      if (_int(side['overdue_kobo']) > 0) 'Overdue ${naira(_int(side['overdue_kobo']))}',
      if (side['terms_days'] != null) termsWords(_int(side['terms_days'])),
    ];
    final docs = _maps(side['documents']);
    // As drawn: what is owed large, the credit limit as a bar; the
    // documents under it; a side with nothing open is one quiet line.
    if (owed == 0 && docs.isEmpty) return [Text(kind == 'invoice' ? 'Nothing open from you to them.' : 'Nothing open from them to you.', style: tkText(size: 13, color: t.muted))];
    return [
      TkTopCard(
        semanticsLabel: title,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TkEyebrow(title, color: kind == 'invoice' ? look.limeInk : look.orangeInk, trailing: _str(side['oldest_due']).isNotEmpty ? 'oldest due ${dateLabel(_str(side['oldest_due']))}' : null),
            const SizedBox(height: 6),
            FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: Text(naira(owed), style: tkFigure(context))),
            if (sub.isNotEmpty) Text(sub.join(' · '), style: tkText(size: 13, color: t.muted)),
            if (limit != null && limit > 0) ...[
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(child: Text('Credit limit ${naira(limit)}', style: tkText(size: 12.5, color: t.muted))),
                  Text('Left to use ${naira(avail == null || avail < 0 ? 0 : avail)}', style: tkText(size: 12.5, color: t.muted)),
                ],
              ),
              const SizedBox(height: 6),
              TkProgress(((limit - (avail ?? limit)) / limit).clamp(0.0, 1.0), color: avail != null && avail < 0 ? look.red : look.lime, semanticsLabel: 'Credit used'),
              if (avail != null && avail < 0) Padding(padding: const EdgeInsets.only(top: 6), child: Text('Over the limit', style: tkText(size: 12.5, weight: 700, color: look.redInk))),
            ],
          ],
        ),
      ),
      TkSection(
        title: docsTitle,
        inset: 56,
        empty: 'None open.',
        children: [
          for (final d in docs)
            TkListRow(
              title: _str(d['reference']),
              icon: 'b-receipt',
              square: d['overdue'] == true ? look.orange : (d['disputed'] == true ? look.red : look.blue),
              sub: [
                _str(d['due_on']).isNotEmpty ? '${d['overdue'] == true ? 'Was due' : 'Due'} ${dateLabel(_str(d['due_on']))}' : 'No due date',
                if (_int(d['paid_kobo']) > 0) '${naira(_int(d['paid_kobo']))} of ${naira(_int(d['total_kobo']))} paid',
                if (d['disputed'] == true) 'queried',
              ].join(' · '),
              amount: naira(_int(d['due_kobo'])),
              onTap: () async {
                await (kind == 'invoice' ? openInvoice(context, widget.auth, _str(d['id'])) : openBill(context, widget.auth, _str(d['reference'])));
                // Paid or changed there: the statement read again.
                await _load();
              },
            ),
        ],
      ),
    ];
  }
}
