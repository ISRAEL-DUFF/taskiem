// People groups in the app (docs/spec/people-chatting.md, "As built: stage
// 5b, groups (app)"): groups in You's one Chats list and their invites in
// Requests; making a group with each add's answer said in the server's
// words alone; a group's chat (senders, events and Taskiem's, reactions
// counted, @mentions picked and picked out, "Read by", warnings, delete for
// everyone, forward to and from groups, the closed, left and admins-only
// notices) and its live updates and typing; its info (members, admins,
// settings, the link, reporting with consent, leaving); joining by a link
// (waiting, taken back, a link gone); links, the QR scanner and pushes
// routed; privacy's "Who can add me to groups" and the Groups switch;
// search and starred across groups; and the app's look kept.
//
// Every fake answer carries every field the server always sends.

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart' show GroupAdded;
import 'package:taskiem/app/applinks.dart';
import 'package:taskiem/app/config.dart' show taskiemUrl;
import 'package:taskiem/app/route.dart';
import 'package:taskiem/chat/group_chat.dart';
import 'package:taskiem/chat/group_info.dart';
import 'package:taskiem/chat/group_new.dart';
import 'package:taskiem/chat/groups.dart';
import 'package:taskiem/chat/my_chat.dart';
import 'package:taskiem/chat/people.dart' show strangerWarning;
import 'package:taskiem/chat/people_find.dart' show PeopleRequestsScreen;
import 'package:taskiem/chat/people_live.dart';
import 'package:taskiem/chat/people_scan.dart' show scannedCode;
import 'package:taskiem/chat/people_search.dart';
import 'package:taskiem/chat/people_settings.dart' show PeoplePrivacyPage;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you_account.dart' show YouNotificationsPage;
import 'package:taskiem/screens/you_links.dart' show openYouPage;
import 'package:taskiem/theme/theme.dart';

import 'people_test.dart';
import 'support.dart' show loadFonts;

/// A group as the server always sends it.
Map<String, dynamic> grp({
  String id = 'g1',
  String name = 'Book club',
  String role = 'admin',
  bool left = false,
  String? leftHow,
  bool canPost = true,
  bool adminsOnlyPost = false,
  bool canEdit = true,
  int unread = 0,
  bool mentioned = false,
  Map<String, dynamic>? closed,
  bool? linkStopped,
  bool approveJoins = false,
  String last = 'See you Friday',
  String? lastSender = 'Ada Obi',
  int members = 3,
}) => {
  'id': id,
  'kind': 'group',
  'name': name,
  'description': 'We read one book a month',
  'photo': null,
  'members': members,
  'role': role,
  'left': left,
  'left_how': ?leftHow,
  'admins_only_post': adminsOnlyPost,
  'admins_only_edit': true,
  'approve_joins': approveJoins,
  'can_post': canPost,
  'can_edit': canEdit,
  'muted': false,
  'archived': false,
  'mentions': true,
  'unread': unread,
  'mentioned': mentioned,
  'last': last,
  'last_mine': false,
  'last_sender': ?lastSender,
  'last_at': '2026-11-17T10:10:00Z',
  'joined_at': '2026-11-10T10:00:00Z',
  'closed': ?closed,
  'link_stopped': ?linkStopped,
  if (role == 'admin') 'join_requests': 0,
};

/// A group message as the server always sends it.
Map<String, dynamic> gm(
  String id, {
  bool mine = false,
  String body = 'Hello all',
  String sender = 'Ada Obi',
  String? senderId = 'p1',
  String kind = 'text',
  String at = '2026-11-17T10:20:00Z',
  Map<String, dynamic>? event,
  List<Map<String, dynamic>>? reactions,
  String? myReaction,
  bool? mentionsMe,
  List<String>? mentions,
  bool? warn,
  String? replyTo,
}) => {
  'id': id,
  'group_id': 'g1',
  'mine': mine,
  'sender_id': mine ? 'me' : senderId,
  'sender': mine ? 'You' : sender,
  'kind': kind,
  'body': body,
  'created_at': at,
  'event': ?event,
  'reactions': ?reactions,
  'my_reaction': ?myReaction,
  'mentions_me': ?mentionsMe,
  'mentions': ?mentions,
  'warn': ?warn,
  'reply_to': ?replyTo,
  if (mine) 'status': 'delivered',
};

Map<String, dynamic> member(String id, String name, {String role = 'member', bool you = false}) => {
  'person_id': id,
  'name': name,
  'role': role,
  'joined_at': '2026-11-10T10:00:00Z',
  'you': you,
  'person': card(id: id, name: name),
};

List<Map<String, dynamic>> groupThread() => [
  gm('e1', kind: 'event', body: 'Tolu Ade made the group', senderId: 'me', sender: 'Tolu Ade', at: '2026-11-17T09:00:00Z', event: {'type': 'created', 'actor_id': 'me'}),
  gm('m1', body: 'Hello all', at: '2026-11-17T09:10:00Z', reactions: [{'reaction': '👍', 'count': 2}], myReaction: '👍'),
  gm('m2', body: '@Tolu Ade are you coming?', sender: 'Bola Ige', senderId: 'p2', at: '2026-11-17T09:12:00Z', mentionsMe: true, mentions: ['me']),
  gm('m3', mine: true, body: 'Bring the book', at: '2026-11-17T10:20:00Z'),
  gm('m4', body: 'Send money to my account first', sender: 'Bola Ige', senderId: 'p2', at: '2026-11-17T10:21:00Z', warn: true),
  gm('e2', kind: 'event', body: 'Taskiem turned this group’s invite link off.', sender: 'Taskiem', senderId: null, at: '2026-11-17T10:22:00Z', event: {'type': 'taskiem_link_off', 'actor_id': '00000000-0000-0000-0000-000000000000'}),
];

/// A group, served.
Api groupApi({Map<String, dynamic>? g, List<Map<String, dynamic>>? messages}) {
  final api = Api();
  api.routes['GET /v1/me/groups/g1'] = (_) => g ?? grp();
  api.routes['GET /v1/me/groups/g1/messages'] = (_) => {'messages': messages ?? groupThread(), 'more': false};
  api.routes['GET /v1/me/groups/g1/members'] = (_) => {
    'members': [member('me', 'Tolu Ade', role: 'admin', you: true), member('p1', 'Ada Obi'), member('p2', 'Bola Ige')],
    'invited': [{'person_id': 'p9', 'name': 'Kemi', 'at': '2026-11-16T10:00:00Z'}],
    'waiting': [{'person_id': 'p8', 'name': 'Femi', 'at': '2026-11-17T08:00:00Z'}],
  };
  api.routes['POST /v1/me/groups/g1/read'] = (_) => answer({}, 204);
  return api;
}

Future<GroupChatData> openGroup(WidgetTester t, Api api, {PeopleLive? live}) async {
  final d = GroupChatData(api.auth(), 'g1', follow: false, now: () => at, live: live);
  await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupChatScreen(data: d)));
  await settle(t);
  return d;
}

void hearLive(PeopleLive live, String event, Object data) => live.hearEvent((event: event, data: jsonEncode(data), id: null));

void main() {
  setUpAll(loadFonts);

  group('the words', () {
    test('each add answered in the server’s words alone', () {
      expect(groupAddedWords('added'), 'Added');
      expect(groupAddedWords('invited'), 'Invited: they’ll see it in their Requests');
      expect(groupAddedWords('member'), 'Already in the group');
      expect(groupAddedWords('full'), 'The group is full');
      expect(groupAddedWords('unavailable'), 'Not on Taskiem');
      expect(
        groupAddedSummary([
          const GroupAdded(personId: 'a', state: 'added'),
          const GroupAdded(personId: 'b', state: 'invited'),
          const GroupAdded(personId: 'c', state: 'invited'),
          const GroupAdded(personId: 'd', state: 'unavailable'),
        ]),
        '1 added, 2 invited, 1 not on Taskiem',
      );
    });

    test('a group never said to be end-to-end encrypted', () {
      expect(groupKeptWords, contains('not end-to-end encrypted'));
      expect(groupKeptWords.replaceAll('not end-to-end encrypted', ''), isNot(contains('end-to-end')));
    });

    test('links, scanned codes and pages of You', () {
      expect(appPageOf(Uri.parse('$taskiemUrl/g/k7m2q9x4pz')), '/g/k7m2q9x4pz');
      expect(appPageOf(Uri.parse('$taskiemUrl/g/../x')), isNull);
      expect(groupLinkCodeOf('/g/k7m2q9x4pz'), 'k7m2q9x4pz');
      expect(groupLinkCodeOf('/p/k7m2q9x4pz'), isNull);
      expect(groupCodeOf('$taskiemUrl/g/k7m2q9x4pz'), 'k7m2q9x4pz');
      expect(groupCodeOf('https://example.com/g/k7m2q9x4pz'), isNull, reason: 'only this server’s links');
      expect(groupCodeOf('k7m2q9x4pz'), isNull, reason: 'a bare code is a person’s');
      expect(scannedCode('$taskiemUrl/g/k7m2q9x4pz'), '/g/k7m2q9x4pz');
      expect(scannedCode('$taskiemUrl/p/k7m2q9x4pz'), 'k7m2q9x4pz');
      expect(scannedCode('https://example.com/x'), isNull);
      expect(youRouteOf('/you/groups/g1')?.page, YouPage.group);
      expect(youRouteOf('/you/groups/g1')?.id, 'g1');
      expect(youRouteOf('/you/groups/invites')?.page, YouPage.groupInvites);
      expect(appPageOf(Uri.parse('$taskiemUrl/you/groups/g1')), '/you/groups/g1');
      expect(routeOf('/you/groups/g1').screen, RouteScreen.youPage);
    });

    test('Android opens group links and has the groups’ channels', () {
      final manifest = File('android/app/src/main/AndroidManifest.xml').readAsStringSync();
      expect(manifest, contains('android:pathPrefix="/g/"'));
      final kotlin = File('android/app/src/main/kotlin/app/taskiem/taskiem/TaskiemApplication.kt').readAsStringSync();
      expect(kotlin, contains('"group" to "Groups"'));
      expect(kotlin, contains('"group_invite" to "Invites to groups"'));
    });
  });

  group('the live connection', () {
    test('reads group messages, changes, receipts, invites and typing', () async {
      final live = PeopleLive(Api().auth().transport);
      final got = <GroupLiveEvent>[];
      live.groupEvents.listen(got.add);
      hearLive(live, 'ready', {'connection': 'c1'});
      hearLive(live, 'group_message', {'group_id': 'g1', 'message': gm('m9')});
      hearLive(live, 'group', {'id': 'g1', 'hidden': false, 'group': grp()});
      hearLive(live, 'group', {'id': 'g2', 'hidden': true});
      hearLive(live, 'group_receipts', {'group_id': 'g1', 'messages': [{'id': 'm3', 'status': 'read'}]});
      hearLive(live, 'group_invite', {'group_id': 'g3', 'invite': {'group_id': 'g3', 'name': 'Choir', 'photo': null, 'invited_by': 'p1', 'by': 'Ada Obi', 'at': '2026-11-17T10:00:00Z'}});
      hearLive(live, 'group_typing', {'groups': [{'group_id': 'g1', 'people': ['p1']}, {'group_id': 'g2', 'people': <String>[]}]});
      await pumpEventQueue();
      expect((got[0] as GroupLiveMessage).message.id, 'm9');
      expect((got[1] as GroupLiveGroup).group?.name, 'Book club');
      expect((got[2] as GroupLiveGroup).hidden, isTrue);
      expect((got[3] as GroupLiveReceipts).statuses, {'m3': 'read'});
      expect((got[4] as GroupLiveInvite).invite.by, 'Ada Obi');
      expect(live.groupTyping, {'g1': ['p1']});
      live.dispose();
    });
  });

  group('You’s one list and Requests', () {
    testWidgets('a group’s row: its name, Group, who wrote last, @ when mentioned, typing live; invites counted with requests', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/conversations'] = (r) => r.url.queryParameters['archived'] == '1'
          ? {'chats': <Object>[], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false}
          : {
              'chats': [
                {
                  'kind': 'group', 'id': 'g1', 'name': 'Book club', 'last': 'See you Friday', 'last_mine': false, 'last_at': '2026-11-17T10:10:00Z',
                  'unread': 2, 'muted': false, 'archived': false, 'blocked': false, 'group': grp(unread: 2, mentioned: true),
                },
              ],
              'unread': 2, 'requests': 1, 'invites': 2, 'more': false,
            };
      final auth = api.auth();
      final live = PeopleLive(auth.transport);
      final chats = MyChats(auth, live: live);
      await t.runAsync(chats.load);
      expect(chats.invites, 2);
      expect(chats.requests, 1);
      final opened = <String>[];
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: ListenableBuilder(
          listenable: live,
          builder: (_, _) => SingleChildScrollView(child: MyChatRows(chats: chats.chats!, now: at, transport: auth.transport, live: live, onOpen: (c) => opened.add(c.kind))),
        ),
      ));
      await settle(t);
      expect(find.text('Book club'), findsOneWidget);
      expect(find.text('Group'), findsOneWidget);
      expect(find.textContaining('Ada Obi: See you Friday'), findsOneWidget);
      expect(find.bySemanticsLabel(RegExp('A group, you were mentioned')), findsOneWidget);
      hearLive(live, 'ready', {'connection': 'c1'});
      hearLive(live, 'group_typing', {'groups': [{'group_id': 'g1', 'people': ['p1']}]});
      await settle(t);
      expect(find.text('typing…'), findsOneWidget);
      await press(t, find.text('Book club'));
      expect(opened, ['group']);
      chats.dispose();
      live.dispose();
      await drain(t);
    });

    testWidgets('invites in Requests: Accept opens the group; Decline asks first and tells the inviter nothing', (t) async {
      phone(t);
      final api = Api();
      var invites = [
        {'group_id': 'g1', 'name': 'Book club', 'photo': null, 'invited_by': 'p1', 'by': 'Ada Obi', 'at': '2026-11-17T09:00:00Z'},
        {'group_id': 'g2', 'name': 'Choir', 'photo': null, 'invited_by': 'p2', 'by': 'Bola Ige', 'at': '2026-11-17T09:00:00Z'},
      ];
      api.routes['GET /v1/me/people/requests'] = (_) => {'requests': <Object>[]};
      api.routes['GET /v1/me/groups/invites'] = (_) => {'invites': invites};
      api.routes['POST /v1/me/groups/invites/g1/accept'] = (_) {
        invites = invites.skip(1).toList();
        return grp();
      };
      api.routes['POST /v1/me/groups/invites/g2/decline'] = (_) {
        invites = [];
        return answer({}, 204);
      };
      final opened = <String>[];
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: PeopleRequestsScreen(auth: api.auth(), openChat: (_, {started}) async {}, openGroup: (id, {focus}) async => opened.add(id), now: () => at),
      ));
      await settle(t);
      expect(find.text('Invites to groups'), findsOneWidget);
      expect(find.textContaining('Ada Obi invited you'), findsOneWidget);
      await press(t, find.bySemanticsLabel('Accept the invite to Book club'));
      expect(opened, ['g1']);
      await press(t, find.bySemanticsLabel('Decline the invite to Choir'));
      expect(find.text('Bola Ige is not told.'), findsOneWidget);
      await press(t, find.text('Decline').last);
      expect(api.sent('POST', '/v1/me/groups/invites/g2/decline'), hasLength(1));
      expect(find.text('Choir'), findsNothing);
      await drain(t);
    });
  });

  group('a group', () {
    testWidgets('senders by name, events in the middle (Taskiem’s marked), reactions counted, a mention of you, the warning, ticks', (t) async {
      phone(t);
      final api = groupApi();
      await openGroup(t, api);
      expect(find.text('Book club'), findsOneWidget);
      expect(find.text('3 members'), findsOneWidget);
      expect(find.bySemanticsLabel('Search this group'), findsOneWidget, reason: 'Search as a round button in the head');
      expect(find.text('Tolu Ade made the group'), findsOneWidget);
      expect(find.bySemanticsLabel(RegExp('^From Taskiem: Taskiem turned')), findsOneWidget);
      expect(find.text('Hello all'), findsOneWidget);
      expect(find.textContaining('Ada Obi'), findsWidgets);
      expect(find.bySemanticsLabel(RegExp('^thumbs up, 2, yours')), findsOneWidget);
      expect(find.textContaining('mentioned you'), findsOneWidget);
      expect(find.bySemanticsLabel(RegExp('Message from Bola Ige, mentioning you')), findsWidgets);
      expect(find.text(strangerWarning), findsOneWidget);
      expect(find.bySemanticsLabel('Delivered'), findsOneWidget);
      expect(api.sent('POST', '/v1/me/groups/g1/read'), hasLength(1), reason: 'read as it is seen');
      await drain(t);
    });

    testWidgets('an @ picks a member, and the send names them', (t) async {
      phone(t);
      final api = groupApi(messages: []);
      api.routes['POST /v1/me/groups/g1/messages'] = (r) => answer(gm('s1', mine: true, body: api.body(r)['text'] as String), 201);
      await openGroup(t, api);
      await typeIn(t, find.bySemanticsLabel('Message Book club'), 'Hi @Ad');
      await settle(t);
      await press(t, find.bySemanticsLabel(RegExp('^Mention Ada Obi')));
      final box = t.widget<EditableText>(find.descendant(of: find.bySemanticsLabel('Message Book club'), matching: find.byType(EditableText)));
      expect(box.controller.text, 'Hi @Ada Obi ');
      await press(t, find.bySemanticsLabel('Send').last);
      final sent = api.body(api.sent('POST', '/v1/me/groups/g1/messages').single);
      expect(sent['mentions'], ['p1']);
      expect(sent['text'], 'Hi @Ada Obi');
      expect(sent['client_id'], isNotEmpty);
      await drain(t);
    });

    testWidgets('your message’s info: read by those with ticks on, delivered to the rest, who doesn’t have it', (t) async {
      phone(t);
      final api = groupApi();
      api.routes['GET /v1/me/groups/g1/messages/m3/info'] = (_) => {
        'read_by': [{'person_id': 'p1', 'name': 'Ada Obi', 'at': '2026-11-17T10:25:00Z'}],
        'delivered_to': [{'person_id': 'p2', 'name': 'Bola Ige', 'at': null}],
        'waiting': 1,
      };
      await openGroup(t, api);
      await t.longPress(find.text('Bring the book'));
      await settle(t);
      await press(t, find.text('Info'));
      expect(find.text('Read by'), findsOneWidget);
      expect(find.text('Delivered to'), findsOneWidget);
      expect(find.text('Bola Ige'), findsWidgets);
      expect(find.text('1 member doesn’t have it yet'), findsOneWidget);
      await drain(t);
    });

    testWidgets('delete for everyone within 24 hours, in the app’s own sheet', (t) async {
      phone(t);
      final api = groupApi();
      api.routes['DELETE /v1/me/groups/g1/messages/m3'] = (_) => answer({}, 204);
      await openGroup(t, api);
      await t.longPress(find.text('Bring the book'));
      await settle(t);
      await press(t, find.text('Delete'));
      expect(find.text('Delete for everyone'), findsOneWidget);
      await press(t, find.text('Delete for everyone'));
      expect(api.sent('DELETE', '/v1/me/groups/g1/messages/m3').single.url.queryParameters['for'], 'everyone');
      expect(find.text('This message was deleted'), findsOneWidget);
      await drain(t);
    });

    testWidgets('forward: people chats and groups in one picker, sent with /v1/me/forward', (t) async {
      phone(t);
      final api = groupApi();
      api.routes['GET /v1/me/conversations'] = (r) => {
        'chats': r.url.queryParameters['archived'] == '1'
            ? <Object>[]
            : [
                {...peopleChat(id: 'pc1', name: 'Ada Obi'), 'blocked': false},
                {'kind': 'group', 'id': 'g2', 'name': 'Choir', 'last': '', 'last_mine': false, 'last_at': '2026-11-17T10:10:00Z', 'unread': 0, 'muted': false, 'archived': false, 'blocked': false, 'group': grp(id: 'g2', name: 'Choir')},
                {'kind': 'group', 'id': 'g3', 'name': 'Old group', 'last': '', 'last_mine': false, 'last_at': '2026-11-17T10:10:00Z', 'unread': 0, 'muted': false, 'archived': false, 'blocked': false, 'group': grp(id: 'g3', name: 'Old group', left: true)},
              ],
        'unread': 0, 'requests': 0, 'invites': 0, 'more': false,
      };
      api.routes['POST /v1/me/forward'] = (_) => {'sent': [{'kind': 'person', 'id': 'pc1', 'message_id': 'x1'}, {'kind': 'group', 'id': 'g2', 'message_id': 'x2'}], 'failed': <Object>[]};
      await openGroup(t, api);
      await t.longPress(find.text('Hello all'));
      await settle(t);
      await press(t, find.text('Forward'));
      expect(find.text('Old group'), findsNothing, reason: 'not a group you left');
      await press(t, find.text('Ada Obi').last);
      await press(t, find.text('Choir'));
      await press(t, find.text('Send'));
      expect(api.body(api.sent('POST', '/v1/me/forward').single), {'group_id': 'g1', 'message_id': 'm1', 'to_chats': ['pc1'], 'to_groups': ['g2']});
      expect(find.text('Forwarded to 2 chats'), findsOneWidget);
      await drain(t);
    });

    testWidgets('live: a new message, its ticks, and who is typing', (t) async {
      phone(t);
      final api = groupApi();
      final auth = api.auth();
      final live = PeopleLive(auth.transport);
      final d = GroupChatData(auth, 'g1', follow: false, now: () => at, live: live);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupChatScreen(data: d)));
      await settle(t);
      hearLive(live, 'ready', {'connection': 'c1'});
      hearLive(live, 'group_message', {'group_id': 'g1', 'message': gm('m9', body: 'Running late', at: '2026-11-17T10:28:00Z')});
      hearLive(live, 'group_receipts', {'group_id': 'g1', 'messages': [{'id': 'm3', 'status': 'read'}]});
      hearLive(live, 'group_typing', {'groups': [{'group_id': 'g1', 'people': ['p2']}]});
      await settle(t);
      expect(find.text('Running late'), findsOneWidget);
      expect(find.bySemanticsLabel('Read'), findsOneWidget);
      expect(find.text('Bola Ige is typing…'), findsOneWidget);
      live.dispose();
      await drain(t);
    });

    testWidgets('closed by Taskiem: its words in the composer’s place, Appeal for an admin', (t) async {
      phone(t);
      final api = groupApi(g: grp(canPost: false, canEdit: false, closed: {'at': '2026-11-17T10:00:00Z', 'until': '2026-12-18T10:00:00Z', 'words': 'Taskiem’s moderators closed this group.', 'can_appeal': true, 'action_id': 'a1', 'reason': 'scam'}));
      api.routes['POST /v1/me/groups/g1/appeal'] = (_) => answer({'appeal': {'id': 'ap1', 'state': 'open', 'at': '2026-11-17T10:30:00Z'}, 'said': 'Your appeal is sent.'}, 201);
      await openGroup(t, api);
      expect(find.text('Taskiem’s moderators closed this group.'), findsOneWidget);
      expect(find.bySemanticsLabel('Message Book club'), findsNothing);
      await press(t, find.text('Appeal'));
      await t.enterText(find.byType(EditableText).last, 'We are a real book club');
      await t.pump();
      await press(t, find.text('Send appeal'));
      expect(api.body(api.sent('POST', '/v1/me/groups/g1/appeal').single), {'text': 'We are a real book club'});
      await drain(t);
    });

    testWidgets('left: read only, said so; only admins post: said so', (t) async {
      phone(t);
      await openGroup(t, groupApi(g: grp(left: true, leftHow: 'removed', canPost: false, role: 'member')));
      expect(find.text('You were removed from this group.'), findsOneWidget);
      expect(find.bySemanticsLabel('Message Book club'), findsNothing);
      await drain(t);
      await openGroup(t, groupApi(g: grp(canPost: false, adminsOnlyPost: true, role: 'member')));
      expect(find.text('Only admins can send messages in this group.'), findsOneWidget);
      await drain(t);
    });
  });

  group('group info', () {
    testWidgets('members with admins first; people waiting let in; settings; a member made admin', (t) async {
      phone(t, 2400);
      final api = groupApi();
      api.routes['POST /v1/me/groups/g1/joins/p8/approve'] = (_) => answer({}, 204);
      api.routes['PUT /v1/me/groups/g1'] = (r) => {...grp(), 'admins_only_post': api.body(r)['admins_only_post'] ?? false};
      api.routes['PUT /v1/me/groups/g1/members/p1'] = (r) => {'person_id': 'p1', 'admin': api.body(r)['admin']};
      final d = GroupChatData(api.auth(), 'g1', follow: false, now: () => at);
      await t.runAsync(d.open);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupInfoScreen(data: d)));
      await settle(t);
      expect(find.text('3 members'), findsOneWidget);
      expect(find.bySemanticsLabel(RegExp('^You, admin')), findsOneWidget);
      expect(find.text('Waiting to join'), findsOneWidget);
      expect(find.text('Invited'), findsOneWidget);
      expect(find.textContaining('not end-to-end encrypted'), findsOneWidget);
      await press(t, find.bySemanticsLabel('Let Femi in'));
      expect(api.sent('POST', '/v1/me/groups/g1/joins/p8/approve'), hasLength(1));
      await press(t, find.text('Who can send messages'));
      await press(t, find.text('Only admins').last);
      expect(api.body(api.sent('PUT', '/v1/me/groups/g1').single), {'admins_only_post': true});
      await press(t, find.text('Ada Obi'));
      await press(t, find.text('Make admin'));
      expect(api.body(api.sent('PUT', '/v1/me/groups/g1/members/p1').single), {'admin': true});
      await drain(t);
    });

    testWidgets('add people: each answer in the server’s words', (t) async {
      phone(t, 2400);
      final api = groupApi();
      api.routes['GET /v1/me/contacts'] = (_) => {
        'contacts': [
          {...card(id: 'p5', name: 'Chidi'), 'added_at': '2026-11-01T10:00:00Z', 'source': 'added'},
          {...card(id: 'p6', name: 'Dayo'), 'added_at': '2026-11-01T10:00:00Z', 'source': 'added'},
        ],
      };
      api.routes['GET /v1/me/people'] = (_) => {'chats': <Object>[], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false};
      api.routes['POST /v1/me/groups/g1/members'] = (_) => {'added': [{'person_id': 'p5', 'state': 'added'}, {'person_id': 'p6', 'state': 'invited'}]};
      final d = GroupChatData(api.auth(), 'g1', follow: false, now: () => at);
      await t.runAsync(d.open);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupInfoScreen(data: d)));
      await settle(t);
      await press(t, find.text('Add people'));
      await press(t, find.text('Chidi'));
      await press(t, find.text('Dayo'));
      await press(t, find.text('Add (2)'));
      expect(api.body(api.sent('POST', '/v1/me/groups/g1/members').single), {'person_ids': ['p5', 'p6']});
      expect(find.text('1 added, 1 invited'), findsOneWidget);
      expect(find.text('Invited: they’ll see it in their Requests'), findsOneWidget);
      await drain(t);
    });

    testWidgets('report: a reason and consent first; then the server’s words', (t) async {
      phone(t, 2400);
      final api = groupApi();
      api.routes['POST /v1/me/groups/g1/report'] = (_) => answer({'id': 'r1', 'messages_sent': true, 'said': 'Thanks. Taskiem’s moderators will look at it.'}, 201);
      final d = GroupChatData(api.auth(), 'g1', follow: false, now: () => at);
      await t.runAsync(d.open);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupInfoScreen(data: d)));
      await settle(t);
      await press(t, find.text('Report this group'));
      await press(t, find.text('Send report'));
      expect(find.text('Choose why you are reporting this group.'), findsOneWidget);
      await press(t, find.text('Spam'));
      await press(t, find.text('Send report'));
      expect(find.textContaining('Agree to send the last 20 messages'), findsOneWidget);
      await press(t, find.text(groupReportConsent));
      await press(t, find.text('Send report'));
      expect(api.body(api.sent('POST', '/v1/me/groups/g1/report').single), {'reason': 'spam', 'consent': true});
      expect(find.text('Thanks. Taskiem’s moderators will look at it.'), findsOneWidget);
      expect(find.text('Leave the group'), findsOneWidget);
      await drain(t);
    });

    testWidgets('the invite link: turned on, shown with its QR, replaced, turned off', (t) async {
      phone(t, 2000);
      final api = groupApi();
      var n = 0;
      Map<String, dynamic> link() => {'on': true, 'code': 'code$n', 'url': 'https://taskiem.app/g/code$n', 'created_at': '2026-11-17T10:00:00Z', 'words': 'Join my group on Taskiem.', 'qr': '/v1/me/groups/g1/link/qr'};
      api.routes['GET /v1/me/groups/g1/link'] = (_) => {'on': false};
      api.routes['POST /v1/me/groups/g1/link'] = (_) {
        n++;
        return answer(link(), 201);
      };
      api.routes['DELETE /v1/me/groups/g1/link'] = (_) => answer({}, 204);
      final d = GroupChatData(api.auth(), 'g1', follow: false, now: () => at);
      await t.runAsync(d.open);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupLinkScreen(data: d)));
      await settle(t);
      await press(t, find.text('Turn on the link'));
      expect(find.text('https://taskiem.app/g/code1'), findsOneWidget);
      expect(find.bySemanticsLabel('QR code for the group’s link'), findsOneWidget);
      await press(t, find.text('Replace the link'));
      await press(t, find.text('Replace'));
      expect(find.text('https://taskiem.app/g/code2'), findsOneWidget);
      await press(t, find.text('Turn the link off'));
      await press(t, find.text('Turn off'));
      expect(api.sent('DELETE', '/v1/me/groups/g1/link'), hasLength(1));
      expect(find.text('The link is off'), findsWidgets);
      await drain(t);
    });

    testWidgets('a link stopped by Taskiem: said so, no new one offered', (t) async {
      phone(t, 2000);
      final api = groupApi(g: grp(linkStopped: true));
      api.routes['GET /v1/me/groups/g1/link'] = (_) => {'on': false};
      final d = GroupChatData(api.auth(), 'g1', follow: false, now: () => at);
      await t.runAsync(d.open);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupLinkScreen(data: d)));
      await settle(t);
      expect(find.text('Taskiem’s moderators turned this group’s link off.'), findsOneWidget);
      expect(find.text('Turn on the link'), findsNothing);
      await drain(t);
    });

    testWidgets('leave: asked first; the last one out closes it', (t) async {
      phone(t, 2400);
      final api = groupApi();
      api.routes['POST /v1/me/groups/g1/leave'] = (_) => {'left': true, 'closed': true, 'group': null};
      final d = GroupChatData(api.auth(), 'g1', follow: false, now: () => at);
      await t.runAsync(d.open);
      var closed = false;
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupInfoScreen(data: d, onClosed: () => closed = true)));
      await settle(t);
      await press(t, find.text('Leave this group'));
      await press(t, find.text('Leave'));
      expect(api.sent('POST', '/v1/me/groups/g1/leave'), hasLength(1));
      expect(closed, isTrue);
      await drain(t);
    });
  });

  group('making and joining', () {
    testWidgets('a new group: its name, people from contacts; each add said; then it opens', (t) async {
      phone(t, 2000);
      final api = Api();
      api.routes['GET /v1/me/contacts'] = (_) => {
        'contacts': [{...card(id: 'p5', name: 'Chidi'), 'added_at': '2026-11-01T10:00:00Z', 'source': 'added'}],
      };
      api.routes['GET /v1/me/people'] = (_) => {'chats': [peopleChat(id: 'pc2', name: 'Ada Obi')], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false};
      api.routes['POST /v1/me/groups'] = (_) => answer({'group': grp(), 'added': [{'person_id': 'p5', 'state': 'added'}, {'person_id': 'p1', 'state': 'unavailable'}]}, 201);
      final opened = <String>[];
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: Builder(builder: (context) => NewGroupScreen(auth: api.auth(), openGroup: (id, {focus}) async => opened.add(id)))));
      await settle(t);
      await press(t, find.text('Make the group'));
      expect(find.text('Give the group a name.'), findsOneWidget);
      await typeIn(t, find.byKey(const ValueKey('group-name')), 'Book club');
      await press(t, find.text('Add people'));
      await press(t, find.text('Chidi'));
      await press(t, find.text('Ada Obi'));
      await press(t, find.text('Choose (2)'));
      await press(t, find.text('Make the group'));
      expect(api.body(api.sent('POST', '/v1/me/groups').single), {'name': 'Book club', 'person_ids': ['p5', 'p1']});
      expect(find.text('Not on Taskiem'), findsOneWidget);
      await press(t, find.text('Done'));
      expect(opened, ['g1']);
      await drain(t);
    });

    testWidgets('a group’s link: name, photo and count first; waiting for an admin; taken back', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/group-links/k7m2'] = (_) => {'code': 'k7m2', 'link': 'https://taskiem.app/g/k7m2', 'name': 'Book club', 'photo': null, 'members': 12, 'full': false};
      api.routes['POST /v1/me/group-links/k7m2/join'] = (_) => {'state': 'waiting'};
      api.routes['DELETE /v1/me/group-links/k7m2/join'] = (_) => answer({}, 204);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupJoinScreen(auth: api.auth(), code: 'k7m2', openGroup: (id, {focus}) async {})));
      await settle(t);
      expect(find.text('Book club'), findsOneWidget);
      expect(find.text('Group · 12 members'), findsOneWidget);
      await press(t, find.text('Join the group'));
      expect(find.text('Waiting for an admin to let you in'), findsOneWidget);
      await press(t, find.text('Take back my request'));
      expect(api.sent('DELETE', '/v1/me/group-links/k7m2/join'), hasLength(1));
      expect(find.text('Join the group'), findsOneWidget);
      await drain(t);
    });

    testWidgets('a link replaced or turned off: said in words', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/group-links/old1'] = (_) => answer({'error': 'gone'}, 410);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: GroupJoinScreen(auth: api.auth(), code: 'old1', openGroup: (id, {focus}) async {})));
      await settle(t);
      expect(find.text('This link was replaced or turned off. Ask someone in the group for the new one.'), findsOneWidget);
      await drain(t);
    });
  });

  group('pushes', () {
    testWidgets('kind group opens the group; kind group_invite opens Requests with the invites', (t) async {
      phone(t);
      final api = groupApi();
      api.routes['GET /v1/me/people/requests'] = (_) => {'requests': <Object>[]};
      api.routes['GET /v1/me/groups/invites'] = (_) => {'invites': [{'group_id': 'g2', 'name': 'Choir', 'photo': null, 'invited_by': 'p2', 'by': 'Bola Ige', 'at': '2026-11-17T09:00:00Z'}]};
      final auth = api.auth();
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: Builder(builder: (context) => Center(child: Text('home', key: const ValueKey('home'))))));
      final context = t.element(find.byKey(const ValueKey('home')));
      unawaited(openYouPage(context, auth, youRouteOf('/you/groups/g1')!, open: (id, {notice, draft = '', tool}) async {}, now: () => at));
      await settle(t);
      expect(find.byType(GroupChatScreen), findsOneWidget);
      Navigator.of(context).pop();
      await settle(t);
      unawaited(openYouPage(context, auth, youRouteOf('/you/groups/invites')!, open: (id, {notice, draft = '', tool}) async {}, now: () => at));
      await settle(t);
      expect(find.text('Choir'), findsOneWidget);
      await drain(t);
    });
  });

  group('settings, search and starred', () {
    testWidgets('privacy: who can add me to groups', (t) async {
      phone(t, 2400);
      final api = Api();
      final p = {'find_by_number': 'everyone', 'find_from_contacts': true, 'groups_add': 'contacts', 'message': 'everyone', 'last_seen': 'everyone', 'photo': 'everyone', 'show_number': false, 'read_ticks': true};
      api.routes['GET /v1/me/privacy/people'] = (_) => p;
      api.routes['PUT /v1/me/privacy/people'] = (r) => {...p, ...api.body(r)};
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeoplePrivacyPage(auth: api.auth())));
      await settle(t);
      expect(find.text('Who can add me to groups'), findsOneWidget);
      expect(find.text('People you haven’t added send you an invite instead'), findsOneWidget);
      await press(t, find.bySemanticsLabel('Who can add me to groups: My contacts. Change'));
      await press(t, find.text('Nobody'));
      expect(api.body(api.sent('PUT', '/v1/me/privacy/people').single), {'groups_add': 'nobody'});
      await drain(t);
    });

    testWidgets('notifications: the Groups switch', (t) async {
      phone(t, 2400);
      final api = Api();
      final n = {'messages': true, 'updates': true, 'offers': false, 'people': true, 'requests': true, 'groups': true};
      api.routes['GET /v1/me/notifications'] = (_) => n;
      api.routes['PUT /v1/me/notifications'] = (r) => {...n, ...api.body(r)};
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouNotificationsPage(auth: api.auth())));
      await settle(t);
      await press(t, find.bySemanticsLabel('Groups').last);
      expect(api.body(api.sent('PUT', '/v1/me/notifications').single), {'groups': false});
      await drain(t);
    });

    testWidgets('search and starred: people chats and groups merged by time; a group’s hit opens the group at it', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/people/search'] = (_) => {
        'results': [{'chat_id': 'pc1', 'chat': peopleChat(), 'message': {'id': 'x1', 'mine': false, 'kind': 'text', 'body': 'the cake recipe', 'read_at': null, 'created_at': '2026-11-17T08:00:00Z'}}],
        'more': false,
      };
      api.routes['GET /v1/me/groups/search'] = (_) => {
        'results': [{'group_id': 'g1', 'group': grp(), 'message': gm('m1', body: 'cake on Friday'), 'snippet': 'cake on Friday'}],
        'more': false,
      };
      api.routes['GET /v1/me/people/starred'] = (_) => {'starred': <Object>[], 'more': false};
      api.routes['GET /v1/me/groups/starred'] = (_) => {
        'starred': [{'group_id': 'g1', 'group': grp(), 'message': gm('m1', body: 'cake on Friday'), 'starred_at': '2026-11-17T10:00:00Z'}],
        'more': false,
      };
      final openedGroups = <(String, String)>[];
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: PeopleSearchScreen(auth: api.auth(), now: () => at, wait: Duration.zero, openAt: (c, m) async {}, openGroupAt: (g, m) async => openedGroups.add((g, m))),
      ));
      await typeIn(t, find.byKey(const ValueKey('people-search')), 'cake');
      await settle(t);
      expect(find.text('Ada Obi: cake on Friday'), findsOneWidget);
      expect(find.text('Ada Obi: the cake recipe'), findsOneWidget);
      expect(t.getRect(find.text('Ada Obi: cake on Friday')).top, lessThan(t.getRect(find.text('Ada Obi: the cake recipe')).top), reason: 'the newest first');
      await press(t, find.text('Book club'));
      expect(openedGroups, [('g1', 'm1')]);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleStarredScreen(auth: api.auth(), now: () => at, openAt: (c, m) async {}, openGroupAt: (g, m) async => openedGroups.add((g, m)))));
      await settle(t);
      expect(find.text('Ada Obi: cake on Friday'), findsOneWidget);
      await drain(t);
    });
  });

  group('the app’s own look', () {
    test('the group screens use no web parts, and every sheet is in the app’s look', () {
      for (final f in ['groups', 'group_chat', 'group_info', 'group_new']) {
        final src = File('lib/chat/$f.dart').readAsStringSync();
        for (final web in ['TkButton(', 'showTkModal', 'showTkModalChoices', 'showTkConfirm', 'TkRadio', 'TkChoiceRow']) {
          expect(src.contains(web), isFalse, reason: '$f.dart uses $web, a web part');
        }
        expect('TkSheet('.allMatches(src).length, lessThanOrEqualTo('TkAppLook('.allMatches(src).length), reason: '$f.dart: each sheet in TkAppLook');
      }
    });
  });
}
