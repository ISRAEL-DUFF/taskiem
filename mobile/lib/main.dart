// Taskiem's chat as a phone app (docs/spec/mobile-app.md): the web chat,
// drawn and moving the same.

import 'dart:async';

import 'package:flutter/material.dart' show MaterialApp;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_svg/flutter_svg.dart';

import 'api/transport.dart';
import 'app/auth.dart';
import 'app/openers.dart';
import 'app/config.dart';
import 'kit/keyboard.dart';
import 'app/lock.dart';
import 'app/applinks.dart';
import 'app/notify.dart';
import 'app/notify_firebase.dart';
import 'app/prefs.dart';
import 'app/route.dart';
import 'app/session.dart';
import 'app/shortcuts.dart';
import 'app/sound.dart' show chime;
import 'app/store.dart' show LocalStore;
import 'api/api.g.dart';
import 'chat/bills_page.dart' show openBill;
import 'chat/invoices_page.dart' show openInvoice;
import 'chat/money_page.dart' show MoneyPage;
import 'chat/payments_page.dart' show SentPaymentsPage;
import 'chat/payroll_page.dart' show PayrollPage, PayrollRunPage;
import 'chat/pages_page.dart' show PageScreen, PagesPage;
import 'chat/purchases_page.dart' show openPurchase;
import 'chat/storefront_orders_page.dart' show StorefrontOrdersPage, openStorefrontOrder;
import 'chat/trade_orders_page.dart' show openTradeOrder;
import 'chat/order_sheet.dart' show showOrderSheet;
import 'chat/orders_tab.dart' show OrdersTab;
import 'chat/today_tab.dart' show TodayTab;
import 'chat/chats.dart';
import 'chat/conversation.dart';
import 'chat/customer.dart';
import 'chat/links_screen.dart';
import 'chat/my_chat.dart' show MyChatData, MyChatScreen;
import 'chat/explore.dart' show showExplore;
import 'chat/profile_screen.dart';
import 'chat/live.dart';
import 'chat/taskiem.dart';
import 'chat/scan_start.dart' show scanFromHome;
import 'screens/chat_link.dart';
import 'chat/group_chat.dart' show GroupChatData, GroupChatScreen;
import 'chat/group_new.dart' show GroupJoinScreen;
import 'chat/people_chat.dart' show PeopleChatData, PeopleChatScreen;
import 'chat/people_find.dart' show PersonLinkScreen;
import 'school/school_clearance.dart' show GateScreen;
import 'school/school_tab.dart' show SchoolTab;
import 'screens/home_tabs.dart';
import 'screens/signed_in.dart';
import 'screens/you_pin.dart' show PinStepScreen;
import 'screens/forgot_password.dart';
import 'screens/sign_in.dart';
import 'screens/switcher.dart' show showSwitcher;
import 'screens/welcome.dart';
import 'screens/you.dart';
import 'screens/you_links.dart' show openYouPage;
import 'theme/theme.dart';
import 'kit/mobile.dart' show TkRoute, tkPush;
import 'kit/modal.dart' show showTkConfirm, showTkToast;

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  // The bars over the app take the web's `theme-color`: pine, light icons.
  SystemChrome.setSystemUIOverlayStyle(const SystemUiOverlayStyle(
    statusBarColor: Color(0x00000000),
    statusBarIconBrightness: Brightness.light,
    statusBarBrightness: Brightness.dark,
  ));
  // The chats kept on the phone, encrypted (lib/app/store.dart).
  LocalStore.enabled = true;
  final auth = Auth(transport: Transport(base: taskiemUrl), store: SessionStore(const SecureKeyStore()), version: appVersion);
  final prefs = Prefs(const SecureKeyStore())..load();
  // Notifications on this phone (lib/app/notify.dart).
  final notify = PhoneNotifications(auth: auth, keys: const SecureKeyStore(), platform: FirebasePush());
  auth.beforeSignOut = notify.signingOut;
  auth.onForget = () async {
    await prefs.forgetPerson();
    await notify.forgotten();
  };
  // The session and the settings read side by side; the app opens once
  // both are (_Root), so a kept session with the lock on is asked for it.
  auth.start();
  // Links to this server's pages, opened in the app (lib/app/applinks.dart).
  final links = LinkInbox();
  // Scan a code and the person's tools on the app's icon (lib/app/shortcuts.dart).
  final shortcuts = HomeShortcuts(PhoneShortcuts());
  // The phone's own start screen (the mark on pine) stays until the app's
  // first screen is the real one, so there is one opening, not two: the
  // app draws nothing until the session and the settings are read (or a
  // few seconds have gone, on a slow phone: then its own mark on pine).
  WidgetsBinding.instance.deferFirstFrame();
  unawaited(_whenOpen(auth, prefs).timeout(const Duration(seconds: 3), onTimeout: () {}).whenComplete(WidgetsBinding.instance.allowFirstFrame));
  runApp(TaskiemApp(auth: auth, unlocker: PhoneUnlocker(), prefs: prefs, notify: notify, links: links, shortcuts: shortcuts));
}

/// Done once the session is known and the settings are read.
Future<void> _whenOpen(Auth auth, Prefs prefs) async {
  await prefs.ready;
  if (auth.state != AuthState.checking) return;
  final done = Completer<void>();
  void changed() {
    if (auth.state != AuthState.checking && !done.isCompleted) done.complete();
  }

  auth.addListener(changed);
  try {
    await done.future;
  } finally {
    auth.removeListener(changed);
  }
}

class TaskiemApp extends StatelessWidget {
  const TaskiemApp({super.key, this.mode = TkMode.light, this.home, this.auth, this.unlocker, this.prefs, this.notify, this.links, this.shortcuts, this.now = DateTime.now});

  /// The app icon's shortcuts: Scan a code, and the person's tools.
  final HomeShortcuts? shortcuts;

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

  /// Notifications on this phone (none in a test that gives none).
  final PhoneNotifications? notify;

  /// Links that open the app; one that comes signed out waits.
  final LinkInbox? links;

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
                      // Signed in, and the lock turned on here (off
                      // until turned on, as WhatsApp's).
                      // Signed in counts once the settings are read too (the
                      // screens open then), so the lock is decided with them.
                      active: auth!.state == AuthState.signedIn && (prefs?.loaded ?? true) && (prefs?.lock ?? false),
                      after: prefs?.lockAfter ?? lockAfter,
                      signedIn: auth!.state == AuthState.signedIn && (prefs?.loaded ?? true),
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
      home: home ?? (auth == null ? const SignInScreen() : _Root(auth: auth!, prefs: prefs, unlocker: unlocker, notify: notify, links: links, shortcuts: shortcuts)),
    );
  }
}

/// Sign in, or the app: whichever the session says. The screens opened on
/// top of the chats belong to the session they were opened in: signed out,
/// or moved to another business, they are closed, and the chats start
/// afresh for the new session.
class _Root extends StatefulWidget {
  const _Root({required this.auth, this.prefs, this.unlocker, this.notify, this.links, this.shortcuts});
  final Auth auth;
  final Prefs? prefs;
  final Unlocker? unlocker;
  final PhoneNotifications? notify;
  final LinkInbox? links;
  final HomeShortcuts? shortcuts;

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
    widget.prefs?.addListener(_prefs);
  }

  @override
  void dispose() {
    widget.auth.removeListener(_changed);
    widget.prefs?.removeListener(_prefs);
    super.dispose();
  }

  bool _loaded = false;

  void _prefs() {
    if (!_loaded && (widget.prefs?.loaded ?? true)) setState(() => _loaded = true);
  }

  void _changed() {
    final auth = widget.auth;
    if (auth.session != _session || auth.state != AuthState.signedIn) {
      // Signed out, or another business or the person's own account: the
      // icon's shortcuts were the last business's. A business's _Home puts
      // its own back.
      if (auth.state != AuthState.checking) unawaited(widget.shortcuts?.clear());
      _session = auth.session;
      Navigator.of(context).popUntil((r) => r.isFirst);
    }
    setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    final auth = widget.auth;
    // Opening, until the session and the settings are read: the mark on
    // pine, as the phone's own start screen shows it, so one runs on into
    // the other.
    if (auth.state == AuthState.checking || !(widget.prefs?.loaded ?? true)) return const _Opening();
    return switch (auth.state) {
      AuthState.checking => const _Opening(),
      AuthState.signedOut => _SignedOut(auth: auth, links: widget.links),
      AuthState.outdated => const _Outdated(),
      // A personal session: the person's own side (taskiem-for-everyone §7).
      // Just made an account: Set your PIN, or Not now (when it has none).
      AuthState.signedIn when auth.offerPin => PinStepScreen(key: ValueKey('pin ${auth.session}'), auth: auth, onDone: () => setState(() => auth.offerPin = false)),
      AuthState.signedIn when auth.personal => YouHome(key: ValueKey(auth.session), auth: auth, links: widget.links, notify: widget.notify, prefs: widget.prefs),
      AuthState.signedIn => _Home(key: ValueKey(auth.session), auth: auth, prefs: widget.prefs, unlocker: widget.unlocker, notify: widget.notify, links: widget.links, shortcuts: widget.shortcuts),
    };
  }
}

/// Signed out: the phone way in (docs/spec/taskiem-for-everyone.md §5.1),
/// and behind **Sign in with email** today's email, passkey and chat
/// sign-in. Nobody starts a business here any more: a person makes their
/// own account, and starts one from **You** (§17, §18).
class _SignedOut extends StatefulWidget {
  const _SignedOut({required this.auth, this.links});
  final Auth auth;

  /// A password reset's link, or a business's chat link (its card over the
  /// way in), that opened the app while signed out.
  final LinkInbox? links;

  @override
  State<_SignedOut> createState() => _SignedOutState();
}

class _SignedOutState extends State<_SignedOut> {
  // What is open: the phone way in ('phone'), signing in with email ('in'),
  // Forgot password? ('forgot') or a reset link's new password ('reset').
  String _at = 'phone';
  bool _passkeys = true;
  // Forgot password?: what was typed in Email; a reset link's token.
  String _forgotEmail = '';
  String? _reset;

  void _linked() {
    if (!mounted) return;
    final token = widget.links?.takeReset();
    setState(() {
      if (token != null) {
        _reset = token;
        _at = 'reset';
      }
    });
  }

  @override
  void initState() {
    super.initState();
    widget.links?.addListener(_linked);
    // The link that opened the app.
    _linked();
    widget.auth.api.appConfig().then((c) {
      if (mounted) setState(() => _passkeys = c.passkeys);
    }, onError: (_) {});
  }

  @override
  void dispose() {
    widget.links?.removeListener(_linked);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    void go(String at) => setState(() => _at = at);
    // Android's back returns to the phone way in, not out of the app.
    return PopScope(
      canPop: _at == 'phone',
      onPopInvokedWithResult: (popped, _) {
        if (!popped && _at != 'phone') go(_at == 'in' ? 'phone' : 'in');
      },
      child: switch (_at) {
        'in' => SignInScreen(
          auth: widget.auth,
          signup: false,
          passkeys: _passkeys,
          onPhone: () => go('phone'),
          onForgot: (email) {
            _forgotEmail = email;
            go('forgot');
          },
        ),
        'forgot' => ForgotPasswordScreen(auth: widget.auth, email: _forgotEmail, onBack: () => go('in')),
        'reset' when _reset != null => ResetPasswordScreen(key: ValueKey(_reset), auth: widget.auth, token: _reset!, onDone: () => go('in')),
        _ => Builder(
          builder: (context) {
            // A business's chat link waiting: its card over the way in.
            final code = chatCodeOf(widget.links?.peek);
            if (code != null) widget.links!.shownSignedOut = true;
            return WelcomeScreen(key: ValueKey('welcome $code'), auth: widget.auth, onEmail: () => go('in'), chatCode: code);
          },
        ),
      },
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
  const _Home({super.key, required this.auth, this.prefs, this.unlocker, this.notify, this.links, this.shortcuts});
  final HomeShortcuts? shortcuts;
  final Auth auth;
  final Prefs? prefs;
  final Unlocker? unlocker;
  final PhoneNotifications? notify;
  final LinkInbox? links;

  @override
  State<_Home> createState() => _HomeState();
}

class _HomeState extends State<_Home> with WidgetsBindingObserver {
  // The chat as it happens, followed while the app is in front.
  late final ChatLive _live = ChatLive(widget.auth.transport);
  late final ChatsData _chats = ChatsData(widget.auth, live: _live, prefs: widget.prefs);
  final _tabs = GlobalKey<HomeTabsState>();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    // Notifications on this phone: started for this session's business; a
    // tapped one opens its page; one that comes with the app in front is a
    // toast; asked once after signing in.
    // Just signed up in the app: Taskiem's conversation, its first message.
    if (widget.auth.welcome) {
      widget.auth.welcome = false;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => _OpenTaskiem(chats: _chats)));
      });
    }
    // A link to one of this server's pages: opened now, or the one that
    // waited behind signing in.
    widget.links?.addListener(_linked);
    WidgetsBinding.instance.addPostFrameCallback((_) => _linked());
    // The app icon's shortcuts: this person's tools on it, and the one
    // tapped (now, or before signing in) opened.
    widget.shortcuts?.addListener(_shortcut);
    WidgetsBinding.instance.addPostFrameCallback((_) => _shortcut());
    unawaited(_putShortcuts());
    final notify = widget.notify;
    if (notify != null) {
      notify.addListener(_notified);
      notify.onArrived = _arrived;
      WidgetsBinding.instance.addPostFrameCallback((_) async {
        await notify.start();
        if (!mounted) return;
        _notified();
        if (await notify.shouldAsk() && mounted) await _askOnce(notify);
      });
    }
    _live.start();
    _chats.follow(true);
    // What a tool's screen leads to: the shell's own screens.
    Openers.linkedBusinesses = _links;
    Openers.findBusiness = (context) => showExplore(context, chats: _chats, onOpen: (c) => _openConv(context, c));
    Openers.businessProfile = (context) => _profile(context, back: 'Back to Explore');
    Openers.customer = (context, id) => Navigator.of(context).push(TkRoute<void>(pageBuilder: (_, _, _) => _OpenCustomer(chats: _chats, id: id, notify: widget.notify)));
    // Drafting an order needs table.edit; else their shop to look around.
    Openers.conversation = (context, link) {
      final c = _chats.conversations.where((x) => x.link == link).firstOrNull;
      if (c != null) _openConv(context, c);
      return c != null;
    };
    Openers.orderFrom = (context, link, business) =>
        showOrderSheet(context, chats: _chats, link: link, business: business, browse: !_chats.auth.can('table.edit'));
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

  /// A tapped notification's page, or a problem to say.
  void _notified() {
    final notify = widget.notify;
    if (notify == null || !mounted) return;
    final problem = notify.takeProblem();
    if (problem != null) showTkToast(context, problem, error: true);
    final link = notify.takePending();
    if (link != null) _openPath(link);
  }

  void _linked() {
    if (!mounted) return;
    // A password reset's link, signed in: Choose a new password over the
    // app (saving it signs every session out, this one too).
    final reset = widget.links?.takeReset();
    if (reset != null) {
      final nav = Navigator.of(context);
      nav.push(TkRoute<void>(pageBuilder: (_, _, _) => ResetPasswordScreen(auth: widget.auth, token: reset, onDone: nav.pop)));
    }
    final page = widget.links?.take();
    if (page != null) _openPath(page);
  }

  /// Scan a code and up to three of the person's tools, on the app's icon.
  Future<void> _putShortcuts() async {
    final shortcuts = widget.shortcuts;
    final business = _business;
    if (shortcuts == null || business == null) return;
    var tools = <({String command, String name})>[];
    try {
      if (_chats.auth.can('run.start')) {
        tools = [for (final t in (await _chats.auth.api.listTools()).tools) (command: t.command, name: t.name)];
      }
    } on Object {
      // Scan alone, then.
    }
    // Still this business: the session may have moved while they were read.
    if (!mounted || _business != business) return;
    await shortcuts.show(business, tools, recent: widget.prefs?.recentTools ?? const []);
  }

  /// The business this session is in.
  String? get _business => _chats.auth.me?.tenantId?.toString();

  /// A shortcut tapped on the app's icon: Scan a code, or a tool.
  void _shortcut() {
    if (!mounted) return;
    final type = widget.shortcuts?.take();
    if (type == null) return;
    // Put there for another business (or kept from before they named
    // one): not opened in this one, and the icon set again for this one.
    final s = readShortcut(type);
    if (s == null || s.business != _business) {
      showTkToast(context, 'That shortcut was for another business. Use the shortcuts on the icon again: they are this one’s now.', error: true);
      unawaited(_putShortcuts());
      return;
    }
    Navigator.of(context).popUntil((r) => r.isFirst);
    final tool = s.tool;
    if (tool != null) {
      _openTool(tool, const {});
    } else {
      unawaited(scanFromHome(context, auth: _chats.auth, openTool: _openTool));
    }
  }

  /// A tool's sheet in Taskiem's conversation, filled in with [initial].
  void _openTool(String command, Map<String, dynamic> initial) {
    if (!mounted) return;
    final nav = Navigator.of(context);
    nav.popUntil((r) => r.isFirst);
    nav.push(TkRoute<void>(pageBuilder: (_, _, _) => _OpenTaskiem(chats: _chats, openTool: (command: command, initial: initial))));
  }

  void _arrived(PushNote n) {
    if (!mounted) return;
    // The chime the app already has, where the person keeps it on.
    if (widget.prefs?.sound ?? true) unawaited(chime());
    final words = n.body.isEmpty ? n.title : '${n.title}: ${n.body}';
    // Open goes the way a tap does: to its business first.
    showTkToast(context, words, action: 'Open', onAction: () => widget.notify?.open(n.data));
  }

  /// The ask-once question is up: a page to open waits for it.
  bool _asking = false;
  String? _afterAsking;

  Future<void> _askOnce(PhoneNotifications notify) async {
    _asking = true;
    final yes = await showTkConfirm(
      context,
      title: 'Get a notification when a business or customer writes?',
      body: 'Taskiem tells you on this phone, even when the app is closed. You can change this in Settings.',
      ok: 'Yes',
      cancel: 'Not now',
    );
    if (yes) {
      await notify.turnOn();
      if (mounted && notify.error != null) showTkToast(context, notify.error!, error: true);
    } else {
      await notify.declined();
    }
    _asking = false;
    final then = _afterAsking;
    _afterAsking = null;
    if (then != null && mounted) unawaited(_openPath(then));
  }

  /// Opens a page of the app by its web path (a notification's link, the
  /// bell's): its conversation, else the tab it belongs to.
  Future<void> _openPath(String path) async {
    final uri = Uri.tryParse(path);
    if (uri == null || !mounted) return;
    // Not over the ask-once question (closing it would count as Not now).
    if (_asking) {
      _afterAsking = path;
      return;
    }
    final nav = Navigator.of(context);
    nav.popUntil((r) => r.isFirst);
    // A business's chat link: its card, and the person's own chat with it
    // (a business session is its person too, taskiem-for-everyone §8.4).
    final code = chatCodeOf(path);
    if (code != null) {
      unawaited(tkPush<void>(context, ChatLinkScreen(auth: _chats.auth, code: code)));
      return;
    }
    // A person's link (people-chatting §5.2): who it is, and the person's
    // own chat with them, over the business.
    final person = personLinkOf(path);
    if (person != null) {
      unawaited(tkPush<void>(context, PersonLinkScreen(auth: _chats.auth, code: person, openChat: _openPerson)));
      return;
    }
    // A group's link (people-chatting §9): the group, then Join, over the
    // business.
    final group = groupLinkCodeOf(path);
    if (group != null) {
      unawaited(tkPush<void>(context, GroupJoinScreen(auth: _chats.auth, code: group, openGroup: _openGroup)));
      return;
    }
    final route = routeOf(path);
    // A school's orders open over Today: School is in Orders' place.
    final overToday = _school && route.tab == tabOrders;
    _tabs.currentState?.show(overToday ? tabToday : route.tab);
    if (overToday && route.screen == RouteScreen.none) {
      nav.push(TkRoute<void>(pageBuilder: (_, _, _) => StorefrontOrdersPage(auth: _chats.auth)));
      return;
    }
    final auth = _chats.auth;
    final id = route.id;
    switch (route.screen) {
      case RouteScreen.none:
        return;
      case RouteScreen.customer:
        nav.push(TkRoute<void>(pageBuilder: (_, _, _) => _OpenCustomer(chats: _chats, id: id, notify: widget.notify)));
      case RouteScreen.conversation:
        var c = _chats.conversations.where((x) => x.link == id).firstOrNull;
        if (c == null) {
          await _chats.load();
          if (!mounted) return;
          c = _chats.conversations.where((x) => x.link == id).firstOrNull;
        }
        if (c != null && mounted) _openConv(context, c);
      case RouteScreen.taskiem:
        nav.push(TkRoute<void>(pageBuilder: (_, _, _) => _OpenTaskiem(chats: _chats)));
      case RouteScreen.invoice:
        openInvoice(context, auth, id);
      case RouteScreen.bill:
        openBill(context, auth, id);
      case RouteScreen.purchase:
        openPurchase(context, auth, id);
      case RouteScreen.tradeOrder:
        openTradeOrder(context, auth, id);
      case RouteScreen.storefrontOrder:
        openStorefrontOrder(context, auth, id);
      case RouteScreen.payments:
        nav.push(TkRoute<void>(pageBuilder: (_, _, _) => SentPaymentsPage(auth: auth, open: id.isEmpty ? null : id)));
      case RouteScreen.paymentBatch:
        nav.push(TkRoute<void>(pageBuilder: (_, _, _) => SentPaymentsPage(auth: auth, openBatch: id)));
      case RouteScreen.payroll:
        nav.push(TkRoute<void>(pageBuilder: (_, _, _) => id.isEmpty ? PayrollPage(auth: auth) : PayrollRunPage(auth: auth, run: id)));
      case RouteScreen.pages:
        nav.push(TkRoute<void>(pageBuilder: (_, _, _) => id.isEmpty ? PagesPage(auth: auth) : PageScreen(auth: auth, name: id)));
      case RouteScreen.you:
        nav.push(TkRoute<void>(pageBuilder: (_, _, _) => MyChatScreen(data: MyChatData(auth, id, prefs: widget.prefs), notify: widget.notify)));
      case RouteScreen.youPage:
        // The rest of the person's own side: pushed over the business.
        final you = youRouteOf(id);
        if (you == null) return;
        unawaited(openYouPage(context, auth, you, open: _openMine, openPerson: _openPerson, openGroup: _openGroup));
    }
  }

  /// The person's own chat with a business, over the business (a business
  /// session is its person too).
  Future<void> _openMine(String chat, {String? notice, String draft = '', String? tool}) =>
      tkPush<void>(context, MyChatScreen(data: MyChatData(_chats.auth, chat, notice: notice, prefs: widget.prefs), draft: draft, notify: widget.notify, tool: tool));

  /// The person's own chat with a person, over the business: a shared
  /// card's Chat opens their chat with that business, and this phone's
  /// notifications of it show nothing while it is on the screen.
  Future<void> _openPerson(String chat, {PeopleChat? started}) =>
      tkPush<void>(context, PeopleChatScreen(data: PeopleChatData(_chats.auth, chat, started: started), notify: widget.notify, openBusiness: _openMine));

  /// One of the person's own groups, over the business.
  Future<void> _openGroup(String id, {String? focus}) =>
      tkPush<void>(context, GroupChatScreen(data: GroupChatData(_chats.auth, id, focus: focus), notify: widget.notify, openBusiness: _openMine, openPerson: _openPerson));

  @override
  void dispose() {
    widget.notify?.removeListener(_notified);
    widget.links?.removeListener(_linked);
    widget.shortcuts?.removeListener(_shortcut);
    if (widget.notify?.onArrived == _arrived) widget.notify?.onArrived = null;
    WidgetsBinding.instance.removeObserver(this);
    Openers.linkedBusinesses = null;
    Openers.businessProfile = null;
    Openers.findBusiness = null;
    Openers.orderFrom = null;
    Openers.conversation = null;
    Openers.customer = null;
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

  /// A school (Schools on, and they may read it): School in Orders' place
  /// (docs/spec/mobile-app-look.md, "The school"); orders open from Today.
  bool get _school => widget.auth.me?.schools == true && widget.auth.can('school.read');

  @override
  Widget build(BuildContext context) {
    // Someone with the gate role only: the gate is the app.
    if (widget.auth.me?.gateOnly == true) return GateScreen(auth: widget.auth, onSignOut: () => widget.auth.signOut());
    return HomeTabs(
      key: _tabs,
      count: () => _chats.tabCount,
      offline: widget.auth.transport.offline,
      updating: widget.auth.transport.updating,
      counted: _chats,
      tabs: [
        (label: 'Chats', icon: 'b-chat', build: _chatsTab),
        // Today (the web's Home); Updates as WhatsApp's (status and
        // channels) is planned for later: docs/spec/mobile-app-updates.md.
        (label: 'Today', icon: 'b-home', build: (_) => TodayTab(auth: _chats.auth)),
        (
          label: 'Money',
          icon: 'b-money',
          build: (_) => MoneyPage(auth: _chats.auth, tab: true),
        ),
        if (_school)
          (label: 'School', icon: 'b-school', build: (_) => SchoolTab(auth: _chats.auth))
        else
          (
            label: 'Orders',
            icon: 'b-box',
            build: (_) => OrdersTab(auth: _chats.auth),
          ),
      ],
    );
  }

  void _profile(BuildContext context, {String back = 'Back to Chat'}) => Navigator.of(context).push(TkRoute<void>(
    pageBuilder: (_, _, _) => ProfileScreen(chats: _chats, backLabel: back),
  ));

  void _openConv(BuildContext context, LinkConversation c) => Navigator.of(context).push(TkRoute<void>(
    pageBuilder: (_, _, _) => _Open(chats: _chats, conv: c, notify: widget.notify),
  ));

  /// Linked businesses, over [context].
  void _links(BuildContext context) => Navigator.of(context).push(TkRoute<void>(
    pageBuilder: (_, _, _) => LinksScreen(
      chats: _chats,
      onProfile: () => _profile(context, back: 'Back to Linked businesses'),
      // No links yet: Find a business, over this page.
      onFind: (links) => showExplore(links, chats: _chats, onOpen: (c) => _openConv(context, c)),
    ),
  ));

  Widget _chatsTab(BuildContext context) {
    return ChatsScreen(
      data: _chats,
      onLinks: () => _links(context),
      // You and the person's other businesses (taskiem-for-everyone §6).
      onSwitch: () => showSwitcher(context, widget.auth),
      onProfile: () => _profile(context),
      onMenu: () => Navigator.of(context).push(TkRoute<void>(
        pageBuilder: (_, _, _) => SignedInScreen(auth: widget.auth, prefs: widget.prefs, unlocker: widget.unlocker, notify: widget.notify),
      )),
      // A conversation slides in over the list, as a phone's chats do.
      onOpenTaskiem: () => Navigator.of(context).push(TkRoute<void>(
    pageBuilder: (_, _, _) => _OpenTaskiem(chats: _chats),
      )),
      onOpenCustomer: (c) => Navigator.of(context).push(TkRoute<void>(
    pageBuilder: (_, _, _) => _OpenCustomer(chats: _chats, id: c.id, notify: widget.notify),
      )),
      onOpen: (c) => _openConv(context, c),
      onOpenAt: (c, message) => Navigator.of(context).push(TkRoute<void>(
    pageBuilder: (_, _, _) => _Open(chats: _chats, conv: c, focus: message, notify: widget.notify),
      )),
    );
  }
}

/// An open conversation: its data lives while it is open.
class _Open extends StatefulWidget {
  const _Open({required this.chats, required this.conv, this.focus, this.notify});
  final ChatsData chats;
  final LinkConversation conv;
  final PhoneNotifications? notify;

  /// Opened at this message.
  final String? focus;

  @override
  State<_Open> createState() => _OpenState();
}

class _OpenState extends State<_Open> {
  late final ConversationData _data = ConversationData(widget.chats, widget.conv, focus: widget.focus);

  @override
  void initState() {
    super.initState();
    // On the screen: its notifications show nothing while it is.
    widget.notify?.show(widget.conv.link, this);
  }

  @override
  void dispose() {
    widget.notify?.unshow(this);
    _data.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ConversationScreen(
    data: _data,
    // A business tagged in a message: its conversation, in this one's place.
    onOpenLink: (link) {
      // "Open" on a save's toast: Taskiem's conversation, in this one's place.
      if (link == 'taskiem') {
        Navigator.of(context).pushReplacement(PageRouteBuilder<void>(
          transitionDuration: Duration.zero,
          reverseTransitionDuration: Duration.zero,
          pageBuilder: (_, _, _) => _OpenTaskiem(chats: widget.chats),
        ));
        return;
      }
      final c = widget.chats.conversations.where((c) => c.link == link).firstOrNull;
      if (c == null) return;
      Navigator.of(context).pushReplacement(PageRouteBuilder<void>(
        transitionDuration: Duration.zero,
        reverseTransitionDuration: Duration.zero,
        pageBuilder: (_, _, _) => _Open(chats: widget.chats, conv: c, notify: widget.notify),
      ));
    },
  );
}

/// Taskiem's conversation, open: its data lives while it is open.
class _OpenTaskiem extends StatefulWidget {
  const _OpenTaskiem({required this.chats, this.openTool});
  final ChatsData chats;
  final ToolToOpen? openTool;

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
    openTool: widget.openTool,
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
  const _OpenCustomer({required this.chats, required this.id, this.notify});
  final ChatsData chats;
  final String id;
  final PhoneNotifications? notify;

  @override
  State<_OpenCustomer> createState() => _OpenCustomerState();
}

class _OpenCustomerState extends State<_OpenCustomer> {
  late final CustomerData _data = CustomerData(widget.chats, widget.id);
  String? _tag;

  @override
  void initState() {
    super.initState();
    _showTag();
    _data.addListener(_showTag);
  }

  /// The customer's notifications are held back while they are open, by
  /// their own id (a pair's is the app thread's, the tag the server gives),
  /// whichever line's id opened them.
  void _showTag() {
    final tag = 'c-${_data.threadId}';
    if (tag == _tag) return;
    _tag = tag;
    widget.notify?.show(tag, this);
  }

  @override
  void dispose() {
    _data.removeListener(_showTag);
    widget.notify?.unshow(this);
    _data.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => CustomerScreen(data: _data);
}

/// The app's first screen while it opens: the Taskiem mark on pine, the
/// size and place the phone's start screen draws it (android/…/values-v31:
/// the launcher icon's foreground, its glyph about 100 dp wide).
class _Opening extends StatelessWidget {
  const _Opening();

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: TkTheme.tokens(context).brand,
    child: Center(
      child: Semantics(
        label: 'Taskiem is opening',
        // The glyph sits a little right of and above the middle of the mark's
        // square; the start screen centres the glyph's own box.
        child: Transform.translate(
          offset: const Offset(-10, 3),
          child: SvgPicture.asset('assets/brand/mark.svg', width: 172, height: 172),
        ),
      ),
    ),
  );
}
