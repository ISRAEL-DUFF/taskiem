// A screen sliding in and out (the founder's feedback of 25 November 2026:
// "the slide in isn't smooth, and going back doesn't visibly slide out"):
// Back, the app's own back arrow and Android's back gesture each slide the
// screen out; a chat opened in another's place slides out too; what a chat
// reads besides its messages waits for the slide in; and the chats list is
// not rebuilt under a chat sliding over it.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/kit/mobile.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

void main() {
  const page = Key('page');

  /// A home with a button that pushes a page in [TkRoute].
  Future<NavigatorState> home(WidgetTester t, {Widget Function()? next, Widget? under}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: under ?? const SizedBox.expand()));
    final nav = t.state<NavigatorState>(find.byType(Navigator).first);
    nav.push(TkRoute<void>(pageBuilder: (_, _, _) => next?.call() ?? const SizedBox.expand(key: page, child: ColoredBox(color: Color(0xFF00FF00)))));
    await t.pumpAndSettle();
    return nav;
  }

  double left(WidgetTester t) => t.getTopLeft(find.byKey(page)).dx;

  testWidgets('Back slides the screen out over 260 ms', (t) async {
    await home(t);
    expect(left(t), 0);
    // Android's Back button, as the app's own back arrow (maybePop).
    await t.binding.handlePopRoute();
    await t.pump();
    await t.pump(const Duration(milliseconds: 130));
    expect(left(t), inExclusiveRange(10, 380), reason: 'half way out');
    await t.pumpAndSettle();
    expect(find.byKey(page), findsNothing);
  });

  Future<void> gesture(WidgetTester t, String method, [double? progress]) => t.binding.defaultBinaryMessenger.handlePlatformMessage(
    SystemChannels.backGesture.name,
    SystemChannels.backGesture.codec.encodeMethodCall(MethodCall(method, progress == null ? null : {'touchOffset': [5.0, 400.0], 'progress': progress, 'swipeEdge': 0})),
    (_) {},
  );

  testWidgets("Android's back gesture: the screen follows the finger, then slides out from there", (t) async {
    await home(t);
    await gesture(t, 'startBackGesture', 0);
    await t.pump();
    await gesture(t, 'updateBackGestureProgress', 0.5);
    await t.pump();
    expect(left(t), moreOrLessEquals(195, epsilon: 2), reason: 'half way, with the finger');
    await gesture(t, 'commitBackGesture');
    await t.pump();
    await t.pump(const Duration(milliseconds: 16));
    expect(left(t), greaterThanOrEqualTo(195), reason: 'on out from where it was, not back to the start first');
    await t.pumpAndSettle();
    expect(find.byKey(page), findsNothing);
  });

  testWidgets("Android's back gesture let go: back in", (t) async {
    await home(t);
    await gesture(t, 'startBackGesture', 0);
    await gesture(t, 'updateBackGestureProgress', 0.3);
    await t.pump();
    expect(left(t), greaterThan(50));
    await gesture(t, 'cancelBackGesture');
    await t.pumpAndSettle();
    expect(left(t), 0);
  });

  testWidgets('a screen put in another’s place at once still slides out on Back', (t) async {
    final nav = await home(t);
    nav.pushReplacement(TkRoute<void>(transitionDuration: Duration.zero, pageBuilder: (_, _, _) => const SizedBox.expand(key: page)));
    await t.pump();
    await t.pump();
    expect(left(t), 0, reason: 'there at once');
    await t.binding.handlePopRoute();
    await t.pump();
    await t.pump(const Duration(milliseconds: 130));
    expect(left(t), inExclusiveRange(10, 380));
    await t.pumpAndSettle();
  });

  testWidgets('what waits for the slide in runs once it has slid in', (t) async {
    final ran = <String>[];
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: const SizedBox.expand()));
    final nav = t.state<NavigatorState>(find.byType(Navigator).first);
    nav.push(TkRoute<void>(pageBuilder: (_, _, _) => Builder(builder: (context) {
      WidgetsBinding.instance.addPostFrameCallback((_) => afterSlideIn(context, () => ran.add('extras')));
      return const SizedBox.expand(key: page);
    })));
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    expect(ran, isEmpty, reason: 'still sliding');
    await t.pumpAndSettle();
    expect(ran, ['extras']);
  });

  testWidgets('the screen under a sliding one is not rebuilt until the slide ends', (t) async {
    final data = ValueNotifier(0);
    var builds = 0;
    final nav = await home(t, under: TkCoveredBuilder(listenable: data, builder: (_) {
      builds++;
      return Text('${data.value}', textDirection: TextDirection.ltr);
    }));
    final before = builds;
    nav.pop();
    await t.pump();
    // Something changes as it slides out (a chat read, the list read again).
    data.value = 1;
    await t.pump(const Duration(milliseconds: 100));
    await t.pump(const Duration(milliseconds: 50));
    expect(builds, before, reason: 'not under the slide');
    await t.pumpAndSettle();
    expect(builds, before + 1);
    expect(find.text('1'), findsOneWidget);
  });
}
