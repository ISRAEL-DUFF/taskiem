// Labels, in Money's settings (docs/spec/what-a-business-buys.md §5.4,
// §9): the business's own labels for what it spends on, the most used
// first, each renamed or merged into another by those who manage money.
// Past spending is never rewritten: reports group it by the label kept.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show MergeSpendLabelRequest, SpendLabel, SpendLabelName;
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkFormGroup, TkFormSheet, TkListRow, TkSection;
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart' show TkPageScreen;
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'groups.dart' show askSure, pickAction, pickOneOf;

/// How often a label is on spending, in words.
String labelUses(int n) => n == 0 ? 'Not used yet' : (n == 1 ? 'Used once' : 'Used $n times');

class LabelsPage extends StatefulWidget {
  const LabelsPage({super.key, required this.auth, this.backLabel = 'Back to Money'});
  final Auth auth;
  final String backLabel;

  @override
  State<LabelsPage> createState() => _LabelsPageState();
}

class _LabelsPageState extends State<LabelsPage> {
  List<SpendLabel>? _labels;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listSpendLabels();
      if (mounted) setState(() => (_labels = r.labels, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _open(SpendLabel l) async {
    final others = (_labels ?? const <SpendLabel>[]).where((x) => x.id != l.id).toList();
    final what = await pickAction(
      context,
      title: l.name,
      note: labelUses(l.uses),
      actions: [
        ('rename', 'Rename', 'b-note', false),
        if (others.isNotEmpty) ('merge', 'Merge into another label', 'b-flow', false),
      ],
    );
    if (!mounted || what == null) return;
    if (what == 'rename') {
      final name = await showTkSheet<String>(context, (_) => _RenameSheet(auth: widget.auth, label: l));
      if (name == null || !mounted) return;
      showTkToast(context, 'Renamed to $name');
      return _load();
    }
    final into = await pickOneOf(
      context,
      title: 'Merge ${l.name} into',
      searchHint: others.length > 8 ? 'Search your labels' : null,
      choices: [for (final o in others) (o.id, o.name, labelUses(o.uses))],
    );
    if (into == null || !mounted) return;
    final kept = others.firstWhere((o) => o.id == into);
    final yes = await askSure(
      context,
      title: 'Merge ${l.name} into ${kept.name}?',
      body: 'All of ${l.name}’s spending, past and to come, shows under ${kept.name} in your reports. Nothing already recorded is changed.',
      ok: 'Merge',
    );
    if (!yes || !mounted) return;
    try {
      await widget.auth.api.mergeSpendLabel(id: l.id, body: MergeSpendLabelRequest(into: kept.id));
      if (mounted) showTkToast(context, '${l.name} merged into ${kept.name}');
      await _load();
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final list = _labels;
    final manage = widget.auth.can('invoice.manage');
    return TkPageScreen(
      title: 'Labels',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'What you spend on, in your own words. Money reports group spending by label. A label is made by typing it on a purchase or an expense.',
      onRefresh: _load,
      children: [
        TkCallout(
          leading: TkSquare('b-flow', color: look.teal),
          title: 'Renaming or merging changes reports, not records',
          sub: 'Past spending moves with its label at once; nothing recorded is rewritten.',
          color: look.teal,
        ),
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error!, color: look.red, subLines: 3),
        if (list == null && _error == null) const TkLoading(rows: 3, label: 'Loading your labels…'),
        if (list != null && list.isEmpty) const TkEmpty(icon: 'tag', title: 'No labels yet', line: 'Label a purchase or an expense, and it is here.'),
        if (list != null && list.isNotEmpty)
          TkSection(
            children: [
              for (final l in list)
                TkListRow(
                  title: l.name,
                  letters: l.name,
                  sub: labelUses(l.uses),
                  semanticsLabel: manage ? 'Rename or merge ${l.name}' : null,
                  onTap: manage ? () => _open(l) : null,
                ),
            ],
          ),
      ],
    );
  }
}

class _RenameSheet extends StatefulWidget {
  const _RenameSheet({required this.auth, required this.label});
  final Auth auth;
  final SpendLabel label;

  @override
  State<_RenameSheet> createState() => _RenameSheetState();
}

class _RenameSheetState extends State<_RenameSheet> {
  late final _name = TextEditingController(text: widget.label.name);
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _name.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      final l = await widget.auth.api.renameSpendLabel(id: widget.label.id, body: SpendLabelName(name: _name.text.trim()));
      if (mounted) Navigator.of(context).pop(l.name);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final name = _name.text.trim();
    return TkFormSheet(
      title: 'Rename ${widget.label.name}',
      crumb: 'Labels',
      action: _busy ? 'Saving…' : 'Rename',
      onAction: _busy || name.isEmpty || name.length > 60 || name == widget.label.name ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(
          children: [
            TkKitField(
              label: 'Name',
              hint: 'To join it with a label you have, merge them instead.',
              child: TkTextBox(controller: _name, max: 60, size: 15, height: 46),
            ),
          ],
        ),
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error!, color: look.red, subLines: 3),
        Text('Reports show its spending under the new name at once.', style: tkText(size: 13, color: TkTheme.tokens(context).muted)),
      ],
    );
  }
}
