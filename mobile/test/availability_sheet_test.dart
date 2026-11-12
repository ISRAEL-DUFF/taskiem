// Availability (M7 part A; web ChatAvailability.tsx): the state now and who
// sees it; Free, Busy till a time, Off sent as the web sends them.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/availability_sheet.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
  }

  testWidgets('free, then busy till 3pm, then off; the deliveries held said', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    var changed = 0;
    http.Response ok(Object body) => http.Response(jsonEncode(body), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.url.path != '/v1/availability') return null;
        if (req.method == 'GET') return ok({'state': 'free', 'businesses': 2, 'open': 1});
        sent.add(req.body);
        final b = jsonDecode(req.body) as Map;
        return ok({'state': b['state'] == 'on' ? 'free' : b['state'], 'businesses': 2, 'open': 1});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: AvailabilitySheet(auth: chats.auth, onChanged: () => changed++)));
    await settle(t);
    expect(find.text('Free'), findsWidgets);
    expect(find.text('The 2 businesses you deliver for see this when they offer a delivery.'), findsOneWidget);
    expect(find.text('You hold 1 delivery now, so you are busy until they are delivered, whatever you choose.'), findsOneWidget);
    await t.enterText(find.byType(EditableText), '3pm');
    await t.tap(find.bySemanticsLabel('Busy'));
    await settle(t);
    expect(sent.last, '{"state":"busy","until":"3pm"}');
    expect(find.text('Busy, 1 delivery'), findsWidgets);
    await t.pump(const Duration(seconds: 2));
    await t.tap(find.bySemanticsLabel('Off'));
    await settle(t);
    expect(sent.last, '{"state":"off"}');
    expect(find.text('Off today'), findsWidgets);
    expect(changed, 2);
    await t.pump(const Duration(seconds: 2));
  });
}
