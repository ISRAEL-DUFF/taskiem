import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';

/// A stand-in server: answers by method and path, and keeps what it was sent.
class FakeServer {
  final calls = <({String method, String path, Map<String, dynamic>? body, String? auth})>[];
  final answers = <String, http.Response Function()>{};
  bool offline = false;

  http.Client client() => MockClient((req) async {
        if (offline) throw http.ClientException('no network');
        calls.add((
          method: req.method,
          path: req.url.path,
          body: req.body.isEmpty ? null : jsonDecode(req.body) as Map<String, dynamic>,
          auth: req.headers['Authorization'],
        ));
        final a = answers['${req.method} ${req.url.path}'];
        return a == null ? http.Response(jsonEncode({'error': 'not found'}), 404) : a();
      });
}

http.Response json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status);

const me = {'tenant_id': 't1', 'tenant_name': 'Ada Foods', 'auth_method': 'password', 'permissions': <String>[], 'roles': ['owner']};

void main() {
  late FakeServer server;
  late MemoryKeyStore keys;
  Auth auth() => Auth(
        transport: Transport(base: 'http://localhost', client: server.client()),
        store: SessionStore(keys, describe: () async => (name: 'Ada\'s Tecno', platform: 'android')),
      );

  setUp(() {
    server = FakeServer();
    keys = MemoryKeyStore();
    server.answers['GET /v1/me'] = () => json(me);
  });

  test('signing in asks for a session for this phone, and keeps it', () async {
    server.answers['POST /v1/auth/login'] = () => json({'token': 'tok-1', 'tenant_id': 't1', 'user_id': 'u1'});
    final a = auth();
    await a.signIn('ada@example.com', 'correct horse battery');
    final login = server.calls.firstWhere((c) => c.path == '/v1/auth/login');
    expect(login.body!['bearer'], isTrue);
    final device = login.body!['device'] as Map<String, dynamic>;
    expect(device['name'], 'Ada\'s Tecno');
    expect(device['platform'], 'android');
    expect(device['id'], matches(RegExp(r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')));
    expect(keys.values['token'], 'tok-1');
    expect(a.state, AuthState.signedIn);
    expect(a.me!.tenantName, 'Ada Foods');
    expect(server.calls.last.auth, 'Bearer tok-1');
  });

  test('the same install names the same phone every time', () async {
    server.answers['POST /v1/auth/login'] = () => json({'token': 'tok-1'});
    await auth().signIn('ada@example.com', 'x');
    await auth().signIn('ada@example.com', 'x');
    final ids = server.calls.where((c) => c.path == '/v1/auth/login').map((c) => (c.body!['device'] as Map)['id']).toSet();
    expect(ids.length, 1);
  });

  test('a wrong password is the server\'s words, and nothing is kept', () async {
    server.answers['POST /v1/auth/login'] = () => json({'error': 'invalid email or password'}, 401);
    final a = auth();
    await expectLater(a.signIn('ada@example.com', 'nope'), throwsA(isA<ApiError>().having((e) => e.message, 'message', 'invalid email or password')));
    expect(keys.values['token'], isNull);
  });

  test('the code: asked for, then signed in with, for this phone', () async {
    server.answers['POST /v1/auth/chat/start'] = () => json({'status': 'If this number is on a Taskiem account, we have sent a code.', 'expires_in': 600}, 202);
    server.answers['POST /v1/auth/chat/verify'] = () => json({'token': 'tok-2'});
    final a = auth();
    expect(await a.sendCode('08031234567'), startsWith('If this number'));
    await a.signInWithCode('08031234567', '123456');
    final v = server.calls.firstWhere((c) => c.path == '/v1/auth/chat/verify');
    expect(v.body!['code'], '123456');
    expect(v.body!['bearer'], isTrue);
    expect(v.body!['device'], isNotNull);
    expect(keys.values['token'], 'tok-2');
  });

  test('opening the app: a kept session is used; one that has ended is forgotten', () async {
    keys.values['token'] = 'kept';
    final a = auth();
    await a.start();
    expect(a.state, AuthState.signedIn);
    expect(server.calls.single.auth, 'Bearer kept');

    server.answers['GET /v1/me'] = () => json({'error': 'unauthenticated'}, 401);
    final b = auth();
    await b.start();
    expect(b.state, AuthState.signedOut);
    expect(keys.values['token'], isNull);
  });

  test('opening the app offline keeps the person signed in', () async {
    keys.values['token'] = 'kept';
    server.offline = true;
    final a = auth();
    await a.start();
    expect(a.state, AuthState.signedIn);
    expect(keys.values['token'], 'kept');
  });

  test('nothing kept: sign in', () async {
    final a = auth();
    await a.start();
    expect(a.state, AuthState.signedOut);
    expect(server.calls, isEmpty);
  });

  test('signing out ends the session on the server, and forgets it here even offline', () async {
    keys.values['token'] = 'kept';
    server.answers['POST /v1/auth/logout'] = () => http.Response('', 204);
    final a = auth();
    await a.start();
    await a.signOut();
    expect(server.calls.last.path, '/v1/auth/logout');
    expect(keys.values['token'], isNull);
    expect(a.state, AuthState.signedOut);

    keys.values['token'] = 'kept';
    final b = auth();
    await b.start();
    server.offline = true;
    await b.signOut();
    expect(keys.values['token'], isNull);
    expect(b.state, AuthState.signedOut);
  });
}
