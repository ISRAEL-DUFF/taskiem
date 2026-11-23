// The stock items the Stock page and a product's Counted by read (S2;
// GET /v1/stock/stock-items), for their tests and previews.

/// GET /v1/stock/stock-items: a product, one sold two ways (a pack of 6
/// uses 6), a material, one not sold yet, and a shirt's two sizes.
final stockItems = <String, Object?>{
  'keeps_stock': true,
  'value_kobo': 8300000,
  'low': 2,
  'items': [
    _si('si-shirt-l', 'Ankara shirt (L)', 1, 'piece', group: 'shirt', groupName: 'Ankara shirt', low: true, lowLevel: 2, suggest: 3, sold: [_sold('shirt-l', 'Ankara shirt', variant: {'Size': 'L'})]),
    _si('si-shirt-m', 'Ankara shirt (M)', 3, 'piece', group: 'shirt', groupName: 'Ankara shirt', sold: [_sold('shirt-m', 'Ankara shirt', variant: {'Size': 'M'})]),
    _si('si-indomie', 'Indomie', 4, 'carton', cost: 850000, low: true, lowLevel: 10, suggest: 46, supplier: 'Dangote', value: 3400000, sold: [_sold('indomie', 'Indomie', price: 950000)]),
    _si('si-malt', 'Malt', 30, 'bottle', sold: [_sold('malt', 'Malt'), _sold('malt-6', 'Malt, pack of 6', uses: 6)]),
    _si('si-nylon', 'Nylon bags', 200, 'bag', lowLevel: 50),
    _si('si-rice', 'Rice', 20, 'bag', cost: 245000, value: 4900000, sold: [_sold('rice', 'Rice')]),
  ],
};

Map<String, Object?> _sold(String id, String name, {int uses = 1, int? price, Map<String, String>? variant}) =>
    {'product_id': id, 'name': name, 'uses': uses, 'price_kobo': price, 'variant': variant, 'available': true};

Map<String, Object?> _si(
  String id,
  String name,
  int count,
  String unit, {
  String group = '',
  String groupName = '',
  bool low = false,
  int? lowLevel,
  int? suggest,
  int? cost,
  String supplier = '',
  int value = 0,
  List<Map<String, Object?>> sold = const [],
}) => {
  'id': id,
  'name': name,
  'unit': unit,
  'category': '',
  'count': count,
  'cost_kobo': cost,
  'low_level': lowLevel,
  'target_level': null,
  'supplier_id': null,
  'supplier': supplier,
  'group_of': group,
  'group_name': groupName,
  'code': '',
  'low': low,
  'suggest': suggest,
  'value_kobo': value,
  'retired': false,
  'sold_as': sold,
};
