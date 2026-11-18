// You's settings for chats with people (docs/spec/people-chatting.md §5.4,
// §8.1): who can find you and message you, who sees your last seen and
// photo, whether your number shows to people you chat with, read ticks
// (`/v1/me/privacy/people`); and the people you blocked, each unblocked
// in one tap (`/v1/me/people/blocked`).

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../app/prefs.dart' show Prefs;
import '../kit/forms.dart' show TkFormRow, TkSwitchRow;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../screens/settings_more.dart' show SettingsCard, SettingsNote, SettingsPage;
import 'my_chat.dart' show sayFailure;
import 'people.dart';
import 'people_standing.dart' show openTransparency;
import 'words.dart';

/// An audience in words.
String audienceWords(String a) => switch (a) {
  'everyone' => 'Everyone',
  'contacts' => 'My contacts',
  'nobody' => 'Nobody',
  _ => a,
};

/// Who can message me, in words: everyone writes as a request.
String messageWords(String a) => a == 'everyone' ? 'Everyone, as a request' : audienceWords(a);

const _audiences = ['everyone', 'contacts', 'nobody'];

/// The daily re-check's switch, in words.
const checkContactsDaily = 'Check my contacts daily';
const checkContactsDailyOn = 'Once a day when you open Taskiem, it checks which of your phone’s contacts are on Taskiem. Only the day is kept';
const checkContactsDailyOff = 'Off. It comes on when you check From your contacts in New chat';

/// Privacy with people (§5.4).
class PeoplePrivacyPage extends StatefulWidget {
  const PeoplePrivacyPage({super.key, required this.auth, this.prefs});
  final Auth auth;

  /// What this phone keeps: Check my contacts daily is on while the day of
  /// the last check is kept (§5.3).
  final Prefs? prefs;

  @override
  State<PeoplePrivacyPage> createState() => _PeoplePrivacyPageState();
}

class _PeoplePrivacyPageState extends State<PeoplePrivacyPage> {
  PeoplePrivacy? _p;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final p = await widget.auth.api.myPeoplePrivacy();
      if (mounted) setState(() => (_p = p, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  /// One change: shown at once, put back if refused.
  Future<void> _put(PutMyPeoplePrivacyRequest body, PeoplePrivacy shown) async {
    final was = _p;
    setState(() => (_p = shown, _busy = true));
    try {
      final p = await widget.auth.api.putMyPeoplePrivacy(body: body);
      if (mounted) setState(() => _p = p);
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _p = was);
      showTkToast(context, sayFailure(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  PeoplePrivacy _with({String? findByNumber, bool? findFromContacts, String? message, String? lastSeen, String? photo, bool? showNumber, bool? readTicks}) {
    final p = _p!;
    return PeoplePrivacy(
      findByNumber: findByNumber ?? p.findByNumber,
      findFromContacts: findFromContacts ?? p.findFromContacts,
      groupsAdd: p.groupsAdd,
      message: message ?? p.message,
      lastSeen: lastSeen ?? p.lastSeen,
      photo: photo ?? p.photo,
      showNumber: showNumber ?? p.showNumber,
      readTicks: readTicks ?? p.readTicks,
    );
  }

  Future<String?> _choose(String title, String value, {bool message = false}) => showTkModalChoices(
    context,
    title: title,
    value: value,
    choices: [for (final a in _audiences) (a, message ? messageWords(a) : audienceWords(a))],
  );

  @override
  Widget build(BuildContext context) {
    final p = _p;
    Widget choice(String title, String sub, String value, Future<void> Function() onTap) => TkFormRow(
      title: title,
      sub: sub,
      trailing: TkSmallButton(label: value, semantics: '$title: $value. Change', onPressed: _busy ? null : onTap),
    );
    return SettingsPage(
      title: 'Privacy with people',
      backLabel: 'Back to Settings',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (p == null && _error == null) const TkLoading(label: 'Loading…'),
        if (p != null) ...[
          SettingsCard(
            title: 'Who can find you',
            children: [
              choice('By my number', 'Who finds you when they type your number', audienceWords(p.findByNumber), () async {
                final v = await _choose('Who can find you by your number', p.findByNumber);
                if (v != null && v != p.findByNumber) await _put(PutMyPeoplePrivacyRequest(findByNumber: v), _with(findByNumber: v));
              }),
              TkSwitchRow(
                label: 'From their contacts',
                hint: 'People with your number in their phone can find you, as far as By my number lets them',
                on: p.findFromContacts,
                onChanged: _busy ? null : (v) => _put(PutMyPeoplePrivacyRequest(findFromContacts: v), _with(findFromContacts: v)),
              ),
            ],
          ),
          // The daily re-check of the phone's contacts (§5.3): on once the
          // person agreed and their contacts were checked; off, the day is
          // forgotten and nothing is checked by itself. Turned on again only
          // by checking From your contacts (the explainer first).
          if (widget.prefs case final prefs?)
            SettingsCard(
              title: 'Your phone’s contacts',
              children: [
                ListenableBuilder(
                  listenable: prefs,
                  builder: (context, _) {
                    final on = prefs.contactsChecked != null;
                    return TkSwitchRow(
                      label: checkContactsDaily,
                      hint: on ? checkContactsDailyOn : checkContactsDailyOff,
                      on: on,
                      onChanged: on ? (_) => prefs.setContactsChecked(null) : null,
                    );
                  },
                ),
              ],
            ),
          SettingsCard(
            title: 'Who can message you',
            children: [
              choice('New chats', p.message == 'everyone' ? 'People you haven’t added wait in Requests' : (p.message == 'contacts' ? 'Only people you added' : 'No one can start a chat with you'), messageWords(p.message), () async {
                final v = await _choose('Who can message you', p.message, message: true);
                if (v != null && v != p.message) await _put(PutMyPeoplePrivacyRequest(message: v), _with(message: v));
              }),
            ],
          ),
          SettingsCard(
            title: 'What people see',
            children: [
              choice('Last seen and online', 'If you hide yours, you don’t see theirs', audienceWords(p.lastSeen), () async {
                final v = await _choose('Who sees your last seen and online', p.lastSeen);
                if (v != null && v != p.lastSeen) await _put(PutMyPeoplePrivacyRequest(lastSeen: v), _with(lastSeen: v));
              }),
              choice('Profile photo', 'Who sees your photo', audienceWords(p.photo), () async {
                final v = await _choose('Who sees your photo', p.photo);
                if (v != null && v != p.photo) await _put(PutMyPeoplePrivacyRequest(photo: v), _with(photo: v));
              }),
              TkSwitchRow(
                label: 'Show my number',
                hint: 'To people you chat with or added. Off: they see only your name',
                on: p.showNumber,
                onChanged: _busy ? null : (v) => _put(PutMyPeoplePrivacyRequest(showNumber: v), _with(showNumber: v)),
              ),
              TkSwitchRow(
                label: 'Read ticks',
                hint: 'Off: people don’t see when you read, and you don’t see when they do',
                on: p.readTicks,
                onChanged: _busy ? null : (v) => _put(PutMyPeoplePrivacyRequest(readTicks: v), _with(readTicks: v)),
              ),
            ],
          ),
          const SettingsNote(peopleKeptWords),
          // Lawful requests are counted and published (§8.5).
          SettingsCard(
            title: 'Requests from the authorities',
            children: [
              TkFormRow(
                title: 'How many Taskiem received',
                sub: 'Taskiem answers only proper legal process and publishes the count every half-year',
                trailing: TkSmallButton(label: 'See', semantics: 'See how many requests from the authorities Taskiem received', onPressed: () => openTransparency(context)),
              ),
            ],
          ),
        ],
      ],
    );
  }
}

/// The day someone was blocked, after "Blocked": today, yesterday, or
/// its date as written (Tue 14 Oct, not tue 14 oct).
String blockedDay(DateTime at, DateTime now) {
  final d = dayWords(at, now);
  return d == 'Today' || d == 'Yesterday' ? d.toLowerCase() : 'on $d';
}

/// People you blocked (§8.1): they cannot message you or see your last
/// seen and photo; Unblock delivers nothing they sent meanwhile.
class BlockedPeopleScreen extends StatefulWidget {
  const BlockedPeopleScreen({super.key, required this.auth, this.now = DateTime.now});
  final Auth auth;
  final DateTime Function() now;

  @override
  State<BlockedPeopleScreen> createState() => _BlockedPeopleScreenState();
}

class _BlockedPeopleScreenState extends State<BlockedPeopleScreen> {
  List<ListPeopleBlockedResponsePeopleItem>? _people;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listPeopleBlocked();
      if (mounted) setState(() => (_people = r.people, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _unblock(ListPeopleBlockedResponsePeopleItem p) async {
    final yes = await showTkConfirm(
      context,
      title: 'Unblock ${p.name}?',
      body: 'They can message you again. Nothing they sent while blocked comes.',
      ok: 'Unblock',
    );
    if (!yes) return;
    try {
      await widget.auth.api.unblockPerson(id: p.chatId);
      await _load();
      if (mounted) showTkToast(context, 'Unblocked ${p.name}');
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final list = _people;
    final now = widget.now();
    return TkScreen(
      title: 'Blocked people',
      onRefresh: _load,
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (list == null && _error == null) const TkLoading(label: 'Loading…'),
        if (list != null && list.isEmpty) const TkPanel(children: [TkEmpty(icon: 'b-x-ring', title: 'No one blocked', line: 'People you block show here.')]),
        if (list != null && list.isNotEmpty)
          TkPanel(
            children: [
              for (final p in list)
                TkListRow(
                  title: p.name,
                  sub: 'Blocked ${blockedDay(p.at, now)}',
                  letters: p.name,
                  trailing: TkSmallButton(label: 'Unblock', semantics: 'Unblock ${p.name}', onPressed: () => _unblock(p)),
                ),
            ],
          ),
      ],
    );
  }
}
