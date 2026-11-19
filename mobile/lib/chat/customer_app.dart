// A customer who chats in the Taskiem app, on the business's side
// (docs/spec/taskiem-for-everyone.md §8.3, §15): the thread has no 24-hour
// window and no templates, and replies can be rich. The + by the box sends
// an order's card, a pay link, a booking offer, a product from the
// catalogue, an invoice sent to them, the rider's progress on their order,
// a photo or a file, or (to a customer who said yes) an offer. To a
// customer on WhatsApp too (§11) they go in the app, and the + says so.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/mobile.dart' show TkFormGroup, TkListRow, TkSection;
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart';
import 'composer.dart' show Pickers;
import 'customer.dart';
import 'door.dart';
import 'words.dart';
import 'pick_problem.dart';

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
String _str(Object? v) => v is String ? v : '';

/// What + offers: what is sent, in words.
enum RichKind { order, pay, invoice, delivery, booking, product, photo, file, offer }

/// The + by the box of an app customer's thread.
Future<void> showRichReply(BuildContext context, CustomerData d, {Pickers pickers = const Pickers(), required TextEditingController box}) async {
  final c = d.chat;
  if (c == null) return;
  final kind = await showTkSheet<RichKind>(context, (_) => _RichMenu(offers: c.offers == 'yes', invoices: d.chats.auth.can('invoice.manage'), both: d.both));
  if (kind == null || !context.mounted) return;
  switch (kind) {
    case RichKind.photo:
      final PickedFile? f;
      try {
        f = await pickers.photo();
      } on Object catch (e) {
        if (context.mounted) sayPickProblem(context, e, camera: false);
        return;
      }
      if (f != null && context.mounted) await _file(context, d, f);
    case RichKind.file:
      final PickedFile? f;
      try {
        f = await pickers.document();
      } on Object {
        if (context.mounted) showTkToast(context, 'That could not be opened. Try again.', error: true);
        return;
      }
      if (f != null && context.mounted) await _file(context, d, f);
    case RichKind.offer:
      final words = box.text.trim();
      if (words.isEmpty) {
        showTkToast(context, 'Write the offer in the box first, then send it as an offer.', error: true);
        return;
      }
      // It goes as an offer, in the app, or not at all: never as a plain
      // message because the app line shut or they said no to offers while
      // the sheet was open (customerlines.ts offerRefused). The words stay.
      final refused = d.offerRefused();
      if (refused != null) {
        showTkToast(context, refused, error: true);
        return;
      }
      if (d.busy) return;
      box.clear();
      await d.send(words, offer: true);
    case RichKind.order || RichKind.pay || RichKind.invoice || RichKind.delivery || RichKind.booking || RichKind.product:
      final card = await showTkSheet<ReplyCustomerRequestCard>(context, (_) => _CardPicker(d: d, kind: kind));
      if (card != null) await d.sendCard(card);
  }
}

Future<void> _file(BuildContext context, CustomerData d, PickedFile f) async {
  final problem = fileProblem(f, doorFiles);
  if (problem != null) {
    showTkToast(context, problem, error: true);
    return;
  }
  await d.sendFile(f);
}

class _RichMenu extends StatelessWidget {
  const _RichMenu({required this.offers, required this.invoices, this.both = false});
  final bool offers;
  final bool invoices;

  /// The customer is on WhatsApp too (§11): what is sent here goes in the app.
  final bool both;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    Widget row(RichKind k, String icon, Color colour, String name, String sub) => TkListRow(
      title: name,
      sub: sub,
      icon: icon,
      square: colour,
      onTap: () => Navigator.of(context).pop(k),
    );
    // As the chat's tools are drawn: each on its accent's square, the rows
    // on cards on the sheet's ground.
    return TkAppLook(
      child: TkSheet(
        title: 'Send',
        crumb: both ? 'In the app, not on WhatsApp: cards, offers and files go there' : 'To this customer, made from your own records',
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            TkSection(
              inset: 56,
              children: [
                row(RichKind.order, 'b-box', look.teal, 'An order', 'Its card: what, the total, its state'),
                row(RichKind.pay, 'b-card', look.lime, 'A pay link', 'What’s due on an order, to pay in the app'),
                if (invoices) row(RichKind.invoice, 'b-receipt', look.blue, 'An invoice', 'One you sent them: what’s due, and its page'),
                row(RichKind.delivery, 'b-bike', look.blue, 'A delivery', 'How far the rider has got with their order'),
                row(RichKind.booking, 'b-calendar', look.violet, 'A booking offer', 'A service they can book from the chat'),
                row(RichKind.product, 'b-bag', look.orange, 'A product', 'An item from your catalogue'),
              ],
            ),
            const SizedBox(height: 14),
            TkSection(
              inset: 56,
              children: [
                row(RichKind.photo, 'b-camera', look.teal, 'A photo', 'Up to 10 MB'),
                row(RichKind.file, 'b-note', look.blue, 'A PDF', 'Up to 25 MB'),
                if (offers) row(RichKind.offer, 'b-bolt', look.orange, 'Send as an offer', 'The words in the box, marked Offer. At most 2 a week.'),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// Choosing what a card is of: one of the business's orders, an invoice, a
/// service, an item of its catalogue.
class _CardPicker extends StatefulWidget {
  const _CardPicker({required this.d, required this.kind});
  final CustomerData d;
  final RichKind kind;

  @override
  State<_CardPicker> createState() => _CardPickerState();
}

class _CardPickerState extends State<_CardPicker> {
  List<Map<String, dynamic>>? _rows;
  String? _error;
  final _reference = TextEditingController();

  Transport get _t => widget.d.chats.auth.transport;
  RichKind get _k => widget.kind;

  /// Chosen by an order's reference (an order, its pay link, its delivery).
  bool get _byRef => _k == RichKind.order || _k == RichKind.pay || _k == RichKind.delivery;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _reference.dispose();
    super.dispose();
  }

  String _customer(Map<String, dynamic> r) => _str((r['customer'] as Map?)?['name']);

  Future<void> _load() async {
    try {
      var rows = switch (_k) {
        RichKind.booking => _maps(((await _t.send('GET', '/v1/storefront/services')) as Map)['items']),
        RichKind.product => _maps(((await _t.send('GET', '/v1/storefront/catalogue')) as Map)['items']).where((i) => _str(i['variant_of']).isEmpty).toList(),
        // Only invoices sent: a draft or a voided one is not for customers.
        RichKind.invoice => _maps(((await _t.send('GET', '/v1/invoices', query: {'limit': '200'})) as Map)['invoices']).where((i) => !const {'draft', 'void'}.contains(i['status'])).toList(),
        _ => _maps(((await _t.send('GET', '/v1/storefront/orders')) as Map)['orders']),
      };
      // This customer's first, by the name in the book.
      final name = widget.d.chat?.name ?? '';
      if (_byRef || _k == RichKind.invoice) {
        rows = [...rows.where((r) => _customer(r) == name), ...rows.where((r) => _customer(r) != name)];
      }
      if (mounted) setState(() => _rows = rows);
    } on ApiError catch (e) {
      if (mounted) setState(() => e.status == 404 ? _rows = const [] : _error = e.message);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  String get _type => switch (_k) {
    RichKind.pay => 'pay',
    RichKind.delivery => 'delivery',
    _ => 'order',
  };

  void _pick(Map<String, dynamic> r) {
    Navigator.of(context).pop(switch (_k) {
      RichKind.booking => ReplyCustomerRequestCard(type: 'booking', serviceId: _str(r['id'])),
      RichKind.product => ReplyCustomerRequestCard(type: 'product', itemId: _str(r['id'])),
      RichKind.invoice => ReplyCustomerRequestCard(type: 'invoice', invoiceId: _str(r['id'])),
      _ => ReplyCustomerRequestCard(type: _type, reference: _str(r['reference'])),
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final title = switch (_k) {
      RichKind.booking => 'A booking offer',
      RichKind.product => 'A product',
      RichKind.pay => 'A pay link',
      RichKind.invoice => 'An invoice',
      RichKind.delivery => 'A delivery',
      _ => 'An order',
    };
    final rows = _rows;
    final name = widget.d.chat?.name ?? '';
    int? kobo(Object? v) => (v as num?)?.toInt();
    Widget row(Map<String, dynamic> r) => switch (_k) {
      RichKind.booking || RichKind.product => TkListRow(
        title: _str(r['name']),
        sub: kobo(r['price_kobo']) == null ? '' : naira(kobo(r['price_kobo'])!),
        icon: _k == RichKind.booking ? 'b-calendar' : 'b-bag',
        square: _k == RichKind.booking ? look.violet : look.orange,
        onTap: () => _pick(r),
      ),
      RichKind.invoice => TkListRow(
        title: 'Invoice ${_str(r['number'])}',
        sub: _customer(r).isEmpty ? 'A customer' : (_customer(r) == name ? '${_customer(r)} · this customer' : _customer(r)),
        icon: 'b-receipt',
        square: look.blue,
        amount: kobo(r['due_kobo']) == null ? null : naira(kobo(r['due_kobo'])!),
        status: invoiceWords(_str(r['status'])),
        statusTone: doorTone(_str(r['status'])),
        onTap: () => _pick(r),
      ),
      _ => TkListRow(
        title: '${_str(r['reference'])} · ${_customer(r).isEmpty ? 'A customer' : _customer(r)}',
        sub: yourOrderStatus(_str(r['status'])),
        icon: _k == RichKind.delivery ? 'b-bike' : 'b-box',
        square: _k == RichKind.delivery ? look.blue : look.teal,
        amount: kobo(r['total_kobo']) == null ? null : naira(kobo(r['total_kobo'])!),
        onTap: () => _pick(r),
      ),
    };
    return TkAppLook(
      child: TkSheet(
        title: title,
        crumb: 'Send',
        footer: _byRef
            ? TkCta(
                label: 'Send',
                onTap: () {
                  final r = _reference.text.trim();
                  if (r.isNotEmpty) Navigator.of(context).pop(ReplyCustomerRequestCard(type: _type, reference: r));
                },
              )
            : null,
        body: ListView(
          padding: const EdgeInsets.fromLTRB(14, 0, 14, 16),
          children: [
            if (_byRef) ...[
              TkFormGroup(
                children: [TkKitField(label: 'Order reference', hint: 'Or choose one of your orders below.', child: TkTextBox(controller: _reference, hint: 'MK-1042'))],
              ),
              const SizedBox(height: 14),
            ],
            if (_error != null) TkErrorBox(_error!),
            if (rows == null && _error == null) const TkLoading(label: 'Loading…'),
            if (rows != null && rows.isEmpty)
              TkEmpty(
                icon: _k == RichKind.booking ? 'calendar' : 'b-box',
                title: switch (_k) {
                  RichKind.booking => 'No services to book',
                  RichKind.product => 'Nothing in your catalogue',
                  RichKind.invoice => 'No invoices sent yet',
                  _ => 'No orders yet',
                },
                line: _k == RichKind.invoice ? 'Send them an invoice first.' : 'Add them in your shop first.',
              ),
            if (rows != null && rows.isNotEmpty) TkSection(inset: 56, children: [for (final r in rows) row(r)]),
            Padding(padding: const EdgeInsets.fromLTRB(4, 12, 4, 0), child: Text('Taskiem makes the card from your own records.', style: tkHint(t))),
          ],
        ),
      ),
    );
  }
}
