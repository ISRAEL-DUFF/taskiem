// Signing in, staying signed in and signing out (docs/spec/mobile-app.md
// §3.6, §5.2). Every way in asks for a device session: the token is kept in
// the phone's secure storage, and on opening the app it is checked with
// GET /v1/me, so a person stays signed in until the session ends (30 days
// unused, 180 at most, or signed out from Your phones).

import 'package:flutter/foundation.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import 'session.dart';

enum AuthState { checking, signedOut, signedIn, outdated }

/// Whether version [a] is older than [b] ("0.1.0" < "0.10.0"), by number.
bool olderThan(String a, String b) {
  List<int> parts(String v) => v.split('+').first.split('.').map((x) => int.tryParse(x) ?? 0).toList();
  final x = parts(a), y = parts(b);
  for (var i = 0; i < 3; i++) {
    final p = i < x.length ? x[i] : 0, q = i < y.length ? y[i] : 0;
    if (p != q) return p < q;
  }
  return false;
}

class Auth extends ChangeNotifier {
  Auth({required this.transport, required this.store, this.version = '0.1.0'}) : api = TaskiemApi(transport) {
    // A session that ends while the app is open (signed out from another
    // phone, a password changed) signs this phone out at once.
    transport.onSessionEnded = () {
      if (state == AuthState.signedIn) _forget();
    };
  }

  final Transport transport;
  final SessionStore store;
  final TaskiemApi api;

  /// Called when the person is signed out, to forget what was theirs on
  /// this phone (Prefs.forgetPerson).
  Future<void> Function()? onForget;

  /// This app's version, checked against the lowest the server serves.
  final String version;

  AuthState state = AuthState.checking;

  /// Who is signed in, once they are.
  Me? me;

  /// auth.tsx can: any of the permissions, "a|b".
  bool can(String perm) => perm.split('|').any((p) => me?.permissions.contains(p) ?? false);

  /// Signed in by a session kept from before (opening the app), not just
  /// now: the app lock asks for the phone's lock only then.
  bool restored = false;

  /// Counts the sessions this app has had: a new one on signing in, on
  /// switching business and on signing out. What belongs to a session (the
  /// chats, the stream, the screens open on it) starts afresh with each.
  int session = 0;

  /// Opening the app: a kept token is checked; one the server no longer
  /// knows (ended, expired) is forgotten and the person signs in again.
  /// Offline, a kept token is trusted until the server can be asked.
  Future<void> start() async {
    // An app older than the server serves asks to be updated, rather than
    // failing in ways nobody can see (GET /v1/app/config). Offline, or the
    // server not saying, the app goes on.
    try {
      final c = await api.appConfig();
      if (olderThan(version, c.minVersion)) return _set(AuthState.outdated);
    } on Object {
      // Asked again next time the app opens (an answer not understood too).
    }
    final String? t;
    try {
      t = await store.token();
    } on Object {
      // The phone's secure storage could not be read (on Android, its keys
      // lost after a backup is restored): the token is as good as gone.
      return _forgetQuietly();
    }
    if (t == null) return _set(AuthState.signedOut);
    transport.token = t;
    restored = true;
    session++;
    try {
      me = await api.me();
      _set(AuthState.signedIn);
    } on ApiError catch (e) {
      // Only 401 means the session has ended. A 403 is a session that is
      // good but held (an administrator asked to add a passkey): kept.
      if (e.status == 401) {
        await _forgetQuietly();
      } else {
        _set(AuthState.signedIn);
      }
    } on Offline {
      _set(AuthState.signedIn);
    } on Object {
      // An answer this app cannot read: signing in again is the way on,
      // never the opening screen for good.
      await _forgetQuietly();
    }
  }

  /// Forgets the session even if the phone's storage will not take it.
  Future<void> _forgetQuietly() async {
    try {
      await _forget();
    } on Object {
      if (state != AuthState.signedOut) _set(AuthState.signedOut);
    }
  }

  /// Email and password.
  Future<void> signIn(String email, String password) async {
    final s = await api.login(body: LoginRequest(email: email, password: password, bearer: true, device: await store.device()));
    await _signedIn(s.token);
  }

  /// Asks for a code on the person's WhatsApp or Telegram; answers the
  /// server's sentence, the same for any number.
  Future<String> sendCode(String phone) async {
    final r = await api.chatSigninStart(body: ChatSigninStartRequest(phone: phone));
    return r.status ?? 'If this number is on a Taskiem account, we have sent a code to it on WhatsApp or Telegram.';
  }

  /// Signs in with the code.
  Future<void> signInWithCode(String phone, String code) async {
    final s = await api.chatSigninVerify(body: ChatSigninVerifyRequest(phone: phone, code: code, bearer: true, device: await store.device()));
    await _signedIn(s.token);
  }

  /// Moves this phone's session to another of the person's businesses: the
  /// same session in every way but the business (POST /v1/me/switch).
  Future<void> switchTo(String tenantId) async {
    final s = await api.switchBusiness(body: SwitchBusinessRequest(tenantId: tenantId));
    final restoredBefore = restored;
    await _signedIn(s.token);
    restored = restoredBefore;
  }

  /// A session made by signing up (or another way in that answers one).
  Future<void> signedInWith(String? token) => _signedIn(token);

  /// Signs this phone out: the server ends its session, the token goes.
  Future<void> signOut() async {
    try {
      await api.logout();
    } on Exception {
      // Offline or already ended: forgotten here all the same; the server's
      // session then ends by itself, and Your phones can end it sooner.
    }
    await _forget();
  }

  Future<void> _signedIn(String? token) async {
    if (token == null) throw ApiError(500, {'error': 'Signed in, but no session came back. Try again.'});
    transport.token = token;
    await store.keep(token);
    restored = false;
    session++;
    // Signed in: the session is good even if who-am-I cannot be read just
    // now (offline, held); the screens read it again.
    try {
      me = await api.me();
    } on ApiError {
      me = null;
    } on Offline {
      me = null;
    }
    _set(AuthState.signedIn);
  }

  Future<void> _forget() async {
    transport.token = null;
    me = null;
    session++;
    await store.keep(null);
    try {
      await onForget?.call();
    } on Object {
      // Signing out goes on whatever happens here.
    }
    _set(AuthState.signedOut);
  }

  void _set(AuthState s) {
    state = s;
    notifyListeners();
  }
}
