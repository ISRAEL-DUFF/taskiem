// Signing in, staying signed in and signing out (docs/spec/mobile-app.md
// §3.6, §5.2). Every way in asks for a device session: the token is kept in
// the phone's secure storage, and on opening the app it is checked with
// GET /v1/me, so a person stays signed in until the session ends (30 days
// unused, 180 at most, or signed out from Your phones).

import 'package:flutter/foundation.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import 'session.dart';

enum AuthState { checking, signedOut, signedIn }

class Auth extends ChangeNotifier {
  Auth({required this.transport, required this.store}) : api = TaskiemApi(transport);

  final Transport transport;
  final SessionStore store;
  final TaskiemApi api;

  AuthState state = AuthState.checking;

  /// Who is signed in, once they are.
  Me? me;

  /// Opening the app: a kept token is checked; one the server no longer
  /// knows (ended, expired) is forgotten and the person signs in again.
  /// Offline, a kept token is trusted until the server can be asked.
  Future<void> start() async {
    final t = await store.token();
    if (t == null) return _set(AuthState.signedOut);
    transport.token = t;
    try {
      me = await api.me();
      _set(AuthState.signedIn);
    } on ApiError catch (e) {
      if (e.status == 401 || e.status == 403) {
        await _forget();
      } else {
        _set(AuthState.signedIn);
      }
    } on Offline {
      _set(AuthState.signedIn);
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
    me = await api.me();
    _set(AuthState.signedIn);
  }

  Future<void> _forget() async {
    transport.token = null;
    me = null;
    await store.keep(null);
    _set(AuthState.signedOut);
  }

  void _set(AuthState s) {
    state = s;
    notifyListeners();
  }
}
