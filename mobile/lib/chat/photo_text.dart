// Reading account details from photos (I1): the words in a photo are read
// ON THE PHONE, with Google ML Kit's text recognition; the photo itself is
// never sent to be read. Only the words go to Taskiem's readers (POST
// /v1/payments/read, /v1/payment-batches/read, /v1/me/saved/read), with
// `from: "photo"`, which puts right the letters a phone reads in place of
// digits. A test stands in for the reader ([photoText]), the picker
// ([pickPhotoToRead]) and the folder a chat's photo is put in for a moment
// ([photoTempDir]).
//
// Reading photos with AI (I3): where an owner turned it on, a photo the
// phone could not read (handwriting) may be sent to Taskiem's AI, once the
// person taps "Read it with AI" ([PhotoAI], [readPhotoWithAI]); it is not
// kept, and its words come back to be read as the phone's are.

import 'dart:async';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:google_mlkit_text_recognition/google_mlkit_text_recognition.dart';
import 'package:image_picker/image_picker.dart' show ImagePicker, ImageSource;
import 'package:path_provider/path_provider.dart';

import '../api/api.g.dart' show AIPhotoReading;
import '../api/transport.dart' show ApiError, Offline;
import '../app/auth.dart';
import '../app/lock.dart' show stepOut;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/modal.dart' show showTkToast;
import '../theme/look.dart' show TkLook;
import '../theme/theme.dart';
import 'groups.dart' show pickAction;

/// Reads the words in a photo on the phone.
abstract class PhotoTextReader {
  /// The words in the photo at [imagePath], a line of it a line; '' when
  /// it has none.
  Future<String> read(String imagePath);
}

/// One line read from a photo: where it is, and its words.
typedef PhotoLine = ({Rect box, String text});

/// The lines in reading order, one a line: top to bottom, and left to
/// right along a row. Lines side by side on a row (a list's name, number
/// and bank in columns) are one row when they overlap by half the height
/// of the shorter, and are put on one line with a space between.
String photoLinesText(List<PhotoLine> lines) {
  final sorted = [
    for (final l in lines)
      if (l.text.trim().isNotEmpty) l,
  ]..sort((a, b) => a.box.top != b.box.top ? a.box.top.compareTo(b.box.top) : a.box.left.compareTo(b.box.left));
  final rows = <List<PhotoLine>>[];
  for (final l in sorted) {
    final row = rows.isEmpty ? null : rows.last;
    if (row != null) {
      final first = row.first.box;
      final overlap = (first.bottom < l.box.bottom ? first.bottom : l.box.bottom) - l.box.top;
      final shorter = first.height < l.box.height ? first.height : l.box.height;
      if (shorter > 0 && overlap >= shorter / 2) {
        row.add(l);
        continue;
      }
    }
    rows.add([l]);
  }
  return [
    for (final row in rows) ([...row]..sort((a, b) => a.box.left.compareTo(b.box.left))).map((l) => l.text.trim()).join(' '),
  ].join('\n');
}

/// ML Kit's text recognition, Latin letters, on the phone. A recognizer
/// is made for each photo and closed after it.
class MlKitPhotoText implements PhotoTextReader {
  const MlKitPhotoText();

  @override
  Future<String> read(String imagePath) async {
    final recognizer = TextRecognizer(script: TextRecognitionScript.latin);
    try {
      final r = await recognizer.processImage(InputImage.fromFilePath(imagePath));
      return photoLinesText([
        for (final b in r.blocks)
          for (final l in b.lines) (box: l.boundingBox, text: l.text),
      ]);
    } finally {
      await recognizer.close();
    }
  }
}

/// The reader the app uses (a test puts its own in).
PhotoTextReader photoText = const MlKitPhotoText();

/// The long edge a photo is made no larger than, as the composer does:
/// the most the AI model reads at full detail, and well within its 5 MB.
const photoSide = 2576.0;

/// The most a photo sent to be read with AI may be (the model's limit).
const maxAIPhotoBytes = 5 << 20;

/// Said when a photo is too large to be read with AI.
const photoTooLarge = 'That photo is too large to read: try a smaller one.';

/// Said with the words when the AI's answer was cut off.
const photoListLong = 'The list was long: check the last lines.';

/// A photo taken now or chosen from the phone's photos, as a file on the
/// phone (the picker's own copy, made smaller and a JPEG, as the composer
/// does: an iPhone's HEIC photo becomes one too); null when put away (a
/// test puts its own in).
Future<String?> Function(ImageSource from) pickPhotoToRead = (from) async {
  final x = await stepOut(
    () => ImagePicker().pickImage(source: from, imageQuality: 85, requestFullMetadata: false, maxWidth: photoSide, maxHeight: photoSide),
  );
  return x?.path;
};

/// Where a chat's photo is put for a moment to be read (a test puts its
/// own in).
Future<Directory> Function() photoTempDir = getTemporaryDirectory;

/// One photo read at a time.
bool _reading = false;

/// Whether a photo is being read just now.
bool get readingPhoto => _reading;

/// A photo read on the phone: its words ('' when it had none), and the
/// photo itself, got again only if it is read with AI ([readPhotoWithAI]).
class PhotoReading {
  const PhotoReading(this.words, this.photo);
  final String words;
  final Future<List<int>> Function() photo;
}

/// Camera or the phone's photos (the phone asks for the camera or the
/// photos only once one is chosen), then the photo read on the phone: what
/// it read (its words may be ''; the photo kept in memory meanwhile, for
/// "Read it with AI"), or null when put away or it could not be read (said
/// in a toast). The picker's copy of the photo is deleted once read.
Future<PhotoReading?> readPhotoFromPhone(BuildContext context, {String title = 'Read a photo'}) async {
  if (_reading) return null;
  final from = await pickAction(
    context,
    title: title,
    note: 'Read on this phone. The photo is not sent.',
    actions: const [('camera', 'Take a photo', 'b-camera', false), ('gallery', 'Choose from your photos', 'b-upload', false)],
  );
  if (from == null || !context.mounted) return null;
  _reading = true;
  String? path;
  try {
    path = await pickPhotoToRead(from == 'camera' ? ImageSource.camera : ImageSource.gallery);
    if (path == null || !context.mounted) return null;
    final words = await _readSaid(context, path);
    if (words == null) return null;
    // Kept in memory for "Read it with AI", so the file can go now.
    // (Read at once: a photo made smaller is a megabyte or two.)
    List<int>? bytes;
    try {
      bytes = File(path).readAsBytesSync();
    } on Object {
      bytes = null;
    }
    final kept = bytes;
    return PhotoReading(words, () async => kept ?? (throw const FileSystemException('the photo is gone')));
  } on Object {
    if (context.mounted) showTkToast(context, 'That photo could not be opened. Try another.', error: true);
    return null;
  } finally {
    _reading = false;
    if (path != null) _deletePicked(path);
  }
}

/// The picker's copy of a photo (in the app's own temporary folder, never
/// the photo in the phone's photos) deleted once it has been read.
void _deletePicked(String path) {
  try {
    final f = File(path);
    if (f.existsSync()) f.deleteSync();
  } on Object {
    // Left in the app's temporary folder, which the phone clears.
  }
}

/// A photo in a chat, which is only on the server ([path], fetched as the
/// chat fetches it to show or save): put in a file on the phone for a
/// moment, read on the phone, and the file deleted. What it read (its
/// words may be ''; the photo kept in memory meanwhile, for "Read it with
/// AI"), or null (said in a toast).
Future<PhotoReading?> readChatPhoto(BuildContext context, Auth auth, String path) async {
  if (_reading) return null;
  _reading = true;
  File? file;
  try {
    final bytes = await auth.transport.bytes(path);
    final dir = await photoTempDir();
    file = File('${dir.path}/read-${DateTime.now().microsecondsSinceEpoch}.jpg');
    await file.writeAsBytes(bytes, flush: true);
    if (!context.mounted) return null;
    final words = await _readSaid(context, file.path);
    return words == null ? null : PhotoReading(words, () async => bytes);
  } on Object {
    if (context.mounted) showTkToast(context, 'That photo could not be got just now. Try again.', error: true);
    return null;
  } finally {
    _reading = false;
    try {
      if (file != null && file.existsSync()) file.deleteSync();
    } on Object {
      // Left in the phone's temporary folder, which the phone clears.
    }
  }
}

/// The photo's words, '' for none; null when the phone could not read it
/// (said).
Future<String?> _readSaid(BuildContext context, String path) async {
  try {
    return (await photoText.read(path)).trim();
  } on Object {
    if (context.mounted) showTkToast(context, 'This phone could not read that photo. Type it in.', error: true);
    return null;
  }
}

/// Said when a photo has no words the phone could read.
const noWordsInPhoto = 'No words in that photo. Type it in.';

/// Beside "Read it with AI": what it does with the photo.
const photoAINote = 'Sends this photo to Taskiem’s AI. It is not kept.';

/// Reading photos with AI (I3), for one screen: whether the business lets
/// a photo the phone could not read be sent to Taskiem's AI, asked once,
/// the first time the phone finds nothing (GET
/// /v1/settings/ai-photo-reading); not known (it failed) is off.
class PhotoAI {
  PhotoAI(this.auth);
  final Auth auth;
  Future<AIPhotoReading?>? _setting;

  Future<AIPhotoReading?> _get() => _setting ??= auth.api.getAIPhotoReading().then<AIPhotoReading?>((v) => v, onError: (Object _) => null);

  /// Whether "Read it with AI" is offered: it is on, and can be done here.
  Future<bool> offered() async {
    final s = await _get();
    return s != null && s.available && s.aiPhotoReading;
  }

  /// What is said when the phone found nothing and it is off ([said], or
  /// nothing): for an owner, that it can be turned on.
  Future<String?> offWords(String? said) async {
    final s = await _get();
    final owner = (auth.me?.roles as List?)?.contains('owner') ?? false;
    if (s == null || !s.available || s.aiPhotoReading || !owner) return said;
    const hint = 'Handwriting? You can turn on Read handwriting with AI in Settings.';
    return said == null ? hint : '$said $hint';
  }

  /// The phone found nothing in [photo]: "Read it with AI" offered in a
  /// sheet ([title]: what the phone could not find), or, when it is off,
  /// [said] in a toast. The AI's words, or null.
  Future<String?> offer(BuildContext context, PhotoReading photo, {required String title, required String said}) async {
    if (!await offered()) {
      final w = await offWords(said);
      if (w != null && context.mounted) showTkToast(context, w);
      return null;
    }
    if (!context.mounted) return null;
    final go = await pickAction(context, title: title, note: photoAINote, actions: const [('ai', 'Read it with AI', 'b-bolt', false)]);
    if (go != 'ai' || !context.mounted) return null;
    return readPhotoWithAI(context, auth, photo);
  }
}

/// The same photo sent to Taskiem's AI to be read (POST /v1/photo-text, a
/// `photo` part; not kept there): its words, to be read as the phone's are
/// (`from: "photo"`), or null (the server's refusal, or no words, said in
/// a toast). A photo over the model's 5 MB (a chat's, sent as it is) is
/// not sent; an answer cut off short is used, and said so.
Future<String?> readPhotoWithAI(BuildContext context, Auth auth, PhotoReading photo) async {
  try {
    final bytes = await photo.photo();
    if (bytes.length > maxAIPhotoBytes) {
      if (context.mounted) showTkToast(context, photoTooLarge, error: true);
      return null;
    }
    final kind = _kind(bytes);
    final j = await auth.transport.upload('/v1/photo-text', bytes: bytes, filename: 'photo.$kind', type: 'image/$kind', field: 'photo');
    final words = '${(j is Map ? j['text'] : null) ?? ''}'.trim();
    final cut = j is Map && j['truncated'] == true;
    if (words.isEmpty && context.mounted) showTkToast(context, 'Taskiem’s AI found no words in that photo. Type it in.');
    if (words.isNotEmpty && cut && context.mounted) showTkToast(context, photoListLong);
    return words.isEmpty ? null : words;
  } on ApiError catch (e) {
    if (context.mounted) showTkToast(context, e.message, error: true);
  } on Offline {
    if (context.mounted) showTkToast(context, 'You’re offline. Try again when you’re back online.', error: true);
  } on Object {
    if (context.mounted) showTkToast(context, 'That photo could not be sent just now. Try again.', error: true);
  }
  return null;
}

/// A photo's kind by its first bytes (the server checks it again).
String _kind(List<int> b) {
  bool starts(List<int> m, [int at = 0]) {
    if (b.length < at + m.length) return false;
    for (var i = 0; i < m.length; i++) {
      if (b[at + i] != m[i]) return false;
    }
    return true;
  }

  if (starts(const [0x89, 0x50, 0x4E, 0x47])) return 'png';
  if (starts(const [0x47, 0x49, 0x46])) return 'gif';
  if (starts(const [0x52, 0x49, 0x46, 0x46]) && starts(const [0x57, 0x45, 0x42, 0x50], 8)) return 'webp';
  return 'jpeg';
}

/// A form's "From a photo" button, and beside it (under it when there is
/// no room) that the photo is read on this phone and not sent.
class PhotoReadButton extends StatelessWidget {
  const PhotoReadButton({super.key, required this.label, required this.semantics, this.onPressed});
  final String label;
  final String semantics;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) => Wrap(
    spacing: 10,
    runSpacing: 6,
    crossAxisAlignment: WrapCrossAlignment.center,
    children: [
      TkSmallButton(label: label, icon: 'b-camera', semantics: semantics, onPressed: onPressed),
      Text('Read on this phone, not sent.', style: tkText(size: 13, lineHeight: 1.35, color: TkTheme.tokens(context).muted)),
    ],
  );
}

/// Under a form's "From a photo", when the phone found nothing ([title])
/// and the business allows it: "Read it with AI", and what that sends.
class PhotoAICallout extends StatelessWidget {
  const PhotoAICallout({super.key, required this.title, this.busy = false, required this.onPressed});
  final String title;
  final bool busy;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkCallout(
      leading: TkSquare('b-bolt', color: look.violet),
      title: title,
      sub: photoAINote,
      subLines: 3,
      color: look.violet,
      button: busy ? 'Reading…' : 'Read it with AI',
      semantics: 'Read it with AI. $photoAINote',
      onPressed: () {
        if (!busy) onPressed();
      },
    );
  }
}
