// People groups (docs/spec/people-chatting.md §9, §13; the 5b checklist A;
// "As built: stage 5b, groups (server)" and "group safety (server)"): what
// the group screens share. A group's words in a line, the add answers said
// honestly (no more than the server says), group links' codes, and the
// app-look pickers: chats and groups to forward to, people to add, one of
// a few choices.
//
// Groups are kept by Taskiem, sealed at rest under each group's own key and
// readable by Taskiem's servers. They are not end-to-end encrypted, and
// nothing here says they are.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../app/config.dart' show taskiemUrl;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading, TkSearchBox;
import '../kit/load_more.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'my_chat.dart' show sayFailure;
import 'people.dart';
import 'words.dart' show CardTone;

/// Opens a group by its id ([focus]: at one of its messages).
typedef OpenGroup = Future<void> Function(String groupId, {String? focus});

/// Where groups are kept and who reads them (§9, §13), as a group's info
/// says it. Wording for legal review, as [peopleKeptWords].
const groupKeptWords =
    'Groups are kept by Taskiem, sealed on its servers, so they show on every phone you sign in on. '
    'Taskiem’s servers can open them: they are not end-to-end encrypted. '
    'No one at Taskiem reads them, except a moderator when someone reports the group or a member, or when the law requires it. '
    'Automated checks look for scams in new groups. Groups are never used for ads or to train AI.';

/// The most a group holds (§17 Q7).
const groupMost = 256;

/// The consent asked before a group or a member is reported (§8.3).
const groupReportConsent = 'The last 20 messages you can see, with their photos and files, go to Taskiem’s moderators';

/// An add's answer in words, per person (`GroupAdded.state`): only what
/// the server says, nothing of the person's settings.
String groupAddedWords(String state) => switch (state) {
  'added' => 'Added',
  'invited' => 'Invited: they’ll see it in their Requests',
  'member' => 'Already in the group',
  'full' => 'The group is full',
  'unavailable' => 'Not on Taskiem',
  _ => 'Not added',
};

/// The adds of one go, in a line: "2 added, 1 invited".
String groupAddedSummary(List<GroupAdded> added) {
  final n = <String, int>{};
  for (final a in added) {
    n[a.state] = (n[a.state] ?? 0) + 1;
  }
  final parts = [
    if ((n['added'] ?? 0) > 0) '${n['added']} added',
    if ((n['invited'] ?? 0) > 0) '${n['invited']} invited',
    if ((n['member'] ?? 0) > 0) '${n['member']} already in it',
    if ((n['full'] ?? 0) > 0) '${n['full']} not added: the group is full',
    if ((n['unavailable'] ?? 0) > 0) '${n['unavailable']} not on Taskiem',
  ];
  return parts.isEmpty ? 'No one was added' : parts.join(', ');
}

/// A group message in a line: a list's last line, a quote, what is
/// forwarded.
String groupLine(GroupMessage m) {
  if (m.deleted == true) return 'This message was deleted';
  if (m.kind == 'event') return m.body;
  final c = m.card;
  if (c != null) return c.title;
  final a = m.attachment;
  if (a != null) {
    final what = switch (a.kind) {
      'photo' => 'Photo',
      'voice' => 'Voice note',
      _ => (a.name ?? '').isEmpty ? 'File' : a.name!,
    };
    return m.body.isEmpty || m.body == what || const {'Photo', 'Voice note', 'File'}.contains(m.body) ? what : '$what: ${m.body}';
  }
  final l = m.location;
  if (l != null) return (l.label ?? '').isEmpty ? 'A place' : l.label!;
  return m.body;
}

/// A group's line in the list: who wrote last and what, or that you left.
String groupListLine(Group g) {
  if (g.last.isEmpty) {
    if (g.left) return g.leftHow == 'removed' ? 'You were removed' : 'You left';
    return 'No messages yet';
  }
  final who = g.lastMine ? 'You' : (g.lastSender ?? '');
  return who.isEmpty || who == 'Taskiem' && g.last.startsWith('Taskiem') ? g.last : '$who: ${g.last}';
}

/// The line under a group's name in its head: who is typing, else how
/// many are in it (or that you left).
String groupHeadLine(Group? g, {List<String> typing = const [], bool offline = false}) {
  if (offline) return 'Waiting for network…';
  if (g == null) return '';
  if (typing.isNotEmpty) return typing.length == 1 ? '${typing.first} is typing…' : '${typing.length} people are typing…';
  if (g.left) return g.leftHow == 'removed' ? 'You were removed from this group' : 'You left this group';
  if (g.closed != null) return 'Closed by Taskiem’s moderators';
  return g.members == 1 ? '1 member' : '${g.members} members';
}

/// The code of a group's link the app opens (`/g/{code}`), or null.
String? groupLinkOf(String page) => RegExp(r'^/g/([A-Za-z0-9]{4,40})$').firstMatch(page)?[1];

/// The code of a group's link: `/g/{code}`, or a whole link to this
/// server; null when it is none (a bare code is a person's).
String? groupCodeOf(String typed) {
  final s = typed.trim();
  final path = RegExp(r'^/g/([A-Za-z0-9]{4,40})/?$').firstMatch(s);
  if (path != null) return path[1];
  final uri = Uri.tryParse(s);
  if (uri == null || !uri.hasScheme) return null;
  final home = Uri.tryParse(taskiemUrl);
  final m = RegExp(r'^/g/([A-Za-z0-9]{4,40})/?$').firstMatch(uri.path);
  if (m != null && (home == null || uri.host.toLowerCase() == home.host.toLowerCase())) return m[1];
  return null;
}

/// A group's photo's path (`/v1/me/groups/{id}/photo?v=…`), as
/// [personPhotoPath] reads a person's.
String? groupPhotoPath(Object? photo) => personPhotoPath(photo);

// --- one of a few choices, in the app's look ---

/// One of [choices] (value, words, a line under), the one on now ticked;
/// null when put away. With [icon], each row has that icon in place of a
/// tick: things to take one of (a saved person), not a setting.
Future<String?> pickOneOf(
  BuildContext context, {
  required String title,
  required List<(String, String, String)> choices,
  String? value,
  String? note,
  String? searchHint,
  String? icon,
}) => showTkSheet<String>(
  context,
  (context) => TkAppLook(
    child: _PickOne(title: title, choices: choices, value: value, note: note, searchHint: searchHint, icon: icon),
  ),
);

/// The words of a choice as searched: lowercase, split at anything but
/// letters and digits.
List<String> _searchWords(String s) => s.toLowerCase().split(RegExp('[^a-z0-9]+')).where((w) => w.isNotEmpty).toList();

/// Whether [words] (a choice's title) has every typed word at the start of
/// one of its words, or (three letters or more) within the whole run
/// together ("gtbank").
bool choiceMatches(String words, String typed) {
  final want = _searchWords(typed);
  if (want.isEmpty) return true;
  final have = _searchWords(words);
  final joined = have.join();
  // Within the name run together only for three letters or more, so
  // "fi" finds First, not Microfinance.
  return want.every((w) => have.any((x) => x.startsWith(w)) || (w.length >= 3 && joined.contains(w)));
}

/// pickOneOf's sheet: the choices as ticked rows, and with [searchHint] a
/// search box over them (for a long list, such as banks) that keeps the
/// chosen one.
class _PickOne extends StatefulWidget {
  const _PickOne({required this.title, required this.choices, this.value, this.note, this.searchHint, this.icon});
  final String title;
  final List<(String, String, String)> choices;
  final String? value;
  final String? note;
  final String? searchHint;
  final String? icon;

  @override
  State<_PickOne> createState() => _PickOneState();
}

class _PickOneState extends State<_PickOne> {
  final _q = TextEditingController();

  @override
  void initState() {
    super.initState();
    _q.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _q.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final shown = [
      for (final c in widget.choices)
        if (c.$1 == widget.value || choiceMatches(c.$2, _q.text)) c,
    ];
    return TkSheet(
      title: widget.title,
      body: ListView(
        padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
        children: [
          if (widget.searchHint != null) TkSearchBox(controller: _q, hint: widget.searchHint!, margin: const EdgeInsets.only(bottom: 12)),
          if (shown.isEmpty)
            const TkPanel(children: [TkEmpty(icon: 'search', title: 'Nothing has that name', line: 'Try fewer letters, or another part of its name.')])
          else
            TkPanel(
              children: [
                for (final (k, words, sub) in shown)
                  Semantics(
                    selected: k == widget.value,
                    child: TkListRow(
                      title: words,
                      sub: sub,
                      leading: widget.icon == null ? TkCheckbox(checked: k == widget.value) : null,
                      icon: widget.icon,
                      square: widget.icon == null ? null : TkLook.of(context).teal,
                      chevron: false,
                      onTap: () => Navigator.of(context).pop(k),
                    ),
                  ),
              ],
            ),
          if (widget.note != null) Padding(padding: const EdgeInsets.fromLTRB(4, 12, 4, 0), child: Text(widget.note!, style: tkHint(TkTheme.tokens(context)))),
        ],
      ),
    );
  }
}

// --- chats and groups to send to ---

/// What a forward picker answers: people chats and groups.
typedef ChatsAndGroups = ({List<String> chats, List<String> groups});

/// People chats and groups to forward to: up to [max] together, from the
/// one list (archived ones too; not a blocked, deleted or suspended
/// person's chat, nor a group you left, can't post in, or one closed);
/// null when put away.
Future<ChatsAndGroups?> pickChatsAndGroups(BuildContext context, Auth auth, {required String title, required String what, int max = 5, String? exceptChat, String? exceptGroup}) =>
    showTkSheet<ChatsAndGroups>(context, (_) => _Forward(auth: auth, title: title, what: what, max: max, exceptChat: exceptChat, exceptGroup: exceptGroup));

class _Forward extends StatefulWidget {
  const _Forward({required this.auth, required this.title, required this.what, required this.max, this.exceptChat, this.exceptGroup});
  final Auth auth;
  final String title;
  final String what;
  final int max;
  final String? exceptChat;
  final String? exceptGroup;

  @override
  State<_Forward> createState() => _ForwardState();
}

class _ForwardState extends State<_Forward> {
  List<ChatListItem>? _open;
  List<ChatListItem> _away = const [];
  String? _nextOpen;
  String? _nextAway;
  String? _error;
  final _picked = <String>[];

  @override
  void initState() {
    super.initState();
    _load();
  }

  bool _can(ChatListItem c) {
    if (c.kind == 'person') return !c.blocked && c.gone != true && c.suspended != true && c.id != widget.exceptChat;
    if (c.kind == 'group') {
      final g = c.group;
      return g != null && !g.left && g.canPost && g.closed == null && c.id != widget.exceptGroup;
    }
    return false;
  }

  List<ChatListItem>? get _rows {
    final open = _open;
    if (open == null) return null;
    final seen = <String>{};
    return [for (final c in [...open, ..._away]) if (_can(c) && seen.add(c.id)) c];
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listAllMyChats();
      ListAllMyChatsResponse? a;
      try {
        a = await widget.auth.api.listAllMyChats(archived: '1');
      } on Object {
        // The archived ones are left out this time.
      }
      if (!mounted) return;
      setState(() {
        _open = r.chats;
        _nextOpen = r.more ? r.next : null;
        _away = [...?a?.chats.where((c) => c.archived)];
        _nextAway = a != null && a.more ? a.next : null;
      });
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _more() async {
    try {
      if (_nextOpen != null) {
        final r = await widget.auth.api.listAllMyChats(before: _nextOpen);
        if (mounted) setState(() => (_open = [...?_open, ...r.chats], _nextOpen = r.more ? r.next : null));
      } else if (_nextAway != null) {
        final r = await widget.auth.api.listAllMyChats(archived: '1', before: _nextAway);
        if (mounted) setState(() => (_away = [..._away, ...r.chats.where((c) => c.archived)], _nextAway = r.more ? r.next : null));
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  void _toggle(String key) {
    setState(() {
      if (_picked.remove(key)) return;
      if (_picked.length >= widget.max) {
        _error = widget.max == 1 ? 'Choose one chat.' : 'At most ${widget.max} chats at once.';
        return;
      }
      _error = null;
      _picked.add(key);
    });
  }

  void _send() {
    Navigator.of(context).pop((
      chats: [for (final k in _picked) if (k.startsWith('person ')) k.substring(7)],
      groups: [for (final k in _picked) if (k.startsWith('group ')) k.substring(6)],
    ));
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final rows = _rows;
    return TkAppLook(
      child: TkSheet(
        title: widget.title,
        crumb: widget.max > 1 ? 'Up to ${widget.max} chats' : null,
        footer: TkCta(label: _picked.isEmpty ? 'Choose a chat' : 'Send', onTap: _picked.isEmpty ? null : _send),
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            Padding(padding: const EdgeInsets.fromLTRB(4, 0, 4, 10), child: Text('“${widget.what}”', maxLines: 2, overflow: TextOverflow.ellipsis, style: tkHint(t))),
            if (_error != null) TkErrorBox(_error!),
            if (rows == null && _error == null) const TkLoading(label: 'Loading…'),
            if (rows != null && rows.isEmpty) const TkPanel(children: [TkEmpty(icon: 'chat', title: 'No chats with people yet', line: 'Start one from New chat on your Chats.')]),
            if (rows != null && rows.isNotEmpty)
              TkPanel(
                inset: 66,
                children: [
                  for (final c in rows)
                    () {
                      final key = '${c.kind} ${c.id}';
                      final on = _picked.contains(key);
                      final group = c.kind == 'group';
                      return Semantics(
                        checked: on,
                        child: TkListRow(
                          title: c.name,
                          sub: group ? 'Group · ${c.group!.members} members' : '',
                          leading: TkCheckbox(checked: on),
                          face: PersonFace(name: c.name, transport: widget.auth.transport, photo: group ? c.group?.photo : c.person?.photo, size: 40),
                          semanticsLabel: '${c.name}${group ? ', a group' : ''}${on ? ', chosen' : ''}',
                          onTap: () => _toggle(key),
                        ),
                      );
                    }(),
                ],
              ),
            if (rows != null && (_nextOpen != null || _nextAway != null)) TkLoadMore(label: 'Show more chats', onMore: _more),
          ],
        ),
      ),
    );
  }
}

/// The words of a forward's answer: how many it went to, and the first
/// reason one was refused.
String forwardWords(ForwardMessageResponse r) {
  if (r.failed.isEmpty) return r.sent.length == 1 ? 'Forwarded' : 'Forwarded to ${r.sent.length} chats';
  final why = r.failed.first.error;
  return r.sent.isEmpty ? why : 'Forwarded to ${r.sent.length}. $why';
}

// --- people to add ---

/// Someone who can be added to a group: from your contacts or your chats
/// with people.
typedef GroupCandidate = ({String id, String name, Object? photo, bool contact});

/// People to add to a group, from your contacts and your chats with
/// people ([except]: in it already; [most]: how many may be picked);
/// null when put away.
Future<List<GroupCandidate>?> pickGroupPeople(BuildContext context, Auth auth, {required String title, String action = 'Add', Set<String> except = const {}, int most = groupMost - 1, List<GroupCandidate> picked = const []}) =>
    showTkSheet<List<GroupCandidate>>(context, (_) => _People(auth: auth, title: title, action: action, except: except, most: most, picked: picked));

class _People extends StatefulWidget {
  const _People({required this.auth, required this.title, required this.action, required this.except, required this.most, required this.picked});
  final Auth auth;
  final String title;
  final String action;
  final Set<String> except;
  final int most;
  final List<GroupCandidate> picked;

  @override
  State<_People> createState() => _PeopleState();
}

class _PeopleState extends State<_People> {
  List<GroupCandidate>? _all;
  String? _error;
  late final _picked = <String, GroupCandidate>{for (final p in widget.picked) p.id: p};
  final _q = TextEditingController();

  @override
  void initState() {
    super.initState();
    _q.addListener(() => setState(() {}));
    _load();
  }

  @override
  void dispose() {
    _q.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final out = <String, GroupCandidate>{};
    Object? failed;
    try {
      final r = await widget.auth.api.myContacts();
      for (final c in r.contacts) {
        if (c.deleted || c.suspended == true) continue;
        final named = c.contactName is String && (c.contactName as String).isNotEmpty ? c.contactName as String : c.name;
        out[c.id] = (id: c.id, name: named, photo: c.photo, contact: true);
      }
    } on Object catch (e) {
      failed = e;
    }
    try {
      final r = await widget.auth.api.listPeopleChats();
      for (final c in r.chats) {
        if (c.gone || c.blocked || c.request || c.suspended == true) continue;
        out.putIfAbsent(c.personId, () => (id: c.personId, name: c.name, photo: c.person?.photo, contact: c.person?.inContacts == true));
      }
    } on Object catch (e) {
      failed ??= e;
    }
    if (!mounted) return;
    setState(() {
      _all = (out.values.where((p) => !widget.except.contains(p.id)).toList()..sort((a, b) => a.name.toLowerCase().compareTo(b.name.toLowerCase())));
      if (out.isEmpty && failed != null) _error = sayFailure(failed);
    });
  }

  void _toggle(GroupCandidate p) {
    setState(() {
      if (_picked.remove(p.id) != null) return;
      if (_picked.length >= widget.most) {
        _error = widget.most <= 0 ? 'The group is full.' : 'A group holds $groupMost people at most.';
        return;
      }
      _error = null;
      _picked[p.id] = p;
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final q = _q.text.trim().toLowerCase();
    final all = _all;
    final shown = all?.where((p) => q.isEmpty || p.name.toLowerCase().contains(q)).toList();
    return TkAppLook(
      child: TkSheet(
        title: widget.title,
        crumb: _picked.isEmpty ? 'Your contacts and chats' : '${_picked.length} chosen',
        footer: TkCta(label: _picked.isEmpty ? 'Choose people' : '${widget.action} (${_picked.length})', onTap: _picked.isEmpty ? null : () => Navigator.of(context).pop(_picked.values.toList())),
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            TkSearchBox(controller: _q, hint: 'Search your contacts', margin: const EdgeInsets.only(bottom: 12), fill: TkLook.of(context).card),
            if (_error != null) TkErrorBox(_error!),
            if (all == null && _error == null) const TkLoading(label: 'Loading…'),
            if (all != null && all.isEmpty)
              const TkPanel(children: [TkEmpty(icon: 'b-people', title: 'No one to add yet', line: 'Add people to your contacts, or chat with them first.')]),
            if (shown != null && shown.isNotEmpty)
              TkPanel(
                inset: 66,
                children: [
                  for (final p in shown)
                    Semantics(
                      checked: _picked.containsKey(p.id),
                      child: TkListRow(
                        title: p.name,
                        sub: p.contact ? 'In your contacts' : 'You chat with them',
                        leading: TkCheckbox(checked: _picked.containsKey(p.id)),
                        face: PersonFace(name: p.name, transport: widget.auth.transport, photo: p.photo, size: 40),
                        semanticsLabel: '${p.name}${_picked.containsKey(p.id) ? ', chosen' : ''}',
                        onTap: () => _toggle(p),
                      ),
                    ),
                ],
              ),
            if (all != null && all.isNotEmpty && shown!.isEmpty) Padding(padding: const EdgeInsets.all(8), child: Text('No one by that name.', style: tkHint(t))),
            Padding(
              padding: const EdgeInsets.fromLTRB(4, 12, 4, 0),
              child: Text('People you have as contacts are added if their settings let you; anyone else gets an invite in their Requests.', style: tkHint(t)),
            ),
          ],
        ),
      ),
    );
  }
}

/// How each add went, person by person, in the server's words alone.
Future<void> showGroupAdded(BuildContext context, List<GroupAdded> added, Map<String, String> names) => showTkSheet<void>(
  context,
  (context) => TkAppLook(
    child: TkSheet(
      title: groupAddedSummary(added),
      footer: TkCta(label: 'Done', onTap: () => Navigator.of(context).pop()),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
        children: [
          TkPanel(
            children: [
              for (final a in added)
                TkListRow(
                  title: names[a.personId] ?? 'Someone',
                  sub: groupAddedWords(a.state),
                  icon: switch (a.state) {
                    'added' || 'member' => 'b-check',
                    'invited' => 'b-mail',
                    _ => 'b-x',
                  },
                  tone: a.state == 'full' || a.state == 'unavailable' ? CardTone.warn : null,
                ),
            ],
          ),
        ],
      ),
    ),
  ),
);

/// A small round mark before a group's name in lists (a group, not a
/// person).
class GroupMark extends StatelessWidget {
  const GroupMark({super.key});

  @override
  Widget build(BuildContext context) => ExcludeSemantics(child: TkIcon('b-people', size: 14, stroke: 2, color: TkTheme.tokens(context).muted));
}

/// Waits a moment, then [f] (a burst of live events read once).
Timer soonOnce(Timer? was, VoidCallback f, [Duration d = const Duration(milliseconds: 300)]) {
  was?.cancel();
  return Timer(d, f);
}

// --- asking first, and a few actions, in the app's look ---

/// Asks first, in a sheet of the app's own look; true when the person
/// agrees.
Future<bool> askSure(BuildContext context, {required String title, String? body, required String ok, String cancel = 'Cancel', bool danger = false}) async {
  final yes = await showTkSheet<bool>(
    context,
    (context) => TkAppLook(
      child: TkSheet(
        title: title,
        bareFooter: true,
        footer: Row(
          children: [
            Expanded(child: TkGhostButton(label: cancel, onTap: () => Navigator.of(context).pop(false))),
            const SizedBox(width: 10),
            Expanded(child: TkCta(label: ok, danger: danger, onTap: () => Navigator.of(context).pop(true))),
          ],
        ),
        body: ListView(
          shrinkWrap: true,
          padding: const EdgeInsets.fromLTRB(18, 0, 18, 16),
          children: [if (body != null) Text(body, style: tkText(size: 15, lineHeight: 1.45, color: TkTheme.tokens(context).text))],
        ),
      ),
    ),
  );
  return yes ?? false;
}

/// One of a few actions (key, words, icon, danger), as rows on a card;
/// [note] under them; null when put away.
Future<String?> pickAction(BuildContext context, {required String title, required List<(String, String, String, bool)> actions, String? note}) => showTkSheet<String>(
  context,
  (context) {
    final look = TkLook.of(context);
    return TkAppLook(
      child: TkSheet(
        title: title,
        body: ListView(
          shrinkWrap: true,
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            if (note != null) Padding(padding: const EdgeInsets.fromLTRB(4, 0, 4, 12), child: Text(note, style: tkHint(TkTheme.tokens(context)))),
            TkSection(
              inset: 56,
              children: [
                for (final (k, words, icon, danger) in actions)
                  TkListRow(title: words, icon: icon, square: danger ? look.red : look.teal, onTap: () => Navigator.of(context).pop(k)),
              ],
            ),
          ],
        ),
      ),
    );
  },
);
