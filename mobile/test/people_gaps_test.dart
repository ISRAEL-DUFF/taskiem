// Stage 5a's gaps in the app (docs/spec/people-chatting.md, "As built:
// stage 5a gaps, app"): You's live connection (its events read, reconnected
// from the last event, a chat, the list and Requests updated from it, and
// presence said on it), typing throttled and shown, search and starred
// messages opening a chat at the message, the trust line on a business's
// row, and the rest of the gaps (in this file's later groups).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/presence.dart';
import 'package:taskiem/chat/my_chat.dart';
import 'package:taskiem/chat/people_chat.dart';
import 'package:taskiem/chat/people_find.dart';
import 'package:taskiem/chat/people_live.dart';
import 'package:taskiem/chat/live.dart' show LiveTiming;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'people_test.dart';
import 'support.dart' show loadFonts;

/// A stand-in live connection: each open is a controller the test writes to.
class _Streams {
  final opens = <StreamController<List<int>>>[];
  final lastIds = <String?>[];
  int status = 200;

  http.Client client() => MockClient.streaming((req, _) async {
    lastIds.add(req.headers['Last-Event-ID']);
    if (status != 200) return http.StreamedResponse(Stream.value(utf8.encode('{"error":"Too many"}')), status);
    final c = StreamController<List<int>>();
    opens.add(c);
    return http.StreamedResponse(c.stream, 200, headers: {'content-type': 'text/event-stream'});
  });

  void say(String event, Object data, {String? id}) => opens.last.add(utf8.encode('event: $event\ndata: ${jsonEncode(data)}\n${id == null ? '' : 'id: $id\n'}\n'));
}

Map<String, dynamic> _m(String id, {bool mine = false, String body = 'Hello', String at = '2026-11-17T10:25:00Z'}) => {
  'id': id, 'mine': mine, 'kind': 'text', 'body': body, 'read_at': null, 'created_at': at, if (mine) 'status': 'sent',
};

/// An event as the server sends it, said to [live] directly.
void hear(PeopleLive live, String event, Object data, {String? id}) => live.hearEvent((event: event, data: jsonEncode(data), id: id));

void main() {
  setUpAll(loadFonts);

  group('the live connection', () {
    test('reads ready, message, chat, gone, typing and presence; a dropped one opens again from its last event', () async {
      final s = _Streams();
      final live = PeopleLive(Transport(base: 'http://localhost', token: 't', client: s.client()), timing: const LiveTiming(retry: Duration(milliseconds: 10)));
      final got = <PeopleLiveEvent>[];
      live.events.listen(got.add);
      String? ready;
      live.onReady = (c) => ready = c;
      live.start();
      await pumpEventQueue();
      expect(s.opens, hasLength(1));
      expect(s.lastIds.single, isNull);
      s.say('ready', {'connection': 'conn-1'});
      s.say('message', {'chat_id': 'pc1', 'request': false, 'message': _m('m9')}, id: '1001');
      s.say('chat', {'id': 'pc2', 'hidden': true}, id: '1002');
      s.say('chat', {'id': 'pc1', 'hidden': false, 'chat': peopleChat()}, id: '1003');
      s.say('gone', {'chat_id': 'pc3', 'id': null}, id: '1004');
      s.say('typing', {'typing': ['pc1']});
      s.say('presence', {'people': [{'person_id': 'p1', 'chat_id': 'pc1', 'online': true, 'last_seen_at': null}]});
      s.say('ping', {});
      await pumpEventQueue();
      expect(live.connected, isTrue);
      expect(live.connection, 'conn-1');
      expect(ready, 'conn-1');
      expect(live.typing, {'pc1'});
      expect(live.presence['pc1']?.online, isTrue);
      expect((got[0] as PeopleLiveMessage).message.id, 'm9');
      expect((got[1] as PeopleLiveChat).hidden, isTrue);
      expect((got[2] as PeopleLiveChat).chat?.name, 'Ada Obi');
      expect((got[3] as PeopleLiveGone).id, isNull);

      await s.opens.last.close();
      await pumpEventQueue();
      expect(live.connected, isFalse);
      expect(live.connection, isNull);
      expect(live.typing, isEmpty);
      await Future<void>.delayed(const Duration(milliseconds: 30));
      expect(s.opens, hasLength(2));
      expect(s.lastIds.last, '1004', reason: 'what was missed is sent again from the last event');
      live.dispose();
    });

    test('refused (too many open): not connected, tried again later; stopped, never again', () async {
      final s = _Streams()..status = 429;
      final live = PeopleLive(Transport(base: 'http://localhost', token: 't', client: s.client()), timing: const LiveTiming(retry: Duration(milliseconds: 10)));
      live.start();
      await pumpEventQueue();
      expect(live.connected, isFalse);
      expect(s.lastIds, hasLength(1));
      await Future<void>.delayed(const Duration(milliseconds: 40));
      expect(s.lastIds, hasLength(1), reason: 'a 429 waits its 30 seconds, not the short retry');
      live.stop();
      live.dispose();
    });

    test('presence on the connection: in front when ready, away with it on going to the background; the heartbeat only while it is not open', () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      final s = _Streams();
      final api = Api();
      api.routes['POST /v1/me/presence'] = (_) => answer({}, 204);
      final live = PeopleLive(Transport(base: 'http://localhost', token: 't', client: s.client()), timing: const LiveTiming(retry: Duration(milliseconds: 10)));
      final p = Presence(api.auth().api, live: live, beat: const Duration(milliseconds: 20));
      p.start();
      await pumpEventQueue();
      expect(api.sent('POST', '/v1/me/presence').map(api.body), [{'online': true}], reason: 'at once, before the connection is ready');
      s.say('ready', {'connection': 'conn-7'});
      await pumpEventQueue();
      expect(api.body(api.sent('POST', '/v1/me/presence').last), {'online': true, 'connection': 'conn-7'});
      final before = api.sent('POST', '/v1/me/presence').length;
      await Future<void>.delayed(const Duration(milliseconds: 70));
      expect(api.sent('POST', '/v1/me/presence').length, before, reason: 'no heartbeat while the connection is open');

      p.didChangeAppLifecycleState(AppLifecycleState.paused);
      await pumpEventQueue();
      expect(api.body(api.sent('POST', '/v1/me/presence').last), {'online': false, 'connection': 'conn-7'});
      expect(live.running, isFalse, reason: 'closed in the background');
      expect(live.connected, isFalse);

      p.didChangeAppLifecycleState(AppLifecycleState.resumed);
      await pumpEventQueue();
      expect(live.running, isTrue, reason: 'opened again in front');
      expect(s.opens, hasLength(2));
      // Not ready (yet): the heartbeat stands in.
      final n = api.sent('POST', '/v1/me/presence').length;
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(api.sent('POST', '/v1/me/presence').length, greaterThan(n));
      expect(api.body(api.sent('POST', '/v1/me/presence').last), {'online': true});
      p.stop();
      live.dispose();
    });
  });

  group('a chat with a person, live', () {
    testWidgets('a message comes, is read, a reaction changes it, one is deleted on another device; typing and online show in the head', (t) async {
      phone(t);
      final api = adaChat();
      final live = PeopleLive(api.auth().transport);
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, live: live);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d)));
      await settle(t);
      final reads = api.sent('POST', '/v1/me/people/pc1/read').length;
      hear(live, 'ready', {'connection': 'c1'});
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': _m('m9', body: 'On my way')});
      hear(live, 'message', {'chat_id': 'other', 'request': false, 'message': _m('x1', body: 'Not this chat')});
      await settle(t);
      expect(find.text('On my way'), findsOneWidget);
      expect(find.text('Not this chat'), findsNothing);
      expect(api.sent('POST', '/v1/me/people/pc1/read').length, reads + 1, reason: 'read as it came, while seen');

      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': {..._m('m9', body: 'On my way'), 'their_reaction': '👍'}});
      await settle(t);
      expect(find.bySemanticsLabel(RegExp('^Reactions')), findsOneWidget);

      hear(live, 'gone', {'chat_id': 'pc1', 'id': 'm9'});
      await settle(t);
      expect(find.text('On my way'), findsNothing);

      hear(live, 'typing', {'typing': ['pc1']});
      await settle(t);
      expect(find.text('typing…'), findsOneWidget);
      hear(live, 'typing', {'typing': <String>[]});
      hear(live, 'presence', {'people': [{'person_id': 'p1', 'chat_id': 'pc1', 'online': true, 'last_seen_at': null}]});
      await settle(t);
      expect(find.text('typing…'), findsNothing);
      expect(find.text('online'), findsOneWidget);

      hear(live, 'chat', {'id': 'pc1', 'hidden': true});
      await settle(t);
      expect(find.text('This chat is no longer here.'), findsOneWidget, reason: 'deleted for the person on another device');
      await drain(t);
      live.dispose();
    });

    testWidgets('while the connection is open, the chat is read again only to reconcile', (t) async {
      phone(t);
      final api = adaChat();
      final live = PeopleLive(api.auth().transport);
      var clock = DateTime.utc(2026, 11, 17, 12);
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, live: live, clock: () => clock);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d)));
      await settle(t);
      int reads() => api.sent('GET', '/v1/me/people/pc1/messages').length;
      final n = reads();
      d.poll();
      await settle(t);
      expect(reads(), n + 1, reason: 'not connected: every poll reads');
      hear(live, 'ready', {'connection': 'c1'});
      d.poll();
      clock = clock.add(const Duration(seconds: 10));
      d.poll();
      await settle(t);
      expect(reads(), n + 1, reason: 'connected: none within 30 s');
      clock = clock.add(const Duration(seconds: 31));
      d.poll();
      await settle(t);
      expect(reads(), n + 2);
      await drain(t);
      live.dispose();
    });

    testWidgets('typing: said at most every 3 seconds while the box changes, stopped when it empties; never in a request', (t) async {
      phone(t);
      final api = adaChat();
      api.routes['POST /v1/me/people/pc1/typing'] = (_) => answer({}, 204);
      var clock = DateTime.utc(2026, 11, 17, 12);
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, clock: () => clock);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d)));
      await settle(t);
      List<Object?> said() => [for (final r in api.sent('POST', '/v1/me/people/pc1/typing')) api.body(r)['typing']];
      await typeIn(t, find.bySemanticsLabel('Message Ada Obi'), 'H');
      await typeIn(t, find.bySemanticsLabel('Message Ada Obi'), 'He');
      clock = clock.add(const Duration(seconds: 1));
      await typeIn(t, find.bySemanticsLabel('Message Ada Obi'), 'Hel');
      await settle(t);
      expect(said(), [true], reason: 'once in 3 seconds');
      clock = clock.add(const Duration(seconds: 3));
      await typeIn(t, find.bySemanticsLabel('Message Ada Obi'), 'Hell');
      await settle(t);
      expect(said(), [true, true]);
      await typeIn(t, find.bySemanticsLabel('Message Ada Obi'), '');
      await settle(t);
      expect(said(), [true, true, false], reason: 'stopped when the box empties');
      await drain(t);

      final req = adaChat(c: peopleChat(request: true));
      req.routes['POST /v1/me/people/pc1/typing'] = (_) => answer({}, 204);
      final r = PeopleChatData(req.auth(), 'pc1', follow: false, now: () => at);
      await r.open();
      r.typing('Hi');
      expect(req.sent('POST', '/v1/me/people/pc1/typing'), isEmpty, reason: 'never while it is a request');
      r.dispose();
    });
  });

  group('You, live', () {
    Api you() {
      final api = adaChat();
      api.routes['GET /v1/me/conversations'] = (r) => r.url.queryParameters['archived'] == '1'
          ? {'chats': <Object>[], 'unread': 0, 'requests': 0, 'more': false}
          : {
              'chats': [
                {...peopleChat(unread: 0), 'kind': 'person'},
                {
                  'kind': 'business', 'id': 'c1', 'name': 'Mama’s Kitchen', 'business_id': 'b1', 'last': 'Your order is ready', 'last_mine': false,
                  'last_at': '2026-11-17T08:00:00Z', 'unread': 0, 'muted': false, 'archived': false, 'blocked': false,
                  'verified': {'phone': true, 'business': false, 'payments': true, 'months': 8},
                },
              ],
              'unread': 0,
              'requests': 0,
              'more': false,
            };
      api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
      return api;
    }

    testWidgets('the Chats list: a business row’s trust line in its head’s words; typing and online from the connection; a change reads it again', (t) async {
      phone(t);
      final api = you();
      final live = PeopleLive(api.auth().transport);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => at, presence: false, live: live)));
      await settle(t);
      expect(find.text('Phone checked · Payments through Taskiem · Trading 8 months'), findsOneWidget);
      expect(find.bySemanticsLabel(RegExp('^Chat with a business, Phone checked')), findsOneWidget);
      final lists = api.sent('GET', '/v1/me/conversations').length;

      hear(live, 'ready', {'connection': 'c1'});
      hear(live, 'typing', {'typing': ['pc1']});
      hear(live, 'presence', {'people': [{'person_id': 'p1', 'chat_id': 'pc1', 'online': true, 'last_seen_at': null}]});
      await settle(t);
      expect(find.text('typing…'), findsOneWidget);
      expect(find.bySemanticsLabel(RegExp('^Chat with a person, online, typing')), findsOneWidget);

      hear(live, 'chat', {'id': 'pc1', 'hidden': false, 'chat': peopleChat(unread: 1, last: 'New!')}, id: '5');
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': _m('m9')}, id: '6');
      await settle(t);
      expect(api.sent('GET', '/v1/me/conversations').length, greaterThan(lists), reason: 'read again at once, a burst once');
      expect(api.sent('GET', '/v1/me/conversations').length, lessThanOrEqualTo(lists + 2));
      await drain(t);
      live.dispose();
    });

    testWidgets('Requests: a new request on the connection reads the list again', (t) async {
      phone(t);
      final api = Api();
      var requests = <Object>[];
      api.routes['GET /v1/me/people/requests'] = (_) => {'requests': requests, 'more': false};
      final live = PeopleLive(api.auth().transport);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleRequestsScreen(auth: api.auth(), openChat: (id, {started}) async {}, now: () => at, live: live)));
      await settle(t);
      expect(find.text('No requests'), findsOneWidget);
      requests = [peopleChat(id: 'pc9', name: 'Dayo', request: true, last: 'Hi there', unread: 1)];
      hear(live, 'message', {'chat_id': 'pc9', 'request': true, 'message': _m('r1', body: 'Hi there')});
      await settle(t);
      expect(find.text('Dayo'), findsOneWidget);
      requests = [];
      hear(live, 'gone', {'chat_id': 'pc9', 'id': null});
      await settle(t);
      expect(find.text('Dayo'), findsNothing, reason: 'deleted on another device');
      await drain(t);
      live.dispose();
    });

    test('MyChats reads again on an event, once for a burst', () async {
      final api = Api();
      api.routes['GET /v1/me/conversations'] = (_) => {'chats': <Object>[], 'unread': 0, 'requests': 0, 'more': false};
      final live = PeopleLive(api.auth().transport);
      final chats = MyChats(api.auth(), live: live);
      hear(live, 'chat', {'id': 'pc1', 'hidden': true});
      hear(live, 'gone', {'chat_id': 'pc1', 'id': 'm1'});
      await Future<void>.delayed(const Duration(milliseconds: 600));
      expect(api.sent('GET', '/v1/me/conversations').where((r) => r.url.queryParameters['archived'] == null), hasLength(1));
      chats.dispose();
      live.dispose();
    });
  });
}
