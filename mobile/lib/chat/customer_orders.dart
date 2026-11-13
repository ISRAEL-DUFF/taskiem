// Orders from customers' messages (web CustomerOrders.tsx,
// lib/customerorders.ts; docs/spec/chat-five-more-flows.md phase 2): the
// card above a customer's reply box. What Taskiem read from their message,
// with its questions; staff answer them, change it, ask the customer, and
// confirm it. Nothing goes to the customer until a person presses Ask them
// or Confirm.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkPill;
import '../kit/modal.dart';
import '../kit/polling.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'stock_page.dart' as stock show feeInput, feeKobo;
import '../kit/sheet.dart' show TkKitField;
import 'words.dart' show CardTone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

/// customerorders.ts workingOn: the order being worked on, one at a time.
Map<String, dynamic>? workingOn(List<Map<String, dynamic>> list) => list.where((d) => d['status'] == 'open' || d['status'] == 'asked').firstOrNull;

/// customerorders.ts lastConfirmed.
Map<String, dynamic>? lastConfirmed(List<Map<String, dynamic>> list) => list.reversed.where((d) => d['status'] == 'confirmed').firstOrNull;

/// customerorders.ts orderPill: where the order stands, in plain words.
({String word, CardTone tone}) orderPill(Map<String, dynamic> d) {
  final q = _int(d['questions']);
  switch (d['status']) {
    case 'asked':
      return (word: 'Waiting for them', tone: CardTone.plain);
    case 'dropped':
      return (word: 'Dropped', tone: CardTone.plain);
    case 'open':
      if (q > 0) return (word: q == 1 ? '1 question' : '$q questions', tone: CardTone.warn);
      return d['ready'] == true ? (word: 'Ready to confirm', tone: CardTone.lime) : (word: 'Needs you', tone: CardTone.warn);
  }
  return switch (d['order_status']) {
    'awaiting_payment' => (word: 'Waiting for payment', tone: CardTone.warn),
    'paid' => (word: 'Paid', tone: CardTone.lime),
    'dispatched' || 'on_the_way' => (word: 'On the way', tone: CardTone.lime),
    'arrived' => (word: 'Arrived', tone: CardTone.lime),
    'completed' => (word: 'Delivered', tone: CardTone.lime),
    'expired' => (word: 'Not paid in time', tone: CardTone.danger),
    'cancelled' || 'declined' => (word: 'Cancelled', tone: CardTone.danger),
    _ => (word: 'Confirmed', tone: CardTone.lime),
  };
}

/// customerorders.ts PAY_WORDS.
const payWords = {
  'transfer': 'They pay now, by bank transfer (we send them the page)',
  'on_delivery': 'They pay when it arrives',
  'later': 'I’ll arrange payment with them',
};

/// customerorders.ts payChoices: the page only when the business has one;
/// at the door only for a delivery.
List<String> payChoices(Map<String, dynamic> d, bool payPage) => [if (payPage) 'transfer', if (d['fulfilment'] == 'delivery') 'on_delivery', 'later'];

/// customerorders.ts payNow: as set, else the first choice.
String payNow(Map<String, dynamic> d, bool payPage) {
  final c = payChoices(d, payPage);
  return c.contains(d['pay']) ? d['pay'] as String : c.first;
}

/// customerorders.ts lineWords: "2 × Jollof rice plate", or their words.
String lineWords(Map<String, dynamic> l) {
  if (_str(l['item_id']).isEmpty) return '“${_str(l['said'])}”';
  return '${_int(l['quantity']) > 0 ? '${_int(l['quantity'])} × ' : ''}${_str(l['name'])}';
}

/// customerorders.ts lineKobo: what a line comes to, if it can be said.
int? lineKobo(Map<String, dynamic> l) {
  if (_str(l['item_id']).isEmpty || _int(l['quantity']) == 0 || l['no_price'] == true) return null;
  return _int(l['price_kobo']) * _int(l['quantity']);
}

/// customerorders.ts lineNote: why a line waits, or what is short.
String lineNote(Map<String, dynamic> l) {
  if (_str(l['item_id']).isEmpty && l['ask'] == null) return 'Not in your catalogue. Pick the item, or remove it.';
  if (l['no_price'] == true) return 'No price in your catalogue: give one.';
  if (l['out'] == true) return l['left'] != null && _int(l['left']) > 0 ? 'Only ${_int(l['left'])} left.' : 'Out of stock.';
  return '';
}

/// customerorders.ts orderSource: the card's kicker.
String orderSource(Map<String, dynamic> d) => d['by_reader'] == true ? 'Order from their message' : 'Order';

/// The orders of one customer's conversation, followed as the thread is.
class CustomerOrders extends StatefulWidget {
  const CustomerOrders({super.key, required this.auth, required this.chatId, required this.canSend, this.follow = true, this.maxHeight = double.infinity});
  final Auth auth;
  final String chatId;

  /// The customer can be written to (not stopped, not erased).
  final bool canSend;

  /// Read again every 4 seconds: a customer's answer lands on the card.
  final bool follow;

  /// The most it takes of the conversation (the rest is the thread's): its
  /// body gives way, then the whole card scrolls (see [_Frame]).
  final double maxHeight;

  @override
  State<CustomerOrders> createState() => _CustomerOrdersState();
}

class _CustomerOrdersState extends State<CustomerOrders> with Polling {
  Map<String, dynamic>? _data;
  String? _error;
  bool _busy = false;
  ({String id, String error})? _told;
  int _asked = 0;

  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _load();
    if (widget.follow) startPolling(const Duration(seconds: 4), _load);
  }

  Future<void> _load() async {
    final asked = ++_asked;
    try {
      final r = (await _t.send('GET', '/v1/customer-chats/${Uri.encodeComponent(widget.chatId)}/orders') as Map).cast<String, dynamic>();
      if (mounted && asked == _asked) setState(() => (_data = r, _error = null));
    } on Object catch (e) {
      if (mounted && asked == _asked && _data == null) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _run(Future<void> Function() f, [String done = '']) async {
    setState(() => (_busy = true, _error = null));
    String? failed;
    try {
      await f();
      if (mounted && done.isNotEmpty) showTkToast(context, done);
    } on Object catch (e) {
      failed = errorWords(e);
    }
    // Read again, then say what went wrong: the read does not wipe it.
    await _load();
    if (mounted) setState(() => (_busy = false, _error = failed));
  }

  @override
  Widget build(BuildContext context) {
    final data = _data;
    if (data == null) {
      return _error == null ? const SizedBox.shrink() : Padding(padding: const EdgeInsets.fromLTRB(12, 0, 12, 8), child: TkErrorBox(_error!));
    }
    final orders = _maps(data['orders']);
    final working = workingOn(orders);
    final confirmed = lastConfirmed(orders);
    return ConstrainedBox(
      constraints: BoxConstraints(maxHeight: widget.maxHeight),
      child: Padding(
      padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (_error != null) Padding(padding: const EdgeInsets.only(bottom: 8), child: TkErrorBox(_error!)),
          if (working != null)
            Flexible(child: _OrderCard(
              key: ValueKey(working['id']),
              auth: widget.auth,
              d: working,
              data: data,
              canSend: widget.canSend,
              onChanged: _load,
              onTold: (id, error) {
                if (mounted) setState(() => _told = error == null ? null : (id: id, error: error));
              },
            ))
          else ...[
            if (confirmed != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: _ConfirmedCard(
                  d: confirmed,
                  told: _told?.id == confirmed['id'] ? _told!.error : null,
                  busy: _busy,
                  onAgain: () => _run(() async {
                    await _t.send('POST', '/v1/order-drafts/${Uri.encodeComponent(_str(confirmed['id']))}/tell', body: const {});
                    if (mounted) setState(() => _told = null);
                  }, 'Sent to them'),
                ),
              ),
            if (widget.canSend)
              Align(
                alignment: Alignment.centerLeft,
                child: StartOrderButton(
                  onTap: _busy ? null : () => _run(() => _t.send('POST', '/v1/customer-chats/${Uri.encodeComponent(widget.chatId)}/orders', body: const {})),
                ),
              ),
          ],
        ],
      ),
      ),
    );
  }
}

/// `.tk-chatcard.customer-order`: the panel, its kicker and pill, the line.
class _Frame extends StatelessWidget {
  const _Frame({required this.kicker, required this.pill, required this.title, required this.children, this.fold, this.label, this.body});
  final String kicker;
  final ({String word, CardTone tone}) pill;
  final String title;

  /// Under the body: the card's buttons and why it waits.
  final List<Widget> children;
  final Widget? fold;
  final String? label;

  /// What gives way when there is little room (`.customer-order-body
  /// { overflow-y: auto }`): it scrolls inside the card, the head and the
  /// buttons staying.
  final Widget? body;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      label: label,
      explicitChildNodes: true,
      child: Container(
        padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
        decoration: BoxDecoration(
          color: t.panel,
          border: Border.all(color: t.border),
          borderRadius: BorderRadius.circular(16),
        ),
        child: LayoutBuilder(
          builder: (context, box) {
            final head = [
              Row(
                children: [
                  Expanded(
                    child: Text(kicker.toUpperCase(), style: tkText(size: 11, weight: 700, letterSpacing: 0.66, color: t.muted)),
                  ),
                  TkPill(pill.word, tone: pill.tone),
                  ?fold,
                ],
              ),
              const SizedBox(height: 4),
              Text(title, style: tkText(size: 15, weight: 700, color: t.text)),
            ];
            // Room for the head and buttons with some body: the body scrolls
            // inside. Less (the keyboard up, large text): the whole card
            // scrolls, so nothing is cut off and the box typed in stays seen.
            final roomy = box.maxHeight == double.infinity || box.maxHeight >= 200 * MediaQuery.textScalerOf(context).scale(1).clamp(1.0, 2.0);
            if (roomy) {
              return Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  ...head,
                  if (body != null) Flexible(child: SingleChildScrollView(child: body)),
                  ...children,
                ],
              );
            }
            return SingleChildScrollView(
              child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [...head, ?body, ...children]),
            );
          },
        ),
      ),
    );
  }
}

String _items(Map<String, dynamic> d) {
  final n = _maps(d['lines']).length;
  return '$n item${n == 1 ? '' : 's'} · ${naira(_int(d['total_kobo']))}';
}

/// ConfirmedCard: a confirmed order, where it stands, and a send that failed.
class _ConfirmedCard extends StatelessWidget {
  const _ConfirmedCard({required this.d, required this.told, required this.busy, required this.onAgain});
  final Map<String, dynamic> d;
  final String? told;
  final bool busy;
  final VoidCallback onAgain;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return _Frame(
      kicker: 'Order ${_str(d['reference'])}',
      label: 'Order ${_str(d['reference'])}',
      pill: orderPill(d),
      title: _items(d),
      children: [
        if (told != null) ...[
          const SizedBox(height: 8),
          Semantics(
            liveRegion: true,
            child: Text('Not sent to them: $told', style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
          const SizedBox(height: 8),
          Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(label: 'Send it again', onPressed: busy ? null : onAgain),
          ),
        ],
      ],
    );
  }
}

/// OrderCard: the order being worked on.
class _OrderCard extends StatefulWidget {
  const _OrderCard({super.key, required this.auth, required this.d, required this.data, required this.canSend, required this.onChanged, required this.onTold});
  final Auth auth;
  final Map<String, dynamic> d;
  final Map<String, dynamic> data;
  final bool canSend;
  final Future<void> Function() onChanged;
  final void Function(String id, String? error) onTold;

  @override
  State<_OrderCard> createState() => _OrderCardState();
}

class _OrderCardState extends State<_OrderCard> {
  bool _open = true;
  bool _busy = false;
  String? _error;
  // The choices show at once; the saved order follows.
  String? _pay;
  String? _getBy;
  final _address = TextEditingController();
  final _fee = TextEditingController();

  Map<String, dynamic> get _d => widget.d;
  String get _base => '/v1/order-drafts/${Uri.encodeComponent(_str(_d['id']))}';
  bool get _payPage => widget.data['pay_page'] == true;

  @override
  void initState() {
    super.initState();
    _fill();
    _address.addListener(() => setState(() {}));
    _fee.addListener(() => setState(() {}));
  }

  @override
  void didUpdateWidget(_OrderCard old) {
    super.didUpdateWidget(old);
    // What was saved since, each box on its own: an area picked (its fee
    // saved) leaves an address typed and not saved as it is (the web's
    // separate effects, CustomerOrders.tsx).
    if (old.d['address'] != _d['address']) _address.text = _str(_d['address']);
    if (old.d['delivery_kobo'] != _d['delivery_kobo']) _fee.text = _feeOf(_d);
    if (old.d['fulfilment'] != _d['fulfilment']) _getBy = null;
    if (old.d['pay'] != _d['pay'] || old.d['fulfilment'] != _d['fulfilment']) _pay = null;
  }

  void _fill() {
    _address.text = _str(_d['address']);
    _fee.text = _feeOf(_d);
  }

  static String _feeOf(Map<String, dynamic> d) => _int(d['delivery_kobo']) > 0 ? stock.feeInput(_int(d['delivery_kobo'])) : '';

  @override
  void dispose() {
    _address.dispose();
    _fee.dispose();
    super.dispose();
  }

  /// One change on the version shown; a change someone made since reloads.
  Future<void> _change(Map<String, Object?> body) async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('PUT', _base, body: {'version': _d['version'], ...body});
    } on Object catch (e) {
      // Refused (a change made since, say): the choices go back to what is
      // saved, not left showing what was not.
      if (mounted) setState(() => (_error = errorWords(e), _getBy = null, _pay = null));
    }
    await widget.onChanged();
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _step(String path, String done) async {
    setState(() => (_busy = true, _error = null));
    try {
      final out = (await widget.auth.transport.send('POST', '$_base/$path', body: {'version': _d['version']}) as Map?)?.cast<String, dynamic>() ?? const {};
      if (path == 'confirm') widget.onTold(_str(_d['id']), out['told'] == false ? _str(out['told_error']) : null);
      if (mounted) showTkToast(context, done);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    await widget.onChanged();
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final d = _d;
    final data = widget.data;
    final lines = _maps(d['lines']);
    final items = _maps(data['items']);
    final zones = _maps(data['zones']);
    final getBy = _getBy ?? _str(d['fulfilment']);
    // A way to pay this order no longer offers (at the door, then picked
    // up): the first it offers, as the web's payNow.
    final choices = payChoices({...d, 'fulfilment': getBy}, _payPage);
    final shown = _pay ?? payNow({...d, 'fulfilment': getBy}, _payPage);
    final pay = choices.contains(shown) ? shown : choices.first;
    final waits = d['ready'] == true ? '' : _str(d['waits']);
    // As typed: "1,500" and "₦1500" are amounts (orders.ts feeKobo); empty, none.
    final feeKobo = _fee.text.trim().isEmpty ? 0 : (stock.feeKobo(_fee.text) ?? -1);
    final dirty = _address.text != _str(d['address']) || feeKobo != _int(d['delivery_kobo']);
    final said = [_str(d['when']), _str(d['note'])].where((x) => x.isNotEmpty).join(' · ');
    final busy = _busy;
    return _Frame(
      kicker: orderSource(d),
      label: '${orderSource(d)}, ${orderPill(d).word}',
      pill: orderPill(d),
      title: _items(d),
      fold: TapArea(
        child: Semantics(
          button: true,
          expanded: _open,
          label: _open ? 'Fold the order' : 'Open the order',
          onTap: () => setState(() => _open = !_open),
          excludeSemantics: true,
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: () => setState(() => _open = !_open),
            child: Padding(
              padding: const EdgeInsets.only(left: 6),
              child: RotatedBox(
                quarterTurns: _open ? 2 : 0,
                child: TkIcon('chevron', size: 18, color: t.muted),
              ),
            ),
          ),
        ),
      ),
      body: !_open
          ? null
          : Padding(
              padding: const EdgeInsets.only(top: 10),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (_error != null) Padding(padding: const EdgeInsets.only(bottom: 8), child: TkErrorBox(_error!)),
                  for (var i = 0; i < lines.length; i++)
                    _LineRow(
                      key: ValueKey(lines[i]['id'] ?? '$i ${lines[i]['item_id'] ?? lines[i]['said']}'),
                      l: lines[i],
                      i: i,
                      items: items,
                      busy: busy,
                      change: _change,
                    ),
                  if (items.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 8),
                      child: Align(
                        alignment: Alignment.centerLeft,
                        child: TkSmallButton(
                          label: 'Add an item…',
                          semantics: 'Add an item',
                          onPressed: busy
                              ? null
                              : () async {
                                  final pick = await showTkModalChoices(
                                    context,
                                    title: 'Add an item',
                                    choices: [
                                      for (final it in items)
                                        (_str(it['id']), '${_str(it['name'])}${it['price_kobo'] != null ? ' · ${naira(_int(it['price_kobo']))}' : ''}'),
                                    ],
                                  );
                                  if (pick != null) await _change({'op': 'add', 'item_id': pick, 'quantity': 1});
                                },
                        ),
                      ),
                    ),
                  const SizedBox(height: 10),
                  _legend(t, 'How they get it'),
                  for (final (f, words) in const [('delivery', 'Delivered'), ('pickup', 'They pick it up')])
                    TkChoiceRow(
                      radio: true,
                      control: TkRadio(checked: getBy == f),
                      label: Text(words, style: tkText(size: 14, color: t.text)),
                      onTap: busy
                          ? null
                          : () {
                              setState(() => _getBy = f);
                              _change({'op': 'details', 'fulfilment': f});
                            },
                    ),
                  if (getBy == 'delivery') ...[
                    const SizedBox(height: 6),
                    TkKitField(label: 'Deliver to', child: TkTextBox(controller: _address, height: 44, size: 15, max: 300)),
                    if (zones.isNotEmpty) ...[
                      const SizedBox(height: 6),
                      _label(t, 'Delivery area'),
                      Align(
                        alignment: Alignment.centerLeft,
                        child: TkSmallButton(
                          label: _str(d['zone']).isEmpty ? 'Pick the area…' : _str(d['zone']),
                          semantics: 'Delivery area: ${_str(d['zone']).isEmpty ? 'none picked' : d['zone']}',
                          onPressed: busy
                              ? null
                              : () async {
                                  final pick = await showTkModalChoices(
                                    context,
                                    title: 'Delivery area',
                                    value: _str(d['zone']),
                                    choices: [for (final z in zones) (_str(z['name']), '${_str(z['name'])} · ${naira(_int(z['fee_kobo']))}')],
                                  );
                                  if (pick == null) return;
                                  final z = zones.where((x) => x['name'] == pick).firstOrNull;
                                  await _change({'op': 'details', 'zone': pick, 'delivery_kobo': _int(z?['fee_kobo'])});
                                },
                        ),
                      ),
                    ],
                    const SizedBox(height: 6),
                    TkKitField(
                      label: 'Delivery fee, ₦',
                      error: feeKobo < 0 ? 'Enter an amount, like 1500.' : null,
                      child: TkTextBox(controller: _fee, money: true, height: 44, size: 15),
                    ),
                    if (dirty)
                      Padding(
                        padding: const EdgeInsets.only(top: 6),
                        child: Align(
                          alignment: Alignment.centerLeft,
                          child: TkSmallButton(
                            label: 'Save',
                            semantics: 'Save the address and fee',
                            onPressed: busy || feeKobo < 0
                                ? null
                                : () => _change({'op': 'details', 'address': _address.text, 'delivery_kobo': feeKobo}),
                          ),
                        ),
                      ),
                  ],
                  const SizedBox(height: 10),
                  _legend(t, 'How they pay'),
                  for (final p in payChoices({...d, 'fulfilment': getBy}, _payPage))
                    TkChoiceRow(
                      radio: true,
                      control: TkRadio(checked: pay == p),
                      label: Text(payWords[p] ?? p, style: tkText(size: 14, color: t.text)),
                      onTap: busy
                          ? null
                          : () {
                              setState(() => _pay = p);
                              _change({'op': 'details', 'pay': p});
                            },
                    ),
                  if (said.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 6),
                      child: Text(said, style: tkText(size: 13, color: t.muted)),
                    ),
                  const SizedBox(height: 8),
                  _sum(t, 'Items', naira(_int(d['items_kobo']))),
                  if (getBy == 'delivery') _sum(t, 'Delivery', naira(_int(d['delivery_kobo']))),
                  _sum(t, 'Total', naira(_int(d['total_kobo'])), bold: true),
                ],
              ),
            ),
      children: [
        const SizedBox(height: 10),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            if (_int(d['questions']) > 0 && widget.canSend) TkSmallButton(label: 'Ask them', onPressed: busy ? null : () => _step('ask', 'Asked them')),
            TkSmallButton(
              label: 'Confirm',
              primary: true,
              semantics: waits.isEmpty ? null : 'Confirm. $waits',
              onPressed: busy || d['ready'] != true || !widget.canSend ? null : () => _step('confirm', 'Order confirmed'),
            ),
            TkSmallButton(label: 'Drop', onPressed: busy ? null : () => _step('drop', 'Order dropped')),
          ],
        ),
        if (waits.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Text(waits, style: tkText(size: 13, color: t.muted)),
          ),
      ],
    );
  }

  Widget _legend(TkTokens t, String s) => Padding(
    padding: const EdgeInsets.only(bottom: 4),
    child: Semantics(
      header: true,
      child: Text(s, style: tkText(size: 13, weight: 700, color: t.text)),
    ),
  );

  Widget _label(TkTokens t, String s) => Padding(
    padding: const EdgeInsets.only(bottom: 4),
    child: Text(s, style: tkText(size: 13, color: t.muted)),
  );

  Widget _sum(TkTokens t, String k, String v, {bool bold = false}) => Padding(
    padding: const EdgeInsets.only(top: 2),
    child: MergeSemantics(
      child: Row(
        children: [
          Expanded(
            child: Text(k, style: tkText(size: 14, color: t.text)),
          ),
          Text(
            v,
            style: tkText(size: 14, weight: bold ? 700 : 400, color: t.text),
          ),
        ],
      ),
    ),
  );
}

/// LineRow: a line, its price, what they wrote, why it waits, a question
/// to answer, the item to pick or price to give, and − + and remove.
class _LineRow extends StatefulWidget {
  const _LineRow({super.key, required this.l, required this.i, required this.items, required this.busy, required this.change});
  final Map<String, dynamic> l;
  final int i;
  final List<Map<String, dynamic>> items;
  final bool busy;
  final Future<void> Function(Map<String, Object?> body) change;

  @override
  State<_LineRow> createState() => _LineRowState();
}

class _LineRowState extends State<_LineRow> {
  final _howMany = TextEditingController();
  final _price = TextEditingController();

  @override
  void initState() {
    super.initState();
    _howMany.addListener(() => setState(() {}));
    _price.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _howMany.dispose();
    _price.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final l = widget.l;
    final i = widget.i;
    final busy = widget.busy;
    final kobo = lineKobo(l);
    final note = lineNote(l);
    final what = _str(l['name']).isNotEmpty ? _str(l['name']) : (_str(l['said']).isNotEmpty ? _str(l['said']) : 'this line');
    final ask = (l['ask'] as Map?)?.cast<String, dynamic>();
    final itemId = _str(l['item_id']);
    final qty = _int(l['quantity']);
    final n = int.tryParse(_howMany.text) ?? 0;
    final price = num.tryParse(_price.text.trim()) ?? 0;
    Widget round(String icon, String label, VoidCallback? go) => TapArea(
      child: Semantics(
        button: true,
        enabled: go != null,
        label: label,
        onTap: go,
        excludeSemantics: true,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: go,
          child: Container(
            width: 32,
            height: 32,
            alignment: Alignment.center,
            decoration: BoxDecoration(
              border: Border.all(color: t.borderStrong),
              borderRadius: BorderRadius.circular(8),
            ),
            child: TkIcon(icon, size: 14, color: go == null ? t.muted : t.text),
          ),
        ),
      ),
    );
    return Container(
      padding: const EdgeInsets.only(bottom: 8, top: 8),
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: t.border)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          MergeSemantics(
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: Text(lineWords(l), style: tkText(size: 14, weight: 600, color: t.text)),
                ),
                if (kobo != null) Text(naira(kobo), style: tkText(size: 14, color: t.text)),
              ],
            ),
          ),
          if (_str(l['said']).isNotEmpty && itemId.isNotEmpty) Text('They wrote “${l['said']}”', style: tkText(size: 13, color: t.muted)),
          if (note.isNotEmpty) Text(note, style: tkText(size: 13, color: t.tkWarnInk)),
          if (ask != null)
            Container(
              margin: const EdgeInsets.only(top: 4),
              padding: const EdgeInsets.all(8),
              decoration: BoxDecoration(color: t.tkWarnBg, borderRadius: BorderRadius.circular(8)),
              child: Semantics(
                container: true,
                label: _str(ask['text']),
                explicitChildNodes: true,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text(_str(ask['text']), style: tkText(size: 13, color: t.tkWarnInk)),
                    const SizedBox(height: 6),
                    if (ask['kind'] == 'how_many')
                      Row(
                        children: [
                          Expanded(
                            child: Semantics(
                              label: 'How many $what',
                              child: TkTextBox(controller: _howMany, digits: 6, height: 40, size: 15),
                            ),
                          ),
                          const SizedBox(width: 8),
                          TkSmallButton(
                            label: 'Set',
                            semantics: 'Set how many $what',
                            onPressed: busy || n <= 0 ? null : () => widget.change({'op': 'quantity', 'line': i, 'quantity': n}),
                          ),
                        ],
                      )
                    else
                      Wrap(
                        spacing: 8,
                        runSpacing: 8,
                        children: [
                          for (final c in _maps(ask['choices']))
                            TkSmallButton(
                              label: '${_str(c['name'])}${c['price_kobo'] != null ? ' · ${naira(_int(c['price_kobo']))}' : ''}',
                              onPressed: busy ? null : () => widget.change({'op': 'choose', 'line': i, 'item_id': c['item_id']}),
                            ),
                        ],
                      ),
                  ],
                ),
              ),
            ),
          if (itemId.isEmpty && ask == null)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(
                  label: 'Pick the item…',
                  semantics: 'The item for “${_str(l['said'])}”',
                  onPressed: busy
                      ? null
                      : () async {
                          final pick = await showTkModalChoices(
                            context,
                            title: 'The item for “${_str(l['said'])}”',
                            choices: [for (final it in widget.items) (_str(it['id']), _str(it['name']))],
                          );
                          if (pick != null) await widget.change({'op': 'choose', 'line': i, 'item_id': pick});
                        },
                ),
              ),
            ),
          if (itemId.isNotEmpty && l['no_price'] == true)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Row(
                children: [
                  Expanded(
                    child: Semantics(
                      label: 'Price of $what, in naira',
                      child: TkTextBox(controller: _price, money: true, height: 40, size: 15, hint: 'Price, ₦'),
                    ),
                  ),
                  const SizedBox(width: 8),
                  TkSmallButton(
                    label: 'Set',
                    semantics: 'Set the price of $what',
                    onPressed: busy || price <= 0 ? null : () => widget.change({'op': 'price', 'line': i, 'price_kobo': (price * 100).round()}),
                  ),
                ],
              ),
            ),
          const SizedBox(height: 6),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              if (itemId.isNotEmpty && qty > 0) ...[
                round('minus', 'One fewer $what', busy || qty <= 1 ? null : () => widget.change({'op': 'quantity', 'line': i, 'quantity': qty - 1})),
                const SizedBox(width: 8),
                round('plus', 'One more $what', busy ? null : () => widget.change({'op': 'quantity', 'line': i, 'quantity': qty + 1})),
                const SizedBox(width: 8),
              ],
              round('trash', 'Remove $what', busy ? null : () => widget.change({'op': 'remove', 'line': i})),
            ],
          ),
        ],
      ),
    );
  }
}

/// `.customer-order-start`: Start an order, a small button with its +.
class StartOrderButton extends StatelessWidget {
  const StartOrderButton({super.key, required this.onTap});
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(
      child: Semantics(
        button: true,
        enabled: onTap != null,
        label: 'Start an order',
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: Container(
            constraints: const BoxConstraints(minHeight: 40),
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
            decoration: BoxDecoration(
              color: t.panel,
              border: Border.all(color: t.borderStrong),
              borderRadius: BorderRadius.circular(8),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                TkIcon('plus', size: 14, color: t.text),
                const SizedBox(width: 6),
                Text('Start an order', style: tkText(size: 11.9, weight: 500, lineHeight: 1.2, color: t.text)),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
