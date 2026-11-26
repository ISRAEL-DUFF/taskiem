// Editing a sent message (docs/spec/chat-previews-and-editing.md §3): the
// rules the hold menu shows Edit by, the same in every chat and as the
// web's (web/src/lib/edits.ts). The server decides (PATCH …/messages/{id}
// {text, edits}); these only keep Edit off where it would be refused.

import '../api/transport.dart';

/// How long after sending a message may be edited.
const editWindow = Duration(minutes: 30);

/// The # tags of a price, an invoice or an order: a message with one is
/// not edited.
const _moneyTags = {'item', 'service', 'invoice', 'bill', 'order', 'purchase'};

/// Why the person may not edit a message now (the server's not_editable
/// reasons), or null when they may.
String? editBlock({
  required bool mine,
  required String kind,
  required DateTime createdAt,
  required DateTime now,
  bool deleted = false,
  bool forwarded = false,
  bool card = false,
  bool money = false,
  Iterable<String> tags = const [],
  String? fileKind,
  String? line,
  bool local = false,
}) {
  if (deleted) return 'deleted';
  if (card || (kind != 'text' && kind != 'media')) return 'card';
  if (!mine || local) return 'not_yours';
  if (money || tags.any(_moneyTags.contains)) return 'money';
  if (tags.contains('saved')) return 'card';
  if (fileKind == 'voice') return 'voice';
  if (forwarded) return 'forwarded';
  if (line == 'whatsapp') return 'elsewhere';
  if (now.difference(createdAt) > editWindow) return 'too_old';
  return null;
}

/// The words a refused edit says, for its toast or its note.
String editRefusal(Object e) {
  if (e is ApiError) {
    if (e.has('stale')) return 'This message was changed on another device. Its newer words are shown.';
    if (e.body['not_editable'] == 'too_old') return 'Your edit was not saved: the message can only be edited for 30 minutes after it was sent';
    return e.message;
  }
  return 'You’re offline. Try again when you’re back online.';
}

/// An edit refused for its time: its words go back to the box, to send as
/// a new message.
bool refusedForTime(Object e) => e is ApiError && e.body['not_editable'] == 'too_old';

/// A refusal for its time, or stale: the words go back (or a note offers
/// them as a new message) rather than trying again.
bool editRefusedForGood(Object e) => e is ApiError && (e.status == 403 || e.status == 409 || e.status == 422);
