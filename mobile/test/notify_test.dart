// Notifications on this phone (docs/spec/mobile-app-m4.md N4), with a
// stand-in for Firebase and the server: turning them on and off, the kinds,
// a token Firebase changes, signing out, a tap in another business, and the
// Settings card.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/notify.dart';
import 'package:taskiem/app/route.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/signed_in.dart';
import 'package:taskiem/theme/theme.dart';

import 'auth_test.dart' show FakeServer;
import 'support.dart' show loadFonts;

class FakeFirebase implements PushPlatform {
  bool startsOk = true;
  PushPermission allowed = PushPermission.granted;
  PushPermission now = PushPermission.notAsked;
  String? current = 'fGx1:APA91bH_phone-one-aaaaaaaaaaaaa';
  bool forgot = false;
  bool unkept = false;
  AppConfigPushFcm? startedWith;
  Map<String, String>? initial;
  final tokens = StreamController<String>.broadcast();
  final taps = StreamController<Map<String, String>>.broadcast();
  final notes = StreamController<PushNote>.broadcast();

  @override
  String get platform => 'android';
  @override
  Future<bool> start(AppConfigPushFcm v) async {
    startedWith = v;
    return startsOk;
  }

  @override
  Future<PushPermission> permission() async => now;
  @override
  Future<PushPermission> ask() async => now = allowed;
  @override
  Future<String?> token() async => current;
  @override
  Stream<String> get tokenChanged => tokens.stream;
  @override
  Future<Map<String, String>?> openedFrom() async => initial;
  @override
  Stream<Map<String, String>> get opened => taps.stream;
  @override
  Stream<PushNote> get arrived => notes.stream;
  @override
  Future<void> forget() async => forgot = true;
  @override
  Future<void> unkeep() async => unkept = true;
  bool cleared = false;
  @override
  Future<void> clearShown() async => cleared = true;
}

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

const _fcm = {'api_key': 'AIzaTest', 'app_id_android': '1:42:android:abc', 'project_id': 'taskiem-test', 'sender_id': '42'};

/// A server that keeps devices as the real one does, enough for the app:
/// the business's devices, and the person's own (You's: a personal session,
/// or `?side=you`), apart.
class PushServer extends FakeServer {
  PushServer({this.firebase = true, this.personal = false}) {
    answers['GET /v1/app/config'] = () => _json({'min_version': '0.1.0', 'passkeys': false, 'signup': false, if (firebase) 'push': {'fcm': _fcm}});
    answers['GET /v1/me'] = () => personal
        ? _json({'personal': true, 'auth_method': 'phone', 'permissions': <String>[], 'roles': <String>[]})
        : _json({'tenant_id': _tenant, 'tenant_name': 'Ada Foods', 'auth_method': 'password', 'permissions': ['chat.trade', 'order.handle'], 'roles': ['owner']});
  }

  final bool firebase;
  final bool personal;
  final String _tenant = 't1';
  int _n = 0;

  /// The business's devices.
  final devices = <Map<String, dynamic>>[];

  /// The person's own devices (You's).
  final youDevices = <Map<String, dynamic>>[];

  /// The side each push request was for.
  final sides = <String>[];

  List<Map<String, dynamic>> listOf(String side) => side == 'you' ? youDevices : devices;

  Map<String, dynamic> view(String side) => {
    'devices': [for (final d in listOf(side)) Map.of(d)..remove('token')],
    'max': 10, 'kinds': side == 'you' ? ['you'] : pushKinds, 'browsers': true, 'phones': firebase, 'side': side,
  };

  /// /v1/me/push, by side.
  http.Response push(String method, String side, Map<String, dynamic>? b) {
    final list = listOf(side);
    switch (method) {
      case 'GET':
        return _json(view(side));
      case 'PUT':
        final token = b!['token'] as String;
        final old = b['replaces'] as String?;
        var d = list.where((d) => d['token'] == token).firstOrNull;
        if (b['known'] == true && d == null) return _json({'error': 'not found'}, 404);
        final was = old == null ? null : list.where((d) => d['token'] == old).firstOrNull;
        if (was != null) list.remove(was);
        if (d == null) {
          d = {
            'id': 'd${++_n}', 'token': token, 'kind': 'fcm', 'platform': 'android',
            'label': (b['label'] as String?) ?? (was?['label'] ?? 'A phone'),
            'preview': was?['preview'] ?? true, 'kinds': was?['kinds'] ?? (b['kinds'] ?? (side == 'you' ? ['you'] : List.of(pushKinds))),
            'created_at': '2026-11-14T10:00:00Z',
          };
          list.add(d);
        }
        return _json({...view(side), 'added': d['id']});
      case 'DELETE':
        list.removeWhere((d) => d['token'] == b!['token']);
        return _json(view(side));
    }
    return _json({'error': 'no'}, 405);
  }

  @override
  http.Client client() {
    final inner = super.client();
    return _Patch(inner, this);
  }
}

/// The push requests, by side; PATCH and DELETE of one device, by id.
class _Patch extends http.BaseClient {
  _Patch(this.inner, this.s);
  final http.Client inner;
  final PushServer s;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest req) async {
    final path = req.url.path;
    if (path != '/v1/me/push' && !RegExp(r'^/v1/me/push/d\d+$').hasMatch(path)) return inner.send(req);
    final body = req is http.Request && req.body.isNotEmpty ? jsonDecode(req.body) as Map<String, dynamic> : null;
    s.calls.add((method: req.method, path: path, body: body, auth: req.headers['Authorization']));
    final side = req.url.queryParameters['side'] ?? (s.personal ? 'you' : 'business');
    s.sides.add(side);
    late final http.Response r;
    if (path == '/v1/me/push') {
      r = s.push(req.method, side, body);
    } else {
      final list = s.listOf(side);
      final d = list.firstWhere((d) => d['id'] == path.split('/').last);
      if (req.method == 'DELETE') list.remove(d);
      if (req.method == 'PATCH') d.addAll({for (final k in ['kinds', 'preview']) if (body!.containsKey(k)) k: body[k]});
      r = _json(s.view(side));
    }
    return http.StreamedResponse(Stream.value(r.bodyBytes), r.statusCode, headers: r.headers);
  }
}

Future<(Auth, PhoneNotifications, FakeFirebase, MemoryKeyStore)> signedIn(PushServer server, {FakeFirebase? fb}) async {
  final keys = MemoryKeyStore();
  final auth = Auth(
    transport: Transport(base: 'http://localhost', client: server.client()),
    store: SessionStore(keys, describe: () async => (name: 'Ada’s Tecno', platform: 'android')),
  );
  server.answers['POST /v1/auth/login'] = () => _json({'token': 'tok-1'});
  await auth.signIn('ada@example.com', 'x');
  final firebase = fb ?? FakeFirebase();
  final n = PhoneNotifications(auth: auth, keys: keys, platform: firebase);
  auth.beforeSignOut = n.signingOut;
  auth.onForget = n.forgotten;
  return (auth, n, firebase, keys);
}

void main() {
  test('without Firebase on the server there is nothing to turn on, and nothing is asked', () async {
    final (_, n, fb, _) = await signedIn(PushServer(firebase: false));
    await n.start();
    expect(n.available, isFalse);
    expect(fb.startedWith, isNull);
    expect(fb.unkept, isTrue);
    expect(await n.shouldAsk(), isFalse);
  });

  test('a phone without Google services says so', () async {
    final (_, n, _, _) = await signedIn(PushServer(), fb: FakeFirebase()..startsOk = false);
    await n.start();
    expect(n.available, isFalse);
    expect(n.noGoogle, isTrue);
  });

  test('on: asked once, the phone registered with its name; the kinds and the words kept; off takes it away', () async {
    final server = PushServer();
    final (_, n, fb, keys) = await signedIn(server);
    await n.start();
    expect(fb.startedWith?.projectId, 'taskiem-test');
    expect(await n.shouldAsk(), isTrue);
    await n.turnOn();
    expect(n.error, isNull);
    expect(n.isOn, isTrue);
    final put = server.calls.lastWhere((c) => c.method == 'PUT');
    expect(put.body, containsPair('kind', 'fcm'));
    expect(put.body, containsPair('platform', 'android'));
    expect(put.body, containsPair('label', 'Ada’s Tecno'));
    expect(n.mine?.label, 'Ada’s Tecno');
    expect(await n.shouldAsk(), isFalse);
    expect(keys.values['push-on:t1'], '1');

    await n.setKind('alerts', false);
    expect(n.mine?.kinds, ['business', 'customers', 'orders', 'approvals']);
    await n.setPreview(false);
    expect(n.mine?.preview, isFalse);

    await n.turnOff();
    expect(n.isOn, isFalse);
    expect(server.devices, isEmpty);
    expect(keys.values['push-on:t1'], isNull);
  });

  test('refused in the phone’s settings: said how to allow it, nothing registered', () async {
    final server = PushServer();
    final (_, n, _, _) = await signedIn(server, fb: FakeFirebase()..allowed = PushPermission.denied);
    await n.start();
    await n.turnOn();
    expect(n.isOn, isFalse);
    expect(n.blocked, isTrue);
    expect(n.error, contains('this phone’s settings'));
    expect(server.devices, isEmpty);
  });

  test('a new token from Firebase replaces the old one, keeping its choices', () async {
    final server = PushServer();
    final (_, n, fb, _) = await signedIn(server);
    await n.start();
    await n.turnOn();
    await n.setKind('orders', false);
    fb.tokens.add('fGx1:APA91bH_phone-two-aaaaaaaaaaaaa');
    await pumpEventQueue();
    expect(server.devices, hasLength(1));
    expect(server.devices.single['token'], 'fGx1:APA91bH_phone-two-aaaaaaaaaaaaa');
    expect(server.calls.lastWhere((c) => c.method == 'PUT').body!['replaces'], 'fGx1:APA91bH_phone-one-aaaaaaaaaaaaa');
    expect(n.mine?.kinds, isNot(contains('orders')));
  });

  test('opened again: found by its token, never added back once removed from another device', () async {
    final server = PushServer();
    final (auth, n, fb, keys) = await signedIn(server);
    await n.start();
    await n.turnOn();
    // The app opened again later: the same keys, a new session of the app.
    final again = PhoneNotifications(auth: auth, keys: keys, platform: fb);
    await again.start();
    expect(again.isOn, isTrue);
    expect(server.calls.lastWhere((c) => c.method == 'PUT').body!['known'], isTrue);
    expect(server.devices, hasLength(1));
    // Removed from the web meanwhile.
    server.devices.clear();
    final third = PhoneNotifications(auth: auth, keys: keys, platform: fb);
    await third.start();
    expect(third.isOn, isFalse);
    expect(server.devices, isEmpty);
  });

  test('signing out takes the phone off before the session ends, and forgets the token', () async {
    final server = PushServer();
    server.answers['POST /v1/auth/logout'] = () => http.Response('', 204);
    final (auth, n, fb, keys) = await signedIn(server);
    await n.start();
    await n.turnOn();
    await auth.signOut();
    final del = server.calls.indexWhere((c) => c.method == 'DELETE' && c.path == '/v1/me/push');
    final out = server.calls.indexWhere((c) => c.path == '/v1/auth/logout');
    expect(del, greaterThanOrEqualTo(0));
    expect(del, lessThan(out));
    expect(server.calls[del].auth, 'Bearer tok-1');
    expect(fb.forgot, isTrue);
    expect(keys.values.keys.where((k) => k.startsWith('push-')), isEmpty);
  });

  test('a tap opens its page; one for another business switches to it first', () async {
    final server = PushServer();
    const other = '0192f6c4-8a9e-7b3c-9d2e-112233445566';
    server.answers['POST /v1/me/switch'] = () {
      // Now in the other business, as the real server's me would say.
      server.answers['GET /v1/me'] = () => _json({'tenant_id': other, 'auth_method': 'password', 'permissions': <String>[], 'roles': ['owner']});
      return _json({'token': 'tok-2'});
    };
    final (auth, n, fb, _) = await signedIn(server);
    await n.start();
    fb.taps.add({'link': '/chat?with=abc', 'business': 't1'});
    await pumpEventQueue();
    expect(n.takePending(), '/chat?with=abc');
    // Another site is never opened: the chats instead.
    fb.taps.add({'link': 'https://evil.example'});
    await pumpEventQueue();
    expect(n.takePending(), '/chat');

    final session = auth.session;
    fb.taps.add({'link': '/approvals', 'business': other});
    await pumpEventQueue();
    expect(server.calls.lastWhere((c) => c.path == '/v1/me/switch').body, {'tenant_id': other});
    expect(auth.session, greaterThan(session));
    expect(n.takePending(), '/approvals');
  });

  test('a tap that opened the app from closed, for another business: switched first, opened only there', () async {
    const other = '0192f6c4-8a9e-7b3c-9d2e-112233445566';
    final server = PushServer();
    server.answers['POST /v1/me/switch'] = () {
      server.answers['GET /v1/me'] = () => _json({'tenant_id': other, 'auth_method': 'password', 'permissions': <String>[], 'roles': ['owner']});
      return _json({'token': 'tok-2'});
    };
    final fb = FakeFirebase()..initial = {'link': '/bills/b1', 'business': other};
    final (auth, n, _, _) = await signedIn(server, fb: fb);
    final session = auth.session;
    var told = 0;
    n.addListener(() => told++);
    await n.start();
    expect(auth.session, greaterThan(session));
    // Not told while the switch ran (the old home would have opened it in
    // the old business); the new business's home takes it.
    expect(told, 0);
    expect(n.takePending(), '/bills/b1');
    // A switch that fails is said once.
    server.answers['POST /v1/me/switch'] = () => _json({'error': 'no'}, 404);
    await n.open({'link': '/approvals', 'business': '0192f6c4-8a9e-7b3c-9d2e-000000000003'});
    expect(n.takeProblem(), contains('another of your businesses'));
    expect(n.takeProblem(), isNull);
  });

  test('a toast for another business is said even with its tag on the screen', () async {
    final (_, n, fb, _) = await signedIn(PushServer());
    await n.start();
    final said = <String>[];
    n.onArrived = (note) => said.add(note.title);
    n.showing = 'abc';
    fb.notes.add(const PushNote(title: 'Other', body: '', data: {'tag': 'abc', 'business': '0192f6c4-8a9e-7b3c-9d2e-112233445566'}));
    await pumpEventQueue();
    expect(said, ['Other']);
  });

  test('a notification for the conversation on the screen shows nothing; another is said', () async {
    final (_, n, fb, _) = await signedIn(PushServer());
    await n.start();
    final said = <String>[];
    n.onArrived = (note) => said.add(note.title);
    n.showing = 'abc';
    fb.notes.add(const PushNote(title: 'Dangote', body: 'Rice', data: {'tag': 'abc', 'link': '/chat?with=abc'}));
    fb.notes.add(const PushNote(title: 'Cherry', body: 'Hi', data: {'tag': 'def', 'link': '/chat?with=def'}));
    await pumpEventQueue();
    expect(said, ['Cherry']);
  });

  test('Android’s "Don’t allow" (it may ask again): nothing is turned on, and it is said', () async {
    final server = PushServer();
    final (_, n, _, _) = await signedIn(server, fb: FakeFirebase()..allowed = PushPermission.notAsked);
    await n.start();
    await n.turnOn();
    expect(n.isOn, isFalse);
    expect(n.blocked, isFalse);
    expect(n.error, contains('choose Allow'));
    expect(server.devices, isEmpty);
  });

  test('two starts at once are one: the second waits for the first', () async {
    final server = PushServer();
    final (_, n, _, _) = await signedIn(server);
    await Future.wait([n.start(), n.start()]);
    expect(n.available, isTrue);
    expect(server.calls.where((c) => c.path == '/v1/app/config'), hasLength(1));
    expect(await n.shouldAsk(), isTrue);
  });

  test('a switch that lands in another business says so, and nothing waits', () async {
    const other = '0192f6c4-8a9e-7b3c-9d2e-112233445566';
    final server = PushServer();
    server.answers['POST /v1/me/switch'] = () => _json({'token': 'tok-2'});
    final (_, n, _, _) = await signedIn(server);
    await n.start();
    await n.open({'link': '/approvals', 'business': other});
    expect(n.takePending(), isNull);
    expect(n.takeProblem(), contains('could not open'));
  });

  test('signing out takes what was shown off the phone, and a tap after it opens nothing', () async {
    final server = PushServer();
    server.answers['POST /v1/auth/logout'] = () => http.Response('', 204);
    final (auth, n, fb, _) = await signedIn(server);
    await n.start();
    await auth.signOut();
    expect(fb.cleared, isTrue);
    fb.taps.add({'link': '/chat?with=abc'});
    await pumpEventQueue();
    expect(n.takePending(), isNull);
  });

  test('the conversation on the screen is the newest screen’s: an older one closing leaves it', () async {
    final (_, n, _, _) = await signedIn(PushServer());
    final older = Object(), newer = Object();
    n.show('abc', older);
    n.show('abc', newer);
    n.unshow(older);
    expect(n.showing, 'abc');
    n.unshow(newer);
    expect(n.showing, isNull);
  });

  test('only paths of the app are opened', () {
    expect(appPath('/chat?with=c-12'), isTrue);
    expect(appPath('/bills/abc'), isTrue);
    for (final bad in ['//evil.example', 'https://evil.example', '/a/../b', null, '']) {
      expect(appPath(bad), isFalse, reason: '$bad');
    }
  });

  group('push on You (taskiem-for-everyone §9)', () {
    test('a personal session: the phone is You\'s device, kinds ["you"], no side asked; signing out takes it off', () async {
      final server = PushServer(personal: true);
      server.answers['POST /v1/auth/logout'] = () => http.Response('', 204);
      final (auth, n, fb, keys) = await signedIn(server);
      await n.start();
      expect(n.personal, isTrue);
      expect(await n.shouldAsk(), isTrue);
      await n.turnOn();
      expect(n.error, isNull);
      expect(n.isOn && n.youIsOn, isTrue);
      final put = server.calls.lastWhere((c) => c.method == 'PUT');
      expect(put.body, containsPair('kind', 'fcm'));
      expect(put.body, containsPair('kinds', ['you']));
      expect(server.sides.where((x) => x != 'you'), isEmpty, reason: 'a personal session is You\'s side');
      expect(server.youDevices, hasLength(1));
      expect(server.devices, isEmpty);
      expect(keys.values['push-on:you'], '1');
      expect(n.youMine?.kinds, ['you']);

      await auth.signOut();
      expect(server.youDevices, isEmpty);
      expect(fb.forgot, isTrue);
      expect(keys.values.keys.where((k) => k.startsWith('push-')), isEmpty);
    });

    test('a business session registers You too (?side=you), turned off apart; signing out takes both off', () async {
      final server = PushServer();
      server.answers['POST /v1/auth/logout'] = () => http.Response('', 204);
      final (auth, n, _, keys) = await signedIn(server);
      await n.start();
      await n.turnOn();
      expect(server.devices, hasLength(1));
      expect(server.youDevices, hasLength(1), reason: 'customer chats reach the person while they work in a business');
      expect(server.youDevices.single['kinds'], ['you']);
      expect(n.youOn, isTrue);
      expect(keys.values['push-on:you'], '1');

      // Off for You here: the business's stay.
      await n.setYou(false);
      expect(server.youDevices, isEmpty);
      expect(server.devices, hasLength(1));
      expect(n.youOn, isFalse);
      // Turned on again with the business's, You's stays off as chosen.
      await n.turnOff();
      await n.turnOn();
      expect(server.youDevices, isEmpty);
      await n.setYou(true);
      expect(server.youDevices, hasLength(1));

      // Opened again later: You's found by its token, from the business.
      final again = PhoneNotifications(auth: auth, keys: keys, platform: FakeFirebase());
      await again.start();
      expect(again.youOn, isTrue);
      final known = server.calls.lastWhere((c) => c.method == 'PUT');
      expect(known.body!['known'], isTrue);
      expect(server.sides.last, 'you');

      await auth.signOut();
      expect(server.devices, isEmpty);
      expect(server.youDevices, isEmpty);
    });

    test('a push for You (no business) opens /you/chats/{id} in any session; its chat on the screen shows nothing', () async {
      final (_, n, fb, _) = await signedIn(PushServer());
      await n.start();
      fb.taps.add({'link': '/you/chats/c1', 'tag': 'you:c1', 'kind': 'you'});
      await pumpEventQueue();
      final page = n.takePending();
      expect(page, '/you/chats/c1');
      expect(youChatOf(page), 'c1');
      expect(youChatOf('/chat'), isNull);
      expect(appPath('/you/chats/c1'), isTrue);

      final said = <String>[];
      n.onArrived = (note) => said.add(note.title);
      n.showing = 'you:c1';
      fb.notes.add(const PushNote(title: 'Mama’s Kitchen', body: 'Yes', data: {'tag': 'you:c1', 'link': '/you/chats/c1', 'business': ''}));
      fb.notes.add(const PushNote(title: 'Bola Stores', body: 'Hi', data: {'tag': 'you:c2', 'link': '/you/chats/c2'}));
      await pumpEventQueue();
      expect(said, ['Bola Stores']);
    });
  });

  test('pushes of chats with people: kind person opens the chat, kind request Requests and the request; on the screen, none shows', () async {
    final (_, n, fb, _) = await signedIn(PushServer());
    await n.start();
    fb.taps.add({'link': '/you/people/c9', 'tag': 'person:c9', 'kind': 'person'});
    await pumpEventQueue();
    final person = n.takePending();
    expect(person, '/you/people/c9');
    expect(appPath(person), isTrue);
    expect(youChatOf(person), isNull, reason: 'not a chat with a business');
    expect(youRouteOf(person!), (page: YouPage.person, id: 'c9', ref: '', tool: null));
    expect(routeOf(person).screen, RouteScreen.youPage);

    fb.taps.add({'link': '/you/people/requests/c8', 'tag': 'person:c8', 'kind': 'request'});
    await pumpEventQueue();
    final request = n.takePending();
    expect(request, '/you/people/requests/c8');
    expect(youRouteOf(request!), (page: YouPage.request, id: 'c8', ref: '', tool: null));

    final said = <String>[];
    n.onArrived = (note) => said.add(note.title);
    n.showing = 'person:c9';
    fb.notes.add(const PushNote(title: 'Ada Obi', body: 'Hi', data: {'tag': 'person:c9', 'link': '/you/people/c9', 'kind': 'person'}));
    fb.notes.add(const PushNote(title: 'Chidi', body: 'Message request: hello', data: {'tag': 'person:c8', 'link': '/you/people/requests/c8', 'kind': 'request'}));
    await pumpEventQueue();
    expect(said, ['Chidi'], reason: 'the chat on the screen shows nothing');
  });

  group('Settings', () {
    setUpAll(loadFonts);

    testWidgets('Notifications on this phone: the switch, then the words and the kinds', (t) async {
      t.view.physicalSize = const Size(390, 2400);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final server = PushServer();
      late PhoneNotifications n;
      await t.runAsync(() async {
        final (_, nn, _, _) = await signedIn(server);
        n = nn;
        await n.start();
      });
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ListView(children: [NotificationsCard(notify: n)])));
      expect(find.text('Notifications on this phone'), findsOneWidget);
      expect(find.text('Show what the message says'), findsNothing);
      await t.runAsync(n.turnOn);
      await t.pump();
      expect(find.text('Show what the message says'), findsOneWidget);
      expect(find.text('What this phone tells you'), findsOneWidget);
      for (final k in pushKinds) {
        expect(find.text(kindWords[k]!.$1), findsOneWidget);
      }
    });
  });
}
