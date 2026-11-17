// The chat's tools (web/src/lib/tools.ts; docs/spec/chat-tools.md §3): the
// tiles of the tools panel and what each one's sheet lists. Each tool reads
// its list from the server as the web's does (the JSON, by its url), and
// makes its rows, its tile's line and badge, and its chips from it.

import 'words.dart';

typedef ToolKey = String;

/// A row's pill, a tile's badge: its words and tone.
typedef ToolPill = ({String text, CardTone tone});

/// One row of a tool's sheet.
class ToolRow {
  const ToolRow({
    required this.id,
    required this.name,
    required this.sub,
    this.pill,
    required this.tags,
    this.category,
    this.words,
    this.data,
    this.link,
  });

  final String id;
  final String name;
  final String sub;
  final ToolPill? pill;

  /// For the chips: which it is in ("low", "hidden", "new", and
  /// "cat:sneakers" for its category).
  final List<String> tags;

  /// Its category and its words (a product's description): search reads
  /// them after the name.
  final String? category;
  final String? words;

  /// What it is, as the server said it (a stock item, an order…).
  final Map<String, dynamic>? data;

  /// The linked business it is with (a buyer's orders and bills): "" for
  /// one with a business not on Taskiem.
  final String? link;

  ToolRow withPill(ToolPill? p) =>
      ToolRow(id: id, name: name, sub: sub, pill: p, tags: tags, category: category, words: words, data: data, link: link);
}

class Tool {
  const Tool({
    required this.key,
    required this.title,
    this.titleFor,
    required this.icon,
    required this.perm,
    required this.url,
    required this.rows,
    required this.line,
    this.badge,
    this.chips = const [],
    required this.more,
    this.rowIcon,
    this.staffOnly = false,
    this.deliversOnly = false,
    this.add,
  });

  final ToolKey key;
  final String title;

  /// The title from the rows, when it depends on them (Orders is
  /// Deliveries for a business whose orders are all deliveries).
  final String Function(List<ToolRow>)? titleFor;
  final String icon;

  /// The permission it needs ("a|b": either).
  final String perm;
  final String url;

  /// The rows, from what the url answers.
  final List<ToolRow> Function(Object? data) rows;

  /// The tile's second line, from the rows.
  final String Function(List<ToolRow>) line;

  /// The tile's badge ("3 low", "4 new"), when there is one.
  final ToolPill? Function(List<ToolRow>)? badge;

  /// The chips above the list: a word and the tag it shows.
  final List<({String label, String tag})> chips;

  /// The full page's words ("Open stock").
  final String more;

  /// Rows with no name of their own to show (an order, a bill): an icon on
  /// each row's tile, not letters.
  final String? rowIcon;
  final bool staffOnly;
  final bool deliversOnly;

  /// ChatTools.tsx ADDS: the sheet's button for a new one ("Add product").
  final String? add;

  String titleOf(List<ToolRow>? rows) => rows != null && titleFor != null ? titleFor!(rows) : title;
}

String _count(int n, String one, [String? many]) => '$n ${n == 1 ? one : (many ?? '${one}s')}';
int _tagged(List<ToolRow> rows, String t) => rows.where((r) => r.tags.contains(t)).length;

List<Map<String, dynamic>> _list(Object? data, String key) =>
    ((data is Map ? data[key] : null) as List?)?.whereType<Map<String, dynamic>>().toList() ?? const [];
int? _int(Object? v) => v is num ? v.toInt() : null;
String _str(Object? v) => v is String ? v : '';

/// "9 Nov": a day as the lists say it.
String toolDay(String iso) {
  final d = DateTime.tryParse(iso)?.toLocal();
  if (d == null) return '';
  const m = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  return '${d.day} ${m[d.month - 1]}';
}

// --- stock (stockRows, variants.ts) ---

ToolPill? _stockPill(int? stock, bool low) {
  if (stock == null) return null;
  if (low || stock <= 0) return (text: stock <= 0 ? 'Out' : '$stock left', tone: CardTone.warn);
  return (text: '$stock in stock', tone: CardTone.plain);
}

/// variants.ts sizesWords.
String sizesWords(int n) => n == 1 ? '1 size or colour' : '$n sizes and colours';

/// A category as a chip's tag.
String categoryTag(String c) => 'cat:${c.trim().toLowerCase()}';

List<ToolRow> stockRows(Object? data) {
  final all = _list(data, 'items');
  return [
    for (final it in all.where((x) => x['variant_of'] == null))
      () {
        final id = _str(it['id']);
        // A product's sizes and colours are not rows of their own: the
        // product says how many it has and how many are low.
        final hasSizes = ((it['choices'] as List?)?.length ?? 0) > 0;
        int? stock = _int(it['stock']);
        var low = it['low'] == true;
        var sizes = 0, sizesLow = 0;
        if (hasSizes) {
          final vs = all.where((x) => x['variant_of'] == id && x['available'] == true).toList();
          sizes = vs.length;
          sizesLow = vs.where((v) => v['low'] == true).length;
          int? s;
          for (final v in vs) {
            final n = _int(v['stock']);
            if (n != null) s = (s ?? 0) + n;
          }
          stock = s;
          low = sizesLow > 0;
        }
        final kobo = _int(it['price_kobo']);
        final unit = _str(it['unit']);
        final price = kobo != null ? '${naira(kobo)}${unit.isNotEmpty ? ' a $unit' : ''}' : 'No price yet';
        final category = it['category'] as String?;
        return ToolRow(
          id: id,
          name: _str(it['name']),
          sub: hasSizes ? '${sizesWords(sizes)}${sizesLow > 0 ? ' · $sizesLow low' : ''} · $price' : price,
          pill: _stockPill(stock, low),
          tags: [if (low) 'low', if (it['available'] != true) 'hidden', if (category != null && category.isNotEmpty) categoryTag(category)],
          category: category,
          words: it['description'] as String?,
          data: it,
        );
      }(),
  ];
}

/// mentions.ts categoriesOf: the most used first.
List<({String name, int count})> categoriesOf(List<ToolRow> rows) {
  final n = <String, ({String name, int count})>{};
  for (final r in rows) {
    final c = r.category?.trim() ?? '';
    if (c.isEmpty) continue;
    final k = c.toLowerCase();
    final got = n[k];
    n[k] = got == null ? (name: c, count: 1) : (name: got.name, count: got.count + 1);
  }
  final out = n.values.toList()..sort((a, b) => b.count != a.count ? b.count - a.count : a.name.compareTo(b.name));
  return out;
}

/// The chips for the categories of a sheet's rows, after its own: a
/// category of one item gets none, unless it is the one on.
List<({String label, String tag})> categoryChips(List<ToolRow> rows, [String? on]) => [
  for (final c in categoriesOf(rows))
    if (c.count > 1 || categoryTag(c.name) == on) (label: c.name, tag: categoryTag(c.name)),
];

ToolPill? _lowBadge(List<ToolRow> rows) {
  final n = _tagged(rows, 'low');
  return n > 0 ? (text: '$n low', tone: CardTone.warn) : null;
}

// --- orders, purchases, bills, invoices, money ---

const _orderWord = {
  'received': 'New',
  'changed': 'Changed',
  'proposed': 'Draft',
  'accepted': 'Accepted',
  'ready': 'Ready',
  'dispatched': 'On the way',
  'arrived': 'At the door',
  'delivered': 'Delivered',
  'declined': 'Declined',
  'cancelled': 'Cancelled',
};

/// buyer.ts purchaseState: an order to a supplier, as its pill says it.
({String word, CardTone tone, int? step, bool quote}) purchaseState(Map<String, dynamic> p) {
  final status = _str(p['status']);
  final seller = _str(p['seller_status']);
  if (status == 'cancelled' || seller == 'cancelled') return (word: 'Cancelled', tone: CardTone.danger, step: null, quote: false);
  if (seller == 'declined') return (word: 'Declined', tone: CardTone.danger, step: null, quote: false);
  if (status == 'received') return (word: 'Received', tone: CardTone.lime, step: 3, quote: false);
  switch (seller) {
    case 'proposed':
      return (word: 'Their quote: confirm it', tone: CardTone.warn, step: 0, quote: true);
    case 'changed':
      return (word: 'They changed it', tone: CardTone.warn, step: 0, quote: false);
    case 'accepted':
    case 'ready':
      return (word: seller == 'ready' ? 'Ready' : 'Accepted', tone: CardTone.lime, step: 1, quote: false);
    case 'dispatched':
      return (word: 'On the way', tone: CardTone.lime, step: 2, quote: false);
    case 'delivered':
      return (word: 'Delivered', tone: CardTone.lime, step: 3, quote: false);
  }
  if (status == 'drafted') return (word: 'Draft', tone: CardTone.plain, step: null, quote: false);
  return (word: p['cancel_asked_at'] != null ? 'Cancel asked' : 'Sent', tone: CardTone.plain, step: 0, quote: false);
}

const _invoiceStatus = {
  'draft': ('Draft', CardTone.plain),
  'recorded': ('Recorded', CardTone.plain),
  'sent': ('Sent', CardTone.plain),
  'part_paid': ('Part-paid', CardTone.warn),
  'paid': ('Paid', CardTone.lime),
  'overdue': ('Overdue', CardTone.warn),
  'void': ('Void', CardTone.plain),
};

bool _owing(Map<String, dynamic> x) =>
    (_int(x['due_kobo']) ?? 0) > 0 && const {'sent', 'part_paid', 'overdue', 'recorded'}.contains(x['status']);

String _invoiceSub(Map<String, dynamic> x) {
  final total = _int(x['total_kobo']) ?? 0, due = _int(x['due_kobo']) ?? 0;
  final dueOn = x['due_on'] as String?;
  final parts = [_str((x['customer'] as Map?)?['name']), naira(total)];
  if (_owing(x)) parts.add(due < total ? '${naira(due)} to pay' : (dueOn != null ? 'due ${toolDay(dueOn)}' : 'to pay'));
  return parts.where((p) => p.isNotEmpty).join(' · ');
}

const _kindWords = {
  'payment': 'Payment',
  'account_credit': 'Transfer to an account',
  'payout': 'Paid out',
  'refund': 'Refund',
  'pos_sale': 'Card sale',
  'fee': 'Fee',
  'statement': 'From a statement',
  'cash': 'Cash',
  'expense': 'Expense',
  'correction': 'Correction',
};

String _entryName(Map<String, dynamic> e) {
  for (final k in ['description', 'vendor']) {
    final v = _str(e[k]);
    if (v.isNotEmpty) return v;
  }
  final kind = _str(e['kind']);
  return _kindWords[kind] ?? kind.replaceAll('_', ' ');
}

String _entrySub(Map<String, dynamic> e) {
  final amount = _int(e['amount_kobo']);
  final a = amount == null ? 'No amount' : '${e['direction'] == 'in' ? '+' : '−'}${naira(amount)}';
  final named = _str(e['description']).isNotEmpty || _str(e['vendor']).isNotEmpty;
  final channel = _str(e['channel']);
  return [a, if (named) _kindWords[_str(e['kind'])], if (channel.isNotEmpty && channel != 'cash') channel, toolDay(_str(e['when']))]
      .whereType<String>()
      .where((p) => p.isNotEmpty)
      .join(' · ');
}

ToolPill? _entryPill(Map<String, dynamic> e) {
  final s = e['status'];
  if (s == 'failed' || s == 'reversed') return (text: s == 'failed' ? 'Failed' : 'Reversed', tone: CardTone.plain);
  if (s == 'pending') return (text: 'Pending', tone: CardTone.plain);
  if (e['to_match'] == true) return (text: 'To match', tone: CardTone.warn);
  if (_str(e['towards']).isNotEmpty) return (text: 'Counted', tone: CardTone.lime);
  return null;
}

final List<Tool> tools = [
  Tool(
    key: 'sell',
    title: 'Sell',
    icon: 'money',
    perm: 'table.edit',
    url: '/v1/stock',
    // Things that can be sold: those with a price, on sale.
    rows: (data) => stockRows(data).where((r) => r.data?['price_kobo'] != null && r.data?['available'] == true).toList(),
    line: (_) => 'Record a sale',
    more: 'Open stock',
  ),
  Tool(
    key: 'handover',
    title: 'Handover',
    icon: 'clock',
    perm: 'table.read',
    url: '/v1/chat-handover',
    rows: (_) => const [],
    line: (_) => 'Hand the shift over',
    staffOnly: true,
    more: 'Open stock',
  ),
  Tool(
    key: 'availability',
    title: 'Availability',
    icon: 'clock',
    perm: 'link.manage',
    url: '/v1/availability',
    rows: (data) {
      if (data is! Map<String, dynamic>) return const [];
      return [ToolRow(id: 'me', name: availabilityWord(data), sub: '', tags: [if (data['state'] != 'free') _str(data['state'])], data: data)];
    },
    line: (r) => r.isEmpty ? 'Free' : r.first.name,
    badge: (r) {
      final s = r.isEmpty ? null : r.first.data?['state'];
      return s != null && s != 'free' ? (text: s == 'off' ? 'Off' : 'Busy', tone: CardTone.warn) : null;
    },
    deliversOnly: true,
    more: 'Open chat',
  ),
  Tool(
    key: 'products',
    title: 'Products',
    icon: 'box',
    perm: 'table.read',
    url: '/v1/stock',
    rows: stockRows,
    line: (r) => _count(r.length, 'item'),
    chips: const [(label: 'Low stock', tag: 'low'), (label: 'Hidden', tag: 'hidden')],
    more: 'Open products',
    add: 'Add product',
  ),
  Tool(
    key: 'services',
    title: 'Services',
    icon: 'clock',
    perm: 'storefront.manage',
    url: '/v1/storefront/services',
    rows: (data) => [
      for (final s in _list(data, 'items'))
        ToolRow(
          id: _str(s['id']),
          name: _str(s['name']),
          sub: [
            _int(s['price_kobo']) != null ? naira(_int(s['price_kobo'])!) : 'Price on the day',
            if ((_int(s['duration_minutes']) ?? 0) > 0) '${_int(s['duration_minutes'])} min',
          ].join(' · '),
          pill: s['available'] == true ? null : (text: 'Hidden', tone: CardTone.plain),
          tags: [if (s['available'] != true) 'hidden'],
          data: s,
        ),
    ],
    line: (r) => _count(r.length, 'service'),
    more: 'Open services',
    add: 'Add service',
  ),
  Tool(
    key: 'stock',
    title: 'Stock',
    icon: 'store',
    perm: 'table.read',
    url: '/v1/stock',
    rows: (data) {
      // Every item, those with no count yet too (their first goods in).
      final rows = [for (final r in stockRows(data)) r.pill != null ? r : r.withPill((text: 'No count', tone: CardTone.plain))];
      // Low first, keeping their order otherwise.
      return [...rows.where((r) => r.tags.contains('low')), ...rows.where((r) => !r.tags.contains('low'))];
    },
    line: (_) => 'In the store',
    badge: _lowBadge,
    chips: const [(label: 'Low stock', tag: 'low')],
    more: 'Open stock',
  ),
  Tool(
    key: 'prices',
    title: 'Price lists',
    icon: 'tag',
    perm: 'storefront.manage',
    url: '/v1/price-lists',
    rows: (data) => [
      for (final l in _list(data, 'lists'))
        () {
          final items = (l['items'] as List?)?.length ?? 0;
          final links = (l['links'] as List?)?.length ?? 0;
          return ToolRow(
            id: _str(l['id']),
            name: _str(l['name']),
            sub: '${_count(items, 'item')} · ${links > 0 ? 'shared with ${_count(links, 'business', 'businesses')}' : 'not shared'}',
            tags: [if (links > 0) 'shared'],
            data: l,
          );
        }(),
    ],
    line: (r) => '${_tagged(r, 'shared')} shared',
    more: 'Open price lists',
  ),
  Tool(
    key: 'orders',
    title: 'Orders',
    icon: 'invoices',
    // Working orders is a cashier's too (order.handle).
    perm: 'storefront.manage|order.handle',
    url: '/v1/trade-orders',
    rows: (data) => [
      for (final o in _list(data, 'orders'))
        () {
          final delivery = o['delivery'] as Map<String, dynamic>?;
          final status = _str(o['status']);
          final total = naira(_int(o['total_kobo']) ?? 0);
          final area = _str(delivery?['drop_area']);
          return ToolRow(
            id: _str(o['id']),
            name: _str(o['order_reference']),
            sub: delivery != null
                ? '${_str(o['buyer_name'])} · Delivery${area.isNotEmpty ? ' to $area' : ''} · $total'
                : '${_str(o['buyer_name'])} · $total',
            pill: (
              text: delivery != null && status == 'received' ? 'Offered' : (_orderWord[status] ?? status),
              tone: status == 'received' ? CardTone.lime : (status == 'changed' ? CardTone.warn : CardTone.plain),
            ),
            tags: [if (status == 'received') 'new'],
            link: _str(o['link']).isEmpty ? null : _str(o['link']),
            data: o,
          );
        }(),
    ],
    line: (_) => 'From businesses',
    titleFor: (rows) => rows.isNotEmpty && rows.every((r) => r.data?['delivery'] != null) ? 'Deliveries' : 'Orders',
    rowIcon: 'invoices',
    badge: (r) {
      final n = _tagged(r, 'new');
      return n > 0 ? (text: '$n new', tone: CardTone.lime) : null;
    },
    chips: const [(label: 'New', tag: 'new')],
    more: 'Open orders',
  ),
  Tool(
    key: 'tables',
    title: 'Tables',
    icon: 'tables',
    perm: 'table.read',
    url: '/v1/tables',
    rows: (data) => [
      for (final t in _list(data, 'tables'))
        ToolRow(
          id: _str(t['id']),
          name: _str(t['title']).isNotEmpty ? _str(t['title']) : _str(t['name']),
          sub: _int(t['row_count']) != null ? _count(_int(t['row_count'])!, 'row') : _str(t['description']),
          tags: const [],
          data: t,
        ),
    ],
    line: (r) => _count(r.length, 'table'),
    more: 'Open tables',
  ),
  // The buyer's tools: the orders sent to suppliers, and their bills.
  Tool(
    key: 'purchases',
    title: 'Your orders',
    icon: 'orders',
    perm: 'table.read',
    url: '/v1/stock/purchases',
    rows: (data) => [
      for (final p in _list(data, 'purchases'))
        () {
          final st = purchaseState(p);
          final status = _str(p['status']);
          return ToolRow(
            id: _str(p['id']),
            name: _str(p['reference']),
            sub: '${_str(p['supplier'])} · ${naira(_int(p['total_kobo']) ?? 0)} · ${toolDay(_str(p['created_at']))}',
            pill: (text: st.word, tone: st.tone == CardTone.danger ? CardTone.warn : st.tone),
            tags: st.quote ? const ['quote'] : (status == 'received' || status == 'cancelled' ? const [] : const ['open']),
            data: p,
            link: _str(p['link_id']),
          );
        }(),
    ],
    line: (r) => _count(r.length, 'order'),
    rowIcon: 'orders',
    badge: (r) {
      final n = _tagged(r, 'quote');
      return n > 0 ? (text: '$n to confirm', tone: CardTone.warn) : null;
    },
    chips: const [(label: 'Open', tag: 'open'), (label: 'Quotes', tag: 'quote')],
    more: 'Open suppliers',
  ),
  Tool(
    key: 'bills',
    title: 'Bills',
    icon: 'money',
    perm: 'invoice.read',
    url: '/v1/bills?status=all',
    rows: (data) => [
      for (final b in _list(data, 'bills'))
        () {
          final due = _int(b['due_kobo']) ?? 0;
          final dueOn = b['due_on'] as String?;
          final status = _str(b['status']);
          return ToolRow(
            id: _str(b['id']),
            name: _str(b['reference']),
            sub:
                '${_str(b['supplier_name'])} · ${due > 0 ? '${naira(due)} to pay${dueOn != null ? ' · due ${toolDay(dueOn)}' : ''}' : naira(_int(b['amount_kobo']) ?? 0)}',
            pill: status == 'paid'
                ? (text: 'Paid', tone: CardTone.lime)
                : b['disputed'] == true
                ? (text: 'Questioned', tone: CardTone.warn)
                : status == 'void'
                ? (text: 'Cancelled', tone: CardTone.plain)
                : (text: 'To pay', tone: CardTone.warn),
            tags: [if (due > 0 && status != 'void') 'due'],
            data: b,
            link: b['link'] as String?,
          );
        }(),
    ],
    line: (r) => '${_tagged(r, 'due')} to pay',
    rowIcon: 'money',
    badge: (r) {
      final n = _tagged(r, 'due');
      return n > 0 ? (text: '$n to pay', tone: CardTone.warn) : null;
    },
    chips: const [(label: 'To pay', tag: 'due')],
    more: 'Open bills',
  ),
  // What the business is owed and the money it has.
  Tool(
    key: 'invoices',
    title: 'Invoices',
    icon: 'invoices',
    perm: 'invoice.read',
    url: '/v1/invoices?limit=200',
    rows: (data) => [
      for (final x in _list(data, 'invoices'))
        () {
          final s = _invoiceStatus[_str(x['status'])] ?? (_str(x['status']).replaceAll('_', ' '), CardTone.plain);
          return ToolRow(
            id: _str(x['id']),
            name: _str(x['number']),
            sub: _invoiceSub(x),
            pill: (text: s.$1, tone: s.$2),
            tags: [
              if (_owing(x)) 'owed',
              if (x['status'] == 'overdue') 'overdue',
              if (x['status'] == 'draft') 'draft',
              if (x['kind'] == 'credit') 'credit',
            ],
            words: [for (final l in (x['lines'] as List?) ?? const []) if (l is Map) _str(l['description'])].join(' '),
            data: x,
          );
        }(),
    ],
    line: (r) => '${_tagged(r, 'owed')} owed',
    rowIcon: 'invoices',
    badge: (r) {
      final n = _tagged(r, 'overdue');
      return n > 0 ? (text: '$n overdue', tone: CardTone.warn) : null;
    },
    chips: const [(label: 'Owed', tag: 'owed'), (label: 'Overdue', tag: 'overdue'), (label: 'Drafts', tag: 'draft')],
    more: 'Open invoices',
    add: 'New invoice',
  ),
  Tool(
    key: 'money',
    title: 'Money',
    icon: 'money',
    perm: 'invoice.read',
    url: '/v1/money/entries',
    rows: (data) => [
      for (final e in _list(data, 'entries'))
        ToolRow(
          id: _str(e['id']),
          name: _entryName(e),
          sub: _entrySub(e),
          pill: _entryPill(e),
          tags: [_str(e['direction']), if (e['to_match'] == true) 'match'],
          category: e['category'] as String?,
          words: [e['vendor'], e['reference'], e['towards']].whereType<String>().where((w) => w.isNotEmpty).join(' '),
          data: e,
        ),
    ],
    line: (r) => _tagged(r, 'match') > 0 ? '${_tagged(r, 'match')} to match' : 'In and out',
    rowIcon: 'money',
    badge: (r) {
      final n = _tagged(r, 'match');
      return n > 0 ? (text: '$n to match', tone: CardTone.warn) : null;
    },
    chips: const [(label: 'In', tag: 'in'), (label: 'Out', tag: 'out'), (label: 'To match', tag: 'match')],
    more: 'Open money',
    add: 'Add cash or expense',
  ),
];

/// "Free", "Busy till 15:30", "Busy, 2 deliveries", "Off today".
String availabilityWord(Map<String, dynamic> a, [DateTime? now]) {
  switch (a['state']) {
    case 'off':
      return 'Off today';
    case 'busy':
      final until = DateTime.tryParse(_str(a['until']))?.toLocal();
      // time.ts shortWhen: the clock today; the day too when it is not
      // (busy late at night runs past midnight).
      if (until != null) {
        final n = now ?? DateTime.now();
        return 'Busy till ${dayWords(until, n) == 'Today' ? '${until.hour.toString().padLeft(2, '0')}:${until.minute.toString().padLeft(2, '0')}' : whenWords(until, n)}';
      }
      final open = _int(a['open']) ?? 0;
      if (open > 0) return 'Busy, $open ${open == 1 ? 'delivery' : 'deliveries'}';
      return 'Busy';
  }
  return 'Free';
}

/// Staff who work from chat see their role's tools only (chat-tools.md §7).
const roleToolKeys = {
  'cashier': ['sell', 'orders', 'stock'],
  'shift_manager': ['handover', 'sell', 'orders', 'stock', 'bills'],
};

/// The tools a person's roles shape them to, or null for all.
Set<ToolKey>? roleTools(List<String>? roles) {
  if (roles == null || roles.isEmpty || roles.any((r) => !roleToolKeys.containsKey(r))) return null;
  return {for (final r in roles) ...?roleToolKeys[r]};
}

/// The tools a business's conversation shows (business-chat-tools.md §3).
const businessTools = ['purchases', 'orders', 'bills', 'invoices', 'prices'];

/// The tools a person may open, matching what follows "/" (a word's
/// start), shaped to their role when they are staff.
List<Tool> toolsFor(bool Function(String) can, {String query = '', List<String>? roles, bool delivers = false}) {
  final q = query.trim().toLowerCase();
  final only = roleTools(roles);
  return [
    for (final t in tools)
      if (can(t.perm) &&
          (!t.deliversOnly || delivers) &&
          (only != null ? only.contains(t.key) : !t.staffOnly) &&
          (q.isEmpty || t.title.toLowerCase().split(RegExp(r'\s+')).any((w) => w.startsWith(q)) || t.key.startsWith(q)))
        t,
  ];
}

/// The rows a sheet shows: those a chip picks, then those its search
/// finds, by name first, then by the rest (its line, category, words).
List<ToolRow> filterRows(List<ToolRow> rows, String? chip, String search) {
  final q = search.trim().toLowerCase();
  final picked = rows.where((r) => chip == null || r.tags.contains(chip)).toList();
  if (q.isEmpty) return picked;
  bool named(ToolRow r) => r.name.toLowerCase().contains(q);
  return [
    ...picked.where(named),
    ...picked.where((r) => !named(r) && [r.sub, r.category ?? '', r.words ?? ''].any((t) => t.toLowerCase().contains(q))),
  ];
}

/// tone.ts tileLetters: a row's tile, "JR" for "Jollof rice", "PP" for "Pepper".
String tileLetters(String name) {
  final words = name.split(RegExp(r'\s+')).where((w) => RegExp(r'^\p{L}', unicode: true).hasMatch(w)).toList();
  if (words.isEmpty) return name.isEmpty ? '' : String.fromCharCode(name.runes.first).toUpperCase();
  final a = words[0];
  if (words.length > 1) return (String.fromCharCode(a.runes.first) + String.fromCharCode(words[1].runes.first)).toUpperCase();
  final rest = a.length > 1 ? a.substring(1) : '';
  final next = RegExp(r'[b-df-hj-np-tv-z]', caseSensitive: false).firstMatch(rest)?.group(0) ?? (rest.isEmpty ? '' : rest[0]);
  return (a[0] + next).toUpperCase();
}
