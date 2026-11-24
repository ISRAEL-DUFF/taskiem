// What the question on a purchase line reads (what a business buys §6,
// U2a): the stock items (stock_items_fixture.dart) and the business's
// labels, with suggestions for an item, for the tests and previews.

import 'dart:convert';

import 'package:http/http.dart' as http;

import 'stock_items_fixture.dart';

Map<String, Object?> label(String id, String name, int uses) => {'id': id, 'name': name, 'created_by': 'Ada', 'created_at': '2026-11-01T09:00:00Z', 'uses': uses};

/// GET /v1/money/labels: the most used first.
final spendLabels = [label('lb-pack', 'Packaging', 12), label('lb-mach', 'Machines', 3), label('lb-staff', 'Staff welfare', 2)];

http.Response _ok(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json'});

/// The answers the question reads, or null for any other request.
http.Response? treatAnswers(http.Request req) {
  if (req.method != 'GET') return null;
  return switch (req.url.path) {
    '/v1/stock/stock-items' => _ok(stockItems),
    '/v1/money/labels' => _ok({
      'labels': spendLabels,
      'suggestions': req.url.queryParameters['item'] == null
          ? <Object>[]
          : [
              {'name': 'Packaging', 'label_id': 'lb-pack'},
              {'name': 'Rice'},
            ],
    }),
    _ => null,
  };
}

/// What a remembered item looks like (ItemMemory).
Map<String, Object?> memory(String name, String treatment, {String? sellerItem, String? stockItemId, String? stockItem, String? labelId, String? label}) => {
  'name': name,
  'seller_item_id': ?sellerItem,
  'treatment': treatment,
  'stock_item_id': ?stockItemId,
  'stock_item': ?stockItem,
  'uses': 1,
  'label_id': ?labelId,
  'label': ?label,
  'updated_by': 'Ada',
  'updated_at': '2026-11-10T09:00:00Z',
};
