// Pages (decision 0072, docs/pages.md; web/src/pages/Pages.tsx): a
// business's own screens, opened on a phone. The list of the pages this
// person may open, pinned first; and a page drawn from what the server
// worked out for them (GET /v1/pages/{name}/view): its filters, its
// figures (the main one on the top card), lists in their groups, tables
// as rows, and a row's card in a sheet. Nothing here reads a table itself.
// Making and changing pages is on the web.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkAppLook, TkSmallButton, tkHint;
import '../kit/list.dart' show TkEmpty, TkSearchBox;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkFormBody, TkLines, TkSheet, showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'cards.dart' show CardPill;
import 'words.dart' show CardTone;

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Opens the Pages list.
Future<void> openPages(BuildContext context, Auth auth, {String backLabel = 'Back'}) => _push(context, PagesPage(auth: auth, backLabel: backLabel));

/// Opens one page.
Future<void> openPage(BuildContext context, Auth auth, String name, {String backLabel = 'Back'}) => _push(context, PageScreen(auth: auth, name: name, backLabel: backLabel));

/// A page's picture as one of the app's icons.
String pageIcon(String icon) => switch (icon) {
  'money' || 'invoices' => 'b-money',
  'orders' || 'box' => 'b-box',
  'members' => 'b-people',
  'clock' => 'b-clock',
  'check' => 'b-check',
  'bell' => 'b-bell',
  'place' => 'b-pin',
  'forms' || 'reports' => 'b-note',
  'store' => 'b-store',
  _ => 'b-table',
};

/// A part's tone as a card's.
CardTone pageTone(String? tone) => switch (tone) {
  'good' => CardTone.lime,
  'warn' => CardTone.warn,
  'bad' => CardTone.danger,
  _ => CardTone.plain,
};

/// How many rows, in words.
String rowsWord(int n) => n == 1 ? '1 row' : '$n rows';

/// A list's rows under their groups, in the server's order; empty groups
/// left out.
List<({String label, List<PageRow> rows})> groupedRows(PageBlock b) {
  final rows = b.rows ?? const <PageRow>[];
  final groups = b.groups;
  if (groups == null || groups.isEmpty) return [(label: '', rows: rows)];
  return [
    for (final g in groups)
      if (rows.any((r) => (r.group ?? '') == g.key)) (label: g.label, rows: rows.where((r) => (r.group ?? '') == g.key).toList()),
  ];
}

/// The filters param a page is drawn with: only those set.
String? filtersParam(Map<String, String> values) {
  final set = {for (final e in values.entries) if (e.value.isNotEmpty) e.key: e.value};
  return set.isEmpty ? null : jsonEncode(set);
}

// --- the list ---

class PagesPage extends StatefulWidget {
  const PagesPage({super.key, required this.auth, this.backLabel = 'Back'});
  final Auth auth;
  final String backLabel;

  @override
  State<PagesPage> createState() => _PagesPageState();
}

class _PagesPageState extends State<PagesPage> {
  PageList? _list;
  Object? _error;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listPages();
      if (mounted) setState(() => (_list = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Widget _row(PageSummary p, bool editor) {
    final look = TkLook.of(context);
    final sub = [
      if (p.about.isNotEmpty) p.about,
      if (editor && !p.published) 'Not published yet',
      if (editor && p.published && p.hasDraft) 'Changes not published',
    ].join(' · ');
    return TkListRow(
      title: p.title,
      sub: sub,
      icon: pageIcon(p.icon),
      square: p.pinned ? look.teal : look.violet,
      semanticsLabel: '${p.title}${p.pinned ? ', pinned' : ''}${sub.isEmpty ? '' : ': $sub'}. Open',
      onTap: () => unawaited(openPage(context, widget.auth, p.name, backLabel: 'Back to Pages')),
    );
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final list = _list;
    final pinned = list?.pages.where((p) => p.pinned).toList() ?? const <PageSummary>[];
    final rest = list?.pages.where((p) => !p.pinned).toList() ?? const <PageSummary>[];
    final editor = list?.canEdit == true;
    return TkPageScreen(
      title: 'Pages',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Screens your business makes for itself from its tables. Pages are made and changed on the web.',
      onRefresh: _load,
      children: [
        if (_error != null)
          TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'Your pages did not load', sub: errorWords(_error!), subLines: 4, color: look.red),
        if (list == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading your pages…'),
        if (list != null && list.pages.isEmpty)
          TkEmpty(
            icon: 'b-table',
            title: 'No pages yet',
            line: editor ? 'Make one on the web: Show as a page on a table makes one in a tap.' : 'Pages your business shares with you show here.',
          ),
        if (pinned.isNotEmpty) TkSection(title: 'Pinned', inset: 56, children: [for (final p in pinned) _row(p, editor)]),
        if (rest.isNotEmpty) TkSection(title: pinned.isEmpty ? null : 'All pages', inset: 56, children: [for (final p in rest) _row(p, editor)]),
      ],
    );
  }
}

// --- a page ---

class PageScreen extends StatefulWidget {
  const PageScreen({super.key, required this.auth, required this.name, this.backLabel = 'Back'});
  final Auth auth;
  final String name;
  final String backLabel;

  @override
  State<PageScreen> createState() => _PageScreenState();
}

class _PageScreenState extends State<PageScreen> {
  PageDrawing? _page;
  Object? _error;
  final _values = <String, String>{};
  final _search = TextEditingController();
  Timer? _typing;
  // Rows read with More, by part.
  final _more = <String, PageBlock>{};
  String? _loadingMore;

  @override
  void initState() {
    super.initState();
    _search.addListener(_searched);
    unawaited(_load());
  }

  @override
  void dispose() {
    _typing?.cancel();
    _search.dispose();
    super.dispose();
  }

  String? get _searchId => _page?.filters.where((f) => f.type == 'search').firstOrNull?.id;

  void _searched() {
    final id = _searchId;
    if (id == null || (_values[id] ?? '') == _search.text.trim()) return;
    _typing?.cancel();
    _typing = Timer(const Duration(milliseconds: 400), () {
      _values[id] = _search.text.trim();
      unawaited(_load());
    });
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.viewPage(name: widget.name, filters: filtersParam(_values));
      if (mounted) {
        setState(() {
          _page = r;
          _error = null;
          _more.clear();
        });
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  void _set(String id, String v) {
    setState(() => _values[id] = v);
    unawaited(_load());
  }

  Future<void> _showMore(PageBlock b) async {
    final cursor = b.more;
    if (cursor is! String || _loadingMore != null) return;
    setState(() => _loadingMore = b.id);
    try {
      final next = await widget.auth.api.getPageBlock(name: widget.name, block: b.id, cursor: cursor, filters: filtersParam(_values));
      final groups = [...?b.groups];
      for (final g in next.groups ?? const <PageBlockGroupsItem>[]) {
        if (!groups.any((x) => x.key == g.key)) groups.add(g);
      }
      if (mounted) {
        setState(() => _more[b.id] = PageBlock.fromJson({
              ...b.toJson(),
              'rows': [...?b.rows, ...?next.rows].map((r) => r.toJson()).toList(),
              'groups': groups.map((g) => g.toJson()).toList(),
              'more': next.more,
            }));
      }
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _loadingMore = null);
    }
  }

  void _open(PageRow r) {
    final d = r.detail;
    if (d == null) return;
    unawaited(showTkSheet<void>(
      context,
      (_) => TkAppLook(
        child: TkSheet(
          title: d.title.isEmpty ? 'Details' : d.title,
          body: TkFormBody(children: [
            TkFormGroup(children: [TkLines([for (final l in d.lines) (l.label, l.value.isEmpty ? '—' : l.value)])]),
          ]),
        ),
      ),
    ));
  }

  Widget _filters(PageDrawing p) {
    final children = <Widget>[];
    for (final f in p.filters) {
      switch (f.type) {
        case 'search':
          children.add(TkSearchBox(controller: _search, hint: '${f.label}…', margin: const EdgeInsets.only(bottom: 8)));
        case 'choice':
          children.add(TkChips<String>(
            label: f.label,
            padding: const EdgeInsets.only(bottom: 8),
            chips: [('', 'All'), for (final o in f.options ?? const <PageFilterOptionsItem>[]) (o.value, o.label)],
            on: _values[f.id] ?? '',
            onPick: (v) => _set(f.id, v),
          ));
        case 'dates':
          children.add(TkChips<String>(
            label: f.label,
            padding: const EdgeInsets.only(bottom: 8),
            chips: const [('', 'Any time'), ('today', 'Today'), ('week', 'This week'), ('month', 'This month')],
            on: _values[f.id] ?? '',
            onPick: (v) => _set(f.id, v),
          ));
        case 'mine':
          children.add(TkChips<String>(
            label: f.label,
            padding: const EdgeInsets.only(bottom: 8),
            chips: [('', 'Everyone’s'), ('on', f.label)],
            on: _values[f.id] ?? '',
            onPick: (v) => _set(f.id, v),
          ));
      }
    }
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: children);
  }

  Widget _lead(PageBlock b) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final change = b.change;
    return TkTopCard(
      semanticsLabel: '${b.label}: ${b.error ?? b.shown}${change == null ? '' : ', ${change.words}'}',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow(b.label ?? '', color: look.limeInk, icon: 'b-bars'),
          const SizedBox(height: 6),
          FittedBox(
            fit: BoxFit.scaleDown,
            alignment: Alignment.centerLeft,
            child: Text(b.error != null ? '—' : b.shown ?? '', style: tkFigure(context, size: 42)),
          ),
          if (change != null) ...[const SizedBox(height: 6), TkTrend(change.words, up: change.up)],
          if (b.error != null || b.over != null) ...[
            const SizedBox(height: 6),
            Text(b.error ?? b.over!, style: tkText(size: 13, lineHeight: 1.35, color: t.muted)),
          ],
        ],
      ),
    );
  }

  TkHighlight _figure(PageBlock b) {
    final look = TkLook.of(context);
    final color = switch (b.tone) {
      'good' => look.limeInk,
      'warn' => look.orangeInk,
      'bad' => look.redInk,
      _ => look.blueInk,
    };
    final line = b.error ?? b.change?.words ?? b.over;
    return TkHighlight(
      label: b.label ?? '',
      icon: 'b-bars',
      color: color,
      value: b.error != null ? '—' : b.shown,
      line: line,
      semanticsLabel: '${b.label}: ${b.error ?? b.shown}${line == null || b.error != null ? '' : ', $line'}',
    );
  }

  Widget _rows(PageBlock b0) {
    final b = _more[b0.id] ?? b0;
    final t = TkTheme.tokens(context);
    final rows = b.rows ?? const <PageRow>[];
    final head = Padding(
      padding: const EdgeInsets.fromLTRB(4, 4, 4, 0),
      child: Text('${b.title ?? ''}${b.error == null ? '${b.title == null ? '' : ' · '}${rowsWord(b.count ?? 0)}' : ''}', style: tkHint(t)),
    );
    if (b.error != null) {
      return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        TkSection(title: b.title, children: const []),
        Text(b.error!, style: tkHint(t)),
      ]);
    }
    if (rows.isEmpty) {
      return TkSection(title: b.title, empty: b.empty ?? 'Nothing to show', children: const []);
    }
    final out = <Widget>[];
    if (b.type == 'list') {
      final groups = groupedRows(b);
      for (final g in groups) {
        if (out.isNotEmpty) out.add(const SizedBox(height: 12));
        out.add(TkSection(
          title: g.label.isEmpty ? b.title : (b.title == null ? g.label : '${b.title} · ${g.label}'),
          count: g.label.isEmpty ? b.count : g.rows.length,
          inset: 16,
          children: [
            for (final r in g.rows)
              TkListRow(
                title: (r.title ?? '').isEmpty ? '—' : r.title!,
                sub: r.sub ?? '',
                status: r.state?.text,
                statusTone: pageTone(r.state?.tone),
                chevron: r.detail != null,
                semanticsLabel: [r.title ?? '', if ((r.sub ?? '').isNotEmpty) r.sub!, if (r.state != null) r.state!.text].join(', ') + (r.detail != null ? '. Open' : ''),
                onTap: r.detail == null ? null : () => _open(r),
              ),
          ],
        ));
      }
    } else {
      // A table on a phone: a row each, its first column the title and the
      // others under it with their names.
      final cols = b.columns ?? const <PageBlockColumnsItem>[];
      out.add(TkSection(
        title: b.title,
        count: b.count,
        inset: 16,
        children: [
          for (final r in rows)
            () {
              final cells = r.cells ?? const <String>[];
              final sub = [
                for (var i = 1; i < cells.length && i < cols.length; i++) '${cols[i].label}: ${cells[i].isEmpty ? '—' : cells[i]}',
              ].join(' · ');
              final title = cells.isEmpty || cells.first.isEmpty ? '—' : cells.first;
              return TkListRow(
                title: title,
                sub: sub,
                subLines: 3,
                chevron: r.detail != null,
                semanticsLabel: '$title${sub.isEmpty ? '' : ', $sub'}${r.detail != null ? '. Open' : ''}',
                onTap: r.detail == null ? null : () => _open(r),
              );
            }(),
        ],
      ));
    }
    if (b.more is String) {
      out.add(Center(
        child: TkSmallButton(
          label: _loadingMore == b.id ? 'Loading…' : 'Show more',
          semantics: 'Show more of ${b.title ?? 'the rows'}',
          onPressed: _loadingMore != null ? null : () => unawaited(_showMore(b)),
        ),
      ));
    }
    if (b.type == 'list' && groupedRows(b).length > 1) out.insert(0, head);
    if (b.more is String) out.insert(out.length - 1, const SizedBox(height: 8));
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: out);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _page;
    final children = <Widget>[];
    if (_error != null) {
      children.add(TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'This page did not open', sub: errorWords(_error!), subLines: 4, color: look.red));
    }
    if (p == null && _error == null) children.add(const TkSkeleton(rows: 4, label: 'Drawing the page…'));
    if (p != null) {
      if (p.page.about.isNotEmpty) children.add(Text(p.page.about, style: tkHint(t)));
      if (p.filters.isNotEmpty) children.add(_filters(p));
      var tiles = <TkHighlight>[];
      void flush() {
        if (tiles.isNotEmpty) children.add(TkHighlights(tiles: tiles));
        tiles = [];
      }

      for (final b in p.blocks) {
        if (b.broken != null) {
          flush();
          children.add(TkCallout(leading: TkSquare('b-warn', color: look.orange), title: 'This part needs fixing', sub: 'Change it on the web.', color: look.orange));
          continue;
        }
        switch (b.type) {
          case 'figure' when b.lead == true:
            flush();
            children.add(_lead(b));
          case 'figure':
            tiles.add(_figure(b));
          case 'heading':
            flush();
            children.add(Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
              Text(b.title ?? '', style: tkText(size: 18, weight: 700, color: t.text)),
              if ((b.text ?? '').isNotEmpty) Text(b.text!, style: tkHint(t)),
            ]));
          default:
            flush();
            children.add(_rows(b));
        }
      }
      flush();
      if (p.blocks.isEmpty) children.add(const TkEmpty(icon: 'b-table', title: 'Nothing on this page yet'));
      if (p.canEdit) {
        children.add(Center(child: Text('Change this page on the web.', style: tkHint(t))));
      }
    }
    return TkPageScreen(
      title: p?.page.title ?? 'Page',
      titleSize: 30,
      backLabel: widget.backLabel,
      onRefresh: _load,
      children: children,
    );
  }
}

/// The pinned pages, for a section of rows (Today's Your pages).
List<Widget> pinnedPageRows(BuildContext context, Auth auth, List<PageSummary> pages, {String backLabel = 'Back'}) {
  final look = TkLook.of(context);
  return [
    for (final p in pages.where((p) => p.pinned))
      TkListRow(
        title: p.title,
        sub: p.about,
        icon: pageIcon(p.icon),
        square: look.teal,
        semanticsLabel: '${p.title}. Open',
        onTap: () => unawaited(openPage(context, auth, p.name, backLabel: backLabel)),
      ),
    TkListRow(
      title: 'All pages',
      sub: pages.length == 1 ? '1 page' : '${pages.length} pages',
      icon: 'b-table',
      square: look.violet,
      onTap: () => unawaited(openPages(context, auth, backLabel: backLabel)),
    ),
  ];
}

/// A tone for a status pill, exported for the tests' words.
Widget pagePill(String words, String? tone) => CardPill(words, pageTone(tone));
