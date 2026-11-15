// The app lock (docs/spec/mobile-app.md §5.2), as WhatsApp's: off until the
// person turns it on in Settings. On, opening the app with a kept session,
// or coming back after the time they chose (at once, a minute, half an
// hour), asks for the phone's own fingerprint, face or PIN, so a phone left
// on a counter does not open the business's chats. It is the phone's lock,
// not the server's step-up. A phone with no lock of its own set opens
// without it. A dialog, the notification shade or the phone's own picker
// over the app is not being away: nothing is covered or asked for then.

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:local_auth/local_auth.dart';

import '../kit/kit.dart';
import '../theme/theme.dart';

/// The times the lock can wait, as WhatsApp's: at once, a minute, half an hour.
const lockTimes = [Duration.zero, Duration(minutes: 1), Duration(minutes: 30)];

/// The time it waits when turned on without a choice.
const lockAfter = Duration(minutes: 1);

/// How a lock time is said in Settings.
String lockTimeLabel(Duration d) => d == Duration.zero
    ? 'Immediately'
    : d.inMinutes == 1
    ? 'After 1 minute'
    : 'After ${d.inMinutes} minutes';

int _outside = 0;

const _window = MethodChannel('taskiem/window');

/// Whether the app switcher may show a picture of the screens: not with the
/// lock on. On Android the phone leaves the picture out (13 and later); an
/// iPhone takes it as the app goes, so the app covers itself then
/// ([AppLock]). A test stands in for it.
Future<void> Function(bool hide) hideFromRecents = (hide) async {
  if (kIsWeb || defaultTargetPlatform != TargetPlatform.android) return;
  try {
    await _window.invokeMethod<void>('recents', hide);
  } on Object {
    // An older app shell: the picture shows.
  }
};

/// Whether the app covers itself as soon as it is not in front (the
/// switcher opening, a call): on an iPhone, where the switcher's picture is
/// taken then. Android leaves the picture out instead, so a dialog, the
/// notification shade or the phone's fingerprint prompt over the app never
/// covers it there.
bool get coverWhenInactive => !kIsWeb && defaultTargetPlatform == TargetPlatform.iOS;

/// Runs [f], which opens the phone's own screen over the app (the camera,
/// the photo picker, a file picker, the share sheet), so that going there
/// and back is not counted as being away: the app neither covers itself nor
/// locks, as WhatsApp does not when a photo is picked.
Future<T> stepOut<T>(Future<T> Function() f) async {
  _outside++;
  try {
    return await f();
  } finally {
    // Only going away is checked against it: coming back needs nothing.
    _outside--;
  }
}

/// The phone's own unlock: local_auth, or a stand-in in tests.
abstract class Unlocker {
  /// Whether this phone has a lock of its own to ask for.
  Future<bool> available();

  /// Asks for it; true when the person unlocked.
  Future<bool> unlock();
}

class PhoneUnlocker implements Unlocker {
  final _auth = LocalAuthentication();

  @override
  Future<bool> available() async {
    try {
      return await _auth.isDeviceSupported();
    } on PlatformException {
      return false;
    }
  }

  @override
  Future<bool> unlock() async {
    try {
      return await _auth.authenticate(localizedReason: 'Unlock Taskiem', persistAcrossBackgrounding: true);
    } on LocalAuthException catch (e) {
      // No lock set on the phone after all: nothing to ask for.
      if (e.code == LocalAuthExceptionCode.noCredentialsSet) return true;
      return false;
    } on PlatformException {
      return false;
    }
  }
}

/// Covers [child] while locked, keeping it as it was underneath (every
/// screen open stays open, its place kept). It sits above the app's
/// screens, so nothing opened on top of the chats shows past it. [active]
/// is whether there is anything to lock (signed in, with the lock turned
/// on); [now] is the clock, replaceable in tests.
class AppLock extends StatefulWidget {
  const AppLock({super.key, required this.child, required this.active, required this.unlocker, this.startLocked = true, this.after = lockAfter, this.signedIn, this.now = DateTime.now});

  final Widget child;
  final bool active;

  /// Whether someone is signed in, with the lock on or off. Null: as
  /// [active]. The lock turned on in Settings does not ask at once; a kept
  /// session opened does.
  final bool? signedIn;

  /// How long the app may be away before it locks (zero: at once).
  final Duration after;

  /// Whether to ask at once: a session kept from before is opened with the
  /// phone's lock; one the person signed in to just now is not.
  final bool startLocked;
  final Unlocker unlocker;
  final DateTime Function() now;

  @override
  State<AppLock> createState() => AppLockState();
}

class AppLockState extends State<AppLock> with WidgetsBindingObserver {
  late bool locked = widget.startLocked;
  DateTime? _away;
  bool _asking = false;

  /// The app is not in front, with the lock on: its screens are covered,
  /// so the phone's app switcher shows the brand and not a conversation or
  /// an amount. With the lock off nothing is covered, as on WhatsApp.
  bool _behind = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    hideFromRecents(widget.active);
    _ask();
  }

  @override
  void didUpdateWidget(AppLock oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.active == widget.active) return;
    hideFromRecents(widget.active);
    _away = null;
    _behind = false;
    if ((oldWidget.signedIn ?? oldWidget.active) == (widget.signedIn ?? widget.active)) {
      // The lock turned on or off in Settings: nothing asked now.
      setState(() => locked = false);
      return;
    }
    // Signed in (a kept session opened, or signed in just now), or out.
    setState(() => locked = widget.active && widget.startLocked);
    _ask();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    switch (state) {
      case AppLifecycleState.hidden:
      case AppLifecycleState.paused:
        // Gone to another app or the home screen; not to the phone's own
        // picker opened from here.
        if (!widget.active || _outside > 0) return;
        _away ??= widget.now();
        if (!_behind) setState(() => _behind = true);
      case AppLifecycleState.resumed:
        if (_behind) setState(() => _behind = false);
        final away = _away;
        _away = null;
        if (widget.active && away != null && widget.now().difference(away) >= widget.after) {
          // The keyboard goes too: nothing typed shows over the lock.
          FocusManager.instance.primaryFocus?.unfocus();
          setState(() => locked = true);
          _ask();
        }
      case AppLifecycleState.inactive:
        // A dialog, the notification shade, the phone's fingerprint prompt:
        // still in the app, nothing covered or counted. An iPhone's switcher
        // takes its picture now, so there, with the lock on, it is covered.
        if (coverWhenInactive && widget.active && _outside == 0 && !_behind) setState(() => _behind = true);
      case AppLifecycleState.detached:
        break;
    }
  }

  Future<void> _ask() async {
    if (_asking || !locked) return;
    if (!widget.active || !await widget.unlocker.available()) {
      if (mounted) setState(() => locked = false);
      return;
    }
    _asking = true;
    final ok = await widget.unlocker.unlock();
    _asking = false;
    if (mounted && ok) setState(() => locked = false);
  }

  @override
  Widget build(BuildContext context) {
    final asked = widget.active && locked;
    final covered = asked || (_behind && widget.active);
    // Offstage: not drawn, not pressed, not read out, but kept.
    return Stack(
      fit: StackFit.expand,
      children: [
        Offstage(offstage: covered, child: TickerMode(enabled: !covered, child: widget.child)),
        if (asked) _LockScreen(onUnlock: _ask) else if (covered) const _AwayCover(),
      ],
    );
  }
}

/// What the app switcher shows: the mark on pine, and nothing of the person's.
class _AwayCover extends StatelessWidget {
  const _AwayCover();

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ColoredBox(
      color: t.brand,
      child: Center(child: SvgPicture.asset('assets/brand/mark.svg', width: 64, height: 64)),
    );
  }
}

/// The brand on pine, the mark, and Unlock in lime (lime belongs on pine).
class _LockScreen extends StatelessWidget {
  const _LockScreen({required this.onUnlock});
  final VoidCallback onUnlock;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: SystemUiOverlayStyle.light,
      child: ColoredBox(
        color: t.brand,
        child: SafeArea(
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 24),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Center(child: SvgPicture.asset('assets/brand/mark.svg', width: 64, height: 64)),
                const SizedBox(height: 20),
                Text('Taskiem is locked', textAlign: TextAlign.center, style: tkText(size: 18, weight: 600, lineHeight: 1.35, color: const Color(0xFFFFFFFF))),
                const SizedBox(height: 24),
                Semantics(
                  button: true,
                  child: TkPress(
                    onTap: onUnlock,
                    child: Container(
                      constraints: const BoxConstraints(minHeight: 44),
                      alignment: Alignment.center,
                      decoration: BoxDecoration(color: t.brandDark, borderRadius: BorderRadius.circular(Tk.radiusSm)),
                      child: Text('Unlock', style: tkText(size: 13.5, weight: 500, lineHeight: 1.2, color: t.brandDarkText)),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
