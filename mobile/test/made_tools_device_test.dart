// A members' tool that takes a location, a photo and a signature on the
// app's sheet (build-plan-gaps H3, g-scan-tool): each asked only at the
// tap, after its why, as a form step's are; the photo (the camera, never
// the gallery) and the pad's drawing sent first (POST
// /v1/tools/{command}/files) and named by their ids in the start; the
// location as the phone read it; Can't is an answer.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/form_request.dart';
import 'package:taskiem/chat/made_tools.dart';
import 'package:taskiem/chat/people_place.dart' show PlaceProblem;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'form_request_d2_test.dart' show signPad;
import 'looks.dart';
import 'people_gaps_test.dart' show FakePlaces;
import 'support.dart';

final _visit = {
  'workflow_id': '0193a000-0000-7000-8000-0000000000a1',
  'environment': 'prod',
  'version': 1,
  'command': 'visit',
  'name': 'Add a site visit',
  'about': 'Record a visit to a site',
  'says': <String>[],
  'care': {'level': 'answers'},
  'answer': 'text',
  'always_ask': false,
  'inputs': {
    'type': 'object',
    'required': ['where', 'proof'],
    'properties': {
      'where': {'type': 'location', 'why': 'so the visit is recorded where it was'},
      'proof': {'type': 'photo', 'why': 'a photo of the work'},
      'signed': {'type': 'signature', 'why': 'the site manager signs it off', 'ask_name': true},
      'note': {'type': 'string', 'title': 'Note'},
    },
  },
};

void main() {
  setUpAll(loadFonts);
  late FakePlaces places;
  var photos = 0;
  setUp(() {
    places = FakePlaces();
    photos = 0;
    final wasPlace = formPlaceFinder;
    final wasPhoto = takeFormPhoto;
    formPlaceFinder = places;
    takeFormPhoto = () async {
      photos++;
      return (bytes: [0xff, 0xd8, 0xff, 0xe0, 0, 16, 74, 70], name: 'photo.jpg');
    };
    addTearDown(() {
      formPlaceFinder = wasPlace;
      takeFormPhoto = wasPhoto;
    });
  });

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 400));
  }

  testWidgets('a site visit: the place, a photo and a signature at the tap, the files sent first; pictured', (t) async {
    t.view.physicalSize = const Size(390, 1100);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tools');
    final files = <String>[];
    Map<String, dynamic>? started;
    http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
    final auth = f.auth(
      also: (req) async {
        final p = req.url.path;
        if (req.method == 'POST' && p == '/v1/tools/visit/files') {
          final body = latin1.decode(req.bodyBytes);
          files.add(body);
          final field = RegExp(r'name="(\w+)"; filename').firstMatch(body)?.group(1) ?? 'x';
          return ok({'field': field, 'file': {'id': 'file-$field', 'name': 'f', 'type': 'image/png', 'size': 1, 'sha256': '00'}}, 201);
        }
        if (req.method == 'POST' && p == '/v1/tools/visit/start') {
          started = jsonDecode(req.body) as Map<String, dynamic>;
          return ok({'run_id': 'r1', 'care': 'answers', 'created': true, 'values': [{'name': 'where', 'label': 'where', 'value': 'your location, read now'}]}, 201);
        }
        return null;
      },
    );
    final tool = MadeTool.fromJson(_visit);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: MadeToolSheet(auth: auth, tool: tool)));
    await settle(t);
    // Nothing is asked of the phone before the tap; each says why.
    expect(places.asked, 0);
    expect(photos, 0);
    expect(find.text('Why: so the visit is recorded where it was'), findsOneWidget);
    expect(find.text('Why: a photo of the work'), findsOneWidget);
    expect(find.text('Use my location'), findsOneWidget);
    await looks(t, 'made-tool-devices');

    await t.tap(find.text('Use my location'));
    await settle(t);
    expect(places.asked, 1);
    expect(find.text('Read by your phone, to within 12 m'), findsOneWidget);
    await t.tap(find.text('Take a photo'));
    await settle(t);
    expect(find.text('Taken with your camera now'), findsOneWidget);
    await t.tap(find.text('Sign on the screen'));
    await settle(t);
    await signPad(t);
    await t.enterText(find.byType(EditableText).first, 'Emeka Obi');
    await settle(t);
    await t.tap(find.text('Use this signature'));
    await settle(t);
    expect(find.text('Signed by Emeka Obi'), findsOneWidget);
    await looks(t, 'made-tool-devices-answered');

    await t.tap(find.text('Run it'));
    await settle(t);
    expect(files, hasLength(2));
    expect(files.join(), contains('name="proof"; filename="photo.jpg"'));
    expect(files.join(), contains('name="signed"; filename="signature.png"'));
    expect(files.join(), contains('name="app"'));
    final input = started!['input'] as Map<String, dynamic>;
    expect(input['proof'], {'file': 'file-proof'});
    expect(input['signed'], {'file': 'file-signed', 'signed_by': 'Emeka Obi'});
    expect(input['where'], containsPair('lat', 6.5244));
    expect(input['where'], containsPair('accuracy_m', 12));
  });

  testWidgets('the phone not allowed to read the place: a refusal is the answer; Can\'t is too', (t) async {
    t.view.physicalSize = const Size(390, 1100);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tools');
    Map<String, dynamic>? started;
    final auth = f.auth(
      also: (req) async {
        if (req.method == 'POST' && req.url.path == '/v1/tools/visit/start') {
          started = jsonDecode(req.body) as Map<String, dynamic>;
          return http.Response(jsonEncode({'run_id': 'r2', 'care': 'answers', 'created': true, 'values': <Object>[]}), 201, headers: {'content-type': 'application/json'});
        }
        return null;
      },
    );
    places.problem = PlaceProblem.refused;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: MadeToolSheet(auth: auth, tool: MadeTool.fromJson(_visit))));
    await settle(t);
    await t.tap(find.text('Use my location'));
    await settle(t);
    expect(find.text('You said you can’t answer this'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('I can’t answer Proof'));
    await settle(t);
    await t.tap(find.text('Run it'));
    await settle(t);
    final input = started!['input'] as Map<String, dynamic>;
    expect(input['where'], {'state': 'refused', 'reason': 'the phone was not allowed to read where I am'});
    expect(input['proof'], {'state': 'refused', 'reason': 'I cannot answer this here'});
    expect(input.containsKey('signed'), isFalse);
  });
}
