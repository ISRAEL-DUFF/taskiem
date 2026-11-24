// Ready-made tools (made tools §13, build plan M6): the library's recipes
// listed with the tool each makes; one added with the word typed as a
// person says it, sent as a command; the next free one given when the
// word was taken, said on the page; someone who may not build is told.
// Pictured light and dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/recipes.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart' show loadFonts;

const _json = {'content-type': 'application/json; charset=utf-8'};

Map<String, dynamic> _recipe(String id, String title, String command, String care, String answer, {String? companion, List<Map<String, dynamic>> params = const []}) => {
  'id': id,
  'title': title,
  'summary': 's',
  'description': 'd',
  'category': 'tools',
  'tags': ['tool'],
  'connectors': <String>[],
  'variables': <Object>[],
  'params': [
    {'name': 'command', 'type': 'command', 'title': 'Its word', 'description': 'What people type to use it, after /.', 'required': true, 'default': command, 'example': command},
    {'name': 'table', 'type': 'string', 'title': 'Table', 'description': 'The table.', 'required': false, 'default': 'customers', 'example': 'customers', 'picker': 'table'},
    {'name': 'roles', 'type': 'roles', 'title': 'Who can use it', 'description': 'The roles whose people may use it.', 'required': false, 'default': ['owner', 'manager'], 'example': ['owner', 'manager']},
    ...params,
  ],
  'steps': <Object>[],
  'available': true,
  'tool': {'name': title.split(' from').first, 'about': 'Finds a row of your table and shows it', 'for': 'members', 'icon': 'search', 'answer': answer, 'command': command, 'care': care, 'companion': ?companion},
};

final _list = {
  'templates': [
    _recipe('tool-look-up', 'Look something up from the chat', 'find-customer', 'answers', 'card'),
    _recipe('tool-add-a-record', 'Add a record from the chat', 'add-sale', 'yes', 'text'),
    _recipe('tool-daily-report', 'A daily report with a button to the list', 'sales-today', 'answers', 'list', companion: 'Daily report'),
    _recipe('tool-call-a-system', 'Ask another system from the chat', 'check-order', 'answers', 'card', params: [
      {'name': 'address', 'type': 'string', 'title': 'Web address', 'description': 'The address it reads, starting with https.', 'required': true, 'example': 'https://api.example.com/orders'},
    ]),
  ],
};

class _Api {
  final sent = <Map<String, dynamic>>[];

  Auth auth({List<String> permissions = const ['workflow.edit']}) {
    final a = Auth(
      transport: Transport(
        base: 'http://localhost',
        token: 'tok',
        client: MockClient((req) async {
          if (req.method == 'GET' && req.url.path == '/v1/templates') {
            expect(req.url.queryParameters['tool'], 'true');
            return http.Response(jsonEncode(_list), 200, headers: _json);
          }
          if (req.method == 'POST' && req.url.path == '/v1/templates/tool-look-up/instantiate') {
            final body = jsonDecode(req.body) as Map<String, dynamic>;
            sent.add(body);
            final asked = (body['params'] as Map)['command'] as String;
            return http.Response(
              jsonEncode({
                'id': 'wf1',
                'version': 1,
                'state': 'draft',
                'template': 'tool-look-up',
                'problems': <Object>[],
                'tool': {'asked': asked, 'command': '$asked-2', 'changed': true},
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

  test('a typed word is a command, and the words say what the tool is', () {
    expect(rolesOf('Owner, Shift manager,, '), ['owner', 'shift_manager']);
    expect(commandOf(' /Find Customer '), 'find-customer');
    const tool = TemplateTool(about: 'a', answer: 'list', care: 'yes', command: 'today', companion: 'Daily report', for_: 'customers', name: 'Today');
    expect(recipeWords(tool), ['Your customers use it', 'Asks yes first', 'Says back a list', 'Comes with “Daily report”, a schedule whose button opens it']);
    expect(
      installedWords(const TemplateInstantiatedTool(asked: 'add-sale', command: 'add-sale-2', changed: true)),
      '/add-sale is taken, so it is called with /add-sale-2.',
    );
  });

  testWidgets('Ready-made tools: listed with their word; one added as a draft, the next free word said', (t) async {
    phone(t, 760);
    final api = _Api();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: RecipesPage(auth: api.auth(), backLabel: 'Back to Taskiem')));
    await settle(t);
    expect(find.text('Ready-made tools'), findsWidgets);
    expect(find.text('Look something up from the chat'), findsOneWidget);
    expect(find.text('/find-customer · Answers at once: it only reads'), findsOneWidget);
    expect(find.text('/add-sale · Asks yes first'), findsOneWidget);
    await looks(t, 'recipes');

    await t.tap(find.text('Look something up from the chat'));
    await settle(t);
    expect(find.text('Its word, after /'), findsOneWidget);
    // Who can use it, shown and changeable (review M6, 4).
    expect(find.text('Who can use it'), findsOneWidget);
    expect(find.text('owner, manager'), findsOneWidget);
    expect(find.text('Says back a card', findRichText: true), findsNothing);
    expect(find.textContaining('Says back a card'), findsOneWidget);
    await looks(t, 'recipes-add');
    await t.enterText(find.byType(EditableText).first, '/Find Customer');
    await t.tap(find.text('Add it as a draft'));
    await settle(t);
    expect((api.sent.single['params'] as Map)['command'], 'find-customer');
    expect((api.sent.single['params'] as Map)['roles'], ['owner', 'manager']);
    expect(find.text('Look something up: saved as a draft'), findsOneWidget);
    expect(find.text('/find-customer is taken, so it is called with /find-customer-2.'), findsOneWidget);
    // The toast has gone by the time the page is pictured.
    await t.pump(const Duration(seconds: 6));
    await settle(t);
    await looks(t, 'recipes-added');
  });

  testWidgets('Ready-made tools: what a recipe needs with no default is asked', (t) async {
    phone(t, 1100);
    final api = _Api();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: RecipesPage(auth: api.auth())));
    await settle(t);
    await t.tap(find.text('Ask another system from the chat'));
    await settle(t);
    expect(find.text('Web address'), findsOneWidget);
    expect(find.text('Table'), findsNothing);
    await t.tap(find.text('Add it as a draft'));
    await settle(t);
    expect(find.text('Web address is needed.'), findsOneWidget);
  });

  testWidgets('Ready-made tools: someone who may not build is told who can', (t) async {
    phone(t, 760);
    final api = _Api();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: RecipesPage(auth: api.auth(permissions: const []))));
    await settle(t);
    expect(find.text('Adding a tool needs a builder'), findsOneWidget);
    await t.tap(find.text('Look something up from the chat'));
    await settle(t);
    expect(find.text('Its word, after /'), findsNothing);
  });

  // What can be pressed is labelled and big enough (targets_test), on the
  // page and in its sheet.
  testWidgets('Ready-made tools: labelled, and big enough to press', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final api = _Api();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: RecipesPage(auth: api.auth())));
    await settle(t);
    await expectPressable(t);
    await t.tap(find.text('Look something up from the chat'));
    await settle(t);
    await expectPressable(t);
  });

  // Large text on a small phone (sizes_test): nothing overflows.
  for (final scale in [1.3, 2.0]) {
    for (final mode in TkMode.values) {
      testWidgets('Ready-made tools, text ×$scale, ${mode.name}, 360 px', (t) async {
        t.view.physicalSize = const Size(360, 640);
        t.view.devicePixelRatio = 1;
        t.platformDispatcher.textScaleFactorTestValue = scale;
        addTearDown(t.view.reset);
        addTearDown(t.platformDispatcher.clearTextScaleFactorTestValue);
        final api = _Api();
        await t.pumpWidget(TaskiemApp(mode: mode, home: RecipesPage(auth: api.auth())));
        await settle(t);
        await t.tap(find.text('Look something up from the chat'));
        await settle(t);
        expect(t.takeException(), isNull);
      });
    }
  }
}
