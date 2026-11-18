// Stage 5a's gaps in the app (docs/spec/people-chatting.md, "As built:
// stage 5a gaps, app"): You's live connection (its events read, reconnected
// from the last event, a chat, the list and Requests updated from it, and
// presence said on it), typing throttled and shown, search and starred
// messages opening a chat at the message, the trust line on a business's
// row, and the rest of the gaps (in this file's later groups).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/config.dart' show taskiemUrl;
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/presence.dart';
import 'package:taskiem/app/session.dart' show MemoryKeyStore;
import 'package:taskiem/chat/my_chat.dart';
import 'package:taskiem/chat/people_chat.dart';
import 'package:taskiem/chat/people_find.dart';
import 'package:taskiem/chat/people_contacts.dart';
import 'package:taskiem/chat/people_live.dart';
import 'package:taskiem/chat/people_scan.dart';
import 'package:taskiem/chat/people_place.dart';
import 'package:taskiem/chat/people_search.dart';
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

  group('search and starred', () {
    Map<String, dynamic> found(String id, {String chat = 'pc1', String name = 'Ada Obi', String body = 'Bring the cake', String? snippet, bool mine = false}) => {
      'chat_id': chat,
      'chat': peopleChat(id: chat, name: name),
      'message': _m(id, body: body, mine: mine),
      'snippet': ?snippet,
    };

    testWidgets('search: typed words searched once they rest, a page at a time; a hit opens its chat at the message', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/people/search'] = (r) => r.url.queryParameters['before'] == null
          ? {'results': [found('m5', snippet: '…Bring the cake at six'), found('x2', chat: 'pc2', name: 'Bola', body: 'cake shop?', mine: true)], 'more': true, 'next': 'n1'}
          : {'results': [found('x1', chat: 'pc2', name: 'Bola', body: 'Cakes!')], 'more': false};
      final opened = <(String, String)>[];
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleSearchScreen(auth: api.auth(), now: () => at, openAt: (c, m) async => opened.add((c, m)))));
      await settle(t);
      expect(find.text('Starred messages'), findsOneWidget, reason: 'with nothing typed');
      await typeIn(t, find.byKey(const ValueKey('people-search')), 'c');
      await t.pump(const Duration(milliseconds: 500));
      expect(api.sent('GET', '/v1/me/people/search'), isEmpty, reason: 'one letter is not searched');
      expect(find.text('Type a word of two letters or more.'), findsOneWidget);
      await typeIn(t, find.byKey(const ValueKey('people-search')), 'ca');
      await typeIn(t, find.byKey(const ValueKey('people-search')), 'cake');
      await t.pump(const Duration(milliseconds: 500));
      await settle(t);
      expect(api.sent('GET', '/v1/me/people/search').where((r) => r.url.queryParameters['before'] == null).map((r) => r.url.queryParameters['q']), ['cake'], reason: 'once, when typing rests');
      expect(find.text('You: …Bring the cake at six'.replaceFirst('You', 'Ada Obi')), findsOneWidget, reason: 'the snippet, with who wrote it');
      expect(find.text('You: cake shop?'), findsOneWidget);
      // The next page as the list's end comes into view.
      await settle(t);
      expect(api.sent('GET', '/v1/me/people/search').last.url.queryParameters['before'], 'n1');
      expect(find.text('Bola: Cakes!'), findsOneWidget);
      await press(t, find.text('Ada Obi'));
      expect(opened, [('pc1', 'm5')]);
      await drain(t);
    });

    testWidgets('search: the server’s refusal said (too fast)', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/people/search'] = (_) => answer({'error': 'You are searching too fast. Wait a moment.'}, 429);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleSearchScreen(auth: api.auth(), now: () => at, wait: Duration.zero)));
      await typeIn(t, find.byKey(const ValueKey('people-search')), 'cake');
      await settle(t);
      expect(find.text('You are searching too fast. Wait a moment.'), findsOneWidget);
      await drain(t);
    });

    testWidgets('starred: across chats, each opening its chat at the message', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/people/starred'] = (_) => {
        'starred': [
          {...found('m2', body: 'Yes, see you at 6'), 'starred_at': '2026-11-17T09:30:00Z'},
          {...found('x1', chat: 'pc2', name: 'Bola', body: 'My account is 0123'), 'starred_at': '2026-11-16T09:30:00Z'},
        ],
        'more': false,
      };
      final opened = <(String, String)>[];
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleStarredScreen(auth: api.auth(), now: () => at, openAt: (c, m) async => opened.add((c, m)))));
      await settle(t);
      expect(find.text('Ada Obi: Yes, see you at 6'), findsOneWidget);
      await press(t, find.text('Bola'));
      expect(opened, [('pc2', 'x1')]);
      expect(api.sent('GET', '/v1/me/people/starred'), hasLength(2), reason: 'read again after, an unstarred one gone');
      await drain(t);
    });

    testWidgets('a chat opened at a message: older pages read until it is there, and it is shown', (t) async {
      phone(t);
      final api = adaChat();
      api.routes['GET /v1/me/people/pc1/messages'] = (r) => switch (r.url.queryParameters['before']) {
        null => {'messages': thread().skip(3).toList(), 'more': true},
        'm4' => {'messages': thread().skip(1).take(2).toList(), 'more': true},
        _ => {'messages': [
          {'id': 'm0', 'mine': false, 'kind': 'text', 'body': 'The very first one', 'read_at': null, 'created_at': '2026-11-16T09:00:00Z'},
          thread().first,
        ], 'more': false},
      };
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, focus: 'm0');
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d)));
      await settle(t);
      expect(api.sent('GET', '/v1/me/people/pc1/messages').map((r) => r.url.queryParameters['before']), [null, 'm4', 'm2']);
      expect(d.focusFound, isTrue);
      expect(find.text('The very first one'), findsOneWidget);
      expect(t.getRect(find.text('The very first one')).top, greaterThan(0), reason: 'placed in view');
      expect(t.getRect(find.text('The very first one')).bottom, lessThan(1600));
      await drain(t);
    });

    testWidgets('search this chat, from its menu: a hit jumps there', (t) async {
      phone(t);
      final api = adaChat();
      api.routes['GET /v1/me/people/search'] = (r) {
        expect(r.url.queryParameters['chat'], 'pc1');
        return {'results': [found('m2', body: 'Yes, see you at 6', snippet: 'Yes, see you at 6')], 'more': false};
      };
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d)));
      await settle(t);
      await press(t, find.bySemanticsLabel('More about this conversation'));
      await press(t, find.text('Search this chat'));
      await typeIn(t, find.byKey(const ValueKey('people-search')), 'see you');
      await t.pump(const Duration(milliseconds: 500));
      await settle(t);
      final before = d.focusRound;
      await press(t, find.text('Ada Obi'));
      expect(find.byType(PeopleSearchScreen), findsNothing);
      expect(d.focus, 'm2');
      expect(d.focusRound, before + 1, reason: 'the thread placed again at it');
      await drain(t);
    });
  });

  group('a place', () {
    test('opens in the map app: geo: on Android, Apple Maps on an iPhone', () {
      const l = PeopleLocation(lat: 6.5244, lng: 3.3792, label: 'Our gate');
      expect(mapUriOf(l, platform: TargetPlatform.android).toString(), 'geo:6.5244,3.3792?q=6.5244%2C3.3792(Our%20gate)');
      expect(mapUriOf(const PeopleLocation(lat: 6.5244, lng: 3.3792), platform: TargetPlatform.android).toString(), 'geo:6.5244,3.3792?q=6.5244%2C3.3792');
      final apple = mapUriOf(l, platform: TargetPlatform.iOS);
      expect(apple.host, 'maps.apple.com');
      expect(apple.queryParameters, {'ll': '6.5244,3.3792', 'q': 'Our gate'});
      expect(webMapOf(l).queryParameters['query'], '6.5244,3.3792');
    });

    Future<void> openPlaceSheet(WidgetTester t) async {
      await press(t, find.bySemanticsLabel('Chat tools'));
      await press(t, find.text('A place'));
    }

    testWidgets('refused, then refused for good (the settings offered): plain words, nothing sent; allowed: shown first, named, sent, opened in maps', (t) async {
      phone(t);
      final api = adaChat();
      Map<String, dynamic>? sent;
      api.routes['POST /v1/me/people/pc1/messages'] = (r) {
        sent = api.body(r);
        return {'id': 'm9', 'mine': true, 'kind': 'location', 'body': 'Our gate', 'location': sent!['location'], 'status': 'sent', 'read_at': null, 'created_at': '2026-11-17T10:29:00Z'};
      };
      final finder = FakePlaces()..problem = PlaceProblem.refused;
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d, places: finder)));
      await settle(t);
      await openPlaceSheet(t);
      expect(finder.asked, 1, reason: 'asked only on tap');
      expect(find.text(placeProblemWords(PlaceProblem.refused)), findsOneWidget);
      finder.problem = PlaceProblem.refusedForever;
      await press(t, find.text('Try again'));
      expect(find.text(placeProblemWords(PlaceProblem.refusedForever)), findsOneWidget);
      await press(t, find.text('Open your phone’s settings'));
      expect(finder.settings, [PlaceProblem.refusedForever]);
      finder.problem = null;
      await press(t, find.text('Try again'));
      expect(find.text('Where you are now'), findsOneWidget, reason: 'seen before it goes');
      expect(find.text('6.52440, 3.37920'), findsOneWidget);
      expect(find.text('To within about 12 m'), findsOneWidget);
      expect(sent, isNull);
      await typeIn(t, find.byKey(const ValueKey('place-label')), 'Our gate');
      await press(t, find.text('Send this place'));
      expect(sent?['location'], {'lat': 6.5244, 'lng': 3.3792, 'label': 'Our gate'});
      expect(sent?['client_id'], isNotNull);
      expect(find.bySemanticsLabel('Our gate, open it in your map app'), findsOneWidget);

      final opened = <PeopleLocation>[];
      final was = openPlace;
      openPlace = (l) async {
        opened.add(l);
        return true;
      };
      addTearDown(() => openPlace = was);
      await press(t, find.bySemanticsLabel('Our gate, open it in your map app'));
      expect(opened.single.label, 'Our gate');
      await drain(t);
    });
  });

  group('a person’s QR code, scanned in the app', () {
    test('only a person’s Taskiem link is taken', () {
      expect(scannedPersonCode('$taskiemUrl/p/k7m2q9x4pz'), 'k7m2q9x4pz');
      expect(scannedPersonCode(' $taskiemUrl/p/k7m2q9x4pz '), 'k7m2q9x4pz');
      expect(scannedPersonCode('https://example.com/p/k7m2q9x4pz'), isNull);
      expect(scannedPersonCode('WIFI:S:home;;'), isNull);
      expect(scannedPersonCode(null), isNull);
    });

    testWidgets('New chat: Scan their QR code opens who it is; put away, nothing; Paste opens a copied link', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/people/links/k7m2q9x4pz'] = (_) => {'found': true, 'person': card(inContacts: false), 'add_token': 'tok'};
      final scanner = FakeScanner();
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: NewPeopleChatScreen(auth: api.auth(), openChat: (id, {started}) async {}, contacts: FakeContacts(const []), scanner: scanner)));
      await settle(t);
      expect(scanner.asked, 0, reason: 'the camera is asked for only on tap');
      scanner.code = null;
      await press(t, find.text('Scan their QR code'));
      expect(scanner.asked, 1);
      expect(find.text('Chat on Taskiem'), findsNothing, reason: 'put away: nothing opens');
      scanner.code = 'k7m2q9x4pz';
      await press(t, find.text('Scan their QR code'));
      expect(find.text('Chat on Taskiem'), findsOneWidget);
      expect(find.text('Ada Obi'), findsOneWidget);
      t.state<NavigatorState>(find.byType(Navigator)).pop();
      await settle(t);

      t.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.getData') return {'text': '$taskiemUrl/p/k7m2q9x4pz'};
        return null;
      });
      addTearDown(() => t.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, null));
      await press(t, find.text('Paste'));
      expect(api.sent('GET', '/v1/people/links/k7m2q9x4pz'), hasLength(2));
      await drain(t);
    });
  });

  group('contacts checked again, at most once a day', () {
    final ada = contactHash(base64.decode(vectorKey), '+2348031234567');
    Api served() {
      final api = Api();
      api.routes['GET /v1/me/contacts/key'] = (_) => {'key_id': 'k1', 'key': vectorKey, 'expires_at': '2026-11-20T00:00:00Z', 'hash': 'sha256', 'max_batch': 500, 'max_per_day': 5000, 'words': ''};
      api.routes['POST /v1/me/contacts/match'] = (r) => {
        'matches': [
          if ((api.body(r)['hashes'] as List).contains(ada)) {'hash': ada, 'person': card(inContacts: false), 'add_token': 'tok-ada'},
        ],
      };
      return api;
    }

    test('never agreed, already today, or no longer allowed: not checked; else checked and the day kept', () async {
      final api = served();
      final c = GrantedContacts([(name: 'Ada', numbers: ['0803 123 4567'])]);
      final kept = <String>[];
      Future<List<ContactMatch>?> run(String? checked, {DateTime? now}) =>
          recheckContacts(api: api.auth().api, contacts: c, checked: checked, now: now ?? at, remember: (d) async => kept.add(d));
      expect(await run(null), isNull, reason: 'never agreed');
      expect(await run(contactsDay(at)), isNull, reason: 'already today');
      c.granted = false;
      expect(await run('2026-11-16'), isNull, reason: 'the phone no longer allows it');
      expect(c.read_, isFalse, reason: 'nothing read in any of those');
      expect(c.asked, isFalse, reason: 'the phone is never asked by the re-check');
      c.granted = true;
      final found = await run('2026-11-16');
      expect(found?.single.person.name, 'Ada Obi');
      expect(kept, ['2026-11-17']);
      expect(contactsDay(DateTime.utc(2026, 11, 17, 23, 30)), '2026-11-18', reason: 'Lagos’s day');
    });

    testWidgets('You opens: re-checked by itself, the people found offered; not again the same day', (t) async {
      phone(t);
      final api = served();
      api.routes['GET /v1/me/conversations'] = (_) => {'chats': <Object>[], 'unread': 0, 'requests': 0, 'more': false};
      api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
      final keys = MemoryKeyStore();
      await keys.write('contacts-checked', '2026-11-16');
      final prefs = Prefs(keys);
      await prefs.load();
      final c = GrantedContacts([(name: 'Ada', numbers: ['0803 123 4567'])]);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => at, presence: false, prefs: prefs, contacts: c)));
      await settle(t);
      expect(find.text('1 of your contacts is on Taskiem'), findsOneWidget);
      expect(await keys.read('contacts-checked'), '2026-11-17', reason: 'only the day is kept');
      await press(t, find.text('1 of your contacts is on Taskiem'));
      expect(find.text('On Taskiem as Ada Obi'), findsOneWidget, reason: 'the list already found, not checked again');
      expect(api.sent('POST', '/v1/me/contacts/match'), hasLength(1));
      await drain(t);

      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(key: const ValueKey('again'), auth: api.auth(), now: () => at, presence: false, prefs: prefs, contacts: c)));
      await settle(t);
      expect(api.sent('POST', '/v1/me/contacts/match'), hasLength(1), reason: 'not twice in a day');
      expect(find.text('1 of your contacts is on Taskiem'), findsNothing);
      await drain(t);
    });
  });

  testWidgets('delete for everyone says copies, forwards and screenshots made before stay; delete for me alone does not', (t) async {
    phone(t);
    final api = adaChat();
    await openPeople(t, api);
    await t.longPress(find.text('Bring the cake'));
    await settle(t);
    await press(t, find.text('Delete'));
    expect(find.text(deleteForEveryoneNote), findsOneWidget);
    expect(deleteForEveryoneNote, contains('screenshots they made before stay'));
    expect(find.text('Delete for everyone'), findsOneWidget);
    t.state<NavigatorState>(find.byType(Navigator)).pop();
    await settle(t);
    await t.longPress(find.text('Yes, see you at 6').last);
    await settle(t);
    await press(t, find.text('Delete'));
    expect(find.text('Delete for everyone'), findsNothing, reason: 'theirs: for me only');
    expect(find.text(deleteForEveryoneNote), findsNothing);
    await drain(t);
  });
}

/// A camera that reads [code] (null: put away).
class FakeScanner extends QrScanner {
  String? code = 'k7m2q9x4pz';
  int asked = 0;

  @override
  Future<String?> scan(BuildContext context) async {
    asked++;
    return code;
  }
}

/// The phone's contacts, allowed (or not) without asking.
class GrantedContacts extends FakeContacts {
  GrantedContacts(super.list);
  bool granted = true;

  @override
  Future<bool> allowed() async => granted;
}

/// The phone's location, as a test gives it.
class FakePlaces extends PlaceFinder {
  PlaceProblem? problem;
  int asked = 0;
  final settings = <PlaceProblem>[];

  @override
  Future<({double lat, double lng, double? accuracy})> here() async {
    asked++;
    final p = problem;
    if (p != null) throw PlaceRefused(p);
    return (lat: 6.5244, lng: 3.3792, accuracy: 12.0);
  }

  @override
  Future<void> openSettings(PlaceProblem problem) async => settings.add(problem);
}
