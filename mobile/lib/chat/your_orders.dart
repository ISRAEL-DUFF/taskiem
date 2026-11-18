// Orders on You (docs/spec/taskiem-for-everyone.md §13; "As built: stage 3,
// Orders and addresses"): every order and booking the person has with any
// business, newest first (`GET /v1/me/orders`, paged by `next` → `before`).
// Each shows the business, what, the total, its state, **Pay** when
// something is due (its link from `…/pay`, opened in the app's browser
// tab) and **Chat about this** (the chat they have, or one started with
// `from: "order"`). Orders made with the phone before it was on the
// account are only counted until **These are mine**; one made before the
// number was verified on this account (`earlier`) stays a short row with
// only Chat about this. A row opens the order: its lines, how it comes,
// the address on it, the rider's progress, a booking's times.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
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
import 'cards.dart' show CardPill;
import '../kit/load_more.dart';
import 'door.dart';
import 'my_chat.dart';
import 'people_cards.dart' show shareWithPeople;
import 'words.dart';

/// The words asking about orders made with the phone before it was on the
/// account (§13): nothing else of them shows until the person says so.
String waitingWords(int n) => n == 1
    ? 'We found an order made with your number before it was on your account. Is it yours?'
    : 'We found $n orders made with your number before it was on your account. Are they yours?';

/// An order made before this number was verified on the account: its row
/// says only this, with no summary and no Pay.
const earlierWords = 'Made before this number was yours';

/// The words for a kind of booking ("Hire", "Tickets"), or "Booking".
String bookingWord(String? type) => switch (type) {
  'hire' => 'Hire',
  'space' => 'Space',
  'class' => 'Class',
  'tickets' => 'Tickets',
  _ => 'Booking',
};

/// "Booking" or "Order", by the order's kind.
String orderWord(String kind, String? bookingType) => kind == 'booking' ? bookingWord(bookingType) : 'Order';

/// A row's line: "2 × Jollof rice · Today, 18:03"; an earlier order's says
/// it was made before the number was the person's.
String yourOrderLine(PersonOrder o, String when) {
  if (o.earlier == true) return '$earlierWords · $when';
  final what = o.summary.isNotEmpty ? o.summary : (o.kind == 'booking' ? 'A booking' : 'An order');
  return '${o.kind == 'booking' ? '${bookingWord(o.bookingType)} · ' : ''}$what · $when';
}

/// The pay link refused, in a person's words (the web's payProblem), else
/// the server's ("This order is closed.", an earlier order, too many tries).
String payProblem(Object e) {
  if (e is ApiError) {
    if (e.status == 403) return 'This business is not taking messages or payments from you.';
    if (e.status == 409 && e.has('area_off')) return 'This business is not taking orders right now.';
  }
  return sayFailure(e);
}

/// The orders being paid now: pressed again before the pay page opens,
/// nothing more is asked or opened.
final _paying = <String>{};

/// Opens an order's pay link (`GET /v1/me/orders/{business}/{reference}/pay`)
/// in the app's browser tab; true when it was opened.
Future<bool> payMyOrder(BuildContext context, Auth auth, {required String business, required String reference}) async {
  final key = '$business/$reference';
  if (!_paying.add(key)) return false;
  try {
    final r = await auth.api.payMyOrder(business: business, reference: reference);
    if (!context.mounted) return false;
    await openOrSay(context, () => openInBrowser(Uri.parse(r.url)), 'The pay page didn’t open. Try again.');
    return true;
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, payProblem(e), error: true);
    return false;
  } finally {
    _paying.remove(key);
  }
}

/// The words a chat about an order starts with, as the web's
/// (personchat.ts aboutOrderDraft).
String aboutOrderDraft(String reference) => 'Hello, about my order $reference: ';

/// "Chat about this": the chat with the order's business, the order named
/// in the box.
Future<void> chatAboutOrder(BuildContext context, Auth auth, OpenMyChat open, {required String business, Object? chat, required String reference}) =>
    chatWithBusiness(context, auth, open, business: business, chat: chat is String ? chat : null, from: 'order', draft: aboutOrderDraft(reference));

/// An order's row again, from what its page read.
PersonOrder rowOf(PersonOrderDetail d) => PersonOrder(
  bookingType: d.bookingType,
  business: d.business,
  businessId: d.businessId,
  chatId: d.chatId,
  dueKobo: d.dueKobo,
  earlier: d.earlier,
  kind: d.kind,
  madeAt: d.madeAt,
  pay: d.pay,
  photo: d.photo,
  reference: d.reference,
  state: d.state,
  summary: d.summary,
  totalKobo: d.totalKobo,
);

String _keyOf(PersonOrder o) => '${o.businessId}/${o.reference}';

/// The Orders tab on You.
class YourOrdersTab extends StatefulWidget {
  const YourOrdersTab({super.key, required this.auth, required this.onOpenChat, this.now = DateTime.now, this.shown, this.back = false});
  final Auth auth;
  final OpenMyChat onOpenChat;
  final DateTime Function() now;

  /// Told each time the tab is shown again: the list is read again, its
  /// pages and place kept.
  final Listenable? shown;

  /// Pushed over a business's home (a link to /you/orders), not a tab.
  final bool back;

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
    widget.shown?.addListener(_shownAgain);
    _load();
  }

  @override
  void dispose() {
    widget.shown?.removeListener(_shownAgain);
    super.dispose();
  }

  void _shownAgain() => unawaited(_refresh());

  /// The first page, from the start (and after "These are mine").
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
      if (!mounted) return;
      // Orders already shown stay; the failure is said.
      if (_orders == null) {
        setState(() => _error = sayFailure(e));
      } else {
        showTkToast(context, sayFailure(e), error: true);
      }
    }
  }

  /// Read again, keeping the pages read and the place in them: the first
  /// page's rows take their places, new ones come on top, older pages stay.
  Future<void> _refresh() async {
    final had = _orders;
    if (had == null) return _load();
    try {
      final r = await widget.auth.api.myOrders();
      if (!mounted) return;
      final first = r.orders;
      final fresh = {for (final o in first) _keyOf(o)};
      final last = first.isEmpty ? null : first.last.madeAt;
      // Rows of the older pages: after the first page's last.
      final older = [for (final o in had) if (!fresh.contains(_keyOf(o)) && last != null && !o.madeAt.isAfter(last)) o];
      setState(() {
        _orders = [...first, ...older];
        // More than one page read: the next stays the one after those.
        if (older.isEmpty) _next = r.next is String ? r.next as String : null;
        _waiting = r.waitingCount;
        _error = null;
      });
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
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
        final seen = {for (final o in _orders ?? const <PersonOrder>[]) _keyOf(o)};
        _orders = [...?_orders, ...r.orders.where((o) => !seen.contains(_keyOf(o)))];
        _next = r.next is String ? r.next as String : null;
      });
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    } finally {
      if (mounted) setState(() => _more = false);
    }
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

  /// An order's page; back from it, only its row is read again, in its
  /// place (the pages read and the scrolling stay).
  Future<void> _open(PersonOrder o) async {
    await tkPush<void>(context, YourOrderPage(auth: widget.auth, order: o, onOpenChat: widget.onOpenChat, now: widget.now));
    if (!mounted || o.earlier == true) return;
    try {
      final d = await widget.auth.api.myOrder(business: o.businessId, reference: o.reference);
      if (!mounted) return;
      setState(() => _orders = [for (final x in _orders ?? const <PersonOrder>[]) _keyOf(x) == _keyOf(o) ? rowOf(d) : x]);
    } on Object {
      // The row stays as it was; the list is read again when shown.
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final orders = _orders;
    final now = widget.now();
    return TkScreen(
      title: 'Orders',
      back: widget.back,
      onRefresh: _refresh,
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
                  sub: yourOrderLine(o, listTime(o.madeAt, now)),
                  face: BusinessFace(name: o.business, urls: o.photo?.urls, size: 40),
                  amount: o.pay ? '${naira(o.dueKobo)} due' : naira(o.totalKobo),
                  status: yourOrderStatus(o.state),
                  statusTone: doorTone(o.state),
                  waiting: o.pay,
                  semanticsLabel:
                      '${orderWord(o.kind, o.bookingType)} ${o.reference} with ${o.business}: ${o.earlier == true ? earlierWords : o.summary}, ${yourOrderStatus(o.state)}, ${naira(o.totalKobo)}${o.pay ? ', ${naira(o.dueKobo)} to pay' : ''}',
                  onTap: () => _open(o),
                ),
            ],
          ),
        // Older pages as the list nears its end (the app's TkLoadMore).
        if (_next != null) TkLoadMore(label: 'Show older orders', onMore: _older),
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

/// One of the person's orders (`GET /v1/me/orders/{business}/{reference}`):
/// from its row, or from a link (its business and reference alone).
class YourOrderPage extends StatefulWidget {
  YourOrderPage({super.key, required this.auth, required PersonOrder this.order, required this.onOpenChat, this.now = DateTime.now})
    : business = order.businessId,
      reference = order.reference;

  /// From a link (/you/orders/{business}/{reference}): the rest is read.
  const YourOrderPage.of({super.key, required this.auth, required this.business, required this.reference, required this.onOpenChat, this.now = DateTime.now}) : order = null;

  final Auth auth;
  final PersonOrder? order;
  final String business;
  final String reference;
  final OpenMyChat onOpenChat;
  final DateTime Function() now;

  @override
  State<YourOrderPage> createState() => _YourOrderPageState();
}

class _YourOrderPageState extends State<YourOrderPage> with WidgetsBindingObserver {
  PersonOrderDetail? _d;
  String? _error;

  /// Pay's page was opened: read again when the person comes back to the app.
  bool _paying = false;

  PersonOrder? get _row => _d == null ? widget.order : rowOf(_d!);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    // An earlier order is its row alone: nothing more to read.
    if (widget.order?.earlier != true) _load();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && _paying && mounted) {
      _paying = false;
      unawaited(_load());
    }
  }

  Future<void> _load() async {
    try {
      final d = await widget.auth.api.myOrder(business: widget.business, reference: widget.reference);
      if (mounted) setState(() => (_d = d, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _pay() async {
    if (await payMyOrder(context, widget.auth, business: widget.business, reference: widget.reference)) _paying = true;
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final o = _row;
    final d = _d;
    final earlier = o?.earlier == true;
    final word = o == null ? 'Order' : orderWord(o.kind, o.bookingType);
    final booking = o?.kind == 'booking';
    String iso(DateTime x) => x.toUtc().toIso8601String();
    return TkScreen(
      title: '$word ${widget.reference}',
      backLabel: 'Back to Orders',
      onRefresh: earlier ? null : _load,
      children: [
        if (o != null)
          TkTopCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  children: [
                    Expanded(child: TkEyebrow('${word.toUpperCase()} · ${o.reference}', color: look.limeInk)),
                    CardPill(yourOrderStatus(o.state), doorTone(o.state)),
                  ],
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    BusinessFace(name: o.business, urls: o.photo?.urls, size: 36),
                    const SizedBox(width: 10),
                    Expanded(child: Text(o.business, style: tkDisplay(size: 18, letterSpacing: -0.3, color: t.text))),
                  ],
                ),
                const SizedBox(height: 12),
                if (booking && d?.startsAt != null) ...[
                  Text(whenLabel(iso(d!.startsAt!)), style: tkFigure(context, size: 26)),
                  Text(
                    [if ((d.serviceName ?? o.summary).isNotEmpty) d.serviceName ?? o.summary, if (d.endsAt != null) '${lagosTime(iso(d.startsAt!))} to ${lagosTime(iso(d.endsAt!))}'].join(' · '),
                    style: tkText(size: 13.5, color: t.muted),
                  ),
                  const SizedBox(height: 8),
                ] else if (booking && o.summary.isNotEmpty) ...[
                  Text(o.summary, style: tkText(size: 14, color: t.text)),
                  const SizedBox(height: 6),
                ],
                Text(naira(o.totalKobo), style: tkFigure(context, size: booking ? 20 : 30)),
                Text(
                  [
                    if (o.pay && o.dueKobo > 0) '${naira(o.dueKobo)} to pay',
                    if ((d?.paidKobo ?? 0) > 0) '${naira(d!.paidKobo)} paid',
                    'Made ${listTime(o.madeAt, widget.now())}',
                  ].join(' · '),
                  style: tkText(size: 13, color: t.muted),
                ),
                if (earlier) ...[
                  const SizedBox(height: 10),
                  Semantics(
                    liveRegion: true,
                    child: Text(
                      '$earlierWords. It was made with your number before the number was on your account, so only this much of it shows, and it is not paid here. Ask ${o.business} about it in the chat.',
                      style: tkText(size: 13.5, lineHeight: 1.4, color: t.text),
                    ),
                  ),
                ],
                const SizedBox(height: 14),
                // As a top card's (TkButtonRow): Pay first when there is
                // something to pay, then Chat; sharing a link under them.
                TkButtonRow([
                  if (o.pay && !earlier) TkSmallButton(label: 'Pay ${naira(o.dueKobo)}', primary: true, onPressed: _pay),
                  TkSmallButton(
                    label: 'Chat about this',
                    primary: earlier || !o.pay,
                    icon: 'b-chat',
                    onPressed: () => chatAboutOrder(context, widget.auth, widget.onOpenChat, business: widget.business, chat: o.chatId, reference: widget.reference),
                  ),
                ]),
                // Shared into a chat with a person (people-chatting §10.1):
                // they see it live only if it is theirs too.
                if (!earlier) ...[
                  const SizedBox(height: 6),
                  Center(
                    child: TkLink(
                      label: 'Share with someone',
                      bold: true,
                      onTap: () => shareWithPeople(context, widget.auth, SendPeopleMessageRequestCard(type: 'order', businessId: widget.business, reference: widget.reference), what: '$word ${widget.reference}'),
                    ),
                  ),
                ],
              ],
            ),
          ),
        if (_error != null) TkErrorBox(_error!),
        if (d == null && _error == null && !earlier) const TkSkeleton(rows: 3, label: 'Loading the order…'),
        if (!earlier && d?.delivery != null)
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
                    // A rider has it: how far they have got, shared into a
                    // chat with a person (people-chatting §10.1).
                    const SizedBox(height: 12),
                    TkSmallButton(
                      label: 'Share the delivery',
                      icon: 'b-share',
                      semantics: 'Share the delivery of $word ${widget.reference} with someone',
                      onPressed: () => shareWithPeople(context, widget.auth, SendPeopleMessageRequestCard(type: 'delivery', businessId: widget.business, reference: widget.reference), what: 'The delivery of $word ${widget.reference}'),
                    ),
                  ],
                ),
              ),
            ],
          ),
        if (!earlier && d != null && d.lines.isNotEmpty)
          TkBill(
            lines: [
              for (final l in d.lines) (name: '${l.quantity} × ${l.name}', sub: l.quantity > 1 ? '${naira(l.priceKobo)} each' : '', under: null, amount: naira(l.lineKobo)),
              if ((d.deliveryKobo ?? 0) > 0) (name: 'Delivery', sub: d.zone ?? '', under: null, amount: naira(d.deliveryKobo!)),
            ],
            total: naira(d.totalKobo),
          ),
        if (!earlier && d != null && (d.fulfilment != null || d.address != null))
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
