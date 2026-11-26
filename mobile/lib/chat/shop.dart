// The shop (docs/spec/mobile-app-look.md, "The shop"): a business's items to
// browse as a window is shopped. A screen of its own: the items as photos
// two across, searched and filtered by their categories, the ones saved
// with the heart and the ones looked at lately; an item's own screen (its
// photo, sizes, Ask about this, Share); and the bag.
//
// Two shops are drawn by it, each a [ShopSource]: a person's, from their
// chat with a business ([PersonShop]: its storefront at its prices, checked
// out in the shop's sheet, my_chat_tools.dart ShopSheet, pickup or
// delivery); and a business's, from its conversation with a linked
// supplier (business_shop.dart: their prices for the link, quantity prices,
// sizes typed in a grid, checked out as a purchase order in the order
// sheet's review). The bag stays while the app runs, as the chat does; the
// hearts and what was looked at are kept on the phone (Prefs), the server
// keeping neither yet.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show Transport;
import '../app/prefs.dart';
import '../app/session.dart' show MemoryKeyStore;
import '../kit/api_image.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/look.dart' show tkTabular;
import '../kit/mobile.dart' show TkChips, TkRoute;
import '../kit/modal.dart' show showTkToast;
import '../kit/sheet.dart';
import '../kit/tap_area.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'buyer.dart' show Break;
import 'cards.dart' show CardPill;
import 'door.dart' show BusinessFace;
import 'my_chat.dart';
import 'my_chat_tools.dart' show ShopSheet, variantWords;
import 'people_cards.dart' show shareWithPeople;
import 'thread.dart' show ChatHeadBar;
import 'words.dart';

/// Opens the shop of [d]'s business; at [item] when a card in the chat
/// named one.
Future<void> openShop(BuildContext context, MyChatData d, {String? item}) => openShopOf(context, PersonShop(d), item: item);

/// Opens a shop; at [item] when a card in the chat named one.
Future<void> openShopOf(BuildContext context, ShopSource source, {String? item}) => Navigator.of(context).push<void>(ShopRoute(ShopScreen(source: source, item: item)));

/// A screen of the shop: back to the chat closes them all at once.
class ShopRoute<T> extends TkRoute<T> {
  ShopRoute(Widget page) : super(pageBuilder: (_, _, _) => page);
}

/// Back to the chat the shop was opened from.
void shopToChat(BuildContext context) => Navigator.of(context).popUntil((r) => r is! ShopRoute);
void _toChat(BuildContext context) => shopToChat(context);

/// Another screen of the shop, over this one.
Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push<void>(ShopRoute(page));

/// What is in the bag, by item (or size) id: one bag a chat, kept while
/// the app runs, so going back to the chat and in again finds it.
class ShopBag extends ChangeNotifier {
  ShopBag._();
  static final _bags = <String, ShopBag>{};
  factory ShopBag.of(String chat) => _bags.putIfAbsent(chat, ShopBag._);

  final Map<String, int> lines = {};
  int get count => lines.values.fold(0, (a, b) => a + b);
  int of(String id) => lines[id] ?? 0;

  void set(String id, int n) {
    if (n <= 0) {
      lines.remove(id);
    } else {
      lines[id] = n;
    }
    notifyListeners();
  }

  /// The whole bag at once (the plain list changed it).
  void setAll(Map<String, int> all) {
    lines
      ..clear()
      ..addAll({for (final e in all.entries) if (e.value > 0) e.key: e.value});
    notifyListeners();
  }

  void clear() {
    lines.clear();
    notifyListeners();
  }

  /// The shop read again: what it no longer sells, or has sold out, leaves
  /// the bag, and none stays above how few are left; the bag's prices are
  /// the shop's own (shopThings). Says whether anything changed.
  bool keepTo(Map<String, ShopThing> things) {
    var changed = false;
    for (final id in [...lines.keys]) {
      final th = things[id];
      if (th == null || !th.available) {
        lines.remove(id);
        changed = true;
      } else if (th.left != null && lines[id]! > th.left!) {
        lines[id] = th.left!;
        changed = true;
      }
    }
    if (changed) notifyListeners();
    return changed;
  }
}

/// One thing the bag can hold: an item with no sizes, or one size of one.
class ShopThing {
  const ShopThing({required this.id, required this.item, this.size, required this.price, this.left, required this.available});
  final String id;
  final ShopItem item;
  final ShopItemSizesItem? size;
  final int? price;
  final int? left;
  final bool available;
  String get sizeWords => size == null ? '' : variantWords(size!.variant);
}

Map<String, ShopThing> shopThings(MyChatShopResponse s) => thingsOf(s.items);

/// What the bag can hold of [items]: each item, or each of its sizes.
Map<String, ShopThing> thingsOf(List<ShopItem> items) => {
  for (final i in items)
    if ((i.sizes ?? const []).isEmpty)
      i.id: ShopThing(id: i.id, item: i, price: i.priceKobo, left: i.fewLeft, available: i.available)
    else
      for (final z in i.sizes!) z.id: ShopThing(id: z.id, item: i, size: z, price: z.priceKobo ?? i.priceKobo, left: z.fewLeft, available: z.available && i.available),
};

/// The lowest price of an item, its sizes counted.
int? fromPrice(ShopItem i) {
  final prices = [for (final z in i.sizes ?? const <ShopItemSizesItem>[]) ?z.priceKobo];
  if (prices.isEmpty) return i.priceKobo;
  prices.sort();
  return prices.first;
}

/// What a shop shows as it is read: its items and whether it takes orders.
class ShopData {
  const ShopData({required this.items, required this.open, this.closed, required this.ways, required this.next});
  final List<ShopItem> items;

  /// It takes orders now (a supplier's: from those who may place them).
  final bool open;

  /// Why it takes none, when it says.
  final String? closed;

  /// The head's line under the name: "Open · Delivery and pickup".
  final String ways;

  /// The bag's line under its total: what comes after it.
  final String next;
}

/// The quantity terms of a supplier's item (business_shop.dart): its unit,
/// its minimum, and its lower prices from a quantity up, its sizes counted
/// together.
class ShopTerms {
  const ShopTerms({this.unit = '', this.min = 1, this.breaks = const [], this.sized = false});
  final String unit;
  final int min;
  final List<Break> breaks;
  final bool sized;
}

/// What a shop reads and how it orders. The screens are one; what differs
/// is here.
abstract class ShopSource {
  /// Whose bag: one a chat.
  String get bagKey;

  /// Whose hearts and lately-seen items, kept on the phone.
  String get keptKey;

  /// The business shopped.
  String get name;
  Widget avatar();
  Prefs get prefs;

  Future<ShopData> load();

  /// Ask about this: the question begun in the chat's box.
  ValueChanged<String>? get onAsk;

  /// Share an item with someone, where it can be.
  void Function(BuildContext context, ShopItem item)? get share;

  /// Photos read from the API (a supplier's, through the link).
  Transport? get photos => null;

  /// What one of [th] costs with [lines] in the bag: a quantity price
  /// reached counts.
  int? priceIn(ShopThing th, Map<String, int> lines) => th.price;

  /// The bag's total.
  int totalOf(ShopData s, Map<String, int> lines) {
    final things = thingsOf(s.items);
    return lines.entries.fold(0, (a, e) => a + (things[e.key] == null ? 0 : (priceIn(things[e.key]!, lines) ?? 0) * e.value));
  }

  /// − or + on a thing: one at a time, unless a minimum says.
  int stepped(ShopThing th, int n, int dir) => n + dir < 0 ? 0 : n + dir;

  /// An item's quantity terms (a supplier's), if any.
  ShopTerms? termsOf(ShopItem item) => null;

  /// An item's sizes typed in a grid (a supplier's), in place of a size
  /// picked and Add to bag.
  Widget? sizesGrid(BuildContext context, ShopItem item, ShopBag bag) => null;

  /// What stops the bag going on, said (a product under its minimum).
  String? stops(Map<String, int> lines) => null;

  /// Whether it has a bag (a supplier's: for those who may order).
  bool get bags => true;

  /// The bag's button.
  String get checkoutWords => 'Check out';

  /// The plain list of the same (a supplier's, for fast reorders).
  Future<void> Function(BuildContext context, ShopBag bag)? get list => null;

  /// Checks the bag out; true once the order went.
  Future<bool> checkout(BuildContext context, Map<String, int> lines);
}

/// A person's shop: the business's storefront, from their chat with it.
class PersonShop extends ShopSource {
  PersonShop(this.d);
  final MyChatData d;

  @override
  String get bagKey => d.id;
  @override
  String get keptKey => (d.chat?.business ?? '').isNotEmpty ? d.chat!.business : d.id;
  @override
  String get name => d.name;
  @override
  Widget avatar() => BusinessFace(name: d.name, urls: d.chat?.photo?.urls, size: 40, letterSize: 15);

  @override
  Prefs get prefs => keptPrefs(d.prefs, d);

  @override
  ValueChanged<String>? get onAsk => d.onAsk;

  @override
  void Function(BuildContext, ShopItem)? get share => (d.chat?.business ?? '').isEmpty
      ? null
      : (context, i) => shareWithPeople(context, d.auth, SendPeopleMessageRequestCard(type: 'product', businessId: d.chat!.business, itemId: i.id), what: i.name);

  @override
  Future<ShopData> load() async {
    final s = await d.api.myChatShop(id: d.id);
    String ways() {
      if (!s.open) return 'Not taking orders now';
      final how = s.pickup && s.delivery ? 'Delivery and pickup' : (s.delivery ? 'Delivery' : (s.pickup ? 'Pickup' : ''));
      return how.isEmpty ? 'Open' : 'Open · $how';
    }

    return ShopData(
      items: s.items,
      open: s.open,
      closed: s.closed,
      ways: ways(),
      next: s.delivery ? 'How you get it, and delivery if you want it, come next.' : 'How you get it comes next.',
    );
  }

  @override
  Future<bool> checkout(BuildContext context, Map<String, int> lines) async {
    var placed = false;
    await showTkSheet<void>(context, (_) => ShopSheet(d: d, bag: {...lines}, onPlaced: () => placed = true));
    return placed;
  }
}

/// The hearts and what was looked at: [prefs] on the phone, or, where the
/// chat was opened without them (a test), kept while [owner] is.
Prefs keptPrefs(Prefs? prefs, Object owner) => prefs ?? (_memory[owner] ??= Prefs(MemoryKeyStore()));
final _memory = Expando<Prefs>('shop');

// --- the shop ---

class ShopScreen extends StatefulWidget {
  ShopScreen({super.key, MyChatData? d, ShopSource? source, this.item}) : src = source ?? PersonShop(d!);
  final ShopSource src;

  /// Opened on this item (a product card in the chat).
  final String? item;

  @override
  State<ShopScreen> createState() => _ShopScreenState();
}

class _ShopScreenState extends State<ShopScreen> {
  ShopData? _shop;
  String? _error;
  final _search = TextEditingController();
  String _filter = 'all';
  late final ShopBag _bag = ShopBag.of(src.bagKey);

  ShopSource get src => widget.src;
  Prefs get _prefs => src.prefs;

  @override
  void initState() {
    super.initState();
    _load();
    _bag.addListener(_changed);
    _prefs.addListener(_changed);
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  Future<void> _load() async {
    setState(() => _error = null);
    try {
      final s = await src.load();
      if (!mounted) return;
      setState(() => _shop = s);
      // Kept while the app ran: what is gone since, out of the bag, said.
      if (_bag.keepTo(thingsOf(s.items)) && mounted) showTkToast(context, 'Your bag changed: something in it is no longer for sale, or fewer are left');
      final start = s.items.where((i) => i.id == widget.item).firstOrNull;
      if (start != null) await _open(start);
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  @override
  void dispose() {
    _bag.removeListener(_changed);
    _prefs.removeListener(_changed);
    _search.dispose();
    super.dispose();
  }

  Future<void> _open(ShopItem i) => _push(context, ShopItemScreen(src: src, shop: _shop!, item: i));

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final s = _shop;
    final pad = MediaQuery.paddingOf(context);
    final saved = _prefs.shopSaved[src.keptKey] ?? const <String>[];
    final list = src.list;
    return TkAppLook(
      child: ColoredBox(
        color: TkLook.of(context).ground,
        child: Column(
          children: [
            ColoredBox(
              color: t.panel,
              child: Padding(
                padding: EdgeInsets.only(top: pad.top),
                child: ChatHeadBar(
                  avatar: src.avatar(),
                  title: src.name,
                  subtitle: s == null ? 'Their shop' : s.ways,
                  onBack: () => Navigator.of(context).maybePop(),
                  actions: [
                    if (list != null) ChatHeadBar.headButton('List view: their prices as a list', TkIcon('b-bars', size: 22, stroke: 2, color: t.text), s == null ? null : () => list(context, _bag)),
                    ChatHeadBar.headButton(
                      _filter == 'saved' ? 'Show all of the shop' : 'Show what you saved',
                      TkIcon('b-heart', size: 22, stroke: 2, color: _filter == 'saved' ? t.danger : t.text, fill: _filter == 'saved'),
                      () => setState(() => _filter = _filter == 'saved' ? 'all' : 'saved'),
                    ),
                    if (src.bags) _BagButton(count: _bag.count, onTap: s == null ? null : () => _openBag(s)),
                  ],
                ),
              ),
            ),
            Expanded(
              child: s == null
                  ? ListView(
                      padding: const EdgeInsets.only(top: 16),
                      children: [if (_error != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: TkErrorBox(_error!)) else const TkLoading(label: 'Loading their shop…')],
                    )
                  : _body(context, s, saved),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _openBag(ShopData s) => _push(context, ShopBagScreen(src: src, shop: s));

  Widget _body(BuildContext context, ShopData s, List<String> saved) {
    final t = TkTheme.tokens(context);
    if (s.items.isEmpty) {
      return ListView(children: [TkEmpty(icon: 'b-box', title: '${src.name} has nothing in their shop yet', line: 'Ask them in the chat.')]);
    }
    final cats = <String>[];
    for (final i in s.items) {
      final c = (i.category ?? '').trim();
      if (c.isNotEmpty && !cats.contains(c)) cats.add(c);
    }
    final q = _search.text.trim().toLowerCase();
    final shown = s.items.where((i) {
      if (_filter == 'saved' && !saved.contains(i.id)) return false;
      if (_filter.startsWith('cat:') && (i.category ?? '').trim() != _filter.substring(4)) return false;
      if (q.isEmpty) return true;
      return '${i.name} ${i.description ?? ''} ${i.category ?? ''}'.toLowerCase().contains(q);
    }).toList();
    final byId = {for (final i in s.items) i.id: i};
    final recent = [for (final id in _prefs.shopRecent[src.keptKey] ?? const <String>[]) ?byId[id]];
    final scale = MediaQuery.textScalerOf(context).scale(1);
    return Stack(
      children: [
        LayoutBuilder(
          builder: (context, box) {
            const gap = 10.0, side = 14.0;
            final w = (box.maxWidth - side * 2 - gap) / 2;
            // The photo square, its name in two lines, its price row.
            final tall = w + 12 + 14 * 1.3 * 2 * scale + 8 + (34 > 16 * 1.3 * scale ? 34 : 16 * 1.3 * scale) + 10;
            return CustomScrollView(
              slivers: [
                SliverToBoxAdapter(
                  child: Padding(
                    padding: const EdgeInsets.only(top: 12),
                    child: TkSearchBox(
                      controller: _search,
                      hint: 'Search ${src.name}',
                      label: 'Search their shop',
                      margin: const EdgeInsets.fromLTRB(14, 0, 14, 0),
                      onChanged: (_) => setState(() {}),
                    ),
                  ),
                ),
                SliverToBoxAdapter(
                  child: TkChips<String>(
                    label: 'Show',
                    padding: const EdgeInsets.fromLTRB(14, 12, 14, 4),
                    chips: [('all', 'All'), ('saved', saved.isEmpty ? 'Saved' : 'Saved · ${saved.length}'), for (final c in cats) ('cat:$c', c)],
                    on: _filter,
                    onPick: (v) => setState(() => _filter = v),
                  ),
                ),
                if (s.closed != null && s.closed!.isNotEmpty && !s.open)
                  SliverToBoxAdapter(child: Padding(padding: const EdgeInsets.fromLTRB(16, 8, 16, 0), child: Text(s.closed!, style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted)))),
                if (recent.isNotEmpty && _filter == 'all' && q.isEmpty) ...[
                  SliverToBoxAdapter(
                    child: Padding(
                      padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
                      child: Semantics(header: true, child: Text('Recently viewed', style: tkDisplay(size: 16, color: t.text))),
                    ),
                  ),
                  SliverToBoxAdapter(
                    child: SizedBox(
                      height: 64 + 6 + 15 * scale + 4,
                      child: ListView.separated(
                        scrollDirection: Axis.horizontal,
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        itemCount: recent.length,
                        separatorBuilder: (_, _) => const SizedBox(width: 10),
                        itemBuilder: (context, k) {
                          final i = recent[k];
                          return TapArea(
                            child: Semantics(
                              button: true,
                              label: i.name,
                              onTap: () => _open(i),
                              excludeSemantics: true,
                              child: TkPress(
                                onTap: () => _open(i),
                                child: SizedBox(
                                  width: 64,
                                  child: Column(
                                    crossAxisAlignment: CrossAxisAlignment.start,
                                    children: [
                                      ShopPhoto(item: i, size: 64, radius: 14, api: src.photos),
                                      const SizedBox(height: 6),
                                      Text(i.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 11.5, lineHeight: 1.3, color: t.muted)),
                                    ],
                                  ),
                                ),
                              ),
                            ),
                          );
                        },
                      ),
                    ),
                  ),
                ],
                if (shown.isEmpty)
                  SliverToBoxAdapter(
                    child: TkEmpty(
                      icon: _filter == 'saved' ? 'b-heart' : 'b-search',
                      title: _filter == 'saved' && q.isEmpty ? 'Nothing saved yet' : 'Nothing matches',
                      line: _filter == 'saved' && q.isEmpty ? 'Press the heart on what you like, to find it here.' : 'Try another word, or show all.',
                    ),
                  )
                else
                  SliverPadding(
                    padding: const EdgeInsets.fromLTRB(side, 12, side, 110),
                    sliver: SliverGrid(
                      gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(crossAxisCount: 2, mainAxisSpacing: gap, crossAxisSpacing: gap, mainAxisExtent: tall),
                      delegate: SliverChildBuilderDelegate(
                        (context, k) => _ItemCard(
                          src: src,
                          shop: s,
                          item: shown[k],
                          saved: saved.contains(shown[k].id),
                          bag: _bag,
                          onOpen: () => _open(shown[k]),
                          onHeart: () => _prefs.toggleShopSaved(src.keptKey, shown[k].id),
                        ),
                        childCount: shown.length,
                      ),
                    ),
                  ),
              ],
            );
          },
        ),
        if (_bag.count > 0 && src.bags)
          Positioned(
            left: 12,
            right: 12,
            bottom: 12 + MediaQuery.paddingOf(context).bottom,
            child: ShopBagBar(count: _bag.count, total: src.totalOf(s, _bag.lines), onTap: () => _openBag(s)),
          ),
      ],
    );
  }
}

/// The bag at the head, with how many are in it.
class _BagButton extends StatelessWidget {
  const _BagButton({required this.count, this.onTap});
  final int count;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Stack(
      clipBehavior: Clip.none,
      children: [
        ChatHeadBar.headButton(count == 0 ? 'Your bag, empty' : 'Your bag, $count ${count == 1 ? 'item' : 'items'}', TkIcon('b-bag', size: 22, stroke: 2, color: t.text), onTap),
        if (count > 0)
          Positioned(
            top: 2,
            right: 0,
            child: IgnorePointer(
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 5),
                constraints: const BoxConstraints(minWidth: 17),
                height: 17,
                alignment: Alignment.center,
                decoration: BoxDecoration(color: look.lime, borderRadius: BorderRadius.circular(9)),
                child: Text('$count', style: tkText(size: 10.5, weight: 700, lineHeight: 1, color: const Color(0xFF0D3B2E))),
              ),
            ),
          ),
      ],
    );
  }
}

/// The bar over the foot of the shop: how many, how much, to the bag.
class ShopBagBar extends StatelessWidget {
  const ShopBagBar({super.key, required this.count, required this.total, required this.onTap});
  final int count;
  final int total;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final ink = TkTheme.of(context).mode == TkMode.dark ? t.brandDarkText : const Color(0xFFFFFFFF);
    final words = '$count ${count == 1 ? 'item' : 'items'} · ${naira(total)}';
    return TapArea(
      child: Semantics(
        button: true,
        label: 'View bag: $words',
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
            decoration: BoxDecoration(
              color: look.on,
              borderRadius: BorderRadius.circular(18),
              boxShadow: const [BoxShadow(color: Color(0x33000000), blurRadius: 18, offset: Offset(0, 6))],
            ),
            child: Row(
              children: [
                TkIcon('b-bag', size: 20, stroke: 2, color: ink),
                const SizedBox(width: 10),
                Expanded(child: Text(words, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14.5, weight: 600, color: ink).copyWith(fontFeatures: tkTabular))),
                Text('View bag', style: tkText(size: 14.5, weight: 700, color: ink)),
                const SizedBox(width: 4),
                TkIcon('chevron', size: 16, stroke: 2, color: ink),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// An item's photo, square; its first letter on a soft tint where it has
/// none (or it cannot be read).
class ShopPhoto extends StatelessWidget {
  const ShopPhoto({super.key, required this.item, this.size, this.radius = 14, this.api});
  final ShopItem item;

  /// Where a photo named by its API path ("/v1/…") is read, signed in.
  final Transport? api;

  /// The side; null: as wide as there is room, square.
  final double? size;
  final double radius;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final tints = [look.lime, look.teal, look.orange, look.blue, look.violet];
    final tint = tints[item.name.codeUnits.fold(0, (a, b) => a + b) % tints.length];
    Widget letter(double side) => Container(
      width: side,
      height: side,
      color: tint.withValues(alpha: 0.22),
      alignment: Alignment.center,
      child: Text(item.name.isEmpty ? '?' : item.name.characters.first.toUpperCase(), style: tkDisplay(size: side * 0.36, color: TkTheme.tokens(context).text.withValues(alpha: 0.55))),
    );
    final url = item.photo;
    Widget pic(double side) => url != null && url.startsWith('/v1/') && api != null
        ? ApiImage(transport: api!, path: url, width: side, height: side, placeholder: letter(side))
        : url == null || !url.startsWith(RegExp('https?://'))
        ? letter(side)
        : Image.network(
            url,
            width: side,
            height: side,
            fit: BoxFit.cover,
            excludeFromSemantics: true,
            gaplessPlayback: true,
            errorBuilder: (_, _, _) => letter(side),
            frameBuilder: (_, child, frame, sync) => sync || frame != null ? child : letter(side),
          );
    return ClipRRect(
      borderRadius: BorderRadius.circular(radius),
      child: size != null ? pic(size!) : LayoutBuilder(builder: (context, box) => pic(box.maxWidth)),
    );
  }
}

/// A round button over a photo: on white, whatever the theme, as the
/// photo's ground is the photo's.
class _OverPhoto extends StatelessWidget {
  const _OverPhoto({required this.label, required this.icon, required this.onTap, this.size = 32, this.color = const Color(0xFF0E1A14), this.fill = false});
  final String label;
  final String icon;
  final VoidCallback onTap;
  final double size;
  final Color color;
  final bool fill;

  @override
  Widget build(BuildContext context) => TapArea(
    child: Semantics(
      button: true,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          width: size,
          height: size,
          decoration: const BoxDecoration(color: Color(0xEBFFFFFF), shape: BoxShape.circle),
          alignment: Alignment.center,
          child: TkIcon(icon, size: size * 0.53, stroke: 2, color: color, fill: fill),
        ),
      ),
    ),
  );
}

const _heartRed = Color(0xFFE5484D);

/// A mark over a photo's corner: Only 2 left, Sold out.
class _PhotoMark extends StatelessWidget {
  const _PhotoMark(this.words, {this.danger = false});
  final String words;
  final bool danger;

  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
    decoration: BoxDecoration(color: const Color(0xFFFFFFFF), borderRadius: BorderRadius.circular(999)),
    child: Text(words, style: tkText(size: 11, weight: 700, lineHeight: 1.3, color: danger ? const Color(0xFFB42318) : const Color(0xFFB54708))),
  );
}

/// One item on the shop's grid: its photo with the heart and its marks,
/// its name, its price, and + (− n + once in the bag).
class _ItemCard extends StatelessWidget {
  const _ItemCard({required this.src, required this.shop, required this.item, required this.saved, required this.bag, required this.onOpen, required this.onHeart});
  final ShopSource src;
  final ShopData shop;
  final ShopItem item;
  final bool saved;
  final ShopBag bag;
  final VoidCallback onOpen;
  final VoidCallback onHeart;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final sizes = item.sizes ?? const <ShopItemSizesItem>[];
    final from = fromPrice(item);
    final price = from == null ? '' : (sizes.isNotEmpty && sizes.map((z) => z.priceKobo ?? item.priceKobo).toSet().length > 1 ? 'From ${naira(from)}' : naira(from));
    final n = sizes.isEmpty ? bag.of(item.id) : sizes.fold(0, (a, z) => a + bag.of(z.id));
    final sold = !item.available || (sizes.isNotEmpty && sizes.every((z) => !z.available));
    final few = sizes.isEmpty ? item.fewLeft : null;
    final th = sizes.isEmpty ? thingsOf([item])[item.id] : null;
    Widget add;
    if (sold || !shop.open) {
      add = const SizedBox.shrink();
    } else if (sizes.isNotEmpty || th == null) {
      // Sizes are chosen on the item's own screen.
      add = _RoundAdd(label: 'Choose a size of ${item.name}', onTap: onOpen, count: n);
    } else if (n > 0) {
      add = _MiniStepper(
        name: item.name,
        n: n,
        onLess: () => bag.set(item.id, src.stepped(th, n, -1)),
        onMore: few == null || n < few ? () => bag.set(item.id, src.stepped(th, n, 1)) : null,
      );
    } else {
      add = _RoundAdd(label: 'Add ${item.name} to the bag', onTap: () => bag.set(item.id, src.stepped(th, 0, 1)));
    }
    return Opacity(
      opacity: sold ? 0.55 : 1,
      child: Container(
        padding: const EdgeInsets.all(6),
        decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(18)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Semantics(
              button: true,
              label: [item.name, if (price.isNotEmpty) price, if (sold) 'Sold out' else if (few != null) 'Only $few left'].join(', '),
              onTap: onOpen,
              excludeSemantics: true,
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: onOpen,
                child: AspectRatio(
                  aspectRatio: 1,
                  child: Stack(
                    fit: StackFit.expand,
                    children: [
                      ShopPhoto(item: item),
                      if (sold) const Positioned(left: 8, bottom: 8, child: _PhotoMark('Sold out', danger: true)) else if (few != null) Positioned(left: 8, bottom: 8, child: _PhotoMark('Only $few left')),
                    ],
                  ),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(6, 8, 2, 2),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  ExcludeSemantics(child: Text(item.name, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 14, weight: 600, lineHeight: 1.3, color: t.text))),
                  const SizedBox(height: 6),
                  Row(
                    children: [
                      Expanded(child: ExcludeSemantics(child: Text(price, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkDisplay(size: 15.5, lineHeight: 1.3, color: t.text).copyWith(fontFeatures: tkTabular)))),
                      SizedBox(height: 34, child: Center(child: add)),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    ).withHeart(saved: saved, name: item.name, onHeart: onHeart);
  }
}

extension on Widget {
  /// The heart over the photo's top right.
  Widget withHeart({required bool saved, required String name, required VoidCallback onHeart}) => Stack(
    children: [
      this,
      Positioned(
        top: 12,
        right: 12,
        child: _OverPhoto(
          label: saved ? 'Saved: $name. Press to unsave' : 'Save $name',
          icon: 'b-heart',
          color: saved ? _heartRed : const Color(0xFF0E1A14),
          fill: saved,
          onTap: onHeart,
        ),
      ),
    ],
  );
}

/// The lime + of a grid's item; with how many of its sizes are in the bag.
class _RoundAdd extends StatelessWidget {
  const _RoundAdd({required this.label, required this.onTap, this.count = 0});
  final String label;
  final VoidCallback onTap;
  final int count;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TapArea(
      child: Semantics(
        button: true,
        label: count > 0 ? '$label ($count in the bag)' : label,
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: Container(
            height: 34,
            constraints: const BoxConstraints(minWidth: 34),
            padding: EdgeInsets.symmetric(horizontal: count > 0 ? 10 : 0),
            decoration: BoxDecoration(color: look.lime, borderRadius: BorderRadius.circular(17)),
            alignment: Alignment.center,
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (count > 0) Padding(padding: const EdgeInsets.only(right: 4), child: Text('$count', style: tkText(size: 13, weight: 700, color: const Color(0xFF0D3B2E)))),
                const TkIcon('b-plus', size: 18, stroke: 2.6, color: Color(0xFF0D3B2E)),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// − n + on the pine (lime in the dark): an item in the bag.
class _MiniStepper extends StatelessWidget {
  const _MiniStepper({required this.name, required this.n, required this.onLess, this.onMore});
  final String name;
  final int n;
  final VoidCallback onLess;
  final VoidCallback? onMore;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final ink = TkTheme.of(context).mode == TkMode.dark ? t.brandDarkText : const Color(0xFFFFFFFF);
    Widget b(String icon, String label, VoidCallback? on) => TapArea(
      child: Semantics(
        button: true,
        enabled: on != null,
        label: label,
        onTap: on,
        excludeSemantics: true,
        child: TkPress(
          onTap: on ?? () {},
          child: Container(
            width: 26,
            height: 26,
            decoration: BoxDecoration(color: ink.withValues(alpha: on == null ? 0.08 : 0.16), shape: BoxShape.circle),
            alignment: Alignment.center,
            child: TkIcon(icon, size: 14, stroke: 2.4, color: ink.withValues(alpha: on == null ? 0.4 : 1)),
          ),
        ),
      ),
    );
    return Container(
      padding: const EdgeInsets.all(4),
      decoration: BoxDecoration(color: look.on, borderRadius: BorderRadius.circular(999)),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          b('b-minus', 'One fewer $name', onLess),
          Padding(padding: const EdgeInsets.symmetric(horizontal: 7), child: Semantics(label: '$n of $name in the bag', child: Text('$n', style: tkText(size: 13, weight: 700, lineHeight: 1, color: ink)))),
          b('b-plus', 'One more $name', onMore),
        ],
      ),
    );
  }
}

/// − n + on a soft track, the item's and the bag's.
class ShopQty extends StatelessWidget {
  const ShopQty({super.key, required this.name, required this.n, required this.onLess, this.onMore, this.small = false});
  final String name;
  final int n;
  final VoidCallback? onLess;
  final VoidCallback? onMore;
  final bool small;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final side = small ? 28.0 : 34.0;
    Widget b(String icon, String label, VoidCallback? on) => TapArea(
      child: Semantics(
        button: true,
        enabled: on != null,
        label: label,
        onTap: on,
        excludeSemantics: true,
        child: TkPress(
          onTap: on ?? () {},
          child: Container(
            width: side,
            height: side,
            decoration: BoxDecoration(color: look.card, shape: BoxShape.circle),
            alignment: Alignment.center,
            child: TkIcon(icon, size: small ? 14 : 16, stroke: 2.4, color: on == null ? t.muted.withValues(alpha: 0.5) : t.text),
          ),
        ),
      ),
    );
    return Container(
      padding: const EdgeInsets.all(4),
      decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(999)),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          b('b-minus', 'One fewer $name', onLess),
          SizedBox(
            width: small ? 24 : 30,
            child: Semantics(label: '$n of $name', child: Text('$n', textAlign: TextAlign.center, style: tkText(size: small ? 13.5 : 15, weight: 700, color: t.text))),
          ),
          b('b-plus', 'One more $name', onMore),
        ],
      ),
    );
  }
}


// --- an item ---

class ShopItemScreen extends StatefulWidget {
  const ShopItemScreen({super.key, required this.src, required this.shop, required this.item});
  final ShopSource src;
  final ShopData shop;
  final ShopItem item;

  @override
  State<ShopItemScreen> createState() => _ShopItemScreenState();
}

class _ShopItemScreenState extends State<ShopItemScreen> {
  late final ShopBag _bag = ShopBag.of(src.bagKey);
  String? _size;
  late int _n = _first();

  ShopSource get src => widget.src;
  ShopItem get i => widget.item;
  List<ShopItemSizesItem> get _sizes => i.sizes ?? const [];
  Prefs get _prefs => src.prefs;
  ShopTerms? get _terms => src.termsOf(i);

  /// How many Add to bag starts at: a supplier's minimum, until some are in
  /// the bag.
  int _first() {
    final min = _terms?.min ?? 1;
    final th = _thing;
    return th != null && _bag.of(th.id) == 0 && min > 1 ? min : 1;
  }

  @override
  void initState() {
    super.initState();
    _size = _sizes.where((z) => z.available).firstOrNull?.id;
    // Looked at: first of what was seen lately (once drawn: the shop under
    // it redraws its strip).
    WidgetsBinding.instance.addPostFrameCallback((_) => _prefs.viewedShopItem(src.keptKey, i.id));
    _prefs.addListener(_changed);
    _bag.addListener(_changed);
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _prefs.removeListener(_changed);
    _bag.removeListener(_changed);
    super.dispose();
  }

  ShopThing? get _thing => thingsOf([i])[_sizes.isEmpty ? i.id : (_size ?? '')];

  void _bagScreen() => _push(context, ShopBagScreen(src: src, shop: widget.shop));

  void _add() {
    final th = _thing;
    if (th == null) return;
    final now = _bag.of(th.id);
    final n = th.left == null ? now + _n : (now + _n).clamp(0, th.left!);
    _bag.set(th.id, n);
    showTkToast(context, '${_n == 1 ? '' : '$_n × '}${i.name}${th.sizeWords.isEmpty ? '' : ' (${th.sizeWords})'} is in your bag', action: 'View bag', onAction: _bagScreen);
    setState(() => _n = 1);
  }

  /// Back to the chat, the question begun in its box (the person sends it,
  /// changed or not).
  void _ask(Widget? grid) {
    final th = grid == null ? _thing : null;
    final what = [i.name, if (th != null && th.sizeWords.isNotEmpty) th.sizeWords, if ((th?.price ?? i.priceKobo) != null) naira((th?.price ?? i.priceKobo)!)].join(', ');
    src.onAsk?.call('About $what: is it still available?');
    _toChat(context);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final pad = MediaQuery.paddingOf(context);
    final saved = (_prefs.shopSaved[src.keptKey] ?? const <String>[]).contains(i.id);
    final terms = _terms;
    final grid = widget.shop.open ? src.sizesGrid(context, i, _bag) : null;
    final th = _thing;
    final sold = th == null ? !i.available : !th.available;
    final left = th?.left;
    final price = th?.price ?? i.priceKobo;
    // What one costs as Add to bag would leave the bag: a quantity price
    // reached counts.
    final each = th == null ? price : src.priceIn(th, {..._bag.lines, th.id: _bag.of(th.id) + _n});
    // The product in the bag, its sizes together: the quantity price reached.
    final have = _sizes.isEmpty ? _bag.of(i.id) : _sizes.fold(0, (a, z) => a + _bag.of(z.id));
    final more = [
      ...widget.shop.items.where((x) => x.id != i.id && (x.category ?? '') == (i.category ?? '') && (i.category ?? '').isNotEmpty),
      ...widget.shop.items.where((x) => x.id != i.id && ((x.category ?? '') != (i.category ?? '') || (i.category ?? '').isEmpty)),
    ].take(8).toList();
    final share = src.share;
    final ink = TkTheme.of(context).mode == TkMode.dark ? t.brandDarkText : const Color(0xFFFFFFFF);
    final total = src.totalOf(widget.shop, _bag.lines);
    return TkAppLook(
      child: ColoredBox(
        color: look.ground,
        child: Column(
          children: [
            Expanded(
              child: ListView(
                padding: EdgeInsets.zero,
                children: [
                  Stack(
                    children: [
                      AspectRatio(aspectRatio: 1, child: ShopPhoto(item: i, radius: 0, api: src.photos)),
                      Positioned(
                        top: pad.top + 8,
                        left: 12,
                        right: 12,
                        child: Row(
                          children: [
                            _OverPhoto(label: 'Back to the shop', icon: 'back', size: 40, onTap: () => Navigator.of(context).maybePop()),
                            const Spacer(),
                            if (share != null) ...[
                              _OverPhoto(label: 'Share ${i.name} with someone', icon: 'b-share', size: 40, onTap: () => share(context, i)),
                              const SizedBox(width: 10),
                            ],
                            _OverPhoto(
                              label: saved ? 'Saved: ${i.name}. Press to unsave' : 'Save ${i.name}',
                              icon: 'b-heart',
                              size: 40,
                              color: saved ? _heartRed : const Color(0xFF0E1A14),
                              fill: saved,
                              onTap: () => _prefs.toggleShopSaved(src.keptKey, i.id),
                            ),
                          ],
                        ),
                      ),
                    ],
                  ),
                  Transform.translate(
                    offset: const Offset(0, -22),
                    child: Container(
                      decoration: BoxDecoration(color: look.ground, borderRadius: const BorderRadius.vertical(top: Radius.circular(24))),
                      padding: const EdgeInsets.fromLTRB(16, 18, 16, 0),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            [if ((i.category ?? '').isNotEmpty) i.category!, src.name].join(' · ').toUpperCase(),
                            style: tkText(size: 12, weight: 600, letterSpacing: 0.4, color: t.muted),
                          ),
                          const SizedBox(height: 4),
                          Semantics(header: true, child: Text(i.name, style: tkDisplay(size: 24, lineHeight: 1.2, letterSpacing: -0.4, color: t.text))),
                          const SizedBox(height: 6),
                          Wrap(
                            crossAxisAlignment: WrapCrossAlignment.center,
                            spacing: 10,
                            runSpacing: 6,
                            children: [
                              if (price != null)
                                Text.rich(
                                  TextSpan(
                                    text: naira(price),
                                    children: [
                                      if ((terms?.unit ?? '').isNotEmpty) TextSpan(text: ' a ${terms!.unit}', style: tkText(size: 15, weight: 500, color: t.muted)),
                                    ],
                                  ),
                                  style: tkDisplay(size: 22, color: t.text).copyWith(fontFeatures: tkTabular),
                                ),
                              if (sold && grid == null)
                                const CardPill('Sold out', CardTone.danger)
                              else if (left != null && grid == null)
                                CardPill('Only $left left', CardTone.warn),
                            ],
                          ),
                          if ((i.description ?? '').isNotEmpty) ...[
                            const SizedBox(height: 10),
                            Text(i.description!, style: tkText(size: 14.5, lineHeight: 1.5, color: t.muted)),
                          ],
                          if (terms != null && (terms.breaks.isNotEmpty || terms.min > 1) && price != null) ...[
                            const SizedBox(height: 16),
                            ShopQuantityPrices(terms: terms, price: price, have: have),
                          ],
                          if (grid != null) ...[
                            const SizedBox(height: 16),
                            Semantics(header: true, child: Text('How many of each', style: tkDisplay(size: 16, color: t.text))),
                            const SizedBox(height: 4),
                            grid,
                          ] else if (_sizes.isNotEmpty) ...[
                            const SizedBox(height: 16),
                            TkChips<String>(
                              label: 'Size',
                              padding: EdgeInsets.zero,
                              chips: [
                                for (final z in _sizes)
                                  (z.id, [variantWords(z.variant), if ((z.priceKobo ?? i.priceKobo) != null) naira((z.priceKobo ?? i.priceKobo)!)].join(' · ')),
                              ],
                              off: {for (final z in _sizes) if (!z.available) z.id: 'Sold out'},
                              on: _size ?? '',
                              onPick: (v) => setState(() => (_size = v, _n = 1)),
                            ),
                          ],
                          const SizedBox(height: 14),
                          Row(
                            children: [
                              Expanded(child: _CardButton(icon: 'b-chat', words: 'Ask about this', label: 'Ask ${src.name} about ${i.name} in the chat', onTap: src.onAsk == null ? null : () => _ask(grid))),
                              if (share != null) ...[
                                const SizedBox(width: 10),
                                Expanded(
                                  child: _CardButton(icon: 'b-share', words: 'Share with a friend', label: 'Share ${i.name} with a friend', onTap: () => share(context, i)),
                                ),
                              ],
                            ],
                          ),
                          if (more.isNotEmpty) ...[
                            const SizedBox(height: 18),
                            Semantics(header: true, child: Text('More from ${src.name}', style: tkDisplay(size: 16, color: t.text))),
                            const SizedBox(height: 10),
                            SizedBox(
                              height: 110 + 8 + 16 * MediaQuery.textScalerOf(context).scale(1),
                              child: ListView.separated(
                                scrollDirection: Axis.horizontal,
                                itemCount: more.length,
                                separatorBuilder: (_, _) => const SizedBox(width: 10),
                                itemBuilder: (context, k) {
                                  final x = more[k];
                                  void go() => Navigator.of(context).pushReplacement(ShopRoute(ShopItemScreen(src: src, shop: widget.shop, item: x)));
                                  return TapArea(
                                    child: Semantics(
                                      button: true,
                                      label: x.name,
                                      onTap: go,
                                      excludeSemantics: true,
                                      child: TkPress(
                                        onTap: go,
                                        child: SizedBox(
                                          width: 110,
                                          child: Column(
                                            crossAxisAlignment: CrossAxisAlignment.start,
                                            children: [
                                              ShopPhoto(item: x, size: 110, radius: 14, api: src.photos),
                                              const SizedBox(height: 6),
                                              Text(x.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 12.5, lineHeight: 1.3, color: t.muted)),
                                            ],
                                          ),
                                        ),
                                      ),
                                    ),
                                  );
                                },
                              ),
                            ),
                          ],
                          const SizedBox(height: 8),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            ),
            // The foot: how many, and into the bag.
            Container(
              decoration: BoxDecoration(color: t.panel, border: Border(top: BorderSide(color: look.line))),
              padding: EdgeInsets.fromLTRB(14, 12, 14, 12 + pad.bottom),
              child: !widget.shop.open
                  ? Text(
                      (widget.shop.closed ?? '').isNotEmpty ? widget.shop.closed! : '${src.name} is not taking orders right now. Ask them in the chat.',
                      style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted),
                    )
                  : grid != null
                  // Sizes typed in the grid go in the bag as they are typed:
                  // the foot goes to it.
                  ? _FootButton(
                      label: _bag.count == 0 ? 'Type how many of each size' : 'View bag · ${naira(total)}',
                      onTap: _bag.count == 0 ? null : _bagScreen,
                    )
                  : Row(
                      children: [
                        ShopQty(
                          name: i.name,
                          n: _n,
                          onLess: _n > 1 ? () => setState(() => _n--) : null,
                          onMore: sold || (left != null && _bag.of(th?.id ?? '') + _n >= left) ? null : () => setState(() => _n++),
                        ),
                        const SizedBox(width: 12),
                        Expanded(
                          child: TapArea(
                            child: Semantics(
                              button: true,
                              enabled: !sold && th != null,
                              label: sold ? 'Sold out' : 'Add to bag',
                              onTap: sold || th == null ? null : _add,
                              excludeSemantics: true,
                              child: TkPress(
                                onTap: sold || th == null ? () {} : _add,
                                child: Container(
                                  height: 48,
                                  alignment: Alignment.center,
                                  decoration: BoxDecoration(color: sold || th == null ? t.tkSoft : look.on, borderRadius: BorderRadius.circular(999)),
                                  child: Text(
                                    sold ? 'Sold out' : (th == null ? 'Choose a size' : 'Add to bag${each == null ? '' : ' · ${naira(each * _n)}'}'),
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: tkText(size: 15, weight: 700, color: sold || th == null ? t.muted : ink).copyWith(fontFeatures: tkTabular),
                                  ),
                                ),
                              ),
                            ),
                          ),
                        ),
                      ],
                    ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A foot's one button, pine (lime in the dark); soft while it cannot be
/// pressed.
class _FootButton extends StatelessWidget {
  const _FootButton({required this.label, this.onTap});
  final String label;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final ink = TkTheme.of(context).mode == TkMode.dark ? t.brandDarkText : const Color(0xFFFFFFFF);
    return TapArea(
      child: Semantics(
        button: true,
        enabled: onTap != null,
        label: label,
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap ?? () {},
          child: Container(
            constraints: const BoxConstraints(minHeight: 50),
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
            alignment: Alignment.center,
            decoration: BoxDecoration(color: onTap == null ? t.tkSoft : look.on, borderRadius: BorderRadius.circular(999)),
            child: Text(
              label,
              textAlign: TextAlign.center,
              style: tkText(size: 15.5, weight: 700, color: onTap == null ? t.muted : ink).copyWith(fontFeatures: tkTabular),
            ),
          ),
        ),
      ),
    );
  }
}

String _units(String unit, int n) => unit.isEmpty ? '' : (n == 1 || unit.endsWith('s') ? unit : '${unit}s');

/// A supplier's quantity prices, as rows: "1 to 9 bags  ₦45,000 each",
/// "10 bags and more  ₦40,000 each"; the one the bag has reached marked,
/// and the minimum said. A product's sizes count together.
class ShopQuantityPrices extends StatelessWidget {
  const ShopQuantityPrices({super.key, required this.terms, required this.price, required this.have});
  final ShopTerms terms;
  final int price;

  /// How many are in the bag (its sizes together).
  final int have;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final from = terms.min < 1 ? 1 : terms.min;
    final steps = <({int from, int? to, int price})>[];
    final breaks = [for (final b in terms.breaks) if (b.min > from) b]..sort((a, b) => a.min - b.min);
    var at = from;
    var p = price;
    for (final b in terms.breaks.where((b) => b.min <= from)) {
      p = b.price;
    }
    for (final b in breaks) {
      steps.add((from: at, to: b.min - 1, price: p));
      at = b.min;
      p = b.price;
    }
    steps.add((from: at, to: null, price: p));
    final reached = steps.lastIndexWhere((s) => have >= s.from);
    String range(({int from, int? to, int price}) s) {
      final u = _units(terms.unit, s.to ?? s.from);
      if (s.to == null) return '${s.from}${u.isEmpty ? '' : ' $u'} and more';
      if (s.to == s.from) return '${s.from}${u.isEmpty ? '' : ' $u'}';
      return '${s.from} to ${s.to}${u.isEmpty ? '' : ' $u'}';
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Semantics(header: true, child: Text('Quantity prices', style: tkDisplay(size: 16, color: t.text))),
        if (terms.sized && breaks.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 2),
            child: Text(
              'Its sizes count together: ${breaks.last.min}${terms.unit.isEmpty ? '' : ' ${_units(terms.unit, breaks.last.min)}'} across sizes get the ${breaks.last.min}${terms.unit.isEmpty ? '' : '-${terms.unit}'} price.',
              style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted),
            ),
          ),
        const SizedBox(height: 8),
        Container(
          decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(16)),
          padding: const EdgeInsets.symmetric(horizontal: 14),
          child: Column(
            children: [
              for (final (k, s) in steps.indexed)
                Semantics(
                  container: true,
                  label: '${range(s)}: ${naira(s.price)} each${k == reached ? ', your price now' : ''}',
                  excludeSemantics: true,
                  child: Container(
                    constraints: const BoxConstraints(minHeight: 44),
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    decoration: BoxDecoration(border: k == steps.length - 1 ? null : Border(bottom: BorderSide(color: look.line))),
                    child: Row(
                      children: [
                        Expanded(child: Text(range(s), style: tkText(size: 14.5, weight: k == reached ? 700 : 500, color: t.text))),
                        if (k == reached && have > 0) ...[const CardPill('Your price', CardTone.lime), const SizedBox(width: 8)],
                        Text('${naira(s.price)} each', style: tkText(size: 14.5, weight: 700, color: t.text).copyWith(fontFeatures: tkTabular)),
                      ],
                    ),
                  ),
                ),
            ],
          ),
        ),
        if (from > 1)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(
              'At least $from${terms.unit.isEmpty ? '' : ' ${_units(terms.unit, from)}'} an order${terms.sized ? ', its sizes together' : ''}.',
              style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted),
            ),
          ),
      ],
    );
  }
}

/// Ask about this, Share with a friend: a card of a button.
class _CardButton extends StatelessWidget {
  const _CardButton({required this.icon, required this.words, required this.label, this.onTap});
  final String icon;
  final String words;
  final String label;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(
      child: Semantics(
        button: true,
        enabled: onTap != null,
        label: label,
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap ?? () {},
          child: Container(
            constraints: const BoxConstraints(minHeight: 48),
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(14)),
            child: Row(
              children: [
                TkIcon(icon, size: 19, stroke: 2, color: onTap == null ? t.muted : t.text),
                const SizedBox(width: 10),
                Expanded(child: Text(words, style: tkText(size: 14, weight: 600, lineHeight: 1.25, color: onTap == null ? t.muted : t.text))),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

// --- the bag ---

class ShopBagScreen extends StatefulWidget {
  const ShopBagScreen({super.key, required this.src, required this.shop});
  final ShopSource src;
  final ShopData shop;

  @override
  State<ShopBagScreen> createState() => _ShopBagScreenState();
}

class _ShopBagScreenState extends State<ShopBagScreen> {
  late final ShopBag _bag = ShopBag.of(widget.src.bagKey);
  ShopSource get src => widget.src;

  @override
  void initState() {
    super.initState();
    _bag.addListener(_changed);
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _bag.removeListener(_changed);
    super.dispose();
  }

  Future<void> _checkout() async {
    final placed = await src.checkout(context, {..._bag.lines});
    if (!placed || !mounted) return;
    _bag.clear();
    // The order is in the chat: back to it.
    _toChat(context);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final pad = MediaQuery.paddingOf(context);
    final things = thingsOf(widget.shop.items);
    final lines = [for (final e in _bag.lines.entries) if (things[e.key] != null) (things[e.key]!, e.value)];
    final total = src.totalOf(widget.shop, _bag.lines);
    final stops = lines.isEmpty ? null : src.stops(_bag.lines);
    final ink = TkTheme.of(context).mode == TkMode.dark ? t.brandDarkText : const Color(0xFFFFFFFF);
    return TkAppLook(
      child: ColoredBox(
        color: look.ground,
        child: Column(
          children: [
            Container(
              decoration: BoxDecoration(color: t.panel, border: Border(bottom: BorderSide(color: look.line))),
              padding: EdgeInsets.fromLTRB(8, pad.top + 6, 16, 10),
              child: Row(
                children: [
                  ChatHeadBar.headButton('Back to the shop', TkIcon('back', size: 24, stroke: 2, color: t.text), () => Navigator.of(context).maybePop()),
                  const SizedBox(width: 4),
                  Expanded(child: Semantics(header: true, child: Text('Your bag', style: tkDisplay(size: 19, color: t.text)))),
                  Flexible(child: Text(src.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 13.5, weight: 600, color: t.muted))),
                ],
              ),
            ),
            Expanded(
              child: lines.isEmpty
                  ? ListView(children: const [TkEmpty(icon: 'b-bag', title: 'Your bag is empty', line: 'Press + on what you want in the shop.')])
                  : ListView(
                      padding: const EdgeInsets.fromLTRB(14, 14, 14, 16),
                      children: [
                        Container(
                          decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(18)),
                          padding: const EdgeInsets.symmetric(horizontal: 10),
                          child: Column(
                            children: [
                              for (final (k, (th, n)) in lines.indexed)
                                Container(
                                  padding: const EdgeInsets.symmetric(vertical: 10),
                                  decoration: BoxDecoration(border: k == lines.length - 1 ? null : Border(bottom: BorderSide(color: look.line))),
                                  child: Builder(
                                    builder: (context) {
                                      final each = src.priceIn(th, _bag.lines);
                                      final less = each != null && th.price != null && each < th.price!;
                                      return Row(
                                        children: [
                                          ShopPhoto(item: th.item, size: 60, radius: 12, api: src.photos),
                                          const SizedBox(width: 12),
                                          Expanded(
                                            child: Column(
                                              crossAxisAlignment: CrossAxisAlignment.start,
                                              children: [
                                                Text(th.item.name, style: tkText(size: 14.5, weight: 600, lineHeight: 1.3, color: t.text)),
                                                Text(
                                                  [if (th.sizeWords.isNotEmpty) th.sizeWords, if (each != null) '${naira(each)} each', if (less) 'quantity price'].join(' · '),
                                                  style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted).copyWith(fontFeatures: tkTabular),
                                                ),
                                                if (each != null) Text(naira(each * n), style: tkDisplay(size: 14.5, lineHeight: 1.35, color: t.text).copyWith(fontFeatures: tkTabular)),
                                              ],
                                            ),
                                          ),
                                          const SizedBox(width: 8),
                                          ShopQty(
                                            small: true,
                                            name: th.item.name,
                                            n: n,
                                            onLess: () => _bag.set(th.id, src.stepped(th, n, -1)),
                                            onMore: th.left == null || n < th.left! ? () => _bag.set(th.id, src.stepped(th, n, 1)) : null,
                                          ),
                                        ],
                                      );
                                    },
                                  ),
                                ),
                            ],
                          ),
                        ),
                        const SizedBox(height: 12),
                        Container(
                          decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(18)),
                          padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
                          child: Row(
                            children: [
                              Expanded(child: Text('Items', style: tkText(size: 15, weight: 700, color: t.text))),
                              Text(naira(total), style: tkDisplay(size: 16, color: t.text).copyWith(fontFeatures: tkTabular)),
                            ],
                          ),
                        ),
                        Padding(
                          padding: const EdgeInsets.fromLTRB(4, 10, 4, 0),
                          child: Text(stops ?? widget.shop.next, style: tkText(size: 13, lineHeight: 1.4, color: stops != null ? t.text : t.muted)),
                        ),
                        const SizedBox(height: 14),
                        if (src.onAsk != null)
                          _CardButton(
                            icon: 'b-chat',
                            words: 'Have a question? Ask ${src.name} in the chat',
                            label: 'Ask ${src.name} in the chat',
                            onTap: () => _toChat(context),
                          ),
                      ],
                    ),
            ),
            if (lines.isNotEmpty && widget.shop.open)
              Container(
                decoration: BoxDecoration(color: t.panel, border: Border(top: BorderSide(color: look.line))),
                padding: EdgeInsets.fromLTRB(14, 12, 14, 12 + pad.bottom),
                child: TapArea(
                  child: Semantics(
                    button: true,
                    enabled: stops == null,
                    label: '${src.checkoutWords}, ${naira(total)}',
                    onTap: stops == null ? _checkout : null,
                    excludeSemantics: true,
                    child: TkPress(
                      onTap: stops == null ? _checkout : () {},
                      child: Container(
                        height: 50,
                        alignment: Alignment.center,
                        decoration: BoxDecoration(color: stops == null ? look.on : t.tkSoft, borderRadius: BorderRadius.circular(999)),
                        child: Text(
                          '${src.checkoutWords} · ${naira(total)}',
                          style: tkText(size: 15.5, weight: 700, color: stops == null ? ink : t.muted).copyWith(fontFeatures: tkTabular),
                        ),
                      ),
                    ),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
