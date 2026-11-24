// A live card (web/src/pages/ChatLiveCard.tsx; chat.css .tk-live): a
// product or a service tagged in a message, with what is true now: the
// price on the conversation's price list today (and what it was when
// tagged), our own stock of it, and Order when we buy from them.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../kit/icon.dart';
import '../kit/modal.dart';
import '../kit/tap_area.dart';
import '../kit/mobile.dart' show tkInitials;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'cards.dart' show CardPill;
import 'tags.dart';
import 'words.dart';

/// ChatLiveCard.tsx liveMention: the first product or service tagged.
LinkMention? liveTag(List<LinkMention>? mentions) => mentions?.where((m) => m.kind == 'item' || m.kind == 'service').firstOrNull;

class LiveCard extends StatelessWidget {
  const LiveCard({super.key, required this.m, required this.items, required this.whose, required this.stock, this.onOrder});
  final LinkMention m;

  /// The conversation's price list, and whose it is.
  final List<PriceItem> items;
  final String whose;

  /// Our own stock (when we may see it).
  final List<StockItem> stock;

  /// Order: their shop opened on [m]'s item.
  final ValueChanged<String>? onOrder;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    // The product's letters on a soft square of its own accent (a service:
    // a clock on blue), as its rows are drawn on Stock.
    final accents = [(look.violet, look.violetInk), (look.orange, look.orangeInk), (look.blue, look.blueInk), (look.teal, look.tealInk), (look.lime, look.limeInk)];
    final (fill, ink) = accents[m.name.codeUnits.fold<int>(0, (a, c) => (a * 31 + c) & 0x7fffffff) % accents.length];
    Widget tile({bool service = false}) => ExcludeSemantics(
      child: Container(
        width: 54,
        height: 54,
        decoration: BoxDecoration(color: (service ? look.blue : fill).withValues(alpha: 0.2), borderRadius: BorderRadius.circular(16)),
        alignment: Alignment.center,
        child: service
            ? TkIcon('clock', size: 24, stroke: 2, color: look.blueInk)
            : Text(tkInitials(m.name), textScaler: TextScaler.noScaling, style: tkText(size: 15, weight: 800, color: ink)),
      ),
    );
    Widget words(String kicker, List<String> subs) => Expanded(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(kicker.toUpperCase(), semanticsLabel: kicker, style: tkText(size: 11, weight: 700, letterSpacing: 0.66, color: t.muted)),
          const SizedBox(height: 2),
          Text(m.name, style: tkDisplay(size: 18, lineHeight: 1.2, letterSpacing: -0.4, color: t.text)),
          for (final s in subs) ...[const SizedBox(height: 2), Text(s, style: tkText(size: 13, color: t.muted))],
        ],
      ),
    );
    Widget frame(String label, List<Widget> children) => Semantics(
      container: true,
      label: label,
      explicitChildNodes: true,
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: children),
    );
    if (m.kind == 'service') {
      return frame('Service: ${m.name}', [
        Padding(
          padding: EdgeInsets.zero,
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              tile(service: true),
              const SizedBox(width: 12),
              words('Service', ['${m.priceKobo != null ? naira(m.priceKobo!) : 'Price on the day'}${m.minutes != null ? ' · ${m.minutes} min' : ''}']),
            ],
          ),
        ),
      ]);
    }
    final now = items.where((it) => it.itemId == m.itemId).firstOrNull;
    final price = now?.priceKobo ?? m.priceKobo;
    final changed = now != null && m.priceKobo != null && now.priceKobo != m.priceKobo;
    final unit = now?.unit ?? m.unit;
    bool same(String a, String b) => a.trim().toLowerCase() == b.trim().toLowerCase();
    final mine = stock.where((it) => it.variantOf == null && same(it.name, m.name)).firstOrNull;
    final ours = mine?.stock is num ? (mine!.stock as num) : null;
    final sub = StringBuffer(price != null ? '${naira(price)}${(unit ?? '').isNotEmpty ? ' a $unit' : ''}' : 'No price');
    if (now != null && now.minQuantity > 1) sub.write(' · min ${now.minQuantity}');
    if (changed) sub.write(' (was ${naira(m.priceKobo!)})');
    if (now != null && !now.available) sub.write(' · not available now');
    final canOrder = whose == 'theirs' && now?.available != false;
    return frame('Product: ${m.name}', [
      Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [tile(), const SizedBox(width: 12), words('Product', [sub.toString()])],
      ),
      if (ours != null || whose == 'theirs')
        Container(
          // Its foot, under a hairline: your stock, and Order.
          margin: const EdgeInsets.only(top: 10),
          padding: const EdgeInsets.only(top: 10),
          decoration: BoxDecoration(border: Border(top: BorderSide(color: look.line))),
          // Its two ends; one under the other when the words are large.
          child: Wrap(
            alignment: WrapAlignment.spaceBetween,
            crossAxisAlignment: WrapCrossAlignment.center,
            spacing: 8,
            runSpacing: 8,
            // Each as wide as its words, not the line (a wrap gives the line).
            children: [
              if (mine != null && ours != null)
                CardPill(
                  'Your stock: ${ours <= 0 ? 'none' : '${quantity(ours, mine.unit)}${mine.low == true ? ' left' : ''}'}',
                  mine.low == true || ours <= 0 ? CardTone.warn : CardTone.lime,
                )
              else
                const SizedBox.shrink(),
              if (canOrder)
                TapArea(child: Semantics(
                  button: true,
                  label: 'Order ${m.name}',
                  onTap: onOrder == null || m.itemId == null ? () => showComingSoon(context) : () => onOrder!(m.itemId!),
                  excludeSemantics: true,
                  child: GestureDetector(
                    // Their shop, opened on this item (in the bag when we may order).
                    onTap: onOrder == null || m.itemId == null ? () => showComingSoon(context) : () => onOrder!(m.itemId!),
                    child: IntrinsicWidth(
                      child: Container(
                        height: 40,
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        alignment: Alignment.center,
                        decoration: BoxDecoration(color: look.on, borderRadius: BorderRadius.circular(999)),
                        child: Text(
                          'Order',
                          style: tkText(size: 14, weight: 700, lineHeight: 1.2, color: dark ? t.brandDarkText : const Color(0xFFFFFFFF)),
                        ),
                      ),
                    ),
                  ),
                )),
            ],
          ),
        ),
    ]);
  }
}
