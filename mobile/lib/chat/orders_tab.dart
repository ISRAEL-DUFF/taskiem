// The home's Orders tab: every order, from placed to delivered, in one
// place. Three cards, each as its own screen has it and leading there:
// orders from customers (the web's Orders, /storefront/orders), orders
// from linked businesses (/trade-orders), and orders to suppliers ("Your
// orders"; the Stock screen and an order's own screen). Those waiting for
// the business come first; each card shows a few and its count, and its
// "See all" opens the full screen. Read again while the tab is open.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow;
import '../kit/page.dart';
import '../kit/polling.dart';
import '../theme/theme.dart';
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
    await Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => page));
    await _load();
  }

  Future<void> _then(Future<void> f) async {
    await f;
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final none = !_seesCustomers && !_seesTrade && !_seesPurchases;
    return TkPageScreen(
      title: 'Orders',
      back: false,
      description: none
          ? null
          : 'Every order, from placed to delivered: from your customers, from businesses you supply, and to your suppliers. Those waiting for you come first.',
      onRefresh: _load,
      children: [
        if (none) const TkEmpty(icon: 'orders', title: 'No orders for you here', line: 'Someone who looks after orders or stock sees them.'),
        if (_seesCustomers) _customersCard(t),
        if (_seesTrade) _tradeCard(t),
        if (_seesPurchases && !_noSuppliers) _purchasesCard(t),
      ],
    );
  }

  /// A card's head line: how many wait, of how many open.
  Widget _count(TkTokens t, int waiting, int open) => Padding(
    padding: const EdgeInsets.only(bottom: 4),
    child: Text(open == 0 ? 'Nothing open.' : '${waiting > 0 ? '$waiting waiting for you · ' : ''}$open open', style: tkHint(t)),
  );

  Widget _seeAll(String label, String semantics, VoidCallback go) => Padding(
    padding: const EdgeInsets.only(top: 8),
    child: Align(
      alignment: Alignment.centerLeft,
      child: TkSmallButton(label: label, semantics: semantics, onPressed: go),
    ),
  );

  Widget _customersCard(TkTokens t) {
    final list = _customers;
    final shown = list == null ? const <Map<String, dynamic>>[] : firstWaiting(list, customerWaiting);
    final now = _now;
    return TkPageCard(
      title: 'From your customers',
      children: [
        if (_customersError != null) TkErrorBox(_customersError!),
        if (list == null && _customersError == null) const TkLoading(rows: 2, label: 'Loading orders from your customers…'),
        if (_noShop) Text('No storefront yet: your customers’ orders arrive here once it is published.', style: tkHint(t)),
        if (list != null && !_noShop) ...[
          _count(t, shown.where(customerWaiting).length, shown.length),
          for (final o in shown.take(ordersShown))
            () {
              final c = _map(o['customer']);
              final st = orderStatus(_str(o['status']));
              final what = orderWhat(o);
              return TkRow(
                name: '${[_str(c['name']), _str(c['phone'])].firstWhere((x) => x.isNotEmpty, orElse: () => 'A customer')} · ${_str(o['reference'])}',
                icon: o['kind'] == 'booking' ? 'clock' : 'invoices',
                sub: [if (what.isNotEmpty) what, orderTotal(o), relativeAge(_str(o['created_at']), now)].where((x) => x.isNotEmpty).join(' · '),
                pill: TkPill(st.label, tone: _tone(st.tone)),
                tone: customerWaiting(o) ? (o['status'] == 'refund_due' ? CardTone.danger : CardTone.warn) : null,
                onTap: () => _go(StorefrontOrderPage(auth: _auth, reference: _str(o['reference']), now: widget.now, backLabel: 'Back to Orders')),
              );
            }(),
          _seeAll('See all', 'See all orders from your customers', () => _go(StorefrontOrdersPage(auth: _auth, now: widget.now))),
        ],
      ],
    );
  }

  Widget _tradeCard(TkTokens t) {
    final list = _trade;
    final shown = list == null ? const <Map<String, dynamic>>[] : firstWaiting(list, tradeWaiting);
    return TkPageCard(
      title: 'From businesses you supply',
      children: [
        if (_tradeError != null) TkErrorBox(_tradeError!),
        if (list == null && _tradeError == null) const TkLoading(rows: 2, label: 'Loading orders from linked businesses…'),
        if (list != null) ...[
          _count(t, shown.where(tradeWaiting).length, shown.length),
          for (final o in shown.take(ordersShown))
            TkRow(
              name: '${_str(o['buyer_name'])} · ${_str(o['reference'])}',
              icon: 'invoices',
              sub: '${linesWords(_maps(o['lines']))} · ${naira(_int(o['total_kobo']))}',
              pill: TkPill(tradeStatus(_str(o['status'])).label, tone: tradeStatus(_str(o['status'])).tone),
              tone: tradeWaiting(o) ? CardTone.warn : null,
              onTap: () => _go(TradeOrderPage(auth: _auth, id: _str(o['id']))),
            ),
          _seeAll('See all', 'See all orders from linked businesses', () => _go(TradeOrdersPage(auth: _auth))),
        ],
      ],
    );
  }

  Widget _purchasesCard(TkTokens t) {
    final list = _purchases;
    final shown = list == null ? const <Map<String, dynamic>>[] : firstWaiting(list, purchaseWaiting);
    return TkPageCard(
      title: 'To your suppliers',
      children: [
        if (_purchasesError != null) TkErrorBox(_purchasesError!),
        if (list == null && _purchasesError == null) const TkLoading(rows: 2, label: 'Loading your orders to suppliers…'),
        if (list != null) ...[
          _count(t, shown.where(purchaseWaiting).length, shown.length),
          for (final p in shown.take(ordersShown))
            () {
              final st = purchaseStatus(_str(p['status']));
              final seller = sellerLine(p);
              final linked = _str(p['link_id']).isNotEmpty;
              return TkRow(
                name: '${_str(p['supplier'])} · ${_str(p['reference'])}',
                icon: 'orders',
                sub: [linesWords(_maps(p['lines'])), naira(_int(p['total_kobo'])), if (seller.isNotEmpty) seller].join(' · '),
                pill: TkPill(st.label, tone: st.tone),
                tone: purchaseWaiting(p) ? CardTone.warn : null,
                // A linked supplier's order has its own screen; the rest are
                // sent, received and paid on the Stock screen.
                onTap: linked ? () => _then(openPurchase(context, _auth, _str(p['id']))) : () => _go(StockPage(auth: _auth)),
              );
            }(),
          _seeAll('See all', 'See all your orders to suppliers', () => _go(StockPage(auth: _auth))),
        ],
      ],
    );
  }
}
