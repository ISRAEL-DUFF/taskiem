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
import '../kit/list.dart' show TkPill;
import '../kit/modal.dart' show openOrSay, showTkConfirm;
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'cards.dart' show CardProgress;
import 'composer.dart' show Pickers;
import 'customer.dart' show phoneWords;
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

/// The state line (`.tk-form-now`): who, and the state as a pill.
Widget _now(TkTokens t, String who, String word, CardTone tone) => Row(
  children: [
    Flexible(child: Text('$who ·', style: tkText(size: 14, color: t.muted))),
    const SizedBox(width: 6),
    TkPill(word, tone: tone),
  ],
);

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
    final status = _str(_o['status']);
    // Accept a new order only: the server accepts nothing else
    // (engine/links/orders.go DecideTx); a changed one waits for the buyer,
    // and may still be declined.
    final open = widget.canEdit && (status == 'received' || status == 'changed');
    final accept = status == 'received';
    final step = tradeStep[status];
    final note = _str(_o['note']);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              _now(
                t,
                _str(_o['buyer_name']),
                tradeStatusWords[status] ?? status,
                status == 'received' ? CardTone.lime : (status == 'declined' || status == 'cancelled' ? CardTone.danger : CardTone.plain),
              ),
              if (step != null) CardProgress(steps: orderSteps, step: step),
              TkLines([
                for (final l in _maps(_o['lines']))
                  ('${_int(l['quantity'])} × ${_str(l['item'])}', _int(l['price_kobo']) > 0 ? naira(_int(l['quantity']) * _int(l['price_kobo'])) : 'No price'),
                ('Total', naira(_int(_o['total_kobo']))),
              ]),
              if (note.isNotEmpty) Text('Their note: “$note”', style: tkText(size: 14, color: t.muted)),
              if (_waits != null) Semantics(liveRegion: true, child: Text(_waits!, style: tkText(size: 14, weight: 600, color: t.text))),
              if (_error != null) TkErrorBox(_error!),
            ],
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
    Widget link(String label, VoidCallback go) => TkLink(label: label, onTap: go);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              _now(
                t,
                _str(_o['buyer_name']),
                deliveryWords[status] ?? status,
                status == 'received'
                    ? CardTone.warn
                    : status == 'declined' || status == 'cancelled'
                    ? CardTone.danger
                    : status == 'delivered'
                    ? CardTone.lime
                    : CardTone.plain,
              ),
              if (step != null) CardProgress(steps: deliverySteps, step: step),
              TkLines([
                if (_str(d['pickup']).isNotEmpty) ('Pick up', _str(d['pickup'])),
                ('Deliver to', address.isNotEmpty ? '$address${area.isNotEmpty ? ' ($area)' : ''}' : (area.isNotEmpty ? area : 'The area is on the order')),
                if (_str(c['landmark']).isNotEmpty) ('Landmark', _str(c['landmark'])),
                if (_str(c['name']).isNotEmpty) ('Customer', [_str(c['name']), phone].where((x) => x.isNotEmpty).join(' · ')),
                if (d['door_payment'] == true) ('Payment', 'At the door'),
                ('Your fee', naira(_int(_o['total_kobo']))),
              ]),
              if (pickupLink.isNotEmpty || pin.isNotEmpty || phone.isNotEmpty)
                Wrap(
                  spacing: 16,
                  runSpacing: 4,
                  children: [
                    if (pickupLink.isNotEmpty)
                      link('Map of the pick-up', () => openOrSay(context, () => openOutside(Uri.parse(pickupLink)), 'The map could not be opened.')),
                    if (pin.isNotEmpty) link('Open the customer’s map', () => openOrSay(context, () => openOutside(Uri.parse(pin)), 'The map could not be opened.')),
                    if (phone.isNotEmpty) link('Call ${phoneWords(phone)}', () => openOrSay(context, () => callPhone(phone), 'That number could not be called.')),
                  ],
                ),
              if (proof)
                TkKitField(
                  field: false,
                  label: 'Photo of the delivery',
                  hint: 'The business asks for one.',
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: TkSmallButton(
                      label: _photo == null ? 'Take the photo' : 'Photo taken: take another',
                      onPressed: _busy || !widget.canEdit ? null : _takePhoto,
                    ),
                  ),
                ),
              if (_error != null) TkErrorBox(_error!),
            ],
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
