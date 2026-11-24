// What the app keeps on the phone (docs/spec/mobile-app.md §5.5): the
// session token, in the phone's Keychain or Keystore, and the random id of
// this install, which names the phone to the server (§5.2). Nothing of the
// business.

import 'dart:io' show Platform;
import 'dart:math';

import 'package:device_info_plus/device_info_plus.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../api/api.g.dart';

/// A place to keep a few strings safely: the phone's secure storage, or a
/// map in tests.
abstract class KeyStore {
  Future<String?> read(String key);
  Future<void> write(String key, String? value);
}

class SecureKeyStore implements KeyStore {
  const SecureKeyStore();
  static const _s = FlutterSecureStorage();
  @override
  Future<String?> read(String key) => _s.read(key: key);
  @override
  Future<void> write(String key, String? value) => value == null ? _s.delete(key: key) : _s.write(key: key, value: value);
}

class MemoryKeyStore implements KeyStore {
  final Map<String, String> values = {};
  @override
  Future<String?> read(String key) async => values[key];
  @override
  Future<void> write(String key, String? value) async => value == null ? values.remove(key) : values[key] = value;
}

class SessionStore {
  SessionStore(this.keys, {this.describe = describePhone});

  final KeyStore keys;

  /// The phone's name and platform, as Your phones shows them.
  final Future<({String name, String platform})> Function() describe;

  Future<String?> token() => keys.read('token');
  Future<void> keep(String? token) => keys.write('token', token);

  /// This install's id: made once, kept until the app is removed.
  Future<String> installId() async {
    final had = await keys.read('install');
    if (had != null) return had;
    final id = uuid4();
    await keys.write('install', id);
    return id;
  }

  /// The phone, for a sign-in that stays signed in.
  Future<DeviceRequest> device() async {
    final d = await describe();
    return DeviceRequest(id: await installId(), name: d.name, platform: d.platform);
  }
}

/// "TECNO Spark 10", "Ada's iPhone": what Your phones calls this phone.
Future<({String name, String platform})> describePhone() async {
  final info = DeviceInfoPlugin();
  if (Platform.isIOS) {
    final i = await info.iosInfo;
    return (name: _cut(i.name.isEmpty ? i.model : i.name), platform: 'ios');
  }
  final a = await info.androidInfo;
  final brand = a.brand.isEmpty ? '' : '${a.brand[0].toUpperCase()}${a.brand.substring(1)} ';
  return (name: _cut('$brand${a.model}'.trim()), platform: 'android');
}

String _cut(String s) => s.isEmpty ? 'Phone' : (s.length > 80 ? s.substring(0, 80) : s);

/// A random (version 4) UUID.
String uuid4() {
  final r = Random.secure();
  final b = List<int>.generate(16, (_) => r.nextInt(256));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  final h = b.map((x) => x.toRadixString(16).padLeft(2, '0')).join();
  return '${h.substring(0, 8)}-${h.substring(8, 12)}-${h.substring(12, 16)}-${h.substring(16, 20)}-${h.substring(20)}';
}
