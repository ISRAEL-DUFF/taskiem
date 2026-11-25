// A film of a deal with a reseller (Gadgets, "Sell to dealers"): Kehinde Alabi
// offers on 20 Nova X5; Maryam counters; Kehinde counters back under Ikenna's
// lowest price, so Maryam's Accept waits for the owner; Ikenna agrees on his
// card in Taskiem's conversation. Adapted from film_food_deal_test.dart. Set-up copied from deal_test.dart (the conversation
// with a deal card) and taskiem_test.dart (Taskiem's conversation with its
// Needs you cards); the filming is film.dart's. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_gadgets_a_deal_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/gadgets-a-deal --out gadgets-a-deal.mp4 --captions

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/conversation.dart';
import 'package:taskiem/chat/deal.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/chat/thread.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'film.dart';
import 'support.dart';

// The fixture's link to Kehinde Alabi's business.
const _link = '155047d5-4065-4887-86b0-8c6a88ebb352';
const _orderId = '01a1ca87-1f7a-752f-99df-17e296553a3a';
const _ref = 'O-20NOVA4';

http.Response _ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});

/// The fixture's person as someone at Techline Gadgets: [name], [roles], and the
/// permissions given ([drop] removes some from the owner's).
Me _me(Fixture f, String name, List<String> roles, {List<String> drop = const []}) {
  final j = Map<String, dynamic>.from(f.json('/v1/me'));
  j['tenant_name'] = 'Techline Gadgets';
  j['roles'] = roles;
  j['user'] = {...(j['user'] as Map<String, dynamic>), 'name': name, 'email': '${name.toLowerCase()}@techline.test'};
  j['permissions'] = [for (final p in (j['permissions'] as List).cast<String>()) if (!drop.contains(p)) p];
  return Me.fromJson(j);
}

Map<String, dynamic> _text(String id, bool mine, String who, String body, String at) => {
  'id': id, 'link': _link, 'mine': mine, 'kind': 'text', 'body': body, 'sender_name': who, 'created_at': at,
};

/// Kehinde's card on the order, as it stands: [event] "offered" or
/// "countered", at [kobo], the steps so far.
Map<String, dynamic> _card(String event, int kobo, List<Map<String, Object?>> steps) => {
  'id': 'deal-card-$event',
  'link': _link,
  'mine': false,
  'kind': 'card',
  'sender_name': 'Kehinde',
  'created_at': '2026-11-11T12:30:00Z',
  'card': {
    'doc': 'order',
    'event': event,
    'reference': _ref,
    'id': _orderId,
    'amount_kobo': kobo,
    'was_kobo': 370000000,
    'deal': {'steps': steps, 'until': '2026-11-13T12:30:00Z'},
  },
};

void main() {
  setUpAll(loadFonts);

  final texts = [
    _text('t1', false, 'Kehinde', 'Good morning Techline. I need 20 Nova X5 for my shop in Ibadan.', '2026-11-11T12:10:00Z'),
    _text('t2', true, 'Ikenna', 'Good morning Kehinde. Our reseller list is with you: ₦178,000 each from 10.', '2026-11-11T12:15:00Z'),
  ];

  /// Techline's conversation with Kehinde Alabi, [card] at its end, as
  /// Maryam sees it.
  Future<List<(String, String, Object?)>> conversation(WidgetTester t, Map<String, dynamic> card, {Map<String, Object?>? answer}) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-deal');
    final sent = <(String, String, Object?)>[];
    final chats = ChatsData(
      f.auth(also: (req) async {
        if (req.method == 'GET' && req.url.path == '/v1/link-chats/$_link/messages' && req.url.queryParameters.isEmpty) {
          return _ok({'messages': [...texts, card]});
        }
        if (req.method == 'GET' && req.url.path == '/v1/links') {
          // Kehinde buys from us: the head says so.
          final links = Map<String, dynamic>.from(f.json('/v1/links'));
          links['links'] = [
            for (final l in (links['links'] as List).cast<Map<String, dynamic>>())
              if (l['id'] == _link) {...l, 'name': 'Kehinde Alabi', 'role': 'customer'} else l,
          ];
          return _ok(links);
        }
        if (req.method == 'GET') return null;
        if (req.url.path.endsWith('/read')) return _ok({});
        sent.add((req.method, req.url.path, req.body.isEmpty ? null : jsonDecode(req.body)));
        return _ok(answer ?? {});
      }),
      now: () => f.at,
    )..load();
    chats.auth.me = _me(f, 'Maryam', ['cashier'], drop: ['storefront.manage', 'approval.decide', 'billing.manage', 'role.manage', 'member.manage']);
    final list = (f.json('/v1/link-chats')['conversations'] as List).cast<Map<String, dynamic>>();
    final json = Map<String, dynamic>.from(list.firstWhere((c) => c['link'] == _link))..['name'] = 'Kehinde Alabi'..['logo'] = false..remove('last');
    final conv = ListLinkChatsResponse.fromJson({'conversations': [json]}).conversations.single;
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: ConversationScreen(data: ConversationData(chats, conv, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    final thread = t.state<ThreadViewState<ThreadMessage>>(find.byType(ThreadView<ThreadMessage>));
    thread.scroll.jumpTo(thread.scroll.position.maxScrollExtent);
    await t.pump();
    return sent;
  }

  /// Ikenna's Taskiem conversation, with the deal waiting on his OK.
  Future<List<String>> needsYou(WidgetTester t) async {
    await t.pumpWidget(const SizedBox());
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chat-taskiem');
    final asked = <String>[];
    var agreed = false;
    final chats = ChatsData(
      f.auth(also: (req) async {
        asked.add('${req.method} ${req.url.path}');
        if (req.method == 'POST' && req.url.path == '/v1/trade-orders/$_orderId/accept') {
          agreed = true;
          return _ok({});
        }
        if (req.method != 'GET') return _ok({});
        return switch (req.url.path) {
          '/v1/taskiem-chat' => _ok({'messages': <Object>[]}),
          '/v1/chat-briefing' => http.Response('{}', 404),
          '/v1/chat-alerts' => _ok({
            'alerts': [
              if (!agreed)
                {
                  'key': 'deal:$_orderId',
                  'kind': 'deal',
                  'at': '2026-11-11T12:40:00Z',
                  'title': 'Maryam would agree ₦3,450,000 with Kehinde Alabi',
                  'line': 'was ₦3,700,000 · under your lowest price',
                  'amount_kobo': 345000000,
                  'id': _orderId,
                  'reference': _ref,
                },
            ],
          }),
          _ => null,
        };
      }),
      now: () => f.at,
    )..load();
    chats.auth.me = _me(f, 'Ikenna Nwachukwu', ['owner']);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    return asked;
  }

  testWidgets('film: a deal', (t) async {
    // Maryam: Kehinde has offered ₦3,400,000 (the list is ₦3,700,000: 20
    // Nova X5 at ₦185,000).
    await conversation(t, _card('offered', 340000000, [
      {'by': 'buyer', 'who': 'Kehinde', 'total_kobo': 340000000, 'at': '2026-11-11T12:30:00Z'},
    ]));
    final f = Film.start(t, 'gadgets-a-deal');
    f.say('Kehinde offers ₦3,400,000 for 20 Nova X5. Maryam sees it.', who: 'Maryam · Techline Gadgets');
    await f.hold(3.4);

    f.say('Maryam taps Counter, to ask for a little more.');
    await t.ensureVisible(find.bySemanticsLabel('Counter $_ref'));
    await t.pump();
    await f.hold(1.2);
    await f.tap(find.bySemanticsLabel('Counter $_ref'), then: 0.8);
    final boxes = find.descendant(of: find.byType(DealActions), matching: find.byType(EditableText));
    f.say('She types ₦3,600,000. Taskiem shows the share off.');
    await f.tap(boxes.first, then: 0.2);
    await f.keyboard(true);
    await f.type(boxes.first, '3600000');
    await f.hold(1.4);
    await f.keyboard(false);
    await t.ensureVisible(find.bySemanticsLabel('Send counter $_ref'));
    await t.pump();
    await f.hold(0.5);
    f.say('She taps Send counter, and the offer goes to Kehinde.');
    await f.tap(find.bySemanticsLabel('Send counter $_ref'), then: 0.4);
    await f.settle();
    await f.hold(2.0);

    // Kehinde counters back at ₦3,450,000: under Ikenna's lowest price (₦3,500,000).
    await conversation(
      t,
      _card('countered', 345000000, [
        {'by': 'buyer', 'who': 'Kehinde', 'total_kobo': 340000000, 'at': '2026-11-11T12:30:00Z'},
        {'by': 'seller', 'who': 'Maryam', 'total_kobo': 360000000, 'at': '2026-11-11T12:38:00Z'},
        {'by': 'buyer', 'who': 'Kehinde', 'total_kobo': 345000000, 'at': '2026-11-11T12:40:00Z'},
      ]),
      answer: {'waiting': 'owner'},
    );
    f.say("Kehinde counters ₦3,450,000. That is under Ikenna's lowest price.");
    await f.hold(3.6);

    f.say('Maryam taps Accept. She cannot go below the lowest price.');
    await t.ensureVisible(find.bySemanticsLabel('Accept ₦3,450,000 $_ref'));
    await t.pump();
    await f.hold(1.6);
    await f.tap(find.bySemanticsLabel('Accept ₦3,450,000 $_ref'), then: 0.4);
    await f.settle();
    f.say("It is not agreed yet. It waits for Ikenna's OK.");
    await f.hold(3.4);

    // Ikenna's phone.
    await needsYou(t);
    f.say('Ikenna sees it in Taskiem, with the price and why it waits.', who: 'Ikenna · Techline Gadgets');
    await f.hold(3.2);
    f.say('He taps Agree ₦3,450,000 to make the deal with Kehinde.');
    final agree = find.bySemanticsLabel(RegExp(r'^Agree ₦3,450,000'));
    await t.ensureVisible(agree);
    await t.pump();
    await f.hold(1.4);
    await f.tap(agree, then: 0.4);
    await f.settle();
    f.say('The deal is agreed at ₦3,450,000, and the card goes.');
    await f.hold(3.0);
    await f.finish();
  }, skip: !filming);
}
