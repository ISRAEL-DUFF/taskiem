// Things we have, under Money (docs/spec/what-a-business-buys.md §5.3,
// §9): what the business keeps track of, one row each, with its cost and
// when it came, by date or by label, and those no longer had (sold,
// broken, given away, gone) on asking; a row's page with its status,
// serial, warranty, note, label, supplier and the order it came on; and
// Add something we have, for what was bought before Taskiem or outside it.
// Seen with table.read, changed with table.edit; sold with a price is
// money in (Sale of things we had), which needs invoice.manage too.

import 'dart:async' show Timer;

import 'package:flutter/material.dart' show showDatePicker;
import 'package:flutter/widgets.dart';

import '../api/api.g.dart' show AddThingRequest, PatchThingRequest, Thing;
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/forms.dart' show TkMoneyField, nairaKobo;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/list.dart' show TkEmpty, TkLoading, TkSearchBox;
import '../kit/look.dart' show TkCallout, TkEyebrow, TkSplitBar, TkSquare, TkTopCard, tkFigure;
import '../kit/mobile.dart' show TkCountUp, TkFormGroup, TkFormSheet, TkHeadButton, TkListRow, TkRoute, TkSection, TkChips, TkTick;
import '../kit/modal.dart' show showTkToast;
import '../kit/page.dart' show TkPageScreen;
import '../kit/sheet.dart' show TkKitField, TkPickBox, showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'book_sheet.dart' show lagosDay;
import 'groups.dart' show pickAction, pickOneOf;
import 'money_sheets.dart' show dateLabel;
import 'purchases_page.dart' show openPurchase;
import 'treat_line.dart' show LabelPickSheet, Treat, TreatChoices;
import 'words.dart' show CardTone, naira;

String _why(Object e) => e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.';

/// A status in words: Have, Sold, Broken, Given away, Gone.
String thingStatus(String s) => switch (s) {
  'sold' => 'Sold',
  'broken' => 'Broken',
  'given' => 'Given away',
  'gone' => 'Gone',
  _ => 'Have',
};

/// A status's pill tone.
CardTone thingTone(String s) => switch (s) {
  'sold' => CardTone.lime,
  'broken' => CardTone.danger,
  'gone' => CardTone.warn,
  _ => CardTone.plain,
};

/// When it came, as a day ("3 Nov 2026"); when it was added if not known.
String thingDay(Thing t) => dateLabel(lagosDay((t.receivedAt ?? t.createdAt).toUtc().toIso8601String()));

/// A row's line: its label, from whom, when it came.
String thingSub(Thing t) => [(t.label ?? '').isEmpty ? 'Not labelled' : t.label!, if ((t.supplier ?? '').isNotEmpty) t.supplier!, thingDay(t)].join(' · ');

/// "1 thing", "3 things".
String thingsCount(int n) => n == 1 ? '1 thing' : '$n things';

/// Whether Money is only Things we have for someone: they may see stock
/// (table.read) but not invoices (build-plan-gaps H2, g-things-menu; the
/// web's thingsOnly).
bool thingsOnly(Auth auth) => auth.can('table.read') && !auth.can('invoice.read');

/// The rows a search keeps: each of its words in the name, label,
/// supplier, serial, note or the order's number, without case.
List<Thing> searchThings(List<Thing> things, String q) {
  final words = q.trim().toLowerCase().split(RegExp(r'\s+')).where((w) => w.isNotEmpty).toList();
  if (words.isEmpty) return things;
  return [
    for (final t in things)
      if (() {
        final text = [t.name, t.label, t.supplier, t.serial, t.note, t.orderReference].whereType<String>().join(' ').toLowerCase();
        return words.every(text.contains);
      }())
        t,
  ];
}

/// Things we have, opened from Money's Go to (or from an order's lines,
/// [order] then the order the rows came on).
class ThingsPage extends StatefulWidget {
  const ThingsPage({super.key, required this.auth, this.backLabel = 'Back to Money', this.order, this.orderReference});
  final Auth auth;
  final String backLabel;

  /// Only the rows that came on this order (its id), every status.
  final String? order;
  final String? orderReference;

  @override
  State<ThingsPage> createState() => _ThingsPageState();
}

class _ThingsPageState extends State<ThingsPage> {
  List<Thing>? _things;
  int _count = 0;
  int _total = 0;
  List<({String label, int count, int total})> _groups = const [];
  String? _error;
  bool _all = false;
  String _by = 'date';
  int _asked = 0;
  final _q = TextEditingController();

  @override
  void initState() {
    super.initState();
    _all = widget.order != null;
    _q.addListener(() => setState(() {}));
    _load();
  }

  @override
  void dispose() {
    _q.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final asked = ++_asked;
    try {
      // An order's rows are asked of the server by its id (every status),
      // not picked from the newest 2,000 here.
      final order = widget.order;
      final r = order != null ? await widget.auth.api.listThings(all: 'true', order: order) : await widget.auth.api.listThings(all: _all ? 'true' : null, group: 'label');
      if (!mounted || asked != _asked) return;
      setState(() {
        _things = r.things;
        _count = r.count;
        _total = r.totalKobo;
        _groups = [for (final g in r.groups ?? const []) (label: g.label, count: g.count, total: g.totalKobo)];
        _error = null;
      });
    } on Object catch (e) {
      if (mounted && asked == _asked) setState(() => _error = _why(e));
    }
  }

  Future<void> _open(Thing t) async {
    final changed = await Navigator.of(context).push<bool>(TkRoute<bool>(pageBuilder: (_, _, _) => ThingPage(auth: widget.auth, thing: t)));
    if (changed == true) await _load();
  }

  Future<void> _add() async {
    final t = await showTkSheet<Thing>(context, (_) => AddThingSheet(auth: widget.auth));
    if (t == null || !mounted) return;
    showTkToast(context, '${t.name} is on Things we have');
    await _load();
  }

  Widget _row(Thing t) => TkListRow(
    title: t.name,
    letters: t.name,
    sub: thingSub(t),
    amount: t.costKobo == null ? null : naira(t.costKobo!),
    status: t.status == 'have' ? null : thingStatus(t.status),
    statusTone: thingTone(t.status),
    semanticsLabel: '${t.name}, ${thingSub(t)}${t.costKobo == null ? '' : ', ${naira(t.costKobo!)}'}${t.status == 'have' ? '' : ', ${thingStatus(t.status)}'}',
    onTap: () => _open(t),
  );

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final order = widget.order;
    final list = _things;
    // A search keeps the rows it finds; the top card stays what is had.
    final q = _q.text.trim();
    final found = list == null ? null : searchThings(list, q);
    final had = found?.where((x) => x.status == 'have').toList() ?? const <Thing>[];
    final gone = found?.where((x) => x.status != 'have').toList() ?? const <Thing>[];
    final colours = [look.violet, look.teal, look.orange, look.blue];
    final edit = widget.auth.can('table.edit');
    return TkPageScreen(
      title: 'Things we have',
      titleSize: 30,
      backLabel: widget.backLabel,
      description:
          'What the business bought to keep, one row each: machines, tools, furniture. Each keeps its cost, when it came and from whom. Sold, broken, given away or gone, it leaves the totals and stays in the history.',
      action: edit && order == null ? TkHeadButton(icon: 'b-plus', label: 'Add something we have', primary: true, onTap: _add) : null,
      under: order == null
          ? TkChips<String>(
              label: 'Show',
              chips: const [('date', 'Newest first'), ('label', 'By label')],
              on: _by,
              onPick: (v) => setState(() => _by = v),
            )
          : null,
      onRefresh: _load,
      children: [
        if (_error != null) TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error!, color: look.red, subLines: 3),
        if (list == null && _error == null) const TkLoading(rows: 3, label: 'Loading what you have…'),
        if (list != null && order == null)
          TkTopCard(
            semanticsLabel: 'What we still have: ${thingsCount(_count)}, ${naira(_total)} at cost',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('What we still have', color: look.violetInk, icon: 'b-hash', trailing: thingsCount(_count)),
                const SizedBox(height: 6),
                FittedBox(fit: BoxFit.scaleDown, alignment: Alignment.centerLeft, child: TkCountUp(_total, style: tkFigure(context, size: 40))),
                Text('at what they cost', style: tkText(size: 13, color: t.muted)),
                if (_total > 0 && _groups.length > 1) ...[
                  const SizedBox(height: 12),
                  TkSplitBar(
                    parts: [
                      for (var i = 0; i < _groups.length && i < 3; i++)
                        (label: _groups[i].label.isEmpty ? 'Not labelled' : _groups[i].label, share: (100 * _groups[i].total / _total).round(), color: colours[i]),
                      if (_groups.length > 3)
                        (label: 'Other', share: (100 * _groups.skip(3).fold(0, (n, g) => n + g.total) / _total).round(), color: colours[3]),
                    ],
                  ),
                ],
              ],
            ),
          ),
        if (list != null && order != null)
          Text(
            '${widget.orderReference == null ? 'From this order' : 'From ${widget.orderReference}'}: ${thingsCount(list.length)}',
            style: tkText(size: 13.5, color: t.muted),
          ),
        if (list != null && list.isNotEmpty && order == null)
          TkSearchBox(controller: _q, hint: 'Search by name, label, supplier or serial', label: 'Search Things we have', margin: EdgeInsets.zero),
        if (list != null && list.isEmpty && order == null)
          TkEmpty(
            icon: 'tag',
            title: _all ? 'Nothing on Things we have yet' : 'Nothing kept track of yet',
            line: 'Order something with Keep track of it, or add what you already have.',
          ),
        if (list != null && list.isNotEmpty && q.isNotEmpty && found!.isEmpty)
          TkEmpty(icon: 'search', title: 'Nothing matches “$q”', line: 'Try part of its name, its label, its supplier or its serial number.'),
        if (list != null && had.isNotEmpty && (_by == 'date' || order != null || q.isNotEmpty)) TkSection(title: order == null ? 'What we have' : null, children: [for (final x in had) _row(x)]),
        if (list != null && had.isNotEmpty && _by == 'label' && order == null && q.isEmpty)
          for (final g in _groups)
            TkSection(
              title: g.label.isEmpty ? 'Not labelled' : g.label,
              trailing: Text(naira(g.total), style: tkText(size: 13.5, weight: 600, color: t.muted)),
              children: [
                for (final x in had)
                  if ((x.label ?? '').toLowerCase() == g.label.toLowerCase() && ((x.labelId ?? '').isEmpty == g.label.isEmpty)) _row(x),
              ],
            ),
        if (gone.isNotEmpty) TkSection(title: 'No longer had', children: [for (final x in gone) _row(x)]),
        if (list != null && order == null)
          Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(
              label: _all ? 'Hide those no longer had' : 'Show those no longer had',
              onPressed: () {
                setState(() => _all = !_all);
                _load();
              },
            ),
          ),
      ],
    );
  }
}

/// One of Things we have: its cost and status on the top card, the status
/// as five choices, its details, and where it came from.
class ThingPage extends StatefulWidget {
  const ThingPage({super.key, required this.auth, required this.thing});
  final Auth auth;
  final Thing thing;

  @override
  State<ThingPage> createState() => _ThingPageState();
}

class _ThingPageState extends State<ThingPage> {
  late Thing _t = widget.thing;
  bool _changed = false;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.getThing(id: widget.thing.id);
      if (mounted) setState(() => _t = r.thing);
    } on Object {
      // What the list had stays.
    }
  }

  /// Sends a change; the row as it is now, or a toast saying why not.
  Future<bool> _patch(PatchThingRequest body, String said, {String? key}) async {
    setState(() => _busy = true);
    try {
      final r = await widget.auth.api.patchThing(id: _t.id, body: body, idempotencyKey: key);
      if (!mounted) return true;
      setState(() => (_t = r.thing, _changed = true, _busy = false));
      showTkToast(context, said);
      return true;
    } on Object catch (e) {
      if (mounted) {
        setState(() => _busy = false);
        showTkToast(context, _why(e), error: true);
      }
      return false;
    }
  }

  /// Whether this person may add the price of a thing marked sold without
  /// one: it is money in, so table.edit and invoice.manage.
  bool get _canPrice => widget.auth.can('table.edit') && widget.auth.can('invoice.manage') && _t.status == 'sold' && (_t.saleEntryId ?? '').isEmpty;

  /// Sold before without a price: asks it now and records it as money in.
  Future<void> _addPrice() async {
    if (_busy || !_canPrice) return;
    final sale = await showTkSheet<({int? kobo, String how})>(context, (_) => SoldSheet(auth: widget.auth, thing: _t, priceOnly: true));
    if (sale == null || sale.kobo == null || !mounted) return;
    await _patch(
      PatchThingRequest(status: 'sold', salePriceKobo: sale.kobo, salePaidHow: sale.how),
      '${naira(sale.kobo!)} in for ${_t.name}',
      key: uuid4(),
    );
  }

  Future<void> _status(String s) async {
    if (_busy) return;
    if (s == _t.status) {
      if (s == 'sold' && _canPrice) await _addPrice();
      return;
    }
    if (s == 'sold' && (_t.saleEntryId ?? '').isNotEmpty) {
      // Its sale is in money in already: the server takes no second price.
      await _patch(const PatchThingRequest(status: 'sold'), 'Marked sold; its sale was recorded already');
      return;
    }
    if (s == 'sold') {
      final sale = await showTkSheet<({int? kobo, String how})>(context, (_) => SoldSheet(auth: widget.auth, thing: _t));
      if (sale == null || !mounted) return;
      await _patch(
        PatchThingRequest(status: 'sold', salePriceKobo: sale.kobo, salePaidHow: sale.kobo == null ? null : sale.how),
        sale.kobo == null ? 'Marked sold' : 'Marked sold, ${naira(sale.kobo!)} in',
        key: sale.kobo == null ? null : uuid4(),
      );
      return;
    }
    if (_t.status == 'sold' && (_t.saleEntryId ?? '').isNotEmpty) {
      // Its sale is in money in, so it stays sold (the server refuses
      // any other status): say why instead of asking.
      showTkToast(context, '${_t.name} was sold with its money recorded, so it stays sold', error: true);
      return;
    }
    await _patch(PatchThingRequest(status: s), s == 'have' ? 'Back on what we have' : 'Marked ${thingStatus(s).toLowerCase()}');
  }

  Future<void> _text(String title, String now, int max, PatchThingRequest Function(String v) body) async {
    final v = await showTkSheet<String>(context, (_) => _TextSheet(title: title, crumb: _t.name, now: now, max: max));
    if (v == null || !mounted || v == now) return;
    await _patch(body(v), v.isEmpty ? '$title cleared' : '$title saved');
  }

  Future<void> _warranty() async {
    final had = _t.warrantyUntil ?? '';
    if (had.isNotEmpty) {
      final what = await pickAction(context, title: 'Warranty ends', note: dateLabel(had), actions: [('date', 'Change the date', 'b-calendar', false), ('clear', 'Clear it', 'b-x', true)]);
      if (what == null || !mounted) return;
      if (what == 'clear') {
        await _patch(const PatchThingRequest(warrantyUntil: ''), 'Warranty cleared');
        return;
      }
    }
    final now = DateTime.now();
    final d = await showDatePicker(context: context, initialDate: DateTime.tryParse(had) ?? now, firstDate: DateTime(now.year - 10), lastDate: DateTime(now.year + 20));
    if (d == null || !mounted) return;
    await _patch(PatchThingRequest(warrantyUntil: _ymd(d)), 'Warranty saved');
  }

  Future<void> _label() async {
    final c = TreatChoices(widget.auth);
    await c.load();
    if (!mounted) return;
    final l = await showTkSheet<({String? id, String name})>(context, (_) => LabelPickSheet(choices: c, item: _t.name, now: _t.labelId));
    if (l == null || !mounted) return;
    if (l.name.isEmpty) {
      if ((_t.labelId ?? '').isEmpty) return;
      await _patch(const PatchThingRequest(clearLabel: true), 'Label taken off');
      return;
    }
    if (l.id == null || l.id == _t.labelId) return;
    await _patch(PatchThingRequest(labelId: l.id), 'Labelled ${l.name}');
  }

  /// From whom: one of the business's suppliers, or Not said.
  Future<void> _supplier() async {
    List<Map<String, dynamic>> list;
    try {
      final r = (await widget.auth.transport.send('GET', '/v1/stock/suppliers') as Map).cast<String, dynamic>();
      list = (r['suppliers'] as List? ?? const []).whereType<Map<String, dynamic>>().toList();
    } on Object catch (e) {
      if (mounted) showTkToast(context, _why(e), error: true);
      return;
    }
    if (!mounted) return;
    final id = await pickOneOf(
      context,
      title: 'From whom',
      value: _t.supplierId ?? '',
      searchHint: list.length > 8 ? 'Search your suppliers' : null,
      choices: [
        ('', 'Not said', 'Bought outside your suppliers'),
        for (final s in list) ('${s['id']}', '${s['name']}', ''),
      ],
    );
    if (id == null || !mounted || id == (_t.supplierId ?? '')) return;
    if (id.isEmpty) {
      await _patch(const PatchThingRequest(clearSupplier: true), 'Supplier taken off');
      return;
    }
    await _patch(PatchThingRequest(supplierId: id), 'From ${list.firstWhere((s) => s['id'] == id)['name']}');
  }

  Widget _choice(String s, String sub, bool edit) => Semantics(
    selected: _t.status == s,
    child: TkListRow(
      title: thingStatus(s),
      sub: sub,
      icon: switch (s) {
        'sold' => 'b-money',
        'broken' => 'b-tools',
        'given' => 'b-share',
        'gone' => 'b-x-ring',
        _ => 'b-check-ring',
      },
      square: switch (s) {
        'sold' => TkLook.of(context).blue,
        'broken' => TkLook.of(context).orange,
        'given' => TkLook.of(context).violet,
        'gone' => TkLook.of(context).red,
        _ => TkLook.of(context).teal,
      },
      chevron: false,
      trailing: TkTick(_t.status == s),
      onTap: edit && !_busy ? () => _status(s) : null,
    ),
  );

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final x = _t;
    final edit = widget.auth.can('table.edit');
    final order = x.purchaseOrderId ?? '';
    final colour = switch (x.status) {
      'have' => look.violetInk,
      'sold' => look.limeInk,
      'broken' => look.redInk,
      'gone' => look.orangeInk,
      _ => t.muted,
    };
    return PopScope<bool>(
      canPop: false,
      onPopInvokedWithResult: (did, _) {
        if (!did) Navigator.of(context).pop(_changed);
      },
      child: TkPageScreen(
        title: x.name,
        titleSize: 22,
        backLabel: 'Back to Things we have',
        onRefresh: _load,
        children: [
          TkTopCard(
            semanticsLabel: '${x.name}: ${thingStatus(x.status)}${x.costKobo == null ? '' : ', cost ${naira(x.costKobo!)}'}',
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TkEyebrow('What it cost', color: colour, icon: 'b-hash', trailing: thingStatus(x.status)),
                const SizedBox(height: 6),
                FittedBox(
                  fit: BoxFit.scaleDown,
                  alignment: Alignment.centerLeft,
                  child: Text(x.costKobo == null ? 'Not known' : naira(x.costKobo!), maxLines: 1, style: tkFigure(context, size: 40)),
                ),
                Text(
                  [
                    'Came ${thingDay(x)}',
                    if ((x.supplier ?? '').isNotEmpty) 'from ${x.supplier}',
                  ].join(' · '),
                  style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
                ),
                if (x.status != 'have') ...[
                  const SizedBox(height: 6),
                  Text(
                    '${thingStatus(x.status)} ${dateLabel(lagosDay(x.statusAt.toUtc().toIso8601String()))}${(x.saleEntryId ?? '').isNotEmpty ? ' · its sale is in money in' : ' · out of the totals'}',
                    style: tkText(size: 13, weight: 600, lineHeight: 1.4, color: colour),
                  ),
                ],
              ],
            ),
          ),
          if (!edit)
            TkCallout(
              leading: TkSquare('b-lock', color: look.blue),
              title: 'You can see this, not change it',
              sub: 'Someone who may change stock can mark it sold, broken or gone.',
              color: look.blue,
            ),
          TkSection(
            title: 'Status',
            children: [
              _choice('have', 'Still with the business', edit),
              _choice('sold', 'Sold, with its price as money in, or without', edit),
              _choice('broken', 'Out of use', edit),
              _choice('given', 'Given to someone', edit),
              _choice('gone', 'Lost, stolen or thrown away', edit),
              if (_canPrice)
                TkListRow(
                  title: 'Add what it sold for',
                  sub: 'Sold without a price; with one, it is money in',
                  icon: 'b-cash',
                  square: look.lime,
                  onTap: _busy ? null : _addPrice,
                ),
            ],
          ),
          TkSection(
            title: 'Details',
            inset: 56,
            children: [
              TkListRow(
                title: 'Label',
                sub: (x.label ?? '').isEmpty ? 'Not labelled' : x.label!,
                icon: 'b-hash',
                square: look.teal,
                onTap: edit && !_busy ? _label : null,
              ),
              TkListRow(
                title: 'Serial number',
                sub: (x.serial ?? '').isEmpty ? 'None' : x.serial!,
                icon: 'b-qr',
                square: look.blue,
                onTap: edit && !_busy ? () => _text('Serial number', x.serial ?? '', 100, (v) => PatchThingRequest(serial: v)) : null,
              ),
              TkListRow(
                title: 'Warranty ends',
                sub: (x.warrantyUntil ?? '').isEmpty ? 'None' : dateLabel(x.warrantyUntil),
                icon: 'b-shield',
                square: look.lime,
                onTap: edit && !_busy ? _warranty : null,
              ),
              TkListRow(
                title: 'Note',
                sub: (x.note ?? '').isEmpty ? 'None' : x.note!,
                icon: 'b-note',
                square: look.orange,
                onTap: edit && !_busy ? () => _text('Note', x.note ?? '', 500, (v) => PatchThingRequest(note: v)) : null,
              ),
            ],
          ),
          TkSection(
            title: 'Where it came from',
            inset: 56,
            children: [
              TkListRow(
                title: 'From',
                sub: (x.supplier ?? '').isEmpty ? 'Not said' : x.supplier!,
                icon: 'b-truck',
                square: look.violet,
                chevron: edit,
                semanticsLabel: 'From: ${(x.supplier ?? '').isEmpty ? 'Not said' : x.supplier!}',
                onTap: edit && !_busy ? _supplier : null,
              ),
              if (order.isNotEmpty)
                TkListRow(
                  title: (x.orderReference ?? '').isEmpty ? 'The order it came on' : 'Order ${x.orderReference}',
                  sub: 'Open the order it came on',
                  icon: 'b-receipt',
                  square: look.blue,
                  onTap: () => openPurchase(context, widget.auth, order, backLabel: 'Back to ${x.name}'),
                )
              else
                // created_by is who added it as an id (a person's or a key's), not a
                // name, so only the day is shown.
                TkListRow(title: 'Added by hand', sub: dateLabel(lagosDay(x.createdAt.toUtc().toIso8601String())), icon: 'b-hand', square: look.teal, chevron: false),
            ],
          ),
        ],
      ),
    );
  }
}

String _ymd(DateTime d) => '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

/// One of a row's words typed: pops with it (empty to clear).
class _TextSheet extends StatefulWidget {
  const _TextSheet({required this.title, required this.crumb, required this.now, required this.max});
  final String title;
  final String crumb;
  final String now;
  final int max;

  @override
  State<_TextSheet> createState() => _TextSheetState();
}

class _TextSheetState extends State<_TextSheet> {
  late final _c = TextEditingController(text: widget.now);

  @override
  void initState() {
    super.initState();
    _c.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final v = _c.text.trim();
    return TkFormSheet(
      title: widget.title,
      crumb: widget.crumb,
      action: 'Save',
      onAction: v == widget.now.trim() || v.length > widget.max ? null : () => Navigator.of(context).pop(v),
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(
          children: [
            TkKitField(
              label: widget.title,
              hint: 'Leave it empty to clear it.',
              child: TkTextBox(controller: _c, max: widget.max, lines: widget.max > 100 ? 3 : 1, size: 15, height: 46),
            ),
          ],
        ),
      ],
    );
  }
}

/// Marked sold: an optional price, and how it was paid. A price is money
/// in, Sale of things we had, so it needs permission to enter money;
/// without it, sold is marked with no price. Pops with the price (null:
/// none) and how it was paid.
class SoldSheet extends StatefulWidget {
  const SoldSheet({super.key, required this.auth, required this.thing, this.priceOnly = false});
  final Auth auth;
  final Thing thing;

  /// Marked sold already, without a price: the price is asked, not optional.
  final bool priceOnly;

  @override
  State<SoldSheet> createState() => _SoldSheetState();
}

class _SoldSheetState extends State<SoldSheet> {
  final _price = TextEditingController();
  String _how = 'cash';

  @override
  void initState() {
    super.initState();
    _price.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _price.dispose();
    super.dispose();
  }

  Widget _paid(String v, String words, String sub) => Semantics(
    selected: _how == v,
    child: TkListRow(
      title: words,
      sub: sub,
      icon: v == 'cash' ? 'b-cash' : 'b-bank',
      square: v == 'cash' ? TkLook.of(context).teal : TkLook.of(context).blue,
      chevron: false,
      trailing: TkTick(_how == v),
      onTap: () => setState(() => _how = v),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final money = widget.auth.can('invoice.manage');
    final typed = _price.text.trim();
    final kobo = typed.isEmpty ? null : nairaKobo(typed);
    final bad = typed.isNotEmpty && (kobo == null || kobo < 1);
    final only = widget.priceOnly && money;
    return TkFormSheet(
      title: only ? 'What it sold for' : 'Sold',
      crumb: widget.thing.name,
      action: kobo == null || !money ? (only ? 'Add the price' : 'Mark it sold') : 'Sold for ${naira(kobo)}',
      onAction: bad || (only && kobo == null) ? null : () => Navigator.of(context).pop((kobo: money ? kobo : null, how: _how)),
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        if (money) ...[
          TkFormGroup(
            children: [
              TkMoneyField(label: only ? 'Sold for' : 'Sold for (optional)', controller: _price, error: bad ? 'An amount, like 85,000.' : null),
            ],
          ),
          if (kobo != null) TkFormGroup(title: 'Paid', children: [_paid('cash', 'In cash', 'Into the cash you hold'), _paid('transfer', 'By transfer', 'Into your bank')]),
          Text(
            only
                ? 'It is money in, as Sale of things we had: not spending on its label.'
                : 'With a price, it is money in, as Sale of things we had: not spending on its label. Without one, it only leaves the totals.',
            style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
          ),
        ] else
          TkCallout(
            leading: TkSquare('b-lock', color: look.blue),
            title: 'Marked sold without a price',
            sub: 'A price is money in, so only someone who manages money can record it. Ask them to, or record the sale in Cash and expenses.',
            color: look.blue,
            subLines: 4,
          ),
      ],
    );
  }
}

/// Add something we have (§5.3): bought before Taskiem or outside it, by
/// hand. Pops with the new row.
class AddThingSheet extends StatefulWidget {
  const AddThingSheet({super.key, required this.auth, this.now});
  final Auth auth;

  /// The clock (a test sets it).
  final DateTime Function()? now;

  @override
  State<AddThingSheet> createState() => _AddThingSheetState();
}

class _AddThingSheetState extends State<AddThingSheet> {
  final _name = TextEditingController();
  final _cost = TextEditingController();
  final _serial = TextEditingController();
  final _note = TextEditingController();
  late final _choices = TreatChoices(widget.auth);
  ({String? id, String name})? _label;
  ({String id, String name})? _supplier;
  List<Map<String, dynamic>>? _suppliers;
  DateTime? _came;
  DateTime? _warranty;
  bool _busy = false;
  String? _error;

  List<TextEditingController> get _all => [_name, _cost, _serial, _note];

  /// The name once it settles, for its suggested label; the label picked
  /// by the person (or No label), which a suggestion never replaces.
  String _named = '';
  Timer? _settle;
  bool _picked = false;

  /// The label as it will be added: picked, else the server's suggestion
  /// for the name (made when the row is added, if not a label yet).
  ({String? id, String name})? get _labelNow => _picked ? _label : (_named.isEmpty ? null : _choices.suggestedLabel(_named));

  @override
  void initState() {
    super.initState();
    for (final c in _all) {
      c.addListener(() => setState(() {}));
    }
    _name.addListener(() {
      _settle?.cancel();
      _settle = Timer(const Duration(milliseconds: 600), () {
        if (mounted && _named != _name.text.trim()) setState(() => _named = _name.text.trim());
      });
    });
    _choices.addListener(() {
      if (mounted) setState(() {});
    });
    _choices.load();
    () async {
      try {
        final r = (await widget.auth.transport.send('GET', '/v1/stock/suppliers') as Map).cast<String, dynamic>();
        if (mounted) setState(() => _suppliers = (r['suppliers'] as List? ?? const []).whereType<Map<String, dynamic>>().toList());
      } on Object {
        if (mounted) setState(() => _suppliers = const []);
      }
    }();
  }

  @override
  void dispose() {
    for (final c in _all) {
      c.dispose();
    }
    _settle?.cancel();
    _choices.dispose();
    super.dispose();
  }

  String? get _problem {
    if (_name.text.trim().isEmpty) return 'Give it a name.';
    if (_cost.text.trim().isNotEmpty && nairaKobo(_cost.text) == null) return 'The cost is an amount, like 450,000.';
    return null;
  }

  Future<void> _pickLabel() async {
    if (!_choices.ready) await _choices.load();
    if (!mounted) return;
    final l = await showTkSheet<({String? id, String name})>(
      context,
      (_) => LabelPickSheet(choices: _choices, item: _name.text.trim().isEmpty ? 'Something we have' : _name.text.trim(), now: _labelNow?.id),
    );
    if (l != null && mounted) setState(() => (_label = l.name.isEmpty ? null : l, _picked = true));
  }

  Future<void> _pickSupplier() async {
    final list = _suppliers ?? const <Map<String, dynamic>>[];
    final id = await pickOneOf(
      context,
      title: 'From whom',
      value: _supplier?.id ?? '',
      searchHint: list.length > 8 ? 'Search your suppliers' : null,
      choices: [
        ('', 'Not said', 'Bought outside your suppliers'),
        for (final s in list) ('${s['id']}', '${s['name']}', ''),
      ],
    );
    if (id == null || !mounted) return;
    setState(() => _supplier = id.isEmpty ? null : (id: id, name: '${list.firstWhere((s) => s['id'] == id)['name']}'));
  }

  Future<DateTime?> _date(DateTime? had, {bool past = true}) {
    final now = (widget.now ?? DateTime.now)();
    return showDatePicker(
      context: context,
      initialDate: had ?? now,
      firstDate: DateTime(now.year - 20),
      lastDate: past ? now : DateTime(now.year + 20),
    );
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _error = null));
    try {
      // A suggested label not yet a label is made now.
      final l = _labelNow;
      final labelId = l == null ? null : (await _choices.madeOrNone(Treat(labelId: l.id, label: l.name))).labelId;
      final r = await widget.auth.api.addThing(
        body: AddThingRequest(
          name: _name.text.trim(),
          labelId: labelId,
          supplierId: _supplier?.id,
          costKobo: _cost.text.trim().isEmpty ? null : nairaKobo(_cost.text),
          receivedAt: _came == null ? null : DateTime.utc(_came!.year, _came!.month, _came!.day, 11),
          serial: _serial.text.trim().isEmpty ? null : _serial.text.trim(),
          warrantyUntil: _warranty == null ? null : _ymd(_warranty!),
          note: _note.text.trim().isEmpty ? null : _note.text.trim(),
        ),
      );
      if (mounted) Navigator.of(context).pop(r.thing);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = _why(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final problem = _problem;
    return TkFormSheet(
      title: 'Add something we have',
      crumb: 'Things we have',
      action: _busy ? 'Adding…' : 'Add it',
      onAction: _busy || problem != null ? null : _save,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(
          children: [
            TkKitField(
              label: 'Name',
              hint: 'A generator, a sewing machine, a laptop',
              child: TkTextBox(controller: _name, max: 200, size: 15, height: 46),
            ),
            TkMoneyField(label: 'What it cost', controller: _cost),
            TkKitField(
              field: false,
              label: 'When it came',
              child: TkPickBox(
                label: 'When it came',
                shown: _came == null ? 'Today' : dateLabel(_ymd(_came!)),
                onTap: () async {
                  final d = await _date(_came);
                  if (d != null && mounted) setState(() => _came = d);
                },
              ),
            ),
            TkKitField(
              field: false,
              label: 'From whom',
              child: TkPickBox(label: 'From whom', shown: _supplier?.name ?? 'Not said', onTap: _suppliers == null ? () {} : _pickSupplier),
            ),
            TkKitField(
              field: false,
              label: 'Label',
              hint: !_picked && _labelNow != null ? 'Suggested from its name' : null,
              child: TkPickBox(label: 'Label', shown: _labelNow?.name ?? 'Not labelled', onTap: _pickLabel),
            ),
          ],
        ),
        TkFormGroup(
          title: 'If you have them',
          children: [
            TkKitField(label: 'Serial number', child: TkTextBox(controller: _serial, max: 100, size: 15, height: 46)),
            TkKitField(
              field: false,
              label: 'Warranty ends',
              child: TkPickBox(
                label: 'Warranty ends',
                shown: _warranty == null ? 'None' : dateLabel(_ymd(_warranty!)),
                onTap: () async {
                  final d = await _date(_warranty, past: false);
                  if (d != null && mounted) setState(() => _warranty = d);
                },
              ),
            ),
            TkKitField(label: 'Note', child: TkTextBox(controller: _note, max: 500, lines: 3, size: 15, height: 46)),
          ],
        ),
        if (_error != null || (problem != null && _cost.text.trim().isNotEmpty))
          TkCallout(leading: TkSquare('b-alert', color: look.red), title: _error ?? problem!, color: look.red, subLines: 3),
      ],
    );
  }
}
