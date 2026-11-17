// Passkeys in the app (docs/spec/mobile-app-m4.md N6): signing in, adding
// one, and confirming an approval or a change with one, through the phone's
// own passkeys (Android's Credential Manager, iCloud Keychain). The relying
// party is the web's, so the same passkey works on the web and here; the
// server trusts the app's origin from TASKIEM_ANDROID_APP, and an iPhone app
// through its associated domain.

import 'package:passkeys/authenticator.dart';
import 'package:passkeys/types.dart';

import '../api/transport.dart';
import 'auth.dart';

/// The person closed the phone's passkey sheet: nothing to say.
class PasskeyCancelled implements Exception {
  const PasskeyCancelled();
}

/// The phone's passkeys: the platform's, a stand-in in a test.
abstract class PasskeyPlatform {
  /// Whether this phone can use passkeys at all (Android 9 and later with a
  /// screen lock, iOS 16 and later).
  Future<bool> available();

  /// A new passkey for [publicKey] (the server's creation options), as the
  /// server takes it (PasskeyCredential).
  Future<Map<String, dynamic>> create(Map<String, dynamic> publicKey);

  /// An assertion for [publicKey] (the server's request options).
  Future<Map<String, dynamic>> get(Map<String, dynamic> publicKey);
}

final _unsupported = ApiError(0, {'error': 'This phone cannot use passkeys (it needs a screen lock, and Android 9 or later). Use another way.'});

/// The phone's own, until a test swaps it.
PasskeyPlatform passkeys = PhonePasskeys();

class PhonePasskeys implements PasskeyPlatform {
  final _a = PasskeyAuthenticator();

  @override
  Future<bool> available() async {
    try {
      // ignore: deprecated_member_use
      return await _a.canAuthenticate();
    } on Object {
      return false;
    }
  }

  /// The server's credential: only the fields it takes.
  static Map<String, dynamic> _credential(Map<String, dynamic> j) => {
    'id': j['id'],
    'rawId': j['rawId'],
    'type': 'public-key',
    'response': j['response'],
  };

  Future<T> _guard<T>(Future<T> Function() f) async {
    try {
      return await f();
    } on PasskeyAuthCancelledException {
      throw const PasskeyCancelled();
    } on NoCredentialsAvailableException {
      throw ApiError(0, {'error': 'This phone has no passkey for Taskiem. Sign in another way, then add one in Settings.'});
    } on DomainNotAssociatedException {
      throw ApiError(0, {'error': 'Passkeys are not set up for this app yet. Sign in another way.'});
    } on PasskeyUnsupportedException {
      throw _unsupported;
    } on DeviceNotSupportedException {
      throw _unsupported;
    } on ExcludeCredentialsCanNotBeRegisteredException {
      throw ApiError(0, {'error': 'This phone already has your passkey for Taskiem.'});
    } on AuthenticatorException {
      throw ApiError(0, {'error': 'The passkey did not work. Try again, or use another way.'});
    }
  }

  @override
  Future<Map<String, dynamic>> create(Map<String, dynamic> publicKey) => _guard(() async {
    final r = await _a.register(RegisterRequestType.fromJson(publicKey));
    return _credential(r.toJson());
  });

  @override
  Future<Map<String, dynamic>> get(Map<String, dynamic> publicKey) => _guard(() async {
    final r = await _a.authenticate(AuthenticateRequestType.fromJson(publicKey, preferImmediatelyAvailableCredentials: false));
    return _credential(r.toJson());
  });
}

Map<String, dynamic> _publicKey(Object? answer) {
  final pk = (answer as Map)['publicKey'];
  return (pk as Map).cast<String, dynamic>();
}

/// Signs in with a passkey on this phone (POST /v1/auth/passkey/options,
/// then /v1/auth/passkey): a device session, as every way in.
Future<void> signInWithPasskey(Auth auth) async {
  final options = await auth.transport.send('POST', '/v1/auth/passkey/options', body: const {});
  final credential = await passkeys.get(_publicKey(options));
  final s = await auth.transport.send('POST', '/v1/auth/passkey', body: {
    'credential': credential,
    'bearer': true,
    'device': (await auth.store.device()).toJson(),
  });
  await auth.signedInWith((s as Map)['token'] as String?);
}

/// A passkey's confirmation of [operation] on [target] (POST
/// /v1/me/step-up/options): an approval's decision, a change to a way in.
Future<Map<String, dynamic>> passkeyProof(Auth auth, String operation, String target) async {
  final options = await auth.transport.send('POST', '/v1/me/step-up/options', body: {'operation': operation, 'target': target});
  return passkeys.get(_publicKey(options));
}

/// Adds a passkey on this phone, named after it; [proof] is what the server
/// asks before a new way in (a code, the password, or another passkey).
Future<void> addPasskey(Auth auth, Map<String, dynamic> proof) async {
  final options = await auth.transport.send('POST', '/v1/me/passkeys/options', body: proof);
  final credential = await passkeys.create(_publicKey(options));
  await auth.transport.send('POST', '/v1/me/passkeys', body: {
    'name': (await auth.store.device()).name,
    'credential': credential,
  });
}
