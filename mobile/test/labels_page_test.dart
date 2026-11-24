// Labels in Money's settings (what a business buys §5.4, U2a): the
// business's labels by use, renamed and merged; never rewriting what was
// recorded.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/labels_page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart';
import 'treat_fixture.dart';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  Future<List<String>> open(WidgetTester t, {TkMode mode = TkMode.light}) async {
    t.view.physicalSize = const Size(390, 1200);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final auth = f.auth(
      also: (req) async {
        final treat = treatAnswers(req);
        if (treat != null) return treat;
        sent.add('${req.method} ${req.url.path} ${req.body}');
        if (req.url.path.endsWith('/merge')) return ok(label('lb-staff', 'Staff welfare', 0));
        if (req.method == 'PATCH') return ok(label('lb-pack', 'Bags and packaging', 0));
        return null;
      },
    );
    await t.pumpWidget(TaskiemApp(mode: mode, home: LabelsPage(auth: auth)));
    await settle(t);
    return sent;
  }

  testWidgets('labels by use, renamed', (t) async {
    final sent = await open(t);
    expect(find.text('Labels'), findsWidgets);
    expect(find.text('Packaging'), findsOneWidget);
    expect(find.text('Used 12 times'), findsOneWidget);
    await looks(t, 'labels');
    await t.tap(find.text('Packaging'));
    await t.pumpAndSettle();
    await t.tap(find.text('Rename'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Name')), matching: find.byType(EditableText)), 'Bags and packaging');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Rename'));
    await settle(t);
    expect(sent.single, 'PATCH /v1/money/labels/lb-pack {"name":"Bags and packaging"}');
    expect(find.text('Renamed to Bags and packaging'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a label merged into another, asked first', (t) async {
    final sent = await open(t);
    await t.tap(find.text('Machines'));
    await t.pumpAndSettle();
    await t.tap(find.text('Merge into another label'));
    await t.pumpAndSettle();
    await t.tap(find.text('Staff welfare').last);
    await t.pumpAndSettle();
    expect(find.text('Merge Machines into Staff welfare?'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Merge'));
    await settle(t);
    expect(sent.single, 'POST /v1/money/labels/lb-mach/merge {"into":"lb-staff"}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('large text and what can be pressed', (t) async {
    await open(t, mode: TkMode.dark);
    await expectPressable(t);
    final f = Fixture('chats');
    await expectLargeText(
      t,
      TaskiemApp(mode: TkMode.light, home: LabelsPage(auth: f.auth(also: (req) async => treatAnswers(req)))),
      drive: settle,
    );
  });
}
