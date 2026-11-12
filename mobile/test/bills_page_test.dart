// Bills (M8 part B; web Bills.tsx, lib/bills.ts): the list and its hero,
// one bill (Pay from Taskiem sent for approval, set up first; how to pay;
// paid outside Taskiem; a query) and what each sends.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/chat/bills_page.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

Map<String, dynamic> _bill({String status = 'open', bool disputed = false}) => {
  'id': 'B1',
  'reference': 'INV-0042',
  'supplier_name': 'Bayo Rice Mills',
  'status': status,
  'disputed': disputed,
  'amount_kobo': 15600000,
  'paid_kobo': 5600000,
  'due_kobo': 10000000,
  'sent_kobo': 0,
  'paying_kobo': 0,
  'declared_paid_kobo': 0,
  'due_on': '2026-11-20',
  'pay_account_number': '9900000001',
  'pay_bank_name': 'Wema Bank',
  'pay_link': 'https://pay.taskiem.test/i/b1',
  'mirrored_at': '2026-11-12T08:00:00Z',
  'lines': [
    {'description': 'Rice 50kg', 'quantity': 2, 'unit_kobo': 7800000},
  ],
};

void main() {
  setUpAll(loadFonts);

  test('what is left to pay on the bills', () {
    expect(billsDue([_bill(), _bill(status: 'void'), _bill()]), 20000000);
    expect(billPath('B 1', '/pay'), '/v1/bills/B%201/pay');
  });

  Future<List<String>> open(WidgetTester t, Widget Function(ChatsData c) home, {Map<String, dynamic>? bill, bool ready = true}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 2600);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    var set = ready;
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET') {
            if (p == '/v1/bills') {
              sent.add('LIST ${req.url.query}');
              return ok({
                'bills': req.url.queryParameters['status'] == 'paid' ? [_bill(status: 'paid')..['id'] = 'B2'] : [bill ?? _bill()],
              });
            }
            if (p == '/v1/bills/pay') return ok({'workflow_id': set ? 'W1' : '', 'approver_role': 'owner'});
            if (p.startsWith('/v1/bills/')) return ok(bill ?? _bill());
            return null;
          }
          sent.add('${req.method} $p ${req.body}');
          if (p == '/v1/bills/pay/setup') set = true;
          return ok({'ok': true});
        },
      ),
      now: () => f.at,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('the list: left to pay, then paid', (t) async {
    final sent = await open(t, (c) => BillsPage(auth: c.auth));
    expect(sent, containsAll(['LIST ', 'LIST status=paid']));
    expect(find.text('₦100,000'), findsWidgets);
    expect(find.text('1 bill'), findsOneWidget);
    expect(find.text('Paid'), findsWidgets);
    await t.tap(find.text('Bayo Rice Mills').first);
    await settle(t);
    expect(find.text('INV-0042 from Bayo Rice Mills'), findsOneWidget);
  });

  testWidgets('a bill paid from Taskiem: sent for approval', (t) async {
    final sent = await open(t, (c) => BillPage(auth: c.auth, id: 'B1', follow: false));
    expect(find.textContaining('once someone with the owner role approves it'), findsOneWidget);
    expect(find.textContaining('9900000001'), findsOneWidget);
    expect(find.text('Rice 50kg'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Pay ₦100,000'));
    await settle(t);
    expect(sent.last, 'POST /v1/bills/B1/pay {}');
    expect(find.text('Sent for approval'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('paying bills set up first', (t) async {
    final sent = await open(t, (c) => BillPage(auth: c.auth, id: 'B1', follow: false), ready: false);
    await t.tap(find.bySemanticsLabel('Set up paying bills'));
    await settle(t);
    expect(sent.last, 'PUT /v1/bills/pay/setup {}');
    expect(find.bySemanticsLabel('Pay ₦100,000'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('paid outside Taskiem; a query', (t) async {
    final sent = await open(t, (c) => BillPage(auth: c.auth, id: 'B1', follow: false));
    await t.tap(find.bySemanticsLabel('Paid outside Taskiem'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Amount')), matching: find.byType(EditableText)), '25,000');
    await t.tap(find.text('In cash'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Record it'));
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/bills/B1/declare {"amount_kobo":2500000,"how":"cash"}');
    await t.pump(const Duration(seconds: 5));
    await t.tap(find.bySemanticsLabel('Query this bill'));
    await t.pumpAndSettle();
    final send = find.bySemanticsLabel('Send the query');
    expect(t.getSemantics(send).flagsCollection.isEnabled.toString(), 'Tristate.isFalse');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^What is wrong')), matching: find.byType(EditableText)), 'Short by one bag');
    await t.pump();
    await t.tap(send);
    await settle(t);
    await t.pumpAndSettle();
    expect(sent.last, 'POST /v1/bills/B1/query {"reason":"Short by one bag"}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a queried bill says so, and is not paid from Taskiem', (t) async {
    await open(
      t,
      (c) => BillPage(auth: c.auth, id: 'B1', follow: false),
      bill: _bill(disputed: true),
    );
    expect(find.text('You queried this bill. It is not paid from Taskiem until Bayo Rice Mills settles the query.'), findsOneWidget);
    expect(find.bySemanticsLabel('Query this bill'), findsNothing);
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async => switch (req.url.path) {
          '/v1/bills' => ok({
            'bills': [_bill()],
          }),
          '/v1/bills/pay' => ok({'workflow_id': 'W1', 'approver_role': 'owner'}),
          '/v1/bills/B1' => ok(_bill()),
          _ => null,
        },
      ),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: BillsPage(auth: chats.auth),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: BillPage(auth: chats.auth, id: 'B1', follow: false),
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
