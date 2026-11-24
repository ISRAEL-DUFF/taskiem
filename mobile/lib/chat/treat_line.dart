// The question on a purchase line (docs/spec/what-a-business-buys.md §5,
// §6): "<item> from <supplier>: How should we treat it?" Just record it,
// Add to stock (a stock item, or a new one asking Do you sell it?) or Keep
// track of it, and its label (typed or picked: the business's labels by
// use, then suggestions; typing makes one). Asked where a line is ordered
// or confirmed; a line remembered shows in a few words, changed for this
// order or remembered for the next. Also a line's small mark on an order
// or a bill (its label, and To stock or Things we have).

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show ItemMemory, SpendLabel, SpendLabelName, StockItemRow;
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/forms.dart' show TkSwitchRow;
import '../kit/icon.dart' show TkIcon;
import '../kit/kit.dart' show TkAppLook, TkSmallButton;
import '../kit/list.dart' show TkSearchBox;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/modal.dart' show showTkToast;
import '../kit/mobile.dart' show TkFormGroup, TkFormSheet, TkListRow, TkPanel, TkSection, TkTick;
import '../kit/sheet.dart' show TkCta, TkGhostButton, TkKitField, TkPickBox, TkSheet, showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'groups.dart' show choiceMatches, pickOneOf;
import 'stock_items.dart' show soldAsLine;
import 'stock_page.dart' show stockQuantity;

String _str(Object? v) => v as String? ?? '';
int _int(Object? v) => (v as num?)?.toInt() ?? 0;

/// How a line is treated when it arrives, and its label: [how] is record,
/// stock (into [stockItemId], [uses] of it a line) or track. A label named
/// with no [labelId] is made when the line is saved.
class Treat {
  const Treat({
    this.how = 'record',
    this.stockItemId,
    this.stockItem = '',
    this.uses = 1,
    this.labelId,
    this.label = '',
    this.usesKept = false,
    this.usesAsked = false,
    this.usesOk = true,
  });
  final String how;
  final String? stockItemId;
  final String stockItem;
  final int uses;
  final String? labelId;
  final String label;

  /// [uses] is the stock item's own, as remembered or on the order line:
  /// sent though not asked here, until the stock item changes.
  final bool usesKept;

  /// How many of the stock item one is was asked ([askUses]); [usesOk]
  /// whether it is answered with a whole number (H2 review fix 2).
  final bool usesAsked;
  final bool usesOk;

  /// As the business remembers a supplier's item.
  factory Treat.ofMemory(ItemMemory m) =>
      Treat(how: m.treatment, stockItemId: m.stockItemId, stockItem: m.stockItem ?? '', uses: m.uses < 1 ? 1 : m.uses, labelId: m.labelId, label: m.label ?? '', usesKept: m.uses > 1);

  /// As an order's line has it, names from what the question offers. Its
  /// label is the server's name of the label kept ([lineLabel]); a line
  /// still to confirm with none pre-selects the server's suggestion for
  /// its name, made when it is confirmed (§6).
  factory Treat.ofLine(Map<String, dynamic> l, TreatChoices c) {
    var labelId = l['label_id'] as String?;
    var label = lineLabel(l, c);
    if ((labelId ?? '').isEmpty && l['unconfirmed'] == true) {
      final s = c.suggestedLabel(_str(l['item']));
      if (s != null) (labelId, label) = (s.id, s.name);
    }
    return Treat(
      how: _str(l['treatment']).isEmpty ? 'record' : _str(l['treatment']),
      stockItemId: l['stock_item_id'] as String?,
      stockItem: c.stockName(l['stock_item_id'] as String?),
      uses: _int(l['uses']) < 1 ? 1 : _int(l['uses']),
      labelId: labelId,
      label: label,
      usesKept: _int(l['uses']) > 1,
      // Asked of its stock item already, as the question will show it: a
      // line read again is never a change of the person's.
      usesAsked: _str(l['treatment']) == 'stock' && askUses(_str(l['unit']), c.stockUnit(l['stock_item_id'] as String?), _int(l['uses'])),
      usesOk: !(_str(l['treatment']) == 'stock' && askUses(_str(l['unit']), c.stockUnit(l['stock_item_id'] as String?), _int(l['uses']))),
    );
  }

  Treat copyWith({
    String? how,
    String? stockItemId,
    String? stockItem,
    int? uses,
    String? labelId,
    String? label,
    bool noLabel = false,
    bool? usesKept,
    bool? usesAsked,
    bool? usesOk,
  }) {
    // Another stock item drops how many of the last one a line was (H2
    // review fix 3): it is asked again of the new one, if at all.
    final other = stockItemId != null && stockItemId != this.stockItemId;
    return Treat(
      how: how ?? this.how,
      stockItemId: stockItemId ?? this.stockItemId,
      stockItem: stockItem ?? this.stockItem,
      uses: uses ?? (other ? 1 : this.uses),
      labelId: noLabel ? null : (labelId ?? this.labelId),
      label: noLabel ? '' : (label ?? this.label),
      usesKept: usesKept ?? (other ? false : this.usesKept),
      usesAsked: usesAsked ?? this.usesAsked,
      usesOk: usesOk ?? (other ? !(usesAsked ?? this.usesAsked) : this.usesOk),
    );
  }

  /// Whether it can be saved: a stock line names its stock item, and
  /// answers how many of it one is when that was asked.
  bool get ready => how != 'stock' || ((stockItemId ?? '').isNotEmpty && (!usesAsked || usesOk));

  bool same(Treat o) => how == o.how && (how != 'stock' || (stockItemId == o.stockItemId && uses == o.uses)) && labelId == o.labelId && label == o.label;

  /// The line's fields in a request (treatment, stock_item_id, uses,
  /// label_id), once its label is made.
  Map<String, Object?> fields() => {
    'treatment': how,
    if (how == 'stock') 'stock_item_id': stockItemId,
    // Only what was asked and answered, or the stock item's own.
    if (how == 'stock' && uses > 1 && (usesAsked || usesKept)) 'uses': uses,
    if ((labelId ?? '').isNotEmpty) 'label_id': labelId,
  };
}

/// A line's label as cards show it: the server's name of the label kept
/// (`label`: a label merged since shows as the one kept), else its id among
/// the business's labels; '' for none.
String lineLabel(Map<String, dynamic> l, TreatChoices c) {
  final s = _str(l['label']);
  return s.isNotEmpty ? s : c.labelName(l['label_id'] as String?);
}

/// A unit word for one: bags, bag; boxes, box (stock.Singular).
String singularUnit(String u) {
  final l = u.toLowerCase();
  if (l.length > 3 && l.endsWith('ies')) return '${u.substring(0, u.length - 3)}y';
  if (['xes', 'ches', 'shes', 'sses'].any(l.endsWith)) return u.substring(0, u.length - 2);
  if (l.length > 2 && l.endsWith('s') && !l.endsWith('ss')) return u.substring(0, u.length - 1);
  return u;
}

const _countUnits = {'', 'piece', 'pc', 'unit', 'item', 'each', 'one', 'no'};

/// Units that measure rather than count (stock.measureUnits).
const _measureUnits = {
  'g', 'gram', 'kg', 'kilo', 'kilogram', 'mg', 'ml', 'cl', 'l', 'litre', 'yard', 'metre', 'meter', 'm', 'cm', 'mm', 'inch', 'foot', 'feet', 'ft', 'lb', 'oz', //
};

String _unitKey(String? u) => singularUnit((u ?? '').trim()).toLowerCase();

/// Whether a line bought in [lineUnit], going into a stock item counted in
/// [stockUnit], asks how many of the stock item one of it is: counted in
/// another unit (a bag into grams); a line with no unit asks only of a
/// stock item measured (grams, yards). The server's stock.AskUses and the
/// web's askUses, by the same rule.
bool askUses(String? lineUnit, String? stockUnit, [int uses = 1]) {
  if (uses > 1) return false;
  final s = _unitKey(stockUnit);
  final l = _unitKey(lineUnit);
  if (_countUnits.contains(s) || l == s) return false;
  return l.isNotEmpty || _measureUnits.contains(s);
}

/// "How many g is one bag?" (one of the item when the line has no unit).
String usesQuestion(String stockUnit, String? lineUnit, String item) =>
    'How many $stockUnit is one ${(lineUnit ?? '').trim().isEmpty ? item : singularUnit(lineUnit!.trim())}?';

/// The three answers, as said.
String treatWords(String how) => switch (how) {
  'stock' => 'Add to stock',
  'track' => 'Keep track of it',
  _ => 'Just record it',
};

/// A treatment in a few words: "Add to stock: Ankara fabric · Fabric".
String treatLine(Treat t) =>
    [t.how == 'stock' && t.stockItem.isNotEmpty ? 'Add to stock: ${t.stockItem}${t.uses > 1 ? ' (${t.uses} each)' : ''}' : treatWords(t.how), t.label.isEmpty ? 'Not labelled' : t.label].join(' · ');

/// What the question offers: the business's stock items and labels, read
/// once for a sheet or a page.
class TreatChoices extends ChangeNotifier {
  TreatChoices(this.auth);
  final Auth auth;
  List<StockItemRow>? stock;
  List<SpendLabel>? labels;
  bool failed = false;

  Future<void> load() async {
    failed = false;
    await Future.wait([
      () async {
        try {
          stock = (await auth.api.listStockItems()).items.where((r) => !r.retired).toList();
        } on Object {
          stock ??= const [];
        }
      }(),
      () async {
        try {
          labels = (await auth.api.listSpendLabels()).labels;
        } on Object {
          labels ??= const [];
          failed = true;
        }
      }(),
    ]);
    notifyListeners();
  }

  bool get ready => stock != null && labels != null;

  /// A stock item's unit (null when none is chosen, or not known).
  String? stockUnit(String? id) => id == null ? null : stock?.where((r) => r.id == id).map((r) => r.unit).firstOrNull;

  String stockName(String? id) => id == null ? '' : (stock?.where((r) => r.id == id).map((r) => r.name).firstOrNull ?? '');
  String labelName(String? id) => id == null ? '' : (labels?.where((l) => l.id == id).map((l) => l.name).firstOrNull ?? '');

  /// A first guess for an item never ordered before (§6): a stock item of
  /// the same name is Add to stock, else Just record it; the label is the
  /// server's first suggestion for the name ([suggestedLabel]: the
  /// business's own, or a new name, made when the line is saved), and
  /// until it comes one of the business's sharing a word with the name.
  Treat suggest(String item, {String? unit}) {
    final k = item.trim().toLowerCase();
    final s = stock?.where((r) => r.name.trim().toLowerCase() == k).firstOrNull;
    // Into a stock item of its name counted in another unit: asked.
    final asked = s != null && askUses(unit, s.unit);
    final server = suggestedLabel(item);
    if (server != null) {
      return Treat(how: s == null ? 'record' : 'stock', stockItemId: s?.id, stockItem: s?.name ?? '', labelId: server.id, label: server.name, usesAsked: asked, usesOk: !asked);
    }
    final words = k.split(RegExp('[^a-z0-9]+')).where((w) => w.length > 2).toSet();
    final l = labels?.where((l) => l.name.toLowerCase().split(RegExp('[^a-z0-9]+')).any(words.contains)).firstOrNull;
    return Treat(how: s == null ? 'record' : 'stock', stockItemId: s?.id, stockItem: s?.name ?? '', labelId: l?.id, label: l?.name ?? '', usesAsked: asked, usesOk: !asked);
  }

  /// The server's suggestions, by name (null: none, or not come yet).
  final Map<String, ({String? id, String name})?> _suggested = {};
  bool _gone = false;

  /// The server's first label suggestion for [item]
  /// (`GET /v1/money/labels?item=`): asked once per name, listeners told
  /// when it comes. Its id is none for a name not yet a label.
  ({String? id, String name})? suggestedLabel(String item) {
    final k = item.trim().toLowerCase();
    if (k.isEmpty) return null;
    if (!_suggested.containsKey(k)) {
      _suggested[k] = null;
      () async {
        try {
          final r = await auth.api.listSpendLabels(item: item.trim());
          final f = r.suggestions.firstOrNull;
          if (f == null || _gone) return;
          _suggested[k] = (id: f.labelId, name: f.name);
          notifyListeners();
        } on Object {
          // No suggestion: the guess by words stands.
        }
      }();
    }
    return _suggested[k];
  }

  @override
  void dispose() {
    _gone = true;
    super.dispose();
  }

  /// The label's id, making a label typed as new.
  Future<Treat> made(Treat t) async {
    if ((t.labelId ?? '').isNotEmpty || t.label.trim().isEmpty) return t;
    final l = await auth.api.makeSpendLabel(body: SpendLabelName(name: t.label.trim()));
    if (!(labels ?? const []).any((x) => x.id == l.id)) labels = [...?labels, l];
    return t.copyWith(labelId: l.id, label: l.name);
  }

  /// [made], or the answer without its label when the label cannot be
  /// made (refused, or failed): the line still goes.
  Future<Treat> madeOrNone(Treat t) async {
    try {
      return await made(t);
    } on ApiError {
      return t.copyWith(noLabel: true);
    }
  }

  /// The new labels shown on each item's question, as the person saw them.
  final Map<String, String> _shown = {};

  /// The question on [item] showed [label] (a name not yet a label).
  void shown(String item, String label) => _shown[item.trim().toLowerCase()] = label;

  /// [t] as the person saw it: a label not yet made that they were never
  /// shown on [item] is left off.
  Treat seenOnly(String item, Treat t) {
    if ((t.labelId ?? '').isNotEmpty || t.label.isEmpty || _shown[item.trim().toLowerCase()] == t.label) return t;
    return t.copyWith(noLabel: true);
  }

  void added(StockItemRow? r) {
    if (r != null) stock = [...?stock, r];
    notifyListeners();
  }
}

/// 409 track_ask: "Put 500 nylon bags on Things we have, one row each?"
/// True: put them on; false: just record them; null: put away.
Future<bool?> askTrackMany(BuildContext context, ApiError e) => showTkSheet<bool>(
  context,
  (context) => TkAppLook(
    child: TkSheet(
      title: 'Put ${_int(e.body['quantity'])} ${_str(e.body['item'])} on Things we have, one row each?',
      bareFooter: true,
      footer: Row(
        children: [
          Expanded(
            child: TkGhostButton(label: 'Just record them', onTap: () => Navigator.of(context).pop(false)),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: TkCta(label: 'Put them on', onTap: () => Navigator.of(context).pop(true)),
          ),
        ],
      ),
      body: ListView(
        shrinkWrap: true,
        padding: const EdgeInsets.fromLTRB(18, 0, 18, 16),
        children: [
          Text('Each one gets its own row, to tell them apart later. Most businesses just record many small things.', style: tkText(size: 15, lineHeight: 1.45, color: TkTheme.tokens(context).text)),
        ],
      ),
    ),
  ),
);

/// The server's answer to a line that cannot be tracked: 409 track_ask,
/// 422 track_too_many or 409 line_locked; null for any other.
String? treatCode(Object e) => e is ApiError && const {'track_ask', 'track_too_many', 'line_locked'}.contains(e.body['code']) ? e.body['code'] as String : null;

/// Words for 422 track_too_many and 409 line_locked, as a toast.
String treatRefusal(ApiError e) => switch (e.body['code']) {
  'track_too_many' => '${_int(e.body['quantity'])} ${_str(e.body['item'])} are too many to keep track of one by one, so they are just recorded.',
  'line_locked' => 'Something on this line came already, so how it is treated stays.',
  _ => e.message,
};

/// The question on one line. Asked ([asked]): the three answers and the
/// label, open. Remembered: in a few words with Change, which opens it for
/// this order, with Remember for next time when [onRemember] is given.
class TreatQuestion extends StatefulWidget {
  const TreatQuestion({
    super.key,
    required this.item,
    required this.supplier,
    required this.value,
    required this.choices,
    required this.onChanged,
    this.asked = true,
    this.remember = false,
    this.onRemember,
    this.locked = false,
    this.foot,
    this.quantity,
    this.unit,
  });
  final String item;
  final String supplier;

  /// The unit the line is bought in ("bag"): a stock item counted in
  /// another asks how many of it one is ([askUses]).
  final String? unit;
  final Treat value;
  final TreatChoices choices;

  /// Null: shown, not changed (the person may not).
  final ValueChanged<Treat>? onChanged;

  /// Unconfirmed: open from the start.
  final bool asked;
  final bool remember;
  final ValueChanged<bool>? onRemember;

  /// Something on it came: shown, never changed.
  final bool locked;

  /// Under the question when open (a line's Save).
  final Widget? foot;

  /// How many are ordered, to say what Keep track of it makes.
  final int? quantity;

  @override
  State<TreatQuestion> createState() => _TreatQuestionState();
}

class _TreatQuestionState extends State<TreatQuestion> {
  late bool _open = widget.asked;

  /// How many of the stock item one is, as typed.
  late final _uses = TextEditingController(text: widget.value.uses > 1 ? '${widget.value.uses}' : '');

  @override
  void initState() {
    super.initState();
    _uses.addListener(() {
      if (_quiet) return;
      final n = _usesTyped;
      _set(_v.copyWith(uses: n ?? 1, usesOk: n != null));
      setState(() {});
    });
  }

  /// While the box is cleared for another stock item: no answer from it.
  bool _quiet = false;

  @override
  void dispose() {
    _uses.dispose();
    super.dispose();
  }

  /// The whole number typed (commas only as thousands), else null.
  int? get _usesTyped {
    final raw = _uses.text.trim();
    if (!RegExp(r'^(\d{1,3}(,\d{3})+|\d+)$').hasMatch(raw)) return null;
    final n = int.tryParse(raw.replaceAll(',', ''));
    return n != null && n >= 1 && n <= 1000000 ? n : null;
  }

  bool get _badUses => _uses.text.trim().isNotEmpty && _usesTyped == null;

  /// Whether this answer asks how many of its stock item one is.
  bool get _asks => _stockUnit != null && askUses(widget.unit, _stockUnit);

  /// Keeps the answer's [Treat.usesAsked] as the question shows it, after
  /// the frame (a sheet's Save waits for it: [Treat.ready]).
  void _syncAsked() {
    if (!_open || widget.locked) return;
    final ask = _asks;
    if (ask == _v.usesAsked || widget.onChanged == null) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || _asks == _v.usesAsked) return;
      _set(_v.copyWith(usesAsked: _asks, usesOk: !_asks || _usesTyped != null));
    });
  }

  /// Another stock item: its box emptied, asked afresh (review fix 3).
  void _otherStock(String id, String name) {
    if (id != _v.stockItemId) {
      _quiet = true;
      _uses.clear();
      _quiet = false;
    }
    _set(_v.copyWith(how: 'stock', stockItemId: id, stockItem: name));
  }

  /// The chosen stock item's unit, when one is chosen.
  String? get _stockUnit => _v.how == 'stock' ? widget.choices.stock?.where((r) => r.id == _v.stockItemId).map((r) => r.unit).firstOrNull : null;

  @override
  void didUpdateWidget(TreatQuestion old) {
    super.didUpdateWidget(old);
    if (widget.asked && !old.asked) _open = true;
    // Answered (confirmed, and read again): shut, in a few words.
    if (!widget.asked && old.asked) _open = false;
  }

  Treat get _v => widget.value;

  void _set(Treat t) => widget.onChanged?.call(t);

  Future<void> _pickStock() async {
    final c = widget.choices;
    final items = c.stock ?? const <StockItemRow>[];
    final pick = await pickOneOf(
      context,
      title: 'Which stock item?',
      value: _v.stockItemId,
      searchHint: items.length > 8 ? 'Search your stock items' : null,
      choices: [('new', 'Make a new one', 'Asks whether you sell it'), for (final r in items) (r.id, r.name, '${stockQuantity(r.count, r.unit)} · ${soldAsLine(r)}')],
    );
    if (pick == null || !mounted) return;
    if (pick != 'new') {
      _otherStock(pick, c.stockName(pick));
      return;
    }
    final made = await showTkSheet<({String id, String name})>(context, (_) => NewStockItemSheet(auth: c.auth, name: widget.item));
    if (made == null || !mounted) return;
    // Read again, so it is offered on the other lines too.
    await c.load();
    _otherStock(made.id, made.name);
  }

  Future<void> _pickLabel() async {
    final got = await showTkSheet<({String? id, String name})>(context, (_) => LabelPickSheet(choices: widget.choices, item: widget.item, now: _v.labelId));
    if (got == null || !mounted) return;
    _set(got.name.isEmpty ? _v.copyWith(noLabel: true) : _v.copyWith(labelId: got.id ?? '', label: got.name));
  }

  Widget _answer(String how, String sub) {
    final on = _v.how == how;
    return Semantics(
      selected: on,
      inMutuallyExclusiveGroup: true,
      child: TkListRow(
        title: treatWords(how),
        sub: sub,
        icon: switch (how) {
          'stock' => 'b-box',
          'track' => 'b-hash',
          _ => 'b-receipt',
        },
        square: switch (how) {
          'stock' => TkLook.of(context).teal,
          'track' => TkLook.of(context).violet,
          _ => TkLook.of(context).blue,
        },
        chevron: false,
        trailing: TkTick(on),
        semanticsLabel: '${treatWords(how)}: ${widget.item}',
        onTap: widget.onChanged == null ? null : () => _set(_v.copyWith(how: how)),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    _syncAsked();
    // A new label shown on the line, so confirming may make it.
    if ((_v.labelId ?? '').isEmpty && _v.label.isNotEmpty) widget.choices.shown(widget.item, _v.label);
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final head = Text.rich(
      TextSpan(
        children: [
          TextSpan(
            text: widget.item,
            style: tkText(size: 15, weight: 700, color: t.text),
          ),
          TextSpan(
            text: ' from ',
            style: tkText(size: 15, color: t.muted),
          ),
          TextSpan(
            text: widget.supplier,
            style: tkText(size: 15, weight: 700, color: t.text),
          ),
        ],
      ),
    );
    final open = _open && !widget.locked;
    if (!open) {
      return Container(
        padding: const EdgeInsets.fromLTRB(14, 12, 10, 12),
        decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
        child: Row(
          children: [
            Expanded(
              child: MergeSemantics(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    head,
                    const SizedBox(height: 2),
                    Text(widget.locked ? '${treatLine(_v)} · it came, so it stays' : treatLine(_v), style: tkText(size: 13, lineHeight: 1.35, color: t.muted)),
                  ],
                ),
              ),
            ),
            if (!widget.locked && widget.onChanged != null) ...[
              const SizedBox(width: 10),
              TkSmallButton(label: 'Change', semantics: 'Change how ${widget.item} is treated', onPressed: () => setState(() => _open = true)),
            ],
          ],
        ),
      );
    }
    final q = widget.quantity;
    return TkFormGroup(
      children: [
        MergeSemantics(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              head,
              const SizedBox(height: 2),
              Text('How should we treat it?', style: tkText(size: 13, color: t.muted)),
            ],
          ),
        ),
        Semantics(
          container: true,
          label: 'How should we treat ${widget.item}?',
          explicitChildNodes: true,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _answer('record', 'An expense; nothing is counted'),
              _answer('stock', _v.how == 'stock' && _v.stockItem.isNotEmpty ? 'Into ${_v.stockItem}' : 'Its count rises when it comes'),
              _answer('track', q != null && q > 1 ? 'Each of the $q on Things we have' : 'Put on Things we have'),
            ],
          ),
        ),
        if (_v.how == 'stock')
          TkKitField(
            field: false,
            label: 'Stock item',
            child: TkPickBox(label: 'Stock item for ${widget.item}', shown: _v.stockItem.isEmpty ? 'Choose…' : _v.stockItem, onTap: _pickStock),
          ),
        if (_asks)
          TkKitField(
            label: usesQuestion(_stockUnit!, widget.unit, widget.item),
            hint: _badUses ? 'A whole number, like 1000.' : '${_v.stockItem.isEmpty ? 'It' : _v.stockItem} is counted in $_stockUnit. Said once, it is remembered.',
            child: TkTextBox(controller: _uses, max: 9, plain: true, size: 15, height: 46, hint: 'Like 1000'),
          ),
        TkKitField(
          field: false,
          label: 'Label',
          child: TkPickBox(label: 'Label for ${widget.item}', shown: _v.label.isEmpty ? 'Not labelled' : _v.label, onTap: _pickLabel),
        ),
        if (widget.onRemember != null) TkSwitchRow(label: 'Remember for next time', on: widget.remember, onChanged: widget.onRemember),
        ?widget.foot,
      ],
    );
  }
}

/// Picks a line's label: typed (a new one made), the business's labels by
/// use, then Taskiem's suggestions for the item; or none. Pops with the
/// label (no id for a suggestion not made yet), or an empty name for none.
class LabelPickSheet extends StatefulWidget {
  const LabelPickSheet({super.key, required this.choices, required this.item, this.now});
  final TreatChoices choices;
  final String item;
  final String? now;

  @override
  State<LabelPickSheet> createState() => _LabelPickSheetState();
}

class _LabelPickSheetState extends State<LabelPickSheet> {
  final _q = TextEditingController();
  List<({String? id, String name})> _suggested = const [];
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _q.addListener(() => setState(() {}));
    _suggest();
  }

  @override
  void dispose() {
    _q.dispose();
    super.dispose();
  }

  Future<void> _suggest() async {
    try {
      final r = await widget.choices.auth.api.listSpendLabels(item: widget.item);
      if (mounted) setState(() => _suggested = [for (final s in r.suggestions) (id: s.labelId, name: s.name)]);
    } on Object {
      // Suggestions are a help; the labels are there without them.
    }
  }

  /// A label typed or suggested and not made yet is made now.
  Future<void> _take(String? id, String name) async {
    if ((id ?? '').isNotEmpty || name.isEmpty) {
      Navigator.of(context).pop((id: id, name: name));
      return;
    }
    setState(() => (_busy = true, _error = null));
    try {
      final t = await widget.choices.made(Treat(label: name));
      if (mounted) Navigator.of(context).pop((id: t.labelId, name: t.label));
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  Widget _row(String? id, String name, String sub, {String? icon, VoidCallback? onTap}) {
    final look = TkLook.of(context);
    return Semantics(
      selected: id != null && id == widget.now,
      child: TkListRow(
        title: name,
        sub: sub,
        icon: icon,
        letters: icon == null ? name : null,
        square: icon == null ? null : look.lime,
        chevron: false,
        onTap: _busy ? null : (onTap ?? () => _take(id, name)),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final typed = _q.text.trim();
    final labels = (widget.choices.labels ?? const <SpendLabel>[]).where((l) => choiceMatches(l.name, typed)).toList();
    final have = {for (final l in widget.choices.labels ?? const <SpendLabel>[]) l.name.toLowerCase()};
    final suggested = _suggested.where((s) => s.id == null && !have.contains(s.name.toLowerCase()) && choiceMatches(s.name, typed)).toList();
    final ownSuggested = {
      for (final s in _suggested)
        if (s.id != null) s.id,
    };
    final exact = have.contains(typed.toLowerCase());
    return TkAppLook(
      child: TkSheet(
        title: 'Label',
        crumb: widget.item,
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            TkSearchBox(controller: _q, hint: 'Type or pick a label', label: 'Type or pick a label', margin: const EdgeInsets.only(bottom: 12)),
            if (_error != null) ...[
              TkCallout(
                leading: TkSquare('b-alert', color: look.red),
                title: _error!,
                color: look.red,
                subLines: 3,
              ),
              const SizedBox(height: 12),
            ],
            if (typed.isNotEmpty && !exact) ...[
              TkPanel(children: [_row(null, typed.length > 60 ? typed.substring(0, 60) : typed, 'Make this label', icon: 'b-plus')]),
              const SizedBox(height: 12),
            ],
            if (labels.isNotEmpty)
              TkSection(
                title: 'Your labels',
                children: [
                  for (final l in labels)
                    _row(l.id, l.name, [if (ownSuggested.contains(l.id)) 'Suggested', l.uses == 1 ? 'Used once' : (l.uses > 1 ? 'Used ${l.uses} times' : 'Not used yet')].join(' · ')),
                ],
              ),
            if (suggested.isNotEmpty) TkSection(title: 'Suggested', children: [for (final s in suggested) _row(null, s.name, 'A new label')]),
            const SizedBox(height: 12),
            TkPanel(
              children: [_row(null, 'No label', 'Its spending shows as Not labelled', icon: 'b-x', onTap: () => Navigator.of(context).pop((id: null, name: '')))],
            ),
            const SizedBox(height: 8),
            Text('Spending is grouped by label in your money reports.', style: tkText(size: 13, color: t.muted)),
          ],
        ),
      ),
    );
  }
}

/// A new stock item made from a line (§5.2), asking Do you sell it? first.
/// Pops with its stock item.
class NewStockItemSheet extends StatefulWidget {
  const NewStockItemSheet({super.key, required this.auth, required this.name});
  final Auth auth;
  final String name;

  @override
  State<NewStockItemSheet> createState() => _NewStockItemSheetState();
}

class _NewStockItemSheetState extends State<NewStockItemSheet> {
  bool? _sell;
  late final _name = TextEditingController(text: widget.name);
  final _unit = TextEditingController();
  late final _key = uuid4();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    for (final c in [_name, _unit]) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    _name.dispose();
    _unit.dispose();
    super.dispose();
  }

  String? get _problem => _sell == null
      ? 'Say whether you sell it.'
      : _name.text.trim().isEmpty
      ? 'Give it a name.'
      : null;

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = (await widget.auth.transport.send(
        'POST',
        '/v1/stock/items',
        body: {'name': _name.text.trim(), 'quantity': 0, 'unit': _unit.text.trim(), if (!_sell!) 'material': true},
        headers: {'Idempotency-Key': _key},
      ) as Map).cast<String, dynamic>();
      final it = (r['item'] as Map).cast<String, dynamic>();
      final id = _str(it['stock_item_id']).isNotEmpty ? _str(it['stock_item_id']) : _str(it['id']);
      if (mounted) Navigator.of(context).pop((id: id, name: _str(it['name']).isEmpty ? _name.text.trim() : _str(it['name'])));
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  Widget _choice(bool yes, String title, String sub) => Semantics(
    selected: _sell == yes,
    child: TkListRow(
      title: title,
      sub: sub,
      icon: yes ? 'b-store' : 'b-box',
      square: yes ? TkLook.of(context).teal : TkLook.of(context).orange,
      chevron: false,
      trailing: TkTick(_sell == yes),
      onTap: () => setState(() => _sell = yes),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final problem = _problem;
    return TkFormSheet(
      title: 'A new stock item',
      crumb: 'Stock',
      action: _busy ? 'Making…' : 'Make it',
      onAction: _busy || problem != null ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(title: 'Do you sell it?', children: [_choice(true, 'Yes, I sell it', 'It goes in your products too'), _choice(false, 'No, I don’t', 'Counted only, as fabric or packaging')]),
        TkFormGroup(
          children: [
            TkKitField(
              label: 'Name',
              child: TkTextBox(controller: _name, max: 120, size: 15, height: 46),
            ),
            TkKitField(
              label: 'Unit',
              hint: 'yard, carton',
              child: TkTextBox(controller: _unit, max: 30, size: 15, height: 46),
            ),
          ],
        ),
        if (_error != null)
          TkCallout(
            leading: TkSquare('b-alert', color: look.red),
            title: _error!,
            color: look.red,
            subLines: 3,
          ),
      ],
    );
  }
}

/// A line's mark on an order or a bill (§9): its label, and To stock or
/// Things we have when it goes there. Nothing for a line just recorded and
/// not labelled.
class LineMark extends StatelessWidget {
  const LineMark({super.key, required this.how, required this.label});
  final String how;
  final String label;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    Widget pill(String words, Color fill, Color ink, String icon) => Container(
      padding: const EdgeInsets.fromLTRB(6, 2, 8, 2),
      decoration: BoxDecoration(color: fill.withValues(alpha: 0.16), borderRadius: BorderRadius.circular(999)),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          TkIcon(icon, size: 12, stroke: 2.2, color: ink),
          const SizedBox(width: 4),
          Text(words, maxLines: 1, style: tkText(size: 11.5, weight: 700, color: ink)),
        ],
      ),
    );
    return Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Wrap(
        spacing: 6,
        runSpacing: 4,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          if (how == 'stock') pill('To stock', look.teal, look.tealInk, 'b-box'),
          if (how == 'track') pill('Things we have', look.violet, look.violetInk, 'b-hash'),
          Text(label.isEmpty ? 'Not labelled' : label, style: tkText(size: 12.5, weight: 600, color: label.isEmpty ? t.muted : t.text)),
        ],
      ),
    );
  }
}

/// The lines of a supplier's draft confirmed (`confirm-proposed`'s
/// `lines`): each line changed or asked, with its fields (a label typed as
/// new made first); null for one taken as it is.
///
/// A label not yet made is made only when the person saw it on the line
/// (a suggestion that came after they looked is left off), and a label
/// that cannot be made (making one needs invoice.manage or table.edit,
/// more than confirming does) leaves the line without it rather than
/// failing the whole confirm (H2 review fix 5).
Future<List<Map<String, Object?>?>> confirmLines(TreatChoices c, List<Map<String, dynamic>> lines, Map<int, Treat> chosen) async => [
  for (var i = 0; i < lines.length; i++)
    if (chosen[i] case final t?)
      (await c.madeOrNone(t)).fields()
    else if (lines[i]['unconfirmed'] == true)
      (await c.madeOrNone(c.seenOnly(_str(lines[i]['item']), Treat.ofLine(lines[i], c)))).fields()
    else
      null,
];

/// The question on a line of an order not yet received (§6, "After it
/// arrived"): asked when unconfirmed, with Confirm; a remembered one in a
/// few words with Change, then Save for this order, or remembered for next
/// time. Saved through `PATCH …/lines/{position}`; tracking many asks
/// first; once something came, it stays.
class OpenLineQuestion extends StatefulWidget {
  const OpenLineQuestion({super.key, required this.auth, required this.purchase, required this.line, required this.choices, required this.onSaved, this.canChange = true});
  final Auth auth;
  final Map<String, dynamic> purchase;
  final Map<String, dynamic> line;
  final TreatChoices choices;
  final VoidCallback onSaved;
  final bool canChange;

  @override
  State<OpenLineQuestion> createState() => _OpenLineQuestionState();
}

class _OpenLineQuestionState extends State<OpenLineQuestion> {
  Treat? _now;
  bool _remember = false;
  bool _busy = false;

  Map<String, dynamic> get _l => widget.line;
  bool get _unconfirmed => _l['unconfirmed'] == true;
  Treat get _was => Treat.ofLine(_l, widget.choices);
  Treat get _v => _now ?? _was;

  Future<void> _save({bool many = false}) async {
    setState(() => _busy = true);
    try {
      final tr = await widget.choices.madeOrNone(_v);
      await widget.auth.transport.send(
        'PATCH',
        '/v1/stock/purchases/${Uri.encodeComponent(_str(widget.purchase['id']))}/lines/${_int(_l['position'])}',
        body: {...tr.fields(), if (many) 'track_many': true, if (_remember && !_unconfirmed) 'remember': true},
      );
      if (!mounted) return;
      showTkToast(context, _unconfirmed || _remember ? '${_str(_l['item'])}: remembered for next time' : '${_str(_l['item'])}: changed for this order');
      setState(() => (_now = null, _remember = false, _busy = false));
      widget.onSaved();
    } on ApiError catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      switch (treatCode(e)) {
        case 'track_ask':
          final yes = await askTrackMany(context, e);
          if (yes == null || !mounted) return;
          if (!yes) setState(() => _now = _v.copyWith(how: 'record'));
          return _save(many: yes);
        case 'track_too_many':
          setState(() => _now = _v.copyWith(how: 'record'));
          showTkToast(context, treatRefusal(e), error: true);
        case 'line_locked':
          showTkToast(context, treatRefusal(e), error: true);
          widget.onSaved();
        default:
          showTkToast(context, e.message, error: true);
      }
    } on Offline {
      if (mounted) {
        setState(() => _busy = false);
        showTkToast(context, 'You’re offline. Try again when you’re back online.', error: true);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final locked = _int(_l['received']) > 0;
    final changed = _now != null && !_now!.same(_was);
    final item = _str(_l['item']);
    return TreatQuestion(
      key: ValueKey('line:${_l['id']}'),
      item: item,
      supplier: _str(widget.purchase['supplier']),
      quantity: _int(_l['quantity']),
      unit: _str(_l['unit']),
      value: _v,
      choices: widget.choices,
      asked: _unconfirmed && !locked,
      locked: locked,
      remember: _remember,
      onRemember: _unconfirmed || !widget.canChange ? null : (on) => setState(() => _remember = on),
      onChanged: !widget.canChange || _busy ? null : (v) => setState(() => _now = v),
      foot: !widget.canChange
          ? null
          : Align(
              alignment: Alignment.centerLeft,
              child: TkSmallButton(
                label: _busy ? 'Saving…' : (_unconfirmed ? 'Confirm' : 'Save'),
                semantics: '${_unconfirmed ? 'Confirm' : 'Save'} how $item is treated',
                primary: true,
                onPressed: _busy || !_v.ready || (!_unconfirmed && !changed && !_remember) ? null : _save,
              ),
            ),
    );
  }
}

/// A heading over the question's cards, as a form group's (no panel round
/// them: each question is a card of its own).
class TreatSection extends StatelessWidget {
  const TreatSection({super.key, required this.title, required this.children});
  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 4, 4, 10),
          child: Semantics(
            header: true,
            child: Text(title, style: tkDisplay(size: 20, letterSpacing: -0.4, color: t.text)),
          ),
        ),
        for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 10), children[i]],
      ],
    );
  }
}
