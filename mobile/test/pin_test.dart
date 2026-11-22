// The person's PIN (GET and PUT /v1/me/pin; screens/you_pin.dart): offered
// once a new account is made, with Not now; set twice as dots, a weak or
// mismatched one refused on the phone; Settings › PIN with when it pays and
// whether it is locked; Change PIN with the passkey, or without it only
// moments after signing in.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart' show YouHome, YouSettingsScreen;
import 'package:taskiem/screens/you_pin.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'my_chats_test.dart' show Api, answer, at, drain, phone, press, settle, typeIn;
import 'passkey_test.dart' show FakePasskeys;
import 'support.dart' show loadFonts, snap;
import 'you_stage3_test.dart' show you;

Map<String, dynamic> _me({bool passkey = false}) => {
  'tenant_id': null,
  'personal': true,
  'roles': <String>[],
  'permissions': <String>[],
  'auth_method': 'phone',
  'user': {'id': 'u1', 'name': 'Tolu Ade', 'email': 'p@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031230000'},
  'businesses': <Object>[],
  'factors': {'passkey': passkey, 'totp': false, 'password': false},
};

/// GET /v1/me/pin as the server answers it (set and change_without are
/// required).
Map<String, dynamic> pin({bool set = true, bool changeWithout = false, DateTime? paysFrom, DateTime? lockedUntil, DateTime? until}) => {
  'set': set,
  'change_without': changeWithout,
  if (set) 'set_at': at.subtract(const Duration(hours: 2)).toIso8601String(),
  if (set) 'pays_from': (paysFrom ?? at.subtract(const Duration(hours: 1))).toIso8601String(),
  'locked_until': ?lockedUntil?.toIso8601String(),
  'change_without_until': ?until?.toIso8601String(),
};

Map<String, dynamic> _body(Api api, String method, String path) => jsonDecode(api.sent(method, path).last.body) as Map<String, dynamic>;

Future<void> _pin(WidgetTester t, String first, String again) async {
  await typeIn(t, find.byKey(const ValueKey('pin-new')), first);
  await typeIn(t, find.byKey(const ValueKey('pin-again')), again);
}

Future<void> _page(WidgetTester t, Api api, {bool passkey = false, TkMode mode = TkMode.light}) async {
  final a = api.auth();
  a.me = Me.fromJson(_me(passkey: passkey));
  await t.pumpWidget(TaskiemApp(mode: mode, home: PinScreen(auth: a, now: () => at)));
  await settle(t);
}

void main() {
  setUpAll(loadFonts);
  late PasskeyPlatform was;
  late FakePasskeys keys;
  setUp(() {
    was = passkeys;
    passkeys = keys = FakePasskeys();
  });
  tearDown(() => passkeys = was);

  test('weak PINs and two entries that differ are refused on the phone', () {
    for (final weak in ['111111', '123456', '654321', '890123', '098765']) {
      expect(weakPin(weak), isTrue, reason: weak);
    }
    expect(weakPin('402719'), isFalse);
    expect(pinProblem('12345', ''), 'Type the 6 digits of your new PIN.');
    expect(pinProblem('123456', '123456'), 'Choose a PIN that is not one digit repeated or a run like 123456.');
    expect(pinProblem('402719', '402718'), 'The two PINs are not the same. Type them again.');
    expect(pinProblem('402719', '402719'), isNull);
    expect(pinWhen(at.add(const Duration(days: 1)), at), 'tomorrow at 10:30');
  });

  testWidgets('a new account is offered Set your PIN; Not now goes on to You', (t) async {
    phone(t);
    final api = you();
    api.routes['POST /v1/auth/phone/create'] = (_) => answer({'status': 'created', 'token': 'tok-p', 'personal': true, 'tenant_id': null, 'user_id': 'u1'}, 201);
    api.routes['GET /v1/me'] = (_) => _me();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false, changeWithout: true);
    final a = api.auth();
    await t.runAsync(() => a.createPersonal('tk-1', 'Tolu Ade'));
    expect(a.offerPin, isTrue);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: a, now: () => at));
    await settle(t);
    expect(find.text('Set your PIN'), findsWidgets);
    expect(find.textContaining('It can pay from 24 hours after it is set.'), findsOneWidget);
    expect(find.text('Not now'), findsOneWidget);
    await snap(t, 'pin-step');
    await looks(t, 'pin-step');
    await press(t, find.text('Not now'));
    expect(find.byType(YouHome), findsOneWidget);
    expect(a.offerPin, isFalse);
    expect(api.sent('PUT', '/v1/me/pin'), isEmpty);
    await drain(t);
  });

  testWidgets('one already set: the step is not shown', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin();
    final a = api.auth()..offerPin = true;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: a, now: () => at));
    await settle(t);
    expect(find.byType(YouHome), findsOneWidget);
    expect(find.text('Not now'), findsNothing);
    await drain(t);
  });

  testWidgets('the step: a mismatch refused here, a weak one in words, then {pin} sent', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false, changeWithout: true);
    api.routes['PUT /v1/me/pin'] = (_) => pin(changeWithout: true, paysFrom: at.add(const Duration(hours: 24)));
    final a = api.auth()..offerPin = true;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: a, now: () => at));
    await settle(t);
    await _pin(t, '402719', '402718');
    await press(t, find.bySemanticsLabel('Set your PIN').last);
    expect(find.text('The two PINs are not the same. Type them again.'), findsOneWidget);
    await _pin(t, '111111', '111111');
    await press(t, find.bySemanticsLabel('Set your PIN').last);
    expect(find.text('Choose a PIN that is not one digit repeated or a run like 123456.'), findsOneWidget);
    expect(api.sent('PUT', '/v1/me/pin'), isEmpty);
    // The digits are dots, never shown.
    await _pin(t, '402719', '402719');
    expect(find.text('4'), findsNothing);
    expect(find.text('•'), findsNWidgets(12));
    await press(t, find.bySemanticsLabel('Set your PIN').last);
    expect(_body(api, 'PUT', '/v1/me/pin'), {'pin': '402719'});
    // The toast (from when it pays is read against the phone's clock).
    expect(find.textContaining('Your PIN is set.'), findsOneWidget);
    expect(find.byType(YouHome), findsOneWidget);
    await drain(t);
  });

  testWidgets('the server\'s refusal of a weak PIN is shown in its words', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false, changeWithout: true);
    api.routes['PUT /v1/me/pin'] = (_) => answer({'error': 'choose a PIN that is not one digit repeated or a run like 123456'}, 400);
    await _page(t, api);
    expect(find.text('Set your PIN'), findsWidgets);
    await _pin(t, '402719', '402719');
    await press(t, find.bySemanticsLabel('Set your PIN').last);
    expect(find.text('choose a PIN that is not one digit repeated or a run like 123456'), findsOneWidget);
    await drain(t);
  });

  testWidgets('Settings has a PIN row that opens its page', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouSettingsScreen(auth: api.auth(), now: () => at)));
    await settle(t);
    await press(t, find.text('PIN'));
    expect(find.text('Change PIN'), findsOneWidget);
    expect(find.text('••••••'), findsOneWidget);
    await drain(t);
  });

  testWidgets('set: when, from when it pays, locked', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(paysFrom: at.add(const Duration(hours: 20)), lockedUntil: at.add(const Duration(minutes: 15)));
    await _page(t, api);
    expect(find.text('today at 08:30'), findsOneWidget);
    expect(find.text('Can pay from tomorrow at 06:30'), findsOneWidget);
    expect(find.text('From tomorrow at 06:30'), findsOneWidget);
    expect(find.text('Locked until today at 10:45'), findsOneWidget);
    await snap(t, 'pin-settings');
    await looks(t, 'pin-settings');
    await drain(t);

    api.routes['GET /v1/me/pin'] = (_) => pin();
    await _page(t, api);
    expect(find.textContaining('Can pay from'), findsNothing);
    expect(find.textContaining('Locked until'), findsNothing);
    expect(find.text('Now, up to ₦100,000'), findsOneWidget);
    await drain(t);
  });

  testWidgets('Remove my PIN, asked first, sends DELETE and shows Set your PIN', (t) async {
    phone(t);
    final api = you();
    var set = true;
    api.routes['GET /v1/me/pin'] = (_) => set ? pin() : pin(set: false, changeWithout: true);
    api.routes['DELETE /v1/me/pin'] = (_) {
      set = false;
      return null;
    };
    await _page(t, api);
    await press(t, find.text('Remove my PIN'));
    expect(find.text('Remove your PIN?'), findsOneWidget);
    await press(t, find.text('Remove'));
    expect(api.sent('DELETE', '/v1/me/pin'), hasLength(1));
    expect(find.text('Set your PIN'), findsWidgets);
    await drain(t);
  });

  testWidgets('Change PIN with the passkey sends {pin, passkey}', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin();
    api.routes['POST /v1/me/step-up/options'] = (_) => {
      'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'},
    };
    api.routes['PUT /v1/me/pin'] = (_) => pin();
    await _page(t, api, passkey: true);
    await press(t, find.text('Change PIN'));
    await _pin(t, '402719', '402719');
    await snap(t, 'pin-change');
    await press(t, find.bySemanticsLabel('Confirm with your passkey and save'));
    expect(_body(api, 'POST', '/v1/me/step-up/options'), {'operation': 'account.reauth', 'target': 'pin.set'});
    expect(keys.asked, hasLength(1));
    final put = _body(api, 'PUT', '/v1/me/pin');
    expect(put['pin'], '402719');
    expect((put['passkey'] as Map)['id'], 'k1');
    expect(find.text('Your new PIN is saved.'), findsOneWidget);
    await drain(t);
  });

  testWidgets('Change PIN moments after signing in: no passkey asked, {pin} sent', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(changeWithout: true, until: at.add(const Duration(minutes: 8)));
    api.routes['PUT /v1/me/pin'] = (_) => pin(changeWithout: true, paysFrom: at.add(const Duration(hours: 24)));
    await _page(t, api, passkey: true);
    await press(t, find.text('Change PIN'));
    expect(find.textContaining('no passkey is needed until 10:38'), findsOneWidget);
    await _pin(t, '402719', '402719');
    await press(t, find.bySemanticsLabel('Save the new PIN'));
    expect(_body(api, 'PUT', '/v1/me/pin'), {'pin': '402719'});
    expect(keys.asked, isEmpty);
    expect(find.text('Your new PIN is saved. It can pay from tomorrow at 10:30.'), findsOneWidget);
    await drain(t);
  });

  testWidgets('Change PIN with no passkey and no sign-in moments ago: sign in again', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin();
    await _page(t, api);
    await press(t, find.text('Change PIN'));
    expect(find.text(pinSignInAgain), findsOneWidget);
    expect(find.byKey(const ValueKey('pin-new')), findsNothing);
    expect(api.sent('PUT', '/v1/me/pin'), isEmpty);
    await snap(t, 'pin-no-passkey');
    await drain(t);
  });

  testWidgets('403 proof_needed is shown in the server\'s words', (t) async {
    phone(t);
    final api = you();
    // Read as changeable moments ago, but the ten minutes ran out.
    api.routes['GET /v1/me/pin'] = (_) => pin(changeWithout: true);
    api.routes['PUT /v1/me/pin'] = (_) => answer({
      'code': 'proof_needed',
      'reauth': ['passkey'],
      'error': 'To change your PIN, confirm with your passkey; or sign out, sign in again and change it within 10 minutes (it can then pay from 24 hours on).',
    }, 403);
    await _page(t, api);
    await press(t, find.text('Change PIN'));
    await _pin(t, '402719', '402719');
    await press(t, find.bySemanticsLabel('Save the new PIN'));
    expect(find.textContaining('To change your PIN, confirm with your passkey'), findsOneWidget);
    await drain(t);
  });

  testWidgets('large text and what can be pressed: the step and the page', (t) async {
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false, changeWithout: true);
    final a = api.auth()..offerPin = true;
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, auth: a, now: () => at), drive: settle);
    await drain(t);
    api.routes['GET /v1/me/pin'] = (_) => pin(paysFrom: at.add(const Duration(hours: 20)), lockedUntil: at.add(const Duration(minutes: 15)));
    a.me = Me.fromJson(_me(passkey: true));
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: PinScreen(auth: a, now: () => at)), drive: (t) async {
      await settle(t);
      await press(t, find.text('Change PIN'));
    });
    await drain(t);
  });
}
