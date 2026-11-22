// Stock items on the screens (S2; docs/spec/what-a-business-buys.md §4.5):
// what a stock item is sold as, or Not sold; the Stock page's rows grouped
// by a product's sizes and colours; Add something to count, asking Do you
// sell it?; and Counted by on a product's page.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show PutCountedByRequest, StockItemRow, StockItemRowSoldAsItem;
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/forms.dart' show TkFormRow, TkMoneyField, nairaKobo;
import '../kit/kit.dart' show TkRadio;
import '../kit/list.dart' show TkSoftButton;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkFormGroup, TkFormSheet, TkListRow;
import '../kit/sheet.dart' show TkKitField, TkPickBox, showTkSheet;
import '../theme/look.dart' show TkLook;
import 'approve.dart' show TkTextBox;
import 'groups.dart' show pickOneOf;
import 'stock_page.dart' show stockQuantity;
import 'words.dart' show naira;

int? _int(Object? v) => (v as num?)?.toInt();

String _why(Object e) => e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.';

/// A size's or colour's words: "M, Red".
String variantWords(Object? v) => v is Map ? v.values.map((x) => '$x').join(', ') : '';

/// One product a stock item is sold as: its name (or its size), and how
/// many of the stock item one sale uses when more than one: "Malt, pack of
/// 6 (6 a sale)".
String soldAsName(StockItemRowSoldAsItem s, {bool size = false}) {
  final name = size && variantWords(s.variant).isNotEmpty ? '${s.name}, ${variantWords(s.variant)}' : s.name;
  return s.uses > 1 ? '$name (${s.uses} a sale)' : name;
}

/// "Sold as Indomie", "Sold as Malt, Pack of 6 (6 a sale)", or
/// "Not sold" for a material.
String soldAsLine(StockItemRow r) =>
    r.soldAs.isEmpty ? 'Not sold' : 'Sold as ${r.soldAs.map((s) => soldAsName(s, size: r.groupOf.isNotEmpty)).join(', ')}';

/// Its cost, levels and supplier, and what to order when it is low.
String stockItemFacts(StockItemRow r) => [
  if (_int(r.costKobo) != null) 'cost ${naira(_int(r.costKobo)!)}',
  if (_int(r.lowLevel) != null) 'low at ${_int(r.lowLevel)}',
  if (_int(r.targetLevel) != null) 'up to ${_int(r.targetLevel)}',
  if (r.supplier.isNotEmpty) r.supplier,
  if (r.low && (_int(r.suggest) ?? 0) > 0) 'order ${stockQuantity(_int(r.suggest)!, r.unit)}',
].join(' · ');

/// A row's line under its name: what it is sold as, then its facts.
String stockItemSub(StockItemRow r) => [soldAsLine(r), if (stockItemFacts(r).isNotEmpty) stockItemFacts(r)].join(' · ');

/// The Stock page's filter.
enum StockShow { all, sold, notSold }

bool shows(StockShow s, StockItemRow r) => switch (s) {
  StockShow.all => true,
  StockShow.sold => r.soldAs.isNotEmpty,
  StockShow.notSold => r.soldAs.isEmpty,
};

/// A row of the Stock page: a stock item on its own, or a product sold in
/// sizes and colours with its stock items after it ([group] the product's
/// id and name, [of] its stock items).
typedef StockRowOf = ({StockItemRow? item, ({String id, String name})? group, List<StockItemRow> of});

/// The stock items in order, by name, the stock items of a product's sizes
/// and colours together under it, where its name falls.
List<StockRowOf> stockRows(List<StockItemRow> items) {
  final groups = <String, List<StockItemRow>>{};
  for (final r in items) {
    if (r.groupOf.isNotEmpty) groups.putIfAbsent(r.groupOf, () => []).add(r);
  }
  final out = <(String, StockRowOf)>[
    for (final r in items)
      if (r.groupOf.isEmpty) (r.name.toLowerCase(), (item: r, group: null, of: const <StockItemRow>[])),
    for (final e in groups.entries)
      (
        e.value.first.groupName.toLowerCase(),
        (item: null, group: (id: e.key, name: e.value.first.groupName.isEmpty ? e.value.first.name : e.value.first.groupName), of: e.value),
      ),
  ]..sort((a, b) => a.$1.compareTo(b.$1));
  return [for (final x in out) x.$2];
}

/// How full a stock item is, against what it is ordered up to (or twice
/// its low level); null with neither.
double? stockLevel(StockItemRow r) {
  final full = _int(r.targetLevel) ?? ((_int(r.lowLevel) ?? 0) * 2);
  return full <= 0 ? null : r.count / full;
}

int? _whole(String v) => RegExp(r'^\d+$').hasMatch(v.trim()) ? int.parse(v.trim()) : null;

/// Add something to count (§4.5): a stock item, asking Do you sell it?
/// Yes also makes its product in the catalogue, with a selling price; No
/// makes a material, counted and never sold. Pops with its name once added.
class AddToCountSheet extends StatefulWidget {
  const AddToCountSheet({super.key, required this.auth});
  final Auth auth;

  @override
  State<AddToCountSheet> createState() => _AddToCountSheetState();
}

class _AddToCountSheetState extends State<AddToCountSheet> {
  bool? _sell;
  final _name = TextEditingController();
  final _quantity = TextEditingController();
  final _unit = TextEditingController();
  final _price = TextEditingController();
  final _cost = TextEditingController();
  final _low = TextEditingController();
  final _target = TextEditingController();
  final _supplier = TextEditingController();
  late final _key = uuid4();
  bool _busy = false;
  String? _error;

  List<TextEditingController> get _all => [_name, _quantity, _unit, _price, _cost, _low, _target, _supplier];

  @override
  void initState() {
    super.initState();
    for (final c in _all) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final c in _all) {
      c.dispose();
    }
    super.dispose();
  }

  /// What stops it being added, in words; null when it can be.
  String? get _problem {
    if (_sell == null) return 'Say whether you sell it.';
    if (_name.text.trim().isEmpty) return 'Give it a name.';
    final q = _whole(_quantity.text);
    if (_quantity.text.trim().isNotEmpty && !(q != null && q <= 1000000)) return 'How many you have is a whole number, like 50.';
    if (_unit.text.trim().length > 30) return 'A unit is one word, like yard or carton.';
    if (_sell! && _price.text.trim().isNotEmpty && nairaKobo(_price.text) == null) return 'The selling price is an amount, like 8,500.';
    if (_cost.text.trim().isNotEmpty && nairaKobo(_cost.text) == null) return 'The cost is an amount, like 8,500.';
    if (_low.text.trim().isNotEmpty && _whole(_low.text) == null) return 'Low at is a whole number.';
    if (_target.text.trim().isNotEmpty && _whole(_target.text) == null) return 'Order up to is a whole number.';
    if (_low.text.trim().isNotEmpty && _target.text.trim().isNotEmpty && _whole(_target.text)! <= _whole(_low.text)!) return 'Order up to more than it is low at.';
    return null;
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    final name = _name.text.trim();
    try {
      await widget.auth.transport.send(
        'POST',
        '/v1/stock/items',
        body: {
          'name': name,
          'quantity': _quantity.text.trim().isEmpty ? 0 : _whole(_quantity.text),
          'unit': _unit.text.trim(),
          if (_sell! && _price.text.trim().isNotEmpty) 'price_kobo': nairaKobo(_price.text),
          if (_cost.text.trim().isNotEmpty) 'cost_kobo': nairaKobo(_cost.text),
          if (_low.text.trim().isNotEmpty) 'reorder_level': _whole(_low.text),
          if (_target.text.trim().isNotEmpty) 'target_level': _whole(_target.text),
          if (_supplier.text.trim().isNotEmpty) 'supplier': _supplier.text.trim(),
          if (!_sell!) 'material': true,
        },
        headers: {'Idempotency-Key': _key},
      );
      if (mounted) Navigator.of(context).pop(name);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = _why(e), _busy = false));
    }
  }

  Widget _choice(bool yes, String title, String sub) => Semantics(
    selected: _sell == yes,
    child: TkListRow(title: title, sub: sub, leading: TkRadio(checked: _sell == yes), onTap: () => setState(() => _sell = yes)),
  );

  Widget _field(String label, TextEditingController c, {String? hint, bool number = false, int? max}) => TkKitField(
    label: label,
    hint: hint,
    child: TkTextBox(controller: c, digits: number ? 7 : null, max: max, size: 15, height: 46),
  );

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final problem = _problem;
    final unit = _unit.text.trim();
    return TkFormSheet(
      title: 'Add something to count',
      crumb: 'Stock',
      action: _busy ? 'Adding…' : 'Add it',
      onAction: _busy || problem != null ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(
          title: 'Do you sell it?',
          children: [
            _choice(true, 'Yes, I sell it', 'It goes in your products too'),
            _choice(false, 'No, I don’t', 'Counted only, as fabric or packaging'),
          ],
        ),
        TkFormGroup(
          children: [
            _field('Name', _name, max: 120, hint: _sell == false ? 'Ankara fabric, nylon bags' : null),
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: _field('How many you have', _quantity, number: true)),
                const SizedBox(width: 10),
                Expanded(child: _field('Unit', _unit, max: 30, hint: 'yard, carton')),
              ],
            ),
            if (_sell == true) TkMoneyField(label: 'Selling price', controller: _price, hint: 'Without one, it is not on your page.'),
          ],
        ),
        TkFormGroup(
          title: 'Cost and levels',
          children: [
            TkMoneyField(label: unit.isEmpty ? 'Cost of one' : 'Cost of one $unit', controller: _cost),
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: _field('Low at', _low, number: true)),
                const SizedBox(width: 10),
                Expanded(child: _field('Order up to', _target, number: true)),
              ],
            ),
            _field('Supplier', _supplier, max: 120, hint: 'One of your suppliers'),
          ],
        ),
        if (_error != null || (problem != null && _sell != null && _name.text.trim().isNotEmpty))
          TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error ?? problem!, color: look.red, subLines: 3),
      ],
    );
  }
}

/// Counted by on a product's page (§4.5): the stock item a sale lowers,
/// how many of it one sale uses, and its count; Change chooses another or
/// none. A product sold in sizes and colours has each counted by its own,
/// so here it is only said. [onChanged] has the count it has now.
class CountedByRow extends StatefulWidget {
  const CountedByRow({super.key, required this.auth, required this.product, required this.name, this.sizes = false, this.canEdit = true, this.onChanged});
  final Auth auth;

  /// The product's catalogue row id.
  final String product;
  final String name;
  final bool sizes;
  final bool canEdit;
  final ValueChanged<int?>? onChanged;

  @override
  State<CountedByRow> createState() => _CountedByRowState();
}

class _CountedByRowState extends State<CountedByRow> {
  List<StockItemRow>? _items;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    if (!widget.sizes) _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listStockItems();
      if (mounted) setState(() => (_items = r.items, _failed = false));
    } on Object {
      if (mounted) setState(() => _failed = true);
    }
  }

  StockItemRow? get _by => _items?.where((r) => r.soldAs.any((s) => s.productId == widget.product)).firstOrNull;

  int get _uses => _by?.soldAs.firstWhere((s) => s.productId == widget.product).uses ?? 1;

  Future<void> _change() async {
    final items = _items;
    if (items == null) return;
    final by = _by;
    final done = await showTkSheet<bool>(
      context,
      (_) => CountedBySheet(auth: widget.auth, product: widget.product, name: widget.name, items: items, by: by, uses: _uses),
    );
    if (done != true || !mounted) return;
    await _load();
    widget.onChanged?.call(_by?.count);
  }

  @override
  Widget build(BuildContext context) {
    if (widget.sizes) {
      return const TkFormRow(
        title: 'Counted by',
        sub: 'Each size and colour is counted by its own stock item, so it cannot be chosen here',
        trailing: SizedBox.shrink(),
      );
    }
    final by = _by;
    final sub = _failed
        ? 'Could not be read. Pull to try again later.'
        : _items == null
        ? 'Loading…'
        : by == null
        ? 'Nothing yet: a sale lowers no count'
        : '${by.name} · ${stockQuantity(_uses, by.unit)} a sale · ${stockQuantity(by.count, by.unit)} now';
    return TkFormRow(
      title: 'Counted by',
      sub: sub,
      trailing: widget.canEdit && _items != null
          ? TkSoftButton(label: by == null ? 'Choose' : 'Change', semanticsLabel: '${by == null ? 'Choose' : 'Change'} what counts ${widget.name}', onTap: _change)
          : const SizedBox.shrink(),
    );
  }
}

/// Choose what counts a product: one of the stock items (not a size's or
/// colour's, nor one retired) and how many of it one sale uses; or none.
/// Pops true once changed.
class CountedBySheet extends StatefulWidget {
  const CountedBySheet({super.key, required this.auth, required this.product, required this.name, required this.items, this.by, this.uses = 1});
  final Auth auth;
  final String product;
  final String name;
  final List<StockItemRow> items;
  final StockItemRow? by;
  final int uses;

  @override
  State<CountedBySheet> createState() => _CountedBySheetState();
}

class _CountedBySheetState extends State<CountedBySheet> {
  late String? _chosen = widget.by?.id;
  late final _uses = TextEditingController(text: '${widget.uses}')..addListener(() => setState(() {}));
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _uses.dispose();
    super.dispose();
  }

  List<StockItemRow> get _choices => widget.items.where((r) => !r.retired && r.groupOf.isEmpty).toList();

  StockItemRow? get _item => widget.items.where((r) => r.id == _chosen).firstOrNull;

  int? get _n {
    final n = _whole(_uses.text);
    return n == null || n < 1 || n > 1000000 ? null : n;
  }

  Future<void> _pick() async {
    final id = await pickOneOf(
      context,
      title: 'Counted by',
      value: _chosen,
      searchHint: _choices.length > 8 ? 'Search your stock items' : null,
      choices: [for (final r in _choices) (r.id, r.name, '${stockQuantity(r.count, r.unit)} · ${soldAsLine(r)}')],
    );
    if (id != null) setState(() => _chosen = id);
  }

  Future<void> _send(Future<void> Function() call) async {
    setState(() => (_busy = true, _error = null));
    try {
      await call();
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = _why(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final item = _item;
    final n = _n;
    final changed = _chosen != widget.by?.id || n != widget.uses;
    return TkFormSheet(
      title: 'Counted by',
      crumb: widget.name,
      action: _busy ? 'Saving…' : 'Save',
      onAction: _busy || _chosen == null || n == null || !changed
          ? null
          : () => _send(() => widget.auth.api.putCountedBy(id: widget.product, body: PutCountedByRequest(stockItemId: _chosen!, uses: n))),
      second: widget.by != null ? 'Count by none' : null,
      secondDanger: true,
      onSecond: _busy ? null : () => _send(() => widget.auth.api.deleteCountedBy(id: widget.product)),
      children: [
        TkFormGroup(
          children: [
            TkKitField(
              label: 'Stock item',
              field: false,
              hint: item == null ? 'What a sale of ${widget.name} lowers' : '${stockQuantity(item.count, item.unit)} now',
              child: TkPickBox(label: 'Stock item', shown: item?.name ?? 'Choose…', onTap: _pick),
            ),
            TkKitField(
              label: 'How many one sale uses',
              hint: item == null || n == null ? 'A pack of 6 uses 6' : 'One sale of ${widget.name} uses ${stockQuantity(n, item.unit)}',
              error: _uses.text.trim().isNotEmpty && n == null ? 'A whole number, 1 or more' : null,
              child: TkTextBox(controller: _uses, digits: 7, size: 15, height: 46),
            ),
          ],
        ),
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error!, color: look.red, subLines: 3),
      ],
    );
  }
}
