// Shared set-up for the Bright Hill Academy films, episodes 1 and 2
// (school-open, school-classes-terms): the stand-in server of
// school_fixture.dart, with Bright Hill's name, Mrs Fashola as the person,
// and a school year, classes and a team that change as the film taps.

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'package:flutter/services.dart' show FontLoader;

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/chat/chats.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'school_fixture.dart';
import 'support.dart';

http.Response bhJson(Object b, [int s = 200]) => http.Response(jsonEncode(b), s, headers: {'content-type': 'application/json'});

/// What the server holds: changes with each tap of a film.
class BhWorld {
  /// Sessions (school years) with their terms; empty: a school just opened.
  final List<Map<String, dynamic>> sessions = [];
  final List<Map<String, dynamic>> classes = [for (final c in schoolClasses) {...c}];
  final List<Map<String, dynamic>> people = [
    {'user_id': 'U1', 'name': 'Tolulope Fashola', 'phone': '+2348031230142', 'roles': ['owner'], 'chat': true, 'you': true},
  ];
  final List<Map<String, dynamic>> invites = [];

  Future<http.Response?> call(http.Request req) async {
    final p = req.url.path;
    final m = req.method;
    Map<String, dynamic> body() => req.body.isEmpty ? {} : jsonDecode(req.body) as Map<String, dynamic>;
    if (m == 'GET') {
      switch (p) {
        case '/v1/schools/sessions':
          return bhJson({'sessions': sessions});
        case '/v1/schools/classes':
          return bhJson({'classes': classes});
        case '/v1/staff':
          return bhJson({'people': people, 'invites': invites});
        case '/v1/staff/clock':
          return bhJson({'day': '2026-11-24', 'shifts': <Object>[], 'in': <Object>[], 'late': <Object>[]});
        case '/v1/roles':
          return bhJson({'roles': [{'name': 'owner'}, {'name': 'cashier'}, {'name': 'bursar'}, {'name': 'gate'}]});
        case '/v1/staff/tasks':
          return bhJson({'give': true, 'people': <Object>[], 'tasks': <Object>[], 'repeats': <Object>[]});
      }
      return null;
    }
    if (m == 'POST') {
      if (p == '/v1/schools/classes') {
        final b = body();
        classes.add({'id': 'c${classes.length}', 'name': b['name'], 'label': '${b['name']}${b['arm'] ?? ''}', 'order': b['order'], 'students': 0});
        return bhJson({'ok': true}, 201);
      }
      if (p == '/v1/schools/sessions') {
        sessions.add({'id': 'SE', 'label': body()['label'], 'terms': <Map<String, dynamic>>[]});
        return bhJson({'ok': true}, 201);
      }
      if (p == '/v1/schools/sessions/SE/terms') {
        final b = body();
        (sessions.single['terms'] as List).add({'id': 'T1', 'name': b['name'], 'starts_on': b['starts_on'], 'ends_on': b['ends_on'], 'fees_due_on': b['fees_due_on'], 'state': 'planned', 'enrolled': 0});
        return bhJson({'ok': true}, 201);
      }
      if (p == '/v1/schools/terms/T1/start') {
        final t = (sessions.single['terms'] as List).single as Map<String, dynamic>;
        t['state'] = 'current';
        t['enrolled'] = 418;
        return bhJson({'enrolled': 418});
      }
      if (p == '/v1/staff/invites') {
        final b = body();
        invites.add({'id': 'I${invites.length + 1}', 'name': b['name'], 'phone': b['phone'], 'roles': b['roles'], 'inviter': 'Tolulope', 'sent_at': '2026-11-24T09:00:00Z', 'expires_at': '2026-12-01T09:00:00Z', 'created_at': '2026-11-24T09:00:00Z'});
        return bhJson({'id': 'I1', 'sent': true});
      }
    }
    return null;
  }
}

/// Mrs Fashola (or [gateOnly]: Mallam Audu) on the recorded answers, school on.
ChatsData bhChats(BhWorld w, {bool gateOnly = false}) {
  final f = Fixture('chats');
  final chats = ChatsData(f.auth(also: (r) async => await w.call(r) ?? await schoolServer(r)), now: () => f.at);
  final j = f.json('/v1/me');
  j['tenant_name'] = 'Bright Hill Academy';
  j['schools'] = true;
  j['gate_only'] = gateOnly;
  j['user'] = {...(j['user'] as Map<String, dynamic>), 'name': gateOnly ? 'Mallam Audu' : 'Tolulope Fashola', 'email': gateOnly ? 'audu@brighthill.example' : 'fashola@brighthill.example'};
  if (gateOnly) j['roles'] = ['gate'];
  chats.auth.me = Me.fromJson(j);
  return chats;
}

Future<void> bhOpen(WidgetTester t, ChatsData c, Widget Function(ChatsData c) home) async {
  await t.pumpWidget(const SizedBox());
  await t.pumpWidget(TaskiemApp(mode: TkMode.light, home: home(c)));
  await bhSettle(t);
}

Future<void> bhSettle(WidgetTester t) async {
  for (var i = 0; i < 6; i++) {
    await t.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 30)));
    await t.pump();
  }
  await t.pump(const Duration(milliseconds: 400));
}

/// Flutter's own Roboto and Material icons, which the system date picker
/// (Material's) is drawn in; the test engine has no fonts of its own.
Future<void> bhLoadMaterialFonts() async {
  final bin = (Platform.environment['PATH'] ?? '').split(':').firstWhere((d) => File('$d/flutter').existsSync(), orElse: () => '');
  if (bin.isEmpty) return;
  final dir = '$bin/cache/artifacts/material_fonts';
  Future<ByteData> read(String n) async => ByteData.sublistView(Uint8List.fromList(File('$dir/$n').readAsBytesSync()));
  final roboto = FontLoader('Roboto');
  for (final n in ['Roboto-Regular.ttf', 'Roboto-Medium.ttf', 'Roboto-Bold.ttf', 'Roboto-Light.ttf']) {
    roboto.addFont(read(n));
  }
  await roboto.load();
  await (FontLoader('MaterialIcons')..addFont(read('MaterialIcons-Regular.otf'))).load();
}
