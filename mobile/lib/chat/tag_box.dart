// Tags in a message box (web/src/pages/Mentions.tsx useMentions), shared by
// a business's conversation and Taskiem's: what is typed after "@", "#" or
// "/invoice" and the like; what was picked (sent with the words while they
// still name it); with chat tools, "@" asks Explore too after two letters,
// a business not linked shown by its card rather than tagged; and the
// picker over the box, floating for "@" and "/" with the tools, else the
// plain list above it.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import 'saved_picks.dart';
import 'tag_picker.dart';
import 'tags.dart';

/// What the box's tags are drawn from.
typedef TagSources = ({
  List<TagSuggestion> items,
  List<TagSuggestion> services,
  List<TagSuggestion> businesses,
  Map<String, List<TagSuggestion>> refs,
});

class TagBox {
  TagBox({required this.text, required this.sources, required this.grouped, required this.taskiem, required this.onChange, this.explore, this.onExplore, this.saved, this.onShareSaved, this.savedAsTags = false, this.onSaveNamed, this.onAttachSaved, this.searchRefs});

  final TextEditingController text;
  final TagSources Function() sources;

  /// With chat tools: "@" in groups, floating; "/" too.
  final bool Function() grouped;

  /// The Taskiem row offered last (not in Taskiem's own conversation).
  final bool taskiem;
  final VoidCallback onChange;

  /// Explore's businesses for the words (chat tools phase 6).
  final Future<List<ExploreCard>> Function(String words)? explore;

  /// A business not linked, picked: its card.
  final ValueChanged<ExploreCard>? onExplore;

  /// Saved things "@" offers (saved objects S1; saved_picks.dart), read
  /// the first time "@" is typed.
  final SavedPicks? saved;

  /// A saved thing other than a text picked: shared as a card (S1 part 3),
  /// what was typed for it taken out of the box.
  final void Function(TagSuggestion)? onShareSaved;

  /// Saved things other than texts kept as tags, sent with the message (a
  /// business's chat with another business; S1 part 3).
  final bool savedAsTags;

  /// Save "@mum"… when nothing saved has the name typed (saved objects S1,
  /// part 4): the name, to save something as.
  final void Function(String name)? onSaveNamed;

  /// A saved file picked: attached to the message as itself (S1), what was
  /// typed for it taken out of the box.
  final void Function(TagSuggestion)? onAttachSaved;

  /// Invoices, orders and customers searched on the server, every record
  /// and not only the newest (menus-everywhere.md §7): the words, the
  /// kinds ("invoice,bill"), where the last page ended; the next page and
  /// where it ends ("" for no more).
  final Future<({List<TagSuggestion> list, String cursor})> Function(String words, String kinds, String? cursor)? searchRefs;

  TagQuery? query;

  /// The picker's chip, when one other than the first was pressed.
  String? _chip;

  /// How many times "Show more" was pressed under this chip.
  int _more = 0;

  /// What the server found for a chip and its words, and where it ended.
  ({String key, List<TagSuggestion> list, String cursor})? _found2;
  bool _searching = false;

  /// The server could not be asked: what the phone keeps is searched.
  bool offline = false;
  Timer? _refSoon;

  /// The picker's search box: what is typed after the mark, both ways.
  final search = TextEditingController();
  final picked = <TagSuggestion>[];
  final overlay = OverlayPortalController();
  final boxTop = LayerLink();

  /// The words changed by a pick, not by typing: the picker stays shut
  /// until the next keystroke (the web reads the tag on input only).
  bool _picking = false;
  ({String q, List<TagSuggestion> list})? _found;
  Timer? _soon;

  void dispose() {
    _soon?.cancel();
    _refSoon?.cancel();
    search.dispose();
  }

  /// The floating picker shut, when it is open: a box with no floating
  /// picker (a chat offering only saved texts) never placed it.
  void _hideFloating() {
    if (overlay.isShowing) overlay.hide();
  }

  /// Floating over the thread (the tools' picker), not the plain list.
  bool get floats => query != null && grouped() && (query!.mark == '@' || query!.mark == '/');

  String get _words => query != null && grouped() && query!.mark == '@' ? query!.query.trim() : '';

  /// The text or the caret moved.
  void typed() {
    if (_picking) return;
    final v = text.value;
    final caret = v.selection.baseOffset < 0 ? v.text.length : v.selection.baseOffset;
    final q = tagQuery(v.text, caret);
    if (q == query) return;
    // Another tag begun: the picker starts again on its own chip.
    if (q == null || query == null || q.start != query!.start || q.mark != query!.mark || q.kind != query!.kind) {
      _chip = null;
      _more = 0;
    }
    query = q;
    if (q != null && search.text != q.query) search.text = q.query;
    _askRefs();
    if (q?.mark == '@' || (q?.mark == '/' && q?.kind == null)) saved?.want();
    _ask();
    onChange();
    if (floats) {
      overlay.show();
    } else {
      _hideFloating();
    }
  }

  void _ask() {
    _soon?.cancel();
    final words = _words;
    final look = explore;
    if (look == null || words.length < 2 || _found?.q == words) return;
    _soon = Timer(const Duration(milliseconds: 250), () async {
      List<TagSuggestion> list;
      try {
        list = [for (final c in await look(words)) ?unlinkedTag(c)];
      } on Object {
        list = const [];
      }
      _found = (q: words, list: list);
      if (_words == words) onChange();
    });
  }

  /// The picker's groups now.
  List<TagGroup> groups() {
    final q = query;
    if (q == null) return const [];
    final src = sources();
    if (q.mark == '/' && q.kind == null) {
      // "/mum": the saved things by that name, if any.
      final found = suggest(saved?.list ?? const [], q.query).take(4).toList();
      return [if (found.isNotEmpty) (label: 'Saved', items: found)];
    }
    if (floats && chip != 'all') return [_chipGroup(q, chip)];
    if (q.mark == '/') {
      final kind = q.kind ?? '';
      return [(label: refLabel[kind] ?? '', items: suggest(src.refs[kind] ?? const [], q.query))];
    }
    if (grouped() && q.mark == '@') {
      final words = _words;
      final found = _found;
      return _withSaveNew(q, groupTags(
        q.query,
        items: src.items,
        services: src.services,
        businesses: src.businesses,
        taskiem: taskiem,
        unlinked: found != null && words.length >= 2 && found.q == words ? found.list : const [],
        saved: saved?.list ?? const [],
      ));
    }
    return _withSaveNew(q, [
      for (final g in <TagGroup>[
        (label: '', items: suggest(q.mark == '@' ? src.businesses : src.items, q.query)),
        if (q.mark == '@' && saved != null) (label: 'Saved', items: suggest(saved!.list, q.query).take(4).toList()),
      ])
        if (g.items.isNotEmpty) g,
    ]);
  }

  /// The chip the picker shows: the one pressed, else the tag's own.
  String get chip => _chip ?? (query?.mark == '@' ? 'all' : tagChipOf(query?.kind));

  /// The picker's chips here: the tag's own, and every kind with something
  /// to offer (menus-everywhere.md §7). None for "/mum".
  List<String> chips() {
    final q = query;
    if (q == null || !floats || (q.mark == '/' && q.kind == null)) return const [];
    final src = sources();
    return [
      for (final k in tagChipLabels.keys)
        if (k == chip ||
            switch (k) {
              'all' => q.mark == '@',
              'saved' => (saved?.list ?? const []).isNotEmpty,
              _ => _local(k, src).isNotEmpty,
            })
          k,
    ];
  }

  /// A chip pressed: its list, from its first six.
  void pickChip(String k) {
    if (k == chip) return;
    _chip = k;
    _more = 0;
    if (k == 'saved') saved?.want();
    _askRefs();
    onChange();
  }

  /// "/bill" and "/purchase" keep to their own kind until a chip is pressed.
  String? get _narrow => _chip == null && (query?.kind == 'bill' || query?.kind == 'purchase') ? query!.kind : null;

  /// The kinds the chip searches on the server.
  String? get _kinds => _narrow ?? tagChipKinds[chip];

  /// What a chip offers from what is on the phone.
  List<TagSuggestion> _local(String k, TagSources src) => switch (k) {
    // "@" offers every item (suppliers' too); "/product" our own.
    'product' => query?.mark == '@' ? src.items : src.refs['product'] ?? src.items,
    'service' => src.services,
    'invoice' || 'order' => src.refs[_narrow ?? k] ?? const [],
    'customer' => src.refs['customer'] ?? const [],
    'business' => src.businesses,
    'saved' => saved?.list ?? const [],
    _ => const [],
  };

  /// The server's answer for the chip and the words now, when it came.
  ({String key, List<TagSuggestion> list, String cursor})? get _served {
    final q = query;
    final f = _found2;
    return q != null && f != null && f.key == '${_narrow ?? chip}|${q.query.trim().toLowerCase()}' ? f : null;
  }

  /// One chip's list: six, and six more for each "Show more".
  TagGroup _chipGroup(TagQuery q, String k) {
    final served = _served;
    final all = served != null ? served.list : suggest(_local(k, sources()), q.query, limit: null);
    // Headed as "/invoice" heads it ("Invoices and bills", "Bills").
    return (label: refLabel[_narrow ?? k] ?? tagChipLabels[k] ?? '', items: all.take(6 * (_more + 1)).toList());
  }

  /// Whether the chip has more than it shows.
  bool get hasMore {
    final q = query;
    if (q == null || !floats || chip == 'all') return false;
    final served = _served;
    final all = served != null ? served.list.length : suggest(_local(chip, sources()), q.query, limit: null).length;
    return all > 6 * (_more + 1) || (served != null && served.cursor.isNotEmpty);
  }

  /// "Show more": six more, the server's next page when they run out.
  Future<void> showMore() async {
    _more++;
    onChange();
    final served = _served;
    final look = searchRefs;
    if (look == null || served == null || served.cursor.isEmpty || served.list.length >= 6 * (_more + 1) || _searching) return;
    _searching = true;
    try {
      final next = await look(query!.query.trim(), _kinds!, served.cursor);
      if (identical(_found2, served)) _found2 = (key: served.key, list: [...served.list, ...next.list], cursor: next.cursor);
    } on Object {
      offline = true;
    } finally {
      _searching = false;
    }
    onChange();
  }

  /// Invoices, orders or customers under their chip: asked of the server
  /// for the words typed, a moment after the last key.
  void _askRefs() {
    _refSoon?.cancel();
    final look = searchRefs;
    final q = query;
    final kinds = _kinds;
    if (look == null || q == null || !floats || kinds == null) return;
    final words = q.query.trim().toLowerCase();
    final key = '${_narrow ?? chip}|$words';
    if (_found2?.key == key) return;
    _refSoon = Timer(const Duration(milliseconds: 250), () async {
      try {
        final got = await look(words, kinds, null);
        _found2 = (key: key, list: got.list, cursor: got.cursor);
        offline = false;
      } on Object {
        offline = true;
      }
      onChange();
    });
  }

  /// The search box typed in: its words in the message box, after the mark
  /// (and "/invoice "), so both say the same.
  void searchFor(String words) {
    final q = query;
    if (q == null) return;
    final v = text.value;
    final head = q.mark == '/' ? RegExp(r'^[a-z]+[/ ]', caseSensitive: false).firstMatch(v.text.substring(q.start + 1))?.end ?? 0 : 0;
    final from = q.start + 1 + head;
    final end = (from + q.query.length).clamp(0, v.text.length);
    final next = v.text.substring(0, from) + words + v.text.substring(end);
    text.value = TextEditingValue(text: next, selection: TextSelection.collapsed(offset: from + words.length));
  }

  /// Save "@mum"… under Saved, once the saved things are read and none
  /// (nor anything else offered) has the name typed.
  List<TagGroup> _withSaveNew(TagQuery q, List<TagGroup> groups) {
    final name = q.query.toLowerCase();
    final picks = saved;
    if (q.mark != '@' || onSaveNamed == null || picks == null || !picks.loaded || !picks.complete || !RegExp(r'^[a-z0-9][a-z0-9_-]{0,31}$').hasMatch(name)) return groups;
    if (picks.list.any((s) => s.name.toLowerCase() == name) || groups.any((g) => g.items.any((s) => s.name.toLowerCase() == name))) return groups;
    final row = TagSuggestion(kind: 'save-new', id: 'save-new:$name', name: name, sub: 'Save something by this name');
    final i = groups.indexWhere((g) => g.label == 'Saved');
    if (i < 0) return [...groups, (label: 'Saved', items: [row])];
    return [for (var j = 0; j < groups.length; j++) j == i ? (label: groups[j].label, items: [...groups[j].items, row]) : groups[j]];
  }

  /// What Enter would take: the first; the Taskiem row only when "Taskiem"
  /// is what is being typed.
  TagSuggestion? first(List<TagGroup> groups) {
    final q = query;
    return [for (final g in groups) ...g.items]
        .where((s) => s.kind != 'save-new' && (s.kind != 'taskiem' || (q != null && q.query.isNotEmpty && 'taskiem'.startsWith(q.query.toLowerCase()))))
        .firstOrNull;
  }

  void pick(TagSuggestion s) {
    final q = query;
    if (q == null) return;
    final explored = s.explore;
    if (explored != null) {
      // Not linked: its card, with Connect; the words stay as typed.
      query = null;
      _hideFloating();
      onChange();
      onExplore?.call(explored);
      return;
    }
    final v = text.value;
    final caret = v.selection.baseOffset < 0 ? v.text.length : v.selection.baseOffset;
    // Where what was typed for it ends: the mark, "invoice " for "/", the words.
    final head = q.mark == '/' ? RegExp(r'^[a-z]+[/ ]', caseSensitive: false).firstMatch(v.text.substring(q.start + 1))?.end ?? 0 : 0;
    final end = q.start + 1 + head + q.query.length;
    // A saved text's words in place of what was typed, to change before
    // sending; any other saved thing as its @name, which the words carry.
    final body = s.body;
    final attach = onAttachSaved;
    if (s.kind == 'saved' && s.fileName != null && attach != null) {
      final out = insertWords(v.text, q, caret < end ? end : caret, '');
      _picking = true;
      text.value = TextEditingValue(text: out.text, selection: TextSelection.collapsed(offset: out.caret));
      _picking = false;
      query = null;
      _hideFloating();
      onChange();
      attach(s);
      return;
    }
    final named = onSaveNamed;
    if (s.kind == 'save-new' && named != null) {
      final out = insertWords(v.text, q, caret < end ? end : caret, '');
      _picking = true;
      text.value = TextEditingValue(text: out.text, selection: TextSelection.collapsed(offset: out.caret));
      _picking = false;
      query = null;
      _hideFloating();
      onChange();
      named(s.name);
      return;
    }
    final share = onShareSaved;
    if (s.kind == 'saved' && body == null && share != null) {
      final out = insertWords(v.text, q, caret < end ? end : caret, '');
      _picking = true;
      text.value = TextEditingValue(text: out.text, selection: TextSelection.collapsed(offset: out.caret));
      _picking = false;
      query = null;
      _hideFloating();
      onChange();
      share(s);
      return;
    }
    final next = s.kind == 'saved' && body != null ? insertWords(v.text, q, caret < end ? end : caret, body) : insertTag(v.text, q, caret < end ? end : caret, s.name, kind: s.kind);
    if (s.kind != 'taskiem' && (s.kind != 'saved' || (savedAsTags && body == null))) picked.add(s);
    _picking = true;
    text.value = TextEditingValue(text: next.text, selection: TextSelection.collapsed(offset: next.caret));
    _picking = false;
    query = null;
    _hideFloating();
    onChange();
  }

  /// What the message sends of its tags, and the picks forgotten (kept in
  /// [taken], to give back if the message does not go).
  List<SendLinkMessageRequestMentionsItem> take(String words) {
    final out = tagRefs(words, picked);
    taken = [...picked];
    picked.clear();
    return out;
  }

  /// The picks of the last message taken.
  List<TagSuggestion> taken = const [];

  /// A message not sent after all: its picks back, with its words.
  void giveBack() {
    picked
      ..clear()
      ..addAll(taken);
  }

  /// Whether "/" is picking a thing to tag (a document, or a saved thing
  /// that has the name typed), not a tool.
  bool get tagging {
    final q = query;
    if (q == null || q.mark != '/') return false;
    return q.kind != null || groups().isNotEmpty;
  }

  /// The plain list above the box, when it is the one shown.
  Widget plain() {
    final q = query;
    if (q == null || floats) return const SizedBox.shrink();
    final shown = [for (final g in groups()) ...g.items];
    if (shown.isEmpty) return const SizedBox.shrink();
    // Only saved things to offer (a chat with no one else to name).
    final onlySaved = shown.every((s) => s.kind == 'saved' || s.kind == 'save-new');
    return TagList(mark: q.mark, items: shown, onPick: pick, label: onlySaved ? 'Saved things' : null);
  }

  /// The floating picker's place: put just above the box; it hangs 8 px
  /// over the thread from there (`.composer > .tk-picker`).
  Widget anchor(BuildContext context) => OverlayPortal(
    controller: overlay,
    overlayChildBuilder: (context) => Positioned(
      left: 0,
      top: 0,
      child: CompositedTransformFollower(
        link: boxTop,
        targetAnchor: Alignment.topLeft,
        followerAnchor: Alignment.bottomLeft,
        offset: const Offset(10, -8),
        child: SizedBox(width: MediaQuery.sizeOf(context).width - 20, child: _Floating(box: this)),
      ),
    ),
    child: CompositedTransformTarget(link: boxTop, child: const SizedBox(width: double.infinity)),
  );
}

/// The tools' picker, drawn from the box's groups.
class _Floating extends StatelessWidget {
  const _Floating({required this.box});
  final TagBox box;

  @override
  Widget build(BuildContext context) {
    final q = box.query;
    if (q == null || !box.floats) return const SizedBox.shrink();
    final groups = box.groups();
    // Empty under the tag's own words with no chips to try: nothing shown.
    if (groups.every((g) => g.items.isEmpty) && box.chips().length < 2) return const SizedBox.shrink();
    return TagPicker(
      query: q.query,
      groups: groups,
      first: box.first(groups),
      onPick: box.pick,
      search: box.search,
      onSearch: box.searchFor,
      chips: box.chips(),
      chip: box.chip,
      onChip: box.pickChip,
      more: box.hasMore ? box.showMore : null,
      offline: box.offline && tagChipKinds.containsKey(box.chip),
    );
  }
}
