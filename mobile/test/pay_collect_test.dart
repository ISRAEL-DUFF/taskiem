// Collecting people to pay from several messages (docs/spec/chat-pay-
// people.md "Pay several people → Filling the list → From messages"):
// "Add to the list" under a message in a link chat and in a customer chat
// puts each on one list kept on the server; "N to pay" at the foot of
// every chat reflects it; the same message twice says so; a row without
// an amount holds Check back until one is typed (kept with PATCH); Pay
// these makes the list a batch (POST /v1/pay-collection/batch) checked and
// paid as any other; a batch waiting is opened from the foot; Remove and
// Clear; "From your list (2)" in Pay several people. Pictured light and
// dark.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/auth.dart' show Auth;
import 'package:taskiem/app/passkey.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/customer.dart';
import 'package:taskiem/chat/pay_collect.dart';
import 'package:taskiem/chat/pay_many.dart';
import 'package:taskiem/kit/kit.dart' show TkField;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'passkey_test.dart' show FakePasskeys;
import 'pay_many_test.dart' show batch;
import 'pay_test.dart' show payment;
import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';
const _chidi = '01a1caf1-bffd-7bf6-b932-a1e10d4b7bab';
const _json = {'content-type': 'application/json; charset=utf-8'};

http.Response _ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: _json);

/// The server's one list per person, shared by every chat's stand-in.
class _List {
  final rows = <Map<String, dynamic>>[];
  String? inBatch;
  final calls = <(String, String, Map<String, dynamic>)>[];
  var _n = 0;

  /// The payments of the batch made from the list.
  List<Map<String, dynamic>> paying = const [];

  List<Map<String, dynamic>> sent(String method, String path) => [
    for (final c in calls)
      if (c.$1 == method && c.$2 == path) c.$3,
  ];

  Map<String, dynamic> list() => {
    'rows': rows,
    'count': rows.length,
    'total_kobo': rows.fold<int>(0, (n, r) => n + ((r['amount_kobo'] as int?) ?? 0)),
    'no_amount': rows.where((r) => r['amount_kobo'] == null).length,
    'in_batch': inBatch,
  };

  Future<http.Response?> answer(http.Request req) async {
    final p = req.url.path;
    if (!p.startsWith('/v1/pay-collection') && !p.startsWith('/v1/payment-batches/B9') && p != '/v1/me/step-up/options') return null;
    final body = req.body.isEmpty ? <String, dynamic>{} : (jsonDecode(req.body) as Map).cast<String, dynamic>();
    calls.add((req.method, p, body));
    final key = '${req.method} $p';
    if (key == 'GET /v1/pay-collection') return _ok(list());
    if (key == 'DELETE /v1/pay-collection') {
      rows.clear();
      return _ok(list());
    }
    if (key == 'POST /v1/pay-collection') {
      final src = (body['source'] as Map).cast<String, dynamic>();
      if (rows.any((r) => (r['source'] as Map)['message'] == src['message'])) {
        return _ok({'error': 'That message is on your list already.', 'code': 'already'}, 409);
      }
      final number = body['account_number'] as String? ?? '';
      rows.add({
        'id': 'c${++_n}',
        'saved_id': number.isEmpty ? 's$_n' : null,
        'payee': body['payee'] ?? '',
        'title': number.isEmpty ? 'Chidi’s GTBank' : 'Account ••${number.substring(6)}',
        'name': body['name'] ?? '',
        'bank_name': body['bank_name'] ?? 'Guaranty Trust Bank',
        'last4': number.isEmpty ? '6789' : number.substring(6),
        'amount_kobo': body['amount_kobo'],
        'purpose': body['purpose'] ?? '',
        'note': '',
        'source': src,
        'added_at': '2026-11-22T09:00:00Z',
      });
      return _ok(list(), 201);
    }
    if (key == 'POST /v1/pay-collection/batch') {
      if (rows.any((r) => r['amount_kobo'] == null)) return _ok({'error': 'Say how much for each person first.', 'code': 'amount'}, 400);
      inBatch = 'B9';
      paying = [
        for (final (i, r) in rows.indexed)
          payment(
            'p$i',
            payee: r['title'] as String,
            name: r['name'] as String,
            kobo: r['amount_kobo'] as int,
            purpose: r['purpose'] as String,
            account: '••${r['last4']}',
          ),
      ];
      return _ok({'batch': batch('B9', paying, kind: 'messages', title: 'From messages')}, 201);
    }
    if (key == 'GET /v1/payment-batches/B9') return _ok({'batch': batch('B9', paying, kind: 'messages', title: 'From messages')});
    if (key == 'POST /v1/payment-batches/B9/cancel') {
      inBatch = null;
      return _ok({
        'batch': batch(
          'B9',
          [
            for (final x in paying) {...x, 'status': 'cancelled'},
          ],
          status: 'cancelled',
          kind: 'messages',
        ),
      });
    }
    if (key == 'POST /v1/me/step-up/options') {
      return _ok({
        'publicKey': {'challenge': 'ch', 'rpId': 'taskiem.test'},
      });
    }
    if (key == 'POST /v1/payment-batches/B9/confirm') {
      if (!body.containsKey('passkey')) {
        return _ok({
          'error': 'confirm with your passkey',
          'step_up': 'required',
          'methods': ['passkey'],
        }, 403);
      }
      // Sent: they leave the list.
      rows.clear();
      inBatch = null;
      paying = [
        for (final x in paying) {...x, 'status': 'paid'},
      ];
      return _ok({'batch': batch('B9', paying, status: 'sent', kind: 'messages', title: 'From messages')}, 202);
    }
    if (p.startsWith('/v1/pay-collection/')) {
      final id = p.split('/').last;
      final r = rows.where((r) => r['id'] == id).firstOrNull;
      if (r == null) return _ok({'error': 'Not found.'}, 404);
      if (req.method == 'DELETE') rows.remove(r);
      if (req.method == 'PATCH') {
        if (body['amount_kobo'] != null) r['amount_kobo'] = body['amount_kobo'];
        if (body['purpose'] != null) r['purpose'] = body['purpose'];
      }
      return _ok(list());
    }
    return null;
  }
}

Map<String, dynamic> _field(String key, String label, String kind) => {'key': key, 'label': label, 'kind': kind};

/// The catalogue's two Pay forms, with a passkey.
final _actions = {
  'topics': [
    {
      'id': 'pay',
      'name': 'Pay',
      'icon': 'money',
      'color': 'lime',
      'actions': [
        {
          'id': 'pay.person',
          'title': 'Pay someone',
          'line': 'From your balance',
          'icon': 'money',
          'does': 'asks',
          'opens': 'form',
          'tool': 'pay',
          'confirm': 'passkey',
          'fields': [_field('who', 'Who are you paying?', 'payee'), _field('amount', 'How much?', 'money')],
        },
        {
          'id': 'pay.account',
          'title': 'Pay an account number',
          'line': 'Any bank: its name is checked first',
          'icon': 'money',
          'does': 'asks',
          'opens': 'form',
          'tool': 'pay',
          'confirm': 'passkey',
          'fields': [
            _field('number', 'What is the account number?', 'account'),
            _field('bank', 'Which bank is it with?', 'bank'),
            _field('amount', 'How much?', 'money'),
          ],
        },
      ],
    },
  ],
};

/// Who may pay: [me] given money.pay.
Me _payer(Me me, {bool owner = true}) => Me.fromJson({
  ...me.toJson(),
  'personal': false,
  'roles': [if (owner) 'owner' else 'cashier'],
  'permissions': [...me.permissions, 'money.pay'],
});

void main() {
  setUpAll(loadFonts);
  setUp(() {
    TkField.debugHideCaret = true;
    PayList.senders.clear();
  });

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    await t.pump(const Duration(milliseconds: 400));
  }

  void phone(WidgetTester t, [double tall = 800]) {
    t.view.physicalSize = Size(360, tall);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
  }

  FakePasskeys fakeKeys() {
    final was = passkeys;
    final fake = FakePasskeys();
    passkeys = fake;
    addTearDown(() => passkeys = was);
    return fake;
  }

  /// The link chat with Mama Tunde's Market: Funke's message with an
  /// account, an amount and a name.
  Auth linkAuth(_List server, {bool owner = true}) {
    final f = Fixture('chat-tunde');
    final list = [
      ...(f.json('/v1/link-chats/$_link/messages')['messages'] as List),
      {
        'id': 'm-acct',
        'link': _link,
        'mine': false,
        'kind': 'text',
        'sender_name': 'Funke',
        'body': 'Send 45k for the tomatoes. 0123456789 GTBank Chidi Okafor',
        'created_at': f.at.subtract(const Duration(minutes: 1)).toIso8601String(),
      },
    ];
    final auth = f.auth(
      also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages') return _ok({'messages': list});
        if (req.method == 'GET' && p == '/v1/chat/actions') return _ok(_actions);
        if (req.method == 'POST' && p == '/v1/payments/read') {
          return _ok({
            'found': true,
            'account_number': '0123456789',
            'bank_name': 'Guaranty Trust Bank',
            'bank_code': '058',
            'amount_kobo': 4500000,
            'name': 'Chidi Okafor',
          });
        }
        return server.answer(req);
      },
    );
    auth.me = _payer(auth.me!, owner: owner);
    return auth;
  }

  Widget linkApp(Auth auth, {TkMode mode = TkMode.light}) {
    final chats = ChatsData(auth, now: () => DateTime.utc(2026, 11, 22, 9));
    return TaskiemApp(
      mode: mode,
      home: Builder(
        builder: (context) => ChatsScreen(
          data: chats,
          // The conversation's data made once, as the app's route does: a
          // route built again (the theme switched) keeps what it read.
          onOpen: (c) {
            final data = ConversationData(chats, c, follow: false);
            Navigator.of(context).push(
              PageRouteBuilder<void>(
                transitionDuration: Duration.zero,
                pageBuilder: (_, _, _) => ConversationScreen(data: data),
              ),
            );
          },
        ),
      ),
    );
  }

  Future<void> openLink(WidgetTester t, Auth auth, {TkMode mode = TkMode.light}) async {
    await t.pumpWidget(linkApp(auth, mode: mode));
    await settle(t);
    await t.tap(find.text("Mama Tunde's Market"));
    await settle(t);
    await settle(t);
  }

  /// Chidi's customer chat: his account, with no amount.
  Widget customerApp(_List server) {
    final f = Fixture('customer-chidi');
    final messages = [
      ...(f.json('/v1/customer-chats/$_chidi/messages')['messages'] as List),
      {
        'id': 'cm-acct',
        'mine': false,
        'kind': 'text',
        'body': 'My account: 0001112223 Opay, Chidi Okeke',
        'read_at': null,
        'created_at': '2026-11-11T14:10:05Z',
      },
    ];
    final auth = f.auth(
      also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/customer-chats/$_chidi/messages') return _ok({'messages': messages, 'more': false});
        if (req.method == 'GET' && p == '/v1/chat/actions') return _ok(_actions);
        if (req.method == 'POST' && p == '/v1/payments/read') {
          return _ok({'found': true, 'account_number': '0001112223', 'bank_name': 'Opay', 'bank_code': '999992', 'name': 'Chidi Okeke'});
        }
        return server.answer(req);
      },
    );
    auth.me = _payer(auth.me!);
    final chats = ChatsData(auth, now: () => f.at)..load();
    return TaskiemApp(
      mode: TkMode.light,
      home: CustomerScreen(data: CustomerData(chats, _chidi, follow: false)),
    );
  }

  testWidgets('added from two chats: one list on the server, "N to pay" at the foot of each; the same message twice says so', (t) async {
    phone(t);
    final server = _List();
    final auth = linkAuth(server);
    await openLink(t, auth);
    // Nothing on the list yet: no foot.
    expect(server.sent('GET', '/v1/pay-collection'), isNotEmpty, reason: 'read on opening the chat');
    expect(find.byType(PayListBar), findsOneWidget);
    expect(find.textContaining('to pay ·'), findsNothing);
    final add = find.bySemanticsLabel('Add this account to your list of people to pay');
    expect(add, findsOneWidget);
    expect(find.text('Add to the list'), findsOneWidget);
    await snap(t, 'collect-chip');
    await t.tap(add);
    await settle(t);
    expect(server.sent('POST', '/v1/pay-collection').single, {
      'account_number': '0123456789',
      'amount_kobo': 4500000,
      'bank_name': 'Guaranty Trust Bank',
      'name': 'Chidi Okafor',
      'source': {'chat': _link, 'kind': 'link', 'message': 'm-acct', 'from': 'Funke'},
    });
    expect(find.text('Added: 1 to pay'), findsOneWidget);
    expect(find.text('1 to pay · ₦45,000'), findsOneWidget);
    expect(find.bySemanticsLabel('Pay these 1 to pay'), findsOneWidget);
    await t.pump(const Duration(seconds: 4));
    await settle(t);

    // The same message again: said, nothing added.
    await t.tap(add);
    await settle(t);
    expect(find.text('Already on your list: 1 to pay'), findsOneWidget);
    expect(server.rows, hasLength(1));
    await t.pump(const Duration(seconds: 4));
    await settle(t);
    await looks(t, 'pay-list-bar');

    // Another chat: the list read on opening it, and Chidi's account added.
    await t.pumpWidget(const SizedBox());
    await t.pumpWidget(customerApp(server));
    await settle(t);
    await settle(t);
    expect(find.text('1 to pay · ₦45,000'), findsOneWidget, reason: 'the same list, in every chat');
    await t.tap(find.bySemanticsLabel('Add this account to your list of people to pay'));
    await settle(t);
    expect(server.sent('POST', '/v1/pay-collection').last, {
      'account_number': '0001112223',
      'bank_name': 'Opay',
      'name': 'Chidi Okeke',
      'source': {'chat': _chidi, 'kind': 'customer', 'message': 'cm-acct', 'from': 'Chidi'},
    });
    expect(find.text('Added: 2 to pay'), findsOneWidget);
    expect(find.text('2 to pay · ₦45,000'), findsOneWidget);
    expect(find.text('1 needs an amount'), findsOneWidget);
    // The toast put away, to see the foot under it.
    await t.tap(find.bySemanticsLabel('Dismiss'));
    await settle(t);
    await snap(t, 'collect-customer-bar');
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Pay these: each row with its message; one without an amount holds Check back; typed, kept, then the batch is checked and paid', (t) async {
    phone(t, 1100);
    final keys = fakeKeys();
    final server = _List();
    PayList.senders['m-acct'] = 'Funke';
    server.rows.addAll([
      {
        'id': 'c1',
        'saved_id': null,
        'payee': '',
        'title': 'Account ••6789',
        'name': 'Chidi Okafor',
        'bank_name': 'Guaranty Trust Bank',
        'last4': '6789',
        'amount_kobo': 4500000,
        'purpose': '',
        'note': '',
        'source': {'kind': 'link', 'chat': _link, 'message': 'm-acct'},
        'added_at': '2026-11-22T09:00:00Z',
      },
      {
        'id': 'c2',
        'saved_id': null,
        'payee': '',
        'title': 'Account ••2223',
        'name': 'Chidi Okeke',
        'bank_name': 'Opay',
        'last4': '2223',
        'amount_kobo': null,
        'purpose': '',
        'note': '',
        'source': {'kind': 'customer', 'chat': _chidi, 'message': 'cm-acct'},
        'added_at': '2026-11-22T09:01:00Z',
      },
    ]);
    final auth = linkAuth(server);
    await openLink(t, auth);
    expect(find.text('2 to pay · ₦45,000'), findsOneWidget);
    expect(find.text('1 needs an amount'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Pay these 2 to pay'));
    await settle(t);
    expect(find.byType(PayManySheet), findsOneWidget);
    expect(find.text('Chidi Okafor'), findsOneWidget);
    expect(find.text('from Funke’s message · Guaranty Trust Bank ••6789'), findsOneWidget);
    expect(find.text('from a customer’s message · Opay ••2223'), findsOneWidget);
    // Only the list: no other ways to add.
    expect(find.bySemanticsLabel('Paste a list'), findsNothing);
    // No amount for Chidi Okeke: Check waits.
    Finder check() => find.descendant(of: find.byType(PayManySheet), matching: find.bySemanticsLabel('Check'));
    expect(t.getSemantics(check()).flagsCollection.isEnabled, isNot(true));
    await t.tap(find.text('wages').first);
    await settle(t);
    // What for, kept on each row.
    expect(server.sent('PATCH', '/v1/pay-collection/c1').single, {'purpose': 'wages'});
    expect(server.sent('PATCH', '/v1/pay-collection/c2').single, {'purpose': 'wages'});
    await looks(t, 'pay-list');
    await t.tap(find.text('Check'));
    await settle(t);
    expect(server.sent('POST', '/v1/pay-collection/batch'), isEmpty, reason: 'a row has no amount');
    final boxes = find.descendant(of: find.byType(PayManySheet), matching: find.byType(EditableText));
    await t.enterText(boxes.last, '20k');
    await t.pump(const Duration(seconds: 1));
    await settle(t);
    expect(server.sent('PATCH', '/v1/pay-collection/c2').last, {'amount_kobo': 2000000});
    expect(find.text('2 people · ₦65,000'), findsOneWidget);

    await t.tap(find.text('Check'));
    await settle(t);
    expect(server.sent('POST', '/v1/pay-collection/batch').single, <String, dynamic>{});
    expect(server.sent('POST', '/v1/payment-batches'), isEmpty, reason: 'the list is made a batch on the server');
    expect(find.text('Check before you pay'), findsOneWidget);
    expect(find.text('Pay 2 people'), findsOneWidget);
    await t.tap(find.text('Pay 2 people'));
    await settle(t);
    expect(keys.asked, hasLength(1));
    expect(server.sent('POST', '/v1/payment-batches/B9/confirm').last['payments'], ['p0', 'p1']);
    await t.pump(const Duration(seconds: 2));
    await settle(t);
    await t.tap(find.text('Done'));
    await settle(t);
    // Sent: the list read again, empty, and the foot gone.
    expect(find.byType(PayManySheet), findsNothing);
    expect(find.textContaining('to pay ·'), findsNothing);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('a batch from the list waiting: "Waiting to be sent" at the foot, opening it; Back from Check lets it go', (t) async {
    phone(t);
    final server = _List()
      ..inBatch = 'B9'
      ..paying = [payment('p0', payee: 'Account ••6789', name: 'Chidi Okafor', purpose: 'wages')];
    final auth = linkAuth(server);
    await openLink(t, auth);
    expect(find.text('Waiting to be sent'), findsOneWidget);
    expect(find.text('Your list, as a batch'), findsOneWidget);
    await snap(t, 'collect-waiting');
    await t.tap(find.bySemanticsLabel('Open the batch waiting to be sent'));
    await settle(t);
    expect(server.sent('GET', '/v1/payment-batches/B9'), hasLength(1));
    expect(find.text('Check before you pay'), findsOneWidget);
    await t.tap(find.text('Cancel it'));
    await settle(t);
    expect(server.sent('POST', '/v1/payment-batches/B9/cancel'), hasLength(1));
    expect(find.text('Waiting to be sent'), findsNothing);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Remove takes a row off the list; Clear behind the foot’s menu, asked first', (t) async {
    phone(t);
    final server = _List();
    final auth = linkAuth(server);
    await openLink(t, auth);
    await t.tap(find.bySemanticsLabel('Add this account to your list of people to pay'));
    await settle(t);
    server.rows.add({
      ...server.rows.single,
      'id': 'c9',
      'name': 'Bola',
      'source': {'kind': 'group', 'chat': 'g1', 'message': 'g-m'},
    });
    await t.pump(const Duration(seconds: 4));
    await settle(t);
    await t.tap(find.bySemanticsLabel('Pay these 1 to pay'));
    await settle(t);
    expect(find.text('from a message in a group · Guaranty Trust Bank ••6789'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Remove Bola'));
    await settle(t);
    expect(server.sent('DELETE', '/v1/pay-collection/c9'), hasLength(1));
    expect(find.text('Bola'), findsNothing);
    await t.tap(find.text('Cancel'));
    await settle(t);
    expect(find.text('1 to pay · ₦45,000'), findsOneWidget);
    await expectPressable(t);

    await t.tap(find.bySemanticsLabel('More: clear the list'));
    await settle(t);
    await t.tap(find.text('Clear the list'));
    await settle(t);
    expect(find.text('Clear your list?'), findsOneWidget);
    await t.tap(find.text('Clear the list'));
    await settle(t);
    expect(server.sent('DELETE', '/v1/pay-collection'), hasLength(1));
    expect(find.text('Cleared: nothing was paid'), findsOneWidget);
    expect(find.textContaining('to pay ·'), findsNothing);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('staff: Add to the list adds the saved account by its @name', (t) async {
    phone(t);
    final server = _List();
    final f = Fixture('chat-tunde');
    final list = [
      ...(f.json('/v1/link-chats/$_link/messages')['messages'] as List),
      {'id': 'm-acct', 'link': _link, 'mine': false, 'kind': 'text', 'body': 'Acct: 0123456789, GTBank', 'created_at': f.at.toIso8601String()},
    ];
    final auth = f.auth(
      also: (req) async {
        final p = req.url.path;
        if (req.method == 'GET' && p == '/v1/link-chats/$_link/messages') return _ok({'messages': list});
        if (req.method == 'GET' && p == '/v1/chat/actions') {
          return _ok({
            'topics': [
              {
                ...(_actions['topics']!.single as Map),
                'actions': [((_actions['topics']!.single as Map)['actions'] as List).first],
              },
            ],
          });
        }
        if (req.method == 'POST' && p == '/v1/payments/read') {
          return _ok({'found': true, 'account_number': '0123456789', 'bank_name': 'Guaranty Trust Bank', 'bank_code': '058', 'payee': 'chidi'});
        }
        return server.answer(req);
      },
    );
    auth.me = _payer(auth.me!, owner: false);
    await openLink(t, auth);
    await t.tap(find.bySemanticsLabel('Add @chidi to your list of people to pay'));
    await settle(t);
    expect(server.sent('POST', '/v1/pay-collection').single, {
      'payee': 'chidi',
      'source': {'chat': _link, 'kind': 'link', 'message': 'm-acct', 'from': 'Mama Tunde\'s Market'},
    });
    expect(find.text('Added: 1 to pay'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('Pay several people: "From your list (2)" among the starting choices fills it with the list', (t) async {
    phone(t, 1000);
    final server = _List();
    final auth = linkAuth(server);
    for (final (i, name) in ['Chidi Okafor', 'Bola Ade'].indexed) {
      server.rows.add({
        'id': 'c$i',
        'saved_id': 's$i',
        'payee': 'p$i',
        'title': name,
        'name': '',
        'bank_name': 'Guaranty Trust Bank',
        'last4': '678$i',
        'amount_kobo': 1000000,
        'purpose': 'stock',
        'note': '',
        'source': {'kind': 'person', 'chat': 'x', 'message': 'm$i'},
        'added_at': '2026-11-22T09:00:00Z',
      });
    }
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _Host((c) => openPayMany(c, auth))));
    await settle(t);
    expect(find.text('From your list (2)'), findsOneWidget);
    await snap(t, 'collect-from-list-choice');
    await t.tap(find.text('From your list (2)'));
    await settle(t);
    expect(find.text('@p0'), findsOneWidget);
    expect(find.text('from a message in your chats · Guaranty Trust Bank ••6780'), findsOneWidget);
    expect(find.text('2 people · ₦20,000'), findsOneWidget);
    expect(find.text('From your list (2)'), findsNothing);
    await t.tap(find.text('Check'));
    await settle(t);
    expect(server.sent('POST', '/v1/pay-collection/batch'), hasLength(1));
    expect(find.text('Check before you pay'), findsOneWidget);
    // Back: the batch let go, so its rows are on the list again.
    await t.tap(find.text('Back'));
    await settle(t);
    expect(server.sent('POST', '/v1/payment-batches/B9/cancel'), isNotEmpty);
    expect(find.text('Pay several people'), findsWidgets);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('large text: the foot and the list cut nothing off', (t) async {
    final server = _List();
    server.rows.add({
      'id': 'c1',
      'saved_id': null,
      'payee': '',
      'title': 'Account ••6789',
      'name': 'Chidi Okafor',
      'bank_name': 'Guaranty Trust Bank',
      'last4': '6789',
      'amount_kobo': null,
      'purpose': '',
      'note': '',
      'source': {'kind': 'link', 'chat': _link, 'message': 'm-acct'},
      'added_at': '2026-11-22T09:00:00Z',
    });
    final auth = linkAuth(server);
    await expectLargeText(
      t,
      linkApp(auth, mode: TkMode.dark),
      drive: (t) async {
        await settle(t);
        await t.scrollUntilVisible(find.text("Mama Tunde's Market"), 200, scrollable: find.byType(Scrollable).first);
        await t.pump(const Duration(seconds: 1));
        await t.tap(find.text("Mama Tunde's Market"));
        await settle(t);
        await settle(t);
        await t.pump(const Duration(seconds: 1));
        expect(find.text('1 to pay'), findsOneWidget);
      },
    );
    await t.tap(find.bySemanticsLabel('Pay these 1 to pay'));
    await settle(t);
    expect(find.byType(PayManySheet), findsOneWidget);
    await t.pumpAndSettle();
    await expectPressable(t);
    await t.pumpWidget(const SizedBox());
    await t.pump(const Duration(seconds: 5));
  });
}

class _Host extends StatefulWidget {
  const _Host(this.open);
  final Future<Object?> Function(BuildContext context) open;

  @override
  State<_Host> createState() => _HostState();
}

class _HostState extends State<_Host> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => widget.open(context));
  }

  @override
  Widget build(BuildContext context) => const SizedBox.expand();
}
