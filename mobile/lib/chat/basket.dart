// Ordering together (docs/spec/people-chatting.md §10.3, §11 A and C;
// people-chatting-5b.md C with the founder's C1, C2 and C3), in the app's
// own look (docs/spec/mobile-app-look.md): "Order together" from the + of
// a people chat or a group starts a shared basket from one business; its
// card opens the basket's sheet: who wants what, your own items from the
// business's shop (through the basket door), the holder's controls
// (pick-up or delivery and the area, close, reopen, hand on, cancel,
// place with who pays), and once placed the order as it moves, each one's
// share, paying it and the "Your share" cards. Taskiem moves no money
// between people.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show TkStepper;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'my_chat.dart' show sayFailure;
import 'people_live.dart';
import 'words.dart';

// --- words (tested on their own) ---

/// The basket's state in a few words, and its tone.
(String, CardTone) basketStateWords(Basket b) => switch (b.state) {
  'open' => ('Open: add what you want', CardTone.lime),
  'closed' => ('Closed for placing', CardTone.warn),
  'placing' => ('Being placed', CardTone.warn),
  'cancelled' => ('Cancelled', CardTone.danger),
  _ => ((b.order?.line ?? '').isNotEmpty ? b.order!.line : 'Placed', CardTone.plain),
};

/// Who holds it, said to the person ("" once placed or cancelled).
String basketHolderWords(Basket b) {
  if (b.state == 'placed' || b.state == 'cancelled') return '';
  if (b.holderGone) return 'The one who held this basket is no longer here: anyone can take it over.';
  return b.youHold ? 'You hold this basket: you close it and place the order.' : '${b.holder} holds this basket: they close it and place the order.';
}

/// A member's share: "₦4,833.33: items ₦4,500 and delivery ₦333.33".
String basketShareWords(BasketShare s) => [
  '${naira(s.amountKobo)}: items ${naira(s.itemsKobo)}',
  if (s.deliveryKobo > 0) 'delivery ${naira(s.deliveryKobo)}',
].join(' and ');

/// Who pays, said as the placer chooses (C2).
String basketPayNote(bool each, String business) => each
    ? 'Each pays the business for their own items and an equal part of the delivery. You can pay what’s left for anyone who hasn’t.'
    : '$business takes no payment in the app, so you pay for everyone. You can then send each a “Your share” card.';

/// What the person may do now.
class BasketCan {
  BasketCan(Basket b)
    : addMine = b.state == 'open' && (b.shop?.open ?? false),
      holder = (b.state == 'open' || b.state == 'closed') && b.youHold,
      close = b.state == 'open' && b.youHold && b.people.isNotEmpty,
      reopen = b.state == 'closed' && b.youHold,
      place = b.state == 'closed' && b.youHold,
      takeOver = (b.state == 'open' || b.state == 'closed') && b.holderGone,
      payMine = _placed(b) && b.payment == 'each' && _mine(b) != null && !_mine(b)!.paid && _mine(b)!.amountKobo >= 100 && (b.order?.payableNow ?? false),
      payOrder = _placer(b) && b.payment == 'all' && (b.order?.payableNow ?? false),
      payFor = _placer(b) && b.payment == 'each' && (b.order?.payableNow ?? false)
          ? b.people.where((p) => !p.you && p.share != null && !p.share!.paid && p.share!.amountKobo >= 100).toList()
          : const [],
      shareCards = _placer(b) && b.payment == 'all' && b.people.any((p) => !p.you && p.share != null && !p.share!.cardSent && p.share!.amountKobo >= 100);

  final bool addMine, holder, close, reopen, place, takeOver, payMine, payOrder, shareCards;
  final List<BasketPerson> payFor;

  static bool _placed(Basket b) => b.state == 'placed';
  static bool _placer(Basket b) => _placed(b) && b.people.any((p) => p.you && p.personId == b.placedBy);
  static BasketShare? _mine(Basket b) {
    for (final p in b.people) {
      if (p.you) return p.share;
    }
    return null;
  }
}

/// A shop's item as a line can name it: the item, or one of its sizes.
typedef BasketPick = ({String id, String name, int? price, bool available, int? fewLeft});

/// Each orderable thing of a shop: a plain item, or each size and colour of
/// one with sizes ("Shirt (Red, 42)").
List<BasketPick> basketPicks(List<ShopItem> items) => [
  for (final it in items)
    if ((it.sizes ?? const []).isEmpty)
      (id: it.id, name: it.name, price: it.priceKobo, available: it.available, fewLeft: it.fewLeft)
    else
      for (final s in it.sizes!)
        (
          id: s.id,
          name: '${it.name} (${s.variant.values.whereType<String>().where((v) => v.isNotEmpty).join(', ')})',
          price: s.priceKobo ?? it.priceKobo,
          available: it.available && s.available,
          fewLeft: s.fewLeft,
        ),
];

// --- starting ---

/// "Order together from…": a business the person chats with; null when put
/// away.
Future<String?> pickTogetherBusiness(BuildContext context, Auth auth) => showTkSheet<String>(context, (_) => _TogetherPicker(auth: auth));

class _TogetherPicker extends StatefulWidget {
  const _TogetherPicker({required this.auth});
  final Auth auth;

  @override
  State<_TogetherPicker> createState() => _TogetherPickerState();
}

class _TogetherPickerState extends State<_TogetherPicker> {
  List<PersonChat>? _chats;
  String? _error;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listMyChats();
      if (mounted) setState(() => _chats = r.chats.where((c) => !c.blockedYou).toList());
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final chats = _chats;
    return TkAppLook(
      child: TkSheet(
        title: 'Order together from…',
        crumb: 'A shared basket: each adds their own',
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            if (_error != null) TkErrorBox(_error!),
            if (chats == null && _error == null) const TkLoading(label: 'Loading your businesses…'),
            if (chats != null && chats.isEmpty) const TkPanel(children: [TkEmpty(icon: 'b-store', title: 'No businesses yet', line: 'Businesses you chat with show here.')]),
            if (chats != null && chats.isNotEmpty)
              TkSection(
                inset: 56,
                children: [
                  for (final c in chats)
                    TkListRow(title: c.name, sub: 'Order together', icon: 'b-store', square: look.orange, onTap: () => Navigator.of(context).pop(c.business)),
                ],
              ),
          ],
        ),
      ),
    );
  }
}

/// Starts a basket in a group or a people chat and says how it went; the
/// card comes into the chat as a message.
Future<bool> startBasket(BuildContext context, Auth auth, {String? groupId, String? chatId}) async {
  final business = await pickTogetherBusiness(context, auth);
  if (business == null || !context.mounted) return false;
  try {
    await auth.api.startBasket(body: StartBasketRequest(businessId: business, groupId: groupId, chatId: chatId));
    if (context.mounted) showTkToast(context, 'Basket started: everyone can add what they want');
    return true;
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
    return false;
  }
}

// --- the basket's sheet ---

/// The basket [id]'s sheet.
Future<void> showBasket(BuildContext context, Auth auth, String id) => showTkSheet<void>(context, (_) => BasketSheet(auth: auth, id: id));

enum _Step { basket, shop, place, account }

class BasketSheet extends StatefulWidget {
  const BasketSheet({super.key, required this.auth, required this.id, this.every = const Duration(seconds: 8)});
  final Auth auth;
  final String id;

  /// How often it is read again while the live connection is not open.
  final Duration every;

  @override
  State<BasketSheet> createState() => _BasketSheetState();
}

class _BasketSheetState extends State<BasketSheet> {
  Basket? _b;
  String? _error;
  bool _busy = false;
  _Step _step = _Step.basket;
  StreamSubscription<Basket>? _live;
  Timer? _poll;

  TaskiemApi get _api => widget.auth.api;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
    final live = PeopleLive.of(widget.auth);
    _live = live?.baskets.listen((b) {
      if (b.id == widget.id && mounted) setState(() => _b = b);
    });
    _poll = Timer.periodic(widget.every, (_) {
      if (!(live?.connected ?? false)) unawaited(_load());
    });
  }

  @override
  void dispose() {
    unawaited(_live?.cancel());
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final b = await _api.getBasket(id: widget.id);
      if (mounted) setState(() => (_b = b, _error = null));
    } on Object catch (e) {
      if (mounted && _b == null) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _do(Future<Basket> Function() fn, [String? ok]) async {
    if (_busy) return;
    setState(() => (_busy = true, _error = null));
    try {
      final b = await fn();
      if (!mounted) return;
      setState(() => (_b = b, _step = _Step.basket));
      if (ok != null) showTkToast(context, ok);
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _pay([String? forId]) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      final r = await _api.payBasketShare(id: widget.id, for_: forId);
      if (mounted) await openOrSay(context, () => openInBrowser(Uri.parse(r.url)), 'The pay page didn’t open. Try again.');
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _shareCards() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      final r = await _api.sendBasketShareCards(id: widget.id, body: const SendBasketShareCardsRequest());
      if (!mounted) return;
      setState(() => _b = r.basket);
      showTkToast(context, r.sent.length == 1 ? 'Share card sent' : '${r.sent.length} share cards sent');
    } on ApiError catch (e) {
      // No saved account: asked for here.
      if (mounted) setState(() => e.status == 400 ? _step = _Step.account : _error = e.message);
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final b = _b;
    return TkAppLook(
      child: TkSheet(
        title: b == null ? 'Order together' : 'Order together from ${b.business}',
        onBack: _step == _Step.basket ? null : () => setState(() => _step = _Step.basket),
        body: b == null
            ? ListView(padding: const EdgeInsets.fromLTRB(14, 0, 14, 16), children: [if (_error != null) TkErrorBox(_error!) else const TkLoading(label: 'Loading the basket…')])
            : switch (_step) {
                _Step.basket => _view(context, b),
                _Step.shop => _MineStep(api: _api, basket: b, onDone: (nb) => setState(() => (_b = nb, _step = _Step.basket))),
                _Step.place => _PlaceStep(api: _api, basket: b, onDone: (nb) => setState(() => (_b = nb, _step = _Step.basket))),
                _Step.account => _AccountStep(api: _api, basket: b, onDone: (nb) => setState(() => (_b = nb, _step = _Step.basket))),
              },
      ),
    );
  }

  Widget _view(BuildContext context, Basket b) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final can = BasketCan(b);
    final (state, _) = basketStateWords(b);
    final o = b.order;
    final holder = basketHolderWords(b);
    final others = b.people.where((p) => !p.you).toList();
    final buttons = <Widget>[
      if (can.addMine)
        TkSmallButton(label: b.people.any((p) => p.you) ? 'Change my items' : 'Add my items', primary: true, onPressed: () => setState(() => _step = _Step.shop)),
      if (can.close) TkSmallButton(label: 'Close the basket', onPressed: _busy ? null : () => _do(() => _api.closeBasket(id: b.id), 'Basket closed')),
      if (can.reopen) TkSmallButton(label: 'Reopen', onPressed: _busy ? null : () => _do(() => _api.reopenBasket(id: b.id), 'Basket open again')),
      if (can.place) TkSmallButton(label: 'Place the order', primary: true, onPressed: () => setState(() => _step = _Step.place)),
      if (can.takeOver)
        TkSmallButton(
          label: 'Take it over',
          onPressed: _busy ? null : () => _do(() => _api.handBasket(id: b.id, body: HandBasketRequest(personId: b.people.firstWhere((p) => p.you).personId)), 'You hold the basket'),
        ),
      if (can.payMine) TkSmallButton(label: 'Pay my share', primary: true, onPressed: _busy ? null : _pay),
      if (can.payOrder) TkSmallButton(label: 'Pay for the order', primary: true, onPressed: _busy ? null : _pay),
      for (final p in can.payFor) TkSmallButton(label: 'Pay ${p.name}’s share', onPressed: _busy ? null : () => _pay(p.personId)),
      if (can.shareCards) TkSmallButton(label: 'Send each their share', primary: true, onPressed: _busy ? null : _shareCards),
    ];
    return ListView(
      padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
      children: [
        TkSummary(
          label: b.where == 'group' ? 'In this group' : 'Between the two of you',
          kobo: o?.totalKobo ?? b.totalKobo,
          tag: state,
          figures: [
            (label: 'Items', value: naira(b.itemsKobo), dot: look.lime),
            if (b.deliveryKobo > 0) (label: 'Delivery', value: naira(b.deliveryKobo), dot: look.teal),
            if (o != null) (label: 'Paid', value: naira(o.paidKobo), dot: look.blue),
          ],
          foot: holder.isEmpty ? null : holder,
        ),
        const SizedBox(height: 12),
        if (_error != null) TkErrorBox(_error!),
        if (b.problems.isNotEmpty && b.state != 'placed')
          TkCallout(leading: TkIcon('b-alert', size: 22, color: look.orange), color: look.orange, title: 'Before it can be placed', sub: b.problems.join('\n')),
        if (o != null)
          TkSection(
            title: 'The order',
            inset: 56,
            children: [
              TkListRow(title: 'Order ${o.reference}', sub: o.line, icon: 'b-box', square: look.blue, amount: naira(o.totalKobo)),
              TkListRow(
                title: switch (o.paymentState) {
                  'paid' => 'Paid',
                  'part_paid' => 'Part paid',
                  _ => 'Not paid yet',
                },
                sub: '${naira(o.paidKobo)} paid · ${naira(o.leftKobo)} left',
                icon: 'b-money',
                square: look.lime,
              ),
              if (o.delivery != null) TkListRow(title: 'Delivery', sub: o.delivery!.line, icon: 'b-bike', square: look.violet),
            ],
          ),
        if (b.people.isEmpty)
          TkPanel(children: [TkEmpty(icon: 'b-bag', title: 'Nothing in it yet', line: 'Add what you want from ${b.business}.')])
        else
          TkSection(
            title: 'Who wants what',
            inset: 56,
            children: [
              for (final p in b.people)
                TkListRow(
                  title: p.name,
                  letters: p.name,
                  subLines: 6,
                  sub: [
                    for (final l in p.lines)
                      '${l.quantity} × ${l.name} · ${naira(l.lineKobo)}${l.priceChanged ? ' (was ${naira(l.addedPriceKobo * l.quantity)})' : ''}${l.available ? '' : ' · not available now'}',
                    if (p.share != null) 'Share: ${basketShareWords(p.share!)}${p.share!.paid ? ' · paid' : ''}${p.share!.cardSent ? ' · share card sent' : ''}',
                  ].join('\n'),
                  amount: naira(p.share?.amountKobo ?? p.itemsKobo),
                ),
            ],
          ),
        if (buttons.isNotEmpty) ...[const SizedBox(height: 12), TkButtonRow(buttons.take(2).toList()), for (final more in buttons.skip(2)) ...[const SizedBox(height: 8), more]],
        if (can.holder) ...[
          const SizedBox(height: 16),
          _HolderControls(
            basket: b,
            others: others,
            busy: _busy,
            onWay: (f, z) => _do(() => _api.putBasket(id: b.id, body: PutBasketRequest(fulfilment: f, zone: z))),
            onHand: (id) => _do(() => _api.handBasket(id: b.id, body: HandBasketRequest(personId: id)), 'Handed on'),
            onCancel: () => _do(() => _api.cancelBasket(id: b.id), 'Basket cancelled'),
          ),
        ],
        const SizedBox(height: 14),
        Text(
          '${b.business} sees one order from the one who places it, with a note of whose items are whose. It never sees this ${b.where == 'group' ? 'group' : 'chat'}. Taskiem moves no money between you.',
          style: tkHint(t),
        ),
      ],
    );
  }
}

class _HolderControls extends StatelessWidget {
  const _HolderControls({required this.basket, required this.others, required this.busy, required this.onWay, required this.onHand, required this.onCancel});
  final Basket basket;
  final List<BasketPerson> others;
  final bool busy;
  final void Function(String fulfilment, String? zone) onWay;
  final ValueChanged<String> onHand;
  final VoidCallback onCancel;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final shop = basket.shop;
    final rows = <Widget>[
      if (shop?.pickup ?? false)
        TkListRow(
          title: 'Pick-up',
          sub: 'Someone collects it',
          icon: 'b-store',
          square: look.teal,
          status: basket.fulfilment == 'pickup' ? 'Chosen' : null,
          statusTone: CardTone.lime,
          onTap: busy ? null : () => onWay('pickup', null),
        ),
      if (shop?.delivery ?? false)
        for (final z in shop!.zones.isEmpty ? [null] : shop.zones)
          TkListRow(
            title: z == null ? 'Delivery' : 'Delivery to ${z.name}',
            sub: z == null ? 'To the address given when placing' : '${naira(z.feeKobo)}, shared equally',
            icon: 'b-bike',
            square: look.violet,
            status: basket.fulfilment == 'delivery' && (z == null || basket.zone == z.name) ? 'Chosen' : null,
            statusTone: CardTone.lime,
            onTap: busy ? null : () => onWay('delivery', z?.name),
          ),
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (rows.isNotEmpty) TkSection(title: 'Pick-up or delivery', inset: 56, children: rows),
        if (others.isNotEmpty) ...[
          const SizedBox(height: 12),
          TkSection(
            title: 'Hand the basket to',
            inset: 56,
            children: [for (final p in others) TkListRow(title: p.name, letters: p.name, sub: 'They close it and place the order', onTap: busy ? null : () => onHand(p.personId))],
          ),
        ],
        const SizedBox(height: 12),
        TkSmallButton(label: 'Cancel the basket', danger: true, onPressed: busy ? null : onCancel),
      ],
    );
  }
}

/// Your own items, from the shop through the basket door.
class _MineStep extends StatefulWidget {
  const _MineStep({required this.api, required this.basket, required this.onDone});
  final TaskiemApi api;
  final Basket basket;
  final ValueChanged<Basket> onDone;

  @override
  State<_MineStep> createState() => _MineStepState();
}

class _MineStepState extends State<_MineStep> {
  List<BasketPick>? _picks;
  final Map<String, int> _qty = {};
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    for (final p in widget.basket.people) {
      if (!p.you) continue;
      for (final l in p.lines) {
        _qty[l.itemId] = (_qty[l.itemId] ?? 0) + l.quantity;
      }
    }
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final r = await widget.api.basketShop(id: widget.basket.id);
      if (mounted) setState(() => _picks = basketPicks(r.items));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      final lines = [for (final e in _qty.entries) if (e.value > 0) PutMyBasketLinesRequestLinesItem(itemId: e.key, quantity: e.value)];
      final b = await widget.api.putMyBasketLines(id: widget.basket.id, body: PutMyBasketLinesRequest(lines: lines));
      if (!mounted) return;
      showTkToast(context, 'Your items are in the basket');
      widget.onDone(b);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final picks = _picks;
    final n = _qty.values.fold(0, (a, b) => a + b);
    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
            children: [
              if (_error != null) TkErrorBox(_error!),
              if (picks == null && _error == null) const TkLoading(label: 'Loading their shop…'),
              if (picks != null)
                TkSection(
                  title: '${widget.basket.business}’s items',
                  inset: 56,
                  children: [
                    for (final p in picks)
                      TkListRow(
                        title: p.name,
                        sub: [p.price == null ? 'Price on asking' : naira(p.price!), if (!p.available) 'Sold out', if (p.fewLeft != null) 'Only ${p.fewLeft} left'].join(' · '),
                        icon: 'b-bag',
                        square: look.orange,
                        trailing: p.available && p.price != null
                            ? TkStepper(label: 'How many ${p.name}', value: _qty[p.id] ?? 0, onChanged: (v) => setState(() => _qty[p.id] = v))
                            : null,
                      ),
                  ],
                ),
            ],
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 14),
          child: TkCta(label: n == 0 ? 'Take my items out' : 'Put my $n in the basket', onTap: _busy ? null : _save),
        ),
      ],
    );
  }
}

/// Placing: who pays, where it goes, a note.
class _PlaceStep extends StatefulWidget {
  const _PlaceStep({required this.api, required this.basket, required this.onDone});
  final TaskiemApi api;
  final Basket basket;
  final ValueChanged<Basket> onDone;

  @override
  State<_PlaceStep> createState() => _PlaceStepState();
}

class _PlaceStepState extends State<_PlaceStep> {
  String _payment = 'all';
  final _address = TextEditingController();
  final _landmark = TextEditingController();
  final _note = TextEditingController();
  String? _error;
  bool _busy = false;

  @override
  void dispose() {
    for (final c in [_address, _landmark, _note]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _place() async {
    final b = widget.basket;
    if (b.fulfilment == 'delivery' && _address.text.trim().isEmpty) return setState(() => _error = 'Write the address to deliver to.');
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.api.placeBasket(
        id: b.id,
        body: PlaceBasketRequest(payment: _payment, address: _address.text.trim(), landmark: _landmark.text.trim(), note: _note.text.trim(), totalKobo: b.totalKobo),
      );
      if (!mounted) return;
      showTkToast(context, 'Order placed');
      widget.onDone(r.basket);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = widget.basket;
    final each = b.shop?.eachOffered ?? false;
    final look = TkLook.of(context);
    Widget choice(String value, String label, String sub) => TkListRow(
      title: label,
      sub: sub,
      icon: value == 'all' ? 'b-money' : 'b-bag',
      square: value == 'all' ? look.lime : look.orange,
      status: _payment == value ? 'Chosen' : null,
      statusTone: CardTone.lime,
      semanticsLabel: '$label${_payment == value ? ', chosen' : ''}',
      onTap: () => setState(() => _payment = value),
    );
    return Column(
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              Text('You place this as your own order of ${naira(b.totalKobo)} with ${b.business}.', style: tkText(size: 15, lineHeight: 1.4, color: t.text)),
              TkSection(
                title: 'Who pays',
                inset: 56,
                children: [
                  choice('all', 'I’ll pay all', 'Then send each a “Your share” card'),
                  if (each) choice('each', 'Each pays their share', 'Their items and an equal part of the delivery'),
                ],
              ),
              Text(basketPayNote(each, b.business), style: tkHint(t)),
              if (b.fulfilment == 'delivery') ...[
                TkKitField(label: 'Address to deliver to', child: TkTextBox(controller: _address, max: 300)),
                TkKitField(label: 'Landmark', hint: 'Optional', child: TkTextBox(controller: _landmark, max: 300)),
              ],
              TkKitField(label: 'A note for the business', hint: 'Optional', child: TkTextBox(controller: _note, max: 500)),
              if (_error != null) TkErrorBox(_error!),
            ],
          ),
        ),
        Padding(padding: const EdgeInsets.fromLTRB(14, 0, 14, 14), child: TkCta(label: 'Place the order', onTap: _busy ? null : _place)),
      ],
    );
  }
}

/// The account the share cards ask to be paid to, when none is saved.
class _AccountStep extends StatefulWidget {
  const _AccountStep({required this.api, required this.basket, required this.onDone});
  final TaskiemApi api;
  final Basket basket;
  final ValueChanged<Basket> onDone;

  @override
  State<_AccountStep> createState() => _AccountStepState();
}

class _AccountStepState extends State<_AccountStep> {
  final _bank = TextEditingController();
  final _number = TextEditingController();
  final _name = TextEditingController();
  String? _error;
  bool _busy = false;

  @override
  void dispose() {
    for (final c in [_bank, _number, _name]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _send() async {
    final number = _number.text.replaceAll(RegExp(r'\s'), '');
    if (_bank.text.trim().isEmpty || !RegExp(r'^\d{10}$').hasMatch(number) || _name.text.trim().isEmpty) {
      return setState(() => _error = 'Write the bank, the 10-digit account number and the name on the account.');
    }
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.api.sendBasketShareCards(
        id: widget.basket.id,
        body: SendBasketShareCardsRequest(payTo: PayoutAccount(bank: _bank.text.trim(), accountNumber: number, accountName: _name.text.trim())),
      );
      if (!mounted) return;
      showTkToast(context, 'Share cards sent');
      widget.onDone(r.basket);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              Text('Where should they pay you back? Taskiem moves no money: they pay with their bank app.', style: tkText(size: 15, lineHeight: 1.4, color: t.text)),
              TkKitField(label: 'Bank', child: TkTextBox(controller: _bank, hint: 'GTBank')),
              TkKitField(label: 'Account number', child: TkTextBox(controller: _number, digits: 10)),
              TkKitField(label: 'Name on the account', child: TkTextBox(controller: _name)),
              if (_error != null) TkErrorBox(_error!),
            ],
          ),
        ),
        Padding(padding: const EdgeInsets.fromLTRB(14, 0, 14, 14), child: TkCta(label: 'Send each their share', onTap: _busy ? null : _send)),
      ],
    );
  }
}
