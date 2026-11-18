// Scanning a person's QR code in the app (docs/spec/people-chatting.md
// §5.2): the camera is asked for only when the person taps Scan a QR code;
// a code that is a person's Taskiem link opens who it is (`/p/{code}`), and
// anything else says so. Typing or pasting the link stays beside it, for a
// phone whose camera is refused or missing.
//
// The camera is behind [QrScanner], so a test gives its own.

import 'package:flutter/widgets.dart';
import 'package:mobile_scanner/mobile_scanner.dart' as ms;

import '../kit/kit.dart';
import '../kit/mobile.dart';
import '../theme/theme.dart';
import 'groups.dart' show groupCodeOf;
import 'people_find.dart' show personCodeOf;

/// The phone's camera reading a QR code, behind one face.
abstract class QrScanner {
  const QrScanner();

  /// A person's link's code read from a QR code (or a group's link as
  /// `/g/{code}`), or null when put away.
  Future<String?> scan(BuildContext context);
}

/// The phone's own camera, through mobile_scanner: QR codes only, until a
/// person's link is read.
class DeviceQrScanner extends QrScanner {
  const DeviceQrScanner();

  @override
  Future<String?> scan(BuildContext context) => tkPush<String>(context, const _ScanScreen());
}

/// What a scanned code says: a person's link's code, or null.
String? scannedPersonCode(String? raw) => raw == null ? null : personCodeOf(raw.trim());

/// What a scanned code opens: a person's link's code, or a group's link as
/// `/g/{code}` (people-chatting §9); null when it is neither.
String? scannedCode(String? raw) {
  if (raw == null) return null;
  final g = groupCodeOf(raw.trim());
  return g != null ? '/g/$g' : scannedPersonCode(raw);
}

/// The words when the camera could not be used.
String scanProblemWords(ms.MobileScannerErrorCode code) => switch (code) {
  ms.MobileScannerErrorCode.permissionDenied => 'Taskiem isn’t allowed to use the camera. Allow it in your phone’s settings, or go back and paste their link instead.',
  ms.MobileScannerErrorCode.unsupported => 'This phone’s camera can’t scan here. Go back and paste their link instead.',
  _ => 'The camera couldn’t start. Go back and paste their link instead.',
};

class _ScanScreen extends StatefulWidget {
  const _ScanScreen();

  @override
  State<_ScanScreen> createState() => _ScanScreenState();
}

class _ScanScreenState extends State<_ScanScreen> {
  final _camera = ms.MobileScannerController(formats: const [ms.BarcodeFormat.qrCode], detectionSpeed: ms.DetectionSpeed.noDuplicates);
  String? _said;
  bool _done = false;

  @override
  void dispose() {
    _camera.dispose();
    super.dispose();
  }

  void _seen(ms.BarcodeCapture c) {
    if (_done) return;
    for (final b in c.barcodes) {
      final code = scannedCode(b.rawValue);
      if (code != null) {
        _done = true;
        Navigator.of(context).pop(code);
        return;
      }
    }
    if (c.barcodes.isNotEmpty && mounted) setState(() => _said = 'That QR code isn’t a Taskiem link to a person or a group.');
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TkScreen(
      title: 'Scan their QR code',
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(20),
          child: AspectRatio(
            aspectRatio: 1,
            child: ms.MobileScanner(
              controller: _camera,
              onDetect: _seen,
              errorBuilder: (context, e) => ColoredBox(
                color: t.tkSoft,
                child: Center(child: Padding(padding: const EdgeInsets.all(20), child: Text(scanProblemWords(e.errorCode), textAlign: TextAlign.center, style: tkText(size: 15, lineHeight: 1.45, color: t.text)))),
              ),
            ),
          ),
        ),
        // What to do, quiet under the camera; the other way a link.
        Text('Point the camera at the QR code on their phone or in what they shared.', textAlign: TextAlign.center, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
        if (_said != null) Semantics(liveRegion: true, child: TkErrorBox(_said!)),
        Center(child: TkLink(label: 'Type or paste their link instead', bold: true, onTap: () => Navigator.of(context).maybePop())),
      ],
    );
  }
}
