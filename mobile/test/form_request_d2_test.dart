// A form step's scan and signature answered in the app (device fields D2):
// the camera asked only at the tap, after the why, through the scan screen
// (a stand-in camera here); a code typed where the question allows; a
// signature drawn with a finger, sent as a PNG beside the photo with the
// signer's name; kept offline and sent later with both files.

import 'dart:convert';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart' show MemoryKeyStore;
import 'package:taskiem/app/store.dart';
import 'package:taskiem/chat/code_scanner.dart';
import 'package:taskiem/chat/form_request.dart';
import 'package:taskiem/chat/signature_pad.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'people_gaps_test.dart' show FakePlaces;
import 'support.dart';

const _rid = '0192e2e0-0000-7000-8000-00000000d2d2';

final _request = {
  'id': _rid,
  'run_id': '0192e2e0-0000-7000-8000-00000000d2d3',
  'title': 'Delivered',
  'description': null,
  'role': '',
  'from': 'Ada Stores',
  'status': 'pending',
  'created_at': '2026-11-23T11:40:00Z',
  'expires_at': '2026-11-23T14:00:00Z',
  'schema': {
    'type': 'object',
    'required': ['parcel'],
    'properties': {
      'parcel': {'type': 'scan', 'why': 'the label on the parcel, so the right order is closed', 'formats': ['qr', 'code_128']},
      'signed': {'type': 'signature', 'why': 'the customer signs that it came', 'ask_name': true},
    },
  },
};

/// A camera that reads what the test shows it.
class StandInCamera {
  ValueChanged<ScannedCode>? _on;
  int opened = 0;

  Widget call(BuildContext context, List<String> formats, ValueChanged<ScannedCode> onCode) {
    _on = onCode;
    opened++;
    final t = TkTheme.tokens(context);
    return ColoredBox(
      color: const Color(0xFF1D2A24),
      child: Center(
        child: Container(
          width: 200,
          height: 120,
          decoration: BoxDecoration(border: Border.all(color: t.brand, width: 3), borderRadius: BorderRadius.circular(14)),
        ),
      ),
    );
  }

  void show(ScannedCode c) => _on?.call(c);
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}

/// Signs on the pad with a finger: two strokes.
Future<void> signPad(WidgetTester t) async {
  final pad = t.getRect(find.byType(SignaturePad));
  final g = await t.startGesture(pad.topLeft + const Offset(40, 120));
  for (var i = 1; i <= 12; i++) {
    await g.moveBy(Offset(16, i.isEven ? -14 : 12));
  }
  await g.up();
  final g2 = await t.startGesture(pad.topLeft + const Offset(80, 70));
  for (var i = 1; i <= 8; i++) {
    await g2.moveBy(Offset(18, i < 4 ? 6 : -4));
  }
  await g2.up();
  await t.pump();
}

void main() {
  setUpAll(loadFonts);
  late StandInCamera camera;
  setUp(() {
    camera = StandInCamera();
    final was = codeScanner;
    final wasPlace = formPlaceFinder;
    codeScanner = DeviceCodeScanner(camera: camera.call);
    formPlaceFinder = FakePlaces();
    addTearDown(() {
      codeScanner = was;
      formPlaceFinder = wasPlace;
    });
  });

  Future<({List<String> sent, Auth auth})> open(WidgetTester t, {bool Function()? online}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 1100);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
    final auth = f.auth(
      also: (req) async {
        if (online != null && !online()) throw http.ClientException('no connection');
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/form-requests/$_rid') return ok(_request);
        if (req.method == 'POST' && p == '/v1/form-requests/$_rid/answers') {
          sent.add('${req.headers['content-type']} ${latin1.decode(req.bodyBytes)}');
          return ok({
            'submission': {'id': 's1', 'reference': 'F-2', 'status': 'answered', 'channel': 'app', 'answers': {}, 'respondent': {'kind': 'linked'}, 'created_at': '2026-11-23T12:00:00Z'},
          }, 201);
        }
        return null;
      },
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: FormRequestScreen(auth: auth, id: _rid, now: () => DateTime.utc(2026, 11, 23, 12))));
    await settle(t);
    return (sent: sent, auth: auth);
  }

  testWidgets('a code scanned at the tap and a customer’s signature with their name, sent with the answer; pictured', (t) async {
    final o = await open(t);
    expect(find.text('Scan the code'), findsOneWidget);
    expect(find.text('Sign on the screen'), findsOneWidget);
    // Nothing asks the camera before the tap.
    expect(camera.opened, 0);

    await t.tap(find.text('Scan the code'));
    await settle(t);
    expect(camera.opened, greaterThan(0));
    expect(find.textContaining('Why: the label on the parcel'), findsOneWidget);
    await looks(t, 'scan-sheet');
    // A barcode this question does not take is said so, not taken.
    camera.show(const ScannedCode('6151100093217', 'ean_13'));
    await settle(t);
    expect(find.textContaining('That code is an EAN-13 barcode. This takes a QR code or a Code 128 barcode.'), findsOneWidget);
    camera.show(const ScannedCode('PKG-7731-LEKKI', 'code_128'));
    await settle(t);
    expect(find.text('PKG-7731-LEKKI · Code 128 barcode'), findsOneWidget);
    expect(find.text('Scanned'), findsOneWidget);

    await t.tap(find.text('Sign on the screen'));
    await settle(t);
    expect(find.textContaining('Why: the customer signs'), findsOneWidget);
    await signPad(t);
    await t.enterText(find.byType(EditableText).first, 'Ngozi Eze');
    await settle(t);
    await looks(t, 'signature-pad');
    await t.tap(find.text('Use this signature'));
    await settle(t);
    expect(find.text('Signed by Ngozi Eze'), findsOneWidget);
    await looks(t, 'form-request-d2');

    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    final body = o.sent.single;
    expect(body, startsWith('multipart/form-data'));
    expect(body, contains('"parcel":{"text":"PKG-7731-LEKKI","format":"code_128"}'));
    expect(body, contains('"signed":{"signed_by":"Ngozi Eze"}'));
    expect(body, contains('name="signed"; filename="signature.png"'));
    expect(body, contains('content-type: image/png'));
    expect(body, contains('\x89PNG'));
    expect(find.text('Sent'), findsOneWidget);
  });

  testWidgets('a code typed where the camera cannot read it; the pad cleared and signed again', (t) async {
    final o = await open(t);
    await t.tap(find.text('Scan the code'));
    await settle(t);
    await t.tap(find.text('Type the code instead'));
    await settle(t);
    await t.enterText(find.byType(EditableText).first, 'PKG-0001');
    await t.tap(find.bySemanticsLabel('Use the code typed'));
    await settle(t);
    expect(find.text('Typed'), findsOneWidget);

    await t.tap(find.text('Sign on the screen'));
    await settle(t);
    expect(find.text('Use this signature'), findsOneWidget);
    await signPad(t);
    await t.tap(find.bySemanticsLabel('Clear the signature'));
    await t.pump();
    expect(find.text('Sign in the box with a finger.'), findsOneWidget);
    await signPad(t);
    await t.enterText(find.byType(EditableText).first, 'Ngozi');
    await t.pump();
    await t.tap(find.text('Use this signature'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(o.sent.single, contains('"parcel":{"text":"PKG-0001"}'));
  });

  testWidgets('made with no connection: the code and the signature kept, both sent when back', (t) async {
    final tmp = Directory.systemTemp.createTempSync('forms-d2');
    LocalStore.enabled = true;
    LocalStore.dataRoot = () async => Directory('${tmp.path}/data');
    LocalStore.mediaRoot = () async => Directory('${tmp.path}/media');
    addTearDown(() {
      LocalStore.current?.close();
      LocalStore.current = null;
      LocalStore.enabled = false;
      tmp.deleteSync(recursive: true);
    });
    var up = true;
    final o = await open(t, online: () => up);
    final store = (await t.runAsync(() => LocalStore.open('t_u2', MemoryKeyStore())))!;
    o.auth.local = store;
    await t.tap(find.text('Scan the code'));
    await settle(t);
    camera.show(const ScannedCode('PKG-9', 'qr'));
    await settle(t);
    await t.tap(find.text('Sign on the screen'));
    await settle(t);
    await signPad(t);
    await t.enterText(find.byType(EditableText).first, 'Ngozi');
    await t.pump();
    await t.tap(find.text('Use this signature'));
    await settle(t);
    up = false;
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.text('Kept on your phone'), findsOneWidget);
    up = true;
    final n = await t.runAsync(() => sendKeptFormAnswers(o.auth));
    expect(n, 1);
    expect(o.sent.single, contains('"online":false'));
    expect(o.sent.single, contains('name="signed"; filename="signature.png"'));
    expect(o.sent.single, contains('"parcel":{"text":"PKG-9","format":"qr"}'));
  });
}
