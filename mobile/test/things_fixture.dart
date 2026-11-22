// Things we have and spending by label (what a business buys §5.3, §5.4,
// U3), for the tests and previews.

import 'dart:convert';

import 'package:http/http.dart' as http;

Map<String, Object?> thing(
  String id,
  String name, {
  String status = 'have',
  int? cost,
  String? labelId,
  String? label,
  String? supplier,
  String? order,
  String? reference,
  String received = '2026-11-03T10:00:00Z',
  String? serial,
  String? warranty,
  String? note,
  String? sale,
}) => {
  'id': id,
  'name': name,
  'label_id': ?labelId,
  'label': ?label,
  'supplier_id': supplier == null ? null : 'sup-${supplier.hashCode}',
  'supplier': ?supplier,
  'purchase_order_id': ?order,
  'order_reference': ?reference,
  'unit_no': order == null ? null : 1,
  'cost_kobo': ?cost,
  'received_at': received,
  'serial': ?serial,
  'warranty_until': ?warranty,
  'note': ?note,
  'status': status,
  'status_at': status == 'have' ? received : '2026-11-20T10:00:00Z',
  'sale_entry_id': ?sale,
  'created_by': 'Ada',
  'created_at': received,
};

final things = [
  thing('th-1', 'Sewing machine', cost: 45000000, labelId: 'lb-mach', label: 'Machines', supplier: 'Singer Ltd', order: 'po-1', reference: 'PO-0012', serial: 'SN-88213', warranty: '2027-11-03'),
  thing('th-2', 'Generator', cost: 32000000, labelId: 'lb-mach', label: 'Machines', supplier: 'Elepaq Stores', received: '2026-10-21T10:00:00Z'),
  thing('th-3', 'Shop shelves', cost: 8500000, received: '2026-09-02T10:00:00Z', note: 'Bought before Taskiem'),
  thing('th-4', 'Old freezer', status: 'sold', cost: 12000000, labelId: 'lb-mach', label: 'Machines', received: '2025-06-01T10:00:00Z', sale: 'me-9'),
];

Map<String, Object?> thingsAnswer(bool all) {
  final list = [for (final t in things) if (all || t['status'] == 'have') t];
  final had = things.where((t) => t['status'] == 'have');
  return {
    'things': list,
    'count': had.length,
    'total_kobo': had.fold<int>(0, (n, t) => n + (t['cost_kobo'] as int? ?? 0)),
    'groups': [
      {'label_id': 'lb-mach', 'label': 'Machines', 'count': 2, 'total_kobo': 77000000},
      {'label_id': null, 'label': '', 'count': 1, 'total_kobo': 8500000},
    ],
  };
}

final spending = {
  'from': '2026-11-01T00:00:00+01:00',
  'to': '2026-12-01T00:00:00+01:00',
  'total_kobo': 102500000,
  'bought_to_sell_kobo': 30000000,
  'bought_to_keep_kobo': 45000000,
  'running_costs_kobo': 27500000,
  'labels': [
    {'label_id': 'lb-mach', 'name': 'Machines', 'kobo': 45000000, 'bought_to_sell_kobo': 0, 'bought_to_keep_kobo': 45000000, 'running_costs_kobo': 0, 'entries': 1},
    {'label_id': 'lb-fab', 'name': 'Fabric', 'kobo': 30000000, 'bought_to_sell_kobo': 30000000, 'bought_to_keep_kobo': 0, 'running_costs_kobo': 0, 'entries': 3},
    {'label_id': 'lb-diesel', 'name': 'Diesel', 'kobo': 15000000, 'bought_to_sell_kobo': 0, 'bought_to_keep_kobo': 0, 'running_costs_kobo': 15000000, 'entries': 6},
    {'label_id': null, 'name': 'Not labelled', 'kobo': 12500000, 'bought_to_sell_kobo': 0, 'bought_to_keep_kobo': 0, 'running_costs_kobo': 12500000, 'entries': 2},
  ],
  'profit': {'in_kobo': 180000000, 'fees_kobo': 1500000, 'costs_kobo': 57500000, 'profit_kobo': 121000000},
};

http.Response _ok(Object b, [int status = 200]) => http.Response(jsonEncode(b), status, headers: {'content-type': 'application/json'});

/// The answers Things we have and spending read, or null for any other.
http.Response? thingsAnswers(http.Request req) {
  final p = req.url.path;
  if (req.method == 'GET') {
    if (p == '/v1/things') return _ok(thingsAnswer(req.url.queryParameters['all'] == 'true'));
    if (p.startsWith('/v1/things/')) {
      final t = things.where((t) => t['id'] == p.split('/').last).firstOrNull;
      return t == null ? _ok({'error': 'not found'}, 404) : _ok({'thing': t});
    }
    if (p == '/v1/money/spending') return _ok(spending);
    if (p == '/v1/stock/suppliers') {
      return _ok({
        'suppliers': [
          {'id': 'sup-singer', 'name': 'Singer Ltd'},
          {'id': 'sup-elepaq', 'name': 'Elepaq Stores'},
        ],
      });
    }
  }
  return null;
}
