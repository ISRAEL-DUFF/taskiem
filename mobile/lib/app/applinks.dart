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
/// else of the server's opens in the phone's browser as before.
final _opens = RegExp(r'^/(chat|approvals|invoices/[^/]+|trade-orders/[^/]+|storefront/orders/[^/]+|bills/[^/]+|purchases/[^/]+)/?$');

/// The page of the app [uri] is, as a path with its question (the web's
/// address), or null when it is not one of this server's the app opens.
String? appPageOf(Uri uri, {String server = taskiemUrl}) {
  final home = Uri.tryParse(server);
  if (home == null || uri.scheme != 'https' || uri.host.toLowerCase() != home.host.toLowerCase()) return null;
  if (!_opens.hasMatch(uri.path)) return null;
  final with_ = uri.queryParameters['with'];
  if (uri.path == '/chat' && with_ != null) {
    return RegExp(r'^[A-Za-z0-9\-]{1,80}$').hasMatch(with_) ? '/chat?with=$with_' : '/chat';
  }
  return uri.path.endsWith('/') && uri.path.length > 1 ? uri.path.substring(0, uri.path.length - 1) : uri.path;
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
