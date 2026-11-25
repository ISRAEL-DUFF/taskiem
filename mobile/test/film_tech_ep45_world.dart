// The stand-in server for the Techline Gadgets films of Episodes 4 and 5 (Get paid,
// Deliver): the recorded answers of the Taskiem chat's tools (fixture
// "chat-tools", which is the fixture's own business), with the story's invoice,
// transfer and order in place of the fixture's, and answers that change as the
// film acts. Not a test; the film_gadgets_*_test.dart files use it.

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

/// Techline Gadgets' world: its invoices, money log and one order, changing as the film acts.
class TechWorld {
  TechWorld({bool okoroInvoice = false, bool transferIn = false, bool deliver = false}) {
    if (okoroInvoice) invoices.insert(0, okoro());
    if (transferIn) entries.insert(0, transfer());
    if (deliver) {
      alerts = [
        {'key': 'rider:TG-2031', 'kind': 'rider', 'at': '2026-11-12T10:50:00Z', 'title': 'Order TG-2031 needs a rider', 'line': 'Chiamaka Eze · Ikeja · ₦187,000', 'amount_kobo': 18700000, 'id': 'S1', 'reference': 'TG-2031'},
      ];
      orderStatus = 'accepted';
    }
  }

  final List<String> sent = [];

  /// The cards at the end of the chat (what needs the owner).
  List<Map<String, dynamic>> alerts = const [];

  /// The Deliver story: Chiamaka's storefront order and its delivery.
  String orderStatus = 'accepted';
  Map<String, dynamic>? delivery;

  Map<String, dynamic> get order => {
    'id': 'S1',
    'reference': 'TG-2031',
    'kind': 'order',
    'status': orderStatus,
    'customer': {'name': 'Chiamaka Eze', 'phone': '•••0417'},
    'basket': [
      {'name': 'Nova X5, 128 GB, Blue', 'quantity': 1, 'line_kobo': 18500000},
    ],
    'total_kobo': 18700000,
    'fulfilment': 'delivery',
    'zone': 'Ikeja',
    'delivery_kobo': 200000,
    'payment': 'on_delivery',
    if (orderStatus == 'completed') 'paid_at': '2026-11-12T12:10:00Z',
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
      if (status == 'delivered') ...{'delivered_at': '2026-11-12T12:10:00Z', 'paid_how': 'cash'},
    };
    orderStatus = switch (status) {
      'picked_up' => 'on_the_way',
      'delivered' => 'completed',
      _ => 'accepted',
    };
  }

  final List<Map<String, dynamic>> invoices = [
    _invoice('I13', 'INV-0013', 'sent', 'Kehinde Alabi', '20 Nova X5 phones', 370000000, '2026-11-26'),
    _invoice('I12', 'INV-0012', 'paid', 'Chiamaka Eze', 'Charger, 2 pieces', 1300000, '2026-11-10'),
  ];

  final List<Map<String, dynamic>> entries = [
    {'id': 'M2', 'kind': 'cash', 'direction': 'in', 'amount_kobo': 1300000, 'status': 'settled', 'channel': 'transfer', 'reference': 'FT2611100021', 'when': '2026-11-10T10:00:00Z', 'description': 'Payment for INV-0012', 'left_kobo': 0, 'towards': 'INV-0012', 'to_match': false},
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

  static Map<String, dynamic> okoro({String status = 'sent'}) => _invoice('I14', 'INV-0014', status, 'Okoro & Sons Ltd', '10 Pulse tablets', 120000000, '2026-11-26');

  static Map<String, dynamic> transfer() => {
    'id': 'M9',
    'kind': 'statement',
    'direction': 'in',
    'amount_kobo': 120000000,
    'status': 'settled',
    'channel': 'gtbank/current',
    'reference': 'FT2611120077',
    'when': '2026-11-12T10:20:00Z',
    'description': 'TRF FROM OKORO AND SONS',
    'left_kobo': 120000000,
    'to_match': true,
  };

  /// The server's answers that move with the film.
  Future<http.Response?> answer(http.Request req) async {
    final p = req.url.path;
    if (req.method == 'GET') {
      if (p == '/v1/customers/chat-link') return ok({'url': 'https://chat.taskiem.ng/techline-gadgets', 'qr': '/v1/customers/chat-link/qr'});
      if (p == '/v1/chat-alerts') return ok({'alerts': alerts});
      if (p == '/v1/chat-briefing') return ok({'ready': false, 'put_off': false});
      if (p == '/v1/taskiem-chat') {
        return ok({
          'messages': [
            {'id': 'd1', 'channel': 'web', 'inbound': false, 'kind': 'diary', 'text': 'Sold 1 Charger, 1 Volt power bank: ₦19,000 (transfer)', 'created_at': '2026-11-12T09:40:00Z'},
            {'id': 'd2', 'channel': 'web', 'inbound': false, 'kind': 'diary', 'text': 'Sold 1 Charger: ₦6,500 (cash)', 'created_at': '2026-11-12T10:05:00Z'},
          ],
        });
      }
      if (p == '/v1/storefront/orders') return ok({'orders': [order]});
      if (p == '/v1/storefront/orders/TG-2031') return ok({'order': order, 'revealed': false, 'payments': []});
      if (p == '/v1/storefront/orders/TG-2031/deliveries') return ok({'deliveries': [?delivery], 'not_ready': ''});
      if (p == '/v1/riders') {
        return ok({
          'riders': [
            {'id': 'R2', 'name': 'Segun', 'active': true, 'zones': ['Surulere'], 'presence': {'state': 'busy'}},
            {'id': 'R1', 'name': 'Emeka', 'active': true, 'zones': ['Ikeja'], 'presence': {'state': 'free'}},
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
    if (p == '/v1/storefront/orders/TG-2031/offer') {
      delivery = {'id': 'D1', 'rider': 'Emeka', 'rider_phone': '', 'status': 'offered', 'fee_kobo': 200000, 'zone': 'Ikeja', 'offered_at': '2026-11-12T11:15:00Z'};
      alerts = [];
      return ok({'status': 'offered', 'rider': 'Emeka'});
    }
    if (p == '/v1/invoices') {
      final b = jsonDecode(req.body) as Map;
      final x = okoro(status: 'draft');
      x['customer'] = {'name': (b['customer'] as Map)['name']};
      invoices.insert(0, x);
      return ok(x, 201);
    }
    if (p == '/v1/invoices/I14/send') {
      invoices[0] = okoro();
      return ok({'invoice': invoices[0], 'link': 'https://pay.taskiem.ng/i/techline-0014', 'queued': ['sms'], 'credit_kobo': 0});
    }
    if (p == '/v1/money/entries/M9/match') {
      final e = entries.firstWhere((e) => e['id'] == 'M9');
      e
        ..['left_kobo'] = 0
        ..['towards'] = 'INV-0014'
        ..['to_match'] = false;
      final i = invoices.indexWhere((x) => x['id'] == 'I14');
      invoices[i] = okoro(status: 'paid');
      return ok({'ok': true});
    }
    return ok({'ok': true});
  }
}

/// The Taskiem chat, as the chat-tools fixture's owner has it (the fixture's own business and person), on [world].
Future<ChatsData> openTechChat(WidgetTester t, TechWorld world) async {
  await t.pumpWidget(const SizedBox());
  t.view.physicalSize = const Size(390, 844);
  t.view.devicePixelRatio = 1;
  addTearDown(t.view.reset);
  final f = Fixture('chat-tools');
  final prefs = Prefs(MemoryKeyStore());
  await t.runAsync(prefs.load);
  final auth = f.auth(also: world.answer);
  final me = f.json('/v1/me');
  me['tenant_name'] = 'Techline Gadgets';
  (me['user'] as Map<String, dynamic>)
    ..['name'] = 'Ikenna Nwachukwu'
    ..['email'] = 'ikenna@demo.test';
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
