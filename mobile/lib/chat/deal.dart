// A deal on its card (web/src/pages/ChatDeal.tsx, lib/deals.ts; kit.css
// .tk-deal, .tk-deal-form, .tk-deal-row): the back and forth so far and
// when the offer lapses; and the answers. The seller, on the buyer's offer
// or counter: Accept (for those who set prices, keeping the price for this
// buyer a while), Counter with a total, Decline. The buyer, on the seller's
// counter: Accept it, Counter back, or Cancel the order. Staff accepting
// below the lowest price wait for the owner.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart';
import 'words.dart';

/// deals.ts cardSide: whose card it is (the seller's order, the buyer's
/// purchase).
String cardSide(String doc) => doc == 'purchase' ? 'buyer' : 'seller';

/// Conversation.tsx CardActions: the side that answers a deal on this
/// card, or null when it is not a deal to answer.
String? dealSide(LinkDocCard c) => switch ('${c.doc}:${c.event}') {
  'order:offered' || 'order:countered' => 'seller',
  'purchase:change' when c.wasKobo != null => 'buyer',
  _ => null,
};

/// deals.ts dealOpen: not lapsed.
bool dealOpen(LinkDocCardDeal? d, DateTime now) => d?.until == null || d!.until!.isAfter(now);

/// deals.ts KEEP_CHOICES.
const keepChoices = [(0, 'This order only'), (7, '7 days'), (14, '14 days'), (30, '30 days')];

/// deals.ts dealKobo: naira as people type them, "760k", "1.2m",
/// "₦760,000"; null when it is not one.
int? dealKobo(String text) {
  final t = text.trim().toLowerCase().replaceAll(RegExp(r'[₦,\s]'), '').replaceFirst(RegExp(r'^n(?=\d)'), '');
  final m = RegExp(r'^(\d+(?:\.\d+)?)([km]?)$').firstMatch(t);
  if (m == null) return null;
  final num = m[1]!, suffix = m[2]!;
  if (suffix.isEmpty && !RegExp(r'^\d+(\.\d{1,2})?$').hasMatch(num)) return null;
  final mult = suffix == 'k' ? 1000 : (suffix == 'm' ? 1000000 : 1);
  return (double.parse(num) * mult * 100).round();
}

/// deals.ts offWords: "6% off" (nothing when not under).
String offWords(int amount, int? was) {
  if (was == null || amount >= was) return '';
  final pct = ((was - amount) * 100 / was).round();
  return pct > 0 ? '$pct% off' : 'under';
}

/// deals.ts stepWords: "They offered ₦740,000", "Tunde countered ₦770,000".
String stepWords(LinkDocCardDealStepsItem s, int index, String mine) {
  final who = s.by == mine ? (s.who ?? 'You') : 'They';
  return '$who ${index == 0 ? 'offered' : 'countered'} ${naira(s.totalKobo)}';
}

/// deals.ts lapseWords: "Lapses in 47 hours", "Lapses in 20 minutes",
/// "Lapsed".
String lapseWords(DateTime? until, DateTime now) {
  if (until == null) return '';
  final ms = until.difference(now).inMilliseconds;
  if (ms <= 0) return 'Lapsed';
  final h = ms ~/ 3600000;
  if (h >= 1) return 'Lapses in $h ${h == 1 ? 'hour' : 'hours'}';
  final m = (ms ~/ 60000).clamp(1, 59);
  return 'Lapses in $m ${m == 1 ? 'minute' : 'minutes'}';
}

/// ChatDeal.tsx DealHistory: the last four steps, newest last (ours in
/// pine), then when it lapses.
class DealHistory extends StatelessWidget {
  const DealHistory({super.key, required this.card, required this.now});
  final LinkDocCard card;
  final DateTime now;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final deal = card.deal!;
    final side = cardSide(card.doc);
    final lapse = lapseWords(deal.until, now);
    final steps = deal.steps.length > 4 ? deal.steps.sublist(deal.steps.length - 4) : deal.steps;
    final skipped = deal.steps.length - steps.length;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (deal.steps.length > 1)
          Semantics(
            container: true,
            label: 'How ${card.reference} was agreed so far',
            explicitChildNodes: true,
            child: Container(
              // `.tk-deal-history { padding-left: 12px; border-left: 2px solid; gap: 3px }`
              padding: const EdgeInsets.only(left: 12),
              decoration: BoxDecoration(border: Border(left: BorderSide(color: t.border, width: 2))),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (var i = 0; i < steps.length; i++) ...[
                    if (i > 0) const SizedBox(height: 3),
                    Text(
                      stepWords(steps[i], skipped + i, side),
                      style: i == steps.length - 1
                          ? tkText(size: 13, weight: 700, color: t.text)
                          : tkText(size: 13, color: steps[i].by == side ? t.tkPineInk : t.muted),
                    ),
                  ],
                ],
              ),
            ),
          ),
        if (lapse.isNotEmpty) ...[
          if (deal.steps.length > 1) const SizedBox(height: 6),
          Text(
            lapse == 'Lapsed' ? 'This offer lapsed: nothing to answer' : lapse,
            style: lapse == 'Lapsed' ? tkText(size: 12, weight: 600, color: t.danger) : tkText(size: 12, color: t.muted),
          ),
        ],
      ],
    );
  }
}

/// ChatDeal.tsx DealActions.
class DealActions extends StatefulWidget {
  const DealActions({super.key, required this.card, required this.side, required this.acts, required this.now});
  final LinkDocCard card;

  /// "seller" or "buyer".
  final String side;
  final CardActs acts;
  final DateTime now;

  @override
  State<DealActions> createState() => _DealActionsState();
}

class _DealActionsState extends State<DealActions> {
  final _text = TextEditingController();
  final _note = TextEditingController();
  bool _countering = false;
  int _keep = 0;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _text.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _text.dispose();
    _note.dispose();
    super.dispose();
  }

  bool get _seller => widget.side == 'seller';
  String get _id => Uri.encodeComponent(widget.card.id ?? widget.card.reference);
  int get _amount => widget.card.amountKobo ?? 0;

  /// What is wrong with the counter, else "": above what they said, under
  /// the price.
  String _problem(int? kobo) {
    if (_text.text.trim().isEmpty) return '';
    if (kobo == null || kobo <= 0) return 'A price in naira, like 770,000 or 770k.';
    if (_seller && kobo <= _amount) return 'A counter is more than their ${naira(_amount)}.';
    if (!_seller && kobo >= _amount) return 'A counter is under their ${naira(_amount)}.';
    return '';
  }

  Future<void> _run(Future<String?> Function() f) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final said = await f();
      if (said != null) widget.acts.said?.call(said);
      try {
        await widget.acts.onDone();
      } on Object {
        // Asked again on the next turn.
      }
    } on ApiError catch (e) {
      _error = e.message;
    } on Offline {
      _error = 'You’re offline. Try again when you’re back online.';
    }
    if (mounted) setState(() => _busy = false);
  }

  Transport get _t => widget.acts.auth.transport;
  String get _ref => widget.card.reference;

  void _accept() => _run(() async {
    if (_seller) {
      final out = await _t.send('POST', '/v1/trade-orders/$_id/accept', body: _keep > 0 ? {'keep_days': _keep} : const {});
      if (out is Map && out['waiting'] == 'owner') return 'Under your lowest price: $_ref waits for the owner\'s OK';
      return _keep > 0 ? 'Agreed ${naira(_amount)}, kept for them $_keep days' : 'Agreed ${naira(_amount)} on $_ref';
    }
    await _t.send('POST', '/v1/stock/purchases/$_id/accept-change', body: const {});
    return 'Agreed ${naira(_amount)} on $_ref';
  });

  void _counter(int kobo) => _run(() async {
    await _t.send('POST', _seller ? '/v1/trade-orders/$_id/change' : '/v1/stock/purchases/$_id/counter', body: {
      'total_kobo': kobo,
      if (_note.text.trim().isNotEmpty) 'note': _note.text.trim(),
    });
    _countering = false;
    _text.clear();
    _note.clear();
    return 'Counter sent: ${naira(kobo)}';
  });

  Future<void> _decline() async {
    final yes = await (widget.acts.ask?.call(_seller ? 'Decline $_ref?' : 'Cancel $_ref?', _seller ? 'Decline' : 'Cancel the order') ?? Future.value(true));
    if (!yes) return;
    await _run(() async {
      await _t.send('POST', _seller ? '/v1/trade-orders/$_id/decline' : '/v1/stock/purchases/$_id/cancel', body: const {});
      return _seller ? '$_ref declined' : '$_ref cancelled';
    });
  }

  Future<void> _pickKeep() async {
    final days = await showTkModal<int>(
      context,
      title: 'Keep this price for them',
      builder: (context) => Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final (d, label) in keepChoices)
            Semantics(
              button: true,
              selected: d == _keep,
              label: label,
              onTap: () => Navigator.of(context).pop(d),
              excludeSemantics: true,
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: () => Navigator.of(context).pop(d),
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  child: Text(label, style: tkText(size: 16, weight: d == _keep ? 700 : 500, color: TkTheme.tokens(context).text)),
                ),
              ),
            ),
        ],
      ),
    );
    if (days != null && mounted) setState(() => _keep = days);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final can = widget.acts.auth.can;
    final mayAct = _seller ? can('storefront.manage|order.handle') : can('storefront.manage');
    final mayCounter = _seller ? can('storefront.manage|order.handle') : can('table.edit');
    final setsPrices = _seller && can('storefront.manage');
    if (!mayAct && !mayCounter) return const SizedBox.shrink();
    // Lapsed: nothing to answer (the server refuses it too).
    if (!dealOpen(widget.card.deal, widget.now)) return const SizedBox.shrink();
    final kobo = dealKobo(_text.text);
    final problem = _problem(kobo);
    final ref = widget.card.reference;
    final was = widget.card.wasKobo;
    final buttons = <(String label, String semantics, bool primary, VoidCallback? onTap)>[
      if (_countering) ...[
        ('Send counter', 'Send counter $ref', true, _busy || kobo == null || kobo <= 0 || problem.isNotEmpty ? null : () => _counter(kobo)),
        ('Back', 'Back', false, _busy ? null : () => setState(() => _countering = false)),
      ] else ...[
        if (mayAct) ('Accept ${naira(_amount)}', 'Accept ${naira(_amount)} $ref', true, _busy ? null : _accept),
        if (mayCounter) ('Counter', 'Counter $ref', false, _busy ? null : () => setState(() => _countering = true)),
        if (mayAct) (_seller ? 'Decline' : 'Cancel', '${_seller ? 'Decline' : 'Cancel'} $ref', false, _busy ? null : _decline),
      ],
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (!_countering && setsPrices) ...[
          TkKitField(
            field: false,
            label: 'Keep this price for them',
            child: Semantics(
              button: true,
              label: 'Keep this price for them: ${keepChoices.firstWhere((c) => c.$1 == _keep).$2}',
              onTap: _pickKeep,
              excludeSemantics: true,
              child: GestureDetector(
                onTap: _pickKeep,
                child: Container(
                  height: 44,
                  padding: const EdgeInsets.symmetric(horizontal: 16),
                  alignment: Alignment.centerLeft,
                  decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(14)),
                  child: Text(keepChoices.firstWhere((c) => c.$1 == _keep).$2, style: tkText(size: 16, weight: 600, color: t.text)),
                ),
              ),
            ),
          ),
          const SizedBox(height: 8),
        ],
        if (_countering) ...[
          TkKitField(
            label: _seller ? 'Your price for the whole order' : 'Your counter',
            error: problem.isEmpty ? null : problem,
            hint: kobo != null && kobo > 0 && problem.isEmpty && was != null
                ? '${offWords(kobo, was).isEmpty ? 'Their full price' : offWords(kobo, was)}${_seller ? ' your price' : ' their price'}'
                : null,
            child: TkTextBox(controller: _text, height: 44, size: 16, money: true, hint: _seller ? '770,000' : '760,000'),
          ),
          const SizedBox(height: 8),
          TkKitField(
            label: 'A word with it (optional)',
            child: TkTextBox(controller: _note, height: 44, size: 16, hint: 'Best I can do'),
          ),
          const SizedBox(height: 8),
        ],
        _FlexRow(
          children: [
            for (final (label, semantics, primary, onTap) in buttons)
              (label, CardButton(label: label, primary: primary, busy: _busy, semantics: semantics, onTap: onTap)),
          ],
        ),
        if (_error != null) ...[const SizedBox(height: 8), TkErrorBox(_error!)],
      ],
    );
  }
}

/// `.tk-deal-row`: buttons 8 px apart, each `flex: 1 1 0` and never
/// narrower than its words; what does not fit goes to the next line.
class _FlexRow extends StatelessWidget {
  const _FlexRow({required this.children});
  final List<(String, Widget)> children;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, box) {
        const gap = 8.0;
        // A button's own width: its words (14 px, 700) and 16 px each side.
        double own(String label) {
          final p = TextPainter(text: TextSpan(text: label, style: tkText(size: 14, weight: 700)), textDirection: TextDirection.ltr)..layout();
          return p.width + 32 + 2;
        }

        final lines = <List<(String, Widget, double)>>[[]];
        var used = 0.0;
        for (final (label, w) in children) {
          final o = own(label);
          final need = lines.last.isEmpty ? o : used + gap + o;
          if (lines.last.isNotEmpty && need > box.maxWidth) {
            lines.add([]);
            used = 0;
          }
          used = lines.last.isEmpty ? o : used + gap + o;
          lines.last.add((label, w, o));
        }
        Widget line(List<(String, Widget, double)> items) {
          // flex: 1 1 0 with each at least its own width: an equal share,
          // those wider than it at their own, the rest sharing what is left.
          var room = box.maxWidth - gap * (items.length - 1);
          final fixed = <int>{};
          while (true) {
            final free = items.length - fixed.length;
            if (free == 0) break;
            final share = room / free;
            var changed = false;
            for (var i = 0; i < items.length; i++) {
              if (!fixed.contains(i) && items[i].$3 > share) {
                fixed.add(i);
                room -= items[i].$3;
                changed = true;
              }
            }
            if (!changed) break;
          }
          final free = items.length - fixed.length;
          final share = free == 0 ? 0.0 : room / free;
          return Row(
            children: [
              for (var i = 0; i < items.length; i++) ...[
                if (i > 0) const SizedBox(width: gap),
                SizedBox(width: fixed.contains(i) ? items[i].$3 : share, child: items[i].$2),
              ],
            ],
          );
        }

        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (var i = 0; i < lines.length; i++) ...[if (i > 0) const SizedBox(height: gap), line(lines[i])],
          ],
        );
      },
    );
  }
}
