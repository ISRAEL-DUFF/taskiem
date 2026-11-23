// A shop's stock (M6 part T; web/src/pages/StorefrontStock.tsx, lib/
// stock.ts; docs/stock.md), opened from Stock's or Products' "Open stock":
// today's sales (Record a sale, a terminal's sale said), what is low, each
// stock item with what it is sold as (S2, docs/spec/what-a-business-buys.md
// §4.5) and Record stock, Change and its History, and every change kept.

import 'dart:math' as math;

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show ListStockItemsResponse, StockItemRow;
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../kit/look.dart';
import '../kit/mobile.dart' show TkChips, TkCountUp, TkHeadButton, TkListRow, TkRoute, TkSection;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'book_sheet.dart' show whenLabel;
import 'chats.dart' show LinkSection;
import 'code_scanner.dart';
import 'groups.dart' show pickOneOf;
import 'product_form.dart' show sizesWords;
import 'purchases_page.dart' show PurchasesCard, SuppliesPage;
import 'stock_items.dart';
import 'words.dart' show Tone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int? _int(Object? v) => (v as num?)?.toInt();

/// stock.ts plural: bag, bags; box, boxes; battery, batteries.
String plural(String unit) {
  final l = unit.toLowerCase();
  if (RegExp(r'(s|x|ch|sh)$').hasMatch(l)) return l.endsWith('s') && !l.endsWith('ss') ? unit : '${unit}es';
  if (RegExp(r'[^aeiou]y$').hasMatch(l)) return '${unit.substring(0, unit.length - 1)}ies';
  return '${unit}s';
}

/// stock.ts quantity: "12 bags", "1 carton", "4".
String stockQuantity(int n, String? unit) {
  final u = unit?.trim() ?? '';
  return u.isEmpty ? '$n' : '$n ${n.abs() == 1 ? u : plural(u)}';
}

/// stock.ts stockLine: "48 cartons", "No count".
String stockLine(Map<String, dynamic> it) => _int(it['stock']) == null ? 'No count' : stockQuantity(_int(it['stock'])!, it['unit'] as String?);

/// stock.ts REASON: why a change was made.
const reasons = {
  'order': 'Order',
  'sale': 'Sold',
  'received': 'Received',
  'adjustment': 'Adjusted',
  'count': 'Counted',
  'moved': 'Moved',
};

/// stock.ts movementTitle: "Indomie: received +50", "Rice: counted 12".
String movementTitle(Map<String, dynamic> m) {
  final n = _int(m['change']) ?? _int(m['delta']) ?? _int(m['set_to']) ?? 0;
  if (m['reason'] == 'count') return '${m['item']}: counted ${_int(m['set_to']) ?? _int(m['after']) ?? n}';
  return '${m['item']}: ${(reasons[m['reason']] ?? '').toLowerCase()} ${n > 0 ? '+' : ''}$n';
}

/// stock.ts movementSub: when, the count it left, what from, why.
String movementSub(Map<String, dynamic> m) {
  final parts = [whenLabel((m['done_at'] ?? m['created_at']) as String? ?? '')];
  if (m['before'] != null && m['after'] != null) {
    parts.add('${m['before']} → ${m['after']}');
  }
  if ((_int(m['short']) ?? 0) > 0) parts.add('${m['short']} more asked for than there was');
  if ((m['reference'] as String? ?? '').isNotEmpty) parts.add(m['reason'] == 'order' ? 'order ${m['reference']}' : m['reference'] as String);
  if ((m['note'] as String? ?? '').isNotEmpty) parts.add(m['note'] as String);
  return parts.join(' · ');
}

const _saleHow = {'cash': 'Cash', 'transfer': 'Transfer', 'pos': 'Terminal', 'credit': 'On credit'};

/// stock.ts byHowLine: "Cash ₦12,000 · Transfer ₦5,000".
String byHowLine(Map<String, dynamic> day) {
  final by = (day['by_how'] as Map?)?.cast<String, dynamic>() ?? const {};
  return [
    for (final h in _saleHow.keys)
      if ((_int(by[h]) ?? 0) > 0) '${_saleHow[h]} ${naira(_int(by[h])!)}',
  ].join(' · ');
}

/// stock.ts saleTitle: what was sold.
String saleTitle(Map<String, dynamic> s) =>
    [for (final l in _maps(s['lines'])) '${stockQuantity(_int(l['quantity']) ?? 0, l['unit'] as String?)} ${l['item']}'].join(', ');

/// stock.ts saleSub: when, how paid, whether its money came, its reference.
String saleSub(Map<String, dynamic> s) {
  final parts = [whenLabel(s['sold_at'] as String? ?? ''), _saleHow[s['paid_how']] ?? '${s['paid_how']}'];
  if (s['status'] == 'waiting') {
    parts.add(s['paid_how'] == 'pos' ? 'waiting for the terminal’s report' : 'waiting for the transfer');
  } else if (s['status'] == 'matched') {
    parts.add('money came in');
  }
  parts.add(s['reference'] as String? ?? '');
  final short = ((s['short'] as List?) ?? const []).whereType<String>().toList();
  if (short.isNotEmpty) parts.add('more ${short.join(', ')} sold than counted');
  if ((s['note'] as String? ?? '').isNotEmpty) parts.add(s['note'] as String);
  return parts.join(' · ');
}

/// orders.ts feeKobo: "" is 0; null when not money.
int? feeKobo(String text) {
  final t = text.replaceAll(RegExp(r'[₦,\s]'), '');
  if (t.isEmpty) return 0;
  if (!RegExp(r'^\d+(\.\d{1,2})?$').hasMatch(t)) return null;
  return (double.parse(t) * 100).round();
}

/// orders.ts feeInput: kobo as typed, "8500", "8500.50".
String feeInput(int kobo) => kobo % 100 == 0 ? '${kobo ~/ 100}' : '${kobo ~/ 100}.${(kobo % 100).toString().padLeft(2, '0')}';

int? _whole(String v) => RegExp(r'^\d+$').hasMatch(v.trim()) ? int.parse(v.trim()) : null;

/// What the stock is worth at cost: each counted item's count by its cost
/// price (sizes counted on their own rows).
int stockValue(List<Map<String, dynamic>> items) =>
    items.fold(0, (n, i) => n + ((_int(i['stock']) ?? 0) > 0 && (_int(i['cost_kobo']) ?? 0) > 0 ? _int(i['stock'])! * _int(i['cost_kobo'])! : 0));

/// An amount short, for a tile: "₦1.24M", "₦86.5k", else as it is.
String compactNaira(int kobo) {
  final n = kobo / 100;
  if (n >= 1000000) return '₦${(n / 1000000).toStringAsFixed(n >= 10000000 ? 1 : 2).replaceAll(RegExp(r'\.?0+$'), '')}M';
  if (n >= 100000) return '₦${(n / 1000).toStringAsFixed(0)}k';
  return naira(kobo);
}

/// Today's sales by the hour they were sold (Lagos), 24 of them.
List<double> salesByHour(List<Map<String, dynamic>> sales) {
  final out = List<double>.filled(24, 0);
  for (final x in sales) {
    final d = DateTime.tryParse(x['sold_at'] as String? ?? '');
    if (d == null) continue;
    out[d.toUtc().add(const Duration(hours: 1)).hour] += (_int(x['amount_kobo']) ?? 0).toDouble();
  }
  return out;
}

class StockPage extends StatefulWidget {
  const StockPage({super.key, required this.auth, this.backLabel = 'Back'});
  final Auth auth;

  /// "Back to Orders": the head's link says where it goes back to.
  final String backLabel;

  @override
  State<StockPage> createState() => _StockPageState();
}

class _StockPageState extends State<StockPage> {
  Map<String, dynamic>? _view;

  /// The stock items (GET /v1/stock/stock-items), every one: the filter
  /// picks among them here, so the tiles keep the whole stock.
  ListStockItemsResponse? _list;
  String? _listError;
  StockShow _show = StockShow.all;
  Map<String, dynamic>? _sales;
  List<Map<String, dynamic>>? _moves;

  /// The latest shelf count, for its tile.
  Map<String, dynamic>? _lastCount;
  String? _error;
  String _item = '';
  String _reason = '';
  bool _busy = false;

  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _reload();
  }

  String _why(Object e) => e is ApiError ? e.message : 'You’re offline. This shows when you’re back online.';

  /// Each pull: the orders card is read again too.
  int _round = 0;

  Future<void> _reload() async {
    try {
      final v = await _t.send('GET', '/v1/stock') as Map<String, dynamic>;
      if (!mounted) return;
      setState(() => (_view = v, _error = null));
      if (v['keeps_stock'] == true) {
        try {
          final l = await widget.auth.api.listStockItems();
          if (mounted) setState(() => (_list = l, _listError = null));
        } on Object catch (e) {
          if (mounted) setState(() => _listError = _why(e));
        }
        final s = await _t.send('GET', '/v1/stock/sales');
        if (mounted) setState(() => _sales = (s as Map?)?.cast<String, dynamic>());
        try {
          final c = (await _t.send('GET', '/v1/stock/counts') as Map).cast<String, dynamic>();
          if (mounted) setState(() => _lastCount = _maps(c['counts']).firstOrNull);
        } on Object {
          // No tile; the counts card says why.
        }
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = _why(e));
    }
    await _loadMoves();
  }

  Future<void> _loadMoves() async {
    // A filter changed while the last was asked: only the newest is shown.
    final asked = ++_movesAsked;
    try {
      final r = await _t.send(
        'GET',
        '/v1/stock/movements',
        query: {'limit': '50', if (_item.isNotEmpty) 'item': _item, if (_reason.isNotEmpty) 'reason': _reason},
      );
      if (mounted && asked == _movesAsked) setState(() => _moves = _maps((r as Map)['movements']));
    } on Object catch (e) {
      if (mounted && asked == _movesAsked) setState(() => _error = _why(e));
    }
  }

  int _movesAsked = 0;

  Future<void> _setup() async {
    setState(() => _busy = true);
    try {
      await _t.send('POST', '/v1/stock/setup', body: const {});
      if (mounted) showTkToast(context, 'Stock is on');
      await _reload();
    } on Object catch (e) {
      if (mounted) setState(() => _error = _why(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _open(Widget Function(BuildContext context) form, String title) async {
    final saved = await showTkModal<bool>(context, title: title, builder: form);
    if (saved == true) await _reload();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final v = _view;
    final keeps = v?['keeps_stock'] == true;
    final items = _maps(v?['items']);
    final list = _list;
    final rows = list?.items ?? const <StockItemRow>[];
    final low = rows.where((r) => r.low).toList();
    final value = list?.valueKobo ?? stockValue(items);
    final sales = _sales;
    final look = TkLook.of(context);
    return TkPageScreen(
      title: 'Stock',
      titleSize: 30,
      backLabel: widget.backLabel,
      description: 'Your items and how many are left. Every change is kept, so a count can always be explained.',
      onRefresh: () async {
        setState(() => _round++);
        await _reload();
      },
      // Adding a stock item changes the business's tables: table.edit.
      action: keeps && widget.auth.can('table.edit') ? TkHeadButton(icon: 'b-plus', label: 'Add something to count', primary: true, onTap: _add) : null,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (v == null && _error == null) Text('Loading your stock…', style: tkHint(t)),
        if (v != null && !keeps)
          TkPageCard(
            title: 'Keep your stock here',
            children: [
              Text(
                '${v['catalogue'] != null ? 'Taskiem counts what you sell from your ${v['catalogue']} table, and what you only use, each change with its reason.' : 'Taskiem makes a products table for what you sell, and counts it and what you only use, each change with its reason. You need no page for it.'}'
                ' On WhatsApp, send add 20 yards of ankara fabric.',
                style: tkHint(t),
              ),
              const SizedBox(height: 12),
              Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(label: 'Keep my stock', primary: true, onPressed: _busy ? null : _setup),
              ),
            ],
          ),
        if (keeps) ...[
          if (sales != null) ..._salesToday(t, sales, items),
          // The stock items did not load: say so, never "nothing is low".
          if (_listError case final e?) TkCallout(leading: TkSquare('b-alert', color: look.red), title: 'What you count did not load', sub: e, color: look.red, subLines: 3),
          TkHighlights(
            tiles: [
              if (_listError == null)
              TkHighlight(
                label: 'Low',
                icon: 'b-box',
                color: look.orangeInk,
                trailing: low.isEmpty ? null : (low.length == 1 ? '1 item' : '${low.length} items'),
                value: low.isEmpty ? 'None' : 'Order ${low.length}',
                line: low.isEmpty ? 'nothing at its low level' : 'to reach your levels',
                semanticsLabel: low.isEmpty ? 'Low on stock: none' : 'Low on stock: ${low.length}. Order them',
                onTap: low.isEmpty ? null : () => _supplies(items),
              ),
              TkHighlight(
                label: 'At cost',
                icon: 'b-bars',
                color: look.tealInk,
                value: compactNaira(value),
                line: 'on your shelves',
                semanticsLabel: 'Your stock at cost: ${naira(value)}',
              ),
              if (_lastCount case final c?)
                TkHighlight(
                  wide: true,
                  label: 'Last count',
                  icon: 'b-check',
                  color: (_int(c['lost_kobo']) ?? 0) > 0 ? look.redInk : look.limeInk,
                  trailing: whenLabel(c['counted_at'] as String? ?? ''),
                  value: (_int(c['lost_kobo']) ?? 0) > 0 ? '${naira(_int(c['lost_kobo'])!)} missing' : 'Nothing missing',
                  line: '${_maps(c['lines']).length == 1 ? '1 item' : '${_maps(c['lines']).length} items'} counted${(c['reason'] as String? ?? '').isNotEmpty ? ' · ${c['reason']}' : ''}',
                  semanticsLabel: 'Last count: ${(_int(c['lost_kobo']) ?? 0) > 0 ? '${naira(_int(c['lost_kobo'])!)} missing' : 'nothing missing'}',
                ),
            ],
          ),
          if (_listError == null)
          TkSection(
            title: 'Running low',
            count: low.length,
            trailing: low.isEmpty ? null : TkSmallButton(label: 'Order them', semantics: 'Order what is low', onPressed: () => _supplies(items)),
            empty: rows.any((r) => r.lowLevel != null)
                ? 'Nothing is at or below its low level.'
                : 'Give what you count a low level, and it shows here when it runs low.',
            children: [for (final r in low) _stockRow(look, r, items)],
          ),
          PurchasesCard(key: const ValueKey('purchases'), auth: widget.auth, items: items, onChanged: _reload, round: _round),
          if (sales != null) ..._salesLists(t, sales, items),
          _stockItems(look, rows, items),
          _CountsCard(key: const ValueKey('counts'), auth: widget.auth, items: items, onChanged: _reload),
          TkPageCard(
            title: _item.isNotEmpty ? '${items.where((i) => i['id'] == _item).map((i) => i['name']).firstOrNull ?? 'The item'}: every change' : 'Every change',
            children: [
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  TkSmallButton(
                    label: _reason.isEmpty ? 'Every reason' : reasons[_reason]!,
                    semantics: 'Why: ${_reason.isEmpty ? 'Every reason' : reasons[_reason]}',
                    onPressed: () async {
                      final r = await showTkModalChoices(
                        context,
                        title: 'Why',
                        value: _reason,
                        choices: [('', 'Every reason'), for (final e in reasons.entries) (e.key, e.value)],
                      );
                      if (r == null) return;
                      setState(() => _reason = r);
                      _loadMoves();
                    },
                  ),
                  if (_item.isNotEmpty)
                    TkSmallButton(
                      label: 'Every item',
                      onPressed: () {
                        setState(() => _item = '');
                        _loadMoves();
                      },
                    ),
                ],
              ),
              const SizedBox(height: 8),
              if (_moves != null && _moves!.isEmpty) Text('No changes yet.', style: tkHint(t)),
              if (_moves != null && _moves!.isNotEmpty)
                LinkSection(
                  top: 0,
                  rows: [
                    for (final m in _moves!)
                      (
                        letter: m['item'] as String? ?? '',
                        icon: null,
                        tone: null,
                        title: movementTitle(m),
                        badge: reasons[m['reason']] ?? '',
                        badgeTone: Tone.muted,
                        sub: movementSub(m),
                        actions: null,
                      ),
                  ],
                ),
            ],
          ),
        ],
      ],
    );
  }

  Future<void> _supplies(List<Map<String, dynamic>> items) async {
    await Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => SuppliesPage(auth: widget.auth, items: items, backLabel: 'Back to Stock')));
    setState(() => _round++);
    await _reload();
  }

  /// Add something to count: a stock item, asking Do you sell it?
  Future<void> _add() async {
    final name = await showTkSheet<String>(context, (_) => AddToCountSheet(auth: widget.auth));
    if (name == null || !mounted) return;
    showTkToast(context, '$name added');
    await _reload();
  }

  /// What the forms of a stock item act on, as GET /v1/stock has it: the
  /// product it is sold as (its row id), or the material itself; its
  /// count, unit, cost, levels and supplier the stock item's.
  Map<String, dynamic> _asItem(StockItemRow r, List<Map<String, dynamic>> items) {
    final key = r.soldAs.isEmpty ? r.id : r.soldAs.first.productId;
    final had = items.where((i) => i['id'] == key).firstOrNull;
    return {
      ...?had,
      'id': key,
      'name': had?['name'] ?? r.name,
      'material': r.soldAs.isEmpty,
      'stock': r.count,
      'unit': r.unit,
      'cost_kobo': r.costKobo,
      'reorder_level': r.lowLevel,
      'target_level': r.targetLevel,
      'supplier': r.supplier,
      'low': r.low,
      'suggest': r.suggest,
    };
  }

  /// What you count (§4.5): All, Sold or Not sold; each stock item with
  /// what it is sold as, a product's sizes and colours under it.
  Widget _stockItems(TkLook look, List<StockItemRow> rows, List<Map<String, dynamic>> items) {
    final shown = rows.where((r) => shows(_show, r)).toList();
    final none = switch (_show) {
      StockShow.all => 'Nothing yet. Add what you sell, or what you only use, and how many you have.',
      StockShow.sold => 'Nothing you count is sold yet.',
      StockShow.notSold => 'Nothing here: fabric or packaging you only use shows here.',
    };
    return Column(
      key: const ValueKey('items'),
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // The head, the filter under it, then the rows (as Orders' lists).
        const TkSection(title: 'What you count'),
        TkChips<StockShow>(
          label: 'Show',
          padding: const EdgeInsets.only(bottom: 10),
          chips: const [(StockShow.all, 'All'), (StockShow.sold, 'Sold'), (StockShow.notSold, 'Not sold')],
          on: _show,
          onPick: (s) => setState(() => _show = s),
        ),
        TkSection(
          empty: _listError ?? (_list == null ? 'Loading…' : none),
          children: [
            for (final x in stockRows(shown))
              if (x.item case final r?)
                _stockRow(look, r, items)
              else ...[
                _groupRow(look, x.group!, x.of, items),
                for (final r in x.of) _stockRow(look, r, items, size: true),
              ],
          ],
        ),
      ],
    );
  }

  /// A stock item: its letters, what it is sold as, its cost, levels and
  /// supplier, how many there are (a bar of it against its level), and
  /// Low when it is; pressed, what can be done to it.
  Widget _stockRow(TkLook look, StockItemRow r, List<Map<String, dynamic>> items, {bool size = false}) {
    final words = size && r.soldAs.isNotEmpty ? variantWords(r.soldAs.first.variant) : '';
    final facts = stockItemFacts(r);
    return TkListRow(
      title: words.isNotEmpty ? words : r.name,
      letters: size ? null : r.name,
      icon: size ? 'tag' : null,
      sub: size ? ['Of ${r.groupName}', if (r.soldAs.isEmpty) 'Not sold', if (facts.isNotEmpty) facts].join(' · ') : stockItemSub(r),
      amount: stockQuantity(r.count, r.unit),
      status: r.low ? 'Low' : null,
      statusAccent: (fill: look.orange, ink: look.orangeInk),
      level: stockLevel(r),
      levelColor: r.low ? look.orange : look.lime,
      onTap: () => _itemActions(_asItem(r, items), sized: size),
    );
  }

  /// A product sold in sizes and colours, over their stock items: how
  /// many, how many low, and their count together.
  Widget _groupRow(TkLook look, ({String id, String name}) g, List<StockItemRow> of, List<Map<String, dynamic>> items) {
    final low = of.where((r) => r.low).length;
    final units = of.map((r) => r.unit).toSet();
    final total = of.fold(0, (n, r) => n + r.count);
    final had = items.where((i) => i['id'] == g.id).firstOrNull;
    return TkListRow(
      title: g.name,
      letters: g.name,
      sub: 'Sold in ${sizesWords(of.length)}${low > 0 ? ' · $low low' : ''}',
      amount: units.length == 1 ? stockQuantity(total, units.first) : '$total in stock',
      status: low > 0 ? 'Low' : null,
      statusAccent: (fill: look.orange, ink: look.orangeInk),
      onTap: () => _itemActions({...?had, 'id': g.id, 'name': had?['name'] ?? g.name}, sizes: true),
    );
  }

  /// What can be done to an item, in a modal: record stock, change it,
  /// its history.
  Future<void> _itemActions(Map<String, dynamic> it, {bool sized = false, bool sizes = false}) async {
    final name = it['name'] as String? ?? '';
    final pick = await showTkModalChoices(
      context,
      title: name,
      choices: [
        if (!sizes) ('stock', 'Record stock'),
        if (!sized) ('change', 'Change'),
        ('history', 'History'),
      ],
    );
    if (!mounted) return;
    switch (pick) {
      case 'stock':
        await _open((c) => _MoveForm(auth: widget.auth, item: it), 'Record stock of $name');
      case 'change':
        await _open((c) => _ItemForm(auth: widget.auth, item: it), 'Change $name');
      case 'history':
        setState(() => _item = it['id'] as String);
        await _loadMoves();
    }
  }

  /// StorefrontStock.tsx SalesToday: the total in a pine hero with Record
  /// a sale, the terminal's sales with no items, then each sale.
  List<Widget> _salesToday(TkTokens t, Map<String, dynamic> day, List<Map<String, dynamic>> items) {
    final look = TkLook.of(context);
    final by = byHowLine(day);
    final count = _int(day['count']) ?? 0;
    final unsold = _maps(day['unsold']);
    final sales = _maps(day['sales']);
    final canSell = items.isNotEmpty;
    final waiting = _int(day['waiting']) ?? 0;
    final perHour = salesByHour(sales);
    final first = perHour.indexWhere((x) => x > 0), last = perHour.lastIndexWhere((x) => x > 0);
    final from = first < 0 ? 8 : math.min(8, first), to = math.max(19, last);
    final unsoldKobo = unsold.fold(0, (n, u) => n + (_int(u['amount_kobo']) ?? 0));
    return [
      TkTopCard(
        semanticsLabel: 'Today’s sales',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TkEyebrow('Today’s sales', color: look.limeInk, trailing: count == 1 ? '1 sale' : '$count sales'),
            const SizedBox(height: 6),
            FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(_int(day['total_kobo']) ?? 0, style: tkFigure(context, size: 40))),
            Text(
              count == 0
                  ? 'No sales recorded today.'
                  : [if (by.isNotEmpty) by, if (waiting > 0) '$waiting waiting for money'].join(' · '),
              style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
            ),
            if (first >= 0) ...[
              const SizedBox(height: 12),
              TkMiniBars(values: perHour.sublist(from, to + 1), color: look.lime, picked: perHour.indexOf(perHour.reduce(math.max)) - from, height: 44),
            ],
            const SizedBox(height: 14),
            Row(
              children: [
                Expanded(
                  child: TkSmallButton(
                    label: 'Record a sale',
                    icon: 'b-cart',
                    primary: true,
                    onPressed: canSell ? () => _open((c) => _SaleForm(auth: widget.auth, items: items), 'Record a sale') : null,
                  ),
                ),
              ],
            ),
            if (unsold.isNotEmpty) ...[
              const SizedBox(height: 14),
              TkCallout(
                leading: TkSquare('b-cash', color: look.orange),
                title: unsold.length == 1 ? '1 terminal sale to explain' : '${unsold.length} terminal sales to explain',
                sub: '${naira(unsoldKobo)} · say what was sold',
                color: look.orange,
                // Say: the oldest first (as drawn).
                button: 'Say',
                semantics: 'Say what the terminal sales were, the oldest first',
                onPressed: canSell
                    ? () => _open(
                          (c) => _SaleForm(auth: widget.auth, items: items, entry: unsold.first),
                          'What was sold for ${naira(_int(unsold.first['amount_kobo']) ?? 0)}',
                        )
                    : null,
              ),
            ],
          ],
        ),
      ),
    ];
  }

  /// The terminal's sales nobody explained, each with Say; then today's
  /// sales, newest first.
  List<Widget> _salesLists(TkTokens t, Map<String, dynamic> day, List<Map<String, dynamic>> items) {
    final look = TkLook.of(context);
    final unsold = _maps(day['unsold']);
    final sales = _maps(day['sales']);
    final canSell = items.isNotEmpty;
    return [
      if (unsold.isNotEmpty)
        TkSection(
          title: 'Terminal sales with no items',
          inset: 56,
          children: [
            for (final u in unsold)
              Row(
                children: [
                  Expanded(
                    child: TkListRow(
                      title: naira(_int(u['amount_kobo']) ?? 0),
                      sub: 'On the terminal · ${whenLabel(u['at'] as String? ?? '')}',
                      icon: 'b-cash',
                      square: look.orange,
                    ),
                  ),
                  Padding(
                    padding: const EdgeInsets.only(right: 12),
                    child: TkSmallButton(
                      label: 'Say',
                      semantics: 'Say what was sold for ${naira(_int(u['amount_kobo']) ?? 0)}',
                      primary: true,
                      onPressed: canSell
                          ? () => _open((c) => _SaleForm(auth: widget.auth, items: items, entry: u), 'What was sold for ${naira(_int(u['amount_kobo']) ?? 0)}')
                          : null,
                    ),
                  ),
                ],
              ),
          ],
        ),
      if (sales.isNotEmpty)
        TkSection(
          title: 'Sold today',
          children: [
            for (final x in sales)
              TkListRow(
                title: saleTitle(x),
                letters: _maps(x['lines']).firstOrNull?['item'] as String? ?? '₦',
                sub: saleSub(x),
                amount: naira(_int(x['amount_kobo']) ?? 0),
                status: x['status'] == 'waiting' ? 'Waiting' : null,
                statusAccent: (fill: look.orange, ink: look.orangeInk),
              ),
          ],
        ),
    ];
  }
}

/// A field as the web's `Field`: its label, the box, a hint.
Widget pageField(String label, TextEditingController c, {String? hint, String? placeholder, bool money = false, bool number = false, int? max}) => TkKitField(
  label: label,
  hint: hint,
  child: TkTextBox(controller: c, hint: placeholder, money: money, digits: number ? 7 : null, max: max),
);

/// StorefrontStock.tsx ItemModal: a stock item's name, unit, cost, levels
/// and supplier changed, and the selling price of the product it is sold
/// as (a material has none). Adding is Add something to count.
class _ItemForm extends StatefulWidget {
  const _ItemForm({required this.auth, required this.item});
  final Auth auth;
  final Map<String, dynamic> item;

  @override
  State<_ItemForm> createState() => _ItemFormState();
}

class _ItemFormState extends State<_ItemForm> {
  Map<String, dynamic> get _it => widget.item;
  bool get _material => _it['material'] == true;
  late final _name = _c(_it['name'] as String? ?? '');
  late final _unit = _c(_it['unit'] as String? ?? '');
  late final _cost = _c(_int(_it['cost_kobo']) == null ? '' : feeInput(_int(_it['cost_kobo'])!));
  late final _price = _c(_int(_it['price_kobo']) == null ? '' : feeInput(_int(_it['price_kobo'])!));
  late final _reorder = _c(_int(_it['reorder_level'])?.toString() ?? '');
  late final _target = _c(_int(_it['target_level'])?.toString() ?? '');
  late final _supplier = _c(_it['supplier'] as String? ?? '');
  bool _busy = false;
  String? _error;

  TextEditingController _c(String text) => TextEditingController(text: text)..addListener(() => setState(() {}));

  @override
  void dispose() {
    for (final c in [_name, _unit, _cost, _price, _reorder, _target, _supplier]) {
      c.dispose();
    }
    super.dispose();
  }

  /// stock.ts itemProblem.
  String? get _problem {
    if (_name.text.trim().isEmpty) return 'Give the item a name.';
    if (_name.text.trim().length > 120) return 'A name is up to 120 characters.';
    if (_unit.text.trim().length > 30) return 'A unit is one word, like bag or carton.';
    if (_cost.text.trim().isNotEmpty && feeKobo(_cost.text) == null) return 'The cost price is an amount, like 8500.';
    if (_price.text.trim().isNotEmpty && feeKobo(_price.text) == null) return 'The selling price is an amount, like 8500.';
    if (_reorder.text.trim().isNotEmpty && _whole(_reorder.text) == null) return 'The low level is a whole number.';
    if (_target.text.trim().isNotEmpty && _whole(_target.text) == null) return 'The level to order up to is a whole number.';
    if (_reorder.text.trim().isNotEmpty && _target.text.trim().isNotEmpty && _whole(_target.text)! <= _whole(_reorder.text)!) {
      return 'Order up to more than the low level.';
    }
    return null;
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    final it = _it;
    try {
      // stock.ts changeRequest: what changed only.
      String was(String k) => switch (k) {
        'cost_kobo' || 'price_kobo' => _int(it[k]) == null ? '' : feeInput(_int(it[k])!),
        'reorder_level' || 'target_level' => _int(it[k])?.toString() ?? '',
        _ => it[k] as String? ?? '',
      };
      final out = <String, Object?>{
        if (_name.text.trim() != it['name']) 'name': _name.text.trim(),
        if (_unit.text.trim() != was('unit')) 'unit': _unit.text.trim(),
        if (_supplier.text.trim() != was('supplier')) 'supplier': _supplier.text.trim(),
        if (_cost.text.trim() != was('cost_kobo') && _cost.text.trim().isNotEmpty) 'cost_kobo': feeKobo(_cost.text),
        if (!_material && _price.text.trim() != was('price_kobo') && _price.text.trim().isNotEmpty) 'price_kobo': feeKobo(_price.text),
        if (_reorder.text.trim() != was('reorder_level') && _reorder.text.trim().isNotEmpty) 'reorder_level': _whole(_reorder.text),
        if (_target.text.trim() != was('target_level') && _target.text.trim().isNotEmpty) 'target_level': _whole(_target.text),
      };
      await widget.auth.transport.send('PATCH', '/v1/stock/items/${it['id']}', body: out);
      if (!mounted) return;
      showTkToast(context, 'Saved');
      Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    return TkModalForm(
      saveWords: 'Save',
      busy: _busy,
      problem: _name.text.isEmpty ? null : _problem,
      error: _error,
      save: _problem == null ? _save : null,
      children: [
        pageField('Name', _name, max: 120),
        pageField('Unit', _unit, max: 30, hint: 'One word: bag, carton, yard. Leave it empty to count pieces.', placeholder: 'carton'),
        pageField('Cost price (₦)', _cost, hint: 'What you pay for one, for the value of your stock and what you lose.', placeholder: '8500', money: true),
        if (!_material)
          pageField(
            'Selling price (₦)',
            _price,
            hint: 'Shown on your page once it is published. Without one, the item is not shown.',
            placeholder: '9500',
            money: true,
          ),
        pageField('Low at', _reorder, hint: 'When the count is this or less, it is low and you are told each morning.', placeholder: '10', number: true),
        pageField('Order up to', _target, hint: 'What to order a low item back up to.', placeholder: '50', number: true),
        pageField('Supplier', _supplier, placeholder: 'Dangote Depot', max: 120),
      ],
    );
  }
}

/// StorefrontStock.tsx MoveModal: goods received, an adjustment with a
/// reason, or a count.
class _MoveForm extends StatefulWidget {
  const _MoveForm({required this.auth, required this.item});
  final Auth auth;
  final Map<String, dynamic> item;

  @override
  State<_MoveForm> createState() => _MoveFormState();
}

class _MoveFormState extends State<_MoveForm> {
  String _kind = 'received';
  bool _off = true;
  final _amount = TextEditingController();
  final _note = TextEditingController();
  late final _key = uuid4();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _amount.addListener(() => setState(() {}));
    _note.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _amount.dispose();
    _note.dispose();
    super.dispose();
  }

  /// stock.ts moveProblem.
  String? get _problem {
    final n = _whole(_amount.text);
    if (n == null) return _kind == 'count' ? 'Enter how many you counted, like 12.' : 'Enter how many, like 5.';
    if (_kind != 'count' && n == 0) return 'Enter how many, more than 0.';
    if (n > 1000000) return 'That is more than Taskiem keeps for one item.';
    if (_kind == 'adjustment' && _note.text.trim().isEmpty) return 'Say why, like spoilt or found.';
    if (_note.text.length > 200) return 'The reason is up to 200 characters.';
    return null;
  }

  /// stock.ts movePreview: "48 → 45 cartons".
  String? get _preview {
    final n = _whole(_amount.text);
    final now = _int(widget.item['stock']);
    if (n == null || now == null) return null;
    final to = _kind == 'count' ? n : (now + (_kind == 'adjustment' && _off ? -n : n)).clamp(0, 1 << 31);
    return '$now → ${stockQuantity(to, widget.item['unit'] as String?)}';
  }

  Future<void> _save() async {
    final n = _whole(_amount.text)!;
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send(
        'POST',
        '/v1/stock/movements',
        body: {
          'item_id': widget.item['id'],
          'reason': _kind,
          'note': _note.text.trim(),
          if (_kind == 'count') 'count': n else 'change': _kind == 'adjustment' && _off ? -n : n,
        },
        headers: {'Idempotency-Key': '$_key-$_kind'},
      );
      if (!mounted) return;
      showTkToast(context, 'Recorded');
      Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  Widget _radio(String label, bool on, VoidCallback onTap) => TkChoiceRow(
    control: TkRadio(checked: on),
    label: Text(label, style: tkText(size: 15, color: TkTheme.tokens(context).text)),
    onTap: onTap,
  );

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = _problem;
    return TkModalForm(
      saveWords: 'Record',
      busy: _busy,
      problem: _amount.text.isEmpty ? null : problem,
      error: _error,
      save: problem == null ? _save : null,
      children: [
        Text('Now: ${stockLine(widget.item)}.', style: tkHint(t)),
        Semantics(
          container: true,
          label: 'What happened',
          explicitChildNodes: true,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text('What happened', style: tkText(size: 12.5, weight: 500, color: t.text)),
              const SizedBox(height: 6),
              _radio('Goods came in', _kind == 'received', () => setState(() => _kind = 'received')),
              _radio('Some were spoilt, lost or found', _kind == 'adjustment', () => setState(() => _kind = 'adjustment')),
              _radio('I counted what is on the shelf', _kind == 'count', () => setState(() => _kind = 'count')),
            ],
          ),
        ),
        if (_kind == 'adjustment')
          Semantics(
            container: true,
            label: 'Take off or add',
            explicitChildNodes: true,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('Take off or add', style: tkText(size: 12.5, weight: 500, color: t.text)),
                const SizedBox(height: 6),
                _radio('Take them off', _off, () => setState(() => _off = true)),
                _radio('Add them', !_off, () => setState(() => _off = false)),
              ],
            ),
          ),
        pageField(_kind == 'count' ? 'How many you counted' : 'How many', _amount, placeholder: _kind == 'count' ? '12' : '5', number: true),
        pageField(_kind == 'adjustment' ? 'Why' : 'Note', _note, hint: _kind == 'adjustment' ? 'Spoilt, lost, found, a gift.' : null, max: 200),
        if (_preview != null && problem == null) Text('The count goes from $_preview.', style: tkHint(t)),
      ],
    );
  }
}

/// StorefrontStock.tsx SaleModal: the items, how many, how it was paid;
/// or what a terminal's sale was.
class _SaleForm extends StatefulWidget {
  const _SaleForm({required this.auth, required this.items, this.entry});
  final Auth auth;
  final List<Map<String, dynamic>> items;
  final Map<String, dynamic>? entry;

  @override
  State<_SaleForm> createState() => _SaleFormState();
}

class _Line {
  _Line(VoidCallback changed) {
    for (final c in [quantity, price]) {
      c.addListener(changed);
    }
  }
  String item = '';
  final quantity = TextEditingController(text: '1');
  final price = TextEditingController();
}

class _SaleFormState extends State<_SaleForm> {
  late final List<_Line> _lines = [_Line(_changed)];
  late String _how = widget.entry != null ? 'pos' : 'cash';
  final _amount = TextEditingController();
  final _note = TextEditingController();
  late final _key = uuid4();
  bool _busy = false;
  String? _error;

  void _changed() => setState(() {});

  @override
  void initState() {
    super.initState();
    _amount.addListener(_changed);
    _note.addListener(_changed);
  }

  @override
  void dispose() {
    for (final l in _lines) {
      l.quantity.dispose();
      l.price.dispose();
    }
    _amount.dispose();
    _note.dispose();
    super.dispose();
  }

  Map<String, dynamic>? _itemOf(_Line l) => widget.items.where((i) => i['id'] == l.item).firstOrNull;

  int? _linePrice(_Line l) => l.price.text.trim().isNotEmpty ? feeKobo(l.price.text) : _int(_itemOf(l)?['price_kobo']);

  /// stock.ts saleTotal.
  int? get _total {
    var sum = 0;
    for (final l in _lines) {
      final p = _linePrice(l);
      final n = _whole(l.quantity.text);
      if (p == null || n == null) return null;
      sum += p * n;
    }
    return sum;
  }

  /// stock.ts saleProblem.
  String? get _problem {
    for (final l in _lines) {
      final it = _itemOf(l);
      if (it == null) return 'Choose the item sold on each line.';
      final n = _whole(l.quantity.text);
      if (n == null || n <= 0 || n > 1000000) return 'How many ${it['name']} is a whole number, like 2.';
      if (l.price.text.trim().isNotEmpty && feeKobo(l.price.text) == null) return 'The price of ${it['name']} is an amount, like 4500.';
    }
    if (_amount.text.trim().isNotEmpty) {
      final a = feeKobo(_amount.text);
      if (a == null || a <= 0) return 'The amount taken is an amount, like 45000.';
    } else {
      final missing = _lines.where((l) => l.price.text.trim().isEmpty).map(_itemOf).where((it) => it != null && it['price_kobo'] == null).firstOrNull;
      if (missing != null) return '${missing['name']} has no price: give its price or the amount taken.';
      if (_total == 0) return 'The sale comes to ₦0.';
    }
    if (_note.text.length > 200) return 'The note is up to 200 characters.';
    return null;
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    final entry = widget.entry;
    try {
      // stock.ts saleRequest.
      await widget.auth.transport.send(
        'POST',
        '/v1/stock/sales',
        body: {
          'lines': [
            for (final l in _lines)
              {'item_id': l.item, 'quantity': _whole(l.quantity.text), if (l.price.text.trim().isNotEmpty) 'price_kobo': feeKobo(l.price.text)},
          ],
          'paid_how': entry != null ? 'pos' : _how,
          if (entry != null) ...{
            'amount_kobo': entry['amount_kobo'],
            'entry_id': entry['entry_id'],
          } else if (_amount.text.trim().isNotEmpty)
            'amount_kobo': feeKobo(_amount.text),
          'note': _note.text.trim(),
        },
        headers: {'Idempotency-Key': _key},
      );
      if (!mounted) return;
      showTkToast(context, 'Sale recorded');
      Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = _problem;
    final total = _total;
    final touched = _lines.any((l) => l.item.isNotEmpty);
    return TkModalForm(
      saveWords: 'Record the sale',
      busy: _busy,
      problem: touched ? problem : null,
      error: _error,
      save: problem == null ? _save : null,
      children: [
        for (var i = 0; i < _lines.length; i++)
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(_lines.length > 1 ? 'Item ${i + 1}' : 'Item', style: tkText(size: 12.5, weight: 500, color: t.text)),
              const SizedBox(height: 6),
              TkKitField(
                field: false,
                label: 'What was sold',
                child: TkSmallButton(
                  label: _itemOf(_lines[i]) == null ? 'Choose an item' : '${_itemOf(_lines[i])!['name']} (${stockLine(_itemOf(_lines[i])!)})',
                  semantics: 'What was sold: ${_itemOf(_lines[i])?['name'] ?? 'Choose an item'}',
                  onPressed: () async {
                    final id = await showTkModalChoices(
                      context,
                      title: 'What was sold',
                      value: _lines[i].item,
                      choices: [for (final x in widget.items) (x['id'] as String, '${x['name']} (${stockLine(x)})')],
                    );
                    if (id != null) setState(() => _lines[i].item = id);
                  },
                ),
              ),
              const SizedBox(height: 10),
              pageField('How many', _lines[i].quantity, number: true),
              const SizedBox(height: 10),
              pageField(
                'Price for one (₦)',
                _lines[i].price,
                money: true,
                hint: _int(_itemOf(_lines[i])?['price_kobo']) != null ? 'Leave it empty for ${naira(_int(_itemOf(_lines[i])!['price_kobo'])!)}.' : null,
              ),
              if (_lines.length > 1)
                Align(
                  alignment: Alignment.centerLeft,
                  child: TkSmallButton(
                    label: 'Take this item off',
                    semantics: 'Take this item off ${i + 1}',
                    onPressed: () => setState(() => _lines.removeAt(i)),
                  ),
                ),
            ],
          ),
        Align(
          alignment: Alignment.centerLeft,
          child: TkSmallButton(label: 'Add another item', onPressed: () => setState(() => _lines.add(_Line(_changed)))),
        ),
        if (widget.entry == null) ...[
          Semantics(
            container: true,
            label: 'How it was paid',
            explicitChildNodes: true,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('How it was paid', style: tkText(size: 12.5, weight: 500, color: t.text)),
                const SizedBox(height: 6),
                for (final h in const [('cash', 'Cash'), ('transfer', 'Transfer'), ('pos', 'Terminal')])
                  TkChoiceRow(
                    control: TkRadio(checked: _how == h.$1),
                    label: Text(h.$2, style: tkText(size: 15, color: t.text)),
                    onTap: () => setState(() => _how = h.$1),
                  ),
              ],
            ),
          ),
          pageField(
            'Amount taken (₦)',
            _amount,
            money: true,
            hint: total != null ? 'Leave it empty for ${naira(total)}, the items’ prices.' : 'Needed when an item has no price.',
          ),
        ],
        pageField('Note', _note, max: 200),
        if (widget.entry == null && _how != 'cash')
          Text('The sale waits for its money, and is matched when the ${_how == 'pos' ? 'terminal reports it' : 'transfer comes in'}.', style: tkHint(t)),
      ],
    );
  }
}

/// stock.ts shrinkLine: this month's in, sold and lost.
String shrinkLine(Map<String, dynamic> s) {
  final received = _int(s['received']) ?? 0, sold = _int(s['sold']) ?? 0, lost = _int(s['lost']) ?? 0;
  if (received == 0 && sold == 0 && lost == 0) return 'Nothing moved yet this month.';
  final lostWords = lost == 0 ? 'nothing lost' : '$lost lost${(_int(s['lost_kobo']) ?? 0) > 0 ? ' (${naira(_int(s['lost_kobo'])!)} at cost)' : ''}';
  return '$received in, $sold sold, $lostWords';
}

/// stock.ts countDiff: how a counted item differs from what Taskiem has.
String? countDiff(Map<String, dynamic> it, String v) {
  final n = _whole(v);
  if (n == null) return null;
  final unit = it['unit'] as String?;
  final have = _int(it['stock']);
  if (have == null) return '${stockQuantity(n, unit)}, first count';
  if (n == have) return 'as Taskiem has it';
  final d = n - have;
  return '$have → ${stockQuantity(n, unit)} (${d.abs()} ${d < 0 ? 'fewer' : 'more'})';
}

/// StockCounts.tsx CountsCard: this month's in, sold and lost; Count the
/// shelf; the latest counts; and the loss a count may find before it is a
/// leak.
class _CountsCard extends StatefulWidget {
  const _CountsCard({super.key, required this.auth, required this.items, required this.onChanged});
  final Auth auth;
  final List<Map<String, dynamic>> items;
  final Future<void> Function() onChanged;

  @override
  State<_CountsCard> createState() => _CountsCardState();
}

class _CountsCardState extends State<_CountsCard> {
  Map<String, dynamic>? _shrink;
  List<Map<String, dynamic>>? _counts;
  String? _error;
  String? _shrinkError;
  String? _limitError;
  int? _limit;
  bool _hasLimit = false;
  final _limitBox = TextEditingController();
  bool _busy = false;

  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _limitBox.addListener(() => setState(() {}));
    _load();
    _loadLimit();
  }

  @override
  void dispose() {
    _limitBox.dispose();
    super.dispose();
  }

  String _why(Object e) => e is ApiError ? e.message : 'You’re offline. This shows when you’re back online.';

  /// The month's figures and the latest counts, each read on its own: one
  /// failing leaves the other shown (the web's two loads).
  Future<void> _load() async {
    await Future.wait([
      () async {
        try {
          final s = (await _t.send('GET', '/v1/stock/shrinkage') as Map).cast<String, dynamic>();
          if (mounted) setState(() => (_shrink = s, _shrinkError = null));
        } on Object catch (e) {
          if (mounted) setState(() => _shrinkError = _why(e));
        }
      }(),
      () async {
        try {
          final c = (await _t.send('GET', '/v1/stock/counts') as Map).cast<String, dynamic>();
          if (mounted) setState(() => (_counts = _maps(c['counts']), _error = null));
        } on Object catch (e) {
          if (mounted) setState(() => _error = _why(e));
        }
      }(),
    ]);
  }

  Future<void> _loadLimit() async {
    try {
      final r = (await _t.send('GET', '/v1/stock/settings') as Map).cast<String, dynamic>();
      final l = _int((r['settings'] as Map?)?['count_limit_kobo']);
      if (!mounted) return;
      setState(() {
        _hasLimit = true;
        _limit = l;
        _limitBox.text = l == null ? '' : feeInput(l);
      });
    } on Object {
      // No settings: the limit is not offered.
    }
  }

  Future<void> _count() async {
    final saved = await showTkModal<bool>(context, title: 'Count the shelf', builder: (_) => _CountForm(auth: widget.auth, items: widget.items));
    if (saved == true && mounted) {
      showTkToast(context, 'Count recorded');
      await _load();
      await widget.onChanged();
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final s = _shrink;
    final worst = s == null ? null : _maps(s['items']).where((i) => (_int(i['lost']) ?? 0) > 0).firstOrNull;
    final counts = _counts ?? const [];
    final typed = _limitBox.text.trim();
    final kobo = typed.isEmpty ? null : feeKobo(typed);
    final bad = typed.isNotEmpty && kobo == null;
    return TkPageCard(
      title: 'Counts and losses',
      trailing: TkSmallButton(label: 'Count the shelf', primary: true, onPressed: widget.items.isEmpty ? null : _count),
      children: [
        if (_shrinkError != null) TkErrorBox(_shrinkError!),
        if (s != null)
          Text(
            'This month: ${shrinkLine(s).replaceAll(RegExp(r'\.$'), '')}.${worst != null ? ' Lost most: ${worst['item']} (${worst['lost']}${(_int(worst['lost_kobo']) ?? 0) > 0 ? ', ${naira(_int(worst['lost_kobo'])!)}' : ''}).' : ''}',
            style: tkHint(t),
          ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        if (counts.isNotEmpty)
          LinkSection(
            top: 12,
            title: 'Latest counts',
            rows: [
              for (final c in counts.take(5))
                (
                  letter: '#',
                  icon: null,
                  tone: null,
                  title: '${c['reference']} · ${_maps(c['lines']).length == 1 ? '1 item' : '${_maps(c['lines']).length} items'}${c['full'] == true ? ', every item' : ''}',
                  badge: (_int(c['lost_kobo']) ?? 0) > 0 ? '${naira(_int(c['lost_kobo'])!)} missing' : (_int(c['changed']) == 1 ? '1 change' : '${_int(c['changed']) ?? 0} changes'),
                  badgeTone: (_int(c['lost_kobo']) ?? 0) > 0 ? Tone.warn : Tone.muted,
                  sub: '${whenLabel(c['counted_at'] as String? ?? '')}${(c['reason'] as String? ?? '').isNotEmpty ? ' · ${c['reason']}' : ''}',
                  actions: null,
                ),
            ],
          ),
        if (_hasLimit) ...[
          const SizedBox(height: 14),
          TkKitField(
            label: 'A count missing more than this is a leak (₦)',
            hint: bad ? null : 'Valued at cost. Leave it empty for ₦5,000. Items with no cost price are always shown.',
            error: bad ? 'Enter an amount, like 5000.' : null,
            child: TkTextBox(controller: _limitBox, money: true, hint: '5000'),
          ),
          const SizedBox(height: 8),
          Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(
              label: 'Save the limit',
              onPressed: _busy || bad || typed == (_limit == null ? '' : feeInput(_limit!))
                  ? null
                  : () async {
                      setState(() => (_busy = true, _limitError = null));
                      try {
                        await _t.send('PUT', '/v1/stock/settings', body: {'count_limit_kobo': kobo});
                        if (mounted) showTkToast(this.context, 'Saved');
                        await _loadLimit();
                      } on Object catch (e) {
                        if (mounted) setState(() => _limitError = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.');
                      }
                      if (mounted) setState(() => _busy = false);
                    },
            ),
          ),
          // Its error by its button, not at the top of the card.
          if (_limitError != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_limitError!)),
        ],
      ],
    );
  }
}

/// CountModal: how many of each item are on the shelf, with why they
/// differ. (The web also reads a CSV; on a phone the shelf is counted by
/// hand, or by scanning each item's barcode: device fields D2.)
class _CountForm extends StatefulWidget {
  const _CountForm({required this.auth, required this.items});
  final Auth auth;
  final List<Map<String, dynamic>> items;

  @override
  State<_CountForm> createState() => _CountFormState();
}

class _CountFormState extends State<_CountForm> {
  late final _boxes = {for (final it in widget.items) it['id'] as String: TextEditingController()..addListener(_changed)};
  final _reason = TextEditingController();
  late final _key = uuid4();
  bool _busy = false;
  String? _error;

  /// Codes scanned that no item has yet, and how many times each.
  final _unknown = <String, int>{};

  /// An item's barcode being saved.
  String? _naming;

  void _changed() => setState(() {});

  /// The item whose barcode this is.
  Map<String, dynamic>? _codeItem(String code) => widget.items.where((it) => (it['code'] as String?)?.trim() == code.trim()).firstOrNull;

  void _addTo(String id, int n) {
    final box = _boxes[id];
    if (box == null) return;
    box.text = '${(_whole(box.text) ?? 0) + n}';
  }

  /// Scan to count: each item's barcode, once for each one on the shelf;
  /// every scan adds one to its count.
  Future<void> _scan() async {
    final got = await codeScanner.scanMany(
      context,
      title: 'Scan to count',
      formats: const ['ean_13', 'ean_8', 'upc_a', 'upc_e', 'code_128', 'qr'],
      name: (c) => _codeItem(c.text)?['name'] as String?,
      backLabel: 'Back to Count the shelf',
    );
    if (got == null || !mounted) return;
    setState(() {
      for (final c in got) {
        final it = _codeItem(c.text);
        if (it != null) {
          _addTo(it['id'] as String, 1);
        } else {
          _unknown[c.text] = (_unknown[c.text] ?? 0) + 1;
        }
      }
    });
  }

  /// A code no item has: the person says which item it is; it is kept on
  /// the item (its barcode), and its scans counted.
  Future<void> _name(String code) async {
    final id = await pickOneOf(
      context,
      title: 'Which item is $code?',
      searchHint: 'Find an item',
      choices: [for (final it in widget.items) if ((it['code'] as String? ?? '').isEmpty) (it['id'] as String, it['name'] as String? ?? '', stockLine(it))],
    );
    if (id == null || !mounted) return;
    setState(() => _naming = code);
    try {
      await widget.auth.transport.send('PATCH', '/v1/stock/items/${Uri.encodeComponent(id)}', body: {'code': code});
      final it = widget.items.firstWhere((x) => x['id'] == id);
      it['code'] = code;
      if (!mounted) return;
      setState(() {
        _addTo(id, _unknown.remove(code) ?? 0);
        _error = null;
      });
    } on Object catch (e) {
      if (mounted) setState(() => _error = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.');
    } finally {
      if (mounted) setState(() => _naming = null);
    }
  }

  @override
  void dispose() {
    for (final c in _boxes.values) {
      c.dispose();
    }
    _reason.dispose();
    super.dispose();
  }

  /// stock.ts countProblem.
  String? get _problem {
    final filled = [for (final c in _boxes.values) if (c.text.trim().isNotEmpty) c.text];
    if (filled.isEmpty) return 'Enter how many of at least one item.';
    if (filled.any((v) => _whole(v) == null)) return 'How many is a whole number, like 12.';
    return null;
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send(
        'POST',
        '/v1/stock/counts',
        body: {
          'lines': [
            for (final e in _boxes.entries)
              if (e.value.text.trim().isNotEmpty) {'item_id': e.key, 'count': _whole(e.value.text)},
          ],
          'reason': _reason.text.trim(),
        },
        headers: {'Idempotency-Key': _key},
      );
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final filled = _boxes.values.any((c) => c.text.trim().isNotEmpty);
    return TkModalForm(
      saveWords: _busy ? 'Recording…' : 'Record the count',
      busy: _busy,
      problem: filled ? _problem : null,
      error: _error,
      save: _problem != null ? null : _save,
      children: [
        Text('Enter what is on the shelf, or scan each item’s barcode. Leave an item empty to skip it. Count every item to close the month’s count.', style: tkHint(t)),
        TkListRow(
          title: 'Scan to count',
          sub: 'Each scan adds one to that item',
          icon: 'b-qr',
          square: TkLook.of(context).teal,
          onTap: _busy ? null : _scan,
        ),
        for (final e in _unknown.entries)
          TkCallout(
            leading: TkSquare('b-qr', color: TkLook.of(context).orange),
            title: 'Not one of your items yet: ${e.key}',
            sub: 'Scanned ${e.value == 1 ? 'once' : '${e.value} times'}. Say which item it is, and its count goes there.',
            subLines: 3,
            color: TkLook.of(context).orange,
            button: _naming == e.key ? 'Saving…' : 'Which item?',
            onPressed: _naming != null ? null : () => _name(e.key),
          ),
        for (final it in widget.items)
          TkKitField(
            label: it['name'] as String? ?? '',
            hint: countDiff(it, _boxes[it['id']]!.text) ?? 'Taskiem has ${stockLine(it)}.',
            child: TkTextBox(controller: _boxes[it['id']]!, digits: 7),
          ),
        TkKitField(label: 'Why they differ', hint: 'Spoilt, rats, a miscount.', child: TkTextBox(controller: _reason, max: 200)),
      ],
    );
  }
}
