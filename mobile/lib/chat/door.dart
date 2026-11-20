// What both sides of a customer's chat in the app show
// (docs/spec/taskiem-for-everyone.md §8.3, §8.5, §15), in the app's own
// chat look (docs/spec/mobile-app-look.md, "The chat", "Cards in a
// conversation"): a message's bubble with its photo, voice note or file;
// an order, pay, invoice, delivery, booking or product card standing free
// of the bubbles; an offer marked Offer; and, under a business's message
// asking for a transfer, the warning to pay only through Taskiem. The
// person's side (You, my_chat.dart) and the business's (customer.dart) draw
// messages the same way.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../kit/icon.dart';
import '../kit/look.dart' show tkTabular;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'attachments.dart';
import 'bookings_page.dart' show orderStatus;
import 'cards.dart' show CardButton, CardPill, CardProgress, CardViewRow, cardDecoration;
import 'chats.dart' show ChatAvatar;
import 'saved_card.dart' show SavedCardView;
import 'thread.dart';
import 'words.dart';

/// An order's state, as its customer reads it: one just placed is Sent,
/// one the business has not taken in yet waits for them.
String yourOrderStatus(String s) => switch (s) {
  'sent' => 'Sent',
  'received' => 'Waiting for them to accept',
  _ => orderStatus(s).label,
};

/// A delivery's step, in a customer's words.
String deliveryStatus(String s) => switch (s) {
  'accepted' => 'Rider coming',
  'picked_up' => 'On its way',
  'arrived' => 'At your door',
  'delivered' => 'Delivered',
  _ => s.replaceAll('_', ' '),
};

/// An invoice's state, in a customer's words.
String invoiceWords(String s) => switch (s) {
  'sent' || 'recorded' => 'To pay',
  'part_paid' => 'Part-paid',
  'overdue' => 'Overdue',
  'paid' => 'Paid',
  'void' => 'Cancelled',
  _ => s.replaceAll('_', ' '),
};

/// A card's state in words, by its kind.
String cardStatus(CustomerCard c) => switch (c.type) {
  'delivery' => deliveryStatus(c.status ?? ''),
  'invoice' => invoiceWords(c.status ?? ''),
  _ => yourOrderStatus(c.status ?? ''),
};

/// A state's pill tone, for either side.
CardTone doorTone(String s) => switch (s) {
  'completed' || 'paid' || 'delivered' => CardTone.lime,
  // As the app's order states: what did not go ahead in red.
  'declined' || 'cancelled' || 'no_show' || 'refund_due' || 'overdue' => CardTone.danger,
  'expired' || 'refunded' || 'void' => CardTone.plain,
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
  'invoice' => 'Invoice',
  'delivery' => 'Delivery',
  'booking' => 'Book',
  'product' => 'Product',
  _ => 'Card',
};

/// A delivery's steps, and the one it is at.
const deliverySteps = ['Rider coming', 'Picked up', 'At your door', 'Delivered'];

int deliveryStep(DeliveryProgress d) => switch (d.status) {
  'delivered' => 3,
  'arrived' => 2,
  'picked_up' => 1,
  _ => 0,
};

/// A card in a customer's chat: an order (what, total, its state), a pay
/// link, an invoice (what is due), a delivery (the rider's progress), a
/// booking offer, a product; and its button, where the side reading it can
/// act on it. As drawn: the kind small in capitals with its state as a
/// pill, the amount large, a line of what it is, then its button.
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
    final line = [if ((c.line ?? '').isNotEmpty && c.type != 'delivery') c.line!, ?minutes].join(' · ');
    final kicker = [cardKind(c), if ((c.reference ?? '').isNotEmpty && !c.title.contains(c.reference!)) c.reference!].join(' · ');
    final due = c.type == 'pay' || (c.type == 'invoice' && (c.totalKobo ?? 0) > 0 && c.status != 'paid');
    final d = c.delivery;
    final amount = c.totalKobo;
    return Semantics(
      container: true,
      label: '${cardKind(c)}: ${c.title}',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  kicker.toUpperCase(),
                  semanticsLabel: kicker,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: tkText(size: 11, weight: 700, letterSpacing: 0.66, color: t.muted),
                ),
              ),
              if ((c.status ?? '').isNotEmpty) ...[const SizedBox(width: 8), CardPill(cardStatus(c), doorTone(c.status!))],
            ],
          ),
          const SizedBox(height: 6),
          if (amount != null && c.type != 'product' && c.type != 'booking') ...[
            Text(due ? '${naira(amount)} due' : naira(amount), style: tkDisplay(size: 24, lineHeight: 1.15, letterSpacing: -0.8, color: t.text)),
            const SizedBox(height: 2),
            Text(c.title, style: tkText(size: 13.5, weight: 600, lineHeight: 1.35, color: t.text)),
          ] else ...[
            Text(c.title, style: tkText(size: 15.5, weight: 600, lineHeight: 1.3, color: t.text)),
            if (amount != null) Text(naira(amount), style: tkText(size: 14, weight: 600, color: t.text).copyWith(fontFeatures: tkTabular)),
          ],
          if (line.isNotEmpty) Text(line, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
          if (d != null) ...[
            const SizedBox(height: 2),
            Text(d.line, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
            const SizedBox(height: 8),
            CardProgress(steps: deliverySteps, step: deliveryStep(d)),
          ],
          // An order's or a delivery's way to it is the app's card foot
          // (View order ›, under a hairline); any other a button.
          if (action != null && onAction != null && (c.type == 'order' || c.type == 'delivery'))
            CardViewRow(label: action!, semantics: '$action ${c.reference ?? ''}'.trim(), onTap: onAction)
          else if (action != null && onAction != null) ...[
            const SizedBox(height: 10),
            CardButton(label: action!, primary: c.type == 'pay' || c.type == 'invoice' || c.type == 'booking', onTap: onAction),
          ],
        ],
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
    child: Align(alignment: Alignment.centerLeft, child: CardPill('Offer', CardTone.warn)),
  );
}

/// The warning under a business's message that asks for a transfer to an
/// account (§15).
const transferWarning = 'Pay only through Taskiem’s pay links. Taskiem never asks you to send money to a personal account.';

/// Under a business's message that holds an account number: pay only
/// through Taskiem.
class TransferWarning extends StatelessWidget {
  const TransferWarning({super.key});

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Semantics(
      container: true,
      label: 'Warning: $transferWarning',
      excludeSemantics: true,
      child: Container(
        margin: const EdgeInsets.only(top: 4),
        constraints: const BoxConstraints(maxWidth: 340),
        padding: const EdgeInsets.fromLTRB(10, 8, 12, 8),
        decoration: BoxDecoration(color: look.red.withValues(alpha: 0.12), borderRadius: BorderRadius.circular(14)),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(padding: const EdgeInsets.only(top: 1), child: TkIcon('warn', size: 16, stroke: 2, color: look.redInk)),
            const SizedBox(width: 8),
            Expanded(child: Text(transferWarning, style: tkText(size: 13, weight: 600, lineHeight: 1.35, color: TkTheme.of(context).mode == TkMode.dark ? t.text : look.redInk))),
          ],
        ),
      ),
    );
  }
}

/// One message of a customer's chat in the app, on either side: who wrote
/// it (staff, on the business's side), an offer's mark, its file, its card,
/// its words, and under it what the side can do about it.
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
    this.last = true,
    this.sender,
    this.card,
    this.attachment,
    this.offer = false,
    this.cardAction,
    this.onCard,
    this.under,
    this.local,
    this.warn = false,
  });

  final String id;
  final bool mine;
  final bool first;

  /// The last of its run: its corner on the sender's side tucked in.
  final bool last;
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

  /// Under the bubble: Stop offers from {business}.
  final Widget? under;

  /// Still being sent: this phone's own copy of the file.
  final PickedFile? local;

  /// Asks for a transfer to an account: the warning under it.
  final bool warn;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final a = attachment;
    final c = card;
    final words = a != null && _fileWords.contains(body) ? '' : (c != null && body == c.title ? '' : body);
    final name = (sender ?? '').isNotEmpty && first ? Text(sender!, style: tkText(size: 12, weight: 600, color: t.tintText)) : null;
    // Your own words white on pine in the light (ownWords' ink): read here,
    // outside the bubble, the theme's own ink would be dark on pine.
    final ink = mine && TkTheme.of(context).mode != TkMode.dark ? const Color(0xFFFFFFFF) : t.text;
    final Widget shown;
    if (c != null && a == null) {
      // A card is its own message, on the card's frame, not a bubble's.
      shown = LayoutBuilder(
        builder: (context, box) => Align(
          alignment: mine ? Alignment.centerRight : Alignment.centerLeft,
          child: Container(
            width: (box.maxWidth * 0.86).clamp(0.0, 360.0),
            padding: const EdgeInsets.fromLTRB(14, 12, 14, 8),
            decoration: cardDecoration(context, mine: mine, first: first),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              mainAxisSize: MainAxisSize.min,
              children: [
                ?name,
                if (offer) const OfferMark(),
                if (c.type == 'saved' && c.saved != null)
                  SavedCardView(card: c.saved!, mine: mine, onSave: onCard)
                else
                  DoorCard(card: c, action: cardAction, onAction: onCard),
                if (words.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(words, style: tkText(size: 15, lineHeight: 1.4, color: t.text))),
                Padding(padding: const EdgeInsets.only(top: 4), child: Align(alignment: Alignment.centerRight, child: stamp)),
              ],
            ),
          ),
        ),
      );
    } else {
      shown = LayoutBuilder(
        builder: (context, box) {
          final room = (box.maxWidth * 0.78 - 24).clamp(120.0, 360.0);
          return BubbleFrame(
            mine: mine,
            first: first,
            last: last,
            stamp: stamp,
            // The time under the words, never over them.
            inline: false,
            children: [
              ?name,
              if (offer) const OfferMark(),
              if (a != null) AttachmentView(m: doorFileMessage(id: id, mine: mine, a: a, at: at), files: files, other: other, room: room, local: local),
              if (c != null) Padding(padding: const EdgeInsets.only(top: 4), child: DoorCard(card: c, action: cardAction, onAction: onCard)),
              if (words.isNotEmpty) Text(words, style: tkText(size: 16, lineHeight: 23 / 16, color: ink)),
            ],
          );
        },
      );
    }
    if (!warn && under == null) return shown;
    return Column(
      crossAxisAlignment: mine ? CrossAxisAlignment.end : CrossAxisAlignment.start,
      children: [
        shown,
        if (warn) const TransferWarning(),
        if (under != null) Padding(padding: const EdgeInsets.only(left: 4, right: 4), child: under),
      ],
    );
  }
}

/// A read tick for the person's own message: one when sent, two lime once
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

/// A business's photo's address for about [width] pixels: the smallest
/// size at least that wide (else the largest), a web address only.
String? photoUrl(Map<String, dynamic>? urls, {int width = 96}) {
  if (urls == null) return null;
  final sizes = <(int, String)>[
    for (final e in urls.entries)
      if (e.value is String && (e.value as String).startsWith(RegExp('https?://'))) (int.tryParse(e.key) ?? 0, e.value as String),
  ]..sort((a, b) => a.$1.compareTo(b.$1));
  if (sizes.isEmpty) return null;
  return sizes.firstWhere((s) => s.$1 >= width, orElse: () => sizes.last).$2;
}

/// A business's face: its photo, else its letters on its name's colour.
class BusinessFace extends StatelessWidget {
  const BusinessFace({super.key, required this.name, this.urls, this.size = 40, this.letterSize});
  final String name;
  final Map<String, dynamic>? urls;
  final double size;
  final double? letterSize;

  @override
  Widget build(BuildContext context) {
    final letters = ChatAvatar(name: name, size: size, letterSize: letterSize);
    final url = photoUrl(urls, width: (size * 2).round());
    if (url == null) return letters;
    return ClipOval(
      child: Image.network(
        url,
        width: size,
        height: size,
        fit: BoxFit.cover,
        excludeFromSemantics: true,
        gaplessPlayback: true,
        errorBuilder: (_, _, _) => letters,
        frameBuilder: (_, child, frame, sync) => sync || frame != null ? child : letters,
      ),
    );
  }
}

/// What a business has proved, in plain words for the head of its chat
/// (§15): "Phone checked · Payments through Taskiem · Trading 8 months".
String verifiedWords(PersonChatVerified? v) {
  if (v == null) return '';
  return [
    if (v.phone) 'Phone checked',
    if (v.business) 'Business checked',
    if (v.payments) 'Payments through Taskiem',
    switch (v.months) {
      <= 0 => 'New on Taskiem',
      1 => 'Trading 1 month',
      final n => 'Trading $n months',
    },
  ].join(' · ');
}

/// Where a message went (taskiem-for-everyone §11): on the business's own
/// WhatsApp number, or in the app.
String lineWords(String? line) => line == 'whatsapp' ? 'on WhatsApp' : 'in the app';

/// A bubble's mark of its line, before its time: "on WhatsApp ·" or "in the
/// app ·" (§11).
class LineMark extends StatelessWidget {
  const LineMark({super.key, required this.line, required this.color});
  final String? line;
  final Color color;

  @override
  Widget build(BuildContext context) => Semantics(
    label: line == 'whatsapp' ? 'On WhatsApp' : 'In the app',
    excludeSemantics: true,
    child: Padding(
      padding: const EdgeInsets.only(right: 4),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (line == 'whatsapp') ...[TkIcon('b-whatsapp', size: 12, stroke: 2, color: color), const SizedBox(width: 3)],
          Text('${lineWords(line)} ·', style: tkText(size: 11, lineHeight: 16 / 11, color: color)),
        ],
      ),
    ),
  );
}
