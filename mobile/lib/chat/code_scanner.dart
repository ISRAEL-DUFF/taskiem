// Scanning a code with the phone's camera (docs/spec/device-fields.md §3,
// D2): a form step's scan field, a count of the shelf by scanning, and the
// scan from the phone's home screen. The camera is asked for only when the
// person taps Scan, after the question's why; a code may be typed instead
// where the question allows it.
//
// The camera is behind [codeScanner] (and the screen's [ScanCamera]), so a
// test gives its own and drives the real screen.

import 'package:flutter/services.dart' show HapticFeedback;
import 'package:flutter/widgets.dart';
import 'package:mobile_scanner/mobile_scanner.dart' as ms;

import '../kit/kit.dart';
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkFormGroup, TkListRow, TkSection, tkPush;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkCta, TkKitField;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'words.dart' show CardTone;

/// A code the phone read: its text, and its format as the server names it
/// (`ean_13`, `qr`…), or null when it was typed.
class ScannedCode {
  const ScannedCode(this.text, [this.format]);
  final String text;
  final String? format;

  /// As a form or a tool's start sends it.
  Map<String, Object> toJson() => {'text': text, 'format': ?format};

  @override
  bool operator ==(Object other) => other is ScannedCode && other.text == text && other.format == format;

  @override
  int get hashCode => Object.hash(text, format);
}

/// The formats the scanner reads, as the server names them (wd.ScanFormats).
const scanFormats = ['qr', 'ean_13', 'ean_8', 'upc_a', 'upc_e', 'code_128'];

/// A format in words: "a QR code", "an EAN-13 barcode".
String formatWords(String? f) => switch (f) {
  'qr' => 'QR code',
  'ean_13' => 'EAN-13 barcode',
  'ean_8' => 'EAN-8 barcode',
  'upc_a' => 'UPC-A barcode',
  'upc_e' => 'UPC-E barcode',
  'code_128' => 'Code 128 barcode',
  _ => 'Typed',
};

/// A format with its article: "an EAN-13 barcode", "a QR code".
String aFormat(String? f) {
  final w = formatWords(f);
  return w.startsWith('EAN') ? 'an $w' : 'a $w';
}

String? _formatOf(ms.BarcodeFormat f) => switch (f) {
  ms.BarcodeFormat.qrCode => 'qr',
  ms.BarcodeFormat.ean13 => 'ean_13',
  ms.BarcodeFormat.ean8 => 'ean_8',
  ms.BarcodeFormat.upcA => 'upc_a',
  ms.BarcodeFormat.upcE => 'upc_e',
  ms.BarcodeFormat.code128 => 'code_128',
  _ => null,
};

ms.BarcodeFormat _msFormat(String f) => switch (f) {
  'qr' => ms.BarcodeFormat.qrCode,
  'ean_13' => ms.BarcodeFormat.ean13,
  'ean_8' => ms.BarcodeFormat.ean8,
  'upc_a' => ms.BarcodeFormat.upcA,
  'upc_e' => ms.BarcodeFormat.upcE,
  _ => ms.BarcodeFormat.code128,
};

/// The camera's view, which calls [onCode] with each code it reads.
typedef ScanCamera = Widget Function(BuildContext context, List<String> formats, ValueChanged<ScannedCode> onCode);

/// The phone's camera reading codes, behind one face.
abstract class CodeScanner {
  const CodeScanner();

  /// One code, or null when put away.
  Future<ScannedCode?> scanOne(BuildContext context, {required String title, String? why, List<String>? formats, bool allowTyped = true, String backLabel = 'Back'});

  /// Codes one after another (a count), each as often as it was scanned,
  /// in order; null when put away. [name] says what a code is (an item's
  /// name), or null when it is not known.
  Future<List<ScannedCode>?> scanMany(BuildContext context, {required String title, String? why, List<String>? formats, String? Function(ScannedCode)? name, String backLabel = 'Back'});
}

/// The phone's own camera, through mobile_scanner; [camera] in place of it
/// (a test's).
class DeviceCodeScanner extends CodeScanner {
  const DeviceCodeScanner({this.camera, this.gap = const Duration(milliseconds: 1500)});
  final ScanCamera? camera;

  /// How long a code must be out of sight before it counts again.
  final Duration gap;

  @override
  Future<ScannedCode?> scanOne(BuildContext context, {required String title, String? why, List<String>? formats, bool allowTyped = true, String backLabel = 'Back'}) async {
    final got = await tkPush<List<ScannedCode>>(
      context,
      CodeScanScreen(title: title, why: why, formats: formats, allowTyped: allowTyped, backLabel: backLabel, camera: camera, gap: gap),
    );
    return got == null || got.isEmpty ? null : got.first;
  }

  @override
  Future<List<ScannedCode>?> scanMany(BuildContext context, {required String title, String? why, List<String>? formats, String? Function(ScannedCode)? name, String backLabel = 'Back'}) =>
      tkPush<List<ScannedCode>>(context, CodeScanScreen(title: title, why: why, formats: formats, many: true, name: name, backLabel: backLabel, camera: camera, gap: gap));
}

/// The scanner the app uses (a test gives its own).
CodeScanner codeScanner = const DeviceCodeScanner();

/// The words when the camera could not be used.
String cameraProblemWords(ms.MobileScannerErrorCode code, {required bool typed}) {
  final instead = typed ? ' Type the code instead.' : '';
  return switch (code) {
    ms.MobileScannerErrorCode.permissionDenied => 'Taskiem isn’t allowed to use the camera. Allow it in your phone’s settings.$instead',
    ms.MobileScannerErrorCode.unsupported => 'This phone’s camera can’t scan here.$instead',
    _ => 'The camera couldn’t start.$instead',
  };
}

/// The camera through mobile_scanner, for the formats asked.
Widget _deviceCamera(BuildContext context, List<String> formats, ValueChanged<ScannedCode> onCode, {required bool typed}) => _DeviceCamera(formats: formats, onCode: onCode, typed: typed);

class _DeviceCamera extends StatefulWidget {
  const _DeviceCamera({required this.formats, required this.onCode, required this.typed});
  final List<String> formats;
  final ValueChanged<ScannedCode> onCode;
  final bool typed;

  @override
  State<_DeviceCamera> createState() => _DeviceCameraState();
}

class _DeviceCameraState extends State<_DeviceCamera> {
  late final _camera = ms.MobileScannerController(formats: [for (final f in widget.formats) _msFormat(f)]);

  @override
  void dispose() {
    _camera.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ms.MobileScanner(
      controller: _camera,
      onDetect: (c) {
        for (final b in c.barcodes) {
          final text = b.rawValue;
          if (text != null && text.trim().isNotEmpty) widget.onCode(ScannedCode(text, _formatOf(b.format)));
        }
      },
      errorBuilder: (context, e) => ColoredBox(
        color: t.tkSoft,
        child: Center(
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Text(cameraProblemWords(e.errorCode, typed: widget.typed), textAlign: TextAlign.center, style: tkText(size: 15, lineHeight: 1.45, color: t.text)),
          ),
        ),
      ),
    );
  }
}

/// The scan screen: the camera, what it read, and the code typed where the
/// question allows. One code closes it; [many] counts codes until Done.
class CodeScanScreen extends StatefulWidget {
  const CodeScanScreen({
    super.key,
    required this.title,
    this.why,
    this.formats,
    this.many = false,
    this.allowTyped = true,
    this.name,
    this.backLabel = 'Back',
    this.camera,
    this.gap = const Duration(milliseconds: 1500),
  });
  final String title;
  final String? why;
  final List<String>? formats;
  final bool many;
  final bool allowTyped;
  final String? Function(ScannedCode)? name;
  final String backLabel;
  final ScanCamera? camera;

  /// How long the camera must not see a code before it counts again: one
  /// held in front of it counts once.
  final Duration gap;

  @override
  State<CodeScanScreen> createState() => _CodeScanScreenState();
}

class _CodeScanScreenState extends State<CodeScanScreen> {
  final _got = <ScannedCode>[];
  final _typed = TextEditingController();
  bool _typing = false;
  bool _done = false;
  String? _said;
  ScannedCode? _last;
  DateTime _lastAt = DateTime(2000);

  List<String> get _formats => widget.formats == null || widget.formats!.isEmpty ? scanFormats : widget.formats!;

  @override
  void dispose() {
    _typed.dispose();
    super.dispose();
  }

  void _seen(ScannedCode c) {
    if (_done || !mounted) return;
    final now = DateTime.now();
    final again = c == _last && now.difference(_lastAt) < widget.gap;
    _last = c;
    _lastAt = now;
    if (again) return;
    if (c.format != null && !_formats.contains(c.format)) {
      setState(() => _said = 'That code is ${aFormat(c.format)}. This takes ${_formats.map(aFormat).join(' or ')}.');
      return;
    }
    _take(c);
  }

  void _take(ScannedCode c) {
    HapticFeedback.selectionClick();
    if (!widget.many) {
      _done = true;
      Navigator.of(context).pop(<ScannedCode>[c]);
      return;
    }
    setState(() {
      _got.add(c);
      _said = null;
    });
  }

  void _useTyped() {
    final s = _typed.text.trim();
    if (s.isEmpty) return;
    _typed.clear();
    _take(ScannedCode(s));
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final camera = widget.camera ?? (c, f, on) => _deviceCamera(c, f, on, typed: widget.allowTyped);
    // What was scanned, each code once with how many times, newest first.
    final counts = <ScannedCode, int>{};
    for (final c in _got.reversed) {
      counts[c] = (counts[c] ?? 0) + 1;
    }
    return TkPageScreen(
      title: widget.title,
      titleSize: 22,
      backLabel: widget.backLabel,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(20),
          child: AspectRatio(
            aspectRatio: widget.many ? 4 / 3 : 1,
            child: Semantics(label: 'The camera, reading codes', child: camera(context, _formats, _seen)),
          ),
        ),
        Text(
          [
            if (widget.why != null && widget.why!.isNotEmpty) 'Why: ${widget.why}${RegExp(r'[.!?]$').hasMatch(widget.why!) ? '' : '.'}',
            widget.many ? 'Point the camera at each code in turn. A code is counted each time you scan it.' : 'Point the camera at the code.',
          ].join('\n'),
          textAlign: TextAlign.center,
          style: tkText(size: 13, lineHeight: 1.4, color: t.muted),
        ),
        if (_said != null)
          Semantics(
            liveRegion: true,
            child: TkCallout(leading: TkSquare('b-alert', color: look.orange), title: 'Not taken', sub: _said, subLines: 3, color: look.orange),
          ),
        if (widget.many && counts.isNotEmpty)
          TkSection(
            title: 'Scanned',
            inset: 56,
            children: [
              for (final e in counts.entries)
                TkListRow(
                  title: widget.name?.call(e.key) ?? 'Not one of your items yet',
                  sub: '${e.key.text} · ${formatWords(e.key.format)}',
                  icon: 'b-qr',
                  square: widget.name?.call(e.key) == null ? look.orange : look.teal,
                  status: '×${e.value}',
                  statusTone: CardTone.lime,
                  chevron: false,
                ),
            ],
          ),
        if (widget.allowTyped && _typing)
          TkFormGroup(
            children: [
              TkKitField(
                label: 'The code',
                hint: 'The numbers under the barcode, or the words of the code.',
                child: Row(
                  children: [
                    Expanded(child: TkTextBox(controller: _typed, size: 15, height: 46, max: 2000)),
                    const SizedBox(width: 8),
                    TkSmallButton(label: widget.many ? 'Add' : 'Use it', primary: true, semantics: 'Use the code typed', onPressed: _useTyped),
                  ],
                ),
              ),
            ],
          ),
        if (widget.allowTyped && !_typing) Center(child: TkLink(label: 'Type the code instead', bold: true, onTap: () => setState(() => _typing = true))),
        if (widget.many) TkCta(label: _got.isEmpty ? 'Done' : 'Done · ${_got.length} scanned', onTap: () => Navigator.of(context).pop(List<ScannedCode>.of(_got))),
      ],
    );
  }
}
