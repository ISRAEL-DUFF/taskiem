// A linked business's statement (M8 part S; web Statement.tsx,
// lib/statements.ts): what each side owes, its terms and limit, and each
// open document leading to its screen.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/openers.dart';
import 'package:taskiem/chat/bills_page.dart' show BillPage;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/invoices_page.dart' show InvoicePage;
import 'package:taskiem/chat/statement_page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

final _statement = {
  'business': 'Rivers Foods',
  'they_owe': {
    'owed_kobo': 5000000,
    'overdue_kobo': 2000000,
    'oldest_due': '2026-11-01',
    'terms_days': 14,
    'credit_limit_kobo': 4000000,
    'available_kobo': -1000000,
    'documents': [
      {'id': 'I1', 'reference': 'INV-0007', 'due_kobo': 2000000, 'total_kobo': 3000000, 'paid_kobo': 1000000, 'due_on': '2026-11-01', 'overdue': true},
    ],
  },
  'we_owe': {
    'owed_kobo': 700000,
    'overdue_kobo': 0,
    'documents': [
      {'id': 'B1', 'reference': 'INV-0042', 'due_kobo': 700000, 'total_kobo': 700000, 'paid_kobo': 0, 'disputed': true},
    ],
  },
};

void main() {
  setUpAll(loadFonts);

  test('terms in words', () {
    expect(termsWords(null), '');
    expect(termsWords(0), 'pay at once');
    expect(termsWords(1), '1 day to pay');
    expect(termsWords(30), '30 days to pay');
  });

  Future<ChatsData> chatsWith(Object statement, [List<String>? asked]) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    return ChatsData(
      f.auth(
        also: (req) async => switch ((asked?..add(req.url.path), req.url.path).$2) {
          '/v1/links/L1/statement' => ok(statement),
          '/v1/invoices/I1' => ok({
            'invoice': {'id': 'I1', 'number': 'INV-0007', 'status': 'overdue', 'customer': {}, 'lines': [], 'payments': []},
          }),
          _ => null,
        },
      ),
      now: () => f.at,
    );
  }

  testWidgets('each side: what is owed, the terms, the limit; a document opened', (t) async {
    t.view.physicalSize = const Size(390, 1800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final chats = await chatsWith(_statement);
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: StatementPage(auth: chats.auth, link: 'L1'),
      ),
    );
    await settle(t);
    await snap(t, 'statement_page-0');
    expect(find.text('Statement with Rivers Foods'), findsOneWidget);
    expect(find.text('Rivers Foods owes you'), findsWidgets);
    expect(find.text('Over the limit'), findsOneWidget);
    expect(find.textContaining('Terms: 14 days to pay'), findsOneWidget);
    expect(find.textContaining('Left to use ₦0'), findsOneWidget);
    expect(find.text('Was due 1 Nov 2026 · ₦10,000 of ₦30,000 paid'), findsOneWidget);
    expect(find.text('No due date · queried'), findsOneWidget);
    await t.tap(find.text('INV-0007'));
    await t.pumpAndSettle();
    expect(find.byType(InvoicePage), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Back').first);
    await t.pumpAndSettle();
    await t.tap(find.text('INV-0042'));
    await t.pumpAndSettle();
    expect(find.byType(BillPage), findsOneWidget);
  });

  testWidgets('M8 fixes 4, 19: read again on return from a document; Chat opens their conversation', (t) async {
    t.view.physicalSize = const Size(390, 1800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final opened = <String>[];
    Openers.conversation = (context, link) {
      opened.add(link);
      return true;
    };
    addTearDown(() => Openers.conversation = null);
    final asked = <String>[];
    final chats = await chatsWith(_statement, asked);
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: StatementPage(auth: chats.auth, link: 'L1'),
      ),
    );
    await settle(t);
    await snap(t, 'statement_page-1');
    await t.tap(find.bySemanticsLabel('Chat with Rivers Foods'));
    expect(opened, ['L1']);
    final before = asked.where((p) => p == '/v1/links/L1/statement').length;
    await t.tap(find.text('INV-0007'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Back').first);
    await t.pumpAndSettle();
    await settle(t);
    expect(asked.where((p) => p == '/v1/links/L1/statement').length, before + 1);
  });

  testWidgets('nothing open between you', (t) async {
    final chats = await chatsWith({'business': 'Rivers Foods'});
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: StatementPage(auth: chats.auth, link: 'L1'),
      ),
    );
    await settle(t);
    await snap(t, 'statement_page-2');
    expect(find.text('Nothing is open between you and Rivers Foods.'), findsOneWidget);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final chats = await chatsWith(_statement);
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: StatementPage(auth: chats.auth, link: 'L1'),
      ),
      drive: settle,
    );
  });
}

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}
