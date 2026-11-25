// The story's world for the Techline Gadgets films of episodes 2 and 3
// (video/series/food): the recorded answers with the signed-in person
// renamed (Ikenna Nwachukwu or Maryam Sani, of Techline Gadgets), the diary, briefing and
// alerts emptied so a chat opens clean, and a drawn photo of a party pack.
// Not a test.

import 'dart:convert';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/auth.dart' show Auth;

import 'support.dart';

http.Response techOk(Object body) => http.Response.bytes(utf8.encode(jsonEncode(body)), 200, headers: {'content-type': 'application/json; charset=utf-8'});

Map<String, dynamic> _me(Fixture f, String person) {
  final me = Map<String, dynamic>.from(f.json('/v1/me'));
  me['tenant_name'] = "Techline Gadgets";
  me['user'] = {...(me['user'] as Map<String, dynamic>), 'name': person, 'email': 'ikenna@techline.example'};
  return me;
}

/// The signed-in person: [person] of Techline Gadgets.
void techMe(Auth a, Fixture f, {String person = 'Ikenna Nwachukwu'}) => a.me = Me.fromJson(_me(f, person));

/// The answers every film here shares; null for anything else.
http.Response? techBase(http.Request req, Fixture f, {String person = 'Ikenna Nwachukwu'}) {
  if (req.method != 'GET') return null;
  switch (req.url.path) {
    case '/v1/me':
      return techOk(_me(f, person));
    case '/v1/taskiem-chat':
      return techOk({'messages': <Object>[]});
    case '/v1/chat-alerts':
      return techOk({'alerts': <Object>[]});
    case '/v1/chat-refs':
      return techOk({'refs': <Object>[]});
    case '/v1/chat-briefing':
      return techOk({'ready': false, 'day': '2026-11-12', 'key': 'briefing:2026-11-12', 'put_off': true});
  }
  return null;
}

/// A picture of a phone (the invented Nova X5), drawn here: a blue phone on a
/// shop counter, as the bytes of a PNG: what the phone's camera hands back.
Future<Uint8List> novaPhoto(WidgetTester t) => t.runAsync(() async {
  const w = 800.0, h = 600.0;
  final rec = ui.PictureRecorder();
  final c = Canvas(rec, const Rect.fromLTWH(0, 0, w, h));
  c.drawRect(const Rect.fromLTWH(0, 0, w, h), Paint()..color = const Color(0xFFE6E9EE));
  c.drawRect(const Rect.fromLTWH(0, 400, w, 200), Paint()..color = const Color(0xFFB5ADA2));
  final body = RRect.fromRectAndRadius(const Rect.fromLTWH(290, 70, 220, 440), const Radius.circular(34));
  c.drawRRect(body.shift(const Offset(0, 14)), Paint()..color = const Color(0x33000000));
  c.drawRRect(body, Paint()..color = const Color(0xFF1F3A68));
  final screen = RRect.fromRectAndRadius(const Rect.fromLTWH(302, 86, 196, 408), const Radius.circular(26));
  c.drawRRect(screen, Paint()..color = const Color(0xFF0E1726));
  c.drawRRect(RRect.fromRectAndRadius(const Rect.fromLTWH(312, 100, 176, 380), const Radius.circular(18)), Paint()..shader = const LinearGradient(begin: Alignment.topLeft, end: Alignment.bottomRight, colors: [Color(0xFF3F8CFF), Color(0xFF7B5CFF)]).createShader(const Rect.fromLTWH(312, 100, 176, 380)));
  c.drawCircle(const Offset(400, 112), 6, Paint()..color = const Color(0xFF0E1726));
  for (var i = 0; i < 4; i++) {
    c.drawRRect(RRect.fromRectAndRadius(Rect.fromLTWH(330.0 + i * 36, 170, 28, 28), const Radius.circular(8)), Paint()..color = const Color(0x55FFFFFF));
  }
  c.drawRRect(RRect.fromRectAndRadius(const Rect.fromLTWH(340, 260, 120, 14), const Radius.circular(7)), Paint()..color = const Color(0x88FFFFFF));
  final img = await rec.endRecording().toImage(w.toInt(), h.toInt());
  final png = await img.toByteData(format: ui.ImageByteFormat.png);
  img.dispose();
  return png!.buffer.asUint8List();
}).then((b) => b!);
