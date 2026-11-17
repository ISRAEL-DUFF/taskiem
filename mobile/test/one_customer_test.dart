// Taskiem for everyone, stage 4 in the app (docs/spec/taskiem-for-everyone.md
// §11): one customer across the app and a business's own WhatsApp number.
// The business's side: one row with both lines' marks; one thread with both
// lines' messages, each marked; both lines' states on the head; the reply
// line chooser (the reply line at first, switched, a shut line not offered
// and why; the 24 hours and the template on WhatsApp); cards going in the
// app; a WhatsApp thread whose customer changed their number. The person's
// side: WhatsApp messages marked, and the line under the business's name.
// The chat link's two buttons (and WhatsApp alone), signed in and out; and
// the "Offer WhatsApp on your chat link" switch.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/applinks.dart';
import 'package:taskiem/app/config.dart' show taskiemUrl;
import 'package:taskiem/app/links.dart';
import 'package:taskiem/chat/chat_link_page.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/customer.dart';
import 'package:taskiem/chat/my_chat.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/screens/chat_link.dart';
import 'package:taskiem/theme/theme.dart';

import 'customer_app_test.dart' show business, openThread, thread;
import 'guidelines.dart';
import 'my_chats_test.dart' show Api, answer, at, chat, conversation, drain, messages, openChat, press, settle, phone;
import 'support.dart' show loadFonts;

Map<String, dynamic> line(String line, {bool stopped = false, bool blocked = false, bool window = true, String? notice, String phone = '+2348031230000'}) => {
  'line': line,
  'id': line == 'app' ? 'cc1' : 'cw1',
  'phone': phone,
  'window_open': window,
  if (window) 'window_until': '2026-11-17T09:00:00Z',
  'stopped': stopped,
  'blocked': blocked,
  'notice': ?notice,
  'last_in_at': '2026-11-16T09:00:00Z',
};

/// Tolu on both lines: the app thread's id, the reply line's state on top.
Map<String, dynamic> pair({String reply = 'whatsapp', Map<String, dynamic>? whatsapp, Map<String, dynamic>? app}) {
  final w = whatsapp ?? line('whatsapp');
  final a = app ?? line('app');
  final r = reply == 'app' ? a : w;
  return {
    ...thread(),
    'lines': [w, a],
    'reply_line': reply,
    'window_open': r['window_open'],
    'stopped': r['stopped'],
    'blocked': r['blocked'],
    'notice': ?r['notice'],
  };
}

final pairMessages = [
  {'id': 'w1', 'mine': false, 'kind': 'text', 'body': 'Hi, is the cake ready?', 'line': 'whatsapp', 'read_at': null, 'created_at': '2026-11-16T08:00:00Z'},
  {'id': 'w2', 'mine': true, 'kind': 'text', 'body': 'Yes, come by 4pm', 'line': 'whatsapp', 'sender_name': 'Bisi', 'delivery': 'read', 'read_at': null, 'created_at': '2026-11-16T08:05:00Z'},
  {'id': 'a1', 'mine': false, 'kind': 'text', 'body': 'Writing from the app now', 'line': 'app', 'read_at': null, 'created_at': '2026-11-16T09:00:00Z'},
];

Api both(Map<String, dynamic> c) {
  final api = business(c: c);
  api.routes['GET /v1/customer-chats/cc1/messages'] = (_) => {'messages': pairMessages, 'more': false};
  return api;
}

void main() {
  setUpAll(loadFonts);

  testWidgets('one row per customer, with both lines\' marks', (t) async {
    phone(t);
    final api = Api();
    api.routes['GET /v1/customer-chats'] = (r) => r.url.queryParameters['archived'] == '1'
        ? {'chats': <Object>[], 'unread': 0}
        : {
            'chats': [
              {...pair(whatsapp: line('whatsapp', stopped: true, notice: 'This customer sent STOP on WhatsApp'), reply: 'app'), 'unread': 2},
              {...thread(), 'id': 'cc2', 'name': 'Ada Obi', 'phone': '+2348039990000'},
            ],
            'unread': 2,
          };
    final chats = ChatsData(api.auth(permissions: const ['storefront.manage', 'order.handle'], personal: false), now: () => at);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatsScreen(data: chats)));
    await settle(t);
    expect(find.text('Tolu Ade'), findsOneWidget, reason: 'one row for the customer on both lines');
    expect(find.text('WhatsApp'), findsOneWidget);
    expect(find.text('App'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('WhatsApp, stopped')), findsOneWidget, reason: 'STOP on WhatsApp marks its line');
    expect(find.text('Ada Obi'), findsOneWidget);
    await drain(t);
    chats.dispose();
  });

  testWidgets('one thread: both lines\' messages marked, both states on the head, the reply on the line they last wrote on, switched', (t) async {
    phone(t);
    final api = both(pair(reply: 'whatsapp'));
    final sent = <Map<String, dynamic>>[];
    api.routes['POST /v1/customer-chats/cc1/messages'] = (r) {
      final b = api.body(r);
      sent.add(b);
      return answer({'id': 'r${sent.length}', 'mine': true, 'kind': 'text', 'body': b['text'], 'line': b['line'] ?? 'whatsapp', 'delivery': 'sent', 'read_at': null, 'created_at': '2026-11-16T10:29:00Z'}, 201);
    };
    api.routes['POST /v1/customer-chats/cc1/typing'] = (_) => answer({}, 204);
    final d = await openThread(t, api);
    expect(find.textContaining('Customer in the app and on WhatsApp'), findsOneWidget);
    expect(find.text('Hi, is the cake ready?'), findsOneWidget);
    expect(find.text('Writing from the app now'), findsOneWidget);
    expect(find.text('on WhatsApp ·'), findsNWidgets(2));
    expect(find.text('in the app ·'), findsOneWidget);
    // Both lines on the head.
    expect(find.bySemanticsLabel('On WhatsApp: Open'), findsOneWidget);
    expect(find.bySemanticsLabel('In the app: Open'), findsOneWidget);
    // The chooser, on the reply line.
    final wa = find.bySemanticsLabel('On WhatsApp').last;
    expect(t.getSemantics(wa).flagsCollection.isSelected.toString(), 'Tristate.isTrue');

    await t.enterText(find.byType(EditableText).first, 'See you at 4');
    await t.pump();
    expect(api.sent('POST', '/v1/customer-chats/cc1/typing'), isEmpty, reason: 'a reply for WhatsApp is not typing in the app');
    await press(t, find.bySemanticsLabel('Send').last);
    expect(sent.last, containsPair('line', 'whatsapp'));
    expect(sent.last['text'], 'See you at 4');

    // Switched to the app.
    await press(t, find.bySemanticsLabel('In the app').last);
    await t.enterText(find.byType(EditableText).first, 'Or pay here');
    await t.pump();
    expect(api.sent('POST', '/v1/customer-chats/cc1/typing'), hasLength(1));
    await press(t, find.bySemanticsLabel('Send').last);
    expect(sent.last, containsPair('line', 'app'));
    await expectPressable(t);
    await drain(t);
    d.dispose();
  });

  testWidgets('a shut line is not offered, and says why; outside the 24 hours WhatsApp says the template rule; cards go in the app', (t) async {
    phone(t);
    // STOP on WhatsApp: the reply goes in the app, WhatsApp not offered.
    var c = pair(reply: 'app', whatsapp: line('whatsapp', stopped: true, notice: 'This customer sent STOP on WhatsApp'));
    final api = both(c);
    api.routes['GET /v1/customer-chats/cc1'] = (_) => c;
    final d = await openThread(t, api);
    expect(find.bySemanticsLabel('On WhatsApp: Sent STOP'), findsOneWidget);
    expect(find.text('Not on WhatsApp: they sent STOP. Nothing can go there until they send START.'), findsOneWidget);
    final wa = t.getSemantics(find.bySemanticsLabel('On WhatsApp').last);
    expect(wa.flagsCollection.isEnabled.toString(), 'Tristate.isFalse');
    expect(wa.hint, 'Sent STOP');
    expect(t.getSemantics(find.bySemanticsLabel('In the app').last).flagsCollection.isSelected.toString(), 'Tristate.isTrue');

    // The + says cards, offers and files go in the app.
    await press(t, find.bySemanticsLabel('Send an order, a pay link, a booking, a product or a file'));
    expect(find.text('In the app, not on WhatsApp: cards, offers and files go there'), findsOneWidget);
    await t.tapAt(const Offset(10, 10));
    await settle(t);
    await drain(t);
    d.dispose();

    // Blocked in the app, WhatsApp past its 24 hours: the template rule.
    c = pair(reply: 'whatsapp', whatsapp: line('whatsapp', window: false), app: line('app', blocked: true, notice: 'You blocked this customer in the app'));
    final api2 = both(c);
    final d2 = await openThread(t, api2);
    expect(find.text('More than a day since they last wrote on WhatsApp: your reply goes as a WhatsApp message they answer to carry on.'), findsOneWidget);
    expect(find.text('Not in the app: you blocked this customer there.'), findsOneWidget);
    expect(find.bySemanticsLabel('In the app: You blocked them'), findsOneWidget);
    expect(find.bySemanticsLabel('Send an order, a pay link, a booking, a product or a file'), findsNothing, reason: 'cards go in the app, which is blocked');
    // The menu unblocks the app line.
    await press(t, find.bySemanticsLabel('More about this conversation'));
    expect(find.text('Unblock this customer'), findsOneWidget);
    await t.tapAt(const Offset(10, 10));
    await settle(t);
    await drain(t);
    d2.dispose();
  });

  testWidgets('both lines, in the dark with large text: nothing cut off', (t) async {
    phone(t);
    final api = both(pair(reply: 'app', whatsapp: line('whatsapp', stopped: true, notice: 'This customer sent STOP on WhatsApp')));
    final chats = ChatsData(api.auth(permissions: const ['storefront.manage', 'order.handle'], personal: false), now: () => at);
    final d = CustomerData(chats, 'cc1', follow: false);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: CustomerScreen(data: d)), drive: settle);
    await drain(t);
    d.dispose();
    final person = conversation(c: {...chat(offers: 'no', ask: false), 'lines': ['app', 'whatsapp']});
    final m = MyChatData(person.auth(), 'c1', follow: false, now: () => at);
    await expectLargeText(t, TaskiemApp(mode: TkMode.dark, home: MyChatScreen(data: m)), drive: settle);
    await drain(t);
  });

  testWidgets('a WhatsApp thread whose customer changed their number says so', (t) async {
    phone(t);
    final c = {...thread(), 'id': 'cc1', 'line': 'whatsapp', 'window_open': true, 'notice': 'This customer changed their number'};
    final d = await openThread(t, business(c: c));
    expect(find.text('This customer changed their number'), findsOneWidget);
    expect(find.byType(EditableText), findsOneWidget, reason: 'the old number can still be answered');
    expect(find.text('On WhatsApp'), findsNothing, reason: 'one line: no chooser');
    await drain(t);
    d.dispose();
  });

  testWidgets('the person\'s side: WhatsApp messages marked, and the line under the business\'s name', (t) async {
    phone(t);
    final api = conversation(c: {...chat(offers: 'no', ask: false), 'lines': ['app', 'whatsapp']});
    api.routes['GET /v1/me/chats/c1/messages'] = (_) => {
      'messages': [
        {'id': 'w1', 'mine': true, 'kind': 'text', 'body': 'Is the cake ready?', 'line': 'whatsapp', 'read_at': '2026-11-16T08:01:00Z', 'created_at': '2026-11-16T08:00:00Z'},
        {'id': 'w2', 'mine': false, 'kind': 'text', 'body': 'Yes, come by 4pm', 'line': 'whatsapp', 'read_at': '2026-11-16T08:06:00Z', 'created_at': '2026-11-16T08:05:00Z'},
        ...messages.take(2).map((m) => {...m, 'line': 'app'}),
      ],
      'more': false,
    };
    final d = await openChat(t, api);
    expect(find.text('Your WhatsApp chat with them is here too'), findsOneWidget);
    expect(find.text('on WhatsApp ·'), findsNWidgets(2));
    expect(find.text('in the app ·'), findsNothing, reason: 'only WhatsApp messages are marked');
    await drain(t);
    expect(d.chat?.lines, ['app', 'whatsapp']);

    final api2 = conversation(c: {...chat(offers: 'no', ask: false), 'lines': ['app']});
    final d2 = await openChat(t, api2);
    expect(find.text('Your WhatsApp chat with them is here too'), findsNothing);
    expect(d2.chat?.lines, ['app']);
    await drain(t);
  });

  Map<String, dynamic> card({bool takes = true, List<String>? choices}) => {
    'name': 'Mama’s Kitchen',
    'takes_chats': takes,
    'code': 'k7m2q9x4pz',
    'link': 'https://app.taskiem.com/m/k7m2q9x4pz',
    'words': '',
    'whatsapp': {'number': '+2348031112222', 'link': 'https://wa.me/2348031112222'},
    'choices': choices ?? ['whatsapp', if (takes) 'taskiem'],
  };

  testWidgets('a chat link with WhatsApp: Chat on Taskiem and Chat on WhatsApp; WhatsApp alone when chats are off', (t) async {
    phone(t);
    final opened = <Uri>[];
    final was = openOutside;
    openOutside = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openOutside = was);
    final api = conversation(c: chat(unread: 0));
    var takes = true;
    api.routes['GET /v1/chat-links/k7m2q9x4pz'] = (_) => card(takes: takes);
    api.routes['POST /v1/me/chats'] = (_) => answer(chat(unread: 0), 201);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatLinkScreen(auth: api.auth(), code: 'k7m2q9x4pz')));
    await settle(t);
    expect(find.text('Chat on Taskiem'), findsOneWidget);
    expect(find.text('Chat on WhatsApp'), findsOneWidget);
    expect(find.text('Chat with Mama’s Kitchen'), findsNothing);
    await press(t, find.text('Chat on WhatsApp'));
    expect(opened, [Uri.parse('https://wa.me/2348031112222')]);
    expect(api.sent('POST', '/v1/me/chats'), isEmpty);
    await expectPressable(t);
    await press(t, find.text('Chat on Taskiem'));
    expect(api.body(api.sent('POST', '/v1/me/chats').single), {'code': 'k7m2q9x4pz'});
    await drain(t);

    takes = false;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatLinkScreen(auth: api.auth(), code: 'k7m2q9x4pz', autoStart: true)));
    await settle(t);
    expect(find.text('Chat on WhatsApp'), findsOneWidget);
    expect(find.text('Chat on Taskiem'), findsNothing);
    expect(api.sent('POST', '/v1/me/chats'), hasLength(1), reason: 'no chat started in Taskiem while it takes none');
    await drain(t);
  });

  testWidgets('signed out, a chat link with WhatsApp: Chat on WhatsApp without signing in', (t) async {
    phone(t);
    final opened = <Uri>[];
    final was = openOutside;
    openOutside = (u) async {
      opened.add(u);
      return true;
    };
    addTearDown(() => openOutside = was);
    final api = Api();
    api.routes['GET /v1/chat-links/k7m2q9x4pz'] = (_) => card();
    final auth = api.auth()..state = AuthState.signedOut;
    final links = LinkInbox(links: const Stream.empty(), first: () async => null);
    links.receive(Uri.parse('$taskiemUrl/m/k7m2q9x4pz'));
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, auth: auth, links: links));
    await settle(t);
    expect(find.text('Welcome to Taskiem'), findsOneWidget);
    await press(t, find.text('Chat on WhatsApp'));
    expect(opened, [Uri.parse('https://wa.me/2348031112222')]);
    await drain(t);
  });

  testWidgets('Customer chats: "Offer WhatsApp on your chat link" saved', (t) async {
    phone(t, 2400);
    final api = Api();
    api.routes['GET /v1/customers/chat-link'] = (_) => {
      'code': 'k7m2q9x4pz',
      'url': 'https://app.taskiem.com/m/k7m2q9x4pz',
      'created_at': '2026-11-16T08:00:00Z',
      'words': 'Chat with Mama’s Kitchen on Taskiem: https://app.taskiem.com/m/k7m2q9x4pz',
      'qr': '/v1/customers/chat-link/qr',
      'settings': {'takes_chats': true, 'storefront_button': 'both', 'link_in_updates': null, 'updates_link': true, 'link_whatsapp': true},
    };
    api.routes['PUT /v1/customers/chat-settings'] = (r) => {'takes_chats': true, 'storefront_button': 'both', 'link_in_updates': null, 'updates_link': true, 'link_whatsapp': api.body(r)['link_whatsapp'] ?? true};
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ChatLinkPage(auth: api.auth(permissions: const ['storefront.manage', 'order.handle'], personal: false))));
    await settle(t);
    expect(find.text('Offer WhatsApp on your chat link'), findsOneWidget);
    await press(t, find.bySemanticsLabel('Offer WhatsApp on your chat link').last);
    expect(api.body(api.sent('PUT', '/v1/customers/chat-settings').single), {'link_whatsapp': false});
    await drain(t);
  });
}
