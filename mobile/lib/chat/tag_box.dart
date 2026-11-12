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
  TagBox({required this.text, required this.sources, required this.grouped, required this.taskiem, required this.onChange, this.explore, this.onExplore});

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

  TagQuery? query;
  final picked = <TagSuggestion>[];
  final overlay = OverlayPortalController();
  final boxTop = LayerLink();

  /// The words changed by a pick, not by typing: the picker stays shut
  /// until the next keystroke (the web reads the tag on input only).
  bool _picking = false;
  ({String q, List<TagSuggestion> list})? _found;
  Timer? _soon;

  void dispose() => _soon?.cancel();

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
    query = q;
    _ask();
    onChange();
    if (floats) {
      overlay.show();
    } else {
      overlay.hide();
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
    if (q.mark == '/') {
      final kind = q.kind ?? '';
      return [(label: refLabel[kind] ?? '', items: suggest(src.refs[kind] ?? const [], q.query))];
    }
    if (grouped() && q.mark == '@') {
      final words = _words;
      final found = _found;
      return groupTags(
        q.query,
        items: src.items,
        services: src.services,
        businesses: src.businesses,
        taskiem: taskiem,
        unlinked: found != null && words.length >= 2 && found.q == words ? found.list : const [],
      );
    }
    return [(label: '', items: suggest(q.mark == '@' ? src.businesses : src.items, q.query))];
  }

  /// What Enter would take: the first; the Taskiem row only when "Taskiem"
  /// is what is being typed.
  TagSuggestion? first(List<TagGroup> groups) {
    final q = query;
    return [for (final g in groups) ...g.items]
        .where((s) => s.kind != 'taskiem' || (q != null && q.query.isNotEmpty && 'taskiem'.startsWith(q.query.toLowerCase())))
        .firstOrNull;
  }

  void pick(TagSuggestion s) {
    final q = query;
    if (q == null) return;
    final explored = s.explore;
    if (explored != null) {
      // Not linked: its card, with Connect; the words stay as typed.
      query = null;
      overlay.hide();
      onChange();
      onExplore?.call(explored);
      return;
    }
    final v = text.value;
    final caret = v.selection.baseOffset < 0 ? v.text.length : v.selection.baseOffset;
    // Where what was typed for it ends: the mark, "invoice " for "/", the words.
    final head = q.mark == '/' ? RegExp(r'^[a-z]+[/ ]', caseSensitive: false).firstMatch(v.text.substring(q.start + 1))?.end ?? 0 : 0;
    final end = q.start + 1 + head + q.query.length;
    final next = insertTag(v.text, q, caret < end ? end : caret, s.name);
    if (s.kind != 'taskiem') picked.add(s);
    _picking = true;
    text.value = TextEditingValue(text: next.text, selection: TextSelection.collapsed(offset: next.caret));
    _picking = false;
    query = null;
    overlay.hide();
    onChange();
  }

  /// What the message sends of its tags, and the picks forgotten.
  List<SendLinkMessageRequestMentionsItem> take(String words) {
    final out = tagRefs(words, picked);
    picked.clear();
    return out;
  }

  /// The plain list above the box, when it is the one shown.
  Widget plain() {
    final q = query;
    if (q == null || floats) return const SizedBox.shrink();
    final shown = [for (final g in groups()) ...g.items];
    if (shown.isEmpty) return const SizedBox.shrink();
    return TagList(mark: q.mark, items: shown, onPick: pick);
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
    if (groups.every((g) => g.items.isEmpty)) return const SizedBox.shrink();
    return TagPicker(query: q.query, groups: groups, first: box.first(groups), onPick: box.pick);
  }
}
