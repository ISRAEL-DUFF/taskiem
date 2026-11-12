// Tables (web ChatToolForms.tsx TableTool, TableViewer.tsx, Tables.tsx;
// lib/tables.ts, lib/tableView.ts): a table's rows, each column drawn by
// its type (naira, dates, yes or no, a status), found by a search, 25 a
// page; a row opened to change it, a row added; on the table's own screen,
// a row removed after yes. A table Taskiem keeps (the money log) is read
// only. A table's columns and a new table stay on the web, set up once.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../kit/forms.dart' show TkToolForm, errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow, TkSearchBox;
import '../kit/modal.dart' show showTkConfirm, showTkModalChoices, showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'money_sheets.dart' show dateLabel, sheetFoot;
import 'words.dart' show CardTone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
String _str(Object? v) => v as String? ?? '';

/// Rows a page shows.
const tablePage = 25;

/// tables.ts cellText: a value as a cell shows it.
String cellText(Object? v) {
  if (v == null) return '';
  if (v is String) return v;
  if (v is num || v is bool) return '$v';
  return '$v';
}

/// tables.ts cellValue: typed words, as the API takes them; "" is null for
/// every type but text.
Object? cellValue(String type, String text) {
  if (type == 'text') return text;
  final t = text.trim();
  if (t.isEmpty) return null;
  switch (type) {
    case 'integer':
    case 'number':
      return num.tryParse(t) ?? t;
    case 'boolean':
      final l = t.toLowerCase();
      if (const {'true', 'yes', '1', 'y'}.contains(l)) return true;
      if (const {'false', 'no', '0', 'n'}.contains(l)) return false;
      return t;
    default:
      return t;
  }
}

final _moneyWords = RegExp(r'^(price|amount|cost|total|balance|fee|paid|owed|due|subtotal|discount|salary|wage|wages)(_naira)?$', caseSensitive: false);
final _statusWords = RegExp(r'^(status|state|stage|payment_status|order_status)$', caseSensitive: false);

/// tableView.ts columnView: how a column is drawn ('money:kobo',
/// 'money:naira', 'number', 'date', 'datetime', 'yesno', 'status', 'text').
String columnView(Map<String, dynamic> c) {
  final type = _str(c['type']);
  final name = _str(c['name']);
  final numeric = type == 'integer' || type == 'number';
  if (numeric && RegExp(r'_kobo$', caseSensitive: false).hasMatch(name)) return 'money:kobo';
  if (numeric && _moneyWords.hasMatch(name)) return 'money:naira';
  return switch (type) {
    'integer' || 'number' => 'number',
    'boolean' => 'yesno',
    'date' => 'date',
    'datetime' => 'datetime',
    _ => type == 'text' && _statusWords.hasMatch(name) ? 'status' : 'text',
  };
}

/// tableView.ts statusTone.
CardTone statusTone(String v) {
  final s = v.trim().toLowerCase();
  if (RegExp(r'^(paid|done|complete|completed|delivered|active|approved|confirmed|in stock|available|open for orders|yes|ok|accepted|received|settled)$').hasMatch(s)) {
    return CardTone.lime;
  }
  if (RegExp(r'^(pending|waiting|new|open|draft|sent|processing|in progress|on the way|partly paid|low|queried|due)$').hasMatch(s)) return CardTone.warn;
  if (RegExp(r'^(cancelled|canceled|failed|overdue|declined|rejected|refunded|out of stock|late|lost|closed|blocked|no)$').hasMatch(s)) {
    return CardTone.danger;
  }
  return CardTone.plain;
}

/// A cell in words, as its column's kind draws it (TableViewer.tsx cell).
String cellWords(Object? v, String view) {
  if (v == null || v == '') return '—';
  switch (view) {
    case 'money:kobo':
    case 'money:naira':
      final n = v is num ? v : num.tryParse('$v');
      if (n == null) return cellText(v);
      return naira(view == 'money:kobo' ? n.round() : (n * 100).round());
    case 'number':
      final n = v is num ? v : num.tryParse('$v');
      if (n == null) return cellText(v);
      final s = n == n.roundToDouble() ? n.toInt().toString() : n.toString();
      return s.replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',');
    case 'date':
      final d = dateLabel('$v');
      return d.isEmpty ? cellText(v) : d;
    case 'yesno':
      return v == true || RegExp(r'^(true|yes|1)$', caseSensitive: false).hasMatch('$v') ? 'Yes' : 'No';
    default:
      return cellText(v);
  }
}

/// tableView.ts searchFilter: the words in one column, or any text one.
Map<String, Object?>? searchFilter(String words, List<String> columns) {
  final cols = columns.take(20).toList();
  if (words.isEmpty || cols.isEmpty) return null;
  Map<String, Object?> one(String c) => {'column': c, 'op': 'ilike', 'value': '%${words.replaceAllMapped(RegExp(r'[%_\\]'), (m) => '\\${m[0]}')}%'};
  return cols.length == 1 ? one(cols.single) : {'any': [for (final c in cols) one(c)]};
}

/// tableView.ts pageWords: "1–25 of 312".
String pageWords(int page, int size, int shown, int? total) {
  String n(int x) => '$x'.replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',');
  if (shown == 0) return total == 0 || total == null ? 'No rows' : '0 of ${n(total)}';
  final span = '${n(page * size + 1)}–${n(page * size + shown)}';
  return total == null ? span : '$span of ${n(total)}';
}

/// tables.ts isKept: a table Taskiem keeps is read only here.
bool isKept(Map<String, dynamic> table) => _str(table['kept_by']).isNotEmpty;

const keptMessage = 'This table is kept by Taskiem: rows are added by Taskiem and cannot be changed or deleted. Corrections are new rows.';

/// A table's rows, read a page at a time: search, Previous and Next; a row
/// pressed is [onOpen]'d.
class TableRows extends StatefulWidget {
  const TableRows({super.key, required this.auth, required this.table, required this.canEdit, required this.onOpen, this.reads = 0, this.padding = EdgeInsets.zero});
  final Auth auth;
  final Map<String, dynamic> table;
  final bool canEdit;
  final void Function(Map<String, dynamic> row) onOpen;

  /// Changes when the rows should be read again (a row added or changed).
  final int reads;
  final EdgeInsets padding;

  @override
  State<TableRows> createState() => _TableRowsState();
}

class _TableRowsState extends State<TableRows> {
  final _search = TextEditingController();
  String _q = '';
  String _in = '';
  List<String> _cursors = [];
  ({List<Map<String, dynamic>> rows, String? next, int? count})? _page;
  String? _error;
  bool _loading = false;
  Timer? _typing;
  int _asked = 0;

  List<Map<String, dynamic>> get _columns => _maps(widget.table['columns']);
  List<String> get _textCols => [
    for (final c in _columns)
      if (const {'text', 'phone', 'email'}.contains(c['type'])) _str(c['name']),
  ];

  @override
  void initState() {
    super.initState();
    _search.addListener(() {
      // A search a moment after the typing stops, back to its first page.
      _typing?.cancel();
      _typing = Timer(const Duration(milliseconds: 300), () {
        final words = _search.text.trim();
        if (words == _q) return;
        setState(() => (_q = words, _cursors = []));
        _load();
      });
    });
    _load();
  }

  @override
  void didUpdateWidget(TableRows old) {
    super.didUpdateWidget(old);
    if (old.reads != widget.reads) _load();
  }

  @override
  void dispose() {
    _typing?.cancel();
    _search.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final asked = ++_asked;
    setState(() => _loading = true);
    final filter = searchFilter(_q, _in.isNotEmpty ? [_in] : _textCols);
    try {
      final r = (await widget.auth.transport.send(
        'POST',
        '/v1/tables/${Uri.encodeComponent(_str(widget.table['id']))}/rows/query',
        body: {
          'limit': tablePage,
          'count': 'exact',
          if (_cursors.isNotEmpty) 'cursor': _cursors.last,
          'filters': ?(filter == null ? null : [filter]),
        },
      ) as Map).cast<String, dynamic>();
      if (!mounted || asked != _asked) return;
      setState(() {
        _page = (rows: _maps(r['rows']), next: r['next_cursor'] as String?, count: (r['count'] as num?)?.toInt());
        _error = null;
        _loading = false;
      });
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => (_error = errorWords(e), _loading = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final cols = _columns;
    final views = {for (final c in cols) _str(c['name']): columnView(c)};
    final page = _page;
    final title = _str(widget.table['title']).isNotEmpty ? _str(widget.table['title']) : _str(widget.table['name']);
    final textCols = _textCols;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (textCols.isNotEmpty) ...[
          TkSearchBox(controller: _search, hint: 'Search', margin: EdgeInsets.zero),
          if (textCols.length > 1)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(
                  label: 'Search in: ${_in.isEmpty ? 'Everything' : (cols.where((c) => c['name'] == _in).map((c) => _str(c['title']).isEmpty ? _str(c['name']) : _str(c['title'])).firstOrNull ?? _in)}',
                  onPressed: () async {
                    final pick = await showTkModalChoices(
                      context,
                      title: 'Search in',
                      value: _in,
                      choices: [
                        ('', 'Everything'),
                        for (final c in cols)
                          if (textCols.contains(c['name'])) (_str(c['name']), _str(c['title']).isEmpty ? _str(c['name']) : _str(c['title'])),
                      ],
                    );
                    if (pick != null && pick != _in) {
                      setState(() => (_in = pick, _cursors = []));
                      unawaited(_load());
                    }
                  },
                ),
              ),
            ),
          const SizedBox(height: 10),
        ],
        if (page == null)
          if (_error != null) TkErrorBox(_error!) else const TkLoading(label: 'Loading rows…')
        else if (page.rows.isEmpty)
          TkEmpty(icon: 'tables', title: _q.isNotEmpty ? 'Nothing found for “$_q”' : 'No rows yet')
        else
          Semantics(
            container: true,
            label: '$title: its rows',
            explicitChildNodes: true,
            child: Opacity(
              opacity: _loading ? 0.6 : 1,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  for (final r in page.rows)
                    () {
                      final first = cols.isEmpty ? null : cols.first;
                      final firstWords = first == null ? '' : cellWords(r[first['name']], views[first['name']] ?? 'text');
                      final rest = [
                        for (final c in cols.skip(1).take(4))
                          if (r[c['name']] != null && r[c['name']] != '')
                            '${_str(c['title']).isEmpty ? _str(c['name']) : _str(c['title'])}: ${cellWords(r[c['name']], views[c['name']] ?? 'text')}',
                      ];
                      final status = cols.where((c) => views[c['name']] == 'status' && cellText(r[c['name']]).isNotEmpty).firstOrNull;
                      return TkRow(
                        name: firstWords == '—' ? 'Row ${r['id']}' : firstWords,
                        letters: firstWords == '—' ? '#' : null,
                        sub: rest.join(' · '),
                        pill: status == null ? null : TkPill(cellText(r[status['name']]), tone: statusTone(cellText(r[status['name']]))),
                        onTap: widget.canEdit ? () => widget.onOpen(r) : null,
                      );
                    }(),
                ],
              ),
            ),
          ),
        if (_error != null && page != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        if (page != null && (_cursors.isNotEmpty || page.rows.isNotEmpty))
          Padding(
            padding: const EdgeInsets.only(top: 10),
            child: Row(
              children: [
                Expanded(
                  child: Semantics(
                    liveRegion: true,
                    child: Text(_loading ? 'Loading…' : pageWords(_cursors.length, tablePage, page.rows.length, page.count), style: tkHint(t)),
                  ),
                ),
                TkSmallButton(
                  label: 'Previous',
                  semantics: 'Previous page',
                  onPressed: _loading || _cursors.isEmpty
                      ? null
                      : () {
                          setState(() => _cursors = _cursors.sublist(0, _cursors.length - 1));
                          _load();
                        },
                ),
                const SizedBox(width: 8),
                TkSmallButton(
                  label: 'Next',
                  semantics: 'Next page',
                  onPressed: _loading || page.next == null
                      ? null
                      : () {
                          setState(() => _cursors = [..._cursors, page.next!]);
                          _load();
                        },
                ),
              ],
            ),
          ),
      ],
    );
  }
}

/// A row changed, or a new one: a box for each column (the shop's photo
/// column is the Products tool's). Answers the diary line.
class RowForm extends StatefulWidget {
  const RowForm({super.key, required this.auth, required this.table, required this.row, required this.onSaved, this.onCancel, this.canEdit = true, this.photoColumn});
  final Auth auth;
  final Map<String, dynamic> table;

  /// Null: a new row.
  final Map<String, dynamic>? row;
  final void Function(String? line) onSaved;
  final VoidCallback? onCancel;
  final bool canEdit;
  final String? photoColumn;

  @override
  State<RowForm> createState() => _RowFormState();
}

class _RowFormState extends State<RowForm> {
  late final List<Map<String, dynamic>> _cols = _maps(widget.table['columns']).where((c) => c['name'] != widget.photoColumn).toList();
  late final Map<String, TextEditingController> _boxes = {
    for (final c in _cols) _str(c['name']): TextEditingController(text: widget.row == null ? '' : cellText(widget.row![c['name']]))..addListener(() => setState(() {})),
  };

  String get _title => _str(widget.table['title']).isNotEmpty ? _str(widget.table['title']) : _str(widget.table['name']);
  String get _base => '/v1/tables/${Uri.encodeComponent(_str(widget.table['id']))}/rows';

  @override
  void dispose() {
    for (final c in _boxes.values) {
      c.dispose();
    }
    super.dispose();
  }

  Future<String?> _save() async {
    final row = widget.row;
    // Only what changed (a new row: what is filled in).
    final set = {
      for (final c in _cols)
        if (_boxes[c['name']]!.text != (row == null ? '' : cellText(row[c['name']]))) _str(c['name']): cellValue(_str(c['type']), _boxes[c['name']]!.text),
    };
    if (row != null) {
      if (set.isEmpty) return null;
      await widget.auth.transport.send('PATCH', '$_base/${Uri.encodeComponent('${row['id']}')}', body: {'set': set});
      return '$_title: a row changed';
    }
    await widget.auth.transport.send('POST', _base, body: {'row': set});
    return '$_title: a row added';
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final missing = widget.row == null && _cols.any((c) => c['required'] == true && _boxes[c['name']]!.text.trim().isEmpty);
    return TkToolForm(
      save: _save,
      onSaved: widget.onSaved,
      onCancel: widget.onCancel,
      saveWords: widget.row != null ? 'Save' : 'Add the row',
      ready: !missing,
      canEdit: widget.canEdit,
      children: [
        for (final c in _cols)
          TkKitField(
            label: '${_str(c['title']).isEmpty ? _str(c['name']) : _str(c['title'])}${c['required'] == true ? '' : ' (optional)'}',
            child: TkTextBox(
              controller: _boxes[c['name']]!,
              money: const {'integer', 'number'}.contains(c['type']),
              phone: c['type'] == 'phone',
              lines: c['type'] == 'json' ? 3 : 1,
            ),
          ),
        if (widget.photoColumn != null) Text('Photos are added from Products, by whoever looks after the shop’s page.', style: tkHint(t)),
      ],
    );
  }
}

/// The shop's products table's photo column, when [table] is it.
Future<String?> _photoColumn(Auth auth, Map<String, dynamic> table) async {
  try {
    final sf = (await auth.transport.send('GET', '/v1/storefront') as Map).cast<String, dynamic>();
    if (sf['catalogue_table'] != table['id']) return null;
    final p = _str((sf['catalogue_columns'] as Map?)?['photo']);
    return p.isEmpty ? null : p;
  } on Object {
    return null;
  }
}

/// The Tables tool's row, in its sheet: the table's rows, a row opened, a
/// row added.
class TableTool extends StatefulWidget {
  const TableTool({super.key, required this.auth, required this.table, required this.onSaved, this.canEdit = true});
  final Auth auth;
  final Map<String, dynamic> table;
  final void Function(String? line) onSaved;
  final bool canEdit;

  @override
  State<TableTool> createState() => _TableToolState();
}

class _TableToolState extends State<TableTool> {
  // Null: the rows; else the row open (an empty map: a new one).
  Map<String, dynamic>? _editing;
  String? _photo;

  bool get _edit => widget.canEdit && !isKept(widget.table);

  @override
  void initState() {
    super.initState();
    _photoColumn(widget.auth, widget.table).then((p) {
      if (mounted) setState(() => _photo = p);
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final editing = _editing;
    if (editing != null) {
      return RowForm(
        auth: widget.auth,
        table: widget.table,
        row: editing.isEmpty ? null : editing,
        onSaved: widget.onSaved,
        onCancel: () => setState(() => _editing = null),
        canEdit: _edit,
        photoColumn: _photo,
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              if (isKept(widget.table)) Text(keptMessage, style: tkHint(t)),
              TableRows(auth: widget.auth, table: widget.table, canEdit: _edit, onOpen: (r) => setState(() => _editing = r)),
            ],
          ),
        ),
        if (_edit) sheetFoot(context, [TkCta(label: 'Add a row', onTap: () => setState(() => _editing = const {}))]),
      ],
    );
  }
}

/// Tables: every table, and a table's own screen.
class TablesPage extends StatefulWidget {
  const TablesPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<TablesPage> createState() => _TablesPageState();
}

class _TablesPageState extends State<TablesPage> {
  List<Map<String, dynamic>>? _tables;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/tables') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_tables = _maps(r['tables']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final list = _tables;
    return TkPageScreen(
      title: 'Tables',
      description: 'The business’s own lists: customers, products, anything kept in rows. A new table and its columns are set up on the web.',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (list == null && _error == null) Text('Loading your tables…', style: tkHint(t)),
        if (list != null && list.isEmpty) const TkEmpty(icon: 'tables', title: 'No tables yet', line: 'Ask Taskiem to make one, or make one on the web.'),
        if (list != null && list.isNotEmpty)
          TkPageCard(
            children: [
              for (final x in list)
                TkRow(
                  name: _str(x['title']).isNotEmpty ? _str(x['title']) : _str(x['name']),
                  icon: 'tables',
                  sub: [
                    if (x['row_count'] != null) '${x['row_count']} ${x['row_count'] == 1 ? 'row' : 'rows'}',
                    if (_str(x['description']).isNotEmpty) _str(x['description']),
                    if (isKept(x)) 'Kept by Taskiem',
                  ].join(' · '),
                  onTap: () => Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => TablePage(auth: widget.auth, table: x))),
                ),
            ],
          ),
      ],
    );
  }
}

/// A table's own screen: its rows, a row added, changed, removed after yes.
class TablePage extends StatefulWidget {
  const TablePage({super.key, required this.auth, required this.table});
  final Auth auth;
  final Map<String, dynamic> table;

  @override
  State<TablePage> createState() => _TablePageState();
}

class _TablePageState extends State<TablePage> {
  int _reads = 0;
  String? _photo;

  bool get _edit => widget.auth.can('table.edit') && !isKept(widget.table);
  String get _title => _str(widget.table['title']).isNotEmpty ? _str(widget.table['title']) : _str(widget.table['name']);

  @override
  void initState() {
    super.initState();
    _photoColumn(widget.auth, widget.table).then((p) {
      if (mounted) setState(() => _photo = p);
    });
  }

  Future<void> _open(Map<String, dynamic>? row) async {
    final changed = await showTkSheet<bool>(
      context,
      (sheet) => TkSheet(
        title: row == null ? 'Add a row to $_title' : 'A row of $_title',
        body: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Expanded(
              child: RowForm(
                auth: widget.auth,
                table: widget.table,
                row: row,
                photoColumn: _photo,
                onSaved: (line) => Navigator.of(sheet).pop(line != null),
                onCancel: () => Navigator.of(sheet).pop(false),
              ),
            ),
            if (row != null)
              TkSheetFoot(
                child: TkGhostButton(
                  label: 'Remove this row',
                  onTap: () async {
                    if (!await showTkConfirm(sheet, title: 'Remove this row of $_title?', body: 'It cannot be brought back.', ok: 'Remove it', cancel: 'Keep it', danger: true)) {
                      return;
                    }
                    try {
                      await widget.auth.transport.send('DELETE', '/v1/tables/${Uri.encodeComponent(_str(widget.table['id']))}/rows/${Uri.encodeComponent('${row['id']}')}');
                      if (sheet.mounted) Navigator.of(sheet).pop(true);
                    } on Object catch (e) {
                      if (sheet.mounted) showTkToast(sheet, errorWords(e), error: true);
                    }
                  },
                ),
              ),
          ],
        ),
      ),
    );
    if (changed == true && mounted) setState(() => _reads++);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TkPageScreen(
      title: _title,
      description: _str(widget.table['description']).isEmpty ? null : _str(widget.table['description']),
      backLabel: 'Back to Tables',
      onRefresh: () async => setState(() => _reads++),
      action: _edit ? TkSmallButton(label: 'Add a row', primary: true, onPressed: () => _open(null)) : null,
      children: [
        if (isKept(widget.table)) Text(keptMessage, style: tkHint(t)),
        TkPageCard(children: [TableRows(auth: widget.auth, table: widget.table, canEdit: _edit, onOpen: _open, reads: _reads)]),
      ],
    );
  }
}
