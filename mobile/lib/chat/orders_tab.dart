// The home's Orders tab: every order, from placed to delivered, in one
// place, as a phone shows it. Chips choose the kind shown; on top, the open
// orders in their stages (new, getting ready, on the way) and the one that
// has waited longest; then a section for each kind, a few compact rows in
// each (who, what, the amount and its status) and "See all ›" to its full
// screen: orders from customers (/storefront/orders), from linked
// businesses (/trade-orders), and to suppliers (Supplies and an
// order's own screen). Those waiting come first, a dot on them. Read again
// while the tab is open.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/polling.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'book_sheet.dart' show lagosTime;
import 'bookings_page.dart' show orderStatus;
import 'money_page.dart' show hourWords;
import 'purchases_page.dart' show SuppliesPage, openPurchase, purchaseStatus, sellerLine;
import 'storefront_orders_page.dart' show StorefrontOrderPage, StorefrontOrdersPage, orderTotal, orderWhat, relativeAge;
import 'trade_orders_page.dart' show TradeOrderPage, TradeOrdersPage, linesWords, tradeStatus;
import 'words.dart' show CardTone, Tone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
Map<String, dynamic> _map(Object? v) => (v as Map?)?.cast<String, dynamic>() ?? const {};
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

/// How many of each kind the tab shows before "See all".
const ordersShown = 5;

/// A customer's order waits for the business (StorefrontOrders.tsx WAITING,
/// and a refund due).
bool customerWaiting(Map<String, dynamic> o) => const {'received', 'awaiting_payment', 'price_changed', 'refund_due'}.contains(o['status']);

/// A linked business's order waits for us: new, a change asked of us, a
/// cancel asked, their count to agree.
bool tradeWaiting(Map<String, dynamic> o) =>
    o['status'] == 'received' || _str(o['cancel_asked_at']).isNotEmpty || (_str(o['short_at']).isNotEmpty && _str(o['short_agreed_at']).isEmpty);

/// An order to a supplier waits for us: a draft to send, their draft or
/// change to answer, a delivery to say what came of.
bool purchaseWaiting(Map<String, dynamic> p) =>
    p['status'] == 'drafted' ||
    p['seller_status'] == 'proposed' ||
    p['seller_status'] == 'changed' ||
    (p['seller_status'] == 'delivered' && _str(p['short_at']).isEmpty && p['status'] != 'received' && p['status'] != 'cancelled');

/// Done ones (delivered, received, cancelled, declined) are not shown in
/// the tab: their screens have them.
bool _open(String status) => !const {'delivered', 'received', 'cancelled', 'declined', 'completed', 'refunded', 'collected'}.contains(status);

/// Those waiting first, then the newest; the done ones left out.
List<Map<String, dynamic>> firstWaiting(List<Map<String, dynamic>> list, bool Function(Map<String, dynamic>) waiting) {
  final open = list.where((o) => _open(_str(o['status'])) || waiting(o)).toList();
  return [...open.where(waiting), ...open.where((o) => !waiting(o))];
}

CardTone _tone(Tone t) => switch (t) {
  Tone.ok => CardTone.lime,
  Tone.warn => CardTone.warn,
  Tone.danger => CardTone.danger,
  _ => CardTone.plain,
};

/// Where an order to us stands, as the Orders tab's flow shows it: new
/// (waiting for the business), getting ready, or on the way; null once
/// done or for one that waits on the customer alone.
String? sellStage(Map<String, dynamic> o, {required bool trade}) {
  if (trade ? tradeWaiting(o) : customerWaiting(o)) return 'new';
  final s = _str(o['status']);
  if (trade ? const {'accepted', 'ready'}.contains(s) : const {'paid', 'accepted'}.contains(s)) return 'ready';
  if (const {'dispatched', 'on_the_way', 'arrived'}.contains(s)) return 'way';
  return null;
}

/// Where an order to a supplier stands: ours to answer, sent to them, or
/// on its way to us.
String? buyStage(Map<String, dynamic> p) {
  if (purchaseWaiting(p)) return 'answer';
  final seller = _str(p['seller_status']);
  if (p['status'] == 'part_received' || seller == 'dispatched') return 'coming';
  if (const {'sent', 'confirmed'}.contains(p['status'])) return 'sent';
  return null;
}

/// The hour of today (Lagos) the most orders came in, and how many: null
/// with none today.
({int hour, int count})? busiestHour(List<Map<String, dynamic>> orders, DateTime now) {
  final per = <int, int>{};
  final today = now.toUtc().add(const Duration(hours: 1));
  for (final o in orders) {
    final d = DateTime.tryParse(_str(o['created_at']))?.toUtc().add(const Duration(hours: 1));
    if (d == null || d.year != today.year || d.month != today.month || d.day != today.day) continue;
    per[d.hour] = (per[d.hour] ?? 0) + 1;
  }
  if (per.isEmpty) return null;
  final best = per.entries.reduce((a, b) => b.value > a.value || (b.value == a.value && b.key < a.key) ? b : a);
  return (hour: best.key, count: best.value);
}

/// How long an order has waited: "2 hours", "3 days".
String waitedWords(String iso, DateTime now) {
  final t = DateTime.tryParse(iso);
  if (t == null) return '';
  final m = now.difference(t).inMinutes;
  if (m < 1) return 'a moment';
  if (m < 60) return '$m min';
  final h = m ~/ 60;
  if (h < 24) return h == 1 ? '1 hour' : '$h hours';
  final d = h ~/ 24;
  return d == 1 ? '1 day' : '$d days';
}

class OrdersTab extends StatefulWidget {
  const OrdersTab({super.key, required this.auth, this.now});
  final Auth auth;
  final DateTime Function()? now;

  @override
  State<OrdersTab> createState() => _OrdersTabState();
}

class _OrdersTabState extends State<OrdersTab> with Polling {
  List<Map<String, dynamic>>? _customers;
  bool _noShop = false;
  String? _customersError;
  List<Map<String, dynamic>>? _trade;
  String? _tradeError;
  List<Map<String, dynamic>>? _purchases;
  bool _noSuppliers = false;
  String? _purchasesError;

  /// The kind shown: all, customers, businesses or suppliers.
  String _kind = 'all';

  Auth get _auth => widget.auth;
  DateTime get _now => (widget.now ?? DateTime.now)();

  bool get _seesCustomers => _auth.can('storefront.manage|order.handle');
  bool get _seesTrade => _auth.can('storefront.manage|order.handle');
  bool get _seesPurchases => _auth.can('table.read');

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _load() async {
    final t = _auth.transport;
    await Future.wait([
      if (_seesCustomers)
        () async {
          try {
            final r = (await t.send('GET', '/v1/storefront/orders') as Map).cast<String, dynamic>();
            if (mounted) setState(() => (_customers = _maps(r['orders']), _customersError = null, _noShop = false));
          } on ApiError catch (e) {
            // No storefront yet: the card says so.
            if (mounted) setState(() => e.status == 404 ? (_customers = const [], _noShop = true, _customersError = null) : _customersError = e.message);
          } on Object catch (e) {
            if (mounted) setState(() => _customersError = errorWords(e));
          }
        }(),
      if (_seesTrade)
        () async {
          try {
            final r = (await t.send('GET', '/v1/trade-orders') as Map).cast<String, dynamic>();
            if (mounted) setState(() => (_trade = _maps(r['orders']), _tradeError = null));
          } on Object catch (e) {
            if (mounted) setState(() => _tradeError = errorWords(e));
          }
        }(),
      if (_seesPurchases)
        () async {
          try {
            final r = (await t.send('GET', '/v1/stock/purchases') as Map).cast<String, dynamic>();
            if (mounted) setState(() => (_purchases = _maps(r['purchases']), _purchasesError = null, _noSuppliers = false));
          } on ApiError catch (e) {
            // No suppliers on this Taskiem (no text messages): no card.
            if (mounted) setState(() => e.status == 503 ? (_purchases = const [], _noSuppliers = true, _purchasesError = null) : _purchasesError = e.message);
          } on Object catch (e) {
            if (mounted) setState(() => _purchasesError = errorWords(e));
          }
        }(),
    ]);
  }

  Future<void> _go(Widget page) async {
    await tkPush<void>(context, page);
    await _load();
  }

  Future<void> _then(Future<void> f) async {
    await f;
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final now = _now;
    final none = !_seesCustomers && !_seesTrade && !_seesPurchases;
    final customers = _customers == null || _noShop ? const <Map<String, dynamic>>[] : firstWaiting(_customers!, customerWaiting);
    final trade = _trade == null ? const <Map<String, dynamic>>[] : firstWaiting(_trade!, tradeWaiting);
    final purchases = _purchases == null ? const <Map<String, dynamic>>[] : firstWaiting(_purchases!, purchaseWaiting);
    final kinds = [
      if (_seesCustomers && !_noShop) ('customers', 'Customers', customers.length),
      if (_seesTrade) ('businesses', 'Businesses', trade.length),
      if (_seesPurchases && !_noSuppliers) ('suppliers', 'Suppliers', purchases.length),
    ];
    final kind = kinds.any((k) => k.$1 == _kind) ? _kind : 'all';
    bool shows(String k) => kind == 'all' || kind == k;
    final sell = [
      if (shows('customers')) for (final o in customers) (o: o, trade: false),
      if (shows('businesses')) for (final o in trade) (o: o, trade: true),
    ];
    final buy = shows('suppliers') ? purchases : const <Map<String, dynamic>>[];
    final waiting = sell.where((x) => sellStage(x.o, trade: x.trade) == 'new').length + buy.where(purchaseWaiting).length;
    final open = sell.length + buy.length;
    final loaded = (!_seesCustomers || _customers != null) && (!_seesTrade || _trade != null) && (!_seesPurchases || _purchases != null);
    // The flow is of orders to us; of orders to suppliers when only those
    // are shown.
    final buying = sell.isEmpty && buy.isNotEmpty || kind == 'suppliers';
    final stages = buying
        ? <TkStage>[
            (label: 'To answer', count: buy.where((p) => buyStage(p) == 'answer').length, ink: look.orangeInk, dot: look.orange),
            (label: 'Sent', count: buy.where((p) => buyStage(p) == 'sent').length, ink: look.tealInk, dot: look.teal),
            (label: 'Coming', count: buy.where((p) => buyStage(p) == 'coming').length, ink: look.blueInk, dot: look.blue),
          ]
        : <TkStage>[
            (label: 'New', count: sell.where((x) => sellStage(x.o, trade: x.trade) == 'new').length, ink: look.orangeInk, dot: look.orange),
            (label: 'Getting ready', count: sell.where((x) => sellStage(x.o, trade: x.trade) == 'ready').length, ink: look.tealInk, dot: look.teal),
            (label: 'On the way', count: sell.where((x) => sellStage(x.o, trade: x.trade) == 'way').length, ink: look.blueInk, dot: look.blue),
          ];
    // The order that has waited longest for the business, said under it.
    final waitingSell = sell.where((x) => sellStage(x.o, trade: x.trade) == 'new' && _str(x.o['created_at']).isNotEmpty).toList()
      ..sort((x, y) => _str(x.o['created_at']).compareTo(_str(y.o['created_at'])));
    final longest = buying || waitingSell.isEmpty ? null : waitingSell.first;
    return TkScreen(
      title: 'Orders',
      back: false,
      onRefresh: _load,
      under: kinds.length > 1
          ? TkChips<String>(
              label: 'Show',
              chips: [
                ('all', 'All · ${customers.length + trade.length + purchases.length}'),
                for (final (k, words, n) in kinds) (k, '$words · $n'),
              ],
              on: kind,
              onPick: (k) => setState(() => _kind = k),
            )
          : null,
      children: [
        if (none) const TkEmpty(icon: 'orders', title: 'No orders for you here', line: 'Someone who looks after orders or stock sees them.'),
        if (!none && loaded)
          TkTopCard(
            semanticsLabel: '$waiting waiting for you, $open open',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow(buying ? 'Orders to suppliers' : 'Orders to you', color: t.muted, icon: 'b-box', trailing: open == 1 ? '1 order open' : '$open orders open'),
                const SizedBox(height: 14),
                TkFlow(stages: stages),
                if (waiting == 0 && open > 0) ...[
                  const SizedBox(height: 12),
                  Center(child: TkTrend('Nothing waiting for you', up: true)),
                ],
                if (longest != null) ...[
                  const SizedBox(height: 14),
                  _longest(context, longest.o, longest.trade, now),
                ],
              ],
            ),
          ),
        if (!none && loaded && !buying) ?_tiles(context, now, kind),
        if (_seesCustomers && shows('customers')) _customersSection(customers),
        if (_seesTrade && shows('businesses')) _tradeSection(trade),
        if (_seesPurchases && !_noSuppliers && shows('suppliers')) _purchasesSection(purchases),
      ],
    );
  }

  /// The day so far: the next booking to come, and the busiest hour.
  Widget? _tiles(BuildContext context, DateTime now, String kind) {
    final look = TkLook.of(context);
    final all = [if (kind == 'all' || kind == 'customers') ...?_customers, if (kind == 'all' || kind == 'businesses') ...?_trade];
    final busy = busiestHour(all, now);
    final next = [
      for (final o in _customers ?? const <Map<String, dynamic>>[])
        if (o['kind'] == 'booking' && _open(_str(o['status'])) && (DateTime.tryParse(_str(o['starts_at']))?.isAfter(now) ?? false)) o,
    ]..sort((a, b) => _str(a['starts_at']).compareTo(_str(b['starts_at'])));
    final tiles = [
      if (next.isNotEmpty && (kind == 'all' || kind == 'customers'))
        () {
          final o = next.first;
          final c = _map(o['customer']);
          final who = [_str(o['service_name']), _str(c['name'])].where((x) => x.isNotEmpty).join(' · ');
          return TkHighlight(
            label: 'Next booking',
            icon: 'b-truck',
            color: look.tealInk,
            value: lagosTime(_str(o['starts_at'])),
            line: who,
            semanticsLabel: 'Next booking at ${lagosTime(_str(o['starts_at']))}${who.isEmpty ? '' : ', $who'}',
            onTap: () => _go(StorefrontOrderPage(auth: _auth, reference: _str(o['reference']), now: widget.now, backLabel: 'Back to Orders')),
          );
        }(),
      if (busy != null)
        TkHighlight(
          label: 'Busiest',
          icon: 'b-bars',
          color: look.limeInk,
          value: hourWords(busy.hour),
          line: busy.count == 1 ? '1 order that hour' : '${busy.count} orders that hour',
          semanticsLabel: 'Busiest today: ${hourWords(busy.hour)}, ${busy.count == 1 ? '1 order' : '${busy.count} orders'}',
        ),
    ];
    return tiles.isEmpty ? null : TkHighlights(tiles: tiles);
  }

  /// An order's stage, in its accent: new orange, getting ready teal, on
  /// the way blue.
  ({Color fill, Color ink})? _accent(BuildContext context, String? stage) {
    final look = TkLook.of(context);
    return switch (stage) {
      'new' || 'answer' => (fill: look.orange, ink: look.orangeInk),
      'ready' || 'sent' => (fill: look.teal, ink: look.tealInk),
      'way' || 'coming' => (fill: look.blue, ink: look.blueInk),
      _ => null,
    };
  }

  /// The order that has waited longest: who, for how long, and Open.
  Widget _longest(BuildContext context, Map<String, dynamic> o, bool trade, DateTime now) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final c = _map(o['customer']);
    final who = trade ? _str(o['buyer_name']) : [_str(c['name']), _str(c['phone'])].firstWhere((x) => x.isNotEmpty, orElse: () => 'A customer');
    final open = trade
        ? () => _go(TradeOrderPage(auth: _auth, id: _str(o['id'])))
        : () => _go(StorefrontOrderPage(auth: _auth, reference: _str(o['reference']), now: widget.now, backLabel: 'Back to Orders'));
    return Container(
      padding: const EdgeInsets.fromLTRB(10, 8, 8, 8),
      decoration: BoxDecoration(color: look.orange.withValues(alpha: 0.12), borderRadius: BorderRadius.circular(16)),
      child: Row(
        children: [
          TkFace(tkInitials(who), size: 34),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('$who has waited ${waitedWords(_str(o['created_at']), now)}', maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 14, weight: 600, lineHeight: 1.3, color: t.text)),
                Text(
                  [_str(o['reference']), if (!trade) orderStatus(_str(o['status'])).label].where((x) => x.isNotEmpty).join(' · '),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: tkText(size: 12.5, lineHeight: 1.3, color: t.muted),
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          TkSmallButton(label: 'Open', semantics: 'Open the order from $who', primary: true, onPressed: open),
        ],
      ),
    );
  }

  /// A section's rows, or its loading, or what went wrong.
  Widget _section({
    required String title,
    required List<Map<String, dynamic>>? list,
    required String? error,
    required String loading,
    required int waiting,
    required String seeAll,
    required VoidCallback onSeeAll,
    required List<Widget> rows,
    String empty = 'Nothing open',
  }) {
    if (error != null) {
      return TkSection(title: title, children: [Padding(padding: const EdgeInsets.all(12), child: TkErrorBox(error))]);
    }
    if (list == null) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkSection(title: title),
          TkSkeleton(rows: 2, label: loading),
        ],
      );
    }
    return TkSection(title: title, count: waiting, onSeeAll: onSeeAll, seeAllLabel: seeAll, empty: empty, children: rows);
  }

  Widget _customersSection(List<Map<String, dynamic>> shown) {
    final now = _now;
    return _section(
      title: 'From customers',
      list: _customers,
      error: _customersError,
      loading: 'Loading orders from your customers…',
      waiting: shown.where(customerWaiting).length,
      seeAll: 'See all orders from your customers',
      onSeeAll: () => _go(StorefrontOrdersPage(auth: _auth, now: widget.now)),
      empty: _noShop ? 'No storefront yet. Your customers’ orders arrive here once it is published.' : 'Nothing open',
      rows: [
        for (final o in shown.take(ordersShown))
          () {
            final c = _map(o['customer']);
            final st = orderStatus(_str(o['status']));
            final what = orderWhat(o);
            final who = [_str(c['name']), _str(c['phone'])].firstWhere((x) => x.isNotEmpty, orElse: () => 'A customer');
            return TkListRow(
              title: who,
              icon: o['kind'] == 'booking' ? 'clock' : null,
              sub: [_str(o['reference']), if (what.isNotEmpty) what, relativeAge(_str(o['created_at']), now)].where((x) => x.isNotEmpty).join(' · '),
              amount: orderTotal(o),
              status: st.label,
              statusTone: _tone(st.tone),
              statusAccent: _accent(context, sellStage(o, trade: false)),
              waiting: customerWaiting(o),
              onTap: () => _go(StorefrontOrderPage(auth: _auth, reference: _str(o['reference']), now: widget.now, backLabel: 'Back to Orders')),
            );
          }(),
      ],
    );
  }

  Widget _tradeSection(List<Map<String, dynamic>> shown) => _section(
    title: 'From businesses',
    list: _trade,
    error: _tradeError,
    loading: 'Loading orders from linked businesses…',
    waiting: shown.where(tradeWaiting).length,
    seeAll: 'See all orders from linked businesses',
    onSeeAll: () => _go(TradeOrdersPage(auth: _auth)),
    rows: [
      for (final o in shown.take(ordersShown))
        TkListRow(
          title: _str(o['buyer_name']),
          sub: [_str(o['reference']), linesWords(_maps(o['lines']))].where((x) => x.isNotEmpty).join(' · '),
          amount: naira(_int(o['total_kobo'])),
          status: tradeStatus(_str(o['status'])).label,
          statusTone: tradeStatus(_str(o['status'])).tone,
          statusAccent: _accent(context, sellStage(o, trade: true)),
          waiting: tradeWaiting(o),
          onTap: () => _go(TradeOrderPage(auth: _auth, id: _str(o['id']))),
        ),
    ],
  );

  Widget _purchasesSection(List<Map<String, dynamic>> shown) => _section(
    title: 'To suppliers',
    list: _purchases,
    error: _purchasesError,
    loading: 'Loading your orders to suppliers…',
    waiting: shown.where(purchaseWaiting).length,
    seeAll: 'See all your orders to suppliers',
    onSeeAll: () => _go(SuppliesPage(auth: _auth, backLabel: 'Back to Orders')),
    rows: [
      for (final p in shown.take(ordersShown))
        () {
          final st = purchaseStatus(_str(p['status']));
          final seller = sellerLine(p);
          final linked = _str(p['link_id']).isNotEmpty;
          return TkListRow(
            title: _str(p['supplier']),
            sub: [_str(p['reference']), if (seller.isNotEmpty) seller else linesWords(_maps(p['lines']))].where((x) => x.isNotEmpty).join(' · '),
            amount: naira(_int(p['total_kobo'])),
            status: st.label,
            statusTone: st.tone,
            statusAccent: _accent(context, buyStage(p)),
            waiting: purchaseWaiting(p),
            // A linked supplier's order has its own screen; the rest are
            // sent, received and paid on Supplies.
            onTap: linked ? () => _then(openPurchase(context, _auth, _str(p['id']))) : () => _go(SuppliesPage(auth: _auth, backLabel: 'Back to Orders')),
          );
        }(),
    ],
  );
}
