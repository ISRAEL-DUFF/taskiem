// The stage 5a review's app findings (docs/spec/people-chatting.md, "As
// built: stage 5a, app", "App review fixes"): nothing read or marked read
// while the app is put away or a screen covers the chat; older messages
// kept once read; numbers from the phone's contacts matched as saved;
// businesses' logos in the one list; matching that goes on after a new key
// and stops at the day's limit with what it found; Block from the person's
// card; sends again with the same client id; the lists a page at a time; a
// suspended account; a chat just started; a share card's warning.

import 'dart:convert' show base64Decode;
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart' show ApiError;
import 'package:taskiem/app/phone.dart';
import 'package:taskiem/chat/attachments.dart' show PickedFile;
import 'package:taskiem/chat/door.dart' show BusinessFace;
import 'package:taskiem/chat/my_chat.dart';
import 'package:taskiem/chat/people.dart';
import 'package:taskiem/chat/people_cards.dart';
import 'package:taskiem/chat/people_chat.dart';
import 'package:taskiem/chat/people_contacts.dart';
import 'package:taskiem/chat/people_find.dart';
import 'package:taskiem/chat/people_settings.dart';
import 'package:taskiem/kit/kit.dart' show TkSmallButton;
import 'package:taskiem/kit/mobile.dart' show TkHeadButton;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'my_chats_test.dart' as mc;
import 'people_test.dart';
import 'support.dart' show loadFonts;

/// The app put away (as Android does: timers still run), and back.
void putAway(WidgetTester t) {
  for (final s in [AppLifecycleState.inactive, AppLifecycleState.hidden, AppLifecycleState.paused]) {
    t.binding.handleAppLifecycleStateChanged(s);
  }
}

void bringBack(WidgetTester t) {
  for (final s in [AppLifecycleState.hidden, AppLifecycleState.inactive, AppLifecycleState.resumed]) {
    t.binding.handleAppLifecycleStateChanged(s);
  }
}

/// A screen with one button that pushes [screen] (or does [tap]).
Widget launcher(Widget Function(BuildContext) screen) => Builder(
  builder: (context) => Center(child: TkSmallButton(label: 'Open', onPressed: () => Navigator.of(context).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => screen(context))))),
);

Map<String, dynamic> listRow(String id, String name, {String kind = 'person', bool archived = false, Map<String, dynamic> extra = const {}}) => {
  'kind': kind,
  'id': id,
  'name': name,
  if (kind == 'person') 'person_id': 'p-$id',
  if (kind == 'business') 'business_id': 'b-$id',
  'last': 'Hello',
  'last_mine': false,
  'last_at': '2026-11-17T09:00:00Z',
  'unread': 0,
  'muted': false,
  'archived': archived,
  'blocked': false,
  ...extra,
};

String? clientOfUpload(String body) => RegExp(r'name="client_id"\r\n\r\n([^\r\n]+)').firstMatch(body)?[1];

void main() {
  setUpAll(loadFonts);
  tearDown(() => TestWidgetsFlutterBinding.instance.handleAppLifecycleStateChanged(AppLifecycleState.resumed));

  group('1. not while put away', () {
    testWidgets('a person’s chat: put away, nothing is read and nothing marked read; back, it reads at once', (t) async {
      phone(t);
      final api = adaChat();
      final d = PeopleChatData(api.auth(), 'pc1', now: () => at);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d)));
      await settle(t);
      expect(api.sent('POST', '/v1/me/people/pc1/read'), hasLength(1), reason: 'seen: their messages marked read');
      putAway(t);
      api.calls.clear();
      for (var i = 0; i < 6; i++) {
        await t.pump(const Duration(seconds: 5));
      }
      await settle(t);
      expect(api.calls, isEmpty, reason: 'no read while put away: the server would take it as the person being here');
      bringBack(t);
      await settle(t);
      expect(api.sent('GET', '/v1/me/people/pc1/messages'), hasLength(1), reason: 'back in front: read at once');
      expect(api.sent('POST', '/v1/me/people/pc1/read'), hasLength(1));
      await drain(t);
    });

    testWidgets('a person’s chat under another screen: not read again, nothing marked read', (t) async {
      phone(t);
      final api = adaChat();
      final d = PeopleChatData(api.auth(), 'pc1', now: () => at);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d)));
      await settle(t);
      Navigator.of(t.element(find.byType(PeopleChatScreen))).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => const ColoredBox(color: Color(0xffffffff))));
      await settle(t);
      api.calls.clear();
      for (var i = 0; i < 4; i++) {
        await t.pump(const Duration(seconds: 5));
      }
      await settle(t);
      expect(api.calls, isEmpty);
      // Read by other means while covered (a refresh): still nothing marked.
      await t.runAsync(d.refresh);
      expect(api.sent('POST', '/v1/me/people/pc1/read'), isEmpty, reason: 'not seen: not marked read');
      await drain(t);
    });

    testWidgets('You’s list: not read again while put away', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/conversations'] = (_) => {'chats': <Object>[], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false};
      api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => at, presence: false)));
      await settle(t);
      await t.pump(const Duration(seconds: 11));
      await settle(t);
      expect(api.sent('GET', '/v1/me/conversations'), isNotEmpty, reason: 'in front: read every 10 seconds');
      putAway(t);
      api.calls.clear();
      for (var i = 0; i < 4; i++) {
        await t.pump(const Duration(seconds: 10));
      }
      await settle(t);
      expect(api.calls, isEmpty);
      await drain(t);
    });

    testWidgets('a chat with a business: put away, nothing read or marked read', (t) async {
      mc.phone(t);
      final api = mc.conversation();
      final d = MyChatData(api.auth(), 'c1', now: () => mc.at);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: MyChatScreen(data: d)));
      await mc.settle(t);
      expect(api.sent('POST', '/v1/me/chats/c1/read'), hasLength(1));
      putAway(t);
      api.calls.clear();
      for (var i = 0; i < 6; i++) {
        await t.pump(const Duration(seconds: 5));
      }
      await mc.settle(t);
      expect(api.calls, isEmpty);
      bringBack(t);
      await mc.settle(t);
      expect(api.sent('GET', '/v1/me/chats/c1/messages'), hasLength(1));
      await mc.drain(t);
    });
  });

  group('2. older messages stay', () {
    final old = {'id': 'm0', 'mine': false, 'kind': 'text', 'body': 'Long ago', 'read_at': '2026-11-16T09:00:00Z', 'created_at': '2026-11-16T08:00:00Z'};

    testWidgets('a person’s chat: the last older page read, then the latest read again: the older messages stay', (t) async {
      phone(t);
      final api = adaChat();
      api.routes['GET /v1/me/people/pc1/messages'] = (r) => r.url.queryParameters.containsKey('before') ? {'messages': [old], 'more': false} : {'messages': thread(), 'more': true};
      final d = await openPeople(t, api);
      // (A short chat reads its older page as it opens; once more is the same.)
      await t.runAsync(d.older);
      expect(d.messages!.first.id, 'm0');
      expect(d.hasOlder, isFalse);
      await t.runAsync(d.refresh);
      expect(d.messages!.first.id, 'm0', reason: 'kept across reads of the latest page');
      expect(d.hasOlder, isFalse, reason: 'nothing more before them');
      await drain(t);
    });

    testWidgets('a chat with a business: the same', (t) async {
      mc.phone(t);
      final api = mc.conversation();
      api.routes['GET /v1/me/chats/c1/messages'] = (r) => r.url.queryParameters.containsKey('before') ? {'messages': [old], 'more': false} : {'messages': mc.messages, 'more': true};
      final d = await mc.openChat(t, api);
      await t.runAsync(d.older);
      expect(d.messages!.first.id, 'm0');
      await t.runAsync(d.refresh);
      expect(d.messages!.first.id, 'm0');
      expect(d.hasOlder, isFalse);
      await mc.drain(t);
    });
  });

  test('3. numbers from the phone’s contacts: without +, with 2340, with format characters; toE164 as it was', () {
    expect(contactE164('2348031234567'), '+2348031234567');
    expect(contactE164('23408031234567'), '+2348031234567');
    expect(contactE164('\u202A+234 803 123 4567\u202C'), '+2348031234567');
    expect(contactE164('\u200E0803-123-4567'), '+2348031234567');
    expect(contactE164('08031234567'), '+2348031234567');
    expect(contactE164('+44 20 7946 0958'), '+442079460958');
    expect(contactE164('234123'), isNull);
    // Typed numbers are read as before.
    expect(toE164('2348031234567'), isNull);
    expect(toE164('\u202A+2348031234567\u202C'), isNull);
    expect(toE164('0803 123 4567'), '+2348031234567');
  });

  testWidgets('4. a business’s logo in You’s Chats and Archived; online said to a screen reader', (t) async {
    phone(t);
    final api = Api();
    final logo = {'urls': {'96': 'http://localhost/logo-96.png'}};
    await t.pumpWidget(TaskiemApp(
      mode: TkMode.light,
      home: MyChatRows(
        chats: [
          ChatListItem.fromJson(listRow('c1', 'Mama’s Kitchen', kind: 'business', extra: {'photo': logo})),
          ChatListItem.fromJson(listRow('pc1', 'Ada Obi', extra: {'person': card(online: true)})),
        ],
        now: at,
        onOpen: (_) {},
        transport: api.auth().transport,
      ),
    ));
    await t.pump();
    final face = t.widget<BusinessFace>(find.byType(BusinessFace));
    expect(face.urls, logo['urls']);
    expect(find.bySemanticsLabel(RegExp('^Chat with a person, online')), findsOneWidget);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: Center(child: PersonFace(name: 'Ada Obi', transport: api.auth().transport, online: true))));
    await t.pump();
    expect(find.bySemanticsLabel('Online'), findsOneWidget, reason: 'the dot is said');
  });

  group('6. matching contacts', () {
    final numbers = [for (var i = 0; i < 5; i++) '0803123456$i'];
    final contacts = [for (final n in numbers) (name: 'Friend $n', numbers: [n])];

    Map<String, dynamic> key(String id, {int perDay = 5000}) => {'key_id': id, 'key': vectorKey, 'expires_at': '2026-11-20T00:00:00Z', 'hash': 'sha256', 'max_batch': 2, 'max_per_day': perDay, 'words': ''};

    test('a new key on the way: only what was not answered goes again, with it', () async {
      final api = Api();
      var keys = 0;
      api.routes['GET /v1/me/contacts/key'] = (_) => key('k${++keys}');
      var k1 = 0;
      api.routes['POST /v1/me/contacts/match'] = (r) {
        if (api.body(r)['key_id'] == 'k1' && ++k1 > 1) return answer({'error': 'The key changed.'}, 409);
        return {'matches': <Object>[]};
      };
      final r = await matchPhoneContacts(api.auth().api, contacts);
      expect(keys, 2);
      final answered = [for (final c in api.sent('POST', '/v1/me/contacts/match')) if (api.body(c)['key_id'] == 'k2' || c == api.sent('POST', '/v1/me/contacts/match').first) ...(api.body(c)['hashes'] as List)];
      expect(answered, hasLength(5), reason: 'each number answered once');
      expect(answered.toSet(), hasLength(5));
      expect(r.stopped, isFalse);
      expect(r.checked, 5);
    });

    test('the day’s limit (429): who was found so far is kept', () async {
      final api = Api();
      api.routes['GET /v1/me/contacts/key'] = (_) => key('k1');
      final first = contactHash(base64Decode(vectorKey), '+2348031234560');
      var n = 0;
      api.routes['POST /v1/me/contacts/match'] = (r) {
        if (++n > 1) return answer({'error': 'Too many numbers checked today.'}, 429);
        return {
          'matches': [
            {'hash': first, 'person': card(inContacts: false), 'add_token': 't'},
          ],
        };
      };
      final r = await matchPhoneContacts(api.auth().api, contacts);
      expect(r.found.single.phoneName, 'Friend 08031234560');
      expect(r.stopped, isTrue);
      expect(r.checked, 2);
      expect(r.total, 5);
    });

    test('max_per_day (1000 in a first week): no more numbers than it; a 429 at once is said', () async {
      final api = Api();
      api.routes['GET /v1/me/contacts/key'] = (_) => key('k1', perDay: 3);
      api.routes['POST /v1/me/contacts/match'] = (_) => {'matches': <Object>[]};
      final r = await matchPhoneContacts(api.auth().api, contacts);
      final sent = [for (final c in api.sent('POST', '/v1/me/contacts/match')) ...(api.body(c)['hashes'] as List)];
      expect(sent, hasLength(3));
      expect(r.stopped, isTrue);
      expect((r.checked, r.total), (3, 5));

      final none = Api();
      none.routes['GET /v1/me/contacts/key'] = (_) => key('k1');
      none.routes['POST /v1/me/contacts/match'] = (_) => answer({'error': 'Too many.'}, 429);
      await expectLater(matchPhoneContacts(none.auth().api, contacts), throwsA(isA<ApiError>()));
    });

    testWidgets('the screen says how many were checked when the day’s limit stopped it', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/contacts/key'] = (_) => key('k1', perDay: 3);
      api.routes['POST /v1/me/contacts/match'] = (_) => {'matches': <Object>[]};
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ContactsMatchScreen(auth: api.auth(), contacts: FakeContacts(contacts), onChat: (_) async {})));
      await settle(t);
      await press(t, find.text('Agree and check my contacts'));
      expect(find.textContaining('Checked 3 of your 5 numbers'), findsOneWidget);
      await drain(t);
    });
  });

  group('7. the person’s card', () {
    testWidgets('Block there, the chat removed: the card and the chat close, back to where it was opened', (t) async {
      phone(t);
      final api = adaChat();
      api.routes['POST /v1/me/people/pc1/block'] = (_) => {'blocked': true, 'removed': true, 'reported': false};
      final auth = api.auth();
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: launcher((_) => PeopleChatScreen(data: PeopleChatData(auth, 'pc1', follow: false, now: () => at)))));
      await press(t, find.text('Open'));
      await press(t, find.bySemanticsLabel(RegExp('^About Ada Obi')));
      expect(find.byType(PersonInfoScreen), findsOneWidget);
      await press(t, find.text('Block Ada Obi'));
      await press(t, find.text('Block'));
      expect(api.sent('POST', '/v1/me/people/pc1/block'), hasLength(1));
      expect(find.byType(PersonInfoScreen), findsNothing);
      expect(find.byType(PeopleChatScreen), findsNothing);
      expect(find.text('Open'), findsOneWidget);
      await drain(t);
    });

    testWidgets('blocked: Unblock on the card', (t) async {
      phone(t);
      var blocked = true;
      final api = adaChat();
      api.routes['GET /v1/me/people/pc1'] = (_) => peopleChat(blocked: blocked);
      api.routes['DELETE /v1/me/people/pc1/block'] = (_) {
        blocked = false;
        return {'blocked': false};
      };
      await openPeople(t, api);
      await press(t, find.bySemanticsLabel(RegExp('^About Ada Obi')));
      await press(t, find.text('Unblock Ada Obi'));
      expect(api.sent('DELETE', '/v1/me/people/pc1/block'), hasLength(1));
      expect(find.text('Block Ada Obi'), findsOneWidget, reason: 'the card shows Block again');
      await drain(t);
    });
  });

  group('9. sent again with the same client id', () {
    testWidgets('a photo and a card whose answer never came', (t) async {
      phone(t);
      final api = adaChat();
      var files = 0;
      api.routes['POST /v1/me/people/pc1/files'] = (_) => ++files == 1
          ? answer({'error': 'Unavailable'}, 503)
          : answer({'id': 'f1', 'mine': true, 'kind': 'media', 'body': '', 'status': 'sent', 'read_at': null, 'created_at': '2026-11-17T10:29:00Z'}, 201);
      var cards = 0;
      api.routes['POST /v1/me/people/pc1/messages'] = (r) => ++cards == 1
          ? answer({'error': 'Unavailable'}, 503)
          : answer({'id': 'k$cards', 'mine': true, 'kind': 'text', 'body': '', 'status': 'sent', 'read_at': null, 'created_at': '2026-11-17T10:29:00Z'}, 201);
      final d = await openPeople(t, api);
      final photo = PickedFile(bytes: Uint8List.fromList(List.filled(64, 7)), name: 'cake.png', type: 'image/png');
      await t.runAsync(() => d.send('', file: photo));
      await t.runAsync(() => d.send('', file: photo));
      final ups = api.sent('POST', '/v1/me/people/pc1/files');
      expect(ups, hasLength(2));
      expect(clientOfUpload(ups[0].body), isNotNull);
      expect(clientOfUpload(ups[1].body), clientOfUpload(ups[0].body));

      final c = SendPeopleMessageRequestCard(type: 'business', businessId: 'b1');
      expect(await t.runAsync(() => d.sendCard(c)), isFalse);
      expect(await t.runAsync(() => d.sendCard(c)), isTrue);
      expect(await t.runAsync(() => d.sendCard(c)), isTrue);
      final sent = [for (final r in api.sent('POST', '/v1/me/people/pc1/messages')) api.body(r)['client_id'] as String];
      expect(sent[1], sent[0], reason: 'the same card again goes with the same id');
      expect(sent[2], isNot(sent[1]), reason: 'answered: a new one next time');
      await drain(t);
    });

    testWidgets('Share with someone: the same card to the same chat again goes with the same id', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/people'] = (_) => {'chats': [peopleChat(id: 'f1', name: 'Friend 1')], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false};
      var n = 0;
      api.routes['POST /v1/me/people/f1/messages'] = (_) => ++n == 1
          ? answer({'error': 'Unavailable'}, 503)
          : answer({'id': 'k$n', 'mine': true, 'kind': 'card', 'body': '', 'status': 'sent', 'read_at': null, 'created_at': '2026-11-17T10:29:00Z'}, 201);
      final auth = api.auth();
      final c = SendPeopleMessageRequestCard(type: 'business', businessId: 'b9');
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: Builder(builder: (context) => Center(child: TkSmallButton(label: 'Share', onPressed: () => shareWithPeople(context, auth, c, what: 'Mama’s Kitchen')))),
      ));
      for (var i = 0; i < 2; i++) {
        await press(t, find.text('Share'));
        await press(t, find.text('Friend 1'));
        await press(t, find.text('Send'));
        await t.pump(const Duration(seconds: 5));
      }
      final ids = [for (final r in api.sent('POST', '/v1/me/people/f1/messages')) api.body(r)['client_id']];
      expect(ids, hasLength(2));
      expect(ids[0], isNotNull);
      expect(ids[1], ids[0]);
      await drain(t);
    });
  });

  group('10. small things', () {
    test('Blocked today, yesterday, or on a date as written', () {
      expect(blockedDay(at, at), 'today');
      expect(blockedDay(at.subtract(const Duration(days: 1)), at), 'yesterday');
      expect(blockedDay(DateTime.utc(2026, 11, 14, 10), at), 'on Sat 14 Nov');
      expect(blockedDay(DateTime.utc(2026, 10, 2, 10), at), 'on 2 Oct');
    });

    testWidgets('the forward and share picker: archived chats too; not a suspended account', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/people'] = (r) => r.url.queryParameters['archived'] == '1'
          ? {'chats': [{...peopleChat(id: 'a1', name: 'Old Friend'), 'archived': true}], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false}
          : {'chats': [peopleChat(id: 'f1', name: 'Friend 1'), {...peopleChat(id: 's1', name: 'Sleepy'), 'suspended': true}], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false};
      final auth = api.auth();
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: Builder(builder: (context) => Center(child: TkSmallButton(label: 'Pick', onPressed: () => pickPeopleChats(context, auth, title: 'Forward to', what: 'Hi')))),
      ));
      await press(t, find.text('Pick'));
      expect(find.text('Friend 1'), findsOneWidget);
      expect(find.text('Old Friend'), findsOneWidget);
      expect(find.text('Sleepy'), findsNothing);
      await drain(t);
    });

    testWidgets('the picker reads the next page as it is scrolled to its end', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/people'] = (r) {
        final q = r.url.queryParameters;
        if (q['archived'] == '1') return {'chats': <Object>[], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false};
        if (q['before'] == 'n1') return {'chats': [peopleChat(id: 'g1', name: 'Page Two')], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false};
        return {'chats': [peopleChat(id: 'f1', name: 'Friend 1')], 'unread': 0, 'requests': 0, 'invites': 0, 'more': true, 'next': 'n1'};
      };
      final auth = api.auth();
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: Builder(builder: (context) => Center(child: TkSmallButton(label: 'Pick', onPressed: () => pickPeopleChats(context, auth, title: 'Forward to', what: 'Hi')))),
      ));
      await press(t, find.text('Pick'));
      await settle(t);
      expect(find.text('Page Two'), findsOneWidget);
      await drain(t);
    });

    testWidgets('Add to contacts by link asks for a name too', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/people/links/k7m2q9x4pz'] = (_) => {'found': true, 'person': card(inContacts: false), 'add_token': 'tok'};
      api.routes['POST /v1/me/contacts'] = (_) => answer({...card(), 'source': 'link', 'added_at': '2026-11-17T10:00:00Z'}, 201);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PersonLinkScreen(auth: api.auth(), code: 'k7m2q9x4pz', openChat: (id, {started}) async {})));
      await settle(t);
      await press(t, find.text('Add to contacts'));
      await press(t, find.text('Save'));
      expect(api.body(api.sent('POST', '/v1/me/contacts').single), {'person_id': 'p1', 'add_token': 'tok', 'from': 'link'}, reason: 'left empty: their own name');
      await drain(t);
    });

    testWidgets('a finger’s room: at least 44 round a head button and a small button, drawn as before', (t) async {
      phone(t);
      var head = 0, small = 0;
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              // Room round each, as a head and a row give it.
              Padding(padding: const EdgeInsets.all(20), child: TkHeadButton(icon: 'b-plus', label: 'New chat', onTap: () => head++)),
              const SizedBox(height: 20),
              Padding(padding: const EdgeInsets.all(20), child: TkSmallButton(label: 'Add', onPressed: () => small++)),
            ],
          ),
        ),
      ));
      await t.pump();
      final h = t.getRect(find.byType(TkHeadButton));
      expect(h.width, lessThan(44), reason: 'drawn as before');
      await t.tapAt(h.center + const Offset(21.5, 0));
      await t.tapAt(h.center + const Offset(0, -21.5));
      final s = t.getRect(find.byType(TkSmallButton));
      await t.tapAt(s.center + Offset(0, 21.5 - (s.height < 43 ? 0 : s.height / 2 - 21.5)));
      await t.pump(const Duration(milliseconds: 500));
      expect(head, 2);
      expect(small, 1);
    });
  });

  group('11. the server’s changes', () {
    testWidgets('You’s Chats a page at a time: the next page read as the list reaches its end', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/conversations'] = (r) {
        final q = r.url.queryParameters;
        if (q['archived'] == '1') return {'chats': <Object>[], 'unread': 0, 'requests': 0, 'invites': 0, 'more': false};
        if (q['before'] == 'n1') return {'chats': [listRow('pc9', 'Page Two Person')], 'unread': 3, 'requests': 0, 'invites': 0, 'more': false};
        return {'chats': [listRow('pc1', 'Ada Obi')], 'unread': 3, 'requests': 0, 'invites': 0, 'more': true, 'next': 'n1'};
      };
      api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => at, presence: false)));
      await settle(t);
      await settle(t);
      expect(find.text('Ada Obi'), findsOneWidget);
      expect(find.text('Page Two Person'), findsOneWidget);
      expect(api.sent('GET', '/v1/me/conversations').where((r) => r.url.queryParameters['before'] == 'n1'), hasLength(1));
      // Read again: the later page stays.
      await t.pump(const Duration(seconds: 11));
      await settle(t);
      expect(find.text('Page Two Person'), findsOneWidget);
      await drain(t);
    });

    testWidgets('a suspended account: the head, the row and the chat say so; no box', (t) async {
      phone(t);
      final api = adaChat(c: {...peopleChat(), 'suspended': true});
      await openPeople(t, api);
      expect(find.text(suspendedWords), findsOneWidget, reason: 'the head');
      expect(find.textContaining('You can read the chat, but not write to it now.'), findsOneWidget);
      expect(find.bySemanticsLabel('Message Ada Obi'), findsNothing, reason: 'no box');
      expect(myChatPreview(ChatListItem.fromJson(listRow('pc1', 'Ada Obi', extra: {'suspended': true}))), 'This account can’t get messages right now');
      await drain(t);
    });

    testWidgets('a chat just started opens from the POST answer, before it is in any list', (t) async {
      phone(t);
      final api = Api();
      api.routes['POST /v1/people/lookup'] = (_) => {'found': true, 'person': card(inContacts: false), 'add_token': 'tok'};
      api.routes['POST /v1/me/people'] = (_) => answer(peopleChat(id: 'new1', last: ''), 201);
      PeopleChat? got;
      await t.pumpWidget(TaskiemApp(
        mode: TkMode.light,
        home: NewPeopleChatScreen(auth: api.auth(), openChat: (id, {started}) async => got = started, contacts: FakeContacts(const [])),
      ));
      await settle(t);
      await typeIn(t, find.byKey(const ValueKey('find-phone')), '0803 123 4567');
      await press(t, find.text('Find'));
      await press(t, find.text('Chat'));
      expect(got?.id, 'new1');
      final d = PeopleChatData(api.auth(), 'new1', follow: false, started: got);
      expect(d.chat?.name, 'Ada Obi', reason: 'shown before any read');
      await drain(t);
    });

    testWidgets('a share card from someone not in your contacts: the transfer warning', (t) async {
      phone(t);
      final api = adaChat(messages: [
        {
          'id': 's1', 'mine': false, 'kind': 'card', 'body': '', 'warn': true, 'warn_transfer': true,
          'card': {'type': 'split', 'title': 'Your share: ₦3,000', 'amount_kobo': 300000, 'what': 'the cake', 'pay_to': {'bank': 'GTBank', 'account_number': '0123456789', 'account_name': 'Ada Obi'}, 'paid': false},
          'read_at': null, 'created_at': '2026-11-17T09:00:00Z',
        },
        {
          'id': 's2', 'mine': false, 'kind': 'card', 'body': '', 'warn': true,
          'card': {'type': 'split', 'title': 'Your share: ₦1,000', 'amount_kobo': 100000, 'what': 'suya', 'pay_to': {'bank': 'GTBank', 'account_number': '0123456789', 'account_name': 'Ada Obi'}, 'paid': false},
          'read_at': null, 'created_at': '2026-11-17T09:01:00Z',
        },
      ]);
      await openPeople(t, api);
      expect(find.text(peopleTransferWarning), findsNWidgets(2), reason: 'warn alone on a share card is the transfer warning too');
      await drain(t);
    });

    testWidgets('Privacy with people: “From their contacts” works within “By my number”', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/privacy/people'] = (_) => {
        'find_by_number': 'contacts', 'groups_add': 'contacts', 'find_from_contacts': true, 'message': 'everyone', 'last_seen': 'everyone', 'photo': 'everyone', 'show_number': false, 'read_ticks': true,
      };
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeoplePrivacyPage(auth: api.auth())));
      await settle(t);
      expect(find.textContaining('as far as By my number lets them'), findsOneWidget);
      await drain(t);
    });
  });
}
