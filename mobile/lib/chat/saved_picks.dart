// The saved things a chat's "@" offers (saved objects S1, part 2;
// web/src/pages/SavedPicks.ts): the person's own (any session reaches them)
// and, in a business, the business's, read the first time "@" is typed in
// the chat. Every kind in the Taskiem chat, which reads an @name; texts only
// in chats with other people, until cards. A list that cannot be read
// offers nothing rather than stopping the chat.

import 'dart:async';

import '../api/api.g.dart';
import '../app/auth.dart';
import 'package:flutter/widgets.dart' show TextEditingController, VoidCallback;

import 'tag_box.dart';
import 'tags.dart';

class SavedPicks {
  SavedPicks({required this.auth, required this.textsOnly, required this.onChange});
  final Auth auth;
  final bool textsOnly;

  /// The list came: the picker is drawn again.
  final void Function() onChange;

  List<TagSuggestion> list = const [];
  bool _wanted = false;

  /// "@" was typed: read them, once.
  void want() {
    if (_wanted) return;
    _wanted = true;
    unawaited(_load());
  }

  Future<void> _load() async {
    List<SavedObject> mine = const [], business = const [];
    try {
      mine = (await auth.api.listMySaved()).saved;
    } on Object {
      // Nothing of one's own to offer.
    }
    if (!auth.personal) {
      try {
        business = (await auth.api.listSaved()).saved;
      } on Object {
        // Nothing of the business's to offer.
      }
    }
    list = savedTags(mine, business, textsOnly: textsOnly);
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
        body: x.kind == 'text' ? (x.details.body ?? '') : null,
      ),
  ];
}

/// A tag box offering saved texts alone (saved objects S1): for the chats
/// with no other "@" (a customer's, a business's with its customer, people
/// chats and groups). The screen calls typed() as the words change, draws
/// plain() over its box, and disposes it.
TagBox savedTextBox({required TextEditingController text, required Auth auth, required VoidCallback onChange}) => TagBox(
  text: text,
  sources: () => (items: const <TagSuggestion>[], services: const <TagSuggestion>[], businesses: const <TagSuggestion>[], refs: const <String, List<TagSuggestion>>{}),
  grouped: () => false,
  taskiem: false,
  onChange: onChange,
  saved: SavedPicks(auth: auth, textsOnly: true, onChange: onChange),
);
