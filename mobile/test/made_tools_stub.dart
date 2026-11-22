// A stand-in for the made tools API (made tools M1; api/tools.go,
// api/tools_answer.go): the business's tools, their starts and confirms,
// and their runs' answers, as the server answers them. Shared by
// made_tools_test.dart and the parity screens.

import 'dart:convert';

import 'package:http/http.dart' as http;

/// Four tools, one of each care and answer: Leave left (reads, a text),
/// Log an expense (asks Yes, a card), Pay a supplier (Yes and a passkey),
/// Open tickets (reads, a list).
final madeTools = <Map<String, dynamic>>[
  {
    'workflow_id': '0193a000-0000-7000-8000-000000000001',
    'environment': 'prod',
    'version': 3,
    'command': 'leave',
    'name': 'Leave left',
    'about': 'Days of leave a person has left',
    'says': ['leave for'],
    'care': {'level': 'answers'},
    'answer': 'text',
    'always_ask': false,
    'inputs': {
      'type': 'object',
      'required': ['name'],
      'properties': {
        'name': {'type': 'string', 'title': 'Name'},
      },
    },
  },
  {
    'workflow_id': '0193a000-0000-7000-8000-000000000002',
    'environment': 'prod',
    'version': 1,
    'command': 'expense',
    'name': 'Log an expense',
    'about': 'Record money spent, with what it was for',
    'says': ['spent'],
    'icon': 'cash',
    'care': {
      'level': 'yes',
      'because': ['It adds a row to Expenses'],
    },
    'answer': 'card',
    'always_ask': false,
    'inputs': {
      'type': 'object',
      'required': ['amount', 'what'],
      'properties': {
        'amount': {
          'type': 'number',
          'title': 'Amount',
          'x-form': {'unit': 'naira'},
        },
        'what': {'type': 'string', 'title': 'What for'},
        'paid': {
          'type': 'string',
          'title': 'Paid by',
          'enum': ['Cash', 'Transfer'],
        },
      },
    },
  },
  {
    'workflow_id': '0193a000-0000-7000-8000-000000000003',
    'environment': 'prod',
    'version': 2,
    'command': 'pay-supplier',
    'name': 'Pay a supplier',
    'about': 'Send money to a supplier’s bank account',
    'says': <String>[],
    'care': {
      'level': 'step_up',
      'because': ['A step pays a bank account'],
    },
    'answer': 'card',
    'always_ask': false,
    'inputs': {
      'type': 'object',
      'required': ['supplier', 'amount'],
      'properties': {
        'supplier': {'type': 'string', 'title': 'Supplier'},
        'amount': {
          'type': 'number',
          'title': 'Amount',
          'x-form': {'unit': 'naira'},
        },
      },
    },
  },
  {
    'workflow_id': '0193a000-0000-7000-8000-000000000004',
    'environment': 'prod',
    'version': 1,
    'command': 'tickets',
    'name': 'Open tickets',
    'about': 'What customers are waiting on',
    'says': <String>[],
    'care': {'level': 'answers'},
    'answer': 'list',
    'always_ask': false,
    'inputs': {'type': 'object', 'properties': <String, dynamic>{}},
  },
];

/// An expense's card, as the server draws it.
Map<String, dynamic> expenseCard() => {
  'status': 'completed',
  'type': 'card',
  'tool': 'expense',
  'title': 'Diesel for the generator',
  'ref': 'EXP-0142',
  'state': {'text': 'Recorded', 'tone': 'good'},
  'lines': [
    {'label': 'Paid by', 'value': 'Cash'},
    {'label': 'Logged by', 'value': 'Ada Obi'},
  ],
  'amount_kobo': 450000,
  'buttons': [
    {
      'label': 'Log another',
      'tool': 'expense',
      'inputs': {'paid': 'Cash'},
    },
  ],
};

/// The tickets' list, with more rows than the chat shows.
Map<String, dynamic> ticketsList() => {
  'status': 'completed',
  'type': 'list',
  'tool': 'tickets',
  'title': 'Open tickets',
  'empty': 'No open tickets',
  'rows': [
    for (final (i, x) in [
      ('Rice short by two bags', 'Mama Tunde', '2 days', 'warn'),
      ('Wrong size delivered', 'Chidi Okafor', '1 day', 'plain'),
      ('Refund for a broken pot', 'Bayo Rice Mills', '5 hours', 'plain'),
      ('Delivery not arrived', 'Ngozi Eze', '3 hours', 'warn'),
      ('Invoice sent twice', 'Kemi Adeyemi', '2 hours', 'plain'),
      ('Change of address', 'Emeka Nwosu', '1 hour', 'plain'),
      ('Price on the receipt', 'Funmi Ade', '20 min', 'plain'),
    ].indexed)
      {
        'title': x.$1,
        'sub': x.$2,
        'state': {'text': x.$3, 'tone': x.$4},
        'actions': [
          {
            'label': 'Assign to me',
            'tool': 'expense',
            'inputs': {'what': 'Ticket ${i + 1}'},
          },
        ],
      },
  ],
};

/// What the stand-in was sent, and what it answers each run with (in
/// turn; the last again).
class MadeServer {
  MadeServer({List<Map<String, dynamic>>? tools}) : tools = tools ?? madeTools;
  final List<Map<String, dynamic>> tools;
  final sent = <({String method, String path, Map<String, dynamic> body})>[];

  /// The answers a run gives, in turn.
  final answers = <String, List<Map<String, dynamic>>>{};

  /// The next start or confirm refused with this status and body.
  ({int status, Map<String, dynamic> body})? refuse;
  int _runs = 0;

  static http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

  List<Map<String, String>> _values(Map<String, dynamic> tool, Map<String, dynamic> input) {
    final props = ((tool['inputs'] as Map)['properties'] as Map).cast<String, dynamic>();
    final names = input.keys.toList()..sort();
    return [
      for (final k in names) {'name': k, 'label': ((props[k] as Map?)?['title'] as String?) ?? k, 'value': '${input[k]}'},
    ];
  }

  String _run(String command) {
    _runs++;
    final id = '0193b000-0000-7000-8000-${_runs.toString().padLeft(12, '0')}';
    answers[id] ??= switch (command) {
      'leave' => [
        {'status': 'running', 'type': 'outcome', 'tool': 'leave'},
        {'status': 'completed', 'type': 'text', 'tool': 'leave', 'text': 'Musa has 12 days of leave left'},
      ],
      'expense' => [expenseCard()],
      'tickets' => [ticketsList()],
      _ => [
        {'status': 'failed', 'type': 'outcome', 'tool': command, 'error': 'The bank refused the transfer: the account is closed'},
      ],
    };
    return id;
  }

  Future<http.Response?> call(http.Request req) async {
    final p = req.url.path;
    if (!p.startsWith('/v1/tools') && !p.startsWith('/v1/runs/') && p != '/v1/me/step-up/options') return null;
    final body = req.body.isEmpty ? <String, dynamic>{} : (jsonDecode(req.body) as Map).cast<String, dynamic>();
    if (req.method != 'GET') sent.add((method: req.method, path: p, body: body));
    if (p == '/v1/tools') return _json({'tools': tools});
    if (p == '/v1/me/step-up/options') {
      return _json({
        'publicKey': {'challenge': 'c', 'rpId': 'taskiem.test'},
      });
    }
    final r = RegExp(r'^/v1/runs/([^/]+)/answer$').firstMatch(p);
    if (r != null) {
      final list = answers[r.group(1)];
      if (list == null) return _json({'error': 'not found'}, 404);
      return _json(list.length > 1 ? list.removeAt(0) : list.first);
    }
    final m = RegExp(r'^/v1/tools/([^/]+)/(start|confirm|settings)$').firstMatch(p);
    if (m == null) return _json({'error': 'not found'}, 404);
    final tool = tools.where((t) => t['command'] == m.group(1)).firstOrNull;
    if (tool == null) return _json({'error': 'no tool /${m.group(1)}'}, 404);
    final no = refuse;
    if (no != null && m.group(2) != 'settings') {
      refuse = null;
      return _json(no.body, no.status);
    }
    final input = ((body['input'] as Map?) ?? const {}).cast<String, dynamic>();
    switch (m.group(2)) {
      case 'settings':
        return http.Response('', 204);
      case 'start':
        final care = tool['always_ask'] == true && (tool['care'] as Map)['level'] == 'answers' ? 'yes' : (tool['care'] as Map)['level'] as String;
        if (care == 'answers') {
          return _json({'run_id': _run(tool['command'] as String), 'care': care, 'values': _values(tool, input), 'created': true}, 201);
        }
        return _json({
          'care': care,
          'confirm': '0193c000-0000-7000-8000-000000000001',
          'expires_at': '2026-11-22T10:10:00Z',
          'values': _values(tool, input),
        });
      case 'confirm':
        if ((tool['care'] as Map)['level'] == 'step_up' && body['passkey'] == null) {
          return _json({'error': 'a tool that moves money is confirmed with a passkey', 'step_up': 'required', 'methods': ['passkey']}, 403);
        }
        return _json({'run_id': _run(tool['command'] as String), 'created': true}, 201);
    }
    return null;
  }
}
