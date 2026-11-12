// Your business in Explore (web/src/pages/BusinessProfile.tsx,
// /settings/links/profile; docs/spec/business-links.md "Business profile
// and gallery"): being found, the card as other businesses see it, the
// logo, the words, delivering for other businesses, and up to 12 photos
// (added from the phone, removed, moved, captioned). Opened from the
// chats' ⋯ menu and from Linked businesses.

import 'dart:async';

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../kit/api_image.dart';
import '../kit/forms.dart' show RuneLimit, TkSwitch;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkSoftButton;
import '../kit/modal.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'chats.dart';
import 'attachments.dart' show PickedFile;
import 'composer.dart' show Pickers;
import 'thread.dart' show ChatHeadBar;

const maxName = 80, maxTagline = 140, maxArea = 80, maxAbout = 1000, maxCaption = 120, maxVehicle = 40, maxDeliveryAreas = 20;

/// businessProfile.ts splitAreas: "Lekki, Ajah, VI" as a list, each once.
List<String> splitAreas(String line) {
  final out = <String>[];
  for (final raw in line.split(RegExp(r'[,\n;]'))) {
    final a = raw.trim();
    if (a.isNotEmpty && !out.any((x) => x.toLowerCase() == a.toLowerCase())) out.add(a);
  }
  return out.take(maxDeliveryAreas).toList();
}

/// The letter that stands where a logo would be.
String profileLetter(String name) => name.trim().isEmpty ? '?' : String.fromCharCode(name.trim().runes.first).toUpperCase();

/// BusinessProfile.tsx foundLine: what being found means, in a line.
String foundLine(Map<String, dynamic> s) {
  final f = s['findable'];
  if (f == null) {
    return s['findable_now'] == true
        ? 'On while your storefront is published. Businesses near you can find you and connect.'
        : 'Hidden until you publish your storefront or turn this on.';
  }
  return f == true ? 'Businesses near you can find you and connect.' : 'Hidden. Only businesses you are linked with see this.';
}

/// A photo's address through the API (businessProfile.ts photoSrc).
String? photoPath(Map<String, dynamic>? p, [String size = '320']) {
  final urls = (p?['urls'] as Map?)?.cast<String, dynamic>();
  final u = urls?[size] ?? urls?['800'] ?? urls?['320'] ?? urls?['2000'];
  return u is String ? '/v1/business-profile/image?src=${Uri.encodeQueryComponent(u)}' : null;
}

class ProfileScreen extends StatefulWidget {
  const ProfileScreen({super.key, required this.chats, this.pickers = const Pickers(), this.backLabel = 'Back'});
  final ChatsData chats;
  final Pickers pickers;

  /// Where Back goes, said ("Back to Linked businesses").
  final String backLabel;

  @override
  State<ProfileScreen> createState() => _ProfileScreenState();
}

class _ProfileScreenState extends State<ProfileScreen> {
  Map<String, dynamic>? _data;
  Map<String, dynamic>? _settings;
  String? _error;
  bool _busy = false;
  int _uploading = 0;
  final _name = TextEditingController();
  final _tagline = TextEditingController();
  final _area = TextEditingController();
  final _about = TextEditingController();
  final _vehicle = TextEditingController();
  final _areas = TextEditingController();
  bool _delivers = false;

  Transport get _t => widget.chats.auth.transport;
  bool get _manage => widget.chats.auth.can('link.manage');

  @override
  void initState() {
    super.initState();
    for (final c in [_name, _tagline, _area, _about, _vehicle, _areas]) {
      c.addListener(() => setState(() {}));
    }
    _load();
  }

  @override
  void dispose() {
    for (final c in [_name, _tagline, _area, _about, _vehicle, _areas]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _load() async {
    await _try(() async {
      final d = await _t.send('GET', '/v1/business-profile') as Map<String, dynamic>;
      _fill(d);
    });
    await _try(() async {
      final s = await _t.send('GET', '/v1/link-settings') as Map<String, dynamic>;
      if (mounted) setState(() => _settings = s);
    });
  }

  void _fill(Map<String, dynamic> d) {
    if (!mounted) return;
    setState(() {
      _data = d;
      _name.text = d['name'] as String? ?? '';
      _tagline.text = d['tagline'] as String? ?? '';
      _area.text = d['area'] as String? ?? '';
      _about.text = d['about'] as String? ?? '';
      _delivers = d['delivers'] == true;
      _vehicle.text = d['vehicle'] as String? ?? '';
      _areas.text = ((d['delivery_areas'] as List?) ?? const []).join(', ');
    });
  }

  Future<bool> _try(Future<void> Function() f) async {
    try {
      await f();
      return true;
    } on ApiError catch (e) {
      if (mounted) setState(() => _error = e.message);
    } on Offline {
      if (mounted) setState(() => _error = 'You’re offline. Try again when you’re back online.');
    }
    return false;
  }

  /// One step at a time, said when done.
  Future<bool> _act(Future<void> Function() f, [String? done]) async {
    if (_busy) return false;
    setState(() {
      _busy = true;
      _error = null;
    });
    final ok = await _try(f);
    if (!mounted) return ok;
    setState(() => _busy = false);
    if (ok && done != null) showTkToast(context, done);
    return ok;
  }

  /// What a change answers: the profile as it is now (only its photos and
  /// logo, the words being typed are left as they are).
  void _apply(Object? r) {
    if (r is Map<String, dynamic> && mounted) setState(() => _data = r);
  }

  Map<String, dynamic> get _words => {
    'name': _name.text,
    'tagline': _tagline.text,
    'area': _area.text,
    'about': _about.text,
    'delivers': _delivers,
    'vehicle': _delivers ? _vehicle.text : '',
    'delivery_areas': _delivers ? splitAreas(_areas.text) : <String>[],
  };

  bool get _dirty {
    final d = _data;
    if (d == null) return false;
    final w = _words;
    final was = ((d['delivery_areas'] as List?) ?? const []).cast<String>();
    final now = (w['delivery_areas'] as List).cast<String>();
    return w['name'] != d['name'] ||
        w['tagline'] != d['tagline'] ||
        w['area'] != d['area'] ||
        w['about'] != d['about'] ||
        w['delivers'] != (d['delivers'] == true) ||
        w['vehicle'] != (d['vehicle'] ?? '') ||
        now.length != was.length ||
        !List.generate(now.length, (i) => now[i] == was[i]).every((x) => x);
  }

  void _save() => _act(() async {
    final r = await _t.send('PUT', '/v1/business-profile', body: _words);
    if (r is Map<String, dynamic>) _fill(r);
  }, 'Saved');

  void _findable(bool on) => _act(() async {
    await _t.send('PUT', '/v1/link-settings', body: {'findable': on});
    // Saved: as it is now; not read, as it was asked.
    try {
      final s = await _t.send('GET', '/v1/link-settings') as Map<String, dynamic>;
      if (mounted) setState(() => _settings = s);
    } on Object {
      if (mounted) setState(() => _settings = {...?_settings, 'findable': on, 'findable_now': on});
    }
  }, 'Saved');

  /// The phone's photos, or why they could not be opened.
  Future<List<PickedFile>> _pick(int limit) async {
    try {
      return await widget.pickers.photos(limit);
    } on Object {
      if (mounted) setState(() => _error = 'Taskiem could not open your photos. Allow it in your phone’s settings, then try again.');
      return const [];
    }
  }

  /// An upload refused, said plainly; one cut off is checked by reading
  /// the profile again (it may have gone through).
  Future<void> _upload(String path, PickedFile f) async {
    try {
      _apply(await _t.sendFile(path, bytes: f.bytes, type: f.type));
    } on ApiError catch (e) {
      throw ApiError(e.status, {'error': e.status == 413 ? 'That photo is too large. Try a smaller one.' : e.message});
    } on Offline {
      try {
        _apply(await _t.send('GET', '/v1/business-profile'));
      } on Object {
        // Still unreachable: said as offline.
      }
      rethrow;
    }
  }

  Future<void> _logo() async {
    final f = (await _pick(1)).firstOrNull;
    if (f == null || !mounted) return;
    await _act(() => _upload('/v1/business-profile/logo', f), 'Logo saved');
  }

  void _dropLogo() => _act(() async => _apply(await _t.send('DELETE', '/v1/business-profile/logo')), 'Logo removed');

  /// BusinessProfile.tsx addPhotos: as many as there is room for, one
  /// after another.
  Future<void> _addPhotos() async {
    final d = _data;
    if (d == null) return;
    final room = ((d['max_gallery'] as num?)?.toInt() ?? 12) - ((d['gallery'] as List?)?.length ?? 0);
    final fs = await _pick(room);
    if (fs.isEmpty || !mounted) return;
    setState(() => _uploading = fs.length);
    await _act(() async {
      for (final f in fs) {
        await _upload('/v1/business-profile/gallery', f);
        if (mounted) setState(() => _uploading--);
      }
    });
    if (mounted) setState(() => _uploading = 0);
  }

  void _remove(String id) => _act(() async => _apply(await _t.send('DELETE', '/v1/business-profile/gallery/${Uri.encodeComponent(id)}')), 'Photo removed');

  void _move(List<String> ids, int i, int by) {
    final to = i + by;
    if (to < 0 || to >= ids.length) return;
    final next = [...ids];
    final x = next.removeAt(i);
    next.insert(to, x);
    _act(() async => _apply(await _t.send('PUT', '/v1/business-profile/gallery/order', body: {'ids': next})));
  }

  Future<void> _caption(String id, String text) async {
    final was = ((_data?['gallery'] as List?) ?? const []).whereType<Map>().where((g) => g['id'] == id).map((g) => g['caption'] as String? ?? '').firstOrNull;
    // Removed meanwhile, or the same: nothing to save.
    if (was == null || text == was) return;
    await _try(() async {
      await _t.send('PATCH', '/v1/business-profile/gallery/${Uri.encodeComponent(id)}', body: {'caption': text});
      final d = _data;
      if (d == null || !mounted) return;
      setState(() {
        _data = {
          ...d,
          'gallery': [
            for (final g in (d['gallery'] as List))
              if (g is Map && g['id'] == id) {...g.cast<String, dynamic>(), 'caption': text} else g,
          ],
        };
      });
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final pad = MediaQuery.paddingOf(context);
    final d = _data;
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark,
      child: ColoredBox(
        color: t.bg,
        child: Padding(
          padding: EdgeInsets.only(top: pad.top),
          child: ListView(
            padding: EdgeInsets.fromLTRB(16, 12, 16, 28 + pad.bottom),
            children: [
              // `.tk-p-head`: back, and the title.
              Row(
                children: [
                  ChatHeadBar.iconButton(widget.backLabel, TkIcon('back', size: 20, color: t.text), () => Navigator.of(context).maybePop()),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Semantics(
                      header: true,
                      child: Text('Your business in Explore', style: tkText(size: 22, weight: 800, letterSpacing: -0.44, color: t.text)),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              if (_error != null) Padding(padding: const EdgeInsets.only(bottom: 16), child: TkErrorBox(_error!)),
              if (d == null && _error != null)
                Align(
                  alignment: Alignment.centerLeft,
                  child: TkSoftButton(
                    label: 'Try again',
                    onTap: () {
                      setState(() => _error = null);
                      _load();
                    },
                  ),
                )
              else if (d == null)
                Text('Loading your profile…', style: tkHint(t))
              else ...[
                _found(t, dark),
                const SizedBox(height: 16),
                _ProfileCard(
                  transport: _t,
                  name: _name.text.isNotEmpty ? _name.text : (widget.chats.auth.me?.tenantName ?? ''),
                  tagline: _tagline.text,
                  area: _area.text,
                  logo: photoPath(d['logo'] as Map<String, dynamic>?),
                  photos: [
                    for (final g in ((d['gallery'] as List?) ?? const []).whereType<Map<String, dynamic>>())
                      if (photoPath(g['photo'] as Map<String, dynamic>?) != null)
                        (src: photoPath(g['photo'] as Map<String, dynamic>?)!, caption: g['caption'] as String? ?? ''),
                  ],
                ),
                const SizedBox(height: 16),
                _form(t, dark, d),
                const SizedBox(height: 16),
                _photos(t, dark, d),
              ],
            ],
          ),
        ),
      ),
    );
  }

  /// `.tk-p-found`: Be found in Explore, its line, and the switch; in pine
  /// while it is on.
  Widget _found(TkTokens t, bool dark) {
    final on = _settings?['findable_now'] == true;
    final ink = on ? t.tkOnPine : t.text;
    return Semantics(
      container: true,
      label: 'Being found',
      explicitChildNodes: true,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 300),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        decoration: BoxDecoration(color: on ? t.brand : t.panel, borderRadius: BorderRadius.circular(20)),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('Be found in Explore', style: tkText(size: 15, weight: 700, color: ink)),
                  const SizedBox(height: 2),
                  Opacity(
                    opacity: 0.85,
                    child: Text(_settings == null ? 'Checking…' : foundLine(_settings!), style: tkText(size: 13, color: ink)),
                  ),
                ],
              ),
            ),
            const SizedBox(width: 14),
            TkSwitch(label: 'Be found in Explore', on: on, onPine: on, onChanged: !_manage || _busy || _settings == null ? null : _findable),
          ],
        ),
      ),
    );
  }

  Widget _card({required Widget child, EdgeInsets padding = const EdgeInsets.all(16)}) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    // `.tk-p-card`: the panel, 24 px round, its soft shadow (a line in dark).
    return Container(
      padding: padding,
      decoration: BoxDecoration(
        color: t.panel,
        borderRadius: BorderRadius.circular(24),
        border: dark ? Border.all(color: t.border) : null,
        boxShadow: dark
            ? null
            : const [
                BoxShadow(color: Color(0x0F0E1A14), offset: Offset(0, 1), blurRadius: 2),
                BoxShadow(color: Color(0x400E1A14), offset: Offset(0, 12), blurRadius: 28, spreadRadius: -18),
              ],
      ),
      child: child,
    );
  }

  Widget _form(TkTokens t, bool dark, Map<String, dynamic> d) {
    final logo = photoPath(d['logo'] as Map<String, dynamic>?);
    final letter = profileLetter(_name.text);
    Widget field(String label, TextEditingController c, {int? max, String? hint, int lines = 1, bool count = false}) =>
        _Field(label: label, controller: c, max: max, hint: hint, lines: lines, count: count, enabled: _manage);
    return _card(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // `.tk-p-logo-row`: the logo (pressed to change it), what it is.
          Row(
            children: [
              TapArea(
                child: Semantics(
                  button: _manage,
                  // A letter, not to be changed: nothing to say (`aria-hidden`).
                  label: _manage ? (logo != null ? 'Change logo' : 'Add a logo') : (logo != null ? 'Your logo' : null),
                  excludeSemantics: true,
                  child: GestureDetector(
                    onTap: _manage && !_busy ? _logo : null,
                    child: SizedBox(
                      width: 64,
                      height: 64,
                      child: Stack(
                        clipBehavior: Clip.none,
                        children: [
                          ClipOval(
                            child: logo != null
                                ? ApiImage(transport: _t, path: logo, width: 64, height: 64)
                                : Container(
                                    width: 64,
                                    height: 64,
                                    color: t.tkLimeSoft,
                                    alignment: Alignment.center,
                                    child: Text(letter, style: tkText(size: 20, weight: 800, color: dark ? t.brandDark : t.brand)),
                                  ),
                          ),
                          if (_manage)
                            Positioned(
                              right: -2,
                              bottom: -2,
                              child: Container(
                                width: 26,
                                height: 26,
                                decoration: BoxDecoration(
                                  color: t.brand,
                                  shape: BoxShape.circle,
                                  border: Border.all(color: t.panel, width: 2),
                                ),
                                alignment: Alignment.center,
                                child: TkIcon(logo != null ? 'pencil' : 'plus', size: 12, color: t.brandDark),
                              ),
                            ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Logo', style: tkText(size: 15, weight: 700, color: t.text)),
                    Text(logo != null ? 'Shown on your card and in chats' : 'A letter shows until you add one', style: tkText(size: 13, color: t.muted)),
                    if (_manage && logo != null) TkLink(label: 'Remove logo', onTap: _busy ? null : _dropLogo),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          field('Name', _name, max: maxName, hint: 'Your storefront’s name, or your business’s'),
          const SizedBox(height: 12),
          field('One line about you', _tagline, max: maxTagline, hint: 'Cement and sand, delivered', count: true),
          const SizedBox(height: 12),
          field('Area', _area, max: maxArea, hint: 'Where you are, such as Ikeja or Wuse 2'),
          const SizedBox(height: 12),
          field('About', _about, max: maxAbout, lines: 3, count: true),
          const SizedBox(height: 12),
          // A rider: a business whose service is delivery.
          TkChoiceRow(
            radio: false,
            control: TkCheckbox(checked: _delivers),
            label: Text('I deliver for other businesses', style: tkText(size: 13, weight: 600, color: t.text)),
            onTap: _manage ? () => setState(() => _delivers = !_delivers) : null,
          ),
          if (_delivers) ...[
            field('What you deliver with', _vehicle, max: maxVehicle, hint: 'Bike, van, on foot'),
            const SizedBox(height: 12),
            field('Areas you deliver to', _areas, hint: 'Lekki, Ajah, Victoria Island'),
            const SizedBox(height: 12),
          ],
          if (_manage)
            Align(
              alignment: Alignment.centerLeft,
              child: Opacity(
                opacity: _busy || !_dirty ? 0.45 : 1,
                child: _PrimaryButton(label: 'Save', onTap: _busy || !_dirty ? null : _save),
              ),
            ),
        ],
      ),
    );
  }

  Widget _photos(TkTokens t, bool dark, Map<String, dynamic> d) {
    final gallery = ((d['gallery'] as List?) ?? const []).whereType<Map<String, dynamic>>().toList();
    final max = (d['max_gallery'] as num?)?.toInt() ?? 12;
    final ids = [for (final g in gallery) g['id'] as String];
    return _card(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 4, 4, 12),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.baseline,
              textBaseline: TextBaseline.alphabetic,
              children: [
                Expanded(
                  child: Semantics(
                    container: true,
                    header: true,
                    child: Text('Photos', style: tkText(size: 17, weight: 800, color: t.text)),
                  ),
                ),
                Semantics(
                  container: true,
                  liveRegion: true,
                  label: '${gallery.length} of $max photos',
                  excludeSemantics: true,
                  child: Text('${gallery.length} of $max', style: tkText(size: 13, weight: 600, color: t.muted)),
                ),
              ],
            ),
          ),
          LayoutBuilder(
            builder: (context, box) {
              final w = (box.maxWidth - 12) / 2;
              return Wrap(
                spacing: 12,
                runSpacing: 12,
                children: [
                  for (var i = 0; i < gallery.length; i++)
                    SizedBox(
                      // Its state (the caption typed) follows the photo, not the place.
                      key: ValueKey(ids[i]),
                      width: w,
                      child: _PhotoTile(
                        transport: _t,
                        g: gallery[i],
                        index: i,
                        count: gallery.length,
                        manage: _manage,
                        busy: _busy,
                        onRemove: () => _remove(ids[i]),
                        onMove: (by) => _move(ids, i, by),
                        onCaption: (text) => _caption(ids[i], text),
                      ),
                    ),
                  if (_uploading > 0)
                    SizedBox(
                      width: w,
                      child: Container(
                        height: 120,
                        decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(16)),
                        alignment: Alignment.center,
                        child: Semantics(
                          liveRegion: true,
                          child: Text('Adding $_uploading ${_uploading == 1 ? 'photo' : 'photos'}…', style: tkText(size: 13, color: t.muted)),
                        ),
                      ),
                    ),
                  if (_manage)
                    SizedBox(
                      width: w,
                      child: Opacity(
                        opacity: _busy || _uploading > 0 || gallery.length >= max ? 0.55 : 1,
                        child: Semantics(
                          container: true,
                          button: true,
                          label: 'Add a photo',
                          onTap: _busy || _uploading > 0 || gallery.length >= max ? null : _addPhotos,
                          excludeSemantics: true,
                          child: TkPress(
                            onTap: _busy || _uploading > 0 || gallery.length >= max ? null : _addPhotos,
                            child: CustomPaint(
                              painter: _Dashed(t.borderStrong),
                              child: Container(
                                height: 120,
                                decoration: BoxDecoration(color: t.tkTile, borderRadius: BorderRadius.circular(16)),
                                child: Column(
                                  mainAxisAlignment: MainAxisAlignment.center,
                                  children: [
                                    TkIcon('plus', size: 24, color: dark ? t.brandDark : t.brand),
                                    const SizedBox(height: 6),
                                    Text('Add a photo', style: tkText(size: 14, weight: 700, color: dark ? t.brandDark : t.brand)),
                                  ],
                                ),
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                ],
              );
            },
          ),
          if (gallery.isEmpty && _uploading == 0)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Text(
                'Show your shop, your products and your team. Other businesses see a letter until you add one.',
                style: tkText(size: 13, color: t.muted),
              ),
            ),
          if (_manage && gallery.length >= max)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Text('That is the most photos.', style: tkText(size: 13, color: t.muted)),
            ),
        ],
      ),
    );
  }
}

/// `.tk-p-field`: the label (and its count) over a tall soft box.
class _Field extends StatelessWidget {
  const _Field({required this.label, required this.controller, this.max, this.hint, this.lines = 1, this.count = false, this.enabled = true});
  final String label;
  final TextEditingController controller;
  final int? max;
  final String? hint;
  final int lines;
  final bool count;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapField(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(label, style: tkText(size: 13, weight: 600, color: t.muted)),
              ),
              if (count && max != null) Text('${controller.text.runes.length} / $max', style: tkText(size: 13, weight: 500, color: t.muted)),
            ],
          ),
          const SizedBox(height: 6),
          Container(
            constraints: const BoxConstraints(minHeight: 50),
            padding: EdgeInsets.symmetric(horizontal: 14, vertical: lines > 1 ? 12 : 0),
            alignment: Alignment.centerLeft,
            decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(14)),
            child: Material(
              type: MaterialType.transparency,
              child: TextField(
                controller: controller,
                enabled: enabled,
                minLines: lines,
                maxLines: lines == 1 ? 1 : null,
                cursorColor: t.text,
                showCursor: !TkField.debugHideCaret,
                inputFormatters: [if (max != null) RuneLimit(max!)],
                style: tkText(size: 15, weight: 500, lineHeight: lines > 1 ? 1.5 : 1.3, color: t.text),
                decoration: InputDecoration.collapsed(
                  hintText: hint,
                  hintStyle: tkText(size: 15, weight: 500, color: t.muted),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// `.tk-btn.primary`: a pine pill, white words.
class _PrimaryButton extends StatelessWidget {
  const _PrimaryButton({required this.label, this.onTap});
  final String label;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(
      child: Semantics(
        button: true,
        enabled: onTap != null,
        label: label,
        onTap: onTap,
        excludeSemantics: true,
        child: TkPress(
          onTap: onTap,
          child: Container(
            constraints: const BoxConstraints(minHeight: 44),
            padding: const EdgeInsets.symmetric(horizontal: 18),
            alignment: Alignment.center,
            decoration: BoxDecoration(color: t.brand, borderRadius: BorderRadius.circular(999)),
            child: Text(label, style: tkText(size: 15, weight: 700, color: const Color(0xFFFFFFFF))),
          ),
        ),
      ),
    );
  }
}

/// `.tk-p-card` with the card as another business sees it (design 11): the
/// logo, the name and line, Connect as they would see it, the photos.
class _ProfileCard extends StatelessWidget {
  const _ProfileCard({required this.transport, required this.name, required this.tagline, required this.area, this.logo, required this.photos});
  final Transport transport;
  final String name;
  final String tagline;
  final String area;
  final String? logo;
  final List<({String src, String caption})> photos;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final line = [tagline, area].where((s) => s.isNotEmpty).join(' · ');
    final more = photos.length - 2;
    Widget tile(int i, double radius, {Widget? over}) => ClipRRect(
      borderRadius: BorderRadius.circular(radius),
      child: Container(
        color: t.tkSoft,
        child: Stack(
          fit: StackFit.expand,
          children: [
            if (photos.length > i) ApiImage(transport: transport, path: photos[i].src),
            ?over,
          ],
        ),
      ),
    );
    return Semantics(
      container: true,
      label: 'How your card looks',
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: t.panel,
          borderRadius: BorderRadius.circular(24),
          border: dark ? Border.all(color: t.border) : null,
          boxShadow: dark
              ? null
              : const [
                  BoxShadow(color: Color(0x0F0E1A14), offset: Offset(0, 1), blurRadius: 2),
                  BoxShadow(color: Color(0x400E1A14), offset: Offset(0, 12), blurRadius: 28, spreadRadius: -18),
                ],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(4, 2, 4, 10),
              child: Text('HOW OTHER BUSINESSES SEE YOU', style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.muted)),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(4, 0, 4, 12),
              child: Row(
                children: [
                  ClipOval(
                    child: logo != null
                        ? ApiImage(transport: transport, path: logo!, width: 48, height: 48)
                        : Container(
                            width: 48,
                            height: 48,
                            color: t.tkLimeSoft,
                            alignment: Alignment.center,
                            child: Text(profileLetter(name), style: tkText(size: 18, weight: 800, color: dark ? t.brandDark : t.brand)),
                          ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(name.isEmpty ? 'Your business' : name, style: tkText(size: 16, weight: 700, color: t.text)),
                        Text(line.isEmpty ? 'A line about what you do' : line, style: tkText(size: 13, color: t.muted)),
                      ],
                    ),
                  ),
                  const SizedBox(width: 12),
                  ExcludeSemantics(
                    child: Container(
                      constraints: const BoxConstraints(minHeight: 36),
                      padding: const EdgeInsets.symmetric(horizontal: 14),
                      alignment: Alignment.center,
                      decoration: BoxDecoration(color: dark ? t.brandDark : t.brand, borderRadius: BorderRadius.circular(999)),
                      child: Text('Connect', style: tkText(size: 13, weight: 700, color: dark ? t.brandDarkText : t.tkOnPine)),
                    ),
                  ),
                ],
              ),
            ),
            ExcludeSemantics(
              child: SizedBox(
                height: 150,
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Expanded(
                      flex: 2,
                      child: tile(
                        0,
                        16,
                        over: Positioned(
                          left: 10,
                          bottom: 10,
                          child: _Caption(photos.isNotEmpty ? (photos[0].caption.isEmpty ? 'Your first photo' : photos[0].caption) : 'Add your first photo'),
                        ),
                      ),
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          Expanded(child: tile(1, 14)),
                          const SizedBox(height: 6),
                          Expanded(
                            child: tile(
                              2,
                              14,
                              over: more > 0
                                  ? Center(
                                      child: Container(
                                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                                        decoration: BoxDecoration(color: t.panel.withValues(alpha: 0.8), borderRadius: BorderRadius.circular(8)),
                                        child: Text('+$more', style: tkText(size: 13, weight: 700, color: t.text)),
                                      ),
                                    )
                                  : null,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// `.tk-x-cap` on a photo: its words on a frosted chip.
class _Caption extends StatelessWidget {
  const _Caption(this.text);
  final String text;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 200),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 5),
        decoration: BoxDecoration(color: t.tkPhotoChip, borderRadius: BorderRadius.circular(9)),
        child: Text(
          text,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: tkText(size: 12, weight: 600, color: t.tkPhotoInk),
        ),
      ),
    );
  }
}

/// `.tk-p-photo`: the picture (First, Not accepted, remove, move), and its
/// caption under it, saved when the box is left.
class _PhotoTile extends StatefulWidget {
  const _PhotoTile({
    required this.transport,
    required this.g,
    required this.index,
    required this.count,
    required this.manage,
    required this.busy,
    required this.onRemove,
    required this.onMove,
    required this.onCaption,
  });
  final Transport transport;
  final Map<String, dynamic> g;
  final int index;
  final int count;
  final bool manage;
  final bool busy;
  final VoidCallback onRemove;
  final ValueChanged<int> onMove;
  final ValueChanged<String> onCaption;

  @override
  State<_PhotoTile> createState() => _PhotoTileState();
}

class _PhotoTileState extends State<_PhotoTile> {
  late final _text = TextEditingController(text: widget.g['caption'] as String? ?? '');
  final _focus = FocusNode();
  Timer? _wait;

  // Saved as the web does on leaving the box, and on a phone, where the box
  // is not left by a tap elsewhere, after a pause in typing too, and when
  // the screen goes.
  void _save() {
    _wait?.cancel();
    _wait = null;
    widget.onCaption(_text.text.trim());
  }

  @override
  void initState() {
    super.initState();
    _focus.addListener(() {
      if (!_focus.hasFocus) _save();
    });
    _text.addListener(() {
      if (!_focus.hasFocus) return;
      _wait?.cancel();
      _wait = Timer(const Duration(milliseconds: 1200), _save);
    });
  }

  @override
  void didUpdateWidget(_PhotoTile oldWidget) {
    super.didUpdateWidget(oldWidget);
    // Its caption changed elsewhere, while not being typed: the box says so.
    final now = widget.g['caption'] as String? ?? '';
    if (!_focus.hasFocus && now != (oldWidget.g['caption'] as String? ?? '') && now != _text.text) _text.text = now;
  }

  @override
  void dispose() {
    if (_wait != null) _save();
    _text.dispose();
    _focus.dispose();
    super.dispose();
  }

  Widget _round(String icon, String label, Color bg, Color ink, VoidCallback? onTap) => TapArea(
    child: Semantics(
      button: true,
      enabled: onTap != null,
      label: label,
      onTap: onTap,
      excludeSemantics: true,
      child: GestureDetector(
        onTap: onTap,
        child: Opacity(
          opacity: onTap == null ? 0.45 : 1,
          child: Container(
            width: 32,
            height: 32,
            decoration: BoxDecoration(color: bg, shape: BoxShape.circle),
            alignment: Alignment.center,
            child: TkIcon(icon, size: 14, color: ink),
          ),
        ),
      ),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final i = widget.index;
    final n = i + 1;
    final path = photoPath(widget.g['photo'] as Map<String, dynamic>?);
    final refused = (widget.g['photo'] as Map?)?['moderation'] == 'rejected';
    final caption = widget.g['caption'] as String? ?? '';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SizedBox(
          height: 120,
          child: ClipRRect(
            borderRadius: BorderRadius.circular(16),
            child: Stack(
              fit: StackFit.expand,
              children: [
                Semantics(
                  image: true,
                  label: caption.isEmpty ? 'Photo $n' : caption,
                  child: path == null ? ColoredBox(color: t.tkSoft) : ApiImage(transport: widget.transport, path: path),
                ),
                if (i == 0)
                  Positioned(
                    left: 8,
                    top: 8,
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                      decoration: BoxDecoration(color: t.brandDark, borderRadius: BorderRadius.circular(999)),
                      child: Text('First', style: tkText(size: 11, weight: 700, color: t.brandDarkText)),
                    ),
                  ),
                if (refused)
                  Positioned(
                    left: 6,
                    right: 6,
                    top: 40,
                    child: Semantics(
                      liveRegion: true,
                      child: Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                        decoration: BoxDecoration(color: t.tkPhotoNotice, borderRadius: BorderRadius.circular(8)),
                        child: Text('Not accepted for a public page. Remove it.', style: tkText(size: 12, weight: 600, color: t.tkPhotoOnDark)),
                      ),
                    ),
                  ),
                if (widget.manage) ...[
                  Positioned(right: 6, top: 6, child: _round('close', 'Remove photo $n', t.tkPhotoDark, t.tkPhotoOnDark, widget.busy ? null : widget.onRemove)),
                  Positioned(
                    right: 6,
                    bottom: 6,
                    child: Row(
                      children: [
                        _round('back', 'Move photo $n earlier', t.tkPhotoBtn, t.tkPhotoInk, widget.busy || i == 0 ? null : () => widget.onMove(-1)),
                        const SizedBox(width: 4),
                        _round(
                          'chevron',
                          'Move photo $n later',
                          t.tkPhotoBtn,
                          t.tkPhotoInk,
                          widget.busy || i == widget.count - 1 ? null : () => widget.onMove(1),
                        ),
                      ],
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
        const SizedBox(height: 6),
        if (widget.manage)
          TapField(
            child: Semantics(
              label: 'Caption for photo $n',
              child: Material(
                type: MaterialType.transparency,
                child: TextField(
                  controller: _text,
                  focusNode: _focus,
                  inputFormatters: [RuneLimit(maxCaption)],
                  textInputAction: TextInputAction.done,
                  onSubmitted: (_) => _save(),
                  cursorColor: t.text,
                  showCursor: !TkField.debugHideCaret,
                  style: tkText(size: 13, lineHeight: 1.35, color: t.text),
                  decoration: InputDecoration.collapsed(
                    hintText: 'Add a caption',
                    hintStyle: tkText(size: 13, color: t.muted),
                  ),
                ),
              ),
            ),
          )
        else if (caption.isNotEmpty)
          Text(caption, style: tkText(size: 13, lineHeight: 1.35, color: t.text)),
      ],
    );
  }
}

/// `border: 1.5px dashed var(--border-strong)`, round as its box.
class _Dashed extends CustomPainter {
  _Dashed(this.color);
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final p = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5;
    final path = Path()..addRRect(RRect.fromRectAndRadius(Offset.zero & size, const Radius.circular(16)).deflate(0.75));
    for (final m in path.computeMetrics()) {
      for (var d = 0.0; d < m.length; d += 9) {
        canvas.drawPath(m.extractPath(d, d + 5), p);
      }
    }
  }

  @override
  bool shouldRepaint(_Dashed old) => old.color != color;
}
