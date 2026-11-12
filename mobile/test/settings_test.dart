// The app's settings (docs/spec/mobile-app.md §3.6; S1–S5 of
// docs/spec/mobile-app-m3-fixes.md), as a person uses them: who they are
// and adding an email, the app lock, chat tools, and the chats they link
// (WhatsApp with its approval PIN, Telegram, Slack).

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/links.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/signed_in.dart';
import 'package:taskiem/theme/theme.dart';

import 'lock_test.dart' show FakeUnlocker;
import 'support.dart';

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

void main() {
  setUpAll(loadFonts);

  /// Settings, signed in as the chats fixture's person with [factors] and
  /// [user]; [answers] by "METHOD path"; every request kept.
  Future<({List<(String, String, Object?)> asked, Prefs prefs, FakeUnlocker u, Auth auth})> open(
    WidgetTester t, {
    Map<String, bool> factors = const {'totp': true, 'password': true},
    Map<String, dynamic>? user,
    Map<String, http.Response Function(Object? body)> answers = const {},
  }) async {
    t.view.physicalSize = const Size(390, 2400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final asked = <(String, String, Object?)>[];
    final auth = f.auth(also: (req) async {
      final key = '${req.method} ${req.url.path}';
      final body = req.body.isEmpty ? null : jsonDecode(req.body);
      if (req.url.path.startsWith('/v1/me/') || req.url.path == '/v1/settings/chat-tools') asked.add((req.method, req.url.path, body));
      final a = answers[key];
      if (a != null) return a(body);
      if (key == 'GET /v1/me/whatsapp' || key == 'GET /v1/me/telegram' || key == 'GET /v1/me/slack') {
        return _json({'enabled': false, 'linked': false, 'pending': false, 'flows': false, 'pin': {'set': false}});
      }
      if (key == 'GET /v1/settings/chat-tools') return _json({'chat_tools': true});
      if (key == 'GET /v1/devices') return _json({'devices': <Object>[]});
      if (key == 'GET /v1/me/businesses') return _json({'businesses': <Object>[]});
      return null;
    });
    final me = f.json('/v1/me');
    me['factors'] = factors;
    if (user != null) me['user'] = user;
    auth.me = Me.fromJson(me);
    final prefs = Prefs(MemoryKeyStore());
    await prefs.load();
    final u = FakeUnlocker();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SignedInScreen(auth: auth, prefs: prefs, unlocker: u)));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    return (asked: asked, prefs: prefs, u: u, auth: auth);
  }

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
  }

  Future<void> field(WidgetTester t, String label, String text) async {
    final box = find.descendant(of: find.ancestor(of: find.text(label), matching: find.byType(Column)).first, matching: find.byType(EditableText));
    await t.ensureVisible(box);
    await t.enterText(box, text);
    await t.pump();
  }

  Future<void> press(WidgetTester t, String label) async {
    final b = find.bySemanticsLabel(label).first;
    await t.ensureVisible(b);
    await t.pump();
    await t.tap(b);
    await settle(t);
  }

  testWidgets('S3: who is signed in; an account made in a chat adds its email', (t) async {
    final o = await open(
      t,
      user: {'id': 'u1', 'name': 'Ada Obi', 'email': 'owner+2348031239001@phone.taskiem.invalid', 'email_placeholder': true},
      answers: {'POST /v1/me/email': (_) => _json({'status': 'sent'})},
    );
    expect(find.text('Ada Obi'), findsOneWidget);
    expect(find.text('owner+2348031239001@phone.taskiem.invalid'), findsNothing, reason: 'a made-up sign-in name is not shown');
    await field(t, 'Email', 'ada@kitchen.ng');
    await press(t, 'Send the link');
    final sent = o.asked.where((a) => a.$1 == 'POST').single;
    expect((sent.$1, sent.$2), ('POST', '/v1/me/email'));
    expect(sent.$3, {'email': 'ada@kitchen.ng'});
    expect(find.text('A link is on its way: open it to confirm'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  testWidgets('S1: the app lock is turned off only by the phone\'s own unlock', (t) async {
    final o = await open(t);
    expect(o.prefs.lock, isTrue);
    final row = find.text('Ask for this phone’s fingerprint, face or PIN');
    o.u.answer = false;
    await t.tap(row);
    await settle(t);
    expect(o.prefs.lock, isTrue, reason: 'not unlocked: still on');
    o.u.answer = true;
    await t.tap(row);
    await settle(t);
    expect(o.prefs.lock, isFalse);
    expect(o.u.asked, 2);
    await t.tap(row);
    await settle(t);
    expect(o.prefs.lock, isTrue, reason: 'on again without asking');
    expect(o.u.asked, 2);
  });

  testWidgets('S5: chat tools switched for everyone; refused, it goes back', (t) async {
    var refuse = false;
    final o = await open(
      t,
      answers: {'PUT /v1/settings/chat-tools': (b) => refuse ? _json({'error': 'Only owners and admins can change this.'}, 403) : _json({'chat_tools': (b as Map)['chat_tools']})},
    );
    final row = find.text('Name a business to message it');
    await t.ensureVisible(row);
    await t.tap(row);
    await settle(t);
    expect((o.asked.lastWhere((a) => a.$1 != 'GET').$1, o.asked.lastWhere((a) => a.$1 != 'GET').$2), ('PUT', '/v1/settings/chat-tools'));
    expect(o.asked.lastWhere((a) => a.$1 != 'GET').$3, {'chat_tools': false});
    refuse = true;
    await t.tap(row);
    await settle(t);
    expect(find.text('Only owners and admins can change this.'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('Name a business')), findsWidgets);
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  testWidgets('S4, S2: WhatsApp linked by a code, with the authenticator code; then its approval PIN', (t) async {
    var linked = false;
    var pinSet = false;
    final o = await open(
      t,
      answers: {
        'GET /v1/me/whatsapp': (_) => _json({
          'enabled': true,
          'flows': true,
          'platform_number': '+234 700 TASKIEM',
          'number': linked ? '+2348031239001' : null,
          'verified_at': linked ? '2026-11-11T10:00:00Z' : null,
          'pin': {'set': pinSet},
        }),
        'POST /v1/me/whatsapp': (_) => _json({'number': '+2348031239001', 'expires_at': '2026-11-11T10:10:00Z'}),
        'POST /v1/me/whatsapp/verify': (_) {
          linked = true;
          return _json({'number': '+2348031239001'});
        },
        'PUT /v1/me/whatsapp/pin': (_) {
          pinSet = true;
          return _json({'set': true});
        },
      },
    );
    await field(t, 'Your WhatsApp number', '+2348031239001');
    await field(t, 'Authenticator code, to link a number', '123456');
    await press(t, 'Send a code');
    expect((o.asked.lastWhere((a) => a.$1 != 'GET').$1, o.asked.lastWhere((a) => a.$1 != 'GET').$2), ('POST', '/v1/me/whatsapp'));
    expect(o.asked.lastWhere((a) => a.$1 != 'GET').$3, {'number': '+2348031239001', 'totp': '123456'});
    await field(t, 'Code sent to your number', '654321');
    await press(t, 'Link');
    expect(o.asked.where((a) => a.$2 == '/v1/me/whatsapp/verify').single.$3, {'code': '654321'});
    expect(find.textContaining('Linked: +2348031239001'), findsOneWidget);
    expect(find.text('Approval PIN'), findsOneWidget);
    await field(t, 'PIN', '480211');
    await field(t, 'Authenticator code, to set the PIN', '111222');
    await press(t, 'Set PIN');
    expect((o.asked.lastWhere((a) => a.$1 != 'GET').$1, o.asked.lastWhere((a) => a.$1 != 'GET').$2), ('PUT', '/v1/me/whatsapp/pin'));
    expect(o.asked.lastWhere((a) => a.$1 != 'GET').$3, {'pin': '480211', 'totp': '111222'});
    expect(find.textContaining('PIN set'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await t.pumpAndSettle();
  });

  testWidgets('S4: Telegram\'s one-time link opens Telegram; Slack waits for its Confirm', (t) async {
    final opened = <Uri>[];
    final was = openOutside;
    openOutside = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openOutside = was);
    var slackPending = false;
    final o = await open(
      t,
      answers: {
        'GET /v1/me/telegram': (_) => _json({'enabled': true, 'linked': false, 'bot_username': 'TaskiemBot'}),
        'POST /v1/me/telegram': (_) => _json({'url': 'https://t.me/TaskiemBot?start=abc', 'expires_at': '2026-11-11T10:10:00Z'}),
        'GET /v1/me/slack': (_) => _json({'enabled': true, 'linked': false, 'pending': slackPending}),
        'POST /v1/me/slack': (_) {
          slackPending = true;
          return _json({'enabled': true, 'linked': false, 'pending': true});
        },
      },
    );
    await field(t, 'Authenticator code, to link Telegram', '123456');
    await press(t, 'Link Telegram');
    expect((o.asked.lastWhere((a) => a.$1 != 'GET').$1, o.asked.lastWhere((a) => a.$1 != 'GET').$2), ('POST', '/v1/me/telegram'));
    expect(o.asked.lastWhere((a) => a.$1 != 'GET').$3, {'totp': '123456'});
    await press(t, 'Open Telegram');
    expect(opened, [Uri.parse('https://t.me/TaskiemBot?start=abc')]);
    await field(t, 'Authenticator code, to link Slack', '654321');
    await press(t, 'Link Slack');
    expect((o.asked.lastWhere((a) => a.$1 != 'GET').$1, o.asked.lastWhere((a) => a.$1 != 'GET').$2), ('POST', '/v1/me/slack'));
    expect(o.asked.lastWhere((a) => a.$1 != 'GET').$3, {'totp': '654321'});
    expect(find.textContaining('press Confirm in the message'), findsOneWidget);
    expect(find.bySemanticsLabel('Send the Confirm again'), findsOneWidget);
  });

  testWidgets('a passkey alone: linking says to do it where the passkey is', (t) async {
    await open(t, factors: const {'passkey': true}, answers: {'GET /v1/me/telegram': (_) => _json({'enabled': true, 'linked': false})});
    expect(find.textContaining('This needs your passkey, and this phone cannot use one yet.'), findsOneWidget);
    final link = t.getSemantics(find.bySemanticsLabel('Link Telegram'));
    expect(link.flagsCollection.isEnabled, isNot(true), reason: 'nothing to press with');
  });
}
