// Tables (web ChatToolForms.tsx TableTool, TableViewer.tsx, Tables.tsx;
// lib/tables.ts, lib/tableView.ts): a table's rows, each column drawn by
// its type (naira, dates, yes or no, a status), found by a search, 25 a
// page; a row opened to change it, a row added; on the table's own screen,
// a row removed after yes. A table Taskiem keeps (the money log) is read
// only. A table's columns and a new table stay on the web, set up once.

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../kit/forms.dart' show TkToolForm, errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow, TkSearchBox;
import '../kit/modal.dart' show showTkConfirm, showTkModal, showTkModalChoices, showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart' show handOffFile;
import 'invoices_page.dart' show fmtTime;
import 'money_page.dart' show csvHeader;
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
  return jsonEncode(v);
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
    case 'json':
      try {
        return jsonDecode(t);
      } on FormatException {
        return t;
      }
    default:
      return t;
  }
}

final _moneyWords = RegExp(r'^(price|amount|cost|total|balance|fee|paid|owed|due|subtotal|discount|salary|wage|wages)(_naira)?$', caseSensitive: false);
final _statusWords = RegExp(r'^(status|state|stage|payment_status|order_status)$', caseSensitive: false);

/// tableView.ts columnView: how a column is drawn ('money:kobo',
/// 'money:naira', 'number', 'date', 'datetime', 'yesno', 'status', 'photo',
/// 'text'). The shop's own price, cost and photo columns ([shop], its
/// catalogue_columns) are known by name.
String columnView(Map<String, dynamic> c, [Map<String, dynamic>? shop]) {
  final type = _str(c['type']);
  final name = _str(c['name']);
  final numeric = type == 'integer' || type == 'number';
  if (shop != null) {
    if (_str(shop['photo']).isNotEmpty && name == shop['photo']) return 'photo';
    final unit = shop['price_unit'] == 'kobo' ? 'money:kobo' : 'money:naira';
    if (numeric && _str(shop['price']).isNotEmpty && name == shop['price']) return unit;
    if (numeric && _str(shop['cost']).isNotEmpty && name == shop['cost']) return unit;
  }
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
  if (RegExp(r'^(paid|done|complete|completed|delivered|active|approved|confirmed|in stock|available|open for orders|yes|ok|accepted|received|settled)$')
      .hasMatch(s)) {
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
      return numberWords(n);
    case 'datetime':
      final w = fmtTime('$v');
      return w.isEmpty ? cellText(v) : w;
    case 'photo':
      final c = v is List ? v.length : (v is String && v.isNotEmpty ? 1 : 0);
      return c == 0 ? 'No photo' : (c == 1 ? '1 photo' : '$c photos');
    case 'date':
      final d = dateLabel('$v');
      return d.isEmpty ? cellText(v) : d;
    case 'yesno':
      return v == true || RegExp(r'^(true|yes|1)$', caseSensitive: false).hasMatch('$v') ? 'Yes' : 'No';
    default:
      return cellText(v);
  }
}

/// `toLocaleString("en-NG")`: commas in the whole part, at most three
/// decimals.
String numberWords(num n) {
  final neg = n < 0;
  var s = n.abs().toStringAsFixed(3).replaceFirst(RegExp(r'\.?0+$'), '');
  if (n.abs() == n.abs().roundToDouble()) s = n.abs().round().toString();
  final dot = s.indexOf('.');
  final whole = dot < 0 ? s : s.substring(0, dot);
  final frac = dot < 0 ? '' : s.substring(dot);
  return '${neg ? '-' : ''}${whole.replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',')}$frac';
}

/// tableView.ts searchFilter: the words in one column, or any text one.
Map<String, Object?>? searchFilter(String words, List<String> columns) {
  final cols = columns.take(20).toList();
  if (words.isEmpty || cols.isEmpty) return null;
  Map<String, Object?> one(String c) => {'column': c, 'op': 'ilike', 'value': '%${words.replaceAllMapped(RegExp(r'[%_\\]'), (m) => '\\${m[0]}')}%'};
  return cols.length == 1
      ? one(cols.single)
      : {
          'any': [for (final c in cols) one(c)],
        };
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

/// tables.ts KEPT_OWN_NOTE.
const keptOwnNote = 'Your organisation uses its own Xenibase project, so the table can still be edited there, outside Taskiem.';

/// tables.ts headerKey: a header or column name as matched.
String headerKey(String s) => s.toLowerCase().replaceAll(RegExp(r'[^\p{L}\p{N}]', unicode: true), '');

const _reserved = ['id', 'created_at', 'updated_at'];

/// tables.ts matchHeaders: each header's column, by name or title loosely;
/// the headers that match none.
({Map<String, String> matched, List<String> unmatched}) matchHeaders(List<String> header, List<Map<String, dynamic>> columns) {
  final by = <String, String>{for (final r in _reserved) headerKey(r): r};
  for (final c in columns) {
    by[headerKey(_str(c['name']))] = _str(c['name']);
  }
  for (final c in columns) {
    if (_str(c['title']).isNotEmpty) by.putIfAbsent(headerKey(_str(c['title'])), () => _str(c['name']));
  }
  final matched = <String, String>{};
  final unmatched = <String>[];
  for (final h in header) {
    final c = by[headerKey(h)];
    if (c != null) {
      matched[h] = c;
    } else {
      unmatched.add(h);
    }
  }
  return (matched: matched, unmatched: unmatched);
}

/// tables.ts importError.
String? importError(List<String> header, List<Map<String, dynamic>> columns, Map<String, String> mapping) {
  if (header.isEmpty) return 'The file has no header row.';
  final m = matchHeaders(header, columns);
  final left = m.unmatched.where((h) => !mapping.containsKey(h)).toList();
  if (left.isNotEmpty) return 'Match ${left.map((h) => '“$h”').join(', ')} to a column of the table, or leave it out.';
  final used = [...m.matched.values, ...mapping.values].where((c) => c.isNotEmpty && !_reserved.contains(c));
  if (used.isEmpty) return 'The file has none of the table’s columns.';
  return null;
}

const keptMessage = 'This table is kept by Taskiem: rows are added by Taskiem and cannot be changed or deleted. Corrections are new rows.';

/// A table's rows, read a page at a time: search, Previous and Next; a row
/// pressed is [onOpen]'d.
class TableRows extends StatefulWidget {
  const TableRows({
    super.key,
    required this.auth,
    required this.table,
    required this.canEdit,
    required this.onOpen,
    this.reads = 0,
    this.padding = EdgeInsets.zero,
    this.shop,
  });
  final Auth auth;
  final Map<String, dynamic> table;
  final bool canEdit;
  final void Function(Map<String, dynamic> row) onOpen;

  /// Changes when the rows should be read again (a row added or changed).
  final int reads;
  final EdgeInsets padding;

  /// The shop's own columns, when this is its products table.
  final Map<String, dynamic>? shop;

  @override
  State<TableRows> createState() => _TableRowsState();
}

class _TableRowsState extends State<TableRows> {
  final _search = TextEditingController();
  String _q = '';
  String _in = '';
  List<String> _cursors = [];

  /// tableView.ts Sort: by one column, up or down; null is newest first.
  ({String column, bool desc})? _sort;
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
          if (_sort case final o?)
            'order': [
              {'column': o.column, 'desc': o.desc},
              {'column': 'id', 'desc': true},
            ],
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
    final views = {for (final c in cols) _str(c['name']): columnView(c, widget.shop)};
    String named(Map<String, dynamic> c) => _str(c['title']).isEmpty ? _str(c['name']) : _str(c['title']);
    final sortWords = _sort == null
        ? 'Newest first'
        : '${cols.where((c) => c['name'] == _sort!.column).map(named).firstOrNull ?? _sort!.column}, ${_sort!.desc ? 'high to low' : 'low to high'}';
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
                  label:
                      'Search in: ${_in.isEmpty ? 'Everything' : (cols.where((c) => c['name'] == _in).map((c) => _str(c['title']).isEmpty ? _str(c['name']) : _str(c['title'])).firstOrNull ?? _in)}',
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
        Padding(
          padding: const EdgeInsets.only(bottom: 10),
          child: Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(
              label: 'Sort: $sortWords',
              onPressed: () async {
                final pick = await showTkModalChoices(
                  context,
                  title: 'Sort',
                  value: _sort == null ? '' : '${_sort!.column}:${_sort!.desc ? 'down' : 'up'}',
                  choices: [
                    ('', 'Newest first'),
                    for (final c in cols) ...[('${c['name']}:up', '${named(c)}, low to high'), ('${c['name']}:down', '${named(c)}, high to low')],
                  ],
                );
                if (pick == null) return;
                final i = pick.lastIndexOf(':');
                setState(() => (_sort = pick.isEmpty ? null : (column: pick.substring(0, i), desc: pick.substring(i + 1) == 'down'), _cursors = []));
                unawaited(_load());
              },
            ),
          ),
        ),
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
  const RowForm({
    super.key,
    required this.auth,
    required this.table,
    required this.row,
    required this.onSaved,
    this.onCancel,
    this.canEdit = true,
    this.photoColumn,
  });
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
    for (final c in _cols)
      _str(c['name']): TextEditingController(text: widget.row == null ? '' : cellText(widget.row![c['name']]))..addListener(() => setState(() {})),
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
              signed: const {'integer', 'number'}.contains(c['type']),
              phone: c['type'] == 'phone',
              lines: c['type'] == 'json' ? 3 : 1,
            ),
          ),
        if (widget.photoColumn != null) Text('Photos are added from Products, by whoever looks after the shop’s page.', style: tkHint(t)),
      ],
    );
  }
}

/// The shop's own columns (price, cost, their unit, photo), when [table]
/// is its products table.
Future<Map<String, dynamic>?> _shopColumns(Auth auth, Map<String, dynamic> table) async {
  try {
    final sf = (await auth.transport.send('GET', '/v1/storefront') as Map).cast<String, dynamic>();
    if (sf['catalogue_table'] != table['id']) return null;
    return (sf['catalogue_columns'] as Map?)?.cast<String, dynamic>();
  } on Object {
    return null;
  }
}

String? _photoOf(Map<String, dynamic>? shop) => _str(shop?['photo']).isEmpty ? null : _str(shop!['photo']);

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
  Map<String, dynamic>? _shop;
  String? get _photo => _photoOf(_shop);

  bool get _edit => widget.canEdit && !isKept(widget.table);

  @override
  void initState() {
    super.initState();
    _shopColumns(widget.auth, widget.table).then((p) {
      if (mounted) setState(() => _shop = p);
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
              TableRows(auth: widget.auth, table: widget.table, canEdit: _edit, shop: _shop, onOpen: (r) => setState(() => _editing = r)),
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
  bool _unavailable = false;

  @override
  void initState() {
    super.initState();
    _load();
    widget.auth.transport.send('GET', '/v1/tables/status').then((r) {
      if (mounted) setState(() => _unavailable = (r as Map)['available'] == false);
    }, onError: (Object _) {});
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
        if (_unavailable)
          Text(
            'Data tables are not available on this Taskiem: no Xenibase is configured. An owner can bring the organisation’s own Xenibase project, on the web.',
            style: tkHint(t),
          ),
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
                  onTap: () async {
                    // Deleted there: the list read again.
                    await Navigator.of(context).push(
                      PageRouteBuilder<bool>(
                        pageBuilder: (_, _, _) => TablePage(auth: widget.auth, table: x),
                      ),
                    );
                    await _load();
                  },
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
  bool _own = false;
  bool _busy = false;
  String? _error;
  String _said = '';
  Map<String, dynamic>? _shop;
  String? get _photo => _photoOf(_shop);

  bool get _edit => widget.auth.can('table.edit') && !isKept(widget.table);
  String get _title => _str(widget.table['title']).isNotEmpty ? _str(widget.table['title']) : _str(widget.table['name']);

  @override
  void initState() {
    super.initState();
    _shopColumns(widget.auth, widget.table).then((p) {
      if (mounted) setState(() => _shop = p);
    });
    if (isKept(widget.table)) {
      widget.auth.transport.send('GET', '/v1/tables/status').then((r) {
        if (mounted) setState(() => _own = (r as Map)['own'] == true);
      }, onError: (Object _) {});
    }
  }

  String get _base => '/v1/tables/${Uri.encodeComponent(_str(widget.table['id']))}';

  /// Export CSV: the whole table, handed to the phone's share sheet.
  Future<void> _export() async {
    setState(() => (_busy = true, _error = null));
    try {
      final bytes = await widget.auth.transport.bytes('$_base/export');
      await handOffFile(Uint8List.fromList(bytes), '${widget.table['name']}.csv', 'text/csv', null);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  /// Import CSV: a file from the phone, its headers matched to the columns.
  Future<void> _import() async {
    final f = await pickTableFile();
    if (f == null || !mounted) return;
    final out = await showTkModal<String>(
      context,
      title: 'Import ${f.name}',
      builder: (_) => _ImportForm(auth: widget.auth, table: widget.table, file: f),
    );
    if (out != null && mounted) setState(() => (_said = out, _reads++));
  }

  Future<void> _delete() async {
    final gone = await showTkModal<bool>(
      context,
      title: 'Delete',
      builder: (_) => _DeleteTable(auth: widget.auth, table: widget.table),
    );
    if (gone == true && mounted) {
      showTkToast(context, 'Deleted');
      Navigator.of(context).pop(true);
    }
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
                    if (!await showTkConfirm(
                      sheet,
                      title: 'Remove this row of $_title?',
                      body: 'It cannot be brought back.',
                      ok: 'Remove it',
                      cancel: 'Keep it',
                      danger: true,
                    )) {
                      return;
                    }
                    try {
                      await widget.auth.transport.send(
                        'DELETE',
                        '/v1/tables/${Uri.encodeComponent(_str(widget.table['id']))}/rows/${Uri.encodeComponent('${row['id']}')}',
                      );
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
        if (isKept(widget.table)) Text(_own ? '$keptMessage $keptOwnNote' : keptMessage, style: tkHint(t)),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkSmallButton(label: 'Export CSV', onPressed: _busy ? null : _export),
            if (_edit) TkSmallButton(label: 'Import CSV', onPressed: _busy ? null : _import),
            if (_edit) TkSmallButton(label: 'Delete table', danger: true, onPressed: _busy ? null : _delete),
          ],
        ),
        if (_said.isNotEmpty)
          Semantics(
            liveRegion: true,
            child: Text(_said, style: tkText(size: 15, color: t.text)),
          ),
        if (_error != null) TkErrorBox(_error!),
        TkPageCard(
          children: [TableRows(auth: widget.auth, table: widget.table, canEdit: _edit, shop: _shop, onOpen: _open, reads: _reads)],
        ),
      ],
    );
  }
}

/// Picks a CSV from the phone's files (a test stands in for it).
typedef TableFilePick = Future<({String name, Uint8List bytes})?> Function();

TableFilePick pickTableFile = () async {
  final x = await openFile(
    acceptedTypeGroups: const [
      XTypeGroup(
        label: 'CSV',
        extensions: ['csv'],
        mimeTypes: ['text/csv', 'text/comma-separated-values'],
        uniformTypeIdentifiers: ['public.comma-separated-values-text'],
      ),
    ],
  );
  if (x == null) return null;
  return (name: x.name, bytes: await x.readAsBytes());
};

/// Import CSV: the file's headers that match no column, matched by the
/// person (or left out); then imported.
class _ImportForm extends StatefulWidget {
  const _ImportForm({required this.auth, required this.table, required this.file});
  final Auth auth;
  final Map<String, dynamic> table;
  final ({String name, Uint8List bytes}) file;

  @override
  State<_ImportForm> createState() => _ImportFormState();
}

class _ImportFormState extends State<_ImportForm> {
  late final List<String> _header = csvHeader(utf8.decode(widget.file.bytes, allowMalformed: true));
  final Map<String, String> _mapping = {};
  bool _busy = false;
  String? _error;

  List<Map<String, dynamic>> get _cols => _maps(widget.table['columns']);

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = importError(_header, _cols, _mapping);
    final unmatched = matchHeaders(_header, _cols).unmatched;
    String named(String c) => _cols.where((x) => x['name'] == c).map((x) => _str(x['title']).isEmpty ? c : _str(x['title'])).firstOrNull ?? c;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('A header row naming the columns, in any case; up to 5,000 rows.', style: tkHint(t)),
        for (final h in unmatched)
          Padding(
            padding: const EdgeInsets.only(top: 10),
            child: TkKitField(
              field: false,
              label: '“$h” goes in',
              child: Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(
                  label: !_mapping.containsKey(h) ? 'Choose a column…' : (_mapping[h]!.isEmpty ? 'Leave it out' : named(_mapping[h]!)),
                  semantics: '“$h” goes in: ${!_mapping.containsKey(h) ? 'none chosen' : (_mapping[h]!.isEmpty ? 'left out' : named(_mapping[h]!))}',
                  onPressed: () async {
                    final v = await showTkModalChoices(
                      context,
                      title: '“$h” goes in',
                      value: _mapping[h],
                      choices: [('', 'Leave it out'), for (final c in _cols) (_str(c['name']), named(_str(c['name'])))],
                    );
                    if (v != null) setState(() => _mapping[h] = v);
                  },
                ),
              ),
            ),
          ),
        if (problem != null)
          Padding(
            padding: const EdgeInsets.only(top: 10),
            child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: 'Import',
              primary: true,
              enabled: !_busy && problem == null,
              onPressed: () async {
                setState(() => (_busy = true, _error = null));
                try {
                  final out =
                      (await widget.auth.transport.upload(
                        '/v1/tables/${Uri.encodeComponent(_str(widget.table['id']))}/import',
                        bytes: widget.file.bytes,
                        filename: widget.file.name,
                        type: 'text/csv',
                        fields: {if (_mapping.isNotEmpty) 'mapping': jsonEncode(_mapping)},
                      ) as Map?)?.cast<String, dynamic>() ??
                      const {};
                  final n = (out['imported'] as num?)?.toInt();
                  if (context.mounted) Navigator.of(context).pop(n == null ? 'Imported.' : 'Imported $n ${n == 1 ? 'row' : 'rows'}.');
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop()),
          ],
        ),
      ],
    );
  }
}

/// Delete table: there is no undo, so its name is typed to confirm.
class _DeleteTable extends StatefulWidget {
  const _DeleteTable({required this.auth, required this.table});
  final Auth auth;
  final Map<String, dynamic> table;

  @override
  State<_DeleteTable> createState() => _DeleteTableState();
}

class _DeleteTableState extends State<_DeleteTable> {
  final _typed = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _typed.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _typed.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final name = _str(widget.table['name']);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('This deletes the table $name and every row. There is no undo. Type $name to confirm.', style: tkText(size: 15, color: t.text)),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Name',
          child: TkTextBox(controller: _typed, plain: true),
        ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: 'Delete',
              primary: true,
              enabled: !_busy && _typed.text == name,
              onPressed: () async {
                setState(() => (_busy = true, _error = null));
                try {
                  await widget.auth.transport.send('DELETE', '/v1/tables/${Uri.encodeComponent(_str(widget.table['id']))}', body: {'confirm': name});
                  if (context.mounted) Navigator.of(context).pop(true);
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop(false)),
          ],
        ),
      ],
    );
  }
}
