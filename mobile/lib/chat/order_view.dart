// An order from a linked business in the Orders sheet (web ChatToolForms.tsx
// OrderView): what it asks for, where it is, Accept or Decline; and, for a
// rider's business, a delivery (DeliveryView, docs/spec/chat-riders.md):
// where to pick up and where it goes, who it is for, the fee, the next step,
// Delivered with a photo when the business asks for proof.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../app/links.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/modal.dart' show openOrSay, showTkConfirm;
import '../kit/sheet.dart';
import '../theme/theme.dart';
import '../kit/look.dart';
import '../kit/mobile.dart' show TkHeadButton;
import '../theme/look.dart';
import 'composer.dart' show Pickers;
import 'customer.dart' show phoneWords;
import 'storefront_orders_page.dart' show groupedPhone;
import 'attachments.dart' show PickedFile;
import 'words.dart';

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

/// ChatToolForms.tsx STATUS: an order's state in a word.
const tradeStatusWords = {
  'received': 'New',
  'changed': 'Changed',
  'proposed': 'Draft',
  'accepted': 'Accepted',
  'ready': 'Ready',
  'dispatched': 'On the way',
  'arrived': 'At the door',
  'delivered': 'Delivered',
  'declined': 'Declined',
  'cancelled': 'Cancelled',
};

/// Where an order is on Placed, Accepted, On the way, Delivered.
const tradeStep = {'received': 0, 'changed': 0, 'proposed': 0, 'accepted': 1, 'ready': 1, 'dispatched': 2, 'arrived': 2, 'delivered': 3};

/// Where a delivery is on Offered, Accepted, Picked up, Arrived, Delivered.
const deliveryStep = {'received': 0, 'accepted': 1, 'dispatched': 2, 'arrived': 3, 'delivered': 4};

const deliveryWords = {
  'received': 'Offered',
  'accepted': 'Accepted',
  'dispatched': 'Picked up',
  'arrived': 'At the door',
  'delivered': 'Delivered',
  'declined': 'Passed',
  'cancelled': 'Taken back',
};

/// toolforms.ts orderLine: "Accepted order AD-7Q4K2P from Rivers Foods".
String orderLine(String step, String reference, String buyer) => '${step == 'accept' ? 'Accepted' : 'Declined'} order $reference from $buyer';

/// ChatToolForms.tsx deliveryLine: the rider's diary line for a step.
String deliveryLine(String step, String reference, String business) => switch (step) {
  'accept' => 'Taking $reference for $business',
  'cant' => 'Passed on $reference for $business',
  'picked_up' => 'Picked up $reference from $business',
  'arrived' => 'At the door with $reference',
  _ => 'Delivered $reference for $business',
};

/// An order from a linked business, or a delivery for one.
class OrderView extends StatelessWidget {
  const OrderView({super.key, required this.auth, required this.order, required this.onSaved, this.canEdit = true, this.pickers = const Pickers()});
  final Auth auth;
  final Map<String, dynamic> order;
  final void Function(String? line) onSaved;
  final bool canEdit;
  final Pickers pickers;

  @override
  Widget build(BuildContext context) => order['delivery'] is Map
      ? DeliveryView(auth: auth, order: order, onSaved: onSaved, canEdit: canEdit, pickers: pickers)
      : _TradeOrderView(auth: auth, order: order, onSaved: onSaved, canEdit: canEdit);
}

/// `.tk-foot-two`: side by side; with large text one under another.
Widget _foot(BuildContext context, List<Widget> buttons) => TkSheetFoot(
  child: buttons.length > 1 && MediaQuery.textScalerOf(context).scale(10) > 15
      ? Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(height: 10), buttons[i]],
          ],
        )
      : Row(
          children: [
            for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(width: 10), Expanded(child: buttons[i])],
          ],
        ),
);

class _TradeOrderView extends StatefulWidget {
  const _TradeOrderView({required this.auth, required this.order, required this.onSaved, required this.canEdit});
  final Auth auth;
  final Map<String, dynamic> order;
  final void Function(String? line) onSaved;
  final bool canEdit;

  @override
  State<_TradeOrderView> createState() => _TradeOrderViewState();
}

class _TradeOrderViewState extends State<_TradeOrderView> {
  bool _busy = false;
  String? _error;

  /// The server's answer for staff under the lowest price: it waits for
  /// the owner's OK, and nothing was accepted.
  String? _waits;

  Map<String, dynamic> get _o => widget.order;
  String get _ref => _str(_o['order_reference']);

  Future<void> _act(String step) async {
    if (step == 'decline' && !await showTkConfirm(context, title: 'Decline $_ref?', ok: 'Decline', cancel: 'Keep it', danger: true)) return;
    if (!mounted) return;
    setState(() => (_busy = true, _error = null));
    try {
      final out = await widget.auth.transport.send('POST', '/v1/trade-orders/${Uri.encodeComponent(_str(_o['id']))}/$step', body: const {});
      // 202 waiting: under the lowest price, a cashier's accept waits for
      // the owner (api/link_orders.go); it is not accepted.
      if (out is Map && out['waiting'] == 'owner') {
        if (mounted) {
          setState(() {
            _busy = false;
            _waits = _str(out['why']).isNotEmpty ? _str(out['why']) : 'Under your lowest price: $_ref waits for the owner’s OK.';
          });
        }
        return;
      }
      widget.onSaved(orderLine(step, _ref, _str(_o['buyer_name'])));
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final status = _str(_o['status']);
    // Accept a new order only: the server accepts nothing else
    // (engine/links/orders.go DecideTx); a changed one waits for the buyer,
    // and may still be declined.
    final open = widget.canEdit && (status == 'received' || status == 'changed');
    final accept = status == 'received';
    final step = tradeStep[status];
    final note = _str(_o['note']);
    final ink = switch (status) {
      'received' => look.orangeInk,
      'accepted' || 'ready' => look.tealInk,
      'dispatched' || 'arrived' || 'changed' => look.blueInk,
      'proposed' => look.violetInk,
      'delivered' => look.limeInk,
      _ => t.muted,
    };
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          // The cards on the page's ground, as a phone's grouped sheet.
          child: ColoredBox(
            color: look.ground,
            child: TkFormBody(
              children: [
                TkTopCard(
                  semanticsLabel: 'The order',
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      TkEyebrow(status == 'received' ? 'New · answer them' : (tradeStatusWords[status] ?? status), color: ink, trailing: _ref),
                      const SizedBox(height: 6),
                      FittedBox(
                        fit: BoxFit.scaleDown,
                        alignment: Alignment.centerLeft,
                        child: Text(naira(_int(_o['total_kobo'])), style: tkFigure(context, size: 36)),
                      ),
                      Text('From ${_str(_o['buyer_name'])}', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
                      if (step != null) ...[
                        const SizedBox(height: 14),
                        TkSteps(steps: [for (final (i, s) in orderSteps.indexed) (label: s, when: '', done: i <= step)]),
                      ],
                    ],
                  ),
                ),
                TkBill(
                  lines: [
                    for (final l in _maps(_o['lines']))
                      (
                        name: '${_int(l['quantity'])} × ${_str(l['item'])}',
                        sub: '',
                        under: null,
                        amount: _int(l['price_kobo']) > 0 ? naira(_int(l['quantity']) * _int(l['price_kobo'])) : 'No price',
                      ),
                  ],
                  total: naira(_int(_o['total_kobo'])),
                ),
                if (note.isNotEmpty) Text('Their note: “$note”', style: tkText(size: 14, color: t.muted)),
                if (_waits != null)
                  Semantics(
                    liveRegion: true,
                    child: Text(_waits!, style: tkText(size: 14, weight: 600, color: t.text)),
                  ),
                if (_error != null) TkErrorBox(_error!),
              ],
            ),
          ),
        ),
        if (open)
          _foot(context, [
            TkGhostButton(label: 'Decline', onTap: _busy ? null : () => _act('decline')),
            if (accept) TkCta(label: 'Accept', onTap: _busy || _waits != null ? null : () => _act('accept')),
          ]),
      ],
    );
  }
}

/// A delivery for a linked business, as its rider sees it.
class DeliveryView extends StatefulWidget {
  const DeliveryView({super.key, required this.auth, required this.order, required this.onSaved, this.canEdit = true, this.pickers = const Pickers()});
  final Auth auth;
  final Map<String, dynamic> order;
  final void Function(String? line) onSaved;
  final bool canEdit;
  final Pickers pickers;

  @override
  State<DeliveryView> createState() => _DeliveryViewState();
}

class _DeliveryViewState extends State<DeliveryView> {
  bool _busy = false;
  String? _error;
  PickedFile? _photo;

  Map<String, dynamic> get _o => widget.order;
  Map<String, dynamic> get _d => (_o['delivery'] as Map).cast<String, dynamic>();
  String get _ref => _str(_o['order_reference']);

  Future<void> _act(String step) async {
    if (step == 'cant' &&
        !await showTkConfirm(context, title: 'Pass on $_ref?', body: 'It goes to another rider.', ok: 'Can’t take it', cancel: 'Keep it', danger: true)) {
      return;
    }
    if (!mounted) return;
    setState(() => (_busy = true, _error = null));
    final path = '/v1/trade-orders/${Uri.encodeComponent(_str(_o['id']))}/$step';
    try {
      final photo = _photo;
      if (step == 'delivered' && photo != null) {
        await widget.auth.transport.upload(path, bytes: photo.bytes, filename: photo.name.isEmpty ? 'door.jpg' : photo.name, type: photo.type, field: 'photo');
      } else {
        await widget.auth.transport.send('POST', path, body: const {});
      }
      widget.onSaved(deliveryLine(step, _ref, _str(_o['buyer_name'])));
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  Future<void> _takePhoto() async {
    try {
      final p = await widget.pickers.camera(side: 1600);
      if (p != null && mounted) setState(() => _photo = p);
    } on Object {
      if (mounted) setState(() => _error = 'Taskiem could not open your camera. Allow it in your phone’s settings, then try again.');
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final status = _str(_o['status']);
    final d = _d;
    final c = (d['customer'] as Map?)?.cast<String, dynamic>() ?? const {};
    final step = deliveryStep[status];
    final area = _str(d['drop_area']);
    final address = _str(c['address']);
    final next = switch (status) {
      'received' => ('accept', 'Accept'),
      'accepted' => ('picked_up', 'Picked up'),
      'dispatched' => ('arrived', 'Arrived'),
      'arrived' => ('delivered', 'Delivered'),
      _ => null,
    };
    final proof = d['proof_required'] == true && status == 'arrived';
    final needsPhoto = proof && _photo == null;
    final pickupLink = _str(d['pickup_link']);
    final pin = _str(c['pin']);
    final phone = _str(c['phone']);
    // What to do now, in the step's colour.
    final (words, ink) = switch (status) {
      'received' => ('A delivery for you', look.orangeInk),
      'accepted' => ('Go and pick it up', look.blueInk),
      'dispatched' => ('Take it to the door', look.blueInk),
      'arrived' => ('At the door', look.tealInk),
      'delivered' => ('Delivered', look.limeInk),
      _ => (deliveryWords[status] ?? status, t.muted),
    };
    Widget stop(Color dot, String label, String where, String? under, String? map, String mapSays) => Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(top: 4),
          child: Container(
            width: 14,
            height: 14,
            decoration: BoxDecoration(color: dot, shape: BoxShape.circle),
          ),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: MergeSemantics(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  label.toUpperCase(),
                  semanticsLabel: label,
                  style: tkText(size: 11, weight: 700, letterSpacing: 0.6, color: t.muted),
                ),
                Text(where, style: tkText(size: 14.5, weight: 600, lineHeight: 1.35, color: t.text)),
                if (under != null && under.isNotEmpty) Text(under, style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted)),
              ],
            ),
          ),
        ),
        if (map != null && map.isNotEmpty)
          TkHeadButton(
            icon: 'b-pin',
            label: mapSays,
            onCard: true,
            onTap: () => openOrSay(context, () => openOutside(Uri.parse(map)), 'The map could not be opened.'),
          ),
      ],
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          // The cards on the page's ground, as a phone's grouped sheet.
          child: ColoredBox(
            color: look.ground,
            child: TkFormBody(
              children: [
                TkTopCard(
                  semanticsLabel: 'The delivery',
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      TkEyebrow(words, color: ink, trailing: _str(_o['buyer_name'])),
                      const SizedBox(height: 6),
                      FittedBox(
                        fit: BoxFit.scaleDown,
                        alignment: Alignment.centerLeft,
                        child: Text(naira(_int(_o['total_kobo'])), style: tkFigure(context, size: 36)),
                      ),
                      Text(
                        d['door_payment'] == true ? 'Your fee · they pay at the door' : 'Your fee',
                        style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
                      ),
                      const SizedBox(height: 14),
                      if (_str(d['pickup']).isNotEmpty) ...[
                        stop(look.blue, 'Pick up', _str(d['pickup']), null, pickupLink, 'Map of the pick-up'),
                        const SizedBox(height: 10),
                      ],
                      stop(
                        look.lime,
                        'Deliver to',
                        address.isNotEmpty ? '$address${area.isNotEmpty ? ' ($area)' : ''}' : (area.isNotEmpty ? area : 'The area is on the order'),
                        _str(c['landmark']),
                        pin,
                        'Open the customer’s map',
                      ),
                      if (step != null) ...[
                        const SizedBox(height: 14),
                        TkSteps(steps: [for (final (i, s) in deliverySteps.indexed) (label: s, when: '', done: i <= step)]),
                      ],
                    ],
                  ),
                ),
                if (_str(c['name']).isNotEmpty || phone.isNotEmpty)
                  TkPersonCard(
                    name: _str(c['name']).isEmpty ? 'The customer' : _str(c['name']),
                    sub: phone.isEmpty ? '' : groupedPhone(phone),
                    actions: [
                      if (phone.isNotEmpty)
                        (
                          icon: 'b-phone',
                          label: 'Call',
                          semantics: 'Call ${phoneWords(phone)}',
                          onTap: () => openOrSay(context, () => callPhone(phone), 'That number could not be called.'),
                        ),
                    ],
                  ),
                // Said from the start; taken at the door.
                if (d['proof_required'] == true && !const {'delivered', 'declined', 'cancelled'}.contains(status))
                  Container(
                    padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
                    decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
                    child: Row(
                      children: [
                        TkSquare('b-camera', color: look.violet, size: 30),
                        const SizedBox(width: 12),
                        Expanded(
                          child: MergeSemantics(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(proof ? 'Photo of the delivery' : 'A photo when you deliver', style: tkText(size: 15, weight: 500, color: t.text)),
                                Text(_photo == null ? 'The business asks for one.' : 'Taken', style: tkText(size: 12.5, color: t.muted)),
                              ],
                            ),
                          ),
                        ),
                        if (proof)
                          TkSmallButton(
                          label: _photo == null ? 'Take the photo' : 'Take another',
                          semantics: _photo == null ? 'Take the photo' : 'Photo taken: take another',
                          primary: _photo == null,
                          onPressed: _busy || !widget.canEdit ? null : _takePhoto,
                        ),
                      ],
                    ),
                  ),
                if (_error != null) TkErrorBox(_error!),
              ],
            ),
          ),
        ),
        if (widget.canEdit && next != null)
          _foot(context, [
            if (status == 'received') TkGhostButton(label: 'Can’t', onTap: _busy ? null : () => _act('cant')),
            TkCta(label: next.$2, onTap: _busy || needsPhoto ? null : () => _act(next.$1)),
          ]),
      ],
    );
  }
}
