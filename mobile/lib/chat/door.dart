// What both sides of a customer's chat in the app show
// (docs/spec/taskiem-for-everyone.md §8.3, §8.5, §15): a message's bubble
// with its photo, voice note or file, an order, pay, booking or product
// card, and an offer marked Offer. The person's side (You, my_chat.dart)
// and the business's (customer.dart) draw messages the same way.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkPill;
import '../theme/theme.dart';
import 'attachments.dart';
import 'bookings_page.dart' show orderStatus;
import 'thread.dart';
import 'words.dart';

/// An order's state, as its customer reads it: one just placed is Sent,
/// one the business has not taken in yet waits for them.
String yourOrderStatus(String s) => switch (s) {
  'sent' => 'Sent',
  'received' => 'Waiting for them to accept',
  _ => orderStatus(s).label,
};

/// A state's pill tone, for either side.
CardTone doorTone(String s) => switch (s) {
  'completed' || 'paid' || 'delivered' => CardTone.lime,
  'declined' || 'cancelled' || 'expired' || 'refunded' || 'no_show' => CardTone.plain,
  'refund_due' => CardTone.danger,
  _ => CardTone.warn,
};

/// The words the server gives a file with no caption: not said again
/// under the file.
const _fileWords = {'Photo', 'Voice note', 'File'};

/// A message's file as the chat's files are drawn: the photo, voice note
/// or file of [id], fetched from where [ChatFiles] says.
LinkMessage doorFileMessage({required String id, required bool mine, required ChatAttachment a, required DateTime at}) => LinkMessage(
  id: id,
  link: '',
  mine: mine,
  kind: 'text',
  createdAt: at,
  attachment: LinkMessageAttachment(kind: a.kind, name: a.name, size: a.size, type: a.type),
);

/// A card's kicker: what it is.
String cardKind(CustomerCard c) => switch (c.type) {
  'order' => c.title.startsWith('Booking') ? 'Booking' : 'Order',
  'pay' => 'Pay',
  'booking' => 'Book',
  'product' => 'Product',
  _ => 'Card',
};

/// A card in a customer's chat: an order (what, total, its state), a pay
/// link, a booking offer, a product; and its button, where the side
/// reading it can act on it.
class DoorCard extends StatelessWidget {
  const DoorCard({super.key, required this.card, this.action, this.onAction});
  final CustomerCard card;

  /// The button's words (Pay, Book, See in their shop) and what it does.
  final String? action;
  final VoidCallback? onAction;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final c = card;
    final minutes = c.minutes == null || c.minutes == 0 ? null : (c.minutes! >= 60 ? '${c.minutes! ~/ 60} h${c.minutes! % 60 == 0 ? '' : ' ${c.minutes! % 60} min'}' : '${c.minutes} min');
    final line = [if ((c.line ?? '').isNotEmpty) c.line!, ?minutes].join(' · ');
    return Semantics(
      container: true,
      label: '${cardKind(c)}: ${c.title}',
      child: Container(
        margin: const EdgeInsets.only(top: 2, bottom: 4),
        padding: const EdgeInsets.all(12),
        constraints: const BoxConstraints(minWidth: 220),
        decoration: BoxDecoration(color: t.panel2, borderRadius: BorderRadius.circular(12)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              children: [
                Expanded(child: Text(cardKind(c).toUpperCase(), style: tkText(size: 11, weight: 700, letterSpacing: 0.4, color: t.muted))),
                if ((c.status ?? '').isNotEmpty) TkPill(yourOrderStatus(c.status!), tone: doorTone(c.status!), small: true),
              ],
            ),
            const SizedBox(height: 4),
            Text(c.title, style: tkText(size: 15, weight: 700, lineHeight: 1.3, color: t.text)),
            if (line.isNotEmpty) Text(line, style: tkText(size: 13, lineHeight: 1.35, color: t.muted)),
            if (c.totalKobo != null) ...[
              const SizedBox(height: 4),
              Text(c.type == 'pay' ? '${naira(c.totalKobo!)} due' : naira(c.totalKobo!), style: tkText(size: 15, weight: 700, color: t.text)),
            ],
            if (action != null && onAction != null) ...[
              const SizedBox(height: 10),
              TkButton(label: action!, primary: c.type == 'pay', onPressed: onAction),
            ],
          ],
        ),
      ),
    );
  }
}

/// `.offer-mark`: Offer, over an offer's words.
class OfferMark extends StatelessWidget {
  const OfferMark({super.key});

  @override
  Widget build(BuildContext context) => const Padding(
    padding: EdgeInsets.only(bottom: 4),
    child: TkPill('Offer', tone: CardTone.warn, small: true),
  );
}

/// One message of a customer's chat in the app, on either side: who wrote
/// it (staff, on the business's side), an offer's mark, its file, its card,
/// its words, and under an offer what the side can do about it.
class DoorBubble extends StatelessWidget {
  const DoorBubble({
    super.key,
    required this.id,
    required this.mine,
    required this.first,
    required this.body,
    required this.at,
    required this.stamp,
    required this.files,
    required this.other,
    this.sender,
    this.card,
    this.attachment,
    this.offer = false,
    this.cardAction,
    this.onCard,
    this.under,
    this.local,
  });

  final String id;
  final bool mine;
  final bool first;
  final String body;
  final DateTime at;
  final Widget stamp;
  final ChatFiles files;

  /// Who the other side is, for a photo's words ("Photo from Mama's Kitchen").
  final String other;
  final String? sender;
  final CustomerCard? card;
  final ChatAttachment? attachment;
  final bool offer;
  final String? cardAction;
  final VoidCallback? onCard;

  /// Under the bubble's words: Stop offers from {business}.
  final Widget? under;

  /// Still being sent: this phone's own copy of the file.
  final PickedFile? local;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = attachment;
    final words = a != null && _fileWords.contains(body) ? '' : (card != null && body == card!.title ? '' : body);
    return LayoutBuilder(
      builder: (context, box) {
        final room = (box.maxWidth * 0.78 - 24).clamp(120.0, 360.0);
        return BubbleFrame(
          mine: mine,
          first: first,
          stamp: stamp,
          // The time under the words, never over them.
          inline: false,
          children: [
            if ((sender ?? '').isNotEmpty && first) Text(sender!, style: tkText(size: 12, weight: 600, color: t.tintText)),
            if (offer) const OfferMark(),
            if (a != null) AttachmentView(m: doorFileMessage(id: id, mine: mine, a: a, at: at), files: files, other: other, room: room, local: local),
            if (card != null) DoorCard(card: card!, action: cardAction, onAction: onCard),
            if (words.isNotEmpty) Text(words, style: tkText(size: 16, lineHeight: 23 / 16, color: t.text)),
            ?under,
          ],
        );
      },
    );
  }
}

/// A read tick for the person's own message: one when sent, two blue once
/// the business read it; a clock while it goes.
class DoorTicks extends StatelessWidget {
  const DoorTicks({super.key, required this.sending, required this.read, required this.meta});
  final bool sending;
  final bool read;
  final Color meta;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    if (sending) return Semantics(label: 'Sending', child: TkIcon('clock', size: 16, stroke: 2, color: meta));
    if (read) return Semantics(label: 'Read', child: TkIcon('tick-double', size: 16, stroke: 2, color: t.chatTickRead));
    return Semantics(label: 'Sent', child: TkIcon('tick', size: 16, stroke: 2, color: meta));
  }
}

/// A file for a customer's chat in the app: a photo up to 10 MB, a voice
/// note, or a PDF up to 25 MB (§8.5; the server checks again).
const doorFiles = FileRules(
  max: 25 * 1024 * 1024,
  maxWords: '25 MB',
  types: r'^(image/(jpeg|png|webp)|application/pdf|audio/)',
  typesMessage: 'Send a photo (JPEG, PNG or WebP), a PDF or a voice note.',
  documents: true,
);
