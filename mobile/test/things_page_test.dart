// Things we have (what a business buys §5.3, U3): the list with its totals
// by label and those no longer had; a row's page, marked sold with a price
// (money in) and, by someone who may not enter money, without one; added
// by hand; and spending by label in Money.

import 'dart:convert';
import 'dart:io' show Platform;

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me, Thing;
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/chat/spending.dart';
import 'package:taskiem/chat/things_page.dart';
import 'package:taskiem/kit/kit.dart' show TkRadio;
import 'package:taskiem/kit/mobile.dart' show TkListRow, TkTick;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart';
import 'things_fixture.dart';
import 'treat_fixture.dart';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  /// Signed in as the fixture's owner, or with only [perms].
  Auth signIn(List<String> sent, {List<String>? perms}) {
    final f = Fixture('chats');
    http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
    final auth = f.auth(
      also: (req) async {
        if (req.method == 'GET') sent.add('GET ${req.url.path}${req.url.query.isEmpty ? '' : '?${req.url.query}'}');
        if (req.method == 'GET' && req.url.path.startsWith('/v1/things/')) {
          final m = _more.where((x) => x['id'] == req.url.path.split('/').last).firstOrNull;
          if (m != null) return ok({'thing': m});
        }
        final a = thingsAnswers(req) ?? treatAnswers(req);
        if (a != null) return a;
        if (req.method == 'GET') return null;
        sent.add('${req.method} ${req.url.path} ${req.body}${req.headers['Idempotency-Key'] != null ? ' keyed' : ''}');
        if (req.method == 'PATCH' && req.url.path.startsWith('/v1/things/')) {
          final was = [...things, ..._more].firstWhere((t) => t['id'] == req.url.path.split('/').last);
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          return ok({
            'thing': {...was, if (b['status'] != null) 'status': b['status'], 'status_at': '2026-11-22T10:00:00Z', if (b['sale_price_kobo'] != null) 'sale_entry_id': 'me-new'},
          });
        }
        if (req.method == 'POST' && req.url.path == '/v1/things') {
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          return ok({'thing': thing('th-new', b['name'] as String, cost: b['cost_kobo'] as int?, labelId: b['label_id'] as String?)}, 201);
        }
        return null;
      },
    );
    if (perms != null) auth.me = Me.fromJson({...f.json('/v1/me'), 'permissions': perms});
    return auth;
  }

  Future<List<String>> open(WidgetTester t, Widget Function(Auth a) home, {TkMode? mode, List<String>? perms, double width = 390}) async {
    t.view.physicalSize = Size(width, 1400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final sent = <String>[];
    final auth = signIn(sent, perms: perms);
    await t.pumpWidget(TaskiemApp(mode: mode ?? (Platform.environment['SNAP_DARK'] == '1' ? TkMode.dark : TkMode.light), home: home(auth)));
    await settle(t);
    return sent;
  }

  testWidgets('the list: what we still have, by label, and those no longer had', (t) async {
    final sent = await open(t, (a) => ThingsPage(auth: a));
    expect(sent, contains('GET /v1/things?group=label'));
    expect(find.text('Things we have'), findsWidgets);
    expect(find.text('at what they cost'), findsOneWidget);
    expect(find.text('Sewing machine'), findsOneWidget);
    expect(find.text('Machines · Singer Ltd · 3 Nov 2026'), findsOneWidget);
    expect(find.text('Not labelled · 2 Sep 2026'), findsOneWidget);
    expect(find.text('Old freezer'), findsNothing);
    await looks(t, 'things');
    await t.tap(find.bySemanticsLabel('By label'));
    await t.pumpAndSettle();
    expect(find.text('₦770,000'), findsOneWidget, reason: 'Machines together');
    expect(find.text('Not labelled'), findsWidgets);
    await t.tap(find.text('Show those no longer had'));
    await settle(t);
    expect(sent, contains('GET /v1/things?all=true&group=label'));
    expect(find.text('No longer had'), findsOneWidget);
    expect(find.text('Old freezer'), findsOneWidget);
    expect(find.text('Sold'), findsOneWidget);
    await snap(t, 'things-by-label');
  });

  testWidgets('a row’s page: sold with a price is money in', (t) async {
    final sent = await open(t, (a) => ThingsPage(auth: a));
    await t.tap(find.text('Sewing machine'));
    await settle(t);
    expect(find.text('SN-88213'), findsOneWidget);
    expect(find.text('3 Nov 2027'), findsOneWidget);
    expect(find.text('Order PO-0012'), findsOneWidget);
    await looks(t, 'thing');
    await t.tap(find.text('Sold'));
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Sold for')), matching: find.byType(EditableText)), '380000');
    await t.pump();
    await t.tap(find.text('By transfer'));
    await t.pump();
    await snap(t, 'thing-sold-sheet');
    await t.tap(find.bySemanticsLabel('Sold for ₦380,000'));
    await settle(t);
    expect(sent.last, 'PATCH /v1/things/th-1 {"sale_paid_how":"transfer","sale_price_kobo":38000000,"status":"sold"} keyed');
    expect(find.text('Marked sold, ₦380,000 in'), findsOneWidget);
    expect(find.textContaining('its sale is in money in'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('sold with its money recorded: it stays sold', (t) async {
    final sent = await open(t, (a) => ThingPage(auth: a, thing: _of('th-7')));
    final asked = sent.length;
    await t.tap(find.text('Broken'));
    await t.pumpAndSettle();
    expect(sent.length, asked, reason: 'nothing is sent: the server would refuse it');
    expect(find.text('Deep fryer was sold with its money recorded, so it stays sold'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a cashier marks it sold, but cannot price it', (t) async {
    final sent = await open(t, (a) => ThingPage(auth: a, thing: _of('th-2')), perms: const ['table.read', 'table.edit', 'invoice.read']);
    await t.tap(find.text('Sold'));
    await t.pumpAndSettle();
    expect(find.text('Marked sold without a price'), findsOneWidget);
    await snap(t, 'thing-sold-cashier');
    expect(find.bySemanticsLabel(RegExp('^Sold for')), findsNothing);
    await t.tap(find.bySemanticsLabel('Mark it sold'));
    await settle(t);
    expect(sent.last, 'PATCH /v1/things/th-2 {"status":"sold"}');
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('sold without a price: the price added later, as money in', (t) async {
    final sent = await open(t, (a) => ThingPage(auth: a, thing: _of('th-5')));
    expect(find.text('Add what it sold for'), findsOneWidget);
    await snap(t, 'thing-sold-no-price');
    await t.tap(find.text('Sold').last);
    await t.pumpAndSettle();
    expect(find.text('What it sold for'), findsOneWidget, reason: 'tapping Sold again asks the price too');
    expect(find.text('Sold for (optional)'), findsNothing);
    await snap(t, 'thing-add-price');
    await t.tap(find.bySemanticsLabel('Add the price'));
    await t.pumpAndSettle();
    expect(sent.where((s) => s.startsWith('PATCH')), isEmpty, reason: 'no price, nothing to send');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Sold for')), matching: find.byType(EditableText)), '60000');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Sold for ₦60,000'));
    await settle(t);
    expect(sent.last, 'PATCH /v1/things/th-5 {"sale_paid_how":"cash","sale_price_kobo":6000000,"status":"sold"} keyed');
    expect(find.text('₦60,000 in for Bicycle'), findsOneWidget);
    expect(find.textContaining('its sale is in money in'), findsOneWidget);
    expect(find.text('Add what it sold for'), findsNothing);
    await t.pump(const Duration(seconds: 5));

    // Someone who may not enter money is not offered it.
    await open(t, (a) => ThingPage(auth: a, thing: _of('th-5')), perms: const ['table.read', 'table.edit', 'invoice.read']);
    expect(find.text('Add what it sold for'), findsNothing);
    await t.tap(find.text('Sold').last);
    await t.pumpAndSettle();
    expect(find.text('What it sold for'), findsNothing);
  });

  testWidgets('its sale recorded already: sold again without asking a price', (t) async {
    final sent = await open(t, (a) => ThingPage(auth: a, thing: _of('th-6')));
    await t.tap(find.text('Sold').last);
    await settle(t);
    expect(find.text('Sold for (optional)'), findsNothing, reason: 'the server takes no second price');
    expect(sent.last, 'PATCH /v1/things/th-6 {"status":"sold"}');
    expect(find.text('Marked sold; its sale was recorded already'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('added by hand: the day, not who added it as an id', (t) async {
    await open(t, (a) => ThingPage(auth: a, thing: _of('th-3')));
    await t.scrollUntilVisible(find.text('Added by hand'), 200);
    expect(find.text('Added by hand'), findsOneWidget);
    expect(find.text('2 Sep 2026'), findsOneWidget);
    expect(find.textContaining('7d1f3c2a'), findsNothing, reason: 'created_by is an actor id, not a name');
    expect(find.textContaining('By '), findsNothing);
  });

  testWidgets('from an order: its rows asked of the server by the order', (t) async {
    final sent = await open(t, (a) => ThingsPage(auth: a, order: 'po-1', orderReference: 'PO-0012', backLabel: 'Back to the order'));
    expect(sent, contains('GET /v1/things?all=true&order=po-1'));
    expect(sent.where((s) => s.startsWith('GET /v1/things?') && !s.contains('order=')), isEmpty, reason: 'not the whole list filtered on the phone');
    expect(find.text('From PO-0012: 1 thing'), findsOneWidget);
    expect(find.text('Sewing machine'), findsOneWidget);
    expect(find.text('Generator'), findsNothing);
  });

  testWidgets('the status is chosen from rows ticked, not radios', (t) async {
    await open(t, (a) => ThingPage(auth: a, thing: _of('th-2')));
    expect(find.byType(TkRadio), findsNothing);
    expect(find.descendant(of: find.ancestor(of: find.text('Still with the business'), matching: find.byType(TkListRow)), matching: find.byType(TkTick)), findsOneWidget);
    expect(find.byType(TkTick), findsNWidgets(5));
  });

  testWidgets('seen, not changed, without table.edit', (t) async {
    final sent = await open(t, (a) => ThingPage(auth: a, thing: _of('th-2')), perms: const ['table.read']);
    expect(find.text('You can see this, not change it'), findsOneWidget);
    await t.tap(find.text('Broken'));
    await settle(t);
    expect(sent.where((s) => s.startsWith('PATCH')), isEmpty);
  });

  testWidgets('added by hand, with a label picked', (t) async {
    final sent = await open(t, (a) => ThingsPage(auth: a));
    await t.tap(find.bySemanticsLabel('Add something we have'));
    await settle(t);
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Name')), matching: find.byType(EditableText)), 'Laptop');
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^What it cost')), matching: find.byType(EditableText)), '650000');
    await t.pump();
    await t.tap(find.bySemanticsLabel(RegExp('^Label')).last);
    await settle(t);
    await t.tap(find.text('Machines').last);
    await settle(t);
    await t.tap(find.bySemanticsLabel(RegExp('^From whom')).last);
    await t.pumpAndSettle();
    await t.tap(find.text('Singer Ltd').last);
    await t.pumpAndSettle();
    await t.enterText(find.descendant(of: find.bySemanticsLabel(RegExp('^Serial number')), matching: find.byType(EditableText)), 'LT-1');
    await t.pump();
    await snap(t, 'thing-add');
    await t.tap(find.bySemanticsLabel('Add it'));
    await settle(t);
    expect(sent.where((s) => s.startsWith('POST')).single, 'POST /v1/things {"cost_kobo":65000000,"label_id":"lb-mach","name":"Laptop","serial":"LT-1","supplier_id":"sup-singer"}');
    expect(find.text('Laptop is on Things we have'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('spending by label: the card on Money, and the month’s page', (t) async {
    final sent = await open(t, (a) => SpendingPage(auth: a, now: () => DateTime.utc(2026, 11, 22, 12)));
    expect(sent, contains('GET /v1/money/spending?month=2026-11'));
    expect(find.text('₦1,210,000'), findsOneWidget, reason: 'profit, Bought to keep left out');
    expect(find.text('on Things we have, left out of profit'), findsOneWidget);
    expect(find.text('Machines'), findsOneWidget);
    expect(find.text('Not labelled'), findsOneWidget);
    expect(find.text('₦300,000 to sell or make with · 3 entries'), findsOneWidget);
    await looks(t, 'spending');
    await t.tap(find.bySemanticsLabel('October'));
    await settle(t);
    expect(sent, contains('GET /v1/money/spending?month=2026-10'));

    final sent2 = await open(t, (a) => SingleChildScrollView(child: SpendingCard(auth: a, from: '2026-11-01', to: '2026-11-22')));
    expect(sent2, contains('GET /v1/money/spending?from=2026-11-01&to=2026-11-22'));
    expect(find.text('Spending by label'), findsOneWidget);
    expect(find.text('Diesel'), findsOneWidget);
    expect(find.text('Not labelled'), findsNothing, reason: 'the three spent on most');
    await snap(t, 'spending-card');
  });

  testWidgets('at 340 px and large text: nothing cut off, all pressable', (t) async {
    await open(t, (a) => ThingsPage(auth: a), mode: TkMode.dark, width: 340);
    await snap(t, 'things-340');
    await expectPressable(t);
    await open(t, (a) => ThingPage(auth: a, thing: _of('th-4')), mode: TkMode.dark, width: 340);
    await snap(t, 'thing-sold-340');
    await expectPressable(t);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: ThingsPage(auth: signIn([]))), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: ThingPage(auth: signIn([]), thing: _of('th-1'))), drive: settle);
    await expectLargeText(t, TaskiemApp(mode: TkMode.light, home: SpendingPage(auth: signIn([]))), drive: settle);
  });
}

/// Rows only these tests read: sold without a price, and had again after
/// a sale with one.
final _more = [
  thing('th-5', 'Bicycle', status: 'sold', cost: 9000000, received: '2026-08-10T10:00:00Z'),
  thing('th-6', 'Chest freezer', cost: 15000000, received: '2026-07-01T10:00:00Z', sale: 'me-7'),
  thing('th-7', 'Deep fryer', status: 'sold', cost: 8000000, received: '2026-06-01T10:00:00Z', sale: 'me-8'),
];

/// A row of the fixture as the app reads it.
Thing _of(String id) => Thing.fromJson(jsonDecode(jsonEncode([...things, ..._more].firstWhere((t) => t['id'] == id))) as Map<String, dynamic>);
