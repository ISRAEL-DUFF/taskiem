// An order from a linked business (M5 part O; web/src/pages/ChatBuyer.tsx
// OrderSheet, the Buyer design): their price list or their storefront,
// picked with − and + (a product's sizes in a grid), the bag in pine at the
// foot, then the review: which of our suppliers they are, how each line is
// treated (what a business buys §6: the question, asked of what is not
// remembered), a note, Make an offer, and Place order or Ask for a quote.
// Placing drafts the order at their prices and sends it, each step once
// however often it is tried again.

import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show ItemMemory, PutItemLinkRequest;
import '../api/transport.dart';
import '../kit/motion.dart';
import '../kit/api_image.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/mobile.dart' show tkInitials;
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../kit/tap_area.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'buyer.dart';
import 'chats.dart';
import 'deal.dart' show dealKobo, offWords;
import 'tools.dart' show categoryTag;
import 'treat_line.dart';
import 'words.dart' show naira;

/// Where the sheet starts: an item in the bag, a past order's lines, a
/// category picked in "@", or Make an offer from its tile.
class OrderStart {
  const OrderStart({this.itemId, this.last, this.category, this.offer = false, this.qty});
  final String? itemId;

  /// What goes in the bag at first (an alert's Reorder: the low item, or
  /// its low sizes), by their item.
  final Map<String, int>? qty;

  /// Reorder: that order's lines, as their list has them now.
  final Map<String, dynamic>? last;
  final String? category;
  final bool offer;
}

/// Opens the order sheet; the future ends with what was sent, said.
Future<String?> showOrderSheet(
  BuildContext context, {
  required ChatsData chats,
  required String link,
  required String business,
  OrderStart? start,
  bool browse = false,
  void Function(String words)? onSent,
}) => showTkSheet<String>(
  context,
  (_) => OrderSheet(chats: chats, link: link, business: business, start: start, browse: browse, onSent: onSent),
);

class OrderSheet extends StatefulWidget {
  const OrderSheet({super.key, required this.chats, required this.link, required this.business, this.start, this.browse = false, this.onSent});
  final ChatsData chats;
  final String link;
  final String business;
  final OrderStart? start;

  /// Someone who may not order looks around their shop: no bag, no
  /// steppers.
  final bool browse;

  /// Sent (an order, an offer, a quote asked), with its words: told even
  /// when the sheet was closed while it was sending.
  final void Function(String words)? onSent;

  @override
  State<OrderSheet> createState() => _OrderSheetState();
}

typedef _Load<T> = ({T? data, Object? error});

int _int(Object? v) => (v as num?)?.toInt() ?? 0;

class _OrderSheetState extends State<OrderSheet> {
  Transport get _t => widget.chats.auth.transport;
  String get _biz => widget.business;
  String get _enc => Uri.encodeComponent(widget.link);

  // What the sheet reads.
  _Load<Map<String, dynamic>?> _prices = (data: null, error: null);
  bool _pricesRead = false;
  List<Map<String, dynamic>>? _links;
  List<Map<String, dynamic>>? _suppliers;
  Map<String, dynamic>? _last;
  List<Map<String, dynamic>>? _own;
  bool _stockOff = false;

  // What the person does.
  late Map<String, int> _qty = {...?widget.start?.qty};
  bool _seeded = false;
  bool _review = false;
  final _search = TextEditingController();
  String? _chip;
  final _note = TextEditingController();
  late bool _offering = widget.start?.offer ?? false;
  final _offer = TextEditingController();
  String? _who;
  // How each line is treated, by their item, as the person said it; and
  // those to remember for next time.
  final Map<String, Treat> _treat = {};
  final Set<String> _remember = {};
  final Set<String> _many = {};
  late final TreatChoices _choices = TreatChoices(widget.chats.auth);
  String? _grid;
  bool _busy = false;
  bool _starting = false;
  String? _stockError;
  String? _error;
  String? _done;

  // What placing has made so far: a retry does each step once.
  String? _madeSupplier;
  bool _linked = false;
  final Map<String, Map<String, dynamic>> _madeOrders = {};
  final Map<String, String> _keys = {};
  final _rand = math.Random();
  String _keyOf(String what) => _keys[what] ??= 'chat-${DateTime.now().microsecondsSinceEpoch}-${_rand.nextInt(1 << 30)}';

  @override
  void initState() {
    super.initState();
    _chip = widget.start?.category == null ? null : categoryTag(widget.start!.category!);
    _grid = widget.start?.itemId;
    for (final c in [_search, _offer]) {
      c.addListener(() => setState(() {}));
    }
    _choices.addListener(_changed);
    _load();
    unawaited(_choices.load());
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _choices.removeListener(_changed);
    for (final c in [_search, _note, _offer]) {
      c.dispose();
    }
    super.dispose();
  }

  List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();

  Future<void> _load() async {
    unawaited(_loadPrices());
    unawaited(() async {
      try {
        final r = await _t.send('GET', '/v1/item-links/$_enc') as Map<String, dynamic>;
        _links = _maps(r['item_links']);
      } on Object {
        _links = [];
      }
      _seed();
    }());
    unawaited(() async {
      try {
        final r = await _t.send('GET', '/v1/stock/suppliers') as Map<String, dynamic>;
        if (mounted) setState(() => _suppliers = _maps(r['suppliers']));
      } on Object {
        // Placing waits for it; the error shows when placing.
      }
    }());
    unawaited(() async {
      try {
        final r = await _t.send('GET', '/v1/stock/purchases') as Map<String, dynamic>;
        final mine = _maps(r['purchases'])
            .where((p) => p['link_id'] == widget.link && p['status'] != 'cancelled' && p['seller_status'] != 'proposed')
            .toList()
          ..sort((a, b) => (b['created_at'] as String? ?? '').compareTo(a['created_at'] as String? ?? ''));
        _last = mine.firstOrNull;
      } on Object {
        _last = null;
      }
      _seed();
    }());
    unawaited(_loadStock());
  }

  Future<void> _loadPrices() async {
    try {
      final r = await _t.send('GET', '/v1/supplier-prices', query: {'link': widget.link}) as Map<String, dynamic>;
      _prices = (data: _maps(r['suppliers']).firstOrNull, error: null);
      _pricesRead = true;
    } on ApiError catch (e) {
      _prices = (data: null, error: e.message);
    } on Offline {
      _prices = (data: null, error: 'You’re offline. Try again when you’re back online.');
    }
    _seed();
  }

  Future<void> _loadStock() async {
    try {
      final r = await _t.send('GET', '/v1/stock') as Map<String, dynamic>;
      _own = _maps(r['items']);
      _stockOff = r['keeps_stock'] == false;
    } on Object {
      _own = [];
      _stockOff = false;
    }
    if (mounted) setState(() {});
  }

  List<PriceItem> get _items => [for (final j in _maps(_prices.data?['items'])) PriceItem.fromJson(j)];

  /// The start's item in the bag, or the last order's lines, once what
  /// they need is read.
  void _seed() {
    if (!mounted) return;
    setState(() {
      final s = widget.start;
      if (_seeded || _prices.data == null || _links == null || s == null || (s.itemId == null && s.last == null)) return;
      if (s.last != null) _qty = reorderQty(_maps(s.last!['lines']), _links!, _items);
      final it = _items.where((x) => x.id == s.itemId).firstOrNull;
      if (it != null && !soldInSizes(it) && (_qty[it.id] ?? 0) == 0) _qty = {..._qty, it.id: math.max(1, it.min)};
      _seeded = true;
    });
  }

  /// What the business remembers of their item, if anything.
  ItemMemory? _memory(String sellerItem) {
    final m = _links?.where((l) => l['seller_item_id'] == sellerItem).firstOrNull;
    return m == null ? null : ItemMemory.fromJson(m);
  }

  /// How a line of theirs is treated: as said here, as remembered, else as
  /// Taskiem guesses.
  Treat _treatOf(PriceItem it) {
    final m = _memory(it.id);
    return _treat[it.id] ?? (m != null ? Treat.ofMemory(m) : _choices.suggest(it.name));
  }

  /// Whether the line is sent with its treatment: not remembered, or
  /// changed for this order.
  bool _says(PriceItem it) {
    final m = _memory(it.id);
    return m == null || !_treatOf(it).same(Treat.ofMemory(m));
  }

  Map<String, dynamic>? get _theirs => _suppliers?.where((x) => x['link_id'] == widget.link).firstOrNull;
  List<Map<String, dynamic>> get _unlinkedSuppliers => (_suppliers ?? const []).where((x) => x['link_id'] == null || x['link_id'] == '').toList();
  String get _supplierFor =>
      _theirs?['id'] as String? ??
      _who ??
      _unlinkedSuppliers.where((x) => (x['name'] as String? ?? '').toLowerCase() == _biz.toLowerCase()).map((x) => x['id'] as String).firstOrNull ??
      'new';

  Future<Object?> _postOnce(String path, String key, Object body) => _t.send('POST', path, body: body, headers: {'Idempotency-Key': _keyOf(key)});

  /// Sent: the tick and its words, then the sheet closes (busy still, so
  /// nothing under the tick is pressed again). Closed meanwhile, the words
  /// are said where the sheet was opened.
  void _finish(String words) {
    widget.onSent?.call(words);
    if (!mounted) return;
    setState(() => _done = words);
    Timer(const Duration(milliseconds: 1300), () {
      if (mounted) Navigator.of(context).pop(words);
    });
  }

  Future<void> _place(Bag bag, int? offerKobo) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      if (_theirs == null && !_linked) {
        if (_supplierFor == 'new') {
          // A new supplier made from the link alone: no number asked, their
          // orders reach them through the link.
          if (_madeSupplier == null) {
            final out = await _postOnce('/v1/stock/suppliers', 'supplier', {'name': _biz, 'link_id': widget.link}) as Map<String, dynamic>;
            _madeSupplier = (out['supplier'] as Map)['id'] as String;
          }
        } else {
          await _t.send('PUT', '/v1/stock/suppliers/${Uri.encodeComponent(_supplierFor)}/link', body: {'link_id': widget.link});
        }
        _linked = true;
      }
      final lines = <Map<String, Object?>>[];
      for (final l in bag.lines) {
        // A label typed as new is made first.
        final tr = _treat[l.item.id] = await _choices.made(_treatOf(l.item));
        if (_remember.contains(l.item.id) && _memory(l.item.id) != null) {
          // Changed and remembered: from now on, for this order too.
          await widget.chats.auth.api.putItemLink(
            link: widget.link,
            body: PutItemLinkRequest(
              sellerItemId: l.item.id,
              treatment: tr.how,
              stockItemId: tr.how == 'stock' ? tr.stockItemId : null,
              uses: tr.how == 'stock' ? tr.uses : null,
              labelId: tr.labelId,
              trackMany: tr.how == 'track' && l.quantity > 50 ? true : null,
            ),
          );
        }
        lines.add({
          'seller_item_id': l.item.id,
          'quantity': l.quantity,
          if (_says(l.item)) ...tr.fields(),
          if (_many.contains(l.item.id)) 'track_many': true,
        });
      }
      final deal = _offering ? {'offer': true, 'total_kobo': offerKobo} : <String, Object?>{};
      final what = '$lines|${_note.text.trim()}|$deal';
      var order = _madeOrders[what];
      if (order == null) {
        final out = await _postOnce('/v1/supplier-prices/$_enc/order', 'order-$what', {'lines': lines, 'note': _note.text.trim(), ...deal}) as Map<String, dynamic>;
        order = _madeOrders[what] = (out['purchase'] as Map).cast<String, dynamic>();
      }
      try {
        await _t.send('POST', '/v1/stock/purchases/${Uri.encodeComponent(order['id'] as String)}/send', body: const {});
      } on ApiError catch (e) {
        // Above the business's limit: drafted, for someone else to approve.
        if (e.status == 409) {
          _finish('${order['reference']} is drafted: it is above your limit, so someone else sends it');
          return;
        }
        rethrow;
      }
      _finish(_offering ? 'Offer sent to $_biz: ${naira(offerKobo!)}' : 'Order sent to $_biz: ${naira(bag.total)}');
    } on ApiError catch (e) {
      final code = treatCode(e);
      final at = _int(e.body['line']);
      final it = at < bag.lines.length ? bag.lines[at].item : null;
      if (code == 'track_ask' && it != null && mounted) {
        // Asked first: put them on, or just record them; then placed again.
        setState(() => _busy = false);
        final yes = await askTrackMany(context, e);
        if (yes == null || !mounted) return;
        setState(() {
          if (yes) {
            _many.add(it.id);
          } else {
            _treat[it.id] = _treatOf(it).copyWith(how: 'record');
          }
        });
        return _place(bag, offerKobo);
      }
      if (code == 'track_too_many' && it != null && mounted) {
        setState(() => _treat[it.id] = _treatOf(it).copyWith(how: 'record'));
        showTkToast(context, treatRefusal(e));
        if (mounted) setState(() => _busy = false);
        return;
      }
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  Future<void> _askQuote(Bag bag) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await _t.send('POST', '/v1/link-chats/$_enc/messages', body: {'text': quoteText(bag.lines)});
      _finish('Asked $_biz for a quote');
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  Future<void> _startStock() async {
    setState(() => (_starting = true, _stockError = null));
    try {
      await _t.send('POST', '/v1/stock/setup', body: const {});
      await _loadStock();
      if (mounted) showTkToast(context, 'Your stock is kept on Taskiem now');
    } on ApiError catch (e) {
      if (mounted) setState(() => _stockError = e.message);
    } on Offline {
      if (mounted) setState(() => _stockError = 'You’re offline. Try again when you’re back online.');
    }
    if (mounted) setState(() => _starting = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final list = _prices.data;
    final items = _items;
    final bag = Bag(items, _qty);
    final short = shortOf(items, _qty);
    final offerKobo = _offering ? dealKobo(_offer.text) : null;
    final offerProblem = !_offering
        ? ''
        : _offer.text.trim().isEmpty
        ? 'Say your price for the whole order.'
        : !(offerKobo != null && offerKobo > 0)
        ? 'A price in naira, like 740,000 or 740k.'
        : offerKobo >= bag.total
        ? 'An offer is under their price, ${naira(bag.total)}.'
        : '';
    final updatedAt = DateTime.tryParse(list?['updated_at'] as String? ?? '')?.toLocal();
    final now = DateTime.now();
    final updated = updatedAt != null && updatedAt.year == now.year && updatedAt.month == now.month && updatedAt.day == now.day ? 'updated today' : 'as they share them';
    final storefront = list?['name'] == storefrontPrices;
    final loaded = _suppliers != null && _own != null && _links != null && _choices.ready;
    final toStock = bag.lines.any((l) => _treatOf(l.item).how == 'stock');
    final stops = !loaded
        ? null
        : _stockOff && toStock
        ? 'Start keeping your stock on Taskiem to add to it.'
        : bag.lines.any((l) => !_treatOf(l.item).ready)
        ? 'Choose the stock item of each line added to stock.'
        : null;
    final canTrade = widget.chats.auth.can('chat.trade');
    final stopped = _busy || !loaded || stops != null || offerProblem.isNotEmpty;
    final place = TkCta(label: _busy ? 'Sending…' : (_offering ? 'Send offer' : 'Place order'), onTap: stopped ? null : () => _place(bag, offerKobo));

    return TkSheet(
      title: _review ? 'Review your order' : (widget.browse ? '$_biz\'s shop' : 'Order from $_biz'),
      lead: _review ? null : ChatAvatar(name: _biz, size: 48, letterSize: 16),
      sub: _review ? null : (storefront ? 'Their storefront prices · $updated' : 'Your prices · $updated'),
      onBack: _review ? () => setState(() => _review = false) : null,
      bareFooter: !_review,
      footer: widget.browse
          ? null
          : !_review
          ? BagBar(
              count: short != null ? '${short.name}: at least ${short.min} in all' : bag.count,
              total: naira(bag.total),
              onGo: bag.lines.isEmpty || short != null ? null : () => setState(() => _review = true),
            )
          : Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.baseline,
                  textBaseline: TextBaseline.alphabetic,
                  children: [
                    Expanded(child: Text(_offering ? 'Your offer' : 'Total', style: tkText(size: 15, color: t.text))),
                    if (_offering && offerKobo != null && offerKobo > 0 && offerProblem.isEmpty) ...[
                      Text(naira(offerKobo), style: tkText(size: 22, weight: 800, color: t.text)),
                      const SizedBox(width: 6),
                      Text(
                        naira(bag.total),
                        style: tkText(size: 15, color: t.muted).copyWith(decoration: TextDecoration.lineThrough),
                      ),
                    ] else
                      Text(naira(bag.total), style: tkText(size: 22, weight: 800, color: t.text)),
                  ],
                ),
                if (stops != null) ...[const SizedBox(height: 12), Text(stops, style: tkText(size: 14, color: t.muted))],
                const SizedBox(height: 12),
                if (canTrade)
                  TkFootTwo(
                    // `.tk-btn.ghost`: 18 px each side and its line; `.tk-cta`: 14 px.
                    firstOwn: 38,
                    secondOwn: 28,
                    first: TkGhostButton(label: 'Ask for a quote', onTap: _busy ? null : () => _askQuote(bag)),
                    second: place,
                  )
                else
                  place,
              ],
            ),
      overlay: _done == null ? null : TkSavedTick(words: _done!),
      body: _body(t, list, items, bag, offerKobo, offerProblem, storefront),
    );
  }

  Widget _body(TkTokens t, Map<String, dynamic>? list, List<PriceItem> items, Bag bag, int? offerKobo, String offerProblem, bool storefront) {
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final stockSection = _stockOff && list != null && _review && bag.lines.any((l) => _treatOf(l.item).how == 'stock')
        ? Container(
            margin: const EdgeInsets.fromLTRB(20, 0, 20, 12),
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(18)),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Semantics(header: true, child: Text('First, keep your stock on Taskiem', style: tkText(size: 16, weight: 700, color: t.text))),
                const SizedBox(height: 6),
                Text(
                  'What you add to stock from $_biz is counted in when it comes. Taskiem keeps your stock in your products table.',
                  style: tkText(size: 14, color: t.muted),
                ),
                const SizedBox(height: 10),
                TkSoftButton(label: _starting ? 'Starting…' : 'Start keeping my stock', onTap: _starting ? null : _startStock),
                if (_stockError != null) ...[const SizedBox(height: 10), TkErrorBox(_stockError!)],
              ],
            ),
          )
        : null;
    if (list == null) {
      return ListView(
        children: [
          if (_prices.error != null)
            Padding(padding: const EdgeInsets.symmetric(horizontal: 20), child: TkErrorBox(_prices.error.toString()))
          else if (!_pricesRead)
            const TkLoading(label: 'Loading their prices…')
          else
            TkEmpty(icon: 'tag', title: 'Nothing on $_biz\'s storefront yet', line: 'Ask them in the chat what they sell, or to share a price list.'),
        ],
      );
    }
    if (!_review) {
      final cats = priceChips(items, _chip);
      final shown = orderShown(items, _chip, _search.text);
      final sections = orderSections(shown);
      // One scroll for all of it, the search and chips too: with large
      // text the head and the bag leave a list of its own no room.
      // Each by its own key: Same as last time goes as the bag fills, and a
      // row (a size being typed) must keep its state when the rest move up.
      Widget side(Key key, Widget w) => Padding(key: key, padding: const EdgeInsets.symmetric(horizontal: 12), child: w);
      return ListView(
        padding: const EdgeInsets.only(bottom: 96),
        children: [
          if (stockSection != null) KeyedSubtree(key: const ValueKey('stock'), child: stockSection),
          if (!widget.browse && _last != null && bag.lines.isEmpty)
            Padding(
              key: const ValueKey('last'),
              padding: const EdgeInsets.fromLTRB(20, 0, 20, 12),
              child: Align(
                alignment: Alignment.centerLeft,
                child: TkSoftButton(
                  label: 'Same as last time',
                  semanticsLabel: 'Same as last time: ${_last!['reference'] ?? ''}',
                  onTap: () => setState(() => _qty = reorderQty(_maps(_last!['lines']), _links ?? const [], items)),
                ),
              ),
            ),
          TkSearchBox(key: const ValueKey('search'), controller: _search, hint: storefront ? 'Search their shop' : 'Search their price list'),
          if (cats.isNotEmpty) TkChipRow(key: const ValueKey('chips'), chips: cats, on: _chip, onPick: (tag) => setState(() => _chip = tag)),
          if (shown.isEmpty) side(const ValueKey('none'), const TkEmpty(icon: 'search', title: 'Nothing matches', line: 'Try another word, or show all.')),
          for (final sec in sections) ...[
            if (sec.label.isNotEmpty)
              Padding(
                key: ValueKey('cat:${sec.label}'),
                // `.tk-order-cat`: 14 px above, in from the list's sides.
                padding: const EdgeInsets.fromLTRB(32, 14, 32, 0),
                child: Semantics(
                  container: true,
                  header: true,
                  label: sec.label,
                  excludeSemantics: true,
                  child: Text(sec.label.toUpperCase(), style: tkText(size: 13, weight: 700, letterSpacing: 0.78, color: t.muted)),
                ),
              ),
            // ChatBuyer.tsx `.tk-order-list.tk-stagger`: rows follow the sheet up.
            // The rows on one card (docs/spec/mobile-app-look.md, "The chat").
            for (final (i, it) in sec.items.indexed)
              side(
                ValueKey('item:${it.id}'),
                TkIn.sheetRow(
                  index: i,
                  child: TkCardSlice(
                    first: i == 0,
                    last: i == sec.items.length - 1,
                    inset: 66,
                    child: Padding(padding: const EdgeInsets.symmetric(horizontal: 12), child: _row(t, dark, it)),
                  ),
                ),
              ),
          ],
        ],
      );
    }
    // The review.
    final theirs = _theirs;
    return TkFormBody(
      children: [
        ?stockSection,
        if (_suppliers != null && theirs == null) ...[
          Text('$_biz becomes one of your suppliers with this order. Orders and bills from them are kept with your other suppliers.', style: tkText(size: 14, color: t.muted)),
          if (_unlinkedSuppliers.isNotEmpty)
            TkKitField(
              field: false,
              label: 'Which of your suppliers is $_biz?',
              child: _Select(
                label: 'Which of your suppliers is $_biz?',
                value: _supplierFor,
                choices: [('new', 'A new supplier: $_biz'), for (final x in _unlinkedSuppliers) (x['id'] as String, x['name'] as String? ?? '')],
                onPick: (v) => setState(() => _who = v),
              ),
            ),
        ],
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final l in bag.lines)
              Padding(
                // `.tk-lines { gap: 10px }`: between lines, none after.
                padding: EdgeInsets.only(bottom: identical(l, bag.lines.last) ? 0 : 10),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: Text.rich(
                        TextSpan(
                          text: '${l.quantity} × ${l.item.name}',
                          children: [
                            if (bag.unitPrice(l.item) < l.item.price)
                              TextSpan(
                                text: ' at ${naira(bag.unitPrice(l.item))}${l.item.variantOf != null ? '' : ' for ${l.quantity}'}',
                                style: tkText(size: 14, color: t.muted),
                              ),
                          ],
                        ),
                        style: tkText(size: 15, color: t.text),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Text(naira(l.quantity * bag.unitPrice(l.item)), style: tkText(size: 15, weight: 700, color: t.text)),
                  ],
                ),
              ),
          ],
        ),
        // The question on each line (§6): asked of what is not remembered;
        // a remembered one in a few words, changed for this order or
        // remembered for the next.
        if (_choices.ready)
          for (final l in bag.lines)
            TreatQuestion(
              key: ValueKey('treat:${l.item.id}'),
              item: l.item.name,
              supplier: _biz,
              quantity: l.quantity,
              value: _treatOf(l.item),
              choices: _choices,
              asked: _memory(l.item.id) == null,
              remember: _remember.contains(l.item.id),
              onRemember: _memory(l.item.id) == null || !widget.chats.auth.can('storefront.manage')
                  ? null
                  : (on) => setState(() => on ? _remember.add(l.item.id) : _remember.remove(l.item.id)),
              onChanged: _busy ? null : (v) => setState(() => _treat[l.item.id] = v),
            )
        else
          const TkLoading(rows: 2, label: 'Loading your stock items and labels…'),
        Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _ChipButton(
              label: _offering ? 'Pay their price instead' : 'Make an offer',
              on: _offering,
              onTap: () => setState(() => _offering = !_offering),
            ),
            if (_offering) ...[
              const SizedBox(height: 10),
              TkKitField(
                label: 'Your price for the whole order',
                hint: 'Their price is ${naira(bag.total)}. They can accept, counter or decline; it lapses in 48 hours.',
                error: _offer.text.trim().isNotEmpty && offerProblem.isNotEmpty ? offerProblem : null,
                child: TkTextBox(controller: _offer, money: true, hint: '740,000'),
              ),
            ],
            if (_offering && offerKobo != null && offerKobo > 0 && offerProblem.isEmpty) ...[
              const SizedBox(height: 10),
              Semantics(liveRegion: true, child: Text('${offWords(offerKobo, bag.total)} their price', style: tkText(size: 14, color: t.muted))),
            ],
          ],
        ),
        TkKitField(label: 'A note for them (optional)', child: TkTextBox(controller: _note, hint: 'Deliver before Friday')),
        Text('Delivery and payment as you agree in the chat. The invoice arrives here.', style: tkText(size: 14, color: t.muted)),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }

  /// `.tk-order-row`: the tile (their photo), the name and its line, and
  /// − n + (pine once in the bag), or Choose sizes and their grid.
  Widget _row(TkTokens t, bool dark, PriceItem it) {
    final n = _qty[it.id] ?? 0;
    final sizes = soldInSizes(it);
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              ExcludeSemantics(
                child: it.photo
                    ? ClipRRect(
                        borderRadius: BorderRadius.circular(12),
                        child: ApiImage(transport: _t, path: '/v1/link-chats/$_enc/photo?item=${Uri.encodeQueryComponent(it.id)}&w=320', width: 40, height: 40),
                      )
                    : NameTile(it.name),
              ),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(it.name, style: tkText(size: 16, weight: 600, color: t.text)),
                    Text(rowLine(it), style: tkText(size: 14, color: t.muted)),
                    if (widget.browse && it.description.isNotEmpty) Text(it.description, style: tkText(size: 14, color: t.muted)),
                    if (it.available && it.breaks.isNotEmpty) Text(breakWords(it), style: tkText(size: 14, color: t.muted)),
                  ],
                ),
              ),
              if (!widget.browse) ...[
                const SizedBox(width: 14),
                if (sizes)
                  Semantics(
                    expanded: _grid == it.id,
                    child: TkSoftButton(
                      label: sizesTaken(it, _qty).isEmpty ? 'Choose sizes' : sizesTaken(it, _qty),
                      semanticsLabel: '${sizesTaken(it, _qty).isEmpty ? 'Choose sizes' : sizesTaken(it, _qty)} of ${it.name}',
                      onTap: !it.available || it.sizes.isEmpty ? null : () => setState(() => _grid = _grid == it.id ? null : it.id),
                    ),
                  )
                else
                  QtyStepper(
                    name: it.name,
                    n: n,
                    on: n > 0,
                    onLess: n == 0 ? null : () => setState(() => _qty = {..._qty, it.id: stepQty(n, it.min, -1)}),
                    onMore: !it.available ? null : () => setState(() => _qty = {..._qty, it.id: stepQty(n, it.min, 1)}),
                  ),
              ],
            ],
          ),
          if (_grid == it.id && sizes && !widget.browse) _sizesGrid(t, it),
        ],
      ),
    );
  }

  Widget _sizesGrid(TkTokens t, PriceItem product) {
    final taken = product.sizes.fold<int>(0, (a, z) => a + (_qty[z.id] ?? 0));
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text('${product.min > 1 ? 'At least ${product.min} in all. ' : ''}${taken > 0 ? '$taken so far.' : ''}', style: tkText(size: 14, color: t.muted)),
          const SizedBox(height: 6),
          LayoutBuilder(
            builder: (context, box) {
              final cols = math.max(1, ((box.maxWidth + 8) / (84 + 8)).floor());
              final w = (box.maxWidth - 8 * (cols - 1)) / cols;
              return Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final z in product.sizes)
                    SizedBox(
                      width: w,
                      child: _SizeCell(size: z, qty: _qty[z.id] ?? 0, onQty: (n) => setState(() => _qty = {..._qty, z.id: n})),
                    ),
                ],
              );
            },
          ),
        ],
      ),
    );
  }
}

/// `.tk-bag`: pine, the count and total, and its button in lime (Review
/// order, Next).
class BagBar extends StatelessWidget {
  const BagBar({super.key, required this.count, required this.total, this.onGo, this.go = 'Review order', this.goIcon = 'arrow-right'});
  final String count;
  final String total;
  final VoidCallback? onGo;
  final String go;
  final String goIcon;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    // With large text the words and the button share no line: the button
    // goes under them, the whole width.
    final stack = MediaQuery.textScalerOf(context).scale(10) > 15;
    final words = Semantics(
      container: true,
      liveRegion: true,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Opacity(opacity: 0.8, child: Text(count, style: tkText(size: 13, color: t.tkOnPine))),
          Text(total, style: tkText(size: 20, weight: 800, letterSpacing: -0.2, color: t.tkOnPine)),
        ],
      ),
    );
    final button = TapArea(
      child: Semantics(
        button: true,
        enabled: onGo != null,
        label: go,
        onTap: onGo,
        excludeSemantics: true,
        child: TkPress(
          onTap: onGo,
          child: Opacity(
            opacity: onGo == null ? 0.55 : 1,
            child: Container(
              constraints: const BoxConstraints(minHeight: 52),
              padding: const EdgeInsets.symmetric(horizontal: 20),
              decoration: BoxDecoration(color: t.brandDark, borderRadius: BorderRadius.circular(999)),
              child: Row(
                mainAxisSize: stack ? MainAxisSize.max : MainAxisSize.min,
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Flexible(child: Text(go, style: tkText(size: 15, weight: 800, color: t.brand))),
                  const SizedBox(width: 6),
                  TkIcon(goIcon, size: 18, color: t.brand),
                ],
              ),
            ),
          ),
        ),
      ),
    );
    return Container(
      padding: stack ? const EdgeInsets.all(10) : const EdgeInsets.fromLTRB(20, 10, 10, 10),
      decoration: BoxDecoration(
        color: t.brand,
        borderRadius: BorderRadius.circular(22),
        boxShadow: [BoxShadow(color: t.brand.withValues(alpha: 0.6), offset: const Offset(0, 20), blurRadius: 40, spreadRadius: -14)],
      ),
      child: stack
          ? Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [Padding(padding: const EdgeInsets.fromLTRB(10, 0, 10, 8), child: words), button],
            )
          : Row(children: [Expanded(child: words), const SizedBox(width: 12), button]),
    );
  }
}

/// `.tk-qty`: − n +, soft, pine once in the bag.
class QtyStepper extends StatelessWidget {
  const QtyStepper({super.key, required this.name, required this.n, required this.on, this.onLess, this.onMore});
  final String name;
  final int n;
  final bool on;
  final VoidCallback? onLess;
  final VoidCallback? onMore;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    // A soft track, − and + on round card buttons, the number lime-inked
    // and the track lime-tinted once something is picked
    // (docs/spec/mobile-app-look.md, "The chat").
    final bg = on ? look.lime.withValues(alpha: 0.22) : look.ground;
    final ink = on ? look.limeInk : t.text;
    Widget step(String sign, String label, VoidCallback? onTap) => TapArea(child: Semantics(
      button: true,
      enabled: onTap != null,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: SizedBox(
          width: 40,
          height: 40,
          child: Center(
            child: Container(
              width: 32,
              height: 32,
              alignment: Alignment.center,
              decoration: BoxDecoration(color: look.card, shape: BoxShape.circle),
              child: Text(sign, style: tkText(size: 18, weight: 600, lineHeight: 1, color: t.text)),
            ),
          ),
        ),
      ),
    ));
    return Semantics(
      container: true,
      label: 'How many $name',
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 200),
        padding: const EdgeInsets.symmetric(horizontal: 2),
        decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            step('−', 'One fewer $name', onLess),
            ConstrainedBox(
              constraints: const BoxConstraints(minWidth: 28),
              child: Semantics(
                liveRegion: true,
                child: Text('$n', textAlign: TextAlign.center, style: tkText(size: 15, weight: 800, color: ink)),
              ),
            ),
            step('+', 'One more $name', onMore),
          ],
        ),
      ),
    );
  }
}

/// `.tk-grid-cell`: a size, its price when its own, how many.
class _SizeCell extends StatefulWidget {
  const _SizeCell({required this.size, required this.qty, required this.onQty});
  final PriceSize size;
  final int qty;
  final ValueChanged<int> onQty;

  @override
  State<_SizeCell> createState() => _SizeCellState();
}

class _SizeCellState extends State<_SizeCell> {
  late final _text = TextEditingController(text: widget.qty > 0 ? '${widget.qty}' : '');
  // Its own, kept across builds: a new one each build loses the keyboard
  // at every digit.
  final _focus = FocusNode();

  @override
  void didUpdateWidget(_SizeCell oldWidget) {
    super.didUpdateWidget(oldWidget);
    // The bag changed elsewhere (Same as last time): the box says so.
    if (widget.qty != (int.tryParse(_text.text) ?? 0)) _text.text = widget.qty > 0 ? '${widget.qty}' : '';
  }

  @override
  void dispose() {
    _text.dispose();
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final z = widget.size;
    final name = z.label.isEmpty ? z.name : z.label;
    _focus.canRequestFocus = z.available;
    return Opacity(
      opacity: z.available ? 1 : 0.55,
      child: Container(
        padding: const EdgeInsets.all(8),
        decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(12)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text.rich(
              TextSpan(
                text: name,
                children: [if ((z.price ?? 0) > 0) TextSpan(text: ' ${naira(z.price!)}', style: tkText(size: 13, weight: 500, color: t.muted))],
              ),
              style: tkText(size: 13, weight: 700, color: t.text),
            ),
            const SizedBox(height: 4),
            Semantics(
              label: '$name: how many',
              textField: true,
              enabled: z.available,
              child: Container(
                height: 36,
                decoration: BoxDecoration(color: t.panel, borderRadius: BorderRadius.circular(10)),
                alignment: Alignment.center,
                child: EditableText(
                  controller: _text,
                  focusNode: _focus,
                  // Not on sale now: no box to type in (`disabled`).
                  readOnly: !z.available,
                  textAlign: TextAlign.center,
                  keyboardType: TextInputType.number,
                  inputFormatters: [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(6)],
                  style: tkText(size: 15, weight: 700, color: t.text),
                  cursorColor: t.text,
                  backgroundCursorColor: t.muted,
                  onChanged: (v) => widget.onQty(math.min(int.tryParse(v) ?? 0, 100000)),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// `.tk-chip-btn`: a soft pill; lime tint while pressed (`.tk-offer`).
class _ChipButton extends StatelessWidget {
  const _ChipButton({required this.label, required this.on, required this.onTap});
  final String label;
  final bool on;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(
      child: Semantics(
        button: true,
        toggled: on,
        label: label,
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: Container(
            constraints: const BoxConstraints(minHeight: 36),
            padding: const EdgeInsets.symmetric(horizontal: 14),
            decoration: BoxDecoration(color: on ? t.tkLimeSoft : t.tkSoft, borderRadius: BorderRadius.circular(999)),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [Text(label, style: tkText(size: 14, weight: 600, color: on ? t.tkPineInk : t.text))],
            ),
          ),
        ),
      ),
    );
  }
}

/// A choice among a few, as the web's `<select>`: the box shows the one
/// chosen; pressed, the choices open.
class _Select extends StatelessWidget {
  const _Select({required this.label, required this.value, required this.choices, required this.onPick});
  final String label;
  final String value;
  final List<(String, String)> choices;
  final ValueChanged<String> onPick;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final now = choices.where((c) => c.$1 == value).map((c) => c.$2).firstOrNull ?? '';
    Future<void> open() async {
      final v = await showTkModal<String>(
        context,
        title: label,
        builder: (context) => Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final (id, words) in choices)
              Semantics(
                button: true,
                selected: id == value,
                label: words,
                onTap: () => Navigator.of(context).pop(id),
                excludeSemantics: true,
                child: GestureDetector(
                  behavior: HitTestBehavior.opaque,
                  onTap: () => Navigator.of(context).pop(id),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(vertical: 12),
                    child: Text(words, style: tkText(size: 16, weight: id == value ? 700 : 500, color: TkTheme.tokens(context).text)),
                  ),
                ),
              ),
          ],
        ),
      );
      if (v != null) onPick(v);
    }

    return TapArea(
      child: Semantics(
        button: true,
        label: '$label: $now',
        onTap: open,
        excludeSemantics: true,
        child: GestureDetector(
          onTap: open,
          child: Container(
            height: 54,
            padding: const EdgeInsets.symmetric(horizontal: 16),
            decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(14)),
            child: Row(
              children: [
                Expanded(child: Text(now, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 17, weight: 600, color: t.text))),
                RotatedBox(quarterTurns: 1, child: TkIcon('chevron', size: 12, color: t.text)),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// A thing for sale with no photo: its letters on a soft square of its
/// name's own colour (docs/spec/mobile-app-look.md, "The chat").
class NameTile extends StatelessWidget {
  const NameTile(this.name, {super.key});
  final String name;

  @override
  Widget build(BuildContext context) {
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final g = tkFaceFor(name);
    final ink = Color.lerp(g.last, dark ? const Color(0xFFFFFFFF) : const Color(0xFF000000), dark ? 0.25 : 0.2)!;
    return ExcludeSemantics(
      child: Container(
        width: 40,
        height: 40,
        decoration: BoxDecoration(color: g.first.withValues(alpha: dark ? 0.2 : 0.18), borderRadius: BorderRadius.circular(12)),
        alignment: Alignment.center,
        child: Text(tkInitials(name), maxLines: 1, textScaler: TextScaler.noScaling, style: tkText(size: 14, weight: 700, color: ink)),
      ),
    );
  }
}
