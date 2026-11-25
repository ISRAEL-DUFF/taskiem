// The story's world for the customer films of episodes 5 and 6 (video/series/
// everyone): Tobi Adewale, a person with a personal account, on You with
// chats and orders with Ada's Kitchen, Bayo Rice Mills and Kola. A stand-in
// server copied from my_chats_test.dart (Api), you_stage3_test.dart (the
// order rows) and my_chats_test.dart (the services and free times). Not a
// test.

import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';

const _json = {'content-type': 'application/json; charset=utf-8'};
http.Response ans(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: _json);

/// A stand-in server: answers by "METHOD /path".
class YouApi {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  http.Client client() => MockClient((req) async {
    calls.add(req);
    final f = routes['${req.method} ${req.url.path}'];
    if (f == null) return http.Response(jsonEncode({'error': 'not here'}), 404, headers: _json);
    final out = f(req);
    if (out is http.Response) return out;
    return http.Response(jsonEncode(out ?? {}), 200, headers: _json);
  });

  Auth auth() {
    final a = Auth(
      transport: Transport(base: 'http://localhost', token: 'tok', client: client()),
      store: SessionStore(MemoryKeyStore(), describe: () async => (name: 'Tobi phone', platform: 'android')),
    );
    a.me = Me.fromJson({
      'tenant_id': null,
      'tenant_name': null,
      'personal': true,
      'roles': <String>[],
      'permissions': <String>[],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Tobi Adewale', 'email': 'p@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031230142'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

/// A person's chat row (GET /v1/me/chats/{id}, and in the list).
Map<String, dynamic> chatRow(String id, String name, String last, String lastAt, {String business = 'b1'}) => {
  'id': id,
  'business': business,
  'name': name,
  'last': last,
  'last_at': lastAt,
  'last_mine': false,
  'unread': 0,
  'muted': false,
  'archived': false,
  'blocked': false,
  'blocked_you': false,
  'offers': 'no',
  'ask_offers': false,
  'hours': {'open': true, 'line': 'Open now, closes at 18:00'},
  'typing': false,
};

/// One order row (GET /v1/me/orders).
Map<String, dynamic> orderRow({
  required String ref,
  required String business,
  required String name,
  required String state,
  required String summary,
  required int total,
  required String made,
  String kind = 'order',
  String? chatId,
}) => {
  'business_id': business,
  'business': name,
  'reference': ref,
  'kind': kind,
  'state': state,
  'summary': summary,
  'total_kobo': total,
  'due_kobo': 0,
  'pay': false,
  'made_at': made,
  'chat_id': chatId,
};

Future<void> letIn(WidgetTester t) async {
  for (var i = 0; i < 8; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 40)));
    await t.pump();
  }
  await t.pumpAndSettle();
}
