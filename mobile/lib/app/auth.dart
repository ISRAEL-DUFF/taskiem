// Signing in, staying signed in and signing out (docs/spec/mobile-app.md
// §3.6, §5.2). Every way in asks for a device session: the token is kept in
// the phone's secure storage, and on opening the app it is checked with
// GET /v1/me, so a person stays signed in until the session ends (30 days
// unused, 180 at most, or signed out from Your phones).

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import 'session.dart';
import 'store.dart';

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

  /// Called on Sign out before the session ends, while the server still
  /// takes it (this phone's notifications taken off: lib/app/notify.dart).
  Future<void> Function()? beforeSignOut;

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

  /// What the phone keeps of this business's chats (lib/app/store.dart):
  /// open while signed in, once who is signed in is known; null before,
  /// or where the phone cannot keep it (the screens then work without).
  LocalStore? get local => _local;
  set local(LocalStore? s) {
    _local = s;
    LocalStore.current = s;
    // The pages read are kept in it too (Today, Money, Orders: K4).
    transport.pages = s;
    if (s == null) transport.offline.value = false;
  }

  LocalStore? _local;

  /// Who is signed in, kept on the phone: the store's name, and their
  /// permissions read offline.
  Future<void> _keepMe() async {
    final m = me;
    // The person's own side keeps no business's chats: what was kept of a
    // business is not opened on it (the next start asks the server). That
    // the session is a personal one is kept, so opening the app offline
    // opens You, not a business.
    if (m?.personal == true) {
      local?.close();
      local = null;
      _keptPersonal = true;
      try {
        await store.keys.write('scope', null);
        await store.keys.write('personal', '1');
      } on Object {
        // Read again from the server next time.
      }
      return;
    }
    _keptPersonal = false;
    try {
      await store.keys.write('personal', null);
    } on Object {
      // As above.
    }
    final tenant = m?.tenantId?.toString();
    final user = m?.user?.id;
    if (m == null || tenant == null || user == null) return;
    final scope = LocalStore.scopeOf(tenant, user);
    try {
      if (local?.scope != scope) {
        local?.close();
        local = await LocalStore.open(scope, store.keys);
      }
      local!.write('me', m.toJson());
      await store.keys.write('scope', scope);
    } on Object {
      // Not kept on this phone: the screens ask the server, as before.
      local = null;
    }
  }

  /// The store of the business last signed in, and who that was, as kept:
  /// for opening at once, offline too.
  Future<Me?> _keptMe() async {
    try {
      final scope = await store.keys.read('scope');
      if (scope == null) return null;
      local = await LocalStore.open(scope, store.keys);
      final j = local!.read('me');
      return j is Map ? Me.fromJson(j.cast<String, dynamic>()) : null;
    } on Object {
      local = null;
      return null;
    }
  }

  /// Opening the app: a kept token is checked; one the server no longer
  /// knows (ended, expired) is forgotten and the person signs in again.
  /// Offline, a kept token is trusted until the server can be asked.
  Future<void> start() async {
    final String? t;
    try {
      t = await store.token();
    } on Object {
      // The phone's secure storage could not be read (on Android, its keys
      // lost after a backup is restored): the token is as good as gone.
      return _forgetQuietly();
    }
    // Signed in before, and who it was kept on the phone: the app opens at
    // once on what it kept, as WhatsApp does, offline too; the server is
    // asked meanwhile, and its answer takes over (an ended session signs
    // out, an app too old asks to be updated).
    if (t != null) {
      final kept = await _keptMe();
      if (kept != null) {
        transport.token = t;
        restored = true;
        session++;
        me = kept;
        _set(AuthState.signedIn);
        unawaited(_checkKept());
        return;
      }
    }
    // An app older than the server serves asks to be updated, rather than
    // failing in ways nobody can see (GET /v1/app/config). Offline, or the
    // server not saying, the app goes on.
    try {
      final c = await api.appConfig();
      if (olderThan(version, c.minVersion)) return _set(AuthState.outdated);
    } on Object {
      // Asked again next time the app opens (an answer not understood too).
    }
    if (t == null) return _set(AuthState.signedOut);
    transport.token = t;
    restored = true;
    session++;
    try {
      _keptPersonal = await store.keys.read('personal') == '1';
    } on Object {
      _keptPersonal = false;
    }
    try {
      me = await api.me();
      await _keepMe();
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

  /// Opened from what was kept: the server asked, its answer taking over.
  Future<void> _checkKept() async {
    final at = session;
    try {
      final c = await api.appConfig();
      if (at == session && olderThan(version, c.minVersion)) return _set(AuthState.outdated);
    } on Object {
      // Asked again next time.
    }
    try {
      final m = await api.me();
      if (at != session) return;
      me = m;
      await _keepMe();
      notifyListeners();
    } on ApiError catch (e) {
      if (at == session && e.status == 401) await _forgetQuietly();
    } on Object {
      // Offline: what was kept stands.
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
    await _signedIn(s.token, personal: s.personal == true);
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
    // Someone in no business, or on a phone not yet proved for their
    // businesses, has their own side: personal, and You opens.
    await _signedIn(s.token, personal: s.personal == true);
  }

  /// The person's own side (docs/spec/taskiem-for-everyone.md §6, §7): a
  /// session in no business. Opened offline, before the server has said
  /// who it is, what the phone kept says.
  bool get personal => me == null ? _keptPersonal : me!.personal == true;

  /// The session kept on this phone is a personal one (You).
  bool _keptPersonal = false;

  /// The phone way in (§5.1): a 6-digit code to [phone], by WhatsApp where
  /// the number is on it, else by SMS; [sms] asks for SMS. Answers how it
  /// went (`whatsapp` or `sms`).
  Future<String> sendPhoneCode(String phone, {bool sms = false, String? chatCode}) async {
    final r = await api.phoneStart(body: PhoneStartRequest(phone: phone, via: sms ? 'sms' : null, chatCode: chatCode));
    return r.sentVia;
  }

  /// Checks the code: a number with an account signs in to it (null comes
  /// back); a new number answers the ticket that makes its account.
  Future<String?> verifyPhoneCode(String phone, String code) async {
    final r = await api.phoneVerify(body: PhoneVerifyRequest(phone: phone, code: code, bearer: true, device: await store.device()));
    if (r.status == 'new') {
      final ticket = r.ticket;
      if (ticket == null) throw ApiError(500, {'error': 'Something went wrong. Try again.'});
      return ticket;
    }
    await _signedIn(r.token, personal: r.personal ?? true);
    return null;
  }

  /// A personal account on a new number: its name, 18 or over, the terms.
  Future<void> createPersonal(String ticket, String name) async {
    final r = await api.phoneCreate(
      body: PhoneCreateRequest(ticket: ticket, name: name, adult: true, terms: true, bearer: true, device: await store.device()),
    );
    await _signedIn(r.token, personal: r.personal ?? true);
  }

  /// Back to the person's own side, **You** (POST /v1/me/switch).
  Future<void> switchToYou() async {
    final s = await api.switchBusiness(body: const SwitchBusinessRequest(personal: true));
    final restoredBefore = restored;
    await _signedIn(s.token, personal: true);
    restored = restoredBefore;
  }

  /// Start something (§18): a business ([kind] `business`, a [name] and
  /// what it does) or workflows (an optional name). The session moves into
  /// it (POST /v1/me/start, answered with its token).
  Future<void> startSomething({required String kind, String? name, List<String>? kinds}) async {
    final r = await api.startSomething(body: StartSomethingRequest(kind: kind, name: name == null || name.isEmpty ? null : name, kinds: kinds));
    await _signedIn(r.token);
  }

  /// Moves this phone's session to another of the person's businesses: the
  /// same session in every way but the business (POST /v1/me/switch).
  Future<void> switchTo(String tenantId) async {
    final s = await api.switchBusiness(body: SwitchBusinessRequest(tenantId: tenantId));
    final restoredBefore = restored;
    await _signedIn(s.token);
    restored = restoredBefore;
  }

  /// Just signed up in the app: the home opens Taskiem's conversation first.
  bool welcome = false;

  /// Who is signed in, read again (a way in added: me.factors).
  Future<void> refreshMe() async {
    try {
      me = await api.me();
      await _keepMe();
      notifyListeners();
    } on Object {
      // As it was; read again next time the app opens.
    }
  }

  /// A session made by signing up (or another way in that answers one).
  Future<void> signedInWith(String? token, {bool personal = false}) => _signedIn(token, personal: personal);

  /// Signs this phone out: the server ends its session, the token goes.
  Future<void> signOut() async {
    try {
      await beforeSignOut?.call();
    } on Object {
      // Signing out goes on whatever happens there.
    }
    try {
      await api.logout();
    } on Exception {
      // Offline or already ended: forgotten here all the same; the server's
      // session then ends by itself, and Your phones can end it sooner.
    }
    await _forget();
  }

  /// [personal]: what the sign-in said the session is, for when who it is
  /// cannot be read just now (offline): You opens, not a business.
  Future<void> _signedIn(String? token, {bool personal = false}) async {
    if (token == null) throw ApiError(500, {'error': 'Signed in, but no session came back. Try again.'});
    transport.token = token;
    await store.keep(token);
    _keptPersonal = personal;
    try {
      await store.keys.write('personal', personal ? '1' : null);
    } on Object {
      // Read from the server when it can be.
    }
    restored = false;
    session++;
    // Signed in: the session is good even if who-am-I cannot be read just
    // now (offline, held); the screens read it again.
    try {
      me = await api.me();
      await _keepMe();
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
    _keptPersonal = false;
    session++;
    await store.keep(null);
    try {
      await store.keys.write('personal', null);
    } on Object {
      // The token is gone: nothing is opened with it.
    }
    try {
      await onForget?.call();
    } on Object {
      // Signing out goes on whatever happens here.
    }
    // Nothing of any business stays on the phone once signed out.
    final open = local;
    local = null;
    try {
      await store.keys.write('scope', null);
      await LocalStore.wipeAll(store.keys, open: [?open]);
    } on Object {
      // The folders could not be read: the keys are gone, so the stores
      // cannot be opened again; the phone clears them in time.
    }
    _set(AuthState.signedOut);
  }

  void _set(AuthState s) {
    state = s;
    notifyListeners();
  }
}
