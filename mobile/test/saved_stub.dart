// A stand-in for the saved-things routes a form's Saved button reads
// (lib/chat/saved_fill.dart): the person's own list and the business's,
// their kinds, and a bank account's number shown with a passkey. Each
// thing has every field the API marks required for SavedObject.

import 'dart:convert';

import 'package:http/http.dart' as http;

Map<String, dynamic> savedThing(
  String id,
  String owner,
  String kind,
  String name,
  String title,
  String hint,
  Map<String, dynamic> details, {
  String? kindId,
  Map<String, dynamic>? values,
  String? accountName,
}) => {
  'id': id,
  'owner': owner,
  'name': name,
  'title': title,
  'kind': kind,
  'hint': hint,
  'details': details,
  'check': {'state': kind == 'bank_account' ? (accountName == null ? 'unchecked' : 'checked') : 'none', 'account_name': accountName, 'via': null, 'at': null, 'problem': null},
  'to_change': true,
  'created_at': '2026-11-20T10:00:00Z',
  'updated_at': '2026-11-20T10:00:00Z',
  'kind_id': ?kindId,
  'values': ?values,
};

/// A made-up kind with its fields ({key, label, type, sensitive?}).
Map<String, dynamic> savedKind(String id, String name, List<Map<String, dynamic>> fields) => {
  'id': id,
  'name': name,
  'count': 1,
  'fields': fields,
  'created_at': '2026-11-20T10:00:00Z',
  'updated_at': '2026-11-20T10:00:00Z',
};

class SavedStub {
  SavedStub({this.mine = const [], this.business = const [], this.myKinds = const [], this.kinds = const [], this.fail = const {}, this.numbers = const {}});
  final List<Map<String, dynamic>> mine;
  final List<Map<String, dynamic>> business;
  final List<Map<String, dynamic>> myKinds;
  final List<Map<String, dynamic>> kinds;

  /// A path that answers with this status and an error.
  final Map<String, int> fail;

  /// A bank account's number in full by its id, for reveal.
  final Map<String, String> numbers;

  /// What was asked, as "GET /v1/me/saved".
  final calls = <String>[];

  static http.Response _json(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});

  /// The answer to [req] when it is a saved-things route, else null.
  http.Response? answer(http.Request req) {
    final p = req.url.path;
    if (!p.startsWith('/v1/me/saved') && !p.startsWith('/v1/saved')) return null;
    calls.add('${req.method} $p');
    if (fail[p] case final s?) return _json({'error': s == 403 ? 'You may not see the business’s saved things.' : 'Saved things cannot be read now.'}, s);
    if (req.method == 'GET') {
      return switch (p) {
        '/v1/me/saved' => _json({'saved': mine, 'limit': 200}),
        '/v1/saved' => _json({'saved': business, 'limit': 200, 'can_check': false}),
        '/v1/me/saved-kinds' => _json({'kinds': myKinds, 'limit': 20, 'starters': [], 'to_change': true}),
        '/v1/saved-kinds' => _json({'kinds': kinds, 'limit': 20, 'starters': [], 'to_change': true}),
        _ => null,
      };
    }
    if (p.endsWith('/step-up/options')) return _json({'publicKey': {'challenge': 'abc', 'rpId': 'taskiem.test'}});
    final m = RegExp(r'/saved/([^/]+)/reveal$').firstMatch(p);
    if (m != null) return _json({'account_number': numbers[m.group(1)] ?? ''});
    return null;
  }
}
