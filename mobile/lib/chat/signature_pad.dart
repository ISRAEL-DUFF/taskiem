// Signing on the screen (docs/spec/device-fields.md §3, D2): a form step's
// signature field. The person (a customer, handed the rider's phone) draws
// with a finger; what was drawn is made a PNG on the phone and sent with
// the answer as a photo is. Where the question asks, the signer's name is
// typed beside it.

import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/gestures.dart';
import 'package:flutter/widgets.dart';

import '../kit/kit.dart';
import '../kit/mobile.dart' show TkFormGroup, tkPush;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkCta, TkKitField;
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;

/// What was signed: the drawing as a PNG, and who signed when asked.
typedef Signed = ({Uint8List png, String? name});

/// The pad is paper: ink as a pen's on white in both themes, so what is
/// kept reads the same wherever it is shown (a card, a printout).
const _paper = Color(0xFFFFFFFF);
const _ink = Color(0xFF14201B);

/// Opens the pad; it closes with what was signed, or nothing.
Future<Signed?> signOnScreen(BuildContext context, {required String title, String? why, bool askName = false, String backLabel = 'Back'}) =>
    tkPush<Signed>(context, SignatureScreen(title: title, why: why, askName: askName, backLabel: backLabel));

/// A pan that takes the finger at once, so drawing never scrolls the page.
class _DrawGesture extends PanGestureRecognizer {
  _DrawGesture({super.debugOwner});

  @override
  void addAllowedPointer(PointerDownEvent event) {
    super.addAllowedPointer(event);
    resolve(GestureDisposition.accepted);
  }
}

/// The strokes drawn, each a line of points.
class SignatureStrokes extends ChangeNotifier {
  final strokes = <List<Offset>>[];

  bool get isEmpty => strokes.every((s) => s.isEmpty);

  void start(Offset p) {
    strokes.add([p]);
    notifyListeners();
  }

  void add(Offset p) {
    if (strokes.isEmpty) return;
    strokes.last.add(p);
    notifyListeners();
  }

  void clear() {
    strokes.clear();
    notifyListeners();
  }

  /// The drawing at [size], made a PNG at [scale] times its size.
  Future<Uint8List> png(Size size, {double scale = 2}) async {
    final rec = ui.PictureRecorder();
    final canvas = Canvas(rec)..scale(scale);
    _paint(canvas, size, strokes);
    final image = await rec.endRecording().toImage((size.width * scale).round(), (size.height * scale).round());
    final data = await image.toByteData(format: ui.ImageByteFormat.png);
    image.dispose();
    return data!.buffer.asUint8List();
  }
}

void _paint(Canvas canvas, Size size, List<List<Offset>> strokes) {
  canvas.drawRect(Offset.zero & size, Paint()..color = _paper);
  final pen = Paint()
    ..color = _ink
    ..strokeWidth = 2.6
    ..strokeCap = StrokeCap.round
    ..strokeJoin = StrokeJoin.round
    ..style = PaintingStyle.stroke;
  for (final s in strokes) {
    if (s.length == 1) {
      canvas.drawCircle(s.first, 1.4, Paint()..color = _ink);
      continue;
    }
    final path = Path()..moveTo(s.first.dx, s.first.dy);
    for (final p in s.skip(1)) {
      path.lineTo(p.dx, p.dy);
    }
    canvas.drawPath(path, pen);
  }
}

class _StrokesPainter extends CustomPainter {
  _StrokesPainter(this.strokes, this.line) : super(repaint: strokes);
  final SignatureStrokes strokes;
  final Color line;

  @override
  void paint(Canvas canvas, Size size) {
    _paint(canvas, size, strokes.strokes);
    // The line to sign on, as on paper.
    final y = size.height - 34;
    canvas.drawLine(Offset(18, y), Offset(size.width - 18, y), Paint()
      ..color = line
      ..strokeWidth = 1);
  }

  @override
  bool shouldRepaint(_StrokesPainter old) => true;
}

/// The pad: a box of paper that takes a finger's strokes.
class SignaturePad extends StatelessWidget {
  const SignaturePad({super.key, required this.strokes, this.height = 200, this.label = 'Sign here with your finger'});
  final SignatureStrokes strokes;
  final double height;
  final String label;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      label: label,
      image: true,
      child: ClipRRect(
        borderRadius: BorderRadius.circular(14),
        child: SizedBox(
          height: height,
          child: RawGestureDetector(
            gestures: {
              _DrawGesture: GestureRecognizerFactoryWithHandlers<_DrawGesture>(
                () => _DrawGesture(debugOwner: this),
                (g) => g
                  ..onStart = ((d) => strokes.start(d.localPosition))
                  ..onUpdate = ((d) => strokes.add(d.localPosition)),
              ),
            },
            child: CustomPaint(painter: _StrokesPainter(strokes, t.muted.withValues(alpha: 0.5)), size: Size.infinite),
          ),
        ),
      ),
    );
  }
}

/// The page with the pad: the question's why, the pad, the signer's name
/// where asked, and Use this signature.
class SignatureScreen extends StatefulWidget {
  const SignatureScreen({super.key, required this.title, this.why, this.askName = false, this.backLabel = 'Back'});
  final String title;
  final String? why;
  final bool askName;
  final String backLabel;

  @override
  State<SignatureScreen> createState() => _SignatureScreenState();
}

class _SignatureScreenState extends State<SignatureScreen> {
  final _strokes = SignatureStrokes();
  final _name = TextEditingController();
  final _pad = GlobalKey();
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _strokes.addListener(() => setState(() {}));
    _name.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _strokes.dispose();
    _name.dispose();
    super.dispose();
  }

  bool get _ready => !_strokes.isEmpty && (!widget.askName || _name.text.trim().isNotEmpty);

  Future<void> _use() async {
    final box = _pad.currentContext?.findRenderObject() as RenderBox?;
    if (box == null || !_ready) return;
    setState(() => _busy = true);
    try {
      final png = await _strokes.png(box.size);
      if (!mounted) return;
      Navigator.of(context).pop<Signed>((png: png, name: widget.askName ? _name.text.trim().replaceAll(RegExp(r'\s+'), ' ') : null));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TkPageScreen(
      title: widget.title,
      titleSize: 22,
      backLabel: widget.backLabel,
      children: [
        if (widget.why != null && widget.why!.isNotEmpty) Text('Why: ${widget.why}', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
        TkFormGroup(
          children: [
            TkKitField(
              label: 'Signature',
              field: false,
              hint: _strokes.isEmpty ? 'Sign in the box with a finger.' : 'Not right? Clear it and sign again.',
              child: SignaturePad(key: _pad, strokes: _strokes),
            ),
            if (!_strokes.isEmpty)
              Align(alignment: Alignment.centerRight, child: TkSmallButton(label: 'Clear', semantics: 'Clear the signature', onPressed: _strokes.clear)),
          ],
        ),
        if (widget.askName)
          TkFormGroup(
            children: [
              TkKitField(
                label: 'Name of who signed',
                child: TkTextBox(controller: _name, size: 15, height: 46, max: 120),
              ),
            ],
          ),
        TkCta(label: _busy ? 'Saving…' : 'Use this signature', onTap: _ready && !_busy ? _use : null),
      ],
    );
  }
}
