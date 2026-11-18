// The second stage 5a review's app findings (docs/spec/people-chatting.md,
// "As built: stage 5a gaps, app", "Second review fixes (app)"): a live
// change to an old message no longer breaks Load older, the camera refused
// is said plainly everywhere, the live connection reconnects at once when
// the server ends it cleanly and forgets presence when it closes, the daily
// contacts re-check goes round the numbers, it can be turned off, covered
// screens read once when back on top, search pages its own query, typing
// stops when it should, and the standing notice reads again.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/people_chat.dart';
import 'package:taskiem/chat/people_live.dart';

import 'people_test.dart';
import 'support.dart' show loadFonts;

Map<String, dynamic> msg(String id, {bool mine = false, String body = 'Hello', String at = '2026-11-17T10:25:00Z', Map<String, dynamic> extra = const {}}) => {
  'id': id, 'mine': mine, 'kind': 'text', 'body': body, 'read_at': null, 'created_at': at, if (mine) 'status': 'sent', ...extra,
};

void hear(PeopleLive live, String event, Object data, {String? id}) => live.hearEvent((event: event, data: jsonEncode(data), id: id));

void main() {
  setUpAll(loadFonts);

  group('1. a live change to an old message', () {
    test('is not merged when older than what is loaded; Load older pages from the oldest loaded; new and loaded ones still merge', () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      final api = Api();
      api.routes['GET /v1/me/people/pc1'] = (_) => peopleChat();
      api.routes['POST /v1/me/people/pc1/read'] = (_) => answer({}, 204);
      api.routes['GET /v1/me/people/pc1/messages'] = (r) {
        if (r.url.queryParameters['before'] == 'm1') return {'messages': [msg('m0', at: '2026-11-17T08:00:00Z')], 'more': true};
        return {'messages': thread(), 'more': true};
      };
      final live = PeopleLive(api.auth().transport);
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, live: live);
      d.seen = () => false;
      // Heard before the first page: an old one is dropped, a new one kept.
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('ancient', at: '2026-11-01T08:00:00Z')});
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('early', at: '2026-11-17T10:26:00Z')});
      await pumpEventQueue();
      await d.open();
      expect(d.messages!.map((m) => m.id), ['m1', 'm2', 'm3', 'm4', 'm5', 'early']);

      // A reaction on a message far back (a replay after a reconnect too).
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('old7', at: '2026-11-10T08:00:00Z', extra: {'their_reaction': '👍'})});
      await pumpEventQueue();
      expect(d.messages!.any((m) => m.id == 'old7'), isFalse);
      // One loaded changes in place; one new comes in.
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': {...thread()[1], 'their_reaction': '👍'}});
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('m9', at: '2026-11-17T10:40:00Z')});
      await pumpEventQueue();
      expect(d.messages!.firstWhere((m) => m.id == 'm2').m.theirReaction, '👍');
      expect(d.messages!.last.id, 'm9');

      await d.refresh();
      expect(d.messages!.any((m) => m.id == 'old7'), isFalse, reason: 'not kept by a read of the latest page');
      await d.older();
      final befores = [for (final r in api.sent('GET', '/v1/me/people/pc1/messages')) r.url.queryParameters['before']].whereType<String>();
      expect(befores, ['m1'], reason: 'paged from the oldest loaded, not the old one heard live');
      d.dispose();
      live.dispose();
    });

    test('with no older pages, any message heard is merged (the loaded ones are the whole chat)', () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      final api = adaChat();
      final live = PeopleLive(api.auth().transport);
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, live: live);
      d.seen = () => false;
      await d.open();
      hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('m0', at: '2026-11-17T08:00:00Z')});
      await pumpEventQueue();
      expect(d.messages!.first.id, 'm0');
      d.dispose();
      live.dispose();
    });
  });
}
