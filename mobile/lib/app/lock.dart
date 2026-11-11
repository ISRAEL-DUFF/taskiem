// The app lock (docs/spec/mobile-app.md §5.2): opening the app with a kept
// session, or coming back after 5 minutes away, asks for the phone's own
// fingerprint, face or PIN, so a phone left on a counter does not open the
// business's chats. It is the phone's lock, not the server's step-up. A
// phone with no lock of its own set opens without it.

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:local_auth/local_auth.dart';

import '../kit/kit.dart';
import '../theme/theme.dart';

/// How long the app may be away before it locks.
const lockAfter = Duration(minutes: 5);

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
/// is whether there is anything to lock (signed in); [now] is the clock,
/// replaceable in tests.
class AppLock extends StatefulWidget {
  const AppLock({super.key, required this.child, required this.active, required this.unlocker, this.startLocked = true, this.now = DateTime.now});

  final Widget child;
  final bool active;

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

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _ask();
  }

  @override
  void didUpdateWidget(AppLock oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.active == widget.active) return;
    // Signed in (a kept session opened, or signed in just now), or out.
    _away = null;
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
        if (widget.active) _away ??= widget.now();
      case AppLifecycleState.resumed:
        final away = _away;
        _away = null;
        if (away != null && widget.now().difference(away) >= lockAfter) {
          // The keyboard goes too: nothing typed shows over the lock.
          FocusManager.instance.primaryFocus?.unfocus();
          setState(() => locked = true);
          _ask();
        }
      case AppLifecycleState.inactive:
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
    final covered = widget.active && locked;
    // Offstage: not drawn, not pressed, not read out, but kept.
    return Stack(
      fit: StackFit.expand,
      children: [
        Offstage(offstage: covered, child: TickerMode(enabled: !covered, child: widget.child)),
        if (covered) _LockScreen(onUnlock: _ask),
      ],
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
