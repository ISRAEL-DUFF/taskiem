// A shop's stock (M6 part T; web/src/pages/StorefrontStock.tsx, lib/
// stock.ts; docs/stock.md), opened from Stock's or Products' "Open stock":
// today's sales (Record a sale, a terminal's sale said), what is low, each
// item with Record stock, Change and its History, and every change kept.

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../kit/page.dart';
import '../kit/sheet.dart' show TkKitField;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'book_sheet.dart' show whenLabel;
import 'chats.dart' show LinkSection;
import 'sizes_forms.dart' show choicesOf, sizesOf, variantLabel, variantOf;
import 'product_form.dart' show hasSizes, sizesLine, sizesWords;
import 'words.dart' show Tone, naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int? _int(Object? v) => (v as num?)?.toInt();

/// stock.ts plural: bag, bags; box, boxes; battery, batteries.
String plural(String unit) {
  final l = unit.toLowerCase();
  if (RegExp(r'(s|x|ch|sh)$').hasMatch(l)) return l.endsWith('s') && !l.endsWith('ss') ? unit : '${unit}es';
  if (RegExp(r'[^aeiou]y$').hasMatch(l)) return '${unit.substring(0, unit.length - 1)}ies';
  return '${unit}s';
}

/// stock.ts quantity: "12 bags", "1 carton", "4".
String stockQuantity(int n, String? unit) {
  final u = unit?.trim() ?? '';
  return u.isEmpty ? '$n' : '$n ${n.abs() == 1 ? u : plural(u)}';
}

/// stock.ts stockLine: "48 cartons", "No count".
String stockLine(Map<String, dynamic> it) => _int(it['stock']) == null ? 'No count' : stockQuantity(_int(it['stock'])!, it['unit'] as String?);

/// stock.ts itemSub: its levels, cost, price and supplier.
String itemSub(Map<String, dynamic> it) {
  final unit = it['unit'] as String?;
  final parts = <String>[
    if (_int(it['reorder_level']) != null) 'reorder at ${_int(it['reorder_level'])}',
    if (_int(it['target_level']) != null) 'up to ${_int(it['target_level'])}',
    if (_int(it['cost_kobo']) != null) 'cost ${naira(_int(it['cost_kobo'])!)}',
    if (_int(it['price_kobo']) != null) 'sells at ${naira(_int(it['price_kobo'])!)}',
    if ((it['supplier'] as String? ?? '').isNotEmpty) it['supplier'] as String,
    if (it['low'] == true && (_int(it['suggest']) ?? 0) > 0) 'order ${stockQuantity(_int(it['suggest'])!, unit)}',
    if ((_int(it['waiting']) ?? 0) > 0) _int(it['waiting']) == 1 ? '1 change on its way' : '${_int(it['waiting'])} changes on their way',
  ];
  return parts.isEmpty ? 'No levels yet' : parts.join(' · ');
}

/// stock.ts REASON: why a change was made.
const reasons = {
  'order': 'Order',
  'sale': 'Sold',
  'received': 'Received',
  'adjustment': 'Adjusted',
  'count': 'Counted',
  'outside': 'Changed in the table',
  'moved': 'Moved',
};

/// stock.ts movementTitle: "Indomie: received +50", "Rice: counted 12".
String movementTitle(Map<String, dynamic> m) {
  final n = _int(m['change']) ?? _int(m['delta']) ?? _int(m['set_to']) ?? 0;
  if (m['reason'] == 'count') return '${m['item']}: counted ${_int(m['set_to']) ?? _int(m['after']) ?? n}';
  return '${m['item']}: ${(reasons[m['reason']] ?? '').toLowerCase()} ${n > 0 ? '+' : ''}$n';
}

/// stock.ts movementSub: when, the count it left, what from, why.
String movementSub(Map<String, dynamic> m) {
  final parts = [whenLabel((m['done_at'] ?? m['created_at']) as String? ?? '')];
  if (m['status'] == 'pending') {
    parts.add(m['error'] != null ? 'not in the table yet: it is sent again' : 'on its way to the table');
  } else if (m['before'] != null && m['after'] != null) {
    parts.add('${m['before']} → ${m['after']}');
  }
  if ((_int(m['short']) ?? 0) > 0) parts.add('${m['short']} more asked for than there was');
  if ((m['reference'] as String? ?? '').isNotEmpty) parts.add(m['reason'] == 'order' ? 'order ${m['reference']}' : m['reference'] as String);
  if ((m['note'] as String? ?? '').isNotEmpty) parts.add(m['note'] as String);
  return parts.join(' · ');
}

const _saleHow = {'cash': 'Cash', 'transfer': 'Transfer', 'pos': 'Terminal', 'credit': 'On credit'};

/// stock.ts byHowLine: "Cash ₦12,000 · Transfer ₦5,000".
String byHowLine(Map<String, dynamic> day) {
  final by = (day['by_how'] as Map?)?.cast<String, dynamic>() ?? const {};
  return [
    for (final h in _saleHow.keys)
      if ((_int(by[h]) ?? 0) > 0) '${_saleHow[h]} ${naira(_int(by[h])!)}',
  ].join(' · ');
}

/// stock.ts saleTitle: what was sold.
String saleTitle(Map<String, dynamic> s) =>
    [for (final l in _maps(s['lines'])) '${stockQuantity(_int(l['quantity']) ?? 0, l['unit'] as String?)} ${l['item']}'].join(', ');

/// stock.ts saleSub: when, how paid, whether its money came, its reference.
String saleSub(Map<String, dynamic> s) {
  final parts = [whenLabel(s['sold_at'] as String? ?? ''), _saleHow[s['paid_how']] ?? '${s['paid_how']}'];
  if (s['status'] == 'waiting') {
    parts.add(s['paid_how'] == 'pos' ? 'waiting for the terminal’s report' : 'waiting for the transfer');
  } else if (s['status'] == 'matched') {
    parts.add('money came in');
  }
  parts.add(s['reference'] as String? ?? '');
  final short = ((s['short'] as List?) ?? const []).whereType<String>().toList();
  if (short.isNotEmpty) parts.add('more ${short.join(', ')} sold than counted');
  if ((s['note'] as String? ?? '').isNotEmpty) parts.add(s['note'] as String);
  return parts.join(' · ');
}

/// orders.ts feeKobo: "" is 0; null when not money.
int? feeKobo(String text) {
  final t = text.replaceAll(RegExp(r'[₦,\s]'), '');
  if (t.isEmpty) return 0;
  if (!RegExp(r'^\d+(\.\d{1,2})?$').hasMatch(t)) return null;
  return (double.parse(t) * 100).round();
}

/// orders.ts feeInput: kobo as typed, "8500", "8500.50".
String feeInput(int kobo) => kobo % 100 == 0 ? '${kobo ~/ 100}' : '${kobo ~/ 100}.${(kobo % 100).toString().padLeft(2, '0')}';

int? _whole(String v) => RegExp(r'^\d+$').hasMatch(v.trim()) ? int.parse(v.trim()) : null;

/// StorefrontStock.tsx deskRows: each product once; one sold in sizes
/// with their count together, its sizes after it.
List<({Map<String, dynamic> it, Map<String, dynamic>? size, ({int count, int low, int? stock})? sizes})> deskRows(List<Map<String, dynamic>> items) {
  final out = <({Map<String, dynamic> it, Map<String, dynamic>? size, ({int count, int low, int? stock})? sizes})>[];
  for (final it in items) {
    if (it['variant_of'] != null) continue;
    if (!hasSizes(it)) {
      out.add((it: it, size: null, sizes: null));
      continue;
    }
    out.add((it: it, size: null, sizes: sizesLine(items, it['id'] as String)));
    for (final z in sizesOf(items, it['id'] as String)) {
      out.add((it: z, size: it, sizes: null));
    }
  }
  return out;
}

class StockPage extends StatefulWidget {
  const StockPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<StockPage> createState() => _StockPageState();
}

class _StockPageState extends State<StockPage> {
  Map<String, dynamic>? _view;
  Map<String, dynamic>? _sales;
  List<Map<String, dynamic>>? _moves;
  String? _error;
  String _item = '';
  String _reason = '';
  bool _busy = false;

  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _reload();
  }

  String _why(Object e) => e is ApiError ? e.message : 'You’re offline. This shows when you’re back online.';

  Future<void> _reload() async {
    try {
      final v = await _t.send('GET', '/v1/stock') as Map<String, dynamic>;
      if (!mounted) return;
      setState(() => (_view = v, _error = null));
      if (v['keeps_stock'] == true) {
        final s = await _t.send('GET', '/v1/stock/sales');
        if (mounted) setState(() => _sales = (s as Map?)?.cast<String, dynamic>());
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = _why(e));
    }
    await _loadMoves();
  }

  Future<void> _loadMoves() async {
    // A filter changed while the last was asked: only the newest is shown.
    final asked = ++_movesAsked;
    try {
      final r = await _t.send(
        'GET',
        '/v1/stock/movements',
        query: {'limit': '50', if (_item.isNotEmpty) 'item': _item, if (_reason.isNotEmpty) 'reason': _reason},
      );
      if (mounted && asked == _movesAsked) setState(() => _moves = _maps((r as Map)['movements']));
    } on Object catch (e) {
      if (mounted && asked == _movesAsked) setState(() => _error = _why(e));
    }
  }

  int _movesAsked = 0;

  Future<void> _setup() async {
    setState(() => _busy = true);
    try {
      await _t.send('POST', '/v1/stock/setup', body: const {});
      if (mounted) showTkToast(context, 'Stock is on');
      await _reload();
    } on Object catch (e) {
      if (mounted) setState(() => _error = _why(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _open(Widget Function(BuildContext context) form, String title) async {
    final saved = await showTkModal<bool>(context, title: title, builder: form);
    if (saved == true) await _reload();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final v = _view;
    final keeps = v?['keeps_stock'] == true;
    final items = _maps(v?['items']);
    final low = items.where((i) => i['low'] == true).toList();
    final stuck = (v?['stuck'] as List?)?.length ?? 0;
    final sales = _sales;
    return TkPageScreen(
      title: 'Stock',
      description: 'Your items and how many are left. Every change is kept, so a count can always be explained.',
      onRefresh: _reload,
      action: keeps
          ? TkSmallButton(
              label: 'Add items',
              primary: true,
              onPressed: () => _open((c) => _ItemForm(auth: widget.auth), 'Add an item'),
            )
          : null,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (v == null && _error == null) Text('Loading your stock…', style: tkHint(t)),
        if (v != null && !keeps)
          TkPageCard(
            title: 'Keep your stock here',
            children: [
              Text(
                '${v['catalogue'] != null ? 'Taskiem keeps the count in your ${v['catalogue']} table, adding the columns it needs: stock, unit, cost price, reorder level and supplier.' : 'Taskiem makes a products table for your items and keeps the count there. You need no page for it.'}'
                ' On WhatsApp, send add 50 cartons of Indomie at 8,500.',
                style: tkHint(t),
              ),
              const SizedBox(height: 12),
              Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(label: 'Keep my stock', primary: true, onPressed: _busy ? null : _setup),
              ),
            ],
          ),
        if (keeps) ...[
          if (sales != null) ..._salesToday(t, sales, items),
          if (stuck > 0)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
              decoration: BoxDecoration(
                color: cssMix(t.info, t.panel, 0.06),
                border: Border.all(color: t.info.withValues(alpha: 0.22)),
                borderRadius: BorderRadius.circular(10),
              ),
              child: Text(
                '${stuck == 1 ? '1 change has' : '$stuck changes have'} not reached your ${v!['catalogue']} table yet. Taskiem sends ${stuck == 1 ? 'it' : 'them'} again by itself.',
                style: tkText(size: 14, color: t.text),
              ),
            ),
          TkPageCard(
            title: 'Low on stock',
            children: [
              if (low.isEmpty)
                Text(
                  items.any((i) => i['reorder_level'] != null)
                      ? 'Nothing is at or below its reorder level.'
                      : 'Give your items a reorder level, and they show here when they run low.',
                  style: tkHint(t),
                )
              else
                LinkSection(
                  top: 0,
                  rows: [
                    for (final it in low)
                      (
                        letter: it['name'] as String? ?? '',
                        icon: null,
                        tone: null,
                        title: it['name'] as String? ?? '',
                        badge: stockLine(it),
                        badgeTone: Tone.warn,
                        sub: itemSub(it),
                        actions: _actions([_recordStock(it)]),
                      ),
                  ],
                ),
            ],
          ),
          TkPageCard(
            title: 'Your items',
            children: [
              if (items.isEmpty)
                Text('No items yet. Add what you sell, and how many you have.', style: tkHint(t))
              else
                LinkSection(
                  top: 0,
                  rows: [
                    for (final r in deskRows(items))
                      (
                        letter: r.size == null ? r.it['name'] as String? ?? '' : null,
                        icon: r.size == null ? null : 'tag',
                        tone: null,
                        title: r.size != null ? variantLabel(variantOf(r.it), choicesOf(r.size!)) : r.it['name'] as String? ?? '',
                        badge: r.sizes != null
                            ? '${sizesWords(r.sizes!.count)}${r.sizes!.stock != null ? ' · ${r.sizes!.stock} in stock' : ''}${r.sizes!.low > 0 ? ' · ${r.sizes!.low} low' : ''}'
                            : stockLine(r.it),
                        badgeTone: r.it['low'] == true || (r.sizes?.low ?? 0) > 0
                            ? Tone.warn
                            : (_int(r.it['stock']) == null && r.sizes == null ? Tone.muted : Tone.ok),
                        sub: r.size != null ? 'Of ${r.size!['name']}' : itemSub(r.it),
                        actions: _actions([
                          if (r.sizes == null) _recordStock(r.it),
                          if (r.size == null)
                            TkSmallButton(
                              label: 'Change',
                              semantics: 'Change ${r.it['name']}',
                              onPressed: () => _open((c) => _ItemForm(auth: widget.auth, item: r.it), 'Change ${r.it['name']}'),
                            ),
                          TkSmallButton(
                            label: 'History',
                            semantics: 'History of ${r.it['name']}',
                            onPressed: () {
                              setState(() => _item = r.it['id'] as String);
                              _loadMoves();
                            },
                          ),
                        ]),
                      ),
                  ],
                ),
            ],
          ),
          TkPageCard(
            title: _item.isNotEmpty ? '${items.where((i) => i['id'] == _item).map((i) => i['name']).firstOrNull ?? 'The item'}: every change' : 'Every change',
            children: [
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  TkSmallButton(
                    label: _reason.isEmpty ? 'Every reason' : reasons[_reason]!,
                    semantics: 'Why: ${_reason.isEmpty ? 'Every reason' : reasons[_reason]}',
                    onPressed: () async {
                      final r = await showTkModalChoices(
                        context,
                        title: 'Why',
                        value: _reason,
                        choices: [('', 'Every reason'), for (final e in reasons.entries) (e.key, e.value)],
                      );
                      if (r == null) return;
                      setState(() => _reason = r);
                      _loadMoves();
                    },
                  ),
                  if (_item.isNotEmpty)
                    TkSmallButton(
                      label: 'Every item',
                      onPressed: () {
                        setState(() => _item = '');
                        _loadMoves();
                      },
                    ),
                ],
              ),
              const SizedBox(height: 8),
              if (_moves != null && _moves!.isEmpty) Text('No changes yet.', style: tkHint(t)),
              if (_moves != null && _moves!.isNotEmpty)
                LinkSection(
                  top: 0,
                  rows: [
                    for (final m in _moves!)
                      (
                        letter: m['item'] as String? ?? '',
                        icon: null,
                        tone: null,
                        title: movementTitle(m),
                        badge: reasons[m['reason']] ?? '',
                        badgeTone: m['status'] == 'pending' ? Tone.warn : (m['reason'] == 'outside' ? Tone.info : Tone.muted),
                        sub: movementSub(m),
                        actions: null,
                      ),
                  ],
                ),
            ],
          ),
        ],
      ],
    );
  }

  Widget _recordStock(Map<String, dynamic> it) => TkSmallButton(
    label: 'Record stock',
    semantics: 'Record stock of ${it['name']}',
    onPressed: () => _open((c) => _MoveForm(auth: widget.auth, item: it), 'Record stock of ${it['name']}'),
  );

  Widget _actions(List<Widget> buttons) => Wrap(spacing: 8, runSpacing: 8, children: buttons);

  /// StorefrontStock.tsx SalesToday: the total in a pine hero with Record
  /// a sale, the terminal's sales with no items, then each sale.
  List<Widget> _salesToday(TkTokens t, Map<String, dynamic> day, List<Map<String, dynamic>> items) {
    final by = byHowLine(day);
    final count = _int(day['count']) ?? 0;
    final unsold = _maps(day['unsold']);
    final sales = _maps(day['sales']);
    final canSell = items.isNotEmpty;
    return [
      TkHero(
        label: 'Today’s sales',
        semanticsLabel: 'Today’s sales',
        tag: (_int(day['waiting']) ?? 0) > 0 ? '${day['waiting']} waiting for money' : null,
        amount: naira(_int(day['total_kobo']) ?? 0),
        sub: count == 0 ? 'No sales recorded today.' : '${count == 1 ? '1 sale' : '$count sales'}${by.isNotEmpty ? ' · $by' : ''}',
        action: TkSmallButton(
          label: 'Record a sale',
          primary: true,
          onPressed: canSell ? () => _open((c) => _SaleForm(auth: widget.auth, items: items), 'Record a sale') : null,
        ),
      ),
      if (unsold.isNotEmpty)
        TkPageCard(
          title: 'Terminal sales with no items',
          children: [
            Text('Your terminal reported these, but nobody said what was sold. Say it, and the items come off your stock.', style: tkHint(t)),
            LinkSection(
              top: 8,
              rows: [
                for (final u in unsold)
                  (
                    letter: '₦',
                    icon: null,
                    tone: null,
                    title: naira(_int(u['amount_kobo']) ?? 0),
                    badge: null,
                    badgeTone: null,
                    sub: 'On the terminal · ${whenLabel(u['at'] as String? ?? '')}',
                    actions: TkSmallButton(
                      label: 'Say what was sold',
                      semantics: 'Say what was sold for ${naira(_int(u['amount_kobo']) ?? 0)}',
                      onPressed: canSell
                          ? () => _open((c) => _SaleForm(auth: widget.auth, items: items, entry: u), 'What was sold for ${naira(_int(u['amount_kobo']) ?? 0)}')
                          : null,
                    ),
                  ),
              ],
            ),
          ],
        ),
      if (sales.isNotEmpty)
        TkPageCard(
          title: 'Sold today',
          children: [
            LinkSection(
              top: 0,
              rows: [
                for (final x in sales)
                  (
                    letter: _maps(x['lines']).firstOrNull?['item'] as String? ?? '₦',
                    icon: null,
                    tone: null,
                    title: saleTitle(x),
                    badge: naira(_int(x['amount_kobo']) ?? 0),
                    badgeTone: x['status'] == 'waiting' ? Tone.warn : Tone.ok,
                    sub: saleSub(x),
                    actions: null,
                  ),
              ],
            ),
          ],
        ),
    ];
  }
}

/// A field as the web's `Field`: its label, the box, a hint.
Widget pageField(String label, TextEditingController c, {String? hint, String? placeholder, bool money = false, bool number = false, int? max}) => TkKitField(
  label: label,
  hint: hint,
  child: TkTextBox(controller: c, hint: placeholder, money: money, digits: number ? 7 : null, max: max),
);

/// StorefrontStock.tsx ItemModal: an item added with how many there are,
/// or its name, unit, prices, levels and supplier changed.
class _ItemForm extends StatefulWidget {
  const _ItemForm({required this.auth, this.item});
  final Auth auth;
  final Map<String, dynamic>? item;

  @override
  State<_ItemForm> createState() => _ItemFormState();
}

class _ItemFormState extends State<_ItemForm> {
  Map<String, dynamic>? get _it => widget.item;
  late final _name = _c(_it?['name'] as String? ?? '');
  final _quantity = TextEditingController();
  late final _unit = _c(_it?['unit'] as String? ?? '');
  late final _cost = _c(_int(_it?['cost_kobo']) == null ? '' : feeInput(_int(_it!['cost_kobo'])!));
  late final _price = _c(_int(_it?['price_kobo']) == null ? '' : feeInput(_int(_it!['price_kobo'])!));
  late final _reorder = _c(_int(_it?['reorder_level'])?.toString() ?? '');
  late final _target = _c(_int(_it?['target_level'])?.toString() ?? '');
  late final _supplier = _c(_it?['supplier'] as String? ?? '');
  late final _key = uuid4();
  bool _busy = false;
  String? _error;

  TextEditingController _c(String text) => TextEditingController(text: text)..addListener(() => setState(() {}));

  @override
  void initState() {
    super.initState();
    _quantity.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    for (final c in [_name, _quantity, _unit, _cost, _price, _reorder, _target, _supplier]) {
      c.dispose();
    }
    super.dispose();
  }

  /// stock.ts itemProblem.
  String? get _problem {
    final adding = _it == null;
    if (_name.text.trim().isEmpty) return 'Give the item a name.';
    if (_name.text.trim().length > 120) return 'A name is up to 120 characters.';
    final q = _whole(_quantity.text);
    if (adding && _quantity.text.trim().isNotEmpty && !(q != null && q <= 1000000)) return 'How many there are is a whole number, like 50.';
    if (_unit.text.trim().length > 30) return 'A unit is one word, like bag or carton.';
    if (_cost.text.trim().isNotEmpty && feeKobo(_cost.text) == null) return 'The cost price is an amount, like 8500.';
    if (_price.text.trim().isNotEmpty && feeKobo(_price.text) == null) return 'The selling price is an amount, like 8500.';
    if (_reorder.text.trim().isNotEmpty && _whole(_reorder.text) == null) return 'The reorder level is a whole number.';
    if (_target.text.trim().isNotEmpty && _whole(_target.text) == null) return 'The level to order up to is a whole number.';
    if (_reorder.text.trim().isNotEmpty && _target.text.trim().isNotEmpty && _whole(_target.text)! <= _whole(_reorder.text)!) {
      return 'Order up to more than the reorder level.';
    }
    return null;
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    final it = _it;
    try {
      if (it == null) {
        // stock.ts addRequest.
        await widget.auth.transport.send(
          'POST',
          '/v1/stock/items',
          body: {
            'name': _name.text.trim(),
            'quantity': _quantity.text.trim().isEmpty ? 0 : _whole(_quantity.text),
            'unit': _unit.text.trim(),
            if (_cost.text.trim().isNotEmpty) 'cost_kobo': feeKobo(_cost.text),
            if (_price.text.trim().isNotEmpty) 'price_kobo': feeKobo(_price.text),
            if (_reorder.text.trim().isNotEmpty) 'reorder_level': _whole(_reorder.text),
            if (_target.text.trim().isNotEmpty) 'target_level': _whole(_target.text),
            'supplier': _supplier.text.trim(),
          },
          headers: {'Idempotency-Key': _key},
        );
      } else {
        // stock.ts changeRequest: what changed only.
        String was(String k) => switch (k) {
          'cost_kobo' || 'price_kobo' => _int(it[k]) == null ? '' : feeInput(_int(it[k])!),
          'reorder_level' || 'target_level' => _int(it[k])?.toString() ?? '',
          _ => it[k] as String? ?? '',
        };
        final out = <String, Object?>{
          if (_name.text.trim() != it['name']) 'name': _name.text.trim(),
          if (_unit.text.trim() != was('unit')) 'unit': _unit.text.trim(),
          if (_supplier.text.trim() != was('supplier')) 'supplier': _supplier.text.trim(),
          if (_cost.text.trim() != was('cost_kobo') && _cost.text.trim().isNotEmpty) 'cost_kobo': feeKobo(_cost.text),
          if (_price.text.trim() != was('price_kobo') && _price.text.trim().isNotEmpty) 'price_kobo': feeKobo(_price.text),
          if (_reorder.text.trim() != was('reorder_level') && _reorder.text.trim().isNotEmpty) 'reorder_level': _whole(_reorder.text),
          if (_target.text.trim() != was('target_level') && _target.text.trim().isNotEmpty) 'target_level': _whole(_target.text),
        };
        await widget.auth.transport.send('PATCH', '/v1/stock/items/${it['id']}', body: out);
      }
      if (!mounted) return;
      showTkToast(context, it == null ? '${_name.text.trim()} added' : 'Saved');
      Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final adding = _it == null;
    return TkModalForm(
      saveWords: adding ? 'Add the item' : 'Save',
      busy: _busy,
      problem: _name.text.isEmpty ? null : _problem,
      error: _error,
      save: _problem == null ? _save : null,
      children: [
        pageField('Name', _name, max: 120),
        if (adding)
          pageField(
            'How many you have',
            _quantity,
            hint: 'Recorded as goods received, so the count is explained from the start.',
            placeholder: '50',
            number: true,
          ),
        pageField('Unit', _unit, max: 30, hint: 'One word: bag, carton, piece. Leave it empty to count pieces.', placeholder: 'carton'),
        pageField('Cost price (₦)', _cost, hint: 'What you pay for one, for the value of your stock and what you lose.', placeholder: '8500', money: true),
        pageField(
          'Selling price (₦)',
          _price,
          hint: 'Shown on your page once it is published. Without one, the item is not shown.',
          placeholder: '9500',
          money: true,
        ),
        pageField('Reorder at', _reorder, hint: 'When the count is this or less, the item is low and you are told each morning.', placeholder: '10', number: true),
        pageField('Order up to', _target, hint: 'What to order a low item back up to.', placeholder: '50', number: true),
        pageField('Supplier', _supplier, placeholder: 'Dangote Depot', max: 120),
      ],
    );
  }
}

/// StorefrontStock.tsx MoveModal: goods received, an adjustment with a
/// reason, or a count.
class _MoveForm extends StatefulWidget {
  const _MoveForm({required this.auth, required this.item});
  final Auth auth;
  final Map<String, dynamic> item;

  @override
  State<_MoveForm> createState() => _MoveFormState();
}

class _MoveFormState extends State<_MoveForm> {
  String _kind = 'received';
  bool _off = true;
  final _amount = TextEditingController();
  final _note = TextEditingController();
  late final _key = uuid4();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _amount.addListener(() => setState(() {}));
    _note.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _amount.dispose();
    _note.dispose();
    super.dispose();
  }

  /// stock.ts moveProblem.
  String? get _problem {
    final n = _whole(_amount.text);
    if (n == null) return _kind == 'count' ? 'Enter how many you counted, like 12.' : 'Enter how many, like 5.';
    if (_kind != 'count' && n == 0) return 'Enter how many, more than 0.';
    if (n > 1000000) return 'That is more than Taskiem keeps for one item.';
    if (_kind == 'adjustment' && _note.text.trim().isEmpty) return 'Say why, like spoilt or found.';
    if (_note.text.length > 200) return 'The reason is up to 200 characters.';
    return null;
  }

  /// stock.ts movePreview: "48 → 45 cartons".
  String? get _preview {
    final n = _whole(_amount.text);
    final now = _int(widget.item['stock']);
    if (n == null || now == null) return null;
    final to = _kind == 'count' ? n : (now + (_kind == 'adjustment' && _off ? -n : n)).clamp(0, 1 << 31);
    return '$now → ${stockQuantity(to, widget.item['unit'] as String?)}';
  }

  Future<void> _save() async {
    final n = _whole(_amount.text)!;
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send(
        'POST',
        '/v1/stock/movements',
        body: {
          'item_id': widget.item['id'],
          'reason': _kind,
          'note': _note.text.trim(),
          if (_kind == 'count') 'count': n else 'change': _kind == 'adjustment' && _off ? -n : n,
        },
        headers: {'Idempotency-Key': '$_key-$_kind'},
      );
      if (!mounted) return;
      showTkToast(context, 'Recorded');
      Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  Widget _radio(String label, bool on, VoidCallback onTap) => TkChoiceRow(
    control: TkRadio(checked: on),
    label: Text(label, style: tkText(size: 15, color: TkTheme.tokens(context).text)),
    onTap: onTap,
  );

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = _problem;
    return TkModalForm(
      saveWords: 'Record',
      busy: _busy,
      problem: _amount.text.isEmpty ? null : problem,
      error: _error,
      save: problem == null ? _save : null,
      children: [
        Text('Now: ${stockLine(widget.item)}.', style: tkHint(t)),
        Semantics(
          container: true,
          label: 'What happened',
          explicitChildNodes: true,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text('What happened', style: tkText(size: 12.5, weight: 500, color: t.text)),
              const SizedBox(height: 6),
              _radio('Goods came in', _kind == 'received', () => setState(() => _kind = 'received')),
              _radio('Some were spoilt, lost or found', _kind == 'adjustment', () => setState(() => _kind = 'adjustment')),
              _radio('I counted what is on the shelf', _kind == 'count', () => setState(() => _kind = 'count')),
            ],
          ),
        ),
        if (_kind == 'adjustment')
          Semantics(
            container: true,
            label: 'Take off or add',
            explicitChildNodes: true,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('Take off or add', style: tkText(size: 12.5, weight: 500, color: t.text)),
                const SizedBox(height: 6),
                _radio('Take them off', _off, () => setState(() => _off = true)),
                _radio('Add them', !_off, () => setState(() => _off = false)),
              ],
            ),
          ),
        pageField(_kind == 'count' ? 'How many you counted' : 'How many', _amount, placeholder: _kind == 'count' ? '12' : '5', number: true),
        pageField(_kind == 'adjustment' ? 'Why' : 'Note', _note, hint: _kind == 'adjustment' ? 'Spoilt, lost, found, a gift.' : null, max: 200),
        if (_preview != null && problem == null) Text('The count goes from $_preview.', style: tkHint(t)),
      ],
    );
  }
}

/// StorefrontStock.tsx SaleModal: the items, how many, how it was paid;
/// or what a terminal's sale was.
class _SaleForm extends StatefulWidget {
  const _SaleForm({required this.auth, required this.items, this.entry});
  final Auth auth;
  final List<Map<String, dynamic>> items;
  final Map<String, dynamic>? entry;

  @override
  State<_SaleForm> createState() => _SaleFormState();
}

class _Line {
  _Line(VoidCallback changed) {
    for (final c in [quantity, price]) {
      c.addListener(changed);
    }
  }
  String item = '';
  final quantity = TextEditingController(text: '1');
  final price = TextEditingController();
}

class _SaleFormState extends State<_SaleForm> {
  late final List<_Line> _lines = [_Line(_changed)];
  late String _how = widget.entry != null ? 'pos' : 'cash';
  final _amount = TextEditingController();
  final _note = TextEditingController();
  late final _key = uuid4();
  bool _busy = false;
  String? _error;

  void _changed() => setState(() {});

  @override
  void initState() {
    super.initState();
    _amount.addListener(_changed);
    _note.addListener(_changed);
  }

  @override
  void dispose() {
    for (final l in _lines) {
      l.quantity.dispose();
      l.price.dispose();
    }
    _amount.dispose();
    _note.dispose();
    super.dispose();
  }

  Map<String, dynamic>? _itemOf(_Line l) => widget.items.where((i) => i['id'] == l.item).firstOrNull;

  int? _linePrice(_Line l) => l.price.text.trim().isNotEmpty ? feeKobo(l.price.text) : _int(_itemOf(l)?['price_kobo']);

  /// stock.ts saleTotal.
  int? get _total {
    var sum = 0;
    for (final l in _lines) {
      final p = _linePrice(l);
      final n = _whole(l.quantity.text);
      if (p == null || n == null) return null;
      sum += p * n;
    }
    return sum;
  }

  /// stock.ts saleProblem.
  String? get _problem {
    for (final l in _lines) {
      final it = _itemOf(l);
      if (it == null) return 'Choose the item sold on each line.';
      final n = _whole(l.quantity.text);
      if (n == null || n <= 0 || n > 1000000) return 'How many ${it['name']} is a whole number, like 2.';
      if (l.price.text.trim().isNotEmpty && feeKobo(l.price.text) == null) return 'The price of ${it['name']} is an amount, like 4500.';
    }
    if (_amount.text.trim().isNotEmpty) {
      final a = feeKobo(_amount.text);
      if (a == null || a <= 0) return 'The amount taken is an amount, like 45000.';
    } else {
      final missing = _lines.where((l) => l.price.text.trim().isEmpty).map(_itemOf).where((it) => it != null && it['price_kobo'] == null).firstOrNull;
      if (missing != null) return '${missing['name']} has no price: give its price or the amount taken.';
      if (_total == 0) return 'The sale comes to ₦0.';
    }
    if (_note.text.length > 200) return 'The note is up to 200 characters.';
    return null;
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    final entry = widget.entry;
    try {
      // stock.ts saleRequest.
      await widget.auth.transport.send(
        'POST',
        '/v1/stock/sales',
        body: {
          'lines': [
            for (final l in _lines)
              {'item_id': l.item, 'quantity': _whole(l.quantity.text), if (l.price.text.trim().isNotEmpty) 'price_kobo': feeKobo(l.price.text)},
          ],
          'paid_how': entry != null ? 'pos' : _how,
          if (entry != null) ...{
            'amount_kobo': entry['amount_kobo'],
            'entry_id': entry['entry_id'],
          } else if (_amount.text.trim().isNotEmpty)
            'amount_kobo': feeKobo(_amount.text),
          'note': _note.text.trim(),
        },
        headers: {'Idempotency-Key': _key},
      );
      if (!mounted) return;
      showTkToast(context, 'Sale recorded');
      Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final problem = _problem;
    final total = _total;
    final touched = _lines.any((l) => l.item.isNotEmpty);
    return TkModalForm(
      saveWords: 'Record the sale',
      busy: _busy,
      problem: touched ? problem : null,
      error: _error,
      save: problem == null ? _save : null,
      children: [
        for (var i = 0; i < _lines.length; i++)
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(_lines.length > 1 ? 'Item ${i + 1}' : 'Item', style: tkText(size: 12.5, weight: 500, color: t.text)),
              const SizedBox(height: 6),
              TkKitField(
                field: false,
                label: 'What was sold',
                child: TkSmallButton(
                  label: _itemOf(_lines[i]) == null ? 'Choose an item' : '${_itemOf(_lines[i])!['name']} (${stockLine(_itemOf(_lines[i])!)})',
                  semantics: 'What was sold: ${_itemOf(_lines[i])?['name'] ?? 'Choose an item'}',
                  onPressed: () async {
                    final id = await showTkModalChoices(
                      context,
                      title: 'What was sold',
                      value: _lines[i].item,
                      choices: [for (final x in widget.items) (x['id'] as String, '${x['name']} (${stockLine(x)})')],
                    );
                    if (id != null) setState(() => _lines[i].item = id);
                  },
                ),
              ),
              const SizedBox(height: 10),
              pageField('How many', _lines[i].quantity, number: true),
              const SizedBox(height: 10),
              pageField(
                'Price for one (₦)',
                _lines[i].price,
                money: true,
                hint: _int(_itemOf(_lines[i])?['price_kobo']) != null ? 'Leave it empty for ${naira(_int(_itemOf(_lines[i])!['price_kobo'])!)}.' : null,
              ),
              if (_lines.length > 1)
                Align(
                  alignment: Alignment.centerLeft,
                  child: TkSmallButton(
                    label: 'Take this item off',
                    semantics: 'Take this item off ${i + 1}',
                    onPressed: () => setState(() => _lines.removeAt(i)),
                  ),
                ),
            ],
          ),
        Align(
          alignment: Alignment.centerLeft,
          child: TkSmallButton(label: 'Add another item', onPressed: () => setState(() => _lines.add(_Line(_changed)))),
        ),
        if (widget.entry == null) ...[
          Semantics(
            container: true,
            label: 'How it was paid',
            explicitChildNodes: true,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('How it was paid', style: tkText(size: 12.5, weight: 500, color: t.text)),
                const SizedBox(height: 6),
                for (final h in const [('cash', 'Cash'), ('transfer', 'Transfer'), ('pos', 'Terminal')])
                  TkChoiceRow(
                    control: TkRadio(checked: _how == h.$1),
                    label: Text(h.$2, style: tkText(size: 15, color: t.text)),
                    onTap: () => setState(() => _how = h.$1),
                  ),
              ],
            ),
          ),
          pageField(
            'Amount taken (₦)',
            _amount,
            money: true,
            hint: total != null ? 'Leave it empty for ${naira(total)}, the items’ prices.' : 'Needed when an item has no price.',
          ),
        ],
        pageField('Note', _note, max: 200),
        if (widget.entry == null && _how != 'cash')
          Text('The sale waits for its money, and is matched when the ${_how == 'pos' ? 'terminal reports it' : 'transfer comes in'}.', style: tkHint(t)),
      ],
    );
  }
}
