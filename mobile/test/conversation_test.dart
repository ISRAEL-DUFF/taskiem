// A conversation with a linked business, as a person uses it: opened from
// the chats, a message sent (a clock, then the server's copy with its two
// ticks), and one that could not be sent (Retry, then sent).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/auth.dart' show Auth;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/bills_page.dart' show BillPage;
import 'package:taskiem/chat/pay_chip.dart' show mayHoldAccount, paidWords;
import 'package:taskiem/chat/pay_save.dart' show PaySaveSheet;
import 'package:taskiem/chat/pay_sheet.dart' show PaySheet;
import 'package:taskiem/kit/sheet.dart' show TkSheet;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'looks.dart';
import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

void main() {
  setUpAll(loadFonts);

  Widget app(ChatsData chats, {TkMode mode = TkMode.light}) => TaskiemApp(
        mode: mode,
        home: Builder(
          builder: (context) => ChatsScreen(
            data: chats,
            // The conversation's data made once, as the app's route does: a
            // route built again (the theme switched) keeps what it read.
            onOpen: (c) {
              final data = ConversationData(chats, c, follow: false);
              Navigator.of(context).push(PageRouteBuilder<void>(
                transitionDuration: Duration.zero,
                pageBuilder: (_, _, _) => ConversationScreen(data: data),
              ));
            },
          ),
        ),
      );

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 4; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    // A screen sliding in, a message arriving: done.
    await t.pump(const Duration(milliseconds: 400));
  }

  testWidgets('opened from the chats, a message is sent and shows its ticks', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final sent = <Map<String, dynamic>>[];
    final answer = Completer<void>();
    var read = 0;
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path == '/v1/link-chats/$_link/read') {
          read++;
          return http.Response(jsonEncode({'read': 2}), 200);
        }
        if (req.method == 'POST' && req.url.path == '/v1/link-chats/$_link/messages') {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          sent.add(body);
          await answer.future;
          return http.Response(
              jsonEncode({
                'message': {'id': 'm-new', 'link': _link, 'mine': true, 'kind': 'text', 'body': body['text'], 'created_at': f.at.toIso8601String()},
              }),
              201);
        }
        return null;
      }),
      now: () => f.at,
    );
    await t.pumpWidget(app(chats));
    await settle(t);
    await looks(t, 'chats');
    await t.tap(find.text("Mama Tunde's Market"));
    await settle(t);
    expect(find.text('2 unread messages'), findsOneWidget);
    expect(find.textContaining('No problem. My boy will be there before 9.', findRichText: true), findsOneWidget);
    expect(read, 1, reason: 'opened, its unread are read');
    expect(find.bySemanticsLabel('Record a voice note'), findsOneWidget);

    await t.enterText(find.byType(EditableText), 'Thank you!');
    await t.pump();
    expect(find.bySemanticsLabel('Send'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Send'));
    await t.pump();
    expect(find.bySemanticsLabel('Sending'), findsOneWidget, reason: 'shown at once, with a clock');
    expect(find.textContaining('Thank you!', findRichText: true), findsOneWidget);
    answer.complete();
    await settle(t);
    expect(sent.single['text'], 'Thank you!');
    expect(sent.single['client_id'], isNotEmpty);
    expect(find.bySemanticsLabel('Sending'), findsNothing);
    expect(find.textContaining('Thank you!', findRichText: true), findsOneWidget);
    expect(find.bySemanticsLabel('Delivered'), findsNWidgets(2));
    await looks(t, 'conversation');
  });

  testWidgets('a message that could not be sent is tried again with the same client id', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final ids = <String>[];
    var fail = true;
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path.endsWith('/messages')) {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          ids.add(body['client_id'] as String);
          if (fail) return http.Response(jsonEncode({'error': 'slow down'}), 429);
          return http.Response(
              jsonEncode({
                'message': {'id': 'm-new', 'link': _link, 'mine': true, 'kind': 'text', 'body': body['text'], 'created_at': f.at.toIso8601String()},
              }),
              201);
        }
        return null;
      }),
      now: () => f.at,
    );
    await t.pumpWidget(app(chats));
    await settle(t);
    await t.tap(find.text("Mama Tunde's Market"));
    await settle(t);
    await t.enterText(find.byType(EditableText), 'Are you coming?');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.bySemanticsLabel(RegExp('^Not sent. You are sending too fast')), findsOneWidget);
    fail = false;
    await t.tap(find.bySemanticsLabel('Retry'));
    await settle(t);
    expect(find.bySemanticsLabel(RegExp('^Not sent')), findsNothing);
    expect(ids.length, 2);
    expect(ids[0], ids[1], reason: 'a retry is the same message to the server');
  });

  testWidgets('an answer that cannot be read marks the message not sent, with Retry', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    var fail = true;
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'POST' && req.url.path.endsWith('/messages')) {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          // A 201 whose body is not the message: reading it throws
          // something that is neither an ApiError nor Offline.
          if (fail) return http.Response(jsonEncode({'message': 'not a message'}), 201);
          return http.Response(
              jsonEncode({
                'message': {'id': 'm-new', 'link': _link, 'mine': true, 'kind': 'text', 'body': body['text'], 'created_at': f.at.toIso8601String()},
              }),
              201);
        }
        return null;
      }),
      now: () => f.at,
    );
    await t.pumpWidget(app(chats));
    await settle(t);
    await t.tap(find.text("Mama Tunde's Market"));
    await settle(t);
    await t.enterText(find.byType(EditableText), 'Are you coming?');
    await t.pump();
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.bySemanticsLabel(RegExp('^Not sent. It could not be sent')), findsOneWidget);
    fail = false;
    await t.tap(find.bySemanticsLabel('Retry'));
    await settle(t);
    expect(find.bySemanticsLabel(RegExp('^Not sent')), findsNothing);
  });

  testWidgets('the keyboard up: the message box sits just above it, as on WhatsApp', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    // A phone's gesture bar, and then its keyboard (which covers the bar).
    t.view.padding = const FakeViewPadding(bottom: 24);
    addTearDown(t.view.reset);
    final f = Fixture('chat-bayo');
    final chats = ChatsData(f.auth(), now: () => f.at)..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    await t.pumpAndSettle();
    final box = find.bySemanticsLabel(RegExp(r'^Message$'));
    expect(t.getRect(box.first).bottom, lessThan(844 - 24), reason: 'above the gesture bar');
    t.view.viewInsets = const FakeViewPadding(bottom: 320);
    await t.pumpAndSettle();
    final r = t.getRect(box.first);
    expect(r.bottom, lessThanOrEqualTo(844 - 320), reason: 'not under the keyboard');
    expect(r.bottom, greaterThan(844 - 320 - 40), reason: 'just above it, no gap for the gesture bar');
  });

  testWidgets('cards: opened at the unread, the round button goes to the newest, Pay opens the invoice', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-bayo');
    final chats = ChatsData(f.auth(), now: () => f.at)..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    final opened = <LinkDocCard>[];
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: ConversationScreen(data: ConversationData(chats, conv, follow: false), onOpenDoc: opened.add),
    ));
    await settle(t);
    await t.pumpAndSettle();
    expect(find.text('5 unread messages'), findsOneWidget);
    final jump = find.bySemanticsLabel('Scroll to the newest messages');
    expect(jump, findsOneWidget, reason: 'opened at the unread, not at the bottom');
    expect(find.text('Today'), findsNWidgets(2), reason: 'the day stays at the top, over its own place');
    await t.tap(jump);
    await t.pumpAndSettle();
    expect(jump, findsNothing);
    final pay = find.bySemanticsLabel('Pay INV-0001');
    expect(pay, findsOneWidget);
    expect(find.text('Pay ₦450,000'), findsOneWidget);
    await t.tap(pay);
    expect(opened.single.reference, 'INV-0001');
    await t.tap(find.bySemanticsLabel('View invoice INV-0001'));
    expect(opened.length, 2);
  });

  testWidgets('M8: a bill card’s Pay opens the bill, in its tool or on its screen, not "coming"', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-bayo');
    final chats = ChatsData(f.auth(), now: () => f.at)..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.name == 'Bayo Rice Mills');
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await settle(t);
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Scroll to the newest messages'));
    await t.pumpAndSettle();
    await t.tap(find.bySemanticsLabel('Pay INV-0001'));
    await t.pumpAndSettle();
    expect(find.text('This is coming to the app soon.'), findsNothing);
    expect(find.byType(TkSheet).evaluate().isNotEmpty || find.byType(BillPage).evaluate().isNotEmpty, isTrue);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a saved account named with @: asked first, sent with the yes, and shown as a card', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final sent = <Map<String, dynamic>>[];
    final card = {
      'kind': 'bank_account', 'name': 'our-account', 'title': 'Our account', 'sensitive': true,
      'details': {'bank_name': 'Zenith Bank', 'account_number': '1234567890'},
      'lines': [{'label': 'Bank', 'value': 'Zenith Bank'}, {'label': 'Account number', 'value': '1234567890'}],
    };
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/me/saved') return http.Response(jsonEncode({'saved': <Object>[], 'limit': 500}), 200);
        if (req.method == 'GET' && req.url.path == '/v1/saved') {
          return http.Response(
              jsonEncode({
                'saved': [
                  {'id': 's1', 'owner': 'business', 'name': 'our-account', 'title': 'Our account', 'kind': 'bank_account', 'hint': 'Zenith Bank 7890', 'details': {'bank_name': 'Zenith Bank'},
                    'check': {'state': 'unchecked', 'account_name': null, 'via': null, 'at': null, 'problem': null}, 'to_change': true, 'created_at': '2026-11-20T10:00:00Z', 'updated_at': '2026-11-20T10:00:00Z'},
                ],
                'limit': 500,
              }),
              200);
        }
        if (req.method == 'POST' && req.url.path == '/v1/link-chats/$_link/messages') {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          sent.add(body);
          return http.Response(
              jsonEncode({
                'message': {'id': 'm-saved', 'link': _link, 'mine': true, 'kind': 'text', 'body': body['text'], 'created_at': f.at.toIso8601String(),
                  'mentions': [{'kind': 'saved', 'name': 'our-account', 'saved': card}]},
              }),
              201);
        }
        return null;
      }),
      now: () => f.at,
    );
    await t.pumpWidget(app(chats));
    await settle(t);
    await t.tap(find.text("Mama Tunde's Market"));
    await settle(t);
    await t.enterText(find.byType(EditableText), 'Pay to @our');
    await settle(t);
    await t.tap(find.text('our-account'));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Send'));
    await settle(t);
    expect(find.text("Share Our account's account details with Mama Tunde's Market?"), findsOneWidget);
    await t.tap(find.text('Share'));
    await settle(t);
    expect(sent.single['text'], 'Pay to @our-account');
    expect(sent.single['mentions'], [
      {'kind': 'saved', 'saved': {'id': 's1', 'owner': 'business', 'confirm': true}},
    ]);
    expect(find.bySemanticsLabel(RegExp(r'^Our account, shared\.')), findsOneWidget);
    await t.tap(find.bySemanticsLabel(RegExp(r'^Our account, shared\. .*Show details$')));
    await settle(t);
    await looks(t, 'saved-link-card');
    await t.pump(const Duration(seconds: 5));
  });
  testWidgets('pay from a message: account details get Pay this account, already paid says so, and open the Pay sheet filled in', (t) async {
    t.view.physicalSize = const Size(360, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    final reads = <String>[];
    final checks = <Map<String, dynamic>>[];
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final messages = f.json('/v1/link-chats/$_link/messages');
    final list = [
      ...(messages['messages'] as List),
      {
        'id': 'm-acct', 'link': _link, 'mine': false, 'kind': 'text', 'sender_name': 'Funke',
        'body': 'Send 45k for the tomatoes. 0123456789 GTBank Chidi Okafor', 'created_at': f.at.subtract(const Duration(minutes: 1)).toIso8601String(),
      },
    ];
    Map<String, dynamic> field(String key, String label, String kind) => {'key': key, 'label': label, 'kind': kind};
    final payFor = {'key': 'for', 'label': 'What is it for?', 'kind': 'choice', 'choices': ['a bill', 'wages', 'a delivery', 'a refund', 'stock bought', 'other']};
    final auth = f.auth(also: (req) async {
      final p = req.url.path;
      if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages') return ok({'messages': list});
      if (req.method == 'GET' && p == '/v1/chat/actions') {
        return ok({
          'topics': [
            {
              'id': 'pay', 'name': 'Pay', 'icon': 'money', 'color': 'lime',
              'actions': [
                {'id': 'pay.person', 'title': 'Pay someone', 'line': 'From your balance', 'icon': 'money', 'does': 'asks', 'opens': 'form', 'tool': 'pay', 'confirm': 'passkey',
                  'fields': [field('who', 'Who are you paying?', 'payee'), field('amount', 'How much?', 'money'), payFor]},
                {'id': 'pay.account', 'title': 'Pay an account number', 'line': 'Any bank: its name is checked first', 'icon': 'money', 'does': 'asks', 'opens': 'form', 'tool': 'pay', 'confirm': 'passkey',
                  'fields': [field('number', 'What is the account number?', 'account'), field('bank', 'Which bank is it with?', 'bank'), field('amount', 'How much?', 'money'), payFor]},
              ],
            },
          ],
        });
      }
      if (req.method == 'POST' && p == '/v1/payments/read') {
        reads.add((jsonDecode(req.body) as Map)['text'] as String);
        return ok({
          'found': true, 'account_number': '0123456789', 'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'amount_kobo': 4500000, 'name': 'Chidi Okafor',
          'paid': {'id': 'p0', 'amount_kobo': 4500000, 'status': 'paid', 'at': '2026-11-03T10:00:00Z'},
        });
      }
      if (req.method == 'GET' && p == '/v1/payments/banks') {
        return ok({'banks': [{'code': '058', 'name': 'Guaranty Trust Bank', 'likely': true}, {'code': '044', 'name': 'Access Bank'}], 'from': 'iswallet/taskiem'});
      }
      if (req.method == 'POST' && p == '/v1/payments/name-check') {
        checks.add(jsonDecode(req.body) as Map<String, dynamic>);
        return ok({'account': '••6789', 'banks': [{'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'account_name': 'CHIDI OKEKE'}]});
      }
      return null;
    });
    // An owner who may pay.
    auth.me = Me.fromJson({...auth.me!.toJson(), 'permissions': [...auth.me!.permissions, 'money.pay']});
    final chats = ChatsData(auth, now: () => f.at);
    await t.pumpWidget(app(chats));
    await settle(t);
    await t.tap(find.text("Mama Tunde's Market"));
    await settle(t);
    await settle(t);
    expect(reads, ['Send 45k for the tomatoes. 0123456789 GTBank Chidi Okafor'], reason: 'only the message with ten digits is read');
    final chip = find.bySemanticsLabel('Paid ₦45,000 on 3 Nov. Pay this account again ending 6789');
    expect(chip, findsOneWidget);
    expect(find.textContaining('Pay again', findRichText: true), findsOneWidget);
    await looks(t, 'pay-chip');
    await t.tap(chip);
    await settle(t);
    await settle(t);
    // Filled in: the number, the bank, the amount; the bank's name checked
    // at once, and the message's name in amber.
    expect(find.text('Pay an account number'), findsWidgets);
    expect(find.text('Guaranty Trust Bank'), findsOneWidget);
    expect(checks.single, {'account_number': '0123456789', 'bank_codes': ['058']});
    expect(find.text('The bank says CHIDI OKEKE'), findsOneWidget);
    expect(find.text('The message says Chidi Okafor; the bank says CHIDI OKEKE.'), findsOneWidget);
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    expect(t.widget<EditableText>(boxes.at(0)).controller.text, '0123456789');
    expect(t.widget<EditableText>(boxes.at(1)).controller.text, '45000');
    await t.tap(find.text('Cancel'));
    await settle(t);
    expect(paidWords(ReadPaymentResponsePaid(amountKobo: 4500000, status: 'sending', at: DateTime.utc(2026, 11, 3, 23, 30))), 'Sent ₦45,000 on 4 Nov');
    expect(mayHoldAccount('Order ref 0803 123 4567'), isFalse, reason: 'eleven digits: a phone');
    expect(mayHoldAccount('Acct: 0123 456 789, Access'), isTrue);
    await t.pump(const Duration(seconds: 5));
  });
  testWidgets('pay from a message, as staff: the saved account by its @name, or ask an owner to save it', (t) async {
    t.view.physicalSize = const Size(360, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-tunde');
    var payee = 'chidi';
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final messages = f.json('/v1/link-chats/$_link/messages');
    final list = [
      ...(messages['messages'] as List),
      {'id': 'm-acct', 'link': _link, 'mine': false, 'kind': 'text', 'body': 'Acct: 0123456789, Access', 'created_at': f.at.toIso8601String()},
    ];
    Auth staff() => f.auth(also: (req) async {
      final p = req.url.path;
      if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages') return ok({'messages': list});
      if (req.method == 'GET' && p == '/v1/chat/actions/pay.person/choices') return ok({'fields': {'who': ['@chidi']}});
      if (req.method == 'GET' && p == '/v1/chat/actions') {
        return ok({
          'topics': [
            {
              'id': 'pay', 'name': 'Pay', 'icon': 'money', 'color': 'lime',
              'actions': [
                {'id': 'pay.person', 'title': 'Pay someone', 'line': 'From your balance', 'icon': 'money', 'does': 'asks', 'opens': 'form', 'tool': 'pay', 'confirm': 'passkey',
                  'fields': [{'key': 'who', 'label': 'Who are you paying?', 'kind': 'payee'}, {'key': 'amount', 'label': 'How much?', 'kind': 'money'}]},
              ],
            },
          ],
        });
      }
      if (req.method == 'POST' && p == '/v1/payments/read') {
        return ok({'found': true, 'account_number': '0123456789', 'bank_name': 'Access Bank', 'bank_code': '044', 'payee': payee});
      }
      return null;
    })..me = Me.fromJson({...f.auth().me!.toJson(), 'roles': ['cashier'], 'permissions': ['money.pay', 'chat.trade']});
    Future<void> openChat() async {
      await t.pumpWidget(const SizedBox());
      await t.pumpWidget(app(ChatsData(staff(), now: () => f.at)));
      await settle(t);
      await t.tap(find.text("Mama Tunde's Market"));
      await settle(t);
      await settle(t);
    }

    await openChat();
    await t.tap(find.bySemanticsLabel('Pay @chidi ending 6789'));
    await settle(t);
    await settle(t);
    expect(find.text('Pay someone'), findsWidgets);
    final boxes = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
    expect(t.widget<EditableText>(boxes.at(0)).controller.text, '@chidi');
    await t.tap(find.text('Cancel'));
    await settle(t);
    await t.pump(const Duration(seconds: 1));

    // Not saved yet: an owner saves it first.
    payee = '';
    await openChat();
    await t.tap(find.bySemanticsLabel('Pay this account ending 6789. Ask an owner to save it first'));
    await t.pump();
    expect(find.text('Ask an owner to save this account: then you can pay it.'), findsOneWidget);
    expect(find.byType(PaySheet), findsNothing);
    expect(find.bySemanticsLabel(RegExp('^Save as')), findsNothing, reason: 'staff may not manage the saved things');
    await t.pump(const Duration(seconds: 5));
  });
  for (final mode in [TkMode.light, TkMode.dark]) {
    testWidgets('pay from a message: Save as… keeps the account by a name, and the chip then pays @name (${mode.name})', (t) async {
      t.view.physicalSize = const Size(360, 800);
      t.view.devicePixelRatio = 1;
      addTearDown(t.view.reset);
      final f = Fixture('chat-tunde');
      final adds = <Map<String, dynamic>>[];
      http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});
      final messages = f.json('/v1/link-chats/$_link/messages');
      final list = [
        ...(messages['messages'] as List),
        {
          'id': 'm-acct', 'link': _link, 'mine': false, 'kind': 'text', 'sender_name': 'Funke',
          'body': 'My rider: 0123456789 Chidi Okafor', 'created_at': f.at.subtract(const Duration(minutes: 1)).toIso8601String(),
        },
      ];
      final auth = f.auth(also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages') return ok({'messages': list});
        if (req.method == 'GET' && p == '/v1/chat/actions') {
          return ok({
            'topics': [
              {
                'id': 'pay', 'name': 'Pay', 'icon': 'money', 'color': 'lime',
                'actions': [
                  {'id': 'pay.person', 'title': 'Pay someone', 'line': 'From your balance', 'icon': 'money', 'does': 'asks', 'opens': 'form', 'tool': 'pay', 'confirm': 'passkey',
                    'fields': [{'key': 'who', 'label': 'Who are you paying?', 'kind': 'payee'}, {'key': 'amount', 'label': 'How much?', 'kind': 'money'}]},
                  {'id': 'pay.account', 'title': 'Pay an account number', 'line': 'Any bank', 'icon': 'money', 'does': 'asks', 'opens': 'form', 'tool': 'pay', 'confirm': 'passkey',
                    'fields': [{'key': 'number', 'label': 'What is the account number?', 'kind': 'account'}, {'key': 'bank', 'label': 'Which bank is it with?', 'kind': 'bank'}]},
                ],
              },
            ],
          });
        }
        if (req.method == 'GET' && p == '/v1/chat/actions/pay.person/choices') return ok({'fields': {'who': ['@chidi-rider']}});
        if (req.method == 'POST' && p == '/v1/payments/read') {
          return ok({'found': true, 'account_number': '0123456789', 'name': 'Chidi Okafor', 'likely': [{'code': '058', 'name': 'Guaranty Trust Bank', 'likely': true}]});
        }
        if (req.method == 'GET' && p == '/v1/payments/banks') {
          return ok({
            'banks': [{'code': '044', 'name': 'Access Bank'}, {'code': '058', 'name': 'Guaranty Trust Bank', 'likely': true}, {'code': '057', 'name': 'Zenith Bank'}],
            'from': 'iswallet/taskiem',
          });
        }
        if (req.method == 'POST' && p == '/v1/saved') {
          final body = jsonDecode(req.body) as Map<String, dynamic>;
          adds.add(body);
          if (body['name'] == 'chidi-okafor') return ok({'error': 'That name is taken.', 'name_taken': true}, 409);
          return ok({
            'saved': {
              'id': 's9', 'kind': 'bank_account', 'name': body['name'], 'title': body['title'], 'owner': 'business', 'hint': 'Guaranty Trust Bank ••6789',
              'details': {'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'code_from': 'iswallet/taskiem', 'account_number': '••6789'},
              'check': {'state': 'checked', 'account_name': 'CHIDI OKAFOR'}, 'to_change': false,
              'created_at': f.at.toIso8601String(), 'updated_at': f.at.toIso8601String(),
            },
          }, 201);
        }
        return null;
      });
      // An owner who may pay and manage the business's saved things.
      auth.me = Me.fromJson({...auth.me!.toJson(), 'permissions': [...auth.me!.permissions, 'money.pay', 'saved.manage']});
      await t.pumpWidget(app(ChatsData(auth, now: () => f.at), mode: mode));
      await settle(t);
      await t.tap(find.text("Mama Tunde's Market"));
      await settle(t);
      await settle(t);
      expect(find.bySemanticsLabel('Pay this account ending 6789'), findsOneWidget);
      final saveAs = find.bySemanticsLabel('Save as… this account, to pay it by a name');
      expect(saveAs, findsOneWidget);
      await snap(t, 'pay-save-chip-${mode.name}');
      await t.tap(saveAs);
      await settle(t);
      await settle(t);
      // Filled in from the message: the name made from its name, the title,
      // the number masked; no bank in the message, so one is picked.
      expect(find.text('Save this account'), findsOneWidget);
      final sheet = find.byType(PaySaveSheet);
      final boxes = find.descendant(of: sheet, matching: find.byType(EditableText));
      expect(t.widget<EditableText>(boxes.at(0)).controller.text, '@chidi-okafor');
      expect(t.widget<EditableText>(boxes.at(1)).controller.text, 'Chidi Okafor');
      expect(t.widget<EditableText>(boxes.at(2)).controller.text, '••••••6789');
      expect(find.text('Choose the bank'), findsOneWidget);
      await snap(t, 'pay-save-sheet-${mode.name}');
      await t.tap(find.text('Save account'));
      await settle(t);
      expect(find.text('Choose the bank.'), findsOneWidget);
      expect(adds, isEmpty);
      await t.tap(find.text('Choose the bank'));
      await settle(t);
      // The Pay sheet's picker: the likely bank first.
      expect(find.text('The banks this number can be at come first.'), findsOneWidget);
      expect(t.getTopLeft(find.text('Guaranty Trust Bank')).dy, lessThan(t.getTopLeft(find.text('Access Bank')).dy));
      await t.tap(find.text('Guaranty Trust Bank'));
      await settle(t);
      await t.tap(find.text('Save account'));
      await settle(t);
      // The name taken: said in plain words, and another typed.
      expect(find.text('That name is taken: choose another.'), findsOneWidget);
      await snap(t, 'pay-save-taken-${mode.name}');
      await t.enterText(boxes.at(0), '@chidi rider');
      await t.enterText(boxes.at(1), 'Chidi, rider');
      await t.tap(find.text('Save account'));
      await settle(t);
      expect(adds.last, {
        'kind': 'bank_account', 'name': 'chidi-rider', 'title': 'Chidi, rider',
        'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'code_from': 'iswallet/taskiem', 'account_number': '0123456789',
      });
      expect(find.byType(PaySaveSheet), findsNothing);
      expect(find.text('Saved as @chidi-rider. Bank’s name: CHIDI OKAFOR'), findsOneWidget);
      // The chip says the name, Save as… is gone, and Pay goes by @name.
      await t.pump(const Duration(seconds: 5));
      expect(find.bySemanticsLabel(RegExp('^Save as')), findsNothing);
      final chip = find.bySemanticsLabel('Pay @chidi-rider ending 6789');
      expect(chip, findsOneWidget);
      await snap(t, 'pay-save-done-${mode.name}');
      await t.tap(chip);
      await settle(t);
      await settle(t);
      expect(find.text('Pay someone'), findsWidgets);
      final pay = find.descendant(of: find.byType(PaySheet), matching: find.byType(EditableText));
      expect(t.widget<EditableText>(pay.at(0)).controller.text, '@chidi-rider');
      await t.tap(find.text('Cancel'));
      await settle(t);
      await t.pump(const Duration(seconds: 5));
    });
  }
}
