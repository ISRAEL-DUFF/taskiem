// A film: Taskiem for you, episode 1, sign up with your phone. Tobi Adewale opens
// the app (its first screen is the phone way in), types his number, the 6-digit
// code, his name, ticks "I'm 18 or over" and the terms, taps Create, and lands on
// You (Chats, Orders, Discover). Set-up copied from everyone_test.dart ("a new
// number: the code, then a name, 18 or over and the terms; then You"). The code
// WhatsApp brings is a stand-in (123456). Skipped unless VIDEO_DIR is set.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'auth_test.dart' show FakeServer;
import 'film.dart';
import 'support.dart' show loadFonts;

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

Map<String, dynamic> _me() => {
  'tenant_id': null,
  'personal': true,
  'roles': <String>[],
  'permissions': <String>[],
  'auth_method': 'phone',
  'user': {'id': 'u1', 'name': 'Tobi Adewale', 'email': 'person+x@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031234567'},
  'businesses': <Object>[],
  'second_proof': {'required': false, 'device_proved': false, 'ways': {'passkey': false, 'email': true, 'pin': true}},
};

void main() {
  setUpAll(loadFonts);

  testWidgets('film: sign up with your phone', (t) async {
    final server = FakeServer();
    server.answers['GET /v1/me'] = () => _json(_me());
    server.answers['GET /v1/me/invitations'] = () => _json({'invitations': <Object>[]});
    server.answers['GET /v1/me/chats'] = () => _json({'chats': <Object>[], 'unread': 0});
    server.answers['POST /v1/auth/phone/start'] = () => _json({'sent_via': 'whatsapp', 'expires_in': 600}, 202);
    server.answers['POST /v1/auth/phone/verify'] = () => _json({'status': 'new', 'ticket': 'tk-1', 'expires_in': 900});
    server.answers['POST /v1/auth/phone/create'] = () => _json({'status': 'created', 'token': 'tok-p', 'personal': true, 'tenant_id': null, 'user_id': 'u1'}, 201);
    final auth = Auth(
      transport: Transport(base: 'http://localhost', client: server.client()),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Tobi’s Tecno', platform: 'android')),
    )..state = AuthState.signedOut;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: auth));
    final f = Film.start(t, 'you-sign-up');
    await f.settle();
    final phone = find.descendant(of: find.byKey(const ValueKey('phone')), matching: find.byType(EditableText));
    final code = find.descendant(of: find.byKey(const ValueKey('code')), matching: find.byType(EditableText));
    final name = find.descendant(of: find.byKey(const ValueKey('name')), matching: find.byType(EditableText));

    f.say('Tobi opens Taskiem. The first screen asks for his phone number.', who: 'Tobi · Lagos');
    await f.play(3.0);
    f.say('He taps the number box and types his number.');
    await f.tap(phone, then: 0.2);
    await f.keyboard(true);
    await f.type(phone, '08031234567', perKey: 0.14);
    await f.hold(0.5);
    await f.keyboard(false);
    await f.hold(0.5);
    f.say('He taps Continue. Taskiem sends a code to his WhatsApp.');
    await f.tap(find.text('Continue'), then: 0.8);
    await f.settle();
    await f.hold(2.4);

    f.say('He types the six digits of the code.');
    await f.tap(code, then: 0.2);
    await f.keyboard(true);
    await f.type(code, '123456', perKey: 0.2);
    await f.hold(0.5);
    await f.keyboard(false);
    await f.hold(0.5);
    f.say('He taps Continue.');
    await f.tap(find.text('Continue'), then: 0.8);
    await f.settle();
    await f.hold(1.6);

    f.say('The number is new, so he types his name.');
    await f.tap(name, then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(name, 'Tobi Adewale', perKey: 0.13);
    await f.hold(0.5);
    await f.keyboard(false);
    await f.hold(0.5);
    f.say('He ticks I’m 18 or over, and agrees to the terms.');
    await f.tap(find.text('I\'m 18 or over'), then: 1.0);
    await f.tap(find.textContaining('I agree to Taskiem\'s'), then: 1.4);
    f.say('He taps Create.');
    await f.tap(find.text('Create'), then: 0.8);
    await f.settle();
    await f.hold(1.0);

    expect(find.byType(YouHome), findsOneWidget);
    f.say('His account is ready: You, with Chats, Orders and Discover.');
    await f.hold(4.0);
    await f.finish();
  }, skip: !filming);
}
