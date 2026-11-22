// Reading account details from photos (I1): the words in a photo are read
// ON THE PHONE, with Google ML Kit's text recognition; the photo itself is
// never sent to be read. Only the words go to Taskiem's readers (POST
// /v1/payments/read, /v1/payment-batches/read, /v1/me/saved/read), with
// `from: "photo"`, which puts right the letters a phone reads in place of
// digits. A test stands in for the reader ([photoText]), the picker
// ([pickPhotoToRead]) and the folder a chat's photo is put in for a moment
// ([photoTempDir]).

import 'dart:async';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:google_mlkit_text_recognition/google_mlkit_text_recognition.dart';
import 'package:image_picker/image_picker.dart' show ImagePicker, ImageSource;
import 'package:path_provider/path_provider.dart';

import '../app/auth.dart';
import '../app/lock.dart' show stepOut;
import '../kit/kit.dart' show TkSmallButton;
import '../kit/modal.dart' show showTkToast;
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

/// A photo taken now or chosen from the phone's photos, as a file on the
/// phone; null when put away (a test puts its own in).
Future<String?> Function(ImageSource from) pickPhotoToRead = (from) async {
  final x = await stepOut(() => ImagePicker().pickImage(source: from, requestFullMetadata: false));
  return x?.path;
};

/// Where a chat's photo is put for a moment to be read (a test puts its
/// own in).
Future<Directory> Function() photoTempDir = getTemporaryDirectory;

/// One photo read at a time.
bool _reading = false;

/// Whether a photo is being read just now.
bool get readingPhoto => _reading;

/// Camera or the phone's photos (the phone asks for the camera or the
/// photos only once one is chosen), then the photo read on the phone: its
/// words, or null when put away or nothing could be read (said in a
/// toast).
Future<String?> readPhotoFromPhone(BuildContext context, {String title = 'Read a photo'}) async {
  if (_reading) return null;
  final from = await pickAction(
    context,
    title: title,
    note: 'Read on this phone. The photo is not sent.',
    actions: const [('camera', 'Take a photo', 'b-camera', false), ('gallery', 'Choose from your photos', 'b-upload', false)],
  );
  if (from == null || !context.mounted) return null;
  _reading = true;
  try {
    final path = await pickPhotoToRead(from == 'camera' ? ImageSource.camera : ImageSource.gallery);
    if (path == null || !context.mounted) return null;
    return await _readSaid(context, path);
  } on Object {
    if (context.mounted) showTkToast(context, 'That photo could not be opened. Try another.', error: true);
    return null;
  } finally {
    _reading = false;
  }
}

/// A photo in a chat, which is only on the server ([path], fetched as the
/// chat fetches it to show or save): put in a file on the phone for a
/// moment, read on the phone, and the file deleted. Its words, or null
/// (said in a toast).
Future<String?> readChatPhoto(BuildContext context, Auth auth, String path) async {
  if (_reading) return null;
  _reading = true;
  File? file;
  try {
    final bytes = await auth.transport.bytes(path);
    final dir = await photoTempDir();
    file = File('${dir.path}/read-${DateTime.now().microsecondsSinceEpoch}.jpg');
    await file.writeAsBytes(bytes, flush: true);
    if (!context.mounted) return null;
    return await _readSaid(context, file.path);
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

Future<String?> _readSaid(BuildContext context, String path) async {
  final String words;
  try {
    words = (await photoText.read(path)).trim();
  } on Object {
    if (context.mounted) showTkToast(context, 'This phone could not read that photo. Type it in.', error: true);
    return null;
  }
  if (words.isEmpty) {
    if (context.mounted) showTkToast(context, 'No words in that photo. Type it in.');
    return null;
  }
  return words;
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
