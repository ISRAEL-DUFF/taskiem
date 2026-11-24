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
import 'pay_chip.dart' show collectFromChatPhoto, mayPayFromMessages, payFromChatPhoto;
import 'pay_collect.dart' show MessageFrom;
import 'photo_text.dart' show PhotoAI, noWordsInPhoto, readChatPhoto;
import 'saved_files.dart' show SavedFileSheet, uploadSavedFile;
import 'saved_accounts.dart';
import 'saved_kinds.dart' show SavedCustomSheet;
import 'saved_picks.dart' show SavedPicks;

/// Something was saved: the chats' pickers read their lists again.
void _saved(BuildContext context, String words) {
  SavedPicks.saves++;
  if (context.mounted) showTkToast(context, words);
}

/// A chat's file being got ready to save: one at a time.
bool _fileBusy = false;

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
    choices: [for (final m in made) ('k:${m.id}', 'A ${m.name.toLowerCase()}', ''), for (final e in savedKinds.entries) (e.key, e.value.one, '')],
  );
  if (kind == null || !context.mounted) return;
  if (kind.startsWith('k:')) {
    final m = made.firstWhere((m) => 'k:${m.id}' == kind);
    final done = await showTkSheet<bool>(context, (_) => SavedCustomSheet(api: api, kind: m, startName: name));
    if (done == true && context.mounted) _saved(context, 'Saved');
    return;
  }
  // A photo or document: its own sheet, with the name.
  if (kind == 'file') {
    final done = await showTkSheet<bool>(context, (_) => SavedFileSheet(api: api, auth: api.auth, startName: name));
    if (done == true && context.mounted) _saved(context, 'Saved');
    return;
  }
  await _open(context, api, readingCard(kind, '', const {}, name: name));
}

Future<void> _open(BuildContext context, SavedApi api, SavedCard start) async {
  final done = start.kind == 'bank_account'
      ? (await showTkSheet<String>(context, (_) => SavedAccountSheet(api: api, checks: false, start: start))) != null
      : (await showTkSheet<bool>(context, (_) => SavedThingSheet(api: api, kind: start.kind, start: start))) == true;
  if (done && context.mounted) _saved(context, 'Saved');
}

/// Save as… on a photo or document in a chat: fetched from the chat
/// ([path]), uploaded to save (checked again), and the sheet that saves a
/// file opened with it.
Future<void> saveFromChatFile(BuildContext context, Auth auth, String path, String name, String type, {bool business = false}) async {
  final api = _savedFor(auth, business);
  if (_fileBusy) {
    showTkToast(context, 'Another file is being got ready: wait a moment.');
    return;
  }
  _fileBusy = true;
  // Said at once: fetching and checking a file takes a moment.
  showTkToast(context, 'Getting $name ready to save…');
  try {
    final bytes = await auth.transport.bytes(path);
    final meta = await uploadSavedFile(auth, api.mine, PickedFile(bytes: Uint8List.fromList(bytes), name: name, type: type));
    _fileBusy = false;
    if (!context.mounted) return;
    final done = await showTkSheet<bool>(context, (_) => SavedFileSheet(api: api, auth: auth, start: meta));
    if (done == true && context.mounted) _saved(context, 'Saved');
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
  } finally {
    _fileBusy = false;
  }
}

/// Save the account on a photo in a chat (reading account details from
/// photos, I1): the photo read on the phone, its words read by Taskiem
/// (POST /v1/me/saved/read, `from: "photo"`), and Save as…'s sheet for an
/// account opened with them. No account in it: said, or, where the
/// business allows it, "Read it with AI" offered (I3), and its words read
/// the same way.
Future<void> saveAccountFromChatPhoto(BuildContext context, Auth auth, String path, {bool business = false}) async {
  final photo = await readChatPhoto(context, auth, path);
  if (photo == null || !context.mounted) return;
  if (await _saveFromPhotoWords(context, auth, photo.words, business) || !context.mounted) return;
  final words = await PhotoAI(auth).offer(
    context,
    photo,
    title: photo.words.isEmpty ? 'This phone read no words in it' : 'This phone found no account number',
    said: photo.words.isEmpty ? noWordsInPhoto : 'No account number in that photo.',
  );
  if (words == null || !context.mounted) return;
  if (!await _saveFromPhotoWords(context, auth, words, business) && context.mounted) showTkToast(context, 'No account number in that photo.');
}

/// A photo's words read by Taskiem and the account's sheet opened: false
/// when they hold no account (nothing said), true when done or said.
Future<bool> _saveFromPhotoWords(BuildContext context, Auth auth, String words, bool business) async {
  if (words.isEmpty) return false;
  final ReadSavedWordsResponse r;
  try {
    r = await auth.api.readSavedWords(
      body: ReadSavedWordsRequest(text: words, from: 'photo'),
    );
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
    return true;
  }
  if (!context.mounted) return true;
  if (r.kind != 'bank_account' || '${r.details['account_number'] ?? ''}'.isEmpty) return false;
  await _open(context, _savedFor(auth, business), readingCard(r.kind, r.title, r.details));
  return true;
}

/// A photo's Pay this account (for those who may pay, as the chip under a
/// message) and Save the account, each read from the photo on the phone
/// first; [filePath] is where the chat fetches it. With [from] (the
/// message), "Add to the list" beside Pay this account.
List<MenuItem> photoAccountItems(BuildContext context, Auth auth, String filePath, {bool business = false, MessageFrom? from}) => [
  if (mayPayFromMessages(auth)) MenuItem('money', 'Pay this account', () => payFromChatPhoto(context, auth, filePath)),
  if (mayPayFromMessages(auth) && from != null) MenuItem('plus', 'Add to the list', () => collectFromChatPhoto(context, auth, filePath, from)),
  MenuItem('card', 'Save the account', () => saveAccountFromChatPhoto(context, auth, filePath, business: business)),
];

/// A customer chat message's menu (saved objects S1): Copy and Save as… on
/// its words, and on a photo or document Save as… ("Save the words as…" and
/// "Save the file as…" when it has both). [filePath] is where its
/// file is fetched; empty when it has none to save. A photo also has
/// [photoAccountItems].
List<MenuItem> saveAsItems(
  BuildContext context,
  Auth auth, {
  required String words,
  required String filePath,
  required String fileName,
  required String fileType,
  bool business = false,
  MessageFrom? from,
}) {
  final both = words.isNotEmpty && filePath.isNotEmpty;
  final photo = filePath.isNotEmpty && fileType.startsWith('image/');
  return [
    if (words.isNotEmpty) MenuItem('copy', 'Copy', () => Clipboard.setData(ClipboardData(text: words)), done: 'Copied'),
    if (words.isNotEmpty) MenuItem('note', both ? 'Save the words as…' : 'Save as…', () => saveFromWords(context, auth, words, business: business)),
    if (filePath.isNotEmpty)
      MenuItem('note', both ? 'Save the file as…' : 'Save as…', () => saveFromChatFile(context, auth, filePath, fileName, fileType, business: business)),
    if (photo) ...photoAccountItems(context, auth, filePath, business: business, from: from),
  ];
}
