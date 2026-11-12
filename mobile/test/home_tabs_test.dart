// The home's bottom bar (phone feedback: "the app does not have a bottom
// nav like WhatsApp"): Chats, Updates, Money, Orders; the open tab's icon in
// its pill, the count on Chats; the tabs not in the app yet say so.

import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/home_tabs.dart';
import 'package:taskiem/theme/theme.dart';

import 'parity_test.dart' show screens;
import 'support.dart';

Widget home(int Function() count) {
  // The chats with their data made once, as the app's are.
  final chats = screens['chats']!.build();
  return HomeTabs(
    count: count,
    tabs: [
      (label: 'Chats', icon: 'chat', build: (_) => chats),
      (label: 'Updates', icon: 'activity', build: (_) => const ComingTab(title: 'Updates', icon: 'activity', words: 'What happened in your business, as it happens.')),
      (label: 'Money', icon: 'money', build: (_) => const ComingTab(title: 'Money', icon: 'money', words: 'Your invoices, bills and payments.')),
      (label: 'Orders', icon: 'orders', build: (_) => const ComingTab(title: 'Orders', icon: 'orders', words: 'Your orders, from placed to delivered.')),
    ],
  );
}

void main() {
  setUpAll(loadFonts);

  testWidgets('four tabs; Chats first with its count; the others are coming', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    t.view.padding = const FakeViewPadding(bottom: 24);
    addTearDown(t.view.reset);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(() => 7)));
    await t.pumpAndSettle();
    for (final l in ['Chats', 'Updates', 'Money', 'Orders']) {
      expect(find.bySemanticsLabel(l), findsOneWidget, reason: l);
    }
    expect(find.text('7'), findsOneWidget, reason: 'the count on Chats');
    expect(find.text('Bayo Rice Mills', findRichText: true), findsOneWidget, reason: 'the chats open first');
    // The bar sits above the gesture bar; the button to find a business above the bar.
    final bar = t.getRect(find.bySemanticsLabel('Chats'));
    expect(bar.bottom, lessThanOrEqualTo(844 - 24));
    expect(t.getRect(find.bySemanticsLabel('Find a business to link with')).bottom, lessThan(bar.top));

    await t.tap(find.bySemanticsLabel('Money'));
    await t.pumpAndSettle();
    expect(find.text('This is coming to the app soon.'), findsOneWidget);
    expect(find.text('Bayo Rice Mills', findRichText: true), findsNothing);
    await t.tap(find.bySemanticsLabel('Chats'));
    await t.pumpAndSettle();
    expect(find.text('Bayo Rice Mills', findRichText: true), findsOneWidget, reason: 'the chats kept as they were');
  });

  for (final mode in TkMode.values) {
    testWidgets('360 px, text twice its size, ${mode.name}: nothing overflows', (t) async {
      t.view.physicalSize = const Size(360, 640);
      t.view.devicePixelRatio = 1;
      t.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(t.view.reset);
      addTearDown(t.platformDispatcher.clearTextScaleFactorTestValue);
      await t.pumpWidget(TaskiemApp(mode: mode, home: home(() => 120)));
      await t.pumpAndSettle();
      expect(t.takeException(), isNull);
      await t.tap(find.bySemanticsLabel('Orders'));
      await t.pumpAndSettle();
      expect(t.takeException(), isNull);
    });
  }

  // Not a check: the picture, for a person to look at (build/home-*.png).
  for (final mode in TkMode.values) {
    testWidgets('picture, ${mode.name}', (t) async {
      t.view.physicalSize = const Size(390, 844);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final key = GlobalKey();
      await t.pumpWidget(
        RepaintBoundary(
          key: key,
          child: TaskiemApp(mode: mode, home: home(() => 7)),
        ),
      );
      await t.pumpAndSettle();
      await t.runAsync(() async {
        final img = await (t.renderObject(find.byKey(key)) as RenderRepaintBoundary).toImage();
        final png = await img.toByteData(format: ui.ImageByteFormat.png);
        File('build/home-${mode.name}.png')
          ..createSync(recursive: true)
          ..writeAsBytesSync(png!.buffer.asUint8List());
      });
      await t.tap(find.bySemanticsLabel('Money'));
      await t.pumpAndSettle();
      await t.runAsync(() async {
        final img = await (t.renderObject(find.byKey(key)) as RenderRepaintBoundary).toImage();
        final png = await img.toByteData(format: ui.ImageByteFormat.png);
        File('build/home-money-${mode.name}.png')
          ..createSync(recursive: true)
          ..writeAsBytesSync(png!.buffer.asUint8List());
      });
    });
  }
}
