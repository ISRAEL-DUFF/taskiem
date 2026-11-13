// The app as a whole, with screens open on top of the chats: the lock
// covers them, signing out closes them, and another business starts the
// chats afresh (docs/spec/mobile-app-m3-fixes.md B1–B3).

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/sign_in.dart';

import 'lock_test.dart' show FakeUnlocker;
import 'support.dart';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  void away(WidgetTester t) {
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
  }

  void back(WidgetTester t) {
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  }

  /// The app signed in as the chats fixture's person, Bayo's conversation
  /// open on top of the list; every request after [mark] is in [after].
  Future<({Auth auth, FakeUnlocker u, List<String> asked, void Function(DateTime) setNow})> openBayo(WidgetTester t, {Prefs? prefs}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final asked = <String>[];
    var now = DateTime(2026, 11, 11, 9);
    final auth = f.auth(also: (req) async {
      asked.add('${req.method} ${req.url.path}');
      if (req.method == 'POST' && req.url.path == '/v1/me/switch') {
        return http.Response(jsonEncode({'token': 'other-business'}), 200, headers: {'content-type': 'application/json'});
      }
      return null;
    });
    final u = FakeUnlocker();
    await t.pumpWidget(TaskiemApp(auth: auth, unlocker: u, now: () => now, prefs: prefs));
    await settle(t);
    await t.tap(find.text('Bayo Rice Mills').first);
    await settle(t);
    expect(find.byType(ConversationScreen), findsOneWidget);
    // Leaves the test with nothing running.
    addTearDown(() async {
      await t.pumpWidget(const SizedBox());
      await t.pump();
    });
    return (auth: auth, u: u, asked: asked, setNow: (d) => now = d);
  }

  testWidgets('the lock covers a conversation open on top of the chats, and it is there unlocked', (t) async {
    final o = await openBayo(t);
    final data = t.widget<ConversationScreen>(find.byType(ConversationScreen)).data;
    o.u.answer = false;
    away(t);
    o.setNow(DateTime(2026, 11, 11, 9, 6));
    back(t);
    await settle(t);
    expect(find.text('Taskiem is locked'), findsOneWidget);
    expect(find.byType(ConversationScreen), findsNothing, reason: 'covered, not drawn');
    expect(find.byType(ConversationScreen, skipOffstage: false), findsOneWidget, reason: 'kept underneath');
    expect(data.chats.disposed, isFalse, reason: 'the chats are not let go while locked');
    o.u.answer = true;
    await t.tap(find.text('Unlock'));
    await settle(t);
    expect(find.text('Taskiem is locked'), findsNothing);
    expect(t.widget<ConversationScreen>(find.byType(ConversationScreen)).data, same(data), reason: 'the same conversation, its place kept');
  });

  testWidgets('the lock turned off in Settings: away 5 minutes, the conversation is still there', (t) async {
    final prefs = Prefs(MemoryKeyStore());
    await prefs.load();
    await prefs.setLock(false);
    final o = await openBayo(t, prefs: prefs);
    away(t);
    o.setNow(DateTime(2026, 11, 11, 9, 6));
    back(t);
    await settle(t);
    expect(find.text('Taskiem is locked'), findsNothing);
    expect(find.byType(ConversationScreen), findsOneWidget);
    expect(o.u.asked, 0);
  });

  testWidgets('signing out closes the screens open on top, and nothing more is asked', (t) async {
    final o = await openBayo(t);
    await o.auth.signOut();
    await settle(t);
    expect(find.byType(SignInScreen), findsOneWidget);
    expect(find.byType(ConversationScreen, skipOffstage: false), findsNothing);
    o.asked.clear();
    await t.pump(const Duration(seconds: 40));
    await settle(t);
    expect(o.asked, isEmpty, reason: 'no conversation or list polling after signing out');
  });

  testWidgets('a session ended elsewhere (401) does the same', (t) async {
    final o = await openBayo(t);
    // The server says the session has ended on the next request.
    o.auth.transport.onSessionEnded?.call();
    await settle(t);
    expect(find.byType(SignInScreen), findsOneWidget);
    expect(find.byType(ConversationScreen, skipOffstage: false), findsNothing);
  });

  testWidgets('another business: the screens close and the chats start afresh', (t) async {
    final o = await openBayo(t);
    final before = t.widget<ConversationScreen>(find.byType(ConversationScreen)).data.chats;
    o.asked.clear();
    await o.auth.switchTo('t2');
    await settle(t);
    expect(find.byType(ConversationScreen, skipOffstage: false), findsNothing);
    final after = t.widget<ChatsScreen>(find.byType(ChatsScreen)).data;
    expect(after, isNot(same(before)));
    expect(before.disposed, isTrue);
    expect(o.asked, contains('GET /v1/link-chats'), reason: 'the new business\'s list');
    expect(o.auth.transport.token, 'other-business');
  });
}
