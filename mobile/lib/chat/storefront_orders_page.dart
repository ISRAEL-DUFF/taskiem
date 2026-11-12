// Orders from customers (web StorefrontOrders.tsx, lib/orders.ts): the
// storefront's orders, bookings, questions and call-backs, newest first;
// one order with its payment, a refund, its booking's Move and Cancel, its
// delivery by the business's riders (Dispatch.tsx OrderDelivery,
// ChooseRider), the money matched to it, and its customer. Deliveries
// (Dispatch.tsx StorefrontDeliveries): the riders' deliveries of a day, and
// how orders are offered to them.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show CloseSoon, TkSwitchRow, errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkPill, TkRow;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField, TkSavedTick, TkSheet, showTkSheet;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'book_sheet.dart' show lagosDay, lagosTime, whenLabel;
import 'bookings_page.dart' show askCancelBooking, canChange, cancelBooking, moveBooking, orderStatus, timeSpan;
import 'stock_page.dart' show feeInput, feeKobo;
import 'trade_orders_page.dart' show TradeOrdersPage, openTradeOrder;
import 'words.dart' show CardTone, Tone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';
Map<String, dynamic> _map(Object? v) => (v as Map?)?.cast<String, dynamic>() ?? const {};

CardTone _pillTone(Tone t) => switch (t) {
  Tone.ok => CardTone.lime,
  Tone.warn => CardTone.warn,
  Tone.danger => CardTone.danger,
  _ => CardTone.plain,
};

/// orders.ts ORDER_FILTERS: the list's filter.
const orderFilters = [
  ('open', 'Open'),
  ('all', 'All'),
  ('completed', 'Completed'),
  ('declined', 'Declined'),
  ('expired', 'Expired'),
  ('refund_due', 'Refund due'),
  ('price_changed', 'New prices to confirm'),
  ('cancelled', 'Cancelled'),
  ('no_show', 'No-show'),
  ('refunded', 'Refunded'),
];

/// orders.ts ordersPath, as the query: "all" asks with no status.
Map<String, String> ordersQuery(String filter, [String front = '']) => {
  if (filter != 'all') 'status': filter.isEmpty ? 'open' : filter,
  if (front.isNotEmpty) 'front': front,
};

/// orders.ts KIND_LABEL.
const kindLabel = {
  'order': 'Order',
  'enquiry': 'Question',
  'callback': 'Call-back',
  'booking': 'Booking',
  'trade': 'From a linked business',
  'hire': 'Hire',
  'space': 'Space booking',
  'tickets': 'Tickets',
  'class': 'Enrolment',
  'quote': 'Quote',
};

const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// orders.ts relativeAge: "just now", "5 min ago", "2 hours ago",
/// "yesterday", "3 days ago", then the date.
String relativeAge(String iso, DateTime now) {
  final t = DateTime.tryParse(iso);
  if (t == null) return '';
  final s = now.difference(t).inSeconds.clamp(0, 1 << 40);
  if (s < 60) return 'just now';
  final m = s ~/ 60;
  if (m < 60) return '$m min ago';
  final h = m ~/ 60;
  if (h < 24) return h == 1 ? '1 hour ago' : '$h hours ago';
  final d = h ~/ 24;
  if (d == 1) return 'yesterday';
  if (d < 7) return '$d days ago';
  final l = t.toUtc().add(const Duration(hours: 1));
  return '${l.day} ${_months[l.month - 1]} ${l.year}';
}

/// orders.ts timeLeft: "in 5 min", "in 3 hours", or "now" once past.
String timeLeft(String iso, DateTime now) {
  final t = DateTime.tryParse(iso);
  if (t == null) return '';
  final m = t.difference(now).inMinutes;
  if (m <= 0) return 'now';
  if (m < 60) return 'in $m min';
  final h = m ~/ 60;
  return h == 1 ? 'in 1 hour' : 'in $h hours';
}

/// orders.ts itemsSummary and orderWhat: the basket, or a booking's
/// service and time.
String orderWhat(Map<String, dynamic> o) {
  if (o['kind'] == 'booking') {
    return [_str(o['service_name']), if (_str(o['starts_at']).isNotEmpty) whenLabel(_str(o['starts_at']))].where((x) => x.isNotEmpty).join(' · ');
  }
  return [
    for (final l in _maps(o['basket']))
      if (_str(l['name']).isNotEmpty) '${(l['quantity'] as num?)?.toInt() ?? 1} × ${l['name']}',
  ].join(', ');
}

/// orders.ts orderTotal: a booking without a price is paid on the day.
String orderTotal(Map<String, dynamic> o) {
  if (o['kind'] == 'booking') return _int(o['total_kobo']) > 0 ? naira(_int(o['total_kobo'])) : 'Price on the day';
  return o['kind'] == 'order' ? naira(_int(o['total_kobo'])) : '—';
}

/// orders.ts priceChangeLine.
String priceChangeLine(Map<String, dynamic>? pc) {
  if (pc == null) return '';
  final gone = ((pc['gone'] as List?) ?? const []).whereType<String>().toList();
  return 'New total ${naira(_int(pc['total_kobo']))}.${gone.isEmpty ? '' : ' No longer offered: ${gone.join(', ')}.'}';
}

/// orders.ts paymentSummary: how the customer pays, and whether they have.
String paymentSummary(Map<String, dynamic> o) {
  final paid = _str(o['paid_at']).isNotEmpty ? 'paid' : 'not paid';
  final payment = _str(o['payment']);
  if (o['kind'] == 'booking') {
    if (payment == 'none') return 'Pays at the appointment';
    final when = payment == 'at_order' ? 'Pays when booking' : 'Pays after you confirm';
    final deposit = _int(o['deposit_kobo']) > 0 ? ' · deposit ${naira(_int(o['deposit_kobo']))}' : '';
    return '$when$deposit · $paid';
  }
  if (o['kind'] == 'order' && payment == 'on_delivery') return 'Pays at the door · $paid';
  if (o['kind'] != 'order' || payment == 'none') return 'No online payment';
  return '${payment == 'at_order' ? 'Pays when ordering' : 'Pays after you accept'} · $paid';
}

/// orders.ts refundLine.
String refundLine(int kobo, String? paidBy) => paidBy != null && paidBy.isNotEmpty && paidBy != 'card'
    ? '${naira(kobo)} is due back to the customer. They paid by bank transfer, so send it back from your bank, then mark it refunded.'
    : '${naira(kobo)} is due back to the customer. Refund it here through Paystack, or refund it yourself in Paystack.';

/// orders.ts isMasked: the server's masked form ("A•••", "•••4567").
bool isMasked(String? v) => v != null && v.contains('•••');

/// orders.ts whatsappLink: international digits, a Nigerian 0803… as
/// 234803…; none for a masked number.
String? whatsappLink(String? phone) {
  if (phone == null || phone.isEmpty || isMasked(phone)) return null;
  var d = phone.replaceAll(RegExp(r'\D'), '');
  if (d.length == 11 && d.startsWith('0')) d = '234${d.substring(1)}';
  return d.length >= 8 ? 'https://wa.me/$d' : null;
}

/// invoices.ts howMatchedOrder.
String howMatchedOrder(String how, String by) => switch (how) {
  'exact' => 'Paid through the order’s checkout page',
  'suggested' => by.isNotEmpty ? 'Suggested, confirmed by $by' : 'Suggested, then confirmed',
  'suggested_auto' => 'Matched automatically',
  'manual' => 'Matched by hand',
  _ => how.replaceAll('_', ' '),
};

// --- deliveries (lib/dispatch.ts) ---

/// dispatch.ts DELIVERY_STATUS: a delivery's state, with its tone.
({String label, Tone tone}) deliveryStatus(String s) => switch (s) {
  'offered' => (label: 'Offered', tone: Tone.warn),
  'declined' => (label: 'Said they can’t', tone: Tone.muted),
  'passed' => (label: 'No answer', tone: Tone.muted),
  'withdrawn' => (label: 'Offer taken back', tone: Tone.muted),
  'accepted' => (label: 'Accepted', tone: Tone.info),
  'picked_up' => (label: 'On the way', tone: Tone.info),
  'arrived' => (label: 'At the door', tone: Tone.info),
  'delivered' => (label: 'Delivered', tone: Tone.ok),
  'released' => (label: 'Taken back', tone: Tone.muted),
  _ => (label: s.replaceAll('_', ' '), tone: Tone.muted),
};

/// Whether a rider holds the delivery now.
bool isHeld(Map<String, dynamic> d) => const {'accepted', 'picked_up', 'arrived'}.contains(d['status']);

/// dispatch.ts currentDelivery: the one the order's rider holds or made,
/// else the live offer.
Map<String, dynamic>? currentDelivery(List<Map<String, dynamic>> list) =>
    list.where((d) => isHeld(d) || d['status'] == 'delivered').firstOrNull ?? list.where((d) => d['status'] == 'offered').firstOrNull;

/// dispatch.ts deliverySteps: the steps so far, with their Lagos times.
List<({String label, String at})> deliveryStepsOf(Map<String, dynamic> d) {
  final how = _str(d['paid_how']);
  final fee = _str(d['fee_paid']);
  final steps = [
    ('Offered to ${_str(d['rider'])}', d['offered_at']),
    ('Accepted', d['accepted_at']),
    ('Picked up', d['picked_up_at']),
    ('At the door', d['arrived_at']),
    (how == 'cash' ? 'Delivered, paid in cash' : (how == 'transfer' ? 'Delivered, paid by transfer at the door' : 'Delivered'), d['delivered_at']),
    (
      fee == 'cash'
          ? 'Confirmed, fee paid in cash'
          : fee == 'bank'
          ? 'Confirmed, fee paid to the rider’s bank'
          : fee == 'pending'
          ? 'Confirmed, fee on its way'
          : 'Confirmed, fee not paid yet',
      d['confirmed_at'],
    ),
    ('Taken back', d['released_at']),
  ];
  return [
    for (final (label, at) in steps)
      if (at is String && at.isNotEmpty) (label: label, at: lagosTime(at)),
  ];
}

/// dispatch.ts deliverySub: rider, area, fee and what is late.
String deliverySub(Map<String, dynamic> d) => [
  _str(d['rider']),
  if (_str(d['zone']).isNotEmpty) _str(d['zone']),
  if (_int(d['fee_kobo']) > 0) 'fee ${naira(_int(d['fee_kobo']))}',
  if (d['late'] == 'pickup') 'not picked up in time',
  if (d['late'] == 'arrival') 'not arrived in time',
  if (_int(d['rating']) > 0) 'rated ${d['rating']} of 5',
].join(' · ');

/// dispatch.ts offerOutcome: the words after an offer.
String offerOutcome(Map<String, dynamic> o) => switch (o['status']) {
  'offered' => 'Offered to ${_str(o['rider']).isEmpty ? 'a rider' : o['rider']}. They have a few minutes to accept.',
  'held' => '${_str(o['rider']).isEmpty ? 'A rider' : o['rider']} has it already.',
  'asked_owner' => 'You were asked in chat who takes it.',
  'no_rider' => 'No free rider covers this area now. You were told in chat.',
  _ => 'Done',
};

/// Riders are not available on this Taskiem (text messages not set up).
bool _dispatchOff(Object e) => e is ApiError && e.status == 409;

void _push(BuildContext context, Widget page) => Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => page));

/// Opens one order from a customer on its own screen.
void openStorefrontOrder(BuildContext context, Auth auth, String reference) => _push(context, StorefrontOrderPage(auth: auth, reference: reference));

/// `.notice`: a line that needs reading, said once.
class _Notice extends StatelessWidget {
  const _Notice(this.words, {this.tone = Tone.ok});
  final String words;
  final Tone tone;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final (bg, ink) = switch (tone) {
      Tone.warn => (t.tintWarn, t.tintWarnText),
      Tone.danger => (t.tkDangerBg, t.tkDangerInk),
      _ => (t.tkLimeSoft, t.text),
    };
    return Semantics(
      liveRegion: true,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(14)),
        child: Text(words, style: tkText(size: 14, weight: 500, color: ink)),
      ),
    );
  }
}

/// `dl.kv`: a word, and what it is, one under the other on a phone.
class _Kv extends StatelessWidget {
  const _Kv(this.rows);
  final List<(String, Widget)> rows;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final (k, v) in rows)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: MergeSemantics(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(k, style: tkText(size: 13, weight: 600, color: t.muted)),
                  const SizedBox(height: 2),
                  v,
                ],
              ),
            ),
          ),
      ],
    );
  }
}

Widget _text(TkTokens t, String s, {bool hint = false}) => Text(s, style: hint ? tkHint(t) : tkText(size: 15, color: t.text));

/// StorefrontOrders: the storefront's orders, newest first; a filter by
/// status and by front; the list refreshes itself.
class StorefrontOrdersPage extends StatefulWidget {
  const StorefrontOrdersPage({super.key, required this.auth, this.now});
  final Auth auth;
  final DateTime Function()? now;

  @override
  State<StorefrontOrdersPage> createState() => _StorefrontOrdersPageState();
}

class _StorefrontOrdersPageState extends State<StorefrontOrdersPage> with Polling {
  // Null: not read yet; empty and _none: this Taskiem takes none.
  List<Map<String, dynamic>>? _orders;
  bool _none = false;
  String? _error;
  String _status = 'open';
  String _front = '';
  List<Map<String, dynamic>> _fronts = const [];
  Map<String, dynamic>? _sf;
  bool _trade = false;
  int _asked = 0;

  Auth get _auth => widget.auth;
  DateTime get _now => (widget.now ?? DateTime.now)();

  @override
  void initState() {
    super.initState();
    _load();
    _side();
    // useLiveRefresh: read again while it is open (ages count on too).
    startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _side() async {
    try {
      final f = (await _auth.transport.send('GET', '/v1/storefront/fronts') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _fronts = _maps(f['fronts']));
    } on Object {
      // One front: nothing to choose.
    }
    try {
      final s = (await _auth.transport.send('GET', '/v1/storefront') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _sf = s);
    } on Object {
      // No storefront: the empty list says so.
    }
    try {
      final r = (await _auth.transport.send('GET', '/v1/trade-orders') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _trade = _maps(r['orders']).isNotEmpty);
    } on Object {
      // None.
    }
  }

  Future<void> _load() async {
    final asked = ++_asked;
    try {
      final r = (await _auth.transport.send('GET', '/v1/storefront/orders', query: ordersQuery(_status, _front)) as Map).cast<String, dynamic>();
      if (mounted && asked == _asked) setState(() => (_orders = _maps(r['orders']), _none = false, _error = null));
    } on ApiError catch (e) {
      if (!mounted || asked != _asked) return;
      setState(() => e.status == 404 ? (_orders = const [], _none = true) : _error = e.message);
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _error = errorWords(e));
    }
  }

  void _filter({String? status, String? front}) {
    setState(() {
      _status = status ?? _status;
      _front = front ?? _front;
      _orders = null;
    });
    _load();
  }

  String? _frontName(String id) => _fronts.length > 1 ? _fronts.where((f) => f['id'] == id).map((f) => _str(f['name'])).firstOrNull : null;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final orders = _orders;
    final now = _now;
    final share = _sf != null && _int(_sf!['published_version']) > 0 ? _str(_sf!['preview_url']) : '';
    return TkPageScreen(
      title: 'Orders',
      description: 'Orders, bookings, questions and call-back requests from your storefront, newest first. The list refreshes itself.',
      onRefresh: _load,
      children: [
        Text('You accept or decline each one in the approval request Taskiem sends you in WhatsApp or Telegram.', style: tkHint(t)),
        if (_trade)
          Wrap(
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              Text('Orders from businesses you are linked with are among these. ', style: tkHint(t)),
              TkLink(label: 'Orders from linked businesses', onTap: () => openTradeOrders(context, _auth)),
            ],
          ),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkSmallButton(
              label: 'Show: ${orderFilters.firstWhere((f) => f.$1 == _status).$2}',
              onPressed: () async {
                final pick = await showTkModalChoices(context, title: 'Show', value: _status, choices: orderFilters);
                if (pick != null && pick != _status) _filter(status: pick);
              },
            ),
            if (_fronts.length > 1)
              TkSmallButton(
                label: 'From: ${_front.isEmpty ? 'Every front' : (_frontName(_front) ?? 'Every front')}',
                onPressed: () async {
                  final pick = await showTkModalChoices(
                    context,
                    title: 'From',
                    value: _front,
                    choices: [('', 'Every front'), for (final f in _fronts) (_str(f['id']), _str(f['name']))],
                  );
                  if (pick != null && pick != _front) _filter(front: pick);
                },
              ),
            TkSmallButton(label: 'Refresh', onPressed: _load),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
        if (orders == null && _error == null) Text('Loading orders…', style: tkHint(t)),
        if (orders != null && _none) const TkEmpty(icon: 'storefront', title: 'This Taskiem does not take storefront orders yet.'),
        if (orders != null && !_none && orders.isEmpty)
          TkEmpty(
            icon: 'storefront',
            title: _status == 'open' || _status == 'all' ? 'No orders yet' : 'Nothing with this status.',
            line: _status == 'open' || _status == 'all'
                ? (share.isNotEmpty
                      ? 'Share your page: ${share.replaceFirst(RegExp(r'^https?://'), '')}'
                      : 'Publish your storefront and switch on its forms to take orders.')
                : null,
          ),
        if (orders != null && orders.isNotEmpty)
          TkPageCard(
            children: [
              for (final o in orders)
                () {
                  final c = _map(o['customer']);
                  final st = orderStatus(_str(o['status']));
                  final what = orderWhat(o);
                  final front = _frontName(_str(o['front_id']));
                  final extra = [
                    paymentSummary(o),
                    if (o['status'] == 'awaiting_payment' && _str(o['expires_at']).isNotEmpty) 'Expires ${timeLeft(_str(o['expires_at']), now)}',
                    if (o['status'] == 'price_changed' && o['price_change'] != null) priceChangeLine(_map(o['price_change'])),
                    if (_str(o['note']).isNotEmpty) _str(o['note']),
                    if (o['status'] == 'refund_due') refundLine(_int(o['total_kobo']), o['paid_by'] as String?),
                  ];
                  return TkRow(
                    name: '${[_str(c['name']), _str(c['phone'])].firstWhere((x) => x.isNotEmpty, orElse: () => 'A customer')} · ${kindLabel[o['kind']] ?? o['kind']}${front != null ? ' · $front' : ''}',
                    icon: o['status'] == 'refund_due'
                        ? 'refund'
                        : o['kind'] == 'booking'
                        ? 'clock'
                        : (o['kind'] == 'order' || o['kind'] == 'trade' ? 'invoices' : 'inbox'),
                    sub: [
                      '${_str(c['name']).isNotEmpty && _str(c['phone']).isNotEmpty ? '${c['phone']} · ' : ''}${_str(o['reference'])}${what.isNotEmpty ? ' · $what' : ''} · ${relativeAge(_str(o['created_at']), now)}',
                      '${orderTotal(o)} · ${extra.join(' · ')}',
                    ].join('\n'),
                    pill: TkPill(st.label, tone: _pillTone(st.tone)),
                    onTap: () async {
                      await Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => StorefrontOrderPage(auth: _auth, reference: _str(o['reference']), now: widget.now)));
                      await _load();
                    },
                  );
                }(),
            ],
          ),
      ],
    );
  }
}

/// Opens Orders from linked businesses (the trade orders' list).
void openTradeOrders(BuildContext context, Auth auth) => _push(context, TradeOrdersPage(auth: auth));

/// StorefrontOrder: one order, its customer's details for those who may
/// see them.
class StorefrontOrderPage extends StatefulWidget {
  const StorefrontOrderPage({super.key, required this.auth, required this.reference, this.now});
  final Auth auth;
  final String reference;
  final DateTime Function()? now;

  @override
  State<StorefrontOrderPage> createState() => _StorefrontOrderPageState();
}

class _StorefrontOrderPageState extends State<StorefrontOrderPage> with Polling {
  Map<String, dynamic>? _view;
  String? _error;
  Map<String, dynamic>? _revealed;
  String _changed = '';
  bool _busy = false;
  String? _actError;

  Auth get _auth => widget.auth;
  String get _ref => Uri.encodeComponent(widget.reference);
  DateTime get _now => (widget.now ?? DateTime.now)();

  @override
  void initState() {
    super.initState();
    _load();
    startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _load() async {
    try {
      final r = (await _auth.transport.send('GET', '/v1/storefront/orders/$_ref') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_view = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _actError = null));
    try {
      await f();
      if (mounted && done.isNotEmpty) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final o = _view == null ? null : _map(_view!['order']);
    return TkPageScreen(
      title: '${o == null ? 'Order' : (kindLabel[o['kind']] ?? 'Order')} ${widget.reference}',
      backLabel: 'Back to Orders',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (o == null && _error == null) Text('Loading the order…', style: tkHint(t)),
        if (o != null) ..._order(t, o),
      ],
    );
  }

  List<Widget> _order(TkTokens t, Map<String, dynamic> o) {
    final now = _now;
    final kind = _str(o['kind']);
    final status = _str(o['status']);
    final booking = kind == 'booking';
    final c = _map(o['customer']);
    final st = orderStatus(status);
    final refundKobo = _int(o['deposit_kobo']) > 0 ? _int(o['deposit_kobo']) : _int(o['total_kobo']);
    final paidBy = o['paid_by'] as String?;
    final card = paidBy == null || paidBy.isEmpty || paidBy == 'card';
    final basket = _maps(o['basket']);
    return [
      if (kind == 'order' || booking || kind == 'trade')
        TkHero(
          label: booking ? (_str(o['service_name']).isEmpty ? 'Booking' : _str(o['service_name'])) : 'Order total',
          tag: st.label,
          amount: orderTotal(o),
          sub:
              '${paymentSummary(o)}\nCustomer ${_str(c['name']).isEmpty ? '—' : c['name']} · ${booking ? (_str(o['starts_at']).isEmpty ? '—' : whenLabel(_str(o['starts_at']))) : 'Received ${relativeAge(_str(o['created_at']), now)}'}',
          semanticsLabel: booking ? 'The booking' : 'The order',
        ),
      if (status == 'refund_due')
        TkPageCard(
          children: [
            _Notice(refundLine(refundKobo, paidBy), tone: Tone.danger),
            if (_str(o['note']).isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: _text(t, _str(o['note']), hint: true)),
            const SizedBox(height: 10),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                if (card)
                  TkSmallButton(
                    label: 'Refund through Paystack',
                    onPressed: _busy
                        ? null
                        : () async {
                            if (!await showTkConfirm(
                              context,
                              title: 'Refund ${naira(refundKobo)} to the customer now?',
                              body: 'It comes from your Paystack balance.',
                              ok: 'Refund',
                              cancel: 'Not now',
                            )) {
                              return;
                            }
                            await _run(() => _auth.transport.send('POST', '/v1/storefront/orders/$_ref/refund', body: const {}), 'Refunded');
                          },
                  ),
                TkSmallButton(
                  label: 'Mark refunded',
                  primary: !card,
                  onPressed: _busy
                      ? null
                      : () async {
                          if (!await showTkConfirm(
                            context,
                            title: 'Mark ${o['reference']} refunded?',
                            body: 'Only once you have sent ${naira(refundKobo)} back to the customer. They will be told.',
                            ok: 'Mark refunded',
                            cancel: 'Not yet',
                          )) {
                            return;
                          }
                          await _run(() => _auth.transport.send('POST', '/v1/storefront/orders/$_ref/refunded', body: const {}), 'Marked refunded');
                        },
                ),
              ],
            ),
          ],
        ),
      if (_actError != null) TkErrorBox(_actError!),
      TkPageCard(
        title: 'Details',
        children: [
          _Kv([
            ('Status', Align(alignment: Alignment.centerLeft, child: TkPill(st.label, tone: _pillTone(st.tone)))),
            ('Received', _text(t, '${relativeAge(_str(o['created_at']), now)} (${whenLabel(_str(o['created_at']))})')),
            if (booking) ...[
              ('Service', _text(t, _str(o['service_name']).isEmpty ? '—' : _str(o['service_name']))),
              ('When', _text(t, _str(o['starts_at']).isEmpty ? '—' : '${whenLabel(_str(o['starts_at']))} (${timeSpan(o)}, Lagos time)')),
              ('Price', _text(t, orderTotal(o))),
            ],
            if (status == 'price_changed' && o['price_change'] != null)
              (
                'New prices',
                Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    _text(t, priceChangeLine(_map(o['price_change']))),
                    _text(t, 'The customer is asked to agree to the new total before the order goes on.', hint: true),
                  ],
                ),
              ),
            if (basket.isNotEmpty && !booking) ...[
              (
                'Items',
                Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    for (final l in basket)
                      _text(t, '${(l['quantity'] as num?)?.toInt() ?? 1} × ${_str(l['name'])}${l['line_kobo'] != null ? ' ${naira(_int(l['line_kobo']))}' : ''}'),
                    if (_str(o['zone']).isNotEmpty) _text(t, 'Delivery to ${o['zone']} ${naira(_int(o['delivery_kobo']))}'),
                  ],
                ),
              ),
              ('Total', _text(t, naira(_int(o['total_kobo'])))),
            ],
            if (_str(o['fulfilment']).isNotEmpty) ('Fulfilment', _text(t, '${_str(o['fulfilment'])[0].toUpperCase()}${_str(o['fulfilment']).substring(1)}')),
            if (kind == 'order' || booking)
              ('Payment', _text(t, '${paymentSummary(o)}${_str(o['paid_at']).isNotEmpty ? ' (${whenLabel(_str(o['paid_at']))})' : ''}')),
            if (status == 'awaiting_payment' && _str(o['expires_at']).isNotEmpty) ('Payment expires', _text(t, whenLabel(_str(o['expires_at'])))),
            if (_str(o['note']).isNotEmpty) ('Note', _text(t, _str(o['note']))),
            if (_str(o['decided_at']).isNotEmpty)
              ('Decided', _text(t, '${whenLabel(_str(o['decided_at']))}${_str(o['decided_by']).isNotEmpty ? ' by ${o['decided_by']}' : ''}')),
          ]),
          Text('You accept or decline each one in the approval request Taskiem sends you in WhatsApp or Telegram.', style: tkHint(t)),
          if (booking && canChange(o)) ...[
            if (_changed.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 10), child: _Notice(_changed)),
            const SizedBox(height: 10),
            Wrap(
              spacing: 8,
              children: [
                TkSmallButton(
                  label: 'Move',
                  semantics: 'Move ${o['service_name'] ?? 'booking'} ${o['reference']}',
                  onPressed: () async {
                    final said = await moveBooking(context, _auth, o);
                    if (said == null || !mounted) return;
                    setState(() => _changed = said);
                    await _load();
                  },
                ),
                TkSmallButton(
                  label: 'Cancel',
                  danger: true,
                  semantics: 'Cancel ${o['service_name'] ?? 'booking'} ${o['reference']}',
                  onPressed: _busy
                      ? null
                      : () async {
                          if (!await askCancelBooking(context, o) || !mounted) return;
                          await _run(() async {
                            final said = await cancelBooking(_auth, o);
                            if (mounted) setState(() => _changed = said);
                          }, '');
                        },
                ),
              ],
            ),
          ],
        ],
      ),
      if (kind == 'trade')
        TkPageCard(
          title: 'From a linked business',
          children: [
            _text(
              t,
              '${_str(c['name']).isEmpty ? 'A business' : c['name']} ordered on Taskiem. Its items, a change to them, and what was delivered of each are on the linked order.',
              hint: true,
            ),
            const SizedBox(height: 10),
            Align(
              alignment: Alignment.centerLeft,
              child: TkSmallButton(label: 'Open the linked order', onPressed: () => openTradeOrder(context, _auth, _str(o['reference']))),
            ),
          ],
        ),
      if ((kind == 'order' || kind == 'trade') && o['fulfilment'] == 'delivery')
        OrderDelivery(key: ValueKey('delivery ${widget.reference}'), auth: _auth, reference: widget.reference, onChanged: _load),
      _money(t),
      TkPageCard(
        title: 'Customer',
        children: [
          _CustomerDetails(c: _revealed ?? c),
          if (_revealed == null && _auth.can('pii.reveal')) ...[
            const SizedBox(height: 4),
            Align(
              alignment: Alignment.centerLeft,
              child: TkSmallButton(
                label: 'Show customer’s details',
                onPressed: _busy
                    ? null
                    : () => _run(() async {
                        final r = (await _auth.transport.send('GET', '/v1/storefront/orders/$_ref', query: {'reveal': 'true'}) as Map).cast<String, dynamic>();
                        if (mounted) setState(() => _revealed = _map(_map(r['order'])['customer']));
                      }, ''),
              ),
            ),
            const SizedBox(height: 6),
            Text('Showing them is recorded in the audit log.', style: tkHint(t)),
          ],
          if (_revealed == null && !_auth.can('pii.reveal'))
            Text('The customer’s details are hidden. Someone who may see personal data can show them.', style: tkHint(t)),
        ],
      ),
    ];
  }

  /// OrderMoney: the money matched to an order, each with how; a wrong
  /// match undone while the order has not gone on.
  Widget _money(TkTokens t) {
    final rows = [
      for (final p in _maps(_view?['payments']))
        if (_str(p['undoes']).isEmpty) (p: p, undone: _str(p['undone_by']).isNotEmpty),
    ];
    if (rows.isEmpty) return const SizedBox.shrink();
    final manage = _auth.can('invoice.manage');
    return TkPageCard(
      title: 'Money matched',
      children: [
        for (final r in rows)
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Wrap(
                  spacing: 6,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    Text(naira(_int(r.p['amount_kobo'])), style: tkText(size: 15, weight: 700, color: t.text)),
                    _text(t, howMatchedOrder(_str(r.p['how']), _str(r.p['by']))),
                    Text(whenLabel(_str(r.p['created_at'])), style: tkHint(t)),
                    if (r.undone) const TkPill('Undone'),
                  ],
                ),
                for (final why in ((r.p['reasons'] as List?) ?? const []).whereType<String>()) Text('· $why', style: tkHint(t)),
                if (manage && !r.undone && r.p['undoable'] == true)
                  Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: TkSmallButton(
                      label: 'Undo the match',
                      onPressed: _busy
                          ? null
                          : () async {
                              if (!await showTkConfirm(
                                context,
                                title: 'Undo this match?',
                                body: 'The order goes back to unpaid, and the money waits to be matched again.',
                                ok: 'Undo the match',
                                cancel: 'Keep it',
                                danger: true,
                              )) {
                                return;
                              }
                              await _run(
                                () => _auth.transport.send('POST', '/v1/money/order-payments/${Uri.encodeComponent(_str(r.p['id']))}/undo', body: const {}),
                                'Match undone',
                              );
                            },
                    ),
                  ),
              ],
            ),
          ),
      ],
    );
  }
}

/// CustomerDetails: who the customer is, with a call, WhatsApp and the
/// map when the details are shown.
class _CustomerDetails extends StatelessWidget {
  const _CustomerDetails({required this.c});
  final Map<String, dynamic> c;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final phone = _str(c['phone']);
    final wa = whatsappLink(phone);
    final pin = _str(c['pin']);
    final rows = <(String, Widget)>[
      if (_str(c['name']).isNotEmpty) ('Name', _text(t, _str(c['name']))),
      if (phone.isNotEmpty)
        (
          'Phone',
          isMasked(phone)
              ? _text(t, phone)
              : Wrap(
                  spacing: 12,
                  children: [
                    TkLink(label: 'Call $phone', onTap: () => callPhone(phone)),
                    if (wa != null) TkLink(label: 'WhatsApp', onTap: () => openOutside(Uri.parse(wa))),
                  ],
                ),
        ),
      if (_str(c['email']).isNotEmpty) ('Email', _text(t, _str(c['email']))),
      if (_str(c['address']).isNotEmpty) ('Address', _text(t, _str(c['address']))),
      if (_str(c['landmark']).isNotEmpty) ('Landmark', _text(t, _str(c['landmark']))),
      if (pin.isNotEmpty)
        ('Location', pin.startsWith('https://') ? Align(alignment: Alignment.centerLeft, child: TkLink(label: 'Open the map', onTap: () => openOutside(Uri.parse(pin)))) : _text(t, pin)),
      if (_str(c['message']).isNotEmpty) ('Message', _text(t, _str(c['message']))),
      if (_str(c['best_time']).isNotEmpty) ('Best time', _text(t, _str(c['best_time']))),
    ];
    if (rows.isEmpty) return Text('No details given.', style: tkHint(t));
    return _Kv(rows);
  }
}

/// OrderDelivery: an order's delivery on its page: who has it and how far
/// it got, with Offer, Choose the rider, Confirm the fee and Take it back.
/// Nothing with riders off.
class OrderDelivery extends StatefulWidget {
  const OrderDelivery({super.key, required this.auth, required this.reference, required this.onChanged});
  final Auth auth;
  final String reference;
  final Future<void> Function() onChanged;

  @override
  State<OrderDelivery> createState() => _OrderDeliveryState();
}

class _OrderDeliveryState extends State<OrderDelivery> {
  Map<String, dynamic>? _v;
  String? _error;
  bool _off = false;
  String _said = '';
  bool _busy = false;
  bool _earlier = false;

  String get _ref => Uri.encodeComponent(widget.reference);
  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = (await _t.send('GET', '/v1/storefront/orders/$_ref/deliveries') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_v = r, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _dispatchOff(e) ? _off = true : _error = errorWords(e));
    }
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _error = null));
    try {
      await f();
      if (mounted && done.isNotEmpty) showTkToast(context, done);
      await _load();
      await widget.onChanged();
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _offer([String? rider]) => _run(() async {
    final out = (await _t.send('POST', '/v1/storefront/orders/$_ref/offer', body: {'rider_id': ?rider}) as Map?)?.cast<String, dynamic>() ?? const {};
    if (mounted) setState(() => _said = offerOutcome(out));
  }, '');

  Future<void> _choose() async {
    final id = await showTkModal<String>(context, title: 'Choose the rider', builder: (_) => _ChooseRider(auth: widget.auth));
    if (id != null) await _offer(id);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (_off) return const SizedBox.shrink();
    final v = _v;
    final list = _maps(v?['deliveries']);
    final cur = v == null ? null : currentDelivery(list);
    final earlier = [
      for (final d in list)
        if (!identical(d, cur)) d,
    ];
    final notReady = _str(v?['not_ready']);
    return TkPageCard(
      title: 'Delivery',
      children: [
        if (_error != null) Padding(padding: const EdgeInsets.only(bottom: 10), child: TkErrorBox(_error!)),
        if (v == null && _error == null) Text('Loading the delivery…', style: tkHint(t)),
        if (_said.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 10), child: _Notice(_said)),
        if (cur != null) ...[
          _Kv([
            (
              'Rider',
              Wrap(
                spacing: 8,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  _text(t, _str(cur['rider'])),
                  if (_str(cur['rider_phone']).isNotEmpty) TkLink(label: 'Call ${cur['rider_phone']}', onTap: () => callPhone(_str(cur['rider_phone']))),
                ],
              ),
            ),
            (
              'Status',
              Wrap(
                spacing: 8,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  TkPill(deliveryStatus(_str(cur['status'])).label, tone: _pillTone(deliveryStatus(_str(cur['status'])).tone)),
                  if (cur['late'] != null) Text(cur['late'] == 'pickup' ? 'Not picked up in time' : 'Not at the door in time', style: tkHint(t)),
                ],
              ),
            ),
            if (_int(cur['fee_kobo']) > 0) ('Rider’s fee', _text(t, naira(_int(cur['fee_kobo'])))),
            if (_int(cur['rating']) > 0) ('Customer’s rating', _text(t, '${cur['rating']} of 5')),
          ]),
          Semantics(
            container: true,
            label: 'How far it got',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                for (final (i, s) in deliveryStepsOf(cur).indexed) Text('${i + 1}. ${s.label}  ${s.at}', style: tkText(size: 14, color: t.text)),
              ],
            ),
          ),
          if (_str(cur['proof_url']).isNotEmpty || _str(cur['location']).isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Wrap(
                spacing: 12,
                children: [
                  if (_str(cur['proof_url']).isNotEmpty) TkLink(label: 'See the photo from the door', onTap: () => openInBrowser(Uri.parse(_str(cur['proof_url'])))),
                  if (_str(cur['location']).isNotEmpty)
                    TkLink(
                      label: 'Where the rider was',
                      onTap: () => openOutside(
                        Uri.parse(
                          _str(cur['location']).startsWith('https://')
                              ? _str(cur['location'])
                              : 'https://maps.google.com/?q=${Uri.encodeQueryComponent(_str(cur['location']))}',
                        ),
                      ),
                    ),
                ],
              ),
            ),
        ],
        if (v != null && cur == null) Text(notReady.isNotEmpty ? 'Not ready for a rider: $notReady.' : 'No rider has it yet.', style: tkHint(t)),
        if (v != null) ...[
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              if (notReady.isEmpty && (cur == null || cur['status'] == 'offered')) ...[
                TkSmallButton(label: cur != null ? 'Offer to the next free rider' : 'Offer to a free rider', primary: true, onPressed: _busy ? null : () => _offer()),
                TkSmallButton(label: 'Choose the rider', onPressed: _busy ? null : _choose),
              ],
              if (cur != null && cur['status'] == 'delivered' && _str(cur['fee_paid']).isEmpty) ...[
                TkSmallButton(
                  label: 'Confirm, pay the fee',
                  primary: true,
                  onPressed: _busy
                      ? null
                      : () => _run(() async {
                          await _t.send('POST', '/v1/storefront/orders/$_ref/confirm', body: const {});
                          _said = '';
                        }, 'Confirmed: the fee is on its way'),
                ),
                TkSmallButton(
                  label: 'Paid in cash',
                  onPressed: _busy
                      ? null
                      : () => _run(() async {
                          await _t.send('POST', '/v1/storefront/orders/$_ref/confirm', body: {'how': 'cash'});
                          _said = '';
                        }, 'Paid in cash'),
                ),
              ],
              if (cur != null && (isHeld(cur) || cur['status'] == 'offered'))
                TkSmallButton(
                  label: 'Take it back',
                  danger: true,
                  onPressed: _busy
                      ? null
                      : () async {
                          if (!await showTkConfirm(
                            context,
                            title: 'Take ${widget.reference} back from ${cur['rider']}?',
                            body: 'They are told, and you can offer it again.',
                            ok: 'Take it back',
                            cancel: 'Leave it with them',
                            danger: true,
                          )) {
                            return;
                          }
                          await _run(() async {
                            await _t.send('POST', '/v1/storefront/orders/$_ref/release', body: const {});
                            _said = '';
                          }, 'Taken back');
                        },
                ),
            ],
          ),
        ],
        if (earlier.isNotEmpty) ...[
          const SizedBox(height: 10),
          Align(
            alignment: Alignment.centerLeft,
            child: TkLink(label: '${_earlier ? 'Hide' : 'Show'} earlier offers (${earlier.length})', onTap: () => setState(() => _earlier = !_earlier)),
          ),
          if (_earlier)
            for (final d in earlier)
              TkRow(
                name: _str(d['rider']),
                letters: _str(d['rider']).isEmpty ? '?' : _str(d['rider']).substring(0, 1).toUpperCase(),
                sub: whenLabel(_str(d['offered_at'])),
                pill: TkPill(deliveryStatus(_str(d['status'])).label, tone: _pillTone(deliveryStatus(_str(d['status'])).tone)),
              ),
        ],
      ],
    );
  }
}

/// ChooseRider: the current riders, free first; pops the rider chosen.
class _ChooseRider extends StatefulWidget {
  const _ChooseRider({required this.auth});
  final Auth auth;

  @override
  State<_ChooseRider> createState() => _ChooseRiderState();
}

class _ChooseRiderState extends State<_ChooseRider> {
  List<Map<String, dynamic>>? _riders;
  String? _error;

  @override
  void initState() {
    super.initState();
    widget.auth.transport.send('GET', '/v1/riders').then(
      (r) {
        const order = {'free': 0, 'busy': 1, 'off': 2};
        final list = _maps((r as Map)['riders']).where((x) => x['active'] == true).toList()
          ..sort((a, b) => (order[_map(a['presence'])['state']] ?? 3).compareTo(order[_map(b['presence'])['state']] ?? 3));
        if (mounted) setState(() => _riders = list);
      },
      onError: (Object e) {
        if (mounted) setState(() => _error = errorWords(e));
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final riders = _riders;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (riders == null && _error == null) Text('Loading your riders…', style: tkHint(t)),
        if (riders != null && riders.isEmpty) Text('No riders yet. Add your riders on the web, under Storefront, Riders.', style: tkHint(t)),
        if (riders != null)
          for (final r in riders)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Row(
                children: [
                  Expanded(
                    child: MergeSemantics(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(_str(r['name']), style: tkText(size: 15, weight: 600, color: t.text)),
                          Text(((r['zones'] as List?) ?? const []).whereType<String>().join(', ').ifEmptyAll, style: tkHint(t)),
                        ],
                      ),
                    ),
                  ),
                  TkSmallButton(label: 'Offer', semantics: 'Offer to ${r['name']}', onPressed: () => Navigator.of(context).pop(_str(r['id']))),
                ],
              ),
            ),
        const SizedBox(height: 4),
        Align(alignment: Alignment.centerLeft, child: TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop())),
      ],
    );
  }
}

extension on String {
  String get ifEmptyAll => isEmpty ? 'All areas' : this;
}

/// StorefrontDeliveries: the day's deliveries (or another day's, or those
/// under way), and how orders are offered to riders.
class DeliveriesPage extends StatefulWidget {
  const DeliveriesPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<DeliveriesPage> createState() => _DeliveriesPageState();
}

class _DeliveriesPageState extends State<DeliveriesPage> with Polling {
  List<Map<String, dynamic>>? _list;
  String? _error;
  bool _off = false;
  bool _open = false;
  String _day = '';
  final _dayBox = TextEditingController();
  bool? _on;
  bool _busy = false;
  int _asked = 0;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _dayBox.addListener(() => setState(() {}));
    _load();
    _auth.transport.send('GET', '/v1/dispatch/workflow').then((r) {
      if (mounted) setState(() => _on = (r as Map)['on'] == true);
    }, onError: (Object _) {});
    startPolling(const Duration(seconds: 30), _load);
  }

  @override
  void dispose() {
    _dayBox.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final asked = ++_asked;
    try {
      final r = (await _auth.transport.send('GET', '/v1/deliveries', query: _open ? {'open': 'true'} : {'day': ?(_day.isEmpty ? null : _day)}) as Map)
          .cast<String, dynamic>();
      if (mounted && asked == _asked) setState(() => (_list = _maps(r['deliveries']), _error = null));
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _dispatchOff(e) ? _off = true : _error = errorWords(e));
    }
  }

  void _show({bool? open, String? day}) {
    setState(() {
      _open = open ?? _open;
      _day = day ?? _day;
      _list = null;
    });
    _load();
  }

  /// A workflow the business made itself is replaced only once the owner
  /// says so.
  Future<void> _turnOn() async {
    setState(() => _busy = true);
    try {
      try {
        await _auth.transport.send('POST', '/v1/dispatch/workflow', body: const {});
      } on ApiError catch (e) {
        if (e.body['code'] != 'replaces_workflow') rethrow;
        if (!mounted) return;
        final yes = await showTkConfirm(
          context,
          title: 'Turn on deliveries?',
          body: 'Your order form starts your own workflow “${e.body['workflow'] ?? ''}”. Turning on deliveries takes it out of use.',
          ok: 'Go ahead',
          cancel: 'Not now',
        );
        if (!yes) return;
        await _auth.transport.send('POST', '/v1/dispatch/workflow', body: {'replace': true});
      }
      if (mounted) {
        showTkToast(context, 'Deliveries are on');
        setState(() => _on = true);
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final list = _list;
    final typed = _dayBox.text.trim();
    final today = lagosDay(DateTime.now().toUtc().toIso8601String());
    return TkPageScreen(
      title: 'Deliveries',
      description: 'Your riders’ deliveries: who has each order, how far it got, and how it was paid at the door.',
      onRefresh: _load,
      action: _off
          ? null
          : TkSmallButton(
              label: 'Settings',
              semantics: 'How orders are offered',
              onPressed: () async {
                final saved = await showTkModal<bool>(context, title: 'How orders are offered', builder: (_) => _DispatchSettings(auth: _auth));
                if (saved == true && mounted) showTkToast(this.context, 'Saved');
              },
            ),
      children: [
        if (_off) const TkEmpty(icon: 'members', title: 'Deliveries need text messages, which are not set up on this Taskiem yet.'),
        if (!_off && _error != null) TkErrorBox(_error!),
        if (!_off && _on == false)
          TkPageCard(
            title: 'Offer orders to your riders by themselves',
            children: [
              Text(
                'Each order for delivery you confirm then goes to your riders in turn, and the rider’s Delivered completes it. Your order form stays as it is; without this, you offer each order from its page.',
                style: tkHint(t),
              ),
              const SizedBox(height: 10),
              Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(label: 'Turn on deliveries', primary: true, onPressed: _busy ? null : _turnOn),
              ),
            ],
          ),
        if (!_off)
          Wrap(
            spacing: 8,
            runSpacing: 8,
            crossAxisAlignment: WrapCrossAlignment.end,
            children: [
              TkSmallButton(label: 'Under way now', primary: _open, onPressed: () => _show(open: !_open)),
              SizedBox(
                width: 150,
                child: TkKitField(label: 'Day', child: TkTextBox(controller: _dayBox, hint: today, height: 44, size: 16)),
              ),
              TkSmallButton(
                label: 'Show that day',
                onPressed: _open || !(typed.isEmpty || RegExp(r'^\d{4}-\d{2}-\d{2}$').hasMatch(typed)) || typed == _day ? null : () => _show(day: typed),
              ),
            ],
          ),
        if (!_off && list == null && _error == null) Text('Loading deliveries…', style: tkHint(t)),
        if (list != null && list.isEmpty) TkEmpty(icon: 'invoices', title: _open ? 'No delivery is under way.' : 'No deliveries that day.'),
        if (list != null && list.isNotEmpty)
          TkPageCard(
            children: [
              for (final d in list)
                TkRow(
                  name: _str(d['reference']),
                  icon: 'invoices',
                  sub: '${deliverySub(d)}\n${whenLabel(_str(d['delivered_at']).isNotEmpty ? _str(d['delivered_at']) : _str(d['offered_at']))}',
                  pill: TkPill(
                    deliveryStatus(_str(d['status'])).label,
                    tone: d['late'] != null ? CardTone.danger : _pillTone(deliveryStatus(_str(d['status'])).tone),
                  ),
                  onTap: () async {
                    await Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => StorefrontOrderPage(auth: _auth, reference: _str(d['reference']))));
                    await _load();
                  },
                ),
            ],
          ),
      ],
    );
  }
}

/// DispatchSettingsModal: how orders are offered to riders, how long they
/// have, and the marks for cash owed and ratings.
class _DispatchSettings extends StatefulWidget {
  const _DispatchSettings({required this.auth});
  final Auth auth;

  @override
  State<_DispatchSettings> createState() => _DispatchSettingsState();
}

class _DispatchSettingsState extends State<_DispatchSettings> {
  bool _read = false;
  String _mode = 'rotation';
  bool _proof = false;
  final _offer = TextEditingController();
  final _pickup = TextEditingController();
  final _arrive = TextEditingController();
  final _owe = TextEditingController();
  final _mark = TextEditingController();
  bool _busy = false;
  String? _error;

  List<TextEditingController> get _boxes => [_offer, _pickup, _arrive, _owe, _mark];

  @override
  void initState() {
    super.initState();
    for (final c in _boxes) {
      c.addListener(() => setState(() {}));
    }
    widget.auth.transport.send('GET', '/v1/dispatch/settings').then(
      (r) {
        final s = (r as Map).cast<String, dynamic>();
        if (!mounted) return;
        setState(() {
          _read = true;
          _mode = s['mode'] == 'ask' ? 'ask' : 'rotation';
          _proof = s['proof'] == true;
          _offer.text = '${_int(s['offer_minutes'])}';
          _pickup.text = '${_int(s['pickup_minutes'])}';
          _arrive.text = '${_int(s['arrive_minutes'])}';
          _owe.text = feeInput(_int(s['owe_limit_kobo']));
          _mark.text = '${s['rating_mark'] ?? ''}';
        });
      },
      onError: (Object e) {
        if (mounted) setState(() => _error = errorWords(e));
      },
    );
  }

  @override
  void dispose() {
    for (final c in _boxes) {
      c.dispose();
    }
    super.dispose();
  }

  int? _whole(TextEditingController c) => RegExp(r'^\d+$').hasMatch(c.text.trim()) ? int.parse(c.text.trim()) : null;

  /// dispatch.ts settingsProblem.
  String? get _problem {
    final offer = _whole(_offer), pickup = _whole(_pickup), arrive = _whole(_arrive);
    if (offer == null || offer < 1 || offer > 60) return 'Riders have 1 to 60 minutes to accept an offer.';
    if (pickup == null || pickup < 5 || pickup > 240) return 'Picking up takes 5 to 240 minutes.';
    if (arrive == null || arrive < 10 || arrive > 600) return 'Getting to the customer takes 10 to 600 minutes.';
    if (_owe.text.trim().isEmpty || feeKobo(_owe.text) == null) return 'Enter the cash a rider may owe, like 20000.';
    final mark = num.tryParse(_mark.text.trim());
    if (mark == null || mark < 1 || mark > 5) return 'The rating mark is 1 to 5.';
    return null;
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send(
        'PUT',
        '/v1/dispatch/settings',
        body: {
          'mode': _mode,
          'offer_minutes': _whole(_offer),
          'pickup_minutes': _whole(_pickup),
          'arrive_minutes': _whole(_arrive),
          'proof': _proof,
          'owe_limit_kobo': feeKobo(_owe.text),
          'rating_mark': num.parse(_mark.text.trim()),
        },
      );
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (!_read) return _error != null ? TkErrorBox(_error!) : Text('Loading…', style: tkHint(t));
    Widget box(String label, TextEditingController c, {String? hint, bool money = false}) =>
        TkKitField(label: label, hint: hint, child: TkTextBox(controller: c, digits: money ? null : 3, money: money));
    return TkModalForm(
      saveWords: _busy ? 'Saving…' : 'Save',
      busy: _busy,
      problem: _problem,
      error: _error,
      save: _save,
      children: [
        TkKitField(
          field: false,
          label: 'When an order is ready for a rider',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (final (v, words) in const [('rotation', 'Offer it to free riders in turn, those covering the area first'), ('ask', 'Ask me in chat who takes it')])
                TkChoiceRow(radio: true, control: TkRadio(checked: _mode == v), label: Text(words, style: tkText(size: 15, color: t.text)), onTap: () => setState(() => _mode = v)),
            ],
          ),
        ),
        box('Minutes a rider has to accept', _offer),
        box('Minutes to pick up', _pickup, hint: 'After this, you are told the rider has not picked it up.'),
        box('Minutes to get to the customer', _arrive, hint: 'After this, you are told the rider has not arrived.'),
        TkSwitchRow(label: 'Ask riders for a photo at the door', on: _proof, onChanged: (v) => setState(() => _proof = v)),
        box('Cash a rider may owe (₦)', _owe, hint: 'Above this, it shows under Money leaks.', money: true),
        TkKitField(label: 'Tell me when a rider’s rating falls below (1 to 5)', child: TkTextBox(controller: _mark, money: true)),
      ],
    );
  }
}

/// ChatAlerts.tsx offerWords: what an offer came to when it did not go out.
const _offerWords = {'held': 'A rider has it already', 'no_rider': 'No rider could take it. You are told when one is free', 'asked_owner': 'You are asked who takes it'};

/// Opens Assign rider for an order: the next in turn, or one by name;
/// [onDone] once it is offered.
Future<void> showRiderSheet(BuildContext context, {required Auth auth, required String reference, String line = '', VoidCallback? onDone}) =>
    showTkSheet<void>(context, (_) => RiderSheet(auth: auth, reference: reference, line: line, onDone: onDone));

/// ChatAlerts.tsx RiderSheet.
class RiderSheet extends StatefulWidget {
  const RiderSheet({super.key, required this.auth, required this.reference, this.line = '', this.onDone});
  final Auth auth;
  final String reference;
  final String line;
  final VoidCallback? onDone;

  @override
  State<RiderSheet> createState() => _RiderSheetState();
}

class _RiderSheetState extends State<RiderSheet> with CloseSoon {
  List<Map<String, dynamic>>? _riders;
  String? _error;
  String? _note;
  String? _saved;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    widget.auth.transport.send('GET', '/v1/riders').then(
      (r) {
        if (mounted) setState(() => _riders = _maps((r as Map)['riders']).where((x) => x['active'] == true).toList());
      },
      onError: (Object e) {
        if (mounted) setState(() => _error = errorWords(e));
      },
    );
  }

  Future<void> _offer([Map<String, dynamic>? r]) async {
    setState(() => (_busy = true, _note = null, _error = null));
    try {
      final out =
          (await widget.auth.transport.send('POST', '/v1/storefront/orders/${Uri.encodeComponent(widget.reference)}/offer', body: {'rider_id': ?r?['id']}) as Map?)
              ?.cast<String, dynamic>() ??
          const {};
      if (!mounted) return;
      // Only an offer out to a rider is done; the rest say why not.
      if (out['status'] != 'offered') {
        setState(() => (_note = _offerWords[out['status']] ?? 'It was not offered', _busy = false));
        return;
      }
      setState(() => _saved = 'Offered to ${_str(out['rider']).isNotEmpty ? out['rider'] : (r?['name'] ?? 'a rider')}');
      widget.onDone?.call();
      closeSoon();
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final riders = _riders;
    return TkSheet(
      title: 'A rider for ${widget.reference}',
      overlay: _saved == null ? null : TkSavedTick(words: _saved!),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        children: [
          if (widget.line.isNotEmpty) Padding(padding: const EdgeInsets.fromLTRB(4, 0, 4, 10), child: Text(widget.line, style: tkText(size: 15, color: t.text))),
          if (_note != null) Padding(padding: const EdgeInsets.fromLTRB(4, 0, 4, 10), child: _Notice(_note!, tone: Tone.warn)),
          if (_error != null) TkErrorBox(_error!),
          if (riders == null && _error == null) const TkLoading(rows: 2, label: 'Loading your riders…'),
          if (riders != null && riders.isEmpty) const TkEmpty(icon: 'store', title: 'No riders on duty', line: 'Add riders on the Deliveries page.'),
          if (riders != null && riders.isNotEmpty) ...[
            TkRow(name: 'Next in turn', icon: 'loop', sub: 'Riders are asked one after another', onTap: _busy ? null : () => _offer()),
            for (final r in riders)
              TkRow(
                name: _str(r['name']),
                icon: 'store',
                sub: ((r['zones'] as List?) ?? const []).whereType<String>().join(', ').ifEmptyAny,
                onTap: _busy ? null : () => _offer(r),
              ),
          ],
        ],
      ),
    );
  }
}

extension on String {
  String get ifEmptyAny => isEmpty ? 'Any area' : this;
}
