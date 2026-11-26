// A business's shop (docs/spec/mobile-app-look.md, "The business shop";
// docs/spec/business-links.md §5.5): a linked supplier's things to window
// shop from the conversation with them, in the person's shop's screens
// (shop.dart). What differs is here, and comes from the order sheet
// (order_sheet.dart, M5 part O), which stays the plain list:
//
// - the prices are theirs for this link (GET /v1/supplier-prices?link=),
//   the price list they share with us, else their storefront's; with their
//   quantity prices, a product's sizes counted together (buyer.dart Bag);
// - + from nothing goes to their minimum; a product sold in sizes is
//   ordered in the order sheet's sizes grid, on its item's screen;
// - the bag checks out in the order sheet's review (which supplier they
//   are, how each line is treated, a note, Make an offer, Place order or
//   Ask for a quote): a purchase order, its card in the conversation;
// - only those who may order (the sheet's browse otherwise) have a bag;
// - the hearts and what was looked at are kept on the phone, by the
//   business we act for and the supplier, and forgotten on signing out
//   (Prefs.forgetPerson), as the person's are.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show SendPeopleMessageRequestCard, ShopItem, ShopItemSizesItem;
import '../api/transport.dart' show Transport;
import '../app/prefs.dart';
import 'buyer.dart';
import 'chats.dart';
import 'order_sheet.dart' show OrderStart, SizesGrid, showOrderSheet;
import 'people_cards.dart' show shareWithPeople;
import 'shop.dart';

/// A linked supplier's shop, from our conversation with them.
class BusinessShop extends ShopSource {
  BusinessShop({
    required this.chats,
    required this.link,
    required this.business,
    required this.name,
    required this.face,
    required this.supplies,
    this.onAsk,
    this.onSent,
  });

  final ChatsData chats;

  /// The link between us.
  final String link;

  /// Their business.
  final String business;
  @override
  final String name;
  final Widget Function() face;

  /// We may order from them (drafting needs table.edit; sending and the
  /// supplier, storefront.manage): else their shop is to look around.
  final bool supplies;

  @override
  final ValueChanged<String>? onAsk;

  /// Sent (an order, an offer, a quote asked), with its words.
  final void Function(String words)? onSent;

  List<PriceItem> _items = const [];
  bool _storefront = false;

  @override
  String get bagKey => 'link:$link';

  @override
  String get keptKey => 'business:${chats.auth.me?.tenantId ?? ''}:$business';

  @override
  Widget avatar() => face();

  @override
  Prefs get prefs => keptPrefs(chats.prefs, chats);

  @override
  Transport? get photos => chats.auth.transport;

  @override
  bool get bags => supplies;

  @override
  String get checkoutWords => 'Review order';

  // A product from their storefront can be shared as the person's shop
  // shares it (their public page, at its prices); a price list's cannot.
  @override
  void Function(BuildContext, ShopItem)? get share => !_storefront || business.isEmpty
      ? null
      : (context, i) => shareWithPeople(context, chats.auth, SendPeopleMessageRequestCard(type: 'product', businessId: business, itemId: i.id), what: i.name);

  @override
  Future<ShopData> load() async {
    final r = await chats.auth.transport.send('GET', '/v1/supplier-prices', query: {'link': link}) as Map<String, dynamic>;
    final list = (r['suppliers'] as List? ?? const []).whereType<Map<String, dynamic>>().firstOrNull;
    _items = [for (final j in (list?['items'] as List? ?? const []).whereType<Map<String, dynamic>>()) PriceItem.fromJson(j)];
    _storefront = list?['name'] == storefrontPrices;
    final updatedAt = DateTime.tryParse(list?['updated_at'] as String? ?? '')?.toLocal();
    final now = chats.now();
    final updated = updatedAt != null && updatedAt.year == now.year && updatedAt.month == now.month && updatedAt.day == now.day ? 'updated today' : 'as they share them';
    return ShopData(
      items: [for (final it in _items) shopItemOf(it, link)],
      open: supplies,
      closed: supplies ? null : 'You can look around. Placing orders with $name is for those on your team who may.',
      // Short, for the head: the date only when it is today's.
      ways: '${_storefront ? 'Their storefront prices' : 'Your prices'}${updated == 'updated today' ? ' · updated today' : ''}',
      next: 'Their prices for these quantities. Next, the review: how each line is treated, a note or an offer, then Place order.',
    );
  }

  PriceItem? _item(String id) => _items.where((x) => x.id == id).firstOrNull;
  PriceItem? _flat(String id) => flatItems(_items).where((x) => x.id == id).firstOrNull;

  @override
  int? priceIn(ShopThing th, Map<String, int> lines) {
    final it = _flat(th.id);
    return it == null ? th.price : Bag(_items, lines).unitPrice(it);
  }

  @override
  int stepped(ShopThing th, int n, int dir) {
    final it = _flat(th.id);
    // A size: one at a time (its product's minimum counts its sizes
    // together, said in the bag).
    if (it == null || it.variantOf != null) return super.stepped(th, n, dir);
    return stepQty(n, it.min, dir);
  }

  @override
  ShopTerms? termsOf(ShopItem item) {
    final it = _item(item.id);
    return it == null ? null : ShopTerms(unit: it.unit, min: it.min, breaks: it.breaks, sized: soldInSizes(it));
  }

  @override
  Widget? sizesGrid(BuildContext context, ShopItem item, ShopBag bag) {
    final it = _item(item.id);
    if (it == null || !soldInSizes(it) || it.sizes.isEmpty || !it.available) return null;
    return SizesGrid(product: it, qty: {...bag.lines}, onQty: bag.set);
  }

  @override
  String? stops(Map<String, int> lines) {
    final short = shortOf(_items, lines);
    return short == null ? null : '${short.name}: at least ${short.min} in all, its sizes together.';
  }

  @override
  Future<void> Function(BuildContext, ShopBag)? get list => (context, bag) async {
    var sent = false;
    await showOrderSheet(
      context,
      chats: chats,
      link: link,
      business: name,
      start: OrderStart(qty: {...bag.lines}),
      browse: !supplies,
      onQty: bag.setAll,
      onSent: (words) {
        sent = true;
        onSent?.call(words);
      },
    );
    // Sent from the list: the bag went with it, and the card is in the
    // conversation.
    if (sent && context.mounted) {
      bag.clear();
      shopToChat(context);
    }
  };

  @override
  Future<bool> checkout(BuildContext context, Map<String, int> lines) async {
    var sent = false;
    await showOrderSheet(
      context,
      chats: chats,
      link: link,
      business: name,
      start: OrderStart(qty: lines, review: true),
      onSent: (words) {
        sent = true;
        onSent?.call(words);
      },
    );
    return sent;
  }
}

/// A supplier's item as the shop draws it: its photo read through the link,
/// a product's sizes by their choices ("42, Black"), each at its own price
/// where it has one.
ShopItem shopItemOf(PriceItem it, String link) {
  final enc = Uri.encodeComponent(link);
  return ShopItem(
    id: it.id,
    name: it.name,
    description: it.description.isEmpty ? null : it.description,
    category: it.category.isEmpty ? null : it.category,
    priceKobo: it.price,
    photo: it.photo ? '/v1/link-chats/$enc/photo?item=${Uri.encodeQueryComponent(it.id)}&w=640' : null,
    // A product sold in sizes with none on sale now: nothing to order.
    available: it.available && !(soldInSizes(it) && it.sizes.isEmpty),
    sizes: it.sizes.isEmpty
        ? null
        : [
            for (final z in it.sizes)
              ShopItemSizesItem(
                id: z.id,
                available: z.available,
                priceKobo: (z.price ?? 0) > 0 && !it.overridden ? z.price : null,
                variant: {'size': z.label.isEmpty ? z.name : z.label},
              ),
          ],
  );
}
