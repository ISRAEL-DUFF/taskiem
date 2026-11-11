// Taskiem's chat as a phone app (docs/spec/mobile-app.md): the web chat,
// drawn and moving the same.

import 'package:flutter/material.dart' show MaterialApp;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import 'api/transport.dart';
import 'app/auth.dart';
import 'app/config.dart';
import 'app/session.dart';
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
  final auth = Auth(transport: Transport(base: taskiemUrl), store: SessionStore(const SecureKeyStore()));
  auth.start();
  runApp(TaskiemApp(auth: auth));
}

class TaskiemApp extends StatelessWidget {
  const TaskiemApp({super.key, this.mode = TkMode.light, this.home, this.auth});

  final TkMode mode;

  /// The first screen (tests show one screen alone).
  final Widget? home;

  /// Signing in, which decides the screen when no [home] is given.
  final Auth? auth;

  @override
  Widget build(BuildContext context) {
    // MaterialApp only for what text fields need (localizations, selection);
    // nothing on screen is Material's.
    return MaterialApp(
      title: 'Taskiem',
      debugShowCheckedModeBanner: false,
      builder: (context, child) => TkTheme(
        mode: mode,
        // The web's body: Inter 14 px, line height 1.55, the text colour.
        child: Builder(
          builder: (context) => DefaultTextStyle(
            style: tkText(size: 14, color: TkTheme.tokens(context).text).copyWith(decoration: TextDecoration.none),
            child: child!,
          ),
        ),
      ),
      home: home ?? (auth == null ? const SignInScreen() : _Root(auth: auth!)),
    );
  }
}

/// Sign in, or the app: whichever the session says.
class _Root extends StatelessWidget {
  const _Root({required this.auth});
  final Auth auth;

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: auth,
      builder: (context, _) => switch (auth.state) {
        // Opening: the pine the sign-in page starts with, for a moment.
        AuthState.checking => ColoredBox(color: TkTheme.tokens(context).brand),
        AuthState.signedOut => _SignedOut(auth: auth),
        AuthState.signedIn => SignedInScreen(auth: auth),
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
    if (_signup) return SignUpScreen(auth: widget.auth, onSignIn: () => setState(() => _signup = false));
    return SignInScreen(auth: widget.auth, signup: _open, onSignUp: () => setState(() => _signup = true));
  }
}
