// Passkeys in the app (docs/spec/mobile-app-m4.md N6), with a stand-in for
// the phone's passkeys and the server: signing in, adding one, approving.

import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/approve.dart';

import 'auth_test.dart' show FakeServer;

class FakePasskeys implements PasskeyPlatform {
  final asked = <Map<String, dynamic>>[];
  bool cancel = false;
  @override
  Future<bool> available() async => true;
  @override
  Future<Map<String, dynamic>> create(Map<String, dynamic> publicKey) async {
    asked.add(publicKey);
    if (cancel) throw const PasskeyCancelled();
    return {'id': 'new', 'rawId': 'new', 'type': 'public-key', 'response': {'clientDataJSON': 'c', 'attestationObject': 'a'}};
  }

  @override
  Future<Map<String, dynamic>> get(Map<String, dynamic> publicKey) async {
    asked.add(publicKey);
    if (cancel) throw const PasskeyCancelled();
    return {'id': 'k1', 'rawId': 'k1', 'type': 'public-key', 'response': {'clientDataJSON': 'c', 'authenticatorData': 'd', 'signature': 's'}};
  }
}

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

const _get = {'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.example', 'userVerification': 'required', 'timeout': 60000}};

void main() {
  late FakeServer server;
  late FakePasskeys phone;
  late Auth auth;

  setUp(() {
    server = FakeServer();
    phone = FakePasskeys();
    passkeys = phone;
    server.answers['GET /v1/me'] = () => _json({'tenant_id': 't1', 'auth_method': 'passkey', 'permissions': <String>[], 'factors': {'passkey': true}});
    auth = Auth(
      transport: Transport(base: 'http://localhost', client: server.client()),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Ada’s Tecno', platform: 'android')),
    );
  });

  test('signing in with the phone’s passkey asks for a session for this phone', () async {
    server.answers['POST /v1/auth/passkey/options'] = () => _json(_get);
    server.answers['POST /v1/auth/passkey'] = () => _json({'token': 'tok-p'});
    await signInWithPasskey(auth);
    expect(phone.asked.single['challenge'], 'ch');
    final login = server.calls.firstWhere((c) => c.path == '/v1/auth/passkey');
    expect(login.body!['bearer'], isTrue);
    expect((login.body!['credential'] as Map)['id'], 'k1');
    expect((login.body!['device'] as Map)['name'], 'Ada’s Tecno');
    expect(auth.state, AuthState.signedIn);
  });

  test('closing the phone’s sheet signs nobody in and says nothing', () async {
    server.answers['POST /v1/auth/passkey/options'] = () => _json(_get);
    phone.cancel = true;
    await expectLater(signInWithPasskey(auth), throwsA(isA<PasskeyCancelled>()));
    expect(server.calls.where((c) => c.path == '/v1/auth/passkey'), isEmpty);
  });

  test('passkeys off on the server: its words', () async {
    server.answers['POST /v1/auth/passkey/options'] = () => _json({'error': 'passkeys are not configured'}, 501);
    await expectLater(signInWithPasskey(auth), throwsA(isA<ApiError>()));
  });

  test('adding one: the proof first, then the phone makes it, named after the phone', () async {
    server.answers['POST /v1/me/passkeys/options'] = () => _json({
      'publicKey': {'challenge': 'new-ch', 'rp': {'id': 'taskiem.example', 'name': 'Taskiem'}, 'user': {'id': 'dQ', 'name': 'ada', 'displayName': 'Ada'}},
    });
    server.answers['POST /v1/me/passkeys'] = () => _json({'id': 'new', 'name': 'Ada’s Tecno', 'synced': true}, 201);
    await addPasskey(auth, {'totp': '123456'});
    expect(server.calls.firstWhere((c) => c.path == '/v1/me/passkeys/options').body, {'totp': '123456'});
    final add = server.calls.firstWhere((c) => c.path == '/v1/me/passkeys');
    expect(add.body!['name'], 'Ada’s Tecno');
    expect((add.body!['credential'] as Map)['response'], containsPair('attestationObject', 'a'));
  });

  test('an approval that asks for a passkey: the phone’s, for this run, step and decision', () async {
    server.answers['POST /v1/me/step-up/options'] = () => _json(_get);
    server.answers['POST /v1/approvals/r1/pay'] = () => _json({'status': 'approved'});
    final ap = Approval.fromJson({
      'run_id': 'r1', 'step_id': 'pay', 'step_name': 'Pay', 'workflow': 'Pay a supplier', 'environment': 'prod',
      'required': 1, 'approvals': 0, 'level': 1, 'levels': 1, 'sole_approver': true, 'requested_at': '2026-11-14T10:00:00Z', 'step_up': 'passkey',
    });
    final proof = await approvalPasskey(auth, ap, 'approved');
    await decideApproval(auth, ap, 'approved', passkey: proof);
    expect(server.calls.firstWhere((c) => c.path == '/v1/me/step-up/options').body, {'operation': 'approval.decide', 'target': 'r1/pay/approved'});
    final decided = server.calls.firstWhere((c) => c.path == '/v1/approvals/r1/pay');
    expect(decided.body!['decision'], 'approved');
    expect((decided.body!['passkey'] as Map)['id'], 'k1');
    expect(decided.body!.containsKey('totp'), isFalse);
  });
}
