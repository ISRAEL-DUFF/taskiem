// The second stage 5a review's app findings (docs/spec/people-chatting.md,
// "As built: stage 5a gaps, app", "Second review fixes (app)"): a live
// change to an old message no longer breaks Load older, the camera refused
// is said plainly everywhere, the live connection reconnects at once when
// the server ends it cleanly and forgets presence when it closes, the daily
// contacts re-check goes round the numbers, it can be turned off, covered
// screens read once when back on top, search pages its own query, typing
// stops when it should, and the standing notice reads again.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/attachments.dart' show PickedFile;
import 'package:taskiem/chat/composer.dart' show Pickers;
import 'package:taskiem/chat/people_chat.dart';
import 'package:taskiem/chat/people_live.dart';
import 'package:taskiem/chat/pick_problem.dart';
import 'package:taskiem/chat/product_form.dart' show PhotoAdd;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'people_test.dart';
import 'support.dart' show loadFonts;

Map<String, dynamic> msg(String id, {bool mine = false, String body = 'Hello', String at = '2026-11-17T10:25:00Z', Map<String, dynamic> extra = const {}}) => {
  'id': id, 'mine': mine, 'kind': 'text', 'body': body, 'read_at': null, 'created_at': at, if (mine) 'status': 'sent', ...extra,
};

void hear(PeopleLive live, String event, Object data, {String? id}) => live.hearEvent((event: event, data: jsonEncode(data), id: id));

/// The phone refusing the camera and photos (as image_picker throws when
/// CAMERA is declared and the person says no), or with no camera.
class RefusedPickers extends Pickers {
  const RefusedPickers({this.code = 'camera_access_denied'});
  final String code;
  @override
  Future<PickedFile?> camera({double? side}) async => throw PlatformException(code: code);
  @override
  Future<PickedFile?> photo({double? side}) async => throw PlatformException(code: 'photo_access_denied');
  @override
  Future<List<PickedFile>> photos(int limit, {double? side}) async => throw PlatformException(code: 'photo_access_denied');
}

/// How often the phone's settings were opened.
class SettingsSpy {
  int opened = 0;
}

SettingsSpy spySettings() {
  final s = SettingsSpy();
  final was = openPhoneSettings;
  openPhoneSettings = () async => s.opened++;
  addTearDown(() => openPhoneSettings = was);
  return s;
}

const cameraRefused = 'Taskiem isn’t allowed to use your camera. To take a photo, allow Camera for Taskiem in your phone’s settings.';
const photosRefused = 'Taskiem isn’t allowed to see your photos. To choose one, allow Photos for Taskiem in your phone’s settings.';

void main() {
  setUpAll(loadFonts);

  group('1. a live change to an old message', () {
    test('is not merged when older than what is loaded; Load older pages from the oldest loaded; new and loaded ones still merge', () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      final api = Api();
      api.routes['GET /v1/me/people/pc1'] = (_) => peopleChat();
      api.routes['POST /v1/me/people/pc1/read'] = (_) => answer({}, 204);
      api.routes['GET /v1/me/people/pc1/messages'] = (r) {
        if (r.url.queryParameters['before'] == 'm1') return {'messages': [msg('m0', at: '2026-11-17T08:00:00Z')], 'more': true};
        return {'messages': thread(), 'more': true};
      };
      final live = PeopleLive(api.auth().transport);
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, live: live);
      d.seen = () => false;
      // Heard before the first page: an old one is dropped, a new one kept.
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('ancient', at: '2026-11-01T08:00:00Z')});
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('early', at: '2026-11-17T10:26:00Z')});
      await pumpEventQueue();
      await d.open();
      expect(d.messages!.map((m) => m.id), ['m1', 'm2', 'm3', 'm4', 'm5', 'early']);

      // A reaction on a message far back (a replay after a reconnect too).
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('old7', at: '2026-11-10T08:00:00Z', extra: {'their_reaction': '👍'})});
      await pumpEventQueue();
      expect(d.messages!.any((m) => m.id == 'old7'), isFalse);
      // One loaded changes in place; one new comes in.
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': {...thread()[1], 'their_reaction': '👍'}});
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('m9', at: '2026-11-17T10:40:00Z')});
      await pumpEventQueue();
      expect(d.messages!.firstWhere((m) => m.id == 'm2').m.theirReaction, '👍');
      expect(d.messages!.last.id, 'm9');

      await d.refresh();
      expect(d.messages!.any((m) => m.id == 'old7'), isFalse, reason: 'not kept by a read of the latest page');
      await d.older();
      final befores = [for (final r in api.sent('GET', '/v1/me/people/pc1/messages')) r.url.queryParameters['before']].whereType<String>();
      expect(befores, ['m1'], reason: 'paged from the oldest loaded, not the old one heard live');
      d.dispose();
      live.dispose();
    });

    test('with no older pages, any message heard is merged (the loaded ones are the whole chat)', () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      final api = adaChat();
      final live = PeopleLive(api.auth().transport);
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, live: live);
      d.seen = () => false;
      await d.open();
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('m0', at: '2026-11-17T08:00:00Z')});
      await pumpEventQueue();
      expect(d.messages!.first.id, 'm0');
      d.dispose();
      live.dispose();
    });
  });

  group('2. the camera or photos refused', () {
    test('what the picker throws, in plain words', () {
      expect(pickProblemOf(PlatformException(code: 'camera_access_denied')), PickProblem.refused);
      expect(pickProblemOf(PlatformException(code: 'photo_access_denied')), PickProblem.refused);
      expect(pickProblemOf(PlatformException(code: 'no_available_camera')), PickProblem.noCamera);
      expect(pickProblemOf(StateError('x')), PickProblem.failed);
      expect(pickProblemWords(PickProblem.refused), cameraRefused);
      expect(pickProblemWords(PickProblem.refused, camera: false), photosRefused);
    });

    testWidgets('a people chat: the camera button and + A photo say why, with Open settings; nothing sent', (t) async {
      phone(t);
      final spy = spySettings();
      final api = adaChat();
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d, pickers: const RefusedPickers())));
      await settle(t);
      await press(t, find.bySemanticsLabel('Take a photo'));
      expect(find.text(cameraRefused), findsOneWidget);
      await t.tap(find.text('Open settings'));
      await t.pump();
      expect(spy.opened, 1);
      expect(find.text(cameraRefused), findsNothing);

      await press(t, find.bySemanticsLabel('Chat tools'));
      await press(t, find.text('A photo'));
      expect(find.text(photosRefused), findsOneWidget);
      expect(api.calls.where((c) => c.method == 'POST' && c.url.path.endsWith('/files')), isEmpty);
      await t.tap(find.bySemanticsLabel('Dismiss'));
      await drain(t);
    });

    testWidgets('no camera on the phone: said, without settings', (t) async {
      phone(t);
      spySettings();
      final api = adaChat();
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d, pickers: const RefusedPickers(code: 'no_available_camera'))));
      await settle(t);
      await press(t, find.bySemanticsLabel('Take a photo'));
      expect(find.text(pickProblemWords(PickProblem.noCamera)), findsOneWidget);
      expect(find.text('Open settings'), findsNothing);
      await t.tap(find.bySemanticsLabel('Dismiss'));
      await drain(t);
    });

    testWidgets('a product’s photos: Take a photo refused says why, with Open settings', (t) async {
      phone(t);
      final spy = spySettings();
      var picked = 0;
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: Center(child: PhotoAdd(pickers: const RefusedPickers(), room: 3, onPick: (_) => picked++))));
      await t.pumpAndSettle();
      await t.tap(find.bySemanticsLabel('Take a photo'));
      await settle(t);
      expect(find.text(cameraRefused), findsOneWidget);
      await t.tap(find.text('Open settings'));
      await t.pump();
      expect(spy.opened, 1);
      await t.tap(find.bySemanticsLabel('From your phone'));
      await settle(t);
      expect(find.text(photosRefused), findsOneWidget);
      expect(picked, 0);
      await t.tap(find.bySemanticsLabel('Dismiss'));
      await drain(t);
    });
  });
}
