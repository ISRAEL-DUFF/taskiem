// Ready-made tools (made tools §13, §17; build plan M6): the template
// library's recipes, each a tool used from the chat, installed from the
// phone with the word it is called by. GET /v1/templates?tool=true lists
// them with the tool each makes; POST /v1/templates/{id}/instantiate saves
// it as a draft with the command asked for, or the next free one when it
// is taken, and says which (web/src/pages/Templates.tsx, the same words).
// Publishing stays where it is: the web, or "publish" in this chat.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart' show TkAppLook;
import '../kit/list.dart' show TkEmpty;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../theme/look.dart';
import '../theme/look_icons.dart' show tkLookIcons;
import 'approve.dart' show TkTextBox;

/// Opens Ready-made tools.
Future<void> openRecipes(BuildContext context, Auth auth, {String backLabel = 'Back'}) =>
    Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => RecipesPage(auth: auth, backLabel: backLabel)));

/// A typed word as the server writes a command: "/Add Sale" is add-sale.
String commandOf(String typed) => typed.trim().replaceFirst(RegExp(r'^/'), '').toLowerCase().split(RegExp(r'\s+')).where((w) => w.isNotEmpty).join('-');

/// Roles typed with commas as a list: "owner, Shift manager" is
/// [owner, shift_manager] (review M6, 4: who can use a recipe's tool).
List<String> rolesOf(String typed) => [
  for (final r in typed.split(','))
    if (r.trim().isNotEmpty) r.trim().toLowerCase().replaceAll(RegExp(r'\s+'), '_'),
];

/// A recipe's tool in plain words, as the web says them.
List<String> recipeWords(TemplateTool t) => [
  t.for_ == 'customers' ? 'Your customers use it' : 'Your team uses it',
  switch (t.care) {
    'answers' => 'Answers at once: it only reads',
    'step_up' => 'Asks yes and a passkey first',
    _ => 'Asks yes first',
  },
  switch (t.answer) {
    'card' => 'Says back a card',
    'list' => 'Says back a list',
    'text' => 'Says back a sentence',
    _ => 'Says it ran',
  },
  if (t.companion != null) 'Comes with “${t.companion}”, a schedule whose button opens it',
];

/// What installing a recipe says: its command, the next free one when the
/// one asked for was taken.
String installedWords(TemplateInstantiatedTool r) {
  final said = r.changed ? '/${r.asked} is taken, so it is called with /${r.command}.' : 'It is called with /${r.command}.';
  return r.companion == null ? said : '$said Its schedule, ${r.companion!.name}, was saved as a draft too.';
}

/// A recipe's icon among the app's.
String recipeIcon(TemplateTool t) {
  final n = 'b-${t.icon ?? ''}';
  if (tkLookIcons.containsKey(n)) return n;
  return switch (t.icon) {
    'list' => 'b-table',
    'chart' => 'b-bars',
    'wrench' => 'b-tools',
    'server' => 'b-flow',
    _ => 'b-bolt',
  };
}

class RecipesPage extends StatefulWidget {
  const RecipesPage({super.key, required this.auth, this.backLabel = 'Back'});
  final Auth auth;
  final String backLabel;

  @override
  State<RecipesPage> createState() => _RecipesPageState();
}

class _RecipesPageState extends State<RecipesPage> {
  List<Template>? _list;
  Object? _error;
  // The last recipe installed, and what it was given.
  ({String title, TemplateInstantiatedTool tool})? _done;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listTemplates(tool: 'true');
      if (mounted) setState(() => (_list = r.templates, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _open(Template t) async {
    final got = await showTkSheet<TemplateInstantiatedTool>(context, (_) => TkAppLook(child: _InstallSheet(auth: widget.auth, template: t)));
    if (got != null && mounted) setState(() => _done = (title: t.tool!.name, tool: got));
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final list = _list;
    final can = widget.auth.can('workflow.edit');
    return TkPageScreen(
      title: 'Ready-made tools',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Tools your team uses from the chat, ready to add: choose the word it is called by. Each is saved as a draft; publish it and it is in everyone’s tools.',
      onRefresh: _load,
      children: [
        if (_done case final d?)
          TkCallout(
            leading: TkSquare('b-check', color: look.teal),
            title: '${d.title}: saved as a draft',
            sub: installedWords(d.tool),
            subLines: 4,
            color: look.teal,
          ),
        if (!can)
          TkCallout(leading: TkSquare('b-lock', color: look.violet), title: 'Adding a tool needs a builder', sub: 'Ask an owner to add one for you.', color: look.violet),
        if (_error != null)
          TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'The tools did not load', sub: errorWords(_error!), subLines: 4, color: look.red),
        if (list == null && _error == null) const TkSkeleton(rows: 4, label: 'Loading the tools…'),
        if (list != null && list.isEmpty) const TkEmpty(icon: 'b-tools', title: 'No ready-made tools', line: 'Describe one in this chat instead: build, then what it should do.'),
        if (list != null && list.isNotEmpty)
          TkSection(
            title: 'For the chat',
            inset: 56,
            children: [
              for (final t in list)
                if (t.tool case final tool?)
                  TkListRow(
                    title: t.title,
                    titleLines: 2,
                    sub: '/${tool.command} · ${recipeWords(tool)[1]}',
                    icon: recipeIcon(tool),
                    square: tool.care == 'answers' ? look.teal : look.violet,
                    semanticsLabel: '${t.title}, /${tool.command}. ${recipeWords(tool).join('. ')}. Add it',
                    onTap: can ? () => unawaited(_open(t)) : null,
                  ),
            ],
          ),
      ],
    );
  }
}

/// A recipe's sheet: what the tool does, its word, and what it needs that
/// has no default; the rest takes the template's own.
class _InstallSheet extends StatefulWidget {
  const _InstallSheet({required this.auth, required this.template});
  final Auth auth;
  final Template template;

  @override
  State<_InstallSheet> createState() => _InstallSheetState();
}

class _InstallSheetState extends State<_InstallSheet> {
  late final Map<String, TextEditingController> _fields;
  bool _busy = false;
  String? _error;

  // The parameters asked here: its word, who can use it, and those the
  // template has no default for. The pickers' ones (a table, a column)
  // keep their defaults, chosen again on the web.
  List<TemplateParam> get _asked => [
    for (final p in widget.template.params)
      if (p.type == 'command' || p.type == 'roles' || (p.required && p.default_ == null)) p,
  ];

  @override
  void initState() {
    super.initState();
    _fields = {for (final p in _asked) p.name: TextEditingController(text: _start(p))};
  }

  // A field's first words: its word, or who can use it, as the recipe has
  // them ("owner, manager").
  static String _start(TemplateParam p) => switch (p.type) {
    'command' => '${p.default_ ?? p.example ?? ''}',
    'roles' => switch (p.default_ ?? p.example) {
      final List<dynamic> l => l.join(', '),
      final Object v => '$v',
      null => '',
    },
    _ => '',
  };

  @override
  void dispose() {
    for (final c in _fields.values) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _add() async {
    final params = <String, dynamic>{};
    for (final p in _asked) {
      final v = _fields[p.name]!.text.trim();
      if (v.isEmpty) {
        setState(() => _error = '${p.title} is needed.');
        return;
      }
      params[p.name] = switch (p.type) {
        'command' => commandOf(v),
        'roles' => rolesOf(v),
        _ => v,
      };
    }
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.auth.api.instantiateTemplate(id: widget.template.id, body: InstantiateRequest(params: params));
      if (!mounted) return;
      final tool = r.tool;
      if (tool != null) showTkToast(context, 'Saved as a draft: /${tool.command}');
      Navigator.of(context).pop(tool);
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = errorWords(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final tool = widget.template.tool!;
    return TkFormSheet(
      title: tool.name,
      crumb: 'Ready-made tools',
      action: _busy ? 'Adding…' : 'Add it as a draft',
      onAction: _busy ? null : () => unawaited(_add()),
      foot: 'It does nothing until it is published.',
      children: [
        TkFormGroup(
          title: 'What it does',
          children: [
            TkListRow(title: tool.about, titleLines: 3, sub: recipeWords(tool).join(' · '), subLines: 4, icon: recipeIcon(tool), square: look.teal, chevron: false),
          ],
        ),
        TkFormGroup(
          children: [
            for (final p in _asked)
              TkKitField(
                label: switch (p.type) {
                  'command' => 'Its word, after /',
                  'roles' => 'Who can use it',
                  _ => p.title,
                },
                hint: p.description,
                child: TkTextBox(controller: _fields[p.name]!, max: p.maxLength ?? 300),
              ),
          ],
        ),
        if (_error case final e?) TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'Not added', sub: e, subLines: 4, color: look.red),
      ],
    );
  }
}
