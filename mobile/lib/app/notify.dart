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
// - It is also a device of the person's own side, You
//   (docs/spec/taskiem-for-everyone.md §9, "push on your side"): from a
//   personal session PUT /v1/me/push registers it there (kinds ["you"]); from
//   a business session the same with ?side=you, so a business answering the
//   person's chat reaches them while they work in a business. Its pushes
//   have no business and open /you/chats/{id}.
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

/// The kind of a push on the person's own side (a business wrote to them).
const youKind = 'you';

const kindWords = {
  'you': ('Chats with businesses', 'When a business you buy from writes to you'),
  'person': ('Chats with people', 'When someone you chat with writes to you'),
  'request': ('Message requests', 'When someone new writes to you first'),
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
  String? get business => (data['business'] ?? '').isEmpty ? null : data['business'];
}

/// The conversation on You a notification's link opens (`/you/chats/{id}`),
/// or null.
String? youChatOf(String? link) {
  final m = RegExp(r'^/you/chats/([A-Za-z0-9\-]{1,80})$').firstMatch(link ?? '');
  return m?[1];
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

  /// Takes the notifications shown off the phone (signing out).
  Future<void> clearShown();
}

/// Whether [link] is a path of the app a notification may open (the
/// worker's safeLink): never another site.
bool appPath(String? link) =>
    link != null && RegExp(r'^/(?!/)[A-Za-z0-9\-_/]{0,120}(\?[a-z_]{1,20}=[A-Za-z0-9\-]{1,80})?$').hasMatch(link);

final _uuid = RegExp(r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');

class PhoneNotifications extends ChangeNotifier {
  PhoneNotifications({required this.auth, required this.keys, this.platform}) {
    auth.addListener(_authChanged);
  }

  // Who is signed in known later (offline at signing in): start then.
  void _authChanged() {
    if (_wanted && auth.state == AuthState.signedIn && _scope.isNotEmpty && _started != auth.session) unawaited(start());
  }

  /// The start running, which a second caller waits for (the home asks
  /// whether to ask once only when it is done).
  Future<void>? _starting;
  int _startingFor = -1;

  /// A home asked to start (start waits for the business to be known).
  bool _wanted = false;

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

  /// A personal session: this phone's notifications are You's.
  bool get personal => auth.personal;

  /// From a business session: this phone is also a device of the person's
  /// own side, so their chats with businesses reach it here.
  bool youOn = false;

  /// The person's own devices (You's), read from a business session.
  PushDevices? youView;

  /// The device this phone is on the person's own side: in a personal
  /// session [mine].
  PushDevice? get youMine {
    if (personal) return mine;
    final id = _youDevice;
    return id == null ? null : youView?.devices.where((d) => d.id == id).firstOrNull;
  }

  /// Notifications for You are on here: from a personal session the switch
  /// itself, from a business session the one for You.
  bool get youIsOn => personal ? isOn : youOn;

  String? _youDevice;
  String? _youToken;

  /// A page to open, once the home is there: a tapped notification's.
  String? pending;

  /// Says a notification that came with the app in front (the home shows a toast).
  void Function(PushNote note)? onArrived;

  /// The conversation on the screen (its tag: a link, or `c-` and a customer):
  /// a notification for it shows nothing.
  String? showing;
  Object? _showingBy;

  /// [by] (a screen) shows the conversation [tag].
  void show(String tag, Object by) {
    showing = tag;
    _showingBy = by;
  }

  /// [by] closes: what it showed, if nothing newer took its place.
  void unshow(Object by) {
    if (!identical(_showingBy, by)) return;
    showing = null;
    _showingBy = null;
  }

  String? _device;
  String? _token;
  int _started = -1;
  final _subs = <StreamSubscription<Object?>>[];

  String get _tenant => '${auth.me?.tenantId ?? ''}';

  /// Whose keys this session's are: its business's, or `you` (personal).
  String get _scope => personal ? youKind : _tenant;
  String get _onKey => 'push-on:$_scope';
  String get _tokenKey => 'push-token:$_scope';
  String get _askedKey => 'push-asked:$_scope';
  static const _youOnKey = 'push-on:$youKind';
  static const _youTokenKey = 'push-token:$youKind';

  /// Starts what notifications need once signed in, for this session's
  /// business: Firebase, this phone's device, the taps.
  Future<void> start() {
    final running = _starting;
    if (running != null && _startingFor == auth.session) return running;
    final f = _start();
    _starting = f;
    _startingFor = auth.session;
    return f.whenComplete(() {
      if (identical(_starting, f)) _starting = null;
    });
  }

  Future<void> _start() async {
    final p = platform;
    _wanted = true;
    if (p == null || auth.session == _started) return;
    // Not before the business is known: its keys are per business (offline
    // at signing in, me comes later and start runs again). A personal
    // session's are You's.
    if (_scope.isEmpty) return;
    _started = auth.session;
    final at = auth.session;
    // Another business (or person): nothing of the last one's stands.
    isOn = false;
    blocked = false;
    view = null;
    _device = null;
    _token = null;
    youOn = false;
    youView = null;
    _youDevice = null;
    _youToken = null;
    final AppConfig c;
    try {
      c = await auth.api.appConfig();
    } on Object {
      if (_started == at) _started = -1; // asked again next time
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
      // A tap for another business switches it: this session's start ends.
      if (first != null) await _tapped(first);
      if (at != auth.session) return;
    }
    blocked = await p.permission() == PushPermission.denied;
    isOn = await keys.read(_onKey) == '1';
    _token = await keys.read(_tokenKey);
    if (!personal) {
      youOn = await keys.read(_youOnKey) == '1';
      _youToken = await keys.read(_youTokenKey);
    }
    if (at != auth.session) return;
    notifyListeners();
    if (isOn) {
      // Still a device of this business? Found by its token, never added
      // back if removed from another device; a changed token replaces it.
      final now = await p.token();
      if (now == null || at != auth.session) return;
      try {
        await _register(now, known: now == _token, replaces: now == _token ? null : _token);
      } on ApiError catch (e) {
        if (e.status == 404) await _forgetHere();
      } on Object {
        // Offline: as it was.
      }
    }
    // From a business, the person's own side too, while it is on here.
    if (!personal && youOn) {
      final now = await p.token();
      if (now != null && at == auth.session) {
        try {
          await _registerYou(now, known: now == _youToken, replaces: now == _youToken ? null : _youToken);
        } on ApiError catch (e) {
          if (e.status == 404) await _forgetYou();
        } on Object {
          // Offline: as it was.
        }
      }
    }
    await load();
  }

  /// The person's devices (and, from a business, their own side's).
  Future<void> load() async {
    try {
      view = await auth.api.listPush();
      notifyListeners();
    } on Object {
      // Shown when it can be.
    }
    if (personal || available != true) return;
    try {
      youView = await auth.api.listPush(side: youKind);
      notifyListeners();
    } on Object {
      // Shown when it can be.
    }
  }

  /// This phone on the person's own side, from a business session
  /// (`?side=you`): the business's answers in their chats reach it here.
  Future<void> _registerYou(String token, {bool known = false, String? replaces}) async {
    final name = (await auth.store.device()).name;
    final v = await auth.api.putPush(
      side: youKind,
      body: PutPushRequest(
        kind: 'fcm',
        token: token,
        platform: platform!.platform,
        kinds: const [youKind],
        label: known ? null : name,
        known: known ? true : null,
        replaces: replaces,
      ),
    );
    _youDevice = v.added;
    _youToken = token;
    youView = v;
    youOn = true;
    await _remember(youKind);
    await keys.write(_youOnKey, '1');
    await keys.write(_youTokenKey, token);
    notifyListeners();
  }

  Future<void> _forgetYou() async {
    youOn = false;
    _youDevice = null;
    await keys.write(_youOnKey, null);
    await keys.write(_youTokenKey, null);
    notifyListeners();
  }

  /// From a business session: the person's chats with businesses reach
  /// this phone too, or no longer.
  Future<void> setYou(bool on) => _run(() async {
    final p = platform;
    if (p == null || available != true) return;
    if (personal) return on ? _turnOn() : _turnOff();
    if (!on) {
      final t = _youToken;
      if (t != null) youView = await auth.api.deletePushEndpoint(side: youKind, body: DeletePushEndpointRequest(token: t));
      await keys.write(_youOffKey, '1');
      return _forgetYou();
    }
    await keys.write(_youOffKey, null);
    final allowed = await p.ask();
    blocked = allowed == PushPermission.denied;
    if (allowed != PushPermission.granted) throw ApiError(0, {'error': _notAllowed(blocked)});
    final t = await p.token();
    if (t == null) throw ApiError(0, {'error': 'This phone could not be set up for notifications. Try again in a moment.'});
    await _registerYou(t);
  });

  static String _notAllowed(bool blocked) => blocked
      ? 'Notifications are off for Taskiem in this phone’s settings. Turn them on there (Settings, Apps, Taskiem, Notifications), then try again.'
      : 'Notifications were not allowed. Turn this on again and choose Allow when the phone asks.';

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
        // On the person's own side, only You's kind.
        kinds: personal ? const [youKind] : null,
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
    if (isOn && token != _token) {
      try {
        await _register(token, replaces: _token);
      } on Object {
        // The next start tries again (the token kept is the old one).
      }
    }
    if (!personal && youOn && token != _youToken) {
      try {
        await _registerYou(token, replaces: _youToken);
      } on Object {
        // The next start tries again.
      }
    }
  }

  /// Turns notifications on for this phone in this business (or on You,
  /// in a personal session). From a business, the person's own chats with
  /// businesses come too, unless they turned that off here.
  Future<void> turnOn() => _run(_turnOn);

  Future<void> _turnOn() async {
    final p = platform;
    if (p == null || available != true) return;
    await _remember();
    await keys.write(_askedKey, '1');
    final allowed = await p.ask();
    blocked = allowed == PushPermission.denied;
    if (blocked) {
      throw ApiError(0, {'error': 'Notifications are off for Taskiem in this phone’s settings. Turn them on there (Settings, Apps, Taskiem, Notifications), then try again.'});
    }
    // Android's "Don't allow", which it may ask again: nothing is on.
    if (allowed != PushPermission.granted) {
      throw ApiError(0, {'error': 'Notifications were not allowed. Turn this on again and choose Allow when the phone asks.'});
    }
    final t = await p.token();
    if (t == null) throw ApiError(0, {'error': 'This phone could not be set up for notifications. Try again in a moment.'});
    await _register(t);
    if (!personal && !youOn && await keys.read(_youOffKey) == null) {
      try {
        await _registerYou(t);
      } on Object {
        // The business's are on; You's can be turned on in Settings.
      }
    }
  }

  /// Turned off for You here by the person, from a business: not turned on
  /// again with the business's.
  static const _youOffKey = 'push-off:$youKind';

  /// Turns them off for this phone in this business (or on You).
  Future<void> turnOff() => _run(_turnOff);

  Future<void> _turnOff() async {
    final t = _token;
    if (t != null) view = await auth.api.deletePushEndpoint(body: DeletePushEndpointRequest(token: t));
    await _forgetHere();
  }

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
    if (d == null || personal) return;
    final kinds = [for (final k in pushKinds) if (k == kind ? take : d.kinds.contains(k)) k];
    view = await auth.api.patchPush(id: d.id, body: PatchPushRequest(kinds: kinds));
  });

  Future<void> remove(String id) => _run(() async {
    view = await auth.api.deletePush(id: id);
  });

  /// Takes one of the person's own devices (You's) off, from a business.
  Future<void> removeYou(String id) => _run(() async {
    if (personal) {
      view = await auth.api.deletePush(id: id);
    } else {
      youView = await auth.api.deletePush(side: youKind, id: id);
    }
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
    // The person's own side too, so their chats reach nobody else here.
    final y = _youToken;
    if (!personal && youOn && y != null) {
      try {
        await auth.api.deletePushEndpoint(side: youKind, body: DeletePushEndpointRequest(token: y));
      } on Object {
        // As above.
      }
    }
    await forgotten();
  }

  /// Signed out, by the person or the server: nothing of theirs stays.
  Future<void> forgotten() async {
    // Off first: a token Firebase changes meanwhile is not registered again.
    isOn = false;
    _device = null;
    _token = null;
    view = null;
    youOn = false;
    _youDevice = null;
    _youToken = null;
    youView = null;
    pending = null;
    tapProblem = null;
    _started = -1;
    _wanted = false;
    try {
      await platform?.forget();
    } on Object {
      // Not reached: the server drops the token when Firebase says it is gone.
    }
    try {
      // What this person was shown stays on no lock screen.
      await platform?.clearShown();
    } on Object {
      // Left: the next person's taps on them open nothing of theirs.
    }
    // The per-business keys are found by what was kept.
    for (final k in await _keptKeys()) {
      try {
        await keys.write(k, null);
      } on Object {
        // Gone while the app runs.
      }
    }
    await keys.write('push-businesses', null);
    for (final k in [_youOnKey, _youTokenKey, _youOffKey, 'push-asked:$youKind']) {
      try {
        await keys.write(k, null);
      } on Object {
        // Gone while the app runs.
      }
    }
    notifyListeners();
  }

  Future<List<String>> _keptKeys() async {
    final list = (await keys.read('push-businesses') ?? '').split(',').where((b) => b.isNotEmpty);
    return [for (final b in list) for (final k in ['push-on', 'push-token', 'push-asked']) '$k:$b'];
  }

  /// A tap (or a toast's Open): its page, in its business.
  Future<void> open(Map<String, String> data) => _tapped(data);

  /// Where the page a tap waits on belongs ('' for the business in use).
  String _pendingFor = '';

  /// A tap that could not be opened, to say once (the home shows it).
  String? tapProblem;

  Future<void> _tapped(Map<String, String> data) async {
    // A notification left from before signing out: nobody's to open now.
    if (auth.state != AuthState.signedIn) return;
    final link = appPath(data['link']) ? data['link']! : '/chat';
    final b = data['business'];
    final business = b != null && _uuid.hasMatch(b) ? b : '';
    pending = link;
    _pendingFor = business;
    if (business.isNotEmpty && business != _tenant) {
      try {
        await auth.switchTo(business);
        if (_tenant == business || _tenant.isEmpty) return; // the new home opens it, in its business
        pending = null;
        tapProblem = 'Taskiem could not open this notification’s business. Switch to it under Settings, Your businesses.';
      } on Object {
        pending = null;
        tapProblem = 'This notification is for another of your businesses, and Taskiem could not open it here. Sign in to that business to see it.';
      }
    }
    notifyListeners();
  }

  void _arrived(PushNote n) {
    // The conversation on the screen, in this business (or on You, which
    // any session opens): it is there already.
    final mine = n.business == null || n.business == _tenant;
    if (mine && n.tag.isNotEmpty && n.tag == showing) return;
    onArrived?.call(n);
  }

  /// The page waiting to be opened, once, when it is for the business in use.
  String? takePending() {
    final p = pending;
    if (p == null || (_pendingFor.isNotEmpty && _pendingFor != _tenant)) return null;
    pending = null;
    return p;
  }

  /// The tap's problem, once.
  String? takeProblem() {
    final p = tapProblem;
    tapProblem = null;
    return p;
  }

  /// Remembers that this business has keys here, to forget them all on
  /// signing out.
  Future<void> _remember([String? scope]) async {
    final b = scope ?? _scope;
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
    auth.removeListener(_authChanged);
    for (final s in _subs) {
      s.cancel();
    }
    super.dispose();
  }
}
