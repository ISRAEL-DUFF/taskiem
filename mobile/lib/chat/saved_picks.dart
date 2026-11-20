// The saved things a chat's "@" offers (saved objects S1, part 2;
// web/src/pages/SavedPicks.ts): the person's own (any session reaches them)
// and, in a business, the business's, read the first time "@" is typed in
// the chat. Every kind in the Taskiem chat, which reads an @name; in chats
// with other people a text's words go in the box and anything else is
// shared as a card (part 3), only one's own with people. A list that cannot be read
// offers nothing rather than stopping the chat.

import 'dart:async';

import '../api/api.g.dart';
import '../app/auth.dart';
import 'package:flutter/widgets.dart' show TextEditingController, VoidCallback;

import 'tag_box.dart';
import 'tags.dart';

class SavedPicks {
  SavedPicks({required this.auth, required this.textsOnly, required this.onChange, this.mineOnly = false});
  final Auth auth;
  final bool textsOnly;

  /// Only the person's own (a chat with people: a business's are not
  /// shared there).
  final bool mineOnly;

  /// The list came: the picker is drawn again.
  final void Function() onChange;

  List<TagSuggestion> list = const [];
  bool _wanted = false;

  /// The lists were read (each, or found unreadable).
  bool loaded = false;

  /// Both lists were read: Save "@x"… is offered only then, so a name is
  /// never offered as new because a list could not be read.
  bool complete = false;

  /// Read again (something was saved): what the picker offers is current.
  void reload() {
    if (!_wanted) return;
    unawaited(_load());
  }

  /// Saves anywhere in the app: a list read before the last one is read
  /// again the next time "@" is typed.
  static int saves = 0;
  int _readAt = 0;

  /// "@" was typed: read them, once (again after something was saved).
  void want() {
    if (_wanted && _readAt == saves) return;
    _wanted = true;
    unawaited(_load());
  }

  Future<void> _load() async {
    _readAt = saves;
    List<SavedObject> mine = const [], business = const [];
    var ok = true;
    try {
      mine = (await auth.api.listMySaved()).saved;
    } on Object {
      // Nothing of one's own to offer.
      ok = false;
    }
    if (!auth.personal && !mineOnly) {
      try {
        business = (await auth.api.listSaved()).saved;
      } on Object {
        // Nothing of the business's to offer.
        ok = false;
      }
    }
    list = savedTags(mine, business, textsOnly: textsOnly);
    loaded = true;
    complete = ok;
    onChange();
  }
}

/// lib/saved.ts savedSuggestions: the person's own first, then the
/// business's, each by its @name with its title and hint, saying whose when
/// two share a name; a text with its words.
List<TagSuggestion> savedTags(List<SavedObject> mine, List<SavedObject> business, {required bool textsOnly}) {
  final all = [for (final x in [...mine, ...business]) if (!textsOnly || x.kind == 'text') x];
  final names = <String, int>{};
  for (final x in all) {
    names[x.name] = (names[x.name] ?? 0) + 1;
  }
  return [
    for (final x in all)
      TagSuggestion(
        kind: 'saved',
        id: x.id,
        name: x.name,
        sub: [x.title, x.hint, if ((names[x.name] ?? 0) > 1) x.owner == 'mine' ? 'Yours' : 'The business’s'].where((w) => w.isNotEmpty).join(' · '),
        words: x.title,
        savedKind: x.kind,
        savedOwner: x.owner,
        savedSensitive: x.shareSensitive ?? savedSensitive(x.kind, x.details),
        fileName: x.kind == 'file' ? (x.details.fileName ?? x.title) : null,
        fileType: x.kind == 'file' ? (x.details.fileType ?? '') : null,
        body: x.kind == 'text' ? (x.details.body ?? '') : null,
      ),
  ];
}

/// saved.MakeCard's rule (an account's number, a person's phone or email,
/// a place's phone), for a server that does not say share_sensitive.
bool savedSensitive(String kind, SavedObjectDetails d) => switch (kind) {
  'bank_account' => true,
  'person' => (d.phone ?? '').isNotEmpty || (d.email ?? '').isNotEmpty,
  'place' => (d.phone ?? '').isNotEmpty,
  _ => false,
};

/// The server's question before sharing what is sensitive (shareQuestion):
/// "Share Mum's account details with Bola Stores?".
String shareQuestion(TagSuggestion s, String to) {
  final what = switch (s.savedKind) {
    'bank_account' => 'account details',
    'person' || 'place' => 'phone number and details',
    _ => 'details',
  };
  return "Share ${(s.words ?? '').isNotEmpty ? s.words : s.name}'s $what with ${to.isEmpty ? 'them' : to}?";
}

/// A tag box offering saved things alone (saved objects S1): for the chats
/// with no other "@" (a customer's, a business's with its customer, people
/// chats and groups). Texts alone, or with [onShare] every kind, the rest
/// shared as cards ([mineOnly]: only the person's own). The screen calls
/// typed() as the words change, draws plain() over its box, and disposes
/// it.
TagBox savedTextBox({required TextEditingController text, required Auth auth, required VoidCallback onChange, void Function(TagSuggestion)? onShare, bool mineOnly = false, void Function(String name)? onSaveNamed, void Function(TagSuggestion)? onAttach}) => TagBox(
  text: text,
  sources: () => (items: const <TagSuggestion>[], services: const <TagSuggestion>[], businesses: const <TagSuggestion>[], refs: const <String, List<TagSuggestion>>{}),
  grouped: () => false,
  taskiem: false,
  onChange: onChange,
  saved: SavedPicks(auth: auth, textsOnly: onShare == null, mineOnly: mineOnly, onChange: onChange),
  onShareSaved: onShare,
  onSaveNamed: onSaveNamed,
  onAttachSaved: onAttach,
);
