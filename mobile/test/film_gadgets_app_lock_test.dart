// A Gadgets film: turn on the app lock (Episode 10, "Keep it safe"; adapted from
// film_food_app_lock_test.dart). Ikenna opens Settings, taps the App lock switch under This
// phone and proves it is him with the phone's own fingerprint check (drawn by
// compose.py); the row says what it will do; the page behind it lists when it
// asks. Then, scene two, Taskiem is opened again after a few minutes away: the
// app's own lock screen, the fingerprint, and Settings as it was. Scene two is
// the same screen put inside an AppLock, as the real app puts every screen: a
// cut between the scenes, not one continuous run.
//
//   VIDEO_DIR=/tmp/films flutter test test/film_gadgets_app_lock_test.dart

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/lock.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/kit/forms.dart' show TkSwitch;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/signed_in.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

/// The phone's fingerprint check takes as long as the film says: it waits on [gate].
class _GateUnlocker implements Unlocker {
  Completer<void>? gate;
  @override
  Future<bool> available() async => true;
  @override
  Future<bool> unlock() async {
    await gate?.future;
    return true;
  }
}

void main() {
  setUpAll(loadFonts);

  testWidgets('film: gadgets, turn on the app lock', (t) async {
    final f = Fixture('chats');
    final auth = f.auth(also: (req) async {
      final key = '${req.method} ${req.url.path}';
      if (key == 'GET /v1/me/whatsapp' || key == 'GET /v1/me/telegram' || key == 'GET /v1/me/slack') {
        return _json({'enabled': false, 'linked': false, 'pending': false, 'flows': false, 'pin': {'set': false}});
      }
      if (key == 'GET /v1/settings/chat-tools') return _json({'chat_tools': true});
      if (key == 'GET /v1/me/devices') return _json({'devices': <Object>[]});
      if (key == 'GET /v1/me/businesses') return _json({'businesses': <Object>[]});
      return null;
    });
    final me = f.json('/v1/me');
    me['factors'] = {'totp': false, 'password': true};
    me['tenant_name'] = 'Techline Gadgets';
    me['user'] = {'id': 'u1', 'name': 'Ikenna Nwachukwu', 'email': 'ikenna@techline.example'};
    auth.me = Me.fromJson(me);
    final prefs = Prefs(MemoryKeyStore());
    await prefs.load();
    final u = _GateUnlocker();
    var clock = DateTime(2026, 11, 11, 9);
    Widget settingsScreen() => SignedInScreen(auth: auth, prefs: prefs, unlocker: u);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: settingsScreen()));
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    await t.pump(const Duration(seconds: 1));

    final film = Film.start(t, 'gadgets-app-lock');
    film.say('Ikenna turns on the app lock to keep Techline Gadgets private.', who: 'Ikenna · Techline Gadgets');
    await film.play(2.6);

    film.say('In Settings, under This phone, he taps the App lock switch.');
    final lockSwitch = find.byWidgetPredicate((w) => w is TkSwitch && w.label == 'App lock');
    await film.drag(find.byType(Scrollable).first, const Offset(0, -200), seconds: 0.8);
    await Scrollable.ensureVisible(t.element(lockSwitch), alignment: 0.3);
    await t.pump();
    await film.hold(1.2);
    final gate = Completer<void>();
    u.gate = gate;
    await film.tap(lockSwitch, then: 0.3);

    film.system('biometric', 2.2, title: 'Unlock Taskiem');
    film.say('The phone asks for his fingerprint first, to prove it is him.');
    await film.play(2.2);
    gate.complete();
    u.gate = null;
    film.say('App lock is on. It asks every time Taskiem opens.');
    await film.play(1.0);
    await film.settle();
    await film.hold(2.4);

    film.say('Tap the row to see when it asks.');
    await film.tap(find.text('App lock').first, then: 1.4);
    await film.settle();
    await film.hold(2.6);
    await film.tap(find.text('Settings').first, then: 0.8);
    await film.settle();

    // Scene two: the same app, inside its lock.
    await t.pumpWidget(
      TaskiemApp(
        key: UniqueKey(),
        mode: TkMode.light,
        home: AppLock(active: true, signedIn: true, startLocked: false, after: Duration.zero, unlocker: u, now: () => clock, child: settingsScreen()),
      ),
    );
    await film.settle();
    film.say('Later, Ikenna comes back to Taskiem after a few minutes away.');
    await film.hold(2.4);
    // Away and back (the test's frames stop while the app is in the
    // background, so the away is not drawn; the lock is what shows).
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    final gate2 = Completer<void>();
    u.gate = gate2;
    clock = clock.add(const Duration(minutes: 5));
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    film.say('Taskiem is locked until he proves it is him.');
    await film.play(1.2);
    film.system('biometric', 2.2, title: 'Unlock Taskiem');
    await film.play(2.2);
    gate2.complete();
    film.say('Fingerprint matched. Taskiem opens, and he carries on.');
    await film.play(1.0);
    await film.settle();
    await film.hold(2.2);
    await film.finish();
  }, skip: !filming);
}
