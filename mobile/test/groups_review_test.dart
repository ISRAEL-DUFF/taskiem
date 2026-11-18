// Review fixes for groups in the app (docs/spec/people-chatting.md, "As
// built: stage 5b, groups (app)", "Review fixes").

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart' show GroupFound, PeopleFound;
import 'package:taskiem/chat/group_chat.dart';
import 'package:taskiem/chat/group_info.dart';
import 'package:taskiem/chat/group_new.dart' show GroupInvitesSection;
import 'package:taskiem/chat/people_live.dart';
import 'package:taskiem/chat/people_search.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'groups_test.dart' show gm, grp, groupApi, hearLive, openGroup;
import 'people_test.dart';
import 'support.dart' show loadFonts;

PeopleFound pf(String id, String at) => PeopleFound.fromJson({
  'chat_id': 'pc1',
  'chat': peopleChat(),
  'message': {'id': id, 'mine': false, 'kind': 'text', 'body': 'cake', 'read_at': null, 'created_at': at},
});

GroupFound gf(String id, String at) => GroupFound.fromJson({'group_id': 'g1', 'group': grp(), 'message': gm(id, at: at)});

void main() {
  setUpAll(loadFonts);

  test('@ mentions: the picked member’s id kept with their name; whole words only', () {
    final picked = [(name: 'Ada', id: 'p1'), (name: 'Ada', id: 'p7')];
    expect(mentionedIn('Hi @Ada', picked), ['p1', 'p7'], reason: 'both picked, neither overwritten');
    expect(mentionedIn('Hi @Adamu', picked), isEmpty, reason: '"@Ada" is not inside "@Adamu"');
    expect(mentionedIn('Hi @Ada, and @Ada.', [(name: 'Ada', id: 'p1')]), ['p1']);
  });

  testWidgets('a send that fails gives back its words and its @ picks', (t) async {
    phone(t);
    final api = groupApi(messages: []);
    var fail = true;
    api.routes['POST /v1/me/groups/g1/messages'] = (r) => fail ? answer({'error': 'Try again.'}, 400) : answer(gm('s1', mine: true, body: api.body(r)['text'] as String), 201);
    await openGroup(t, api);
    await typeIn(t, find.bySemanticsLabel('Message Book club'), 'Hi @Ad');
    await settle(t);
    await press(t, find.bySemanticsLabel(RegExp('^Mention Ada Obi')));
    await press(t, find.bySemanticsLabel('Send').last);
    final box = t.widget<EditableText>(find.descendant(of: find.bySemanticsLabel('Message Book club'), matching: find.byType(EditableText)));
    expect(box.controller.text, 'Hi @Ada Obi');
    fail = false;
    await press(t, find.bySemanticsLabel('Send').last);
    expect(api.body(api.sent('POST', '/v1/me/groups/g1/messages').last)['mentions'], ['p1']);
    await drain(t);
  });

  testWidgets('a group cleared on another device is emptied, not gone; a new message brings it back', (t) async {
    final api = groupApi();
    final live = PeopleLive(api.auth().transport);
    final d = GroupChatData(api.auth(), 'g1', follow: false, now: () => at, live: live);
    await t.runAsync(d.open);
    expect(d.messages, isNotEmpty);
    hearLive(live, 'ready', {'connection': 'c1'});
    hearLive(live, 'group', {'id': 'g1', 'hidden': true});
    await t.runAsync(pumpEventQueue);
    expect(d.missing, isFalse);
    expect(d.messages, isEmpty);
    // Left, then cleared: gone until the group comes again.
    api.routes['GET /v1/me/groups/g1'] = (_) => grp(left: true, canPost: false, role: 'member');
    await t.runAsync(d.refresh);
    hearLive(live, 'group', {'id': 'g1', 'hidden': true});
    await t.runAsync(pumpEventQueue);
    expect(d.missing, isTrue);
    api.routes['GET /v1/me/groups/g1/messages'] = (_) => {'messages': [gm('m9', body: 'New one')], 'more': false};
    hearLive(live, 'group', {'id': 'g1', 'hidden': false, 'group': grp()});
    await t.runAsync(() => pumpEventQueue(times: 50));
    expect(d.missing, isFalse);
    expect(d.messages?.map((l) => l.id), ['m9']);
    d.dispose();
    live.dispose();
  });

  testWidgets('an invite taken back or answered elsewhere leaves Requests: live, and on a 404 from Accept', (t) async {
    phone(t);
    final api = Api();
    Map<String, dynamic> inv(String id, String name) => {'group_id': id, 'name': name, 'photo': null, 'invited_by': 'p1', 'by': 'Ada Obi', 'at': '2026-11-17T09:00:00Z'};
    var invites = [inv('g1', 'Book club'), inv('g2', 'Choir')];
    api.routes['GET /v1/me/groups/invites'] = (_) => {'invites': invites};
    api.routes['POST /v1/me/groups/invites/g2/accept'] = (_) {
      invites = [];
      return answer({'error': 'That invite is no longer there.'}, 404);
    };
    final live = PeopleLive(api.auth().transport);
    final opened = <String>[];
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: SingleChildScrollView(child: GroupInvitesSection(auth: api.auth(), live: live, openGroup: (id, {focus}) async => opened.add(id), now: () => at)),
    ));
    await settle(t);
    expect(find.text('Book club'), findsOneWidget);
    hearLive(live, 'ready', {'connection': 'c1'});
    invites = [inv('g2', 'Choir')];
    hearLive(live, 'group_invite', {'group_id': 'g1', 'invite': null});
    await t.runAsync(pumpEventQueue);
    await t.pump();
    expect(find.text('Book club'), findsNothing, reason: 'out at once');
    await settle(t);
    expect(find.text('Choir'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Accept the invite to Choir'));
    await settle(t);
    expect(find.text('Choir'), findsNothing, reason: 'read again on the 404');
    expect(opened, isEmpty);
    live.dispose();
    await drain(t);
  });

  testWidgets('a former member can still report the group', (t) async {
    phone(t, 2400);
    final api = groupApi(g: grp(left: true, leftHow: 'left', canPost: false, role: 'member'));
    final d = GroupChatData(api.auth(), 'g1', follow: false, now: () => at);
    await t.runAsync(d.open);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupInfoScreen(data: d)));
    await settle(t);
    expect(find.text('Report this group'), findsOneWidget);
    expect(find.text('Leave this group'), findsNothing);
    await drain(t);
  });

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
