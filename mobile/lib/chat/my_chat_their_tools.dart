// Their tools: a business's tools for its customers (made tools T5; docs/
// tools.md, "Customers' tools"), in a person's chat with it, behind the
// tools button beside Their shop and Book. A tool opens its sheet: its
// form, drawn from its inputs; then what will run and Yes (a customer
// always says yes, whatever the tool does); then its answer, read until
// its run ends, drawn from what the business made public. A button on the
// answer opens another of their tools, filled in.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/forms.dart' show TkSwitch, nairaKobo;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkChips, TkFormGroup, TkFormSheet, TkListRow, TkSection;
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart' show CardPill, cardDecoration;
import 'groups.dart' show pickOneOf;
import 'made_tools.dart' show MadeAnswerCard, MadeRun, SchemaField, schemaFields;
import 'my_chat.dart' show MyChatData, sayFailure;
import 'words.dart' show CardTone;

/// The business's tools for its customers; none when it has none, or they
/// could not be read (the tools sheet then shows its own rows only).
Future<List<CustomerTool>> theirTools(MyChatData d) async {
  try {
    return (await d.api.myChatTools(id: d.id)).tools;
  } on Object {
    return const [];
  }
}

/// Their tools as rows on the tools sheet, each with its about.
class TheirToolsSection extends StatelessWidget {
  const TheirToolsSection({super.key, required this.tools, required this.onPick});
  final List<CustomerTool> tools;
  final void Function(CustomerTool) onPick;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkSection(
      title: 'Their tools',
      inset: 56,
      children: [
        for (final tool in tools)
          TkListRow(
            title: tool.name,
            sub: tool.about ?? '',
            icon: 'b-bolt',
            square: look.blue,
            onTap: () => onPick(tool),
          ),
      ],
    );
  }
}

/// Opens one of their tools: its sheet, filled in with [initial].
Future<void> showTheirToolSheet(BuildContext context, MyChatData d, CustomerTool tool, {List<CustomerTool> all = const [], Map<String, dynamic> initial = const {}}) async {
  final next = await showTkSheet<({CustomerTool tool, Map<String, dynamic> values})>(context, (_) => TheirToolSheet(d: d, tool: tool, all: all, initial: initial));
  // A button on the answer pressed: the tool it opens, filled in.
  if (next != null && context.mounted) await showTheirToolSheet(context, d, next.tool, all: all, initial: next.values);
}

String _shown(Object? v) => switch (v) {
  null => '',
  num n when n == n.roundToDouble() => '${n.toInt()}',
  _ => '$v',
};

/// One of their tools: its form, then Yes, then its answer.
class TheirToolSheet extends StatefulWidget {
  const TheirToolSheet({super.key, required this.d, required this.tool, this.all = const [], this.initial = const {}});
  final MyChatData d;
  final CustomerTool tool;
  final List<CustomerTool> all;
  final Map<String, dynamic> initial;

  /// How often an answer is read while its run goes on.
  static Duration every = const Duration(milliseconds: 1500);

  @override
  State<TheirToolSheet> createState() => _TheirToolSheetState();
}

class _TheirToolSheetState extends State<TheirToolSheet> {
  late final List<SchemaField> _fields = schemaFields(widget.tool.inputs).where((f) => f.kind != 'device' && f.kind != 'other').toList();
  final _boxes = <String, TextEditingController>{};
  final _picked = <String, Object?>{};

  /// Yes is asked: what will run, and the confirm to send back.
  ToolStart? _start;
  Map<String, dynamic>? _sent;

  /// Said yes: its run, and its answer as last read.
  String? _run;
  ToolAnswer? _answer;
  bool _busy = false;
  String? _error;
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    for (final f in _fields) {
      final v = widget.initial[f.name];
      switch (f.kind) {
        case 'yes_no':
          _picked[f.name] = v == true;
        case 'choice':
          if (v != null) _picked[f.name] = f.choices.where((c) => '$c' == '$v').firstOrNull;
        case 'choices':
          _picked[f.name] = v is List ? [...v] : <Object?>[];
        default:
          _boxes[f.name] = TextEditingController(text: _shown(v))..addListener(_changed);
      }
    }
  }

  void _changed() => setState(() {});

  @override
  void dispose() {
    _timer?.cancel();
    for (final b in _boxes.values) {
      b.dispose();
    }
    super.dispose();
  }

  String? _problem(SchemaField f) {
    final s = _boxes[f.name]?.text.trim() ?? '';
    if (s.isEmpty) return null;
    if (f.kind == 'money' && nairaKobo(s) == null) return 'An amount in naira, like 4,500';
    if (f.kind == 'number') {
      final n = num.tryParse(s.replaceAll(',', ''));
      if (n == null) return 'A number';
      if (f.prop['type'] == 'integer' && n != n.roundToDouble()) return 'A whole number';
    }
    return null;
  }

  bool get _ready => _fields.every((f) {
    if (_problem(f) != null) return false;
    if (!f.required) return true;
    return switch (f.kind) {
      'yes_no' => true,
      'choice' => _picked[f.name] != null,
      'choices' => (_picked[f.name] as List).isNotEmpty,
      _ => _boxes[f.name]!.text.trim().isNotEmpty,
    };
  });

  Map<String, dynamic> _input() {
    final out = <String, dynamic>{};
    for (final f in _fields) {
      switch (f.kind) {
        case 'yes_no':
          out[f.name] = _picked[f.name] == true;
        case 'choice':
          if (_picked[f.name] != null) out[f.name] = _picked[f.name];
        case 'choices':
          if ((_picked[f.name] as List).isNotEmpty) out[f.name] = _picked[f.name];
        case 'money':
          final k = nairaKobo(_boxes[f.name]!.text.trim());
          if (k != null) out[f.name] = k % 100 == 0 ? k ~/ 100 : k / 100;
        case 'number':
          final n = num.tryParse(_boxes[f.name]!.text.trim().replaceAll(',', ''));
          if (n != null) out[f.name] = f.prop['type'] == 'integer' ? n.toInt() : n;
        default:
          final s = _boxes[f.name]!.text.trim();
          if (s.isNotEmpty) out[f.name] = s;
      }
    }
    return out;
  }

  String _say(Object e) {
    if (e is ApiError && e.status == 422) return 'Some details don’t fit. Check them and try again.';
    return sayFailure(e);
  }

  Future<void> _next() async {
    if (_busy) return;
    setState(() => (_busy = true, _error = null));
    final input = _input();
    try {
      final s = await widget.d.api.startMyChatTool(id: widget.d.id, command: widget.tool.command, body: StartMyChatToolRequest(input: input));
      if (mounted) setState(() => (_start = s, _sent = input));
    } on Object catch (e) {
      if (mounted) setState(() => _error = _say(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _yes() async {
    if (_busy || _start?.confirm == null) return;
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.d.api.confirmMyChatTool(
        id: widget.d.id,
        command: widget.tool.command,
        body: ConfirmMyChatToolRequest(confirm: _start!.confirm!, input: _sent),
      );
      if (!mounted) return;
      setState(() => _run = r.runId);
      unawaited(_read());
    } on Object catch (e) {
      if (mounted) setState(() => _error = _say(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  bool get _going => _answer == null || const {'running', 'queued', 'waiting', 'pending'}.contains(_answer!.status);

  Future<void> _read() async {
    if (!mounted || _run == null) return;
    try {
      final a = await widget.d.api.myChatToolAnswer(id: widget.d.id, run: _run!);
      if (mounted) setState(() => _answer = a);
    } on Object {
      // Offline for a moment: read again.
    }
    if (mounted && _going) _timer = Timer(TheirToolSheet.every, _read);
  }

  Future<void> _press(MadeRun run, ToolButton b, String key) async {
    final token = b.token;
    if (token == null) return;
    try {
      final p = await widget.d.api.pressMyChatToolButton(id: widget.d.id, token: token);
      final tool = widget.all.where((t) => t.command == p.command).firstOrNull;
      if (!mounted) return;
      if (tool == null) {
        showTkToast(context, 'This isn’t available any more.', error: true);
        return;
      }
      Navigator.of(context).pop((tool: tool, values: p.inputs));
    } on Object catch (e) {
      if (mounted) showTkToast(context, e is ApiError && e.status == 404 ? 'This button isn’t available any more.' : sayFailure(e), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final tool = widget.tool;
    if (_run != null) {
      return TkFormSheet(
        title: tool.name,
        crumb: 'Their tools',
        action: 'Done',
        onAction: () => Navigator.of(context).pop(),
        children: [_answerView(context)],
      );
    }
    if (_start != null) {
      final values = _start!.values;
      return TkFormSheet(
        title: tool.name,
        crumb: 'Check, then say yes',
        action: _busy ? 'Starting…' : 'Yes, go ahead',
        onAction: _busy ? null : _yes,
        second: 'Back',
        onSecond: _busy ? null : () => setState(() => (_start = null, _error = null)),
        children: [
          TkFormGroup(
            title: 'What you’re asking for',
            children: [
              if (values.isEmpty)
                Text('Nothing to fill in.', style: tkText(size: 14.5, color: TkTheme.tokens(context).muted))
              else
                TkLines([for (final v in values) (v.label, v.value)]),
            ],
          ),
          if (_error != null) _errorNote(context),
        ],
      );
    }
    return TkFormSheet(
      title: tool.name,
      crumb: (tool.about ?? '').isEmpty ? 'Their tools' : tool.about,
      action: _busy ? 'Checking…' : 'Next',
      onAction: _busy || !_ready ? null : _next,
      children: [
        if (_fields.isNotEmpty) TkFormGroup(children: [for (final f in _fields) _field(context, f)]),
        if (_fields.isEmpty) Text('Nothing to fill in. Next shows what will happen, and you say yes.', style: tkText(size: 14.5, color: TkTheme.tokens(context).muted)),
        if (_error != null) _errorNote(context),
      ],
    );
  }

  Widget _errorNote(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TkCallout(
      leading: TkSquare('b-alert', color: TkLook.of(context).red),
      title: _error!,
      color: t.danger,
      subLines: 3,
    );
  }

  String _label(SchemaField f) => f.required || f.kind == 'yes_no' ? f.label : '${f.label} (optional)';

  Widget _field(BuildContext context, SchemaField f) {
    final t = TkTheme.tokens(context);
    switch (f.kind) {
      case 'yes_no':
        return Row(
          children: [
            Expanded(child: Text(f.label, style: tkText(size: 15, weight: 600, color: t.text))),
            TkSwitch(label: f.label, on: _picked[f.name] == true, onChanged: (v) => setState(() => _picked[f.name] = v)),
          ],
        );
      case 'choice':
        final on = _picked[f.name];
        if (f.choices.length <= 3 && f.choices.every((c) => '$c'.length <= 12)) {
          return TkKitField(
            label: _label(f),
            hint: f.hint,
            field: false,
            child: TkChips<Object?>(
              label: f.label,
              padding: EdgeInsets.zero,
              chips: [for (final c in f.choices) (c, '$c')],
              on: on,
              onPick: (v) => setState(() => _picked[f.name] = v),
            ),
          );
        }
        return TkKitField(
          label: _label(f),
          hint: f.hint,
          field: false,
          child: TkPickBox(
            label: f.label,
            shown: on == null ? 'Choose…' : '$on',
            onTap: () async {
              final got = await pickOneOf(context, title: f.label, value: on == null ? null : '$on', choices: [for (final c in f.choices) ('$c', '$c', '')]);
              if (got == null) return;
              setState(() => _picked[f.name] = f.choices.firstWhere((c) => '$c' == got, orElse: () => got));
            },
          ),
        );
      case 'choices':
        final on = _picked[f.name] as List;
        return TkKitField(
          label: _label(f),
          hint: f.hint,
          field: false,
          child: TkChips<Object?>(
            label: f.label,
            padding: EdgeInsets.zero,
            chips: [for (final c in f.choices) (c, on.contains(c) ? '✓ $c' : '$c')],
            on: null,
            onPick: (c) => setState(() => on.contains(c) ? on.remove(c) : on.add(c)),
          ),
        );
    }
    return TkKitField(
      label: _label(f),
      hint: f.hint,
      error: _problem(f),
      child: TkTextBox(
        controller: _boxes[f.name]!,
        size: 15,
        height: 46,
        lines: f.kind == 'long' ? 3 : 1,
        money: f.kind == 'money',
        signed: f.kind == 'number',
        prefix: f.kind == 'money' ? '₦' : null,
        phone: f.prop['format'] == 'phone-ng',
        max: f.kind == 'long' ? 2000 : 300,
      ),
    );
  }

  Widget _answerView(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = _answer;
    final tool = widget.tool;
    final reference = _run!.substring(0, 8).toUpperCase();
    if (a == null || _going) {
      return _note(
        context,
        pill: const CardPill('Working on it', CardTone.warn),
        title: a?.status == 'waiting' ? 'Waiting on ${widget.d.chat?.name ?? 'the business'}' : 'Working on it',
        words: 'Your reference is $reference. The answer shows here when it’s ready.',
      );
    }
    if (a.status != 'completed') {
      return _note(
        context,
        pill: const CardPill('Didn’t go through', CardTone.danger),
        title: '${tool.name} didn’t go through',
        words: 'Your reference is $reference. Try again, or ask them here in the chat.',
      );
    }
    if (a.type == 'card' || a.type == 'list') {
      final run = MadeRun(id: _run!, command: tool.command, name: tool.name, values: const [], at: DateTime.now(), answer: a);
      return MadeAnswerCard(run: run, auth: widget.d.auth, onButton: _press, shownRows: 10);
    }
    final words = a.type == 'text' && (a.text ?? '').isNotEmpty ? a.text! : 'Done. Your reference is $reference.';
    return _note(context, pill: const CardPill('Done', CardTone.lime), title: tool.name, words: words, wordsColor: t.text);
  }

  Widget _note(BuildContext context, {required Widget pill, required String title, required String words, Color? wordsColor}) {
    final t = TkTheme.tokens(context);
    return Container(
      padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
      decoration: cardDecoration(context, mine: false, first: true),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          pill,
          const SizedBox(height: 8),
          Text(title, style: tkText(size: 15.5, weight: 600, color: t.text)),
          const SizedBox(height: 4),
          Text(words, style: tkText(size: 14, lineHeight: 1.4, color: wordsColor ?? t.muted)),
        ],
      ),
    );
  }
}
