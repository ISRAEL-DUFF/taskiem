// How the app talks to Taskiem: JSON over HTTPS, signed in with the session
// token as a bearer (docs/spec/mobile-app.md §5.2). The generated client
// (api.g.dart) calls send; everything about the wire is here.

import 'dart:async';
import 'dart:convert';
import 'dart:io' show IOException;

import 'package:flutter/foundation.dart' show ValueNotifier;
import 'package:http/http.dart' as http;
import 'package:http_parser/http_parser.dart';

/// An answer that is not a success: its status and the server's body, which
/// always has a human-readable `error` and sometimes a machine-readable
/// reason (`passkey_required`, `sso_required`, `step_up`...).
class ApiError implements Exception {
  ApiError(this.status, this.body);

  final int status;
  final Map<String, dynamic> body;

  /// The server's words, for the person.
  String get message => (body['error'] as String?) ?? 'Something went wrong. Try again.';

  /// Whether the answer carries [flag] (`{"passkey_required": true}`).
  bool has(String flag) => body[flag] == true;

  @override
  String toString() => 'ApiError($status, $message)';
}

/// The network could not be reached: said plainly, never as a status.
class Offline implements Exception {
  const Offline({this.slow = false});

  /// No answer in time (a weak connection), rather than none at all.
  final bool slow;
  @override
  String toString() => 'Offline';
}

/// Where the answers the app read are kept on the phone (lib/app/store.dart),
/// with their ETags: asked again with If-None-Match, an unchanged page is a
/// 304; with no connection, the kept answer is the page.
typedef KeptPage = ({String body, String? etag});

abstract class PageKeep {
  KeptPage? readPage(String key);
  void writePage(String key, String body, String? etag);
}

class Transport {
  Transport({required this.base, this.token, http.Client? client, this.timeout = const Duration(seconds: 30)}) : _client = client ?? http.Client();

  /// The pages kept on the phone, while signed in (null: none kept). Another
  /// store (another business or person) starts afresh: what was shown and
  /// brought up to date was the other one's.
  PageKeep? get pages => _pages;
  set pages(PageKeep? p) {
    if (identical(p, _pages)) return;
    _pages = p;
    _shown.clear();
    _justFresh.clear();
    _asked.clear();
  }

  PageKeep? _pages;

  /// Whether the last request found no connection and was answered from
  /// what the phone kept: the app says so, quietly, as WhatsApp does.
  final offline = ValueNotifier<bool>(false);

  /// How long a page that is kept waits for the network before the kept
  /// one is shown: a weak connection must not hold up what the phone has.
  static const keptWait = Duration(seconds: 6);

  /// Told the key of a kept page whose fresh copy differs from what was
  /// shown (docs/spec/mobile-app-explore-and-offline.md §3.1: draw what was
  /// kept at once, then bring it up to date in place). The screens that
  /// poll read again when told (lib/kit/polling.dart).
  static final pageChanged = StreamController<String>.broadcast();

  /// How many kept pages are being brought up to date now ("Updating…").
  final updating = ValueNotifier<int>(0);

  /// The pages read this run (from the phone or the server): asked again,
  /// the server is asked first, as before.
  final _shown = <String>{};

  /// Pages just brought up to date behind what was shown: the one read
  /// that follows (the screen reading again when told) is answered from
  /// the phone, with no request; only soon after, and only while nothing
  /// was written since (a write may have changed the page).
  final _justFresh = <String, DateTime>{};
  static const _freshFor = Duration(seconds: 5);

  /// The latest ask of each page: an answer to an earlier one that arrives
  /// later (a slow refresh) is not kept over it.
  final _asked = <String, int>{};
  int _asks = 0;

  /// The pages worth keeping (docs/spec/mobile-app-explore-and-offline.md
  /// §3.2): Today, Money, Orders and the tools' lists, and Explore's start
  /// page; reads only, by their exact path. Nothing else is kept: not who
  /// is signed in or the app's settings (they decide what may be done),
  /// not a form's data (saved whole, it must be the server's), not a
  /// search, a section's next page or anything shown unmasked.
  static final _kept = <RegExp>[
    // Today.
    RegExp(r'^/v1/(dashboard|approvals|runs|form-requests|workflows|onboarding|staff/tasks|staff/clock)$'),
    // Money.
    RegExp(r'^/v1/money/(summary|leaks|statements|statement-waiting)$'),
    RegExp(r'^/v1/(invoices|bills)$'),
    // Orders.
    RegExp(r'^/v1/(storefront/orders|trade-orders|stock/purchases|storefront/bookings|storefront/fronts|deliveries)$'),
    // The tools' lists.
    RegExp(r'^/v1/(stock|price-lists|riders|storefront/services)$'),
    RegExp(r'^/v1/links/[^/]+/statement$'),
    // Explore's start page (no section, no search).
    RegExp(r'^/v1/explore$'),
  ];

  /// Of those, the pages drawn at once from what was kept: the screens
  /// that read again when told it changed (lib/kit/polling.dart, Explore).
  /// The rest (lists a form also reads, as the stock a sale is taken from)
  /// ask the server first, the kept copy only offline.
  static final _atOnce = <RegExp>[
    RegExp(r'^/v1/(dashboard|approvals|runs|form-requests|workflows|onboarding|staff/tasks|staff/clock)$'),
    RegExp(r'^/v1/money/(summary|leaks|statements|statement-waiting)$'),
    RegExp(r'^/v1/(invoices|bills|storefront/orders|trade-orders|stock/purchases|storefront/bookings|explore)$'),
    RegExp(r'^/v1/links/[^/]+/statement$'),
  ];

  static bool atOnce(String path) => _atOnce.any((r) => r.hasMatch(path));

  static bool keeps(String path, Map<String, String>? query) {
    if (query != null && (query.containsKey('q') || query.containsKey('reveal') || query.containsKey('section') || query.containsKey('offset'))) return false;
    if (path.contains('reveal')) return false;
    return _kept.any((r) => r.hasMatch(path));
  }

  /// How long an answer may take before the network counts as gone: on a
  /// weak connection a request must not hang with its button spinning.
  final Duration timeout;

  /// Told when the server says the session has ended (401 on a request made
  /// with one): signed out from another phone, a password changed.
  void Function()? onSessionEnded;

  /// Where Taskiem is, as `https://host` (no trailing slash).
  final String base;

  /// The session token, once signed in.
  String? token;

  final http.Client _client;

  /// Sends one request and answers its JSON (null for 204). A read the
  /// phone kept is asked again with its ETag; unchanged, or with no
  /// connection, the kept answer is the answer.
  Future<Object?> send(String method, String path, {Object? body, Map<String, String>? query, Map<String, String>? headers}) async {
    // A write may change any page: none is "just brought up to date" now.
    if (method != 'GET') _justFresh.clear();
    final keep = method == 'GET' && body == null ? pages : null;
    if (keep != null && keeps(path, query)) return _sendKept(keep, path, query, headers);
    return _send(method, path, body: body, query: query, headers: headers);
  }

  Future<Object?> _sendKept(PageKeep keep, String path, Map<String, String>? query, Map<String, String>? headers) async {
    final key = _uri(path, query).toString().substring(base.length);
    var kept = keep.readPage(key);
    Object? json;
    if (kept != null) {
      try {
        json = kept.body.isEmpty ? null : jsonDecode(kept.body);
      } on FormatException {
        // Spoilt: as if nothing was kept.
        kept = null;
      }
    }
    if (kept != null) {
      // Just brought up to date: the phone's copy is the server's.
      final at = _justFresh.remove(key);
      if (at != null && DateTime.now().difference(at) < _freshFor) return json;
      // First asked this run: what was kept, at once; the server after.
      if (atOnce(path) && _shown.add(key)) {
        unawaited(_refresh(keep, key, path, query, headers, kept));
        return json;
      }
    }
    return _ask(keep, key, path, query, headers, kept);
  }

  /// A kept page brought up to date behind what is shown: told when it
  /// changed; left as it is when the server cannot be reached or refuses
  /// (the next read says why).
  Future<void> _refresh(PageKeep keep, String key, String path, Map<String, String>? query, Map<String, String>? headers, KeptPage kept) async {
    updating.value++;
    try {
      final wrote = await _ask(keep, key, path, query, headers, kept, wait: timeout, refreshing: true);
      // Offline, or a newer ask answered first: nothing to tell.
      if (wrote != true || !identical(keep, _pages)) return;
      final now = keep.readPage(key);
      if (now != null && now.body != kept.body) {
        _justFresh[key] = DateTime.now();
        pageChanged.add(key);
      }
    } on Object {
      // Offline or refused: what was kept stays shown.
    } finally {
      updating.value--;
    }
  }

  /// Asks the server for a page, keeping its answer. When [refreshing],
  /// answers whether the page was brought up to date (true), instead of
  /// the page.
  Future<Object?> _ask(
    PageKeep keep,
    String key,
    String path,
    Map<String, String>? query,
    Map<String, String>? headers,
    KeptPage? kept, {
    Duration? wait,
    bool refreshing = false,
  }) async {
    final ask = _asked[key] = ++_asks;
    final req = http.Request('GET', _uri(path, query))..headers['Accept'] = 'application/json';
    if (headers != null) req.headers.addAll(headers);
    if (kept?.etag != null) req.headers['If-None-Match'] = kept!.etag!;
    final t = _sign(req);
    final http.Response res;
    try {
      res = await _reach(() async => http.Response.fromStream(await _client.send(req)), wait ?? (kept == null ? timeout : keptWait));
    } on Offline catch (e) {
      // Slow is not gone: the strip says "No connection" only when there is none.
      if (!e.slow) offline.value = true;
      if (kept == null || refreshing) rethrow;
      return kept.body.isEmpty ? null : jsonDecode(kept.body);
    }
    offline.value = false;
    _shown.add(key);
    // Answered after a newer ask of the same page: that one's answer stands.
    final latest = _asked[key] == ask;
    if (res.statusCode == 304 && kept != null) return refreshing ? false : (kept.body.isEmpty ? null : jsonDecode(kept.body));
    final text = utf8.decode(res.bodyBytes);
    final out = _answer(res.statusCode, text, t);
    if (res.statusCode == 200 && latest) {
      try {
        keep.writePage(key, text, res.headers['etag']);
      } on Object {
        // Not kept this time.
      }
    }
    return refreshing ? latest : out;
  }

  Future<Object?> _send(String method, String path, {Object? body, Map<String, String>? query, Map<String, String>? headers}) async {
    final req = http.Request(method, _uri(path, query))..headers['Accept'] = 'application/json';
    // An Idempotency-Key, where one press must be one action however often it is sent.
    if (headers != null) req.headers.addAll(headers);
    if (body != null) {
      req.headers['Content-Type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    final t = _sign(req);
    // The whole exchange, waiting for the answer to start included.
    final http.Response res;
    try {
      res = await _reach(() async => http.Response.fromStream(await _client.send(req)), timeout);
    } on Offline catch (e) {
      if (!e.slow) offline.value = true;
      rethrow;
    }
    // Reached: whatever was said about being offline is over.
    if (offline.value) offline.value = false;
    return _answer(res.statusCode, utf8.decode(res.bodyBytes), t);
  }

  /// Sends a file as the body itself, its type the Content-Type (api.ts
  /// uploadFile: a logo, a profile's photo), and answers the JSON.
  Future<Object?> sendFile(String path, {required List<int> bytes, required String type}) async {
    final req = http.Request('POST', _uri(path))
      ..headers['Accept'] = 'application/json'
      ..headers['Content-Type'] = type
      ..bodyBytes = bytes;
    final t = _sign(req);
    final res = await _reach(() async => http.Response.fromStream(await _client.send(req)), timeout * 2);
    return _answer(res.statusCode, utf8.decode(res.bodyBytes), t);
  }

  /// Sends a file as a multipart form (`file`, and the [fields] beside it)
  /// and answers the JSON, as [send] does.
  Future<Object?> upload(String path, {required List<int> bytes, required String filename, required String type, Map<String, String> fields = const {}, String field = 'file'}) async {
    final req = http.MultipartRequest('POST', _uri(path))
      ..headers['Accept'] = 'application/json'
      ..fields.addAll(fields)
      ..files.add(http.MultipartFile.fromBytes(field, bytes, filename: filename, contentType: MediaType.parse(type)));
    final t = _sign(req);
    // A file takes longer than a message: twice the time.
    final res = await _reach(() async => http.Response.fromStream(await _client.send(req)), timeout * 2);
    return _answer(res.statusCode, utf8.decode(res.bodyBytes), t);
  }

  /// A picture or a file's bytes (a logo, a photo), signed in as the rest.
  Future<List<int>> bytes(String path) async {
    final req = http.Request('GET', _uri(path));
    final t = _sign(req);
    final res = await _reach(() async => http.Response.fromStream(await _client.send(req)), timeout);
    if (res.statusCode == 200) return res.bodyBytes;
    // Not the file: the server's words (in JSON), as any request's.
    _answer(res.statusCode, utf8.decode(res.bodyBytes, allowMalformed: true), t);
    throw ApiError(res.statusCode, {'error': 'The server answered ${res.statusCode}.'});
  }

  /// A long answer read as it comes (the chat's server-sent events): the
  /// response once its head is here, its body a stream. The head must come
  /// within the timeout; the body has none (the caller watches for a
  /// stall). A 401 ends the session as any request's does.
  Future<http.StreamedResponse> stream(String path, {Map<String, String>? headers}) async {
    final req = http.Request('GET', _uri(path))
      ..headers['Accept'] = 'text/event-stream'
      ..headers.addAll(headers ?? const {});
    final t = _sign(req);
    final res = await _reach(() => _client.send(req), timeout);
    if (res.statusCode == 200) return res;
    final text = await utf8.decodeStream(res.stream).catchError((Object _) => '');
    _answer(res.statusCode, text, t);
    throw ApiError(res.statusCode, {'error': 'The server answered ${res.statusCode}.'});
  }

  /// Where [path] is; only ever over https, but for development.
  Uri _uri(String path, [Map<String, String>? query]) {
    final uri = Uri.parse('$base$path').replace(queryParameters: (query == null || query.isEmpty) ? null : query);
    if (uri.scheme != 'https' && !_local(uri.host)) {
      throw ArgumentError('Taskiem is only reached over https');
    }
    return uri;
  }

  /// Signs [req] with the session, if there is one; the token it was
  /// signed with.
  String? _sign(http.BaseRequest req) {
    final t = token;
    if (t != null) req.headers['Authorization'] = 'Bearer $t';
    return t;
  }

  /// The exchange within [limit]; the network gone, said as [Offline].
  Future<T> _reach<T>(Future<T> Function() exchange, Duration limit) async {
    try {
      return await exchange().timeout(limit);
    } on http.ClientException {
      throw const Offline();
    } on IOException {
      throw const Offline();
    } on TimeoutException {
      throw const Offline(slow: true);
    }
  }

  /// A success's JSON (null for none), or the failure as an [ApiError]: a
  /// 401 to the session it was sent with ends that session.
  Object? _answer(int status, String text, String? sentWith) {
    Object? json;
    if (text.isNotEmpty) {
      try {
        json = jsonDecode(text);
      } on FormatException {
        json = null;
      }
    }
    if (status >= 200 && status < 300) return json;
    if (status == 401 && sentWith != null && token == sentWith) onSessionEnded?.call();
    throw ApiError(status, json is Map<String, dynamic> ? json : {'error': 'The server answered $status.'});
  }

  // Plain http only to a server on this machine or the emulator's host,
  // for development.
  static bool _local(String host) => host == 'localhost' || host == '127.0.0.1' || host == '10.0.2.2';
}
