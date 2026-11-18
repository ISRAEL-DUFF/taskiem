// People chatting with people in the app (docs/spec/people-chatting.md,
// stage 5a; decision 0069): You's one Chats list with Requests; a chat with
// a person (ticks only while read ticks go both ways, replies and quotes,
// Forwarded, delete for everyone within 24 hours, the warning on a
// stranger's message, a message held by the checks, "Deleted account");
// a request opened without telling the sender, Accept, Delete, Block and
// report; New chat by number with Invite, and by link; finding friends from
// contacts only after agreeing, hashed as the server hashes (the fixed
// vector engine/people's TestContactHashVector holds too); Privacy with
// people; blocked people; shared cards opened live; "Your share" with Mark
// as paid; the bank account for shares; links and pushes routed; and the
// words, which never say people chats are end-to-end encrypted.

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/applinks.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/config.dart' show taskiemUrl;
import 'package:taskiem/app/route.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/people.dart';
import 'package:taskiem/chat/people_cards.dart';
import 'package:taskiem/chat/people_chat.dart';
import 'package:taskiem/chat/people_contacts.dart';
import 'package:taskiem/chat/people_find.dart';
import 'package:taskiem/chat/people_settings.dart';
import 'package:taskiem/chat/words.dart' show clock;
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/you.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart' show loadFonts;

/// A stand-in server: answers by "METHOD /path", every request kept.
class Api {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  http.Client client() => MockClient((req) async {
    calls.add(req);
    final f = routes['${req.method} ${req.url.path}'];
    if (f == null) return http.Response(jsonEncode({'error': 'not here'}), 404, headers: _json);
    var out = f(req);
    if (out is Future) out = await out;
    if (out is http.Response) return out;
    return http.Response(jsonEncode(out ?? {}), 200, headers: _json);
  });

  List<http.Request> sent(String method, String path) => calls.where((c) => c.method == method && c.url.path == path).toList();
  Map<String, dynamic> body(http.Request r) => jsonDecode(r.body) as Map<String, dynamic>;

  Auth auth() {
    final a = Auth(
      transport: Transport(base: 'http://localhost', token: 'tok', client: client()),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Test phone', platform: 'android')),
    );
    a.me = Me.fromJson({
      'tenant_id': null,
      'personal': true,
      'roles': <String>[],
      'permissions': <String>[],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Tolu Ade', 'email': 'p@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

const _json = {'content-type': 'application/json; charset=utf-8'};
http.Response answer(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: _json);

final at = DateTime.utc(2026, 11, 17, 10, 30);

Map<String, dynamic> card({String id = 'p1', String name = 'Ada Obi', bool readTicks = true, Object? lastSeen = '2026-11-17T09:05:00Z', bool online = false, bool inContacts = true, Object? contactName}) => {
  'id': id,
  'name': name,
  'deleted': false,
  'contact_name': contactName,
  'in_contacts': inContacts,
  'they_added_you': true,
  'photo': null,
  'last_seen_at': lastSeen,
  'online': online,
  'read_ticks': readTicks,
};

Map<String, dynamic> peopleChat({
  String id = 'pc1',
  String name = 'Ada Obi',
  bool request = false,
  bool waiting = false,
  bool blocked = false,
  bool gone = false,
  bool readTicks = true,
  Object? phone,
  int unread = 0,
  String last = 'See you at 6',
}) => {
  'id': id,
  'kind': 'person',
  'person_id': 'p1',
  'name': name,
  'gone': gone,
  'last': last,
  'last_mine': false,
  'last_at': '2026-11-17T09:10:00Z',
  'unread': unread,
  'muted': false,
  'archived': false,
  'request': request,
  'waiting': waiting,
  'blocked': blocked,
  'phone': phone,
  if (!gone) 'person': card(name: name, readTicks: readTicks),
};

List<Map<String, dynamic>> thread() => [
  {'id': 'm1', 'mine': true, 'kind': 'text', 'body': 'Are you coming tonight?', 'status': 'read', 'read_at': '2026-11-17T09:02:00Z', 'created_at': '2026-11-17T09:00:00Z'},
  {'id': 'm2', 'mine': false, 'kind': 'text', 'body': 'Yes, see you at 6', 'reply_to': 'm1', 'read_at': null, 'created_at': '2026-11-17T09:05:00Z'},
  {'id': 'm3', 'mine': false, 'kind': 'text', 'body': 'Look at this', 'forwarded': true, 'forwarded_many': true, 'read_at': null, 'created_at': '2026-11-17T09:06:00Z'},
  {'id': 'm4', 'mine': true, 'kind': 'text', 'body': '', 'deleted': true, 'status': 'delivered', 'read_at': null, 'created_at': '2026-11-17T09:07:00Z'},
  {'id': 'm5', 'mine': true, 'kind': 'text', 'body': 'Bring the cake', 'status': 'delivered', 'read_at': null, 'created_at': '2026-11-17T10:20:00Z'},
];

Future<void> settle(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 600));
}

Future<void> drain(WidgetTester t) async {
  await t.pumpWidget(const SizedBox());
  await t.pump(const Duration(seconds: 5));
}

Future<void> press(WidgetTester t, Finder f) async {
  await t.ensureVisible(f);
  await t.pump();
  await t.tap(f);
  await settle(t);
}

Future<void> typeIn(WidgetTester t, Finder field, String text) async {
  final box = find.descendant(of: field, matching: find.byType(EditableText));
  await t.enterText(box.first, text);
  await t.pump();
}

void phone(WidgetTester t, [double height = 1600]) {
  t.view.physicalSize = Size(390, height);
  t.view.devicePixelRatio = 1;
  addTearDown(t.view.reset);
}

/// A chat with Ada, served.
Api adaChat({Map<String, dynamic>? c, List<Map<String, dynamic>>? messages}) {
  final api = Api();
  api.routes['GET /v1/me/people/pc1'] = (_) => c ?? peopleChat(phone: '+2348031234567');
  api.routes['GET /v1/me/people/pc1/messages'] = (_) => {'messages': messages ?? thread(), 'more': false};
  api.routes['POST /v1/me/people/pc1/read'] = (_) => answer({}, 204);
  return api;
}

Future<PeopleChatData> openPeople(WidgetTester t, Api api) async {
  final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at);
  await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d)));
  await settle(t);
  return d;
}

/// The phone's contacts, as a test gives them; whether they were asked
/// for and read.
class FakeContacts extends PhoneContacts {
  FakeContacts(this.list, {this.allow = true});
  final List<PhoneContact> list;
  final bool allow;
  bool asked = false;
  bool read_ = false;

  @override
  Future<bool> ask() async {
    asked = true;
    return allow;
  }

  @override
  Future<List<PhoneContact>> read() async {
    read_ = true;
    return list;
  }
}

/// A white 1×1 PNG, for the QR code.
const _png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=';

/// The key of engine/people's TestContactHashVector.
const vectorKey = 'AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=';

void main() {
  setUpAll(loadFonts);

  test('the contacts hash: the same key and number give the server’s 64 hex digits', () {
    // engine/people/hash_test.go holds the same vector.
    expect(contactHash(base64.decode(vectorKey), '+2348031234567'), 'b4913c73c5ed5c7ea72d96186255b89cddbb6931ce8aea9da6a39d26af286458');
    expect(contactHash(base64.decode(vectorKey), '+2348031234568'), isNot('b4913c73c5ed5c7ea72d96186255b89cddbb6931ce8aea9da6a39d26af286458'));
    expect(contactHash(base64.decode(vectorKey), '+2348031234567'), hasLength(64));
  });

  test('matching contacts: numbers in E.164, each once, hashed and sent; a changed key asked again once', () async {
    final api = Api();
    var keys = 0;
    api.routes['GET /v1/me/contacts/key'] = (_) {
      keys++;
      return {'key_id': 'k$keys', 'key': vectorKey, 'expires_at': '2026-11-20T00:00:00Z', 'hash': 'sha256', 'max_batch': 2, 'max_per_day': 5000, 'words': 'Taskiem holds the key.'};
    };
    final hashOf = contactHash(base64.decode(vectorKey), '+2348031234567');
    api.routes['POST /v1/me/contacts/match'] = (r) {
      if (api.body(r)['key_id'] == 'k1') return answer({'error': 'The key changed.', 'key_changed': true}, 409);
      final hashes = (api.body(r)['hashes'] as List).cast<String>();
      return {
        'matches': [
          if (hashes.contains(hashOf)) {'hash': hashOf, 'person': card(inContacts: false), 'add_token': 'tok-ada'},
        ],
      };
    };
    final found = await matchPhoneContacts(api.auth().api, [
      (name: 'Ada', numbers: ['0803 123 4567', '+234 803 123 4567']),
      (name: 'Bisi', numbers: ['08031234568', 'not a number']),
      (name: 'Chidi', numbers: ['07012345678']),
    ]);
    expect(found.found.single.person.name, 'Ada Obi');
    expect(found.found.single.phoneName, 'Ada');
    expect(found.found.single.addToken, 'tok-ada');
    expect(keys, 2, reason: 'the key changed: fetched again');
    final sent = [for (final r in api.sent('POST', '/v1/me/contacts/match').where((r) => api.body(r)['key_id'] == 'k2')) ...(api.body(r)['hashes'] as List)];
    expect(sent, hasLength(3), reason: 'three numbers, each once, in batches of two');
    expect(sent, contains(hashOf));
    expect(sent.every((h) => RegExp(r'^[0-9a-f]{64}$').hasMatch(h as String)), isTrue);
    expect(api.calls.map((c) => c.body).join(), isNot(contains('803')), reason: 'no number goes in readable form');
  });

  test('links and pushes: a person’s chat, Requests, a request, a person’s link', () {
    expect(youRouteOf('/you/people/pc1'), (page: YouPage.person, id: 'pc1', ref: '', tool: null));
    expect(youRouteOf('/you/people/requests'), (page: YouPage.requests, id: '', ref: '', tool: null));
    expect(youRouteOf('/you/people/requests/pc1'), (page: YouPage.request, id: 'pc1', ref: '', tool: null));
    expect(routeOf('/you/people/pc1').screen, RouteScreen.youPage);
    expect(appPageOf(Uri.parse('$taskiemUrl/you/people/pc1')), '/you/people/pc1');
    expect(appPageOf(Uri.parse('$taskiemUrl/you/people/requests/pc1')), '/you/people/requests/pc1');
    expect(appPageOf(Uri.parse('$taskiemUrl/p/k7m2q9x4pz')), '/p/k7m2q9x4pz');
    expect(appPageOf(Uri.parse('$taskiemUrl/p/../x')), isNull);
    expect(personLinkOf('/p/k7m2q9x4pz'), 'k7m2q9x4pz');
    expect(personLinkOf('/m/k7m2q9x4pz'), isNull);
    expect(personCodeOf('$taskiemUrl/p/k7m2q9x4pz'), 'k7m2q9x4pz');
    expect(personCodeOf('k7m2q9x4pz'), 'k7m2q9x4pz');
    expect(personCodeOf('https://example.com/p/k7m2q9x4pz'), isNull, reason: 'only this server’s links');
  });

  test('the words: kept by Taskiem, read only on a report or a lawful request; never claimed end-to-end encrypted', () {
    expect(peopleKeptWords, contains('kept by Taskiem'));
    expect(peopleKeptWords, contains('not end-to-end encrypted'));
    expect(peopleKeptWords, contains('when you report a chat'));
    expect(peopleKeptWords, contains('when the law requires it'));
    expect(contactsAskWords.join(' '), contains('Taskiem holds the key'));
    // Every mention of encryption in the people chats' code says it is not.
    for (final f in Directory('lib/chat').listSync().whereType<File>().where((f) => f.path.contains('people'))) {
      final text = f.readAsStringSync().toLowerCase();
      for (final m in RegExp(r'end-to-end').allMatches(text)) {
        final around = text.substring((m.start - 80).clamp(0, text.length), m.end);
        expect(around, anyOf(contains('not '), contains('never'), contains('nothing')), reason: '${f.path}: "$around"');
      }
    }
  });

  testWidgets('You: one Chats list, businesses and people together, each marked; Requests with its count; a person opens their chat', (t) async {
    phone(t);
    final api = adaChat();
    api.routes['GET /v1/me/conversations'] = (r) => r.url.queryParameters['archived'] == '1'
        ? {'chats': <Object>[], 'unread': 0, 'requests': 2, 'more': false}
        : {
            'chats': [
              {...peopleChat(unread: 1), 'kind': 'person'},
              {'kind': 'business', 'id': 'c1', 'name': 'Mama’s Kitchen', 'business_id': 'b1', 'last': 'Your order is ready', 'last_mine': false, 'last_at': '2026-11-17T08:00:00Z', 'unread': 0, 'muted': false, 'archived': false, 'blocked': false},
            ],
            'unread': 1,
            'requests': 2, 'more': false,
          };
    api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => at, presence: false)));
    await settle(t);
    expect(find.text('Ada Obi'), findsOneWidget);
    expect(find.text('Mama’s Kitchen'), findsOneWidget);
    expect(find.text('Business'), findsOneWidget, reason: 'a business’s row is marked');
    expect(find.bySemanticsLabel(RegExp('^Chat with a person')), findsOneWidget);
    expect(find.text('Message requests'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('Message requests, 2')), findsOneWidget);
    expect(api.sent('GET', '/v1/me/chats'), isEmpty, reason: 'the one list is /v1/me/conversations');

    await press(t, find.text('Ada Obi'));
    expect(find.text('Are you coming tonight?'), findsWidgets);
    expect(api.sent('POST', '/v1/me/people/pc1/read'), hasLength(1));
    await drain(t);
  });

  testWidgets('a person’s chat: the head’s card, ticks, a quote, Forwarded many times, a deleted message', (t) async {
    phone(t);
    final api = adaChat();
    await openPeople(t, api);
    expect(find.text('Ada Obi'), findsWidgets);
    expect(find.text('last seen today at ${clock(DateTime.parse('2026-11-17T09:05:00Z'))}'), findsOneWidget);
    expect(find.text('Forwarded many times'), findsOneWidget);
    expect(find.text('This message was deleted'), findsOneWidget);
    expect(find.bySemanticsLabel('Read'), findsOneWidget, reason: 'm1 read, both have read ticks on');
    expect(find.bySemanticsLabel('Delivered'), findsNWidgets(2));
    // The reply quotes what it answers.
    expect(find.text('Are you coming tonight?'), findsNWidgets(2));
    await drain(t);
  });

  testWidgets('read ticks off on either side: a read message shows delivered only', (t) async {
    phone(t);
    final api = adaChat(c: peopleChat(readTicks: false));
    await openPeople(t, api);
    expect(find.bySemanticsLabel('Read'), findsNothing);
    expect(find.bySemanticsLabel('Delivered'), findsNWidgets(3));
    await drain(t);
  });

  testWidgets('sending: a client id and the reply; held by the checks, the words come back and the server says why', (t) async {
    phone(t);
    final api = adaChat();
    var hold = false;
    api.routes['POST /v1/me/people/pc1/messages'] = (r) {
      if (hold) return answer({'error': 'This message was not sent: it looks like a scam.'}, 403);
      final b = api.body(r);
      return answer({'id': 'm9', 'mine': true, 'kind': 'text', 'body': b['text'], 'reply_to': b['reply_to'], 'status': 'sent', 'read_at': null, 'created_at': '2026-11-17T10:29:00Z'}, 201);
    };
    final d = await openPeople(t, api);
    await d.send('On my way', reply: PeopleMessage.fromJson(thread()[1]));
    await settle(t);
    final b = api.body(api.sent('POST', '/v1/me/people/pc1/messages').single);
    expect(b['text'], 'On my way');
    expect(b['reply_to'], 'm2');
    expect(b['client_id'], isNotEmpty);
    expect(find.text('On my way'), findsOneWidget);
    expect(find.bySemanticsLabel('Sent'), findsOneWidget);

    hold = true;
    await d.send('Send me your code');
    await settle(t);
    expect(find.text('This message was not sent: it looks like a scam.'), findsOneWidget);
    expect((t.widget(find.byType(EditableText).last) as EditableText).controller.text, 'Send me your code', reason: 'the words come back to the box');
    await drain(t);
  });

  testWidgets('a stranger’s message the checks marked: the warning on the receiver’s side only', (t) async {
    phone(t);
    final api = adaChat(messages: [
      {'id': 'w1', 'mine': false, 'kind': 'text', 'body': 'You have won! Pay the fee to claim', 'warn': true, 'read_at': null, 'created_at': '2026-11-17T09:00:00Z'},
      {'id': 'w2', 'mine': true, 'kind': 'text', 'body': 'Send to 0123456789', 'warn': true, 'status': 'sent', 'read_at': null, 'created_at': '2026-11-17T09:01:00Z'},
    ]);
    await openPeople(t, api);
    expect(find.text(strangerWarning), findsOneWidget);
    await drain(t);
  });

  testWidgets('delete: for everyone within 24 hours, for me after', (t) async {
    phone(t);
    final api = adaChat();
    api.routes['DELETE /v1/me/people/pc1/messages/m5'] = (_) => answer({}, 204);
    final d = await openPeople(t, api);
    final m5 = d.messages!.firstWhere((m) => m.id == 'm5').m;
    final m1 = d.messages!.firstWhere((m) => m.id == 'm1').m;
    expect(d.canDeleteForEveryone(m5), isTrue);
    expect(d.canDeleteForEveryone(PeopleMessage.fromJson({...thread()[0], 'created_at': '2026-11-16T10:00:00Z'})), isFalse, reason: 'more than 24 hours ago');
    expect(d.canDeleteForEveryone(PeopleMessage.fromJson(thread()[1])), isFalse, reason: 'theirs');
    expect(d.canDeleteForEveryone(m1), isTrue);
    await d.delete(m5, everyone: true);
    await settle(t);
    expect(api.sent('DELETE', '/v1/me/people/pc1/messages/m5').single.url.queryParameters['for'], 'everyone');
    expect(find.text('Bring the cake'), findsNothing);
    expect(find.text('This message was deleted'), findsNWidgets(2));
    await drain(t);
  });

  testWidgets('forward: to at most five chats; one forwarded many times to one at a time', (t) async {
    phone(t);
    final api = adaChat();
    api.routes['GET /v1/me/people'] = (_) => {
      'chats': [for (var i = 0; i < 7; i++) peopleChat(id: 'f$i', name: 'Friend $i')],
      'unread': 0,
      'requests': 0, 'more': false,
    };
    api.routes['POST /v1/me/people/forward'] = (r) => {'sent': [for (final id in api.body(r)['to'] as List) {'chat_id': id, 'message': thread()[2]}], 'failed': <Object>[]};
    final d = await openPeople(t, api);
    final m = d.messages!.firstWhere((m) => m.id == 'm2').m;
    final words = d.forward(m, ['f1', 'f2']);
    expect(await t.runAsync(() => words), 'Forwarded to 2 chats');
    expect(api.body(api.sent('POST', '/v1/me/people/forward').single), {'chat_id': 'pc1', 'message_id': 'm2', 'to': ['f1', 'f2']});

    // The picker: five at most.
    unawaited(pickPeopleChats(t.element(find.byType(PeopleChatScreen)), d.auth, title: 'Forward to', what: 'Hi'));
    await settle(t);
    for (var i = 0; i < 6; i++) {
      await press(t, find.text('Friend $i'));
    }
    expect(find.text('At most 5 chats at once.'), findsOneWidget);
    await drain(t);
  });

  testWidgets('a deleted account: "Deleted account", read only', (t) async {
    phone(t);
    final api = adaChat(c: peopleChat(name: 'Deleted account', gone: true));
    await openPeople(t, api);
    expect(find.text('Deleted account'), findsWidgets);
    expect(find.text('This account was deleted. You can read the chat, but not write to it.'), findsOneWidget);
    expect(find.bySemanticsLabel('Message Deleted account'), findsNothing);
    await drain(t);
  });

  testWidgets('a request: opened and marked read (the sender is told nothing); Accept', (t) async {
    phone(t);
    final api = adaChat(c: peopleChat(request: true), messages: [thread()[1]..remove('reply_to')]);
    api.routes['POST /v1/me/people/pc1/accept'] = (_) => peopleChat();
    await openPeople(t, api);
    expect(find.text('Message request'), findsOneWidget);
    expect(find.textContaining('not told you read this'), findsOneWidget);
    // Marked read as any chat: the server tells the sender nothing of a
    // request until it is accepted (TestPeopleReadTicksEverywhere).
    expect(api.sent('POST', '/v1/me/people/pc1/read'), hasLength(1), reason: 'an opened request is no longer unread');
    expect(find.bySemanticsLabel('Message Ada Obi'), findsNothing, reason: 'no box until accepted');
    await press(t, find.text('Accept'));
    expect(api.sent('POST', '/v1/me/people/pc1/accept'), hasLength(1));
    expect(find.bySemanticsLabel('Message Ada Obi'), findsOneWidget);
    await drain(t);
  });

  testWidgets('a request: Block and report sends the report with consent; Delete asks first', (t) async {
    phone(t);
    final api = adaChat(c: peopleChat(request: true));
    api.routes['POST /v1/me/people/pc1/block'] = (_) => {'blocked': true, 'removed': true, 'reported': true};
    api.routes['POST /v1/me/people/pc1/decline'] = (_) => answer({}, 204);
    await openPeople(t, api);
    await press(t, find.text('Block and report'));
    expect(find.textContaining('last 20 messages'), findsOneWidget);
    await press(t, find.text('A scam or fraud'));
    await press(t, find.bySemanticsLabel('Block and report').last);
    expect(find.text('Agree to send the last 20 messages, so the moderators can see what happened.'), findsOneWidget);
    await press(t, find.textContaining('Send the last 20 messages'));
    await press(t, find.bySemanticsLabel('Block and report').last);
    final b = api.body(api.sent('POST', '/v1/me/people/pc1/block').single);
    expect(b, {'report': {'reason': 'scam', 'consent': true}});
    await drain(t);
  });

  testWidgets('a request: Delete asks first, and the sender is not told', (t) async {
    phone(t);
    final api = adaChat(c: peopleChat(request: true));
    api.routes['POST /v1/me/people/pc1/decline'] = (_) => answer({}, 204);
    await openPeople(t, api);
    await press(t, find.text('Delete'));
    expect(find.text('Delete this request?'), findsOneWidget);
    expect(find.textContaining('is not told'), findsOneWidget);
    await press(t, find.text('Delete').last);
    expect(api.sent('POST', '/v1/me/people/pc1/decline'), hasLength(1));
    await drain(t);
  });

  testWidgets('about the person: the name you gave, their number only as shown, and where the chat is kept', (t) async {
    phone(t);
    final api = adaChat();
    await openPeople(t, api);
    await press(t, find.bySemanticsLabel(RegExp('^About Ada Obi')));
    expect(find.text('+234 803 123 4567'), findsOneWidget);
    expect(find.text(peopleKeptWords), findsOneWidget);
    expect(find.text('Block Ada Obi'), findsOneWidget);
    await drain(t);

    final hidden = adaChat(c: peopleChat());
    await openPeople(t, hidden);
    await press(t, find.bySemanticsLabel(RegExp('^About Ada Obi')));
    expect(find.textContaining('803'), findsNothing, reason: 'the number shows only when they chose to show it');
    await drain(t);
  });

  testWidgets('sharing into a person’s chat: a business you chat with, a product of its shop, one of your orders', (t) async {
    phone(t);
    final api = adaChat();
    api.routes['GET /v1/me/chats'] = (_) => {
      'chats': [
        {'id': 'c1', 'business': 'b1', 'name': 'Mama’s Kitchen', 'last': '', 'last_at': '2026-11-16T10:00:00Z', 'last_mine': false, 'unread': 0, 'muted': false, 'archived': false, 'blocked': false, 'blocked_you': false, 'offers': 'no', 'ask_offers': false},
      ],
      'unread': 0,
    };
    api.routes['GET /v1/me/chats/c1/shop'] = (_) => {
      'items': [{'id': 'i1', 'name': 'Jollof rice', 'available': true, 'price_kobo': 250000}],
      'open': true, 'delivery': false, 'pickup': true, 'payment': 'transfer', 'zones': <Object>[],
    };
    api.routes['GET /v1/me/orders'] = (_) => {
      'orders': [
        {'business': 'Mama’s Kitchen', 'business_id': 'b1', 'reference': 'MK-1042', 'kind': 'order', 'state': 'sent', 'summary': '2 × Jollof rice', 'total_kobo': 500000, 'due_kobo': 0, 'pay': false, 'made_at': '2026-11-16T10:00:00Z'},
      ],
      'waiting_count': 0,
    };
    final d = await openPeople(t, api);
    final context = t.element(find.byType(PeopleChatScreen));
    SendPeopleMessageRequestCard? got;
    unawaited(pickShareCard(context, d.auth, PeopleShare.business).then((c) => got = c));
    await settle(t);
    await press(t, find.text('Mama’s Kitchen'));
    expect(got?.toJson(), {'type': 'business', 'business_id': 'b1'});

    unawaited(pickShareCard(context, d.auth, PeopleShare.product).then((c) => got = c));
    await settle(t);
    await press(t, find.text('Mama’s Kitchen'));
    await press(t, find.text('Jollof rice'));
    expect(got?.toJson(), {'type': 'product', 'business_id': 'b1', 'item_id': 'i1'});

    unawaited(pickShareCard(context, d.auth, PeopleShare.order).then((c) => got = c));
    await settle(t);
    await press(t, find.textContaining('MK-1042'));
    expect(got?.toJson(), {'type': 'order', 'business_id': 'b1', 'reference': 'MK-1042'});
    await drain(t);
  });

  testWidgets('Requests: listed, opened, answered', (t) async {
    phone(t);
    final api = adaChat(c: peopleChat(request: true));
    api.routes['GET /v1/me/people/requests'] = (_) => {'requests': [peopleChat(request: true, last: 'Hello, I got your number from Bisi')]};
    final opened = <String>[];
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleRequestsScreen(auth: api.auth(), now: () => at, openChat: (id, {started}) async => opened.add(id))));
    await settle(t);
    expect(find.text('Hello, I got your number from Bisi'), findsOneWidget);
    await press(t, find.text('Ada Obi'));
    expect(opened, ['pc1']);
    await drain(t);
  });

  testWidgets('New chat: by number, found and started; not on Taskiem, Invite', (t) async {
    phone(t);
    final api = Api();
    api.routes['POST /v1/people/lookup'] = (r) => api.body(r)['phone'] == '+2348031234567'
        ? {'found': true, 'person': card(inContacts: false), 'add_token': 'tok'}
        : {'found': false, 'invite': {'words': 'Chat with me on Taskiem: https://taskiem.app/p/k7m2', 'link': 'https://taskiem.app/p/k7m2'}};
    api.routes['POST /v1/me/people'] = (_) => answer(peopleChat(), 201);
    api.routes['POST /v1/me/contacts'] = (_) => answer({...card(), 'source': 'number', 'added_at': '2026-11-17T10:00:00Z'}, 201);
    final opened = <String>[];
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: NewPeopleChatScreen(auth: api.auth(), openChat: (id, {started}) async => opened.add(id), contacts: FakeContacts(const []))));
    await settle(t);
    await typeIn(t, find.byKey(const ValueKey('find-phone')), '0803 123 4567');
    await press(t, find.text('Find'));
    expect(find.text('Ada Obi'), findsOneWidget);
    await press(t, find.text('Add to contacts'));
    // Their name in your contacts is asked first, as the web does.
    expect(find.text('Their name in your contacts'), findsOneWidget);
    expect(api.sent('POST', '/v1/me/contacts'), isEmpty);
    await t.enterText(find.byType(EditableText).last, 'Ada from church');
    await t.pump();
    await press(t, find.text('Save'));
    expect(api.body(api.sent('POST', '/v1/me/contacts').single), {'person_id': 'p1', 'add_token': 'tok', 'from': 'number', 'name': 'Ada from church'});
    await press(t, find.text('Chat'));
    expect(api.body(api.sent('POST', '/v1/me/people').single), {'person_id': 'p1'});
    expect(opened, ['pc1']);

    await typeIn(t, find.byKey(const ValueKey('find-phone')), '0809 000 0000');
    await press(t, find.text('Find'));
    expect(find.text('Not on Taskiem yet'), findsOneWidget);
    expect(find.text('Invite'), findsOneWidget);
    await drain(t);
  });

  testWidgets('a person’s link: who it is, then a chat by the link; a replaced link says so', (t) async {
    phone(t);
    final api = Api();
    api.routes['GET /v1/people/links/k7m2q9x4pz'] = (_) => {'found': true, 'person': card(inContacts: false), 'add_token': 'tok'};
    api.routes['GET /v1/people/links/old0000'] = (_) => answer({'error': 'gone', 'gone': true}, 410);
    api.routes['POST /v1/me/people'] = (_) => answer(peopleChat(), 201);
    final opened = <String>[];
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PersonLinkScreen(auth: api.auth(), code: 'k7m2q9x4pz', openChat: (id, {started}) async => opened.add(id))));
    await settle(t);
    expect(find.text('Ada Obi'), findsOneWidget);
    await press(t, find.text('Chat'));
    expect(api.body(api.sent('POST', '/v1/me/people').single), {'link_code': 'k7m2q9x4pz'});
    expect(opened, ['pc1']);

    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PersonLinkScreen(key: const ValueKey('old'), auth: api.auth(), code: 'old0000', openChat: (id, {started}) async {})));
    await settle(t);
    expect(find.text('This link no longer works. Ask them for their new link.'), findsOneWidget);
    await drain(t);
  });

  testWidgets('your link: shown with its QR code, shared, replaced', (t) async {
    phone(t);
    final api = Api();
    var n = 0;
    Map<String, dynamic> link() => {'code': 'code$n', 'url': 'https://taskiem.app/p/code$n', 'created_at': '2026-11-17T10:00:00Z', 'words': 'Chat with me on Taskiem.', 'qr': '/v1/me/link/qr'};
    api.routes['GET /v1/me/link'] = (_) => link();
    api.routes['POST /v1/me/link'] = (_) {
      n++;
      return link();
    };
    api.routes['GET /v1/me/link/qr'] = (_) => http.Response.bytes(base64.decode(_png), 200, headers: {'content-type': 'image/png'});
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: MyPersonLinkScreen(auth: api.auth())));
    await settle(t);
    expect(find.text('https://taskiem.app/p/code0'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('Your QR code')), findsOneWidget);
    expect(api.sent('GET', '/v1/me/link/qr').single.url.queryParameters['format'], 'png');
    await press(t, find.text('Replace the link'));
    await press(t, find.text('Replace'));
    expect(api.sent('POST', '/v1/me/link'), hasLength(1));
    expect(find.text('https://taskiem.app/p/code1'), findsOneWidget);
    await drain(t);
  });

  testWidgets('from your contacts: nothing read before Agree; then hashed, matched, added', (t) async {
    phone(t);
    final api = Api();
    api.routes['GET /v1/me/contacts/key'] = (_) => {'key_id': 'k1', 'key': vectorKey, 'expires_at': '2026-11-20T00:00:00Z', 'hash': 'sha256', 'max_batch': 500, 'max_per_day': 5000, 'words': ''};
    final ada = contactHash(base64.decode(vectorKey), '+2348031234567');
    api.routes['POST /v1/me/contacts/match'] = (_) => {'matches': [{'hash': ada, 'person': card(inContacts: false), 'add_token': 'tok'}]};
    api.routes['POST /v1/me/contacts'] = (_) => answer({...card(), 'source': 'contacts', 'added_at': '2026-11-17T10:00:00Z'}, 201);
    final contacts = FakeContacts([(name: 'Ada (school)', numbers: ['08031234567'])]);
    final chats = <String>[];
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ContactsMatchScreen(auth: api.auth(), contacts: contacts, onChat: (p) async => chats.add(p))));
    await settle(t);
    expect(find.text(contactsAskTitle), findsOneWidget);
    expect(find.textContaining('not from Taskiem itself'), findsOneWidget, reason: 'the limit said honestly');
    expect(find.textContaining('the name you give them are kept'), findsOneWidget, reason: 'the explainer says what Add keeps');
    expect(contacts.asked, isFalse);
    expect(contacts.read_, isFalse);
    expect(api.calls, isEmpty, reason: 'nothing is sent before the person agrees');

    await press(t, find.text('Agree and check my contacts'));
    expect(contacts.asked && contacts.read_, isTrue);
    expect(api.body(api.sent('POST', '/v1/me/contacts/match').single), {'key_id': 'k1', 'hashes': [ada]});
    expect(find.text('Ada (school)'), findsOneWidget);
    expect(find.text('On Taskiem as Ada Obi'), findsOneWidget);
    await press(t, find.text('Add'));
    // The name kept is the one given: the phone's, unless changed.
    expect(find.text('Their name in your contacts'), findsOneWidget);
    await press(t, find.text('Save'));
    expect(api.body(api.sent('POST', '/v1/me/contacts').single), {'person_id': 'p1', 'add_token': 'tok', 'from': 'contacts', 'name': 'Ada (school)'});
    await press(t, find.text('Chat'));
    expect(chats, ['p1']);
    await drain(t);
  });

  testWidgets('from your contacts: the phone says no, nothing is read or sent', (t) async {
    phone(t);
    final api = Api();
    final contacts = FakeContacts([(name: 'Ada', numbers: ['08031234567'])], allow: false);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ContactsMatchScreen(auth: api.auth(), contacts: contacts, onChat: (p) async {})));
    await settle(t);
    await press(t, find.text('Agree and check my contacts'));
    expect(find.text('Contacts not allowed'), findsOneWidget);
    expect(contacts.read_, isFalse);
    expect(api.calls, isEmpty);
    await drain(t);
  });

  testWidgets('your contacts: listed by the name you gave, renamed, removed', (t) async {
    phone(t);
    final api = Api();
    var name = 'Ada from church';
    api.routes['GET /v1/me/contacts'] = (_) => {'contacts': [{...card(contactName: name), 'source': 'number', 'added_at': '2026-11-17T10:00:00Z'}]};
    api.routes['PUT /v1/me/contacts/p1'] = (r) {
      name = api.body(r)['name'] as String;
      return {...card(contactName: name), 'source': 'number', 'added_at': '2026-11-17T10:00:00Z'};
    };
    api.routes['DELETE /v1/me/contacts/p1'] = (_) => answer({}, 204);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleContactsScreen(auth: api.auth(), onChat: (p) async {})));
    await settle(t);
    expect(find.text('Ada from church'), findsOneWidget);
    await press(t, find.text('Ada from church'));
    await press(t, find.text('Rename'));
    await t.enterText(find.byType(EditableText).last, 'Ada O.');
    await t.pump();
    await press(t, find.text('Save'));
    expect(api.body(api.sent('PUT', '/v1/me/contacts/p1').single), {'name': 'Ada O.'});
    expect(find.text('Ada O.'), findsOneWidget);
    await press(t, find.text('Ada O.'));
    await press(t, find.text('Remove from contacts'));
    await press(t, find.text('Remove'));
    expect(api.sent('DELETE', '/v1/me/contacts/p1'), hasLength(1));
    await drain(t);
  });

  testWidgets('Privacy with people: each setting put at once, put back if refused', (t) async {
    phone(t);
    final api = Api();
    var refuse = false;
    final p = <String, dynamic>{'find_by_number': 'contacts', 'find_from_contacts': true, 'message': 'everyone', 'last_seen': 'contacts', 'read_ticks': true, 'photo': 'contacts', 'show_number': false};
    api.routes['GET /v1/me/privacy/people'] = (_) => p;
    api.routes['PUT /v1/me/privacy/people'] = (r) {
      if (refuse) return answer({'error': 'Something went wrong. Try again.'}, 500);
      p.addAll(api.body(r));
      return p;
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeoplePrivacyPage(auth: api.auth())));
    await settle(t);
    expect(find.text('Everyone, as a request'), findsOneWidget);
    await press(t, find.text('Everyone, as a request'));
    await press(t, find.text('Nobody').last);
    expect(api.body(api.sent('PUT', '/v1/me/privacy/people').single), {'message': 'nobody'});
    await press(t, find.bySemanticsLabel('Read ticks').last);
    expect(api.body(api.sent('PUT', '/v1/me/privacy/people').last), {'read_ticks': false});
    refuse = true;
    await press(t, find.bySemanticsLabel('Show my number').last);
    expect(find.text('Something went wrong. Try again.'), findsOneWidget);
    expect(find.text(peopleKeptWords), findsOneWidget);
    await drain(t);
  });

  testWidgets('blocked people: listed, unblocked', (t) async {
    phone(t);
    final api = Api();
    var blocked = true;
    api.routes['GET /v1/me/people/blocked'] = (_) => {'people': [if (blocked) {'person_id': 'p1', 'name': 'Chidi', 'chat_id': 'pc7', 'at': '2026-11-16T10:00:00Z'}]};
    api.routes['DELETE /v1/me/people/pc7/block'] = (_) {
      blocked = false;
      return {'blocked': false};
    };
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: BlockedPeopleScreen(auth: api.auth(), now: () => at)));
    await settle(t);
    expect(find.text('Chidi'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Unblock Chidi'));
    await press(t, find.text('Unblock').last);
    expect(api.sent('DELETE', '/v1/me/people/pc7/block'), hasLength(1));
    expect(find.text('No one blocked'), findsOneWidget);
    await drain(t);
  });

  testWidgets('cards: a product opened live through the door; your share with the payer’s account and Mark as paid', (t) async {
    phone(t);
    final share = {
      'id': 's1',
      'mine': false,
      'kind': 'card',
      'body': 'Your share: ₦3,000',
      'card': {'type': 'split', 'title': 'Your share: ₦3,000', 'amount_kobo': 300000, 'what': 'the cake', 'pay_to': {'bank': 'GTBank', 'account_number': '0123456789', 'account_name': 'Ada Obi'}, 'paid': false},
      'read_at': null,
      'created_at': '2026-11-17T09:00:00Z',
    };
    final product = {
      'id': 'k1',
      'mine': false,
      'kind': 'card',
      'body': 'Jollof rice',
      'card': {'type': 'product', 'title': 'Jollof rice', 'business': 'Mama’s Kitchen', 'business_id': 'b1', 'item_id': 'i1', 'price_kobo': 250000},
      'read_at': null,
      'created_at': '2026-11-17T09:01:00Z',
    };
    final api = adaChat(messages: [share, product]);
    api.routes['POST /v1/me/people/pc1/messages/s1/paid'] = (r) => {...share, 'card': {...share['card']! as Map, 'paid': api.body(r)['paid'], 'paid_by_me': true}};
    api.routes['GET /v1/me/people/pc1/messages/k1/card'] = (_) => {
      'card': product['card'],
      'live': {'yours': false, 'product': {'id': 'i1', 'name': 'Jollof rice', 'available': true, 'price_kobo': 280000, 'few_left': 3}},
    };
    await openPeople(t, api);
    expect(find.text('₦3,000'), findsOneWidget);
    expect(find.text('GTBank · 0123456789 · Ada Obi'), findsOneWidget);
    expect(find.text('Not paid yet'), findsOneWidget);
    await press(t, find.text('Mark as paid'));
    expect(api.body(api.sent('POST', '/v1/me/people/pc1/messages/s1/paid').single), {'paid': true});
    expect(find.text('Paid'), findsOneWidget);

    await press(t, find.text('Open'));
    expect(find.text('₦2,800 now (it was ₦2,500 when shared)'), findsOneWidget);
    expect(find.text('Only 3 left'), findsOneWidget);
    expect(find.text('Order from their shop'), findsOneWidget);
    await drain(t);
  });

  testWidgets('your share: sent with a typed account; the bank account for shares saved', (t) async {
    phone(t);
    final api = adaChat();
    api.routes['GET /v1/me/payout-account'] = (_) => {'account': null, 'checked': false, 'words': 'Taskiem does not check this account.'};
    api.routes['PUT /v1/me/payout-account'] = (r) => {'account': api.body(r), 'checked': false, 'words': 'Taskiem does not check this account.'};
    api.routes['POST /v1/me/people/pc1/messages'] = (r) => answer({
      'id': 's2', 'mine': true, 'kind': 'card', 'body': '', 'card': {'type': 'split', 'title': 'Your share: ₦1,500', ...api.body(r)['card'] as Map}, 'status': 'sent', 'read_at': null, 'created_at': '2026-11-17T10:29:00Z',
    }, 201);
    final d = await openPeople(t, api);
    unawaited(showSplitForm(t.element(find.byType(PeopleChatScreen)), d.auth).then((c) async {
      if (c != null) await d.sendCard(c);
    }));
    await settle(t);
    await typeIn(t, find.bySemanticsLabel('Their share'), '1,500');
    await typeIn(t, find.bySemanticsLabel('What it is for'), 'Suya');
    await press(t, find.bySemanticsLabel('Send').last);
    expect(find.text('Write the bank, the 10-digit account number and the name on the account.'), findsOneWidget);
    await typeIn(t, find.bySemanticsLabel('Bank'), 'GTBank');
    await typeIn(t, find.bySemanticsLabel('Account number'), '0123456789');
    await typeIn(t, find.bySemanticsLabel('Name on the account'), 'Tolu Ade');
    await press(t, find.bySemanticsLabel('Send').last);
    expect(api.body(api.sent('POST', '/v1/me/people/pc1/messages').single)['card'], {
      'type': 'split', 'amount_kobo': 150000, 'what': 'Suya', 'pay_to': {'bank': 'GTBank', 'account_number': '0123456789', 'account_name': 'Tolu Ade'},
    });

    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PayoutAccountPage(auth: api.auth())));
    await settle(t);
    await typeIn(t, find.bySemanticsLabel('Bank'), 'Opay');
    await typeIn(t, find.bySemanticsLabel('Account number'), '8031230000');
    await typeIn(t, find.bySemanticsLabel('Name on the account'), 'Tolu Ade');
    await press(t, find.text('Save'));
    expect(api.body(api.sent('PUT', '/v1/me/payout-account').single), {'bank': 'Opay', 'account_number': '8031230000', 'account_name': 'Tolu Ade'});
    expect(find.text('Remove it'), findsOneWidget);
    await drain(t);
  });

  testWidgets('a link to a person’s chat, and to a request, opens it from You (as a push’s does)', (t) async {
    phone(t);
    final api = adaChat(c: peopleChat(request: true));
    api.routes['GET /v1/me/conversations'] = (_) => {'chats': <Object>[], 'unread': 0, 'requests': 1, 'more': false};
    api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
    api.routes['GET /v1/me/people/requests'] = (_) => {'requests': [peopleChat(request: true)]};
    final links = StreamController<Uri>();
    final inbox = LinkInbox(links: links.stream, first: () async => null);
    addTearDown(() {
      inbox.dispose();
      links.close();
    });
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), links: inbox, now: () => at, presence: false)));
    await settle(t);
    links.add(Uri.parse('$taskiemUrl/you/people/requests/pc1'));
    await settle(t);
    await settle(t);
    expect(find.text('Accept a chat with Ada Obi?'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Back to the chats'));
    expect(find.text('Message requests'), findsWidgets, reason: 'Requests under the request');

    api.routes['GET /v1/me/people/pc1'] = (_) => peopleChat();
    links.add(Uri.parse('$taskiemUrl/you/people/pc1'));
    await settle(t);
    await settle(t);
    expect(find.text('Are you coming tonight?'), findsWidgets);
    await drain(t);
  });

  testWidgets('presence: here on opening, and every 30 seconds', (t) async {
    phone(t);
    final api = Api();
    api.routes['POST /v1/me/presence'] = (_) => answer({}, 204);
    api.routes['GET /v1/me/conversations'] = (_) => {'chats': <Object>[], 'unread': 0, 'requests': 0, 'more': false};
    api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => at)));
    await settle(t);
    expect(api.sent('POST', '/v1/me/presence').map(api.body), [{'online': true}]);
    await t.pump(const Duration(seconds: 31));
    await settle(t);
    expect(api.sent('POST', '/v1/me/presence').length, 2);
    await t.pumpWidget(const SizedBox());
    await settle(t);
    expect(api.body(api.sent('POST', '/v1/me/presence').last), {'online': false}, reason: 'away when You goes');
    await t.pump(const Duration(seconds: 5));
  });
}
