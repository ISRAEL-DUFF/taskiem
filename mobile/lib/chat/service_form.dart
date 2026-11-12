// A service and a price list from their tools (M6 parts V, L; web/src/
// pages/ChatToolForms.tsx ServiceForm, PriceListForm; lib/bookings.ts
// serviceRow; lib/storefront.ts itemRow): a service's name, how long it
// takes and its price, kept in the business's services table; a list's
// prices, each in naira.

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/forms.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import 'approve.dart' show TkTextBox;
import 'words.dart' show naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int? _int(Object? v) => (v as num?)?.toInt();

/// toolforms.ts serviceLine: "New service: Rice milling, 30 min, ₦2,500".
String serviceLine(String name, int minutes, int? kobo) => 'New service: $name, $minutes min, ${kobo == null ? 'price on the day' : naira(kobo)}';

/// storefront.ts itemRow and bookings.ts serviceRow: a services table row
/// from what the owner types, in the table's own columns; or why not.
({Map<String, Object?>? row, String? error}) serviceRow(Map<String, dynamic> columns, String name, String minutes, String price) {
  final n = name.trim();
  if (n.isEmpty) return (row: null, error: 'Give the item a name.');
  final nameCol = columns['name'] as String?;
  if (nameCol == null || nameCol.isEmpty) return (row: null, error: 'The catalogue has no name column.');
  final row = <String, Object?>{nameCol: n};
  final p = price.replaceAll(RegExp(r'[₦,\s]'), '');
  if (p.isNotEmpty) {
    if (!RegExp(r'^\d+(\.\d{1,2})?$').hasMatch(p)) return (row: null, error: 'Write the price as a number of naira, such as 1500.');
    final priceCol = columns['price'] as String?;
    if (priceCol == null || priceCol.isEmpty) return (row: null, error: 'The catalogue has no price column.');
    final nairaN = double.parse(p);
    row[priceCol] = columns['price_unit'] == 'kobo' ? (nairaN * 100).round() : (nairaN == nairaN.roundToDouble() ? nairaN.round() : nairaN);
  }
  final m = int.tryParse(minutes.trim());
  if (m == null || m < 5 || m > 480) return (row: null, error: 'Say how long it takes in minutes, from 5 to 480.');
  final dur = columns['duration'] as String?;
  if (dur == null || dur.isEmpty) return (row: null, error: 'The services have no duration column.');
  row[dur] = m;
  // A services table keeps no "available" flag the editor sets.
  row.remove(columns['available']);
  return (row: row, error: null);
}

/// ChatToolForms.tsx ServiceForm: a service, new or as it is.
class ServiceForm extends StatefulWidget {
  const ServiceForm({super.key, required this.auth, this.service, required this.onSaved, this.canEdit = true});
  final Auth auth;
  final Map<String, dynamic>? service;
  final void Function(String? line) onSaved;
  final bool canEdit;

  @override
  State<ServiceForm> createState() => _ServiceFormState();
}

class _ServiceFormState extends State<ServiceForm> {
  Map<String, dynamic>? _sf;
  String? _error;
  late final _name = TextEditingController(text: widget.service?['name'] as String? ?? '');
  late int _minutes = _int(widget.service?['duration_minutes']) ?? 30;
  late final _price = TextEditingController(text: _int(widget.service?['price_kobo']) != null ? nairaText(_int(widget.service!['price_kobo'])!) : '');

  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _name.addListener(() => setState(() {}));
    _t
        .send('GET', '/v1/storefront')
        .then(
          (r) {
            if (mounted) setState(() => _sf = (r as Map?)?.cast<String, dynamic>() ?? {});
          },
          onError: (Object e) {
            if (mounted) setState(() => _error = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.');
          },
        );
  }

  @override
  void dispose() {
    _name.dispose();
    _price.dispose();
    super.dispose();
  }

  Future<void> _refresh() async {
    try {
      await _t.send('POST', '/v1/storefront/catalogue/refresh', body: const {});
    } on Object {
      // The page reads it later.
    }
  }

  @override
  Widget build(BuildContext context) {
    final sf = _sf;
    if (sf == null) {
      return _error != null ? Padding(padding: const EdgeInsets.all(20), child: TkErrorBox(_error!)) : const TkLoading();
    }
    final table = sf['services_table'] as String?;
    if (table == null || table.isEmpty) {
      return ListView(
        children: const [TkEmpty(icon: 'clock', title: 'Services are not set up yet', line: 'Open services to choose where your services are kept.')],
      );
    }
    final cols = (sf['services_columns'] as Map?)?.cast<String, dynamic>() ?? const {};
    final s = widget.service;
    Future<String?> save() async {
      final made = serviceRow(cols, _name.text, '$_minutes', _price.text);
      if (made.error != null) throw FormProblem(made.error!);
      if (s != null) {
        await _t.send('PATCH', '/v1/tables/$table/rows/${Uri.encodeComponent(s['id'] as String)}', body: {'set': made.row});
      } else {
        await _t.send('POST', '/v1/tables/$table/rows', body: {'row': made.row});
      }
      await _refresh();
      return s != null ? '${_name.text.trim()}: saved' : serviceLine(_name.text.trim(), _minutes, nairaKobo(_price.text));
    }

    Future<void> remove() async {
      if (s == null) return;
      final yes = await showTkConfirm(context, title: 'Remove ${s['name']}?', body: 'Customers can no longer book it.', ok: 'Remove', danger: true);
      if (!yes) return;
      try {
        await _t.send('DELETE', '/v1/tables/$table/rows/${Uri.encodeComponent(s['id'] as String)}');
        await _refresh();
        widget.onSaved('${s['name']}: removed');
      } on Object catch (e) {
        if (mounted) setState(() => _error = errorWords(e));
      }
    }

    return TkToolForm(
      save: save,
      onSaved: widget.onSaved,
      saveWords: s != null ? 'Save' : 'Add service',
      ready: _name.text.trim().isNotEmpty && _minutes > 0,
      canEdit: widget.canEdit,
      children: [
        TkKitField(
          label: 'Name',
          child: TkTextBox(controller: _name, hint: 'Rice milling, per bag', max: 120),
        ),
        TkFormRow(
          title: 'Takes',
          sub: 'Minutes',
          trailing: TkStepper(label: 'Minutes it takes', value: _minutes, onChanged: (n) => setState(() => _minutes = n)),
        ),
        TkMoneyField(label: 'Price', controller: _price),
        if (s != null && widget.canEdit) TkGhostButton(label: 'Remove this service', danger: true, onTap: remove),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}

/// ChatToolForms.tsx PriceListForm: a list's prices, each in naira.
class PriceListForm extends StatefulWidget {
  const PriceListForm({super.key, required this.auth, required this.list, required this.onSaved, this.canEdit = true});
  final Auth auth;
  final Map<String, dynamic> list;
  final void Function(String? line) onSaved;
  final bool canEdit;

  @override
  State<PriceListForm> createState() => _PriceListFormState();
}

class _PriceListFormState extends State<PriceListForm> {
  late final List<Map<String, dynamic>> _items = _maps(widget.list['items']);
  late final List<TextEditingController> _prices = [
    for (final it in _items) TextEditingController(text: nairaText(_int(it['price_kobo']) ?? 0))..addListener(() => setState(() {})),
  ];

  @override
  void dispose() {
    for (final c in _prices) {
      c.dispose();
    }
    super.dispose();
  }

  Future<String?> _save() async {
    var changed = 0;
    final items = <Map<String, Object?>>[];
    for (var i = 0; i < _items.length; i++) {
      final it = _items[i];
      final to = nairaKobo(_prices[i].text);
      if (to != _int(it['price_kobo'])) changed++;
      final own = [
        for (final z in _maps(it['sizes']))
          if ((_int(z['price_kobo']) ?? 0) > 0) {'item_id': z['item_id'], 'price_kobo': z['price_kobo']},
      ];
      items.add({
        'item_id': it['item_id'],
        'price_kobo': to,
        'min_quantity': it['min_quantity'],
        'available': it['available'],
        if (own.isNotEmpty) 'sizes': own,
      });
    }
    if (changed == 0) return null;
    await widget.auth.transport.send('PUT', '/v1/price-lists/${widget.list['id']}/items', body: {'items': items});
    return '${widget.list['name']}: saved';
  }

  @override
  Widget build(BuildContext context) {
    if (_items.isEmpty) {
      return ListView(
        children: const [TkEmpty(icon: 'tag', title: 'Nothing on this list yet', line: 'Open price lists to add items to it.')],
      );
    }
    final bad = _prices.any((p) => !((nairaKobo(p.text) ?? 0) > 0));
    return TkToolForm(
      save: _save,
      onSaved: widget.onSaved,
      ready: !bad,
      canEdit: widget.canEdit,
      children: [
        for (var i = 0; i < _items.length; i++)
          TkMoneyField(
            key: ValueKey(_items[i]['item_id']),
            label: '${_items[i]['name'] ?? ''}${(_items[i]['unit'] as String? ?? '').isNotEmpty ? ', a ${_items[i]['unit']}' : ''}',
            controller: _prices[i],
          ),
      ],
    );
  }
}
