// A business's saved things from a file (saved objects S3; the server's
// POST /v1/saved/import): a CSV from the phone's files, read as text and
// sent for the plan; the plan's things shown (title, @name, kind, masked
// hint) and saved with its hash, or the lines to put right. As the school's
// students are imported (school/school_setup.dart ImportStudentsPage).

import 'dart:convert' show utf8;

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart' show TkKitField, TkPickBox;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'money_page.dart' show pickStatement;
import 'my_chat.dart' show sayFailure;
import 'saved_accounts.dart' show savedKinds;

/// What one of a kind is called in a plan's row.
String savedImportKind(String kind) => switch (kind) {
  'bank_account' => 'Bank account',
  'person' => 'Person',
  'place' => 'Place',
  'text' => 'Text',
  'provider_account' => 'Bill',
  _ => kind,
};

/// The columns the server reads, in a line under the file.
const savedImportColumns =
    'Columns it reads: title (or name), kind; bank, account_number; phone, email, address, birthday; street, area, landmark; text; provider, type, number, name_on_it; note. Up to 200 lines.';

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
  SavedImport? _plan;
  SavedImport? _done;
  // The 422's words and lines: nothing was saved.
  String? _refused;
  List<({int line, String message})> _problems = const [];
  String? _error;
  bool _busy = false;

  TaskiemApi get _api => widget.auth.api;

  Future<void> _pick() async {
    final f = await pickStatement();
    if (f == null || !mounted) return;
    setState(() => (_name = f.name, _csv = utf8.decode(f.bytes, allowMalformed: true), _plan = null, _done = null, _refused = null, _problems = const [], _error = null, _busy = true));
    try {
      final r = await _api.importSaved(body: ImportSavedRequest(csv: _csv!));
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
      final r = await _api.importSaved(body: ImportSavedRequest(csv: _csv!, confirm: p.hash));
      if (mounted) setState(() => (_done = r, _plan = null, _busy = false));
    } on Object catch (e) {
      if (mounted) _failed(e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _plan;
    final done = _done;
    final rows = p?.rows ?? const <SavedImportRowsItem>[];
    final added = done?.added ?? const <String>[];
    final failed = done?.failed ?? const <SavedImportFailedItem>[];
    final n = rows.length;
    return TkFormSheet(
      title: 'Import from a file',
      crumb: 'Saved',
      action: done != null
          ? 'Done'
          : _busy && p != null
          ? 'Saving…'
          : (p == null ? 'Save' : 'Save $n ${n == 1 ? 'thing' : 'things'}'),
      onAction: done != null ? () => Navigator.of(context).pop(added.isNotEmpty) : (_busy || p == null || n == 0 ? null : _save),
      second: done != null ? null : 'Not now',
      onSecond: () => Navigator.of(context).pop(false),
      children: [
        if (done == null) ...[
          TkFormGroup(
            children: [
              TkKitField(
                label: 'The file',
                hint: 'A CSV whose first line names the columns',
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
              for (final x in rows)
                TkListRow(
                  key: ValueKey('import ${x.line}'),
                  title: x.title,
                  sub: ['@${x.name}', savedImportKind(x.kind), if (x.hint.isNotEmpty) x.hint.replaceAll(' ', ' ')].join(' · '),
                  icon: savedKinds[x.kind]?.icon ?? 'b-note',
                  square: x.kind == 'bank_account' ? look.lime : look.teal,
                  chevron: false,
                ),
            ],
          ),
        if (done != null) ...[
          TkCallout(
            leading: TkSquare(added.isEmpty ? 'b-warn' : 'b-check', color: added.isEmpty ? look.orange : look.lime),
            title: added.isEmpty ? 'Nothing was saved' : '${added.length} ${added.length == 1 ? 'thing' : 'things'} saved',
            sub: failed.isEmpty ? 'From $_name' : '${failed.length} ${failed.length == 1 ? 'line was' : 'lines were'} not saved',
            color: added.isEmpty ? look.orange : look.lime,
          ),
          if (failed.isNotEmpty)
            TkSection(
              title: 'Not saved',
              inset: 56,
              children: [for (final f in failed) TkListRow(title: 'Line ${f.line}: @${f.name}', sub: f.message, subLines: 3, icon: 'b-warn', square: look.orange, chevron: false)],
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
