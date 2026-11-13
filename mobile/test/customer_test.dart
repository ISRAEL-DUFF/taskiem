// Customers' conversations, as staff use them: the list's Customers with
// how many wait; a conversation opened from it (read on opening); a reply
// sent (at once with a clock, then the server's copy with its client id)
// or given back to the box if it could not go; a customer who sent STOP;
// a reply after the 24 hours and its length; the words, as customerchat.ts.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/customer.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

const _chidi = '01a1caf1-bffd-7bf6-b932-a1e10d4b7bab';

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
  }

  void phone(WidgetTester t) {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  testWidgets('the list: Customers, how many wait; a row opens the conversation, read on opening', (t) async {
    phone(t);
    final f = Fixture('customers');
    final reads = <String>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path.endsWith('/read')) reads.add(req.url.path);
        return null;
      }),
      now: () => f.at,
    );
    final chidi = Fixture('customer-chidi');
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: Builder(
        builder: (context) => ChatsScreen(
          data: chats,
          onOpenCustomer: (c) => Navigator.of(context).push(PageRouteBuilder<void>(
            pageBuilder: (_, _, _) => CustomerScreen(data: CustomerData(ChatsData(chidi.auth(also: (req) async {
              if (req.method == 'POST' && req.url.path.endsWith('/read')) reads.add(req.url.path);
              return null;
            }), now: () => chidi.at), c.id, follow: false)),
          )),
        ),
      ),
    ));
    await settle(t);
    expect(find.text('Customers · 2 waiting'), findsOneWidget);
    expect(find.text('No linked businesses yet. Search for a supplier or a customer that is on Taskiem, and ask to link.'), findsOneWidget);
    expect(find.text('Let other businesses on Taskiem find you?'), findsOneWidget);
    await t.tap(find.text('Chidi'));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('Perfect. Send it to 12 Herbert Macaulay Way please', findRichText: true), findsOneWidget);
    expect(find.text('+234 803 111 2222 · Customer on WhatsApp'), findsOneWidget);
    expect(reads, ['/v1/customer-chats/$_chidi/read']);
  });

  Future<(List<Map<String, dynamic>>, Completer<void>)> open(WidgetTester t, {int fail = 0, Map<String, dynamic>? chat}) async {
    phone(t);
    final f = Fixture('customer-chidi');
    final sent = <Map<String, dynamic>>[];
    final gate = Completer<void>();
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/customer-chats/$_chidi' && chat != null) {
          return http.Response(jsonEncode({...f.json('/v1/customer-chats/$_chidi'), ...chat}), 200);
        }
        if (req.method == 'POST' && req.url.path == '/v1/customer-chats/$_chidi/messages') {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          sent.add(body);
          await gate.future;
          if (fail > 0) return http.Response(jsonEncode({'error': 'WhatsApp did not take it.'}), fail);
          return http.Response(
            jsonEncode({
              'id': 'r1',
              'mine': true,
              'kind': 'text',
              'body': body['text'],
              'sender_name': 'Ada',
              'sent_as': 'text',
              'delivery': 'sent',
              'read_at': null,
              'created_at': '2026-11-11T14:11:00Z',
            }),
            201,
          );
        }
        return null;
      }),
      now: () => f.at,
    )..load();
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: CustomerScreen(data: CustomerData(chats, _chidi, follow: false))));
    await settle(t);
    return (sent, gate);
  }

  testWidgets('a reply: at once with a clock, then the server\'s copy, sent once', (t) async {
    final (sent, gate) = await open(t);
    await t.enterText(find.byType(EditableText), 'On its way, 30 minutes');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await t.pump();
    expect(find.bySemanticsLabel('Sending'), findsOneWidget);
    gate.complete();
    await settle(t);
    expect(sent.single['text'], 'On its way, 30 minutes');
    expect(sent.single['client_id'], isNotEmpty);
    expect(find.bySemanticsLabel('Sent'), findsOneWidget, reason: 'one tick: WhatsApp has it');
  });

  testWidgets('a reply that could not go comes back to the box', (t) async {
    final (_, gate) = await open(t, fail: 502);
    gate.complete();
    await t.enterText(find.byType(EditableText), 'Hello again');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.text('WhatsApp did not take it.'), findsOneWidget);
    expect(t.widget<EditableText>(find.byType(EditableText)).controller.text, 'Hello again');
  });

  testWidgets('a customer who sent STOP: said, and no box to reply in', (t) async {
    await open(t, chat: {'stopped': true});
    expect(find.textContaining('they sent STOP'), findsOneWidget);
    expect(find.byType(EditableText), findsNothing);
    expect(find.bySemanticsLabel('Start an order'), findsNothing);
  });

  testWidgets('after the 24 hours: said, and a reply over 900 characters waits', (t) async {
    await open(t, chat: {'window_open': false});
    expect(find.textContaining('More than a day since they last wrote'), findsOneWidget);
    await t.enterText(find.byType(EditableText), 'a ' * 451);
    await t.pump();
    expect(find.textContaining('Too long to send now'), findsOneWidget);
    expect(find.textContaining('This one is 901.'), findsOneWidget);
  });

  test('the words, as customerchat.ts says them', () {
    CustomerChat c(String name, String phone) => CustomerChat.fromJson({
      'id': 'x', 'name': name, 'phone': phone, 'last': '', 'last_mine': false, 'last_at': '2026-11-11T14:10:00Z', 'unread': 0,
      'window_open': true, 'window_until': null, 'stopped': false, 'archived': false,
    });
    expect(phoneWords('+2348031112222'), '+234 803 111 2222');
    expect(phoneWords('+447700900123'), '+447700900123');
    expect(customerTitle(c('+2348031112222', '+2348031112222')), '+234 803 111 2222');
    expect(customerTitle(c('Chidi', '+2348031112222')), 'Chidi');
    expect(customerLetter(c('+2348031112222', '+2348031112222')), 'Customer');
    expect(customerLetter(c('Chidi', '+2348031112222')), 'Chidi');
    expect(owesWords(1250000, 2), 'Owes ₦12,500 on 2 invoices');
    expect(owesWords(500000, 1), 'Owes ₦5,000 on 1 invoice');
    expect(owesWords(0, 3), '');
    expect(lateLength('  a\n\n b  '), 3);
    expect(lateTooLong('x' * 901, false), isTrue);
    expect(lateTooLong('x' * 901, true), isFalse);
  });

  testWidgets('the head\'s menu: Archive, said, and back to the list', (t) async {
    phone(t);
    final f = Fixture('customer-chidi');
    final puts = <Object?>[];
    var popped = false;
    final data = CustomerData(
      ChatsData(
        f.auth(also: (req) async {
          if (req.method == 'PUT') {
            puts.add(jsonDecode(req.body));
            return http.Response(jsonEncode({'archived': true}), 200, headers: {'content-type': 'application/json'});
          }
          return null;
        }),
        now: () => f.at,
      ),
      _chidi,
      follow: false,
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: CustomerScreen(data: data, onBack: () => popped = true)));
    await settle(t);
    await t.tap(find.bySemanticsLabel('More about this conversation'));
    await t.pumpAndSettle();
    expect(find.text('Archive'), findsOneWidget);
    await t.tap(find.text('Archive'));
    await settle(t);
    expect(puts, [
      {'archived': true},
    ]);
    expect(find.text('Archived'), findsOneWidget);
    expect(popped, isTrue);
    await t.pump(const Duration(seconds: 5));
  });
}
