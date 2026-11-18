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
import '../app/phone.dart' show contactE164;
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

  /// Whether the phone still lets Taskiem read them, without asking.
  Future<bool> allowed() async => false;
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
  Future<bool> allowed() async {
    try {
      final s = await fc.FlutterContacts.permissions.check(fc.PermissionType.read);
      return s == fc.PermissionStatus.granted || s == fc.PermissionStatus.limited;
    } on Object {
      return false;
    }
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

/// What matching found: the people, and whether it stopped before every
/// number was checked (the day's limit: `max_per_day`, or the server's 429)
/// with how many were.
typedef ContactsMatched = ({List<ContactMatch> found, bool stopped, int checked, int total});

/// Finds which of [contacts] are people on Taskiem who let themselves be
/// found from contacts: the numbers hashed here with the week's key, sent
/// in batches, no more than the day's `max_per_day`. When the key changes
/// meanwhile (409), only the batches not yet answered are sent again, with
/// the new key; at the day's limit (429), what was found so far is kept.
///
/// [day] (the daily re-check's, `2026-11-18`): with more numbers than the
/// day allows, each day starts further round them (sorted, so the order
/// holds from day to day), so that over the days all are checked, not the
/// same first ones each time. Nothing but the day is needed or kept.
Future<ContactsMatched> matchPhoneContacts(TaskiemApi api, List<PhoneContact> contacts, {String? day}) async {
  // Each number once, in E.164, with the first name it has in the phone.
  final names = <String, String>{};
  for (final c in contacts) {
    for (final n in c.numbers) {
      final e = contactE164(n);
      if (e != null) names.putIfAbsent(e, () => c.name);
    }
  }
  final out = <ContactMatch>[];
  if (names.isEmpty) return (found: out, stopped: false, checked: 0, total: 0);
  var k = await api.contactsKey();
  // No more than the day allows (1000 in an account's first week).
  final numbers = dayShare(names.keys.toList(), k.maxPerDay, day);
  var done = 0;
  for (var changes = 0; done < numbers.length;) {
    final key = base64.decode(k.key);
    final batch = k.maxBatch > 0 ? k.maxBatch : 500;
    final part = numbers.sublist(done, (done + batch).clamp(0, numbers.length));
    final byHash = {for (final e in part) contactHash(key, e): e};
    try {
      final r = await api.matchContacts(body: MatchContactsRequest(keyId: k.keyId, hashes: byHash.keys.toList()));
      for (final m in r.matches) {
        out.add((person: m.person, phoneName: names[byHash[m.hash]] ?? '', addToken: m.addToken));
      }
      done += part.length;
    } on ApiError catch (e) {
      // The week's key changed while sending: the rest with the new one.
      if (e.status == 409 && changes < 2) {
        changes++;
        k = await api.contactsKey();
        continue;
      }
      // The day's limit: who was found so far.
      if (e.status == 429 && done > 0) return (found: out, stopped: true, checked: done, total: names.length);
      rethrow;
    }
  }
  return (found: out, stopped: numbers.length < names.length, checked: done, total: names.length);
}

/// The numbers checked on [day]: all of [all] when the day allows them
/// ([perDay] 0 or more than there are); else [perDay] of them, starting
/// [perDay] further round the sorted numbers each day, so the days go
/// round them all. Without a day, the first [perDay] as the phone has them.
List<String> dayShare(List<String> all, int perDay, String? day) {
  if (perDay <= 0 || perDay >= all.length) return all;
  if (day == null) return all.take(perDay).toList();
  final sorted = [...all]..sort();
  final n = DateTime.parse('${day}T00:00:00Z').difference(DateTime.utc(1970)).inDays;
  final start = (n * perDay) % sorted.length;
  return [for (var i = 0; i < perDay; i++) sorted[(start + i) % sorted.length]];
}

/// The explainer, said before the phone is asked (§5.3): what is sent, what
/// is kept, and the method's limit.
const contactsAskTitle = 'Find friends from your contacts?';
const contactsAskWords = [
  'Taskiem can check which of the people in your phone’s contacts are on Taskiem, so you can chat with them.',
  'Their numbers are not sent to us in readable form. Each number is scrambled on this phone with a key from Taskiem (a keyed hash), and only the scrambled numbers are sent. Taskiem answers which are people whose settings let you find them this way, then forgets what was sent. Nothing of your contacts is kept, on this phone or by Taskiem.',
  'The honest limit: Taskiem holds the key, and phone numbers are few enough to try them all. So this protects your contacts if what is sent is seen by someone else, but not from Taskiem itself.',
  'Once you agree, Taskiem checks again by itself at most once a day when you open it, while your phone still allows it, and keeps only the day it last checked. Turn it off with Check my contacts daily in Privacy with people, or by not allowing Contacts for Taskiem in your phone’s settings.',
  'Only people who allow it can be found, and you can stop at any time.',
  'When you tap Add, that person and the name you give them are kept in your contacts on Taskiem, which only you see.',
];

/// A day as kept for the daily re-check: `2026-11-18`, in Lagos time.
String contactsDay(DateTime at) {
  final l = at.toUtc().add(const Duration(hours: 1));
  return '${l.year.toString().padLeft(4, '0')}-${l.month.toString().padLeft(2, '0')}-${l.day.toString().padLeft(2, '0')}';
}

/// The daily re-check (§5.3), when You opens: only if the person agreed
/// before ([checked], the last day checked, is kept only then), not already
/// today, and the phone still allows it without asking. The people found who
/// are not in their contacts yet, or null when it did not run. Nothing is
/// kept but the day ([remember]).
Future<List<ContactMatch>?> recheckContacts({
  required TaskiemApi api,
  required PhoneContacts contacts,
  required String? checked,
  required DateTime now,
  required Future<void> Function(String day) remember,
}) async {
  final today = contactsDay(now);
  if (checked == null || checked == today) return null;
  if (!await contacts.allowed()) return null;
  final r = await matchPhoneContacts(api, await contacts.read(), day: today);
  await remember(today);
  return [for (final m in r.found) if (!m.person.inContacts) m];
}

/// Finding friends from the phone's contacts: the explainer first, the
/// phone's own question only after Agree, then who was found, each with
/// Chat and Add. Nothing is kept once the screen closes.
class ContactsMatchScreen extends StatefulWidget {
  const ContactsMatchScreen({super.key, required this.auth, this.contacts = const DevicePhoneContacts(), required this.onChat, this.found, this.onChecked});
  final Auth auth;
  final PhoneContacts contacts;

  /// Already found by the daily re-check: shown as they are.
  final List<ContactMatch>? found;

  /// The person agreed and the contacts were checked (the day is kept).
  final VoidCallback? onChecked;

  /// Starts (or opens) a chat with someone found.
  final Future<void> Function(String personId) onChat;

  @override
  State<ContactsMatchScreen> createState() => _ContactsMatchScreenState();
}

enum _Step { ask, checking, found, refused }

class _ContactsMatchScreenState extends State<ContactsMatchScreen> {
  late _Step _step = widget.found == null ? _Step.ask : _Step.found;
  late List<ContactMatch> _found = widget.found ?? const [];

  /// Stopped at the day's limit: how many numbers were checked of how many.
  ({int checked, int total})? _stopped;
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
      final r = await matchPhoneContacts(widget.auth.api, all);
      widget.onChecked?.call();
      if (mounted) setState(() => (_found = r.found, _stopped = r.stopped ? (checked: r.checked, total: r.total) : null, _step = _Step.found));
    } on ApiError catch (e) {
      if (mounted) setState(() => (_error = e.status == 429 ? 'You have checked many numbers today. Try again tomorrow.' : e.message, _step = _Step.ask));
    } on Object catch (e) {
      if (mounted) setState(() => (_error = sayFailure(e), _step = _Step.ask));
    }
  }

  Future<void> _add(ContactMatch m) async {
    // The name kept is the one given here: the phone's, unless changed.
    final name = await askContactName(context, initial: m.phoneName, theirs: m.person.name);
    if (name == null || !mounted) return;
    try {
      await widget.auth.api.addContact(body: AddContactRequest(personId: m.person.id, addToken: m.addToken, from: 'contacts', name: name.isEmpty ? null : name));
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
          if (_stopped != null)
            TkPanel(
              padding: const EdgeInsets.all(16),
              children: [
                Semantics(
                  liveRegion: true,
                  child: Text(
                    'Checked ${_stopped!.checked} of your ${_stopped!.total} numbers: that is as many as can be checked in a day. Try again tomorrow for the rest.',
                    style: tkText(size: 14, color: t.text),
                  ),
                ),
              ],
            ),
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
          Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: Text('Nothing of your contacts was kept, on this phone or by Taskiem, except the people you add and the day they were last checked.', style: tkHint(t))),
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
