// A document's card in a conversation (docs/spec/chat-tools.md §3), as the
// web draws it (web/src/pages/Conversation.tsx CardFrame, CardActions;
// kit.tsx DiaryLine; chat.css .tk-chatcard): the kind and reference, the
// state as a pill, what happened, the amount, an order's progress, and its
// buttons. A change we made ourselves is a diary line instead.

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/motion.dart';
import '../kit/tap_area.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'deal.dart';
import 'words.dart';

/// `.tk-chatcard`: the frame, inside its bubble.
class CardFrame extends StatelessWidget {
  const CardFrame({super.key, required this.card, required this.title, required this.mine, required this.now, this.current, this.view, required this.actions, this.deal});

  final LinkDocCard card;

  /// The document's newest card, when this one is older: its state shows.
  final LinkDocCard? current;
  final String title;
  final bool mine;
  final DateTime now;
  final List<Widget> actions;

  /// The card's own button to its document (View order, View invoice): on a
  /// row of its own under [actions], the whole width (`.tk-chatcard-actions .view`).
  final Widget? view;

  /// A deal's answers (ChatDeal.tsx DealActions), under the buttons.
  final Widget? deal;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final st = cardStatus(current ?? card, now);
    final steps = stepsFor(card);
    final step = st.step;
    final amount = card.amountKobo;
    // The words under the amount: what happened, then what was and the note.
    final line = [
      if (amount != null) breakAfterHyphens(title),
      if (card.wasKobo != null && card.wasKobo! > (amount ?? 0)) 'was ${naira(card.wasKobo!)}',
      if ((card.note ?? '').isNotEmpty) '“${card.note}”',
    ].join(' · ');
    final children = <Widget>[
      // The kind and reference small, the state as a capsule at the end.
      Row(
        children: [
          Expanded(
            child: Text(
              cardKicker(card).toUpperCase(),
              semanticsLabel: cardKicker(card),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: tkText(size: 11, weight: 700, letterSpacing: 0.66, color: t.muted),
            ),
          ),
          const SizedBox(width: 8),
          CardPill(st.word, st.tone),
        ],
      ),
      // The amount large (Inter Tight), else what happened.
      if (amount != null)
        Text(naira(amount), style: tkDisplay(size: 26, lineHeight: 1.15, letterSpacing: -0.9, color: t.text))
      else
        Text(breakAfterHyphens(title), style: tkText(size: 15.5, weight: 600, color: t.text)),
      if (line.isNotEmpty) Text(line, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
      if (card.deal != null) DealHistory(card: card, now: now),
      if (step != null) Padding(padding: const EdgeInsets.only(top: 4), child: CardProgress(steps: steps, step: step)),
      if (actions.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 4), child: CardActionRows(children: actions)),
      // `.tk-deal-form { margin-top: 6px }`, on a line of its own.
      if (deal != null) Padding(padding: const EdgeInsets.only(top: 6), child: deal),
      // Last: its document, as a quiet row.
      ?view,
    ];
    return Semantics(
      container: true,
      label: cardKicker(card),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (var i = 0; i < children.length; i++) ...[if (i > 0) SizedBox(height: i == 1 ? 6 : 4), children[i]],
        ],
      ),
    );
  }
}

/// The card's frame on the conversation: its own message, not a bubble's
/// box (docs/spec/mobile-app-look.md, "Cards in a conversation"): the card
/// colour, rounder than a bubble, the sender's corner tight, a soft lift.
BoxDecoration cardDecoration(BuildContext context, {required bool mine, required bool first}) {
  final look = TkLook.of(context);
  final dark = TkTheme.of(context).mode == TkMode.dark;
  return BoxDecoration(
    color: look.card,
    borderRadius: BorderRadius.only(
      topLeft: Radius.circular(first && !mine ? 6 : 20),
      topRight: Radius.circular(first && mine ? 6 : 20),
      bottomLeft: const Radius.circular(20),
      bottomRight: const Radius.circular(20),
    ),
    border: dark ? Border.all(color: look.line) : null,
    boxShadow: dark
        ? null
        : const [
            BoxShadow(color: Color(0x14000000), offset: Offset(0, 1), blurRadius: 2),
            BoxShadow(color: Color(0x1F000000), offset: Offset(0, 8), blurRadius: 24, spreadRadius: -16),
          ],
  );
}

/// `.tk-chatcard-actions` (flex-wrap, each button `flex: 1` and its word on
/// one line): the buttons share a row, 8 px apart, as wide as each other
/// where they can be; one that does not fit beside the others goes to the
/// next row, rather than its word being cut or broken.
class CardActionRows extends MultiChildRenderObjectWidget {
  const CardActionRows({super.key, required super.children});

  @override
  RenderObject createRenderObject(BuildContext context) => _RenderActionRows();
}

class _ActionRowsData extends ContainerBoxParentData<RenderBox> {}

class _RenderActionRows extends RenderBox
    with ContainerRenderObjectMixin<RenderBox, _ActionRowsData>, RenderBoxContainerDefaultsMixin<RenderBox, _ActionRowsData> {
  static const gap = 8.0;

  @override
  void setupParentData(RenderBox child) {
    if (child.parentData is! _ActionRowsData) child.parentData = _ActionRowsData();
  }

  List<RenderBox> get _kids {
    final out = <RenderBox>[];
    var c = firstChild;
    while (c != null) {
      out.add(c);
      c = childAfter(c);
    }
    return out;
  }

  /// The children in rows, each with the width it needs.
  List<List<(RenderBox, double)>> _rows(double width) {
    final rows = <List<(RenderBox, double)>>[];
    var row = <(RenderBox, double)>[];
    var used = 0.0;
    for (final c in _kids) {
      final need = c.getMaxIntrinsicWidth(double.infinity).clamp(0.0, width);
      if (row.isNotEmpty && used + gap + need > width) {
        rows.add(row);
        row = [];
        used = 0;
      }
      used += (row.isEmpty ? 0 : gap) + need;
      row.add((c, need));
    }
    if (row.isNotEmpty) rows.add(row);
    return rows;
  }

  /// `flex: 1` from nothing: as wide as each other, and none narrower than
  /// its word.
  static List<double> _widths(List<(RenderBox, double)> row, double width) {
    final room = width - gap * (row.length - 1);
    final even = room / row.length;
    if (row.every((r) => r.$2 <= even)) return [for (final _ in row) even];
    final extra = (room - row.fold(0.0, (a, r) => a + r.$2)) / row.length;
    return [for (final r in row) r.$2 + extra];
  }

  @override
  void performLayout() {
    final width = constraints.maxWidth;
    var y = 0.0;
    for (final row in _rows(width)) {
      final ws = _widths(row, width);
      var h = 0.0;
      for (var i = 0; i < row.length; i++) {
        row[i].$1.layout(BoxConstraints.tightFor(width: ws[i]), parentUsesSize: true);
        h = h > row[i].$1.size.height ? h : row[i].$1.size.height;
      }
      var x = 0.0;
      for (var i = 0; i < row.length; i++) {
        final c = row[i].$1;
        (c.parentData! as _ActionRowsData).offset = Offset(x, y + (h - c.size.height) / 2);
        x += ws[i] + gap;
      }
      y += h + gap;
    }
    size = constraints.constrain(Size(width, y > 0 ? y - gap : 0));
  }

  @override
  double computeMinIntrinsicWidth(double height) => _kids.fold(0.0, (a, c) => a > c.getMinIntrinsicWidth(height) ? a : c.getMinIntrinsicWidth(height));

  @override
  double computeMaxIntrinsicWidth(double height) {
    final k = _kids;
    return k.fold(0.0, (a, c) => a + c.getMaxIntrinsicWidth(height)) + gap * (k.isEmpty ? 0 : k.length - 1);
  }

  @override
  double computeMinIntrinsicHeight(double width) => computeMaxIntrinsicHeight(width);

  @override
  double computeMaxIntrinsicHeight(double width) {
    var y = 0.0;
    for (final row in _rows(width)) {
      final ws = _widths(row, width);
      var h = 0.0;
      for (var i = 0; i < row.length; i++) {
        final ch = row[i].$1.getMaxIntrinsicHeight(ws[i]);
        h = h > ch ? h : ch;
      }
      y += h + gap;
    }
    return y > 0 ? y - gap : 0;
  }

  @override
  bool hitTestChildren(BoxHitTestResult result, {required Offset position}) => defaultHitTestChildren(result, position: position);

  @override
  void paint(PaintingContext context, Offset offset) => defaultPaint(context, offset);
}

/// kit.css `.tk-pill`: a state in a word.
class CardPill extends StatelessWidget {
  const CardPill(this.word, this.tone, {super.key});
  final String word;
  final CardTone tone;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final (bg, ink) = switch (tone) {
      CardTone.lime => (look.lime.withValues(alpha: 0.18), look.limeInk),
      CardTone.warn => (look.orange.withValues(alpha: 0.16), look.orangeInk),
      CardTone.danger => (look.red.withValues(alpha: 0.16), look.redInk),
      CardTone.plain => (t.tkSoft, t.muted),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
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
    final look = TkLook.of(context);
    // The main step pine (lime in the dark); the others on the ground.
    final bg = primary ? look.on : look.ground;
    // On your own pine bubble the main step is lime, so its words are pine.
    final ink = primary ? (dark ? t.brandDarkText : (look.onPine ? const Color(0xFF0D3B2E) : const Color(0xFFFFFFFF))) : t.text;
    return TapArea(child: Semantics(
      button: true,
      enabled: !busy && onTap != null,
      label: semantics ?? label,
      onTap: busy ? null : onTap,
      excludeSemantics: true,
      child: TkPress(
        onTap: busy ? null : onTap,
        child: Container(
          constraints: const BoxConstraints(minHeight: 40),
          padding: const EdgeInsets.symmetric(horizontal: 16),
          alignment: Alignment.center,
          decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
          child: Text(
            label,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: tkText(size: 14, weight: 700, lineHeight: 1.2, color: ink),
          ),
        ),
      ),
    ));
  }
}

/// What a card's buttons do: a step taken from the card, as the web's
/// CardActions takes it, and the document opened.
class CardActs {
  CardActs({required this.auth, required this.onDone, this.onOpen, this.ask, this.said});

  final Auth auth;

  /// After a step: what is newer is asked for.
  final Future<void> Function() onDone;

  /// A document of ours opened in the app (View order, View invoice, Pay).
  final void Function(LinkDocCard card)? onOpen;

  /// Asks first (the web's confirmation): true to go on.
  final Future<bool> Function(String title, String ok)? ask;

  /// Says a step is done (the web's toast).
  final void Function(String words)? said;

  bool busy = false;
  String? error;

  /// A step sent; [done] said once it is.
  Future<void> post(String path, [Map<String, dynamic>? body, String? done]) async {
    busy = true;
    error = null;
    try {
      await auth.transport.send('POST', path, body: body ?? const {});
      if (done != null) said?.call(done);
      // Done: what is newer is asked for, and its own failure is the
      // conversation's to say, not the step's.
      try {
        await onDone();
      } on Object {
        // Asked again on the next turn.
      }
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
  // Each says what it did, as the web's do; Decline asks first.
  Widget step(String label, String path, String done, {bool primary = true, Map<String, dynamic>? body, String? askFirst}) => CardButton(
    label: label,
    primary: primary,
    busy: acts.busy,
    semantics: '$label $ref',
    onTap: () async {
      if (askFirst != null && !(await acts.ask?.call(askFirst, label) ?? true)) return;
      final f = acts.post(path, body, done);
      changed();
      await f;
      changed();
    },
  );
  if (card.doc == 'delivery' && can('storefront.manage|order.handle')) {
    switch (card.event) {
      case 'offered':
        return [step('Accept', '/v1/trade-orders/$id/accept', 'Accept'), step('Can’t', '/v1/trade-orders/$id/cant', 'Passed on $ref', primary: false)];
      case 'accepted':
        return [step('Picked up', '/v1/trade-orders/$id/picked_up', 'Picked up')];
      case 'picked_up':
        return [step('Arrived', '/v1/trade-orders/$id/arrived', 'Arrived')];
      case 'arrived':
        return [step('Delivered', '/v1/trade-orders/$id/delivered', 'Delivered')];
    }
  }
  if (card.doc == 'dispatch' && card.event == 'delivered' && can('storefront.manage')) {
    final r = Uri.encodeComponent(ref);
    return [
      step('Confirm, pay the fee', '/v1/storefront/orders/$r/confirm', 'Confirmed $ref'),
      step('Paid in cash', '/v1/storefront/orders/$r/confirm', '$ref paid in cash', primary: false, body: {'how': 'cash'}),
    ];
  }
  switch ('${card.doc}:${card.event}') {
    case 'order:ordered':
      if (can('storefront.manage|order.handle')) {
        return [
          step('Accept', '/v1/trade-orders/$id/accept', '$ref accepted'),
          step('Decline', '/v1/trade-orders/$id/decline', '$ref declined', primary: false, askFirst: 'Decline $ref?'),
        ];
      }
    case 'purchase:change':
      if (card.wasKobo == null && can('storefront.manage')) return [step('Accept the change', '/v1/stock/purchases/$id/accept-change', 'Change accepted')];
    case 'purchase:delivered':
      if (can('table.edit')) return [step('All came', '/v1/stock/purchases/$id/all-came', 'Received, and your stock is up')];
    case 'invoice:queried':
      // The buyer queried our invoice: settled on its screen.
      if (can('invoice.manage')) {
        return [CardButton(label: 'Settle the query', primary: true, semantics: 'Settle the query $ref', onTap: acts.onOpen == null ? null : () => acts.onOpen!(card))];
      }
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
  return CardViewRow(label: label, semantics: '$label ${card.reference}', onTap: acts.onOpen == null ? null : () => acts.onOpen!(card));
}

/// A card's way to its document, at its foot: a hairline, the words, a
/// chevron at the end.
class CardViewRow extends StatelessWidget {
  const CardViewRow({super.key, required this.label, required this.semantics, this.onTap});
  final String label;
  final String semantics;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return TapArea(
      child: Semantics(
        button: true,
        enabled: onTap != null,
        label: semantics,
        onTap: onTap,
        excludeSemantics: true,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: onTap,
          child: Container(
            margin: const EdgeInsets.only(top: 6),
            padding: const EdgeInsets.only(top: 10, bottom: 2),
            decoration: BoxDecoration(border: Border(top: BorderSide(color: TkLook.of(context).line))),
            child: Row(
              children: [
                Expanded(child: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 14, weight: 600, color: t.text))),
                TkIcon('chevron', size: 16, color: t.muted),
              ],
            ),
          ),
        ),
      ),
    );
  }
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
    final fresh = TkFresh.of(context);
    final arrow = words.lastIndexOf(' → ');
    final spans = <InlineSpan>[
      if (arrow >= 0) ...[
        TextSpan(text: words.substring(0, arrow + 3)),
        // kit.tsx: a fresh line's new value `.tk-roll`s into place.
        if (!fresh)
          TextSpan(
            text: words.substring(arrow + 3),
            style: const TextStyle(fontWeight: FontWeight.w700, fontVariations: [FontVariation('wght', 700)]),
          )
        else
        WidgetSpan(
          alignment: PlaceholderAlignment.baseline,
          baseline: TextBaseline.alphabetic,
          child: TkRoll(
            value: words,
            child: Text(
              words.substring(arrow + 3),
              semanticsLabel: words.substring(arrow + 3),
              style: tkText(size: 13, weight: 700, color: t.text),
            ),
          ),
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
                    // kit.tsx: a fresh line `.tk-land`s, lit in lime.
                    child: TkLand(
                      fresh: fresh,
                      decoration: BoxDecoration(
                        color: t.panel,
                        borderRadius: BorderRadius.circular(999),
                        boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 2), blurRadius: 8)],
                      ),
                      child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
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

/// chat.css `.tk-progress`: the steps as a bar of [steps] columns, the one
/// it is at in words under it.
class CardProgress extends StatelessWidget {
  const CardProgress({super.key, required this.steps, required this.step});
  final List<String> steps;
  final int step;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    // The steps as dots on a line: those done (and the one it is at) filled
    // lime, the rest rings; the word under each, the one it is at bold.
    return Semantics(
      label: '${steps[step]}: step ${step + 1} of ${steps.length}',
      excludeSemantics: true,
      child: Stack(
        children: [
          Positioned(
            left: 0,
            right: 0,
            top: 5,
            child: FractionallySizedBox(widthFactor: 1 - 1 / steps.length, child: Container(height: 2, color: look.line)),
          ),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              for (var i = 0; i < steps.length; i++)
                Expanded(
                  child: Column(
                    children: [
                      Container(
                        width: 12,
                        height: 12,
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          color: i <= step ? look.lime : look.card,
                          border: i <= step ? null : Border.all(color: t.muted, width: 2),
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        steps[i],
                        textAlign: TextAlign.center,
                        style: tkText(size: 10.5, weight: i == step ? 700 : 500, lineHeight: 1.2, color: i <= step ? t.text : t.muted),
                      ),
                    ],
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}
