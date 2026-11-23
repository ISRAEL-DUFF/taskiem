// Pages (decision 0072, pages G1): the pages a person may open, pinned
// first; a page drawn from the server's view (the count on the top card,
// figures beside each other, a list in its groups, a table as rows), a
// row's card in a sheet, a search and a choice sent to the server as the
// page's filters, Show more; a part that needs fixing; /pages links; a
// table put in a column's order, only by the columns the server allows.
// Pictured light and dark.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/route.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/pages_page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'support.dart' show loadFonts, snap;

const _json = {'content-type': 'application/json; charset=utf-8'};

class _Api {
  final calls = <http.BaseRequest>[];
  final routes = <String, Object? Function(http.BaseRequest r)>{};

  /// The page's live notices, when a test follows them.
  StreamController<List<int>>? live;

  Auth auth() {
    final a = Auth(
      transport: Transport(
        base: 'http://localhost',
        token: 'tok',
        client: MockClient.streaming((req, _) async {
          calls.add(req);
          final l = live;
          if (l != null && req.url.path.endsWith('/live')) {
            return http.StreamedResponse(l.stream, 200, headers: {'content-type': 'text/event-stream'});
          }
          final f = routes['${req.method} ${req.url.path}'];
          final body = f == null ? jsonEncode({'error': 'not here'}) : jsonEncode(f(req) ?? {});
          return http.StreamedResponse(Stream.value(utf8.encode(body)), f == null ? 404 : 200, headers: _json);
        }),
      ),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    a.me = Me.fromJson({
      'tenant_id': 't1',
      'tenant_name': 'Mama’s Kitchen',
      'personal': false,
      'roles': ['support'],
      'permissions': <String>[],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Sam Eze', 'email': 'sam@example.com', 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

Map<String, dynamic> _summary(String name, String title, {bool pinned = false, String about = ''}) => {
  'name': name,
  'title': title,
  'icon': 'tables',
  'about': about,
  'roles': <String>[],
  'pinned': pinned,
  'published': true,
  'has_draft': false,
  'version': 1,
  'updated_at': '2026-11-22T10:00:00Z',
};

Map<String, dynamic> _row(String id, String title, String sub, String state, String tone) => {
  'id': id,
  'title': title,
  'sub': sub,
  'state': {'text': state, 'tone': tone},
  'group': state,
  'detail': {
    'title': title,
    'lines': [
      {'label': 'Customer', 'value': sub},
      {'label': 'Phone', 'value': '+234 ••• ••• 5678'},
    ],
  },
};

Map<String, dynamic> _view({String? more = 'c2'}) => {
  'page': _summary('tickets', 'Tickets', pinned: true),
  'draft': false,
  'can_edit': false,
  'drawn_at': '2026-11-22T10:00:00Z',
  'filters': [
    {'id': 'q', 'type': 'search', 'label': 'Search', 'value': ''},
    {
      'id': 'st',
      'type': 'choice',
      'label': 'Show',
      'value': '',
      'options': [
        {'value': 'open', 'label': 'Open'},
        {'value': 'done', 'label': 'Done'},
      ],
    },
  ],
  'blocks': [
    {'id': 'all', 'type': 'figure', 'label': 'Open tickets', 'value': 7, 'shown': '7', 'tone': 'warn', 'lead': true, 'naira': false,
      'change': {'before': 5, 'words': 'up 40% on last week', 'up': true, 'down': false}},
    {'id': 'waiting', 'type': 'figure', 'label': 'Waiting', 'value': 2, 'shown': '2', 'tone': 'warn', 'lead': false, 'naira': false},
    {'id': 'owed', 'type': 'figure', 'label': 'Owed', 'value': 250050, 'shown': '₦2,500.50', 'tone': 'plain', 'lead': false, 'naira': true},
    {
      'id': 'rows',
      'type': 'list',
      'title': 'Tickets',
      'size': 'full',
      'empty': 'No tickets',
      'has_detail': true,
      'count': 4,
      'more': more,
      'groups': [
        {'key': 'open', 'label': 'Open'},
        {'key': 'done', 'label': 'Done'},
      ],
      'rows': [
        _row('1', 'Printer jam', 'Bayo', 'open', 'warn'),
        _row('3', 'Screen dark', 'Chi', 'open', 'warn'),
        _row('2', 'No ink', 'Ada', 'done', 'good'),
      ],
    },
    {
      'id': 'all_rows',
      'type': 'table',
      'title': 'Every ticket',
      'size': 'full',
      'empty': 'Nothing to show',
      'has_detail': false,
      'count': 1,
      'more': null,
      'columns': [
        {'column': 'title', 'label': 'Title', 'naira': false, 'sortable': true},
        {'column': 'amount_kobo', 'label': 'Amount', 'naira': true, 'sortable': true},
      ],
      'rows': [
        {'id': '1', 'cells': ['Printer jam', '₦2,500']},
      ],
    },
    {'id': 'gone', 'type': 'figure', 'broken': 'This part needs fixing: the table orders2 is gone.'},
  ],
};

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

  testWidgets('Pages: those this person may open, pinned first', (t) async {
    phone(t, 700);
    final api = _Api();
    api.routes['GET /v1/pages'] = (_) => {
      'pages': [_summary('tickets', 'Tickets', pinned: true, about: 'The support desk'), _summary('expenses', 'Expenses this month'), _summary('leave', 'Leave')],
      'can_edit': false,
      'can_publish': false,
      'max_pinned': 5,
      'max_pages': 10,
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PagesPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Pages'), findsWidgets);
    expect(find.text('Pinned'), findsWidgets);
    expect(find.text('Tickets'), findsOneWidget);
    expect(find.text('The support desk'), findsOneWidget);
    expect(find.text('Expenses this month'), findsOneWidget);
    await looks(t, 'pages');
  });

  testWidgets('Pages: none yet says where they are made', (t) async {
    phone(t, 600);
    final api = _Api();
    api.routes['GET /v1/pages'] = (_) => {'pages': <Object>[], 'can_edit': true, 'can_publish': true, 'max_pinned': 5, 'max_pages': 10};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PagesPage(auth: api.auth())));
    await settle(t);
    expect(find.text('No pages yet'), findsOneWidget);
    expect(find.textContaining('Show as a page'), findsOneWidget);
  });

  testWidgets('A page: its figures, a list in groups, a table as rows, a row’s card, filters sent', (t) async {
    phone(t, 1500);
    final api = _Api();
    api.routes['GET /v1/pages/tickets/view'] = (_) => _view();
    api.routes['GET /v1/pages/tickets/blocks/rows'] = (_) => {
      'id': 'rows',
      'type': 'list',
      'title': 'Tickets',
      'count': 4,
      'more': null,
      'groups': [
        {'key': 'waiting', 'label': 'Waiting'},
      ],
      'rows': [_row('4', 'Card reader', 'Dupe', 'waiting', 'warn')],
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PageScreen(auth: api.auth(), name: 'tickets', backLabel: 'Back to Pages')));
    await settle(t);
    expect(find.text('Tickets'), findsWidgets);
    expect(find.text('OPEN TICKETS'), findsOneWidget);
    expect(find.text('7'), findsOneWidget);
    expect(find.textContaining('up 40% on last week'), findsOneWidget);
    expect(find.text('₦2,500.50'), findsOneWidget);
    expect(find.text('Printer jam'), findsWidgets);
    expect(find.text('Amount: ₦2,500'), findsOneWidget);
    expect(find.text('This part needs fixing'), findsOneWidget);
    await looks(t, 'page');
    await snap(t, 'page');

    // A row opens its card.
    await t.tap(find.text('Screen dark'));
    await settle(t);
    expect(find.text('+234 ••• ••• 5678'), findsOneWidget);
    expect(find.text('Chi'), findsWidgets);
    await looks(t, 'page-row');
    await t.tapAt(const Offset(180, 40));
    await settle(t);

    // A choice is sent to the server as the page's filters.
    await t.tap(find.text('Done').first);
    await settle(t);
    final asked = api.calls.where((c) => c.url.path == '/v1/pages/tickets/view').last.url.queryParameters['filters'];
    expect(jsonDecode(asked!), {'st': 'done'});

    // Show more adds the next rows, under their own group.
    await t.ensureVisible(find.text('Show more'));
    await t.tap(find.text('Show more'));
    await settle(t);
    expect(api.calls.where((c) => c.url.path == '/v1/pages/tickets/blocks/rows').last.url.queryParameters['cursor'], 'c2');
    expect(find.text('Card reader'), findsOneWidget);
    expect(find.text('Show more'), findsNothing);
  });

  testWidgets('A table is put in a column’s order, only by the columns this person may', (t) async {
    phone(t, 800);
    final api = _Api();
    Map<String, dynamic> people({Map<String, dynamic>? order, List<List<String>>? rows}) => {
      'id': 'people',
      'type': 'table',
      'title': 'Staff',
      'size': 'full',
      'empty': 'Nothing to show',
      'has_detail': false,
      'count': 3,
      'more': null,
      'columns': [
        {'column': 'name', 'label': 'Name', 'naira': false, 'sortable': false}, // personal: no order for this person
        {'column': 'team', 'label': 'Team', 'naira': false, 'sortable': true},
        {'column': 'started', 'label': 'Started', 'naira': false, 'sortable': true},
      ],
      'order': ?order,
      'rows': [
        for (final (i, r) in (rows ?? [['•••ola', 'Floor', '3 Mar 2024'], ['•••ayo', 'Desk', '1 Jun 2025'], ['•••chi', 'Kitchen', '9 Jan 2023']]).indexed)
          {'id': '$i', 'cells': r},
      ],
    };
    api.routes['GET /v1/pages/staff/view'] = (_) => {
      'page': _summary('staff', 'Staff'),
      'draft': false,
      'can_edit': false,
      'drawn_at': '2026-11-22T10:00:00Z',
      'filters': <Object>[],
      'blocks': [people()],
    };
    api.routes['GET /v1/pages/staff/blocks/people'] = (r) {
      final desc = r.url.queryParameters['desc'] == '1';
      final rows = [['•••ayo', 'Desk', '1 Jun 2025'], ['•••ola', 'Floor', '3 Mar 2024'], ['•••chi', 'Kitchen', '9 Jan 2023']];
      return people(order: {'column': r.url.queryParameters['order'], 'desc': desc}, rows: desc ? rows.reversed.toList() : rows);
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PageScreen(auth: api.auth(), name: 'staff', backLabel: 'Back to Pages')));
    await settle(t);
    expect(find.text('Order'), findsOneWidget);
    expect(find.bySemanticsLabel('Put Staff in order'), findsOneWidget);
    await looks(t, 'page-order');
    await snap(t, 'page-order');

    // The sheet offers the columns the server allows: never the personal one.
    await t.tap(find.text('Order'));
    await settle(t);
    expect(find.text('Put Staff in order'), findsOneWidget);
    expect(find.text('As the page puts them'), findsOneWidget);
    expect(find.text('Team'), findsWidgets);
    expect(find.text('Started'), findsOneWidget);
    expect(find.text('Name'), findsNothing);
    await looks(t, 'page-order-sheet');
    await snap(t, 'page-order-sheet');
    await t.tap(find.text('Team').last);
    await settle(t);
    var asked = api.calls.where((c) => c.url.path == '/v1/pages/staff/blocks/people').last.url.queryParameters;
    expect(asked['order'], 'team');
    expect(asked['desc'], isNull);
    expect(find.text('By Team'), findsOneWidget);
    expect(find.text('•••ayo'), findsOneWidget);
    expect(tester(t).dy(find.text('•••ayo')) < tester(t).dy(find.text('•••chi')), isTrue);

    // Picked again, the other way round.
    await t.tap(find.text('By Team'));
    await settle(t);
    expect(find.textContaining('Pick again for Z to A'), findsOneWidget);
    await t.tap(find.text('Team').last);
    await settle(t);
    asked = api.calls.where((c) => c.url.path == '/v1/pages/staff/blocks/people').last.url.queryParameters;
    expect(asked['desc'], '1');
    expect(tester(t).dy(find.text('•••chi')) < tester(t).dy(find.text('•••ayo')), isTrue);

    // Back to the page's own order: no call, the rows as drawn.
    final before = api.calls.length;
    await t.tap(find.text('By Team'));
    await settle(t);
    await t.tap(find.text('As the page puts them'));
    await settle(t);
    expect(api.calls.length, before);
    expect(find.text('Order'), findsOneWidget);
  });

  testWidgets('Taskiem’s orders on a page: a row opens the order; a part not for this person says so', (t) async {
    phone(t, 900);
    final api = _Api();
    api.routes['GET /v1/pages/desk/view'] = (_) => {
      'page': _summary('desk', 'Shop desk'),
      'draft': false,
      'can_edit': false,
      'drawn_at': '2026-11-22T10:00:00Z',
      'filters': <Object>[],
      'blocks': [
        {'id': 'n', 'type': 'figure', 'label': 'Invoiced', 'value': 5000000, 'shown': '₦50,000', 'tone': 'plain', 'lead': true, 'naira': true},
        {
          'id': 'orders',
          'type': 'list',
          'title': 'Orders',
          'size': 'full',
          'empty': 'No orders',
          'has_detail': false,
          'count': 2,
          'more': null,
          'rows': [
            {'id': 'AC-2', 'title': 'AC-2', 'sub': 'C••• · ₦9,900', 'state': {'text': 'accepted', 'tone': 'good'}, 'link': '/storefront/orders/AC-2'},
            {'id': 'AC-1', 'title': 'AC-1', 'sub': 'B••• · ₦1,500', 'state': {'text': 'received', 'tone': 'warn'}, 'link': '/storefront/orders/AC-1'},
          ],
        },
        {'id': 'inv', 'type': 'table', 'title': 'Invoices', 'unavailable': 'You cannot see invoices.'},
      ],
    };
    api.routes['GET /v1/storefront/orders/AC-2'] = (_) => {'error': 'stand-in'};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PageScreen(auth: api.auth(), name: 'desk', backLabel: 'Back to Pages')));
    await settle(t);
    expect(find.text('AC-2'), findsOneWidget);
    expect(find.text('You cannot see invoices.'), findsOneWidget);
    await looks(t, 'page-records');
    await snap(t, 'page-records');
    await t.pumpWidget(TaskiemApp(mode: TkMode.dark, home: PageScreen(auth: api.auth(), name: 'desk', backLabel: 'Back to Pages')));
    await settle(t);
    await snap(t, 'page-records-dark');
    await t.tap(find.text('AC-2'));
    await settle(t);
    expect(api.calls.any((c) => c.url.path == '/v1/storefront/orders/AC-2'), isTrue);
    expect(recordLinkOf('/invoices/INV-0001'), (kind: 'invoice', id: 'INV-0001'));
    expect(recordLinkOf('/elsewhere/1'), isNull);
  });

  testWidgets('An open page reads the parts a notice names, once for a burst, keeping a table’s order', (t) async {
    phone(t, 1500);
    final api = _Api()..live = StreamController<List<int>>();
    var drawn = 0;
    api.routes['GET /v1/pages/tickets/view'] = (_) {
      drawn++;
      final v = _view(more: null);
      (v['blocks'] as List)[0] = {...(v['blocks'] as List)[0] as Map<String, dynamic>, 'shown': drawn == 1 ? '7' : '8'};
      return v;
    };
    var ordered = 0;
    api.routes['GET /v1/pages/tickets/blocks/all_rows'] = (r) {
      ordered++;
      return {
        'id': 'all_rows',
        'type': 'table',
        'title': 'Every ticket',
        'count': 2,
        'more': null,
        'columns': [
          {'column': 'title', 'label': 'Title', 'naira': false, 'sortable': true},
        ],
        'order': {'column': r.url.queryParameters['order'], 'desc': false},
        'rows': [
          {'id': '1', 'cells': ['Printer jam']},
          if (ordered > 1) {'id': '9', 'cells': ['A new one']},
        ],
      };
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PageScreen(auth: api.auth(), name: 'tickets', backLabel: 'Back to Pages')));
    await settle(t);
    expect(find.text('7'), findsOneWidget);
    // The table put in order by Title.
    await t.ensureVisible(find.text('Order'));
    await t.tap(find.text('Order'));
    await settle(t);
    await t.tap(find.text('Title').last);
    await settle(t);
    expect(find.text('By Title'), findsOneWidget);

    // Three notices at once: one read, the figure and the table in its order.
    void notice(String blocks) => api.live!.add(utf8.encode('event: changed\ndata: {"page":"tickets","blocks":$blocks}\n\n'));
    notice('["all"]');
    notice('["all_rows"]');
    notice('["all","all_rows"]');
    await settle(t);
    await t.pump(const Duration(seconds: 2));
    await settle(t);
    expect(drawn, 2);
    expect(ordered, 2);
    expect(find.text('8'), findsOneWidget);
    expect(find.text('A new one'), findsOneWidget);
    expect(find.text('By Title'), findsOneWidget);
    final asked = api.calls.where((c) => c.url.path == '/v1/pages/tickets/blocks/all_rows').last.url.queryParameters;
    expect(asked['order'], 'title');
    expect(asked['cursor'], isNull);
    await api.live!.close();
  });

  test('A table part’s order in words', () {
    final b = PageBlock.fromJson({
      'id': 'x',
      'type': 'table',
      'columns': [
        {'column': 'name', 'label': 'Name', 'naira': false, 'sortable': false},
        {'column': 'team', 'label': 'Team', 'naira': false, 'sortable': true},
      ],
      'order': {'column': 'team', 'desc': true},
    });
    expect(sortableColumns(b).map((c) => c.column), ['team']);
    expect(orderLabel(b), 'By Team');
    expect(orderLabel(PageBlock.fromJson({'id': 'x', 'type': 'table'})), 'Order');
    expect(orderWay(true), 'Z to A, largest or latest first');
  });

  test('/pages links open the list and a page', () {
    expect(routeOf('/pages').screen, RouteScreen.pages);
    expect(routeOf('/pages').id, '');
    expect(routeOf('/pages/tickets').id, 'tickets');
    expect(routeOf('/pages/tickets/edit').screen, isNot(RouteScreen.pages));
    expect(groupedRows(PageBlock.fromJson(_view()['blocks'][3] as Map<String, dynamic>)).map((g) => g.label).toList(), ['Open', 'Done']);
    expect(filtersParam({'q': '', 'st': 'open'}), '{"st":"open"}');
    expect(filtersParam({}), isNull);
  });
}

/// Where a widget sits, to say which row comes first.
({double Function(Finder) dy}) tester(WidgetTester t) => (dy: (f) => t.getTopLeft(f.first).dy);
