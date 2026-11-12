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
import '../kit/modal.dart' show showComingSoon;
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'attachments.dart';
import 'tools_panel.dart';

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
/// both kinds of phone play. A stop that comes while it is still starting
/// (a slow phone, the microphone question showing) waits for the start and
/// keeps nothing: there is no note yet, and nothing is left recording.
class Recorder {
  Recorder();
  AudioRecorder? _rec;
  String? _path;
  Future<bool>? _starting;
  bool _began = false;

  /// The phone's part, each a step a test can stand in for.
  @protected
  Future<bool> mayRecord() => (_rec ??= AudioRecorder()).hasPermission();
  @protected
  Future<String> place() async => '${(await getTemporaryDirectory()).path}/voice-${DateTime.now().microsecondsSinceEpoch}.m4a';
  @protected
  Future<void> begin(String path) =>
      (_rec ??= AudioRecorder()).start(const RecordConfig(encoder: AudioEncoder.aacLc, bitRate: 48000, numChannels: 1), path: path);
  @protected
  Future<String?> end() async => _rec?.stop();

  /// The note's bytes when kept; the file is removed either way.
  @protected
  Future<Uint8List?> take(String path, {required bool keep}) async {
    final f = File(path);
    if (!f.existsSync()) return null;
    final bytes = keep ? await f.readAsBytes() : null;
    f.deleteSync();
    return bytes;
  }

  /// Starts; false when the phone's microphone may not be used.
  Future<bool> start() {
    _began = false;
    final s = () async {
      if (!await mayRecord()) return false;
      final p = await place();
      _path = p;
      await begin(p);
      _began = true;
      return true;
    }();
    _starting = s;
    return s;
  }

  /// Stops: the note when kept, nothing when thrown away (or when stopped
  /// before it had begun).
  Future<PickedFile?> stop({required bool keep}) async {
    final starting = _starting;
    _starting = null;
    if (starting == null) return null;
    final early = !_began;
    bool began;
    try {
      began = await starting;
    } on Object {
      began = false;
    }
    final path = _path;
    _path = null;
    _began = false;
    if (!began) return null;
    String? at;
    try {
      at = await end() ?? path;
    } on Object {
      at = path;
    }
    if (at == null) return null;
    try {
      final bytes = await take(at, keep: keep && !early);
      return bytes == null ? null : PickedFile(bytes: bytes, name: 'Voice note.m4a', type: 'audio/mp4');
    } on Object {
      return null;
    }
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
    this.replying,
    this.onCancelReply,
    this.tools,
  });

  final TextEditingController text;

  /// The message being answered, in a line (ChatComposer's replyLine).
  final String? replying;
  final VoidCallback? onCancelReply;

  /// The tools panel this composer's button opens (with chat tools).
  final ToolsController? tools;

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

  /// Counts the recordings: one stopped before it had started is not
  /// begun after all when its start comes.
  int _take = 0;
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
    final take = ++_take;
    bool ok;
    try {
      ok = await _recorder.start();
    } on Object {
      ok = false;
    }
    // Stopped (or thrown away) while it was starting: that one is over.
    if (!mounted || take != _take) return;
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
    _take++;
    _tick?.cancel();
    PickedFile? f;
    try {
      f = await _recorder.stop(keep: keep);
    } on Object {
      f = null;
    }
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
        if (widget.replying != null && !recording) _Replying(line: widget.replying!, onCancel: widget.onCancelReply),
        if (file != null && !recording) _Pending(file: file, onRemove: () => setState(() => _file = null)),
        Container(
          // On the chat's wallpaper, as WhatsApp's (`.chat-pane .composer` on a
          // phone): no strip of its own.
          // `.chat-pane .composer { padding: 8px 10px 12px; gap: 8px }`
          padding: EdgeInsets.fromLTRB(10, 8, 10, 12 + widget.bottom),
          child: recording ? _recordingBar(t) : _row(t, dark, file, words),
        ),
      ],
    );
  }

  Widget _row(TkTokens t, bool dark, PickedFile? file, bool words) {
    Widget small(String icon, String label, VoidCallback onTap) => TapArea(
      child: Semantics(
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
      ),
    );
    final sendable = words || file != null;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        ListenableBuilder(
          listenable: widget.tools ?? const _Still(),
          builder: (context, _) {
            // `.composer-tools.open { background: var(--brand); color: var(--brand-dark) }`
            final open = widget.tools?.shown ?? false;
            return TapArea(
              child: Semantics(
                button: true,
                expanded: widget.tools == null ? null : open,
                label: widget.tools == null ? 'Chat tools' : (open ? 'Close tools' : 'Open tools'),
                child: TkPress(
                  // Without chat tools, the tools are not here yet.
                  onTap: widget.tools?.toggle ?? () => showComingSoon(context),
                  child: Container(
                    width: 46,
                    height: 46,
                    decoration: BoxDecoration(color: open ? t.brand : t.panel, shape: BoxShape.circle, boxShadow: _lift),
                    alignment: Alignment.center,
                    child: TkIcon('grid', size: 22, stroke: 2, color: open ? t.brandDark : (dark ? t.text : t.brand)),
                  ),
                ),
              ),
            );
          },
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
                  child: TapField(child: Padding(
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
                  )),
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
          TapArea(child: Semantics(
            button: true,
            label: 'Send',
            child: TkPress(onTap: widget.busy ? null : _send, child: _round(t, dark, 'arrow-up')),
          ))
        else
          // MicButton: hold to record, release to send; tap to record and
          // send when done.
          TapArea(child: Semantics(
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
          )),
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
              Flexible(child: Text(label, style: tkText(size: 13.5, weight: 500, lineHeight: 1.2, color: t.text))),
            ],
          ),
        ),
      ),
    );
    return Stack(
      children: [
        // A tap elsewhere closes it.
        Positioned.fill(
          child: Backdrop(behavior: HitTestBehavior.translucent, onClose: onClose),
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

/// `.composer-reply`: "Replying to …" with its line, and Cancel.
class _Replying extends StatelessWidget {
  const _Replying({required this.line, this.onCancel});
  final String line;
  final VoidCallback? onCancel;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(10, 6, 10, 0),
      child: Row(
        children: [
          Expanded(
            child: Container(
              padding: const EdgeInsets.only(left: 8),
              decoration: BoxDecoration(border: Border(left: BorderSide(color: t.accent, width: 3))),
              child: Text('Replying to $line', style: tkText(size: 13, color: t.muted)),
            ),
          ),
          const SizedBox(width: 8),
          Semantics(
            button: true,
            label: 'Cancel the reply',
            excludeSemantics: true,
            child: TkPress(
              onTap: onCancel,
              child: Container(
                constraints: const BoxConstraints(minHeight: 40),
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                alignment: Alignment.center,
                decoration: BoxDecoration(color: t.panel, border: Border.all(color: t.borderStrong), borderRadius: BorderRadius.circular(8)),
                // `button.small` in a 13 px row: 0.85 em is 11.05 px.
                child: Text('Cancel', style: tkText(size: 11.05, weight: 500, lineHeight: 1.2, color: t.text)),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _Still extends Listenable {
  const _Still();
  @override
  void addListener(VoidCallback listener) {}
  @override
  void removeListener(VoidCallback listener) {}
}
