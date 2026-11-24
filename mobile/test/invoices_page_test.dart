// Invoices (M8 part I; web Invoices.tsx, lib/invoices.ts): the list and its
// filters, one invoice (send and share, void, amend, a payment undone, the
// customer shown, a query settled), the editor and the settings.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/invoices_page.dart';
import 'package:taskiem/kit/forms.dart' show TkSwitch;
import 'package:taskiem/app/links.dart' show pickContact;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'guidelines.dart';
import 'saved_stub.dart';
import 'support.dart';

Map<String, dynamic> _invoice({String status = 'sent', bool disputed = false, String key = 'phone:+2348031234567'}) => {
  'id': 'I1',
  'number': 'INV-0007',
  'status': status,
  'kind': 'invoice',
  'disputed': disputed,
  'customer': {'name': 'Bisi Ade', 'phone': '+234*******567', 'email': '', 'key': key},
  'lines': [
    {'description': 'Jollof rice tray', 'quantity': 2, 'unit_kobo': 1500000},
    {'description': 'Delivery', 'quantity': 1, 'unit_kobo': 200000},
  ],
  'amendments': <Object>[],
  'total_kobo': 3200000,
  'paid_kobo': 1000000,
  'due_kobo': 2200000,
  'due_on': '2026-11-20',
  'created_at': '2026-11-10T09:00:00Z',
  'sent_at': status == 'draft' ? null : '2026-11-10T09:05:00Z',
  'connection': 'main',
  'fee_paid_by': 'business',
  'allow_part_payments': true,
  'payments': [
    {'id': 'P1', 'payment_id': 'PSK-991', 'amount_kobo': 1000000, 'how': 'exact', 'created_at': '2026-11-11T10:00:00Z'},
  ],
};

Map<String, dynamic> _saved(String id, String owner, String kind, String name, String title, String hint, Map<String, dynamic> details) => {
  'id': id,
  'owner': owner,
  'name': name,
  'title': title,
  'kind': kind,
  'hint': hint,
  'details': details,
  'check': {'state': 'none', 'account_name': null, 'via': null, 'at': null, 'problem': null},
  'to_change': true,
  'created_at': '2026-11-20T10:00:00Z',
  'updated_at': '2026-11-20T10:00:00Z',
};

void main() {
  setUpAll(loadFonts);

  test('the hero, sent, notes, amendments and payments in words', () {
    expect(invoiceHero(_invoice()).label, 'Still due');
    expect(invoiceHero(_invoice()).kobo, 2200000);
    expect(invoiceHero(_invoice(status: 'draft')).label, 'To ask for');
    expect(invoiceHero(_invoice(status: 'paid')).paid, 1.0);
    expect(invoicesQuery('credit'), {'kind': 'credit'});
    expect(invoicesQuery('overdue'), {'status': 'overdue'});
    expect(invoicesQuery(''), isEmpty);
    expect(
      sentLines({
        'queued': ['sms', 'email'],
        'credit_kobo': 50000,
      }),
      ['Sent by SMS and email.', '₦500 of the customer’s credit counts towards it.'],
    );
    expect(sentLines({'queued': [], 'sms_over_allowance': true}).last, 'SMS not sent: plan allowance used.');
    expect(lineNote({'pass_through_for': 'WAEC'}), 'Collected on behalf of WAEC.');
    expect(voidProblem(' '), isNotNull);
    expect(voidProblem('Wrong customer'), isNull);
    expect(removableLines(_invoice()).map((r) => r.index), [0, 1]);
    expect(
      removableLines({
        ..._invoice(),
        'amendments': [
          {'removes': 0},
        ],
      }).map((r) => r.index),
      [1],
    );
    expect(amendmentProblem(kind: 'add', amount: '500', description: '', line: '', reason: 'Extra'), 'Say what the added line is for.');
    expect(amendmentProblem(kind: 'discount', amount: '500', description: '', line: '', reason: 'Loyal'), isNull);
    expect(amendmentProblem(kind: 'remove', amount: '', description: '', line: '', reason: 'Gone'), 'Choose the item to take out.');
    final rows = paymentRows([
      {'id': 'P1', 'amount_kobo': 100},
      {'id': 'P2', 'amount_kobo': -100, 'undoes': 'P1', 'created_at': '2026-11-12T10:00:00Z'},
    ]);
    expect(rows.single.undoneBy?['id'], 'P2');
    expect(numberPreview('INV-', 7), 'INV-0007');
    expect(creditTermsWords({'credit_terms_at': '2026-01-01', 'credit_days': 14}), '14 days from the day a debt is recorded.');
    expect(fmtTime('2026-11-12T13:05:00Z'), '12 Nov 2026, 14:05');
  });

  Future<List<String>> open(
    WidgetTester t,
    Widget Function(ChatsData c) home, {
    Map<String, dynamic>? invoice,
    double height = 2400,
    bool voidFails = false,
    bool schools = false,
    Map<String, List<Map<String, dynamic>>> saved = const {},
    SavedStub? stub,
  }) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = Size(390, height);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
    final x = invoice ?? _invoice();
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          final key = req.headers['idempotency-key'] ?? req.headers['Idempotency-Key'];
          if (stub?.answer(req) case final r?) return r;
          if (req.method == 'GET') {
            if (saved[p] case final list?) return ok({'saved': list, 'limit': 200, 'can_check': false});
            if (p == '/v1/schools/receipts') {
              sent.add('RECEIPTS ${req.url.query}');
              return ok({
                'receipts': [
                  {'id': 'RC1', 'number': 'R-0042', 'part_id': 'P1', 'voided': false},
                ],
              });
            }
            if (p == '/v1/invoices') {
              sent.add('LIST ${req.url.query}');
              return ok({
                'invoices': [x],
              });
            }
            if (p.startsWith('/v1/invoices/')) {
              if (req.url.queryParameters['reveal'] == 'true') {
                sent.add('REVEAL');
                return ok({
                  'invoice': {
                    ...x,
                    'customer': {...x['customer'] as Map, 'phone': '+2348031234567', 'email': 'bisi@example.com'},
                  },
                });
              }
              return ok({'invoice': x});
            }
            if (p == '/v1/invoice-settings') {
              return ok({
                'prefix': 'INV-',
                'next_number': 8,
                'connection': 'main',
                'fee_paid_by': 'business',
                'allow_part_payments': false,
                'auto_match': false,
                'credit_terms_at': null,
                'credit_days': null,
              });
            }
            if (p == '/v1/links') {
              return ok({
                'links': [
                  {'business': 'B2', 'name': 'Chidi Foods', 'state': 'active', 'role': 'customer'},
                ],
              });
            }
            return null;
          }
          sent.add('${req.method} $p ${req.body}${key != null ? ' key' : ''}');
          if (voidFails && p.endsWith('/void')) {
            return http.Response(jsonEncode({'error': 'It cannot be voided now.'}), 409, headers: {'content-type': 'application/json'});
          }
          if (p.endsWith('/send')) {
            return ok({
              'invoice': x,
              'link': 'https://pay.taskiem.test/i/abc',
              'queued': ['sms'],
              'credit_kobo': 0,
            });
          }
          if (p == '/v1/invoices') return ok({...x, 'id': 'I9', 'number': 'INV-0008', 'status': 'draft'}, 201);
          if (p == '/v1/invoice-settings/credit-terms') {
            return ok({
              'settings': {'credit_terms_at': '2026-11-12T00:00:00Z', 'credit_days': 30},
              'applied': 3,
            });
          }
          if (p == '/v1/invoice-settings') return ok(jsonDecode(req.body) as Map);
          return ok({'ok': true});
        },
      ),
      now: () => f.at,
    );
    if (schools) chats.auth.me = Me.fromJson({...f.json('/v1/me'), 'schools': true});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(chats)));
    await settle(t);
    return sent;
  }

  testWidgets('the list: each invoice, a filter asked of the server', (t) async {
    final sent = await open(t, (c) => InvoicesPage(auth: c.auth));
    await looks(t, 'invoices');
    expect(sent.first, 'LIST ');
    expect(find.text('Bisi Ade'), findsOneWidget);
    expect(find.text('INV-0007 · ₦22,000 still due'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp(r'^Still due: ₦')), findsOneWidget, reason: 'what customers still owe, on top');
    expect(find.text('₦32,000'), findsOneWidget);
    await t.ensureVisible(find.bySemanticsLabel('Overdue'));
    await t.tap(find.bySemanticsLabel('Overdue'));
    await settle(t);
    expect(sent.last, 'LIST status=overdue');
    await t.tap(find.text('Bisi Ade'));
    await settle(t);
    expect(find.text('INV-0007'), findsOneWidget, reason: 'its number, the title');
  });

  testWidgets('an invoice sent again: what was sent, its page to share', (t) async {
    final sent = await open(t, (c) => InvoicePage(auth: c.auth, id: 'INV-0007', follow: false));
    expect(find.bySemanticsLabel('Still due'), findsOneWidget);
    expect(find.text('Jollof rice tray × 2'), findsOneWidget);
    expect(find.text('₦30,000'), findsOneWidget);
    expect(find.text('Sent by SMS.'), findsNothing);
    await t.tap(find.bySemanticsLabel('Send again'));
    await settle(t);
    expect(sent.last, 'POST /v1/invoices/I1/send {} key');
    expect(find.text('Sent by SMS.'), findsOneWidget);
    expect(find.bySemanticsLabel('Share its page'), findsOneWidget, reason: 'in the head, once the page is known');
    expect(find.bySemanticsLabel('Its page'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('void needs a reason; a payment undone after yes; the customer shown', (t) async {
    final sent = await open(t, (c) => InvoicePage(auth: c.auth, id: 'INV-0007', follow: false));
    await t.tap(find.bySemanticsLabel('More about this invoice'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Void'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Void invoice'));
    await t.pump();
    expect(find.text('Say why it is void. The customer’s page shows the reason.'), findsOneWidget);
    expect(sent.where((s) => s.contains('/void')), isEmpty);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Reason')), matching: find.byType(EditableText)), 'Wrong customer');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Void invoice'));
    await settle(t);
    expect(sent.last, 'POST /v1/invoices/I1/void {"reason":"Wrong customer"}');

    await t.tap(find.bySemanticsLabel('Undo the payment PSK-991'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Undo payment'));
    await settle(t);
    expect(sent.last, 'POST /v1/invoices/I1/payments/P1/undo {}');

    await t.tap(find.bySemanticsLabel('Show customer details'));
    await settle(t);
    expect(sent.last, 'REVEAL');
    expect(find.bySemanticsLabel('Call +2348031234567'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('amended: a discount with its reason', (t) async {
    final sent = await open(t, (c) => InvoicePage(auth: c.auth, id: 'INV-0007', follow: false), height: 3000);
    await t.tap(find.bySemanticsLabel('Amend the invoice'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Change: Add a line'));
    await t.pumpAndSettle();
    await t.tap(find.text('Give a discount'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Amount')), matching: find.byType(EditableText)), '2000');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Reason')), matching: find.byType(EditableText)), 'Loyal customer');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Amend the invoice').last);
    await settle(t);
    expect(sent.last, 'POST /v1/invoices/I1/amendments {"kind":"discount","amount_kobo":200000,"reason":"Loyal customer"}');
    expect(find.text('Invoice amended.'), findsOneWidget);
  });

  testWidgets('a query settled: the invoice stands', (t) async {
    final sent = await open(
      t,
      (c) => InvoicePage(auth: c.auth, id: 'INV-0007', follow: false),
      invoice: _invoice(disputed: true),
    );
    expect(find.text('Queried by the customer'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Settle the query'));
    await settle(t);
    expect(sent.last, 'POST /v1/invoices/INV-0007/settle-query {"how":"as_it_stands","note":""}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a new invoice: what it needs, then saved and sent', (t) async {
    final sent = await open(t, (c) => InvoiceEditorPage(auth: c.auth));
    await t.tap(find.bySemanticsLabel('Save and send'));
    await t.pump();
    expect(find.text('Give the customer’s name.'), findsOneWidget);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Name')), matching: find.byType(EditableText)), 'Bisi Ade');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Phone number')), matching: find.byType(EditableText)), '08031234567');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp(r'^Amount')), matching: find.byType(EditableText)), '25000');
    await t.pump();
    expect(find.bySemanticsLabel('Total ₦25,000'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Save and send'));
    await settle(t);
    final posts = sent.where((s) => s.startsWith('POST')).toList();
    expect(
      posts.first,
      'POST /v1/invoices {"customer":{"name":"Bisi Ade","phone":"08031234567"},"amount_kobo":2500000,"fee_paid_by":"business","allow_part_payments":false} key',
    );
    expect(posts.last, 'POST /v1/invoices/I9/send {} key');
    expect(find.text('Sent by SMS.'), findsOneWidget, reason: 'the invoice opens, saying what was sent');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a new invoice: the customer from the phone\'s contacts; an item\'s quantity by − and +', (t) async {
    final was = pickContact;
    pickContact = () async => (name: 'Bisi Ade', phone: '0803 123 4567');
    addTearDown(() => pickContact = was);
    await open(t, (c) => InvoiceEditorPage(auth: c.auth));
    await t.tap(find.bySemanticsLabel('Pick from your contacts'));
    await t.pump();
    expect(find.text('Bisi Ade'), findsOneWidget);
    expect(find.text('0803 123 4567'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('A list of items'));
    await t.pump();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp(r'^Price each')), matching: find.byType(EditableText)), '1500');
    await t.pump();
    await t.tap(find.bySemanticsLabel('One more item 1'));
    await t.pump();
    expect(find.bySemanticsLabel('Total ₦3,000'), findsOneWidget);
  });

  testWidgets('a new invoice: the customer from a saved person, the business’s or one’s own', (t) async {
    await open(
      t,
      (c) => InvoiceEditorPage(auth: c.auth),
      saved: {
        '/v1/me/saved': [_saved('M1', 'mine', 'person', 'mum', 'Mum', '', {'email': 'mum@example.com'})],
        '/v1/saved': [
          _saved('B1', 'business', 'person', 'tolu', 'Tolu Bakare', '••0099', {'phone': '+2348031120099', 'email': 'tolu@example.com'}),
          _saved('B2', 'business', 'place', 'shop', 'The shop', 'Yaba', {'line': '3 Herbert Macaulay Way'}),
        ],
      },
    );
    await t.tap(find.bySemanticsLabel('Pick from your saved people'));
    await t.pumpAndSettle();
    expect(find.text('Saved people'), findsOneWidget);
    expect(find.text('@tolu · ••0099 · The business’s'), findsOneWidget);
    expect(find.text('The shop'), findsNothing, reason: 'a place is not a person');
    await t.tap(find.text('Tolu Bakare'));
    await t.pumpAndSettle();
    expect(find.text('Tolu Bakare'), findsOneWidget);
    expect(find.text('+2348031120099'), findsOneWidget, reason: 'the details in full, not the masked hint');
    expect(find.text('tolu@example.com'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Pick from your saved people'));
    await t.pumpAndSettle();
    await t.tap(find.text('Mum'));
    await t.pumpAndSettle();
    expect(find.text('Mum'), findsOneWidget);
    expect(find.text('+2348031120099'), findsNothing, reason: 'a pick is the whole customer: Mum has no phone, so Tolu’s goes');
    expect(find.text('tolu@example.com'), findsNothing);
    expect(find.text('mum@example.com'), findsOneWidget);
  });

  testWidgets('a new invoice: Saved tapped twice opens one picker, both lists read at once', (t) async {
    final stub = SavedStub(mine: [savedThing('M1', 'mine', 'person', 'mum', 'Mum', '', {'phone': '+2348030000001'})]);
    await open(t, (c) => InvoiceEditorPage(auth: c.auth), stub: stub);
    final saved = find.bySemanticsLabel('Pick from your saved people');
    await t.tap(saved);
    await t.tap(saved, warnIfMissed: false);
    await t.pumpAndSettle();
    expect(find.text('Saved people'), findsOneWidget);
    expect(stub.calls, unorderedEquals(['GET /v1/me/saved', 'GET /v1/saved']), reason: 'one read of each, not two');
    await t.tap(find.text('Mum'));
    await t.pumpAndSettle();
    expect(find.text('+2348030000001'), findsOneWidget);
  });

  for (final status in [500, 403]) {
    testWidgets('a new invoice: the business’s list not read ($status), one’s own still offered', (t) async {
      final stub = SavedStub(mine: [savedThing('M1', 'mine', 'person', 'mum', 'Mum', '', {'phone': '+2348030000001'})], fail: {'/v1/saved': status});
      await open(t, (c) => InvoiceEditorPage(auth: c.auth), stub: stub);
      await t.tap(find.bySemanticsLabel('Pick from your saved people'));
      await t.pumpAndSettle();
      expect(find.text('Saved people'), findsOneWidget);
      expect(find.text('Mum'), findsOneWidget);
      expect(find.textContaining('cannot be read'), findsNothing, reason: 'no toast while something can be picked');
      expect(find.textContaining('may not see'), findsNothing);
    });
  }

  testWidgets('a new invoice: both lists not read, or only the business’s with nothing of the kind, is said', (t) async {
    await open(t, (c) => InvoiceEditorPage(auth: c.auth), stub: SavedStub(fail: {'/v1/me/saved': 500, '/v1/saved': 500}));
    await t.tap(find.bySemanticsLabel('Pick from your saved people'));
    await t.pump();
    await t.pump();
    expect(find.text('Saved people'), findsNothing);
    expect(find.text('Saved things cannot be read now.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
    await open(
      t,
      (c) => InvoiceEditorPage(auth: c.auth),
      stub: SavedStub(mine: [savedThing('P1', 'mine', 'place', 'home', 'Home', 'Yaba', {'line': '3 Herbert Macaulay Way'})], fail: {'/v1/saved': 500}),
    );
    await t.tap(find.bySemanticsLabel('Pick from your saved people'));
    await t.pump();
    await t.pump();
    expect(find.text('Saved people'), findsNothing);
    expect(find.text('Saved things cannot be read now. Nothing of this kind in what could be read.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a new invoice: the due date from a date saved on a thing; a hidden date left out', (t) async {
    final stub = SavedStub(
      mine: [
        savedThing('C1', 'mine', 'custom', 'car', 'The car', 'LND 123', const {}, kindId: 'K1', values: {'insurance_due': '2027-03-03', 'bought': '••', 'plate': 'LND 123'}),
        savedThing('M1', 'mine', 'person', 'mum', 'Mum', '', {'birthday': '1970-03-21'}),
      ],
      myKinds: [
        savedKind('K1', 'Car', [
          {'key': 'insurance_due', 'label': 'Insurance due', 'type': 'date'},
          {'key': 'bought', 'label': 'Bought', 'type': 'date', 'sensitive': true},
          {'key': 'plate', 'label': 'Plate', 'type': 'text'},
        ]),
      ],
    );
    await open(t, (c) => InvoiceEditorPage(auth: c.auth), stub: stub);
    await t.tap(find.bySemanticsLabel('Pick a date from your saved things'));
    await t.pumpAndSettle();
    expect(find.text('Saved dates'), findsOneWidget);
    expect(find.text('@car · Insurance due · 3 Mar 2027'), findsOneWidget);
    expect(find.textContaining('Bought'), findsNothing, reason: 'a sensitive date is masked: left out');
    expect(find.textContaining('@mum · Birthday'), findsOneWidget);
    await t.tap(find.text('@car · Insurance due · 3 Mar 2027'));
    await t.pumpAndSettle();
    expect(find.text('2027-03-03'), findsOneWidget);
  });

  testWidgets('a new invoice: nothing saved of the kind is said', (t) async {
    await open(t, (c) => InvoiceEditorPage(auth: c.auth), saved: {'/v1/me/saved': [], '/v1/saved': []});
    await t.tap(find.bySemanticsLabel('Pick from your saved people'));
    await t.pump();
    expect(find.text('Nothing saved of this kind yet'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a new invoice for a linked business: no phone asked', (t) async {
    final sent = await open(t, (c) => InvoiceEditorPage(auth: c.auth));
    // A linked business, by its face.
    await t.tap(find.bySemanticsLabel('Who is it for: Chidi Foods (linked)'));
    await t.pumpAndSettle();
    expect(find.bySemanticsLabel(RegExp('^Phone number')), findsNothing);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp(r'^Amount')), matching: find.byType(EditableText)), '9000');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save draft'));
    await settle(t);
    expect(
      sent.where((s) => s.startsWith('POST')).single,
      startsWith('POST /v1/invoices {"customer":{"name":"Chidi Foods","key":"link:B2"},"amount_kobo":900000'),
    );
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('settings: the number only moves forward; credit terms saved', (t) async {
    final sent = await open(t, (c) => InvoiceSettingsPage(auth: c.auth));
    expect(find.textContaining('The next invoice is INV-0008.'), findsOneWidget);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Next number')), matching: find.byType(EditableText)), '3');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save settings'));
    await t.pump();
    expect(find.textContaining('can only move forward'), findsOneWidget);
    expect(sent, isEmpty);
    await t.tap(find.text('30 days'));
    await t.pump();
    await t.tap(find.byType(TkSwitch).last);
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save credit terms'));
    await settle(t);
    expect(sent.last, 'PUT /v1/invoice-settings/credit-terms {"credit_days":30,"apply_open":true}');
    expect(find.text('3 open debts now follow these terms.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('M8 fixes 6, 23, 29: a line added after sending says so; the query asks its question; the payments say where matching is', (t) async {
    final x = _invoice(disputed: true);
    x['lines'] = [
      ...(x['lines'] as List),
      {'description': 'Extra tray', 'quantity': 1, 'unit_kobo': 500000, 'amendment': 'A1'},
    ];
    x['payments'] = <Object>[];
    await open(
      t,
      (c) => InvoicePage(auth: c.auth, id: 'INV-0007', follow: false),
      invoice: x,
      height: 3200,
    );
    expect(find.textContaining('Added after the invoice was sent.'), findsOneWidget);
    expect(find.text('How do you settle it?'), findsOneWidget);
    expect(find.textContaining('matched to this invoice on To match.'), findsOneWidget);
  });

  testWidgets('M8 fixes 8: a void that fails says so in its own card; Keep it clears it', (t) async {
    await open(t, (c) => InvoicePage(auth: c.auth, id: 'INV-0007', follow: false), voidFails: true);
    await t.tap(find.bySemanticsLabel('More about this invoice'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Void'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Reason')), matching: find.byType(EditableText)), 'Wrong customer');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Void invoice'));
    await settle(t);
    final err = find.text('It cannot be voided now.');
    expect(err, findsOneWidget);
    expect(t.getTopLeft(err).dy, greaterThan(t.getTopLeft(find.bySemanticsLabel('Void invoice')).dy), reason: 'under its button, in its card');
    await t.tap(find.bySemanticsLabel('Keep it').first);
    await t.pump();
    expect(err, findsNothing);
  });

  testWidgets('M8 fixes 22: a school sees each payment’s receipt', (t) async {
    final sent = await open(t, (c) => InvoicePage(auth: c.auth, id: 'INV-0007', follow: false), schools: true, height: 3200);
    expect(sent, contains('RECEIPTS invoice=I1'));
    expect(find.text('Receipt R-0042'), findsOneWidget);
    expect(find.bySemanticsLabel('Open PDF of receipt R-0042'), findsOneWidget);
  });

  testWidgets('M8 fixes 7: saving credit terms keeps the numbering typed but not saved', (t) async {
    await open(t, (c) => InvoiceSettingsPage(auth: c.auth));
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Number prefix')), matching: find.byType(EditableText)), 'TK-');
    await t.pump();
    await t.tap(find.text('30 days'));
    await t.pump();
    await t.tap(find.bySemanticsLabel('Save credit terms'));
    await settle(t);
    expect(find.text('TK-'), findsOneWidget, reason: 'not put back to INV-');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('M8 fixes 24: a draft changed from its screen goes back to it, not to a second copy', (t) async {
    await open(
      t,
      (c) => InvoicePage(auth: c.auth, id: 'INV-0007', follow: false),
      invoice: _invoice(status: 'draft'),
    );
    await t.tap(find.bySemanticsLabel('More about this invoice'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Edit'));
    await t.pumpAndSettle();
    await settle(t);
    await t.tap(find.bySemanticsLabel('Save draft'));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.byType(InvoicePage), findsOneWidget);
    expect(find.byType(InvoiceEditorPage), findsNothing);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final chats = ChatsData(
      f.auth(
        also: (req) async => switch (req.url.path) {
          '/v1/invoices' => ok({
            'invoices': [_invoice()],
          }),
          '/v1/invoices/INV-0007' => ok({'invoice': _invoice()}),
          '/v1/invoice-settings' => ok({'prefix': 'INV-', 'next_number': 8, 'connection': 'main', 'fee_paid_by': 'business'}),
          '/v1/links' => ok({'links': []}),
          _ => null,
        },
      ),
      now: () => f.at,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: InvoicesPage(auth: chats.auth),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.dark,
        home: InvoicePage(auth: chats.auth, id: 'INV-0007', follow: false),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: InvoiceEditorPage(auth: chats.auth),
      ),
      drive: settle,
    );
    await expectLargeText(
      t,
      TaskiemApp(
        mode: TkMode.light,
        home: InvoiceSettingsPage(auth: chats.auth),
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
