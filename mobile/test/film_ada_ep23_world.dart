// The story's world for the Ada's Kitchen films of episodes 2 and 3
// (video/series/food): the recorded answers with the signed-in person
// renamed (Ada Obi or Tunde, of Ada's Kitchen), the diary, briefing and
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

http.Response adaOk(Object body) => http.Response.bytes(utf8.encode(jsonEncode(body)), 200, headers: {'content-type': 'application/json; charset=utf-8'});

Map<String, dynamic> _me(Fixture f, String person) {
  final me = Map<String, dynamic>.from(f.json('/v1/me'));
  me['tenant_name'] = "Ada's Kitchen";
  me['user'] = {...(me['user'] as Map<String, dynamic>), 'name': person, 'email': 'ada@adaskitchen.example'};
  return me;
}

/// The signed-in person: [person] of Ada's Kitchen.
void adaMe(Auth a, Fixture f, {String person = 'Ada Obi'}) => a.me = Me.fromJson(_me(f, person));

/// The answers every film here shares; null for anything else.
http.Response? adaBase(http.Request req, Fixture f, {String person = 'Ada Obi'}) {
  if (req.method != 'GET') return null;
  switch (req.url.path) {
    case '/v1/me':
      return adaOk(_me(f, person));
    case '/v1/taskiem-chat':
      return adaOk({'messages': <Object>[]});
    case '/v1/chat-alerts':
      return adaOk({'alerts': <Object>[]});
    case '/v1/chat-refs':
      return adaOk({'refs': <Object>[]});
    case '/v1/chat-briefing':
      return adaOk({'ready': false, 'day': '2026-11-12', 'key': 'briefing:2026-11-12', 'put_off': true});
  }
  return null;
}

/// A picture of a party pack, drawn here (a foil tray of jollof rice with
/// fried chicken and plantain on a steel counter), as the bytes of a PNG:
/// what the phone's camera hands back in the film.
Future<Uint8List> partyPackPhoto(WidgetTester t) => t.runAsync(() async {
  const w = 800.0, h = 600.0;
  final rec = ui.PictureRecorder();
  final c = Canvas(rec, const Rect.fromLTWH(0, 0, w, h));
  c.drawRect(const Rect.fromLTWH(0, 0, w, h), Paint()..color = const Color(0xFFE9E4DA));
  c.drawRect(const Rect.fromLTWH(0, 380, w, 220), Paint()..color = const Color(0xFFB9BEC3));
  c.drawRect(const Rect.fromLTWH(0, 380, w, 6), Paint()..color = const Color(0xFF9AA0A6));
  final tray = RRect.fromRectAndRadius(const Rect.fromLTWH(120, 190, 560, 300), const Radius.circular(36));
  c.drawRRect(tray.shift(const Offset(0, 14)), Paint()..color = const Color(0x33000000));
  c.drawRRect(tray, Paint()..color = const Color(0xFFD7DADD));
  final inner = RRect.fromRectAndRadius(const Rect.fromLTWH(144, 214, 512, 252), const Radius.circular(28));
  c.drawRRect(inner, Paint()..color = const Color(0xFFC9A24B));
  c.save();
  c.clipRRect(inner);
  c.drawOval(const Rect.fromLTWH(160, 210, 480, 240), Paint()..color = const Color(0xFFD2481C));
  final grain = [const Color(0xFFE4642A), const Color(0xFFBF3A14), const Color(0xFFEE8A3C)];
  for (var i = 0; i < 380; i++) {
    final x = 175 + (i * 97 % 450).toDouble();
    final y = 225 + (i * 61 % 220).toDouble();
    c.drawOval(Rect.fromCenter(center: Offset(x, y), width: 14, height: 6), Paint()..color = grain[i % 3]);
  }
  for (final p in const [Offset(250, 300), Offset(360, 262), Offset(470, 300), Offset(560, 350), Offset(300, 380)]) {
    c.drawOval(Rect.fromCenter(center: p, width: 96, height: 58), Paint()..color = const Color(0xFF8A4B16));
    c.drawOval(Rect.fromCenter(center: p.translate(-8, -8), width: 62, height: 30), Paint()..color = const Color(0xFFB8721F));
  }
  for (final p in const [Offset(520, 410), Offset(430, 420), Offset(340, 424)]) {
    c.drawOval(Rect.fromCenter(center: p, width: 70, height: 30), Paint()..color = const Color(0xFFF0B429));
    c.drawOval(Rect.fromCenter(center: p, width: 40, height: 12), Paint()..color = const Color(0xFF8C5A0B));
  }
  c.restore();
  c.drawRRect(inner, Paint()..style = PaintingStyle.stroke..strokeWidth = 4..color = const Color(0x44FFFFFF));
  final img = await rec.endRecording().toImage(w.toInt(), h.toInt());
  final png = await img.toByteData(format: ui.ImageByteFormat.png);
  img.dispose();
  return png!.buffer.asUint8List();
}).then((b) => b!);
