// Review fixes for groups in the app (docs/spec/people-chatting.md, "As
// built: stage 5b, groups (app)", "Review fixes").

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart' show GroupFound, PeopleFound;
import 'package:taskiem/chat/people_search.dart';

import 'groups_test.dart' show gm, grp;
import 'people_test.dart' show peopleChat;

PeopleFound pf(String id, String at) => PeopleFound.fromJson({
  'chat_id': 'pc1',
  'chat': peopleChat(),
  'message': {'id': id, 'mine': false, 'kind': 'text', 'body': 'cake', 'read_at': null, 'created_at': at},
});

GroupFound gf(String id, String at) => GroupFound.fromJson({'group_id': 'g1', 'group': grp(), 'message': gm(id, at: at)});

void main() {
  test('Show more across people chats and groups: rows below a source’s frontier are held back, the list kept in time order', () async {
    // People chats: 10:00, 08:00 | more | 06:00. Groups: 09:00, 07:00, 05:00 (all at once).
    final merge = FoundMerge(
      people: (b) async => b == null
          ? (found: [pf('p10', '2026-11-17T10:00:00Z'), pf('p8', '2026-11-17T08:00:00Z')], more: true, next: 'c1')
          : (found: [pf('p6', '2026-11-17T06:00:00Z')], more: false, next: null),
      groups: (b) async => (found: [gf('g9', '2026-11-17T09:00:00Z'), gf('g7', '2026-11-17T07:00:00Z'), gf('g5', '2026-11-17T05:00:00Z')], more: false, next: null),
    );
    final list = FoundList(merge.read);
    await list.load();
    expect(list.found!.map((f) => f.messageId), ['p10', 'g9', 'p8'], reason: 'groups older than the people chats’ oldest are held back');
    expect(list.more, isTrue);
    await list.loadMore();
    expect(list.found!.map((f) => f.messageId), ['p10', 'g9', 'p8', 'g7', 'p6', 'g5']);
    expect(list.more, isFalse);
  });
}
