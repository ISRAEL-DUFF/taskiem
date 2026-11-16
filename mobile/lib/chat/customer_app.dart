// A customer who chats in the Taskiem app, on the business's side
// (docs/spec/taskiem-for-everyone.md §8.3, §15): the thread has no 24-hour
// window and no templates, and replies can be rich. The + by the box sends
// an order's card, a pay link, a booking offer, a product from the
// catalogue, a photo or a file, or (to a customer who said yes) an offer.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/forms.dart' show errorWords;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'attachments.dart';
import 'composer.dart' show Pickers;
import 'customer.dart';
import 'door.dart';
import 'words.dart';

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
String _str(Object? v) => v is String ? v : '';

/// What + offers: what is sent, in words.
enum RichKind { order, pay, booking, product, photo, file, offer }

/// The + by the box of an app customer's thread.
Future<void> showRichReply(BuildContext context, CustomerData d, {Pickers pickers = const Pickers(), required TextEditingController box}) async {
  final c = d.chat;
  if (c == null) return;
  final kind = await showTkSheet<RichKind>(context, (_) => _RichMenu(offers: c.offers == 'yes'));
  if (kind == null || !context.mounted) return;
  switch (kind) {
    case RichKind.photo:
      final f = await pickers.photo();
      if (f != null && context.mounted) await _file(context, d, f);
    case RichKind.file:
      final f = await pickers.document();
      if (f != null && context.mounted) await _file(context, d, f);
    case RichKind.offer:
      final words = box.text.trim();
      if (words.isEmpty) {
        showTkToast(context, 'Write the offer in the box first, then send it as an offer.', error: true);
        return;
      }
      box.clear();
      await d.send(words, offer: true);
    case RichKind.order || RichKind.pay || RichKind.booking || RichKind.product:
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
  const _RichMenu({required this.offers});
  final bool offers;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    Widget row(RichKind k, String icon, String name, String sub) => TkRow(
      name: name,
      sub: sub,
      icon: icon,
      trailing: TkIcon('chevron', size: 18, color: t.muted),
      onTap: () => Navigator.of(context).pop(k),
    );
    return TkSheet(
      title: 'Send',
      body: ListView(
        padding: const EdgeInsets.symmetric(horizontal: 12),
        children: [
          row(RichKind.order, 'b-box', 'An order', 'Its card: what, the total, its state'),
          row(RichKind.pay, 'money', 'A pay link', 'What’s due on an order, to pay in the app'),
          row(RichKind.booking, 'calendar', 'A booking offer', 'A service they can book from the chat'),
          row(RichKind.product, 'tag', 'A product', 'An item from your catalogue'),
          row(RichKind.photo, 'image', 'A photo', 'Up to 10 MB'),
          row(RichKind.file, 'file', 'A PDF', 'Up to 25 MB'),
          if (offers) row(RichKind.offer, 'sparkle', 'Send as an offer', 'The words in the box, marked Offer. At most 2 a week.'),
        ],
      ),
    );
  }
}

/// Choosing what a card is of: one of the business's orders, a service, an
/// item of its catalogue.
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

  Future<void> _load() async {
    try {
      final rows = switch (widget.kind) {
        RichKind.booking => _maps(((await _t.send('GET', '/v1/storefront/services')) as Map)['items']),
        RichKind.product => _maps(((await _t.send('GET', '/v1/storefront/catalogue')) as Map)['items']).where((i) => _str(i['variant_of']).isEmpty).toList(),
        _ => _maps(((await _t.send('GET', '/v1/storefront/orders')) as Map)['orders']),
      };
      // This customer's orders first, by the name in the book.
      final name = widget.d.chat?.name ?? '';
      if (widget.kind == RichKind.order || widget.kind == RichKind.pay) {
        rows.sort((a, b) => (_str((b['customer'] as Map?)?['name']) == name ? 1 : 0) - (_str((a['customer'] as Map?)?['name']) == name ? 1 : 0));
      }
      if (mounted) setState(() => _rows = rows);
    } on ApiError catch (e) {
      if (mounted) setState(() => e.status == 404 ? _rows = const [] : _error = e.message);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    }
  }

  void _pick(Map<String, dynamic> r) {
    final k = widget.kind;
    Navigator.of(context).pop(switch (k) {
      RichKind.booking => ReplyCustomerRequestCard(type: 'booking', serviceId: _str(r['id'])),
      RichKind.product => ReplyCustomerRequestCard(type: 'product', itemId: _str(r['id'])),
      _ => ReplyCustomerRequestCard(type: k == RichKind.pay ? 'pay' : 'order', reference: _str(r['reference'])),
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final k = widget.kind;
    final title = switch (k) {
      RichKind.booking => 'A booking offer',
      RichKind.product => 'A product',
      RichKind.pay => 'A pay link',
      _ => 'An order',
    };
    final rows = _rows;
    final byRef = k == RichKind.order || k == RichKind.pay;
    return TkSheet(
      title: title,
      footer: byRef ? TkCta(label: 'Send', onTap: () {
        final r = _reference.text.trim();
        if (r.isNotEmpty) Navigator.of(context).pop(ReplyCustomerRequestCard(type: k == RichKind.pay ? 'pay' : 'order', reference: r));
      }) : null,
      body: ListView(
        padding: const EdgeInsets.symmetric(horizontal: 12),
        children: [
          if (byRef)
            Padding(
              padding: const EdgeInsets.fromLTRB(8, 0, 8, 12),
              child: TkKitField(label: 'Order reference', hint: 'Or choose one of your orders below.', child: TkTextBox(controller: _reference, hint: 'MK-1042')),
            ),
          if (_error != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 8), child: TkErrorBox(_error!)),
          if (rows == null && _error == null) const TkLoading(label: 'Loading…'),
          if (rows != null && rows.isEmpty)
            TkEmpty(
              icon: k == RichKind.booking ? 'calendar' : 'b-box',
              title: switch (k) {
                RichKind.booking => 'No services to book',
                RichKind.product => 'Nothing in your catalogue',
                _ => 'No orders yet',
              },
              line: 'Add them in your shop first.',
            ),
          for (final r in rows ?? const <Map<String, dynamic>>[])
            switch (k) {
              RichKind.booking || RichKind.product => TkRow(
                name: _str(r['name']),
                sub: (r['price_kobo'] as num?) == null ? '' : naira((r['price_kobo'] as num).toInt()),
                icon: k == RichKind.booking ? 'calendar' : 'tag',
                onTap: () => _pick(r),
              ),
              _ => TkRow(
                name: '${_str(r['reference'])} · ${_str((r['customer'] as Map?)?['name']).isEmpty ? 'A customer' : _str((r['customer'] as Map?)?['name'])}',
                sub: [yourOrderStatus(_str(r['status'])), if ((r['total_kobo'] as num?) != null) naira((r['total_kobo'] as num).toInt())].join(' · '),
                icon: 'b-box',
                onTap: () => _pick(r),
              ),
            },
          Padding(padding: const EdgeInsets.fromLTRB(8, 12, 8, 0), child: Text('Taskiem makes the card from your own records.', style: tkHint(t))),
        ],
      ),
    );
  }
}
