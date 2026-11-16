// Taskiem for everyone, stage 1 in the app (docs/spec/taskiem-for-everyone.md
// §5, §6, §7, §18): the phone way in, You, the switcher and its steps,
// Start something, invitations and changing a number.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/applinks.dart';
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/sign_in.dart';
import 'package:taskiem/screens/start_something.dart';
import 'package:taskiem/screens/switcher.dart';
import 'package:taskiem/screens/welcome.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'auth_test.dart' show FakeServer;
import 'passkey_test.dart' show FakePasskeys;
import 'support.dart' show loadFonts, snap;

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

Map<String, dynamic> personalMe({List<Map<String, dynamic>> businesses = const [], Map<String, dynamic>? ways}) => {
  'tenant_id': null,
  'personal': true,
  'roles': <String>[],
  'permissions': <String>[],
  'auth_method': 'phone',
  'user': {'id': 'u1', 'name': 'Tolu Ade', 'email': 'person+x@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031230000'},
  'businesses': businesses,
  'second_proof': {'required': businesses.isNotEmpty, 'device_proved': false, 'ways': ways ?? {'passkey': false, 'email': true, 'pin': true}},
};

const _business = {'id': 'b1', 'name': 'Mama’s Kitchen', 'kind': 'business', 'locked': true, 'lock': 'second_proof_required'};
const _workflows = {'id': 'w1', 'name': 'Tolu’s workflows', 'kind': 'workflows', 'locked': false, 'lock': null};

Auth _auth(FakeServer server, {MemoryKeyStore? keys}) => Auth(
  transport: Transport(base: 'http://localhost', client: server.client()),
  store: SessionStore(keys ?? MemoryKeyStore(), describe: () async => (name: 'Tolu’s Tecno', platform: 'android')),
);

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 500));
}

Future<void> typeIn(WidgetTester t, Finder field, String text) async {
  final box = find.descendant(of: field, matching: find.byType(EditableText));
  await t.ensureVisible(box.first);
  await t.enterText(box.first, text);
  await t.pump();
}

/// The field whose label is [label].
Finder labelled(String label) => find.ancestor(of: find.text(label), matching: find.byType(Column)).first;

Future<void> press(WidgetTester t, String words) async {
  final f = find.text(words).last;
  await t.ensureVisible(f);
  await t.pump();
  await t.tap(f);
  await settle(t);
}

void main() {
  setUpAll(loadFonts);

  void phone(WidgetTester t) {
    t.view.physicalSize = const Size(390, 1400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  for (final mode in TkMode.values) {
    testWidgets('the first screen is the phone way in, ${mode.name}', (t) async {
      phone(t);
      final server = FakeServer();
      final auth = _auth(server)..state = AuthState.signedOut;
      await t.pumpWidget(TaskiemApp(mode: mode, auth: auth));
      await settle(t);
      for (final words in ['Welcome to Taskiem', 'Your phone number', 'Continue', 'Sign in with email']) {
        expect(find.text(words), findsOneWidget, reason: words);
      }
      expect(find.text('Chat with the businesses you buy from, and run your own.'), findsWidgets);
      expect(find.text('Create an account'), findsNothing, reason: 'no business sign-up as the way in');
      await snap(t, 'welcome-${mode.name}');
    });
  }

  testWidgets('a new number: the code, then a name, 18 or over and the terms; then You', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json(personalMe());
    server.answers['GET /v1/me/invitations'] = () => _json({'invitations': <Object>[]});
    server.answers['POST /v1/auth/phone/start'] = () => _json({'sent_via': 'whatsapp', 'expires_in': 600}, 202);
    server.answers['POST /v1/auth/phone/verify'] = () => _json({'status': 'new', 'ticket': 'tk-1', 'expires_in': 900});
    server.answers['POST /v1/auth/phone/create'] = () => _json({'status': 'created', 'token': 'tok-p', 'personal': true, 'tenant_id': null, 'user_id': 'u1'}, 201);
    final auth = _auth(server)..state = AuthState.signedOut;
    await t.pumpWidget(TaskiemApp(auth: auth));
    await settle(t);

    await press(t, 'Continue');
    expect(find.text('That doesn\'t look like a phone number.'), findsOneWidget);
    // Said before sending: half a number sends nothing.
    await typeIn(t, find.byKey(const ValueKey('phone')), '0803 123');
    await press(t, 'Continue');
    expect(find.text('That doesn\'t look like a phone number.'), findsOneWidget);
    expect(server.calls.where((c) => c.path == '/v1/auth/phone/start'), isEmpty);
    await typeIn(t, find.byKey(const ValueKey('phone')), '0803 123 0000');
    await press(t, 'Continue');
    expect(server.calls.last.body, {'phone': '+2348031230000'});
    expect(find.text('We sent a 6-digit code to WhatsApp on +234 803 123 0000.'), findsOneWidget);
    // A new code waits a minute, as on the web.
    expect(find.textContaining(RegExp(r'You can ask for a new code in [01]:[0-9]{2}\.')), findsOneWidget);
    expect(find.text('Send a new code'), findsNothing);
    expect(find.text('We sent a new code.'), findsNothing);

    // By SMS instead.
    server.answers['POST /v1/auth/phone/start'] = () => _json({'sent_via': 'sms', 'expires_in': 600}, 202);
    await press(t, 'Send by SMS instead');
    expect(server.calls.last.body, {'phone': '+2348031230000', 'via': 'sms'});
    expect(find.text('We sent a 6-digit code by SMS to +234 803 123 0000.'), findsOneWidget);
    expect(find.text('Send by SMS instead'), findsNothing);
    await t.pump(const Duration(seconds: 61));
    expect(find.text('Send a new code'), findsOneWidget);

    // A wrong code, in plain words.
    server.answers['POST /v1/auth/phone/verify'] = () => _json({'error': 'That code is not right.', 'code_wrong': true}, 401);
    await typeIn(t, find.byKey(const ValueKey('code')), '111111');
    await press(t, 'Continue');
    expect(find.text('That code is not right. Check it, or ask for a new one.'), findsOneWidget);
    expect(auth.state, AuthState.signedOut, reason: 'a wrong code signs nobody out or in');

    server.answers['POST /v1/auth/phone/verify'] = () => _json({'status': 'new', 'ticket': 'tk-1', 'expires_in': 900});
    await typeIn(t, find.byKey(const ValueKey('code')), '123456');
    await press(t, 'Continue');
    final verify = server.calls.lastWhere((c) => c.path == '/v1/auth/phone/verify').body!;
    expect(verify['code'], '123456');
    expect(verify['bearer'], isTrue);
    expect((verify['device'] as Map)['name'], 'Tolu’s Tecno');
    expect(find.text('Make your account'), findsOneWidget);
    await snap(t, 'welcome-create');

    await typeIn(t, find.byKey(const ValueKey('name')), 'Tolu Ade');
    await press(t, 'Create');
    expect(find.text('You need to be 18 or over to use Taskiem.'), findsOneWidget);
    await press(t, 'I\'m 18 or over');
    await press(t, 'Create');
    expect(find.text('Agree to the terms and privacy notice to go on.'), findsOneWidget);
    await t.tap(find.textContaining('I agree to Taskiem\'s'));
    await settle(t);
    await press(t, 'Create');
    final create = server.calls.lastWhere((c) => c.path == '/v1/auth/phone/create').body!;
    expect(create['ticket'], 'tk-1');
    expect(create['name'], 'Tolu Ade');
    expect(create['adult'], isTrue);
    expect(create['terms'], isTrue);
    expect(create['bearer'], isTrue);
    expect(create['device'], isNotNull);
    expect(auth.transport.token, 'tok-p');
    expect(auth.personal, isTrue);
    expect(find.byType(YouHome), findsOneWidget);
    expect(find.text('Chats with businesses will show here.'), findsOneWidget);
    expect(find.text('Start something'), findsOneWidget);
    await snap(t, 'you');
    expect(find.bySemanticsLabel('Switch'), findsNothing, reason: 'no switcher for a person in no business');
  });

  testWidgets('a number with an account signs in to You; Sign in with email is a link away', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json(personalMe(businesses: [_business]));
    server.answers['GET /v1/me/invitations'] = () => _json({'invitations': <Object>[]});
    server.answers['POST /v1/auth/phone/start'] = () => _json({'sent_via': 'whatsapp', 'expires_in': 600}, 202);
    server.answers['POST /v1/auth/phone/verify'] = () => _json({'status': 'signed_in', 'token': 'tok-s', 'personal': true, 'tenant_id': null});
    final auth = _auth(server)..state = AuthState.signedOut;
    await t.pumpWidget(TaskiemApp(auth: auth));
    await settle(t);

    await press(t, 'Sign in with email');
    expect(find.byType(SignInScreen), findsOneWidget);
    expect(find.text('Create an account'), findsNothing);
    await press(t, 'Use your phone number instead');
    expect(find.byType(WelcomeScreen), findsOneWidget);

    await typeIn(t, find.byKey(const ValueKey('phone')), '+2348031230000');
    await press(t, 'Continue');
    await typeIn(t, find.byKey(const ValueKey('code')), '123456');
    await press(t, 'Continue');
    expect(auth.transport.token, 'tok-s');
    expect(find.byType(YouHome), findsOneWidget);
    expect(find.bySemanticsLabel('Switch'), findsOneWidget, reason: 'in a business: the switcher');
  });

  testWidgets('a locked business asks for a second proof (the chat PIN), then opens; a 401 there signs nobody out', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json(personalMe(businesses: [_business, _workflows]));
    server.answers['GET /v1/me/businesses'] = () => _json({'personal': true, 'businesses': [_business, _workflows]});
    var proved = false;
    server.answers['POST /v1/me/switch'] = () => proved
        ? _json({'token': 'tok-b', 'tenant_id': 'b1', 'user_id': 'u1'})
        : _json({'error': 'Confirm it is you first.', 'second_proof_required': true, 'ways': {'passkey': false, 'email': true, 'pin': true}}, 401);
    server.answers['POST /v1/auth/second-proof/pin'] = () {
      if (server.calls.last.body!['pin'] != '4321') return _json({'error': 'That PIN is not right.', 'tries_left': 4}, 401);
      proved = true;
      return _json({'device_proved': true});
    };
    final auth = _auth(server);
    auth.transport.token = 'tok-p';
    auth.me = Me.fromJson(personalMe(businesses: [_business, _workflows]));
    auth.state = AuthState.signedIn;
    await t.pumpWidget(TaskiemApp(home: Builder(builder: (c) => GestureDetector(onTap: () => showSwitcher(c, auth), child: const Text('open')))));
    await t.tap(find.text('open'));
    await settle(t);

    expect(find.text('Switch to'), findsOneWidget);
    expect(find.text('You'), findsOneWidget);
    expect(find.text('New device: prove it\'s you to open it'), findsOneWidget);
    expect(find.text('Workflows'), findsOneWidget, reason: 'a workflows workspace says so');
    await snap(t, 'switcher');

    await press(t, 'Mama’s Kitchen');
    expect(server.calls.last.body, {'tenant_id': 'b1'});
    expect(find.text('Email me a link'), findsOneWidget);
    expect(find.text('Your chat PIN'), findsOneWidget);
    await snap(t, 'switcher-proof');
    expect(auth.state, AuthState.signedIn, reason: 'asked for a step, still signed in');

    await typeIn(t, labelled('Your chat PIN'), '1111');
    await press(t, 'Confirm with my PIN');
    expect(find.text('That PIN is not right. 4 tries left.'), findsOneWidget);
    expect(auth.state, AuthState.signedIn, reason: 'a wrong PIN signs nobody out');

    await typeIn(t, labelled('Your chat PIN'), '4321');
    await press(t, 'Confirm with my PIN');
    expect(auth.transport.token, 'tok-b');
  });

  testWidgets('the email link: "We sent a link to …", then back and checked', (t) async {
    phone(t);
    final server = FakeServer();
    var proved = false;
    server.answers['GET /v1/me'] = () {
      final m = personalMe(businesses: [_business]);
      (m['second_proof'] as Map)['device_proved'] = proved;
      return _json(m);
    };
    server.answers['POST /v1/me/switch'] = () => proved
        ? _json({'token': 'tok-b', 'tenant_id': 'b1', 'user_id': 'u1'})
        : _json({'error': 'Confirm it is you first.', 'second_proof_required': true, 'ways': {'email': true}}, 401);
    server.answers['POST /v1/auth/second-proof/email/start'] = () => _json({'sent_to': 't***@example.com', 'expires_in': 900}, 202);
    final auth = _auth(server);
    auth.transport.token = 'tok-p';
    auth.me = Me.fromJson(personalMe(businesses: [_business]));
    auth.state = AuthState.signedIn;
    await t.pumpWidget(TaskiemApp(home: Builder(builder: (c) => GestureDetector(onTap: () => showSwitcher(c, auth), child: const Text('open')))));
    await t.tap(find.text('open'));
    await settle(t);
    await press(t, 'Mama’s Kitchen');
    await press(t, 'Email me a link');
    expect(find.text('We sent a link to t***@example.com. Open it on this phone within 15 minutes.'), findsOneWidget);
    await press(t, 'I’ve opened the link');
    expect(find.textContaining('The link has not been opened yet'), findsOneWidget);
    proved = true;
    await press(t, 'I’ve opened the link');
    expect(auth.transport.token, 'tok-b');
  });

  testWidgets('a passkey rule: Use your passkey; back to You from a business', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json({'tenant_id': 'b1', 'tenant_name': 'Mama’s Kitchen', 'auth_method': 'phone', 'permissions': <String>[], 'roles': ['owner']});
    server.answers['POST /v1/me/switch'] = () => _json({'token': 'tok-you', 'personal': true, 'tenant_id': null});
    final auth = _auth(server);
    auth.transport.token = 'tok-b';
    auth.me = Me.fromJson({'tenant_id': 'b1', 'auth_method': 'phone', 'permissions': <String>[], 'businesses': [{..._business, 'current': true, 'locked': false, 'lock': null}]});
    auth.state = AuthState.signedIn;
    await t.pumpWidget(TaskiemApp(home: SwitcherSheet(auth: auth, businesses: [BusinessEntry.fromJson({..._business, 'current': true, 'locked': false, 'lock': null})])));
    await settle(t);
    expect(find.text('Open now'), findsOneWidget);
    await press(t, 'You');
    expect(server.calls.firstWhere((c) => c.path == '/v1/me/switch').body, {'personal': true});
    expect(auth.transport.token, 'tok-you');
  });

  testWidgets('Start something: workflows with no name, or a business with its name and what it does', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json({'tenant_id': 'w9', 'tenant_kind': 'workflows', 'auth_method': 'phone', 'permissions': <String>[]});
    server.answers['POST /v1/me/start'] = () => _json({'tenant_id': 'w9', 'kind': 'workflows', 'name': 'Tolu’s workflows', 'user_id': 'u1', 'token': 'tok-w'}, 201);
    final auth = _auth(server);
    auth.transport.token = 'tok-p';
    auth.me = Me.fromJson(personalMe());
    auth.state = AuthState.signedIn;
    await t.pumpWidget(TaskiemApp(home: StartSomethingScreen(auth: auth)));
    await settle(t);
    expect(find.text('What do you want to start?'), findsOneWidget);
    expect(find.text('A business'), findsOneWidget);
    expect(find.text('Workflows'), findsOneWidget);

    await press(t, 'A business');
    await press(t, 'Start the business');
    expect(find.text('Give your business a name.'), findsOneWidget);
    await typeIn(t, find.byKey(const ValueKey('business-name')), 'Tolu Prints');
    await press(t, 'Start the business');
    expect(find.text('Choose what your business does.'), findsOneWidget);
    await snap(t, 'start-business');
    server.answers['POST /v1/me/start'] = () => _json({'error': 'Five a day. Try again tomorrow.'}, 429);
    await press(t, 'I sell things');
    await press(t, 'Start the business');
    expect(server.calls.last.body, {'kind': 'business', 'kinds': ['shop'], 'name': 'Tolu Prints'});
    expect(find.text('Five a day. Try again tomorrow.'), findsOneWidget);

    server.answers['POST /v1/me/start'] = () => _json({'tenant_id': 'w9', 'kind': 'workflows', 'name': 'Tolu’s workflows', 'user_id': 'u1', 'token': 'tok-w'}, 201);
    await press(t, 'Workflows');
    expect(find.text('Left empty, it\'s called “Tolu Ade\'s workflows”. You can rename it later.'), findsOneWidget);
    await typeIn(t, find.byKey(const ValueKey('workflows-name')), '');
    await press(t, 'Start workflows');
    expect(server.calls.lastWhere((c) => c.path == '/v1/me/start').body, {'kind': 'workflows'});
    expect(auth.transport.token, 'tok-w');
    expect(auth.me?.tenantKind, 'workflows');
  });

  testWidgets('You: invitations waiting, joined; Settings with the phone and Change number', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json(personalMe());
    server.answers['GET /v1/me/invitations'] = () => _json({
      'invitations': [
        {'tenant_id': 'b2', 'tenant': 'Bola Stores', 'roles': ['staff'], 'invited_at': '2026-11-15T10:00:00Z'},
      ],
    });
    server.answers['POST /v1/me/invitations/b2/accept'] = () => _json({'tenant_id': 'b2', 'roles': ['staff'], 'token': 'tok-b2', 'personal': false});
    final auth = _auth(server);
    auth.transport.token = 'tok-p';
    auth.me = Me.fromJson(personalMe());
    auth.state = AuthState.signedIn;
    await t.pumpWidget(TaskiemApp(home: YouHome(auth: auth)));
    await settle(t);
    expect(find.text('Invitations'), findsOneWidget);
    expect(find.text('Bola Stores'), findsOneWidget);
    expect(find.text('Invited you as staff'), findsOneWidget);
    await snap(t, 'you-invited');

    // Settings: name, phone, Change number.
    await t.tap(find.bySemanticsLabel('Settings'));
    await settle(t);
    expect(find.text('Tolu Ade'), findsOneWidget);
    expect(find.text('+234 803 123 0000'), findsOneWidget);
    expect(find.text('Sign out'), findsOneWidget);
    expect(find.textContaining('phone.taskiem.invalid'), findsNothing, reason: 'a made-up sign-in name is not shown');

    server.answers['POST /v1/me/phone/start'] = () => _json({'sent_via': 'sms', 'old_sent_via': 'whatsapp', 'expires_in': 600, 'needs_old_code': true}, 202);
    server.answers['POST /v1/me/phone/verify'] = () => _json({'phone': '+2348039990000'});
    await press(t, 'Change number');
    await typeIn(t, find.byKey(const ValueKey('new-phone')), '08039990000');
    await press(t, 'Send a code');
    final start = server.calls.lastWhere((c) => c.path == '/v1/me/phone/start').body!;
    expect(start, {'phone': '+2348039990000'}, reason: 'asked first, not guessed');
    // The server says the old number is needed: asked, as on the web.
    expect(find.text('To keep your account safe, we also check it\'s you. Do you still have your old number, +234 803 123 0000?'), findsOneWidget);
    expect(find.text('No, prove it\'s me another way'), findsOneWidget);
    await press(t, 'Yes, send it a code');
    expect(server.calls.lastWhere((c) => c.path == '/v1/me/phone/start').body, {'phone': '+2348039990000', 'confirm_old': true});
    expect(find.text('We sent a 6-digit code by SMS to +234 803 999 0000. And one to your old number, +234 803 123 0000.'), findsOneWidget);
    await snap(t, 'change-number');
    // A wrong code: said, and nobody is signed out.
    server.answers['POST /v1/me/phone/verify'] = () => _json({'error': 'That code is not right.', 'code_wrong': true}, 401);
    await typeIn(t, find.byKey(const ValueKey('new-code')), '111111');
    await typeIn(t, find.byKey(const ValueKey('old-code')), '333333');
    await t.tap(find.text('Confirm the new number').last);
    await settle(t);
    expect(find.text('That code is not right. Check it, or ask for a new one.'), findsOneWidget);
    expect(auth.state, AuthState.signedIn, reason: 'a wrong code signs nobody out');
    expect(auth.transport.token, 'tok-p');
    server.answers['POST /v1/me/phone/verify'] = () => _json({'phone': '+2348039990000'});
    await typeIn(t, find.byKey(const ValueKey('new-code')), '222222');
    await t.tap(find.text('Confirm the new number').last);
    await settle(t);
    expect(server.calls.lastWhere((c) => c.path == '/v1/me/phone/verify').body, {'code': '222222', 'old_code': '333333', 'phone': '+2348039990000'});
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  testWidgets('joining an invitation moves into the business', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json({'tenant_id': 'b2', 'tenant_name': 'Bola Stores', 'auth_method': 'phone', 'permissions': <String>[]});
    server.answers['GET /v1/me/invitations'] = () => _json({
      'invitations': [
        {'tenant_id': 'b2', 'tenant': 'Bola Stores', 'roles': ['staff'], 'invited_at': '2026-11-15T10:00:00Z'},
      ],
    });
    server.answers['POST /v1/me/invitations/b2/accept'] = () => _json({'tenant_id': 'b2', 'roles': ['staff'], 'token': 'tok-b2', 'personal': false});
    final auth = _auth(server);
    auth.transport.token = 'tok-p';
    auth.me = Me.fromJson(personalMe());
    auth.state = AuthState.signedIn;
    await t.pumpWidget(TaskiemApp(home: YouHome(auth: auth)));
    await settle(t);
    await press(t, 'Join');
    expect(auth.transport.token, 'tok-b2');
    expect(auth.me?.tenantName, 'Bola Stores');
  });

  test('the email’s link to confirm a phone opens in the app', () {
    expect(appPageOf(Uri.parse('https://taskiem.com/second-proof#token=abcdefghij0123'), server: 'https://taskiem.com'), '/second-proof?token=abcdefghij0123');
    expect(appPageOf(Uri.parse('https://taskiem.com/second-proof'), server: 'https://taskiem.com'), isNull);
    expect(appPageOf(Uri.parse('https://evil.example/second-proof#token=abcdefghij0123'), server: 'https://taskiem.com'), isNull);
  });

  test('a business asking for a step on switching signs nobody out; an ended session does', () async {
    final server = FakeServer();
    server.answers['POST /v1/me/switch'] = () => _json({'error': 'Use your passkey.', 'passkey_required': true}, 401);
    server.answers['GET /v1/me'] = () => _json({'error': 'unauthenticated'}, 401);
    final auth = _auth(server);
    auth.transport.token = 'tok';
    auth.state = AuthState.signedIn;
    var ended = 0;
    auth.transport.onSessionEnded = () => ended++;
    await expectLater(auth.switchTo('b1'), throwsA(isA<ApiError>()));
    expect(ended, 0);
    await expectLater(auth.api.me(), throwsA(isA<ApiError>()));
    expect(ended, 1);
  });

  test('a wrong or expired code, or a wrong PIN, signs nobody out; an ended session does', () async {
    final server = FakeServer();
    final auth = _auth(server);
    auth.transport.token = 'tok';
    auth.state = AuthState.signedIn;
    var ended = 0;
    auth.transport.onSessionEnded = () => ended++;
    for (final flag in [{'code_wrong': true}, {'ticket_expired': true}, {'tries_left': 2}]) {
      server.answers['POST /v1/me/phone/verify'] = () => _json({'error': 'no', ...flag}, 401);
      await expectLater(auth.api.phoneChangeVerify(body: const PhoneChangeVerifyRequest(code: '000000')), throwsA(isA<ApiError>()));
    }
    expect(ended, 0);
    server.answers['POST /v1/me/phone/verify'] = () => _json({'error': 'the session has ended'}, 401);
    await expectLater(auth.api.phoneChangeVerify(body: const PhoneChangeVerifyRequest(code: '000000')), throwsA(isA<ApiError>()));
    expect(ended, 1);
  });

  testWidgets('Change number: no old number, so another proof (the PIN), then only the new number’s code', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json(personalMe(ways: {'pin': true}));
    server.answers['POST /v1/me/phone/start'] = () => _json({'sent_via': 'whatsapp', 'expires_in': 600, 'needs_old_code': true}, 202);
    server.answers['POST /v1/auth/second-proof/pin'] = () => _json({'device_proved': true});
    server.answers['POST /v1/me/phone/verify'] = () => _json({'error': 'That code has expired.', 'ticket_expired': true}, 401);
    final auth = _auth(server);
    auth.transport.token = 'tok-p';
    auth.me = Me.fromJson(personalMe(ways: {'pin': true}));
    auth.state = AuthState.signedIn;
    await t.pumpWidget(TaskiemApp(home: ChangeNumberScreen(auth: auth)));
    await settle(t);
    await typeIn(t, find.byKey(const ValueKey('new-phone')), '+44 7700 900123');
    await press(t, 'Send a code');
    expect(server.calls.last.body, {'phone': '+447700900123'});
    await press(t, 'No, prove it\'s me another way');
    expect(find.text('Prove it\'s you'), findsOneWidget);
    await typeIn(t, labelled('Your chat PIN'), '4321');
    await press(t, 'Confirm with my PIN');
    expect(server.calls.any((c) => c.path == '/v1/auth/second-proof/pin'), isTrue);
    expect(find.text('Code sent to your new number'), findsOneWidget);
    expect(find.text('Code sent to your old number'), findsNothing);
    // The code ran out: back to the number, saying so; still signed in.
    await typeIn(t, find.byKey(const ValueKey('new-code')), '222222');
    await t.tap(find.text('Confirm the new number').last);
    await settle(t);
    expect(server.calls.last.body, {'code': '222222', 'phone': '+447700900123'});
    expect(find.text('That code has expired: ask for a new one.'), findsOneWidget);
    expect(find.text('New phone number'), findsOneWidget);
    expect(auth.state, AuthState.signedIn);
  });

  testWidgets('a passkey as a second proof proves the phone, keeps the session, then switches', (t) async {
    phone(t);
    final was = passkeys;
    final fake = FakePasskeys();
    passkeys = fake;
    addTearDown(() => passkeys = was);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json(personalMe(businesses: [_business], ways: {'passkey': true}));
    var proved = false;
    server.answers['POST /v1/me/switch'] = () => proved
        ? _json({'token': 'tok-b', 'tenant_id': 'b1', 'user_id': 'u1'})
        : _json({'error': 'Confirm it is you first.', 'second_proof_required': true, 'ways': {'passkey': true}}, 401);
    server.answers['POST /v1/auth/second-proof/passkey/options'] = () => _json({'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.example'}});
    server.answers['POST /v1/auth/second-proof/passkey'] = () {
      proved = true;
      return _json({'device_proved': true});
    };
    final auth = _auth(server);
    auth.transport.token = 'tok-p';
    auth.me = Me.fromJson(personalMe(businesses: [_business], ways: {'passkey': true}));
    auth.state = AuthState.signedIn;
    await t.pumpWidget(TaskiemApp(home: Builder(builder: (c) => GestureDetector(onTap: () => showSwitcher(c, auth), child: const Text('open')))));
    await t.tap(find.text('open'));
    await settle(t);
    await press(t, 'Mama’s Kitchen');
    await press(t, 'Use your passkey');
    final proof = server.calls.firstWhere((c) => c.path == '/v1/auth/second-proof/passkey');
    expect((proof.body!['credential'] as Map)['id'], 'k1');
    expect(proof.auth, 'Bearer tok-p', reason: 'the same session, proved');
    expect(server.calls.where((c) => c.path == '/v1/auth/passkey'), isEmpty, reason: 'no new sign-in with the passkey');
    expect(auth.transport.token, 'tok-b');
  });

  testWidgets('an invitation whose business asks for a second proof: the proof, then the business', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json(personalMe(businesses: [_business]));
    server.answers['GET /v1/me/invitations'] = () => _json({
      'invitations': [
        {'tenant_id': 'b2', 'tenant': 'Bola Stores', 'roles': ['staff'], 'invited_at': '2026-11-15T10:00:00Z'},
      ],
    });
    server.answers['POST /v1/me/invitations/b2/accept'] = () => _json({'tenant_id': 'b2', 'roles': ['staff'], 'sign_in': 'second_proof_required', 'ways': {'pin': true}});
    server.answers['POST /v1/auth/second-proof/pin'] = () => _json({'device_proved': true});
    server.answers['POST /v1/me/switch'] = () => _json({'token': 'tok-b2', 'tenant_id': 'b2', 'user_id': 'u1'});
    final auth = _auth(server);
    auth.transport.token = 'tok-p';
    auth.me = Me.fromJson(personalMe(businesses: [_business]));
    auth.state = AuthState.signedIn;
    await t.pumpWidget(TaskiemApp(home: YouHome(auth: auth)));
    await settle(t);
    // A new phone: the businesses wait for a proof, and they are listed.
    expect(find.text('You signed in on a new device, so your businesses are locked until you prove it\'s you.'), findsOneWidget);
    expect(find.text('Your businesses'), findsOneWidget);
    await press(t, 'Join');
    expect(auth.transport.token, 'tok-p', reason: 'not moved before the proof');
    expect(find.text('Prove it\'s you'), findsWidgets);
    expect(find.textContaining('Before Bola Stores opens'), findsOneWidget);
    await typeIn(t, labelled('Your chat PIN'), '4321');
    await press(t, 'Confirm with my PIN');
    expect(server.calls.lastWhere((c) => c.path == '/v1/me/switch').body, {'tenant_id': 'b2'});
    expect(auth.transport.token, 'tok-b2');
  });

  testWidgets('a personal session opened offline opens You, not a business', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json(personalMe());
    server.answers['POST /v1/auth/chat/verify'] = () => _json({'token': 'tok-c', 'personal': true, 'tenant_id': null});
    final keys = MemoryKeyStore();
    final first = _auth(server, keys: keys);
    // Signed in with the chat code: a personal session (C4).
    await first.signInWithCode('+2348031230000', '123456');
    expect(first.personal, isTrue);
    // Opened again with no network: what was kept says You.
    server.offline = true;
    final auth = _auth(server, keys: keys);
    await auth.start();
    expect(auth.state, AuthState.signedIn);
    expect(auth.me, isNull);
    expect(auth.personal, isTrue);
    await t.pumpWidget(TaskiemApp(auth: auth));
    await settle(t);
    expect(find.byType(YouHome), findsOneWidget);
    // Signed out, nothing of it stays.
    await auth.signOut();
    expect(auth.personal, isFalse);
    expect(await keys.read('personal'), isNull);
  });

  testWidgets('the way in: a code that ran out while the name was typed goes back to the number', (t) async {
    phone(t);
    final server = FakeServer();
    server.answers['POST /v1/auth/phone/create'] = () => _json({'error': 'This sign-up has expired. Ask for a new code.', 'ticket_expired': true}, 401);
    server.answers['POST /v1/auth/phone/start'] = () => _json({'sent_via': 'whatsapp', 'expires_in': 600}, 202);
    final auth = _auth(server)..state = AuthState.signedOut;
    await t.pumpWidget(TaskiemApp(home: WelcomeScreen(auth: auth, step: WelcomeStep.code, phone: '+2348031230000')));
    await settle(t);
    expect(find.text('We sent a 6-digit code to WhatsApp on +234 803 123 0000.'), findsOneWidget);
    server.answers['POST /v1/auth/phone/verify'] = () => _json({'status': 'new', 'ticket': 'tk-1', 'expires_in': 900});
    await typeIn(t, find.byKey(const ValueKey('code')), '123 456');
    expect(find.text('123456'), findsOneWidget, reason: 'a pasted "123 456" is six digits');
    await press(t, 'Continue');
    expect(find.text('Make your account'), findsOneWidget);
    expect(find.text('Use another number'), findsOneWidget);
    await typeIn(t, find.byKey(const ValueKey('name')), 'Tolu Ade');
    await press(t, 'I\'m 18 or over');
    await t.tap(find.textContaining('I agree to Taskiem\'s'));
    await settle(t);
    await press(t, 'Create');
    expect(find.text('Your code has expired: ask for a new one.'), findsOneWidget);
    expect(find.text('Welcome to Taskiem'), findsOneWidget);
  });
}
