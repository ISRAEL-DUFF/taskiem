// Pages (decision 0072, docs/pages.md; web/src/pages/Pages.tsx): a
// business's own screens, opened on a phone. The list of the pages this
// person may open, pinned first; and a page drawn from what the server
// worked out for them (GET /v1/pages/{name}/view): its filters, its
// figures (the main one on the top card), lists in their groups, tables
// as rows (put in a column's order from the part's Order button, among the
// columns the server says this person may order by), and a row's card in a
// sheet. Nothing here reads a table itself. A part over Taskiem's own
// orders or invoices is drawn the same way; a row of them opens the order
// or invoice, and a part this person may not see says so in its place.
// While the page is open it follows the page's live notices
// (GET /v1/pages/{name}/live): the parts named are read again, at most once
// in two seconds, a part put in an order or shown further keeping its
// order and reading its first rows again. Pull to refresh stays.
// Making and changing pages is on the web.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkAppLook, TkCheckbox, TkSmallButton, tkHint;
import '../kit/list.dart' show TkEmpty, TkSearchBox;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkFormBody, TkLines, TkSheet, showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show ApproveSheet;
import 'cards.dart' show CardPill;
import 'groups.dart' show askSure, pickOneOf;
import 'invoices_page.dart' show openInvoice;
import 'made_tools.dart' show showMadeToolSheet;
import 'live.dart' show SseEvent, readSse;
import 'storefront_orders_page.dart' show openStorefrontOrder;
import 'words.dart' show CardTone, whenWords;

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

/// The columns of a table part this person may put its rows in order by
/// (the server's `sortable`: never a personal column for someone who may
/// not read the table).
List<PageBlockColumnsItem> sortableColumns(PageBlock b) => [for (final c in b.columns ?? const <PageBlockColumnsItem>[]) if (c.sortable) c];

/// The words of a table part's Order button: what it is in order by now.
String orderLabel(PageBlock b) {
  final o = b.order;
  if (o == null) return 'Order';
  final c = (b.columns ?? const <PageBlockColumnsItem>[]).where((c) => c.column == o.column).firstOrNull;
  return 'By ${c?.label ?? o.column}';
}

/// Which way a column's order goes, in words.
String orderWay(bool desc) => desc ? 'Z to A, largest or latest first' : 'A to Z, smallest or earliest first';

/// The record a row of Taskiem's own records opens: an order by its
/// reference, an invoice by its number (the web's addresses).
({String kind, String id})? recordLinkOf(String? link) {
  if (link == null) return null;
  final m = RegExp(r'^/(storefront/orders|invoices)/([^/]+)$').firstMatch(link);
  if (m == null) return null;
  try {
    return (kind: m[1] == 'invoices' ? 'invoice' : 'order', id: Uri.decodeComponent(m[2]!));
  } on FormatException {
    return null;
  }
}

/// "Worked out 3 minutes ago": when a tool worked a part out (pages G2).
String workedWords(DateTime? t, {DateTime? now}) {
  if (t == null) return '';
  final mins = (now ?? DateTime.now()).difference(t).inMinutes;
  if (mins < 1) return 'Worked out just now';
  if (mins == 1) return 'Worked out a minute ago';
  if (mins < 60) return 'Worked out $mins minutes ago';
  final hours = (mins / 60).round();
  return hours == 1 ? 'Worked out an hour ago' : 'Worked out $hours hours ago';
}

/// A figure's line under its number: why it is not there, its change, or
/// when a tool worked it out.
String? figureLine(PageBlock b) => b.working ?? b.error ?? b.change?.words ?? b.over ?? (b.workedAt == null ? null : workedWords(b.workedAt));

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
  // Cells ticked in place and not yet read back, by part, row and column.
  final _ticked = <String, bool>{};
  final _ticking = <String>{};
  // The live notices: the parts named since the last read, and when it was.
  StreamSubscription<SseEvent>? _live;
  Timer? _liveRetry;
  Timer? _liveWait;
  final _liveIds = <String>{};
  bool _liveAll = false;
  bool _liveBusy = false;
  DateTime _liveLast = DateTime.fromMillisecondsSinceEpoch(0);
  bool _gone = false;
  // A part a tool is still working out: the page is read again in a moment.
  Timer? _again;
  int _tries = 0;

  void _working() {
    _again?.cancel();
    final p = _page;
    if (p == null || !p.blocks.any((b) => b.working != null) || _tries >= 10) {
      _tries = 0;
      return;
    }
    _tries++;
    _again = Timer(const Duration(seconds: 3), () => unawaited(_load()));
  }

  @override
  void initState() {
    super.initState();
    _search.addListener(_searched);
    unawaited(_load());
    unawaited(_liveOpen());
  }

  @override
  void dispose() {
    _gone = true;
    _typing?.cancel();
    _liveRetry?.cancel();
    _liveWait?.cancel();
    _again?.cancel();
    unawaited(_live?.cancel());
    _search.dispose();
    super.dispose();
  }

  // --- kept up to date ---

  Future<void> _liveOpen() async {
    try {
      final res = await widget.auth.transport.stream('/v1/pages/${Uri.encodeComponent(widget.name)}/live');
      if (_gone) {
        unawaited(res.stream.listen(null).cancel());
        return;
      }
      _live = readSse(res.stream).listen(_liveOn, onError: (Object _) => _liveDropped(), onDone: _liveDropped, cancelOnError: true);
    } on Object {
      // Offline, refused (too many open) or no such page: pull to refresh
      // as before, and it is tried again later.
      _liveDropped();
    }
  }

  void _liveDropped() {
    unawaited(_live?.cancel());
    _live = null;
    if (_gone) return;
    _liveRetry?.cancel();
    _liveRetry = Timer(const Duration(seconds: 30), () => unawaited(_liveOpen()));
  }

  void _liveOn(SseEvent e) {
    if (e.event == 'gone') {
      unawaited(_live?.cancel());
      _live = null;
      return;
    }
    if (e.event != 'changed') return;
    try {
      final d = jsonDecode(e.data) as Map<String, dynamic>;
      _liveIds.addAll([for (final b in (d['blocks'] as List? ?? const [])) '$b']);
      _liveAll = _liveAll || d['all'] == true;
    } on Object {
      return;
    }
    _liveSoon();
  }

  /// At most one read in two seconds: a burst of notices is one.
  void _liveSoon() {
    if (_liveWait != null || _liveBusy) return;
    final wait = const Duration(seconds: 2) - DateTime.now().difference(_liveLast);
    _liveWait = Timer(wait.isNegative ? Duration.zero : wait, () {
      _liveWait = null;
      unawaited(_liveRead());
    });
  }

  /// Reads again the parts named: the page as drawn, keeping the rows
  /// shown further or put in an order for the parts not named; a part
  /// named that is in an order reads its first rows in that order.
  Future<void> _liveRead() async {
    final ids = {..._liveIds};
    final all = _liveAll;
    _liveIds.clear();
    _liveAll = false;
    if ((ids.isEmpty && !all) || _gone) return;
    _liveBusy = true;
    _liveLast = DateTime.now();
    try {
      final r = await widget.auth.api.viewPage(name: widget.name, filters: filtersParam(_values));
      final again = <String, PageBlock>{};
      for (final MapEntry(key: id, value: b) in _more.entries) {
        if (!all && !ids.contains(id)) {
          again[id] = b;
          continue;
        }
        final o = b.order;
        if (o == null) continue; // the page's first rows, as drawn
        again[id] = await widget.auth.api.getPageBlock(name: widget.name, block: id, order: o.column, desc: o.desc ? '1' : null, filters: filtersParam(_values));
      }
      if (mounted) {
        setState(() {
          _page = r;
          _error = null;
          _more
            ..clear()
            ..addAll(again);
        });
      }
    } on Object {
      // Kept as it is: the next notice, or a pull, reads it.
    } finally {
      _liveBusy = false;
      if (_liveIds.isNotEmpty || _liveAll) _liveSoon();
    }
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
          _working();
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
      final o = b.order;
      final next = await widget.auth.api.getPageBlock(
        name: widget.name,
        block: b.id,
        cursor: cursor,
        order: o?.column,
        desc: o?.desc == true ? '1' : null,
        filters: filtersParam(_values),
      );
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

  /// Puts a table part's rows in a column's order: the columns this
  /// person may order by, the one in use ticked (picked again, the other
  /// way round), and the page's own order. The part is read again from
  /// the server in that order.
  Future<void> _order(PageBlock b) async {
    final cols = sortableColumns(b);
    final now = b.order;
    final picked = await pickOneOf(
      context,
      title: 'Put ${b.title ?? 'the rows'} in order',
      value: now?.column ?? '',
      choices: [
        ('', 'As the page puts them', 'The order the page was made with'),
        for (final c in cols)
          (
            c.column,
            c.label,
            now?.column == c.column ? 'Now ${orderWay(now!.desc)}. Pick again for ${orderWay(!now.desc)}' : orderWay(false),
          ),
      ],
    );
    if (picked == null || !mounted || _loadingMore != null) return;
    if (picked.isEmpty) {
      if (now != null) setState(() => _more.remove(b.id));
      return;
    }
    final desc = now?.column == picked ? !now!.desc : false;
    setState(() => _loadingMore = b.id);
    try {
      final next = await widget.auth.api.getPageBlock(name: widget.name, block: b.id, order: picked, desc: desc ? '1' : null, filters: filtersParam(_values));
      if (mounted) setState(() => _more[b.id] = next);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _loadingMore = null);
    }
  }

  /// Ticks a yes/no cell of a table part in place (pages §7, g-pages-tick):
  /// the row is changed in its table (`PATCH /v1/tables/{id}/rows/{row}`,
  /// its own permission, table.edit, and audit), shown at once and the part
  /// read again. The server sends `ticks` only to a person with table.edit.
  Future<void> _tick(PageBlock b, PageRow r, int i, bool v) async {
    final table = b.tableId;
    final col = (b.columns ?? const <PageBlockColumnsItem>[]).elementAtOrNull(i)?.column;
    final key = '${b.id}/${r.id}/$i';
    if (table == null || col == null || _ticking.contains(key)) return;
    setState(() {
      _ticking.add(key);
      _ticked[key] = v;
    });
    try {
      await widget.auth.api.updateTableRow(id: table, row: r.id, body: RowPatch(set: {col: v}));
      if (!mounted) return;
      final o = (_more[b.id] ?? b).order;
      if (o != null || _more.containsKey(b.id)) {
        // Rows read further or in an order: that part again, as it is.
        final next = await widget.auth.api.getPageBlock(name: widget.name, block: b.id, order: o?.column, desc: o?.desc == true ? '1' : null, filters: filtersParam(_values));
        if (mounted) setState(() => _more[b.id] = next);
      } else {
        await _load();
      }
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) {
        setState(() {
          _ticking.remove(key);
          _ticked.remove(key);
        });
      }
    }
  }

  /// Presses a page's button (POST /v1/pages/{name}/act): the server reads
  /// the row again and works out the values. A tool's form opens filled in
  /// (a masked value is left to be asked); a signal asks Yes first; an
  /// approval opens its sheet, with its rules.
  Future<void> _press(String block, String? row, PageAction a) async {
    final api = widget.auth.api;
    final filters = filtersParam(_values);
    try {
      final r = await api.actOnPage(name: widget.name, filters: filters, body: PageActRequest(block: block, row: row, action: a.index));
      if (!mounted) return;
      switch (r.kind) {
        case 'tool':
          final pressed = r.tool!;
          final tool = (await api.listTools()).tools.where((x) => x.command == pressed.command).firstOrNull;
          if (!mounted) return;
          if (tool == null) {
            showTkToast(context, 'This tool is not one of yours.', error: true);
            return;
          }
          await showMadeToolSheet(context, auth: widget.auth, tool: tool, initial: pressed.inputs, button: pressed.button);
        case 'signal':
          final yes = await askSure(context, title: r.asks ?? '${a.label}?', ok: 'Yes, ${a.label.toLowerCase()}');
          if (!yes || !mounted) return;
          final sent = await api.actOnPage(name: widget.name, filters: filters, body: PageActRequest(block: block, row: row, action: a.index, yes: true));
          if (mounted) showTkToast(context, sent.words ?? 'Done.');
        case 'approve':
          final ap = r.approval;
          if (ap == null) return;
          final card = ChatAlert(at: DateTime.now(), key: 'page:${widget.name}', kind: 'approval', title: ap.workflow, line: ap.stepName, approval: ap);
          await showTkSheet<bool>(context, (_) => TkAppLook(child: ApproveSheet(a: card, decision: 'approved', auth: widget.auth)));
      }
      if (mounted) unawaited(_load());
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
      if (mounted) unawaited(_load());
    }
  }

  /// A row's buttons, under its card or its words.
  Widget _actionButtons(String block, String? row, List<PageAction> actions, {VoidCallback? before}) => Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          for (final a in actions)
            TkSmallButton(
              label: a.label,
              primary: a.disabled == null,
              semantics: a.disabled == null ? a.label : '${a.label}: ${a.disabled}',
              onPressed: a.disabled != null
                  ? null
                  : () {
                      before?.call();
                      unawaited(_press(block, row, a));
                    },
            ),
        ],
      );

  void _open(PageRow r, [String? block]) {
    final d = r.detail;
    final actions = r.actions ?? const <PageAction>[];
    if (d == null && actions.isNotEmpty && block != null) {
      // A row with buttons and no card: its words and its buttons.
      unawaited(showTkSheet<void>(
        context,
        (sheet) => TkAppLook(
          child: TkSheet(
            title: (r.title ?? '').isEmpty ? (r.cells?.firstOrNull ?? 'This row') : r.title!,
            body: TkFormBody(children: [
              if ((r.sub ?? '').isNotEmpty) TkFormGroup(children: [TkLines([('', r.sub!)])]),
              _actionButtons(block, r.id, actions, before: () => Navigator.of(sheet).pop()),
            ]),
          ),
        ),
      ));
      return;
    }
    if (d == null) {
      final rec = recordLinkOf(r.link);
      if (rec == null) return;
      if (rec.kind == 'invoice') {
        unawaited(openInvoice(context, widget.auth, rec.id));
      } else {
        openStorefrontOrder(context, widget.auth, rec.id);
      }
      return;
    }
    unawaited(showTkSheet<void>(
      context,
      (sheet) => TkAppLook(
        child: TkSheet(
          title: d.title.isEmpty ? 'Details' : d.title,
          body: TkFormBody(children: [
            TkFormGroup(children: [
              TkLines([
                for (final l in d.lines) (l.label, l.value.isEmpty ? '—' : l.value),
                if (r.runStatus != null) ('Where it is', runStatusLine(r.runStatus)),
              ]),
            ]),
            if (actions.isNotEmpty && block != null) _actionButtons(block, r.id, actions, before: () => Navigator.of(sheet).pop()),
          ]),
        ),
      ),
    ));
  }

  bool _opens(PageRow r) => r.detail != null || recordLinkOf(r.link) != null || (r.actions ?? const []).isNotEmpty;

  /// The page's own buttons (pages §7).
  Widget _buttons(PageBlock b) {
    final t = TkTheme.tokens(context);
    final actions = b.actions ?? const <PageAction>[];
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      if ((b.title ?? '').isNotEmpty) Text(b.title!, style: tkText(size: 18, weight: 700, color: t.text)),
      _actionButtons(b.id, null, actions),
      for (final a in actions.where((a) => a.disabled != null)) Text('${a.label}: ${a.disabled}', style: tkHint(t)),
    ]);
  }

  /// A run's state in plain words (pages §4).
  Widget _status(PageBlock b) {
    final waits = [for (final w in b.waitingFor ?? const <ToolAnswerWait>[]) 'Waiting for ${w.who}'];
    final started = b.startedAt;
    return TkSection(title: b.title, inset: 16, children: [
      TkListRow(
        title: b.error ?? (b.status == null ? (b.words ?? 'Nothing has run yet.') : (waits.isEmpty ? 'Its latest run' : waits.removeAt(0))),
        sub: [
          ...waits,
          if (started != null) 'Started ${whenWords(started.toLocal(), DateTime.now())}',
        ].join(' · '),
        icon: 'b-clock',
        square: TkLook.of(context).blue,
        status: b.status == null ? null : b.words,
        statusTone: pageTone(b.tone),
        chevron: false,
      ),
    ]);
  }

  /// A part over Taskiem's records this person may not see, or whose area
  /// is off: said in its place.
  Widget _unavailable(PageBlock b) {
    final look = TkLook.of(context);
    return TkCallout(
      leading: TkSquare('b-lock', color: look.violet),
      title: b.title ?? b.label ?? 'This part',
      sub: b.unavailable!,
      color: look.violet,
    );
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
            child: Text(b.error != null || b.working != null ? '—' : b.shown ?? '', style: tkFigure(context, size: 42)),
          ),
          if (change != null) ...[const SizedBox(height: 6), TkTrend(change.words, up: change.up)],
          if (change == null && figureLine(b) != null) ...[
            const SizedBox(height: 6),
            Text(figureLine(b)!, style: tkText(size: 13, lineHeight: 1.35, color: t.muted)),
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
    final line = figureLine(b);
    return TkHighlight(
      label: b.label ?? '',
      icon: 'b-bars',
      color: color,
      value: b.error != null || b.working != null ? '—' : b.shown,
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
      return TkSection(title: b.title, empty: b.working ?? b.empty ?? 'Nothing to show', children: const []);
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
              () {
                // Its run's status (H5): the pill when the row has no state
                // of its own, else a line under it.
                final run = runStatusLine(r.runStatus);
                final pill = r.state == null && run.isNotEmpty;
                final under = pill ? runWaitsFor(r.runStatus) : run;
                final sub = [if ((r.sub ?? '').isNotEmpty) r.sub!, if (under.isNotEmpty) under].join(' · ');
                return TkListRow(
                  title: (r.title ?? '').isEmpty ? '—' : r.title!,
                  sub: sub,
                  status: pill ? r.runStatus!.words : r.state?.text,
                  statusTone: pageTone(pill ? r.runStatus!.tone : r.state?.tone),
                  chevron: _opens(r),
                  semanticsLabel: [r.title ?? '', if (sub.isNotEmpty) sub, if (r.state != null) r.state!.text, if (pill) r.runStatus!.words].join(', ') + (_opens(r) ? '. Open' : ''),
                  onTap: _opens(r) ? () => _open(r, b.id) : null,
                );
              }(),
          ],
        ));
      }
    } else {
      // A table on a phone: a row each, its first column the title and the
      // others under it with their names.
      final cols = b.columns ?? const <PageBlockColumnsItem>[];
      final o = b.order;
      out.add(TkSection(
        title: b.title,
        count: b.count,
        inset: 16,
        trailing: sortableColumns(b).isEmpty
            ? null
            : TkSmallButton(
                label: orderLabel(b),
                icon: 'b-sort',
                semantics: o == null
                    ? 'Put ${b.title ?? 'the rows'} in order'
                    : '${b.title ?? 'The rows'} in order ${orderLabel(b).toLowerCase()}, ${orderWay(o.desc)}. Change the order',
                onPressed: _loadingMore != null ? null : () => unawaited(_order(b)),
              ),
        children: [
          for (final r in rows)
            () {
              final cells = r.cells ?? const <String>[];
              // A yes/no column ticked in place (table.edit): a box at the
              // row's end, not a word under it.
              final ticks = [
                for (var i = 0; i < (r.ticks?.length ?? 0) && i < cols.length; i++)
                  if (r.ticks![i] is bool) i,
              ];
              final waits = runWaitsFor(r.runStatus);
              final sub = [
                for (var i = 1; i < cells.length && i < cols.length; i++)
                  if (!ticks.contains(i)) '${cols[i].label}: ${cells[i].isEmpty ? '—' : cells[i]}',
                if (waits.isNotEmpty) waits,
              ].join(' · ');
              final title = cells.isEmpty || cells.first.isEmpty ? '—' : cells.first;
              final run = r.runStatus?.words ?? '';
              final row = TkListRow(
                title: title,
                sub: sub,
                subLines: 3,
                status: run.isEmpty ? null : run,
                statusTone: pageTone(r.runStatus?.tone),
                chevron: _opens(r),
                semanticsLabel: '$title${sub.isEmpty ? '' : ', $sub'}${run.isEmpty ? '' : ', $run'}${_opens(r) ? '. Open' : ''}',
                onTap: _opens(r) ? () => _open(r, b.id) : null,
              );
              if (ticks.isEmpty) return row;
              // The boxes beside the row, not in it: the row's words are
              // one thing to a screen reader, each box another.
              return Row(children: [
                Expanded(child: row),
                for (final i in ticks)
                  () {
                    final key = '${b.id}/${r.id}/$i';
                    final on = _ticked[key] ?? r.ticks![i] as bool;
                    return PageTick(
                      on: on,
                      label: '${cols[i].label}: $title',
                      word: ticks.length > 1 ? cols[i].label : null,
                      onTap: _ticking.contains(key) ? null : () => unawaited(_tick(b, r, i, !on)),
                    );
                  }(),
                const SizedBox(width: 8),
              ]);
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
        if (b.unavailable != null) {
          flush();
          children.add(_unavailable(b));
          continue;
        }
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
          case 'buttons':
            flush();
            children.add(_buttons(b));
          case 'status':
            flush();
            children.add(_status(b));
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

/// The business's pages placed under an area (pages §9, `"place": {"area":
/// "money"}`): a section after Taskiem's own on that area's screen (the
/// screen shows while the area is on), each row opening its page. Nothing
/// is drawn when there are none.
class PlacedPages extends StatefulWidget {
  const PlacedPages({super.key, required this.auth, required this.areas, this.backLabel = 'Back'});
  final Auth auth;
  final List<String> areas;
  final String backLabel;

  @override
  State<PlacedPages> createState() => _PlacedPagesState();
}

class _PlacedPagesState extends State<PlacedPages> {
  List<PageSummary> _pages = const [];

  @override
  void initState() {
    super.initState();
    unawaited(() async {
      try {
        final all = (await widget.auth.api.listPages()).pages;
        if (mounted) setState(() => _pages = placedPages(all, widget.areas));
      } on Object {
        // None shown: the screen stands without them.
      }
    }());
  }

  @override
  Widget build(BuildContext context) {
    if (_pages.isEmpty) return const SizedBox.shrink();
    final look = TkLook.of(context);
    return TkSection(title: 'Your pages', inset: 56, children: [
      for (final p in _pages)
        TkListRow(
          title: p.title,
          sub: p.about,
          icon: pageIcon(p.icon),
          square: look.teal,
          semanticsLabel: '${p.title}. Open',
          onTap: () => unawaited(openPage(context, widget.auth, p.name, backLabel: widget.backLabel)),
        ),
    ]);
  }
}

/// The business's pages placed under an area, as tabs after the area's own
/// (pages §9; build-plan-gaps H5, g-pages-tab): Linked businesses' tab bar
/// in the app. The first tab is the screen itself; a page's tab opens the
/// page. Nothing is drawn when no page is placed there.
class PlacedPageTabs extends StatefulWidget {
  const PlacedPageTabs({super.key, required this.auth, required this.areas, required this.own, required this.label, this.backLabel = 'Back'});
  final Auth auth;
  final List<String> areas;

  /// The screen's own tab ("Your links").
  final String own;

  /// What the tab bar is, for a screen reader ("Linked businesses").
  final String label;
  final String backLabel;

  @override
  State<PlacedPageTabs> createState() => _PlacedPageTabsState();
}

class _PlacedPageTabsState extends State<PlacedPageTabs> {
  List<PageSummary> _pages = const [];

  @override
  void initState() {
    super.initState();
    unawaited(() async {
      try {
        final all = (await widget.auth.api.listPages()).pages;
        if (mounted) setState(() => _pages = placedPages(all, widget.areas));
      } on Object {
        // None shown: the screen stands without them.
      }
    }());
  }

  @override
  Widget build(BuildContext context) {
    if (_pages.isEmpty) return const SizedBox.shrink();
    return TkChips<String>(
      label: widget.label,
      padding: const EdgeInsets.only(bottom: 12),
      chips: [('', widget.own), for (final p in _pages) (p.name, p.title)],
      on: '',
      onPick: (name) {
        if (name.isNotEmpty) unawaited(openPage(context, widget.auth, name, backLabel: widget.backLabel));
      },
    );
  }
}

/// The published pages placed under these areas.
List<PageSummary> placedPages(List<PageSummary> pages, List<String> areas) => [for (final p in pages) if (p.published && p.place != null && areas.contains(p.place)) p];

/// A row's own status in a line (pages §4; H5): its run's words, and who
/// it waits for when the server says (run.read): "Waiting: for Ada".
String runStatusLine(PageRowRunStatus? s) {
  if (s == null) return '';
  final who = runWaitsFor(s);
  return who.isEmpty ? s.words : '${s.words}: ${who.substring(0, 1).toLowerCase()}${who.substring(1)}';
}

/// Who a row's run waits for, as a line under the row ("For Ada"); '' when
/// the server says no one (or the viewer may not read runs).
String runWaitsFor(PageRowRunStatus? s) {
  final who = [for (final w in s?.waitingFor ?? const <ToolAnswerWait>[]) if (w.you == true) 'you' else w.who];
  return who.isEmpty ? '' : 'For ${who.join(', ')}';
}

/// A tone for a status pill, exported for the tests' words.
Widget pagePill(String words, String? tone) => CardPill(words, pageTone(tone));

/// A yes/no cell of a page's table that this person may change here
/// (table.edit): the app's box, 48 px to press, its column's name under it
/// when a row has more than one.
class PageTick extends StatelessWidget {
  const PageTick({super.key, required this.on, required this.label, this.word, this.onTap});
  final bool on;
  final String label;
  final String? word;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      checked: on,
      label: label,
      enabled: onTap != null,
      onTap: onTap,
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: ConstrainedBox(
          constraints: const BoxConstraints(minWidth: 48, minHeight: 48),
          child: Opacity(
            opacity: onTap == null ? 0.6 : 1,
            child: Column(mainAxisSize: MainAxisSize.min, mainAxisAlignment: MainAxisAlignment.center, children: [
              TkCheckbox(checked: on),
              if (word != null) Padding(padding: const EdgeInsets.only(top: 3), child: Text(word!, maxLines: 1, style: tkHint(t).copyWith(fontSize: 11))),
            ]),
          ),
        ),
      ),
    );
  }
}
