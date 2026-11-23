// A code scanned from the phone's home screen, with no workflow open, and
// tools on the app's icon (device fields §7, D2): the icon offers Scan a
// code and the person's tools, those opened last first; the code scanned
// is offered to the workflows that take it (GET /v1/scan/offers sends only
// its kind); a tool opens in Taskiem's conversation with its form filled
// in with the code, sent as the camera read it; a workflow that needs
// nothing more starts on Yes.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/shortcuts.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/code_scanner.dart';
import 'package:taskiem/chat/scan_start.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/kit/kit.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'form_request_d2_test.dart' show StandInCamera;
import 'looks.dart';
import 'made_tools_stub.dart';
import 'support.dart';

class FakeShortcuts implements ShortcutPlatform {
  void Function(String type)? tapped;
  List<({String type, String title})> items = const [];

  @override
  void initialize(void Function(String type) onAction) => tapped = onAction;

  @override
  Future<void> set(List<({String type, String title})> items) async => this.items = items;
}

/// Count an item: a members' tool whose input is a scan.
final _countTool = <String, dynamic>{
  'workflow_id': '0193a000-0000-7000-8000-0000000000c1',
  'environment': 'prod',
  'version': 1,
  'command': 'count-item',
  'name': 'Count an item',
  'about': 'How many of an item are on the shelf',
  'says': <String>[],
  'care': {'level': 'answers'},
  'answer': 'text',
  'always_ask': false,
  'inputs': {
    'type': 'object',
    'required': ['item'],
    'properties': {
      'item': {'type': 'scan', 'title': 'Item', 'why': 'the code on the item’s pack', 'formats': ['ean_13', 'upc_a']},
      'count': {'type': 'integer', 'title': 'How many'},
    },
  },
};

final _offers = {
  'format': 'ean_13',
  'offers': [
    {'kind': 'tool', 'workflow_id': _countTool['workflow_id'], 'version': 1, 'environment': 'prod', 'name': 'Count an item', 'about': 'How many of an item are on the shelf', 'command': 'count-item', 'input': 'item', 'asks': <String>[]},
    {'kind': 'workflow', 'workflow_id': '0193a000-0000-7000-8000-0000000000c2', 'version': 4, 'environment': 'prod', 'name': 'Goods received', 'input': 'code', 'asks': <String>[]},
    {'kind': 'workflow', 'workflow_id': '0193a000-0000-7000-8000-0000000000c3', 'version': 2, 'environment': 'prod', 'name': 'Return an item', 'input': 'code', 'asks': ['reason']},
  ],
};

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pumpAndSettle();
}

void main() {
  setUpAll(loadFonts);

  test('the icon: Scan a code, then three tools, those opened last first', () async {
    final phone = FakeShortcuts();
    final s = HomeShortcuts(phone);
    await s.show([
      (command: 'leave', name: 'Leave left'),
      (command: 'expense', name: 'Log an expense'),
      (command: 'tickets', name: 'Open tickets'),
      (command: 'count-item', name: 'Count an item'),
    ], recent: const ['tool:tickets', 'invoice']);
    expect([for (final i in phone.items) i.title], ['Scan a code', 'Open tickets', 'Count an item', 'Leave left']);
    expect(phone.items.first.type, scanShortcut);
    phone.tapped!('tool:expense');
    expect(shortcutTool(s.take()!), 'expense');
    expect(s.take(), isNull, reason: 'once');
  });

  testWidgets('scanned from the home screen: what takes the code; a tool opened filled in, a workflow started on Yes; pictured', (t) async {
    t.view.physicalSize = const Size(390, 900);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final camera = StandInCamera();
    final was = codeScanner;
    codeScanner = DeviceCodeScanner(camera: camera.call);
    addTearDown(() => codeScanner = was);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
    final auth = f.auth(
      also: (req) async {
        if (req.url.path == '/v1/scan/offers') {
          sent.add('GET ${req.url.query}');
          return ok(_offers);
        }
        if (req.method == 'POST' && req.url.path.endsWith('/runs')) {
          sent.add('POST ${req.url.path} ${req.body}');
          return ok({'run_id': 'r1', 'workflow_id': 'w', 'version': 4, 'environment': 'prod', 'status': 'queued', 'created': true}, 201);
        }
        return null;
      },
    );
    final opened = <(String, Map<String, dynamic>)>[];
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: Builder(
          builder: (context) => TkSmallButton(label: 'Scan a code', onPressed: () => scanFromHome(context, auth: auth, openTool: (c, i) => opened.add((c, i)))),
        ),
      ),
    );
    await t.tap(find.text('Scan a code'));
    await settle(t);
    camera.show(const ScannedCode('6151100093217', 'ean_13'));
    await settle(t);
    expect(sent.single, 'GET format=ean_13', reason: 'the kind alone, never the code');
    expect(find.text('What this code starts'), findsOneWidget);
    expect(find.text('Asks more than the code: start it on the web'), findsOneWidget);
    await looks(t, 'scan-offers');

    await t.tap(find.text('Goods received'));
    await settle(t);
    await t.tap(find.text('Start'));
    await settle(t);
    expect(sent.last, 'POST /v1/workflows/0193a000-0000-7000-8000-0000000000c2/runs {"input":{"code":{"text":"6151100093217","format":"ean_13"}},"environment":"prod"}');
    await t.pump(const Duration(seconds: 5));

    await t.tap(find.text('Scan a code'));
    await settle(t);
    camera.show(const ScannedCode('6151100093217', 'ean_13'));
    await settle(t);
    await t.tap(find.text('Count an item'));
    await settle(t);
    expect(opened.single.$1, 'count-item');
    expect(opened.single.$2, {
      'item': {'text': '6151100093217', 'format': 'ean_13'},
    });
  });

  testWidgets('a tool opened for a code: its form filled in, the code sent as the camera read it', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tools');
    final s = MadeServer(tools: [...madeTools, _countTool]);
    final chats = ChatsData(f.auth(also: s.call), now: () => f.at)..load();
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: TaskiemScreen(
          data: TaskiemData(chats, follow: false),
          openTool: (
            command: 'count-item',
            initial: {
              'item': {'text': '6151100093217', 'format': 'ean_13'},
            },
          ),
        ),
      ),
    );
    await settle(t);
    expect(find.text('Count an item'), findsWidgets);
    expect(find.text('Read by the camera: an EAN-13 barcode'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Run it'));
    await settle(t);
    final start = s.sent.where((x) => x.path == '/v1/tools/count-item/start').single;
    expect(start.body['input'], {
      'item': {'text': '6151100093217', 'format': 'ean_13'},
    });
  });
}
