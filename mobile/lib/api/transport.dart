// How the app talks to Taskiem: JSON over HTTPS, signed in with the session
// token as a bearer (docs/spec/mobile-app.md §5.2). The generated client
// (api.g.dart) calls send; everything about the wire is here.

import 'dart:convert';

import 'package:http/http.dart' as http;

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
  Transport({required this.base, this.token, http.Client? client}) : _client = client ?? http.Client();

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
      res = await http.Response.fromStream(await _client.send(req));
    } on http.ClientException {
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
    throw ApiError(res.statusCode, json is Map<String, dynamic> ? json : {'error': 'The server answered ${res.statusCode}.'});
  }

  // Plain http only to a server on this machine or the emulator's host,
  // for development.
  static bool _local(String host) => host == 'localhost' || host == '127.0.0.1' || host == '10.0.2.2';
}
