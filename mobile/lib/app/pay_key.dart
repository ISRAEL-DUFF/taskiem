// Paying by fingerprint (docs/spec/fingerprint-key.md): a key pair made in
// this phone's secure hardware (Android Keystore, StrongBox where there is
// one; the iPhone's Secure Enclave), usable only after the phone's own
// fingerprint or face check, and lost when a fingerprint or face is added
// or removed. The private key never leaves the phone; its public key is
// registered with the server for this person and this phone (POST
// /v1/me/fingerprint). Paying: the server's challenge for one payment is
// signed after the fingerprint, and the server checks it. The plugin is
// biometric_signature (MIT): ECDSA P-256, signatures over SHA-256 in DER.
//
// The app keeps, in the phone's secure storage, which of the server's keys
// is this phone's for the person signed in ('paykey:<user>'), so the Pay
// sheet knows to offer it.

import 'package:biometric_signature/biometric_signature.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import 'auth.dart';

/// The person closed the phone's fingerprint prompt: nothing to say.
class FingerprintCancelled implements Exception {
  const FingerprintCancelled();
}

/// The key on this phone is gone (a fingerprint or face was added or
/// removed, or the app was put back): turn it on again.
class FingerprintGone implements Exception {
  const FingerprintGone();
  String get message => 'A fingerprint or face was added or removed on this phone, so paying by fingerprint is off. Turn it on again in Settings.';
}

/// The phone cannot do it now, in words for the person.
class FingerprintUnavailable implements Exception {
  const FingerprintUnavailable(this.message);
  final String message;
}

/// The phone's secure hardware: the platform's, a stand-in in a test.
abstract class PayKeyPlatform {
  /// Whether this phone has a strong fingerprint or face check set up, so
  /// a key can be made.
  Future<bool> available();

  /// A new key under [alias] (replacing one there): its public key, base64
  /// DER SubjectPublicKeyInfo.
  Future<String> create(String alias, {required String prompt});

  /// [words] signed with the key under [alias], after the fingerprint:
  /// base64 DER ECDSA.
  Future<String> sign(String alias, String words, {required String prompt});

  /// The key under [alias] removed from the phone.
  Future<void> remove(String alias);
}

/// The phone's own, until a test swaps it.
PayKeyPlatform payKeys = PhonePayKeys();

class PhonePayKeys implements PayKeyPlatform {
  final _b = BiometricSignature();

  @override
  Future<bool> available() async {
    try {
      final a = await _b.biometricAuthAvailable();
      return a.canAuthenticate == true && a.hasEnrolledBiometrics != false;
    } on Object {
      return false;
    }
  }

  static Never _fail(BiometricError? code) => switch (code) {
    BiometricError.userCanceled || BiometricError.systemCanceled => throw const FingerprintCancelled(),
    BiometricError.keyInvalidated || BiometricError.keyNotFound => throw const FingerprintGone(),
    BiometricError.notEnrolled => throw const FingerprintUnavailable('Set up a fingerprint or face on this phone first, in its own settings.'),
    BiometricError.lockedOut || BiometricError.lockedOutPermanent =>
      throw const FingerprintUnavailable('Too many tries: the phone has locked its fingerprint for now. Unlock the phone, then try again.'),
    BiometricError.notAvailable || BiometricError.notSupported =>
      throw const FingerprintUnavailable('This phone cannot keep a key behind its fingerprint. Use your passkey or PIN.'),
    _ => throw const FingerprintUnavailable('The fingerprint check did not work. Try again.'),
  };

  @override
  Future<String> create(String alias, {required String prompt}) async {
    final r = await _b.createKeys(
      keyAlias: alias,
      promptMessage: prompt,
      config: CreateKeysConfig(
        signatureType: SignatureType.ecdsa,
        enforceBiometric: true,
        setInvalidatedByBiometricEnrollment: true,
        useDeviceCredentials: false,
        requireAuthentication: true,
        enableDecryption: false,
      ),
    );
    final key = r.publicKey;
    if (key == null || key.isEmpty || (r.code != null && r.code != BiometricError.success)) _fail(r.code);
    return key;
  }

  @override
  Future<String> sign(String alias, String words, {required String prompt}) async {
    final r = await _b.createSignature(
      payload: words,
      keyAlias: alias,
      promptMessage: prompt,
      config: CreateSignatureConfig(allowDeviceCredentials: false, cancelButtonText: 'Cancel'),
    );
    final sig = r.signature;
    if (sig == null || sig.isEmpty || (r.code != null && r.code != BiometricError.success)) _fail(r.code);
    return sig;
  }

  @override
  Future<void> remove(String alias) async {
    try {
      await _b.deleteKeys(keyAlias: alias);
    } on Object {
      // Gone already.
    }
  }
}

String? _userOf(Auth auth) {
  final u = auth.me?.user;
  final id = u?.id;
  return id is String && id.isNotEmpty ? id : null;
}

/// The phone's key for this person: one alias each, so two people signed
/// in on one phone in turn keep their own.
String payKeyAlias(String user) => 'taskiem-pay-$user';

/// The server's id of this phone's key for the person signed in, or null
/// when paying by fingerprint is off here.
Future<String?> payKeyHere(Auth auth) async {
  final user = _userOf(auth);
  if (user == null) return null;
  return auth.store.keys.read('paykey:$user');
}

/// What proves turning it on, as the server takes it (one of them).
class FingerprintProof {
  const FingerprintProof({this.pin, this.code, this.passkey, this.totp});
  final String? pin;
  final String? code;
  final Map<String, dynamic>? passkey;
  final String? totp;
}

/// Turns paying by fingerprint on for this phone: a new key behind the
/// fingerprint, its public key registered with [proof]. Replaces this
/// phone's earlier key.
Future<FingerprintDevice> turnOnFingerprint(Auth auth, FingerprintProof proof) async {
  final user = _userOf(auth);
  if (user == null) throw ApiError(0, {'error': 'Sign in again, then try.'});
  final alias = payKeyAlias(user);
  final public = await payKeys.create(alias, prompt: 'Turn on paying by fingerprint');
  final phone = await auth.store.device();
  final FingerprintDevice d;
  try {
    d = (await auth.api.addMeFingerprint(
      body: AddMeFingerprintRequest(
        publicKey: public,
        device: AddMeFingerprintRequestDevice(id: phone.id, name: phone.name, platform: phone.platform),
        pin: proof.pin,
        code: proof.code,
        passkey: proof.passkey,
        totp: proof.totp,
      ),
    )).device;
  } on Object {
    // Not registered: no key is left that the server does not know.
    await payKeys.remove(alias);
    rethrow;
  }
  await auth.store.keys.write('paykey:$user', d.id);
  return d;
}

/// Forgets this phone's key: on the phone, and (when [server]) on the
/// server too.
Future<void> turnOffFingerprintHere(Auth auth, {bool server = true}) async {
  final user = _userOf(auth);
  if (user == null) return;
  final id = await auth.store.keys.read('paykey:$user');
  if (server && id != null) {
    try {
      await auth.api.removeMeFingerprint(id: id);
    } on ApiError catch (e) {
      if (e.status != 404) rethrow;
    }
  }
  await auth.store.keys.write('paykey:$user', null);
  await payKeys.remove(payKeyAlias(user));
}

/// Pays [p] with this phone's fingerprint: the server's challenge for it,
/// signed after the fingerprint, then confirmed. Throws the server's
/// refusals as ApiError (code `fingerprint_…`, `no_fingerprint`), and
/// FingerprintCancelled, FingerprintGone or FingerprintUnavailable.
Future<Payment> payByFingerprint(Auth auth, Payment p, {bool again = false, bool? tellPayee, bool? sendReceipt}) async {
  final user = _userOf(auth);
  final key = await payKeyHere(auth);
  if (user == null || key == null) throw ApiError(403, {'code': 'no_fingerprint', 'error': 'Paying by fingerprint is not on for this phone.'});
  final c = await auth.api.fingerprintPayChallenge(id: p.id, body: FingerprintPayChallengeRequest(keyId: key, again: again ? true : null));
  final String sig;
  try {
    sig = await payKeys.sign(payKeyAlias(user), c.challenge, prompt: 'Pay ${_naira(p.amountKobo)}');
  } on FingerprintGone {
    await turnOffFingerprintHere(auth);
    rethrow;
  }
  return (await auth.api.fingerprintPayConfirm(
    id: p.id,
    body: FingerprintPayConfirmRequest(keyId: key, challenge: c.challenge, signature: sig, again: again ? true : null, tellPayee: tellPayee, sendReceipt: sendReceipt),
  )).payment;
}

String _naira(int kobo) {
  final n = (kobo ~/ 100).toString().replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',');
  return '₦$n';
}
