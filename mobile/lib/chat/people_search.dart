// Search across a person's own people chats, and their starred messages
// (docs/spec/people-chatting.md §6.2, §7; the server's As built, "stage 5a
// gaps, chat features": `GET /v1/me/people/search`, `GET
// /v1/me/people/starred`). Each found message opens its chat at it, older
// pages read until it is there, placed in the middle and flashed.
//
// The search runs on Taskiem's server over the person's own copies (people
// chats are kept by Taskiem, readable by its servers; not end-to-end
// encrypted). Nothing of a search is kept on the phone.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/load_more.dart';
import '../kit/mobile.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'chats.dart' show ChatRow;
import 'my_chat.dart' show sayFailure;
import 'groups.dart' show groupLine;
import 'people.dart';
import 'words.dart';

/// Opens a people chat at one of its messages.
typedef OpenPeopleChatAt = Future<void> Function(String chatId, String messageId);

/// A found message: in a chat with a person ([person]) or in a group
/// ([group]).
class AnyFound {
  AnyFound.person(PeopleFound this.person) : group = null;
  AnyFound.group(GroupFound this.group) : person = null;
  final PeopleFound? person;
  final GroupFound? group;

  /// The chat's or the group's id.
  String get chatId => person?.chatId ?? group!.groupId;
  String get messageId => person?.message.id ?? group!.message.id;

  /// When it was starred, else sent: the merged lists' order.
  DateTime get at => person?.starredAt ?? group?.starredAt ?? person?.message.createdAt ?? group!.message.createdAt;
}

/// The cursors of a merged list (people chats', groups'), as one.
String? _joinNext(String? people, String? groups) => people == null && groups == null ? null : '${people ?? ''}|${groups ?? ''}';
(String?, String?) _splitNext(String next) {
  final i = next.indexOf('|');
  String? or(String s) => s.isEmpty ? null : s;
  return i < 0 ? (or(next), null) : (or(next.substring(0, i)), or(next.substring(i + 1)));
}

/// People chats' pages and groups' pages read together and merged by
/// time (the newest first); a server without groups (404) gives none of
/// groups.
Future<FoundPage> mergedPage(
  String? before, {
  required Future<({List<PeopleFound> found, bool more, String? next})> Function(String? before) people,
  required Future<({List<GroupFound> found, bool more, String? next})> Function(String? before) groups,
}) async {
  final (p, g) = before == null ? (null, null) : _splitNext(before);
  final readPeople = before == null || p != null;
  final readGroups = before == null || g != null;
  Future<({List<GroupFound> found, bool more, String? next})> groupsOrNone() async {
    try {
      return await groups(g);
    } on ApiError catch (e) {
      if (e.status == 404) return (found: const <GroupFound>[], more: false, next: null);
      rethrow;
    }
  }

  final pr = readPeople ? await people(p) : null;
  final gr = readGroups ? await groupsOrNone() : null;
  final found = [...?pr?.found.map(AnyFound.person), ...?gr?.found.map(AnyFound.group)]..sort((a, b) => b.at.compareTo(a.at));
  final pNext = pr != null && pr.more ? pr.next : null;
  final gNext = gr != null && gr.more ? gr.next : null;
  return (found: found, more: pNext != null || gNext != null, next: _joinNext(pNext, gNext));
}

/// Whether [q] can be searched: a word of two letters or more (the server
/// answers 400 otherwise).
bool searchable(String q) => q.trim().length <= 200 && RegExp(r'[\p{L}\p{N}]{2,}', unicode: true).hasMatch(q);

/// The words for a search that could not run: the server's own (too
/// fast, nothing to search for), or the network's.
String searchFailure(Object e) {
  if (e is ApiError && e.status == 429 && !e.body.containsKey('error')) return 'You are searching very fast. Wait a moment and try again.';
  return sayFailure(e);
}

/// One page of found messages (search or starred).
typedef FoundPage = ({List<AnyFound> found, bool more, String? next});

/// A list of found messages read a page at a time.
class FoundList extends ChangeNotifier {
  FoundList(this.read);

  /// Reads the page after [before] (null: the first).
  final Future<FoundPage> Function(String? before) read;

  List<AnyFound>? found;
  bool more = false;
  String? _next;
  String? error;
  bool busy = false;
  int _round = 0;
  bool _disposed = false;

  /// The first page, again.
  Future<void> load() async {
    final round = ++_round;
    busy = true;
    error = null;
    _changed();
    try {
      final p = await read(null);
      if (round != _round || _disposed) return;
      found = p.found;
      more = p.more;
      _next = p.next;
    } on Object catch (e) {
      if (round != _round || _disposed) return;
      error = searchFailure(e);
    }
    busy = false;
    _changed();
  }

  /// The page after the last read.
  Future<void> loadMore() async {
    final next = _next;
    if (!more || next == null || busy) return;
    final round = _round;
    busy = true;
    try {
      final p = await read(next);
      if (round != _round || _disposed) return;
      found = [...?found, ...p.found.where((f) => !(found ?? const []).any((g) => g.messageId == f.messageId))];
      more = p.more;
      _next = p.next;
    } on Object catch (e) {
      if (round == _round && !_disposed) error = searchFailure(e);
    }
    busy = false;
    _changed();
  }

  /// Nothing shown (the box emptied).
  void clear() {
    _round++;
    found = null;
    more = false;
    _next = null;
    error = null;
    busy = false;
    _changed();
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}

/// A found message's row: the chat's name, the words around what was
/// found (or the message's line), and when.
class FoundRow extends StatelessWidget {
  const FoundRow({super.key, required this.f, required this.now, required this.transport, required this.onTap, this.showChat = true});
  final AnyFound f;
  final DateTime now;
  final Transport transport;
  final VoidCallback onTap;
  final bool showChat;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final g = f.group;
    if (g != null) {
      final m = g.message;
      final who = m.mine ? 'You' : m.sender;
      final words = (g.snippet ?? '').isNotEmpty ? g.snippet! : groupLine(m);
      return Semantics(
        label: 'Message ${m.mine ? 'from you' : 'from $who'}${showChat ? ' in the group ${g.group.name}' : ''}: $words',
        button: true,
        excludeSemantics: true,
        child: ChatRow(
          key: ValueKey('found ${m.id}'),
          title: showChat ? g.group.name : who,
          letter: g.group.name,
          avatar: PersonFace(name: g.group.name, transport: transport, photo: g.group.photo, size: 48),
          marks: showChat ? const [(label: 'Group', off: false)] : const [],
          time: listTime(m.createdAt, now),
          preview: Text(showChat ? '$who: $words' : words, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 14, color: t.muted)),
          onTap: onTap,
        ),
      );
    }
    final p = f.person!;
    final m = p.message;
    final who = m.mine ? 'You' : p.chat.name;
    final words = (p.snippet ?? '').isNotEmpty ? p.snippet! : peopleLine(m);
    return Semantics(
      label: 'Message ${m.mine ? 'from you' : 'from ${p.chat.name}'}${showChat ? ' in your chat with ${p.chat.name}' : ''}: $words',
      button: true,
      excludeSemantics: true,
      child: ChatRow(
        key: ValueKey('found ${m.id}'),
        title: showChat ? p.chat.name : who,
        letter: p.chat.name,
        avatar: PersonFace(name: p.chat.name, transport: transport, photo: p.chat.person?.photo, size: 48),
        time: listTime(m.createdAt, now),
        preview: Text(showChat ? '$who: $words' : words, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 14, color: t.muted)),
        onTap: onTap,
      ),
    );
  }
}

/// Search your people chats and groups ([chat]: only that chat; [group]:
/// only that group). With [onPick], a found message is given back (the
/// chat it is in jumps to it); else it is opened with [openAt] (a group's
/// with [openGroupAt]).
class PeopleSearchScreen extends StatefulWidget {
  const PeopleSearchScreen({super.key, required this.auth, this.openAt, this.openGroupAt, this.onPick, this.chat, this.group, this.chatName, this.now = DateTime.now, this.wait = const Duration(milliseconds: 400)});
  final Auth auth;
  final OpenPeopleChatAt? openAt;
  final OpenPeopleChatAt? openGroupAt;
  final ValueChanged<String>? onPick;
  final String? chat;
  final String? group;
  final String? chatName;
  final DateTime Function() now;

  /// How long typing rests before it is searched.
  final Duration wait;

  @override
  State<PeopleSearchScreen> createState() => _PeopleSearchScreenState();
}

class _PeopleSearchScreenState extends State<PeopleSearchScreen> {
  final _q = TextEditingController();
  // The words last searched, not the box's: Show more pages what is shown
  // even while new words are being typed (not yet searched).
  late final FoundList _list = FoundList((before) async {
    if (widget.group != null) {
      final r = await widget.auth.api.searchGroups(q: _searched, group: widget.group, before: before);
      return (found: r.results.map(AnyFound.group).toList(), more: r.more, next: r.next);
    }
    if (widget.chat != null) {
      final r = await widget.auth.api.searchPeople(q: _searched, chat: widget.chat, before: before);
      return (found: r.results.map(AnyFound.person).toList(), more: r.more, next: r.next);
    }
    return mergedPage(
      before,
      people: (b) async {
        final r = await widget.auth.api.searchPeople(q: _searched, before: b);
        return (found: r.results, more: r.more, next: r.next);
      },
      groups: (b) async {
        final r = await widget.auth.api.searchGroups(q: _searched, before: b);
        return (found: r.results, more: r.more, next: r.next);
      },
    );
  });
  Timer? _soon;
  String _searched = '';

  @override
  void initState() {
    super.initState();
    _q.addListener(_typed);
  }

  @override
  void dispose() {
    _soon?.cancel();
    _q.dispose();
    _list.dispose();
    super.dispose();
  }

  void _typed() {
    final q = _q.text.trim();
    if (q == _searched) return;
    _soon?.cancel();
    if (!searchable(q)) {
      _searched = '';
      _list.clear();
      return;
    }
    _soon = Timer(widget.wait, () {
      if (!mounted) return;
      _searched = q;
      _list.load();
    });
  }

  Future<void> _open(AnyFound f) async {
    final pick = widget.onPick;
    if (pick != null) {
      Navigator.of(context).pop();
      return pick(f.messageId);
    }
    await (f.group != null ? widget.openGroupAt : widget.openAt)?.call(f.chatId, f.messageId);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final one = widget.chat != null || widget.group != null;
    return ListenableBuilder(
      listenable: _list,
      builder: (context, _) {
        final found = _list.found;
        final q = _q.text.trim();
        return TkScreen(
          title: widget.group != null ? 'Search this group' : (one ? 'Search this chat' : 'Search chats with people'),
          children: [
            Semantics(
              label: widget.group != null ? 'Search the group ${widget.chatName ?? ''}' : (one ? 'Search your chat with ${widget.chatName ?? 'them'}' : 'Search your chats with people and groups'),
              textField: true,
              child: TkTextBox(key: const ValueKey('people-search'), controller: _q, hint: 'Words, a file’s name, a place…', max: 200),
            ),
            if (!one && q.isEmpty)
              TkSection(
                inset: 56,
                children: [
                  TkListRow(
                    title: 'Starred messages',
                    sub: 'Messages you starred, in every chat with people and group',
                    icon: 'star',
                    square: look.orange,
                    onTap: () => tkPush<void>(context, PeopleStarredScreen(auth: widget.auth, openAt: widget.openAt ?? (_, _) async {}, openGroupAt: widget.openGroupAt, now: widget.now)),
                  ),
                ],
              ),
            if (q.isNotEmpty && !searchable(q)) Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: Text('Type a word of two letters or more.', style: tkHint(t))),
            if (_list.error != null) TkErrorBox(_list.error!),
            if (found == null && _list.busy) const TkLoading(label: 'Searching…'),
            if (found != null && found.isEmpty)
              const TkPanel(children: [TkEmpty(icon: 'search', title: 'Nothing found', line: 'No message has those words. Try the start of a word.')]),
            if (found != null && found.isNotEmpty)
              TkPanel(
                inset: 76,
                children: [
                  for (final f in found) FoundRow(f: f, now: widget.now(), transport: widget.auth.transport, showChat: !one, onTap: () => _open(f)),
                ],
              ),
            if (found != null && _list.more) TkLoadMore(label: 'Show more', onMore: _list.loadMore),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 4),
              child: Text('Search runs on Taskiem’s servers, over your own copy of your chats. Nothing you search for is kept.', style: tkHint(t)),
            ),
          ],
        );
      },
    );
  }
}

/// Your starred messages across your people chats, the most recently
/// starred first; each opens its chat at it.
class PeopleStarredScreen extends StatefulWidget {
  const PeopleStarredScreen({super.key, required this.auth, required this.openAt, this.openGroupAt, this.now = DateTime.now});
  final Auth auth;
  final OpenPeopleChatAt openAt;

  /// Opens a group at a starred message of it.
  final OpenPeopleChatAt? openGroupAt;
  final DateTime Function() now;

  @override
  State<PeopleStarredScreen> createState() => _PeopleStarredScreenState();
}

class _PeopleStarredScreenState extends State<PeopleStarredScreen> {
  late final FoundList _list = FoundList(
    (before) => mergedPage(
      before,
      people: (b) async {
        final r = await widget.auth.api.starredPeople(before: b);
        return (found: r.starred, more: r.more, next: r.next);
      },
      groups: (b) async {
        final r = await widget.auth.api.starredGroups(before: b);
        return (found: r.starred, more: r.more, next: r.next);
      },
    ),
  );

  @override
  void initState() {
    super.initState();
    _list.load();
  }

  @override
  void dispose() {
    _list.dispose();
    super.dispose();
  }

  Future<void> _open(AnyFound f) async {
    if (f.group != null) {
      await widget.openGroupAt?.call(f.chatId, f.messageId);
    } else {
      await widget.openAt(f.chatId, f.messageId);
    }
    // Unstarred in the chat: gone from here too.
    if (mounted) await _list.load();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: _list,
    builder: (context, _) {
      final found = _list.found;
      return TkScreen(
        title: 'Starred messages',
        onRefresh: _list.load,
        children: [
          if (_list.error != null) TkErrorBox(_list.error!),
          if (found == null && _list.error == null) const TkLoading(label: 'Loading…'),
          if (found != null && found.isEmpty)
            const TkPanel(children: [TkEmpty(icon: 'star', title: 'No starred messages', line: 'Hold a message in a chat or a group and choose Star to keep it here.')]),
          if (found != null && found.isNotEmpty)
            TkPanel(
              inset: 76,
              children: [for (final f in found) FoundRow(f: f, now: widget.now(), transport: widget.auth.transport, onTap: () => _open(f))],
            ),
          if (found != null && _list.more) TkLoadMore(label: 'Show more', onMore: _list.loadMore),
        ],
      );
    },
  );
}
