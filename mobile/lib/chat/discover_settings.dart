// Settings > Discover, for a business (docs/spec/taskiem-for-everyone.md
// §10; "As built: stage 3, Discover"): **Found by the public in the
// Taskiem app**, apart from Explore's switch; what a listing still needs
// (a category, an area, a photo and hours); its own category, area and
// hours, each winning over the profile's and the shop's; why a listing
// that is on is hidden (taken out by the moderators, reported, the page
// suspended); why it shows without Chat; and the listing as customers see
// it (`GET`/`PUT /v1/settings/discover`). Its pin on the map is not here
// yet. Without `storefront.manage` it is shown, not changed.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show TkSwitch, TkSwitchRow;
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading, TkPill;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../screens/chat_link.dart' show hoursLines;
import '../screens/settings_more.dart' show SettingsCard, SettingsNote, SettingsPage;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'door.dart' show BusinessFace;
import 'explore.dart' show exploreCategoryNames;
import 'my_chat.dart' show sayFailure;
import 'words.dart' show CardTone;

/// Why a listing that is on is not shown.
String? discoverHiddenWords(String? hidden) => switch (hidden) {
  'removed' => 'Taskiem’s moderators took your listing out of Discover.',
  'reported' => 'Your listing is hidden while Taskiem’s moderators look at reports about it.',
  'suspended' => 'Your shop page is suspended, so your listing is hidden.',
  _ => null,
};

/// Why a listing shows without its Chat button.
String? discoverChatOffWords(String? off) => switch (off) {
  'orders_off' => 'Your Orders area is off, so your listing shows without Chat.',
  'chats_off' => 'You aren’t taking chats in the app, so your listing shows without Chat.',
  _ => null,
};

/// The Settings row's line: where the listing stands.
String discoverLine(DiscoverSettings s) {
  if (s.workspace == true) return 'Not for a workflows workspace';
  if (!s.listed) return 'Off';
  if (s.shown) return 'Customers can find you';
  if (s.hidden != null) return 'Hidden';
  return 'Not shown yet';
}

class DiscoverSettingsPage extends StatefulWidget {
  const DiscoverSettingsPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<DiscoverSettingsPage> createState() => _DiscoverSettingsPageState();
}

class _DiscoverSettingsPageState extends State<DiscoverSettingsPage> {
  DiscoverSettings? _s;
  String? _error;
  bool _busy = false;
  final _area = TextEditingController();

  bool get _can => widget.auth.can('storefront.manage');

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _area.dispose();
    super.dispose();
  }

  void _take(DiscoverSettings s) {
    _s = s;
    _area.text = s.area ?? '';
    _error = null;
  }

  Future<void> _load() async {
    try {
      final s = await widget.auth.api.getDiscoverSettings();
      if (mounted) setState(() => _take(s));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<bool> _put(PutDiscoverSettingsRequest body) async {
    if (_busy) return false;
    setState(() => (_busy = true, _error = null));
    try {
      final s = await widget.auth.api.putDiscoverSettings(body: body);
      if (mounted) {
        setState(() {
          _take(s);
          _busy = false;
        });
      }
      return true;
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _busy = false));
      return false;
    }
  }

  Future<void> _category() async {
    final s = _s!;
    final pick = await showTkModalChoices(
      context,
      title: 'Your category in Discover',
      value: s.category ?? s.listing.category,
      choices: [for (final e in exploreCategoryNames.entries) (e.key, e.value)],
    );
    if (pick != null && mounted) await _put(PutDiscoverSettingsRequest(category: pick));
  }

  Future<void> _saveArea() async {
    if (await _put(PutDiscoverSettingsRequest(area: _area.text.trim())) && mounted) showTkToast(context, 'Area saved');
  }

  Future<void> _hours() async {
    final s = _s!;
    final hours = await showTkSheet<Map<String, dynamic>>(context, (_) => DiscoverHoursSheet(hours: s.hours ?? s.listing.hours));
    if (hours != null && mounted) await _put(PutDiscoverSettingsRequest(hours: hours));
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final s = _s;
    return SettingsPage(
      title: 'Discover',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (s == null && _error == null) const TkLoading(label: 'Loading your listing…'),
        if (s != null) ...[
          _Preview(s: s),
          SettingsCard(
            title: 'Discover',
            children: [
              TkSwitchRow(
                label: 'Found by the public in the Taskiem app',
                hint: s.workspace == true
                    ? 'A workflows workspace isn’t listed in Discover.'
                    : 'Customers find you in Taskiem’s Discover and chat with you. Apart from Explore, where businesses find you.',
                on: s.listed,
                onChanged: !_can || _busy || s.workspace == true ? null : (v) => _put(PutDiscoverSettingsRequest(listed: v)),
              ),
              if (s.chosen != null && s.workspace != true && _can)
                Align(
                  alignment: Alignment.centerLeft,
                  child: TkLinkButton(
                    label: s.default_ ? 'Use the default: on while your shop is published' : 'Use the default: off without a published shop',
                    onPressed: _busy ? null : () => _put(const PutDiscoverSettingsRequest(default_: true)),
                  ),
                ),
            ],
          ),
          if (s.listed && (s.missingLine ?? '').isNotEmpty)
            _Note(words: s.missingLine!, colour: look.orangeInk, icon: 'b-alert'),
          if (discoverHiddenWords(s.hidden) != null)
            _Note(words: [discoverHiddenWords(s.hidden)!, if ((s.removedNote ?? '').isNotEmpty) '“${s.removedNote}”'].join(' '), colour: look.redInk, icon: 'b-x-ring'),
          if (discoverChatOffWords(s.chatOff) != null) _Note(words: discoverChatOffWords(s.chatOff)!, colour: t.muted, icon: 'b-chat'),
          if (s.workspace != true)
            TkSection(
              title: 'Your listing',
              inset: 56,
              children: [
                TkListRow(
                  title: 'Category',
                  sub: _categoryWords(s),
                  icon: 'b-bag',
                  square: s.missing.contains('category') ? look.orange : look.teal,
                  onTap: _can && !_busy ? _category : null,
                ),
                TkListRow(
                  title: 'Opening hours',
                  sub: _hoursWords(s),
                  icon: 'b-clock',
                  square: s.missing.contains('hours') ? look.orange : look.blue,
                  onTap: _can && !_busy ? _hours : null,
                ),
                if (s.hours != null && _can)
                  TkListRow(title: 'Use your shop’s hours', icon: 'b-store', square: look.violet, onTap: _busy ? null : () => _put(const PutDiscoverSettingsRequest(clearHours: true))),
              ],
            ),
          if (s.workspace != true)
            SettingsCard(
              title: 'Area',
              children: [
                TkKitField(
                  label: 'Where customers find you',
                  hint: s.area == null ? 'Empty: your profile’s or your shop’s area${(s.listing.area ?? '').isEmpty ? '' : ' (${s.listing.area})'}.' : null,
                  child: TkTextBox(controller: _area, max: 80, enabled: _can),
                ),
                if (_can) Align(alignment: Alignment.centerLeft, child: TkSmallButton(label: _busy ? 'Saving…' : 'Save', primary: true, onPressed: _busy ? null : _saveArea)),
              ],
            ),
          if (s.missing.contains('photo')) const SettingsNote('Your photo comes from your profile or your shop. Add a logo there.'),
          if (!_can) const SettingsNote('Only those who manage your shop can change this.'),
        ],
      ],
    );
  }

  String _categoryWords(DiscoverSettings s) {
    final key = s.category ?? s.listing.category;
    if (key == null) return 'Not chosen yet';
    final name = exploreCategoryNames[key] ?? key;
    return s.category == null ? '$name · from your profile' : name;
  }

  String _hoursWords(DiscoverSettings s) {
    final lines = hoursLines(s.hours ?? s.listing.hours);
    final open = lines.where((l) => l.$2 != 'Closed').toList();
    if (open.isEmpty) return 'Not set yet';
    final first = '${open.first.$1} ${open.first.$2}';
    final from = s.hours == null ? ' · from your shop' : '';
    return open.length == 1 ? '$first$from' : '$first and ${open.length - 1} more days$from';
  }
}

/// A line of what to know, with its icon, on a card.
class _Note extends StatelessWidget {
  const _Note({required this.words, required this.colour, required this.icon});
  final String words;
  final Color colour;
  final String icon;

  @override
  Widget build(BuildContext context) => Semantics(
    liveRegion: true,
    child: TkPanel(
      padding: const EdgeInsets.all(14),
      children: [
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            ExcludeSemantics(child: TkSquare(icon, color: colour, size: 30)),
            const SizedBox(width: 12),
            Expanded(child: Text(words, style: tkText(size: 14, lineHeight: 1.4, color: TkTheme.tokens(context).text))),
          ],
        ),
      ],
    ),
  );
}

/// The listing as customers see it in Discover.
class _Preview extends StatelessWidget {
  const _Preview({required this.s});
  final DiscoverSettings s;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final l = s.listing;
    final category = l.category == null ? null : exploreCategoryNames[l.category] ?? l.category;
    return TkTopCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow('YOUR LISTING', color: look.limeInk, icon: 'b-search', trailing: s.shown ? 'Shown' : (s.listed ? 'Not shown' : 'Off')),
          const SizedBox(height: 12),
          Row(
            children: [
              BusinessFace(name: l.name, urls: l.photo?.urls, size: 52),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(l.name, style: tkDisplay(size: 19, letterSpacing: -0.3, color: t.text)),
                    if ((l.tagline ?? '').isNotEmpty) Text(l.tagline!, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 13, color: t.muted)),
                    Text([?category, if ((l.area ?? '').isNotEmpty) l.area!].join(' · '), style: tkText(size: 13, color: t.muted)),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              if (l.canChat) const TkPill('Chat', tone: CardTone.lime, small: true),
              if (l.hasShop) const TkPill('Their shop', small: true),
              if (l.hasBook) const TkPill('Book', small: true),
              if (l.photos.isNotEmpty) TkPill(l.photos.length == 1 ? '1 photo' : '${l.photos.length} photos', small: true),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            s.shown
                ? 'Customers can find you in Discover.'
                : !s.listed
                ? 'You’re not in Discover. Turn it on below to be found.'
                : 'Customers can’t find you in Discover yet.',
            style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
          ),
        ],
      ),
    );
  }
}

/// The listing's own opening hours: a line a day, open or closed, from
/// and to (HH:MM).
class DiscoverHoursSheet extends StatefulWidget {
  const DiscoverHoursSheet({super.key, required this.hours});
  final Map<String, dynamic>? hours;

  @override
  State<DiscoverHoursSheet> createState() => _DiscoverHoursSheetState();
}

class _DiscoverHoursSheetState extends State<DiscoverHoursSheet> {
  static const _days = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'];
  late final List<bool> _open;
  late final List<TextEditingController> _from;
  late final List<TextEditingController> _to;
  String? _error;

  @override
  void initState() {
    super.initState();
    final days = widget.hours?['days'];
    Map<String, dynamic>? first(int i) {
      if (days is! List || days.length != 7 || days[i] is! List || (days[i] as List).isEmpty) return null;
      final s = (days[i] as List).first;
      return s is Map<String, dynamic> ? s : null;
    }

    _open = [for (var i = 0; i < 7; i++) first(i) != null];
    _from = [for (var i = 0; i < 7; i++) TextEditingController(text: first(i)?['open'] as String? ?? '09:00')];
    _to = [for (var i = 0; i < 7; i++) TextEditingController(text: first(i)?['close'] as String? ?? '18:00')];
  }

  @override
  void dispose() {
    for (final c in [..._from, ..._to]) {
      c.dispose();
    }
    super.dispose();
  }

  /// A day's openings as they came, when its first is unchanged.
  List<Object?>? _kept(int i) {
    final days = widget.hours?['days'];
    if (days is! List || days.length != 7 || days[i] is! List || (days[i] as List).isEmpty) return null;
    final first = (days[i] as List).first;
    if (first is! Map || first['open'] != _from[i].text.trim() || first['close'] != _to[i].text.trim()) return null;
    return days[i] as List<Object?>;
  }

  static final _time = RegExp(r'^([01]\d|2[0-3]):[0-5]\d$');

  void _save() {
    for (var i = 0; i < 7; i++) {
      if (_open[i] && (!_time.hasMatch(_from[i].text.trim()) || !_time.hasMatch(_to[i].text.trim()) || _from[i].text.trim() == _to[i].text.trim())) {
        return setState(() => _error = 'Opening hours are times like 08:00 to 18:30 (${_days[i]}).');
      }
    }
    if (!_open.contains(true)) return setState(() => _error = 'Open at least one day.');
    Navigator.of(context).pop(<String, dynamic>{
      'days': [
        for (var i = 0; i < 7; i++)
          if (!_open[i])
            <Object>[]
          // A day left as it was keeps all its openings.
          else if (_kept(i) != null)
            _kept(i)!
          else
            [{'open': _from[i].text.trim(), 'close': _to[i].text.trim()}],
      ],
      if (widget.hours?['note'] is String && (widget.hours!['note'] as String).isNotEmpty) 'note': widget.hours!['note'],
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TkFormSheet(
      title: 'Opening hours',
      crumb: 'Discover',
      action: 'Save',
      onAction: _save,
      children: [
        TkFormGroup(
          children: [
            for (var i = 0; i < 7; i++)
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Row(
                    children: [
                      Expanded(child: Text(_days[i], style: tkText(size: 15, weight: 600, color: t.text))),
                      Text(_open[i] ? 'Open' : 'Closed', style: tkText(size: 13, color: t.muted)),
                      const SizedBox(width: 8),
                      TkSwitch(label: '${_days[i]} open', on: _open[i], onChanged: (v) => setState(() => _open[i] = v)),
                    ],
                  ),
                  if (_open[i])
                    Padding(
                      padding: const EdgeInsets.only(top: 6),
                      child: Row(
                        children: [
                          Expanded(child: TkKitField(label: 'Opens', child: TkTextBox(controller: _from[i], height: 44, size: 16, hint: '09:00', max: 5))),
                          const SizedBox(width: 10),
                          Expanded(child: TkKitField(label: 'Closes', child: TkTextBox(controller: _to[i], height: 44, size: 16, hint: '18:00', max: 5))),
                        ],
                      ),
                    ),
                ],
              ),
          ],
        ),
        Text('A day you leave as it is keeps all its openings.', style: tkText(size: 13, color: t.muted)),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}
