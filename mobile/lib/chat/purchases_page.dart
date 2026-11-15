// Purchases and suppliers (web TradeOrders.tsx PurchasePage,
// ConfirmProposed, BuyerActions; StockPurchases.tsx PurchasesCard;
// StorefrontSuppliers.tsx; lib/purchases.ts, lib/tradeorders.ts): an
// order to a supplier on its own screen (the buyer's side: confirm their
// draft, accept their change, cancel, all came, not all came); the Stock
// screen's orders to suppliers (order what is low, send, confirmed,
// goods in, a payment); the suppliers (added, renamed, linked, retired,
// their prices, what each is owed), who may send a large order, and where
// linked suppliers deliver.

import 'package:flutter/widgets.dart';

import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../app/openers.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkRow;
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField, TkLines;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'bills_page.dart' show openBills;
import 'book_sheet.dart' show whenLabel;
import 'doc_thread.dart' show DocThread;
import 'product_form.dart' show formKey;
import 'purchase_view.dart' show answersDelivery, buyerSteps, sameName;
import 'statement_page.dart' show openStatement;
import 'stock_page.dart' show StockPage, feeInput, feeKobo;
import 'tags.dart' show quantity;
import 'trade_orders_page.dart' show linesWords;
import 'words.dart' show CardTone, naira;
import '../kit/look.dart';
import '../kit/mobile.dart' show TkCountUp, TkHeadButton, TkListRow, TkPanel, TkRoute, TkSection, TkSkeleton, TkChips, tkInitials;
import '../theme/look.dart';
import 'invoices_page.dart' show shortDay;
import 'orders_tab.dart' show purchaseWaiting;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;
String _str(Object? v) => v as String? ?? '';

String _purchasePath(String id, [String rest = '']) => '/v1/stock/purchases/${Uri.encodeComponent(id)}$rest';

Future<void> _push(BuildContext context, Widget page) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => page));

/// Opens an order to a supplier.
Future<void> openPurchase(BuildContext context, Auth auth, String id, {String backLabel = 'Back'}) =>
    _push(context, PurchasePage(auth: auth, id: id, backLabel: backLabel));

/// Opens Suppliers.
void openSuppliers(BuildContext context, Auth auth) => _push(context, SuppliersPage(auth: auth));

/// purchases.ts STATUS.
({String label, CardTone tone}) purchaseStatus(String s) => switch (s) {
  'drafted' => (label: 'Draft', tone: CardTone.plain),
  'sent' => (label: 'Sent', tone: CardTone.plain),
  'confirmed' => (label: 'Confirmed', tone: CardTone.plain),
  'part_received' => (label: 'Part received', tone: CardTone.warn),
  'received' => (label: 'Received', tone: CardTone.lime),
  'cancelled' => (label: 'Cancelled', tone: CardTone.plain),
  _ => (label: s, tone: CardTone.plain),
};

/// tradeorders.ts sellerLine: the seller's side, in words.
String sellerLine(Map<String, dynamic> p) {
  if (_str(p['link_id']).isEmpty) return '';
  final s = _str(p['supplier']);
  final cancel = _str(p['cancel_asked_at']).isNotEmpty;
  return switch (_str(p['seller_status'])) {
    '' => 'On its way to $s on Taskiem.',
    'proposed' => '$s drafted this order for you. Confirm it, saying which of your items each line is, or cancel it.',
    'received' => cancel ? '' : '$s has it, and has not answered yet.',
    'changed' => '$s asks to change it.',
    'accepted' => cancel ? 'You asked $s to cancel it.' : '$s accepted it.',
    'ready' => '$s has it ready.',
    'dispatched' => '$s sent it on its way.',
    'delivered' => '$s delivered it.',
    'declined' => '$s declined it.',
    _ => '$s cancelled it.',
  };
}

/// purchases.ts linkedOrder: a linked supplier handles it.
bool linkedOrder(Map<String, dynamic> p) => _str(p['link_id']).isNotEmpty && _str(p['seller_status']).isNotEmpty;

/// purchases.ts purchaseActions.
({bool send, bool confirm, bool receive, bool answerDelivery, bool pay, bool cancel}) purchaseActions(Map<String, dynamic> p) {
  final s = _str(p['status']);
  final linked = _str(p['link_id']).isNotEmpty;
  return (
    send: s == 'drafted' && p['seller_status'] != 'proposed',
    confirm: s == 'sent' && !linked,
    receive: (s == 'sent' || s == 'confirmed' || s == 'part_received') && !linkedOrder(p),
    answerDelivery: answersDelivery(p),
    pay: s != 'drafted' && s != 'cancelled',
    cancel: (s == 'drafted' || s == 'sent' || s == 'confirmed') && !(linked && (_str(p['cancel_asked_at']).isNotEmpty || p['seller_status'] == 'delivered')),
  );
}

/// purchases.ts purchaseSub: its items, when, what the supplier said, owed.
String purchaseSub(Map<String, dynamic> p, [Map<String, String> parents = const {}]) {
  final lines = _maps(p['lines']);
  final when = [p['received_at'], p['confirmed_at'], p['sent_at'], p['created_at']].whereType<String>().where((x) => x.isNotEmpty).firstOrNull ?? '';
  final s = _str(p['status']);
  return [
    linesWords(lines, parents),
    if (when.isNotEmpty) whenLabel(when),
    if (s == 'confirmed' || _str(p['confirmed_how']).isNotEmpty) p['confirmed_how'] == 'supplier' ? 'the supplier confirmed' : 'marked confirmed',
    if (s == 'part_received')
      () {
        final left = lines.where((l) => _int(l['received']) < _int(l['quantity'])).length;
        return left == 1 ? '1 item still to come' : '$left items to come';
      }(),
    if (_int(p['owed_kobo']) > 0) 'owed ${naira(_int(p['owed_kobo']))}' else if (_int(p['paid_kobo']) > 0) 'paid',
  ].join(' · ');
}

int? _whole(String v) => RegExp(r'^\d+$').hasMatch(v.trim()) ? int.parse(v.trim()) : null;

/// purchases.ts receiveProblem.
String? receiveProblem(List<String> counts) {
  if (counts.any((v) => v.trim().isNotEmpty && _whole(v) == null)) return 'How many came is a whole number, like 20.';
  if (!counts.any((v) => (_whole(v) ?? 0) > 0)) return 'Say how many came of at least one item.';
  return null;
}

/// purchases.ts payProblem.
String? payProblem(String amount) {
  final k = feeKobo(amount);
  if (amount.trim().isEmpty || k == null || k <= 0) return 'Enter the amount paid, like 425000.';
  return null;
}

/// purchases.ts supplierProblem.
String? supplierProblem(String name, String phone, bool adding) {
  if (name.trim().isEmpty) return 'Give the supplier a name.';
  if (name.trim().length > 80) return 'A name is up to 80 characters.';
  if (adding && !RegExp(r'^\+?[\d\s-]{10,16}$').hasMatch(phone.trim())) return 'Enter a mobile number, like 0803 123 4567.';
  return null;
}

/// purchases.ts reachedLine.
String reachedLine(Map<String, dynamic> s) => switch (s['reached']) {
  'whatsapp' => 'Gets orders on WhatsApp',
  'telegram' => 'Gets orders on Telegram',
  'link' => 'Linked on Taskiem: orders go straight to their business',
  _ => 'Gets orders by text message until they tap the link',
};

/// purchases.ts supplierSub.
String supplierSub(Map<String, dynamic> s, int owed) => [
  _str(s['phone']),
  reachedLine(s),
  if (_int(s['open']) > 0) _int(s['open']) == 1 ? '1 open order' : '${_int(s['open'])} open orders',
  if (owed > 0) 'you owe ${naira(owed)}',
].where((x) => x.isNotEmpty).join(' · ');

Widget _notice(TkTokens t, String text) => Semantics(
  liveRegion: true,
  container: true,
  child: Container(
    width: double.infinity,
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
    decoration: BoxDecoration(color: t.tkLimeSoft, borderRadius: BorderRadius.circular(14)),
    child: Text(text, style: tkText(size: 15, color: t.text)),
  ),
);

Widget _problem(TkTokens t, String text) => Padding(
  padding: const EdgeInsets.only(top: 8),
  child: Text(text, style: tkText(size: 13, weight: 600, color: t.danger)),
);

/// A modal's form: its fields, its problem, its error, its buttons; it
/// owns its boxes, so they outlive no closing.
class _ModalForm extends StatefulWidget {
  const _ModalForm({required this.boxes, required this.build, required this.problem, required this.save, required this.saveWords});
  final Map<String, String> boxes;
  final List<Widget> Function(BuildContext context, Map<String, TextEditingController> c, StateSetter set) build;
  final String? Function(Map<String, TextEditingController> c) problem;
  final Future<void> Function(Map<String, TextEditingController> c) save;
  final String saveWords;

  @override
  State<_ModalForm> createState() => _ModalFormState();
}

class _ModalFormState extends State<_ModalForm> {
  late final Map<String, TextEditingController> _c = {for (final e in widget.boxes.entries) e.key: TextEditingController(text: e.value)};
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    for (final c in _c.values) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final c in _c.values) {
      c.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = widget.problem(_c);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        ...widget.build(context, _c, setState),
        if (problem != null) _problem(t, problem),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 8), child: TkErrorBox(_error!)),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TkButton(
              label: widget.saveWords,
              primary: true,
              enabled: !_busy && problem == null,
              onPressed: () async {
                setState(() => (_busy = true, _error = null));
                try {
                  await widget.save(_c);
                  if (context.mounted) Navigator.of(context).pop(true);
                } on Object catch (e) {
                  if (mounted) setState(() => (_error = errorWords(e), _busy = false));
                }
              },
            ),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop(false)),
          ],
        ),
      ],
    );
  }
}

// --- one order ---

/// An order to a supplier: its total, what the supplier says, its lines,
/// their change, and the buyer's steps.
class PurchasePage extends StatefulWidget {
  const PurchasePage({super.key, required this.auth, required this.id, this.follow = true, this.backLabel = 'Back'});
  final Auth auth;
  final String id;
  final bool follow;

  /// "Back to Supplies": the head's link says where it goes back to.
  final String backLabel;

  @override
  State<PurchasePage> createState() => _PurchasePageState();
}

class _PurchasePageState extends State<PurchasePage> with Polling {
  Map<String, dynamic>? _p;
  String? _error;
  bool _busy = false;
  String? _actError;
  List<Map<String, dynamic>>? _own;
  bool _ownFailed = false;
  final Map<int, String> _chosen = {};

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
    if (widget.follow) startPolling(const Duration(seconds: 30), _load);
  }

  Future<void> _load() async {
    try {
      final r = (await _auth.transport.send('GET', _purchasePath(widget.id)) as Map).cast<String, dynamic>();
      final p = (r['purchase'] as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_p = p, _error = null));
      if (_own == null && !_ownFailed && _auth.can('storefront.manage') && buyerSteps(p).confirmProposed) {
        try {
          final s = (await _auth.transport.send('GET', '/v1/stock') as Map).cast<String, dynamic>();
          if (mounted) setState(() => _own = s['keeps_stock'] == true ? _maps(s['items']) : const []);
        } on Object {
          if (mounted) setState(() => _ownFailed = true);
        }
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _go(String what, String done, [Object? body]) async {
    setState(() => (_busy = true, _actError = null));
    try {
      await _auth.transport.send('POST', _purchasePath(_str(_p!['id']), '/$what'), body: body ?? const {});
      if (mounted) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  String _pick(int i) => _chosen[i] ?? sameName(_own ?? const [], _str(_maps(_p!['lines'])[i]['item']));

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final p = _p;
    return TkPageScreen(
      title: p == null ? 'Order' : '${p['reference']} to ${p['supplier']}',
      titleSize: 22,
      backLabel: widget.backLabel,
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (p == null && _error == null) const TkLoading(rows: 3, label: 'Loading the order…'),
        if (p != null) ..._page(t, p),
      ],
    );
  }

  List<Widget> _page(TkTokens t, Map<String, dynamic> p) {
    final supplier = _str(p['supplier']);
    final ref = _str(p['reference']);
    final lines = _maps(p['lines']);
    final change = _maps(p['change']);
    final seller = sellerLine(p);
    final steps = buyerSteps(p);
    final manage = _auth.can('storefront.manage');
    final receive = _auth.can('table.edit');
    final answer = answersDelivery(p);
    final delivered = _str(p['delivered_at']).isNotEmpty;
    final asking = const {'accepted', 'ready', 'dispatched'}.contains(p['seller_status']);
    return [
      _top(context, p, steps, manage, receive, answer),
      // The supplier: on one card, with what they last said.
      TkPanel(
        padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
        children: [
          Row(
            children: [
              TkFace(tkInitials(supplier), size: 40, colors: tkFaceFor(supplier)),
              const SizedBox(width: 12),
              Expanded(
                child: MergeSemantics(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(supplier, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 16, weight: 600, color: t.text)),
                      Text(
                        seller.isNotEmpty
                            ? '$seller${_str(p['seller_note']).isNotEmpty ? ' “${p['seller_note']}”' : ''}'
                            : (_str(p['link_id']).isNotEmpty ? 'On Taskiem' : 'Gets your orders on WhatsApp or by text'),
                        maxLines: 3,
                        overflow: TextOverflow.ellipsis,
                        style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted),
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
      if (_str(p['cancel_asked_at']).isNotEmpty) Text('You asked $supplier to cancel it; they cancel it or keep it.', style: tkHint(t)),
      TkPageCard(
        title: 'What you ordered',
        children: [
          for (final l in lines)
            MergeSemantics(
              child: Padding(
                padding: const EdgeInsets.only(bottom: 10),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Expanded(child: Text(_str(l['item']), style: tkText(size: 15, weight: 600, color: t.text))),
                        const SizedBox(width: 12),
                        Text(naira(_int(l['quantity']) * _int(l['cost_kobo'])), style: tkText(size: 15, weight: 600, color: t.text)),
                      ],
                    ),
                    Text(
                      [
                        'Ordered ${quantity(_int(l['quantity']), l['unit'] as String?)} at ${naira(_int(l['cost_kobo']))} each',
                        if (delivered) 'delivered ${_int(l['delivered'])}',
                        'received ${_int(l['received'])}',
                      ].join(' · '),
                      style: tkHint(t),
                    ),
                  ],
                ),
              ),
            ),
          Container(height: 1, color: TkLook.of(context).line),
          const SizedBox(height: 10),
          MergeSemantics(
            child: Row(
              children: [
                Expanded(child: Text('Total', style: tkText(size: 15, weight: 700, color: t.text))),
                Text(naira(_int(p['total_kobo'])), style: tkDisplay(size: 19, letterSpacing: -0.4, color: t.text)),
              ],
            ),
          ),
        ],
      ),
      if (change.isNotEmpty && p['seller_status'] == 'changed')
        TkPageCard(
          title: '$supplier can supply it like this',
          children: [
            TkLines([
              for (final l in change)
                (
                  '${_str(l['item'])} · ${_int(l['quantity']) == 0 ? 'None' : quantity(_int(l['quantity']), l['unit'] as String?)}',
                  '${naira(_int(l['cost_kobo']))} each',
                ),
              ('New total', naira(change.fold(0, (n, l) => n + _int(l['quantity']) * _int(l['cost_kobo'])))),
            ]),
          ],
        ),
      if (manage && steps.confirmProposed)
        TkPageCard(
          title: 'Confirm it',
          children: [
            if (_ownFailed || (_own != null && _own!.isEmpty))
              Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('Keep your stock on Taskiem to confirm it, so what comes is received into your items.', style: tkHint(t)),
                  TkLink(
                    label: 'Stock',
                    onTap: () => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => StockPage(auth: _auth))),
                  ),
                ],
              )
            else if (_own == null)
              const TkLoading(rows: 2, label: 'Loading your items…')
            else ...[
              Text('Which of your items each is. What comes is received into it.', style: tkHint(t)),
              for (var i = 0; i < lines.length; i++)
                Padding(
                  padding: const EdgeInsets.only(top: 10),
                  child: TkKitField(
                    field: false,
                    label: 'Their ${lines[i]['item']}',
                    child: Align(
                      alignment: Alignment.centerLeft,
                      child: TkSmallButton(
                        label: _own!.where((x) => x['id'] == _pick(i)).map((x) => _str(x['name'])).firstOrNull ?? 'Choose your item',
                        semantics:
                            'Their ${lines[i]['item']}: ${_own!.where((x) => x['id'] == _pick(i)).map((x) => _str(x['name'])).firstOrNull ?? 'none chosen'}',
                        onPressed: () async {
                          final v = await showTkModalChoices(
                            context,
                            title: 'Their ${lines[i]['item']}',
                            value: _pick(i),
                            choices: [for (final it in _own!) (_str(it['id']), _str(it['name']))],
                          );
                          if (v != null) setState(() => _chosen[i] = v);
                        },
                      ),
                    ),
                  ),
                ),
              const SizedBox(height: 10),
              Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(
                  label: 'Confirm the order',
                  primary: true,
                  onPressed: _busy || [for (var i = 0; i < lines.length; i++) _pick(i)].any((x) => x.isEmpty)
                      ? null
                      : () => _go('confirm-proposed', 'Confirmed, and the supplier is told', {
                          'items': [for (var i = 0; i < lines.length; i++) _pick(i)],
                        }),
                ),
              ),
            ],
          ],
        ),
      if (manage && steps.cancel)
        TkSection(
          inset: 56,
          children: [
            TkListRow(
              title: 'Cancel the order',
              sub: asking ? '$supplier has it: you ask them to cancel it' : '$supplier is told',
              icon: 'b-query',
              square: TkLook.of(context).red,
              onTap: _busy
                  ? null
                  : () async {
                      final yes = await showTkConfirm(
                        context,
                        title: asking ? 'Ask $supplier to cancel $ref?' : 'Cancel $ref?',
                        body: '$supplier is told.',
                        ok: asking ? 'Ask to cancel' : 'Cancel the order',
                        cancel: 'Keep it',
                        danger: true,
                      );
                      if (yes) await _go('cancel', 'Done, and the supplier is told');
                    },
            ),
          ],
        ),
      if (_actError != null) TkErrorBox(_actError!),
      if (_str(p['link_id']).isNotEmpty) DocThread(key: ValueKey('thread $ref'), auth: _auth, doc: 'purchase', reference: ref, follow: widget.follow),
    ];
  }

  /// The top card: what the order is now (and so what to do), its total,
  /// how far it has gone, and its answer: All came or Not all came, or
  /// Accept their change.
  Widget _top(BuildContext context, Map<String, dynamic> p, ({bool confirmProposed, bool acceptChange, bool cancel}) steps, bool manage, bool receive, bool answer) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final supplier = _str(p['supplier']);
    final status = _str(p['status']);
    final linked = _str(p['link_id']).isNotEmpty;
    final (words, ink) = receive && answer
        ? ('Say what came', look.orangeInk)
        : manage && steps.acceptChange
        ? ('They ask to change it', look.orangeInk)
        : manage && steps.confirmProposed
        ? ('Confirm their draft', look.violetInk)
        : (purchaseStatus(status).label, switch (status) {
            'received' => look.limeInk,
            'part_received' => look.orangeInk,
            'cancelled' => t.muted,
            'confirmed' => look.tealInk,
            _ => look.blueInk,
          });
    String when(String k) => _str(p[k]).isEmpty ? '' : shortDay(_str(p[k]));
    final latest = ['received_at', 'delivered_at', 'confirmed_at', 'sent_at'].firstWhere((k) => _str(p[k]).isNotEmpty, orElse: () => '');
    final buttons = <Widget>[
      if (receive && answer) ...[
        TkSmallButton(label: 'All came', primary: true, onPressed: _busy ? null : () => _go('all-came', 'Received, and your stock is up')),
        TkSmallButton(label: 'Not all came', onPressed: _busy ? null : () => _short(p)),
      ],
      if (manage && steps.acceptChange) TkSmallButton(label: 'Accept the change', primary: true, onPressed: _busy ? null : () => _go('accept-change', 'Change accepted')),
    ];
    return TkTopCard(
      semanticsLabel: 'The order',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow(words, color: ink, trailing: latest.isEmpty ? purchaseStatus(status).label : '${latest.replaceAll('_at', '')} ${when(latest)}'),
          const SizedBox(height: 6),
          FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(_int(p['total_kobo']), style: tkFigure(context, size: 40))),
          Text(
            'To $supplier · received ${naira(_int(p['received_kobo']))} · owed ${naira(_int(p['owed_kobo']))}',
            style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
          ),
          if (status != 'cancelled') ...[
            const SizedBox(height: 14),
            TkSteps(
              steps: [
                (label: 'Sent', when: when('sent_at'), done: _str(p['sent_at']).isNotEmpty || status != 'drafted'),
                (label: 'Confirmed', when: when('confirmed_at'), done: _str(p['confirmed_at']).isNotEmpty || const {'confirmed', 'part_received', 'received'}.contains(status)),
                if (linked) (label: 'Delivered', when: when('delivered_at'), done: _str(p['delivered_at']).isNotEmpty),
                (label: 'Received', when: when('received_at'), done: status == 'received'),
              ],
            ),
          ],
          if (buttons.isNotEmpty) ...[
            const SizedBox(height: 14),
            Row(children: [for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(width: 8), Expanded(child: buttons[i])]]),
          ],
          if (receive && answer) ...[
            const SizedBox(height: 8),
            Text('Within a day of the delivery; after that, $supplier’s count is taken.', style: tkText(size: 12.5, lineHeight: 1.4, color: t.muted)),
          ],
        ],
      ),
    );
  }

  Future<void> _short(Map<String, dynamic> p) async {
    final lines = _maps(p['lines']);
    await showTkModal<bool>(
      context,
      title: 'Not all came',
      builder: (_) => _ModalForm(
        boxes: {for (var i = 0; i < lines.length; i++) '$i': '${_int(lines[i]['delivered'])}'},
        saveWords: 'Record what came',
        problem: (c) => c.values.any((x) => _whole(x.text) == null) ? 'How many came is a whole number, like 20.' : null,
        build: (context, c, set) {
          final t = TkTheme.tokens(context);
          return [
            Text('How many came of each. Your stock goes up by your count, and ${p['supplier']} is told.', style: tkHint(t)),
            for (var i = 0; i < lines.length; i++)
              Padding(
                padding: const EdgeInsets.only(top: 10),
                child: TkKitField(
                  label: '${lines[i]['item']} (delivered ${_int(lines[i]['delivered'])})',
                  child: TkTextBox(controller: c['$i']!, digits: 6),
                ),
              ),
          ];
        },
        save: (c) async {
          await _auth.transport.send(
            'POST',
            _purchasePath(_str(p['id']), '/not-all-came'),
            body: {
              'counts': [for (var i = 0; i < lines.length; i++) _whole(c['$i']!.text)],
            },
          );
          if (mounted) showTkToast(context, 'Recorded, and the supplier is told');
          await _load();
        },
      ),
    );
  }
}

// --- the Stock screen's orders to suppliers ---

/// Orders to suppliers, on the Stock screen: order what is low, approve
/// and send, mark confirmed, goods in, a payment, cancel.
/// The orders to suppliers: on Stock, one row that opens Supplies (and
/// says how many are open and wait for the business); on Supplies, all of
/// them ([full]): what is on order, its flow, Order what is low, and each
/// order with what can be done to it.
class PurchasesCard extends StatefulWidget {
  const PurchasesCard({super.key, required this.auth, required this.items, required this.onChanged, this.round = 0, this.full = false, this.show = 'open'});
  final Auth auth;

  /// All of them, as Supplies' body; else Stock's row.
  final bool full;

  /// Supplies' filter: open, received or cancelled.
  final String show;

  /// The business's own items (a product's sizes are told together).
  final List<Map<String, dynamic>> items;
  final VoidCallback onChanged;

  /// Changed when the screen is read again: the card is too.
  final int round;

  @override
  State<PurchasesCard> createState() => _PurchasesCardState();
}

class _PurchasesCardState extends State<PurchasesCard> {
  List<Map<String, dynamic>>? _list;
  String? _error;
  bool _off = false;
  bool _busy = false;
  String? _actError;
  List<String> _skipped = const [];
  String _lowKey = formKey();

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(PurchasesCard old) {
    super.didUpdateWidget(old);
    if (old.round != widget.round) _load();
  }

  Future<void> _load() async {
    try {
      final r = (await _auth.transport.send('GET', '/v1/stock/purchases') as Map).cast<String, dynamic>();
      if (mounted) setState(() => (_list = _maps(r['purchases']), _error = null));
    } on Object catch (e) {
      // No suppliers on this Taskiem (no text messages): no card.
      if (mounted) setState(() => (_off = e is ApiError && e.status == 503, _error = errorWords(e)));
    }
  }

  Future<void> _reload() async {
    await _load();
    widget.onChanged();
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _actError = null));
    try {
      await f();
      if (mounted) showTkToast(context, done);
      await _reload();
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  /// An order opened on its screen; the card read again on return.
  Future<void> _open(String id) async {
    await openPurchase(context, widget.auth, id, backLabel: widget.full ? 'Back to Supplies' : 'Back to Stock');
    await _reload();
  }

  void _step(Map<String, dynamic> p, String what, String done) =>
      _run(() => _auth.transport.send('POST', _purchasePath(_str(p['id']), '/$what'), body: const {}), done);

  /// Where an order stands, as Supplies' flow shows it.
  static String? stage(Map<String, dynamic> p) {
    final s = _str(p['status']);
    final seller = _str(p['seller_status']);
    if (s == 'part_received' || (s != 'received' && s != 'cancelled' && (seller == 'dispatched' || seller == 'delivered'))) return 'coming';
    if (s == 'drafted') return 'draft';
    if (s == 'sent') return 'sent';
    if (s == 'confirmed') return 'confirmed';
    return null;
  }

  ({Color fill, Color ink})? _accent(TkLook look, Map<String, dynamic> p) => switch (p['status'] == 'received' ? 'received' : stage(p)) {
    'draft' => (fill: look.violet, ink: look.violetInk),
    'sent' => (fill: look.blue, ink: look.blueInk),
    'confirmed' => (fill: look.teal, ink: look.tealInk),
    'coming' => (fill: look.orange, ink: look.orangeInk),
    'received' => (fill: look.lime, ink: look.limeInk),
    _ => null,
  };

  Future<void> _orderLow() => _run(() async {
    final out = (await _auth.transport.send('POST', '/v1/stock/purchases/low', body: const {}, headers: {'Idempotency-Key': _lowKey}) as Map).cast<String, dynamic>();
    if (mounted) setState(() => (_lowKey = formKey(), _skipped = ((out['no_supplier'] as List?) ?? const []).whereType<String>().toList()));
  }, 'Drafted what is low');

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final list = _list ?? const <Map<String, dynamic>>[];
    bool open(Map<String, dynamic> p) => const {'drafted', 'sent', 'confirmed', 'part_received'}.contains(p['status']);
    final opened = list.where(open).toList();
    final waiting = opened.where(purchaseWaiting).toList();
    if (!widget.full) {
      if (_off) return const SizedBox.shrink();
      return TkPanel(
        inset: 56,
        children: [
          TkListRow(
            title: 'Orders to suppliers',
            sub: _list == null
                ? (_error ?? 'Loading…')
                : [opened.length == 1 ? '1 open' : '${opened.length} open', if (waiting.isNotEmpty) waiting.length == 1 ? '1 needs you' : '${waiting.length} need you'].join(' · '),
            icon: 'b-truck',
            square: look.blue,
            waiting: waiting.isNotEmpty,
            onTap: () async {
              await Navigator.of(context).push(
                TkRoute<void>(pageBuilder: (_, _, _) => SuppliesPage(auth: widget.auth, items: widget.items, backLabel: 'Back to Stock')),
              );
              await _reload();
            },
          ),
        ],
      );
    }
    if (_off) return const TkEmpty(icon: 'members', title: 'Suppliers need text messages', line: 'Text messages are not set up on this Taskiem yet.');
    final parents = {
      for (final i in widget.items)
        if (_str(i['variant_of']).isNotEmpty) _str(i['id']): _str(i['variant_of']),
    };
    final low = widget.items.where((i) => i['low'] == true).length;
    final onOrder = opened.fold(0, (n, p) => n + _int(p['total_kobo']));
    final owed = list.where((p) => p['status'] != 'cancelled').fold(0, (n, p) => n + _int(p['owed_kobo']));
    final shown = switch (widget.show) {
      'received' => list.where((p) => p['status'] == 'received').toList(),
      'cancelled' => list.where((p) => p['status'] == 'cancelled').toList(),
      _ => opened,
    };
    final parts = <Widget>[
      if (widget.show == 'open' && _list != null)
        TkTopCard(
          semanticsLabel: 'On order: ${naira(onOrder)}',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TkEyebrow('On order', color: look.blueInk, trailing: opened.length == 1 ? '1 order' : '${opened.length} orders'),
              const SizedBox(height: 6),
              FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(onOrder, style: tkFigure(context, size: 40))),
              Text(owed > 0 ? '${naira(owed)} still owed on what came' : 'Nothing owed on what came', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
              const SizedBox(height: 14),
              TkFlow(
                stages: [
                  (label: 'Draft', count: opened.where((p) => stage(p) == 'draft').length, ink: look.violetInk, dot: look.violet),
                  (label: 'Sent', count: opened.where((p) => stage(p) == 'sent').length, ink: look.blueInk, dot: look.blue),
                  (label: 'Confirmed', count: opened.where((p) => stage(p) == 'confirmed').length, ink: look.tealInk, dot: look.teal),
                  (label: 'Coming in', count: opened.where((p) => stage(p) == 'coming').length, ink: look.orangeInk, dot: look.orange),
                ],
              ),
              const SizedBox(height: 14),
              TkCallout(
                leading: TkSquare('b-cart', color: look.orange),
                title: low > 0 ? (low == 1 ? '1 item is low' : '$low items are low') : 'Low items',
                sub: 'Each becomes an order to its supplier, enough to reach its order-up-to level',
                color: look.orange,
                button: 'Order',
                semantics: 'Order what is low',
                onPressed: _busy ? null : _orderLow,
              ),
            ],
          ),
        ),
      if (_skipped.isNotEmpty)
        _notice(t, '${_skipped.join(', ')} ${_skipped.length == 1 ? 'has' : 'have'} no supplier Taskiem knows. Add the supplier, then give the item their name.'),
      if (_error != null) TkErrorBox(_error!),
      if (_actError != null) TkErrorBox(_actError!),
      if (_list == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading your orders to suppliers…'),
      if (_list != null && shown.isEmpty)
        TkSection(empty: widget.show == 'open' ? 'No open orders. Order what is low, and the drafts show here.' : 'None yet.', children: const []),
      if (widget.show == 'open') ...[
        if (waiting.isNotEmpty) TkSection(title: 'Needs you', count: waiting.length, children: [for (final p in waiting) _row(t, p, parents)]),
        if (opened.length > waiting.length)
          TkSection(title: 'On the way', children: [for (final p in opened.where((p) => !purchaseWaiting(p))) _row(t, p, parents)]),
      ] else if (shown.isNotEmpty)
        TkSection(children: [for (final p in shown.take(50)) _row(t, p, parents)]),
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < parts.length; i++) ...[if (i > 0) const SizedBox(height: 14), parts[i]],
      ],
    );
  }

  Widget _row(TkTokens t, Map<String, dynamic> p, Map<String, String> parents) {
    final look = TkLook.of(context);
    final can = purchaseActions(p);
    final ref = _str(p['reference']);
    final supplier = _str(p['supplier']);
    final st = purchaseStatus(_str(p['status']));
    final seller = sellerLine(p);
    final linked = _str(p['link_id']).isNotEmpty;
    final note = [if (_str(p['counter']).isNotEmpty) '$supplier asked: “${p['counter']}”', if (seller.isNotEmpty) seller].join(' ');
    final buttons = <Widget>[
      if (linked) TkSmallButton(label: 'Open', semantics: 'Open $ref', onPressed: () => _open(_str(p['id']))),
      if (can.send)
        TkSmallButton(
          label: 'Approve and send',
          semantics: 'Approve and send $ref',
          primary: true,
          onPressed: _busy ? null : () => _step(p, 'send', '$ref sent to $supplier'),
        ),
      if (can.confirm)
        TkSmallButton(label: 'Mark confirmed', semantics: 'Mark confirmed $ref', onPressed: _busy ? null : () => _step(p, 'confirm', '$ref marked confirmed')),
      if (can.receive) TkSmallButton(label: 'Goods came in', semantics: 'Goods came in on $ref', primary: true, onPressed: () => _receive(p)),
      if (can.answerDelivery) TkSmallButton(label: 'Goods came in', semantics: 'Goods came in on $ref', primary: true, onPressed: () => _open(_str(p['id']))),
      if (can.pay && _int(p['owed_kobo']) > 0) TkSmallButton(label: 'Record a payment', semantics: 'Record a payment for $ref', onPressed: () => _pay(p)),
      if (can.cancel)
        TkSmallButton(
          label: 'Cancel',
          semantics: 'Cancel $ref',
          danger: true,
          onPressed: _busy
              ? null
              : () async {
                  final yes = await showTkConfirm(
                    context,
                    title: 'Cancel $ref to $supplier?',
                    body: '$supplier is told.',
                    ok: 'Cancel it',
                    cancel: 'Keep it',
                    danger: true,
                  );
                  if (yes) _step(p, 'cancel', '$ref cancelled');
                },
        ),
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkListRow(
          title: supplier,
          letters: supplier,
          sub: '$ref · ${purchaseSub(p, parents)}',
          amount: naira(_int(p['total_kobo'])),
          status: st.label,
          statusAccent: _accent(look, p),
          waiting: purchaseWaiting(p),
          onTap: linked ? () => _open(_str(p['id'])) : null,
        ),
        if (note.isNotEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(66, 0, 14, 8),
            child: Text(note, maxLines: 3, overflow: TextOverflow.ellipsis, style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted)),
          ),
        if (buttons.isNotEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(66, 0, 14, 12),
            child: Wrap(spacing: 8, runSpacing: 8, children: buttons),
          ),
      ],
    );
  }

  Future<void> _receive(Map<String, dynamic> p) async {
    final lines = _maps(p['lines']);
    final key = formKey();
    await showTkModal<bool>(
      context,
      title: 'Goods in on ${p['reference']}',
      builder: (_) => _ModalForm(
        boxes: {for (final l in lines) _str(l['item_id']): '${(_int(l['quantity']) - _int(l['received'])).clamp(0, 1 << 31)}'},
        saveWords: 'Record',
        problem: (c) => receiveProblem([for (final x in c.values) x.text]),
        build: (context, c, set) {
          final t = TkTheme.tokens(context);
          return [
            Text('From ${p['supplier']}. Each item comes into your stock, and its cost price becomes the order’s.', style: tkHint(t)),
            for (final l in lines)
              Padding(
                padding: const EdgeInsets.only(top: 10),
                child: TkKitField(
                  label: '${l['item']}: how many came',
                  hint:
                      'Ordered ${quantity(_int(l['quantity']), l['unit'] as String?)}${_int(l['received']) > 0 ? ', ${_int(l['received'])} came before' : ''}.',
                  child: TkTextBox(controller: c[_str(l['item_id'])]!, digits: 6),
                ),
              ),
          ];
        },
        save: (c) async {
          await _auth.transport.send(
            'POST',
            _purchasePath(_str(p['id']), '/receive'),
            headers: {'Idempotency-Key': key},
            body: {
              'lines': [
                for (final e in c.entries)
                  if ((_whole(e.value.text) ?? 0) > 0) {'item_id': e.key, 'quantity': _whole(e.value.text)},
              ],
            },
          );
          if (mounted) showTkToast(context, 'Recorded');
          await _reload();
        },
      ),
    );
  }

  Future<void> _pay(Map<String, dynamic> p) async {
    final key = formKey();
    var how = 'transfer';
    await showTkModal<bool>(
      context,
      title: 'Pay ${p['supplier']}',
      builder: (_) => _ModalForm(
        boxes: {'amount': feeInput(_int(p['owed_kobo']))},
        saveWords: 'Record the payment',
        problem: (c) => payProblem(c['amount']!.text),
        build: (context, c, set) {
          final t = TkTheme.tokens(context);
          return [
            Text('For ${p['reference']}: ${naira(_int(p['owed_kobo']))} owed. It goes into your money record as an expense.', style: tkHint(t)),
            const SizedBox(height: 10),
            TkKitField(
              label: 'Amount paid (₦)',
              child: TkTextBox(controller: c['amount']!, money: true),
            ),
            const SizedBox(height: 10),
            Text('How you paid', style: tkText(size: 13, weight: 600, color: t.muted)),
            for (final (v, words) in const [('transfer', 'Transfer'), ('cash', 'Cash')])
              TkChoiceRow(
                control: TkRadio(checked: how == v),
                label: Text(words, style: tkText(size: 15, color: t.text)),
                onTap: () => set(() => how = v),
              ),
          ];
        },
        save: (c) async {
          await _auth.transport.send(
            'POST',
            _purchasePath(_str(p['id']), '/pay'),
            headers: {'Idempotency-Key': key},
            body: {'amount_kobo': feeKobo(c['amount']!.text), 'paid_how': how},
          );
          if (mounted) showTkToast(context, 'Payment recorded');
          await _reload();
        },
      ),
    );
  }
}

// --- supplies ---

/// Supplies: the orders to suppliers on a screen of their own (docs/spec/
/// mobile-app-look.md), opened from Stock's row and from Orders' "To
/// suppliers". It reads the business's items itself when not handed them,
/// to say what is low.
class SuppliesPage extends StatefulWidget {
  const SuppliesPage({super.key, required this.auth, this.items, this.backLabel = 'Back'});
  final Auth auth;
  final List<Map<String, dynamic>>? items;
  final String backLabel;

  @override
  State<SuppliesPage> createState() => _SuppliesPageState();
}

class _SuppliesPageState extends State<SuppliesPage> {
  String _show = 'open';
  int _round = 0;
  late List<Map<String, dynamic>> _items = widget.items ?? const [];

  @override
  void initState() {
    super.initState();
    if (widget.items == null) _loadItems();
  }

  Future<void> _loadItems() async {
    try {
      final v = (await widget.auth.transport.send('GET', '/v1/stock') as Map).cast<String, dynamic>();
      if (mounted) setState(() => _items = _maps(v['items']));
    } on Object {
      // Order what is low still works; it just cannot say how many.
    }
  }

  @override
  Widget build(BuildContext context) => TkPageScreen(
    title: 'Supplies',
    titleSize: 30,
    backLabel: widget.backLabel,
    description: 'Your orders to suppliers. Each low item becomes an order to its supplier, for enough to reach its order-up-to level. Approve and send it, and the supplier confirms on WhatsApp.',
    onRefresh: () async {
      setState(() => _round++);
      await _loadItems();
    },
    action: TkHeadButton(icon: 'b-people', label: 'Suppliers', onTap: () => openSuppliers(context, widget.auth)),
    under: TkChips<String>(
      label: 'Show',
      chips: const [('open', 'Open'), ('received', 'Received'), ('cancelled', 'Cancelled')],
      on: _show,
      onPick: (v) => setState(() => _show = v),
    ),
    children: [
      PurchasesCard(key: const ValueKey('supplies'), auth: widget.auth, items: _items, onChanged: _loadItems, round: _round, full: true, show: _show),
    ],
  );
}

// --- suppliers ---

/// The business's suppliers: who it buys from, where their orders reach
/// them, what is owed to them; who may send a large order; where linked
/// suppliers deliver.
class SuppliersPage extends StatefulWidget {
  const SuppliersPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<SuppliersPage> createState() => _SuppliersPageState();
}

class _SuppliersPageState extends State<SuppliersPage> {
  Map<String, dynamic>? _view;
  String? _error;
  bool _off = false;
  bool _retired = false;
  List<Map<String, dynamic>> _bills = const [];
  Map<String, dynamic>? _deliver;
  bool _busy = false;
  String? _actError;
  final _limit = TextEditingController();
  String _limitWas = '';
  final _address = TextEditingController();
  final _phone = TextEditingController();

  Auth get _auth => widget.auth;

  @override
  void initState() {
    super.initState();
    for (final c in [_limit, _address, _phone]) {
      c.addListener(() => setState(() {}));
    }
    _load();
  }

  @override
  void dispose() {
    _limit.dispose();
    _address.dispose();
    _phone.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final t = _auth.transport;
    await Future.wait([
      () async {
        try {
          final v = (await t.send('GET', '/v1/stock/suppliers', query: _retired ? {'retired': 'true'} : null) as Map).cast<String, dynamic>();
          final limit = (v['settings'] as Map?)?['purchase_limit_kobo'];
          if (mounted) {
            setState(() {
              _view = v;
              _error = null;
              _off = false;
              final was = limit == null ? '' : feeInput((limit as num).toInt());
              if (_limit.text == _limitWas) _limit.text = was;
              _limitWas = was;
            });
          }
        } on Object catch (e) {
          if (mounted) setState(() => (_error = errorWords(e), _off = e is ApiError && e.status == 503));
        }
      }(),
      () async {
        try {
          final r = (await t.send('GET', '/v1/bills') as Map).cast<String, dynamic>();
          if (mounted) setState(() => _bills = _maps(r['bills']));
        } on Object {
          // What linked suppliers are owed, unknown: their own figures show.
        }
      }(),
      if (_deliver == null)
        () async {
          try {
            final r = (await t.send('GET', '/v1/stock/deliver-to') as Map).cast<String, dynamic>();
            if (mounted) setState(() => (_deliver = r, _address.text = _str(r['address']), _phone.text = _str(r['phone'])));
          } on Object {
            // No card, then.
          }
        }(),
    ]);
  }

  /// What is owed to a linked supplier is its open bills alone.
  int _owed(Map<String, dynamic> s) {
    final link = _str(s['link_id']);
    if (link.isEmpty) return _int(s['owed_kobo']);
    return _bills.where((b) => b['link'] == link && b['status'] != 'void' && _int(b['due_kobo']) > 0).fold(0, (n, b) => n + _int(b['due_kobo']));
  }

  Future<void> _run(Future<void> Function() f, String done) async {
    setState(() => (_busy = true, _actError = null));
    try {
      await f();
      if (mounted) showTkToast(context, done);
      await _load();
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final v = _view;
    final list = v == null ? const <Map<String, dynamic>>[] : _maps(v['suppliers']);
    final limitKobo = _limit.text.trim().isEmpty ? 0 : feeKobo(_limit.text);
    final limitBad = _limit.text.trim().isNotEmpty && limitKobo == null;
    // Linked suppliers' open bills, by supplier.
    final owedLinked = <String, ({String name, int due, int bills})>{};
    for (final b in _bills.where((b) => b['status'] != 'void' && _int(b['due_kobo']) > 0)) {
      final k = _str(b['supplier']).isNotEmpty ? _str(b['supplier']) : _str(b['supplier_name']);
      final was = owedLinked[k];
      owedLinked[k] = (name: _str(b['supplier_name']), due: (was?.due ?? 0) + _int(b['due_kobo']), bills: (was?.bills ?? 0) + 1);
    }
    final look = TkLook.of(context);
    final owedBills = owedLinked.values.fold(0, (n, o) => n + o.due);
    final owedOrders = list.where((s) => _str(s['link_id']).isEmpty).fold(0, (n, s) => n + _int(s['owed_kobo']));
    final owing = list.where((s) => _owed(s) > 0).length;
    return TkPageScreen(
      title: 'Suppliers',
      description: 'Who you buy your stock from. Your orders reach them on WhatsApp, and they confirm with one tap.',
      backLabel: 'Back to Stock',
      onRefresh: _load,
      titleSize: 30,
      action: v == null ? null : TkHeadButton(icon: 'b-plus', label: 'Add a supplier', primary: true, onTap: () => _edit(null)),
      under: _off
          ? null
          : TkChips<bool>(
              label: 'Show',
              chips: const [(false, 'Current'), (true, 'Retired')],
              on: _retired,
              onPick: (r) {
                if (r == _retired) return;
                setState(() => (_retired = r, _view = null));
                _load();
              },
            ),
      children: [
        if (_off)
          const TkEmpty(icon: 'members', title: 'Suppliers need text messages', line: 'Text messages are not set up on this Taskiem yet.')
        else if (_error != null)
          TkErrorBox(_error!),
        if (v == null && _error == null) const TkLoading(rows: 3, label: 'Loading your suppliers…'),
        if (_actError != null) TkErrorBox(_actError!),
        if (v != null) ...[
          if (!_retired && (owedBills > 0 || owedOrders > 0))
            TkTopCard(
              semanticsLabel: 'You owe your suppliers ${naira(owedBills + owedOrders)}',
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  TkEyebrow('You owe them', color: look.orangeInk, trailing: owing == 1 ? '1 supplier' : '$owing suppliers'),
                  const SizedBox(height: 6),
                  FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(owedBills + owedOrders, style: tkFigure(context, size: 40))),
                  const SizedBox(height: 12),
                  TkSplitBar(
                    percent: false,
                    parts: [
                      (label: 'Bills on Taskiem ${naira(owedBills)}', share: (100 * owedBills / (owedBills + owedOrders)).round(), color: look.orange),
                      (label: 'Unpaid orders ${naira(owedOrders)}', share: (100 * owedOrders / (owedBills + owedOrders)).round(), color: look.blue),
                    ],
                  ),
                ],
              ),
            ),
          TkSection(
            empty: _retired ? 'No retired suppliers.' : 'No suppliers yet. Add who you buy from, and the items they supply.',
            children: [for (final s in list) _supplier(t, s)],
          ),
          if (!_retired)
            TkPageCard(
              title: 'Approving orders',
              children: [
                Text(
                  'An order above this amount is sent by another owner or admin, not the person who drafted it. Leave it empty and whoever drafts an order sends it.',
                  style: tkHint(t),
                ),
                const SizedBox(height: 10),
                TkKitField(
                  label: 'A second person sends orders above (₦)',
                  child: TkTextBox(controller: _limit, money: true, hint: '500000'),
                ),
                if (limitBad) _problem(t, 'Enter an amount, like 500000.'),
                const SizedBox(height: 10),
                Align(
                  alignment: Alignment.centerLeft,
                  child: TkSmallButton(
                    label: 'Save',
                    semantics: 'Save the limit',
                    primary: true,
                    onPressed: _busy || limitBad || _limit.text == _limitWas
                        ? null
                        : () => _run(
                            () =>
                                _auth.transport.send('PUT', '/v1/stock/settings', body: {'purchase_limit_kobo': _limit.text.trim().isEmpty ? null : limitKobo}),
                            'Saved',
                          ),
                  ),
                ),
              ],
            ),
        ],
        if (owedLinked.isNotEmpty)
          TkPageCard(
            title: 'Linked suppliers on Taskiem',
            trailing: TkLink(label: 'All bills', onTap: () => openBills(context, _auth)),
            children: [
              Text('What you owe them is their open bills, as their own records say.', style: tkHint(t)),
              for (final o in owedLinked.values)
                TkRow(
                  name: o.name,
                  icon: 'invoices',
                  tone: CardTone.warn,
                  sub: '${o.bills} ${o.bills == 1 ? 'bill' : 'bills'} to pay · ${naira(o.due)}',
                  onTap: () => openBills(context, _auth),
                ),
            ],
          ),
        if (_deliver != null)
          TkPageCard(
            title: 'Where linked suppliers deliver',
            children: [
              Text('Orders to suppliers on Taskiem carry it, and their rider brings them here. Leave it empty and you pick orders up.', style: tkHint(t)),
              const SizedBox(height: 10),
              TkKitField(
                label: 'Address',
                child: TkTextBox(controller: _address, max: 300, hint: '12 Admiralty Way, Lekki'),
              ),
              const SizedBox(height: 10),
              TkKitField(
                label: 'Number the rider calls at the door',
                child: TkTextBox(controller: _phone, phone: true, hint: '0803 123 4567'),
              ),
              const SizedBox(height: 10),
              Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(
                  label: 'Save',
                  semantics: 'Save where they deliver',
                  primary: true,
                  onPressed: _busy || (_address.text == _str(_deliver!['address']) && _phone.text == _str(_deliver!['phone']))
                      ? null
                      : () => _run(() async {
                          final body = {'address': _address.text.trim(), 'phone': _phone.text.trim()};
                          await _auth.transport.send('PUT', '/v1/stock/deliver-to', body: body);
                          if (mounted) setState(() => _deliver = body);
                        }, 'Saved'),
                ),
              ),
            ],
          ),
      ],
    );
  }

  /// A supplier: their letters, how orders reach them and what is open,
  /// what you owe them, On Taskiem when linked; pressed, what can be done.
  Widget _supplier(TkTokens t, Map<String, dynamic> s) {
    final look = TkLook.of(context);
    final name = _str(s['name']);
    final owed = _owed(s);
    final link = _str(s['link_id']);
    return TkListRow(
      title: name,
      letters: name,
      sub: [
        _str(s['phone']),
        if (link.isEmpty) reachedLine(s),
        if (_int(s['open']) > 0) _int(s['open']) == 1 ? '1 open order' : '${_int(s['open'])} open orders',
      ].where((x) => x.isNotEmpty).join(' · '),
      amount: owed > 0 ? naira(owed) : null,
      status: link.isNotEmpty ? 'On Taskiem' : null,
      statusAccent: (fill: look.lime, ink: look.limeInk),
      semanticsLabel: owed > 0 ? '$name, you owe ${naira(owed)}' : name,
      onTap: _retired ? null : () => _menu(s),
    );
  }

  /// What can be done to a supplier, in a modal.
  Future<void> _menu(Map<String, dynamic> s) async {
    final name = _str(s['name']);
    final link = _str(s['link_id']);
    final pick = await showTkModalChoices(
      context,
      title: name,
      choices: [
        ('change', 'Change'),
        if (link.isNotEmpty) ('prices', 'Prices'),
        if (link.isNotEmpty) ('statement', 'Statement'),
        ('link', 'On Taskiem'),
        if (s['reached'] == 'sms') ('invite', 'Send the link again'),
        ('retire', 'Retire'),
      ],
    );
    if (!mounted || pick == null) return;
    switch (pick) {
      case 'change':
        await _edit(s);
      case 'prices':
        final open = Openers.orderFrom;
        if (open == null) return showComingSoon(context);
        open(context, link, name);
      case 'statement':
        openStatement(context, _auth, link);
      case 'link':
        await _link(s);
      case 'invite':
        await _run(() => _auth.transport.send('POST', '/v1/stock/suppliers/${Uri.encodeComponent(_str(s['id']))}/invite', body: const {}), 'Link sent to $name');
      case 'retire':
        final yes = await showTkConfirm(
          context,
          title: 'Retire $name?',
          body: 'Their orders stay, and you can add them again.',
          ok: 'Retire',
          cancel: 'Keep them',
          danger: true,
        );
        if (!yes) return;
        await _run(() => _auth.transport.send('POST', '/v1/stock/suppliers/${Uri.encodeComponent(_str(s['id']))}/retire', body: const {}), '$name retired');
    }
  }

  Future<void> _edit(Map<String, dynamic>? s) async {
    final adding = s == null;
    var items = const <Map<String, dynamic>>[];
    if (adding) {
      try {
        final v = (await _auth.transport.send('GET', '/v1/stock') as Map).cast<String, dynamic>();
        if (v['keeps_stock'] == true) items = _maps(v['items']);
      } on Object {
        // No items to choose: the supplier is added without them.
      }
    }
    if (!mounted) return;
    final picked = <String>{};
    var said = '';
    final saved = await showTkModal<bool>(
      context,
      title: adding ? 'Add a supplier' : 'Change ${s['name']}',
      builder: (_) => _ModalForm(
        boxes: {'name': adding ? '' : _str(s['name']), 'phone': ''},
        saveWords: adding ? 'Add the supplier' : 'Save',
        problem: (c) =>
            c['name']!.text.isEmpty && c['phone']!.text.isEmpty ? 'Give the supplier a name.' : supplierProblem(c['name']!.text, c['phone']!.text, adding),
        build: (context, c, set) {
          final t = TkTheme.tokens(context);
          return [
            TkKitField(
              label: 'Name',
              child: TkTextBox(controller: c['name']!, max: 80, hint: 'Dangote Depot'),
            ),
            if (adding) ...[
              const SizedBox(height: 10),
              TkKitField(
                label: 'Mobile number',
                hint: 'They get a link by SMS. One tap, and your orders reach them on WhatsApp.',
                child: TkTextBox(controller: c['phone']!, phone: true, hint: '0803 123 4567'),
              ),
            ],
            if (adding && items.isNotEmpty) ...[
              const SizedBox(height: 10),
              Text('What they supply', style: tkText(size: 13, weight: 600, color: t.muted)),
              for (final it in items)
                TkChoiceRow(
                  control: TkCheckbox(checked: picked.contains(it['id'])),
                  label: Text('${it['name']}${_str(it['supplier']).isNotEmpty ? ' (now ${it['supplier']})' : ''}', style: tkText(size: 15, color: t.text)),
                  onTap: () => set(() => picked.contains(it['id']) ? picked.remove(it['id']) : picked.add(_str(it['id']))),
                ),
            ],
            if (!adding)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text('The items naming ${s['name']} take the new name too.', style: tkHint(t)),
              ),
          ];
        },
        save: (c) async {
          final name = c['name']!.text.trim();
          if (adding) {
            await _auth.transport.send('POST', '/v1/stock/suppliers', body: {'name': name, 'phone': c['phone']!.text.trim(), 'items': picked.toList()});
            said = '$name added; the link is on its way by SMS';
          } else {
            await _auth.transport.send('PATCH', '/v1/stock/suppliers/${Uri.encodeComponent(_str(s['id']))}', body: {'name': name});
            said = 'Saved';
          }
        },
      ),
    );
    if (saved == true && mounted) {
      showTkToast(context, said);
      if (_retired) setState(() => _retired = false);
      await _load();
    }
  }

  Future<void> _link(Map<String, dynamic> s) async {
    List<Map<String, dynamic>> linked;
    try {
      final r = (await _auth.transport.send('GET', '/v1/links') as Map).cast<String, dynamic>();
      linked = _maps(r['links']).where((l) => l['state'] == 'active' && (l['role'] == 'supplier' || l['role'] == 'both')).toList();
    } on ApiError catch (e) {
      // Links not available on this Taskiem: none. Any other failure is said.
      if (e.status != 503) {
        if (mounted) setState(() => _actError = e.message);
        return;
      }
      linked = const [];
    } on Object catch (e) {
      if (mounted) setState(() => _actError = errorWords(e));
      return;
    }
    if (!mounted) return;
    final name = _str(s['name']);
    final was = _str(s['link_id']);
    if (linked.isEmpty) {
      await showTkModal<void>(
        context,
        title: '$name on Taskiem',
        builder: (ctx) => Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'You are not linked with a business that supplies you yet. Find them, then say here which business $name is.',
              style: tkHint(TkTheme.tokens(ctx)),
            ),
            if (Openers.findBusiness != null) ...[
              const SizedBox(height: 8),
              TkLink(
                label: 'Find them',
                onTap: () {
                  Navigator.of(ctx).pop();
                  Openers.findBusiness!(context);
                },
              ),
            ],
          ],
        ),
      );
      return;
    }
    final pick = await showTkModalChoices(
      context,
      title: 'Which business is $name?',
      value: was,
      choices: [('', 'Not on Taskiem (orders by message)'), for (final l in linked) (_str(l['id']), _str(l['name']))],
    );
    if (pick == null || pick == was) return;
    await _run(
      () => _auth.transport.send('PUT', '/v1/stock/suppliers/${Uri.encodeComponent(_str(s['id']))}/link', body: {'link_id': pick.isEmpty ? null : pick}),
      'Saved',
    );
  }
}
