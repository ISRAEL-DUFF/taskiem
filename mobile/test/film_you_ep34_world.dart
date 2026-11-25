// The stand-in server for the customer films of Episodes 3 and 4 (Chat with a
// business, Order and pay): Tobi Adewale's personal account and his chat with
// Ada's Kitchen. Its shapes are those of test/my_chats_test.dart (the person's
// side: chat, messages, shop, orders, pay, chat link); the words are the
// story's. Answers change as the film acts. Not a test.

import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/api/transport.dart';
import 'package:taskiem/app/auth.dart';
import 'package:taskiem/app/session.dart';

const jsonHeaders = {'content-type': 'application/json; charset=utf-8'};
http.Response answer(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: jsonHeaders);

/// A stand-in server: answers by "METHOD /path", every request kept.
class YouApi {
  final calls = <http.Request>[];
  final routes = <String, Object? Function(http.Request r)>{};

  http.Client client() => MockClient((req) async {
    calls.add(req);
    final f = routes['${req.method} ${req.url.path}'];
    if (f == null) return http.Response(jsonEncode({'error': 'not here'}), 404, headers: jsonHeaders);
    var out = f(req);
    if (out is Future) out = await out;
    if (out is http.Response) return out;
    return http.Response(jsonEncode(out ?? {}), 200, headers: jsonHeaders);
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
      'tenant_name': null,
      'personal': true,
      'roles': <String>[],
      'permissions': <String>[],
      'auth_method': 'phone',
      'user': {'id': 'u1', 'name': 'Tobi Adewale', 'email': 'p@phone.taskiem.invalid', 'email_placeholder': true, 'phone': '+2348031230000'},
      'businesses': <Object>[],
    });
    a.state = AuthState.signedIn;
    return a;
  }
}

/// The phone's own clock in the films is 13:00; the app's `now`.
final filmNow = DateTime.utc(2026, 11, 24, 13, 0);

Map<String, dynamic> adaChat({String offers = 'unasked', bool ask = true, int unread = 0, String last = ''}) => {
  'id': 'c1',
  'business': 'b1',
  'name': 'Ada’s Kitchen',
  'last': last,
  'last_at': '2026-11-24T12:58:00Z',
  'last_mine': false,
  'unread': unread,
  'muted': false,
  'archived': false,
  'blocked': false,
  'blocked_you': false,
  'offers': offers,
  'ask_offers': ask,
  'hours': {'open': true, 'line': 'Open now, closes at 20:00'},
  'typing': false,
};

Map<String, dynamic> textMsg(String id, bool mine, String body, String hhmm) => {
  'id': id,
  'mine': mine,
  'kind': 'text',
  'body': body,
  'read_at': mine ? '2026-11-24T$hhmm:30Z' : null,
  'created_at': '2026-11-24T$hhmm:00Z',
};
