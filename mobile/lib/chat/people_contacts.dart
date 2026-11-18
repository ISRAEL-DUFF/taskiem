// Contacts (docs/spec/people-chatting.md §5.3, §5.4): the people a person
// added (`/v1/me/contacts`: add, rename, remove), and finding friends from
// the phone's own contacts, only once the person agrees after reading
// how it works and its limit, honestly said.
//
// Each number is put in E.164 and hashed on the phone with the week's key
// from the server (`GET /v1/me/contacts/key`), exactly as the server's As
// built section says: SHA-256(key ‖ "taskiem-contacts:" ‖ E.164), 64 hex
// digits. Only the hashes are sent (`POST /v1/me/contacts/match`, in
// batches of at most 500). Nothing is kept on the phone: not the numbers,
// not the hashes, not the answer once the screen closes. The phone's
// contacts are behind [PhoneContacts], so a test gives its own.

import 'dart:convert';

import 'package:crypto/crypto.dart' show sha256;
import 'package:flutter/widgets.dart';
import 'package:flutter_contacts/flutter_contacts.dart' as fc;

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../app/lock.dart' show stepOut;
import '../app/phone.dart' show toE164;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'my_chat.dart' show sayFailure;
import 'people.dart';

/// What the phone hashes before each number (engine/people HashPrefix).
const contactsHashPrefix = 'taskiem-contacts:';

/// The hash of one number with the week's key: SHA-256 of the key's bytes,
/// then "taskiem-contacts:" and the E.164 number, as 64 hex digits (the
/// server's ContactHash).
String contactHash(List<int> key, String e164) => sha256.convert([...key, ...utf8.encode('$contactsHashPrefix$e164')]).toString();

/// A contact of the phone: its name and its numbers as typed.
typedef PhoneContact = ({String name, List<String> numbers});

/// The phone's address book, behind one face (a test gives its own).
abstract class PhoneContacts {
  const PhoneContacts();

  /// Asks the phone for the contacts (its own question); false when not
  /// allowed.
  Future<bool> ask();

  /// Every contact with a number.
  Future<List<PhoneContact>> read();
}

/// The phone's own contacts, through flutter_contacts: read only, names
/// and numbers only.
class DevicePhoneContacts extends PhoneContacts {
  const DevicePhoneContacts();

  @override
  Future<bool> ask() async {
    final s = await stepOut(() => fc.FlutterContacts.permissions.request(fc.PermissionType.read));
    return s == fc.PermissionStatus.granted || s == fc.PermissionStatus.limited;
  }

  @override
  Future<List<PhoneContact>> read() async {
    final all = await fc.FlutterContacts.getAll(properties: {fc.ContactProperty.name, fc.ContactProperty.phone});
    return [
      for (final c in all)
        if (c.phones.isNotEmpty) (name: c.displayName ?? '', numbers: [for (final p in c.phones) p.normalizedNumber ?? p.number]),
    ];
  }
}

/// A person found among the phone's contacts: as the server shows them,
/// the name they have in the phone, and the token to add them.
typedef ContactMatch = ({PersonCard person, String phoneName, String addToken});

/// Finds which of [contacts] are people on Taskiem who let themselves be
/// found from contacts: the numbers hashed here with the week's key, sent
/// in batches; asked again once with a new key if it changed meanwhile.
Future<List<ContactMatch>> matchPhoneContacts(TaskiemApi api, List<PhoneContact> contacts) async {
  // Each number once, in E.164, with the first name it has in the phone.
  final names = <String, String>{};
  for (final c in contacts) {
    for (final n in c.numbers) {
      final e = toE164(n);
      if (e != null) names.putIfAbsent(e, () => c.name);
    }
  }
  if (names.isEmpty) return const [];
  for (var attempt = 0; ; attempt++) {
    final k = await api.contactsKey();
    final key = base64.decode(k.key);
    final byHash = {for (final e in names.keys) contactHash(key, e): e};
    final hashes = byHash.keys.take(k.maxPerDay > 0 ? k.maxPerDay : byHash.length).toList();
    final batch = k.maxBatch > 0 ? k.maxBatch : 500;
    final out = <ContactMatch>[];
    try {
      for (var i = 0; i < hashes.length; i += batch) {
        final r = await api.matchContacts(body: MatchContactsRequest(keyId: k.keyId, hashes: hashes.sublist(i, (i + batch).clamp(0, hashes.length))));
        for (final m in r.matches) {
          out.add((person: m.person, phoneName: names[byHash[m.hash]] ?? '', addToken: m.addToken));
        }
      }
      return out;
    } on ApiError catch (e) {
      // The week's key changed while sending: once more with the new one.
      if (e.status == 409 && attempt == 0) continue;
      rethrow;
    }
  }
}

/// The explainer, said before the phone is asked (§5.3): what is sent, what
/// is kept, and the method's limit.
const contactsAskTitle = 'Find friends from your contacts?';
const contactsAskWords = [
  'Taskiem can check which of the people in your phone’s contacts are on Taskiem, so you can chat with them.',
  'Their numbers are not sent to us in readable form. Each number is scrambled on this phone with a key from Taskiem (a keyed hash), and only the scrambled numbers are sent. Taskiem answers which are people who let themselves be found this way, then forgets what was sent. Nothing of your contacts is kept, on this phone or by Taskiem.',
  'The honest limit: Taskiem holds the key, and phone numbers are few enough to try them all. So this protects your contacts if what is sent is seen by someone else, but not from Taskiem itself.',
  'Only people who allow it can be found, and you can stop at any time.',
];

/// Finding friends from the phone's contacts: the explainer first, the
/// phone's own question only after Agree, then who was found, each with
/// Chat and Add. Nothing is kept once the screen closes.
class ContactsMatchScreen extends StatefulWidget {
  const ContactsMatchScreen({super.key, required this.auth, this.contacts = const DevicePhoneContacts(), required this.onChat});
  final Auth auth;
  final PhoneContacts contacts;

  /// Starts (or opens) a chat with someone found.
  final Future<void> Function(String personId) onChat;

  @override
  State<ContactsMatchScreen> createState() => _ContactsMatchScreenState();
}

enum _Step { ask, checking, found, refused }

class _ContactsMatchScreenState extends State<ContactsMatchScreen> {
  _Step _step = _Step.ask;
  List<ContactMatch> _found = const [];
  final _added = <String>{};
  String? _error;

  Future<void> _agree() async {
    setState(() => (_step = _Step.checking, _error = null));
    try {
      if (!await widget.contacts.ask()) {
        if (mounted) setState(() => _step = _Step.refused);
        return;
      }
      final all = await widget.contacts.read();
      final found = await matchPhoneContacts(widget.auth.api, all);
      if (mounted) setState(() => (_found = found, _step = _Step.found));
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.status == 429 ? 'You have checked many numbers today. Try again tomorrow.' : e.message, _step = _Step.ask));
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _step = _Step.ask));
    }
  }

  Future<void> _add(ContactMatch m) async {
    try {
      await widget.auth.api.addContact(body: AddContactRequest(personId: m.person.id, addToken: m.addToken, from: 'contacts', name: m.phoneName.isEmpty ? null : m.phoneName));
      if (!mounted) return;
      setState(() => _added.add(m.person.id));
      showTkToast(context, 'Added to your contacts');
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TkScreen(
      title: 'From your contacts',
      children: switch (_step) {
        _Step.ask => [
          TkPanel(
            padding: const EdgeInsets.all(16),
            children: [
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Semantics(header: true, child: Text(contactsAskTitle, style: tkDisplay(size: 20, color: t.text))),
                  const SizedBox(height: 10),
                  for (final w in contactsAskWords) Padding(padding: const EdgeInsets.only(bottom: 10), child: Text(w, style: tkText(size: 14.5, lineHeight: 1.45, color: t.text))),
                  const SizedBox(height: 4),
                  TkButton(label: 'Agree and check my contacts', primary: true, minHeight: 44, onPressed: _agree),
                  const SizedBox(height: 8),
                  TkButton(label: 'Not now', minHeight: 44, onPressed: () => Navigator.of(context).maybePop()),
                ],
              ),
            ],
          ),
          if (_error != null) TkErrorBox(_error!),
        ],
        _Step.checking => [const TkLoading(label: 'Checking your contacts…')],
        _Step.refused => [
          const TkPanel(children: [TkEmpty(icon: 'b-people', title: 'Contacts not allowed', line: 'The phone did not let Taskiem read your contacts. You can still find people by their number or their link.')]),
        ],
        _Step.found => [
          if (_found.isEmpty)
            const TkPanel(children: [TkEmpty(icon: 'b-people', title: 'No one found yet', line: 'None of your contacts can be found on Taskiem now. Share your link to invite them.')])
          else
            TkSection(
              title: 'On Taskiem',
              count: _found.length,
              children: [
                for (final m in _found)
                  TkListRow(
                    title: m.phoneName.isEmpty ? m.person.name : m.phoneName,
                    sub: m.phoneName.isEmpty || m.phoneName == m.person.name ? 'On Taskiem' : 'On Taskiem as ${m.person.name}',
                    face: PersonFace(name: m.person.name, transport: widget.auth.transport, photo: m.person.photo, size: 40),
                    trailing: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (!m.person.inContacts && !_added.contains(m.person.id)) ...[TkSmallButton(label: 'Add', onPressed: () => _add(m)), const SizedBox(width: 6)],
                        TkSmallButton(label: 'Chat', primary: true, onPressed: () => widget.onChat(m.person.id)),
                      ],
                    ),
                  ),
              ],
            ),
          Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: Text('Nothing of your contacts was kept: not on this phone, not by Taskiem.', style: tkHint(t))),
        ],
      },
    );
  }
}

// --- Your contacts ---

/// Your contacts (`GET /v1/me/contacts`): the people you added, by the
/// name you see; each with Chat, Rename and Remove.
class PeopleContactsScreen extends StatefulWidget {
  const PeopleContactsScreen({super.key, required this.auth, required this.onChat, this.onFind});
  final Auth auth;
  final Future<void> Function(String personId) onChat;

  /// Finding more people (New chat).
  final VoidCallback? onFind;

  @override
  State<PeopleContactsScreen> createState() => _PeopleContactsScreenState();
}

class _PeopleContactsScreenState extends State<PeopleContactsScreen> {
  List<PersonContact>? _contacts;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.myContacts();
      if (mounted) setState(() => (_contacts = r.contacts, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _rename(PersonContact c) async {
    final name = await askContactName(context, initial: c.contactName is String ? c.contactName as String : '', theirs: c.name);
    if (name == null || !mounted) return;
    try {
      await widget.auth.api.renameContact(person: c.id, body: RenameContactRequest(name: name.isEmpty ? null : name));
      await _load();
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
  }

  Future<void> _remove(PersonContact c) async {
    final yes = await showTkConfirm(
      context,
      title: 'Remove ${_shown(c)} from your contacts?',
      body: 'Your chat with them stays. They are not told.',
      ok: 'Remove',
      danger: true,
    );
    if (!yes) return;
    try {
      await widget.auth.api.removeContact(person: c.id);
      await _load();
      if (mounted) showTkToast(context, 'Removed');
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
  }

  static String _shown(PersonContact c) => c.contactName is String && (c.contactName as String).isNotEmpty ? c.contactName as String : c.name;

  Future<void> _open(PersonContact c) async {
    final what = await showTkModalChoices(
      context,
      title: _shown(c),
      choices: [if (!c.deleted) ('chat', 'Chat'), ('rename', 'Rename'), ('remove', 'Remove from contacts')],
    );
    if (!mounted) return;
    switch (what) {
      case 'chat':
        await widget.onChat(c.id);
      case 'rename':
        await _rename(c);
      case 'remove':
        await _remove(c);
    }
  }

  @override
  Widget build(BuildContext context) {
    final contacts = _contacts;
    final now = DateTime.now();
    return TkScreen(
      title: 'Your contacts',
      onRefresh: _load,
      actions: [if (widget.onFind != null) TkHeadButton(icon: 'b-plus', label: 'Find people', onTap: widget.onFind!)],
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (contacts == null && _error == null) const TkLoading(label: 'Loading…'),
        if (contacts != null && contacts.isEmpty)
          const TkPanel(children: [TkEmpty(icon: 'b-people', title: 'No contacts yet', line: 'Add people you find by their number, their link, or your phone’s contacts.')]),
        if (contacts != null && contacts.isNotEmpty)
          TkPanel(
            children: [
              for (final c in contacts)
                TkListRow(
                  title: _shown(c),
                  sub: c.deleted ? deletedAccount : (lastSeenWords(PersonCard(id: c.id, name: c.name, deleted: c.deleted, inContacts: true, readTicks: c.readTicks, theyAddedYou: c.theyAddedYou, lastSeenAt: c.lastSeenAt, online: c.online), now) ?? (_shown(c) == c.name ? '' : c.name)),
                  face: PersonFace(name: _shown(c), transport: widget.auth.transport, photo: c.photo, size: 40, online: c.online == true),
                  onTap: () => _open(c),
                ),
            ],
          ),
      ],
    );
  }
}
