// The chat as it happens (lib/chat/live.dart): server-sent events read as
// EventSource reads them, a dropped stream opened again from where it was
// (Last-Event-ID), and a conversation taking messages, deletions and
// typing from it.

import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/live.dart';

import 'support.dart';

const _link = '3af349e2-b776-4306-bdb8-10df59687c55';

String _msg(String id, {bool mine = false, String body = 'Hello', String at = '2026-11-11T12:20:00Z'}) => jsonEncode({
      'message': {'id': id, 'link': _link, 'mine': mine, 'kind': 'text', 'body': body, 'created_at': at},
    });

/// A stand-in stream: each open is a controller the test writes to.
class _Streams {
  final opens = <StreamController<List<int>>>[];
  final lastIds = <String?>[];

  http.Client client() => MockClient.streaming((req, _) async {
        lastIds.add(req.headers['Last-Event-ID']);
        final c = StreamController<List<int>>();
        opens.add(c);
        return http.StreamedResponse(c.stream, 200, headers: {'content-type': 'text/event-stream'});
      });

  void say(String text) => opens.last.add(utf8.encode(text));
}

void main() {
  test('server-sent events: event, data on several lines, id, comments', () async {
    final body = Stream.fromIterable([
      utf8.encode(': hello\n\nevent: ready\ndata: {}\nid: 1\n\n'),
      utf8.encode('event: typing\ndata: {"typing":\ndata: ["a"]}\nid: 2\n\ndata: plain\n\n'),
    ]);
    final events = await readSse(body).toList();
    expect(events.map((e) => e.event), ['ready', 'typing', 'message']);
    expect(events[1].data, '{"typing":\n["a"]}');
    expect(events[1].id, '2');
    expect(events[2].data, 'plain');
  });

  test('connected on ready; typing; a dropped stream is opened again from its last event', () async {
    final s = _Streams();
    final live = ChatLive(Transport(base: 'http://localhost', token: 't', client: s.client()),
        timing: const LiveTiming(retry: Duration(milliseconds: 10)));
    final got = <LiveEvent>[];
    live.events.listen(got.add);
    live.start();
    await pumpEventQueue();
    expect(s.opens.length, 1);
    expect(s.lastIds.single, isNull);
    s.say('event: ready\ndata: {}\nid: 100\n\n');
    s.say('event: typing\ndata: {"typing":["$_link"]}\nid: 101\n\n');
    s.say('event: message\ndata: ${_msg('m1')}\nid: 102\n\n');
    await pumpEventQueue();
    expect(live.connected, isTrue);
    expect(live.typing, {_link});
    expect((got.single as LiveMessage).message.id, 'm1');

    await s.opens.last.close();
    await pumpEventQueue();
    expect(live.connected, isFalse);
    expect(live.typing, isEmpty);
    await Future<void>.delayed(const Duration(milliseconds: 30));
    expect(s.opens.length, 2);
    expect(s.lastIds.last, '102', reason: 'what was missed is sent, nothing twice');
    live.dispose();
  });

  test('a quiet stream counts as stalled and is opened again', () async {
    final s = _Streams();
    var now = DateTime.utc(2026, 11, 11, 12);
    final live = ChatLive(Transport(base: 'http://localhost', token: 't', client: s.client()),
        timing: const LiveTiming(stall: Duration(seconds: 30), check: Duration(milliseconds: 5)), clock: () => now);
    live.start();
    await pumpEventQueue();
    s.say('event: ready\ndata: {}\n\n');
    await pumpEventQueue();
    expect(s.opens.length, 1);
    now = now.add(const Duration(seconds: 31));
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(s.opens.length, greaterThanOrEqualTo(2));
    live.dispose();
  });

  test('a conversation takes messages, deletions and typing from the stream, and reads what came', () async {
    final s = _Streams();
    final f = Fixture('chat-tunde');
    var read = 0;
    final auth = f.auth(also: (req) async {
      if (req.method == 'POST' && req.url.path.endsWith('/read')) {
        read++;
        return http.Response(jsonEncode({'read': 1}), 200);
      }
      return null;
    });
    final live = ChatLive(Transport(base: 'http://localhost', token: 't', client: s.client()));
    final chats = ChatsData(auth, now: () => f.at, live: live);
    final conv = ListLinkChatsResponse.fromJson(f.json('/v1/link-chats')).conversations.firstWhere((c) => c.link == _link);
    final d = ConversationData(chats, conv);
    await d.open();
    final before = d.messages!.length;
    final readAtOpen = read;
    live.start();
    await pumpEventQueue();
    s.say('event: ready\ndata: {}\n\n');
    s.say('event: typing\ndata: {"typing":["$_link"]}\n\n');
    await pumpEventQueue();
    expect(d.typingNow, isTrue);
    s.say('event: message\ndata: ${_msg('m-live', body: 'On my way')}\n\n');
    await pumpEventQueue();
    expect(d.messages!.length, before + 1);
    expect(d.messages!.last.body, 'On my way');
    await Future<void>.delayed(const Duration(milliseconds: 450));
    expect(read, readAtOpen + 1, reason: 'read once it came, while open');
    s.say('event: gone\ndata: {"id":"m-live","link":"$_link"}\n\n');
    await pumpEventQueue();
    expect(d.messages!.length, before);
    d.dispose();
    chats.dispose();
    live.dispose();
  });
}
