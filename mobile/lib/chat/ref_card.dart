// A thing tagged in a message, as a card (web/src/pages/ChatRefCard.tsx,
// lib/refs.ts; docs/spec/chat-five-more-flows.md, phase 2 additions): an
// invoice, a bill, an order, an order to a supplier or a customer, read
// now, so a payment made since shows as paid. Its one action (send, remind,
// message) and "Give this to…" work from it; Open and Pay lead to the tools'
// screens (M5 to M8) and come with them.

import 'dart:math' as math;

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'cards.dart' show CardPill;
import 'give_task.dart';
import 'words.dart';

// --- lib/refs.ts, lib/mentions.ts ---

/// What each kind is called, on a row's badge and a card's kicker.
const kindWord = {'invoice': 'Invoice', 'bill': 'Bill', 'order': 'Order', 'purchase': 'Your order', 'customer': 'Customer'};

const _refStatus = {
  'sent': 'sent',
  'open': 'to pay',
  'part_paid': 'part-paid',
  'paid': 'paid',
  'overdue': 'overdue',
  'void': 'cancelled',
  'disputed': 'questioned',
  'paying': 'being paid',
  'received': 'new',
  'changed': 'changed',
  'accepted': 'accepted',
  'dispatched': 'on the way',
  'delivered': 'delivered',
  'declined': 'declined',
  'cancelled': 'cancelled',
  'confirmed': 'confirmed',
  'part_received': 'part received',
};

/// mentions.ts refStatusWords: an invoice's or an order's status, as a
/// reference shows it.
String refStatusWords(String s) => _refStatus[s] ?? s.replaceAll('_', ' ');

const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// refs.ts shortDate: "9 Nov", or "9 Nov 2025" in another year.
String shortDate(DateTime d, DateTime now) {
  final l = d.toLocal();
  return '${l.day} ${_months[l.month - 1]}${l.year != now.toLocal().year ? ' ${l.year}' : ''}';
}

/// refs.ts cardPill: where it stands, and its tone.
({String text, CardTone tone})? cardPill(ChatCard c) {
  final s = c.status;
  if (s == null || s.isEmpty) return null;
  final words = refStatusWords(s);
  final word = words[0].toUpperCase() + words.substring(1);
  if (const {'paid', 'delivered', 'completed', 'received'}.contains(s)) return (text: word, tone: CardTone.lime);
  if (const {'overdue', 'expired', 'declined', 'cancelled', 'void'}.contains(s)) return (text: word, tone: s == 'overdue' ? CardTone.danger : CardTone.plain);
  if ((c.dueKobo ?? 0) > 0 || const {'part_paid', 'disputed', 'awaiting_payment'}.contains(s)) return (text: word, tone: CardTone.warn);
  return (text: word, tone: CardTone.plain);
}

/// refs.ts paidShare: how much of it is paid, 0 to 1, for the bar.
double paidShare(ChatCard c) {
  final total = c.totalKobo ?? 0;
  if (total <= 0) return 0;
  return math.min(1, math.max(0, (c.paidKobo ?? 0) / total));
}

/// refs.ts ACTION_WORD.
const actionWord = {'send': 'Send it', 'remind': 'Send a reminder', 'pay': 'Pay', 'message': 'Message them'};

/// refs.ts docMentions: the invoices, bills and orders a business
/// conversation's message names, once each, three at most.
List<({String kind, String name})> docMentions(List<LinkMention>? mentions) {
  final out = <({String kind, String name})>[];
  for (final m in mentions ?? const <LinkMention>[]) {
    if (const {'invoice', 'bill', 'order', 'purchase'}.contains(m.kind) && !out.any((x) => x.name == m.name)) out.add((kind: m.kind, name: m.name));
  }
  return out.take(3).toList();
}

/// refs.ts KnownRef: a thing a message may name with "#", to show as a
/// chip and a card.
typedef KnownRef = ({String kind, String id, String name});

/// refs.ts refParts: a message's words in parts, each "#name" of a known
/// thing marked (the longest name first, so "#Rice 50kg" is not "#Rice").
List<({String text, KnownRef? ref})> refParts(String text, List<KnownRef> known) {
  if (known.isEmpty || !text.contains('#')) return [(text: text, ref: null)];
  final lower = text.toLowerCase();
  final byLength = [...known]..sort((a, b) => b.name.length.compareTo(a.name.length));
  final found = <({int at, int len, KnownRef ref})>[];
  final wordChar = RegExp(r'[\p{L}\p{N}-]', unicode: true);
  for (final r in byLength) {
    final token = '#${r.name.toLowerCase()}';
    for (var from = 0;;) {
      final at = lower.indexOf(token, from);
      if (at < 0) break;
      final end = at + token.length;
      // The name ends where a word ends.
      final boundary = end == lower.length || !wordChar.hasMatch(lower[end]);
      if (boundary && !found.any((f) => at < f.at + f.len && f.at < end)) found.add((at: at, len: token.length, ref: r));
      from = end;
    }
  }
  found.sort((a, b) => a.at.compareTo(b.at));
  final out = <({String text, KnownRef? ref})>[];
  var pos = 0;
  for (final f in found) {
    if (f.at > pos) out.add((text: text.substring(pos, f.at), ref: null));
    out.add((text: text.substring(f.at, f.at + f.len), ref: f.ref));
    pos = f.at + f.len;
  }
  if (pos < text.length) out.add((text: text.substring(pos), ref: null));
  return out;
}

/// refs.ts cardRefs: the things a message names that have cards, once
/// each, three at most.
List<KnownRef> cardRefs(String text, List<KnownRef> known) {
  final out = <KnownRef>[];
  for (final p in refParts(text, known)) {
    final r = p.ref;
    if (r != null && r.kind != 'product' && !out.any((x) => x.kind == r.kind && x.id == r.id)) out.add(r);
  }
  return out.take(3).toList();
}

// --- the card ---

/// ChatRefCard.tsx RefCard: read from [load], live: again once acted on.
class RefCard extends StatefulWidget {
  const RefCard({super.key, required this.load, required this.auth, required this.now, this.onMessage});

  /// GET /v1/chat-card or /v1/link-chats/{link}/card.
  final Future<ChatCard> Function() load;
  final Auth auth;
  final DateTime now;

  /// A customer's "Message them": their conversation.
  final ValueChanged<String>? onMessage;

  @override
  State<RefCard> createState() => _RefCardState();
}

class _RefCardState extends State<RefCard> {
  ChatCard? _c;
  bool _gone = false;
  bool _busy = false;
  int _tick = 0;
  final _key = 'card-${DateTime.now().microsecondsSinceEpoch}-${math.Random().nextInt(1 << 30)}';

  @override
  void initState() {
    super.initState();
    _read();
  }

  Future<void> _read() async {
    try {
      final c = await widget.load();
      if (mounted) setState(() => _c = c);
    } on Object {
      // Not one this person may see, or gone: the words stay as they are.
      if (mounted) setState(() => _gone = true);
    }
  }

  Future<void> _act(ChatCard c) async {
    if (c.action == 'message') {
      widget.onMessage?.call(c.id);
      return;
    }
    if (c.action == 'pay') {
      // Paying is the bill's screen: coming with the tools.
      showComingSoon(context);
      return;
    }
    setState(() => _busy = true);
    try {
      await widget.auth.transport.send('POST', '/v1/invoices/${Uri.encodeComponent(c.id)}/send', body: const {}, headers: {'Idempotency-Key': '$_key:$_tick'});
      if (mounted) showTkToast(context, c.action == 'remind' ? 'Reminder sent for ${c.name}' : '${c.name} sent');
      _tick++;
      await _read();
    } on ApiError catch (e) {
      if (mounted) showTkToast(context, e.message, error: true);
    } on Offline {
      if (mounted) showTkToast(context, 'That didn\'t go through. Try again.', error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_gone) return const SizedBox.shrink();
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final c = _c;
    // `.tk-ref { background: var(--panel); border-radius: 14px; margin: 6px 0;
    // box-shadow: 0 0 0 1px var(--border); max-width: 360px; width: 100% }`
    Widget frame(Widget child, {String? label}) => Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Align(
        alignment: Alignment.centerLeft,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 360),
          child: Semantics(
            container: true,
            label: label,
            child: Container(
              width: double.infinity,
              clipBehavior: Clip.antiAlias,
              decoration: BoxDecoration(color: t.panel, borderRadius: BorderRadius.circular(14), border: Border.all(color: t.border)),
              child: child,
            ),
          ),
        ),
      ),
    );
    if (c == null) {
      // `.tk-ref-loading`: two bars.
      Widget bar(double f) => FractionallySizedBox(
        widthFactor: f,
        alignment: Alignment.centerLeft,
        child: Container(height: 12, decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(6))),
      );
      return frame(
        Semantics(
          label: 'Loading…',
          child: Padding(
            padding: const EdgeInsets.all(14),
            child: Column(mainAxisSize: MainAxisSize.min, children: [bar(1), const SizedBox(height: 8), bar(0.6)]),
          ),
        ),
      );
    }
    final kind = kindWord[c.kind] ?? c.kind;
    final label = '$kind ${c.name}';
    final pill = cardPill(c);
    final (tileBg, tileInk) = switch (c.kind) {
      'bill' || 'purchase' => (t.tkBlueBg, t.tkBlueInk),
      'customer' => (t.tkSoft, t.text),
      _ => (t.tkLimeSoft, dark ? t.brandDark : t.brand),
    };
    final icon = switch (c.kind) {
      'invoice' => 'invoices',
      'bill' => 'money',
      'customer' => 'members',
      _ => 'orders',
    };
    final can = widget.auth.can;
    final giving = c.kind != 'customer' && can('member.manage|invoice.manage');
    final action = c.action;
    final acts = <Widget>[
      // Open shows it where it is worked on, a tool's screen: coming.
      if (c.kind != 'customer') _Btn(label: 'Open', named: label, onTap: () => showComingSoon(context)),
      if (giving)
        _Btn(label: 'Give this to…', named: label, onTap: () => giveThis(context, widget.auth, (kind: c.kind, id: c.id, name: c.name))),
      if (action != null && (action != 'message' || widget.onMessage != null))
        _Btn(label: actionWord[action] ?? action, named: label, primary: true, onTap: _busy ? null : () => _act(c)),
    ];
    final share = paidShare(c);
    final sub = [?c.party, c.kind == 'customer' ? 'last wrote ${shortDate(c.when, widget.now)}' : shortDate(c.when, widget.now)].where((s) => s.isNotEmpty).join(' · ');
    return frame(
      label: label,
      Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // `.tk-ref-top`
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 8),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Container(
                  width: 44,
                  height: 44,
                  alignment: Alignment.center,
                  decoration: BoxDecoration(color: tileBg, borderRadius: BorderRadius.circular(12)),
                  child: TkIcon(icon, size: 22, color: tileInk),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(kind.toUpperCase(), style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.muted)),
                      const SizedBox(height: 2),
                      Text(c.name, style: tkText(size: 16, weight: 700, color: t.text)),
                      if (sub.isNotEmpty) ...[const SizedBox(height: 2), Text(sub, style: tkText(size: 13, color: t.muted))],
                    ],
                  ),
                ),
                if (pill != null) ...[const SizedBox(width: 12), CardPill(pill.text, pill.tone)],
              ],
            ),
          ),
          if (c.kind == 'customer')
            _Stats(c: c, now: widget.now)
          else ...[
            // `.tk-ref-money`
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
              child: Wrap(
                spacing: 6,
                crossAxisAlignment: WrapCrossAlignment.end,
                children: [
                  Text(
                    naira((c.dueKobo ?? 0) > 0 ? c.dueKobo! : (c.totalKobo ?? 0)),
                    style: tkText(size: 22, weight: 800, color: t.text).copyWith(fontFeatures: const [FontFeature.tabularFigures()]),
                  ),
                  Padding(
                    padding: const EdgeInsets.only(bottom: 4),
                    child: Text(
                      (c.dueKobo ?? 0) > 0 ? 'to pay of ${naira(c.totalKobo ?? 0)}${c.dueOn != null ? ' · due ${shortDate(DateTime.parse(c.dueOn!), widget.now)}' : ''}' : 'total',
                      style: tkText(size: 13, color: t.muted),
                    ),
                  ),
                ],
              ),
            ),
            if ((c.paidKobo ?? 0) > 0 && (c.totalKobo ?? 0) > 0)
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
                child: Row(
                  children: [
                    Expanded(
                      child: ClipRRect(
                        borderRadius: BorderRadius.circular(3),
                        child: SizedBox(
                          height: 6,
                          child: Stack(
                            children: [
                              Positioned.fill(child: ColoredBox(color: t.tkSoft)),
                              FractionallySizedBox(widthFactor: share, child: Container(decoration: BoxDecoration(color: t.brand, borderRadius: BorderRadius.circular(3)))),
                            ],
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Text('${naira(c.paidKobo!)} paid', style: tkText(size: 13, color: t.muted)),
                  ],
                ),
              ),
            if ((c.lines ?? const []).isNotEmpty)
              Container(
                padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
                decoration: BoxDecoration(border: Border(top: BorderSide(color: t.border))),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    for (final (i, l) in c.lines!.indexed) ...[
                      if (i > 0) const SizedBox(height: 4),
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Expanded(child: Text('${l.quantity} × ${l.name}', style: tkText(size: 14, color: t.text))),
                          const SizedBox(width: 12),
                          Text(naira(l.totalKobo), style: tkText(size: 14, weight: 600, color: t.text)),
                        ],
                      ),
                    ],
                    if ((c.more ?? 0) > 0) ...[
                      const SizedBox(height: 4),
                      Text('and ${c.more} more ${c.more == 1 ? 'line' : 'lines'}', style: tkText(size: 13, color: t.muted)),
                    ],
                  ],
                ),
              ),
          ],
          if (acts.isNotEmpty)
            // `.tk-ref-foot`
            Container(
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
              decoration: BoxDecoration(border: Border(top: BorderSide(color: t.border))),
              child: Wrap(alignment: WrapAlignment.end, spacing: 8, runSpacing: 8, children: acts),
            ),
        ],
      ),
    );
  }
}

/// `.tk-ref-stats`: a customer's orders, what they owe, their last order.
class _Stats extends StatelessWidget {
  const _Stats({required this.c, required this.now});
  final ChatCard c;
  final DateTime now;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    Widget cell(String label, Widget value, {String? when}) => Container(
      padding: const EdgeInsets.fromLTRB(10, 8, 10, 8),
      decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(10)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: tkText(size: 12, color: t.muted)),
          const SizedBox(height: 2),
          value,
          if (when != null) ...[const SizedBox(height: 2), Text(when, style: tkText(size: 12, color: t.muted))],
        ],
      ),
    );
    final owes = c.owesKobo;
    final last = c.lastOrder;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 0, 12, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          IntrinsicHeight(
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Expanded(child: cell('Orders', Text('${c.orders ?? 0}', style: tkText(size: 15, weight: 700, color: t.text)))),
                if (owes != null) ...[
                  const SizedBox(width: 8),
                  Expanded(child: cell('Owes', Text(naira(owes), style: tkText(size: 15, weight: 700, color: owes > 0 ? t.tkWarnInk : t.text)))),
                ] else
                  const Expanded(child: SizedBox.shrink()),
              ],
            ),
          ),
          if (last != null) ...[
            const SizedBox(height: 8),
            cell('Last order', Text('${last.reference} · ${naira(last.totalKobo)}', style: tkText(size: 15, weight: 700, color: t.text)), when: shortDate(last.when, now)),
          ],
        ],
      ),
    );
  }
}

/// `.tk-ref-foot .tk-btn`: 40 px, 0 16 px, 14 px words; soft or primary.
class _Btn extends StatelessWidget {
  const _Btn({required this.label, required this.named, required this.onTap, this.primary = false});
  final String label;
  final String named;
  final VoidCallback? onTap;
  final bool primary;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = primary ? (dark ? (t.brandDark, t.brandDarkText) : (t.brand, const Color(0xFFFFFFFF))) : (t.tkSoft, dark ? t.text : t.brand);
    return Semantics(
      button: true,
      enabled: onTap != null,
      label: '$label $named',
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Opacity(
          opacity: onTap == null ? 0.45 : 1,
          child: Container(
            constraints: const BoxConstraints(minHeight: 40),
            padding: const EdgeInsets.symmetric(horizontal: 16),
            alignment: Alignment.center,
            decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
            child: Text(label, style: tkText(size: 14, weight: 700, lineHeight: 1.2, color: ink)),
          ),
        ),
      ),
    );
  }
}
