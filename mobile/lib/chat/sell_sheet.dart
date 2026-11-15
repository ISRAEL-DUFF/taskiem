// Staff who work from chat (M6 part S; web/src/pages/ChatStaff.tsx
// SellSheet, HandoverSheet, SizedRow; lib/staff.ts): a counter sale as the
// order sheet looks (the items with steppers, the pine bag, then how it
// was paid), recorded as the Stock page's sales are (POST /v1/stock/sales);
// and the shift handed over: what was sold, the cash counted, a note.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/motion.dart';
import '../kit/forms.dart' show CloseSoon, errorWords, nairaKobo;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/sheet.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'order_sheet.dart' show BagBar, QtyStepper;
import 'sizes_forms.dart' show choicesOf, keyOf, sizesOf, variantLabel, variantOf, Variant;
import 'product_form.dart' show hasSizes, sizesWords;
import 'words.dart' show naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int? _int(Object? v) => (v as num?)?.toInt();

/// staff.ts saleOf: the sale's lines, its total, and how many things.
({List<({Map<String, dynamic> item, int quantity})> lines, int total, String count}) saleOf(List<Map<String, dynamic>> items, Map<String, int> qty) {
  final lines = [
    for (final it in items)
      if ((qty[it['id']] ?? 0) > 0) (item: it, quantity: qty[it['id']]!),
  ];
  final total = lines.fold<int>(0, (n, l) => n + l.quantity * (_int(l.item['price_kobo']) ?? 0));
  final things = lines.fold<int>(0, (n, l) => n + l.quantity);
  return (lines: lines, total: total, count: things == 0 ? 'Nothing yet' : (things == 1 ? '1 thing' : '$things things'));
}

/// staff.ts cashWords: "As sold", "₦500 short", "₦200 over".
String cashWords(int counted, int sold) => counted == sold ? 'As sold' : (counted < sold ? '${naira(sold - counted)} short' : '${naira(counted - sold)} over');

/// variants.ts pickable: a choice's values still to be picked, given the
/// others picked: on sale, with some left (or no count kept).
Set<String> pickable(List<Map<String, dynamic>> items, String product, String choice, Variant picked) {
  final out = <String>{};
  for (final v in sizesOf(items, product)) {
    final vv = variantOf(v);
    final n = _int(v['stock']);
    if (vv == null || (n != null && n <= 0)) continue;
    final fits = picked.entries.every((e) => e.key == choice || vv[e.key] == e.value);
    final x = vv[choice];
    if (fits && x != null && x.isNotEmpty) out.add(x);
  }
  return out;
}

/// variants.ts findSize: the size a pick names, when on sale.
Map<String, dynamic>? findSize(List<Map<String, dynamic>> items, String product, Variant picked) {
  final k = keyOf(picked);
  return sizesOf(items, product).where((v) => variantOf(v) != null && keyOf(variantOf(v)!) == k).firstOrNull;
}

const _hows = [('cash', 'Cash'), ('transfer', 'Transfer'), ('pos', 'POS')];

/// Opens Sell; the future ends when it closes.
Future<void> showSellSheet(BuildContext context, {required Auth auth, VoidCallback? onSold}) =>
    showTkSheet<void>(context, (_) => SellSheet(auth: auth, onSold: onSold));

class SellSheet extends StatefulWidget {
  const SellSheet({super.key, required this.auth, this.onSold});
  final Auth auth;
  final VoidCallback? onSold;

  @override
  State<SellSheet> createState() => _SellSheetState();
}

class _SellSheetState extends State<SellSheet> with CloseSoon {
  List<Map<String, dynamic>>? _items;
  String? _loadError;
  final _qty = <String, int>{};
  final _search = TextEditingController();
  bool _pay = false;
  String _how = 'cash';
  bool _busy = false;
  String? _error;
  String? _done;
  String? _open;
  final _picked = <String, Variant>{};

  /// One sale, one key: the same sale tried again is recorded once; a
  /// changed one (other things, or paid another way) is new.
  final _keys = <String, String>{};

  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _search.addListener(() => setState(() {}));
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await _t.send('GET', '/v1/stock') as Map<String, dynamic>;
      _items = _maps(r['items']).where((x) => x['price_kobo'] != null && x['available'] == true).toList();
    } on Object catch (e) {
      _loadError = errorWords(e);
    }
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  void _set(Map<String, dynamic> it, int n) => setState(() => _qty[it['id'] as String] = n < 0 ? 0 : n);

  Future<void> _record() async {
    final sale = saleOf(_items ?? const [], _qty);
    final sig = '${[for (final l in sale.lines) '${l.item['id']}:${l.quantity}']}|$_how';
    final key = _keys[sig] ??= 'sell-${uuid4()}';
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await _t.send(
        'POST',
        '/v1/stock/sales',
        body: {
          'lines': [
            for (final l in sale.lines) {'item_id': l.item['id'], 'quantity': l.quantity},
          ],
          'paid_how': _how,
        },
        headers: {'Idempotency-Key': key},
      );
      widget.onSold?.call();
      if (!mounted) return;
      setState(() => _done = 'Sold: ${naira(sale.total)}');
      closeSoon();
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final items = _items;
    final sale = saleOf(items ?? const [], _qty);
    return TkSheet(
      title: _pay ? 'How was it paid?' : 'Sell',
      onBack: _pay ? () => setState(() => _pay = false) : null,
      bareFooter: !_pay,
      overlay: _done == null ? null : TkSavedTick(words: _done!),
      footer: !_pay
          ? BagBar(
              count: sale.count,
              total: naira(sale.total),
              go: 'Next',
              goIcon: 'chevron',
              onGo: sale.lines.isEmpty ? null : () => setState(() => _pay = true),
            )
          : Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.baseline,
                  textBaseline: TextBaseline.alphabetic,
                  children: [
                    Expanded(
                      child: Text('Total', style: tkText(size: 15, color: t.text)),
                    ),
                    Text(naira(sale.total), style: tkText(size: 22, weight: 800, color: t.text)),
                  ],
                ),
                const SizedBox(height: 12),
                TkCta(label: _busy ? 'Recording…' : 'Record the sale', onTap: _busy ? null : _record),
              ],
            ),
      body: items == null
          ? ListView(
              children: [
                if (_loadError != null)
                  Padding(padding: const EdgeInsets.symmetric(horizontal: 20), child: TkErrorBox(_loadError!))
                else
                  const TkLoading(label: 'Loading what you sell…'),
              ],
            )
          : items.isEmpty
          ? ListView(
              children: const [TkEmpty(icon: 'box', title: 'Nothing with a price yet', line: 'Give your products a price to sell them here.')],
            )
          : !_pay
          ? _pick(t, items)
          : TkFormBody(
              children: [
                TkLines([for (final l in sale.lines) ('${l.quantity} × ${l.item['name']}', naira(l.quantity * (_int(l.item['price_kobo']) ?? 0)))]),
                // `.tk-hows`: three side by side, the one chosen in pine.
                Semantics(
                  container: true,
                  label: 'How it was paid',
                  explicitChildNodes: true,
                  child: Row(
                    children: [
                      for (var i = 0; i < _hows.length; i++) ...[
                        if (i > 0) const SizedBox(width: 8),
                        Expanded(
                          child: _How(words: _hows[i].$2, on: _how == _hows[i].$1, onTap: () => setState(() => _how = _hows[i].$1)),
                        ),
                      ],
                    ],
                  ),
                ),
                Text('The stock goes down, and the money is written in your money log.', style: tkFormNow(t)),
                if (_error != null) TkErrorBox(_error!),
              ],
            ),
    );
  }

  Widget _pick(TkTokens t, List<Map<String, dynamic>> items) {
    final q = _search.text.trim().toLowerCase();
    // A product's sizes and colours are picked under it, not listed apart.
    final shown = items.where((it) => it['variant_of'] == null && (q.isEmpty || (it['name'] as String? ?? '').toLowerCase().contains(q))).toList();
    return ListView(
      padding: const EdgeInsets.only(bottom: 96),
      children: [
        TkSearchBox(key: const ValueKey('search'), controller: _search, hint: 'Search what you sell'),
        // ChatStaff.tsx `.tk-order-list.tk-stagger`: rows follow the sheet up.
        for (final (i, it) in shown.indexed)
          Padding(
            key: ValueKey(it['id']),
            // `.tk-order-list { padding: 0 20px 96px }`.
            padding: const EdgeInsets.symmetric(horizontal: 20),
            child: TkIn.sheetRow(index: i, child: hasSizes(it) ? _sizedRow(t, it, items) : _row(t, it)),
          ),
      ],
    );
  }

  Widget _tile(TkTokens t) => ExcludeSemantics(
    child: Container(
      width: 52,
      height: 52,
      decoration: BoxDecoration(color: t.tkLimeSoft, borderRadius: BorderRadius.circular(14)),
      alignment: Alignment.center,
      child: TkIcon('box', size: 22, color: TkTheme.of(context).mode == TkMode.dark ? t.brandDark : t.brand),
    ),
  );

  Widget _row(TkTokens t, Map<String, dynamic> it) {
    final name = it['name'] as String? ?? '';
    final n = _qty[it['id']] ?? 0;
    final stock = _int(it['stock']);
    final out = stock != null && stock <= 0;
    final over = stock != null && n > (stock < 0 ? 0 : stock);
    final unit = it['unit'] as String? ?? '';
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 14),
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: t.border)),
      ),
      child: Row(
        children: [
          _tile(t),
          const SizedBox(width: 14),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(name, style: tkText(size: 16, weight: 600, color: t.text)),
                Text(
                  '${naira(_int(it['price_kobo']) ?? 0)}${unit.isNotEmpty ? ' a $unit' : ''}${stock != null ? ' · ${out ? 'none left' : '$stock left'}' : ''}',
                  style: tkText(size: 14, color: t.muted),
                ),
                if (over)
                  Semantics(
                    liveRegion: true,
                    child: Padding(
                      padding: const EdgeInsets.only(top: 2),
                      child: Text(out ? 'None left in your stock' : 'Only $stock left in your stock', style: tkText(size: 13, weight: 600, color: t.danger)),
                    ),
                  ),
              ],
            ),
          ),
          const SizedBox(width: 14),
          QtyStepper(name: name, n: n, on: n > 0, onLess: n == 0 ? null : () => _set(it, n - 1), onMore: () => _set(it, n + 1)),
        ],
      ),
    );
  }

  /// ChatStaff.tsx SizedRow: a product sold in sizes, its sizes chosen
  /// under it by their choices, each chosen with its stepper.
  Widget _sizedRow(TkTokens t, Map<String, dynamic> it, List<Map<String, dynamic>> all) {
    final id = it['id'] as String;
    final name = it['name'] as String? ?? '';
    final cs = choicesOf(it);
    final sizes = sizesOf(all, id);
    final chosen = sizes.where((v) => (_qty[v['id']] ?? 0) > 0).toList();
    final left = sizes.fold<int>(0, (a, v) => a + (_int(v['stock']) ?? 0));
    final picked = _picked[id] ?? const {};
    final found = cs.every((c) => picked[c.name] != null) ? findSize(all, id, picked) : null;
    final open = _open == id;
    Widget stepper(Map<String, dynamic> v) {
      final m = _qty[v['id']] ?? 0;
      return QtyStepper(
        name: '$name, ${variantLabel(variantOf(v), cs)}',
        n: m,
        on: m > 0,
        onLess: m == 0 ? null : () => _set(v, m - 1),
        onMore: () => _set(v, m + 1),
      );
    }

    final price = _int(it['price_kobo']);
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 14),
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: t.border)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              _tile(t),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(name, style: tkText(size: 16, weight: 600, color: t.text)),
                    Text('${price != null ? naira(price) : ''} · ${sizesWords(sizes.length)} · $left left', style: tkText(size: 14, color: t.muted)),
                  ],
                ),
              ),
              const SizedBox(width: 14),
              Semantics(
                expanded: open,
                child: TkSoftButton(
                  label: open ? 'Done' : 'Choose',
                  semanticsLabel: '${open ? 'Done' : 'Choose'} a size or colour of $name',
                  onTap: () => setState(() => _open = open ? null : id),
                ),
              ),
            ],
          ),
          if (chosen.isNotEmpty) ...[
            const SizedBox(height: 10),
            Padding(
              padding: const EdgeInsets.only(left: 56),
              child: Semantics(
                container: true,
                label: '$name chosen',
                explicitChildNodes: true,
                child: Column(
                  children: [
                    for (final v in chosen)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 6),
                        child: Row(
                          children: [
                            Expanded(
                              child: Text(
                                '${variantLabel(variantOf(v), cs)}${_int(v['price_kobo']) != null && _int(v['price_kobo']) != price ? ' · ${naira(_int(v['price_kobo'])!)}' : ''}',
                                style: tkText(size: 15, weight: 600, color: t.text),
                              ),
                            ),
                            stepper(v),
                          ],
                        ),
                      ),
                  ],
                ),
              ),
            ),
          ],
          if (open) ...[
            const SizedBox(height: 10),
            for (final c in cs)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Semantics(
                  container: true,
                  label: c.name,
                  explicitChildNodes: true,
                  child: Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      for (final x in c.values)
                        _PickChip(
                          label: x,
                          on: picked[c.name] == x,
                          enabled: pickable(all, id, c.name, picked).contains(x),
                          onTap: () => setState(() => _picked[id] = {...picked, c.name: x}),
                        ),
                    ],
                  ),
                ),
              ),
            if (found != null)
              Row(
                children: [
                  Expanded(
                    child: Text(
                      '${variantLabel(variantOf(found), cs)}${_int(found['stock']) != null ? ' · ${_int(found['stock'])} left' : ''}',
                      style: tkText(size: 15, weight: 600, color: t.text),
                    ),
                  ),
                  stepper(found),
                ],
              )
            else
              Text('Choose ${cs.map((c) => c.name.toLowerCase()).join(' and ')}.', style: tkHint(t)),
          ],
        ],
      ),
    );
  }
}

/// A value to pick (`.tk-sized-pick .tk-chips button`): pine when picked;
/// one with none left struck through.
class _PickChip extends StatelessWidget {
  const _PickChip({required this.label, required this.on, required this.enabled, required this.onTap});
  final String label;
  final bool on;
  final bool enabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = on ? (dark ? (t.brandDark, t.brandDarkText) : (t.brand, const Color(0xFFFFFFFF))) : (t.tkSoft, t.text);
    return TapArea(
      child: Semantics(
        button: true,
        selected: on,
        enabled: enabled,
        label: label,
        onTap: enabled ? onTap : null,
        excludeSemantics: true,
        child: GestureDetector(
          onTap: enabled ? onTap : null,
          child: Opacity(
            opacity: enabled ? 1 : 0.45,
            child: Container(
              constraints: const BoxConstraints(minHeight: 36),
              padding: const EdgeInsets.symmetric(horizontal: 14),
              decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    label,
                    style: tkText(size: 14, weight: 600, color: ink, decoration: enabled ? null : TextDecoration.lineThrough),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// `.tk-hows button`: 52 high, soft, the chosen one pine.
class _How extends StatelessWidget {
  const _How({required this.words, required this.on, required this.onTap});
  final String words;
  final bool on;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final (bg, ink) = on ? (dark ? (t.brandDark, t.brandDarkText) : (t.brand, t.tkOnPine)) : (t.tkSoft, t.text);
    return Semantics(
      inMutuallyExclusiveGroup: true,
      checked: on,
      label: words,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          constraints: const BoxConstraints(minHeight: 52),
          alignment: Alignment.center,
          decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(16)),
          child: Text(words, style: tkText(size: 15, weight: 700, color: ink)),
        ),
      ),
    );
  }
}

const _howWords = {'cash': 'Cash', 'transfer': 'Transfer', 'pos': 'POS', 'credit': 'On credit'};

/// Opens Handover.
Future<void> showHandoverSheet(BuildContext context, {required Auth auth, VoidCallback? onSent}) =>
    showTkSheet<void>(context, (_) => HandoverSheet(auth: auth, onSent: onSent));

/// ChatStaff.tsx HandoverSheet: the shift so far, the cash counted against
/// the cash sold, a note; told to those who run the business.
class HandoverSheet extends StatefulWidget {
  const HandoverSheet({super.key, required this.auth, this.onSent});
  final Auth auth;
  final VoidCallback? onSent;

  @override
  State<HandoverSheet> createState() => _HandoverSheetState();
}

class _HandoverSheetState extends State<HandoverSheet> with CloseSoon {
  Map<String, dynamic>? _h;
  String? _loadError;
  final _cash = TextEditingController();
  final _note = TextEditingController();
  bool _busy = false;
  String? _error;
  bool _done = false;

  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _cash.addListener(() => setState(() {}));
    _t
        .send('GET', '/v1/chat-handover')
        .then(
          (r) {
            if (mounted) setState(() => _h = r as Map<String, dynamic>?);
          },
          onError: (Object e) {
            if (mounted) setState(() => _loadError = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.');
          },
        );
  }

  @override
  void dispose() {
    _cash.dispose();
    _note.dispose();
    super.dispose();
  }

  /// The cash typed, in kobo; null when nothing is typed; -1 when it is
  /// not money.
  int? get _counted => _cash.text.trim().isEmpty ? null : (nairaKobo(_cash.text) ?? -1);

  Future<void> _send() async {
    final counted = _counted;
    setState(() => (_busy = true, _error = null));
    try {
      await _t.send('POST', '/v1/chat-handover', body: {if (counted != null && counted >= 0) 'cash_counted_kobo': counted, 'note': _note.text.trim()});
      widget.onSent?.call();
      if (!mounted) return;
      setState(() => _done = true);
      closeSoon();
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final h = _h;
    final counted = _counted;
    final sales = (h?['sales'] as Map?)?.cast<String, dynamic>();
    final byHow = (sales?['by_how'] as Map?)?.cast<String, dynamic>() ?? const {};
    final sold = _int(byHow['cash']) ?? 0;
    final deliveries = (h?['deliveries'] as Map?)?.cast<String, dynamic>();
    final low = ((h?['low'] as List?) ?? const []).whereType<String>().toList();
    return TkSheet(
      title: 'Hand the shift over',
      overlay: _done ? const TkSavedTick(words: 'Handed over') : null,
      footer: TkCta(label: _busy ? 'Sending…' : 'Hand over', onTap: _busy || h == null || counted == -1 ? null : _send),
      body: h == null
          ? ListView(
              children: [
                if (_loadError != null)
                  Padding(padding: const EdgeInsets.symmetric(horizontal: 20), child: TkErrorBox(_loadError!))
                else
                  const TkLoading(rows: 3, label: 'Adding up the shift…'),
              ],
            )
          : TkFormBody(
              children: [
                if (sales != null)
                  // `.tk-bill-hero`: pine, the money in large.
                  Container(
                    padding: const EdgeInsets.all(20),
                    decoration: BoxDecoration(color: t.brand, borderRadius: BorderRadius.circular(22)),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text('SOLD TODAY', style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.brandDark)),
                        const SizedBox(height: 4),
                        Text(naira(_int(sales['total_kobo']) ?? 0), style: tkText(size: 32, weight: 800, letterSpacing: -0.64, color: t.tkOnPine)),
                        const SizedBox(height: 4),
                        Text(
                          '${_int(sales['count']) == 1 ? '1 sale' : '${_int(sales['count']) ?? 0} sales'}'
                          '${(_int(sales['waiting']) ?? 0) > 0 ? ' · ${_int(sales['waiting'])} waiting for their money' : ''}',
                          style: tkText(size: 14, color: t.tkOnPine.withValues(alpha: 0.82)),
                        ),
                      ],
                    ),
                  ),
                TkLines([
                  for (final e in byHow.entries)
                    if ((_int(e.value) ?? 0) > 0) (_howWords[e.key] ?? e.key, naira(_int(e.value)!)),
                  if (h['orders_open'] != null) ('Orders still open', '${h['orders_open']}'),
                  if (deliveries != null) ('Deliveries on the way · done', '${deliveries['out']} · ${deliveries['done']}'),
                  if (low.isNotEmpty) ('Low', low.join(', ')),
                ]),
                TkKitField(
                  label: 'Cash counted (₦)',
                  error: counted == -1 ? 'The cash in naira, like 45,000.' : null,
                  hint: counted == -1 ? null : (counted != null ? '${cashWords(counted, sold)} · ${naira(sold)} sold in cash' : '${naira(sold)} sold in cash'),
                  child: TkTextBox(controller: _cash, money: true, hint: '0'),
                ),
                TkKitField(
                  label: 'A note for the next shift (optional)',
                  child: TkTextBox(controller: _note, lines: 2, hint: 'The freezer is making a noise', max: 500),
                ),
                if (_error != null) TkErrorBox(_error!),
              ],
            ),
    );
  }
}
