// Scrolling a long conversation (the phone feedback: "scrolling isn't smooth
// at all", and 25 November 2026: "the chat flickers as it opens"): the
// thread is a list built lazily, anchored at the newest message. Opening
// builds what is in view and a little round it, not every message; the
// first frame shows the newest at the bottom with nothing jumping after;
// moving through it builds each message as it comes near, and the day chip
// held at the top still follows.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  final now = DateTime.utc(2026, 11, 12, 12);
  // 300 messages over three days, 10 minutes apart.
  DateTime at(int i) => now.subtract(Duration(minutes: 10 * (300 - i)));

  Widget thread(List<int> items, void Function(int) built, {String? focus, ({String id, int count})? unread}) => TaskiemApp(
    mode: TkMode.light,
    home: ThreadView<int>(
      items: items,
      of: (id: (i) => '$i', at: at, mine: (i) => i.isEven),
      now: now,
      focus: focus,
      unread: unread,
      render: (m, prev) {
        built(m);
        return SizedBox(height: 44, child: Text('Message $m'));
      },
    ),
  );

  void phone(WidgetTester t) {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  testWidgets('opening builds only what is in view, and the newest is at the bottom in the first frame', (t) async {
    phone(t);
    final built = <int>[];
    // One frame only: what the person sees as the chat slides in.
    await t.pumpWidget(thread(List.generate(300, (i) => i), built.add));
    // Before (every message in a Column): 300 built. Now about two screens.
    expect(built.toSet().length, lessThan(60), reason: 'built ${built.toSet().length} of 300');
    expect(built, contains(299));
    expect(built, isNot(contains(0)));
    final newest = t.getRect(find.text('Message 299'));
    final view = t.getRect(find.byType(ThreadView<int>));
    expect(newest.bottom, moreOrLessEquals(view.bottom - 8, epsilon: 1), reason: 'at the bottom in the first frame');
    // Nothing moves after it.
    await t.pumpAndSettle();
    expect(t.getRect(find.text('Message 299')), newest);
  });

  testWidgets('scrolling up builds each message as it comes near, and the day chip still follows', (t) async {
    phone(t);
    final built = <int>[];
    await t.pumpWidget(thread(List.generate(300, (i) => i), built.add));
    await t.pumpAndSettle();
    final once = built.length;
    final s = t.state<ThreadViewState<int>>(find.byType(ThreadView<int>));
    // Up through the days a frame at a time, as a finger does.
    for (var i = 0; i < 2000 && s.scroll.position.pixels > s.scroll.position.minScrollExtent + 1; i++) {
      s.scroll.jumpTo(s.scroll.position.pixels - 37);
      await t.pump(const Duration(milliseconds: 16));
    }
    await t.pumpAndSettle();
    expect(find.text('Message 0'), findsOneWidget, reason: 'the top reached');
    expect(built.length - once, lessThan(330), reason: 'each message built about once on the way up');

    // Halfway: a day's chip is held at the top of the thread.
    s.scroll.jumpTo((s.scroll.position.minScrollExtent + s.scroll.position.maxScrollExtent) / 2);
    await t.pump();
    final held = find.byWidgetPredicate((w) => w is DayChip && !w.margin);
    expect(held, findsOneWidget);
    expect(t.getTopLeft(held).dy, lessThan(80));
  });

  testWidgets('a message arriving: followed at the bottom; scrolled up, the view holds still and the button counts', (t) async {
    phone(t);
    var items = List.generate(300, (i) => i);
    await t.pumpWidget(thread(items, (_) {}));
    await t.pumpAndSettle();
    items = [...items, 301];
    await t.pumpWidget(thread(items, (_) {}));
    // In the same frame: no jump after it.
    final view = t.getRect(find.byType(ThreadView<int>));
    expect(t.getRect(find.text('Message 301')).bottom, moreOrLessEquals(view.bottom - 8, epsilon: 1));
    await t.pumpAndSettle();

    final s = t.state<ThreadViewState<int>>(find.byType(ThreadView<int>));
    s.scroll.jumpTo(s.scroll.position.pixels - 600);
    await t.pumpAndSettle();
    final place = t.getRect(find.text('Message 280'));
    items = [...items, 303];
    await t.pumpWidget(thread(items, (_) {}));
    expect(t.getRect(find.text('Message 280')), place, reason: 'the view holds still');
    await t.pumpAndSettle();
    expect(t.getRect(find.text('Message 280')), place);
    expect(find.bySemanticsLabel('1 new message: scroll to the newest'), findsOneWidget);
  });

  testWidgets('opened at the first unread or at a message: placed in the first frame', (t) async {
    phone(t);
    await t.pumpWidget(thread(List.generate(300, (i) => i), (_) {}, unread: (id: '250', count: 50)));
    expect(t.getTopLeft(find.text('50 unread messages')).dy, moreOrLessEquals(40 + 4, epsilon: 1), reason: 'the line 40 px under the top');
    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(thread(List.generate(300, (i) => i), (_) {}, focus: '120'));
    final view = t.getRect(find.byType(ThreadView<int>));
    expect(t.getCenter(find.text('Message 120')).dy, moreOrLessEquals(view.center.dy, epsilon: 1), reason: 'in the middle');
  });
}
