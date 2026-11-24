// A film of a deal with a business buyer (episode 8, "Sell to businesses"):
// Ada's Kitchen offers on 60 staff uniforms; Dami counters; Ada counters back
// under the lowest price, so Dami's Accept waits for the owner; Zainab agrees
// on her Needs you card. Set-up copied from deal_test.dart (the conversation
// with a deal card) and taskiem_test.dart (Taskiem's conversation with its
// Needs you cards); the filming is film.dart's. Skipped unless VIDEO_DIR is set:
//
//   VIDEO_DIR=/tmp/films flutter test test/film_deal_test.dart
//   python3 video/phone/compose.py --frames /tmp/films/a-deal --out a-deal.mp4 --captions

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

const _link = 'ef99249d-9322-40ca-91c2-e24fdd450c15';
const _orderId = '01a1ca87-1f7a-752f-99df-17e296553a3a';
const _ref = 'O-60UNI4';

http.Response _ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});

/// The fixture's person as someone at Zuri Atelier: [name], [roles], and the
/// permissions given ([drop] removes some from the owner's).
Me _me(Fixture f, String name, List<String> roles, {List<String> drop = const []}) {
  final j = Map<String, dynamic>.from(f.json('/v1/me'));
  j['tenant_name'] = 'Zuri Atelier';
  j['roles'] = roles;
  j['user'] = {...(j['user'] as Map<String, dynamic>), 'name': name, 'email': '${name.toLowerCase()}@zuri.test'};
  j['permissions'] = [for (final p in (j['permissions'] as List).cast<String>()) if (!drop.contains(p)) p];
  return Me.fromJson(j);
}

Map<String, dynamic> _text(String id, bool mine, String who, String body, String at) => {
  'id': id, 'link': _link, 'mine': mine, 'kind': 'text', 'body': body, 'sender_name': who, 'created_at': at,
};

/// Ada's Kitchen's card on the order, as it stands: [event] "offered" or
/// "countered", at [kobo], the steps so far.
Map<String, dynamic> _card(String event, int kobo, List<Map<String, Object?>> steps) => {
  'id': 'deal-card-$event',
  'link': _link,
  'mine': false,
  'kind': 'card',
  'sender_name': 'Ada',
  'created_at': '2026-11-11T12:30:00Z',
  'card': {
    'doc': 'order',
    'event': event,
    'reference': _ref,
    'id': _orderId,
    'amount_kobo': kobo,
    'was_kobo': 96000000,
    'deal': {'steps': steps, 'until': '2026-11-13T12:30:00Z'},
  },
};

void main() {
  setUpAll(loadFonts);

  final texts = [
    _text('t1', false, 'Ada', 'Good morning Zuri Atelier. We need 60 uniforms for our kitchen staff.', '2026-11-11T12:10:00Z'),
    _text('t2', true, 'Zainab', 'Good morning Ada. Our trade list is with you: ₦16,000 each from 50.', '2026-11-11T12:15:00Z'),
  ];

  /// Zuri Atelier's conversation with Ada's Kitchen, [card] at its end, as
  /// [name] sees it.
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
          // Ada's Kitchen buys from us: the head says so.
          final links = Map<String, dynamic>.from(f.json('/v1/links'));
          links['links'] = [
            for (final l in (links['links'] as List).cast<Map<String, dynamic>>())
              if (l['id'] == _link) {...l, 'name': "Ada's Kitchen", 'role': 'customer'} else l,
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
    chats.auth.me = _me(f, 'Dami', ['staff'], drop: ['storefront.manage', 'approval.decide', 'billing.manage', 'role.manage', 'member.manage']);
    final list = (f.json('/v1/link-chats')['conversations'] as List).cast<Map<String, dynamic>>();
    final json = Map<String, dynamic>.from(list.firstWhere((c) => c['link'] == _link))..['name'] = "Ada's Kitchen"..['logo'] = false..remove('last');
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

  /// Zainab's Taskiem conversation, with the deal waiting on her OK.
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
                  'title': "Dami would agree ₦876,000 with Ada's Kitchen",
                  'line': 'was ₦960,000 · under your lowest price',
                  'amount_kobo': 87600000,
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
    chats.auth.me = _me(f, 'Zainab Yusuf', ['owner']);
    await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: TaskiemScreen(data: TaskiemData(chats, follow: false))));
    for (var i = 0; i < 8; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
      await t.pump();
    }
    return asked;
  }

  testWidgets('film: a deal', (t) async {
    // Dami: Ada's Kitchen has offered ₦864,000 (10% off ₦960,000, the price
    // for 60 uniforms from 50).
    await conversation(t, _card('offered', 86400000, [
      {'by': 'buyer', 'who': 'Ada', 'total_kobo': 86400000, 'at': '2026-11-11T12:30:00Z'},
    ]));
    final f = Film.start(t, 'a-deal');
    f.say("Ada's Kitchen offers ₦864,000 for 60 uniforms. Dami sees it.", who: 'Dami · Zuri Atelier');
    await f.hold(3.4);

    f.say('Dami taps Counter, to ask a little more.');
    await t.ensureVisible(find.bySemanticsLabel('Counter $_ref'));
    await t.pump();
    await f.tap(find.bySemanticsLabel('Counter $_ref'), then: 0.8);
    final boxes = find.descendant(of: find.byType(DealActions), matching: find.byType(EditableText));
    f.say('He types ₦912,000. Taskiem shows the share off.');
    await f.tap(boxes.first, then: 0.2);
    await f.keyboard(true);
    await f.type(boxes.first, '912000');
    await f.hold(1.4);
    await f.keyboard(false);
    await t.ensureVisible(find.bySemanticsLabel('Send counter $_ref'));
    await t.pump();
    await f.hold(0.5);
    f.say('He sends it.');
    await f.tap(find.bySemanticsLabel('Send counter $_ref'), then: 0.4);
    await f.settle();
    await f.hold(2.0);

    // Ada counters back at ₦876,000: under Zuri Atelier's lowest price.
    await conversation(
      t,
      _card('countered', 87600000, [
        {'by': 'buyer', 'who': 'Ada', 'total_kobo': 86400000, 'at': '2026-11-11T12:30:00Z'},
        {'by': 'seller', 'who': 'Dami', 'total_kobo': 91200000, 'at': '2026-11-11T12:38:00Z'},
        {'by': 'buyer', 'who': 'Ada', 'total_kobo': 87600000, 'at': '2026-11-11T12:40:00Z'},
      ]),
      answer: {'waiting': 'owner'},
    );
    f.say("Ada counters back at ₦876,000. That is under Zuri's lowest price.");
    await f.hold(3.6);

    f.say('Dami taps Accept. He cannot go below the lowest price.');
    await t.ensureVisible(find.bySemanticsLabel('Accept ₦876,000 $_ref'));
    await t.pump();
    await f.hold(0.5);
    await f.tap(find.bySemanticsLabel('Accept ₦876,000 $_ref'), then: 0.4);
    await f.settle();
    f.say("It is not agreed. It waits for Zainab's OK.");
    await f.hold(3.4);

    // Zainab's phone.
    await needsYou(t);
    f.say('Zainab sees it under Needs you, with the price and why.', who: 'Zainab · Zuri Atelier');
    await f.hold(3.2);
    f.say('She taps Agree ₦876,000.');
    final agree = find.bySemanticsLabel(RegExp(r'^Agree ₦876,000'));
    await t.ensureVisible(agree);
    await t.pump();
    await f.tap(agree, then: 0.4);
    await f.settle();
    f.say('The deal is agreed at ₦876,000, and the card goes.');
    await f.hold(3.0);
    await f.finish();
  }, skip: !filming);
}
