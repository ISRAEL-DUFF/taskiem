// Making a group, joining one by its link, and the invites waiting
// (docs/spec/people-chatting.md §9; the 5b checklist A and the founder's A3,
// A4): a new group's name, photo and description and the people to add
// (each add's answer in the server's words alone: added, invited, already
// in it, full, not on Taskiem); a group's link (`/g/{code}`) showing its
// name, photo and how many are in it before joining, then joined at once
// or waiting for an admin (and taken back); and invites in Requests,
// accepted or declined (the inviter is not told a decline).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart' show PickedFile;
import 'composer.dart' show Pickers;
import 'groups.dart';
import 'my_chat.dart' show sayFailure;
import 'people.dart';
import 'people_live.dart';
import 'pick_problem.dart';
import 'words.dart';

// --- a new group ---

/// New group: its name, photo and description, and the people to add.
/// Made, the adds' answers are said, then the group opens.
class NewGroupScreen extends StatefulWidget {
  const NewGroupScreen({super.key, required this.auth, required this.openGroup, this.pickers = const Pickers()});
  final Auth auth;
  final OpenGroup openGroup;
  final Pickers pickers;

  @override
  State<NewGroupScreen> createState() => _NewGroupScreenState();
}

class _NewGroupScreenState extends State<NewGroupScreen> {
  final _name = TextEditingController();
  final _about = TextEditingController();
  List<GroupCandidate> _people = const [];
  PickedFile? _photo;
  String? _error;
  bool _busy = false;

  @override
  void dispose() {
    _name.dispose();
    _about.dispose();
    super.dispose();
  }

  Future<void> _pick() async {
    final picked = await pickGroupPeople(context, widget.auth, title: 'Add people', action: 'Choose', picked: _people);
    if (picked != null && mounted) setState(() => _people = picked);
  }

  Future<void> _choosePhoto() async {
    try {
      final f = await widget.pickers.photo(side: 1024);
      if (f != null && mounted) setState(() => _photo = f);
    } on Object catch (e) {
      if (mounted) sayPickProblem(context, e, camera: false);
    }
  }

  Future<void> _make() async {
    final name = _name.text.trim();
    if (name.isEmpty) return setState(() => _error = 'Give the group a name.');
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.auth.api.createGroup(
        body: CreateGroupRequest(name: name, description: _about.text.trim().isEmpty ? null : _about.text.trim(), personIds: _people.isEmpty ? null : [for (final p in _people) p.id]),
      );
      final f = _photo;
      if (f != null) {
        try {
          await widget.auth.transport.upload('/v1/me/groups/${Uri.encodeComponent(r.group.id)}/photo', bytes: f.bytes, filename: f.name, type: f.type, method: 'PUT');
        } on Object catch (e) {
          if (mounted) showTkToast(context, 'The group is made, but its photo didn’t go: ${sayFailure(e)} Try again from its info.', error: true);
        }
      }
      if (!mounted) return;
      if (r.added.isNotEmpty) await showGroupAdded(context, r.added, {for (final p in _people) p.id: p.name});
      if (!mounted) return;
      Navigator.of(context).pop();
      unawaited(widget.openGroup(r.group.id));
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final photo = _photo;
    return TkScreen(
      title: 'New group',
      children: [
        TkTopCard(
          child: Column(
            children: [
              Semantics(
                button: true,
                label: photo == null ? 'Add a photo for the group' : 'Change the group’s photo',
                child: GestureDetector(
                  onTap: _busy ? null : _choosePhoto,
                  child: photo != null
                      ? ClipOval(child: Image.memory(photo.bytes, width: 88, height: 88, fit: BoxFit.cover, excludeFromSemantics: true))
                      : TkSquare('b-camera', color: look.violet, size: 88),
                ),
              ),
              const SizedBox(height: 8),
              Text(photo == null ? 'Add a photo' : 'Change the photo', style: tkText(size: 13, weight: 600, color: look.limeInk)),
            ],
          ),
        ),
        TkFormGroup(
          children: [
            TkKitField(label: 'Name', child: TkTextBox(key: const ValueKey('group-name'), controller: _name, max: 80)),
            TkKitField(label: 'Description', hint: 'Optional: what the group is for', child: TkTextBox(controller: _about, lines: 3, max: 500)),
          ],
        ),
        TkSection(
          title: _people.isEmpty ? 'People' : '${_people.length} ${_people.length == 1 ? 'person' : 'people'}',
          children: [
            TkListRow(title: _people.isEmpty ? 'Add people' : 'Add or remove people', sub: 'From your contacts and chats', icon: 'b-plus', square: look.teal, onTap: _busy ? null : _pick),
            for (final p in _people)
              TkListRow(
                title: p.name,
                sub: p.contact ? 'In your contacts' : 'You chat with them',
                face: PersonFace(name: p.name, transport: widget.auth.transport, photo: p.photo, size: 40),
                trailing: TkSmallButton(label: 'Remove', semantics: 'Remove ${p.name}', onPressed: _busy ? null : () => setState(() => _people = [..._people.where((x) => x.id != p.id)])),
              ),
          ],
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Text('People you have as contacts are added if their settings let you; anyone else gets an invite in their Requests. A group holds $groupMost people.', style: tkHint(t)),
        ),
        if (_error != null) TkErrorBox(_error!),
        TkCta(label: _busy ? 'Making the group…' : 'Make the group', icon: 'b-people', onTap: _busy ? null : _make),
        Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: Text(groupKeptWords, style: tkText(size: 12.5, lineHeight: 1.4, color: t.muted))),
      ],
    );
  }
}

// --- joining by a link ---

/// The words for a group's link that cannot be opened or joined.
String groupLinkProblem(Object e) {
  if (e is ApiError) {
    if (e.status == 410) return 'This link was replaced or turned off. Ask someone in the group for the new one.';
    if (e.status == 404) return 'That isn’t a group’s Taskiem link.';
    if (e.status == 503) return 'This group can’t be joined right now. Try again later.';
  }
  return sayFailure(e);
}

/// A group's link opened (`/g/{code}`, a scanned QR code): its name,
/// photo and how many are in it; Join, then the group (or waiting for an
/// admin, which can be taken back).
class GroupJoinScreen extends StatefulWidget {
  const GroupJoinScreen({super.key, required this.auth, required this.code, required this.openGroup});
  final Auth auth;
  final String code;
  final OpenGroup openGroup;

  @override
  State<GroupJoinScreen> createState() => _GroupJoinScreenState();
}

class _GroupJoinScreenState extends State<GroupJoinScreen> {
  GroupLinkPreviewResponse? _g;
  String? _error;
  bool _busy = false;
  bool _waiting = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final g = await widget.auth.api.groupLinkPreview(code: widget.code);
      if (mounted) setState(() => (_g = g, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = groupLinkProblem(e));
    }
  }

  Future<void> _join() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = await widget.auth.api.joinGroupByLink(code: widget.code);
      if (!mounted) return;
      if (r.state == 'waiting') {
        setState(() => (_busy = false, _waiting = true));
        return;
      }
      final id = r.group?.id;
      Navigator.of(context).pop();
      if (id != null) unawaited(widget.openGroup(id));
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = groupLinkProblem(e)));
    }
  }

  Future<void> _withdraw() async {
    setState(() => _busy = true);
    try {
      await widget.auth.api.withdrawGroupJoin(code: widget.code);
      if (!mounted) return;
      setState(() => (_busy = false, _waiting = false));
      showTkToast(context, 'Your request to join is taken back');
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = groupLinkProblem(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final g = _g;
    return TkScreen(
      title: 'Join a group',
      onRefresh: _load,
      children: [
        if (g == null && _error == null) const TkLoading(label: 'Loading…'),
        if (g != null)
          TkTopCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Center(child: PersonFace(name: g.name, transport: widget.auth.transport, photo: g.photo, size: 96, letterSize: 32)),
                const SizedBox(height: 12),
                Semantics(header: true, child: Text(g.name, textAlign: TextAlign.center, style: tkDisplay(size: 22, color: t.text))),
                const SizedBox(height: 2),
                Text('Group · ${g.members == 1 ? '1 member' : '${g.members} members'}', textAlign: TextAlign.center, style: tkHint(t)),
                const SizedBox(height: 16),
                if (_waiting) ...[
                  TkCallout(
                    leading: TkSquare('b-clock', color: look.orange),
                    title: 'Waiting for an admin to let you in',
                    sub: 'You’ll find the group in your chats once they do.',
                    color: look.orange,
                  ),
                  const SizedBox(height: 10),
                  TkSmallButton(label: 'Take back my request', onPressed: _busy ? null : _withdraw),
                ] else if (g.full)
                  TkCallout(leading: TkSquare('b-people', color: look.red), title: 'This group is full', sub: 'A group holds $groupMost people.', color: look.red)
                else
                  TkCta(label: _busy ? 'Joining…' : 'Join the group', icon: 'b-people', onTap: _busy ? null : _join),
              ],
            ),
          ),
        if (_error != null) TkErrorBox(_error!),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Text('Members see your name and photo as your settings allow, and messages from when you join. Your number stays hidden unless you chose to show it.', style: tkHint(t)),
        ),
      ],
    );
  }
}

// --- invites waiting, in Requests ---

/// The invites to groups waiting for you (A3): Accept opens the group;
/// Decline is not told to the one who invited you.
class GroupInvitesSection extends StatefulWidget {
  const GroupInvitesSection({super.key, required this.auth, required this.openGroup, this.live, this.now = DateTime.now});
  final Auth auth;
  final OpenGroup openGroup;
  final PeopleLive? live;
  final DateTime Function() now;

  @override
  State<GroupInvitesSection> createState() => _GroupInvitesSectionState();
}

class _GroupInvitesSectionState extends State<GroupInvitesSection> {
  List<GroupInvite>? _invites;
  String? _error;
  final _busy = <String>{};
  StreamSubscription<GroupLiveEvent>? _events;
  Timer? _soon;

  @override
  void initState() {
    super.initState();
    _load();
    _events = (widget.live ?? PeopleLive.of(widget.auth))?.groupEvents.listen((e) {
      // An invite gone (taken back, answered on another device): out at once.
      if (e is GroupLiveInvite && e.invite == null && mounted) setState(() => _invites = _invites?.where((i) => i.groupId != e.groupId).toList());
      if (e is GroupLiveInvite || (_invites?.any((i) => i.groupId == e.groupId) ?? false)) _soon = soonOnce(_soon, _load);
    });
  }

  @override
  void dispose() {
    _events?.cancel();
    _soon?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listGroupInvites();
      if (mounted) setState(() => (_invites = r.invites, _error = null));
    } on ApiError catch (e) {
      // A server without groups: no invites.
      if (mounted) setState(() => e.status == 404 ? _invites = const [] : _error = sayFailure(e));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _accept(GroupInvite i) async {
    setState(() => _busy.add(i.groupId));
    try {
      final g = await widget.auth.api.acceptGroupInvite(id: i.groupId);
      if (!mounted) return;
      await _load();
      unawaited(widget.openGroup(g.id));
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
      // Gone already (taken back, answered on another device): read again.
      if (e is ApiError && e.status == 404) await _load();
    }
    if (mounted) setState(() => _busy.remove(i.groupId));
  }

  Future<void> _decline(GroupInvite i) async {
    final yes = await askSure(context, title: 'Decline the invite to ${i.name}?', body: '${i.by} is not told.', ok: 'Decline', danger: true);
    if (!yes || !mounted) return;
    setState(() => _busy.add(i.groupId));
    try {
      await widget.auth.api.declineGroupInvite(id: i.groupId);
      await _load();
      if (mounted) showTkToast(context, 'Declined');
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
      if (e is ApiError && e.status == 404) await _load();
    }
    if (mounted) setState(() => _busy.remove(i.groupId));
  }

  @override
  Widget build(BuildContext context) {
    final list = _invites;
    if (_error != null) return TkErrorBox(_error!);
    if (list == null || list.isEmpty) return const SizedBox.shrink();
    return TkSection(
      title: 'Invites to groups',
      count: list.length,
      children: [
        for (final i in list)
          TkListRow(
            key: ValueKey('invite ${i.groupId}'),
            title: i.name,
            sub: '${i.by} invited you · ${dayWords(i.at, widget.now())}',
            face: PersonFace(name: i.name, transport: widget.auth.transport, photo: i.photo, size: 40),
            trailing: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                TkSmallButton(label: 'Accept', semantics: 'Accept the invite to ${i.name}', primary: true, onPressed: _busy.contains(i.groupId) ? null : () => _accept(i)),
                const SizedBox(width: 6),
                TkSmallButton(label: 'Decline', semantics: 'Decline the invite to ${i.name}', onPressed: _busy.contains(i.groupId) ? null : () => _decline(i)),
              ],
            ),
          ),
      ],
    );
  }
}
