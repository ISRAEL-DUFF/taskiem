// Orders between linked businesses, the seller's side (web
// TradeOrders.tsx, lib/tradeorders.ts; business-links.md §7): every order
// linked businesses sent us, those needing us first; one order with its
// total, what it needs from us, its lines, which of our items each is, and
// the steps its status allows; how this business's orders are handled.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../app/openers.dart';
import '../kit/forms.dart' show TkSwitchRow, errorWords, nairaKobo, nairaText;
import '../kit/kit.dart';
import '../kit/list.dart' show TkPill, TkRow, TkEmpty;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField, TkLines;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'book_sheet.dart' show whenLabel;
import 'storefront_orders_page.dart' show openStorefrontOrder, openStorefrontOrders;
import 'tags.dart' show quantity;
import 'words.dart' show CardTone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

String tradePath(String id) => '/v1/trade-orders/${Uri.encodeComponent(id)}';

/// tradeorders.ts tradeStatus: an order's status in a word or two, with its
/// tone, as either side says it.
({String label, CardTone tone}) tradeStatus(String s) => switch (s) {
  'proposed' => (label: 'Waiting for them', tone: CardTone.plain),
  'received' => (label: 'New', tone: CardTone.warn),
  'changed' => (label: 'Change asked', tone: CardTone.plain),
  'accepted' => (label: 'Accepted', tone: CardTone.plain),
  'ready' => (label: 'Ready', tone: CardTone.plain),
  'dispatched' => (label: 'On its way', tone: CardTone.plain),
  'arrived' => (label: 'At the door', tone: CardTone.plain),
  'delivered' => (label: 'Delivered', tone: CardTone.lime),
  'declined' => (label: 'Declined', tone: CardTone.plain),
  _ => (label: 'Cancelled', tone: CardTone.plain),
};

typedef SellerSteps = ({bool accept, bool change, bool decline, bool ready, bool dispatched, bool delivered, bool cancel, bool keep, bool agree});

/// tradeorders.ts sellerSteps: the steps the seller may take on an order
/// in its status.
SellerSteps sellerSteps(Map<String, dynamic> o) {
  final s = _str(o['status']);
  final live = const {'accepted', 'ready', 'dispatched', 'arrived'}.contains(s);
  final asked = _str(o['cancel_asked_at']).isNotEmpty;
  return (
    accept: s == 'received',
    change: s == 'received' || s == 'changed',
    decline: s == 'received' || s == 'changed',
    ready: s == 'accepted',
    dispatched: s == 'accepted' || s == 'ready',
    delivered: live,
    cancel: live || s == 'proposed',
    keep: live && asked,
    agree: s == 'delivered' && _str(o['short_at']).isNotEmpty && _str(o['short_agreed_at']).isEmpty,
  );
}

bool _any(SellerSteps s) => s.accept || s.change || s.decline || s.ready || s.dispatched || s.delivered || s.cancel || s.keep || s.agree;

/// tradeorders.ts sellerAsk: what still needs the seller, in words; empty
/// when nothing does.
String sellerAsk(Map<String, dynamic> o) {
  final s = _str(o['status']);
  final buyer = _str(o['buyer_name']);
  if (s == 'received') return 'Accept it, change it, or decline it.';
  if (_str(o['cancel_asked_at']).isNotEmpty) return '$buyer asks to cancel it. Cancel it, or keep it.';
  if (s == 'delivered' && _str(o['short_at']).isNotEmpty && _str(o['short_agreed_at']).isEmpty) {
    return '$buyer says not all of it came. Agree their count, or talk to them.';
  }
  if (s == 'changed') return 'Waiting for $buyer to accept your change.';
  if (s == 'proposed') return 'You drafted it for $buyer: waiting for them to confirm it.';
  return '';
}

/// purchases.ts sizeParents: each size's product.
Map<String, String> sizeParents(List<Map<String, dynamic>> items) => {
  for (final i in items)
    if (_str(i['variant_of']).isNotEmpty) _str(i['id']): _str(i['variant_of']),
};

/// purchases.ts linesWords: "46 cartons Indomie, 17 bags Rice"; a product's
/// sizes together: "36 pairs Air Max 90 in 7 sizes and colours".
String linesWords(List<Map<String, dynamic>> lines, [Map<String, String> parents = const {}]) {
  final groups = <String, ({String name, String full, String? unit, num qty, int n, bool sized})>{};
  for (final l in lines) {
    final item = _str(l['item']);
    final product = parents[_str(l['item_id'])];
    final cut = item.indexOf(' – ');
    final sized = product != null;
    final key = sized ? '\u0000$product' : '\u0001$item';
    final g = groups[key];
    groups[key] = (
      name: g?.name ?? (sized && cut > 0 ? item.substring(0, cut) : item),
      full: g?.full ?? item,
      unit: g?.unit ?? l['unit'] as String?,
      qty: (g?.qty ?? 0) + ((l['quantity'] as num?) ?? 0),
      n: (g?.n ?? 0) + 1,
      sized: sized,
    );
  }
  return [
    for (final g in groups.values) !g.sized || g.n == 1 ? '${quantity(g.qty, g.unit)} ${g.full}' : '${quantity(g.qty, g.unit)} ${g.name} in ${g.n} sizes and colours',
  ].join(', ');
}

/// statements.ts daysValue: days to pay as typed; null when empty, -1 when
/// it is not 0 to 365.
int? daysValue(String text) {
  final t = text.trim();
  if (t.isEmpty) return null;
  final n = RegExp(r'^\d+$').hasMatch(t) ? int.parse(t) : -1;
  return n > 365 ? -1 : n;
}

int? _whole(String v) => RegExp(r'^\d+$').hasMatch(v.trim()) ? int.parse(v.trim()) : null;

/// The business's own items, when it keeps its stock on Taskiem; none
/// otherwise.
Future<List<Map<String, dynamic>>> _ownItems(Auth auth) async {
  try {
    final v = (await auth.transport.send('GET', '/v1/stock') as Map).cast<String, dynamic>();
    return v['keeps_stock'] == true ? _maps(v['items']) : const [];
  } on Object {
    return const [];
  }
}

void _push(BuildContext context, Widget page) => Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => page));

/// What an order leads to that the app has elsewhere: the storefront's
/// order (its delivery and riders), its invoice, the buyer's statement.
/// Null: it says it is coming.
class TradeLinks {
  const TradeLinks({this.storefrontOrder, this.invoice, this.statement});
  final void Function(BuildContext context, String reference)? storefrontOrder;
  final void Function(BuildContext context, String id)? invoice;
  final void Function(BuildContext context, String link)? statement;
}

/// TradeOrdersPage: every order linked businesses sent us, those needing
/// us first.
class TradeOrdersPage extends StatefulWidget {
  const TradeOrdersPage({super.key, required this.auth, this.links = const TradeLinks()});
  final Auth auth;
  final TradeLinks links;

  @override
  State<TradeOrdersPage> createState() => _TradeOrdersPageState();
}

class _TradeOrdersPageState extends State<TradeOrdersPage> {
  List<Map<String, dynamic>>? _orders;
  Map<String, String> _parents = const {};
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
    _ownItems(widget.auth).then((x) {
      if (mounted) setState(() => _parents = sizeParents(x));
    });
  }

  Future<void> _load() async {
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/trade-orders') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_orders = _maps(r['orders']), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _draft() async {
    final sent = await showTkModal<bool>(context, title: 'Draft an order for them', builder: (_) => _DraftForm(auth: widget.auth));
    if (sent == true && mounted) {
      showTkToast(context, 'Sent for them to confirm');
      await _load();
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final orders = _orders;
    return TkPageScreen(
      title: 'Orders from linked businesses',
      backLabel: 'Back',
      description: 'Purchase orders businesses you are linked with on Taskiem sent you. What you do here reaches their order at once.',
      onRefresh: _load,
      action: widget.auth.can('storefront.manage') ? TkSmallButton(label: 'Draft', semantics: 'Draft an order for them', primary: true, onPressed: _draft) : null,
      children: [
        // The web's crumb back to Orders, where every order is.
        Align(
          alignment: Alignment.centerLeft,
          child: TkLink(label: 'All your orders', onTap: () => openStorefrontOrders(context, widget.auth)),
        ),
        if (_error != null) TkErrorBox(_error!),
        if (orders == null && _error == null) Text('Loading the orders…', style: tkHint(t)),
        if (orders != null && orders.isEmpty)
          TkEmpty(
            icon: 'invoices',
            title: 'No orders yet',
            line: 'When a business that buys from you orders on Taskiem, it arrives here.',
            action: Openers.linkedBusinesses == null ? null : TkSmallButton(label: 'Linked businesses', onPressed: () => Openers.linkedBusinesses!(context)),
          )
        else if (orders != null)
          TkPageCard(
            children: [
              for (final o in orders)
                TkRow(
                  name: _str(o['buyer_name']),
                  icon: 'invoices',
                  sub: '${_str(o['reference'])} · ${linesWords(_maps(o['lines']), _parents)} · ${naira(_int(o['total_kobo']))}',
                  pill: TkPill(tradeStatus(_str(o['status'])).label, tone: tradeStatus(_str(o['status'])).tone),
                  onTap: () async {
                    await Navigator.of(context).push(
                      PageRouteBuilder<void>(pageBuilder: (_, _, _) => TradeOrderPage(auth: widget.auth, id: _str(o['id']), links: widget.links)),
                    );
                    await _load();
                  },
                ),
            ],
          ),
      ],
    );
  }
}

/// Opens one order from a linked business on its own screen.
void openTradeOrder(BuildContext context, Auth auth, String id, {TradeLinks links = const TradeLinks()}) =>
    _push(context, TradeOrderPage(auth: auth, id: id, links: links));

/// TradeOrderPage: one order from a linked business: its total first, what
/// it needs from us, its lines, and the steps its status allows.
class TradeOrderPage extends StatefulWidget {
  const TradeOrderPage({super.key, required this.auth, required this.id, this.links = const TradeLinks()});
  final Auth auth;
  final String id;
  final TradeLinks links;

  @override
  State<TradeOrderPage> createState() => _TradeOrderPageState();
}

class _TradeOrderPageState extends State<TradeOrderPage> {
  Map<String, dynamic>? _o;
  String? _error;
  List<Map<String, dynamic>> _own = const [];
  bool _busy = false;
  String? _stepError;

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
    _ownItems(_auth).then((x) {
      if (mounted) setState(() => _own = x);
    });
  }

  Future<void> _load() async {
    try {
      final r = (await _auth.transport.send('GET', tradePath(widget.id)) as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_o = (r['order'] as Map).cast<String, dynamic>(), _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  /// A step with no form: sent, told, read again.
  Future<void> _step(String step, String done) async {
    setState(() => (_busy = true, _stepError = null));
    try {
      await _auth.transport.send('POST', '${tradePath(widget.id)}/$step', body: const {});
      if (mounted) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _stepError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  /// A step with its form, in a modal; read again once it is sent.
  Future<void> _form(String title, Widget form, String done) async {
    final sent = await showTkModal<bool>(context, title: title, builder: (_) => form);
    if (sent == true && mounted) {
      showTkToast(context, done);
      await _load();
    }
  }

  void _soon() => showComingSoon(context);

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final o = _o;
    return TkPageScreen(
      title: o == null ? 'Order' : '${_str(o['reference'])} from ${_str(o['buyer_name'])}',
      backLabel: 'Back to Orders from linked businesses',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (o == null && _error == null) Text('Loading the order…', style: tkHint(t)),
        if (o != null) ..._order(t, o),
      ],
    );
  }

  List<Widget> _order(TkTokens t, Map<String, dynamic> o) {
    final buyer = _str(o['buyer_name']);
    final status = _str(o['status']);
    final ask = sellerAsk(o);
    final note = _str(o['note']);
    final change = _maps(o['change']);
    final manage = _auth.can('storefront.manage');
    final live = const {'accepted', 'ready', 'dispatched', 'delivered'}.contains(status);
    final delivery = _str(o['fulfilment']) == 'delivery';
    final invoiceId = _str(o['invoice_id']);
    final links = widget.links;
    return [
      TkHero(
        label: 'Order total',
        tag: tradeStatus(status).label,
        amount: naira(_int(o['total_kobo'])),
        sub:
            'From $buyer\nOrdered ${_str(o['created_at']).isEmpty ? '—' : whenLabel(_str(o['created_at']))} · Invoiced ${invoiceId.isNotEmpty ? 'yes' : (o['invoice_at'] == 'accepted' ? 'when accepted' : 'when received')}',
        semanticsLabel: 'The order',
      ),
      if (ask.isNotEmpty) _Notice(ask),
      if (_str(o['over_limit_at']).isNotEmpty && status == 'received')
        _Notice(
          'This order takes $buyer past the credit limit you give them. It was not accepted by itself; it waits for them to pay, and you can still accept it.',
          warn: true,
          link: (
            label: 'Their statement',
            onTap: links.statement == null ? _soon : () => links.statement!(context, _str(o['link'])),
          ),
        ),
      if (note.isNotEmpty) Text('Their note: “$note”', style: tkHint(t)),
      TkPageCard(
        title: 'What $buyer ordered',
        children: [_Lines(lines: _maps(o['lines']), short: (o['short'] as List?)?.whereType<num>().toList())],
      ),
      if (manage && status == 'received' && _own.isNotEmpty) _OwnItems(auth: _auth, order: o, own: _own, onDone: _load),
      if (change.isNotEmpty) TkPageCard(title: 'Your change', children: [_Lines(lines: change)]),
      if (live)
        Wrap(
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            TkLink(
              label: delivery ? 'Its delivery and riders' : 'See it on the Orders page',
              onTap: () => (links.storefrontOrder ?? (c, r) => openStorefrontOrder(c, _auth, r))(context, _str(o['order_reference'])),
            ),
            Text(delivery ? ': a rider takes it to where $buyer said.' : '.', style: tkText(size: 15, color: t.text)),
          ],
        ),
      if (invoiceId.isNotEmpty)
        Wrap(
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            TkLink(label: 'Its invoice', onTap: links.invoice == null ? _soon : () => links.invoice!(context, invoiceId)),
            Text(', which is $buyer’s bill on Taskiem.', style: tkText(size: 15, color: t.text)),
          ],
        ),
      if (manage && _any(sellerSteps(o))) _actions(t, o),
      if (_auth.can('link.manage')) _TradeSettings(auth: _auth, order: o, onDone: _load, onStatement: links.statement, onSoon: _soon),
    ];
  }

  /// SellerActions: the seller's steps, as the status allows.
  Widget _actions(TkTokens t, Map<String, dynamic> o) {
    final can = sellerSteps(o);
    final ref = _str(o['reference']);
    final buyer = _str(o['buyer_name']);
    final lines = _maps(o['lines']);
    final busy = _busy;
    return TkPageCard(
      title: 'Answer $buyer',
      children: [
        if (_stepError != null) Padding(padding: const EdgeInsets.only(bottom: 10), child: TkErrorBox(_stepError!)),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            if (can.accept) TkSmallButton(label: 'Accept', primary: true, onPressed: busy ? null : () => _step('accept', '$ref accepted')),
            if (can.change)
              TkSmallButton(
                label: 'Change',
                onPressed: busy ? null : () => _form('Change $ref', _ChangeForm(auth: _auth, order: o), 'Your change is sent'),
              ),
            if (can.ready) TkSmallButton(label: 'It is ready', onPressed: busy ? null : () => _step('ready', 'They are told it is ready')),
            if (can.dispatched) TkSmallButton(label: 'Sent on its way', onPressed: busy ? null : () => _step('dispatched', 'They are told it is on its way')),
            if (can.delivered)
              TkSmallButton(
                label: 'Delivered',
                primary: true,
                onPressed: busy ? null : () => _form('Delivered $ref', _DeliveredForm(auth: _auth, order: o, lines: lines), 'Delivered, and they are told'),
              ),
            if (can.keep) TkSmallButton(label: 'Keep the order', onPressed: busy ? null : () => _step('keep', 'Kept, and they are told')),
            if (can.agree) TkSmallButton(label: 'Agree their count', primary: true, onPressed: busy ? null : () => _step('agree', 'Agreed, and they are told')),
            if (can.cancel)
              TkSmallButton(
                label: 'Cancel the order',
                danger: true,
                onPressed: busy
                    ? null
                    : () async {
                        if (await showTkConfirm(context, title: 'Cancel $ref for $buyer?', ok: 'Cancel the order', cancel: 'Keep it', danger: true)) {
                          await _step('cancel', '$ref cancelled');
                        }
                      },
              ),
            if (can.decline)
              TkSmallButton(
                label: 'Decline',
                danger: true,
                onPressed: busy ? null : () => _form('Decline $ref', _DeclineForm(auth: _auth, order: o), '$ref declined'),
              ),
          ],
        ),
      ],
    );
  }
}

/// `.notice` (and `.notice.warn`): what needs us, said once.
class _Notice extends StatelessWidget {
  const _Notice(this.words, {this.warn = false, this.link});
  final String words;
  final bool warn;

  /// A link after the words (the web's "Their statement").
  final ({String label, VoidCallback onTap})? link;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      liveRegion: true,
      container: true,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        decoration: BoxDecoration(color: warn ? t.tintWarn : t.tkLimeSoft, borderRadius: BorderRadius.circular(14)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(words, style: tkText(size: 14, weight: 500, color: warn ? t.tintWarnText : t.text)),
            if (link != null) ...[const SizedBox(height: 6), TkLink(label: link!.label, onTap: link!.onTap)],
          ],
        ),
      ),
    );
  }
}

/// LinesTable, as rows on a phone: each item, how many at what each, and
/// what was proposed, delivered and counted; the total under.
class _Lines extends StatelessWidget {
  const _Lines({required this.lines, this.short});
  final List<Map<String, dynamic>> lines;
  final List<num>? short;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final total = lines.fold<int>(0, (a, l) => a + _int(l['quantity']) * _int(l['price_kobo']));
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < lines.length; i++)
          () {
            final l = lines[i];
            final price = _int(l['price_kobo']);
            final proposed = l['proposed_kobo'] as num?;
            final parts = [
              '${quantity(_int(l['quantity']), l['unit'] as String?)} at ${price > 0 ? naira(price) : 'no price yet'}',
              if (proposed != null && proposed.toInt() != price) 'they proposed ${naira(proposed.toInt())}',
              if (l['delivered'] != null) 'delivered ${_int(l['delivered'])}',
              if (short != null) 'they counted ${i < short!.length ? short![i].toInt() : 0}',
            ];
            return Padding(
              padding: const EdgeInsets.only(bottom: 10),
              child: MergeSemantics(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(_str(l['item']), style: tkText(size: 15, weight: 600, color: t.text)),
                    Text(parts.join(' · '), style: tkText(size: 13, color: t.muted)),
                  ],
                ),
              ),
            );
          }(),
        Container(height: 1, color: t.border),
        const SizedBox(height: 10),
        TkLines([('Total', naira(total))]),
      ],
    );
  }
}

/// OwnItems: which of our own items each line of a new order is. Our price
/// applies, and accepting lowers our stock.
class _OwnItems extends StatefulWidget {
  const _OwnItems({required this.auth, required this.order, required this.own, required this.onDone});
  final Auth auth;
  final Map<String, dynamic> order;
  final List<Map<String, dynamic>> own;
  final Future<void> Function() onDone;

  @override
  State<_OwnItems> createState() => _OwnItemsState();
}

class _OwnItemsState extends State<_OwnItems> {
  bool _busy = false;
  String? _error;

  Future<void> _set(int i, String id) async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('PUT', '${tradePath(_str(widget.order['id']))}/lines/$i/item', body: {'item_id': id});
      if (mounted) showTkToast(context, 'Remembered for their next orders');
      await widget.onDone();
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final lines = _maps(widget.order['lines']);
    return TkPageCard(
      title: 'Your items',
      children: [
        Text(
          'Which of your items each is. Your price applies, accepting takes it off your stock, and Taskiem remembers it for ${_str(widget.order['buyer_name'])}’s next orders.',
          style: tkHint(t),
        ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 10), child: TkErrorBox(_error!)),
        for (var i = 0; i < lines.length; i++)
          () {
            final l = lines[i];
            final id = _str(l['item_id']);
            final name = widget.own.where((x) => x['id'] == id).map((x) => _str(x['name'])).firstOrNull;
            return Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Wrap(
                spacing: 8,
                runSpacing: 6,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  Text('Their ${_str(l['item'])}', style: tkText(size: 14, weight: 600, color: t.text)),
                  TkSmallButton(
                    label: name ?? 'Not one of mine',
                    semantics: 'Their ${_str(l['item'])}: ${name ?? 'Not one of mine'}',
                    onPressed: _busy
                        ? null
                        : () async {
                            final pick = await showTkModalChoices(
                              context,
                              title: 'Their ${_str(l['item'])}',
                              value: id,
                              choices: [for (final it in widget.own) (_str(it['id']), _str(it['name']))],
                            );
                            if (pick != null && pick.isNotEmpty) await _set(i, pick);
                          },
                  ),
                  if (l['matched'] == 'name' && id.isNotEmpty)
                    TkSmallButton(label: 'Yes, that is it', semantics: 'Yes, that is it for ${_str(l['item'])}', onPressed: _busy ? null : () => _set(i, id)),
                ],
              ),
            );
          }(),
      ],
    );
  }
}

/// Sends a step with its body from a modal; pops true once it is sent.
mixin _Sends<T extends StatefulWidget> on State<T> {
  bool busy = false;
  String? error;
  Auth get auth;

  Future<void> send(String path, Object body) async {
    setState(() => (busy = true, error = null));
    try {
      await auth.transport.send('POST', path, body: body);
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (error = errorWords(e), busy = false));
    }
  }
}

/// Change: what we can supply, at our prices; 0 for what we cannot.
class _ChangeForm extends StatefulWidget {
  const _ChangeForm({required this.auth, required this.order});
  final Auth auth;
  final Map<String, dynamic> order;

  @override
  State<_ChangeForm> createState() => _ChangeFormState();
}

class _ChangeFormState extends State<_ChangeForm> with _Sends {
  @override
  Auth get auth => widget.auth;

  late final _lines = _maps(widget.order['lines']);
  late final _qty = [for (final l in _lines) TextEditingController(text: '${_int(l['quantity'])}')];
  late final _price = [for (final l in _lines) TextEditingController(text: nairaText(_int(l['price_kobo'])))];
  final _note = TextEditingController();

  @override
  void initState() {
    super.initState();
    for (final c in [..._qty, ..._price]) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final c in [..._qty, ..._price, _note]) {
      c.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final bad = _qty.any((c) => _whole(c.text) == null)
        ? 'How many is a whole number; 0 for what you cannot supply.'
        : _price.any((c) => c.text.trim().isNotEmpty && nairaKobo(c.text) == null)
        ? 'A price in naira, like 45,000.'
        : null;
    return TkModalForm(
      saveWords: busy ? 'Sending…' : 'Send the change',
      busy: busy,
      problem: bad,
      error: error,
      save: () => send('${tradePath(_str(widget.order['id']))}/change', {
        'lines': [
          for (var i = 0; i < _lines.length; i++)
            {
              'item': _lines[i]['item'],
              if (_lines[i]['unit'] != null) 'unit': _lines[i]['unit'],
              'quantity': _whole(_qty[i].text),
              'price_kobo': nairaKobo(_price[i].text) ?? 0,
            },
        ],
        'note': _note.text.trim(),
      }),
      children: [
        Text('What you can supply, at your prices. ${_str(widget.order['buyer_name'])} accepts it, or cancels. Put 0 for what you cannot supply.', style: tkHint(t)),
        for (var i = 0; i < _lines.length; i++)
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Semantics(header: true, child: Text(_str(_lines[i]['item']), style: tkText(size: 15, weight: 600, color: t.text))),
              const SizedBox(height: 8),
              Wrap(
                spacing: 10,
                runSpacing: 10,
                children: [
                  SizedBox(
                    width: 120,
                    child: TkKitField(label: 'How many', child: TkTextBox(controller: _qty[i], digits: 6, height: 44, size: 16)),
                  ),
                  SizedBox(
                    width: 160,
                    child: TkKitField(label: 'Price each (₦)', child: TkTextBox(controller: _price[i], money: true, height: 44, size: 16)),
                  ),
                ],
              ),
            ],
          ),
        TkKitField(label: 'A word for them (optional)', child: TkTextBox(controller: _note, max: 500)),
      ],
    );
  }
}

/// Decline: why, and they are told.
class _DeclineForm extends StatefulWidget {
  const _DeclineForm({required this.auth, required this.order});
  final Auth auth;
  final Map<String, dynamic> order;

  @override
  State<_DeclineForm> createState() => _DeclineFormState();
}

class _DeclineFormState extends State<_DeclineForm> with _Sends {
  @override
  Auth get auth => widget.auth;

  final _note = TextEditingController();

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => TkModalForm(
    saveWords: busy ? 'Declining…' : 'Decline the order',
    busy: busy,
    error: error,
    save: () => send('${tradePath(_str(widget.order['id']))}/decline', {'note': _note.text.trim()}),
    children: [
      TkKitField(label: 'Why (they are told)', child: TkTextBox(controller: _note, lines: 3, max: 500)),
    ],
  );
}

/// Delivered: what we delivered of each; the buyer says All came or Not all
/// came within a day, and after that our count is taken.
class _DeliveredForm extends StatefulWidget {
  const _DeliveredForm({required this.auth, required this.order, required this.lines});
  final Auth auth;
  final Map<String, dynamic> order;
  final List<Map<String, dynamic>> lines;

  @override
  State<_DeliveredForm> createState() => _DeliveredFormState();
}

class _DeliveredFormState extends State<_DeliveredForm> with _Sends {
  @override
  Auth get auth => widget.auth;

  late final _got = [for (final l in widget.lines) TextEditingController(text: '${_int(l['quantity'])}')..addListener(() => setState(() {}))];

  @override
  void dispose() {
    for (final c in _got) {
      c.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TkModalForm(
      saveWords: busy ? 'Recording…' : 'Record the delivery',
      busy: busy,
      error: error,
      problem: _got.any((c) => _whole(c.text) == null) ? 'How many is a whole number.' : null,
      save: () => send('${tradePath(_str(widget.order['id']))}/delivered', {
        'delivered': [for (final c in _got) _whole(c.text)],
      }),
      children: [
        Text(
          'What you delivered of each. ${_str(widget.order['buyer_name'])} says All came or Not all came within a day; after that, your count is taken.',
          style: tkHint(t),
        ),
        for (var i = 0; i < widget.lines.length; i++)
          TkKitField(
            label: '${_str(widget.lines[i]['item'])} (ordered ${quantity(_int(widget.lines[i]['quantity']), widget.lines[i]['unit'] as String?)})',
            child: TkTextBox(controller: _got[i], digits: 6),
          ),
      ],
    );
  }
}

/// TradeSettings: how this linked business's orders are handled: when
/// their invoice is raised, whether they are accepted by themselves, days
/// to pay and a credit limit.
class _TradeSettings extends StatefulWidget {
  const _TradeSettings({required this.auth, required this.order, required this.onDone, required this.onStatement, required this.onSoon});
  final Auth auth;
  final Map<String, dynamic> order;
  final Future<void> Function() onDone;
  final void Function(BuildContext context, String link)? onStatement;
  final VoidCallback onSoon;

  @override
  State<_TradeSettings> createState() => _TradeSettingsState();
}

class _TradeSettingsState extends State<_TradeSettings> {
  Map<String, dynamic>? _terms;
  final _days = TextEditingController();
  final _limit = TextEditingController();
  bool _busy = false;
  String? _error;

  String get _link => _str(widget.order['link']);

  @override
  void initState() {
    super.initState();
    _days.addListener(() => setState(() {}));
    _limit.addListener(() => setState(() {}));
    _loadTerms();
  }

  @override
  void dispose() {
    _days.dispose();
    _limit.dispose();
    super.dispose();
  }

  /// Reads the terms; the boxes are filled from them only when [fill]
  /// (the first read, and after Save terms): what is typed and not saved
  /// stays through another change (the web's `days ?? terms`).
  Future<void> _loadTerms({bool fill = true}) async {
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/links/$_link/trade') as Map).cast<String, dynamic>();
      if (!mounted) return;
      setState(() {
        _terms = r;
        if (fill) {
          _days.text = r['terms_days'] != null ? '${r['terms_days']}' : '';
          _limit.text = r['credit_limit_kobo'] != null ? nairaText(_int(r['credit_limit_kobo'])) : '';
        }
      });
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _save(Map<String, Object?> body) async {
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('PUT', '/v1/links/$_link/trade', body: body);
      if (mounted) showTkToast(context, 'Saved');
      await widget.onDone();
      await _loadTerms(fill: body.containsKey('terms_days'));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final o = widget.order;
    final days = daysValue(_days.text);
    final limit = _limit.text.trim().isEmpty ? null : (nairaKobo(_limit.text) ?? -1);
    final bad = days == -1 || (limit != null && limit <= 0);
    final at = o['invoice_at'] == 'accepted' ? 'accepted' : 'delivered';
    const ats = [('delivered', 'Once the delivery is received'), ('accepted', 'When I accept an order')];
    return TkPageCard(
      title: '${_str(o['buyer_name'])}’s orders',
      children: [
        Text('Each order’s invoice is raised by itself and becomes their bill on Taskiem.', style: tkHint(t)),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 10), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        TkKitField(
          field: false,
          label: 'Raise the invoice',
          child: Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(
              label: ats.firstWhere((x) => x.$1 == at).$2,
              semantics: 'Raise the invoice: ${ats.firstWhere((x) => x.$1 == at).$2}',
              onPressed: _busy
                  ? null
                  : () async {
                      final pick = await showTkModalChoices(context, title: 'Raise the invoice', value: at, choices: ats);
                      if (pick != null && pick != at) await _save({'invoice_at': pick});
                    },
            ),
          ),
        ),
        const SizedBox(height: 10),
        TkSwitchRow(
          label: 'Accept their orders by themselves when every item has a price',
          on: o['auto_accept'] == true,
          onChanged: _busy ? null : (v) => _save({'auto_accept': v}),
        ),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Days to pay',
          hint: days == -1 ? null : 'Each invoice to them is due this many days after it is raised. Empty: your usual.',
          error: days == -1 ? 'From 0 to 365 days.' : null,
          child: TkTextBox(controller: _days, digits: 3),
        ),
        const SizedBox(height: 10),
        TkKitField(
          label: 'Credit limit (₦)',
          hint: limit != null && limit <= 0 ? null : 'Past it, their new orders wait for payment or your own accept. Empty: no limit.',
          error: limit != null && limit <= 0 ? 'A limit in naira, like 500,000.' : null,
          child: TkTextBox(controller: _limit, money: true),
        ),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkSmallButton(
              label: 'Save terms',
              onPressed: _busy || bad || _terms == null ? null : () => _save({'terms_days': days, 'credit_limit_kobo': limit}),
            ),
            TkSmallButton(
              label: 'Their statement',
              onPressed: widget.onStatement == null ? widget.onSoon : () => widget.onStatement!(context, _link),
            ),
          ],
        ),
      ],
    );
  }
}

/// DraftOrder: an order drafted for a business that buys from us; they
/// confirm it, or cancel it.
class _DraftForm extends StatefulWidget {
  const _DraftForm({required this.auth});
  final Auth auth;

  @override
  State<_DraftForm> createState() => _DraftFormState();
}

class _DraftLine {
  _DraftLine(VoidCallback changed) {
    for (final c in [item, quantity, price]) {
      c.addListener(changed);
    }
  }
  String itemId = '';
  final item = TextEditingController();
  final quantity = TextEditingController(text: '1');
  final price = TextEditingController();
  void dispose() {
    item.dispose();
    quantity.dispose();
    price.dispose();
  }
}

class _DraftFormState extends State<_DraftForm> with _Sends {
  @override
  Auth get auth => widget.auth;

  List<Map<String, dynamic>>? _buyers;
  List<Map<String, dynamic>> _own = const [];
  String _link = '';
  late final List<_DraftLine> _lines = [_DraftLine(_changed)];
  final _note = TextEditingController();

  void _changed() => setState(() {});

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/links') as Map).cast<String, dynamic>();
      final b = _maps(r['links']).where((l) => l['state'] == 'active' && (l['role'] == 'customer' || l['role'] == 'both')).toList();
      if (mounted) setState(() => (_buyers = b, _link = b.isEmpty ? '' : _str(b.first['id'])));
    } on Object catch (e) {
      if (mounted) setState(() => (_buyers = const [], error = errorWords(e)));
    }
    final own = await _ownItems(widget.auth);
    if (mounted) setState(() => _own = own);
  }

  @override
  void dispose() {
    for (final l in _lines) {
      l.dispose();
    }
    _note.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final buyers = _buyers;
    if (buyers == null) return Text('Loading the businesses you sell to…', style: tkHint(t));
    if (buyers.isEmpty) {
      return Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (error != null) TkErrorBox(error!) else Text('You are not linked with a business that buys from you yet.', style: tkHint(t)),
          const SizedBox(height: 12),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              if (error == null && Openers.findBusiness != null)
                TkButton(
                  label: 'Find them',
                  primary: true,
                  onPressed: () {
                    // Explore over the page, once the draft is closed.
                    final nav = Navigator.of(context);
                    nav.pop(false);
                    Openers.findBusiness!(nav.context);
                  },
                ),
              TkButton(label: 'Close', onPressed: () => Navigator.of(context).pop(false)),
            ],
          ),
        ],
      );
    }
    final bad = _lines.any((l) => _whole(l.quantity.text) == null)
        ? 'How many is a whole number.'
        : _lines.any((l) => l.itemId.isEmpty && l.item.text.trim().isEmpty)
        ? 'Say what each item is.'
        : _lines.any((l) => l.itemId.isEmpty && l.price.text.trim().isNotEmpty && nairaKobo(l.price.text) == null)
        ? 'A price in naira, like 45,000.'
        : null;
    final name = buyers.where((b) => b['id'] == _link).map((b) => _str(b['name'])).firstOrNull ?? '';
    return TkModalForm(
      saveWords: busy ? 'Sending…' : 'Send for them to confirm',
      busy: busy,
      problem: bad,
      error: error,
      save: () => send('/v1/trade-orders', {
        'link': _link,
        'lines': [
          for (final l in _lines)
            l.itemId.isNotEmpty
                ? {'item_id': l.itemId, 'quantity': _whole(l.quantity.text)}
                : {'item': l.item.text.trim(), 'quantity': _whole(l.quantity.text), 'price_kobo': nairaKobo(l.price.text) ?? 0},
        ],
        'note': _note.text.trim(),
      }),
      children: [
        Text('They confirm it, or cancel it, in their Taskiem; until then it goes no further.', style: tkHint(t)),
        TkKitField(
          field: false,
          label: 'Which business',
          child: Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(
              label: name,
              semantics: 'Which business: $name',
              onPressed: () async {
                final pick = await showTkModalChoices(context, title: 'Which business', value: _link, choices: [for (final b in buyers) (_str(b['id']), _str(b['name']))]);
                if (pick != null) setState(() => _link = pick);
              },
            ),
          ),
        ),
        for (var i = 0; i < _lines.length; i++)
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Semantics(header: true, child: Text('Item ${i + 1}', style: tkText(size: 15, weight: 600, color: t.text))),
              const SizedBox(height: 8),
              if (_own.isNotEmpty) ...[
                TkKitField(
                  field: false,
                  label: 'Your item',
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: TkSmallButton(
                      label: _own.where((x) => x['id'] == _lines[i].itemId).map((x) => _str(x['name'])).firstOrNull ?? 'Another, named here',
                      semantics: 'Item ${i + 1}, your item',
                      onPressed: () async {
                        final pick = await showTkModalChoices(
                          context,
                          title: 'Your item',
                          value: _lines[i].itemId,
                          choices: [('', 'Another, named here'), for (final it in _own) (_str(it['id']), _str(it['name']))],
                        );
                        if (pick != null) setState(() => _lines[i].itemId = pick);
                      },
                    ),
                  ),
                ),
                const SizedBox(height: 10),
              ],
              if (_lines[i].itemId.isEmpty) ...[
                TkKitField(label: 'What it is', child: TkTextBox(controller: _lines[i].item, max: 120)),
                const SizedBox(height: 10),
                TkKitField(label: 'Price each (₦)', child: TkTextBox(controller: _lines[i].price, money: true)),
                const SizedBox(height: 10),
              ],
              TkKitField(label: 'How many', child: TkTextBox(controller: _lines[i].quantity, digits: 6)),
            ],
          ),
        Align(
          alignment: Alignment.centerLeft,
          child: TkSmallButton(label: 'Add an item', onPressed: _lines.length >= 50 ? null : () => setState(() => _lines.add(_DraftLine(_changed)))),
        ),
        TkKitField(label: 'A word for them (optional)', child: TkTextBox(controller: _note, max: 500)),
      ],
    );
  }
}
