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
    final uri = Uri.parse('$base$path').replace(queryParameters: (query == null || query.isEmpty) ? null : query);
    if (uri.scheme != 'https' && !_local(uri.host)) {
      throw ArgumentError('Taskiem is only reached over https');
    }
    final req = http.Request(method, uri)..headers['Accept'] = 'application/json';
    if (body != null) {
      req.headers['Content-Type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    final t = token;
    if (t != null) req.headers['Authorization'] = 'Bearer $t';
    http.Response res;
    try {
      // The whole exchange, waiting for the answer to start included.
      res = await (() async => http.Response.fromStream(await _client.send(req)))().timeout(timeout);
    } on http.ClientException {
      throw const Offline();
    } on IOException {
      throw const Offline();
    } on TimeoutException {
      throw const Offline();
    }
    final text = utf8.decode(res.bodyBytes);
    Object? json;
    if (text.isNotEmpty) {
      try {
        json = jsonDecode(text);
      } on FormatException {
        json = null;
      }
    }
    if (res.statusCode >= 200 && res.statusCode < 300) return json;
    if (res.statusCode == 401 && t != null && token == t) onSessionEnded?.call();
    throw ApiError(res.statusCode, json is Map<String, dynamic> ? json : {'error': 'The server answered ${res.statusCode}.'});
  }

  /// Sends a file as a multipart form (`file`, and the [fields] beside it)
  /// and answers the JSON, as [send] does.
  Future<Object?> upload(String path, {required List<int> bytes, required String filename, required String type, Map<String, String> fields = const {}}) async {
    final uri = Uri.parse('$base$path');
    if (uri.scheme != 'https' && !_local(uri.host)) {
      throw ArgumentError('Taskiem is only reached over https');
    }
    final req = http.MultipartRequest('POST', uri)
      ..headers['Accept'] = 'application/json'
      ..fields.addAll(fields)
      ..files.add(http.MultipartFile.fromBytes('file', bytes, filename: filename, contentType: MediaType.parse(type)));
    final t = token;
    if (t != null) req.headers['Authorization'] = 'Bearer $t';
    http.Response res;
    try {
      // A file takes longer than a message: twice the time.
      res = await (() async => http.Response.fromStream(await _client.send(req)))().timeout(timeout * 2);
    } on http.ClientException {
      throw const Offline();
    } on IOException {
      throw const Offline();
    } on TimeoutException {
      throw const Offline();
    }
    final text = utf8.decode(res.bodyBytes);
    Object? json;
    try {
      json = text.isEmpty ? null : jsonDecode(text);
    } on FormatException {
      json = null;
    }
    if (res.statusCode >= 200 && res.statusCode < 300) return json;
    if (res.statusCode == 401 && t != null && token == t) onSessionEnded?.call();
    throw ApiError(res.statusCode, json is Map<String, dynamic> ? json : {'error': 'The server answered ${res.statusCode}.'});
  }

  /// A picture or a file's bytes (a logo, a photo), signed in as the rest.
  Future<List<int>> bytes(String path) async {
    final uri = Uri.parse('$base$path');
    if (uri.scheme != 'https' && !_local(uri.host)) {
      throw ArgumentError('Taskiem is only reached over https');
    }
    final req = http.Request('GET', uri);
    final t = token;
    if (t != null) req.headers['Authorization'] = 'Bearer $t';
    http.Response res;
    try {
      res = await (() async => http.Response.fromStream(await _client.send(req)))().timeout(timeout);
    } on http.ClientException {
      throw const Offline();
    } on IOException {
      throw const Offline();
    } on TimeoutException {
      throw const Offline();
    }
    if (res.statusCode != 200) throw ApiError(res.statusCode, {'error': 'not found'});
    return res.bodyBytes;
  }

  /// A long answer read as it comes (the chat's server-sent events): the
  /// response once its head is here, its body a stream. The head must come
  /// within the timeout; the body has none (the caller watches for a
  /// stall). A 401 ends the session as any request's does.
  Future<http.StreamedResponse> stream(String path, {Map<String, String>? headers}) async {
    final uri = Uri.parse('$base$path');
    if (uri.scheme != 'https' && !_local(uri.host)) {
      throw ArgumentError('Taskiem is only reached over https');
    }
    final req = http.Request('GET', uri)
      ..headers['Accept'] = 'text/event-stream'
      ..headers.addAll(headers ?? const {});
    final t = token;
    if (t != null) req.headers['Authorization'] = 'Bearer $t';
    http.StreamedResponse res;
    try {
      res = await _client.send(req).timeout(timeout);
    } on http.ClientException {
      throw const Offline();
    } on IOException {
      throw const Offline();
    } on TimeoutException {
      throw const Offline();
    }
    if (res.statusCode == 200) return res;
    final text = await utf8.decodeStream(res.stream).catchError((Object _) => '');
    Object? json;
    try {
      json = text.isEmpty ? null : jsonDecode(text);
    } on FormatException {
      json = null;
    }
    if (res.statusCode == 401 && t != null && token == t) onSessionEnded?.call();
    throw ApiError(res.statusCode, json is Map<String, dynamic> ? json : {'error': 'The server answered ${res.statusCode}.'});
  }

  // Plain http only to a server on this machine or the emulator's host,
  // for development.
  static bool _local(String host) => host == 'localhost' || host == '127.0.0.1' || host == '10.0.2.2';
}
