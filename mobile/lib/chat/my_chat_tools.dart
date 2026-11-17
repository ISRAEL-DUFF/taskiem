// The tools of a person's chat with a business (docs/spec/taskiem-for-everyone.md
// §8.3, §8.4), behind the button by the box as a linked business's are:
// **Their shop** (their items at their shop's prices, in sizes and
// colours, few left, pickup or delivery, and ordering), **Book** (their
// services and free times), **Your orders with them**, and **Pay** (an
// order's pay link, opened in the app). Orders and bookings carry an
// Idempotency-Key, so a second press places nothing twice.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/links.dart';
import '../app/session.dart' show uuid4;
import '../kit/forms.dart' show CloseSoon;
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/look.dart' show tkTabular;
import '../kit/mobile.dart' show TkFormGroup, TkListRow, TkPanel, TkSection;
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'book_sheet.dart' show showBookSheet, whenLabel;
import 'chats.dart' show ChatAvatar;
import 'door.dart';
import 'addresses.dart' show addressLine;
import 'my_chat.dart';
import 'order_sheet.dart' show BagBar, QtyStepper;
import 'words.dart';

/// The tools button: what a customer can do with the business.
Future<void> showMyChatTools(BuildContext context, MyChatData d) async {
  final pick = await showTkSheet<String>(context, (_) => _ToolsSheet(name: d.name));
  if (pick == null || !context.mounted) return;
  switch (pick) {
    case 'shop':
      await showShopSheet(context, d);
    case 'book':
      await showBookSheet(context, auth: d.auth, link: '', business: d.name, customer: d.base, onBooked: (_) => d.refresh());
    case 'orders':
      await showMyOrdersSheet(context, d);
    case 'pay':
      await showMyOrdersSheet(context, d, due: true);
  }
}

class _ToolsSheet extends StatelessWidget {
  const _ToolsSheet({required this.name});
  final String name;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    Widget row(String key, String icon, Color colour, String title, String sub) => TkListRow(
      title: title,
      sub: sub,
      icon: icon,
      square: colour,
      onTap: () => Navigator.of(context).pop(key),
    );
    // As the chat's tools are drawn: each on its accent's square, the rows
    // on one card on the sheet's ground.
    return TkAppLook(
      child: TkSheet(
        title: 'With $name',
        lead: ChatAvatar(name: name, size: 38, letterSize: 13),
        crumb: 'Order, book and pay here in the chat',
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            TkSection(
              inset: 56,
              children: [
                row('shop', 'b-store', look.lime, 'Their shop', 'What they sell, at their shop’s prices'),
                row('book', 'b-calendar', look.violet, 'Book', 'Their services and free times'),
                row('orders', 'b-box', look.teal, 'Your orders with them', 'What you ordered and booked here'),
                row('pay', 'b-card', look.orange, 'Pay', 'What’s due on your orders'),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// A size or colour's words: "M · Red".
String variantWords(Map<String, dynamic> v) => v.values.map((x) => '$x').where((x) => x.isNotEmpty).join(' · ');

/// How their shop takes payment, said before ordering.
String payWords(String payment, String business) => switch (payment) {
  'at_order' => 'You pay when you order, here in the app.',
  'after_accept' => 'You pay once $business accepts your order. They’ll send the pay link here.',
  _ => 'You pay $business as you agree with them.',
};

// --- Their shop ---

Future<void> showShopSheet(BuildContext context, MyChatData d) => showTkSheet<void>(context, (_) => ShopSheet(d: d));

/// One thing that can go in the bag: an item, or one of its sizes.
typedef _Thing = ({String id, String name, int? price, int? left, bool available});

class ShopSheet extends StatefulWidget {
  const ShopSheet({super.key, required this.d});
  final MyChatData d;

  @override
  State<ShopSheet> createState() => _ShopSheetState();
}

class _ShopSheetState extends State<ShopSheet> with CloseSoon {
  MyChatShopResponse? _shop;
  String? _loadError;
  final Map<String, int> _bag = {};
  bool _checkout = false;
  String _fulfilment = '';
  String? _zone;
  final _address = TextEditingController();
  final _landmark = TextEditingController();
  final _note = TextEditingController();

  /// The person's saved addresses (§13), offered for a delivery; the one
  /// picked (null: an address typed), and whether a typed one is saved.
  List<SavedAddress> _saved = const [];
  String? _addressId;
  bool _saveIt = false;
  final _saveAs = TextEditingController();
  bool _busy = false;
  String? _error;
  String? _done;

  /// One key for one bag: pressed again after an answer that never came,
  /// it places nothing twice.
  String? _key;
  String _keyFor = '';

  MyChatData get d => widget.d;

  @override
  void initState() {
    super.initState();
    d.api.myChatShop(id: d.id).then((s) {
      if (!mounted) return;
      setState(() {
        _shop = s;
        _fulfilment = s.pickup ? 'pickup' : (s.delivery ? 'delivery' : '');
      });
    }, onError: (Object e) {
      if (mounted) setState(() => _loadError = sayFailure(e));
    });
    d.api.myAddresses().then((r) {
      if (!mounted) return;
      setState(() {
        _saved = r.addresses;
        _addressId = r.addresses.where((a) => a.default_).firstOrNull?.id ?? r.addresses.firstOrNull?.id;
      });
    }, onError: (Object _) {
      // None offered: the address is typed.
    });
    _address.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _address.dispose();
    _landmark.dispose();
    _saveAs.dispose();
    _note.dispose();
    super.dispose();
  }

  Map<String, _Thing> get _things {
    final out = <String, _Thing>{};
    for (final i in _shop?.items ?? const <ShopItem>[]) {
      final sizes = i.sizes ?? const [];
      if (sizes.isEmpty) {
        out[i.id] = (id: i.id, name: i.name, price: i.priceKobo, left: i.fewLeft, available: i.available);
      } else {
        for (final s in sizes) {
          out[s.id] = (id: s.id, name: '${i.name} (${variantWords(s.variant)})', price: s.priceKobo ?? i.priceKobo, left: s.fewLeft, available: s.available && i.available);
        }
      }
    }
    return out;
  }

  int get _count => _bag.values.fold(0, (a, b) => a + b);
  int get _subtotal {
    final th = _things;
    return _bag.entries.fold(0, (a, e) => a + (th[e.key]?.price ?? 0) * e.value);
  }

  int get _fee => _fulfilment == 'delivery' ? (_shop?.zones.where((z) => z.name == _zone).firstOrNull?.feeKobo ?? 0) : 0;

  void _set(String id, int n) => setState(() => n <= 0 ? _bag.remove(id) : _bag[id] = n);

  Future<void> _place() async {
    final s = _shop!;
    if (_fulfilment == 'delivery') {
      if (s.zones.isNotEmpty && _zone == null) return setState(() => _error = 'Choose where to deliver to.');
      if (_addressId == null && _address.text.trim().isEmpty) return setState(() => _error = 'Write the address to deliver to.');
      if (_addressId == null && _saveIt && _saveAs.text.trim().isEmpty) return setState(() => _error = 'Give the address a name (Home, Work) to save it.');
    }
    final delivery = _fulfilment == 'delivery';
    final saved = delivery ? _addressId : null;
    final typed = delivery && saved == null;
    setState(() => (_busy = true, _error = null));
    final lines = [for (final e in _bag.entries) PlaceMyChatOrderRequestLinesItem(itemId: e.key, quantity: e.value)];
    final shape = '${_bag.entries.map((e) => '${e.key}:${e.value}').join(',')}|$_fulfilment|$_zone|$saved|${_address.text.trim()}|${_landmark.text.trim()}|${_note.text.trim()}';
    if (shape != _keyFor) {
      _key = 'order-${uuid4()}';
      _keyFor = shape;
    }
    try {
      final r = await d.api.placeMyChatOrder(
        id: d.id,
        idempotencyKey: _key!,
        body: PlaceMyChatOrderRequest(
          lines: lines,
          fulfilment: _fulfilment.isEmpty ? null : _fulfilment,
          zone: _fulfilment == 'delivery' ? _zone : null,
          address: typed ? _address.text.trim() : null,
          landmark: typed && _landmark.text.trim().isNotEmpty ? _landmark.text.trim() : null,
          addressId: saved,
          note: _note.text.trim().isEmpty ? null : _note.text.trim(),
        ),
      );
      unawaited(d.refresh());
      // Save this address: kept on the person's side once the order went.
      var words = 'Order ${r.order.reference} sent to ${d.name}';
      if (typed && _saveIt) {
        try {
          await d.api.addMyAddress(
            body: SavedAddressIn(label: _saveAs.text.trim(), line: _address.text.trim(), landmark: _landmark.text.trim().isEmpty ? null : _landmark.text.trim()),
          );
        } on Object catch (e) {
          words = '$words. The address wasn’t saved: ${sayFailure(e)}';
        }
      }
      if (!mounted) return;
      setState(() => _done = words);
      closeSoon();
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final s = _shop;
    final total = naira(_subtotal + _fee);
    final count = _count == 1 ? '1 item' : '$_count items';
    return TkAppLook(child: TkSheet(
      title: _checkout ? 'Check your order' : 'Their shop',
      crumb: _checkout ? 'Their shop' : null,
      onBack: _checkout ? () => setState(() => _checkout = false) : null,
      lead: _checkout ? null : ChatAvatar(name: d.name, size: 48, letterSize: 16),
      sub: _checkout ? null : '${d.name}’s shop, at their prices',
      overlay: _done == null ? null : TkSavedTick(words: _done!),
      bareFooter: !_checkout && _count > 0,
      footer: s == null || !s.open || _count == 0
          ? null
          : _checkout
          ? TkCta(label: _busy ? 'Placing…' : 'Place order · $total', onTap: _busy || _fulfilment.isEmpty ? null : _place)
          : BagBar(count: count, total: total, onGo: () => setState(() => _checkout = true)),
      body: s == null
          ? ListView(children: [if (_loadError != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 20), child: TkErrorBox(_loadError!)) else const TkLoading(label: 'Loading their shop…')])
          : _checkout
          ? _checkoutBody(t, s)
          : _list(t, s),
    ));
  }

  Widget _list(TkTokens t, MyChatShopResponse s) {
    if (s.items.isEmpty) return ListView(children: [TkEmpty(icon: 'b-box', title: '${d.name} has nothing in their shop yet', line: 'Ask them in the chat.')]);
    Widget stepper(_Thing x) {
      final n = _bag[x.id] ?? 0;
      if (!x.available) return const TkPill('Sold out', small: true);
      if (!s.open) return const SizedBox.shrink();
      return QtyStepper(
        name: x.name,
        n: n,
        on: n > 0,
        onLess: n > 0 ? () => _set(x.id, n - 1) : null,
        onMore: x.left == null || n < x.left! ? () => _set(x.id, n + 1) : null,
      );
    }

    String? left(int? n) => n == null ? null : 'Only $n left';
    final things = _things;
    final look = TkLook.of(context);
    Widget item(ShopItem i) {
      final sizes = i.sizes ?? const <ShopItemSizesItem>[];
      return Padding(
        padding: const EdgeInsets.fromLTRB(14, 12, 12, 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(i.name, style: tkText(size: 15.5, weight: 600, lineHeight: 1.3, color: t.text)),
                      Text(
                        [if (i.priceKobo != null) naira(i.priceKobo!), if (sizes.isEmpty) ?left(i.fewLeft)].join(' · '),
                        style: tkText(size: 13, color: sizes.isEmpty && i.fewLeft != null ? look.orangeInk : t.muted).copyWith(fontFeatures: tkTabular),
                      ),
                      if ((i.description ?? '').isNotEmpty) Text(i.description!, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted)),
                    ],
                  ),
                ),
                if (sizes.isEmpty) stepper(things[i.id]!),
              ],
            ),
            for (final z in sizes)
              Padding(
                padding: const EdgeInsets.only(left: 12, top: 8),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        [variantWords(z.variant), if (z.priceKobo != null && z.priceKobo != i.priceKobo) naira(z.priceKobo!), ?left(z.fewLeft)].join(' · '),
                        style: tkText(size: 14, color: z.fewLeft != null ? look.orangeInk : t.text),
                      ),
                    ),
                    stepper(things[z.id]!),
                  ],
                ),
              ),
          ],
        ),
      );
    }

    // As drawn: what they sell as rows on one card, on the sheet's ground.
    return ListView(
      padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
      children: [
        if (!s.open && (s.closed ?? '').isNotEmpty) Padding(padding: const EdgeInsets.fromLTRB(4, 0, 4, 12), child: Text(s.closed!, style: tkFormNow(t))),
        TkPanel(inset: 14, children: [for (final i in s.items) item(i)]),
      ],
    );
  }

  Widget _checkoutBody(TkTokens t, MyChatShopResponse s) {
    final th = _things;
    Widget choice(bool on, String words, VoidCallback onTap) => TkChoiceRow(control: TkRadio(checked: on), label: Text(words, style: tkText(size: 15, color: t.text)), onTap: onTap);
    return TkFormBody(
      children: [
        TkFormGroup(children: [
          TkLines([
            for (final e in _bag.entries) ('${e.value} × ${th[e.key]?.name ?? ''}', naira((th[e.key]?.price ?? 0) * e.value)),
            if (_fee > 0) ('Delivery', naira(_fee)),
          ]),
        ]),
        if (s.pickup || s.delivery)
          TkFormGroup(
            title: 'How you get it',
            children: [
              if (s.pickup) choice(_fulfilment == 'pickup', 'I’ll pick it up', () => setState(() => _fulfilment = 'pickup')),
              if (s.delivery) choice(_fulfilment == 'delivery', 'Deliver it to me', () => setState(() => _fulfilment = 'delivery')),
            ],
          )
        else
          Text('${d.name} will tell you here how you get it.', style: tkFormNow(t)),
        if (_fulfilment == 'delivery')
          TkFormGroup(
            title: 'Deliver to',
            children: [
              for (final z in s.zones) choice(_zone == z.name, '${z.name} · ${naira(z.feeKobo)}', () => setState(() => _zone = z.name)),
            ],
          ),
        if (_fulfilment == 'delivery')
          TkFormGroup(
            title: _saved.isEmpty ? null : 'Address',
            children: [
              // A saved address (§13), or one typed here.
              for (final a in _saved)
                TkChoiceRow(
                  control: TkRadio(checked: _addressId == a.id),
                  label: Text('${a.label} · ${addressLine(a)}', style: tkText(size: 15, color: t.text)),
                  onTap: () => setState(() => _addressId = a.id),
                ),
              if (_saved.isNotEmpty) choice(_addressId == null, 'Another address', () => setState(() => _addressId = null)),
              if (_addressId == null) ...[
                TkKitField(label: 'Address', child: TkTextBox(controller: _address, lines: 2, max: 300)),
                TkKitField(label: 'Landmark', hint: 'Optional: what the rider looks for', child: TkTextBox(controller: _landmark, max: 200)),
                TkChoiceRow(
                  radio: false,
                  control: TkCheckbox(checked: _saveIt),
                  label: Text('Save this address', style: tkText(size: 15, color: t.text)),
                  onTap: () => setState(() => _saveIt = !_saveIt),
                ),
                if (_saveIt) TkKitField(label: 'Name it', hint: 'Home, Work', child: TkTextBox(controller: _saveAs, max: 40)),
              ],
            ],
          ),
        TkFormGroup(children: [TkKitField(label: 'Note for them', hint: 'Optional', child: TkTextBox(controller: _note, lines: 2, max: 500))]),
        Text(payWords(s.payment, d.name), style: tkFormNow(t)),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

// --- Your orders with them, and Pay ---

Future<void> showMyOrdersSheet(BuildContext context, MyChatData d, {bool due = false}) => showTkSheet<void>(context, (_) => MyOrdersSheet(d: d, due: due));

/// Opens an order's pay link, in the app's browser tab.
Future<void> payOrder(BuildContext context, MyChatData d, String reference) async {
  try {
    final r = await d.api.payMyChatOrder(id: d.id, reference: reference);
    if (!context.mounted) return;
    await openOrSay(context, () => openInBrowser(Uri.parse(r.url)), 'The pay page didn’t open. Try again.');
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
  }
}

class MyOrdersSheet extends StatefulWidget {
  const MyOrdersSheet({super.key, required this.d, this.due = false});
  final MyChatData d;

  /// Only what is due now (Pay).
  final bool due;

  @override
  State<MyOrdersSheet> createState() => _MyOrdersSheetState();
}

class _MyOrdersSheetState extends State<MyOrdersSheet> {
  List<DoorOrder>? _orders;
  String? _error;

  @override
  void initState() {
    super.initState();
    widget.d.api.myChatOrders(id: widget.d.id).then((r) {
      if (mounted) setState(() => _orders = r.orders);
    }, onError: (Object e) {
      if (mounted) setState(() => _error = e is ApiError ? e.message : sayFailure(e));
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final name = widget.d.name;
    final orders = _orders?.where((o) => !widget.due || o.pay).toList();
    return TkAppLook(
      child: TkSheet(
        title: widget.due ? 'Pay $name' : 'Your orders with $name',
        count: orders?.length,
        body: orders == null
            ? ListView(children: [if (_error != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 20), child: TkErrorBox(_error!)) else const TkLoading(label: 'Loading your orders…')])
            : orders.isEmpty
            ? ListView(
                children: [
                  widget.due
                      ? TkEmpty(icon: 'money', title: 'Nothing to pay', line: 'When something is due on an order with $name, you pay it here.')
                      : TkEmpty(icon: 'orders', title: 'No orders with $name yet', line: 'Order from their shop or book with the button by the box.'),
                ],
              )
            : ListView(
                padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
                children: [
                  TkSection(
                    children: [
                      for (final o in orders)
                        TkListRow(
                          title: o.kind == 'booking' ? (o.serviceName ?? o.summary) : o.summary,
                          sub: [o.reference, if (o.startsAt != null) whenLabel(o.startsAt!.toIso8601String()), if (o.delivery != null) o.delivery!.line].join(' · '),
                          icon: o.kind == 'booking' ? 'b-calendar' : (o.delivery != null ? 'b-bike' : 'b-box'),
                          tone: o.pay ? CardTone.warn : null,
                          amount: naira(o.pay ? o.dueKobo : o.totalKobo) + (o.pay ? ' due' : ''),
                          status: o.delivery != null ? deliveryStatus(o.delivery!.status) : yourOrderStatus(o.status),
                          statusTone: o.delivery != null ? doorTone(o.delivery!.status) : doorTone(o.status),
                          trailing: o.pay ? TkSmallButton(label: 'Pay', primary: true, semantics: 'Pay ${naira(o.dueKobo)} for ${o.reference}', onPressed: () => payOrder(context, widget.d, o.reference)) : null,
                        ),
                    ],
                  ),
                  Padding(padding: const EdgeInsets.fromLTRB(4, 12, 4, 0), child: Text('Orders you placed here, in this chat.', style: tkHint(t))),
                ],
              ),
      ),
    );
  }
}
