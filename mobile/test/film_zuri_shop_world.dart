// The story's world for the Zuri Atelier films (video/series/fashion): the
// recorded 'chat-tools' answers, with the person and the business renamed
// (Zainab Yusuf, Zuri Atelier) and the restaurant's diary, briefing and
// alerts emptied, so a chat opens clean. Shared by the film_*_test.dart films
// of episodes 2 and 3; it is not a test.

import 'dart:convert';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:taskiem/api/api.g.dart' show Me;
import 'package:taskiem/app/auth.dart' show Auth;

import 'support.dart';

http.Response okJson(Object body) => http.Response.bytes(utf8.encode(jsonEncode(body)), 200, headers: {'content-type': 'application/json; charset=utf-8'});

/// The signed-in person, renamed: Zainab Yusuf of Zuri Atelier.
void zuriMe(Auth a, Fixture f, {String person = 'Zainab Yusuf'}) {
  final me = Map<String, dynamic>.from(f.json('/v1/me'));
  me['tenant_name'] = 'Zuri Atelier';
  me['user'] = {...(me['user'] as Map<String, dynamic>), 'name': person, 'email': 'zainab@zuriatelier.example'};
  a.me = Me.fromJson(me);
}

/// The answers every Zuri film shares; null for anything else.
http.Response? zuriBase(http.Request req, Fixture f, {String person = 'Zainab Yusuf'}) {
  if (req.method != 'GET') return null;
  switch (req.url.path) {
    case '/v1/me':
      final me = Map<String, dynamic>.from(f.json('/v1/me'));
      me['tenant_name'] = 'Zuri Atelier';
      me['user'] = {...(me['user'] as Map<String, dynamic>), 'name': person, 'email': 'zainab@zuriatelier.example'};
      return okJson(me);
    case '/v1/taskiem-chat':
      return okJson({'messages': <Object>[]});
    case '/v1/chat-alerts':
      return okJson({'alerts': <Object>[]});
    case '/v1/chat-refs':
      return okJson({'refs': <Object>[]});
    case '/v1/chat-briefing':
      return okJson({'ready': false, 'day': '2026-11-12', 'key': 'briefing:2026-11-12', 'put_off': true});
  }
  return null;
}

/// A picture of an Ankara dress, drawn here (bold print on a dress shape),
/// as the bytes of a PNG: what the phone's camera hands back in the film.
Future<Uint8List> ankaraDressPhoto(WidgetTester t) => t.runAsync(() async {
  const w = 600.0, h = 800.0;
  final rec = ui.PictureRecorder();
  final c = Canvas(rec, const Rect.fromLTWH(0, 0, w, h));
  c.drawRect(const Rect.fromLTWH(0, 0, w, h), Paint()..color = const Color(0xFFEFE6D8));
  c.drawRect(const Rect.fromLTWH(0, 560, w, 240), Paint()..color = const Color(0xFFDCCDB6));
  final dress = Path()
    ..moveTo(240, 120)
    ..quadraticBezierTo(300, 175, 360, 120)
    ..lineTo(400, 150)
    ..lineTo(372, 330)
    ..lineTo(470, 700)
    ..lineTo(130, 700)
    ..lineTo(228, 330)
    ..lineTo(200, 150)
    ..close();
  c.save();
  c.clipPath(dress);
  c.drawRect(const Rect.fromLTWH(0, 0, w, h), Paint()..color = const Color(0xFF1F6F5C));
  final inks = [const Color(0xFFF2B33D), const Color(0xFFD9482B), const Color(0xFF1B2A6B), const Color(0xFFF7EFE0)];
  for (var row = 0; row < 9; row++) {
    for (var col = 0; col < 7; col++) {
      final x = 90.0 + col * 62 + (row.isOdd ? 31 : 0), y = 110.0 + row * 68;
      c.drawCircle(Offset(x, y), 24, Paint()..color = inks[(row + col) % 4]);
      c.drawCircle(Offset(x, y), 12, Paint()..color = inks[(row + col + 2) % 4]);
      c.drawCircle(Offset(x, y), 4, Paint()..color = const Color(0xFF1F6F5C));
    }
  }
  c.restore();
  c.drawPath(dress, Paint()..style = PaintingStyle.stroke..strokeWidth = 4..color = const Color(0x33000000));
  final img = await rec.endRecording().toImage(w.toInt(), h.toInt());
  final png = await img.toByteData(format: ui.ImageByteFormat.png);
  img.dispose();
  return png!.buffer.asUint8List();
}).then((b) => b!);
