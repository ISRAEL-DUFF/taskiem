// Taskiem's chat as a phone app (docs/spec/mobile-app.md): the web chat,
// drawn and moving the same.

import 'package:flutter/material.dart' show MaterialApp;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import 'api/transport.dart';
import 'app/auth.dart';
import 'app/config.dart';
import 'app/lock.dart';
import 'app/prefs.dart';
import 'app/session.dart';
import 'chat/chats.dart';
import 'screens/signed_in.dart';
import 'screens/sign_in.dart';
import 'screens/sign_up.dart';
import 'theme/theme.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  // The bars over the app take the web's `theme-color`: pine, light icons.
  SystemChrome.setSystemUIOverlayStyle(const SystemUiOverlayStyle(
    statusBarColor: Color(0x00000000),
    statusBarIconBrightness: Brightness.light,
    statusBarBrightness: Brightness.dark,
  ));
  final auth = Auth(transport: Transport(base: taskiemUrl), store: SessionStore(const SecureKeyStore()), version: appVersion);
  final prefs = Prefs(const SecureKeyStore())..load();
  auth.start();
  runApp(TaskiemApp(auth: auth, unlocker: PhoneUnlocker(), prefs: prefs));
}

class TaskiemApp extends StatelessWidget {
  const TaskiemApp({super.key, this.mode = TkMode.light, this.home, this.auth, this.unlocker, this.prefs});

  /// The theme when there are no [prefs] (tests show one theme).
  final TkMode mode;

  /// The person's choices on this phone: Appearance.
  final Prefs? prefs;

  /// The first screen (tests show one screen alone).
  final Widget? home;

  /// Signing in, which decides the screen when no [home] is given.
  final Auth? auth;

  /// The phone's own unlock, for the app lock.
  final Unlocker? unlocker;

  @override
  Widget build(BuildContext context) {
    // MaterialApp only for what text fields need (localizations, selection);
    // nothing on screen is Material's.
    return MaterialApp(
      title: 'Taskiem',
      debugShowCheckedModeBanner: false,
      builder: (context, child) => _Themed(
        prefs: prefs,
        fallback: mode,
        // The web's body: Inter 14 px, line height 1.55, the text colour.
        child: Builder(
          builder: (context) => DefaultTextStyle(
            style: tkText(size: 14, color: TkTheme.tokens(context).text).copyWith(decoration: TextDecoration.none),
            child: child!,
          ),
        ),
      ),
      home: home ?? (auth == null ? const SignInScreen() : _Root(auth: auth!, unlocker: unlocker, prefs: prefs)),
    );
  }
}

/// Sign in, or the app: whichever the session says.
class _Root extends StatelessWidget {
  const _Root({required this.auth, this.unlocker, this.prefs});
  final Auth auth;
  final Unlocker? unlocker;
  final Prefs? prefs;

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: auth,
      builder: (context, _) => switch (auth.state) {
        // Opening: the pine the sign-in page starts with, for a moment.
        AuthState.checking => ColoredBox(color: TkTheme.tokens(context).brand),
        AuthState.signedOut => _SignedOut(auth: auth),
        AuthState.outdated => const _Outdated(),
        AuthState.signedIn => unlocker == null
            ? _Home(auth: auth, prefs: prefs)
            : AppLock(active: true, unlocker: unlocker!, startLocked: auth.restored, child: _Home(auth: auth, prefs: prefs)),
      },
    );
  }
}

/// Signed out: signing in, or signing up when the server has it open
/// (GET /v1/signup), as the web offers "Create an account".
class _SignedOut extends StatefulWidget {
  const _SignedOut({required this.auth});
  final Auth auth;

  @override
  State<_SignedOut> createState() => _SignedOutState();
}

class _SignedOutState extends State<_SignedOut> {
  bool _signup = false;
  bool _open = false;

  @override
  void initState() {
    super.initState();
    widget.auth.api.signupOptions().then((o) {
      if (mounted) setState(() => _open = o.enabled);
    }, onError: (_) {});
  }

  @override
  Widget build(BuildContext context) {
    // Android's back from signing up returns to signing in, not out of the app.
    return PopScope(
      canPop: !_signup,
      onPopInvokedWithResult: (popped, _) {
        if (!popped && _signup) setState(() => _signup = false);
      },
      child: _signup
          ? SignUpScreen(auth: widget.auth, onSignIn: () => setState(() => _signup = false))
          : SignInScreen(auth: widget.auth, signup: _open, onSignUp: () => setState(() => _signup = true)),
    );
  }
}

/// The theme in force: the person's choice, or the phone's when they chose
/// Match this device, following it as it changes.
class _Themed extends StatelessWidget {
  const _Themed({required this.prefs, required this.fallback, required this.child});
  final Prefs? prefs;
  final TkMode fallback;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final p = prefs;
    if (p == null) return TkTheme(mode: fallback, child: child);
    return ListenableBuilder(
      listenable: p,
      builder: (context, _) {
        final dark = switch (p.theme) {
          ThemeChoice.light => false,
          ThemeChoice.dark => true,
          ThemeChoice.system => MediaQuery.platformBrightnessOf(context) == Brightness.dark,
        };
        return TkTheme(mode: dark ? TkMode.dark : TkMode.light, child: child);
      },
    );
  }
}

/// This app is older than the server serves: the brand on pine, and why.
class _Outdated extends StatelessWidget {
  const _Outdated();

  @override
  Widget build(BuildContext context) {
    const white = Color(0xFFFFFFFF);
    return ColoredBox(
      color: TkTheme.tokens(context).brand,
      child: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text('Update Taskiem', textAlign: TextAlign.center, style: tkText(size: 24, weight: 700, lineHeight: 1.2, color: white)),
              const SizedBox(height: 12),
              Text(
                'This version of the app is older than Taskiem works with. Update it from the Play Store or the App Store, then open it again.',
                textAlign: TextAlign.center,
                style: tkText(size: 14, color: white),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Signed in: the chats; the menu opens the app's settings.
class _Home extends StatefulWidget {
  const _Home({required this.auth, this.prefs});
  final Auth auth;
  final Prefs? prefs;

  @override
  State<_Home> createState() => _HomeState();
}

class _HomeState extends State<_Home> {
  late final ChatsData _chats = ChatsData(widget.auth);

  @override
  Widget build(BuildContext context) {
    return ChatsScreen(
      data: _chats,
      onMenu: () => Navigator.of(context).push(PageRouteBuilder<void>(
        pageBuilder: (_, _, _) => SignedInScreen(auth: widget.auth, prefs: widget.prefs),
      )),
    );
  }
}
