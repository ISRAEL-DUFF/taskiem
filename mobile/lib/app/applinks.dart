// Links that open the app (docs/spec/mobile-app-m4.md N5): a link to this
// server's pages (a conversation, an invoice, an order, the approvals) opens
// in the app when the phone trusts the app with it (the server's
// /.well-known files). A link that comes while signed out waits behind
// signing in, then opens.

import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/foundation.dart';

import 'config.dart';

/// The pages the app opens from a link (the server's appPaths); anything
/// else of the server's opens in the phone's browser as before. You's
/// (taskiem-for-everyone §7): the chats and one chat, Orders and one order,
/// Discover and a business's profile; You's settings stay on the web.
final _opens = RegExp(
  r'^/(chat|approvals|invoices/[^/]+|trade-orders/[^/]+|storefront/orders/[^/]+|bills/[^/]+|purchases/[^/]+|payments|payments/[^/]+'
  r'|you|you/chats|you/chats/[^/]+|you/orders|you/orders/[^/]+/[^/]+|you/discover|you/discover/[^/]+'
  r'|you/people/[^/]+|you/people/requests/[^/]+|you/groups/[^/]+)/?$',
);

/// The code of a group's link the app opens (`/g/{code}`), or null.
String? groupLinkCodeOf(String page) => RegExp(r'^/g/([A-Za-z0-9]{4,40})$').firstMatch(page)?[1];

/// The code of a person's link the app opens (`/p/{code}`), or null.
String? personLinkOf(String page) => RegExp(r'^/p/([A-Za-z0-9]{4,40})$').firstMatch(page)?[1];

/// A part of a /you link's question: letters, digits and dashes.
final _plain = RegExp(r'^[A-Za-z0-9\-]{1,80}$');

/// The page of the app [uri] is, as a path with its question (the web's
/// address), or null when it is not one of this server's the app opens.
String? appPageOf(Uri uri, {String server = taskiemUrl}) {
  final home = Uri.tryParse(server);
  if (home == null || uri.scheme != 'https' || uri.host.toLowerCase() != home.host.toLowerCase()) return null;
  // The email's link that confirms this phone (a second proof,
  // docs/spec/taskiem-for-everyone.md §5.4): its token is after the #.
  if (uri.path == '/second-proof') {
    final token = Uri.splitQueryString(uri.fragment)['token'];
    return token != null && RegExp(r'^[A-Za-z0-9_\-]{10,200}$').hasMatch(token) ? '/second-proof?token=$token' : null;
  }
  // A business's chat link (taskiem-for-everyone §8.2): its card, then the
  // chat with it.
  final m = RegExp(r'^/m/([A-Za-z0-9]{4,40})/?$').firstMatch(uri.path);
  if (m != null) return '/m/${m[1]}';
  // A person's link, or their QR code scanned with the phone's camera
  // (people-chatting §5.2): who it is, then a chat with them.
  final p = RegExp(r'^/p/([A-Za-z0-9]{4,40})/?$').firstMatch(uri.path);
  if (p != null) return '/p/${p[1]}';
  // A group's invite link, or its QR code scanned with the phone's camera
  // (people-chatting §9): the group's name, photo and count, then Join.
  final g = RegExp(r'^/g/([A-Za-z0-9]{4,40})/?$').firstMatch(uri.path);
  if (g != null) return '/g/${g[1]}';
  if (!_opens.hasMatch(uri.path)) return null;
  final with_ = uri.queryParameters['with'];
  if (uri.path == '/chat' && with_ != null) {
    return RegExp(r'^[A-Za-z0-9\-]{1,80}$').hasMatch(with_) ? '/chat?with=$with_' : '/chat';
  }
  // A chat to start with a business (the web's /you/chats/new, from
  // Discover or an order): only its business, the tool and the order kept.
  if (uri.path == '/you/chats/new' || uri.path == '/you/chats/new/') {
    final q = uri.queryParameters;
    final business = q['business'] ?? '';
    if (!_plain.hasMatch(business)) return '/you';
    final tool = q['tool'], ref = q['ref'];
    return Uri(path: '/you/chats/new', queryParameters: {
      'business': business,
      if (tool == 'shop' || tool == 'book') 'tool': tool!,
      if (ref != null && _plain.hasMatch(ref)) 'ref': ref,
    }).toString();
  }
  return uri.path.endsWith('/') && uri.path.length > 1 ? uri.path.substring(0, uri.path.length - 1) : uri.path;
}

/// A password reset's token from its link (the server's /reset-password
/// with #token=…, from sendPasswordReset), or null when [uri] is not one.
/// The token is in the fragment, which no server is sent.
String? resetTokenOf(Uri uri, {String server = taskiemUrl}) {
  final home = Uri.tryParse(server);
  if (home == null || uri.scheme != 'https' || uri.host.toLowerCase() != home.host.toLowerCase()) return null;
  if (uri.path != '/reset-password' && uri.path != '/reset-password/') return null;
  final token = Uri.splitQueryString(uri.fragment)['token'] ?? '';
  return RegExp(r'^[A-Za-z0-9_\-.~]{8,200}$').hasMatch(token) ? token : null;
}

/// The link waiting to be opened.
class LinkInbox extends ChangeNotifier {
  LinkInbox({Stream<Uri>? links, Future<Uri?> Function()? first}) {
    unawaited(_listen(links, first));
  }

  String? _pending;

  /// The page waiting, once.
  String? take() {
    final p = _pending;
    _pending = null;
    return p;
  }

  bool get waiting => _pending != null;

  /// The page waiting, left waiting (a chat link shown while signed out).
  String? get peek => _pending;

  /// The waiting chat link's card was shown on the way in: once signed in,
  /// its chat starts without asking again.
  bool shownSignedOut = false;

  String? _reset;

  /// A password reset's token waiting (signed in or out), once.
  String? takeReset() {
    final r = _reset;
    _reset = null;
    return r;
  }

  Uri? _last;
  DateTime _lastAt = DateTime(2000);

  /// A link that came (tests give one). The first link may come twice (the
  /// initial link, and again on the stream): once is opened; the same link
  /// tapped again later opens again.
  void receive(Uri uri) {
    final now = DateTime.now();
    if (uri == _last && now.difference(_lastAt) < const Duration(seconds: 3)) return;
    _last = uri;
    _lastAt = now;
    final reset = resetTokenOf(uri);
    if (reset != null) {
      _reset = reset;
      notifyListeners();
      return;
    }
    final page = appPageOf(uri);
    if (page == null) return;
    _pending = page;
    notifyListeners();
  }

  StreamSubscription<Uri>? _sub;

  Future<void> _listen(Stream<Uri>? links, Future<Uri?> Function()? first) async {
    try {
      final app = links == null ? AppLinks() : null;
      final f = first ?? app?.getInitialLink;
      final u = await f?.call();
      if (u != null) receive(u);
      _sub = (links ?? app!.uriLinkStream).listen(receive, onError: (Object _) {});
    } on Object {
      // No links on this platform (a test, a desktop): nothing waits.
    }
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }
}
