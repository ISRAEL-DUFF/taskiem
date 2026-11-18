// A group's info (docs/spec/people-chatting.md §9, §8.3; the 5b checklist
// A, B and the founder's A2, A4, A7, A8): its photo, name and description
// (edited by admins, or by everyone when A2's switch lets them); its members
// and admins, each as the person sees them; for admins, adding people (each
// answer said in the server's words alone), making and unmaking admins,
// removing people, who can post, who can edit the info, the invite link
// (on, replaced, off, its QR code, shared) with "Approve new members" (A4)
// and the people waiting; for everyone, mute, mentions while muted (A7),
// archive, leave, delete, block a member (A8), and report the group or a
// member with consent. A closed group says so, and its admins can appeal.
//
// Groups are kept by Taskiem and readable by its servers; this screen says
// so plainly, and nothing says they are end-to-end encrypted.

import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:share_plus/share_plus.dart';

import '../api/api.g.dart';
import '../app/lock.dart' show stepOut;
import '../kit/api_image.dart';
import '../kit/forms.dart' show TkSwitch;
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
import 'group_chat.dart';
import 'group_media.dart';
import 'groups.dart';
import 'my_chat.dart' show sayFailure;
import 'people.dart';
import 'people_find.dart' show OpenPeopleChat, chatWithPerson;
import 'people_standing.dart' show appealWords;
import 'pick_problem.dart';
import 'words.dart';

/// The group's info.
class GroupInfoScreen extends StatefulWidget {
  const GroupInfoScreen({super.key, required this.data, this.onClosed, this.openPerson, this.onJump, this.pickers = const Pickers()});
  final GroupChatData data;

  /// The group is gone for you (left as the last one, deleted): the
  /// group closes too.
  final VoidCallback? onClosed;

  /// Opens a one-to-one chat (a member's Message).
  final OpenPeopleChat? openPerson;

  /// Back to the chat, at one of its messages (an item of its media);
  /// without it, the info closes and the group goes to the message.
  final ValueChanged<String>? onJump;
  final Pickers pickers;

  @override
  State<GroupInfoScreen> createState() => _GroupInfoScreenState();
}

class _GroupInfoScreenState extends State<GroupInfoScreen> {
  GroupChatData get d => widget.data;

  @override
  void initState() {
    super.initState();
    unawaited(d.readMembers());
  }

  void _jump(String message) {
    final to = widget.onJump;
    if (to != null) return to(message);
    // Back past this info (and the media list over it) to the chat.
    final nav = Navigator.of(context);
    final info = ModalRoute.of(context);
    if (info != null) nav.popUntil((r) => r == info);
    unawaited(nav.maybePop());
    unawaited(d.jump(message));
  }

  void _say(bool ok, String done) {
    if (!mounted) return;
    showTkToast(context, ok ? done : (d.actionError ?? 'Something went wrong. Try again.'), error: !ok);
  }

  Future<void> _photo() async {
    final g = d.group!;
    final has = groupPhotoPath(g.photo) != null;
    final what = has
        ? await pickAction(context, title: 'Group photo', actions: [('new', 'Choose a new photo', 'b-camera', false), ('remove', 'Remove the photo', 'b-x', true)])
        : 'new';
    if (what == null || !mounted) return;
    if (what == 'remove') return _say(await d.removePhoto(), 'Photo removed');
    final PickedFile? f;
    try {
      f = await widget.pickers.photo(side: 1024);
    } on Object catch (e) {
      if (mounted) sayPickProblem(context, e, camera: false);
      return;
    }
    if (f == null || !mounted) return;
    _say(await d.setPhoto(f), 'Photo changed');
  }

  Future<void> _editInfo() async {
    final g = d.group!;
    final saved = await showTkSheet<bool>(context, (_) => _InfoForm(data: d, group: g));
    if (saved == true && mounted) showTkToast(context, 'Saved');
  }

  Future<void> _add() async {
    final g = d.group!;
    final here = {...?d.members?.map((m) => m.personId), ...d.invited.map((p) => p.personId)};
    final picked = await pickGroupPeople(context, d.auth, title: 'Add people', except: here, most: groupMost - g.members);
    if (picked == null || picked.isEmpty || !mounted) return;
    final added = await d.add([for (final p in picked) p.id]);
    if (!mounted) return;
    if (added == null) return _say(false, '');
    await showGroupAdded(context, added, {for (final p in picked) p.id: p.name});
  }

  Future<void> _member(GroupMember m) async {
    if (m.you) return;
    final g = d.group!;
    final admin = isAdminOf(g) && g.closed == null;
    final pick = await pickAction(
      context,
      title: m.name,
      actions: [
        if (widget.openPerson != null && m.person?.deleted != true) ('message', 'Message ${m.name}', 'b-chat', false),
        if (admin) m.role == 'admin' ? ('unadmin', 'Remove as admin', 'b-person', false) : ('admin', 'Make admin', 'b-shield', false),
        if (admin) ('remove', 'Remove from the group', 'b-out', true),
        if (m.person?.deleted != true) ('block', 'Block ${m.name}', 'b-x-ring', true),
        ('report', 'Report ${m.name}', 'b-alert', true),
      ],
    );
    if (pick == null || !mounted) return;
    switch (pick) {
      case 'message':
        await chatWithPerson(context, d.auth, widget.openPerson!, person: m.personId);
      case 'admin':
        _say(await d.setAdmin(m.personId, true), '${m.name} is an admin now');
      case 'unadmin':
        _say(await d.setAdmin(m.personId, false), '${m.name} is no longer an admin');
      case 'remove':
        final yes = await askSure(context, title: 'Remove ${m.name}?', body: 'They keep what they saw until now, but see nothing new and can’t write.', ok: 'Remove', danger: true);
        if (yes) _say(await d.remove(m.personId), '${m.name} removed');
      case 'block':
        await blockMemberFlow(context, d, m.personId, m.name);
      case 'report':
        await reportGroupFlow(context, d, person: m.personId, name: m.name);
    }
  }

  Future<void> _invited(GroupPending p) async {
    final yes = await askSure(context, title: 'Take back the invite to ${p.name}?', body: 'It goes from their Requests.', ok: 'Take it back', danger: true);
    if (yes) _say(await d.cancelInvite(p.personId), 'Invite taken back');
  }

  Future<void> _who(String title, String now, List<(String, String, String)> choices, PutGroupRequest Function(String v) body) async {
    final v = await pickOneOf(context, title: title, value: now, choices: choices);
    if (v == null || v == now || !mounted) return;
    _say(await d.edit(body(v)), 'Saved');
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return ListenableBuilder(
      listenable: d,
      builder: (context, _) {
        final g = d.group;
        if (g == null) return const TkScreen(title: 'Group info', children: [TkLoading(label: 'Loading…')]);
        final admin = isAdminOf(g);
        final open = g.closed == null && !g.left;
        final members = [...?d.members]..sort((a, b) {
          if (a.you != b.you) return a.you ? -1 : 1;
          if (a.role != b.role) return a.role == 'admin' ? -1 : 1;
          return a.name.toLowerCase().compareTo(b.name.toLowerCase());
        });
        Widget toggle(String title, String sub, bool on, ValueChanged<bool>? onChanged) => TkListRow(
          title: title,
          sub: sub,
          titleLines: 2,
          trailing: TkSwitch(label: title, on: on, onChanged: onChanged),
        );
        return TkScreen(
          title: 'Group info',
          onRefresh: d.refresh,
          children: [
            TkTopCard(
              child: Column(
                children: [
                  Semantics(
                    button: g.canEdit,
                    label: g.canEdit ? 'Change the group’s photo' : null,
                    child: GestureDetector(
                      onTap: g.canEdit ? _photo : null,
                      child: PersonFace(name: g.name, transport: d.auth.transport, photo: g.photo, size: 96, letterSize: 32),
                    ),
                  ),
                  const SizedBox(height: 12),
                  Semantics(header: true, child: Text(g.name, textAlign: TextAlign.center, style: tkDisplay(size: 22, color: t.text))),
                  const SizedBox(height: 2),
                  Text(g.left ? (g.leftHow == 'removed' ? 'You were removed' : 'You left') : 'Group · ${g.members == 1 ? '1 member' : '${g.members} members'}', style: tkHint(t)),
                  if (g.description.isNotEmpty) ...[const SizedBox(height: 10), Text(g.description, textAlign: TextAlign.center, style: tkText(size: 14.5, lineHeight: 1.45, color: t.text))],
                  if (open && (admin || g.canEdit)) ...[
                    const SizedBox(height: 14),
                    TkButtonRow([
                      if (admin) TkSmallButton(label: 'Add people', icon: 'b-plus', primary: true, onPressed: _add),
                      if (admin) TkSmallButton(label: 'Invite link', icon: 'b-qr', onPressed: () => tkPush<void>(context, GroupLinkScreen(data: d))),
                      if (g.canEdit) TkSmallButton(label: 'Edit', onPressed: _editInfo),
                    ]),
                  ],
                ],
              ),
            ),
            if (g.closed case final closed?)
              TkCallout(
                leading: TkSquare('b-shield', color: look.red),
                title: closed.words,
                sub: closed.appeal != null ? appealWords(closed.appeal!) : null,
                color: look.red,
                button: admin && closed.canAppeal && closed.appeal == null ? 'Appeal' : null,
                onPressed: () => appealGroupFlow(context, d),
              ),
            if (admin && open && d.waiting.isNotEmpty)
              TkSection(
                title: 'Waiting to join',
                count: d.waiting.length,
                children: [
                  for (final p in d.waiting)
                    TkListRow(
                      title: p.name,
                      sub: 'Asked ${dayWords(p.at, d.now())}',
                      letters: p.name,
                      trailing: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          TkSmallButton(label: 'Let in', semantics: 'Let ${p.name} in', primary: true, onPressed: () async => _say(await d.approve(p.personId), '${p.name} joined')),
                          const SizedBox(width: 6),
                          TkSmallButton(label: 'Decline', semantics: 'Turn ${p.name} away', onPressed: () async => _say(await d.declineJoin(p.personId), 'Turned away. They are not told why.')),
                        ],
                      ),
                    ),
                ],
              ),
            GroupMediaSection(data: d, onJump: _jump),
            if (!g.left)
              TkSection(
                title: 'Notifications',
                children: [
                  toggle('Mute', 'No notifications for new messages', g.muted, (v) async => _say(await d.put(muted: v), v ? 'Muted' : 'Notifications on')),
                  toggle('Mentions while muted', 'Tell me when I’m mentioned, even when muted', g.mentions, (v) async => _say(await d.put(mentions: v), 'Saved')),
                ],
              ),
            if (admin && open)
              TkSection(
                title: 'Settings',
                children: [
                  TkListRow(
                    title: 'Who can send messages',
                    sub: g.adminsOnlyPost ? 'Only admins' : 'Everyone',
                    icon: 'b-chat',
                    square: look.teal,
                    onTap: () => _who('Who can send messages', g.adminsOnlyPost ? 'admins' : 'everyone', const [
                      ('everyone', 'Everyone', 'Every member can write'),
                      ('admins', 'Only admins', 'The rest read'),
                    ], (v) => PutGroupRequest(adminsOnlyPost: v == 'admins')),
                  ),
                  TkListRow(
                    title: 'Who can edit the info',
                    sub: g.adminsOnlyEdit ? 'Only admins' : 'Everyone',
                    icon: 'b-note',
                    square: look.blue,
                    onTap: () => _who('Who can edit the name, photo and description', g.adminsOnlyEdit ? 'admins' : 'everyone', const [
                      ('admins', 'Only admins', 'As a new group starts'),
                      ('everyone', 'Everyone', 'Every member can change them'),
                    ], (v) => PutGroupRequest(adminsOnlyEdit: v == 'admins')),
                  ),
                  toggle('Approve new members', 'People joining by the link wait for an admin', g.approveJoins, (v) async => _say(await d.edit(PutGroupRequest(approveJoins: v)), 'Saved')),
                ],
              ),
            if (d.members != null)
              TkSection(
                title: g.members == 1 ? '1 member' : '${g.members} members',
                children: [
                  for (final m in members)
                    TkListRow(
                      title: m.you ? 'You' : m.name,
                      sub: [if (m.role == 'admin') 'Admin', if (m.phone != null && m.phone!.isNotEmpty) m.phone!].join(' · '),
                      face: PersonFace(name: m.name, transport: d.auth.transport, photo: m.person?.photo, size: 40),
                      semanticsLabel: '${m.you ? 'You' : m.name}${m.role == 'admin' ? ', admin' : ''}',
                      onTap: m.you ? null : () => _member(m),
                    ),
                ],
              ),
            if (admin && open && d.invited.isNotEmpty)
              TkSection(
                title: 'Invited',
                count: d.invited.length,
                children: [
                  for (final p in d.invited)
                    TkListRow(title: p.name, sub: 'Invited ${dayWords(p.at, d.now())}: in their Requests', icon: 'b-mail', onTap: () => _invited(p)),
                ],
              ),
            TkSection(
              inset: 56,
              children: [
                TkListRow(
                  title: g.archived ? 'Unarchive' : 'Archive',
                  icon: 'b-box',
                  square: look.teal,
                  onTap: () async => _say(await d.put(archived: !g.archived), g.archived ? 'Brought back to the chats' : 'Archived'),
                ),
                TkListRow(title: 'Report this group', sub: 'The last 20 messages go to Taskiem’s moderators, if you agree', icon: 'b-alert', square: look.red, onTap: () => reportGroupFlow(context, d)),
                if (!g.left) TkListRow(title: 'Leave this group', icon: 'b-out', square: look.red, onTap: () => leaveGroupFlow(context, d, onClosed: widget.onClosed)),
                TkListRow(title: 'Delete this group', sub: g.left ? 'For good, on every phone you use' : 'Your copy up to now', icon: 'b-x', square: look.red, onTap: () => deleteGroupFlow(context, d, onClosed: widget.onClosed)),
              ],
            ),
            Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: Text(groupKeptWords, style: tkText(size: 13, lineHeight: 1.4, color: t.muted))),
          ],
        );
      },
    );
  }
}


/// The name and the description, edited.
class _InfoForm extends StatefulWidget {
  const _InfoForm({required this.data, required this.group});
  final GroupChatData data;
  final Group group;

  @override
  State<_InfoForm> createState() => _InfoFormState();
}

class _InfoFormState extends State<_InfoForm> {
  late final _name = TextEditingController(text: widget.group.name);
  late final _about = TextEditingController(text: widget.group.description);
  String? _error;
  bool _busy = false;

  @override
  void dispose() {
    _name.dispose();
    _about.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final name = _name.text.trim();
    if (name.isEmpty) return setState(() => _error = 'Give the group a name.');
    setState(() => (_busy = true, _error = null));
    final ok = await widget.data.edit(PutGroupRequest(name: name, description: _about.text.trim()));
    if (!mounted) return;
    if (ok) return Navigator.of(context).pop(true);
    setState(() => (_busy = false, _error = widget.data.actionError));
  }

  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: 'Edit the group',
    action: _busy ? 'Saving…' : 'Save',
    onAction: _busy ? null : _save,
    children: [
      TkFormGroup(
        children: [
          TkKitField(label: 'Name', child: TkTextBox(controller: _name, max: 80)),
          TkKitField(label: 'Description', hint: 'Optional: what the group is for', child: TkTextBox(controller: _about, lines: 3, max: 500)),
        ],
      ),
      if (_error != null) TkErrorBox(_error!),
    ],
  );
}

// --- the invite link ---

/// The group's invite link (admins): on, replaced, off; its QR code and
/// sharing; "Approve new members" (A4).
class GroupLinkScreen extends StatefulWidget {
  const GroupLinkScreen({super.key, required this.data});
  final GroupChatData data;

  @override
  State<GroupLinkScreen> createState() => _GroupLinkScreenState();
}

class _GroupLinkScreenState extends State<GroupLinkScreen> {
  GroupLink? _link;
  String? _error;
  bool _busy = false;
  bool _copied = false;

  GroupChatData get d => widget.data;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final l = await d.api.groupLink(id: d.id);
      if (mounted) setState(() => (_link = l, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _run(Future<GroupLink?> Function() f, String done) async {
    setState(() => _busy = true);
    try {
      final l = await f();
      if (!mounted) return;
      setState(() => (_link = l ?? const GroupLink(on: false), _error = null, _copied = false));
      showTkToast(context, done);
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _replace() async {
    final yes = await askSure(context, title: 'Replace the link?', body: 'The old link and QR code stop working. People already in the group stay.', ok: 'Replace');
    if (yes) await _run(() => d.api.newGroupLink(id: d.id), 'A new link is on');
  }

  Future<void> _off() async {
    final yes = await askSure(context, title: 'Turn the link off?', body: 'No one can join by it, or by its QR code. You can turn a new one on later.', ok: 'Turn off', danger: true);
    if (yes) {
      await _run(() async {
        await d.api.groupLinkOff(id: d.id);
        return null;
      }, 'The link is off');
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return ListenableBuilder(
      listenable: d,
      builder: (context, _) {
        final g = d.group;
        final l = _link;
        final stopped = g?.linkStopped == true;
        return TkScreen(
          title: 'Invite link',
          onRefresh: _load,
          children: [
            if (_error != null) TkErrorBox(_error!),
            if (l == null && _error == null) const TkLoading(label: 'Loading…'),
            if (stopped)
              TkCallout(
                leading: TkSquare('b-shield', color: look.red),
                title: 'Taskiem’s moderators turned this group’s link off.',
                sub: 'No new link can be made for now.',
                color: look.red,
              ),
            if (l != null && l.on && (l.url ?? '').isNotEmpty)
              TkTopCard(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text((l.words ?? '').isEmpty ? 'Anyone with this link or QR code can see the group’s name, photo and how many are in it, and join.' : l.words!, style: tkText(size: 14, lineHeight: 1.45, color: t.text)),
                    const SizedBox(height: 14),
                    if ((l.qr ?? '').isNotEmpty)
                      Center(
                        child: Semantics(
                          image: true,
                          label: 'QR code for the group’s link',
                          child: Container(
                            padding: const EdgeInsets.all(10),
                            decoration: BoxDecoration(color: t.qrGround, borderRadius: BorderRadius.circular(16)),
                            child: ApiImage(key: ValueKey(l.code), transport: d.auth.transport, path: '${l.qr}${l.qr!.contains('?') ? '&' : '?'}format=png', width: 200, height: 200),
                          ),
                        ),
                      ),
                    const SizedBox(height: 12),
                    Text(l.url!, textAlign: TextAlign.center, style: tkText(size: 13.5, weight: 600, color: t.text)),
                    const SizedBox(height: 12),
                    TkButtonRow([
                      TkSmallButton(label: 'Share', icon: 'b-share', primary: true, onPressed: () => stepOut(() => SharePlus.instance.share(ShareParams(text: l.url!)))),
                      TkSmallButton(
                        label: _copied ? 'Copied' : 'Copy',
                        icon: 'b-copy',
                        onPressed: () async {
                          await Clipboard.setData(ClipboardData(text: l.url!));
                          if (mounted) setState(() => _copied = true);
                        },
                      ),
                    ]),
                  ],
                ),
              ),
            if (l != null && !l.on && !stopped)
              TkTopCard(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text('The link is off', style: tkDisplay(size: 20, color: t.text)),
                    const SizedBox(height: 4),
                    Text('Turn it on to let people join with a link or a QR code, without being added.', style: tkHint(t)),
                    const SizedBox(height: 12),
                    TkSmallButton(label: _busy ? 'Turning on…' : 'Turn on the link', primary: true, onPressed: _busy ? null : () => _run(() => d.api.newGroupLink(id: d.id), 'The link is on')),
                  ],
                ),
              ),
            if (g != null && !stopped)
              TkSection(
                children: [
                  TkListRow(
                    title: 'Approve new members',
                    sub: 'People joining by the link wait for an admin',
                    titleLines: 2,
                    trailing: TkSwitch(
                      label: 'Approve new members',
                      on: g.approveJoins,
                      onChanged: (v) async {
                        final ok = await d.edit(PutGroupRequest(approveJoins: v));
                        if (mounted && !ok) showTkToast(this.context, d.actionError ?? 'Something went wrong. Try again.', error: true);
                      },
                    ),
                  ),
                  if (l != null && l.on) ...[
                    TkListRow(title: 'Replace the link', sub: 'The old one stops working', icon: 'loop', square: look.orange, onTap: _busy ? null : _replace),
                    TkListRow(title: 'Turn the link off', icon: 'b-x', square: look.red, onTap: _busy ? null : _off),
                  ],
                ],
              ),
          ],
        );
      },
    );
  }
}

// --- leaving, deleting, blocking, reporting, appealing ---

/// Leave [d]'s group, asked first; a group closed by the last one out
/// closes its screens.
Future<void> leaveGroupFlow(BuildContext context, GroupChatData d, {VoidCallback? onClosed}) async {
  final yes = await askSure(
    context,
    title: 'Leave ${d.name}?',
    body: 'What came before stays yours until you delete it. You see nothing new and can’t write. The others are told you left.',
    ok: 'Leave',
    danger: true,
  );
  if (!yes || !context.mounted) return;
  final out = await d.leave();
  if (!context.mounted) return;
  if (out == null) return showTkToast(context, d.actionError ?? 'Something went wrong. Try again.', error: true);
  showTkToast(context, out.closed ? 'You were the last one, so the group is closed.' : 'You left ${d.name}');
  if (out.closed) onClosed?.call();
}

/// Delete [d]'s group for you, asked first.
Future<void> deleteGroupFlow(BuildContext context, GroupChatData d, {VoidCallback? onClosed}) async {
  final left = d.group?.left == true;
  final yes = await askSure(
    context,
    title: 'Delete ${d.name}?',
    body: left
        ? 'It goes for good, from every phone you use.'
        : 'Your copy of its messages up to now goes, from every phone you use. You stay in the group: a new message brings it back.',
    ok: 'Delete',
    danger: true,
  );
  if (!yes || !context.mounted) return;
  final ok = await d.deleteForMe();
  if (!context.mounted) return;
  showTkToast(context, ok ? 'Deleted' : (d.actionError ?? 'Something went wrong. Try again.'), error: !ok);
  if (ok) onClosed?.call();
}

/// Block a member (A8), asked first.
Future<void> blockMemberFlow(BuildContext context, GroupChatData d, String person, String name) async {
  final yes = await askSure(
    context,
    title: 'Block $name?',
    body: 'You still see their messages in groups you share. They can’t message you one to one, add you to groups, or see your last seen and photo. They are not told. You can unblock them in Blocked people.',
    ok: 'Block',
    danger: true,
  );
  if (!yes || !context.mounted) return;
  final ok = await d.block(person);
  if (context.mounted) showTkToast(context, ok ? 'Blocked $name' : (d.actionError ?? 'Something went wrong. Try again.'), error: !ok);
}

/// Report the group, or one member ([person]), with consent (§8.3); then
/// offers to leave the group, or block the member.
Future<void> reportGroupFlow(BuildContext context, GroupChatData d, {String? person, String? name}) async {
  final r = await showTkSheet<({String reason, String note})>(context, (_) => _ReportSheet(name: name ?? d.name, member: person != null));
  if (r == null || !context.mounted) return;
  final said = await d.report(r.reason, r.note, person: person);
  if (!context.mounted) return;
  if (said == null) return showTkToast(context, d.actionError ?? 'Something went wrong. Try again.', error: true);
  final next = await pickAction(
    context,
    title: 'Reported',
    note: said,
    actions: [
      if (person != null) ('block', 'Block $name too', 'b-x-ring', true),
      if (d.group?.left != true) ('leave', 'Leave the group', 'b-out', true),
      ('done', 'Done', 'b-check', false),
    ],
  );
  if (!context.mounted) return;
  if (next == 'block') await blockMemberFlow(context, d, person!, name ?? 'them');
  if (next == 'leave' && context.mounted) await leaveGroupFlow(context, d);
}

class _ReportSheet extends StatefulWidget {
  const _ReportSheet({required this.name, required this.member});
  final String name;
  final bool member;

  @override
  State<_ReportSheet> createState() => _ReportSheetState();
}

class _ReportSheetState extends State<_ReportSheet> {
  String? _reason;
  bool _consent = false;
  String? _error;
  final _note = TextEditingController();

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  void _send() {
    final r = _reason;
    if (r == null) return setState(() => _error = widget.member ? 'Choose why you are reporting them.' : 'Choose why you are reporting this group.');
    if (!_consent) return setState(() => _error = 'Agree to send the last 20 messages, so the moderators can see what happened.');
    Navigator.of(context).pop((reason: r, note: _note.text.trim()));
  }

  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: 'Report ${widget.name}',
    crumb: widget.member ? 'A member of the group' : 'The group',
    action: 'Send report',
    onAction: _send,
    children: [
      TkFormGroup(
        title: 'Why',
        children: [
          for (final (k, words) in peopleReportReasons)
            Semantics(
              selected: _reason == k,
              child: TkListRow(title: words, leading: TkCheckbox(checked: _reason == k), onTap: () => setState(() => _reason = k)),
            ),
        ],
      ),
      TkFormGroup(children: [TkKitField(label: 'What happened', hint: 'Optional', child: TkTextBox(controller: _note, lines: 3, max: 1000))]),
      TkFormGroup(
        children: [
          Semantics(
            checked: _consent,
            child: TkListRow(title: groupReportConsent, titleLines: 3, leading: TkCheckbox(checked: _consent), onTap: () => setState(() => _consent = !_consent)),
          ),
        ],
      ),
      if (_error != null) TkErrorBox(_error!),
    ],
  );
}

/// An admin's appeal of a closed group.
Future<void> appealGroupFlow(BuildContext context, GroupChatData d) async {
  final text = await showTkSheet<String>(context, (_) => const _AppealSheet());
  if (text == null || !context.mounted) return;
  final said = await d.appeal(text);
  if (!context.mounted) return;
  showTkToast(context, said ?? d.actionError ?? 'Something went wrong. Try again.', error: said == null);
}

class _AppealSheet extends StatefulWidget {
  const _AppealSheet();

  @override
  State<_AppealSheet> createState() => _AppealSheetState();
}

class _AppealSheetState extends State<_AppealSheet> {
  final _text = TextEditingController();
  String? _error;

  @override
  void dispose() {
    _text.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: 'Appeal the closing',
    crumb: 'A moderator who didn’t close it reads this',
    action: 'Send appeal',
    onAction: () {
      final w = _text.text.trim();
      if (w.isEmpty) return setState(() => _error = 'Write why you think the group should open again.');
      Navigator.of(context).pop(w);
    },
    children: [
      TkFormGroup(children: [TkKitField(label: 'Why it should open again', child: TkTextBox(controller: _text, lines: 5, max: 2000))]),
      if (_error != null) TkErrorBox(_error!),
    ],
  );
}
