// People chatting with people (docs/spec/people-chatting.md, decision
// 0069): what the screens of people chats share. A person's face (their
// photo when their setting lets you see it), their last seen and online in
// words, a message in a line, the warnings under a stranger's message, and
// the plain words of where people chats are kept (§6.2, §6.3). People chats
// are kept by Taskiem and readable by its servers: nothing here says or
// suggests they are end-to-end encrypted.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/session.dart' show uuid4;
import '../kit/api_image.dart';
import '../kit/icon.dart';
import '../kit/mobile.dart' show TkFormGroup, TkFormSheet;
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'chats.dart' show ChatAvatar;
import 'words.dart';

/// Where people chats are kept and who reads them (§6.2, §6.3), as a
/// people chat's info and Privacy say it. Wording for legal review (§17
/// Q13).
const peopleKeptWords =
    'Your chats with people are kept by Taskiem, sealed on its servers, so they show on every phone you sign in on. '
    'Taskiem’s servers can open them: they are not end-to-end encrypted. '
    'No one at Taskiem reads them, except a moderator when you report a chat, or when the law requires it. '
    'Automated checks look for scams in messages from people you don’t know. Your chats are never used for ads or to train AI.';

/// The answer of a chat that is not a person's (a deleted account).
const deletedAccount = 'Deleted account';

/// A suspended account (not a deleted one): its name shows, nothing is
/// sent to it (the server's 409 says the same).
const suspendedWords = 'This account can’t get messages right now.';

/// A new client id for a send (`client_id`): kept with the send while
/// it has no answer, so the same sent again is kept once.
String newClientId(DateTime at, int n) => '${at.millisecondsSinceEpoch}-$n-${uuid4().substring(0, 8)}';

String _str(Object? v) => v is String ? v : '';

/// A person's photo's path (`/v1/people/{id}/photo?v=…`), when their
/// setting lets you see it.
String? personPhotoPath(Object? photo) {
  if (photo is! Map) return null;
  final url = _str(photo['url']);
  return url.startsWith('/v1/') ? url : null;
}

/// Online, or when they were last seen ("last seen today at 14:05"), when
/// both your settings and theirs allow it (§5.4); null when not shown.
String? lastSeenWords(PersonCard? p, DateTime now) {
  if (p == null || p.deleted) return null;
  if (p.online == true) return 'online';
  final at = p.lastSeenAt is String ? DateTime.tryParse(p.lastSeenAt as String) : null;
  if (at == null) return null;
  final day = dayLabel(at, now);
  if (day == 'Today') return 'last seen today at ${clock(at)}';
  if (day == 'Yesterday') return 'last seen yesterday at ${clock(at)}';
  return 'last seen ${dayWords(at, now)}';
}

/// A person's face: their photo when shown, else their letters on their
/// name's colour; a deleted account's is plain letters.
class PersonFace extends StatelessWidget {
  const PersonFace({super.key, required this.name, required this.transport, this.photo, this.size = 48, this.letterSize, this.online = false});
  final String name;
  final Transport transport;

  /// The card's photo (`{url, updated_at}`), or null.
  final Object? photo;
  final double size;
  final double? letterSize;

  /// A small lime dot when they are online now.
  final bool online;

  @override
  Widget build(BuildContext context) {
    final path = personPhotoPath(photo);
    final letters = ChatAvatar(name: name, size: size, letterSize: letterSize);
    final face = path == null
        ? letters
        : SizedBox(
            width: size,
            height: size,
            child: ClipOval(
              child: Stack(
                fit: StackFit.expand,
                children: [letters, ApiImage(transport: transport, path: path, width: size, height: size)],
              ),
            ),
          );
    if (!online) return face;
    final look = TkLook.of(context);
    return Stack(
      clipBehavior: Clip.none,
      children: [
        face,
        Positioned(
          right: 0,
          bottom: 0,
          // Said to a screen reader too, not only drawn.
          child: Semantics(
            label: 'Online',
            child: Container(
              width: size / 4,
              height: size / 4,
              decoration: BoxDecoration(color: look.lime, shape: BoxShape.circle, border: Border.all(color: look.card, width: 2)),
            ),
          ),
        ),
      ],
    );
  }
}

/// A people message in a line: a list's last line, a quote, what is
/// forwarded.
String peopleLine(PeopleMessage m) {
  if (m.deleted == true) return 'This message was deleted';
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

/// The warning under a message from someone you don't know that the checks
/// marked (§8.4): asking for money, a code, a fee or a prize.
const strangerWarning = 'Be careful: this message looks like ones scammers send. Never send money, a code or your card details to someone you don’t know.';

/// The warning under a message asking for a transfer to an account (§8.4,
/// the plan's §15).
const peopleTransferWarning = 'Be careful with requests to send money to an account. Taskiem never asks you to. Only pay people you know.';

/// Under a stranger's message the checks marked, on the receiver's side.
class PeopleWarning extends StatelessWidget {
  const PeopleWarning(this.words, {super.key});
  final String words;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Semantics(
      container: true,
      label: 'Warning: $words',
      excludeSemantics: true,
      child: Container(
        margin: const EdgeInsets.only(top: 4),
        constraints: const BoxConstraints(maxWidth: 340),
        padding: const EdgeInsets.fromLTRB(10, 8, 12, 8),
        decoration: BoxDecoration(color: look.red.withValues(alpha: 0.12), borderRadius: BorderRadius.circular(14)),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(padding: const EdgeInsets.only(top: 1), child: TkIcon('warn', size: 16, stroke: 2, color: look.redInk)),
            const SizedBox(width: 8),
            Expanded(child: Text(words, style: tkText(size: 13, weight: 600, lineHeight: 1.35, color: TkTheme.of(context).mode == TkMode.dark ? t.text : look.redInk))),
          ],
        ),
      ),
    );
  }
}

/// The reasons a person can give for reporting someone (§8.3).
const peopleReportReasons = [('spam', 'Spam'), ('scam', 'A scam or fraud'), ('abuse', 'Abuse or threats'), ('other', 'Something else')];

/// The name you give someone in your contacts (only you see it): asked in
/// a modal; empty for their own name; null when put away.
Future<String?> askContactName(BuildContext context, {required String initial, required String theirs}) =>
    showTkSheet<String>(context, (_) => _NameForm(initial: initial, theirs: theirs));

class _NameForm extends StatefulWidget {
  const _NameForm({required this.initial, required this.theirs});
  final String initial;
  final String theirs;

  @override
  State<_NameForm> createState() => _NameFormState();
}

class _NameFormState extends State<_NameForm> {
  late final _box = TextEditingController(text: widget.initial);

  @override
  void dispose() {
    _box.dispose();
    super.dispose();
  }

  // A form sheet of the app's (as Saved addresses'): the name in a form
  // group, Save and Cancel at its foot.
  @override
  Widget build(BuildContext context) => TkFormSheet(
    title: 'Their name in your contacts',
    crumb: 'Only you see it',
    action: 'Save',
    onAction: () => Navigator.of(context).pop(_box.text.trim()),
    second: 'Cancel',
    onSecond: () => Navigator.of(context).pop(),
    children: [
      TkFormGroup(children: [TkKitField(label: 'Name', hint: 'Leave it empty for their own name.', child: TkTextBox(controller: _box, hint: widget.theirs, max: 80, size: 15, height: 46))]),
    ],
  );
}
