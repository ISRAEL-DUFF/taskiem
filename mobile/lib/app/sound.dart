// The chat's moments (docs/spec/chat-tools.md §3; web/src/lib/moments.ts):
// a sound for a new order or payment, which the owner can switch off. The
// web's two soft notes, rising (660 Hz then 880 Hz, 120 ms apart, each
// rising in 20 ms and fading over 330 ms), made here the same way and
// played as a short sound file.

import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:just_audio/just_audio.dart';
import 'package:path_provider/path_provider.dart';

/// The two notes as a WAV file: 16-bit, one channel.
Uint8List chimeWav({int rate = 22050}) {
  const length = 0.52;
  final n = (rate * length).round();
  final samples = Float64List(n);
  for (final (i, f) in [660.0, 880.0].indexed) {
    final start = i * 0.12;
    for (var k = 0; k < n; k++) {
      final t = k / rate - start;
      if (t < 0 || t > 0.4) continue;
      // linearRampToValueAtTime(0.08, +0.02), then exponential to 0.0001 by +0.35.
      final g = t < 0.02 ? 0.08 * t / 0.02 : (t < 0.35 ? 0.08 * math.pow(0.0001 / 0.08, (t - 0.02) / 0.33) : 0.0001);
      samples[k] += g * math.sin(2 * math.pi * f * t);
    }
  }
  final data = ByteData(44 + n * 2);
  void ascii(int at, String s) {
    for (var i = 0; i < s.length; i++) {
      data.setUint8(at + i, s.codeUnitAt(i));
    }
  }

  ascii(0, 'RIFF');
  data.setUint32(4, 36 + n * 2, Endian.little);
  ascii(8, 'WAVE');
  ascii(12, 'fmt ');
  data.setUint32(16, 16, Endian.little);
  data.setUint16(20, 1, Endian.little);
  data.setUint16(22, 1, Endian.little);
  data.setUint32(24, rate, Endian.little);
  data.setUint32(28, rate * 2, Endian.little);
  data.setUint16(32, 2, Endian.little);
  data.setUint16(34, 16, Endian.little);
  ascii(36, 'data');
  data.setUint32(40, n * 2, Endian.little);
  for (var k = 0; k < n; k++) {
    // The web's gain is of a full-scale wave: kept as it is.
    data.setInt16(44 + k * 2, (samples[k].clamp(-1.0, 1.0) * 32767).round(), Endian.little);
  }
  return data.buffer.asUint8List();
}

AudioPlayer? _player;
String? _file;

/// Plays the chime; nothing if the phone cannot. A test stands in for it.
Future<void> Function() chime = () async {
  try {
    final path = _file ??= await () async {
      final f = File('${(await getTemporaryDirectory()).path}/chime.wav');
      await f.writeAsBytes(chimeWav());
      return f.path;
    }();
    final p = _player ??= AudioPlayer();
    await p.setFilePath(path);
    await p.play();
  } on Object {
    // No sound: nothing else changes.
  }
};

/// moments.ts isNews: a card that is news, an order or a payment from the
/// other side.
bool isNews(String event, bool mine) => !mine && const {'ordered', 'auto_accepted', 'bill', 'declared'}.contains(event);

/// moments.ts isGoodMoment: a payment, or the first order of the day.
bool isGoodMoment(String event, bool mine, bool firstOrderToday) {
  if (mine) return false;
  return event == 'declared' || ((event == 'ordered' || event == 'auto_accepted') && firstOrderToday);
}

/// ChatAlerts.tsx NEWS: the alert cards that come with the sound.
const alertNews = {'new_order', 'unmatched', 'approval', 'deal'};
