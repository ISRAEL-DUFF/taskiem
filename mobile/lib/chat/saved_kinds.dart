// Kinds the owner makes (saved objects S1, part 5; saved-objects §4.2;
// web/src/pages/SavedKinds.tsx): the sheet that makes or changes one (its
// name and fields, or a starter to copy), the sheet that saves a thing of
// one (a field for each; sensitive ones masked and kept unless typed
// again), and a thing's hidden values once shown with a passkey.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/forms.dart' show TkSwitchRow;
import '../kit/kit.dart';
import '../kit/mobile.dart';
import '../kit/look.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import 'approve.dart' show TkTextBox;
import 'groups.dart' show pickOneOf;
import 'my_chat.dart' show sayFailure;
import 'saved_accounts.dart' show SavedApi, cleanSavedName, savedNameProblem;

/// The types a field may have, as the form names them.
const savedFieldTypes = <(String, String)>[
  ('text', 'Words'),
  ('number', 'A number'),
  ('money', 'Money (₦)'),
  ('date', 'A date'),
  ('phone', 'A phone'),
  ('email', 'An email'),
  ('choice', 'A choice from a list'),
];

/// A value as a person reads it: money as naira, a date as a day; a
/// sensitive one as the server masked it.
String savedShownValue(SavedKindField f, String v) {
  if (v.startsWith('••')) return v;
  if (f.type == 'money') {
    final parts = v.split('.');
    final whole = parts.first.replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',');
    return '₦$whole${parts.length > 1 ? '.${parts[1]}' : ''}';
  }
  if (f.type == 'date') {
    final d = DateTime.tryParse(v);
    if (d != null) {
      const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
      return '${d.day} ${months[d.month - 1]} ${d.year}';
    }
  }
  return v;
}

/// What is wrong with a kind in its form, or null.
String? savedKindProblem(String name, List<SavedKindField> fields) {
  if (name.trim().isEmpty) return 'Give the kind a name, such as Car or Generator.';
  if (fields.isEmpty) return 'Give the kind at least one field.';
  if (fields.length > 30) return 'A kind has up to 30 fields.';
  final seen = <String>{};
  for (final f in fields) {
    final l = f.label.trim().toLowerCase();
    if (l.isEmpty) return 'Give each field a label.';
    if (!seen.add(l)) return 'Two fields are called ${f.label.trim()}: give each its own label.';
    if (f.type == 'choice' && (f.options ?? const []).where((o) => o.trim().isNotEmpty).length < 2) return 'Give ${f.label.trim()} at least 2 choices.';
  }
  return null;
}

/// A field being written: its label and choices as typed.
class _FieldDraft {
  _FieldDraft(SavedKindField f)
    : key = f.key,
      label = TextEditingController(text: f.label),
      options = TextEditingController(text: (f.options ?? const []).join('\n')),
      type = f.type,
      required = f.required ?? false,
      sensitive = f.sensitive ?? false,
      neverShare = f.neverShare ?? false;
  final String? key;
  final TextEditingController label;
  final TextEditingController options;
  String type;
  bool required;
  bool sensitive;
  bool neverShare;

  SavedKindField get field => SavedKindField(
    key: key,
    label: label.text.trim(),
    type: type,
    required: required,
    sensitive: sensitive || neverShare,
    neverShare: neverShare,
    options: type == 'choice' ? [for (final o in options.text.split('\n')) if (o.trim().isNotEmpty) o.trim()] : null,
  );
}

/// Make a kind, or change one. Pops the kind as saved.
class SavedKindSheet extends StatefulWidget {
  const SavedKindSheet({super.key, required this.api, this.kind, this.starters = const []});
  final SavedApi api;
  final SavedKind? kind;
  final List<SavedKindListStartersItem> starters;

  @override
  State<SavedKindSheet> createState() => _SavedKindSheetState();
}

class _SavedKindSheetState extends State<SavedKindSheet> {
  late final _name = TextEditingController(text: widget.kind?.name ?? '');
  late List<_FieldDraft> _fields = [for (final f in widget.kind?.fields ?? const [SavedKindField(label: '', type: 'text')]) _FieldDraft(f)];
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _name.dispose();
    for (final f in _fields) {
      f.label.dispose();
      f.options.dispose();
    }
    super.dispose();
  }

  void _start(SavedKindListStartersItem st) => setState(() {
    _name.text = st.name;
    _fields = [for (final f in st.fields) _FieldDraft(SavedKindField(label: f.label, type: f.type, required: f.required, sensitive: f.sensitive, neverShare: f.neverShare, options: f.options))];
  });

  Future<void> _pickType(_FieldDraft f) async {
    final v = await pickOneOf(context, title: 'What ${f.label.text.trim().isEmpty ? 'it' : f.label.text.trim()} is', value: f.type, choices: [for (final (k, w) in savedFieldTypes) (k, w, '')]);
    if (v != null && mounted) setState(() => f.type = v);
  }

  Future<void> _save() async {
    final fields = [for (final f in _fields) f.field];
    final problem = savedKindProblem(_name.text, fields);
    if (problem != null) return setState(() => _error = problem);
    setState(() => (_busy = true, _error = null));
    try {
      final body = SavedKindIn(name: _name.text.trim(), fields: fields);
      final k = widget.kind == null ? await widget.api.addKind(body) : await widget.api.changeKind(widget.kind!.id, body);
      if (mounted) Navigator.of(context).pop(k);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_busy = false, _error = e.has('name_taken') ? 'You have a kind by that name: choose another.' : e.message));
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkFormSheet(
      title: widget.kind == null ? 'Make a kind' : 'Change ${widget.kind!.name}',
      crumb: 'Saved',
      action: _busy ? 'Saving…' : (widget.kind == null ? 'Make it' : 'Save'),
      onAction: _busy ? null : _save,
      children: [
        if (widget.kind == null && widget.starters.isNotEmpty)
          TkFormGroup(
            title: 'Start from',
            children: [
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [for (final st in widget.starters) TkSmallButton(label: st.name, onPressed: () => _start(st))],
              ),
            ],
          ),
        TkFormGroup(
          children: [TkKitField(label: 'Name of the kind', hint: 'Such as Car, Generator or Tenant', child: TkTextBox(controller: _name, max: 40))],
        ),
        for (var i = 0; i < _fields.length; i++)
          () {
            final f = _fields[i];
            return TkFormGroup(
              key: ObjectKey(f),
              title: 'Field ${i + 1}',
              children: [
                TkKitField(label: 'Label', hint: 'Such as Plate number', child: TkTextBox(controller: f.label, max: 60)),
                TkKitField(
                  label: 'What it is',
                  field: false,
                  child: TkPickBox(label: 'What it is', shown: [for (final (k, w) in savedFieldTypes) if (k == f.type) w].firstOrNull ?? 'Words', onTap: () => _pickType(f)),
                ),
                if (f.type == 'choice') TkKitField(label: 'Its choices', hint: 'One on each line', child: TkTextBox(controller: f.options, lines: 3, max: 600)),
                TkSwitchRow(label: 'Required', on: f.required, onChanged: (v) => setState(() => f.required = v)),
                TkSwitchRow(label: 'Sensitive', hint: 'Sealed, shown in full only with your passkey', on: f.sensitive || f.neverShare, onChanged: f.neverShare ? null : (v) => setState(() => f.sensitive = v)),
                TkSwitchRow(label: 'Never share', hint: 'Kept out of anything you share', on: f.neverShare, onChanged: (v) => setState(() => f.neverShare = v)),
                Wrap(
                  spacing: 8,
                  children: [
                    if (i > 0)
                      TkSmallButton(
                        label: 'Up',
                        semantics: 'Move ${f.label.text.trim().isEmpty ? 'field ${i + 1}' : f.label.text.trim()} up',
                        onPressed: () => setState(() => _fields.insert(i - 1, _fields.removeAt(i))),
                      ),
                    if (_fields.length > 1)
                      TkSmallButton(
                        label: 'Remove',
                        danger: true,
                        semantics: 'Remove ${f.label.text.trim().isEmpty ? 'field ${i + 1}' : f.label.text.trim()}',
                        onPressed: () => setState(() => _fields.removeAt(i)),
                      ),
                  ],
                ),
              ],
            );
          }(),
        if (_fields.length < 30) TkSmallButton(label: 'Add a field', icon: 'b-plus', onPressed: () => setState(() => _fields.add(_FieldDraft(const SavedKindField(label: '', type: 'text'))))),
        if (_error != null) TkCallout(leading: TkSquare('b-warn', color: look.red), title: _error!, color: look.red),
      ],
    );
  }
}

/// Save a thing of a made-up kind, or change one. Pops true when saved.
class SavedCustomSheet extends StatefulWidget {
  const SavedCustomSheet({super.key, required this.api, required this.kind, this.thing, this.startName});
  final SavedApi api;
  final SavedKind kind;
  final SavedObject? thing;

  /// A name to begin with (Save "@car"… in the picker).
  final String? startName;

  @override
  State<SavedCustomSheet> createState() => _SavedCustomSheetState();
}

class _SavedCustomSheetState extends State<SavedCustomSheet> {
  late final SavedObject? _x = widget.thing;
  late final Map<String, String> _had = {for (final e in (_x?.values ?? const {}).entries) e.key: '${e.value}'};
  late final _name = TextEditingController(text: _x != null ? '@${_x.name}' : ((widget.startName ?? '').isEmpty ? '' : '@${widget.startName}'));
  late final _title = TextEditingController(text: _x?.title ?? '');
  late final _note = TextEditingController(text: _x?.details.note ?? '');
  // Sensitive values start empty: typed again only to change them.
  late final Map<String, TextEditingController> _v = {
    for (final f in widget.kind.fields) f.key ?? '': TextEditingController(text: (f.sensitive ?? false) ? '' : (_had[f.key ?? ''] ?? '')),
  };
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    for (final c in [_name, _title, _note, ..._v.values]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _pick(SavedKindField f) async {
    final c = _v[f.key ?? '']!;
    final v = await pickOneOf(context, title: f.label, value: c.text, choices: [for (final o in f.options ?? const <String>[]) (o, o, '')]);
    if (v != null && mounted) setState(() => c.text = v);
  }

  Future<void> _save() async {
    final x = _x;
    String? problem = savedNameProblem(_name.text) ?? (_title.text.trim().isEmpty ? 'Give it a title, like My car.' : null);
    for (final f in widget.kind.fields) {
      final key = f.key ?? '';
      if (problem == null && (f.required ?? false) && _v[key]!.text.trim().isEmpty && !(x != null && (_had[key] ?? '').isNotEmpty)) {
        problem = '${f.label} is needed.';
      }
    }
    if (problem != null) return setState(() => _error = problem);
    final values = <String, dynamic>{};
    for (final f in widget.kind.fields) {
      final key = f.key ?? '';
      final v = _v[key]!.text.trim();
      if (v.isNotEmpty) {
        values[key] = v;
      } else if (x != null && !(f.sensitive ?? false) && (_had[key] ?? '').isNotEmpty) {
        values[key] = null;
      }
    }
    setState(() => (_busy = true, _error = null));
    try {
      final body = SavedObjectIn(
        name: cleanSavedName(_name.text),
        title: _title.text.trim(),
        kind: x == null ? 'custom' : null,
        kindId: x == null ? widget.kind.id : null,
        values: values,
        note: _note.text.trim(),
      );
      if (x == null) {
        await widget.api.add(body);
      } else {
        await widget.api.change(x.id, body);
      }
      if (mounted) Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_busy = false, _error = e.has('name_taken') ? 'That name is taken: choose another.' : e.message));
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final x = _x;
    return TkFormSheet(
      title: x == null ? 'Save a ${widget.kind.name.toLowerCase()}' : 'Change @${x.name}',
      crumb: 'Saved',
      action: _busy ? 'Saving…' : 'Save',
      onAction: _busy ? null : _save,
      children: [
        TkFormGroup(
          children: [
            TkKitField(label: 'Name to call it up by', hint: 'Such as @car or @generator', child: TkTextBox(controller: _name, max: 33, plain: true)),
            TkKitField(label: 'Title', hint: 'How it is shown', child: TkTextBox(controller: _title, max: 80)),
          ],
        ),
        TkFormGroup(
          children: [
            for (final f in widget.kind.fields)
              () {
                final key = f.key ?? '';
                final kept = (f.sensitive ?? false) && (_had[key] ?? '').isNotEmpty;
                final hint = [
                  if (!(f.required ?? false)) 'Optional',
                  if (kept) 'Kept as ${_had[key]}: type it only to change it' else if (f.sensitive ?? false) 'Kept sealed',
                  if (f.type == 'date') 'Like 2027-03-03',
                ].join('. ');
                if (f.type == 'choice') {
                  return TkKitField(
                    label: f.label,
                    hint: hint.isEmpty ? null : hint,
                    field: false,
                    child: TkPickBox(label: f.label, shown: _v[key]!.text.isEmpty ? 'Choose' : _v[key]!.text, onTap: () => _pick(f)),
                  );
                }
                return TkKitField(
                  label: f.label,
                  hint: hint.isEmpty ? null : hint,
                  child: TkTextBox(controller: _v[key]!, max: 200, money: f.type == 'money', phone: f.type == 'phone', plain: f.type == 'email' || f.type == 'date'),
                );
              }(),
            TkKitField(label: 'Note', hint: 'Optional, for your own reference', child: TkTextBox(controller: _note, max: 200)),
          ],
        ),
        if (_error != null) TkCallout(leading: TkSquare('b-warn', color: look.red), title: _error!, color: look.red),
      ],
    );
  }
}

/// A made-up kind's thing in lines: each filled field, the sensitive ones
/// as [shown] gave them in full (after a passkey), else masked.
List<(String, String)> savedValueLines(SavedKind kind, SavedObject x, [Map<String, String> shown = const {}]) => [
  for (final f in kind.fields)
    if ('${x.values?[f.key] ?? ''}'.isNotEmpty) (f.label, shown[f.key] ?? savedShownValue(f, '${x.values?[f.key]}')),
];

/// A thing's details, its hidden values shown with a passkey.
Future<void> showSavedValues(BuildContext context, SavedKind kind, SavedObject x, Map<String, String> shown) => showTkSheet<void>(
  context,
  (context) => TkFormSheet(
    title: x.title,
    crumb: 'Saved',
    action: 'Done',
    onAction: () => Navigator.of(context).pop(),
    children: [
      TkFormGroup(children: [TkLines(savedValueLines(kind, x, shown))]),
    ],
  ),
);
