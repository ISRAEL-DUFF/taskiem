// What every screen must meet to be pressed (docs/spec/mobile-app.md §8):
// words for a screen reader, the tap action, and 48 by 48 to a finger;
// shared by the parity screens' check (targets_test.dart) and each
// screen's own tests.

import 'dart:async';
import 'dart:ui' show Tristate;

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';

/// Every guideline at once, on what is on the screen now.
Future<void> expectPressable(WidgetTester t) async {
  final handle = t.ensureSemantics();
  await expectLater(t, meetsGuideline(labeledTapTargetGuideline));
  await expectLater(t, meetsGuideline(const TapTargets()));
  await expectLater(t, meetsGuideline(const Pressable()));
  handle.dispose();
}

/// [home] at 360 px with the phone's text twice its size: nothing
/// overflows (an overflow fails the test as it is drawn), and what can be
/// pressed passes [expectPressable].
Future<void> expectLargeText(WidgetTester t, Widget home, {Future<void> Function(WidgetTester t)? drive}) async {
  t.view.physicalSize = const Size(360, 640);
  t.view.devicePixelRatio = 1;
  t.platformDispatcher.textScaleFactorTestValue = 2;
  addTearDown(t.view.reset);
  addTearDown(t.platformDispatcher.clearTextScaleFactorTestValue);
  await t.pumpWidget(home);
  await t.pumpAndSettle();
  if (drive != null) await drive(t);
  await t.pumpAndSettle();
  await expectPressable(t);
}

/// A button or a link a screen reader can press: its node offers the tap
/// action (a node that only says "button" cannot be double-tapped), unless
/// it says it is off.
class Pressable extends AccessibilityGuideline {
  const Pressable();

  @override
  String get description => 'Buttons and links can be pressed with a screen reader';

  @override
  FutureOr<Evaluation> evaluate(WidgetTester tester) {
    var result = const Evaluation.pass();
    void visit(SemanticsNode n) {
      final d = n.getSemanticsData();
      final f = n.flagsCollection;
      final off = f.isEnabled == Tristate.isFalse;
      if ((f.isButton || f.isLink) && !off && !d.hasAction(SemanticsAction.tap) && !n.isMergedIntoParent) {
        result += Evaluation.fail('"${d.label}" says it is a button but cannot be pressed with a screen reader.\n');
      }
      n.visitChildren((c) {
        visit(c);
        return true;
      });
    }

    for (final v in tester.binding.renderViews) {
      final root = v.owner?.semanticsOwner?.rootSemanticsNode;
      if (root != null) visit(root);
    }
    return result;
  }
}

/// Android's 48 by 48, skipping what a scrolling list has cut off at its
/// edge (a long bubble half scrolled away): Flutter's own check means to,
/// but compares the two in different coordinates.
class TapTargets extends MinimumTapTargetGuideline {
  const TapTargets() : super(size: const Size(48, 48), link: 'https://support.google.com/accessibility/android/answer/7101858');

  static Rect _onScreen(SemanticsNode n) {
    var r = n.rect;
    for (SemanticsNode? p = n; p != null; p = p.parent) {
      if (p.transform != null) r = MatrixUtils.transformRect(p.transform!, r);
    }
    return r;
  }

  @override
  bool shouldSkipNode(SemanticsNode node) {
    if (super.shouldSkipNode(node)) return true;
    // The emoji keyboard's keys: a dense grid, as the web's and a phone
    // keyboard's.
    if (const {'emoji-key', 'calendar-day'}.contains(node.getSemanticsData().identifier)) return true;
    final r = _onScreen(node);
    for (var p = node.parent; p != null; p = p.parent) {
      if (!p.flagsCollection.hasImplicitScrolling) continue;
      final list = _onScreen(p);
      const e = 0.5;
      return r.top <= list.top + e || r.bottom >= list.bottom - e || r.left <= list.left + e || r.right >= list.right - e;
    }
    return false;
  }
}
