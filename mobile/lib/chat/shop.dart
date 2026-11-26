// The shop (docs/spec/mobile-app-look.md, "The shop"): a business's items to
// browse as a window is shopped, from a person's chat with it. A screen of
// its own: the items as photos two across, searched and filtered by their
// categories, the ones saved with the heart and the ones looked at lately;
// an item's own screen (its photo, sizes, Ask about this, Share); and the
// bag, which checks out in the shop's sheet (my_chat_tools.dart ShopSheet,
// pickup or delivery). The bag stays while the app runs, as the chat does;
// the hearts and what was looked at are kept on the phone (Prefs), the
// server keeping neither yet.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/prefs.dart';
import '../app/session.dart' show MemoryKeyStore;
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
import 'cards.dart' show CardPill;
import 'door.dart' show BusinessFace;
import 'my_chat.dart';
import 'my_chat_tools.dart' show ShopSheet, variantWords;
import 'people_cards.dart' show shareWithPeople;
import 'thread.dart' show ChatHeadBar;
import 'words.dart';

/// Opens the shop of [d]'s business; at [item] when a card in the chat
/// named one.
Future<void> openShop(BuildContext context, MyChatData d, {String? item}) => Navigator.of(context).push<void>(ShopRoute(ShopScreen(d: d, item: item)));

/// A screen of the shop: back to the chat closes them all at once.
class ShopRoute<T> extends TkRoute<T> {
  ShopRoute(Widget page) : super(pageBuilder: (_, _, _) => page);
}

/// Back to the chat the shop was opened from.
void _toChat(BuildContext context) => Navigator.of(context).popUntil((r) => r is! ShopRoute);

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

  void clear() {
    lines.clear();
    notifyListeners();
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

Map<String, ShopThing> shopThings(MyChatShopResponse s) => {
  for (final i in s.items)
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

/// The hearts and what was looked at: on the phone (Prefs), or, where the
/// chat was opened without them (a test), while its chat is open.
Prefs _kept(MyChatData d) => d.prefs ?? (_memory[d] ??= Prefs(MemoryKeyStore()));
final _memory = Expando<Prefs>('shop');

String _business(MyChatData d) => (d.chat?.business ?? '').isNotEmpty ? d.chat!.business : d.id;

// --- the shop ---

class ShopScreen extends StatefulWidget {
  const ShopScreen({super.key, required this.d, this.item});
  final MyChatData d;

  /// Opened on this item (a product card in the chat).
  final String? item;

  @override
  State<ShopScreen> createState() => _ShopScreenState();
}

class _ShopScreenState extends State<ShopScreen> {
  MyChatShopResponse? _shop;
  String? _error;
  final _search = TextEditingController();
  String _filter = 'all';
  late final ShopBag _bag = ShopBag.of(d.id);

  MyChatData get d => widget.d;
  Prefs get _prefs => _kept(d);

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
      final s = await d.api.myChatShop(id: d.id);
      if (!mounted) return;
      setState(() => _shop = s);
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

  Future<void> _open(ShopItem i) => _push(context, ShopItemScreen(d: d, shop: _shop!, item: i));

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final s = _shop;
    final pad = MediaQuery.paddingOf(context);
    final c = d.chat;
    final saved = _prefs.shopSaved[_business(d)] ?? const <String>[];
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
                  avatar: BusinessFace(name: d.name, urls: c?.photo?.urls, size: 40, letterSize: 15),
                  title: d.name,
                  subtitle: s == null ? 'Their shop' : _ways(s),
                  onBack: () => Navigator.of(context).maybePop(),
                  actions: [
                    ChatHeadBar.headButton(
                      _filter == 'saved' ? 'Show all of the shop' : 'Show what you saved',
                      TkIcon('b-heart', size: 22, stroke: 2, color: _filter == 'saved' ? t.danger : t.text, fill: _filter == 'saved'),
                      () => setState(() => _filter = _filter == 'saved' ? 'all' : 'saved'),
                    ),
                    _BagButton(count: _bag.count, onTap: s == null ? null : () => _openBag(s)),
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

  String _ways(MyChatShopResponse s) {
    if (!s.open) return 'Not taking orders now';
    final how = s.pickup && s.delivery ? 'Delivery and pickup' : (s.delivery ? 'Delivery' : (s.pickup ? 'Pickup' : ''));
    return how.isEmpty ? 'Open' : 'Open · $how';
  }

  Future<void> _openBag(MyChatShopResponse s) => _push(context, ShopBagScreen(d: d, shop: s));

  Widget _body(BuildContext context, MyChatShopResponse s, List<String> saved) {
    final t = TkTheme.tokens(context);
    if (s.items.isEmpty) {
      return ListView(children: [TkEmpty(icon: 'b-box', title: '${d.name} has nothing in their shop yet', line: 'Ask them in the chat.')]);
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
    final recent = [for (final id in _prefs.shopRecent[_business(d)] ?? const <String>[]) ?byId[id]];
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
                      hint: 'Search ${d.name}',
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
                                      ShopPhoto(item: i, size: 64, radius: 14),
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
                          d: d,
                          shop: s,
                          item: shown[k],
                          saved: saved.contains(shown[k].id),
                          bag: _bag,
                          onOpen: () => _open(shown[k]),
                          onHeart: () => _prefs.toggleShopSaved(_business(d), shown[k].id),
                        ),
                        childCount: shown.length,
                      ),
                    ),
                  ),
              ],
            );
          },
        ),
        if (_bag.count > 0)
          Positioned(
            left: 12,
            right: 12,
            bottom: 12 + MediaQuery.paddingOf(context).bottom,
            child: ShopBagBar(count: _bag.count, total: _total(s), onTap: () => _openBag(s)),
          ),
      ],
    );
  }

  int _total(MyChatShopResponse s) {
    final things = shopThings(s);
    return _bag.lines.entries.fold(0, (a, e) => a + (things[e.key]?.price ?? 0) * e.value);
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
  const ShopPhoto({super.key, required this.item, this.size, this.radius = 14});
  final ShopItem item;

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
    Widget pic(double side) => url == null || !url.startsWith(RegExp('https?://'))
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
  const _ItemCard({required this.d, required this.shop, required this.item, required this.saved, required this.bag, required this.onOpen, required this.onHeart});
  final MyChatData d;
  final MyChatShopResponse shop;
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
    Widget add;
    if (sold || !shop.open) {
      add = const SizedBox.shrink();
    } else if (sizes.isNotEmpty) {
      // Sizes are chosen on the item's own screen.
      add = _RoundAdd(label: 'Choose a size of ${item.name}', onTap: onOpen, count: n);
    } else if (n > 0) {
      add = _MiniStepper(
        name: item.name,
        n: n,
        onLess: () => bag.set(item.id, n - 1),
        onMore: few == null || n < few ? () => bag.set(item.id, n + 1) : null,
      );
    } else {
      add = _RoundAdd(label: 'Add ${item.name} to the bag', onTap: () => bag.set(item.id, 1));
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
  const ShopItemScreen({super.key, required this.d, required this.shop, required this.item});
  final MyChatData d;
  final MyChatShopResponse shop;
  final ShopItem item;

  @override
  State<ShopItemScreen> createState() => _ShopItemScreenState();
}

class _ShopItemScreenState extends State<ShopItemScreen> {
  late final ShopBag _bag = ShopBag.of(d.id);
  String? _size;
  int _n = 1;

  MyChatData get d => widget.d;
  ShopItem get i => widget.item;
  List<ShopItemSizesItem> get _sizes => i.sizes ?? const [];
  Prefs get _prefs => _kept(d);

  @override
  void initState() {
    super.initState();
    _size = _sizes.where((z) => z.available).firstOrNull?.id;
    // Looked at: first of what was seen lately (once drawn: the shop under
    // it redraws its strip).
    WidgetsBinding.instance.addPostFrameCallback((_) => _prefs.viewedShopItem(_business(d), i.id));
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

  ShopThing? get _thing => shopThings(widget.shop)[_sizes.isEmpty ? i.id : (_size ?? '')];

  void _add() {
    final th = _thing;
    if (th == null) return;
    final now = _bag.of(th.id);
    final n = th.left == null ? now + _n : (now + _n).clamp(0, th.left!);
    _bag.set(th.id, n);
    showTkToast(context, '${_n == 1 ? '' : '$_n × '}${i.name}${th.sizeWords.isEmpty ? '' : ' (${th.sizeWords})'} is in your bag', action: 'View bag', onAction: () => _push(context, ShopBagScreen(d: d, shop: widget.shop)));
    setState(() => _n = 1);
  }

  /// Back to the chat, the question begun in its box (the person sends it,
  /// changed or not).
  void _ask() {
    final th = _thing;
    final what = [i.name, if (th != null && th.sizeWords.isNotEmpty) th.sizeWords, if ((th?.price ?? i.priceKobo) != null) naira((th?.price ?? i.priceKobo)!)].join(', ');
    d.onAsk?.call('About $what: is it still available?');
    _toChat(context);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final pad = MediaQuery.paddingOf(context);
    final saved = (_prefs.shopSaved[_business(d)] ?? const <String>[]).contains(i.id);
    final th = _thing;
    final sold = th == null ? !i.available : !th.available;
    final left = th?.left;
    final price = th?.price ?? i.priceKobo;
    final more = [
      ...widget.shop.items.where((x) => x.id != i.id && (x.category ?? '') == (i.category ?? '') && (i.category ?? '').isNotEmpty),
      ...widget.shop.items.where((x) => x.id != i.id && ((x.category ?? '') != (i.category ?? '') || (i.category ?? '').isEmpty)),
    ].take(8).toList();
    final canShare = (d.chat?.business ?? '').isNotEmpty;
    final ink = TkTheme.of(context).mode == TkMode.dark ? t.brandDarkText : const Color(0xFFFFFFFF);
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
                      AspectRatio(aspectRatio: 1, child: ShopPhoto(item: i, radius: 0)),
                      Positioned(
                        top: pad.top + 8,
                        left: 12,
                        right: 12,
                        child: Row(
                          children: [
                            _OverPhoto(label: 'Back to the shop', icon: 'back', size: 40, onTap: () => Navigator.of(context).maybePop()),
                            const Spacer(),
                            if (canShare) ...[
                              _OverPhoto(
                                label: 'Share ${i.name} with someone',
                                icon: 'b-share',
                                size: 40,
                                onTap: () => shareWithPeople(context, d.auth, SendPeopleMessageRequestCard(type: 'product', businessId: d.chat!.business, itemId: i.id), what: i.name),
                              ),
                              const SizedBox(width: 10),
                            ],
                            _OverPhoto(
                              label: saved ? 'Saved: ${i.name}. Press to unsave' : 'Save ${i.name}',
                              icon: 'b-heart',
                              size: 40,
                              color: saved ? _heartRed : const Color(0xFF0E1A14),
                              fill: saved,
                              onTap: () => _prefs.toggleShopSaved(_business(d), i.id),
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
                            [if ((i.category ?? '').isNotEmpty) i.category!, d.name].join(' · ').toUpperCase(),
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
                              if (price != null) Text(naira(price), style: tkDisplay(size: 22, color: t.text).copyWith(fontFeatures: tkTabular)),
                              if (sold)
                                const CardPill('Sold out', CardTone.danger)
                              else if (left != null)
                                CardPill('Only $left left', CardTone.warn),
                            ],
                          ),
                          if ((i.description ?? '').isNotEmpty) ...[
                            const SizedBox(height: 10),
                            Text(i.description!, style: tkText(size: 14.5, lineHeight: 1.5, color: t.muted)),
                          ],
                          if (_sizes.isNotEmpty) ...[
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
                              Expanded(child: _CardButton(icon: 'b-chat', words: 'Ask about this', label: 'Ask ${d.name} about ${i.name} in the chat', onTap: d.onAsk == null ? null : _ask)),
                              if (canShare) ...[
                                const SizedBox(width: 10),
                                Expanded(
                                  child: _CardButton(
                                    icon: 'b-share',
                                    words: 'Share with a friend',
                                    label: 'Share ${i.name} with a friend',
                                    onTap: () => shareWithPeople(context, d.auth, SendPeopleMessageRequestCard(type: 'product', businessId: d.chat!.business, itemId: i.id), what: i.name),
                                  ),
                                ),
                              ],
                            ],
                          ),
                          if (more.isNotEmpty) ...[
                            const SizedBox(height: 18),
                            Semantics(header: true, child: Text('More from ${d.name}', style: tkDisplay(size: 16, color: t.text))),
                            const SizedBox(height: 10),
                            SizedBox(
                              height: 110 + 8 + 16 * MediaQuery.textScalerOf(context).scale(1),
                              child: ListView.separated(
                                scrollDirection: Axis.horizontal,
                                itemCount: more.length,
                                separatorBuilder: (_, _) => const SizedBox(width: 10),
                                itemBuilder: (context, k) {
                                  final x = more[k];
                                  return TapArea(
                                    child: Semantics(
                                      button: true,
                                      label: x.name,
                                      onTap: () => Navigator.of(context).pushReplacement(ShopRoute(ShopItemScreen(d: d, shop: widget.shop, item: x))),
                                      excludeSemantics: true,
                                      child: TkPress(
                                        onTap: () => Navigator.of(context).pushReplacement(ShopRoute(ShopItemScreen(d: d, shop: widget.shop, item: x))),
                                        child: SizedBox(
                                          width: 110,
                                          child: Column(
                                            crossAxisAlignment: CrossAxisAlignment.start,
                                            children: [
                                              ShopPhoto(item: x, size: 110, radius: 14),
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
                      (widget.shop.closed ?? '').isNotEmpty ? widget.shop.closed! : '${d.name} is not taking orders right now. Ask them in the chat.',
                      style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted),
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
                                    sold ? 'Sold out' : (th == null ? 'Choose a size' : 'Add to bag${price == null ? '' : ' · ${naira(price * _n)}'}'),
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
  const ShopBagScreen({super.key, required this.d, required this.shop});
  final MyChatData d;
  final MyChatShopResponse shop;

  @override
  State<ShopBagScreen> createState() => _ShopBagScreenState();
}

class _ShopBagScreenState extends State<ShopBagScreen> {
  late final ShopBag _bag = ShopBag.of(widget.d.id);

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
    var placed = false;
    await showTkSheet<void>(context, (_) => ShopSheet(d: widget.d, bag: {..._bag.lines}, onPlaced: () => placed = true));
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
    final things = shopThings(widget.shop);
    final lines = [for (final e in _bag.lines.entries) if (things[e.key] != null) (things[e.key]!, e.value)];
    final total = lines.fold(0, (a, l) => a + (l.$1.price ?? 0) * l.$2);
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
                  Flexible(child: Text(widget.d.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 13.5, weight: 600, color: t.muted))),
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
                                  child: Row(
                                    children: [
                                      ShopPhoto(item: th.item, size: 60, radius: 12),
                                      const SizedBox(width: 12),
                                      Expanded(
                                        child: Column(
                                          crossAxisAlignment: CrossAxisAlignment.start,
                                          children: [
                                            Text(th.item.name, style: tkText(size: 14.5, weight: 600, lineHeight: 1.3, color: t.text)),
                                            Text(
                                              [if (th.sizeWords.isNotEmpty) th.sizeWords, if (th.price != null) '${naira(th.price!)} each'].join(' · '),
                                              style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted).copyWith(fontFeatures: tkTabular),
                                            ),
                                            if (th.price != null) Text(naira(th.price! * n), style: tkDisplay(size: 14.5, lineHeight: 1.35, color: t.text).copyWith(fontFeatures: tkTabular)),
                                          ],
                                        ),
                                      ),
                                      const SizedBox(width: 8),
                                      ShopQty(
                                        small: true,
                                        name: th.item.name,
                                        n: n,
                                        onLess: () => _bag.set(th.id, n - 1),
                                        onMore: th.left == null || n < th.left! ? () => _bag.set(th.id, n + 1) : null,
                                      ),
                                    ],
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
                          child: Text(
                            widget.shop.delivery ? 'How you get it, and delivery if you want it, come next.' : 'How you get it comes next.',
                            style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
                          ),
                        ),
                        const SizedBox(height: 14),
                        if (widget.d.onAsk != null)
                          _CardButton(
                            icon: 'b-chat',
                            words: 'Have a question? Ask ${widget.d.name} in the chat',
                            label: 'Ask ${widget.d.name} in the chat',
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
                    label: 'Check out, ${naira(total)}',
                    onTap: _checkout,
                    excludeSemantics: true,
                    child: TkPress(
                      onTap: _checkout,
                      child: Container(
                        height: 50,
                        alignment: Alignment.center,
                        decoration: BoxDecoration(color: look.on, borderRadius: BorderRadius.circular(999)),
                        child: Text('Check out · ${naira(total)}', style: tkText(size: 15.5, weight: 700, color: ink).copyWith(fontFeatures: tkTabular)),
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
