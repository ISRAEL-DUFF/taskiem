// Ordering from a linked business (M5 part O): their price list or their
// storefront as items, the bag, and what an order sent them says. A port
// of web/src/lib/buyer.ts, deals.ts (priceAt, breakWords) and
// ChatBuyer.tsx (orderShown, orderSections), pure, so each is tested.
// Shapes are the API's JSON (GET /v1/supplier-prices).

import 'tools.dart' show categoryTag;
import 'words.dart' show naira;

/// The storefront's name for a seller's offer with no shared list
/// (engine/links StorefrontListName).
const storefrontPrices = 'Storefront prices';

/// A lower price from a quantity up.
typedef Break = ({int min, int price});

/// One of their sizes of a product.
class PriceSize {
  const PriceSize({required this.id, required this.name, this.label = '', this.price, required this.available, this.left});
  final String id;
  final String name;

  /// Its choices' values in the product's order: "42, Black"
  /// (variants.ts label).
  final String label;
  final int? price;
  final bool available;
  final int? left;

  factory PriceSize.fromJson(Map<String, dynamic> j, [List<String> choices = const []]) {
    final v = (j['variant'] as Map?)?.cast<String, dynamic>() ?? const {};
    return PriceSize(
      id: j['item_id'] as String,
      name: j['name'] as String? ?? '',
      label: (choices.isEmpty ? v.values : choices.map((c) => v[c])).whereType<String>().where((x) => x.isNotEmpty).join(', '),
    price: (j['price_kobo'] as num?)?.toInt(),
    available: j['available'] == true,
      left: (j['left'] as num?)?.toInt(),
    );
  }
}

/// An item on their list (pricelists.ts PriceItem), or one size of a
/// product as an order line names it ([variantOf] its product).
class PriceItem {
  const PriceItem({
    required this.id,
    required this.name,
    this.unit = '',
    this.category = '',
    this.description = '',
    required this.price,
    this.min = 1,
    this.available = true,
    this.overridden = false,
    this.breaks = const [],
    this.choices = 0,
    this.sizes = const [],
    this.photo = false,
    this.left,
    this.variantOf,
  });
  final String id;
  final String name;
  final String unit;
  final String category;
  final String description;
  final int price;
  final int min;
  final bool available;
  final bool overridden;
  final List<Break> breaks;

  /// How many choices (size, colour) a product sold in sizes has.
  final int choices;
  final List<PriceSize> sizes;
  final bool photo;
  final int? left;
  final String? variantOf;

  factory PriceItem.fromJson(Map<String, dynamic> j) {
    final choices = [for (final c in (j['choices'] as List? ?? const []).whereType<Map>()) c['name'] as String? ?? ''];
    return PriceItem(
    id: j['item_id'] as String,
    name: j['name'] as String? ?? '',
    unit: j['unit'] as String? ?? '',
    category: j['category'] as String? ?? '',
    description: j['description'] as String? ?? '',
    price: (j['price_kobo'] as num?)?.toInt() ?? 0,
    min: (j['min_quantity'] as num?)?.toInt() ?? 1,
    available: j['available'] == true,
    overridden: j['overridden'] == true,
    breaks: [
      for (final b in (j['breaks'] as List? ?? const []).whereType<Map<String, dynamic>>())
        (min: (b['min_quantity'] as num).toInt(), price: (b['price_kobo'] as num).toInt()),
    ],
    choices: choices.length,
    sizes: [for (final z in (j['sizes'] as List? ?? const []).whereType<Map<String, dynamic>>()) PriceSize.fromJson(z, choices)],
    photo: (j['photo'] as Map?)?.isNotEmpty == true,
    left: (j['left'] as num?)?.toInt(),
  );
  }

  /// A size's product, else the item itself: where its minimum and
  /// quantity prices count.
  String get product => variantOf ?? id;
}

/// A step of − or +: + from nothing goes to its minimum, − under the
/// minimum takes it off.
int stepQty(int qty, int min, int dir) {
  final m = min < 1 ? 1 : min;
  if (dir > 0) return qty == 0 ? m : qty + 1;
  return qty - 1 < m ? 0 : qty - 1;
}

/// A product sold in sizes and colours: ordered by its sizes, never itself.
bool soldInSizes(PriceItem it) => it.choices > 0 || it.sizes.isNotEmpty;

/// What one costs at a quantity: the last break reached, else its price.
int priceAt(PriceItem it, int qty) {
  var p = it.price;
  for (final b in it.breaks) {
    if (qty >= b.min) p = b.price;
  }
  return p;
}

String _plural(String unit, int n) => n == 1 || unit.endsWith('s') ? unit : '${unit}s';

/// "from 10 bags ₦39,500 · from 50 ₦38,800".
String breakWords(PriceItem it) => [
  for (var i = 0; i < it.breaks.length; i++)
    'from ${it.breaks[i].min}${i == 0 && it.unit.isNotEmpty ? ' ${_plural(it.unit, it.breaks[i].min)}' : ''} ${naira(it.breaks[i].price)}',
].join(' · ');

/// The list as order lines name it: each size of a product on its own.
List<PriceItem> flatItems(List<PriceItem> items) {
  final out = <PriceItem>[];
  for (final it in items) {
    if (it.sizes.isEmpty) {
      if (it.choices == 0) out.add(it);
      continue;
    }
    for (final z in it.sizes) {
      final own = (z.price ?? 0) > 0 && !it.overridden;
      out.add(PriceItem(
        id: z.id,
        name: z.name,
        unit: it.unit,
        category: it.category,
        price: own ? z.price! : it.price,
        min: 1,
        available: it.available && z.available,
        breaks: own ? const [] : it.breaks,
        variantOf: it.id,
      ));
    }
  }
  return out;
}

/// "36 pairs in 7 sizes and colours".
String sizesTaken(PriceItem product, Map<String, int> qty) {
  final taken = product.sizes.where((z) => (qty[z.id] ?? 0) > 0).toList();
  if (taken.isEmpty) return '';
  final n = taken.fold<int>(0, (a, z) => a + (qty[z.id] ?? 0));
  final unit = product.unit.isEmpty ? '' : (n == 1 ? product.unit : '${product.unit}s');
  final sizes = taken.length == 1 ? '1 size or colour' : '${taken.length} sizes and colours';
  return '$n${unit.isEmpty ? '' : ' $unit'} in $sizes';
}

typedef BagLine = ({PriceItem item, int quantity});

/// The bag: its lines, the total, how many, and a line's price each.
class Bag {
  Bag(List<PriceItem> items, Map<String, int> qty) {
    final flat = flatItems(items);
    lines = [
      for (final it in flat)
        if ((qty[it.id] ?? 0) > 0) (item: it, quantity: qty[it.id]!),
    ];
    for (final l in lines) {
      _sum[l.item.product] = (_sum[l.item.product] ?? 0) + l.quantity;
    }
    total = lines.fold(0, (n, l) => n + l.quantity * unitPrice(l.item));
    final products = lines.map((l) => l.item.product).toSet().length;
    count = products == 0 ? 'Nothing yet' : '$products ${products == 1 ? 'item' : 'items'} in your order';
  }

  final _sum = <String, int>{};
  late final List<BagLine> lines;
  late final int total;
  late final String count;

  int unitPrice(PriceItem it) => priceAt(it, _sum[it.product] ?? 0);
}

/// A product sold in sizes whose sizes together are under its minimum.
PriceItem? shortOf(List<PriceItem> items, Map<String, int> qty) {
  for (final it in items) {
    final n = it.sizes.fold<int>(0, (a, z) => a + (qty[z.id] ?? 0));
    if (n > 0 && n < it.min) return it;
  }
  return null;
}

/// A message asking them to quote for the bag.
String quoteText(List<BagLine> lines) => ['Could you quote me for:', for (final l in lines) '• ${l.quantity} × ${l.item.name}'].join('\n');

/// An order row's line: "₦78,000 · min 2 · 3 left".
String orderLine(PriceItem it) =>
    '${naira(it.price)}${it.min > 1 ? ' · min ${it.min}' : ''}${it.left != null && it.left! > 0 ? ' · ${it.left} left' : ''}';

/// What an item that cannot be ordered now says.
String notNowLine(PriceItem it) => it.left == 0 ? 'Sold out' : 'Not available now';

/// The row's line: its price, or why it cannot be ordered now.
String rowLine(PriceItem it) => it.available && !(soldInSizes(it) && it.sizes.isEmpty) ? orderLine(it) : notNowLine(it);

/// The items a chip and a search show: by name first, then by category or
/// description.
List<PriceItem> orderShown(List<PriceItem> items, String? chip, String search) {
  final q = search.trim().toLowerCase();
  final picked = items.where((it) => chip == null || (it.category.isNotEmpty && categoryTag(it.category) == chip)).toList();
  if (q.isEmpty) return picked;
  bool named(PriceItem it) => it.name.toLowerCase().contains(q);
  return [
    ...picked.where(named),
    ...picked.where((it) => !named(it) && [it.category, it.description].any((t) => t.toLowerCase().contains(q))),
  ];
}

/// Their list under its categories' headings, "Other" last; one section
/// with no heading when it has none.
List<({String label, List<PriceItem> items})> orderSections(List<PriceItem> items) {
  if (!items.any((it) => it.category.isNotEmpty)) return items.isEmpty ? [] : [(label: '', items: items)];
  final by = <String, ({String label, List<PriceItem> items})>{};
  final other = <PriceItem>[];
  for (final it in items) {
    final c = it.category.trim();
    if (c.isEmpty) {
      other.add(it);
      continue;
    }
    by.putIfAbsent(c.toLowerCase(), () => (label: c, items: <PriceItem>[])).items.add(it);
  }
  return [...by.values, if (other.isNotEmpty) (label: 'Other', items: other)];
}

/// The last order's lines again, as their list has them now.
Map<String, int> reorderQty(List<Map<String, dynamic>> lastLines, List<Map<String, dynamic>> links, List<PriceItem> items) {
  final out = <String, int>{};
  final orderable = flatItems(items);
  for (final l in lastLines) {
    final ours = l['item_id'] as String?;
    final seller = links.where((x) => x['item_id'] == ours).map((x) => x['seller_item_id'] as String?).firstOrNull ??
        orderable.where((it) => it.name == l['item']).map((it) => it.id).firstOrNull;
    final it = orderable.where((x) => x.id == seller).firstOrNull;
    if (it != null && it.available) {
      final q = (l['quantity'] as num?)?.toInt() ?? 0;
      out[it.id] = (out[it.id] ?? 0) + (q > it.min ? q : it.min);
    }
  }
  return out;
}

/// Their categories as chips (tools.ts categoryChips over a price list):
/// most items first; a category of one gets none, unless it is on.
List<({String label, String tag})> priceChips(List<PriceItem> items, [String? on]) {
  final n = <String, ({String name, int count})>{};
  for (final it in items) {
    final c = it.category.trim();
    if (c.isEmpty) continue;
    final got = n[c.toLowerCase()];
    n[c.toLowerCase()] = got == null ? (name: c, count: 1) : (name: got.name, count: got.count + 1);
  }
  final out = n.values.toList()..sort((a, b) => b.count != a.count ? b.count - a.count : a.name.compareTo(b.name));
  return [
    for (final c in out)
      if (c.count > 1 || categoryTag(c.name) == on) (label: c.name, tag: categoryTag(c.name)),
  ];
}
