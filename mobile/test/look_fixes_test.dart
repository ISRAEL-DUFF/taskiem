// Three things seen on a real phone in the light (17 November 2026): the
// chats' "…" menu ran off the left of the screen; a card's pill and a tag's
// letters in your own pine bubble did not show; the open tab at the bottom
// was hard to tell from the others.

import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/cards.dart' show CardButton, CardPill;
import 'package:taskiem/chat/conversation.dart' show ownWords;
import 'package:taskiem/chat/thread.dart' show showHeadMenu;
import 'package:taskiem/chat/words.dart' show CardTone;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/home_tabs.dart';
import 'package:taskiem/theme/look.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const pine = Color(0xFF0D3B2E);

/// WCAG's contrast ratio of two colours.
double contrast(Color a, Color b) {
  double lum(Color c) {
    double ch(double v) => v <= 0.03928 ? v / 12.92 : math.pow((v + 0.055) / 1.055, 2.4).toDouble();
    return 0.2126 * ch(c.r) + 0.7152 * ch(c.g) + 0.0722 * ch(c.b);
  }

  final (x, y) = (lum(a), lum(b));
  return (math.max(x, y) + 0.05) / (math.min(x, y) + 0.05);
}

void main() {
  setUpAll(loadFonts);

  testWidgets('the "…" menu stays on the screen, a long item wrapping, with large text', (t) async {
    t.view.physicalSize = const Size(360, 780);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    late BuildContext ctx;
    await t.pumpWidget(TaskiemApp(
      home: MediaQuery(
        data: const MediaQueryData(size: Size(360, 780), textScaler: TextScaler.linear(1.3)),
        child: Builder(builder: (c) {
          ctx = c;
          return const SizedBox();
        }),
      ),
    ));
    // The "…" button sits left of the "+" one: its right edge at 300.
    unawaited(showHeadMenu(ctx, at: const Rect.fromLTRB(260, 60, 300, 100), items: [
      ('Linked businesses', () {}),
      ('Your business in Explore', () {}),
      ('Settings', () {}),
      ('Sound for new orders and payments: on', () {}),
    ]));
    await t.pumpAndSettle();
    for (final words in ['Linked businesses', 'Sound for new orders and payments: on']) {
      final r = t.getRect(find.text(words));
      expect(r.left, greaterThanOrEqualTo(16), reason: '"$words" starts on the screen');
      expect(r.right, lessThanOrEqualTo(300));
    }
  });

  testWidgets('in your own pine bubble in the light, a card\'s pill and a tag\'s letters show', (t) async {
    late TkLook look;
    await t.pumpWidget(TaskiemApp(
      home: Builder(
        builder: (context) => ownWords(
          context,
          true,
          Builder(builder: (inner) {
            look = TkLook.of(inner);
            return const Center(child: CardPill('Completed', CardTone.lime));
          }),
        ),
      ),
    ));
    final ink = t.widget<Text>(find.text('Completed')).style!.color!;
    expect(contrast(ink, pine), greaterThan(4.5), reason: 'the pill\'s word on pine');
    for (final c in [look.limeInk, look.tealInk, look.orangeInk, look.blueInk, look.violetInk]) {
      expect(contrast(c, pine), greaterThan(4.5), reason: 'a tag\'s letters on pine');
    }
    // Before: the light's lime ink, dark green on the dark green.
    expect(contrast(tkLookLight.limeInk, pine), lessThan(3));
  });

  testWidgets('in your own pine bubble in the light, a card\'s buttons look pressable: light ones with white words, the main one lime', (t) async {
    await t.pumpWidget(TaskiemApp(
      home: Builder(
        builder: (context) => ownWords(
          context,
          true,
          ColoredBox(
            color: pine,
            child: Center(
              child: Column(mainAxisSize: MainAxisSize.min, children: [
                CardButton(label: 'Open', onTap: () {}),
                CardButton(label: 'Pay', primary: true, onTap: () {}),
              ]),
            ),
          ),
        ),
      ),
    ));
    Color ground(String label) => (t.widget<Container>(find.ancestor(of: find.text(label), matching: find.byType(Container)).first).decoration as BoxDecoration).color!;
    final open = t.widget<Text>(find.text('Open')).style!.color!;
    final pay = t.widget<Text>(find.text('Pay')).style!.color!;
    expect(contrast(open, Color.alphaBlend(ground('Open'), pine)), greaterThan(4.5), reason: 'a plain button\'s words');
    expect(contrast(ground('Pay'), pine), greaterThan(3), reason: 'the main button stands off the bubble');
    expect(contrast(pay, ground('Pay')), greaterThan(4.5), reason: 'its words');
  });

  testWidgets('in the dark your own bubble is left as it is', (t) async {
    late TkLook look;
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.dark,
      home: Builder(
        builder: (context) => ownWords(context, true, Builder(builder: (inner) {
          look = TkLook.of(inner);
          return const SizedBox();
        })),
      ),
    ));
    expect(identical(look, tkLookDark), isTrue);
  });

  testWidgets('the open tab has a pill behind its icon and a bold name; the others do not', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final key = GlobalKey<HomeTabsState>();
    await t.pumpWidget(TaskiemApp(
      home: HomeTabs(key: key, tabs: [
        (label: 'Chats', icon: 'chat', build: (_) => const SizedBox()),
        (label: 'Money', icon: 'money', build: (_) => const SizedBox()),
      ]),
    ));
    await t.tap(find.text('Money'));
    await t.pumpAndSettle();
    Color? pill(String label) {
      final box = find.descendant(
        of: find.ancestor(of: find.text(label), matching: find.byType(Column)).first,
        matching: find.byType(AnimatedContainer),
      );
      return (t.widget<AnimatedContainer>(box).decoration as BoxDecoration?)?.color;
    }

    expect(pill('Money')!.a, greaterThan(0), reason: 'the open tab');
    expect(pill('Chats')!.a, 0);
    expect(t.widget<Text>(find.text('Money')).style!.fontWeight!.value, greaterThan(t.widget<Text>(find.text('Chats')).style!.fontWeight!.value));
  });
}
