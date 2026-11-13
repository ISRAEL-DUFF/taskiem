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

  testWidgets('away 5 minutes or more locks; a moment away does not', (t) async {
    var now = DateTime(2026, 11, 11, 9);
    final u = FakeUnlocker();
    await t.pumpWidget(app(u, () => now));
    await t.pumpAndSettle();
    expect(u.asked, 1);

    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    now = now.add(const Duration(minutes: 4));
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await t.pumpAndSettle();
    expect(u.asked, 1);

    u.answer = false;
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    now = now.add(const Duration(minutes: 5));
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await t.pumpAndSettle();
    expect(u.asked, 2);
    expect(find.text('The chats'), findsNothing);
  });

  testWidgets('not in front: the screens are covered at once, and shown again on coming back', (t) async {
    final u = FakeUnlocker();
    await t.pumpWidget(app(u, DateTime.now));
    await t.pumpAndSettle();
    expect(find.text('The chats'), findsOneWidget);

    // The app switcher, a call coming in: before any time has passed.
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    await t.pump();
    expect(find.text('The chats'), findsNothing);
    expect(find.text('Taskiem is locked'), findsNothing);

    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await t.pumpAndSettle();
    expect(find.text('The chats'), findsOneWidget);
    expect(u.asked, 1, reason: 'a moment away does not ask for the lock');
  });

  testWidgets('with the lock turned off, the screens are still covered when not in front', (t) async {
    final u = FakeUnlocker();
    await t.pumpWidget(TaskiemApp(home: AppLock(active: false, hideWhenAway: true, unlocker: u, child: const Text('The chats'))));
    await t.pumpAndSettle();
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    await t.pump();
    expect(find.text('The chats'), findsNothing);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await t.pumpAndSettle();
    expect(find.text('The chats'), findsOneWidget);
    expect(u.asked, 0);
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
