// The chat's tools as the web's (web/src/lib/tools.test.ts, the same cases).

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/chat/tools.dart';
import 'package:taskiem/chat/words.dart';

Tool tool(String k) => tools.firstWhere((t) => t.key == k);
List<String> keys(List<Tool> ts) => [for (final t in ts) t.key];
List<String> ids(List<ToolRow> rs) => [for (final r in rs) r.id];

void main() {
  final stock = {
    'items': [
      {'id': '1', 'name': 'Rice, 50kg bag', 'available': true, 'stock': 42, 'unit': 'bag', 'price_kobo': 7800000},
      {'id': '2', 'name': 'Rice, 25kg bag', 'available': true, 'stock': 4, 'low': true, 'price_kobo': 4100000},
      {'id': '3', 'name': 'Old beans', 'available': false, 'stock': null},
    ],
  };

  test('lists products with their price and stock, and counts the low ones', () {
    final rows = tool('products').rows(stock);
    expect(rows[0].name, 'Rice, 50kg bag');
    expect(rows[0].sub, '₦78,000 a bag');
    expect(rows[0].pill, (text: '42 in stock', tone: CardTone.plain));
    expect(rows[1].pill, (text: '4 left', tone: CardTone.warn));
    expect(rows[2].sub, 'No price yet');
    expect(tool('products').line(rows), '3 items');
    expect(tool('stock').badge!(tool('stock').rows(stock)), (text: '1 low', tone: CardTone.warn));
    // The stock tool lists every item, those with no count too.
    expect(ids(tool('stock').rows(stock)), ['2', '1', '3']);
    expect(tool('stock').rows(stock)[2].pill, (text: 'No count', tone: CardTone.plain));
  });

  test('filters by chip and by search', () {
    final rows = tool('products').rows(stock);
    expect(ids(filterRows(rows, 'low', '')), ['2']);
    expect(ids(filterRows(rows, 'hidden', '')), ['3']);
    expect(ids(filterRows(rows, null, '50kg')), ['1']);
  });

  test('offers only the tools a person may open, matching what follows /', () {
    bool all(String _) => true;
    expect(keys(toolsFor((p) => p == 'table.read')), ['products', 'stock', 'tables', 'purchases']);
    expect(keys(toolsFor(all, query: 'pri')), ['prices']);
    // Staff see their role's tools; the handover is a shift manager's.
    expect(keys(toolsFor(all, roles: ['cashier'])), ['sell', 'stock', 'orders']);
    expect(keys(toolsFor(all, roles: ['shift_manager'])), ['sell', 'handover', 'stock', 'orders', 'bills']);
    expect(keys(toolsFor(all, roles: ['owner'])), isNot(contains('handover')));
    expect(toolsFor(all, roles: ['cashier', 'admin']).length, greaterThan(5));
    expect(keys(toolsFor(all, query: 'lists')), ['prices']);
    // Availability is a delivery business's alone.
    expect(keys(toolsFor(all)), isNot(contains('availability')));
    expect(keys(toolsFor(all, delivers: true)), contains('availability'));
    expect(keys(toolsFor(all, query: 'ava', delivers: true)), ['availability']);
  });

  test("words a rider's availability and shows it on the tile", () {
    final avail = tool('availability');
    final soon = DateTime.now().copyWith(hour: 12, minute: 30, second: 0, millisecond: 0, microsecond: 0);
    expect(availabilityWord({'state': 'free', 'businesses': 1}), 'Free');
    expect(availabilityWord({'state': 'off', 'businesses': 1}), 'Off today');
    expect(availabilityWord({'state': 'busy', 'until': soon.toUtc().toIso8601String(), 'businesses': 1}), 'Busy till 12:30');
    // Past midnight: the day too (time.ts shortWhen).
    final late = DateTime(2026, 11, 12, 23, 40);
    final after = DateTime(2026, 11, 13, 0, 40);
    expect(availabilityWord({'state': 'busy', 'until': after.toUtc().toIso8601String(), 'businesses': 1}, late), 'Busy till 13 Nov, 00:40');
    expect(availabilityWord({'state': 'busy', 'until': late.add(const Duration(minutes: 10)).toUtc().toIso8601String(), 'businesses': 1}, late), 'Busy till 23:50');
    expect(availabilityWord({'state': 'busy', 'open': 2, 'businesses': 1}), 'Busy, 2 deliveries');
    expect(availabilityWord({'state': 'busy', 'open': 1, 'businesses': 1}), 'Busy, 1 delivery');
    final rows = avail.rows({'state': 'off', 'businesses': 2});
    expect(avail.line(rows), 'Off today');
    expect(avail.badge!(rows), (text: 'Off', tone: CardTone.warn));
    expect(avail.badge!(avail.rows({'state': 'free', 'businesses': 2})), isNull);
    expect(avail.line(avail.rows(null)), 'Free');
  });

  test('words orders, new ones first in the badge', () {
    final rows = tool('orders').rows({
      'orders': [
        {'id': 'o1', 'order_reference': 'AD-1', 'buyer_name': 'Ada', 'total_kobo': 82000000, 'status': 'received'},
      ],
    });
    expect(rows[0].name, 'AD-1');
    expect(rows[0].sub, 'Ada · ₦820,000');
    expect(rows[0].pill, (text: 'New', tone: CardTone.lime));
    expect(tool('orders').badge!(rows), (text: '1 new', tone: CardTone.lime));
  });

  test('tile letters as the boards draw them', () {
    expect(tileLetters('Jollof rice'), 'JR');
    expect(tileLetters('Party pack, 10 plates'), 'PP');
    expect(tileLetters('Zobo'), 'ZB');
    expect(tileLetters('Chapman'), 'CH');
  });
}
