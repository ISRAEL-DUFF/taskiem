// A message's photo, voice note or PDF, as the web shows it
// (web/src/pages/Conversation.tsx AttachmentView; styles.css .chat-photo,
// .chat-voice, .chat-file): this business's own copy, streamed by
// GET /v1/link-chats/{link}/files/{id}. The web's voice note is the
// browser's own audio player; the app draws Chromium's, as the web is seen.

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/widgets.dart';
import 'package:just_audio/just_audio.dart';
import 'package:path_provider/path_provider.dart';
import 'package:share_plus/share_plus.dart';

import '../app/lock.dart' show stepOut;
import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/store.dart' show keepMediaBytes, keptMedia;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/tap_area.dart';
import '../kit/modal.dart';
import '../theme/theme.dart';
import 'words.dart';

/// linkchat.ts fileSize: 820 KB, 1.4 MB.
String fileSize(int bytes) {
  if (bytes < 1024 * 1024) return '${(bytes / 1024).round().clamp(1, 1 << 30)} KB';
  return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
}

/// A file chosen to send: a photo, a PDF or a voice note.
class PickedFile {
  PickedFile({required this.bytes, required this.name, required this.type});
  final Uint8List bytes;
  final String name;
  final String type;

  bool get photo => type.startsWith('image/');
  bool get voice => type.startsWith('audio/');

  /// outbox.ts attachmentOf: what kind of attachment it will be.
  LinkMessageAttachment get attachment => LinkMessageAttachment(kind: voice ? 'voice' : (photo ? 'photo' : 'file'), type: type, size: bytes.length, name: name);
}

/// ChatComposer.tsx FileRules: what a conversation takes.
class FileRules {
  const FileRules({required this.max, required this.maxWords, required this.types, required this.typesMessage, required this.documents});
  final int max;
  final String maxWords;
  final String types;
  final String typesMessage;

  /// The clip's menu offers a document.
  final bool documents;
}

/// ChatComposer.tsx BUSINESS_FILES: a photo, a PDF or a voice note, to 25 MB.
const businessFiles = FileRules(
  max: 25 * 1024 * 1024,
  maxWords: '25 MB',
  types: r'^(image/(jpeg|png|webp)|application/pdf|audio/)',
  typesMessage: 'Send a photo (JPEG, PNG or WebP), a PDF or a voice note.',
  documents: true,
);

/// ChatComposer.tsx TASKIEM_FILES: a photo or a voice note, to 10 MB.
const taskiemFiles = FileRules(
  max: 10 * 1024 * 1024,
  maxWords: '10 MB',
  types: r'^(image/(jpeg|png|webp)|audio/)',
  typesMessage: 'Send a photo (JPEG, PNG or WebP) or a voice note.',
  documents: false,
);

/// Why a file cannot be sent, in words for the person; null when it can.
String? fileProblem(PickedFile f, [FileRules rules = businessFiles]) {
  if (f.bytes.length > rules.max) return '${f.name} is larger than ${rules.maxWords}.';
  if (!RegExp(rules.types).hasMatch(f.type)) return rules.typesMessage;
  return null;
}

/// A file of a conversation, fetched once with the session: its bytes, and
/// a photo's size once decoded.
class ChatFile {
  ChatFile(this.bytes, this.size);
  final Uint8List bytes;
  final Size? size;
}

/// The files of the conversations, fetched as they are shown. At most
/// [limit] bytes are kept, those drawn least lately let go first (drawn
/// again, they are fetched again); a file that could not be fetched is not
/// asked for again on every redraw, only after a minute or when pressed.
class ChatFiles extends ChangeNotifier {
  ChatFiles(this.transport, {DateTime Function()? now, this.limit = 50 << 20, String Function(LinkMessage m)? pathOf}) : now = now ?? DateTime.now, _pathOf = pathOf ?? path;
  final Transport transport;

  /// Where a message's file is (a linked business's, by default; a
  /// customer conversation's on either side).
  final String Function(LinkMessage m) _pathOf;
  final DateTime Function() now;
  final int limit;

  /// Oldest drawn first (a LinkedHashMap keeps the order things go in).
  final Map<String, ChatFile?> _got = {};
  final Map<String, DateTime> _failed = {};
  int _kept = 0;

  /// How many bytes are kept now.
  int get kept => _kept;

  void _put(String id, ChatFile f) {
    final was = _got.remove(id);
    if (was != null) _kept -= was.bytes.length;
    _got[id] = f;
    _kept += f.bytes.length;
    // The least lately drawn go, never the one just put; those on their
    // way (null) are left to come.
    for (final k in _got.keys.toList()) {
      if (_kept <= limit) break;
      if (k == id) continue;
      final g = _got[k];
      if (g == null) continue;
      _got.remove(k);
      _kept -= g.bytes.length;
    }
  }

  static String path(LinkMessage m) => '/v1/link-chats/${Uri.encodeComponent(m.link)}/files/${Uri.encodeComponent(m.id)}';

  /// A file we just sent: kept as it is, a photo's size read from it.
  Future<void> keep(String id, Uint8List bytes, {String? link}) async {
    if (link != null) unawaited(keepMediaBytes('/v1/link-chats/${Uri.encodeComponent(link)}/files/${Uri.encodeComponent(id)}', bytes));
    Size? size;
    try {
      final codec = await ui.instantiateImageCodec(bytes);
      final frame = await codec.getNextFrame();
      size = Size(frame.image.width.toDouble(), frame.image.height.toDouble());
      frame.image.dispose();
      codec.dispose();
    } on Object {
      size = null;
    }
    _put(id, ChatFile(bytes, size));
    notifyListeners();
  }

  /// The file, or null while it comes (asked for once, and after a
  /// failure not again for a minute).
  ChatFile? of(LinkMessage m) {
    if (_got.containsKey(m.id)) {
      final f = _got.remove(m.id);
      // Drawn now: the last to go.
      _got[m.id] = f;
      return f;
    }
    final failed = _failed[m.id];
    if (failed != null && now().difference(failed) < const Duration(minutes: 1)) return null;
    _got[m.id] = null;
    unawaited(fetch(m));
    return null;
  }

  Future<ChatFile?> fetch(LinkMessage m) async {
    try {
      // Kept on the phone (a message's file never changes): at once, and
      // offline too; else from the server, then kept.
      final at = _pathOf(m);
      final kept = await keptMedia(at);
      final b = kept ?? Uint8List.fromList(await transport.bytes(at));
      if (kept == null) unawaited(keepMediaBytes(at, b));
      Size? size;
      if (m.attachment?.kind == 'photo') {
        final codec = await ui.instantiateImageCodec(b);
        final frame = await codec.getNextFrame();
        size = Size(frame.image.width.toDouble(), frame.image.height.toDouble());
        frame.image.dispose();
        codec.dispose();
      }
      final f = ChatFile(b, size);
      _failed.remove(m.id);
      _put(m.id, f);
      notifyListeners();
      return f;
    } on Object {
      if (_got.containsKey(m.id) && _got[m.id] == null) _got.remove(m.id);
      _failed[m.id] = now();
      return null;
    }
  }
}

/// `.bubble .chat-photo img { max-width: 100%; max-height: 320px }`: the
/// photo's size in a bubble whose content is [room] wide.
Size photoSize(Size natural, double room) {
  final s = [1.0, room / natural.width, 320 / natural.height].reduce((a, b) => a < b ? a : b);
  return Size(natural.width * s, natural.height * s);
}

/// Hands a file to the phone's share sheet, from where it opens in a
/// viewer or is kept (the web downloads it). A test stands in for it.
typedef FileHandOff = Future<void> Function(Uint8List bytes, String name, String type, Rect? from);

FileHandOff handOffFile = (bytes, name, type, from) async {
  await stepOut(() => SharePlus.instance.share(
    ShareParams(files: [XFile.fromData(bytes, mimeType: type, name: name)], fileNameOverrides: [name], sharePositionOrigin: from),
  ));
};

/// Conversation.tsx AttachmentView's link: a photo opens full size, as the
/// web opens it in a tab of its own; here over everything, pinched to look
/// closer, closed with Close or Back.
Future<void> showPhoto(BuildContext context, Uint8List bytes, String label) {
  return Navigator.of(context).push<void>(
    PageRouteBuilder<void>(
      opaque: false,
      transitionDuration: (MediaQuery.maybeDisableAnimationsOf(context) ?? false) ? Duration.zero : const Duration(milliseconds: 120),
      reverseTransitionDuration: Duration.zero,
      pageBuilder: (context, _, _) => _PhotoView(bytes: bytes, label: label),
      transitionsBuilder: (context, a, _, child) => FadeTransition(opacity: a, child: child),
    ),
  );
}

class _PhotoView extends StatelessWidget {
  const _PhotoView({required this.bytes, required this.label});
  final Uint8List bytes;
  final String label;

  @override
  Widget build(BuildContext context) {
    final pad = MediaQuery.paddingOf(context);
    const white = Color(0xFFFFFFFF);
    return Semantics(
      scopesRoute: true,
      namesRoute: true,
      explicitChildNodes: true,
      label: label,
      child: ColoredBox(
        color: const Color(0xF2000000),
        child: Stack(
          children: [
            Positioned.fill(
              child: InteractiveViewer(
                maxScale: 5,
                child: Center(child: Semantics(image: true, label: label, child: Image.memory(bytes, fit: BoxFit.contain, gaplessPlayback: true))),
              ),
            ),
            Positioned(
              top: pad.top + 8,
              left: 8,
              child: Semantics(
                button: true,
                label: 'Close',
                onTap: () => Navigator.of(context).maybePop(),
                excludeSemantics: true,
                child: TkPress(
                  onTap: () => Navigator.of(context).maybePop(),
                  child: const SizedBox(width: 48, height: 48, child: Center(child: TkIcon('close', size: 24, color: white))),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The attachment in its bubble.
class AttachmentView extends StatelessWidget {
  const AttachmentView({super.key, required this.m, required this.files, required this.other, required this.room, this.local, this.stamp});

  final LinkMessage m;

  /// Still being sent: this phone's own copy (a photo's preview, or the
  /// name), as the web shows one.
  final PickedFile? local;
  final ChatFiles files;
  final String other;

  /// The width the bubble's content may take.
  final double room;

  /// A voice note's time and ticks, at the end of its row (as drawn).
  final Widget? stamp;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = m.attachment!;
    final l = local;
    if (l != null) {
      if (l.photo) {
        return _LocalPhoto(file: l, room: room);
      }
      return _fileRow(t, a);
    }
    switch (a.kind) {
      case 'photo':
        return ListenableBuilder(
          listenable: files,
          builder: (context, _) {
            final f = files.of(m);
            final size = f?.size;
            // `.chat-photo { margin: 2px 0 4px }`; nothing until it is here,
            // as the web's image has no size before it loads.
            if (f == null || size == null) return const SizedBox(height: 6);
            final s = photoSize(size, room);
            void open() => showPhoto(context, f.bytes, m.mine ? 'Your photo' : 'Photo from $other');
            return Padding(
              padding: const EdgeInsets.only(top: 2, bottom: 4),
              child: Semantics(
                image: true,
                button: true,
                label: m.mine ? 'Your photo' : 'Photo from $other',
                onTapHint: 'Open it',
                onTap: open,
                // As wide as the photo is (up to the room), the picture at
                // its start: a tall photo leaves the bubble as wide as the
                // web's, which sizes it by the photo's own width.
                child: GestureDetector(
                  // Said above, as one node with its words.
                  excludeFromSemantics: true,
                  onTap: open,
                  child: SizedBox(
                  width: size.width < room ? size.width : room,
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(8),
                      child: Image.memory(
                        f.bytes,
                        width: s.width,
                        height: s.height,
                        // Decoded at the size it is shown, not the camera's.
                        cacheWidth: (s.width * MediaQuery.devicePixelRatioOf(context)).round(),
                        fit: BoxFit.fill,
                        gaplessPlayback: true,
                        excludeFromSemantics: true,
                      ),
                    ),
                  ),
                ),
                ),
              ),
            );
          },
        );
      case 'voice':
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 2),
          child: VoicePlayer(m: m, files: files, label: m.mine ? 'Your voice note' : 'Voice note from $other', width: room, trailing: stamp),
        );
      default:
        return _fileRow(t, a, onTap: (from) => _open(context, a, from));
    }
  }

  /// A PDF pressed: fetched if it is not here, then to the share sheet.
  Future<void> _open(BuildContext context, LinkMessageAttachment a, Rect from) async {
    final f = files.of(m) ?? await files.fetch(m);
    if (f == null) {
      if (context.mounted) showTkToast(context, 'The file didn\'t load. Try again.', error: true);
      return;
    }
    await handOffFile(f.bytes, a.name ?? attachmentLabel(a), a.type, from);
  }

  /// `.chat-file`: the file's icon, its name, its size, on panel-2.
  Widget _fileRow(TkTokens t, LinkMessageAttachment a, {void Function(Rect from)? onTap}) => Padding(
    padding: const EdgeInsets.only(top: 2, bottom: 4),
    child: Builder(
      builder: (context) {
        // Opened from where it is (the viewer grows from the row).
        final open = onTap == null
            ? null
            : () {
                final box = context.findRenderObject() as RenderBox?;
                onTap(box == null ? Rect.zero : box.localToGlobal(Offset.zero) & box.size);
              };
        return TapArea(child: Semantics(
          button: true,
          label: '${attachmentLabel(a)}, ${fileSize(a.size)}',
          onTap: open,
          excludeSemantics: true,
          child: GestureDetector(
            onTap: open,
            child: Container(
              padding: const EdgeInsets.all(8),
              decoration: BoxDecoration(color: t.panel2, borderRadius: BorderRadius.circular(8)),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TkIcon('file', size: 22, color: t.text),
                  const SizedBox(width: 8),
                  Flexible(
                    child: Text(attachmentLabel(a), style: tkText(size: 14, weight: 600, color: t.text)),
                  ),
                  const SizedBox(width: 8),
                  Text(fileSize(a.size), style: tkText(size: 13, color: m.mine ? t.chatOutMeta : t.muted)),
                ],
              ),
            ),
          ),
        ));
      },
    ),
  );
}

/// Our photo on its way, from this phone's own copy.
class _LocalPhoto extends StatefulWidget {
  const _LocalPhoto({required this.file, required this.room});
  final PickedFile file;
  final double room;

  @override
  State<_LocalPhoto> createState() => _LocalPhotoState();
}

class _LocalPhotoState extends State<_LocalPhoto> {
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
    final size = _size;
    if (size == null) return const SizedBox(height: 6);
    final s = photoSize(size, widget.room);
    return Padding(
      padding: const EdgeInsets.only(top: 2, bottom: 4),
      child: Semantics(
        image: true,
        label: 'Your photo',
        child: ClipRRect(
          borderRadius: BorderRadius.circular(8),
          child: Image.memory(widget.file.bytes, width: s.width, height: s.height, cacheWidth: (s.width * MediaQuery.devicePixelRatioOf(context)).round(), fit: BoxFit.fill, gaplessPlayback: true),
        ),
      ),
    );
  }
}

/// A voice note: play, its bars filling as it plays, and how long it is.
class VoicePlayer extends StatefulWidget {
  const VoicePlayer({super.key, required this.m, required this.files, required this.label, this.width = 260, this.trailing});
  final LinkMessage m;
  final ChatFiles files;
  final String label;
  final double width;

  /// The message's time and ticks, last in the row.
  final Widget? trailing;

  @override
  State<VoicePlayer> createState() => _VoicePlayerState();
}

class _VoicePlayerState extends State<VoicePlayer> {
  AudioPlayer? _player;
  final _subs = <StreamSubscription<Object?>>[];
  bool _playing = false;
  Duration _at = Duration.zero;
  Duration _length = Duration.zero;

  @override
  void dispose() {
    for (final s in _subs) {
      s.cancel();
    }
    _player?.dispose();
    super.dispose();
  }

  Future<void> _toggle() async {
    var p = _player;
    if (p == null) {
      // `preload="none"`: the note is fetched when it is first played.
      final f = widget.files.of(widget.m) ?? await widget.files.fetch(widget.m);
      if (f == null || !mounted) return;
      p = _player = AudioPlayer();
      _subs
        ..add(p.positionStream.listen((d) => setState(() => _at = d)))
        ..add(p.durationStream.listen((d) => setState(() => _length = d ?? Duration.zero)))
        ..add(
          p.playerStateStream.listen((s) {
            if (s.processingState == ProcessingState.completed) {
              p!.pause();
              p.seek(Duration.zero);
            }
            setState(() => _playing = s.playing && s.processingState != ProcessingState.completed);
          }),
        );
      // Played from a file of the app's own, as the player reads files.
      final dir = await getTemporaryDirectory();
      final file = File('${dir.path}/voice-${widget.m.id}${_ext(widget.m.attachment?.type)}');
      if (!file.existsSync()) await file.writeAsBytes(f.bytes, flush: true);
      await p.setFilePath(file.path);
    }
    if (p.playing) {
      await p.pause();
    } else {
      unawaited(p.play());
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final progress = _length.inMilliseconds == 0 ? 0.0 : (_at.inMilliseconds / _length.inMilliseconds).clamp(0.0, 1.0);
    // A lime play button, bars that fill as it plays, and the time
    // (docs/spec/mobile-app-look.md, "The chat").
    return Semantics(
      label: widget.label,
      button: true,
      value: '${_clock(_at)} of ${_clock(_length)}',
      onTapHint: _playing ? 'Pause' : 'Play',
      onTap: _toggle,
      excludeSemantics: true,
      child: TkPress(
        onTap: _toggle,
        scale: 1,
        child: SizedBox(
          width: widget.width,
          height: 48,
          child: Row(
            children: [
              Container(
                width: 36,
                height: 36,
                decoration: const BoxDecoration(color: Color(0xFFC8F169), shape: BoxShape.circle),
                alignment: Alignment.center,
                child: TkIcon(_playing ? 'pause' : 'play', size: 16, fill: true, color: const Color(0xFF0D3B2E)),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: CustomPaint(
                  size: const Size(double.infinity, 26),
                  painter: _VoiceBars(seed: widget.m.id.hashCode, progress: progress, color: t.text),
                ),
              ),
              const SizedBox(width: 10),
              Text(_clock(_at > Duration.zero ? _at : _length), style: tkText(size: 12, lineHeight: 1, color: widget.m.mine ? t.chatOutMeta : t.muted).copyWith(fontFeatures: const [FontFeature.tabularFigures()])),
              if (widget.trailing != null) ...[
                const SizedBox(width: 10),
                Padding(padding: const EdgeInsets.only(top: 18), child: widget.trailing),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// A voice note's bars: the same for a note each time it is drawn, the
/// part played full and the rest soft.
class _VoiceBars extends CustomPainter {
  _VoiceBars({required this.seed, required this.progress, required this.color});
  final int seed;
  final double progress;
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    const gap = 2.0, w = 3.0;
    final n = ((size.width + gap) / (w + gap)).floor();
    var x = seed & 0x7fffffff;
    final p = Paint()..isAntiAlias = true;
    for (var i = 0; i < n; i++) {
      x = (x * 1103515245 + 12345) & 0x7fffffff;
      final h = 5 + (x % 1000) / 1000 * (size.height - 5);
      final left = i * (w + gap);
      p.color = (i + 0.5) / n <= progress ? color : color.withValues(alpha: 0.4);
      canvas.drawRRect(RRect.fromRectAndRadius(Rect.fromLTWH(left, (size.height - h) / 2, w, h), const Radius.circular(1.5)), p);
    }
  }

  @override
  bool shouldRepaint(_VoiceBars old) => old.progress != progress || old.color != color || old.seed != seed;
}

/// The file's ending, by its type, so the phone's player reads it.
String _ext(String? type) => switch (type) {
  'audio/ogg' => '.ogg',
  'audio/webm' => '.webm',
  'audio/mpeg' => '.mp3',
  _ => '.m4a',
};

String _clock(Duration d) => '${d.inMinutes}:${(d.inSeconds % 60).toString().padLeft(2, '0')}';
