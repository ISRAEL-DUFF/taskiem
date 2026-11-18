// Finding people (docs/spec/people-chatting.md §5): New chat, by a phone
// number (with Invite when they are not on Taskiem: sent from the person's
// own phone, never by Taskiem), by a person's link or its QR code (your own,
// with Share and Replace; someone's, opened from `/p/{code}` or pasted),
// and from the phone's contacts once the person agrees (people_contacts.dart).
// Also Requests (§7): strangers' first messages, opened without telling
// the sender.

import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:share_plus/share_plus.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/config.dart' show taskiemUrl;
import '../app/lock.dart' show stepOut;
import '../app/phone.dart' show notAPhone, toE164;
import '../kit/api_image.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/page.dart';
import '../kit/modal.dart';
import '../kit/polling.dart';
import '../kit/sheet.dart' show TkKitField;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'chats.dart' show ChatRow;
import 'my_chat.dart' show sayFailure;
import 'group_new.dart' show GroupJoinScreen, GroupInvitesSection, NewGroupScreen;
import 'groups.dart';
import 'people.dart';
import 'people_contacts.dart';
import 'people_live.dart';
import 'people_scan.dart';
import 'people_standing.dart' show YourStandingScreen, pointsToStanding;
import 'words.dart';

/// Opens a chat with a person by its id (over You); [started] is the chat
/// as `POST /v1/me/people` answered it, when just started: a chat with no
/// message yet is in no list, so it opens from that.
typedef OpenPeopleChat = Future<void> Function(String chatId, {PeopleChat? started});

/// The person's chat with someone found ([person], or by their link's
/// [linkCode]): the one they have, else started (`POST /v1/me/people`),
/// then opened. A refusal is said as a toast.
Future<void> chatWithPerson(BuildContext context, Auth auth, OpenPeopleChat open, {String? person, String? linkCode}) async {
  try {
    final c = await auth.api.startPeopleChat(body: StartPeopleChatRequest(personId: person, linkCode: linkCode));
    await open(c.id, started: c);
  } on Object catch (e) {
    if (!context.mounted) return;
    // Their own account stopped by the moderators: the words point to
    // Your standing, one tap away.
    showTkToast(
      context,
      sayFailure(e),
      error: true,
      action: pointsToStanding(e) ? 'Your standing' : null,
      onAction: pointsToStanding(e) ? () => tkPush<void>(context, YourStandingScreen(auth: auth)) : null,
    );
  }
}

/// The code of a person's link: `/p/{code}` (as applinks pass it on), a
/// whole link to this server, or the code alone; null when it is none.
String? personCodeOf(String typed) {
  final s = typed.trim();
  final path = RegExp(r'^/p/([A-Za-z0-9]{4,40})/?$').firstMatch(s);
  if (path != null) return path[1];
  final uri = Uri.tryParse(s);
  if (uri != null && uri.hasScheme) {
    final home = Uri.tryParse(taskiemUrl);
    final m = RegExp(r'^/p/([A-Za-z0-9]{4,40})/?$').firstMatch(uri.path);
    if (m != null && (home == null || uri.host.toLowerCase() == home.host.toLowerCase())) return m[1];
    return null;
  }
  return RegExp(r'^[A-Za-z0-9]{4,40}$').hasMatch(s) ? s : null;
}

/// A person found (by number or link), with Chat and Add to contacts.
class FoundPerson extends StatefulWidget {
  const FoundPerson({super.key, required this.auth, required this.found, required this.from, required this.onChat});
  final Auth auth;
  final PersonFound found;

  /// How they were found: number or link.
  final String from;
  final VoidCallback onChat;

  @override
  State<FoundPerson> createState() => _FoundPersonState();
}

class _FoundPersonState extends State<FoundPerson> {
  bool _added = false;
  bool _busy = false;

  Future<void> _add() async {
    final p = widget.found.person!;
    final token = widget.found.addToken;
    if (token == null) return;
    // Their name in your contacts, as the web asks: empty for their own.
    final name = await askContactName(context, initial: '', theirs: p.name);
    if (name == null || !mounted) return;
    setState(() => _busy = true);
    try {
      await widget.auth.api.addContact(body: AddContactRequest(personId: p.id, addToken: token, from: widget.from, name: name.isEmpty ? null : name));
      if (!mounted) return;
      setState(() => _added = true);
      showTkToast(context, 'Added to your contacts');
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final p = widget.found.person!;
    if (widget.found.self == true) {
      return TkCallout(leading: TkSquare('b-person', color: TkLook.of(context).teal), title: 'That’s you', sub: 'Share your link so people can chat with you.', color: TkLook.of(context).teal);
    }
    final seen = lastSeenWords(p, DateTime.now());
    // As a top card: their face, their name in the app's display type, then
    // Chat and Add to contacts side by side.
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(22)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              PersonFace(name: p.name, transport: widget.auth.transport, photo: p.photo, size: 52),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(p.contactName is String && (p.contactName as String).isNotEmpty ? p.contactName as String : p.name, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkDisplay(size: 19, letterSpacing: -0.3, color: t.text)),
                    Text(p.inContacts || _added ? 'In your contacts' : (seen ?? 'On Taskiem'), style: tkText(size: 13, color: t.muted)),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          TkButtonRow([
            TkSmallButton(label: 'Chat', primary: true, icon: 'b-chat', onPressed: widget.onChat),
            if (!p.inContacts && !_added && widget.found.addToken != null) TkSmallButton(label: 'Add to contacts', onPressed: _busy ? null : _add),
          ]),
        ],
      ),
    );
  }
}

/// New chat: by a phone number, by a link, from your contacts; and your
/// own link to share.
class NewPeopleChatScreen extends StatefulWidget {
  const NewPeopleChatScreen({super.key, required this.auth, required this.openChat, this.contacts = const DevicePhoneContacts(), this.scanner = const DeviceQrScanner(), this.onContactsChecked, this.openGroup});

  /// Opens a group (one made here, or joined by its link); null: no New
  /// group.
  final OpenGroup? openGroup;
  final Auth auth;
  final OpenPeopleChat openChat;
  final PhoneContacts contacts;

  /// The camera for a person's QR code, asked for only on tap (a test's own).
  final QrScanner scanner;

  /// Told when the person agreed and their contacts were checked (the daily
  /// re-check's date).
  final VoidCallback? onContactsChecked;

  @override
  State<NewPeopleChatScreen> createState() => _NewPeopleChatScreenState();
}

class _NewPeopleChatScreenState extends State<NewPeopleChatScreen> {
  final _phone = TextEditingController();
  final _link = TextEditingController();
  PersonFound? _found;
  String _foundFrom = 'number';
  String? _error;
  bool _busy = false;

  @override
  void dispose() {
    _phone.dispose();
    _link.dispose();
    super.dispose();
  }

  Future<void> _byNumber() async {
    final e = toE164(_phone.text);
    if (e == null) return setState(() => (_error = notAPhone, _found = null));
    setState(() => (_busy = true, _error = null, _found = null));
    try {
      final f = await widget.auth.api.lookupPerson(body: LookupPersonRequest(phone: e));
      if (mounted) setState(() => (_found = f, _foundFrom = 'number'));
    } on ApiError catch (x) {
      if (mounted) {
        setState(() => _error = x.has('phone_invalid') ? notAPhone : x.message);
      }
    } on Object catch (x) {
      if (mounted) setState(() => _error = sayFailure(x));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _byLink() async {
    final group = groupCodeOf(_link.text);
    if (group != null && widget.openGroup != null) {
      setState(() => (_error = null, _found = null));
      await tkPush<void>(context, GroupJoinScreen(auth: widget.auth, code: group, openGroup: widget.openGroup!));
      return;
    }
    final code = personCodeOf(_link.text);
    if (code == null) return setState(() => (_error = 'That doesn’t look like a person’s Taskiem link.', _found = null));
    setState(() => (_busy = true, _error = null, _found = null));
    try {
      final f = await widget.auth.api.personByLink(code: code);
      if (mounted) setState(() => (_found = f, _foundFrom = 'link'));
    } on Object catch (x) {
      if (mounted) setState(() => _error = personLinkProblem(x));
    }
    if (mounted) setState(() => _busy = false);
  }

  /// A person's QR code scanned in the app: who it is, then the chat.
  Future<void> _scan() async {
    final code = await widget.scanner.scan(context);
    if (code == null || !mounted) return;
    final group = groupLinkOf(code);
    if (group != null) {
      if (widget.openGroup != null) await tkPush<void>(context, GroupJoinScreen(auth: widget.auth, code: group, openGroup: widget.openGroup!));
      return;
    }
    await tkPush<void>(context, PersonLinkScreen(auth: widget.auth, code: code, openChat: widget.openChat));
  }

  /// A link copied elsewhere, put in the box and opened.
  Future<void> _paste() async {
    final got = (await Clipboard.getData(Clipboard.kTextPlain))?.text?.trim() ?? '';
    if (!mounted) return;
    if (got.isEmpty) return setState(() => _error = 'There is no link copied. Copy their link, then tap Paste.');
    _link.text = got;
    await _byLink();
  }

  Future<void> _invite(PersonFoundInvite i) => stepOut(() => SharePlus.instance.share(ShareParams(text: i.words.contains(i.link) ? i.words : '${i.words} ${i.link}')));

  Future<void> _chat(String person) => chatWithPerson(context, widget.auth, widget.openChat, person: person);

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final f = _found;
    // A large-title page: the ways in as rows, then a number or a link each
    // in a form group with the app's buttons.
    return TkPageScreen(
      title: 'New chat',
      titleSize: 30,
      backLabel: 'Back to Chats',
      children: [
        TkSection(
          inset: 56,
          children: [
            if (widget.openGroup != null)
              TkListRow(
                title: 'New group',
                sub: 'Chat with up to $groupMost people at once',
                icon: 'b-group',
                square: look.lime,
                onTap: () => tkPush<void>(context, NewGroupScreen(auth: widget.auth, openGroup: widget.openGroup!)),
              ),
            TkListRow(
              title: 'From your contacts',
              sub: 'Only if you agree: see how it works first',
              icon: 'b-people',
              square: look.teal,
              onTap: () => tkPush<void>(context, ContactsMatchScreen(auth: widget.auth, contacts: widget.contacts, onChat: _chat, onChecked: widget.onContactsChecked)),
            ),
            TkListRow(
              title: 'Your contacts',
              sub: 'People you added',
              icon: 'b-person',
              square: look.blue,
              onTap: () => tkPush<void>(context, PeopleContactsScreen(auth: widget.auth, onChat: _chat)),
            ),
            TkListRow(title: 'Scan their QR code', sub: 'With your phone’s camera', icon: 'b-camera', square: look.orange, onTap: _busy ? null : _scan),
            TkListRow(
              title: 'Your link and QR code',
              sub: 'Share it, so people can chat with you without your number',
              icon: 'b-qr',
              square: look.violet,
              onTap: () => tkPush<void>(context, MyPersonLinkScreen(auth: widget.auth)),
            ),
          ],
        ),
        TkFormGroup(
          title: 'By phone number',
          children: [
            TkKitField(label: 'Their number', hint: 'A Nigerian number can start with 0.', child: TkTextBox(key: const ValueKey('find-phone'), controller: _phone, phone: true, size: 15, height: 46)),
            TkButtonRow([TkSmallButton(label: _busy ? 'Looking…' : 'Find', primary: true, icon: 'b-search', onPressed: _busy ? null : _byNumber)]),
          ],
        ),
        if (_error != null) TkErrorBox(_error!),
        if (f != null && f.found && f.person != null) FoundPerson(auth: widget.auth, found: f, from: _foundFrom, onChat: () => _chat(f.person!.id)),
        if (f != null && !f.found)
          TkCallout(
            leading: TkSquare('b-send', color: look.blue),
            title: 'Not on Taskiem yet',
            sub: 'Invite them with your link. It goes from your phone, as your own message: Taskiem sends nothing.',
            subLines: 4,
            color: look.blue,
            button: f.invite == null ? null : 'Invite',
            onPressed: f.invite == null ? null : () => _invite(f.invite!),
          ),
        TkFormGroup(
          title: 'By their link',
          children: [
            TkKitField(label: 'Their link', hint: 'Type or paste a link someone shared.', child: TkTextBox(key: const ValueKey('find-link'), controller: _link, size: 15, height: 46)),
            TkButtonRow([
              TkSmallButton(label: 'Paste', onPressed: _busy ? null : _paste),
              TkSmallButton(label: 'Open the link', primary: true, onPressed: _busy ? null : _byLink),
            ]),
          ],
        ),
      ],
    );
  }
}

/// The words for a person's link that cannot be opened.
String personLinkProblem(Object e) => switch (e) {
  ApiError(status: 410) => 'This link no longer works. Ask them for their new link.',
  ApiError(status: 404) => 'This link does not open a chat with anyone.',
  _ => sayFailure(e),
};

/// A person's link opened in the app (`/p/{code}`, a QR code scanned with
/// the phone's camera): who it is, Chat (a request unless they know you)
/// and Add to contacts.
class PersonLinkScreen extends StatefulWidget {
  const PersonLinkScreen({super.key, required this.auth, required this.code, required this.openChat});
  final Auth auth;
  final String code;
  final OpenPeopleChat openChat;

  @override
  State<PersonLinkScreen> createState() => _PersonLinkScreenState();
}

class _PersonLinkScreenState extends State<PersonLinkScreen> {
  PersonFound? _found;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final f = await widget.auth.api.personByLink(code: widget.code);
      if (mounted) setState(() => _found = f);
    } on Object catch (e) {
      if (mounted) setState(() => _error = personLinkProblem(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final f = _found;
    return TkScreen(
      title: 'Chat on Taskiem',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (f == null && _error == null) const TkLoading(label: 'Loading…'),
        if (f != null && f.self == true) TkPanel(padding: const EdgeInsets.all(16), children: [Text('This is your own link. Share it so people can chat with you.', style: tkText(size: 15, color: t.text))]),
        if (f != null && f.self != true && f.person != null)
          FoundPerson(
            auth: widget.auth,
            found: f,
            from: 'link',
            onChat: () async {
              final nav = Navigator.of(context);
              await chatWithPerson(context, widget.auth, (id, {started}) async {
                await nav.maybePop();
                await widget.openChat(id, started: started);
              }, linkCode: widget.code);
            },
          ),
        if (f != null && f.self != true && f.person != null)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 4),
            child: Text('If you have not chatted before, your first message waits in their requests until they accept.', style: tkHint(t)),
          ),
      ],
    );
  }
}

/// Your own link and QR code (`/v1/me/link`): share it, copy it, or
/// replace it (the old one stops working).
class MyPersonLinkScreen extends StatefulWidget {
  const MyPersonLinkScreen({super.key, required this.auth});
  final Auth auth;

  @override
  State<MyPersonLinkScreen> createState() => _MyPersonLinkScreenState();
}

class _MyPersonLinkScreenState extends State<MyPersonLinkScreen> {
  PersonLink? _link;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final l = await widget.auth.api.myPersonLink();
      if (mounted) setState(() => _link = l);
    } on ApiError catch (e) {
      if (mounted) setState(() => _error = e.status == 503 ? 'Links can’t be made right now. Try again later.' : e.message);
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _replace() async {
    final yes = await showTkConfirm(
      context,
      title: 'Replace your link?',
      body: 'Your old link and QR code stop working. People who already chat with you are not affected.',
      ok: 'Replace',
      danger: true,
    );
    if (!yes) return;
    setState(() => _busy = true);
    try {
      final l = await widget.auth.api.newPersonLink();
      if (!mounted) return;
      setState(() => _link = l);
      showTkToast(context, 'Your new link is ready');
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final l = _link;
    final look = TkLook.of(context);
    // As a group's invite link: a top card with the QR on its own ground,
    // Share and Copy side by side; Replace as a row.
    return TkScreen(
      title: 'Your link',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (l == null && _error == null) const TkLoading(label: 'Loading…'),
        if (l != null) ...[
          TkTopCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(l.words.isEmpty ? 'People who open your link or scan your QR code can chat with you, without your number.' : l.words, style: tkText(size: 14, lineHeight: 1.45, color: t.text)),
                const SizedBox(height: 14),
                Center(
                  child: Semantics(
                    label: 'Your QR code',
                    image: true,
                    child: Container(
                      padding: const EdgeInsets.all(10),
                      decoration: BoxDecoration(color: t.qrGround, borderRadius: BorderRadius.circular(16)),
                      child: ApiImage(key: ValueKey(l.code), transport: widget.auth.transport, path: '${l.qr}${l.qr.contains('?') ? '&' : '?'}format=png', width: 200, height: 200),
                    ),
                  ),
                ),
                const SizedBox(height: 6),
                Text('Scanned with a phone’s camera, it opens a chat with you.', textAlign: TextAlign.center, style: tkHint(t)),
                const SizedBox(height: 12),
                Text(l.url, textAlign: TextAlign.center, style: tkText(size: 13.5, weight: 600, color: t.text)),
                const SizedBox(height: 12),
                TkButtonRow([
                  TkSmallButton(label: 'Share', icon: 'b-share', primary: true, onPressed: () => stepOut(() => SharePlus.instance.share(ShareParams(text: l.url)))),
                  TkSmallButton(
                    label: 'Copy',
                    icon: 'b-copy',
                    onPressed: () async {
                      await Clipboard.setData(ClipboardData(text: l.url));
                      if (context.mounted) showTkToast(context, 'Link copied');
                    },
                  ),
                ]),
              ],
            ),
          ),
          TkSection(
            inset: 56,
            children: [TkListRow(title: _busy ? 'Replacing…' : 'Replace the link', sub: 'The old one stops working', icon: 'loop', square: look.orange, onTap: _busy ? null : _replace)],
          ),
        ],
      ],
    );
  }
}

// --- Requests ---

/// Requests (§7): first messages from people you have not chatted with and
/// have not added. Opening one tells the sender nothing; in it, Accept,
/// Delete, or Block and report.
class PeopleRequestsScreen extends StatefulWidget {
  const PeopleRequestsScreen({super.key, required this.auth, required this.openChat, this.now = DateTime.now, this.live, this.openGroup});
  final Auth auth;
  final OpenPeopleChat openChat;

  /// Opens a group (an invite accepted); with it, the invites to groups
  /// show here too.
  final OpenGroup? openGroup;
  final DateTime Function() now;

  /// You's live connection (by default the one You holds): a request new,
  /// answered or deleted elsewhere reads the list again at once.
  final PeopleLive? live;

  @override
  State<PeopleRequestsScreen> createState() => _PeopleRequestsScreenState();
}

class _PeopleRequestsScreenState extends State<PeopleRequestsScreen> with Polling {
  List<PeopleChat>? _requests;
  String? _error;
  late final PeopleLive? _live = widget.live ?? PeopleLive.of(widget.auth);
  StreamSubscription<PeopleLiveEvent>? _events;
  Timer? _soon;
  final _slow = SlowWhileLive();

  @override
  void initState() {
    super.initState();
    _load();
    _events = _live?.events.listen((e) {
      final mine = _requests?.any((c) => c.id == e.chatId) == true;
      final about = switch (e) {
        PeopleLiveMessage(:final request) => request || mine,
        PeopleLiveChat() => true,
        PeopleLiveGone(:final id) => id == null || mine,
      };
      if (!about) return;
      _soon?.cancel();
      // Covered by a request opened: read once when it is on top again.
      _soon = Timer(const Duration(milliseconds: 300), () {
        if (mounted) readWhenSeen(_load);
      });
    });
    // Polled slowly as the fallback while the connection is not open.
    startPolling(const Duration(seconds: 10), () {
      if (_slow.due(_live)) _load();
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
      final r = await widget.auth.api.listPeopleRequests();
      if (mounted) setState(() => (_requests = r.requests, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _open(PeopleChat c) async {
    await widget.openChat(c.id);
    await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final list = _requests;
    final now = widget.now();
    return TkScreen(
      title: 'Message requests',
      onRefresh: _load,
      children: [
        if (widget.openGroup != null) GroupInvitesSection(auth: widget.auth, openGroup: widget.openGroup!, live: _live, now: widget.now),
        if (_error != null) TkErrorBox(_error!),
        if (list == null && _error == null) const TkLoading(label: 'Loading…'),
        if (list != null && list.isEmpty) const TkPanel(children: [TkEmpty(icon: 'chat', title: 'No requests', line: 'First messages from people you don’t know wait here.')]),
        if (list != null && list.isNotEmpty)
          TkPanel(
            inset: 76,
            children: [
              for (final c in list)
                ChatRow(
                  key: ValueKey('request ${c.id}'),
                  title: c.name,
                  letter: c.name,
                  avatar: PersonFace(name: c.name, transport: widget.auth.transport, photo: c.person?.photo, size: 48),
                  time: listTime(c.lastAt, now),
                  preview: Text(c.last.isEmpty ? 'Wants to chat' : c.last, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14, color: t.muted)),
                  unread: c.unread,
                  onTap: () => _open(c),
                ),
            ],
          ),
        // A quiet note under the list (main's pages), not a paragraph under
        // the title.
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Text('People you haven’t chatted with. They are not told you read their message unless you accept.', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
        ),
      ],
    );
  }
}
