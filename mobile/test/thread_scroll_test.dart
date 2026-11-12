// Scrolling a long conversation (the phone feedback: "scrolling isn't smooth
// at all"): moving through it draws the day chip held at the top, not every
// message again on every frame.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  testWidgets('scrolling builds no message again, and the day chip still follows', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final now = DateTime.utc(2026, 11, 12, 12);
    // 300 messages over three days, 10 minutes apart.
    final items = List.generate(300, (i) => i);
    DateTime at(int i) => now.subtract(Duration(minutes: 10 * (300 - i)));
    var built = 0;
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: ThreadView<int>(
        items: items,
        of: (id: (i) => '$i', at: at, mine: (i) => i.isEven),
        now: now,
        render: (m, prev) {
          built++;
          return SizedBox(height: 44, child: Text('Message $m'));
        },
      ),
    ));
    await t.pumpAndSettle();
    final once = built;
    expect(once, greaterThanOrEqualTo(300));

    final thread = t.state<ThreadViewState<int>>(find.byType(ThreadView<int>));
    final end = thread.scroll.position.maxScrollExtent;
    // Up through the days a frame at a time, as a finger does.
    for (var y = end; y > 0; y -= 37) {
      thread.scroll.jumpTo(y);
      await t.pump(const Duration(milliseconds: 16));
    }
    await t.pumpAndSettle();
    expect(built, once, reason: 'scrolling rebuilt messages');

    // Halfway: a day's chip is held at the top of the thread.
    thread.scroll.jumpTo(end / 2);
    await t.pump();
    final held = find.byWidgetPredicate((w) => w is DayChip && !w.margin);
    expect(held, findsOneWidget);
    expect(t.getTopLeft(held).dy, lessThan(80));
  });
}
