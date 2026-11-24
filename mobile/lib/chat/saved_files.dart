// Saved files (saved objects S1; web/src/pages/SavedKinds.tsx FileSheet): a
// photo or document saved to call up by name (@menu, @cac-certificate),
// and a made-up kind's file fields. A file is uploaded first (POST
// …/saved/uploads), checked as a chat's file is, and claimed by the saved
// thing saved with it. Opened: a photo full size, a PDF handed to the
// phone's share sheet. Picked with "@" in a chat: attached as itself.

import 'dart:typed_data';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart' show PickedFile, fileSize, handOffFile, showPhoto;
import 'composer.dart' show Pickers;
import 'groups.dart' show pickAction;
import 'my_chat.dart' show sayFailure;
import 'saved_accounts.dart' show SavedApi, cleanSavedName, savedNameProblem;
import 'tags.dart' show TagSuggestion;
import 'saved_runs.dart' show SavedRunsSection;

/// A saved file as lists name it: its upload's id, name, type and size.
typedef SavedFileMeta = ({String id, String name, String type, int size});

SavedFileMeta savedFileMetaOf(Map<String, dynamic> j) =>
    (id: '${j['file'] ?? ''}', name: '${j['name'] ?? ''}', type: '${j['type'] ?? ''}', size: (j['size'] as num?)?.toInt() ?? 0);

/// A file object's file, from its details.
SavedFileMeta? savedFileOfThing(SavedObject x) {
  final d = x.details;
  if ((d.file ?? '').isEmpty) return null;
  return (id: d.file!, name: d.fileName ?? x.title, type: d.fileType ?? '', size: d.fileSize ?? 0);
}

/// Where a saved thing's file is: its own, or a field's.
String savedFilePath(bool mine, String id, [String? field]) =>
    '${mine ? '/v1/me/saved' : '/v1/saved'}/${Uri.encodeComponent(id)}/file${field == null ? '' : '?field=${Uri.encodeQueryComponent(field)}'}';

/// A photo or document uploaded to save.
Future<SavedFileMeta> uploadSavedFile(Auth auth, bool mine, PickedFile f) async {
  final j = await auth.transport.upload(mine ? '/v1/me/saved/uploads' : '/v1/saved/uploads', bytes: f.bytes, filename: f.name, type: f.type);
  return savedFileMetaOf(((j as Map)['upload'] as Map).cast<String, dynamic>());
}

/// A saved file as a file to send: fetched, as it was kept.
Future<PickedFile> savedFileToSend(Auth auth, bool mine, String id, String name, String type) async {
  final bytes = await auth.transport.bytes(savedFilePath(mine, id));
  return PickedFile(bytes: Uint8List.fromList(bytes), name: name, type: type);
}

/// Opens a saved file: a photo full size, a PDF to the share sheet.
Future<void> openSavedFile(BuildContext context, Auth auth, bool mine, String id, SavedFileMeta f, {String? field}) async {
  try {
    final bytes = Uint8List.fromList(await auth.transport.bytes(savedFilePath(mine, id, field)));
    if (!context.mounted) return;
    if (f.type.startsWith('image/')) {
      await showPhoto(context, bytes, f.name);
    } else {
      await handOffFile(bytes, f.name, f.type, null);
    }
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
  }
}

/// Choose a photo or a PDF and upload it to save; null when put away.
Future<SavedFileMeta?> pickSavedFile(BuildContext context, Auth auth, bool mine, Pickers pickers) async {
  final what = await pickAction(context, title: 'A photo or document', actions: const [
    ('photo', 'A photo', 'b-camera', false),
    ('document', 'A PDF', 'b-note', false),
  ]);
  if (what == null) return null;
  final f = what == 'photo' ? await pickers.photo() : await pickers.document();
  if (f == null) return null;
  return uploadSavedFile(auth, mine, f);
}

/// Save a photo or document, or change one. Pops true when saved.
class SavedFileSheet extends StatefulWidget {
  const SavedFileSheet({super.key, required this.api, required this.auth, this.thing, this.start, this.startName, this.pickers = const Pickers()});
  final SavedApi api;
  final Auth auth;
  final SavedObject? thing;

  /// A file already uploaded to save (Save as… on a chat's file).
  final SavedFileMeta? start;

  /// The name typed in the picker (Save "@menu"…).
  final String? startName;
  final Pickers pickers;

  @override
  State<SavedFileSheet> createState() => _SavedFileSheetState();
}

class _SavedFileSheetState extends State<SavedFileSheet> {
  late final SavedObject? _x = widget.thing;
  late final _name = TextEditingController(text: _x != null ? '@${_x.name}' : ((widget.startName ?? '').isEmpty ? '' : '@${widget.startName}'));
  late final _title = TextEditingController(text: _x?.title ?? (widget.start?.name.replaceFirst(RegExp(r'\.[a-zA-Z0-9]{1,5}$'), '') ?? ''));
  late final _note = TextEditingController(text: _x?.details.note ?? '');
  late SavedFileMeta? _file = widget.start;
  late final SavedFileMeta? _had = _x == null ? widget.start : savedFileOfThing(_x);
  bool _busy = false;
  bool _uploading = false;
  String? _error;

  @override
  void dispose() {
    for (final c in [_name, _title, _note]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _pick() async {
    setState(() => (_uploading = true, _error = null));
    try {
      final f = await pickSavedFile(context, widget.auth, widget.api.mine, widget.pickers);
      if (mounted) setState(() => (_uploading = false, _file = f ?? _file));
    } on Object catch (e) {
      if (mounted) setState(() => (_uploading = false, _error = sayFailure(e)));
    }
  }

  Future<void> _save() async {
    final x = _x;
    final file = _file;
    final problem = savedNameProblem(_name.text) ?? (_title.text.trim().isEmpty ? 'Give it a title, like Our menu.' : null) ?? (file == null && _had == null ? 'Choose the photo or document.' : null);
    if (problem != null) return setState(() => _error = problem);
    setState(() => (_busy = true, _error = null));
    try {
      // A new thing names its file; a change only a file chosen anew.
      final details = <String, String?>{'note': _note.text.trim(), if (file != null && (x == null || file.id != _had?.id)) 'file': file.id};
      final body = SavedObjectIn(name: cleanSavedName(_name.text), title: _title.text.trim(), kind: x == null ? 'file' : null, details: details);
      if (x == null) {
        await widget.api.add(body);
      } else {
        await widget.api.change(x.id, body);
      }
      if (mounted) Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_busy = false, _error = e.has('name_taken') ? 'That name is taken: choose another.' : e.message));
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final x = _x;
    final shown = _file ?? _had;
    return TkFormSheet(
      title: x == null ? 'Save a photo or document' : 'Change @${x.name}',
      crumb: 'Saved',
      action: _busy ? 'Saving…' : 'Save',
      onAction: _busy || _uploading ? null : _save,
      children: [
        TkFormGroup(
          children: [
            TkKitField(
              label: 'The photo or document',
              hint: 'A photo, or a PDF up to 25 MB. It is checked before it is kept',
              field: false,
              child: TkPickBox(
                label: 'The photo or document',
                shown: _uploading ? 'Checking the file…' : (shown == null ? 'Choose a photo or PDF' : '${shown.name} · ${fileSize(shown.size)}'),
                onTap: _pick,
              ),
            ),
          ],
        ),
        TkFormGroup(
          children: [
            TkKitField(label: 'Name to call it up by', hint: 'Such as @menu or @cac-certificate', child: TkTextBox(controller: _name, max: 33, plain: true)),
            TkKitField(label: 'Title', hint: 'How it is shown', child: TkTextBox(controller: _title, max: 80)),
            TkKitField(label: 'Note', hint: 'Optional, for your own reference', child: TkTextBox(controller: _note, max: 200)),
          ],
        ),
        if (_x != null && !widget.api.mine) SavedRunsSection(auth: widget.api.auth, id: _x.id),
        if (_error != null) TkCallout(leading: TkSquare('b-warn', color: look.red), title: _error!, color: look.red),
      ],
    );
  }
}

/// A saved file picked with "@" in a chat: fetched and handed to [to] (the
/// composer's choose, or the business's send), as if chosen from the
/// phone.
Future<void> attachSavedFile(BuildContext context, Auth auth, TagSuggestion s, void Function(PickedFile f) to) async {
  try {
    final f = await savedFileToSend(auth, s.savedOwner != 'business', s.id, s.fileName ?? s.name, s.fileType ?? '');
    to(f);
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
  }
}
