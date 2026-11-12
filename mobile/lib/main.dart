// Taskiem's chat as a phone app (docs/spec/mobile-app.md): the web chat,
// drawn and moving the same.

import 'package:flutter/material.dart' show MaterialApp;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import 'api/transport.dart';
import 'app/auth.dart';
import 'app/config.dart';
import 'kit/keyboard.dart';
import 'app/lock.dart';
import 'app/prefs.dart';
import 'app/session.dart';
import 'api/api.g.dart';
import 'chat/chats.dart';
import 'chat/conversation.dart';
import 'chat/customer.dart';
import 'chat/live.dart';
import 'chat/taskiem.dart';
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
  const TaskiemApp({super.key, this.mode = TkMode.light, this.home, this.auth, this.unlocker, this.prefs, this.now = DateTime.now});

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

  /// The clock the lock counts time away by (a test's own).
  final DateTime Function() now;

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
            // The lock over every screen, not only the first.
            // Above the keyboard when it is up, every screen and sheet.
            child: KeyboardRoom(
              child: auth == null || unlocker == null || home != null
                ? child!
                : ListenableBuilder(
                    listenable: Listenable.merge([auth!, ?prefs]),
                    builder: (context, _) => AppLock(
                      // Signed in, and the lock not turned off here.
                      active: auth!.state == AuthState.signedIn && (prefs?.lock ?? true),
                      unlocker: unlocker!,
                      startLocked: auth!.restored,
                      now: now,
                      child: child!,
                    ),
                  ),
            ),
          ),
        ),
      ),
      home: home ?? (auth == null ? const SignInScreen() : _Root(auth: auth!, prefs: prefs, unlocker: unlocker)),
    );
  }
}

/// Sign in, or the app: whichever the session says. The screens opened on
/// top of the chats belong to the session they were opened in: signed out,
/// or moved to another business, they are closed, and the chats start
/// afresh for the new session.
class _Root extends StatefulWidget {
  const _Root({required this.auth, this.prefs, this.unlocker});
  final Auth auth;
  final Prefs? prefs;
  final Unlocker? unlocker;

  @override
  State<_Root> createState() => _RootState();
}

class _RootState extends State<_Root> {
  late int _session;

  @override
  void initState() {
    super.initState();
    _session = widget.auth.session;
    widget.auth.addListener(_changed);
  }

  @override
  void dispose() {
    widget.auth.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    final auth = widget.auth;
    if (auth.session != _session || auth.state != AuthState.signedIn) {
      _session = auth.session;
      Navigator.of(context).popUntil((r) => r.isFirst);
    }
    setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    final auth = widget.auth;
    return switch (auth.state) {
      // Opening: the pine the sign-in page starts with, for a moment.
      AuthState.checking => ColoredBox(color: TkTheme.tokens(context).brand),
      AuthState.signedOut => _SignedOut(auth: auth),
      AuthState.outdated => const _Outdated(),
      AuthState.signedIn => _Home(key: ValueKey(auth.session), auth: auth, prefs: widget.prefs, unlocker: widget.unlocker),
    };
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
  const _Home({super.key, required this.auth, this.prefs, this.unlocker});
  final Auth auth;
  final Prefs? prefs;
  final Unlocker? unlocker;

  @override
  State<_Home> createState() => _HomeState();
}

class _HomeState extends State<_Home> with WidgetsBindingObserver {
  // The chat as it happens, followed while the app is in front.
  late final ChatLive _live = ChatLive(widget.auth.transport);
  late final ChatsData _chats = ChatsData(widget.auth, live: _live, prefs: widget.prefs);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _live.start();
    _chats.follow(true);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _live.start();
      _chats.follow(true);
      _chats.load();
    } else if (state == AppLifecycleState.paused || state == AppLifecycleState.hidden) {
      _live.stop();
      _chats.follow(false);
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    // Stopped now; let go once the screens closed with this session (which
    // may still hold them this frame) are gone.
    _chats.follow(false);
    _live.stop();
    final chats = _chats, live = _live;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      chats.dispose();
      live.dispose();
    });
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return ChatsScreen(
      data: _chats,
      onMenu: () => Navigator.of(context).push(PageRouteBuilder<void>(
        pageBuilder: (_, _, _) => SignedInScreen(auth: widget.auth, prefs: widget.prefs, unlocker: widget.unlocker),
      )),
      // On a phone the web shows the conversation in the list's place, at
      // once: no transition either way.
      onOpenTaskiem: () => Navigator.of(context).push(PageRouteBuilder<void>(
        transitionDuration: Duration.zero,
        reverseTransitionDuration: Duration.zero,
        pageBuilder: (_, _, _) => _OpenTaskiem(chats: _chats),
      )),
      onOpenCustomer: (c) => Navigator.of(context).push(PageRouteBuilder<void>(
        transitionDuration: Duration.zero,
        reverseTransitionDuration: Duration.zero,
        pageBuilder: (_, _, _) => _OpenCustomer(chats: _chats, id: c.id),
      )),
      onOpen: (c) => Navigator.of(context).push(PageRouteBuilder<void>(
        transitionDuration: Duration.zero,
        reverseTransitionDuration: Duration.zero,
        pageBuilder: (_, _, _) => _Open(chats: _chats, conv: c),
      )),
      onOpenAt: (c, message) => Navigator.of(context).push(PageRouteBuilder<void>(
        transitionDuration: Duration.zero,
        reverseTransitionDuration: Duration.zero,
        pageBuilder: (_, _, _) => _Open(chats: _chats, conv: c, focus: message),
      )),
    );
  }
}

/// An open conversation: its data lives while it is open.
class _Open extends StatefulWidget {
  const _Open({required this.chats, required this.conv, this.focus});
  final ChatsData chats;
  final LinkConversation conv;

  /// Opened at this message.
  final String? focus;

  @override
  State<_Open> createState() => _OpenState();
}

class _OpenState extends State<_Open> {
  late final ConversationData _data = ConversationData(widget.chats, widget.conv, focus: widget.focus);

  @override
  void dispose() {
    _data.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ConversationScreen(
    data: _data,
    // A business tagged in a message: its conversation, in this one's place.
    onOpenLink: (link) {
      final c = widget.chats.conversations.where((c) => c.link == link).firstOrNull;
      if (c == null) return;
      Navigator.of(context).pushReplacement(PageRouteBuilder<void>(
        transitionDuration: Duration.zero,
        reverseTransitionDuration: Duration.zero,
        pageBuilder: (_, _, _) => _Open(chats: widget.chats, conv: c),
      ));
    },
  );
}

/// Taskiem's conversation, open: its data lives while it is open.
class _OpenTaskiem extends StatefulWidget {
  const _OpenTaskiem({required this.chats});
  final ChatsData chats;

  @override
  State<_OpenTaskiem> createState() => _OpenTaskiemState();
}

class _OpenTaskiemState extends State<_OpenTaskiem> {
  late final TaskiemData _data = TaskiemData(widget.chats);

  @override
  void dispose() {
    _data.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => TaskiemScreen(
    data: _data,
    onOpenCustomer: (id) => Navigator.of(context).pushReplacement(PageRouteBuilder<void>(
      transitionDuration: Duration.zero,
      reverseTransitionDuration: Duration.zero,
      pageBuilder: (_, _, _) => _OpenCustomer(chats: widget.chats, id: id),
    )),
    onOpenLink: (c) => Navigator.of(context).pushReplacement(PageRouteBuilder<void>(
      transitionDuration: Duration.zero,
      reverseTransitionDuration: Duration.zero,
      pageBuilder: (_, _, _) => _Open(chats: widget.chats, conv: c),
    )),
  );
}

/// A customer's conversation, open: its data lives while it is open.
class _OpenCustomer extends StatefulWidget {
  const _OpenCustomer({required this.chats, required this.id});
  final ChatsData chats;
  final String id;

  @override
  State<_OpenCustomer> createState() => _OpenCustomerState();
}

class _OpenCustomerState extends State<_OpenCustomer> {
  late final CustomerData _data = CustomerData(widget.chats, widget.id);

  @override
  void dispose() {
    _data.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => CustomerScreen(data: _data);
}
