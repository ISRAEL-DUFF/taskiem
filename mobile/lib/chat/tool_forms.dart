// What a tool's row opens in its sheet, and its Add (web ChatTools.tsx
// Detail): each tool's form, where the app has it; null leaves the row
// saying its screens are coming.

import 'package:flutter/widgets.dart';

import '../app/auth.dart';
import 'composer.dart' show Pickers;
import 'money_sheets.dart';
import 'order_view.dart';
import 'table_tool.dart' show TableTool;
import 'product_form.dart';
import 'purchase_view.dart';
import 'service_form.dart';
import 'tool_sheet.dart';
import 'invoices_page.dart' show openInvoice;

ToolDetail toolDetail(
  Auth auth, {
  void Function(Map<String, dynamic> purchase)? onReorder,
  String? link,
  Pickers pickers = const Pickers(),
  MoneyOpeners? openers,
}) => (context, tool, row, host) {
  final data = row?.data;
  if (row == null) {
    return switch (tool.key) {
      'products' => ProductAdd(auth: auth, onSaved: host.done, pickers: pickers),
      'services' => ServiceForm(auth: auth, onSaved: host.done),
      'invoices' => InvoiceAdd(auth: auth, onSaved: host.done, onCancel: host.back),
      'money' => MoneyAdd(auth: auth, onSaved: host.done, onCancel: host.back),
      _ => null,
    };
  }
  if (data == null) return null;
  return switch (tool.key) {
    'products' => ProductForm(auth: auth, item: data, onSaved: host.done, canEdit: host.canEdit, onCancel: host.back, link: link, pickers: pickers),
    'stock' => StockInForm(auth: auth, item: data, onSaved: host.done, canEdit: host.canEdit),
    'services' => ServiceForm(auth: auth, service: data, onSaved: host.done, canEdit: host.canEdit),
    'prices' => PriceListForm(auth: auth, list: data, onSaved: host.done, canEdit: host.canEdit),
    'purchases' => PurchaseView(auth: auth, purchase: data, onSaved: host.done, onReorder: (data['link_id'] as String? ?? '').isEmpty ? null : onReorder),
    'invoices' => InvoiceView(
      auth: auth,
      invoice: data,
      onSaved: host.done,
      canEdit: host.canEdit,
      onOpen: openers?.invoice ?? (context, id) => openInvoice(context, auth, id),
    ),
    'bills' => BillView(auth: auth, bill: data, onSaved: host.done, canEdit: host.canEdit, onOpen: openers?.bill),
    'money' => MoneyEntryView(auth: auth, entry: data, onSaved: host.done, canEdit: host.canEdit),
    'tables' => TableTool(auth: auth, table: data, onSaved: host.done, canEdit: host.canEdit),
    'orders' => OrderView(auth: auth, order: data, onSaved: host.done, canEdit: host.canEdit, pickers: pickers),
    _ => null,
  };
};

/// Where a money row's own screen opens (an invoice's, a bill's); null in
/// the sheet says it is coming.
class MoneyOpeners {
  const MoneyOpeners({this.invoice, this.bill});
  final void Function(BuildContext context, String id)? invoice;
  final void Function(BuildContext context, String id)? bill;
}
