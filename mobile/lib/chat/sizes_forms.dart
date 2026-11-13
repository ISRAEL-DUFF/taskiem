// Sizes and colours (M6 part P; web/src/pages/SizesForms.tsx, lib/
// variants.ts; docs/spec/chat-categories-and-variants.md Part B): a
// product's choices and the grid of what it sells, each box its count;
// a photo for each colour; goods received by the grid; and items made one
// product's sizes and colours. Every combination starts on sale; a box
// left empty is one not sold. The words are "sizes and colours" and
// "choices", never "variant".

import 'dart:math' as math;

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/api_image.dart';
import '../kit/forms.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'composer.dart' show Pickers;
import 'product_form.dart';
import 'words.dart' show naira;

const maxChoices = 3;
const maxValues = 20;
const maxSizes = 100;

typedef Choice = ({String name, List<String> values});
typedef Variant = Map<String, String>;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int? _int(Object? v) => (v as num?)?.toInt();

/// A product's choices, as the API has them.
List<Choice> choicesOf(Map<String, dynamic> it) => [
  for (final c in _maps(it['choices'])) (name: c['name'] as String? ?? '', values: ((c['values'] as List?) ?? const []).whereType<String>().toList()),
];

Variant? variantOf(Map<String, dynamic> it) => (it['variant'] as Map?)?.map((k, v) => MapEntry(k as String, '$v'));

/// variants.ts label: "42, Black", in the choices' order.
String variantLabel(Variant? v, List<Choice> cs) {
  if (v == null) return '';
  if (cs.isEmpty) return v.values.join(', ');
  return [for (final c in cs) ?v[c.name]].where((x) => x.isNotEmpty).join(', ');
}

/// variants.ts keyOf: a variant as one string, whatever its case.
String keyOf(Variant v) => ([for (final e in v.entries) '${e.key.trim().toLowerCase()}=${e.value.trim().toLowerCase()}']..sort()).join('|');

/// variants.ts allCells: every combination, the first choice slowest.
List<Variant> allCells(List<Choice> cs) {
  if (cs.isEmpty) return const [];
  var out = <Variant>[{}];
  for (final c in cs) {
    out = [
      for (final v in out)
        for (final x in c.values) {...v, c.name: x},
    ];
  }
  return out;
}

/// variants.ts sizesOf: a product's sizes and colours (those off sale too,
/// with [all]).
List<Map<String, dynamic>> sizesOf(List<Map<String, dynamic>> items, String product, {bool all = false}) =>
    items.where((x) => x['variant_of'] == product && (all || x['available'] == true)).toList();

/// variants.ts parseValues: "40, 41, 42" or one a line, each once.
List<String> parseValues(String text) {
  final seen = <String>{};
  final out = <String>[];
  for (final raw in text.split(RegExp(r'[,\n]'))) {
    final v = raw.trim().replaceAll(RegExp(r'\s+'), ' ');
    if (v.isNotEmpty && seen.add(v.toLowerCase())) out.add(v);
  }
  return out;
}

/// variants.ts PRESETS: ready-made values, to save typing.
const presets = <({String label, String name, List<String> values})>[
  (label: 'Shoe sizes (EU)', name: 'Size', values: ['36', '37', '38', '39', '40', '41', '42', '43', '44', '45', '46']),
  (label: 'Shoe sizes (UK)', name: 'Size', values: ['3', '4', '5', '6', '7', '8', '9', '10', '11', '12']),
  (label: 'Clothing', name: 'Size', values: ['XS', 'S', 'M', 'L', 'XL', 'XXL']),
  (label: 'Small, medium, large', name: 'Size', values: ['Small', 'Medium', 'Large']),
  (label: "Children's ages", name: 'Age', values: ['0–1', '1–2', '2–3', '3–4', '4–5', '5–6', '7–8', '9–10', '11–12']),
  (label: 'Bottles', name: 'Size', values: ['35cl', '50cl', '75cl', '1L', '1.5L']),
  (label: 'Bags', name: 'Size', values: ['5kg', '10kg', '25kg', '50kg']),
  (label: 'Colours', name: 'Colour', values: ['Black', 'White', 'Red', 'Blue', 'Brown', 'Grey']),
];

const _colours = {
  'black', 'white', 'red', 'blue', 'navy', 'green', 'yellow', 'orange', 'purple', 'pink', 'brown', 'grey', 'gray', 'beige', 'cream', //
  'gold', 'silver', 'maroon', 'wine', 'burgundy', 'khaki', 'olive', 'teal', 'turquoise', 'lilac', 'peach', 'coral', 'ivory', 'tan', //
  'camel', 'mint', 'denim', 'multi', 'multicolour',
};
final _sizeWord = RegExp(r'^(\d+(\.\d+)?([a-z]{1,3})?|x{0,3}[sl]|m|xxxl|[2-5]xl|small|medium|large|free|one ?size|uk\d+|us\d+|eu\d+)$', caseSensitive: false);

String _kindOf(String word) {
  final w = word.toLowerCase();
  if (_colours.contains(w)) return 'Colour';
  if (_sizeWord.hasMatch(w)) return 'Size';
  return 'Style';
}

/// variants.ts proposeGroup: items made one product: its name (the words
/// their names share at the start), its choices (from what the rest of
/// each name is) and each item's values. Only a proposal.
({String name, List<String> choices, Map<String, Variant> values}) proposeGroup(List<({String id, String name})> items) {
  final words = [for (final it in items) it.name.split(RegExp(r'[\s,–—|]+')).where((w) => w.isNotEmpty && !RegExp(r'^[-/]+$').hasMatch(w)).toList()];
  final head = words.isEmpty ? const <String>[] : words.first;
  var common = 0;
  while (common < head.length && words.every((w) => common < w.length - 1 && w[common].toLowerCase() == head[common].toLowerCase())) {
    common++;
  }
  final values = <String, Variant>{};
  for (var i = 0; i < items.length; i++) {
    final v = <String, String>{};
    for (final w in words[i].skip(common)) {
      final k = _kindOf(w);
      v[k] = v[k] == null ? w : '${v[k]} $w';
    }
    values[items[i].id] = v;
  }
  final kinds = [
    for (final k in const ['Size', 'Colour', 'Style'])
      if (values.values.any((v) => v[k] != null)) k,
  ];
  for (final v in values.values) {
    for (final k in kinds) {
      v[k] ??= '';
    }
  }
  return (name: head.take(common).join(' '), choices: kinds, values: values);
}

/// SizesForms.tsx groups: a grid's boxes in groups by every choice but
/// the last, the last's values side by side.
List<({String label, List<Variant> cells})> gridGroups(List<Variant> cells, List<Choice> cs) {
  final head = cs.isEmpty ? const <Choice>[] : cs.sublist(0, cs.length - 1);
  final out = <({String label, List<Variant> cells})>[];
  for (final v in cells) {
    final l = head.isEmpty ? '' : variantLabel(v, head);
    final g = out.where((x) => x.label == l).firstOrNull;
    if (g != null) {
      g.cells.add(v);
    } else {
      out.add((label: l, cells: [v]));
    }
  }
  return out;
}

/// A grid's variant in the product's choices as they were, so a value
/// corrected in place (40 to 43) is the same size, renamed.
Variant _asWas(Variant v, List<Choice> now, List<Choice> was) {
  final out = <String, String>{};
  for (var i = 0; i < now.length; i++) {
    final c = now[i];
    final old = i < was.length ? was[i] : null;
    final x = v[c.name] ?? '';
    final at = c.values.indexOf(x);
    out[old?.name ?? c.name] = old != null && old.values.length == c.values.length && at >= 0 ? old.values[at] : x;
  }
  return out;
}

/// `.tk-choice`, `.tk-grid-group`: a fieldset's line, its legend on it.
class TkFieldset extends StatelessWidget {
  const TkFieldset({super.key, required this.legend, required this.children, this.low = false});
  final String legend;
  final List<Widget> children;
  final bool low;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      label: legend,
      explicitChildNodes: true,
      child: Stack(
        clipBehavior: Clip.none,
        children: [
          Container(
            margin: const EdgeInsets.only(top: 9),
            padding: const EdgeInsets.fromLTRB(14, 14, 14, 12),
            decoration: BoxDecoration(
              border: Border.all(color: t.border),
              borderRadius: BorderRadius.circular(14),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 10), children[i]],
              ],
            ),
          ),
          // The legend sits on the line, the panel behind its words.
          Positioned(
            left: 8,
            top: 0,
            child: ExcludeSemantics(
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 6),
                color: t.panel,
                child: Text(legend, style: tkText(size: 14, weight: 700, color: t.text)),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// `.tk-grid-cells`: boxes at least 84 wide, as many to a row as fit.
class GridCells extends StatelessWidget {
  const GridCells({super.key, required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, box) {
      final cols = math.max(1, ((box.maxWidth + 8) / (84 + 8)).floor());
      final w = (box.maxWidth - 8 * (cols - 1)) / cols;
      return Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [for (final c in children) SizedBox(width: w, child: c)],
      );
    },
  );
}

/// `.tk-grid-cell`: a size's name (and a line under it), and its box.
class GridCell extends StatefulWidget {
  const GridCell({
    super.key,
    required this.name,
    this.small,
    required this.label,
    required this.value,
    required this.onChanged,
    this.hint = '',
    this.low = false,
    this.price,
  });
  final String name;
  final String? small;
  final String label;
  final String value;
  final ValueChanged<String> onChanged;
  final String hint;
  final bool low;

  /// Its own price, under its count (Some cost more or less).
  final ({String value, String hint, String label, ValueChanged<String> onChanged})? price;

  @override
  State<GridCell> createState() => _GridCellState();
}

class _GridCellState extends State<GridCell> {
  late final _count = TextEditingController(text: widget.value);
  late final _price = TextEditingController(text: widget.price?.value ?? '');
  final _countFocus = FocusNode();
  final _priceFocus = FocusNode();

  @override
  void didUpdateWidget(GridCell oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.value != _count.text && !_countFocus.hasFocus) _count.text = widget.value;
    final p = widget.price?.value;
    if (p != null && p != _price.text && !_priceFocus.hasFocus) _price.text = p;
  }

  @override
  void dispose() {
    _count.dispose();
    _price.dispose();
    _countFocus.dispose();
    _priceFocus.dispose();
    super.dispose();
  }

  Widget _box(TkTokens t, TextEditingController c, FocusNode f, String label, String hint, ValueChanged<String> onChanged, {bool money = false}) => Semantics(
    label: label,
    textField: true,
    child: Container(
      height: 40,
      decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(10)),
      alignment: Alignment.center,
      child: Stack(
        alignment: Alignment.center,
        children: [
          if (c.text.isEmpty)
            ExcludeSemantics(
              child: Text(hint, style: tkText(size: 15, weight: 600, color: t.muted)),
            ),
          EditableText(
            controller: c,
            focusNode: f,
            textAlign: TextAlign.center,
            keyboardType: money ? const TextInputType.numberWithOptions(decimal: true) : TextInputType.number,
            inputFormatters: money ? const [] : [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(6)],
            style: tkText(size: 15, weight: 700, color: t.text),
            cursorColor: t.text,
            backgroundCursorColor: t.muted,
            showCursor: !TkField.debugHideCaret,
            onChanged: (v) {
              setState(() {});
              onChanged(v);
            },
          ),
        ],
      ),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final ink = widget.low ? t.tkWarnInk : t.text;
    return Container(
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: widget.low ? t.tkWarnBg : t.tkSoft,
        borderRadius: BorderRadius.circular(12),
        border: widget.low ? Border.all(color: Color.lerp(t.tkSoft, t.warn, 0.55)!, width: 1.5) : null,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          ExcludeSemantics(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(widget.name, style: tkText(size: 13, weight: 700, color: ink)),
                if (widget.small != null) Text(widget.small!, style: tkText(size: 13, weight: 500, color: widget.low ? ink : t.muted)),
              ],
            ),
          ),
          const SizedBox(height: 4),
          _box(t, _count, _countFocus, widget.label, widget.hint, widget.onChanged),
          if (widget.price != null) ...[
            const SizedBox(height: 4),
            _box(t, _price, _priceFocus, widget.price!.label, widget.price!.hint, widget.price!.onChanged, money: true),
          ],
        ],
      ),
    );
  }
}

Future<List<Map<String, dynamic>>> _stock(Transport t) async => _maps((await t.send('GET', '/v1/stock') as Map<String, dynamic>)['items']);

/// SizesForms.tsx SizesEditor: a product's choices, typed or ready-made,
/// and the grid of how many of each; some at a price of their own.
class SizesEditor extends StatefulWidget {
  const SizesEditor({super.key, required this.auth, required this.item, required this.onSaved, required this.onCancel, this.pickers = const Pickers()});
  final Auth auth;
  final Map<String, dynamic> item;
  final void Function(String? line) onSaved;
  final VoidCallback onCancel;
  final Pickers pickers;

  @override
  State<SizesEditor> createState() => _SizesEditorState();
}

class _Draft {
  _Draft(String name, String text) : name = TextEditingController(text: name), text = TextEditingController(text: text);
  final TextEditingController name;
  final TextEditingController text;
}

class _SizesEditorState extends State<SizesEditor> {
  Map<String, dynamic> get _it => widget.item;
  late final List<Choice> _was = choicesOf(_it);
  late final List<_Draft> _drafts = _was.isEmpty ? [_draft('Size', '')] : [for (final c in _was) _draft(c.name, c.values.join(', '))];
  final _counts = <String, String>{};
  final _prices = <String, String>{};
  late final Set<String> _ownWas = ((_it['own_price'] as List?) ?? const []).whereType<String>().toSet();
  late bool _own = _ownWas.isNotEmpty;
  final _fill = TextEditingController();
  List<Map<String, dynamic>>? _all;
  String? _error;
  final _key = SaveKey();

  _Draft _draft(String name, String text) {
    final d = _Draft(name, text);
    d.name.addListener(() => setState(() {}));
    d.text.addListener(() => setState(() {}));
    return d;
  }

  @override
  void initState() {
    super.initState();
    _fill.addListener(() => setState(() {}));
    _stock(widget.auth.transport).then(
      (x) {
        if (mounted) setState(() => _all = x);
      },
      onError: (Object e) {
        if (mounted) setState(() => _error = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.');
      },
    );
  }

  @override
  void dispose() {
    for (final d in _drafts) {
      d.name.dispose();
      d.text.dispose();
    }
    _fill.dispose();
    super.dispose();
  }

  List<Choice> get _cs => [
    for (final d in _drafts)
      if (d.name.text.trim().isNotEmpty && parseValues(d.text.text).isNotEmpty) (name: d.name.text.trim(), values: parseValues(d.text.text)),
  ];

  Map<String, Map<String, dynamic>> get _before => {
    for (final v in sizesOf(_all ?? const [], _it['id'] as String, all: true))
      if (variantOf(v) != null) keyOf(variantOf(v)!): v,
  };

  Map<String, dynamic>? _old(Variant v, List<Choice> cs) => _before[keyOf(_asWas(v, cs, _was))];

  String _countOf(Variant v, List<Choice> cs) {
    final k = keyOf(v);
    if (_counts.containsKey(k)) return _counts[k]!;
    final o = _old(v, cs);
    if (o != null && o['available'] != true) return '';
    return o != null ? '${_int(o['stock']) ?? 0}' : '0';
  }

  String _priceOf(Variant v, List<Choice> cs) {
    final k = keyOf(v);
    if (_prices.containsKey(k)) return _prices[k]!;
    final o = _old(v, cs);
    final p = _int(o?['price_kobo']);
    return p != null && _ownWas.contains(keyOf(_asWas(v, cs, _was))) ? '${p / 100 == (p ~/ 100) ? p ~/ 100 : p / 100}' : '';
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final name = _it['name'] as String? ?? '';
    if (_all == null) {
      return _error != null ? Padding(padding: const EdgeInsets.all(20), child: TkErrorBox(_error!)) : const TkLoading(label: 'Loading its sizes and colours…');
    }
    final cs = _cs;
    final cells = allCells(cs);
    final sold = cells.where((v) => _countOf(v, cs).trim().isNotEmpty).toList();
    final shelf = (_int(_it['stock']) ?? 0) > 0 ? _int(_it['stock'])! : 0;
    final taken = sold.where((v) => _old(v, cs) == null).fold<int>(0, (n, v) => n + (int.tryParse(_countOf(v, cs)) ?? 0));
    final problem = _drafts.any((d) => parseValues(d.text.text).length > maxValues)
        ? 'A choice has up to $maxValues values.'
        : sold.length > maxSizes
        ? 'Up to $maxSizes sizes and colours in all: clear the boxes of those you do not sell.'
        : sold.any((v) => !RegExp(r'^\d+$').hasMatch(_countOf(v, cs).trim()))
        ? 'A box holds how many there are: a whole number, or empty for one you do not sell.'
        : _own && sold.any((v) => _priceOf(v, cs).trim().isNotEmpty && nairaKobo(_priceOf(v, cs)) == null)
        ? 'A price in naira, like 45,000.'
        : '';
    Future<String?> save() async {
      final body = {
        'choices': [
          for (final c in cs) {'name': c.name, 'values': c.values},
        ],
        'cells': [
          for (final v in sold)
            {
              'id': _old(v, cs)?['id'],
              'variant': v,
              'count': int.parse(_countOf(v, cs)),
              if (_own && _priceOf(v, cs).trim().isNotEmpty) 'price_kobo': nairaKobo(_priceOf(v, cs)),
            },
        ],
      };
      await widget.auth.transport.send(
        'PUT',
        '/v1/stock/items/${Uri.encodeComponent(_it['id'] as String)}/variants',
        body: body,
        headers: {'Idempotency-Key': _key.of(body), 'X-Taskiem-Diary': '1'},
      );
      return '$name: ${sizesWords(sold.length)}';
    }

    final price = _int(_it['price_kobo']);
    return TkToolForm(
      save: save,
      onSaved: widget.onSaved,
      onCancel: widget.onCancel,
      ready: cs.isNotEmpty && sold.isNotEmpty && problem.isEmpty,
      children: [
        Text('Each size and colour has its own stock. $name itself keeps none: its stock is theirs together.', style: tkFormNow(t)),
        for (var i = 0; i < _drafts.length; i++)
          TkFieldset(
            legend: 'Choice ${i + 1}',
            children: [
              TkKitField(
                label: 'Its name',
                hint: 'Size, Colour, Material…',
                child: TkTextBox(controller: _drafts[i].name, max: 30),
              ),
              TkKitField(
                label: 'Its values',
                hint: 'With commas between: 40, 41, 42',
                child: TkTextBox(controller: _drafts[i].text),
              ),
              TkChipRow(
                padding: EdgeInsets.zero,
                all: false,
                label: 'Ready-made values for choice ${i + 1}',
                chips: [for (final p in presets) (label: p.label, tag: p.label)],
                on: null,
                onPick: (tag) {
                  final p = presets.firstWhere((x) => x.label == tag);
                  if (_drafts[i].name.text.trim().isEmpty) _drafts[i].name.text = p.name;
                  _drafts[i].text.text = p.values.join(', ');
                },
              ),
              if (_drafts.length > 1)
                Align(
                  alignment: Alignment.centerLeft,
                  child: TkLink(
                    label: 'Remove this choice',
                    onTap: () {
                      final gone = _drafts[i];
                      setState(() => _drafts.removeAt(i));
                      // Once its fields are gone from the screen.
                      WidgetsBinding.instance.addPostFrameCallback((_) {
                        gone.name.dispose();
                        gone.text.dispose();
                      });
                    },
                  ),
                ),
            ],
          ),
        if (_drafts.length < maxChoices)
          Align(
            alignment: Alignment.centerLeft,
            child: TkSoftButton(label: 'Add a choice', onTap: () => setState(() => _drafts.add(_draft(_drafts.length == 1 ? 'Colour' : '', '')))),
          ),
        if (cells.isNotEmpty) ...[
          // `.tk-grid-tools`: the number and Fill, side by side.
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Expanded(
                child: TkKitField(
                  label: 'Same number for all',
                  hint: 'Then change the ones that differ.',
                  child: TkTextBox(controller: _fill, digits: 6),
                ),
              ),
              const SizedBox(width: 10),
              Padding(
                padding: const EdgeInsets.only(bottom: 25),
                child: TkSoftButton(
                  label: 'Fill',
                  onTap: RegExp(r'^\d+$').hasMatch(_fill.text.trim())
                      ? () => setState(() {
                          for (final v in cells) {
                            _counts[keyOf(v)] = _fill.text.trim();
                          }
                        })
                      : null,
                ),
              ),
            ],
          ),
          TkChoiceRow(
            radio: false,
            control: TkCheckbox(checked: _own),
            label: Text('Some cost more or less than ${price != null ? naira(price) : 'the product'}', style: tkText(size: 15, weight: 600, color: t.text)),
            onTap: () => setState(() => _own = !_own),
          ),
          Text('How many of each. Empty a box for one you do not sell.', style: tkHint(t)),
          for (final g in gridGroups(cells, cs))
            TkFieldset(
              legend: g.label.isEmpty ? 'How many' : g.label,
              children: [
                GridCells(
                  children: [
                    for (final v in g.cells)
                      GridCell(
                        key: ValueKey(keyOf(v)),
                        name: v[cs.last.name] ?? '',
                        label: '${variantLabel(v, cs)}: how many',
                        hint: '–',
                        value: _countOf(v, cs),
                        onChanged: (x) => setState(() => _counts[keyOf(v)] = x),
                        price: _own
                            ? (
                                value: _priceOf(v, cs),
                                hint: price != null ? nairaText(price) : '₦',
                                label: '${variantLabel(v, cs)}: its own price in naira',
                                onChanged: (x) => setState(() => _prices[keyOf(v)] = x),
                              )
                            : null,
                      ),
                  ],
                ),
              ],
            ),
          Semantics(liveRegion: true, child: Text('${sizesWords(sold.length)} on sale', style: tkFormNow(t))),
          if (shelf > 0)
            Text(
              '$name has $shelf on the shelf now: they move into its sizes and colours, ${taken > shelf ? shelf : taken} counted in so far.'
              '${taken < shelf ? ' ${shelf - taken} not counted into one will be recorded as missing.' : ''}'
              '${taken > shelf ? ' ${taken - shelf} more than that will be recorded as new stock.' : ''}',
              style: tkFormNow(t),
            ),
        ],
        if (problem.isNotEmpty)
          Semantics(
            liveRegion: true,
            child: Text(problem, style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
        if (_was.isNotEmpty) ColourPhotos(auth: widget.auth, item: _it, pickers: widget.pickers),
        if (_was.isNotEmpty) _StopSizes(auth: widget.auth, item: _it, onSaved: widget.onSaved, dark: dark),
      ],
    );
  }
}

/// SizesForms.tsx ColourPhotos: a photo for each colour some size has,
/// shown when a customer picks it; saved at once.
class ColourPhotos extends StatefulWidget {
  const ColourPhotos({super.key, required this.auth, required this.item, required this.pickers});
  final Auth auth;
  final Map<String, dynamic> item;
  final Pickers pickers;

  @override
  State<ColourPhotos> createState() => _ColourPhotosState();
}

class _ColourPhotosState extends State<ColourPhotos> {
  List<Map<String, dynamic>>? _sizes;
  String? _busy;
  String? _error;
  String _said = '';

  Choice? get _colour => choicesOf(widget.item).where((c) => RegExp(r'^colou?rs?$', caseSensitive: false).hasMatch(c.name)).firstOrNull;

  @override
  void initState() {
    super.initState();
    if (_colour != null) _load();
  }

  Future<void> _load() async {
    try {
      final all = await _stock(widget.auth.transport);
      if (mounted) setState(() => _sizes = sizesOf(all, widget.item['id'] as String, all: true));
    } on Object {
      if (mounted) setState(() => _sizes = const []);
    }
  }

  String _path(String choice, String value) =>
      '/v1/stock/items/${Uri.encodeComponent(widget.item['id'] as String)}/variants/photo?choice=${Uri.encodeQueryComponent(choice)}&value=${Uri.encodeQueryComponent(value)}';

  Future<void> _run(String value, Future<void> Function() f, String line) async {
    setState(() => (_busy = value, _error = null));
    try {
      await f();
      _said = line;
      await _load();
    } on Object catch (e) {
      _error = errorWords(e);
    }
    if (mounted) setState(() => _busy = null);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final colour = _colour;
    if (colour == null) return const SizedBox.shrink();
    final word = colour.name.toLowerCase();
    List<Map<String, dynamic>> of(String value) => (_sizes ?? const []).where((z) => variantOf(z)?[colour.name] == value).toList();
    String? photoOf(String value) {
      final p = of(value).where((z) => z['photo'] is Map).map((z) => (z['photo'] as Map).cast<String, dynamic>()).firstOrNull;
      final u = p == null ? null : (p['320'] ?? p['800'] ?? p['2000']);
      return u is String ? '/v1/storefront/image?src=${Uri.encodeQueryComponent(u)}' : null;
    }

    return TkFieldset(
      legend: 'A photo for each $word',
      children: [
        Text('Your page shows it when a customer picks that $word. Saved at once.', style: tkHint(t)),
        for (final value in colour.values.where((v) => of(v).isNotEmpty))
          Container(
            padding: const EdgeInsets.symmetric(vertical: 8),
            decoration: BoxDecoration(
              border: Border(top: BorderSide(color: t.border)),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  children: [
                    ClipRRect(
                      borderRadius: BorderRadius.circular(10),
                      child: photoOf(value) == null
                          ? Container(width: 48, height: 48, color: t.panel2)
                          : Semantics(
                              image: true,
                              label: '$value ${widget.item['name']}',
                              child: ApiImage(transport: widget.auth.transport, path: photoOf(value)!, width: 48, height: 48),
                            ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Text(value, style: tkText(size: 15, weight: 600, color: t.text)),
                    ),
                  ],
                ),
                const SizedBox(height: 6),
                PhotoAdd(
                  pickers: widget.pickers,
                  room: 1,
                  busy: _busy == value,
                  onError: (w) => setState(() => _error = w),
                  onPick: (picks) {
                    final p = picks.first;
                    _run(value, () async {
                      final path = _path(colour.name, value);
                      if (p.url != null) {
                        await widget.auth.transport.send('POST', path, body: {'url': p.url!.trim()});
                      } else {
                        await widget.auth.transport.sendFile(path, bytes: p.file!.bytes, type: p.file!.type);
                      }
                    }, 'Photo for $value saved');
                  },
                ),
                if (photoOf(value) != null) ...[
                  const SizedBox(height: 6),
                  Align(
                    alignment: Alignment.centerLeft,
                    child: TkLink(
                      label: 'Remove',
                      onTap: _busy == value
                          ? null
                          : () => _run(value, () async => widget.auth.transport.send('DELETE', _path(colour.name, value)), 'Photo for $value taken out'),
                    ),
                  ),
                ],
              ],
            ),
          ),
        if (_error != null) TkErrorBox(_error!),
        if (_said.isNotEmpty) Semantics(liveRegion: true, child: Text(_said, style: tkFormNow(t))),
      ],
    );
  }
}

/// SizesForms.tsx StopSizes: all its sizes and colours off sale (kept).
class _StopSizes extends StatefulWidget {
  const _StopSizes({required this.auth, required this.item, required this.onSaved, required this.dark});
  final Auth auth;
  final Map<String, dynamic> item;
  final void Function(String? line) onSaved;
  final bool dark;

  @override
  State<_StopSizes> createState() => _StopSizesState();
}

class _StopSizesState extends State<_StopSizes> {
  bool _sure = false;
  String? _error;
  final _key = SaveKey();

  @override
  Widget build(BuildContext context) {
    if (!_sure) {
      return Align(
        alignment: Alignment.centerLeft,
        child: TkLink(label: 'Stop selling it in sizes and colours', onTap: () => setState(() => _sure = true)),
      );
    }
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Take all its sizes and colours off sale? Their sales stay in your records.', style: tkText(size: 15, color: t.text)),
        const SizedBox(height: 8),
        TkSoftButton(
          label: 'Yes, take them off sale',
          onTap: () async {
            try {
              await widget.auth.transport.send(
                'PUT',
                '/v1/stock/items/${Uri.encodeComponent(widget.item['id'] as String)}/variants',
                body: {'choices': <Object>[], 'cells': <Object>[]},
                headers: {'Idempotency-Key': _key.of(null), 'X-Taskiem-Diary': '1'},
              );
              widget.onSaved('${widget.item['name']}: no sizes and colours');
            } on Object catch (e) {
              if (mounted) setState(() => _error = errorWords(e));
            }
          },
        ),
        if (_error != null) ...[const SizedBox(height: 8), TkErrorBox(_error!)],
      ],
    );
  }
}

/// SizesForms.tsx GridStockIn: goods received by the grid, the low ones
/// marked.
class GridStockIn extends StatefulWidget {
  const GridStockIn({super.key, required this.auth, required this.item, required this.onSaved, this.canEdit = true});
  final Auth auth;
  final Map<String, dynamic> item;
  final void Function(String? line) onSaved;
  final bool canEdit;

  @override
  State<GridStockIn> createState() => _GridStockInState();
}

class _GridStockInState extends State<GridStockIn> {
  List<Map<String, dynamic>>? _all;
  String? _error;
  final _came = <String, String>{};
  final _key = SaveKey();

  @override
  void initState() {
    super.initState();
    _stock(widget.auth.transport).then(
      (x) {
        if (mounted) setState(() => _all = x);
      },
      onError: (Object e) {
        if (mounted) setState(() => _error = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.');
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final all = _all;
    if (all == null) {
      return _error != null ? Padding(padding: const EdgeInsets.all(20), child: TkErrorBox(_error!)) : const TkLoading(label: 'Loading its sizes and colours…');
    }
    final cs = choicesOf(widget.item);
    final sizes = sizesOf(all, widget.item['id'] as String).where((v) => variantOf(v) != null).toList();
    final byKey = {for (final v in sizes) keyOf(variantOf(v)!): v};
    final cells = [for (final v in sizes) variantOf(v)!];
    final lines = [
      for (final v in sizes)
        if ((int.tryParse((_came[v['id']] ?? '').trim()) ?? 0) > 0) {'item_id': v['id'], 'change': int.parse(_came[v['id']]!.trim())},
    ];
    final bad = _came.values.any((x) => x.trim().isNotEmpty && !RegExp(r'^\d+$').hasMatch(x.trim()));
    Future<String?> save() async {
      await widget.auth.transport.send(
        'POST',
        '/v1/stock/movements/grid',
        body: {'reason': 'received', 'lines': lines},
        headers: {'Idempotency-Key': _key.of(lines), 'X-Taskiem-Diary': '1'},
      );
      final n = lines.fold<int>(0, (a, l) => a + (l['change'] as int));
      return 'Stock in: ${widget.item['name']}, +$n';
    }

    return TkToolForm(
      save: save,
      onSaved: widget.onSaved,
      saveWords: 'Record stock in',
      ready: lines.isNotEmpty && !bad,
      canEdit: widget.canEdit,
      children: [
        Text('How many of each came in. The low ones are marked.', style: tkFormNow(t)),
        for (final g in gridGroups(cells, cs))
          TkFieldset(
            legend: g.label.isEmpty ? 'Came in' : g.label,
            children: [
              GridCells(
                children: [
                  for (final v in g.cells)
                    if (byKey[keyOf(v)] case final it?)
                      GridCell(
                        key: ValueKey(it['id']),
                        name: cs.isEmpty ? variantLabel(v, cs) : (v[cs.last.name] ?? ''),
                        small: '${_int(it['stock']) == null ? 'no count' : '${_int(it['stock'])} now'}${it['low'] == true ? ' · low' : ''}',
                        low: it['low'] == true,
                        label: '${variantLabel(v, cs)}: how many came in${it['low'] == true ? ' (low)' : ''}',
                        hint: '0',
                        value: _came[it['id']] ?? '',
                        onChanged: (x) => setState(() => _came[it['id'] as String] = x),
                      ),
                ],
              ),
            ],
          ),
        if (bad) Text('A box holds a whole number.', style: tkText(size: 13, weight: 600, color: t.danger)),
      ],
    );
  }
}

/// SizesForms.tsx GroupForm: items chosen in Products made one product's
/// sizes and colours; each keeps its own stock, history, price and links.
class GroupForm extends StatefulWidget {
  const GroupForm({super.key, required this.auth, required this.items, required this.onSaved, required this.onCancel});
  final Auth auth;
  final List<Map<String, dynamic>> items;
  final void Function(String? line) onSaved;
  final VoidCallback onCancel;

  @override
  State<GroupForm> createState() => _GroupFormState();
}

class _GroupFormState extends State<GroupForm> {
  late final _first = proposeGroup([for (final it in widget.items) (id: it['id'] as String, name: it['name'] as String? ?? '')]);
  late final _name = _ctl(_first.name);
  late final List<TextEditingController> _choices = [
    for (final c in (_first.choices.isEmpty ? const ['Size'] : _first.choices)) _ctl(c),
  ];
  late final Map<String, List<TextEditingController>> _values = {
    for (final it in widget.items)
      it['id'] as String: [
        for (final c in (_first.choices.isEmpty ? const ['Size'] : _first.choices)) _ctl(_first.values[it['id']]?[c] ?? ''),
      ],
  };
  late final _key = formKey();

  TextEditingController _ctl(String text) => TextEditingController(text: text)..addListener(() => setState(() {}));

  @override
  void dispose() {
    _name.dispose();
    for (final c in [..._choices, for (final v in _values.values) ...v]) {
      c.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final named = [for (final c in _choices) c.text.trim()];
    final filled = [
      for (final it in widget.items) {for (var i = 0; i < named.length; i++) named[i]: _values[it['id']]![i].text.trim()},
    ];
    final keys = [for (final v in filled) keyOf(v)];
    final problem = _name.text.trim().isEmpty
        ? 'Give the product a name.'
        : named.any((c) => c.isEmpty)
        ? 'Give each choice a name: Size, Colour…'
        : named.map((c) => c.toLowerCase()).toSet().length != named.length
        ? 'Each choice has its own name.'
        : filled.any((v) => v.values.any((x) => x.isEmpty))
        ? 'Say each item’s size and colour.'
        : keys.toSet().length != keys.length
        ? 'Two items have the same size and colour: change one.'
        : '';
    Future<String?> save() async {
      await widget.auth.transport.send(
        'POST',
        '/v1/stock/group',
        body: {
          'name': _name.text.trim(),
          'choices': named,
          'items': [
            for (var i = 0; i < widget.items.length; i++) {'item_id': widget.items[i]['id'], 'variant': filled[i]},
          ],
        },
        headers: {'Idempotency-Key': _key, 'X-Taskiem-Diary': '1'},
      );
      return '${_name.text.trim()}: ${sizesWords(widget.items.length)}';
    }

    return TkToolForm(
      save: save,
      onSaved: widget.onSaved,
      onCancel: widget.onCancel,
      saveWords: 'Make one product',
      ready: problem.isEmpty,
      children: [
        Text('These ${widget.items.length} become one product’s sizes and colours. Each keeps its own stock, history and price.', style: tkFormNow(t)),
        TkKitField(
          label: 'The product’s name',
          child: TkTextBox(controller: _name, max: 120),
        ),
        TkFieldset(
          legend: 'Its choices',
          children: [
            for (var i = 0; i < _choices.length; i++)
              TkKitField(
                label: 'Choice ${i + 1}',
                hint: 'Size, Colour, Style…',
                child: TkTextBox(controller: _choices[i], max: 30),
              ),
            if (_choices.length < maxChoices)
              Align(
                alignment: Alignment.centerLeft,
                child: TkSoftButton(
                  label: 'Add a choice',
                  onTap: () => setState(() {
                    _choices.add(_ctl(''));
                    for (final v in _values.values) {
                      v.add(_ctl(''));
                    }
                  }),
                ),
              ),
            if (_choices.length > 1)
              Align(
                alignment: Alignment.centerLeft,
                child: TkSoftButton(
                  label: 'Take away ${_choices.last.text.trim().isEmpty ? 'the last choice' : _choices.last.text.trim()}',
                  onTap: () => setState(() {
                    _choices.removeLast().dispose();
                    for (final v in _values.values) {
                      v.removeLast().dispose();
                    }
                  }),
                ),
              ),
          ],
        ),
        for (final it in widget.items)
          TkFieldset(
            legend: it['name'] as String? ?? '',
            children: [
              for (var i = 0; i < _choices.length; i++)
                TkKitField(
                  label: _choices[i].text.trim().isEmpty ? 'Choice ${i + 1}' : _choices[i].text.trim(),
                  child: TkTextBox(controller: _values[it['id']]![i], max: 30),
                ),
            ],
          ),
        if (problem.isNotEmpty) Semantics(liveRegion: true, child: Text(problem, style: tkFormNow(t))),
      ],
    );
  }
}
