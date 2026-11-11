// The box to write in, as the web's (web/src/pages/ChatComposer.tsx
// ComposerView, AttachMenu.tsx, PendingFile.tsx, VoiceNote.tsx): the tools,
// the round box with the clip (Photo, Document, Camera) and the camera, and
// the microphone, which becomes Send once there are words or a file. A file
// chosen waits above the box for its caption; a voice note records while
// the microphone is held (released, it goes; slid left, it is thrown away)
// or, tapped, until Done or Cancel.

import 'dart:async';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:image_picker/image_picker.dart' show ImagePicker, ImageSource;
import 'package:path_provider/path_provider.dart';
import 'package:record/record.dart';

import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'attachments.dart';

/// Where a file to send comes from: the phone's gallery, camera and files.
class Pickers {
  const Pickers();

  /// A photo from the gallery. JPEG at 85: an iPhone's HEIC comes as a
  /// photo the server takes, and a phone's large photo well under the limit.
  Future<PickedFile?> photo() => _image(ImageSource.gallery);

  /// A photo taken now, with the phone's own camera.
  Future<PickedFile?> camera() => _image(ImageSource.camera);

  Future<PickedFile?> _image(ImageSource from) async {
    final x = await ImagePicker().pickImage(source: from, imageQuality: 85, requestFullMetadata: false);
    if (x == null) return null;
    final bytes = await x.readAsBytes();
    final name = x.name.toLowerCase().endsWith('.png') ? x.name : '${x.name.split('.').first}.jpg';
    return PickedFile(bytes: bytes, name: name, type: name.endsWith('.png') ? 'image/png' : 'image/jpeg');
  }

  /// A PDF from the phone's files.
  Future<PickedFile?> document() async {
    final x = await openFile(
      acceptedTypeGroups: const [
        XTypeGroup(label: 'PDF', extensions: ['pdf'], mimeTypes: ['application/pdf'], uniformTypeIdentifiers: ['com.adobe.pdf']),
      ],
    );
    if (x == null) return null;
    return PickedFile(bytes: await x.readAsBytes(), name: x.name, type: 'application/pdf');
  }
}

/// The microphone, for a voice note: AAC in MP4, which the server takes and
/// both kinds of phone play.
class Recorder {
  Recorder();
  AudioRecorder? _rec;
  String? _path;

  /// Starts; false when the phone's microphone may not be used.
  Future<bool> start() async {
    final r = _rec ??= AudioRecorder();
    if (!await r.hasPermission()) return false;
    final dir = await getTemporaryDirectory();
    _path = '${dir.path}/voice-${DateTime.now().microsecondsSinceEpoch}.m4a';
    await r.start(const RecordConfig(encoder: AudioEncoder.aacLc, bitRate: 48000, numChannels: 1), path: _path!);
    return true;
  }

  /// Stops: the note when kept, nothing when thrown away.
  Future<PickedFile?> stop({required bool keep}) async {
    final path = await _rec?.stop() ?? _path;
    if (path == null) return null;
    final f = File(path);
    if (!keep) {
      if (f.existsSync()) f.deleteSync();
      return null;
    }
    final bytes = await f.readAsBytes();
    f.deleteSync();
    return PickedFile(bytes: bytes, name: 'Voice note.m4a', type: 'audio/mp4');
  }

  void dispose() => _rec?.dispose();
}

/// VoiceNote.tsx MAX_VOICE_SECONDS, HOLD_MS, SLIDE_CANCEL_PX.
const _maxVoice = 600;
const _hold = Duration(milliseconds: 400);
const _slideCancel = 80.0;

/// `.composer-pill`'s and the round buttons' soft lift.
const _lift = [BoxShadow(color: Color(0x140E1A14), offset: Offset(0, 1), blurRadius: 2)];

/// The box to write in.
class Composer extends StatefulWidget {
  const Composer({
    super.key,
    required this.text,
    required this.onSend,
    required this.bottom,
    this.pickers = const Pickers(),
    this.recorder,
    this.hint = 'Message',
    this.label = 'Message',
    this.rules = businessFiles,
    this.busy = false,
  });

  final TextEditingController text;

  /// Sends the words, with the file waiting for its caption if there is one.
  final void Function(String text, PickedFile? file) onSend;

  /// The phone's own inset at the bottom (its gesture bar).
  final double bottom;
  final Pickers pickers;
  final Recorder? recorder;

  /// The box's hint on a phone: the words before its comma (`hint()`).
  final String hint;

  /// What a screen reader calls the box.
  final String label;

  /// What may be sent here.
  final FileRules rules;

  /// Sending: Send waits.
  final bool busy;

  @override
  State<Composer> createState() => ComposerState();
}

class ComposerState extends State<Composer> {
  PickedFile? _file;
  String? _error;
  final _menu = OverlayPortalController();
  final _clip = LayerLink();
  late final Recorder _recorder = widget.recorder ?? Recorder();

  /// Recording: the seconds so far (null when not), and whether it is held.
  int? _secs;
  bool _holding = false;
  Timer? _tick;
  DateTime? _pressedAt;
  double _pressX = 0;

  @override
  void initState() {
    super.initState();
    widget.text.addListener(_typed);
  }

  @override
  void dispose() {
    widget.text.removeListener(_typed);
    _tick?.cancel();
    if (widget.recorder == null) _recorder.dispose();
    super.dispose();
  }

  void _typed() => setState(() {});

  /// A file chosen: it waits above the box for its caption.
  void choose(PickedFile? f) {
    if (f == null) return;
    final problem = fileProblem(f, widget.rules);
    setState(() {
      _error = problem;
      _file = problem == null ? f : null;
    });
  }

  Future<void> _attach(String what) async {
    _menu.hide();
    setState(() => _error = null);
    try {
      choose(await switch (what) {
        'photo' => widget.pickers.photo(),
        'document' => widget.pickers.document(),
        _ => widget.pickers.camera(),
      });
    } on PlatformException catch (e) {
      setState(
        () => _error = e.code.contains('denied') || e.code.contains('access')
            ? 'Taskiem may not use your ${what == 'camera' ? 'camera' : 'photos'}: allow it in your phone’s settings.'
            : 'That could not be opened. Try again.',
      );
    }
  }

  void _send([PickedFile? held]) {
    final f = held ?? _file;
    final words = held != null ? '' : widget.text.text.trim();
    if (words.isEmpty && f == null) return;
    if (held == null) {
      widget.text.clear();
      setState(() => _file = null);
    }
    widget.onSend(words, f);
  }

  // --- the voice note ---

  Future<void> _record() async {
    setState(() => _error = null);
    bool ok;
    try {
      ok = await _recorder.start();
    } on Object {
      ok = false;
    }
    if (!ok) {
      setState(() {
        _holding = false;
        _error = 'Taskiem may not use your microphone: allow it in your phone’s settings.';
      });
      return;
    }
    setState(() => _secs = 0);
    _tick = Timer.periodic(const Duration(seconds: 1), (_) {
      setState(() => _secs = (_secs ?? 0) + 1);
      if ((_secs ?? 0) >= _maxVoice) _stop(keep: true, send: _holding);
    });
  }

  /// Done keeps it to send with a caption; a held note released goes now;
  /// Cancel, or sliding away, throws it away.
  Future<void> _stop({required bool keep, bool send = false}) async {
    _tick?.cancel();
    final f = await _recorder.stop(keep: keep);
    if (!mounted) return;
    setState(() {
      _secs = null;
      _holding = false;
    });
    if (f == null) return;
    if (send) {
      _send(f);
    } else {
      choose(f);
    }
  }

  void _press(PointerDownEvent e) {
    _pressedAt = DateTime.now();
    _pressX = e.position.dx;
    setState(() => _holding = true);
    _record();
  }

  void _move(PointerMoveEvent e) {
    if (_holding && _pressX - e.position.dx > _slideCancel) _stop(keep: false);
  }

  void _release(PointerUpEvent e) {
    if (!_holding) return;
    final at = _pressedAt;
    if (at != null && DateTime.now().difference(at) < _hold) {
      // A tap: it records until Done or Cancel.
      setState(() => _holding = false);
    } else {
      _stop(keep: true, send: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final file = _file;
    final words = widget.text.text.trim().isNotEmpty;
    final recording = _secs != null || _holding;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (_error != null) Container(color: t.panel, padding: const EdgeInsets.symmetric(horizontal: 12), child: TkErrorBox(_error!)),
        if (file != null && !recording) _Pending(file: file, onRemove: () => setState(() => _file = null)),
        Container(
          color: t.tkSoft,
          // `.chat-pane .composer { padding: 8px 10px 12px; gap: 8px }`
          padding: EdgeInsets.fromLTRB(10, 8, 10, 12 + widget.bottom),
          child: recording ? _recordingBar(t) : _row(t, dark, file, words),
        ),
      ],
    );
  }

  Widget _row(TkTokens t, bool dark, PickedFile? file, bool words) {
    Widget small(String icon, String label, VoidCallback onTap) => Semantics(
      button: true,
      label: label,
      child: TkPress(
        onTap: onTap,
        child: SizedBox(
          width: 40,
          height: 40,
          child: Center(child: TkIcon(icon, size: 18, color: t.muted)),
        ),
      ),
    );
    final sendable = words || file != null;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Semantics(
          button: true,
          label: 'Chat tools',
          child: TkPress(
            onTap: () {},
            child: Container(
              width: 46,
              height: 46,
              decoration: BoxDecoration(color: t.panel, shape: BoxShape.circle, boxShadow: _lift),
              alignment: Alignment.center,
              child: TkIcon('grid', size: 22, stroke: 2, color: dark ? t.text : t.brand),
            ),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: Container(
            constraints: const BoxConstraints(minHeight: 46),
            padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
            decoration: BoxDecoration(color: t.panel, borderRadius: BorderRadius.circular(24), boxShadow: _lift),
            child: Row(
              children: [
                Expanded(
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 10),
                    child: Material(
                      type: MaterialType.transparency,
                      child: Semantics(
                        label: widget.label,
                        textField: true,
                        child: TextField(
                          controller: widget.text,
                          minLines: 1,
                          maxLines: 6,
                          maxLength: 4000,
                          buildCounter: (_, {required currentLength, required isFocused, maxLength}) => null,
                          textCapitalization: TextCapitalization.sentences,
                          keyboardType: TextInputType.multiline,
                          cursorColor: t.text,
                          showCursor: !TkField.debugHideCaret,
                          style: tkText(size: 15.5, lineHeight: 1.4, color: t.text),
                          decoration: InputDecoration.collapsed(
                            hintText: file != null ? 'Add a caption' : widget.hint,
                            hintStyle: tkText(size: 15.5, lineHeight: 1.4, color: t.muted),
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
                OverlayPortal(
                  controller: _menu,
                  overlayChildBuilder: (context) => _AttachMenu(link: _clip, onChoose: _attach, onClose: _menu.hide, documents: widget.rules.documents),
                  child: CompositedTransformTarget(
                    link: _clip,
                    child: Semantics(expanded: _menu.isShowing, child: small('attach', 'Attach', () => setState(_menu.toggle))),
                  ),
                ),
                if (file == null) small('camera', 'Take a photo', () => _attach('camera')),
              ],
            ),
          ),
        ),
        const SizedBox(width: 8),
        if (sendable)
          Semantics(
            button: true,
            label: 'Send',
            child: TkPress(onTap: widget.busy ? null : _send, child: _round(t, dark, 'arrow-up')),
          )
        else
          // MicButton: hold to record, release to send; tap to record and
          // send when done.
          Semantics(
            button: true,
            label: 'Record a voice note',
            hint: 'Hold to record, release to send. Tap to record and send when you are done.',
            child: Listener(
              onPointerDown: _press,
              onPointerMove: _move,
              onPointerUp: _release,
              onPointerCancel: (_) => _holding ? _stop(keep: false) : null,
              child: _round(t, dark, 'mic'),
            ),
          ),
      ],
    );
  }

  Widget _round(TkTokens t, bool dark, String icon) => Container(
    width: 46,
    height: 46,
    decoration: BoxDecoration(color: dark ? t.brandDark : t.brand, shape: BoxShape.circle),
    alignment: Alignment.center,
    child: TkIcon(icon, size: 22, stroke: 2.4, color: dark ? t.brandDarkText : t.brandDark),
  );

  /// RecordingBar: the time, and Cancel and Done; while held, how to send
  /// or cancel it.
  Widget _recordingBar(TkTokens t) {
    final s = _secs ?? 0;
    final time = '${s ~/ 60}:${(s % 60).toString().padLeft(2, '0')}';
    return Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Expanded(
          child: Semantics(
            liveRegion: true,
            child: Container(
              constraints: const BoxConstraints(minHeight: 40),
              padding: const EdgeInsets.symmetric(horizontal: 14),
              decoration: BoxDecoration(color: t.panel, borderRadius: BorderRadius.circular(24)),
              child: Row(
                children: [
                  Container(
                    width: 10,
                    height: 10,
                    decoration: BoxDecoration(color: t.danger, shape: BoxShape.circle),
                  ),
                  const SizedBox(width: 8),
                  Flexible(
                    child: Text.rich(
                      TextSpan(
                        children: [
                          TextSpan(text: 'Recording $time'),
                          if (_holding)
                            TextSpan(
                              text: ' · release to send, slide left to cancel',
                              style: tkText(size: 13, color: t.muted),
                            ),
                        ],
                      ),
                      maxLines: 2,
                      style: tkText(size: 14, color: t.text).copyWith(fontFeatures: const [ui.FontFeature.tabularFigures()]),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
        if (!_holding) ...[
          const SizedBox(width: 8),
          _PlainSmall(label: 'Cancel', semantics: 'Cancel the voice note', onTap: () => _stop(keep: false)),
          const SizedBox(width: 8),
          Semantics(
            button: true,
            label: 'Done',
            excludeSemantics: true,
            child: TkPress(
              onTap: () => _stop(keep: true),
              child: Container(
                width: 46,
                height: 46,
                decoration: BoxDecoration(
                  color: t.primary,
                  border: Border.all(color: t.primary),
                  borderRadius: BorderRadius.circular(8),
                  boxShadow: const [BoxShadow(color: Color(0x1F0E0F0C), offset: Offset(0, 1), blurRadius: 2)],
                ),
                alignment: Alignment.center,
                child: Text('Done', style: tkText(size: 13.5, weight: 500, lineHeight: 1.2, color: t.primaryText)),
              ),
            ),
          ),
        ],
      ],
    );
  }
}

/// The web's plain `button.small`: 0.85 em, a line round it, 40 px tall.
class _PlainSmall extends StatelessWidget {
  const _PlainSmall({required this.label, required this.onTap, this.semantics});
  final String label;
  final String? semantics;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      label: semantics ?? label,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 40),
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: t.panel,
            border: Border.all(color: t.borderStrong),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Text(label, style: tkText(size: 11.9, weight: 500, lineHeight: 1.2, color: t.text)),
        ),
      ),
    );
  }
}

/// PendingFile.tsx: a file waiting for its caption: the photo itself, or
/// the name and size, and Remove.
class _Pending extends StatelessWidget {
  const _Pending({required this.file, required this.onRemove});
  final PickedFile file;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final words =
        '${file.voice
            ? 'Voice note'
            : file.photo
            ? 'Photo: ${file.name}'
            : file.name} · ${fileSize(file.bytes.length)}';
    return Container(
      decoration: BoxDecoration(
        color: t.panel,
        border: Border(top: BorderSide(color: t.border)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (file.photo) ...[
            // `.pending-photo { max-height: 200px; flex-basis: 100%;
            // object-fit: contain; background: var(--panel-2) }`
            Semantics(
              image: true,
              label: 'Preview of ${file.name}',
              child: _PendingPhoto(file: file),
            ),
            const SizedBox(height: 8),
          ],
          Row(
            children: [
              if (!file.photo) ...[
                Container(
                  width: 36,
                  height: 36,
                  decoration: BoxDecoration(color: t.tint, borderRadius: BorderRadius.circular(8)),
                  alignment: Alignment.center,
                  child: TkIcon(file.voice ? 'mic' : 'file', size: 20, color: t.tintText),
                ),
                const SizedBox(width: 10),
              ],
              Expanded(
                child: Text(words, style: tkText(size: 13, color: t.muted)),
              ),
              const SizedBox(width: 10),
              _PlainSmall(label: 'Remove', semantics: 'Remove the file', onTap: onRemove),
            ],
          ),
        ],
      ),
    );
  }
}

class _PendingPhoto extends StatefulWidget {
  const _PendingPhoto({required this.file});
  final PickedFile file;

  @override
  State<_PendingPhoto> createState() => _PendingPhotoState();
}

class _PendingPhotoState extends State<_PendingPhoto> {
  Size? _size;

  @override
  void initState() {
    super.initState();
    ui.instantiateImageCodec(widget.file.bytes).then((c) async {
      final f = await c.getNextFrame();
      if (mounted) setState(() => _size = Size(f.image.width.toDouble(), f.image.height.toDouble()));
      f.image.dispose();
      c.dispose();
    }, onError: (Object _) {});
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final size = _size;
    if (size == null) return const SizedBox.shrink();
    return LayoutBuilder(
      builder: (context, box) {
        final h = (box.maxWidth * size.height / size.width).clamp(0.0, 200.0);
        return ClipRRect(
          borderRadius: BorderRadius.circular(8),
          child: Container(
            width: box.maxWidth,
            height: h,
            color: t.panel2,
            child: Image.memory(widget.file.bytes, fit: BoxFit.contain, gaplessPlayback: true),
          ),
        );
      },
    );
  }
}

/// AttachMenu.tsx `.attach-menu`: above the clip, its left edge at the
/// clip's: Photo, Document, Camera.
class _AttachMenu extends StatelessWidget {
  const _AttachMenu({required this.link, required this.onChoose, required this.onClose, this.documents = true});
  final LayerLink link;

  /// Document is offered (a business's conversation, not Taskiem's).
  final bool documents;
  final void Function(String what) onChoose;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    Widget item(String what, String icon, String label) => Semantics(
      button: true,
      label: label,
      excludeSemantics: true,
      child: TkPress(
        onTap: () => onChoose(what),
        child: Container(
          constraints: const BoxConstraints(minHeight: 40),
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(6),
            boxShadow: outsideOnly(const [BoxShadow(color: Color(0x0A0E0F0C), offset: Offset(0, 1), blurRadius: 2)]),
          ),
          child: Row(
            children: [
              TkIcon(icon, size: 18, color: t.text),
              const SizedBox(width: 10),
              Text(label, style: tkText(size: 13.5, weight: 500, lineHeight: 1.2, color: t.text)),
            ],
          ),
        ),
      ),
    );
    return Stack(
      children: [
        // A tap elsewhere closes it.
        Positioned.fill(
          child: GestureDetector(behavior: HitTestBehavior.translucent, onTap: onClose),
        ),
        CompositedTransformFollower(
          link: link,
          targetAnchor: Alignment.topLeft,
          followerAnchor: Alignment.bottomLeft,
          offset: const Offset(0, -6),
          child: Align(
            alignment: Alignment.bottomLeft,
            child: Semantics(
              container: true,
              label: 'Attach',
              child: Container(
                width: 170,
                padding: const EdgeInsets.all(4),
                decoration: BoxDecoration(
                  color: t.panel,
                  border: Border.all(color: t.border),
                  borderRadius: BorderRadius.circular(16),
                  boxShadow: t.shadowMd,
                ),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [item('photo', 'image', 'Photo'), if (documents) item('document', 'file', 'Document'), item('camera', 'camera', 'Camera')],
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}
