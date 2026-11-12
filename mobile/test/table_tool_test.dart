// Tables (M8 parts W and T; web TableTool, TableViewer.tsx, Tables.tsx,
// lib/tables.ts, lib/tableView.ts): the cells' words, what a search, a
// page, a row added, changed and removed send.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/table_tool.dart';
import 'package:taskiem/chat/words.dart' show CardTone;
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

final _table = {
  'id': 'TB1',
  'name': 'customers',
  'title': 'Customers',
  'description': 'Who buys from us',
  'columns': [
    {'name': 'name', 'title': 'Name', 'type': 'text', 'required': true},
    {'name': 'phone', 'title': 'Phone', 'type': 'phone', 'required': false},
    {'name': 'balance_kobo', 'title': 'Balance', 'type': 'integer', 'required': false},
    {'name': 'status', 'title': 'Status', 'type': 'text', 'required': false},
    {'name': 'paid', 'title': 'Paid', 'type': 'boolean', 'required': false},
  ],
};

void main() {
  setUpAll(loadFonts);

  test('cells in words; a search; the page’s words', () {
    expect(columnView({'name': 'balance_kobo', 'type': 'integer'}), 'money:kobo');
    expect(columnView({'name': 'price', 'type': 'number'}), 'money:naira');
    expect(columnView({'name': 'status', 'type': 'text'}), 'status');
    expect(cellWords(500000, 'money:kobo'), '₦5,000');
    expect(cellWords(45000, 'money:naira'), '₦45,000');
    expect(cellWords(1234567, 'number'), '1,234,567');
    expect(cellWords('2026-11-12', 'date'), '12 Nov 2026');
    expect(cellWords(true, 'yesno'), 'Yes');
    expect(cellWords(null, 'text'), '—');
    expect(statusTone('Paid'), CardTone.lime);
    expect(statusTone('overdue'), CardTone.danger);
    expect(cellValue('integer', '12'), 12);
    expect(cellValue('boolean', 'yes'), true);
    expect(cellValue('text', ''), '');
    expect(cellValue('date', ''), isNull);
    expect(searchFilter('ada', ['name']), {'column': 'name', 'op': 'ilike', 'value': '%ada%'});
    expect((searchFilter('a_b', ['name', 'phone'])!['any'] as List).first, {'column': 'name', 'op': 'ilike', 'value': r'%a\_b%'});
    expect(pageWords(0, 25, 25, 312), '1–25 of 312');
    expect(pageWords(1, 25, 6, 31), '26–31 of 31');
    expect(pageWords(0, 25, 0, 0), 'No rows');
  });

  Future<List<String>> open(WidgetTester t, Widget Function(ChatsData c) home, {Map<String, dynamic>? table, int count = 2}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 1400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(also: (req) async {
        final p = req.url.path;
        if (p == '/v1/storefront') return ok({'catalogue_table': 'other'});
        if (p == '/v1/tables' && req.method == 'GET') return ok({'tables': [table ?? _table]});
        if (p.endsWith('/rows/query')) {
          sent.add('QUERY ${req.body}');
          final b = jsonDecode(req.body) as Map;
          final second = b['cursor'] == 'c2';
          return ok({
            'rows': second
                ? [
                    {'id': 3, 'name': 'Chidi'},
                  ]
                : [
                    {'id': 1, 'name': 'Ada Obi', 'phone': '+2348031234567', 'balance_kobo': 500000, 'status': 'overdue', 'paid': false},
                    {'id': 2, 'name': 'Bisi', 'phone': null, 'balance_kobo': 0, 'status': 'paid', 'paid': true},
                  ],
            'next_cursor': second ? null : 'c2',
            'count': 3,
          });
        }
        if (req.method == 'GET') return null;
        sent.add('${req.method} $p ${req.body}');
        return ok({'rows': [{'id': 9}]}, req.method == 'POST' ? 201 : 200);
      }),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('the rows in their words; searched; the next page', (t) async {
    final sent = await open(t, (c) => TkSheet(title: 'Customers', body: TableTool(auth: c.auth, table: _table, onSaved: (_) {})));
    expect(sent.first, 'QUERY {"limit":25,"count":"exact"}');
    expect(find.text('Ada Obi'), findsOneWidget);
    expect(find.text('Phone: +2348031234567 · Balance: ₦5,000 · Status: overdue · Paid: No'), findsOneWidget);
    expect(find.text('1–2 of 3'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Next page'));
    await settle(t);
    expect(sent.last, 'QUERY {"limit":25,"count":"exact","cursor":"c2"}');
    expect(find.text('Chidi'), findsOneWidget);
    expect(find.text('26–26 of 3'), findsOneWidget);
    await t.enterText(find.byType(EditableText), 'ada');
    await t.pump(const Duration(milliseconds: 350));
    await settle(t);
    final q = jsonDecode(sent.last.substring(6)) as Map;
    expect(q['cursor'], isNull, reason: 'a search starts at its first page');
    expect((q['filters'] as List).single, {
      'any': [
        {'column': 'name', 'op': 'ilike', 'value': '%ada%'},
        {'column': 'phone', 'op': 'ilike', 'value': '%ada%'},
        {'column': 'status', 'op': 'ilike', 'value': '%ada%'},
      ],
    });
  });

  testWidgets('a row changed: only what changed; a row added', (t) async {
    final lines = <String?>[];
    final sent = await open(t, (c) => TkSheet(title: 'Customers', body: TableTool(auth: c.auth, table: _table, onSaved: lines.add)));
    await t.tap(find.text('Ada Obi'));
    await settle(t);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Balance')), matching: find.byType(EditableText)), '0');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save changes'));
    await settle(t);
    expect(sent.last, 'PATCH /v1/tables/TB1/rows/1 {"set":{"balance_kobo":0}}');
    expect(lines.last, 'Customers: a row changed');
  });

  testWidgets('a new row needs what is required', (t) async {
    final lines = <String?>[];
    final sent = await open(t, (c) => TkSheet(title: 'Customers', body: TableTool(auth: c.auth, table: _table, onSaved: lines.add)));
    await t.tap(find.bySemanticsLabel('Add a row'));
    await settle(t);
    final add = find.bySemanticsLabel('Add the row');
    expect(t.getSemantics(add).flagsCollection.isEnabled.toString(), 'Tristate.isFalse');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Name')), matching: find.byType(EditableText)), 'Dayo');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Paid')), matching: find.byType(EditableText)), 'yes');
    await t.pump();
    await t.tap(add);
    await settle(t);
    expect(sent.last, 'POST /v1/tables/TB1/rows {"row":{"name":"Dayo","paid":true}}');
    expect(lines.last, 'Customers: a row added');
  });

  testWidgets('a table Taskiem keeps: said, and no row opened or added', (t) async {
    await open(t, (c) => TkSheet(title: 'Money log', body: TableTool(auth: c.auth, table: {..._table, 'kept_by': 'money_log'}, onSaved: (_) {})));
    expect(find.text(keptMessage), findsOneWidget);
    expect(find.bySemanticsLabel('Add a row'), findsNothing);
    await t.tap(find.text('Ada Obi'));
    await settle(t);
    expect(find.text('Name'), findsNothing, reason: 'not opened');
  });

  testWidgets('Tables: each table; a row removed after yes', (t) async {
    final sent = await open(t, (c) => TablesPage(auth: c.auth));
    expect(find.text('Customers'), findsOneWidget);
    await t.tap(find.text('Customers'));
    await settle(t);
    await t.tap(find.text('Ada Obi'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Remove this row'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Remove it'));
    await settle(t);
    expect(sent.last.startsWith('QUERY'), isTrue, reason: 'the rows read again');
    expect(sent.where((x) => x.startsWith('DELETE')).single, 'DELETE /v1/tables/TB1/rows/1 ');
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    final chats = ChatsData(
      f.auth(
        also: (req) async => req.url.path.endsWith('/rows/query')
            ? http.Response(jsonEncode({'rows': [{'id': 1, 'name': 'Ada Obi', 'status': 'paid', 'balance_kobo': 500000}], 'next_cursor': null, 'count': 1}), 200,
                headers: {'content-type': 'application/json'})
            : null,
      ),
      now: () => f.at,
    );
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: TkSheet(title: 'Customers', body: TableTool(auth: chats.auth, table: _table, onSaved: (_) {}))), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: TablePage(auth: chats.auth, table: _table)), drive: settle);
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
