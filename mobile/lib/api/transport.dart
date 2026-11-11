// How the app talks to Taskiem: JSON over HTTPS, signed in with the session
// token as a bearer (docs/spec/mobile-app.md §5.2). The generated client
// (api.g.dart) calls send; everything about the wire is here.

import 'dart:async';
import 'dart:convert';
import 'dart:io' show IOException;

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
  const Offline();
  @override
  String toString() => 'Offline';
}

class Transport {
  Transport({required this.base, this.token, http.Client? client, this.timeout = const Duration(seconds: 30)}) : _client = client ?? http.Client();

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

  /// Sends one request and answers its JSON (null for 204).
  Future<Object?> send(String method, String path, {Object? body, Map<String, String>? query}) async {
    final req = http.Request(method, _uri(path, query))..headers['Accept'] = 'application/json';
    if (body != null) {
      req.headers['Content-Type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    final t = _sign(req);
    // The whole exchange, waiting for the answer to start included.
    final res = await _reach(() async => http.Response.fromStream(await _client.send(req)), timeout);
    return _answer(res.statusCode, utf8.decode(res.bodyBytes), t);
  }

  /// Sends a file as a multipart form (`file`, and the [fields] beside it)
  /// and answers the JSON, as [send] does.
  Future<Object?> upload(String path, {required List<int> bytes, required String filename, required String type, Map<String, String> fields = const {}}) async {
    final req = http.MultipartRequest('POST', _uri(path))
      ..headers['Accept'] = 'application/json'
      ..fields.addAll(fields)
      ..files.add(http.MultipartFile.fromBytes('file', bytes, filename: filename, contentType: MediaType.parse(type)));
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
      throw const Offline();
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
