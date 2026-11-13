// The home's Orders tab: every order, from placed to delivered, in one
// place, as a phone shows it. On top, how many wait for the business; then
// a section for each kind, a few compact rows in each (who, what, the
// amount and its status) and "See all ›" to its full screen: orders from
// customers (/storefront/orders), from linked businesses (/trade-orders),
// and to suppliers (the Stock screen and an order's own screen). Those
// waiting come first, a dot on them. Read again while the tab is open.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty;
import '../kit/mobile.dart';
import '../kit/polling.dart';
import 'bookings_page.dart' show orderStatus;
import 'purchases_page.dart' show openPurchase, purchaseStatus, sellerLine;
import 'stock_page.dart' show StockPage;
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
    final none = !_seesCustomers && !_seesTrade && !_seesPurchases;
    final customers = _customers == null || _noShop ? const <Map<String, dynamic>>[] : firstWaiting(_customers!, customerWaiting);
    final trade = _trade == null ? const <Map<String, dynamic>>[] : firstWaiting(_trade!, tradeWaiting);
    final purchases = _purchases == null ? const <Map<String, dynamic>>[] : firstWaiting(_purchases!, purchaseWaiting);
    final waiting = customers.where(customerWaiting).length + trade.where(tradeWaiting).length + purchases.where(purchaseWaiting).length;
    final open = customers.length + trade.length + purchases.length;
    final loaded = (!_seesCustomers || _customers != null) && (!_seesTrade || _trade != null) && (!_seesPurchases || _purchases != null);
    return TkScreen(
      title: 'Orders',
      back: false,
      onRefresh: _load,
      children: [
        if (none) const TkEmpty(icon: 'orders', title: 'No orders for you here', line: 'Someone who looks after orders or stock sees them.'),
        if (!none && loaded)
          TkSummary(
            label: 'Waiting for you',
            big: '$waiting',
            tag: waiting == 0 ? 'All done' : null,
            foot: open == 1 ? '1 order open' : '$open orders open',
            semanticsLabel: '$waiting waiting for you, $open open',
            figures: [
              if (_seesCustomers && !_noShop) (label: 'Customers', value: '${customers.length}', dot: null),
              if (_seesTrade) (label: 'Businesses', value: '${trade.length}', dot: null),
              if (_seesPurchases && !_noSuppliers) (label: 'Suppliers', value: '${purchases.length}', dot: null),
            ],
          ),
        if (_seesCustomers) _customersSection(customers),
        if (_seesTrade) _tradeSection(trade),
        if (_seesPurchases && !_noSuppliers) _purchasesSection(purchases),
      ],
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
    onSeeAll: () => _go(StockPage(auth: _auth)),
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
            waiting: purchaseWaiting(p),
            // A linked supplier's order has its own screen; the rest are
            // sent, received and paid on the Stock screen.
            onTap: linked ? () => _then(openPurchase(context, _auth, _str(p['id']))) : () => _go(StockPage(auth: _auth)),
          );
        }(),
    ],
  );
}
