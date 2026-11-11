// What needs the person, at the end of Taskiem's conversation
// (docs/spec/chat-tools.md §6; web/src/pages/ChatAlerts.tsx, ChatStaff.tsx
// BriefingCard; kit.css .tk-brief, .tk-alert, .tk-approve): the day's
// briefing on pine, then a card for each thing to act on, with its next
// step and Later, and approvals with Decline and Approve.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/sheet.dart';
import 'approve.dart';
import '../theme/theme.dart';
import 'cards.dart' show CardPill;
import 'give_task.dart';
import 'words.dart';

// --- words (web/src/lib/alerts.ts, lib/approvals.ts, lib/staff.ts) ---

/// alerts.ts ALERT_LOOK: each kind's icon and tile.
const alertLook = <String, ({String icon, CardTone tone})>{
  'low_stock': (icon: 'warn', tone: CardTone.warn),
  'overdue': (icon: 'invoices', tone: CardTone.warn),
  'new_order': (icon: 'orders', tone: CardTone.lime),
  'rider': (icon: 'store', tone: CardTone.plain),
  'confirm': (icon: 'money', tone: CardTone.lime),
  'unmatched': (icon: 'money', tone: CardTone.plain),
  'approval': (icon: 'approvals', tone: CardTone.plain),
  'deal': (icon: 'money', tone: CardTone.warn),
  'order_draft': (icon: 'chat', tone: CardTone.lime),
};

/// alerts.ts ago: "Just now", "3 min ago", "2 h ago", "Yesterday", "9 Nov".
String ago(DateTime at, DateTime now) {
  final s = now.difference(at).inSeconds.clamp(0, 1 << 31);
  if (s < 60) return 'Just now';
  if (s < 3600) return '${s ~/ 60} min ago';
  if (s < 86400) return '${s ~/ 3600} h ago';
  if (s < 2 * 86400) return 'Yesterday';
  const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  final l = at.toLocal();
  return '${l.day} ${months[l.month - 1]}';
}

/// approvals.ts subjectWords: the amount, who it goes to and the account,
/// when the approval's subject names them.
({String? amount, String? to, String? account, List<(String, String)> rest}) subjectWords(Object? subject) {
  if (subject is! Map) return (amount: null, to: null, account: null, rest: const []);
  final s = subject.cast<String, Object?>();
  String? first(List<String> keys, bool Function(Object?) ok) {
    for (final k in keys) {
      if (s.containsKey(k) && ok(s[k])) return k;
    }
    return null;
  }

  final amountKey =
      first(['amount_kobo', 'total_kobo', 'amount', 'total'], (v) => v is num) ?? s.keys.where((k) => k.endsWith('_kobo') && s[k] is num).firstOrNull;
  String? amount;
  if (amountKey != null) {
    final v = s[amountKey]! as num;
    amount = amountKey.endsWith('_kobo') ? naira(v.toInt()) : _grouped(v);
  }
  final toKey = first(['to', 'payee', 'recipient', 'beneficiary', 'account_name', 'supplier', 'customer', 'name'], (v) => v is String && v.trim().isNotEmpty);
  final bankKey = first(['bank', 'bank_name'], (v) => v is String && v.trim().isNotEmpty);
  final accKey = first(['account_number', 'account', 'nuban'], (v) => (v is String || v is num) && '$v'.replaceAll(RegExp(r'\D'), '').length >= 4);
  String? account;
  if (accKey != null) {
    final digits = '${s[accKey]}'.replaceAll(RegExp(r'\D'), '');
    account = '${bankKey != null ? '${s[bankKey]} ' : ''}••${digits.substring(digits.length - 4)}';
  }
  // Every other plain field, as label and value.
  final used = {?amountKey, ?toKey, ?accKey, if (accKey != null && bankKey != null) bankKey};
  final rest = <(String, String)>[
    for (final e in s.entries)
      if (!used.contains(e.key) && (e.value is String || e.value is num || e.value is bool)) (fieldLabel(e.key), fieldValue(e.key, e.value!)),
  ];
  return (amount: amount, to: toKey == null ? null : s[toKey] as String, account: account, rest: rest);
}

/// approvals.ts fieldLabel: "amount_kobo" is "Amount", "due_date" "Due date".
String fieldLabel(String key) {
  final words = key.replaceAll(RegExp(r'_kobo$'), '').replaceAll(RegExp(r'[_-]+'), ' ').trim();
  return words.isEmpty ? key : words[0].toUpperCase() + words.substring(1);
}

/// approvals.ts fieldValue: kobo as naira, numbers grouped, yes and no.
String fieldValue(String key, Object v) {
  if (v is num && key.endsWith('_kobo')) return naira(v.toInt());
  if (v is num) return _grouped(v);
  if (v is bool) return v ? 'Yes' : 'No';
  return '$v';
}

String _grouped(num v) => v.toString().replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',');

/// staff.ts firstName: the greeting's name, the first name, else the email's start.
String firstName(String? name, String? email) {
  final n = (name ?? '').trim().split(RegExp(r'\s+')).first;
  return n.isNotEmpty ? n : (email ?? '').split('@').first;
}

// --- the briefing ---

/// ChatStaff.tsx BriefingCard: on pine, from 8am: yesterday's sales and the
/// week's bars, what is owed, low and due today; Got it puts it away.
class BriefingCard extends StatefulWidget {
  const BriefingCard({super.key, required this.briefing, required this.auth, this.onAsk, this.onGone});
  final GetChatBriefingResponse briefing;
  final Auth auth;

  /// "How did we do yesterday", asked from the sales.
  final ValueChanged<String>? onAsk;

  /// Put away for the day.
  final VoidCallback? onGone;

  @override
  State<BriefingCard> createState() => _BriefingCardState();
}

class _BriefingCardState extends State<BriefingCard> {
  bool _gone = false;
  bool _failed = false;

  Future<void> _gotIt() async {
    setState(() {
      _gone = true;
      _failed = false;
    });
    try {
      await widget.auth.transport.send('POST', '/v1/chat-alerts/later', body: {'key': widget.briefing.key, 'hours': 24});
      widget.onGone?.call();
    } on Object {
      // Not put away: the card comes back, saying so.
      if (mounted) {
        setState(() {
          _gone = false;
          _failed = true;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final b = widget.briefing;
    if (!b.ready || b.putOff || _gone) return const SizedBox.shrink();
    final t = TkTheme.tokens(context);
    final on = t.tkOnPine;
    Color mix(double p) => cssMix(on, const Color(0x00000000), p);
    final day = DateTime.parse('${b.day}T12:00:00');
    const weekdays = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'];
    final hello = switch (b.part) {
      'afternoon' => 'Good afternoon',
      'evening' => 'Good evening',
      _ => 'Good morning',
    };
    final me = widget.auth.me;
    final sales = b.sales;
    final change = sales == null ? null : changeWords(sales.changePct as num?, 'last ${sales.weekday ?? ''}');
    final week = sales?.week ?? const <DaySales>[];
    final heights = barHeights([for (final d in week) d.kobo]);
    Widget tile(String label, String value) => Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(color: mix(0.09), borderRadius: BorderRadius.circular(14)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: tkText(size: 12, color: mix(0.72))),
          const SizedBox(height: 2),
          Text(value, style: tkText(size: 15, weight: 700, color: on)),
        ],
      ),
    );
    final tiles = <Widget>[
      if (b.owedKobo != null) tile('Owed to you', naira(b.owedKobo!)),
      if (b.low != null) tile('Low stock', b.low == 1 ? '1 item' : '${b.low} items'),
      if (b.deliveries != null) tile('Deliveries', '${b.deliveries} today'),
      if (b.staff case final n?) tile('Staff', staffTile(n.in_.length, n.late.length)),
      if (b.tasks case final n?) tile('Tasks', tasksTile(n.today, n.late)),
      if (b.bookings != null && b.bookings! > 0) tile('Bookings', '${b.bookings} today'),
    ];
    final parts = <Widget>[
      Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '$hello, ${firstName(me?.user?.name, me?.user?.email)}'.toUpperCase(),
                  style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.brandDark),
                ),
                const SizedBox(height: 2),
                Text('${weekdays[day.weekday - 1]}${b.now.isNotEmpty ? ' · ${b.now}' : ''}', style: tkText(size: 14, color: mix(0.72))),
              ],
            ),
          ),
          const SizedBox(width: 12),
          // Live: today's numbers.
          Container(
            margin: const EdgeInsets.only(top: 6, right: 4),
            width: 10,
            height: 10,
            decoration: BoxDecoration(color: t.brandDark, shape: BoxShape.circle),
          ),
        ],
      ),
      if (sales != null)
        Semantics(
          button: true,
          label: 'Sales yesterday ${naira(sales.yesterdayKobo ?? 0)}: ask how we did yesterday',
          excludeSemantics: true,
          child: TkPress(
            onTap: () => widget.onAsk?.call('how did we do yesterday'),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('Sales yesterday', style: tkText(size: 14, color: mix(0.8))),
                      const SizedBox(height: 2),
                      Text(naira(sales.yesterdayKobo ?? 0), style: tkText(size: 34, weight: 800, letterSpacing: -0.68, color: on)),
                      if (change != null) ...[const SizedBox(height: 2), Text(change, style: tkText(size: 14, weight: 700, color: t.brandDark))],
                    ],
                  ),
                ),
                const SizedBox(width: 12),
                SizedBox(
                  height: 58,
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.end,
                    children: [
                      for (var i = 0; i < week.length; i++) ...[
                        if (i > 0) const SizedBox(width: 5),
                        Container(
                          width: 9,
                          height: (heights[i] * 100).round() / 100 * 58,
                          decoration: BoxDecoration(
                            color: i == week.length - 1 ? t.brandDark : cssMix(t.brandDark, const Color(0x00000000), 0.45),
                            borderRadius: BorderRadius.circular(3),
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      if (tiles.isNotEmpty) _AutoFitGrid(min: 96, gap: 8, children: tiles),
      Semantics(
        button: true,
        label: 'Got it: put the briefing away for today',
        excludeSemantics: true,
        // chat.css `.tk-brief .tk-brief-ok`: plain words, 40 px tall, 10 px
        // below the tiles (over kit.css's pill).
        child: TkPress(
          onTap: _gotIt,
          child: Container(
            margin: const EdgeInsets.only(top: 10),
            constraints: const BoxConstraints(minHeight: 40),
            alignment: Alignment.center,
            child: Text('Got it', style: tkText(size: 13, weight: 600, lineHeight: 1.2, color: mix(0.78))),
          ),
        ),
      ),
      if (_failed)
        Semantics(
          liveRegion: true,
          child: Text('That didn’t go through. Try Got it again.', style: tkText(size: 13, weight: 600, color: on)),
        ),
    ];
    return Align(
      alignment: Alignment.centerLeft,
      child: Semantics(
        container: true,
        label: 'Today’s briefing',
        child: Container(
          constraints: const BoxConstraints(maxWidth: 480),
          padding: const EdgeInsets.all(18),
          decoration: BoxDecoration(
            color: t.brand,
            borderRadius: BorderRadius.circular(22),
            boxShadow: [BoxShadow(color: cssMix(t.brand, const Color(0x00000000), 0.6), offset: const Offset(0, 20), blurRadius: 40, spreadRadius: -18)],
          ),
          child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: _gaps(parts, 14)),
        ),
      ),
    );
  }
}

List<Widget> _gaps(List<Widget> list, double gap) => [
  for (var i = 0; i < list.length; i++) ...[if (i > 0) SizedBox(height: gap), list[i]],
];

/// `grid-template-columns: repeat(auto-fit, minmax(min, 1fr))`: as many
/// equal columns as fit at [min] wide each, filled in rows.
class _AutoFitGrid extends StatelessWidget {
  const _AutoFitGrid({required this.min, required this.gap, required this.children});
  final double min;
  final double gap;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, box) {
        var cols = ((box.maxWidth + gap) / (min + gap)).floor().clamp(1, 1 << 10);
        // auto-fit: empty tracks collapse, so the cards share the row.
        if (cols > children.length) cols = children.length;
        final w = (box.maxWidth - gap * (cols - 1)) / cols;
        final rows = <Widget>[];
        for (var i = 0; i < children.length; i += cols) {
          final row = children.sublist(i, (i + cols).clamp(0, children.length));
          rows.add(
            IntrinsicHeight(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  for (var j = 0; j < row.length; j++) ...[if (j > 0) SizedBox(width: gap), SizedBox(width: w, child: row[j])],
                ],
              ),
            ),
          );
        }
        return Column(crossAxisAlignment: CrossAxisAlignment.start, children: _gaps(rows, gap));
      },
    );
  }
}

// --- the cards ---

/// What a card's step does when it opens something not yet in the app (a
/// tool's sheet, the approval's sheet): told to the screen.
typedef AlertOpen = void Function(String what, ChatAlert a);

/// ChatAlerts.tsx AlertCards: the cards in their order, each going once
/// dealt with.
class AlertCards extends StatefulWidget {
  const AlertCards({super.key, required this.alerts, required this.auth, required this.now, this.onChanged, this.onOpen});
  final List<ChatAlert> alerts;
  final Auth auth;
  final DateTime now;

  /// Something was dealt with: the list, and the conversation, again.
  final VoidCallback? onChanged;
  final AlertOpen? onOpen;

  @override
  State<AlertCards> createState() => _AlertCardsState();
}

class _AlertCardsState extends State<AlertCards> {
  final _gone = <String>{};

  void _done(ChatAlert a) {
    setState(() => _gone.add(a.key));
    widget.onChanged?.call();
  }

  @override
  Widget build(BuildContext context) {
    final shown = widget.alerts.where((a) => !_gone.contains(a.key)).toList();
    if (shown.isEmpty) return const SizedBox.shrink();
    return Align(
      alignment: Alignment.centerLeft,
      child: Semantics(
        container: true,
        label: 'Needs you',
        child: Container(
          // `.tk-alerts { margin: 4px 0 8px; gap: 12px; max-width: 480px }`
          constraints: const BoxConstraints(maxWidth: 480),
          margin: const EdgeInsets.only(top: 4, bottom: 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: _gaps([
              for (final a in shown)
                a.kind == 'approval'
                    ? _ApprovalCard(
                        a: a,
                        now: widget.now,
                        onDecide: (d) async {
                          final decided = await showTkSheet<bool>(context, (_) => ApproveSheet(a: a, decision: d, auth: widget.auth));
                          if (decided == true) _done(a);
                        },
                      )
                    : _AlertCard(a: a, auth: widget.auth, onDone: () => _done(a), onOpen: widget.onOpen),
            ], 12),
          ),
        ),
      ),
    );
  }
}

BoxDecoration _cardLook(TkTokens t, bool dark) => BoxDecoration(
  color: t.panel,
  borderRadius: BorderRadius.circular(22),
  border: dark ? Border.all(color: t.border) : null,
  boxShadow: dark ? null : [BoxShadow(color: cssMix(t.brand, const Color(0x00000000), 0.45), offset: const Offset(0, 10), blurRadius: 30, spreadRadius: -18)],
);

/// `.tk-alert-go`, `.tk-alert-soft`, `.tk-approve-go`: the cards' 52 px pills.
class _Pill extends StatelessWidget {
  const _Pill({required this.label, required this.named, required this.onTap, this.kind = 'go', this.icon});
  final String label;

  /// The card it is for, said after the label ("Later: Rice is running low").
  final String named;
  final VoidCallback? onTap;
  final String kind;
  final String? icon;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = switch (kind) {
      'soft' => (t.tkSoft, t.text),
      'approve' => (t.brandDark, t.brand),
      _ => dark ? (t.brandDark, t.brandDarkText) : (t.brand, t.tkOnPine),
    };
    return Semantics(
      button: true,
      enabled: onTap != null,
      label: '$label: $named',
      excludeSemantics: true,
      child: TkPress(
        scale: 0.97,
        onTap: onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 52),
          padding: EdgeInsets.symmetric(horizontal: kind == 'go' ? 16 : (kind == 'soft' ? 22 : 18)),
          alignment: Alignment.center,
          decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (icon != null) ...[TkIcon(icon!, size: 20, color: ink), const SizedBox(width: 8)],
              Flexible(child: Text(label, textAlign: TextAlign.center, style: tkText(size: 15, weight: 700, lineHeight: 1.2, color: ink))),
            ],
          ),
        ),
      ),
    );
  }
}

/// team.ts staffTile: who is in, and who is late.
String staffTile(int inNow, int late) => ['$inNow in', if (late > 0) '$late late'].join(' · ');

/// tasks.ts tasksTile: tasks due today, and late.
String tasksTile(int today, int late) {
  final w = [if (today > 0) '$today due today', if (late > 0) '$late late'].join(' · ');
  return w.isEmpty ? 'None due' : w;
}

/// `.tk-alert-acts { display: flex; flex-wrap: wrap; gap: 10px }`: the
/// next step (`.tk-alert-go { flex: 1; min-width: min(100%, 180px) }`)
/// then the soft pills, each as wide as its words; a line that is full
/// wraps, and the step grows into the room left on its line.
class _Acts extends StatelessWidget {
  const _Acts({required this.pills});
  final List<({Widget pill, String words, bool grow})> pills;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, box) {
        final room = box.maxWidth;
        double base(({Widget pill, String words, bool grow}) p) {
          final w = (TextPainter(
                text: TextSpan(text: p.words, style: tkText(size: 15, weight: 700, lineHeight: 1.2)),
                textDirection: TextDirection.ltr,
              )..layout()).width +
              (p.grow ? 32 : 44);
          return (p.grow ? (w < 180 ? 180.0 : w) : w).clamp(0.0, room);
        }

        final lines = <List<(int, double)>>[[]];
        var used = 0.0;
        for (var i = 0; i < pills.length; i++) {
          final w = base(pills[i]);
          final gap = lines.last.isEmpty ? 0.0 : 10.0;
          if (lines.last.isNotEmpty && used + gap + w > room) {
            lines.add([]);
            used = 0;
          }
          used += (lines.last.isEmpty ? 0.0 : 10.0) + w;
          lines.last.add((i, w));
        }
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final (n, line) in lines.indexed) ...[
              if (n > 0) const SizedBox(height: 10),
              Row(
                children: [
                  for (final (k, (i, w)) in line.indexed) ...[
                    if (k > 0) const SizedBox(width: 10),
                    if (pills[i].grow) Expanded(child: pills[i].pill) else SizedBox(width: w, child: pills[i].pill),
                  ],
                ],
              ),
            ],
          ],
        );
      },
    );
  }
}

/// ChatAlerts.tsx AlertCard: what it is, and its next step with Later.
class _AlertCard extends StatefulWidget {
  const _AlertCard({required this.a, required this.auth, required this.onDone, this.onOpen});
  final ChatAlert a;
  final Auth auth;
  final VoidCallback onDone;
  final AlertOpen? onOpen;

  @override
  State<_AlertCard> createState() => _AlertCardState();
}

class _AlertCardState extends State<_AlertCard> {
  bool _busy = false;
  String? _error;

  Future<void> _run(Future<void> Function() f) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await f();
      widget.onDone();
    } on ApiError catch (e) {
      _error = e.message;
    } on Offline {
      _error = 'You’re offline. Try again when you’re back online.';
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _post(String path, [Object? body]) => widget.auth.transport.send('POST', path, body: body ?? const {});

  /// The card's next step, by its kind and what the person may do.
  ({String words, VoidCallback go})? _step() {
    final a = widget.a;
    final can = widget.auth.can;
    final id = Uri.encodeComponent(a.id ?? '');
    final open = widget.onOpen;
    switch (a.kind) {
      case 'low_stock':
        final r = a.reorder;
        if (r != null) return (words: 'Reorder ${r.quantity} from ${r.supplier}', go: () => open?.call('reorder', a));
        return (words: can('table.edit') ? 'Record stock in' : 'Open stock', go: () => open?.call('stock', a));
      case 'overdue':
        if (!can('invoice.manage')) return null;
        // Sent: the card is put off for a day, not shown again at once.
        return (
          words: 'Send reminder',
          go: () => _run(() async {
            await _post('/v1/invoices/$id/send');
            await _post('/v1/chat-alerts/later', {'key': a.key, 'hours': 24});
          }),
        );
      case 'new_order':
        return (words: 'Review order', go: () => open?.call('orders', a));
      case 'deal':
        if (!can('storefront.manage')) return null;
        return (words: 'Agree${a.amountKobo != null ? ' ${naira(a.amountKobo!)}' : ''}', go: () => _run(() => _post('/v1/trade-orders/$id/accept')));
      case 'rider':
        return (words: 'Assign rider', go: () => open?.call('rider', a));
      case 'confirm':
        if (!can('storefront.manage')) return null;
        return (words: 'Confirm, pay the fee', go: () => _run(() => _post('/v1/storefront/orders/${Uri.encodeComponent(a.reference ?? '')}/confirm')));
      case 'order_draft':
        return (words: 'Open the order', go: () => open?.call('customer', a));
      case 'unmatched':
        return can('invoice.manage') ? (words: 'Match it', go: () => open?.call('match', a)) : null;
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final a = widget.a;
    final look = alertLook[a.kind] ?? (icon: 'info', tone: CardTone.plain);
    final (tileBg, tileInk) = switch (look.tone) {
      CardTone.warn => (t.tkWarnBg, t.tkWarnInk),
      CardTone.lime => (t.tkLimeSoft, dark ? t.brandDark : t.brand),
      _ => (t.tkSoft, t.text),
    };
    final step = _step();
    // ChatAlerts.tsx: "Give this to…" a low stock item, for those who give tasks.
    final can = widget.auth.can;
    final give = a.kind == 'low_stock' && (a.id ?? '').isNotEmpty && can('member.manage|invoice.manage')
        ? () => giveThis(context, widget.auth, (kind: 'stock', id: a.id!, name: a.title.replaceFirst(RegExp(r' is running low$'), '')))
        : null;
    return Semantics(
      container: true,
      label: a.title,
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: _cardLook(t, dark),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Container(
                  width: 44,
                  height: 44,
                  decoration: BoxDecoration(color: tileBg, borderRadius: BorderRadius.circular(12)),
                  alignment: Alignment.center,
                  child: TkIcon(look.icon, size: 22, color: tileInk),
                ),
                const SizedBox(width: 14),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(a.title, style: tkText(size: 16, weight: 700, color: t.text)),
                      const SizedBox(height: 2),
                      Text(a.line, style: tkText(size: 14, color: t.muted)),
                    ],
                  ),
                ),
              ],
            ),
            const SizedBox(height: 14),
            _Acts(
              pills: [
                if (step != null) (pill: _Pill(label: step.words, named: a.title, onTap: _busy ? null : step.go), words: step.words, grow: true),
                if (give != null) (pill: _Pill(label: 'Give this to…', named: a.title, kind: 'soft', onTap: _busy ? null : give), words: 'Give this to…', grow: false),
                (
                  pill: _Pill(
                    label: 'Later',
                    named: a.title,
                    kind: 'soft',
                    onTap: _busy ? null : () => _run(() => _post('/v1/chat-alerts/later', {'key': a.key})),
                  ),
                  words: 'Later',
                  grow: false,
                ),
              ],
            ),
            if (_error != null) TkErrorBox(_error!),
          ],
        ),
      ),
    );
  }
}

/// ChatAlerts.tsx ApprovalCard: who asks for what, the amount and to whom,
/// what else to know, Decline and Approve.
class _ApprovalCard extends StatelessWidget {
  const _ApprovalCard({required this.a, required this.now, required this.onDecide});
  final ChatAlert a;
  final DateTime now;
  final ValueChanged<String> onDecide;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final ap = a.approval;
    if (ap == null) return const SizedBox.shrink();
    final w = subjectWords(ap.subject);
    final stepUp = ap.stepUp as String?;
    final notes = <Widget>[
      if (ap.firstPayment == true) const CardPill('First payment to this account', CardTone.warn),
      if (stepUp != null) CardPill(stepUp == 'passkey' ? 'Needs your passkey' : 'Needs your passkey or a code', CardTone.plain),
      if (ap.levels > 1) CardPill('Level ${ap.level} of ${ap.levels}', CardTone.plain),
      if (ap.required > 1) CardPill('${ap.approvals} of ${ap.required} approvals', CardTone.plain),
    ];
    final parts = <Widget>[
      Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(child: Text('NEEDS YOUR OK', style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.muted))),
          Text(ago(a.at, now), style: tkText(size: 13, color: t.muted)),
        ],
      ),
      Text(a.who != null ? '${a.who} asks: ${a.title}' : a.title, style: tkText(size: 16, color: t.text)),
      if (w.amount != null || w.to != null || w.account != null)
        Wrap(
          alignment: WrapAlignment.spaceBetween,
          crossAxisAlignment: WrapCrossAlignment.end,
          spacing: 12,
          runSpacing: 4,
          children: [
            if (w.amount != null) Text(w.amount!, style: tkText(size: 30, weight: 800, letterSpacing: -0.6, color: t.text)),
            if (w.to != null || w.account != null)
              Text(
                [if (w.to != null) 'to ${w.to}', ?w.account].join('\n'),
                textAlign: TextAlign.right,
                style: tkText(size: 14, color: t.muted),
              ),
          ],
        ),
      if (notes.isNotEmpty) Wrap(spacing: 6, runSpacing: 6, children: notes),
      // `.tk-approve-acts`: Decline `flex: 1`, Approve `flex: 1.4`, each
      // first as wide as its words, the room left shared 1 to 1.4.
      LayoutBuilder(
        builder: (context, box) {
          double words(String s) => (TextPainter(
            text: TextSpan(text: s, style: tkText(size: 15, weight: 700, lineHeight: 1.2)),
            textDirection: TextDirection.ltr,
          )..layout()).width;
          final no = words('Decline') + 36, yes = 20 + 8 + words('Approve') + 36;
          final room = (box.maxWidth - 10 - no - yes).clamp(0.0, double.infinity);
          return Row(
            children: [
              SizedBox(width: no + room / 2.4, child: _Pill(label: 'Decline', named: a.title, kind: 'soft', onTap: () => onDecide('rejected'))),
              const SizedBox(width: 10),
              SizedBox(width: yes + room * 1.4 / 2.4, child: _Pill(label: 'Approve', named: a.title, kind: 'approve', icon: 'fingerprint', onTap: () => onDecide('approved'))),
            ],
          );
        },
      ),
    ];
    return Semantics(
      container: true,
      label: 'Needs your OK: ${a.title}',
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: _cardLook(t, dark),
        child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: _gaps(parts, 14)),
      ),
    );
  }
}
