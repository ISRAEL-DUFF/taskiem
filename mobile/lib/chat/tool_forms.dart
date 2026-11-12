// What a tool's row opens in its sheet, and its Add (web ChatTools.tsx
// Detail): each tool's form, where the app has it; null leaves the row
// saying its screens are coming.


import '../app/auth.dart';
import 'composer.dart' show Pickers;
import 'product_form.dart';
import 'purchase_view.dart';
import 'service_form.dart';
import 'tool_sheet.dart';

ToolDetail toolDetail(
  Auth auth, {
  void Function(Map<String, dynamic> purchase)? onReorder,
  String? link,
  Pickers pickers = const Pickers(),
}) => (context, tool, row, host) {
  final data = row?.data;
  if (row == null) {
    return switch (tool.key) {
      'products' => ProductAdd(auth: auth, onSaved: host.done, pickers: pickers),
      'services' => ServiceForm(auth: auth, onSaved: host.done),
      _ => null,
    };
  }
  if (data == null) return null;
  return switch (tool.key) {
    'products' => ProductForm(
      auth: auth,
      item: data,
      onSaved: host.done,
      canEdit: host.canEdit,
      onCancel: host.back,
      link: link,
      pickers: pickers,
    ),
    'stock' => StockInForm(auth: auth, item: data, onSaved: host.done, canEdit: host.canEdit),
    'services' => ServiceForm(auth: auth, service: data, onSaved: host.done, canEdit: host.canEdit),
    'prices' => PriceListForm(auth: auth, list: data, onSaved: host.done, canEdit: host.canEdit),
    'purchases' => PurchaseView(
      auth: auth,
      purchase: data,
      onSaved: host.done,
      onReorder: (data['link_id'] as String? ?? '').isEmpty ? null : onReorder,
    ),
    _ => null,
  };
};
