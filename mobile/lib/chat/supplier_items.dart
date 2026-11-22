// What you buy from a supplier, remembered (docs/spec/what-a-business-buys.md
// §6, §9): each of their items with how it is treated, its stock item and
// its label, changed or forgotten here. A linked supplier's are its item
// links (/v1/item-links/{link}); a supplier not on Taskiem's, the items
// remembered by name (/v1/stock/suppliers/{id}/items). A change applies to
// orders drafted after it; nothing already received moves.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show ItemMemory;
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkFormSheet, TkListRow, TkSection;
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart' show TkPageScreen;
import '../kit/sheet.dart' show showTkSheet;
import '../theme/look.dart';
import 'groups.dart' show askSure;
import 'treat_line.dart';

/// The page of one supplier's remembered items.
class SupplierItemsPage extends StatefulWidget {
  const SupplierItemsPage({super.key, required this.auth, required this.supplierId, required this.name, this.link = ''});
  final Auth auth;
  final String supplierId;
  final String name;

  /// The link, for a supplier on Taskiem.
  final String link;

  @override
  State<SupplierItemsPage> createState() => _SupplierItemsPageState();
}

class _SupplierItemsPageState extends State<SupplierItemsPage> {
  List<ItemMemory>? _items;
  String? _error;
  late final TreatChoices _choices = TreatChoices(widget.auth);

  bool get _linked => widget.link.isNotEmpty;

  @override
  void initState() {
    super.initState();
    _load();
    _choices.load();
  }

  Future<void> _load() async {
    try {
      final list = _linked ? (await widget.auth.api.listItemLinks(link: widget.link)).itemLinks : (await widget.auth.api.listSupplierItems(id: widget.supplierId)).items;
      list.sort((a, b) => a.name.toLowerCase().compareTo(b.name.toLowerCase()));
      if (mounted) setState(() => (_items = list, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  Future<void> _open(ItemMemory m) async {
    final said = await showTkSheet<String>(
      context,
      (_) => _ItemSheet(auth: widget.auth, memory: m, supplier: widget.name, supplierId: widget.supplierId, link: widget.link, choices: _choices),
    );
    if (said == null || !mounted) return;
    showTkToast(context, said);
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final list = _items;
    final manage = widget.auth.can('storefront.manage');
    return TkPageScreen(
      title: 'What you buy from ${widget.name}',
      titleSize: 22,
      backLabel: 'Back to Suppliers',
      description: 'How each of their items is treated when you order it: just recorded, added to stock, or kept track of on Things we have, and its label. A change applies to orders after it.',
      onRefresh: _load,
      children: [
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error!, color: look.red, subLines: 3),
        if (list == null && _error == null) const TkLoading(rows: 3, label: 'Loading what you buy from them…'),
        if (list != null && list.isEmpty)
          TkEmpty(icon: 'tag', title: 'Nothing remembered yet', line: 'The first order of each item from ${widget.name} asks how to treat it, and it is remembered here.'),
        if (list != null && list.isNotEmpty)
          TkSection(
            children: [
              for (final m in list)
                TkListRow(
                  title: m.name,
                  sub: treatLine(Treat.ofMemory(m)),
                  icon: switch (m.treatment) {
                    'stock' => 'b-box',
                    'track' => 'b-hash',
                    _ => 'b-receipt',
                  },
                  square: switch (m.treatment) {
                    'stock' => look.teal,
                    'track' => look.violet,
                    _ => look.blue,
                  },
                  semanticsLabel: '${m.name}: ${treatLine(Treat.ofMemory(m))}',
                  onTap: manage ? () => _open(m) : null,
                ),
            ],
          ),
      ],
    );
  }
}

/// One remembered item: the question, Save, and Forget.
class _ItemSheet extends StatefulWidget {
  const _ItemSheet({required this.auth, required this.memory, required this.supplier, required this.supplierId, required this.link, required this.choices});
  final Auth auth;
  final ItemMemory memory;
  final String supplier;
  final String supplierId;
  final String link;
  final TreatChoices choices;

  @override
  State<_ItemSheet> createState() => _ItemSheetState();
}

class _ItemSheetState extends State<_ItemSheet> {
  late Treat _v = Treat.ofMemory(widget.memory);
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    widget.choices.addListener(_changed);
  }

  @override
  void dispose() {
    widget.choices.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  ItemMemory get _m => widget.memory;

  Future<void> _save({bool many = false}) async {
    setState(() => _busy = true);
    try {
      final tr = await widget.choices.made(_v);
      final body = {...tr.fields(), if (many) 'track_many': true};
      if (widget.link.isNotEmpty) {
        await widget.auth.transport.send('PUT', '/v1/item-links/${Uri.encodeComponent(widget.link)}', body: {'seller_item_id': _m.sellerItemId, 'seller_item': _m.name, ...body});
      } else {
        await widget.auth.transport.send('PUT', '/v1/stock/suppliers/${Uri.encodeComponent(widget.supplierId)}/items', body: {'name': _m.name, ...body});
      }
      if (mounted) Navigator.of(context).pop('${_m.name}: changed for your next orders');
    } on ApiError catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      if (treatCode(e) == 'track_ask') {
        final yes = await askTrackMany(context, e);
        if (yes == null || !mounted) return;
        if (!yes) setState(() => _v = _v.copyWith(how: 'record'));
        return _save(many: yes);
      }
      showTkToast(context, treatCode(e) == null ? e.message : treatRefusal(e), error: true);
    } on Offline {
      if (mounted) {
        setState(() => _busy = false);
        showTkToast(context, 'You’re offline. Try again when you’re back online.', error: true);
      }
    }
  }

  Future<void> _forget() async {
    final yes = await askSure(
      context,
      title: 'Forget ${_m.name}?',
      body: 'Its next order from ${widget.supplier} asks how to treat it again. Orders already made keep theirs.',
      ok: 'Forget it',
      danger: true,
    );
    if (!yes || !mounted) return;
    setState(() => _busy = true);
    try {
      if (widget.link.isNotEmpty) {
        await widget.auth.api.deleteItemLink(link: widget.link, item: _m.sellerItemId ?? '');
      } else {
        await widget.auth.api.deleteSupplierItem(id: widget.supplierId, name: _m.name);
      }
      if (mounted) Navigator.of(context).pop('${_m.name} forgotten');
    } on Object catch (e) {
      if (mounted) {
        setState(() => _busy = false);
        showTkToast(context, errorWords(e), error: true);
      }
    }
  }

  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: _m.name,
    crumb: widget.supplier,
    action: _busy ? 'Saving…' : 'Save',
    onAction: _busy || !_v.ready || _v.same(Treat.ofMemory(_m)) ? null : _save,
    second: 'Forget',
    secondDanger: true,
    onSecond: _busy ? null : _forget,
    children: [
      if (!widget.choices.ready)
        const TkLoading(rows: 2, label: 'Loading your stock items and labels…')
      else
        TreatQuestion(item: _m.name, supplier: widget.supplier, value: _v, choices: widget.choices, onChanged: _busy ? null : (v) => setState(() => _v = v)),
    ],
  );
}
