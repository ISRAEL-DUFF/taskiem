// Paying by fingerprint (docs/spec/fingerprint-key.md; lib/app/pay_key.dart,
// lib/screens/you_fingerprint.dart): the Pay sheet pays with this phone's
// key when it has one (the server's words signed after the fingerprint),
// goes back to the passkey or PIN when the server refuses, and offers to
// turn it on; Settings › Fingerprint turns it on with the PIN, lists the
// phones and removes them. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/app/pay_key.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/pay_sheet.dart';
import 'package:taskiem/kit/kit.dart' show TkField;
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you_fingerprint.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'passkey_test.dart' show FakePasskeys;
import 'pay_test.dart' show payment;
import 'support.dart' show loadFonts, snap;

const _json = {'content-type': 'application/json; charset=utf-8'};

/// The phone's secure hardware, stood in: keys by alias, signatures that
/// say what they signed.
class FakePayKeys implements PayKeyPlatform {
  FakePayKeys({this.can = true});
  bool can;
  final keys = <String, String>{};
  final signed = <String>[];
  bool cancel = false;

  @override
  Future<bool> available() async => can;
  @override
  Future<String> create(String alias, {required String prompt}) async {
    if (cancel) throw const FingerprintCancelled();
    return keys[alias] = 'PUB-$alias';
  }

  @override
  Future<String> sign(String alias, String words, {required String prompt}) async {
    if (cancel) throw const FingerprintCancelled();
    if (!keys.containsKey(alias)) throw const FingerprintGone();
    signed.add(words);
    return 'SIG:$words';
  }

  @override
  Future<void> remove(String alias) async => keys.remove(alias);
}

class _NoPasskeys extends FakePasskeys {
  @override
  Future<bool> available() async => false;
}

class _Api {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};
  final keys = MemoryKeyStore();

  List<Map<String, dynamic>> sent(String method, String path) => [
    for (final c in calls.where((c) => c.method == method && c.url.path == path))
      c.body.isEmpty ? <String, dynamic>{} : jsonDecode(c.body) as Map<String, dynamic>,
  ];

  late final Auth auth = () {
    final a = Auth(
      transport: Transport(
        base: 'http://localhost',
        token: 'tok',
        client: MockClient((req) async {
          calls.add(req);
          final f = routes['${req.method} ${req.url.path}'];
          if (f == null) return http.Response(jsonEncode({'error': 'not here'}), 404, headers: _json);
          final out = f(req);
          if (out is http.Response) return out;
          return http.Response(jsonEncode(out ?? {}), 200, headers: _json);
        }),
      ),
      store: SessionStore(keys, describe: () async => (name: 'Tecno Spark 10', platform: 'android')),
    );
    a.me = Me.fromJson({
      'tenant_id': 't1',
      'tenant_name': 'Mama’s Kitchen',
      'personal': false,
      'roles': ['owner'],
      'permissions': ['money.pay'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Ada Obi', 'email': 'ada@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }();
}

http.Response _err(int status, String code, String words) => http.Response(jsonEncode({'error': words, 'code': code}), status, headers: _json);

Map<String, dynamic> _device(String id, String name, {String device = 'd1', String? used}) => {
  'id': id,
  'device_id': device,
  'name': name,
  'platform': 'android',
  'created_at': '2026-11-20T09:00:00Z',
  'last_used_at': ?used,
};

Map<String, dynamic> _me(List<Map<String, dynamic>> devices, {List<String> proofs = const ['pin', 'code']}) => {
  'devices': devices,
  'one_kobo': 50000000,
  'day_kobo': 100000000,
  'left_today_kobo': 70000000,
  'proofs': proofs,
};

class _Host extends StatefulWidget {
  const _Host(this.open, this.closed);
  final Future<Object?> Function(BuildContext context) open;
  final List<Object?> closed;

  @override
  State<_Host> createState() => _HostState();
}

class _HostState extends State<_Host> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) async => widget.closed.add(await widget.open(context)));
  }

  @override
  Widget build(BuildContext context) => const SizedBox.expand();
}

void main() {
  setUpAll(loadFonts);
  setUp(() => TkField.debugHideCaret = true);

  late FakePayKeys fake;
  setUp(() {
    final was = payKeys, wasPasskeys = passkeys;
    fake = FakePayKeys();
    payKeys = fake;
    passkeys = _NoPasskeys();
    addTearDown(() => (payKeys = was, passkeys = wasPasskeys));
  });

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 600));
  }

  void phone(WidgetTester t) {
    t.view.physicalSize = const Size(360, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  Future<List<Object?>> openSheet(WidgetTester t, _Api api, Map<String, dynamic> p, {TkMode mode = TkMode.light}) async {
    final closed = <Object?>[];
    await t.pumpWidget(
      TaskiemApp(
        mode: mode,
        home: _Host((c) => showTkSheet<Payment>(c, (_) => PaymentSheet(auth: api.auth, payment: Payment.fromJson(p))), closed),
      ),
    );
    await settle(t);
    return closed;
  }

  for (final mode in [TkMode.light, TkMode.dark]) {
    testWidgets('this phone has the key: Pay signs the server’s words after the fingerprint (${mode.name})', (t) async {
      phone(t);
      final api = _Api();
      await api.keys.write('paykey:u1', 'k1');
      fake.keys[payKeyAlias('u1')] = 'PUB';
      api.routes['POST /v1/payments/p1/fingerprint'] = (_) => {'challenge': 'Taskiem payment\np1\n4500000\nnonce', 'expires_in': 120, 'limit_kobo': 50000000};
      api.routes['POST /v1/payments/p1/fingerprint/confirm'] = (_) => http.Response(jsonEncode({'payment': payment('p1', status: 'sending')}), 202, headers: _json);
      final closed = await openSheet(t, api, payment('p1'), mode: mode);
      expect(find.text('Use my passkey or PIN instead'), findsOneWidget);
      expect(find.text('Pay by fingerprint from now on'), findsNothing);
      await snap(t, 'pay-fingerprint-${mode.name}');
      await t.tap(find.text('Pay ₦45,000'));
      await settle(t);
      expect(api.sent('POST', '/v1/payments/p1/fingerprint').single, {'key_id': 'k1'});
      expect(fake.signed, ['Taskiem payment\np1\n4500000\nnonce']);
      expect(api.sent('POST', '/v1/payments/p1/fingerprint/confirm').single, {
        'key_id': 'k1',
        'challenge': 'Taskiem payment\np1\n4500000\nnonce',
        'signature': 'SIG:Taskiem payment\np1\n4500000\nnonce',
      });
      expect(api.sent('POST', '/v1/payments/p1/confirm'), isEmpty);
      await t.pump(const Duration(seconds: 2));
      await settle(t);
      expect((closed.single! as Payment).status, 'sending');
    });
  }

  testWidgets('over its limit: back to the passkey or PIN, said; a payment over it starts there', (t) async {
    phone(t);
    final api = _Api();
    await api.keys.write('paykey:u1', 'k1');
    fake.keys[payKeyAlias('u1')] = 'PUB';
    api.routes['POST /v1/payments/p2/fingerprint'] =
        (_) => _err(403, 'fingerprint_limit', 'Over your fingerprint’s limit of ₦1,000,000 a day (₦200,000 left today): use your passkey for this one.');
    await openSheet(t, api, payment('p2', kobo: 30000000));
    await t.tap(find.text('Pay ₦300,000'));
    await settle(t);
    expect(find.text('Use your passkey or PIN for this one'), findsOneWidget);
    expect(find.textContaining('₦200,000 left today'), findsOneWidget);
    expect(find.text('Pay with my fingerprint'), findsOneWidget);
    expect(fake.signed, isEmpty);

    // Over ₦500,000: the sheet starts with the passkey, the fingerprint offered.
    final big = _Api();
    await big.keys.write('paykey:u1', 'k1');
    await openSheet(t, big, payment('p3', kobo: 60000000));
    expect(find.text('Pay with my fingerprint'), findsOneWidget);
    expect(find.text('Use my passkey or PIN instead'), findsNothing);
  });

  testWidgets('no key here: Pay by fingerprint from now on turns it on with the PIN, then Pay uses it', (t) async {
    phone(t);
    final api = _Api();
    api.routes['GET /v1/me/fingerprint'] = (_) => _me([]);
    var tries = 0;
    api.routes['POST /v1/me/fingerprint'] = (r) {
      final b = jsonDecode(r.body) as Map<String, dynamic>;
      if (b['pin'] != '482913') {
        tries++;
        return _err(403, 'proof_needed', 'That PIN is not right. 4 tries left.');
      }
      return http.Response(jsonEncode({'device': _device('k9', 'Tecno Spark 10')}), 201, headers: _json);
    };
    api.routes['POST /v1/payments/p4/fingerprint'] = (_) => {'challenge': 'words', 'expires_in': 120, 'limit_kobo': 50000000};
    api.routes['POST /v1/payments/p4/fingerprint/confirm'] = (_) => http.Response(jsonEncode({'payment': payment('p4', status: 'sending')}), 202, headers: _json);
    await openSheet(t, api, payment('p4'));
    await t.tap(find.text('Pay by fingerprint from now on'));
    await settle(t);
    expect(find.text('Up to ₦500,000 a payment'), findsOneWidget);
    expect(find.text('Send a code to my phone instead'), findsOneWidget);
    await snap(t, 'fingerprint-on-light');
    final pad = find.descendant(of: find.byKey(const ValueKey('fingerprint-pin')), matching: find.byType(EditableText));
    await t.enterText(pad, '111111');
    await settle(t);
    await t.tap(find.text('Turn it on'));
    await settle(t);
    expect(tries, 1);
    expect(find.text('That PIN is not right. 4 tries left.'), findsOneWidget);
    // Refused: no key is left on the phone the server does not know.
    expect(fake.keys, isEmpty);
    await t.enterText(pad, '482913');
    await settle(t);
    await t.tap(find.text('Turn it on'));
    await settle(t);
    final on = api.sent('POST', '/v1/me/fingerprint').last;
    expect(on['pin'], '482913');
    expect(on['public_key'], 'PUB-taskiem-pay-u1');
    expect((on['device'] as Map)['name'], 'Tecno Spark 10');
    expect(await api.keys.read('paykey:u1'), 'k9');
    expect(find.text('Use my passkey or PIN instead'), findsOneWidget);
    await t.tap(find.text('Pay ₦45,000'));
    await settle(t);
    expect(api.sent('POST', '/v1/payments/p4/fingerprint/confirm').single['key_id'], 'k9');
    await t.pump(const Duration(seconds: 5));
    await settle(t);
  });

  testWidgets('a fingerprint added on the phone: the key is gone, said, and off here', (t) async {
    phone(t);
    final api = _Api();
    await api.keys.write('paykey:u1', 'k1');
    api.routes['POST /v1/payments/p5/fingerprint'] = (_) => {'challenge': 'words', 'expires_in': 120, 'limit_kobo': 50000000};
    api.routes['DELETE /v1/me/fingerprint/k1'] = (_) => http.Response('', 204);
    await openSheet(t, api, payment('p5'));
    await t.tap(find.text('Pay ₦45,000'));
    await settle(t);
    expect(find.text('Fingerprint is off'), findsOneWidget);
    expect(await api.keys.read('paykey:u1'), isNull);
    expect(api.sent('DELETE', '/v1/me/fingerprint/k1'), hasLength(1));
  });

  for (final mode in [TkMode.light, TkMode.dark]) {
    testWidgets('Settings › Fingerprint: this phone on, the phones listed, one removed, this one turned off (${mode.name})', (t) async {
      phone(t);
      final api = _Api();
      await api.keys.write('paykey:u1', 'k1');
      fake.keys[payKeyAlias('u1')] = 'PUB';
      var devices = [_device('k1', 'Tecno Spark 10', used: '2026-11-25T08:00:00Z'), _device('k2', 'Ada’s iPhone', device: 'd2')];
      api.routes['GET /v1/me/fingerprint'] = (_) => _me(devices);
      api.routes['DELETE /v1/me/fingerprint/k2'] = (_) {
        devices = devices.where((d) => d['id'] != 'k2').toList();
        return http.Response('', 204);
      };
      api.routes['DELETE /v1/me/fingerprint/k1'] = (_) {
        devices = devices.where((d) => d['id'] != 'k1').toList();
        return http.Response('', 204);
      };
      await t.pumpWidget(TaskiemApp(mode: mode, home: FingerprintScreen(auth: api.auth, now: () => DateTime.utc(2026, 11, 25, 12))));
      await settle(t);
      expect(find.text('On · up to ₦500,000 a payment'), findsOneWidget);
      expect(find.text('₦700,000'), findsOneWidget);
      expect(find.textContaining('This phone · turned on'), findsOneWidget);
      expect(find.textContaining('paid today'), findsOneWidget);
      await snap(t, 'fingerprint-settings-${mode.name}');
      if (mode == TkMode.dark) return;
      await t.tap(find.bySemanticsLabel('Remove Ada’s iPhone'));
      await settle(t);
      await t.tap(find.text('Remove').last);
      await settle(t);
      expect(api.sent('DELETE', '/v1/me/fingerprint/k2'), hasLength(1));
      expect(find.text('Ada’s iPhone'), findsNothing);
      await t.tap(find.bySemanticsLabel(RegExp('Pay with my fingerprint')).last);
      await settle(t);
      await t.tap(find.text('Turn off'));
      await settle(t);
      expect(api.sent('DELETE', '/v1/me/fingerprint/k1'), hasLength(1));
      expect(await api.keys.read('paykey:u1'), isNull);
      expect(fake.keys, isEmpty);
      expect(find.text('Off'), findsOneWidget);
      await t.pump(const Duration(seconds: 5));
    });
  }

  testWidgets('the turn-on sheet in the dark; large text and what can be pressed, here and in Settings', (t) async {
    phone(t);
    final api = _Api();
    await api.keys.write('paykey:u1', 'k1');
    api.routes['GET /v1/me/fingerprint'] = (_) => _me([_device('k1', 'Tecno Spark 10')], proofs: ['pin', 'code', 'totp']);
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: _Host((c) => turnOnFingerprintSheet(c, api.auth), [])));
    await settle(t);
    await t.tap(find.text('Send a code to my phone instead'));
    await settle(t);
    expect(find.text('Send the code'), findsOneWidget);
    await snap(t, 'fingerprint-on-code-dark');
    await expectPressable(t);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: _Host((c) => turnOnFingerprintSheet(c, api.auth), [])), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: FingerprintScreen(auth: api.auth)), drive: settle);
  });
}
