// The kit's parts the M7 audit fixed (docs/spec/mobile-app-m7-fixes.md
// Part 1): a radio row says which one is chosen; a screen that reads again
// does not while another screen covers it.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/kit/kit.dart';
import 'package:taskiem/kit/polling.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

class _Reads extends StatefulWidget {
  const _Reads(this.reads);
  final List<int> reads;
  @override
  State<_Reads> createState() => _ReadsState();
}

class _ReadsState extends State<_Reads> with Polling {
  @override
  void initState() {
    super.initState();
    startPolling(const Duration(seconds: 1), () => widget.reads.add(1));
  }

  @override
  Widget build(BuildContext context) => const SizedBox.expand();
}

void main() {
  setUpAll(loadFonts);

  testWidgets('a radio row says it is the one chosen; a checkbox row too', (t) async {
    final h = t.ensureSemantics();
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: Column(
          children: [
            TkChoiceRow(control: const TkRadio(checked: true), label: const Text('Delivered'), onTap: () {}),
            TkChoiceRow(control: const TkRadio(checked: false), label: const Text('They pick it up'), onTap: () {}),
            TkChoiceRow(radio: false, control: const TkCheckbox(checked: true), label: const Text('Proof'), onTap: () {}),
          ],
        ),
      ),
    );
    expect(t.getSemantics(find.text('Delivered')), matchesSemantics(label: 'Delivered', isChecked: true, hasCheckedState: true, isInMutuallyExclusiveGroup: true, hasEnabledState: true, isEnabled: true, hasTapAction: true));
    expect(t.getSemantics(find.text('They pick it up')).flagsCollection.isChecked.toString(), 'CheckedState.isFalse');
    expect(t.getSemantics(find.text('Proof')).flagsCollection.isChecked.toString(), 'CheckedState.isTrue');
    h.dispose();
  });

  testWidgets('reading again: not while covered, at once when back in front', (t) async {
    final reads = <int>[];
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Reads(reads)));
    final nav = t.state<NavigatorState>(find.byType(Navigator));
    await t.pump(const Duration(seconds: 1));
    expect(reads.length, 1);
    nav.push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => const SizedBox.expand()));
    await t.pumpAndSettle();
    final was = reads.length;
    await t.pump(const Duration(seconds: 3));
    expect(reads.length, was, reason: 'covered');
    nav.pop();
    await t.pumpAndSettle();
    await t.pump(const Duration(seconds: 1));
    expect(reads.length, greaterThan(was));
    // Put away, then back: one read at once.
    final before = reads.length;
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await t.pump(const Duration(seconds: 3));
    expect(reads.length, before);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    t.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    expect(reads.length, before + 1);
    await t.pumpWidget(const SizedBox());
  });
}
