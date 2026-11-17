// Discover on You (docs/spec/taskiem-for-everyone.md §10; "As built: stage
// 3, Discover"): businesses a person can find, apart from Explore (which is
// businesses finding businesses). A search by name or what they want, an
// area, Explore's categories as chips (`GET /v1/discover/categories`), and
// the results (`POST /v1/discover/search`, 20 a page by `offset`): each with
// its photo, name, category, area, open now and **Chat**. A business's
// profile (`GET /v1/discover/{business}`): its photos, hours, address and
// map link, what it says of itself, **Their shop**, **Book** and **Chat**
// (the person's chat, or one started with `from: "discover"`, then the
// tool), and **Report this business**. Searching too fast (429) is said in
// the server's words. Near me is not here yet: the app has no way to ask
// for the phone's place.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../screens/chat_link.dart' show hoursLines;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart' show CardButton, CardPill;
import 'door.dart' show BusinessFace, photoUrl;
import 'my_chat.dart';
import 'words.dart' show CardTone;

/// The reasons a business is reported for in Discover, in the web's words
/// (web/src/lib/trust.ts REPORT_REASON).
const discoverReportReasons = [
  ('scam', 'Looks like a scam'),
  ('impersonation', 'Pretends to be someone else'),
  ('prohibited', 'Sells something not allowed'),
  ('offensive', 'Offensive'),
  ('other', 'Something else'),
];

/// The Discover tab on You.
class DiscoverTab extends StatefulWidget {
  const DiscoverTab({super.key, required this.auth, required this.onOpenChat});
  final Auth auth;
  final OpenMyChat onOpenChat;

  @override
  State<DiscoverTab> createState() => _DiscoverTabState();
}

class _DiscoverTabState extends State<DiscoverTab> {
  final _q = TextEditingController();
  final _area = TextEditingController();
  List<DiscoverCategoriesResponseCategoriesItem> _categories = const [];
  String? _category;
  List<DiscoverResult>? _results;
  bool _more = false;
  bool _loadingMore = false;

  /// How many results the server gave, the next page's offset.
  int _got = 0;
  String? _error;
  Timer? _wait;

  /// Each search's number: an answer to an older one is let go.
  int _asked = 0;
  String _was = '';

  @override
  void initState() {
    super.initState();
    _q.addListener(_typed);
    _area.addListener(_typed);
    unawaited(_loadCategories());
    unawaited(_search());
  }

  @override
  void dispose() {
    _wait?.cancel();
    _q.dispose();
    _area.dispose();
    super.dispose();
  }

  Future<void> _loadCategories() async {
    try {
      final r = await widget.auth.api.discoverCategories();
      if (mounted) setState(() => _categories = r.categories);
    } on Object {
      // No chips; the search still finds by words and area.
    }
  }

  String get _shape => '${_q.text.trim()}|${_area.text.trim()}|$_category';

  void _typed() {
    if (_shape == _was) return;
    _wait?.cancel();
    _wait = Timer(const Duration(milliseconds: 400), _search);
  }

  DiscoverSearchRequest _request(int offset) {
    final q = _q.text.trim(), area = _area.text.trim();
    return DiscoverSearchRequest(q: q.isEmpty ? null : q, area: area.isEmpty ? null : area, category: _category, offset: offset == 0 ? null : offset);
  }

  Future<void> _search() async {
    _wait?.cancel();
    _was = _shape;
    final n = ++_asked;
    setState(() => _error = null);
    try {
      final r = await widget.auth.api.discoverSearch(body: _request(0));
      if (!mounted || n != _asked) return;
      setState(() => (_results = r.results, _more = r.more, _got = r.results.length));
    } on Object catch (e) {
      if (mounted && n == _asked) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _next() async {
    final have = _results;
    if (have == null || _loadingMore) return;
    final n = _asked;
    setState(() => _loadingMore = true);
    try {
      final r = await widget.auth.api.discoverSearch(body: _request(_got));
      if (!mounted || n != _asked) return;
      final seen = {for (final x in have) x.business};
      setState(() => (_results = [...have, ...r.results.where((x) => !seen.contains(x.business))], _more = r.more, _got = _got + r.results.length));
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
    if (mounted) setState(() => _loadingMore = false);
  }

  void _pick(String? key) {
    setState(() => _category = key);
    unawaited(_search());
  }

  Future<void> _chat(DiscoverResult r) =>
      chatWithBusiness(context, widget.auth, widget.onOpenChat, business: r.business, from: 'discover');

  void _profile(DiscoverResult r) =>
      tkPush<void>(context, DiscoverProfileScreen(auth: widget.auth, business: r.business, name: r.name, onOpenChat: widget.onOpenChat));

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final results = _results;
    return TkScreen(
      title: 'Discover',
      back: false,
      onRefresh: _search,
      under: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkSearchBox(controller: _q, hint: 'Search by name or what you want', margin: const EdgeInsets.fromLTRB(16, 4, 16, 8)),
          TkSearchBox(controller: _area, hint: 'Area (Yaba, Lekki…)', margin: const EdgeInsets.fromLTRB(16, 0, 16, 8)),
          if (_categories.isNotEmpty)
            TkChipRow(
              label: 'Categories',
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
              chips: [for (final c in _categories) (label: c.name, tag: c.key)],
              on: _category,
              onPick: _pick,
            ),
        ],
      ),
      children: [
        if (_error != null) Semantics(liveRegion: true, child: TkErrorBox(_error!)),
        if (results == null && _error == null) const TkSkeleton(rows: 4, label: 'Finding businesses…'),
        if (results != null && results.isEmpty && _error == null)
          const TkPanel(children: [TkEmpty(icon: 'search', title: 'No businesses found', line: 'Try other words, another area or another category.')]),
        if (results != null && results.isNotEmpty)
          TkSection(
            children: [
              for (final r in results)
                TkListRow(
                  key: ValueKey('discover ${r.business}'),
                  title: r.name,
                  sub: [r.categoryName, if (r.area.isNotEmpty) r.area, if (r.km != null) '${r.km!.toStringAsFixed(1)} km'].join(' · '),
                  face: BusinessFace(name: r.name, urls: r.photo, size: 40),
                  status: r.openNow ? 'Open now' : 'Closed',
                  statusAccent: r.openNow ? (fill: look.lime, ink: look.limeInk) : null,
                  semanticsLabel: '${r.name}, ${r.categoryName}${r.area.isEmpty ? '' : ', ${r.area}'}, ${r.openNow ? 'open now' : 'closed now'}',
                  trailing: r.canChat ? TkSmallButton(label: 'Chat', primary: true, semantics: 'Chat with ${r.name}', onPressed: () => _chat(r)) : null,
                  onTap: () => _profile(r),
                ),
            ],
          ),
        if (results != null && _more) TkButton(label: _loadingMore ? 'Loading…' : 'Show more', minHeight: 44, enabled: !_loadingMore, onPressed: _next),
        if (results != null) Text('Businesses choose to be found here.', textAlign: TextAlign.center, style: tkHint(t)),
      ],
    );
  }
}

/// A business's profile in Discover.
class DiscoverProfileScreen extends StatefulWidget {
  const DiscoverProfileScreen({super.key, required this.auth, required this.business, this.name, required this.onOpenChat});
  final Auth auth;
  final String business;

  /// Its name, for the head while the profile is read.
  final String? name;
  final OpenMyChat onOpenChat;

  @override
  State<DiscoverProfileScreen> createState() => _DiscoverProfileScreenState();
}

class _DiscoverProfileScreenState extends State<DiscoverProfileScreen> {
  DiscoverProfile? _p;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final p = await widget.auth.api.discoverProfile(business: widget.business);
      if (mounted) setState(() => (_p = p, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  /// Chat, or the chat then a tool (`shop`, `book`).
  Future<void> _chat({String? tool}) async {
    final p = _p;
    if (p == null || _busy) return;
    setState(() => _busy = true);
    await chatWithBusiness(context, widget.auth, widget.onOpenChat, business: p.business, chat: p.chat, from: 'discover', tool: tool);
    if (!mounted) return;
    setState(() => _busy = false);
    // The chat it has now, for the next press.
    unawaited(_load());
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _p;
    final hours = hoursLines(p?.hours);
    final note = p?.hours['note'];
    final canChat = p != null && (p.canChat || (p.chat ?? '').isNotEmpty);
    final photos = p?.photos.where((x) => photoUrl(x.urls, width: 480) != null).toList() ?? const <DiscoverPhoto>[];
    return TkScreen(
      title: p?.name ?? widget.name ?? 'Discover',
      backLabel: 'Back to Discover',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (p == null && _error == null) const TkSkeleton(rows: 3, label: 'Loading their profile…'),
        if (p != null) ...[
          TkTopCard(
            padding: const EdgeInsets.fromLTRB(18, 22, 18, 18),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Center(child: BusinessFace(name: p.name, urls: p.photo?.urls, size: 72, letterSize: 26)),
                const SizedBox(height: 12),
                Semantics(header: true, child: Text(p.name, textAlign: TextAlign.center, style: tkDisplay(size: 24, lineHeight: 1.2, letterSpacing: -0.6, color: t.text))),
                if ((p.tagline ?? '').isNotEmpty)
                  Padding(padding: const EdgeInsets.only(top: 4), child: Text(p.tagline!, textAlign: TextAlign.center, style: tkText(size: 14, lineHeight: 1.4, color: t.muted))),
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text([p.categoryName, if ((p.area ?? '').isNotEmpty) p.area!].join(' · '), textAlign: TextAlign.center, style: tkText(size: 13, color: t.muted)),
                ),
                const SizedBox(height: 10),
                Center(child: CardPill(p.openNow ? 'Open now' : 'Closed now', p.openNow ? CardTone.lime : CardTone.plain)),
                if (p.line.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 6), child: Text(p.line, textAlign: TextAlign.center, style: tkText(size: 13, color: t.muted))),
                const SizedBox(height: 16),
                if (canChat)
                  CardButton(label: _busy ? 'Opening the chat…' : 'Chat', semantics: 'Chat with ${p.name}', primary: true, busy: _busy, onTap: () => _chat())
                else
                  Text('${p.name} isn’t taking chats in Taskiem right now.', textAlign: TextAlign.center, style: tkText(size: 14, color: t.muted)),
                if (canChat && (p.hasShop || p.hasBook)) ...[
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      if (p.hasShop) Expanded(child: CardButton(label: 'Their shop', onTap: _busy ? null : () => _chat(tool: 'shop'))),
                      if (p.hasShop && p.hasBook) const SizedBox(width: 8),
                      if (p.hasBook) Expanded(child: CardButton(label: 'Book', onTap: _busy ? null : () => _chat(tool: 'book'))),
                    ],
                  ),
                ],
              ],
            ),
          ),
          if (photos.isNotEmpty) _Photos(photos: photos, name: p.name),
          if (hours.isNotEmpty)
            TkSection(
              title: 'Hours',
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      for (final (day, line) in hours)
                        Padding(
                          padding: const EdgeInsets.symmetric(vertical: 2),
                          child: Row(
                            children: [
                              SizedBox(width: 56, child: Text(day, style: tkText(size: 14, color: t.muted))),
                              Expanded(child: Text(line, style: tkText(size: 14, color: t.text).copyWith(fontFeatures: tkTabular))),
                            ],
                          ),
                        ),
                      if (note is String && note.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 6), child: Text(note, style: tkText(size: 13, color: t.muted))),
                    ],
                  ),
                ),
              ],
            ),
          if ((p.address ?? '').isNotEmpty || (p.mapLink ?? '').isNotEmpty)
            TkSection(
              title: 'Where',
              inset: 56,
              children: [
                if ((p.address ?? '').isNotEmpty) TkListRow(title: p.address!, icon: 'b-pin', square: look.orange, semanticsLabel: 'Address: ${p.address}'),
                if ((p.mapLink ?? '').isNotEmpty)
                  TkListRow(
                    title: 'Open in maps',
                    icon: 'b-route',
                    square: look.blue,
                    onTap: () => openOrSay(context, () => openInBrowser(Uri.parse(p.mapLink!)), 'The map didn’t open. Try again.'),
                  ),
              ],
            ),
          TkSection(
            inset: 56,
            children: [
              TkListRow(
                title: 'Report this business',
                icon: 'b-alert',
                square: look.red,
                onTap: () => showTkSheet<void>(context, (_) => DiscoverReportSheet(auth: widget.auth, business: p.business, name: p.name)),
              ),
            ],
          ),
        ],
      ],
    );
  }
}

/// A business's photos, across, each with its caption read out.
class _Photos extends StatelessWidget {
  const _Photos({required this.photos, required this.name});
  final List<DiscoverPhoto> photos;
  final String name;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return SizedBox(
      height: 168,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemCount: photos.length,
        separatorBuilder: (_, _) => const SizedBox(width: 10),
        itemBuilder: (context, i) {
          final p = photos[i];
          return Semantics(
            image: true,
            label: (p.caption ?? '').isNotEmpty ? p.caption : 'Photo ${i + 1} of $name',
            child: ClipRRect(
              borderRadius: BorderRadius.circular(16),
              child: Image.network(
                photoUrl(p.urls, width: 480)!,
                width: 220,
                height: 168,
                fit: BoxFit.cover,
                excludeFromSemantics: true,
                errorBuilder: (_, _, _) => Container(width: 220, height: 168, color: t.tkSoft),
              ),
            ),
          );
        },
      ),
    );
  }
}

/// Report this business (§10, §15): why, and a note.
class DiscoverReportSheet extends StatefulWidget {
  const DiscoverReportSheet({super.key, required this.auth, required this.business, required this.name});
  final Auth auth;
  final String business;
  final String name;

  @override
  State<DiscoverReportSheet> createState() => _DiscoverReportSheetState();
}

class _DiscoverReportSheetState extends State<DiscoverReportSheet> {
  String? _reason;
  bool _busy = false;
  String? _error;
  String? _done;
  final _note = TextEditingController();

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final r = _reason;
    if (r == null) return setState(() => _error = 'Choose why you are reporting them.');
    setState(() => (_busy = true, _error = null));
    try {
      final note = _note.text.trim();
      final out = await widget.auth.api.discoverReport(business: widget.business, body: DiscoverReportRequest(reason: r, note: note.isEmpty ? null : note));
      if (mounted) setState(() => _done = out.words);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.message, _busy = false));
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (_done != null) {
      return TkAppLook(
        child: TkSheet(
          title: 'Report sent',
          body: TkFormBody(children: [Semantics(liveRegion: true, child: Text(_done!, style: tkText(size: 15, color: t.text)))]),
          footer: TkCta(label: 'Done', onTap: () => Navigator.of(context).maybePop()),
        ),
      );
    }
    return TkAppLook(
      child: TkSheet(
        title: 'Report ${widget.name}',
        crumb: 'Taskiem’s moderators look at every report',
        footer: TkCta(label: _busy ? 'Sending…' : 'Send report', danger: true, onTap: _busy ? null : _send),
        body: TkFormBody(
          children: [
            TkFormGroup(
              title: 'Why',
              children: [
                for (final (k, words) in discoverReportReasons)
                  TkChoiceRow(control: TkRadio(checked: _reason == k), label: Text(words, style: tkText(size: 15, color: t.text)), onTap: () => setState(() => _reason = k)),
              ],
            ),
            TkFormGroup(children: [TkKitField(label: 'What happened', hint: 'Optional', child: TkTextBox(controller: _note, lines: 3, max: 500))]),
            if (_error != null) TkErrorBox(_error!),
          ],
        ),
      ),
    );
  }
}
