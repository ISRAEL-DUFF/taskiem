// A product, from the Products tool (M6 part P; web/src/pages/
// ChatToolForms.tsx ProductForm, ProductAdd, StockInForm, CategoryField;
// ProductPhotos.tsx; PhotoAdd.tsx): its photos, name, category, price (and
// for those who keep the storefront's prices, its price on a list, who
// sees the list, and the lowest price staff may agree), how many are on
// the shelf, its sizes and colours, and whether it shows. Each saves
// through the same API as the web's page; the server tells the change as
// a diary line.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/api_image.dart';
import '../kit/forms.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/modal.dart' show showTkModalChoices;
import '../kit/sheet.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart' show PickedFile;
import 'chats.dart' show toneColours;
import 'composer.dart' show Pickers;
import 'sizes_forms.dart';
import 'tags.dart' show quantity;
import 'tools.dart' show tileLetters;
import 'words.dart' show naira, toneOf;

/// PhotoStrip.tsx MAX_GALLERY: a product's photos past its main one.
const maxGallery = 5;

/// photos.ts SEND_SIDE: a photo's longest side as it is sent.
const sendSide = 1600.0;

/// FormFrame.tsx formKey: one change, sent once however often it is tried.
String formKey() => 'chat-${uuid4()}';

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int? _int(Object? v) => (v as num?)?.toInt();

/// toolforms.ts productLine: "New product: Garri, Ijebu, ₦32,000 a bag".
String productLine(String name, int kobo, [String? unit]) => 'New product: $name, ${naira(kobo)}${unit != null && unit.isNotEmpty ? ' a $unit' : ''}';

/// toolforms.ts stockInLine: "Stock in: Rice +20 bags (now 24 bags)".
String stockInLine(String name, int n, int? now, [String? unit]) =>
    'Stock in: $name +${quantity(n, unit)}${now == null ? '' : ' (now ${quantity(now + n, unit)})'}';

/// variants.ts hasSizes: a product sold in sizes and colours.
bool hasSizes(Map<String, dynamic> it) => ((it['choices'] as List?)?.length ?? 0) > 0;

/// variants.ts sizesWords: "12 sizes and colours", "1 size or colour".
String sizesWords(int n) => n == 1 ? '1 size or colour' : '$n sizes and colours';

/// variants.ts sizesLine: a product's sizes on sale, their stock and low.
({int count, int low, int? stock}) sizesLine(List<Map<String, dynamic>> items, String product) {
  final vs = items.where((x) => x['variant_of'] == product && x['available'] == true).toList();
  int? stock;
  for (final v in vs) {
    final n = _int(v['stock']);
    if (n != null) stock = (stock ?? 0) + n;
  }
  return (count: vs.length, low: vs.where((v) => v['low'] == true).length, stock: stock);
}

/// pricelists.ts listFor: the list a product's trade price is on: one
/// shared with this business and holding it, one shared with it, one
/// holding it, else the first.
Map<String, dynamic>? listFor(List<Map<String, dynamic>> lists, String itemId, String? link) {
  bool holds(Map<String, dynamic> l) => _maps(l['items']).any((x) => x['item_id'] == itemId);
  bool shared(Map<String, dynamic> l) => link != null && ((l['links'] as List?) ?? const []).contains(link);
  return lists.where((l) => shared(l) && holds(l)).firstOrNull ?? lists.where(shared).firstOrNull ?? lists.where(holds).firstOrNull ?? lists.firstOrNull;
}

/// pricelists.ts listLabel: "Trade price list", but "Trade prices" as it is.
String listLabel(String name) => RegExp(r'\bprices?\b|\bprice list\b', caseSensitive: false).hasMatch(name) ? name : '$name price list';

/// pricelists.ts sharedWords.
String sharedWords(List<String> ids, Map<String, String> names) {
  final shown = [for (final id in ids) ?names[id]];
  return shown.isEmpty ? 'No one yet' : shown.join(', ');
}

/// pricelists.ts itemsWithPrice: the list's items as PUT takes them, this
/// one at its new price (its breaks kept while under it), added if not on
/// it; each size's own price kept.
List<Map<String, Object?>> itemsWithPrice(Map<String, dynamic> list, String itemId, int kobo) {
  final items = _maps(list['items']);
  final out = <Map<String, Object?>>[
    for (final x in items)
      {
        'item_id': x['item_id'],
        'price_kobo': x['item_id'] == itemId ? kobo : x['price_kobo'],
        'min_quantity': x['min_quantity'],
        'available': x['available'],
        'breaks': x['item_id'] == itemId ? [for (final b in _maps(x['breaks'])) if ((_int(b['price_kobo']) ?? 0) < kobo) b] : x['breaks'],
        if (_maps(x['sizes']).any((z) => (_int(z['price_kobo']) ?? 0) > 0))
          'sizes': [
            for (final z in _maps(x['sizes']))
              if ((_int(z['price_kobo']) ?? 0) > 0) {'item_id': z['item_id'], 'price_kobo': z['price_kobo']},
          ],
      },
  ];
  if (!items.any((x) => x['item_id'] == itemId)) out.add({'item_id': itemId, 'price_kobo': kobo, 'min_quantity': 1, 'available': true});
  return out;
}

/// mentions.ts categoriesOf: the categories used, the most first.
List<({String name, int count})> categoriesUsed(List<Map<String, dynamic>> items) {
  final n = <String, ({String name, int count})>{};
  for (final it in items) {
    final c = (it['category'] as String? ?? '').trim();
    if (c.isEmpty) continue;
    final got = n[c.toLowerCase()];
    n[c.toLowerCase()] = got == null ? (name: c, count: 1) : (name: got.name, count: got.count + 1);
  }
  return n.values.toList()..sort((a, b) => b.count != a.count ? b.count - a.count : a.name.compareTo(b.name));
}

Future<List<Map<String, dynamic>>> _stockItems(Transport t) async {
  final r = await t.send('GET', '/v1/stock') as Map<String, dynamic>;
  return _maps(r['items']);
}

/// ChatToolForms.tsx CategoryField: any word typed, with the categories
/// already used offered as chips ("Sneakers · 48"), so spellings don't
/// drift.
class CategoryField extends StatefulWidget {
  const CategoryField({super.key, required this.transport, required this.controller});
  final Transport transport;
  final TextEditingController controller;

  @override
  State<CategoryField> createState() => _CategoryFieldState();
}

class _CategoryFieldState extends State<CategoryField> {
  List<({String name, int count})> _used = const [];

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_changed);
    _stockItems(widget.transport).then((items) {
      if (mounted) setState(() => _used = categoriesUsed(items));
    }, onError: (Object _) {});
  }

  void _changed() => setState(() {});

  @override
  void dispose() {
    widget.controller.removeListener(_changed);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final q = widget.controller.text.trim().toLowerCase();
    final offer = _used.where((c) => c.name.toLowerCase() != q && (q.isEmpty || c.name.toLowerCase().contains(q))).take(6).toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkKitField(
          label: 'Category',
          hint: 'Sneakers, Soft drinks… Search and the chips use it.',
          child: TkTextBox(controller: widget.controller, hint: 'None'),
        ),
        if (offer.isNotEmpty) ...[
          const SizedBox(height: 8),
          Semantics(
            label: 'Categories you use',
            container: true,
            child: TkChipRow(
              padding: EdgeInsets.zero,
              all: false,
              chips: [for (final c in offer) (label: '${c.name} · ${c.count}', tag: c.name)],
              on: null,
              onPick: (name) {
                if (name == null) return;
                widget.controller.text = name;
                widget.controller.selection = TextSelection.collapsed(offset: name.length);
              },
            ),
          ),
        ],
      ],
    );
  }
}

/// PhotoAdd.tsx: the ways to add a photo: the phone's camera, its photos,
/// or a link Taskiem fetches once.
class PhotoAdd extends StatefulWidget {
  const PhotoAdd({super.key, required this.pickers, required this.room, required this.onPick, this.busy = false, this.onError});

  final Pickers pickers;

  /// How many more the product can hold.
  final int room;
  final void Function(List<PhotoPick> picks) onPick;
  final bool busy;

  /// The phone would not open its camera or photos.
  final ValueChanged<String>? onError;

  @override
  State<PhotoAdd> createState() => _PhotoAddState();
}

/// photos.ts PhotoPick: a photo chosen, a file or a link.
typedef PhotoPick = ({PickedFile? file, String? url});

/// photos.ts linkProblem.
String? linkProblem(String raw) {
  final v = raw.trim();
  if (v.isEmpty) return 'Paste the link of a picture.';
  final u = Uri.tryParse(v);
  if (u == null || !(u.scheme == 'https' || u.scheme == 'http') || u.host.isEmpty) return 'That is not a link: it starts with https://';
  return null;
}

/// photos.ts pickName: a file's name, or the link's site.
String pickName(PhotoPick p) {
  if (p.file != null) return p.file!.name.isEmpty ? 'a photo' : p.file!.name;
  final u = Uri.tryParse(p.url ?? '');
  return u == null || u.host.isEmpty ? (p.url ?? '') : u.host.replaceFirst(RegExp(r'^www\.'), '');
}

class _PhotoAddState extends State<PhotoAdd> {
  bool _linking = false;
  final _link = TextEditingController();
  String? _problem;

  @override
  void dispose() {
    _link.dispose();
    super.dispose();
  }

  Future<void> _take(Future<List<PickedFile>> Function() pick) async {
    try {
      final fs = await pick();
      if (fs.isNotEmpty) widget.onPick([for (final f in fs.take(widget.room)) (file: f, url: null)]);
    } on Object {
      widget.onError?.call('Taskiem could not open your photos. Allow it in your phone’s settings, then try again.');
    }
  }

  void _addLink() {
    final bad = linkProblem(_link.text);
    setState(() => _problem = bad);
    if (bad != null) return;
    widget.onPick([(file: null, url: _link.text.trim())]);
    setState(() {
      _link.clear();
      _linking = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (widget.room <= 0) {
      return Text('This product has all the photos it can hold. Remove one to add another.', style: tkHint(t));
    }
    Widget way(String icon, String label, VoidCallback onTap) => _Way(icon: icon, label: label, onTap: widget.busy ? null : onTap);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Semantics(
          container: true,
          label: 'Add a photo',
          explicitChildNodes: true,
          // `.tk-photo-ways`: side by side, wrapping, each growing.
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              way('camera', 'Take a photo', () => _take(() async => [?await widget.pickers.camera(side: sendSide)])),
              way('image', 'From your phone', () => _take(() => widget.pickers.photos(widget.room, side: sendSide))),
              way('link', 'Paste a link', () => setState(() => (_linking = !_linking, _problem = null))),
            ],
          ),
        ),
        if (_linking) ...[
          const SizedBox(height: 10),
          TkKitField(
            label: 'Link to a picture',
            hint: 'Taskiem keeps its own copy, so it stays on your page.',
            error: _problem,
            child: TkTextBox(controller: _link, hint: 'https://', plain: true),
          ),
          const SizedBox(height: 10),
          Align(
            alignment: Alignment.centerLeft,
            child: TkSoftButton(label: 'Add this photo', primary: true, onTap: widget.busy ? null : _addLink),
          ),
        ],
      ],
    );
  }
}

/// `.tk-photo-ways .tk-btn.soft`: 40 high, 14 each side, 14 px, its icon.
class _Way extends StatelessWidget {
  const _Way({required this.icon, required this.label, this.onTap});
  final String icon;
  final String label;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final ink = dark ? t.text : t.brand;
    return TapArea(
      child: Semantics(
        button: true,
        enabled: onTap != null,
        label: label,
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: Opacity(
            opacity: onTap == null ? 0.5 : 1,
            child: Container(
              constraints: const BoxConstraints(minHeight: 40),
              padding: const EdgeInsets.symmetric(horizontal: 14),
              decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(999)),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TkIcon(icon, size: 18, color: ink),
                  const SizedBox(width: 8),
                  Text(label, style: tkText(size: 14, weight: 700, color: ink)),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// photos.ts sendPhoto: a photo to a catalogue row, added to its photos
/// ([add]) else as its main one; a file as it is, a link for Taskiem to
/// fetch.
Future<void> sendPhoto(Transport t, String row, PhotoPick p, {required bool add}) async {
  final path = '/v1/storefront/catalogue/rows/${Uri.encodeComponent(row)}/photo${add ? '?add=1' : ''}';
  if (p.url != null) {
    await t.send('POST', path, body: {'url': p.url!.trim()});
  } else {
    await t.sendFile(path, bytes: p.file!.bytes, type: p.file!.type);
  }
}

String _why(Object e) => e is ApiError ? e.message : (e is Offline ? 'You’re offline.' : 'It did not go.');

/// ProductPhotos.tsx sendAll: one after another; those that did not go,
/// each with why.
Future<List<({PhotoPick pick, String why})>> sendAll(Transport t, String row, List<PhotoPick> picks, int have, {VoidCallback? onSent}) async {
  final failed = <({PhotoPick pick, String why})>[];
  var n = have;
  for (final p in picks) {
    try {
      await sendPhoto(t, row, p, add: n > 0);
      n++;
      onSent?.call();
    } on Object catch (e) {
      failed.add((pick: p, why: _why(e)));
    }
  }
  return failed;
}

/// ProductPhotos.tsx SendingLine.
Widget sendingLine(BuildContext context, ({int done, int of})? s) {
  if (s == null) return const SizedBox.shrink();
  final t = TkTheme.tokens(context);
  return Semantics(
    liveRegion: true,
    child: Text(s.of == 1 ? 'Sending the photo…' : 'Sending photo ${s.done + 1 > s.of ? s.of : s.done + 1} of ${s.of}…', style: tkHint(t)),
  );
}

/// ProductPhotos.tsx PhotoFailures: what did not go, and Try again.
class PhotoFailures extends StatelessWidget {
  const PhotoFailures({super.key, required this.failed, this.onRetry, this.busy = false});
  final List<({PhotoPick pick, String why})> failed;
  final VoidCallback? onRetry;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    if (failed.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TkErrorBox(
          [
            failed.length == 1 ? 'A photo did not go:' : '${failed.length} photos did not go:',
            for (final f in failed) '• ${pickName(f.pick)}: ${f.why}',
          ].join('\n'),
        ),
        if (onRetry != null) ...[
          const SizedBox(height: 8),
          Align(alignment: Alignment.centerLeft, child: TkSoftButton(label: 'Try again', onTap: busy ? null : onRetry)),
        ],
      ],
    );
  }
}

/// `.tk-photos`: the main one 112 square (or its letters on its tone), the
/// rest 72; each with its links under it.
class _PhotoStrip extends StatelessWidget {
  const _PhotoStrip({required this.name, required this.tiles, this.label});
  final String name;
  final List<Widget> tiles;
  final String? label;

  @override
  Widget build(BuildContext context) => Semantics(
    container: true,
    label: label,
    explicitChildNodes: true,
    child: Wrap(spacing: 10, runSpacing: 10, crossAxisAlignment: WrapCrossAlignment.end, children: tiles),
  );
}

/// `.tk-photo-none`: a product's letters, 112 square, on its name's tone.
class PhotoNone extends StatelessWidget {
  const PhotoNone({super.key, required this.name});
  final String name;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final (bg, ink) = toneColours(t, toneOf(name));
    return Semantics(
      label: 'No photo yet',
      excludeSemantics: true,
      child: Container(
        width: 112,
        height: 112,
        decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(18)),
        alignment: Alignment.center,
        child: Text(tileLetters(name), style: tkText(size: 28, weight: 800, color: ink)),
      ),
    );
  }
}

/// `.tk-photo-acts`: small links under a photo (Make main, Remove).
class _PhotoLink extends StatelessWidget {
  const _PhotoLink({required this.label, required this.said, this.onTap});
  final String label;
  final String said;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(
      child: Semantics(
        button: true,
        enabled: onTap != null,
        label: said,
        onTap: onTap,
        excludeSemantics: true,
        child: GestureDetector(
          onTap: onTap,
          child: Text(label, style: tkText(size: 13, color: t.accent, decoration: TextDecoration.underline)),
        ),
      ),
    );
  }
}

/// ProductPhotos.tsx: a product's photos, each with Make main and Remove,
/// and the ways to add one; changing them needs storefront.manage.
class ProductPhotos extends StatefulWidget {
  const ProductPhotos({super.key, required this.auth, required this.row, required this.name, required this.canEdit, required this.pickers});
  final Auth auth;
  final String row;
  final String name;
  final bool canEdit;
  final Pickers pickers;

  @override
  State<ProductPhotos> createState() => _ProductPhotosState();
}

class _ProductPhotosState extends State<ProductPhotos> {
  Map<String, dynamic>? _item;
  bool _read = false;
  ({int done, int of})? _sending;
  List<({PhotoPick pick, String why})> _failed = const [];
  String? _error;

  Transport get _t => widget.auth.transport;
  String get _path => '/v1/storefront/catalogue/rows/${Uri.encodeComponent(widget.row)}/photo';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await _t.send('GET', '/v1/storefront/catalogue') as Map<String, dynamic>;
      _item = _maps(r['items']).where((x) => x['id'] == widget.row).firstOrNull;
    } on Object {
      _item = null;
    }
    if (mounted) setState(() => _read = true);
  }

  /// What the page shows changes with the photos: read again.
  Future<void> _refresh() async {
    try {
      await _t.send('POST', '/v1/storefront/catalogue/refresh', body: const {});
    } on Object {
      // Read as it is.
    }
    await _load();
  }

  List<String> get _ids => ((_item?['photo_ids'] as List?) ?? const []).whereType<String>().toList();

  Future<void> _send(List<PhotoPick> picks) async {
    setState(() {
      _failed = const [];
      _error = null;
      _sending = (done: 0, of: picks.length);
    });
    final bad = await sendAll(_t, widget.row, picks, _ids.length, onSent: () {
      if (mounted) setState(() => _sending = (done: _sending!.done + 1, of: _sending!.of));
    });
    if (!mounted) return;
    setState(() {
      _sending = null;
      _failed = bad;
    });
    await _refresh();
  }

  Future<void> _act(Future<void> Function() f) async {
    setState(() => _error = null);
    try {
      await f();
      await _refresh();
    } on Object catch (e) {
      if (mounted) setState(() => _error = _why(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final may = widget.canEdit && widget.auth.can('storefront.manage');
    final shots = [
      if (_item?['photo'] is Map) (_item!['photo'] as Map).cast<String, dynamic>(),
      for (final g in (_item?['gallery'] as List? ?? const []).whereType<Map>()) g.cast<String, dynamic>(),
    ];
    final ids = _ids;
    final quiet = _sending != null;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _PhotoStrip(
          name: widget.name,
          label: 'Photos of ${widget.name}',
          tiles: [
            if (shots.isEmpty) PhotoNone(name: widget.name),
            for (var i = 0; i < shots.length; i++)
              Column(
                key: ValueKey(i < ids.length ? ids[i] : 'shot$i'),
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Semantics(
                    image: true,
                    label: i == 0 ? '${widget.name}, its main photo' : '${widget.name}, photo ${i + 1}',
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(i == 0 ? 18 : 14),
                      child: _thumb(shots[i]) == null
                          ? Container(width: i == 0 ? 112 : 72, height: i == 0 ? 112 : 72, color: t.tkLimeSoft)
                          : ApiImage(transport: _t, path: _thumb(shots[i])!, width: i == 0 ? 112 : 72, height: i == 0 ? 112 : 72),
                    ),
                  ),
                  if (may && i < ids.length) ...[
                    const SizedBox(height: 4),
                    Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (i > 0) ...[
                          _PhotoLink(
                            label: 'Make main',
                            said: 'Make main, photo ${i + 1}',
                            onTap: quiet ? null : () => _act(() async => _t.send('PUT', '$_path/main?media=${Uri.encodeQueryComponent(ids[i])}')),
                          ),
                          const SizedBox(width: 10),
                        ],
                        _PhotoLink(
                          label: 'Remove',
                          said: 'Remove, ${i == 0 ? 'the main photo' : 'photo ${i + 1}'}',
                          onTap: quiet ? null : () => _act(() async => _t.send('DELETE', '$_path?media=${Uri.encodeQueryComponent(ids[i])}')),
                        ),
                      ],
                    ),
                  ],
                ],
              ),
          ],
        ),
        if (may && _read) ...[
          const SizedBox(height: 10),
          PhotoAdd(
            pickers: widget.pickers,
            room: maxGallery + 1 - ids.length,
            busy: quiet,
            onPick: _send,
            onError: (w) => setState(() => _error = w),
          ),
        ],
        if (_sending != null) ...[const SizedBox(height: 8), sendingLine(context, _sending)],
        if (_failed.isNotEmpty) ...[
          const SizedBox(height: 8),
          PhotoFailures(failed: _failed, busy: quiet, onRetry: () => _send([for (final f in _failed) f.pick])),
        ],
        if (_error != null) ...[const SizedBox(height: 8), TkErrorBox(_error!)],
      ],
    );
  }
}

/// storefront.ts thumb: a picture's small size, through the API.
String? _thumb(Map<String, dynamic> img) {
  final u = img['320'] ?? img['800'] ?? img['2000'];
  return u is String ? '/v1/storefront/image?src=${Uri.encodeQueryComponent(u)}' : null;
}

/// ProductPhotos.tsx PendingPhotos: photos chosen for a product not yet
/// made, each with Remove; sent once it exists.
class PendingPhotos extends StatelessWidget {
  const PendingPhotos({super.key, required this.picks, required this.onChange, required this.pickers, this.busy = false, this.onError});
  final List<PhotoPick> picks;
  final ValueChanged<List<PhotoPick>> onChange;
  final Pickers pickers;
  final bool busy;
  final ValueChanged<String>? onError;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (picks.isNotEmpty) ...[
          _PhotoStrip(
            name: '',
            label: 'Photos to add',
            tiles: [
              for (var i = 0; i < picks.length; i++)
                Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    ClipRRect(
                      borderRadius: BorderRadius.circular(i == 0 ? 18 : 14),
                      child: picks[i].file != null
                          ? Image.memory(
                              picks[i].file!.bytes,
                              width: i == 0 ? 112 : 72,
                              height: i == 0 ? 112 : 72,
                              fit: BoxFit.cover,
                              semanticLabel: 'Photo ${i + 1} to add',
                            )
                          : Container(
                              width: 72,
                              height: 72,
                              padding: const EdgeInsets.all(6),
                              color: t.tkSoft,
                              alignment: Alignment.center,
                              child: Text(pickName(picks[i]), textAlign: TextAlign.center, style: tkText(size: 11, weight: 600, color: t.text)),
                            ),
                    ),
                    const SizedBox(height: 4),
                    _PhotoLink(
                      label: 'Remove',
                      said: 'Remove photo ${i + 1}',
                      onTap: busy ? null : () => onChange([...picks]..removeAt(i)),
                    ),
                  ],
                ),
            ],
          ),
          const SizedBox(height: 10),
        ],
        PhotoAdd(pickers: pickers, room: maxGallery + 1 - picks.length, busy: busy, onPick: (more) => onChange([...picks, ...more]), onError: onError),
      ],
    );
  }
}

/// ChatToolForms.tsx ProductForm.
class ProductForm extends StatefulWidget {
  const ProductForm({
    super.key,
    required this.auth,
    required this.item,
    required this.onSaved,
    this.canEdit = true,
    this.onCancel,
    this.link,
    this.pickers = const Pickers(),
    this.onSizes,
  });
  final Auth auth;
  final Map<String, dynamic> item;
  final void Function(String? line) onSaved;
  final bool canEdit;
  final VoidCallback? onCancel;

  /// The business whose conversation it was opened from (its list first).
  final String? link;
  final Pickers pickers;

  /// Its sizes and colours, set up or changed.
  final VoidCallback? onSizes;

  @override
  State<ProductForm> createState() => _ProductFormState();
}

class _ProductFormState extends State<ProductForm> {
  Map<String, dynamic> get _it => widget.item;
  Transport get _t => widget.auth.transport;
  late final bool _trade = widget.auth.can('storefront.manage');

  late final _name = TextEditingController(text: _it['name'] as String? ?? '');
  late final _price = TextEditingController(text: _int(_it['price_kobo']) != null ? nairaText(_int(_it['price_kobo'])!) : '');
  late final _category = TextEditingController(text: _it['category'] as String? ?? '');
  final _tradePrice = TextEditingController();
  bool _tradeTyped = false;
  final _floor = TextEditingController();
  bool _floorTyped = false;
  late int? _count = _int(_it['stock']);
  late bool _shown = _it['available'] == true;

  List<Map<String, dynamic>>? _lists;
  List<Map<String, dynamic>>? _linked;
  Map<String, dynamic>? _floors;
  String? _listId;
  List<String>? _share;
  bool _picking = false;
  List<Map<String, dynamic>>? _all;

  /// Its sizes and colours, set up in place of the form.
  bool _sizesOpen = false;

  @override
  void initState() {
    super.initState();
    for (final c in [_name, _price, _category]) {
      c.addListener(() => setState(() {}));
    }
    _tradePrice.addListener(() => setState(() => _tradeTyped = true));
    _floor.addListener(() => setState(() => _floorTyped = true));
    if (_trade) {
      _get('/v1/price-lists', (r) => _lists = _maps(r['lists']));
      _get('/v1/links', (r) => _linked = _maps(r['links']).where((l) => l['state'] == 'active').toList());
      _get('/v1/trade-floors', (r) => _floors = (r['floors'] as Map?)?.cast<String, dynamic>() ?? {});
    }
    if (hasSizes(_it)) _get('/v1/stock', (r) => _all = _maps(r['items']));
  }

  void _get(String path, void Function(Map<String, dynamic> r) use) {
    _t.send('GET', path).then((r) {
      if (!mounted || r is! Map<String, dynamic>) return;
      setState(() {
        use(r);
        _fillTyped();
      });
    }, onError: (Object _) {});
  }

  /// The trade price and the lowest price as they are, until typed.
  void _fillTyped() {
    final onList = _onList;
    if (!_tradeTyped) {
      _tradePrice.text = onList != null ? nairaText(_int(onList['price_kobo']) ?? 0) : '';
      _tradeTyped = false;
    }
    final was = _floorWas;
    if (!_floorTyped) {
      _floor.text = was != null ? nairaText(was) : '';
      _floorTyped = false;
    }
  }

  @override
  void dispose() {
    for (final c in [_name, _price, _category, _tradePrice, _floor]) {
      c.dispose();
    }
    super.dispose();
  }

  String get _id => _it['id'] as String;
  int? get _floorWas => _int(_floors?[_id]);
  Map<String, dynamic>? get _chosen {
    final ls = _lists;
    if (ls == null || ls.isEmpty) return null;
    return ls.where((l) => l['id'] == _listId).firstOrNull ?? listFor(ls, _id, widget.link);
  }

  Map<String, dynamic>? get _onList => _maps(_chosen?['items']).where((x) => x['item_id'] == _id).firstOrNull;
  List<String> get _sharing => _share ?? ((_chosen?['links'] as List?) ?? const []).whereType<String>().toList();

  Future<String?> _save() async {
    final said = <String>[];
    final change = <String, Object?>{};
    final kobo = nairaKobo(_price.text);
    if (kobo != null && kobo != _int(_it['price_kobo'])) change['price_kobo'] = kobo;
    final name = _name.text.trim();
    if (name.isNotEmpty && name != _it['name']) change['name'] = name;
    if (_shown != (_it['available'] == true)) change['available'] = _shown;
    if (_category.text.trim() != (_it['category'] as String? ?? '')) change['category'] = _category.text.trim();
    if (change.isNotEmpty) {
      await _t.send('PATCH', '/v1/stock/items/${Uri.encodeComponent(_id)}', body: change);
      said.add('Saved');
    }
    final chosen = _chosen;
    final tradeKobo = nairaKobo(_tradePrice.text);
    if (chosen != null && _tradeTyped && _tradePrice.text.trim().isNotEmpty && tradeKobo != null && tradeKobo != _int(_onList?['price_kobo'])) {
      await _t.send('PUT', '/v1/price-lists/${chosen['id']}/items', body: {'items': itemsWithPrice(chosen, _id, tradeKobo)});
      said.add('Priced');
    }
    final share = _share;
    if (chosen != null && share != null) {
      final was = ((chosen['links'] as List?) ?? const []).whereType<String>().toList()..sort();
      if (([...share]..sort()).join(',') != was.join(',')) {
        await _t.send('PUT', '/v1/price-lists/${chosen['id']}/links', body: {'links': share});
        said.add('Shared');
      }
    }
    final floorKobo = _floor.text.trim().isEmpty ? 0 : nairaKobo(_floor.text);
    if (_floors != null && _floorTyped && floorKobo != null && floorKobo != (_floorWas ?? 0)) {
      await _t.send('PUT', '/v1/trade-floors', body: {'item_id': _id, 'floor_kobo': floorKobo});
      said.add('Lowest price set');
    }
    if (_count != null && _count != _int(_it['stock'])) {
      await _t.send(
        'POST',
        '/v1/stock/movements',
        body: {'item_id': _id, 'reason': 'count', 'count': _count, 'note': 'Counted in chat'},
        headers: {'Idempotency-Key': formKey()},
      );
      said.add('Counted');
    }
    return said.isEmpty ? null : '${name.isEmpty ? _it['name'] : name}: saved';
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final chosen = _chosen;
    final kobo = nairaKobo(_price.text);
    final tradeKobo = nairaKobo(_tradePrice.text);
    final floorKobo = _floor.text.trim().isEmpty ? 0 : nairaKobo(_floor.text);
    final names = {for (final l in _linked ?? const <Map<String, dynamic>>[]) l['id'] as String: l['name'] as String? ?? ''};
    final sizes = hasSizes(_it);
    final line = sizes && _all != null ? sizesLine(_all!, _id) : null;
    if (_sizesOpen) {
      return SizesEditor(auth: widget.auth, item: _it, onSaved: widget.onSaved, onCancel: () => setState(() => _sizesOpen = false), pickers: widget.pickers);
    }
    return TkToolForm(
      save: _save,
      onSaved: widget.onSaved,
      canEdit: widget.canEdit,
      onCancel: widget.onCancel,
      ready:
          _name.text.trim().isNotEmpty &&
          (_price.text.trim().isEmpty || kobo != null) &&
          (_tradePrice.text.trim().isEmpty || tradeKobo != null) &&
          floorKobo != null,
      children: [
        ProductPhotos(auth: widget.auth, row: _id, name: _it['name'] as String? ?? '', canEdit: widget.canEdit, pickers: widget.pickers),
        TkKitField(label: 'Name', child: TkTextBox(controller: _name)),
        CategoryField(transport: _t, controller: _category),
        if (chosen != null) ...[
          // `.tk-form-two`: two columns, 12 apart.
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(child: TkMoneyField(label: 'Storefront price', controller: _price)),
              const SizedBox(width: 12),
              Expanded(child: TkMoneyField(label: listLabel(chosen['name'] as String? ?? ''), controller: _tradePrice)),
            ],
          ),
          if ((_lists?.length ?? 0) > 1)
            _ListPick(
              lists: _lists!,
              value: chosen['id'] as String,
              onPick: (id) => setState(() {
                _listId = id;
                _tradeTyped = false;
                _share = null;
                _picking = false;
                _fillTyped();
              }),
            ),
          // `.tk-share-box`: who sees the list, and Change.
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
            decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(14)),
            child: Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('Shared with', style: tkText(size: 15, weight: 600, color: t.text)),
                      const SizedBox(height: 2),
                      Text(sharedWords(_sharing, names), style: tkText(size: 13, color: t.muted)),
                    ],
                  ),
                ),
                const SizedBox(width: 12),
                TapArea(
                  child: Semantics(
                    button: true,
                    expanded: _picking,
                    label: '${_picking ? 'Done' : 'Change'} who sees the ${chosen['name']} list',
                    onTap: () => setState(() => _picking = !_picking),
                    excludeSemantics: true,
                    child: GestureDetector(
                      onTap: () => setState(() => _picking = !_picking),
                      child: Container(
                        constraints: const BoxConstraints(minHeight: 36),
                        padding: const EdgeInsets.symmetric(horizontal: 4),
                        alignment: Alignment.center,
                        child: Text(_picking ? 'Done' : 'Change', style: tkText(size: 14, weight: 700, color: dark ? t.brandDark : t.brand)),
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
          if (_picking)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
              decoration: BoxDecoration(border: Border.all(color: t.border), borderRadius: BorderRadius.circular(14)),
              child: (_linked ?? const []).isEmpty
                  ? Text('No linked businesses yet. Link with a business to share a list.', style: tkText(size: 13, color: t.muted))
                  : Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        for (final l in _linked!)
                          TapArea(
                            child: Semantics(
                              checked: _sharing.contains(l['id']),
                              label: l['name'] as String? ?? '',
                              excludeSemantics: true,
                              child: GestureDetector(
                                behavior: HitTestBehavior.opaque,
                                onTap: () => setState(
                                  () => _share = _sharing.contains(l['id']) ? (_sharing.where((x) => x != l['id']).toList()) : [..._sharing, l['id'] as String],
                                ),
                                child: ConstrainedBox(
                                  constraints: const BoxConstraints(minHeight: 40),
                                  child: Row(
                                    children: [
                                      TkCheckbox(checked: _sharing.contains(l['id'])),
                                      const SizedBox(width: 10),
                                      Expanded(child: Text(l['name'] as String? ?? '', style: tkText(size: 14, weight: 500, color: t.text))),
                                    ],
                                  ),
                                ),
                              ),
                            ),
                          ),
                      ],
                    ),
            ),
          if (_floors != null)
            TkMoneyField(
              label: 'Lowest price',
              controller: _floor,
              hint: (floorKobo ?? 0) > 0 && (tradeKobo ?? 0) > 0 && floorKobo! > tradeKobo!
                  ? 'Higher than the trade price: every offer will wait for you.'
                  : 'Staff can agree offers down to this. Under it, an offer waits for your OK. Buyers never see it.',
            ),
        ] else
          TkMoneyField(label: 'Price', controller: _price),
        if (!sizes)
          TkFormRow(
            title: 'In stock',
            sub: _count == null ? 'No count yet: set one' : 'Counts down as orders are accepted',
            trailing: TkStepper(label: 'In stock', value: _count ?? 0, onChanged: (n) => setState(() => _count = n)),
          ),
        TkFormRow(
          title: 'Sizes and colours',
          sub: !sizes
              ? 'A count for each size and colour'
              : line == null
              ? 'Loading…'
              : '${sizesWords(line.count)}${line.stock != null ? ' · ${line.stock} in stock' : ''}${line.low > 0 ? ' · ${line.low} low' : ''}',
          trailing: widget.canEdit
              ? TkSoftButton(
                  label: sizes ? 'Change' : 'Set up',
                  semanticsLabel: '${sizes ? 'Change' : 'Set up'} sizes and colours',
                  onTap: widget.onSizes ?? () => setState(() => _sizesOpen = true),
                )
              : const SizedBox.shrink(),
        ),
        TkSwitchRow(label: 'Show on your storefront', hint: 'And on price lists you share', on: _shown, onChanged: (v) => setState(() => _shown = v)),
        if (chosen != null)
          // `.tk-share-note`: dashed, on the green tone.
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
            decoration: BoxDecoration(
              color: t.toneGreenBg,
              borderRadius: BorderRadius.circular(14),
              border: Border.all(color: Color.lerp(t.borderStrong, t.brandDark, 0.7)!),
            ),
            child: Text.rich(
              TextSpan(
                children: [
                  TextSpan(text: 'Saving posts a line in this chat', style: tkText(size: 13, weight: 700, lineHeight: 1.45, color: t.toneGreenInk)),
                  TextSpan(text: ' and updates the price for every business on the ${listLabel(chosen['name'] as String? ?? '')}.'),
                ],
              ),
              style: tkText(size: 13, lineHeight: 1.45, color: t.toneGreenInk),
            ),
          ),
      ],
    );
  }
}

/// `.tk-list-pick`: "Price list" and the list chosen, pressed to choose.
class _ListPick extends StatelessWidget {
  const _ListPick({required this.lists, required this.value, required this.onPick});
  final List<Map<String, dynamic>> lists;
  final String value;
  final ValueChanged<String> onPick;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final now = lists.where((l) => l['id'] == value).map((l) => l['name'] as String? ?? '').firstOrNull ?? '';
    Future<void> open() async {
      final v = await showTkModalChoices(context, title: 'Price list', choices: [for (final l in lists) (l['id'] as String, l['name'] as String? ?? '')], value: value);
      if (v != null) onPick(v);
    }

    return Row(
      children: [
        Expanded(child: Text('Price list', style: tkText(size: 13, weight: 600, color: t.muted))),
        const SizedBox(width: 12),
        TapArea(
          child: Semantics(
            button: true,
            label: 'Price list: $now',
            onTap: open,
            excludeSemantics: true,
            child: GestureDetector(
              onTap: open,
              child: Container(
                constraints: const BoxConstraints(minHeight: 40, maxWidth: 200),
                padding: const EdgeInsets.symmetric(horizontal: 14),
                decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(12)),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Flexible(child: Text(now, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 15, weight: 600, color: t.text))),
                    const SizedBox(width: 8),
                    RotatedBox(quarterTurns: 1, child: TkIcon('chevron', size: 12, color: t.text)),
                  ],
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}


/// ChatToolForms.tsx ProductAdd: a new product, its name, category,
/// price, unit and how many; its photos held until it exists, then sent.
class ProductAdd extends StatefulWidget {
  const ProductAdd({super.key, required this.auth, required this.onSaved, this.pickers = const Pickers()});
  final Auth auth;
  final void Function(String? line) onSaved;
  final Pickers pickers;

  @override
  State<ProductAdd> createState() => _ProductAddState();
}

class _ProductAddState extends State<ProductAdd> {
  final _name = TextEditingController();
  final _price = TextEditingController();
  final _unit = TextEditingController();
  final _category = TextEditingController();
  int _count = 0;
  List<PhotoPick> _picks = const [];
  String? _added;
  List<({PhotoPick pick, String why})> _failed = const [];
  ({int done, int of})? _sending;
  String? _photoError;
  late final _key = formKey();

  Transport get _t => widget.auth.transport;
  bool get _photosOK => widget.auth.can('storefront.manage');

  @override
  void initState() {
    super.initState();
    for (final c in [_name, _price, _unit, _category]) {
      c.addListener(() => setState(() {}));
    }
  }

  @override
  void dispose() {
    for (final c in [_name, _price, _unit, _category]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<String?> _save() async {
    final kobo = nairaKobo(_price.text)!;
    var id = _added;
    if (id == null) {
      final out = await _t.send(
        'POST',
        '/v1/stock/items',
        body: {
          'name': _name.text.trim(),
          'price_kobo': kobo,
          'unit': _unit.text.trim(),
          'quantity': _count,
          if (_category.text.trim().isNotEmpty) 'category': _category.text.trim(),
        },
        headers: {'Idempotency-Key': _key},
      ) as Map<String, dynamic>;
      id = (out['item'] as Map)['id'] as String;
    }
    final todo = _added != null ? [for (final f in _failed) f.pick] : (_photosOK ? _picks : const <PhotoPick>[]);
    if (todo.isNotEmpty) {
      setState(() => _sending = (done: 0, of: todo.length));
      final bad = await sendAll(_t, id, todo, _added != null ? _picks.length - _failed.length : 0, onSent: () {
        if (mounted) setState(() => _sending = (done: _sending!.done + 1, of: _sending!.of));
      });
      if (mounted) setState(() => _sending = null);
      try {
        await _t.send('POST', '/v1/storefront/catalogue/refresh', body: const {});
      } on Object {
        // The page reads them later.
      }
      if (bad.isNotEmpty) {
        setState(() {
          _added = id;
          _failed = bad;
        });
        throw FormProblem(
          '${_name.text.trim()} is added, but ${bad.length == 1 ? 'a photo' : '${bad.length} photos'} did not go. Press Send the photos again, or close: the product stays.',
        );
      }
    }
    return productLine(_name.text.trim(), kobo, _unit.text.trim());
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final kobo = nairaKobo(_price.text);
    return TkToolForm(
      save: _save,
      onSaved: widget.onSaved,
      saveWords: _added != null ? 'Send the photos again' : 'Add product',
      ready: _name.text.trim().isNotEmpty && kobo != null && kobo > 0,
      children: [
        if (_sending != null) sendingLine(context, _sending),
        if (_photosOK)
          if (_added != null)
            PhotoFailures(failed: _failed)
          else
            PendingPhotos(
              picks: _picks,
              pickers: widget.pickers,
              onChange: (p) => setState(() => _picks = p),
              onError: (w) => setState(() => _photoError = w),
            ),
        if (_photoError != null) TkErrorBox(_photoError!),
        if (_added != null)
          Text('${_name.text.trim()} is in your products.', style: tkFormNow(t))
        else ...[
          TkKitField(label: 'Name', child: TkTextBox(controller: _name, hint: 'Rice, 50kg bag')),
          CategoryField(transport: _t, controller: _category),
          TkMoneyField(label: 'Price', controller: _price),
          TkKitField(label: 'Sold by the', hint: 'bag, carton, piece…', child: TkTextBox(controller: _unit)),
          TkFormRow(
            title: 'In stock now',
            trailing: TkStepper(label: 'In stock now', value: _count, onChanged: (n) => setState(() => _count = n)),
          ),
        ],
      ],
    );
  }
}

/// ChatToolForms.tsx StockInForm: goods that came in.
class StockInForm extends StatefulWidget {
  const StockInForm({super.key, required this.auth, required this.item, required this.onSaved, this.canEdit = true});
  final Auth auth;
  final Map<String, dynamic> item;
  final void Function(String? line) onSaved;
  final bool canEdit;

  @override
  State<StockInForm> createState() => _StockInFormState();
}

class _StockInFormState extends State<StockInForm> {
  int _n = 1;
  final _note = TextEditingController();
  late final _key = formKey();

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  Future<String?> _save() async {
    final it = widget.item;
    await widget.auth.transport.send(
      'POST',
      '/v1/stock/movements',
      body: {'item_id': it['id'], 'reason': 'received', 'change': _n, 'note': _note.text.trim().isEmpty ? 'Recorded in chat' : _note.text.trim()},
      headers: {'Idempotency-Key': _key},
    );
    return stockInLine(it['name'] as String? ?? '', _n, _int(it['stock']), it['unit'] as String?);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final it = widget.item;
    // A product with sizes and colours: goods received by its grid.
    if (hasSizes(it)) return GridStockIn(auth: widget.auth, item: it, onSaved: widget.onSaved, canEdit: widget.canEdit);
    final now = _int(it['stock']);
    return TkToolForm(
      save: _save,
      onSaved: widget.onSaved,
      saveWords: 'Record stock in',
      ready: _n > 0,
      canEdit: widget.canEdit,
      children: [
        Text('Now: ${now == null ? 'no count yet' : quantity(now, it['unit'] as String?)}', style: tkFormNow(t)),
        TkFormRow(title: 'Came in', trailing: TkStepper(label: 'Came in', value: _n, onChanged: (n) => setState(() => _n = n))),
        TkKitField(label: 'Note', hint: 'Who brought it, a delivery note…', child: TkTextBox(controller: _note)),
      ],
    );
  }
}
