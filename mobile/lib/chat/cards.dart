// A document's card in a conversation (docs/spec/chat-tools.md §3), as the
// web draws it (web/src/pages/Conversation.tsx CardFrame, CardActions;
// kit.tsx DiaryLine; chat.css .tk-chatcard): the kind and reference, the
// state as a pill, what happened, the amount, an order's progress, and its
// buttons. A change we made ourselves is a diary line instead.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'words.dart';

/// `.tk-chatcard`: the frame, inside its bubble.
class CardFrame extends StatelessWidget {
  const CardFrame({super.key, required this.card, required this.title, required this.mine, required this.now, this.current, required this.actions});

  final LinkDocCard card;

  /// The document's newest card, when this one is older: its state shows.
  final LinkDocCard? current;
  final String title;
  final bool mine;
  final DateTime now;
  final List<Widget> actions;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final st = cardStatus(current ?? card, now);
    final steps = stepsFor(card);
    final sub = cardSub(card);
    final step = st.step;
    final children = <Widget>[
      // `.tk-chatcard-top`: the kicker, the pill at the end.
      Row(
        children: [
          Expanded(
            child: Text(
              cardKicker(card).toUpperCase(),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: tkText(size: 11, weight: 700, letterSpacing: 0.88, color: t.muted),
            ),
          ),
          const SizedBox(width: 8),
          CardPill(st.word, st.tone),
        ],
      ),
      Text(breakAfterHyphens(title), style: tkText(size: 15, weight: 600, color: t.text)),
      if (sub.isNotEmpty) Text(sub, style: tkText(size: 14, color: mine ? t.chatOutMeta : t.muted)),
      if (step != null)
        Semantics(
          label: '${steps[step]}: step ${step + 1} of ${steps.length}',
          excludeSemantics: true,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // `.tk-progress-bar`: four columns, 4 px apart, 5 px tall.
              Row(
                children: [
                  for (var i = 0; i < steps.length; i++) ...[
                    if (i > 0) const SizedBox(width: 4),
                    Expanded(
                      child: Container(
                        height: 5,
                        decoration: BoxDecoration(color: i <= step ? (dark ? t.brandDark : t.brand) : t.borderStrong, borderRadius: BorderRadius.circular(3)),
                      ),
                    ),
                  ],
                ],
              ),
              const SizedBox(height: 6),
              // `.tk-progress-words`: the same columns with no gap.
              Row(
                children: [
                  for (var i = 0; i < steps.length; i++)
                    Expanded(
                      child: Text(
                        steps[i],
                        style: tkText(size: 11.5, weight: i == step ? 600 : 400, color: i == step ? t.text : t.muted),
                      ),
                    ),
                ],
              ),
            ],
          ),
        ),
      if (actions.isNotEmpty) Row(children: _spaced(actions)),
    ];
    return Semantics(
      container: true,
      label: cardKicker(card),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          // `.tk-chatcard` on --tk-tile; in our own bubble the panel at 70%.
          color: mine ? cssMix(t.panel, const Color(0x00000000), 0.7) : t.tkTile,
          borderRadius: BorderRadius.circular(12),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (var i = 0; i < children.length; i++) ...[if (i > 0) const SizedBox(height: 8), children[i]],
          ],
        ),
      ),
    );
  }
}

/// `.tk-chatcard-actions`: the buttons share the row, 8 px apart.
List<Widget> _spaced(List<Widget> buttons) => [
  for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(width: 8), Expanded(child: buttons[i])],
];

/// kit.css `.tk-pill`: a state in a word.
class CardPill extends StatelessWidget {
  const CardPill(this.word, this.tone, {super.key});
  final String word;
  final CardTone tone;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final (bg, ink) = switch (tone) {
      CardTone.lime => (t.brandDark, t.brandDarkText),
      CardTone.warn => (t.tkWarnBg, t.tkWarnInk),
      CardTone.danger => (t.tkDangerBg, t.tkDangerInk),
      CardTone.plain => (t.tkSoft, t.text),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
      child: Text(word, maxLines: 1, style: tkText(size: 12, weight: 700, color: ink)),
    );
  }
}

/// A card's button (`.tk-chatcard-actions button.small`): a 40 px pill,
/// on the panel with a line round it, or the primary colour.
class CardButton extends StatelessWidget {
  const CardButton({super.key, required this.label, this.onTap, this.primary = false, this.busy = false, this.semantics});

  final String label;
  final VoidCallback? onTap;
  final bool primary;
  final bool busy;

  /// What a screen reader says (with the document's reference).
  final String? semantics;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    final bg = primary ? t.primary : t.panel;
    final line = primary ? t.primary : t.borderStrong;
    final ink = primary ? t.primaryText : (dark ? t.text : t.brand);
    return Semantics(
      button: true,
      enabled: !busy && onTap != null,
      label: semantics ?? label,
      excludeSemantics: true,
      child: TkPress(
        onTap: busy ? null : onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 40),
          padding: const EdgeInsets.symmetric(horizontal: 16),
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: bg,
            border: Border.all(color: line),
            borderRadius: BorderRadius.circular(999),
          ),
          child: Text(
            label,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: tkText(size: 14, weight: 700, lineHeight: 1.2, color: ink),
          ),
        ),
      ),
    );
  }
}

/// What a card's buttons do: a step taken from the card, as the web's
/// CardActions takes it, and the document opened.
class CardActs {
  CardActs({required this.auth, required this.onDone, this.onOpen});

  final Auth auth;

  /// After a step: what is newer is asked for.
  final Future<void> Function() onDone;

  /// A document of ours opened in the app (View order, View invoice, Pay).
  final void Function(LinkDocCard card)? onOpen;

  bool busy = false;
  String? error;

  Future<void> post(String path, [Map<String, dynamic>? body]) async {
    busy = true;
    error = null;
    try {
      await auth.transport.send('POST', path, body: body ?? const {});
      await onDone();
    } on ApiError catch (e) {
      error = e.message;
    } on Offline {
      error = 'You’re offline. Try again when you’re back online.';
    } finally {
      busy = false;
    }
  }
}

/// The card's own steps (Conversation.tsx CardActions): what the person may
/// do next from the card, by its document and event and their permissions.
List<Widget> cardActions(LinkDocCard card, CardActs acts, VoidCallback changed) {
  final can = acts.auth.can;
  final id = Uri.encodeComponent(card.id ?? card.reference);
  final ref = card.reference;
  Widget step(String label, String path, {bool primary = true, Map<String, dynamic>? body}) => CardButton(
    label: label,
    primary: primary,
    busy: acts.busy,
    semantics: '$label $ref',
    onTap: () async {
      final f = acts.post(path, body);
      changed();
      await f;
      changed();
    },
  );
  if (card.doc == 'delivery' && can('storefront.manage|order.handle')) {
    switch (card.event) {
      case 'offered':
        return [step('Accept', '/v1/trade-orders/$id/accept'), step('Can’t', '/v1/trade-orders/$id/cant', primary: false)];
      case 'accepted':
        return [step('Picked up', '/v1/trade-orders/$id/picked_up')];
      case 'picked_up':
        return [step('Arrived', '/v1/trade-orders/$id/arrived')];
      case 'arrived':
        return [step('Delivered', '/v1/trade-orders/$id/delivered')];
    }
  }
  if (card.doc == 'dispatch' && card.event == 'delivered' && can('storefront.manage')) {
    final r = Uri.encodeComponent(ref);
    return [
      step('Confirm, pay the fee', '/v1/storefront/orders/$r/confirm'),
      step('Paid in cash', '/v1/storefront/orders/$r/confirm', primary: false, body: {'how': 'cash'}),
    ];
  }
  switch ('${card.doc}:${card.event}') {
    case 'order:ordered':
      // Decline asks first (the web's confirmation): with the app's sheets.
      if (can('storefront.manage|order.handle')) return [step('Accept', '/v1/trade-orders/$id/accept')];
    case 'purchase:change':
      if (card.wasKobo == null && can('storefront.manage')) return [step('Accept the change', '/v1/stock/purchases/$id/accept-change')];
    case 'purchase:delivered':
      if (can('table.edit')) return [step('All came', '/v1/stock/purchases/$id/all-came')];
    case 'bill:bill':
      if (can('invoice.manage')) {
        return [
          CardButton(
            label: 'Pay${card.amountKobo != null ? ' ${naira(card.amountKobo!)}' : ''}',
            primary: true,
            semantics: 'Pay $ref',
            onTap: acts.onOpen == null ? null : () => acts.onOpen!(card),
          ),
        ];
      }
  }
  return const [];
}

/// The card's own button to its document: View order, View invoice, View delivery.
Widget viewButton(LinkDocCard card, CardActs acts) {
  final label = isDelivery(card)
      ? 'View delivery'
      : card.doc == 'order' || card.doc == 'purchase'
      ? 'View order'
      : 'View invoice';
  return CardButton(label: label, semantics: '$label ${card.reference}', onTap: acts.onOpen == null ? null : () => acts.onOpen!(card));
}

/// kit.tsx DiaryLine: a change we made ourselves, told as a line in a
/// soft pill, with a tick; "and 2 earlier changes" unfolds the rest.
class DiaryLine extends StatelessWidget {
  const DiaryLine({super.key, required this.words, this.sub, required this.when, this.more, this.onTap});

  final String words;
  final String? sub;

  /// "You, 10:42": said, not drawn.
  final String when;
  final Widget? more;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final arrow = words.lastIndexOf(' → ');
    final spans = <InlineSpan>[
      if (arrow >= 0) ...[
        TextSpan(text: words.substring(0, arrow + 3)),
        TextSpan(
          text: words.substring(arrow + 3),
          style: const TextStyle(fontWeight: FontWeight.w700, fontVariations: [FontVariation('wght', 700)]),
        ),
      ] else
        TextSpan(text: words),
      if (sub != null && sub!.isNotEmpty)
        TextSpan(
          text: ' $sub',
          style: TextStyle(color: t.muted),
        ),
    ];
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: LayoutBuilder(
        builder: (context, box) => Center(
          child: ConstrainedBox(
            // `.tk-diary { max-width: 92% }`
            constraints: BoxConstraints(maxWidth: box.maxWidth * 0.92),
            child: Column(
              children: [
                Semantics(
                  label: when,
                  child: TkPress(
                    onTap: onTap,
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
                      decoration: BoxDecoration(
                        color: t.panel,
                        borderRadius: BorderRadius.circular(999),
                        boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 2), blurRadius: 8)],
                      ),
                      // `display: inline-flex; flex-wrap: wrap; justify-content:
                      // center; gap: 4px 8px`: words too long to sit beside the
                      // tick put it on a line of its own, above them.
                      child: Wrap(
                        alignment: WrapAlignment.center,
                        crossAxisAlignment: WrapCrossAlignment.center,
                        spacing: 8,
                        runSpacing: 4,
                        children: [
                          TkIcon('tick', size: 16, color: t.ok),
                          Text.rich(TextSpan(children: spans), style: tkText(size: 13, color: t.text)),
                        ],
                      ),
                    ),
                  ),
                ),
                if (more != null) Padding(padding: const EdgeInsets.only(top: 4), child: more),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
