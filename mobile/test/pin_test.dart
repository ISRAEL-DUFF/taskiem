// The person's PIN (GET and PUT /v1/me/pin, POST /v1/me/pin/code;
// screens/you_pin.dart; docs/spec/transaction-pin.md): four digits, offered
// once a new account is made, with Not now; set twice as dots, a weak or
// mismatched one refused on the phone; confirmed by a code sent to the
// phone (or the passkey on this phone), and paying at once; Settings › PIN
// with when it was set and whether it is locked; with no phone and no
// passkey, what to add.

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

const _codeTo = '+234••••••0000';

/// GET /v1/me/pin as the server answers it (set, digits and ways are
/// required).
Map<String, dynamic> pin({bool set = true, List<String> ways = const ['code'], DateTime? lockedUntil, bool renew = false}) => {
  'set': set,
  'digits': 4,
  'ways': ways,
  if (ways.contains('code')) 'code_to': _codeTo,
  if (set) 'set_at': at.subtract(const Duration(hours: 2)).toIso8601String(),
  'locked_until': ?lockedUntil?.toIso8601String(),
  if (renew) 'renew': true,
};

Map<String, dynamic> _sentCode() => {'sent_via': 'whatsapp', 'expires_in': 600, 'code_to': _codeTo};

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

  test('four digits; weak PINs and two entries that differ are refused on the phone', () {
    expect(pinDigits, 4);
    for (final weak in ['1111', '1234', '6543', '8901', '0987']) {
      expect(weakPin(weak), isTrue, reason: weak);
    }
    expect(weakPin('4027'), isFalse);
    expect(pinProblem('402', ''), 'Type the 4 digits of your new PIN.');
    expect(pinProblem('402719', '402719'), 'Type the 4 digits of your new PIN.');
    expect(pinProblem('1234', '1234'), 'Choose a PIN that is not one digit repeated or a run like 1234.');
    expect(pinProblem('4027', '4028'), 'The two PINs are not the same. Type them again.');
    expect(pinProblem('4027', '4027'), isNull);
    expect(pinWhen(at.add(const Duration(days: 1)), at), 'tomorrow at 10:30');
  });

  testWidgets('a new account is offered Set your PIN; Not now goes on to You', (t) async {
    phone(t);
    final api = you();
    api.routes['POST /v1/auth/phone/create'] = (_) => answer({'status': 'created', 'token': 'tok-p', 'personal': true, 'tenant_id': null, 'user_id': 'u1'}, 201);
    api.routes['GET /v1/me'] = (_) => _me();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false);
    final a = api.auth();
    await t.runAsync(() => a.createPersonal('tk-1', 'Tolu Ade'));
    expect(a.offerPin, isTrue);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: a, now: () => at));
    await settle(t);
    expect(find.text('Set your PIN'), findsWidgets);
    expect(find.textContaining('It can pay as soon as it is set.'), findsOneWidget);
    expect(find.textContaining('We send a code to $_codeTo'), findsOneWidget);
    expect(find.text('Send me a code'), findsOneWidget);
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

  testWidgets('the step: a mismatch and a weak one refused here, then a code, then {pin, code}', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false);
    api.routes['POST /v1/me/pin/code'] = (_) => answer(_sentCode(), 202);
    var tries = 0;
    api.routes['PUT /v1/me/pin'] = (_) {
      tries++;
      if (tries == 1) return answer({'error': 'That code is not right, or it has expired. Ask for a new one.', 'code_wrong': true}, 401);
      return pin();
    };
    final a = api.auth()..offerPin = true;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: a, now: () => at));
    await settle(t);
    await _pin(t, '4027', '4028');
    await press(t, find.bySemanticsLabel('Send me a code').last);
    expect(find.text('The two PINs are not the same. Type them again.'), findsOneWidget);
    await _pin(t, '1111', '1111');
    await press(t, find.bySemanticsLabel('Send me a code').last);
    expect(find.text('Choose a PIN that is not one digit repeated or a run like 1234.'), findsOneWidget);
    expect(api.sent('POST', '/v1/me/pin/code'), isEmpty);
    // The digits are dots, never shown: four boxes each.
    await _pin(t, '4027', '4027');
    expect(find.text('4'), findsNothing);
    expect(find.text('•'), findsNWidgets(8));
    await press(t, find.bySemanticsLabel('Send me a code').last);
    expect(api.sent('POST', '/v1/me/pin/code'), hasLength(1));
    expect(find.textContaining('Sent to $_codeTo on WhatsApp'), findsOneWidget);
    await snap(t, 'pin-step-code');
    // A wrong code: said, typed again.
    await typeIn(t, find.byKey(const ValueKey('pin-code')), '111111');
    await press(t, find.bySemanticsLabel('Set your PIN').last);
    expect(find.text('That code is not right. Check it, or ask for a new one.'), findsOneWidget);
    expect(find.byType(YouHome), findsNothing);
    await typeIn(t, find.byKey(const ValueKey('pin-code')), '482913');
    await press(t, find.bySemanticsLabel('Set your PIN').last);
    expect(_body(api, 'PUT', '/v1/me/pin'), {'pin': '4027', 'code': '482913'});
    expect(find.text('Your PIN is set. It can pay now.'), findsOneWidget);
    expect(find.byType(YouHome), findsOneWidget);
    await drain(t);
  });

  testWidgets('no phone and no passkey: what to add, no form', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false, ways: const []);
    await _page(t, api);
    expect(find.text('Add your phone first'), findsOneWidget);
    expect(find.byKey(const ValueKey('pin-new')), findsNothing);
    await snap(t, 'pin-no-way');
    await drain(t);
  });

  testWidgets('the old 6-digit PIN went: said, and a new one set', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false, renew: true);
    await _page(t, api);
    expect(find.text('PINs are now 4 digits'), findsOneWidget);
    expect(find.byKey(const ValueKey('pin-new')), findsOneWidget);
    await snap(t, 'pin-renew');
    await drain(t);
  });

  testWidgets('the server\'s refusal of a weak PIN is shown in its words', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false);
    api.routes['POST /v1/me/pin/code'] = (_) => answer({'error': 'choose a PIN that is not one digit repeated or a run like 1234'}, 400);
    await _page(t, api);
    expect(find.text('Set your PIN'), findsWidgets);
    await _pin(t, '4027', '4027');
    await press(t, find.bySemanticsLabel('Send me a code').last);
    expect(find.text('choose a PIN that is not one digit repeated or a run like 1234'), findsOneWidget);
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
    expect(find.text('••••'), findsOneWidget);
    await drain(t);
  });

  testWidgets('set: when, what it pays, locked', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(lockedUntil: at.add(const Duration(minutes: 15)));
    await _page(t, api);
    expect(find.text('today at 08:30'), findsOneWidget);
    expect(find.text('Up to ₦100,000 a payment'), findsOneWidget);
    expect(find.text('Locked until today at 10:45'), findsOneWidget);
    expect(find.textContaining('Can pay from'), findsNothing);
    await snap(t, 'pin-settings');
    await looks(t, 'pin-settings');
    await drain(t);

    api.routes['GET /v1/me/pin'] = (_) => pin();
    await _page(t, api);
    expect(find.textContaining('Locked until'), findsNothing);
    await drain(t);
  });

  testWidgets('Remove my PIN, asked first, sends DELETE and shows Set your PIN', (t) async {
    phone(t);
    final api = you();
    var set = true;
    api.routes['GET /v1/me/pin'] = (_) => set ? pin() : pin(set: false);
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

  testWidgets('Change PIN: a code to the phone; a passkey is never offered in the app', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(ways: const ['code', 'passkey']);
    api.routes['POST /v1/me/pin/code'] = (_) => answer(_sentCode(), 202);
    api.routes['PUT /v1/me/pin'] = (_) => pin(ways: const ['code', 'passkey']);
    await _page(t, api, passkey: true);
    await press(t, find.text('Change PIN'));
    expect(find.text('Use my passkey instead'), findsNothing);
    expect(find.bySemanticsLabel('Confirm with your passkey and save'), findsNothing);
    await _pin(t, '4027', '4027');
    await snap(t, 'pin-change');
    await press(t, find.bySemanticsLabel('Send me a code'));
    await typeIn(t, find.byKey(const ValueKey('pin-code')), '482913');
    await press(t, find.bySemanticsLabel('Save the new PIN'));
    expect(_body(api, 'PUT', '/v1/me/pin'), {'pin': '4027', 'code': '482913'});
    expect(keys.asked, isEmpty);
    expect(find.text('Your new PIN is saved. It can pay now.'), findsOneWidget);
    await drain(t);
  });

  testWidgets('only a passkey on the account: Add my phone, not the passkey', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false, ways: const ['passkey']);
    await _page(t, api, passkey: true);
    expect(find.text('Add your phone first'), findsOneWidget);
    expect(find.bySemanticsLabel('Add my phone'), findsWidgets);
    await snap(t, 'pin-add-phone');
    expect(find.bySemanticsLabel('Confirm with your passkey and save'), findsNothing);
    await drain(t);
  });

  testWidgets('Change PIN with no passkey on this phone: a code to the phone', (t) async {
    phone(t);
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(ways: const ['code', 'passkey']);
    api.routes['POST /v1/me/pin/code'] = (_) => answer(_sentCode(), 202);
    api.routes['PUT /v1/me/pin'] = (_) => pin();
    // The account has a passkey, but not on this phone.
    await _page(t, api);
    await press(t, find.text('Change PIN'));
    expect(find.text('Use a code to my phone instead'), findsNothing);
    expect(find.text('Use my passkey instead'), findsNothing);
    await _pin(t, '4027', '4027');
    await press(t, find.bySemanticsLabel('Send me a code'));
    await typeIn(t, find.byKey(const ValueKey('pin-code')), '482913');
    await press(t, find.bySemanticsLabel('Save the new PIN'));
    expect(_body(api, 'PUT', '/v1/me/pin'), {'pin': '4027', 'code': '482913'});
    expect(keys.asked, isEmpty);
    expect(find.text('Your new PIN is saved. It can pay now.'), findsOneWidget);
    await drain(t);
  });

  testWidgets('large text and what can be pressed: the step and the page', (t) async {
    final api = you();
    api.routes['GET /v1/me/pin'] = (_) => pin(set: false);
    final a = api.auth()..offerPin = true;
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, auth: a, now: () => at), drive: settle);
    await drain(t);
    api.routes['GET /v1/me/pin'] = (_) => pin(lockedUntil: at.add(const Duration(minutes: 15)), ways: const ['code', 'passkey']);
    a.me = Me.fromJson(_me(passkey: true));
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: PinScreen(auth: a, now: () => at)), drive: (t) async {
      await settle(t);
      await press(t, find.text('Change PIN'));
    });
    await drain(t);
  });
}
