// A form step answered in the app (docs/spec/device-fields.md §4, D1):
// the location read only at the tap, after the question's why; the camera,
// never the gallery; a refusal is an answer; an answer made offline kept
// and sent later with online: false.

import 'dart:convert';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart' show MemoryKeyStore;
import 'package:taskiem/app/store.dart';
import 'package:taskiem/chat/form_request.dart';
import 'package:taskiem/chat/people_place.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'people_gaps_test.dart' show FakePlaces;
import 'support.dart';

const _rid = '0192e2e0-0000-7000-8000-00000000d1d1';

final _request = {
  'id': _rid,
  'run_id': '0192e2e0-0000-7000-8000-00000000d1d2',
  'title': 'Delivered',
  'description': null,
  'role': '',
  'from': 'Ada Stores',
  'status': 'pending',
  'created_at': '2026-11-12T11:40:00Z',
  'expires_at': '2026-11-12T14:00:00Z',
  'schema': {
    'type': 'object',
    'required': ['where', 'proof'],
    'properties': {
      'where': {'type': 'location', 'why': 'so the delivery is recorded where it was handed over'},
      'proof': {'type': 'photo', 'why': 'a photo of the parcel at the door, for the shop’s records'},
      'signed': {'type': 'signature', 'why': 'if the customer will sign for it'},
      'note': {'type': 'string', 'title': 'A note for the shop'},
    },
  },
};

// A 1×1 PNG: the photo the stand-in camera takes.
final _png = base64Decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==');

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}

void main() {
  setUpAll(loadFonts);
  late FakePlaces places;
  late int photos;
  setUp(() {
    places = FakePlaces();
    photos = 0;
    final wasPlace = formPlaceFinder;
    final wasPhoto = takeFormPhoto;
    formPlaceFinder = places;
    takeFormPhoto = () async {
      photos++;
      return (bytes: _png, name: 'photo.jpg');
    };
    addTearDown(() {
      formPlaceFinder = wasPlace;
      takeFormPhoto = wasPhoto;
    });
  });

  /// The screen, its request answered; what it sent, and the auth.
  Future<({List<String> sent, Auth auth})> open(WidgetTester t, {bool Function()? online, Size size = const Size(390, 1400)}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = size;
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
            'submission': {'id': 's1', 'reference': 'F-1', 'status': 'answered', 'channel': 'app', 'answers': {}, 'respondent': {'kind': 'linked'}, 'created_at': '2026-11-12T12:00:00Z'},
          }, 201);
        }
        return null;
      },
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: FormRequestScreen(auth: auth, id: _rid, now: () => DateTime.utc(2026, 11, 12, 12))));
    await settle(t);
    return (sent: sent, auth: auth);
  }

  testWidgets('a rider proves a delivery: the location at the tap only, the camera, sent; pictured', (t) async {
    final o = await open(t);
    expect(find.text('Delivered'), findsWidgets);
    expect(find.textContaining('Ada Stores'), findsWidgets);
    expect(find.textContaining('Why: so the delivery is recorded'), findsOneWidget);
    // Nothing is asked of the phone before the person taps.
    expect(places.asked, 0);
    expect(photos, 0);
    await looks(t, 'form-request-asked');
    // Sending with nothing answered holds the form.
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.textContaining('Answer Where and Proof'), findsOneWidget);
    expect(o.sent, isEmpty);
    await t.tap(find.text('Use my location'));
    await settle(t);
    expect(places.asked, 1);
    expect(find.text('Read by your phone, to within 12 m'), findsOneWidget);
    await t.tap(find.text('Take a photo'));
    await settle(t);
    expect(photos, 1);
    expect(find.text('Taken with your camera now'), findsOneWidget);
    await looks(t, 'form-request');
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(o.sent, hasLength(1));
    final body = o.sent.single;
    expect(body, startsWith('multipart/form-data'));
    expect(body, contains('name="proof"; filename="photo.jpg"'));
    expect(body, contains('"where":{"lat":6.5244,"lng":3.3792,"accuracy_m":12,'));
    expect(body, contains('"signed":{"state":"unavailable"'));
    expect(body, contains('"app":"app"'));
    expect(body, contains('"online":true'));
    expect(find.text('Sent'), findsOneWidget);
  });

  testWidgets('a location refused for good is an answer, with the way to the settings; Can’t for the photo', (t) async {
    places.problem = PlaceProblem.refusedForever;
    final o = await open(t);
    await t.tap(find.text('Use my location'));
    await settle(t);
    expect(find.text('Your location was not read'), findsOneWidget);
    await t.tap(find.text('Settings'));
    await settle(t);
    expect(places.settings, [PlaceProblem.refusedForever]);
    await t.tap(find.bySemanticsLabel('I can’t answer Proof'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    final body = o.sent.single;
    expect(body, startsWith('application/json'));
    expect(body, contains('"where":{"state":"refused"'));
    expect(body, contains('"proof":{"state":"refused"'));
    expect(photos, 0);
  });

  testWidgets('made with no connection: kept on the phone, sent when back with online false', (t) async {
    final tmp = Directory.systemTemp.createTempSync('forms');
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
    final store = (await t.runAsync(() => LocalStore.open('t_u', MemoryKeyStore())))!;
    o.auth.local = store;
    await t.tap(find.text('Use my location'));
    await settle(t);
    await t.tap(find.text('Take a photo'));
    await settle(t);
    up = false; // a dead spot
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.text('Kept on your phone'), findsOneWidget);
    expect(o.sent, isEmpty);
    expect(store.read(keptFormAnswersKey), isA<List<Object?>>());
    up = true;
    final n = await t.runAsync(() => sendKeptFormAnswers(o.auth));
    expect(n, 1);
    expect(o.sent.single, contains('"online":false'));
    expect(o.sent.single, contains('name="proof"'));
    expect(store.read(keptFormAnswersKey), isNull);
  });
}
