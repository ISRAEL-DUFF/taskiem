// A business's saved things from a file (saved objects S3; the server's
// POST /v1/saved/import): a CSV (or Excel's "Unicode text", UTF-16 and
// maybe tab-separated) from the phone's files, up to 256 KB, read as text
// and sent for the plan; the plan's things shown (title, @name, kind, masked
// hint, a warning, those saved already skipped) and saved with its hash, or
// the lines to put right. As the school's students are imported
// (school/school_setup.dart ImportStudentsPage).

import 'dart:convert' show utf8;
import 'dart:typed_data' show Uint8List;

import 'package:file_selector/file_selector.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/lock.dart' show stepOut;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart' show TkKitField, TkPickBox;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'money_page.dart' show StatementPick;
import 'my_chat.dart' show sayFailure;
import 'saved_accounts.dart' show savedKinds;

/// What one of a kind is called in a plan's row: the Saved page's word for
/// one, without its "a" ("Bank account", "Account with a provider").
String savedImportKind(String kind) {
  final one = savedKinds[kind]?.one;
  if (one == null) return kind;
  final w = one.replaceFirst(RegExp('^An? '), '');
  return w.isEmpty ? kind : w[0].toUpperCase() + w.substring(1);
}

/// The columns the server reads, in a line under the file.
const savedImportColumns =
    'Columns it reads: title (or name), kind; bank, account_number; phone, email, address, birthday; street, area, landmark; text; provider, type, number, name_on_it; note. Up to 200 lines.';

/// The biggest file the server takes.
const savedImportMax = 256 * 1024;

/// A file's text: UTF-16 when it starts with its mark (Excel's "Unicode
/// text": FF FE little-endian, FE FF big-endian), else UTF-8 (without a
/// UTF-8 mark).
String savedImportText(Uint8List b) {
  if (b.length >= 2 && ((b[0] == 0xFF && b[1] == 0xFE) || (b[0] == 0xFE && b[1] == 0xFF))) {
    final little = b[0] == 0xFF;
    final units = <int>[
      for (var i = 2; i + 1 < b.length; i += 2) little ? b[i] | (b[i + 1] << 8) : (b[i] << 8) | b[i + 1],
    ];
    return String.fromCharCodes(units);
  }
  final s = utf8.decode(b, allowMalformed: true);
  return s.startsWith('﻿') ? s.substring(1) : s;
}

/// The phone's files, for a file of saved things: a CSV, or text with tabs.
StatementPick pickSavedFile = () async {
  final x = await stepOut(() => openFile(
    acceptedTypeGroups: const [
      XTypeGroup(
        label: 'CSV or text',
        extensions: ['csv', 'tsv', 'txt'],
        mimeTypes: ['text/csv', 'text/comma-separated-values', 'text/tab-separated-values', 'text/plain'],
        uniformTypeIdentifiers: ['public.comma-separated-values-text', 'public.tab-separated-values-text', 'public.plain-text'],
      ),
    ],
  ));
  if (x == null) return null;
  return (name: x.name, bytes: await x.readAsBytes());
};

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v is String ? v : '';

/// The business's Settings › Saved › Import from a file, in a sheet. Pops
/// true once anything was saved.
class SavedImportSheet extends StatefulWidget {
  const SavedImportSheet({super.key, required this.auth});
  final Auth auth;

  @override
  State<SavedImportSheet> createState() => _SavedImportSheetState();
}

class _SavedImportSheetState extends State<SavedImportSheet> {
  String? _name;
  String? _csv;
  // The plan and what was saved, as the server sent them: rows may carry a
  // warning and a skip, the result what was skipped, not checked, left.
  Map<String, dynamic>? _plan;
  Map<String, dynamic>? _done;
  // The 422's words and lines: nothing was saved.
  String? _refused;
  List<({int line, String message})> _problems = const [];
  String? _error;
  bool _tooBig = false;
  bool _busy = false;

  Future<Map<String, dynamic>> _send(ImportSavedRequest body) async =>
      (await widget.auth.transport.send('POST', '/v1/saved/import', body: body.toJson()) as Map).cast<String, dynamic>();

  Future<void> _pick() async {
    final f = await pickSavedFile();
    if (f == null || !mounted) return;
    final big = f.bytes.length > savedImportMax;
    setState(() => (_name = f.name, _csv = big ? null : savedImportText(f.bytes), _plan = null, _done = null, _refused = null, _problems = const [], _error = null, _tooBig = big, _busy = !big));
    if (big) return;
    try {
      final r = await _send(ImportSavedRequest(csv: _csv!));
      SavedImport.fromJson(r);
      if (mounted) setState(() => (_plan = r, _busy = false));
    } on Object catch (e) {
      if (mounted) _failed(e);
    }
  }

  void _failed(Object e) {
    if (e is ApiError && e.body['code'] == 'import_unreadable') {
      final lines = [
        for (final p in (e.body['problems'] as List?) ?? const [])
          if (p is Map) (line: (p['line'] as num?)?.toInt() ?? 0, message: '${p['message'] ?? ''}'),
      ];
      setState(() => (_refused = e.message, _problems = lines, _plan = null, _busy = false));
      return;
    }
    if (e is ApiError && e.body['code'] == 'import_changed') {
      setState(() => (_error = e.message, _plan = null, _busy = false));
      return;
    }
    setState(() => (_error = sayFailure(e), _busy = false));
  }

  Future<void> _save() async {
    final p = _plan;
    if (p == null) return;
    setState(() => (_busy = true, _error = null));
    try {
      final r = await _send(ImportSavedRequest(csv: _csv!, confirm: SavedImport.fromJson(p).hash));
      SavedImport.fromJson(r);
      if (mounted) setState(() => (_done = r, _plan = null, _busy = false));
    } on Object catch (e) {
      if (mounted) _failed(e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final raw = _plan;
    final p = raw == null ? null : SavedImport.fromJson(raw);
    final rawDone = _done;
    final done = rawDone == null ? null : SavedImport.fromJson(rawDone);
    final rows = [
      for (final x in _maps(raw?['rows'])) (row: SavedImportRowsItem.fromJson(x), warning: _str(x['warning']).trim(), skip: x['skip'] == true),
    ];
    final added = done?.added ?? const <String>[];
    final failed = done?.failed ?? const <SavedImportFailedItem>[];
    final skipped = [for (final s in (rawDone?['skipped'] as List?) ?? const []) if (s is String) s];
    final unchecked = [for (final u in _maps(rawDone?['unchecked'])) (line: _int(u['line']), name: _str(u['name']), problem: _str(u['problem']))];
    final left = [for (final l in _maps(rawDone?['left'])) (line: _int(l['line']), name: _str(l['name']))];
    // What Save saves: the rows not saved already.
    final n = rows.where((r) => !r.skip).length;
    final String action;
    final VoidCallback? onAction;
    if (done != null) {
      (action, onAction) = ('Done', () => Navigator.of(context).pop(added.isNotEmpty));
    } else if (_busy) {
      (action, onAction) = (p != null ? 'Saving…' : 'Checking…', null);
    } else if (p != null) {
      (action, onAction) = n == 0 ? ('Nothing new to save', null) : ('Save $n ${n == 1 ? 'thing' : 'things'}', _save);
    } else {
      (action, onAction) = (_name == null ? 'Choose a file' : 'Choose another file', _pick);
    }
    final notSaved = [
      if (failed.isNotEmpty) '${failed.length} ${failed.length == 1 ? 'line was' : 'lines were'} not saved',
      if (left.isNotEmpty) '${left.length} not saved yet',
      if (skipped.isNotEmpty) '${skipped.length} saved already',
    ];
    return TkFormSheet(
      title: 'Import from a file',
      crumb: 'Saved',
      action: action,
      onAction: onAction,
      second: done != null ? null : 'Not now',
      onSecond: () => Navigator.of(context).pop(false),
      children: [
        if (done == null) ...[
          TkFormGroup(
            children: [
              TkKitField(
                label: 'The file',
                hint: 'A CSV whose first line names the columns, up to 256\u00A0KB',
                field: false,
                child: TkPickBox(
                  label: 'The file',
                  shown: _busy && p == null ? 'Checking ${_name ?? 'the file'}…' : (_name == null ? 'Choose a CSV file' : (p == null ? _name! : '$_name · ${p.total} ${p.total == 1 ? 'line' : 'lines'}')),
                  onTap: _busy ? () {} : _pick,
                ),
              ),
            ],
          ),
        ],
        if (_tooBig) TkCallout(leading: TkSquare('b-warn', color: look.red), title: 'The file is too big: up to 256 KB.', sub: 'Nothing was sent', color: look.red),
        if (_error != null) TkCallout(leading: TkSquare('b-warn', color: look.red), title: 'Nothing was saved', sub: _error, color: look.red, subLines: 3),
        if (_refused != null) ...[
          TkCallout(leading: TkSquare('b-warn', color: look.red), title: 'Nothing was saved', sub: _refused, color: look.red, subLines: 3),
          if (_problems.isNotEmpty)
            TkSection(
              title: 'What to fix',
              inset: 56,
              children: [
                for (final pr in _problems.take(50)) TkListRow(title: 'Line ${pr.line}', sub: pr.message, subLines: 3, icon: 'b-warn', square: look.orange, chevron: false),
                if (_problems.length > 50) TkListRow(title: 'And ${_problems.length - 50} more lines', icon: 'b-warn', square: look.orange, chevron: false),
              ],
            ),
        ],
        if (p == null && done == null) Text(savedImportColumns, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
        if (p != null && rows.isNotEmpty)
          TkSection(
            title: 'To save',
            inset: 56,
            children: [
              for (final r in rows)
                TkListRow(
                  key: ValueKey('import ${r.row.line}'),
                  title: r.row.title,
                  sub: [
                    ['@${r.row.name}', savedImportKind(r.row.kind), if (r.row.hint.isNotEmpty) r.row.hint.replaceAll(' ', '\u00A0')].join(' · '),
                    if (r.skip) 'Saved already: skipped' else if (r.warning.isNotEmpty) r.warning,
                  ].join('\n'),
                  subLines: r.skip || r.warning.isNotEmpty ? 4 : 2,
                  icon: r.skip ? 'b-check' : (r.warning.isNotEmpty ? 'b-warn' : savedKinds[r.row.kind]?.icon ?? 'b-note'),
                  square: r.skip
                      ? look.violet
                      : r.warning.isNotEmpty
                      ? look.orange
                      : (r.row.kind == 'bank_account' ? look.lime : look.teal),
                  chevron: false,
                ),
            ],
          ),
        if (done != null) ...[
          TkCallout(
            leading: TkSquare(added.isEmpty ? 'b-warn' : 'b-check', color: added.isEmpty ? look.orange : look.lime),
            title: added.isEmpty ? 'Nothing was saved' : '${added.length} ${added.length == 1 ? 'thing' : 'things'} saved',
            sub: notSaved.isEmpty ? 'From $_name' : notSaved.join(', '),
            color: added.isEmpty ? look.orange : look.lime,
          ),
          if (failed.isNotEmpty)
            TkSection(
              title: 'Not saved',
              inset: 56,
              children: [for (final f in failed) TkListRow(title: 'Line ${f.line}: @${f.name}', sub: f.message, subLines: 3, icon: 'b-warn', square: look.orange, chevron: false)],
            ),
          if (left.isNotEmpty)
            TkSection(
              title: 'Not saved yet: send the file again',
              inset: 56,
              children: [for (final l in left) TkListRow(title: 'Line ${l.line}: @${l.name}', icon: 'b-clock', square: look.orange, chevron: false)],
            ),
          if (unchecked.isNotEmpty) ...[
            TkSection(
              title: 'Not checked yet',
              inset: 56,
              children: [
                for (final u in unchecked)
                  TkListRow(title: 'Line ${u.line}: @${u.name}', sub: u.problem, subLines: 3, icon: 'b-bank', square: look.orange, chevron: false),
              ],
            ),
            Text('Check names on the Saved page', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
          ],
          if (skipped.isNotEmpty)
            TkSection(
              title: 'Saved already, skipped',
              inset: 56,
              children: [for (final s in skipped) TkListRow(title: '@$s', icon: 'b-check', square: look.violet, chevron: false)],
            ),
          if (added.isNotEmpty)
            TkSection(
              title: 'Added',
              inset: 56,
              children: [for (final a in added) TkListRow(title: '@$a', icon: 'b-check', square: look.lime, chevron: false)],
            ),
        ],
      ],
    );
  }
}
