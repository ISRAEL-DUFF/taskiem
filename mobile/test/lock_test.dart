import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/app/lock.dart';
import 'package:taskiem/kit/keyboard.dart';
import 'package:taskiem/main.dart';

class FakeUnlocker implements Unlocker {
  bool has = true;
  bool answer = true;
  int asked = 0;
  @override
  Future<bool> available() async => has;
  @override
  Future<bool> unlock() async {
    asked++;
    return answer;
  }
}

Widget app(FakeUnlocker u, DateTime Function() now) => TaskiemApp(
      home: AppLock(active: true, unlocker: u, now: now, child: const Text('The chats')),
    );

void main() {
  testWidgets('a kept session opens with the phone\'s lock', (t) async {
    final u = FakeUnlocker();
    await t.pumpWidget(app(u, DateTime.now));
    await t.pumpAndSettle();
    expect(u.asked, 1);
    expect(find.text('The chats'), findsOneWidget);
  });

  testWidgets('not unlocked: the chats stay covered, and Unlock asks again', (t) async {
    final u = FakeUnlocker()..answer = false;
    await t.pumpWidget(app(u, DateTime.now));
    await t.pumpAndSettle();
    expect(find.text('The chats'), findsNothing);
    expect(find.text('Taskiem is locked'), findsOneWidget);
    u.answer = true;
    await t.tap(find.text('Unlock'));
    await t.pumpAndSettle();
    expect(u.asked, 2);
    expect(find.text('The chats'), findsOneWidget);
  });

  testWidgets('just signed in: not asked again at once', (t) async {
    final u = FakeUnlocker();
    await t.pumpWidget(TaskiemApp(home: AppLock(active: true, unlocker: u, startLocked: false, child: const Text('The chats'))));
    await t.pumpAndSettle();
    expect(u.asked, 0);
    expect(find.text('The chats'), findsOneWidget);
  });

  testWidgets('a phone with no lock of its own opens without one', (t) async {
    final u = FakeUnlocker()..has = false;
    await t.pumpWidget(app(u, DateTime.now));
    await t.pumpAndSettle();
    expect(u.asked, 0);
    expect(find.text('The chats'), findsOneWidget);
  });

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

  testWidgets('away the time chosen or more locks; less does not', (t) async {
    var now = DateTime(2026, 11, 11, 9);
    final u = FakeUnlocker();
    await t.pumpWidget(app(u, () => now));
    await t.pumpAndSettle();
    expect(u.asked, 1);

    away(t);
    now = now.add(const Duration(seconds: 50));
    back(t);
    await t.pumpAndSettle();
    expect(u.asked, 1);

    u.answer = false;
    away(t);
    now = now.add(const Duration(minutes: 1));
    back(t);
    await t.pumpAndSettle();
    expect(u.asked, 2);
    expect(find.text('The chats'), findsNothing);
  });

  testWidgets('Immediately: any time in another app locks', (t) async {
    var now = DateTime(2026, 11, 11, 9);
    final u = FakeUnlocker();
    await t.pumpWidget(TaskiemApp(home: AppLock(active: true, unlocker: u, startLocked: false, after: Duration.zero, now: () => now, child: const Text('The chats'))));
    await t.pumpAndSettle();
    away(t);
    now = now.add(const Duration(seconds: 2));
    back(t);
    await t.pumpAndSettle();
    expect(u.asked, 1);
  });

  testWidgets('a dialog, the notification shade or the fingerprint prompt over the app covers nothing and locks nothing', (t) async {
    var now = DateTime(2026, 11, 11, 9);
    final u = FakeUnlocker();
    await t.pumpWidget(TaskiemApp(home: AppLock(active: true, unlocker: u, startLocked: false, after: Duration.zero, now: () => now, child: const Text('The chats'))));
    await t.pumpAndSettle();
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    await t.pump();
    expect(find.text('The chats'), findsOneWidget, reason: 'not covered while still in the app');
    now = now.add(const Duration(minutes: 40));
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await t.pumpAndSettle();
    expect(find.text('The chats'), findsOneWidget);
    expect(u.asked, 0);
  });

  testWidgets('picking a photo or a file (the phone\'s own screen over the app) is not being away', (t) async {
    var now = DateTime(2026, 11, 11, 9);
    final u = FakeUnlocker();
    await t.pumpWidget(TaskiemApp(home: AppLock(active: true, unlocker: u, startLocked: false, after: Duration.zero, now: () => now, child: const Text('The chats'))));
    await t.pumpAndSettle();
    final picked = Completer<String>();
    final pick = stepOut(() => picked.future);
    away(t);
    await t.pump();
    expect(find.text('The chats'), findsOneWidget, reason: 'not covered');
    now = now.add(const Duration(minutes: 3));
    back(t);
    picked.complete('photo.jpg');
    expect(await pick, 'photo.jpg');
    await t.pumpAndSettle();
    expect(u.asked, 0);
    expect(find.text('The chats'), findsOneWidget);

    // Afterwards, going to another app counts again.
    away(t);
    back(t);
    await t.pumpAndSettle();
    expect(u.asked, 1);
  });

  testWidgets('Android: with the lock on the switcher shows no picture of the screens, and nothing in the app is covered', (t) async {
    final hidden = <bool>[];
    final was = hideFromRecents;
    hideFromRecents = (h) async => hidden.add(h);
    addTearDown(() => hideFromRecents = was);
    final u = FakeUnlocker();
    Widget at(bool on) => TaskiemApp(home: AppLock(active: on, signedIn: true, startLocked: false, unlocker: u, child: const Text('The chats')));
    await t.pumpWidget(at(false));
    await t.pumpAndSettle();
    expect(hidden, [false]);
    await t.pumpWidget(at(true));
    await t.pumpAndSettle();
    expect(hidden, [false, true]);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    await t.pump();
    expect(find.text('The chats'), findsOneWidget, reason: 'the notification shade over the app covers nothing');
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await t.pumpWidget(at(false));
    await t.pumpAndSettle();
    expect(hidden, [false, true, false]);
  });

  testWidgets('iPhone: with the lock on the screens are covered as the switcher opens, and shown again on coming back', (t) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    final u = FakeUnlocker();
    await t.pumpWidget(app(u, DateTime.now));
    await t.pumpAndSettle();
    expect(find.text('The chats'), findsOneWidget);

    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    await t.pump();
    expect(find.text('The chats'), findsNothing);
    expect(find.text('Taskiem is locked'), findsNothing);

    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await t.pumpAndSettle();
    expect(find.text('The chats'), findsOneWidget);
    expect(u.asked, 1, reason: 'a moment away does not ask for the lock');
    debugDefaultTargetPlatformOverride = null;
  });

  testWidgets('with the lock off (as it is until turned on), nothing is ever covered, as on WhatsApp', (t) async {
    final u = FakeUnlocker();
    await t.pumpWidget(TaskiemApp(home: AppLock(active: false, signedIn: true, unlocker: u, child: const Text('The chats'))));
    await t.pumpAndSettle();
    away(t);
    await t.pump();
    expect(find.text('The chats'), findsOneWidget);
    back(t);
    await t.pumpAndSettle();
    expect(find.text('The chats'), findsOneWidget);
    expect(u.asked, 0);
  });

  testWidgets('turning the lock on in Settings does not ask at once; signing in with a kept session does', (t) async {
    final u = FakeUnlocker();
    Widget at({required bool on, required bool signedIn}) =>
        TaskiemApp(home: AppLock(active: signedIn && on, signedIn: signedIn, unlocker: u, child: const Text('The chats')));
    await t.pumpWidget(at(on: false, signedIn: true));
    await t.pumpAndSettle();
    await t.pumpWidget(at(on: true, signedIn: true));
    await t.pumpAndSettle();
    expect(u.asked, 0);
    expect(find.text('The chats'), findsOneWidget);
    await t.pumpWidget(at(on: true, signedIn: false));
    await t.pumpAndSettle();
    await t.pumpWidget(at(on: true, signedIn: true));
    await t.pumpAndSettle();
    expect(u.asked, 1);
  });

  testWidgets('the keyboard coming up does not build the app afresh (typing in a search box locked it)', (t) async {
    var made = 0;
    Widget at(double keys) => MediaQuery(
      data: MediaQueryData(viewInsets: EdgeInsets.only(bottom: keys)),
      child: Directionality(
        textDirection: TextDirection.ltr,
        child: KeyboardRoom(child: _Counted(onMade: () => made++)),
      ),
    );
    await t.pumpWidget(at(0));
    await t.pumpWidget(at(300));
    await t.pumpWidget(at(0));
    expect(made, 1, reason: 'the lock under it is kept, not made again (and locked again)');
  });
}

class _Counted extends StatefulWidget {
  const _Counted({required this.onMade});
  final VoidCallback onMade;

  @override
  State<_Counted> createState() => _CountedState();
}

class _CountedState extends State<_Counted> {
  @override
  void initState() {
    super.initState();
    widget.onMade();
  }

  @override
  Widget build(BuildContext context) => const SizedBox();
}
