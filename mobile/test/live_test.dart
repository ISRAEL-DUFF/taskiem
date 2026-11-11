// The app's client against a real Taskiem (TASKIEM_LIVE_URL, such as the
// server web/e2e/serve.sh starts at http://127.0.0.1:18080), so the
// generated client is checked against what the server really answers.
// Without the variable it is skipped.

@Tags(['live'])
library;

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';

void main() {
  final base = Platform.environment['TASKIEM_LIVE_URL'];

  test('sign up, sign in on a phone, Your phones, sign out', () async {
    final stamp = DateTime.now().microsecondsSinceEpoch;
    final email = 'phone-$stamp@live.test';
    final api = TaskiemApi(Transport(base: base!, client: http.Client()));
    final opts = await api.appConfig();
    expect(opts.minVersion, isNotEmpty);
    final up = await api.signup(body: SignupRequest(tenant: 'Live $stamp', email: email, name: 'Ada', password: 'correct horse battery', acceptTerms: true));
    expect(up.tenantId, isNotEmpty);

    final keys = MemoryKeyStore();
    final auth = Auth(
      transport: Transport(base: base, client: http.Client()),
      store: SessionStore(keys, describe: () async => (name: 'Live phone', platform: 'android')),
    );
    await auth.signIn(email, 'correct horse battery');
    expect(auth.state, AuthState.signedIn);
    expect(auth.me!.tenantName, 'Live $stamp');

    final phones = (await auth.api.listDevices()).devices;
    expect(phones, hasLength(1));
    expect(phones.single.name, 'Live phone');
    expect(phones.single.this_, isTrue);

    // Opening the app again: the kept session still works.
    final again = Auth(transport: Transport(base: base, client: http.Client()), store: SessionStore(keys));
    await again.start();
    expect(again.state, AuthState.signedIn);

    await again.signOut();
    expect(keys.values['token'], isNull);
    final after = Auth(transport: Transport(base: base, client: http.Client()), store: SessionStore(keys));
    await after.start();
    expect(after.state, AuthState.signedOut);
  }, skip: base == null ? 'set TASKIEM_LIVE_URL to a Taskiem to run' : null);
}
