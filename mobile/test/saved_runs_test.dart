// The runs that read a business's saved thing (build plan H6,
// lib/chat/saved_runs.dart): under its sheet, newest first, for those
// who may read runs; never asked for by others, nor for one's own.

import 'package:flutter/widgets.dart' show Scrollable;
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/saved_accounts.dart';
import 'package:taskiem/chat/saved_runs.dart' show savedStepsWords;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'my_chats_test.dart' show Api, chat, conversation, drain, phone, press, settle;
import 'saved_accounts_test.dart' show saved;
import 'support.dart' show loadFonts;

Api _api() {
  final api = conversation(c: chat());
  final list = <Map<String, dynamic>>[
    {
      ...saved('w1', 'price-list', 'Price list', '1 line of text'),
      'kind': 'text',
      'details': {'body': 'Rice 50kg: 78,000'},
      'check': {'state': 'none', 'account_name': null, 'via': null, 'at': null, 'problem': null},
    },
  ];
  api.routes['GET /v1/saved'] = (_) => {'saved': list, 'limit': 2000, 'can_check': false};
  api.routes['GET /v1/saved/provider'] = (_) => {'connected': {'channel': 'iswallet/taskiem', 'name': 'Your Taskiem wallet', 'wallet': true}, 'owner': true};
  api.routes['GET /v1/saved/w1/runs'] = (_) => {
    'runs': [
      {'run_id': 'r2', 'workflow_id': 'wf1', 'workflow': 'Weekly prices', 'version': 3, 'name': 'price-list', 'steps': ['note', 'send'], 'read_at': '2026-11-24T08:00:00Z', 'status': 'completed'},
      {'run_id': 'r1', 'workflow_id': 'wf1', 'workflow': 'Weekly prices', 'version': 2, 'name': 'price-list', 'steps': ['send'], 'read_at': '2026-11-17T08:00:00Z', 'status': 'failed'},
    ],
  };
  return api;
}

void main() {
  setUpAll(loadFonts);

  test('which steps read it, in words', () {
    expect(savedStepsWords([]), 'Read when the run started');
    expect(savedStepsWords(['send']), 'Read by step send');
    expect(savedStepsWords(['a', 'b', 'c']), 'Read by steps a, b and c');
  });

  testWidgets('a business’s saved thing lists the runs that read it', (t) async {
    phone(t);
    final api = _api();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage', 'run.read'], personal: false), mine: false)));
    await settle(t);
    await press(t, find.text('Price list'));
    if (find.text('Change').evaluate().isNotEmpty) await press(t, find.text('Change'));
    await settle(t);
    expect(find.text('Runs that read it'), findsOneWidget);
    expect(find.text('Weekly prices'), findsNWidgets(2));
    expect(find.textContaining('Read by steps note and send'), findsOneWidget);
    expect(find.text('Failed'), findsOneWidget);
    await t.scrollUntilVisible(find.text('Runs that read it'), 200, scrollable: find.byType(Scrollable).last);
    await looks(t, 'saved-runs');
    await drain(t);
  });

  testWidgets('without run.read: not asked for, not shown', (t) async {
    phone(t);
    final api = _api();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: SavedAccountsPage(auth: api.auth(permissions: ['saved.manage'], personal: false), mine: false)));
    await settle(t);
    await press(t, find.text('Price list'));
    if (find.text('Change').evaluate().isNotEmpty) await press(t, find.text('Change'));
    await settle(t);
    expect(find.text('Runs that read it'), findsNothing);
    expect(api.sent('GET', '/v1/saved/w1/runs'), isEmpty);
    await drain(t);
  });
}
