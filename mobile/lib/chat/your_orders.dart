// Orders on You (docs/spec/taskiem-for-everyone.md §13; "As built: stage 3,
// Orders and addresses"): every order and booking the person has with any
// business, newest first (`GET /v1/me/orders`, paged by `next` → `before`).
// Each shows the business, what, the total, its state, **Pay** when
// something is due (its link from `…/pay`, opened in the app's browser
// tab) and **Chat about this** (the chat they have, or one started with
// `from: "order"`). Orders made with the phone before it was on the
// account are only counted until **These are mine**. A row opens the
// order: its lines, how it comes, the address on it, the rider's progress,
// a booking's times.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'book_sheet.dart' show lagosTime, whenLabel;
import 'cards.dart' show CardButton, CardPill;
import 'door.dart';
import 'my_chat.dart';
import 'words.dart';

/// The words asking about orders made with the phone before it was on the
/// account (§13): nothing else of them shows until the person says so.
String waitingWords(int n) => n == 1
    ? 'We found an order made with your number before it was on your account. Is it yours?'
    : 'We found $n orders made with your number before it was on your account. Are they yours?';

/// Opens an order's pay link (`GET /v1/me/orders/{business}/{reference}/pay`)
/// in the app's browser tab.
Future<void> payMyOrder(BuildContext context, Auth auth, {required String business, required String reference}) async {
  try {
    final r = await auth.api.payMyOrder(business: business, reference: reference);
    if (!context.mounted) return;
    await openOrSay(context, () => openInBrowser(Uri.parse(r.url)), 'The pay page didn’t open. Try again.');
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
  }
}

/// "Chat about this": the chat with the order's business, the order named
/// in the box.
Future<void> chatAboutOrder(BuildContext context, Auth auth, OpenMyChat open, {required String business, Object? chat, required String reference}) =>
    chatWithBusiness(context, auth, open, business: business, chat: chat is String ? chat : null, from: 'order', draft: 'About my order $reference: ');

/// The Orders tab on You.
class YourOrdersTab extends StatefulWidget {
  const YourOrdersTab({super.key, required this.auth, required this.onOpenChat, this.now = DateTime.now});
  final Auth auth;
  final OpenMyChat onOpenChat;
  final DateTime Function() now;

  @override
  State<YourOrdersTab> createState() => _YourOrdersTabState();
}

class _YourOrdersTabState extends State<YourOrdersTab> {
  List<PersonOrder>? _orders;
  String? _next;
  int _waiting = 0;
  String? _error;
  bool _more = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.myOrders();
      if (!mounted) return;
      setState(() {
        _orders = r.orders;
        _next = r.next is String ? r.next as String : null;
        _waiting = r.waitingCount;
        _error = null;
      });
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _older() async {
    final next = _next;
    if (next == null || _more) return;
    setState(() => _more = true);
    try {
      final r = await widget.auth.api.myOrders(before: next);
      if (!mounted) return;
      setState(() {
        final seen = {for (final o in _orders ?? const <PersonOrder>[]) '${o.businessId}/${o.reference}'};
        _orders = [...?_orders, ...r.orders.where((o) => !seen.contains('${o.businessId}/${o.reference}'))];
        _next = r.next is String ? r.next as String : null;
      });
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
    if (mounted) setState(() => _more = false);
  }

  /// "These are mine" (§13): the orders made with this phone before it was
  /// on the account.
  Future<void> _theseAreMine() async {
    try {
      await widget.auth.api.confirmStorefrontOrders();
      await _load();
      if (mounted) showTkToast(context, 'Done: these orders are on your account now.');
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
  }

  Future<void> _open(PersonOrder o) async {
    await tkPush<void>(context, YourOrderPage(auth: widget.auth, order: o, onOpenChat: widget.onOpenChat, now: widget.now));
    if (mounted) await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final orders = _orders;
    final now = widget.now();
    return TkScreen(
      title: 'Orders',
      back: false,
      onRefresh: _load,
      children: [
        if (_waiting > 0)
          TkTopCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('FOUND WITH YOUR NUMBER', color: TkLook.of(context).orangeInk, icon: 'b-phone'),
                const SizedBox(height: 8),
                Semantics(liveRegion: true, child: Text(waitingWords(_waiting), style: tkText(size: 15, lineHeight: 1.4, color: t.text))),
                const SizedBox(height: 4),
                Text('Nothing of them shows until you say so.', style: tkText(size: 12.5, color: t.muted)),
                const SizedBox(height: 12),
                Align(
                  alignment: Alignment.centerLeft,
                  child: TkSmallButton(label: _waiting == 1 ? 'It’s mine' : 'These are mine', primary: true, onPressed: _theseAreMine),
                ),
              ],
            ),
          ),
        if (_error != null && orders == null) TkErrorBox(_error!),
        if (orders == null && _error == null) const TkSkeleton(rows: 4, label: 'Loading your orders…'),
        if (orders != null && orders.isEmpty)
          const TkPanel(
            children: [TkEmpty(icon: 'orders', title: 'No orders yet', line: 'Your orders and bookings with businesses show here.')],
          ),
        if (orders != null && orders.isNotEmpty)
          TkSection(
            children: [
              for (final o in orders)
                TkListRow(
                  key: ValueKey('order ${o.businessId} ${o.reference}'),
                  title: o.business,
                  sub: '${o.kind == 'booking' ? 'Booking · ' : ''}${o.summary} · ${listTime(o.madeAt, now)}',
                  face: BusinessFace(name: o.business, urls: o.photo?.urls, size: 40),
                  amount: o.pay ? '${naira(o.dueKobo)} due' : naira(o.totalKobo),
                  status: yourOrderStatus(o.state),
                  statusTone: doorTone(o.state),
                  waiting: o.pay,
                  semanticsLabel: '${o.kind == 'booking' ? 'Booking' : 'Order'} ${o.reference} with ${o.business}: ${o.summary}, ${yourOrderStatus(o.state)}, ${naira(o.totalKobo)}${o.pay ? ', ${naira(o.dueKobo)} to pay' : ''}',
                  onTap: () => _open(o),
                ),
            ],
          ),
        if (_next != null) TkButton(label: _more ? 'Loading…' : 'Show older orders', minHeight: 44, enabled: !_more, onPressed: _older),
      ],
    );
  }
}

/// A delivery's steps, as the rider goes.
List<({String label, String when, bool done})> deliverySteps(DeliveryProgress d) {
  String at(DateTime? x) => x == null ? '' : lagosTime(x.toUtc().toIso8601String());
  return [
    (label: 'Rider coming', when: at(d.acceptedAt), done: d.acceptedAt != null),
    (label: 'Picked up', when: at(d.pickedUpAt), done: d.pickedUpAt != null),
    (label: 'At your door', when: at(d.arrivedAt), done: d.arrivedAt != null),
    (label: 'Delivered', when: at(d.deliveredAt), done: d.deliveredAt != null),
  ];
}

/// One of the person's orders (`GET /v1/me/orders/{business}/{reference}`).
class YourOrderPage extends StatefulWidget {
  const YourOrderPage({super.key, required this.auth, required this.order, required this.onOpenChat, this.now = DateTime.now});
  final Auth auth;
  final PersonOrder order;
  final OpenMyChat onOpenChat;
  final DateTime Function() now;

  @override
  State<YourOrderPage> createState() => _YourOrderPageState();
}

class _YourOrderPageState extends State<YourOrderPage> {
  PersonOrderDetail? _d;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final d = await widget.auth.api.myOrder(business: widget.order.businessId, reference: widget.order.reference);
      if (mounted) setState(() => (_d = d, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final o = widget.order;
    final d = _d;
    final booking = (d?.kind ?? o.kind) == 'booking';
    final state = d?.state ?? o.state;
    final pay = d?.pay ?? o.pay;
    final due = d?.dueKobo ?? o.dueKobo;
    final total = d?.totalKobo ?? o.totalKobo;
    final paid = d?.paidKobo ?? 0;
    String iso(DateTime x) => x.toUtc().toIso8601String();
    return TkScreen(
      title: '${booking ? 'Booking' : 'Order'} ${o.reference}',
      backLabel: 'Back to Orders',
      onRefresh: _load,
      children: [
        TkTopCard(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Expanded(child: TkEyebrow('${booking ? 'BOOKING' : 'ORDER'} · ${o.reference}', color: look.limeInk)),
                  CardPill(yourOrderStatus(state), doorTone(state)),
                ],
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  BusinessFace(name: o.business, urls: (d?.photo ?? o.photo)?.urls, size: 36),
                  const SizedBox(width: 10),
                  Expanded(child: Text(o.business, style: tkDisplay(size: 18, letterSpacing: -0.3, color: t.text))),
                ],
              ),
              const SizedBox(height: 12),
              if (booking && d?.startsAt != null) ...[
                Text(whenLabel(iso(d!.startsAt!)), style: tkFigure(context, size: 26)),
                Text(
                  [d.serviceName ?? o.summary, if (d.endsAt != null) '${lagosTime(iso(d.startsAt!))} to ${lagosTime(iso(d.endsAt!))}'].join(' · '),
                  style: tkText(size: 13.5, color: t.muted),
                ),
                const SizedBox(height: 8),
              ],
              Text(naira(total), style: tkFigure(context, size: booking ? 20 : 30)),
              Text(
                [
                  if (pay && due > 0) '${naira(due)} to pay',
                  if (paid > 0) '${naira(paid)} paid',
                  'Made ${listTime(o.madeAt, widget.now())}',
                ].join(' · '),
                style: tkText(size: 13, color: t.muted),
              ),
              const SizedBox(height: 14),
              if (pay) ...[
                CardButton(label: 'Pay ${naira(due)}', primary: true, onTap: () => payMyOrder(context, widget.auth, business: o.businessId, reference: o.reference)),
                const SizedBox(height: 8),
              ],
              CardButton(
                label: 'Chat about this',
                onTap: () => chatAboutOrder(context, widget.auth, widget.onOpenChat, business: o.businessId, chat: d?.chatId ?? o.chatId, reference: o.reference),
              ),
            ],
          ),
        ),
        if (_error != null) TkErrorBox(_error!),
        if (d == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading the order…'),
        if (d?.delivery != null)
          TkSection(
            title: 'Delivery',
            children: [
              Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text(d!.delivery!.line, style: tkText(size: 15, weight: 600, color: t.text)),
                    const SizedBox(height: 12),
                    TkSteps(steps: deliverySteps(d.delivery!)),
                  ],
                ),
              ),
            ],
          ),
        if (d != null && d.lines.isNotEmpty)
          TkBill(
            lines: [
              for (final l in d.lines) (name: '${l.quantity} × ${l.name}', sub: l.quantity > 1 ? '${naira(l.priceKobo)} each' : '', under: null, amount: naira(l.lineKobo)),
              if ((d.deliveryKobo ?? 0) > 0) (name: 'Delivery', sub: d.zone ?? '', under: null, amount: naira(d.deliveryKobo!)),
            ],
            total: naira(total),
          ),
        if (d != null && (d.fulfilment != null || d.address != null))
          TkSection(
            title: booking ? 'Where' : 'How you get it',
            inset: 56,
            children: [
              if (d.fulfilment != null)
                TkListRow(
                  title: d.fulfilment == 'delivery' ? 'Delivered to you' : 'You pick it up',
                  sub: d.fulfilment == 'delivery' ? (d.zone ?? '') : '',
                  icon: d.fulfilment == 'delivery' ? 'b-bike' : 'b-store',
                  square: d.fulfilment == 'delivery' ? look.blue : look.teal,
                ),
              if (d.address != null)
                TkListRow(
                  title: d.address!.address,
                  sub: d.address!.landmark ?? '',
                  icon: 'b-pin',
                  square: look.orange,
                  semanticsLabel: 'Address: ${d.address!.address}${(d.address!.landmark ?? '').isEmpty ? '' : ', ${d.address!.landmark}'}',
                ),
            ],
          ),
      ],
    );
  }
}
