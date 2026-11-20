// Saving from a message, and from the picker (saved objects S1, part 4;
// saved-objects §9; web/src/pages/SaveAs.tsx): Save as… on a message reads
// its words (POST /v1/me/saved/read) and opens the sheet for what they
// make, filled in to change before saving; Save "@mum"… in the picker,
// when nothing has that name, asks what kind it is and opens its sheet
// with the name. In a business's chat, for whoever may manage its saved
// things, they go in the business's; else in one's own.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import 'groups.dart' show pickOneOf;
import 'saved_accounts.dart';

/// Whose saved things it goes in: the business's in its chats for those
/// who may manage them.
SavedApi _savedFor(Auth auth, bool business) => SavedApi(auth, !(business && auth.can('saved.manage')));

/// A reading as the sheets start from it.
SavedCard readingCard(String kind, String title, Map<String, dynamic> details, {String name = ''}) =>
    SavedCard(kind: kind, title: title, details: details, lines: const [], name: name, sensitive: false);

/// Save as… on a message: its words read, then the sheet for what they make.
Future<void> saveFromWords(BuildContext context, Auth auth, String text, {bool business = false}) async {
  SavedCard start;
  try {
    final r = await auth.api.readSavedWords(body: ReadSavedWordsRequest(text: text));
    start = readingCard(r.kind, r.title, r.details);
  } on Object {
    start = readingCard('text', '', {'body': text});
  }
  if (!context.mounted) return;
  await _open(context, _savedFor(auth, business), start);
}

/// Save "@mum"… in the picker: what kind, then its sheet with the name.
Future<void> saveNamed(BuildContext context, Auth auth, String name, {bool business = false}) async {
  final kind = await pickOneOf(context, title: 'Save @$name', choices: [for (final e in savedKinds.entries) (e.key, e.value.one, '')]);
  if (kind == null || !context.mounted) return;
  await _open(context, _savedFor(auth, business), readingCard(kind, '', const {}, name: name));
}

Future<void> _open(BuildContext context, SavedApi api, SavedCard start) async {
  final done = start.kind == 'bank_account'
      ? (await showTkSheet<String>(context, (_) => SavedAccountSheet(api: api, checks: false, start: start))) != null
      : (await showTkSheet<bool>(context, (_) => SavedThingSheet(api: api, kind: start.kind, start: start))) == true;
  if (done && context.mounted) showTkToast(context, 'Saved');
}
