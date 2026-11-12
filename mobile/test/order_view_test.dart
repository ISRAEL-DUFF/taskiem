// An order in the Orders sheet (M7 part V; web ChatToolForms.tsx OrderView,
// TradeOrderView, DeliveryView): what each button sends and the line it
// leaves.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/semantics.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/attachments.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/composer.dart';
import 'package:taskiem/chat/order_view.dart';
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'support.dart';

class _Camera extends Pickers {
  const _Camera();
  @override
  Future<PickedFile?> camera({double? side}) async => PickedFile(bytes: Uint8List.fromList([1, 2, 3]), name: 'door.jpg', type: 'image/jpeg');
}

void main() {
  setUpAll(loadFonts);

  final order = {
    'id': 'o1',
    'order_reference': 'AD-7Q4K2P',
    'buyer_name': 'Rivers Foods',
    'status': 'received',
    'total_kobo': 1500000,
    'note': 'By Friday',
    'lines': [
      {'item': 'Rice 50kg', 'quantity': 2, 'price_kobo': 500000},
      {'item': 'Beans', 'quantity': 1, 'price_kobo': 0},
    ],
  };

  Map<String, dynamic> delivery(String status, {bool proof = false}) => {
    'id': 'd1',
    'order_reference': 'DL-1',
    'buyer_name': 'Ada Stores',
    'status': status,
    'total_kobo': 200000,
    'delivery': {
      'delivery_id': 'x',
      'pickup': '12 Allen Avenue',
      'drop_area': 'Yaba',
      'proof_required': proof,
      'door_payment': true,
      'customer': {'name': 'Ngozi', 'phone': '08031234567', 'address': '4 Herbert Macaulay', 'landmark': 'By the church'},
    },
  };

  Future<({List<String> sent, List<String?> lines})> open(WidgetTester t, Map<String, dynamic> o, {Pickers pickers = const Pickers(), http.Response? answer}) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    final lines = <String?>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET') return null;
        final type = req.headers['content-type'] ?? req.headers['Content-Type'] ?? '';
        sent.add('${req.method} ${req.url.path} ${type.startsWith('multipart/') ? 'photo' : req.body}');
        return answer ?? http.Response(jsonEncode({}), 200, headers: {'content-type': 'application/json'});
      }),
      now: () => f.at,
    );
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: TkSheet(title: 'Orders', body: OrderView(auth: chats.auth, order: o, onSaved: lines.add, pickers: pickers)),
      ),
    );
    await t.pumpAndSettle();
    return (sent: sent, lines: lines);
  }

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
  }

  testWidgets('an order: its lines and note; accepted', (t) async {
    final r = await open(t, order);
    expect(find.text('2 × Rice 50kg'), findsOneWidget);
    expect(find.text('₦10,000'), findsOneWidget);
    expect(find.text('No price'), findsOneWidget);
    expect(find.text('₦15,000'), findsOneWidget);
    expect(find.text('Their note: “By Friday”'), findsOneWidget);
    expect(find.bySemanticsLabel('Placed: step 1 of 4'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Accept'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/trade-orders/o1/accept {}');
    expect(r.lines.single, 'Accepted order AD-7Q4K2P from Rivers Foods');
  });

  testWidgets('an order declined after yes; kept on Keep it', (t) async {
    final r = await open(t, order);
    await t.tap(find.bySemanticsLabel('Decline'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Keep it'));
    await t.pumpAndSettle();
    expect(r.sent, isEmpty);
    await t.tap(find.bySemanticsLabel('Decline'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Decline').last);
    await settle(t);
    expect(r.sent.single, 'POST /v1/trade-orders/o1/decline {}');
    expect(r.lines.single, 'Declined order AD-7Q4K2P from Rivers Foods');
  });

  testWidgets('an accepted order has no buttons', (t) async {
    await open(t, {...order, 'status': 'accepted'});
    expect(find.bySemanticsLabel('Accept'), findsNothing);
    expect(find.bySemanticsLabel('Accepted: step 2 of 4'), findsOneWidget);
  });

  testWidgets('a delivery offered: where and who; passed on after yes; the customer rung', (t) async {
    final called = <String>[];
    final was = callPhone;
    callPhone = (p) async {
      called.add(p);
      return true;
    };
    addTearDown(() => callPhone = was);
    final r = await open(t, delivery('received'));
    expect(find.text('12 Allen Avenue'), findsOneWidget);
    expect(find.text('4 Herbert Macaulay (Yaba)'), findsOneWidget);
    expect(find.text('By the church'), findsOneWidget);
    expect(find.text('At the door'), findsOneWidget);
    expect(find.text('₦2,000'), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp('^Call ')));
    expect(called.single, '08031234567');
    await t.tap(find.bySemanticsLabel('Can’t'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Can’t take it'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/trade-orders/d1/cant {}');
    expect(r.lines.single, 'Passed on DL-1 for Ada Stores');
  });

  testWidgets('a delivery at the door with proof: Delivered once the photo is taken, sent with it', (t) async {
    final r = await open(t, delivery('arrived', proof: true), pickers: const _Camera());
    expect(find.bySemanticsLabel('Arrived: step 4 of 5'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Delivered'));
    await settle(t);
    expect(r.sent, isEmpty, reason: 'no photo yet');
    await t.tap(find.bySemanticsLabel('Take the photo'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Delivered'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/trade-orders/d1/delivered photo');
    expect(r.lines.single, 'Delivered DL-1 for Ada Stores');
  });

  testWidgets('a cashier under the lowest price: it waits for the owner, nothing said as accepted', (t) async {
    final r = await open(
      t,
      order,
      answer: http.Response(jsonEncode({'waiting': 'owner', 'why': 'Under the lowest price for Rice 50kg: it waits for the owner’s OK.'}), 202, headers: {'content-type': 'application/json'}),
    );
    await t.tap(find.bySemanticsLabel('Accept'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/trade-orders/o1/accept {}');
    expect(r.lines, isEmpty, reason: 'no "Accepted" line: nothing was accepted');
    expect(find.text('Under the lowest price for Rice 50kg: it waits for the owner’s OK.'), findsOneWidget);
    expect(find.bySemanticsLabel('Decline'), findsOneWidget);
  });

  testWidgets('a changed order waits for the buyer: Decline, no Accept', (t) async {
    await open(t, {...order, 'status': 'changed'});
    expect(find.bySemanticsLabel('Decline'), findsOneWidget);
    expect(find.bySemanticsLabel('Accept'), findsNothing);
  });

  testWidgets('an order at the door: its word and step', (t) async {
    await open(t, {...order, 'status': 'arrived'});
    expect(find.text('At the door'), findsOneWidget);
    expect(find.bySemanticsLabel('On the way: step 3 of 4'), findsOneWidget);
  });

  testWidgets('a call that cannot be made says so', (t) async {
    final was = callPhone;
    callPhone = (p) async => false;
    addTearDown(() => callPhone = was);
    await open(t, delivery('received'));
    await t.tap(find.bySemanticsLabel(RegExp('^Call ')));
    await t.pump();
    expect(find.text('That number could not be called.'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a refused step: the server’s words, the buttons back', (t) async {
    final r = await open(t, order, answer: http.Response(jsonEncode({'error': 'AD-7Q4K2P is cancelled'}), 409, headers: {'content-type': 'application/json'}));
    await t.tap(find.bySemanticsLabel('Accept'));
    await settle(t);
    expect(r.lines, isEmpty);
    expect(find.text('AD-7Q4K2P is cancelled'), findsOneWidget);
    final accept = t.getSemantics(find.bySemanticsLabel('Accept'));
    expect(accept.getSemanticsData().hasAction(SemanticsAction.tap), isTrue);
  });

  testWidgets('delivered with no proof asked: sent as JSON, not a form', (t) async {
    final r = await open(t, delivery('arrived'));
    await t.tap(find.bySemanticsLabel('Delivered'));
    await settle(t);
    expect(r.sent.single, 'POST /v1/trade-orders/d1/delivered {}');
  });

  testWidgets('at large text on a small phone: nothing cut off, all pressable', (t) async {
    final f = Fixture('chats');
    final chats = ChatsData(f.auth(), now: () => f.at);
    for (final o in [order, delivery('received'), delivery('arrived', proof: true)]) {
      await expectLargeText(
        t,
        TaskiemApp(
          mode: TkMode.light,
          home: TkSheet(title: 'Orders', body: OrderView(auth: chats.auth, order: o, onSaved: (_) {})),
        ),
      );
    }
  });
}
