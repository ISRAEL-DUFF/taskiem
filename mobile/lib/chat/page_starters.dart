// Page starters (pages §10, 3; build-plan-gaps G5): Tickets desk, Expenses
// this month, Leave, Stock count, Deliveries today, Waiting list and Service
// log, each a page with the table and the tools it brings, added from the
// phone. GET /v1/page-starters lists them with what each makes, in words;
// POST /v1/page-starters/{id}/install makes the tables, the tools and the
// page together and publishes them where the person may, saying what was
// given another name and why anything is still a draft (the web's
// Start from a starter, the same words). Changing a page stays on the web.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkAppLook;
import '../kit/list.dart' show TkEmpty;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show showTkSheet;
import '../theme/look.dart';
import 'pages_page.dart' show openPage, pageIcon;

/// Opens Page starters.
Future<void> openPageStarters(BuildContext context, Auth auth, {String backLabel = 'Back'}) =>
    Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => PageStartersPage(auth: auth, backLabel: backLabel)));

const _partWords = {
  'heading': 'a heading',
  'figure': 'figures',
  'list': 'a list',
  'table': 'a table',
  'buttons': 'buttons',
  'status': 'where a run is',
  'board': 'a board',
  'chart': 'a chart',
};

/// A page's parts in a line: "figures, buttons and a board" (the web's words).
String partsWords(List<String> parts) {
  final seen = <String>[];
  for (final p in parts) {
    final w = _partWords[p] ?? p;
    if (!seen.contains(w)) seen.add(w);
  }
  if (seen.length <= 1) return seen.join();
  return '${seen.sublist(0, seen.length - 1).join(', ')} and ${seen.last}';
}

/// What was made: a name taken is said, and why it is not in use yet.
List<String> madeWords(PageBundleMade m) => [
  for (final t in m.tables)
    if (t.changed) 'A table called ${t.asked} was there already, so this one is ${t.name}.',
  for (final t in m.tools)
    if (t.changed) '/${t.asked} is taken, so it is called with /${t.command}.',
  if (m.published) '${m.title} is ready, with its tools.' else (m.waiting ?? '${m.title} is saved as a draft.'),
];

class PageStartersPage extends StatefulWidget {
  const PageStartersPage({super.key, required this.auth, this.backLabel = 'Back'});
  final Auth auth;
  final String backLabel;

  @override
  State<PageStartersPage> createState() => _PageStartersPageState();
}

class _PageStartersPageState extends State<PageStartersPage> {
  List<PageStarter>? _list;
  Object? _error;
  PageBundleMade? _made;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listPageStarters();
      if (mounted) setState(() => (_list = r.starters, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _open(PageStarter s) async {
    final got = await showTkSheet<PageBundleMade>(context, (_) => TkAppLook(child: _AddSheet(auth: widget.auth, starter: s)));
    if (got != null && mounted) setState(() => _made = got);
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final list = _list;
    final made = _made;
    return TkPageScreen(
      title: 'Page starters',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Pages ready to add, each with the table and the tools it needs. Change one after on the web.',
      onRefresh: _load,
      children: [
        if (made != null)
          TkCallout(
            leading: TkSquare('b-check', color: made.published ? look.teal : look.violet),
            title: made.published ? '${made.title}: added' : '${made.title}: saved as a draft',
            sub: madeWords(made).join(' '),
            subLines: 8,
            color: made.published ? look.teal : look.violet,
          ),
        if (made != null && made.published)
          TkSection(
            inset: 56,
            children: [
              TkListRow(
                title: 'Open the page',
                sub: made.title,
                icon: 'b-table',
                square: look.teal,
                onTap: () => unawaited(openPage(context, widget.auth, made.page, backLabel: 'Back to Page starters')),
              ),
            ],
          ),
        if (_error != null)
          TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'The starters did not load', sub: errorWords(_error!), subLines: 4, color: look.red),
        if (list == null && _error == null) const TkSkeleton(rows: 4, label: 'Loading the starters…'),
        if (list != null && list.isEmpty) const TkEmpty(icon: 'b-table', title: 'No starters', line: 'Make a page on the web from one of your tables.'),
        if (list != null && list.isNotEmpty)
          TkSection(
            title: 'Pages to add',
            inset: 56,
            children: [
              for (final s in list)
                TkListRow(
                  title: s.title,
                  sub: s.about,
                  subLines: 2,
                  icon: pageIcon(s.icon),
                  square: look.violet,
                  semanticsLabel: '${s.title}: ${s.about} Add it',
                  onTap: () => unawaited(_open(s)),
                ),
            ],
          ),
      ],
    );
  }
}

/// A starter's sheet: what the page has and what it brings, then Add it.
class _AddSheet extends StatefulWidget {
  const _AddSheet({required this.auth, required this.starter});
  final Auth auth;
  final PageStarter starter;

  @override
  State<_AddSheet> createState() => _AddSheetState();
}

class _AddSheetState extends State<_AddSheet> {
  bool _busy = false;
  String? _error;

  Future<void> _add() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.auth.api.installPageStarter(id: widget.starter.id, body: const PageBundleRequest(publish: true));
      if (mounted) Navigator.of(context).pop(r);
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = errorWords(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final s = widget.starter;
    return TkFormSheet(
      title: s.title,
      crumb: 'Page starters',
      action: _busy ? 'Adding…' : 'Add it',
      onAction: _busy ? null : () => unawaited(_add()),
      foot: 'Its tools and the page are put to use at once, where you may.',
      children: [
        TkFormGroup(
          title: 'The page',
          children: [
            TkListRow(title: s.page, sub: 'It has ${partsWords(s.parts)}', subLines: 2, icon: pageIcon(s.icon), square: look.violet, chevron: false),
          ],
        ),
        TkFormGroup(
          title: 'It brings',
          children: [
            for (final m in s.makes)
              TkListRow(title: m, titleLines: 4, icon: m.startsWith('/') ? 'b-tools' : 'b-table', square: m.startsWith('/') ? look.teal : look.lime, chevron: false),
          ],
        ),
        if (_error case final e?) TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'Not added', sub: e, subLines: 4, color: look.red),
      ],
    );
  }
}
