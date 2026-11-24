// The stand-in server for the Zuri Atelier films (Get paid, Deliver): the
// recorded answers of the Taskiem chat's tools (fixture "chat-tools", as
// tools_panel_test.dart uses them), with the story's people and naira in place
// of the fixture's, and answers that change as the film acts (an invoice made,
// a payment matched). Not a test; the film_*_test.dart files use it.

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/app/prefs.dart';
import 'package:taskiem/app/session.dart';
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/chat/taskiem.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

http.Response ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});

/// Zuri Atelier's world: its invoices and money log, changing as the film acts.
class ZuriWorld {
  ZuriWorld({bool nnekaInvoice = false, bool transferIn = false, bool deliver = false}) {
    if (nnekaInvoice) invoices.insert(0, nneka());
    if (transferIn) entries.insert(0, transfer());
    if (deliver) {
      alerts = [
        {'key': 'rider:ZA-8412', 'kind': 'rider', 'at': '2026-11-12T10:50:00Z', 'title': 'Order ZA-8412 needs a rider', 'line': 'Nneka Eze · Surulere · ₦87,500', 'amount_kobo': 8750000, 'id': 'S1', 'reference': 'ZA-8412'},
      ];
      orderStatus = 'accepted';
    }
  }

  final List<String> sent = [];

  /// The cards at the end of the chat (what needs Zainab).
  List<Map<String, dynamic>> alerts = const [];

  /// The Deliver story: Nneka's storefront order and its delivery.
  String orderStatus = 'accepted';
  Map<String, dynamic>? delivery;

  Map<String, dynamic> get order => {
    'id': 'S1',
    'reference': 'ZA-8412',
    'kind': 'order',
    'status': orderStatus,
    'customer': {'name': 'Nneka Eze', 'phone': '•••0098'},
    'basket': [
      {'name': 'Ankara dress, made to measure', 'quantity': 1, 'line_kobo': 8500000},
    ],
    'total_kobo': 8750000,
    'fulfilment': 'delivery',
    'zone': 'Surulere',
    'delivery_kobo': 250000,
    'payment': 'at_order',
    'paid_at': '2026-11-12T09:30:00Z',
    'decided_at': '2026-11-12T09:40:00Z',
    'created_at': '2026-11-12T09:30:00Z',
  };

  /// The rider moves it on (on WhatsApp, not in the app); the app reads it again.
  void riderMoves(String status) {
    delivery = {
      ...?delivery,
      'status': status,
      if (status == 'accepted') 'accepted_at': '2026-11-12T11:20:00Z',
      if (status == 'picked_up') 'picked_up_at': '2026-11-12T11:40:00Z',
      if (status == 'delivered') 'delivered_at': '2026-11-12T12:10:00Z',
    };
    orderStatus = switch (status) {
      'picked_up' => 'on_the_way',
      'delivered' => 'completed',
      _ => 'accepted',
    };
  }

  final List<Map<String, dynamic>> invoices = [
    _invoice('I6', 'INV-0006', 'sent', 'Funke Adeyemi', 'Agbada, two pieces', 6000000, '2026-11-25'),
    _invoice('I5', 'INV-0005', 'paid', 'Tolu Bankole', 'Aso-ebi for 4', 4500000, '2026-11-14'),
  ];

  final List<Map<String, dynamic>> entries = [
    {'id': 'M2', 'kind': 'cash', 'direction': 'in', 'amount_kobo': 4500000, 'status': 'settled', 'channel': 'transfer', 'reference': 'FT2611180021', 'when': '2026-11-18T10:00:00Z', 'description': 'Payment for INV-0005', 'left_kobo': 0, 'towards': 'INV-0005', 'to_match': false},
  ];

  static Map<String, dynamic> _invoice(String id, String number, String status, String name, String what, int kobo, String due) => {
    'id': id,
    'number': number,
    'status': status,
    'kind': 'invoice',
    'customer': {'name': name},
    'lines': [
      {'description': what, 'quantity': 1, 'unit_kobo': kobo},
    ],
    'total_kobo': kobo,
    'paid_kobo': status == 'paid' ? kobo : 0,
    'due_kobo': status == 'paid' ? 0 : kobo,
    'due_on': due,
    'currency': 'NGN',
  };

  static Map<String, dynamic> nneka({String status = 'sent'}) => _invoice('I8', 'INV-0008', status, 'Nneka Eze', 'Ankara dress, made to measure', 8500000, '2026-11-30');

  static Map<String, dynamic> transfer() => {
    'id': 'M9',
    'kind': 'statement',
    'direction': 'in',
    'amount_kobo': 8500000,
    'status': 'settled',
    'channel': 'gtbank/current',
    'reference': 'FT2611240077',
    'when': '2026-11-24T12:20:00Z',
    'description': 'TRF FROM NNEKA EZE',
    'left_kobo': 8500000,
    'to_match': true,
  };

  /// The server's answers that move with the film.
  Future<http.Response?> answer(http.Request req) async {
    final p = req.url.path;
    if (req.method == 'GET') {
      if (p == '/v1/chat-alerts') return ok({'alerts': alerts});
      if (p == '/v1/chat-briefing') return ok({'ready': false, 'put_off': false});
      if (p == '/v1/taskiem-chat') {
        return ok({
          'messages': [
            {'id': 'd1', 'channel': 'web', 'inbound': false, 'kind': 'diary', 'text': 'Sold 4 yards Ankara: ₦48,000 (transfer)', 'created_at': '2026-11-24T09:40:00Z'},
            {'id': 'd2', 'channel': 'web', 'inbound': false, 'kind': 'diary', 'text': 'Sold 2 yards lace: ₦30,000 (cash)', 'created_at': '2026-11-24T11:05:00Z'},
          ],
        });
      }
      if (p == '/v1/storefront/orders') return ok({'orders': [order]});
      if (p == '/v1/storefront/orders/ZA-8412') return ok({'order': order, 'revealed': false, 'payments': []});
      if (p == '/v1/storefront/orders/ZA-8412/deliveries') return ok({'deliveries': [?delivery], 'not_ready': ''});
      if (p == '/v1/riders') {
        return ok({
          'riders': [
            {'id': 'R2', 'name': 'Segun', 'active': true, 'zones': ['Ikeja'], 'presence': {'state': 'busy'}},
            {'id': 'R1', 'name': 'Emeka', 'active': true, 'zones': ['Surulere'], 'presence': {'state': 'free'}},
          ],
        });
      }
      if (p == '/v1/trade-orders') return ok({'orders': []});
      if (p == '/v1/stock/purchases') return ok({'purchases': []});
      if (p == '/v1/invoices') return ok({'invoices': invoices});
      if (p == '/v1/money/entries') return ok({'entries': entries});
      return null;
    }
    sent.add('${req.method} $p ${req.body}');
    if (p == '/v1/storefront/orders/ZA-8412/offer') {
      delivery = {'id': 'D1', 'rider': 'Emeka', 'rider_phone': '', 'status': 'offered', 'fee_kobo': 250000, 'zone': 'Surulere', 'offered_at': '2026-11-12T11:15:00Z'};
      alerts = [];
      return ok({'status': 'offered', 'rider': 'Emeka'});
    }
    if (p == '/v1/invoices') {
      final b = jsonDecode(req.body) as Map;
      final x = nneka(status: 'draft');
      x['customer'] = {'name': (b['customer'] as Map)['name']};
      invoices.insert(0, x);
      return ok(x, 201);
    }
    if (p == '/v1/invoices/I8/send') {
      invoices[0] = nneka();
      return ok({'invoice': invoices[0], 'link': 'https://pay.taskiem.ng/i/zuri-0008', 'queued': ['sms'], 'credit_kobo': 0});
    }
    if (p == '/v1/money/entries/M9/match') {
      final e = entries.firstWhere((e) => e['id'] == 'M9');
      e
        ..['left_kobo'] = 0
        ..['towards'] = 'INV-0008'
        ..['to_match'] = false;
      final i = invoices.indexWhere((x) => x['id'] == 'I8');
      invoices[i] = nneka(status: 'paid');
      return ok({'ok': true});
    }
    return ok({'ok': true});
  }
}

/// The Taskiem chat, as Zainab Yusuf of Zuri Atelier has it, on [world].
Future<ChatsData> openZuriChat(WidgetTester t, ZuriWorld world) async {
  await t.pumpWidget(const SizedBox());
  t.view.physicalSize = const Size(390, 844);
  t.view.devicePixelRatio = 1;
  addTearDown(t.view.reset);
  final f = Fixture('chat-tools');
  final prefs = Prefs(MemoryKeyStore());
  await t.runAsync(prefs.load);
  final auth = f.auth(also: world.answer);
  final me = f.json('/v1/me');
  me['tenant_name'] = 'Zuri Atelier';
  (me['user'] as Map)
    ..['name'] = 'Zainab Yusuf'
    ..['email'] = 'zainab@zuriatelier.example';
  auth.me = Me.fromJson(me);
  final chats = ChatsData(auth, now: () => f.at, prefs: prefs)..load();
  await t.pumpWidget(
    TaskiemApp(
      mode: TkMode.light,
      home: TaskiemScreen(data: TaskiemData(chats, follow: false)),
    ),
  );
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 60)));
    await t.pump();
  }
  await t.pump(const Duration(seconds: 1));
  return chats;
}
