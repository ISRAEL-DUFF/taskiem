// Pages G4 (docs/pages.md; build-plan-gaps §11): a board's columns one
// under another, a card's menu moving it to another column (the server
// asked, the board's move tool opened filled in, its Yes, its run); a
// column's next cards; a chart drawn from the server's figures, a bar
// pressed saying its figures, the figures listed. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/pages_page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'guidelines.dart';
import 'made_tools_stub.dart';
import 'support.dart' show loadFonts, snap;

const _json = {'content-type': 'application/json; charset=utf-8'};

/// The move tool: a ticket and the column it goes to; it writes, so it asks Yes.
final _moveTool = <String, dynamic>{
  'workflow_id': '0193a000-0000-7000-8000-0000000000c4',
  'environment': 'prod',
  'version': 1,
  'command': 'move-ticket',
  'name': 'Move ticket',
  'about': 'Move a ticket on',
  'says': <String>[],
  'care': {
    'level': 'yes',
    'because': ['It changes a row of Tickets'],
  },
  'answer': 'text',
  'always_ask': false,
  'inputs': {
    'type': 'object',
    'required': ['ref', 'status'],
    'properties': {
      'ref': {'type': 'string', 'title': 'Ticket'},
      'status': {'type': 'string', 'title': 'Status'},
    },
  },
};

class _Api {
  final server = MadeServer(tools: [_moveTool]);
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  Auth auth() {
    final a = Auth(
      transport: Transport(
        base: 'http://localhost',
        token: 'tok',
        client: MockClient((req) async {
          calls.add(req);
          final made = await server.call(req);
          if (made != null) return made;
          final f = routes['${req.method} ${req.url.path}'];
          return http.Response(jsonEncode(f == null ? {'error': 'not here'} : f(req) ?? {}), f == null ? 404 : 200, headers: _json);
        }),
      ),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    a.me = Me.fromJson({
      'tenant_id': 't1',
      'tenant_name': 'Mama’s Kitchen',
      'personal': false,
      'roles': ['support'],
      'permissions': ['run.start'],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Sam Eze', 'email': 'sam@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

Map<String, dynamic> _card(String id, String title, String sub) => {
  'id': id,
  'title': title,
  'sub': sub,
  'detail': {
    'title': title,
    'lines': [
      {'label': 'Customer', 'value': sub},
    ],
  },
};

Map<String, dynamic> _view(Set<String> moved) {
  final cards = [_card('1', 'Printer jam', 'Bayo'), _card('2', 'No ink', 'Ada'), _card('3', 'Paper', 'Musa')];
  String at(String id) => moved.contains(id) ? 'doing' : (id == '3' ? 'done' : 'open');
  Map<String, dynamic> lane(String value, String label, {Object? more}) {
    final rows = cards.where((c) => at(c['id'] as String) == value).toList();
    return {'value': value, 'label': label, 'count': rows.length + (more == null ? 0 : 1), 'rows': rows, 'more': more};
  }

  return {
    'page': {
      'name': 'desk',
      'title': 'Desk',
      'icon': 'tables',
      'about': '',
      'roles': <String>[],
      'pinned': false,
      'published': true,
      'has_draft': false,
      'version': 1,
      'updated_at': '2026-11-24T10:00:00Z',
    },
    'draft': false,
    'can_edit': false,
    'drawn_at': '2026-11-24T10:00:0${moved.length}Z',
    'filters': <Object>[],
    'blocks': [
      {
        'id': 'board',
        'type': 'board',
        'title': 'Tickets',
        'size': 'full',
        'empty': 'Nothing here',
        'count': 4,
        'has_detail': true,
        'lanes': [lane('open', 'Open', more: moved.isEmpty ? 'c1' : null), lane('doing', 'Doing'), lane('done', 'Done')],
        'move': {'label': 'Move', 'tool': 'move-ticket'},
      },
      {
        'id': 'spend',
        'type': 'chart',
        'title': 'Spent by week',
        'size': 'full',
        'naira': true,
        'empty': 'Nothing to show yet',
        'kind': 'bars',
        'points': [
          {'label': '3 Nov', 'long': 'Week of 3 November', 'value': 350000, 'shown': '₦3,500', 'share': 0.5833, 'tick': true},
          {'label': '10 Nov', 'long': 'Week of 10 November', 'value': 0, 'shown': '₦0', 'share': 0, 'tick': true},
          {'label': '17 Nov', 'long': 'Week of 17 November', 'value': 450000, 'shown': '₦4,500', 'share': 0.75, 'tick': true},
          {'label': '24 Nov', 'long': 'Week of 24 November', 'value': 120000, 'shown': '₦1,200', 'share': 0.2, 'tick': true},
        ],
        'ticks': [
          {'value': 0, 'shown': '₦0', 'share': 0},
          {'value': 200000, 'shown': '₦2k', 'share': 0.3333},
          {'value': 400000, 'shown': '₦4k', 'share': 0.6667},
          {'value': 600000, 'shown': '₦6k', 'share': 1},
        ],
        'zero': 0,
        'label_head': 'Week',
        'value_head': 'Amount',
        'summary': 'Highest ₦4,500 (Week of 17 November); latest ₦1,200 (Week of 24 November), 4 in all.',
      },
    ],
  };
}

void phone(WidgetTester t, [double tall = 1400]) {
  t.view.physicalSize = Size(360, tall);
  t.view.devicePixelRatio = 1;
  addTearDown(t.view.reset);
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump(const Duration(milliseconds: 100));
  }
  await t.pump(const Duration(milliseconds: 600));
}

void main() {
  setUpAll(loadFonts);

  testWidgets('A board: its columns, a card’s menu moves it on through the move tool, and its Yes', (t) async {
    phone(t);
    final api = _Api();
    final moved = <String>{};
    final acts = <Map<String, dynamic>>[];
    api.routes['GET /v1/pages/desk/view'] = (_) => _view(moved);
    api.routes['GET /v1/pages/desk/blocks/board'] = (r) => {
      'id': 'board',
      'type': 'board',
      'lanes': [
        {'value': 'open', 'label': 'Open', 'count': 3, 'rows': [_card('4', 'Card reader', 'Dupe')], 'more': null},
      ],
    };
    api.routes['POST /v1/pages/desk/act'] = (r) {
      final body = jsonDecode(r.body) as Map<String, dynamic>;
      acts.add(body);
      return {
        'kind': 'tool',
        'tool': {'button': 'b1', 'command': 'move-ticket', 'name': 'Move ticket', 'inputs': {'ref': body['row'], 'status': body['to']}, 'once': false},
      };
    };
    api.server.answers['0193b000-0000-7000-8000-000000000001'] = [
      {'status': 'completed', 'type': 'text', 'tool': 'move-ticket', 'text': 'Moved to doing'},
    ];
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PageScreen(auth: api.auth(), name: 'desk', backLabel: 'Back to Pages')));
    await settle(t);
    expect(find.text('Open'), findsOneWidget);
    expect(find.text('Doing'), findsOneWidget);
    expect(find.text('Done'), findsOneWidget);
    expect(find.text('Printer jam'), findsOneWidget);
    expect(find.text('Nothing here'), findsOneWidget, reason: 'Doing has no card');
    expect(find.text('Tap a card to move it to another column.'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp(r'Printer jam, Bayo, in Open\. Open, or move it')), findsOneWidget);
    await looks(t, 'page-board');
    await snap(t, 'page-board');

    // A column's next cards, read by its column.
    await t.tap(find.bySemanticsLabel('Show more of Open'));
    await settle(t);
    final more = api.calls.lastWhere((c) => c.url.path == '/v1/pages/desk/blocks/board');
    expect(more.url.queryParameters['lane'], 'open');
    expect(more.url.queryParameters['cursor'], 'c1');
    expect(find.text('Card reader'), findsOneWidget);

    // Printer jam's menu: open it, or move it to Doing or Done.
    await t.tap(find.text('Printer jam'));
    await settle(t);
    expect(find.text('Open it'), findsOneWidget);
    expect(find.text('Move to Doing'), findsOneWidget);
    expect(find.text('Move to Done'), findsOneWidget);
    expect(find.text('Move to Open'), findsNothing);
    await looks(t, 'page-board-menu');
    await t.tap(find.text('Move to Doing'));
    await settle(t);
    expect(acts.single, {'block': 'board', 'row': '1', 'action': 0, 'to': 'doing'});
    // The move tool's form, filled in from the card and the column.
    expect(find.text('Move ticket'), findsWidgets);
    expect(find.text('doing'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Next'));
    await settle(t);
    final start = api.server.sent.firstWhere((s) => s.path == '/v1/tools/move-ticket/start');
    expect(start.body['input'], {'ref': '1', 'status': 'doing'});
    expect(start.body['button'], 'b1');
    moved.add('1');
    await t.tap(find.bySemanticsLabel('Yes, run it'));
    await settle(t);
    expect(api.server.sent.last.path, '/v1/tools/move-ticket/confirm');
    await t.pump(const Duration(seconds: 3));
    await settle(t);
  });

  testWidgets('A board and a chart with large text: nothing cut off, all labelled and big enough to press', (t) async {
    final api = _Api();
    api.routes['GET /v1/pages/desk/view'] = (_) => _view({});
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: PageScreen(auth: api.auth(), name: 'desk')), drive: settle);
    // A chart of twelve weeks: its bars too narrow for a finger are not
    // pressed; the figures are listed instead.
    phone(t, 900);
    t.platformDispatcher.clearTextScaleFactorTestValue();
    api.routes['GET /v1/pages/desk/view'] = (_) {
      final v = _view({});
      final chart = (v['blocks'] as List).last as Map<String, dynamic>;
      chart['points'] = [
        for (var i = 0; i < 12; i++) {'label': 'W$i', 'long': 'Week $i', 'value': i * 100, 'shown': '₦$i', 'share': i / 12, 'tick': i.isEven},
      ];
      v['blocks'] = [chart];
      return v;
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: PageScreen(auth: api.auth(), name: 'desk', key: const ValueKey('twelve'))));
    await settle(t);
    expect(find.bySemanticsLabel('Week 3: ₦3'), findsNothing);
    expect(find.text('W2'), findsOneWidget);
    expect(find.text('W3'), findsNothing);
    await expectPressable(t);
  });

  testWidgets('A board this person may not move: no move in its menu, and why', (t) async {
    phone(t, 900);
    final api = _Api();
    api.routes['GET /v1/pages/desk/view'] = (_) {
      final v = _view({});
      final board = (v['blocks'] as List).first as Map<String, dynamic>;
      board['move'] = {'label': 'Move', 'tool': 'move-ticket', 'disabled': 'Only managers can use Move ticket.'};
      return v;
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PageScreen(auth: api.auth(), name: 'desk')));
    await settle(t);
    expect(find.text('Only managers can use Move ticket.'), findsOneWidget);
    expect(find.text('Tap a card to move it to another column.'), findsNothing);
    await t.tap(find.text('Printer jam'));
    await settle(t);
    // Its card opens straight away: nothing else to do with it.
    expect(find.text('Move to Doing'), findsNothing);
    expect(find.text('Customer'), findsOneWidget);
  });

  testWidgets('A chart: drawn from the server’s figures, a bar pressed says its own, the figures listed', (t) async {
    phone(t, 1400);
    final api = _Api();
    api.routes['GET /v1/pages/desk/view'] = (_) {
      final v = _view({});
      v['blocks'] = [(v['blocks'] as List).last];
      return v;
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: PageScreen(auth: api.auth(), name: 'desk')));
    await settle(t);
    expect(find.text('Spent by week'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp(r'^Spent by week\. Highest ₦4,500 \(Week of 17 November\)')), findsWidgets);
    // The latest first: the week now.
    expect(find.text('Week of 24 November  ₦1,200', findRichText: true), findsOneWidget);
    for (final l in ['3 Nov', '10 Nov', '17 Nov', '24 Nov']) {
      expect(find.text(l), findsOneWidget);
    }
    await t.tap(find.bySemanticsLabel('Week of 17 November: ₦4,500'));
    await settle(t);
    expect(find.text('Week of 17 November  ₦4,500', findRichText: true), findsOneWidget);
    await looks(t, 'page-chart');
    await snap(t, 'page-chart');
    await t.tap(find.text('See the figures'));
    await settle(t);
    expect(find.text('₦3,500'), findsOneWidget);
    expect(find.text('Week of 10 November'), findsOneWidget);
  });
}
