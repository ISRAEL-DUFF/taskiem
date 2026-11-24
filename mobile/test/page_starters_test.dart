// Page starters (pages §10, 3; build plan G5): the starters listed with
// what each is; one's sheet says what its page has and what it brings; Add
// it makes it, published, and says what was given another name; the
// page's Open the page; Pages offers them to someone who may make tables
// and tools, and not to anyone else. Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/page_starters.dart';
import 'package:taskiem/chat/pages_page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart' show loadFonts;

const _json = {'content-type': 'application/json; charset=utf-8'};

Map<String, dynamic> _starter(String id, String title, String about, String icon, String page, List<String> parts, List<String> makes) =>
    {'id': id, 'title': title, 'about': about, 'icon': icon, 'page': page, 'parts': parts, 'makes': makes};

final _list = {
  'starters': [
    _starter('tickets-desk', 'Tickets desk', 'Tickets on a board by where each one is, with how many are open, and a button to add one.', 'check', 'Tickets',
        ['figure', 'figure', 'figure', 'buttons', 'board'], [
      'The table Tickets: Title, Customer, Email, Priority, Status',
      '/new-ticket (New ticket): adds a row to Tickets, asking for Title, Customer, Email and Priority',
      '/move-ticket (Move a ticket): changes a row of Tickets: Status',
    ]),
    _starter('expenses-month', 'Expenses this month', 'What was spent this month, a chart by week, every expense, and a button to log one.', 'money', 'Expenses this month',
        ['figure', 'figure', 'buttons', 'chart', 'table'], ['The table Expenses: What, Amount, Note', '/log-expense (Log an expense): the ready-made tool tool-add-a-record']),
    _starter('leave', 'Leave', 'Requests for time off, grouped by what was decided, with Approve and Decline on each, and a button to ask.', 'clock', 'Leave',
        ['figure', 'figure', 'buttons', 'list'], ['The table Leave requests: Who, Email, From, To, Why, Status']),
    _starter('waiting-list', 'Waiting list', 'Who is waiting, first come first served, with Seen and Left on each, and a button to add someone.', 'members', 'Waiting list',
        ['figure', 'figure', 'buttons', 'list'], ['The table Waiting list: Name, Phone, How many, Note, Status']),
  ],
};

class _Api {
  final sent = <String, Map<String, dynamic>>{};

  Auth auth({List<String> permissions = const ['page.edit', 'table.read', 'table.edit', 'workflow.edit']}) {
    final a = Auth(
      transport: Transport(
        base: 'http://localhost',
        token: 'tok',
        client: MockClient((req) async {
          if (req.method == 'GET' && req.url.path == '/v1/page-starters') return http.Response(jsonEncode(_list), 200, headers: _json);
          if (req.method == 'GET' && req.url.path == '/v1/pages') {
            return http.Response(jsonEncode({'pages': <Object>[], 'can_edit': permissions.contains('page.edit'), 'can_publish': true, 'max_pages': 10, 'max_pinned': 5}), 200, headers: _json);
          }
          if (req.method == 'POST' && req.url.path == '/v1/page-starters/tickets-desk/install') {
            sent['tickets-desk'] = jsonDecode(req.body) as Map<String, dynamic>;
            return http.Response(
              jsonEncode({
                'page': 'tickets-2',
                'title': 'Tickets',
                'tables': [
                  {'name': 'tickets_2', 'title': 'Tickets', 'asked': 'tickets', 'changed': true},
                ],
                'tools': [
                  {'command': 'new-ticket-2', 'asked': 'new-ticket', 'changed': true, 'name': 'New ticket', 'workflow': 'w1', 'state': 'published'},
                ],
                'published': true,
                'problems': <Object>[],
              }),
              201,
              headers: _json,
            );
          }
          return http.Response(jsonEncode({'error': 'not here'}), 404, headers: _json);
        }),
      ),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    a.me = Me.fromJson({
      'tenant_id': 't1',
      'tenant_name': 'Mama’s Kitchen',
      'personal': false,
      'roles': ['owner'],
      'permissions': permissions,
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Sam Eze', 'email': 'sam@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

void phone(WidgetTester t, [double tall = 900]) {
  t.view.physicalSize = Size(360, tall);
  t.view.devicePixelRatio = 1;
  addTearDown(t.view.reset);
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.pump(const Duration(milliseconds: 100));
  }
  await t.pump(const Duration(milliseconds: 600));
}

void main() {
  setUpAll(loadFonts);

  test('a page\'s parts and what was made, in words', () {
    expect(partsWords(['figure', 'figure', 'buttons', 'board']), 'figures, buttons and a board');
    expect(
      madeWords(PageBundleMade.fromJson({
        'page': 'leave-2',
        'title': 'Leave',
        'tables': <Object>[],
        'tools': [
          {'command': 'ask-leave-2', 'asked': 'ask-leave', 'changed': true, 'name': 'Ask', 'workflow': 'w', 'state': 'draft'},
        ],
        'published': false,
        'waiting': 'Saved as drafts: publishing the tools needs the right to publish workflows.',
        'problems': <Object>[],
      })),
      ['/ask-leave is taken, so it is called with /ask-leave-2.', 'Saved as drafts: publishing the tools needs the right to publish workflows.'],
    );
  });

  testWidgets('Page starters: listed; one added with its table and tools, published, the names given said', (t) async {
    phone(t, 760);
    final api = _Api();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PageStartersPage(auth: api.auth(), backLabel: 'Back to Pages')));
    await settle(t);
    expect(find.text('Page starters'), findsWidgets);
    expect(find.text('Tickets desk'), findsOneWidget);
    expect(find.text('Expenses this month'), findsOneWidget);
    await looks(t, 'page-starters');

    await t.tap(find.text('Tickets desk'));
    await settle(t);
    expect(find.text('It has figures, buttons and a board'), findsOneWidget);
    expect(find.text('The table Tickets: Title, Customer, Email, Priority, Status'), findsOneWidget);
    expect(find.text('/move-ticket (Move a ticket): changes a row of Tickets: Status'), findsOneWidget);
    await looks(t, 'page-starters-add');
    await t.tap(find.text('Add it'));
    await settle(t);
    expect(api.sent['tickets-desk'], {'publish': true});
    expect(find.text('Tickets: added'), findsOneWidget);
    expect(find.textContaining('/new-ticket is taken, so it is called with /new-ticket-2.'), findsOneWidget);
    expect(find.textContaining('Tickets is ready, with its tools.'), findsOneWidget);
    expect(find.text('Open the page'), findsOneWidget);
    await looks(t, 'page-starters-added');
  });

  testWidgets('Pages offers the starters to someone who may make tables and tools, and no one else', (t) async {
    phone(t, 760);
    final api = _Api();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PagesPage(auth: api.auth())));
    await settle(t);
    expect(find.text('From a starter'), findsOneWidget);
    await t.tap(find.text('From a starter'));
    await settle(t);
    expect(find.text('Tickets desk'), findsOneWidget);

    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PagesPage(auth: _Api().auth(permissions: const ['page.edit', 'table.read']))));
    await settle(t);
    expect(find.text('From a starter'), findsNothing);
  });

  testWidgets('Page starters: labelled, and big enough to press', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final api = _Api();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PageStartersPage(auth: api.auth())));
    await settle(t);
    await expectPressable(t);
    await t.tap(find.text('Tickets desk'));
    await settle(t);
    await expectPressable(t);
  });
}
