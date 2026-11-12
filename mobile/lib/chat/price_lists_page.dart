// The seller's price lists (M6 part R; web/src/pages/PriceLists.tsx
// StorefrontPriceLists, ListItemsModal, ShareModal, LinkPricesModal,
// OffersCard; lib/deals.ts breaksProblem), opened from Prices' "Open price
// lists": lists built on the business's own stock items, shared with
// linked buyers; one business's own prices; and how far staff may agree
// to an offer.

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'chats.dart' show LinkSection;
import 'product_form.dart' show hasSizes, sizesWords;
import 'sizes_forms.dart' show choicesOf, sizesOf, variantLabel, variantOf;
import 'stock_page.dart' show feeInput, feeKobo;
import 'words.dart' show naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int? _int(Object? v) => (v as num?)?.toInt();

const maxBreaks = 5;

/// deals.ts breaksProblem: each a larger quantity than the last (and than
/// its minimum), each a lower price; "" when none.
String breaksProblem(int min, int price, List<({int? qty, int? price})> breaks) {
  if (breaks.length > maxBreaks) return 'Up to $maxBreaks quantity prices.';
  var q = min < 1 ? 1 : min;
  var p = price;
  for (final b in breaks) {
    if (b.qty == null || b.qty! <= q) return 'Each quantity is more than the one before it.';
    if (b.price == null || b.price! <= 0 || b.price! >= p) return 'Each price is lower than the one before it.';
    q = b.qty!;
    p = b.price!;
  }
  return '';
}

class PriceListsPage extends StatefulWidget {
  const PriceListsPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<PriceListsPage> createState() => _PriceListsPageState();
}

class _PriceListsPageState extends State<PriceListsPage> {
  List<Map<String, dynamic>>? _lists;
  List<Map<String, dynamic>> _active = const [];
  String? _error;
  bool _busy = false;
  final _name = TextEditingController();

  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _name.addListener(() => setState(() {}));
    _load();
  }

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  String _why(Object e) => e is ApiError ? e.message : 'You’re offline. This shows when you’re back online.';

  Future<void> _load() async {
    try {
      final r = await _t.send('GET', '/v1/price-lists');
      if (mounted) setState(() => (_lists = _maps((r as Map)['lists']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = _why(e));
    }
    try {
      final r = await _t.send('GET', '/v1/links');
      if (mounted) setState(() => _active = _maps((r as Map)['links']).where((l) => l['state'] == 'active').toList());
    } on Object {
      // Offline: no names, "A business".
    }
  }

  String _nameOf(String id) => _active.where((l) => l['id'] == id).map((l) => l['name'] as String? ?? '').firstOrNull ?? 'A business';

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _error = null));
    try {
      await f();
      if (mounted) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _error = _why(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _modal(String title, Widget Function(BuildContext) form) async {
    final saved = await showTkModal<bool>(context, title: title, builder: form);
    if (saved == true) await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final lists = _lists;
    return TkPageScreen(
      title: 'Price lists',
      onRefresh: _load,
      description: 'The prices linked businesses that buy from you see, from your own stock items. Share a list with them, and give one their own prices.',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (lists == null && _error == null) Text('Loading your price lists…', style: tkHint(t)),
        if (lists != null) ...[
          TkPageCard(
            title: 'Your price lists',
            children: [
              if (lists.isEmpty)
                Text('No price lists yet. Make one, add items from your stock, and share it with the businesses that buy from you.', style: tkHint(t))
              else
                LinkSection(
                  top: 0,
                  rows: [
                    for (final l in lists)
                      (
                        letter: null,
                        icon: 'money',
                        tone: null,
                        title: l['name'] as String? ?? '',
                        badge: null,
                        badgeTone: null,
                        sub:
                            '${_maps(l['items']).length} item${_maps(l['items']).length == 1 ? '' : 's'} · ${((l['links'] as List?) ?? const []).isEmpty ? 'not shared' : 'shared with ${((l['links'] as List?) ?? const []).whereType<String>().map(_nameOf).join(', ')}'}',
                        actions: Wrap(
                          spacing: 8,
                          runSpacing: 8,
                          children: [
                            TkSmallButton(
                              label: 'Items',
                              semantics: 'Items of ${l['name']}',
                              onPressed: () => _modal('Items of ${l['name']}', (_) => _ListItems(auth: widget.auth, list: l)),
                            ),
                            TkSmallButton(
                              label: 'Share',
                              semantics: 'Share ${l['name']}',
                              onPressed: () => _modal('Share ${l['name']}', (_) => _Share(auth: widget.auth, list: l, links: _active)),
                            ),
                            TkSmallButton(
                              label: 'Delete',
                              danger: true,
                              semantics: 'Delete ${l['name']}',
                              onPressed: _busy
                                  ? null
                                  : () async {
                                      final yes = await showTkConfirm(
                                        context,
                                        title: 'Delete ${l['name']}?',
                                        body: 'The businesses it is shared with no longer see it. Open orders keep their prices.',
                                        ok: 'Delete',
                                        danger: true,
                                      );
                                      if (yes) await _run(() async => _t.send('DELETE', '/v1/price-lists/${l['id']}'), 'Deleted');
                                    },
                            ),
                          ],
                        ),
                      ),
                  ],
                ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: Semantics(
                      label: 'New price list’s name',
                      child: TkTextBox(controller: _name, hint: 'Trade prices', height: 44, size: 16, max: 80),
                    ),
                  ),
                  const SizedBox(width: 8),
                  TkSmallButton(
                    label: 'Make a price list',
                    primary: true,
                    onPressed: _busy || _name.text.trim().isEmpty
                        ? null
                        : () => _run(() async {
                            await _t.send('POST', '/v1/price-lists', body: {'name': _name.text.trim()});
                            _name.clear();
                          }, 'Price list made'),
                  ),
                ],
              ),
            ],
          ),
          if (lists.any((l) => ((l['links'] as List?) ?? const []).isNotEmpty))
            TkPageCard(
              title: 'A business’s own prices',
              children: [
                Text('Give one business its own price for an item on its list.', style: tkHint(t)),
                LinkSection(
                  top: 8,
                  rows: [
                    for (final l in lists)
                      for (final id in ((l['links'] as List?) ?? const []).whereType<String>())
                        (
                          letter: _nameOf(id),
                          icon: null,
                          tone: null,
                          title: _nameOf(id),
                          badge: null,
                          badgeTone: null,
                          sub: l['name'] as String? ?? '',
                          actions: TkSmallButton(
                            label: 'Their prices',
                            semantics: 'Their prices for ${_nameOf(id)}',
                            onPressed: () => _modal('${_nameOf(id)}’s prices', (_) => _LinkPrices(auth: widget.auth, link: id, name: _nameOf(id))),
                          ),
                        ),
                  ],
                ),
              ],
            ),
          _Offers(key: const ValueKey('offers'), auth: widget.auth),
        ],
      ],
    );
  }
}

/// The modal's Save and Cancel, Save first (`.row-actions`).
Widget _saveCancel(BuildContext context, {required VoidCallback? save, bool busy = false}) => Wrap(
  spacing: 8,
  children: [
    TkButton(label: 'Save', primary: true, enabled: !busy, onPressed: save),
    TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop(false)),
  ],
);

class _Row {
  _Row(this.item, Map<String, dynamic>? on, VoidCallback changed)
    : on = on != null,
      price = TextEditingController(text: feeInput(_int(on?['price_kobo']) ?? _int(item['price_kobo']) ?? 0)),
      min = TextEditingController(text: '${_int(on?['min_quantity']) ?? 1}'),
      available = on?['available'] as bool? ?? true,
      breaks = [
        for (final b in _maps(on?['breaks']))
          (qty: TextEditingController(text: '${b['min_quantity']}'), price: TextEditingController(text: feeInput(_int(b['price_kobo']) ?? 0))),
      ],
      sizePrices = {
        for (final z in _maps(on?['sizes']))
          if ((_int(z['price_kobo']) ?? 0) > 0) z['item_id'] as String: TextEditingController(text: feeInput(_int(z['price_kobo'])!)),
      } {
    for (final c in [
      price,
      min,
      for (final b in breaks) ...[b.qty, b.price],
      ...sizePrices.values,
    ]) {
      c.addListener(changed);
    }
  }
  final Map<String, dynamic> item;
  bool on;
  final TextEditingController price;
  final TextEditingController min;
  bool available;
  final List<({TextEditingController qty, TextEditingController price})> breaks;
  final Map<String, TextEditingController> sizePrices;

  List<({int? qty, int? price})> get typedBreaks => [
    for (final b in breaks) (qty: RegExp(r'^\d+$').hasMatch(b.qty.text) ? int.parse(b.qty.text) : null, price: feeKobo(b.price.text)),
  ];
}

/// PriceLists.tsx ListItemsModal: which of the business's own items are
/// on the list, at what price, at least how many, whether available, a
/// product's sizes' own prices, and lower prices for more.
class _ListItems extends StatefulWidget {
  const _ListItems({required this.auth, required this.list});
  final Auth auth;
  final Map<String, dynamic> list;

  @override
  State<_ListItems> createState() => _ListItemsState();
}

class _ListItemsState extends State<_ListItems> {
  List<_Row>? _rows;
  List<Map<String, dynamic>> _all = const [];
  String? _error;
  bool _busy = false;
  final _openSizes = <String>{};

  void _changed() => setState(() {});

  @override
  void initState() {
    super.initState();
    widget.auth.transport
        .send('GET', '/v1/stock')
        .then(
          (r) {
            if (!mounted) return;
            final items = _maps((r as Map)['items']);
            final on = _maps(widget.list['items']);
            setState(() {
              _all = items;
              _rows = [for (final it in items.where((x) => x['variant_of'] == null)) _Row(it, on.where((x) => x['item_id'] == it['id']).firstOrNull, _changed)];
            });
          },
          onError: (Object e) {
            if (mounted) {
              setState(
                () => _error = e is ApiError && e.status == 409
                    ? 'Keep your stock on Taskiem first: its items are what a price list sells.'
                    : (e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.'),
              );
            }
          },
        );
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send(
        'PUT',
        '/v1/price-lists/${widget.list['id']}/items',
        body: {
          'items': [
            for (final r in _rows!.where((r) => r.on))
              {
                'item_id': r.item['id'],
                'price_kobo': feeKobo(r.price.text),
                'min_quantity': int.parse(r.min.text),
                'available': r.available,
                'breaks': [
                  for (final b in r.typedBreaks) {'min_quantity': b.qty, 'price_kobo': b.price},
                ],
                if (hasSizes(r.item))
                  'sizes': [
                    for (final e in r.sizePrices.entries)
                      if (e.value.text.trim().isNotEmpty) {'item_id': e.key, 'price_kobo': feeKobo(e.value.text)},
                  ],
              },
          ],
        },
      );
      if (!mounted) return;
      showTkToast(context, 'Saved. The businesses it is shared with see it now.');
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
    final rows = _rows;
    bool badRow(_Row r) =>
        r.on &&
        (!((feeKobo(r.price.text) ?? 0) > 0) ||
            !RegExp(r'^\d+$').hasMatch(r.min.text) ||
            int.parse(r.min.text) < 1 ||
            r.sizePrices.values.any((p) => p.text.trim().isNotEmpty && !((feeKobo(p.text) ?? 0) > 0)));
    final bad = rows?.where(badRow).firstOrNull;
    final badBreaks = rows
        ?.where((r) => r.on && bad == null)
        .map((r) => (r: r, why: breaksProblem(int.parse(r.min.text), feeKobo(r.price.text)!, r.typedBreaks)))
        .where((x) => x.why.isNotEmpty)
        .firstOrNull;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (_error != null) Padding(padding: const EdgeInsets.only(bottom: 10), child: TkErrorBox(_error!)),
        if (rows == null && _error == null) Text('Loading your items…', style: tkHint(t)),
        if (rows != null && rows.isEmpty) Text('No stock items yet. Add them on the Stock page.', style: tkHint(t)),
        if (rows != null)
          for (final r in rows)
            Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  TkChoiceRow(
                    radio: false,
                    control: TkCheckbox(checked: r.on),
                    label: Text(
                      '${r.item['name']}${(r.item['unit'] as String? ?? '').isNotEmpty ? ' (${r.item['unit']})' : ''}',
                      style: tkText(size: 15, weight: 600, color: t.text),
                    ),
                    onTap: () => setState(() => r.on = !r.on),
                  ),
                  if (r.on)
                    Padding(
                      padding: const EdgeInsets.only(left: 32),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          Row(
                            children: [
                              Expanded(
                                child: TkKitField(
                                  label: 'Price (₦)',
                                  child: TkTextBox(controller: r.price, money: true, height: 44, size: 16),
                                ),
                              ),
                              const SizedBox(width: 10),
                              SizedBox(
                                width: 100,
                                child: TkKitField(
                                  label: 'At least',
                                  child: TkTextBox(controller: r.min, digits: 6, height: 44, size: 16),
                                ),
                              ),
                            ],
                          ),
                          const SizedBox(height: 8),
                          TkChoiceRow(
                            radio: false,
                            control: TkCheckbox(checked: r.available),
                            label: Text('Available', style: tkText(size: 15, color: t.text)),
                            onTap: () => setState(() => r.available = !r.available),
                          ),
                          if (hasSizes(r.item)) ...[
                            TkLink(
                              label: '${sizesWords(sizesOf(_all, r.item['id'] as String).length)}: some cost more or less',
                              onTap: () =>
                                  setState(() => _openSizes.contains(r.item['id']) ? _openSizes.remove(r.item['id']) : _openSizes.add(r.item['id'] as String)),
                            ),
                            if (_openSizes.contains(r.item['id'])) ...[
                              Text('Each goes on the list at the price above unless you give it its own.', style: tkHint(t)),
                              for (final z in sizesOf(_all, r.item['id'] as String))
                                Padding(
                                  padding: const EdgeInsets.only(top: 6),
                                  child: TkKitField(
                                    label: '${variantLabel(variantOf(z), choicesOf(r.item))} (₦)',
                                    child: TkTextBox(
                                      controller: r.sizePrices.putIfAbsent(z['id'] as String, () => TextEditingController()..addListener(_changed)),
                                      money: true,
                                      hint: r.price.text,
                                      height: 44,
                                      size: 16,
                                    ),
                                  ),
                                ),
                            ],
                          ],
                          for (var k = 0; k < r.breaks.length; k++)
                            Semantics(
                              container: true,
                              label: '${r.item['name']}: quantity price ${k + 1}',
                              explicitChildNodes: true,
                              child: Padding(
                                padding: const EdgeInsets.only(top: 8),
                                child: Row(
                                  crossAxisAlignment: CrossAxisAlignment.end,
                                  children: [
                                    SizedBox(
                                      width: 90,
                                      child: TkKitField(
                                        label: 'From',
                                        child: TkTextBox(controller: r.breaks[k].qty, digits: 6, height: 44, size: 16),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: TkKitField(
                                        label: 'Price (₦)',
                                        child: TkTextBox(controller: r.breaks[k].price, money: true, height: 44, size: 16),
                                      ),
                                    ),
                                    const SizedBox(width: 8),
                                    TkSmallButton(
                                      label: 'Remove',
                                      semantics: 'Remove quantity price ${k + 1} of ${r.item['name']}',
                                      onPressed: () => setState(() => r.breaks.removeAt(k)),
                                    ),
                                  ],
                                ),
                              ),
                            ),
                          if (r.breaks.length < maxBreaks)
                            Padding(
                              padding: const EdgeInsets.only(top: 8),
                              child: Align(
                                alignment: Alignment.centerLeft,
                                child: TkSmallButton(
                                  label: '+ Lower price for more',
                                  semantics: '+ Lower price for more of ${r.item['name']}',
                                  onPressed: () => setState(() {
                                    final b = (qty: TextEditingController()..addListener(_changed), price: TextEditingController()..addListener(_changed));
                                    r.breaks.add(b);
                                  }),
                                ),
                              ),
                            ),
                        ],
                      ),
                    ),
                ],
              ),
            ),
        if (bad != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text('${bad.item['name']} needs a price and a whole number for “At least”.', style: tkHint(t)),
          ),
        if (badBreaks != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Semantics(liveRegion: true, child: Text('${badBreaks.r.item['name']}: ${badBreaks.why}', style: tkHint(t))),
          ),
        _saveCancel(context, busy: _busy, save: rows == null || bad != null || badBreaks != null ? null : _save),
      ],
    );
  }
}

/// PriceLists.tsx ShareModal: which linked businesses see a list.
class _Share extends StatefulWidget {
  const _Share({required this.auth, required this.list, required this.links});
  final Auth auth;
  final Map<String, dynamic> list;
  final List<Map<String, dynamic>> links;

  @override
  State<_Share> createState() => _ShareState();
}

class _ShareState extends State<_Share> {
  late final _picked = ((widget.list['links'] as List?) ?? const []).whereType<String>().toList();
  bool _busy = false;
  String? _error;

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('PUT', '/v1/price-lists/${widget.list['id']}/links', body: {'links': _picked});
      if (!mounted) return;
      showTkToast(context, 'Shared');
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
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('A business sees one price list: sharing this one with a business replaces the one it had.', style: tkHint(t)),
        const SizedBox(height: 12),
        if (widget.links.isEmpty)
          Text('No linked businesses yet. Link with them on the Chat page.', style: tkHint(t))
        else
          for (final l in widget.links)
            TkChoiceRow(
              radio: false,
              control: TkCheckbox(checked: _picked.contains(l['id'])),
              label: Text(l['name'] as String? ?? '', style: tkText(size: 15, color: t.text)),
              onTap: () => setState(() => _picked.contains(l['id']) ? _picked.remove(l['id']) : _picked.add(l['id'] as String)),
            ),
        if (_error != null) Padding(padding: const EdgeInsets.only(bottom: 10), child: TkErrorBox(_error!)),
        _saveCancel(context, busy: _busy, save: _save),
      ],
    );
  }
}

/// PriceLists.tsx LinkPricesModal: one business's own prices over its
/// list's.
class _LinkPrices extends StatefulWidget {
  const _LinkPrices({required this.auth, required this.link, required this.name});
  final Auth auth;
  final String link;
  final String name;

  @override
  State<_LinkPrices> createState() => _LinkPricesState();
}

class _LinkPricesState extends State<_LinkPrices> {
  List<Map<String, dynamic>>? _items;
  final _prices = <String, TextEditingController>{};
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    widget.auth.transport
        .send('GET', '/v1/link-prices/${widget.link}')
        .then(
          (r) {
            if (!mounted) return;
            final items = _maps((r as Map)['items']);
            setState(() {
              _items = items;
              for (final it in items) {
                _prices[it['item_id'] as String] = TextEditingController(text: it['overridden'] == true ? feeInput(_int(it['price_kobo']) ?? 0) : '')
                  ..addListener(() => setState(() {}));
              }
            });
          },
          onError: (Object e) {
            if (mounted) setState(() => _error = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.');
          },
        );
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send(
        'PUT',
        '/v1/link-prices/${widget.link}',
        body: {
          'prices': {
            for (final e in _prices.entries)
              if (e.value.text.trim().isNotEmpty) e.key: feeKobo(e.value.text),
          },
        },
      );
      if (!mounted) return;
      showTkToast(context, '${widget.name}’s prices saved');
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
    final items = _items;
    final bad = _prices.values.any((p) => p.text.trim().isNotEmpty && !((feeKobo(p.text) ?? 0) > 0));
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('Leave a price empty for the list’s.', style: tkHint(t)),
        const SizedBox(height: 12),
        if (_error != null) Padding(padding: const EdgeInsets.only(bottom: 10), child: TkErrorBox(_error!)),
        if (items == null && _error == null) Text('Loading their prices…', style: tkHint(t)),
        if (items != null)
          for (final it in items)
            Padding(
              padding: const EdgeInsets.only(bottom: 10),
              child: TkKitField(
                label:
                    '${it['name']} (list: ${naira(items.where((x) => x['item_id'] == it['item_id'] && x['overridden'] != true).map((x) => _int(x['price_kobo'])).firstOrNull ?? _int(it['price_kobo']) ?? 0)})',
                child: TkTextBox(controller: _prices[it['item_id']]!, money: true, hint: 'The list’s', height: 44, size: 16),
              ),
            ),
        _saveCancel(context, busy: _busy, save: items == null || bad ? null : _save),
      ],
    );
  }
}

/// PriceLists.tsx OffersCard: the largest discount staff may agree on a
/// whole order; under it, or under an item's lowest price, an offer waits
/// for the owner.
class _Offers extends StatefulWidget {
  const _Offers({super.key, required this.auth});
  final Auth auth;

  @override
  State<_Offers> createState() => _OffersState();
}

class _OffersState extends State<_Offers> {
  Map<String, dynamic>? _view;
  final _pct = TextEditingController();

  /// What is saved, as the box shows it: Save is for a change from it.
  String _saved = '';
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _pct.addListener(() => setState(() {}));
    _load();
  }

  Future<void> _load() async {
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/trade-floors') as Map).cast<String, dynamic>();
      if (!mounted) return;
      setState(() {
        _view = r;
        _pct.text = _saved = (_int(r['max_discount_pct']) ?? 0) > 0 ? '${r['max_discount_pct']}' : '';
      });
    } on Object catch (e) {
      if (mounted) setState(() => _error = e is ApiError ? e.message : 'You’re offline. This shows when you’re back online.');
    }
  }

  @override
  void dispose() {
    _pct.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final v = _pct.text.trim();
    final n = v.isEmpty ? null : int.tryParse(v);
    final bad = v.isNotEmpty && (n == null || n < 1 || n > 90);
    final floors = ((_view?['floors'] as Map?) ?? const {}).length;
    return TkPageCard(
      title: 'Offers from buyers',
      children: [
        Text(
          'Buyers can offer their own price; you and your staff accept, counter or decline it in the chat. Staff agree down to each product’s lowest price '
          '(${floors == 0 ? 'none set yet' : '$floors set'}, on the product) and this discount. Under them, the offer waits for your OK.',
          style: tkHint(t),
        ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 10), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Row(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Expanded(
              child: TkKitField(
                label: 'Largest discount staff can agree (%)',
                child: TkTextBox(controller: _pct, digits: 2, hint: 'No limit', height: 44, size: 16),
              ),
            ),
            const SizedBox(width: 8),
            TkSmallButton(
              label: 'Save',
              primary: true,
              onPressed: _busy || _view == null || bad || v == _saved
                  ? null
                  : () async {
                      setState(() => (_busy = true, _error = null));
                      try {
                        await widget.auth.transport.send('PUT', '/v1/trade-floors', body: {'set_discount': true, 'max_discount_pct': n});
                        if (mounted) showTkToast(this.context, n == null ? 'No discount limit' : 'Staff can agree up to $n% off');
                        await _load();
                      } on Object catch (e) {
                        if (mounted) setState(() => _error = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.');
                      }
                      if (mounted) setState(() => _busy = false);
                    },
            ),
          ],
        ),
        if (bad)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Semantics(liveRegion: true, child: Text('A whole number from 1 to 90.', style: tkHint(t))),
          ),
      ],
    );
  }
}
