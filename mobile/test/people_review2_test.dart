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

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/chat/live.dart' show LiveTiming;
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart' show MemoryKeyStore;
import 'package:taskiem/chat/people_contacts.dart';
import 'package:taskiem/chat/people_settings.dart';
import 'package:taskiem/chat/people_find.dart' show PeopleRequestsScreen;
import 'package:taskiem/screens/you.dart' show YouHome;
import 'package:taskiem/chat/people_search.dart' show PeopleSearchScreen;
import 'package:taskiem/kit/load_more.dart' show TkLoadMore;
import 'package:taskiem/chat/attachments.dart' show PickedFile;
import 'package:taskiem/chat/composer.dart' show Pickers;
import 'package:taskiem/chat/people_chat.dart';
import 'package:taskiem/chat/people_live.dart';
import 'package:taskiem/chat/pick_problem.dart';
import 'package:taskiem/chat/product_form.dart' show PhotoAdd;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'people_test.dart';
import 'support.dart' show loadFonts;

Map<String, dynamic> msg(String id, {bool mine = false, String body = 'Hello', String at = '2026-11-17T10:25:00Z', Map<String, dynamic> extra = const {}}) => {
  'id': id, 'mine': mine, 'kind': 'text', 'body': body, 'read_at': null, 'created_at': at, if (mine) 'status': 'sent', ...extra,
};

void hear(PeopleLive live, String event, Object data, {String? id}) => live.hearEvent((event: event, data: jsonEncode(data), id: id));

/// A stand-in live connection: each open is a controller the test writes
/// to.
class Streams {
  final opens = <StreamController<List<int>>>[];

  http.Client client() => MockClient.streaming((req, _) async {
    final c = StreamController<List<int>>();
    opens.add(c);
    return http.StreamedResponse(c.stream, 200, headers: {'content-type': 'text/event-stream'});
  });

  void raw(String text) => opens.last.add(utf8.encode(text));
  void say(String event, Object data, {String? id}) => raw('event: $event\ndata: ${jsonEncode(data)}\n${id == null ? '' : 'id: $id\n'}\n');
}

/// The phone's contacts, allowed without asking.
class AllowedContacts extends FakeContacts {
  AllowedContacts(super.list);
  @override
  Future<bool> allowed() async => true;
}

/// A dropped connection's error.
class SocketExceptionLike implements Exception {
  const SocketExceptionLike();
}

Future<void> wait(int ms) => Future<void>.delayed(Duration(milliseconds: ms));

/// The phone refusing the camera and photos (as image_picker throws when
/// CAMERA is declared and the person says no), or with no camera.
class RefusedPickers extends Pickers {
  const RefusedPickers({this.code = 'camera_access_denied'});
  final String code;
  @override
  Future<PickedFile?> camera({double? side}) async => throw PlatformException(code: code);
  @override
  Future<PickedFile?> photo({double? side}) async => throw PlatformException(code: 'photo_access_denied');
  @override
  Future<List<PickedFile>> photos(int limit, {double? side}) async => throw PlatformException(code: 'photo_access_denied');
}

/// How often the phone's settings were opened.
class SettingsSpy {
  int opened = 0;
}

SettingsSpy spySettings() {
  final s = SettingsSpy();
  final was = openPhoneSettings;
  openPhoneSettings = () async => s.opened++;
  addTearDown(() => openPhoneSettings = was);
  return s;
}

const cameraRefused = 'Taskiem isn’t allowed to use your camera. To take a photo, allow Camera for Taskiem in your phone’s settings.';
const photosRefused = 'Taskiem isn’t allowed to see your photos. To choose one, allow Photos for Taskiem in your phone’s settings.';

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

  group('2. the camera or photos refused', () {
    test('what the picker throws, in plain words', () {
      expect(pickProblemOf(PlatformException(code: 'camera_access_denied')), PickProblem.refused);
      expect(pickProblemOf(PlatformException(code: 'photo_access_denied')), PickProblem.refused);
      expect(pickProblemOf(PlatformException(code: 'no_available_camera')), PickProblem.noCamera);
      expect(pickProblemOf(StateError('x')), PickProblem.failed);
      expect(pickProblemWords(PickProblem.refused), cameraRefused);
      expect(pickProblemWords(PickProblem.refused, camera: false), photosRefused);
    });

    testWidgets('a people chat: the camera button and + A photo say why, with Open settings; nothing sent', (t) async {
      phone(t);
      final spy = spySettings();
      final api = adaChat();
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d, pickers: const RefusedPickers())));
      await settle(t);
      await press(t, find.bySemanticsLabel('Take a photo'));
      expect(find.text(cameraRefused), findsOneWidget);
      await t.tap(find.text('Open settings'));
      await t.pump();
      expect(spy.opened, 1);
      expect(find.text(cameraRefused), findsNothing);

      await press(t, find.bySemanticsLabel('Chat tools'));
      await press(t, find.text('A photo'));
      expect(find.text(photosRefused), findsOneWidget);
      expect(api.calls.where((c) => c.method == 'POST' && c.url.path.endsWith('/files')), isEmpty);
      await t.tap(find.bySemanticsLabel('Dismiss'));
      await drain(t);
    });

    testWidgets('no camera on the phone: said, without settings', (t) async {
      phone(t);
      spySettings();
      final api = adaChat();
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d, pickers: const RefusedPickers(code: 'no_available_camera'))));
      await settle(t);
      await press(t, find.bySemanticsLabel('Take a photo'));
      expect(find.text(pickProblemWords(PickProblem.noCamera)), findsOneWidget);
      expect(find.text('Open settings'), findsNothing);
      await t.tap(find.bySemanticsLabel('Dismiss'));
      await drain(t);
    });

    testWidgets('a product’s photos: Take a photo refused says why, with Open settings', (t) async {
      phone(t);
      final spy = spySettings();
      var picked = 0;
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: Center(child: PhotoAdd(pickers: const RefusedPickers(), room: 3, onPick: (_) => picked++))));
      await t.pumpAndSettle();
      await t.tap(find.bySemanticsLabel('Take a photo'));
      await settle(t);
      expect(find.text(cameraRefused), findsOneWidget);
      await t.tap(find.text('Open settings'));
      await t.pump();
      expect(spy.opened, 1);
      await t.tap(find.bySemanticsLabel('From your phone'));
      await settle(t);
      expect(find.text(photosRefused), findsOneWidget);
      expect(picked, 0);
      await t.tap(find.bySemanticsLabel('Dismiss'));
      await drain(t);
    });
  });

  group('3. the live connection ended or dropped', () {
    test('ended cleanly by the server (after 30 minutes): opened again at once; an error waits the server’s retry, doubled while errors go on', () async {
      final s = Streams();
      // The longest wait (timing.retry) is long: only the server's retry
      // and the jitter can explain a quick reopen.
      final live = PeopleLive(Transport(base: 'http://localhost', token: 't', client: s.client()), timing: const LiveTiming(retry: Duration(seconds: 30)));
      live.start();
      await pumpEventQueue();
      s.raw('retry: 100\n\n');
      s.say('ready', {'connection': 'c1'});
      await pumpEventQueue();
      expect(live.connected, isTrue);
      await s.opens.last.close();
      await wait(PeopleLive.jitter.inMilliseconds + 100);
      expect(s.opens, hasLength(2), reason: 'a clean end reopens within the jitter, not after 30 s');

      s.say('ready', {'connection': 'c2'});
      await pumpEventQueue();
      s.opens.last.addError(const SocketExceptionLike());
      await wait(40);
      expect(s.opens, hasLength(2), reason: 'an error waits the server’s retry (100 ms here)');
      await wait(150);
      expect(s.opens, hasLength(3));
      s.opens.last.addError(const SocketExceptionLike());
      await wait(150);
      expect(s.opens, hasLength(3), reason: 'the second error in a row waits twice as long');
      await wait(150);
      expect(s.opens, hasLength(4));
      live.stop();
      live.dispose();
    });

    test('a connection that ends before it is ready is taken as an error (no loop)', () async {
      final s = Streams();
      final live = PeopleLive(Transport(base: 'http://localhost', token: 't', client: s.client()), timing: const LiveTiming(retry: Duration(seconds: 30)));
      live.start();
      await pumpEventQueue();
      await s.opens.last.close();
      await wait(PeopleLive.jitter.inMilliseconds + 100);
      expect(s.opens, hasLength(1));
      live.stop();
      live.dispose();
    });
  });

  group('4. presence from the connection', () {
    test('forgotten when it closes, and used only while connected', () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      final api = adaChat();
      final live = PeopleLive(api.auth().transport);
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, live: live);
      d.seen = () => false;
      await d.open();
      expect(d.person?.online, isFalse);
      hear(live, 'presence', {'people': [{'person_id': 'p1', 'chat_id': 'pc1', 'online': true, 'last_seen_at': null}]});
      expect(live.presenceOf('pc1'), isNull, reason: 'not connected: not taken');
      hear(live, 'ready', {'connection': 'c1'});
      hear(live, 'presence', {'people': [{'person_id': 'p1', 'chat_id': 'pc1', 'online': true, 'last_seen_at': null}]});
      expect(d.person?.online, isTrue);
      live.stop();
      expect(live.presence, isEmpty);
      expect(live.presenceOf('pc1'), isNull);
      expect(d.person?.online, isFalse, reason: 'the chat’s own read again, not a stale online');
      d.dispose();
      live.dispose();
    });
  });

  group('5. the daily re-check goes round the numbers', () {
    test('more numbers than the day allows: each day starts further round them, and over the days all are checked', () {
      final all = ['+2348030000005', '+2348030000001', '+2348030000004', '+2348030000002', '+2348030000003'];
      expect(dayShare(all, 0, '2026-11-18'), all);
      expect(dayShare(all, 9, '2026-11-18'), all);
      expect(dayShare(all, 2, null), all.take(2), reason: 'without a day, as the phone has them');
      final seen = <String>{};
      final days = ['2026-11-18', '2026-11-19', '2026-11-20'];
      final shares = [for (final d in days) dayShare(all, 2, d)];
      for (final share in shares) {
        expect(share, hasLength(2));
        seen.addAll(share);
      }
      expect(shares[0], isNot(shares[1]));
      expect(seen, all.toSet(), reason: 'three days of two go round all five');
      expect(dayShare(all, 2, '2026-11-18'), shares[0], reason: 'the same day, the same share');
    });

    test('recheckContacts sends a different share on the next day; only the day is kept', () async {
      final api = Api();
      api.routes['GET /v1/me/contacts/key'] = (_) => {'key_id': 'k1', 'key': vectorKey, 'expires_at': '2026-11-20T00:00:00Z', 'hash': 'sha256', 'max_batch': 500, 'max_per_day': 1, 'words': ''};
      api.routes['POST /v1/me/contacts/match'] = (_) => {'matches': <Object>[]};
      final c = AllowedContacts([(name: 'Ada', numbers: ['0803 123 4567']), (name: 'Bayo', numbers: ['0805 765 4321'])]);
      final kept = <String>[];
      await recheckContacts(api: api.auth().api, contacts: c, checked: '2026-11-16', now: DateTime.utc(2026, 11, 17, 10), remember: (d) async => kept.add(d));
      await recheckContacts(api: api.auth().api, contacts: c, checked: '2026-11-17', now: DateTime.utc(2026, 11, 18, 10), remember: (d) async => kept.add(d));
      final sent = [for (final r in api.sent('POST', '/v1/me/contacts/match')) (api.body(r)['hashes'] as List).single];
      expect(sent, hasLength(2));
      expect(sent[0], isNot(sent[1]), reason: 'not the same first number every day');
      expect(kept, ['2026-11-17', '2026-11-18']);
    });
  });

  group('6. Check my contacts daily', () {
    Api privacy() {
      final api = Api();
      api.routes['GET /v1/me/privacy/people'] = (_) => {'find_by_number': 'contacts', 'find_from_contacts': true, 'message': 'everyone', 'last_seen': 'contacts', 'read_ticks': true, 'photo': 'contacts', 'show_number': false};
      return api;
    }

    testWidgets('on while the day is kept; turned off, the day is forgotten and nothing is checked by itself', (t) async {
      phone(t);
      final keys = MemoryKeyStore();
      await keys.write('contacts-checked', '2026-11-16');
      final prefs = Prefs(keys);
      await prefs.load();
      final api = privacy();
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeoplePrivacyPage(auth: api.auth(), prefs: prefs)));
      await settle(t);
      expect(find.text(checkContactsDaily), findsOneWidget);
      expect(find.text(checkContactsDailyOn), findsOneWidget);
      await press(t, find.bySemanticsLabel(checkContactsDaily).last);
      expect(prefs.contactsChecked, isNull);
      expect(await keys.read('contacts-checked'), isNull);
      expect(find.text(checkContactsDailyOff), findsOneWidget);
      expect(api.sent('PUT', '/v1/me/privacy/people'), isEmpty, reason: 'this phone’s, not the server’s');
      final c = AllowedContacts([(name: 'Ada', numbers: ['0803 123 4567'])]);
      expect(await recheckContacts(api: api.auth().api, contacts: c, checked: prefs.contactsChecked, now: at, remember: prefs.setContactsChecked), isNull);
      expect(c.read_, isFalse);
      await drain(t);
    });

    test('the explainer says how to turn it off', () {
      expect(contactsAskWords.join(' '), contains('Check my contacts daily in Privacy with people'));
    });
  });

  group('7. covered screens read once when on top again', () {
    /// A screen pushed over what is shown, and taken away.
    Future<void> cover(WidgetTester t) async {
      unawaited(t.state<NavigatorState>(find.byType(Navigator).first).push(PageRouteBuilder<void>(pageBuilder: (_, _, _) => const ColoredBox(color: Color(0xFFFFFFFF), child: SizedBox.expand()))));
      await settle(t);
    }

    Future<void> uncover(WidgetTester t) async {
      t.state<NavigatorState>(find.byType(Navigator).first).pop();
      await settle(t);
    }

    testWidgets('Requests: events while a request covers it read nothing; one read when it is on top', (t) async {
      phone(t);
      final api = Api();
      api.routes['GET /v1/me/people/requests'] = (_) => {'requests': <Object>[], 'more': false};
      final live = PeopleLive(api.auth().transport);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleRequestsScreen(auth: api.auth(), openChat: (id, {started}) async {}, now: () => at, live: live)));
      await settle(t);
      int reads() => api.sent('GET', '/v1/me/people/requests').length;
      final n = reads();
      await cover(t);
      for (var i = 0; i < 3; i++) {
        hear(live, 'message', {'chat_id': 'pc9', 'request': true, 'message': msg('r$i')});
        await settle(t);
      }
      expect(reads(), n, reason: 'covered: nothing read');
      await uncover(t);
      expect(reads(), n + 1, reason: 'on top again: read once');
      hear(live, 'message', {'chat_id': 'pc9', 'request': true, 'message': msg('r9')});
      await settle(t);
      expect(reads(), n + 2, reason: 'seen: read at once');
      await drain(t);
      live.dispose();
    });

    testWidgets('You’s Chats: events while a chat covers it read nothing; one read when it is on top', (t) async {
      phone(t);
      final api = adaChat();
      api.routes['GET /v1/me/conversations'] = (_) => {'chats': <Object>[], 'unread': 0, 'requests': 0, 'more': false};
      api.routes['GET /v1/me/invitations'] = (_) => {'invitations': <Object>[]};
      final live = PeopleLive(api.auth().transport);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: YouHome(auth: api.auth(), now: () => at, presence: false, live: live)));
      await settle(t);
      int reads() => api.sent('GET', '/v1/me/conversations').where((r) => r.url.queryParameters['archived'] == null).length;
      final n = reads();
      await cover(t);
      for (var i = 0; i < 3; i++) {
        hear(live, 'message', {'chat_id': 'pc1', 'request': false, 'message': msg('m$i')});
        await settle(t);
      }
      expect(reads(), n, reason: 'covered: nothing read');
      await uncover(t);
      expect(reads(), n + 1, reason: 'on top again: read once');
      await drain(t);
      live.dispose();
    });
  });

  group('8. search pages its own words', () {
    testWidgets('Show more pages the words searched, not new words typed but not yet searched', (t) async {
      phone(t, 700);
      final api = Api();
      api.routes['GET /v1/me/people/search'] = (r) => {
        // A long first page: Show more is far below, not read by itself.
        'results': [
          for (var i = 0; i < 30; i++) {'chat_id': 'pc1', 'chat': peopleChat(), 'message': msg('${r.url.queryParameters['before'] ?? 'p'}$i', body: 'cake')},
        ],
        'more': r.url.queryParameters['before'] == null,
        'next': 'n1',
      };
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleSearchScreen(auth: api.auth(), now: () => at, openAt: (c, m) async {}, wait: const Duration(seconds: 2))));
      await settle(t);
      await typeIn(t, find.byKey(const ValueKey('people-search')), 'cake');
      await t.pump(const Duration(seconds: 3));
      await settle(t);
      final firsts = api.sent('GET', '/v1/me/people/search').where((r) => r.url.queryParameters['before'] == null).toList();
      expect(firsts.map((r) => r.url.queryParameters['q']), ['cake']);
      await typeIn(t, find.byKey(const ValueKey('people-search')), 'bread');
      expect(api.sent('GET', '/v1/me/people/search').where((r) => r.url.queryParameters['before'] != null), isEmpty, reason: 'not yet in view');
      await t.runAsync(() => t.widget<TkLoadMore>(find.byType(TkLoadMore, skipOffstage: false).first).onMore());
      await t.pump();
      final pages = api.sent('GET', '/v1/me/people/search').where((r) => r.url.queryParameters['before'] == 'n1').toList();
      expect(pages, isNotEmpty);
      expect(pages.map((r) => r.url.queryParameters['q']).toSet(), {'cake'});
      await drain(t);
    });
  });

  group('9. typing', () {
    Api typingChat() {
      final api = adaChat();
      api.routes['POST /v1/me/people/pc1/typing'] = (_) => answer({}, 204);
      return api;
    }

    List<Object?> said(Api api) => [for (final r in api.sent('POST', '/v1/me/people/pc1/typing')) api.body(r)['typing']];
    Finder box() => find.bySemanticsLabel('Message Ada Obi');

    testWidgets('closed with words in the box: stopped', (t) async {
      phone(t);
      final api = typingChat();
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at))));
      await settle(t);
      await typeIn(t, box(), 'On my');
      await settle(t);
      expect(said(api), [true]);
      await t.pumpWidget(const SizedBox());
      await settle(t);
      expect(said(api), [true, false]);
    });

    testWidgets('put away with words in the box: stopped; back and typing, said again', (t) async {
      phone(t);
      final api = typingChat();
      var clock = DateTime.utc(2026, 11, 17, 12);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, clock: () => clock))));
      await settle(t);
      await typeIn(t, box(), 'On my');
      await settle(t);
      for (final s in [AppLifecycleState.inactive, AppLifecycleState.hidden, AppLifecycleState.paused]) {
        t.binding.handleAppLifecycleStateChanged(s);
      }
      await settle(t);
      expect(said(api), [true, false]);
      for (final s in [AppLifecycleState.hidden, AppLifecycleState.inactive, AppLifecycleState.resumed]) {
        t.binding.handleAppLifecycleStateChanged(s);
      }
      clock = clock.add(const Duration(seconds: 1));
      await typeIn(t, box(), 'On my way');
      await settle(t);
      expect(said(api), [true, false, true]);
      await drain(t);
    });

    testWidgets('the beat is about 2.5 seconds', (t) async {
      expect(typingBeat, const Duration(milliseconds: 2500));
      phone(t);
      final api = typingChat();
      var clock = DateTime.utc(2026, 11, 17, 12);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, clock: () => clock))));
      await settle(t);
      await typeIn(t, box(), 'O');
      clock = clock.add(const Duration(milliseconds: 2400));
      await typeIn(t, box(), 'On');
      clock = clock.add(const Duration(milliseconds: 200));
      await typeIn(t, box(), 'On ');
      await settle(t);
      expect(said(api), [true, true]);
      await drain(t);
    });

    testWidgets('words given back after a failed send are not typing; a suspended sender says no typing', (t) async {
      phone(t);
      final api = typingChat();
      var standing = false;
      api.routes['POST /v1/me/people/pc1/messages'] = (_) => standing
          ? answer({'error': 'Taskiem’s moderators have stopped your account from messaging people for now. See Your standing in Settings to find out why and to appeal.'}, 403)
          : answer({'error': 'This message was not sent.'}, 400);
      var clock = DateTime.utc(2026, 11, 17, 12);
      final d = PeopleChatData(api.auth(), 'pc1', follow: false, now: () => at, clock: () => clock);
      await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: PeopleChatScreen(data: d)));
      await settle(t);
      await typeIn(t, box(), 'Hi');
      await settle(t);
      await press(t, find.bySemanticsLabel('Send'));
      expect(find.text('Hi'), findsWidgets, reason: 'the words back in the box');
      expect(d.toStanding, isFalse);
      expect(said(api), [true, false], reason: 'the words given back said nothing');

      standing = true;
      clock = clock.add(const Duration(seconds: 10));
      await press(t, find.bySemanticsLabel('Send'));
      expect(d.toStanding, isTrue);
      expect(said(api), [true, false]);
      clock = clock.add(const Duration(seconds: 10));
      await typeIn(t, box(), 'Hi there');
      await settle(t);
      expect(said(api), [true, false], reason: 'stopped from messaging: no typing');
      await drain(t);
    });
  });
}
