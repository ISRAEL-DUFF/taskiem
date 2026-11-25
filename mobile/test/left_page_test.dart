// Left without ordering (forms that sell §6.6, L3; web StorefrontLeft.tsx,
// lib/left.ts): an order form's follow-ups, the week's counts and who
// left, each contact masked; turning follow-ups off, the wait, a hold
// released; and the chat's alert that opens it.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show FormLeftLeftItem;
import 'package:taskiem/chat/left_page.dart';
import 'package:taskiem/chat/words.dart' show CardTone;
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'guidelines.dart';
import 'looks.dart';
import 'support.dart';

Map<String, dynamic> _left({bool on = true, bool held = false, bool whatsapp = false, bool waOn = true}) => {
  'action_id': 'A1',
  'label': 'Order the ear cleaner',
  'followups': {
    'on': on,
    'wait_minutes': 30,
    if (held) 'held_at': '2026-11-25T09:00:00Z',
    if (whatsapp) 'whatsapp_ready': true,
    'whatsapp_on': waOn,
    if (whatsapp && waOn) 'whatsapp': true,
  },
  'week': {'left': 3, 'followed': 2, 'came_back': 1},
  'left': [
    {
      'id': 'L1',
      'contact_kind': 'phone',
      'contact': '… 1234',
      'product': 'Smart Ear Cleaner',
      'package': 2,
      'zone': 'Lekki',
      'at': '2026-11-25T08:00:00Z',
      'followed_up_at': '2026-11-25T08:31:00Z',
      'came_back_at': '2026-11-25T09:10:00Z',
      'channel': 'sms',
    },
    {
      'id': 'L2',
      'contact_kind': 'email',
      'contact': '…@example.com',
      'product': 'Smart Ear Cleaner',
      'package': 1,
      'at': '2026-11-25T07:00:00Z',
      'followed_up_at': '2026-11-25T07:31:00Z',
      'channel': 'email',
    },
    {
      'id': 'L4',
      'contact_kind': 'phone',
      'contact': '… 5550',
      'product': 'Smart Ear Cleaner',
      'package': 1,
      'zone': 'Yaba',
      'at': '2026-11-25T06:00:00Z',
      'followed_up_at': '2026-11-25T06:31:00Z',
      'channel': 'whatsapp',
    },
    {'id': 'L3', 'contact_kind': 'phone', 'contact': '… 9876', 'product': 'Smart Ear Cleaner', 'package': 3, 'at': '2026-11-24T07:00:00Z', 'outcome': 'stopped'},
  ],
};

void main() {
  setUpAll(loadFonts);

  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 6; i++) {
      await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
      await t.pump();
    }
    await t.pumpAndSettle();
  }

  Future<List<String>> open(WidgetTester t, {TkMode mode = TkMode.light, bool held = false, bool whatsapp = false}) async {
    t.view.physicalSize = const Size(390, 1400);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final f = Fixture('chats');
    final sent = <String>[];
    var on = true;
    var waOn = true;
    http.Response ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});
    final auth = f.auth(
      also: (req) async {
        if (req.url.path == '/v1/storefront/actions/A1/left') return ok(_left(on: on, held: held, whatsapp: whatsapp, waOn: waOn));
        if (req.url.path == '/v1/storefront/actions/A1' && req.method == 'PATCH') {
          sent.add('PATCH ${req.body}');
          final b = jsonDecode(req.body) as Map<String, dynamic>;
          if (b['followup_on'] == false) on = false;
          if (b['release_hold'] == true) held = false;
          if (b['followup_whatsapp'] is bool) waOn = b['followup_whatsapp'] as bool;
          return ok({'followups': _left(on: on, whatsapp: whatsapp, waOn: waOn)['followups']});
        }
        return null;
      },
    );
    await t.pumpWidget(TaskiemApp(mode: mode, home: LeftPage(auth: auth, action: 'A1')));
    await settle(t);
    return sent;
  }

  test('its words, as the web’s', () {
    expect(waitWords(30), '30 minutes');
    expect(waitWords(60), '1 hour');
    expect(waitWords(1440), '24 hours');
    final r = FormLeftLeftItem.fromJson((_left()['left'] as List).first as Map<String, dynamic>);
    expect(choiceWords(r), 'Smart Ear Cleaner, 2 items, to Lekki');
    expect(rowTitle(r), '2 items to Lekki');
    expect(leftStatus(r, false), ('Came back', CardTone.lime));
    final stopped = FormLeftLeftItem.fromJson((_left()['left'] as List).last as Map<String, dynamic>);
    expect(leftStatus(stopped, false).$1, 'Stopped');
    // L4: the channel each follow-up used.
    final wa = FormLeftLeftItem.fromJson((_left()['left'] as List)[2] as Map<String, dynamic>);
    expect(leftStatus(wa, false), ('On WhatsApp', CardTone.plain));
    expect(channelWords('whatsapp'), 'on WhatsApp');
    expect(channelWords('sms'), 'by text');
    expect(channelWords('email'), 'by email');
  });

  testWidgets('follow-ups on WhatsApp: each row says how it was followed up (L4)', (t) async {
    final sent = await open(t, whatsapp: true);
    expect(find.text('One message by text, email or WhatsApp from your number'), findsOneWidget);
    expect(find.text('Also follow up on WhatsApp from my number'), findsOneWidget);
    expect(find.text('On WhatsApp'), findsOneWidget);
    expect(find.textContaining('Followed up on WhatsApp'), findsOneWidget);
    expect(find.textContaining('Followed up by text'), findsOneWidget);
    expect(find.textContaining('Followed up by email'), findsOneWidget);
    await looks(t, 'left-without-ordering-whatsapp');
    await t.pump(const Duration(seconds: 5));
    // The owner turns WhatsApp off for the business: texts and emails only.
    await t.tap(find.bySemanticsLabel('Also follow up on WhatsApp from my number'));
    await settle(t);
    expect(sent, contains('PATCH {"followup_whatsapp":false}'));
    expect(find.text('Off: by text or email only'), findsOneWidget);
    expect(find.text('One text, or an email when they gave only that'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('the week and who left, masked; follow-ups turned off', (t) async {
    final sent = await open(t);
    expect(find.text('Left without ordering'), findsWidgets);
    expect(find.text('Order the ear cleaner'), findsOneWidget);
    expect(find.text('2 items to Lekki'), findsOneWidget);
    expect(find.textContaining('… 1234'), findsOneWidget);
    expect(find.textContaining('…@example.com'), findsOneWidget);
    expect(find.textContaining('They stopped your messages'), findsOneWidget);
    expect(find.text('Came back'), findsWidgets);
    expect(find.text('30 minutes'), findsOneWidget);
    await looks(t, 'left-without-ordering');
    await t.tap(find.bySemanticsLabel(RegExp('^Follow up people who leave')).last);
    await settle(t);
    expect(sent.single, contains('"followup_on":false'));
    expect(find.text('Off: your form keeps nothing of them'), findsOneWidget);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('follow-ups held, released', (t) async {
    final sent = await open(t, mode: TkMode.dark, held: true);
    expect(find.text('Follow-ups held'), findsOneWidget);
    await looks(t, 'left-without-ordering-held');
    await t.tap(find.text('Release follow-ups'));
    await settle(t);
    expect(sent.single, contains('"release_hold":true'));
    expect(find.text('Follow-ups held'), findsNothing);
    await t.pump(const Duration(seconds: 5));
  });

  testWidgets('large text and what can be pressed', (t) async {
    await open(t, mode: TkMode.dark);
    await expectPressable(t);
  });
}
