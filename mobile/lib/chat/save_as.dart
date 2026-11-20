// Saving from a message, and from the picker (saved objects S1, part 4;
// saved-objects §9; web/src/pages/SaveAs.tsx): Save as… on a message reads
// its words (POST /v1/me/saved/read) and opens the sheet for what they
// make, filled in to change before saving; Save "@mum"… in the picker,
// when nothing has that name, asks what kind it is and opens its sheet
// with the name. In a business's chat, for whoever may manage its saved
// things, they go in the business's; else in one's own.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import 'attachments.dart' show PickedFile;
import 'groups.dart' show pickOneOf;
import 'menu.dart' show MenuItem;
import 'my_chat.dart' show sayFailure;
import 'saved_files.dart' show SavedFileSheet, uploadSavedFile;
import 'saved_accounts.dart';
import 'saved_kinds.dart' show SavedCustomSheet;

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
  final api = _savedFor(auth, business);
  // The owner's own kinds first (saved objects S1, part 5).
  var made = const <SavedKind>[];
  try {
    made = (await api.kinds()).kinds;
  } on Object {
    // Only the kinds Taskiem knows, then.
  }
  if (!context.mounted) return;
  final kind = await pickOneOf(
    context,
    title: 'Save @$name',
    choices: [
      for (final m in made) ('k:${m.id}', 'A ${m.name.toLowerCase()}', ''),
      for (final e in savedKinds.entries) (e.key, e.value.one, ''),
    ],
  );
  if (kind == null || !context.mounted) return;
  if (kind.startsWith('k:')) {
    final m = made.firstWhere((m) => 'k:${m.id}' == kind);
    final done = await showTkSheet<bool>(context, (_) => SavedCustomSheet(api: api, kind: m, startName: name));
    if (done == true && context.mounted) showTkToast(context, 'Saved');
    return;
  }
  await _open(context, api, readingCard(kind, '', const {}, name: name));
}

Future<void> _open(BuildContext context, SavedApi api, SavedCard start) async {
  final done = start.kind == 'bank_account'
      ? (await showTkSheet<String>(context, (_) => SavedAccountSheet(api: api, checks: false, start: start))) != null
      : (await showTkSheet<bool>(context, (_) => SavedThingSheet(api: api, kind: start.kind, start: start))) == true;
  if (done && context.mounted) showTkToast(context, 'Saved');
}

/// Save as… on a photo or document in a chat: fetched from the chat
/// ([path]), uploaded to save (checked again), and the sheet that saves a
/// file opened with it.
Future<void> saveFromChatFile(BuildContext context, Auth auth, String path, String name, String type, {bool business = false}) async {
  final api = _savedFor(auth, business);
  try {
    final bytes = await auth.transport.bytes(path);
    final meta = await uploadSavedFile(auth, api.mine, PickedFile(bytes: Uint8List.fromList(bytes), name: name, type: type));
    if (!context.mounted) return;
    final done = await showTkSheet<bool>(context, (_) => SavedFileSheet(api: api, auth: auth, start: meta));
    if (done == true && context.mounted) showTkToast(context, 'Saved');
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
  }
}

/// A customer chat message's menu (saved objects S1): Copy and Save as… on
/// its words, and on a photo or document Save as… ("Save the words as…" and
/// "Save the file as…" when it has both). [filePath] is where its
/// file is fetched; empty when it has none to save.
List<MenuItem> saveAsItems(BuildContext context, Auth auth, {required String words, required String filePath, required String fileName, required String fileType, bool business = false}) {
  final both = words.isNotEmpty && filePath.isNotEmpty;
  return [
    if (words.isNotEmpty) MenuItem('copy', 'Copy', () => Clipboard.setData(ClipboardData(text: words)), done: 'Copied'),
    if (words.isNotEmpty) MenuItem('note', both ? 'Save the words as…' : 'Save as…', () => saveFromWords(context, auth, words, business: business)),
    if (filePath.isNotEmpty) MenuItem('note', both ? 'Save the file as…' : 'Save as…', () => saveFromChatFile(context, auth, filePath, fileName, fileType, business: business)),
  ];
}
