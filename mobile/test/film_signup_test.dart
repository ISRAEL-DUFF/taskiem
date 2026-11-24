// A film: open your shop (video/recording-app.md). Zainab signs up in the app
// with her WhatsApp number, as the app's sign-up conversation goes: the code,
// the terms, a business, its name, her name, what she sells, how customers pay.
// The words are the server's own English (engine/lang/messages/en.json,
// signup.*). Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_signup_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/open-your-shop --out signup.mp4 --captions

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/app_sign_up.dart';
import 'package:taskiem/theme/theme.dart';

import 'auth_test.dart' show FakeServer;
import 'film.dart';
import 'support.dart' show loadFonts;

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json; charset=utf-8'});

Map<String, dynamic> _q(String text, [List<(String, String)> buttons = const []]) => {
  'text': text,
  if (buttons.isNotEmpty) 'buttons': [for (final b in buttons) {'id': b.$1, 'title': b.$2}],
};

Auth _world() {
  final server = FakeServer();
  server.answers['GET /v1/me'] = () => _json({'tenant_id': 't1', 'tenant_name': 'Zuri Atelier', 'auth_method': 'chat', 'permissions': <String>[], 'roles': ['owner']});
  server.answers['POST /v1/signup/app/code'] = () => _json({'status': 'We sent a code to +2348031230142 on WhatsApp.'}, 202);
  server.answers['POST /v1/signup/app/verify'] = () => _json({
    'token': 'su-tok',
    'messages': [
      _q(
        "Taskiem keeps your business's records and the details you give it. Read how, and the terms: taskiem.ng/terms\n\nReply *Yes* to agree and start.",
        [('su:yes', 'Yes'), ('su:no', 'No')],
      ),
    ],
  });
  final turns = [
    {
      'messages': [
        _q(
          'Is this for a business, or for workflows?\n\n*A business*: sell, take orders and bookings, send invoices, with your staff.\n*Workflows*: get work done for you or your team: forms, reminders, approvals, your other apps.',
          [('su:kind:business', 'A business'), ('su:kind:workflows', 'Workflows')],
        ),
      ],
    },
    {'messages': [_q('What is your business called? (Send *cancel* to stop at any point.)')]},
    {'messages': [_q('And your name?')]},
    {
      'messages': [
        _q('What do you sell or do? Reply with a number:\n1. Food\n2. Provisions\n3. Fashion\n4. Phones and gadgets\n5. School\n6. Salon\n7. Services\n8. Deliveries (I am a rider)\n9. Something else'),
      ],
    },
    {
      'messages': [
        _q('How do customers pay you? Reply with the numbers that fit, like 1 2 3:\n1. Cash\n2. Bank transfer\n3. POS\n4. Paystack\n5. Moniepoint\n6. OPay'),
      ],
    },
    {
      'messages': [_q('Done: *Zuri Atelier* is on Taskiem, free for 14 days.')],
      'done': true,
      'token': 'tok-new',
      'tenant_id': 't1',
    },
  ];
  server.answers['POST /v1/signup/app/turn'] = () => _json(turns.removeAt(0));
  return Auth(
    transport: Transport(base: 'http://localhost', client: server.client()),
    store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Zainab’s phone', platform: 'android')),
  );
}

void main() {
  setUpAll(loadFonts);

  testWidgets('film: open your shop', (t) async {
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: AppSignUpScreen(auth: _world())));
    final f = Film.start(t, 'open-your-shop');
    final field = find.byType(EditableText);

    f.say('Zainab runs Zuri Atelier. She opens Taskiem and signs up with her WhatsApp number.', who: 'Zainab · Zuri Atelier');
    await f.play(2.6);
    await f.tap(field.first, then: 0.2);
    await f.keyboard(true);
    await f.type(field.first, '+2348031230142', perKey: 0.12);
    await f.hold(0.4);
    await f.keyboard(false);
    f.say('Tap Send me a code.');
    await f.tap(find.text('Send me a code'), then: 1.4);

    f.say('Taskiem sends a code to her WhatsApp. She types it in.');
    await f.tap(field.first, then: 0.2);
    await f.keyboard(true);
    await f.type(field.first, '123456', perKey: 0.18);
    await f.hold(0.3);
    await f.keyboard(false);
    await f.tap(find.text('Continue'), then: 1.8);

    f.say('Taskiem asks first: do you agree to the terms?');
    await f.hold(2.2);
    await f.tap(find.text('Yes'), then: 1.6);

    f.say('A business, or workflows? Zainab runs a business.');
    await f.hold(1.8);
    await f.tap(find.text('A business'), then: 1.4);

    f.say('What is your business called?');
    await f.hold(0.6);
    await f.tap(find.byType(EditableText).last, then: 0.2);
    await f.keyboard(true, kind: 'text');
    await f.type(find.byType(EditableText).last, 'Zuri Atelier', perKey: 0.13);
    await f.hold(0.3);
    await f.tap(find.bySemanticsLabel('Send'), then: 1.4);

    f.say('And her own name.');
    await f.type(find.byType(EditableText).last, 'Zainab Yusuf', perKey: 0.13);
    await f.hold(0.3);
    await f.tap(find.bySemanticsLabel('Send'), then: 1.4);

    f.say('What does she sell? Fashion is number 3.');
    await f.hold(1.6);
    await f.keyboard(true, kind: 'number', swap: true);
    await f.type(find.byType(EditableText).last, '3', perKey: 0.3);
    await f.hold(0.3);
    await f.tap(find.bySemanticsLabel('Send'), then: 1.4);

    f.say('How do customers pay her? Cash and transfer: 1 and 2.');
    await f.hold(1.6);
    await f.type(find.byType(EditableText).last, '1 2', perKey: 0.28);
    await f.hold(0.3);
    await f.tap(find.bySemanticsLabel('Send'), then: 1.0);
    await f.keyboard(false);

    f.say('Done. Zuri Atelier is on Taskiem, free for 14 days.');
    await f.hold(3.0);
    await f.finish();
  }, skip: !filming);
}
