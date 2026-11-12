// The app's own code against the real server (docs/spec/mobile-app.md §8;
// T1 and T3 of docs/spec/mobile-app-m3-fixes.md): two businesses sign up in
// the app and talk (from two phones, each its own session), a message that
// did not go is sent again as the same message, the live stream tells the
// other phone, and an owner talks to Taskiem: makes their page, publishes
// and runs a workflow, and approves its request from the card.
//
// Needs the server web/e2e/serve.sh starts: TASKIEM_E2E_URL names it
// (http://127.0.0.1:18080). Without it these tests are skipped.
// The web's e2e scenarios they follow are listed in
// docs/spec/chat-scenarios.md.

@Tags(['server'])
library;

import 'dart:io';
import 'dart:math';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/approve.dart' show decideApproval;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/live.dart';
import 'package:taskiem/chat/taskiem.dart';

final _base = Platform.environment['TASKIEM_E2E_URL'];

/// A phone's network: real, but it can be cut (a send that does not go).
class _Network extends http.BaseClient {
  final _inner = http.Client();
  bool down = false;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    if (down) throw http.ClientException('no network', request.url);
    return _inner.send(request);
  }
}

/// A phone: its own network, its own session.
class _Phone {
  _Phone(this.auth, this.net);
  final Auth auth;
  final _Network net;
  late final live = ChatLive(auth.transport);
  late final chats = ChatsData(auth, live: live);
}

final _stamp = DateTime.now().millisecondsSinceEpoch.toRadixString(36) + Random().nextInt(1 << 20).toRadixString(36);

/// Signs a business up in the app, as the sign-up screen does.
Future<_Phone> _signUp(String business) async {
  final net = _Network();
  final auth = Auth(
    transport: Transport(base: _base!, client: net),
    store: SessionStore(MemoryKeyStore(), describe: () async => (name: '$business phone', platform: 'android')),
  );
  final r = await auth.api.signup(
    body: SignupRequest(
      tenant: '$business $_stamp',
      name: '${business.split(' ').first} Owner',
      email: '${business.split(' ').first.toLowerCase()}-$_stamp@app.test',
      password: 'correct horse battery',
      acceptTerms: true,
      bearer: true,
      device: await auth.store.device(),
    ),
  );
  await auth.signedInWith(r.token);
  expect(auth.state, AuthState.signedIn);
  return _Phone(auth, net);
}

/// Waits for [ok], asking again every 200 ms, for at most [within].
Future<void> _until(Future<bool> Function() ok, {Duration within = const Duration(seconds: 10), String? what}) async {
  final end = DateTime.now().add(within);
  while (!await ok()) {
    if (DateTime.now().isAfter(end)) fail('timed out: ${what ?? 'waiting'}');
    await Future<void>.delayed(const Duration(milliseconds: 200));
  }
}

void main() {
  final skip = _base == null ? 'TASKIEM_E2E_URL is not set: start web/e2e/serve.sh and set it' : null;

  group('two businesses talk, from two phones', skip: skip, () {
    late _Phone seller, buyer;
    late ConversationData buying, selling;

    setUpAll(() async {
      seller = await _signUp('Dangote Depot');
      buyer = await _signUp('Ada Kitchen');
      // The seller can be found; the buyer asks to link, from the app.
      await seller.auth.transport.send('PUT', '/v1/link-settings', body: {'findable': true});
      final found = await buyer.auth.api.searchLinks(q: 'dangote depot $_stamp');
      await buyer.auth.transport.send('POST', '/v1/links', body: {'business': found.results.first.business, 'role': 'supplier'});
      // The seller accepts from the chats list, as the row's Accept does.
      await seller.chats.load();
      final asked = seller.chats.links.firstWhere((l) => l.state == 'asked_us');
      expect(await seller.chats.answer(asked, 'accept'), isNull);
      await buyer.chats.load();
      await seller.chats.load();
      buying = ConversationData(buyer.chats, buyer.chats.conversations.single, follow: false);
      selling = ConversationData(seller.chats, seller.chats.conversations.single, follow: false);
      await buying.open();
      await selling.open();
    });

    tearDownAll(() {
      buying.dispose();
      selling.dispose();
      for (final p in [seller, buyer]) {
        p.chats.dispose();
        p.live.dispose();
      }
    });

    test('a message sent is the other side\'s unread, read, then blue ticks', () async {
      await buying.send('Do you have rice today?');
      final mine = buying.messages!.lastWhere((m) => m.mine);
      expect(mine.body, 'Do you have rice today?');
      expect(buying.outbox, isEmpty, reason: 'the server\'s copy took its place');
      await seller.chats.load();
      final row = seller.chats.conversations.single;
      expect(row.unread, 1);
      expect(row.last?.body, 'Do you have rice today?');
      // Opened on the seller's phone: read.
      await selling.openNewest();
      expect(selling.messages!.last.body, 'Do you have rice today?');
      await _until(() async {
        await buying.pull();
        await buying.refresh();
        return buying.messages!.lastWhere((m) => m.mine).readAt != null;
      }, what: 'the buyer\'s ticks turning blue');
    });

    test('a reply quotes, a reaction both see', () async {
      final asked = selling.messages!.lastWhere((m) => !m.mine);
      await selling.send('Yes: ₦78,000 a 50kg bag', reply: asked);
      await buying.pull();
      final reply = buying.messages!.last;
      expect(reply.body, 'Yes: ₦78,000 a 50kg bag');
      expect(reply.reply?.pairId, buying.messages!.lastWhere((m) => m.mine).id);
      await buying.react(reply, '👍');
      await _until(() async {
        await selling.refresh();
        final m = selling.messages!.lastWhere((m) => m.mine);
        return (m.reactions ?? const []).any((r) => r.emoji == '👍');
      }, what: 'the reaction on the seller\'s side');
    });

    test('a message that did not go is sent again as the same message, once', () async {
      buyer.net.down = true;
      await buying.send('Send 4 bags please');
      final o = buying.outbox.single;
      expect(o.failed, isTrue);
      buyer.net.down = false;
      await buying.retry(o);
      await buying.retry(o);
      expect(buying.outbox, isEmpty);
      await selling.pull();
      expect(selling.messages!.where((m) => m.body == 'Send 4 bags please'), hasLength(1));
    });

    test('the live stream tells the other phone at once', () async {
      final heard = <LiveEvent>[];
      final sub = seller.live.events.listen(heard.add);
      seller.live.start();
      await _until(() async => seller.live.connected, what: 'the stream connecting');
      await buying.send('On my way to collect');
      await _until(() async => heard.whereType<LiveMessage>().any((e) => e.message.body == 'On my way to collect'), what: 'the message on the stream');
      await sub.cancel();
      seller.live.stop();
    });
  });

  group('an owner talks to Taskiem in the app', skip: skip, () {
    late _Phone owner;
    late TaskiemData taskiem;

    setUpAll(() async {
      owner = await _signUp('Mama Put');
      await owner.auth.api.putChatTools(body: const PutChatToolsRequest(chatTools: true));
      await owner.chats.load();
      taskiem = TaskiemData(owner.chats, follow: false);
      await taskiem.open();
    });

    tearDownAll(() {
      taskiem.dispose();
      owner.chats.dispose();
      owner.live.dispose();
    });

    String last() => taskiem.messages!.lastWhere((x) => !x.m.inbound).m.text;
    TaskiemChatMessagesItem lastItem() => taskiem.messages!.lastWhere((x) => !x.m.inbound).m;

    test('sets up their page, one question at a time, and Taskiem says what publishing needs', () async {
      await taskiem.say(text: 'make my page');
      expect(last(), contains('1/5. What is your business called?'));
      await taskiem.say(text: 'Mama Put Kitchen');
      expect(last(), contains('2/5.'));
      await taskiem.say(text: 'Home-cooked jollof and small chops');
      expect(last(), contains('3/5.'));
      await taskiem.say(text: 'Yaba, Lagos');
      expect(last(), contains('4/5.'));
      await taskiem.say(text: 'daily 9am-9pm');
      expect(last(), contains('5/5.'));
      await taskiem.say(text: 'skip');
      expect(last(), contains('Your page is saved as a draft'));
      // Its reply button, pressed as the bubble presses it.
      final publish = lastItem().buttons!.firstWhere((b) => b.title == 'Yes, publish');
      await taskiem.say(reply: publish.id, text: publish.title);
      // This server has no phone to verify: Taskiem says so, in the app.
      expect(last(), contains('publishing needs a verified phone'));
    });

    test('publishes a workflow by name, runs it, and approves its request from the card', () async {
      final wf = await owner.auth.transport.send(
        'POST',
        '/v1/workflows',
        body: {
          'name': 'Pay a supplier',
          'definition': {
            'schema': 'wd/v1',
            'id': 'wf_paySupplierApp',
            'version': 1,
            'name': 'Pay a supplier',
            'trigger': {'type': 'manual'},
            'steps': [
              {
                'id': 'ok',
                'name': 'Pay Kunle Supplies',
                'type': 'approval',
                'config': {
                  'role': 'owner',
                  'subject': {'amount': 4500000, 'to': 'Kunle Supplies'},
                },
              },
            ],
          },
        },
      );
      expect(wf, isA<Map<String, dynamic>>());
      await taskiem.say(text: 'publish Pay a supplier');
      expect(last(), contains('is published'));
      await taskiem.say(text: 'run Pay a supplier');
      final run = lastItem().buttons!.firstWhere((b) => b.title == 'Yes, run it');
      await taskiem.say(reply: run.id, text: run.title);
      expect(last(), contains('Started Pay a supplier'));
      // The approval comes as a card at the end of the conversation.
      ChatAlert? card;
      await _until(() async {
        await taskiem.loadCards();
        card = taskiem.alerts.where((a) => a.kind == 'approval' && a.title.contains('Pay Kunle Supplies')).firstOrNull;
        return card != null;
      }, what: 'the approval card');
      await decideApproval(owner.auth, card!.approval!, 'approved', comment: 'Paid by transfer');
      await _until(() async {
        await taskiem.loadCards();
        return !taskiem.alerts.any((a) => a.key == card!.key);
      }, what: 'the card going once decided');
    });
  });
}
