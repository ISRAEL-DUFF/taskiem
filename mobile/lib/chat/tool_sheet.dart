// A tool's sheet (web/src/pages/ChatTools.tsx ToolSheet): its list, with
// the search, the chips (the tool's own, then its rows' categories), each
// row, and the line to the rest. A row's form and the Add buttons come with
// the tool's milestone (M6 selling, M7 orders, M8 money); until then they
// say the tool's screens are coming, as the panel's button did.

import 'package:flutter/widgets.dart';

import '../kit/list.dart';
import '../kit/modal.dart' show showComingSoon;
import '../kit/sheet.dart';
import 'tools.dart';
import 'tools_panel.dart';

/// ChatTools.tsx mayEdit: whether the person may change what a tool shows.
bool mayEdit(String key, bool Function(String) can) => switch (key) {
  'products' || 'stock' || 'tables' || 'sell' => can('table.edit'),
  'services' => can('table.edit') && can('storefront.manage'),
  'purchases' => can('storefront.manage'),
  'orders' => can('storefront.manage|order.handle'),
  'order' => can('table.edit') && can('storefront.manage'),
  'bills' || 'invoices' => can('invoice.manage'),
  _ => false,
};

/// Opens [tool]'s sheet over the conversation.
Future<void> showToolSheet(
  BuildContext context, {
  required ToolsController tools,
  required Tool tool,
  String? chip,
  void Function(BuildContext context, ToolRow row)? onRow,
  Widget? footer,
}) {
  tools.chats.prefs?.rememberTool(tool.key);
  return showTkSheet<void>(context, (_) => ToolSheet(tools: tools, tool: tool, chip: chip, onRow: onRow, footer: footer));
}

/// A product not sold in sizes and colours already (ChatTools.tsx plainItem).
bool _plain(ToolRow r) => ((r.data?['choices'] as List?)?.length ?? 0) == 0 && r.data?['variant_of'] == null;

class ToolSheet extends StatefulWidget {
  const ToolSheet({super.key, required this.tools, required this.tool, this.chip, this.onRow, this.footer});
  final ToolsController tools;
  final Tool tool;

  /// A chip already on (a category picked in "@").
  final String? chip;

  /// A row pressed (its form, where the tool has one here).
  final void Function(BuildContext context, ToolRow row)? onRow;

  /// The sheet's own button, in place of the tool's (New order).
  final Widget? footer;

  @override
  State<ToolSheet> createState() => _ToolSheetState();
}

class _ToolSheetState extends State<ToolSheet> {
  final _search = TextEditingController();
  late String? _chip = widget.chip;

  @override
  void initState() {
    super.initState();
    _search.addListener(() => setState(() {}));
    widget.tools.load([widget.tool]);
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: widget.tools,
      builder: (context, _) {
        final c = widget.tools;
        final tool = widget.tool;
        final list = c.rowsOf(tool);
        final title = tool.titleOf(list);
        final name = title.toLowerCase();
        final add = tool.add;
        final canAdd = add != null && mayEdit(tool.key, c.can);
        return TkSheet(
          title: title,
          count: list?.length,
          footer: widget.footer ?? TkCta(label: canAdd ? add : tool.more, icon: canAdd ? 'plus' : null, onTap: () => showComingSoon(context)),
          body: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TkSearchBox(controller: _search, hint: 'Search $name'),
              if (list != null && (tool.chips.isNotEmpty || categoryChips(list, _chip).isNotEmpty))
                TkChipRow(
                  chips: [
                    for (final ch in [...tool.chips, ...categoryChips(list, _chip)])
                      (label: '${ch.label} · ${list.where((r) => r.tags.contains(ch.tag)).length}', tag: ch.tag),
                  ],
                  on: _chip,
                  onPick: (tag) => setState(() => _chip = tag),
                ),
              // Make one product with sizes and colours (Products, for those who
              // may change them, with two plain items): its form is M6's.
              if (tool.key == 'products' && mayEdit(tool.key, c.can) && (list ?? const []).where(_plain).length >= 2)
                Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: TkSoftButton(label: 'Make one product with sizes and colours', onTap: () => showComingSoon(context)),
                  ),
                ),
              Expanded(child: _list(context, list, name)),
            ],
          ),
        );
      },
    );
  }

  Widget _list(BuildContext context, List<ToolRow>? list, String name) {
    final c = widget.tools;
    final tool = widget.tool;
    // `.tk-sheet-list { padding: 0 12px }`
    const pad = EdgeInsets.symmetric(horizontal: 12);
    if (c.failed(tool)) {
      return ListView(
        padding: pad,
        children: [
          TkEmpty(
            title: 'Your $name could not be loaded',
            line: 'Check your connection, then try again.',
            action: TkSoftButton(label: 'Try again', onTap: () => c.retry(tool)),
          ),
        ],
      );
    }
    if (list == null) return ListView(padding: pad, children: [TkLoading(label: 'Loading $name…')]);
    final shown = filterRows(list, _chip, _search.text);
    if (shown.isEmpty) {
      return ListView(
        padding: pad,
        children: [
          list.isEmpty
              ? TkEmpty(icon: tool.icon, title: 'No $name yet', line: tool.add != null ? '${tool.add} to put the first one here.' : '${tool.more} to add the first.')
              : const TkEmpty(icon: 'search', title: 'Nothing matches', line: 'Try another word, or show all.'),
        ],
      );
    }
    return ListView.builder(
      padding: pad,
      itemCount: shown.length + 1,
      itemBuilder: (context, i) {
        if (i == shown.length) {
          // The rest, for what the sheet does not do, after the list.
          return TkSheetMore(label: tool.more, onTap: () => showComingSoon(context));
        }
        final r = shown[i];
        return TkRow(
          name: r.name,
          sub: r.sub,
          letters: tool.rowIcon == null ? tileLetters(r.name) : null,
          icon: tool.rowIcon,
          pill: r.pill == null ? null : TkPill(r.pill!.text, tone: r.pill!.tone),
          onTap: () => widget.onRow != null ? widget.onRow!(context, r) : showComingSoon(context),
        );
      },
    );
  }
}
