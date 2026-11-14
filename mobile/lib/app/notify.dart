// Notifications on this phone (docs/spec/mobile-app-m4.md N4): the web's
// "Notifications when Taskiem is closed" (web/src/pages/ChatPush.tsx) for the
// app, through Firebase Cloud Messaging.
//
// - Firebase is started from the values the server gives (GET
//   /v1/app/config), so the build has no Firebase file and a server without
//   Firebase simply has no notifications.
// - The phone is a device of the person in each business it is turned on in
//   (PUT /v1/me/push, kind fcm); the switch is per business, as the server
//   keeps devices.
// - A tap opens what the push is about, in its business (switched first).
// - With the app open nothing shows from the system: a toast does, unless the
//   conversation is the one on the screen.

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import 'auth.dart';
import 'session.dart';

/// The kinds of notification a device may take, in the web's order and words.
const pushKinds = ['business', 'customers', 'orders', 'approvals', 'alerts'];

const kindWords = {
  'business': ('Businesses you work with', 'Messages from linked businesses'),
  'customers': ('Customers', 'When a customer writes'),
  'orders': ('Orders and money', 'New orders, bills, payments and reminders'),
  'approvals': ('Approvals', 'When something waits for your yes or no'),
  'alerts': ('Alerts', 'Everything else the bell tells you'),
};

enum PushPermission { granted, denied, notAsked }

/// A notification that came while the app was in front.
class PushNote {
  const PushNote({required this.title, required this.body, required this.data});
  final String title;
  final String body;
  final Map<String, String> data;

  String get link => data['link'] ?? '/chat';
  String get tag => data['tag'] ?? '';
  String? get business => data['business'];
}

/// What the phone's notifications are, behind one face: Firebase on a
/// phone (FirebasePush, lib/app/notify_firebase.dart), a stand-in in a test.
abstract class PushPlatform {
  /// Starts Firebase with the server's values; false where it cannot (a
  /// phone without Google's services).
  Future<bool> start(AppConfigPushFcm values);

  /// "android" or "ios".
  String get platform;

  Future<PushPermission> permission();

  /// Asks the person (Android 13 and later, iPhones).
  Future<PushPermission> ask();

  /// The phone's registration token, or null when Firebase gives none.
  Future<String?> token();

  /// A new token, when Firebase changes it.
  Stream<String> get tokenChanged;

  /// The data of the notification that opened the app from closed, once.
  Future<Map<String, String>?> openedFrom();

  /// Taps on a notification while the app runs behind.
  Stream<Map<String, String>> get opened;

  /// Notifications that come while the app is in front.
  Stream<PushNote> get arrived;

  /// Makes the token worthless everywhere (signing out of this phone).
  Future<void> forget();

  /// Forgets the server's values kept for a closed app's start.
  Future<void> unkeep();
}

/// Whether [link] is a path of the app a notification may open (the
/// worker's safeLink): never another site.
bool appPath(String? link) =>
    link != null && RegExp(r'^/(?!/)[A-Za-z0-9\-_/]{0,120}(\?[a-z_]{1,20}=[A-Za-z0-9\-]{1,80})?$').hasMatch(link);

final _uuid = RegExp(r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');

class PhoneNotifications extends ChangeNotifier {
  PhoneNotifications({required this.auth, required this.keys, this.platform});

  final Auth auth;
  final KeyStore keys;

  /// Null where the app cannot have notifications at all (a test).
  final PushPlatform? platform;

  /// Whether this server and this phone can notify: null while finding out.
  bool? available;

  /// The server has Firebase, but this phone cannot use it (no Google services).
  bool noGoogle = false;

  /// On for this phone in this business.
  bool isOn = false;

  /// The person said no to notifications in the phone's settings.
  bool blocked = false;

  /// The person's devices in this business, phones and browsers.
  PushDevices? view;

  String? error;
  bool busy = false;

  /// The device this phone is in [view].
  PushDevice? get mine {
    final id = _device;
    return id == null ? null : view?.devices.where((d) => d.id == id).firstOrNull;
  }

  /// A page to open, once the home is there: a tapped notification's.
  String? pending;

  /// Says a notification that came with the app in front (the home shows a toast).
  void Function(PushNote note)? onArrived;

  /// The conversation on the screen (its tag: a link, or `c-` and a customer):
  /// a notification for it shows nothing.
  String? showing;

  String? _device;
  String? _token;
  int _started = -1;
  final _subs = <StreamSubscription<Object?>>[];

  String get _tenant => '${auth.me?.tenantId ?? ''}';
  String get _onKey => 'push-on:$_tenant';
  String get _tokenKey => 'push-token:$_tenant';
  String get _askedKey => 'push-asked:$_tenant';

  /// Starts what notifications need once signed in, for this session's
  /// business: Firebase, this phone's device, the taps.
  Future<void> start() async {
    final p = platform;
    if (p == null || auth.session == _started) return;
    _started = auth.session;
    final at = auth.session;
    final AppConfig c;
    try {
      c = await auth.api.appConfig();
    } on Object {
      _started = -1; // asked again next time
      return;
    }
    final fcm = c.push?.fcm;
    if (fcm == null) {
      available = false;
      await p.unkeep();
      return notifyListeners();
    }
    if (!await p.start(fcm)) {
      available = false;
      noGoogle = true;
      return notifyListeners();
    }
    if (at != auth.session) return;
    available = true;
    if (_subs.isEmpty) {
      _subs
        ..add(p.opened.listen(_tapped))
        ..add(p.arrived.listen(_arrived))
        ..add(p.tokenChanged.listen((t) => _renew(t)));
      final first = await p.openedFrom();
      if (first != null) _tapped(first);
    }
    blocked = await p.permission() == PushPermission.denied;
    isOn = await keys.read(_onKey) == '1';
    _token = await keys.read(_tokenKey);
    notifyListeners();
    if (isOn) {
      // Still a device of this business? Found by its token, never added
      // back if removed from another device; a changed token replaces it.
      final now = await p.token();
      if (now == null) return;
      try {
        await _register(now, known: now == _token, replaces: now == _token ? null : _token);
      } on ApiError catch (e) {
        if (e.status == 404) await _forgetHere();
      } on Object {
        // Offline: as it was.
      }
    }
    await load();
  }

  /// The person's devices.
  Future<void> load() async {
    try {
      view = await auth.api.listPush();
      notifyListeners();
    } on Object {
      // Shown when it can be.
    }
  }

  Future<void> _register(String token, {bool known = false, String? replaces}) async {
    final name = (await auth.store.device()).name;
    final v = await auth.api.putPush(
      body: PutPushRequest(
        kind: 'fcm',
        token: token,
        platform: platform!.platform,
        label: known ? null : name,
        known: known ? true : null,
        replaces: replaces,
      ),
    );
    _device = v.added;
    _token = token;
    await _remember();
    view = v;
    isOn = true;
    await keys.write(_onKey, '1');
    await keys.write(_tokenKey, token);
    notifyListeners();
  }

  Future<void> _renew(String token) async {
    if (!isOn || token == _token) return;
    try {
      await _register(token, replaces: _token);
    } on Object {
      // The next start tries again (the token kept is the old one).
    }
  }

  /// Turns notifications on for this phone in this business.
  Future<void> turnOn() => _run(() async {
    final p = platform;
    if (p == null || available != true) return;
    await _remember();
    await keys.write(_askedKey, '1');
    final allowed = await p.ask();
    blocked = allowed == PushPermission.denied;
    if (blocked) {
      throw ApiError(0, {'error': 'Notifications are off for Taskiem in this phone’s settings. Turn them on there (Settings, Apps, Taskiem, Notifications), then try again.'});
    }
    final t = await p.token();
    if (t == null) throw ApiError(0, {'error': 'This phone could not be set up for notifications. Try again in a moment.'});
    await _register(t);
  });

  /// Turns them off for this phone in this business.
  Future<void> turnOff() => _run(() async {
    final t = _token;
    if (t != null) view = await auth.api.deletePushEndpoint(body: DeletePushEndpointRequest(token: t));
    await _forgetHere();
  });

  Future<void> _forgetHere() async {
    isOn = false;
    _device = null;
    await keys.write(_onKey, null);
    await keys.write(_tokenKey, null);
    notifyListeners();
  }

  Future<void> setPreview(bool preview) => _run(() async {
    final d = mine;
    if (d != null) view = await auth.api.patchPush(id: d.id, body: PatchPushRequest(preview: preview));
  });

  Future<void> setKind(String kind, bool take) => _run(() async {
    final d = mine;
    if (d == null) return;
    final kinds = [for (final k in pushKinds) if (k == kind ? take : d.kinds.contains(k)) k];
    view = await auth.api.patchPush(id: d.id, body: PatchPushRequest(kinds: kinds));
  });

  Future<void> remove(String id) => _run(() async {
    view = await auth.api.deletePush(id: id);
  });

  Future<void> _run(Future<void> Function() f) async {
    busy = true;
    error = null;
    notifyListeners();
    try {
      await f();
    } on ApiError catch (e) {
      error = e.message;
    } on Offline {
      error = 'You’re offline. Try again when you’re back online.';
    } on Object {
      error = 'Something went wrong. Try again.';
    }
    busy = false;
    notifyListeners();
  }

  /// Whether to ask once, after signing in: notifications can be had and
  /// were never turned on or declined here.
  Future<bool> shouldAsk() async {
    if (available != true || isOn || blocked) return false;
    return await keys.read(_askedKey) == null;
  }

  /// "Not now": asked again only from Settings.
  Future<void> declined() async {
    await _remember();
    await keys.write(_askedKey, '1');
  }

  /// Before this phone's session ends (Sign out): the server is told while
  /// it still can be, then the token is made worthless in every business.
  Future<void> signingOut() async {
    final t = _token;
    if (isOn && t != null) {
      try {
        await auth.api.deletePushEndpoint(body: DeletePushEndpointRequest(token: t));
      } on Object {
        // Forgetting the token below ends it all the same.
      }
    }
    await forgotten();
  }

  /// Signed out, by the person or the server: nothing of theirs stays.
  Future<void> forgotten() async {
    try {
      await platform?.forget();
    } on Object {
      // Not reached: the server drops the token when Firebase says it is gone.
    }
    isOn = false;
    _device = null;
    _token = null;
    view = null;
    pending = null;
    _started = -1;
    // The per-business keys are found by what was kept.
    for (final k in await _keptKeys()) {
      try {
        await keys.write(k, null);
      } on Object {
        // Gone while the app runs.
      }
    }
    await keys.write('push-businesses', null);
    notifyListeners();
  }

  Future<List<String>> _keptKeys() async {
    final list = (await keys.read('push-businesses') ?? '').split(',').where((b) => b.isNotEmpty);
    return [for (final b in list) for (final k in ['push-on', 'push-token', 'push-asked']) '$k:$b'];
  }

  /// A tap: its page, in its business.
  Future<void> _tapped(Map<String, String> data) async {
    final link = appPath(data['link']) ? data['link']! : '/chat';
    final business = data['business'];
    if (business != null && _uuid.hasMatch(business) && business != _tenant) {
      try {
        pending = link;
        await auth.switchTo(business);
        return; // the new home opens it
      } on Object {
        pending = null;
        error = 'This notification is for another of your businesses, and Taskiem could not open it here. Sign in to that business to see it.';
        notifyListeners();
        return;
      }
    }
    pending = link;
    notifyListeners();
  }

  void _arrived(PushNote n) {
    if (n.tag.isNotEmpty && n.tag == showing) return;
    onArrived?.call(n);
  }

  /// The page waiting to be opened, once.
  String? takePending() {
    final p = pending;
    pending = null;
    return p;
  }

  /// Remembers that this business has keys here, to forget them all on
  /// signing out.
  Future<void> _remember() async {
    final b = _tenant;
    if (b.isEmpty) return;
    try {
      final list = (await keys.read('push-businesses') ?? '').split(',').where((x) => x.isNotEmpty).toSet();
      if (list.add(b)) await keys.write('push-businesses', list.join(','));
    } on Object {
      // Not kept: those keys stay until signed in there again.
    }
  }

  @override
  void dispose() {
    for (final s in _subs) {
      s.cancel();
    }
    super.dispose();
  }
}
