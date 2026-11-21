// Pay a bill by transfer, in the chat (the payments flow, part 1;
// docs/spec/chat-payments-flow.md): the ways to pay in a small sheet (by
// transfer first, the last way used with a seller first next time, one way
// straight to it), the transfer card in each of its states (asking, open
// with its countdown, the last five minutes, paid with Pay the next one,
// part paid with Pay the rest, expired with Get a new number, and couldn't
// get one with the other ways), Copy without spaces, I need longer, the
// seller's card, and a customer's invoice paid the same way.

import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/links.dart' as links;
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart' show MemoryKeyStore;
import 'package:taskiem/app/sound.dart' as sound;
import 'package:taskiem/chat/bills_page.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/pay_transfer.dart';
import 'package:taskiem/chat/words.dart';
import 'package:taskiem/kit/kit.dart' show TkAppLook, TkSmallButton;
import 'package:taskiem/kit/page.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/look.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart';

final _at = DateTime.utc(2026, 11, 21, 13, 40);

Map<String, dynamic> _bill({List<String> ways = const ['transfer', 'taskiem', 'checkout'], Map<String, dynamic>? transfer, int due = 8300000}) => {
  'id': 'B1',
  'link': 'L1',
  'supplier': 'S1',
  'supplier_name': 'Bayo Rice Mills',
  'reference': 'INV-0042',
  'lines': [
    {'description': 'Rice 50kg', 'quantity': 1, 'unit_kobo': 8300000},
  ],
  'amount_kobo': 8300000,
  'paid_kobo': 8300000 - due,
  'due_kobo': due,
  'status': due == 0 ? 'paid' : 'open',
  'allow_part_payments': true,
  'disputed': false,
  'declared_paid_kobo': 0,
  'paying_kobo': 0,
  'sent_kobo': 0,
  'uncounted_kobo': 0,
  'pay_link': 'https://pay.taskiem.test/i/b1',
  'mirrored_at': '2026-11-21T13:52:00Z',
  'created_at': '2026-11-12T08:00:00Z',
  'transfer': transfer,
  'ways_to_pay': ways,
};

Map<String, dynamic> _open({int minutes = 60}) => {
  'state': 'open',
  'bank_name': 'Wema Bank',
  'account_number': '0123456789',
  'account_name': 'Bayo Rice Mills / Taskiem',
  'amount_kobo': 8300000,
  'expires_at': _at.add(Duration(minutes: minutes)).toIso8601String(),
};

http.Response _ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});

Future<void> _settle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}

/// A page with Pay on it, as a bill's card has it.
class _PayHost extends StatelessWidget {
  const _PayHost({required this.onPay});
  final void Function(BuildContext context) onPay;

  @override
  Widget build(BuildContext context) => TkPageScreen(
    title: 'A bill',
    children: [Builder(builder: (c) => TkSmallButton(label: 'Pay', primary: true, onPressed: () => onPay(c)))],
  );
}

/// The card in each of its states, as the chat shows them.
class _Gallery extends StatelessWidget {
  const _Gallery();

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final now = _at;
    const open = TransferNow(state: 'open', bank: 'Wema Bank', number: '0123456789', name: 'Bayo Rice Mills / Taskiem', amountKobo: 8300000);
    Widget card(TransferNow s, {VoidCallback? again, VoidCallback? longer, VoidCallback? next, List<(String, VoidCallback)> others = const []}) => Container(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
      child: TransferCard(s: s, now: now, number: 'INV-0042', onAgain: again, onLonger: longer, onNext: next, others: others),
    );
    void none() {}
    return TkPageScreen(
      title: 'By transfer',
      titleSize: 22,
      children: [
        card(const TransferNow(state: 'asking')),
        card(
          TransferNow(state: 'open', bank: open.bank, number: open.number, name: open.name, amountKobo: open.amountKobo, expiresAt: now.add(const Duration(minutes: 29, seconds: 12))),
          longer: none,
        ),
        card(TransferNow(state: 'open', bank: open.bank, number: open.number, name: open.name, amountKobo: open.amountKobo, expiresAt: now.add(const Duration(minutes: 4, seconds: 5)))),
        card(TransferNow(state: 'paid', amountKobo: 8300000, paidAt: DateTime(2026, 11, 21, 14, 52), next: 'B2'), next: none),
        card(const TransferNow(state: 'part', amountKobo: 8300000, receivedKobo: 5000000, leftKobo: 3300000), again: none),
        card(const TransferNow(state: 'expired', amountKobo: 8300000), again: none),
        card(
          const TransferNow(state: 'failed', problem: 'Too many numbers asked for this bill. Try again in a while, or pay on their checkout.'),
          again: none,
          others: [('Pay on their checkout', none)],
        ),
      ],
    );
  }
}

void main() {
  setUpAll(loadFonts);

  test('the countdown: good for, the last five minutes, stopped at zero', () {
    expect(clockLeft(const Duration(minutes: 29, seconds: 12)), '29:12');
    expect(clockLeft(const Duration(minutes: 4, seconds: 5)), '4:05');
    expect(clockLeft(const Duration(hours: 2, minutes: 1, seconds: 3)), '2:01:03');
    expect(clockLeft(const Duration(seconds: -4)), '0:00');
    expect(goodFor(_at.add(const Duration(minutes: 29, seconds: 12)), _at), (words: 'Good for 29:12', warn: false));
    expect(goodFor(_at.add(const Duration(minutes: 4, seconds: 5)), _at), (words: 'Expires in 4:05', warn: true));
    expect(goodFor(_at, _at).words, 'Expired');
    expect(goodFor(null, _at).words, '', reason: 'a number kept for good has no countdown');
    final s = TransferNow(state: 'open', expiresAt: _at);
    expect(s.at(_at.subtract(const Duration(seconds: 1))), 'open');
    expect(s.at(_at), 'expired', reason: 'by its own time, not only once the server says');
  });

  test('the ways: the last one used with the seller first; numbers read in groups', () {
    expect(orderWays(['transfer', 'taskiem', 'checkout'], null), ['transfer', 'taskiem', 'checkout']);
    expect(orderWays(['transfer', 'taskiem', 'checkout'], 'checkout'), ['checkout', 'transfer', 'taskiem']);
    expect(orderWays(['transfer', 'checkout'], 'taskiem'), ['transfer', 'checkout'], reason: 'a way no longer offered is not added');
    expect(spacedNumber('0123456789'), '0123 456 789');
    expect(spacedNumber('0123 456 789'), '0123 456 789');
    expect(pollEvery('asking'), const Duration(seconds: 2));
    expect(pollEvery('open'), const Duration(seconds: 10));
    expect(pollEvery('paid'), isNull);
  });

  test('the cards in the chat: the buyer asked, the seller sees who is paying', () {
    const buyer = LinkDocCard(doc: 'bill', event: 'transfer', reference: 'INV-0042', amountKobo: 8300000);
    const seller = LinkDocCard(doc: 'invoice', event: 'transfer', reference: 'INV-0042', amountKobo: 8300000);
    expect(cardLine(seller, false, 'Ada’s Kitchen'), 'Ada’s Kitchen is paying by transfer');
    expect(cardLine(buyer, true, 'Bayo Rice Mills'), 'You asked to pay INV-0042 by transfer');
    expect(isDiary(buyer, true), isFalse, reason: 'the buyer’s card shows, with the number');
    expect(cardStatus(seller, _at).word, 'Paying');
  });

  test('the bill\'s and the customer\'s answers read', () {
    final part = TransferNow.ofBill(Bill.fromJson(_bill(due: 3300000, transfer: {..._open(), 'state': 'part', 'received_kobo': 5000000})));
    expect((part.state, part.receivedKobo, part.leftKobo), ('part', 5000000, 3300000));
    final paid = TransferNow.ofBill(Bill.fromJson(_bill(due: 0, transfer: {..._open(), 'state': 'paid', 'next': 'B2'})));
    expect((paid.state, paid.next, paid.paidAt), ('paid', 'B2', DateTime.utc(2026, 11, 21, 13, 52)));
    final mine = TransferNow.ofInvoice(
      CustomerTransfer.fromJson({'invoice': 'I1', 'reference': 'INV-0007', 'due_kobo': 1500000, 'state': 'part', 'amount_kobo': 4500000, 'account_number': '0123456789'}),
    );
    expect((mine.receivedKobo, mine.leftKobo), (3000000, 1500000));
  });

  testWidgets('the card in each state, light and dark', (t) async {
    t.view.physicalSize = const Size(360, 2000);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await t.pumpWidget(const TaskiemApp(mode: TkMode.light, home: _Gallery()));
    await t.pumpAndSettle();
    await looks(t, 'pay-transfer-states');
    expect(find.text('Getting an account number…'), findsOneWidget);
    expect(find.text('Good for 29:12'), findsOneWidget);
    expect(find.text('Expires in 4:05'), findsOneWidget);
    expect(find.text('0123 456 789'), findsNWidgets(2));
    expect(find.text('Paid ₦83,000 · 14:52'), findsOneWidget);
    expect(find.text('Received ₦50,000 of ₦83,000'), findsOneWidget);
    expect(find.text('₦33,000 left'), findsOneWidget);
    expect(find.text('This number has expired. Don’t send to it.'), findsOneWidget);
    for (final b in ['I need longer', 'Pay the next one', 'Pay the rest, ₦33,000', 'Get a new number', 'Pay on their checkout', 'Try again']) {
      expect(find.bySemanticsLabel(b), findsOneWidget, reason: b);
    }
    // Words never on screen.
    for (final w in ['virtual account', 'dynamic', 'rail', 'reference']) {
      expect(find.textContaining(RegExp(w, caseSensitive: false)), findsNothing, reason: w);
    }
  });

  testWidgets('the card at large text: nothing cut off, all pressable', (t) async {
    await expectLargeText(t, const TaskiemApp(mode: TkMode.dark, home: _Gallery()));
  });

  testWidgets('Pay: the ways in a sheet; by transfer, the number comes, is copied, counts down, and turns paid', (t) async {
    t.view.physicalSize = const Size(360, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    var now = _at;
    final sent = <String>[];
    var transfer = <String, dynamic>{'state': 'asking'};
    var chimed = 0;
    final was = sound.chime;
    sound.chime = () async => chimed++;
    addTearDown(() => sound.chime = was);
    String? copied;
    t.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') copied = (call.arguments as Map)['text'] as String;
      return null;
    });
    addTearDown(() => t.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, null));
    final f = Fixture('chats');
    var asked = false;
    final auth = f.auth(
      also: (req) async {
        if (req.url.path != '/v1/bills/B1/transfer') return null;
        sent.add('${req.method} ${req.body}');
        if (req.method == 'POST') {
          asked = true;
          return _ok(_bill(transfer: {'state': 'asking'}), 202);
        }
        return _ok(_bill(transfer: asked ? transfer : null));
      },
    );
    final prefs = Prefs(MemoryKeyStore());
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: _PayHost(onPay: (c) => payBill(c, auth: auth, bill: 'B1', prefs: prefs, now: () => now)),
      ),
    );
    await _settle(t);
    await t.tap(find.bySemanticsLabel('Pay'));
    await _settle(t);
    await t.pumpAndSettle();
    expect(find.text('Pay ₦83,000'), findsOneWidget, reason: 'the sheet');
    final rows = ['Pay by transfer', 'Pay from Taskiem', 'Pay on their checkout'];
    final tops = [for (final r in rows) t.getTopLeft(find.text(r)).dy];
    expect(tops, [...tops]..sort(), reason: 'by transfer first, then from Taskiem, then their checkout');
    await looks(t, 'pay-ways');
    await t.tap(find.text('Pay by transfer'));
    await _settle(t);
    await t.pumpAndSettle();
    expect(sent, ['GET ', 'POST {}']);
    expect(prefs.payWays, {'S1': 'transfer'}, reason: 'remembered for this seller');
    expect(find.text('Getting an account number…'), findsOneWidget);
    // The number arrives; read again two seconds on.
    transfer = _open();
    await t.pump(const Duration(seconds: 2));
    await _settle(t);
    expect(find.text('Send exactly'), findsOneWidget);
    expect(find.text('₦83,000'), findsOneWidget);
    expect(find.text('0123 456 789'), findsOneWidget);
    expect(find.text('Good for 60:00'), findsOneWidget);
    expect(find.text('We’ll mark it paid here as soon as it lands.'), findsOneWidget);
    expect(find.text('Waiting for your transfer'), findsOneWidget);
    await looks(t, 'pay-transfer-sheet');
    await t.tap(find.bySemanticsLabel('Copy the account number'));
    await t.pump();
    expect(copied, '0123456789', reason: 'without its spaces');
    await t.pump(const Duration(seconds: 4));
    // The last five minutes.
    now = _at.add(const Duration(minutes: 55, seconds: 55));
    await t.pump(const Duration(seconds: 1));
    expect(find.text('Expires in 4:05'), findsOneWidget);
    // I need longer: asked again, for the longest.
    await t.tap(find.bySemanticsLabel('I need longer'));
    await _settle(t);
    expect(sent.last, 'POST {"longer":true}');
    // Paid.
    transfer = {..._open(), 'state': 'paid', 'next': 'B2'};
    await t.pump(const Duration(seconds: 10));
    await _settle(t);
    expect(find.text('Paid ₦83,000 · ${clock(DateTime.utc(2026, 11, 21, 13, 52))}'), findsOneWidget);
    expect(find.bySemanticsLabel('Pay the next one'), findsOneWidget);
    expect(find.text('0123 456 789'), findsNothing, reason: 'the countdown and the number go');
    expect(chimed, 1, reason: 'the good-moment sound');
    final n = sent.length;
    await t.pump(const Duration(seconds: 30));
    expect(sent.length, n, reason: 'paid: not asked again');
  });

  testWidgets('expired, then a new number; part paid, then Pay the rest', (t) async {
    t.view.physicalSize = const Size(360, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    var now = _at;
    final sent = <String>[];
    var answer = _bill(transfer: _open(minutes: 1));
    final auth = Fixture('chats').auth(
      also: (req) async {
        if (req.url.path != '/v1/bills/B1/transfer') return null;
        sent.add(req.method);
        return _ok(answer);
      },
    );
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: _PayHost(onPay: (c) => payBill(c, auth: auth, bill: 'B1', now: () => now)),
      ),
    );
    await _settle(t);
    await t.tap(find.bySemanticsLabel('Pay'));
    await _settle(t);
    await t.tap(find.text('Pay by transfer'));
    await _settle(t);
    expect(find.text('Expires in 1:00'), findsOneWidget);
    now = _at.add(const Duration(minutes: 1));
    await t.pump(const Duration(seconds: 1));
    expect(find.text('This number has expired. Don’t send to it.'), findsOneWidget, reason: 'stops at zero');
    expect(find.text('0123 456 789'), findsNothing);
    answer = _bill(transfer: _open());
    await t.tap(find.bySemanticsLabel('Get a new number'));
    await _settle(t);
    expect(sent.last, 'POST');
    expect(find.text('Good for 59:00'), findsOneWidget);
    answer = _bill(due: 3300000, transfer: {..._open(), 'state': 'part', 'received_kobo': 5000000});
    await t.pump(const Duration(seconds: 10));
    await _settle(t);
    expect(find.text('Received ₦50,000 of ₦83,000'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Pay the rest, ₦33,000'));
    await _settle(t);
    expect(sent.last, 'POST');
  });

  testWidgets('couldn\'t get one: the words, and the other ways', (t) async {
    t.view.physicalSize = const Size(360, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final opened = <Uri>[];
    final was = links.openInBrowser;
    links.openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => links.openInBrowser = was);
    final auth = Fixture('chats').auth(
      also: (req) async => switch ((req.method, req.url.path)) {
        ('GET', '/v1/bills/B1/transfer') => _ok(_bill(ways: ['transfer', 'checkout'])),
        ('POST', '/v1/bills/B1/transfer') => _ok({'error': 'Too many numbers asked for this bill. Try again in a while, or pay on their checkout.', 'code': 'too_many'}, 429),
        _ => null,
      },
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _PayHost(onPay: (c) => payBill(c, auth: auth, bill: 'B1', now: () => _at))));
    await _settle(t);
    await t.tap(find.bySemanticsLabel('Pay'));
    await _settle(t);
    await t.tap(find.text('Pay by transfer'));
    await _settle(t);
    expect(find.text('Too many numbers asked for this bill. Try again in a while, or pay on their checkout.'), findsOneWidget);
    await t.tap(find.bySemanticsLabel('Pay on their checkout'));
    await _settle(t);
    expect(opened.single.toString(), 'https://pay.taskiem.test/i/b1');
  });

  testWidgets('one way: straight to it; the way last used comes first', (t) async {
    t.view.physicalSize = const Size(360, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final opened = <Uri>[];
    final was = links.openInBrowser;
    links.openInBrowser = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => links.openInBrowser = was);
    var ways = ['checkout'];
    final auth = Fixture('chats').auth(also: (req) async => req.url.path == '/v1/bills/B1/transfer' ? _ok(_bill(ways: ways)) : null);
    final prefs = Prefs(MemoryKeyStore());
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: _PayHost(onPay: (c) => payBill(c, auth: auth, bill: 'B1', prefs: prefs))));
    await _settle(t);
    await t.tap(find.bySemanticsLabel('Pay'));
    await _settle(t);
    expect(opened, hasLength(1), reason: 'no sheet with one way');
    expect(find.text('Pay by transfer'), findsNothing);
    ways = ['transfer', 'taskiem', 'checkout'];
    await t.tap(find.bySemanticsLabel('Pay'));
    await _settle(t);
    await t.pumpAndSettle();
    expect(t.getTopLeft(find.text('Pay on their checkout')).dy, lessThan(t.getTopLeft(find.text('Pay by transfer')).dy), reason: 'checkout, used last, first');
  });

  testWidgets('a customer pays their invoice by transfer from its card', (t) async {
    t.view.physicalSize = const Size(360, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final sent = <String>[];
    final auth = Fixture('chats').auth(
      also: (req) async {
        if (req.url.path != '/v1/me/chats/C1/invoices/I1/transfer') return null;
        sent.add('${req.method} ${req.body}');
        return _ok({
          'invoice': 'I1',
          'reference': 'INV-0007',
          'due_kobo': 4500000,
          'state': 'open',
          'bank_name': 'Wema Bank',
          'account_number': '0123456789',
          'account_name': 'Bola Stores',
          'amount_kobo': 4500000,
          'expires_at': _at.add(const Duration(minutes: 60)).toIso8601String(),
        });
      },
    );
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.dark,
        home: _PayHost(
          onPay: (c) => payMyInvoice(c, auth: auth, chat: 'C1', invoice: 'I1', business: 'Bola Stores', number: 'INV-0007', url: 'https://pay.taskiem.test/i/7', dueKobo: 4500000, now: () => _at),
        ),
      ),
    );
    await _settle(t);
    await t.tap(find.bySemanticsLabel('Pay'));
    await _settle(t);
    await t.pumpAndSettle();
    expect(find.text('Pay on the invoice’s page'), findsOneWidget);
    await t.tap(find.text('Pay by transfer'));
    await _settle(t);
    expect(sent, ['POST {}']);
    expect(find.text('Pay Bola Stores'), findsOneWidget);
    expect(find.text('Good for 60:00'), findsOneWidget);
    expect(find.text('Bola Stores'), findsOneWidget, reason: 'the account name');
    await looks(t, 'pay-invoice-transfer');
    await t.pump(const Duration(seconds: 10));
    await _settle(t);
    expect(sent, ['POST {}', 'GET '], reason: 'read again while it waits');
  });

  testWidgets('in the chat: Pay on the bill\'s card opens the ways; the transfer card shows the number, kept up to date', (t) async {
    t.view.physicalSize = const Size(360, 1400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    const link = '3af349e2-b776-4306-bdb8-10df59687c55';
    final f = Fixture('chat-tunde');
    final reads = <String>[];
    var withTransfer = false;
    Map<String, Object> card(String id, String event, String at) => {
      'id': id,
      'link': link,
      'mine': event == 'transfer',
      'kind': 'card',
      'card': {'doc': 'bill', 'event': event, 'reference': 'INV-0042', 'id': 'B1', 'amount_kobo': 8300000},
      'sender_name': 'Tunde',
      'created_at': at,
      'read_at': at,
    };
    final chats = ChatsData(
      f.auth(
        also: (req) async {
          final p = req.url.path;
          if (req.method == 'GET' && p == '/v1/link-chats/$link/messages' && !req.url.queryParameters.containsKey('after')) {
            final page = f.json(p);
            (page['messages'] as List).add(card('bill-1', 'bill', '2026-11-11T12:25:00Z'));
            if (withTransfer) (page['messages'] as List).add(card('tr-1', 'transfer', '2026-11-11T12:26:00Z'));
            return _ok(page);
          }
          if (p == '/v1/bills/B1/transfer') {
            reads.add(req.method);
            return _ok(_bill(transfer: withTransfer ? _open() : null));
          }
          if (req.method == 'GET') return null;
          return _ok(<String, Object>{});
        },
      ),
      now: () => _at,
    )..load();
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == link);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    await _settle(t);
    await t.tap(find.bySemanticsLabel('Pay INV-0042'));
    await _settle(t);
    await t.pumpAndSettle();
    expect(find.text('Pay by transfer'), findsOneWidget, reason: 'a sheet over the chat, not a new tab');
    await t.tap(find.bySemanticsLabel('Close').last);
    await t.pumpAndSettle();
    // The number asked for: its card, the newest of the bill, follows it.
    withTransfer = true;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(key: const ValueKey(2), data: ConversationData(chats, conv, follow: false))));
    await _settle(t);
    expect(find.text('0123 456 789'), findsOneWidget);
    expect(find.text('Good for 60:00'), findsOneWidget);
    expect(find.bySemanticsLabel('Pay INV-0042'), findsNothing, reason: 'Pay is on the newest card only');
    await t.pump(const Duration(seconds: 10));
    await _settle(t);
    expect(reads.where((r) => r == 'GET').length, greaterThanOrEqualTo(3), reason: 'read again while it waits');
  });

  testWidgets('the bill\'s page: Pay opens the ways; an open number shows on it', (t) async {
    t.view.physicalSize = const Size(360, 1600);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final auth = Fixture('chats').auth(
      also: (req) async => switch (req.url.path) {
        // The page counts down by the phone's own clock.
        '/v1/bills/B1' || '/v1/bills/B1/transfer' => _ok(_bill(transfer: {..._open(), 'expires_at': DateTime.now().add(const Duration(minutes: 30)).toUtc().toIso8601String()})),
        '/v1/bills/pay' => _ok({'workflow_id': 'W1', 'approver_role': 'owner'}),
        _ => null,
      },
    );
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: BillPage(auth: auth, id: 'B1', follow: false)));
    await _settle(t);
    expect(find.text('Pay by transfer, or 2 other ways.'), findsOneWidget);
    expect(find.text('0123 456 789', skipOffstage: false), findsOneWidget, reason: 'the number asked for, on the page');
    expect(find.bySemanticsLabel('Pay from Taskiem'), findsNothing, reason: 'one Pay, its ways in the sheet');
    await t.tap(find.text('Pay ₦83,000'));
    await _settle(t);
    await t.pumpAndSettle();
    expect(find.text('Pay on their checkout'), findsOneWidget);
  });

  testWidgets('the app look holds in the sheet', (t) async {
    // A sheet opens inside the app's look (mobile/CLAUDE.md).
    await t.pumpWidget(const TaskiemApp(mode: TkMode.light, home: _Gallery()));
    await t.pumpAndSettle();
    expect(TkAppLook.on(t.element(find.byType(TransferCard).first)), isTrue);
  });
}
