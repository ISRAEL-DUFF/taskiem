// A tool's sheet (web/src/pages/ChatTools.tsx ToolSheet): its list, with
// the search, the chips (the tool's own, then its rows' categories), each
// row, and the line to the rest. A row's form and the Add buttons come with
// the tool's milestone (M6 selling, M7 orders, M8 money); until then they
// say the tool's screens are coming, as the panel's button did.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../kit/list.dart';
import '../kit/modal.dart' show showComingSoon;
import '../kit/sheet.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'sizes_forms.dart' show GroupForm;
import 'bookings_page.dart' show BookingsPage;
import 'price_lists_page.dart' show PriceListsPage;
import 'stock_page.dart' show StockPage;
import 'tools.dart';
import 'tools_panel.dart';
import 'table_tool.dart' show TablesPage;
import 'trade_orders_page.dart' show TradeOrdersPage;

/// A tool's "Open …": the page it leads to (tools.ts `page`), where the
/// app has it as a screen; the rest say they are coming.
void openToolPage(BuildContext context, Tool tool, Auth auth) {
  final Widget? page = switch (tool.key) {
    'products' || 'stock' || 'sell' || 'handover' => StockPage(auth: auth),
    'services' => BookingsPage(auth: auth),
    'prices' => PriceListsPage(auth: auth),
    'orders' => TradeOrdersPage(auth: auth),
    'tables' => TablesPage(auth: auth),
    _ => null,
  };
  if (page == null) return showComingSoon(context);
  Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => page));
}

/// ChatTools.tsx mayEdit: whether the person may change what a tool shows.
bool mayEdit(String key, bool Function(String) can) => switch (key) {
  'products' || 'stock' || 'tables' || 'sell' => can('table.edit'),
  'services' => can('table.edit') && can('storefront.manage'),
  'purchases' => can('storefront.manage'),
  'orders' => can('storefront.manage|order.handle'),
  'order' => can('table.edit') && can('storefront.manage'),
  'bills' || 'invoices' || 'money' => can('invoice.manage'),
  _ => can('storefront.manage'),
};

/// Opens [tool]'s sheet over the conversation.
Future<void> showToolSheet(
  BuildContext context, {
  required ToolsController tools,
  required Tool tool,
  String? chip,
  void Function(BuildContext context, ToolRow row)? onRow,
  ToolDetail? detail,
  Widget? footer,
  String? openId,
  void Function(String words)? onSaved,
}) {
  tools.chats.prefs?.rememberTool(tool.key);
  return showTkSheet<void>(
    context,
    (_) => ToolSheet(tools: tools, tool: tool, chip: chip, onRow: onRow, detail: detail, footer: footer, openId: openId, onSaved: onSaved),
  );
}

/// A row's detail in its sheet (ChatTools.tsx Detail): the body and its own
/// foot; [done] with the words to show, once saved (the sheet then closes).
typedef ToolDetail = Widget? Function(BuildContext context, Tool tool, ToolRow? row, ToolHost host);

/// What a row's form (or a tool's Add) is given by its sheet.
class ToolHost {
  const ToolHost({required this.done, required this.back, required this.canEdit});

  /// Saved: the tick, the list read again, the sheet closing; [line], the
  /// change in words for where it was opened (null: nothing to tell).
  final void Function(String? line) done;

  /// Back to the list (a form's Cancel).
  final VoidCallback back;

  /// ChatTools.tsx mayEdit: whether the person may change it.
  final bool canEdit;
}

/// A product not sold in sizes and colours already (ChatTools.tsx plainItem).
bool _plain(ToolRow r) => r.data != null && ((r.data!['choices'] as List?)?.length ?? 0) == 0 && r.data!['variant_of'] == null;

class ToolSheet extends StatefulWidget {
  const ToolSheet({super.key, required this.tools, required this.tool, this.chip, this.onRow, this.detail, this.footer, this.openId, this.onSaved});
  final ToolsController tools;
  final Tool tool;

  /// A chip already on (a category picked in "@").
  final String? chip;

  /// A row pressed (its form, where the tool has one here).
  final void Function(BuildContext context, ToolRow row)? onRow;

  /// A row opened in the sheet, where the tool's detail is here (null:
  /// [onRow], else coming soon).
  final ToolDetail? detail;

  /// A row to open once the list is read (a card's View order): its id,
  /// else its number when one row of those here has it.
  final String? openId;

  /// Saved from a row's detail: where it was opened says where it went.
  final void Function(String words)? onSaved;

  /// The sheet's own button, in place of the tool's (New order).
  final Widget? footer;

  @override
  State<ToolSheet> createState() => _ToolSheetState();
}

class _ToolSheetState extends State<ToolSheet> {
  final _search = TextEditingController();
  late String? _chip = widget.chip;
  ToolRow? _open;
  bool _adding = false;

  // Make one product with sizes and colours: choosing, then its form.
  bool _picking = false;
  bool _grouping = false;
  final _picked = <String>[];
  bool _saved = false;
  late bool _opening = widget.openId != null;

  void _done(String? line) {
    widget.tools.refresh(widget.tool);
    if (line != null) widget.onSaved?.call(line);
    if (!mounted) return;
    // FormFrame's SavedTick: "Saved", whatever was saved.
    setState(() => _saved = true);
    Future<void>.delayed(const Duration(milliseconds: 1300), () {
      if (mounted) Navigator.of(context).maybePop();
    });
  }

  @override
  void initState() {
    super.initState();
    _search.addListener(() => setState(() {}));
    widget.tools.load([widget.tool], again: true);
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
        if (_opening && list != null) {
          _opening = false;
          // ChatTools.tsx startRow.
          final named = list.where((r) => r.data?['reference'] == widget.openId).toList();
          _open = list.where((r) => r.id == widget.openId).firstOrNull ?? (named.length == 1 ? named.single : null);
        }
        final open = _open;
        final host = ToolHost(done: _done, back: () => setState(() => (_open = null, _adding = false)), canEdit: mayEdit(tool.key, c.can));
        final detail = open == null && !_adding ? null : widget.detail?.call(context, widget.tool, open, host);
        if ((open != null || _adding) && detail != null) {
          return TkSheet(
            // A row: its name, the tool's above; a new one: its Add's words.
            title: open?.name ?? add ?? title,
            crumb: open == null ? null : title,
            onBack: host.back,
            overlay: _saved ? const TkSavedTick() : null,
            body: detail,
          );
        }
        if (_grouping && list != null) {
          return TkSheet(
            title: 'Make one product',
            onBack: () => setState(() => _grouping = false),
            overlay: _saved ? const TkSavedTick() : null,
            body: GroupForm(
              auth: c.chats.auth,
              items: [
                // In the list's order, as the web's picked rows.
                for (final r in list)
                  if (_picked.contains(r.id) && r.data != null) r.data!,
              ],
              onSaved: (line) {
                setState(() {
                  _picking = false;
                  _grouping = false;
                  _picked.clear();
                });
                _done(line);
              },
              onCancel: () => setState(() => _grouping = false),
            ),
          );
        }
        final adds = canAdd && widget.detail?.call(context, widget.tool, null, host) != null;
        return TkSheet(
          title: title,
          count: list?.length,
          overlay: _saved ? const TkSavedTick() : null,
          footer: _picking
              ? TkCta(
                  label: _picked.length < 2 ? 'Choose at least two' : 'Make one product of ${_picked.length}',
                  onTap: _picked.length < 2 ? null : () => setState(() => _grouping = true),
                )
              : widget.footer ??
                    TkCta(
                      label: canAdd ? add : tool.more,
                      icon: canAdd ? 'plus' : null,
                      onTap: adds ? () => setState(() => _adding = true) : () => openToolPage(context, tool, c.chats.auth),
                    ),
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
              // may change them, with two plain items): `.tk-group-bar`.
              if (tool.key == 'products' && mayEdit(tool.key, c.can) && (list ?? const []).where(_plain).length >= 2)
                Padding(
                  padding: const EdgeInsets.fromLTRB(20, 0, 20, 8),
                  child: _picking
                      ? Wrap(
                          spacing: 8,
                          runSpacing: 8,
                          crossAxisAlignment: WrapCrossAlignment.center,
                          children: [
                            Semantics(
                              liveRegion: true,
                              child: Text(
                                'Choose the items that are one product in sizes and colours.',
                                style: tkText(size: 15, color: TkTheme.tokens(context).muted),
                              ),
                            ),
                            TkSoftButton(
                              label: 'Cancel',
                              onTap: () => setState(() {
                                _picking = false;
                                _picked.clear();
                              }),
                            ),
                          ],
                        )
                      : Align(
                          alignment: Alignment.centerLeft,
                          child: TkSoftButton(label: 'Make one product with sizes and colours', onTap: () => setState(() => _picking = true)),
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
    if (list == null) {
      return ListView(padding: pad, children: [TkLoading(label: 'Loading $name…')]);
    }
    final shown = filterRows(list, _chip, _search.text);
    if (shown.isEmpty) {
      return ListView(
        padding: pad,
        children: [
          list.isEmpty
              ? TkEmpty(
                  icon: tool.icon,
                  title: 'No $name yet',
                  line: tool.add != null ? '${tool.add} to put the first one here.' : '${tool.more} to add the first.',
                )
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
          return TkSheetMore(label: tool.more, onTap: () => openToolPage(context, tool, c.chats.auth));
        }
        final r = shown[i];
        if (_picking) return _pickRow(context, r);
        return TkRow(
          name: r.name,
          sub: r.sub,
          letters: tool.rowIcon == null ? tileLetters(r.name) : null,
          icon: tool.rowIcon,
          pill: r.pill == null ? null : TkPill(r.pill!.text, tone: r.pill!.tone),
          onTap: () => widget.detail?.call(context, widget.tool, r, ToolHost(done: _done, back: () {}, canEdit: false)) != null
              ? setState(() => _open = r)
              : widget.onRow != null
              ? widget.onRow!(context, r)
              : showComingSoon(context),
        );
      },
    );
  }

  /// `.tk-row.tk-pick`: a box to choose it; one sold in sizes already is
  /// shown, not to be chosen.
  Widget _pickRow(BuildContext context, ToolRow r) {
    final t = TkTheme.tokens(context);
    final ok = _plain(r);
    final on = _picked.contains(r.id);
    return Semantics(
      checked: on,
      enabled: ok,
      label: r.name,
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: !ok ? null : () => setState(() => on ? _picked.remove(r.id) : _picked.add(r.id)),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 8),
          child: Row(
            children: [
              TkCheckbox(checked: on),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(r.name, style: tkText(size: 16, weight: 600, color: ok ? t.text : t.muted)),
                    Text(ok ? r.sub : 'Already sold in sizes and colours', style: tkText(size: 14, color: t.muted)),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
