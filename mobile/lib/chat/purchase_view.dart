// An order sent to a linked business (M5 part O; web ChatBuyer.tsx
// PurchaseView), opened from Your orders: what it asks for, where it is,
// and what can be done now: confirm their quote (saying how each line is
// treated, what a business buys §6), accept their change, say all came or how many did,
// cancel, reorder; each as the person's leave allows.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import '../api/transport.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart' show CardProgress;
import 'tools.dart' show purchaseState;
import 'treat_line.dart';
import 'words.dart' show naira, orderSteps;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int _int(Object? v) => (v as num?)?.toInt() ?? 0;

/// purchases.ts answersDelivery: delivered, and not yet said what came.
bool answersDelivery(Map<String, dynamic> p) =>
    (p['link_id'] as String? ?? '').isNotEmpty && p['seller_status'] == 'delivered' && (p['short_at'] as String? ?? '').isEmpty && p['status'] != 'cancelled';

/// tradeorders.ts buyerSteps: what the buyer may do with it now.
({bool confirmProposed, bool acceptChange, bool cancel}) buyerSteps(Map<String, dynamic> p) {
  final linked = (p['link_id'] as String? ?? '').isNotEmpty;
  final open = p['status'] == 'sent' || p['status'] == 'confirmed';
  final proposed = linked && p['status'] == 'drafted' && p['seller_status'] == 'proposed';
  return (
    confirmProposed: proposed,
    acceptChange: linked && open && p['seller_status'] == 'changed' && p['change'] != null,
    cancel: proposed || (linked && open && p['cancel_asked_at'] == null && p['seller_status'] != 'delivered'),
  );
}

/// tradeorders.ts sameName: the buyer's item of the same name, "" if none.
String sameName(List<Map<String, dynamic>> items, String name) {
  final k = name.trim().toLowerCase();
  return items.where((it) => (it['name'] as String? ?? '').trim().toLowerCase() == k).map((it) => it['id'] as String).firstOrNull ?? '';
}

const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

class PurchaseView extends StatefulWidget {
  const PurchaseView({super.key, required this.auth, required this.purchase, required this.onSaved, this.onReorder});
  final Auth auth;
  final Map<String, dynamic> purchase;
  final void Function(String words) onSaved;
  final void Function(Map<String, dynamic> purchase)? onReorder;

  @override
  State<PurchaseView> createState() => _PurchaseViewState();
}

enum _Mode { none, confirm, short }

class _PurchaseViewState extends State<PurchaseView> {
  Map<String, dynamic> get _p => widget.purchase;
  Transport get _t => widget.auth.transport;
  bool _busy = false;
  String? _error;
  _Mode _mode = _Mode.none;
  final Map<int, Treat> _chosen = {};
  late final TreatChoices _choices = TreatChoices(widget.auth);
  late final List<TextEditingController> _counts = [
    for (final l in _maps(_p['lines'])) TextEditingController(text: '${_int(l['delivered'] ?? l['quantity'])}'),
  ];

  @override
  void initState() {
    super.initState();
    for (final c in _counts) {
      c.addListener(() => setState(() {}));
    }
    _choices.addListener(_changed);
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _choices.removeListener(_changed);
    for (final c in _counts) {
      c.dispose();
    }
    super.dispose();
  }

  String get _ref => _p['reference'] as String? ?? '';
  String get _supplier => _p['supplier'] as String? ?? '';

  Future<void> _act(String what, String words, [Object? body]) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await _t.send('POST', '/v1/stock/purchases/${Uri.encodeComponent(_p['id'] as String)}/$what', body: body ?? const {});
      widget.onSaved(words);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  Future<void> _confirmMode() async {
    setState(() {
      _mode = _Mode.confirm;
      _error = null;
    });
    if (!_choices.ready) await _choices.load();
  }

  /// Confirms their quote with each line as asked; tracking many asks first.
  Future<void> _confirm(List<Map<String, dynamic>> lines, {Set<int> many = const {}}) async {
    setState(() => (_busy = true, _error = null));
    try {
      final body = await confirmLines(_choices, lines, _chosen);
      for (final i in many) {
        body[i] = {...body[i] ?? Treat.ofLine(lines[i], _choices).fields(), 'track_many': true};
      }
      await _t.send('POST', '/v1/stock/purchases/${Uri.encodeComponent(_p['id'] as String)}/confirm-proposed', body: {'lines': body});
      widget.onSaved('Confirmed $_ref');
    } on ApiError catch (e) {
      final at = _int(e.body['line']);
      final code = treatCode(e);
      if (code == 'track_ask' && at < lines.length && mounted) {
        setState(() => _busy = false);
        final yes = await askTrackMany(context, e);
        if (yes == null || !mounted) return;
        if (!yes) setState(() => _chosen[at] = (_chosen[at] ?? Treat.ofLine(lines[at], _choices)).copyWith(how: 'record'));
        return _confirm(lines, many: {...many, if (yes) at});
      }
      if (code == 'track_too_many' && at < lines.length) _chosen[at] = (_chosen[at] ?? Treat.ofLine(lines[at], _choices)).copyWith(how: 'record');
      if (mounted) setState(() => (_error = code == null ? e.message : treatRefusal(e), _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final can = widget.auth.can;
    final manage = can('storefront.manage');
    final receive = can('table.edit');
    final st = purchaseState(_p);
    final steps = buyerSteps(_p);
    final delivery = answersDelivery(_p) && _p['status'] != 'received';
    final change = _maps(_p['change']);
    final lines = steps.acceptChange && change.isNotEmpty ? change : _maps(_p['lines']);
    final total = steps.acceptChange && change.isNotEmpty
        ? change.fold<int>(0, (n, l) => n + _int(l['quantity']) * _int(l['cost_kobo']))
        : _int(_p['total_kobo']);
    final ordered = _maps(_p['lines']);
    final delivered = DateTime.tryParse(_p['delivered_at'] as String? ?? '')?.toLocal();
    Widget note(String s) => Text(s, style: tkText(size: 14, color: t.muted));
    final back = TkGhostButton(label: 'Not now', onTap: _busy ? null : () => setState(() => (_mode = _Mode.none, _error = null)));
    Widget cta(String label, VoidCallback? onTap) => TkCta(label: label, onTap: onTap);

    final buttons = <Widget>[
      if (_mode == _Mode.confirm) ...[
        back,
        cta('Confirm the order', _busy || !_choices.ready || [for (var i = 0; i < ordered.length; i++) _chosen[i] ?? Treat.ofLine(ordered[i], _choices)].any((x) => !x.ready)
            ? null
            : () => _confirm(ordered)),
      ] else if (_mode == _Mode.short) ...[
        back,
        cta('Tell $_supplier', _busy || _counts.any((c) => c.text.isEmpty)
            ? null
            : () => _act('not-all-came', 'Told $_supplier what came of $_ref', {'counts': [for (final c in _counts) int.parse(c.text)]})),
      ] else ...[
        if (manage && steps.confirmProposed) cta('Confirm their quote', _busy ? null : _confirmMode),
        if (manage && steps.acceptChange) cta('Accept the change', _busy ? null : () => _act('accept-change', 'Accepted the change to $_ref')),
        if (receive && delivery) ...[
          TkGhostButton(label: 'Not all came', onTap: _busy ? null : () => setState(() => _mode = _Mode.short)),
          cta('All came', _busy ? null : () => _act('all-came', 'All of $_ref came')),
        ],
        if (manage && steps.cancel && !delivery)
          TkGhostButton(
            label: steps.confirmProposed ? 'Decline' : 'Cancel',
            onTap: _busy
                ? null
                : () async {
                    final yes = await showTkConfirm(context, title: 'Cancel $_ref?', body: '$_supplier is told.', ok: 'Cancel the order', danger: true);
                    if (yes) await _act('cancel', 'Cancelled $_ref');
                  },
          ),
        if (widget.onReorder != null && !delivery)
          steps.confirmProposed || steps.acceptChange
              ? TkGhostButton(label: 'Reorder', onTap: () => widget.onReorder!(_p))
              : cta('Reorder', () => widget.onReorder!(_p)),
      ],
    ];

    return TkSheetStep(
      open: _mode != _Mode.none,
      onBack: () => setState(() => (_mode = _Mode.none, _error = null)),
      child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: TkFormBody(
            children: [
              Row(
                children: [
                  Expanded(child: Text(_supplier, style: tkText(size: 14, color: t.muted))),
                  const SizedBox(width: 8),
                  TkPill(st.word, tone: st.tone),
                ],
              ),
              if (st.step != null) CardProgress(steps: orderSteps, step: st.step!),
              if (steps.acceptChange)
                note('$_supplier changed it${(_p['counter'] as String? ?? '').isNotEmpty ? ': “${_p['counter']}”' : ''}. It comes to ${naira(total)} now, from ${naira(_int(_p['total_kobo']))}.'),
              if (_mode == _Mode.none)
                TkLines([
                  for (final l in lines)
                    ('${_int(l['quantity'])} × ${l['item'] ?? ''}', _int(l['cost_kobo']) != 0 ? naira(_int(l['quantity']) * _int(l['cost_kobo'])) : 'No price'),
                  ('Total', naira(total)),
                ]),
              if (_mode == _Mode.confirm)
                if (!_choices.ready)
                  const TkLoading(rows: 2, label: 'Loading your stock items and labels…')
                else
                  for (var i = 0; i < ordered.length; i++)
                    TreatQuestion(
                      key: ValueKey('confirm:$i'),
                      item: ordered[i]['item'] as String? ?? '',
                      supplier: _supplier,
                      quantity: _int(ordered[i]['quantity']),
                      unit: ordered[i]['unit'] as String?,
                      value: _chosen[i] ?? Treat.ofLine(ordered[i], _choices),
                      choices: _choices,
                      asked: ordered[i]['unconfirmed'] == true,
                      onChanged: _busy ? null : (v) => setState(() => _chosen[i] = v),
                    ),
              if (_mode == _Mode.short) ...[
                note('How many of each came? $_supplier is told, and agrees the count or talks to you.'),
                for (var i = 0; i < ordered.length; i++)
                  TkKitField(
                    label: '${ordered[i]['item'] ?? ''}: of ${_int(ordered[i]['quantity'])}',
                    child: TkTextBox(controller: _counts[i], height: 50, size: 16, hint: '0', digits: 6),
                  ),
              ],
              if ((_p['seller_note'] as String? ?? '').isNotEmpty && _mode == _Mode.none) note('Their note: “${_p['seller_note']}”'),
              if (delivered != null && _mode == _Mode.none) note('Delivered ${delivered.day} ${_months[delivered.month - 1]}'),
              if (_error != null) TkErrorBox(_error!),
            ],
          ),
        ),
        if (buttons.isNotEmpty)
          TkSheetFoot(
            // `.tk-foot-two`: side by side, as the web; with large text one
            // under another, the whole width, so each keeps its words.
            child: buttons.length > 1 && MediaQuery.textScalerOf(context).scale(10) > 15
                ? Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(height: 10), buttons[i]],
                    ],
                  )
                : Row(
                    children: [
                      for (var i = 0; i < buttons.length; i++) ...[
                        if (i > 0) const SizedBox(width: 10),
                        Expanded(child: buttons[i]),
                      ],
                    ],
                  ),
          ),
      ],
    ),
    );
  }
}
